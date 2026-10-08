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
	"strings"
	"sync"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/live"
	"github.com/napfkuchen1/mtmon/internal/store"
)

type Engine struct {
	Cfg   *config.Config
	St    *store.Store
	Hub   *live.Hub
	Log   *slog.Logger
	start time.Time

	mu       sync.Mutex
	last     map[string]time.Time
	devUp    map[string]bool
	ifDown   map[string]time.Time // device/iface -> when it went down
	wan      map[string]*wanState // device -> WAN uplinks currently down
	cpuHigh  map[string]int
	baseline float64
	hc       *http.Client
	Notify   func(store.Alert) // test hook
}

func New(c *config.Config, s *store.Store, h *live.Hub, log *slog.Logger) *Engine {
	return &Engine{Cfg: c, St: s, Hub: h, Log: log, start: time.Now(), last: map[string]time.Time{},
		devUp: map[string]bool{}, ifDown: map[string]time.Time{}, wan: map[string]*wanState{}, cpuHigh: map[string]int{}, hc: &http.Client{Timeout: 8 * time.Second}}
}

// Raise stores and delivers an alert unless the same kind|subject fired within cooldown.
func (e *Engine) Raise(kind, subject, severity, msg string, cooldown time.Duration) bool {
	key := kind + "|" + subject
	e.mu.Lock()
	if t, ok := e.last[key]; ok && time.Since(t) < cooldown {
		e.mu.Unlock()
		return false
	}
	e.last[key] = time.Now()
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
	e.mu.Unlock()
	switch {
	case !up && (!seen || prev):
		e.Raise("device_offline", name, "critical", fmt.Sprintf("%s unreachable: %s", name, errMsg), time.Minute)
	case up && seen && !prev:
		e.Raise("device_online", name, "info", name+" is back online", time.Minute)
	}
	if up && high >= 3 {
		e.Raise("cpu_high", name, "warning", fmt.Sprintf("%s CPU at %.0f%% for >30s", name, cpu), 30*time.Minute)
	}
	if up && temp >= 80 {
		e.Raise("temp_high", name, "warning", fmt.Sprintf("%s temperature %.0f°C", name, temp), 30*time.Minute)
	}
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
func (e *Engine) wanEvent(device, iface string, running bool) {
	now := time.Now()
	e.mu.Lock()
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
			e.Raise("wan_down", device, "warning", fmt.Sprintf("WAN link on %s went down (%s)", device, iface), time.Minute)
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
		e.mu.Lock()
		e.ifDown[key] = time.Now()
		e.mu.Unlock()
		e.Raise("iface_down", key, "warning", fmt.Sprintf("Interface %s on %s went down", iface, device), 5*time.Minute)
		return
	}
	e.mu.Lock()
	since, wasDown := e.ifDown[key]
	delete(e.ifDown, key)
	e.mu.Unlock()
	if wasDown { // recovery after a down is always reported, with the outage length
		e.Raise("iface_up", key, "info", fmt.Sprintf("Interface %s on %s is up again after %s", iface, device, fmtDur(time.Since(since))), 0)
		return
	}
	e.Raise("iface_up", key, "info", fmt.Sprintf("Interface %s on %s is up", iface, device), 5*time.Minute)
}

func (e *Engine) NewClient(mac, name, ip string) {
	if time.Since(e.start) < 3*time.Minute { // warm-up: initial inventory import
		return
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
	if cur > 20e6 && cur > 5*base {
		e.Raise("traffic_spike", "total", "warning", fmt.Sprintf("Traffic spike: %.1f Mbit/s (baseline %.1f)", cur/1e6, base/1e6), 30*time.Minute)
	}
	if m, err := e.St.PortScanSuspects(60, 200); err == nil {
		for _, mac := range m {
			e.Raise("port_scan", mac, "warning", "Client "+mac+" contacted ≥200 distinct ip:port pairs in 60 s (scan-like)", time.Hour)
		}
	}
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
