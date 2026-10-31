// SPDX-License-Identifier: AGPL-3.0-only

package advice

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ---- DNS ----

const dnsMinFlows = 30

func routerLAN(s Snapshot) string {
	rs := routers(s)
	if len(rs) == 1 && !IsPublic(rs[0].Addr) && strings.Count(rs[0].Addr, ".") == 3 {
		return rs[0].Addr
	}
	return "192.168.88.1"
}

// dnsRedirected reports whether a router already sends the given protocol's port-53 traffic to itself
// (an active dst-nat rule with matched packets). Flows are exported with the original destination, so
// without this check a working redirect looks like a bypass.
func dnsRedirected(s Snapshot, proto int) bool {
	for _, d := range routers(s) {
		if d.Caps == nil {
			continue
		}
		if (proto == 17 && d.Caps.DNSRedirectUDP) || (proto == 6 && d.Caps.DNSRedirectTCP) {
			return true
		}
	}
	return false
}

type dnsAgg struct {
	flows int64
	dst   map[string]bool
}

type dnsGroup map[string]*dnsAgg

func (g dnsGroup) add(r PortUse) {
	a := g[r.MAC]
	if a == nil {
		a = &dnsAgg{dst: map[string]bool{}}
		g[r.MAC] = a
	}
	a.flows += r.Flows
	a.dst[r.RIP] = true
}

// evidence lists the clients above the flow threshold, busiest first.
func (g dnsGroup) evidence(idx map[string]Client, what string) (int, []string) {
	type row struct {
		name string
		a    *dnsAgg
	}
	var rows []row
	for mac, a := range g {
		if a.flows >= dnsMinFlows {
			rows = append(rows, row{nameOf(idx, mac), a})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].a.flows > rows[j].a.flows })
	var ev []string
	for _, r := range rows {
		var ds []string
		for d := range r.a.dst {
			ds = append(ds, d)
		}
		sort.Strings(ds)
		ev = append(ev, fmt.Sprintf("%s: %d %s to %s", r.name, r.a.flows, what, list(ds, 3)))
	}
	return len(rows), ev
}

func ruleDNSBypass(s Snapshot) ([]Suggestion, string) {
	if len(s.Traffic24h()) == 0 {
		return nil, "no flow data in the last 24 h"
	}
	idx := clientIndex(s)
	open, redir, dot := dnsGroup{}, dnsGroup{}, dnsGroup{}
	for _, r := range portRows(s, 53, 853) {
		if !IsPublic(r.RIP) {
			continue
		}
		switch {
		case r.Port == 853:
			dot.add(r)
		case dnsRedirected(s, r.Proto):
			redir.add(r)
		default:
			open.add(r)
		}
	}
	rip := routerLAN(s)
	var out []Suggestion
	if n, ev := open.evidence(idx, "DNS connections"); n > 0 {
		out = append(out, Suggestion{
			Severity: SevTip, Category: CatSecurity, Link: "insights/ports", Confidence: "high",
			Title:    "Some clients ask public DNS servers directly instead of the router",
			Why:      "When a device uses its own DNS server (for example 8.8.8.8), the router never sees which names it looks up. mtmon then cannot name destinations or recognise apps by their host name, DNS-based filtering on the router is bypassed, and local names do not resolve. Many smart TVs and gadgets have a fixed DNS server built in.",
			Evidence: append([]string{fmt.Sprintf("%d clients sent DNS traffic (port 53) to public servers in the last 24 h", n)}, list0(ev, 5)...),
			Steps: []string{
				"Hand out the router as DNS server in DHCP and let it answer DNS requests.",
				"Redirect all other DNS traffic from the LAN to the router (dst-nat); devices with a fixed DNS server then still end up at the router without noticing.",
			},
			Commands: fmt.Sprintf("/ip dns set allow-remote-requests=yes\n/ip firewall nat add chain=dstnat action=dst-nat protocol=udp dst-port=53 in-interface-list=LAN dst-address=!%[1]s to-addresses=%[1]s to-ports=53 comment=\"redirect DNS to router\"\n/ip firewall nat add chain=dstnat action=dst-nat protocol=tcp dst-port=53 in-interface-list=LAN dst-address=!%[1]s to-addresses=%[1]s to-ports=53 comment=\"redirect DNS to router\"", rip),
			Limits:   "The router address in the commands is taken from your device list; adjust it if your LAN gateway is different. The redirect needs the interface list LAN.",
		})
	}
	if n, ev := redir.evidence(idx, "DNS connections"); n > 0 {
		out = append(out, Suggestion{
			Subject: "redirected", Severity: SevInfo, Category: CatSecurity, Link: "insights/ports", Confidence: "high",
			Title: "Clients ask public DNS servers, but the router redirects them",
			Why:   "These devices use their own DNS server (for example 8.8.8.8). A DNS redirect rule on the router already sends those requests to the router, so name lookups, filtering and host names in mtmon still work. Nothing to do.",
			Evidence: append([]string{fmt.Sprintf("%d clients sent port 53 traffic to public servers; the redirect rule on the router has matched packets", n)},
				list0(ev, 5)...),
			Limits: "Flows show the original destination (before the redirect). mtmon read the redirect rule and its packet counter from the router; it is refreshed hourly.",
		})
	}
	if n, ev := dot.evidence(idx, "DNS-over-TLS connections"); n > 0 {
		out = append(out, Suggestion{
			Subject: "dot", Severity: SevTip, Category: CatSecurity, Link: "insights/ports", Confidence: "high",
			Title:    "Some clients use encrypted DNS (DNS-over-TLS) to public servers",
			Why:      "DNS-over-TLS (port 853) cannot be redirected to the router like plain DNS. The router does not see these lookups, so DNS-based filtering and host names in mtmon are missing for these devices.",
			Evidence: append([]string{fmt.Sprintf("%d clients sent port 853 traffic to public servers in the last 24 h", n)}, list0(ev, 5)...),
			Steps: []string{
				"Optional: block port 853 so devices fall back to plain DNS, which the router can redirect. Put the rule above any FastTrack or accept-all rules.",
			},
			Commands: "/ip firewall filter add chain=forward action=reject reject-with=icmp-admin-prohibited protocol=tcp dst-port=853 in-interface-list=LAN comment=\"block DNS-over-TLS\"",
			Limits:   "The rule assumes the interface list LAN. DNS-over-HTTPS (port 443) is covered by a separate suggestion.",
		})
	}
	return out, ""
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

// regressionMinHits: how many logged drops in the last 24 h make "the accept rule before it never matches" worth a warning.
const regressionMinHits = 50

func portInSpec(spec string, port int) bool {
	for _, p := range strings.Split(spec, ",") {
		p = strings.TrimSpace(p)
		if a, b, ok := strings.Cut(p, "-"); ok {
			lo, e1 := strconv.Atoi(a)
			hi, e2 := strconv.Atoi(b)
			if e1 == nil && e2 == nil && port >= lo && port <= hi {
				return true
			}
		} else if n, err := strconv.Atoi(p); err == nil && n == port {
			return true
		}
	}
	return false
}

// ruleRegression finds an accept rule that never matches although a later drop rule for the very same
// protocol/port drops a lot: the accept rule is shadowed or its conditions no longer fit, so legitimate traffic
// is being blocked ("rule regression").
func commentOf(c string) string {
	if c == "" {
		return ""
	}
	return " “" + c + "”"
}

func ruleRegression(s Snapshot) ([]Suggestion, string) {
	hits := map[string]int64{}
	for _, r := range s.FwRules() {
		hits[r.Device+"|"+r.Prefix] += r.Hits24h
	}
	if len(hits) == 0 {
		return nil, "no firewall rules with log prefix known"
	}
	idx := clientIndex(s)
	var out []Suggestion
	for _, d := range s.Devices() {
		if d.Caps == nil {
			continue
		}
		f := d.Caps.Filter
		for i, drop := range f {
			if drop.Disabled || (drop.Action != "drop" && drop.Action != "reject") || drop.LogPrefix == "" || drop.DPort == "" {
				continue
			}
			port, err := strconv.Atoi(drop.DPort)
			n := hits[d.Name+"|"+drop.LogPrefix]
			if err != nil || n < regressionMinHits || (drop.Proto != "tcp" && drop.Proto != "udp") {
				continue
			}
			// Every accept rule in front of the drop that covers this protocol/port is judged together: traffic is
			// only shadowed when none of them ever matches. A rule meant for a sub-group (e.g. a VPN subnet) may
			// legitimately stay at 0 while a sibling rule carries the traffic.
			var accepts []FilterRule
			for j := i - 1; j >= 0; j-- {
				acc := f[j]
				if acc.Disabled || acc.Action != "accept" || acc.Chain != drop.Chain || acc.Proto != drop.Proto || !portInSpec(acc.DPort, port) {
					continue
				}
				accepts = append(accepts, acc)
			}
			allZero := len(accepts) > 0
			for _, acc := range accepts {
				if !acc.Counted || acc.Packets != 0 {
					allZero = false
				}
			}
			if !allZero {
				continue
			}
			acc := accepts[0] // nearest to the drop rule
			proto := 6
			if drop.Proto == "udp" {
				proto = 17
			}
			seen := map[string]bool{}
			var names []string
			for _, r := range portRows(s, port) {
				if r.Proto == proto && !seen[r.MAC] {
					seen[r.MAC] = true
					names = append(names, nameOf(idx, r.MAC))
				}
			}
			sort.Strings(names)
			ev := []string{
				fmt.Sprintf("Accept rule%s: %s %s (0 packets counted)", commentOf(acc.Comment), acc.Chain, acc.Summary),
				fmt.Sprintf("Drop rule right after it%s: %s %s – %d logged drops in the last 24 h", commentOf(drop.Comment), drop.Chain, drop.Summary, n),
			}
			if len(accepts) > 1 {
				ev = append(ev, fmt.Sprintf("All %d accept rules for %s/%d in front of the drop rule are at 0 packets", len(accepts), drop.Proto, port))
			}
			if len(names) > 0 {
				ev = append(ev, fmt.Sprintf("Clients that used %s/%d: %s", drop.Proto, port, list(names, 5)))
			}
			out = append(out, Suggestion{
				Subject: fmt.Sprintf("%s-%s-%s-%d", d.Name, drop.Chain, drop.Proto, port), Severity: SevWarning, Category: CatHygiene,
				Link: "firewall", Confidence: "medium",
				Title:    fmt.Sprintf("An accept rule for %s/%d on %s never matches, but the drop rule behind it does", drop.Proto, port, d.Name),
				Why:      "An accept rule is meant to let this traffic through before the drop rule catches it. Its counter stays at 0 while the drop rule blocks the same port again and again, so the accept rule does not apply any more: something in front of it changes the traffic, or its interface, address or connection-state condition no longer fits. The traffic that was meant to pass is being blocked.",
				Evidence: ev,
				Steps: []string{
					"Compare the conditions of the accept rule (interface list, addresses, connection state) with a blocked connection on the Firewall page.",
					"Fix the condition or move the accept rule above whatever matches first, then check that its counter rises and the drop counter stops.",
				},
				Commands: "/ip firewall filter print stats where chain=" + drop.Chain + " and (action=accept or action=drop)",
				Limits:   "Counters are read when the device is probed (about hourly) and restart at 0 after a reboot or counter reset, so a very new rule can look unused. Drops are counted over the last 24 h only.",
			})
		}
	}
	return out, ""
}

// list3 returns at most n lines and, when more exist, a final "… and N more" line.
func list3(lines []string, n int) []string {
	if len(lines) <= n {
		return append([]string(nil), lines...)
	}
	return append(append([]string(nil), lines[:n]...), fmt.Sprintf("… and %d more", len(lines)-n))
}
