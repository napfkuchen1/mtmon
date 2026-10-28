package poller

import (
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestIsSelf(t *testing.T) {
	m := &Manager{Cfg: &config.Config{Devices: []config.Device{{Name: "gw", Addr: "192.168.0.1"}}}, self: map[string]string{}}
	m.noteSelfMACs("gw", []Row{{"mac-address": "04:f4:1c:29:a5:dc"}, {"name": "x"}})
	cases := []struct {
		mac, ip string
		want    bool
	}{
		{"04:F4:1C:29:A5:DC", "", true},            // own interface MAC (case-insensitive)
		{"AA:BB:CC:00:00:01", "192.168.0.1", true}, // managed device address
		{"AA:BB:CC:00:00:02", "192.168.0.50", false},
	}
	if d, ok := m.SelfDevice("04:F4:1C:29:A5:DC"); !ok || d != "gw" {
		t.Errorf("SelfDevice = %q,%v want gw,true", d, ok)
	}
	for _, c := range cases {
		if got := m.isSelf(c.mac, c.ip); got != c.want {
			t.Errorf("isSelf(%s,%s)=%v want %v", c.mac, c.ip, got, c.want)
		}
	}
}

func TestSelfMACByAddr(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	caps := `{"addresses":["192.168.0.1/24 on bridge","2001:db8:1::1/64 on pppoe-out1","fe80::1/64 on bridge"]}`
	if err := st.UpsertDevice(store.DeviceRow{Name: "gw", Addr: "192.168.0.1", Scheme: "https", Port: 443, Caps: []byte(caps)}); err != nil {
		t.Fatal(err)
	}
	m := &Manager{Cfg: &config.Config{Devices: []config.Device{{Name: "gw", Addr: "192.168.0.1"}}}, St: st, self: map[string]string{}}
	m.noteSelfMACs("gw", []Row{{"mac-address": "04:F4:1C:29:A5:DD"}, {"mac-address": "04:F4:1C:29:A5:DC"}})
	for ip, want := range map[string]string{
		"2001:db8:1::1": "04:F4:1C:29:A5:DC", // dynamic WAN address on the PPPoE interface: the router, not a client
		"192.168.0.1":   "04:F4:1C:29:A5:DC",
		"2001:db8:1::5": "", // a client in the same prefix
		"fe80::1":       "", // link-local is left to the EUI-64 logic
	} {
		if got := m.SelfMACByAddr(ip); got != want {
			t.Errorf("SelfMACByAddr(%s) = %q, want %q", ip, got, want)
		}
	}
	if d, ok := m.SelfByAddr("2001:db8:1::1"); !ok || d != "gw" {
		t.Errorf("SelfByAddr with the stored 'addr on iface' format = %q,%v", d, ok)
	}
}

func TestWifiPerDevice(t *testing.T) {
	obs := []store.Observation{
		{MAC: "A", Device: "cap1", WiFi: true, Present: true}, {MAC: "B", Device: "cap1", WiFi: true, Present: true},
		{MAC: "C", Device: "cap2", WiFi: true, Present: true}, {MAC: "D", Device: "gw", WiFi: false, Present: true},
		{MAC: "E", Device: "cap2", WiFi: true, Present: false}, {MAC: "F", Device: "other", WiFi: true, Present: true},
	}
	got := wifiPerDevice(obs, []config.Device{{Name: "gw"}, {Name: "cap1"}, {Name: "cap2"}})
	if got["cap1"] != 2 || got["cap2"] != 1 || got["gw"] != 0 || len(got) != 3 {
		t.Fatalf("wifiPerDevice = %v", got)
	}
}

func TestUplinkPorts(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// the AP sees the router on ether1 (trunk); the camera is an ordinary neighbor and must not count
	st.SaveNeighbors("ap", []store.Neighbor{
		{Device: "ap", Iface: "bridge/ether1", MAC: "AA:00:00:00:00:01", Ident: "hex"},
		{Device: "ap", Iface: "ether3", MAC: "AA:00:00:00:00:02", Ident: "camera"},
	})
	m := &Manager{Cfg: &config.Config{Devices: []config.Device{{Name: "hex"}, {Name: "ap"}}}, St: st}
	up := m.uplinkPorts()
	if !up["ap|ether1"] || up["ap|ether3"] || len(up) != 1 {
		t.Fatalf("uplinkPorts = %v", up)
	}
}

// A PPPoE reconnect gives the router a new IPv6 address that the daily capability probe does not know yet: the
// per-poll /ipv6/address read must already count it as the router (#122).
func TestSelfAddrAfterPPPoEChange(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	caps := `{"addresses":["192.168.0.1/24 on bridge","2001:db8:1::1/64 on pppoe-out1"]}`
	if err := st.UpsertDevice(store.DeviceRow{Name: "gw", Addr: "192.168.0.1", Scheme: "https", Port: 443, Caps: []byte(caps)}); err != nil {
		t.Fatal(err)
	}
	m := &Manager{Cfg: &config.Config{Devices: []config.Device{{Name: "gw", Addr: "192.168.0.1"}}}, St: st,
		self: map[string]string{}, snaps: map[string]*devSnap{"gw": {}}}
	// pppoe-out1 has no hardware address: only the real port MAC may become the router's key
	m.noteSelfMACs("gw", []Row{{"mac-address": "00:00:00:00:00:00"}, {"mac-address": "04:F4:1C:29:A5:DC"}})
	if _, ok := m.self["00:00:00:00:00:00"]; ok {
		t.Fatal("zero MAC must not be a router key")
	}
	if got := m.SelfMACByAddr("2001:db8:2::1"); got != "" {
		t.Fatalf("new address unknown before the poll: %q", got)
	}
	m.setSnap("gw", func(s *devSnap) { s.addrs6 = []Row{{"address": "2001:db8:2::1/64", "interface": "pppoe-out1"}} })
	m.addrAt = time.Time{} // let the cached address map refresh
	if got := m.SelfMACByAddr("2001:db8:2::1"); got != "04:F4:1C:29:A5:DC" {
		t.Fatalf("new PPPoE address must be the router: %q", got)
	}
	if d, ok := m.SelfByAddr("2001:db8:2::1"); !ok || d != "gw" {
		t.Fatalf("SelfByAddr = %q,%v", d, ok)
	}
	if d, ok := m.SelfDevice("00:00:00:00:00:00"); !ok || d != "gw" {
		t.Fatalf("zero MAC rows from older data count as router: %q,%v", d, ok)
	}
}
