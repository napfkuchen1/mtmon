package api

import "net/http"

// clientWatch: PUT /api/clients/{mac}/watch {watch} – "must be online": an alert fires when the client stays offline.
func (s *Server) clientWatch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Watch bool `json:"watch"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if _, err := s.St.Client(r.PathValue("mac")); err != nil {
		jerr(w, 404, "unknown client")
		return
	}
	if err := s.St.SetWatch([]string{r.PathValue("mac")}, in.Watch); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]bool{"ok": true})
}

// clientsWatch: POST /api/clients/watch {macs, watch} – the same for several clients at once.
func (s *Server) clientsWatch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MACs  []string `json:"macs"`
		Watch bool     `json:"watch"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.MACs) == 0 || len(in.MACs) > 1000 {
		jerr(w, 400, "1 to 1000 clients")
		return
	}
	if err := s.St.SetWatch(in.MACs, in.Watch); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]int{"count": len(in.MACs)})
}
