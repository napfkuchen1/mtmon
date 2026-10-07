package provision

import (
	"context"
	"fmt"
	"strings"

	"github.com/napfkuchen1/mtmon/internal/poller"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// Features are the optional router-side parts of the auto setup that can be switched on and off one by one on a device
// that is already registered (Devices → Edit → Features). The read-only monitoring user is the basis and not a feature.
const (
	FeatFlow      = "flow"          // Traffic Flow (IPFIX) export: who talks to whom
	FeatFwLog     = "firewall_logs" // syslog of the firewall topic + logging on selected drop rules
	FeatAllowLog  = "allow_log"     // logging on selected accept rules (needs the syslog target of FeatFwLog)
	FeatLogNew    = "log_new"       // passthrough rule that logs every NEW connection that passed all drop rules
	FeatFasttrack = "fasttrack"     // disable FastTrack so Traffic Flow sees every packet
)

// FeatureKeys in display order.
var FeatureKeys = []string{FeatFlow, FeatFwLog, FeatAllowLog, FeatLogNew, FeatFasttrack}

// LogDependents need the syslog target that FeatFwLog creates: they cannot stay on without it.
var LogDependents = []string{FeatAllowLog, FeatLogNew}

// FeatureOf tells which feature a recorded router change belongs to ("" = not a feature: user, group, backup).
func FeatureOf(e store.ManifestEntry) string {
	switch {
	case strings.HasPrefix(e.Path, "/ip/traffic-flow"):
		return FeatFlow
	case e.Path == "/system/logging" || e.Path == "/system/logging/action":
		return FeatFwLog
	case e.Path == "/ip/firewall/filter":
		switch {
		case strings.Contains(e.Descr, "FastTrack"):
			return FeatFasttrack
		case strings.Contains(e.Descr, "MTM-NEW"):
			return FeatLogNew
		case strings.HasPrefix(e.Descr, "Allow "):
			return FeatAllowLog
		}
		return FeatFwLog
	}
	return ""
}

// stepKeys maps a feature to the steps (see buildActions) that implement it.
var stepKeys = map[string][]string{FeatFlow: {"flow"}, FeatFwLog: {"syslog", "fwlog"}, FeatAllowLog: {"allowlog"}, FeatLogNew: {"lognew"}, FeatFasttrack: {"fasttrack"}}

// featureOptions narrows o to the requested features.
func featureOptions(o Options, feats map[string]bool) Options {
	o.Flow = feats[FeatFlow]
	o.Syslog = feats[FeatFwLog]
	o.SyslogReady = o.Syslog || feats[FeatAllowLog] || feats[FeatLogNew]
	if !feats[FeatFwLog] {
		o.FwRules = nil
	}
	if !feats[FeatAllowLog] {
		o.AllowRules = nil
	}
	o.LogNew = feats[FeatLogNew]
	o.DisableFasttrack = feats[FeatFasttrack]
	return o
}

func featureActions(k *Caps, o Options, feats map[string]bool) []action {
	keep := map[string]bool{"backup": true}
	for f, on := range feats {
		if on {
			for _, s := range stepKeys[f] {
				keep[s] = true
			}
		}
	}
	var out []action
	for _, a := range buildActions(k, featureOptions(o, feats)) {
		if keep[a.Key] {
			out = append(out, a)
		}
	}
	return out
}

// PlanFeatures is what ApplyFeatures would do, in the same form as the setup wizard shows it.
func PlanFeatures(k *Caps, o Options, feats map[string]bool) []Step {
	var out []Step
	for _, a := range featureActions(k, o, feats) {
		out = append(out, a.Step)
	}
	if len(out) == 1 && out[0].Key == "backup" {
		return nil // nothing but the backup: no feature requested (or nothing applicable on this device)
	}
	return out
}

// ApplyFeatures runs only the steps of the requested features (after the usual configuration backup). Manifest
// sequence numbers continue after startSeq. Like Apply it undoes its own changes if a step fails.
func ApplyFeatures(ctx context.Context, cl *poller.Client, k *Caps, o Options, feats map[string]bool, startSeq int, rec func(store.ManifestEntry)) *Result {
	res := &Result{}
	r := &runner{cl: cl, rec: rec, res: res, seq: startSeq}
	for _, a := range featureActions(k, o, feats) {
		err := a.run(ctx, r)
		sr := StepResult{Key: a.Key, Title: a.Title, OK: err == nil}
		if err != nil {
			sr.Msg = err.Error()
			res.Steps = append(res.Steps, sr)
			res.Error = fmt.Sprintf("%s: %v", a.Title, err)
			if len(r.entries) > 0 {
				res.RolledBack = true
				res.Rollback = Rollback(ctx, cl, r.entries, nil)
			}
			res.Entries = r.entries
			return res
		}
		res.Steps = append(res.Steps, sr)
	}
	res.OK, res.Entries = true, r.entries
	return res
}
