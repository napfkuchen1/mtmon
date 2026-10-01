package enrich

import (
	"net/netip"
	"testing"

	"github.com/daniel/mtmon/internal/store"
)

func TestClassifierPrecedence(t *testing.T) {
	c := NewClassifier()
	c.Set([]store.ServiceRule{
		{ID: 1, Name: "My NTP", Kind: "port", Value: "123", Proto: 17},
		{ID: 2, Name: "Cloud A", Kind: "asn", Value: "64500"},
		{ID: 3, Name: "Bambu", Kind: "host", Value: "bambulab.com"},
		{ID: 4, Name: "Net10", Kind: "cidr", Value: "203.0.113.0/24"},
		{ID: 5, Name: "Net10-narrow", Kind: "cidr", Value: "203.0.113.128/25"},
		{ID: 6, Name: "Exact", Kind: "ip", Value: "203.0.113.200"},
	})
	ip := netip.MustParseAddr
	cases := []struct {
		name  string
		proto uint8
		port  uint16
		ip    string
		host  string
		asn   uint32
		want  string
	}{
		{"port rule beats builtin", 17, 123, "1.1.1.1", "", 0, "My NTP"},
		{"tcp 123 not matched by udp rule", 6, 123, "1.1.1.1", "", 0, "NTP"},
		{"asn beats port", 17, 123, "1.1.1.1", "", 64500, "Cloud A"},
		{"host suffix beats asn", 6, 443, "1.1.1.1", "mqtt.eu.bambulab.com", 64500, "Bambu"},
		{"host exact", 6, 443, "1.1.1.1", "bambulab.com", 0, "Bambu"},
		{"host must be a real suffix", 6, 443, "1.1.1.1", "notbambulab.com", 0, "HTTPS"},
		{"cidr beats host", 6, 443, "203.0.113.5", "x.bambulab.com", 0, "Net10"},
		{"longer prefix wins", 6, 443, "203.0.113.130", "", 0, "Net10-narrow"},
		{"ip beats everything", 6, 443, "203.0.113.200", "x.bambulab.com", 64500, "Exact"},
		{"no rule: builtin", 6, 22, "9.9.9.9", "", 0, "SSH"},
		{"icmp never port-matched", 1, 123, "9.9.9.9", "", 0, "ICMP"},
	}
	for _, tc := range cases {
		if got := c.Classify(tc.proto, tc.port, 40000, ip(tc.ip), tc.host, tc.asn); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestValidateRule(t *testing.T) {
	ok := []store.ServiceRule{
		{Name: "A", Kind: "port", Value: "443", Proto: 6}, {Name: "B", Kind: "ip", Value: "10.0.0.1"},
		{Name: "C", Kind: "cidr", Value: "10.1.2.3/8"}, {Name: "D", Kind: "host", Value: "*.Example.COM."}, {Name: "E", Kind: "asn", Value: "AS13335"},
	}
	for _, r := range ok {
		if _, err := ValidateRule(r); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	if r, _ := ValidateRule(ok[2]); r.Value != "10.0.0.0/8" {
		t.Errorf("cidr not masked: %s", r.Value)
	}
	if r, _ := ValidateRule(ok[3]); r.Value != "example.com" {
		t.Errorf("host not normalised: %s", r.Value)
	}
	bad := []store.ServiceRule{
		{Name: "", Kind: "port", Value: "1"}, {Name: "x<b>", Kind: "port", Value: "1"}, {Name: "ok", Kind: "port", Value: "70000"},
		{Name: "ok", Kind: "port", Value: "80", Proto: 1}, {Name: "ok", Kind: "ip", Value: "nope"}, {Name: "ok", Kind: "cidr", Value: "10.0.0.0"},
		{Name: "ok", Kind: "host", Value: "a b"}, {Name: "ok", Kind: "host", Value: "100%.com"}, {Name: "ok", Kind: "asn", Value: "x"}, {Name: "ok", Kind: "regex", Value: ".*"},
	}
	for _, r := range bad {
		if _, err := ValidateRule(r); err == nil {
			t.Errorf("accepted bad rule %+v", r)
		}
	}
}
