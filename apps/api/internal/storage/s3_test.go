package storage

import (
	"context"
	"encoding/json"
	"github.com/noeulnight/memehub/apps/api/internal/config"
	"strings"
	"testing"
	"time"
)

func TestPublicReadURL(t *testing.T) {
	for _, pathStyle := range []bool{true, false} {
		s := New(config.Config{S3Endpoint: "https://s3.example.test", S3PresignEndpoint: "https://s3.example.test", S3Bucket: "memes", S3Region: "test", S3AccessKey: "test", S3SecretKey: "secret", S3PathStyle: pathStyle, S3TTL: time.Minute})
		got, err := s.ReadURL(context.Background(), "originals/cat gif.gif")
		if err != nil {
			t.Fatal(err)
		}
		want := "https://memes.s3.example.test/originals/cat%20gif.gif"
		if pathStyle {
			want = "https://s3.example.test/memes/originals/cat%20gif.gif"
		}
		if got.URL != want || got.ExpiresAt != nil {
			t.Fatalf("unexpected public URL: %+v", got)
		}
		payload, _ := json.Marshal(got)
		if strings.Contains(string(payload), "expires_at") {
			t.Fatal("public URL has expiry")
		}
		upload, err := s.UploadURL(context.Background(), "staging/test", "image/gif", 10)
		if err != nil || !strings.Contains(upload.URL, "X-Amz-Signature=") {
			t.Fatal("upload must remain signed", err)
		}
		for _, key := range []string{"staging/test", "../outside", "originals/../staging/test"} {
			if _, err := s.ReadURL(context.Background(), key); err == nil {
				t.Fatalf("exposed unsafe key: %s", key)
			}
		}
	}
}
