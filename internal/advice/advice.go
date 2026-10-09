// Package advice derives improvement suggestions from data mtmon already has. Rules are pure functions
// over a read-only Snapshot, so they can be unit-tested with synthetic data; the evaluator runs on demand
// (see internal/api) and never polls anything itself.
package advice

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	SevInfo    = "info"
	SevTip     = "tip"
	SevWarning = "warning"

	CatPerformance = "Performance"
	CatSecurity    = "Security"
	CatWifi        = "Wi-Fi"
	CatHygiene     = "Network hygiene"
	CatMonitoring  = "Monitoring quality"
)

// Suggestion is one concrete, explainable improvement. All text is English; the UI translates it.
type Suggestion struct {
	ID         string   `json:"id"`   // rule id + subject, stable so dismissals stick
	Rule       string   `json:"rule"` // rule id
	Subject    string   `json:"subject,omitempty"`
	Severity   string   `json:"severity"`
	Category   string   `json:"category"`
	Title      string   `json:"title"`
	Why        string   `json:"why"`
	Evidence   []string `json:"evidence"`
	Steps      []string `json:"steps"`          // how to fix (UI steps)
	Commands   string   `json:"commands"`       // ready-to-copy RouterOS commands (optional)
	Link       string   `json:"link,omitempty"` // UI route, e.g. "clients" or "device/gw-main"
	Confidence string   `json:"confidence"`     // high | medium | low
	Limits     string   `json:"limits,omitempty"`
	Fix        *Fix     `json:"fix,omitempty"` // set when mtmon can apply the change itself (needs the router's admin login once)
}

// Fix names the per-device features (see provision.FeatureKeys) that resolve a suggestion when switched on.
type Fix struct {
	Device   string   `json:"device"`
	Features []string `json:"features"`
}

// Skipped explains a rule that could not run because its inputs are unavailable.
type Skipped struct {
	Rule   string `json:"rule"`
	Reason string `json:"reason"`
}

type Result struct {
	GeneratedAt int64        `json:"generated_at"`
	Items       []Suggestion `json:"suggestions"`
	Skipped     []Skipped    `json:"skipped"`
}

// RuleFunc returns suggestions, or a non-empty skip reason when the data it needs is missing.
type RuleFunc func(s Snapshot) (out []Suggestion, skip string)

type Rule struct {
	ID   string
	Eval RuleFunc
}

// Rules is the registry; evaluation order is irrelevant (results are sorted).
var Rules = []Rule{
	{"ram", ruleRAM}, {"cpu", ruleCPU}, {"version", ruleVersion}, {"firmware", ruleFirmware},
	{"fasttrack", ruleFasttrack}, {"hw-offload", ruleHWOffload},
	{"weak-signal", ruleWeakSignal}, {"band-24", ruleBand24}, {"roaming", ruleRoaming}, {"ap-load", ruleAPLoad},
	{"unidentified", ruleUnidentified},
	{"proto-cleartext", ruleCleartext}, {"proto-smb", ruleSMB}, {"proto-rdp", ruleRDPOut}, {"proto-http-iot", ruleHTTPIoT},
	{"exposed-inbound", ruleInbound},
	{"fw-origin", ruleBlockOrigin}, {"mgmt-open", ruleMgmtOpen}, {"fw-unused", ruleUnusedRules}, {"fw-regression", ruleRegression}, {"fw-nolog", ruleDropNoLog},
	{"dns-bypass", ruleDNSBypass}, {"dns-doh", ruleDoH},
	{"dominant-client", ruleDominant}, {"upload-heavy", ruleUploadHeavy}, {"chatty", ruleChatty},
	{"flow-disabled", ruleFlowDisabled}, {"flow-silent", ruleFlowSilent}, {"no-syslog", ruleNoSyslog},
	{"static-lease", ruleStaticLease}, {"stale-clients", ruleStaleClients},
}

// Evaluate runs every rule (a panicking rule is reported as skipped, never fatal) and sorts the result:
// warnings first, then by category and title.
func Evaluate(s Snapshot) Result {
	res := Result{GeneratedAt: s.Now().Unix(), Items: []Suggestion{}, Skipped: []Skipped{}}
	for _, r := range Rules {
		func() {
			defer func() {
				if e := recover(); e != nil {
					res.Skipped = append(res.Skipped, Skipped{r.ID, "internal error"})
				}
			}()
			out, skip := r.Eval(s)
			if skip != "" {
				res.Skipped = append(res.Skipped, Skipped{r.ID, skip})
			}
			for _, x := range out {
				if x.Rule == "" {
					x.Rule = r.ID
				}
				if x.ID == "" {
					x.ID = MakeID(x.Rule, x.Subject)
				}
				if x.Evidence == nil {
					x.Evidence = []string{}
				}
				if x.Steps == nil {
					x.Steps = []string{}
				}
				if x.Confidence == "" {
					x.Confidence = "high"
				}
				res.Items = append(res.Items, x)
			}
		}()
	}
	rank := map[string]int{SevWarning: 0, SevTip: 1, SevInfo: 2}
	sort.SliceStable(res.Items, func(i, j int) bool {
		a, b := res.Items[i], res.Items[j]
		if rank[a.Severity] != rank[b.Severity] {
			return rank[a.Severity] < rank[b.Severity]
		}
		if a.Category != b.Category {
			return a.Category < b.Category
		}
		return a.ID < b.ID
	})
	return res
}

var nonID = regexp.MustCompile(`[^a-z0-9._]+`)

// MakeID builds a stable, URL-safe id from the rule id and a subject (device name, MAC, ...).
func MakeID(rule, subject string) string {
	if subject == "" {
		return rule
	}
	return rule + ":" + strings.Trim(nonID.ReplaceAllString(strings.ToLower(subject), "-"), "-")
}

// ---- small helpers shared by the rules ----

func pct(v float64) string { return fmt.Sprintf("%.0f", v) }

// list joins up to max items and appends "…(+N)" for the remainder (language neutral).
func list(items []string, max int) string {
	if len(items) <= max {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:max], ", ") + fmt.Sprintf(", …(+%d)", len(items)-max)
}

func humanBytes(b int64) string {
	f := float64(b)
	for _, u := range []string{"B", "kB", "MB", "GB", "TB"} {
		if f < 1000 || u == "TB" {
			if u == "B" {
				return fmt.Sprintf("%d B", b)
			}
			if f >= 100 {
				return fmt.Sprintf("%.0f %s", f, u)
			}
			return fmt.Sprintf("%.1f %s", f, u)
		}
		f /= 1000
	}
	return ""
}

func since(now time.Time, d time.Duration) int64 { return now.Add(-d).Unix() }
