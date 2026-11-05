// SPDX-License-Identifier: AGPL-3.0-only

package poller

import (
	"context"
	"errors"
	"net/netip"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/live"
)

// connProps are the fields of /ip/firewall/connection a lookup needs. RouterOS documents the ports as fields of their
// own while its print joins them to the address, so connHostPort accepts both forms.
const connProps = "src-address,reply-src-address,reply-dst-address,reply-src-port,reply-dst-port,protocol"

// connRetry is the pause after a router did not answer a lookup.
const connRetry = time.Minute

type connClient struct {
	d       config.Device
	c       *Client
	off     bool      // the login may not read the table: no more lookups until the device changes
	retryAt time.Time // the router did not answer
}

// VPNConn asks the router's connection table for the original source of a NAT connection, for a reply into a tunnel
// that no upload explains (#287). A plain GET filtered to one port, never the whole table.
func (m *Manager) VPNConn(dev string, q live.VPNConnQuery) (netip.Addr, bool) {
	d, ok := m.Cfg.Device(dev)
	if !ok {
		return netip.Addr{}, false
	}
	m.connMu.Lock()
	cc := m.conn[dev]
	if cc == nil || !reflect.DeepEqual(cc.d, d) {
		cc = &connClient{d: d, c: NewClient(d)}
		if m.conn == nil {
			m.conn = map[string]*connClient{}
		}
		m.conn[dev] = cc
	}
	skip := cc.off || time.Now().Before(cc.retryAt)
	m.connMu.Unlock()
	if skip {
		return netip.Addr{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	peer, err := connLookup(ctx, cc.c, q)
	if err != nil {
		m.connMu.Lock()
		if errors.Is(err, ErrAuth) {
			cc.off = true
		} else {
			cc.retryAt = time.Now().Add(connRetry)
		}
		m.connMu.Unlock()
		return netip.Addr{}, false
	}
	return peer, peer.IsValid()
}

// connLookup returns the invalid address when the table has no such connection. Every row is checked here as well:
// that the router applied the filter says nothing about the other fields of the row.
func connLookup(ctx context.Context, c *Client, q live.VPNConnQuery) (netip.Addr, error) {
	port := strconv.Itoa(int(q.NATPort))
	var firstErr error
	for _, filter := range []string{
		"reply-dst-port=" + port,
		"reply-dst-address=" + url.QueryEscape(q.NAT.String()+":"+port),
	} {
		rows, err := c.Get(ctx, "/ip/firewall/connection?"+filter+"&.proplist="+connProps)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if errors.Is(err, ErrAuth) {
				break
			}
			continue
		}
		if peer := connPeer(rows, q); peer.IsValid() {
			return peer, nil
		}
	}
	return netip.Addr{}, firstErr
}

// connPeer finds the connection of q in the rows and returns its original source.
func connPeer(rows []Row, q live.VPNConnQuery) netip.Addr {
	for _, r := range rows {
		nat, natPort := connHostPort(r["reply-dst-address"], r["reply-dst-port"])
		srv, srvPort := connHostPort(r["reply-src-address"], r["reply-src-port"])
		if nat != q.NAT || natPort != q.NATPort || srv != q.Server || srvPort != q.ServerPort || !connProto(r["protocol"], q.Proto) {
			continue
		}
		if src, _ := connHostPort(r["src-address"], ""); src.IsValid() {
			return src
		}
	}
	return netip.Addr{}
}

// connHostPort reads "1.2.3.4:80", or "1.2.3.4" with the port in a field of its own.
func connHostPort(addr, port string) (netip.Addr, uint16) {
	if ap, err := netip.ParseAddrPort(addr); err == nil {
		return ap.Addr().Unmap(), ap.Port()
	}
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return netip.Addr{}, 0
	}
	n, _ := strconv.ParseUint(port, 10, 16)
	return a.Unmap(), uint16(n)
}

// connProto compares the protocol, which RouterOS names ("tcp") or numbers. A row without it is judged by its addresses.
func connProto(v string, want uint8) bool {
	switch strings.ToLower(v) {
	case "":
		return true
	case "tcp":
		return want == 6
	case "udp":
		return want == 17
	}
	n, err := strconv.Atoi(v)
	return err == nil && n == int(want)
}
