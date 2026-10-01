package flow

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

// ExporterStat is per-exporter liveness, used by the UI to confirm a new device really sends flows.
type ExporterStat struct {
	Last    time.Time
	Records uint64
}

// Collector listens on UDP and forwards decoded records to a handler.
type Collector struct {
	Addr    string
	Allow   func(netip.Addr) bool // exporter allowlist; nil = allow all
	Handler func([]Record)
	Dec     *Decoder

	Dropped atomic.Uint64 // datagrams from unknown exporters
	mu      sync.Mutex
	conn    *net.UDPConn
	seen    map[netip.Addr]*ExporterStat
}

func (c *Collector) touch(ip netip.Addr, n int) {
	c.mu.Lock()
	if c.seen == nil {
		c.seen = map[netip.Addr]*ExporterStat{}
	}
	st := c.seen[ip]
	if st == nil {
		st = &ExporterStat{}
		c.seen[ip] = st
	}
	st.Last, st.Records = time.Now(), st.Records+uint64(n)
	c.mu.Unlock()
}

// Exporter returns liveness for an exporter IP (zero value if never seen).
func (c *Collector) Exporter(ip netip.Addr) ExporterStat {
	c.mu.Lock()
	defer c.mu.Unlock()
	if st := c.seen[ip.Unmap()]; st != nil {
		return *st
	}
	return ExporterStat{}
}

func (c *Collector) Run(ctx context.Context) error {
	ua, err := net.ResolveUDPAddr("udp", c.Addr)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", ua)
	if err != nil {
		return err
	}
	_ = conn.SetReadBuffer(8 << 20)
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	go func() { <-ctx.Done(); conn.Close() }()
	if c.Dec == nil {
		c.Dec = NewDecoder()
	}
	buf := make([]byte, 65535)
	for {
		n, from, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		exp := from.Addr().Unmap()
		if c.Allow != nil && !c.Allow(exp) {
			c.Dropped.Add(1)
			continue
		}
		recs, _ := c.Dec.Decode(exp, buf[:n])
		c.touch(exp, len(recs))
		if len(recs) > 0 && c.Handler != nil {
			c.Handler(recs)
		}
	}
}

// LocalAddr returns the bound address (useful for tests with port 0).
func (c *Collector) LocalAddr() net.Addr {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	return c.conn.LocalAddr()
}
