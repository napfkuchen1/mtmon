package alert

import (
	"strings"
	"testing"
	"time"
)

// A keyed alert stores key + params next to the English text; the notes the engine adds follow the base text.
func TestRaisePStoresKeyAndParams(t *testing.T) {
	e, _ := newEngine(t)
	if !e.RaiseP("traffic_spike", "m", "info", "Traffic spike: {cur} Mbit/s (baseline {base})", map[string]string{"cur": "40.0", "base": "5.0"}, " – one known client causes the load", time.Hour) {
		t.Fatal("not raised")
	}
	as, _ := e.St.Alerts(10)
	if len(as) != 1 {
		t.Fatalf("alerts: %d", len(as))
	}
	a := as[0]
	if a.MsgKey == "" || a.Params["cur"] != "40.0" || !strings.HasPrefix(a.Msg, "Traffic spike: 40.0 Mbit/s (baseline 5.0)") || !strings.HasSuffix(a.Msg, "causes the load") {
		t.Fatalf("stored: %+v", a)
	}
	e.Raise("cpu_high", "r", "warning", "r CPU at 99% for >30s", 0) // old style stays without key
	as, _ = e.St.Alerts(10)
	for _, x := range as {
		if x.Kind == "cpu_high" && (x.MsgKey != "" || x.Params != nil) {
			t.Fatalf("unkeyed alert got a key: %+v", x)
		}
	}
}
