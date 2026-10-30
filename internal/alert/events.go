package alert

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// Alerts derived from the router event log (RouterOS topics other than firewall). The message wording of RouterOS is
// matched defensively; a line that does not match simply raises nothing.
var (
	reLoginFail     = regexp.MustCompile(`(?i)login failure for user (\S+) from (\S+) via (\S+)`)
	reDHCPFail      = regexp.MustCompile(`(?i)without success|no free address|pool is empty|address pool.*(exhaust|empty)`)
	reWiFiDisc      = regexp.MustCompile(`(?i)([0-9a-f]{2}(?::[0-9a-f]{2}){5})(?:@([^\s,]+))?\S*\s+.*?disconnected(?:,?\s*(?:reason:?\s*)?(.*))?$`)
	reWGFail        = regexp.MustCompile(`(?i)handshake.*(fail|did not complete|timeout|timed out)`)
	reSessionScript = regexp.MustCompile(`executing script from (sshd|telnet|winbox|api)\b`) // the second log line of a failed typed command
	reSignalDBm     = regexp.MustCompile(`signal strength (-\d+)`)
	reCLICmd        = regexp.MustCompile(`\(\w+-cmd:([^@\s)]+)@([^)\s]+)\)`)
)

// burst records one occurrence of key and reports whether n or more happened within window.
func (e *Engine) burst(key string, window time.Duration, n int) bool {
	now := time.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.bursts == nil {
		e.bursts = map[string][]time.Time{}
	}
	keep := e.bursts[key][:0]
	for _, t := range e.bursts[key] {
		if now.Sub(t) < window {
			keep = append(keep, t)
		}
	}
	keep = append(keep, now)
	e.bursts[key] = keep
	if len(e.bursts) > 500 { // forget keys that went quiet
		for k, ts := range e.bursts {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) > window {
				delete(e.bursts, k)
			}
		}
	}
	return len(keep) >= n
}

func hasTopic(topics, t string) bool {
	for _, x := range strings.Split(topics, ",") {
		if x == t {
			return true
		}
	}
	return false
}

// LogEvent looks at one stored router log line and raises an alert for the patterns that matter.
func (e *Engine) LogEvent(ev store.LogEvent) {
	if time.Since(e.start) < time.Minute { // the router may replay its buffer when a rule is added
		return
	}
	dev, msg := ev.Device, ev.Msg
	switch {
	case hasTopic(ev.Topics, "critical") || (hasTopic(ev.Topics, "system") && (ev.Level == "error" || ev.Level == "critical")):
		sev := "warning"
		if ev.Level == "critical" || hasTopic(ev.Topics, "critical") {
			sev = "critical"
		}
		e.Raise("system_critical", dev, sev, fmt.Sprintf("%s logged an important event (%s): %s", dev, ev.Topics, msg), 10*time.Minute)
	case hasTopic(ev.Topics, "script") && (ev.Level == "error" || hasTopic(ev.Topics, "error")):
		if m := reCLICmd.FindStringSubmatch(msg); m != nil { // a command typed in a session, not a failing script
			why := strings.TrimSpace(strings.Replace(msg, m[0], "", 1))
			e.Raise("cli_error", dev+"/"+m[1], "info", fmt.Sprintf("Command from %s@%s failed on %s: %s", m[1], m[2], dev, why), time.Hour)
			break
		}
		if reSessionScript.MatchString(msg) { // already reported as cli_error from the line that carries the command
			break
		}
		e.Raise("script_error", dev, "warning", fmt.Sprintf("A script on %s failed: %s", dev, msg), time.Hour)
	}
	if m := reLoginFail.FindStringSubmatch(msg); m != nil && hasTopic(ev.Topics, "account") {
		if e.burst("login|"+dev+"|"+m[2], 10*time.Minute, 5) {
			e.Raise("login_failures", dev+"/"+m[2], "warning",
				fmt.Sprintf("Repeated failed logins on %s from %s (last user %q via %s, 5 or more in 10 min)", dev, m[2], m[1], m[3]), 30*time.Minute)
		}
	}
	if hasTopic(ev.Topics, "dhcp") && reDHCPFail.MatchString(msg) && !e.dhcpAfterRoam(ev.TS, msg) {
		if e.burst("dhcp|"+dev, 10*time.Minute, 3) {
			e.Raise("dhcp_problem", dev, "warning", fmt.Sprintf("DHCP on %s has trouble handing out addresses: %s", dev, msg), time.Hour)
		}
	}
	if (hasTopic(ev.Topics, "wireless") || hasTopic(ev.Topics, "wifi")) && !hasTopic(ev.Topics, "debug") {
		e.noteWiFiConnect(ev.TS, dev, msg)
		if m := reWiFiDisc.FindStringSubmatch(msg); m != nil {
			mac := strings.ToUpper(m[1])
			if e.St != nil {
				e.St.AddWifiDisc(ev.TS, mac, dev)
			}
			if e.burst("wifi|"+dev+"|"+mac, 5*time.Minute, 6) {
				who := mac
				if e.St != nil {
					if c, err := e.St.Client(mac); err == nil && c != nil {
						if n := firstNonEmpty(c.Label, c.Hostname); n != "" {
							who = n + " (" + mac + ")"
						}
					}
				}
				where := dev
				if m[2] != "" { // with CAPsMAN the log comes from the controller but names the AP interface
					where = dev + " (" + m[2] + ")"
				}
				msg := fmt.Sprintf("Client %s keeps disconnecting from %s (6 or more in 5 min)", who, where)
				if why := strings.TrimSpace(m[3]); why != "" {
					msg += " – last reason: " + why
					if dbm := reSignalDBm.FindStringSubmatch(why); dbm != nil {
						if v, err := strconv.Atoi(dbm[1]); err == nil && v >= -65 {
							msg += ". The signal is good, so this is probably not a coverage problem; check access-list reject rules for this client and roaming settings"
						}
					}
				}
				e.Raise("wifi_disconnects", dev+"/"+mac, "warning", msg, 30*time.Minute)
			}
		}
	}
	if hasTopic(ev.Topics, "wireguard") && reWGFail.MatchString(msg) {
		if e.burst("wg|"+dev, 10*time.Minute, 5) {
			e.Raise("wireguard_handshake", dev, "info", fmt.Sprintf("WireGuard handshakes on %s keep failing: %s", dev, msg), time.Hour)
		}
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
