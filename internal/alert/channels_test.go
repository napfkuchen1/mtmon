package alert

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func at(day, h, m int) time.Time { // 2026-11-01 is a Sunday
	return time.Date(2026, 11, day, h, m, 0, 0, time.UTC)
}

func TestQuietActive(t *testing.T) {
	night := Quiet{Enabled: true, From: "22:00", To: "06:30"}
	for _, c := range []struct {
		t    time.Time
		want bool
	}{
		{at(1, 21, 59), false}, {at(1, 22, 0), true}, {at(2, 3, 0), true}, {at(2, 6, 29), true}, {at(2, 6, 30), false}, {at(2, 12, 0), false},
	} {
		if got := night.Active(c.t); got != c.want {
			t.Errorf("%v: got %v want %v", c.t, got, c.want)
		}
	}
	day := Quiet{Enabled: true, From: "09:00", To: "17:00", Days: []int{1}} // Mondays only
	if day.Active(at(1, 10, 0)) || !day.Active(at(2, 10, 0)) {
		t.Error("day window must only apply on Monday")
	}
	// overnight window that starts on Friday runs into Saturday morning, not into Friday morning
	fri := Quiet{Enabled: true, From: "22:00", To: "06:00", Days: []int{5}}
	if !fri.Active(at(6, 23, 0)) || !fri.Active(at(7, 2, 0)) || fri.Active(at(6, 2, 0)) || fri.Active(at(7, 23, 0)) {
		t.Error("overnight window belongs to the weekday it starts on")
	}
	if (Quiet{From: "22:00", To: "06:00"}).Active(at(1, 23, 0)) {
		t.Error("disabled window must not be active")
	}
}

func TestQuietValidate(t *testing.T) {
	for _, q := range []Quiet{
		{Enabled: true, From: "25:00", To: "06:00"}, {Enabled: true, From: "22:00", To: "22:00"},
		{Enabled: true, From: "x", To: "06:00"}, {Enabled: true, From: "22:00", To: "06:00", Days: []int{7}},
	} {
		if q.Validate() == nil {
			t.Errorf("%+v must be rejected", q)
		}
	}
	if (Quiet{Enabled: true, From: "22:00", To: "06:00", Days: []int{0, 6}}).Validate() != nil {
		t.Error("valid window rejected")
	}
	if (Quiet{From: "junk"}).Validate() != nil {
		t.Error("a disabled window is not checked")
	}
}

type hookLog struct {
	mu   sync.Mutex
	body [][]byte
	sig  []string
}

func newHook(t *testing.T) (*httptest.Server, *hookLog) {
	t.Helper()
	l := &hookLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		l.mu.Lock()
		l.body = append(l.body, b)
		l.sig = append(l.sig, r.Header.Get("X-Mtmon-Signature"))
		l.mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return srv, l
}

func (l *hookLog) n() int { l.mu.Lock(); defer l.mu.Unlock(); return len(l.body) }

func TestQuietHoldsAndFlushes(t *testing.T) {
	e, _ := newEngine(t)
	srv, l := newHook(t)
	if err := e.SaveChannels(Channels{Webhook: WebhookCh{URL: srv.URL, Secret: "k", Quiet: Quiet{Enabled: true, From: "22:00", To: "06:00"}}}); err != nil {
		t.Fatal(err)
	}
	night, morning := at(1, 23, 0), at(2, 7, 0)
	e.deliverAt(store.Alert{TS: night.Unix(), Kind: "device_offline", Severity: "warning", Msg: "AP down"}, night)
	e.deliverAt(store.Alert{TS: night.Unix(), Kind: "new_client", Severity: "info", Msg: "New client"}, night)
	if l.n() != 0 {
		t.Fatalf("warnings must be held in quiet hours, got %d", l.n())
	}
	e.deliverAt(store.Alert{TS: night.Unix(), Kind: "wan_down", Severity: "critical", Msg: "WAN down"}, night)
	if l.n() != 1 {
		t.Fatalf("critical must go out at once, got %d", l.n())
	}
	e.flushHeld(night) // still quiet
	if l.n() != 1 {
		t.Fatal("no summary during quiet hours")
	}
	e.flushHeld(morning)
	if l.n() != 2 {
		t.Fatalf("one summary after quiet hours, got %d", l.n())
	}
	var s struct {
		Kind   string
		Count  int
		Alerts []heldAlert
	}
	if err := json.Unmarshal(l.body[1], &s); err != nil || s.Kind != "quiet_summary" || s.Count != 2 || len(s.Alerts) != 2 {
		t.Fatalf("summary wrong: %s (%v)", l.body[1], err)
	}
	if l.sig[1] != SignWebhook("k", l.body[1]) || l.sig[1] == "" {
		t.Error("summary must be signed")
	}
	e.flushHeld(morning)
	if l.n() != 2 {
		t.Error("held list must be empty after the summary")
	}
}

func TestChannelsKeepSecretAndTest(t *testing.T) {
	e, _ := newEngine(t)
	srv, l := newHook(t)
	if err := e.SaveChannels(Channels{Webhook: WebhookCh{URL: srv.URL, Secret: "s1"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.SaveChannels(Channels{Webhook: WebhookCh{URL: srv.URL}}); err != nil {
		t.Fatal(err)
	}
	if e.Channels().Webhook.Secret != "s1" {
		t.Error("empty secret must keep the stored one")
	}
	if err := e.TestChannel("webhook"); err != nil || l.n() != 1 {
		t.Fatalf("test message: %v (%d)", err, l.n())
	}
	if e.TestChannel("ntfy") == nil {
		t.Error("channel that is not set up must error")
	}
	if e.SaveChannels(Channels{Ntfy: NtfyCh{URL: "ftp://x"}}) == nil {
		t.Error("non-http URL must be rejected")
	}
}
