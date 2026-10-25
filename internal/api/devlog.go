package api

import (
	"net/http"
	"strconv"
)

func init() {
	routeHooks = append(routeHooks, func(s *Server, m *http.ServeMux, a func(http.HandlerFunc) http.HandlerFunc) {
		m.HandleFunc("GET /api/devices/{name}/log", a(s.deviceLog))
	})
}

// deviceLog: GET /api/devices/{name}/log?q=&level=&range=&limit= – the device's router event log (system, account,
// dhcp, wifi, … lines the router sends to mtmon via syslog), newest first.
func (s *Server) deviceLog(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := s.Cfg.Device(name); !ok {
		jerr(w, 404, "unknown device")
		return
	}
	since, _, rn := parseRange(r)
	q := r.URL.Query()
	lim, _ := strconv.Atoi(q.Get("limit"))
	if lim <= 0 || lim > 1000 {
		lim = 200
	}
	es, err := s.St.LogEvents(name, q.Get("q"), q.Get("level"), since, lim)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]any{"range": rn, "events": es})
}
