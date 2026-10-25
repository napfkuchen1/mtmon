package dnsbypass

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// Meta keys: the alert is off by default; "seen" holds the "mac|type" pairs that were already reported.
const (
	MetaAlert = "dns_bypass_alert"
	metaSeen  = "dns_bypass_seen"
	// Window is how far back a client counts as bypassing.
	Window = 7 * 24 * time.Hour
)

// Watcher raises an info alert when a client starts bypassing the local DNS (off by default).
type Watcher struct {
	St    *store.Store
	Raise func(kind, subject, severity, msg string)
	Name  func(mac string) string // display name of a client; optional
	Probe func() []string         // WAN-probe targets (host:port), never a DoH finding; optional
	Every time.Duration           // 0 = 15 minutes
}

// Run checks periodically until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	every := w.Every
	if every == 0 {
		every = 15 * time.Minute
	}
	t := time.NewTimer(2 * time.Minute) // let the first flows come in after a start
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		w.Check(time.Now())
		t.Reset(every)
	}
}

// Check reports clients with a bypass type that was not known yet. The first check after switching the alert on only
// records the current state, so enabling it does not raise an alert per existing client.
func (w *Watcher) Check(now time.Time) {
	if w.St.GetMeta(MetaAlert) != "on" {
		return
	}
	var ignore []string
	if w.Probe != nil {
		ignore = ProbeHosts(w.Probe())
	}
	resets := LoadResets(w.St)
	cs, err := DetectOpts(w.St, now.Add(-Window).Unix(), Options{Ignore: ignore, Resets: resets, Now: now})
	if err != nil {
		return
	}
	raw := w.St.GetMeta(metaSeen)
	first := raw == ""
	seen := map[string]bool{}
	var list []string
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &list)
	}
	for _, k := range list {
		seen[k] = true
	}
	for _, c := range cs {
		for _, typ := range c.Types {
			k := c.MAC + "|" + typ
			if seen[k] {
				continue
			}
			seen[k] = true
			if first || w.Raise == nil {
				continue
			}
			name := c.MAC
			if w.Name != nil {
				if n := w.Name(c.MAC); n != "" {
					name = n
				}
			}
			var res []string
			for _, r := range c.Resolvers {
				if r.Type == typ && len(res) < 3 {
					res = append(res, r.IP)
				}
			}
			again := ""
			if resets[c.MAC] > 0 {
				again = " again"
			}
			w.Raise("dns_bypass", c.MAC+"|"+typ, "info", fmt.Sprintf("%s bypasses the local DNS%s (%s) via %s", name, again, typ, strings.Join(res, ", ")))
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	b, _ := json.Marshal(out)
	w.St.SetMeta(metaSeen, string(b))
}
