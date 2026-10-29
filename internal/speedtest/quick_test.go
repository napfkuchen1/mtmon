package speedtest

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// A quick test with its own provider choice runs only those providers and never touches the saved schedule.
func TestRunNowOnlyLeavesSavedSettingsAlone(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.Local)
	st, _ := store.Open(t.TempDir())
	defer st.Close()
	var seen []string
	r := &Runner{St: st, Now: func() time.Time { return now }, Providers: func(s Settings) []Provider {
		seen = append([]string(nil), s.Providers...)
		return []Provider{&fakeProvider{name: "fake", res: Result{DownBps: 1e6}}}
	}}
	saved := save(t, r, func(s *Settings) { s.IntervalMin = 60; s.Providers = []string{"cloudflare", "ndt7"} })
	if err := r.RunNow("hetzner"); err != nil {
		t.Fatal(err)
	}
	waitIdle(r)
	if !reflect.DeepEqual(seen, []string{"hetzner"}) {
		t.Errorf("round used providers %v, want [hetzner]", seen)
	}
	if got := LoadSettings(st); !reflect.DeepEqual(got.Providers, saved.Providers) {
		t.Errorf("saved providers changed to %v", got.Providers)
	}
	if err := r.RunNow("bogus"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("unknown provider: %v", err)
	}
}
