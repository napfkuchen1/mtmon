// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/napfkuchen1/mtmon/internal/poller"
	"github.com/napfkuchen1/mtmon/internal/probe"
)

// CustomTargets returns the own probe targets from the settings (never nil).
func CustomTargets(get func(string) string) []probe.Custom {
	out := []probe.Custom{}
	if v := get("wan_custom"); v != "" {
		json.Unmarshal([]byte(v), &out)
	}
	if out == nil {
		out = []probe.Custom{}
	}
	return out
}

// internetTargets: GET /api/internet/targets – the own targets and the limit.
func (s *Server) internetTargets(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, map[string]any{"targets": CustomTargets(s.St.GetMeta), "max": probe.MaxCustom})
}

// internetTargetsSave: PUT /api/internet/targets {targets:[{kind,target,insecure}]} – replaces the list; the probe
// reads it every round, so no restart is needed.
func (s *Server) internetTargetsSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Targets []probe.Custom `json:"targets"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	list, err := probe.ValidateCustom(in.Targets)
	if err != nil {
		jerr(w, 400, err.Error())
		return
	}
	old := CustomTargets(s.St.GetMeta)
	b, _ := json.Marshal(list)
	if err := s.St.SetMeta("wan_custom", string(b)); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	s.targetsRemoved(old, list)
	jsonOut(w, map[string]any{"targets": list, "max": probe.MaxCustom})
}

// targetsRemoved closes the alerts of the targets that are in old but no longer in list (#194).
func (s *Server) targetsRemoved(old, list []probe.Custom) {
	keep := map[string]bool{}
	for _, c := range list {
		for _, k := range probe.Keys(c) {
			keep[k] = true
		}
	}
	for _, c := range old {
		for _, k := range probe.Keys(c) {
			if keep[k] {
				continue
			}
			if s.Alerts != nil {
				s.Alerts.TargetRemoved(k)
			} else {
				s.St.ResolveAlerts("target_down", k, " – target removed")
			}
		}
	}
}

// internet: GET /api/internet?range=… – quality of the internet connection as measured by mtmon's own probes, plus
// how often the WAN link reconnected (from the alert history).
func (s *Server) internet(w http.ResponseWriter, r *http.Request) {
	since, bucket, rn := parseRange(r)
	if bucket < 300 {
		bucket = 300
	}
	now := time.Now().Unix()
	downs, lastDown := s.St.AlertKindStats("wan_down", now-86400)
	_, lastUp := s.St.AlertKindStats("wan_up", now-86400)
	jsonOut(w, map[string]any{
		"enabled": s.St.GetMeta("wan_probe") != "off", "range": rn,
		"now":            s.St.WANQualitySince(now - 300),
		"window":         s.St.WANQualitySince(since),
		"day":            s.St.WANQualitySince(now - 86400),
		"targets":        s.St.WANTargets(since),
		"series":         s.St.WANSeries(since, bucket),
		"upstream":       s.upstreams(),
		"reconnects_24h": downs, "last_drop": lastDown, "last_reconnect": lastUp,
	})
}

// internetSave: PUT /api/internet {enabled} – switches the probes on or off (they connect to public resolvers).
func (s *Server) internetSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	v := "on"
	if !in.Enabled {
		v = "off"
	}
	if err := s.St.SetMeta("wan_probe", v); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]bool{"enabled": in.Enabled})
}

// upstreams lists the ISP modems / routers in front of the gateways (never nil).
func (s *Server) upstreams() []poller.Upstream {
	if s.Mgr == nil {
		return []poller.Upstream{}
	}
	return s.Mgr.Upstreams()
}
