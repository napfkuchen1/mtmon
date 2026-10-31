// SPDX-License-Identifier: AGPL-3.0-only

package provision

import (
	"context"
	"reflect"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/poller"
)

func fixtureRows(t *testing.T, dir, path string) []poller.Row {
	t.Helper()
	rows, err := poller.NewClient(fixtureServer(t, dir)).Get(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// real-7.24: all services restricted to private ranges via available-from, active sessions are extra dynamic rows.
func TestOpenMgmtReal724(t *testing.T) {
	for _, dev := range []string{"hex", "wap", "aud"} {
		if got := openMgmtNames(fixtureRows(t, "real-7.24-"+dev, "/ip/service")); len(got) != 0 {
			t.Errorf("%s: want no open service, got %v", dev, got)
		}
	}
}

func TestOpenMgmtEmptyListAndPublic(t *testing.T) {
	rows := []poller.Row{
		{"name": "ssh", "available-from": ""},
		{"name": "ssh", "dynamic": "true", "connection": "true"}, // session row: never a service
		{"name": "www", "available-from": "0.0.0.0/0"},
		{"name": "winbox", "available-from": "192.168.0.0/24,203.0.113.0/24"}, // one public range = open
		{"name": "api", "address": "10.0.0.0/8"},                              // RouterOS < 7.x field
		{"name": "api-ssl", "available-from": "::/0"},
		{"name": "www-ssl", "available-from": "100.64.0.0/10,fd00::/8,fe80::/10"},
		{"name": "ftp", "available-from": ""}, // not a management service here
		{"name": "ssh", "available-from": ""},
	}
	want := []string{"ssh", "www", "winbox", "api-ssl"}
	if got := openMgmtNames(rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestPinnedMACsReal724(t *testing.T) {
	got := pinnedMACs(fixtureRows(t, "real-7.24-hex", "/interface/wifi/access-list"))
	if len(got) != 2 {
		t.Fatalf("two distinct pinned clients expected, got %v", got)
	}
}
