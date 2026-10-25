package live

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/flow"
	"github.com/napfkuchen1/mtmon/internal/speedtest"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// The router exports mtmon's speed test as traffic of the mtmon host: it is labelled "Speed test", stays in the
// totals and the live list, and does not count as a client's own traffic (spike/chatty detection).
func TestSpeedtestTrafficIsLabelledAndNotClientRate(t *testing.T) {
	p := dnsPipe(t)
	l, _ := net.ResolveTCPAddr("tcp", "192.168.0.77:50000")
	r, _ := net.ResolveTCPAddr("tcp", "198.51.100.7:443")
	speedtest.Traffic.Note(l, r)
	now := time.Now()
	mk := func(rip string) flow.Record {
		return flow.Record{Src: netip.MustParseAddr("192.168.0.77"), Dst: netip.MustParseAddr(rip), SrcPort: 50000, DstPort: 443, Proto: 6,
			Bytes: 5_000_000, Packets: 4000, Start: now, End: now}
	}
	p.Handle([]flow.Record{mk("198.51.100.7"), mk("203.0.113.9")})
	snap := p.Hub.Snapshot(0, 10)
	var test, other bool
	for _, c := range snap.Conns {
		switch c.RIP {
		case "198.51.100.7":
			test = c.Svc == store.SpeedtestSvc
		case "203.0.113.9":
			other = c.Svc != store.SpeedtestSvc
		}
	}
	if !test || !other {
		t.Fatalf("labels: test=%v other=%v conns=%+v", test, other, snap.Conns)
	}
	if snap.UpBps == 0 {
		t.Error("the line total still counts the test")
	}
	if len(snap.Clients) != 1 || snap.Clients[0].UpBps >= snap.UpBps {
		t.Errorf("the client rate must exclude the test traffic: %+v total %.0f", snap.Clients, snap.UpBps)
	}
}
