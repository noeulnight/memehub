package server

import (
	"bytes"
	"context"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"

	"github.com/google/uuid"
	"github.com/noeulnight/memehub/apps/api/internal/config"
)

func TestDoubleMemeEndpoint(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
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
	app, err := New(context.Background(), cfg, pool)
	if err != nil {
		t.Fatal(err)
	}
	h := app.Handler()
	owner, left, right, text := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()
	if _, err = pool.Exec(ctx, "INSERT INTO users(id,oidc_issuer,oidc_subject) VALUES($1,'test','double')", owner); err != nil {
		t.Fatal(err)
	}
	data, hash := testPNG(t)
	for _, id := range []uuid.UUID{left, right} {
		key := "originals/" + id.String() + ".png"
		_, err = pool.Exec(ctx, `INSERT INTO memes(id,created_by,content_type,original_filename,original_object_key,mime_type,size_bytes,width,height,sha256,status) VALUES($1,$2,'image','test.png',$3,'image/png',$4,1000,500,$5,'ready')`, id, owner, key, len(data), hash)
		if err != nil {
			t.Fatal(err)
		}
		if err = app.store.Put(ctx, key, "image/png", data); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, "INSERT INTO memes(id,created_by,content_type,body_text,status) VALUES($1,$2,'text','not an image','ready')", text, owner); err != nil {
		t.Fatal(err)
	}
	path := "/api/double/" + left.String() + "/" + right.String() + "/file.png"
	response := request(t, h, "GET", path, nil, nil, "")
	mustStatus(t, response, 200)
	img, err := png.Decode(bytes.NewReader(response.Body.Bytes()))
	if err != nil || img.Bounds().Dx() != 400 || img.Bounds().Dy() != 200 {
		t.Fatal("invalid composition", err)
	}
	if response.Header().Get("Content-Type") != "image/png" || response.Header().Get("Content-Length") == "" || response.Header().Get("ETag") == "" {
		t.Fatal(response.Header())
	}
	conditional := httptest.NewRequest("GET", path, nil)
	conditional.Header.Set("If-None-Match", response.Header().Get("ETag"))
	unchanged := httptest.NewRecorder()
	h.ServeHTTP(unchanged, conditional)
	mustStatus(t, unchanged, http.StatusNotModified)
	mustStatus(t, request(t, h, "GET", "/api/double/invalid/"+right.String()+"/file.png", nil, nil, ""), 400)
	mustStatus(t, request(t, h, "GET", "/api/double/"+text.String()+"/"+right.String()+"/file.png", nil, nil, ""), 404)
	mustStatus(t, request(t, h, "GET", "/api/double/"+left.String()+"/"+right.String()+"/file.gif", nil, nil, ""), 400)
	for range cap(doubleSlots) {
		doubleSlots <- struct{}{}
	}
	busy := request(t, h, "GET", path, nil, nil, "")
	for range cap(doubleSlots) {
		<-doubleSlots
	}
	mustStatus(t, busy, 503)
	if busy.Header().Get("Cache-Control") != "no-store" || busy.Header().Get("Retry-After") == "" {
		t.Fatal("busy response was cacheable")
	}
	if _, err = pool.Exec(ctx, "UPDATE memes SET deleted_at=now() WHERE id=$1", left); err != nil {
		t.Fatal(err)
	}
	deleted := httptest.NewRecorder()
	h.ServeHTTP(deleted, conditional)
	mustStatus(t, deleted, 404)
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM memes").Scan(&count); err != nil || count != 3 {
		t.Fatal("composition persisted a new meme", count, err)
	}
}
