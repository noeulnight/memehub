package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/noeulnight/memehub/apps/api/internal/config"
	"github.com/noeulnight/memehub/apps/api/internal/storage"
)

func testPNG(t *testing.T) ([]byte, string) {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1000, 500))); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b.Bytes())
	return b.Bytes(), hex.EncodeToString(sum[:])
}
func TestPrepareImage(t *testing.T) {
	data, hash := testPNG(t)
	width, height, thumb, err := prepareImage(data, "image/png", hash, int64(len(data)))
	if err != nil || width != 1000 || height != 500 {
		t.Fatalf("prepare image: %d %d %v", width, height, err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(thumb))
	if err != nil || cfg.Width != 640 || cfg.Height != 320 {
		t.Fatalf("thumbnail: %+v %v", cfg, err)
	}
	invalidData := []byte("not an image")
	invalidHash := sha256.Sum256(invalidData)
	oversized := bytes.Clone(data)
	binary.BigEndian.PutUint32(oversized[16:20], 5000)
	binary.BigEndian.PutUint32(oversized[20:24], 5000)
	binary.BigEndian.PutUint32(oversized[29:33], crc32.ChecksumIEEE(oversized[12:29]))
	oversizedHash := sha256.Sum256(oversized)
	for _, tc := range []struct {
		name, mime, hash string
		size             int64
		data             []byte
	}{
		{"wrong MIME", "image/jpeg", hash, int64(len(data)), data},
		{"wrong hash", "image/png", "0000000000000000000000000000000000000000000000000000000000000000", int64(len(data)), data},
		{"wrong size", "image/png", hash, int64(len(data) + 1), data},
		{"invalid data", "image/png", hex.EncodeToString(invalidHash[:]), int64(len(invalidData)), invalidData},
		{"oversized decoded dimensions", "image/png", hex.EncodeToString(oversizedHash[:]), int64(len(oversized)), oversized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, err := prepareImage(tc.data, tc.mime, tc.hash, tc.size); err == nil {
				t.Fatal("invalid image accepted")
			}
		})
	}
}

func TestPrepareAnimatedGIF(t *testing.T) {
	palette := color.Palette{color.Transparent, color.White}
	frames := []*image.Paletted{image.NewPaletted(image.Rect(0, 0, 100, 100), palette), image.NewPaletted(image.Rect(0, 0, 100, 100), palette)}
	frames[1].SetColorIndex(1, 1, 1)
	var encoded bytes.Buffer
	if err := gif.EncodeAll(&encoded, &gif.GIF{Image: frames, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded.Bytes())
	width, height, thumbnail, err := prepareImage(encoded.Bytes(), "image/gif", hex.EncodeToString(sum[:]), int64(encoded.Len()))
	if err != nil || width != 100 || height != 100 {
		t.Fatalf("GIF preparation: %v", err)
	}
	if _, err := png.Decode(bytes.NewReader(thumbnail)); err != nil {
		t.Fatalf("GIF analysis preview must be PNG: %v", err)
	}
}
func TestImageLifecycle(t *testing.T) {
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	if endpoint == "" {
		endpoint = fakeObjectStore(t)
	}
	pool := testPool(t)
	issuer := newIssuer(t)
	bucket := "memehub-test-" + time.Now().Format("150405")
	cfg := config.Config{AppBaseURL: "http://localhost:4321", OIDCIssuer: issuer.server.URL, OIDCClientID: "test-client", OIDCRedirectURL: "http://localhost:4321/api/auth/callback", SessionTTL: time.Hour, TextMaxLength: 10000, OpenAIModel: "test-model", S3Endpoint: endpoint, S3PresignEndpoint: endpoint, S3Region: "us-east-1", S3Bucket: bucket, S3AccessKey: "memehub-test", S3SecretKey: "memehub-test-secret", S3PathStyle: true, S3TTL: time.Minute, UploadMaxBytes: 10 << 20, UploadAllowedMIMEs: []string{"image/png", "image/jpeg", "image/webp"}}
	client := s3.NewFromConfig(aws.Config{Region: cfg.S3Region, Credentials: credentials.NewStaticCredentialsProvider(cfg.S3AccessKey, cfg.S3SecretKey, "")}, func(o *s3.Options) { o.BaseEndpoint = aws.String(endpoint); o.UsePathStyle = true })
	if _, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatal(err)
	}
	app, err := New(context.Background(), cfg, pool)
	if err != nil {
		t.Fatal(err)
	}
	h := app.Handler()
	cookies, csrf := signIn(t, h, issuer, "author", false)
	other, otherCSRF := signIn(t, h, issuer, "other", false)
	data, hash := testPNG(t)
	input := map[string]any{"content_type": "image", "original_filename": "meme.png", "mime_type": "image/png", "size_bytes": len(data), "sha256": hash, "title": "Image meme", "source_name": "Source", "tags": []string{"image"}}
	mustStatus(t, request(t, h, "POST", "/api/memes", input, nil, ""), 401)
	invalid := map[string]any{"content_type": "image", "original_filename": "m.svg", "mime_type": "image/svg+xml", "size_bytes": 100, "sha256": hash}
	mustStatus(t, request(t, h, "POST", "/api/memes", invalid, cookies, csrf), 422)
	created := request(t, h, "POST", "/api/memes", input, cookies, csrf)
	mustStatus(t, created, 201)
	var reservation struct {
		Meme
		Upload storage.Upload `json:"upload"`
	}
	if err = json.Unmarshal(created.Body.Bytes(), &reservation); err != nil {
		t.Fatal(err)
	}
	var reservedKey string
	if err = pool.QueryRow(context.Background(), "SELECT original_object_key FROM memes WHERE id=$1", reservation.ID).Scan(&reservedKey); err != nil || reservedKey != "originals/"+reservation.ID.String()+".png" {
		t.Fatal("new reservation must have a PNG extension", reservedKey, err)
	}
	// Simulate an upload reserved before the extension rollout.
	if _, err = pool.Exec(context.Background(), "UPDATE memes SET original_object_key=$2 WHERE id=$1", reservation.ID, "originals/"+reservation.ID.String()); err != nil {
		t.Fatal(err)
	}
	path := "/api/memes/" + reservation.ID.String()
	mustStatus(t, request(t, h, "GET", path+"/media", nil, nil, ""), 404)
	mustStatus(t, request(t, h, "GET", path+"/file", nil, nil, ""), 404)
	mustStatus(t, request(t, h, "GET", path, nil, nil, ""), 404)
	before := request(t, h, "GET", "/api/memes?content_type=image", nil, nil, "")
	mustStatus(t, before, 200)
	var page struct {
		Items []Meme `json:"items"`
	}
	if err = json.Unmarshal(before.Body.Bytes(), &page); err != nil || len(page.Items) != 0 {
		t.Fatal("incomplete image is public")
	}

	mustStatus(t, request(t, h, "POST", path+"/upload-url", nil, other, otherCSRF), 404)
	mustStatus(t, request(t, h, "POST", path+"/upload-completion", nil, cookies, ""), 403)
	mustStatus(t, request(t, h, "POST", path+"/upload-completion", nil, other, otherCSRF), 404)
	mustStatus(t, request(t, h, "POST", path+"/upload-completion", nil, cookies, csrf), 422)
	put := func(payload []byte) {
		t.Helper()
		req, err := http.NewRequest("PUT", reservation.Upload.URL, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range reservation.Upload.Headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("S3 upload %d: %s", resp.StatusCode, body)
		}
	}
	// Same byte count but different data must fail hash verification.
	corrupt := bytes.Clone(data)
	corrupt[len(corrupt)-1] ^= 1
	put(corrupt)
	mustStatus(t, request(t, h, "POST", path+"/upload-completion", nil, cookies, csrf), 422)
	// Two reservations can exist before either image is ready.
	pending := request(t, h, "POST", "/api/memes", input, cookies, csrf)
	mustStatus(t, pending, 201)
	var pendingMeme Meme
	if err = json.Unmarshal(pending.Body.Bytes(), &pendingMeme); err != nil {
		t.Fatal(err)
	}
	if err = app.store.Put(context.Background(), stagingKey(pendingMeme.ID), "image/png", data); err != nil {
		t.Fatal(err)
	}
	put(data)
	mustStatus(t, request(t, h, "POST", path+"/upload-completion", nil, cookies, csrf), 200)
	if err = pool.QueryRow(context.Background(), "SELECT original_object_key FROM memes WHERE id=$1", reservation.ID).Scan(&reservedKey); err != nil || reservedKey != "originals/"+reservation.ID.String()+".png" {
		t.Fatal("old reservation must complete with a PNG extension", reservedKey, err)
	}

	pendingPath := "/api/memes/" + pendingMeme.ID.String()
	mustStatus(t, request(t, h, "POST", pendingPath+"/upload-completion", nil, cookies, csrf), 409)
	mustStatus(t, request(t, h, "POST", pendingPath+"/upload-completion", map[string]bool{"allow_duplicate": true}, cookies, csrf), 200)
	mustStatus(t, request(t, h, "DELETE", pendingPath, nil, cookies, csrf), 204)

	mustStatus(t, request(t, h, "POST", path+"/upload-completion", nil, cookies, csrf), 200)
	mustStatus(t, request(t, h, "POST", path+"/upload-url", nil, cookies, csrf), 409)
	// A ready image blocks the same bytes unless the uploader explicitly confirms.
	mustStatus(t, request(t, h, "POST", "/api/memes", input, cookies, csrf), 409)
	input["allow_duplicate"] = true
	allowed := request(t, h, "POST", "/api/memes", input, cookies, csrf)
	mustStatus(t, allowed, 201)
	var duplicate struct {
		ID uuid.UUID `json:"id"`
	}
	if err = json.Unmarshal(allowed.Body.Bytes(), &duplicate); err != nil {
		t.Fatal(err)
	}
	if err = app.store.Put(context.Background(), stagingKey(duplicate.ID), "image/png", data); err != nil {
		t.Fatal(err)
	}
	duplicatePath := "/api/memes/" + duplicate.ID.String()
	mustStatus(t, request(t, h, "POST", duplicatePath+"/upload-completion", nil, cookies, csrf), 200)
	mustStatus(t, request(t, h, "DELETE", duplicatePath, nil, cookies, csrf), 204)
	mustStatus(t, request(t, h, "GET", duplicatePath+"/file", nil, nil, ""), 404)
	delete(input, "allow_duplicate")

	mustStatus(t, request(t, h, "GET", path, nil, nil, ""), 200)
	after := request(t, h, "GET", "/api/memes?content_type=image&q=Image", nil, nil, "")
	mustStatus(t, after, 200)
	if err = json.Unmarshal(after.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].ID != reservation.ID {
		t.Fatal("ready image is missing from search")
	}

	file := request(t, h, "GET", path+"/file", nil, nil, "")
	mustStatus(t, file, 200)
	if file.Header().Get("Content-Type") != "image/png" || file.Header().Get("Content-Disposition") != "inline" || !bytes.Equal(file.Body.Bytes(), data) {
		t.Fatal("stable URL did not serve original PNG")
	}
	head := request(t, h, "HEAD", path+"/file", nil, nil, "")
	mustStatus(t, head, 200)
	if head.Body.Len() != 0 || head.Header().Get("Content-Length") == "" {
		t.Fatal("invalid HEAD response")
	}
	media := request(t, h, "GET", path+"/media", nil, nil, "")
	mustStatus(t, media, 200)
	var urls struct{ Original, Thumbnail storage.SignedURL }
	if err = json.Unmarshal(media.Body.Bytes(), &urls); err != nil {
		t.Fatal(err)
	}
	// An unexpired staging URL must not overwrite the frozen original.
	put(corrupt)
	for _, url := range []string{urls.Original.URL, urls.Thumbnail.URL} {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("media %d: %s", resp.StatusCode, body)
		}
		if url == urls.Original.URL && !bytes.Equal(body, data) {
			t.Fatal("original was overwritten through staging URL")
		}
		if _, _, err = image.DecodeConfig(bytes.NewReader(body)); err != nil {
			t.Fatal("invalid media", err)
		}
	}
	var jobs int
	if err = pool.QueryRow(context.Background(), "SELECT count(*) FROM jobs WHERE meme_id=$1 AND type='analyze_content'", reservation.ID).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("expected one automatic analysis job: %d %v", jobs, err)
	}
}

// Local transport fixture; TEST_S3_ENDPOINT can run the same lifecycle against real S3-compatible storage.
func fakeObjectStore(t *testing.T) string {
	t.Helper()
	var mu sync.Mutex
	objects := map[string][]byte{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case "PUT":
			data, err := io.ReadAll(r.Body)
			if err != nil {
				w.WriteHeader(500)
				return
			}
			objects[r.URL.Path] = data
			w.WriteHeader(200)
		case "GET":
			data, ok := objects[r.URL.Path]
			if !ok {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(404)
				_, _ = w.Write([]byte("<Error><Code>NoSuchKey</Code></Error>"))
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(data)
		case "DELETE":
			delete(objects, r.URL.Path)
			w.WriteHeader(204)
		default:
			w.WriteHeader(405)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestGIFCompletionPublishesAnimatedPreview(t *testing.T) {
	if _, err := exec.LookPath("gif2webp"); err != nil {
		t.Skip("libwebp gif2webp is not installed")
	}
	pool := testPool(t)
	issuer := newIssuer(t)
	endpoint := fakeObjectStore(t)
	cfg := config.Config{AppBaseURL: "http://localhost:4321", OIDCIssuer: issuer.server.URL, OIDCClientID: "test-client", SessionTTL: time.Hour, OpenAIModel: "test-model", S3Endpoint: endpoint, S3PresignEndpoint: endpoint, S3Region: "us-east-1", S3Bucket: "gif-test", S3AccessKey: "test", S3SecretKey: "test-secret", S3PathStyle: true, S3TTL: time.Minute, UploadMaxBytes: 10 << 20, UploadAllowedMIMEs: []string{"image/gif"}}
	app, err := New(context.Background(), cfg, pool)
	if err != nil {
		t.Fatal(err)
	}
	h := app.Handler()
	cookies, csrf := signIn(t, h, issuer, "author", false)
	palette := color.Palette{color.Transparent, color.White}
	frames := []*image.Paletted{image.NewPaletted(image.Rect(0, 0, 100, 100), palette), image.NewPaletted(image.Rect(0, 0, 100, 100), palette)}
	frames[1].SetColorIndex(1, 1, 1)
	var encoded bytes.Buffer
	if err = gif.EncodeAll(&encoded, &gif.GIF{Image: frames, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	data := encoded.Bytes()
	sum := sha256.Sum256(data)
	created := request(t, h, "POST", "/api/memes", map[string]any{"content_type": "image", "original_filename": "cat.gif", "mime_type": "image/gif", "size_bytes": len(data), "sha256": hex.EncodeToString(sum[:])}, cookies, csrf)
	mustStatus(t, created, 201)
	var reservation struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(created.Body.Bytes(), &reservation); err != nil {
		t.Fatal(err)
	}
	if err = app.store.Put(context.Background(), "staging/"+reservation.ID, "image/gif", data); err != nil {
		t.Fatal(err)
	}
	path := "/api/memes/" + reservation.ID
	mustStatus(t, request(t, h, "POST", path+"/upload-completion", nil, cookies, csrf), 200)
	var staticKey, previewKey, originalKey string
	if err = pool.QueryRow(context.Background(), "SELECT thumbnail_object_key,preview_object_key,original_object_key FROM memes WHERE id=$1", reservation.ID).Scan(&staticKey, &previewKey, &originalKey); err != nil {
		t.Fatal(err)
	}
	if originalKey != "originals/"+reservation.ID+".gif" {
		t.Fatal("original GIF extension missing", originalKey)
	}
	if staticKey == previewKey || !strings.HasSuffix(previewKey, ".webp") {
		t.Fatal("static analysis image must remain separate")
	}
	file := request(t, h, "GET", path+"/file", nil, nil, "")
	mustStatus(t, file, 200)
	if file.Header().Get("Content-Type") != "image/gif" || !bytes.Equal(file.Body.Bytes(), data) {
		t.Fatal("stable URL changed original GIF")
	}
	media := request(t, h, "GET", path+"/media", nil, nil, "")
	mustStatus(t, media, 200)
	var urls struct{ Original, Thumbnail storage.SignedURL }
	if err = json.Unmarshal(media.Body.Bytes(), &urls); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		url       string
		isPreview bool
	}{{urls.Original.URL, false}, {urls.Thumbnail.URL, true}} {
		response, e := http.Get(entry.url)
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(response.Body)
		response.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		if entry.isPreview {
			if !bytes.Contains(body, []byte("ANIM")) {
				t.Fatal("animated gallery preview missing")
			}
		} else if !bytes.Equal(body, data) {
			t.Fatal("GIF original was changed")
		}
	}
}

func TestPublicImageCacheAndStaticOG(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	palette := color.Palette{color.Transparent, color.White}
	frames := []*image.Paletted{image.NewPaletted(image.Rect(0, 0, 100, 50), palette), image.NewPaletted(image.Rect(0, 0, 100, 50), palette)}
	frames[1].SetColorIndex(1, 1, 1)
	var encoded bytes.Buffer
	if err := gif.EncodeAll(&encoded, &gif.GIF{Image: frames, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	original := encoded.Bytes()
	sum := sha256.Sum256(original)
	hash := hex.EncodeToString(sum[:])
	_, _, thumbnail, err := prepareImage(original, "image/gif", hash, int64(len(original)))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	reads := 0
	unavailable := false
	objects := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		reads++
		if unavailable {
			w.WriteHeader(403)
			return
		}
		data := original
		if strings.HasSuffix(r.URL.Path, "thumb.png") {
			data = thumbnail
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}))
	t.Cleanup(objects.Close)
	store := storage.New(config.Config{S3Endpoint: objects.URL, S3PresignEndpoint: objects.URL, S3Region: "test", S3Bucket: "test", S3AccessKey: "test", S3SecretKey: "test"})
	app := &Server{db: pool, store: store}
	h := app.Handler()
	user, id := uuid.New(), uuid.New()
	execSQL := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	execSQL(`INSERT INTO users(id,oidc_issuer,oidc_subject) VALUES($1,'test','media-cache')`, user)
	execSQL(`INSERT INTO memes(id,created_by,content_type,title,status,analysis_status,original_filename,original_object_key,thumbnail_object_key,mime_type,size_bytes,width,height,sha256)
 VALUES($1,$2,'image','GIF','ready','completed','test.gif','original.gif','thumb.png','image/gif',$3,100,50,$4)`, id, user, len(original), hash)
	get := func(method, path, validator string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		if validator != "" {
			r.Header.Set("If-None-Match", validator)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	base := "/api/memes/" + id.String()
	detail := get("GET", base, "")
	mustStatus(t, detail, 200)
	var meme Meme
	if err := json.Unmarshal(detail.Body.Bytes(), &meme); err != nil || meme.MIMEType == nil || *meme.MIMEType != "image/gif" {
		t.Fatal("share links need the stored MIME type", err)
	}
	for _, extension := range []string{"jpg", "png", "webp"} {
		wrong := get("GET", base+"/file."+extension, "*")
		mustStatus(t, wrong, 404)
		if wrong.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("incorrect extension is cacheable")
		}
	}
	var originalTag string
	for index, suffix := range []string{"/file", "/file.gif", "/og.png"} {
		path := base + suffix
		head := get("HEAD", path, "")
		mustStatus(t, head, 200)
		if head.Body.Len() != 0 {
			t.Fatal("HEAD returned a body")
		}
		mu.Lock()
		n := reads
		mu.Unlock()
		if n != index {
			t.Fatal("HEAD read storage", n)
		}
		res := get("GET", path, "")
		mustStatus(t, res, 200)
		tag := res.Header().Get("ETag")
		if tag == "" || tag != head.Header().Get("ETag") || res.Header().Get("Cache-Control") != "public, max-age=60, must-revalidate" {
			t.Fatal("missing cache policy or unstable validator")
		}
		if suffix == "/file" || suffix == "/file.gif" {
			originalTag = tag
			if !bytes.Equal(res.Body.Bytes(), original) || res.Header().Get("Content-Type") != "image/gif" {
				t.Fatal("original GIF changed")
			}
		} else {
			if tag == originalTag || res.Header().Get("Content-Type") != "image/png" {
				t.Fatal("preview reused original representation")
			}
			img, err := png.Decode(bytes.NewReader(res.Body.Bytes()))
			if err != nil || img.Bounds().Dx() != 100 || img.Bounds().Dy() != 50 {
				t.Fatal("invalid static PNG", err)
			}
		}
		for _, validator := range []string{tag, "W/" + strings.TrimPrefix(tag, "W/"), `"other", ` + tag, "*"} {
			cached := get("GET", path, validator)
			mustStatus(t, cached, 304)
			if cached.Body.Len() != 0 || cached.Header().Get("ETag") != tag {
				t.Fatal("invalid 304")
			}
		}
		mu.Lock()
		n = reads
		mu.Unlock()
		if n != index+1 {
			t.Fatal("conditional requests read storage", n)
		}
	}
	// Errors must not inherit the public cache policy, and deletion must win over validators.
	mu.Lock()
	unavailable = true
	mu.Unlock()
	failed := get("GET", base+"/file", `"different"`)
	mustStatus(t, failed, 502)
	if failed.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("storage failure is cacheable")
	}
	execSQL(`UPDATE memes SET deleted_at=now() WHERE id=$1`, id)
	for _, suffix := range []string{"/file", "/file.gif", "/og.png"} {
		missing := get("GET", base+suffix, "*")
		mustStatus(t, missing, 404)
		if missing.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("deleted image is cacheable")
		}
	}
}
