// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
)

// Actions (#319): one-time admin steps that a release note asks for and that mtmon can detect on its own. The list is
// computed from checks, never from text, and an item disappears as soon as its check passes. Admin only: it is in
// viewerDenied, so neither the viewer role nor an API token can read it.

type actionItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Link   string `json:"link"` // in-app route that fixes it
}

func init() {
	routeHooks = append(routeHooks, func(s *Server, m *http.ServeMux, a func(http.HandlerFunc) http.HandlerFunc) {
		m.HandleFunc("GET /api/actions", a(s.actionsGet))
	})
}

// forwardedHeaders: the request carries proxy headers (X-Forwarded-*).
func forwardedHeaders(r *http.Request) bool {
	return r.Header.Get("X-Forwarded-Host") != "" || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Forwarded-Proto") != ""
}

func (s *Server) actionsGet(w http.ResponseWriter, r *http.Request) {
	items := []actionItem{}
	// (a) the browser reaches mtmon through a proxy whose address mtmon does not trust: forwarded headers are ignored,
	// so origin and HTTPS detection fail. Loopback proxies are always trusted and never show up here.
	if forwardedHeaders(r) && !proxyTrusted(r) {
		items = append(items, actionItem{ID: "proxy_untrusted", Title: "Reverse proxy is not trusted",
			Detail: "Requests arrive through a proxy that is not in the trusted list, so mtmon ignores its X-Forwarded headers. Enter the proxy's address under Settings → Reverse proxy.",
			Link:   "/settings/system"})
	}
	// (b) the installed root update helper is older than this release needs
	if have, old := s.Upd.HelperOutdated(); old {
		items = append(items, actionItem{ID: "helper_outdated", Title: "Update helper is outdated",
			Detail: "The installed update helper is older than this version expects. In-app updates still work, but run the installer's update once to get the new helper.",
			Link:   "/settings/updates"})
		_ = have
	}
	jsonOut(w, map[string]any{"items": items})
}
