package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestEnrichSetupEndpoints(t *testing.T) {
	ts, s := newTestServer(t)
	_, c := login(t, ts, "test-password-1")
	post := func(path, body string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(body))
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		return r.StatusCode, m
	}
	if code, _ := post("/api/enrich/setup", `{"what":["../etc/passwd"]}`); code != 400 {
		t.Errorf("unknown dataset must be refused: %d", code)
	}
	r, _ := c.Get(ts.URL + "/api/enrich/setup")
	var st struct {
		Running bool        `json:"running"`
		Steps   []setupStep `json:"steps"`
	}
	json.NewDecoder(r.Body).Decode(&st)
	if st.Running || st.Steps == nil {
		t.Errorf("idle state must have an empty list, not null: %+v", st)
	}
	s.enr.mu.Lock()
	s.enr.running = true
	s.enr.mu.Unlock()
	if code, _ := post("/api/enrich/setup", `{}`); code != 409 {
		t.Errorf("second start while running: %d", code)
	}
	if code, m := post("/api/enrich/reverse_dns", `{"enabled":true}`); code != 200 || m["enabled"] != true || m["persisted"] != true || s.St.GetMeta("reverse_dns") != "1" || !s.En.Status()["reverse_dns"] {
		t.Errorf("reverse dns toggle: %d %v", code, m)
	}
}
