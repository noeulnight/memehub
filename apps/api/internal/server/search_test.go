package server

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/noeulnight/memehub/apps/api/internal/config"
	"github.com/noeulnight/memehub/apps/api/internal/embedding"
)

type searchEmbedFunc func(context.Context, string) (embedding.Result, error)

func (f searchEmbedFunc) Embed(ctx context.Context, s string) (embedding.Result, error) {
	return f(ctx, s)
}
func searchVector(sim float64) []float32 {
	v := make([]float32, embedding.Dimensions)
	v[0] = float32(sim)
	v[1] = float32(math.Sqrt(1 - sim*sim))
	return v
}
func TestHybridSearchPaginationFiltersAndFallback(t *testing.T) {
	db := testPool(t)
	issuer := newIssuer(t)
	ctx := context.Background()
	app, err := New(ctx, config.Config{AppBaseURL: "http://localhost:4321", OIDCIssuer: issuer.server.URL, OIDCClientID: "test-client", OIDCRedirectURL: "http://localhost:4321/api/auth/callback", SessionTTL: time.Hour, TextMaxLength: 10000, OpenAIModel: "test"}, db)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	app.embedder = embedding.NewQueryCache(searchEmbedFunc(func(context.Context, string) (embedding.Result, error) {
		calls++
		return embedding.Result{Vector: searchVector(1)}, nil
	}))
	h := app.Handler()
	owner, _ := signIn(t, h, issuer, "owner", false)
	other, _ := signIn(t, h, issuer, "other", false)
	var ownerID, otherID uuid.UUID
	for name, target := range map[string]*uuid.UUID{"owner": &ownerID, "other": &otherID} {
		if err = db.QueryRow(ctx, `SELECT id FROM users WHERE oidc_subject=$1`, name).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(title string, sim float64, user uuid.UUID) uuid.UUID {
		id := uuid.New()
		exec(`INSERT INTO memes(id,created_by,content_type,title,body_text,status,analysis_status) VALUES($1,$2,'text',$3,'본문','ready','completed')`, id, user, title)
		exec(`INSERT INTO meme_embeddings(meme_id,model,input_version,input_hash,content_revision,embedding) VALUES($1,$2,'v1','hash',0,$3::public.vector)`, id, embedding.Model, embedding.Literal(searchVector(sim)))
		return id
	}
	first := insert("semantic first", 1, ownerID)
	second := insert("semantic second", .8, otherID)
	exact := insert("쥐쥐이야", .6, ownerID)
	stale := insert("stale", 1, ownerID)
	exec(`UPDATE memes SET content_revision=1 WHERE id=$1`, stale)
	deleted := insert("deleted", 1, ownerID)
	exec(`UPDATE memes SET deleted_at=now() WHERE id=$1`, deleted)
	tag := uuid.New()
	exec(`INSERT INTO tags(id,name,normalized_name) VALUES($1,'test','test')`, tag)
	exec(`INSERT INTO meme_tags(meme_id,tag_id,source) VALUES($1,$2,'user')`, second, tag)
	type page struct {
		Items []Meme  `json:"items"`
		Next  *string `json:"next_cursor"`
	}
	get := func(path string, cookies []*http.Cookie) page {
		t.Helper()
		w := request(t, h, "GET", path, nil, cookies, "")
		mustStatus(t, w, 200)
		var p page
		if err = json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	base := "/api/memes?q=" + url.QueryEscape("쥐쥐이야")
	p := get(base+"&limit=1", nil)
	if len(p.Items) != 1 || p.Items[0].ID != exact || p.Next == nil {
		t.Fatal("exact match did not win", p)
	}
	oldCursor := *p.Next
	// A new meme must not enter an existing result sequence, and a deleted one must disappear.
	fresh := insert("new result", 1, ownerID)
	_ = fresh
	exec(`UPDATE memes SET deleted_at=now() WHERE id=$1`, first)
	p = get(base+"&limit=1&cursor="+url.QueryEscape(oldCursor), nil)
	if len(p.Items) != 1 || p.Items[0].ID != second || p.Next != nil || calls != 1 {
		t.Fatal("unstable paging or repeated embedding", p, calls)
	}
	mustStatus(t, request(t, h, "GET", "/api/memes?q=changed&cursor="+url.QueryEscape(oldCursor), nil, nil, ""), 400)
	mustStatus(t, request(t, h, "GET", base+"&mine=true", nil, nil, ""), 401)
	for _, path := range []string{base + "&tag=test", base + "&uploader=other"} {
		p = get(path, nil)
		if len(p.Items) != 1 || p.Items[0].ID != second {
			t.Fatal("search filter ignored", path, p)
		}
	}
	p = get(base+"&mine=true", other)
	if len(p.Items) != 1 || p.Items[0].ID != second {
		t.Fatal("mine filter ignored", p)
	}
	p = get(base+"&content_type=image", nil)
	if len(p.Items) != 0 {
		t.Fatal("content filter ignored")
	}
	collection := uuid.New()
	exec(`INSERT INTO collections(id,user_id,name) VALUES($1,$2,'private')`, collection, ownerID)
	exec(`INSERT INTO collection_memes(collection_id,meme_id) VALUES($1,$2)`, collection, exact)
	path := base + "&collection=" + collection.String()
	mustStatus(t, request(t, h, "GET", path, nil, nil, ""), 401)
	mustStatus(t, request(t, h, "GET", path, nil, other, ""), 404)
	p = get(path, owner)
	if len(p.Items) != 1 || p.Items[0].ID != exact {
		t.Fatal("collection filter ignored")
	}
	exec(`UPDATE meme_searches SET expires_at=now()-interval '1 second'`)
	mustStatus(t, request(t, h, "GET", base+"&cursor="+url.QueryEscape(oldCursor), nil, nil, ""), 400)
	app.embedder = searchEmbedFunc(func(context.Context, string) (embedding.Result, error) {
		return embedding.Result{}, errors.New("unavailable")
	})
	p = get(base, nil)
	if len(p.Items) != 1 || p.Items[0].ID != exact {
		t.Fatal("keyword fallback failed", p)
	}
	p = get("/api/memes?q=zzzz-no-match", nil)
	if len(p.Items) != 0 || p.Next != nil {
		t.Fatal("empty fallback invalid")
	}
}

// Optional acceptance test against an exported public corpus and previously generated embeddings.
// No OpenAI requests are made. SEARCH_EVAL_DIR must contain corpus.json, cases.json and the model response.
func TestHybridSearchCachedCorpus(t *testing.T) {
	dir := os.Getenv("SEARCH_EVAL_DIR")
	if dir == "" {
		t.Skip("SEARCH_EVAL_DIR is not set")
	}
	read := func(name string, dst any) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(data, dst); err != nil {
			t.Fatal(err)
		}
	}
	var corpus []Meme
	read("corpus.json", &corpus)
	var response struct {
		Data []struct {
			Index  int       `json:"index"`
			Vector []float32 `json:"embedding"`
		} `json:"data"`
	}
	read(embedding.Model+".json", &response)
	var cases [][]json.RawMessage
	read("cases.json", &cases)
	vectors := make([][]float32, len(response.Data))
	for _, e := range response.Data {
		vectors[e.Index] = e.Vector
	}
	if len(vectors) != len(corpus)+len(cases) {
		t.Fatal("corpus and embedding counts differ")
	}
	db := testPool(t)
	ctx := context.Background()
	user := uuid.New()
	if _, err := db.Exec(ctx, `INSERT INTO users(id,oidc_issuer,oidc_subject) VALUES($1,'eval','eval')`, user); err != nil {
		t.Fatal(err)
	}
	for i, m := range corpus {
		// Public fixture metadata is preserved; placeholder storage keys are never read by this test.
		_, err := db.Exec(ctx, `INSERT INTO memes(id,created_by,content_type,title,body_text,description,extracted_text,status,analysis_status,original_filename,original_object_key,mime_type,size_bytes,width,height,sha256)
 VALUES($1,$2,'image',$3,NULL,$4,$5,'ready','completed','eval.png',$6,'image/png',1,1,1,$7)`, m.ID, user, m.Title, m.Description, m.ExtractedText, m.ID.String(), strings.Repeat("a", 64))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range m.Tags {
			var tagID uuid.UUID
			err = db.QueryRow(ctx, `INSERT INTO tags(id,name,normalized_name) VALUES($1,$2,$3) ON CONFLICT(normalized_name) DO UPDATE SET name=tags.name RETURNING id`, uuid.New(), name, normalizeTag(name)).Scan(&tagID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(ctx, `INSERT INTO meme_tags(meme_id,tag_id,source) VALUES($1,$2,'ai') ON CONFLICT DO NOTHING`, m.ID, tagID); err != nil {
				t.Fatal(err)
			}
		}
		_, err = db.Exec(ctx, `INSERT INTO meme_embeddings(meme_id,model,input_version,input_hash,content_revision,embedding) VALUES($1,$2,'v1','eval',0,$3::public.vector)`, m.ID, embedding.Model, embedding.Literal(vectors[i]))
		if err != nil {
			t.Fatal(err)
		}
	}
	queryVectors := map[string][]float32{}
	for i, c := range cases {
		var q string
		json.Unmarshal(c[0], &q)
		queryVectors[q] = vectors[len(corpus)+i]
	}
	app := &Server{db: db, embedder: searchEmbedFunc(func(ctx context.Context, q string) (embedding.Result, error) {
		return embedding.Result{Vector: queryVectors[q]}, nil
	})}
	if os.Getenv("SEARCH_EVAL_LIVE") == "true" {
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			t.Fatal("OPENAI_API_KEY is required for live evaluation")
		}
		app.embedder = embedding.NewQueryCache(embedding.NewClient(key))
	}
	for _, c := range cases {
		var q string
		var targets []string
		json.Unmarshal(c[0], &q)
		json.Unmarshal(c[1], &targets)
		t.Run(q, func(t *testing.T) {
			w := request(t, app.Handler(), "GET", "/api/memes?q="+url.QueryEscape(q)+"&limit=5", nil, nil, "")
			mustStatus(t, w, 200)
			var p struct {
				Items []Meme `json:"items"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}
			rank := 0
			for i, m := range p.Items {
				for _, target := range targets {
					if strings.HasPrefix(m.ID.String(), target) {
						if rank == 0 {
							rank = i + 1
						}
					}
				}
			}
			if len(targets) > 0 && rank == 0 {
				t.Errorf("target absent from top five: %+v", p.Items)
			}
			t.Logf("target rank: %d; top results: %v", rank, func() []string {
				var s []string
				for _, m := range p.Items {
					s = append(s, m.Title)
				}
				return s
			}())
		})
	}
}
