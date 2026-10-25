package api

import (
	"encoding/csv"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/napfkuchen1/mtmon/internal/speedtest"
)

// speedRanges are the windows the speed-test history can be viewed in.
var speedRanges = map[string]time.Duration{
	"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "90d": 90 * 24 * time.Hour, "365d": 365 * 24 * time.Hour,
}

func speedRange(r *http.Request) (string, int64) {
	rn := r.URL.Query().Get("range")
	d, ok := speedRanges[rn]
	if !ok {
		rn, d = "30d", speedRanges["30d"]
	}
	return rn, time.Now().Add(-d).Unix()
}

func (s *Server) speedRoutes(m *http.ServeMux, a func(http.HandlerFunc) http.HandlerFunc) {
	m.HandleFunc("GET /api/internet/speedtests", a(s.speedtests))
	m.HandleFunc("GET /api/internet/speedtests/export", a(s.speedtestsExport))
	m.HandleFunc("PUT /api/internet/speedtest/settings", a(s.speedtestSettings))
	m.HandleFunc("POST /api/internet/speedtest", a(s.speedtestRun))
	m.HandleFunc("POST /api/internet/speedtests/import", a(s.speedtestsImport))
}

// speedtests: GET /api/internet/speedtests?range=30d – settings, state, summary and the results (newest first, at
// most 1000). Read-only tokens may read this; starting a test and changing the settings need the admin session.
func (s *Server) speedtests(w http.ResponseWriter, r *http.Request) {
	rn, since := speedRange(r)
	items, err := s.St.Speedtests(since, 1000)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	var status speedtest.Status
	if s.Speed != nil {
		status = s.Speed.Status()
	}
	last := s.St.SpeedtestLastOK()
	var lastOut any
	if last.ID != 0 {
		lastOut = last
	}
	jsonOut(w, map[string]any{
		"range": rn, "settings": speedtest.LoadSettings(s.St), "status": status, "last": lastOut,
		"summary": s.St.SpeedtestSummary(since), "items": items,
		"hetzner_servers": hetznerKeys(),
	})
}

func hetznerKeys() []string {
	out := []string{}
	for _, k := range []string{"fsn1", "nbg1", "hel1", "ash", "hil", "sin"} {
		if _, ok := speedtest.HetznerServers[k]; ok {
			out = append(out, k)
		}
	}
	return out
}

// speedtestSettings: PUT /api/internet/speedtest/settings – the whole settings object.
func (s *Server) speedtestSettings(w http.ResponseWriter, r *http.Request) {
	in := speedtest.LoadSettings(s.St)
	if !readJSON(w, r, &in) {
		return
	}
	v, err := speedtest.SaveSettings(s.St, in)
	if err != nil {
		jerr(w, 400, err.Error())
		return
	}
	if s.Speed != nil {
		s.Speed.Reschedule()
	}
	jsonOut(w, v)
}

// speedtestRun: POST /api/internet/speedtest – start a round now (admin session; API tokens are GET-only).
func (s *Server) speedtestRun(w http.ResponseWriter, r *http.Request) {
	if s.Speed == nil {
		jerr(w, 503, "speed tests are not available")
		return
	}
	if err := s.Speed.RunNow(); err != nil {
		var ref speedtest.ErrRefused
		if errors.As(err, &ref) {
			jerr(w, 409, string(ref))
			return
		}
		jerr(w, 500, err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
	jsonOut(w, map[string]any{"running": true})
}

// speedtestsExport: GET /api/internet/speedtests/export?range=365d&format=csv|json.
func (s *Server) speedtestsExport(w http.ResponseWriter, r *http.Request) {
	_, since := speedRange(r)
	items, err := s.St.Speedtests(since, 0)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Disposition", `attachment; filename="mtmon-speedtests.json"`)
		jsonOut(w, items)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="mtmon-speedtests.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"time", "provider", "server", "family", "down_mbps", "up_mbps", "ping_ms", "jitter_ms", "bytes", "error"})
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }
	for _, t := range items {
		cw.Write([]string{time.Unix(t.TS, 0).UTC().Format(time.RFC3339), csvSafe(t.Provider), csvSafe(t.Server), t.Family,
			f(t.DownBps / 1e6), f(t.UpBps / 1e6), f(t.PingMS), f(t.JitterMS), strconv.FormatInt(t.Bytes, 10), csvSafe(t.Err)})
	}
	cw.Flush()
}

// speedtestsImport: POST /api/internet/speedtests/import – body is a MySpeed export (CSV or JSON, up to 8 MB).
// Importing the same file twice adds nothing the second time.
func (s *Server) speedtestsImport(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	if err != nil {
		jerr(w, 400, "file too large (8 MB at most)")
		return
	}
	rows, bad, err := speedtest.ParseMySpeed(data)
	if err != nil {
		jerr(w, 400, err.Error())
		return
	}
	added, dup, err := s.St.ImportSpeedtests(rows)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]int{"added": added, "duplicates": dup, "skipped": bad})
}
