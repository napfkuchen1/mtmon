// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/enrich"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// #319: a read-only token reads /api/system (health fields) and /metrics, but not the settings-like endpoints.
func TestTokenReadsSystemAndMetrics(t *testing.T) {
	ts, _ := newTestServer(t)
	_, c := login(t, ts, "test-password-1")
	resp, err := c.Post(ts.URL+"/api/tokens", "application/json", strings.NewReader(`{"name":"ha"}`))
	if err != nil {
		t.Fatal(err)
	}
	var tk struct {
		Token string `json:"token"`
	}
	json.NewDecoder(resp.Body).Decode(&tk)
	resp.Body.Close()
	if !strings.HasPrefix(tk.Token, "mtm_") {
		t.Fatalf("token: %q", tk.Token)
	}
	get := func(path string) (int, string) {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+tk.Token)
		r, err := (&http.Client{}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}
	if code, body := get("/api/system"); code != 200 || !strings.Contains(body, `"version"`) || strings.Contains(body, "data_dir\"") {
		t.Errorf("/api/system with token: %d %s", code, body)
	}
	if code, body := get("/metrics"); code != 200 || !strings.Contains(body, "mtmon_build_info") {
		t.Errorf("/metrics with token: %d %s", code, body)
	}
	for _, p := range []string{"/api/tokens", "/api/access", "/api/audit"} {
		if code, _ := get(p); code != 401 {
			t.Errorf("%s with token: %d, want 401", p, code)
		}
	}
}

// #286: the hint names the gateway only when the exporting peer is the top of the tree and this is another router.
func TestFlowPeerIsGateway(t *testing.T) {
	cases := []struct {
		root, peer, self string
		want             bool
	}{
		{"gw", "gw", "lab", true},
		{"gw", "lab2", "lab", false}, // peer is no gateway
		{"gw", "gw", "gw", false},
		{"gw", "", "lab", false},
	}
	for _, c := range cases {
		if got := flowPeerIsGateway(c.root, c.peer, c.self); got != c.want {
			t.Errorf("%+v: got %v", c, got)
		}
	}
}

// #289: IPv6 sources of the firewall log get country and network like IPv4 ones; link-local ones stay local.
func TestIPv6FirewallSourcesGetGeo(t *testing.T) {
	src := nameSources{geo: func(a netip.Addr) enrich.Geo {
		if a == netip.MustParseAddr("2a00:1450:4001:81b::200e") {
			return enrich.Geo{CC: "IE", ASN: 15169, ASOrg: "GOOGLE"}
		}
		return enrich.Geo{}
	}}
	if got := resolveIP(src, "2a00:1450:4001:81b::200e", ""); got.Country != "IE" || got.Org != "GOOGLE" || got.Local {
		t.Errorf("global v6: %+v", got)
	}
	if got := resolveIP(src, "fe80::1:2", ""); !got.Local || got.Country != "" {
		t.Errorf("link-local v6: %+v", got)
	}
	if got := resolveIP(src, "[2a00:1450:4001:81b::200e]", ""); got != (IPInfo{}) {
		t.Errorf("bracketed text is not an address: %+v", got)
	}
}

// The firewall top lists use the same lookup, so IPv6 entries carry country and network too.
func TestNameTopsIPv6(t *testing.T) {
	_, s := newTestServer(t)
	s.En = enrich.New("", "", "", false)
	sum := &store.FwSummary{TopSrc: []store.FwTop{{Key: "2001:db8:5::7", Hits: 3}, {Key: "fe80::1", Hits: 1}}, TopDst: []store.FwTop{}}
	s.nameTops(sum)
	if sum.TopSrc[1].Local != true {
		t.Errorf("link-local source should be local: %+v", sum.TopSrc[1])
	}
	if sum.TopSrc[0].Key != "2001:db8:5::7" {
		t.Errorf("key changed: %+v", sum.TopSrc[0])
	}
}
