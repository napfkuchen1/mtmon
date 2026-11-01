// SPDX-License-Identifier: AGPL-3.0-only

package live

import (
	"net/netip"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/flow"
)

func TestMonitoringTrafficIsRecognised(t *testing.T) {
	p := &Pipeline{
		HostAddr: func(ip string) bool { return ip == "192.168.1.9" },
		Owns:     func(ip string) bool { return ip == "192.168.1.1" || ip == "fe80::1" },
	}
	a := netip.MustParseAddr
	for _, c := range []struct {
		name     string
		src, dst string
		sp, dp   uint16
		want     bool
	}{
		{"REST poll", "192.168.1.9", "192.168.1.1", 40000, 443, true},
		{"reply", "192.168.1.1", "192.168.1.9", 443, 40000, true},
		{"netflow export", "192.168.1.1", "192.168.1.9", 50000, 2055, true},
		{"link-local", "192.168.1.9", "fe80::1", 40000, 8729, true},
		{"user to router web", "192.168.1.5", "192.168.1.1", 40000, 443, false},
		{"mtmon host browsing", "192.168.1.9", "203.0.113.9", 40000, 443, false},
		{"mtmon host to router, other port", "192.168.1.9", "192.168.1.1", 40000, 5000, false},
	} {
		if got := p.monitoring(flow.Record{SrcPort: c.sp, DstPort: c.dp}, a(c.src), a(c.dst)); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
