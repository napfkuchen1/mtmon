// SPDX-License-Identifier: AGPL-3.0-only

package poller

import (
	"net/netip"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
)

func TestOwnV6Prefixes(t *testing.T) {
	got := ownV6Prefixes(
		[]Row{{"address": "2001:db8:1:2::1/64"}, {"address": "fe80::1/64"}, {"address": "fd00:1::1/64"}, {"address": "2001:db8:9::1/64", "disabled": "true"}},
		[]Row{{"prefix": "2001:db8:1::/56,6d23h"}},
		[]Row{{"prefix": "2001:db8:1::/56"}, {"prefix": "::/0"}, {"prefix": "garbage"}})
	want := []string{"2001:db8:1:2::/64", "2001:db8:1::/56", "2001:db8:1::/56"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i, p := range got {
		if p.String() != want[i] {
			t.Errorf("%d: %v want %s", i, p, want[i])
		}
	}
}

func TestLearnedNetsAreLocalAndExpire(t *testing.T) {
	c := &config.Config{}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	ps := ownV6Prefixes([]Row{{"address": "2001:db8:1:2::1/64"}}, nil, nil)
	now := time.Now()
	c.LearnNets(ps, now)
	in, out := netip.MustParseAddr("2001:db8:1:2::77"), netip.MustParseAddr("2001:db8:ffff::1")
	if !c.IsLocal(in) || c.IsLocal(out) {
		t.Errorf("learned prefix: in=%v out=%v", c.IsLocal(in), c.IsLocal(out))
	}
	c.LearnNets(nil, now.Add(config.LearnedNetsKeep+time.Hour)) // not reported for a week: forgotten
	if c.IsLocal(in) {
		t.Error("stale prefix still local")
	}
}

// #297: LAN-side IPv4 networks are learned, WAN side, tunnels and host addresses are not.
func TestOwnV4Prefixes(t *testing.T) {
	addrs := []Row{
		{"address": "192.168.0.1/24", "interface": "bridge"},
		{"address": "192.168.50.1/24", "interface": "vlan50"},
		{"address": "192.168.216.1/24", "interface": "vlan216"},
		{"address": "203.0.113.3/29", "interface": "ipipv6-tunnel1"},
		{"address": "192.168.178.2/24", "interface": "ether1"},
		{"address": "100.64.7.1/24", "interface": "vlan64"},
		{"address": "10.9.9.1/32", "interface": "lo"},
		{"address": "10.8.8.1/24", "interface": "bridge", "disabled": "true"},
		{"address": "2001:db8::1/64", "interface": "bridge"},
	}
	ifs := []Row{{"name": "ipipv6-tunnel1", "type": "ipipv6"}, {"name": "bridge", "type": "bridge"}, {"name": "ether1", "type": "ether"}}
	learn, att := ownV4Prefixes(addrs, ifs, map[string]bool{"ether1": true})
	var got []string
	for _, p := range learn {
		got = append(got, p.String())
	}
	want := []string{"192.168.0.0/24", "192.168.50.0/24", "192.168.216.0/24", "100.64.7.0/24"}
	if len(got) != len(want) {
		t.Fatalf("learned %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %s want %s", i, got[i], want[i])
		}
	}
	if len(att) != 2 { // the tunnel /29 and the transfer net on ether1
		t.Errorf("attached %v", att)
	}
	// without a WAN list nothing is learned: the upstream network would turn internal
	if l, _ := ownV4Prefixes(addrs, ifs, nil); len(l) != 0 {
		t.Errorf("no WAN list: %v", l)
	}
	c := &config.Config{}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.LearnNets(learn, time.Now())
	c.NoteAttached(att, time.Now())
	if !c.IsLocal(netip.MustParseAddr("100.64.7.20")) || c.IsLocal(netip.MustParseAddr("203.0.113.5")) {
		t.Error("learned CGNAT LAN must be local, tunnel /29 must stay remote")
	}
	if !c.InAttached(netip.MustParseAddr("203.0.113.5")) || c.InAttached(netip.MustParseAddr("8.8.8.8")) {
		t.Error("attached lookup")
	}
}
