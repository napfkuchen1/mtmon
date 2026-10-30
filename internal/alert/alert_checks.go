package alert

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/live"
	"github.com/napfkuchen1/mtmon/internal/store"
)

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
			e.closeStale(time.Now())
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
	key := "Traffic spike: {cur} Mbit/s (baseline {base})"
	params := map[string]string{"cur": fmt.Sprintf("%.1f", cur/1e6), "base": fmt.Sprintf("%.1f", base/1e6)}
	var msg string // notes after the base text
	sev, subject := "warning", "total"
	if len(snap.Clients) > 0 {
		top := snap.Clients[0]
		if e.ignored(top.Key) {
			return
		}
		r := (top.UpBps + top.DownBps) / 1e6
		key += " – mostly {who} ({rate} Mbit/s)"
		params["who"], params["rate"] = e.who(top.Key), fmt.Sprintf("%.1f", r)
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
	e.RaiseP("traffic_spike", subject, sev, key, params, msg, time.Hour)
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

// SignWebhook returns the X-Mtmon-Signature value for body ("sha256=<hex HMAC>"), or "" when no secret is set.
func SignWebhook(secret string, body []byte) string {
	if secret == "" {
		return ""
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func (e *Engine) deliver(a store.Alert) {
	text := fmt.Sprintf("[%s] %s", strings.ToUpper(a.Severity), a.Msg)
	if u := e.Cfg.Webhook; u != "" {
		b, _ := json.Marshal(a)
		req, _ := http.NewRequest("POST", u, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		if sig := SignWebhook(e.Cfg.WebhookSecret, b); sig != "" {
			req.Header.Set("X-Mtmon-Signature", sig)
		}
		if resp, err := e.hc.Do(req); err == nil {
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
