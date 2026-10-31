// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/live"
	"github.com/napfkuchen1/mtmon/internal/poller"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// Path view (#147): the hops between one client and one destination, each with the facts mtmon already has and a
// health colour. Everything is joined from existing data; no router is queried.

// Health of a hop, worst last so a larger rank is worse.
const (
	hOK      = "ok"
	hWarn    = "warn"
	hBad     = "bad"
	hUnknown = "unknown"
)

func healthRank(h string) int {
	switch h {
	case hBad:
		return 3
	case hWarn:
		return 2
	case hOK:
		return 1
	}
	return 0
}

type pathFact struct {
	K string `json:"k"`
	V string `json:"v"`
}

type pathHop struct {
	Kind   string     `json:"kind"` // client | link | device | wan | network | endpoint
	Title  string     `json:"title"`
	Sub    string     `json:"sub,omitempty"`
	Health string     `json:"health"`
	Why    string     `json:"why,omitempty"` // what made it amber/red
	Bps    float64    `json:"bps"`           // throughput on the link into this hop (0 = unknown)
	Facts  []pathFact `json:"facts"`
}

// pathDev is one managed device between the client and the router, from the client's side up.
type pathDev struct {
	Name, Role string
	Up         bool
	CPU        float64
	Port       string  // port on the parent this device hangs off ("" = unknown)
	ParentName string  // "" for the root
	PortRx     float64 // rates of that port on the parent
	PortTx     float64
	PortKnown  bool
	PortUp     bool
}

type pathDst struct {
	Host, IP, CC, ASOrg, Svc string
	ASN, Port, Proto         int
	Bytes, Conns             int64
}

type pathIn struct {
	Client    store.Client
	AddrOnly  bool
	ClientBps float64
	DNS       string // "", "bypass", "redirected"
	Access    live.IfRate
	AccessOK  bool
	Chain     []pathDev // client's access device first, root last
	Upstream  *poller.Upstream
	WAN       store.WANWindow
	Dst       pathDst
}

// vpnHop is the access link of a WireGuard peer: the tunnel interface and whether the peer shook hands recently
// (the poller marks a peer online when the last handshake is at most 3 minutes old).
func vpnHop(c store.Client) pathHop {
	l := pathHop{Kind: "link", Title: "VPN tunnel", Sub: "via " + c.Device, Health: hOK, Facts: []pathFact{}}
	if c.Iface != "" {
		l.Facts = append(l.Facts, fact("Tunnel interface", c.Iface))
	}
	if c.Online {
		l.Facts = append(l.Facts, fact("Handshake", "recent (under 3 minutes)"))
	} else {
		l.Health, l.Why = hWarn, "no recent WireGuard handshake"
		l.Facts = append(l.Facts, fact("Handshake", "none in the last 3 minutes"))
	}
	if c.IP != "" {
		l.Facts = append(l.Facts, fact("Tunnel address", c.IP))
	}
	return l
}

func wifiHealth(sig int) (string, string) {
	switch {
	case sig == 0:
		return hUnknown, ""
	case sig >= -67:
		return hOK, ""
	case sig >= -75:
		return hWarn, fmt.Sprintf("weak signal (%d dBm)", sig)
	}
	return hBad, fmt.Sprintf("very weak signal (%d dBm)", sig)
}

func cpuHealth(cpu float64, up bool) (string, string) {
	switch {
	case !up:
		return hBad, "device is down"
	case cpu >= 90:
		return hBad, fmt.Sprintf("CPU %.0f%%", cpu)
	case cpu >= 70:
		return hWarn, fmt.Sprintf("CPU %.0f%%", cpu)
	}
	return hOK, ""
}

func wanHealth(w store.WANWindow) (string, string) {
	switch {
	case w.Sent == 0:
		return hUnknown, ""
	case w.LossPct >= 5:
		return hBad, fmt.Sprintf("%.1f%% packet loss", w.LossPct)
	case w.LossPct >= 1 || w.AvgMS >= 150:
		return hWarn, fmt.Sprintf("%.1f%% loss, %.0f ms", w.LossPct, w.AvgMS)
	}
	return hOK, ""
}

// rateText turns a raw bit rate ("576500000") into "576.5 Mbit/s"; router-formatted strings stay as they are.
func rateText(s string) string {
	bps, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || bps <= 0 {
		return s
	}
	for _, u := range []string{"bit/s", "kbit/s", "Mbit/s", "Gbit/s"} {
		if bps < 1000 || u == "Gbit/s" {
			if u == "bit/s" {
				return fmt.Sprintf("%.0f %s", bps, u)
			}
			return fmt.Sprintf("%.1f %s", bps, u)
		}
		bps /= 1000
	}
	return s
}

func bytesText(b int64) string {
	f := float64(b)
	for _, u := range []string{"B", "kB", "MB", "GB", "TB"} {
		if f < 1000 || u == "TB" {
			if u == "B" {
				return fmt.Sprintf("%d B", b)
			}
			return fmt.Sprintf("%.1f %s", f, u)
		}
		f /= 1000
	}
	return ""
}

func protoName(p int) string {
	switch p {
	case 6:
		return "TCP"
	case 17:
		return "UDP"
	case 1:
		return "ICMP"
	}
	return strconv.Itoa(p)
}

func fact(k, v string) pathFact { return pathFact{K: k, V: v} }

func clientTitle(c store.Client) string {
	if n := ClientName(c); n != "" {
		return n
	}
	return c.MAC
}

// buildPath assembles the hop list. It is pure so every branch (Wi-Fi, cable, VPN, no device, no upstream) is table
// tested without a router.
func buildPath(in pathIn) (hops []pathHop, weakest int) {
	c := in.Client
	vpn := strings.HasPrefix(c.MAC, "VPN:")
	ch := pathHop{Kind: "client", Title: clientTitle(c), Sub: c.IP, Health: hOK, Bps: in.ClientBps, Facts: []pathFact{}}
	if c.Group != "" {
		ch.Facts = append(ch.Facts, fact("Group", c.Group))
	}
	switch in.DNS {
	case "bypass":
		ch.Health, ch.Why = hWarn, "uses its own DNS server"
		ch.Facts = append(ch.Facts, fact("DNS", "bypasses the router"))
	case "redirected":
		ch.Facts = append(ch.Facts, fact("DNS", "redirected to the router"))
	default:
		ch.Facts = append(ch.Facts, fact("DNS", "router"))
	}
	hops = append(hops, ch)

	// access link: Wi-Fi, cable or VPN tunnel
	switch {
	case in.AddrOnly || c.Device == "":
		hops = append(hops, pathHop{Kind: "link", Title: "Unknown access", Sub: "client is not placed on a device", Health: hUnknown, Facts: []pathFact{}})
	case vpn:
		hops = append(hops, vpnHop(c))
	case c.WiFi:
		h, why := wifiHealth(c.Signal)
		l := pathHop{Kind: "link", Title: "Wi-Fi " + strings.TrimSpace(c.SSID+" "+c.Band), Health: h, Why: why, Facts: []pathFact{}}
		if c.Signal != 0 {
			l.Facts = append(l.Facts, fact("Signal", fmt.Sprintf("%d dBm", c.Signal)))
		}
		if c.TxRate != "" {
			l.Facts = append(l.Facts, fact("TX rate", rateText(c.TxRate)))
		}
		if c.RxRate != "" {
			l.Facts = append(l.Facts, fact("RX rate", rateText(c.RxRate)))
		}
		hops = append(hops, l)
	default:
		h := hUnknown
		l := pathHop{Kind: "link", Title: "Cable " + c.Iface, Facts: []pathFact{}}
		if in.AccessOK {
			h = hOK
			l.Bps = in.Access.RxBps + in.Access.TxBps
			if !in.Access.Running {
				h, l.Why = hBad, "port is down"
			}
		}
		l.Health = h
		hops = append(hops, l)
	}

	for i, d := range in.Chain {
		if i > 0 {
			up := in.Chain[i-1]
			lk := pathHop{Kind: "link", Title: "Uplink " + up.Name + " → " + d.Name, Health: hUnknown, Facts: []pathFact{}}
			if up.Port != "" {
				lk.Sub = d.Name + " port " + up.Port
			}
			if up.PortKnown {
				lk.Health, lk.Bps = hOK, up.PortRx+up.PortTx
				if !up.PortUp {
					lk.Health, lk.Why = hBad, "port is down"
				}
			}
			hops = append(hops, lk)
		}
		h, why := cpuHealth(d.CPU, d.Up)
		dh := pathHop{Kind: "device", Title: d.Name, Sub: d.Role, Health: h, Why: why, Facts: []pathFact{fact("CPU", fmt.Sprintf("%.0f%%", d.CPU))}}
		if i == len(in.Chain)-1 && d.Role == "router" {
			dh.Facts = append(dh.Facts, fact("Role", "firewall / NAT"))
		}
		hops = append(hops, dh)
	}

	// WAN: the upstream facing the gateway, with mtmon's own probe quality
	wh, why := wanHealth(in.WAN)
	wan := pathHop{Kind: "wan", Title: "Internet access", Health: wh, Why: why, Facts: []pathFact{}}
	if u := in.Upstream; u != nil {
		wan.Title, wan.Sub = u.Label, strings.Join(u.Modes, ", ")
		if u.Port != "" {
			wan.Facts = append(wan.Facts, fact("Port", u.Port))
		}
	}
	if a, err := netip.ParseAddr(in.Dst.IP); err == nil {
		fam := "IPv6 (no NAT)"
		if a.Is4() || a.Is4In6() {
			fam = "IPv4"
			if ca, err := netip.ParseAddr(in.Client.IP); err == nil && ca.IsPrivate() {
				fam = "IPv4 (source NAT on the gateway)"
			}
		}
		wan.Facts = append(wan.Facts, fact("Address family of this target", fam))
	}
	if in.WAN.Sent > 0 {
		wan.Facts = append(wan.Facts, fact("Probe latency", fmt.Sprintf("%.0f ms avg, %.0f ms max", in.WAN.AvgMS, in.WAN.MaxMS)), fact("Probe loss", fmt.Sprintf("%.1f%%", in.WAN.LossPct)))
	}
	hops = append(hops, wan)

	d := in.Dst
	if d.ASOrg != "" || d.CC != "" {
		n := pathHop{Kind: "network", Title: d.ASOrg, Sub: d.CC, Health: hUnknown, Facts: []pathFact{}}
		if n.Title == "" {
			n.Title = d.CC
		}
		if d.ASN != 0 {
			n.Facts = append(n.Facts, fact("ASN", fmt.Sprintf("AS%d", d.ASN)))
		}
		hops = append(hops, n)
	}
	ep := pathHop{Kind: "endpoint", Title: d.Host, Sub: d.IP, Health: hUnknown, Facts: []pathFact{}}
	if ep.Title == "" {
		ep.Title = d.IP
	}
	if d.Conns == 0 && ep.Title != "" {
		ep.Facts = append(ep.Facts, fact("Traffic", "none to this target in the selected range"))
	}
	if d.Svc != "" {
		ep.Facts = append(ep.Facts, fact("Service", d.Svc))
	}
	if d.Port != 0 {
		ep.Facts = append(ep.Facts, fact("Port", protoName(d.Proto)+"/"+strconv.Itoa(d.Port)))
	}
	if d.Conns > 0 {
		ep.Facts = append(ep.Facts, fact("Connections", fmt.Sprintf("%d", d.Conns)), fact("Bytes", bytesText(d.Bytes)))
	}
	hops = append(hops, ep)

	best := 0
	weakest = -1
	for i, h := range hops {
		if r := healthRank(h.Health); r > best && r >= 2 {
			best, weakest = r, i
		}
	}
	return hops, weakest
}

// pathChain walks from the client's device up to the root along the oriented links. A device without a known
// link hangs directly off the root (as in the topology map).
func pathChain(start, root string, links []deviceLink, roleOf map[string]string, devs map[string]live.DevLive, ifaces func(string) []live.IfRate) []pathDev {
	parent := map[string]deviceLink{}
	for _, l := range links {
		parent[l.To] = l
	}
	var out []pathDev
	seen := map[string]bool{}
	for cur := start; cur != "" && !seen[cur]; {
		seen[cur] = true
		pd := pathDev{Name: cur, Role: roleOf[cur], Up: devs[cur].Up, CPU: devs[cur].CPU}
		next := ""
		if l, ok := parent[cur]; ok {
			next, pd.ParentName, pd.Port = l.From, l.From, neighborPort(l.Port)
		} else if cur != root && root != "" {
			next, pd.ParentName = root, root
		}
		if next != "" && pd.Port != "" {
			for _, r := range ifaces(next) {
				if r.Iface == pd.Port {
					pd.PortRx, pd.PortTx, pd.PortKnown, pd.PortUp = r.RxBps, r.TxBps, true, r.Running
					break
				}
			}
		}
		out = append(out, pd)
		cur = next
	}
	return out
}

// matchDst folds a client's flows towards one destination (an IP, or a host name) into a pathDst.
func matchDst(conns []store.Conn, dst string) (pathDst, bool) {
	ip := ""
	if a, err := netip.ParseAddr(dst); err == nil {
		ip = a.Unmap().String()
	}
	var d pathDst
	found := false
	for _, c := range conns {
		if ip != "" && c.RIP != ip || ip == "" && !hostMatches(c.Host, dst) {
			continue
		}
		if !found {
			d = pathDst{Host: c.Host, IP: c.RIP, CC: c.CC, ASN: c.ASN, ASOrg: c.ASOrg, Svc: c.Svc, Port: c.RPort, Proto: c.Proto}
			found = true
		}
		d.Bytes += c.Bytes
		d.Conns++
	}
	return d, found
}

func hostMatches(host, want string) bool {
	host, want = strings.ToLower(host), strings.ToLower(strings.TrimSuffix(want, "."))
	return host != "" && want != "" && (host == want || strings.HasSuffix(host, "."+want))
}

// path: GET /api/path?client=<mac>&dst=<ip|host>&range=… – the hops from a client to a destination (default: its top host).
func (s *Server) path(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	key := store.NormMAC(q.Get("client"))
	c, addrOnly, err := s.clientOrAddr(key)
	if err != nil {
		jerr(w, 404, "unknown client")
		return
	}
	since, _, rn := parseRange(r)
	dst := strings.TrimSpace(q.Get("dst"))
	if dst == "" {
		if rows, _ := s.St.TopBy("hosts", since, 1, key); len(rows) > 0 {
			dst = rows[0].Key
		}
	}
	var d pathDst
	if dst != "" {
		f := store.ConnFilter{}
		if a, perr := netip.ParseAddr(dst); perr == nil {
			f.RIP = a.Unmap().String()
		}
		conns, _ := s.St.ConnectionsFiltered(key, since, 1000, 0, f)
		var ok bool
		if d, ok = matchDst(conns, dst); !ok {
			d = pathDst{Host: dst}
			if _, perr := netip.ParseAddr(dst); perr == nil {
				d = pathDst{IP: dst}
			}
		}
	}
	in := pathIn{Client: *c, AddrOnly: addrOnly, WAN: s.St.WANQualitySince(since), Dst: d}
	for _, cl := range s.Hub.Snapshot(s.Hub.LastSeq(), 1000).Clients {
		if cl.Key == key {
			in.ClientBps = cl.UpBps + cl.DownBps
		}
	}
	if _, byp := s.dnsBypass(); byp != nil {
		if b, ok := byp[key]; ok && b.Active {
			in.DNS = "bypass"
			if b.Redirected {
				in.DNS = "redirected"
			}
		}
	}
	if c.Device != "" && !addrOnly {
		all, _ := s.St.Clients()
		root, _, links, roleOf := s.deviceTree(all)
		devs := map[string]live.DevLive{}
		for _, dv := range s.Hub.Devices() {
			devs[dv.Name] = dv
		}
		in.Chain = pathChain(c.Device, root, links, roleOf, devs, s.Hub.Ifaces)
		if !c.WiFi {
			for _, ir := range s.Hub.Ifaces(c.Device) {
				if ir.Iface == c.Iface {
					in.Access, in.AccessOK = ir, true
				}
			}
		}
		if root != "" {
			for _, u := range s.upstreams() {
				if u.Device == root {
					u := u
					in.Upstream = &u
					break
				}
			}
		}
	}
	hops, weakest := buildPath(in)
	jsonOut(w, map[string]any{"client": c.MAC, "dst": dst, "range": rn, "hops": hops, "weakest": weakest, "at": time.Now().Unix()})
}
