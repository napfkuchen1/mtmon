// SPDX-License-Identifier: AGPL-3.0-only

package mockros

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// PoePort is one PoE-out port. Watts empty = a model without power figures (the field is not sent at all).
type PoePort struct{ Name, Status, Pair, Watts string }

// SfpPort is one SFP cage. A field left empty is not sent (a module without DOM, or an empty cage).
type SfpPort struct {
	Name         string
	Present      bool
	Rx, Tx, Temp string
	Voltage      string
}

// LteIface is one LTE interface with the signal values its monitor call returns (empty = not sent).
type LteIface struct{ Name, RSRP, RSRQ, SINR, RSSI, Tech, Status string }

// monitorPaths are the only POSTs the mock answers with data; the real client allowlists the same three.
var monitorPaths = map[string]bool{
	"/interface/ethernet/poe/monitor": true,
	"/interface/ethernet/monitor":     true,
	"/interface/lte/monitor":          true,
}

// MonitorCalls returns the paths of all monitor POSTs received so far (tests).
func (s *Server) MonitorCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.monitors...)
}

// SetPoe replaces the PoE fixture (tests).
func (s *Server) SetPoe(p []PoePort) { s.mu.Lock(); s.Poe = p; s.mu.Unlock() }

// SetMonitorDeny makes every monitor POST answer 403, as a read-only user without the right does (tests).
func (s *Server) SetMonitorDeny(d bool) { s.mu.Lock(); s.MonitorDeny = d; s.mu.Unlock() }

// hardware serves the GET lists that tell a client which hardware exists, and the monitor POSTs. It returns true
// when it answered. A list with no fixture answers 404, like a RouterOS without that package or hardware.
func (s *Server) hardware(w http.ResponseWriter, r *http.Request) bool {
	path := strings.TrimPrefix(r.URL.Path, "/rest")
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/monitor") {
		return s.monitor(w, r, path)
	}
	if r.Method != http.MethodGet {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch path {
	case "/interface/ethernet/poe":
		if len(s.Poe) == 0 {
			http.NotFound(w, r)
			return true
		}
		out := []obj{}
		for _, p := range s.Poe {
			out = append(out, obj{"name": p.Name, "poe-out": "auto-on"})
		}
		writeJSON(w, 200, out)
		return true
	case "/interface/ethernet":
		if len(s.Poe) == 0 && len(s.Sfp) == 0 {
			return false
		}
		out := []obj{{"name": "ether1", "default-name": "ether1"}}
		for _, p := range s.Sfp {
			out = append(out, obj{"name": p.Name, "default-name": p.Name})
		}
		writeJSON(w, 200, out)
		return true
	case "/interface/lte":
		if len(s.Lte) == 0 {
			http.NotFound(w, r)
			return true
		}
		out := []obj{}
		for _, l := range s.Lte {
			out = append(out, obj{"name": l.Name})
		}
		writeJSON(w, 200, out)
		return true
	}
	return false
}

func (s *Server) monitor(w http.ResponseWriter, r *http.Request, path string) bool {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.monitors = append(s.monitors, path)
	deny := s.MonitorDeny
	s.mu.Unlock()
	if !monitorPaths[path] {
		return false
	}
	if deny {
		rosErr(w, 403, "forbidden")
		return true
	}
	var req struct {
		Numbers  string `json:"numbers"`
		Proplist string `json:".proplist"`
	}
	json.Unmarshal(body, &req)
	want := map[string]bool{}
	for _, f := range strings.Split(req.Proplist, ",") {
		if f = strings.TrimSpace(f); f != "" {
			want[f] = true
		}
	}
	row := obj{}
	set := func(k, v string) {
		if v != "" && (len(want) == 0 || want[k]) {
			row[k] = v
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch path {
	case "/interface/ethernet/poe/monitor":
		for _, p := range s.Poe {
			if p.Name == req.Numbers {
				set("name", p.Name)
				set("poe-out-status", p.Status)
				set("poe-out-power-pair", p.Pair)
				set("poe-out-power", p.Watts)
			}
		}
	case "/interface/ethernet/monitor":
		for _, p := range s.Sfp {
			if p.Name == req.Numbers {
				set("name", p.Name)
				if p.Present {
					set("sfp-module-present", "true")
				}
				set("sfp-rx-power", p.Rx)
				set("sfp-tx-power", p.Tx)
				set("sfp-temperature", p.Temp)
				set("sfp-supply-voltage", p.Voltage)
			}
		}
	case "/interface/lte/monitor":
		for _, l := range s.Lte {
			if l.Name == req.Numbers {
				set("name", l.Name)
				set("rsrp", l.RSRP)
				set("rsrq", l.RSRQ)
				set("sinr", l.SINR)
				set("rssi", l.RSSI)
				set("access-technology", l.Tech)
				set("status", l.Status)
			}
		}
	}
	writeJSON(w, 200, []obj{row})
	return true
}
