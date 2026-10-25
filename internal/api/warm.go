package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"
)

// warmEvery stays below the memo lifetimes in store/memo.go callers (1–2 minutes) so the long-window answers
// are always cached when a page asks; the first load after a quiet period used to cost 1–3 s on a big database.
const warmEvery = 50 * time.Second

// warmPages are the long-window reads the dashboard and the Services page open with.
var warmPages = []struct {
	path string
	h    func(*Server) http.HandlerFunc
}{
	{"/api/overview?range=7d", func(s *Server) http.HandlerFunc { return s.overview }},
	{"/api/overview?range=30d", func(s *Server) http.HandlerFunc { return s.overview }},
	{"/api/services?range=24h", func(s *Server) http.HandlerFunc { return s.services }},
	{"/api/services?range=7d", func(s *Server) http.HandlerFunc { return s.services }},
}

// WarmCaches keeps the memoised long-window queries hot by running the handlers in the background. Results are
// discarded; it only fills the store's memo. Stops with ctx.
func (s *Server) WarmCaches(ctx context.Context) {
	t := time.NewTimer(20 * time.Second) // let the start-up work (migrations, first polls) settle
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.warmOnce(ctx)
		t.Reset(warmEvery)
	}
}

func (s *Server) warmOnce(ctx context.Context) {
	for _, p := range warmPages {
		if ctx.Err() != nil {
			return
		}
		r := httptest.NewRequest("GET", p.path, nil).WithContext(ctx)
		p.h(s)(httptest.NewRecorder(), r)
	}
}
