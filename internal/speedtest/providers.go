package speedtest

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// Cloudflare measures against speed.cloudflare.com (unofficial but long-standing endpoints /__down and /__up).
type Cloudflare struct {
	Base string // "https://speed.cloudflare.com"; tests point it at a local server
}

func (Cloudflare) Name() string { return "cloudflare" }

func (p Cloudflare) base() string {
	if p.Base != "" {
		return strings.TrimRight(p.Base, "/")
	}
	return "https://speed.cloudflare.com"
}

func (p Cloudflare) Run(ctx context.Context, o Opts) Result {
	res := Result{Server: strings.TrimPrefix(strings.TrimPrefix(p.base(), "https://"), "http://")}
	c := newClient(o.Family)
	defer c.CloseIdleConnections()
	start := time.Now()
	// latency = TCP connect time (one round trip); TLS and HTTP time would inflate it
	ping, jit, err := connectLatency(ctx, o.Family, hostPort(p.base()), 7)
	if err != nil {
		res.Err = trimErr(err)
		return res
	}
	res.PingMS, res.JitterMS = ping, jit
	per := o.MaxBytes / int64(o.Streams)
	dn, dd, err := download(ctx, c, o, func(int) string { return fmt.Sprintf("%s/__down?bytes=%d", p.base(), per) })
	res.Bytes += dn
	if err != nil {
		res.Err = "download: " + trimErr(err)
		res.Dur = time.Since(start)
		return res
	}
	res.DownBps = bps(dn, dd)
	un, ud, err := upload(ctx, c, o, p.base()+"/__up")
	res.Bytes += un
	if err != nil {
		res.Err = "upload: " + trimErr(err)
	} else {
		res.UpBps = bps(un, ud)
	}
	res.Dur = time.Since(start)
	return res
}

// Hetzner downloads the public test files of the Hetzner speed-test hosts (download only, no account, no binary).
type Hetzner struct {
	Location string // key of HetznerServers
	Base     string // tests only: full base URL instead of https://<host>
}

func (Hetzner) Name() string { return "hetzner" }

func (p Hetzner) Run(ctx context.Context, o Opts) Result {
	host := HetznerServers[p.Location]
	base := "https://" + host
	if p.Base != "" {
		base = strings.TrimRight(p.Base, "/")
	}
	res := Result{Server: host}
	if host == "" {
		res.Err = "unknown Hetzner location"
		return res
	}
	c := newClient(o.Family)
	defer c.CloseIdleConnections()
	start := time.Now()
	// 100 MB.bin is the smallest file that still outlasts a fast line for a few seconds; the byte and time caps cut it
	n, d, err := download(ctx, c, o, func(int) string { return base + "/100MB.bin" })
	res.Bytes = n
	res.Dur = time.Since(start)
	if err != nil {
		res.Err = "download: " + trimErr(err)
		return res
	}
	res.DownBps = bps(n, d)
	return res
}

// hostPort turns a base URL into host:port for a TCP connect.
func hostPort(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return base
	}
	if u.Port() == "" {
		if u.Scheme == "http" {
			return net.JoinHostPort(u.Hostname(), "80")
		}
		return net.JoinHostPort(u.Hostname(), "443")
	}
	return u.Host
}

func trimErr(err error) string {
	s := err.Error()
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}
