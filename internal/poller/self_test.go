package poller

import (
	"testing"

	"github.com/napfkuchen1/mtmon/internal/config"
)

func TestIsSelf(t *testing.T) {
	m := &Manager{Cfg: &config.Config{Devices: []config.Device{{Name: "gw", Addr: "192.168.0.1"}}}, self: map[string]bool{}}
	m.noteSelfMACs([]Row{{"mac-address": "04:f4:1c:29:a5:dc"}, {"name": "x"}})
	cases := []struct {
		mac, ip string
		want    bool
	}{
		{"04:F4:1C:29:A5:DC", "", true},            // own interface MAC (case-insensitive)
		{"AA:BB:CC:00:00:01", "192.168.0.1", true}, // managed device address
		{"AA:BB:CC:00:00:02", "192.168.0.50", false},
	}
	for _, c := range cases {
		if got := m.isSelf(c.mac, c.ip); got != c.want {
			t.Errorf("isSelf(%s,%s)=%v want %v", c.mac, c.ip, got, c.want)
		}
	}
}
