// SPDX-License-Identifier: AGPL-3.0-only

package alert

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func storyAlerts(t *testing.T, e *Engine, kind string) []store.Alert {
	t.Helper()
	all, err := e.St.Alerts(100)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Alert
	for _, a := range all {
		if a.Kind == kind {
			out = append(out, a)
		}
	}
	return out
}

// #188: the real burst (27 disconnect/connect pairs and failed DHCP offers within 6 minutes) is one alert.
func TestDisconnectBurstIsOneStory(t *testing.T) {
	e, got := newEngine(t)
	const mac = "AA:BB:CC:00:00:01"
	base := time.Now().Add(-6 * time.Minute).Unix()
	var ts int64
	for i := 0; i < 27; i++ {
		ts = base + int64(i)*13
		e.LogEvent(store.LogEvent{TS: ts, Device: "ap", Topics: "wireless,info", Msg: mac + "@5G-Audience connected, signal strength -50"})
		e.LogEvent(store.LogEvent{TS: ts + 5, Device: "ap", Topics: "wireless,info", Msg: mac + "@5G-Audience disconnected, signal strength -50"})
	}
	for i := 1; i <= 3; i++ {
		e.LogEvent(store.LogEvent{TS: ts + 6 + int64(i), Device: "ap", Topics: "dhcp,info", Msg: "dhcp1 offering lease 10.0.0.5 for " + mac + " without success"})
	}
	as := storyAlerts(t, e, "wifi_disconnects")
	if len(as) != 1 || as[0].Count != 27 {
		t.Fatalf("want one story with 27 events, got %+v", as)
	}
	if n := len(storyAlerts(t, e, "wifi_roam_dhcp")) + len(storyAlerts(t, e, "dhcp_problem")); n != 0 {
		t.Fatalf("DHCP offers must fold into the story, got %d extra alerts", n)
	}
	if !strings.Contains(as[0].Msg, "3 failed DHCP offers") || !strings.Contains(as[0].Msg, "signal is good") {
		t.Fatalf("story text: %s", as[0].Msg)
	}
	opened := len(*got)
	e.closeStale(time.Unix(ts, 0).Add(10 * time.Minute))
	if len(storyAlerts(t, e, "wifi_disconnects")) != 1 || len(*got) != opened {
		t.Fatal("still inside the quiet time: nothing closes")
	}
	e.closeStale(time.Unix(ts, 0).Add(storyQuiet + time.Minute))
	as = storyAlerts(t, e, "wifi_disconnects")
	if !strings.HasPrefix(as[0].Msg, fmt.Sprintf("Client %s: 27 disconnects", mac)) || as[0].MsgKey != keyWifiDiscClosed {
		t.Fatalf("summary: %+v", as[0])
	}
	if len(*got) != opened+1 || (*got)[opened].Severity != "info" {
		t.Fatalf("one closing notification expected: %+v", (*got)[opened:])
	}
	e.closeStale(time.Unix(ts, 0).Add(time.Hour))
	if len(*got) != opened+1 {
		t.Fatal("a closed story must not close again")
	}
}

// A second burst after the quiet time is a new story.
func TestDisconnectNewStoryAfterQuiet(t *testing.T) {
	e, _ := newEngine(t)
	const mac = "AA:BB:CC:00:00:02"
	for i := 0; i < 6; i++ {
		e.LogEvent(store.LogEvent{TS: time.Now().Unix() + int64(i), Device: "ap", Topics: "wireless,info", Msg: mac + "@5G disconnected, signal strength -70"})
	}
	e.closeStale(time.Now().Add(storyQuiet + time.Minute))
	for i := 0; i < 6; i++ {
		e.LogEvent(store.LogEvent{TS: time.Now().Add(20 * time.Minute).Unix(), Device: "ap", Topics: "wireless,info", Msg: mac + "@5G disconnected, signal strength -70"})
	}
	if as := storyAlerts(t, e, "wifi_disconnects"); len(as) != 2 {
		t.Fatalf("want 2 stories, got %d", len(as))
	}
}
