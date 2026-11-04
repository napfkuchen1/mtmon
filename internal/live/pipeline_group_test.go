// SPDX-License-Identifier: AGPL-3.0-only

package live

import (
	"net/netip"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/enrich"
	"github.com/napfkuchen1/mtmon/internal/flow"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// #290: multicast and broadcast destinations queue no reverse-DNS lookup (a public destination does).
func TestMulticastSkipsLookups(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir(), NoTLS: true, LocalNets: []string{"192.168.0.0/16"}}
	cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	en := enrich.New("", "", "", true)
	p := NewPipeline(cfg, st, en, NewHub())
	now := time.Now()
	mk := func(dst string) flow.Record {
		return flow.Record{Src: netip.MustParseAddr("192.168.0.20"), Dst: netip.MustParseAddr(dst), SrcPort: 5353, DstPort: 5353,
			Proto: 17, Bytes: 200, Packets: 2, Start: now, End: now}
	}
	p.Handle([]flow.Record{mk("224.0.0.251"), mk("239.255.255.250"), mk("255.255.255.255"), mk("ff02::fb")})
	if n := en.Queued(); n != 0 {
		t.Fatalf("%d reverse lookups queued for group addresses", n)
	}
	p.Handle([]flow.Record{mk("93.184.216.34")})
	if n := en.Queued(); n != 1 {
		t.Fatalf("public destination: %d lookups queued, want 1", n)
	}
}
