// SPDX-License-Identifier: AGPL-3.0-only

package alert

import (
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func discBurst(e *Engine, mac string) {
	base := time.Now().Add(-6 * time.Minute).Unix()
	for i := 0; i < 27; i++ {
		ts := base + int64(i)*13
		e.LogEvent(store.LogEvent{TS: ts, Device: "capsman", Topics: "wifi,info", Msg: mac + "@cap-wifi2 disconnected, connection lost, signal strength -52"})
		e.LogEvent(store.LogEvent{TS: ts + 2, Device: "capsman", Topics: "wifi,info", Msg: mac + "@cap-wifi2 connected, signal strength -47"})
	}
}

// #211: a reject rule for the client whose match-count rose during the burst (0 -> 13) is named as a reject loop.
func TestRejectLoopHint(t *testing.T) {
	e, _ := newEngine(t)
	const mac = "5C:AA:BB:CC:DD:EE"
	calls := 0
	e.AccessList = func(dev string) ([]map[string]string, error) {
		calls++
		rule := map[string]string{".id": "*1", "mac-address": strings.ToLower(mac), "action": "reject", "interface": "cap-wifi1", "comment": "pin phone"}
		if calls > 1 { // RouterOS leaves match-count out while it is 0
			rule["match-count"] = "13"
		}
		return []map[string]string{rule, {".id": "*2", "mac-address": "11:22:33:44:55:66", "action": "reject", "match-count": "99"}}, nil
	}
	discBurst(e, mac)
	as := storyAlerts(t, e, "wifi_disconnects")
	if len(as) != 1 || as[0].Subject != "cap-wifi2/"+mac {
		t.Fatalf("subject must be AP/client: %+v", as)
	}
	if !strings.Contains(as[0].Msg, "rejected on cap-wifi1 by access-list rule 'pin phone'") || !strings.Contains(as[0].Msg, "signal-range") {
		t.Fatalf("hint missing: %s", as[0].Msg)
	}
}

// No rule, an allow rule or a counter that did not move: no hint.
func TestRejectLoopNoHint(t *testing.T) {
	for name, rows := range map[string][]map[string]string{
		"none":      nil,
		"allow":     {{".id": "*1", "mac-address": "5C:AA:BB:CC:DD:EE", "action": "accept", "match-count": "5"}},
		"disabled":  {{".id": "*1", "mac-address": "5C:AA:BB:CC:DD:EE", "action": "reject", "disabled": "true", "match-count": "5"}},
		"other mac": {{".id": "*1", "mac-address": "5C:AA:BB:CC:DD:EF", "action": "reject", "match-count": "5"}},
		"unchanged": {{".id": "*1", "mac-address": "5C:AA:BB:CC:DD:EE", "action": "reject", "match-count": "13"}},
	} {
		e, _ := newEngine(t)
		e.AccessList = func(string) ([]map[string]string, error) { return rows, nil }
		discBurst(e, "5C:AA:BB:CC:DD:EE")
		as := storyAlerts(t, e, "wifi_disconnects")
		if len(as) != 1 || strings.Contains(as[0].Msg, "rejected on") {
			t.Errorf("%s: %+v", name, as)
		}
	}
}

// A counter reset (rule edit, reboot) is not a hit, a mask covers a range.
func TestRejectMacMaskAndReset(t *testing.T) {
	if !macMatches("5C:AA:BB:00:00:00/FF:FF:FF:00:00:00", "5C:AA:BB:CC:DD:EE") || macMatches("5C:AA:BC:00:00:00/FF:FF:FF:00:00:00", "5C:AA:BB:CC:DD:EE") {
		t.Fatal("mask")
	}
	e, _ := newEngine(t)
	n := 50
	e.AccessList = func(string) ([]map[string]string, error) {
		n -= 20
		return []map[string]string{{".id": "*1", "mac-address": "5C:AA:BB:CC:DD:EE", "action": "reject", "match-count": itoa(n)}}, nil
	}
	now := time.Now()
	e.rejectLoop("d", "5C:AA:BB:CC:DD:EE", now)
	if _, ok := e.rejectLoop("d", "5C:AA:BB:CC:DD:EE", now.Add(time.Minute)); ok {
		t.Fatal("falling counter is a reset")
	}
}
