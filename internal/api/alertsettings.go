package api

import "net/http"

func init() {
	routeHooks = append(routeHooks, func(s *Server, m *http.ServeMux, a func(http.HandlerFunc) http.HandlerFunc) {
		m.HandleFunc("GET /api/alert-settings", a(s.alertSettings))
		m.HandleFunc("PUT /api/alert-settings", a(s.alertSettingsSave))
	})
}

// alertSettings: GET /api/alert-settings – which new clients raise an alert.
func (s *Server) alertSettings(w http.ResponseWriter, r *http.Request) {
	mode := s.St.GetMeta("alert_new_client")
	if mode != "all" && mode != "off" {
		mode = "unknown"
	}
	jsonOut(w, map[string]string{"new_client": mode})
}

// alertSettingsSave: PUT /api/alert-settings {new_client: all|unknown|off}.
func (s *Server) alertSettingsSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		NewClient string `json:"new_client"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.NewClient != "all" && in.NewClient != "unknown" && in.NewClient != "off" {
		jerr(w, 400, "new_client must be all, unknown or off")
		return
	}
	if err := s.St.SetMeta("alert_new_client", in.NewClient); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]string{"new_client": in.NewClient})
}
