package store

import (
	"testing"
	"time"
)

func TestNoisyDebug(t *testing.T) {
	has := func(l ...string) func(string) bool {
		return func(x string) bool {
			for _, y := range l {
				if y == x {
					return true
				}
			}
			return false
		}
	}
	if tp, ok := NoisyDebug("debug", has("dhcp", "debug", "packet")); !ok || tp != "dhcp" {
		t.Error("dhcp packet dump must be dropped")
	}
	if _, ok := NoisyDebug("debug", has("wireguard", "debug")); !ok {
		t.Error("wireguard keepalive must be dropped")
	}
	if _, ok := NoisyDebug("error", has("script", "error", "debug")); ok {
		t.Error("script,error,debug must stay (cli_error)")
	}
	if tp, ok := NoisyDebug("debug", has("script", "debug")); !ok || tp != OtherDebug {
		t.Error("script,debug must be dropped too (#236)")
	}
	if _, ok := NoisyDebug("info", has("dhcp", "info")); ok {
		t.Error("normal dhcp line must stay")
	}
}

func TestDebugLineRate(t *testing.T) {
	st, _ := Open(t.TempDir())
	defer st.Close()
	now := time.Unix(1_800_000_000, 0)
	for i := 0; i < 150; i++ {
		st.NoteDebugLine("gw", "dhcp", now)
	}
	for i := 0; i < 20; i++ {
		st.NoteDebugLine("gw", "wireguard", now)
	}
	n, ts := st.DebugLineRate("gw", now)
	if n != 170 || len(ts) != 2 || ts[0] != "dhcp" {
		t.Errorf("%d %v", n, ts)
	}
	for i := 0; i < 500; i++ {
		st.NoteDebugLine("gw", OtherDebug, now)
	}
	if n, ts := st.DebugLineRate("gw", now); n != 170 || len(ts) != 2 {
		t.Errorf("other debug must not count towards the suggestion: %d %v", n, ts)
	}
	if n, _ := st.DebugLineRate("gw", now.Add(30*time.Minute)); n != 170 && n != 0 {
		t.Errorf("%d", n)
	}
	if n, _ := st.DebugLineRate("gw", now.Add(3*time.Hour)); n != 0 {
		t.Errorf("stale counter %d", n)
	}
}
