// Package speedtest runs scheduled speed tests from the mtmon host (never from a router: /tool fetch on a small
// router is CPU-bound and would measure the router). Providers are native HTTP measurements (Hetzner download,
// Cloudflare download/upload/latency); results go to the speedtests table. Everything that costs traffic is
// bounded: per-test size and duration, a daily and monthly data budget, a time window, and "skip when busy".
package speedtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/napfkuchen1/mtmon/internal/store"
)

const metaKey = "speedtest_settings"

// Settings is stored as JSON in meta (the service user cannot write config.json).
type Settings struct {
	Enabled      bool     `json:"enabled"`
	Paused       bool     `json:"paused"`    // keeps the schedule configured but runs nothing
	Providers    []string `json:"providers"` // "cloudflare", "hetzner"
	Families     []string `json:"families"`  // "4", "6" or "" (system choice); one test per family and provider
	Hetzner      string   `json:"hetzner_server"`
	OoklaID      int      `json:"ookla_server_id"` // 0 = automatic
	OoklaOK      bool     `json:"ookla_accepted"`  // the user accepted Ookla\'s licence and privacy terms (needed to run it)
	IntervalMin  int      `json:"interval_min"`    // 0 = off
	WindowFrom   string   `json:"window_from"`     // "HH:MM" local time, with WindowTo; empty = any time
	WindowTo     string   `json:"window_to"`
	StartAt      string   `json:"start_at"`   // "HH:MM" anchor: runs land on start_at + n*interval; empty = interval after the last test
	JitterMin    int      `json:"jitter_min"` // random delay added to every run so installs do not all hit the servers at :00
	MaxMB        int      `json:"max_mb"`     // download volume per test and stream budget
	MaxSec       int      `json:"max_sec"`    // duration limit per direction
	Streams      int      `json:"streams"`
	BudgetDayMB  int      `json:"budget_day_mb"` // 0 = unlimited
	BudgetMonMB  int      `json:"budget_month_mb"`
	SkipBusyPct  int      `json:"skip_busy_pct"` // skip when current WAN traffic exceeds this % of the last result; 0 = never skip
	ContractDown float64  `json:"contract_down_mbps"`
	ContractUp   float64  `json:"contract_up_mbps"`
	AlertBelow   int      `json:"alert_below_pct"` // alert when this many tests in a row stay under X % of the contract
	AlertCount   int      `json:"alert_count"`
}

// HetznerServers maps the selectable Hetzner speed-test locations to their hosts.
var HetznerServers = map[string]string{
	"fsn1": "fsn1-speed.hetzner.com", "nbg1": "nbg1-speed.hetzner.com", "hel1": "hel1-speed.hetzner.com",
	"ash": "ash-speed.hetzner.com", "hil": "hil-speed.hetzner.com", "sin": "sin-speed.hetzner.com",
}

var validProviders = map[string]bool{"cloudflare": true, "hetzner": true, "ookla": true}

// DefaultSettings is conservative on purpose: off, hourly when switched on, small tests, 50 % busy guard.
func DefaultSettings() Settings {
	return Settings{Providers: []string{"cloudflare"}, Families: []string{""}, Hetzner: "fsn1", IntervalMin: 360, JitterMin: 10,
		MaxMB: 100, MaxSec: 10, Streams: 4, SkipBusyPct: 50, AlertBelow: 70, AlertCount: 3}
}

// Validate normalises and checks the settings; the result is safe to store.
func (s Settings) Validate() (Settings, error) {
	d := DefaultSettings()
	if len(s.Providers) == 0 {
		s.Providers = d.Providers
	}
	seen := map[string]bool{}
	var ps []string
	for _, p := range s.Providers {
		p = strings.ToLower(strings.TrimSpace(p))
		if !validProviders[p] {
			return s, fmt.Errorf("unknown provider %q", p)
		}
		if !seen[p] {
			seen[p] = true
			ps = append(ps, p)
		}
	}
	s.Providers = ps
	if len(s.Families) == 0 {
		s.Families = d.Families
	}
	seen = map[string]bool{}
	var fs []string
	for _, f := range s.Families {
		if f != "" && f != "4" && f != "6" {
			return s, fmt.Errorf("family must be 4, 6 or empty")
		}
		if !seen[f] {
			seen[f] = true
			fs = append(fs, f)
		}
	}
	s.Families = fs
	if s.Hetzner == "" {
		s.Hetzner = d.Hetzner
	}
	if _, ok := HetznerServers[s.Hetzner]; !ok {
		return s, fmt.Errorf("unknown Hetzner location %q", s.Hetzner)
	}
	if s.OoklaID < 0 || s.OoklaID > 99999999 {
		return s, errors.New("Ookla server id must be 0 (automatic) or a positive number")
	}
	if slices.Contains(s.Providers, "ookla") && !s.OoklaOK {
		return s, errors.New("accept the Ookla licence terms first to use Ookla")
	}
	if s.IntervalMin < 0 || s.IntervalMin > 7*24*60 || (s.IntervalMin > 0 && s.IntervalMin < 15) {
		return s, errors.New("interval must be 0 (off) or between 15 minutes and 7 days")
	}
	if (s.WindowFrom == "") != (s.WindowTo == "") {
		return s, errors.New("time window needs both a start and an end")
	}
	for _, w := range []string{s.WindowFrom, s.WindowTo} {
		if w != "" {
			if _, ok := parseHM(w); !ok {
				return s, fmt.Errorf("time %q is not HH:MM", w)
			}
		}
	}
	if s.WindowFrom != "" && s.WindowFrom == s.WindowTo {
		return s, errors.New("time window start and end must differ")
	}
	if s.StartAt != "" {
		if _, ok := parseHM(s.StartAt); !ok {
			return s, fmt.Errorf("start time %q is not HH:MM", s.StartAt)
		}
	}
	if s.JitterMin < 0 || s.JitterMin > 240 {
		return s, errors.New("jitter must be 0-240 minutes")
	}
	if s.MaxMB < 5 || s.MaxMB > 2000 {
		return s, errors.New("test size must be 5-2000 MB")
	}
	if s.MaxSec < 3 || s.MaxSec > 60 {
		return s, errors.New("test duration must be 3-60 seconds")
	}
	if s.Streams < 1 || s.Streams > 8 {
		return s, errors.New("streams must be 1-8")
	}
	if s.BudgetDayMB < 0 || s.BudgetMonMB < 0 || s.BudgetDayMB > 10_000_000 || s.BudgetMonMB > 10_000_000 {
		return s, errors.New("budgets must be 0 (unlimited) or a positive number of MB")
	}
	if s.SkipBusyPct < 0 || s.SkipBusyPct > 100 {
		return s, errors.New("busy limit must be 0-100 %")
	}
	if s.ContractDown < 0 || s.ContractUp < 0 || s.ContractDown > 100000 || s.ContractUp > 100000 {
		return s, errors.New("contracted speed must be 0-100000 Mbit/s")
	}
	if s.AlertBelow < 1 || s.AlertBelow > 100 || s.AlertCount < 1 || s.AlertCount > 50 {
		return s, errors.New("alert threshold must be 1-100 % for 1-50 tests")
	}
	return s, nil
}

// LoadSettings reads the stored settings; anything missing or unreadable falls back to the defaults.
func LoadSettings(st *store.Store) Settings {
	s := DefaultSettings()
	if raw := st.GetMeta(metaKey); raw != "" {
		_ = json.Unmarshal([]byte(raw), &s)
	}
	if v, err := s.Validate(); err == nil {
		return v
	}
	return DefaultSettings()
}

// SaveSettings validates and stores the settings.
func SaveSettings(st *store.Store, s Settings) (Settings, error) {
	v, err := s.Validate()
	if err != nil {
		return s, err
	}
	b, _ := json.Marshal(v)
	return v, st.SetMeta(metaKey, string(b))
}

// parseHM parses "HH:MM" into minutes since midnight.
func parseHM(s string) (int, bool) {
	var h, m int
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	if _, err := fmt.Sscanf(s, "%02d:%02d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// InWindow reports whether minutes-since-midnight now lies in [from, to); a window may cross midnight.
func InWindow(from, to string, nowMin int) bool {
	if from == "" || to == "" {
		return true
	}
	f, ok1 := parseHM(from)
	t, ok2 := parseHM(to)
	if !ok1 || !ok2 {
		return true
	}
	if f < t {
		return nowMin >= f && nowMin < t
	}
	return nowMin >= f || nowMin < t
}
