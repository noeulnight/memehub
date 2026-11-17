package server

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/noeulnight/memehub/apps/api/internal/config"
	"github.com/noeulnight/memehub/apps/api/internal/embedding"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPackLifecycleAndMemeFilters(t *testing.T) {
	db := testPool(t)
	issuer := newIssuer(t)
	app, err := New(context.Background(), config.Config{AppBaseURL: "http://localhost:4321", OIDCIssuer: issuer.server.URL, OIDCClientID: "test-client", OIDCRedirectURL: "http://localhost:4321/api/auth/callback", SessionTTL: time.Hour, TextMaxLength: 10000, OpenAIModel: "test-model"}, db)
	if err != nil {
		t.Fatal(err)
	}
	h := app.Handler()
	owner, csrf := signIn(t, h, issuer, "owner", false)
	other, otherCSRF := signIn(t, h, issuer, "other", false)
	admin, adminCSRF := signIn(t, h, issuer, "admin", false)
	if _, err = db.Exec(context.Background(), "UPDATE users SET is_admin=true WHERE oidc_subject='admin'"); err != nil {
		t.Fatal(err)
	}
	decode := func(w *httptest.ResponseRecorder, v any) {
		t.Helper()
		if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
			t.Fatal(err)
		}
	}
	mustStatus(t, request(t, h, "GET", "/api/packs", nil, nil, ""), 200)
	mustStatus(t, request(t, h, "POST", "/api/packs", map[string]string{"name": "Pack"}, nil, ""), 401)
	mustStatus(t, request(t, h, "POST", "/api/packs", map[string]string{"name": "Pack"}, owner, ""), 403)
	for _, input := range []map[string]string{{"name": " "}, {"name": "bad\nname"}, {"name": "Pack", "source_url": "javascript:bad"}} {
		mustStatus(t, request(t, h, "POST", "/api/packs", input, owner, csrf), 422)
	}
	w := request(t, h, "POST", "/api/packs", map[string]string{"name": " Original pack ", "source_url": "https://example.com/pack"}, owner, csrf)
	mustStatus(t, w, 201)
	var p pack
	decode(w, &p)
	if p.Name != "Original pack" || p.MemeCount != 0 {
		t.Fatalf("pack: %+v", p)
	}
	mustStatus(t, request(t, h, "POST", "/api/packs", map[string]string{"name": "original PACK"}, owner, csrf), 409)
	path := "/api/packs/" + p.ID.String()
	mustStatus(t, request(t, h, "PUT", path, map[string]string{"name": "stolen"}, other, otherCSRF), 404)
	mustStatus(t, request(t, h, "DELETE", path, nil, other, otherCSRF), 404)
	mustStatus(t, request(t, h, "PUT", path, map[string]string{"name": "Renamed"}, admin, adminCSRF), 200)
	mustStatus(t, request(t, h, "GET", "/api/packs?cursor=bad", nil, nil, ""), 400)
	mustStatus(t, request(t, h, "GET", "/api/memes?pack=bad", nil, nil, ""), 400)
	for _, packID := range []string{"bad", uuid.Nil.String(), uuid.NewString()} {
		mustStatus(t, request(t, h, "POST", "/api/memes", map[string]any{"content_type": "text", "body_text": "rollback", "pack_id": packID}, owner, csrf), 422)
	}
	w = request(t, h, "POST", "/api/memes", map[string]any{"content_type": "text", "title": "same", "body_text": "first", "pack_id": p.ID}, other, otherCSRF)
	mustStatus(t, w, 201)
	var m Meme
	decode(w, &m)
	if m.PackID == nil || *m.PackID != p.ID || m.PackName == nil || *m.PackName != "Renamed" {
		t.Fatalf("membership: %+v", m)
	}
	mustStatus(t, request(t, h, "POST", "/api/memes", map[string]any{"content_type": "text", "title": "same", "body_text": "second", "tags": []string{"Renamed"}}, other, otherCSRF), 201)
	if _, err = db.Exec(context.Background(), `INSERT INTO meme_embeddings(meme_id,model,input_version,input_hash,content_revision,embedding)
 SELECT id,$1,'v1','hash',content_revision,$2::public.vector FROM memes WHERE deleted_at IS NULL`, embedding.Model, embedding.Literal(searchVector(1))); err != nil {
		t.Fatal(err)
	}
	for _, semantic := range []bool{false, true} {
		if semantic {
			app.embedder = searchEmbedFunc(func(context.Context, string) (embedding.Result, error) {
				return embedding.Result{Vector: searchVector(1)}, nil
			})
		}
		suffixes := []string{"", "&q=same", "&q=same&tag=Renamed"}
		if semantic {
			suffixes = append(suffixes, "&q=unrelated")
		}
		for _, suffix := range suffixes {
			w = request(t, h, "GET", "/api/memes?pack="+p.ID.String()+suffix, nil, nil, "")
			mustStatus(t, w, 200)
			var page struct {
				Items []Meme `json:"items"`
			}
			decode(w, &page)
			expected := 1
			if suffix == "&q=same&tag=Renamed" {
				expected = 0
			}
			if len(page.Items) != expected {
				t.Fatalf("semantic=%v suffix=%s: %s", semantic, suffix, w.Body.String())
			}
		}
	}
	update := map[string]any{"content_type": "text", "title": "same", "body_text": "first", "content_revision": m.Revision}
	mustStatus(t, request(t, h, "PUT", "/api/memes/"+m.ID.String(), update, owner, csrf), 404)
	w = request(t, h, "PUT", "/api/memes/"+m.ID.String(), update, other, otherCSRF)
	mustStatus(t, w, 200)
	decode(w, &m)
	if m.PackID == nil {
		t.Fatal("old client update removed pack")
	}
	update["pack_id"] = ""
	update["content_revision"] = m.Revision
	w = request(t, h, "PUT", "/api/memes/"+m.ID.String(), update, other, otherCSRF)
	mustStatus(t, w, 200)
	decode(w, &m)
	if m.PackID != nil {
		t.Fatal("pack not cleared")
	}
	update["pack_id"] = p.ID
	update["content_revision"] = m.Revision
	mustStatus(t, request(t, h, "PUT", "/api/memes/"+m.ID.String(), update, other, otherCSRF), 200)
	w = request(t, h, "GET", "/api/packs", nil, nil, "")
	mustStatus(t, w, 200)
	var page struct {
		Items []pack `json:"items"`
	}
	decode(w, &page)
	if len(page.Items) != 1 || page.Items[0].MemeCount != 1 {
		t.Fatalf("counts: %s", w.Body.String())
	}
	mustStatus(t, request(t, h, "DELETE", path, nil, owner, csrf), 204)
	w = request(t, h, "GET", "/api/memes/"+m.ID.String(), nil, nil, "")
	mustStatus(t, w, 200)
	decode(w, &m)
	if m.PackID != nil || m.PackName != nil {
		t.Fatal("deleting pack must preserve meme and clear membership")
	}
	// Empty packs remain selectable across page boundaries.
	if _, err = db.Exec(context.Background(), `INSERT INTO packs(id,name,created_by)
 SELECT gen_random_uuid(),'Pack '||lpad(n::text,3,'0'),$1 FROM generate_series(1,101) n`, p.CreatedBy); err != nil {
		t.Fatal(err)
	}
	w = request(t, h, "GET", "/api/packs", nil, nil, "")
	mustStatus(t, w, 200)
	var firstPage struct {
		Items []pack  `json:"items"`
		Next  *string `json:"next_cursor"`
	}
	decode(w, &firstPage)
	if len(firstPage.Items) != 100 || firstPage.Next == nil {
		t.Fatalf("first pack page: %s", w.Body.String())
	}
	w = request(t, h, "GET", "/api/packs?cursor="+*firstPage.Next, nil, nil, "")
	mustStatus(t, w, 200)
	var lastPage struct {
		Items []pack  `json:"items"`
		Next  *string `json:"next_cursor"`
	}
	decode(w, &lastPage)
	if len(lastPage.Items) != 1 || lastPage.Items[0].Name != "Pack 101" || lastPage.Next != nil {
		t.Fatalf("last pack page: %s", w.Body.String())
	}

}
