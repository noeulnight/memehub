package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/noeulnight/memehub/apps/api/internal/config"
	"github.com/noeulnight/memehub/apps/api/internal/embedding"
)

func TestSafetyFiltersAcrossSearchPaths(t *testing.T) {
	db := testPool(t)
	issuer := newIssuer(t)
	ctx := context.Background()
	app, err := New(ctx, config.Config{AppBaseURL: "http://localhost:4321", OIDCIssuer: issuer.server.URL, OIDCClientID: "test-client", OIDCRedirectURL: "http://localhost:4321/api/auth/callback", SessionTTL: time.Hour, TextMaxLength: 10000, OpenAIModel: "test"}, db)
	if err != nil {
		t.Fatal(err)
	}
	user := uuid.New()
	if _, err = db.Exec(ctx, "INSERT INTO users(id,oidc_issuer,oidc_subject) VALUES($1,$2,'owner')", user, issuer.server.URL); err != nil {
		t.Fatal(err)
	}
	for _, rating := range []string{"sfw", "nsfw", "unknown", "nsfw"} {
		id := uuid.New()
		if _, err = db.Exec(ctx, "INSERT INTO memes(id,created_by,content_type,title,body_text,status,safety_rating) VALUES($1,$2,'text','cat','cat','ready',$3)", id, user, rating); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, "INSERT INTO meme_embeddings(meme_id,model,input_version,input_hash,content_revision,embedding) VALUES($1,$2,'v1','hash',0,$3::public.vector)", id, embedding.Model, embedding.Literal(searchVector(1))); err != nil {
			t.Fatal(err)
		}
		// A real tag named sfw must not override an NSFW classification.
		if rating == "nsfw" {
			tag := uuid.New()
			if _, err = db.Exec(ctx, "INSERT INTO tags(id,name,normalized_name) VALUES($1,'sfw','sfw') ON CONFLICT DO NOTHING", tag); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(ctx, "INSERT INTO meme_tags(meme_id,tag_id,source) SELECT $1,id,'user' FROM tags WHERE normalized_name='sfw'", id); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, hybrid := range []bool{false, true} {
		name := "keyword"
		if hybrid {
			name = "hybrid"
			app.embedder = embedding.NewQueryCache(searchEmbedFunc(func(context.Context, string) (embedding.Result, error) {
				return embedding.Result{Vector: searchVector(1)}, nil
			}))
		}
		t.Run(name, func(t *testing.T) {
			for _, test := range []struct {
				filter, want string
				count        int
			}{
				{"safety_rating=sfw", "sfw", 1}, {"safety_rating=nsfw", "nsfw", 2}, {"safety_rating=unknown", "unknown", 1}, {"tag=SFW", "sfw", 1}, {"tag=nsfw", "nsfw", 2},
			} {
				response := request(t, app.Handler(), "GET", "/api/memes?q=cat&"+test.filter, nil, nil, "")
				mustStatus(t, response, 200)
				var page struct {
					Items []Meme  `json:"items"`
					Next  *string `json:"next_cursor"`
				}
				if err = json.Unmarshal(response.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				if len(page.Items) != test.count {
					t.Fatalf("%s: count=%d", test.filter, len(page.Items))
				}
				for _, m := range page.Items {
					if m.SafetyRating != test.want {
						t.Fatalf("%s leaked %s", test.filter, m.SafetyRating)
					}
				}
			}
			first := request(t, app.Handler(), "GET", "/api/memes?q=cat&safety_rating=nsfw&limit=1", nil, nil, "")
			mustStatus(t, first, 200)
			var page struct {
				Items []Meme `json:"items"`
				Next  string `json:"next_cursor"`
			}
			if err = json.Unmarshal(first.Body.Bytes(), &page); err != nil || page.Next == "" {
				t.Fatal("missing pagination", err)
			}
			next := request(t, app.Handler(), "GET", "/api/memes?q=cat&safety_rating=nsfw&limit=1&cursor="+page.Next, nil, nil, "")
			mustStatus(t, next, 200)
			var second struct {
				Items []Meme `json:"items"`
			}
			if err = json.Unmarshal(next.Body.Bytes(), &second); err != nil || len(second.Items) != 1 || second.Items[0].SafetyRating != "nsfw" || second.Items[0].ID == page.Items[0].ID {
				t.Fatal("filtered pagination failed", err)
			}
		})
	}
	mustStatus(t, request(t, app.Handler(), "GET", "/api/memes?safety_rating=invalid", nil, nil, ""), 400)
	mustStatus(t, request(t, app.Handler(), "GET", "/api/memes?tag=sfw&safety_rating=nsfw", nil, nil, ""), 400)
}
