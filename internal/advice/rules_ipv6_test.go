package advice

import (
	"strings"
	"testing"
	"time"
)

func TestRALifetimes(t *testing.T) {
	d := base()
	skipped(t, d, "ra-lifetimes") // no capabilities
	d.Devs = []Device{{Name: "gw", Role: "router", Caps: &Caps{IPv6PD: true, RAValid: 4*7*86400 + 2*86400, RAPreferred: 7 * 86400}}}
	d.Cls = []Client{{MAC: "AA:00:00:00:00:01", Hostname: "nas"}, {MAC: "AA:00:00:00:00:02", Hostname: "tv"}}
	// long lifetimes, but nobody has addresses from several prefixes yet: nothing to say
	d.V6 = map[string][]V6Prefix{"AA:00:00:00:00:02": {{Prefix: "2001:db8:1::/64", Addrs: 2, First: t0.Add(-time.Hour).Unix(), Last: t0.Unix()}}}
	expect(t, d, "ra-lifetimes", 0)
	// the symptom from a real network: one server with five prefixes
	var ps []V6Prefix
	for i := 0; i < 5; i++ {
		ps = append(ps, V6Prefix{Prefix: "2001:db8:" + string(rune('a'+i)) + "::/64", Addrs: 1, First: t0.Add(-time.Duration(5-i) * 24 * time.Hour).Unix(), Last: t0.Add(-time.Duration(4-i) * 24 * time.Hour).Unix()})
	}
	d.V6["AA:00:00:00:00:01"] = []V6Prefix{ps[4], ps[3], ps[2], ps[1], ps[0]} // newest first
	out := expect(t, d, "ra-lifetimes", 1)
	s := out[0]
	if s.Subject != "gw" || s.Commands != "" || !strings.Contains(strings.Join(s.Evidence, "\n"), "nas: addresses from 5 prefixes") ||
		!strings.Contains(s.Evidence[0], "4 weeks") && !strings.Contains(s.Evidence[0], "30.0 days") {
		t.Errorf("%+v", s)
	}
	if !strings.Contains(s.Limits, "not verified") {
		t.Errorf("no unverified RouterOS syntax may be given as fact: %+v", s)
	}
	// short lifetimes: fine
	d.Devs[0].Caps.RAValid, d.Devs[0].Caps.RAPreferred = 7200, 3600
	expect(t, d, "ra-lifetimes", 0)
	// no DHCPv6 prefix request: the prefix is static, nothing changes
	d.Devs[0].Caps.IPv6PD, d.Devs[0].Caps.RAValid = false, 4*7*86400
	skipped(t, d, "ra-lifetimes")
}
