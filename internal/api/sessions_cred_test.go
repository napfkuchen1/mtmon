// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// get returns the status of an authenticated GET.
func get(t *testing.T, base string, c *http.Client, path string, hdr map[string]string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", base+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func viewerLoginClient(t *testing.T, base, pw string) *http.Client {
	t.Helper()
	c := &http.Client{Jar: &cookieJar{}}
	r, err := c.Post(base+"/api/login", "application/json", strings.NewReader(`{"user":"watch","password":"`+pw+`"}`))
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("viewer login: %v %v", err, r)
	}
	r.Body.Close()
	return c
}

func putJSON(t *testing.T, base string, c *http.Client, method, path, body string) int {
	t.Helper()
	req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// Advisory: removing the viewer or changing its password ends the viewer's existing sessions at once.
func TestViewerSessionsEndOnRemovalAndPasswordChange(t *testing.T) {
	ts, _ := newTestServer(t)
	_, admin := login(t, ts, "test-password-1")
	if code := putJSON(t, ts.URL, admin, "PUT", "/api/viewer", `{"user":"watch","password":"viewer-pass-1"}`); code != 200 {
		t.Fatalf("create viewer: %d", code)
	}
	v := viewerLoginClient(t, ts.URL, "viewer-pass-1")
	if code := get(t, ts.URL, v, "/api/devices", nil); code != 200 {
		t.Fatalf("viewer session: %d", code)
	}
	putJSON(t, ts.URL, admin, "PUT", "/api/viewer", `{"user":"watch","password":"viewer-pass-2"}`)
	if code := get(t, ts.URL, v, "/api/devices", nil); code != 401 {
		t.Errorf("after a viewer password change the old session must end: %d", code)
	}
	v2 := viewerLoginClient(t, ts.URL, "viewer-pass-2")
	putJSON(t, ts.URL, admin, "DELETE", "/api/viewer", "")
	if code := get(t, ts.URL, v2, "/api/devices", nil); code != 401 {
		t.Errorf("after removing the viewer its session must end: %d", code)
	}
	if code := get(t, ts.URL, admin, "/api/devices", nil); code != 200 {
		t.Errorf("admin session must not be affected: %d", code)
	}
}

// Advisory: a new admin password (mtmon passwd + restart) ends admin sessions opened with the old one, including
// sessions read back from the database after the restart.
func TestAdminSessionsEndOnPasswordChange(t *testing.T) {
	ts, s := newTestServer(t)
	_, admin := login(t, ts, "test-password-1")
	if code := get(t, ts.URL, admin, "/api/devices", nil); code != 200 {
		t.Fatalf("admin session: %d", code)
	}
	h, _ := HashPassword("test-password-2")
	s.Cfg.AdminHash = h
	s.sess.mu.Lock() // restart: memory empty, only the database row is left
	s.sess.tokens = map[string]session{}
	s.sess.mu.Unlock()
	if code := get(t, ts.URL, admin, "/api/devices", nil); code != 401 {
		t.Errorf("session opened with the old admin password must end: %d", code)
	}
	_, admin2 := login(t, ts, "test-password-2")
	if code := get(t, ts.URL, admin2, "/api/devices", nil); code != 200 {
		t.Errorf("new login: %d", code)
	}
}

// Advisory: role and credential are stored with the session in one write; a viewer session read back from the
// database is a viewer session, and a row without credential (older version / failed write) is not accepted.
func TestStoredSessionKeepsRoleAndCred(t *testing.T) {
	ts, s := newTestServer(t)
	_, admin := login(t, ts, "test-password-1")
	putJSON(t, ts.URL, admin, "PUT", "/api/viewer", `{"user":"watch","password":"viewer-pass-1"}`)
	tok, err := s.sess.open(roleViewer)
	if err != nil {
		t.Fatal(err)
	}
	row, ok := s.St.LoadSession(hashToken(tok))
	if !ok || row.Role != roleViewer || row.Cred != s.credOf(roleViewer) || row.Cred == "" {
		t.Fatalf("stored session: %+v %v", row, ok)
	}
	s.sess.mu.Lock()
	delete(s.sess.tokens, tok)
	s.sess.mu.Unlock()
	if !s.sess.valid(tok) || s.sess.roleOf(tok) != roleViewer {
		t.Fatal("after a restart the viewer session must stay a viewer session")
	}
	// a row as written by an older version: admin role "", no credential
	if err := s.St.CreateSession(hashToken("legacy"), row.IdleUntil, row.Born, "", ""); err != nil {
		t.Fatal(err)
	}
	if s.sess.valid("legacy") {
		t.Error("a session without credential binding must not be accepted")
	}
	if _, ok := s.St.LoadSession(hashToken("legacy")); ok {
		t.Error("the rejected row must be removed")
	}
	if credFingerprint("") == credFingerprint("x") || credFingerprint("") == "" {
		t.Error("an empty hash must have its own, non-empty fingerprint")
	}
}

// Advisory: a read-only API token never reaches more than the viewer role.
func TestTokenNotBroaderThanViewer(t *testing.T) {
	ts, _ := newTestServer(t)
	_, admin := login(t, ts, "test-password-1")
	req, _ := http.NewRequest("POST", ts.URL+"/api/tokens", strings.NewReader(`{"name":"ha"}`))
	resp, err := admin.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("create token: %v %v", err, resp)
	}
	var out struct{ Token string }
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if !strings.HasPrefix(out.Token, "mtm_") {
		t.Fatalf("token: %q", out.Token)
	}
	bearer := map[string]string{"Authorization": "Bearer " + out.Token}
	anon := &http.Client{}
	if code := get(t, ts.URL, anon, "/api/overview", bearer); code != 200 {
		t.Errorf("token must read operational data: %d", code)
	}
	for _, d := range viewerDenied {
		if code := get(t, ts.URL, anon, d, bearer); code == 200 {
			t.Errorf("token reached viewer-denied %s", d)
		}
	}
	for _, p := range []string{"/api/devices/gw/config", "/api/devices/gw/manifest", "/api/devices/gw/offboard-script"} {
		if viewerAllowed(httptest.NewRequest("GET", p, nil)) {
			t.Fatalf("test assumption: %s is viewer-denied", p)
		}
		if code := get(t, ts.URL, anon, p, bearer); code == 200 {
			t.Errorf("token reached %s", p)
		}
	}
}

// #301: a failed login with an unknown user name is answered against the dummy hash, a known name is not.
func TestUnknownUserLoginCostsAHash(t *testing.T) {
	ts, _ := newTestServer(t)
	post := func(user string) int {
		r, err := http.Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"user":"`+user+`","password":"wrong-password"}`))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	before := dummyCount.Load()
	if code := post("nobody"); code != 401 {
		t.Fatalf("unknown user: %d", code)
	}
	if dummyCount.Load() != before+1 {
		t.Error("unknown user must run the dummy hash")
	}
	before = dummyCount.Load()
	if code := post("admin"); code != 401 {
		t.Fatalf("known user, wrong password: %d", code)
	}
	if dummyCount.Load() != before {
		t.Error("known user already costs a hash; no dummy run")
	}
}
