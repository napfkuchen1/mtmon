// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
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

func TestMetricsWifiScoreOnlyPoorClients(t *testing.T) {
	ts, s := newTestServer(t)
	at := time.Now().Unix() - 600
	s.St.DB.Exec(`INSERT INTO wifi_hist(ts,mac,device,band,n,sig_sum,sig_min,sig_max,weak,tx_sum,rx_sum) VALUES(?,?,?,?,4,-320,-80,-80,4,40,40)`, at, "AA:00:00:00:00:01", "ap1", "5ghz")
	s.St.DB.Exec(`INSERT INTO wifi_hist(ts,mac,device,band,n,sig_sum,sig_min,sig_max,weak,tx_sum,rx_sum) VALUES(?,?,?,?,4,-220,-55,-55,0,1200,1200)`, at, "AA:00:00:00:00:02", "ap1", "5ghz")
	_, c := login(t, ts, "test-password-1")
	resp, err := c.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	out := string(body)
	if !strings.Contains(out, `mtmon_client_wifi_score{mac="AA:00:00:00:00:01"`) || strings.Contains(out, `mac="AA:00:00:00:00:02"`) {
		t.Fatalf("expected only the poor client:\n%s", out)
	}
}
