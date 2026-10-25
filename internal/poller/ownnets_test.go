package poller

import (
	"net/netip"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
)

func TestOwnV6Prefixes(t *testing.T) {
	got := ownV6Prefixes(
		[]Row{{"address": "2001:db8:1:2::1/64"}, {"address": "fe80::1/64"}, {"address": "fd00:1::1/64"}, {"address": "2001:db8:9::1/64", "disabled": "true"}},
		[]Row{{"prefix": "2001:db8:1::/56,6d23h"}},
		[]Row{{"prefix": "2001:db8:1::/56"}, {"prefix": "::/0"}, {"prefix": "garbage"}})
	want := []string{"2001:db8:1:2::/64", "2001:db8:1::/56", "2001:db8:1::/56"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i, p := range got {
		if p.String() != want[i] {
			t.Errorf("%d: %v want %s", i, p, want[i])
		}
	}
}

func TestLearnedNetsAreLocalAndExpire(t *testing.T) {
	c := &config.Config{}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	ps := ownV6Prefixes([]Row{{"address": "2001:db8:1:2::1/64"}}, nil, nil)
	now := time.Now()
	c.LearnNets(ps, now)
	in, out := netip.MustParseAddr("2001:db8:1:2::77"), netip.MustParseAddr("2001:db8:ffff::1")
	if !c.IsLocal(in) || c.IsLocal(out) {
		t.Errorf("learned prefix: in=%v out=%v", c.IsLocal(in), c.IsLocal(out))
	}
	c.LearnNets(nil, now.Add(config.LearnedNetsKeep+time.Hour)) // not reported for a week: forgotten
	if c.IsLocal(in) {
		t.Error("stale prefix still local")
	}
}
