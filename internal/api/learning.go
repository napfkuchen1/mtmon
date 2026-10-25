package api

import (
	"net/http"
	"time"
)

// learning: GET /api/learning – is a learning period running (after a reset or an update that changed detection)?
func (s *Server) learning(w http.ResponseWriter, r *http.Request) {
	until := s.St.LearningUntil()
	jsonOut(w, map[string]any{"active": until > 0, "until": until, "reason": s.St.LearningReason(),
		"baseline_since": s.St.BaselineSince()})
}

// learningReset: POST /api/learning/reset {hours} – "reset baselines only": traffic level and recurring-pattern
// history start from zero, a learning period begins; flows, clients, alerts and dismissed suggestions stay.
func (s *Server) learningReset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Hours int `json:"hours"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Hours <= 0 {
		in.Hours = 24
	}
	if in.Hours > 24*14 {
		jerr(w, 400, "at most 336 hours")
		return
	}
	s.St.StartLearning(time.Duration(in.Hours)*time.Hour, true, "baselines reset")
	if s.ResetBaseline != nil {
		s.ResetBaseline()
	}
	s.adv.mu.Lock()
	s.adv.at = time.Time{} // suggestions are judged again
	s.adv.mu.Unlock()
	s.learning(w, r)
}
