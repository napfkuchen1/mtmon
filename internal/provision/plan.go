// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/napfkuchen1/mtmon/internal/poller"
	"github.com/napfkuchen1/mtmon/internal/store"
)

type Options struct {
	Name             string   `json:"name"`
	Role             string   `json:"role"`
	Site             string   `json:"site"`
	MtmonIP          string   `json:"mtmon_ip"` // address of mtmon as the router sees it
	FlowPort         int      `json:"flow_port"`
	SyslogPort       int      `json:"syslog_port"`
	Flow             bool     `json:"flow"`
	Syslog           bool     `json:"syslog"`
	FwRules          []string `json:"fw_rules"`      // .id of filter rules to enable logging on
	AllowRules       []string `json:"allow_rules"`   // .id of accept rules to enable logging on
	ConvertAllow     []string `json:"convert_allow"` // .id of accept rules mtmon logged earlier: switch them to counter mode (log=no)
	LogNew           bool     `json:"log_new"`
	DisableFasttrack bool     `json:"disable_fasttrack"`
	SkipBackup       bool     `json:"skip_backup"`
	MonUser          string   `json:"mon_user"`
	DeviceAddr       string   `json:"device_addr"` // management address of the device; used as export source address
	MonPass          string   `json:"-"`
	SyslogReady      bool     `json:"-"`          // the syslog target already exists (set up earlier), so only the rules are added
	EventLogs        bool     `json:"event_logs"` // also send the system/account/dhcp/wifi/wireguard/script log topics (router event log)
}

// Step is the human-readable description shown in the wizard before anything is changed.
type Step struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Why      string   `json:"why"`
	Commands []string `json:"commands"` // RouterOS CLI equivalent
	Undo     string   `json:"undo"`
	Risk     string   `json:"risk"` // none | low | medium
}

type FwMeta struct {
	Prefix, Chain, Action, Descr string
	Managed                      bool
	Counter                      bool // accept rule counted via the router's rule counter, not logged (#278)
}

// action couples a Step with its executor, so the plan shown to the user can never drift from what runs.
type action struct {
	Step
	run func(ctx context.Context, r *runner) error
}

const monGroup = "mtmon-ro"
const syslogAction = "mtmonsyslog"

func DefaultOptions(k *Caps, mtmonIP string) Options {
	o := Options{Name: k.Identity, Role: k.Role, MtmonIP: mtmonIP, FlowPort: 2055, SyslogPort: 5514, MonUser: "mtmon",
		Flow: k.ExportsFlow, Syslog: k.Role == "router"}
	if o.Name == "" {
		o.Name = k.Model
	}
	for _, f := range k.Filter {
		if o.Syslog && !f.Disabled && !f.Managed && (f.Action == "drop" || f.Action == "reject") {
			o.FwRules = append(o.FwRules, f.ID)
		}
	}
	for _, f := range k.Filter6 {
		if o.Syslog && !f.Disabled && !f.Managed && (f.Action == "drop" || f.Action == "reject") {
			o.FwRules = append(o.FwRules, f.ID)
		}
	}
	return o
}

func srcArg(o Options) string {
	if a, err := netip.ParseAddr(o.DeviceAddr); err == nil && !a.IsLoopback() {
		return " src-address=" + a.String()
	}
	return ""
}

// v6Prefix marks rule IDs of /ipv6/firewall/filter inside the shared ID lists (Options, requests); the router never sees it.
const v6Prefix = "v6:"

// prefixFor is the log prefix of a rule: MTM-<id> for IPv4, MTM6-<id> for IPv6 (#298).
func prefixFor(id string) string {
	if raw, ok := strings.CutPrefix(id, v6Prefix); ok {
		return "MTM6-" + strings.TrimPrefix(raw, "*")
	}
	return "MTM-" + strings.TrimPrefix(id, "*")
}

// ruleMenu splits a rule ID into its REST menu, the CLI form of the menu and the router's own .id.
func ruleMenu(id string) (menu, cli, raw string) {
	if raw, ok := strings.CutPrefix(id, v6Prefix); ok {
		return "/ipv6/firewall/filter", "/ipv6 firewall filter", raw
	}
	return "/ip/firewall/filter", "/ip firewall filter", id
}

func buildActions(k *Caps, o Options) []action {
	var a []action
	ip := o.MtmonIP
	if !o.SkipBackup {
		a = append(a, action{Step: Step{Key: "backup", Title: "Back up configuration (/export)",
			Why:      "Before any change, the complete router configuration is saved as an .rsc file on the router (Files). If that fails, mtmon changes nothing.",
			Commands: []string{"/export file=mtmon-before-<time>"}, Undo: "Nothing to do (just a file)", Risk: "none"},
			run: func(ctx context.Context, r *runner) error { return r.backup(ctx) }})
	}
	a = append(a, action{Step: Step{Key: "user", Title: "Create read-only user for mtmon",
		Why: "mtmon uses it to read status, Wi-Fi clients, DHCP, ARP, etc. The group only has the policies read, api and rest-api (no write, no ssh/winbox/ftp) and the login is restricted to mtmon's IP.",
		Commands: []string{
			"/user group add name=" + monGroup + " policy=read,api,rest-api comment=" + ManagedTag,
			"/user add name=" + o.MonUser + " group=" + monGroup + " password=<random, 24 chars> address=" + ip + "/32 comment=" + ManagedTag},
		Undo: "User and group are deleted", Risk: "low"},
		run: func(ctx context.Context, r *runner) error { return r.createUser(ctx, o) }})
	if o.Flow {
		a = append(a, action{Step: Step{Key: "flow", Title: "Export Traffic Flow (IPFIX) to mtmon",
			Why: "This is the data source for 'who connects where': source, destination, ports and bytes per connection. Active flows are reported every 30 s (real time). Existing targets/collectors are left untouched.",
			Commands: []string{
				"/ip traffic-flow set enabled=yes interfaces=all cache-entries=16k active-flow-timeout=30s inactive-flow-timeout=15s",
				fmt.Sprintf("/ip traffic-flow target add dst-address=%s port=%d version=ipfix%s", ip, o.FlowPort, srcArg(o))},
			Undo: "The target is deleted and the previous Traffic Flow settings are restored exactly", Risk: "low"},
			run: func(ctx context.Context, r *runner) error { return r.flow(ctx, k, o) }})
	}
	if o.Syslog {
		a = append(a, action{Step: Step{Key: "syslog", Title: "Send firewall logs to mtmon via syslog",
			Why: "This lets mtmon see which firewall rule blocked or allowed a connection. Only the 'firewall' topic is sent to mtmon; local logging stays as it is.",
			Commands: []string{
				fmt.Sprintf("/system logging action add name=%s target=remote remote=%s remote-port=%d%s", syslogAction, ip, o.SyslogPort, srcArg(o)),
				"/system logging add topics=firewall action=" + syslogAction},
			Undo: "The logging rule and action are deleted", Risk: "low"},
			run: func(ctx context.Context, r *runner) error { return r.syslog(ctx, o) }})
	}
	if o.Syslog || o.SyslogReady {
		// a rule that already holds the target state gets no step at all (#343): plan and run stay the same action
		if fw := pendingRules(k, o.FwRules, false); len(fw) > 0 {
			a = append(a, action{Step: logRulesStep(k, "fwlog", fw, fmt.Sprintf("Enable logging on %d existing firewall rules", len(fw)),
				"Only 'log' and 'log-prefix' of these rules are set (conditions, action and order stay unchanged). The prefix tells mtmon which rule matched. This way dropped traffic that Traffic Flow never sees shows up in mtmon."),
				run: func(ctx context.Context, r *runner) error { return r.fwLog(ctx, k, fw, false) }})
		}
		if al := pendingRules(k, o.AllowRules, true); len(al) > 0 {
			a = append(a, action{Step: countRulesStep(k, "allowlog", al, fmt.Sprintf("Count hits on %d allow rules", len(al)),
				"Only 'log-prefix' of these accept rules is set; 'log' stays off (conditions, action and order stay unchanged). mtmon reads the rule's hit counter on every poll and shows the difference as 'allowed', so nothing is written to the router log and no syslog line is sent per packet."),
				run: func(ctx context.Context, r *runner) error { return r.fwLog(ctx, k, al, true) }})
		}
		if conv := pendingConvert(k, o.ConvertAllow); len(conv) > 0 {
			a = append(a, action{Step: countRulesStep(k, "allowcount", conv, fmt.Sprintf("Switch %d allow rules from logging to counting", len(conv)),
				"These accept rules were set to log every packet by an earlier mtmon version. Only 'log' is switched off; the log prefix stays. mtmon then reads the rule's hit counter instead, so the Firewall page keeps its numbers while the router log and the syslog traffic shrink."),
				run: func(ctx context.Context, r *runner) error { return r.fwCount(ctx, k, conv) }})
		}
		if o.EventLogs {
			var cmds []string
			for _, t := range EventTopics {
				cmds = append(cmds, "/system logging add topics="+t+" action="+syslogAction+" comment="+eventRuleComment)
			}
			a = append(a, action{Step: Step{Key: "evlog", Title: "Send the router's event log to mtmon",
				Why:      "Login failures, critical system events, DHCP and Wi-Fi trouble, WireGuard handshakes and failing scripts reach mtmon, which shows them per device and raises alerts for the ones that matter. One logging rule per topic is added; '!debug' keeps the router's debug flood (DHCP packet dumps, WireGuard keepalives) away from mtmon, while 'script,error' still delivers failing commands. Local logging stays as it is.",
				Commands: cmds, Undo: "The logging rules are deleted", Risk: "low"},
				run: func(ctx context.Context, r *runner) error { return r.eventLogs(ctx) }})
		}
		if o.LogNew {
			a = append(a, action{Step: Step{Key: "lognew", Title: "Rule at the end of the forward chain: log new connections",
				Why:      "A 'passthrough' rule at the very bottom logs every NEW connection that survived all drop rules. Proof: 'allowed'. Costs CPU and log volume on the router.",
				Commands: []string{"/ip firewall filter add chain=forward action=passthrough connection-state=new log=yes log-prefix=MTM-NEW comment=\"" + ManagedTag + ": log new connections\""},
				Undo:     "The rule is deleted", Risk: "medium"},
				run: func(ctx context.Context, r *runner) error { return r.logNew(ctx) }})
		}
	}
	if o.DisableFasttrack && k.Fasttrack {
		a = append(a, action{Step: Step{Key: "fasttrack", Title: "Disable FastTrack rules",
			Why:      "FastTrack bypasses large parts of the packet path, so Traffic Flow only sees the start of each connection. Without FastTrack mtmon counts completely, but the router needs more CPU.",
			Commands: []string{"/ip firewall filter disable [find where action=fasttrack-connection]"},
			Undo:     "The rules are re-enabled", Risk: "medium"},
			run: func(ctx context.Context, r *runner) error { return r.fasttrack(ctx, k) }})
	}
	a = append(a, action{Step: Step{Key: "verify", Title: "Test: log in with the new user",
		Why:      "mtmon logs in with the new read-only user. If that fails (e.g. wrong IP from the router's point of view), everything is rolled back automatically.",
		Commands: []string{"GET /rest/system/resource  (as " + o.MonUser + ")"}, Undo: "–", Risk: "none"},
		run: func(ctx context.Context, r *runner) error { return r.verify(ctx) }})
	return a
}

// ruleHolds tells whether a selected rule already is in the state mtmon wants. A rule that logs is never touched, the
// logging belongs to whoever set it. A counted accept rule stays counted, also when it arrives through the drop-rule list.
func ruleHolds(f *FilterRule, id string, allow bool) bool {
	if f.Log {
		return true
	}
	return f.LogPrefix == prefixFor(id) && (allow || f.Action == "accept")
}

// pendingRules: the selected rules that still need a change. Unknown IDs stay in, the step reports them.
func pendingRules(k *Caps, ids []string, allow bool) []string {
	var out []string
	for _, id := range ids {
		if f := k.rule(id); f == nil || !ruleHolds(f, id, allow) {
			out = append(out, id)
		}
	}
	return out
}

// pendingConvert: the accept rules to switch to counting that still log.
func pendingConvert(k *Caps, ids []string) []string {
	var out []string
	for _, id := range ids {
		if f := k.rule(id); f == nil || f.Log {
			out = append(out, id)
		}
	}
	return out
}

// holdingRuleMeta names the selected rules that need no step, so the Firewall page keeps its labels for them.
func holdingRuleMeta(k *Caps, o Options) []FwMeta {
	if !o.Syslog && !o.SyslogReady {
		return nil
	}
	var out []FwMeta
	note := func(ids []string, allow bool) {
		for _, id := range ids {
			f := k.rule(id)
			if f == nil || !ruleHolds(f, id, allow) || f.LogPrefix == "" {
				continue
			}
			descr := strings.TrimSpace(f.Comment)
			if descr == "" {
				descr = f.Summary
			}
			out = append(out, FwMeta{Prefix: f.LogPrefix, Chain: f.Chain, Action: f.Action, Descr: descr,
				Managed: f.LogPrefix == prefixFor(id), Counter: !f.Log})
		}
	}
	note(o.FwRules, false)
	note(o.AllowRules, true)
	return out
}

// logRulesStep describes "switch logging on for these filter rules" (drop rules and allow rules share it).
func logRulesStep(k *Caps, key string, ids []string, title, why string) Step {
	var cmds []string
	for _, id := range ids {
		if f := k.rule(id); f != nil {
			_, cli, raw := ruleMenu(id)
			cmds = append(cmds, fmt.Sprintf("%s set %s log=yes log-prefix=%s   # %s %s %s", cli, raw, prefixFor(id), f.Chain, f.Action, f.Comment))
		}
	}
	return Step{Key: key, Title: title, Why: why, Commands: cmds, Undo: "log and log-prefix of each rule are reset to their previous values", Risk: "medium"}
}

// countRulesStep describes "set the log prefix, keep log off" for accept rules (hits come from the rule counter).
func countRulesStep(k *Caps, key string, ids []string, title, why string) Step {
	var cmds []string
	for _, id := range ids {
		if f := k.rule(id); f != nil {
			_, cli, raw := ruleMenu(id)
			cmds = append(cmds, fmt.Sprintf("%s set %s log=no log-prefix=%s   # %s %s %s", cli, raw, prefixFor(id), f.Chain, f.Action, f.Comment))
		}
	}
	return Step{Key: key, Title: title, Why: why, Commands: cmds, Undo: "log and log-prefix of each rule are reset to their previous values", Risk: "low"}
}

// ConvertibleAllow lists the accept rules that carry mtmon's own log prefix and still log every packet (set up by
// mtmon versions before 0.20.11). Rules with any other prefix, and rules that are not plain accept, are never touched.
func ConvertibleAllow(k *Caps) []string {
	var out []string
	for _, f := range k.Filter {
		if f.Action == "accept" && f.Log && !f.Managed && f.LogPrefix == prefixFor(f.ID) {
			out = append(out, f.ID)
		}
	}
	return out
}

func (k *Caps) rule(id string) *FilterRule {
	list := k.Filter
	if strings.HasPrefix(id, v6Prefix) {
		list = k.Filter6
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

// BuildPlan returns the ordered list of steps that Apply would run.
func BuildPlan(k *Caps, o Options) []Step {
	acts := buildActions(k, o)
	out := make([]Step, len(acts))
	for i, a := range acts {
		out[i] = a.Step
	}
	return out
}

var _ = poller.ErrAuth
var _ store.ManifestEntry
