package store

import (
	"testing"
	"time"
)

func TestAlertNoise(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now().Unix()
	add := func(kind, sev string, acked bool, ts int64) {
		id, _ := st.AddAlert(Alert{TS: ts, Kind: kind, Subject: "x", Severity: sev})
		if acked {
			st.AckAlert(id)
		}
	}
	for i := 0; i < 5; i++ {
		add("script_error", "warning", true, now)
	}
	add("script_error", "warning", false, now)
	add("script_error", "info", true, now) // info is not a warning
	add("device_offline", "warning", false, now)
	add("old", "warning", true, now-40*86400) // outside the window
	got := st.AlertNoise(now - 7*86400)
	if len(got) != 2 {
		t.Fatalf("want 2 kinds, got %+v", got)
	}
	if got[0].Kind != "script_error" || got[0].Warnings != 6 || got[0].Acked != 5 || !got[0].Noisy {
		t.Fatalf("script_error: %+v", got[0])
	}
	if got[1].Kind != "device_offline" || got[1].Noisy {
		t.Fatalf("device_offline: %+v", got[1])
	}
}
