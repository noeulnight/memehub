package analysis

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/noeulnight/memehub/apps/api/internal/database"
)

type analyzeFunc func(context.Context, Input) (Response, error)

func (f analyzeFunc) Analyze(ctx context.Context, input Input) (Response, error) {
	return f(ctx, input)
}

type readFunc func(context.Context, string) (io.ReadCloser, error)

func (f readFunc) Read(ctx context.Context, key string) (io.ReadCloser, error) { return f(ctx, key) }

func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "analysis_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	if err = database.Migrate(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T, kind string) (*Worker, uuid.UUID, uuid.UUID) {
	t.Helper()
	db := testDB(t)
	user, meme, job := uuid.New(), uuid.New(), uuid.New()
	exec(t, db, "INSERT INTO users(id,oidc_issuer,oidc_subject) VALUES($1,'issuer','subject')", user)
	if kind == "image" {
		exec(t, db, `INSERT INTO memes(id,created_by,content_type,title,description,status,analysis_status,original_filename,original_object_key,mime_type,size_bytes,width,height,sha256)
 VALUES($1,$2,'image','Manual title','Manual description','ready','queued','test.png','original/test.png','image/png',8,1,1,$3)`, meme, user, strings.Repeat("a", 64))
	} else {
		exec(t, db, "INSERT INTO memes(id,created_by,content_type,body_text,status,analysis_status) VALUES($1,$2,'text','original body','ready','queued')", meme, user)
	}
	exec(t, db, "INSERT INTO jobs(id,meme_id,type,input_revision,provider,requested_model,prompt_version) VALUES($1,$2,'analyze_content',0,'openai','test-model','v1')", job, meme)
	w := &Worker{DB: db, Timeout: time.Second, MaxImageBytes: 1024, Analyzer: analyzeFunc(func(ctx context.Context, input Input) (Response, error) {
		return Response{Result: Result{SafetyRating: "sfw", Title: "AI title", Description: "AI description", Tags: []string{"cat", "웃음"}}, Model: "response-model", RequestID: "req_test", InputTokens: 10, OutputTokens: 20}, nil
	})}
	return w, meme, job
}

func TestImageAnalysisPreservesManualContent(t *testing.T) {
	w, meme, job := fixture(t, "image")
	tag := uuid.New()
	exec(t, w.DB, "INSERT INTO tags(id,name,normalized_name) VALUES($1,'cat','cat')", tag)
	exec(t, w.DB, "INSERT INTO meme_tags(meme_id,tag_id,source) VALUES($1,$2,'user')", meme, tag)
	w.Store = readFunc(func(ctx context.Context, key string) (io.ReadCloser, error) {
		if key != "original/test.png" {
			t.Error("wrong object")
		}
		return io.NopCloser(bytes.NewReader([]byte("\x89PNG\r\n\x1a\n"))), nil
	})
	w.Analyzer = analyzeFunc(func(ctx context.Context, input Input) (Response, error) {
		if input.MIME != "image/png" || len(input.Image) != 8 || input.Model != "test-model" {
			t.Error("missing image input")
		}
		return Response{Result: Result{SafetyRating: "sfw", Title: "AI title", Description: "AI description", ExtractedText: "visible text", Tags: []string{"cat", "웃음"}}, Model: "response-model", InputTokens: 10, OutputTokens: 20}, nil
	})
	if worked, err := w.RunOne(context.Background()); err != nil || !worked {
		t.Fatalf("run: %v", err)
	}
	var title, description, ocr, status, body string
	var revision int64
	if err := w.DB.QueryRow(context.Background(), "SELECT title,description,extracted_text,analysis_status,content_revision FROM memes WHERE id=$1", meme).Scan(&title, &description, &ocr, &status, &revision); err != nil {
		t.Fatal(err)
	}
	if title != "Manual title" || description != "Manual description" || ocr != "visible text" || status != "completed" || revision != 1 {
		t.Fatal("manual content lost or generated content missing")
	}
	var count int
	w.DB.QueryRow(context.Background(), "SELECT count(*) FROM meme_tags WHERE meme_id=$1 AND source='ai'", meme).Scan(&count)
	if count != 1 {
		t.Fatal("AI must not replace manual tag ownership")
	}
	w.DB.QueryRow(context.Background(), "SELECT status,result->>'extracted_text' FROM jobs WHERE id=$1", job).Scan(&status, &body)
	if status != "completed" || body != "visible text" {
		t.Fatal("analysis result not saved")
	}
}

func TestTextAnalysisAndConcurrentClaims(t *testing.T) {
	w, meme, _ := fixture(t, "text")
	var calls atomic.Int32
	w.Analyzer = analyzeFunc(func(ctx context.Context, input Input) (Response, error) {
		calls.Add(1)
		if input.Body != "original body" {
			t.Error("missing body")
		}
		return Response{Result: Result{SafetyRating: "sfw", Title: "Title", Description: "Description", Tags: []string{"tag"}}, Model: "test"}, nil
	})
	done := make(chan error, 2)
	for range 2 {
		go func() { _, err := w.RunOne(context.Background()); done <- err }()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate provider call")
	}
	var title, body string
	if err := w.DB.QueryRow(context.Background(), "SELECT title,body_text FROM memes WHERE id=$1", meme).Scan(&title, &body); err != nil {
		t.Fatal(err)
	}
	if title != "Title" || body != "original body" {
		t.Fatal("text analysis did not preserve original")
	}
}

func TestAnalysisConflictAndDeletion(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "edit", true: "delete"}[deleted], func(t *testing.T) {
			w, meme, job := fixture(t, "text")
			w.Analyzer = analyzeFunc(func(ctx context.Context, input Input) (Response, error) {
				if deleted {
					exec(t, w.DB, "UPDATE memes SET deleted_at=now() WHERE id=$1", meme)
				} else {
					exec(t, w.DB, "UPDATE memes SET title='User edit',content_revision=content_revision+1 WHERE id=$1", meme)
				}
				return Response{Result: Result{SafetyRating: "sfw", Title: "AI title", Tags: []string{"AI tag"}}, Model: "test"}, nil
			})
			if _, err := w.RunOne(context.Background()); err != nil {
				t.Fatal(err)
			}
			var title, status, rating string
			var count int
			w.DB.QueryRow(context.Background(), "SELECT title,analysis_status,safety_rating FROM memes WHERE id=$1", meme).Scan(&title, &status, &rating)
			w.DB.QueryRow(context.Background(), "SELECT count(*) FROM meme_tags WHERE meme_id=$1", meme).Scan(&count)
			if rating != "unknown" || count != 0 || (!deleted && (title != "User edit" || status != "not_requested")) {
				t.Fatal("stale result applied")
			}
			w.DB.QueryRow(context.Background(), "SELECT status FROM jobs WHERE id=$1 AND result IS NOT NULL", job).Scan(&status)
			if status != "completed" {
				t.Fatal("conflicting result not retained")
			}
		})
	}
}

func TestAnalysisRetryAndLeaseRecovery(t *testing.T) {
	w, _, jobID := fixture(t, "text")
	w.Analyzer = analyzeFunc(func(context.Context, Input) (Response, error) {
		return Response{}, failure("openai_rate_limited", true)
	})
	for attempt := 1; attempt <= 3; attempt++ {
		exec(t, w.DB, "UPDATE jobs SET run_after=now() WHERE id=$1", jobID)
		if _, err := w.RunOne(context.Background()); err != nil {
			t.Fatal(err)
		}
		var status, code string
		var attempts int
		if err := w.DB.QueryRow(context.Background(), "SELECT status,attempts,error_code FROM jobs WHERE id=$1", jobID).Scan(&status, &attempts, &code); err != nil {
			t.Fatal(err)
		}
		want := "queued"
		if attempt == 3 {
			want = "failed"
		}
		if status != want || attempts != attempt || code != "openai_rate_limited" {
			t.Fatal("retry budget not enforced")
		}
	}
	// A previous process can die holding a lease; its result must not overwrite the new worker.
	exec(t, w.DB, "UPDATE jobs SET status='queued',attempts=0,run_after=now() WHERE id=$1", jobID)
	old, err := w.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	exec(t, w.DB, "UPDATE jobs SET locked_until=now()-interval '1 second' WHERE id=$1", jobID)
	current, err := w.claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	response := Response{Result: Result{SafetyRating: "sfw", Title: "Recovered", Tags: []string{}}, Model: "test"}
	if err = w.complete(context.Background(), old, response); !errors.Is(err, ErrLeaseLost) {
		t.Fatal("late result accepted")
	}
	if err = w.complete(context.Background(), current, response); err != nil {
		t.Fatal(err)
	}
}

func TestAnalysisSkipsDeletedAndReanalysis(t *testing.T) {
	t.Run("deleted", func(t *testing.T) {
		w, meme, job := fixture(t, "text")
		exec(t, w.DB, "UPDATE memes SET deleted_at=now() WHERE id=$1", meme)
		w.Analyzer = analyzeFunc(func(context.Context, Input) (Response, error) {
			t.Error("deleted meme sent to provider")
			return Response{}, nil
		})
		if _, err := w.RunOne(context.Background()); err != nil {
			t.Fatal(err)
		}
		var code string
		w.DB.QueryRow(context.Background(), "SELECT error_code FROM jobs WHERE id=$1", job).Scan(&code)
		if code != "meme_unavailable" {
			t.Fatal("deleted job not skipped")
		}
	})
	t.Run("reanalysis", func(t *testing.T) {
		w, meme, _ := fixture(t, "text")
		exec(t, w.DB, "INSERT INTO jobs(id,meme_id,type,status,input_revision,provider,requested_model,prompt_version) VALUES($1,$2,'analyze_content','completed',0,'openai','test','v1')", uuid.New(), meme)
		if _, err := w.RunOne(context.Background()); err != nil {
			t.Fatal(err)
		}
		var title string
		w.DB.QueryRow(context.Background(), "SELECT title FROM memes WHERE id=$1", meme).Scan(&title)
		if title != "" {
			t.Fatal("reanalysis applied automatically")
		}
	})
}

func TestClaimSnapshotsCurrentRevision(t *testing.T) {
	w, meme, jobID := fixture(t, "text")
	exec(t, w.DB, "UPDATE memes SET body_text='latest body',content_revision=2 WHERE id=$1", meme)
	w.Analyzer = analyzeFunc(func(ctx context.Context, input Input) (Response, error) {
		if input.Body != "latest body" {
			t.Error("analysis input was stale")
		}
		return Response{Result: Result{SafetyRating: "sfw", Title: "Current result", Tags: []string{}}, Model: "test"}, nil
	})
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	var inputRevision, revision int64
	var title string
	if err := w.DB.QueryRow(context.Background(), "SELECT j.input_revision,m.content_revision,m.title FROM jobs j JOIN memes m ON m.id=j.meme_id WHERE j.id=$1", jobID).Scan(&inputRevision, &revision, &title); err != nil {
		t.Fatal(err)
	}
	if inputRevision != 2 || revision != 3 || title != "Current result" {
		t.Fatal("revision and input snapshot diverged")
	}
}

func TestExhaustedLeaseDoesNotCallProvider(t *testing.T) {
	w, _, jobID := fixture(t, "text")
	exec(t, w.DB, "UPDATE jobs SET status='processing',attempts=max_attempts,lock_token=$2,locked_until=now()-interval '1 second' WHERE id=$1", jobID, uuid.New())
	w.Analyzer = analyzeFunc(func(context.Context, Input) (Response, error) {
		t.Error("exhausted job sent to provider")
		return Response{}, nil
	})
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	var status, code string
	var attempts int
	if err := w.DB.QueryRow(context.Background(), "SELECT status,error_code,attempts FROM jobs WHERE id=$1", jobID).Scan(&status, &code, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || code != "analysis_attempts_exhausted" || attempts != 3 {
		t.Fatal("exhausted lease was not finalized")
	}
}

func TestOversizeImageNeverReachesProvider(t *testing.T) {
	w, _, jobID := fixture(t, "image")
	w.MaxImageBytes = 4
	w.Store = readFunc(func(context.Context, string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("too long")), nil
	})
	w.Analyzer = analyzeFunc(func(context.Context, Input) (Response, error) {
		t.Error("oversize image sent to provider")
		return Response{}, nil
	})
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	var status, code string
	if err := w.DB.QueryRow(context.Background(), "SELECT status,error_code FROM jobs WHERE id=$1", jobID).Scan(&status, &code); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || code != "invalid_analysis_image" {
		t.Fatal("oversize image not rejected")
	}
}

func TestGIFAnalysisUsesStaticPreview(t *testing.T) {
	w, meme, _ := fixture(t, "image")
	exec(t, w.DB, "UPDATE memes SET mime_type='image/gif',thumbnail_object_key='thumbnails/frame.png' WHERE id=$1", meme)
	w.Store = readFunc(func(ctx context.Context, key string) (io.ReadCloser, error) {
		if key != "thumbnails/frame.png" {
			t.Error("animated GIF must use its static analysis preview")
		}
		return io.NopCloser(bytes.NewReader([]byte("\x89PNG\r\n\x1a\n"))), nil
	})
	w.Analyzer = analyzeFunc(func(ctx context.Context, input Input) (Response, error) {
		if input.MIME != "image/png" {
			t.Error("unexpected analysis format")
		}
		return Response{Result: Result{SafetyRating: "sfw", Tags: []string{"움짤"}}, Model: "test"}, nil
	})
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRunCancellationReleasesActiveJob(t *testing.T) {
	w, _, jobID := fixture(t, "text")
	started := make(chan struct{})
	w.Analyzer = analyzeFunc(func(ctx context.Context, input Input) (Response, error) {
		close(started)
		<-ctx.Done()
		return Response{}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx, 2) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
	var status string
	var released bool
	if err := w.DB.QueryRow(context.Background(), "SELECT status,lock_token IS NULL AND locked_until IS NULL FROM jobs WHERE id=$1", jobID).Scan(&status, &released); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || !released {
		t.Fatal("interrupted job was not released for retry")
	}
}

func TestRunStopsWhenAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { defer close(done); (&Worker{}).Run(ctx, 2) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("idle worker did not stop")
	}
}

func TestSafetyRatingPersistsOnEveryAnalysis(t *testing.T) {
	w, meme, _ := fixture(t, "text")
	for _, rating := range []string{"nsfw", "sfw", "unknown"} {
		w.Analyzer = analyzeFunc(func(context.Context, Input) (Response, error) {
			return Response{Model: "test", Result: Result{SafetyRating: rating, Tags: []string{}}}, nil
		})
		if worked, err := w.RunOne(context.Background()); err != nil || !worked {
			t.Fatalf("analysis: worked=%v err=%v", worked, err)
		}
		var stored, history string
		if err := w.DB.QueryRow(context.Background(), "SELECT safety_rating FROM memes WHERE id=$1", meme).Scan(&stored); err != nil || stored != rating {
			t.Fatalf("rating: %q %v", stored, err)
		}
		if err := w.DB.QueryRow(context.Background(), "SELECT result->>'safety_rating' FROM jobs WHERE meme_id=$1 ORDER BY created_at DESC LIMIT 1", meme).Scan(&history); err != nil || history != rating {
			t.Fatalf("history: %q %v", history, err)
		}
		if rating != "unknown" {
			exec(t, w.DB, "INSERT INTO jobs(id,meme_id,type,provider,requested_model,prompt_version,input_revision) SELECT $1,id,'analyze_content','openai','test','v1',content_revision FROM memes WHERE id=$2", uuid.New(), meme)
		}
	}
}
