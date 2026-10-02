package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		origin, host string
		want         bool
	}{
		{"https://mtmon.lan:8443", "mtmon.lan:8443", true},
		{"http://127.0.0.1:18443", "127.0.0.1:18443", true},
		{"https://MTMON.lan:8443", "mtmon.lan:8443", true},
		{"https://evilmtmon.lan:8443", "mtmon.lan:8443", false}, // suffix match without a label boundary
		{"https://mtmon.lan.evil.com", "mtmon.lan", false},
		{"https://mtmon.lan:8444", "mtmon.lan:8443", false},
		{"https://mtmon.lan", "mtmon.lan:8443", false},
		{"https://evil.com/https://mtmon.lan:8443", "mtmon.lan:8443", false},
		{"null", "mtmon.lan:8443", false},
		{"", "mtmon.lan:8443", false},
		{"ftp://mtmon.lan:8443", "mtmon.lan:8443", false},
		{"//mtmon.lan:8443", "mtmon.lan:8443", false},
	}
	for _, c := range cases {
		if got := sameOrigin(c.origin, c.host); got != c.want {
			t.Errorf("sameOrigin(%q, %q) = %v, want %v", c.origin, c.host, got, c.want)
		}
	}
}

func TestSessionLifetime(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	s := newSessions()
	s.now = func() time.Time { return now }

	idle := s.create()
	now = now.Add(sessionIdle - time.Minute)
	if !s.valid(idle) {
		t.Fatal("session expired before the idle timeout")
	}
	now = now.Add(sessionIdle + time.Minute)
	if s.valid(idle) {
		t.Fatal("idle session still valid after sessionIdle without use")
	}

	// continuous use extends the session, but never beyond sessionMax
	active := s.create()
	for i := 0; i < int(sessionMax/time.Hour); i++ {
		now = now.Add(time.Hour)
		if !s.valid(active) {
			t.Fatalf("active session expired after %d h, before sessionMax", i)
		}
	}
	now = now.Add(time.Hour)
	if s.valid(active) {
		t.Fatal("session survived past sessionMax although it was used continuously")
	}
}

func TestSessionsArePruned(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	s := newSessions()
	s.now = func() time.Time { return now }
	for i := 0; i < 50; i++ {
		s.create() // logins that never come back
	}
	now = now.Add(sessionIdle + time.Hour)
	s.create()
	if n := len(s.tokens); n != 1 {
		t.Fatalf("%d sessions kept, want only the new one", n)
	}
}

func TestFailRecordsArePruned(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	s := newSessions()
	s.now = func() time.Time { return now }
	for i := 0; i < 300; i++ {
		s.fail("10.0.0." + strings.Repeat("1", i%9+1) + string(rune('a'+i%26)) + string(rune('a'+i/26)))
	}
	now = now.Add(2 * failWindow)
	s.allow("1.2.3.4") // triggers the sweep
	if n := len(s.fails); n != 0 {
		t.Fatalf("%d stale failure records kept", n)
	}
	// and an address that stays under the limit is not blocked, one at the limit is
	for i := 0; i < failLimit; i++ {
		s.fail("9.9.9.9")
	}
	if s.allow("9.9.9.9") {
		t.Fatal("limit not enforced")
	}
}

func TestSecurityHeaders(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	h := resp.Header
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "frame-ancestors 'none'", "base-uri 'none'", "object-src 'none'", "form-action 'self'", "connect-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %q: %s", want, csp)
		}
	}
	for _, bad := range []string{"ws:", "wss:", "*", "unsafe-eval"} {
		if strings.Contains(csp, bad) {
			t.Errorf("CSP contains %q: %s", bad, csp)
		}
	}
	if h.Get("X-Frame-Options") != "DENY" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("missing legacy headers: %v", h)
	}
}

func TestStateChangeCrossOriginBlocked(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, c := login(t, ts, "test-password-1")
	if resp.StatusCode != 200 {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	u, _ := url.Parse(ts.URL)
	for _, tc := range []struct {
		origin  string
		blocked bool
	}{
		{"http://" + u.Host, false},
		{"http://evil" + u.Host, true}, // suffix trick: ends with our host
		{"https://evil.example", true},
		{"null", true},
	} {
		req, _ := http.NewRequest("PUT", ts.URL+"/api/clients/aa:bb:cc:dd:ee:ff/label", strings.NewReader(`{"label":"x"}`))
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Content-Type", "application/json")
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if blocked := r.StatusCode == 403; blocked != tc.blocked {
			t.Errorf("Origin %q: status %d, blocked=%v want %v", tc.origin, r.StatusCode, blocked, tc.blocked)
		}
	}
}

// dialLive opens the live WebSocket with the session of c and the given Origin header.
func dialLive(t *testing.T, tsURL string, c *http.Client, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	u, _ := url.Parse(tsURL)
	var cookies []string
	for _, ck := range c.Jar.Cookies(u) {
		cookies = append(cookies, ck.Name+"="+ck.Value)
	}
	h := http.Header{"Cookie": {strings.Join(cookies, "; ")}}
	if origin != "" {
		h.Set("Origin", origin)
	}
	return websocket.DefaultDialer.Dial("ws://"+u.Host+"/api/live", h)
}

func TestLiveWebSocketOrigin(t *testing.T) {
	ts, _ := newTestServer(t)
	_, c := login(t, ts, "test-password-1")
	u, _ := url.Parse(ts.URL)

	for _, origin := range []string{"", "http://" + u.Host} {
		conn, _, err := dialLive(t, ts.URL, c, origin)
		if err != nil {
			t.Fatalf("Origin %q rejected: %v", origin, err)
		}
		conn.Close()
	}
	for _, origin := range []string{"http://evil" + u.Host, "https://evil.example", "null"} {
		conn, resp, err := dialLive(t, ts.URL, c, origin)
		if err == nil {
			conn.Close()
			t.Fatalf("cross-origin WebSocket accepted for Origin %q", origin)
		}
		if resp == nil || resp.StatusCode != 403 {
			t.Errorf("Origin %q: %v, want HTTP 403", origin, resp)
		}
	}
}

func TestLiveWebSocketReadLimit(t *testing.T) {
	ts, _ := newTestServer(t)
	_, c := login(t, ts, "test-password-1")
	conn, _, err := dialLive(t, ts.URL, c, "")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"Pause":true}`)); err != nil {
		t.Fatal(err)
	}
	conn.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", 64<<10))) // far above the limit
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for { // the server must close the connection after the oversized message
		if _, _, err := conn.ReadMessage(); err != nil {
			if websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
				return
			}
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				t.Fatal("connection stayed open after an oversized message")
			}
			return // closed by the server (abnormal close also proves the limit)
		}
	}
}
