package api

import (
	"net/http"
	"time"
)

const maxMaintenanceMinutes = 7 * 24 * 60

// deviceMaintenance: PUT /api/devices/{name}/maintenance {"minutes": 30} – opens a maintenance window, 0 ends it.
// While it is open, offline and uplink-down alerts of the device are recorded as info.
func (s *Server) deviceMaintenance(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := s.Cfg.Device(name); !ok {
		jerr(w, 404, "unknown device")
		return
	}
	var in struct {
		Minutes int `json:"minutes"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Minutes < 0 || in.Minutes > maxMaintenanceMinutes {
		jerr(w, 400, "minutes must be between 0 and 10080")
		return
	}
	var until int64
	if in.Minutes > 0 {
		until = time.Now().Add(time.Duration(in.Minutes) * time.Minute).Unix()
	}
	if err := s.St.SetMaintenance(name, until); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]any{"ok": true, "maintenance_until": until})
}
