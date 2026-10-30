package advice

import (
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestRuleDNSGhost(t *testing.T) {
	d := &Data{NowT: time.Now(), Traffic: []ClientTraffic{{MAC: "AA:BB:CC:00:00:50", Up: 1}},
		Cls:    []Client{{MAC: "AA:BB:CC:00:00:50", Hostname: "pc-office"}},
		Ghosts: []GhostDNS{{MAC: "AA:BB:CC:00:00:50", IP: "fd00:1a2b::1", Flows: 120, Last: time.Now().Unix()}, {MAC: "AA:BB:CC:00:00:51", IP: "10.9.9.9", Flows: 1}, {MAC: "AA:BB:CC:00:00:50", IP: "fe80::1", Flows: 500, Last: time.Now().Unix()}}}
	out, skip := ruleDNSGhost(d)
	if skip != "" || len(out) != 1 {
		t.Fatalf("got %d suggestions, skip %q", len(out), skip)
	}
	if !strings.Contains(out[0].Title, "pc-office") || !strings.Contains(out[0].Title, "fd00:1a2b::1") {
		t.Errorf("title: %s", out[0].Title)
	}
}

// #235: sightings stored before the pipeline knew the router's address disappear at the next advice run.
func TestGhostDNSSightingOfOwnAddressFiltered(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.NoteGhostDNS(map[[2]string]int64{{"AA:BB:CC:00:00:50", "192.168.0.1"}: 5, {"AA:BB:CC:00:00:50", "192.168.77.77"}: 5}, time.Now().Unix())
	l := NewLive(st, nil, nil, nil, time.Now())
	if n := len(l.GhostDNS7d()); n != 2 {
		t.Fatalf("without ownership data both stay: %d", n)
	}
	l.OwnsAddr = func(ip string) bool { return ip == "192.168.0.1" }
	g := l.GhostDNS7d()
	if len(g) != 1 || g[0].IP != "192.168.77.77" {
		t.Fatalf("%+v", g)
	}
}
