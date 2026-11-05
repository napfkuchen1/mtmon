// SPDX-License-Identifier: AGPL-3.0-only

package alert

import (
	"fmt"
	"strings"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// sfpHyst is how far above the warning limit the receive power must climb before the alert resolves, so a value
// that wobbles around the limit does not open and close the alert every poll.
const sfpHyst = 0.5 // dB

// Hardware is called after every hardware poll of a device (poller, #300). It raises one alert when a PoE port
// reports short-circuit or overload, and one when an SFP receive power falls below the configured limit; each
// resolves when the reading is fine again. A port or feature that is not read this time (transient error) keeps
// its alert open; one that is gone, switched off or no longer permitted is resolved.
func (e *Engine) Hardware(device string, h store.HWDevice) {
	limit, haveLimit := e.St.SfpWarnDBm()
	seen := map[string]bool{}

	for _, p := range h.PoE {
		subj := device + "/" + p.Iface
		hk := "poe_fault|" + subj
		seen[hk] = true
		if p.Fault {
			if e.hwOpenSet(hk, true) {
				msgKey := "PoE overload on {d} port {i}"
				if p.Status == "short-circuit" {
					msgKey = "PoE short circuit on {d} port {i}"
				}
				e.RaiseP("poe_fault", subj, "warning", msgKey, map[string]string{"d": device, "i": p.Iface}, "", 0)
			}
		} else if e.hwOpenSet(hk, false) {
			e.St.ResolveAlerts("poe_fault", subj, " – resolved: the PoE port is fine again")
		}
	}

	for _, s := range h.SFP {
		subj := device + "/" + s.Iface
		hk := "sfp_rx_low|" + subj
		seen[hk] = true
		if s.RxDBm == nil {
			continue
		}
		rx := *s.RxDBm
		switch {
		case !haveLimit:
			if e.hwOpenSet(hk, false) {
				e.St.ResolveAlerts("sfp_rx_low", subj, " – resolved: the limit was removed")
			}
		case rx < limit:
			if e.hwOpenSet(hk, true) {
				e.RaiseP("sfp_rx_low", subj, "warning", "{d} {i}: SFP receive power {p} dBm is below the limit of {l} dBm",
					map[string]string{"d": device, "i": s.Iface, "p": fmtDBm(rx), "l": fmtDBm(limit)}, "", 0)
			}
		case rx >= limit+sfpHyst:
			if e.hwOpenSet(hk, false) {
				e.St.ResolveAlerts("sfp_rx_low", subj, " – resolved: the SFP receive power is above the limit again")
			}
		}
	}

	// Ports that vanished: resolve when the feature is gone or off; keep the alert on a transient gap.
	e.mu.Lock()
	var gone []string
	for k := range e.hwOpen {
		if seen[k] || !strings.Contains(k, "|"+device+"/") {
			continue
		}
		kind := k[:strings.Index(k, "|")]
		state := h.Caps.PoE
		if kind == "sfp_rx_low" {
			state = h.Caps.SFP
		}
		if state == store.HWOff || state == store.HWNone {
			gone = append(gone, k)
		}
	}
	for _, k := range gone {
		delete(e.hwOpen, k)
	}
	e.mu.Unlock()
	for _, k := range gone {
		i := strings.Index(k, "|")
		e.St.ResolveAlerts(k[:i], k[i+1:], " – resolved: the port is no longer monitored")
	}
}

// hwOpenSet records whether the alert for key is open and reports whether that was a change.
func (e *Engine) hwOpenSet(key string, open bool) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.hwOpen == nil {
		e.hwOpen = map[string]bool{}
	}
	if e.hwOpen[key] == open {
		return false
	}
	if open {
		e.hwOpen[key] = true
	} else {
		delete(e.hwOpen, key)
	}
	return true
}

func fmtDBm(v float64) string { return fmt.Sprintf("%.1f", v) }
