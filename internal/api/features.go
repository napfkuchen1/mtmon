package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/poller"
	"github.com/napfkuchen1/mtmon/internal/provision"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// ---- per-device features (Devices → Edit → Features) --------------------------------------------------------------
// The optional router-side parts of the auto setup, switched on and off one by one on a registered device. Every
// change needs the router's admin credentials once (never stored), is recorded in the manifest and can be undone
// like the original setup; "off" reverts exactly what mtmon recorded for that feature.

type featureInfo struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Why         string `json:"why"`
	On          bool   `json:"on"`        // mtmon has applied it (recorded in the manifest)
	External    bool   `json:"external"`  // the router already does this, set up outside mtmon
	Available   bool   `json:"available"` // can be switched on for this device
	Unavailable string `json:"unavailable_reason,omitempty"`
	Changes     int    `json:"changes"`
}

var featureText = map[string][2]string{
	provision.FeatFlow: {"Traffic Flow (who connects where)",
		"The router reports every connection (source, destination, ports, bytes) to mtmon. This is the data behind Clients, Services, Insights and the live view."},
	provision.FeatFwLog: {"Firewall log (who was blocked)",
		"The router sends firewall log lines to mtmon and logs on the drop rules you pick, so blocked connections – which Traffic Flow never sees – show up in the Firewall page."},
	provision.FeatAllowLog: {"Log allowed traffic (which rule let it through)",
		"Turns on logging for the accept rules you pick, so the Firewall page also shows what was allowed and by which rule. Needs the firewall log target; it is set up with it if missing. Rules that match every packet of a connection are noisy: pick with care."},
	provision.FeatLogNew: {"Log every new connection (proof of 'allowed')",
		"Adds one rule at the end of the forward chain that logs each new connection that passed all drop rules. Complete picture of allowed connections, but more router CPU and log volume."},
	provision.FeatFasttrack: {"Disable FastTrack (count everything)",
		"FastTrack lets most of a connection bypass the packet path, so Traffic Flow only sees its start. Switching it off gives complete numbers but costs router CPU."},
}

// deviceFeatures derives the state of every feature from the manifest (and the router's capabilities).
func deviceFeatures(entries []store.ManifestEntry, caps *provision.Caps) []featureInfo {
	on := map[string]int{}
	for _, e := range entries {
		if e.State == "applied" {
			if f := provision.FeatureOf(e); f != "" {
				on[f]++
			}
		}
	}
	var out []featureInfo
	for _, k := range provision.FeatureKeys {
		fi := featureInfo{Key: k, Title: featureText[k][0], Why: featureText[k][1], On: on[k] > 0, Changes: on[k], Available: true}
		switch k {
		case provision.FeatFlow:
			fi.External = !fi.On && caps != nil && caps.Flow.Enabled && len(caps.Flow.Targets) > 0
		case provision.FeatFwLog, provision.FeatAllowLog, provision.FeatLogNew:
			if caps != nil && caps.Role != "router" && !fi.On {
				fi.Available, fi.Unavailable = false, "Only routers have a firewall worth logging."
			}
		case provision.FeatFasttrack:
			if !fi.On && (caps == nil || !caps.Fasttrack) {
				fi.Available, fi.Unavailable = false, "This device has no active FastTrack rule."
			}
		}
		out = append(out, fi)
	}
	return out
}

// storedCaps reads the capabilities saved with the device row.
func storedCaps(row *store.DeviceRow) *provision.Caps {
	var c provision.Caps
	if len(row.Caps) > 0 && json.Unmarshal(row.Caps, &c) == nil {
		return &c
	}
	return nil
}

// deviceFeaturesGet: GET /api/devices/{name}/features
func (s *Server) deviceFeaturesGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	row, err := s.St.DeviceRow(name)
	if err != nil || row == nil {
		jerr(w, 404, "unknown device (or defined in config.json)")
		return
	}
	entries, _ := s.St.Manifest(name)
	caps := storedCaps(row)
	// fresh view with the device's own (read-only) login; falls back to what was saved when it was added
	if d, ok := s.Cfg.Device(name); ok && s.Box != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		if c, err := provision.Probe(ctx, poller.NewGuardedClient(d, s.addrAllowed)); err == nil {
			caps = c
		}
		cancel()
	}
	out := map[string]any{"features": deviceFeatures(entries, caps), "managed": row.Managed}
	if caps != nil {
		out["filter_rules"] = caps.Filter
		out["role"] = caps.Role
	} else {
		out["filter_rules"] = []provision.FilterRule{}
	}
	jsonOut(w, out)
}

type featuresReq struct {
	User    string   `json:"user"`
	Pass    string   `json:"pass"`
	Enable  []string `json:"enable"`
	Disable []string `json:"disable"`
	FwRules []string `json:"fw_rules"` // .id of drop rules to log on (empty = suggested drop rules)
	// AllowRules: .id of accept rules to log on (empty = the enabled accept rules that are not "established/related")
	AllowRules []string `json:"allow_rules"`
	LogNew     bool     `json:"log_new"` // legacy: together with enabling firewall_logs it enables log_new as well
	DryRun     bool     `json:"dry_run"`
}

// defaultAllowRules: enabled, not yet logging accept rules that do not match every packet of established connections
// (logging those would flood the log).
func defaultAllowRules(caps *provision.Caps) []string {
	var out []string
	for _, f := range caps.Filter {
		sum := strings.ToLower(f.Summary)
		if f.Disabled || f.Managed || f.Log || f.Action != "accept" || strings.Contains(sum, "established") || strings.Contains(sum, "related") || strings.Contains(sum, "untracked") {
			continue
		}
		out = append(out, f.ID)
	}
	return out
}

func validFeature(k string) bool {
	for _, f := range provision.FeatureKeys {
		if f == k {
			return true
		}
	}
	return false
}

// deviceFeaturesApply: POST /api/devices/{name}/features – preview (dry_run) or apply.
func (s *Server) deviceFeaturesApply(w http.ResponseWriter, r *http.Request) {
	if s.Box == nil {
		jerr(w, 503, "device management not available")
		return
	}
	name := r.PathValue("name")
	var in featuresReq
	if !readJSON(w, r, &in) {
		return
	}
	for _, k := range append(append([]string{}, in.Enable...), in.Disable...) {
		if !validFeature(k) {
			jerr(w, 400, "unknown feature")
			return
		}
	}
	if len(in.Enable)+len(in.Disable) == 0 {
		jerr(w, 400, "nothing selected")
		return
	}
	if in.User == "" || in.Pass == "" {
		jerr(w, 400, "admin user and password are required (used once, never stored)")
		return
	}
	s.provMu.Lock()
	defer s.provMu.Unlock()
	row, err := s.St.DeviceRow(name)
	if err != nil || row == nil {
		jerr(w, 404, "unknown device (or defined in config.json)")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	admin := poller.NewGuardedClient(config.Device{Name: name, Addr: row.Addr, Port: row.Port, Scheme: row.Scheme, User: in.User, Pass: in.Pass,
		Fingerprint: row.Fingerprint, Insecure: row.Insecure}, s.addrAllowed)
	caps, err := provision.Probe(ctx, admin)
	if err != nil {
		jerr(w, 502, friendlyConnErr(err))
		return
	}
	if _, werr := admin.Get(ctx, "/user/group"); werr != nil {
		jerr(w, 403, "this account is not allowed to change the router (needs full or write rights)")
		return
	}
	entries, _ := s.St.Manifest(name)
	state := map[string]bool{}
	for _, f := range deviceFeatures(entries, caps) {
		state[f.Key] = f.On
	}
	enable, disable := map[string]bool{}, map[string]bool{}
	for _, k := range in.Enable {
		if !state[k] {
			enable[k] = true // already on: nothing to do
		}
	}
	for _, k := range in.Disable {
		if state[k] {
			disable[k] = true
		}
	}
	// logging on accept rules / new connections needs the syslog target of firewall_logs: pull it in (target only, no
	// drop-rule logging) when it is not there; switching firewall_logs off takes its dependents with it.
	if in.LogNew && enable[provision.FeatFwLog] {
		enable[provision.FeatLogNew] = true
	}
	depOnly := false
	for _, d := range provision.LogDependents {
		if enable[d] && !state[provision.FeatFwLog] && !enable[provision.FeatFwLog] {
			enable[provision.FeatFwLog], depOnly = true, true
		}
		if disable[provision.FeatFwLog] && state[d] {
			disable[d] = true
		}
	}
	// the options the wizard would use, for the features that are requested
	opt := provision.DefaultOptions(caps, srcIPFor(row.Addr, row.Port))
	opt.DeviceAddr = row.Addr
	opt.MonUser = row.User
	if _, fp, _ := net.SplitHostPort(s.Cfg.FlowListen); fp != "" {
		if n, _ := strconv.Atoi(fp); n > 0 {
			opt.FlowPort = n
		}
	}
	if _, sp, _ := net.SplitHostPort(s.Cfg.SyslogListen); sp != "" {
		if n, _ := strconv.Atoi(sp); n > 0 {
			opt.SyslogPort = n
		}
	}
	if len(in.FwRules) > 0 {
		opt.FwRules = in.FwRules
	}
	if depOnly {
		opt.FwRules = nil
	}
	opt.AllowRules = in.AllowRules
	if len(opt.AllowRules) == 0 {
		opt.AllowRules = defaultAllowRules(caps)
	}
	if enable[provision.FeatFwLog] || enable[provision.FeatFlow] {
		if opt.MtmonIP == "" {
			jerr(w, 502, "could not determine mtmon's address as seen from the router")
			return
		}
	}
	var undo []store.ManifestEntry
	for _, e := range entries {
		if e.State == "applied" && disable[provision.FeatureOf(e)] {
			undo = append(undo, e)
		}
	}
	if in.DryRun {
		descrs := []string{}
		for _, e := range undo {
			descrs = append(descrs, e.Descr)
		}
		plan := provision.PlanFeatures(caps, opt, enable)
		if plan == nil {
			plan = []provision.Step{}
		}
		jsonOut(w, map[string]any{"plan": plan, "will_undo": descrs, "fw_rules": opt.FwRules, "allow_rules": opt.AllowRules, "filter_rules": caps.Filter})
		return
	}

	out := map[string]any{"name": name}
	// 1. switch off: revert exactly what mtmon recorded for those features
	if len(undo) > 0 {
		res := provision.Rollback(ctx, admin, undo, func(id int64, st string) { s.St.SetManifestState(id, st) })
		out["undone"] = res
		for _, x := range res {
			if !x.OK {
				out["ok"] = false
				all, _ := s.St.Manifest(name)
				out["manual_script"] = provision.ManualScript(all)
				jsonOut(w, out)
				return // do not stack new changes on a half-reverted state
			}
		}
		for _, e := range undo { // forget the labels of exactly the rules that were switched back
			switch {
			case provision.FeatureOf(e) == provision.FeatLogNew:
				s.St.DB.Exec(`DELETE FROM fw_rules WHERE device=? AND prefix=?`, name, "MTM-NEW")
			case e.Path == "/ip/firewall/filter" && e.RID != "":
				s.St.DB.Exec(`DELETE FROM fw_rules WHERE device=? AND prefix=?`, name, "MTM-"+strings.TrimPrefix(e.RID, "*"))
			}
		}
	}
	// 2. switch on
	changed := len(undo) > 0
	if len(enable) > 0 {
		maxSeq := 0
		for _, e := range entries {
			if e.Seq > maxSeq {
				maxSeq = e.Seq
			}
		}
		res := provision.ApplyFeatures(ctx, admin, caps, opt, enable, maxSeq, func(e store.ManifestEntry) { s.St.AddManifest(name, e) })
		out["result"] = res
		if !res.OK {
			failed := false
			for _, rb := range res.Rollback {
				failed = failed || !rb.OK
			}
			if failed {
				all, _ := s.St.Manifest(name)
				out["manual_script"] = provision.ManualScript(all)
			} else {
				s.St.DB.Exec(`DELETE FROM manifest WHERE device=? AND seq>?`, name, maxSeq) // everything of this attempt was undone
			}
			out["ok"] = false
			jsonOut(w, out)
			return
		}
		var metas []store.FwRule
		for _, m := range res.FwMeta {
			metas = append(metas, store.FwRule{Device: name, Prefix: m.Prefix, Chain: m.Chain, Action: m.Action, Descr: m.Descr, Managed: m.Managed})
		}
		s.St.SaveFwRules(metas)
		changed = true
	}
	if changed {
		now, _ := s.St.Manifest(name)
		applied := false
		for _, e := range now {
			applied = applied || e.State == "applied"
		}
		row.Managed = applied
		if c, err := provision.Probe(ctx, admin); err == nil {
			row.Caps, _ = json.Marshal(c)
		}
		s.St.UpsertDevice(*row)
		s.LoadDevices()
	}
	out["ok"] = true
	jsonOut(w, out)
}
