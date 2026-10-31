// SPDX-License-Identifier: AGPL-3.0-only

package poller

import (
	"context"
	"net"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/mockros"
)

func TestSummariseExport(t *testing.T) {
	text := "# 2026-10-25 by RouterOS 7.24.4\\n/ip service\\nset www-ssl disabled=no\\n/user\\nadd name=ops password=hunter2\\nadd name=x password=\\\"\\\"\\n"
	for _, c := range []struct {
		name   string
		status int
		body   string
		want   ExportResult
	}{
		{"inline in ret", 200, `[{"ret":"` + text + `"}]`, ExportResult{Status: 200, Inline: true, Lines: 5, SecretLines: 1}},
		{"bare string", 200, `"` + text + `"`, ExportResult{Status: 200, Inline: true, Lines: 5, SecretLines: 1}},
		{"file name only", 200, `[{"ret":"export.rsc"}]`, ExportResult{Status: 200, Lines: 1}},
		{"empty", 200, `[]`, ExportResult{Status: 200}},
	} {
		if got := summariseExport(c.status, []byte(c.body), nil); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
	got := summariseExport(403, []byte(`{"error":403,"message":"Forbidden"}`), nil)
	if got.Status != 403 || got.Inline || got.Error == "" {
		t.Errorf("forbidden: %+v", got)
	}
}

// The probe is fixed (terse export, no file argument) and reports only counts: against the mock a read-only user
// is refused by default and gets inline text once the router allows it.
func TestExportProbeReadOnlyUser(t *testing.T) {
	rt := mockros.New("gw", "router", "admin", "adminpw", nil)
	ts := httptest.NewServer(rt.Handler())
	defer ts.Close()
	host, port, _ := net.SplitHostPort(ts.Listener.Addr().String())
	p, _ := strconv.Atoi(port)
	dev := func(user, pass string) config.Device {
		return config.Device{Name: "gw", Addr: host, Port: p, Scheme: "http", User: user, Pass: pass}
	}
	ctx := context.Background()
	admin := NewClient(dev("admin", "adminpw"))
	if _, err := admin.Do(ctx, "PUT", "/user/group", map[string]string{"name": "mtmon-ro", "policy": "read,api,rest-api"}); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Do(ctx, "PUT", "/user", map[string]string{"name": "mtmon", "group": "mtmon-ro", "password": "pw"}); err != nil {
		t.Fatal(err)
	}
	ro := NewClient(dev("mtmon", "pw"))
	if r := ro.ExportProbe(ctx); r.Status != 403 || r.Inline {
		t.Fatalf("refused by default: %+v", r)
	}
	rt.ROExport = true
	if r := ro.ExportProbe(ctx); r.Status != 200 || !r.Inline || r.Lines != 6 || r.SecretLines != 1 {
		t.Fatalf("inline text: %+v", r)
	}
}
