package api

import (
	"net/netip"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/enrich"
	"github.com/napfkuchen1/mtmon/internal/store"
)

func testSources() nameSources {
	cl := map[string]store.Client{
		"192.168.88.10": {MAC: "AA:BB:CC:00:3F:A2", Label: "Living room TV", Hostname: "lg-tv", Vendor: "LG"},
		"192.168.88.11": {MAC: "AA:BB:CC:00:11:22", Hostname: "pixel-8", Vendor: "Google"},
		"192.168.88.12": {MAC: "AA:BB:CC:00:C0:DE", Vendor: "Espressif Inc."},
		"192.168.88.13": {MAC: "02:BB:CC:00:C0:DE", Vendor: "Private (randomized MAC)"},
		"192.168.88.14": {MAC: "AA:BB:CC:00:C0:DF"},
	}
	return nameSources{
		clientByIP: func(ip string) (store.Client, bool) { c, ok := cl[ip]; return c, ok },
		clientByMAC: func(mac string) (store.Client, bool) {
			for _, c := range cl {
				if c.MAC == mac {
					return c, true
				}
			}
			return store.Client{}, false
		},
		deviceByIP: func(ip string) string {
			if ip == "192.168.88.1" {
				return "gw-main"
			}
			return ""
		},
		dns: func(a netip.Addr) string {
			if a == netip.MustParseAddr("142.250.1.1") {
				return "fra16s50-in-f1.1e100.net."
			}
			return ""
		},
		geo: func(a netip.Addr) enrich.Geo {
			if a == netip.MustParseAddr("142.250.1.1") || a == netip.MustParseAddr("203.0.113.9") {
				return enrich.Geo{CC: "US", ASN: 15169, ASOrg: "GOOGLE"}
			}
			return enrich.Geo{}
		},
	}
}

func TestResolveIP(t *testing.T) {
	src := testSources()
	cases := []struct {
		name, ip, mac string
		want          IPInfo
	}{
		{"label wins", "192.168.88.10", "", IPInfo{Name: "Living room TV", Local: true}},
		{"hostname", "192.168.88.11", "", IPInfo{Name: "pixel-8", Local: true}},
		{"vendor + mac suffix", "192.168.88.12", "", IPInfo{Name: "Espressif Inc. C0:DE", Local: true}},
		{"randomized mac", "192.168.88.13", "", IPInfo{Name: "Randomized MAC C0:DE", Local: true}},
		{"nothing but mac", "192.168.88.14", "", IPInfo{Name: "AA:BB:CC:00:C0:DF", Local: true}},
		{"managed device", "192.168.88.1", "", IPInfo{Name: "gw-main", Local: true}},
		{"unknown local, no mac", "192.168.88.99", "", IPInfo{Local: true}},
		{"unknown local, mac from log", "192.168.88.99", "AA:BB:CC:00:11:22", IPInfo{Name: "pixel-8", Local: true}},
		{"cgnat is local", "100.64.1.2", "", IPInfo{Local: true}},
		{"external dns + org + cc", "142.250.1.1", "", IPInfo{Name: "fra16s50-in-f1.1e100.net", Org: "GOOGLE", Country: "US"}},
		{"external org only", "203.0.113.9", "", IPInfo{Org: "GOOGLE", Country: "US"}},
		{"external unknown", "198.51.100.7", "", IPInfo{}},
		{"ipv4-mapped v6", "::ffff:192.168.88.10", "", IPInfo{Name: "Living room TV", Local: true}},
		{"garbage", "not-an-ip", "", IPInfo{}},
		{"empty", "", "", IPInfo{}},
	}
	for _, c := range cases {
		if got := resolveIP(src, c.ip, c.mac); got != c.want {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}

func TestResolveIPNilSources(t *testing.T) {
	if got := resolveIP(nameSources{}, "8.8.8.8", ""); got != (IPInfo{}) {
		t.Fatal(got)
	}
	if got := resolveIP(nameSources{}, "10.0.0.1", ""); got != (IPInfo{Local: true}) {
		t.Fatal(got)
	}
}
