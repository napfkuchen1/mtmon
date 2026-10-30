package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/live"
	"github.com/napfkuchen1/mtmon/internal/store"
)

func kinds(h []pathHop) string {
	s := ""
	for _, x := range h {
		s += x.Kind[:1]
	}
	return s
}

func TestBuildPathWiFiWeakSignal(t *testing.T) {
	in := pathIn{
		Client: store.Client{MAC: "AA:BB:CC:00:00:01", Hostname: "laptop", IP: "192.168.1.20", WiFi: true, Signal: -78, SSID: "home", Band: "5ghz", Device: "ap"},
		Chain: []pathDev{
			{Name: "ap", Role: "ap", Up: true, CPU: 10, Port: "ether2", PortKnown: true, PortUp: true, PortRx: 1e6, PortTx: 2e6},
			{Name: "rtr", Role: "router", Up: true, CPU: 20},
		},
		WAN: store.WANWindow{Sent: 100, AvgMS: 12},
		Dst: pathDst{Host: "netflix.com", IP: "1.2.3.4", CC: "US", ASN: 2906, ASOrg: "Netflix", Port: 443, Proto: 6, Bytes: 10, Conns: 2},
	}
	hops, weakest := buildPath(in)
	if got := kinds(hops); got != "cldldwne" {
		t.Fatalf("hop order = %s", got)
	}
	if hops[1].Kind != "link" || hops[1].Health != hBad || weakest != 1 {
		t.Fatalf("weak Wi-Fi must be the weakest hop: %+v weakest=%d", hops[1], weakest)
	}
	if hops[len(hops)-1].Kind != "endpoint" || hops[len(hops)-2].Kind != "network" || hops[len(hops)-3].Kind != "wan" {
		t.Fatalf("tail order wrong: %s", kinds(hops))
	}
}

func TestBuildPathVariants(t *testing.T) {
	root := []pathDev{{Name: "rtr", Role: "router", Up: true, CPU: 95}}
	for _, c := range []struct {
		name     string
		in       pathIn
		access   string
		weakest  string // kind of the weakest hop, "" = none
		accessOK string
	}{
		{"vpn", pathIn{Client: store.Client{MAC: "VPN:10.9.0.2", Device: "rtr", Iface: "wg0", Online: true}, Chain: root}, "VPN tunnel", "device", hOK},
		{"vpn stale", pathIn{Client: store.Client{MAC: "VPN:10.9.0.2", Device: "rtr", Iface: "wg0"}, Chain: root}, "VPN tunnel", "device", hWarn},
		{"cable down", pathIn{Client: store.Client{MAC: "AA:BB:CC:00:00:02", Device: "rtr", Iface: "ether3"}, Chain: root, AccessOK: true, Access: live.IfRate{Iface: "ether3"}}, "Cable ether3", "link", hBad},
		{"cable up", pathIn{Client: store.Client{MAC: "AA:BB:CC:00:00:02", Device: "rtr", Iface: "ether3"}, Chain: []pathDev{{Name: "rtr", Up: true}}, AccessOK: true, Access: live.IfRate{Iface: "ether3", Running: true}}, "Cable ether3", "", hOK},
		{"address only", pathIn{Client: store.Client{MAC: "9.9.9.9", IP: "9.9.9.9"}, AddrOnly: true}, "Unknown access", "", hUnknown},
	} {
		hops, weakest := buildPath(c.in)
		if hops[1].Title != c.access || hops[1].Health != c.accessOK {
			t.Errorf("%s: access hop = %q/%s, want %q/%s", c.name, hops[1].Title, hops[1].Health, c.access, c.accessOK)
		}
		got := ""
		if weakest >= 0 {
			got = hops[weakest].Kind
		}
		if got != c.weakest {
			t.Errorf("%s: weakest = %q, want %q", c.name, got, c.weakest)
		}
	}
}

func TestBuildPathWANAndDNS(t *testing.T) {
	hops, weakest := buildPath(pathIn{
		Client: store.Client{MAC: "AA:BB:CC:00:00:03", Device: "rtr", WiFi: true, Signal: -50},
		DNS:    "bypass", Chain: []pathDev{{Name: "rtr", Role: "router", Up: true}},
		WAN: store.WANWindow{Sent: 10, Lost: 2, LossPct: 20},
	})
	if hops[0].Health != hWarn || hops[0].Why == "" {
		t.Fatalf("DNS bypass must warn on the client: %+v", hops[0])
	}
	if hops[weakest].Kind != "wan" {
		t.Fatalf("20%% loss must make the WAN the weakest hop, got %s", hops[weakest].Kind)
	}
}

func TestPathChain(t *testing.T) {
	links := []deviceLink{{From: "rtr", To: "sw", Port: "ether2,bridge"}, {From: "sw", To: "ap", Port: "ether5"}}
	roleOf := map[string]string{"rtr": "router", "sw": "switch", "ap": "ap"}
	ifs := func(d string) []live.IfRate {
		return map[string][]live.IfRate{"sw": {{Iface: "ether5", RxBps: 5, Running: true}}}[d]
	}
	ch := pathChain("ap", "rtr", links, roleOf, map[string]live.DevLive{"ap": {Up: true}}, ifs)
	if len(ch) != 3 || ch[0].Name != "ap" || ch[1].Name != "sw" || ch[2].Name != "rtr" {
		t.Fatalf("chain = %+v", ch)
	}
	if !ch[0].PortKnown || ch[0].PortRx != 5 || ch[0].Port != "ether5" {
		t.Fatalf("uplink port rates missing: %+v", ch[0])
	}
	// unlinked device hangs off the root; a loop ends
	ch = pathChain("x", "rtr", nil, roleOf, nil, ifs)
	if len(ch) != 2 || ch[1].Name != "rtr" {
		t.Fatalf("unlinked chain = %+v", ch)
	}
	ch = pathChain("a", "", []deviceLink{{From: "a", To: "b"}, {From: "b", To: "a"}}, roleOf, nil, ifs)
	if len(ch) != 2 {
		t.Fatalf("loop must end: %+v", ch)
	}
}

func TestMatchDst(t *testing.T) {
	conns := []store.Conn{
		{RIP: "1.2.3.4", Host: "www.netflix.com", CC: "US", Bytes: 5, RPort: 443},
		{RIP: "1.2.3.4", Host: "www.netflix.com", Bytes: 7},
		{RIP: "5.6.7.8", Host: "other.org", Bytes: 1},
	}
	if d, ok := matchDst(conns, "1.2.3.4"); !ok || d.Bytes != 12 || d.Conns != 2 || d.CC != "US" {
		t.Fatalf("by ip: %+v %v", d, ok)
	}
	if d, ok := matchDst(conns, "Netflix.com"); !ok || d.IP != "1.2.3.4" {
		t.Fatalf("by host suffix: %+v %v", d, ok)
	}
	if _, ok := matchDst(conns, "nomatch.example"); ok {
		t.Fatal("unexpected match")
	}
	if hostMatches("evilnetflix.com", "netflix.com") {
		t.Fatal("suffix must be label-aligned")
	}
}

func TestPathEndpoint(t *testing.T) {
	ts, s := newTestServer(t)
	if r, _ := http.Get(ts.URL + "/api/path?client=1.2.3.4"); r.StatusCode != 401 {
		t.Fatalf("path without session: %d, want 401", r.StatusCode)
	}
	_, c := login(t, ts, "test-password-1")
	if r, _ := c.Get(ts.URL + "/api/path?client=ZZ:ZZ"); r.StatusCode != 404 {
		t.Fatalf("unknown client: %d, want 404", r.StatusCode)
	}
	s.St.UpsertClients([]store.Observation{{MAC: "AA:BB:CC:00:00:09", IP: "192.168.1.9", Present: true}})
	r, err := c.Get(ts.URL + "/api/path?client=aa:bb:cc:00:00:09&dst=example.org")
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("path: %v %v", err, r)
	}
	var out struct {
		Hops    []pathHop `json:"hops"`
		Weakest int       `json:"weakest"`
	}
	json.NewDecoder(r.Body).Decode(&out)
	if len(out.Hops) < 3 || out.Hops[0].Kind != "client" || out.Hops[len(out.Hops)-1].Title != "example.org" || out.Hops[0].Facts == nil {
		t.Fatalf("hops = %+v", out.Hops)
	}
}

func TestPathFactFormatting(t *testing.T) {
	for in, want := range map[string]string{"576500000": "576.5 Mbit/s", "1200000000": "1.2 Gbit/s", "866.7Mbps": "866.7Mbps", "": ""} {
		if got := rateText(in); got != want {
			t.Errorf("rateText(%q) = %q, want %q", in, got, want)
		}
	}
	if got := bytesText(2365757836); got != "2.4 GB" {
		t.Errorf("bytesText = %q", got)
	}
	if protoName(6) != "TCP" || protoName(17) != "UDP" || protoName(99) != "99" {
		t.Error("protoName")
	}
	hops, _ := buildPath(pathIn{Client: store.Client{MAC: "AA:BB:CC:00:00:09"}, Dst: pathDst{Host: "example.org"}})
	ep := hops[len(hops)-1]
	found := false
	for _, f := range ep.Facts {
		found = found || f.K == "Traffic"
	}
	if !found {
		t.Fatalf("endpoint without traffic must say so: %+v", ep)
	}
	hops, _ = buildPath(pathIn{Client: store.Client{MAC: "AA:BB:CC:00:00:09"}, Dst: pathDst{Host: "example.org", Port: 443, Proto: 6, Conns: 2, Bytes: 2365757836}})
	var port, by string
	for _, f := range hops[len(hops)-1].Facts {
		switch f.K {
		case "Port":
			port = f.V
		case "Bytes":
			by = f.V
		}
	}
	if port != "TCP/443" || by != "2.4 GB" {
		t.Fatalf("port=%q bytes=%q", port, by)
	}
}

func TestPathWANAddressFamily(t *testing.T) {
	famOf := func(clientIP, dst string) string {
		hops, _ := buildPath(pathIn{Client: store.Client{MAC: "AA:BB:CC:00:00:09", IP: clientIP}, Dst: pathDst{IP: dst}})
		for _, h := range hops {
			if h.Kind == "wan" {
				for _, f := range h.Facts {
					if f.K == "Address family of this target" {
						return f.V
					}
				}
			}
		}
		return ""
	}
	if got := famOf("192.168.1.5", "1.2.3.4"); got != "IPv4 (source NAT on the gateway)" {
		t.Errorf("v4 behind NAT: %q", got)
	}
	if got := famOf("fd00::5", "2606:4700::1111"); got != "IPv6 (no NAT)" {
		t.Errorf("v6: %q", got)
	}
	if got := famOf("192.168.1.5", ""); got != "" {
		t.Errorf("no target address must add no fact: %q", got)
	}
}
