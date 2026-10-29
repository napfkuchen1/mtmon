package provision

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/poller"
)

// fixtureServer answers /rest/<path> from testdata/routeros/<dir>/<path with _>.json; paths without a file get 404
// like a RouterOS without that package.
func fixtureServer(t *testing.T, dir string) config.Device {
	t.Helper()
	root := filepath.Join("testdata", "routeros", dir)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.ReplaceAll(strings.TrimPrefix(r.URL.Path, "/rest/"), "/", "_") + ".json"
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			http.Error(w, `{"error":404,"message":"no such command or directory"}`, 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	}))
	t.Cleanup(ts.Close)
	h, p, _ := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	port, _ := strconv.Atoi(p)
	return config.Device{Name: "gw", Addr: h, Port: port, Scheme: "http", User: "admin", Pass: "x"}
}

// TestProbeFixtureCAPsMAN reads a synthetic RouterOS 7.16 CAPsMAN controller (see testdata/.../README.md).
func TestProbeFixtureCAPsMAN(t *testing.T) {
	k, err := Probe(context.Background(), poller.NewClient(fixtureServer(t, "7.16-hap-ax3-capsman")))
	if err != nil {
		t.Fatal(err)
	}
	if k.Version != "7.16 (stable)" || k.Identity != "gw-main" || k.Model != "C52iG-5HaxD2HaxD-TC" || k.Firmware != "7.16" {
		t.Errorf("identity: %+v", k)
	}
	if k.WifiStack != "wifi" || len(k.WifiIfs) != 2 || k.WifiIfs[1].SSID != "Home" || k.WifiIfs[1].Band != "5ghz-ax" || k.WifiIfs[1].Channel != "5180 / 80mhz" {
		t.Errorf("wifi: %q %+v", k.WifiStack, k.WifiIfs)
	}
	if !k.CAPsMAN || k.CAP || k.WifiClient != 2 {
		t.Errorf("capsman %v cap %v clients %d", k.CAPsMAN, k.CAP, k.WifiClient)
	}
	if !k.NAT || !k.Fasttrack || !k.HWOffload || !k.DHCPServer || len(k.Bridges) != 1 {
		t.Errorf("nat %v fasttrack %v hw %v dhcp %v bridges %v", k.NAT, k.Fasttrack, k.HWOffload, k.DHCPServer, k.Bridges)
	}
	if len(k.Filter) != 2 || !k.Filter[1].Log || k.Filter[1].Packets != 37 {
		t.Errorf("filter: %+v", k.Filter)
	}
	if k.WwwSSL != true || k.WwwSSLAddr != "192.168.88.0/24" {
		t.Errorf("www-ssl %v %q", k.WwwSSL, k.WwwSSLAddr)
	}
	if k.Update.Latest != "7.16.1" || len(k.Addresses) != 2 || len(k.Packages) != 2 {
		t.Errorf("update %+v addresses %v packages %v", k.Update, k.Addresses, k.Packages)
	}
	if len(k.Dangling) != 1 || k.Dangling[0].Menu != "/interface/wifi/access-list" || k.Dangling[0].Value != "*61" || k.Dangling[0].Action != "reject" || k.Dangling[0].Comment == "" {
		t.Errorf("dangling refs: %+v", k.Dangling)
	}
	if k.Flow.Supported || k.Flow.Targets == nil || k.Certs == nil {
		t.Errorf("lists must be empty, not nil: %+v", k.Flow)
	}
}

// TestProbeFixtureRejectsOldRouterOS: a RouterOS 6 answer is refused with a clear message.
func TestProbeFixtureRejectsOldRouterOS(t *testing.T) {
	d := fixtureServer(t, "6.49-legacy")
	if _, err := Probe(context.Background(), poller.NewClient(d)); err == nil || !strings.Contains(err.Error(), "needs RouterOS 7") {
		t.Fatalf("err = %v", err)
	}
}
