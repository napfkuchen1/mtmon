// Package dnsbypass finds clients that do not use the router's DNS: DNS-over-TLS (TCP 853), DNS-over-QUIC (UDP 853),
// DNS-over-HTTPS to well-known resolvers (443) and plain DNS to a public server that the router did not redirect
// (flows of redirected queries are already dropped by the pipeline). It needs only flow data, no router changes.
package dnsbypass

import (
	"net"
	"sort"

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
}

// Client is a client that bypasses the local DNS.
type Client struct {
	MAC       string     `json:"mac"`
	Types     []string   `json:"types"`
	Flows     int64      `json:"flows"`
	First     int64      `json:"first"`
	Last      int64      `json:"last"`
	Resolvers []Resolver `json:"resolvers"`
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
	rows, err := st.DNSUse(since, advice.DoHSuffixes())
	if err != nil {
		return nil, err
	}
	return fromRows(rows, ignore), nil
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
		a.c.Resolvers = append(a.c.Resolvers, Resolver{IP: r.RIP, Host: r.Host, Type: typ, Flows: r.Flows})
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
		out = append(out, *a.c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Flows != out[j].Flows {
			return out[i].Flows > out[j].Flows
		}
		return out[i].MAC < out[j].MAC
	})
	return out
}
