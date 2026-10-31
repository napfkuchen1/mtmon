// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/napfkuchen1/mtmon/internal/enrich"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// ---- services ----

func (s *Server) services(w http.ResponseWriter, r *http.Request) {
	since, _, rn := parseRange(r)
	rows, err := s.St.Services(since)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	type row struct {
		store.ServiceRow
		Category string `json:"category"`
	}
	out := make([]row, 0, len(rows))
	for _, x := range rows {
		out = append(out, row{x, s.category(x.Name)})
	}
	jsonOut(w, map[string]any{"range": rn, "services": out, "reclassifying": s.reclass.Load()})
}

func (s *Server) category(svc string) string {
	if s.Cls != nil {
		return s.Cls.Category(svc)
	}
	return "Other"
}

func (s *Server) serviceDetail(w http.ResponseWriter, r *http.Request) {
	since, bucket, rn := parseRange(r)
	name := r.PathValue("name")
	d, err := s.St.ServiceDetail(name, since, bucket)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]any{"range": rn, "detail": d, "category": s.category(name)})
}

func (s *Server) ruleList(w http.ResponseWriter, r *http.Request) {
	rs, err := s.St.ServiceRules()
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, rs)
}

func (s *Server) rebuild(reset string) {
	if s.Cls == nil {
		return
	}
	rs, _ := s.St.ServiceRules()
	s.Cls.Set(rs)
	go func() {
		if !s.reclass.CompareAndSwap(false, true) {
			return
		}
		defer s.reclass.Store(false)
		for {
			cur, _ := s.St.ServiceRules()
			s.St.RebuildServices(enrich.OrderRules(cur), reset, func(p uint8, port uint16) string { return enrich.Service(p, port, 0) })
			reset = ""
			again, _ := s.St.ServiceRules()
			if len(again) == len(cur) {
				return
			}
		}
	}()
}

func (s *Server) ruleAdd(w http.ResponseWriter, r *http.Request) {
	var in store.ServiceRule
	if !readJSON(w, r, &in) {
		return
	}
	rule, err := enrich.ValidateRule(in)
	if err != nil {
		jerr(w, 400, err.Error())
		return
	}
	if all, _ := s.St.ServiceRules(); len(all) >= 500 {
		jerr(w, 400, "too many rules (max 500)")
		return
	}
	id, err := s.St.AddServiceRule(rule)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	s.rebuild("")
	rule.ID = id
	jsonOut(w, rule)
}

func (s *Server) rulePreview(w http.ResponseWriter, r *http.Request) {
	var in store.ServiceRule
	if !readJSON(w, r, &in) {
		return
	}
	rule, err := enrich.ValidateRule(in)
	if err != nil {
		jerr(w, 400, err.Error())
		return
	}
	c, d, b, f := s.St.RulePreview(rule, time.Now().Add(-30*24*time.Hour).Unix())
	jsonOut(w, map[string]any{"rule": rule, "clients": c, "dests": d, "bytes": b, "flows": f})
}

func (s *Server) ruleDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rs, _ := s.St.ServiceRules()
	name := ""
	for _, x := range rs {
		if x.ID == id {
			name = x.Name
		}
	}
	if name == "" {
		jerr(w, 404, "unknown rule")
		return
	}
	if err := s.St.DeleteServiceRule(id); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	s.rebuild(name)
	jsonOut(w, map[string]bool{"ok": true})
}
