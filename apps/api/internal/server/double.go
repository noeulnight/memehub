package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/noeulnight/memehub/apps/api/internal/preview"
)

var doubleSlots = make(chan struct{}, 2)

func (s *Server) doubleMeme(w http.ResponseWriter, r *http.Request) {
	var ids [2]uuid.UUID
	for i, name := range []string{"left", "right"} {
		id, err := uuid.Parse(r.PathValue(name))
		if err != nil {
			problem(w, 400, "invalid_meme_id")
			return
		}
		ids[i] = id
	}
	var keys, hashes, mimes [2]string
	for i, id := range ids {
		err := s.db.QueryRow(r.Context(), "SELECT original_object_key,sha256,mime_type FROM memes WHERE id=$1 AND content_type='image' AND status='ready' AND deleted_at IS NULL", id).Scan(&keys[i], &hashes[i], &mimes[i])
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				problem(w, 404, "meme_not_found")
			} else {
				internal(w)
			}
			return
		}
	}
	ext := "png"
	if mimes[0] == "image/gif" || mimes[1] == "image/gif" {
		ext = "gif"
	}
	if r.PathValue("extension") != ext {
		problem(w, 400, "invalid_double_extension")
		return
	}
	if s.store == nil {
		problem(w, 503, "storage_unavailable")
		return
	}
	etag := fmt.Sprintf(`"%x"`, sha256.Sum256([]byte("double-v1:"+hashes[0]+":"+hashes[1])))
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	select {
	case doubleSlots <- struct{}{}:
		defer func() { <-doubleSlots }()
	default:
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Retry-After", "2")
		problem(w, 503, "double_generation_busy")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var inputs [2][]byte
	for i, key := range keys {
		body, err := s.store.Read(ctx, key)
		if err != nil {
			w.Header().Set("Cache-Control", "no-store")
			problem(w, 502, "storage_unavailable")
			return
		}
		inputs[i], err = io.ReadAll(io.LimitReader(body, (10<<20)+1))
		body.Close()
		if err != nil || len(inputs[i]) > 10<<20 {
			w.Header().Set("Cache-Control", "no-store")
			problem(w, 502, "storage_unavailable")
			return
		}
	}
	data, mime, err := preview.Double(ctx, inputs)
	if err != nil {
		w.Header().Set("Cache-Control", "no-store")
		code := "double_generation_failed"
		if err.Error() == "double_animation_duration_unsupported" {
			code = err.Error()
		}
		problem(w, 422, code)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", `inline; filename="double.`+ext+`"`)
	http.ServeContent(w, r, "double."+ext, time.Time{}, bytes.NewReader(data))
}
