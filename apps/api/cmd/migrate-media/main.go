package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/noeulnight/memehub/apps/api/internal/config"
	"github.com/noeulnight/memehub/apps/api/internal/storage"
)

func main() {
	envFile := flag.String("env-file", ".env", "Environment file path")
	apply := flag.Bool("apply", false, "Copy and verify originals, then update DB paths; default is read-only dry-run")
	flag.Parse()
	cfg, err := config.Load(*envFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid configuration")
		os.Exit(1)
	}
	store := storage.New(cfg)
	if store == nil {
		fmt.Fprintln(os.Stderr, "S3 storage is required")
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot configure database")
		os.Exit(1)
	}
	defer db.Close()
	if err = run(ctx, db, store, *apply, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type objectStore interface {
	Read(context.Context, string) (io.ReadCloser, error)
	Put(context.Context, string, string, []byte) error
}

func verifiedRead(ctx context.Context, store objectStore, key, hash string, size int64) ([]byte, error) {
	if size < 1 || size > 50<<20 {
		return nil, fmt.Errorf("invalid original size")
	}
	body, err := store.Read(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("cannot read object")
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, size+1))
	if err != nil || int64(len(data)) != size {
		return nil, fmt.Errorf("object size mismatch")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != hash {
		return nil, fmt.Errorf("object checksum mismatch")
	}
	return data, nil
}

func copyVerified(ctx context.Context, store objectStore, oldKey, newKey, mime, hash string, size int64) error {
	data, err := verifiedRead(ctx, store, oldKey, hash, size)
	if err != nil {
		return err
	}
	if err = store.Put(ctx, newKey, mime, data); err != nil {
		return fmt.Errorf("cannot write destination")
	}
	_, err = verifiedRead(ctx, store, newKey, hash, size)
	return err
}

func run(ctx context.Context, db *pgxpool.Pool, store objectStore, apply bool, out io.Writer) error {
	// Only published, live originals are migrated. Deleted items remain deleted.
	rows, err := db.Query(ctx, `SELECT id::text FROM memes WHERE content_type='image' AND status='ready' AND deleted_at IS NULL AND objects_deleted_at IS NULL AND original_object_key='originals/'||id::text ORDER BY id`)
	if err != nil {
		return fmt.Errorf("cannot list migration candidates")
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err != nil || rows.Err() != nil {
		return fmt.Errorf("cannot read migration candidates")
	}
	fmt.Fprintf(out, "candidates=%d apply=%t\n", len(ids), apply)
	for _, id := range ids {
		if err = migrateOne(ctx, db, store, id, apply, out); err != nil {
			return fmt.Errorf("meme %s: %w", id, err)
		}
	}
	return nil
}

func migrateOne(ctx context.Context, db *pgxpool.Pool, store objectStore, id string, apply bool, out io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("cannot start transaction")
	}
	defer tx.Rollback(context.Background())
	query := `SELECT original_object_key,mime_type,sha256,size_bytes FROM memes WHERE id=$1 AND content_type='image' AND status='ready' AND deleted_at IS NULL AND objects_deleted_at IS NULL AND original_object_key='originals/'||id::text`
	if apply {
		query += " FOR UPDATE"
	}
	var oldKey, mime, hash string
	var size int64
	err = tx.QueryRow(ctx, query, id).Scan(&oldKey, &mime, &hash, &size)
	if err == pgx.ErrNoRows {
		fmt.Fprintf(out, "skip %s (state changed)\n", id)
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot read meme")
	}
	ext := storage.ImageExtension(mime)
	if ext == "" {
		return fmt.Errorf("unsupported original MIME type")
	}
	newKey := oldKey + "." + ext
	fmt.Fprintf(out, "%s -> %s\n", oldKey, newKey)
	if !apply {
		return nil
	}
	// Keep the row locked during copy so deletion/cleanup cannot race the DB path change.
	if err = copyVerified(ctx, store, oldKey, newKey, mime, hash, size); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE memes SET original_object_key=$2,updated_at=now() WHERE id=$1`, id, newKey); err != nil {
		return fmt.Errorf("cannot update object path")
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("cannot commit object path; rerun safely")
	}
	// Retain the old object for existing URLs and rollback. Never delete it here.
	fmt.Fprintf(out, "migrated %s\n", id)
	return nil
}
