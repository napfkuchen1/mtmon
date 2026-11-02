// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/update"
)

// actionIDs runs the handler directly so the remote address can be chosen.
func actionIDs(t *testing.T, s *Server, remote string, hdr map[string]string) []string {
	t.Helper()
	r := httptest.NewRequest("GET", "/api/actions", nil)
	r.RemoteAddr = remote
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.actionsGet(w, r)
	var out struct{ Items []actionItem }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, i := range out.Items {
		ids = append(ids, i.ID)
	}
	return ids
}

func TestActionsProxyBanner(t *testing.T) {
	_, s := newTestServer(t)
	fwd := map[string]string{"X-Forwarded-For": "198.51.100.7", "X-Forwarded-Proto": "https"}
	if ids := actionIDs(t, s, "192.0.2.9:4000", nil); len(ids) != 0 {
		t.Errorf("direct request: %v", ids)
	}
	if ids := actionIDs(t, s, "192.0.2.9:4000", fwd); len(ids) != 1 || ids[0] != "proxy_untrusted" {
		t.Errorf("untrusted proxy: %v", ids)
	}
	if ids := actionIDs(t, s, "127.0.0.1:4000", fwd); len(ids) != 0 {
		t.Errorf("proxy on the same machine must not show the banner: %v", ids)
	}
	s.St.SetMeta(metaTrustedProxies, "192.0.2.9")
	s.loadTrustedProxies()
	t.Cleanup(func() { setTrustedProxies(nil) })
	if ids := actionIDs(t, s, "192.0.2.9:4000", fwd); len(ids) != 0 {
		t.Errorf("banner must vanish once the proxy is trusted: %v", ids)
	}
}

func TestActionsHelperBanner(t *testing.T) {
	_, s := newTestServer(t)
	d := t.TempDir()
	old := filepath.Join(d, "old")
	os.WriteFile(old, []byte("#!/bin/sh\nset -u\n"), 0o755)
	cur := filepath.Join(d, "cur")
	os.WriteFile(cur, []byte("#!/bin/sh\n# mtmon-helper-version: 2\n"), 0o755)
	for path, want := range map[string]int{old: 1, cur: 0, filepath.Join(d, "none"): 0, "": 0} {
		m, err := update.New(update.Options{HelperPath: path})
		if err != nil {
			t.Fatal(err)
		}
		s.Upd = m
		if ids := actionIDs(t, s, "127.0.0.1:1", nil); len(ids) != want {
			t.Errorf("helper %q: %v, want %d items", filepath.Base(path), ids, want)
		}
	}
}

// Scope matrix: for every static GET route the token answers exactly like the viewer rules say; /api/system is open
// to tokens again, /api/actions and the admin areas stay closed.
func TestTokenScopeMatrix(t *testing.T) {
	ts, _ := newTestServer(t)
	_, admin := login(t, ts, "test-password-1")
	req, _ := http.NewRequest("POST", ts.URL+"/api/tokens", strings.NewReader(`{"name":"m"}`))
	resp, err := admin.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("token: %v", err)
	}
	var out struct{ Token string }
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	bearer := map[string]string{"Authorization": "Bearer " + out.Token}

	src, _ := filepath.Glob("*.go")
	re := regexp.MustCompile(`"GET (/api/[a-z0-9/_-]*)"`)
	seen := map[string]bool{}
	for _, f := range src {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			seen[strings.TrimSuffix(m[1], "/")] = true
		}
	}
	if len(seen) < 20 {
		t.Fatalf("route scan found only %d routes", len(seen))
	}
	for _, p := range []string{"/api/tokens", "/api/access", "/api/audit"} {
		seen[p] = true
	}
	for p := range seen {
		allowed := viewerAllowed(httptest.NewRequest("GET", p, nil)) && !strings.HasPrefix(p, "/api/tokens") && !strings.HasPrefix(p, "/api/access") && !strings.HasPrefix(p, "/api/audit")
		code := get(t, ts.URL, &http.Client{}, p, bearer)
		if allowed && (code == 401 || code == 403) {
			t.Errorf("token GET %s: %d, want reachable", p, code)
		}
		if !allowed && code != 401 {
			t.Errorf("token GET %s: %d, want 401", p, code)
		}
	}
	for _, p := range []string{"/api/system", "/api/me", "/api/overview"} {
		if code := get(t, ts.URL, &http.Client{}, p, bearer); code != 200 {
			t.Errorf("token GET %s: %d, want 200", p, code)
		}
	}
	if code := get(t, ts.URL, &http.Client{}, "/api/actions", bearer); code != 401 {
		t.Errorf("token GET /api/actions: %d, want 401", code)
	}
	// the system answer carries operational numbers only: no paths, no proxy or origin settings
	req2, _ := http.NewRequest("GET", ts.URL+"/api/system", nil)
	req2.Header.Set("Authorization", bearer["Authorization"])
	r2, _ := (&http.Client{}).Do(req2)
	var sys map[string]any
	json.NewDecoder(r2.Body).Decode(&sys)
	r2.Body.Close()
	if sys["version"] == nil || sys["uptime_s"] == nil || sys["db_bytes"] == nil {
		t.Errorf("system lacks version/uptime/db fields: %v", sys)
	}
	for k, v := range sys {
		if s, ok := v.(string); ok && (strings.Contains(s, "/") || strings.Contains(strings.ToLower(k), "proxy") || strings.Contains(k, "origin") || strings.Contains(k, "dir")) && k != "version" {
			t.Errorf("system field %s looks like configuration: %v", k, v)
		}
	}
}
