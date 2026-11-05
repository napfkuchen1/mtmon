// SPDX-License-Identifier: AGPL-3.0-only

package integration

import (
	"strings"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/api"
	"github.com/napfkuchen1/mtmon/internal/store"
)

func mixedAlert(st *store.Store) *store.Alert {
	alerts, _ := st.Alerts(200)
	for i := range alerts {
		if alerts[i].Kind == api.MixedStateAlert {
			return &alerts[i]
		}
	}
	return nil
}

// A rollback the router partly refuses: the cause, the undo with a valid command, a warning, and a retry that clears it (#343).
func TestFailedRollbackRaisesMixedStateAlert(t *testing.T) {
	r := newAPIRig(t)
	body := r.conn()
	body["name"], body["role"] = "gw-ro", "router"
	r.call("POST", "/api/devices/readonly", body, nil)
	before := r.rt.Snapshot()

	r.rt.FailMenu = "/ip/firewall/filter" // the new-connection rule cannot be added
	r.rt.FailSet = func(_ string, b map[string]any) bool { return b["log"] == "no" }
	var out struct {
		OK     bool `json:"ok"`
		Result struct {
			Error string `json:"error"`
			Steps []struct {
				OK  bool   `json:"ok"`
				Msg string `json:"msg"`
			} `json:"steps"`
			Rollback []struct {
				OK  bool   `json:"ok"`
				Cmd string `json:"cmd"`
			} `json:"rollback"`
		} `json:"result"`
		Manual string `json:"manual_script"`
	}
	req := map[string]any{"user": "admin", "pass": "adminpw", "enable": []string{"firewall_logs", "log_new"}, "log_new": true}
	r.call("POST", "/api/devices/gw-ro/features", req, &out)
	if out.OK {
		t.Fatalf("apply should fail: %+v", out)
	}
	if n := len(out.Result.Steps); n == 0 || out.Result.Steps[n-1].OK || out.Result.Steps[n-1].Msg == "" {
		t.Fatalf("no failing step with a cause: %+v", out.Result.Steps)
	}
	cmds := 0
	for _, s := range out.Result.Rollback {
		if !s.OK {
			cmds++
			if !strings.Contains(s.Cmd, "log=no") || strings.Contains(s.Cmd, `log=""`) {
				t.Fatalf("bad manual command %q", s.Cmd)
			}
		}
	}
	if cmds == 0 {
		t.Fatalf("no refused undo step: %+v", out.Result.Rollback)
	}
	if !strings.Contains(out.Manual, "log=no") || strings.Count(out.Manual, "firewall filter set") != cmds {
		t.Fatalf("manual script must list the %d failed steps only:\n%s", cmds, out.Manual)
	}
	a := mixedAlert(r.st)
	if a == nil || a.Severity != "warning" || a.Acked || a.MsgKey == "" || a.Params["n"] == "" {
		t.Fatalf("alert %+v", a)
	}

	// router accepts again: switching the feature off retries what is left, the alert clears, the router is clean
	r.rt.FailMenu, r.rt.FailSet = "", nil
	var off map[string]any
	r.call("POST", "/api/devices/gw-ro/features", map[string]any{"user": "admin", "pass": "adminpw", "disable": []string{"firewall_logs"}}, &off)
	if off["ok"] != true {
		t.Fatalf("retry: %+v", off)
	}
	if a := mixedAlert(r.st); a == nil || !a.Acked || a.Severity != "info" {
		t.Fatalf("alert after retry %+v", a)
	}
	if after := r.rt.Snapshot(); after != before {
		t.Fatalf("router not clean\nbefore %s\nafter  %s", before, after)
	}
}
