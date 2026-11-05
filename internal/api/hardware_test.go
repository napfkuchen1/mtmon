// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/store"
)

func fl(v float64) *float64 { return &v }

func seedHW(s *Server) {
	s.Cfg.Devices = append(s.Cfg.Devices, config.Device{Name: "sw1", Role: "switch", Addr: "10.0.0.2"})
	s.St.SetHW("sw1", store.HWDevice{TS: time.Now().Unix(), Caps: store.HWCaps{SFP: store.HWOn, PoE: store.HWOn, LTE: store.HWOn},
		SFP: []store.HWSfp{{Iface: "sfp1", Present: true, RxDBm: fl(-7.5), TxDBm: fl(-2), TempC: nil}},
		PoE: []store.HWPoe{{Iface: "ether2", Status: "powered-on", Pair: "b"}, {Iface: "ether3", Status: "overload", Fault: true, PowerW: fl(0)}},
		LTE: []store.HWLte{{Iface: "lte1", Tech: "lte", RSRP: fl(-97), SINR: fl(8)}}})
}

func getBody(t *testing.T, c *http.Client, url string) string {
	t.Helper()
	r, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 {
		t.Fatalf("%s: %d %s", url, r.StatusCode, b)
	}
	return string(b)
}

// Only values the router reported become samples: the PoE port without watts has no power sample, the module
// without a temperature has none either, and the families stay in one block.
func TestMetricsHardwareHasNoInventedZero(t *testing.T) {
	ts, s := newTestServer(t)
	seedHW(s)
	_, c := login(t, ts, "test-password-1")
	m := getBody(t, c, ts.URL+"/metrics")
	for _, want := range []string{
		`mtmon_sfp_rx_power_dbm{device="sw1",interface="sfp1"} -7.5`,
		`mtmon_sfp_tx_power_dbm{device="sw1",interface="sfp1"} -2`,
		`mtmon_poe_out_fault{device="sw1",interface="ether2"} 0`,
		`mtmon_poe_out_fault{device="sw1",interface="ether3"} 1`,
		`mtmon_lte_rsrp_dbm{device="sw1",interface="lte1"} -97`,
		`mtmon_lte_sinr_db{device="sw1",interface="lte1"} 8`,
	} {
		if !strings.Contains(m, want+"\n") {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(m, `mtmon_poe_out_power_watts{device="sw1",interface="ether2"}`) {
		t.Error("PoE port without power figures got a power sample")
	}
	for _, absent := range []string{"mtmon_sfp_temperature_celsius", "mtmon_lte_rsrq_db", "mtmon_lte_rssi_dbm"} {
		if strings.Contains(m, absent) {
			t.Errorf("%s emitted without any reading", absent)
		}
	}
}

func TestMetricsWithoutHardwareEmitsNoHardwareFamilies(t *testing.T) {
	ts, _ := newTestServer(t)
	_, c := login(t, ts, "test-password-1")
	m := getBody(t, c, ts.URL+"/metrics")
	for _, f := range []string{"mtmon_sfp_", "mtmon_poe_", "mtmon_lte_"} {
		if strings.Contains(m, f) {
			t.Errorf("%s* present without hardware", f)
		}
	}
}

func TestDeviceDetailHardwareAndInternetLTE(t *testing.T) {
	ts, s := newTestServer(t)
	_, c := login(t, ts, "test-password-1")

	s.Cfg.Devices = append(s.Cfg.Devices, config.Device{Name: "plain", Role: "router", Addr: "10.0.0.3"})
	var plain map[string]any
	json.Unmarshal([]byte(getBody(t, c, ts.URL+"/api/devices/plain")), &plain)
	if v, ok := plain["hardware"]; !ok || v != nil {
		t.Errorf("device without hardware: hardware = %v (want null)", v)
	}
	var inet map[string]any
	json.Unmarshal([]byte(getBody(t, c, ts.URL+"/api/internet")), &inet)
	if l, _ := inet["lte"].([]any); len(l) != 0 {
		t.Errorf("lte listed without an LTE interface: %v", inet["lte"])
	}

	seedHW(s)
	s.St.SetSfpWarnDBm(fl(-20))
	var d struct {
		Hardware struct {
			SFP  []store.HWSfp
			PoE  []store.HWPoe
			LTE  []store.HWLte
			Warn float64 `json:"sfp_rx_warn_dbm"`
			Hist []any   `json:"sfp_history"`
		}
	}
	d.Hardware.SFP = nil
	raw := getBody(t, c, ts.URL+"/api/devices/sw1")
	var wrap map[string]json.RawMessage
	json.Unmarshal([]byte(raw), &wrap)
	if err := json.Unmarshal(wrap["hardware"], &struct {
		SFP *[]store.HWSfp `json:"sfp"`
		PoE *[]store.HWPoe `json:"poe"`
		LTE *[]store.HWLte `json:"lte"`
	}{&d.Hardware.SFP, &d.Hardware.PoE, &d.Hardware.LTE}); err != nil {
		t.Fatal(err)
	}
	if len(d.Hardware.SFP) != 1 || len(d.Hardware.PoE) != 2 || len(d.Hardware.LTE) != 1 {
		t.Fatalf("hardware = %s", wrap["hardware"])
	}
	if !strings.Contains(string(wrap["hardware"]), `"sfp_rx_warn_dbm":-20`) {
		t.Errorf("limit missing: %s", wrap["hardware"])
	}
	if !strings.Contains(string(wrap["hardware"]), `"power_w":null`) {
		t.Errorf("a port without watts must serialise as null: %s", wrap["hardware"])
	}

	inet = nil
	json.Unmarshal([]byte(getBody(t, c, ts.URL+"/api/internet")), &inet)
	l, _ := inet["lte"].([]any)
	if len(l) != 1 || l[0].(map[string]any)["iface"] != "lte1" || l[0].(map[string]any)["rsrp"] != float64(-97) || l[0].(map[string]any)["rssi"] != nil {
		t.Fatalf("lte = %v", inet["lte"])
	}
}

func TestHardwareSettings(t *testing.T) {
	ts, _ := newTestServer(t)
	_, c := login(t, ts, "test-password-1")
	put := func(body string) int {
		req, _ := http.NewRequest("PUT", ts.URL+"/api/hardware-settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if g := getBody(t, c, ts.URL+"/api/hardware-settings"); !strings.Contains(g, `"sfp_rx_warn_dbm":null`) {
		t.Fatalf("default = %s", g)
	}
	if put(`{"sfp_rx_warn_dbm":-18.5}`) != 200 || !strings.Contains(getBody(t, c, ts.URL+"/api/hardware-settings"), `-18.5`) {
		t.Error("save")
	}
	if put(`{"sfp_rx_warn_dbm":-500}`) != 400 || put(`{"sfp_rx_warn_dbm":99}`) != 400 {
		t.Error("out-of-range value must be refused")
	}
	if put(`{"sfp_rx_warn_dbm":null}`) != 200 || !strings.Contains(getBody(t, c, ts.URL+"/api/hardware-settings"), `"sfp_rx_warn_dbm":null`) {
		t.Error("clear")
	}
}
