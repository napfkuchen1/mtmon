// Package probe measures the internet connection from mtmon itself: TCP connect time and loss to a few well-known
// anycast resolvers. It needs no router rights and no raw sockets (works in an unprivileged container). It answers
// "is the internet slow or my Wi-Fi?", which flow data cannot.
package probe

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// DefaultTargets are public anycast resolvers (host:port, TCP).
var DefaultTargets = []string{"1.1.1.1:443", "9.9.9.9:443", "8.8.8.8:443"}

// Interval between rounds; every round probes each target once.
const Interval = 30 * time.Second

// Prober runs the probes and stores them.
type Prober struct {
	St      *store.Store
	Log     *slog.Logger
	Targets func() []string // nil: DefaultTargets
	Enabled func() bool     // nil: always on
	Dial    func(ctx context.Context, addr string) error
}

func dial(ctx context.Context, addr string) error {
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return c.Close()
}

// Once probes every target one time (in parallel, so one slow target does not delay the others).
func (p *Prober) Once(ctx context.Context) {
	targets := DefaultTargets
	if p.Targets != nil {
		if t := p.Targets(); len(t) > 0 {
			targets = t
		}
	}
	dialer := p.Dial
	if dialer == nil {
		dialer = dial
	}
	done := make(chan struct{}, len(targets))
	for _, t := range targets {
		go func(t string) {
			defer func() { done <- struct{}{} }()
			start := time.Now()
			err := dialer(ctx, t)
			if ctx.Err() != nil {
				return
			}
			p.St.AddWANSample(start, t, err == nil, float64(time.Since(start).Microseconds())/1000)
		}(t)
	}
	for range targets {
		<-done
	}
}

// Run probes until ctx is done.
func (p *Prober) Run(ctx context.Context) {
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		if p.Enabled == nil || p.Enabled() {
			p.Once(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
