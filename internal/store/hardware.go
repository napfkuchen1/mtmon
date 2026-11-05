// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"sort"
	"strconv"
	"strings"
)

// Hardware health beyond CPU and RAM (#300): SFP optics (DOM), PoE-out ports and LTE signal. A value the router
// did not report stays nil – it is shown as "—" and never as 0 (a model without power figures must not read 0 W).

// Capability states of one hardware feature on one device.
const (
	HWUnknown = ""     // not probed yet, or the last probe failed for a transient reason
	HWOn      = "on"   // the monitor call answered
	HWOff     = "off"  // the router refused the monitor call (permission): not asked again
	HWNone    = "none" // the device has no such hardware or package
)

// HWCaps is the probe result per feature.
type HWCaps struct {
	SFP string `json:"sfp"`
	PoE string `json:"poe"`
	LTE string `json:"lte"`
}

// HWSfp is the latest reading of one SFP cage with a module that reports optical power.
type HWSfp struct {
	Iface   string   `json:"iface"`
	Present bool     `json:"present"`
	RxDBm   *float64 `json:"rx_dbm"`
	TxDBm   *float64 `json:"tx_dbm"`
	TempC   *float64 `json:"temp_c"`
	VoltV   *float64 `json:"volt_v"`
}

// HWPoe is the latest reading of one PoE-out port.
type HWPoe struct {
	Iface  string   `json:"iface"`
	Status string   `json:"status"`          // poe-out-status, e.g. powered-on, short-circuit, overload
	Pair   string   `json:"pair,omitempty"`  // poe-out-power-pair
	PowerW *float64 `json:"power_w"`         // nil on models without power figures
	Fault  bool     `json:"fault,omitempty"` // short-circuit or overload
}

// HWLte is the latest signal of one LTE interface.
type HWLte struct {
	Iface  string   `json:"iface"`
	Tech   string   `json:"tech,omitempty"`
	Status string   `json:"status,omitempty"`
	RSRP   *float64 `json:"rsrp"`
	RSRQ   *float64 `json:"rsrq"`
	SINR   *float64 `json:"sinr"`
	RSSI   *float64 `json:"rssi"`
}

// HWDevice is the latest hardware reading of one device.
type HWDevice struct {
	TS   int64   `json:"ts"`
	Caps HWCaps  `json:"caps"`
	SFP  []HWSfp `json:"sfp,omitempty"`
	PoE  []HWPoe `json:"poe,omitempty"`
	LTE  []HWLte `json:"lte,omitempty"`
}

// Empty reports whether there is nothing to show.
func (h HWDevice) Empty() bool { return len(h.SFP) == 0 && len(h.PoE) == 0 && len(h.LTE) == 0 }

// SetHW keeps the latest reading of a device (in memory; the next poll rewrites it) and stores the SFP values.
func (s *Store) SetHW(device string, h HWDevice) {
	s.hwMu.Lock()
	if s.hw == nil {
		s.hw = map[string]HWDevice{}
	}
	s.hw[device] = h
	s.hwMu.Unlock()
	for _, p := range h.SFP {
		if p.RxDBm == nil && p.TxDBm == nil && p.TempC == nil {
			continue
		}
		s.exec(`INSERT INTO sfp_metrics(ts,device,iface,rx_dbm,tx_dbm,temp) VALUES(?,?,?,?,?,?)`,
			h.TS, device, p.Iface, nullF(p.RxDBm), nullF(p.TxDBm), nullF(p.TempC))
	}
}

func nullF(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// HW returns the latest reading of a device.
func (s *Store) HW(device string) (HWDevice, bool) {
	s.hwMu.Lock()
	defer s.hwMu.Unlock()
	h, ok := s.hw[device]
	return h, ok
}

// AllHW returns the latest reading of every device, by device name.
func (s *Store) AllHW() map[string]HWDevice {
	s.hwMu.Lock()
	defer s.hwMu.Unlock()
	out := make(map[string]HWDevice, len(s.hw))
	for k, v := range s.hw {
		out[k] = v
	}
	return out
}

// ForgetHW drops the in-memory reading of a device (device removed).
func (s *Store) ForgetHW(device string) {
	s.hwMu.Lock()
	delete(s.hw, device)
	s.hwMu.Unlock()
}

// SfpSample is one stored optical reading.
type SfpSample struct {
	TS    int64    `json:"ts"`
	Iface string   `json:"iface"`
	RxDBm *float64 `json:"rx_dbm"`
	TxDBm *float64 `json:"tx_dbm"`
	TempC *float64 `json:"temp_c"`
}

// SfpHistory returns the stored optical readings of a device since the given time, oldest first.
func (s *Store) SfpHistory(device string, since int64) []SfpSample {
	rows, err := s.DB.Query(`SELECT ts,iface,rx_dbm,tx_dbm,temp FROM sfp_metrics WHERE device=? AND ts>=? ORDER BY ts, rowid`, device, since)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []SfpSample
	for rows.Next() {
		var x SfpSample
		var rx, tx, tp *float64
		if rows.Scan(&x.TS, &x.Iface, &rx, &tx, &tp) == nil {
			x.RxDBm, x.TxDBm, x.TempC = rx, tx, tp
			out = append(out, x)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out
}

// metaSfpWarn is the setting key of the SFP receive-power warning limit in dBm.
const metaSfpWarn = "sfp_rx_warn_dbm"

// SfpWarnDBm returns the configured warning limit; ok is false while none is set (no warning is raised then).
func (s *Store) SfpWarnDBm() (float64, bool) {
	v := strings.TrimSpace(s.GetMeta(metaSfpWarn))
	if v == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// SetSfpWarnDBm stores the warning limit; nil clears it.
func (s *Store) SetSfpWarnDBm(v *float64) error {
	if v == nil {
		return s.SetMeta(metaSfpWarn, "")
	}
	return s.SetMeta(metaSfpWarn, strconv.FormatFloat(*v, 'f', -1, 64))
}
