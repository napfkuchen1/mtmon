// SPDX-License-Identifier: AGPL-3.0-only

package poller

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"1w2d3h4m5s": (7*24+2*24+3)*time.Hour + 4*time.Minute + 5*time.Second,
		"3d12:30:05": 3*24*time.Hour + 12*time.Hour + 30*time.Minute + 5*time.Second,
		"12:30:05":   12*time.Hour + 30*time.Minute + 5*time.Second,
		"30:05":      30*time.Minute + 5*time.Second,
		"5s":         5 * time.Second,
		"100ms":      100 * time.Millisecond,
		"1w":         7 * 24 * time.Hour,
		"":           0,
	}
	for in, want := range cases {
		if got := ParseDuration(in); got != want {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
}

func TestParseRows(t *testing.T) {
	r, err := parseRows([]byte(`[{"a":"1","b":true,"c":null}]`))
	if err != nil || len(r) != 1 || r[0]["b"] != "true" {
		t.Fatalf("%v %v", r, err)
	}
	r, err = parseRows([]byte(`{"uptime":"5s"}`))
	if err != nil || len(r) != 1 || r[0]["uptime"] != "5s" {
		t.Fatalf("%v %v", r, err)
	}
}

func TestGetPropsAsksForPropertiesAndCountsBytes(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Write([]byte(`[{"name":"ether1"}]`))
	}))
	defer srv.Close()
	c := &Client{base: srv.URL + "/rest", hc: srv.Client()}
	rows, err := c.GetProps(t.Context(), "/interface", "name", "running")
	if err != nil || len(rows) != 1 {
		t.Fatalf("%v %v", rows, err)
	}
	if query != ".proplist=name,running" {
		t.Errorf("query %q", query)
	}
	if c.Bytes() != int64(len(`[{"name":"ether1"}]`)) {
		t.Errorf("bytes %d", c.Bytes())
	}
}
