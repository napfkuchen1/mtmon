// Package syslog receives RouterOS syslog (UDP) and parses firewall log lines.
package syslog

import (
	"context"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Event struct {
	TS     int64
	Device string
	Prefix string
	Chain  string
	InIf   string
	OutIf  string
	Proto  string
	Flags  string
	Src    string
	SPort  int
	Dst    string
	DPort  int
	NAT    string
	Len    int
	MAC    string
	CState string
}

var (
	reTopic = regexp.MustCompile(`(?:^|\s)firewall(?:,[\w-]+)*\s+(.*)$`)
	reMsg   = regexp.MustCompile(`^(?:(.*?)\s+)?([\w-]+): in:([^\s,]+) out:([^,]+),\s*` +
		`(?:connection-state:(\S+)\s+)?(?:src-mac ([0-9A-Fa-f:]{17}),\s*)?` +
		`proto ([\w-]+)(?: \(([^)]*)\))?,\s*` +
		`(\S+?)(?::(\d+))?->(\S+?)(?::(\d+))?(?:,\s*NAT \(([^)]*)\)->\S+)?,\s*len (\d+)`)
)

// ParseFirewall extracts a firewall log event from one syslog payload (any RouterOS syslog flavour:
// plain, or with <PRI> and BSD timestamp/hostname header).
func ParseFirewall(msg string) (Event, bool) {
	msg = strings.TrimSpace(msg)
	t := reTopic.FindStringSubmatch(msg)
	if t == nil {
		return Event{}, false
	}
	m := reMsg.FindStringSubmatch(strings.TrimSpace(t[1]))
	if m == nil {
		return Event{}, false
	}
	e := Event{Prefix: strings.TrimSpace(m[1]), Chain: m[2], InIf: m[3], OutIf: strings.TrimSpace(m[4]), CState: m[5],
		MAC: strings.ToUpper(m[6]), Proto: m[7], Flags: m[8], Src: m[9], Dst: m[11], NAT: m[13]}
	e.SPort, _ = strconv.Atoi(m[10])
	e.DPort, _ = strconv.Atoi(m[12])
	e.Len, _ = strconv.Atoi(m[14])
	return e, true
}

// Server listens for syslog datagrams.
type Server struct {
	Addr    string
	Allow   func(netip.Addr) string // returns device name or "" to drop
	Handler func([]Event)

	Received, Parsed, Unknown, Dropped atomic.Uint64
	conn                               net.PacketConn

	smu     sync.Mutex
	samples []Sample // newest last, at most maxSamples
}

// Sample is one syslog line mtmon could not turn into a firewall event.
type Sample struct {
	TS     int64  `json:"ts"`
	Device string `json:"device"`
	Reason string `json:"reason"` // "not-firewall" (other log topic) or "format" (firewall line in an unknown layout)
	Line   string `json:"line"`
}

const maxSamples = 8

func (s *Server) addSample(dev, line string) {
	reason := "not-firewall"
	if reTopic.MatchString(line) {
		reason = "format"
	}
	if len(line) > 300 {
		line = line[:300]
	}
	s.smu.Lock()
	s.samples = append(s.samples, Sample{TS: time.Now().Unix(), Device: dev, Reason: reason, Line: line})
	if len(s.samples) > maxSamples {
		s.samples = s.samples[len(s.samples)-maxSamples:]
	}
	s.smu.Unlock()
}

// Samples returns the most recent unreadable lines (newest first).
func (s *Server) Samples() []Sample {
	s.smu.Lock()
	defer s.smu.Unlock()
	out := make([]Sample, 0, len(s.samples))
	for i := len(s.samples) - 1; i >= 0; i-- {
		out = append(out, s.samples[i])
	}
	return out
}

func (s *Server) Listen() error {
	pc, err := net.ListenPacket("udp", s.Addr)
	if err != nil {
		return err
	}
	s.conn = pc
	return nil
}

func (s *Server) Bound() bool { return s.conn != nil }

// LocalAddr returns the bound address (tests use port 0).
func (s *Server) LocalAddr() net.Addr {
	if s.conn == nil {
		return nil
	}
	return s.conn.LocalAddr()
}

// Run reads until ctx is done. Listen must have been called.
func (s *Server) Run(ctx context.Context) error {
	if s.conn == nil {
		if err := s.Listen(); err != nil {
			return err
		}
	}
	go func() { <-ctx.Done(); s.conn.Close() }()
	buf := make([]byte, 4096)
	for {
		n, from, err := s.conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		s.Received.Add(1)
		ua, ok := from.(*net.UDPAddr)
		if !ok {
			continue
		}
		ip, _ := netip.AddrFromSlice(ua.IP)
		dev := s.Allow(ip)
		if dev == "" {
			s.Dropped.Add(1)
			continue
		}
		e, ok := ParseFirewall(string(buf[:n]))
		if !ok {
			s.Unknown.Add(1)
			s.addSample(dev, strings.TrimSpace(string(buf[:n])))
			continue
		}
		e.TS, e.Device = time.Now().Unix(), dev
		s.Parsed.Add(1)
		s.Handler([]Event{e})
	}
}
