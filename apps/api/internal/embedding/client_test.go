package embedding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func unitVector() []float32 { v := make([]float32, Dimensions); v[0] = 1; return v }
func TestClientValidationAndSafeErrors(t *testing.T) {
	for _, kind := range []string{"valid", "dimensions", "zero", "model", "429", "401", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var p struct {
					Model, Input string
					Dimensions   int
				}
				if json.NewDecoder(r.Body).Decode(&p) != nil || p.Model != Model || p.Dimensions != Dimensions || p.Input != "고양이" || r.Header.Get("Authorization") != "Bearer secret" {
					t.Error("invalid embedding request")
				}
				if kind == "429" {
					w.WriteHeader(429)
					w.Write([]byte("secret"))
					return
				}
				if kind == "401" {
					w.WriteHeader(401)
					return
				}
				if kind == "malformed" {
					w.Write([]byte("secret"))
					return
				}
				v := unitVector()
				if kind == "dimensions" {
					v = v[:10]
				}
				if kind == "zero" {
					v = make([]float32, Dimensions)
				}
				model := Model
				if kind == "model" {
					model = "other"
				}
				json.NewEncoder(w).Encode(map[string]any{"model": model, "data": []any{map[string]any{"index": 0, "embedding": v}}, "usage": map[string]any{"total_tokens": 9}})
			}))
			defer ts.Close()
			c := NewClient("secret")
			c.endpoint = ts.URL
			r, err := c.Embed(context.Background(), "고양이")
			if kind == "valid" {
				if err != nil || r.Tokens != 9 || !Valid(r.Vector) {
					t.Fatal(r.Tokens, err)
				}
				return
			}
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal("unsafe or missing error", err)
			}
			if retryable(err) != (kind == "429") {
				t.Fatal("incorrect retry policy")
			}
		})
	}
}

type embedFunc func(context.Context, string) (Result, error)

func (f embedFunc) Embed(c context.Context, s string) (Result, error) { return f(c, s) }
func TestQueryCacheCoalescesAndHonorsCancellation(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	c := NewQueryCache(embedFunc(func(ctx context.Context, q string) (Result, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return Result{Vector: unitVector()}, nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.Embed(ctx, "고양이"); done <- err }()
	<-started
	cancel()
	if !errors.Is(<-done, context.Canceled) {
		t.Fatal("caller cancellation ignored")
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			r, err := c.Embed(context.Background(), "고양이")
			if err != nil || !Valid(r.Vector) {
				t.Error("missing shared result", err)
			}
		})
	}
	close(release)
	wg.Wait()
	if _, err := c.Embed(context.Background(), "고양이"); err != nil || calls.Load() != 1 {
		t.Fatal("query was embedded more than once", calls.Load(), err)
	}
	c.mu.Lock()
	c.entries["고양이"] = cached{Result{Vector: unitVector()}, time.Now().Add(-time.Minute)}
	c.mu.Unlock()
	if _, err := c.Embed(context.Background(), "고양이"); err != nil || calls.Load() != 2 {
		t.Fatal("expired entry reused")
	}
}
func TestInputBoundsAndHash(t *testing.T) {
	s := Input("title", strings.Repeat("고양이", 4000), "ocr", "body", []string{"tag"})
	if len(s) > 7500 || !utf8.ValidString(s) || Hash(s) == Hash(s+"x") {
		t.Fatal("invalid input or hash")
	}
}
