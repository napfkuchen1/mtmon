package api

import (
	"encoding/json"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// Snapshots stored before a field was recognised as a runtime value must not show it as a change (#133).
func TestConfigHistoryRefiltersStoredSnapshots(t *testing.T) {
	ts, s := newTestServer(t)
	s.Cfg.Devices = []config.Device{{Name: "gw", Role: "router", Addr: "10.0.0.1"}}
	a := store.ConfigSnap{"/system/scheduler": {{"name": "n", "interval": "1d", "next-run": "a", "run-count": "1"}}}
	b := store.ConfigSnap{"/system/scheduler": {{"name": "n", "interval": "1d", "next-run": "b", "run-count": "2"}}}
	s.St.SaveConfigSnap("gw", 100, a)
	s.St.SaveConfigSnap("gw", 200, b)
	_, c := login(t, ts, "test-password-1")
	resp, err := c.Get(ts.URL + "/api/devices/gw/config")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Changes []store.ConfigChange `json:"changes"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != 200 || len(out.Changes) != 0 {
		t.Fatalf("status %d, runtime-only differences must not be changes: %+v", resp.StatusCode, out.Changes)
	}
}
