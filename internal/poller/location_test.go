package poller

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/enrich"
	"github.com/napfkuchen1/mtmon/internal/live"
	"github.com/napfkuchen1/mtmon/internal/store"
)

type nopEvents struct{}

func (nopEvents) DeviceState(string, bool, string, float64, float64) {}
func (nopEvents) InterfaceState(string, string, bool)                {}
func (nopEvents) NewClient(string, string, string)                   {}

// hEX (router/CAPsMAN) lists every Wi-Fi client; the AP learns the Wi-Fi client on its own radio port, and the
// hEX sees it only through the trunk to the AP. The wired box hangs directly on the hEX.
func TestMergeLocatesClientsBehindAPs(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SaveNeighbors("hex", []store.Neighbor{{Device: "hex", Iface: "ether2", MAC: "AA:00:00:00:00:A1", Ident: "ap"}})
	st.SaveNeighbors("ap", []store.Neighbor{{Device: "ap", Iface: "ether1", MAC: "AA:00:00:00:00:B1", Ident: "hex"}})
	m := NewManager(&config.Config{Devices: []config.Device{{Name: "hex", Role: "router", Addr: "10.0.0.1"}, {Name: "ap", Role: "ap", Addr: "10.0.0.2"}}},
		st, live.NewHub(), enrich.New("", "", "", false), nopEvents{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Now()
	m.snaps["hex"] = &devSnap{role: "router", at: now,
		wifi: []Row{{"mac-address": "02:00:00:00:00:01", "interface": "cap-wifi1", "ssid": "Home", "signal": "-50"}},
		bridge: []Row{
			{"mac-address": "02:00:00:00:00:01", "on-interface": "ether2"}, // wifi client, via trunk to the AP
			{"mac-address": "02:00:00:00:00:02", "on-interface": "ether3"}, // wired box
			{"mac-address": "02:00:00:00:00:03", "on-interface": "ether2"}, // wired client behind the AP
		},
		arp: []Row{
			{"mac-address": "02:00:00:00:00:04", "address": "10.0.0.9", "status": "permanent", "dhcp": "true", "interface": "bridge"}, // lease only
			{"mac-address": "02:00:00:00:00:02", "address": "10.0.0.7", "status": "reachable", "interface": "bridge"},
		}}
	m.snaps["ap"] = &devSnap{role: "ap", at: now,
		bridge: []Row{
			{"mac-address": "02:00:00:00:00:01", "on-interface": "wifi1"},
			{"mac-address": "02:00:00:00:00:02", "on-interface": "ether1"}, // trunk to hEX
			{"mac-address": "02:00:00:00:00:03", "on-interface": "ether3"},
		}}
	m.merge()
	get := func(mac string) *store.Client {
		c, err := st.Client(mac)
		if err != nil {
			t.Fatalf("%s: %v", mac, err)
		}
		return c
	}
	if c := get("02:00:00:00:00:01"); c.Device != "ap" || !c.WiFi || c.SSID != "Home" || c.Signal != -50 {
		t.Errorf("wifi client must sit on the AP and keep its wifi data: %+v", c)
	}
	if c := get("02:00:00:00:00:02"); c.Device != "hex" || c.Iface != "ether3" {
		t.Errorf("wired box: %+v", c)
	}
	if c := get("02:00:00:00:00:03"); c.Device != "ap" || c.Iface != "ether3" {
		t.Errorf("wired client behind the AP: %+v", c)
	}
	if c, err := st.Client("02:00:00:00:00:04"); err == nil && c.Online {
		t.Errorf("a DHCP-created permanent ARP entry is not proof of presence: %+v", c)
	}
}
