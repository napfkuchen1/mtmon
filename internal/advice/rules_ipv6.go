package advice

import (
	"fmt"
	"sort"
	"time"
)

// RA lifetimes above these keep an old prefix alive on clients for days after the ISP handed out a new one.
const (
	raLongValid     = 2 * 24 * 3600 // seconds
	raLongPreferred = 24 * 3600
)

// ruleRALifetimes: with a dynamic IPv6 prefix (the ISP changes it on reconnect) the router announces the delegated
// prefix with the lifetimes of the ISP lease (often a day or more). After a prefix change clients keep the old
// address as preferred until that runs out and may pick a dead one as source address ("IPv6 is sometimes slow").
// Advice only; no router command is given because the exact RouterOS syntax is not verified here.
func ruleRALifetimes(s Snapshot) ([]Suggestion, string) {
	var cands []Device
	for _, d := range routers(s) {
		if d.Caps != nil && d.Caps.IPv6PD && d.Caps.RAValid > 0 {
			cands = append(cands, d)
		}
	}
	if len(cands) == 0 {
		return nil, "no router with a dynamic IPv6 prefix (DHCPv6 prefix request) and readable RA lifetimes"
	}
	idx := clientIndex(s)
	type row struct {
		mac      string
		prefixes []V6Prefix
	}
	var rows []row
	for mac, ps := range s.IPv6Prefixes14d() {
		if len(ps) >= 2 && !isVPNKey(mac) {
			rows = append(rows, row{mac, ps})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if len(rows[i].prefixes) != len(rows[j].prefixes) {
			return len(rows[i].prefixes) > len(rows[j].prefixes)
		}
		return rows[i].mac < rows[j].mac
	})
	var out []Suggestion
	for _, d := range cands {
		if d.Caps.RAValid <= raLongValid && d.Caps.RAPreferred <= raLongPreferred {
			continue
		}
		if len(rows) == 0 {
			continue // long lifetimes, but no client shows the effect (yet)
		}
		ev := []string{fmt.Sprintf("%s announces its delegated IPv6 prefix with valid lifetime %s and preferred lifetime %s (from the provider's lease)", d.Name, fmtSecs(d.Caps.RAValid), fmtSecs(d.Caps.RAPreferred)),
			fmt.Sprintf("%d clients had global IPv6 addresses from more than one /64 prefix in the last 14 days", len(rows))}
		for i, r := range rows {
			if i == 5 {
				ev = append(ev, fmt.Sprintf("…(+%d)", len(rows)-5))
				break
			}
			oldest := r.prefixes[len(r.prefixes)-1]
			ev = append(ev, fmt.Sprintf("%s: addresses from %d prefixes, newest %s, oldest seen %s ago", nameOf(idx, r.mac), len(r.prefixes), r.prefixes[0].Prefix,
				fmtSecs(int64(s.Now().Sub(time.Unix(oldest.First, 0)).Seconds()))))
		}
		out = append(out, Suggestion{
			Subject: d.Name, Severity: SevTip, Category: CatHygiene, Link: "device/" + d.Name, Confidence: "medium",
			Title:    fmt.Sprintf("Old IPv6 prefixes stay in use on clients after a prefix change (%s)", d.Name),
			Why:      "Your provider changes the delegated IPv6 prefix. The router announces it with the lifetimes of the provider's lease, so after a change clients keep the old address as preferred for up to that time and may choose a dead one as source address. Browsers hide this with fallbacks, so it shows up as \"IPv6 is sometimes slow or broken\".",
			Evidence: ev,
			Steps: []string{
				"Best fix: ask your provider for a static IPv6 prefix.",
				"Some providers keep the same prefix if the router asks for it again (a prefix hint on the DHCPv6 client); this depends on the provider.",
				"Avoid needless reconnects of the internet line, each one can mean a new prefix.",
				"Changing \"/ipv6 nd prefix default\" does not help here: it only applies to prefixes without lifetimes of their own (for example your ULA prefix).",
				"Old addresses on clients disappear when their lifetime runs out or when the client reconnects.",
			},
			Limits: "No ready-made command is shown: the RouterOS syntax of the prefix hint was not verified against the RouterOS documentation. mtmon only learns about client addresses from the router's neighbour table, so the list may be incomplete.",
		})
	}
	return out, ""
}

func isVPNKey(mac string) bool { return len(mac) > 4 && mac[:4] == "VPN:" }

func fmtSecs(s int64) string {
	switch {
	case s >= 86400*7 && s%(86400*7) == 0:
		return fmt.Sprintf("%d weeks", s/(86400*7))
	case s >= 86400:
		return fmt.Sprintf("%.1f days", float64(s)/86400)
	case s >= 3600:
		return fmt.Sprintf("%.1f hours", float64(s)/3600)
	}
	return fmt.Sprintf("%d minutes", s/60)
}
