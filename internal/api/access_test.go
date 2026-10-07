package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOriginBehindProxy(t *testing.T) {
	ts, s := newTestServer(t)
	_ = ts
	mk := func(remote, host, origin, xfh string) *http.Request {
		r := httptest.NewRequest("POST", "/api/x", nil)
		r.RemoteAddr, r.Host = remote, host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if xfh != "" {
			r.Header.Set("X-Forwarded-Host", xfh)
		}
		return r
	}
	if !s.originOK(mk("10.0.0.5:1234", "mtmon.lan:8443", "https://mtmon.lan:8443", "")) {
		t.Error("same host")
	}
	// nginx without "proxy_set_header Host $host": Host is the upstream, Origin is the public name
	if s.originOK(mk("127.0.0.1:5555", "127.0.0.1:8443", "https://mtmon.example.com", "")) {
		t.Error("must not accept the public origin without any hint")
	}
	if !s.originOK(mk("127.0.0.1:5555", "127.0.0.1:8443", "https://mtmon.example.com", "mtmon.example.com")) {
		t.Error("X-Forwarded-Host from a proxy on loopback must be accepted")
	}
	if s.originOK(mk("203.0.113.9:5555", "mtmon.lan", "https://evil.example", "evil.example")) {
		t.Error("X-Forwarded-Host from a public peer must be ignored")
	}
	s.St.SetMeta("public_url", "https://mtmon.example.com")
	if !s.originOK(mk("127.0.0.1:5555", "127.0.0.1:8443", "https://mtmon.example.com", "")) {
		t.Error("configured public URL must be accepted")
	}
	if s.originOK(mk("127.0.0.1:5555", "127.0.0.1:8443", "https://other.example", "")) {
		t.Error("a different origin stays refused")
	}
}

func TestAccessTokensIconEndpoints(t *testing.T) {
	ts, s := newTestServer(t)
	s.St.DB.Exec(`INSERT INTO clients(mac,first_seen,last_seen) VALUES('AA:00:00:00:00:01',1,1)`)
	_, c := login(t, ts, "test-password-1")
	do := func(method, path, body string, cl *http.Client, hdr map[string]string) (int, map[string]any) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		r, err := cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		return r.StatusCode, m
	}
	if code, _ := do("PUT", "/api/access", `{"public_url":"https://x.example/path"}`, c, nil); code != 400 {
		t.Errorf("public URL with a path: %d", code)
	}
	if code, _ := do("PUT", "/api/access", `{"listen_port":80}`, c, nil); code != 400 {
		t.Errorf("privileged port: %d", code)
	}
	if code, m := do("PUT", "/api/access", `{"public_url":"https://mtmon.example.com/","listen_port":9443}`, c, nil); code != 200 || m["public_url"] != "https://mtmon.example.com" || m["port_override"] != "9443" {
		t.Errorf("save: %d %v", code, m)
	}
	// API token: shown once, read-only, no token management with it
	code, m := do("POST", "/api/tokens", `{"name":"homeassistant"}`, c, nil)
	tok, _ := m["token"].(string)
	if code != 200 || !strings.HasPrefix(tok, "mtm_") {
		t.Fatalf("create token: %d %v", code, m)
	}
	anon := &http.Client{}
	bearer := map[string]string{"Authorization": "Bearer " + tok}
	if code, _ := do("GET", "/api/overview", "", anon, bearer); code != 200 {
		t.Errorf("token must read data: %d", code)
	}
	if code, _ := do("GET", "/api/overview", "", anon, map[string]string{"Authorization": "Bearer mtm_wrong"}); code != 401 {
		t.Errorf("wrong token: %d", code)
	}
	if code, _ := do("POST", "/api/clients/cleanup", `{"days":90,"dry_run":true}`, anon, bearer); code != 401 {
		t.Errorf("token must not write: %d", code)
	}
	if code, _ := do("GET", "/api/tokens", "", anon, bearer); code != 401 {
		t.Errorf("token must not manage tokens: %d", code)
	}
	if code, _ := do("DELETE", "/api/tokens/1", "", c, nil); code != 200 {
		t.Errorf("delete token: %d", code)
	}
	if code, _ := do("GET", "/api/overview", "", anon, bearer); code != 401 {
		t.Errorf("revoked token: %d", code)
	}
	// icon override
	if code, _ := do("PUT", "/api/clients/AA:00:00:00:00:01/icon", `{"icon":"printer3d"}`, c, nil); code != 200 {
		t.Errorf("icon: %d", code)
	}
	if cl, _ := s.St.Client("AA:00:00:00:00:01"); cl.Icon != "printer3d" {
		t.Errorf("icon not stored: %+v", cl)
	}
	if code, _ := do("PUT", "/api/clients/AA:00:00:00:00:01/icon", `{"icon":"<script>"}`, c, nil); code != 400 {
		t.Errorf("bad icon name: %d", code)
	}
	// restart not wired in tests
	if code, _ := do("POST", "/api/system/restart", `{}`, c, nil); code != 501 {
		t.Errorf("restart without hook: %d", code)
	}
	if code, _ := do("PUT", "/api/devices/nope", `{}`, c, nil); code != 404 && code != 503 {
		t.Errorf("edit unknown device: %d", code)
	}
}
