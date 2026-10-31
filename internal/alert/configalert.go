// SPDX-License-Identifier: AGPL-3.0-only

package alert

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// selfChangeWindow: a config change shortly after one mtmon applied itself (wizard, provisioning) is its own doing.
// The config check runs every few minutes, so the window is generous.
const selfChangeWindow = 30 * time.Minute

// ConfigChanged records that a device's configuration differs from its last snapshot (#46). It is a warning, an
// info while the device is in a maintenance window (someone is working on it on purpose).
func (e *Engine) ConfigChanged(device string, n int, by []string) {
	key := keyConfigChangedN
	if n == 1 {
		key = keyConfigChanged1
	}
	params := map[string]string{"dev": device, "n": strconv.Itoa(n)}
	sev, tail := "warning", ""
	if len(by) > 0 {
		tail += " – changed by: " + strings.Join(by, "; ")
	}
	if _, ok := e.St.Maintenance()[device]; ok {
		sev, tail = "info", tail+" (maintenance window)"
	}
	if name := e.plannedConfig(device, time.Now()); name != "" && sev == "warning" {
		sev, tail = "info", tail+fmt.Sprintf(" – planned: scheduler %s", name)
	}
	if ts := e.St.LastManifestTS(device); ts > 0 && time.Since(time.Unix(ts, 0)) < selfChangeWindow {
		sev, tail = "info", tail+" (made through mtmon)"
	}
	e.RaiseP("config_changed", device, sev, key, params, tail, 0)
}

// Message keys of config_changed (singular and plural are separate keys so the German text is right for 1 entry).
const (
	keyConfigChanged1 = "{dev}: configuration changed (1 entry) – see the device page, Config history"
	keyConfigChangedN = "{dev}: configuration changed ({n} entries) – see the device page, Config history"
)
