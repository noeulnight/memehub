package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/noeulnight/memehub/apps/api/internal/tag"
)

type memeInput struct {
	ContentType      string   `json:"content_type"`
	OriginalFilename string   `json:"original_filename"`
	MIMEType         string   `json:"mime_type"`
	SizeBytes        int64    `json:"size_bytes"`
	SHA256           string   `json:"sha256"`
	AllowDuplicate   bool     `json:"allow_duplicate"`
	Title            string   `json:"title"`
	BodyText         string   `json:"body_text"`
	Description      string   `json:"description"`
	ExtractedText    string   `json:"extracted_text"`
	SourceURL        string   `json:"source_url"`
	SourceName       string   `json:"source_name"`
	SourceAuthor     string   `json:"source_author"`
	SourceNote       string   `json:"source_note"`
	Tags             []string `json:"tags"`
	Revision         *int64   `json:"content_revision"`
}
type Meme struct {
	ID                uuid.UUID `json:"id"`
	CreatedBy         uuid.UUID `json:"created_by"`
	CreatedByName     *string   `json:"created_by_name"`
	CreatedByUsername *string   `json:"created_by_username"`
	ContentType       string    `json:"content_type"`
	Title             string    `json:"title"`
	BodyText          *string   `json:"body_text"`
	Description       string    `json:"description"`
	ExtractedText     string    `json:"extracted_text"`
	SourceURL         *string   `json:"source_url"`
	SourceName        *string   `json:"source_name"`
	SourceAuthor      *string   `json:"source_author"`
	SourceNote        *string   `json:"source_note"`
	Status            string    `json:"status"`
	AnalysisStatus    string    `json:"analysis_status"`
	Width             *int      `json:"width"`
	Height            *int      `json:"height"`
	Revision          int64     `json:"content_revision"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	Tags              []string  `json:"tags"`
}

const memeColumns = `m.id,m.created_by,m.content_type,m.title,m.body_text,m.description,m.extracted_text,m.source_url,m.source_name,m.source_author,m.source_note,m.status,m.analysis_status,m.content_revision,m.created_at,m.updated_at,
 ARRAY(SELECT t.name FROM tags t JOIN meme_tags mt ON mt.tag_id=t.id WHERE mt.meme_id=m.id ORDER BY t.normalized_name),
 (SELECT u.display_name FROM users u WHERE u.id=m.created_by),
 (SELECT u.username FROM users u WHERE u.id=m.created_by),m.width,m.height`

func scanMeme(row pgx.Row) (Meme, error) {
	var m Meme
	err := row.Scan(&m.ID, &m.CreatedBy, &m.ContentType, &m.Title, &m.BodyText, &m.Description, &m.ExtractedText, &m.SourceURL, &m.SourceName, &m.SourceAuthor, &m.SourceNote, &m.Status, &m.AnalysisStatus, &m.Revision, &m.CreatedAt, &m.UpdatedAt, &m.Tags, &m.CreatedByName, &m.CreatedByUsername, &m.Width, &m.Height)
	return m, err
}
func nullable(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func normalizeTag(value string) string { return tag.Key(value) }

func (s *Server) validateMeme(in *memeInput) string {
	if in.ContentType != "text" && in.ContentType != "image" {
		return "invalid_content_type"
	}
	if in.ContentType == "text" && (!utf8.ValidString(in.BodyText) || strings.ContainsRune(in.BodyText, 0) || strings.TrimSpace(in.BodyText) == "" || utf8.RuneCountInString(in.BodyText) > s.cfg.TextMaxLength) {
		return "invalid_body_text"
	}
	for _, item := range []struct {
		value string
		limit int
	}{{in.Title, 200}, {in.ExtractedText, 10000}, {in.Description, 10000}, {in.SourceURL, 2048}, {in.SourceName, 200}, {in.SourceAuthor, 200}, {in.SourceNote, 2000}} {
		if !utf8.ValidString(item.value) || strings.ContainsRune(item.value, 0) || utf8.RuneCountInString(item.value) > item.limit {
			return "invalid_field_length"
		}
	}
	in.Title = strings.TrimSpace(in.Title)
	in.SourceURL = strings.TrimSpace(in.SourceURL)
	in.SourceName = strings.TrimSpace(in.SourceName)
	in.SourceAuthor = strings.TrimSpace(in.SourceAuthor)
	in.SourceNote = strings.TrimSpace(in.SourceNote)
	if in.SourceURL != "" {
		u, err := url.Parse(in.SourceURL)
		if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return "invalid_source_url"
		}
	}
	if len(in.Tags) > 30 {
		return "too_many_tags"
	}
	seen := map[string]bool{}
	cleaned := []string{}
	for _, name := range in.Tags {
		tag := tag.Name(name)
		if tag == "" || !utf8.ValidString(tag) || strings.ContainsRune(tag, 0) || utf8.RuneCountInString(tag) > 50 {
			return "invalid_tag"
		}
		key := normalizeTag(tag)
		if !seen[key] {
			cleaned = append(cleaned, tag)
			seen[key] = true
		}
	}
	in.Tags = cleaned
	return ""
}
func saveTags(ctx context.Context, tx pgx.Tx, memeID uuid.UUID, tags []string) error {
	if _, err := tx.Exec(ctx, "DELETE FROM meme_tags WHERE meme_id=$1", memeID); err != nil {
		return err
	}
	for _, name := range tags {
		var tagID uuid.UUID
		err := tx.QueryRow(ctx, `INSERT INTO tags(id,name,normalized_name) VALUES($1,$2,$3)
   ON CONFLICT(normalized_name) DO UPDATE SET normalized_name=excluded.normalized_name RETURNING id`, uuid.New(), name, normalizeTag(name)).Scan(&tagID)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO meme_tags(meme_id,tag_id,source) VALUES($1,$2,'user')", memeID, tagID); err != nil {
			return err
		}
	}
	return nil
}
func (s *Server) createMeme(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticate(w, r, true)
	if !ok {
		return
	}
	var in memeInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if code := s.validateMeme(&in); code != "" {
		problem(w, 422, code)
		return
	}
	if in.ContentType == "image" {
		if code := s.validateImage(&in); code != "" {
			problem(w, 422, code)
			return
		}
		s.reserveImage(w, r, sess, in)
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		internal(w)
		return
	}
	defer tx.Rollback(r.Context())
	id := uuid.New()
	analysisStatus := "queued"
	if s.cfg.DemoMode {
		analysisStatus = "not_requested"
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO memes(id,created_by,content_type,title,body_text,description,source_url,source_name,source_author,source_note,status,analysis_status)
 VALUES($1,$2,'text',$3,$4,$5,$6,$7,$8,$9,'ready',$10)`, id, sess.UserID, in.Title, in.BodyText, in.Description, nullable(in.SourceURL), nullable(in.SourceName), nullable(in.SourceAuthor), nullable(in.SourceNote), analysisStatus)
	if err != nil {
		internal(w)
		return
	}
	if err = saveTags(r.Context(), tx, id, in.Tags); err != nil {
		internal(w)
		return
	}
	if !s.cfg.DemoMode {
		_, err = tx.Exec(r.Context(), `INSERT INTO jobs(id,meme_id,type,input_revision,provider,requested_model,prompt_version)
 VALUES($1,$2,'analyze_content',0,'openai',COALESCE((SELECT openai_model FROM admin_settings WHERE singleton),$3),'v1')`, uuid.New(), id, s.cfg.OpenAIModel)
		if err != nil {
			internal(w)
			return
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
	w.Header().Set("Location", "/api/memes/"+id.String())
	writeJSON(w, 201, m)
}
func (s *Server) getMeme(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "invalid_meme_id")
		return
	}
	m, err := scanMeme(s.db.QueryRow(r.Context(), "SELECT "+memeColumns+" FROM memes m WHERE m.id=$1 AND m.deleted_at IS NULL AND m.status='ready'", id))
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "meme_not_found")
		return
	}
	if err != nil {
		internal(w)
		return
	}
	writeJSON(w, 200, m)
}
func (s *Server) updateMeme(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticate(w, r, true)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "invalid_meme_id")
		return
	}
	var in memeInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if code := s.validateMeme(&in); code != "" {
		problem(w, 422, code)
		return
	}
	if in.Revision == nil || *in.Revision < 0 {
		problem(w, 422, "content_revision_required")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		internal(w)
		return
	}
	defer tx.Rollback(r.Context())
	var revision int64
	var contentType string
	err = tx.QueryRow(r.Context(), "SELECT content_revision,content_type FROM memes WHERE id=$1 AND (created_by=$2 OR $3) AND deleted_at IS NULL AND status='ready' FOR UPDATE", id, sess.UserID, sess.IsAdmin).Scan(&revision, &contentType)
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "meme_not_found")
		return
	}
	if err != nil {
		internal(w)
		return
	}
	if contentType != in.ContentType || (contentType == "image" && (in.BodyText != "" || in.OriginalFilename != "" || in.MIMEType != "" || in.SizeBytes != 0 || in.SHA256 != "")) {
		problem(w, 422, "invalid_content_type_update")
		return
	}
	if revision != *in.Revision {
		problem(w, 409, "revision_conflict")
		return
	}
	_, err = tx.Exec(r.Context(), `UPDATE memes SET title=$2,body_text=$3,description=$4,source_url=$5,source_name=$6,source_author=$7,source_note=$8,extracted_text=CASE WHEN content_type='image' THEN $9 ELSE extracted_text END,
 content_revision=content_revision+1,updated_at=now(),analysis_status=CASE WHEN EXISTS(SELECT 1 FROM jobs WHERE meme_id=$1 AND type='analyze_content' AND status IN ('queued','processing')) THEN analysis_status ELSE 'not_requested' END WHERE id=$1`, id, in.Title, nullable(in.BodyText), in.Description, nullable(in.SourceURL), nullable(in.SourceName), nullable(in.SourceAuthor), nullable(in.SourceNote), in.ExtractedText)
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
	writeJSON(w, 200, m)
}
func (s *Server) deleteMeme(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticate(w, r, true)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "invalid_meme_id")
		return
	}
	result, err := s.db.Exec(r.Context(), "UPDATE memes SET deleted_at=now(),updated_at=now() WHERE id=$1 AND (created_by=$2 OR $3) AND deleted_at IS NULL", id, sess.UserID, sess.IsAdmin)
	if err != nil {
		internal(w)
		return
	}
	if result.RowsAffected() == 0 {
		problem(w, 404, "meme_not_found")
		return
	}
	w.WriteHeader(204)
}

type cursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        uuid.UUID `json:"id"`
}

func validUsername(value string) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= 200 && !strings.ContainsRune(value, 0) && strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) == -1
}

func (s *Server) listMemes(w http.ResponseWriter, r *http.Request) {
	s.listMemesFiltered(w, r, "")
}
func (s *Server) listAdminMemes(w http.ResponseWriter, r *http.Request) {
	if !s.adminSession(w, r, false) {
		return
	}
	status := r.URL.Query().Get("analysis_status")
	if status != "" && status != "queued" && status != "processing" && status != "failed" && status != "completed" && status != "not_requested" {
		problem(w, 400, "invalid_analysis_status")
		return
	}
	s.listMemesFiltered(w, r, status)
}
func (s *Server) listMemesFiltered(w http.ResponseWriter, r *http.Request, analysisStatus string) {
	query := r.URL.Query()
	limit := 50
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			problem(w, 400, "invalid_limit")
			return
		}
		limit = n
	}
	contentType := query.Get("content_type")
	if contentType != "" && contentType != "text" && contentType != "image" {
		problem(w, 400, "invalid_content_type")
		return
	}
	username := query.Get("uploader")
	if username != "" && !validUsername(username) {
		problem(w, 400, "invalid_uploader")
		return
	}
	var mine *uuid.UUID
	if value := query.Get("mine"); value != "" && value != "false" {
		if value != "true" {
			problem(w, 400, "invalid_mine_filter")
			return
		}
		sess, ok := s.authenticate(w, r, false)
		if !ok {
			return
		}
		mine = &sess.UserID
	}
	var savedCollection *uuid.UUID
	var collectionOwner uuid.UUID
	if raw := query.Get("collection"); raw != "" {
		sess, ok := s.authenticate(w, r, false)
		if !ok {
			return
		}
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			problem(w, 400, "invalid_collection_id")
			return
		}
		var exists bool
		if err = s.db.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM collections WHERE id=$1 AND user_id=$2)", id, sess.UserID).Scan(&exists); err != nil {
			internal(w)
			return
		}
		if !exists {
			problem(w, 404, "collection_not_found")
			return
		}
		savedCollection, collectionOwner = &id, sess.UserID
	}
	q := query.Get("q")
	if !utf8.ValidString(q) || strings.ContainsRune(q, 0) || utf8.RuneCountInString(q) > 200 {
		problem(w, 400, "invalid_query")
		return
	}
	tags := []string{}
	if len(query["tag"]) > 30 {
		problem(w, 400, "too_many_tags")
		return
	}
	for _, tag := range query["tag"] {
		if len(tag) > 200 || strings.ContainsRune(tag, 0) {
			problem(w, 400, "invalid_tag")
			return
		}
		tags = append(tags, normalizeTag(tag))
	}
	var at *time.Time
	var id *uuid.UUID
	if raw := query.Get("cursor"); raw != "" {
		if len(raw) > 512 {
			problem(w, 400, "invalid_cursor")
			return
		}
		data, err := base64.RawURLEncoding.DecodeString(raw)
		var c cursor
		if err != nil || json.Unmarshal(data, &c) != nil || c.CreatedAt.IsZero() || c.ID == uuid.Nil {
			problem(w, 400, "invalid_cursor")
			return
		}
		at = &c.CreatedAt
		id = &c.ID
	}
	pattern := "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(q) + "%"
	rows, err := s.db.Query(r.Context(), "SELECT "+memeColumns+` FROM memes m
 WHERE m.deleted_at IS NULL AND m.status='ready'
 AND ($2='' OR m.content_type=$2)
 AND (concat_ws(' ',m.title,m.body_text,m.description,m.extracted_text,m.source_name,m.source_author) ILIKE $1
      OR EXISTS(SELECT 1 FROM meme_tags mt JOIN tags t ON t.id=mt.tag_id WHERE mt.meme_id=m.id AND t.name ILIKE $1))
 AND NOT EXISTS(SELECT 1 FROM unnest($3::text[]) wanted(name) WHERE NOT EXISTS(
     SELECT 1 FROM meme_tags mt JOIN tags t ON t.id=mt.tag_id WHERE mt.meme_id=m.id AND t.normalized_name=wanted.name))
 AND ($4::timestamptz IS NULL OR (m.created_at,m.id)<($4,$5::uuid))
 AND ($7='' OR EXISTS(SELECT 1 FROM users u WHERE u.id=m.created_by AND u.oidc_issuer=$8 AND lower(u.username)=lower($7)))
 AND ($9::uuid IS NULL OR m.created_by=$9)
 AND ($10='' OR m.analysis_status=$10)
	 AND ($11::uuid IS NULL OR EXISTS(SELECT 1 FROM collection_memes cm JOIN collections c ON c.id=cm.collection_id
	     WHERE cm.meme_id=m.id AND c.id=$11 AND c.user_id=$12))
	 ORDER BY m.created_at DESC,m.id DESC LIMIT $6`, pattern, contentType, tags, at, id, limit+1, username, s.cfg.OIDCIssuer, mine, analysisStatus, savedCollection, collectionOwner)
	if err != nil {
		internal(w)
		return
	}
	defer rows.Close()
	items := []Meme{}
	for rows.Next() {
		m, err := scanMeme(rows)
		if err != nil {
			internal(w)
			return
		}
		items = append(items, m)
	}
	if rows.Err() != nil {
		internal(w)
		return
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		data, _ := json.Marshal(cursor{CreatedAt: last.CreatedAt, ID: last.ID})
		value := base64.RawURLEncoding.EncodeToString(data)
		next = &value
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}
