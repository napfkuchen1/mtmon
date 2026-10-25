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

// A cycle in which the AP does not list the client (aged-out bridge host) must not move it to the controller:
// that would be counted as a roam, and back again on the next cycle.
func TestMergeKeepsAPWhenEvidenceGapsOut(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SaveNeighbors("hex", []store.Neighbor{{Device: "hex", Iface: "ether2", MAC: "AA:00:00:00:00:A1", Ident: "ap"}})
	st.SaveNeighbors("ap", []store.Neighbor{{Device: "ap", Iface: "ether1", MAC: "AA:00:00:00:00:B1", Ident: "hex"}})
	m := NewManager(&config.Config{Devices: []config.Device{{Name: "hex", Role: "router", Addr: "10.0.0.1"}, {Name: "ap", Role: "ap", Addr: "10.0.0.2"}}},
		st, live.NewHub(), enrich.New("", "", "", false), nopEvents{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	const mac = "02:00:00:00:00:01"
	wifi := []Row{{"mac-address": mac, "interface": "cap-wifi1", "ssid": "Home", "signal": "-50"}}
	m.snaps["hex"] = &devSnap{role: "router", at: time.Now(), wifi: wifi}
	m.snaps["ap"] = &devSnap{role: "ap", at: time.Now(), bridge: []Row{{"mac-address": mac, "on-interface": "wifi1"}}}
	m.merge()
	m.snaps["ap"] = &devSnap{role: "ap", at: time.Now()} // gap
	m.merge()
	m.snaps["ap"] = &devSnap{role: "ap", at: time.Now(), bridge: []Row{{"mac-address": mac, "on-interface": "wifi1"}}}
	m.merge()
	c, err := st.Client(mac)
	if err != nil || c.Device != "ap" {
		t.Fatalf("client must stay on the AP: %+v %v", c, err)
	}
	if r, _ := st.Roams(mac); len(r) != 0 {
		t.Fatalf("no roam expected: %+v", r)
	}
}

// An unmanaged switch on the uplink port: wired devices behind it are learned only on that port. They belong to the
// port; the managed devices' own MACs do not become clients.
func TestMergeSharedUplinkPort(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SaveNeighbors("hex", []store.Neighbor{{Device: "hex", Iface: "ether2", MAC: "AA:00:00:00:00:A1", Ident: "ap"}})
	m := NewManager(&config.Config{Devices: []config.Device{{Name: "hex", Role: "router", Addr: "10.0.0.1"}, {Name: "ap", Role: "ap", Addr: "10.0.0.2"}}},
		st, live.NewHub(), enrich.New("", "", "", false), nopEvents{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Now()
	m.snaps["hex"] = &devSnap{role: "router", at: now, bridge: []Row{
		{"mac-address": "02:00:00:00:00:05", "on-interface": "ether2"}, // wired box behind the unmanaged switch
		{"mac-address": "aa:00:00:00:00:b1", "on-interface": "ether2"}, // the AP itself
		{"mac-address": "02:00:00:00:00:06", "on-interface": "ether2"}, // wired client of the AP
	}}
	m.snaps["ap"] = &devSnap{role: "ap", at: now, bridge: []Row{
		{"mac-address": "AA:00:00:00:00:B1", "on-interface": "bridge", "local": "true"},
		{"mac-address": "02:00:00:00:00:06", "on-interface": "ether3"},
	}}
	m.merge()
	c, err := st.Client("02:00:00:00:00:05")
	if err != nil || c.Device != "hex" || c.Iface != "ether2" || !c.Online || c.Via != "bridge host on hex/ether2 (shared port)" {
		t.Fatalf("box behind the unmanaged switch: %+v %v", c, err)
	}
	if c, err := st.Client("02:00:00:00:00:06"); err != nil || c.Device != "ap" {
		t.Fatalf("client with an edge port of its own: %+v %v", c, err)
	}
	if c, err := st.Client("AA:00:00:00:00:B1"); err == nil {
		t.Fatalf("a managed device is no client: %+v", c)
	}
}

// The modem on the WAN port shows up in the gateway's ARP table; it is the upstream, not a LAN client.
func TestMergeIgnoresUpstreamOnWANInterface(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := NewManager(&config.Config{Devices: []config.Device{{Name: "gw", Role: "router", Addr: "10.0.0.1"}}},
		st, live.NewHub(), enrich.New("", "", "", false), nopEvents{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.snaps["gw"] = &devSnap{role: "router", at: time.Now(),
		wan: wanInterfaces(
			[]Row{{"list": "WAN", "interface": "ether1"}, {"list": "LAN", "interface": "bridge"}},
			[]Row{{"dst-address": "0.0.0.0/0", "active": "true", "immediate-gw": "203.0.113.1%pppoe-out1"}}),
		arp: []Row{
			{"mac-address": "02:00:00:00:00:0A", "address": "192.168.100.1", "status": "reachable", "interface": "ether1"}, // modem
			{"mac-address": "02:00:00:00:00:0B", "address": "10.0.0.7", "status": "reachable", "interface": "bridge"},
		},
		bridge: []Row{{"mac-address": "02:00:00:00:00:0A", "on-interface": "ether1"}}}
	m.merge()
	if c, err := st.Client("02:00:00:00:00:0A"); err == nil {
		t.Errorf("modem on the WAN port must not be a client: %+v", c)
	}
	if c, err := st.Client("02:00:00:00:00:0B"); err != nil || !c.Online {
		t.Errorf("LAN client: %+v %v", c, err)
	}
	wan := wanInterfaces(nil, []Row{{"dst-address": "0.0.0.0/0", "active": "true", "gateway": "203.0.113.1%pppoe-out1"}, {"dst-address": "10.1.0.0/16", "gateway": "x%y"}})
	if !wan["pppoe-out1"] || wan["y"] {
		t.Errorf("default route interface: %v", wan)
	}
}

func TestSelfByAddr(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m := NewManager(&config.Config{Devices: []config.Device{{Name: "gw", Role: "router", Addr: "10.0.0.1"}}},
		st, live.NewHub(), enrich.New("", "", "", false), nopEvents{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := st.UpsertDevice(store.DeviceRow{Name: "gw", Addr: "10.0.0.1", Port: 443, Scheme: "https", Role: "router", User: "u"}); err != nil {
		t.Fatal(err)
	}
	st.SetDeviceCaps("gw", []byte(`{"addresses":["192.168.88.1/24","fd00::1/64"]}`))
	m.self["AA:BB:CC:11:22:33"] = "gw"
	for ip, want := range map[string]bool{"10.0.0.1": true, "192.168.88.1": true, "fd00::1": true, "fe80::a8bb:ccff:fe11:2233": true, "192.168.88.50": false, "fe80::1": false} {
		if dev, ok := m.SelfByAddr(ip); ok != want || (ok && dev != "gw") {
			t.Errorf("%s: %q %v", ip, dev, ok)
		}
	}
}

// The WAN list is authoritative: a downstream router's default route runs over its LAN bridge, which must not
// count as upstream. Without a list the default-route interface is the fallback.
func TestWANInterfacesListWins(t *testing.T) {
	members := []Row{{"list": "WAN", "interface": "ether1"}, {"list": "LAN", "interface": "ether2"}}
	routes := []Row{{"dst-address": "0.0.0.0/0", "active": "true", "immediate-gw": "192.168.1.1%bridge1"}}
	if w := wanInterfaces(members, routes); len(w) != 1 || !w["ether1"] {
		t.Errorf("list must win over the route: %v", w)
	}
	if w := wanInterfaces(nil, routes); !w["bridge1"] {
		t.Errorf("route fallback without a list: %v", w)
	}
}

func TestRemoveWANClients(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.DB.Exec(`INSERT INTO clients(mac,ip,device,iface,online,label,first_seen,last_seen) VALUES
		('02:00:00:00:00:0A','192.168.100.1','gw','ether1',0,'',1,1),
		('02:00:00:00:00:0B','10.0.0.7','gw','bridge',0,'',1,1),
		('02:00:00:00:00:0C','192.168.100.2','gw','ether1',1,'',1,1),
		('02:00:00:00:00:0D','192.168.100.3','gw','ether1',0,'keep me',1,1)`)
	if n := st.RemoveWANClients("gw", []string{"ether1"}); n != 1 {
		t.Fatalf("removed %d, want 1", n)
	}
	if _, err := st.Client("02:00:00:00:00:0A"); err == nil {
		t.Error("offline modem record must go")
	}
	for _, m := range []string{"02:00:00:00:00:0B", "02:00:00:00:00:0C", "02:00:00:00:00:0D"} {
		if _, err := st.Client(m); err != nil {
			t.Errorf("%s must stay: %v", m, err)
		}
	}
}

// The same MAC is learned on the uplink ports of several managed devices (router port to the unmanaged switch, and
// each AP's own uplink). The router's port, nearest to the gateway, is where the host hangs.
func TestSharedPortPrefersGatewayNearestDevice(t *testing.T) {
	for i := 0; i < 20; i++ { // map order is random: repeat
		st, err := store.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		st.SaveNeighbors("hex", []store.Neighbor{
			{Device: "hex", Iface: "ether2", MAC: "AA:00:00:00:00:A1", Ident: "ap1"},
			{Device: "hex", Iface: "ether5", MAC: "AA:00:00:00:00:A2", Ident: "ap2"}})
		st.SaveNeighbors("ap1", []store.Neighbor{{Device: "ap1", Iface: "ether1", MAC: "AA:00:00:00:00:C1", Ident: "hex"}})
		st.SaveNeighbors("ap2", []store.Neighbor{{Device: "ap2", Iface: "ether1", MAC: "AA:00:00:00:00:C1", Ident: "hex"}})
		m := NewManager(&config.Config{Devices: []config.Device{{Name: "hex", Role: "router", Addr: "10.0.0.1"},
			{Name: "ap1", Role: "ap", Addr: "10.0.0.2"}, {Name: "ap2", Role: "ap", Addr: "10.0.0.3"}}},
			st, live.NewHub(), enrich.New("", "", "", false), nopEvents{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		now := time.Now()
		cam := Row{"mac-address": "02:00:00:00:00:05", "on-interface": "ether2"}
		m.snaps["hex"] = &devSnap{role: "router", at: now, wan: map[string]bool{"ether1": true}, bridge: []Row{cam}}
		m.snaps["ap1"] = &devSnap{role: "ap", at: now, bridge: []Row{{"mac-address": "02:00:00:00:00:05", "on-interface": "ether1"}}}
		m.snaps["ap2"] = &devSnap{role: "ap", at: now, bridge: []Row{{"mac-address": "02:00:00:00:00:05", "on-interface": "ether1"}}}
		m.merge()
		c, err := st.Client("02:00:00:00:00:05")
		if err != nil || c.Device != "hex" || c.Iface != "ether2" {
			t.Fatalf("run %d: want hex/ether2, got %+v %v", i, c, err)
		}
		st.Close()
	}
}
