package alert

import "fmt"

// ConfigChanged records that a device's configuration differs from its last snapshot (#46). It is a warning, an
// info while the device is in a maintenance window (someone is working on it on purpose).
func (e *Engine) ConfigChanged(device string, n int) {
	sev, msg := "warning", fmt.Sprintf("%s: configuration changed (%d entries) – see the device page, Config history", device, n)
	if _, ok := e.St.Maintenance()[device]; ok {
		sev, msg = "info", msg+" (maintenance window)"
	}
	e.Raise("config_changed", device, sev, msg, 0)
}
