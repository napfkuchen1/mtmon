// SPDX-License-Identifier: AGPL-3.0-only

package poller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/live"
)

var connQuery = live.VPNConnQuery{NAT: netip.MustParseAddr("192.0.0.2"), Server: netip.MustParseAddr("93.184.216.34"),
	NATPort: 61000, ServerPort: 443, Proto: 6}

// Both forms RouterOS can show, and rows that only look alike: the mock ignores the filter, as if the router had.
func TestVPNConnFindsOriginalSource(t *testing.T) {
	for name, rows := range map[string][]map[string]any{
		"address with port": {
			{"src-address": "192.168.216.9:50000", "reply-src-address": "93.184.216.34:443", "reply-dst-address": "192.0.0.2:61001", "protocol": "tcp"}, // other NAT port
			{"src-address": "192.168.216.8:50000", "reply-src-address": "93.184.216.35:443", "reply-dst-address": "192.0.0.2:61000", "protocol": "tcp"}, // other server
			{"src-address": "192.168.216.7:50000", "reply-src-address": "93.184.216.34:443", "reply-dst-address": "192.0.0.2:61000", "protocol": "udp"}, // other protocol
			{"src-address": "192.168.216.3:50000", "reply-src-address": "93.184.216.34:443", "reply-dst-address": "192.0.0.2:61000", "protocol": "tcp"},
		},
		"port in its own field": {
			{"src-address": "192.168.216.3", "reply-src-address": "93.184.216.34", "reply-src-port": "443", "reply-dst-address": "192.0.0.2", "reply-dst-port": "61000", "protocol": "6"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newHWRig(t, nil)
			rig.rt.SetConnections(rows)
			peer, ok := rig.w.m.VPNConn("gw", connQuery)
			if !ok || peer != netip.MustParseAddr("192.168.216.3") {
				t.Errorf("got %v %v", peer, ok)
			}
		})
	}
}

func TestVPNConnNoSuchConnection(t *testing.T) {
	rig := newHWRig(t, nil)
	if peer, ok := rig.w.m.VPNConn("gw", connQuery); ok {
		t.Errorf("empty table named %v", peer)
	}
	if peer, ok := rig.w.m.VPNConn("other", connQuery); ok {
		t.Errorf("unknown device named %v", peer)
	}
}

// The lookup is a GET filtered to one port. A router that refuses it is not asked again; one that fails pauses.
func TestVPNConnRequestsAndBackoff(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{{"refused", http.StatusForbidden}, {"broken", http.StatusInternalServerError}} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var seen []string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				seen = append(seen, r.Method+" "+r.URL.RequestURI())
				mu.Unlock()
				w.WriteHeader(tc.status)
			}))
			t.Cleanup(ts.Close)
			d := devFor(t, ts, "http")
			d.Name = "gw"
			m := &Manager{Cfg: &config.Config{Devices: []config.Device{d}}}
			for range 3 {
				if _, ok := m.VPNConn("gw", connQuery); ok {
					t.Error("failed lookup named a source")
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if len(seen) == 0 || len(seen) > 2 {
				t.Fatalf("requests %v, want one round", seen)
			}
			for _, s := range seen {
				if !strings.HasPrefix(s, "GET /rest/ip/firewall/connection?reply-dst-") || !strings.Contains(s, ".proplist=") {
					t.Errorf("request %q", s)
				}
			}
		})
	}
}

func TestConnLookupOverMock(t *testing.T) {
	rig := newHWRig(t, nil)
	rig.rt.SetConnections([]map[string]any{{"src-address": "192.168.216.3:1", "reply-src-address": "93.184.216.34:443", "reply-dst-address": "192.0.0.2:61000"}})
	peer, err := connLookup(context.Background(), rig.w.c, connQuery)
	if err != nil || peer != netip.MustParseAddr("192.168.216.3") {
		t.Errorf("no protocol in the row: %v %v", peer, err)
	}
}

func TestConnProto(t *testing.T) {
	for _, tc := range []struct {
		v    string
		want uint8
		ok   bool
	}{{"tcp", 6, true}, {"TCP", 6, true}, {"udp", 17, true}, {"udp", 6, false}, {"6", 6, true}, {"17", 6, false}, {"", 6, true}, {"gre", 47, false}} {
		if connProto(tc.v, tc.want) != tc.ok {
			t.Errorf("connProto(%q, %d)", tc.v, tc.want)
		}
	}
}
