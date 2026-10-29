// Package alert raises, de-duplicates and delivers alerts.
package alert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/smtp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/live"
	"github.com/napfkuchen1/mtmon/internal/store"
)

type bootInfo struct {
	ver string
	up  int64
}

type Engine struct {
	Cfg *config.Config
	St  *store.Store
	Hub *live.Hub
	Log *slog.Logger
	// SelfName names the managed router/AP that owns a MAC; its traffic is reported under the device name
	SelfName func(mac string) (string, bool)
	// Depths gives the hops of every managed device from the gateway; with the stored neighbours it names the
	// upstream device of an outage that took several devices down together
	Depths func() map[string]int
	start    time.Time

	mu       sync.Mutex
	last     map[string]time.Time
	devUp    map[string]bool
	ifDown   map[string]time.Time // device/iface -> when it went down
	wan      map[string]*wanState // device -> WAN uplinks currently down
	cpuHigh  map[string]int
	baseline float64
	lastWAN  time.Time // last WAN uplink down/up event (used to explain follow-up findings)
	// offline devices wait offGroupDelay for companions, so a shared cause (mtmon's own link, a switch) is one alert
	pendingOff    map[string]string
	offTimer      *time.Timer
	offGroupDelay time.Duration
	// a device that is gone for less than rebootGrace is a reboot (update, power cycle), not an outage: its
	// offline alert and the neighbours' link-down alerts wait this long, and a quick return is one info line
	rebootGrace time.Duration
	boot        map[string]bootInfo  // device -> version and uptime at the last poll
	downAt      map[string]time.Time // device -> when it stopped answering
	lastDevDown time.Time
	ifPending   map[string]bool // device/iface -> link-down alert is still held back
	spikeRun    int
	spikeDay    map[string]string      // client -> day of its last traffic_spike alert
	watchDown   map[string]time.Time   // watched client -> when it was last seen (it is offline and was reported)
	ifFolded    map[string]bool        // device/iface -> the link-down was folded into the alert of the device behind it
	resolved    map[string]bool        // device -> its offline alert was closed on return (a reboot note may follow)
	bursts      map[string][]time.Time // router log patterns: key -> recent occurrences

	Suggest func(store.Client) string // group name suggested for a client without a group (set by main), "" = none
	hc      *http.Client
	Notify  func(store.Alert) // test hook
	wanBad  bool              // the internet quality alert is open (a recovery follows)
}

func New(c *config.Config, s *store.Store, h *live.Hub, log *slog.Logger) *Engine {
	return &Engine{Cfg: c, St: s, Hub: h, Log: log, start: time.Now(), last: map[string]time.Time{},
		devUp: map[string]bool{}, pendingOff: map[string]string{}, offGroupDelay: 5 * time.Second, rebootGrace: 90 * time.Second, boot: map[string]bootInfo{}, downAt: map[string]time.Time{}, ifPending: map[string]bool{}, spikeDay: map[string]string{}, watchDown: map[string]time.Time{}, resolved: map[string]bool{}, ifFolded: map[string]bool{}, ifDown: map[string]time.Time{}, wan: map[string]*wanState{}, cpuHigh: map[string]int{}, hc: &http.Client{Timeout: 8 * time.Second}}
}

// SetDelays changes how long offline alerts wait for companions and for a reboot to finish (tests use short ones).
func (e *Engine) SetDelays(group, reboot time.Duration) {
	e.mu.Lock()
	e.offGroupDelay, e.rebootGrace = group, reboot
	e.mu.Unlock()
}

// Raise stores and delivers an alert unless the same kind|subject fired within cooldown.
func (e *Engine) Raise(kind, subject, severity, msg string, cooldown time.Duration) bool {
	if kind == "device_offline" || kind == "iface_down" || kind == "iface_up" {
		if e.inMaintenance(kind, subject) {
			severity = "info"
			msg += " (maintenance window)"
		}
	}
	if kind == "traffic_spike" && e.St.LearningUntil() > 0 { // the baseline is being re-learned: record, do not alarm
		severity = "info"
		msg += " – learning: normal traffic is being re-learned"
	}
	key := kind + "|" + subject
	e.mu.Lock()
	if t, ok := e.last[key]; ok && time.Since(t) < cooldown {
		e.mu.Unlock()
		return false
	}
	e.last[key] = time.Now()
	if (kind == "traffic_spike" || kind == "port_scan" || kind == "rogue_dns") && len(e.wan) > 0 {
		msg += " – a WAN link is down right now, this is probably a side effect"
	} else if (kind == "traffic_spike" || kind == "port_scan" || kind == "rogue_dns") && !e.lastWAN.IsZero() && time.Since(e.lastWAN) < 10*time.Minute {
		msg += fmt.Sprintf(" – a WAN link flapped %s ago, this may be a side effect", fmtDur(time.Since(e.lastWAN)))
	}
	e.mu.Unlock()
	a := store.Alert{TS: time.Now().Unix(), Kind: kind, Subject: subject, Severity: severity, Msg: msg}
	id, err := e.St.AddAlert(a)
	if err != nil {
		e.Log.Error("store alert", "err", err)
	}
	a.ID = id
	if e.Notify != nil {
		e.Notify(a)
	}
	go e.deliver(a)
	return true
}

// inMaintenance: the alert concerns a device in a maintenance window, or a link on another device that leads to it
// (the uplink of an access point that is being worked on).
func (e *Engine) inMaintenance(kind, subject string) bool {
	m := e.St.Maintenance()
	if len(m) == 0 {
		return false
	}
	if kind == "device_offline" {
		_, ok := m[subject]
		return ok
	}
	dev, iface, _ := strings.Cut(subject, "/")
	if _, ok := m[dev]; ok {
		return true
	}
	ns, _ := e.St.Neighbors()
	for _, n := range ns {
		if n.Device != dev || n.Iface != iface {
			continue
		}
		for name := range m {
			if n.Ident == name {
				return true
			}
			if d, ok := e.Cfg.Device(name); ok && n.Addr != "" && n.Addr == d.Addr {
				return true
			}
		}
	}
	return false
}

// ResetBaseline forgets the learned traffic level and the spike-per-day memory (a learning period follows).
func (e *Engine) ResetBaseline() {
	e.mu.Lock()
	e.baseline, e.spikeRun = 0, 0
	for k := range e.spikeDay {
		delete(e.spikeDay, k)
	}
	e.mu.Unlock()
}

// --- poller.Events ---

func (e *Engine) DeviceState(name string, up bool, errMsg string, cpu, temp float64) {
	e.mu.Lock()
	prev, seen := e.devUp[name]
	e.devUp[name] = up
	if up {
		if cpu > 90 {
			e.cpuHigh[name]++
		} else {
			e.cpuHigh[name] = 0
		}
	}
	high := e.cpuHigh[name]
	queued := false
	wasPending := false
	if up {
		_, wasPending = e.pendingOff[name]
		delete(e.pendingOff, name)
	} else if !seen || prev {
		e.pendingOff[name] = errMsg
		e.downAt[name] = time.Now()
		e.lastDevDown = time.Now()
		queued = true
	}
	e.mu.Unlock()
	switch {
	case queued:
		e.queueOffline()
	case up && seen && !prev && !wasPending: // came back before the offline alert went out: nothing to undo
		e.mu.Lock()
		out := time.Duration(0)
		if t, ok := e.downAt[name]; ok {
			out = time.Since(t)
		}
		e.mu.Unlock()
		note := " – resolved: the device is reachable again"
		if out > 0 {
			note = " – resolved: back after " + fmtDur(out)
		}
		if n := e.St.ResolveAlerts("device_offline", name, note); n > 0 {
			e.mu.Lock()
			e.resolved[name] = true
			e.mu.Unlock()
		}
		e.Raise("device_online", name, "info", name+" is back online", time.Minute)
	}
	if up && high >= 3 {
		e.Raise("cpu_high", name, "warning", fmt.Sprintf("%s CPU at %.0f%% for >30s", name, cpu), 30*time.Minute)
	}
	if up && temp >= 80 {
		e.Raise("temp_high", name, "warning", fmt.Sprintf("%s temperature %.0f°C", name, temp), 30*time.Minute)
	}
}

// DeviceBoot is called after every successful system poll. A uptime that went backwards is a reboot; with a new
// version it was an update. Both are one info alert instead of the offline/online pair.
func (e *Engine) DeviceBoot(name, ver string, uptime int64) {
	e.mu.Lock()
	p, ok := e.boot[name]
	e.boot[name] = bootInfo{ver, uptime}
	down, wasDown := e.downAt[name]
	delete(e.downAt, name)
	reported := e.resolved[name] // an offline alert went out for this outage and was closed on return
	delete(e.resolved, name)
	e.mu.Unlock()
	if !ok || p.up <= 0 || uptime >= p.up {
		return
	}
	if reported && wasDown { // the offline alert says what happened; no second info line about the same reboot
		note := fmt.Sprintf(" – the device rebooted (uptime now %s)", fmtDur(time.Duration(uptime)*time.Second))
		if ver != "" && p.ver != "" && ver != p.ver {
			note = fmt.Sprintf(" – the device updated %s → %s and rebooted", p.ver, ver)
		}
		e.St.AmendAlert("device_offline", name, note)
		return
	}
	gone := ""
	if wasDown {
		gone = ", " + fmtDur(time.Since(down))
	}
	if ver != "" && p.ver != "" && ver != p.ver {
		e.Raise("device_updated", name, "info", fmt.Sprintf("%s updated %s → %s (reboot%s)", name, p.ver, ver, gone), 0)
		return
	}
	e.Raise("device_reboot", name, "info", fmt.Sprintf("%s rebooted %s ago (version %s%s)", name, fmtDur(time.Duration(uptime)*time.Second), ver, gone), time.Minute)
}

// reachReason names why a poll failed in one word: a timeout (nothing answers, usually the path or the device is
// down) differs from a refused connection (the device answers but the API service does not).
func reachReason(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "timeout") || strings.Contains(m, "deadline") || strings.Contains(m, "no route") || strings.Contains(m, "unreachable"):
		return "timeout"
	case strings.Contains(m, "refused"):
		return "refused"
	case strings.Contains(m, "authentication"):
		return "auth"
	}
	return "error"
}

// queueOffline collects devices that just went offline; after offGroupDelay they are reported together.
func (e *Engine) queueOffline() {
	delay := e.offGroupDelay + e.rebootGrace
	if delay <= 0 {
		e.flushOffline()
		return
	}
	e.mu.Lock()
	if e.offTimer == nil {
		e.offTimer = time.AfterFunc(delay, e.flushOffline)
	}
	e.mu.Unlock()
}

func (e *Engine) flushOffline() {
	e.mu.Lock()
	pend := e.pendingOff
	e.pendingOff = map[string]string{}
	e.offTimer = nil
	for n := range pend {
		e.devUp[n] = false
	}
	e.mu.Unlock()
	if len(pend) == 0 {
		return
	}
	if m := e.St.Maintenance(); len(m) > 0 { // planned work is not a shared cause: report those on their own
		for n, msg := range pend {
			if _, ok := m[n]; ok {
				e.Raise("device_offline", n, "critical", fmt.Sprintf("%s unreachable: %s (%s)", n, msg, reachReason(msg)), time.Minute)
				delete(pend, n)
			}
		}
		if len(pend) == 0 {
			return
		}
	}
	if len(pend) == 1 {
		for n, msg := range pend {
			e.Raise("device_offline", n, "critical", fmt.Sprintf("%s unreachable: %s (%s)", n, msg, reachReason(msg)), time.Minute)
		}
		return
	}
	names := make([]string, 0, len(pend))
	reasons := map[string]int{}
	for n, msg := range pend {
		names = append(names, n)
		reasons[reachReason(msg)]++
	}
	sort.Strings(names)
	if root, deps, ok := e.outageRoot(pend); ok {
		e.Raise("devices_unreachable", root, "warning", fmt.Sprintf("%s is unreachable (%s); %s unreachable too because their uplink runs through %s", root, pend[root], strings.Join(deps, ", "), root), 5*time.Minute)
		return
	}
	hint := "several devices failing together point to a shared cause (the link between mtmon and the network, a switch or power), not to each device"
	if reasons["refused"] == len(pend) {
		hint = "all answer but refuse the API connection: check the API service / firewall on the devices"
	}
	e.Raise("devices_unreachable", "group", "warning", fmt.Sprintf("mtmon cannot reach %d devices: %s – %s", len(pend), strings.Join(names, ", "), hint), 5*time.Minute)
}

// outageRoot finds the one failed device the others hang below: the failing devices must form one connected
// group in the topology, and the member nearest to the gateway is the cause. Without topology or depths (or when
// the failures are separate islands) it reports nothing and the generic shared-cause text applies.
func (e *Engine) outageRoot(pend map[string]string) (string, []string, bool) {
	if e.Depths == nil || e.St == nil {
		return "", nil, false
	}
	ns, err := e.St.Neighbors()
	if err != nil {
		return "", nil, false
	}
	adj := map[string][]string{}
	for _, n := range ns {
		_, a := pend[n.Device]
		_, b := pend[n.Ident]
		if a && b && n.Device != n.Ident {
			adj[n.Device] = append(adj[n.Device], n.Ident)
			adj[n.Ident] = append(adj[n.Ident], n.Device)
		}
	}
	depth := e.Depths()
	root, best := "", -1
	for n := range pend {
		d, ok := depth[n]
		if !ok {
			return "", nil, false
		}
		if best < 0 || d < best || (d == best && n < root) {
			root, best = n, d
		}
	}
	seen := map[string]bool{root: true}
	for q := []string{root}; len(q) > 0; q = q[1:] {
		for _, x := range adj[q[0]] {
			if !seen[x] {
				seen[x] = true
				q = append(q, x)
			}
		}
	}
	if len(seen) != len(pend) {
		return "", nil, false
	}
	var deps []string
	for n := range pend {
		if n != root {
			deps = append(deps, n)
		}
	}
	sort.Strings(deps)
	return root, deps, true
}

// dynamicPrefixes name interfaces that RouterOS creates and removes by itself (tunnels, CAP/CAPsMAN, virtual
// ethernet); their going down or up is normal and never alerts. The loopback "lo" is matched exactly (see
// isDynamicIface): as a prefix it would also hide user-named ports like "lounge" or "lo-bridge".
var dynamicPrefixes = []string{"pppoe-", "l2tp-", "sstp-", "ovpn-", "<", "veth", "cap", "wifi-cap", "wg-peer"}

func isDynamicIface(name string) bool {
	if name == "lo" {
		return true
	}
	for _, p := range dynamicPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// wanPrefixes name the interfaces that make up an internet uplink (PPPoE and the tunnels on top of it). A modem
// resync takes several of them down together, so they are reported as one WAN event per device.
var wanPrefixes = []string{"pppoe-out", "ipip", "ipv6-", "gre", "sit", "6to4", "eoip"}

func isWANIface(name string) bool {
	for _, p := range wanPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

type wanState struct {
	since time.Time
	down  map[string]bool
	seen  []string // every member that was down during this outage, in order
}

func fmtDur(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

// wanState bundles uplink interfaces: one alert when the first goes down, one when the last is back, with duration.
// recurMinDays: earlier days with a WAN drop at the same time of day before a drop counts as recurring.
const recurMinDays = 3

// recurringWAN counts the earlier days on which this device's WAN dropped within ±5 minutes of now's time of day
// (from the stored alert history, so it survives restarts) and returns the clock time for the message.
func (e *Engine) recurringWAN(device string, now time.Time) (int, string) {
	return e.recurring("wan_down", device, now, 5)
}

// recurring counts the earlier days with an alert of this kind and subject within ±tol minutes of now's time of day.
func (e *Engine) recurring(kind, subject string, now time.Time, tol int) (int, string) {
	as, err := e.St.BaselineAlerts(2000)
	if err != nil {
		return 0, ""
	}
	mod := func(t time.Time) int { return t.Hour()*60 + t.Minute() }
	today := now.Format("2006-01-02")
	days := map[string]bool{}
	for _, a := range as {
		if a.Kind != kind || a.Subject != subject {
			continue
		}
		t := time.Unix(a.TS, 0).In(now.Location())
		d := t.Format("2006-01-02")
		if d == today {
			continue
		}
		diff := mod(t) - mod(now)
		if diff < 0 {
			diff = -diff
		}
		if diff > 720 {
			diff = 1440 - diff
		}
		if diff <= tol {
			days[d] = true
		}
	}
	return len(days), now.Format("15:04")
}

func (e *Engine) wanEvent(device, iface string, running bool) {
	now := time.Now()
	e.mu.Lock()
	e.lastWAN = now
	st := e.wan[device]
	if !running {
		if st == nil {
			st = &wanState{since: now, down: map[string]bool{}}
			e.wan[device] = st
		}
		first := len(st.down) == 0
		if !st.down[iface] {
			st.down[iface] = true
			st.seen = append(st.seen, iface)
		}
		e.mu.Unlock()
		if first {
			sev, msg := "warning", fmt.Sprintf("WAN link on %s went down (%s)", device, iface)
			if name := e.plannedReconnect(device, iface, now); name != "" {
				sev = "info"
				msg += fmt.Sprintf(" – planned reconnect (scheduler %s)", name)
			} else if n, at := e.recurringWAN(device, now); n >= recurMinDays {
				sev = "info"
				msg += fmt.Sprintf(" – recurring: the same drop happened on %d earlier days around %s, probably scheduled (provider reconnect, router scheduler)", n, at)
			}
			e.Raise("wan_down", device, sev, msg, time.Minute)
		}
		return
	}
	if st == nil || !st.down[iface] {
		e.mu.Unlock()
		return
	}
	delete(st.down, iface)
	if len(st.down) > 0 {
		e.mu.Unlock()
		return
	}
	delete(e.wan, device)
	dur, members := now.Sub(st.since), strings.Join(st.seen, ", ")
	e.mu.Unlock()
	e.Raise("wan_up", device, "info", fmt.Sprintf("WAN link on %s is back after %s (affected: %s)", device, fmtDur(dur), members), 0)
}

func (e *Engine) InterfaceState(device, iface string, running bool) {
	if isWANIface(iface) {
		e.wanEvent(device, iface, running)
		return
	}
	if isDynamicIface(iface) {
		return
	}
	key := device + "/" + iface
	if !running {
		at := time.Now()
		e.mu.Lock()
		e.ifDown[key] = at
		hold := e.rebootGrace > 0
		e.ifPending[key] = hold
		e.mu.Unlock()
		if !hold {
			e.raiseIfaceDown(device, iface, key)
			return
		}
		time.AfterFunc(e.rebootGrace, func() { // still down after the grace: a real link problem
			e.mu.Lock()
			still := e.ifDown[key].Equal(at) && e.ifPending[key]
			e.ifPending[key] = false
			e.mu.Unlock()
			if still {
				e.raiseIfaceDown(device, iface, key)
			}
		})
		return
	}
	e.mu.Lock()
	since, wasDown := e.ifDown[key]
	held := e.ifPending[key]
	delete(e.ifDown, key)
	delete(e.ifPending, key)
	nearReboot := !e.lastDevDown.IsZero() && time.Since(e.lastDevDown) < e.rebootGrace+3*time.Minute
	e.mu.Unlock()
	if wasDown && held && nearReboot { // the link flapped because the device behind it rebooted: that has its own info
		return
	}
	if wasDown && held { // not a reboot after all: report the flap late but complete
		e.raiseIfaceFlap(device, iface, key, time.Since(since))
	}
	e.mu.Lock()
	folded := e.ifFolded[key]
	delete(e.ifFolded, key)
	e.mu.Unlock()
	if wasDown && folded { // its down was part of a device outage: that has its own alerts
		return
	}
	if wasDown { // recovery after a down is always reported, with the outage length
		// a second "up" with the same text within a minute is the same recovery seen twice (poll overlap)
		e.Raise("iface_up", key, "info", fmt.Sprintf("Interface %s on %s is up again after %s", iface, device, fmtDur(time.Since(since))), time.Minute)
		return
	}
	e.Raise("iface_up", key, "info", fmt.Sprintf("Interface %s on %s is up", iface, device), 5*time.Minute)
}

// downBehind names a managed device that hangs on this port and is unreachable right now: the link-down is then a
// consequence (the device rebooted or lost power) and belongs to the device's own alert.
func (e *Engine) downBehind(device, iface string) string {
	ns, err := e.St.Neighbors()
	if err != nil {
		return ""
	}
	for _, n := range ns {
		if n.Device != device {
			continue
		}
		on := false
		for _, p := range strings.Split(n.Iface, ",") {
			p = strings.TrimSpace(p)
			if i := strings.LastIndex(p, "/"); i >= 0 {
				p = p[i+1:]
			}
			on = on || p == iface
		}
		if !on {
			continue
		}
		name := n.Ident
		if d, ok := e.Cfg.Device(name); !ok || d.Name == device {
			name = ""
			for _, d := range e.Cfg.AllDevices() {
				if n.Addr != "" && d.Addr == n.Addr && d.Name != device {
					name = d.Name
				}
			}
		}
		if name == "" {
			continue
		}
		e.mu.Lock()
		up, seen := e.devUp[name]
		_, pending := e.pendingOff[name]
		e.mu.Unlock()
		if pending || (seen && !up) {
			return name
		}
	}
	return ""
}

// raiseIfaceFlap reports a link that went down and came back inside the reboot grace. That is most often the device
// itself (or the one behind the port) restarting, so it is an info line with the length, not a warning.
func (e *Engine) raiseIfaceFlap(device, iface, key string, d time.Duration) {
	if dev := e.downBehind(device, iface); dev != "" {
		e.mu.Lock()
		e.ifFolded[key] = true
		e.mu.Unlock()
		return
	}
	if d < e.rebootGrace {
		e.Raise("iface_down", key, "info", fmt.Sprintf("Interface %s on %s went down for %s (short flap, possibly a restart)", iface, device, fmtDur(d)), 5*time.Minute)
		return
	}
	e.raiseIfaceDown(device, iface, key)
}

func (e *Engine) raiseIfaceDown(device, iface, key string) {
	if dev := e.downBehind(device, iface); dev != "" {
		e.mu.Lock()
		e.ifFolded[key] = true
		e.mu.Unlock()
		return
	}
	e.Raise("iface_down", key, "warning", fmt.Sprintf("Interface %s on %s went down", iface, device), 5*time.Minute)
}

// NewClientMode says which new clients raise an alert: "all", "unknown" (default: no vendor known or a randomised
// MAC – the ones worth a look) or "off". The Clients → New devices inbox lists every new client either way.
func (e *Engine) NewClientMode() string {
	switch m := e.St.GetMeta("alert_new_client"); m {
	case "all", "off":
		return m
	}
	return "unknown"
}

// randomisedMAC reports a locally administered address (private Wi-Fi address of phones and laptops).
func randomisedMAC(mac string) bool {
	if len(mac) < 2 {
		return false
	}
	switch mac[1] {
	case '2', '6', 'a', 'A', 'e', 'E':
		return true
	}
	return false
}

func (e *Engine) NewClient(mac, name, ip string) {
	if time.Since(e.start) < 3*time.Minute { // warm-up: initial inventory import
		return
	}
	if e.groupSaysNoNewAlert(mac) {
		return
	}
	switch e.NewClientMode() {
	case "off":
		return
	case "unknown":
		if c, err := e.St.Client(mac); err == nil && c.Vendor != "" && !randomisedMAC(mac) {
			return // a known manufacturer on its real address: listed in the inbox, no alert
		}
	}
	n := name
	if n == "" {
		n = ip
	}
	msg := fmt.Sprintf("New client %s (%s) %s", mac, n, ip)
	if n == "" { // neither a name nor an address known yet
		msg = "New client " + mac
	}
	e.Raise("new_client", mac, "info", msg, time.Hour)
}

// Run executes the periodic checks until stop is closed.
func (e *Engine) Run(stop <-chan struct{}) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			e.Checks()
		}
	}
}

// ignored: the client is on the user's alert-ignore list (compared case-insensitively).
func (e *Engine) ignored(mac string) bool {
	for _, m := range e.Cfg.AlertIgnore {
		if strings.EqualFold(strings.TrimSpace(m), mac) {
			return true
		}
	}
	return false
}

// who is the best human name for a client MAC, with the MAC itself in brackets.
func (e *Engine) who(mac string) string {
	if e.SelfName != nil {
		if n, ok := e.SelfName(mac); ok && n != "" {
			return n
		}
	}
	if c, err := e.St.Client(mac); err == nil && c != nil {
		n := c.Label
		if n == "" {
			n = c.Hostname
		}
		if n == "" {
			n = c.IP
		}
		if n != "" {
			return fmt.Sprintf("%s (%s)", n, mac)
		}
	}
	return mac
}

func portList(ps []int) string {
	s := make([]string, len(ps))
	for i, p := range ps {
		s[i] = strconv.Itoa(p)
	}
	return strings.Join(s, ", ")
}

func (e *Engine) scanMsg(s store.ScanSuspect) string {
	if s.Kind == "ports" {
		return fmt.Sprintf("Client %s probed %d ports on %s within 60 s (e.g. %s) – looks like a port scan", e.who(s.MAC), s.Ports, strings.Join(s.Targets, ", "), portList(s.TopPorts))
	}
	return fmt.Sprintf("Client %s contacted %d hosts on port %s within 60 s (e.g. %s) – looks like a network sweep", e.who(s.MAC), s.IPs, portList(s.TopPorts), strings.Join(s.Targets, ", "))
}

// spike raises the traffic-spike alert with the client and destination that cause it. A spike caused by an
// ignored client (backup, scanner) is dropped.
func (e *Engine) spike(snap live.Snapshot, cur, base float64) {
	msg := fmt.Sprintf("Traffic spike: %.1f Mbit/s (baseline %.1f)", cur/1e6, base/1e6)
	sev, subject := "warning", "total"
	if len(snap.Clients) > 0 {
		top := snap.Clients[0]
		if e.ignored(top.Key) {
			return
		}
		r := (top.UpBps + top.DownBps) / 1e6
		msg += fmt.Sprintf(" – mostly %s (%.1f Mbit/s)", e.who(top.Key), r)
		subject = top.Key
		if r*1e6 >= 0.8*cur && e.knownClient(top.Key) {
			sev = "info" // one long-known device (backup, update, stream) explains almost everything: worth a note, not a warning
			msg += " – one known client causes the load, probably a download, backup or stream"
		}
		if g, ok := e.clientGroup(top.Key); ok && g.SpikeInfo {
			sev = "info"
			msg += " – group " + g.Name + " (spikes of this group are recorded as info)"
		}
		day := time.Now().Format("2006-01-02")
		e.mu.Lock()
		if e.spikeDay[top.Key] == day {
			e.mu.Unlock()
			return // at most one spike alert per client and day
		}
		e.spikeDay[top.Key] = day
		e.mu.Unlock()
		if ip, host, b := e.St.TopRemote(top.Key, time.Now().Unix()-120); b > 0 {
			if host != "" {
				ip = host + " / " + ip
			}
			msg += " ↔ " + ip
		}
		// the same client at the same time of day on several days: a schedule (speed test, backup, update), not an incident
		if n, at := e.recurring("traffic_spike", top.Key, time.Now(), 10); n >= recurMinDays {
			sev = "info"
			msg += fmt.Sprintf(" – recurring: the same client had a spike on %d earlier days around %s, probably scheduled (speed test, backup, update)", n, at)
		}
	}
	e.Raise("traffic_spike", subject, sev, msg, time.Hour)
}

// knownClient: the client was first seen more than 24 h ago (a new device doing a lot of traffic stays a warning).
func (e *Engine) knownClient(mac string) bool {
	var first int64
	if err := e.St.DB.QueryRow(`SELECT first_seen FROM clients WHERE mac=?`, mac).Scan(&first); err != nil {
		return false
	}
	return first > 0 && time.Now().Unix()-first > 86400
}

// Checks runs traffic-spike, port-scan and rogue-DNS detection once.
func (e *Engine) Checks() {
	if time.Since(e.start) < 2*time.Minute {
		return
	}
	snap := e.Hub.Snapshot(e.Hub.LastSeq(), 1)
	cur := snap.UpBps + snap.DownBps
	e.mu.Lock()
	if e.baseline == 0 {
		e.baseline = cur
	}
	base := e.baseline
	e.baseline = e.baseline*0.97 + cur*0.03
	e.mu.Unlock()
	// a spike needs to be big (>= 100 Mbit/s at once) or to last (> 20 Mbit/s and 5x the baseline for 5 checks in a row)
	e.mu.Lock()
	if cur > 20e6 && cur > 5*base {
		e.spikeRun++
	} else {
		e.spikeRun = 0
	}
	run := e.spikeRun
	e.mu.Unlock()
	if run > 0 && (cur >= 100e6 || run >= 5) {
		e.spike(snap, cur, base)
	}
	ports, hosts := e.Cfg.PortScanPorts, e.Cfg.PortScanHosts
	if ports <= 0 {
		ports = 100
	}
	if hosts <= 0 {
		hosts = 200
	}
	if sus, err := e.St.PortScanDetails(60, ports, hosts); err == nil {
		for _, s := range sus {
			if e.ignored(s.MAC) {
				continue
			}
			e.Raise("port_scan", s.MAC, "warning", e.scanMsg(s), time.Hour)
		}
	}
	e.wanQualityChecks()
	if len(e.Cfg.DNSResolvers) > 0 {
		rows, err := e.St.DB.Query(`SELECT mac, rip FROM flows WHERE ts>=? AND dir='u' AND rport=53 GROUP BY mac, rip`, time.Now().Unix()-300)
		if err == nil {
			allowed := map[string]bool{}
			for _, r := range e.Cfg.DNSResolvers {
				allowed[r] = true
			}
			for rows.Next() {
				var mac, rip string
				rows.Scan(&mac, &rip)
				if a, err := netip.ParseAddr(rip); err == nil && !e.Cfg.IsLocal(a) && !allowed[rip] {
					e.Raise("rogue_dns", mac, "warning", fmt.Sprintf("Client %s sends DNS to %s (not an allowed resolver)", mac, rip), 6*time.Hour)
				}
			}
			rows.Close()
		}
	}
	e.watchChecks()
}

// WatchOfflineAfter is how long a watched client may be gone before it is reported.
const WatchOfflineAfter = 10 * time.Minute

// watchChecks reports watched clients ("must be online") that have been gone for WatchOfflineAfter, and their return
// with the duration. Clients hanging on a device in a maintenance window are left alone until it is over.
func (e *Engine) watchChecks() {
	cs, err := e.St.WatchedClients()
	if err != nil || len(cs) == 0 {
		e.mu.Lock()
		for k := range e.watchDown { // nothing watched any more: forget old outages
			delete(e.watchDown, k)
		}
		e.mu.Unlock()
		return
	}
	maint := e.St.Maintenance()
	now := time.Now()
	for _, c := range cs {
		name := c.Label
		if name == "" {
			name = c.Hostname
		}
		if name == "" {
			name = c.MAC
		}
		gone := !c.Online && now.Sub(time.Unix(c.LastSeen, 0)) >= WatchOfflineAfter
		e.mu.Lock()
		since, down := e.watchDown[c.MAC]
		e.mu.Unlock()
		switch {
		case gone && !down:
			if _, ok := maint[c.Device]; ok {
				continue
			}
			e.mu.Lock()
			e.watchDown[c.MAC] = time.Unix(c.LastSeen, 0)
			e.mu.Unlock()
			where := ""
			if c.Device != "" {
				where = ", last seen on " + c.Device
			}
			e.Raise("client_offline", c.MAC, "warning", fmt.Sprintf("Watched client %s (%s) has been offline for %s%s", name, c.MAC, fmtDur(now.Sub(time.Unix(c.LastSeen, 0))), where), time.Minute)
		case !gone && c.Online && down:
			e.mu.Lock()
			delete(e.watchDown, c.MAC)
			e.mu.Unlock()
			e.Raise("client_online", c.MAC, "info", fmt.Sprintf("Watched client %s (%s) is back online after %s", name, c.MAC, fmtDur(now.Sub(since))), time.Minute)
		}
	}
}

func (e *Engine) deliver(a store.Alert) {
	text := fmt.Sprintf("[%s] %s", strings.ToUpper(a.Severity), a.Msg)
	if u := e.Cfg.Webhook; u != "" {
		b, _ := json.Marshal(a)
		if resp, err := e.hc.Post(u, "application/json", bytes.NewReader(b)); err == nil {
			resp.Body.Close()
		} else {
			e.Log.Warn("webhook failed", "err", err)
		}
	}
	if u := e.Cfg.NtfyURL; u != "" {
		req, _ := http.NewRequest("POST", u, strings.NewReader(a.Msg))
		req.Header.Set("Title", "mtmon: "+a.Kind)
		req.Header.Set("Priority", map[string]string{"critical": "urgent", "warning": "high"}[a.Severity])
		if resp, err := e.hc.Do(req); err == nil {
			resp.Body.Close()
		} else {
			e.Log.Warn("ntfy failed", "err", err)
		}
	}
	if s := e.Cfg.SMTP; s != nil && s.Host != "" && s.To != "" {
		port := s.Port
		if port == 0 {
			port = 587
		}
		var auth smtp.Auth
		if s.User != "" {
			auth = smtp.PlainAuth("", s.User, s.Pass, s.Host)
		}
		msg := "From: " + s.From + "\r\nTo: " + s.To + "\r\nSubject: mtmon: " + a.Kind + "\r\n\r\n" + text + "\r\n"
		if err := smtp.SendMail(fmt.Sprintf("%s:%d", s.Host, port), auth, s.From, strings.Split(s.To, ","), []byte(msg)); err != nil {
			e.Log.Warn("smtp failed", "err", err)
		}
	}
}

// clientGroup returns the group the client was put into (manually).
func (e *Engine) clientGroup(mac string) (store.Group, bool) {
	gs, err := e.St.ClientGroups()
	if err != nil {
		return store.Group{}, false
	}
	g, ok := gs[store.NormMAC(mac)]
	return g, ok
}

// groupSaysNoNewAlert: the client's group (assigned, or the one suggested for it) is set to not alert on new clients.
func (e *Engine) groupSaysNoNewAlert(mac string) bool {
	if g, ok := e.clientGroup(mac); ok {
		return !g.NewAlert
	}
	if e.Suggest == nil {
		return false
	}
	c, err := e.St.Client(mac)
	if err != nil || c.Group != "" {
		return false
	}
	name := e.Suggest(*c)
	if name == "" {
		return false
	}
	gs, err := e.St.Groups()
	if err != nil {
		return false
	}
	for _, g := range gs {
		if g.Name == name {
			return !g.NewAlert
		}
	}
	return false
}

// WAN quality thresholds over the last WANWindow of probes.
const (
	WANWindow    = 5 * time.Minute
	WANLossPct   = 20.0
	WANLatencyMS = 150.0
	wanMinProbes = 12 // fewer probes than this in the window say nothing (just started, probing off)
)

// wanQualityChecks raises one alert when the internet connection (probes from mtmon, see internal/probe) loses
// packets or gets slow for a whole window, and one info when it is good again. A WAN link that is down has its own
// alerts: a complete loss while one is open is not reported twice.
func (e *Engine) wanQualityChecks() {
	w := e.St.WANQualitySince(time.Now().Add(-WANWindow).Unix())
	if w.Sent < wanMinProbes {
		return
	}
	e.mu.Lock()
	wanDown := len(e.wan) > 0
	bad := e.wanBad
	e.mu.Unlock()
	lossy, slow := w.LossPct >= WANLossPct, w.AvgMS >= WANLatencyMS
	switch {
	case (lossy || slow) && !wanDown:
		if !bad {
			var why []string
			if lossy {
				why = append(why, fmt.Sprintf("%.0f %% of the probes were lost", w.LossPct))
			}
			if slow {
				why = append(why, fmt.Sprintf("answers took %.0f ms on average", w.AvgMS))
			}
			if e.Raise("wan_quality", "internet", "warning", fmt.Sprintf("Internet connection is poor (last %d min, %d probes) – %s", int(WANWindow.Minutes()), w.Sent, strings.Join(why, " – ")), 30*time.Minute) {
				e.mu.Lock()
				e.wanBad = true
				e.mu.Unlock()
			}
		}
	case !lossy && !slow && bad:
		e.mu.Lock()
		e.wanBad = false
		e.mu.Unlock()
		e.St.ResolveAlerts("wan_quality", "internet", " – resolved")
		e.Raise("wan_quality_ok", "internet", "info", fmt.Sprintf("Internet connection is good again: %.0f %% loss, %.0f ms (last %d min)", w.LossPct, w.AvgMS, int(WANWindow.Minutes())), time.Minute)
	}
}
