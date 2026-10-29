package advice

import (
	"fmt"
	"sort"
	"strings"
)

var portName = map[int]string{21: "FTP", 23: "Telnet", 139: "SMB", 445: "SMB", 3389: "RDP", 5900: "VNC"}

const minConns = 3 // connections in 24 h before a protocol use is worth mentioning

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
