package advice

import (
	"fmt"
	"sort"
	"strings"
)

var portName = map[int]string{21: "FTP", 23: "Telnet", 139: "SMB", 445: "SMB", 3389: "RDP", 5900: "VNC"}

const minConns = 3 // connections in 24 h before a protocol use is worth mentioning

type pairKey struct{ mac, dst string }

// useLines turns port rows into short evidence lines "client → destination: n connections, size".
func useLines(idx map[string]Client, rows []PortUse, max int) []string {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Flows > rows[j].Flows })
	var out []string
	for i, r := range rows {
		if i >= max {
			out = append(out, fmt.Sprintf("…(+%d)", len(rows)-max))
			break
		}
		dst := r.RIP
		if r.Host != "" {
			dst = r.Host + " (" + r.RIP + ")"
		}
		out = append(out, fmt.Sprintf("%s → %s: %d connections, %s", nameOf(idx, r.MAC), dst, r.Flows, humanBytes(r.Bytes)))
	}
	return out
}

func portRows(s Snapshot, ports ...int) []PortUse {
	var out []PortUse
	for _, r := range s.PortUse24h() {
		for _, p := range ports {
			if r.Port == p {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

func ruleCleartext(s Snapshot) ([]Suggestion, string) {
	if len(s.Traffic24h()) == 0 {
		return nil, "no flow data in the last 24 h"
	}
	idx := clientIndex(s)
	var out []Suggestion
	for _, p := range []struct {
		port                                      int
		name, subj, why, step, titlePub, titleLAN string
	}{
		{23, "Telnet", "telnet", "Telnet sends everything, including passwords, as readable text. Anyone on the path between the two ends can read it.",
			"Find out which program or device talks Telnet and switch it to SSH if it supports that.",
			"Telnet connections to the internet were seen", "Telnet connections inside your network were seen"},
		{21, "FTP", "ftp", "FTP sends user names, passwords and files as readable text. Anyone on the path between the two ends can read or change them.",
			"Find out which program or device talks FTP and switch it to SFTP or FTPS if it supports that.",
			"FTP connections to the internet were seen", "FTP connections inside your network were seen"},
	} {
		var rows []PortUse
		pub := false
		for _, r := range portRows(s, p.port) {
			if r.Proto != 6 || r.Flows < minConns {
				continue
			}
			rows = append(rows, r)
			pub = pub || IsPublic(r.RIP)
		}
		if len(rows) == 0 {
			continue
		}
		sev, title := SevInfo, p.titleLAN
		if pub {
			sev, title = SevWarning, p.titlePub
		}
		out = append(out, Suggestion{
			Subject: p.subj, Severity: sev, Category: CatSecurity, Link: "insights/ports", Confidence: "high",
			Title: title,
			Why:   p.why,
			Evidence: append([]string{fmt.Sprintf("%d client/destination pairs used %s (port %d) in the last 24 h", len(rows), p.name, p.port)},
				useLines(idx, rows, 5)...),
			Steps: []string{
				p.step,
				"If it is an old device that cannot do better, keep it in a separate network and do not allow it to reach the internet.",
			},
		})
	}
	return out, ""
}

func ruleSMB(s Snapshot) ([]Suggestion, string) {
	if len(s.Traffic24h()) == 0 {
		return nil, "no flow data in the last 24 h"
	}
	var rows []PortUse
	for _, r := range portRows(s, 445, 139) {
		if r.Proto == 6 && r.Flows >= minConns && IsPublic(r.RIP) {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return nil, ""
	}
	idx := clientIndex(s)
	return []Suggestion{{
		Severity: SevWarning, Category: CatSecurity, Link: "insights/ports", Confidence: "high",
		Title: "Windows file sharing (SMB) traffic is leaving for the internet",
		Why:   "SMB is made for local networks. Over the internet it is a common target for attacks, and old SMB versions (SMBv1) are insecure. Normally a client never needs to talk SMB to an internet address; when it does, it is a misconfiguration or an unwanted program.",
		Evidence: append([]string{fmt.Sprintf("%d client/destination pairs used ports 445/139 towards public addresses in the last 24 h", len(rows))},
			useLines(idx, rows, 5)...),
		Steps: []string{
			"Check the listed clients for the program that opens the connection (a mapped network drive that points to a public address is typical).",
			"Block SMB towards the internet in the forward chain; local file sharing is not affected.",
		},
		Commands: "/ip firewall filter add chain=forward action=drop protocol=tcp dst-port=139,445 out-interface-list=WAN comment=\"no SMB to internet\"",
		Limits:   "mtmon sees the connection, not the SMB version. The command assumes the default interface list WAN.",
	}}, ""
}

func ruleRDPOut(s Snapshot) ([]Suggestion, string) {
	if len(s.Traffic24h()) == 0 {
		return nil, "no flow data in the last 24 h"
	}
	var rows []PortUse
	for _, r := range portRows(s, 3389) {
		if r.Proto == 6 && r.Flows >= minConns && IsPublic(r.RIP) {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return nil, ""
	}
	idx := clientIndex(s)
	return []Suggestion{{
		Severity: SevInfo, Category: CatSecurity, Link: "insights/ports", Confidence: "high",
		Title: "Remote Desktop (RDP) connections to the internet were seen",
		Why:   "Remote Desktop is fine when you connect to your own machines on purpose, but an RDP service that is directly reachable from the internet is one of the most attacked doors there is. A VPN in front of it is much safer.",
		Evidence: append([]string{fmt.Sprintf("%d client/destination pairs used RDP (port 3389) towards public addresses in the last 24 h", len(rows))},
			useLines(idx, rows, 5)...),
		Steps: []string{
			"If this is expected (for example remote work), nothing needs to change; consider a VPN to the target network instead of exposing RDP.",
			"If nobody here uses Remote Desktop, find out which program created these connections.",
		},
	}}, ""
}

var iotVendor = []string{"espressif", "tuya", "shelly", "sonos", "xiaomi", "hikvision", "dahua", "reolink", "wyze", "ring", "nest", "signify", "philips lighting", "ikea", "lumi", "yeelight", "amazon", "tasmota", "hangzhou", "shenzhen", "foscam", "amcrest"}
var iotHost = []string{"esp", "cam", "plug", "bulb", "sensor", "thermostat", "iot", "tasmota", "shelly", "ipcam", "nvr", "echo", "alexa", "tv"}

func iotLike(c Client) bool {
	v := strings.ToLower(c.Vendor)
	for _, k := range iotVendor {
		if strings.Contains(v, k) {
			return true
		}
	}
	h := strings.ToLower(c.Hostname + " " + c.Label)
	for _, k := range iotHost {
		if strings.Contains(h, k) {
			return true
		}
	}
	return false
}

func ruleHTTPIoT(s Snapshot) ([]Suggestion, string) {
	tr := s.Traffic24h()
	if len(tr) == 0 {
		return nil, "no flow data in the last 24 h"
	}
	tot := map[string]int64{}
	for _, t := range tr {
		tot[t.MAC] = t.Up + t.Down
	}
	idx := clientIndex(s)
	bytes := map[string]int64{}
	flows := map[string]int64{}
	dest := map[string]map[string]bool{}
	for _, r := range portRows(s, 80) {
		if r.Proto != 6 || !IsPublic(r.RIP) {
			continue
		}
		bytes[r.MAC] += r.Bytes
		flows[r.MAC] += r.Flows
		if dest[r.MAC] == nil {
			dest[r.MAC] = map[string]bool{}
		}
		d := r.Host
		if d == "" {
			d = r.RIP
		}
		dest[r.MAC][d] = true
	}
	var lines []string
	n := 0
	for mac, b := range bytes {
		c, ok := idx[mac]
		if !ok || !iotLike(c) || b < 50_000 || tot[mac] == 0 || float64(b)/float64(tot[mac]) < 0.5 {
			continue
		}
		n++
		var ds []string
		for d := range dest[mac] {
			ds = append(ds, d)
		}
		sort.Strings(ds)
		lines = append(lines, fmt.Sprintf("%s: %s%% of its internet traffic is unencrypted HTTP (%s, %s)", c.Display(), pct(float64(b)*100/float64(tot[mac])), humanBytes(b), list(ds, 3)))
	}
	if n == 0 {
		return nil, ""
	}
	sort.Strings(lines)
	return []Suggestion{{
		Severity: SevInfo, Category: CatSecurity, Link: "insights/ports", Confidence: "medium",
		Title:    "Smart-home or camera devices talk unencrypted HTTP to the internet",
		Why:      "Plain HTTP can be read and modified on its way. For gadgets such as cameras, plugs or sensors this can expose video, status data or firmware updates. Often only the vendor can fix it, but you can limit the damage.",
		Evidence: append([]string{fmt.Sprintf("%d devices that look like IoT gadgets send mostly HTTP (port 80) to the internet", n)}, lines...),
		Steps: []string{
			"Check the device's app or web page for a setting that enables HTTPS or cloud encryption, and install pending firmware updates.",
			"Put such devices into their own network or VLAN and allow only what they need.",
			"If a device is a camera, consider disabling its cloud access and using it only locally.",
		},
		Limits: "\"IoT-like\" is guessed from the vendor and host name. Port 80 is also used by harmless update checks.",
	}}, ""
}

func ruleInbound(s Snapshot) ([]Suggestion, string) {
	in := s.InboundRecent()
	if len(in) == 0 {
		return nil, ""
	}
	idx := clientIndex(s)
	var out []Suggestion
	for _, r := range in {
		name, ok := portName[r.Port]
		if !ok || r.Flows < 2 || r.Proto != 6 {
			continue
		}
		who := nameOf(idx, r.MAC)
		if r.MAC == "" {
			who = r.IP
		}
		out = append(out, Suggestion{
			Subject: fmt.Sprintf("%s-%d", r.MAC, r.Port), Severity: SevWarning, Category: CatSecurity, Link: "client/" + r.MAC, Confidence: "medium",
			Title: fmt.Sprintf("%s on %s is being contacted from outside", name, who),
			Why:   "Services such as remote desktop, file sharing, Telnet and FTP are constantly scanned and attacked when they are reachable from the internet. If this is a port forward you set up on purpose it should at least be restricted; if not, it points to an unwanted rule or UPnP mapping.",
			Evidence: []string{
				fmt.Sprintf("%d connections from %d remote hosts reached port %d (%s) on %s in the last hour", r.Flows, r.Hosts, r.Port, name, who),
			},
			Steps: []string{
				"Look at the NAT rules (dst-nat) and the UPnP list for a forward to this port and remove it if it is not needed.",
				"If it is needed, allow only known source addresses or use a VPN instead.",
			},
			Commands: "/ip firewall nat print where chain=dstnat\n/ip upnp print\n/ip upnp mappings print",
			Limits:   "Recognised from flow records of the last hour: remote hosts that opened connections to this port.",
		})
	}
	return out, ""
}

// ---- firewall ----

const (
	OriginMinHits  = 150
	OriginShareCC  = 0.40
	OriginShareASN = 0.30
	OriginMinTotal = 300
	UnusedRuleHist = 7 * 24 // hours of firewall history before "never matches" is credible
)

func ruleBlockOrigin(s Snapshot) ([]Suggestion, string) {
	bs := s.BlockedSources24h()
	if len(bs) == 0 {
		return nil, "no blocked inbound events with a logging drop rule in the last 24 h"
	}
	var total int64
	known := false
	for _, b := range bs {
		if !IsPublic(b.IP) {
			continue
		}
		total += b.Hits
		known = known || b.CC != "" || b.ASN != 0
	}
	if !known {
		return nil, "no GeoIP/ASN database configured"
	}
	if total < OriginMinTotal {
		return nil, ""
	}
	type grp struct {
		key, label string
		hits       int64
		ips        []BlockedSrc
	}
	byCC, byASN := map[string]*grp{}, map[int]*grp{}
	for _, b := range bs {
		if !IsPublic(b.IP) {
			continue
		}
		if b.CC != "" {
			g := byCC[b.CC]
			if g == nil {
				g = &grp{key: b.CC, label: b.CC}
				byCC[b.CC] = g
			}
			g.hits += b.Hits
			g.ips = append(g.ips, b)
		}
		if b.ASN != 0 {
			g := byASN[b.ASN]
			if g == nil {
				g = &grp{key: fmt.Sprint(b.ASN), label: fmt.Sprintf("AS%d %s", b.ASN, b.ASOrg)}
				byASN[b.ASN] = g
			}
			g.hits += b.Hits
			g.ips = append(g.ips, b)
		}
	}
	mk := func(kind string, g *grp, share float64) Suggestion {
		sort.Slice(g.ips, func(i, j int) bool { return g.ips[i].Hits > g.ips[j].Hits })
		var tops, cmds []string
		for i, b := range g.ips {
			if i >= 5 {
				break
			}
			tops = append(tops, fmt.Sprintf("%s (%d)", b.IP, b.Hits))
			cmds = append(cmds, fmt.Sprintf("/ip firewall address-list add list=blocked-scanners address=%s timeout=7d comment=\"mtmon suggestion\"", b.IP))
		}
		cmds = append(cmds, "/ip firewall raw add chain=prerouting action=drop src-address-list=blocked-scanners in-interface-list=WAN comment=\"drop known scanners early\"")
		return Suggestion{
			Subject: kind + "-" + strings.ToLower(g.key), Severity: SevInfo, Category: CatSecurity, Link: "firewall", Confidence: "medium",
			Title: fmt.Sprintf("Most blocked attempts come from %s", g.label),
			Why:   "Your firewall already blocks these attempts, so this is not an emergency. But a large share from one place means automated scanning, and dropping known sources very early (in the raw table) saves router CPU and keeps the logs readable.",
			Evidence: []string{
				fmt.Sprintf("%d blocked inbound events came from %s in the last 24 h (%s%% of all blocked events with a known source)", g.hits, g.label, pct(share*100)),
				fmt.Sprintf("Busiest sources: %s", list(tops, 5)),
			},
			Steps: []string{
				"Add the busiest sources to an address list and drop them in the raw table, as in the commands below (they expire after 7 days).",
				"RouterOS has no built-in country filter. For a whole country or provider you would need a maintained list of its address ranges.",
			},
			Commands: strings.Join(cmds, "\n"),
			Limits:   "Based on the most recent blocked events (at most 60,000 within 24 h) and the 300 busiest sources. The commands assume the default interface list WAN.",
		}
	}
	var out []Suggestion
	tf := float64(total)
	for _, g := range byCC {
		if g.hits >= OriginMinHits && float64(g.hits)/tf >= OriginShareCC {
			out = append(out, mk("cc", g, float64(g.hits)/tf))
		}
	}
	for _, g := range byASN {
		if g.hits >= OriginMinHits && float64(g.hits)/tf >= OriginShareASN {
			out = append(out, mk("asn", g, float64(g.hits)/tf))
		}
	}
	return out, ""
}

func ruleMgmtOpen(s Snapshot) ([]Suggestion, string) {
	var out []Suggestion
	seen := false
	for _, d := range s.Devices() {
		if d.Caps == nil || d.Role != "router" {
			continue
		}
		seen = true
		if !d.Caps.WwwSSL || strings.TrimSpace(d.Caps.WwwSSLAddr) != "" {
			continue
		}
		guarded := false
		for _, f := range d.Caps.Filter {
			if f.Chain == "input" && f.Action == "drop" && !f.Disabled {
				guarded = true
			}
		}
		if guarded {
			continue
		}
		out = append(out, Suggestion{
			Subject: d.Name, Severity: SevTip, Category: CatSecurity, Link: "device/" + d.Name, Confidence: "medium",
			Title: fmt.Sprintf("The web interface of %s is not restricted to your network", d.Name),
			Why:   "The HTTPS service of the router (used by mtmon) accepts connections from any address, and no firewall rule dropping unwanted input traffic was found. If the router has a public address, its login page could be reachable from the internet.",
			Evidence: []string{
				fmt.Sprintf("Service www-ssl on %s has no address restriction", d.Name),
				"No enabled drop rule in the input chain was found in the stored capabilities",
			},
			Steps: []string{
				"Restrict the management services to your LAN and to the address where mtmon runs (keep mtmon's address in the list, otherwise monitoring stops).",
				"Make sure the input chain ends with a drop rule for everything from the WAN side.",
			},
			Commands: "/ip service print\n# example: replace with your LAN and the mtmon host\n/ip service set www-ssl address=192.168.88.0/24\n/ip service disable [find name=telnet]\n/ip service disable [find name=ftp]\n/ip service disable [find name=www]",
			Limits:   "Based on the capabilities stored when the device was set up. It cannot tell whether the router is reachable from the internet at all.",
		})
	}
	if !seen {
		return nil, "no stored router capabilities"
	}
	return out, ""
}

func ruleUnusedRules(s Snapshot) ([]Suggestion, string) {
	rs := s.FwRules()
	if len(rs) == 0 {
		return nil, "no firewall rules with log prefix known"
	}
	if s.FwHistory().Hours() < UnusedRuleHist {
		return nil, "less than 7 days of firewall log history"
	}
	active := map[string]bool{}
	for _, r := range rs {
		if r.Hits7d > 0 {
			active[r.Device] = true
		}
	}
	by := map[string][]FwRule{}
	for _, r := range rs {
		if !r.Managed && r.Hits7d == 0 && active[r.Device] {
			by[r.Device] = append(by[r.Device], r)
		}
	}
	var out []Suggestion
	for dev, list0 := range by {
		var ev []string
		for _, r := range list0 {
			ev = append(ev, strings.TrimSpace(fmt.Sprintf("%s %s %s", r.Chain, r.Action, r.Descr)))
		}
		sort.Strings(ev)
		out = append(out, Suggestion{
			Subject: dev, Severity: SevInfo, Category: CatHygiene, Link: "firewall", Confidence: "medium",
			Title: fmt.Sprintf("Some logging firewall rules on %s never matched", dev),
			Why:   "A rule that has not matched for a week may be obsolete, shadowed by an earlier rule, or aimed at traffic that no longer exists. Cleaning those up keeps the rule set easy to understand and a little faster.",
			Evidence: []string{
				fmt.Sprintf("%d logging rules produced no log event in the last 7 days while other rules on %s did", len(list0), dev),
				fmt.Sprintf("Rules: %s", list(ev, 4)),
			},
			Steps: []string{
				"Open the rule list in Winbox and check the packet counters of these rules. A counter of 0 confirms that they never match.",
				"Disable such a rule for a few days before deleting it.",
			},
			Commands: "/ip firewall filter print stats where log=yes",
			Limits:   "Only rules that write log lines can be judged; rules without logging are not visible to mtmon.",
		})
	}
	return out, ""
}

func ruleDropNoLog(s Snapshot) ([]Suggestion, string) {
	var out []Suggestion
	seen := false
	for _, d := range s.Devices() {
		if d.Caps == nil || len(d.Caps.Filter) == 0 {
			continue
		}
		seen = true
		var nolog []string
		drops := 0
		for _, f := range d.Caps.Filter {
			if f.Disabled || (f.Action != "drop" && f.Action != "reject") {
				continue
			}
			drops++
			if !f.Log {
				txt := f.Chain + " " + f.Action
				if f.Comment != "" {
					txt += " (" + f.Comment + ")"
				} else if f.Summary != "" {
					txt += " (" + f.Summary + ")"
				}
				nolog = append(nolog, txt)
			}
		}
		if len(nolog) == 0 {
			continue
		}
		out = append(out, Suggestion{
			Subject: d.Name, Severity: SevInfo, Category: CatMonitoring, Link: "firewall", Confidence: "high",
			Title: fmt.Sprintf("Blocked traffic on %s is not logged", d.Name),
			Why:   "A drop rule without logging blocks silently. The Firewall page of mtmon can then not show what was blocked, who tried, and whether a rule works as intended. You do not need to log everything, but the final catch-all drop rules are worth it.",
			Evidence: []string{
				fmt.Sprintf("%d of %d active drop/reject rules on %s have logging switched off", len(nolog), drops, d.Name),
				fmt.Sprintf("Rules: %s", list(nolog, 4)),
			},
			Steps: []string{
				"Enable logging with a short prefix on the rules you want to see, preferably the last drop rule of the input and forward chains.",
				"Make sure the router sends firewall logs to mtmon (see the suggestion about syslog if present).",
			},
			Commands: "/ip firewall filter print where (action=drop or action=reject) and log=no\n# then for a rule number from that list:\n/ip firewall filter set <number> log=yes log-prefix=drop-input",
			Limits:   "Based on the capabilities stored when the device was set up.",
		})
	}
	if !seen {
		return nil, "no stored firewall rules"
	}
	return out, ""
}

// ---- DNS ----

const dnsMinFlows = 30

func routerLAN(s Snapshot) string {
	rs := routers(s)
	if len(rs) == 1 && !IsPublic(rs[0].Addr) && strings.Count(rs[0].Addr, ".") == 3 {
		return rs[0].Addr
	}
	return "192.168.88.1"
}

func ruleDNSBypass(s Snapshot) ([]Suggestion, string) {
	if len(s.Traffic24h()) == 0 {
		return nil, "no flow data in the last 24 h"
	}
	idx := clientIndex(s)
	type agg struct {
		flows int64
		dst   map[string]bool
	}
	by := map[string]*agg{}
	for _, r := range portRows(s, 53, 853) {
		if !IsPublic(r.RIP) {
			continue
		}
		a := by[r.MAC]
		if a == nil {
			a = &agg{dst: map[string]bool{}}
			by[r.MAC] = a
		}
		a.flows += r.Flows
		a.dst[r.RIP] = true
	}
	type row struct {
		name string
		a    *agg
	}
	var rows []row
	for mac, a := range by {
		if a.flows >= dnsMinFlows {
			rows = append(rows, row{nameOf(idx, mac), a})
		}
	}
	if len(rows) == 0 {
		return nil, ""
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].a.flows > rows[j].a.flows })
	var ev []string
	for _, r := range rows {
		var ds []string
		for d := range r.a.dst {
			ds = append(ds, d)
		}
		sort.Strings(ds)
		ev = append(ev, fmt.Sprintf("%s: %d DNS connections to %s", r.name, r.a.flows, list(ds, 3)))
	}
	rip := routerLAN(s)
	return []Suggestion{{
		Severity: SevTip, Category: CatSecurity, Link: "insights/ports", Confidence: "high",
		Title:    "Some clients ask public DNS servers directly instead of the router",
		Why:      "When a device uses its own DNS server (for example 8.8.8.8), the router never sees which names it looks up. mtmon then cannot name destinations or recognise apps by their host name, DNS-based filtering on the router is bypassed, and local names do not resolve. Many smart TVs and gadgets have a fixed DNS server built in.",
		Evidence: append([]string{fmt.Sprintf("%d clients sent DNS traffic (port 53/853) to public servers in the last 24 h", len(rows))}, list0(ev, 5)...),
		Steps: []string{
			"Hand out the router as DNS server in DHCP and let it answer DNS requests.",
			"Redirect all other DNS traffic from the LAN to the router (dst-nat); devices with a fixed DNS server then still end up at the router without noticing.",
			"Optional: block DNS-over-TLS (port 853), which cannot be redirected. Put these rules above any FastTrack or accept-all rules.",
		},
		Commands: fmt.Sprintf("/ip dns set allow-remote-requests=yes\n/ip firewall nat add chain=dstnat action=dst-nat protocol=udp dst-port=53 in-interface-list=LAN dst-address=!%[1]s to-addresses=%[1]s to-ports=53 comment=\"redirect DNS to router\"\n/ip firewall nat add chain=dstnat action=dst-nat protocol=tcp dst-port=53 in-interface-list=LAN dst-address=!%[1]s to-addresses=%[1]s to-ports=53 comment=\"redirect DNS to router\"\n/ip firewall filter add chain=forward action=reject reject-with=icmp-admin-prohibited protocol=tcp dst-port=853 in-interface-list=LAN comment=\"block DNS-over-TLS\"", rip),
		Limits:   "The router address in the commands is taken from your device list; adjust it if your LAN gateway is different. The redirect needs the interface list LAN.",
	}}, ""
}

func list0(ev []string, n int) []string {
	if len(ev) <= n {
		return ev
	}
	return append(append([]string{}, ev[:n]...), fmt.Sprintf("…(+%d)", len(ev)-n))
}

var dohHosts = []string{"dns.google", "cloudflare-dns.com", "dns.quad9.net", "doh.opendns.com", "dns.nextdns.io", "dns.adguard-dns.com", "doh.dns.sb"}

// DoHSuffixes is used by the live snapshot to query the rollups.
func DoHSuffixes() []string { return dohHosts }

const dohMinFlows = 40

func ruleDoH(s Snapshot) ([]Suggestion, string) {
	if len(s.Traffic24h()) == 0 {
		return nil, "no flow data in the last 24 h"
	}
	idx := clientIndex(s)
	flows := map[string]int64{}
	hosts := map[string]map[string]bool{}
	for _, r := range s.DoH24h() {
		flows[r.MAC] += r.Flows
		if hosts[r.MAC] == nil {
			hosts[r.MAC] = map[string]bool{}
		}
		hosts[r.MAC][r.Host] = true
	}
	var ev []string
	n := 0
	for mac, f := range flows {
		if f < dohMinFlows {
			continue
		}
		n++
		var hs []string
		for h := range hosts[mac] {
			hs = append(hs, h)
		}
		sort.Strings(hs)
		ev = append(ev, fmt.Sprintf("%s: %d connections to %s", nameOf(idx, mac), f, list(hs, 3)))
	}
	if n == 0 {
		return nil, ""
	}
	sort.Strings(ev)
	return []Suggestion{{
		Severity: SevInfo, Category: CatSecurity, Link: "insights/hosts", Confidence: "medium",
		Title:    "Some clients use encrypted DNS (DNS over HTTPS) with a public provider",
		Why:      "Browsers and phones can hide their name lookups inside HTTPS. That is good for privacy against outsiders, but the router cannot see the names either, so mtmon falls back to IP addresses and providers instead of host names, and router-side filtering does not apply to these clients. A DNS redirect does not catch DoH.",
		Evidence: append([]string{fmt.Sprintf("%d clients made many connections to well-known DoH resolvers in the last 24 h", n)}, list0(ev, 5)...),
		Steps: []string{
			"If you want the router to see the lookups, switch off \"secure DNS\" in the browser or set it to your own resolver.",
			"Alternatively accept it: traffic is still counted, only the naming is less precise for these clients.",
			"Optional: block the well-known resolvers on port 443 so that clients fall back to normal DNS (they are then caught by a DNS redirect).",
		},
		Commands: "/ip firewall address-list add list=doh-servers address=dns.google\n/ip firewall address-list add list=doh-servers address=cloudflare-dns.com\n/ip firewall address-list add list=doh-servers address=dns.quad9.net\n/ip firewall filter add chain=forward action=reject reject-with=icmp-admin-prohibited protocol=tcp dst-port=443 dst-address-list=doh-servers in-interface-list=LAN comment=\"block public DoH\"",
		Limits:   "Only resolvers that appear under their well-known name in the router DNS cache are detected; custom DoH servers are not.",
	}}, ""
}
