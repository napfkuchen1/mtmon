package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestDNSBypassAPI(t *testing.T) {
	ts, s := newTestServer(t)
	_, c := login(t, ts, "test-password-1")
	mac := "AA:00:00:00:00:01"
	s.St.DB.Exec(`INSERT INTO clients(mac,hostname,first_seen,last_seen,online) VALUES(?,?,1,1,1)`, mac, "ha-server")
	now := time.Now().Unix()
	for i := 0; i < 6; i++ {
		s.St.Enqueue(store.FlowRow{TS: now, MAC: mac, CIP: "192.168.0.9", RIP: "1.1.1.1", RPort: 853, CPort: uint16(41000 + i), Proto: 6, Dir: "u", Bytes: 500})
	}
	if err := s.St.Flush(); err != nil {
		t.Fatal(err)
	}
	get := func(path string, out any) {
		r, err := c.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s: %v %s", path, err, b)
		}
	}
	var list struct {
		Total   int `json:"total"`
		Clients []struct {
			MAC   string   `json:"mac"`
			Name  string   `json:"name"`
			Types []string `json:"types"`
		} `json:"clients"`
		Alert bool `json:"alert"`
	}
	get("/api/dns-bypass", &list)
	if list.Total != 1 || list.Clients[0].MAC != mac || list.Clients[0].Name != "ha-server" || list.Clients[0].Types[0] != "DoT" || list.Alert {
		t.Fatalf("list: %+v", list)
	}
	var cl []struct {
		Client struct {
			MAC string `json:"mac"`
		} `json:"client"`
		Bypass []string `json:"dns_bypass"`
	}
	get("/api/clients", &cl)
	if len(cl) != 1 || len(cl[0].Bypass) != 1 || cl[0].Bypass[0] != "DoT" {
		t.Fatalf("clients: %+v", cl)
	}
	var det struct {
		Bypass *struct {
			Types []string `json:"types"`
		} `json:"dns_bypass"`
	}
	get("/api/clients/"+mac, &det)
	if det.Bypass == nil || det.Bypass.Types[0] != "DoT" {
		t.Fatalf("detail: %+v", det)
	}
	req, _ := http.NewRequest("PUT", ts.URL+"/api/dns-bypass", strings.NewReader(`{"alert":true}`))
	if r, err := c.Do(req); err != nil || r.StatusCode != 200 {
		t.Fatalf("save: %v %v", err, r)
	}
	get("/api/dns-bypass", &list)
	if !list.Alert {
		t.Error("alert setting not kept")
	}
	var short struct {
		Total int    `json:"total"`
		Range string `json:"range"`
	}
	get("/api/dns-bypass?range=1h", &short)
	if short.Range != "1h" || short.Total != 1 {
		t.Fatalf("range 1h: %+v", short)
	}
	get("/api/dns-bypass?range=bogus", &short)
	if short.Range != "7d" {
		t.Errorf("bad range not defaulted: %+v", short)
	}

	// mark as fixed: the old flows no longer count, the client is under observation
	post := func(body string) {
		r, err := c.Post(ts.URL+"/api/dns-bypass/reset", "application/json", strings.NewReader(body))
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("reset: %v %v", err, r)
		}
		r.Body.Close()
	}
	post(`{"mac":"` + mac + `"}`)
	var after struct {
		Total     int        `json:"total"`
		Clients   []struct{} `json:"clients"`
		Observing []struct {
			MAC string `json:"mac"`
		} `json:"observing"`
	}
	get("/api/dns-bypass", &after)
	if after.Total != 0 || len(after.Clients) != 0 || len(after.Observing) != 1 || after.Observing[0].MAC != mac {
		t.Fatalf("after reset: %+v", after)
	}
	get("/api/clients", &cl)
	if len(cl[0].Bypass) != 0 {
		t.Errorf("badge after reset: %+v", cl)
	}
	// a new flow in a later hour flags the client again
	later := now/3600*3600 + 2*3600 + 60
	for i := 0; i < 6; i++ {
		s.St.Enqueue(store.FlowRow{TS: later, MAC: mac, CIP: "192.168.0.9", RIP: "1.1.1.1", RPort: 853, CPort: uint16(42000 + i), Proto: 6, Dir: "u", Bytes: 500})
	}
	if err := s.St.Flush(); err != nil {
		t.Fatal(err)
	}
	s.bypass.mu.Lock()
	s.bypass.by = nil // the 5 min cache would hide the new flows
	s.bypass.mu.Unlock()
	get("/api/dns-bypass", &after)
	if len(after.Clients) != 1 || len(after.Observing) != 0 {
		t.Fatalf("after new flows: %+v", after)
	}
	post(`{"all":true}`) // flows of the later hour are still newer than the reset, so the client stays flagged
}

func TestClientDetailIPv6Prefixes(t *testing.T) {
	ts, s := newTestServer(t)
	_, c := login(t, ts, "test-password-1")
	mac := "AA:00:00:00:00:09"
	s.St.DB.Exec(`INSERT INTO clients(mac,hostname,first_seen,last_seen,online) VALUES(?,?,1,1,1)`, mac, "nas")
	get := func() map[string]json.RawMessage {
		r, err := c.Get(ts.URL + "/api/clients/" + mac)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var m map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&m)
		return m
	}
	if string(get()["ipv6_prefixes"]) != "[]" {
		t.Fatalf("no history: must be [] not null: %s", get()["ipv6_prefixes"])
	}
	now := time.Now().Unix()
	s.St.NoteIPv6(map[string]string{"2001:db8:1:1::10": mac}, now-86400)
	s.St.NoteIPv6(map[string]string{"2001:db8:1:2::10": mac}, now)
	var ps []store.V6Prefix
	json.Unmarshal(get()["ipv6_prefixes"], &ps)
	if len(ps) != 2 || ps[0].Prefix != "2001:db8:1:2::/64" {
		t.Errorf("%+v", ps)
	}
}
