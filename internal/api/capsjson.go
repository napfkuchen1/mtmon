// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"strings"
)

// capsListKeys are the list fields of the stored device capabilities. Probes of older versions stored them as null
// (Go nil slices); the API never delivers null for a list.
var capsListKeys = []string{"packages", "wifi_ifs", "bridges", "addresses", "log_actions", "filter_rules", "warnings"}

// managedWarning is the probe hint for a device that already holds objects of an earlier mtmon setup. Only useful in
// the Add-device flow; for a device this instance manages (it holds those objects on purpose) it is dropped.
const managedWarning = "already holds objects tagged"

// cleanCaps makes stored capabilities safe for the UI: lists are [] instead of null, and the "already holds mtmon
// objects" warning is removed when managed is true.
func cleanCaps(raw json.RawMessage, managed bool) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	for _, k := range capsListKeys {
		if v, ok := m[k]; !ok || string(v) == "null" {
			m[k] = json.RawMessage("[]")
		}
	}
	if tf, ok := m["traffic_flow"]; ok {
		var f map[string]json.RawMessage
		if json.Unmarshal(tf, &f) == nil {
			if v, ok := f["targets"]; !ok || string(v) == "null" {
				f["targets"] = json.RawMessage("[]")
				if b, err := json.Marshal(f); err == nil {
					m["traffic_flow"] = b
				}
			}
		}
	}
	if managed {
		var ws []string
		if json.Unmarshal(m["warnings"], &ws) == nil {
			keep := []string{}
			for _, w := range ws {
				if !strings.Contains(w, managedWarning) {
					keep = append(keep, w)
				}
			}
			m["warnings"], _ = json.Marshal(keep)
		}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}
