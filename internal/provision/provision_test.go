package provision

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daniel/mtmon/internal/config"
	"github.com/daniel/mtmon/internal/mockros"
	"github.com/daniel/mtmon/internal/poller"
	"github.com/daniel/mtmon/internal/store"
)

func setup(t *testing.T, role string) (*mockros.Server, *httptest.Server, config.Device) {
	m := mockros.New("gw", role, "admin", "adminpw", nil)
	ts := httptest.NewServer(m.Handler())
	t.Cleanup(ts.Close)
	h := strings.TrimPrefix(ts.URL, "http://")
	i := strings.LastIndex(h, ":")
	port := 0
	for _, c := range h[i+1:] {
		port = port*10 + int(c-'0')
	}
	return m, ts, config.Device{Name: "gw", Addr: h[:i], Port: port, Scheme: "http", User: "admin", Pass: "adminpw"}
}

func ro(d config.Device) func(u, p string) *poller.Client {
	return func(u, p string) *poller.Client { d.User, d.Pass = u, p; return poller.NewClient(d) }
}

func TestProbeRouter(t *testing.T) {
	_, _, d := setup(t, "router")
	k, err := Probe(context.Background(), poller.NewClient(d))
	if err != nil {
		t.Fatal(err)
	}
	if k.Role != "router" || !k.ExportsFlow || !k.Fasttrack || !k.DHCPServer || !k.NAT || k.WifiStack != "none" || !k.HWOffload {
		t.Fatalf("caps: %+v", k)
	}
	if len(k.Filter) != 5 || k.Model == "" {
		t.Fatalf("filter %d model %q", len(k.Filter), k.Model)
	}
}

func TestProbeAP(t *testing.T) {
	_, _, d := setup(t, "ap")
	k, err := Probe(context.Background(), poller.NewClient(d))
	if err != nil {
		t.Fatal(err)
	}
	if k.Role != "ap" || k.ExportsFlow || k.WifiStack != "wifi" || len(k.WifiIfs) != 2 || k.WifiIfs[0].SSID != "HomeNet" {
		t.Fatalf("caps: %+v", k)
	}
}

func TestApplyAndRollback(t *testing.T) {
	m, _, d := setup(t, "router")
	cl := poller.NewClient(d)
	ctx := context.Background()
	k, _ := Probe(ctx, cl)
	o := DefaultOptions(k, "127.0.0.1")
	o.LogNew, o.DisableFasttrack = true, true
	if len(o.FwRules) != 3 { // 3 drop rules
		t.Fatalf("default fw rules %v", o.FwRules)
	}
	steps := BuildPlan(k, o)
	if len(steps) != 8 {
		t.Fatalf("steps %d", len(steps))
	}
	var entries []store.ManifestEntry
	before := m.Single("/ip/traffic-flow")
	res := Apply(ctx, cl, ro(d), k, o, func(e store.ManifestEntry) { entries = append(entries, e) })
	if !res.OK {
		t.Fatalf("apply failed: %s %+v", res.Error, res.Steps)
	}
	if m.Exports != 1 || res.Backup == "" || res.MonPass == "" {
		t.Fatal("backup/pass missing")
	}
	if got := m.Single("/ip/traffic-flow"); got["enabled"] != "yes" || got["active-flow-timeout"] != "30s" {
		t.Fatalf("flow %v", got)
	}
	if len(m.Lists("/ip/traffic-flow/target")) != 1 || len(m.Lists("/system/logging")) != 1 || len(res.FwMeta) != 4 {
		t.Fatalf("objects missing; meta=%v", res.FwMeta)
	}
	logged := 0
	for _, f := range m.Lists("/ip/firewall/filter") {
		if f["log"] == "yes" {
			logged++
		}
		if f["action"] == "fasttrack-connection" && f["disabled"] != "yes" {
			t.Fatal("fasttrack still on")
		}
	}
	if logged != 4 { // 3 drops + MTM-NEW
		t.Fatalf("logged=%d", logged)
	}
	// the read-only user really is read-only
	rc := ro(d)(res.MonUser, res.MonPass)
	if _, err := rc.Get(ctx, "/system/resource"); err != nil {
		t.Fatal(err)
	}
	if _, err := rc.Do(ctx, "PUT", "/user", map[string]string{"name": "x", "group": "full"}); err == nil {
		t.Fatal("read-only user could write")
	}
	if script := ManualScript(entries); !strings.Contains(script, "/ip firewall filter set") || !strings.Contains(script, "remove") {
		t.Fatalf("script:\n%s", script)
	}
	// offboard
	rb := Rollback(ctx, cl, entries, nil)
	for _, r := range rb {
		if !r.OK {
			t.Fatalf("rollback step failed: %+v", r)
		}
	}
	if got := m.Single("/ip/traffic-flow"); got["enabled"] != before["enabled"] || got["active-flow-timeout"] != before["active-flow-timeout"] {
		t.Fatalf("flow not restored: %v vs %v", got, before)
	}
	if len(m.Lists("/ip/traffic-flow/target")) != 0 || len(m.Lists("/system/logging")) != 0 || len(m.Lists("/system/logging/action")) != 0 {
		t.Fatal("leftover objects")
	}
	if len(m.Lists("/user")) != 1 || len(m.Lists("/user/group")) != 1 {
		t.Fatal("user/group left behind")
	}
	for _, f := range m.Lists("/ip/firewall/filter") {
		if f["log"] == "yes" || f["disabled"] == "yes" || strings.HasPrefix(f["comment"], ManagedTag) {
			t.Fatalf("filter rule not restored: %v", f)
		}
	}
}

func TestAutoRollbackOnFailure(t *testing.T) {
	m, _, d := setup(t, "router")
	cl := poller.NewClient(d)
	ctx := context.Background()
	k, _ := Probe(ctx, cl)
	o := DefaultOptions(k, "127.0.0.1")
	m.FailMenu = "/system/logging/action" // fails midway, after user + flow were created
	res := Apply(ctx, cl, ro(d), k, o, nil)
	if res.OK || !res.RolledBack || res.Error == "" {
		t.Fatalf("expected rollback: %+v", res)
	}
	if len(m.Lists("/user")) != 1 || len(m.Lists("/ip/traffic-flow/target")) != 0 || m.Single("/ip/traffic-flow")["enabled"] != "false" {
		t.Fatal("router not restored after failed apply")
	}
}

func TestVerifyFailureRollsBack(t *testing.T) {
	m, _, d := setup(t, "router")
	cl := poller.NewClient(d)
	ctx := context.Background()
	k, _ := Probe(ctx, cl)
	o := DefaultOptions(k, "10.9.9.9") // router will not accept logins from the real peer (127.0.0.1)
	res := Apply(ctx, cl, ro(d), k, o, nil)
	if res.OK || !res.RolledBack || !strings.Contains(res.Error, "login") {
		t.Fatalf("expected verify rollback: %+v", res)
	}
	if len(m.Lists("/user")) != 1 {
		t.Fatal("user left behind")
	}
}

func TestBackupFailureChangesNothing(t *testing.T) {
	m, _, d := setup(t, "router")
	m.FailExport = true
	cl := poller.NewClient(d)
	k, _ := Probe(context.Background(), cl)
	res := Apply(context.Background(), cl, ro(d), k, DefaultOptions(k, "127.0.0.1"), nil)
	if res.OK || res.RolledBack || len(m.Lists("/user")) != 1 {
		t.Fatalf("must abort cleanly: %+v", res)
	}
}

func TestGuardProtectsForeignObjects(t *testing.T) {
	m, _, d := setup(t, "router")
	cl := poller.NewClient(d)
	ctx := context.Background()
	k, _ := Probe(ctx, cl)
	var entries []store.ManifestEntry
	res := Apply(ctx, cl, ro(d), k, DefaultOptions(k, "127.0.0.1"), func(e store.ManifestEntry) { entries = append(entries, e) })
	if !res.OK {
		t.Fatal(res.Error)
	}
	// admin repurposes the mtmon group name's policy owner: rename the user -> guard must refuse deleting it
	for _, u := range m.Lists("/user") {
		if u["name"] == "mtmon" {
			cl.Do(ctx, "POST", "/user/set", map[string]string{".id": u[".id"], "name": "someone-else"})
		}
	}
	rb := Rollback(ctx, cl, entries, nil)
	skipped := 0
	for _, r := range rb {
		if !r.OK && strings.Contains(r.Msg, "changed since setup") {
			skipped++
		}
	}
	if skipped != 1 {
		t.Fatalf("expected exactly one guarded skip, got %d: %+v", skipped, rb)
	}
}
