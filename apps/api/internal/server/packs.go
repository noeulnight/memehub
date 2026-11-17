package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type pack struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	SourceURL *string   `json:"source_url"`
	CreatedBy uuid.UUID `json:"created_by"`
	MemeCount int64     `json:"meme_count"`
}

const packColumns = `p.id,p.name,p.source_url,p.created_by,
 (SELECT count(*) FROM memes m WHERE m.pack_id=p.id AND m.deleted_at IS NULL AND m.status='ready')`

func (s *Server) listPacks(w http.ResponseWriter, r *http.Request) {
	var name *string
	var id *uuid.UUID
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		var c struct {
			Name string    `json:"name"`
			ID   uuid.UUID `json:"id"`
		}
		data, err := base64.RawURLEncoding.DecodeString(raw)
		if len(raw) > 1024 || err != nil || json.Unmarshal(data, &c) != nil || c.Name == "" || c.ID == uuid.Nil {
			problem(w, 400, "invalid_cursor")
			return
		}
		name, id = &c.Name, &c.ID
	}
	rows, err := s.db.Query(r.Context(), "SELECT "+packColumns+` FROM packs p
 WHERE ($1::text IS NULL OR (lower(p.name),p.id)>(lower($1),$2::uuid))
 ORDER BY lower(p.name),p.id LIMIT 101`, name, id)
	if err != nil {
		internal(w)
		return
	}
	defer rows.Close()
	items := []pack{}
	for rows.Next() {
		var p pack
		if err = rows.Scan(&p.ID, &p.Name, &p.SourceURL, &p.CreatedBy, &p.MemeCount); err != nil {
			internal(w)
			return
		}
		items = append(items, p)
	}
	if rows.Err() != nil {
		internal(w)
		return
	}
	var next *string
	if len(items) > 100 {
		items = items[:100]
		last := items[len(items)-1]
		data, _ := json.Marshal(struct {
			Name string    `json:"name"`
			ID   uuid.UUID `json:"id"`
		}{last.Name, last.ID})
		value := base64.RawURLEncoding.EncodeToString(data)
		next = &value
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}
func packError(w http.ResponseWriter, err error) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		problem(w, 409, "pack_name_exists")
		return
	}
	internal(w)
}
func (s *Server) writePack(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.authenticate(w, r, true)
	if !ok {
		return
	}
	id := uuid.New()
	if r.Method != "POST" {
		var err error
		id, err = uuid.Parse(r.PathValue("packID"))
		if err != nil || id == uuid.Nil {
			problem(w, 400, "invalid_pack_id")
			return
		}
	}
	if r.Method == "DELETE" {
		result, err := s.db.Exec(r.Context(), "DELETE FROM packs WHERE id=$1 AND (created_by=$2 OR $3)", id, sess.UserID, sess.IsAdmin)
		if err != nil {
			internal(w)
			return
		}
		if result.RowsAffected() == 0 {
			problem(w, 404, "pack_not_found")
			return
		}
		w.WriteHeader(204)
		return
	}
	var in struct {
		Name      string `json:"name"`
		SourceURL string `json:"source_url"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	name, valid := collectionName(in.Name)
	if !valid {
		problem(w, 422, "invalid_pack_name")
		return
	}
	in.SourceURL = strings.TrimSpace(in.SourceURL)
	if in.SourceURL != "" {
		u, err := url.Parse(in.SourceURL)
		if err != nil || !utf8.ValidString(in.SourceURL) || len(in.SourceURL) > 2048 || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
			problem(w, 422, "invalid_source_url")
			return
		}
	}
	var err error
	if r.Method == "POST" {
		_, err = s.db.Exec(r.Context(), "INSERT INTO packs(id,name,source_url,created_by) VALUES($1,$2,$3,$4)", id, name, nullable(in.SourceURL), sess.UserID)
	} else {
		var resultID uuid.UUID
		err = s.db.QueryRow(r.Context(), "UPDATE packs SET name=$2,source_url=$3,updated_at=now() WHERE id=$1 AND (created_by=$4 OR $5) RETURNING id", id, name, nullable(in.SourceURL), sess.UserID, sess.IsAdmin).Scan(&resultID)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "pack_not_found")
		return
	}
	if err != nil {
		packError(w, err)
		return
	}
	var p pack
	err = s.db.QueryRow(r.Context(), "SELECT "+packColumns+" FROM packs p WHERE p.id=$1", id).Scan(&p.ID, &p.Name, &p.SourceURL, &p.CreatedBy, &p.MemeCount)
	if err != nil {
		internal(w)
		return
	}
	status := 200
	if r.Method == "POST" {
		status = 201
	}
	writeJSON(w, status, p)
}

// A missing pack field on update preserves membership for older clients; an empty string clears it.
func savePack(r *http.Request, tx pgx.Tx, w http.ResponseWriter, memeID uuid.UUID, raw *string) bool {
	if raw == nil {
		return true
	}
	var id *uuid.UUID
	if *raw != "" {
		parsed, err := uuid.Parse(*raw)
		if err != nil || parsed == uuid.Nil {
			problem(w, 422, "invalid_pack_id")
			return false
		}
		var exists uuid.UUID
		err = tx.QueryRow(r.Context(), "SELECT id FROM packs WHERE id=$1 FOR KEY SHARE", parsed).Scan(&exists)
		if errors.Is(err, pgx.ErrNoRows) {
			problem(w, 422, "pack_not_found")
			return false
		}
		if err != nil {
			internal(w)
			return false
		}
		id = &parsed
	}
	if _, err := tx.Exec(r.Context(), "UPDATE memes SET pack_id=$2 WHERE id=$1", memeID, id); err != nil {
		internal(w)
		return false
	}
	return true
}
