// SPDX-License-Identifier: AGPL-3.0-only

package advice

import (
	"strings"
	"testing"
)

func regData(accPackets int64, counted bool, hits int64) *Data {
	d := base()
	d.Devs = []Device{{Name: "gw", Role: "router", Caps: &Caps{Filter: []FilterRule{
		{Chain: "forward", Action: "accept", Proto: "tcp", DPort: "443,8443", Packets: accPackets, Counted: counted, Summary: "accept tcp 443"},
		{Chain: "forward", Action: "drop", Proto: "tcp", DPort: "443", LogPrefix: "blk", Log: true, Summary: "drop tcp 443"},
	}}}}
	d.Rules = []FwRule{{Device: "gw", Prefix: "blk", Chain: "forward", Action: "drop", Hits7d: hits, Hits24h: hits}}
	return d
}

func TestRuleRegression(t *testing.T) {
	if got := find(Evaluate(regData(0, true, 500)), "fw-regression"); len(got) != 1 {
		t.Fatalf("shadowed accept rule: want 1 finding, got %d", len(got))
	}
	if got := find(Evaluate(regData(0, false, 500)), "fw-regression"); len(got) != 0 {
		t.Error("rule without a real counter reading must not be flagged")
	}
	if got := find(Evaluate(regData(40, true, 500)), "fw-regression"); len(got) != 0 {
		t.Error("accept rule with matches is fine")
	}
	if got := find(Evaluate(regData(0, true, 5)), "fw-regression"); len(got) != 0 {
		t.Error("few drops must not be flagged")
	}
}

// A sibling accept rule that carries the traffic means the zero-hit rule (e.g. a VPN-subnet exception) is fine.
func TestRuleRegressionSiblingAccept(t *testing.T) {
	d := base()
	d.Devs = []Device{{Name: "gw", Role: "router", Caps: &Caps{Filter: []FilterRule{
		{Chain: "input", Action: "accept", Proto: "tcp", DPort: "53", Packets: 1245, Counted: true, Summary: "accept tcp 53 rate-limit", Comment: "INPUT: DNS TCP/53 rate-limit accept"},
		{Chain: "input", Action: "accept", Proto: "tcp", DPort: "53", Packets: 0, Counted: true, Summary: "tcp src 192.168.216.0/24 dst-port 53", Comment: "WireGuard exception"},
		{Chain: "input", Action: "drop", Proto: "tcp", DPort: "53", LogPrefix: "blk", Log: true, Summary: "drop tcp 53"},
	}}}}
	d.Rules = []FwRule{{Device: "gw", Prefix: "blk", Chain: "input", Action: "drop", Hits24h: 400, Hits7d: 970}}
	if got := find(Evaluate(d), "fw-regression"); len(got) != 0 {
		t.Fatalf("sibling accept rule carries traffic, want no finding, got %+v", got)
	}
	d.Devs[0].Caps.Filter[0].Packets = 0
	got := find(Evaluate(d), "fw-regression")
	if len(got) != 1 || !strings.Contains(strings.Join(got[0].Evidence, "\n"), "All 2 accept rules") {
		t.Fatalf("all accept rules at 0: want 1 finding, got %+v", got)
	}
	// old drops (7 d) without current growth must not count
	d.Rules[0].Hits24h = 3
	if got := find(Evaluate(d), "fw-regression"); len(got) != 0 {
		t.Error("no drops in the last 24 h: no finding")
	}
}
