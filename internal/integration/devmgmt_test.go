package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/api"
	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/enrich"
	"github.com/napfkuchen1/mtmon/internal/flow"
	"github.com/napfkuchen1/mtmon/internal/live"
	"github.com/napfkuchen1/mtmon/internal/mockros"
	"github.com/napfkuchen1/mtmon/internal/poller"
	"github.com/napfkuchen1/mtmon/internal/secret"
	"github.com/napfkuchen1/mtmon/internal/store"
	"github.com/napfkuchen1/mtmon/internal/syslog"
)

type apiRig struct {
	t    *testing.T
	ts   *httptest.Server
	c    *http.Client
	srv  *api.Server
	rt   *mockros.Server
	rts  *httptest.Server
	st   *store.Store
	pipe *live.Pipeline
	sysA net.Addr
}

func newAPIRig(t *testing.T) *apiRig {
	cfg := &config.Config{DataDir: t.TempDir(), NoTLS: true, AdminUser: "admin", SyslogListen: "127.0.0.1:0"}
	cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.AdminHash, _ = api.HashPassword("test-password-1")
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := live.NewHub()
	en := enrich.New("", "", "", false)
	cls := enrich.NewClassifier()
	pipe := live.NewPipeline(cfg, st, en, hub)
	pipe.Cls = cls
	box, _ := secret.Open(cfg.DataDir)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pm := poller.NewManager(cfg, st, hub, en, nopEvents{}, log)
	pm.Scale = 0.02
	sys := &syslog.Server{Addr: "127.0.0.1:0", Allow: cfg.SourceDevice, Handler: func(es []syslog.Event) {
		var out []store.FwEvent
		for _, e := range es {
			out = append(out, store.FwEvent{TS: e.TS, Device: e.Device, Prefix: e.Prefix, Chain: e.Chain, InIf: e.InIf, OutIf: e.OutIf, Proto: e.Proto,
				Src: e.Src, SPort: e.SPort, Dst: e.Dst, DPort: e.DPort, Len: e.Len, MAC: e.MAC})
		}
		st.EnqueueFw(out...)
	}}
	if err := sys.Listen(); err != nil {
		t.Fatal(err)
	}
	col := &flow.Collector{Dec: flow.NewDecoder()}
	srv := &api.Server{Cfg: cfg, St: st, Hub: hub, En: en, Col: col, Pipe: pipe, Version: "test", Box: box, Mgr: pm, Cls: cls, Sys: sys}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go pm.Run(ctx)
	go sys.Run(ctx)
	go st.Writer(ctx)
	ts := httptest.NewServer(api.New(srv))
	t.Cleanup(ts.Close)
	rt := mockros.New("gw-main", "router", "admin", "adminpw", clients)
	rts := httptest.NewServer(rt.Handler())
	t.Cleanup(rts.Close)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	resp, err := c.Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"user":"admin","password":"test-password-1"}`))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("login: %v %v", err, resp)
	}
	return &apiRig{t: t, ts: ts, c: c, srv: srv, rt: rt, rts: rts, st: st, pipe: pipe, sysA: sys.LocalAddr()}
}

type nopEvents struct{}

func (nopEvents) DeviceState(string, bool, string, float64, float64) {}
func (nopEvents) InterfaceState(string, string, bool)                {}
func (nopEvents) NewClient(string, string, string)                   {}

func (r *apiRig) call(method, path string, body any, out any) int {
	r.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, r.ts.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.c.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			r.t.Fatalf("%s %s: bad json %q: %v", method, path, b, err)
		}
	}
	return resp.StatusCode
}

func (r *apiRig) conn() map[string]any {
	h, p, _ := net.SplitHostPort(r.rts.Listener.Addr().String())
	return map[string]any{"addr": h, "port": atoi(p), "user": "admin", "pass": "adminpw", "scheme": "http"}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func (r *apiRig) sendSyslog(line string) {
	c, _ := net.Dial("udp", r.sysA.String())
	defer c.Close()
	c.Write([]byte(line))
}

func TestDeviceLifecycleEndToEnd(t *testing.T) {
	r := newAPIRig(t)

	// 1. probe: capabilities + plan, nothing changed on the router
	var pr struct {
		Caps     map[string]any
		CanWrite bool           `json:"can_write"`
		Options  map[string]any `json:"options"`
		Plan     []map[string]any
	}
	body := r.conn()
	if code := r.call("POST", "/api/devices/probe", body, &pr); code != 200 {
		t.Fatalf("probe %d", code)
	}
	if pr.Caps["role"] != "router" || !pr.CanWrite || len(pr.Plan) < 5 {
		t.Fatalf("probe result: %+v", pr)
	}
	if len(r.rt.Lists("/user")) != 1 || r.rt.Exports != 0 {
		t.Fatal("probe must not change the router")
	}
	for _, s := range pr.Plan {
		if s["why"] == "" || s["undo"] == "" {
			t.Fatalf("every step needs an explanation and undo text: %v", s)
		}
	}

	// 2. provision
	opt := pr.Options
	opt["name"] = "gw-main"
	opt["mtmon_ip"] = "127.0.0.1"
	opt["log_new"] = true
	body["options"] = opt
	var prov struct {
		Result struct {
			OK    bool
			Error string
			Steps []map[string]any
		}
		Name string
	}
	if code := r.call("POST", "/api/devices/provision", body, &prov); code != 200 || !prov.Result.OK {
		t.Fatalf("provision: %d %+v", code, prov)
	}
	if len(r.rt.Lists("/user")) != 2 || len(r.rt.Lists("/ip/traffic-flow/target")) != 1 {
		t.Fatal("router not configured")
	}
	var devs []map[string]any
	r.call("GET", "/api/devices", nil, &devs)
	if len(devs) != 1 || devs[0]["managed"] != true || devs[0]["source"] != "ui" || devs[0]["firewall_logging"] != true {
		t.Fatalf("devices: %v", devs)
	}
	// admin credentials must not be stored anywhere in the DB
	rows, _ := r.st.DeviceRows()
	if len(rows) != 1 || rows[0].User != "mtmon" || bytes.Contains(rows[0].PassEnc, []byte("adminpw")) {
		t.Fatalf("stored credentials wrong: %+v", rows[0])
	}
	var dump []byte
	r.st.DB.QueryRow(`SELECT group_concat(pass_enc || user, ',') FROM devices`).Scan(&dump)
	if bytes.Contains(dump, []byte("adminpw")) {
		t.Fatal("admin password leaked into DB")
	}

	// 3. the new device is polled without restart
	waitFor(t, "clients from new device", 8*time.Second, func() bool { c, _ := r.st.Clients(); return len(c) == 2 })

	// 4. firewall: blocked + allowed events via syslog, flows via collector path
	metas, _ := r.st.FwRules()
	var dropPrefix string
	for _, m := range metas {
		if m.Action == "drop" && strings.Contains(m.Descr, "invalid") {
			dropPrefix = m.Prefix
		}
	}
	if dropPrefix == "" {
		t.Fatalf("no drop rule registered: %+v", metas)
	}
	// unknown sender is ignored; known sender (127.0.0.1) accepted
	r.sendSyslog("firewall,info " + dropPrefix + " forward: in:bridge out:ether1, connection-state:invalid src-mac 3c:22:fb:10:00:01, proto TCP (ACK), 192.168.88.23:50000->203.0.113.99:8883, len 60")
	r.sendSyslog("not a firewall line")
	exp := netip.MustParseAddr("127.0.0.1")
	now := time.Now()
	rec := func(dst string, sp, dp uint16, proto uint8) flow.Record {
		return flow.Record{Exporter: exp, Src: netip.MustParseAddr("192.168.88.23"), Dst: netip.MustParseAddr(dst), SrcPort: sp, DstPort: dp, Proto: proto, Bytes: 1200, Packets: 4, End: now}
	}
	r.pipe.Handle([]flow.Record{rec("203.0.113.99", 50000, 8883, 6), rec("129.6.15.28", 40000, 123, 17), rec("93.184.216.34", 51000, 443, 6)})
	waitFor(t, "fw event stored", 5*time.Second, func() bool {
		var n int
		r.st.DB.QueryRow(`SELECT count(*) FROM fw_events`).Scan(&n)
		return n == 1
	})
	var sum struct {
		Enabled bool
		Summary struct{ Blocked int64 }
		Syslog  map[string]uint64
	}
	r.call("GET", "/api/firewall/summary?range=1h", nil, &sum)
	if !sum.Enabled || sum.Summary.Blocked != 1 || sum.Syslog["unparsed"] != 1 {
		t.Fatalf("fw summary: %+v", sum)
	}
	mac := "3C:22:FB:10:00:01"
	var conns []map[string]any
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end) && len(conns) != 3; time.Sleep(50 * time.Millisecond) {
		conns = nil
		r.call("GET", "/api/clients/"+mac+"/connections?range=1h", nil, &conns)
	}
	if len(conns) != 3 {
		var n int
		var m string
		r.st.DB.QueryRow(`SELECT count(*), coalesce(max(mac),'') FROM flows`).Scan(&n, &m)
		t.Fatalf("flows=%d mac=%q conns=%v", n, m, conns)
	}
	verdict := map[float64]string{}
	for _, c := range conns {
		verdict[c["rport"].(float64)], _ = c["verdict"].(string)
		if c["device"] != "gw-main" {
			t.Fatalf("connection must name the router that saw it: %v", c)
		}
	}
	// the connection to :8883 has a matching logged drop; flow exists too (retry/partial) -> blocked wins
	if verdict[8883] != "blocked" || verdict[123] != "allowed" || verdict[443] != "allowed" {
		t.Fatalf("verdicts: %v", verdict)
	}

	// 5. services: built-in NTP, then a custom rule relabels existing data
	waitFor(t, "rollup flushed", 5*time.Second, func() bool {
		var n int
		r.st.DB.QueryRow(`SELECT count(*) FROM rollup_1m`).Scan(&n)
		return n >= 3
	})
	var sd struct {
		Detail struct {
			Clients []map[string]any
			Dests   []map[string]any
		}
		Category string
	}
	r.call("GET", "/api/services/NTP?range=1h", nil, &sd)
	if len(sd.Detail.Clients) != 1 || sd.Category != "Time" || sd.Detail.Dests[0]["rip"] != "129.6.15.28" {
		t.Fatalf("NTP detail: %+v", sd)
	}
	var rule map[string]any
	if code := r.call("POST", "/api/service-rules", map[string]any{"name": "MQTT Cloud", "category": "IoT", "kind": "port", "value": "8883", "proto": 6}, &rule); code != 200 {
		t.Fatalf("rule add %d", code)
	}
	var bad map[string]any
	if code := r.call("POST", "/api/service-rules", map[string]any{"name": "x<script>", "kind": "port", "value": "1"}, &bad); code != 400 {
		t.Fatalf("invalid rule accepted: %d", code)
	}
	waitFor(t, "relabel", 8*time.Second, func() bool {
		var d struct{ Detail struct{ Clients []any } }
		r.call("GET", "/api/services/MQTT%20Cloud?range=1h", nil, &d)
		return len(d.Detail.Clients) == 1
	})
	waitFor(t, "reclass done", 5*time.Second, func() bool {
		var l struct{ Reclassifying bool }
		r.call("GET", "/api/services?range=1h", nil, &l)
		return !l.Reclassifying
	})
	// new flows are labelled on ingest
	r.pipe.Handle([]flow.Record{rec("203.0.113.99", 50001, 8883, 6)})
	var svcs struct {
		Services []struct{ Name, Category string }
	}
	waitFor(t, "ingest label", 5*time.Second, func() bool {
		r.call("GET", "/api/services?range=1h", nil, &svcs)
		for _, s := range svcs.Services {
			if s.Name == "MQTT Cloud" && s.Category == "IoT" {
				return true
			}
		}
		return false
	})
	// deleting the rule restores the built-in label (MQTT-TLS)
	r.call("DELETE", "/api/service-rules/"+itoa(rule["id"]), nil, nil)
	waitFor(t, "label restored", 8*time.Second, func() bool {
		var d struct{ Detail struct{ Clients []any } }
		r.call("GET", "/api/services/MQTT-TLS?range=1h", nil, &d)
		return len(d.Detail.Clients) == 1
	})

	// 5b. editing a device: role/site change is tested against the router and saved; a bad address changes nothing
	if code := r.call("PUT", "/api/devices/gw-main", map[string]any{"role": "switch", "site": "Keller"}, nil); code != 200 {
		t.Fatalf("edit: %d", code)
	}
	var after []map[string]any
	r.call("GET", "/api/devices", nil, &after)
	if len(after) != 1 || after[0]["role"] != "switch" || after[0]["site"] != "Keller" || after[0]["managed"] != true {
		t.Fatalf("edit result (managed flag must survive): %v", after)
	}
	if code := r.call("PUT", "/api/devices/gw-main", map[string]any{"addr": "8.8.8.8"}, nil); code != 400 {
		t.Fatalf("public address must be refused: %d", code)
	}
	if code := r.call("PUT", "/api/devices/gw-main", map[string]any{"port": 1}, nil); code != 502 {
		t.Fatalf("unreachable port must be refused: %d", code)
	}
	r.call("GET", "/api/devices", nil, &after)
	if after[0]["role"] != "switch" {
		t.Fatalf("a failed edit changed the device: %v", after)
	}

	// 6. offboarding with admin credentials restores the router
	var off struct {
		OK       bool
		Rollback []map[string]any
	}
	if code := r.call("POST", "/api/devices/gw-main/offboard", map[string]any{"user": "admin", "pass": "adminpw", "mode": "rollback"}, &off); code != 200 || !off.OK {
		t.Fatalf("offboard: %d %+v", code, off)
	}
	if len(r.rt.Lists("/user")) != 1 || len(r.rt.Lists("/ip/traffic-flow/target")) != 0 || len(r.rt.Lists("/system/logging")) != 0 {
		t.Fatal("router not restored")
	}
	for _, f := range r.rt.Lists("/ip/firewall/filter") {
		if f["log"] == "yes" || strings.HasPrefix(f["comment"], "mtmon-managed") {
			t.Fatalf("filter rule left changed: %v", f)
		}
	}
	devs = nil
	r.call("GET", "/api/devices", nil, &devs)
	if len(devs) != 0 {
		t.Fatalf("device still listed: %v", devs)
	}
	if rules, _ := r.st.FwRules(); len(rules) != 0 {
		t.Fatal("fw rule meta left")
	}
}

func itoa(v any) string {
	f, _ := v.(float64)
	return strings.TrimSuffix(strings.TrimSuffix(strings.Replace(jsonNum(f), ".0", "", 1), "e+00"), " ")
}

func jsonNum(f float64) string { b, _ := json.Marshal(f); return string(b) }

func TestProvisionRejectsPublicAndBadInput(t *testing.T) {
	r := newAPIRig(t)
	for _, b := range []map[string]any{
		{"addr": "8.8.8.8", "user": "a", "pass": "b"},
		{"addr": "169.254.169.254", "user": "a", "pass": "b"},
		{"addr": "10.0.0.1/../x", "user": "a", "pass": "b"},
		{"addr": "", "user": "a", "pass": "b"},
		{"addr": "192.168.1.1", "user": "a", "pass": "b", "scheme": "http"}, // plain http only on loopback
	} {
		var e map[string]string
		if code := r.call("POST", "/api/devices/probe", b, &e); code != 400 {
			t.Errorf("%v: got %d %v, want 400", b, code, e)
		}
	}
	// wrong credentials -> friendly 502, nothing stored
	b := r.conn()
	b["pass"] = "nope"
	var e map[string]string
	if code := r.call("POST", "/api/devices/probe", b, &e); code != 502 || !strings.Contains(e["error"], "Login") {
		t.Fatalf("bad creds: %d %v", code, e)
	}
}

func TestProvisionFailureLeavesNoDevice(t *testing.T) {
	r := newAPIRig(t)
	r.rt.FailMenu = "/system/logging/action"
	var pr struct{ Options map[string]any }
	body := r.conn()
	r.call("POST", "/api/devices/probe", body, &pr)
	pr.Options["name"], pr.Options["mtmon_ip"] = "gw-main", "127.0.0.1"
	body["options"] = pr.Options
	var prov struct {
		Result struct {
			OK         bool
			RolledBack bool `json:"rolled_back"`
			Error      string
		}
	}
	r.call("POST", "/api/devices/provision", body, &prov)
	if prov.Result.OK || !prov.Result.RolledBack {
		t.Fatalf("expected rolled back failure: %+v", prov)
	}
	if rows, _ := r.st.DeviceRows(); len(rows) != 0 {
		t.Fatal("device saved despite failure")
	}
	if m, _ := r.st.Manifest("gw-main"); len(m) != 0 {
		t.Fatal("manifest rows left after clean rollback")
	}
	if len(r.rt.Lists("/user")) != 1 || r.rt.Single("/ip/traffic-flow")["enabled"] != "false" {
		t.Fatal("router not restored")
	}
}

// Logging on allow rules and on new connections are features of their own: each one switches on and off alone, pulls in
// the firewall log target when it is missing, and goes away with it.
func TestDeviceFeaturesAllowLog(t *testing.T) {
	r := newAPIRig(t)
	body := r.conn()
	body["name"], body["role"] = "gw-ro", "router"
	if code := r.call("POST", "/api/devices/readonly", body, nil); code != 200 {
		t.Fatalf("readonly add: %d", code)
	}
	var fl struct {
		Rules []struct {
			ID, Action string
		} `json:"filter_rules"`
		Features []struct {
			Key string
			On  bool
		}
	}
	r.call("GET", "/api/devices/gw-ro/features", nil, &fl)
	accept := ""
	for _, x := range fl.Rules {
		if x.Action == "accept" {
			accept = x.ID
		}
	}
	if accept == "" {
		t.Fatal("mock router has no accept rule")
	}
	on := func(k string) bool {
		var f struct {
			Features []struct {
				Key string
				On  bool
			}
		}
		r.call("GET", "/api/devices/gw-ro/features", nil, &f)
		for _, x := range f.Features {
			if x.Key == k {
				return x.On
			}
		}
		return false
	}
	req := func(extra map[string]any) map[string]any {
		m := map[string]any{"user": "admin", "pass": "adminpw"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	var out map[string]any
	logging := func(prefix string) int {
		n := 0
		for _, f := range r.rt.Lists("/ip/firewall/filter") {
			if f["log"] == "yes" && strings.HasPrefix(f["log-prefix"], prefix) {
				n++
			}
		}
		return n
	}
	// allow logging alone: the syslog target is pulled in, the drop rules stay quiet
	if code := r.call("POST", "/api/devices/gw-ro/features", req(map[string]any{"enable": []string{"allow_log"}, "allow_rules": []string{accept}}), &out); code != 200 || out["ok"] != true {
		t.Fatalf("enable allow_log: %d %v", code, out)
	}
	if len(r.rt.Lists("/system/logging")) != 1 || logging("MTM-") != 1 || !on("allow_log") {
		t.Fatalf("logging=%d rules=%d allow=%v", len(r.rt.Lists("/system/logging")), logging("MTM-"), on("allow_log"))
	}
	// only the allow logging off: target and the rest stay
	if code := r.call("POST", "/api/devices/gw-ro/features", req(map[string]any{"disable": []string{"allow_log"}}), &out); code != 200 || out["ok"] != true {
		t.Fatalf("disable allow_log: %d %v", code, out)
	}
	if logging("MTM-") != 0 || len(r.rt.Lists("/system/logging")) != 1 || on("allow_log") || !on("firewall_logs") {
		t.Fatalf("rules=%d logging=%d", logging("MTM-"), len(r.rt.Lists("/system/logging")))
	}
	// new-connection logging, then the firewall log off: it takes the dependent feature with it
	if code := r.call("POST", "/api/devices/gw-ro/features", req(map[string]any{"enable": []string{"log_new"}}), &out); code != 200 || out["ok"] != true || !on("log_new") {
		t.Fatalf("enable log_new: %d %v", code, out)
	}
	if code := r.call("POST", "/api/devices/gw-ro/features", req(map[string]any{"disable": []string{"firewall_logs"}}), &out); code != 200 || out["ok"] != true {
		t.Fatalf("disable firewall_logs: %d %v", code, out)
	}
	if len(r.rt.Lists("/system/logging")) != 0 || logging("MTM-") != 0 || on("log_new") {
		t.Fatal("firewall_logs off must revert log_new as well")
	}
	if rules, _ := r.st.FwRules(); len(rules) != 0 {
		t.Fatalf("firewall rule meta left: %v", rules)
	}
}

// A device added "monitor only" can get the optional router features one by one later, and lose them again.
func TestDeviceFeaturesOnOff(t *testing.T) {
	r := newAPIRig(t)
	body := r.conn()
	body["name"], body["role"] = "gw-ro", "router"
	if code := r.call("POST", "/api/devices/readonly", body, nil); code != 200 {
		t.Fatalf("readonly add: %d", code)
	}
	type feat struct {
		Key       string
		On        bool
		Available bool
	}
	getFeats := func() map[string]feat {
		var f struct{ Features []feat }
		if code := r.call("GET", "/api/devices/gw-ro/features", nil, &f); code != 200 {
			t.Fatalf("features: %d", code)
		}
		m := map[string]feat{}
		for _, x := range f.Features {
			m[x.Key] = x
		}
		return m
	}
	if f := getFeats(); len(f) != 5 || f["flow"].On || f["firewall_logs"].On {
		t.Fatalf("monitor-only device must start with everything off: %+v", f)
	}
	if len(r.rt.Lists("/ip/traffic-flow/target")) != 0 {
		t.Fatal("monitor only must not touch the router")
	}
	admin := map[string]any{"user": "admin", "pass": "adminpw"}
	req := func(extra map[string]any) map[string]any {
		m := map[string]any{}
		for k, v := range admin {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	// validation
	if code := r.call("POST", "/api/devices/gw-ro/features", map[string]any{"enable": []string{"flow"}}, nil); code != 400 {
		t.Fatalf("credentials are required: %d", code)
	}
	if code := r.call("POST", "/api/devices/gw-ro/features", req(map[string]any{"enable": []string{"rm -rf"}}), nil); code != 400 {
		t.Fatalf("unknown feature: %d", code)
	}
	// preview changes nothing
	var prev struct {
		Plan     []map[string]any
		WillUndo []string `json:"will_undo"`
	}
	if code := r.call("POST", "/api/devices/gw-ro/features", req(map[string]any{"enable": []string{"flow"}, "dry_run": true}), &prev); code != 200 || len(prev.Plan) < 2 {
		t.Fatalf("preview: %d %+v", code, prev)
	}
	for _, s := range prev.Plan {
		if s["why"] == "" || s["undo"] == "" {
			t.Fatalf("every step needs explanation and undo: %v", s)
		}
	}
	if len(r.rt.Lists("/ip/traffic-flow/target")) != 0 {
		t.Fatal("preview changed the router")
	}
	// switch on
	var on map[string]any
	if code := r.call("POST", "/api/devices/gw-ro/features", req(map[string]any{"enable": []string{"flow"}}), &on); code != 200 || on["ok"] != true {
		t.Fatalf("enable: %d %v", code, on)
	}
	if len(r.rt.Lists("/ip/traffic-flow/target")) != 1 {
		t.Fatal("traffic flow target not created")
	}
	if f := getFeats(); !f["flow"].On || f["firewall_logs"].On {
		t.Fatalf("after enabling flow: %+v", f)
	}
	var devs []map[string]any
	r.call("GET", "/api/devices", nil, &devs)
	if len(devs) != 1 || devs[0]["managed"] != true {
		t.Fatalf("a device with recorded changes must be marked managed: %v", devs)
	}
	// admin credentials never stored
	if rows, _ := r.st.DeviceRows(); len(rows) != 1 || rows[0].User != "admin" && bytes.Contains(rows[0].PassEnc, []byte("adminpw")) {
		t.Fatal("credentials stored")
	}
	// switching on again is a no-op (no duplicate target)
	r.call("POST", "/api/devices/gw-ro/features", req(map[string]any{"enable": []string{"flow"}}), &on)
	if len(r.rt.Lists("/ip/traffic-flow/target")) != 1 {
		t.Fatal("enabling an enabled feature created a duplicate")
	}
	// switch off again: the router is back to its previous state and the manifest says so
	var off map[string]any
	if code := r.call("POST", "/api/devices/gw-ro/features", req(map[string]any{"disable": []string{"flow"}}), &off); code != 200 || off["ok"] != true {
		t.Fatalf("disable: %d %v", code, off)
	}
	if len(r.rt.Lists("/ip/traffic-flow/target")) != 0 {
		t.Fatal("target not removed")
	}
	if f := getFeats(); f["flow"].On {
		t.Fatalf("flow still on: %+v", f)
	}
	r.call("GET", "/api/devices", nil, &devs)
	if devs[0]["managed"] != false {
		t.Fatalf("no recorded changes left: must be read-only again: %v", devs)
	}
	// firewall log + flow together, then only the firewall log off: flow must stay
	if code := r.call("POST", "/api/devices/gw-ro/features", req(map[string]any{"enable": []string{"flow", "firewall_logs"}, "log_new": true}), &on); code != 200 || on["ok"] != true {
		t.Fatalf("enable both: %d %v", code, on)
	}
	if len(r.rt.Lists("/system/logging")) != 1 || len(r.rt.Lists("/ip/traffic-flow/target")) != 1 {
		t.Fatalf("logging=%d targets=%d", len(r.rt.Lists("/system/logging")), len(r.rt.Lists("/ip/traffic-flow/target")))
	}
	if rules, _ := r.st.FwRules(); len(rules) == 0 {
		t.Fatal("firewall rule meta not saved")
	}
	if code := r.call("POST", "/api/devices/gw-ro/features", req(map[string]any{"disable": []string{"firewall_logs"}}), &off); code != 200 || off["ok"] != true {
		t.Fatalf("disable firewall logs: %d %v", code, off)
	}
	if len(r.rt.Lists("/system/logging")) != 0 || len(r.rt.Lists("/ip/traffic-flow/target")) != 1 {
		t.Fatal("only the firewall log feature must be reverted")
	}
	for _, f := range r.rt.Lists("/ip/firewall/filter") {
		if f["log"] == "yes" && strings.HasPrefix(f["log-prefix"], "MTM-") || strings.HasPrefix(f["comment"], "mtmon-managed") {
			t.Fatalf("filter rule left changed: %v", f)
		}
	}
	if rules, _ := r.st.FwRules(); len(rules) != 0 {
		t.Fatalf("firewall rule meta left: %v", rules)
	}
	r.call("POST", "/api/devices/gw-ro/features", req(map[string]any{"disable": []string{"flow"}}), &off)
	// wrong password changes nothing
	if code := r.call("POST", "/api/devices/gw-ro/features", map[string]any{"user": "admin", "pass": "nope", "enable": []string{"flow"}}, nil); code < 400 {
		t.Fatalf("wrong credentials accepted: %d", code)
	}
	if len(r.rt.Lists("/ip/traffic-flow/target")) != 0 {
		t.Fatal("changed with wrong credentials")
	}
}
