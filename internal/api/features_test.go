package api

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestClassifyDests(t *testing.T) {
	now := time.Now().Unix()
	dests := []store.DestAgg{
		{RIP: "203.0.113.1", Host: "rr1.googlevideo.com", Proto: 6, Port: 443, Up: 10, Down: 1000, Flows: 3, Last: now - 600},
		{RIP: "203.0.113.2", Host: "rr2.googlevideo.com", Proto: 6, Port: 443, Up: 5, Down: 500, Flows: 2, Last: now - 60},
		{RIP: "203.0.113.3", Host: "", Proto: 6, Port: 443, Up: 1, Down: 300, Flows: 1, Last: now - 900}, // name from cache
		{RIP: "203.0.113.4", Host: "", Proto: 6, Port: 443, Up: 7, Down: 70, Flows: 1, Last: now - 900},  // unknown
		{RIP: "203.0.113.5", Host: "example.org", ASOrg: "Hoster", Proto: 6, Port: 443, Up: 1, Down: 1, Flows: 1},
	}
	recent := []store.DestAgg{{RIP: "203.0.113.2", Host: "rr2.googlevideo.com", Proto: 6, Port: 443, Up: 1, Down: 50, Flows: 1, Last: now - 60},
		{RIP: "203.0.113.9", Host: "x.nflxvideo.net", Proto: 6, Port: 443, Up: 1, Down: 99, Flows: 1, Last: now - 30}}
	cache := func(a netip.Addr) string {
		if a == netip.MustParseAddr("203.0.113.3") {
			return "i.scdn.co"
		}
		return ""
	}
	res := classifyDests(dests, recent, cache, now)
	if len(res.Apps) != 3 {
		t.Fatalf("apps: %+v", res.Apps)
	}
	by := map[string]AppRow{}
	for _, a := range res.Apps {
		by[a.Key] = a
	}
	yt := by["youtube"]
	if yt.Down != 1500 || yt.Up != 15 || yt.Dests != 2 || !yt.Active || yt.LastSeen != now-60 {
		t.Errorf("youtube: %+v", yt)
	}
	if sp := by["spotify"]; sp.Down != 300 || sp.Active {
		t.Errorf("spotify via cached name: %+v", sp)
	}
	if nf := by["netflix"]; !nf.Active || nf.Down != 99 { // active but outside the main list
		t.Errorf("netflix: %+v", nf)
	}
	if res.UnknownBytes != 77+2 || res.KnownBytes != 1515+301 {
		t.Errorf("bytes: known=%d unknown=%d", res.KnownBytes, res.UnknownBytes)
	}
	if !res.Apps[0].Active {
		t.Error("active apps sort first")
	}
	// nothing known -> empty list, never nil
	if r := classifyDests(nil, nil, nil, now); r.Apps == nil || len(r.Apps) != 0 {
		t.Errorf("empty: %+v", r)
	}
}

func TestCleanupEndpointAndApps(t *testing.T) {
	ts, s := newTestServer(t)
	old := time.Now().Add(-200 * 24 * time.Hour).Unix()
	s.St.DB.Exec(`INSERT INTO clients(mac,hostname,vendor,ip,label,first_seen,last_seen,online) VALUES('AA:00:00:00:00:01','old','V','10.0.0.5','',?,?,0)`, old, old)
	s.St.DB.Exec(`INSERT INTO clients(mac,hostname,vendor,ip,label,first_seen,last_seen,online) VALUES('AA:00:00:00:00:02','now','V','10.0.0.6','',?,?,1)`, old, time.Now().Unix())
	now := time.Now().Unix() / 60 * 60
	s.St.DB.Exec(`INSERT INTO rollup_1m(ts,mac,rip,rport,proto,svc,host,cc,asn,asorg,b_up,b_down,b_int,flows) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		now, "AA:00:00:00:00:02", "203.0.113.1", 443, 6, "HTTPS", "r1.googlevideo.com", "", 0, "", 10, 4000, 0, 2)
	_, c := login(t, ts, "test-password-1")

	post := func(body, origin string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", ts.URL+"/api/clients/cleanup", strings.NewReader(body))
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		return r.StatusCode, m
	}
	if code, _ := post(`{"days":-1}`, ""); code != 400 {
		t.Errorf("negative days: %d", code)
	}
	if code, _ := post(`{"days":90}`, "https://evil.example"); code != 403 {
		t.Errorf("cross-origin: %d", code)
	}
	if r, _ := http.Post(ts.URL+"/api/clients/cleanup", "application/json", strings.NewReader(`{"days":90}`)); r.StatusCode != 401 {
		t.Errorf("no session: %d", r.StatusCode)
	}
	code, m := post(`{"days":90,"dry_run":true,"keep_labeled":true}`, "")
	if code != 200 || m["count"].(float64) != 1 || m["removed"].(float64) != 0 || len(m["sample"].([]any)) != 1 {
		t.Errorf("dry run: %d %v", code, m)
	}
	code, m = post(`{"days":90,"keep_labeled":true}`, "")
	if code != 200 || m["removed"].(float64) != 1 {
		t.Errorf("real: %d %v", code, m)
	}
	if n, _ := s.St.Clients(); len(n) != 1 || n[0].MAC != "AA:00:00:00:00:02" {
		t.Errorf("remaining clients: %+v", n)
	}

	r, _ := c.Get(ts.URL + "/api/clients/AA:00:00:00:00:02/apps?range=1h")
	var res struct {
		Apps []AppRow `json:"apps"`
	}
	json.NewDecoder(r.Body).Decode(&res)
	if r.StatusCode != 200 || len(res.Apps) != 1 || res.Apps[0].Key != "youtube" || !res.Apps[0].Active || res.Apps[0].Down != 4000 {
		t.Errorf("client apps: %d %+v", r.StatusCode, res)
	}
	if r, _ := c.Get(ts.URL + "/api/clients/FF:FF:FF:FF:FF:FF/apps"); r.StatusCode != 404 {
		t.Errorf("unknown client apps: %d", r.StatusCode)
	}
	r, _ = c.Get(ts.URL + "/api/apps?range=1h")
	res.Apps = nil
	json.NewDecoder(r.Body).Decode(&res)
	if r.StatusCode != 200 || len(res.Apps) != 1 {
		t.Errorf("global apps: %d %+v", r.StatusCode, res)
	}
}

func TestFirewallNamesEndpoints(t *testing.T) {
	ts, s := newTestServer(t)
	s.St.DB.Exec(`INSERT INTO clients(mac,hostname,vendor,ip,label,first_seen,last_seen,online) VALUES('AA:00:00:00:00:02','pixel','V','10.0.0.6','Anna phone',1,?,1)`, time.Now().Unix())
	now := time.Now().Unix()
	s.St.DB.Exec(`INSERT INTO fw_rules(device,prefix,chain,action,descr,managed) VALUES('gw','mtmon-drop','forward','drop','x',1)`)
	s.St.DB.Exec(`INSERT INTO fw_events(ts,device,prefix,chain,in_if,out_if,proto,flags,src,sport,dst,dport,nat,len,mac,cstate) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		now, "gw", "mtmon-drop", "forward", "", "", "TCP", "", "10.0.0.6", 5000, "203.0.113.8", 443, "", 60, "", "")
	_, c := login(t, ts, "test-password-1")
	r, _ := c.Get(ts.URL + "/api/firewall/events?range=1h")
	var ev []map[string]any
	json.NewDecoder(r.Body).Decode(&ev)
	if len(ev) != 1 || ev[0]["src_name"] != "Anna phone" || ev[0]["src"] != "10.0.0.6" {
		t.Fatalf("events: %v", ev)
	}
	if _, has := ev[0]["dst_name"]; has {
		t.Errorf("unknown external IP must not get an invented name: %v", ev[0])
	}
	r, _ = c.Get(ts.URL + "/api/firewall/summary?range=1h")
	var sum struct {
		Summary struct {
			Top []map[string]any `json:"top_blocked_src"`
		} `json:"summary"`
	}
	json.NewDecoder(r.Body).Decode(&sum)
	if len(sum.Summary.Top) != 1 || sum.Summary.Top[0]["name"] != "Anna phone" || sum.Summary.Top[0]["key"] != "10.0.0.6" {
		t.Errorf("summary: %v", sum.Summary.Top)
	}
}
