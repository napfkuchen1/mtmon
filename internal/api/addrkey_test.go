package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// Traffic that could not be matched to a client is booked under its address; its page must open and show it.
func TestClientPageForBareAddress(t *testing.T) {
	ts, s := newTestServer(t)
	now := time.Now().Unix()
	s.St.Enqueue(store.FlowRow{TS: now - 30, CIP: "fd00::5", RIP: "203.0.113.9", CPort: 4000, RPort: 443, Proto: 6, Dir: "u", Bytes: 1234, Svc: "Web"})
	if err := s.St.Flush(); err != nil {
		t.Fatal(err)
	}
	_, c := login(t, ts, "test-password-1")
	r, _ := c.Get(ts.URL + "/api/clients/fd00::5?range=1h")
	if r.StatusCode != 200 {
		t.Fatalf("address page: %d", r.StatusCode)
	}
	var out struct {
		AddressOnly bool `json:"address_only"`
		Client      struct{ IP string }
		TopHosts    []struct{ Key string } `json:"top_hosts"`
	}
	json.NewDecoder(r.Body).Decode(&out)
	if !out.AddressOnly || out.Client.IP != "fd00::5" || len(out.TopHosts) != 1 || out.TopHosts[0].Key != "203.0.113.9" {
		t.Errorf("%+v", out)
	}
	r, _ = c.Get(ts.URL + "/api/clients/fd00::5/connections?range=1h")
	var conns []map[string]any
	json.NewDecoder(r.Body).Decode(&conns)
	if len(conns) != 1 {
		t.Errorf("connections of the address: %v", conns)
	}
	if r, _ := c.Get(ts.URL + "/api/clients/not-an-address"); r.StatusCode != 404 {
		t.Errorf("garbage key: %d", r.StatusCode)
	}
}

func TestNormMACKeepsAddressKeys(t *testing.T) {
	for in, want := range map[string]string{
		"aa:bb:cc:00:11:22": "AA:BB:CC:00:11:22", "vpn:fd00::9": "VPN:FD00::9", " 10.0.0.2 ": "10.0.0.2",
		"FD00::5": "fd00::5", "fe80::1": "fe80::1",
	} {
		if got := store.NormMAC(in); got != want {
			t.Errorf("NormMAC(%q)=%q want %q", in, got, want)
		}
	}
}
