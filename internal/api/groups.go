// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/advice"
	"github.com/napfkuchen1/mtmon/internal/store"
)

func init() {
	routeHooks = append(routeHooks, func(s *Server, m *http.ServeMux, a func(http.HandlerFunc) http.HandlerFunc) {
		m.HandleFunc("GET /api/groups", a(s.groups))
		m.HandleFunc("PUT /api/groups/{name}", a(s.groupSave))
		m.HandleFunc("DELETE /api/groups/{name}", a(s.groupDelete))
		m.HandleFunc("POST /api/groups/presets", a(s.groupPresets))
		m.HandleFunc("PUT /api/clients/{mac}/group", a(s.clientGroup))
		m.HandleFunc("POST /api/clients/group", a(s.clientsGroup))
	})
}

// SuggestGroup names the group a client probably belongs in, from vendor and name hints: "IoT" for smart-home gadgets,
// "Servers" for NAS, servers, cameras and printers. "" = no idea. It is only a suggestion; the user decides.
func SuggestGroup(c store.Client) string {
	if c.MAC == "" || strings.HasPrefix(c.MAC, "VPN:") {
		return ""
	}
	ac := advice.Client{MAC: c.MAC, Label: c.Label, Hostname: c.Hostname, Vendor: c.Vendor}
	switch {
	case advice.IsIoT24(ac):
		return "IoT"
	case advice.ServerLike(ac):
		return "Servers"
	}
	return ""
}

// suggestedGroup is SuggestGroup for a client that has no group yet.
func suggestedGroup(c store.Client) string {
	if c.Group != "" {
		return ""
	}
	return SuggestGroup(c)
}

// groups: GET /api/groups – every group with its settings, client counts and the traffic of the last 24 h.
func (s *Server) groups(w http.ResponseWriter, r *http.Request) {
	gs, err := s.St.GroupStats(time.Now().Add(-24 * time.Hour).Unix())
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]any{"groups": gs, "presets": store.GroupPresets})
}

// groupSave: PUT /api/groups/{name} {new_alert, chatty_mult, spike_info, ignore_weak, watch_default} – create or update.
func (s *Server) groupSave(w http.ResponseWriter, r *http.Request) {
	g := store.Group{NewAlert: true, ChattyMult: 1}
	if !readJSON(w, r, &g) {
		return
	}
	g.Name = r.PathValue("name")
	if !store.ValidGroupName(g.Name) {
		jerr(w, 400, "group name: 1-32 characters, letters or digits first")
		return
	}
	if g.ChattyMult < 0 || g.ChattyMult > 1000 {
		jerr(w, 400, "chatty_mult must be between 0 and 1000")
		return
	}
	if err := s.St.SaveGroup(g); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	s.expireAdvice()
	jsonOut(w, map[string]bool{"ok": true})
}

func (s *Server) groupDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.St.DeleteGroup(r.PathValue("name")); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	s.expireAdvice()
	jsonOut(w, map[string]bool{"ok": true})
}

// groupPresets: POST /api/groups/presets – adds the missing standard groups (IoT, Family, Servers, Guests, Lab).
func (s *Server) groupPresets(w http.ResponseWriter, r *http.Request) {
	have, err := s.St.Groups()
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	exists := map[string]bool{}
	for _, g := range have {
		exists[g.Name] = true
	}
	n := 0
	for _, g := range store.GroupPresets {
		if !exists[g.Name] {
			if err := s.St.SaveGroup(g); err == nil {
				n++
			}
		}
	}
	jsonOut(w, map[string]int{"added": n})
}

// clientGroup: PUT /api/clients/{mac}/group {group} – "" removes the client from its group.
func (s *Server) clientGroup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Group string `json:"group"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if _, err := s.St.Client(r.PathValue("mac")); err != nil {
		jerr(w, 404, "unknown client")
		return
	}
	if err := s.St.SetClientGroup([]string{r.PathValue("mac")}, in.Group); err != nil {
		jerr(w, 400, err.Error())
		return
	}
	s.expireAdvice()
	jsonOut(w, map[string]bool{"ok": true})
}

// clientsGroup: POST /api/clients/group {macs, group} or {macs, auto: true} (each client gets the group suggested for
// it, clients without a suggestion are left alone).
func (s *Server) clientsGroup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MACs  []string `json:"macs"`
		Group string   `json:"group"`
		Auto  bool     `json:"auto"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.MACs) == 0 || len(in.MACs) > 1000 {
		jerr(w, 400, "1 to 1000 clients")
		return
	}
	n := 0
	if in.Auto {
		by := map[string][]string{}
		for _, m := range in.MACs {
			if c, err := s.St.Client(m); err == nil {
				if g := suggestedGroup(*c); g != "" {
					by[g] = append(by[g], m)
				}
			}
		}
		for g, macs := range by {
			if err := s.St.SetClientGroup(macs, g); err != nil {
				continue // the suggested group does not exist (yet)
			}
			n += len(macs)
		}
	} else {
		if err := s.St.SetClientGroup(in.MACs, in.Group); err != nil {
			jerr(w, 400, err.Error())
			return
		}
		n = len(in.MACs)
	}
	s.expireAdvice()
	jsonOut(w, map[string]int{"count": n})
}

// expireAdvice makes the next Suggestions request evaluate again (group settings change what is reported).
func (s *Server) expireAdvice() {
	s.adv.mu.Lock()
	s.adv.at = time.Time{}
	s.adv.mu.Unlock()
}
