package store

import (
	"testing"
	"time"
)

// Flows of mtmon's own speed test are stored (visible per service) but do not become "new destinations" of the
// mtmon host.
func TestSpeedtestFlowsDoNotCreateNewDestinations(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().Unix()
	st.Enqueue(
		FlowRow{TS: now, MAC: "AA:00:00:00:00:01", CIP: "192.168.1.5", RIP: "198.51.100.7", Dir: "u", Bytes: 1000, Svc: SpeedtestSvc},
		FlowRow{TS: now, MAC: "AA:00:00:00:00:01", CIP: "192.168.1.5", RIP: "203.0.113.9", Dir: "u", Bytes: 1000, Svc: "HTTPS"},
	)
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	got, err := st.NewDests("AA:00:00:00:00:01", 3600)
	if err != nil || len(got) != 1 || got[0] != "203.0.113.9" {
		t.Fatalf("new destinations: %v %v", got, err)
	}
}
