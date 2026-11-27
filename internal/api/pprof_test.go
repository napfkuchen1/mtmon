// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pprof is opt-in: off by default (the SPA fallback answers), and when enabled it sits behind auth.
func TestPprofOptIn(t *testing.T) {
	ts, s := newTestServer(t)
	resp, err := http.Get(ts.URL + "/debug/pprof/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "Types of profiles") {
		t.Fatal("pprof served while debug_pprof is off")
	}

	s.Cfg.DebugPprof = true
	h := New(s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("pprof without session: %d, want 401", rec.Code)
	}
}
