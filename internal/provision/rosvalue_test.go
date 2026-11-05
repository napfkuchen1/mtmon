// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"context"
	"strings"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/poller"
	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestRosValue(t *testing.T) {
	for _, c := range []struct{ key, in, want string }{
		{"log", "true", "yes"}, {"log", "false", "no"}, {"log", "yes", "yes"}, {"log", "no", "no"},
		{"log", "", "no"}, {"log", "TRUE", "yes"}, {"log", " no ", "no"}, {"log", "garbage", "no"},
		{"disabled", "", "no"}, {"disabled", "true", "yes"}, {"enabled", "", "no"}, {"enabled", "false", "no"},
		{"passthrough", "", "yes"}, {"passthrough", "false", "no"},
		{"log-prefix", "", ""}, {"log-prefix", "true", "true"}, {"comment", "no", "no"},
	} {
		if got := rosValue(c.key, c.in); got != c.want {
			t.Errorf("rosValue(%q, %q) = %q, want %q", c.key, c.in, got, c.want)
		}
	}
	if rosBody(nil) != nil {
		t.Error("rosBody(nil) must stay nil")
	}
}

func TestCliSet(t *testing.T) {
	got := cliSet(map[string]string{".id": "*A", "log": "false", "log-prefix": "", "comment": `say "hi" $x`})
	want := `*A comment="say \"hi\" \$x" log=no log-prefix=""`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// Undo records written by older versions hold the values RouterOS rejects. They must still run, and the manual script
// built from them must be valid (#343: `log=""` is what left a router half changed).
func TestOldUndoRecordsAreRepaired(t *testing.T) {
	for _, old := range []string{"", "false"} {
		m, _, d := setup(t, "router")
		cl := poller.NewClient(d)
		ctx := context.Background()
		k, _ := Probe(ctx, cl)
		id := acceptID(t, k)
		m.Patch("/ip/firewall/filter", id, map[string]string{"log": "true", "log-prefix": "MTM-X"})
		e := store.ManifestEntry{ID: 1, Seq: 1, Kind: "fw_log", Descr: "Allow Logging enabled", State: "applied",
			Undo: `{"method":"POST","path":"/ip/firewall/filter/set","body":{".id":"` + id + `","log":"` + old + `","log-prefix":""}}`}
		script := ManualScript([]store.ManifestEntry{e})
		if !strings.Contains(script, "/ip firewall filter set "+id+` log=no log-prefix=""`) {
			t.Fatalf("old value %q, script:\n%s", old, script)
		}
		steps := Rollback(ctx, cl, []store.ManifestEntry{e}, nil)
		if len(steps) != 1 || !steps[0].OK {
			t.Fatalf("old value %q: rollback %+v", old, steps)
		}
		if f := filterByID(m, id); f["log"] != "false" || f["log-prefix"] != "" {
			t.Fatalf("old value %q: rule after rollback %v", old, f)
		}
	}
}

// A refused undo step reports the exact command, and the manual script lists only what is really left.
func TestFailedRollbackStepCarriesCommand(t *testing.T) {
	m, _, d := setup(t, "router")
	cl := poller.NewClient(d)
	ctx := context.Background()
	k, _ := Probe(ctx, cl)
	id := acceptID(t, k)
	entries := []store.ManifestEntry{
		{ID: 1, Seq: 1, Kind: "fw_log", Descr: "works", State: "applied", Undo: `{"method":"POST","path":"/ip/firewall/filter/set","body":{".id":"` + id + `","comment":"x"}}`},
		{ID: 2, Seq: 2, Kind: "fw_log", Descr: "refused", State: "applied", Undo: `{"method":"POST","path":"/ip/firewall/filter/set","body":{".id":"` + id + `","log":"false","log-prefix":""}}`},
	}
	m.FailSet = func(_ string, body map[string]any) bool { return body["log"] == "no" }
	states := map[int64]string{}
	steps := Rollback(ctx, cl, entries, func(id int64, st string) { states[id] = st })
	if len(steps) != 2 || steps[0].OK || !steps[1].OK {
		t.Fatalf("steps %+v", steps)
	}
	if want := "/ip firewall filter set " + id + ` log=no log-prefix=""`; steps[0].Cmd != want || steps[0].Msg == "" {
		t.Fatalf("cmd %q msg %q, want %q", steps[0].Cmd, steps[0].Msg, want)
	}
	if steps[1].Cmd != "" {
		t.Fatalf("a step that worked has no command: %q", steps[1].Cmd)
	}
	if states[1] != "reverted" || states[2] != "revert-failed" {
		t.Fatalf("states %v", states)
	}
	script := ManualScript(FailedEntries(entries, steps))
	if !strings.Contains(script, "refused") || strings.Contains(script, "works") {
		t.Fatalf("script:\n%s", script)
	}
}

// A failure in the last step must leave the router exactly as it was: apply plus rollback is a no-op (#343 MUST 6).
func TestFailureLeavesRouterUnchanged(t *testing.T) {
	userRule := func(t *testing.T, k *Caps, m interface {
		Patch(string, string, map[string]string)
	}) {
		t.Helper()
		for _, f := range k.Filter {
			if f.Action != "accept" {
				m.Patch("/ip/firewall/filter", f.ID, map[string]string{"log-prefix": "someone-elses"})
				return
			}
		}
		t.Fatal("mock router has no drop rule")
	}

	t.Run("apply fails at the verify step", func(t *testing.T) {
		m, _, d := setup(t, "router")
		cl := poller.NewClient(d)
		ctx := context.Background()
		k, _ := Probe(ctx, cl)
		userRule(t, k, m)
		k, _ = Probe(ctx, cl)
		before := m.Snapshot()
		o := DefaultOptions(k, "127.0.0.1")
		o.LogNew, o.DisableFasttrack = true, true
		badLogin := func(u, _ string) *poller.Client { x := d; x.User, x.Pass = u, "wrong"; return poller.NewClient(x) }
		res := Apply(ctx, cl, badLogin, k, o, func(store.ManifestEntry) {})
		if res.OK || res.Steps[len(res.Steps)-1].Key != "verify" {
			t.Fatalf("want failure at verify, got %+v", res.Steps)
		}
		for _, s := range res.Rollback {
			if !s.OK {
				t.Fatalf("rollback step failed: %+v", s)
			}
		}
		if after := m.Snapshot(); after != before {
			t.Fatalf("router changed\nbefore %s\nafter  %s", before, after)
		}
	})

	t.Run("features fail at the last write", func(t *testing.T) {
		m, _, d := setup(t, "router")
		cl := poller.NewClient(d)
		ctx := context.Background()
		k, _ := Probe(ctx, cl)
		userRule(t, k, m)
		k, _ = Probe(ctx, cl)
		before := m.Snapshot()
		o := DefaultOptions(k, "127.0.0.1")
		feats := map[string]bool{}
		for _, f := range FeatureKeys {
			feats[f] = true
		}
		m.FailSet = func(_ string, body map[string]any) bool { return body["disabled"] == "yes" } // only fasttrack writes that
		res := ApplyFeatures(ctx, cl, k, o, feats, 0, func(store.ManifestEntry) {})
		if res.OK || res.Steps[len(res.Steps)-1].Key != "fasttrack" || len(res.Rollback) < 3 {
			t.Fatalf("want failure at fasttrack after several changes: %+v rollback %+v", res.Steps, res.Rollback)
		}
		for _, s := range res.Rollback {
			if !s.OK {
				t.Fatalf("rollback step failed: %+v", s)
			}
		}
		if after := m.Snapshot(); after != before {
			t.Fatalf("router changed\nbefore %s\nafter  %s", before, after)
		}
	})
}

// A feature that is already in place plans nothing, and the Firewall page keeps its labels (#343 MUST 2).
func TestPlansAreIdempotent(t *testing.T) {
	m, _, d := setup(t, "router")
	cl := poller.NewClient(d)
	ctx := context.Background()
	k, _ := Probe(ctx, cl)
	id := acceptID(t, k)
	o := DefaultOptions(k, "127.0.0.1")
	o.SyslogReady, o.AllowRules = true, []string{id}
	feat := map[string]bool{FeatAllowLog: true}
	if len(PlanFeatures(k, o, feat)) == 0 {
		t.Fatal("first plan is empty")
	}
	if res := ApplyFeatures(ctx, cl, k, o, feat, 0, func(store.ManifestEntry) {}); !res.OK {
		t.Fatalf("apply: %s", res.Error)
	}
	k, _ = Probe(ctx, cl)
	if p := PlanFeatures(k, o, feat); len(p) != 0 {
		t.Fatalf("counted rule still planned: %+v", p)
	}
	before := m.Snapshot()
	res := ApplyFeatures(ctx, cl, k, o, feat, 10, func(e store.ManifestEntry) { t.Errorf("unexpected change: %+v", e) })
	if !res.OK || len(res.FwMeta) != 1 || !res.FwMeta[0].Counter {
		t.Fatalf("second apply ok=%v meta %+v", res.OK, res.FwMeta)
	}
	if m.Snapshot() != before {
		t.Fatal("second apply changed the router")
	}
}

// A rule someone else logs is never touched, in either list (#343 edge case).
func TestUserLoggingRuleIsLeftAlone(t *testing.T) {
	m, _, d := setup(t, "router")
	cl := poller.NewClient(d)
	ctx := context.Background()
	k, _ := Probe(ctx, cl)
	id := acceptID(t, k)
	m.Patch("/ip/firewall/filter", id, map[string]string{"log": "true", "log-prefix": "mine"})
	k, _ = Probe(ctx, cl)
	o := DefaultOptions(k, "127.0.0.1")
	o.SyslogReady, o.AllowRules = true, []string{id}
	if p := PlanFeatures(k, o, map[string]bool{FeatAllowLog: true}); len(p) != 0 {
		t.Fatalf("user rule planned: %+v", p)
	}
}
