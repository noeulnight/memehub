package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/noeulnight/memehub/apps/api/internal/config"
	"github.com/noeulnight/memehub/apps/api/internal/database"
)

// TEST_DATABASE_URL must point to an isolated database; each test uses a private schema.
func testPool(t *testing.T) *pgxpool.Pool {
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
	schema := "test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
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
	if err = database.Migrate(ctx, u.String()); err != nil {
		t.Fatalf("repeated migration: %v", err)
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type fakeIssuer struct {
	server *httptest.Server
	mu     sync.Mutex
	codes  map[string]struct{ nonce, challenge, subject string }
}

func newIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "test-key"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIssuer{codes: make(map[string]struct{ nonce, challenge, subject string })}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"issuer": f.server.URL, "authorization_endpoint": f.server.URL + "/authorize", "token_endpoint": f.server.URL + "/token", "jwks_uri": f.server.URL + "/keys", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(400)
			return
		}
		f.mu.Lock()
		entry, ok := f.codes[r.Form.Get("code")]
		delete(f.codes, r.Form.Get("code"))
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != entry.challenge {
			problem(w, 400, "invalid_grant")
			return
		}
		payload, _ := json.Marshal(map[string]any{"iss": f.server.URL, "aud": "test-client", "sub": entry.subject, "nonce": entry.nonce, "name": "Test User", "preferred_username": entry.subject, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()})
		signed, err := signer.Sign(payload)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		raw, err := signed.CompactSerialize()
		if err != nil {
			w.WriteHeader(500)
			return
		}
		writeJSON(w, 200, map[string]any{"access_token": "unused", "token_type": "Bearer", "id_token": raw, "expires_in": 3600})
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}
func request(t *testing.T, handler http.Handler, method, path string, body any, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	r := httptest.NewRequest(method, path, reader)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	if csrf != "" {
		r.Header.Set("Origin", "http://localhost:4321")
		r.Header.Set("X-CSRF-Token", csrf)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}
func mustStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status %d, want %d: %s", w.Code, want, w.Body.String())
	}
}
func signIn(t *testing.T, h http.Handler, f *fakeIssuer, subject string, wrongNonce bool) ([]*http.Cookie, string) {
	t.Helper()
	w := request(t, h, "GET", "/api/auth/login", nil, nil, "")
	mustStatus(t, w, 302)
	auth, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	q := auth.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" {
		t.Fatal("PKCE or nonce missing")
	}
	code := uuid.NewString()
	nonce := q.Get("nonce")
	if wrongNonce {
		nonce = "wrong"
	}
	f.mu.Lock()
	f.codes[code] = struct{ nonce, challenge, subject string }{nonce, q.Get("code_challenge"), subject}
	f.mu.Unlock()
	callback := "/api/auth/callback?state=" + url.QueryEscape(q.Get("state")) + "&code=" + code
	result := request(t, h, "GET", callback, nil, w.Result().Cookies(), "")
	if wrongNonce {
		mustStatus(t, result, 401)
		return nil, ""
	}
	mustStatus(t, result, 303)
	cookies := result.Result().Cookies()
	csrf := ""
	for _, c := range cookies {
		if c.Name == csrfCookie {
			csrf = c.Value
		}
		if c.Name == sessionCookie && !c.HttpOnly {
			t.Fatal("session cookie must be HttpOnly")
		}
	}
	replay := request(t, h, "GET", callback, nil, w.Result().Cookies(), "")
	mustStatus(t, replay, 400)
	return cookies, csrf
}
func TestOIDCAndTextMemeLifecycle(t *testing.T) {
	pool := testPool(t)
	issuer := newIssuer(t)
	cfg := config.Config{AppBaseURL: "http://localhost:4321", OIDCIssuer: issuer.server.URL, OIDCClientID: "test-client", OIDCRedirectURL: "http://localhost:4321/api/auth/callback", SessionTTL: time.Hour, TextMaxLength: 10000, OpenAIModel: "test-model"}
	app, err := New(context.Background(), cfg, pool)
	if err != nil {
		t.Fatal(err)
	}
	h := app.Handler()
	mustStatus(t, request(t, h, "GET", "/api/health/ready", nil, nil, ""), 200)
	mustStatus(t, request(t, h, "POST", "/api/memes", map[string]string{}, nil, ""), 401)
	mustStatus(t, request(t, h, "GET", "/api/auth/callback?state=bad&code=bad", nil, nil, ""), 400)
	signIn(t, h, issuer, "nonce-attacker", true)
	cookies, csrf := signIn(t, h, issuer, "author", false)
	other, otherCSRF := signIn(t, h, issuer, "other", false)
	mustStatus(t, request(t, h, "GET", "/api/auth/me", nil, cookies, ""), 200)
	wrongOrigin := httptest.NewRequest("POST", "/api/auth/logout", nil)
	for _, c := range cookies {
		wrongOrigin.AddCookie(c)
	}
	wrongOrigin.Header.Set("Origin", "https://other.example")
	wrongOrigin.Header.Set("X-CSRF-Token", csrf)
	denied := httptest.NewRecorder()
	h.ServeHTTP(denied, wrongOrigin)
	mustStatus(t, denied, 403)

	input := map[string]any{"content_type": "text", "title": "A literal 100% meme", "body_text": "First line\nSecond line", "source_url": "https://example.com/post/1", "source_name": "Example", "source_author": "Original creator", "tags": []string{"Work", "work", "Monday"}}
	mustStatus(t, request(t, h, "POST", "/api/memes", input, cookies, ""), 403)
	mustStatus(t, request(t, h, "POST", "/api/memes", input, cookies, otherCSRF), 403)
	invalid := map[string]any{"content_type": "text", "body_text": "body", "source_url": "javascript:alert(1)"}
	mustStatus(t, request(t, h, "POST", "/api/memes", invalid, cookies, csrf), 422)
	created := request(t, h, "POST", "/api/memes", input, cookies, csrf)
	mustStatus(t, created, 201)
	var meme Meme
	if err = json.Unmarshal(created.Body.Bytes(), &meme); err != nil {
		t.Fatal(err)
	}
	if len(meme.Tags) != 2 || meme.BodyText == nil || *meme.BodyText != input["body_text"] || meme.AnalysisStatus != "queued" {
		t.Fatalf("unexpected meme: %+v", meme)
	}
	if meme.CreatedByName == nil || *meme.CreatedByName != "Test User" || meme.CreatedAt.IsZero() {
		t.Fatalf("uploader metadata missing: %+v", meme)
	}
	var jobs int
	if err = pool.QueryRow(context.Background(), "SELECT count(*) FROM jobs WHERE meme_id=$1 AND provider='openai' AND requested_model='test-model'", meme.ID).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("analysis job missing: %d %v", jobs, err)
	}
	path := "/api/memes/" + meme.ID.String()
	mustStatus(t, request(t, h, "GET", path, nil, nil, ""), 200)
	list := request(t, h, "GET", "/api/memes?q=100%25&tag=WORK&tag=monday", nil, nil, "")
	mustStatus(t, list, 200)
	var page struct {
		Items []Meme  `json:"items"`
		Next  *string `json:"next_cursor"`
	}
	if err = json.Unmarshal(list.Body.Bytes(), &page); err != nil || len(page.Items) != 1 {
		t.Fatalf("search failed: %s", list.Body.String())
	}
	if page.Items[0].CreatedByName == nil || *page.Items[0].CreatedByName != "Test User" || !page.Items[0].CreatedAt.Equal(meme.CreatedAt) {
		t.Fatal("list lost uploader metadata")
	}
	if _, err = pool.Exec(context.Background(), "UPDATE users SET display_name=NULL WHERE id=$1", meme.CreatedBy); err != nil {
		t.Fatal(err)
	}
	unnamed := request(t, h, "GET", path, nil, nil, "")
	mustStatus(t, unnamed, 200)
	var unnamedMeme Meme
	if err = json.Unmarshal(unnamed.Body.Bytes(), &unnamedMeme); err != nil || unnamedMeme.CreatedByName != nil {
		t.Fatal("uploader without a display name must remain readable")
	}
	noMatch := request(t, h, "GET", "/api/memes?q=100_", nil, nil, "")
	mustStatus(t, noMatch, 200)
	if err = json.Unmarshal(noMatch.Body.Bytes(), &page); err != nil || len(page.Items) != 0 {
		t.Fatal("wildcard was not escaped")
	}
	input["content_revision"] = 0
	input["body_text"] = "Updated body"
	mustStatus(t, request(t, h, "PUT", path, input, other, otherCSRF), 404)
	mustStatus(t, request(t, h, "DELETE", path, nil, other, otherCSRF), 404)
	mustStatus(t, request(t, h, "PUT", path, input, cookies, csrf), 200)
	mustStatus(t, request(t, h, "PUT", path, input, cookies, csrf), 409)
	for i := 0; i < 2; i++ {
		mustStatus(t, request(t, h, "POST", "/api/memes", input, cookies, csrf), 201)
	}
	first := request(t, h, "GET", "/api/memes?limit=1", nil, nil, "")
	mustStatus(t, first, 200)
	if err = json.Unmarshal(first.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Next == nil {
		t.Fatalf("cursor missing: %s", first.Body.String())
	}
	firstID := page.Items[0].ID
	second := request(t, h, "GET", "/api/memes?limit=1&cursor="+url.QueryEscape(*page.Next), nil, nil, "")
	mustStatus(t, second, 200)
	if err = json.Unmarshal(second.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].ID == firstID {
		t.Fatal("cursor repeated a meme")
	}
	mustStatus(t, request(t, h, "GET", "/api/memes?cursor=invalid", nil, nil, ""), 400)
	mustStatus(t, request(t, h, "DELETE", path, nil, cookies, csrf), 204)
	mustStatus(t, request(t, h, "GET", path, nil, nil, ""), 404)

	expired, expiredCSRF := signIn(t, h, issuer, "expired", false)
	for _, c := range expired {
		if c.Name == sessionCookie {
			if _, err := pool.Exec(context.Background(), "UPDATE sessions SET expires_at=now()-interval '1 second' WHERE token_hash=$1", hash(c.Value)); err != nil {
				t.Fatal(err)
			}
		}
	}
	mustStatus(t, request(t, h, "GET", "/api/auth/me", nil, expired, ""), 401)
	mustStatus(t, request(t, h, "POST", "/api/auth/logout", nil, expired, expiredCSRF), 401)
	mustStatus(t, request(t, h, "POST", "/api/auth/logout", nil, cookies, csrf), 204)
	mustStatus(t, request(t, h, "GET", "/api/auth/me", nil, cookies, ""), 401)
}
func TestSchemaRejectsInvalidContent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	user := uuid.New()
	if _, err := pool.Exec(ctx, "INSERT INTO users(id,oidc_issuer,oidc_subject) VALUES($1,'https://issuer.example','subject')", user); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"INSERT INTO memes(id,created_by,content_type,status,body_text) VALUES($1,$2,'text','ready',NULL)",
		"INSERT INTO memes(id,created_by,content_type,status,body_text) VALUES($1,$2,'text','ready','   ')",
		"INSERT INTO memes(id,created_by,content_type,status,body_text,original_object_key) VALUES($1,$2,'text','ready','body','image-key')",
		"INSERT INTO memes(id,created_by,content_type,status) VALUES($1,$2,'image','ready')",
	} {
		if _, err := pool.Exec(ctx, query, uuid.New(), user); err == nil {
			t.Fatal("invalid content accepted")
		}
	}
	id := uuid.New()
	if _, err := pool.Exec(ctx, "INSERT INTO memes(id,created_by,content_type,status,body_text) VALUES($1,$2,'text','ready','body')", id, user); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		_, err := pool.Exec(ctx, "INSERT INTO jobs(id,meme_id,type,provider,requested_model,prompt_version,input_revision) VALUES($1,$2,'analyze_content','openai','model','v1',0)", uuid.New(), id)
		if (err != nil) != (i == 1) {
			t.Fatal(fmt.Sprintf("active job uniqueness: %v", err))
		}
	}
}

func TestMemeManagementPermissions(t *testing.T) {
	pool := testPool(t)
	issuer := newIssuer(t)
	cfg := config.Config{AppBaseURL: "http://localhost:4321", OIDCIssuer: issuer.server.URL, OIDCClientID: "test-client", OIDCRedirectURL: "http://localhost:4321/api/auth/callback", SessionTTL: time.Hour, TextMaxLength: 10000, OpenAIModel: "test-model"}
	app, err := New(context.Background(), cfg, pool)
	if err != nil {
		t.Fatal(err)
	}
	h := app.Handler()
	owner, ownerCSRF := signIn(t, h, issuer, "owner", false)
	other, otherCSRF := signIn(t, h, issuer, "other", false)
	admin, adminCSRF := signIn(t, h, issuer, "admin", false)
	if _, err = pool.Exec(context.Background(), "UPDATE users SET is_admin=true WHERE oidc_subject='admin'"); err != nil {
		t.Fatal(err)
	}
	me := request(t, h, "GET", "/api/auth/me", nil, admin, "")
	mustStatus(t, me, 200)
	var identity struct {
		IsAdmin bool `json:"is_admin"`
	}
	if err = json.Unmarshal(me.Body.Bytes(), &identity); err != nil || !identity.IsAdmin {
		t.Fatal("admin not exposed to UI")
	}
	var ownerID uuid.UUID
	if err = pool.QueryRow(context.Background(), "SELECT id FROM users WHERE oidc_subject='owner'").Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"text", "image"} {
		t.Run(kind, func(t *testing.T) {
			id := uuid.New()
			if kind == "text" {
				_, err = pool.Exec(context.Background(), "INSERT INTO memes(id,created_by,content_type,body_text,status) VALUES($1,$2,'text','Original','ready')", id, ownerID)
			} else {
				_, err = pool.Exec(context.Background(), `INSERT INTO memes(id,created_by,content_type,original_filename,original_object_key,mime_type,size_bytes,width,height,sha256,status) VALUES($1,$2,'image','test.png',$3,'image/png',100,10,10,$4,'ready')`, id, ownerID, id.String(), strings.Repeat("a", 64))
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(context.Background(), "UPDATE memes SET safety_rating='nsfw' WHERE id=$1", id); err != nil {
				t.Fatal(err)
			}
			before := request(t, h, "GET", "/api/memes/"+id.String(), nil, nil, "")
			mustStatus(t, before, 200)
			var classified Meme
			if err = json.Unmarshal(before.Body.Bytes(), &classified); err != nil || classified.SafetyRating != "nsfw" {
				t.Fatal("classification missing from detail")
			}
			path := "/api/memes/" + id.String()
			input := map[string]any{"content_type": kind, "title": "Edited", "content_revision": 0, "tags": []string{"Exact tag"}}
			if kind == "text" {
				input["body_text"] = "Changed"
			} else {
				input["extracted_text"] = "Read from image"
			}
			mustStatus(t, request(t, h, "PUT", path, input, nil, ""), 401)
			mustStatus(t, request(t, h, "PUT", path, input, other, otherCSRF), 404)
			mustStatus(t, request(t, h, "DELETE", path, nil, other, otherCSRF), 404)
			mustStatus(t, request(t, h, "PUT", path, input, admin, ""), 403)
			mustStatus(t, request(t, h, "DELETE", path, nil, admin, ""), 403)
			mustStatus(t, request(t, h, "PUT", path, input, owner, ownerCSRF), 200)
			mustStatus(t, request(t, h, "PUT", path, input, owner, ownerCSRF), 409)
			input["content_revision"] = 1
			updated := request(t, h, "PUT", path, input, admin, adminCSRF)
			mustStatus(t, updated, 200)
			var meme Meme
			if err = json.Unmarshal(updated.Body.Bytes(), &meme); err != nil || meme.Revision != 2 || meme.SafetyRating != "unknown" || len(meme.Tags) != 1 || meme.Tags[0] != "Exacttag" {
				t.Fatal("edit did not preserve fields and revision")
			}
			if kind == "image" && (meme.BodyText != nil || meme.ExtractedText != "Read from image") {
				t.Fatal("invalid image metadata")
			}
			input["content_revision"] = 2
			input["content_type"] = "image"
			if kind == "image" {
				input["body_text"] = "not permitted"
			}
			mustStatus(t, request(t, h, "PUT", path, input, owner, ownerCSRF), 422)
			filtered := request(t, h, "GET", "/api/memes?tag=Exact+tag", nil, nil, "")
			mustStatus(t, filtered, 200)
			var page struct {
				Items []Meme `json:"items"`
			}
			if err = json.Unmarshal(filtered.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].ID != id || page.Items[0].SafetyRating != "unknown" {
				t.Fatal("exact tag filter failed")
			}
			if _, err = pool.Exec(context.Background(), "UPDATE users SET is_admin=false WHERE oidc_subject='admin'"); err != nil {
				t.Fatal(err)
			}
			mustStatus(t, request(t, h, "DELETE", path, nil, admin, adminCSRF), 404)
			if _, err = pool.Exec(context.Background(), "UPDATE users SET is_admin=true WHERE oidc_subject='admin'"); err != nil {
				t.Fatal(err)
			}
			mustStatus(t, request(t, h, "DELETE", path, nil, admin, adminCSRF), 204)
			mustStatus(t, request(t, h, "GET", path, nil, nil, ""), 404)
			mustStatus(t, request(t, h, "GET", path+"/media", nil, nil, ""), 404)
			mustStatus(t, request(t, h, "PUT", path, input, owner, ownerCSRF), 404)
			mustStatus(t, request(t, h, "DELETE", path, nil, owner, ownerCSRF), 404)
			if _, err = pool.Exec(context.Background(), "UPDATE memes SET deleted_at=NULL WHERE id=$1", id); err != nil {
				t.Fatal(err)
			}
			mustStatus(t, request(t, h, "DELETE", path, nil, owner, ownerCSRF), 204)
			mustStatus(t, request(t, h, "GET", path, nil, nil, ""), 404)
		})
	}
	// Owners can also delete without an administrator.
	created := request(t, h, "POST", "/api/memes", map[string]any{"content_type": "text", "body_text": "Owner deletes"}, owner, ownerCSRF)
	mustStatus(t, created, 201)
	var meme Meme
	if err = json.Unmarshal(created.Body.Bytes(), &meme); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, request(t, h, "DELETE", "/api/memes/"+meme.ID.String(), nil, owner, ownerCSRF), 204)
}

func TestUploaderAndOwnLibraryFilters(t *testing.T) {
	pool := testPool(t)
	issuer := newIssuer(t)
	cfg := config.Config{AppBaseURL: "http://localhost:4321", OIDCIssuer: issuer.server.URL, OIDCClientID: "test-client", OIDCRedirectURL: "http://localhost:4321/api/auth/callback", SessionTTL: time.Hour, TextMaxLength: 10000, OpenAIModel: "test-model"}
	app, err := New(context.Background(), cfg, pool)
	if err != nil {
		t.Fatal(err)
	}
	h := app.Handler()
	owner, csrf := signIn(t, h, issuer, "noeulnight", false)
	other, otherCSRF := signIn(t, h, issuer, "other", false)
	for _, entry := range []struct {
		cookies    []*http.Cookie
		csrf, body string
	}{{owner, csrf, "cat"}, {owner, csrf, "dog"}, {other, otherCSRF, "cat"}} {
		mustStatus(t, request(t, h, "POST", "/api/memes", map[string]any{"content_type": "text", "body_text": entry.body, "tags": []string{"animal"}}, entry.cookies, entry.csrf), 201)
	}
	me := request(t, h, "GET", "/api/auth/me", nil, owner, "")
	mustStatus(t, me, 200)
	var user struct {
		Username *string `json:"username"`
	}
	if err = json.Unmarshal(me.Body.Bytes(), &user); err != nil || user.Username == nil || *user.Username != "noeulnight" {
		t.Fatal("verified OIDC username missing")
	}
	mustStatus(t, request(t, h, "GET", "/api/memes?mine=true", nil, nil, ""), 401)
	for _, query := range []string{"mine=other", "uploader=bad+name"} {
		mustStatus(t, request(t, h, "GET", "/api/memes?"+query, nil, owner, ""), 400)
	}
	for _, entry := range []struct {
		query   string
		cookies []*http.Cookie
		count   int
	}{
		{"uploader=NOEULNIGHT", nil, 2},
		{"uploader=noeulnight&q=cat&tag=animal", nil, 1},
		{"uploader=missing", nil, 0},
		{"uploader=%25", nil, 0},
		{"mine=true", owner, 2},
		{"mine=true", other, 1},
		{"mine=true&uploader=noeulnight", other, 0},
		{"mine=false", nil, 3},
	} {
		result := request(t, h, "GET", "/api/memes?"+entry.query, nil, entry.cookies, "")
		mustStatus(t, result, 200)
		var page struct {
			Items []Meme `json:"items"`
		}
		if err = json.Unmarshal(result.Body.Bytes(), &page); err != nil || len(page.Items) != entry.count {
			t.Fatalf("filter %s: want %d items", entry.query, entry.count)
		}
		if entry.query == "uploader=NOEULNIGHT" && (page.Items[0].CreatedByUsername == nil || *page.Items[0].CreatedByUsername != "noeulnight") {
			t.Fatal("uploader handle missing on meme")
		}
	}
	first := request(t, h, "GET", "/api/memes?mine=true&limit=1", nil, owner, "")
	mustStatus(t, first, 200)
	var page struct {
		Items []Meme  `json:"items"`
		Next  *string `json:"next_cursor"`
	}
	if err = json.Unmarshal(first.Body.Bytes(), &page); err != nil || page.Next == nil {
		t.Fatal("own-library cursor missing")
	}
	firstID := page.Items[0].ID
	second := request(t, h, "GET", "/api/memes?mine=true&limit=1&cursor="+url.QueryEscape(*page.Next), nil, owner, "")
	mustStatus(t, second, 200)
	if err = json.Unmarshal(second.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].ID == firstID || page.Next != nil {
		t.Fatal("own-library cursor repeated or leaked items")
	}
	if _, err = pool.Exec(context.Background(), "UPDATE users SET username=NULL WHERE oidc_subject='noeulnight'"); err != nil {
		t.Fatal(err)
	}
	missing := request(t, h, "GET", "/api/memes?uploader=noeulnight", nil, nil, "")
	mustStatus(t, missing, 200)
	if err = json.Unmarshal(missing.Body.Bytes(), &page); err != nil || len(page.Items) != 0 {
		t.Fatal("missing handle must not match display name or subject")
	}
}
