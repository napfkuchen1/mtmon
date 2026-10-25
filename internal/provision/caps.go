// Package provision discovers what a MikroTik device can do (Probe), explains what mtmon would change
// (BuildPlan), applies it with a full change manifest and automatic rollback (Apply), and undoes it
// again on offboarding (Rollback). All router writes in mtmon live here.
package provision

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/poller"
)

type WifiIf struct {
	Name string `json:"name"`
	SSID string `json:"ssid,omitempty"`
	Band string `json:"band,omitempty"`
	// Channel is the configured frequency/channel as the router reports it (e.g. "5180" or "auto"); the field names
	// differ between RouterOS builds, so it is taken from whichever of them is present.
	Channel string `json:"channel,omitempty"`
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
	Proto     string `json:"proto,omitempty"`
	DPort     string `json:"dport,omitempty"`
	Packets   int64  `json:"packets,omitempty"`
	Counted   bool   `json:"counted,omitempty"` // Packets was read from the router (older stored probes have no counter)
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
	WwwSSL     bool         `json:"www_ssl"`
	WwwSSLAddr string       `json:"www_ssl_address"`
	LogActions []string     `json:"log_actions"`
	Filter     []FilterRule `json:"filter_rules"`
	// DNSRedirect: active dst-nat/redirect rules that send port-53 traffic to the router (hit counters from the probe).
	DNSRedirect struct {
		UDP        bool  `json:"udp"`
		TCP        bool  `json:"tcp"`
		UDPPackets int64 `json:"udp_packets"`
		TCPPackets int64 `json:"tcp_packets"`
	} `json:"dns_redirect"`
	Role        string   `json:"role"` // suggested: router | ap | switch
	RoleWhy     string   `json:"role_why"`
	ExportsFlow bool     `json:"suggest_flow"` // this device should export Traffic Flow
	Warnings    []string `json:"warnings"`
	Managed     bool     `json:"already_managed"`     // carries mtmon-managed objects from an earlier setup
	ProbedAt    int64    `json:"probed_at,omitempty"` // unix time the router was read
	Files       Files    `json:"files"`               // newest backup / export file (read-only /file listing)
	Certs       []Cert   `json:"certs"`               // certificates that services use (www-ssl, api-ssl) and when they expire
	Update      Update   `json:"update"`              // RouterOS update state as the router last checked
	// Export is what one POST /rest/export with the monitoring login looked like (#46): decides whether a
	// config-drift view can work. Only counts and flags, never the text. Refreshed once a day.
	Export ExportProbe `json:"export_probe"`
	// IPv6: a DHCPv6 client asks the ISP for a prefix (dynamic prefix), and the lifetimes the router advertises in RAs
	// ("/ipv6 nd prefix default"). Seconds, 0 = not readable.
	IPv6PD      bool  `json:"ipv6_pd"`
	RAValid     int64 `json:"ra_valid_s,omitempty"`
	RAPreferred int64 `json:"ra_preferred_s,omitempty"`
}

// ExportProbe is poller.ExportResult plus the time it was taken (unix, 0 = never probed).
type ExportProbe struct {
	poller.ExportResult
	At int64 `json:"at,omitempty"`
}

// Cert is a certificate a router service uses; Expires is a unix time (0 = unknown).
type Cert struct {
	Name    string `json:"name"`
	Service string `json:"service"`
	Expires int64  `json:"expires"`
}

// Update is the result of the router's last "check for updates" (RouterOS only fills it after such a check).
type Update struct {
	Channel   string `json:"channel,omitempty"`
	Installed string `json:"installed,omitempty"`
	Latest    string `json:"latest,omitempty"`
}

const ManagedTag = "mtmon-managed"

// firstOf returns the first non-empty value among the given field names.
func firstOf(r poller.Row, keys ...string) string {
	for _, k := range keys {
		if v := r[k]; v != "" {
			return v
		}
	}
	return ""
}

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
		CPUCount: r.Int("cpu-count"), MemTotal: r.Int("total-memory"), IfaceTypes: map[string]int{}, ProbedAt: time.Now().Unix()}
	k.Packages, k.WifiIfs, k.Bridges, k.Addresses, k.LogActions, k.Filter, k.Warnings = []string{}, []WifiIf{}, []string{}, []string{}, []string{}, []FilterRule{}, []string{}
	k.Flow.Targets = []string{}
	k.Certs = []Cert{}
	if rows, err := c.Get(ctx, "/file"); err == nil { // a read-only user may be refused here: then nothing is claimed
		k.Files = parseFiles(rows)
	}
	for _, r := range get(ctx, c, "/ipv6/dhcp-client") { // absent without the ipv6 package
		if !r.Bool("disabled") && strings.Contains(r["request"], "prefix") {
			k.IPv6PD = true
		}
	}
	if nd := get(ctx, c, "/ipv6/nd/prefix/default"); len(nd) > 0 {
		k.RAValid = int64(poller.ParseDuration(nd[0]["valid-lifetime"]).Seconds())
		k.RAPreferred = int64(poller.ParseDuration(nd[0]["preferred-lifetime"]).Seconds())
	}
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
			wi.Channel = firstOf(x, "channel.frequency", "frequency")
			if wi.Channel == "" {
				wi.Channel = firstOf(cfg[x["configuration"]], "channel.frequency", "frequency")
			}
			if w := firstOf(x, "channel.width"); w != "" && wi.Channel != "" {
				wi.Channel += " / " + w
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
			k.WifiIfs = append(k.WifiIfs, WifiIf{Name: x["name"], SSID: x["ssid"], Band: x["band"], Channel: firstOf(x, "frequency")})
		}
		k.Warnings = append(k.Warnings, "Legacy “wireless” package: mtmon only reads Wi-Fi clients from the new “wifi” package (wifi-qcom). Clients are still found via DHCP/ARP/bridge hosts, but signal, SSID and band are missing.")
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
	for _, a := range get(ctx, c, "/ipv6/address") {
		if a["disabled"] != "true" && a["address"] != "" {
			k.Addresses = append(k.Addresses, a["address"]+" on "+a["interface"])
		}
	}
	for _, n := range get(ctx, c, "/ip/firewall/nat") {
		if n["chain"] == "srcnat" && n["disabled"] != "true" && (n["action"] == "masquerade" || n["action"] == "src-nat") {
			k.NAT = true
		}
		if n["chain"] == "dstnat" && n["disabled"] != "true" && (n["action"] == "dst-nat" || n["action"] == "redirect") && portListHas(n["dst-port"], "53") {
			pk, _ := strconv.ParseInt(n["packets"], 10, 64)
			switch n["protocol"] {
			case "udp":
				k.DNSRedirect.UDP, k.DNSRedirect.UDPPackets = true, k.DNSRedirect.UDPPackets+pk
			case "tcp":
				k.DNSRedirect.TCP, k.DNSRedirect.TCPPackets = true, k.DNSRedirect.TCPPackets+pk
			}
		}
	}
	for _, f := range get(ctx, c, "/ip/firewall/filter") {
		if f["dynamic"] == "true" || f["dynamic"] == "yes" { // RouterOS: "can't edit dynamic object"
			continue
		}
		fr := FilterRule{ID: f[".id"], Chain: f["chain"], Action: f["action"], Comment: f["comment"],
			Log: f["log"] == "true" || f["log"] == "yes", LogPrefix: f["log-prefix"], Disabled: f["disabled"] == "true"}
		fr.Managed = strings.HasPrefix(fr.Comment, ManagedTag)
		fr.Summary = ruleSummary(f)
		fr.Proto, fr.DPort = f["protocol"], f["dst-port"]
		if n, err := strconv.ParseInt(f["packets"], 10, 64); err == nil {
			fr.Packets, fr.Counted = n, true
		}
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
	svcCert := map[string]string{} // service -> certificate name
	for _, s := range get(ctx, c, "/ip/service") {
		if s["name"] == "www-ssl" {
			k.WwwSSL = s["disabled"] != "true"
			k.WwwSSLAddr = s["address"]
		}
		if (s["name"] == "www-ssl" || s["name"] == "api-ssl") && s["disabled"] != "true" && s["certificate"] != "" && s["certificate"] != "none" {
			svcCert[s["name"]] = s["certificate"]
		}
	}
	if len(svcCert) > 0 {
		k.Certs = parseCerts(svcCert, get(ctx, c, "/certificate"))
	}
	if u := get(ctx, c, "/system/package/update"); len(u) > 0 {
		k.Update = Update{Channel: u[0]["channel"], Installed: u[0]["installed-version"], Latest: u[0]["latest-version"]}
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
	if k.Flow.Targets == nil {
		k.Flow.Targets = []string{}
	}
	if k.Filter == nil {
		k.Filter = []FilterRule{}
	}
	k.suggestRole()
	return k, nil
}

// portListHas reports whether a RouterOS port list ("53", "53,5353", "50-60") contains the port.
func portListHas(list, port string) bool {
	p, err := strconv.Atoi(port)
	if err != nil {
		return false
	}
	for _, it := range strings.Split(list, ",") {
		it = strings.TrimSpace(it)
		if lo, hi, ok := strings.Cut(it, "-"); ok {
			a, e1 := strconv.Atoi(lo)
			b, e2 := strconv.Atoi(hi)
			if e1 == nil && e2 == nil && a <= p && p <= b {
				return true
			}
		} else if it == port {
			return true
		}
	}
	return false
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
		k.Role, k.RoleWhy = "router", "has a DHCP server and/or NAT"
		k.ExportsFlow = k.Flow.Supported
	case k.WifiStack != "none" && len(k.WifiIfs) > 0:
		k.Role, k.RoleWhy = "ap", "has Wi-Fi interfaces, but neither a DHCP server nor NAT"
	default:
		k.Role, k.RoleWhy = "switch", "no routing services and no Wi-Fi"
	}
	if k.Role != "router" && k.Flow.Supported {
		k.Warnings = append(k.Warnings, "Access points and switches usually bridge traffic in hardware – Traffic Flow is incomplete there and partly double-counts. That is why mtmon only sets up Traffic Flow on routers; the device is still monitored (status, Wi-Fi clients, interfaces).")
	}
	if k.Fasttrack {
		k.Warnings = append(k.Warnings, "FastTrack is active: accelerated packets bypass large parts of the packet path, so Traffic Flow may undercount. FastTrack can optionally be disabled (higher CPU load).")
	}
	if k.HWOffload && k.Role == "router" {
		k.Warnings = append(k.Warnings, "Bridge hardware offload is on: LAN-to-LAN traffic forwarded by the switch chip is not exported (internet traffic via the router CPU is).")
	}
	if !k.WwwSSL {
		k.Warnings = append(k.Warnings, "www-ssl (HTTPS/REST) is disabled on the device – mtmon needs it for monitoring.")
	}
	if k.Managed {
		k.Warnings = append(k.Warnings, "This device already holds objects tagged “mtmon-managed” (earlier setup). Run offboarding or clean up first, otherwise duplicates will be created.")
	}
}
