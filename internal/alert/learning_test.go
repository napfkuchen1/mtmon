package alert

import (
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// While baselines are re-learned a spike is recorded as info and marked, not raised as a warning.
func TestSpikeDuringLearningIsInfo(t *testing.T) {
	e, got := newEngine(t)
	e.St.StartLearning(time.Hour, false, "test")
	e.St.DB.Exec(`INSERT INTO clients(mac,first_seen,last_seen) VALUES('m',?,?)`, time.Now().Unix(), time.Now().Unix()) // new client: a warning otherwise
	e.Hub.AddFlow(store.FlowRow{MAC: "m", Dir: "d", Bytes: 10_000})
	e.Checks()
	e.Hub.AddFlow(store.FlowRow{MAC: "m", Dir: "d", Bytes: 2 << 30})
	e.Checks()
	if len(*got) != 1 || (*got)[0].Severity != "info" || !strings.Contains((*got)[0].Msg, "learning") {
		t.Fatalf("spike during learning: %+v", *got)
	}
}

// "Reset baselines only" forgets the alert history for recurring patterns, so old spikes no longer make a new one "scheduled".
func TestBaselineResetForgetsRecurringHistory(t *testing.T) {
	e, got := newEngine(t)
	now := time.Now()
	for d := 1; d <= 3; d++ {
		e.St.AddAlert(store.Alert{TS: now.AddDate(0, 0, -d).Add(2 * time.Minute).Unix(), Kind: "wan_down", Subject: "gw", Severity: "warning", Msg: "x"})
	}
	e.St.StartLearning(time.Hour, true, "test")
	e.ResetBaseline()
	e.InterfaceState("gw", "pppoe-out1", false)
	if len(*got) != 1 || (*got)[0].Severity != "warning" || strings.Contains((*got)[0].Msg, "recurring") {
		t.Fatalf("history before the reset must not count: %+v", *got)
	}
}
