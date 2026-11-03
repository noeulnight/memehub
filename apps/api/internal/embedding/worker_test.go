package embedding

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/noeulnight/memehub/apps/api/internal/database"
)

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
	schema := "embedding_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	if err = database.Migrate(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	db, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}
func sql(t *testing.T, db *pgxpool.Pool, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), q, args...); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T) (*pgxpool.Pool, uuid.UUID) {
	db := testDB(t)
	user, meme := uuid.New(), uuid.New()
	sql(t, db, `INSERT INTO users(id,oidc_issuer,oidc_subject) VALUES($1,'issuer','u')`, user)
	sql(t, db, `INSERT INTO memes(id,created_by,content_type,title,body_text,status,analysis_status) VALUES($1,$2,'text','고양이','원래 본문','ready','completed')`, meme, user)
	return db, meme
}
func TestWorkerBackfillReuseAndChanges(t *testing.T) {
	db, id := fixture(t)
	calls := 0
	w := &Worker{DB: db, Embedder: embedFunc(func(ctx context.Context, input string) (Result, error) {
		calls++
		return Result{Vector: unitVector(), Tokens: 5}, nil
	})}
	run := func(want bool) {
		t.Helper()
		worked, err := w.RunOne(context.Background())
		if err != nil || worked != want {
			t.Fatal(worked, err)
		}
	}
	run(true)
	run(false)
	// Attribution-only edit changes the revision, but not the embedding input.
	sql(t, db, `UPDATE memes SET source_name='source',content_revision=1 WHERE id=$1`, id)
	run(true)
	if calls != 1 {
		t.Fatal("unchanged input was billed again")
	}
	sql(t, db, `UPDATE memes SET description='화난 고양이',content_revision=2 WHERE id=$1`, id)
	run(true)
	var rev, tokens int64
	if err := db.QueryRow(context.Background(), `SELECT content_revision,input_tokens FROM meme_embeddings WHERE meme_id=$1`, id).Scan(&rev, &tokens); err != nil || rev != 2 || tokens != 5 || calls != 2 {
		t.Fatal(rev, tokens, calls, err)
	}
	sql(t, db, `UPDATE memes SET deleted_at=now(),content_revision=3 WHERE id=$1`, id)
	run(false)
}
func TestWorkerRejectsStaleResultsAndRetries(t *testing.T) {
	db, id := fixture(t)
	calls := 0
	w := &Worker{DB: db, Embedder: embedFunc(func(ctx context.Context, input string) (Result, error) {
		calls++
		if calls == 1 {
			sql(t, db, `UPDATE memes SET description='edited',content_revision=1 WHERE id=$1`, id)
		}
		return Result{Vector: unitVector()}, nil
	})}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM meme_embeddings`).Scan(&n); err != nil || n != 0 {
		t.Fatal("stale vector persisted", n, err)
	}
	if worked, err := w.RunOne(context.Background()); err != nil || !worked {
		t.Fatal(worked, err)
	}
	sql(t, db, `UPDATE memes SET description='edited again',content_revision=2 WHERE id=$1`, id)
	w.Embedder = embedFunc(func(context.Context, string) (Result, error) { return Result{}, &Failure{Retryable: true} })
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	var status string
	var attempts int
	if err := db.QueryRow(context.Background(), `SELECT status,attempts FROM jobs WHERE type='embed_content' AND input_revision=2`).Scan(&status, &attempts); err != nil || status != "queued" || attempts != 1 {
		t.Fatal(status, attempts, err)
	}
	w.Embedder = embedFunc(func(context.Context, string) (Result, error) { return Result{Vector: unitVector()}, nil })
	sql(t, db, `UPDATE jobs SET run_after=now() WHERE type='embed_content' AND input_revision=2`)
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestWorkerLeasePreventsDuplicateClaims(t *testing.T) {
	db, _ := fixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	w := &Worker{DB: db, Embedder: embedFunc(func(context.Context, string) (Result, error) {
		close(started)
		<-release
		return Result{Vector: unitVector()}, nil
	})}
	done := make(chan error, 1)
	go func() { _, err := w.RunOne(context.Background()); done <- err }()
	<-started
	if worked, err := w.RunOne(context.Background()); err != nil || worked {
		close(release)
		t.Fatal("leased job reclaimed", worked, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
