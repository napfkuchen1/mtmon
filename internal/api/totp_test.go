package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/secret"
)

// RFC 6238 appendix B, SHA-1 secret "12345678901234567890": T=59 → 94287082 (8 digits), so the last 6 are 287082.
func TestTOTPRFCVector(t *testing.T) {
	secret := []byte("12345678901234567890")
	for ts, want := range map[int64]string{59: "287082", 1111111109: "081804", 1111111111: "050471", 2000000000: "279037"} {
		if got := totpAt(secret, ts/totpStep); got != want {
			t.Errorf("T=%d: got %s want %s", ts, got, want)
		}
	}
}

func TestTOTPSkew(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1111111109, 0)
	cur := now.Unix() / totpStep
	for d, ok := range map[int64]bool{-2: false, -1: true, 0: true, 1: true, 2: false} {
		if got := totpMatch(secret, totpAt(secret, cur+d), now) != 0; got != ok {
			t.Errorf("skew %d: got %v want %v", d, got, ok)
		}
	}
	if totpMatch(secret, "12345", now) != 0 || totpMatch(secret, "", now) != 0 {
		t.Error("short code accepted")
	}
}

func TestTOTPLoginFlow(t *testing.T) {
	ts, s := newTestServer(t)
	s.Box, _ = secret.New(make([]byte, 32)) // as in production: the secret is sealed
	_, c := login(t, ts, "test-password-1")
	post := func(c *http.Client, path, body string, hdr map[string]string) (*http.Response, map[string]any) {
		req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		return resp, m
	}
	_, setup := post(c, "/api/totp/setup", "", nil)
	secret, err := b32.DecodeString(setup["secret"].(string))
	if err != nil || !strings.HasPrefix(setup["uri"].(string), "otpauth://totp/") {
		t.Fatalf("setup: %v %v", setup, err)
	}
	if resp, _ := post(c, "/api/totp/enable", `{"code":"000000"}`, nil); resp.StatusCode != 400 {
		t.Errorf("wrong code enabled 2FA: %d", resp.StatusCode)
	}
	code := totpAt(secret, time.Now().Unix()/totpStep)
	resp, en := post(c, "/api/totp/enable", `{"code":"`+code+`"}`, nil)
	rec, _ := en["recovery"].([]any)
	if resp.StatusCode != 200 || len(rec) != 10 || !s.totpEnabled() {
		t.Fatalf("enable: %d %v", resp.StatusCode, en)
	}
	for k, v := range s.St.MetaAll() {
		if strings.HasPrefix(k, "totp_") && (strings.Contains(v, setup["secret"].(string)) || strings.Contains(v, rec[0].(string))) {
			t.Errorf("%s holds a secret in the clear: %s", k, v)
		}
	}

	// new browser: password alone is no longer enough
	fresh := func() *http.Client { return &http.Client{Jar: &cookieJar{}} }
	resp, m := post(fresh(), "/api/login", `{"user":"admin","password":"test-password-1"}`, nil)
	if resp.StatusCode != 401 || m["totp_required"] != true {
		t.Fatalf("login without code: %d %v", resp.StatusCode, m)
	}
	// the code used to enable it is spent
	if resp, _ := post(fresh(), "/api/login", `{"user":"admin","password":"test-password-1","code":"`+code+`"}`, nil); resp.StatusCode != 401 {
		t.Errorf("replayed code accepted: %d", resp.StatusCode)
	}
	// a recovery code works exactly once
	rc := rec[0].(string)
	if resp, _ := post(fresh(), "/api/login", `{"user":"admin","password":"test-password-1","code":"`+rc+`"}`, nil); resp.StatusCode != 200 {
		t.Errorf("recovery code rejected: %d", resp.StatusCode)
	}
	if resp, _ := post(fresh(), "/api/login", `{"user":"admin","password":"test-password-1","code":"`+rc+`"}`, nil); resp.StatusCode != 401 {
		t.Errorf("recovery code reused: %d", resp.StatusCode)
	}
	// wrong password never reveals that 2FA is on
	if resp, m := post(fresh(), "/api/login", `{"user":"admin","password":"nope-nope-nope"}`, nil); resp.StatusCode != 401 || m["totp_required"] != nil {
		t.Errorf("wrong password: %d %v", resp.StatusCode, m)
	}

	// router-login actions need a code in the header
	if resp, m := post(c, "/api/devices/probe", `{}`, nil); resp.StatusCode != 403 || m["totp_required"] != true {
		t.Errorf("probe without code: %d %v", resp.StatusCode, m)
	}
	if resp, _ := post(c, "/api/devices/probe", `{}`, map[string]string{"X-TOTP-Code": "000000"}); resp.StatusCode != 403 {
		t.Errorf("probe with a wrong code: %d", resp.StatusCode)
	}
	if resp, _ := post(c, "/api/devices/probe", `{}`, map[string]string{"X-TOTP-Code": rec[1].(string)}); resp.StatusCode == 403 {
		t.Errorf("probe with a recovery code was blocked")
	}

	// disable: password + code
	if resp, _ := post(c, "/api/totp/disable", `{"password":"test-password-1","code":"000000"}`, nil); resp.StatusCode != 403 {
		t.Errorf("disable with wrong code: %d", resp.StatusCode)
	}
	if resp, _ := post(c, "/api/totp/disable", `{"password":"test-password-1","code":"`+rec[2].(string)+`"}`, nil); resp.StatusCode != 200 || s.totpEnabled() {
		t.Errorf("disable failed: %d", resp.StatusCode)
	}
	if resp, _ := post(fresh(), "/api/login", `{"user":"admin","password":"test-password-1"}`, nil); resp.StatusCode != 200 {
		t.Errorf("login after disable: %d", resp.StatusCode)
	}
}
