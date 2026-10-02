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
	FwRules          []string `json:"fw_rules"` // .id of filter rules to enable logging on
	LogNew           bool     `json:"log_new"`
	DisableFasttrack bool     `json:"disable_fasttrack"`
	SkipBackup       bool     `json:"skip_backup"`
	MonUser          string   `json:"mon_user"`
	DeviceAddr       string   `json:"device_addr"` // management address of the device; used as export source address
	MonPass          string   `json:"-"`
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
	return o
}

func srcArg(o Options) string {
	if a, err := netip.ParseAddr(o.DeviceAddr); err == nil && !a.IsLoopback() {
		return " src-address=" + a.String()
	}
	return ""
}

func prefixFor(id string) string { return "MTM-" + strings.TrimPrefix(id, "*") }

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
		if len(o.FwRules) > 0 {
			var cmds []string
			for _, id := range o.FwRules {
				if f := k.rule(id); f != nil {
					cmds = append(cmds, fmt.Sprintf("/ip firewall filter set %s log=yes log-prefix=%s   # %s %s %s", id, prefixFor(id), f.Chain, f.Action, f.Comment))
				}
			}
			a = append(a, action{Step: Step{Key: "fwlog", Title: fmt.Sprintf("Enable logging on %d existing firewall rules", len(o.FwRules)),
				Why:      "Only 'log' and 'log-prefix' of these rules are set (conditions, action and order stay unchanged). The prefix tells mtmon which rule matched. This way dropped traffic that Traffic Flow never sees shows up in mtmon.",
				Commands: cmds, Undo: "log and log-prefix of each rule are reset to their previous values", Risk: "medium"},
				run: func(ctx context.Context, r *runner) error { return r.fwLog(ctx, k, o) }})
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

func (k *Caps) rule(id string) *FilterRule {
	for i := range k.Filter {
		if k.Filter[i].ID == id {
			return &k.Filter[i]
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
