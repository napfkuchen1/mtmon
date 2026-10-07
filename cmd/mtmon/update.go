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

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/store"
	"github.com/napfkuchen1/mtmon/internal/update"
)

// errUpdateRestart makes run() shut down cleanly (DB flushed) and main() exit with update.ExitCode,
// which systemd's Restart=on-failure turns into a restart; the ExecStartPre helper then swaps the binary.
var errUpdateRestart = errors.New("restarting to install an update")

// newUpdater wires the self-update manager. MTMON_UPDATE_API / MTMON_UPDATE_REPO exist for tests and demos;
// a custom API base URL also becomes an allowed download host (it is set by whoever controls the service
// environment, i.e. root).
func newUpdater(cfg *config.Config, st *store.Store, log *slog.Logger, restart func()) (*update.Manager, error) {
	o := update.Options{
		Repo: cfg.UpdateRepo, Current: version, Dir: filepath.Join(cfg.DataDir, "update"), Meta: st,
		HelperPath: update.HelperPath, Log: log, OnRestart: restart,
		Health: func() error { return localHealth(cfg) },
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
