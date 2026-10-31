// SPDX-License-Identifier: AGPL-3.0-only

package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// MaxCustom is the number of own targets (Settings -> Internet).
const MaxCustom = 10

// Custom is one own target: a DNS name or an HTTP(S) URL. Insecure skips the certificate check of an https URL
// (for a server with a self-signed certificate; the target then measures reachability only).
type Custom struct {
	Kind     string `json:"kind"` // "dns" or "http"
	Target   string `json:"target"`
	Insecure bool   `json:"insecure,omitempty"`
}

var (
	// ErrNoAddr: the name has no address of the requested family (an IPv6 probe of a name without AAAA).
	ErrNoAddr = errors.New("no address of this family")
	// ErrBlocked: the resolver answered 0.0.0.0 or :: (a DNS filter on the router blocks the name).
	ErrBlocked = errors.New("blocked by the router (answer 0.0.0.0)")

	dnsName = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,63}$`)
)

// ValidateCustom checks and normalises a list of own targets: at most MaxCustom, no duplicates, DNS names or
// absolute http(s) URLs. The error names the first bad entry.
func ValidateCustom(in []Custom) ([]Custom, error) {
	if len(in) > MaxCustom {
		return nil, fmt.Errorf("at most %d targets", MaxCustom)
	}
	out := make([]Custom, 0, len(in))
	seen := map[string]bool{}
	for _, c := range in {
		c.Target = strings.TrimSpace(c.Target)
		switch c.Kind {
		case "dns":
			c.Target = strings.ToLower(strings.TrimSuffix(c.Target, "."))
			if !dnsName.MatchString(c.Target) {
				return nil, fmt.Errorf("not a DNS name: %s", c.Target)
			}
			c.Insecure = false
		case "http":
			if !ValidHTTPTarget(c.Target) {
				return nil, fmt.Errorf("not an http(s) URL: %s", c.Target)
			}
			if u, _ := url.Parse(c.Target); u.Scheme != "https" {
				c.Insecure = false
			}
		default:
			return nil, fmt.Errorf("unknown kind: %s", c.Kind)
		}
		k := c.Kind + c.Target
		if seen[k] {
			return nil, fmt.Errorf("duplicate target: %s", c.Target)
		}
		seen[k] = true
		out = append(out, c)
	}
	return out, nil
}

// Reason turns a probe error into the short text the Internet page shows.
func Reason(err error) string {
	var de *net.DNSError
	var ue *url.Error
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrBlocked):
		return "blocked by the router (answer 0.0.0.0)"
	case errors.As(err, &de) && de.IsNotFound:
		return "NXDOMAIN"
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &de) && de.IsTimeout):
		return "timeout"
	case strings.HasPrefix(err.Error(), "status "):
		return "HTTP " + strings.TrimPrefix(err.Error(), "status ")
	}
	msg := err.Error()
	if errors.As(err, &ue) {
		msg = ue.Err.Error()
	}
	if strings.Contains(msg, "tls:") || strings.Contains(msg, "x509:") {
		return "TLS error: " + msg
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	return msg
}

// Keys are the probe keys under which c is measured and reported: dns:<name>, or http4:/http6:<url>.
func Keys(c Custom) []string {
	if c.Kind == "dns" {
		return []string{"dns:" + c.Target}
	}
	return []string{"http4:" + c.Target, "http6:" + c.Target}
}

// fqdn makes a host name absolute (trailing dot) so the resolver neither appends a search domain nor tries the name
// under it first: a dead name must be NXDOMAIN, not whatever a wildcard record of the local domain answers (#194).
func fqdn(host string) string {
	if host == "" || net.ParseIP(host) != nil || strings.HasSuffix(host, ".") {
		return host
	}
	return host + "."
}

// resolveFam resolves host to addresses of one family (4 or 6). 0.0.0.0 / :: answers are a router block.
func resolveFam(ctx context.Context, host string, fam int) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if (fam == 4) != (ip.To4() != nil) {
			return nil, ErrNoAddr
		}
		return []net.IP{ip}, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, fqdn(host))
	if err != nil {
		return nil, err
	}
	var out []net.IP
	blocked := 0
	for _, a := range addrs {
		if (fam == 4) != (a.IP.To4() != nil) {
			continue
		}
		if a.IP.IsUnspecified() {
			blocked++
			continue
		}
		out = append(out, a.IP)
	}
	if len(out) == 0 {
		if blocked > 0 {
			return nil, ErrBlocked
		}
		return nil, ErrNoAddr
	}
	return out, nil
}

// fetch requests rawURL over IPv4 or IPv6 only (fam 4 or 6): time to the response headers, redirects not followed
// (a 3xx answer is a reachable server), status below 500 = answered.
func fetch(ctx context.Context, rawURL string, insecure bool, fam int) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	ips, err := resolveFam(ctx, u.Hostname(), fam)
	if err != nil {
		return err
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var last error
			for _, ip := range ips {
				c, e := d.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
				if e == nil {
					return c, nil
				}
				last = e
			}
			return nil, last
		},
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: insecure}, // per target, set by the operator
		DisableKeepAlives: true,
	}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "mtmon-probe")
	c := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
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
