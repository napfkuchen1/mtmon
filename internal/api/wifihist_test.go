package api

import (
	"encoding/json"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestWifiHistoryEndpoints(t *testing.T) {
	ts, s := newTestServer(t)
	s.Cfg.Devices = append(s.Cfg.Devices, config.Device{Name: "ap1", Addr: "10.0.0.2", Role: "ap"})
	s.St.UpsertClients([]store.Observation{{MAC: "AA:BB:CC:00:11:22", Hostname: "phone", WiFi: true, Present: true, Device: "ap1", Band: "5ghz-ax", Signal: -61, TxRate: "866.7Mbps-80MHz/2S/SGI", RxRate: "650Mbps"}})
	s.St.UpsertClients([]store.Observation{{MAC: "AA:BB:CC:00:11:22", Hostname: "phone", WiFi: true, Present: true, Device: "ap1", Band: "5ghz-ax", Signal: -67, TxRate: "866.7Mbps", RxRate: "650Mbps"}})
	_, c := login(t, ts, "test-password-1")
	r, _ := c.Get(ts.URL + "/api/clients/aa:bb:cc:00:11:22/wifi?range=1h")
	var m struct {
		Points []struct {
			Avg float64 `json:"avg"`
			Min int     `json:"min"`
		} `json:"points"`
		Events []any `json:"events"`
	}
	json.NewDecoder(r.Body).Decode(&m)
	if r.StatusCode != 200 || len(m.Points) != 1 || m.Points[0].Avg != -64 || m.Points[0].Min != -67 || m.Events == nil {
		t.Errorf("client wifi: %d %+v", r.StatusCode, m)
	}
	r, _ = c.Get(ts.URL + "/api/devices/ap1/wifi?range=" + "1h")
	var b struct {
		Bands []store.BandPoint `json:"bands"`
	}
	json.NewDecoder(r.Body).Decode(&b)
	if len(b.Bands) == 0 || b.Bands[0].Band != "5ghz-ax" || b.Bands[0].Clients != 1 {
		t.Errorf("device wifi: %+v", b)
	}
	if r, _ := c.Get(ts.URL + "/api/devices/nope/wifi"); r.StatusCode != 404 {
		t.Errorf("unknown device: %d", r.StatusCode)
	}
}
