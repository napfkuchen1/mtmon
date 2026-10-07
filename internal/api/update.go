package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// updateRoutes registers the self-update endpoints (all behind the session + CSRF wrapper).
func (s *Server) updateRoutes(m *http.ServeMux, a func(http.HandlerFunc) http.HandlerFunc) {
	m.HandleFunc("GET /api/update", a(s.updateStatus))
	m.HandleFunc("POST /api/update/check", a(s.updateCheck))
	m.HandleFunc("POST /api/update/apply", a(s.updateApply))
	m.HandleFunc("PUT /api/update/mode", a(s.updateMode))
}

func (s *Server) updateStatus(w http.ResponseWriter, r *http.Request) {
	if s.Upd == nil {
		jerr(w, 503, "updates are not available")
		return
	}
	jsonOut(w, s.Upd.Status())
}

func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	if s.Upd == nil {
		jerr(w, 503, "updates are not available")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	jsonOut(w, s.Upd.Check(ctx, true))
}

func (s *Server) updateApply(w http.ResponseWriter, r *http.Request) {
	if s.Upd == nil {
		jerr(w, 503, "updates are not available")
		return
	}
	if err := s.Upd.Apply(false); err != nil {
		jerr(w, 409, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(s.Upd.Status())
}

func (s *Server) updateMode(w http.ResponseWriter, r *http.Request) {
	if s.Upd == nil {
		jerr(w, 503, "updates are not available")
		return
	}
	var in struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil {
		jerr(w, 400, "bad request")
		return
	}
	if err := s.Upd.SetMode(in.Mode); err != nil {
		jerr(w, 400, err.Error())
		return
	}
	jsonOut(w, s.Upd.Status())
}
