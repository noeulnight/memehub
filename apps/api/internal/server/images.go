package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/noeulnight/memehub/apps/api/internal/preview"
	"github.com/noeulnight/memehub/apps/api/internal/storage"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

func (s *Server) validateImage(in *memeInput) string {
	if in.BodyText != "" || strings.TrimSpace(in.OriginalFilename) == "" || !utf8.ValidString(in.OriginalFilename) || utf8.RuneCountInString(in.OriginalFilename) > 255 || strings.ContainsAny(in.OriginalFilename, "\x00/\\") {
		return "invalid_original_filename"
	}
	if in.SizeBytes < 1 || in.SizeBytes > s.cfg.UploadMaxBytes {
		return "invalid_image_size"
	}
	if !slices.Contains(s.cfg.UploadAllowedMIMEs, in.MIMEType) {
		return "unsupported_image_type"
	}
	hash, err := hex.DecodeString(in.SHA256)
	if err != nil || len(hash) != 32 || strings.ToLower(in.SHA256) != in.SHA256 {
		return "invalid_sha256"
	}
	return ""
}
func stagingKey(id uuid.UUID) string { return "staging/" + id.String() }
func (s *Server) reserveImage(w http.ResponseWriter, r *http.Request, sess session, in memeInput) {
	if s.store == nil {
		problem(w, 503, "storage_unavailable")
		return
	}
	id := uuid.New()
	upload, err := s.store.UploadURL(r.Context(), stagingKey(id), in.MIMEType, in.SizeBytes)
	if err != nil {
		problem(w, 502, "storage_unavailable")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		internal(w)
		return
	}
	defer tx.Rollback(r.Context())
	if !in.AllowDuplicate {
		found, e := duplicateImage(r.Context(), tx, w, in.SHA256, id)
		if e != nil {
			internal(w)
			return
		}
		if found {
			return
		}
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO memes(id,created_by,content_type,title,description,source_url,source_name,source_author,source_note,original_filename,original_object_key,mime_type,size_bytes,sha256,status,allow_duplicate)
 VALUES($1,$2,'image',$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'uploading',$14)`, id, sess.UserID, in.Title, in.Description, nullable(in.SourceURL), nullable(in.SourceName), nullable(in.SourceAuthor), nullable(in.SourceNote), in.OriginalFilename, "originals/"+id.String(), in.MIMEType, in.SizeBytes, in.SHA256, in.AllowDuplicate)
	if err != nil {
		internal(w)
		return
	}
	if err = saveTags(r.Context(), tx, id, in.Tags); err != nil {
		internal(w)
		return
	}
	m, err := scanMeme(tx.QueryRow(r.Context(), "SELECT "+memeColumns+" FROM memes m WHERE m.id=$1", id))
	if err != nil {
		internal(w)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		internal(w)
		return
	}
	w.Header().Set("Location", "/api/memes/"+id.String())
	writeJSON(w, 201, struct {
		Meme
		Upload storage.Upload `json:"upload"`
	}{m, upload})
}
func (s *Server) renewUpload(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticate(w, r, true)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "invalid_meme_id")
		return
	}
	var state, mime string
	var size int64
	err = s.db.QueryRow(r.Context(), "UPDATE memes SET updated_at=now() WHERE id=$1 AND created_by=$2 AND content_type='image' AND deleted_at IS NULL AND objects_deleted_at IS NULL RETURNING status,mime_type,size_bytes", id, sess.UserID).Scan(&state, &mime, &size)
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "meme_not_found")
		return
	}
	if err != nil {
		internal(w)
		return
	}
	if state != "uploading" {
		problem(w, 409, "upload_already_completed")
		return
	}
	if s.store == nil {
		problem(w, 503, "storage_unavailable")
		return
	}
	upload, err := s.store.UploadURL(r.Context(), stagingKey(id), mime, size)
	if err != nil {
		problem(w, 502, "storage_unavailable")
		return
	}
	writeJSON(w, 200, upload)
}

// Decode only bounded raster images; declarations never determine the decoded format.
func prepareImage(data []byte, mime, hash string, size int64) (int, int, []byte, error) {
	sum := sha256.Sum256(data)
	if int64(len(data)) != size || hex.EncodeToString(sum[:]) != hash {
		return 0, 0, nil, errors.New("image_integrity_mismatch")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || "image/"+format != mime || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 20_000_000 {
		return 0, 0, nil, errors.New("invalid_image")
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return 0, 0, nil, errors.New("invalid_image")
	}
	width, height := cfg.Width, cfg.Height
	if width > 640 || height > 640 {
		if width >= height {
			height = max(1, height*640/width)
			width = 640
		} else {
			width = max(1, width*640/height)
			height = 640
		}
	}
	thumb := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.ApproxBiLinear.Scale(thumb, thumb.Bounds(), decoded, decoded.Bounds(), draw.Src, nil)
	var out bytes.Buffer
	if err = png.Encode(&out, thumb); err != nil {
		return 0, 0, nil, err
	}
	return cfg.Width, cfg.Height, out.Bytes(), nil
}
func (s *Server) completeUpload(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticate(w, r, true)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "invalid_meme_id")
		return
	}
	if s.store == nil {
		problem(w, 503, "storage_unavailable")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		internal(w)
		return
	}
	defer tx.Rollback(r.Context())
	var state, mime, hash, key string
	var size int64
	var allowDuplicate bool
	var input struct {
		AllowDuplicate bool `json:"allow_duplicate"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &input) {
		return
	}
	err = tx.QueryRow(r.Context(), `SELECT status,mime_type,sha256,size_bytes,original_object_key,allow_duplicate FROM memes WHERE id=$1 AND created_by=$2 AND content_type='image' AND deleted_at IS NULL FOR UPDATE`, id, sess.UserID).Scan(&state, &mime, &hash, &size, &key, &allowDuplicate)
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "meme_not_found")
		return
	}
	if err != nil {
		internal(w)
		return
	}
	if state != "uploading" && state != "ready" {
		problem(w, 409, "invalid_upload_state")
		return
	}
	if state == "uploading" {
		body, e := s.store.Read(r.Context(), stagingKey(id))
		if e != nil {
			problem(w, 422, "uploaded_image_unavailable")
			return
		}
		data, e := io.ReadAll(io.LimitReader(body, s.cfg.UploadMaxBytes+1))
		body.Close()
		if e != nil {
			problem(w, 502, "storage_unavailable")
			return
		}
		if int64(len(data)) > s.cfg.UploadMaxBytes {
			problem(w, 422, "invalid_image_size")
			return
		}
		width, height, thumbnail, e := prepareImage(data, mime, hash, size)
		if e != nil {
			problem(w, 422, e.Error())
			return
		}
		// Read staging once and write the verified bytes to a key never signed for upload.
		if _, e = tx.Exec(r.Context(), "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", hash); e != nil {
			internal(w)
			return
		}
		if !allowDuplicate && !input.AllowDuplicate {
			found, e := duplicateImage(r.Context(), tx, w, hash, id)
			if e != nil {
				internal(w)
				return
			}
			if found {
				return
			}
		}
		thumbKey := "thumbnails/" + id.String() + ".png"
		if e = s.store.Put(r.Context(), key, mime, data); e != nil {
			problem(w, 502, "storage_unavailable")
			return
		}
		if e = s.store.Put(r.Context(), thumbKey, "image/png", thumbnail); e != nil {
			problem(w, 502, "storage_unavailable")
			return
		}
		var previewKey *string
		if mime == "image/gif" {
			animated, e := preview.Animated(r.Context(), data)
			if e != nil {
				problem(w, 502, "preview_generation_failed")
				return
			}
			value := "previews/" + id.String() + ".webp"
			if e = s.store.Put(r.Context(), value, "image/webp", animated); e != nil {
				problem(w, 502, "storage_unavailable")
				return
			}
			previewKey = &value
		}
		analysisStatus := "queued"
		if s.cfg.DemoMode {
			analysisStatus = "not_requested"
		}
		_, err = tx.Exec(r.Context(), `UPDATE memes SET width=$2,height=$3,thumbnail_object_key=$4,preview_object_key=$5,status='ready',analysis_status=$6,updated_at=now() WHERE id=$1`, id, width, height, thumbKey, previewKey, analysisStatus)
		if err != nil {
			internal(w)
			return
		}
		if !s.cfg.DemoMode {
			_, err = tx.Exec(r.Context(), `INSERT INTO jobs(id,meme_id,type,input_revision,provider,requested_model,prompt_version) VALUES($1,$2,'analyze_content',0,'openai',COALESCE((SELECT openai_model FROM admin_settings WHERE singleton),$3),'v1')`, uuid.New(), id, s.cfg.OpenAIModel)
			if err != nil {
				internal(w)
				return
			}
		}
	}
	m, err := scanMeme(tx.QueryRow(r.Context(), "SELECT "+memeColumns+" FROM memes m WHERE m.id=$1", id))
	if err != nil {
		internal(w)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		internal(w)
		return
	}
	// A failed staging cleanup does not invalidate the committed original.
	_ = s.store.Delete(r.Context(), stagingKey(id))
	writeJSON(w, 200, m)
}
func (s *Server) media(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "invalid_meme_id")
		return
	}
	var original, thumbnail string
	// Older GIFs use their original until an animated WebP preview is available.
	err = s.db.QueryRow(r.Context(), `SELECT original_object_key,COALESCE(preview_object_key,CASE WHEN mime_type='image/gif' THEN original_object_key ELSE thumbnail_object_key END) FROM memes WHERE id=$1 AND content_type='image' AND status='ready' AND deleted_at IS NULL`, id).Scan(&original, &thumbnail)
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "meme_not_found")
		return
	}
	if err != nil {
		internal(w)
		return
	}
	if s.store == nil {
		problem(w, 503, "storage_unavailable")
		return
	}
	first, err := s.store.ReadURL(r.Context(), original)
	if err != nil {
		problem(w, 502, "storage_unavailable")
		return
	}
	second, err := s.store.ReadURL(r.Context(), thumbnail)
	if err != nil {
		problem(w, 502, "storage_unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"original": first, "thumbnail": second})
}

// Public image URLs never expose storage credentials or signed URLs.
func (s *Server) originalMedia(w http.ResponseWriter, r *http.Request) {
	s.publicImage(w, r, false)
}
func (s *Server) ogImage(w http.ResponseWriter, r *http.Request) {
	s.publicImage(w, r, true)
}
func (s *Server) publicImage(w http.ResponseWriter, r *http.Request, thumbnail bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "invalid_meme_id")
		return
	}
	var key, mime, hash string
	var thumb *string
	var size int64
	err = s.db.QueryRow(r.Context(), `SELECT original_object_key,thumbnail_object_key,mime_type,size_bytes,sha256 FROM memes WHERE id=$1 AND content_type='image' AND status='ready' AND deleted_at IS NULL`, id).Scan(&key, &thumb, &mime, &size, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "meme_not_found")
		return
	}
	if err != nil {
		internal(w)
		return
	}
	etag := `"` + hash + `"`
	if thumbnail {
		if thumb == nil || *thumb == "" {
			problem(w, 404, "preview_not_found")
			return
		}
		key = *thumb
		mime = "image/png"
		size = 0 // Thumbnail length is not stored; stream it without an original-file length.
		// Bump the rendition version if thumbnail encoding changes.
		sum := sha256.Sum256([]byte(hash + "\n" + key + "\nog-png-v1"))
		etag = `W/"` + hex.EncodeToString(sum[:]) + `"`
	}
	if s.store == nil {
		problem(w, 503, "storage_unavailable")
		return
	}
	setHeaders := func() {
		// A short freshness lifetime bounds stale copies after deletion, including at a CDN.
		w.Header().Set("Cache-Control", "public, max-age=60, must-revalidate")
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Content-Disposition", "inline")
	}
	for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == strings.TrimPrefix(etag, "W/") {
			setHeaders()
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	if r.Method == http.MethodHead {
		setHeaders()
		if size > 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		}
		return
	}
	body, err := s.store.Read(r.Context(), key)
	if err != nil {
		problem(w, 502, "storage_unavailable")
		return
	}
	defer body.Close()
	setHeaders()
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	_, _ = io.Copy(w, body)
}
