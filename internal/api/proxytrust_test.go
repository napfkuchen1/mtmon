// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
)

// withProxies sets the operator's trusted proxies for one test and resets them afterwards.
func withProxies(t *testing.T, list string) {
	t.Helper()
	p, err := parseTrustedProxies(list)
	if err != nil {
		t.Fatal(err)
	}
	setTrustedProxies(p)
	t.Cleanup(func() { setTrustedProxies(nil) })
}

func TestParseTrustedProxies(t *testing.T) {
	p, err := parseTrustedProxies(" 10.0.0.5, 192.168.10.0/24\n2001:db8::/64;fd00::1 ")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, x := range p {
		got = append(got, x.String())
	}
	if want := "10.0.0.5/32 192.168.10.0/24 2001:db8::/64 fd00::1/128"; strings.Join(got, " ") != want {
		t.Fatalf("got %v want %s", got, want)
	}
	if p, err := parseTrustedProxies("  "); err != nil || len(p) != 0 {
		t.Errorf("empty list: %v %v", p, err)
	}
	if p, _ := parseTrustedProxies("192.168.10.7/24"); p[0].String() != "192.168.10.0/24" {
		t.Errorf("host bits must be masked: %v", p)
	}
	for _, bad := range []string{"0.0.0.0/0", "::/0", "nginx.lan", "10.0.0.0/33", "1.2.3"} {
		if _, err := parseTrustedProxies(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

// GHSA "rate limit bypass": a LAN client is not a proxy just because its address is private.
func TestForwardedHeadersFromUntrustedLANPeerIgnored(t *testing.T) {
	setTrustedProxies(nil)
	mk := func(peer, xff string) *http.Request {
		r := httptest.NewRequest("POST", "/api/login", nil)
		r.RemoteAddr = peer
		r.Header.Set("X-Forwarded-For", xff)
		r.Header.Set("X-Forwarded-Proto", "https")
		return r
	}
	for _, peer := range []string{"192.168.1.50:4000", "10.0.0.2:4000", "172.16.5.5:4000", "[fd00::5]:4000"} {
		r := mk(peer, "203.0.113.77")
		if proxyTrusted(r) {
			t.Errorf("%s: a private peer must not be trusted by default", peer)
		}
		if got, want := sourceIP(r), clientIP(r); got != want {
			t.Errorf("%s: spoofed X-Forwarded-For used: %s", peer, got)
		}
	}
	if !proxyTrusted(mk("127.0.0.1:4000", "x")) || !proxyTrusted(mk("[::1]:4000", "x")) {
		t.Error("loopback stays trusted (proxy in the same container)")
	}
	withProxies(t, "192.168.1.50")
	if got := sourceIP(mk("192.168.1.50:4000", "203.0.113.77")); got != "203.0.113.77" {
		t.Errorf("configured proxy: got %s", got)
	}
	if got := sourceIP(mk("192.168.1.51:4000", "203.0.113.77")); got != "192.168.1.51" {
		t.Errorf("neighbour of the configured proxy: got %s", got)
	}
}

func TestLoginRateLimitWithRotatingForwardedFor(t *testing.T) {
	// The test server listens on loopback, which is trusted. A LAN peer is simulated by calling the handler directly.
	_, s := newTestServer(t)
	h := New(s)
	codes := map[int]int{}
	for i := 0; i < failLimit+5; i++ {
		r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"user":"admin","password":"nope"}`))
		r.RemoteAddr = "192.168.1.50:4000"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-For", "203.0.113."+string(rune('1'+i%9)))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		codes[w.Code]++
	}
	if codes[401] != failLimit || codes[429] != 5 {
		t.Fatalf("rotating X-Forwarded-For from a LAN peer must not escape the limit: %v", codes)
	}
}

// GHSA "check-then-act": parallel failures must not all pass before the first one is counted.
func TestLoginRateLimitParallel(t *testing.T) {
	_, s := newTestServer(t)
	h := New(s)
	const n = 30
	var mu sync.Mutex
	codes := map[int]int{}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"user":"admin","password":"nope"}`))
			r.RemoteAddr = "192.168.1.60:4000"
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			mu.Lock()
			codes[w.Code]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if codes[401] > failLimit || codes[401]+codes[429] != n {
		t.Fatalf("at most %d attempts may reach the password check: %v", failLimit, codes)
	}
	// a correct login from another address still works, and successful logins are not counted
	for i := 0; i < failLimit+2; i++ {
		r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"user":"admin","password":"test-password-1"}`))
		r.RemoteAddr = "192.168.1.61:4000"
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("successful login #%d: %d", i+1, w.Code)
		}
	}
}

func TestReserveRelease(t *testing.T) {
	s := newSessions()
	for i := 0; i < failLimit; i++ {
		if !s.reserve("k") {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	if s.reserve("k") {
		t.Fatal("window full: must refuse")
	}
	s.release("k")
	if !s.reserve("k") {
		t.Fatal("a released slot must be usable again")
	}
	for i := 0; i < failLimit+3; i++ {
		s.release("k")
	}
	if _, ok := s.fails["k"]; ok {
		t.Fatal("releasing everything must drop the entry")
	}
}

// GHSA "argon2 memory": password checks are capped; a waiting request gives up instead of allocating.
func TestPasswordCheckConcurrencyCap(t *testing.T) {
	hash, _ := HashPassword("pw-123456789")
	r := httptest.NewRequest("POST", "/", nil)
	if ok, err := checkPasswordLimited(r, "pw-123456789", hash); !ok || err != nil {
		t.Fatalf("free slot: %v %v", ok, err)
	}
	for i := 0; i < cap(hashSem); i++ { // occupy every slot
		hashSem <- struct{}{}
	}
	t.Cleanup(func() {
		for i := 0; i < cap(hashSem); i++ {
			<-hashSem
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ok, err := checkPasswordLimited(r.WithContext(ctx), "pw-123456789", hash); ok || !errors.Is(err, errBusy) {
		t.Fatalf("all slots taken: got %v %v, want errBusy", ok, err)
	}
	if cap(hashSem) != hashSlots {
		t.Fatalf("semaphore size %d, want %d", cap(hashSem), hashSlots)
	}
}

func TestLoginBusyIs503AndNotCounted(t *testing.T) {
	_, s := newTestServer(t)
	h := New(s)
	for i := 0; i < cap(hashSem); i++ {
		hashSem <- struct{}{}
	}
	release := func() {
		for i := 0; i < cap(hashSem); i++ {
			<-hashSem
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the client gave up while waiting
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"user":"admin","password":"nope"}`)).WithContext(ctx)
	r.RemoteAddr = "192.168.1.70:4000"
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	release()
	if w.Code != 503 {
		t.Fatalf("busy: got %d", w.Code)
	}
	if n := len(s.sess.fails["login|192.168.1.70"]); n != 0 {
		t.Fatalf("a busy answer must not count as a failed attempt: %d", n)
	}
}

func TestAccessSaveTrustedProxies(t *testing.T) {
	ts, s := newTestServer(t)
	_, c := login(t, ts, "test-password-1")
	put := func(body string) (int, map[string]any) {
		req, _ := http.NewRequest("PUT", ts.URL+"/api/access", strings.NewReader(body))
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m
	}
	t.Cleanup(func() { setTrustedProxies(nil) })
	if code, _ := put(`{"trusted_proxies":"0.0.0.0/0"}`); code != 400 {
		t.Errorf("trust everything: %d", code)
	}
	if code, m := put(`{"trusted_proxies":"192.168.1.5, 10.9.0.0/16"}`); code != 200 || m["trusted_proxies"] != "192.168.1.5/32, 10.9.0.0/16" {
		t.Fatalf("save: %d %v", code, m)
	}
	if !isTrustedProxy(netip.MustParseAddr("10.9.3.4")) || isTrustedProxy(netip.MustParseAddr("10.8.0.1")) {
		t.Error("saved list must take effect at once")
	}
	if code, m := put(`{"public_url":""}`); code != 200 || m["trusted_proxies"] != "192.168.1.5/32, 10.9.0.0/16" {
		t.Errorf("a save without the field keeps the list: %d %v", code, m)
	}
	// restart: the list comes back from the database
	setTrustedProxies(nil)
	s.loadTrustedProxies()
	if !isTrustedProxy(netip.MustParseAddr("192.168.1.5")) {
		t.Error("stored list must be loaded at start")
	}
	if code, _ := put(`{"trusted_proxies":""}`); code != 200 || isTrustedProxy(netip.MustParseAddr("192.168.1.5")) {
		t.Errorf("empty list: only loopback stays trusted (%d)", code)
	}
}
