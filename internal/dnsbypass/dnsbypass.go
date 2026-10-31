// SPDX-License-Identifier: AGPL-3.0-only

// Package dnsbypass finds clients that do not use the router's DNS: DNS-over-TLS (TCP 853), DNS-over-QUIC (UDP 853),
// DNS-over-HTTPS to well-known resolvers (443) and plain DNS to a public server that the router did not redirect
// (flows of redirected queries are already dropped by the pipeline). It needs only flow data, no router changes.
package dnsbypass

import (
	"net"
	"sort"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/advice"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// Types, in the order they are listed.
const (
	DoT = "DoT"
	DoQ = "DoQ"
	DoH = "DoH"
	DNS = "external DNS"
)

var typeOrder = map[string]int{DoT: 0, DoQ: 1, DoH: 2, DNS: 3}

const (
	minFlows    = 3  // a handful of connections: a one-off lookup is no pattern
	minFlowsDoH = 40 // apps use a DoH resolver now and then; a browser with secure DNS makes this many quickly
)

// Resolver is one external destination of a client.
type Resolver struct {
	IP    string `json:"ip"`
	Host  string `json:"host,omitempty"`
	Type  string `json:"type"`
	Flows int64  `json:"flows"`
	// Redirected: plain DNS whose queries the router redirects to itself (dst-nat with matched packets, for this
	// address family). The client gets the router's answers, so this is no bypass.
	Redirected bool `json:"redirected,omitempty"`
}

// Client is a client that bypasses the local DNS.
type Client struct {
	MAC       string     `json:"mac"`
	Types     []string   `json:"types"`
	Flows     int64      `json:"flows"`
	First     int64      `json:"first"`
	Last      int64      `json:"last"`
	Active    bool       `json:"active"`                  // seen within EndedAfter; false = ended, not a current problem
	Since     int64      `json:"checked_since,omitempty"` // time of the last "mark as fixed" (only later flows count)
	Resolvers []Resolver `json:"resolvers"`
	// Redirected: every resolver of the client is plain DNS that the router redirects. Listed, but not counted and not alerted.
	Redirected bool `json:"redirected,omitempty"`
}

// RedirectFunc says whether the router redirects plain DNS of a protocol (17 udp, 6 tcp) and address family.
type RedirectFunc func(proto int, v6, vpn bool) bool

// RedirectsFrom builds a RedirectFunc from the routers' probed capabilities: some router has an active rule with
// matched packets for that protocol and family.
func RedirectsFrom(devs []advice.Device) RedirectFunc {
	return func(proto int, v6, vpn bool) bool {
		for _, d := range devs {
			c := d.Caps
			if c == nil || (d.Role != "" && d.Role != "router") {
				continue
			}
			switch {
			case vpn && (proto == 17 && !v6 && c.DNSRedirectVPNUDP || proto == 6 && !v6 && c.DNSRedirectVPNTCP ||
				proto == 17 && v6 && c.DNSRedirectVPNUDP6 || proto == 6 && v6 && c.DNSRedirectVPNTCP6):
				return true
			case proto == 17 && !v6 && c.DNSRedirectUDP, proto == 6 && !v6 && c.DNSRedirectTCP,
				proto == 17 && v6 && c.DNSRedirectUDP6, proto == 6 && v6 && c.DNSRedirectTCP6:
				return true
			}
		}
		return false
	}
}

// Classify says which kind of DNS bypass a row is ("" = none).
func Classify(r store.DNSUseRow) string {
	if !advice.IsPublic(r.RIP) {
		return "" // a resolver inside the network (Pi-hole, AdGuard Home) is what we want
	}
	switch {
	case r.RPort == 853 && r.Proto == 17:
		return DoQ
	case r.RPort == 853:
		return DoT
	case r.RPort == 53:
		return DNS
	case r.RPort == 443:
		return DoH
	}
	return ""
}

// Detect lists the clients bypassing the local DNS since the given time, busiest first.
//
// ignore lists addresses that are not a resolver choice of the client even though they answer on 443: the WAN-quality
// probe of mtmon itself connects to public resolvers (1.1.1.1:443, 8.8.8.8:443 ...) and must not show up as DoH.
func Detect(st *store.Store, since int64, ignore ...string) ([]Client, error) {
	return DetectOpts(st, since, Options{Ignore: ignore})
}

// Options refine Detect: Resets are the "mark as fixed" times per MAC (only flows after them count), Now fixes the
// clock for the Active flag (zero = time.Now).
type Options struct {
	Ignore []string
	Skip   func(mac string) bool // true for a managed router/AP itself: its own DNS is no client bypass; optional
	// Redirected marks plain DNS that the router redirects to itself (not a real bypass); optional
	Redirected RedirectFunc
	Resets     map[string]int64
	Now        time.Time
}

// DetectOpts is Detect with per-client resets.
func DetectOpts(st *store.Store, since int64, o Options) ([]Client, error) {
	doh := advice.DoHSuffixes()
	rows, err := st.DNSUse(since, doh)
	if err != nil {
		return nil, err
	}
	// a client that was marked as fixed is read again from the hour after the reset
	byFrom := map[int64]map[string]bool{}
	for mac, ts := range o.Resets {
		if f := resetFrom(ts); f > since {
			if byFrom[f] == nil {
				byFrom[f] = map[string]bool{}
			}
			byFrom[f][mac] = true
		}
	}
	if len(byFrom) > 0 {
		reset := map[string]bool{}
		for _, macs := range byFrom {
			for m := range macs {
				reset[m] = true
			}
		}
		kept := rows[:0]
		for _, r := range rows {
			if !reset[r.MAC] {
				kept = append(kept, r)
			}
		}
		rows = kept
		for f, macs := range byFrom {
			again, err := st.DNSUse(f, doh)
			if err != nil {
				return nil, err
			}
			for _, r := range again {
				if macs[r.MAC] {
					rows = append(rows, r)
				}
			}
		}
	}
	if o.Skip != nil {
		kept := rows[:0]
		for _, r := range rows {
			if !o.Skip(r.MAC) {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	out := fromRowsRedirect(rows, o.Ignore, o.Redirected)
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	for i := range out {
		out[i].Active = out[i].Last >= now.Add(-EndedAfter).Unix()-3600 // Last is the start of an hourly bucket
		out[i].Since = o.Resets[out[i].MAC]
	}
	return out, nil
}

// ProbeHosts returns the host part of probe targets ("1.1.1.1:443" -> "1.1.1.1"), for Detect's ignore list.
func ProbeHosts(targets []string) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		if h, _, err := net.SplitHostPort(t); err == nil {
			t = h
		}
		out = append(out, t)
	}
	return out
}

func fromRows(rows []store.DNSUseRow, ignore []string) []Client {
	return fromRowsRedirect(rows, ignore, nil)
}

func fromRowsRedirect(rows []store.DNSUseRow, ignore []string, redir RedirectFunc) []Client {
	skip := make(map[string]bool, len(ignore))
	for _, ip := range ignore {
		skip[ip] = true
	}
	type agg struct {
		c     *Client
		flows map[string]int64 // per type, to apply the thresholds
	}
	by := map[string]*agg{}
	for _, r := range rows {
		typ := Classify(r)
		if typ == "" || (r.RPort == 443 && skip[r.RIP]) {
			continue
		}
		a := by[r.MAC]
		if a == nil {
			a = &agg{c: &Client{MAC: r.MAC, Types: []string{}, Resolvers: []Resolver{}}, flows: map[string]int64{}}
			by[r.MAC] = a
		}
		a.flows[typ] += r.Flows
		res := Resolver{IP: r.RIP, Host: r.Host, Type: typ, Flows: r.Flows}
		res.Redirected = typ == DNS && redir != nil && redir(r.Proto, strings.Contains(r.RIP, ":"), strings.HasPrefix(r.MAC, "VPN:"))
		a.c.Resolvers = append(a.c.Resolvers, res)
		if a.c.First == 0 || r.First < a.c.First {
			a.c.First = r.First
		}
		if r.Last > a.c.Last {
			a.c.Last = r.Last
		}
	}
	out := []Client{}
	for _, a := range by {
		for typ, n := range a.flows {
			min := int64(minFlows)
			if typ == DoH {
				min = minFlowsDoH
			}
			if n >= min {
				a.c.Types = append(a.c.Types, typ)
				a.c.Flows += n
			}
		}
		if len(a.c.Types) == 0 {
			continue
		}
		sort.Slice(a.c.Types, func(i, j int) bool { return typeOrder[a.c.Types[i]] < typeOrder[a.c.Types[j]] })
		keep := a.c.Resolvers[:0]
		for _, r := range a.c.Resolvers {
			for _, t := range a.c.Types {
				if r.Type == t {
					keep = append(keep, r)
					break
				}
			}
		}
		sort.Slice(keep, func(i, j int) bool { return keep[i].Flows > keep[j].Flows })
		if len(keep) > 8 {
			keep = keep[:8]
		}
		a.c.Resolvers = keep
		a.c.Redirected = len(keep) > 0
		for _, r := range keep {
			a.c.Redirected = a.c.Redirected && r.Redirected
		}
		out = append(out, *a.c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Redirected != out[j].Redirected {
			return !out[i].Redirected // real bypasses first
		}
		if out[i].Flows != out[j].Flows {
			return out[i].Flows > out[j].Flows
		}
		return out[i].MAC < out[j].MAC
	})
	return out
}
