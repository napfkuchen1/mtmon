// SPDX-License-Identifier: AGPL-3.0-only

package poller

import (
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// TestLiveIndexCached: SelfByAddr answers from the in-memory index. Repeated calls do not rebuild it (no database
// read, no JSON decode per flow); after the 10 s window a changed stored address is picked up.
func TestLiveIndexCached(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	caps := `{"addresses":["192.168.0.1/24 on bridge"]}`
	if err := st.UpsertDevice(store.DeviceRow{Name: "gw", Addr: "10.0.0.9", Scheme: "https", Port: 443, Caps: []byte(caps)}); err != nil {
		t.Fatal(err)
	}
	m := &Manager{Cfg: &config.Config{Devices: []config.Device{{Name: "gw", Addr: "10.0.0.9"}}}, St: st, self: map[string]string{}}

	if d, ok := m.SelfByAddr("192.168.0.1"); !ok || d != "gw" {
		t.Fatalf("SelfByAddr(stored) = %q,%v want gw,true", d, ok)
	}
	first := m.ixAt
	for i := 0; i < 1000; i++ {
		m.SelfByAddr("192.168.0.1")
	}
	if !m.ixAt.Equal(first) {
		t.Errorf("index rebuilt inside the 10 s window")
	}

	// stored caps change: the new address shows up after the window, the old one goes away
	caps2 := `{"addresses":["192.168.7.1/24 on bridge"]}`
	if err := st.UpsertDevice(store.DeviceRow{Name: "gw", Addr: "10.0.0.9", Scheme: "https", Port: 443, Caps: []byte(caps2)}); err != nil {
		t.Fatal(err)
	}
	m.ixAt = time.Now().Add(-11 * time.Second)
	if d, ok := m.SelfByAddr("192.168.7.1"); !ok || d != "gw" {
		t.Errorf("after rebuild SelfByAddr(new) = %q,%v want gw,true", d, ok)
	}
	if _, ok := m.SelfByAddr("192.168.0.1"); ok {
		t.Errorf("after rebuild old address still owned")
	}
}
