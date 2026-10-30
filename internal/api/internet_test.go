package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestInternetQualityEndpoint(t *testing.T) {
	ts, s := newTestServer(t)
	now := time.Now()
	for i := 0; i < 10; i++ {
		s.St.AddWANSample(now.Add(-time.Duration(i)*time.Minute), "1.1.1.1:443", i != 3, 25)
	}
	s.St.AddAlert(store.Alert{TS: now.Unix() - 600, Kind: "wan_down", Subject: "gw", Severity: "warning", Msg: "x"})
	s.St.AddAlert(store.Alert{TS: now.Unix() - 540, Kind: "wan_up", Subject: "gw", Severity: "info", Msg: "x"})
	_, c := login(t, ts, "test-password-1")
	r, _ := c.Get(ts.URL + "/api/internet?range=1h")
	var m struct {
		Enabled bool `json:"enabled"`
		Now     struct {
			Sent    int     `json:"sent"`
			LossPct float64 `json:"loss_pct"`
			AvgMS   float64 `json:"avg_ms"`
		} `json:"now"`
		Series        []map[string]any `json:"series"`
		Targets       []map[string]any `json:"targets"`
		Reconnects    int              `json:"reconnects_24h"`
		LastReconnect int64            `json:"last_reconnect"`
	}
	json.NewDecoder(r.Body).Decode(&m)
	if !m.Enabled || m.Now.Sent != 6 || m.Series == nil || len(m.Targets) != 1 || m.Reconnects != 1 || m.LastReconnect == 0 {
		t.Errorf("%+v", m)
	}
	req, _ := http.NewRequest("PUT", ts.URL+"/api/internet", strings.NewReader(`{"enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	if r, _ := c.Do(req); r.StatusCode != 200 || s.St.GetMeta("wan_probe") != "off" {
		t.Error("probe switch")
	}
}

func TestInternetTargetsAPI(t *testing.T) {
	ts, s := newTestServer(t)
	_, c := login(t, ts, "test-password-1")
	do := func(method, body string) (int, string) {
		req, _ := http.NewRequest(method, ts.URL+"/api/internet/targets", strings.NewReader(body))
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}
	if code, b := do("GET", ""); code != 200 || !strings.Contains(b, `"targets":[]`) {
		t.Fatalf("empty list must be [] : %d %s", code, b)
	}
	if code, _ := do("PUT", `{"targets":[{"kind":"dns","target":"no name"}]}`); code != 400 {
		t.Errorf("bad name accepted: %d", code)
	}
	if code, b := do("PUT", `{"targets":[{"kind":"http","target":"https://nas.lan/","insecure":true},{"kind":"dns","target":"example.org"}]}`); code != 200 {
		t.Fatalf("save: %d %s", code, b)
	}
	got := CustomTargets(s.St.GetMeta)
	if len(got) != 2 || !got[0].Insecure || got[1].Target != "example.org" {
		t.Errorf("stored: %+v", got)
	}
}
