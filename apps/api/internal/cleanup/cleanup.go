package cleanup

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store interface {
	Delete(context.Context, string) error
}
type Worker struct {
	DB                                *pgxpool.Pool
	Store                             Store
	DeletedRetention, UploadRetention time.Duration
}

func (w *Worker) RunOne(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tx, err := w.DB.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	var original string
	var thumbnail, preview *string
	err = tx.QueryRow(ctx, `SELECT id,original_object_key,thumbnail_object_key,preview_object_key FROM memes
 WHERE content_type IN ('image','audio') AND objects_deleted_at IS NULL AND (cleanup_after IS NULL OR cleanup_after<=now())
 AND ((deleted_at IS NOT NULL AND deleted_at<now()-COALESCE((SELECT deleted_image_retention_days*interval '1 day' FROM admin_settings WHERE singleton),$1::interval))
 OR (deleted_at IS NULL AND status IN ('uploading','failed') AND updated_at<now()-COALESCE((SELECT incomplete_upload_retention_hours*interval '1 hour' FROM admin_settings WHERE singleton),$2::interval)))
 ORDER BY updated_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`, w.DeletedRetention.String(), w.UploadRetention.String()).Scan(&id, &original, &thumbnail, &preview)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	keys := map[string]bool{original: true, "staging/" + id.String(): true, "thumbnails/" + id.String() + ".png": true, "previews/" + id.String() + ".webp": true}
	if thumbnail != nil {
		keys[*thumbnail] = true
	}
	if preview != nil {
		keys[*preview] = true
	}
	for key := range keys {
		if key != "" {
			if err = w.Store.Delete(ctx, key); err != nil {
				if ctx.Err() == nil {
					if _, e := tx.Exec(ctx, "UPDATE memes SET cleanup_after=now()+interval '5 minutes' WHERE id=$1", id); e == nil {
						_ = tx.Commit(ctx)
					}
				}
				return true, errors.New("image_object_cleanup_failed")
			}
		}
	}
	if _, err = tx.Exec(ctx, "UPDATE jobs SET status='failed',error_code='meme_deleted',lock_token=NULL,locked_until=NULL,finished_at=now(),updated_at=now() WHERE meme_id=$1 AND status IN ('queued','processing')", id); err != nil {
		return true, err
	}
	if _, err = tx.Exec(ctx, "UPDATE memes SET deleted_at=COALESCE(deleted_at,now()),objects_deleted_at=now(),updated_at=now() WHERE id=$1", id); err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}

func (w *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		for range 20 {
			worked, err := w.RunOne(ctx)
			if err != nil && ctx.Err() == nil {
				slog.Error("image object cleanup failed")
			}
			if !worked || err != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
	}
}
