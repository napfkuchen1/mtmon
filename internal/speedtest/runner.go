package speedtest

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// Skip reasons shown in the UI.
const (
	SkipOff    = "off"
	SkipPaused = "paused"
	SkipWindow = "outside the time window"
	SkipBudget = "data budget reached"
	SkipBusy   = "the line is busy"
)

// Status is what the UI shows next to the results.
type Status struct {
	Running  bool   `json:"running"`
	NextTS   int64  `json:"next_ts"` // 0 = nothing scheduled
	Skipped  string `json:"skipped"` // why the last due run did not start
	SkippedT int64  `json:"skipped_ts"`
}

// Runner schedules and runs the tests.
type Runner struct {
	St  *store.Store
	Log *slog.Logger
	// DataDir is where an installed Ookla CLI is looked for (<DataDir>/ookla/speedtest).
	DataDir string
	// Busy returns the current WAN traffic (down, up in bit/s) so a test does not measure the family's streaming.
	Busy func() (down, up float64)
	// WAN names the WAN interface(s) that are up (stored with each result); optional.
	WAN func() string
	// Raise / Resolve connect the contract alert to the alert engine; optional.
	Raise   func(kind, subject, severity, msg string)
	Resolve func(kind, subject, note string)
	// Now and Providers are replaced in tests.
	Now       func() time.Time
	Providers func(Settings) []Provider

	mu      sync.Mutex
	base    context.Context // outlives a request: manual runs go on after the HTTP call returned
	running bool
	next    time.Time
	skipped string
	skipT   time.Time
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) providers(s Settings) []Provider {
	if r.Providers != nil {
		return r.Providers(s)
	}
	var out []Provider
	for _, n := range s.Providers {
		switch n {
		case "cloudflare":
			out = append(out, Cloudflare{})
		case "hetzner":
			out = append(out, Hetzner{Location: s.Hetzner})
		case "ookla":
			out = append(out, Ookla{DataDir: r.DataDir, ServerID: s.OoklaID})
		}
	}
	return out
}

// Status reports the current state.
func (r *Runner) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := Status{Running: r.running, Skipped: r.skipped}
	if !r.next.IsZero() {
		st.NextTS = r.next.Unix()
	}
	if !r.skipT.IsZero() {
		st.SkippedT = r.skipT.Unix()
	}
	return st
}

// schedule picks the next run: the interval after the newest test of mtmon, plus a random jitter. A fresh install
// waits a few minutes first so the start-up work is done.
func (r *Runner) schedule(s Settings) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !s.Enabled || s.IntervalMin == 0 {
		r.next = time.Time{}
		return
	}
	base := r.now()
	if last := r.St.SpeedtestLastTS(); last > 0 {
		base = time.Unix(last, 0)
	} else {
		base = base.Add(2 * time.Minute)
	}
	jit := time.Duration(0)
	if s.JitterMin > 0 {
		jit = time.Duration(rand.Int63n(int64(s.JitterMin)*60)) * time.Second
	}
	r.next = base.Add(time.Duration(s.IntervalMin)*time.Minute + jit)
	if r.next.Before(r.now()) { // overdue (mtmon was off): soon, but not instantly
		r.next = r.now().Add(time.Duration(30+rand.Intn(120)) * time.Second)
	}
}

// Reschedule drops the planned time so the next tick plans again from the current settings.
func (r *Runner) Reschedule() {
	r.mu.Lock()
	r.next = time.Time{}
	r.mu.Unlock()
}

// Run checks the schedule every 30 seconds until ctx ends.
func (r *Runner) Run(ctx context.Context) {
	r.mu.Lock()
	r.base = ctx
	r.mu.Unlock()
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		r.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick starts a scheduled round when one is due and allowed.
func (r *Runner) Tick(ctx context.Context) {
	s := LoadSettings(r.St)
	r.mu.Lock()
	needSched := r.next.IsZero()
	r.mu.Unlock()
	if needSched {
		r.schedule(s)
	}
	r.mu.Lock()
	due := !r.next.IsZero() && !r.now().Before(r.next) && !r.running
	r.mu.Unlock()
	if !s.Enabled || s.IntervalMin == 0 || !due {
		return
	}
	if reason := r.allowed(s, false); reason != "" {
		r.mu.Lock()
		r.skipped, r.skipT = reason, r.now()
		// try again soon: a window opens, the line quietens down
		r.next = r.now().Add(5 * time.Minute)
		r.mu.Unlock()
		return
	}
	r.mu.Lock()
	r.skipped = ""
	r.mu.Unlock()
	r.round(ctx, s)
	r.schedule(s)
}

// allowed returns why a round must not start now ("" = go). A manual run ignores the time window and the busy guard
// but never the data budget.
func (r *Runner) allowed(s Settings, manual bool) string {
	if s.Paused {
		return SkipPaused
	}
	now := r.now()
	if !manual && !InWindow(s.WindowFrom, s.WindowTo, now.Hour()*60+now.Minute()) {
		return SkipWindow
	}
	need := int64(s.MaxMB) << 20 * int64(len(s.Providers)*len(s.Families)) * 2 // worst case: download and upload both run into the size cap
	if s.BudgetDayMB > 0 {
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		if r.St.SpeedtestBytesSince(midnight.Unix())+need > int64(s.BudgetDayMB)<<20 {
			return SkipBudget
		}
	}
	if s.BudgetMonMB > 0 {
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		if r.St.SpeedtestBytesSince(first.Unix())+need > int64(s.BudgetMonMB)<<20 {
			return SkipBudget
		}
	}
	if !manual && s.SkipBusyPct > 0 && r.Busy != nil {
		if last := r.St.SpeedtestLastOK(); last.DownBps > 0 {
			down, up := r.Busy()
			f := float64(s.SkipBusyPct) / 100
			if down > f*last.DownBps || (last.UpBps > 0 && up > f*last.UpBps) {
				return SkipBusy
			}
		}
	}
	return ""
}

// ErrRefused explains a refused manual run.
type ErrRefused string

func (e ErrRefused) Error() string { return string(e) }

// RunNow starts a round in the background (manual run, admin only). It refuses while a round is running or when
// the data budget is used up.
func (r *Runner) RunNow() error {
	s := LoadSettings(r.St)
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return ErrRefused("a speed test is already running")
	}
	r.mu.Unlock()
	if reason := r.allowed(s, true); reason != "" && reason != SkipPaused {
		return ErrRefused(reason)
	}
	r.mu.Lock()
	ctx := r.base
	r.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		r.round(ctx, s)
		r.schedule(s)
	}()
	// give the goroutine the chance to mark itself running so an immediate second click is refused
	for i := 0; i < 50; i++ {
		r.mu.Lock()
		run := r.running
		r.mu.Unlock()
		if run {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	return nil
}

func (r *Runner) round(ctx context.Context, s Settings) {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.running = false; r.mu.Unlock() }()
	o := Opts{MaxBytes: int64(s.MaxMB) << 20, MaxDur: time.Duration(s.MaxSec) * time.Second, Streams: s.Streams}
	wan := ""
	if r.WAN != nil {
		wan = r.WAN()
	}
	for _, p := range r.providers(s) {
		fams := s.Families
		if p.Name() == "ookla" && len(fams) > 1 {
			fams = fams[:1] // the CLI picks the address family itself
		}
		for _, fam := range fams {
			if ctx.Err() != nil {
				return
			}
			o.Family = fam
			start := r.now()
			res := p.Run(ctx, o)
			t := store.Speedtest{TS: start.Unix(), Provider: p.Name(), Server: res.Server, Family: fam, DownBps: res.DownBps, UpBps: res.UpBps,
				PingMS: res.PingMS, JitterMS: res.JitterMS, Bytes: res.Bytes, DurS: res.Dur.Seconds(), Err: res.Err, WAN: wan}
			if res.Err != "" { // a failed test is a failure, never a 0 Mbit/s result
				t.DownBps, t.UpBps = 0, 0
			}
			if _, err := r.St.AddSpeedtest(t); err != nil && r.Log != nil {
				r.Log.Warn("store speed test", "err", err)
			}
			r.checkContract(s, p.Name(), fam)
		}
	}
}

// checkContract raises an alert when the last AlertCount successful tests of a provider/family were all below
// AlertBelow % of the contracted speed, and resolves it when a test is back above. Failed tests are skipped.
func (r *Runner) checkContract(s Settings, provider, family string) {
	if r.Raise == nil || (s.ContractDown <= 0 && s.ContractUp <= 0) {
		return
	}
	recent, err := r.St.SpeedtestRecentOK(provider, family, s.AlertCount)
	if err != nil || len(recent) == 0 {
		return
	}
	limitDown, limitUp := s.ContractDown*1e6*float64(s.AlertBelow)/100, s.ContractUp*1e6*float64(s.AlertBelow)/100
	below := func(t store.Speedtest) bool {
		return (s.ContractDown > 0 && t.DownBps > 0 && t.DownBps < limitDown) || (s.ContractUp > 0 && t.UpBps > 0 && t.UpBps < limitUp)
	}
	subject := provider
	if family != "" {
		subject += "/IPv" + family
	}
	if !below(recent[0]) {
		if r.Resolve != nil {
			r.Resolve("speed_low", subject, " – speed is back above the limit")
		}
		return
	}
	if len(recent) < s.AlertCount {
		return
	}
	for _, t := range recent {
		if !below(t) {
			return
		}
	}
	t := recent[0]
	r.Raise("speed_low", subject, "warning", fmt.Sprintf("Internet speed (%s) stayed below %d %% of the contracted speed in the last %d tests: %.0f/%.0f Mbit/s down/up (contract %.0f/%.0f)",
		subject, s.AlertBelow, s.AlertCount, t.DownBps/1e6, t.UpBps/1e6, s.ContractDown, s.ContractUp))
}
