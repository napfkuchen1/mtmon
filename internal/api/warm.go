// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"
)

// warmEvery is the spacing of one warm-up step. The pages run one per step (round robin), so each page is warmed
// every 4 steps (80 s), inside the memo lifetimes (1–2 minutes) of store/memo.go callers.
const warmEvery = 20 * time.Second

// warmIdle: after this long without an interactive, session-authenticated request the warmer stops. A cold start
// then costs the first UI user one slower load, instead of the database being scanned around the clock.
const warmIdle = 10 * time.Minute

// warmBase is the monotonic reference for the last-UI timestamp.
var warmBase = time.Now()

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

// touchUI records an interactive request. Only the session-cookie branch of auth calls it; API tokens, health
// checks and the warmer's own calls (which bypass auth) do not reset the idle timer.
func (s *Server) touchUI() {
	s.uiLast.Store(int64(time.Since(warmBase)) + 1) // +1: 0 means "never"
}

// uiIdle reports whether no interactive request came within warmIdle.
func (s *Server) uiIdle() bool {
	v := s.uiLast.Load()
	if v == 0 {
		return true
	}
	return time.Since(warmBase)-time.Duration(v-1) > warmIdle
}

// WarmCaches keeps the memoised long-window queries hot while someone uses the UI: one page per step, and no step
// at all when the UI has been idle for warmIdle. Results are discarded; it only fills the store's memo. Stops with ctx.
func (s *Server) WarmCaches(ctx context.Context) {
	t := time.NewTimer(20 * time.Second) // let the start-up work (migrations, first polls) settle
	defer t.Stop()
	next := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !s.uiIdle() {
			s.warmStep(ctx, next)
			next = (next + 1) % len(warmPages)
		}
		t.Reset(warmEvery)
	}
}

// warmStep runs one warm-up page (index i).
func (s *Server) warmStep(ctx context.Context, i int) {
	if ctx.Err() != nil {
		return
	}
	p := warmPages[i]
	r := httptest.NewRequest("GET", p.path, nil).WithContext(ctx)
	p.h(s)(httptest.NewRecorder(), r)
}

// warmOnce runs all pages in a row (tests and the coverage check).
func (s *Server) warmOnce(ctx context.Context) {
	for i := range warmPages {
		s.warmStep(ctx, i)
	}
}
