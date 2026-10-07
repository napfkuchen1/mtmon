package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"regexp"
	"sync"
	"time"

	"github.com/napfkuchen1/mtmon/internal/advice"
	"github.com/napfkuchen1/mtmon/internal/poller"
	"github.com/napfkuchen1/mtmon/internal/provision"
)

// The suggestion rules are evaluated on demand and cached for adviceTTL; there is no background job.
const adviceTTL = 60 * time.Second

type adviceState struct {
	mu  sync.Mutex
	at  time.Time
	res advice.Result
}

var adviceID = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,119}$`)

func (s *Server) adviceRoutes(m *http.ServeMux, a func(http.HandlerFunc) http.HandlerFunc) {
	m.HandleFunc("GET /api/suggestions", a(s.suggestions))
	m.HandleFunc("GET /api/suggestions/summary", a(s.suggestionSummary))
	m.HandleFunc("POST /api/suggestions/{id}/dismiss", a(s.suggestionDismiss))
	m.HandleFunc("POST /api/suggestions/{id}/restore", a(s.suggestionRestore))
}

// adviceResult returns the cached evaluation (re-evaluated when older than adviceTTL or forced).
func (s *Server) adviceResult(force bool) advice.Result {
	s.maybeRefreshCaps()
	s.adv.mu.Lock()
	defer s.adv.mu.Unlock()
	if !force && !s.adv.at.IsZero() && time.Since(s.adv.at) < adviceTTL {
		return s.adv.res
	}
	l := advice.NewLive(s.St, s.Cfg, s.Hub, s.En, s.Started)
	if s.Mgr != nil {
		l.IsSelf = func(mac string) bool { _, ok := s.Mgr.SelfDevice(mac); return ok }
	}
	if s.Col != nil {
		l.ExporterLast = func(addr string) (time.Time, bool) {
			ip, err := netip.ParseAddr(addr)
			if err != nil {
				return time.Time{}, false
			}
			return s.Col.Exporter(ip).Last, true
		}
	}
	s.adv.res = advice.Evaluate(l)
	s.adv.at = time.Now()
	return s.adv.res
}

// capsTTL: stored router capabilities are re-read this often. They are written when a device is added or edited,
// so without a refresh a change made on the router later (Traffic Flow, logging, rules) produces false suggestions.
const capsTTL = time.Hour

// maybeRefreshCaps starts a background refresh when the stored capabilities are older than capsTTL.
func (s *Server) maybeRefreshCaps() {
	if s.Box == nil || time.Since(time.Unix(s.capsAt.Load(), 0)) < capsTTL || !s.capsRun.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.capsRun.Store(false)
		s.refreshCaps()
		s.capsAt.Store(time.Now().Unix())
		s.adv.mu.Lock()
		s.adv.at = time.Time{} // evaluate again with the fresh capabilities
		s.adv.mu.Unlock()
	}()
}

// refreshCaps re-probes every UI-managed device with its own (read-only) login and stores the result.
// A device that cannot be reached keeps its old capabilities.
func (s *Server) refreshCaps() {
	rows, err := s.St.DeviceRows()
	if err != nil {
		return
	}
	for _, row := range rows {
		d, ok := s.Cfg.Device(row.Name)
		if !ok {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		c, err := provision.Probe(ctx, poller.NewGuardedClient(d, s.addrAllowed))
		cancel()
		if err != nil {
			continue
		}
		if raw, err := json.Marshal(c); err == nil {
			s.St.SetDeviceCaps(row.Name, raw)
		}
	}
}

type suggestionOut struct {
	advice.Suggestion
	Hidden      bool  `json:"hidden"`
	HiddenUntil int64 `json:"hidden_until"` // 0 = hidden for good (when hidden)
}

func (s *Server) suggestions(w http.ResponseWriter, r *http.Request) {
	res := s.adviceResult(r.URL.Query().Get("refresh") == "1")
	dis, _ := s.St.DismissedSuggestions()
	items := make([]suggestionOut, 0, len(res.Items))
	counts := map[string]int{"warning": 0, "tip": 0, "info": 0, "hidden": 0}
	for _, it := range res.Items {
		o := suggestionOut{Suggestion: it}
		if u, ok := dis[it.ID]; ok {
			o.Hidden, o.HiddenUntil = true, u
			counts["hidden"]++
		} else {
			counts[it.Severity]++
		}
		items = append(items, o)
	}
	skipped := res.Skipped
	if skipped == nil {
		skipped = []advice.Skipped{}
	}
	jsonOut(w, map[string]any{"generated_at": res.GeneratedAt, "suggestions": items, "counts": counts, "skipped": skipped})
}

// suggestionSummary is the cheap badge for the navigation (reuses the cache).
func (s *Server) suggestionSummary(w http.ResponseWriter, r *http.Request) {
	res := s.adviceResult(false)
	dis, _ := s.St.DismissedSuggestions()
	warn, total := 0, 0
	for _, it := range res.Items {
		if _, hidden := dis[it.ID]; hidden {
			continue
		}
		total++
		if it.Severity == advice.SevWarning {
			warn++
		}
	}
	jsonOut(w, map[string]int{"warnings": warn, "total": total})
}

func (s *Server) suggestionDismiss(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !adviceID.MatchString(id) {
		jerr(w, 400, "bad suggestion id")
		return
	}
	var in struct {
		Days int `json:"days"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil {
			jerr(w, 400, "bad request")
			return
		}
	}
	if in.Days < 0 || in.Days > 3650 {
		jerr(w, 400, "bad request")
		return
	}
	if err := s.St.DismissSuggestion(id, in.Days); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]bool{"ok": true})
}

func (s *Server) suggestionRestore(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !adviceID.MatchString(id) {
		jerr(w, 400, "bad suggestion id")
		return
	}
	if err := s.St.RestoreSuggestion(id); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]bool{"ok": true})
}
