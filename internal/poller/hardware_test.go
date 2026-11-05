// SPDX-License-Identifier: AGPL-3.0-only

package poller

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/enrich"
	"github.com/napfkuchen1/mtmon/internal/live"
	"github.com/napfkuchen1/mtmon/internal/mockros"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// hwRig is a worker against the mock router with a real store.
type hwRig struct {
	rt *mockros.Server
	w  *worker
	st *store.Store
	ts *httptest.Server
}

func newHWRig(t *testing.T, setup func(*mockros.Server)) *hwRig {
	t.Helper()
	rt := mockros.New("gw", "router", "u", "p", nil)
	if setup != nil {
		setup(rt)
	}
	ts := httptest.NewServer(rt.Handler())
	t.Cleanup(ts.Close)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	host, port, _ := net.SplitHostPort(ts.Listener.Addr().String())
	p, _ := strconv.Atoi(port)
	d := config.Device{Name: "gw", Role: "router", Addr: host, Port: p, Scheme: "http", User: "u", Pass: "p"}
	m := NewManager(&config.Config{Devices: []config.Device{d}}, st, live.NewHub(), enrich.New("", "", "", false), nopEvents{},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &hwRig{rt: rt, st: st, ts: ts, w: &worker{m: m, d: d, c: NewClient(d), slow: 1}}
}

func TestMonitorAllowlistRefusesEverythingElse(t *testing.T) {
	var mu sync.Mutex
	var posts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		posts = append(posts, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Write([]byte(`[{"name":"x"}]`))
	}))
	defer srv.Close()
	c := NewClient(devFor(t, srv, "http"))
	for _, bad := range []string{
		"system/reboot", "/system/reboot", "ip/address/add", "interface/ethernet/set", "interface/monitor-traffic",
		"interface/ethernet/monitor/../../system/reboot", "interface/ethernet/monitor?x=1", "interface/ethernet/monitor ",
		"INTERFACE/ETHERNET/MONITOR", "interface/lte/at-chat", "export", "", "/",
	} {
		if _, err := c.Monitor(context.Background(), bad, "ether1"); err != ErrMonitorPath {
			t.Errorf("%q: err = %v, want ErrMonitorPath", bad, err)
		}
	}
	mu.Lock()
	if len(posts) != 0 {
		t.Fatalf("refused paths reached the router: %v", posts)
	}
	mu.Unlock()
	for _, ok := range []string{"interface/ethernet/poe/monitor", "/interface/ethernet/monitor", "interface/lte/monitor"} {
		if _, err := c.Monitor(context.Background(), ok, "ether1", "name"); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 3 || posts[0] != "POST /rest/interface/ethernet/poe/monitor" || posts[1] != "POST /rest/interface/ethernet/monitor" || posts[2] != "POST /rest/interface/lte/monitor" {
		t.Fatalf("posts = %v", posts)
	}
}

// The body is numbers + once + .proplist and nothing else.
func TestMonitorBody(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	_, err := NewClient(devFor(t, srv, "http")).Monitor(context.Background(), "interface/lte/monitor", "lte1", "rsrp", "sinr")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got["numbers"] != "lte1" || got["once"] != "" || got[".proplist"] != "rsrp,sinr" {
		t.Fatalf("body = %v", got)
	}
}

func TestNumParsesUnitsAndMissing(t *testing.T) {
	r := Row{"a": "-3.25dBm", "b": "41C", "c": "", "d": "n/a", "e": "0"}
	if v, ok := num(r, "a"); !ok || v != -3.25 {
		t.Errorf("a = %v %v", v, ok)
	}
	if v, ok := num(r, "b"); !ok || v != 41 {
		t.Errorf("b = %v %v", v, ok)
	}
	for _, k := range []string{"c", "d", "missing"} {
		if _, ok := num(r, k); ok {
			t.Errorf("%s must be absent, not a number", k)
		}
	}
	if v, ok := num(r, "e"); !ok || v != 0 {
		t.Errorf("a real zero stays a zero: %v %v", v, ok)
	}
}

// A 403 on the first monitor call turns the feature off for the device: later polls send nothing more, and the
// reading carries no data and no hint.
func TestHardwareProbe403TurnsFeatureOffWithoutRetry(t *testing.T) {
	rig := newHWRig(t, func(rt *mockros.Server) {
		rt.Poe = []mockros.PoePort{{Name: "ether2", Status: "powered-on"}, {Name: "ether3", Status: "powered-on"}}
		rt.MonitorDeny = true
	})
	ctx := context.Background()
	rig.w.hwPoll(ctx)
	if n := len(rig.rt.MonitorCalls()); n != 1 {
		t.Fatalf("first poll sent %d monitor calls, want 1 (the probe)", n)
	}
	if rig.w.hw.poe != store.HWOff {
		t.Fatalf("poe state = %q, want off", rig.w.hw.poe)
	}
	for i := 0; i < 3; i++ {
		rig.w.hwPoll(ctx)
	}
	if n := len(rig.rt.MonitorCalls()); n != 1 {
		t.Fatalf("later polls sent more monitor calls: %d", n)
	}
	h, ok := rig.st.HW("gw")
	if !ok || !h.Empty() || h.Caps.PoE != store.HWOff {
		t.Fatalf("hw = %+v ok=%v", h, ok)
	}
	// no SFP or LTE hardware: those were never probed with a POST
	for _, p := range rig.rt.MonitorCalls() {
		if !strings.Contains(p, "poe") {
			t.Errorf("unexpected monitor call %s", p)
		}
	}
}

// An answer turns the feature on and it keeps being read; a model without power figures yields no power value.
func TestHardwareNoPowerModelHasNoZero(t *testing.T) {
	rig := newHWRig(t, func(rt *mockros.Server) {
		rt.Poe = []mockros.PoePort{{Name: "ether2", Status: "powered-on", Pair: "b"}}
	})
	rig.w.hwPoll(context.Background())
	h, _ := rig.st.HW("gw")
	if h.Caps.PoE != store.HWOn || len(h.PoE) != 1 {
		t.Fatalf("hw = %+v", h)
	}
	p := h.PoE[0]
	if p.Status != "powered-on" || p.Pair != "b" || p.PowerW != nil || p.Fault {
		t.Fatalf("port = %+v", p)
	}
	b, _ := json.Marshal(p)
	if !strings.Contains(string(b), `"power_w":null`) {
		t.Fatalf("json = %s", b)
	}
}

func TestHardwarePoeWithWattsAndFault(t *testing.T) {
	rig := newHWRig(t, func(rt *mockros.Server) {
		rt.Poe = []mockros.PoePort{{Name: "ether2", Status: "powered-on", Watts: "4.5"}, {Name: "ether3", Status: "short-circuit"},
			{Name: "ether4", Status: "Overload"}}
	})
	rig.w.hwPoll(context.Background())
	h, _ := rig.st.HW("gw")
	if len(h.PoE) != 3 {
		t.Fatalf("poe = %+v", h.PoE)
	}
	if h.PoE[0].PowerW == nil || *h.PoE[0].PowerW != 4.5 || h.PoE[0].Fault {
		t.Errorf("ether2 = %+v", h.PoE[0])
	}
	if !h.PoE[1].Fault || !h.PoE[2].Fault || h.PoE[2].Status != "overload" {
		t.Errorf("faults = %+v %+v", h.PoE[1], h.PoE[2])
	}
}

// SFP: only cages with a module show up; a value the module does not report stays nil; the history is stored.
func TestHardwareSfpValues(t *testing.T) {
	rig := newHWRig(t, func(rt *mockros.Server) {
		rt.Sfp = []mockros.SfpPort{
			{Name: "sfp-sfpplus1", Present: true, Rx: "-7.25", Tx: "-2.1", Temp: "38"},
			{Name: "sfp-sfpplus2"},                             // empty cage
			{Name: "sfp-sfpplus3", Present: true, Rx: "-30.5"}, // no tx, no temperature
		}
	})
	rig.w.hwPoll(context.Background())
	h, _ := rig.st.HW("gw")
	if h.Caps.SFP != store.HWOn || len(h.SFP) != 2 {
		t.Fatalf("hw = %+v", h)
	}
	a, b := h.SFP[0], h.SFP[1]
	if a.RxDBm == nil || *a.RxDBm != -7.25 || a.TxDBm == nil || *a.TxDBm != -2.1 || a.TempC == nil || *a.TempC != 38 {
		t.Errorf("sfp1 = %+v", a)
	}
	if b.RxDBm == nil || *b.RxDBm != -30.5 || b.TxDBm != nil || b.TempC != nil {
		t.Errorf("sfp3 = %+v", b)
	}
	hist := rig.st.SfpHistory("gw", 0)
	if len(hist) != 2 || hist[0].Iface != "sfp-sfpplus1" || hist[1].TxDBm != nil {
		t.Fatalf("history = %+v", hist)
	}
}

// LTE signal is read only when an LTE interface exists; without one nothing is posted for it.
func TestHardwareLteOnlyWhenPresent(t *testing.T) {
	none := newHWRig(t, nil)
	none.w.hwPoll(context.Background())
	if calls := none.rt.MonitorCalls(); len(calls) != 0 {
		t.Fatalf("no hardware, yet monitor calls: %v", calls)
	}
	if h, ok := none.st.HW("gw"); ok && !h.Empty() {
		t.Fatalf("hw = %+v", h)
	}
	if none.w.hw.lte != store.HWNone || none.w.hw.poe != store.HWNone || none.w.hw.sfp != store.HWNone {
		t.Fatalf("state = %+v", none.w.hw)
	}

	with := newHWRig(t, func(rt *mockros.Server) {
		rt.Lte = []mockros.LteIface{{Name: "lte1", RSRP: "-97", RSRQ: "-11", SINR: "8", Tech: "lte", Status: "connected"}}
	})
	with.w.hwPoll(context.Background())
	h, _ := with.st.HW("gw")
	if h.Caps.LTE != store.HWOn || len(h.LTE) != 1 {
		t.Fatalf("hw = %+v", h)
	}
	l := h.LTE[0]
	if l.RSRP == nil || *l.RSRP != -97 || l.SINR == nil || *l.SINR != 8 || l.RSSI != nil || l.Tech != "lte" {
		t.Fatalf("lte = %+v", l)
	}
	for _, p := range with.rt.MonitorCalls() {
		if !strings.Contains(p, "lte") {
			t.Errorf("unexpected monitor call %s", p)
		}
	}
}

func TestHwDueInterval(t *testing.T) {
	if !hwDue(1, 1) {
		t.Error("the first tick must poll")
	}
	for _, tick := range []int64{2, 10, 299, 301, 599} {
		if hwDue(tick, 1) {
			t.Errorf("tick %d must not poll", tick)
		}
	}
	if !hwDue(300, 1) || !hwDue(600, 1) {
		t.Error("every 300 ticks must poll")
	}
	if hwDue(300, 2) || !hwDue(600, 2) { // a busy router (slow=2) is asked half as often
		t.Error("slow factor not respected")
	}
	if hwEvery < 300 {
		t.Errorf("hwEvery = %d, the issue asks for at least 5 minutes", hwEvery)
	}
}

// Over a real run loop the hardware poll happens once at start and not again within the interval.
func TestHardwarePollRunsOncePerInterval(t *testing.T) {
	rig := newHWRig(t, func(rt *mockros.Server) {
		rt.Poe = []mockros.PoePort{{Name: "ether2", Status: "powered-on"}}
	})
	rig.w.m.Scale = 0.01 // 10 ms per tick
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rig.w.m.runDevice(ctx, rig.w.d); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(rig.rt.MonitorCalls()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(400 * time.Millisecond) // ~40 ticks, far below the 300 of one interval
	cancel()
	<-done
	if n := len(rig.rt.MonitorCalls()); n != 1 {
		t.Fatalf("%d monitor calls in the first minutes, want exactly 1 (one port, one round)", n)
	}
}
