// SPDX-License-Identifier: AGPL-3.0-only

package alert

import (
	"strings"
	"time"
)

// targetFails is the number of failed checks in a row after which an own internet target is reported (#194).
const targetFails = 3

// TargetResult takes one check of an own internet target (a probe key such as "dns:example.com" or
// "http4:https://host/"). Three failures in a row raise one warning for that target; the first answer afterwards
// raises an info. This is a separate kind: a single unreachable site is not a WAN outage.
func (e *Engine) TargetResult(target string, ok bool, why string) {
	e.mu.Lock()
	if e.tgtFails == nil {
		e.tgtFails = map[string]int{}
	}
	if e.tgtNX == nil {
		e.tgtNX = map[string]bool{}
	}
	n := e.tgtFails[target]
	nx := e.tgtNX[target]
	switch {
	case !ok:
		e.tgtFails[target] = n + 1
		n++
		nx = why == "NXDOMAIN"
		e.tgtNX[target] = nx
	default:
		delete(e.tgtFails, target)
		delete(e.tgtNX, target)
	}
	e.mu.Unlock()
	name, subject, cooldown := targetLabel(target), target, time.Duration(0)
	if shared := sharedSubject(target); nx && shared != "" {
		// a name that does not exist fails IPv4 and IPv6 alike: one alert for the target, raised by the IPv4 check
		if !ok && strings.HasPrefix(target, "http6:") {
			return
		}
		name, subject, cooldown = strings.TrimPrefix(shared, "http:"), shared, time.Minute
	}
	if !ok && n == targetFails {
		e.RaiseP("target_down", subject, "warning", "Internet target {t} did not answer 3 checks in a row", map[string]string{"t": name}, " – "+why, cooldown)
	}
	if ok && n >= targetFails {
		e.RaiseP("target_up", subject, "info", "Internet target {t} answers again", map[string]string{"t": name}, "", cooldown)
	}
}

// sharedSubject is the alert subject of an HTTP target without the address family ("http:https://host/"), or "" for
// other probe keys.
func sharedSubject(k string) string {
	for _, p := range []string{"http4:", "http6:"} {
		if strings.HasPrefix(k, p) {
			return "http:" + strings.TrimPrefix(k, p)
		}
	}
	return ""
}

// TargetRemoved closes what is open for a target the operator took out of the list: the failure counter starts over
// and an open target_down warning is resolved (shown as info, "target removed"), so it no longer counts as open.
func (e *Engine) TargetRemoved(target string) {
	e.mu.Lock()
	delete(e.tgtFails, target)
	delete(e.tgtNX, target)
	e.mu.Unlock()
	e.St.ResolveAlerts("target_down", target, " – target removed")
	if shared := sharedSubject(target); shared != "" {
		e.St.ResolveAlerts("target_down", shared, " – target removed")
	}
}

// targetLabel is the target as the operator typed it: "http6:https://host/" shows as "https://host/ (IPv6)".
func targetLabel(k string) string {
	switch {
	case strings.HasPrefix(k, "http4:"):
		return strings.TrimPrefix(k, "http4:") + " (IPv4)"
	case strings.HasPrefix(k, "http6:"):
		return strings.TrimPrefix(k, "http6:") + " (IPv6)"
	}
	return strings.TrimPrefix(k, "dns:")
}
