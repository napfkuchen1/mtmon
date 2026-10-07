package poller

import (
	"testing"

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
