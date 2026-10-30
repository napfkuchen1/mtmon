package alert

import (
	"fmt"
	"strings"
	"time"
)

// selfChangeWindow: a config change shortly after one mtmon applied itself (wizard, provisioning) is its own doing.
// The config check runs every few minutes, so the window is generous.
const selfChangeWindow = 30 * time.Minute

// ConfigChanged records that a device's configuration differs from its last snapshot (#46). It is a warning, an
// info while the device is in a maintenance window (someone is working on it on purpose).
func (e *Engine) ConfigChanged(device string, n int, by []string) {
	count := fmt.Sprintf("%d entries", n)
	if n == 1 {
		count = "1 entry"
	}
	sev, msg := "warning", fmt.Sprintf("%s: configuration changed (%s) – see the device page, Config history", device, count)
	if len(by) > 0 {
		msg += " – changed by: " + strings.Join(by, "; ")
	}
	if _, ok := e.St.Maintenance()[device]; ok {
		sev, msg = "info", msg+" (maintenance window)"
	}
	if name := e.plannedConfig(device, time.Now()); name != "" && sev == "warning" {
		sev, msg = "info", msg+fmt.Sprintf(" – planned: scheduler %s", name)
	}
	if ts := e.St.LastManifestTS(device); ts > 0 && time.Since(time.Unix(ts, 0)) < selfChangeWindow {
		sev, msg = "info", msg+" (made through mtmon)"
	}
	e.Raise("config_changed", device, sev, msg, 0)
}
