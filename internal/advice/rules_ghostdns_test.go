package advice

import (
	"strings"
	"testing"
	"time"
)

func TestRuleDNSGhost(t *testing.T) {
	d := &Data{NowT: time.Now(), Traffic: []ClientTraffic{{MAC: "AA:BB:CC:00:00:50", Up: 1}},
		Cls:    []Client{{MAC: "AA:BB:CC:00:00:50", Hostname: "pc-office"}},
		Ghosts: []GhostDNS{{MAC: "AA:BB:CC:00:00:50", IP: "fd00:1a2b::1", Flows: 120, Last: time.Now().Unix()}, {MAC: "AA:BB:CC:00:00:51", IP: "10.9.9.9", Flows: 1}}}
	out, skip := ruleDNSGhost(d)
	if skip != "" || len(out) != 1 {
		t.Fatalf("got %d suggestions, skip %q", len(out), skip)
	}
	if !strings.Contains(out[0].Title, "pc-office") || !strings.Contains(out[0].Title, "fd00:1a2b::1") {
		t.Errorf("title: %s", out[0].Title)
	}
}
