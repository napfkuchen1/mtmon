package store

import (
	"sync"
	"time"
)

// Long-window lists (25 h and more) answer from big rollup tables and change slowly: a short-lived memo keeps
// repeated page loads cheap. Every caller gets its own copy of the cached slice.
type memoEntry struct {
	at time.Time
	v  any
}

type memoCache struct {
	mu sync.Mutex
	m  map[string]memoEntry
}

func memoize[T any](c *memoCache, key string, ttl time.Duration, clone func(T) T, f func() (T, error)) (T, error) {
	c.mu.Lock()
	if e, ok := c.m[key]; ok && time.Since(e.at) < ttl {
		c.mu.Unlock()
		return clone(e.v.(T)), nil
	}
	c.mu.Unlock()
	v, err := f()
	if err != nil {
		return v, err
	}
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]memoEntry{}
	}
	if len(c.m) > 256 { // keys carry the window start: drop the stale ones now and then
		for k, e := range c.m {
			if time.Since(e.at) > 10*time.Minute {
				delete(c.m, k)
			}
		}
	}
	c.m[key] = memoEntry{time.Now(), v}
	c.mu.Unlock()
	return clone(v), nil
}

func sliceCopy[T any](v []T) []T { return append([]T(nil), v...) }

// ClearMemo drops the memoised long-window lists (the scale test measures cold queries).
func (s *Store) ClearMemo() { s.memo.mu.Lock(); s.memo.m = nil; s.memo.mu.Unlock() }
