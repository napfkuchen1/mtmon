package speedtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// NDT7 measures against M-Lab (ndt7 protocol, https://www.measurementlab.net/tests/ndt/ndt7/): the nearest server
// comes from the M-Lab locate API, download and upload run over one WebSocket each. Open protocol, no extra binary.
// M-Lab publishes every result including the client IP address as open data, so this provider is opt-in.
type NDT7 struct {
	Locate string // locate API URL; tests point it at a local server
}

func (NDT7) Name() string { return "ndt7" }

const (
	ndt7Locate   = "https://locate.measurementlab.net/v2/nearest/ndt/ndt7"
	ndt7Protocol = "net.measurementlab.ndt.v7"
	ndt7MaxDur   = 10 * time.Second // the server ends a test after 10 s anyway
)

type ndt7Machine struct {
	Machine  string `json:"machine"`
	Location struct {
		City    string `json:"city"`
		Country string `json:"country"`
	} `json:"location"`
	URLs map[string]string `json:"urls"`
}

// ndt7Measure is the part of a server measurement mtmon reads.
type ndt7Measure struct {
	TCPInfo *struct {
		MinRTT        int64 `json:"MinRTT"`        // microseconds
		BytesReceived int64 `json:"BytesReceived"` // upload: what the server got
		ElapsedTime   int64 `json:"ElapsedTime"`   // microseconds
	} `json:"TCPInfo"`
}

func (p NDT7) locate(ctx context.Context, c *http.Client) (ndt7Machine, error) {
	u := p.Locate
	if u == "" {
		u = ndt7Locate
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+"?client_name=mtmon", nil)
	if err != nil {
		return ndt7Machine{}, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return ndt7Machine{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ndt7Machine{}, fmt.Errorf("locate: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Results []ndt7Machine `json:"results"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&out); err != nil {
		return ndt7Machine{}, fmt.Errorf("locate: %w", err)
	}
	for _, m := range out.Results {
		if m.URLs["wss:///ndt/v7/download"] != "" && m.URLs["wss:///ndt/v7/upload"] != "" ||
			m.URLs["ws:///ndt/v7/download"] != "" && m.URLs["ws:///ndt/v7/upload"] != "" {
			return m, nil
		}
	}
	return ndt7Machine{}, errors.New("locate: no ndt7 server offered")
}

func (m ndt7Machine) url(dir string) string {
	if u := m.URLs["wss:///ndt/v7/"+dir]; u != "" {
		return u
	}
	return m.URLs["ws:///ndt/v7/"+dir]
}

func ndt7Dialer(family string) *websocket.Dialer {
	d := &net.Dialer{Timeout: 5 * time.Second}
	network := networkFor(family)
	return &websocket.Dialer{
		HandshakeTimeout: 8 * time.Second,
		Subprotocols:     []string{ndt7Protocol},
		NetDialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			c, err := d.DialContext(ctx, network, addr)
			if err == nil {
				Traffic.Note(c.LocalAddr(), c.RemoteAddr())
			}
			return c, err
		},
	}
}

func (p NDT7) Run(ctx context.Context, o Opts) Result {
	res := Result{}
	start := time.Now()
	c := newClient(o.Family)
	defer c.CloseIdleConnections()
	m, err := p.locate(ctx, c)
	if err != nil {
		res.Err = trimErr(err)
		return res
	}
	res.Server = m.Machine
	if m.Location.City != "" {
		res.Server += " (" + m.Location.City + ")"
	}
	maxDur := o.MaxDur
	if maxDur > ndt7MaxDur {
		maxDur = ndt7MaxDur
	}
	d := ndt7Dialer(o.Family)
	bytes, bps1, rtt, err := p.download(ctx, d, m.url("download"), maxDur, o.MaxBytes)
	res.Bytes += bytes
	if err != nil {
		res.Err = "download: " + trimErr(err)
		res.Dur = time.Since(start)
		return res
	}
	res.DownBps, res.PingMS = bps1, rtt
	bytes, bps2, err := p.upload(ctx, d, m.url("upload"), maxDur, o.MaxBytes)
	res.Bytes += bytes
	if err != nil {
		res.Err = "upload: " + trimErr(err)
	} else {
		res.UpBps = bps2
	}
	res.Dur = time.Since(start)
	return res
}

// download reads binary frames until the server closes, MaxDur or MaxBytes. It also takes the minimum RTT the
// server reports in its text measurements.
func (NDT7) download(parent context.Context, d *websocket.Dialer, wsURL string, maxDur time.Duration, maxBytes int64) (int64, float64, float64, error) {
	ctx, cancel := context.WithTimeout(parent, maxDur)
	defer cancel()
	conn, _, err := d.DialContext(ctx, wsURL, http.Header{"User-Agent": {UserAgent}})
	if err != nil {
		return 0, 0, 0, err
	}
	defer conn.Close()
	conn.SetReadLimit(1 << 24)
	var total int64
	var minRTT int64
	start := time.Now()
	for total < maxBytes {
		conn.SetReadDeadline(deadline(ctx))
		typ, msg, err := conn.ReadMessage()
		if err != nil {
			if total > 0 && (ctx.Err() != nil || websocket.IsCloseError(err, websocket.CloseNormalClosure) || isTimeout(err)) {
				break
			}
			return total, 0, 0, err
		}
		total += int64(len(msg))
		if typ == websocket.TextMessage {
			var mm ndt7Measure
			if json.Unmarshal(msg, &mm) == nil && mm.TCPInfo != nil && mm.TCPInfo.MinRTT > 0 && (minRTT == 0 || mm.TCPInfo.MinRTT < minRTT) {
				minRTT = mm.TCPInfo.MinRTT
			}
		}
	}
	dur := time.Since(start)
	if total == 0 {
		return 0, 0, 0, errors.New("no data received")
	}
	return total, bps(total, dur), float64(minRTT) / 1000, nil
}

// upload sends binary frames and trusts the server's own byte count (TCPInfo.BytesReceived) over the local one,
// which only shows what went into the socket buffer.
func (NDT7) upload(parent context.Context, d *websocket.Dialer, wsURL string, maxDur time.Duration, maxBytes int64) (int64, float64, error) {
	ctx, cancel := context.WithTimeout(parent, maxDur)
	defer cancel()
	conn, _, err := d.DialContext(ctx, wsURL, http.Header{"User-Agent": {UserAgent}})
	if err != nil {
		return 0, 0, err
	}
	defer conn.Close()
	var srvBytes, srvElapsed atomic.Int64
	done := make(chan struct{})
	go func() { // the server talks while we send
		defer close(done)
		for {
			typ, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var mm ndt7Measure
			if typ == websocket.TextMessage && json.Unmarshal(msg, &mm) == nil && mm.TCPInfo != nil && mm.TCPInfo.ElapsedTime > 0 {
				srvBytes.Store(mm.TCPInfo.BytesReceived)
				srvElapsed.Store(mm.TCPInfo.ElapsedTime)
			}
		}
	}()
	payload := make([]byte, 1<<17)
	var sent int64
	start := time.Now()
	for sent < maxBytes && ctx.Err() == nil {
		conn.SetWriteDeadline(deadline(ctx))
		if err := conn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
			if sent > 0 && (ctx.Err() != nil || isTimeout(err)) {
				break
			}
			return sent, 0, err
		}
		sent += int64(len(payload))
	}
	local := time.Since(start)
	conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
	select { // the closing frame makes the server send its last measurement
	case <-done:
	case <-time.After(2 * time.Second):
	}
	if sent == 0 {
		return 0, 0, errors.New("nothing sent")
	}
	if b, e := srvBytes.Load(), srvElapsed.Load(); b > 0 && e > 0 {
		return sent, bps(b, time.Duration(e)*time.Microsecond), nil
	}
	return sent, bps(sent, local), nil
}

func deadline(ctx context.Context) time.Time {
	if t, ok := ctx.Deadline(); ok {
		return t.Add(time.Second)
	}
	return time.Now().Add(15 * time.Second)
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
