// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"encoding/json"
	"net/url"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestInterfacesAPI(t *testing.T) {
	ts, s := newTestServer(t)
	_, c := login(t, ts, "test-password-1")
	get := func(p string, out any) {
		t.Helper()
		r, err := c.Get(ts.URL + p)
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("%s: %v %v", p, r, err)
		}
		json.NewDecoder(r.Body).Decode(out)
	}
	type lists struct {
		Interfaces []IfaceInfo       `json:"interfaces"`
		Wan        []IfaceInfo       `json:"wan"`
		Matrix     []json.RawMessage `json:"matrix"`
	}
	var empty lists
	get("/api/interfaces?range=24h", &empty)
	if empty.Interfaces == nil || empty.Wan == nil || empty.Matrix == nil {
		t.Fatalf("lists must be [] not null: %+v", empty)
	}

	now := time.Now().Unix()
	row := func(mac, dir string, ifc, ifr uint32, b uint64) store.FlowRow {
		return store.FlowRow{TS: now, Exporter: "10.0.0.1", Dev: "r1", MAC: mac, CIP: "10.0.0.5", RIP: "93.184.216.34", RPort: 443, Proto: 6,
			Dir: dir, Bytes: b, IfC: ifc, IfR: ifr, Svc: "https"}
	}
	s.St.Enqueue(row("aa:00:00:00:00:01", "u", 10, 1, 1000), row("aa:00:00:00:00:02", "u", 2, 1, 300), row("aa:00:00:00:00:01", "i", 10, 2, 700))
	if err := s.St.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := s.St.SetIfNames("r1", []store.IfName{{Idx: 1, Name: "ether1", Type: "ether"}, {Idx: 10, Name: "vlan10", Type: "vlan"}}); err != nil {
		t.Fatal(err)
	}
	var l lists
	get("/api/interfaces?range=24h&own=1", &l)
	names := map[uint32]string{}
	for _, i := range l.Interfaces {
		names[i.Idx] = i.Name
	}
	if names[10] != "vlan10" || names[2] != "if2" { // listed -> name, not listed -> if<N>, never guessed
		t.Errorf("names: %v", names)
	}
	if len(l.Wan) != 1 || l.Wan[0].Name != "ether1" || len(l.Matrix) != 1 {
		t.Errorf("wan/matrix: %+v", l)
	}

	var top struct {
		Rows []store.TopRow `json:"rows"`
	}
	get("/api/top/clients?range=24h&own=1&iface="+url.QueryEscape("r1:10"), &top)
	if len(top.Rows) != 1 || top.Rows[0].Key != "aa:00:00:00:00:01" {
		t.Errorf("filtered top: %+v", top.Rows)
	}
	for _, bad := range []string{"", "garbage", "r1:999", "other:10", "r1:"} { // never an error or null
		var res struct {
			Rows []store.TopRow `json:"rows"`
		}
		rows := &res.Rows
		get("/api/top/hosts?range=24h&iface="+url.QueryEscape(bad), &res)
		if bad != "" && *rows == nil {
			t.Errorf("iface=%q: list is null", bad)
		}
		if bad == "" && len(*rows) == 0 {
			t.Error("without iface the plain list stays unchanged")
		}
	}
	r, _ := c.Get(ts.URL + "/api/export/top/clients?range=24h&iface=" + url.QueryEscape("r1:10"))
	if r.StatusCode != 200 {
		t.Errorf("export with iface: %d", r.StatusCode)
	}
	r, _ = c.Get(ts.URL + "/api/export/interfaces?range=24h")
	if r.StatusCode != 200 {
		t.Errorf("interfaces export: %d", r.StatusCode)
	}
	var in struct {
		WanUsage []IfaceInfo `json:"wan_usage"`
	}
	get("/api/internet?range=24h", &in)
	if len(in.WanUsage) != 1 {
		t.Errorf("internet wan_usage: %+v", in.WanUsage)
	}
}
