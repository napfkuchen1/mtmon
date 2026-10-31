// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestDeviceLogEndpoint(t *testing.T) {
	ts, s := newTestServer(t)
	s.Cfg.Devices = append(s.Cfg.Devices, config.Device{Name: "gw", Addr: "10.0.0.1", Role: "router"})
	now := time.Now().Unix()
	s.St.AddLogEvent(store.LogEvent{TS: now, Device: "gw", Topics: "system,info,account", Level: "info", Msg: "user admin logged in"})
	s.St.AddLogEvent(store.LogEvent{TS: now, Device: "gw", Topics: "system,error,critical", Level: "critical", Msg: "router rebooted"})
	_, c := login(t, ts, "test-password-1")
	get := func(q string) (int, []store.LogEvent) {
		r, _ := c.Get(ts.URL + "/api/devices/gw/log?range=1h" + q)
		var m struct {
			Events []store.LogEvent `json:"events"`
		}
		json.NewDecoder(r.Body).Decode(&m)
		return r.StatusCode, m.Events
	}
	if code, es := get(""); code != 200 || len(es) != 2 {
		t.Errorf("all: %d %+v", code, es)
	}
	if _, es := get("&level=error"); len(es) != 1 || es[0].Msg != "router rebooted" {
		t.Errorf("level: %+v", es)
	}
	if _, es := get("&q=ADMIN"); len(es) != 1 {
		t.Errorf("search: %+v", es)
	}
	if r, _ := c.Get(ts.URL + "/api/devices/nope/log"); r.StatusCode != 404 {
		t.Errorf("unknown device: %d", r.StatusCode)
	}
}
