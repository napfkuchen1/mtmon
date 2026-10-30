package alert

import (
	"fmt"
	"time"
)

// selfChangeWindow: a config change shortly after one mtmon applied itself (wizard, provisioning) is its own doing.
// The config check runs every few minutes, so the window is generous.
const selfChangeWindow = 30 * time.Minute

// ConfigChanged records that a device's configuration differs from its last snapshot (#46). It is a warning, an
// info while the device is in a maintenance window (someone is working on it on purpose).
func (e *Engine) ConfigChanged(device string, n int) {
	sev, msg := "warning", fmt.Sprintf("%s: configuration changed (%d entries) – see the device page, Config history", device, n)
	if _, ok := e.St.Maintenance()[device]; ok {
		sev, msg = "info", msg+" (maintenance window)"
	}
	if ts := e.St.LastManifestTS(device); ts > 0 && time.Since(time.Unix(ts, 0)) < selfChangeWindow {
		sev, msg = "info", msg+" (made through mtmon)"
	}
	e.Raise("config_changed", device, sev, msg, 0)
}
