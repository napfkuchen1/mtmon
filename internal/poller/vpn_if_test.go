// SPDX-License-Identifier: AGPL-3.0-only

package poller

import (
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// A tunnel is found by name or type; only peers with a recent handshake count as active, one address per family.
func TestVPNIfTable(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetIfNames("gw", []store.IfName{
		{Idx: 96, Name: "back-to-home-vpn", Type: "wg"},
		{Idx: 97, Name: "wg-plain", Type: "wg"},
		{Idx: 85, Name: "dslite", Type: "ipip"},
	}); err != nil {
		t.Fatal(err)
	}
	m := &Manager{St: st, snaps: map[string]*devSnap{"gw": {at: time.Now(), flowActive: 5 * time.Minute, peers: []Row{
		{"interface": "back-to-home-vpn", "allowed-address": "192.168.216.2/32,fd00::2/128", "last-handshake": "10s"},
		{"interface": "back-to-home-vpn", "allowed-address": "192.168.216.3/32", "last-handshake": "1h"},
		{"interface": "wg-plain", "allowed-address": "10.9.0.0/24", "last-handshake": "10s"}, // site-to-site: no client
	}}}}
	v, ok := m.VPNIf("gw", 96)
	if v.HoldSec != 600 {
		t.Errorf("hold %d, want twice the active flow timeout", v.HoldSec)
	}
	if !ok || len(v.Active) != 2 || v.Active[0].String() != "192.168.216.2" || v.Active[1].String() != "fd00::2" {
		t.Errorf("BTH: %v %+v", ok, v)
	}
	if v, ok := m.VPNIf("gw", 97); !ok || len(v.Active) != 0 {
		t.Errorf("plain: %v %+v", ok, v)
	}
	if _, ok := m.VPNIf("gw", 85); ok {
		t.Error("the DS-Lite interface is no WireGuard tunnel")
	}
	if _, ok := m.VPNIf("other", 96); ok {
		t.Error("unknown device")
	}
}
