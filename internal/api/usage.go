// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"net/http"
	"sort"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// usage: GET /api/usage/{what}?key=…&range=… lists the clients that use one row of the Top lists or one app:
// what = apps | services | hosts | ports | asn | country | protocols.
func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	since, _, rn := parseRange(r)
	what, key := r.PathValue("what"), r.URL.Query().Get("key")
	if key == "" {
		jerr(w, 400, "key is required")
		return
	}
	var cl []store.SvcClient
	var err error
	truncated := false
	if what == "apps" {
		cl, truncated, err = s.appClients(key, since)
	} else {
		cl, err = s.St.ClientsOf(what, key, since, 200)
	}
	if err != nil {
		jerr(w, 400, err.Error())
		return
	}
	var up, down int64
	for _, c := range cl {
		up, down = up+c.Up, down+c.Down
	}
	jsonOut(w, map[string]any{"range": rn, "what": what, "key": key, "clients": cl, "up": up, "down": down, "truncated": truncated})
}

// appClients sums the traffic of every client towards destinations that classify as the app with the given key.
func (s *Server) appClients(key string, since int64) ([]store.SvcClient, bool, error) {
	dests, err := s.St.AllClientDests(since, 0)
	if err != nil {
		return nil, false, err
	}
	by := map[string]*store.SvcClient{}
	seen := map[string]map[string]bool{}
	for _, d := range dests {
		if app, ok := classifyDest(d.DestAgg, s.cachedName); !ok || app.Key != key {
			continue
		}
		c := by[d.MAC]
		if c == nil {
			c = &store.SvcClient{MAC: d.MAC}
			by[d.MAC] = c
			seen[d.MAC] = map[string]bool{}
		}
		c.Up += d.Up
		c.Down += d.Down
		c.Flows += d.Flows
		if !seen[d.MAC][d.RIP] {
			seen[d.MAC][d.RIP] = true
			c.Dests++
		}
		if d.Last > c.LastSeen {
			c.LastSeen = d.Last
		}
	}
	known, _ := s.St.Clients()
	info := map[string]store.Client{}
	for _, c := range known {
		info[c.MAC] = c
	}
	out := make([]store.SvcClient, 0, len(by))
	for mac, c := range by {
		c.Label = mac
		if k, ok := info[mac]; ok {
			c.IP, c.Vendor, c.Device = k.IP, k.Vendor, k.Device
			switch {
			case k.Label != "":
				c.Label = k.Label
			case k.Hostname != "":
				c.Label = k.Hostname
			}
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Up+out[i].Down, out[j].Up+out[j].Down; a != b {
			return a > b
		}
		return out[i].MAC < out[j].MAC
	})
	if len(out) > 200 {
		out = out[:200]
	}
	return out, len(dests) >= 20*store.AppDestLimit, nil
}
