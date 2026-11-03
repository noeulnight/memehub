package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type memoryStore struct {
	data          map[string][]byte
	mime          map[string]string
	writes, reads int
	corrupt       bool
}

func (s *memoryStore) Read(_ context.Context, key string) (io.ReadCloser, error) {
	s.reads++
	v, ok := s.data[key]
	if !ok {
		return nil, fmt.Errorf("missing")
	}
	return io.NopCloser(bytes.NewReader(v)), nil
}
func (s *memoryStore) Put(_ context.Context, key, mime string, data []byte) error {
	s.writes++
	s.data[key] = bytes.Clone(data)
	s.mime[key] = mime
	if s.corrupt {
		s.data[key][0] ^= 1
	}
	return nil
}

func TestCopyVerification(t *testing.T) {
	data := []byte("animated source bytes")
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	for _, tc := range []struct {
		name       string
		corrupt    bool
		hash       string
		wantWrites int
	}{{"source mismatch", false, strings.Repeat("0", 64), 0}, {"destination mismatch", true, hash, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			s := &memoryStore{data: map[string][]byte{"old": data}, mime: map[string]string{}, corrupt: tc.corrupt}
			if err := copyVerified(context.Background(), s, "old", "new", "image/gif", tc.hash, int64(len(data))); err == nil {
				t.Fatal("accepted checksum mismatch")
			}
			if s.writes != tc.wantWrites {
				t.Fatal("unexpected writes", s.writes)
			}
		})
	}
}

func TestMigrationLifecycle(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "media_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+ident+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(ctx, `CREATE TABLE memes(id uuid PRIMARY KEY,content_type text,status text,deleted_at timestamptz,objects_deleted_at timestamptz,original_object_key text,mime_type text,sha256 text,size_bytes bigint,updated_at timestamptz DEFAULT now())`)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("original bytes")
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	s := &memoryStore{data: map[string][]byte{}, mime: map[string]string{}}
	ids := []string{}
	for i, mime := range []string{"image/jpeg", "image/png", "image/webp", "image/gif", "image/gif", "image/png"} {
		id := uuid.NewString()
		ids = append(ids, id)
		key := "originals/" + id
		s.data[key] = bytes.Clone(data)
		_, err = db.Exec(ctx, `INSERT INTO memes(id,content_type,status,original_object_key,mime_type,sha256,size_bytes,deleted_at) VALUES($1,'image',$2,$3,$4,$5,$6,CASE WHEN $7 THEN now() ELSE NULL END)`, id, map[bool]string{true: "uploading", false: "ready"}[i == 5], key, mime, hash, len(data), i == 4)
		if err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err = run(ctx, db, s, false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "candidates=4 apply=false") || s.writes != 0 || s.reads != 0 {
		t.Fatal("dry-run touched storage", out.String())
	}
	s.corrupt = true
	if err = run(ctx, db, s, true, io.Discard); err == nil {
		t.Fatal("accepted corrupt destination")
	}
	var changed int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM memes WHERE original_object_key<>'originals/'||id::text`).Scan(&changed); err != nil || changed != 0 {
		t.Fatal("changed DB before verification", changed, err)
	}
	s.corrupt = false
	if err = run(ctx, db, s, true, io.Discard); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		var key, mime string
		if err = db.QueryRow(ctx, `SELECT original_object_key,mime_type FROM memes WHERE id=$1`, id).Scan(&key, &mime); err != nil {
			t.Fatal(err)
		}
		if i >= 4 {
			if key != "originals/"+id {
				t.Fatal("migrated deleted/incomplete image")
			}
			continue
		}
		ext := []string{"jpg", "png", "webp", "gif"}[i]
		if key != "originals/"+id+"."+ext || !bytes.Equal(s.data[key], data) || s.mime[key] != mime {
			t.Fatal("invalid destination", key)
		}
		if !bytes.Equal(s.data["originals/"+id], data) {
			t.Fatal("removed original")
		}
	}
	out.Reset()
	writes := s.writes
	if err = run(ctx, db, s, true, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "candidates=0") || s.writes != writes {
		t.Fatal("rerun is not idempotent", out.String())
	}
}
