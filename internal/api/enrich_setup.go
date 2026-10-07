package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/napfkuchen1/mtmon/internal/enrich"
)

type setupStep struct {
	What   string `json:"what"`
	Status string `json:"status"` // pending | running | ok | failed
	Error  string `json:"error"`
}

// enrichJob is the state of the one-click download of GeoIP / ASN / vendor data (Settings → Enrichment).
type enrichJob struct {
	mu      sync.Mutex
	running bool
	steps   []setupStep
	started time.Time
}

func (j *enrichJob) snapshot() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	steps := append([]setupStep{}, j.steps...)
	return map[string]any{"running": j.running, "steps": steps, "started": j.started.Unix()}
}

func (j *enrichJob) set(i int, status, err string) {
	j.mu.Lock()
	j.steps[i].Status, j.steps[i].Error = status, err
	j.mu.Unlock()
}

// enrichSetupState: GET /api/enrich/setup
func (s *Server) enrichSetupState(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, s.enr.snapshot())
}

// enrichSetupStart: POST /api/enrich/setup {what:["geo","asn","oui"]} – downloads in the background, hot-reloads.
// Only the fixed download sources in package enrich are contacted; nothing the client sends ends up in a URL.
func (s *Server) enrichSetupStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		What []string `json:"what"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil {
		jerr(w, 400, "bad request")
		return
	}
	if len(in.What) == 0 {
		in.What = []string{enrich.DataGeo, enrich.DataASN, enrich.DataOUI}
	}
	for _, k := range in.What {
		if k != enrich.DataGeo && k != enrich.DataASN && k != enrich.DataOUI {
			jerr(w, 400, "unknown dataset")
			return
		}
	}
	s.enr.mu.Lock()
	if s.enr.running {
		s.enr.mu.Unlock()
		jerr(w, 409, "a download is already running")
		return
	}
	s.enr.running, s.enr.started = true, time.Now()
	s.enr.steps = nil
	for _, k := range in.What {
		s.enr.steps = append(s.enr.steps, setupStep{What: k, Status: "pending"})
	}
	s.enr.mu.Unlock()
	go s.runEnrichSetup(in.What)
	jsonOut(w, s.enr.snapshot())
}

func (s *Server) runEnrichSetup(what []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	dir := filepath.Join(s.Cfg.DataDir, "geo")
	for i, k := range what {
		s.enr.set(i, "running", "")
		p, err := enrich.Fetch(ctx, dir, k)
		if err == nil {
			switch k {
			case enrich.DataGeo:
				if err = s.En.ReloadGeo(p, ""); err == nil {
					s.Cfg.GeoCityDB = p
				}
			case enrich.DataASN:
				if err = s.En.ReloadGeo("", p); err == nil {
					s.Cfg.GeoASNDB = p
				}
			case enrich.DataOUI:
				if err = s.En.LoadOUI(p); err == nil {
					s.Cfg.OUIFile = p
				}
			}
		}
		if err != nil {
			s.enr.set(i, "failed", err.Error())
			continue
		}
		s.enr.set(i, "ok", "")
	}
	s.enr.mu.Lock()
	s.enr.running = false
	s.enr.mu.Unlock()
}

// enrichReverseDNS: POST /api/enrich/reverse_dns {enabled} – runtime switch, persisted in the database.
func (s *Server) enrichReverseDNS(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil {
		jerr(w, 400, "bad request")
		return
	}
	s.En.SetReverseDNS(in.Enabled)
	s.Cfg.ReverseDNS = in.Enabled
	// stored in the database (data dir), not in config.json: the service user usually cannot write /etc/mtmon
	v := "0"
	if in.Enabled {
		v = "1"
	}
	saved := s.St.SetMeta("reverse_dns", v) == nil
	jsonOut(w, map[string]any{"enabled": in.Enabled, "persisted": saved})
}
