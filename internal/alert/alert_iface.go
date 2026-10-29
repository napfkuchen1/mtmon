package alert

import (
	"fmt"
	"strings"
	"time"
)

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
