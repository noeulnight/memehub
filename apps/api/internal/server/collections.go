package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/text/unicode/norm"
)

type collection struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MemeCount    int64     `json:"meme_count"`
	ContainsMeme bool      `json:"contains_meme"`
}

const collectionColumns = `c.id,c.name,c.created_at,c.updated_at,
 (SELECT count(*) FROM collection_memes cm JOIN memes m ON m.id=cm.meme_id
  WHERE cm.collection_id=c.id AND m.deleted_at IS NULL AND m.status='ready')`

func collectionID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("collectionID"))
	if err != nil || id == uuid.Nil {
		problem(w, 400, "invalid_collection_id")
		return uuid.Nil, false
	}
	return id, true
}

func collectionName(value string) (string, bool) {
	value = norm.NFC.String(strings.TrimSpace(value))
	return value, value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= 100 && strings.IndexFunc(value, unicode.IsControl) == -1
}

func collectionError(w http.ResponseWriter, err error) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		problem(w, 409, "collection_name_exists")
		return
	}
	internal(w)
}

func (s *Server) listCollections(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			problem(w, 400, "invalid_limit")
			return
		}
		limit = n
	}
	var memeID *uuid.UUID
	if raw := r.URL.Query().Get("meme_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			problem(w, 400, "invalid_meme_id")
			return
		}
		memeID = &id
	}
	var at *time.Time
	var id *uuid.UUID
	if raw := r.URL.Query().Get("cursor"); raw != "" {
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
		at, id = &c.CreatedAt, &c.ID
	}
	rows, err := s.db.Query(r.Context(), "SELECT "+collectionColumns+`,
 EXISTS(SELECT 1 FROM collection_memes cm JOIN memes m ON m.id=cm.meme_id
 WHERE cm.collection_id=c.id AND cm.meme_id=$5 AND m.deleted_at IS NULL AND m.status='ready')
 FROM collections c WHERE c.user_id=$1 AND ($2::timestamptz IS NULL OR (c.created_at,c.id)<($2,$3::uuid))
 ORDER BY c.created_at DESC,c.id DESC LIMIT $4`, sess.UserID, at, id, limit+1, memeID)
	if err != nil {
		internal(w)
		return
	}
	defer rows.Close()
	items := []collection{}
	for rows.Next() {
		var item collection
		if err = rows.Scan(&item.ID, &item.Name, &item.CreatedAt, &item.UpdatedAt, &item.MemeCount, &item.ContainsMeme); err != nil {
			internal(w)
			return
		}
		items = append(items, item)
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

func (s *Server) getCollection(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	id, ok := collectionID(w, r)
	if !ok {
		return
	}
	var item collection
	err := s.db.QueryRow(r.Context(), "SELECT "+collectionColumns+" FROM collections c WHERE c.id=$1 AND c.user_id=$2", id, sess.UserID).
		Scan(&item.ID, &item.Name, &item.CreatedAt, &item.UpdatedAt, &item.MemeCount)
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "collection_not_found")
		return
	}
	if err != nil {
		internal(w)
		return
	}
	writeJSON(w, 200, item)
}

func (s *Server) createCollection(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticate(w, r, true)
	if !ok {
		return
	}
	var input struct {
		Name   string     `json:"name"`
		MemeID *uuid.UUID `json:"meme_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	name, ok := collectionName(input.Name)
	if !ok {
		problem(w, 422, "invalid_collection_name")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		internal(w)
		return
	}
	defer tx.Rollback(r.Context())
	if input.MemeID != nil {
		var id uuid.UUID
		err = tx.QueryRow(r.Context(), "SELECT id FROM memes WHERE id=$1 AND deleted_at IS NULL AND status='ready' FOR SHARE", input.MemeID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			problem(w, 404, "meme_not_found")
			return
		}
		if err != nil {
			internal(w)
			return
		}
	}
	item := collection{ID: uuid.New(), Name: name}
	err = tx.QueryRow(r.Context(), "INSERT INTO collections(id,user_id,name) VALUES($1,$2,$3) RETURNING created_at,updated_at", item.ID, sess.UserID, name).Scan(&item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		collectionError(w, err)
		return
	}
	if input.MemeID != nil {
		if _, err = tx.Exec(r.Context(), "INSERT INTO collection_memes(collection_id,meme_id) VALUES($1,$2)", item.ID, input.MemeID); err != nil {
			internal(w)
			return
		}
		item.MemeCount, item.ContainsMeme = 1, true
	}
	if err = tx.Commit(r.Context()); err != nil {
		internal(w)
		return
	}
	w.Header().Set("Location", "/api/collections/"+item.ID.String())
	writeJSON(w, 201, item)
}

func (s *Server) updateCollection(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticate(w, r, true)
	if !ok {
		return
	}
	id, ok := collectionID(w, r)
	if !ok {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	name, ok := collectionName(input.Name)
	if !ok {
		problem(w, 422, "invalid_collection_name")
		return
	}
	result, err := s.db.Exec(r.Context(), "UPDATE collections SET name=$3,updated_at=now() WHERE id=$1 AND user_id=$2", id, sess.UserID, name)
	if err != nil {
		collectionError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		problem(w, 404, "collection_not_found")
		return
	}
	w.WriteHeader(204)
}

func (s *Server) deleteCollection(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticate(w, r, true)
	if !ok {
		return
	}
	id, ok := collectionID(w, r)
	if !ok {
		return
	}
	result, err := s.db.Exec(r.Context(), "DELETE FROM collections WHERE id=$1 AND user_id=$2", id, sess.UserID)
	if err != nil {
		internal(w)
		return
	}
	if result.RowsAffected() == 0 {
		problem(w, 404, "collection_not_found")
		return
	}
	w.WriteHeader(204)
}

func (s *Server) collectionMeme(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticate(w, r, true)
	if !ok {
		return
	}
	id, ok := collectionID(w, r)
	if !ok {
		return
	}
	memeID, err := uuid.Parse(r.PathValue("id"))
	if err != nil || memeID == uuid.Nil {
		problem(w, 400, "invalid_meme_id")
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		internal(w)
		return
	}
	defer tx.Rollback(r.Context())
	var found uuid.UUID
	err = tx.QueryRow(r.Context(), "SELECT id FROM collections WHERE id=$1 AND user_id=$2 FOR UPDATE", id, sess.UserID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "collection_not_found")
		return
	}
	if err != nil {
		internal(w)
		return
	}
	if r.Method == http.MethodPut {
		err = tx.QueryRow(r.Context(), "SELECT id FROM memes WHERE id=$1 AND deleted_at IS NULL AND status='ready' FOR SHARE", memeID).Scan(&found)
		if errors.Is(err, pgx.ErrNoRows) {
			problem(w, 404, "meme_not_found")
			return
		}
		if err == nil {
			_, err = tx.Exec(r.Context(), "INSERT INTO collection_memes(collection_id,meme_id) VALUES($1,$2) ON CONFLICT DO NOTHING", id, memeID)
		}
	} else {
		_, err = tx.Exec(r.Context(), "DELETE FROM collection_memes WHERE collection_id=$1 AND meme_id=$2", id, memeID)
	}
	if err != nil {
		internal(w)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE collections SET updated_at=now() WHERE id=$1", id); err != nil {
		internal(w)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		internal(w)
		return
	}
	w.WriteHeader(204)
}
