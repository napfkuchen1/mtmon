package live

import (
	"net/netip"
	"sync"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/enrich"
	"github.com/napfkuchen1/mtmon/internal/flow"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// Pipeline turns raw flow records into enriched, client-attributed rows.
//
// Direction / dedup rules (documented in docs/architecture.md):
//   - src local, dst remote  → upload   (client = src)
//   - dst local, src remote  → download (client = dst)
//   - both local             → internal (client = src); deduped because a routed flow is exported
//     once per interface it crosses
//   - both remote            → ignored (NAT'd WAN-side copy or transit)
//
// Because only the local-IP side is counted, the post-NAT copy on the WAN interface is ignored
// automatically, so exporting with interfaces=all does not double-count internet traffic.
type Pipeline struct {
	Cfg               *config.Config
	St                *store.Store
	En                *enrich.Enricher
	Hub               *Hub
	Cls               *enrich.Classifier
	mu                sync.Mutex
	seen              map[dkey]int64
	dnsSeen           map[rkey]int64
	Ignored, Internal uint64
}

type dkey struct {
	s, d        netip.Addr
	sp, dp      uint16
	proto       uint8
	bytes, pkts uint64
	sec         int64
}

// rkey identifies a DNS query by client address, client port and protocol.
type rkey struct {
	s     netip.Addr
	sp    uint16
	proto uint8
}

func NewPipeline(c *config.Config, s *store.Store, e *enrich.Enricher, h *Hub) *Pipeline {
	return &Pipeline{Cfg: c, St: s, En: e, Hub: h, seen: map[dkey]int64{}, dnsSeen: map[rkey]int64{}}
}

// dnsRedirected reports whether r is the original-destination copy of a DNS query that the router redirected to
// itself (dst-nat): the same client port was also exported with a local destination on port 53. Only the copy
// that went to the router counts; the one to "8.8.8.8" never left the network.
// local lists the keys of redirected copies seen in the current batch.
func (p *Pipeline) dnsRedirected(r flow.Record, local map[rkey]bool, now int64) bool {
	if r.DstPort != 53 || (r.Proto != 17 && r.Proto != 6) {
		return false
	}
	k := rkey{r.Src.Unmap(), r.SrcPort, r.Proto}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Cfg.IsLocal(r.Dst.Unmap()) {
		p.dnsSeen[k] = now
		return false
	}
	if local[k] {
		return true
	}
	if t, ok := p.dnsSeen[k]; ok && now-t <= 10 {
		return true
	}
	if len(p.dnsSeen) > 20000 {
		for kk, t := range p.dnsSeen {
			if now-t > 30 {
				delete(p.dnsSeen, kk)
			}
		}
	}
	return false
}

// Handle is the flow.Collector handler.
func (p *Pipeline) Handle(recs []flow.Record) {
	now := time.Now()
	rows := make([]store.FlowRow, 0, len(recs))
	local := map[rkey]bool{}
	for _, r := range recs {
		if r.DstPort == 53 && p.Cfg.IsLocal(r.Src.Unmap()) && p.Cfg.IsLocal(r.Dst.Unmap()) {
			local[rkey{r.Src.Unmap(), r.SrcPort, r.Proto}] = true
		}
	}
	for _, r := range recs {
		src, dst := r.Src.Unmap(), r.Dst.Unmap()
		if p.dnsRedirected(r, local, now.Unix()) {
			continue
		}
		sl, dl := p.Cfg.IsLocal(src), p.Cfg.IsLocal(dst)
		var row store.FlowRow
		row.Exporter = r.Exporter.String()
		row.Proto, row.Bytes, row.Pkts, row.Flags = r.Proto, r.Bytes, r.Packets, r.TCPFlags
		ts := r.End
		if ts.IsZero() || ts.After(now.Add(time.Minute)) || ts.Before(now.Add(-2*time.Hour)) {
			ts = now
		}
		row.TS = ts.Unix()
		switch {
		case sl && !dl:
			row.Dir, row.CIP, row.RIP, row.CPort, row.RPort = "u", src.String(), dst.String(), r.SrcPort, r.DstPort
		case dl && !sl:
			row.Dir, row.CIP, row.RIP, row.CPort, row.RPort = "d", dst.String(), src.String(), r.DstPort, r.SrcPort
		case sl && dl:
			if p.dupInternal(r, row.TS) {
				continue
			}
			row.Dir, row.CIP, row.RIP, row.CPort, row.RPort = "i", src.String(), dst.String(), r.SrcPort, r.DstPort
		default:
			p.mu.Lock()
			p.Ignored++
			p.mu.Unlock()
			continue
		}
		row.MAC = p.Hub.MACFor(row.CIP)
		if row.MAC == "" {
			row.MAC = p.St.MACForIPAt(row.CIP, row.TS)
		}
		rip, _ := netip.ParseAddr(row.RIP)
		if !p.Cfg.IsLocal(rip) {
			row.Host = p.En.Name(rip)
			g := p.En.Lookup(rip)
			row.CC, row.ASN, row.ASOrg = g.CC, g.ASN, g.ASOrg
		} else {
			row.Host = p.En.Name(rip)
		}
		if p.Cls != nil {
			row.Svc = p.Cls.Classify(r.Proto, row.RPort, row.CPort, rip, row.Host, row.ASN)
		} else {
			row.Svc = enrich.Service(r.Proto, row.RPort, row.CPort)
		}
		rows = append(rows, row)
		p.Hub.AddFlow(row)
	}
	if len(rows) > 0 {
		p.St.Enqueue(rows...)
	}
}

func (p *Pipeline) dupInternal(r flow.Record, ts int64) bool {
	k := dkey{r.Src, r.Dst, r.SrcPort, r.DstPort, r.Proto, r.Bytes, r.Packets, ts / 5}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.seen[k]; ok {
		return true
	}
	p.seen[k] = ts
	p.Internal++
	if len(p.seen) > 50000 {
		for kk, t := range p.seen {
			if ts-t > 30 {
				delete(p.seen, kk)
			}
		}
	}
	return false
}

// IgnoredCount returns the number of transit (both-sides-remote) flows skipped.
func (p *Pipeline) IgnoredCount() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Ignored
}
