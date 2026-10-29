package provision

import (
	"testing"

	"github.com/napfkuchen1/mtmon/internal/poller"
)

func TestDNSRedirectRuleCountsWithoutPackets(t *testing.T) {
	lan := func(n string) bool { return n == "bridge" }
	var d DNSRedirectInfo
	d.note(poller.Row{"chain": "dstnat", "protocol": "udp", "dst-port": "53", "action": "dst-nat", "packets": "0", "in-interface": "bridge"}, lan)
	if !d.UDP {
		t.Fatal("rule with zero packets must still count as redirect")
	}
	d.note(poller.Row{"chain": "dstnat", "protocol": "tcp", "dst-port": "53", "action": "dst-nat", "packets": "5", "in-interface": "back-to-home-vpn"}, lan)
	if d.TCP || !d.VPNTCP {
		t.Fatalf("tunnel-scoped rule: TCP=%v VPNTCP=%v, want false/true", d.TCP, d.VPNTCP)
	}
}
