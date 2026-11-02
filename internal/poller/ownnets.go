// SPDX-License-Identifier: AGPL-3.0-only

package poller

import (
	"net/netip"
	"strings"

	"github.com/napfkuchen1/mtmon/internal/addr"
)

// tunnelTypes are interface types whose addresses are never a LAN: tunnels, VPNs and PPP links lead to the upstream or
// to a peer (a DS-Lite /29 on the tunnel must stay WAN side).
var tunnelTypes = []string{"ipip", "gre", "eoip", "6to4", "sit", "wg", "wireguard", "ovpn", "l2tp", "sstp", "pptp", "pppoe", "ppp", "lte", "vxlan", "zerotier", "ipsec"}

func isTunnelType(t string) bool {
	t = strings.ToLower(t)
	for _, p := range tunnelTypes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// ownV4Prefixes (#297) returns the IPv4 networks that sit on LAN-side interfaces of a router, so a routed public
// /29 or a CGNAT-range VLAN counts as internal without editing local_nets. learn holds the prefixes of addresses
// whose interface is neither on the WAN list nor a tunnel/PPP link; attached holds all other prefixes the router
// has (WAN side), which stay remote but are used to spot dropped transit traffic. Without a known WAN list
// nothing is learned (the upstream network would become internal).
func ownV4Prefixes(addrs, ifs []Row, wan map[string]bool) (learn, attached []netip.Prefix) {
	types := map[string]string{}
	for _, r := range ifs {
		types[r["name"]] = r["type"]
	}
	for _, r := range addrs {
		if r.Bool("disabled") || r.Bool("invalid") {
			continue
		}
		p, err := netip.ParsePrefix(r["address"])
		if err != nil || !p.Addr().Is4() || p.Bits() < 8 || p.Bits() > 30 {
			continue // IPv6, host addresses (/31, /32: loopbacks, VRRP, point-to-point) and silly masks
		}
		a := p.Addr()
		if a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsMulticast() || a.IsUnspecified() {
			continue
		}
		p = p.Masked()
		ifc := r["interface"]
		if wan[ifc] || isTunnelType(types[ifc]) || len(wan) == 0 {
			attached = append(attached, p)
			continue
		}
		learn = append(learn, p)
	}
	return learn, attached
}

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
			if addr.Classify(a) != addr.Public {
				continue // not the ISP prefix: link-local, loopback, multicast, unspecified, ULA
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
