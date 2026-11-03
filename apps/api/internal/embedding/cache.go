package embedding

import (
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

type cached struct {
	result  Result
	expires time.Time
}

// QueryCache stores query embeddings only, never filtered results or access decisions.
type QueryCache struct {
	source  Embedder
	mu      sync.Mutex
	entries map[string]cached
	flight  singleflight.Group
	slots   chan struct{}
}

func NewQueryCache(source Embedder) *QueryCache {
	return &QueryCache{source: source, entries: map[string]cached{}, slots: make(chan struct{}, 8)}
}
func (c *QueryCache) Embed(ctx context.Context, q string) (Result, error) {
	c.mu.Lock()
	entry, ok := c.entries[q]
	c.mu.Unlock()
	if ok && time.Now().Before(entry.expires) {
		return entry.result, nil
	}
	result := c.flight.DoChan(q, func() (any, error) {
		c.mu.Lock()
		entry, ok := c.entries[q]
		c.mu.Unlock()
		if ok && time.Now().Before(entry.expires) {
			return entry.result, nil
		}
		select {
		case c.slots <- struct{}{}:
			defer func() { <-c.slots }()
		default:
			return nil, errors.New("embedding concurrency limit")
		}
		callCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		r, err := c.source.Embed(callCtx, q)
		if err != nil {
			return nil, err
		}
		if !Valid(r.Vector) {
			return nil, &Failure{}
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if len(c.entries) >= 1024 {
			var oldest string
			var expiry time.Time
			for k, e := range c.entries {
				if oldest == "" || e.expires.Before(expiry) {
					oldest, expiry = k, e.expires
				}
			}
			delete(c.entries, oldest)
		}
		c.entries[q] = cached{r, time.Now().Add(10 * time.Minute)}
		return r, nil
	})
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case r := <-result:
		if r.Err != nil {
			return Result{}, r.Err
		}
		return r.Val.(Result), nil
	}
}
