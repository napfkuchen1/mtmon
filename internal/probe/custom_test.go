package probe

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestValidateCustom(t *testing.T) {
	ok, err := ValidateCustom([]Custom{{Kind: "dns", Target: " Example.COM. "}, {Kind: "http", Target: "http://10.0.0.1/x", Insecure: true}})
	if err != nil || ok[0].Target != "example.com" || ok[1].Insecure {
		t.Fatalf("%+v %v", ok, err)
	}
	for _, bad := range [][]Custom{
		{{Kind: "dns", Target: "not a name"}}, {{Kind: "http", Target: "ftp://x"}}, {{Kind: "x", Target: "a.de"}},
		{{Kind: "dns", Target: "a.de"}, {Kind: "dns", Target: "a.de"}},
	} {
		if _, err := ValidateCustom(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	var many []Custom
	for i := 0; i < MaxCustom+1; i++ {
		many = append(many, Custom{Kind: "dns", Target: string(rune('a'+i)) + ".de"})
	}
	if _, err := ValidateCustom(many); err == nil {
		t.Error("more than the limit accepted")
	}
}

func TestReason(t *testing.T) {
	for err, want := range map[error]string{
		ErrBlocked: "blocked", &net.DNSError{IsNotFound: true}: "NXDOMAIN", context.DeadlineExceeded: "timeout",
		errors.New("status 503"): "HTTP 503", errors.New("tls: bad certificate"): "TLS error",
	} {
		if got := Reason(err); !strings.Contains(got, want) {
			t.Errorf("%v -> %q, want %q", err, got, want)
		}
	}
}

func TestFetchStatusAndFamily(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redir":
			http.Redirect(w, r, "/ok", http.StatusFound)
		case "/bad":
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	if err := fetch(ctx, srv.URL+"/redir", false, 4); err != nil {
		t.Errorf("3xx must count as answered: %v", err)
	}
	if err := fetch(ctx, srv.URL+"/bad", false, 4); Reason(err) != "HTTP 503" {
		t.Errorf("503: %v", err)
	}
	if err := fetch(ctx, srv.URL, false, 6); !errors.Is(err, ErrNoAddr) {
		t.Errorf("an IPv4 literal has no IPv6 address: %v", err)
	}
	tls := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer tls.Close()
	if err := fetch(ctx, tls.URL, false, 4); !strings.HasPrefix(Reason(err), "TLS error") {
		t.Errorf("self-signed certificate must fail: %v", err)
	}
	if err := fetch(ctx, tls.URL, true, 4); err != nil {
		t.Errorf("insecure target: %v", err)
	}
}

func TestOnceCustomTargets(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got := map[string][]bool{}
	var mu sync.Mutex
	p := &Prober{St: st, Log: slog.Default(), Targets: func() []string { return []string{"a:1"} }, DNS: func() []string { return []string{"x.de"} },
		Dial: func(context.Context, string) error { return nil },
		Lookup: func(_ context.Context, n string) error {
			if n == "blocked.de" {
				return ErrBlocked
			}
			return nil
		},
		Custom: func() []Custom {
			return []Custom{{Kind: "dns", Target: "blocked.de"}, {Kind: "http", Target: "https://v4only.de/"}}
		},
		Fetch: func(_ context.Context, _ string, _ bool, fam int) error {
			if fam == 6 {
				return ErrNoAddr
			}
			return nil
		},
		Report: func(target string, ok bool, _ string) {
			mu.Lock()
			defer mu.Unlock()
			got[target] = append(got[target], ok)
		}}
	p.Once(context.Background())
	if len(got) != 2 || got["dns:blocked.de"][0] || !got["http4:https://v4only.de/"][0] {
		t.Fatalf("reports: %v", got)
	}
	ts := st.WANTargets(time.Now().Add(-time.Hour).Unix())
	by := map[string]store.WANTarget{}
	for _, x := range ts {
		by[x.Target] = x
	}
	if !strings.Contains(by["dns:blocked.de"].Err, "blocked") {
		t.Errorf("blocked row: %+v", by["dns:blocked.de"])
	}
	if v6 := by["http6:https://v4only.de/"]; v6.Note != "no IPv6 address" || v6.Sent != 0 {
		t.Errorf("IPv6 row: %+v", v6)
	}
	if w := st.WANQualitySince(time.Now().Add(-time.Hour).Unix()); w.Sent != 1 {
		t.Errorf("own targets must stay out of the connection totals: %+v", w)
	}
}
