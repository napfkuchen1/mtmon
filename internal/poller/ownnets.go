package poller

import (
	"net/netip"
	"strings"
)

// ownV6Prefixes returns the global IPv6 prefixes a router uses itself: the prefixes of its global addresses, the
// prefix delegated to its DHCPv6 client and its prefix pools. ULA and link-local ranges are left out (local_nets
// covers them).
func ownV6Prefixes(addrs, dhcp, pools []Row) []netip.Prefix {
	var out []netip.Prefix
	add := func(s string) {
		// RouterOS may append the lifetime ("2001:db8::/56,6d23h") or a pool name
		for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
			p, err := netip.ParsePrefix(f)
			if err != nil || !p.Addr().Is6() || p.Addr().Is4In6() {
				continue
			}
			a := p.Addr()
			if a.IsLinkLocalUnicast() || a.IsLoopback() || a.IsMulticast() || a.IsUnspecified() || a.IsPrivate() {
				continue // IsPrivate covers fc00::/7
			}
			if p.Bits() < 16 { // never a whole range because of a bad row
				continue
			}
			out = append(out, p.Masked())
			return
		}
	}
	for _, r := range addrs {
		if !r.Bool("disabled") && !r.Bool("invalid") {
			add(r["address"])
		}
	}
	for _, r := range dhcp {
		if !r.Bool("disabled") {
			add(r["prefix"])
		}
	}
	for _, r := range pools {
		if !r.Bool("disabled") {
			add(r["prefix"])
		}
	}
	return out
}
