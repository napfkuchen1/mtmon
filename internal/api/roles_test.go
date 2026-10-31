package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// #192: the viewer reads operational data, gets 403 on settings, audit, tokens and every change.
func TestViewerRole(t *testing.T) {
	ts, s := newTestServer(t)
	_, admin := login(t, ts, "test-password-1")
	call := func(c *http.Client, method, path, body string) (int, string) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		b, _ := json.Marshal(m)
		return resp.StatusCode, string(b)
	}
	// no viewer yet
	resp, _ := http.Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"user":"watch","password":"viewer-pass-1"}`))
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("login of a viewer that does not exist: %d", resp.StatusCode)
	}
	if code, _ := call(admin, "PUT", "/api/viewer", `{"user":"admin","password":"viewer-pass-1"}`); code != 400 {
		t.Errorf("viewer named like the admin: %d", code)
	}
	if code, _ := call(admin, "PUT", "/api/viewer", `{"user":"watch","password":"short"}`); code != 400 {
		t.Errorf("short viewer password: %d", code)
	}
	if code, _ := call(admin, "PUT", "/api/viewer", `{"user":"watch","password":"viewer-pass-1"}`); code != 200 {
		t.Fatalf("create viewer: %d", code)
	}
	if h := s.St.GetMeta(metaViewerHash); h == "" || strings.Contains(h, "viewer-pass-1") {
		t.Errorf("viewer password not hashed: %q", h)
	}
	if _, v := login(t, ts, "viewer-pass-1"); v != nil { // wrong user name for this helper: must fail as admin
		if code, _ := call(v, "GET", "/api/devices", ""); code == 200 {
			t.Errorf("helper logs in as admin user with the viewer password")
		}
	}
	jar := &cookieJar{}
	v := &http.Client{Jar: jar}
	r, err := v.Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"user":"watch","password":"viewer-pass-1"}`))
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("viewer login: %v %v", err, r)
	}
	r.Body.Close()
	if code, body := call(v, "GET", "/api/me", ""); code != 200 || !strings.Contains(body, `"role":"viewer"`) {
		t.Errorf("me: %d %s", code, body)
	}
	if code, _ := call(v, "GET", "/api/devices", ""); code != 200 {
		t.Errorf("viewer cannot read devices: %d", code)
	}
	for _, p := range []string{"/api/audit", "/api/tokens", "/api/access", "/api/totp", "/api/viewer", "/api/notify", "/api/system", "/api/alert-settings", "/api/update"} {
		if code, _ := call(v, "GET", p, ""); code != 403 {
			t.Errorf("viewer GET %s: %d, want 403", p, code)
		}
	}
	for _, c := range [][2]string{{"PUT", "/api/timezone"}, {"POST", "/api/tokens"}, {"POST", "/api/devices/probe"}, {"POST", "/api/alerts/ack-all"}, {"PUT", "/api/viewer"}, {"POST", "/api/totp/setup"}} {
		if code, _ := call(v, c[0], c[1], `{}`); code != 403 {
			t.Errorf("viewer %s %s: %d, want 403", c[0], c[1], code)
		}
	}
	// the viewer's session survives a restart with its role (read back from the database)
	var tok string
	for _, c := range jar.Cookies(nil) {
		if c.Name == "mtmon_session" {
			tok = c.Value
		}
	}
	if tok != "" {
		s.sess.mu.Lock()
		delete(s.sess.tokens, tok)
		s.sess.mu.Unlock()
		if code, _ := call(v, "GET", "/api/audit", ""); code != 403 {
			t.Errorf("after restart the viewer got %d on /api/audit", code)
		}
	}
	// the admin is unaffected; removing the viewer stops its login
	if code, _ := call(admin, "GET", "/api/audit", ""); code != 200 {
		t.Errorf("admin audit: %d", code)
	}
	call(admin, "DELETE", "/api/viewer", "")
	r, _ = (&http.Client{}).Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"user":"watch","password":"viewer-pass-1"}`))
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Errorf("login after removing the viewer: %d", r.StatusCode)
	}
}
