package api

import (
	"net/http"
	"net/netip"
	"sort"
	"time"

	"github.com/daniel/mtmon/internal/enrich"
	"github.com/daniel/mtmon/internal/store"
)

// activeWindow is how recent traffic must be for an app to count as "active now". Rollups have
// one-minute buckets, so this covers the current and the previous minute.
const activeWindow = 120

// AppRow is one application in a result.
type AppRow struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
	Flows    int64  `json:"flows"`
	Dests    int    `json:"dests"`
	LastSeen int64  `json:"last_seen"`
	Active   bool   `json:"active"`
}

// AppsResult is the payload of the apps endpoints.
type AppsResult struct {
	Apps []AppRow `json:"apps"`
	// UnknownBytes is the traffic whose destination could not be attributed to an application
	// (no DNS name / unknown organisation). It is reported instead of guessing.
	UnknownBytes int64 `json:"unknown_bytes"`
	KnownBytes   int64 `json:"known_bytes"`
	// Truncated is set when more destinations existed than the per-request bound (the quiet tail).
	Truncated bool `json:"truncated"`
}

// classifyDests groups destinations by application. name resolves a destination address when the
// rollup row has no host (cache-only lookup).
func classifyDests(dests, recent []store.DestAgg, name func(netip.Addr) string, now int64) AppsResult {
	by := map[string]*AppRow{}
	var res AppsResult
	classify := func(d store.DestAgg) (enrich.App, bool) {
		host := d.Host
		if host == "" && name != nil {
			if a, err := netip.ParseAddr(d.RIP); err == nil {
				host = name(a.Unmap())
			}
		}
		return enrich.ClassifyApp(host, d.ASOrg, uint8(d.Proto), uint16(d.Port))
	}
	row := func(a enrich.App) *AppRow {
		r := by[a.Key]
		if r == nil {
			r = &AppRow{Key: a.Key, Name: a.Name, Category: a.Category}
			by[a.Key] = r
		}
		return r
	}
	for _, d := range dests {
		a, ok := classify(d)
		if !ok {
			res.UnknownBytes += d.Up + d.Down
			continue
		}
		r := row(a)
		r.Up += d.Up
		r.Down += d.Down
		r.Flows += d.Flows
		r.Dests++
		if d.Last > r.LastSeen {
			r.LastSeen = d.Last
		}
		res.KnownBytes += d.Up + d.Down
	}
	inList := map[string]bool{}
	for k := range by {
		inList[k] = true
	}
	for _, d := range recent {
		a, ok := classify(d)
		if !ok {
			continue
		}
		r := row(a)
		r.Active = true
		if !inList[a.Key] { // active but below the traffic cut-off of the main list
			r.Up += d.Up
			r.Down += d.Down
			r.Flows += d.Flows
			r.Dests++
		}
		if d.Last > r.LastSeen {
			r.LastSeen = d.Last
		}
	}
	res.Apps = make([]AppRow, 0, len(by))
	for _, r := range by {
		res.Apps = append(res.Apps, *r)
	}
	sort.Slice(res.Apps, func(i, j int) bool {
		a, b := res.Apps[i], res.Apps[j]
		if a.Active != b.Active {
			return a.Active
		}
		if ta, tb := a.Up+a.Down, b.Up+b.Down; ta != tb {
			return ta > tb
		}
		return a.Key < b.Key
	})
	res.Truncated = len(dests) >= store.AppDestLimit
	_ = now
	return res
}

func (s *Server) cachedName(a netip.Addr) string {
	if s.En == nil {
		return ""
	}
	return s.En.NameCached(a)
}

// clientApps: GET /api/clients/{mac}/apps?range=…
func (s *Server) clientApps(w http.ResponseWriter, r *http.Request) {
	mac := store.NormMAC(r.PathValue("mac"))
	if _, err := s.St.Client(mac); err != nil {
		jerr(w, 404, "unknown client")
		return
	}
	since, _, rn := parseRange(r)
	dests, err := s.St.ClientDests(mac, since, store.AppDestLimit)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	recent, err := s.St.ClientActiveDests(mac, activeWindow)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	res := classifyDests(dests, recent, s.cachedName, time.Now().Unix())
	jsonOut(w, map[string]any{"range": rn, "apps": res.Apps, "known_bytes": res.KnownBytes, "unknown_bytes": res.UnknownBytes,
		"truncated": res.Truncated, "categories": appCategoriesOf(res.Apps)})
}

// apps: GET /api/apps?range=… (Services page "Apps" view, from the busiest destinations)
func (s *Server) apps(w http.ResponseWriter, r *http.Request) {
	since, _, rn := parseRange(r)
	dests, err := s.St.TopDests(since, store.AppDestLimit)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	res := classifyDests(dests, nil, s.cachedName, time.Now().Unix())
	jsonOut(w, map[string]any{"range": rn, "apps": res.Apps, "known_bytes": res.KnownBytes, "unknown_bytes": res.UnknownBytes,
		"truncated": res.Truncated, "categories": appCategoriesOf(res.Apps)})
}

func appCategoriesOf(apps []AppRow) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, a := range apps {
		if !seen[a.Category] {
			seen[a.Category] = true
			out = append(out, a.Category)
		}
	}
	sort.Strings(out)
	return out
}
