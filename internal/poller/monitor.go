// SPDX-License-Identifier: AGPL-3.0-only

package poller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// RouterOS answers live hardware values (SFP optics, PoE-out state, LTE signal) only to a `monitor` command, and
// over REST a command is a POST. Such a call with `once` changes nothing on the router. It is the second narrow
// exception to "mtmon only reads" (next to ExportProbe, see CLAUDE.md): the path must be one of the three entries
// of monitorAllowlist, the body is built here (numbers, once, .proplist), and nothing else may ever be posted
// through Monitor.
var monitorAllowlist = map[string]bool{
	"interface/ethernet/poe/monitor": true, // PoE-out state and power per port
	"interface/ethernet/monitor":     true, // SFP digital optical monitoring (DOM) per port
	"interface/lte/monitor":          true, // LTE signal
}

var (
	// ErrMonitorPath is returned for a path outside the allowlist; nothing is sent.
	ErrMonitorPath = errors.New("routeros: monitor path not allowed")
	// ErrMonitorForbidden means the router refused the call for this user (HTTP 403). The caller turns the
	// feature off for the device and does not ask again.
	ErrMonitorForbidden = errors.New("routeros: monitor not permitted for this user")
	// ErrMonitorNotFound means the router has no such command (package or hardware missing, HTTP 404).
	ErrMonitorNotFound = errors.New("routeros: monitor command not found")
)

// Monitor runs `<path> numbers=<port> once` and returns the one answer row (nil if the router sent none). The
// .proplist limits the answer to the named properties.
func (c *Client) Monitor(ctx context.Context, path, port string, props ...string) (Row, error) {
	p := strings.Trim(path, "/")
	if !monitorAllowlist[p] {
		return nil, ErrMonitorPath
	}
	body := map[string]string{"numbers": port, "once": ""}
	if len(props) > 0 {
		body[".proplist"] = strings.Join(props, ",")
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/"+p, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.user, c.pass)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	c.rx.Add(int64(len(out)))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == 403:
		return nil, ErrMonitorForbidden
	case resp.StatusCode == 401:
		return nil, ErrAuth
	case resp.StatusCode == 404:
		return nil, ErrMonitorNotFound
	case resp.StatusCode >= 300:
		return nil, fmt.Errorf("routeros: /%s: HTTP %d", p, resp.StatusCode)
	}
	rows, err := parseRows(out)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

// num reads a numeric property. ok is false when the router did not send it or it is not a number, so a missing
// value never turns into 0. A unit suffix such as "dBm" or "C" is ignored.
func num(r Row, key string) (float64, bool) {
	v := strings.TrimSpace(r[key])
	if v == "" {
		return 0, false
	}
	end := 0
	for end < len(v) {
		ch := v[end]
		if ch >= '0' && ch <= '9' || ch == '.' || (end == 0 && (ch == '-' || ch == '+')) {
			end++
			continue
		}
		break
	}
	f, err := strconv.ParseFloat(v[:end], 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// numPtr is num as a pointer: nil = not reported.
func numPtr(r Row, key string) *float64 {
	if f, ok := num(r, key); ok {
		return &f
	}
	return nil
}
