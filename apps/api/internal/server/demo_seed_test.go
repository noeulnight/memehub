package server

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/noeulnight/memehub/apps/api/internal/config"
)

func TestDemoSamples(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	cfg, err := config.Parse(func(key string) string {
		if key == "DEMO_MODE" {
			return "true"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg.DemoDataDir = t.TempDir()
	app, err := New(ctx, cfg, pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	if err := app.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM memes").Scan(&count); err != nil || count != len(demoSamples) {
		t.Fatalf("duplicate or missing samples: %d %v", count, err)
	}
	for _, sample := range demoSamples {
		id := demoSampleID(sample.slug)
		meme, err := scanMeme(pool.QueryRow(ctx, "SELECT "+memeColumns+" FROM memes m WHERE m.id=$1", id))
		if err != nil {
			t.Fatal(err)
		}
		if meme.Status != "ready" || meme.AnalysisStatus != "not_requested" || len(meme.Tags) < 2 || meme.CreatedByUsername == nil || *meme.CreatedByUsername != "demo" || meme.ExtractedText == "" {
			t.Fatalf("invalid sample: %+v", meme)
		}
		for _, key := range []string{"originals/" + id.String() + ".png", "thumbnails/" + id.String() + ".png"} {
			data, err := os.ReadFile(filepath.Join(cfg.DemoDataDir, key))
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil || img.Bounds().Dx() < 600 {
				t.Fatalf("invalid sample PNG %s: %v", key, err)
			}
		}
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM jobs").Scan(&count); err != nil || count != 0 {
		t.Fatalf("seed queued external jobs: %d %v", count, err)
	}
	editedID, deletedID := demoSampleID(demoSamples[0].slug), demoSampleID(demoSamples[1].slug)
	if _, err := pool.Exec(ctx, "UPDATE memes SET title='My edited title' WHERE id=$1", editedID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE memes SET deleted_at=now() WHERE id=$1", deletedID); err != nil {
		t.Fatal(err)
	}
	if err := app.SeedDemo(ctx); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := pool.QueryRow(ctx, "SELECT title FROM memes WHERE id=$1", editedID).Scan(&title); err != nil || title != "My edited title" {
		t.Fatal("seed overwrote edited sample", err)
	}
	var deleted bool
	if err := pool.QueryRow(ctx, "SELECT deleted_at IS NOT NULL FROM memes WHERE id=$1", deletedID).Scan(&deleted); err != nil || !deleted {
		t.Fatal("seed resurrected deleted sample", err)
	}
}

func TestStandardModeDoesNotSeed(t *testing.T) {
	// No database or filesystem should be touched outside demo mode.
	if err := (&Server{}).SeedDemo(context.Background()); err != nil {
		t.Fatal(err)
	}
}
