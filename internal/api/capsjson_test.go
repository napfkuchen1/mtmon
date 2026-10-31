// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"testing"
)

func TestCleanCaps(t *testing.T) {
	raw := json.RawMessage(`{"identity":"gw","wifi_ifs":null,"packages":["wifi"],"traffic_flow":{"enabled":true,"targets":null},
		"warnings":["FastTrack is active","This device already holds objects tagged “mtmon-managed” (earlier setup)."]}`)
	var m struct {
		WifiIfs  []any    `json:"wifi_ifs"`
		Bridges  []any    `json:"bridges"`
		Warnings []string `json:"warnings"`
		Flow     struct {
			Targets []string `json:"targets"`
		} `json:"traffic_flow"`
	}
	out := cleanCaps(raw, true)
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m.WifiIfs == nil || m.Bridges == nil || m.Flow.Targets == nil {
		t.Errorf("null lists: %s", out)
	}
	if len(m.Warnings) != 1 || m.Warnings[0] != "FastTrack is active" {
		t.Errorf("managed device must lose the earlier-setup warning: %v", m.Warnings)
	}
	json.Unmarshal(cleanCaps(raw, false), &m)
	if len(m.Warnings) != 2 {
		t.Errorf("other devices keep it: %v", m.Warnings)
	}
	if string(cleanCaps(nil, true)) != "" {
		t.Error("empty stays empty")
	}
}

func TestEnrichSnapshotHasNoZeroTime(t *testing.T) {
	j := &enrichJob{}
	if got := j.snapshot()["started"]; got != int64(0) {
		t.Errorf("started before any run: %v", got)
	}
}
