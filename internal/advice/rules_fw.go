// SPDX-License-Identifier: AGPL-3.0-only

package advice

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

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
	out, scans := splitBlockedSources(s, bs)
	return originSuggestions(out, scans)
}

// splitBlockedSources sorts the blocked sources: packets with our own public address as sender and late replies from
// hosts our clients talked to become suggestions of their own; the rest are scans and are returned for grouping.
func splitBlockedSources(s Snapshot, bs []BlockedSrc) (out []Suggestion, scans []BlockedSrc) {
	own := ownPublicIPs(s)
	// Sources our own devices talked to are not scanners: their packets are blocked when they arrive after the
	// connection was lost (WAN reconnect, router restart), because the firewall no longer knows the connection.
	var cand []string
	for _, b := range bs {
		if len(cand) < 300 && IsPublic(b.IP) && !own[b.IP] {
			cand = append(cand, b.IP)
		}
	}
	contacted := s.ContactedByLAN(cand)
	var replies, replyIPs []string // blocked sources our own clients talked to: grouped into one suggestion
	for _, b := range bs {
		if own[b.IP] {
			if b.Hits < OriginMinHits {
				continue
			}
			out = append(out, Suggestion{
				Subject: "own-source-" + b.IP, Severity: SevWarning, Category: CatSecurity, Link: "firewall", Confidence: "medium",
				Title: fmt.Sprintf("Blocked packets arrive with your own public address %s as sender", b.IP),
				Why:   "The source of these blocked packets is the public address of your own router, not a remote host. That points to hairpin traffic (a LAN device reaching your public address, for example a VPN or port-forward test), a routing loop, or spoofed packets. It is not a late reply from an outside server and not a scan, so neither a raw drop nor an address list for this address makes sense.",
				Evidence: []string{
					fmt.Sprintf("%d blocked inbound events with source %s (your own WAN address) in the last 24 h", b.Hits, b.IP),
				},
				Steps: []string{
					"Check the blocked entries on the Firewall page: which destination address and port do they target, which device sends them?",
					"If a LAN device uses your public address (VPN client, port-forward test), use the internal address or add a hairpin NAT rule.",
				},
				Limits: "mtmon knows your own address from the router's interface addresses; if the provider changed it recently the match can lag by up to an hour.",
			})
			continue
		}
		if !contacted[b.IP] {
			scans = append(scans, b)
			continue
		}
		if b.Hits < OriginMinHits {
			continue
		}
		who := b.IP
		if b.ASN != 0 {
			who = fmt.Sprintf("%s (AS%d %s)", b.IP, b.ASN, b.ASOrg)
		}
		replies = append(replies, fmt.Sprintf("%d blocked inbound events from %s in the last 24 h", b.Hits, who))
		replyIPs = append(replyIPs, b.IP)
	}
	if len(replies) > 0 { // one suggestion for all of them: they are the same harmless thing
		title := fmt.Sprintf("Blocked packets from %s look like late replies, not a scan", replyIPs[0])
		if len(replies) > 1 {
			title = fmt.Sprintf("Blocked packets from %d addresses look like late replies, not a scan", len(replies))
		}
		out = append(out, Suggestion{
			Subject: "replies", Severity: SevInfo, Category: CatSecurity, Link: "firewall", Confidence: "medium",
			Title:    title,
			Why:      "Devices in your network talked to these addresses, and the firewall now blocks packets coming back from them. That usually happens after a connection was lost (WAN reconnect, modem resync, router restart): the replies arrive, the router no longer knows the connection and drops them. This is normal. A raw drop rule for these addresses would only break the connection.",
			Evidence: append(list3(replies, 8), "A LAN client exchanged traffic with each of these addresses in the last 24 h"),
			Limits:   "mtmon cannot see the packets themselves, only that these addresses talked to your network and were blocked at the same time. If you do not recognise a connection, look it up under Clients.",
		})
	}
	return out, scans
}

// originSuggestions groups the scan sources by country and provider and suggests dropping the dominant ones early.
func originSuggestions(out []Suggestion, bs []BlockedSrc) ([]Suggestion, string) {
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
		return out, "no GeoIP/ASN database configured"
	}
	if total < OriginMinTotal {
		return out, ""
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
		single := len(g.ips) > 0 && float64(g.ips[0].Hits) >= 0.8*float64(g.hits)
		for i, b := range g.ips {
			if i >= 5 {
				break
			}
			tops = append(tops, fmt.Sprintf("%s (%d)", b.IP, b.Hits))
			cmds = append(cmds, fmt.Sprintf("/ip firewall address-list add list=blocked-scanners address=%s timeout=7d comment=\"mtmon suggestion\"", b.IP))
		}
		steps := []string{
			"Add the busiest sources to an address list and drop them in the raw table, as in the commands below (they expire after 7 days).",
			"RouterOS has no built-in country filter. For a whole country or provider you would need a maintained list of its address ranges.",
		}
		if kind == "cc" {
			// A country is a poor filter criterion (CDNs and clouds sit in the same country as scanners), so no commands.
			cmds, steps = nil, []string{"Nothing to do: the firewall already blocks these. RouterOS has no built-in country filter, and dropping a whole country would also cut off legitimate services hosted there."}
		} else {
			cmds = append(cmds, "/ip firewall raw add chain=prerouting action=drop src-address-list=blocked-scanners in-interface-list=WAN comment=\"drop known scanners early\"")
		}
		title := fmt.Sprintf("Most blocked attempts come from %s", g.label)
		why := "Your firewall already blocks these attempts, so this is not an emergency. But a large share from one place means automated scanning, and dropping known sources very early (in the raw table) saves router CPU and keeps the logs readable."
		if single {
			title = fmt.Sprintf("Most blocked attempts come from one address, %s (%s)", g.ips[0].IP, g.label)
			why = "Your firewall already blocks these attempts, so this is not an emergency. Almost all of them come from a single address, so this is one persistent source rather than a wide scan by the whole provider or country. Dropping that address very early (in the raw table) is enough."
		}
		return Suggestion{
			Subject: kind + "-" + strings.ToLower(g.key), Severity: SevInfo, Category: CatSecurity, Link: "firewall", Confidence: "medium",
			Title: title,
			Why:   why,
			Evidence: []string{
				fmt.Sprintf("%d blocked inbound events came from %s in the last 24 h (%s%% of all blocked events with a known source)", g.hits, g.label, pct(share*100)),
				fmt.Sprintf("Busiest sources: %s", list(tops, 5)),
			},
			Steps:    steps,
			Commands: strings.Join(cmds, "\n"),
			Limits:   "Based on the most recent blocked events (at most 60,000 within 24 h) and the 300 busiest sources. The commands assume the default interface list WAN.",
		}
	}
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

// ruleFields parses FilterRule.Summary ("key=value key=value", values contain no spaces).
func ruleFields(f FilterRule) map[string]string {
	m := map[string]string{}
	for _, tok := range strings.Fields(f.Summary) {
		if k, v, ok := strings.Cut(tok, "="); ok {
			m[k] = v
		}
	}
	return m
}

var rfc1918 = map[string]bool{"192.168.0.0/16": true, "10.0.0.0/8": true, "172.16.0.0/12": true}

// wanZone: interface (list) names that face the internet. A drop of private ranges from there is a bogon filter, not isolation.
func wanZone(z string) bool {
	l := strings.ToLower(z)
	return strings.HasPrefix(l, "!") || strings.Contains(l, "wan") || strings.Contains(l, "internet") || strings.Contains(l, "pppoe") || l == "ether1"
}

// isolationFlags marks the drop rules that keep one segment (VLAN, interface list) away from the others. This is
// recognised by structure, the comment is only an extra hint:
//   - forward drop from a zone (in-interface / in-interface-list) to a private range (RFC1918),
//   - forward drop that only names a zone as destination (out-interface / out-interface-list): inbound into the zone,
//   - forward drop that only names a zone that already has such a rule ("drop rest").
func isolationFlags(rules []FilterRule) []bool {
	zones := map[string]bool{}
	zoneOf := func(m map[string]string, keys ...string) string {
		for _, k := range keys {
			if m[k] != "" {
				return m[k]
			}
		}
		return ""
	}
	flags := make([]bool, len(rules))
	fields := make([]map[string]string, len(rules))
	for i, f := range rules {
		fields[i] = ruleFields(f)
		if f.Disabled || f.Chain != "forward" || (f.Action != "drop" && f.Action != "reject") {
			continue
		}
		m := fields[i]
		if in := zoneOf(m, "in-interface-list", "in-interface"); in != "" && rfc1918[m["dst-address"]] && !wanZone(in) {
			flags[i], zones[in] = true, true
		}
		if out := zoneOf(m, "out-interface-list", "out-interface"); out != "" && zoneOf(m, "in-interface-list", "in-interface") == "" && m["dst-address"] == "" && !wanZone(out) {
			flags[i], zones[out] = true, true
		}
	}
	for i, f := range rules {
		if flags[i] || f.Disabled || f.Chain != "forward" || (f.Action != "drop" && f.Action != "reject") {
			continue
		}
		m := fields[i]
		z := zoneOf(m, "in-interface-list", "in-interface", "out-interface-list", "out-interface")
		if z != "" && zones[z] {
			flags[i] = true
			continue
		}
		t := strings.ToLower(f.Comment + " " + f.Summary) // extra hint: the comment says so
		if strings.Contains(t, "isolat") || (strings.Contains(t, "192.168") && strings.Contains(t, "10.0.0.0/8")) ||
			(strings.Contains(t, "192.168/16") && strings.Contains(t, "10/8")) {
			flags[i] = true
		}
	}
	return flags
}

func firingCount(s string) int64 {
	n, _ := strconv.ParseInt(s[:strings.Index(s, "|")], 10, 64)
	return n
}

func ruleDropNoLog(s Snapshot) ([]Suggestion, string) {
	var out []Suggestion
	seen := false
	for _, d := range routers(s) { // access points and switches only carry the default input rules: not worth logging
		if d.Caps == nil || len(d.Caps.Filter) == 0 {
			continue
		}
		seen = true
		var nolog []string
		var nologPk []int64 // packets of nolog[i] (0 = none or unknown), to list the busy rules first
		var iso []string
		var zero, fired, isoFired int
		drops := 0
		isoFlag := isolationFlags(d.Caps.Filter)
		var firing []string // not logged but matching packets right now
		for i, f := range d.Caps.Filter {
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
				if f.Counted {
					if f.Packets == 0 {
						zero++
					} else {
						fired++
					}
					txt += fmt.Sprintf(" [%d packets]", f.Packets)
				}
				if f.Counted && f.Packets > 0 {
					firing = append(firing, fmt.Sprintf("%d|%s", f.Packets, txt))
				}
				if isoFlag[i] {
					iso = append(iso, txt)
					if f.Counted && f.Packets > 0 {
						isoFired++
					}
				}
				nolog = append(nolog, txt)
				nologPk = append(nologPk, f.Packets)
			}
		}
		if len(nolog) == 0 {
			continue
		}
		order := make([]int, len(nolog))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool { return nologPk[order[a]] > nologPk[order[b]] })
		sorted := make([]string, len(nolog))
		for i, o := range order {
			sorted[i] = nolog[o]
		}
		nolog = sorted
		ev := []string{
			fmt.Sprintf("%d of %d active drop/reject rules on %s have logging switched off", len(nolog), drops, d.Name),
			fmt.Sprintf("Rules: %s", list(nolog, 4)),
			"Source: " + d.Caps.ReadNote(),
		}
		if len(firing) > 0 { // rules that really block something right now come first
			sort.Slice(firing, func(i, j int) bool { return firingCount(firing[i]) > firingCount(firing[j]) })
			for i := range firing {
				firing[i] = firing[i][strings.Index(firing[i], "|")+1:]
			}
			ev = append([]string{fmt.Sprintf("%d rule(s) drop packets right now without logging: %s", len(firing), list(firing, 4))}, ev...)
		}
		steps := []string{
			"Enable logging with a short prefix on the rules you want to see, preferably the last drop rule of the input and forward chains.",
			"Make sure the router sends firewall logs to mtmon (see the suggestion about syslog if present).",
		}
		if zero+fired > 0 {
			ev = append(ev, fmt.Sprintf("Rule counters: %d of these rules have matched packets, %d never did (0 packets: no hurry)", fired, zero))
		}
		cmds := "/ip firewall filter print where (action=drop or action=reject) and log=no\n# then for a rule number from that list:\n/ip firewall filter set <number> log=yes log-prefix=drop-input"
		if len(iso) > 0 {
			ev = append(ev, fmt.Sprintf("Isolation rules (blocking private ranges between segments): %s", list(iso, 3)))
			steps = append(steps, "Isolation rules between segments are usually intentional. Do not log every drop there: put a log-only rule with a rate limit directly in front of the isolation rule, so you see that it works without flooding the log.")
			cmds += "\n# isolation rule: log-only rule with a rate limit in front of it (copy the conditions of the drop rule)\n/ip firewall filter add chain=<chain> action=log log-prefix=isolation limit=5,10 place-before=<number of the drop rule> <same conditions as the drop rule>"
		}
		title := fmt.Sprintf("Blocked traffic on %s is not logged", d.Name)
		if isoFired > 0 {
			title = fmt.Sprintf("An isolation rule on %s blocks traffic you cannot see", d.Name)
			ev = append([]string{fmt.Sprintf("%d isolation rule(s) are matching packets right now but are not logged: you cannot tell who tries to cross the segment", isoFired)}, ev...)
		}
		out = append(out, Suggestion{
			Subject: d.Name, Severity: SevInfo, Category: CatMonitoring, Link: "firewall", Confidence: "high",
			Title:    title,
			Why:      "A drop rule without logging blocks silently. The Firewall page of mtmon can then not show what was blocked, who tried, and whether a rule works as intended. You do not need to log everything, but the final catch-all drop rules are worth it.",
			Evidence: ev,
			Steps:    steps,
			Fix:      &Fix{Device: d.Name, Features: []string{"firewall_logs"}},
			Commands: cmds,
			Limits:   "Based on the rules stored at the last router probe (hourly, and right after setup or a feature change); " + d.Caps.ReadNote() + ".",
		})
	}
	if !seen {
		return nil, "no stored firewall rules"
	}
	return out, ""
}
