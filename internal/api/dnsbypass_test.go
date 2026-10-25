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
}
