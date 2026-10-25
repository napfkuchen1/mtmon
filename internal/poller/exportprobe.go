package poller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// ExportResult summarises one POST /rest/export. It never carries the configuration text.
type ExportResult struct {
	Status      int    `json:"status"`                 // HTTP status (0 = no answer)
	Inline      bool   `json:"inline"`                 // the configuration text came back in the response
	Lines       int    `json:"lines,omitempty"`        // lines of that text, header comments excluded
	SecretLines int    `json:"secret_lines,omitempty"` // lines that carry a non-empty password=/private-key=/secret=/pre-shared-key= value
	Error       string `json:"error,omitempty"`
}

var secretLine = regexp.MustCompile(`(?:^|\s)(?:password|private-key|secret|pre-shared-key|passphrase)=(?:"[^"]+"|[^\s"]+)`)

// ExportProbe asks the router once for its terse configuration export and reports only what the answer looks
// like (status, inline or not, line count, whether secret-looking values occur). It is the single POST outside the
// setup wizard and exists to decide whether a config-drift view (#46) can work with the read-only user: the request
// is fixed (no caller-supplied path or body), has no "file" argument so nothing is written on the router, and the
// text is counted and dropped.
func (c *Client) ExportProbe(ctx context.Context) ExportResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/export", strings.NewReader(`{"terse":""}`))
	if err != nil {
		return ExportResult{Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.user, c.pass)
	resp, err := c.hc.Do(req)
	if err != nil {
		return ExportResult{Error: trunc(err.Error(), 120)}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return summariseExport(resp.StatusCode, body, err)
}

func summariseExport(status int, body []byte, readErr error) ExportResult {
	res := ExportResult{Status: status}
	if readErr != nil {
		res.Error = trunc(readErr.Error(), 120)
		return res
	}
	if status >= 300 {
		res.Error = rosError(body, status)
		return res
	}
	text := exportText(body)
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		res.Lines++
		if secretLine.MatchString(l) {
			res.SecretLines++
		}
	}
	res.Inline = res.Lines > 1 // a single value is a file name or a result code, not a configuration
	return res
}

// exportText finds the configuration text in the answer: a bare string, or the longest string value of an object
// or of the objects in an array (RouterOS puts command output under a key such as "ret").
func exportText(body []byte) string {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return string(body)
	}
	best := ""
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case string:
			if len(t) > len(best) {
				best = t
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	return best
}
