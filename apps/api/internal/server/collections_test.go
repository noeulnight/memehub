package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/noeulnight/memehub/apps/api/internal/config"
)

func TestCollectionLifecycleAndPrivacy(t *testing.T) {
	pool := testPool(t)
	issuer := newIssuer(t)
	app, err := New(context.Background(), config.Config{AppBaseURL: "http://localhost:4321", OIDCIssuer: issuer.server.URL, OIDCClientID: "test-client", OIDCRedirectURL: "http://localhost:4321/api/auth/callback", SessionTTL: time.Hour, TextMaxLength: 10000, OpenAIModel: "test-model"}, pool)
	if err != nil {
		t.Fatal(err)
	}
	h := app.Handler()
	owner, csrf := signIn(t, h, issuer, "collector", false)
	other, otherCSRF := signIn(t, h, issuer, "uploader", false)
	admin, adminCSRF := signIn(t, h, issuer, "admin", false)
	if _, err = pool.Exec(context.Background(), "UPDATE users SET is_admin=true WHERE oidc_subject='admin'"); err != nil {
		t.Fatal(err)
	}
	decode := func(w interface{ Bytes() []byte }, value any) {
		t.Helper()
		if err := json.Unmarshal(w.Bytes(), value); err != nil {
			t.Fatal(err)
		}
	}
	newMeme := func(body string) Meme {
		t.Helper()
		w := request(t, h, "POST", "/api/memes", map[string]any{"content_type": "text", "body_text": body, "tags": []string{"cats"}}, other, otherCSRF)
		mustStatus(t, w, 201)
		var meme Meme
		decode(w.Body, &meme)
		return meme
	}
	first, second := newMeme("first cat"), newMeme("second cat")
	base := "/api/collections"
	mustStatus(t, request(t, h, "GET", base, nil, nil, ""), 401)
	mustStatus(t, request(t, h, "POST", base, map[string]string{"name": "Cats"}, nil, ""), 401)
	mustStatus(t, request(t, h, "POST", base, map[string]string{"name": "Cats"}, owner, ""), 403)
	mustStatus(t, request(t, h, "POST", base, map[string]string{"name": "Cats"}, owner, otherCSRF), 403)
	for _, name := range []string{"", " \t\n", "bad\nname", strings.Repeat("가", 101)} {
		mustStatus(t, request(t, h, "POST", base, map[string]string{"name": name}, owner, csrf), 422)
	}
	for _, query := range []string{"limit=0", "limit=101", "cursor=bad", "meme_id=bad", "meme_id=" + uuid.Nil.String()} {
		mustStatus(t, request(t, h, "GET", base+"?"+query, nil, owner, ""), 400)
	}
	missing := uuid.NewString()
	mustStatus(t, request(t, h, "POST", base, map[string]string{"name": "Rollback", "meme_id": missing}, owner, csrf), 404)
	created := request(t, h, "POST", base, map[string]any{"name": "  고양이 모음  ", "meme_id": first.ID}, owner, csrf)
	mustStatus(t, created, 201)
	var cats collection
	decode(created.Body, &cats)
	if cats.Name != "고양이 모음" || cats.MemeCount != 1 || !cats.ContainsMeme || created.Header().Get("Location") != base+"/"+cats.ID.String() {
		t.Fatalf("unexpected collection: %+v", cats)
	}
	path := base + "/" + cats.ID.String()
	mustStatus(t, request(t, h, "POST", base, map[string]string{"name": "고양이 모음"}, owner, csrf), 409)
	mustStatus(t, request(t, h, "POST", base, map[string]string{"name": "고양이 모음"}, other, otherCSRF), 201)
	mustStatus(t, request(t, h, "POST", base, map[string]string{"name": "Other folder"}, owner, csrf), 201)
	for _, member := range []struct {
		cookies []*http.Cookie
		csrf    string
	}{{other, otherCSRF}, {admin, adminCSRF}} {
		mustStatus(t, request(t, h, "GET", path, nil, member.cookies, ""), 404)
		mustStatus(t, request(t, h, "GET", "/api/memes?collection="+cats.ID.String(), nil, member.cookies, ""), 404)
		mustStatus(t, request(t, h, "PUT", path, map[string]string{"name": "Stolen"}, member.cookies, member.csrf), 404)
		mustStatus(t, request(t, h, "DELETE", path, nil, member.cookies, member.csrf), 404)
		for _, method := range []string{"PUT", "DELETE"} {
			mustStatus(t, request(t, h, method, path+"/memes/"+first.ID.String(), nil, member.cookies, member.csrf), 404)
		}
	}
	mustStatus(t, request(t, h, "GET", path, nil, nil, ""), 401)
	mustStatus(t, request(t, h, "GET", "/api/memes?collection="+cats.ID.String(), nil, nil, ""), 401)
	mustStatus(t, request(t, h, "GET", "/api/memes?collection=bad", nil, owner, ""), 400)
	for _, method := range []string{"PUT", "DELETE"} {
		mustStatus(t, request(t, h, method, path+"/memes/"+first.ID.String(), nil, owner, ""), 403)
	}
	// Duplicate saves are idempotent, and saving a public meme does not change its author.
	for range 2 {
		mustStatus(t, request(t, h, "PUT", path+"/memes/"+first.ID.String(), nil, owner, csrf), 204)
	}
	mustStatus(t, request(t, h, "PUT", path+"/memes/"+second.ID.String(), nil, owner, csrf), 204)
	mustStatus(t, request(t, h, "PUT", path+"/memes/"+missing, nil, owner, csrf), 404)
	var listed struct {
		Items []collection `json:"items"`
		Next  *string      `json:"next_cursor"`
	}
	w := request(t, h, "GET", base+"?meme_id="+first.ID.String(), nil, owner, "")
	mustStatus(t, w, 200)
	decode(w.Body, &listed)
	if len(listed.Items) != 2 {
		t.Fatalf("unexpected collections or failed rollback: %s", w.Body.String())
	}
	for _, item := range listed.Items {
		if item.ID == cats.ID && (item.MemeCount != 2 || !item.ContainsMeme) {
			t.Fatalf("duplicate save or missing membership: %+v", item)
		}
	}
	w = request(t, h, "GET", base+"?limit=1", nil, owner, "")
	mustStatus(t, w, 200)
	decode(w.Body, &listed)
	if len(listed.Items) != 1 || listed.Next == nil {
		t.Fatal("missing collection pagination")
	}
	firstCollection := listed.Items[0].ID
	w = request(t, h, "GET", base+"?limit=1&cursor="+url.QueryEscape(*listed.Next), nil, owner, "")
	mustStatus(t, w, 200)
	decode(w.Body, &listed)
	if len(listed.Items) != 1 || listed.Next != nil || listed.Items[0].ID == firstCollection {
		t.Fatal("invalid collection continuation")
	}
	var memes struct {
		Items []Meme  `json:"items"`
		Next  *string `json:"next_cursor"`
	}
	w = request(t, h, "GET", "/api/memes?collection="+cats.ID.String()+"&limit=1&tag=cats&content_type=text", nil, owner, "")
	mustStatus(t, w, 200)
	decode(w.Body, &memes)
	if len(memes.Items) != 1 || memes.Next == nil || memes.Items[0].CreatedBy != first.CreatedBy {
		t.Fatal("collection filters, author, or pagination missing")
	}
	firstPageID := memes.Items[0].ID
	w = request(t, h, "GET", "/api/memes?collection="+cats.ID.String()+"&limit=1&tag=cats&content_type=text&cursor="+url.QueryEscape(*memes.Next), nil, owner, "")
	mustStatus(t, w, 200)
	decode(w.Body, &memes)
	if len(memes.Items) != 1 || memes.Next != nil || memes.Items[0].ID == firstPageID {
		t.Fatal("invalid collection meme continuation")
	}
	w = request(t, h, "GET", "/api/memes?collection="+cats.ID.String()+"&q=first", nil, owner, "")
	mustStatus(t, w, 200)
	decode(w.Body, &memes)
	if len(memes.Items) != 1 || memes.Items[0].ID != first.ID {
		t.Fatal("collection text filter missing")
	}
	for range 2 {
		mustStatus(t, request(t, h, "DELETE", path+"/memes/"+first.ID.String(), nil, owner, csrf), 204)
	}
	mustStatus(t, request(t, h, "GET", "/api/memes/"+first.ID.String(), nil, nil, ""), 200)
	mustStatus(t, request(t, h, "PUT", path+"/memes/"+first.ID.String(), nil, owner, csrf), 204)
	mustStatus(t, request(t, h, "DELETE", "/api/memes/"+first.ID.String(), nil, other, otherCSRF), 204)
	mustStatus(t, request(t, h, "PUT", path+"/memes/"+first.ID.String(), nil, owner, csrf), 404)
	mustStatus(t, request(t, h, "POST", base, map[string]any{"name": "Deleted meme", "meme_id": first.ID}, owner, csrf), 404)
	w = request(t, h, "GET", base+"?meme_id="+first.ID.String(), nil, owner, "")
	mustStatus(t, w, 200)
	decode(w.Body, &listed)
	for _, item := range listed.Items {
		if item.ID == cats.ID && (item.MemeCount != 1 || item.ContainsMeme) {
			t.Fatal("deleted memes counted or marked saved")
		}
	}
	w = request(t, h, "GET", "/api/memes?collection="+cats.ID.String(), nil, owner, "")
	mustStatus(t, w, 200)
	decode(w.Body, &memes)
	if len(memes.Items) != 1 || memes.Items[0].ID != second.ID {
		t.Fatal("deleted meme visible in collection")
	}
	mustStatus(t, request(t, h, "PUT", path, map[string]string{"name": "Other folder"}, owner, csrf), 409)
	mustStatus(t, request(t, h, "PUT", path, map[string]string{"name": "Renamed"}, owner, csrf), 204)
	w = request(t, h, "GET", path, nil, owner, "")
	mustStatus(t, w, 200)
	decode(w.Body, &cats)
	if cats.Name != "Renamed" || cats.MemeCount != 1 {
		t.Fatal("rename or count missing")
	}
	imageID := uuid.New()
	_, err = pool.Exec(context.Background(), `INSERT INTO memes(id,created_by,content_type,original_filename,original_object_key,status)
 VALUES($1,$2,'image','cat.png',$3,'uploading')`, imageID, first.CreatedBy, "originals/"+imageID.String())
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, request(t, h, "PUT", path+"/memes/"+imageID.String(), nil, owner, csrf), 404)
	mustStatus(t, request(t, h, "POST", base, map[string]any{"name": "Incomplete image", "meme_id": imageID}, owner, csrf), 404)
	_, err = pool.Exec(context.Background(), "UPDATE memes SET status='ready',mime_type='image/png',size_bytes=100,width=120,height=120,sha256=$2 WHERE id=$1", imageID, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	mustStatus(t, request(t, h, "PUT", path+"/memes/"+imageID.String(), nil, owner, csrf), 204)
	w = request(t, h, "GET", "/api/memes?collection="+cats.ID.String()+"&content_type=image", nil, owner, "")
	mustStatus(t, w, 200)
	decode(w.Body, &memes)
	if len(memes.Items) != 1 || memes.Items[0].ID != imageID {
		t.Fatal("ready image not saved or image filter missing")
	}
	mustStatus(t, request(t, h, "DELETE", path, nil, owner, csrf), 204)
	mustStatus(t, request(t, h, "GET", path, nil, owner, ""), 404)
	mustStatus(t, request(t, h, "GET", "/api/memes/"+second.ID.String(), nil, nil, ""), 200)
	mustStatus(t, request(t, h, "GET", "/api/memes/"+imageID.String(), nil, nil, ""), 200)
	var count int
	if err = pool.QueryRow(context.Background(), "SELECT count(*) FROM collection_memes WHERE collection_id=$1", cats.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphaned collection associations: %d %v", count, err)
	}
}
