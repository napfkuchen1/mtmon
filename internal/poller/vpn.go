package poller

import (
	"net/netip"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// VPNVendor marks clients that are VPN peers (shown with the VPN icon, skipped by the "chatty" suggestion).
const VPNVendor = "WireGuard VPN"

const bthPrefix = "back-to-home-vpn" // comment of Back to Home peers: "back-to-home-vpn <user>"

// vpnObservations turns the WireGuard peers of a router (/interface/wireguard/peers, includes Back to Home) into
// client observations. Only peers with single-host addresses (/32, /128) are clients: a peer that routes a whole
// network is a site-to-site link and not one device. All host addresses of a peer (IPv4 and IPv6) are aliases of
// the one client, so tunnel traffic over either family lands on it. Name: Back to Home user (from the comment), peer name,
// comment, address. The key is the tunnel address, so renamed dynamic peers keep their history.
func vpnObservations(device string, peers []Row) []store.Observation {
	var out []store.Observation
	for _, p := range peers {
		if p.Bool("disabled") {
			continue
		}
		var hosts []netip.Addr
		for _, a := range strings.Split(p["allowed-address"], ",") {
			if pf, err := netip.ParsePrefix(strings.TrimSpace(a)); err == nil && pf.Bits() == pf.Addr().BitLen() {
				hosts = append(hosts, pf.Addr())
			}
		}
		if len(hosts) == 0 {
			continue
		}
		// the first IPv4 host is the key (stable since 0.16); an IPv6-only peer is keyed by its first address
		host := hosts[0]
		for _, h := range hosts {
			if h.Is4() {
				host = h
				break
			}
		}
		var aliases []string
		for _, h := range hosts {
			aliases = append(aliases, h.String())
		}
		name := p["name"]
		comment := strings.TrimSpace(p["comment"])
		if rest, ok := strings.CutPrefix(comment, bthPrefix); ok && strings.TrimSpace(rest) != "" {
			name = strings.TrimSpace(rest)
		} else if name == "" {
			name = comment
		}
		if name == "" {
			name = host.String()
		}
		d := ParseDuration(strings.TrimSpace(p["last-handshake"]))
		out = append(out, store.Observation{MAC: "VPN:" + host.String(), Hostname: name, Vendor: VPNVendor, IP: host.String(),
			Device: device, Iface: p["interface"], Via: "WireGuard peer", Present: d > 0 && d <= 3*time.Minute, Aliases: aliases})
	}
	return out
}
