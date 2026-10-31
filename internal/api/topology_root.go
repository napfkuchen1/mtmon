// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
)

// rootCand is what the root choice knows about a managed device.
type rootCand struct {
	Name, Role         string
	NAT, DHCP, CAPsMAN bool
	Clients            int
}

// pickRoot chooses the top of the topology tree. A manual choice wins (if the device still exists). Otherwise the
// gateway is the router that does NAT and hands out DHCP (and runs the CAPsMAN), ties go to the one with more
// clients, then to the first one in the list. Without any router the first device is used.
func pickRoot(devs []rootCand, override string) string {
	if len(devs) == 0 {
		return ""
	}
	if override != "" {
		for _, d := range devs {
			if d.Name == override {
				return override
			}
		}
	}
	best, bestScore := "", -1.0
	for _, d := range devs {
		if d.Role != "router" {
			continue
		}
		sc := float64(d.Clients) / 1000
		if d.NAT {
			sc += 4
		}
		if d.DHCP {
			sc += 2
		}
		if d.CAPsMAN {
			sc++
		}
		if sc > bestScore {
			best, bestScore = d.Name, sc
		}
	}
	if best == "" {
		return devs[0].Name
	}
	return best
}

const rootMetaKey = "topo_root"

// topologyRoot: PUT /api/topology/root {"device": "name"} – use this device as the top of the tree; "" = automatic.
func (s *Server) topologyRoot(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Device string `json:"device"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Device != "" {
		if _, ok := s.Cfg.Device(in.Device); !ok {
			jerr(w, 404, "unknown device")
			return
		}
	}
	if err := s.St.SetMeta(rootMetaKey, in.Device); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]any{"ok": true, "root_override": in.Device})
}
