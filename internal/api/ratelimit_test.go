package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSourceIP(t *testing.T) {
	for _, c := range []struct{ peer, xff, real, want string }{
		{"203.0.113.9:1000", "198.51.100.1", "", "203.0.113.9"},         // untrusted peer: headers ignored
		{"127.0.0.1:1000", "198.51.100.7", "", "198.51.100.7"},          // proxy appends the client
		{"10.0.0.2:1000", "6.6.6.6, 198.51.100.7", "", "198.51.100.7"},  // spoofed leftmost entry ignored
		{"10.0.0.2:1000", "198.51.100.7, 10.0.0.3", "", "198.51.100.7"}, // second local hop skipped
		{"10.0.0.2:1000", "", "198.51.100.8", "198.51.100.8"},           // X-Real-IP fallback
		{"10.0.0.2:1000", "192.168.1.20", "", "192.168.1.20"},           // LAN client behind a LAN proxy
		{"10.0.0.2:1000", "garbage", "", "10.0.0.2"},                    // unparsable: peer
		{"[::1]:1000", "2001:db8::5", "", "2001:db8::5"},                // IPv6
		{"10.0.0.2:1000", "", "", "10.0.0.2"},                           // no headers
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.peer
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if c.real != "" {
			r.Header.Set("X-Real-IP", c.real)
		}
		if got := sourceIP(r); got != c.want {
			t.Errorf("peer %s xff %q real %q: got %s want %s", c.peer, c.xff, c.real, got, c.want)
		}
	}
}

// One noisy client behind the proxy, or a revoked token, must not lock out everybody else.
func TestRateLimitBucketsAreSeparate(t *testing.T) {
	ts, _ := newTestServer(t)
	post := func(xff, pw string) int {
		req, _ := http.NewRequest("POST", ts.URL+"/api/login", strings.NewReader(`{"user":"admin","password":"`+pw+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", xff)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for i := 0; i < failLimit+2; i++ {
		post("198.51.100.1", "nope")
	}
	if c := post("198.51.100.1", "nope"); c != 429 {
		t.Fatalf("the noisy client must be throttled: %d", c)
	}
	if c := post("198.51.100.2", "test-password-1"); c != 200 {
		t.Fatalf("another client behind the same proxy must still log in: %d", c)
	}
	// failing tokens from one address do not block interactive login from it
	for i := 0; i < failLimit+2; i++ {
		req, _ := http.NewRequest("GET", ts.URL+"/api/devices", nil)
		req.Header.Set("Authorization", "Bearer mtm_revoked")
		req.Header.Set("X-Forwarded-For", "198.51.100.3")
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
	}
	if c := post("198.51.100.3", "test-password-1"); c != 200 {
		t.Fatalf("bad tokens must not block login: %d", c)
	}
}
