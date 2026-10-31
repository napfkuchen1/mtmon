// SPDX-License-Identifier: AGPL-3.0-only

package poller

import (
	"context"
	"fmt"
	"time"
)

// accessListPaths are the access-list menus of the Wi-Fi stacks: wifi (RouterOS 7.13+), CAPsMAN v1, legacy wireless.
var accessListPaths = []string{"/interface/wifi/access-list", "/caps-man/access-list", "/interface/wireless/access-list"}

// AccessList reads the Wi-Fi access-list rows of a device (read-only GETs, with their match-count) for the
// reject-loop hint of a disconnect burst. Every menu the device answers is included.
func (m *Manager) AccessList(device string) ([]map[string]string, error) {
	d, ok := m.Cfg.Device(device)
	if !ok {
		return nil, fmt.Errorf("unknown device %q", device)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	c := NewClient(d)
	var out []map[string]string
	var last error
	ok = false
	for _, p := range accessListPaths {
		rows, err := c.Get(ctx, p)
		if err != nil {
			last = err
			continue
		}
		ok = true
		for _, r := range rows {
			out = append(out, r)
		}
	}
	if !ok {
		return nil, last
	}
	return out, nil
}
