package poller

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/enrich"
	"github.com/napfkuchen1/mtmon/internal/live"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// Events lets the alert engine observe poll results without import cycles.
type Events interface {
	DeviceState(name string, up bool, err string, cpu, temp float64)
	InterfaceState(device, iface string, running bool)
	NewClient(mac, name, ip string)
}

type Manager struct {
	rekeyMu sync.Mutex
	rekeyed map[string]bool // "key|addr" already merged into the VPN client
	Cfg     *config.Config
	St      *store.Store
	Hub     *live.Hub
	En      *enrich.Enricher
	Ev      Events
	Log     *slog.Logger
	Scale   float64 // interval multiplier (tests use <1)
	mu      sync.Mutex
	reload  chan struct{}
	snaps   map[string]*devSnap
	known   map[string]bool
	primed  bool
	self    map[string]string // MAC of a managed device's own interface -> device name (never a client)
	addrMu  sync.Mutex
	addrAt  time.Time
	addrMAC map[string]string // address configured on a managed device -> one of its own MACs
}

type devSnap struct {
	role   string
	leases []Row
	arp    []Row
	nd6    []Row // /ipv6/neighbor: IPv6 address -> MAC (merges IPv6 traffic into the client)
	wifi   []Row
	bridge []Row
	peers  []Row           // /interface/wireguard/peers (absent without the wireguard package)
	wan    map[string]bool // interfaces that face the upstream: members of the WAN interface list, default-route interface
	addrs  []Row           // /ip/address (transfer net towards a modem)
	addrs6 []Row           // /ipv6/address (dynamic PPPoE / DHCPv6 addresses change between capability probes)
	pppoe  []Row           // /interface/pppoe-client
	ifs    []Row           // /interface (name, type) to tell ports, PPPoE and tunnels apart
	routes []Row           // /ip/route (default route: double NAT)
	at     time.Time
}

func NewManager(c *config.Config, s *store.Store, h *live.Hub, e *enrich.Enricher, ev Events, log *slog.Logger) *Manager {
	return &Manager{Cfg: c, St: s, Hub: h, En: e, Ev: ev, Log: log, Scale: 1,
		snaps: map[string]*devSnap{}, known: map[string]bool{}, self: map[string]string{}, reload: make(chan struct{}, 1)}
}

// Reload asks Run to reconcile workers with the current device list (add / remove / change).
func (m *Manager) Reload() {
	select {
	case m.reload <- struct{}{}:
	default:
	}
}

type runner struct {
	d      config.Device
	cancel context.CancelFunc
}

// Run keeps one worker per device (file + UI managed) and the merge loop. Devices can be added,
// changed and removed at runtime via Reload().
func (m *Manager) Run(ctx context.Context) {
	var wg sync.WaitGroup
	running := map[string]*runner{}
	sync1 := func() {
		want := map[string]config.Device{}
		for _, d := range m.Cfg.AllDevices() {
			want[d.Name] = d
		}
		for n, r := range running {
			if d, ok := want[n]; !ok || !reflect.DeepEqual(d, r.d) {
				r.cancel()
				delete(running, n)
				if !ok {
					m.mu.Lock()
					delete(m.snaps, n)
					m.mu.Unlock()
					m.Hub.RemoveDevice(n)
				}
			}
		}
		for n, d := range want {
			if _, ok := running[n]; ok {
				continue
			}
			dctx, cancel := context.WithCancel(ctx)
			running[n] = &runner{d: d, cancel: cancel}
			wg.Add(1)
			go func(d config.Device) { defer wg.Done(); m.runDevice(dctx, d) }(d)
		}
	}
	sync1()
	t := time.NewTicker(m.dur(5 * time.Second))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-m.reload:
			sync1()
		case <-t.C:
			m.merge()
		}
	}
}

func (m *Manager) dur(d time.Duration) time.Duration {
	if m.Scale > 0 && m.Scale != 1 {
		d = time.Duration(float64(d) * m.Scale)
		if d < 10*time.Millisecond {
			d = 10 * time.Millisecond
		}
	}
	return d
}

type ifCounter struct {
	rx, tx int64
	at     time.Time
}

type ifAgg struct{ rx, tx float64 }

type worker struct {
	m         *Manager
	d         config.Device
	c         *Client
	fails     int
	slow      int // 1 normally, 2 when router CPU is high
	counters  map[string]ifCounter
	running   map[string]bool
	agg       map[string]*ifAgg
	aggAt     time.Time
	noWifi    time.Time
	wifiCount int
	cpu, temp float64
	retryAt   time.Time
}

func (m *Manager) runDevice(ctx context.Context, d config.Device) {
	w := &worker{m: m, d: d, c: NewClient(d), slow: 1, counters: map[string]ifCounter{}, running: map[string]bool{},
		agg: map[string]*ifAgg{}, aggAt: time.Now()}
	m.mu.Lock()
	m.snaps[d.Name] = &devSnap{role: d.Role}
	m.mu.Unlock()
	base := m.dur(time.Second)
	t := time.NewTicker(base)
	defer t.Stop()
	var tick int64
	w.identity(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		tick++
		// Exponential backoff while failing: 2,4,8,16,30 base intervals between attempts.
		if w.fails > 0 && time.Now().Before(w.retryAt) {
			continue
		}
		sl := int64(w.slow)
		run := func(every int64) bool { return tick%(every*sl) == 0 }
		var err error
		ran := false
		if run(10) || tick == 1 || w.fails > 0 {
			err = w.system(ctx)
			ran = true
		}
		if err == nil && (run(2) || tick == 1) {
			err = w.interfaces(ctx)
			ran = true
		}
		if err == nil && (run(5) || tick == 1) {
			w.wifiPoll(ctx)
		}
		if err == nil && (run(15) || tick == 1) {
			w.l3Poll(ctx)
		}
		if err == nil && (run(30) || tick == 1) {
			w.dnsCache(ctx)
		}
		if err == nil && (run(60) || tick == 1) {
			w.neighbors(ctx)
		}
		if run(3600) && tick > 1 {
			w.identity(ctx)
		}
		if ran {
			w.report(err)
			if err != nil {
				n := 1 << uint(min(w.fails, 5))
				if n > 30 {
					n = 30
				}
				w.retryAt = time.Now().Add(time.Duration(n) * base)
			}
		}
	}
}

func (w *worker) report(err error) {
	name := w.d.Name
	if err == nil {
		if w.fails > 0 {
			w.m.Log.Info("device recovered", "device", name)
		}
		w.fails = 0
		return
	}
	w.fails++
	msg := err.Error()
	if errors.Is(err, ErrAuth) {
		msg = "authentication failed (check read-only user/password)"
	}
	w.m.Log.Warn("poll failed", "device", name, "err", msg, "fails", w.fails)
	w.m.St.SaveDeviceState(store.DeviceState{Name: name, LastErr: msg})
	w.m.Hub.SetDevice(live.DevLive{Name: name, Up: false, Err: msg})
	if w.fails >= 3 {
		w.m.Ev.DeviceState(name, false, msg, 0, 0)
	}
}

func (w *worker) identity(ctx context.Context) {
	cc, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ds := store.DeviceState{Name: w.d.Name, LastOK: 0}
	if rows, err := w.c.Get(cc, "/system/identity"); err == nil && len(rows) > 0 {
		ds.Ident = rows[0]["name"]
	}
	if rows, err := w.c.Get(cc, "/system/routerboard"); err == nil && len(rows) > 0 {
		ds.Model = rows[0]["model"]
		ds.Board = rows[0]["serial-number"]
		info, _ := json.Marshal(map[string]string{"firmware": rows[0]["current-firmware"], "upgrade": rows[0]["upgrade-firmware"]})
		ds.Info = info
	}
	if rows, err := w.c.Get(cc, "/system/resource"); err == nil && len(rows) > 0 {
		ds.Version = rows[0]["version"]
		if ds.Model == "" {
			ds.Model = rows[0]["board-name"]
		}
	}
	if ds.Version != "" || ds.Ident != "" {
		w.m.St.SaveDeviceState(ds)
	}
}

func (w *worker) system(ctx context.Context) error {
	rows, err := w.c.Get(ctx, "/system/resource")
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return errors.New("empty /system/resource")
	}
	r := rows[0]
	total, free := r.Int("total-memory"), r.Int("free-memory")
	cpu := r.Float("cpu-load")
	temp := 0.0
	if hr, err := w.c.Get(ctx, "/system/health"); err == nil {
		temp = parseTemp(hr)
	}
	up := int64(ParseDuration(r["uptime"]).Seconds())
	memPct := 0.0
	if total > 0 {
		memPct = float64(total-free) * 100 / float64(total)
	}
	w.cpu, w.temp = cpu, temp
	if cpu > 70 {
		w.slow = 2
	} else {
		w.slow = 1
	}
	now := time.Now()
	w.m.Hub.SetDevice(live.DevLive{Name: w.d.Name, Up: true, CPU: cpu, MemPct: memPct, Temp: temp, Uptime: up,
		Clients: w.wifiCount, LastOK: now.Unix()})
	w.m.St.SaveDeviceState(store.DeviceState{Name: w.d.Name, Version: r["version"], Model: r["board-name"], LastOK: now.Unix()})
	w.m.Ev.DeviceState(w.d.Name, true, "", cpu, temp)
	if b, ok := w.m.Ev.(interface {
		DeviceBoot(name, ver string, uptime int64)
	}); ok {
		b.DeviceBoot(w.d.Name, r["version"], up)
	}
	if now.Sub(w.aggAt) >= 60*time.Second {
		w.aggAt = now
		w.m.St.SaveDevMetric(store.DevMetric{TS: now.Unix(), Device: w.d.Name, CPU: cpu, MemUsed: total - free, MemTotal: total,
			Temp: temp, Uptime: up, Clients: w.wifiCount, Up: true})
		var out []store.IfMetric
		for n, a := range w.agg {
			out = append(out, store.IfMetric{TS: now.Unix(), Device: w.d.Name, Iface: n, RxBps: a.rx, TxBps: a.tx})
		}
		w.m.St.SaveIfMetrics(out)
		w.agg = map[string]*ifAgg{}
	}
	return nil
}

// parseTemp handles both RouterOS health formats (object and name/value list).
func parseTemp(rows []Row) float64 {
	best := 0.0
	for _, r := range rows {
		if n, ok := r["name"]; ok {
			if strings.Contains(n, "temperature") {
				if v := r.Float("value"); v > best {
					best = v
				}
			}
			continue
		}
		for k, v := range r {
			if strings.Contains(k, "temperature") {
				if f := (Row{"x": v}).Float("x"); f > best {
					best = f
				}
			}
		}
	}
	return best
}

func (w *worker) interfaces(ctx context.Context) error {
	rows, err := w.c.Get(ctx, "/interface")
	if err != nil {
		return err
	}
	now := time.Now()
	var rates []live.IfRate
	w.m.noteSelfMACs(w.d.Name, rows)
	for _, r := range rows {
		name := r["name"]
		if name == "" || r.Bool("disabled") {
			continue
		}
		rx, tx := r.Int("rx-byte"), r.Int("tx-byte")
		running := r.Bool("running")
		if prev, ok := w.running[name]; ok && prev != running {
			w.m.Ev.InterfaceState(w.d.Name, name, running)
		}
		w.running[name] = running
		var rxb, txb float64
		if p, ok := w.counters[name]; ok {
			dt := now.Sub(p.at).Seconds()
			if dt > 0 && rx >= p.rx && tx >= p.tx { // counter reset → skip
				rxb, txb = float64(rx-p.rx)*8/dt, float64(tx-p.tx)*8/dt
			}
		}
		w.counters[name] = ifCounter{rx, tx, now}
		rates = append(rates, live.IfRate{Device: w.d.Name, Iface: name, RxBps: rxb, TxBps: txb, Running: running, Type: r["type"]})
		a := w.agg[name]
		if a == nil {
			a = &ifAgg{}
			w.agg[name] = a
		}
		if rxb > a.rx {
			a.rx = rxb
		}
		if txb > a.tx {
			a.tx = txb
		}
	}
	w.m.Hub.SetIfaces(w.d.Name, rates)
	return nil
}

func (w *worker) wifiPoll(ctx context.Context) {
	if !w.noWifi.IsZero() && time.Since(w.noWifi) < 10*time.Minute {
		return
	}
	rows, err := w.c.Get(ctx, "/interface/wifi/registration-table")
	if err != nil {
		// Device without the wifi package (plain router/switch): retry rarely.
		w.noWifi = time.Now()
		return
	}
	w.noWifi = time.Time{}
	w.wifiCount = len(rows)
	w.m.setSnap(w.d.Name, func(s *devSnap) { s.wifi = rows })
}

func (w *worker) l3Poll(ctx context.Context) {
	if rows, err := w.c.Get(ctx, "/ip/dhcp-server/lease"); err == nil {
		w.m.setSnap(w.d.Name, func(s *devSnap) { s.leases = rows })
	}
	if rows, err := w.c.Get(ctx, "/ip/arp"); err == nil {
		w.m.setSnap(w.d.Name, func(s *devSnap) { s.arp = rows })
	}
	if rows, err := w.c.Get(ctx, "/ipv6/neighbor"); err == nil { // absent without the ipv6 package
		w.m.setSnap(w.d.Name, func(s *devSnap) { s.nd6 = rows })
	}
	if rows, err := w.c.Get(ctx, "/interface/bridge/host"); err == nil {
		w.m.setSnap(w.d.Name, func(s *devSnap) { s.bridge = rows })
	}
	if rows, err := w.c.Get(ctx, "/interface/wireguard/peers"); err == nil {
		w.m.setSnap(w.d.Name, func(s *devSnap) { s.peers = rows })
	}
	members, merr := w.c.Get(ctx, "/interface/list/member")
	routes, rerr := w.c.Get(ctx, "/ip/route")
	addrs, _ := w.c.Get(ctx, "/ip/address")
	pppoe, _ := w.c.Get(ctx, "/interface/pppoe-client") // absent without the ppp package: nothing to show then
	ifs, _ := w.c.Get(ctx, "/interface")
	// the router's own delegated IPv6 prefixes are internal, too (absent without the ipv6 package: nothing to learn)
	a6, _ := w.c.Get(ctx, "/ipv6/address")
	d6, _ := w.c.Get(ctx, "/ipv6/dhcp-client")
	p6, _ := w.c.Get(ctx, "/ipv6/pool")
	if ps := ownV6Prefixes(a6, d6, p6); len(ps) > 0 {
		w.m.Cfg.LearnNets(ps, time.Now())
	}
	w.m.setSnap(w.d.Name, func(s *devSnap) { s.addrs, s.addrs6, s.pppoe, s.ifs, s.routes = addrs, a6, pppoe, ifs, routes })
	if merr == nil || rerr == nil {
		wan := wanInterfaces(members, routes)
		if w.d.Role == "ap" { // an access point has no upstream side; its only bridge is its LAN
			wan = map[string]bool{}
		}
		w.m.setSnap(w.d.Name, func(s *devSnap) { s.wan = wan })
		if n := w.m.St.RemoveWANClients(w.d.Name, wanList(wan)); n > 0 { // leftovers from before the WAN check existed
			w.m.Log.Info("removed offline records learned on a WAN interface", "device", w.d.Name, "count", n)
		}
	}
}

// wanInterfaces names the interfaces that lead to the upstream (modem, ISP). Members of the interface list "WAN"
// are authoritative; only without such a list the interface of the active default route (the part after "%" in the
// gateway) is used, because on a downstream router that route runs over the LAN bridge. Hosts learned on a WAN
// interface are not LAN clients. Field names are matched defensively; unknown shapes just yield fewer interfaces.
func wanInterfaces(members, routes []Row) map[string]bool {
	out := map[string]bool{}
	for _, r := range members {
		if strings.EqualFold(strings.TrimSpace(r["list"]), "WAN") && r["interface"] != "" && !r.Bool("disabled") {
			out[r["interface"]] = true
		}
	}
	if len(out) > 0 {
		return out
	}
	for _, r := range routes {
		if r["dst-address"] != "0.0.0.0/0" || r.Bool("disabled") || (r["active"] != "" && !r.Bool("active")) {
			continue
		}
		for _, k := range []string{"immediate-gw", "gateway"} {
			if _, ifc, ok := strings.Cut(r[k], "%"); ok && ifc != "" {
				out[ifc] = true
			}
		}
	}
	return out
}

func (w *worker) dnsCache(ctx context.Context) {
	// static entries name local devices (A records); useful as reverse names for clients without a DHCP host-name
	if st, err := w.c.Get(ctx, "/ip/dns/static"); err == nil {
		for _, r := range st {
			if r.Bool("disabled") || (r["type"] != "" && r["type"] != "A" && r["type"] != "AAAA") {
				continue
			}
			if ip, err := netip.ParseAddr(r["address"]); err == nil && r["name"] != "" {
				w.m.En.SetName(ip, r["name"], time.Hour)
			}
		}
	}
	rows, err := w.c.Get(ctx, "/ip/dns/cache")
	if err != nil {
		return
	}
	for _, r := range rows {
		t := r["type"]
		if t != "A" && t != "AAAA" && t != "" {
			continue
		}
		ip, err := netip.ParseAddr(r["data"])
		if err != nil || r["name"] == "" {
			continue
		}
		ttl := ParseDuration(r["ttl"])
		if ttl < 10*time.Minute {
			ttl = 10 * time.Minute
		}
		w.m.En.SetName(ip, r["name"], ttl)
	}
}

func (w *worker) neighbors(ctx context.Context) {
	rows, err := w.c.Get(ctx, "/ip/neighbor")
	if err != nil {
		return
	}
	var ns []store.Neighbor
	for _, r := range rows {
		ns = append(ns, store.Neighbor{Device: w.d.Name, Iface: r["interface"], MAC: r["mac-address"], Ident: r["identity"],
			Addr: r["address"], Platform: r["platform"]})
	}
	w.m.St.SaveNeighbors(w.d.Name, ns)
}

func (m *Manager) setSnap(dev string, f func(*devSnap)) {
	m.mu.Lock()
	s := m.snaps[dev]
	if s != nil {
		f(s)
		s.at = time.Now()
	}
	m.mu.Unlock()
}

func parseSignal(s string) int {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "dBm"))
	if i := strings.IndexAny(s, "@/ "); i > 0 {
		s = s[:i]
	}
	n := (Row{"x": s}).Int("x")
	if n < math.MinInt16 || n > math.MaxInt16 { // a dBm value is tiny; anything else is garbage
		return 0
	}
	return int(n)
}
