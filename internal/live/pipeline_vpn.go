// SPDX-License-Identifier: AGPL-3.0-only

package live

import (
	"net/netip"

	"github.com/napfkuchen1/mtmon/internal/flow"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// A VPN client behind a masquerading router (#287): the upload is exported with the peer as source, so it lands on the
// peer. The download is exported with the address the router translated to as destination, for example
// src=<server> dst=<router NAT address> out_if=<wg>: no client address anywhere, and the bytes would be dropped as
// transit. The egress interface says the bytes went into the tunnel, so they belong to a peer of that interface.
//
// Attribution, best evidence first:
//  1. an upload of a peer on that interface with the reversed tuple (server, server port, NAT port = peer port; the
//     router keeps the source port unless it collides)
//  2. the only peer that talked to this server and port on that interface recently (the router changed the port)
//  3. the only peer with a recent handshake on that interface
//  4. the client "VPN (not attributable)": the bytes stay visible and the diagnostics counter shows them
//
// An upload that arrives after its download (other batch) is missed by 1 and 2 and falls through to 3 or 4.

// VPNIface describes an exporter interface that is a WireGuard tunnel. Active lists, per peer with a recent handshake,
// its first host address of each address family.
type VPNIface struct {
	Active []netip.Addr
}

// vpnUploadWindow is how long, in seconds, an upload of a peer can explain a reply. Both directions of a flow are
// exported on the same timers, so the two records arrive close together.
const vpnUploadWindow = 600

// vpnKey identifies a peer's connection as the reply sees it. nat 0 is the entry for "any port" (step 2).
type vpnKey struct {
	dev        string
	ifx        uint32
	rip        netip.Addr
	rport, nat uint16
	proto      uint8
}

type vpnSeen struct {
	peer      netip.Addr
	ts        int64
	ambiguous bool // two peers talked to this server and port in the window
}

type vpnVerdict int

const (
	vpnNone       vpnVerdict = iota // not a reply into a tunnel
	vpnMatched                      // on the peer that sent the upload
	vpnSinglePeer                   // on the only active peer
	vpnUnassigned                   // on the client "VPN (not attributable)"
	vpnDuplicate                    // the exporter also reported the flow with the peer as destination
)

// routerOwned reports whether a managed router has this address configured.
func (p *Pipeline) routerOwned(a netip.Addr) bool {
	ip := a.String()
	return p.SelfMAC != nil && p.SelfMAC(ip) != "" || p.Owns != nil && p.Owns(ip)
}

// noteVPNUploads remembers which peer talks to which server, before the batch is booked: an upload and its reply
// often arrive in one batch, in either order.
func (p *Pipeline) noteVPNUploads(recs []flow.Record, ts int64) {
	if p.VPNIf == nil {
		return
	}
	for _, r := range recs {
		if r.InIf == 0 || r.InIf == r.OutIf {
			continue
		}
		dev := p.Cfg.SourceDevice(r.Exporter)
		if dev == "" {
			continue
		}
		if _, ok := p.VPNIf(dev, r.InIf); !ok {
			continue
		}
		src, dst := r.Src.Unmap(), r.Dst.Unmap()
		if p.Cfg.IsLocal(dst) || p.routerOwned(src) {
			continue // no translation towards a local server; the router's own traffic has no peer
		}
		p.mu.Lock()
		if p.vpnUp == nil {
			p.vpnUp = map[vpnKey]vpnSeen{}
		}
		p.vpnUp[vpnKey{dev, r.InIf, dst, r.DstPort, r.SrcPort, r.Proto}] = vpnSeen{peer: src, ts: ts}
		loose := vpnKey{dev, r.InIf, dst, r.DstPort, 0, r.Proto}
		v, ok := p.vpnUp[loose]
		live := ok && ts-v.ts <= vpnUploadWindow
		p.vpnUp[loose] = vpnSeen{peer: src, ts: ts, ambiguous: live && (v.ambiguous || v.peer != src)}
		if len(p.vpnUp) > 50000 {
			for k, v := range p.vpnUp {
				if ts-v.ts > vpnUploadWindow {
					delete(p.vpnUp, k)
				}
			}
		}
		p.mu.Unlock()
	}
}

// vpnReply decides whether r is a reply into a tunnel, and which peer it belongs to (see the top of this file).
func (p *Pipeline) vpnReply(r flow.Record, src, dst netip.Addr, sl bool, ts int64) (netip.Addr, vpnVerdict) {
	if p.VPNIf == nil || r.OutIf == 0 || sl || !p.routerOwned(dst) || p.routerOwned(src) {
		return netip.Addr{}, vpnNone
	}
	dev := p.Cfg.SourceDevice(r.Exporter)
	if dev == "" {
		return netip.Addr{}, vpnNone
	}
	info, ok := p.VPNIf(dev, r.OutIf)
	if !ok {
		return netip.Addr{}, vpnNone
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if v, ok := p.vpnUp[vpnKey{dev, r.OutIf, src, r.SrcPort, r.DstPort, r.Proto}]; ok && ts-v.ts <= vpnUploadWindow {
		if s, ok := p.xseen[xkey{r.Src, v.peer, r.SrcPort, r.DstPort, r.Proto}]; ok && ts-s.ts <= xWindow {
			return v.peer, vpnDuplicate
		}
		return v.peer, vpnMatched
	}
	if v, ok := p.vpnUp[vpnKey{dev, r.OutIf, src, r.SrcPort, 0, r.Proto}]; ok && !v.ambiguous && ts-v.ts <= vpnUploadWindow {
		return v.peer, vpnMatched
	}
	var only netip.Addr
	n := 0
	for _, a := range info.Active {
		if a.Is4() == dst.Is4() {
			only = a
			n++
		}
	}
	if n == 1 {
		return only, vpnSinglePeer
	}
	return netip.Addr{}, vpnUnassigned
}

// ensureVPNUnassigned creates the client the unattributable bytes are booked on, the first time it is needed.
func (p *Pipeline) ensureVPNUnassigned(dev string) {
	p.mu.Lock()
	done := p.vpnClient
	p.vpnClient = true
	p.mu.Unlock()
	if done {
		return
	}
	p.St.UpsertClients([]store.Observation{{MAC: store.VPNUnassignedMAC, Hostname: store.VPNUnassignedName, Vendor: store.VPNVendor, Device: dev}})
}
