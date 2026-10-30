package probe

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestOnceStoresLossAndLatency(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := &Prober{St: st, Log: slog.Default(), Targets: func() []string { return []string{"a:1", "b:1"} },
		Dial: func(ctx context.Context, addr string) error {
			if addr == "b:1" {
				return errors.New("timeout")
			}
			time.Sleep(5 * time.Millisecond)
			return nil
		},
		Lookup: func(_ context.Context, name string) error {
			if name == "cloudflare.com" {
				return errors.New("servfail")
			}
			return nil
		}}
	p.Once(context.Background())
	p.Once(context.Background())
	w := st.WANQualitySince(time.Now().Add(-time.Hour).Unix())
	if w.Sent != 4 || w.Lost != 2 || w.LossPct != 50 || w.AvgMS < 4 || w.AvgMS > 500 {
		t.Errorf("window: %+v", w)
	}
	ts := st.WANTargets(time.Now().Add(-time.Hour).Unix())
	if len(ts) != 4 || ts[0].Target != "a:1" || ts[0].LossPct != 0 || ts[1].LossPct != 100 || ts[1].AvgMS != 0 {
		t.Errorf("targets: %+v", ts)
	}
	// DNS probes are listed per target but stay out of the totals above
	if ts[2].Target != "dns:cloudflare.com" || ts[2].LossPct != 100 || ts[3].Target != "dns:example.com" || ts[3].LossPct != 0 {
		t.Errorf("dns targets: %+v", ts[2:])
	}
	if s := st.WANSeries(time.Now().Add(-time.Hour).Unix(), 300); len(s) != 1 || s[0].LossPct != 50 {
		t.Errorf("series: %+v", s)
	}
}

func TestDisabledProberDoesNotProbe(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	defer st.Close()
	called := false
	p := &Prober{St: st, Enabled: func() bool { return false }, Dial: func(context.Context, string) error { called = true; return nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	p.Run(ctx)
	if called {
		t.Error("probed while disabled")
	}
}

func TestConfiguredDNSAndHTTPTargets(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := &Prober{St: st, Log: slog.Default(), Targets: func() []string { return []string{"a:1"} },
		Dial:   func(context.Context, string) error { return nil },
		Lookup: func(context.Context, string) error { return nil },
		DNS:    func() []string { return []string{"intranet.lan"} },
		HTTP:   func() []string { return []string{"https://ok.example/", "ftp://nope", "https://down.example/"} },
		Get: func(_ context.Context, u string) error {
			if u == "https://down.example/" {
				return errors.New("status 503")
			}
			return nil
		}}
	p.Once(context.Background())
	ts := st.WANTargets(time.Now().Add(-time.Hour).Unix())
	got := map[string]float64{}
	for _, x := range ts {
		got[x.Target] = x.LossPct
	}
	if len(got) != 4 || got["dns:intranet.lan"] != 0 || got["http:https://ok.example/"] != 0 || got["http:https://down.example/"] != 100 {
		t.Errorf("targets: %+v", got)
	}
	if w := st.WANQualitySince(time.Now().Add(-time.Hour).Unix()); w.Sent != 1 || w.Lost != 0 {
		t.Errorf("dns/http probes leaked into the line totals: %+v", w)
	}
}
