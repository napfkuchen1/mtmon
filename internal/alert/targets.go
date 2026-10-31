// SPDX-License-Identifier: AGPL-3.0-only

package alert

import "strings"

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
	n := e.tgtFails[target]
	switch {
	case !ok:
		e.tgtFails[target] = n + 1
		n++
	default:
		delete(e.tgtFails, target)
	}
	e.mu.Unlock()
	name := targetLabel(target)
	if !ok && n == targetFails {
		e.RaiseP("target_down", target, "warning", "Internet target {t} did not answer 3 checks in a row", map[string]string{"t": name}, " – "+why, 0)
	}
	if ok && n >= targetFails {
		e.RaiseP("target_up", target, "info", "Internet target {t} answers again", map[string]string{"t": name}, "", 0)
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
