// SPDX-License-Identifier: AGPL-3.0-only

package speedtest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/__down", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("bytes"))
		w.Header().Set("Content-Length", strconv.Itoa(n))
		w.Write(make([]byte, n))
	})
	mux.HandleFunc("/__up", func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
	})
	mux.HandleFunc("/100MB.bin", func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, 2<<20))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func opts() Opts { return Opts{MaxBytes: 4 << 20, MaxDur: 5 * time.Second, Streams: 2} }

func TestCloudflareMeasuresDownUpAndPing(t *testing.T) {
	ts := testServer(t)
	res := Cloudflare{Base: ts.URL}.Run(context.Background(), opts())
	if res.Err != "" {
		t.Fatal(res.Err)
	}
	if res.DownBps <= 0 || res.UpBps <= 0 || res.PingMS < 0 || res.Bytes < 4<<20 /* loopback connects can round to 0 ms on a coarse Windows clock */ {
		t.Errorf("%+v", res)
	}
}

func TestHetznerIsDownloadOnly(t *testing.T) {
	ts := testServer(t)
	res := Hetzner{Location: "fsn1", Base: ts.URL}.Run(context.Background(), opts())
	if res.Err != "" || res.DownBps <= 0 || res.UpBps != 0 {
		t.Errorf("%+v", res)
	}
}

func TestProviderErrorIsReportedNotZeroSpeed(t *testing.T) {
	ts := testServer(t)
	url := ts.URL
	ts.Close()
	res := Cloudflare{Base: url}.Run(context.Background(), opts())
	if res.Err == "" || res.DownBps != 0 {
		t.Errorf("a dead server must be an error: %+v", res)
	}
}

func TestInWindow(t *testing.T) {
	for _, c := range []struct {
		from, to string
		now      int
		want     bool
	}{
		{"", "", 600, true},
		{"01:00", "06:00", 3*60 + 5, true},
		{"01:00", "06:00", 7 * 60, false},
		{"01:00", "06:00", 6 * 60, false}, // end is exclusive
		{"22:00", "05:00", 23 * 60, true}, // crosses midnight
		{"22:00", "05:00", 2 * 60, true},
		{"22:00", "05:00", 12 * 60, false},
	} {
		if got := InWindow(c.from, c.to, c.now); got != c.want {
			t.Errorf("%s-%s at %d: %v", c.from, c.to, c.now, got)
		}
	}
}

func TestSettingsValidate(t *testing.T) {
	ok := DefaultSettings()
	if _, err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]func(s *Settings){
		"provider":     func(s *Settings) { s.Providers = []string{"speedtest.example"} },
		"interval":     func(s *Settings) { s.IntervalMin = 5 },
		"half window":  func(s *Settings) { s.WindowFrom = "01:00" },
		"bad time":     func(s *Settings) { s.WindowFrom, s.WindowTo = "25:00", "03:00" },
		"size":         func(s *Settings) { s.MaxMB = 100000 },
		"family":       func(s *Settings) { s.Families = []string{"5"} },
		"hetzner":      func(s *Settings) { s.Hetzner = "evil.example" },
		"alert":        func(s *Settings) { s.AlertBelow = 0 },
		"negative day": func(s *Settings) { s.BudgetDayMB = -1 },
	} {
		s := DefaultSettings()
		f(&s)
		if _, err := s.Validate(); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}

type fakeProvider struct {
	name string
	res  Result
	mu   sync.Mutex
	n    int
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Run(context.Context, Opts) Result {
	f.mu.Lock()
	f.n++
	f.mu.Unlock()
	return f.res
}

func newRunner(t *testing.T, now *time.Time, p *fakeProvider) *Runner {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Runner{St: st, Now: func() time.Time { return *now }, Providers: func(Settings) []Provider { return []Provider{p} }}
}

func save(t *testing.T, r *Runner, mod func(s *Settings)) Settings {
	t.Helper()
	s := DefaultSettings()
	s.Enabled, s.JitterMin = true, 0
	mod(&s)
	v, err := SaveSettings(r.St, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func waitIdle(r *Runner) {
	for i := 0; i < 500; i++ {
		if !r.Status().Running {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTickRunsWhenDueAndStoresResult(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.Local)
	p := &fakeProvider{name: "fake", res: Result{Server: "x", DownBps: 90e6, UpBps: 40e6, PingMS: 12, Bytes: 5 << 20}}
	r := newRunner(t, &now, p)
	save(t, r, func(s *Settings) { s.IntervalMin = 60 })
	r.Tick(context.Background()) // schedules (fresh install waits a few minutes)
	if p.n != 0 || r.Status().NextTS == 0 {
		t.Fatalf("first tick must only schedule: n=%d %+v", p.n, r.Status())
	}
	now = now.Add(3*time.Hour + time.Minute)
	r.Tick(context.Background())
	if p.n != 1 {
		t.Fatalf("due: %d runs", p.n)
	}
	list, _ := r.St.Speedtests(0, 10)
	if len(list) != 1 || list[0].DownBps != 90e6 || list[0].Provider != "fake" {
		t.Fatalf("stored: %+v", list)
	}
	r.Tick(context.Background()) // next one is an interval away
	if p.n != 1 {
		t.Errorf("not due again: %d", p.n)
	}
}

func TestTickRespectsWindowBudgetAndBusy(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.Local)
	p := &fakeProvider{name: "fake", res: Result{DownBps: 100e6, UpBps: 50e6, Bytes: 10 << 20}}
	r := newRunner(t, &now, p)
	// a previous good result so the busy guard has a reference
	r.St.AddSpeedtest(store.Speedtest{TS: now.Add(-24 * time.Hour).Unix(), Provider: "fake", DownBps: 100e6, UpBps: 50e6})
	due := func() { now = now.Add(48 * time.Hour); r.mu.Lock(); r.next = now.Add(-time.Minute); r.mu.Unlock() }

	save(t, r, func(s *Settings) { s.IntervalMin = 60; s.WindowFrom, s.WindowTo = "01:00", "06:00" })
	now = time.Date(2026, 10, 27, 12, 0, 0, 0, time.Local) // noon: outside
	r.mu.Lock()
	r.next = now.Add(-time.Minute)
	r.mu.Unlock()
	r.Tick(context.Background())
	if p.n != 0 || r.Status().Skipped != SkipWindow {
		t.Fatalf("window: n=%d %+v", p.n, r.Status())
	}

	save(t, r, func(s *Settings) { s.IntervalMin = 60; s.BudgetDayMB = 1 }) // 1 MB per day: the worst case does not fit
	due()
	r.Tick(context.Background())
	if p.n != 0 || r.Status().Skipped != SkipBudget {
		t.Fatalf("budget: n=%d %+v", p.n, r.Status())
	}

	save(t, r, func(s *Settings) { s.IntervalMin = 60; s.SkipBusyPct = 50 })
	r.Busy = func() (float64, float64) { return 80e6, 1e6 } // 80 % of the last result in use
	due()
	r.Tick(context.Background())
	if p.n != 0 || r.Status().Skipped != SkipBusy {
		t.Fatalf("busy: n=%d %+v", p.n, r.Status())
	}
	r.Busy = func() (float64, float64) { return 1e6, 1e6 }
	due()
	r.Tick(context.Background())
	if p.n != 1 || r.Status().Skipped != "" {
		t.Fatalf("quiet line must run: n=%d %+v", p.n, r.Status())
	}
}

func TestFailedTestIsStoredAsErrorNotZeroSpeed(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.Local)
	p := &fakeProvider{name: "fake", res: Result{Err: "dial tcp: lookup failed", DownBps: 5e6}}
	r := newRunner(t, &now, p)
	s := save(t, r, func(s *Settings) { s.IntervalMin = 60 })
	r.round(context.Background(), s)
	list, _ := r.St.Speedtests(0, 10)
	if len(list) != 1 || list[0].Err == "" || list[0].DownBps != 0 {
		t.Fatalf("%+v", list)
	}
	if sum := r.St.SpeedtestSummary(0); sum.Failed != 1 || sum.Down.N != 0 {
		t.Errorf("a failure must not enter the statistics: %+v", sum)
	}
}

func TestContractAlertNeedsConsecutiveLowTestsAndResolves(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.Local)
	p := &fakeProvider{name: "fake", res: Result{DownBps: 40e6, UpBps: 20e6}}
	r := newRunner(t, &now, p)
	var raised []string
	var resolved int
	r.Raise = func(kind, subject, sev, msg string) { raised = append(raised, kind+"|"+subject) }
	r.Resolve = func(kind, subject, note string) { resolved++ }
	s := save(t, r, func(s *Settings) {
		s.IntervalMin = 60
		s.ContractDown = 100
		s.ContractUp = 50
		s.AlertBelow = 70
		s.AlertCount = 3
	})
	for i := 0; i < 2; i++ {
		now = now.Add(time.Hour)
		r.round(context.Background(), s)
	}
	if len(raised) != 0 {
		t.Fatalf("two low tests are not enough: %v", raised)
	}
	// a failed test in between is skipped, it neither raises nor breaks the series
	p.res = Result{Err: "timeout"}
	now = now.Add(time.Hour)
	r.round(context.Background(), s)
	p.res = Result{DownBps: 40e6, UpBps: 20e6}
	now = now.Add(time.Hour)
	r.round(context.Background(), s)
	if len(raised) != 1 || raised[0] != "speed_low|fake" {
		t.Fatalf("raised: %v", raised)
	}
	p.res = Result{DownBps: 95e6, UpBps: 48e6}
	now = now.Add(time.Hour)
	r.round(context.Background(), s)
	if resolved != 1 {
		t.Errorf("resolved %d", resolved)
	}
}

func TestRunNowRefusesWhileRunningAndOnBudget(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.Local)
	block := make(chan struct{})
	p := &blockingProvider{block: block}
	st, _ := store.Open(t.TempDir())
	defer st.Close()
	r := &Runner{St: st, Now: func() time.Time { return now }, Providers: func(Settings) []Provider { return []Provider{p} }}
	save(t, r, func(s *Settings) { s.IntervalMin = 60 })
	if err := r.RunNow(); err != nil {
		t.Fatal(err)
	}
	if err := r.RunNow(); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("second click: %v", err)
	}
	close(block)
	waitIdle(r)
	save(t, r, func(s *Settings) { s.IntervalMin = 60; s.BudgetDayMB = 1 })
	if err := r.RunNow(); err == nil {
		t.Error("a used-up budget must refuse a manual run")
	}
}

type blockingProvider struct{ block chan struct{} }

func (b *blockingProvider) Name() string { return "block" }
func (b *blockingProvider) Run(context.Context, Opts) Result {
	<-b.block
	return Result{DownBps: 1e6}
}

func TestAnchorSlot(t *testing.T) {
	loc := time.Local
	at := func(h, m int) time.Time { return time.Date(2026, 10, 20, h, m, 0, 0, loc) }
	start := 1*60 + 30
	for _, c := range []struct {
		name  string
		after time.Time
		iv    time.Duration
		want  time.Time
	}{
		{"before first slot", at(0, 10), 3 * time.Hour, at(1, 30)},
		{"after a slot", at(1, 31), 3 * time.Hour, at(4, 30)},
		{"on a slot moves on", at(4, 30), 3 * time.Hour, at(7, 30)},
		{"late evening wraps to the grid", at(23, 0), 3 * time.Hour, at(22, 30).Add(3 * time.Hour)},
	} {
		if got := anchorSlot(c.after, start, c.iv); !got.Equal(c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestShortErr(t *testing.T) {
	if got := shortErr(`{"type":"log","level":"error","message":"Configuration - Couldn't resolve host name"}`); got != "Configuration - Couldn't resolve host name" {
		t.Errorf("json: %q", got)
	}
	if got := shortErr("  plain  "); got != "plain" {
		t.Errorf("plain: %q", got)
	}
}

func TestOoklaMissingIsSkipped(t *testing.T) {
	now := time.Now()
	r := newRunner(t, &now, &fakeProvider{})
	r.Providers = nil
	r.DataDir = t.TempDir()
	t.Setenv("PATH", t.TempDir())
	save(t, r, func(s *Settings) { s.Providers, s.OoklaOK = []string{"ookla"}, true })
	if !r.Status().OoklaMissing {
		t.Fatal("status must flag the missing CLI")
	}
	if got := r.providers(LoadSettings(r.St)); len(got) != 0 {
		t.Fatalf("missing Ookla must be left out, got %d", len(got))
	}
	if err := r.RunNow(); err == nil {
		t.Fatal("RunNow must refuse when Ookla is the only provider and missing")
	}
	if items, _ := r.St.Speedtests(0, 10); len(items) != 0 {
		t.Fatalf("no failed rows expected, got %d", len(items))
	}
}
