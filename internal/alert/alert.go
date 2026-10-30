// Package alert raises, de-duplicates and delivers alerts.
package alert

import (
	"fmt"
	"log/slog"
	"net/http"
	"sort"
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

	// AccessList reads the Wi-Fi access-list rows of a device (read-only); nil = no reject-loop hint (#211)
	AccessList func(device string) ([]map[string]string, error)

	start time.Time

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
	roams       map[string]roamConn    // client MAC -> its last Wi-Fi connect (see events_roam.go)
	roamDHCP    map[string]*roamDHCP   // client MAC -> failed DHCP offers right after such a connect
	acl         map[string]*aclState   // device -> access-list baseline of the running burst (#211)
	aclHits     map[string]aclRule     // device|mac -> reject rule whose counter rose
	stories     map[string]*story      // kind|subject -> open story (see stories.go)
	tgtFails    map[string]int         // own internet target -> failed checks in a row (targets.go)

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
	return e.raise(kind, subject, severity, msg, "", nil, cooldown)
}

// RaiseP raises an alert from a message key (the English template with {name} placeholders) and its parameters.
// The text is stored in English as well (notifications, /metrics); the UI renders key + params in the viewer's language.
// tail is English text that follows the base (' – note' segments); it is translated separately. Notes the engine appends later (maintenance, learning, WAN side effect) follow the base text and are translated separately.
func (e *Engine) RaiseP(kind, subject, severity, key string, params map[string]string, tail string, cooldown time.Duration) bool {
	kv := make([]string, 0, 2*len(params))
	for k, v := range params {
		kv = append(kv, "{"+k+"}", v)
	}
	msg := strings.NewReplacer(kv...).Replace(key) // one pass: a value that looks like a placeholder stays as it is
	return e.raise(kind, subject, severity, msg+tail, key, params, cooldown)
}

func (e *Engine) raise(kind, subject, severity, msg, key string, params map[string]string, cooldown time.Duration) bool {
	_, ok := e.raiseID(kind, subject, severity, msg, key, params, 0, 0, 0, cooldown)
	return ok
}

// raiseID is raise for alerts that may collect events later (count/first/last, see stories.go); it returns the alert ID.
func (e *Engine) raiseID(kind, subject, severity, msg, key string, params map[string]string, count int, first, last int64, cooldown time.Duration) (int64, bool) {
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
	if severity != "info" && e.St.IsInfoKind(kind) {
		severity = "info"
		msg += " – set to info in the alert settings"
	}
	lastKey := kind + "|" + subject
	e.mu.Lock()
	if t, ok := e.last[lastKey]; ok && time.Since(t) < cooldown {
		e.mu.Unlock()
		return 0, false
	}
	e.last[lastKey] = time.Now()
	if (kind == "traffic_spike" || kind == "port_scan" || kind == "rogue_dns") && len(e.wan) > 0 {
		msg += " – a WAN link is down right now, this is probably a side effect"
	} else if (kind == "traffic_spike" || kind == "port_scan" || kind == "rogue_dns") && !e.lastWAN.IsZero() && time.Since(e.lastWAN) < 10*time.Minute {
		msg += fmt.Sprintf(" – a WAN link flapped %s ago, this may be a side effect", fmtDur(time.Since(e.lastWAN)))
	}
	e.mu.Unlock()
	a := store.Alert{TS: time.Now().Unix(), Kind: kind, Subject: subject, Severity: severity, Msg: msg, MsgKey: key, Params: params, Count: count, First: first, Last: last}
	id, err := e.St.AddAlert(a)
	if err != nil {
		e.Log.Error("store alert", "err", err)
	}
	a.ID = id
	if e.Notify != nil {
		e.Notify(a)
	}
	go e.deliver(a)
	return id, true
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
