package poller

import (
	"net/netip"
	"sort"
	"strings"

	"github.com/napfkuchen1/mtmon/internal/addr"
)

// Upstream is the ISP modem / router (or ONT) in front of a gateway, derived from data mtmon already reads: the
// gateway's WAN interfaces, its ARP table, addresses, PPPoE clients, tunnel interfaces and default route. It is not a
// client and not a managed device; the UI shows it above the gateway with the way the line is brought in.
type Upstream struct {
	Device string   `json:"device"` // the gateway that faces it
	Port   string   `json:"port"`   // the physical port towards the modem
	Label  string   `json:"label"`
	IP     string   `json:"ip,omitempty"`
	MAC    string   `json:"mac,omitempty"`
	Vendor string   `json:"vendor,omitempty"`
	NoIP   bool     `json:"no_ip"` // modem without an address of its own (bridge mode, ONT): only PPPoE shows it
	Modes  []string `json:"modes"` // e.g. "PPPoE", "DS-Lite", "transfer net 192.168.178.0/24", "double NAT"
}

// ifaceKind sorts a WAN interface by its RouterOS type, falling back to the name.
func ifaceKind(typ, name string) string {
	t, n := strings.ToLower(typ), strings.ToLower(name)
	switch {
	case strings.Contains(t, "pppoe") || strings.HasPrefix(n, "pppoe"):
		return "ppp"
	case strings.Contains(t, "ipipv6") || strings.Contains(n, "ipipv6") || strings.Contains(n, "dslite") || strings.Contains(n, "ds-lite"):
		return "dslite"
	case strings.Contains(t, "gre") || strings.Contains(t, "ipip") || strings.Contains(t, "6to4") || strings.Contains(t, "sit") ||
		strings.HasPrefix(n, "gre") || strings.HasPrefix(n, "ipip") || strings.HasPrefix(n, "6to4") || strings.HasPrefix(n, "sit"):
		return "tunnel"
	case t == "bridge" || strings.HasPrefix(n, "bridge"):
		return "bridge"
	case strings.Contains(t, "vlan") || strings.HasPrefix(n, "vlan"):
		return "vlan"
	}
	return "port"
}

func addAfter(modes []string, m string) []string {
	for _, x := range modes {
		if x == m {
			return modes
		}
	}
	return append(modes, m)
}

// upstreamsFor derives the upstream devices of one gateway from its snapshot. Pure (no locks): tests feed it rows.
func upstreamsFor(dev string, s *devSnap, vendor func(mac string) string) []Upstream {
	if s == nil || len(s.wan) == 0 {
		return nil
	}
	types := map[string]string{}
	for _, r := range s.ifs {
		types[r["name"]] = r["type"]
	}
	var ports []string
	pppOn := map[string]bool{} // physical port -> a PPPoE client runs over it
	dsl, tun := false, false
	for name := range s.wan {
		switch ifaceKind(types[name], name) {
		case "port":
			ports = append(ports, name)
		case "dslite":
			dsl = true
		case "tunnel":
			tun = true
		}
	}
	for _, r := range s.pppoe {
		if p := r["interface"]; p != "" && !r.Bool("disabled") {
			pppOn[p] = true
		}
	}
	sort.Strings(ports)
	var out []Upstream
	for _, p := range ports {
		var modes []string
		if pppOn[p] {
			modes = addAfter(modes, "PPPoE")
		}
		if dsl {
			modes = addAfter(modes, "DS-Lite")
		}
		if tun {
			modes = addAfter(modes, "tunnel")
		}
		for _, a := range s.addrs {
			if a["interface"] != p || a.Bool("disabled") {
				continue
			}
			if pf, err := netip.ParsePrefix(a["address"]); err == nil && pf.Addr().Is4() {
				modes = addAfter(modes, "transfer net "+pf.Masked().String())
			}
		}
		// the default route runs through a private gateway on this port and no PPPoE dials over it: another router
		// in front of this one
		if !pppOn[p] {
			for _, r := range s.routes {
				if r["dst-address"] != "0.0.0.0/0" || r.Bool("disabled") || (r["active"] != "" && !r.Bool("active")) {
					continue
				}
				gw, ifc, _ := strings.Cut(firstNonEmpty(r["immediate-gw"], r["gateway"]), "%")
				if ip, err := netip.ParseAddr(gw); err == nil && (ifc == p || ifc == "") && (addr.Classify(ip) == addr.Private || ip.Is4() && addr.IsCGNAT(ip)) {
					modes = addAfter(modes, "double NAT")
				}
			}
		}
		seen := 0
		for _, r := range s.arp {
			if r["interface"] != p || r.Bool("invalid") {
				continue
			}
			if st := r["status"]; st != "reachable" && st != "delay" && st != "probe" && st != "stale" {
				continue
			}
			mac := strings.ToUpper(r["mac-address"])
			if mac == "" || seen >= 3 {
				continue
			}
			seen++
			u := Upstream{Device: dev, Port: p, IP: r["address"], MAC: mac, Modes: append([]string{}, modes...)}
			if vendor != nil {
				u.Vendor = vendor(mac)
			}
			u.Label = "Modem / router"
			if u.Vendor != "" {
				u.Label += " (" + shortVendor(u.Vendor) + ")"
			}
			out = append(out, u)
		}
		if seen == 0 && pppOn[p] { // a PPPoE client over a port, nothing answers ARP there: ONT or bridged modem
			out = append(out, Upstream{Device: dev, Port: p, Label: "ONT / modem (no IP)", NoIP: true, Modes: modes})
		}
	}
	return out
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// shortVendor trims an OUI vendor name to its first words ("AVM Audiovisuelles Marketing und Computersysteme GmbH" -> "AVM").
func shortVendor(v string) string {
	v = strings.TrimSpace(v)
	for _, cut := range []string{",", " GmbH", " Co.", " Inc", " Ltd", " Corp", " Technologies", " Audiovisuelles"} {
		if i := strings.Index(v, cut); i > 0 {
			v = v[:i]
		}
	}
	return strings.TrimSpace(v)
}

// Upstreams lists the upstream devices of all gateways; never nil.
func (m *Manager) Upstreams() []Upstream {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.snaps))
	for n := range m.snaps {
		names = append(names, n)
	}
	sort.Strings(names)
	out := []Upstream{}
	for _, n := range names {
		if !m.cascaded(n) {
			out = append(out, upstreamsFor(n, m.snaps[n], m.En.Vendor)...)
		}
	}
	return out
}
