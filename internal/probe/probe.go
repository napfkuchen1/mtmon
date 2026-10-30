// Package probe measures the internet connection from mtmon itself: TCP connect time and loss to a few well-known
// anycast resolvers. It needs no router rights and no raw sockets (works in an unprivileged container). It answers
// "is the internet slow or my Wi-Fi?", which flow data cannot.
package probe

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// DefaultTargets are public anycast resolvers (host:port, TCP).
var DefaultTargets = []string{"1.1.1.1:443", "9.9.9.9:443", "8.8.8.8:443"}

// DefaultDNS are the names resolved through the system resolver (normally the router) every round. Stored as
// target "dns:<name>"; a failed lookup counts as loss, the time is the resolution time.
var DefaultDNS = []string{"example.com", "cloudflare.com"}

// Interval between rounds; every round probes each target once.
const Interval = 30 * time.Second

// Prober runs the probes and stores them.
type Prober struct {
	St      *store.Store
	Log     *slog.Logger
	Targets func() []string // nil: DefaultTargets
	Enabled func() bool     // nil: always on
	Dial    func(ctx context.Context, addr string) error
	Lookup  func(ctx context.Context, name string) error // nil: net.DefaultResolver
	DNS     func() []string                              // names to resolve; nil or empty: DefaultDNS
	HTTP    func() []string                              // URLs to request; nil or empty: none
	Get     func(ctx context.Context, url string) error  // nil: real HTTP request
}

func lookup(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := net.DefaultResolver.LookupHost(ctx, name)
	return err
}

// get requests url and counts it as answered when the server returns headers with a status below 500. The body is
// not read, redirects are not followed: the time is DNS + connect + TLS + first byte of the given address.
func get(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "mtmon-probe")
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// ValidHTTPTarget reports whether s is an absolute http(s) URL with a host.
func ValidHTTPTarget(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
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
	resolve := p.Lookup
	if resolve == nil {
		resolve = lookup
	}
	names := DefaultDNS
	if p.DNS != nil {
		if n := p.DNS(); len(n) > 0 {
			names = n
		}
	}
	var urls []string
	if p.HTTP != nil {
		for _, u := range p.HTTP() {
			if ValidHTTPTarget(u) {
				urls = append(urls, u)
			}
		}
	}
	fetch := p.Get
	if fetch == nil {
		fetch = get
	}
	done := make(chan struct{}, len(targets)+len(names)+len(urls))
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
	for _, n := range names {
		go func(n string) {
			defer func() { done <- struct{}{} }()
			start := time.Now()
			err := resolve(ctx, n)
			if ctx.Err() != nil {
				return
			}
			p.St.AddWANSample(start, "dns:"+n, err == nil, float64(time.Since(start).Microseconds())/1000)
		}(n)
	}
	for _, u := range urls {
		go func(u string) {
			defer func() { done <- struct{}{} }()
			start := time.Now()
			err := fetch(ctx, u)
			if ctx.Err() != nil {
				return
			}
			p.St.AddWANSample(start, "http:"+u, err == nil, float64(time.Since(start).Microseconds())/1000)
		}(u)
	}
	for range len(targets) + len(names) + len(urls) {
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
