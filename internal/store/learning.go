package store

import (
	"regexp"
	"strconv"
	"time"
)

// Learning period: after a reset, or after an update that changes how traffic is judged, mtmon records
// baseline-based alerts and suggestions as info ("learning") instead of raising false alarms, while the baselines
// (spike level, recurring patterns) fill up again. meta keys: learning_until, baseline_since (unix), learning_reason.

const LearningDefault = 24 * time.Hour

// LearningUntil is the end of the learning period (0 = none running).
func (s *Store) LearningUntil() int64 {
	n, _ := strconv.ParseInt(s.GetMeta("learning_until"), 10, 64)
	if n <= time.Now().Unix() {
		return 0
	}
	return n
}

// LearningReason says why the running period was started: "update <version>", "data reset" or "baselines reset".
// Empty when none is running.
func (s *Store) LearningReason() string {
	if s.LearningUntil() == 0 {
		return ""
	}
	return s.GetMeta("learning_reason")
}

// BaselineSince: alerts older than this no longer count as history for recurring-pattern detection (0 = all).
func (s *Store) BaselineSince() int64 {
	n, _ := strconv.ParseInt(s.GetMeta("baseline_since"), 10, 64)
	return n
}

// StartLearning begins a learning period of d; resetBaselines also makes the alert history forgotten for
// recurring-pattern detection (the alerts themselves stay). reason is shown next to the banner.
func (s *Store) StartLearning(d time.Duration, resetBaselines bool, reason string) {
	now := time.Now()
	s.SetMeta("learning_until", strconv.FormatInt(now.Add(d).Unix(), 10))
	s.SetMeta("learning_reason", reason)
	if resetBaselines {
		s.SetMeta("baseline_since", strconv.FormatInt(now.Unix(), 10))
	}
}

// BaselineAlerts is Alerts without the history before the last baseline reset.
func (s *Store) BaselineAlerts(limit int) ([]Alert, error) {
	as, err := s.Alerts(limit)
	if err != nil {
		return nil, err
	}
	since := s.BaselineSince()
	if since == 0 {
		return as, nil
	}
	out := as[:0]
	for _, a := range as {
		if a.TS >= since {
			out = append(out, a)
		}
	}
	return out, nil
}

// relearnReleases lists the versions whose detection logic changed so that stored baselines no longer fit
// (e.g. VPN peers re-keyed). Updating across one of them starts a learning period on first start.
var relearnReleases = []string{"0.17.0"}

var verNum = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

func verTriple(v string) (t [3]int, ok bool) {
	m := verNum.FindStringSubmatch(v)
	if m == nil {
		return t, false
	}
	for i := range t {
		t[i], _ = strconv.Atoi(m[i+1])
	}
	return t, true
}

func verLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// NeedsRelearn reports whether going from prev to cur crosses a release in relearnReleases. An unknown or empty
// prev (first start, fresh database) never does: there is nothing to relearn.
func NeedsRelearn(prev, cur string) bool {
	p, ok1 := verTriple(prev)
	c, ok2 := verTriple(cur)
	if !ok1 || !ok2 {
		return false
	}
	for _, r := range relearnReleases {
		rv, _ := verTriple(r)
		if verLess(p, rv) && !verLess(c, rv) {
			return true
		}
	}
	return false
}

// NoteStart records the running version and starts a learning period when the update needs one.
func (s *Store) NoteStart(version string) (relearn bool) {
	prev := s.GetMeta("last_run_version")
	if prev == "" { // started by a version that did not record this yet: an existing database is "older", a fresh one is not
		var n int
		s.DB.QueryRow(`SELECT count(*) FROM clients`).Scan(&n)
		if n > 0 {
			prev = "0.0.0"
		}
	}
	if prev != version {
		if NeedsRelearn(prev, version) {
			s.StartLearning(LearningDefault, false, "update "+version)
			relearn = true
		}
		s.SetMeta("last_run_version", version)
	}
	return relearn
}
