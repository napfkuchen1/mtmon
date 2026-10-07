package api

import (
	"encoding/json"
	"net/http"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// clientsCleanup: POST /api/clients/cleanup {days, unused_days, keep_labeled, dry_run}. Session + same-origin
// protection come from the auth wrapper like every other mutating endpoint.
func (s *Server) clientsCleanup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Days        int  `json:"days"`
		UnusedDays  int  `json:"unused_days"`
		KeepLabeled bool `json:"keep_labeled"`
		DryRun      bool `json:"dry_run"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil {
		jerr(w, 400, "bad request")
		return
	}
	if in.Days < store.MinCleanupDays || in.Days > 3650 || in.UnusedDays < 0 || in.UnusedDays > 3650 {
		jerr(w, 400, "days must be between 0 and 3650")
		return
	}
	o := store.CleanupOpts{Days: in.Days, UnusedDays: in.UnusedDays, KeepLabeled: in.KeepLabeled, DryRun: in.DryRun}
	// managed devices: never remove a client that is (or sits on the address of) a configured router / AP
	for _, d := range s.Cfg.AllDevices() {
		o.ProtectIPs = append(o.ProtectIPs, d.Addr)
	}
	if ns, err := s.St.Neighbors(); err == nil {
		names := map[string]bool{}
		for _, d := range s.Cfg.AllDevices() {
			names[d.Name] = true
		}
		for _, n := range ns {
			if names[n.Ident] {
				o.ProtectMACs = append(o.ProtectMACs, n.MAC)
			}
		}
	}
	res, err := s.St.CleanupClients(o)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, res)
}

// featureRoutes registers the clean-up, app-detection endpoints.
func (s *Server) featureRoutes(m *http.ServeMux, a func(http.HandlerFunc) http.HandlerFunc) {
	m.HandleFunc("POST /api/clients/cleanup", a(s.clientsCleanup))
	m.HandleFunc("GET /api/clients/inbox", a(s.clientsInbox))
	m.HandleFunc("POST /api/clients/review", a(s.clientsReview))
	m.HandleFunc("GET /api/enrich/setup", a(s.enrichSetupState))
	m.HandleFunc("POST /api/enrich/setup", a(s.enrichSetupStart))
	m.HandleFunc("POST /api/enrich/reverse_dns", a(s.enrichReverseDNS))
	m.HandleFunc("GET /api/access", a(s.accessInfo))
	m.HandleFunc("PUT /api/access", a(s.accessSave))
	m.HandleFunc("POST /api/system/restart", a(s.systemRestart))
	m.HandleFunc("GET /api/tokens", a(s.tokensList))
	m.HandleFunc("POST /api/tokens", a(s.tokensCreate))
	m.HandleFunc("DELETE /api/tokens/{id}", a(s.tokensDelete))
	m.HandleFunc("PUT /api/clients/{mac}/icon", a(s.clientIcon))
	m.HandleFunc("PUT /api/devices/{name}", a(s.editDevice))
	m.HandleFunc("GET /api/devices/{name}/features", a(s.deviceFeaturesGet))
	m.HandleFunc("POST /api/devices/{name}/features", a(s.deviceFeaturesApply))
	m.HandleFunc("GET /api/diagnostics", a(s.diagnostics))
	m.HandleFunc("GET /api/search/index", a(s.searchIndex))
	m.HandleFunc("GET /api/export/clients", a(s.exportClients))
	m.HandleFunc("GET /api/clients/{mac}/apps", a(s.clientApps))
	m.HandleFunc("GET /api/apps", a(s.apps))
}
