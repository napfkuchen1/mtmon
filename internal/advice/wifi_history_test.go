// SPDX-License-Identifier: AGPL-3.0-only

package advice

import (
	"strings"
	"testing"
)

// A client that is weak for most of the day is flagged even when the last poll was good, and a short dip of a
// client with history is not.
func TestWeakSignalUsesHistory(t *testing.T) {
	d := base()
	rec := t0.Unix()
	d.Cls = []Client{
		{MAC: "AA:00:00:00:00:01", Hostname: "tv", Device: "ap1", WiFi: true, Online: true, Signal: -60, LastSeen: rec, SigSamples: 500, WeakFrac: 0.8},     // weak all day, fine now
		{MAC: "AA:00:00:00:00:02", Hostname: "phone", Device: "ap1", WiFi: true, Online: true, Signal: -80, LastSeen: rec, SigSamples: 500, WeakFrac: 0.05}, // short dip
		{MAC: "AA:00:00:00:00:03", Hostname: "new", Device: "ap1", WiFi: true, Online: true, Signal: -82, LastSeen: rec},                                    // no history: last poll counts
	}
	out := expect(t, d, "weak-signal", 1)
	ev := strings.Join(out[0].Evidence, " ")
	if !strings.Contains(ev, "tv (-60 dBm now, weak in 80 % of the last 24 h)") || !strings.Contains(ev, "new (-82 dBm)") || strings.Contains(ev, "phone") {
		t.Errorf("evidence: %s", ev)
	}
	if !strings.Contains(out[0].Limits, "last 24 h") {
		t.Errorf("limits: %s", out[0].Limits)
	}
}
