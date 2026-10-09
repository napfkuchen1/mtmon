package live

import "testing"

func TestEUI64Merge(t *testing.T) {
	if got := eui64MAC("fe80::9a22:efff:fe7a:7927"); got != "98:22:EF:7A:79:27" {
		t.Fatalf("eui64MAC = %q", got)
	}
	for _, ip := range []string{"192.168.1.5", "2001:db8::1", "::ffff:10.0.0.1", "nonsense"} {
		if got := eui64MAC(ip); got != "" {
			t.Errorf("eui64MAC(%q) = %q, want empty", ip, got)
		}
	}
	h := NewHub()
	if h.MACFor("fe80::9a22:efff:fe7a:7927") != "" {
		t.Error("unknown client must not be attributed")
	}
	h.SetIPMap(map[string]string{"2001:db8::77": "AA:BB:CC:00:00:01"}, map[string]string{"98:22:EF:7A:79:27": "tv"})
	if got := h.MACFor("fd00::9a22:efff:fe7a:7927"); got != "98:22:EF:7A:79:27" {
		t.Errorf("EUI-64 address of a known client: %q", got)
	}
	if got := h.MACFor("2001:db8::77"); got != "AA:BB:CC:00:00:01" {
		t.Errorf("neighbor-table mapping: %q", got)
	}
}
