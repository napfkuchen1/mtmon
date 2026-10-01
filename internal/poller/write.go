package poller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Do performs a state-changing REST call. It is ONLY used by internal/provision with credentials that
// the operator typed into the setup wizard (never stored). The monitoring poller itself only calls Get.
func (c *Client) Do(ctx context.Context, method, path string, body any) ([]Row, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.SetBasicAuth(c.user, c.pass)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == 401:
		return nil, ErrAuth
	case resp.StatusCode == 403:
		return nil, fmt.Errorf("routeros: %s %s: forbidden (user lacks the needed policy)", method, path)
	case resp.StatusCode >= 300:
		return nil, fmt.Errorf("routeros: %s %s: %s", method, path, rosError(b, resp.StatusCode))
	}
	return parseRows(b)
}

func rosError(b []byte, code int) string {
	var e struct {
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if json.Unmarshal(b, &e) == nil && e.Message != "" {
		if e.Detail != "" {
			return fmt.Sprintf("HTTP %d: %s (%s)", code, e.Message, e.Detail)
		}
		return fmt.Sprintf("HTTP %d: %s", code, e.Message)
	}
	return fmt.Sprintf("HTTP %d: %s", code, strings.TrimSpace(trunc(string(b), 120)))
}
