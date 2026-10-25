package live

import (
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/napfkuchen1/mtmon/internal/flow"
)

// Diagnostics for "why is this direction missing": how many flows each exporter delivered per ingress/egress
// interface index and direction, and the last flows that were dropped as transit (both sides outside local_nets).
// Counters and addresses only, no payload. Read through GET /api/diagnostics.
const (
	diagIgnoredKeep = 60
	diagMaxKeys     = 300
)

type diagKey struct {
	exporter netip.Addr
	in, out  uint32
	dir      string // u, d, i or x (dropped as transit)
}

type diagIgnored struct {
	At       int64  `json:"at"`
	Exporter string `json:"exporter"`
	Src      string `json:"src"`
	Dst      string `json:"dst"`
	SrcPort  uint16 `json:"sport"`
	DstPort  uint16 `json:"dport"`
	Proto    uint8  `json:"proto"`
	Bytes    uint64 `json:"bytes"`
	InIf     uint32 `json:"in_if"`
	OutIf    uint32 `json:"out_if"`
}

type diagRow struct {
	Exporter string `json:"exporter"`
	InIf     uint32 `json:"in_if"`
	OutIf    uint32 `json:"out_if"`
	Dir      string `json:"dir"` // u upload, d download, i internal, x dropped as transit
	Flows    int64  `json:"flows"`
	Bytes    uint64 `json:"bytes"`
}

type pipeDiag struct {
	mu      sync.Mutex
	counts  map[diagKey]int64
	bytes   map[diagKey]uint64
	ignored []diagIgnored // ring, oldest first once full
	next    int
}

func (d *pipeDiag) note(r flow.Record, dir string) {
	k := diagKey{r.Exporter, r.InIf, r.OutIf, dir}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.counts == nil {
		d.counts, d.bytes = map[diagKey]int64{}, map[diagKey]uint64{}
	}
	if _, ok := d.counts[k]; !ok && len(d.counts) >= diagMaxKeys {
		return // a flood of odd interface indexes must not grow this without bound
	}
	d.counts[k]++
	d.bytes[k] += r.Bytes
	if dir != "x" {
		return
	}
	e := diagIgnored{At: time.Now().Unix(), Exporter: r.Exporter.String(), Src: r.Src.Unmap().String(), Dst: r.Dst.Unmap().String(),
		SrcPort: r.SrcPort, DstPort: r.DstPort, Proto: r.Proto, Bytes: r.Bytes, InIf: r.InIf, OutIf: r.OutIf}
	if len(d.ignored) < diagIgnoredKeep {
		d.ignored = append(d.ignored, e)
		return
	}
	d.ignored[d.next] = e
	d.next = (d.next + 1) % diagIgnoredKeep
}

// Diagnostics reports the per-interface counters and the most recent dropped (transit) flows, newest first.
func (p *Pipeline) Diagnostics() map[string]any {
	d := &p.diag
	d.mu.Lock()
	defer d.mu.Unlock()
	rows := []diagRow{}
	for k, n := range d.counts {
		rows = append(rows, diagRow{k.exporter.String(), k.in, k.out, k.dir, n, d.bytes[k]})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.Exporter != b.Exporter {
			return a.Exporter < b.Exporter
		}
		if a.InIf != b.InIf {
			return a.InIf < b.InIf
		}
		if a.OutIf != b.OutIf {
			return a.OutIf < b.OutIf
		}
		return a.Dir < b.Dir
	})
	ign := make([]diagIgnored, 0, len(d.ignored))
	for i := range d.ignored { // newest first
		ign = append(ign, d.ignored[(d.next+len(d.ignored)-1-i)%len(d.ignored)])
	}
	nets := []string{}
	if p.Cfg != nil {
		nets = append(nets, p.Cfg.LocalNets...)
		nets = append(nets, p.Cfg.LearnedNets()...) // the routers' own delegated IPv6 prefixes, learned automatically
	}
	return map[string]any{"local_nets": nets, "by_interface": rows, "ignored_recent": ign, "note": "in_if/out_if are the router's interface indexes (since boot); dir x = dropped because neither address is in local_nets"}
}
