// Package poller talks to MikroTik RouterOS 7 over the REST API (read-only).
package poller

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/daniel/mtmon/internal/config"
)

type Client struct {
	base string
	user string
	pass string
	hc   *http.Client
}

var ErrAuth = errors.New("routeros: authentication failed")

func NewClient(d config.Device) *Client {
	tr := &http.Transport{
		MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second,
		DialContext: (&net.Dialer{Timeout: 4 * time.Second}).DialContext,
	}
	if d.Scheme == "https" {
		tc := &tls.Config{MinVersion: tls.VersionTLS12}
		switch {
		case d.Fingerprint != "":
			want := normFP(d.Fingerprint)
			tc.InsecureSkipVerify = true // verification done by pin below
			tc.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
				if len(raw) == 0 {
					return errors.New("no certificate")
				}
				sum := sha256.Sum256(raw[0])
				if hex.EncodeToString(sum[:]) != want {
					return fmt.Errorf("certificate fingerprint mismatch (got %x)", sum)
				}
				return nil
			}
		case d.Insecure:
			tc.InsecureSkipVerify = true
		}
		tr.TLSClientConfig = tc
	}
	return &Client{
		base: fmt.Sprintf("%s://%s/rest", d.Scheme, net.JoinHostPort(d.Addr, strconv.Itoa(d.Port))),
		user: d.User, pass: d.Pass,
		hc: &http.Client{Transport: tr, Timeout: 8 * time.Second},
	}
}

func normFP(s string) string {
	return strings.ToLower(strings.NewReplacer(":", "", " ", "").Replace(s))
}

// Fingerprint dials host:port and returns the SHA-256 fingerprint of the leaf certificate (TOFU helper).
func Fingerprint(addr string) (string, error) {
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		return "", err
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.New("no certificate presented")
	}
	sum := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(sum[:]), nil
}

// Row is one RouterOS record; all values arrive as strings.
type Row map[string]string

// Get fetches a path. Arrays are returned as rows; a single object as a one-element slice.
func (c *Client) Get(ctx context.Context, path string) ([]Row, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.user, c.pass)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return nil, ErrAuth
	case resp.StatusCode == 404:
		return nil, fmt.Errorf("routeros: %s not found (package missing?)", path)
	case resp.StatusCode >= 300:
		return nil, fmt.Errorf("routeros: %s: HTTP %d: %s", path, resp.StatusCode, trunc(string(body), 120))
	}
	return parseRows(body)
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func parseRows(b []byte) ([]Row, error) {
	b = []byte(strings.TrimSpace(string(b)))
	if len(b) == 0 {
		return nil, nil
	}
	var raw []map[string]any
	if b[0] == '{' {
		var one map[string]any
		if err := json.Unmarshal(b, &one); err != nil {
			return nil, err
		}
		raw = []map[string]any{one}
	} else if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := make([]Row, 0, len(raw))
	for _, m := range raw {
		r := make(Row, len(m))
		for k, v := range m {
			switch t := v.(type) {
			case string:
				r[k] = t
			case nil:
			default:
				r[k] = fmt.Sprint(t)
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// Int parses a numeric field (0 on error).
func (r Row) Int(k string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(r[k]), 10, 64)
	return n
}

func (r Row) Float(k string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(r[k]), 64)
	return f
}

func (r Row) Bool(k string) bool { return r[k] == "true" || r[k] == "yes" }

// ParseDuration parses RouterOS durations: "1w2d3h4m5s", "3d12:30:05", "12:30:05", "5s", "100ms".
func ParseDuration(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	var total time.Duration
	// colon form hh:mm:ss[.fff] possibly prefixed by Nw/Nd
	if strings.Contains(s, ":") {
		head := s
		idx := strings.LastIndexAny(s[:strings.Index(s, ":")], "wd")
		if idx >= 0 {
			total += parseUnits(s[:idx+1])
			head = s[idx+1:]
		}
		parts := strings.Split(head, ":")
		mult := []time.Duration{time.Hour, time.Minute, time.Second}
		if len(parts) == 2 {
			mult = mult[1:]
		}
		for i, p := range parts {
			f, _ := strconv.ParseFloat(p, 64)
			total += time.Duration(f * float64(mult[i]))
		}
		return total
	}
	return parseUnits(s)
}

func parseUnits(s string) time.Duration {
	var total time.Duration
	num := ""
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '.' {
			num += string(c)
			continue
		}
		unit := string(c)
		if c == 'm' && i+1 < len(s) && s[i+1] == 's' {
			unit = "ms"
			i++
		}
		f, _ := strconv.ParseFloat(num, 64)
		num = ""
		switch unit {
		case "w":
			total += time.Duration(f * float64(7*24*time.Hour))
		case "d":
			total += time.Duration(f * float64(24*time.Hour))
		case "h":
			total += time.Duration(f * float64(time.Hour))
		case "m":
			total += time.Duration(f * float64(time.Minute))
		case "s":
			total += time.Duration(f * float64(time.Second))
		case "ms":
			total += time.Duration(f * float64(time.Millisecond))
		}
	}
	return total
}
