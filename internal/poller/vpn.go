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
// client observations. Only peers with a single-host address (/32) are clients: a peer that routes a whole
// network is a site-to-site link and not one device. Name: Back to Home user (from the comment), peer name,
// comment, address. The key is the tunnel address, so renamed dynamic peers keep their history.
func vpnObservations(device string, peers []Row) []store.Observation {
	var out []store.Observation
	for _, p := range peers {
		if p.Bool("disabled") {
			continue
		}
		var host netip.Addr
		for _, a := range strings.Split(p["allowed-address"], ",") {
			if pf, err := netip.ParsePrefix(strings.TrimSpace(a)); err == nil && pf.Addr().Is4() && pf.Bits() == 32 {
				host = pf.Addr()
				break
			}
		}
		if !host.IsValid() {
			continue
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
			Device: device, Iface: p["interface"], Via: "WireGuard peer", Present: d > 0 && d <= 3*time.Minute})
	}
	return out
}
