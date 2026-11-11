package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/noeulnight/memehub/apps/api/internal/tag"
)

type Analyzer interface {
	Analyze(context.Context, Input) (Response, error)
}
type ImageStore interface {
	Read(context.Context, string) (io.ReadCloser, error)
}

type Worker struct {
	DB            *pgxpool.Pool
	Analyzer      Analyzer
	Store         ImageStore
	Timeout       time.Duration
	MaxImageBytes int64
}

type job struct {
	ID, MemeID, Token                         uuid.UUID
	Attempts, MaxAttempts                     int
	Revision                                  int64
	PromptVersion, Provider, Status, ImageKey string
	Deleted                                   bool
	Input                                     Input
}

var ErrLeaseLost = errors.New("analysis lease lost")

func (w *Worker) claim(ctx context.Context) (job, error) {
	tx, err := w.DB.Begin(ctx)
	if err != nil {
		return job{}, err
	}
	defer tx.Rollback(ctx)
	var j job
	err = tx.QueryRow(ctx, `SELECT j.id,j.meme_id,j.attempts,j.max_attempts,j.requested_model,j.prompt_version,j.provider,
 m.content_revision,m.content_type,m.title,COALESCE(m.body_text,''),m.description,
 COALESCE(CASE WHEN m.mime_type='image/gif' THEN m.thumbnail_object_key ELSE m.original_object_key END,''),m.status,m.deleted_at IS NOT NULL
 FROM jobs j JOIN memes m ON m.id=j.meme_id
 WHERE j.type='analyze_content' AND ((j.status='queued' AND j.run_after<=now()) OR (j.status='processing' AND j.locked_until<=now()))
 ORDER BY j.run_after,j.id LIMIT 1 FOR UPDATE OF m,j SKIP LOCKED`).Scan(&j.ID, &j.MemeID, &j.Attempts, &j.MaxAttempts, &j.Input.Model, &j.PromptVersion, &j.Provider, &j.Revision, &j.Input.ContentType, &j.Input.Title, &j.Input.Body, &j.Input.Description, &j.ImageKey, &j.Status, &j.Deleted)
	if err != nil {
		return j, err
	}
	j.Token = uuid.New()
	if j.Attempts < j.MaxAttempts {
		j.Attempts++
	} else {
		j.MaxAttempts = 0
	}
	_, err = tx.Exec(ctx, `UPDATE jobs SET status='processing',attempts=$2,input_revision=$3,lock_token=$4,locked_until=now()+$5::interval,started_at=COALESCE(started_at,now()),updated_at=now() WHERE id=$1`, j.ID, j.Attempts, j.Revision, j.Token, (w.Timeout + 30*time.Second).String())
	if err != nil {
		return j, err
	}
	if !j.Deleted && j.Status == "ready" {
		_, err = tx.Exec(ctx, "UPDATE memes SET analysis_status='processing',updated_at=now() WHERE id=$1", j.MemeID)
		if err != nil {
			return j, err
		}
	}
	return j, tx.Commit(ctx)
}

// RunOne claims at most one job. A lease protects against overlapping workers and late results.
func (w *Worker) RunOne(ctx context.Context) (bool, error) {
	j, err := w.claim(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	callCtx, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()
	var response Response
	switch {
	case j.MaxAttempts == 0:
		err = failure("analysis_attempts_exhausted", false)
	case j.Deleted || j.Status != "ready":
		err = failure("meme_unavailable", false)
	case j.Provider != "openai" || j.PromptVersion != "v1":
		err = failure("unsupported_analysis_version", false)
	default:
		if j.Input.ContentType == "image" {
			err = w.readImage(callCtx, &j)
		}
		if err == nil {
			response, err = w.Analyzer.Analyze(callCtx, j.Input)
		}
		if err == nil {
			err = response.Result.validate(j.Input.ContentType)
		}
	}
	// Persist cancellation safely; never log provider bodies, keys, image bytes, or signed URLs.
	saveCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer stop()
	if err != nil {
		return true, w.fail(saveCtx, j, err)
	}
	return true, w.complete(saveCtx, j, response)
}

func (w *Worker) readImage(ctx context.Context, j *job) error {
	if w.Store == nil {
		return failure("image_storage_unavailable", true)
	}
	body, err := w.Store.Read(ctx, j.ImageKey)
	if err != nil {
		return failure("image_read_failed", true)
	}
	defer body.Close()
	j.Input.Image, err = io.ReadAll(io.LimitReader(body, w.MaxImageBytes+1))
	if err != nil {
		return failure("image_read_failed", true)
	}
	if len(j.Input.Image) == 0 || int64(len(j.Input.Image)) > w.MaxImageBytes {
		return failure("invalid_analysis_image", false)
	}
	j.Input.MIME = http.DetectContentType(j.Input.Image)
	return nil
}

func (w *Worker) complete(ctx context.Context, j job, response Response) error {
	tx, err := w.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var revision int64
	var deleted, first bool
	var status string
	err = tx.QueryRow(ctx, `SELECT m.content_revision,m.deleted_at IS NOT NULL,m.status,
 NOT EXISTS(SELECT 1 FROM jobs p WHERE p.meme_id=m.id AND p.type='analyze_content' AND p.status='completed' AND p.id<>j.id)
 FROM memes m JOIN jobs j ON j.meme_id=m.id
 WHERE j.id=$1 AND j.status='processing' AND j.lock_token=$2 AND j.locked_until>now()
 FOR UPDATE OF m,j`, j.ID, j.Token).Scan(&revision, &deleted, &status, &first)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	result, err := json.Marshal(response.Result)
	if err != nil {
		return err
	}
	if !deleted && status == "ready" {
		analysisStatus := "completed"
		if revision != j.Revision {
			analysisStatus = "not_requested"
		}
		if revision == j.Revision {
			_, err = tx.Exec(ctx, "UPDATE memes SET safety_rating=$2 WHERE id=$1", j.MemeID, response.Result.SafetyRating)
			if err != nil {
				return err
			}
		}
		if first && revision == j.Revision {
			changed := false
			for _, rawName := range response.Result.Tags {
				name := tag.Name(rawName)
				var tagID uuid.UUID
				err = tx.QueryRow(ctx, `INSERT INTO tags(id,name,normalized_name) VALUES($1,$2,$3)
 ON CONFLICT(normalized_name) DO UPDATE SET normalized_name=excluded.normalized_name RETURNING id`, uuid.New(), name, tag.Key(name)).Scan(&tagID)
				if err != nil {
					return err
				}
				command, e := tx.Exec(ctx, "INSERT INTO meme_tags(meme_id,tag_id,source) VALUES($1,$2,'ai') ON CONFLICT DO NOTHING", j.MemeID, tagID)
				if e != nil {
					return e
				}
				changed = changed || command.RowsAffected() > 0
			}
			_, err = tx.Exec(ctx, `UPDATE memes SET
 title=CASE WHEN btrim(title)='' THEN $2 ELSE title END,
 description=CASE WHEN btrim(description)='' THEN $3 ELSE description END,
 extracted_text=CASE WHEN content_type='image' AND btrim(extracted_text)='' THEN $4 ELSE extracted_text END,
 content_revision=content_revision+CASE WHEN $5 OR (btrim(title)='' AND $2<>'') OR (btrim(description)='' AND $3<>'') OR (content_type='image' AND btrim(extracted_text)='' AND $4<>'') THEN 1 ELSE 0 END
 WHERE id=$1`, j.MemeID, response.Result.Title, response.Result.Description, response.Result.ExtractedText, changed)
			if err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, "UPDATE memes SET analysis_status=$2,updated_at=now() WHERE id=$1", j.MemeID, analysisStatus)
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE jobs SET status='completed',result=$2,response_model=$3,provider_request_id=NULLIF($4,''),input_tokens=$5,output_tokens=$6,error_code=NULL,lock_token=NULL,locked_until=NULL,finished_at=now(),updated_at=now() WHERE id=$1`, j.ID, result, response.Model, response.RequestID, response.InputTokens, response.OutputTokens)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (w *Worker) fail(ctx context.Context, j job, cause error) error {
	code, retry := failureDetails(cause)
	status := "failed"
	if retry && j.Attempts < j.MaxAttempts {
		status = "queued"
	}
	tx, err := w.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var revision int64
	var deleted bool
	var memeStatus string
	err = tx.QueryRow(ctx, `SELECT m.content_revision,m.deleted_at IS NOT NULL,m.status FROM memes m JOIN jobs j ON j.meme_id=m.id
 WHERE j.id=$1 AND j.status='processing' AND j.lock_token=$2 AND j.locked_until>now() FOR UPDATE OF m,j`, j.ID, j.Token).Scan(&revision, &deleted, &memeStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	delay := 5 * time.Second * time.Duration(1<<min(j.Attempts, 6))
	_, err = tx.Exec(ctx, `UPDATE jobs SET status=$2,error_code=$3,run_after=now()+$4::interval,lock_token=NULL,locked_until=NULL,finished_at=CASE WHEN $2='failed' THEN now() ELSE NULL END,updated_at=now() WHERE id=$1`, j.ID, status, code, delay.String())
	if err != nil {
		return err
	}
	if !deleted && memeStatus == "ready" {
		analysisStatus := status
		if revision != j.Revision {
			analysisStatus = "not_requested"
		}
		_, err = tx.Exec(ctx, "UPDATE memes SET analysis_status=$2,updated_at=now() WHERE id=$1", j.MemeID, analysisStatus)
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err == nil {
		slog.Warn("analysis attempt failed", "job_id", j.ID, "code", code, "retry", status == "queued")
	}
	return err
}

func (w *Worker) Run(ctx context.Context, concurrency int) {
	done := make(chan struct{}, concurrency)
	for range concurrency {
		go func() {
			defer func() { done <- struct{}{} }()
			for ctx.Err() == nil {
				worked, err := w.RunOne(ctx)
				if err != nil && ctx.Err() == nil {
					slog.Error("analysis worker iteration failed")
				}
				if !worked || err != nil {
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Second):
					}
				}
			}
		}()
	}
	for range concurrency {
		<-done
	}
}
