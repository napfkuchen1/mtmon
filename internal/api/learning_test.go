package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestLearningEndpoints(t *testing.T) {
	ts, s := newTestServer(t)
	_, c := login(t, ts, "test-password-1")
	var m struct {
		Active bool  `json:"active"`
		Until  int64 `json:"until"`
	}
	get := func() {
		m.Active, m.Until = false, 0
		r, _ := c.Get(ts.URL + "/api/learning")
		json.NewDecoder(r.Body).Decode(&m)
	}
	get()
	if m.Active {
		t.Fatal("learning without a reason")
	}
	called := false
	s.ResetBaseline = func() { called = true }
	r, _ := c.Post(ts.URL+"/api/learning/reset", "application/json", strings.NewReader(`{"hours":6}`))
	if r.StatusCode != 200 || !called {
		t.Fatalf("reset: %d %v", r.StatusCode, called)
	}
	get()
	if !m.Active || m.Until < time.Now().Add(5*time.Hour).Unix() || m.Until > time.Now().Add(7*time.Hour).Unix() {
		t.Errorf("until: %+v", m)
	}
	r, _ = c.Get(ts.URL + "/api/overview?range=1h")
	var o struct {
		Until int64 `json:"learning_until"`
	}
	json.NewDecoder(r.Body).Decode(&o)
	if o.Until != m.Until {
		t.Errorf("overview learning_until: %d", o.Until)
	}
	if r, _ := c.Post(ts.URL+"/api/learning/reset", "application/json", strings.NewReader(`{"hours":9999}`)); r.StatusCode != http.StatusBadRequest {
		t.Errorf("too long: %d", r.StatusCode)
	}
}
