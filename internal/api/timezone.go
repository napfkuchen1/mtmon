// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"fmt"
	"net/http"
	"os"
	"time"
	_ "time/tzdata" // zone names must resolve in minimal containers without /usr/share/zoneinfo
)

func init() {
	routeHooks = append(routeHooks, func(s *Server, m *http.ServeMux, a func(http.HandlerFunc) http.HandlerFunc) {
		m.HandleFunc("GET /api/timezone", a(s.timezone))
		m.HandleFunc("PUT /api/timezone", a(s.timezoneSave))
	})
}

// appliedZone is the IANA name ApplyTimeZone switched to at start-up ("" = the zone the process came with).
var appliedZone string

// processZoneName names the zone the process runs in: the applied one, else TZ, else the abbreviation and offset.
func processZoneName() string {
	if appliedZone != "" {
		return appliedZone
	}
	if tz := os.Getenv("TZ"); tz != "" && tz != "Local" {
		return tz
	}
	abbr, off := time.Now().Zone()
	return fmt.Sprintf("%s (UTC%+03d:%02d)", abbr, off/3600, (off%3600)/60)
}

// ApplyTimeZone makes `name` the zone of all clock times (speed-test anchor and window, quiet hours, "around HH:MM"
// texts, nightly jobs). They all read time.Local, which is only switched at start-up (before any goroutine runs);
// a change in the UI is stored and takes effect with the next restart.
func ApplyTimeZone(name string) error {
	if name == "" {
		return nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return err
	}
	time.Local = loc
	appliedZone = name
	return nil
}

// timezone: GET /api/timezone – the zone in use and whether it was chosen (meta) or inherited from the process.
func (s *Server) timezone(w http.ResponseWriter, r *http.Request) {
	name := s.St.GetMeta("timezone")
	now := time.Now()
	jsonOut(w, map[string]any{
		"zone": processZoneName(), "set": name != "",
		"configured": name, "restart_required": name != "" && name != appliedZone,
		"now": now.Format("2006-01-02 15:04"), "offset_min": func() int { _, o := now.Zone(); return o / 60 }(),
	})
}

// timezoneSave: PUT /api/timezone {zone} – IANA name such as Europe/Berlin; empty = back to the process zone (after a restart).
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
	if in.Zone != "" {
		if _, err := time.LoadLocation(in.Zone); err != nil {
			jerr(w, 400, "unknown time zone")
			return
		}
	}
	if err := s.St.SetMeta("timezone", in.Zone); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	s.timezone(w, r)
}
