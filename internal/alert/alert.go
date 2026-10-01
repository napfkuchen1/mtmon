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

	"github.com/daniel/mtmon/internal/config"
	"github.com/daniel/mtmon/internal/live"
	"github.com/daniel/mtmon/internal/store"
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
	cpuHigh  map[string]int
	baseline float64
	hc       *http.Client
	Notify   func(store.Alert) // test hook
}

func New(c *config.Config, s *store.Store, h *live.Hub, log *slog.Logger) *Engine {
	return &Engine{Cfg: c, St: s, Hub: h, Log: log, start: time.Now(), last: map[string]time.Time{},
		devUp: map[string]bool{}, cpuHigh: map[string]int{}, hc: &http.Client{Timeout: 8 * time.Second}}
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

var dynamicPrefixes = []string{"pppoe-", "l2tp-", "sstp-", "ovpn-", "<", "veth", "lo", "cap", "wifi-cap", "wg-peer"}

func (e *Engine) InterfaceState(device, iface string, running bool) {
	for _, p := range dynamicPrefixes {
		if strings.HasPrefix(iface, p) {
			return
		}
	}
	if !running {
		e.Raise("iface_down", device+"/"+iface, "warning", fmt.Sprintf("Interface %s on %s went down", iface, device), 5*time.Minute)
	} else {
		e.Raise("iface_up", device+"/"+iface, "info", fmt.Sprintf("Interface %s on %s is up", iface, device), 5*time.Minute)
	}
}

func (e *Engine) NewClient(mac, name, ip string) {
	if time.Since(e.start) < 3*time.Minute { // warm-up: initial inventory import
		return
	}
	n := name
	if n == "" {
		n = ip
	}
	e.Raise("new_client", mac, "info", fmt.Sprintf("New client %s (%s) %s", mac, n, ip), time.Hour)
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
