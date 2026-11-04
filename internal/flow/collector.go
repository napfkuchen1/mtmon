// SPDX-License-Identifier: AGPL-3.0-only

package flow

import (
	"context"
	"log/slog"
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

	Dropped      atomic.Uint64 // datagrams from unknown exporters
	QueueDropped atomic.Uint64 // datagrams dropped because decoding fell behind the socket (#294)
	rcvbuf       atomic.Int64  // effective kernel receive buffer in bytes (0 = unknown)
	seq          seqTracker
	mu           sync.Mutex
	conn         *net.UDPConn
	seen         map[netip.Addr]*ExporterStat
}

// queueLen bounds the datagrams waiting for the decoder; the socket loop never blocks on it.
const queueLen = 8192

// wantRcvbuf is the receive buffer asked for; below minRcvbuf a warning names the sysctl to raise.
const (
	wantRcvbuf = 8 << 20
	minRcvbuf  = 4 << 20
)

type dgram struct {
	exp netip.Addr
	b   []byte
}

// Loss returns the export-packet loss accounting per exporter and observation domain (#294).
func (c *Collector) Loss() []LossStat { return c.seq.stats() }

// RcvBuf is the effective kernel receive buffer in bytes (0 when unknown or not listening).
func (c *Collector) RcvBuf() int64 { return c.rcvbuf.Load() }

// process decodes one datagram and accounts its sequence number.
func (c *Collector) process(d dgram) {
	_, _, _, nt0 := c.Dec.Stats()
	recs, _ := c.Dec.Decode(d.exp, d.b)
	_, _, _, nt1 := c.Dec.Stats()
	c.seq.observe(d.exp, d.b, len(recs), nt1 != nt0)
	c.touch(d.exp, len(recs))
	if len(recs) > 0 && c.Handler != nil {
		c.Handler(recs)
	}
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
	_ = conn.SetReadBuffer(wantRcvbuf)
	if n := readRcvbuf(conn); n > 0 {
		c.rcvbuf.Store(int64(n))
		if n < minRcvbuf {
			slog.Warn("flow receive buffer is small, bursts will be dropped by the kernel", "bytes", n,
				"hint", "raise net.core.rmem_max on the host (e.g. 8388608)")
		}
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	go func() { <-ctx.Done(); conn.Close() }()
	if c.Dec == nil {
		c.Dec = NewDecoder()
	}
	// The socket loop only copies; one worker decodes. A slow handler no longer stalls the socket (#294).
	q := make(chan dgram, queueLen)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for d := range q {
			c.process(d)
		}
	}()
	defer func() { close(q); <-done }()
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
		select {
		case q <- dgram{exp, append([]byte(nil), buf[:n]...)}:
		default:
			c.QueueDropped.Add(1)
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
