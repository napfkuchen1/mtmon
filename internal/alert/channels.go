// SPDX-License-Identifier: AGPL-3.0-only

package alert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// Notification channels (#193): webhook, ntfy and e-mail. The settings come from the config file; once they are
// saved in the UI they live in meta "notify" and win. Each channel has its own quiet hours: in that window warnings
// and info are held and sent as one summary when it ends, critical alerts always go out at once.

// Quiet is a daily window in local time. Days are the weekdays (0 = Sunday) on which the window starts; empty = every day.
type Quiet struct {
	Enabled bool   `json:"enabled"`
	From    string `json:"from"` // HH:MM
	To      string `json:"to"`
	Days    []int  `json:"days,omitempty"`
}

type WebhookCh struct {
	URL    string `json:"url"`
	Secret string `json:"secret,omitempty"`
	Quiet  Quiet  `json:"quiet"`
	MinSev string `json:"min_sev,omitempty"`
}

type NtfyCh struct {
	URL    string `json:"url"`
	Quiet  Quiet  `json:"quiet"`
	MinSev string `json:"min_sev,omitempty"`
}

type SMTPCh struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	User   string `json:"user"`
	Pass   string `json:"pass,omitempty"`
	From   string `json:"from"`
	To     string `json:"to"`
	Quiet  Quiet  `json:"quiet"`
	MinSev string `json:"min_sev,omitempty"`
}

// Channels are the settings of all notification channels.
type Channels struct {
	Webhook WebhookCh `json:"webhook"`
	Ntfy    NtfyCh    `json:"ntfy"`
	SMTP    SMTPCh    `json:"smtp"`
}

// ChannelNames are the keys of the channels (test button, held alerts).
var ChannelNames = []string{"webhook", "ntfy", "smtp"}

func clock(s string) (int, bool) {
	h, m, ok := strings.Cut(s, ":")
	hh, e1 := strconv.Atoi(h)
	mm, e2 := strconv.Atoi(m)
	if !ok || e1 != nil || e2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// Validate checks a quiet-hours window.
func (q Quiet) Validate() error {
	if !q.Enabled {
		return nil
	}
	f, ok1 := clock(q.From)
	t, ok2 := clock(q.To)
	if !ok1 || !ok2 {
		return fmt.Errorf("quiet hours need times as HH:MM")
	}
	if f == t {
		return fmt.Errorf("quiet hours start and end must differ")
	}
	for _, d := range q.Days {
		if d < 0 || d > 6 {
			return fmt.Errorf("quiet hours: weekday out of range")
		}
	}
	return nil
}

func (q Quiet) hasDay(d time.Weekday) bool {
	if len(q.Days) == 0 {
		return true
	}
	for _, x := range q.Days {
		if x == int(d) {
			return true
		}
	}
	return false
}

// Active reports whether t (in its own zone) lies inside the window. A window that ends before it starts runs
// over midnight and belongs to the weekday it started on.
func (q Quiet) Active(t time.Time) bool {
	if !q.Enabled {
		return false
	}
	f, ok1 := clock(q.From)
	to, ok2 := clock(q.To)
	if !ok1 || !ok2 || f == to {
		return false
	}
	m := t.Hour()*60 + t.Minute()
	if f < to {
		return q.hasDay(t.Weekday()) && m >= f && m < to
	}
	return (m >= f && q.hasDay(t.Weekday())) || (m < to && q.hasDay((t.Weekday()+6)%7))
}

// channels returns the effective settings: the config file, replaced as a whole by what the UI saved.
func (e *Engine) channels() Channels {
	c := Channels{Webhook: WebhookCh{URL: e.Cfg.Webhook, Secret: e.Cfg.WebhookSecret}, Ntfy: NtfyCh{URL: e.Cfg.NtfyURL}}
	if s := e.Cfg.SMTP; s != nil {
		c.SMTP = SMTPCh{Host: s.Host, Port: s.Port, User: s.User, Pass: s.Pass, From: s.From, To: s.To}
	}
	if v := e.St.GetMeta("notify"); v != "" {
		var m Channels
		if json.Unmarshal([]byte(v), &m) == nil {
			return m
		}
	}
	return c
}

// SaveChannels stores the settings from the UI; an empty secret or password keeps the stored one.
func (e *Engine) SaveChannels(in Channels) error {
	for _, q := range []Quiet{in.Webhook.Quiet, in.Ntfy.Quiet, in.SMTP.Quiet} {
		if err := q.Validate(); err != nil {
			return err
		}
	}
	for _, m := range []string{in.Webhook.MinSev, in.Ntfy.MinSev, in.SMTP.MinSev} {
		if m != "" && m != "info" && m != "warning" && m != "critical" {
			return fmt.Errorf("minimum severity must be info, warning or critical")
		}
	}
	for _, u := range []string{in.Webhook.URL, in.Ntfy.URL} {
		if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return fmt.Errorf("URL must start with http:// or https://")
		}
	}
	old := e.channels()
	if in.Webhook.Secret == "" {
		in.Webhook.Secret = old.Webhook.Secret
	}
	if in.SMTP.Pass == "" {
		in.SMTP.Pass = old.SMTP.Pass
	}
	b, _ := json.Marshal(in)
	return e.St.SetMeta("notify", string(b))
}

// Channels returns the effective settings for the UI.
func (e *Engine) Channels() Channels { return e.channels() }

func (c Channels) enabled(name string) bool {
	switch name {
	case "webhook":
		return c.Webhook.URL != ""
	case "ntfy":
		return c.Ntfy.URL != ""
	case "smtp":
		return c.SMTP.Host != "" && c.SMTP.To != ""
	}
	return false
}

// minSev is the lowest severity a channel gets (default info = all).
func (c Channels) minSev(name string) string {
	switch name {
	case "webhook":
		return c.Webhook.MinSev
	case "ntfy":
		return c.Ntfy.MinSev
	}
	return c.SMTP.MinSev
}

func (c Channels) quiet(name string) Quiet {
	switch name {
	case "webhook":
		return c.Webhook.Quiet
	case "ntfy":
		return c.Ntfy.Quiet
	}
	return c.SMTP.Quiet
}

// heldAlert is what a summary needs of an alert that was held back.
type heldAlert struct {
	TS       int64  `json:"ts"`
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Msg      string `json:"msg"`
}

const maxHeld = 100

func (e *Engine) loadHeld() map[string][]heldAlert {
	m := map[string][]heldAlert{}
	if v := e.St.GetMeta("notify_held"); v != "" {
		json.Unmarshal([]byte(v), &m)
	}
	return m
}

func (e *Engine) saveHeld(m map[string][]heldAlert) {
	for k, v := range m {
		if len(v) == 0 {
			delete(m, k)
		}
	}
	b, _ := json.Marshal(m)
	e.St.SetMeta("notify_held", string(b))
}

// deliver sends an alert to every channel that is not in quiet hours; the others hold it (critical is never held).
func (e *Engine) deliver(a store.Alert) { e.deliverAt(a, time.Now()) }

func (e *Engine) deliverAt(a store.Alert, now time.Time) {
	c := e.channels()
	for _, name := range ChannelNames {
		if !c.enabled(name) {
			continue
		}
		if sevRank[a.Severity] < sevRank[c.minSev(name)] {
			continue // this channel only wants more serious alerts
		}
		if a.Severity != "critical" && c.quiet(name).Active(now) {
			e.nmu.Lock()
			m := e.loadHeld()
			m[name] = append(m[name], heldAlert{a.TS, a.Kind, a.Severity, a.Msg})
			if len(m[name]) > maxHeld {
				m[name] = m[name][len(m[name])-maxHeld:]
			}
			e.saveHeld(m)
			e.nmu.Unlock()
			continue
		}
		if err := e.send(c, name, a); err != nil {
			e.Log.Warn("notification failed", "channel", name, "err", err)
		}
	}
}

// flushHeld sends one summary per channel whose quiet hours are over.
func (e *Engine) flushHeld(now time.Time) {
	e.nmu.Lock()
	m := e.loadHeld()
	e.nmu.Unlock()
	if len(m) == 0 {
		return
	}
	c := e.channels()
	for name, list := range m {
		if len(list) == 0 || c.quiet(name).Active(now) {
			continue
		}
		if c.enabled(name) {
			if err := e.sendSummary(c, name, list); err != nil {
				e.Log.Warn("notification summary failed", "channel", name, "err", err)
				continue
			}
		}
		e.nmu.Lock()
		cur := e.loadHeld()
		delete(cur, name)
		e.saveHeld(cur)
		e.nmu.Unlock()
	}
}

// maxHeldListed caps the lines of one summary so a long night cannot make an oversized message.
const maxHeldListed = 200

func summaryText(list []heldAlert) (title, body string) {
	title = fmt.Sprintf("%d alerts held during quiet hours", len(list))
	if len(list) == 1 {
		title = "1 alert held during quiet hours"
	}
	var b strings.Builder
	shown := list
	if len(shown) > maxHeldListed {
		shown = shown[len(shown)-maxHeldListed:] // newest ones; held alerts are appended in time order
	}
	if older := len(list) - len(shown); older > 0 {
		fmt.Fprintf(&b, "+%d older not listed\n", older)
	}
	for _, h := range shown {
		fmt.Fprintf(&b, "%s [%s] %s\n", time.Unix(h.TS, 0).Format("15:04"), strings.ToUpper(h.Severity), h.Msg)
	}
	return title, strings.TrimRight(b.String(), "\n")
}

func (e *Engine) sendSummary(c Channels, name string, list []heldAlert) error {
	title, body := summaryText(list)
	switch name {
	case "webhook":
		b, _ := json.Marshal(map[string]any{"kind": "quiet_summary", "severity": "info", "msg": title, "count": len(list), "alerts": list})
		return e.postWebhook(c.Webhook, b)
	case "ntfy":
		return e.postNtfy(c.Ntfy, "mtmon: "+title, body, "")
	}
	return e.mail(c.SMTP, "mtmon: "+title, body)
}

func (e *Engine) send(c Channels, name string, a store.Alert) error {
	switch name {
	case "webhook":
		b, _ := json.Marshal(a)
		return e.postWebhook(c.Webhook, b)
	case "ntfy":
		return e.postNtfy(c.Ntfy, "mtmon: "+a.Kind, a.Msg, a.Severity)
	}
	return e.mail(c.SMTP, "mtmon: "+a.Kind, fmt.Sprintf("[%s] %s", strings.ToUpper(a.Severity), a.Msg))
}

// TestChannel sends a test message through one channel right now (quiet hours do not apply).
func (e *Engine) TestChannel(name string) error {
	c := e.channels()
	if !c.enabled(name) {
		return fmt.Errorf("channel is not set up")
	}
	return e.send(c, name, store.Alert{TS: time.Now().Unix(), Kind: "test", Severity: "info", Msg: "Test message from mtmon"})
}

func (e *Engine) postWebhook(w WebhookCh, body []byte) error {
	req, err := http.NewRequest("POST", w.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if sig := SignWebhook(w.Secret, body); sig != "" {
		req.Header.Set("X-Mtmon-Signature", sig)
	}
	return e.do(req)
}

func (e *Engine) postNtfy(n NtfyCh, title, body, severity string) error {
	req, err := http.NewRequest("POST", n.URL, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", title)
	if p := map[string]string{"critical": "urgent", "warning": "high"}[severity]; p != "" {
		req.Header.Set("Priority", p)
	}
	return e.do(req)
}

func (e *Engine) do(req *http.Request) error {
	resp, err := e.hc.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (e *Engine) mail(s SMTPCh, subject, text string) error {
	port := s.Port
	if port == 0 {
		port = 587
	}
	var auth smtp.Auth
	if s.User != "" {
		auth = smtp.PlainAuth("", s.User, s.Pass, s.Host)
	}
	msg := "From: " + s.From + "\r\nTo: " + s.To + "\r\nSubject: " + subject + "\r\n\r\n" + strings.ReplaceAll(text, "\n", "\r\n") + "\r\n"
	return smtp.SendMail(fmt.Sprintf("%s:%d", s.Host, port), auth, s.From, strings.Split(s.To, ","), []byte(msg))
}
