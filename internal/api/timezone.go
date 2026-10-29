package api

import (
	"net/http"
	"time"
	_ "time/tzdata" // zone names must resolve in minimal containers without /usr/share/zoneinfo
)

func init() {
	routeHooks = append(routeHooks, func(s *Server, m *http.ServeMux, a func(http.HandlerFunc) http.HandlerFunc) {
		m.HandleFunc("GET /api/timezone", a(s.timezone))
		m.HandleFunc("PUT /api/timezone", a(s.timezoneSave))
	})
}

// processZone is the zone the process started with (TZ or the container's /etc/localtime).
var processZone = time.Local

// ApplyTimeZone makes `name` the zone of all clock times (speed-test anchor and window, quiet hours, "around HH:MM"
// texts, nightly jobs). They all read time.Local, so it is switched once at start-up and on every change.
// An empty name restores the zone the process started with.
func ApplyTimeZone(name string) error {
	if name == "" {
		time.Local = processZone
		return nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return err
	}
	time.Local = loc
	return nil
}

// timezone: GET /api/timezone – the zone in use and whether it was chosen (meta) or inherited from the process.
func (s *Server) timezone(w http.ResponseWriter, r *http.Request) {
	name := s.St.GetMeta("timezone")
	now := time.Now()
	jsonOut(w, map[string]any{
		"zone": time.Local.String(), "set": name != "", "process_zone": processZone.String(),
		"now": now.Format("2006-01-02 15:04"), "offset_min": func() int { _, o := now.Zone(); return o / 60 }(),
	})
}

// timezoneSave: PUT /api/timezone {zone} – IANA name such as Europe/Berlin; empty = back to the process zone.
func (s *Server) timezoneSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Zone string `json:"zone"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Zone == "Local" {
		jerr(w, 400, "use an IANA zone name such as Europe/Berlin")
		return
	}
	if err := ApplyTimeZone(in.Zone); err != nil {
		jerr(w, 400, "unknown time zone")
		return
	}
	if err := s.St.SetMeta("timezone", in.Zone); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	s.timezone(w, r)
}
