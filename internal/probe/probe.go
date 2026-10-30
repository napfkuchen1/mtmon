// Package probe measures the internet connection from mtmon itself: TCP connect time and loss to a few well-known
// anycast resolvers. It needs no router rights and no raw sockets (works in an unprivileged container). It answers
// "is the internet slow or my Wi-Fi?", which flow data cannot.
package probe

import (
	"context"
	"errors"
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
	// Custom lists the own targets (Settings -> Internet); nil: none
	Custom func() []Custom
	// Fetch requests an own http target over IPv4 or IPv6 (fam 4 or 6); ErrNoAddr = no address of that family. nil: real request
	Fetch func(ctx context.Context, url string, insecure bool, fam int) error
	// Report gets every result of an own target (ok, short error text); the alert engine counts failures in a row
	Report func(target string, ok bool, errText string)
}

func lookup(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, name)
	if err == nil && len(addrs) > 0 {
		blocked := true
		for _, a := range addrs {
			if ip := net.ParseIP(a); ip == nil || !ip.IsUnspecified() {
				blocked = false
			}
		}
		if blocked {
			return ErrBlocked
		}
	}
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
	legacyGet := p.Get
	if legacyGet == nil {
		legacyGet = get
	}
	var custom []Custom
	if p.Custom != nil {
		custom = p.Custom()
	}
	ownFetch := p.Fetch
	if ownFetch == nil {
		ownFetch = fetch
	}
	keep := map[string]bool{}
	var jobs []func()
	probe := func(target string, report bool, run func() error) {
		keep[target] = true
		jobs = append(jobs, func() {
			start := time.Now()
			err := run()
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, ErrNoAddr) {
				p.St.SetWANStatus(target, store.WANStatus{Note: "no IPv6 address"})
				return
			}
			why := Reason(err)
			p.St.SetWANStatus(target, store.WANStatus{Err: why})
			p.St.AddWANSample(start, target, err == nil, float64(time.Since(start).Microseconds())/1000)
			if report && p.Report != nil {
				p.Report(target, err == nil, why)
			}
		})
	}
	for _, t := range targets {
		probe(t, false, func() error { return dialer(ctx, t) })
	}
	own := map[string]bool{}
	for _, c := range custom {
		if c.Kind == "dns" {
			own[c.Target] = true
			names = append(names, c.Target)
		}
	}
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		probe("dns:"+n, own[n], func() error { return resolve(ctx, n) })
	}
	for _, u := range urls {
		probe("http:"+u, false, func() error { return legacyGet(ctx, u) })
	}
	for _, c := range custom {
		if c.Kind != "http" || !ValidHTTPTarget(c.Target) {
			continue
		}
		for _, fam := range []int{4, 6} {
			probe(fmt.Sprintf("http%d:%s", fam, c.Target), true, func() error { return ownFetch(ctx, c.Target, c.Insecure, fam) })
		}
	}
	p.St.ForgetWANStatus(keep)
	done := make(chan struct{}, len(jobs))
	for _, j := range jobs {
		go func() { j(); done <- struct{}{} }()
	}
	for range jobs {
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
