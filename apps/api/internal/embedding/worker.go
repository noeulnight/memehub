package embedding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Worker struct {
	DB       *pgxpool.Pool
	Embedder Embedder
}
type job struct {
	id, meme, token uuid.UUID
	revision        int64
	attempts, max   int
	input, hash     string
	reusable        bool
}

// Input keeps a deterministic order and bounds the UTF-8 byte count below the model's token limit.
func Input(title, description, ocr, body string, tags []string) string {
	text := strings.TrimSpace(strings.Join([]string{title, description, ocr, body, strings.Join(tags, " ")}, "\n"))
	if len(text) > 7500 {
		text = text[:7500]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	return text
}
func Hash(input string) string {
	s := sha256.Sum256([]byte(Model + "\n" + InputVersion + "\n" + input))
	return hex.EncodeToString(s[:])
}

func (w *Worker) enqueue(ctx context.Context) error {
	_, err := w.DB.Exec(ctx, `INSERT INTO jobs(id,meme_id,type,input_revision,provider,requested_model,prompt_version)
 SELECT gen_random_uuid(),m.id,'embed_content',m.content_revision,'openai',$1,$2 FROM memes m
 WHERE m.deleted_at IS NULL AND m.status='ready' AND m.analysis_status NOT IN ('queued','processing')
 AND NOT EXISTS(SELECT 1 FROM meme_embeddings e WHERE e.meme_id=m.id AND e.content_revision=m.content_revision AND e.model=$1 AND e.input_version=$2)
 AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.meme_id=m.id AND j.type='embed_content' AND
   (j.status IN ('queued','processing') OR (j.input_revision=m.content_revision AND j.requested_model=$1 AND j.prompt_version=$2)))
 ORDER BY m.created_at,m.id LIMIT 50 ON CONFLICT DO NOTHING`, Model, InputVersion)
	return err
}
func (w *Worker) claim(ctx context.Context) (job, error) {
	tx, err := w.DB.Begin(ctx)
	if err != nil {
		return job{}, err
	}
	defer tx.Rollback(ctx)
	var j job
	var title, desc, ocr, body string
	var tags []string
	err = tx.QueryRow(ctx, `SELECT j.id,m.id,m.content_revision,j.attempts,j.max_attempts,m.title,m.description,m.extracted_text,COALESCE(m.body_text,''),
 ARRAY(SELECT t.name FROM tags t JOIN meme_tags mt ON mt.tag_id=t.id WHERE mt.meme_id=m.id ORDER BY t.normalized_name)
 FROM jobs j JOIN memes m ON m.id=j.meme_id
 WHERE j.type='embed_content' AND ((j.status='queued' AND j.run_after<=now()) OR (j.status='processing' AND j.locked_until<=now()))
 ORDER BY j.run_after,j.id LIMIT 1 FOR UPDATE OF m,j SKIP LOCKED`).Scan(&j.id, &j.meme, &j.revision, &j.attempts, &j.max, &title, &desc, &ocr, &body, &tags)
	if err != nil {
		return j, err
	}
	j.input = Input(title, desc, ocr, body, tags)
	j.hash = Hash(j.input)
	j.token = uuid.New()
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM meme_embeddings WHERE meme_id=$1 AND input_hash=$2 AND model=$3 AND input_version=$4)`, j.meme, j.hash, Model, InputVersion).Scan(&j.reusable)
	if err != nil {
		return j, err
	}
	_, err = tx.Exec(ctx, `UPDATE jobs SET status='processing',attempts=attempts+1,input_revision=$2,lock_token=$3,locked_until=now()+interval '45 seconds',started_at=COALESCE(started_at,now()),updated_at=now() WHERE id=$1`, j.id, j.revision, j.token)
	if err != nil {
		return j, err
	}
	return j, tx.Commit(ctx)
}
func (w *Worker) RunOne(ctx context.Context) (bool, error) {
	j, err := w.claim(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		if err = w.enqueue(ctx); err != nil {
			return false, err
		}
		j, err = w.claim(ctx)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var available bool
	err = w.DB.QueryRow(ctx, `SELECT deleted_at IS NULL AND status='ready' FROM memes WHERE id=$1`, j.meme).Scan(&available)
	var result Result
	switch {
	case err != nil:
	case !available || j.attempts >= j.max || j.input == "":
		err = &Failure{}
	case j.reusable:
	default:
		callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		result, err = w.Embedder.Embed(callCtx, j.input)
		cancel()
		if err == nil && !Valid(result.Vector) {
			err = &Failure{}
		}
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err != nil {
		status := "failed"
		if retryable(err) && j.attempts+1 < j.max {
			status = "queued"
		}
		_, saveErr := w.DB.Exec(saveCtx, `UPDATE jobs SET status=$3,error_code='embedding_failed',run_after=now()+interval '30 seconds',lock_token=NULL,locked_until=NULL,finished_at=CASE WHEN $3='failed' THEN now() ELSE NULL END,updated_at=now() WHERE id=$1 AND lock_token=$2 AND locked_until>now()`, j.id, j.token, status)
		return true, saveErr
	}
	tx, err := w.DB.Begin(saveCtx)
	if err != nil {
		return true, err
	}
	defer tx.Rollback(saveCtx)
	var revision int64
	var deleted bool
	err = tx.QueryRow(saveCtx, `SELECT m.content_revision,m.deleted_at IS NOT NULL FROM memes m JOIN jobs j ON j.meme_id=m.id WHERE j.id=$1 AND j.lock_token=$2 AND j.locked_until>now() FOR UPDATE OF m,j`, j.id, j.token).Scan(&revision, &deleted)
	if err != nil {
		return true, err
	}
	if revision == j.revision && !deleted {
		if j.reusable {
			_, err = tx.Exec(saveCtx, `UPDATE meme_embeddings SET content_revision=$2,updated_at=now() WHERE meme_id=$1`, j.meme, j.revision)
		} else {
			_, err = tx.Exec(saveCtx, `INSERT INTO meme_embeddings(meme_id,model,input_version,input_hash,content_revision,embedding,input_tokens)
 VALUES($1,$2,$3,$4,$5,$6::public.vector,$7) ON CONFLICT(meme_id) DO UPDATE SET model=excluded.model,input_version=excluded.input_version,input_hash=excluded.input_hash,content_revision=excluded.content_revision,embedding=excluded.embedding,input_tokens=excluded.input_tokens,updated_at=now()`, j.meme, Model, InputVersion, j.hash, j.revision, Literal(result.Vector), result.Tokens)
		}
		if err != nil {
			return true, err
		}
	}
	_, err = tx.Exec(saveCtx, `UPDATE jobs SET status='completed',response_model=$3,input_tokens=$4,output_tokens=0,error_code=NULL,lock_token=NULL,locked_until=NULL,finished_at=now(),updated_at=now() WHERE id=$1 AND lock_token=$2`, j.id, j.token, Model, result.Tokens)
	if err != nil {
		return true, err
	}
	return true, tx.Commit(saveCtx)
}
func (w *Worker) Run(ctx context.Context) {
	for ctx.Err() == nil {
		worked, err := w.RunOne(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Error("embedding worker iteration failed")
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}
