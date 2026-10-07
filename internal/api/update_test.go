package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/update"
)

func newUpdateTestServer(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.7.0", "body": "notes", "html_url": "http://x/rel", "assets": []any{}})
	}))
	t.Cleanup(gh.Close)
	u, _ := url.Parse(gh.URL)
	ts, s := newTestServer(t)
	m, err := update.New(update.Options{Repo: "o/r", API: gh.URL, ExtraHosts: []string{u.Host}, Current: "v0.6.0", Meta: s.St, Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s.Upd = m
	resp, c := login(t, ts, "test-password-1")
	if resp.StatusCode != 200 {
		t.Fatal("login failed")
	}
	return ts, c
}

func TestUpdateEndpointsRequireAuth(t *testing.T) {
	ts, s := newTestServer(t)
	s.Upd, _ = update.New(update.Options{Current: "v0.6.0"})
	for _, c := range [][2]string{{"GET", "/api/update"}, {"POST", "/api/update/check"}, {"POST", "/api/update/apply"}, {"PUT", "/api/update/mode"}} {
		req, _ := http.NewRequest(c[0], ts.URL+c[1], strings.NewReader(`{"mode":"off"}`))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 401 {
			t.Errorf("%s %s without session: %d, want 401", c[0], c[1], resp.StatusCode)
		}
	}
	if s.Upd.Mode() != update.ModeNotify {
		t.Error("unauthenticated request changed the mode")
	}
}

func TestUpdateFlow(t *testing.T) {
	ts, c := newUpdateTestServer(t)
	get := func() update.Status {
		resp, err := c.Get(ts.URL + "/api/update")
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("GET: %v %v", resp, err)
		}
		var st update.Status
		json.NewDecoder(resp.Body).Decode(&st)
		return st
	}
	if st := get(); st.Current != "v0.6.0" || st.Mode != "notify" || st.Available {
		t.Fatalf("initial %+v", st)
	}
	resp, _ := c.Post(ts.URL+"/api/update/check", "application/json", nil)
	var st update.Status
	json.NewDecoder(resp.Body).Decode(&st)
	if !st.Available || st.Latest != "v0.7.0" {
		t.Fatalf("after check %+v", st)
	}
	put := func(body, origin string) int {
		req, _ := http.NewRequest("PUT", ts.URL+"/api/update/mode", strings.NewReader(body))
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return r.StatusCode
	}
	if code := put(`{"mode":"auto"}`, ""); code != 200 || get().Mode != "auto" {
		t.Errorf("set auto: %d", code)
	}
	if code := put(`{"mode":"bogus"}`, ""); code != 400 {
		t.Errorf("bogus mode: %d", code)
	}
	if code := put(`{"mode":"off"}`, "https://evil.example"); code != 403 || get().Mode != "auto" {
		t.Errorf("cross-origin PUT must be blocked: %d", code)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/api/update/apply", nil)
	req.Header.Set("Origin", "https://evil.example")
	if r, _ := c.Do(req); r.StatusCode != 403 {
		t.Errorf("cross-origin apply must be blocked: %d", r.StatusCode)
	}
	// no assets in the fake release: apply starts, then fails in the background (never installs anything)
	r, _ := c.Post(ts.URL+"/api/update/apply", "application/json", nil)
	if r.StatusCode != 202 && r.StatusCode != 409 {
		t.Errorf("apply: %d", r.StatusCode)
	}
	// let the background attempt finish before the test (and its TempDir) ends: otherwise it can still be writing
	// into the directory while the cleanup removes it ("directory not empty")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := get(); st.Apply.State != update.StateDownloading {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}
