// SPDX-License-Identifier: AGPL-3.0-only

package addr

import (
	"net/netip"
	"testing"
)

func TestClassify(t *testing.T) {
	for _, c := range []struct {
		in   string
		want Kind
	}{
		{"", Invalid}, {"nonsense", Invalid},
		{"0.0.0.0", Unspecified}, {"::", Unspecified},
		{"127.0.0.1", Loopback}, {"::1", Loopback},
		{"169.254.1.1", LinkLocal}, {"fe80::1", LinkLocal},
		{"224.0.0.251", Multicast}, {"ff02::1", Multicast},
		{"192.168.1.5", Private}, {"10.0.0.1", Private}, {"172.16.0.9", Private}, {"fd12:3456::1", Private},
		{"100.64.0.1", CGNAT}, {"100.127.255.254", CGNAT}, {"100.128.0.1", Public},
		{"8.8.8.8", Public}, {"2a00:1450::1", Public},
		{"::ffff:192.168.1.5", Private}, {"::ffff:8.8.8.8", Public},
		{" 8.8.8.8 ", Public},
	} {
		if got := ClassifyString(c.in); got != c.want {
			t.Errorf("ClassifyString(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestIsPublicAndLocal(t *testing.T) {
	for in, pub := range map[string]bool{"8.8.8.8": true, "100.64.0.1": false, "192.168.0.1": false, "0.0.0.0": false, "x": false, "2a00::1": true} {
		if IsPublic(in) != pub {
			t.Errorf("IsPublic(%q) = %v, want %v", in, !pub, pub)
		}
	}
	for in, loc := range map[string]bool{"192.168.0.1": true, "100.64.0.1": true, "224.0.0.1": true, "127.0.0.1": true, "8.8.8.8": false, "0.0.0.0": false} {
		if got := IsLocal(netip.MustParseAddr(in)); got != loc {
			t.Errorf("IsLocal(%q) = %v, want %v", in, got, loc)
		}
	}
	if IsLocal(netip.Addr{}) {
		t.Error("the zero address is not local")
	}
}

func TestIPv6Helpers(t *testing.T) {
	for in, want := range map[string]bool{"fd00::1": true, "fc00::1": true, "fe80::1": false, "2a00::1": false, "10.0.0.1": false, "": false} {
		if IsULA(in) != want {
			t.Errorf("IsULA(%q) != %v", in, want)
		}
	}
	for in, want := range map[string]bool{"2a00:1450::1": true, "2000::1": true, "fd00::1": false, "fe80::1": false, "1.2.3.4": false} {
		if IsGlobalIPv6(in) != want {
			t.Errorf("IsGlobalIPv6(%q) != %v", in, want)
		}
	}
	for in, want := range map[string]bool{"fd12::/64": true, "FE80::/64": true, "feb0::/64": true, "fec0::/64": false, "2a00::/56": false} {
		if IsULAOrLinkLocalPrefix(in) != want {
			t.Errorf("IsULAOrLinkLocalPrefix(%q) != %v", in, want)
		}
	}
	if !IsCGNAT(netip.MustParseAddr("::ffff:100.64.0.1")) || IsCGNAT(netip.MustParseAddr("100.63.255.255")) {
		t.Error("IsCGNAT boundaries")
	}
}
