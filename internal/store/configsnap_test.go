package store

import "testing"

func TestConfigSnapRoundTripDiffAndKeep(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := ConfigSnap{
		"/ip/firewall/filter": {{".id": "*1", "chain": "input", "action": "accept", "comment": "ssh"}, {".id": "*2", "chain": "forward", "action": "drop"}},
		"/ip/address":         {{".id": "*5", "address": "10.0.0.1/24"}},
	}
	b := ConfigSnap{
		"/ip/firewall/filter": {{".id": "*1", "chain": "input", "action": "drop", "comment": "ssh"}, {".id": "*3", "chain": "input", "action": "accept", "comment": "new"}},
		// /ip/address missing: a failed read must not count as removed
	}
	if err := st.SaveConfigSnap("gw", 100, a); err != nil {
		t.Fatal(err)
	}
	got, err := st.ConfigSnapAt("gw", 100)
	if err != nil || len(got["/ip/firewall/filter"]) != 2 {
		t.Fatalf("round trip: %v %+v", err, got)
	}
	ch := DiffConfig(a, b)
	if len(ch) != 3 {
		t.Fatalf("want changed+added+removed, got %+v", ch)
	}
	kinds := map[string]string{}
	for _, c := range ch {
		kinds[c.ID] = c.Kind
	}
	if kinds["*1"] != "changed" || kinds["*3"] != "added" || kinds["*2"] != "removed" {
		t.Errorf("kinds: %v", kinds)
	}
	if len(DiffConfig(a, a)) != 0 {
		t.Error("identical snapshots must not differ")
	}
	for i := int64(1); i <= 35; i++ {
		st.SaveConfigSnap("gw", 200+i, a)
	}
	if n := len(st.ConfigSnapTimes("gw")); n != configSnapKeep {
		t.Errorf("kept %d, want %d", n, configSnapKeep)
	}
}

func TestDiffConfigIgnoresIDRenumbering(t *testing.T) {
	a := ConfigSnap{"/interface/wifi/access-list": {{".id": "*1", "interface": "*1A", "action": "reject"}}}
	b := ConfigSnap{"/interface/wifi/access-list": {{".id": "*1", "interface": "*2B", "action": "reject"}}}
	if ch := DiffConfig(a, b); len(ch) != 0 {
		t.Fatalf("id renumbering is no change: %+v", ch)
	}
	c := ConfigSnap{"/interface/wifi/access-list": {{".id": "*1", "interface": "cap-wifi1", "action": "reject"}}}
	if ch := DiffConfig(a, c); len(ch) != 0 {
		t.Fatalf("*ID resolved to cap name is no change: %+v", ch)
	}
	d := ConfigSnap{"/interface/wifi/access-list": {{".id": "*1", "interface": "*1A", "action": "accept"}}}
	if ch := DiffConfig(a, d); len(ch) != 1 {
		t.Fatalf("real edit must stay: %+v", ch)
	}
}
