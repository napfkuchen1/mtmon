package poller

import (
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// Peers as RouterOS 7.24 reports them for Back to Home (dynamic, comment carries the user) and a plain WireGuard setup.
func TestVPNObservations(t *testing.T) {
	obs := vpnObservations("gw", []Row{
		{"name": "peer1", "interface": "back-to-home-vpn", "comment": "back-to-home-vpn alice", "allowed-address": "192.168.216.2/32,fd00::2/128", "last-handshake": "30s"},
		{"name": "laptop", "interface": "wg0", "comment": "work laptop", "allowed-address": "10.9.0.5/32", "last-handshake": "1h2m"},
		{"name": "branch", "interface": "wg0", "allowed-address": "10.20.0.0/24"},                 // site-to-site: a network, not a client
		{"name": "off", "interface": "wg0", "allowed-address": "10.9.0.7/32", "disabled": "true"}, // disabled
		{"interface": "wg0", "allowed-address": "10.9.0.8/32"},                                    // no name at all: the address
	})
	if len(obs) != 3 {
		t.Fatalf("%d observations: %+v", len(obs), obs)
	}
	a, b, c := obs[0], obs[1], obs[2]
	if a.MAC != "VPN:192.168.216.2" || a.Hostname != "alice" || a.IP != "192.168.216.2" || a.Vendor != VPNVendor || !a.Present || a.Device != "gw" || a.Iface != "back-to-home-vpn" {
		t.Errorf("Back to Home peer: %+v", a)
	}
	if b.Hostname != "laptop" || b.Present { // peer name beats the comment; last handshake an hour ago: not online
		t.Errorf("plain peer: %+v", b)
	}
	if c.Hostname != "10.9.0.8" {
		t.Errorf("unnamed peer: %+v", c)
	}
}

// The observation goes through the normal client store and is found again by its tunnel address (flows are matched by IP).
func TestVPNPeerBecomesClient(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	obs := vpnObservations("gw", []Row{{"name": "peer1", "interface": "wg0", "comment": "back-to-home-vpn alice", "allowed-address": "192.168.216.2/32", "last-handshake": "10s"}})
	if _, err := st.UpsertClients(obs); err != nil {
		t.Fatal(err)
	}
	cs, _ := st.Clients()
	if len(cs) != 1 || cs[0].Label != "" || cs[0].Hostname != "alice" || cs[0].Vendor != VPNVendor || !cs[0].Online {
		t.Fatalf("%+v", cs)
	}
	if got := st.MACForIPAt("192.168.216.2", time.Now().Unix()); got != "VPN:192.168.216.2" {
		t.Errorf("MACForIPAt: %q", got)
	}
}

// All host addresses of a peer are aliases of the one client; an IPv6-only peer is a client as well.
func TestVPNAliases(t *testing.T) {
	obs := vpnObservations("gw", []Row{
		{"name": "p", "interface": "wg0", "allowed-address": "192.168.216.4/32,fd00::4/128", "last-handshake": "10s"},
		{"name": "v6", "interface": "wg0", "allowed-address": "fd00::9/128", "last-handshake": "10s"},
		{"name": "net", "interface": "wg0", "allowed-address": "fd00:1::/64"},
	})
	if len(obs) != 2 {
		t.Fatalf("%+v", obs)
	}
	if obs[0].MAC != "VPN:192.168.216.4" || len(obs[0].Aliases) != 2 || obs[0].Aliases[1] != "fd00::4" {
		t.Errorf("dual-stack peer: %+v", obs[0])
	}
	if obs[1].MAC != "VPN:FD00::9" || obs[1].IP != "fd00::9" {
		t.Errorf("IPv6-only peer: %+v", obs[1])
	}
}
