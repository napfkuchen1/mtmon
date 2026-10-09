package advice

import "testing"

func regData(accPackets int64, counted bool, hits int64) *Data {
	d := base()
	d.Devs = []Device{{Name: "gw", Role: "router", Caps: &Caps{Filter: []FilterRule{
		{Chain: "forward", Action: "accept", Proto: "tcp", DPort: "443,8443", Packets: accPackets, Counted: counted, Summary: "accept tcp 443"},
		{Chain: "forward", Action: "drop", Proto: "tcp", DPort: "443", LogPrefix: "blk", Log: true, Summary: "drop tcp 443"},
	}}}}
	d.Rules = []FwRule{{Device: "gw", Prefix: "blk", Chain: "forward", Action: "drop", Hits7d: hits}}
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
