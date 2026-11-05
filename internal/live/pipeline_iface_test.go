// SPDX-License-Identifier: AGPL-3.0-only

package live

import (
	"net/netip"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/flow"
	"github.com/napfkuchen1/mtmon/internal/store"
)

func ifRec(src, dst string, in, out uint32, bytes uint64) flow.Record {
	now := time.Now()
	return flow.Record{Exporter: netip.MustParseAddr("192.168.0.1"), Src: netip.MustParseAddr(src), Dst: netip.MustParseAddr(dst),
		SrcPort: 40000, DstPort: 443, Proto: 6, Bytes: bytes, Packets: 2, Start: now, End: now, InIf: in, OutIf: out}
}

// Flows that enter on different interfaces give separate rollup rows per interface; for downloads the client
// side is the egress interface.
func TestPipelineRollupPerInterface(t *testing.T) {
	p := netPipe(t, "192.168.0.0/16")
	p.Cfg.Devices = append(p.Cfg.Devices, config.Device{Name: "r1", Addr: "192.168.0.1"})
	var missed []string
	p.IfMiss = func(dev string) { missed = append(missed, dev) }
	p.Handle([]flow.Record{
		ifRec("192.168.0.50", "93.184.216.34", 2, 1, 1000),   // upload from the bridge
		ifRec("192.168.10.60", "93.184.216.34", 10, 1, 500),  // upload from VLAN 10
		ifRec("93.184.216.34", "192.168.10.60", 1, 10, 7000), // download to VLAN 10
	})
	if err := p.St.Flush(); err != nil {
		t.Fatal(err)
	}
	lan, wan, _, err := p.St.IfaceUsage(time.Now().Add(-time.Hour).Unix(), true)
	if err != nil {
		t.Fatal(err)
	}
	got := map[uint32]store.IfaceRow{}
	for _, r := range lan {
		got[r.Idx] = r
	}
	if got[2].Up != 1000 || got[2].Dev != "r1" {
		t.Errorf("bridge: %+v", got[2])
	}
	if got[10].Up != 500 || got[10].Down != 7000 {
		t.Errorf("vlan 10: %+v", got[10])
	}
	if len(wan) != 1 || wan[0].Idx != 1 || wan[0].Up != 1500 || wan[0].Down != 7000 {
		t.Errorf("wan: %+v", wan)
	}
	if len(missed) != 1 || missed[0] != "r1" { // the names are not known yet: one rate-limited refresh request
		t.Errorf("refresh requests: %v", missed)
	}
	// with the interface list known there is no request any more
	if err := p.St.SetIfNames("r1", []store.IfName{{Idx: 1, Name: "ether1"}, {Idx: 2, Name: "bridge"}, {Idx: 10, Name: "vlan10"}}); err != nil {
		t.Fatal(err)
	}
	p.missAt = nil
	missed = nil
	p.Handle([]flow.Record{ifRec("192.168.0.50", "93.184.216.34", 2, 1, 10)})
	if len(missed) != 0 {
		t.Errorf("known interfaces must not ask for a refresh: %v", missed)
	}
	p.Handle([]flow.Record{ifRec("192.168.0.50", "93.184.216.34", 77, 1, 10)}) // a new VLAN appears
	if len(missed) != 1 {
		t.Errorf("an unknown index must ask for a refresh: %v", missed)
	}
}
