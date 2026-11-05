// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"context"
	"strings"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/mockros"
	"github.com/napfkuchen1/mtmon/internal/poller"
	"github.com/napfkuchen1/mtmon/internal/store"
)

func filterState(m *mockros.Server, menu string) map[string]string {
	out := map[string]string{}
	for _, f := range m.Lists(menu) {
		out[f[".id"]] = f["log"] + "|" + f["log-prefix"] + "|" + f["disabled"]
	}
	return out
}

// A router set up by an IPv4-only version: the firewall log is on, no IPv6 rule logs. The addition plans the IPv6 step
// and nothing else; applying it leaves the IPv4 rules and the log target alone, and undo resets it.
func TestAdditionIPv6OnlyTouchesIPv6(t *testing.T) {
	m, _, d := setup(t, "router")
	cl := poller.NewClient(d)
	ctx := context.Background()
	k, err := Probe(ctx, cl)
	if err != nil {
		t.Fatal(err)
	}
	o := DefaultOptions(k, "10.0.0.5")
	var v4 []string
	for _, id := range o.FwRules {
		if !strings.HasPrefix(id, v6Prefix) {
			v4 = append(v4, id)
		}
	}
	o.FwRules = v4
	seq := 0
	if res := ApplyFeatures(ctx, cl, k, o, map[string]bool{FeatFwLog: true}, 0, func(e store.ManifestEntry) { seq = e.Seq }); !res.OK {
		t.Fatalf("setup: %+v", res)
	}
	if k, err = Probe(ctx, cl); err != nil {
		t.Fatal(err)
	}

	if a := Additions(k, map[string]bool{FeatFlow: true}); len(a) != 0 {
		t.Fatalf("firewall log off, nothing to complete: %+v", a)
	}
	adds := Additions(k, map[string]bool{FeatFwLog: true})
	if len(adds) != 1 || adds[0].Key != AddIPv6Firewall || adds[0].Feature != FeatFwLog || len(adds[0].Rules) != 1 {
		t.Fatalf("additions: %+v", adds)
	}

	o = DefaultOptions(k, "10.0.0.5")
	o.AddRules = adds[0].Rules
	plan := PlanFeatures(k, o, map[string]bool{})
	if len(plan) != 2 || plan[0].Key != "backup" || plan[1].Key != "fwlog" || len(plan[1].Commands) != 1 ||
		!strings.HasPrefix(plan[1].Commands[0], "/ipv6 firewall filter set") || !strings.Contains(plan[1].Commands[0], "MTM6-") {
		t.Fatalf("plan: %+v", plan)
	}

	v4Before, logging, actions := filterState(m, "/ip/firewall/filter"), len(m.Lists("/system/logging")), len(m.Lists("/system/logging/action"))
	var added []store.ManifestEntry
	res := ApplyFeatures(ctx, cl, k, o, map[string]bool{}, seq, func(e store.ManifestEntry) { added = append(added, e) })
	if !res.OK {
		t.Fatalf("apply: %+v", res)
	}
	if len(added) != 1 || added[0].Path != "/ipv6/firewall/filter" || FeatureOf(added[0]) != FeatFwLog || added[0].Seq <= seq {
		t.Fatalf("manifest: %+v", added)
	}
	for id, s := range filterState(m, "/ip/firewall/filter") {
		if v4Before[id] != s {
			t.Errorf("IPv4 rule %s changed: %s -> %s", id, v4Before[id], s)
		}
	}
	if len(m.Lists("/system/logging")) != logging || len(m.Lists("/system/logging/action")) != actions {
		t.Error("log target touched")
	}
	logged := func() (n int) {
		for _, f := range m.Lists("/ipv6/firewall/filter") {
			if f["log"] == "true" {
				n++
			}
		}
		return n
	}
	if logged() != 1 {
		t.Fatalf("IPv6 rules logging: %d", logged())
	}

	// done: nothing left to offer
	if k, err = Probe(ctx, cl); err != nil {
		t.Fatal(err)
	}
	if a := Additions(k, map[string]bool{FeatFwLog: true}); len(a) != 0 {
		t.Fatalf("still offered: %+v", a)
	}

	// undo goes the same way as for the rest of the feature
	for _, r := range Rollback(ctx, cl, added, nil) {
		if !r.OK {
			t.Fatalf("rollback: %+v", r)
		}
	}
	if logged() != 0 {
		t.Fatalf("IPv6 rules still logging: %d", logged())
	}
}

// A drop rule that already logs belongs to whoever set it: no addition for it. Disabled rules are not offered either.
func TestAdditionSkipsRulesThatNeedNothing(t *testing.T) {
	k := &Caps{Filter6: []FilterRule{
		{ID: v6Prefix + "*1", Action: "drop", Log: true, LogPrefix: "mine"},
		{ID: v6Prefix + "*2", Action: "drop", Disabled: true},
		{ID: v6Prefix + "*3", Action: "accept"},
		{ID: v6Prefix + "*4", Action: "drop", Managed: true, Log: true, LogPrefix: "MTM6-4"},
	}}
	if a := Additions(k, map[string]bool{FeatFwLog: true}); len(a) != 0 {
		t.Fatalf("%+v", a)
	}
	if a := Additions(nil, map[string]bool{FeatFwLog: true}); a == nil || len(a) != 0 {
		t.Fatalf("nil caps: %#v", a)
	}
}
