package api

import (
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/config"
)

func TestAddrAllowed(t *testing.T) {
	cfg := &config.Config{LocalNets: []string{"203.0.113.0/24"}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	s := &Server{Cfg: cfg}
	for ip, ok := range map[string]bool{
		"192.168.88.1": true, "10.0.0.5": true, "172.16.1.1": true, "127.0.0.1": true, "169.254.10.10": true,
		"100.64.0.7": true, "fd12:3456::1": true, "fe80::1": true, "::1": true,
		"203.0.113.50":       true, // configured local network
		"::ffff:192.168.1.5": true,
		"8.8.8.8":            false, "1.1.1.1": false, "2001:4860:4860::8888": false, "172.32.0.1": false,
		"::ffff:8.8.8.8":  false,                                                                                    // mapped form must follow the IPv4 rule
		"169.254.169.254": false, "::ffff:169.254.169.254": false, "fd00:ec2::254": false, "100.100.100.200": false, // cloud metadata
	} {
		err := s.addrAllowed(netip.MustParseAddr(ip))
		if (err == nil) != ok {
			t.Errorf("addrAllowed(%s) = %v, want allowed=%v", ip, err, ok)
		}
	}
}

func TestProbeRejectsForbiddenTargetsBeforeConnecting(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, c := login(t, ts, "test-password-1")
	if resp.StatusCode != 200 {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	for addr, want := range map[string]string{
		"8.8.8.8": "public address", "169.254.169.254": "not allowed", "100.100.100.200": "not allowed",
		"fd00:ec2::254": "not allowed", "::ffff:169.254.169.254": "not allowed",
	} {
		body := `{"addr":"` + addr + `","user":"admin","pass":"x"}`
		r, err := c.Post(ts.URL+"/api/devices/probe", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), want) {
			t.Errorf("%s: %d %s, want 400 containing %q", addr, r.StatusCode, b, want)
		}
	}
}
