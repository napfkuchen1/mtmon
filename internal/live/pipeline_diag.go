// SPDX-License-Identifier: AGPL-3.0-only

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
	Why      string `json:"why,omitempty"` // reason when it is not plain transit, e.g. "tunnel outer"
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
	dedup   map[netip.Addr]int64 // flows dropped as a second copy of another exporter's flow, per exporter (#286)
	ignored []diagIgnored        // ring, oldest first once full
	next    int
	total   int64 // all flows seen
	attDrop int64 // dropped as transit while one side is in a network a router has on its WAN side (#297)

	vpnMatched, vpnSingle, vpnUnassigned int64  // replies into a tunnel, by how they were attributed (#287)
	vpnUnassignedBytes                   uint64 // bytes booked on "VPN (not attributable)"
	vpnConntrack, vpnLate                int64  // named by the connection table; matched by an upload that came later (also in vpnMatched)
	vpnHeldNow                           int64  // replies waiting in the buffer right now
	vpnHeldBytes                         uint64
}

// vpnReply counts a reply into a tunnel by how it was attributed.
func (d *pipeDiag) vpnReply(v vpnVerdict, bytes uint64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch v {
	case vpnMatched:
		d.vpnMatched++
	case vpnSinglePeer:
		d.vpnSingle++
	case vpnConntrack:
		d.vpnConntrack++
	case vpnUnassigned:
		d.vpnUnassigned++
		d.vpnUnassignedBytes += bytes
	}
}

// vpnLateMatch counts a reply whose upload arrived while it waited in the buffer.
func (d *pipeDiag) vpnLateMatch() {
	d.mu.Lock()
	d.vpnLate++
	d.mu.Unlock()
}

// vpnHeldState publishes the size of the buffer of waiting replies.
func (d *pipeDiag) vpnHeldState(n int, bytes uint64) {
	d.mu.Lock()
	d.vpnHeldNow, d.vpnHeldBytes = int64(n), bytes
	d.mu.Unlock()
}

func (d *pipeDiag) attachedDrop() {
	d.mu.Lock()
	d.attDrop++
	d.mu.Unlock()
}

// TransitAttached returns the flows dropped as transit that touch a router-attached network, and all flows seen.
func (p *Pipeline) TransitAttached() (dropped, total int64) {
	p.diag.mu.Lock()
	defer p.diag.mu.Unlock()
	return p.diag.attDrop, p.diag.total
}

// dedupDrop counts a flow of exporter that another router already reported (#286).
func (d *pipeDiag) dedupDrop(exp netip.Addr) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dedup == nil {
		d.dedup = map[netip.Addr]int64{}
	}
	if _, ok := d.dedup[exp]; ok || len(d.dedup) < diagMaxKeys {
		d.dedup[exp]++
	}
}

func (d *pipeDiag) note(r flow.Record, dir string, why ...string) {
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
	d.total++
	if dir != "x" {
		return
	}
	e := diagIgnored{At: time.Now().Unix(), Exporter: r.Exporter.String(), Src: r.Src.Unmap().String(), Dst: r.Dst.Unmap().String(),
		SrcPort: r.SrcPort, DstPort: r.DstPort, Proto: r.Proto, Bytes: r.Bytes, InIf: r.InIf, OutIf: r.OutIf}
	if len(why) > 0 {
		e.Why = why[0]
	}
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
		nets = append(nets, p.Cfg.LearnedNets()...) // the routers' own delegated IPv6 prefixes and LAN-side IPv4 networks, learned automatically
	}
	attached := []string{}
	if p.Cfg != nil {
		attached = p.Cfg.AttachedNets()
	}
	dd := map[string]int64{}
	for a, n := range d.dedup {
		dd[a.String()] = n
	}
	return map[string]any{"local_nets": nets, "by_interface": rows, "dedup_dropped": dd, "ignored_recent": ign, "attached_nets": attached, "transit_attached": d.attDrop,
		"vpn_replies_matched": d.vpnMatched, "vpn_replies_single_peer": d.vpnSingle, "vpn_replies_unassigned": d.vpnUnassigned, "vpn_replies_unassigned_bytes": d.vpnUnassignedBytes,
		"vpn_replies_conntrack": d.vpnConntrack, "vpn_replies_late_matched": d.vpnLate, "vpn_replies_held": d.vpnHeldNow, "vpn_replies_held_bytes": d.vpnHeldBytes,
		"note": "in_if/out_if are the router's interface indexes (since boot); dir x = dropped because neither address is in local_nets"}
}
