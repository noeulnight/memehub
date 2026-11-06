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
	for _, filter := range []string{"content_type=image", "type=image", "type=audio"} {
		p = get(base+"&"+filter, nil)
		if len(p.Items) != 0 {
			t.Fatal("content filter ignored", filter)
		}
	}
	p = get(base+"&type=text&limit=1", nil)
	if len(p.Items) != 1 || p.Items[0].ID != exact || p.Next == nil {
		t.Fatal("type filter missing hybrid results", p)
	}
	typeCursor := url.QueryEscape(*p.Next)
	// The alias resolves to the same filter fingerprint; changing the type invalidates the cursor.
	get(base+"&content_type=text&limit=1&cursor="+typeCursor, nil)
	mustStatus(t, request(t, h, "GET", base+"&type=audio&cursor="+typeCursor, nil, nil, ""), 400)
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
	positives, topOne, topFive := 0, 0, 0
	for _, c := range cases {
		var q string
		var targets []string
		json.Unmarshal(c[0], &q)
		json.Unmarshal(c[1], &targets)
		t.Run(q, func(t *testing.T) {
			started := time.Now()
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
			if len(targets) > 0 {
				positives++
				if rank == 1 {
					topOne++
				}
				if rank > 0 {
					topFive++
				}
			}
			if len(targets) > 0 && rank == 0 {
				t.Errorf("target absent from top five: %+v", p.Items)
			}
			t.Logf("target rank: %d; response: %s; top results: %v", rank, time.Since(started), func() []string {
				var s []string
				for _, m := range p.Items {
					s = append(s, m.Title)
				}
				return s
			}())
		})
	}
	t.Logf("positive cases: %d; top1: %d; top5: %d (cached vectors; no provider latency)", positives, topOne, topFive)
}

func TestSearchNormalizationAndFieldPriority(t *testing.T) {
	db := testPool(t)
	ctx := context.Background()
	user := uuid.New()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,oidc_issuer,oidc_subject) VALUES($1,'test','search-normalization')`, user)
	insert := func(title, ocr, body, description string, similarity float64) uuid.UUID {
		id := uuid.New()
		if ocr != "" {
			exec(`INSERT INTO memes(id,created_by,content_type,title,extracted_text,description,status,analysis_status,original_filename,original_object_key,mime_type,size_bytes,width,height,sha256)
 VALUES($1,$2,'image',$3,$4,$5,'ready','completed','test.png',$6,'image/png',1,1,1,$7)`, id, user, title, ocr, description, id.String(), strings.Repeat("a", 64))
		} else {
			exec(`INSERT INTO memes(id,created_by,content_type,title,body_text,description,status,analysis_status) VALUES($1,$2,'text',$3,$4,$5,'ready','completed')`, id, user, title, body, description)
		}
		exec(`INSERT INTO meme_embeddings(meme_id,model,input_version,input_hash,content_revision,embedding) VALUES($1,$2,'v1','test',0,$3::public.vector)`, id, embedding.Model, embedding.Literal(searchVector(similarity)))
		return id
	}
	yoi := insert("cat 7", "요~이!", "본문", "", .01)
	space := insert("cat 8", "하기\n싫어...", "본문", "", .01)
	fullwidth := insert("cat 9", "ＡＢＣ！", "본문", "", .01)
	ocr := insert("cat 10", "고마워!", "본문", "", .01)
	tagged := insert("cat 11", "", "본문", "", .01)
	desc := insert("cat 12", "", "본문", "고마워 라고 답하는 이미지", 1)
	insert("필드경계앞", "", "필드경계뒤", "", .01)
	percent := insert("100%", "", "본문", "", .01)
	tagID := uuid.New()
	exec(`INSERT INTO tags(id,name,normalized_name) VALUES($1,'고마워','고마워')`, tagID)
	exec(`INSERT INTO meme_tags(meme_id,tag_id,source) VALUES($1,$2,'user')`, tagged, tagID)
	for _, hybrid := range []bool{false, true} {
		t.Run(map[bool]string{false: "keyword", true: "hybrid"}[hybrid], func(t *testing.T) {
			app := &Server{db: db}
			if hybrid {
				app.embedder = searchEmbedFunc(func(context.Context, string) (embedding.Result, error) {
					return embedding.Result{Vector: searchVector(1)}, nil
				})
			}
			get := func(q string) []Meme {
				t.Helper()
				w := request(t, app.Handler(), "GET", "/api/memes?q="+url.QueryEscape(q)+"&limit=100", nil, nil, "")
				mustStatus(t, w, 200)
				var p struct {
					Items []Meme `json:"items"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
					t.Fatal(err)
				}
				return p.Items
			}
			for _, tc := range []struct {
				q    string
				want uuid.UUID
			}{{"요이", yoi}, {"요 이", yoi}, {"요~이!", yoi}, {"하기싫어", space}, {"하기 싫어", space}, {"abc", fullwidth}, {"100%", percent}} {
				t.Run(tc.q, func(t *testing.T) {
					items := get(tc.q)
					if len(items) == 0 || items[0].ID != tc.want {
						t.Fatalf("expected %s first for %q; got %+v", tc.want, tc.q, items)
					}
					if tc.want == yoi && items[0].ExtractedText != "요~이!" {
						t.Fatal("original OCR was changed")
					}
				})
			}
			if hybrid {
				items := get("고마워")
				if len(items) < 3 || items[0].ID != tagged || items[1].ID != ocr || items[2].ID != desc {
					t.Fatalf("tag/OCR did not outrank description and semantic result: %+v", items)
				}
			} else {
				for _, q := range []string{"필드경계앞필드경계뒤", "!!!", "%%%", "___", "100_", "🦒"} {
					if items := get(q); len(items) != 0 {
						t.Fatalf("unexpected broad match for %q: %+v", q, items)
					}
				}
			}
		})
	}
}

func TestMemeTypeFilter(t *testing.T) {
	db := testPool(t)
	app := &Server{db: db}
	h := app.Handler()
	ctx := context.Background()
	owner := uuid.New()
	if _, err := db.Exec(ctx, "INSERT INTO users(id,oidc_issuer,oidc_subject) VALUES($1,'test','type-filter')", owner); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"text", "image", "audio"} {
		var err error
		if kind == "text" {
			_, err = db.Exec(ctx, "INSERT INTO memes(id,created_by,content_type,title,body_text,status) VALUES($1,$2,'text','filterable','body','ready')", uuid.New(), owner)
		} else {
			mime := "image/png"
			var dimension *int
			if kind == "image" {
				value := 10
				dimension = &value
			} else {
				mime = "audio/mpeg"
			}
			_, err = db.Exec(ctx, `INSERT INTO memes(id,created_by,content_type,title,status,original_filename,original_object_key,mime_type,size_bytes,width,height,sha256) VALUES($1,$2,$3,'filterable','ready','file',$4,$5,100,$6,$6,$7)`, uuid.New(), owner, kind, uuid.NewString(), mime, dimension, strings.Repeat("a", 64))
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, prefix := range []string{"/api/memes?", "/api/memes?q=filterable&"} {
		for _, tc := range []struct {
			filter, kind  string
			count, status int
		}{
			{"", "", 3, 200}, {"type=", "", 3, 200},
			{"type=image", "image", 1, 200}, {"type=text", "text", 1, 200}, {"type=audio", "audio", 1, 200},
			{"content_type=image", "image", 1, 200}, {"type=audio&content_type=audio", "audio", 1, 200},
			{"type=&content_type=text", "text", 1, 200}, {"type=image&content_type=", "image", 1, 200},
			{"type=video", "", 0, 400}, {"content_type=video", "", 0, 400}, {"type=image&content_type=audio", "", 0, 400},
		} {
			t.Run(prefix+tc.filter, func(t *testing.T) {
				response := request(t, h, "GET", prefix+tc.filter, nil, nil, "")
				mustStatus(t, response, tc.status)
				if tc.status != 200 {
					return
				}
				var page struct {
					Items []Meme `json:"items"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				if len(page.Items) != tc.count {
					t.Fatalf("got %d items, want %d", len(page.Items), tc.count)
				}
				for _, m := range page.Items {
					if tc.kind != "" && m.ContentType != tc.kind {
						t.Fatal("wrong type", m.ContentType)
					}
				}
			})
		}
	}
}
