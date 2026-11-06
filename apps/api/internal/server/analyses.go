package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Server) adminSession(w http.ResponseWriter, r *http.Request, mutation bool) bool {
	session, ok := s.authenticate(w, r, mutation)
	if !ok {
		return false
	}
	if !session.IsAdmin {
		problem(w, 403, "admin_required")
		return false
	}
	return true
}

func (s *Server) listAnalyses(w http.ResponseWriter, r *http.Request) {
	if !s.adminSession(w, r, false) {
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "invalid_meme_id")
		return
	}
	var status string
	err = s.db.QueryRow(r.Context(), "SELECT analysis_status FROM memes WHERE id=$1 AND deleted_at IS NULL", id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "meme_not_found")
		return
	}
	if err != nil {
		internal(w)
		return
	}
	rows, err := s.db.Query(r.Context(), "SELECT id,status,attempts,max_attempts,error_code,created_at FROM jobs WHERE meme_id=$1 AND type='analyze_content' ORDER BY created_at DESC,id DESC LIMIT 10", id)
	if err != nil {
		internal(w)
		return
	}
	defer rows.Close()
	type item struct {
		ID          uuid.UUID `json:"id"`
		Status      string    `json:"status"`
		Attempts    int       `json:"attempts"`
		MaxAttempts int       `json:"max_attempts"`
		ErrorCode   *string   `json:"error_code"`
		CreatedAt   time.Time `json:"created_at"`
	}
	items := []item{}
	for rows.Next() {
		var entry item
		if err = rows.Scan(&entry.ID, &entry.Status, &entry.Attempts, &entry.MaxAttempts, &entry.ErrorCode, &entry.CreatedAt); err != nil {
			internal(w)
			return
		}
		items = append(items, entry)
	}
	if rows.Err() != nil {
		internal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"analysis_status": status, "items": items})
}

func (s *Server) retryAnalysis(w http.ResponseWriter, r *http.Request) {
	if !s.adminSession(w, r, true) {
		return
	}
	if s.cfg.DemoMode {
		problem(w, http.StatusServiceUnavailable, "analysis_disabled_in_demo")
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		problem(w, 400, "invalid_meme_id")
		return
	}
	var input struct {
		Revision *int64 `json:"content_revision"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Revision == nil {
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
	err = tx.QueryRow(r.Context(), "SELECT content_revision,content_type FROM memes WHERE id=$1 AND deleted_at IS NULL AND status='ready' FOR UPDATE", id).Scan(&revision, &contentType)
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 404, "meme_not_found")
		return
	}
	if err != nil {
		internal(w)
		return
	}
	if contentType == "audio" {
		problem(w, 422, "audio_analysis_not_supported")
		return
	}
	if revision != *input.Revision {
		problem(w, 409, "revision_conflict")
		return
	}
	var latest string
	err = tx.QueryRow(r.Context(), "SELECT status FROM jobs WHERE meme_id=$1 AND type='analyze_content' ORDER BY created_at DESC,id DESC LIMIT 1", id).Scan(&latest)
	if errors.Is(err, pgx.ErrNoRows) {
		problem(w, 409, "no_failed_analysis")
		return
	}
	if err != nil {
		internal(w)
		return
	}
	var active bool
	if err = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM jobs WHERE meme_id=$1 AND type='analyze_content' AND status IN ('queued','processing'))", id).Scan(&active); err != nil {
		internal(w)
		return
	}
	if active {
		problem(w, 409, "analysis_in_progress")
		return
	}
	if latest != "failed" {
		problem(w, 409, "no_failed_analysis")
		return
	}
	jobID := uuid.New()
	_, err = tx.Exec(r.Context(), "INSERT INTO jobs(id,meme_id,type,input_revision,provider,requested_model,prompt_version) VALUES($1,$2,'analyze_content',$3,'openai',COALESCE((SELECT openai_model FROM admin_settings WHERE singleton),$4),'v1')", jobID, id, revision, s.cfg.OpenAIModel)
	if err != nil {
		internal(w)
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE memes SET analysis_status='queued',updated_at=now() WHERE id=$1", id); err != nil {
		internal(w)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		internal(w)
		return
	}
	writeJSON(w, 202, map[string]any{"job_id": jobID, "meme_id": id, "status": "queued"})
}
