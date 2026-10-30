package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLblEscapes(t *testing.T) {
	if got := lbl(`a"b`); got != `"a\"b"` {
		t.Fatalf("got %s", got)
	}
}

func TestMetricsNeedsLogin(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("anonymous /metrics: %d", resp.StatusCode)
	}
	_, c := login(t, ts, "test-password-1")
	resp, err = c.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "mtmon_alerts_open 0") || !strings.Contains(string(body), `mtmon_build_info{version="test"} 1`) {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
}
