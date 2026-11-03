package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/noeulnight/memehub/apps/api/internal/config"
	"github.com/noeulnight/memehub/apps/api/internal/storage"
)

func TestDemoRejectsProduction(t *testing.T) {
	if _, err := New(context.Background(), config.Config{DemoMode: true, Env: "production"}, nil); err == nil {
		t.Fatal("production accepted demo mode")
	}
}

func TestDemoLocalImageLifecycle(t *testing.T) {
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
	login := request(t, h, "GET", "/api/auth/login", nil, nil, "")
	mustStatus(t, login, 303)
	cookies := login.Result().Cookies()
	csrf := ""
	for _, c := range cookies {
		if c.Name == csrfCookie {
			csrf = c.Value
		}
	}
	data, hash := testPNG(t)
	input := map[string]any{"content_type": "image", "original_filename": "demo.png", "mime_type": "image/png", "size_bytes": len(data), "sha256": hash, "title": "Local demo image"}
	created := request(t, h, "POST", "/api/memes", input, cookies, csrf)
	mustStatus(t, created, 201)
	var reservation struct {
		Meme
		Upload storage.Upload `json:"upload"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &reservation); err != nil {
		t.Fatal(err)
	}
	put := httptest.NewRequest("PUT", reservation.Upload.URL, bytes.NewReader(data))
	put.Header.Set("Content-Type", "image/png")
	uploaded := httptest.NewRecorder()
	h.ServeHTTP(uploaded, put)
	mustStatus(t, uploaded, 200)
	path := "/api/memes/" + reservation.ID.String()
	completed := request(t, h, "POST", path+"/upload-completion", nil, cookies, csrf)
	mustStatus(t, completed, 200)
	var meme Meme
	if err := json.Unmarshal(completed.Body.Bytes(), &meme); err != nil || meme.Status != "ready" || meme.AnalysisStatus != "not_requested" {
		t.Fatalf("unexpected image: %s", completed.Body.String())
	}
	disk, err := os.ReadFile(filepath.Join(cfg.DemoDataDir, "originals", meme.ID.String()+".png"))
	if err != nil || !bytes.Equal(disk, data) {
		t.Fatal("original not persisted to local data directory", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.DemoDataDir, "staging", meme.ID.String())); !os.IsNotExist(err) {
		t.Fatal("staging was not cleaned up")
	}
	media := request(t, h, "GET", path+"/media", nil, nil, "")
	mustStatus(t, media, 200)
	var urls struct{ Original, Thumbnail storage.SignedURL }
	if err := json.Unmarshal(media.Body.Bytes(), &urls); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{urls.Original.URL, urls.Thumbnail.URL} {
		u, err := url.Parse(target)
		if err != nil {
			t.Fatal(err)
		}
		w := request(t, h, "GET", u.RequestURI(), nil, nil, "")
		mustStatus(t, w, 200)
		if target == urls.Original.URL && !bytes.Equal(w.Body.Bytes(), data) {
			t.Fatal("original differs")
		}
	}
	var jobs int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM jobs WHERE meme_id=$1", meme.ID).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("demo queued analysis jobs: %d %v", jobs, err)
	}
	mustStatus(t, request(t, h, "DELETE", path, nil, cookies, csrf), 204)
	mustStatus(t, request(t, h, "GET", path+"/media", nil, nil, ""), 404)
}

func TestDemoSessionAndTextLifecycle(t *testing.T) {
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
	mustStatus(t, request(t, h, "GET", "/api/auth/me", nil, nil, ""), 401)
	mustStatus(t, request(t, h, "GET", "/api/auth/callback", nil, nil, ""), 404)
	login := request(t, h, "GET", "/api/auth/login", nil, nil, "")
	mustStatus(t, login, http.StatusSeeOther)
	if login.Header().Get("Location") != cfg.AppBaseURL+"/" {
		t.Fatal("unexpected demo redirect")
	}
	cookies := login.Result().Cookies()
	csrf := ""
	for _, c := range cookies {
		if c.Name == csrfCookie {
			csrf = c.Value
		}
	}
	if len(csrf) != 43 {
		t.Fatal("missing CSRF cookie")
	}
	me := request(t, h, "GET", "/api/auth/me", nil, cookies, "")
	mustStatus(t, me, 200)
	var user struct {
		Username string `json:"username"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &user); err != nil || user.Username != "demo" || !user.IsAdmin {
		t.Fatalf("unexpected demo user: %s", me.Body.String())
	}
	input := map[string]any{"content_type": "text", "title": "Demo", "body_text": "Hello demo", "tags": []string{"demo"}}
	mustStatus(t, request(t, h, "POST", "/api/memes", input, nil, ""), 401)
	mustStatus(t, request(t, h, "POST", "/api/memes", input, cookies, ""), 403)
	created := request(t, h, "POST", "/api/memes", input, cookies, csrf)
	mustStatus(t, created, 201)
	var meme Meme
	if err := json.Unmarshal(created.Body.Bytes(), &meme); err != nil || meme.AnalysisStatus != "not_requested" {
		t.Fatalf("unexpected demo meme: %s", created.Body.String())
	}
	var jobs int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM jobs WHERE meme_id=$1", meme.ID).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("demo queued external jobs: %d %v", jobs, err)
	}
	path := "/api/memes/" + meme.ID.String()
	mustStatus(t, request(t, h, "GET", path, nil, nil, ""), 200)
	input["content_revision"] = 0
	input["body_text"] = "Edited demo"
	mustStatus(t, request(t, h, "PUT", path, input, cookies, csrf), 200)
	mustStatus(t, request(t, h, "GET", "/api/memes?username=demo", nil, nil, ""), 200)
	mustStatus(t, request(t, h, "GET", "/api/admin/settings", nil, cookies, ""), 200)
	mustStatus(t, request(t, h, "POST", "/api/admin/memes/"+meme.ID.String()+"/analyses", map[string]any{"content_revision": 1}, cookies, csrf), 503)
	mustStatus(t, request(t, h, "DELETE", path, nil, cookies, csrf), 204)
	// Signing in again rotates the session but reuses the same demo account.
	rotated := request(t, h, "GET", "/api/auth/login", nil, cookies, "")
	mustStatus(t, rotated, 303)
	mustStatus(t, request(t, h, "GET", "/api/auth/me", nil, cookies, ""), 401)
	newCookies := rotated.Result().Cookies()
	for _, c := range newCookies {
		if c.Name == csrfCookie {
			csrf = c.Value
		}
	}
	mustStatus(t, request(t, h, "POST", "/api/auth/logout", nil, newCookies, csrf), 204)
	mustStatus(t, request(t, h, "GET", "/api/auth/me", nil, newCookies, ""), 401)
}
