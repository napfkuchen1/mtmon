package api

import (
	"net/http"
	"strings"
)

// Roles (#192): "admin" is the account from the config; "viewer" is one optional second account that may look at
// everything operational but not change anything and not see settings, the audit log, tokens or 2FA. Its login name
// and password hash live in the meta table, so it can be managed from the UI.

const (
	metaViewerUser = "viewer_user"
	metaViewerHash = "viewer_hash"
	roleViewer     = "viewer"
)

func init() {
	routeHooks = append(routeHooks, func(s *Server, m *http.ServeMux, a func(http.HandlerFunc) http.HandlerFunc) {
		m.HandleFunc("GET /api/viewer", a(s.viewerGet))
		m.HandleFunc("PUT /api/viewer", a(s.viewerSave))
		m.HandleFunc("DELETE /api/viewer", a(s.viewerDelete))
	})
}

// viewerDenied are read endpoints a viewer must not see: configuration, access, audit, tokens, updates, snapshots.
var viewerDenied = []string{
	"/api/audit", "/api/tokens", "/api/access", "/api/totp", "/api/viewer", "/api/notify", "/api/update",
	"/api/settings", "/api/config-history", "/api/enrich", "/api/timezone", "/api/system", "/api/alert-settings",
}

func viewerAllowed(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	p := r.URL.Path
	for _, d := range viewerDenied {
		if p == d || strings.HasPrefix(p, d+"/") {
			return false
		}
	}
	return !strings.Contains(p, "/config") && !strings.HasSuffix(p, "/manifest") && !strings.HasSuffix(p, "/offboard-script")
}

func (s *Server) sessionUser(tok string) string {
	if s.sess.roleOf(tok) == roleViewer {
		return s.St.GetMeta(metaViewerUser)
	}
	return s.Cfg.AdminUser
}

func (s *Server) viewerLoginOK(user, password string) bool {
	u, h := s.St.GetMeta(metaViewerUser), s.St.GetMeta(metaViewerHash)
	return u != "" && h != "" && user == u && CheckPassword(password, h)
}

func (s *Server) viewerGet(w http.ResponseWriter, r *http.Request) {
	u := s.St.GetMeta(metaViewerUser)
	jsonOut(w, map[string]any{"enabled": u != "" && s.St.GetMeta(metaViewerHash) != "", "user": u})
}

// viewerSave: PUT /api/viewer {user, password} – create the viewer or change its password.
func (s *Server) viewerSave(w http.ResponseWriter, r *http.Request) {
	var in struct{ User, Password string }
	if !readJSON(w, r, &in) {
		return
	}
	in.User = strings.TrimSpace(in.User)
	switch {
	case in.User == "" || len(in.User) > 40:
		jerr(w, http.StatusBadRequest, "user name needed (up to 40 characters)")
	case in.User == s.Cfg.AdminUser:
		jerr(w, http.StatusBadRequest, "the viewer needs its own user name")
	case len(in.Password) < 10:
		jerr(w, http.StatusBadRequest, "password must have at least 10 characters")
	default:
		h, err := HashPassword(in.Password)
		if err != nil {
			jerr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.St.SetMeta(metaViewerUser, in.User)
		s.St.SetMeta(metaViewerHash, h)
		jsonOut(w, map[string]any{"enabled": true, "user": in.User})
	}
}

func (s *Server) viewerDelete(w http.ResponseWriter, r *http.Request) {
	s.St.SetMeta(metaViewerUser, "")
	s.St.SetMeta(metaViewerHash, "")
	jsonOut(w, map[string]bool{"enabled": false})
}
