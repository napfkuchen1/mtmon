package advice

import (
	"strings"
	"testing"
)

func ownData() *Data {
	d := base()
	d.Devs = []Device{{Name: "gw", Role: "router", Caps: &Caps{Addresses: []string{"192.168.88.1/24 on bridge", "89.246.98.219/32 on pppoe-out1"}}}}
	return d
}

func TestBlockOriginOwnAddress(t *testing.T) {
	d := ownData()
	d.Blocked = []BlockedSrc{{IP: "89.246.98.219", Hits: 982, CC: "DE", ASN: 8881, ASOrg: "Versatel"}}
	d.Contact = map[string]bool{"89.246.98.219": true}
	out := expect(t, d, "fw-origin", 1)
	if !strings.Contains(out[0].Title, "own public address") || strings.Contains(out[0].Title, "late replies") || out[0].Commands != "" {
		t.Fatalf("%+v", out[0])
	}
}

func TestBlockOriginChecksEveryIP(t *testing.T) {
	d := base()
	for i := 0; i < 40; i++ {
		d.Blocked = append(d.Blocked, BlockedSrc{IP: "34.120.28." + itoa(i+1), Hits: 200, CC: "US", ASN: 396982, ASOrg: "Google"})
	}
	// the 35th address is a LAN peer: it must be dropped from the commands although it is beyond the first 30
	d.Contact = map[string]bool{"34.120.28.35": true}
	out := expect(t, d, "fw-origin", 3) // late-reply card + cc + asn
	for _, s := range out {
		if strings.Contains(s.Commands, "34.120.28.35 ") {
			t.Fatalf("contacted IP must not get a drop command: %s", s.Commands)
		}
		if strings.HasPrefix(s.Subject, "cc-") && s.Commands != "" {
			t.Fatalf("country card must not carry commands: %s", s.Commands)
		}
	}
}

func itoa(i int) string {
	return strings.TrimSpace(strings.Repeat(" ", 0) + string(rune('0'+i/10)) + string(rune('0'+i%10)))
}

func TestBand24SkipsIoT(t *testing.T) {
	d := base()
	a, b, c := wifiClient("aa1", "ap1", "Home", "2ghz-n", -55), wifiClient("bb1", "ap1", "Home", "2ghz-n", -60), wifiClient("cc1", "ap1", "Home", "5ghz-ac", -50)
	a.Vendor, b.Hostname = "Espressif Inc.", "shellyplus1pm-ab12"
	d.Cls = []Client{a, b, c}
	expect(t, d, "band-24", 0)
}

func TestWeakSignalIoTIsInfo(t *testing.T) {
	d := base()
	p := wifiClient("p1", "ap1", "Home", "2ghz-n", -85)
	p.Hostname = "neff-dishwasher"
	d.Cls = []Client{p}
	if s := expect(t, d, "weak-signal", 1); s[0].Severity != SevInfo {
		t.Fatalf("IoT only: want info, got %s", s[0].Severity)
	}
	d.Cls = append(d.Cls, wifiClient("p2", "ap1", "Home", "5ghz-ac", -85))
	if s := expect(t, d, "weak-signal", 1); s[0].Severity != SevTip {
		t.Fatalf("mixed: want tip, got %s", s[0].Severity)
	}
}

func TestChattyInfraAndServers(t *testing.T) {
	d := base()
	for _, m := range []string{"A", "B", "C", "D", "E"} {
		d.Traffic = append(d.Traffic, ClientTraffic{MAC: m, Fl: 1000})
	}
	d.Traffic = append(d.Traffic, ClientTraffic{MAC: "192.168.216.1", Fl: 90000}, ClientTraffic{MAC: "H", Fl: 90000})
	d.Devs = []Device{{Name: "gw", Addr: "192.168.88.1", Role: "router", Caps: &Caps{Addresses: []string{"192.168.216.1/24 on wg-back", "fd00:1a2b::1/64 on wg-back"}}}}
	d.Cls = []Client{{MAC: "H", Hostname: "homeassistant"}}
	expect(t, d, "chatty", 0)
	d.Traffic[6].MAC = "fd00:1a2b::1"
	expect(t, d, "chatty", 0)
}

func TestChattySkipsVPNPeers(t *testing.T) {
	d := base()
	for _, m := range []string{"A", "B", "C", "D", "E"} {
		d.Traffic = append(d.Traffic, ClientTraffic{MAC: m, Fl: 1000})
	}
	d.Traffic = append(d.Traffic, ClientTraffic{MAC: "192.168.216.4", Fl: 90000})
	d.Devs = []Device{{Name: "gw", Addr: "192.168.88.1", Role: "router", Caps: &Caps{Addresses: []string{"192.168.216.1/24 on back-to-home-vpn"}}}}
	expect(t, d, "chatty", 0)
	d.Devs[0].Caps.Addresses = []string{"192.168.216.1/24 on bridge"}
	expect(t, d, "chatty", 1)
}

func TestDropNoLogCountersAndIsolation(t *testing.T) {
	d := base()
	d.Devs = []Device{{Name: "gw", Role: "router", Caps: &Caps{Filter: []FilterRule{
		{Chain: "forward", Action: "drop", Comment: "AGENTS50: block 192.168/16, 10/8, 172.16/12, drop rest inbound", Packets: 0, Counted: true},
	}}}}
	s := expect(t, d, "fw-nolog", 1)
	ev := strings.Join(s[0].Evidence, "\n")
	if !strings.Contains(ev, "never did") || !strings.Contains(ev, "Isolation rules") || !strings.Contains(s[0].Commands, "action=log") {
		t.Fatalf("%s\n%s", ev, s[0].Commands)
	}
}

// Real rules of a router (comments without "isolat"): the VLAN is an interface list, found by structure.
func TestDropNoLogIsolationByStructure(t *testing.T) {
	d := base()
	rules := []FilterRule{
		{Chain: "forward", Action: "drop", Comment: "AGENTS50: block 192.168/16", Summary: "in-interface-list=AGENTS dst-address=192.168.0.0/16", Packets: 56, Counted: true},
		{Chain: "forward", Action: "drop", Comment: "AGENTS50: block 10/8", Summary: "in-interface-list=AGENTS dst-address=10.0.0.0/8", Counted: true},
		{Chain: "forward", Action: "drop", Comment: "AGENTS50: drop rest", Summary: "in-interface-list=AGENTS", Log: true, Packets: 33, Counted: true},
		{Chain: "forward", Action: "drop", Comment: "AGENTS50: drop rest inbound", Summary: "out-interface-list=AGENTS", Packets: 332, Counted: true},
		{Chain: "forward", Action: "drop", Comment: "bogons", Summary: "in-interface-list=WAN dst-address=10.0.0.0/8", Packets: 7, Counted: true},
	}
	flags := isolationFlags(rules)
	for i, want := range []bool{true, true, true, true, false} {
		if flags[i] != want {
			t.Errorf("rule %d: isolation=%v, want %v", i, flags[i], want)
		}
	}
	d.Devs = []Device{{Name: "gw", Role: "router", Caps: &Caps{Filter: rules}}}
	s := expect(t, d, "fw-nolog", 1)
	ev := strings.Join(s[0].Evidence, "\n")
	line := ""
	for _, e := range s[0].Evidence {
		if strings.Contains(e, "drop packets right now without logging") {
			line = e
		}
	}
	iso := ""
	for _, e := range s[0].Evidence {
		if strings.HasPrefix(e, "Isolation rules") {
			iso = e
		}
	}
	if !strings.Contains(s[0].Title, "isolation rule") || line == "" || strings.Index(line, "[332 packets]") > strings.Index(line, "[56 packets]") ||
		strings.Contains(line, "[33 packets]") || strings.Contains(iso, "bogons") || !strings.Contains(iso, "drop rest inbound") {
		t.Fatalf("%s\n%s", s[0].Title, ev)
	}
}

// A tunnel is recognised by the interface type, whatever its name; a VPN peer listed as a client is never "chatty".
func TestChattyTunnelTypeAndVPNPeerClient(t *testing.T) {
	d := base()
	for _, m := range []string{"A", "B", "C", "D", "E"} {
		d.Traffic = append(d.Traffic, ClientTraffic{MAC: m, Fl: 1000})
	}
	d.Traffic = append(d.Traffic, ClientTraffic{MAC: "10.77.0.4", Fl: 90000})
	d.Devs = []Device{{Name: "gw", Addr: "192.168.88.1", Role: "router", Caps: &Caps{Addresses: []string{"10.77.0.1/24 on roadwarriors"}}}}
	expect(t, d, "chatty", 1) // nothing says this interface is a VPN
	d.Devs[0].TunnelIfs = []string{"roadwarriors"}
	expect(t, d, "chatty", 0) // interface type wg
	d.Devs[0].TunnelIfs = nil
	d.Traffic[5].MAC = "VPN:10.77.0.4"
	d.Cls = []Client{{MAC: "VPN:10.77.0.4", Hostname: "alice", Vendor: "WireGuard VPN", IP: "10.77.0.4"}}
	expect(t, d, "chatty", 0) // the peer is a known VPN client
	if !isTunnelType("wg") || !isTunnelType("ovpn-in") || isTunnelType("ether") || isTunnelType("bridge") {
		t.Error("isTunnelType")
	}
}

// #235: only clients that were seen on 5 GHz (or are a typical dual-band class) are listed, pinned ones never.
func TestBand24NeedsProofAndSkipsPinned(t *testing.T) {
	d := base()
	a, b, c := wifiClient("aa1", "ap1", "Home", "2ghz-n", -55), wifiClient("bb1", "ap1", "Home", "2ghz-n", -60), wifiClient("cc1", "ap1", "Home", "5ghz-ac", -50)
	d.Cls = []Client{a, b, c}
	expect(t, d, "band-24", 0) // media boxes that never used 5 GHz
	d.Cls[0].Seen5, d.Cls[1].Seen5 = true, true
	s := expect(t, d, "band-24", 1)
	if !strings.Contains(s[0].Evidence[0], "already seen on 5 GHz") {
		t.Fatal(s[0].Evidence)
	}
	// pinned by an access-list rule: left out
	d.Devs = []Device{{Name: "ap1", Caps: &Caps{PinnedMACs: []string{strings.ToLower(a.MAC)}}}}
	expect(t, d, "band-24", 0)
	// OUI class only
	d.Devs = nil
	d.Cls[0].Seen5, d.Cls[1].Seen5 = false, false
	d.Cls[0].Vendor, d.Cls[1].Vendor = "Apple, Inc.", "Samsung Electronics"
	s = expect(t, d, "band-24", 1)
	if !strings.Contains(s[0].Evidence[0], "usually supports 5 GHz") {
		t.Fatal(s[0].Evidence)
	}
}
