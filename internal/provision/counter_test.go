// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"context"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/poller"
	"github.com/napfkuchen1/mtmon/internal/store"
)

func filterByID(m interface{ Lists(string) []map[string]string }, id string) map[string]string {
	for _, f := range m.Lists("/ip/firewall/filter") {
		if f[".id"] == id {
			return f
		}
	}
	return nil
}

func acceptID(t *testing.T, k *Caps) string {
	t.Helper()
	for _, f := range k.Filter {
		if f.Action == "accept" {
			return f.ID
		}
	}
	t.Fatal("mock router has no accept rule")
	return ""
}

// Accept rules are counted, not logged (#278): only the prefix is set, log stays off, the rule is booked as counter.
func TestAllowRulesAreCounted(t *testing.T) {
	m, _, d := setup(t, "router")
	cl := poller.NewClient(d)
	ctx := context.Background()
	k, _ := Probe(ctx, cl)
	id := acceptID(t, k)
	o := DefaultOptions(k, "127.0.0.1")
	o.SyslogReady, o.AllowRules = true, []string{id}
	var entries []store.ManifestEntry
	res := ApplyFeatures(ctx, cl, k, o, map[string]bool{FeatAllowLog: true}, 0, func(e store.ManifestEntry) { entries = append(entries, e) })
	if !res.OK {
		t.Fatalf("apply: %s", res.Error)
	}
	f := filterByID(m, id)
	if f["log"] == "yes" || f["log-prefix"] != prefixFor(id) {
		t.Fatalf("rule must keep log off and carry the prefix: %v", f)
	}
	if len(res.FwMeta) != 1 || !res.FwMeta[0].Counter || FeatureOf(entries[0]) != FeatAllowLog {
		t.Fatalf("meta %+v entry %+v", res.FwMeta, entries)
	}
	Rollback(ctx, cl, entries, nil)
	if f := filterByID(m, id); f["log-prefix"] != "" {
		t.Fatalf("prefix not restored: %v", f)
	}
}

// Rules that an earlier version set to log every packet are switched to counting; foreign rules are left alone.
func TestConvertLoggedAllowRules(t *testing.T) {
	m, _, d := setup(t, "router")
	cl := poller.NewClient(d)
	ctx := context.Background()
	k, _ := Probe(ctx, cl)
	id := acceptID(t, k)
	if _, err := cl.Do(ctx, "POST", "/ip/firewall/filter/set", map[string]string{".id": id, "log": "yes", "log-prefix": prefixFor(id)}); err != nil {
		t.Fatal(err)
	}
	k, _ = Probe(ctx, cl)
	if got := ConvertibleAllow(k); len(got) != 1 || got[0] != id {
		t.Fatalf("convertible %v", got)
	}
	o := DefaultOptions(k, "127.0.0.1")
	o.SyslogReady, o.ConvertAllow = true, ConvertibleAllow(k)
	var entries []store.ManifestEntry
	res := ApplyFeatures(ctx, cl, k, o, map[string]bool{FeatAllowLog: true}, 0, func(e store.ManifestEntry) { entries = append(entries, e) })
	if !res.OK {
		t.Fatalf("apply: %s", res.Error)
	}
	f := filterByID(m, id)
	if f["log"] == "yes" || f["log"] == "true" || f["log-prefix"] != prefixFor(id) {
		t.Fatalf("log must be off, prefix kept: %v", f)
	}
	if len(res.FwMeta) != 1 || !res.FwMeta[0].Counter || FeatureOf(entries[0]) != FeatAllowLog {
		t.Fatalf("meta %+v", res.FwMeta)
	}
	// a rule with a prefix of its own is not mtmon's: never offered
	if _, err := cl.Do(ctx, "POST", "/ip/firewall/filter/set", map[string]string{".id": id, "log": "yes", "log-prefix": "mine"}); err != nil {
		t.Fatal(err)
	}
	k, _ = Probe(ctx, cl)
	if got := ConvertibleAllow(k); len(got) != 0 {
		t.Fatalf("foreign rule offered: %v", got)
	}
}
