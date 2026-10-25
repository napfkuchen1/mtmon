package api

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

// Every warm-up page must be a valid request for its handler, otherwise the memo is never filled.
func TestWarmPagesAnswer(t *testing.T) {
	_, s := newTestServer(t)
	for _, p := range warmPages {
		w := httptest.NewRecorder()
		p.h(s)(w, httptest.NewRequest("GET", p.path, nil))
		if w.Code != 200 {
			t.Errorf("%s: %d %s", p.path, w.Code, w.Body.String())
		}
	}
	s.warmOnce(context.Background())
}

func TestWarmCachesStopsWithContext(t *testing.T) {
	_, s := newTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.WarmCaches(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WarmCaches did not stop")
	}
}
