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

func dnsPipe(t *testing.T) *Pipeline {
	t.Helper()
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
	return NewPipeline(cfg, st, enrich.New("", "", "", false), NewHub())
}

func dnsRec(dst string, sport uint16) flow.Record {
	now := time.Now()
	return flow.Record{Src: netip.MustParseAddr("192.168.0.50"), Dst: netip.MustParseAddr(dst), SrcPort: sport, DstPort: 53,
		Proto: 17, Bytes: 80, Packets: 1, Start: now, End: now}
}

// A DNS query redirected by dst-nat is exported twice: to the original public server and to the router.
// Only the copy that went to the router counts, whatever the order or batch.
func TestDNSRedirectCountedOnce(t *testing.T) {
	p := dnsPipe(t)
	p.Handle([]flow.Record{dnsRec("8.8.8.8", 38209), dnsRec("192.168.0.1", 38209)}) // same batch
	if n := p.St.QueueLen(); n != 1 {
		t.Fatalf("same batch: %d rows", n)
	}
	p.Handle([]flow.Record{dnsRec("192.168.0.1", 40001)})
	p.Handle([]flow.Record{dnsRec("1.1.1.1", 40001)}) // later batch
	if n := p.St.QueueLen(); n != 2 {
		t.Fatalf("later batch: %d rows", n)
	}
	p.Handle([]flow.Record{dnsRec("8.8.8.8", 40002)}) // no redirect: genuine bypass stays visible
	if n := p.St.QueueLen(); n != 3 {
		t.Fatalf("unredirected: %d rows", n)
	}
}
