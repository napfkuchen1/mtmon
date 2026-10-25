package live

import (
	"net/netip"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/flow"
)

func diagRec(src, dst string, in, out uint32) flow.Record {
	now := time.Now()
	return flow.Record{Exporter: netip.MustParseAddr("192.168.0.1"), Src: netip.MustParseAddr(src), Dst: netip.MustParseAddr(dst),
		SrcPort: 40000, DstPort: 443, Proto: 6, Bytes: 100, Packets: 1, InIf: in, OutIf: out, Start: now, End: now}
}

// Per interface and direction counters show which side of a connection arrives where; transit drops are kept.
func TestPipelineDiagnostics(t *testing.T) {
	p := dnsPipe(t)
	p.Handle([]flow.Record{
		diagRec("192.168.0.50", "93.184.216.34", 7, 3), // upload
		diagRec("93.184.216.34", "192.168.0.50", 3, 7), // download
		diagRec("93.184.216.34", "203.0.113.5", 3, 0),  // pre-NAT copy on the WAN side: dropped as transit
	})
	d := p.Diagnostics()
	rows, ok := d["by_interface"].([]diagRow)
	if !ok || len(rows) != 3 {
		t.Fatalf("by_interface: %T %v", d["by_interface"], d["by_interface"])
	}
	dirs := map[string]int64{}
	for _, r := range rows {
		dirs[r.Dir] += r.Flows
	}
	if dirs["u"] != 1 || dirs["d"] != 1 || dirs["x"] != 1 {
		t.Errorf("directions: %v", dirs)
	}
	ign := d["ignored_recent"].([]diagIgnored)
	if len(ign) != 1 || ign[0].Dst != "203.0.113.5" || ign[0].InIf != 3 {
		t.Errorf("ignored_recent: %+v", ign)
	}
	if nets := d["local_nets"].([]string); len(nets) == 0 {
		t.Error("local_nets missing")
	}
}

func TestPipelineDiagnosticsRingNewestFirstAndBounded(t *testing.T) {
	p := dnsPipe(t)
	for i := 0; i < diagIgnoredKeep+10; i++ {
		r := diagRec("8.8.8.8", "9.9.9.9", 1, 2)
		r.SrcPort = uint16(1000 + i)
		p.Handle([]flow.Record{r})
	}
	ign := p.Diagnostics()["ignored_recent"].([]diagIgnored)
	if len(ign) != diagIgnoredKeep || ign[0].SrcPort != uint16(1000+diagIgnoredKeep+9) || ign[len(ign)-1].SrcPort != 1010 {
		t.Fatalf("ring: len %d first %d last %d", len(ign), ign[0].SrcPort, ign[len(ign)-1].SrcPort)
	}
}
