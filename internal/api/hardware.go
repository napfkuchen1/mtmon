// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func init() {
	routeHooks = append(routeHooks, func(s *Server, m *http.ServeMux, a func(http.HandlerFunc) http.HandlerFunc) {
		m.HandleFunc("GET /api/hardware-settings", a(s.hardwareSettings))
		m.HandleFunc("PUT /api/hardware-settings", a(s.hardwareSettingsSave))
	})
}

// hardwareSettings: GET /api/hardware-settings – the SFP receive-power warning limit in dBm (null = no warning).
func (s *Server) hardwareSettings(w http.ResponseWriter, r *http.Request) {
	var v *float64
	if f, ok := s.St.SfpWarnDBm(); ok {
		v = &f
	}
	jsonOut(w, map[string]any{"sfp_rx_warn_dbm": v})
}

// hardwareSettingsSave: PUT /api/hardware-settings {sfp_rx_warn_dbm: number|null}.
func (s *Server) hardwareSettingsSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SfpRxWarnDBm *float64 `json:"sfp_rx_warn_dbm"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if v := in.SfpRxWarnDBm; v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < -60 || *v > 10) {
		jerr(w, 400, "sfp_rx_warn_dbm must be between -60 and 10 dBm")
		return
	}
	if err := s.St.SetSfpWarnDBm(in.SfpRxWarnDBm); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]any{"sfp_rx_warn_dbm": in.SfpRxWarnDBm})
}

// deviceHardware is the "hardware" part of /api/devices/{name}: the latest SFP, PoE and LTE readings and the
// stored optical history. nil when the device has no such hardware (or none was read yet).
func (s *Server) deviceHardware(name string, since int64) map[string]any {
	h, ok := s.St.HW(name)
	if !ok || h.Empty() {
		return nil
	}
	out := map[string]any{"ts": h.TS, "sfp": nz(h.SFP), "poe": nz(h.PoE), "lte": nz(h.LTE)}
	if len(h.SFP) > 0 {
		out["sfp_history"] = s.St.SfpHistory(name, since)
		if f, ok := s.St.SfpWarnDBm(); ok {
			out["sfp_rx_warn_dbm"] = f
		}
	}
	return out
}

// nz keeps a nil slice from turning into JSON null.
func nz[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

// lteSignals lists the LTE interfaces of all devices for the Internet page; empty without any LTE interface.
func (s *Server) lteSignals() []map[string]any {
	all := s.St.AllHW()
	names := make([]string, 0, len(all))
	for n := range all {
		names = append(names, n)
	}
	sort.Strings(names)
	out := []map[string]any{}
	for _, n := range names {
		for _, l := range all[n].LTE {
			out = append(out, map[string]any{"device": n, "iface": l.Iface, "tech": l.Tech, "status": l.Status,
				"rsrp": l.RSRP, "rsrq": l.RSRQ, "sinr": l.SINR, "rssi": l.RSSI, "ts": all[n].TS})
		}
	}
	return out
}

// hardwareMetrics appends the hardware gauges to /metrics. A value the router did not report is left out (a
// missing sample, never a 0). Each family is written in one block.
func (s *Server) hardwareMetrics(b *strings.Builder, g func(name, help string)) {
	all := s.St.AllHW()
	names := make([]string, 0, len(all))
	for n := range all {
		names = append(names, n)
	}
	sort.Strings(names)
	fam := func(name, help string, emit func(add func(labels string, v float64))) {
		var lines strings.Builder
		emit(func(labels string, v float64) { fmt.Fprintf(&lines, "%s%s %g\n", name, labels, v) })
		if lines.Len() == 0 {
			return
		}
		g(name, help)
		b.WriteString(lines.String())
	}
	sfp := func(name, help string, pick func(store.HWSfp) *float64) {
		fam(name, help, func(add func(string, float64)) {
			for _, n := range names {
				for _, p := range all[n].SFP {
					if v := pick(p); v != nil {
						add(fmt.Sprintf("{device=%s,interface=%s}", lbl(n), lbl(p.Iface)), *v)
					}
				}
			}
		})
	}
	sfp("mtmon_sfp_rx_power_dbm", "Optical receive power of an SFP module in dBm.", func(p store.HWSfp) *float64 { return p.RxDBm })
	sfp("mtmon_sfp_tx_power_dbm", "Optical transmit power of an SFP module in dBm.", func(p store.HWSfp) *float64 { return p.TxDBm })
	sfp("mtmon_sfp_temperature_celsius", "Temperature of an SFP module.", func(p store.HWSfp) *float64 { return p.TempC })
	fam("mtmon_poe_out_power_watts", "PoE output power of a port, only for models that report it.", func(add func(string, float64)) {
		for _, n := range names {
			for _, p := range all[n].PoE {
				if p.PowerW != nil {
					add(fmt.Sprintf("{device=%s,interface=%s}", lbl(n), lbl(p.Iface)), *p.PowerW)
				}
			}
		}
	})
	fam("mtmon_poe_out_fault", "1 when a PoE port reports short-circuit or overload, else 0.", func(add func(string, float64)) {
		for _, n := range names {
			for _, p := range all[n].PoE {
				v := 0.0
				if p.Fault {
					v = 1
				}
				add(fmt.Sprintf("{device=%s,interface=%s}", lbl(n), lbl(p.Iface)), v)
			}
		}
	})
	lte := func(name, help string, pick func(store.HWLte) *float64) {
		fam(name, help, func(add func(string, float64)) {
			for _, n := range names {
				for _, l := range all[n].LTE {
					if v := pick(l); v != nil {
						add(fmt.Sprintf("{device=%s,interface=%s}", lbl(n), lbl(l.Iface)), *v)
					}
				}
			}
		})
	}
	lte("mtmon_lte_rsrp_dbm", "LTE reference signal received power in dBm.", func(l store.HWLte) *float64 { return l.RSRP })
	lte("mtmon_lte_rsrq_db", "LTE reference signal received quality in dB.", func(l store.HWLte) *float64 { return l.RSRQ })
	lte("mtmon_lte_sinr_db", "LTE signal to interference plus noise ratio in dB.", func(l store.HWLte) *float64 { return l.SINR })
	lte("mtmon_lte_rssi_dbm", "LTE received signal strength in dBm.", func(l store.HWLte) *float64 { return l.RSSI })
}
