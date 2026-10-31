// SPDX-License-Identifier: AGPL-3.0-only

package alert

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Planned WAN reconnects (#136): a router scheduler entry that disables/enables the WAN interface (a common
// MikroTik habit against forced ISP disconnects) makes a wan_down every night. The scheduler list is part of the
// stored configuration snapshot, so a drop right after such a run is announced, not a fault.

const (
	plannedAfter  = 2 * time.Minute // a drop up to this long after a run belongs to it (poll delay, PPPoE teardown)
	plannedBefore = time.Minute     // clock difference between router and mtmon
)

var durPart = regexp.MustCompile(`(\d+)([wdhms])`)

// parseRouterDuration reads RouterOS durations: "1d", "1w2d", "30m", "1d00:00:00", "00:30:00".
func parseRouterDuration(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	var d time.Duration
	if i := strings.Index(s, ":"); i >= 0 {
		clock := s
		if k := strings.LastIndexAny(s[:i], "wd"); k >= 0 {
			d += parseRouterDuration(s[:k+1])
			clock = s[k+1:]
		}
		parts := strings.Split(clock, ":")
		if len(parts) == 3 {
			h, e1 := strconv.Atoi(parts[0])
			m, e2 := strconv.Atoi(parts[1])
			sec, e3 := strconv.Atoi(strings.SplitN(parts[2], ".", 2)[0])
			if e1 == nil && e2 == nil && e3 == nil {
				d += time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec)*time.Second
			}
		}
		return d
	}
	for _, m := range durPart.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(m[1])
		switch m[2] {
		case "w":
			d += time.Duration(n) * 7 * 24 * time.Hour
		case "d":
			d += time.Duration(n) * 24 * time.Hour
		case "h":
			d += time.Duration(n) * time.Hour
		case "m":
			d += time.Duration(n) * time.Minute
		case "s":
			d += time.Duration(n) * time.Second
		}
	}
	return d
}

var monthNames = map[string]time.Month{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}

// parseSchedulerStart combines start-date ("oct/28/2026" or "2026-10-28") and start-time ("03:10:00"). Without a
// usable date the start is today's date, which is right for any interval that divides a day.
func parseSchedulerStart(date, clock string, now time.Time) (time.Time, bool) {
	var h, m, s int
	parts := strings.Split(strings.TrimSpace(clock), ":")
	if len(parts) < 2 {
		return time.Time{}, false // "startup" or empty
	}
	var err error
	if h, err = strconv.Atoi(parts[0]); err != nil {
		return time.Time{}, false
	}
	if m, err = strconv.Atoi(parts[1]); err != nil {
		return time.Time{}, false
	}
	if len(parts) > 2 {
		s, _ = strconv.Atoi(strings.SplitN(parts[2], ".", 2)[0])
	}
	y, mo, d := now.Date()
	date = strings.ToLower(strings.TrimSpace(date))
	if p := strings.Split(date, "/"); len(p) == 3 {
		if mm, ok := monthNames[p[0]]; ok {
			dd, e1 := strconv.Atoi(p[1])
			yy, e2 := strconv.Atoi(p[2])
			if e1 == nil && e2 == nil {
				y, mo, d = yy, mm, dd
			}
		}
	} else if p := strings.Split(date, "-"); len(p) == 3 {
		yy, e1 := strconv.Atoi(p[0])
		mm, e2 := strconv.Atoi(p[1])
		dd, e3 := strconv.Atoi(p[2])
		if e1 == nil && e2 == nil && e3 == nil {
			y, mo, d = yy, time.Month(mm), dd
		}
	}
	return time.Date(y, mo, d, h, m, s, 0, now.Location()), true
}

// touchesWAN: the script switches the WAN interface (or some PPPoE client) off and on.
func touchesWAN(script, iface string) bool {
	s := strings.ToLower(script)
	if iface != "" && strings.Contains(s, strings.ToLower(iface)) {
		return true
	}
	if !strings.Contains(s, "disable") && !strings.Contains(s, "enable") && !strings.Contains(s, "set disabled") && !strings.Contains(s, "reset") {
		return false
	}
	return strings.Contains(s, "pppoe-client") || strings.Contains(s, "pppoe-out") || strings.Contains(s, "lte") || strings.Contains(s, "wan")
}

// minPlannedInterval: only schedulers that run rarely can announce an event. A job that runs every few minutes
// (a watchdog like WG_Check_Reload) would otherwise explain every outage as "planned".
const minPlannedInterval = time.Hour

// touchesIface: the script names this interface (disable/enable/reset of a port or tunnel).
func touchesIface(script, iface string) bool {
	return iface != "" && strings.Contains(strings.ToLower(script), strings.ToLower(iface))
}

// plannedReconnect returns the name of the scheduler entry on the device that explains a WAN drop of iface at now
// ("" = none known). It reads the newest stored configuration snapshot.
func (e *Engine) plannedReconnect(device, iface string, now time.Time) string {
	return e.plannedRun(device, now, plannedAfter, func(script string) bool { return touchesWAN(script, iface) })
}

// plannedIface: a scheduler entry that touches this (non-WAN) interface ran just before its link went down.
func (e *Engine) plannedIface(device, iface string, now time.Time) string {
	return e.plannedRun(device, now, plannedAfter, func(script string) bool { return touchesIface(script, iface) })
}

// plannedConfig: any rarely running scheduler entry ran within the last few minutes; the config check runs every
// few minutes, so the window is wider than for a link event.
func (e *Engine) plannedConfig(device string, now time.Time) string {
	return e.plannedRun(device, now, plannedConfigAfter, func(string) bool { return true })
}

const plannedConfigAfter = 10 * time.Minute

// plannedRun finds an enabled scheduler entry of the device whose script matches and whose last run (start-time
// plus a whole number of intervals, interval at least minPlannedInterval) lies within [now-after, now+plannedBefore].
func (e *Engine) plannedRun(device string, now time.Time, after time.Duration, match func(script string) bool) string {
	times := e.St.ConfigSnapTimes(device)
	if len(times) == 0 {
		return ""
	}
	snap, err := e.St.ConfigSnapAt(device, times[0])
	if err != nil {
		return ""
	}
	for _, r := range snap["/system/scheduler"] {
		if r["disabled"] == "true" || !match(r["on-event"]) {
			continue
		}
		iv := parseRouterDuration(r["interval"])
		if iv < minPlannedInterval {
			continue
		}
		start, ok := parseSchedulerStart(r["start-date"], r["start-time"], now)
		if !ok {
			continue
		}
		off := sinceLastRun(start, now, iv)
		if off <= after || iv-off <= plannedBefore {
			return r["name"]
		}
	}
	return ""
}

// sinceLastRun is the time since the last run of a scheduler entry that starts at start and repeats every iv. An
// interval of whole days counts calendar days, so a daylight-saving change between start-date and now does not
// shift the run by an hour.
func sinceLastRun(start, now time.Time, iv time.Duration) time.Duration {
	if iv%(24*time.Hour) == 0 {
		n := int(iv / (24 * time.Hour))
		y1, m1, d1 := start.Date()
		y2, m2, d2 := now.Date()
		days := int(time.Date(y2, m2, d2, 0, 0, 0, 0, time.UTC).Sub(time.Date(y1, m1, d1, 0, 0, 0, 0, time.UTC)) / (24 * time.Hour))
		k := days % n
		if k < 0 {
			k += n
		}
		h, mi, sec := start.Clock()
		last := time.Date(y2, m2, d2-k, h, mi, sec, 0, now.Location())
		if last.After(now) {
			last = time.Date(y2, m2, d2-k-n, h, mi, sec, 0, now.Location())
		}
		return now.Sub(last)
	}
	off := now.Sub(start) % iv
	if off < 0 {
		off += iv
	}
	return off
}
