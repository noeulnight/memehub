package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noeulnight/memehub/apps/api/internal/cleanup"
	"github.com/noeulnight/memehub/apps/api/internal/config"
	"github.com/noeulnight/memehub/apps/api/internal/storage"
)

func audioFixture(t *testing.T, extension string, extra ...string) ([]byte, string) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	path := filepath.Join(t.TempDir(), "sample."+extension)
	args := []string{"-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.2"}
	if extension == "ogg" {
		args = append(args, "-c:a", "libopus")
	}
	args = append(args, extra...)
	args = append(args, "-y", path)
	if output, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:])
}

func TestPrepareAudio(t *testing.T) {
	for _, tc := range []struct{ extension, mime string }{{"mp3", "audio/mpeg"}, {"wav", "audio/wav"}, {"ogg", "audio/ogg"}, {"m4a", "audio/mp4"}} {
		t.Run(tc.extension, func(t *testing.T) {
			data, hash := audioFixture(t, tc.extension)
			if err := prepareAudio(context.Background(), data, tc.mime, hash, int64(len(data))); err != nil {
				t.Fatal(err)
			}
			for _, invalid := range []struct {
				mime, hash string
				size       int64
			}{{"audio/ogg", hash, int64(len(data))}, {tc.mime, strings.Repeat("0", 64), int64(len(data))}, {tc.mime, hash, int64(len(data) + 1)}} {
				if invalid.mime == tc.mime && invalid.hash == hash && invalid.size == int64(len(data)) {
					continue
				}
				if err := prepareAudio(context.Background(), data, invalid.mime, invalid.hash, invalid.size); err == nil {
					t.Fatal("invalid audio accepted")
				}
			}
		})
	}
	data := []byte("not an audio file")
	sum := sha256.Sum256(data)
	if err := prepareAudio(context.Background(), data, "audio/mpeg", hex.EncodeToString(sum[:]), int64(len(data))); err == nil {
		t.Fatal("junk accepted")
	}
	// M4A containers carrying video must not bypass the audio-only validator.
	data, hash := audioFixture(t, "mp4", "-f", "lavfi", "-i", "color=size=16x16:duration=0.2", "-c:v", "mpeg4", "-shortest")
	if err := prepareAudio(context.Background(), data, "audio/mp4", hash, int64(len(data))); err == nil {
		t.Fatal("video accepted as audio")
	}
}

func TestAudioLifecycle(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	issuer := newIssuer(t)
	endpoint := fakeObjectStore(t)
	cfg := config.Config{AppBaseURL: "http://localhost:4321", OIDCIssuer: issuer.server.URL, OIDCClientID: "test-client", SessionTTL: time.Hour, OpenAIModel: "test-model", S3Endpoint: endpoint, S3PresignEndpoint: endpoint, S3Region: "us-east-1", S3Bucket: "audio-test", S3AccessKey: "test", S3SecretKey: "test-secret", S3PathStyle: true, S3TTL: time.Minute, UploadMaxBytes: 10 << 20, UploadAllowedMIMEs: []string{"audio/mpeg"}}
	app, err := New(ctx, cfg, pool)
	if err != nil {
		t.Fatal(err)
	}
	h := app.Handler()
	owner, csrf := signIn(t, h, issuer, "author", false)
	other, otherCSRF := signIn(t, h, issuer, "other", false)
	admin, adminCSRF := signIn(t, h, issuer, "admin", false)
	if _, err := pool.Exec(ctx, "UPDATE users SET is_admin=true WHERE oidc_subject='admin'"); err != nil {
		t.Fatal(err)
	}
	data, hash := audioFixture(t, "mp3")
	input := map[string]any{"content_type": "audio", "original_filename": "sample.mp3", "mime_type": "audio/mpeg", "size_bytes": len(data), "sha256": hash, "title": "Sound", "tags": []string{"sound"}}
	mustStatus(t, request(t, h, "POST", "/api/memes", input, nil, ""), 401)
	created := request(t, h, "POST", "/api/memes", input, owner, csrf)
	mustStatus(t, created, 201)
	var reservation struct {
		Meme
		Upload storage.Upload `json:"upload"`
	}
	if err = json.Unmarshal(created.Body.Bytes(), &reservation); err != nil {
		t.Fatal(err)
	}
	path := "/api/memes/" + reservation.ID.String()
	mustStatus(t, request(t, h, "GET", path+"/media", nil, nil, ""), 404)
	mustStatus(t, request(t, h, "POST", path+"/upload-url", nil, other, otherCSRF), 404)
	mustStatus(t, request(t, h, "POST", path+"/upload-url", nil, owner, csrf), 200)
	mustStatus(t, request(t, h, "POST", path+"/upload-completion", nil, owner, ""), 403)
	mustStatus(t, request(t, h, "POST", path+"/upload-completion", nil, other, otherCSRF), 404)
	if err = app.store.Put(ctx, stagingKey(reservation.ID), "audio/mpeg", []byte("invalid")); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, request(t, h, "POST", path+"/upload-completion", nil, owner, csrf), 422)
	if err = app.store.Put(ctx, stagingKey(reservation.ID), "audio/mpeg", data); err != nil {
		t.Fatal(err)
	}
	complete := request(t, h, "POST", path+"/upload-completion", nil, owner, csrf)
	mustStatus(t, complete, 200)
	var meme Meme
	if err = json.Unmarshal(complete.Body.Bytes(), &meme); err != nil {
		t.Fatal(err)
	}
	if meme.ContentType != "audio" || meme.AnalysisStatus != "not_requested" || meme.Width != nil || meme.Height != nil {
		t.Fatalf("invalid audio: %+v", meme)
	}
	var key string
	var jobs int
	if err = pool.QueryRow(ctx, "SELECT original_object_key FROM memes WHERE id=$1", meme.ID).Scan(&key); err != nil || key != "originals/"+meme.ID.String()+".mp3" {
		t.Fatal(key, err)
	}
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE meme_id=$1 AND type='analyze_content'", meme.ID).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatal("audio analysis enqueued", err)
	}
	mustStatus(t, request(t, h, "POST", "/api/memes", input, owner, csrf), 409)
	mustStatus(t, request(t, h, "POST", "/api/admin/memes/"+meme.ID.String()+"/analyses", map[string]any{"content_revision": 0}, admin, adminCSRF), 422)
	filtered := request(t, h, "GET", "/api/memes?content_type=audio&q=Sound", nil, nil, "")
	mustStatus(t, filtered, 200)
	if !bytes.Contains(filtered.Body.Bytes(), []byte(meme.ID.String())) {
		t.Fatal("audio missing from search")
	}
	file := request(t, h, "GET", path+"/file.mp3", nil, nil, "")
	mustStatus(t, file, 200)
	if file.Header().Get("Content-Type") != "audio/mpeg" || !bytes.Equal(file.Body.Bytes(), data) {
		t.Fatal("original audio changed")
	}
	mustStatus(t, request(t, h, "GET", path+"/file.wav", nil, nil, ""), 404)
	mustStatus(t, request(t, h, "GET", path+"/media", nil, nil, ""), 200)
	edit := map[string]any{"content_type": "audio", "content_revision": 0, "title": "Updated sound", "tags": []string{"sound"}}
	mustStatus(t, request(t, h, "PUT", path, edit, other, otherCSRF), 404)
	mustStatus(t, request(t, h, "PUT", path, edit, owner, csrf), 200)
	mustStatus(t, request(t, h, "PUT", path, edit, owner, csrf), 409)
	edit["content_revision"] = 1
	mustStatus(t, request(t, h, "PUT", path, edit, admin, adminCSRF), 200)
	mustStatus(t, request(t, h, "DELETE", path, nil, other, otherCSRF), 404)
	mustStatus(t, request(t, h, "DELETE", path, nil, admin, adminCSRF), 204)
	mustStatus(t, request(t, h, "GET", path+"/file.mp3", nil, nil, ""), 404)
	if _, err = pool.Exec(ctx, "UPDATE memes SET deleted_at=now()-interval '8 days' WHERE id=$1", meme.ID); err != nil {
		t.Fatal(err)
	}
	worker := cleanup.Worker{DB: pool, Store: app.store, DeletedRetention: 7 * 24 * time.Hour, UploadRetention: 24 * time.Hour}
	if worked, err := worker.RunOne(ctx); !worked || err != nil {
		t.Fatalf("audio cleanup: %v", err)
	}
	if body, err := app.store.Read(ctx, key); err == nil {
		body.Close()
		t.Fatal("audio original not deleted")
	}
}
