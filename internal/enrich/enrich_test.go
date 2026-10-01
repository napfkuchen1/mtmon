package enrich

import (
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"
)

func TestVendorRandomized(t *testing.T) {
	e := New("", "", "", false)
	if v := e.Vendor("02:11:22:33:44:55"); v != "Private (randomized MAC)" {
		t.Fatal(v)
	}
	if v := e.Vendor("00:11:22:33:44:55"); v != "" {
		t.Fatalf("unknown OUI should be empty, got %q", v)
	}
}

func TestService(t *testing.T) {
	if Service(6, 443, 50000) != "HTTPS" || Service(17, 443, 50000) != "HTTPS" {
		t.Fatal("443")
	}
	if Service(1, 0, 0) != "ICMP" || Service(6, 12345, 55555) != "" {
		t.Fatal("misc")
	}
}

func TestNameCache(t *testing.T) {
	e := New("", "", "", false)
	ip := netip.MustParseAddr("1.2.3.4")
	e.SetName(ip, "Example.COM.", time.Minute)
	if e.Name(ip) != "example.com" {
		t.Fatal("name")
	}
}

// Real-database check. Run: MTMON_GEO_DIR=/path/with/{dbip-country.mmdb,dbip-asn.mmdb,oui.csv} go test ./internal/enrich
func TestRealDatabases(t *testing.T) {
	dir := os.Getenv("MTMON_GEO_DIR")
	if dir == "" {
		t.Skip("MTMON_GEO_DIR not set")
	}
	e := New(dir+"/dbip-country.mmdb", dir+"/dbip-asn.mmdb", dir+"/oui.csv", false)
	st := e.Status()
	if !st["geo"] || !st["asn"] || !st["oui"] {
		t.Fatalf("databases not loaded: %v", st)
	}
	g := e.Lookup(netip.MustParseAddr("8.8.8.8"))
	t.Logf("8.8.8.8 -> %+v", g)
	if g.CC != "US" || g.ASN != 15169 || !strings.Contains(strings.ToLower(g.ASOrg), "google") {
		t.Errorf("8.8.8.8 lookup wrong: %+v", g)
	}
	g = e.Lookup(netip.MustParseAddr("1.1.1.1"))
	t.Logf("1.1.1.1 -> %+v", g)
	if g.ASN != 13335 {
		t.Errorf("1.1.1.1 ASN: %+v", g)
	}
	if v := e.Vendor("28:6F:B9:00:00:01"); !strings.Contains(v, "Nokia") {
		t.Errorf("OUI vendor: %q", v)
	}
	if g := e.Lookup(netip.MustParseAddr("2606:4700:4700::1111")); g.ASN != 13335 {
		t.Errorf("IPv6 lookup: %+v", g)
	}
}
