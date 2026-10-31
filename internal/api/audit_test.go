// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestMetaDiffMasksSecretsAndSkipsBookkeeping(t *testing.T) {
	before := map[string]string{"timezone": "", "notify": "a", "cfgid:gw": "1", "last_run_version": "x"}
	after := map[string]string{"timezone": "Europe/Berlin", "notify": "b", "cfgid:gw": "2", "last_run_version": "y", "smtp_pass": "hunter2"}
	got := metaDiff(before, after)
	if !strings.Contains(got, "timezone: – → Europe/Berlin") || !strings.Contains(got, "notify: changed (value hidden)") || !strings.Contains(got, "smtp_pass: changed (value hidden)") {
		t.Fatalf("diff: %q", got)
	}
	for _, bad := range []string{"hunter2", "cfgid", "last_run_version"} {
		if strings.Contains(got, bad) {
			t.Errorf("diff leaks %q: %q", bad, got)
		}
	}
}

func auditActions(t *testing.T, s *Server) map[string]store.AuditEntry {
	t.Helper()
	m := map[string]store.AuditEntry{}
	for _, e := range s.St.Audit(500, 0, "") {
		if _, ok := m[e.Action]; !ok {
			m[e.Action] = e
		}
	}
	return m
}

// #192: login ok/fail, a settings change with old→new, token create/revoke/use, logout.
func TestAuditLog(t *testing.T) {
	ts, s := newTestServer(t)
	// failed login
	resp, _ := http.Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"user":"admin","password":"wrong-password"}`))
	resp.Body.Close()
	_, c := login(t, ts, "test-password-1")
	do := func(method, path, body string) *http.Response {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	do("PUT", "/api/timezone", `{"zone":"Europe/Berlin"}`).Body.Close()
	do("PUT", "/api/timezone", `{"zone":"Nonsense/Zone"}`).Body.Close() // fails: not logged
	r := do("POST", "/api/tokens", `{"name":"cowork"}`)
	var tk struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	json.NewDecoder(r.Body).Decode(&tk)
	r.Body.Close()
	req, _ := http.NewRequest("GET", ts.URL+"/api/devices", nil)
	req.Header.Set("Authorization", "Bearer "+tk.Token)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 200 {
		t.Fatalf("token use: %v %v", err, resp)
	} else {
		resp.Body.Close()
	}
	do("DELETE", "/api/tokens/"+strconv.FormatInt(tk.ID, 10), "").Body.Close()
	do("POST", "/api/logout", "").Body.Close()

	got := auditActions(t, s)
	for _, a := range []string{"login.fail", "login.ok", "settings.timezone", "token.create", "token.use", "token.revoke", "logout"} {
		if _, ok := got[a]; !ok {
			t.Errorf("missing %q in %+v", a, got)
		}
	}
	if e := got["settings.timezone"]; !strings.Contains(e.Detail, "timezone: – → Europe/Berlin") || e.User != "admin" {
		t.Errorf("timezone entry: %+v", e)
	}
	if e := got["token.use"]; e.User != "token:cowork" {
		t.Errorf("token use entry: %+v", e)
	}
	n := 0
	for _, e := range s.St.Audit(500, 0, "settings.timezone") {
		n++
		_ = e
	}
	if n != 1 {
		t.Errorf("a failed change must not be logged, got %d timezone entries", n)
	}
	// secrets never end up in the log
	for _, e := range s.St.Audit(500, 0, "") {
		if strings.Contains(e.Detail, tk.Token) || strings.Contains(e.Detail, "test-password-1") {
			t.Errorf("secret in entry %+v", e)
		}
	}
}

func TestAuditAPIAdminOnlyAndRetention(t *testing.T) {
	ts, s := newTestServer(t)
	s.St.AddAudit(store.AuditEntry{TS: time.Now().Add(-181 * 24 * time.Hour).Unix(), User: "x", Action: "old"})
	s.St.AddAudit(store.AuditEntry{TS: time.Now().Unix(), User: "x", Action: "new"})
	s.St.PruneAudit(time.Now().Unix())
	if es := s.St.Audit(10, 0, ""); len(es) != 1 || es[0].Action != "new" {
		t.Fatalf("retention: %+v", es)
	}
	_, c := login(t, ts, "test-password-1")
	resp, err := c.Get(ts.URL + "/api/audit")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"entries":[`) {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
	// a read-only API token must not read the audit log
	tok := "mtm_" + strings.Repeat("a", 48)
	s.St.CreateToken("t", hashToken(tok), tok[:8])
	req, _ := http.NewRequest("GET", ts.URL+"/api/audit", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("token on /api/audit: %d", resp.StatusCode)
	}
}
