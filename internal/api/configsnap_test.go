// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// Snapshots stored before a field was recognised as a runtime value must not show it as a change (#133).
func TestConfigHistoryRefiltersStoredSnapshots(t *testing.T) {
	ts, s := newTestServer(t)
	s.Cfg.Devices = []config.Device{{Name: "gw", Role: "router", Addr: "10.0.0.1"}}
	a := store.ConfigSnap{"/system/scheduler": {{"name": "n", "interval": "1d", "next-run": "a", "run-count": "1"}}}
	b := store.ConfigSnap{"/system/scheduler": {{"name": "n", "interval": "1d", "next-run": "b", "run-count": "2"}}}
	s.St.SaveConfigSnap("gw", 100, a)
	s.St.SaveConfigSnap("gw", 200, b)
	_, c := login(t, ts, "test-password-1")
	resp, err := c.Get(ts.URL + "/api/devices/gw/config")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Changes []store.ConfigChange `json:"changes"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != 200 || len(out.Changes) != 0 {
		t.Fatalf("status %d, runtime-only differences must not be changes: %+v", resp.StatusCode, out.Changes)
	}
}

// #190: the download has no secrets and no runtime fields, even from snapshots stored before they were filtered.
func TestConfigDownloadScrubbed(t *testing.T) {
	ts, s := newTestServer(t)
	s.Cfg.Devices = []config.Device{{Name: "gw 1", Role: "router", Addr: "10.0.0.1"}}
	snap := store.ConfigSnap{
		"/interface/wireguard":       {{".id": "*1", "name": "wg0", "private-key": "SECRETKEY", "listen-port": "13231"}},
		"/interface/wireguard/peers": {{".id": "*2", "preshared-key": "PSK", "comment": "my peer", "token": "TOK"}},
		"/system/scheduler":          {{"name": "n", "interval": "1d", "next-run": "later", "run-count": "9"}},
		"/ip/dns":                    {{"servers": "1.1.1.1,9.9.9.9", "cache-used": "123"}},
		"/ip/service":                {{"name": "ssh", "port": "22"}},
	}
	s.St.SaveConfigSnap("gw 1", 1700000000, snap)
	_, c := login(t, ts, "test-password-1")
	resp, err := c.Get(ts.URL + "/api/devices/gw%201/config/download?ts=1700000000")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	body := string(b)
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), `attachment; filename="gw_1_`) {
		t.Fatalf("status %d, header %q", resp.StatusCode, resp.Header.Get("Content-Disposition"))
	}
	for _, bad := range []string{"SECRETKEY", "PSK", "TOK", "next-run", "run-count", "cache-used", ".id", "*1"} {
		if strings.Contains(body, bad) {
			t.Errorf("download leaks %q:\n%s", bad, body)
		}
	}
	for _, want := range []string{"/interface wireguard\nadd name=wg0 listen-port=13231", `comment="my peer"`, "/ip dns\nset servers=1.1.1.1,9.9.9.9", "set [ find name=ssh ] port=22"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q:\n%s", want, body)
		}
	}
	if r, _ := c.Get(ts.URL + "/api/devices/gw%201/config/download?ts=5"); r.StatusCode != 404 {
		t.Errorf("unknown snapshot: %d", r.StatusCode)
	}
}

func TestConfigKeepAPI(t *testing.T) {
	ts, s := newTestServer(t)
	_, c := login(t, ts, "test-password-1")
	put := func(body string) int {
		req, _ := http.NewRequest("PUT", ts.URL+"/api/config-history", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if put(`{"keep_days":3}`) != 400 || s.St.ConfigKeepDays() != 90 {
		t.Errorf("below the minimum must be refused, keep=%d", s.St.ConfigKeepDays())
	}
	if put(`{"keep_days":30}`) != 200 || s.St.ConfigKeepDays() != 30 {
		t.Errorf("keep=%d", s.St.ConfigKeepDays())
	}
}
