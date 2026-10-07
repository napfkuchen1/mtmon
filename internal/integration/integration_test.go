package integration

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/alert"
	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/enrich"
	"github.com/napfkuchen1/mtmon/internal/flow"
	"github.com/napfkuchen1/mtmon/internal/live"
	"github.com/napfkuchen1/mtmon/internal/mockros"
	"github.com/napfkuchen1/mtmon/internal/poller"
	"github.com/napfkuchen1/mtmon/internal/store"
)

var clients = []mockros.Client{
	{MAC: "3C:22:FB:10:00:01", IP: "192.168.88.23", Host: "daniel-iphone", WiFi: true, Signal: -52, SSID: "Home"},
	{MAC: "DC:A6:32:10:00:02", IP: "192.168.88.30", Host: "homeassistant"},
}

func dev(name, role string, ts *httptest.Server) config.Device {
	h, p, _ := net.SplitHostPort(ts.Listener.Addr().String())
	port, _ := strconv.Atoi(p)
	return config.Device{Name: name, Addr: h, Port: port, Role: role, User: "mtmon", Pass: "pw", Scheme: "http"}
}

type rig struct {
	cfg    *config.Config
	st     *store.Store
	hub    *live.Hub
	en     *enrich.Enricher
	pipe   *live.Pipeline
	al     *alert.Engine
	rt     *mockros.Server
	alerts []store.Alert
	mu     sync.Mutex
}

func newRig(t *testing.T) *rig {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rt := mockros.New("gw-main", "router", "mtmon", "pw", clients)
	ap := mockros.New("ap-living", "ap", "mtmon", "pw", clients)
	rts, aps := httptest.NewServer(rt.Handler()), httptest.NewServer(ap.Handler())
	t.Cleanup(rts.Close)
	t.Cleanup(aps.Close)
	cfg := &config.Config{DataDir: t.TempDir(), Devices: []config.Device{dev("gw-main", "router", rts), dev("ap-living", "ap", aps)}}
	cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := live.NewHub()
	en := enrich.New("", "", "", false)
	r := &rig{cfg: cfg, st: st, hub: hub, en: en, rt: rt}
	r.al = alert.New(cfg, st, hub, log)
	r.al.Notify = func(a store.Alert) { r.mu.Lock(); r.alerts = append(r.alerts, a); r.mu.Unlock() }
	r.pipe = live.NewPipeline(cfg, st, en, hub)
	return r
}

func (r *rig) kinds() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := map[string]int{}
	for _, a := range r.alerts {
		m[a.Kind]++
	}
	return m
}

func waitFor(t *testing.T, what string, d time.Duration, f func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if f() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestPollerBuildsInventory(t *testing.T) {
	r := newRig(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := poller.NewManager(r.cfg, r.st, r.hub, r.en, r.al, log)
	m.Scale = 0.02
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)

	waitFor(t, "2 clients", 5*time.Second, func() bool { c, _ := r.st.Clients(); return len(c) == 2 })
	cs, _ := r.st.Clients()
	byMAC := map[string]store.Client{}
	for _, c := range cs {
		byMAC[c.MAC] = c
	}
	ip := byMAC["3C:22:FB:10:00:01"]
	if !ip.WiFi || ip.Device != "ap-living" || ip.SSID != "Home" || ip.Signal != -52 || ip.Hostname != "daniel-iphone" || ip.IP != "192.168.88.23" {
		t.Fatalf("wifi client wrong: %+v", ip)
	}
	ha := byMAC["DC:A6:32:10:00:02"]
	if ha.WiFi || ha.Device == "" || ha.Hostname != "homeassistant" {
		t.Fatalf("wired client wrong: %+v", ha)
	}
	if r.hub.MACFor("192.168.88.23") != "3C:22:FB:10:00:01" {
		t.Fatal("ip map missing")
	}
	waitFor(t, "device live", 5*time.Second, func() bool {
		d := r.hub.Devices()
		return len(d) == 2 && d[0].Up && d[1].Up && d[0].Temp == 41
	})
	waitFor(t, "iface rates", 5*time.Second, func() bool {
		for _, i := range r.hub.Ifaces("gw-main") {
			if i.Iface == "ether1" && i.RxBps > 0 {
				return true
			}
		}
		return false
	})
	waitFor(t, "dns cache → enrich", 5*time.Second, func() bool { return r.en.Name(netip.MustParseAddr("93.184.216.34")) == "example.com" })
	waitFor(t, "neighbors", 5*time.Second, func() bool { n, _ := r.st.Neighbors(); return len(n) > 0 })
}

func TestOutageAlertAndRecovery(t *testing.T) {
	r := newRig(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := poller.NewManager(r.cfg, r.st, r.hub, r.en, r.al, log)
	m.Scale = 0.02
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	waitFor(t, "up", 5*time.Second, func() bool { d := r.hub.Devices(); return len(d) == 2 && d[0].Up })
	r.rt.SetFail(true)
	waitFor(t, "offline alert", 10*time.Second, func() bool { return r.kinds()["device_offline"] == 1 })
	r.rt.SetFail(false)
	waitFor(t, "recovery", 15*time.Second, func() bool { return r.kinds()["device_online"] >= 1 })
	if r.kinds()["device_offline"] != 1 {
		t.Fatalf("offline alert must fire exactly once (dedup): %v", r.kinds())
	}
}

func TestPipelineEndToEnd(t *testing.T) {
	r := newRig(t)
	r.hub.SetIPMap(map[string]string{"192.168.88.23": "3C:22:FB:10:00:01"}, map[string]string{"3C:22:FB:10:00:01": "daniel-iphone"})
	r.en.SetName(netip.MustParseAddr("1.1.1.1"), "one.one.one.one", time.Hour)
	col := &flow.Collector{Addr: "127.0.0.1:0", Allow: r.cfg.ExporterAllowed, Handler: r.pipe.Handle}
	r.cfg.Devices = append(r.cfg.Devices, config.Device{Name: "lo", Addr: "127.0.0.1"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go col.Run(ctx)
	waitFor(t, "listen", time.Second, func() bool { return col.LocalAddr() != nil })
	conn, err := net.Dial("udp", col.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	e := flow.NewEncoder(true)
	now := time.Now()
	conn.Write(e.Template(now))
	a := netip.MustParseAddr
	conn.Write(e.Data(now, []flow.Record{
		{Src: a("192.168.88.23"), Dst: a("1.1.1.1"), SrcPort: 50000, DstPort: 443, Proto: 6, Bytes: 1000, Packets: 5},
		{Src: a("1.1.1.1"), Dst: a("192.168.88.23"), SrcPort: 443, DstPort: 50000, Proto: 6, Bytes: 9000, Packets: 9},
		{Src: a("203.0.113.9"), Dst: a("1.1.1.1"), SrcPort: 50000, DstPort: 443, Proto: 6, Bytes: 7777, Packets: 1}, // WAN-side NAT copy → ignore
		{Src: a("192.168.88.23"), Dst: a("192.168.88.30"), SrcPort: 4000, DstPort: 8123, Proto: 6, Bytes: 500, Packets: 2},
		{Src: a("192.168.88.23"), Dst: a("192.168.88.30"), SrcPort: 4000, DstPort: 8123, Proto: 6, Bytes: 500, Packets: 2}, // duplicate (2nd interface)
	}))
	waitFor(t, "queue", 2*time.Second, func() bool { return r.st.QueueLen() == 3 })
	if err := r.st.Flush(); err != nil {
		t.Fatal(err)
	}
	conns, err := r.st.Connections("3C:22:FB:10:00:01", 0, 50, 0)
	if err != nil || len(conns) != 3 {
		t.Fatalf("conns=%d err=%v", len(conns), err)
	}
	up, down, flows := r.st.Totals(now.Unix() - 120)
	if up != 1000 || down != 9000 || flows != 3 {
		t.Fatalf("totals up=%d down=%d flows=%d", up, down, flows)
	}
	hosts, _ := r.st.TopBy("hosts", now.Unix()-120, 5, "")
	if len(hosts) == 0 || hosts[0].Label != "one.one.one.one" || hosts[0].Key != "1.1.1.1" {
		t.Fatalf("hosts %+v", hosts)
	}
	ports, _ := r.st.TopBy("services", now.Unix()-120, 5, "")
	if len(ports) == 0 || ports[0].Key != "HTTPS" {
		t.Fatalf("services %+v", ports)
	}
	cl, _ := r.st.TopBy("clients", now.Unix()-120, 5, "")
	if len(cl) == 0 || cl[0].Key != "3C:22:FB:10:00:01" {
		t.Fatalf("clients %+v", cl)
	}
	if r.pipe.Ignored != 1 {
		t.Fatalf("ignored=%d want 1 (WAN copy)", r.pipe.Ignored)
	}
	snap := r.hub.Snapshot(0, 10)
	if len(snap.Clients) == 0 || snap.Clients[0].Name != "daniel-iphone" || len(snap.Conns) != 3 {
		t.Fatalf("snapshot %+v", snap)
	}
	// unknown exporter is dropped
	if col.Dropped.Load() != 0 {
		t.Fatal("known exporter dropped")
	}
}

func TestExporterAllowlist(t *testing.T) {
	cfg := &config.Config{Devices: []config.Device{{Name: "r", Addr: "10.0.0.1", FlowSources: []string{"10.9.9.9"}}}}
	cfg.Defaults()
	if !cfg.ExporterAllowed(netip.MustParseAddr("10.0.0.1")) || !cfg.ExporterAllowed(netip.MustParseAddr("10.9.9.9")) || cfg.ExporterAllowed(netip.MustParseAddr("10.0.0.2")) {
		t.Fatal("allowlist")
	}
}

func TestRetentionAndBackup(t *testing.T) {
	r := newRig(t)
	old := time.Now().AddDate(0, 0, -40).Unix()
	r.st.Enqueue(store.FlowRow{TS: old, MAC: "AA", CIP: "192.168.1.2", RIP: "1.1.1.1", RPort: 443, Proto: 6, Dir: "u", Bytes: 10})
	r.st.Enqueue(store.FlowRow{TS: time.Now().Unix(), MAC: "AA", CIP: "192.168.1.2", RIP: "1.1.1.1", RPort: 443, Proto: 6, Dir: "u", Bytes: 10})
	r.st.Flush()
	r.st.Cleanup(7, 30)
	var n int
	r.st.DB.QueryRow(`SELECT count(*) FROM flows`).Scan(&n)
	if n != 1 {
		t.Fatalf("raw retention: %d", n)
	}
	r.st.DB.QueryRow(`SELECT count(*) FROM rollup_1m`).Scan(&n)
	if n != 1 {
		t.Fatalf("rollup retention: %d", n)
	}
	dir := t.TempDir()
	for i := 0; i < 9; i++ {
		if err := r.st.Backup(dir, 7); err != nil {
			t.Fatal(err)
		}
		time.Sleep(1100 * time.Millisecond)
		if i == 2 {
			break
		}
	}
}
