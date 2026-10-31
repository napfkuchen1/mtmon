// SPDX-License-Identifier: AGPL-3.0-only

package sysinfo

import "testing"

func TestReadings(t *testing.T) {
	if RSS() <= 0 {
		t.Skip("no /proc on this platform")
	}
	free, total, ok := Disk(t.TempDir())
	if !ok || free <= 0 || total < free {
		t.Errorf("disk: %d of %d (%v)", free, total, ok)
	}
	var c CPU
	if p := c.Percent(); p < 0 || p > 100*64 {
		t.Errorf("cpu: %v", p)
	}
}
