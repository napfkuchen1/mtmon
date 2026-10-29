package poller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/netip"
	"reflect"
	"sort"
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

// noteSelfMACs remembers the MAC addresses of a managed device's own interfaces, so the router/AP itself
// (seen by other devices through ARP or the bridge host table) is not listed as a client.
func (m *Manager) noteSelfMACs(device string, rows []Row) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range rows {
		if mac := store.NormMAC(r["mac-address"]); mac != "" && !zeroMAC(mac) {
			m.self[mac] = device
		}
	}
}

// zeroMAC: tunnel and PPPoE interfaces report 00:00:00:00:00:00 – no real hardware address, so never a key.
func zeroMAC(mac string) bool {
	return strings.Trim(mac, "0:-") == ""
}

// liveAddrs returns the addresses the managed devices have right now: the ones read from the router on each poll
// (/ip/address, /ipv6/address, so a PPPoE address that changed after the nightly reconnect is known at once), plus
// the stored capability list as a fallback. device -> addresses.
func (m *Manager) liveAddrs() map[string][]netip.Addr {
	out := map[string][]netip.Addr{}
	add := func(dev, s string) {
		if ad, ok := addrOf(s); ok {
			out[dev] = append(out[dev], ad)
		}
	}
	m.mu.Lock()
	for dev, s := range m.snaps {
		for _, r := range s.addrs {
			if r["disabled"] != "true" {
				add(dev, r["address"])
			}
		}
		for _, r := range s.addrs6 {
			if r["disabled"] != "true" {
				add(dev, r["address"])
			}
		}
	}
	m.mu.Unlock()
	if rows, err := m.St.DeviceRows(); err == nil {
		for _, r := range rows {
			var c struct {
				Addresses []string `json:"addresses"`
			}
			if json.Unmarshal(r.Caps, &c) != nil {
				continue
			}
			for _, x := range c.Addresses {
				add(r.Name, x)
			}
		}
	}
	return out
}

// SelfDevice returns the managed device that owns this MAC address on one of its own interfaces.
func (m *Manager) SelfDevice(mac string) (string, bool) {
	mac = store.NormMAC(mac)
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.self[mac]; ok {
		return d, true
	}
	if mac != "" && zeroMAC(mac) { // older data booked a router's PPPoE/tunnel traffic on the zero MAC: that is a router, too
		if ds := m.Cfg.AllDevices(); len(ds) > 0 {
			return ds[0].Name, true
		}
	}
	return "", false
}

// SelfByAddr returns the managed device that owns this IP address: its management address, an address configured on
// it (from the stored capabilities), or a link-local address built from one of its interface MACs.
func (m *Manager) SelfByAddr(ip string) (string, bool) {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return "", false
	}
	for _, d := range m.Cfg.AllDevices() {
		if d.Addr == ip {
			return d.Name, true
		}
	}
	for dev, list := range m.liveAddrs() {
		for _, ad := range list {
			if ad == a {
				return dev, true
			}
		}
	}
	if a.Is6() { // EUI-64 interface id: link-local, SLAAC or an old PPPoE address (zero MAC = 0200:00ff:fe00:0000) of a router
		b := a.As16()
		if b[11] == 0xff && b[12] == 0xfe {
			mac := fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", b[8]^0x02, b[9], b[10], b[13], b[14], b[15])
			return m.SelfDevice(mac)
		}
	}
	return "", false
}

// addrOf parses an entry of the stored capabilities ("192.168.1.1/24 on bridge", "2001:db8::1/64 on pppoe-out1").
func addrOf(s string) (netip.Addr, bool) {
	if i := strings.IndexByte(s, ' '); i > 0 {
		s = s[:i]
	}
	if pf, err := netip.ParsePrefix(s); err == nil {
		return pf.Addr(), true
	}
	ad, err := netip.ParseAddr(s)
	return ad, err == nil
}

// SelfMACByAddr returns one of the own interface MACs of the managed device that has this IP address configured
// (WAN / PPPoE addresses included, dynamic ones too), or "". The flow pipeline books the device's own traffic on
// that MAC, so it never turns into a pseudo-client. The address list is rebuilt at most every 30 s.
func (m *Manager) SelfMACByAddr(ip string) string {
	m.addrMu.Lock()
	defer m.addrMu.Unlock()
	if m.addrMAC == nil || time.Since(m.addrAt) > 10*time.Second {
		macs := map[string]string{} // device -> smallest own MAC
		m.mu.Lock()
		for mac, dev := range m.self {
			if old, ok := macs[dev]; !ok || mac < old {
				macs[dev] = mac
			}
		}
		m.mu.Unlock()
		out := map[string]string{}
		for dev, list := range m.liveAddrs() {
			mac := macs[dev]
			if mac == "" {
				continue
			}
			for _, ad := range list {
				if !ad.IsLinkLocalUnicast() {
					out[ad.String()] = mac
				}
			}
		}
		m.addrMAC, m.addrAt = out, time.Now()
	}
	if a, err := netip.ParseAddr(ip); err == nil {
		return m.addrMAC[a.String()]
	}
	return ""
}

// isSelf reports whether the observation is one of the managed devices themselves.
func (m *Manager) isSelf(mac, ip string) bool {
	if _, ok := m.self[store.NormMAC(mac)]; ok { // callers hold m.mu
		return true
	}
	if ip == "" {
		return false
	}
	for _, d := range m.Cfg.AllDevices() {
		if d.Addr == ip {
			return true
		}
	}
	return false
}

// uplinkPorts returns "device|port" for every port on which another managed device was discovered as a
// neighbor (LLDP/MNDP). A bridge host entry on such a port only says "reachable through that link", so it must
// not decide where a client is attached.
func (m *Manager) uplinkPorts() map[string]bool {
	out := map[string]bool{}
	ns, err := m.St.Neighbors()
	if err != nil {
		return out
	}
	managed := map[string]bool{}
	for _, d := range m.Cfg.AllDevices() {
		managed[d.Name] = true
	}
	for _, n := range ns {
		if !managed[n.Ident] || n.Ident == n.Device {
			continue
		}
		for _, p := range strings.Split(n.Iface, ",") {
			p = strings.TrimSpace(p)
			if i := strings.LastIndex(p, "/"); i >= 0 {
				p = p[i+1:]
			}
			if p != "" {
				out[n.Device+"|"+p] = true
			}
		}
	}
	return out
}

// deviceDepths counts the hops of every managed device from the gateway (the device with WAN interfaces, else the
// routers) along the neighbor links. Needs m.mu held for m.snaps. Unreachable devices are absent.
func (m *Manager) deviceDepths() map[string]int {
	out := map[string]int{}
	ns, err := m.St.Neighbors()
	if err != nil {
		return out
	}
	adj := map[string][]string{}
	managed := map[string]bool{}
	for _, d := range m.Cfg.AllDevices() {
		managed[d.Name] = true
	}
	for _, n := range ns {
		if managed[n.Device] && managed[n.Ident] && n.Device != n.Ident {
			adj[n.Device] = append(adj[n.Device], n.Ident)
			adj[n.Ident] = append(adj[n.Ident], n.Device)
		}
	}
	var queue []string
	for _, d := range m.Cfg.AllDevices() {
		if s := m.snaps[d.Name]; s != nil && len(s.wan) > 0 {
			out[d.Name] = 0
			queue = append(queue, d.Name)
		}
	}
	if len(queue) == 0 { // no WAN info yet: routers are the best guess
		for _, d := range m.Cfg.AllDevices() {
			if d.Role == "router" {
				out[d.Name] = 0
				queue = append(queue, d.Name)
			}
		}
	}
	for len(queue) > 0 {
		d := queue[0]
		queue = queue[1:]
		for _, n := range adj[d] {
			if _, seen := out[n]; !seen {
				out[n] = out[d] + 1
				queue = append(queue, n)
			}
		}
	}
	return out
}

// nearer reports whether device a is closer to the gateway than b (unknown devices are farthest; ties by name so
// the result does not depend on map order).
func nearer(depth map[string]int, a, b string) bool {
	da, oka := depth[a]
	db, okb := depth[b]
	switch {
	case oka != okb:
		return oka
	case da != db:
		return da < db
	}
	return a < b
}

// Diagnostics returns the raw tables the last poll cycle got from every device plus the derived uplink ports, so a
// wrong location or presence can be explained from real data. No credentials are involved.
func (m *Manager) Diagnostics() map[string]any {
	up := []string{}
	for k, v := range m.uplinkPorts() {
		if v {
			up = append(up, k)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	devs := map[string]any{}
	for name, s := range m.snaps {
		devs[name] = map[string]any{"role": s.role, "polled": s.at.Unix(), "wifi": rowsOrEmpty(s.wifi), "bridge_hosts": rowsOrEmpty(s.bridge),
			"arp": rowsOrEmpty(s.arp), "dhcp_leases": rowsOrEmpty(s.leases), "wan_interfaces": wanList(s.wan),
			"ip_addresses": rowsOrEmpty(s.addrs), "pppoe_clients": rowsOrEmpty(s.pppoe), "routes": rowsOrEmpty(s.routes)}
	}
	ups := []Upstream{}
	for name, s := range m.snaps {
		ups = append(ups, upstreamsFor(name, s, m.En.Vendor)...)
	}
	return map[string]any{"devices": devs, "uplink_ports": up, "upstream": ups}
}

func rowsOrEmpty(r []Row) []Row {
	if r == nil {
		return []Row{}
	}
	return r
}

// capHint finds a managed device named in a registration row, e.g. the CAP a controller-side row belongs to.
// RouterOS field names for that differ between versions, so any value that equals a managed device name counts
// (the controller itself and the "interface"/"ssid" values are ignored).
func capHint(r Row, names map[string]string, self string) string {
	for k, v := range r {
		if k == "interface" || k == "ssid" || k == "mac-address" {
			continue
		}
		if n, ok := names[strings.ToLower(strings.TrimSpace(v))]; ok && n != self {
			return n
		}
	}
	return ""
}

// neighborIdents maps MAC -> identity for devices announced via LLDP/MNDP (MikroTik, many APs and switches).
func (m *Manager) neighborIdents() map[string]string {
	out := map[string]string{}
	if ns, err := m.St.Neighbors(); err == nil {
		for _, n := range ns {
			if n.Ident != "" && n.MAC != "" {
				out[store.NormMAC(n.MAC)] = n.Ident
			}
		}
	}
	return out
}

// fallbackName names a client the DHCP server gave no host-name: neighbor identity first, then a
// (static or reverse) DNS name for its address. Reverse lookups run in the background and show up on a later cycle.
func (m *Manager) fallbackName(mac, ip string, idents map[string]string) string {
	if id := idents[mac]; id != "" {
		return id
	}
	if a, err := netip.ParseAddr(ip); err == nil {
		return m.En.Name(a)
	}
	return ""
}

// merge builds client observations from all device snapshots and writes them.
func (m *Manager) merge() {
	uplinks := m.uplinkPorts()
	m.mu.Lock()
	byMAC := map[string]*store.Observation{}
	nd6 := map[string]string{}
	rank := map[string]int{} // higher wins for location (Device/Iface)
	upd := func(mac string, loc bool, prio int, f func(o *store.Observation)) {
		mac = store.NormMAC(mac)
		if mac == "" || mac == "00:00:00:00:00:00" {
			return
		}
		o := byMAC[mac]
		if o == nil {
			o = &store.Observation{MAC: mac}
			byMAC[mac] = o
		}
		if loc {
			if prio < rank[mac] {
				return
			}
			rank[mac] = prio
		}
		f(o)
	}
	// where a client is really attached: a bridge host entry on a port that does not face another managed
	// device (an "edge" port). With CAPsMAN the controller lists every Wi-Fi client, but the AP the client talks
	// to learns its MAC on a non-uplink port, which is the better answer.
	type edgeLoc struct {
		dev, iface string
		ap         bool
	}
	edge := map[string]edgeLoc{}
	hint := map[string]string{} // MAC -> managed device named inside the registration row (CAP identity)
	// hosts learned on an uplink port (an unmanaged switch may sit in between) and MACs of the managed devices
	// themselves; see the shared-port pass below
	shared := map[string]edgeLoc{}
	depth := m.deviceDepths() // hops from the gateway; the shared port nearest to it wins
	ownMAC := m.neighborIdents()
	names := map[string]string{}
	for _, d := range m.Cfg.AllDevices() {
		names[strings.ToLower(d.Name)] = d.Name
	}
	for dev, s := range m.snaps {
		if time.Since(s.at) > 3*time.Minute {
			continue // device not answering: its last answers say nothing about the network now
		}
		apBonus := 0
		if s.role == "ap" {
			apBonus = 10 // a wifi registration on the AP itself beats the CAPsMAN manager's copy
		}
		for _, r := range s.bridge {
			if r.Bool("invalid") {
				continue
			}
			if r.Bool("local") {
				ownMAC[store.NormMAC(r["mac-address"])] = dev
				continue
			}
			iface := r["on-interface"]
			if s.wan[iface] {
				continue // upstream side (modem, ISP): not a LAN client
			}
			mac := store.NormMAC(r["mac-address"])
			if uplinks[dev+"|"+iface] {
				// learned via a trunk to another managed device: not where the host is attached, unless no
				// device behind that trunk has it on a port of its own (then an unmanaged switch is in between)
				if old, ok := shared[mac]; !ok || nearer(depth, dev, old.dev) {
					shared[mac] = edgeLoc{dev, iface, false}
				}
				continue
			}
			if _, ok := edge[mac]; !ok || s.role == "ap" {
				edge[mac] = edgeLoc{dev, iface, s.role == "ap"}
			}
			via := "bridge host on " + dev + "/" + iface
			upd(r["mac-address"], true, 1+apBonus, func(o *store.Observation) {
				o.Device, o.Iface, o.Present = dev, iface, true
				if o.Via == "" || strings.HasPrefix(o.Via, "ARP") {
					o.Via = via
				}
			})
		}
		for _, r := range s.nd6 {
			if r["status"] != "failed" && r["status"] != "incomplete" && r["address"] != "" {
				nd6[strings.TrimSuffix(r["address"], "/128")] = store.NormMAC(r["mac-address"])
			}
		}
		for _, r := range s.arp {
			if r.Bool("invalid") || r["status"] == "failed" || r["status"] == "incomplete" || s.wan[r["interface"]] {
				continue
			}
			// DHCP-created ARP entries are "permanent" for as long as the lease lives: no sign of life
			pres := r["status"] == "reachable" || r["status"] == "delay" || r["status"] == "probe" ||
				(r["status"] == "permanent" && !r.Bool("dhcp") && !r.Bool("dynamic"))
			addr := r["address"]
			iface := r["interface"]
			via := "ARP " + r["status"] + " on " + dev
			upd(r["mac-address"], false, 0, func(o *store.Observation) {
				if o.IP == "" {
					o.IP = addr
				}
				if pres {
					o.Present = true
					if o.Via == "" {
						o.Via = via
					}
					if o.Device == "" {
						o.Device, o.Iface = dev, iface
					}
				}
			})
		}
		for _, r := range s.leases {
			mac := r["active-mac-address"]
			if mac == "" {
				mac = r["mac-address"]
			}
			ip := r["active-address"]
			if ip == "" {
				ip = r["address"]
			}
			host := r["host-name"]
			if r["status"] != "bound" && r["status"] != "" {
				continue
			}
			upd(mac, false, 0, func(o *store.Observation) {
				o.IP = ip
				if host != "" {
					o.Hostname = host
				} else if r["comment"] != "" && o.Hostname == "" {
					o.Hostname = r["comment"]
				}
			})
		}
		for _, r := range s.wifi {
			sig := parseSignal(r["signal"])
			iface, ssid, band, tx, rx := r["interface"], r["ssid"], r["band"], r["tx-rate"], r["rx-rate"]
			via := "Wi-Fi registration on " + dev
			if h := capHint(r, names, dev); h != "" {
				hint[store.NormMAC(r["mac-address"])] = h
			}
			upd(r["mac-address"], true, 20+apBonus, func(o *store.Observation) {
				o.WiFi, o.Present, o.Via = true, true, via
				o.Device, o.Iface, o.SSID, o.Band, o.Signal, o.TxRate, o.RxRate = dev, iface, ssid, band, sig, tx, rx
			})
		}
	}
	// A MAC seen only on uplink ports, that no managed device reports on a port or radio of its own and that is
	// not a managed device itself, hangs on an unmanaged switch at the uplink port.
	for mac, e := range shared {
		if _, ok := edge[mac]; ok || ownMAC[mac] != "" {
			continue
		}
		if o := byMAC[mac]; o != nil && o.WiFi {
			continue
		}
		e := e
		upd(mac, true, 1, func(o *store.Observation) {
			o.Device, o.Iface, o.Present = e.dev, e.iface, true
			if o.Via == "" || strings.HasPrefix(o.Via, "ARP") {
				o.Via = "bridge host on " + e.dev + "/" + e.iface + " (shared port)"
			}
		})
	}
	// A Wi-Fi client reported only by a controller (router) but sitting on an AP's edge port belongs to that AP.
	roleOf := map[string]string{}
	for dev, s := range m.snaps {
		roleOf[dev] = s.role
	}
	for mac, o := range byMAC {
		if !o.WiFi {
			continue
		}
		if h := hint[mac]; h != "" {
			o.Device = h
		} else if e, ok := edge[mac]; ok && e.ap && roleOf[o.Device] != "ap" {
			o.Device, o.Iface = e.dev, e.iface
		} else if roleOf[o.Device] != "ap" && o.Device != "" {
			// Only the controller reports this client and no AP showed it on an edge port this cycle (aged-out
			// bridge host, slow poll). Keep the AP it was last seen on instead of flipping to the controller,
			// otherwise every gap counts as a roam.
			var prev, iface string
			if m.St.DB.QueryRow(`SELECT device, iface FROM clients WHERE mac=? AND wifi=1`, mac).Scan(&prev, &iface) == nil &&
				prev != o.Device && roleOf[prev] == "ap" {
				o.Device, o.Iface = prev, iface
			}
		}
	}
	m.mu.Unlock()

	m.mu.Lock()
	for dev, s := range m.snaps {
		if time.Since(s.at) > 3*time.Minute {
			continue
		}
		for _, v := range vpnObservations(dev, s.peers) {
			v := v
			byMAC[v.MAC] = &v
		}
	}
	for mac, o := range byMAC {
		if m.isSelf(mac, o.IP) {
			delete(byMAC, mac)
			m.St.DB.Exec(`DELETE FROM clients WHERE mac=?`, mac) // cleanup of rows created by earlier versions
		}
	}
	m.mu.Unlock()
	obs := make([]store.Observation, 0, len(byMAC))
	ipmac := map[string]string{}
	hubNames := map[string]string{}
	for ip, mac := range nd6 {
		if _, ok := byMAC[mac]; ok { // never creates a client, only attaches IPv6 addresses to known ones
			ipmac[ip] = mac
		}
	}
	v6 := map[string]string{}
	for ip, mac := range nd6 {
		if _, ok := byMAC[mac]; ok && store.IsGlobalIPv6(ip) {
			v6[ip] = mac
		}
	}
	m.St.NoteIPv6(v6, time.Now().Unix()) // evidence for addresses from old (dynamic) prefixes
	m.St.NoteULA(ipmac, time.Now().Unix())
	var newOnes []store.Observation
	idents := m.neighborIdents()
	for mac, o := range byMAC {
		if !strings.HasPrefix(mac, "VPN:") {
			o.Vendor = m.En.Vendor(mac)
		}
		if o.Hostname == "" {
			o.Hostname = m.fallbackName(mac, o.IP, idents)
		}
		obs = append(obs, *o)
		if o.IP != "" {
			ipmac[o.IP] = mac
		}
		for _, a := range o.Aliases {
			ipmac[a] = mac
			m.rekeyAlias(mac, a)
		}
		n := o.Hostname
		if n == "" {
			n = o.IP
		}
		if n != "" {
			hubNames[mac] = n
		}
		if !m.known[mac] {
			m.known[mac] = true
			newOnes = append(newOnes, *o)
		}
	}
	if len(obs) == 0 {
		return
	}
	if !m.primed {
		// load existing clients so a restart doesn't alert for everyone
		if cs, err := m.St.Clients(); err == nil {
			for _, c := range cs {
				m.known[c.MAC] = true
			}
			newOnes = newOnes[:0]
			for _, o := range obs {
				if !containsClient(cs, o.MAC) {
					newOnes = append(newOnes, o)
				}
			}
		}
		m.primed = true
		if len(m.known) > 0 && len(newOnes) == len(obs) {
			newOnes = nil // first ever run: everything is "new", suppress alerts
		}
	}
	m.Hub.SetIPMap(ipmac, hubNames)
	m.Hub.SetWifiClients(wifiPerDevice(obs, m.Cfg.AllDevices()))
	if _, err := m.St.UpsertClients(obs); err != nil {
		m.Log.Error("upsert clients", "err", err)
	}
	m.St.MarkOffline(120)
	for _, o := range newOnes {
		m.Ev.NewClient(o.MAC, o.Hostname, o.IP)
	}
}

// wifiPerDevice counts the Wi-Fi clients that are present on each managed device, as the client merge placed them
// (the AP a client talks to, not the CAPsMAN controller that lists all registrations).
func wifiPerDevice(obs []store.Observation, devs []config.Device) map[string]int {
	out := make(map[string]int, len(devs))
	for _, d := range devs {
		out[d.Name] = 0
	}
	for _, o := range obs {
		if o.WiFi && o.Present && o.Device != "" {
			if _, ok := out[o.Device]; ok {
				out[o.Device]++
			}
		}
	}
	return out
}

func containsClient(cs []store.Client, mac string) bool {
	for _, c := range cs {
		if c.MAC == mac {
			return true
		}
	}
	return false
}

// rekeyAlias moves traffic that was stored under a bare address of a VPN peer (before the peer was known by all of
// its addresses) to the peer's client key. Runs once per address and process, in the background.
func (m *Manager) rekeyAlias(key, addr string) {
	m.rekeyMu.Lock()
	k := key + "|" + addr
	if m.rekeyed[k] {
		m.rekeyMu.Unlock()
		return
	}
	if m.rekeyed == nil {
		m.rekeyed = map[string]bool{}
	}
	m.rekeyed[k] = true
	m.rekeyMu.Unlock()
	go func() {
		if err := m.St.RekeyTraffic(addr, key); err != nil {
			m.Log.Error("re-key vpn traffic", "from", addr, "to", key, "err", err)
		}
	}()
}

// WANSummary names the WAN interfaces of the gateways ("gw: ether1, pppoe-out1"), stored with each speed test so
// results after a failover or a changed uplink are not mixed up.
func (m *Manager) WANSummary() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var parts []string
	for name, s := range m.snaps {
		if l := wanList(s.wan); len(l) > 0 {
			parts = append(parts, name+": "+strings.Join(l, ", "))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

func wanList(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
