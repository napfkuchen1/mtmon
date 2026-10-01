// Package provision discovers what a MikroTik device can do (Probe), explains what mtmon would change
// (BuildPlan), applies it with a full change manifest and automatic rollback (Apply), and undoes it
// again on offboarding (Rollback). All router writes in mtmon live here.
package provision

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/daniel/mtmon/internal/poller"
)

type WifiIf struct {
	Name string `json:"name"`
	SSID string `json:"ssid,omitempty"`
	Band string `json:"band,omitempty"`
}

type FilterRule struct {
	ID        string `json:"id"`
	Chain     string `json:"chain"`
	Action    string `json:"action"`
	Comment   string `json:"comment"`
	Log       bool   `json:"log"`
	LogPrefix string `json:"log_prefix"`
	Disabled  bool   `json:"disabled"`
	Summary   string `json:"summary"`
	Managed   bool   `json:"managed"`
}

type Caps struct {
	Identity   string         `json:"identity"`
	Model      string         `json:"model"`
	Serial     string         `json:"serial"`
	Version    string         `json:"version"`
	Arch       string         `json:"arch"`
	CPUCount   int64          `json:"cpu_count"`
	MemTotal   int64          `json:"mem_total"`
	Firmware   string         `json:"firmware"`
	Packages   []string       `json:"packages"`
	WifiStack  string         `json:"wifi_stack"` // wifi | wireless | none
	WifiIfs    []WifiIf       `json:"wifi_ifs"`
	WifiClient int            `json:"wifi_clients"`
	CAP        bool           `json:"cap_mode"`    // device is a CAP managed by a CAPsMAN
	CAPsMAN    bool           `json:"capsman"`     // device runs the CAPsMAN manager
	IfaceTypes map[string]int `json:"iface_types"` // type → count
	Bridges    []string       `json:"bridges"`
	HWOffload  bool           `json:"hw_offload"`
	DHCPServer bool           `json:"dhcp_server"`
	NAT        bool           `json:"nat"`
	Fasttrack  bool           `json:"fasttrack"`
	Addresses  []string       `json:"addresses"`
	Flow       struct {
		Supported bool     `json:"supported"`
		Enabled   bool     `json:"enabled"`
		Ifaces    string   `json:"interfaces"`
		Targets   []string `json:"targets"`
	} `json:"traffic_flow"`
	WwwSSL      bool         `json:"www_ssl"`
	WwwSSLAddr  string       `json:"www_ssl_address"`
	LogActions  []string     `json:"log_actions"`
	Filter      []FilterRule `json:"filter_rules"`
	Role        string       `json:"role"` // suggested: router | ap | switch
	RoleWhy     string       `json:"role_why"`
	ExportsFlow bool         `json:"suggest_flow"` // this device should export Traffic Flow
	Warnings    []string     `json:"warnings"`
	Managed     bool         `json:"already_managed"` // carries mtmon-managed objects from an earlier setup
}

const ManagedTag = "mtmon-managed"

func get(ctx context.Context, c *poller.Client, path string) []poller.Row {
	rows, err := c.Get(ctx, path)
	if err != nil {
		return nil
	}
	return rows
}

// Probe reads everything needed for the capability summary. Only /system/resource is mandatory; every
// other query is best effort so a stripped-down device still produces a useful result.
func Probe(ctx context.Context, c *poller.Client) (*Caps, error) {
	res, err := c.Get(ctx, "/system/resource")
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, errors.New("device answered but /system/resource is empty - is this RouterOS 7?")
	}
	r := res[0]
	if !strings.HasPrefix(strings.TrimSpace(r["version"]), "7") {
		return nil, fmt.Errorf("RouterOS %q found - mtmon needs RouterOS 7 (REST API)", r["version"])
	}
	k := &Caps{Version: r["version"], Arch: r["architecture-name"], Model: r["board-name"],
		CPUCount: r.Int("cpu-count"), MemTotal: r.Int("total-memory"), IfaceTypes: map[string]int{}}
	if id := get(ctx, c, "/system/identity"); len(id) > 0 {
		k.Identity = id[0]["name"]
	}
	if rb := get(ctx, c, "/system/routerboard"); len(rb) > 0 {
		if m := rb[0]["model"]; m != "" {
			k.Model = m
		}
		k.Serial, k.Firmware = rb[0]["serial-number"], rb[0]["current-firmware"]
	}
	for _, p := range get(ctx, c, "/system/package") {
		if p["disabled"] != "true" {
			k.Packages = append(k.Packages, p["name"])
		}
	}
	sort.Strings(k.Packages)
	for _, i := range get(ctx, c, "/interface") {
		k.IfaceTypes[i["type"]]++
	}
	// Wi-Fi: RouterOS 7.13+ "wifi" package, or the legacy "wireless" package
	if w := get(ctx, c, "/interface/wifi"); len(w) > 0 {
		k.WifiStack = "wifi"
		cfg := map[string]poller.Row{}
		for _, x := range get(ctx, c, "/interface/wifi/configuration") {
			cfg[x["name"]] = x
		}
		for _, x := range w {
			wi := WifiIf{Name: x["name"], SSID: x["configuration.ssid"]}
			if wi.SSID == "" {
				wi.SSID = cfg[x["configuration"]]["ssid"]
			}
			wi.Band = x["channel.band"]
			if wi.Band == "" {
				wi.Band = x["band"]
			}
			k.WifiIfs = append(k.WifiIfs, wi)
		}
		k.WifiClient = len(get(ctx, c, "/interface/wifi/registration-table"))
		if cp := get(ctx, c, "/interface/wifi/cap"); len(cp) > 0 && (cp[0]["enabled"] == "yes" || cp[0]["enabled"] == "true") {
			k.CAP = true
		}
		if cm := get(ctx, c, "/interface/wifi/capsman"); len(cm) > 0 && (cm[0]["enabled"] == "yes" || cm[0]["enabled"] == "true") {
			k.CAPsMAN = true
		}
	} else if w := get(ctx, c, "/interface/wireless"); len(w) > 0 {
		k.WifiStack = "wireless"
		for _, x := range w {
			k.WifiIfs = append(k.WifiIfs, WifiIf{Name: x["name"], SSID: x["ssid"], Band: x["band"]})
		}
		k.Warnings = append(k.Warnings, "Altes „wireless“-Paket: WLAN-Clients liest mtmon nur aus dem neuen „wifi“-Paket (wifi-qcom). Clients werden weiter über DHCP/ARP/Bridge-Hosts gefunden, aber Signal, SSID und Band fehlen.")
	} else {
		k.WifiStack = "none"
	}
	for _, b := range get(ctx, c, "/interface/bridge") {
		k.Bridges = append(k.Bridges, b["name"])
	}
	for _, p := range get(ctx, c, "/interface/bridge/port") {
		if p["hw"] == "true" {
			k.HWOffload = true
		}
	}
	k.DHCPServer = len(get(ctx, c, "/ip/dhcp-server")) > 0
	for _, a := range get(ctx, c, "/ip/address") {
		if a["disabled"] != "true" {
			k.Addresses = append(k.Addresses, a["address"]+" on "+a["interface"])
		}
	}
	for _, n := range get(ctx, c, "/ip/firewall/nat") {
		if n["chain"] == "srcnat" && n["disabled"] != "true" && (n["action"] == "masquerade" || n["action"] == "src-nat") {
			k.NAT = true
		}
	}
	for _, f := range get(ctx, c, "/ip/firewall/filter") {
		fr := FilterRule{ID: f[".id"], Chain: f["chain"], Action: f["action"], Comment: f["comment"],
			Log: f["log"] == "true" || f["log"] == "yes", LogPrefix: f["log-prefix"], Disabled: f["disabled"] == "true"}
		fr.Managed = strings.HasPrefix(fr.Comment, ManagedTag)
		fr.Summary = ruleSummary(f)
		if fr.Action == "fasttrack-connection" && !fr.Disabled {
			k.Fasttrack = true
		}
		if fr.Managed {
			k.Managed = true
		}
		k.Filter = append(k.Filter, fr)
	}
	// Traffic Flow
	if tf := get(ctx, c, "/ip/traffic-flow"); len(tf) > 0 {
		k.Flow.Supported = true
		k.Flow.Enabled = tf[0]["enabled"] == "true" || tf[0]["enabled"] == "yes"
		k.Flow.Ifaces = tf[0]["interfaces"]
	}
	for _, t := range get(ctx, c, "/ip/traffic-flow/target") {
		k.Flow.Targets = append(k.Flow.Targets, t["dst-address"]+" ("+t["version"]+")")
	}
	for _, s := range get(ctx, c, "/ip/service") {
		if s["name"] == "www-ssl" {
			k.WwwSSL = s["disabled"] != "true"
			k.WwwSSLAddr = s["address"]
		}
	}
	for _, a := range get(ctx, c, "/system/logging/action") {
		if a["target"] == "remote" {
			k.LogActions = append(k.LogActions, a["name"]+" → "+a["remote"])
		}
	}
	for _, u := range get(ctx, c, "/user") {
		if strings.HasPrefix(u["comment"], ManagedTag) {
			k.Managed = true
		}
	}
	k.suggestRole()
	return k, nil
}

func ruleSummary(f poller.Row) string {
	var p []string
	for _, key := range []string{"connection-state", "protocol", "in-interface", "in-interface-list", "out-interface", "out-interface-list",
		"src-address", "dst-address", "dst-port", "src-address-list", "dst-address-list", "connection-nat-state"} {
		if v := f[key]; v != "" {
			p = append(p, key+"="+v)
		}
	}
	if len(p) == 0 {
		return "all packets"
	}
	return strings.Join(p, " ")
}

func (k *Caps) suggestRole() {
	switch {
	case k.DHCPServer || k.NAT:
		k.Role, k.RoleWhy = "router", "hat DHCP-Server und/oder NAT"
		k.ExportsFlow = k.Flow.Supported
	case k.WifiStack != "none" && len(k.WifiIfs) > 0:
		k.Role, k.RoleWhy = "ap", "hat WLAN-Interfaces, aber weder DHCP-Server noch NAT"
	default:
		k.Role, k.RoleWhy = "switch", "keine Routing-Dienste und kein WLAN"
	}
	if k.Role != "router" && k.Flow.Supported {
		k.Warnings = append(k.Warnings, "Access Points und Switches bridgen Traffic meist in Hardware – Traffic Flow ist dort unvollständig und zählt teils doppelt. mtmon richtet Traffic Flow deshalb nur auf Routern ein; das Gerät wird trotzdem überwacht (Status, WLAN-Clients, Interfaces).")
	}
	if k.Fasttrack {
		k.Warnings = append(k.Warnings, "FastTrack ist aktiv: beschleunigte Pakete umgehen große Teile des Pakettpfads, Traffic Flow kann dadurch zu wenig zählen. Optional kann FastTrack deaktiviert werden (mehr CPU-Last).")
	}
	if k.HWOffload && k.Role == "router" {
		k.Warnings = append(k.Warnings, "Bridge-Hardware-Offload ist an: vom Switch-Chip weitergeleiteter LAN-zu-LAN-Traffic wird nicht exportiert (Internet-Traffic über die Router-CPU schon).")
	}
	if !k.WwwSSL {
		k.Warnings = append(k.Warnings, "www-ssl (HTTPS/REST) ist am Gerät deaktiviert – mtmon braucht es zum Überwachen.")
	}
	if k.Managed {
		k.Warnings = append(k.Warnings, "Auf diesem Gerät liegen schon Objekte mit dem Tag „mtmon-managed“ (frühere Einrichtung). Erst Offboarding bzw. Aufräumen, sonst gibt es Duplikate.")
	}
}
