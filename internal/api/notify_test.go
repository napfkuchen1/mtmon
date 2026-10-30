package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/alert"
	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestNotifyAPI(t *testing.T) {
	ts, s := newTestServer(t)
	s.Alerts = alert.New(s.Cfg, s.St, s.Hub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, c := login(t, ts, "test-password-1")
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer hook.Close()

	put := func(body string) (int, string) {
		req, _ := http.NewRequest("PUT", ts.URL+"/api/notify", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	code, out := put(`{"webhook":{"url":"` + hook.URL + `","secret":"topsecret","quiet":{"enabled":true,"from":"22:00","to":"06:00"}}}`)
	if code != 200 || strings.Contains(out, "topsecret") || !strings.Contains(out, `"secret_set":true`) {
		t.Fatalf("save: %d %s", code, out)
	}
	if code, _ := put(`{"webhook":{"quiet":{"enabled":true,"from":"22:00","to":"22:00"}}}`); code != 400 {
		t.Errorf("bad window must be 400, got %d", code)
	}
	resp, err := c.Get(ts.URL + "/api/notify")
	if err != nil {
		t.Fatal(err)
	}
	var v notifyView
	json.NewDecoder(resp.Body).Decode(&v)
	resp.Body.Close()
	if v.Webhook.URL != hook.URL || !v.Webhook.SecretSet || !v.Webhook.Quiet.Enabled {
		t.Fatalf("get: %+v", v)
	}
	post := func(ch string) map[string]any {
		resp, err := c.Post(ts.URL+"/api/notify/test?channel="+ch, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		m := map[string]any{"status": float64(resp.StatusCode)}
		json.NewDecoder(resp.Body).Decode(&m)
		return m
	}
	if m := post("webhook"); m["ok"] != true {
		t.Errorf("webhook test: %v", m)
	}
	if m := post("ntfy"); m["ok"] != false || m["error"] == "" {
		t.Errorf("unset channel must report an error: %v", m)
	}
	if m := post("pigeon"); m["status"] != float64(400) {
		t.Errorf("unknown channel: %v", m)
	}
}

func TestMetricsInternetAndSpeed(t *testing.T) {
	ts, s := newTestServer(t)
	_, c := login(t, ts, "test-password-1")
	now := time.Now()
	s.St.AddWANSample(now, "dns:example.org", true, 12)
	s.St.AddWANSample(now, "http4:https://example.org/", false, 0)
	if _, err := s.St.AddSpeedtest(store.Speedtest{TS: now.Unix(), DownBps: 5e8, UpBps: 1e8, PingMS: 9}); err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	body := string(b)
	for _, want := range []string{
		`mtmon_probe_latency_ms{target="dns:example.org"} 12`,
		`mtmon_probe_loss_percent{target="http4:https://example.org/"} 100`,
		`mtmon_speedtest_down_bps 5e+08`,
		`mtmon_alerts_open_by_severity{severity="critical"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}
