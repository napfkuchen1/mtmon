package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestGroupsEndToEnd(t *testing.T) {
	ts, s := newTestServer(t)
	now := time.Now().Unix()
	s.St.DB.Exec(`INSERT INTO clients(mac,hostname,vendor,first_seen,last_seen,online) VALUES
		('AA:00:00:00:00:01','shelly-plug','Espressif',?,?,1), ('AA:00:00:00:00:02','nas','Synology',?,?,1), ('AA:00:00:00:00:03','laptop','Dell',?,?,0)`, now, now, now, now, now, now)
	_, c := login(t, ts, "test-password-1")
	call := func(method, path, body string) (int, map[string]any) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		return r.StatusCode, m
	}
	if code, _ := call("PUT", "/api/clients/AA:00:00:00:00:01/group", `{"group":"IoT"}`); code != 400 {
		t.Fatalf("unknown group must be refused: %d", code)
	}
	if code, m := call("POST", "/api/groups/presets", `{}`); code != 200 || m["added"] != float64(len(store.GroupPresets)) {
		t.Fatalf("presets: %d %v", code, m)
	}
	if code, m := call("POST", "/api/groups/presets", `{}`); code != 200 || m["added"] != float64(0) {
		t.Fatalf("presets are added once: %d %v", code, m)
	}
	if code, _ := call("PUT", "/api/groups/bad%2Fname", `{}`); code != 400 {
		t.Fatalf("invalid name: %d", code)
	}
	// suggestions: IoT for the plug, Servers for the NAS, nothing for the laptop; auto applies them
	r, _ := c.Get(ts.URL + "/api/clients?range=1h")
	var cl []struct {
		Client    store.Client `json:"client"`
		Suggested string       `json:"suggested_group"`
	}
	json.NewDecoder(r.Body).Decode(&cl)
	sug := map[string]string{}
	for _, x := range cl {
		sug[x.Client.MAC] = x.Suggested
	}
	if sug["AA:00:00:00:00:01"] != "IoT" || sug["AA:00:00:00:00:02"] != "Servers" || sug["AA:00:00:00:00:03"] != "" {
		t.Fatalf("suggestions: %v", sug)
	}
	if code, m := call("POST", "/api/clients/group", `{"macs":["AA:00:00:00:00:01","AA:00:00:00:00:02","AA:00:00:00:00:03"],"auto":true}`); code != 200 || m["count"] != float64(2) {
		t.Fatalf("auto: %d %v", code, m)
	}
	nas, _ := s.St.Client("AA:00:00:00:00:02")
	if nas.Group != "Servers" || !nas.Watch { // the Servers preset watches its clients
		t.Errorf("nas: %+v", nas)
	}
	// filter and top list by group
	r, _ = c.Get(ts.URL + "/api/clients?range=1h&group=IoT")
	cl = nil
	json.NewDecoder(r.Body).Decode(&cl)
	if len(cl) != 1 || cl[0].Client.MAC != "AA:00:00:00:00:01" {
		t.Errorf("group filter: %+v", cl)
	}
	r, _ = c.Get(ts.URL + "/api/clients?range=1h&group=none")
	cl = nil
	json.NewDecoder(r.Body).Decode(&cl)
	if len(cl) != 1 || cl[0].Client.MAC != "AA:00:00:00:00:03" {
		t.Errorf("ungrouped filter: %+v", cl)
	}
	// stats and delete
	r, _ = c.Get(ts.URL + "/api/groups")
	var gs struct {
		Groups []store.GroupStat `json:"groups"`
	}
	json.NewDecoder(r.Body).Decode(&gs)
	found := map[string]int{}
	for _, g := range gs.Groups {
		found[g.Name] = g.Clients
	}
	if found["IoT"] != 1 || found["Servers"] != 1 || found["Guests"] != 0 {
		t.Errorf("stats: %v", found)
	}
	if code, _ := call("DELETE", "/api/groups/IoT", ""); code != 200 {
		t.Fatal(code)
	}
	if c1, _ := s.St.Client("AA:00:00:00:00:01"); c1.Group != "" {
		t.Errorf("deleting a group ungroups its clients: %+v", c1)
	}
}
