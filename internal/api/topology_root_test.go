package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/config"
)

func TestPickRoot(t *testing.T) {
	devs := []rootCand{
		{Name: "basement", Role: "router", Clients: 5},
		{Name: "ap1", Role: "ap", NAT: true},
		{Name: "main", Role: "router", NAT: true, DHCP: true, CAPsMAN: true, Clients: 20},
	}
	if got := pickRoot(devs, ""); got != "main" {
		t.Errorf("gateway: %s", got)
	}
	if got := pickRoot(devs, "basement"); got != "basement" {
		t.Errorf("override: %s", got)
	}
	if got := pickRoot(devs, "gone"); got != "main" {
		t.Errorf("stale override: %s", got)
	}
	// no caps known: more clients wins, otherwise list order
	plain := []rootCand{{Name: "a", Role: "router"}, {Name: "b", Role: "router", Clients: 3}}
	if got := pickRoot(plain, ""); got != "b" {
		t.Errorf("clients: %s", got)
	}
	if got := pickRoot([]rootCand{{Name: "sw", Role: "switch"}, {Name: "ap", Role: "ap"}}, ""); got != "sw" {
		t.Errorf("no router: %s", got)
	}
	if got := pickRoot(nil, ""); got != "" {
		t.Errorf("empty: %q", got)
	}
}

func TestTopologyRootOverride(t *testing.T) {
	ts, s := newTestServer(t)
	s.Cfg.Devices = append(s.Cfg.Devices, config.Device{Name: "a", Addr: "192.0.2.1", Role: "router"}, config.Device{Name: "b", Addr: "192.0.2.2", Role: "router"})
	_, c := login(t, ts, "test-password-1")
	put := func(body string) int {
		req, _ := http.NewRequest("PUT", ts.URL+"/api/topology/root", strings.NewReader(body))
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if code := put(`{"device":"nope"}`); code != 404 {
		t.Errorf("unknown: %d", code)
	}
	if code := put(`{"device":"b"}`); code != 200 || s.St.GetMeta(rootMetaKey) != "b" {
		t.Errorf("set: %d %q", code, s.St.GetMeta(rootMetaKey))
	}
	if code := put(`{"device":""}`); code != 200 || s.St.GetMeta(rootMetaKey) != "" {
		t.Errorf("clear: %d", code)
	}
}
