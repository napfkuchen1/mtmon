// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/alert"
	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/live"
	"github.com/napfkuchen1/mtmon/internal/speedtest"
	"github.com/napfkuchen1/mtmon/internal/store"
	"github.com/napfkuchen1/mtmon/internal/update"
)

// errUpdateRestart makes run() shut down cleanly (DB flushed) and main() exit with update.ExitCode,
// which systemd's Restart=on-failure turns into a restart; the ExecStartPre helper then swaps the binary.
var errUpdateRestart = errors.New("restarting to install an update")

// newUpdater wires the self-update manager. MTMON_UPDATE_API / MTMON_UPDATE_REPO exist for tests and demos;
// a custom API base URL also becomes an allowed download host (it is set by whoever controls the service
// environment, i.e. root).
func newUpdater(cfg *config.Config, st *store.Store, hub *live.Hub, speed *speedtest.Runner, log *slog.Logger, restart func()) (*update.Manager, error) {
	o := update.Options{
		Repo: cfg.UpdateRepo, Current: version, Dir: filepath.Join(cfg.DataDir, "update"), Meta: st,
		HelperPath: update.HelperPath, Log: log, OnRestart: restart,
		Health:     func() error { return updateHealth(cfg, st, hub) },
		PreRestart: func() error { return st.Backup(filepath.Join(cfg.DataDir, "backups", "pre-update"), 2) },
		Busy: func() string {
			if n := st.OpenCritical(); n > 0 {
				return fmt.Sprintf("%d critical alert(s) open", n)
			}
			if speed != nil && speed.Status().Running { // a restart would cut the measurement short
				return "a speed test is running"
			}
			return ""
		},
	}
	if v := os.Getenv("MTMON_UPDATE_REPO"); v != "" {
		o.Repo = v
	}
	if v := os.Getenv("MTMON_UPDATE_API"); v != "" {
		u, err := url.Parse(v)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("MTMON_UPDATE_API: invalid URL %q", v)
		}
		o.API = v
		o.ExtraHosts = []string{u.Host}
		o.HelperPath = os.Getenv("MTMON_UPDATE_HELPER") // demos run without the root helper
	}
	return update.New(o)
}

// updateHealth is the health of a freshly updated mtmon: HTTP answers, the database opens and the data sources work
// (a router polls fine or flows arrived). Without configured routers only HTTP and the database count.
func updateHealth(cfg *config.Config, st *store.Store, hub *live.Hub) error {
	if err := localHealth(cfg); err != nil {
		return err
	}
	if err := st.DB.Ping(); err != nil {
		return fmt.Errorf("database: %w", err)
	}
	if len(cfg.AllDevices()) == 0 {
		return nil
	}
	for _, d := range hub.Devices() {
		if d.Up {
			return nil
		}
	}
	if hub.LastSeq() > 0 {
		return nil
	}
	return errors.New("no router polled successfully and no flows arrived")
}

// raiseUpdateFailed raises one update_failed warning when the last update was rolled back or refused.
func raiseUpdateFailed(u *update.Manager, al *alert.Engine) {
	rb := u.Status().LastRollbck
	if rb == nil || time.Since(time.Unix(rb.At, 0)) > 7*24*time.Hour {
		return
	}
	v := rb.Version
	if v == "" {
		v = "the new version"
	}
	al.RaiseP("update_failed", "mtmon", "warning", "Update to {v} failed and was rolled back", map[string]string{"v": v}, " – "+rb.Reason, 24*time.Hour)
}

// localHealth probes the running listener on loopback (same check as `mtmon selftest`).
func localHealth(cfg *config.Config) error {
	host := cfg.Listen
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	scheme := "https"
	if cfg.NoTLS {
		scheme = "http"
	}
	hc := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // loopback probe only
	resp, err := hc.Get(scheme + "://" + host + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("healthz returned %d", resp.StatusCode)
	}
	return nil
}
