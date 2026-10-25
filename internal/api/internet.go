package api

import (
	"net/http"
	"time"

	"github.com/napfkuchen1/mtmon/internal/poller"
)

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
