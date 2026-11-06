package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func duplicateMedia(ctx context.Context, tx pgx.Tx, w http.ResponseWriter, hash string, except uuid.UUID) (bool, error) {
	var id uuid.UUID
	var title, contentType string
	err := tx.QueryRow(ctx, "SELECT id,title,content_type FROM memes WHERE sha256=$1 AND id<>$2 AND content_type IN ('image','audio') AND status='ready' AND deleted_at IS NULL ORDER BY created_at,id LIMIT 1", hash, except).Scan(&id, &title, &contentType)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	writeJSON(w, 409, map[string]any{"error": "duplicate_" + contentType, "duplicate": map[string]any{"id": id, "title": title}})
	return true, nil
}
