package advice

import (
	"fmt"
	"net/netip"
	"time"
)

// ruleDNSGhost: a client has a DNS server configured that does not exist in the network. It only works because the
// router redirects DNS (dst-nat) and answers for it; without that rule the client would have no name resolution.
func ruleDNSGhost(s Snapshot) ([]Suggestion, string) {
	if len(s.Traffic24h()) == 0 {
		return nil, "no flow data in the last 24 h"
	}
	idx := clientIndex(s)
	var out []Suggestion
	for _, g := range s.GhostDNS7d() {
		if g.Flows < 3 {
			continue // a one-off is no pattern
		}
		if a, err := netip.ParseAddr(g.IP); err == nil && a.IsLinkLocalUnicast() {
			continue // the router's own link-local address (RDNSS), also in sightings stored before this was excluded
		}
		name := nameOf(idx, g.MAC)
		out = append(out, Suggestion{
			Subject: g.MAC, Severity: SevTip, Category: CatHygiene, Link: "clients", Confidence: "medium",
			Title: fmt.Sprintf("%s uses a DNS server that does not exist (%s)", name, g.IP),
			Why:   "This device sends its DNS requests to an address that no device in the network owns, typically an old static DNS entry. It only works because the router redirects DNS requests to itself and answers for the missing server. Without that redirect rule the device would have no name resolution, and the traffic is counted against an imaginary host.",
			Evidence: []string{
				fmt.Sprintf("%d answers came from %s, an address without router, client, ARP/ND entry or lease", g.Flows, g.IP),
				"last seen " + time.Unix(g.Last, 0).Format("2006-01-02 15:04"),
			},
			Steps: []string{
				"On the device set the DNS server to automatic (DHCP / router advertisement) and remove the static entry for " + g.IP + ".",
			},
			Limits: "mtmon sees only flows: if " + g.IP + " is a real host that no router has an ARP/ND entry or lease for, this is a false alarm. Direct queries to the router itself are normal when the DNS redirect skips local destinations (dst-address-type=!local).",
		})
	}
	return out, ""
}
