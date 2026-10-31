// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/poller"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// Snapshot download and retention (#190). The download is a readable change log in RouterOS-like syntax, scrubbed of
// secrets and runtime values. It is not a restorable backup (use /export or a binary backup on the router for that).

// singleRow sections hold one fixed row: rendered as `set`, not `add`.
var singleRow = map[string]bool{"/ip/dns": true, "/system/identity": true, "/system/ntp/client": true, "/snmp": true}

// findByName sections list built-in rows that exist on every router: rendered as `set [ find name=X ]`.
var findByName = map[string]bool{"/ip/service": true, "/interface": true}

var plainValue = regexp.MustCompile(`^[A-Za-z0-9_./:@+*,%-]+$`)

func rosValue(v string) string {
	if v == "" {
		return `""`
	}
	if plainValue.MatchString(v) {
		return v
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "\n", `\n`, "\r", "").Replace(v) + `"`
}

func rosPath(sec string) string { return strings.ReplaceAll(sec, "/", " ")[1:] }

// renderConfig turns a snapshot into the download text. Secrets and runtime fields are removed again here, so old
// snapshots stored before a field was recognised as such stay clean.
func renderConfig(device string, ts time.Time, snap store.ConfigSnap) string {
	snap = scrubStored(snap)
	var b strings.Builder
	fmt.Fprintf(&b, "# mtmon configuration snapshot of %s, taken %s\n", device, ts.Format("2006-01-02 15:04:05 MST"))
	b.WriteString("# Change log, not a restorable backup: passwords, keys and runtime values are left out.\n")
	b.WriteString("# For a restorable copy use /export or a backup on the router itself.\n")
	known := map[string]bool{}
	var secs []string
	for _, s := range poller.ConfigSections {
		known[s] = true
		if _, ok := snap[s]; ok {
			secs = append(secs, s)
		}
	}
	var extra []string
	for s := range snap {
		if !known[s] {
			extra = append(extra, s)
		}
	}
	sort.Strings(extra)
	for _, sec := range append(secs, extra...) {
		rows := snap[sec]
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n/%s\n", rosPath(sec))
		for _, r := range rows {
			b.WriteString(renderRow(sec, r))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func renderRow(sec string, r map[string]string) string {
	keys := make([]string, 0, len(r))
	for k := range r {
		if k == ".id" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if (keys[i] == "name") != (keys[j] == "name") {
			return keys[i] == "name"
		}
		return keys[i] < keys[j]
	})
	var parts []string
	verb := "add"
	switch {
	case singleRow[sec]:
		verb = "set"
	case findByName[sec] && r["name"] != "":
		verb = "set [ find name=" + rosValue(r["name"]) + " ]"
		keys = without(keys, "name")
	}
	parts = append(parts, verb)
	for _, k := range keys {
		parts = append(parts, k+"="+rosValue(r[k]))
	}
	return strings.Join(parts, " ")
}

func without(keys []string, k string) []string {
	out := keys[:0:0]
	for _, x := range keys {
		if x != k {
			out = append(out, x)
		}
	}
	return out
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// configDownload: GET /api/devices/{name}/config/download?ts= – one stored snapshot as a text file.
func (s *Server) configDownload(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := s.Cfg.Device(name); !ok {
		jerr(w, http.StatusNotFound, "unknown device")
		return
	}
	ts, _ := strconv.ParseInt(r.URL.Query().Get("ts"), 10, 64)
	if ts == 0 {
		if t := s.St.ConfigSnapTimes(name); len(t) > 0 {
			ts = t[0]
		}
	}
	snap, err := s.St.ConfigSnapAt(name, ts)
	if err != nil || ts == 0 {
		jerr(w, http.StatusNotFound, "unknown snapshot")
		return
	}
	at := time.Unix(ts, 0)
	file := unsafeName.ReplaceAllString(name, "_") + "_" + at.Format("20060102-1504") + ".txt"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+`"`)
	w.Write([]byte(renderConfig(name, at, snap)))
}

// configKeepSave: PUT /api/config-history {keep_days}
func (s *Server) configKeepSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		KeepDays int `json:"keep_days"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.St.SetConfigKeepDays(in.KeepDays); err != nil {
		jerr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.St.PruneConfigSnaps(time.Now().Unix())
	jsonOut(w, map[string]any{"keep_days": s.St.ConfigKeepDays()})
}

// pruneConfigDaily drops expired snapshots once a day.
func (s *Server) pruneConfigDaily(now time.Time) {
	last, _ := strconv.ParseInt(s.St.GetMeta("cfgprune"), 10, 64)
	if now.Unix()-last < 86400 {
		return
	}
	s.St.SetMeta("cfgprune", strconv.FormatInt(now.Unix(), 10))
	s.St.PruneConfigSnaps(now.Unix())
}
