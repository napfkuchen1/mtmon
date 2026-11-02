// SPDX-License-Identifier: AGPL-3.0-only

package addr

import (
	"net/netip"
	"testing"
)

func TestOutboundDenied(t *testing.T) {
	for _, c := range []struct {
		ip       string
		loopback bool
		deny     bool
	}{
		{"169.254.169.254", true, true}, {"169.254.170.2", true, true}, {"169.254.10.1", true, true},
		{"fe80::1", true, true}, {"fd00:ec2::254", true, true}, {"100.100.100.200", true, true},
		{"::ffff:169.254.169.254", true, true}, {"0.0.0.0", true, true}, {"224.0.0.1", true, true},
		{"127.0.0.1", false, true}, {"::1", false, true}, {"127.0.0.1", true, false},
		{"192.168.1.10", false, false}, {"fd12::1", false, false}, {"100.64.0.5", false, false}, {"93.184.216.34", false, false},
	} {
		err := OutboundDenied(netip.MustParseAddr(c.ip), c.loopback)
		if (err != nil) != c.deny {
			t.Errorf("%s loopback=%v: denied=%v, want %v (%v)", c.ip, c.loopback, err != nil, c.deny, err)
		}
	}
}
