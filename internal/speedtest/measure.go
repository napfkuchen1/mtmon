package speedtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Opts bound one measurement: nothing runs longer than MaxDur or moves more than MaxBytes per direction.
type Opts struct {
	Family   string // "4", "6" or "" (system choice)
	MaxBytes int64
	MaxDur   time.Duration
	Streams  int
}

// Result is what a provider reports; Err set means the test failed (no numbers are trusted then).
type Result struct {
	Server   string
	DownBps  float64
	UpBps    float64
	PingMS   float64
	JitterMS float64
	Bytes    int64 // all traffic of the test, both directions
	Dur      time.Duration
	Err      string
}

// Provider runs one test against one service.
type Provider interface {
	Name() string
	Run(ctx context.Context, o Opts) Result
}

// UserAgent is sent with every request. Hetzner's speed hosts reset connections that carry Go's default agent, so
// main sets it to "mtmon/<version>".
var UserAgent = "mtmon"

type uaTransport struct{ rt http.RoundTripper }

func (t uaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("User-Agent", UserAgent)
	return t.rt.RoundTrip(r)
}

func networkFor(family string) string {
	switch family {
	case "4":
		return "tcp4"
	case "6":
		return "tcp6"
	}
	return "tcp"
}

// newClient returns an HTTP client that connects over the requested IP family only. No overall timeout: the
// context bounds every call.
func newClient(family string) *http.Client {
	network := networkFor(family)
	d := &net.Dialer{Timeout: 5 * time.Second}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			c, err := d.DialContext(ctx, network, addr)
			if err == nil {
				Traffic.Note(c.LocalAddr(), c.RemoteAddr()) // so the flow pipeline can label this traffic
			}
			return c, err
		},
		TLSHandshakeTimeout: 8 * time.Second, DisableCompression: true, MaxIdleConnsPerHost: 8,
	}
	return &http.Client{Transport: uaTransport{tr}}
}

// countWriter discards and counts what is read from a response.
func drain(ctx context.Context, r io.Reader, total *atomic.Int64) error {
	buf := make([]byte, 64<<10)
	for {
		n, err := r.Read(buf)
		total.Add(int64(n))
		if err != nil {
			if err == io.EOF || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

// download opens streams parallel GETs (urlFor gives each stream's URL) and counts bytes until MaxDur passes or the
// streams are done. Returns the bytes received and the time they took.
func download(parent context.Context, c *http.Client, o Opts, urlFor func(stream int) string) (int64, time.Duration, error) {
	ctx, cancel := context.WithTimeout(parent, o.MaxDur)
	defer cancel()
	var total atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, o.Streams)
	start := time.Now()
	for i := 0; i < o.Streams; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlFor(i), nil)
			if err != nil {
				errs <- err
				return
			}
			resp, err := c.Do(req)
			if err != nil {
				if ctx.Err() == nil {
					errs <- err
				}
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				errs <- fmt.Errorf("HTTP %d", resp.StatusCode)
				return
			}
			if err := drain(ctx, io.LimitReader(resp.Body, o.MaxBytes/int64(o.Streams)), &total); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	dur := time.Since(start)
	close(errs)
	if total.Load() == 0 {
		if err, ok := <-errs; ok {
			return 0, dur, err
		}
		return 0, dur, errors.New("no data received")
	}
	return total.Load(), dur, nil
}

// zeroReader produces zero bytes up to limit or until ctx ends, counting what was actually read by the transport.
type zeroReader struct {
	ctx   context.Context
	left  int64
	total *atomic.Int64
}

func (z *zeroReader) Read(p []byte) (int, error) {
	if z.ctx.Err() != nil || z.left <= 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > z.left {
		n = z.left
	}
	for i := int64(0); i < n; i++ {
		p[i] = 0
	}
	z.left -= n
	z.total.Add(n)
	return int(n), nil
}

// upload POSTs generated zero bytes over streams parallel connections until MaxDur or MaxBytes.
func upload(parent context.Context, c *http.Client, o Opts, url string) (int64, time.Duration, error) {
	ctx, cancel := context.WithTimeout(parent, o.MaxDur)
	defer cancel()
	var total atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, o.Streams)
	start := time.Now()
	for i := 0; i < o.Streams; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := &zeroReader{ctx: ctx, left: o.MaxBytes / int64(o.Streams), total: &total}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
			if err != nil {
				errs <- err
				return
			}
			req.Header.Set("Content-Type", "application/octet-stream")
			resp, err := c.Do(req)
			if err != nil {
				if ctx.Err() == nil {
					errs <- err
				}
				return
			}
			io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
			resp.Body.Close()
			if resp.StatusCode >= 300 {
				errs <- fmt.Errorf("HTTP %d", resp.StatusCode)
			}
		}()
	}
	wg.Wait()
	dur := time.Since(start)
	close(errs)
	if total.Load() == 0 {
		if err, ok := <-errs; ok {
			return 0, dur, err
		}
		return 0, dur, errors.New("nothing sent")
	}
	return total.Load(), dur, nil
}

// connectLatency times n TCP connects to addr (host:port) and returns the median and the mean absolute difference of
// consecutive samples (jitter) in ms. A connect is one round trip, so TLS and HTTP time do not inflate it.
func connectLatency(ctx context.Context, family, addr string, n int) (median, jitter float64, err error) {
	d := &net.Dialer{Timeout: 5 * time.Second}
	var samples []float64
	for i := 0; i < n; i++ {
		t0 := time.Now()
		c, e := d.DialContext(ctx, networkFor(family), addr)
		if e != nil {
			return 0, 0, e
		}
		samples = append(samples, float64(time.Since(t0).Nanoseconds())/1e6)
		c.Close()
	}
	var diff float64
	for i := 1; i < len(samples); i++ {
		diff += math.Abs(samples[i] - samples[i-1])
	}
	if len(samples) > 1 {
		jitter = diff / float64(len(samples)-1)
	}
	s := append([]float64(nil), samples...)
	sort.Float64s(s)
	median = s[len(s)/2]
	if len(s)%2 == 0 {
		median = (s[len(s)/2-1] + s[len(s)/2]) / 2
	}
	return median, jitter, nil
}

func bps(bytes int64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(bytes) * 8 / d.Seconds()
}
