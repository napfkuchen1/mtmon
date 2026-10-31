// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"sort"
	"sync"
	"time"
)

// Debug log lines of chatty topics (dhcp packet dumps, WireGuard keepalives) are not stored (#236), but counted per
// device and hour so a suggestion can tell the user that the router sends them.
var debugNoise = struct {
	sync.Mutex
	hour   map[string]int64          // device -> hour the counters belong to
	topics map[string]map[string]int // device -> topic -> lines in that hour
	last   map[string]map[string]int // device -> topic -> lines in the previous hour
}{hour: map[string]int64{}, topics: map[string]map[string]int{}, last: map[string]map[string]int{}}

// DebugTopics are the topics whose debug lines are dropped on ingest.
var DebugTopics = map[string]bool{"dhcp": true, "wireguard": true, "wireless": true, "wifi": true}

// OtherDebug is the counter name of debug-only lines outside DebugTopics (a script that logs "ok" every 5 minutes).
// They are dropped and counted, but the suggestion cannot exclude them on the router (it would hide "script,error,debug").
const OtherDebug = "other"

// NoisyDebug reports whether a log line carries nothing but debug (and not e.g. "script,error,debug", which
// cli_error needs): the chatty topic it belongs to, or OtherDebug.
func NoisyDebug(level string, has func(string) bool) (string, bool) {
	if level != "debug" || has("error") || has("critical") || has("warning") {
		return "", false
	}
	for t := range DebugTopics {
		if has(t) {
			return t, true
		}
	}
	return OtherDebug, true
}

// NoteDebugLine counts one dropped debug line of `topic` from `device`.
func (s *Store) NoteDebugLine(device, topic string, now time.Time) {
	h := now.Unix() / 3600
	debugNoise.Lock()
	defer debugNoise.Unlock()
	if debugNoise.hour[device] != h {
		if debugNoise.hour[device] == h-1 {
			debugNoise.last[device] = debugNoise.topics[device]
		} else {
			debugNoise.last[device] = nil
		}
		debugNoise.topics[device] = nil
		debugNoise.hour[device] = h
	}
	if debugNoise.topics[device] == nil {
		debugNoise.topics[device] = map[string]int{}
	}
	debugNoise.topics[device][topic]++
}

// DebugLineRate returns the debug lines per hour that `device` sent (the larger of the running and the previous
// hour) and the topics that make them up, busiest first.
func (s *Store) DebugLineRate(device string, now time.Time) (int, []string) {
	h := now.Unix() / 3600
	debugNoise.Lock()
	defer debugNoise.Unlock()
	cur, prev := debugNoise.topics[device], debugNoise.last[device]
	switch debugNoise.hour[device] {
	case h:
	case h - 1:
		cur, prev = nil, debugNoise.topics[device]
	default:
		return 0, nil
	}
	sum := func(m map[string]int) (n int) {
		for t, v := range m {
			if t != OtherDebug {
				n += v
			}
		}
		return
	}
	src := cur
	if sum(prev) > sum(cur) {
		src = prev
	}
	var ts []string
	for t := range src {
		if t != OtherDebug {
			ts = append(ts, t)
		}
	}
	sort.Slice(ts, func(i, j int) bool {
		if src[ts[i]] != src[ts[j]] {
			return src[ts[i]] > src[ts[j]]
		}
		return ts[i] < ts[j]
	})
	return sum(src), ts
}

// StoreDebugLogs reports the setting "Store debug log lines" (default off).
func (s *Store) StoreDebugLogs() bool { return s.GetMeta("store_debug_logs") == "1" }
