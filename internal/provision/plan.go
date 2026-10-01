package provision

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/daniel/mtmon/internal/poller"
	"github.com/daniel/mtmon/internal/store"
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
const syslogAction = "mtmon-syslog"

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
		a = append(a, action{Step: Step{Key: "backup", Title: "Konfiguration sichern (/export)",
			Why:      "Vor jeder Änderung wird die komplette Router-Konfiguration als .rsc-Datei auf dem Router abgelegt (Files). Schlägt das fehl, ändert mtmon nichts.",
			Commands: []string{"/export file=mtmon-before-<zeit>"}, Undo: "nichts zu tun (nur eine Datei)", Risk: "none"},
			run: func(ctx context.Context, r *runner) error { return r.backup(ctx) }})
	}
	a = append(a, action{Step: Step{Key: "user", Title: "Read-only-Benutzer für mtmon anlegen",
		Why: "mtmon liest damit Status, WLAN-Clients, DHCP, ARP usw. Gruppe hat nur die Rechte read, api, rest-api (kein write, kein ssh/winbox/ftp) und der Login ist auf die IP von mtmon beschränkt.",
		Commands: []string{
			"/user group add name=" + monGroup + " policy=read,api,rest-api comment=" + ManagedTag,
			"/user add name=" + o.MonUser + " group=" + monGroup + " password=<zufällig, 24 Zeichen> address=" + ip + "/32 comment=" + ManagedTag},
		Undo: "Benutzer und Gruppe werden gelöscht", Risk: "low"},
		run: func(ctx context.Context, r *runner) error { return r.createUser(ctx, o) }})
	if o.Flow {
		a = append(a, action{Step: Step{Key: "flow", Title: "Traffic Flow (IPFIX) an mtmon exportieren",
			Why: "Das ist die Datenquelle für 'wer verbindet sich wohin': Quelle, Ziel, Ports, Bytes pro Verbindung. Aktive Flows werden alle 30 s gemeldet (Echtzeit). Bestehende Ziele/Collector bleiben unberührt.",
			Commands: []string{
				"/ip traffic-flow set enabled=yes interfaces=all cache-entries=16k active-flow-timeout=30s inactive-flow-timeout=15s",
				fmt.Sprintf("/ip traffic-flow target add dst-address=%s port=%d version=ipfix%s", ip, o.FlowPort, srcArg(o))},
			Undo: "Target wird gelöscht, die vorherigen Traffic-Flow-Einstellungen werden exakt wiederhergestellt", Risk: "low"},
			run: func(ctx context.Context, r *runner) error { return r.flow(ctx, k, o) }})
	}
	if o.Syslog {
		a = append(a, action{Step: Step{Key: "syslog", Title: "Firewall-Logs per Syslog an mtmon senden",
			Why: "Damit mtmon sieht, welche Firewall-Regel eine Verbindung geblockt oder durchgelassen hat. Es wird nur das Topic 'firewall' an mtmon gesendet; lokales Logging bleibt wie es ist.",
			Commands: []string{
				fmt.Sprintf("/system logging action add name=%s target=remote remote=%s remote-port=%d%s", syslogAction, ip, o.SyslogPort, srcArg(o)),
				"/system logging add topics=firewall action=" + syslogAction},
			Undo: "Logging-Regel und -Action werden gelöscht", Risk: "low"},
			run: func(ctx context.Context, r *runner) error { return r.syslog(ctx, o) }})
		if len(o.FwRules) > 0 {
			var cmds []string
			for _, id := range o.FwRules {
				if f := k.rule(id); f != nil {
					cmds = append(cmds, fmt.Sprintf("/ip firewall filter set %s log=yes log-prefix=%s   # %s %s %s", id, prefixFor(id), f.Chain, f.Action, f.Comment))
				}
			}
			a = append(a, action{Step: Step{Key: "fwlog", Title: fmt.Sprintf("Logging bei %d bestehenden Firewall-Regeln aktivieren", len(o.FwRules)),
				Why:      "Nur 'log' und 'log-prefix' dieser Regeln werden gesetzt (Bedingungen/Aktion/Reihenfolge bleiben unverändert). Der Prefix verrät mtmon, welche Regel getroffen hat. Gedropptes Zeug taucht so in mtmon auf, das Traffic Flow nie sieht.",
				Commands: cmds, Undo: "log und log-prefix jeder Regel werden auf den alten Wert zurückgesetzt", Risk: "medium"},
				run: func(ctx context.Context, r *runner) error { return r.fwLog(ctx, k, o) }})
		}
		if o.LogNew {
			a = append(a, action{Step: Step{Key: "lognew", Title: "Regel am Ende der forward-Chain: neue Verbindungen loggen",
				Why:      "Eine 'passthrough'-Regel ganz unten loggt jede NEUE Verbindung, die alle Drop-Regeln überlebt hat. Beweis: 'durchgelassen'. Kostet CPU/Log-Volumen auf dem Router.",
				Commands: []string{"/ip firewall filter add chain=forward action=passthrough connection-state=new log=yes log-prefix=MTM-NEW comment=\"" + ManagedTag + ": log new connections\""},
				Undo:     "Regel wird gelöscht", Risk: "medium"},
				run: func(ctx context.Context, r *runner) error { return r.logNew(ctx) }})
		}
	}
	if o.DisableFasttrack && k.Fasttrack {
		a = append(a, action{Step: Step{Key: "fasttrack", Title: "FastTrack-Regeln deaktivieren",
			Why:      "FastTrack umgeht große Teile des Pakettpfads; Traffic Flow sieht dann nur den Anfang jeder Verbindung. Ohne FastTrack zählt mtmon vollständig, der Router braucht aber mehr CPU.",
			Commands: []string{"/ip firewall filter disable [find where action=fasttrack-connection]"},
			Undo:     "Regeln werden wieder aktiviert", Risk: "medium"},
			run: func(ctx context.Context, r *runner) error { return r.fasttrack(ctx, k) }})
	}
	a = append(a, action{Step: Step{Key: "verify", Title: "Test: Login mit dem neuen Benutzer",
		Why:      "mtmon meldet sich mit dem neuen Read-only-Benutzer an. Klappt das nicht (z.B. falsche IP aus Sicht des Routers), wird automatisch alles zurückgebaut.",
		Commands: []string{"GET /rest/system/resource  (als " + o.MonUser + ")"}, Undo: "–", Risk: "none"},
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
