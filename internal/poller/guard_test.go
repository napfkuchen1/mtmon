package poller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
)

func devFor(t *testing.T, srv *httptest.Server, scheme string) config.Device {
	t.Helper()
	h, p, _ := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://"))
	port, _ := strconv.Atoi(p)
	return config.Device{Name: "t", Addr: h, Port: port, Scheme: scheme, User: "u", Pass: "p", Insecure: scheme == "https"}
}

func jsonServer(hits *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"uptime":"1d"}`))
	}))
}

func denyAll(netip.Addr) error  { return errors.New("denied by test guard") }
func allowAll(netip.Addr) error { return nil }

func TestGuardedClientRefusesBeforeConnecting(t *testing.T) {
	var hits atomic.Int32
	srv := jsonServer(&hits)
	defer srv.Close()
	_, err := NewGuardedClient(devFor(t, srv, "http"), denyAll).Get(context.Background(), "/system/resource")
	if err == nil || !strings.Contains(err.Error(), "denied by test guard") {
		t.Fatalf("error = %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("the request reached the server although the guard refused the address")
	}
}

func TestGuardedClientAllowsPermittedAddress(t *testing.T) {
	var hits atomic.Int32
	srv := jsonServer(&hits)
	defer srv.Close()
	rows, err := NewGuardedClient(devFor(t, srv, "http"), allowAll).Get(context.Background(), "/system/resource")
	if err != nil || len(rows) != 1 || rows[0]["uptime"] != "1d" || hits.Load() != 1 {
		t.Fatalf("rows=%v err=%v hits=%d", rows, err, hits.Load())
	}
}

// DNS rebinding: the name resolves to an allowed address at check time and to a forbidden one afterwards. With the
// guard inside the dialer there is exactly one lookup per connection, and the connection goes to the address that
// was checked.
func TestGuardedDialResolvesOnceAndDialsTheCheckedAddress(t *testing.T) {
	var hits atomic.Int32
	srv := jsonServer(&hits)
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))

	var lookups atomic.Int32
	old := lookupNetIP
	defer func() { lookupNetIP = old }()
	lookupNetIP = func(ctx context.Context, host string) ([]netip.Addr, error) {
		if lookups.Add(1) == 1 {
			return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil // what a rebinding DNS server answers next
	}
	onlyLoopback := func(a netip.Addr) error {
		if !a.IsLoopback() {
			return errors.New("not loopback: " + a.String())
		}
		return nil
	}
	p, _ := strconv.Atoi(port)
	c := NewGuardedClient(config.Device{Addr: "router.example", Port: p, Scheme: "http", User: "u", Pass: "p"}, onlyLoopback)
	if _, err := c.Get(context.Background(), "/system/resource"); err != nil {
		t.Fatalf("first request: %v", err)
	}
	if lookups.Load() != 1 {
		t.Fatalf("%d lookups for one connection, want 1", lookups.Load())
	}
	if hits.Load() != 1 {
		t.Fatal("request did not reach the checked address")
	}
	// the next connection resolves again, now gets the forbidden address, and is refused before any traffic
	c.hc.CloseIdleConnections()
	c.hc.Transport.(*http.Transport).CloseIdleConnections()
	c2 := NewGuardedClient(config.Device{Addr: "router.example", Port: p, Scheme: "http", User: "u", Pass: "p"}, onlyLoopback)
	if _, err := c2.Get(context.Background(), "/system/resource"); err == nil || !strings.Contains(err.Error(), "not loopback") {
		t.Fatalf("rebound address accepted: %v", err)
	}
}

func TestGuardedDialRequiresEveryResolvedAddressToPass(t *testing.T) {
	old := lookupNetIP
	defer func() { lookupNetIP = old }()
	lookupNetIP = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("169.254.169.254")}, nil
	}
	noMeta := func(a netip.Addr) error {
		if a == netip.MustParseAddr("169.254.169.254") {
			return errors.New("metadata address")
		}
		return nil
	}
	_, err := guardedDial(noMeta)(context.Background(), "tcp", "router.example:443")
	if err == nil || !strings.Contains(err.Error(), "metadata") {
		t.Fatalf("a name with one forbidden address among its answers was accepted: %v", err)
	}
	_, err = guardedDial(allowAll)(context.Background(), "tcp", "router.example")
	if err == nil {
		t.Fatal("address without port accepted")
	}
}

func TestGuardedDialMapsIPv4InIPv6(t *testing.T) {
	var seen netip.Addr
	g := func(a netip.Addr) error { seen = a; return errors.New("stop") }
	guardedDial(g)(context.Background(), "tcp", "[::ffff:169.254.169.254]:80")
	if !seen.Is4() {
		t.Fatalf("guard saw %v, want the unmapped IPv4 address (a mapped form must not bypass IPv4 rules)", seen)
	}
}

func TestRedirectToOtherHostIsRefused(t *testing.T) {
	var other atomic.Int32
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { other.Add(1); w.Write([]byte(`{}`)) }))
	defer b.Close()
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, b.URL+"/rest/system/resource", http.StatusFound)
	}))
	defer a.Close()
	for name, c := range map[string]*Client{"plain": NewClient(devFor(t, a, "http")), "guarded": NewGuardedClient(devFor(t, a, "http"), allowAll)} {
		_, err := c.Get(context.Background(), "/system/resource")
		if err == nil || !strings.Contains(err.Error(), "refusing redirect") {
			t.Errorf("%s: error = %v", name, err)
		}
	}
	if other.Load() != 0 {
		t.Fatal("redirect to another host was followed")
	}
}

func TestRedirectDowngradeToHTTPIsRefused(t *testing.T) {
	var plain atomic.Int32
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { plain.Add(1); w.Write([]byte(`{}`)) }))
	defer b.Close()
	a := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, b.URL+"/rest/x", http.StatusFound)
	}))
	defer a.Close()
	if _, err := NewClient(devFor(t, a, "https")).Get(context.Background(), "/x"); err == nil {
		t.Fatal("https -> http redirect followed")
	}
	if plain.Load() != 0 {
		t.Fatal("plain-http target was contacted")
	}
}

func TestRedirectWithinTheSameHostStillWorks(t *testing.T) {
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/system/resource":
			http.Redirect(w, r, "/rest/system/resource/", http.StatusMovedPermanently)
		case "/rest/system/resource/":
			w.Write([]byte(`{"uptime":"2d"}`))
		}
	}))
	defer a.Close()
	rows, err := NewClient(devFor(t, a, "http")).Get(context.Background(), "/system/resource")
	if err != nil || len(rows) != 1 || rows[0]["uptime"] != "2d" {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
}

func TestRedirectLoopStops(t *testing.T) {
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path, http.StatusFound)
	}))
	defer a.Close()
	done := make(chan error, 1)
	go func() { _, err := NewClient(devFor(t, a, "http")).Get(context.Background(), "/loop"); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("redirect loop returned success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("redirect loop does not stop")
	}
}

func TestFingerprintGuarded(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "https://")
	sum := sha256.Sum256(srv.Certificate().Raw)
	want := hex.EncodeToString(sum[:])
	if got, err := FingerprintGuarded(addr, allowAll); err != nil || got != want {
		t.Fatalf("guarded fingerprint = %q, %v; want %q", got, err, want)
	}
	if got, err := Fingerprint(addr); err != nil || got != want {
		t.Fatalf("plain fingerprint = %q, %v", got, err)
	}
	if _, err := FingerprintGuarded(addr, denyAll); err == nil || !strings.Contains(err.Error(), "denied by test guard") {
		t.Fatalf("fingerprint of a refused address: %v", err)
	}
}
