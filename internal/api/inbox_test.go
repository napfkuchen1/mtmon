package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestInboxReviewSearchEndpoints(t *testing.T) {
	ts, s := newTestServer(t)
	now := time.Now().Unix()
	s.St.DB.Exec(`INSERT INTO clients(mac,hostname,vendor,ip,first_seen,last_seen,online,reviewed) VALUES('AA:00:00:00:00:01','Max-iPhone','Apple','10.0.0.5',?,?,1,0)`, now, now)
	_, c := login(t, ts, "test-password-1")

	get := func(path string, v any) {
		r, err := c.Get(ts.URL + path)
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("%s: %v %v", path, err, r)
		}
		json.NewDecoder(r.Body).Decode(v)
	}
	var inbox struct {
		Clients []struct{ MAC, Suggest, Conf string } `json:"clients"`
		New     int                                   `json:"new"`
	}
	get("/api/clients/inbox", &inbox)
	if inbox.New != 1 || len(inbox.Clients) != 1 || inbox.Clients[0].Suggest != "" { // "Max-iPhone" is already a fine name
		t.Fatalf("inbox: %+v", inbox)
	}
	s.St.DB.Exec(`UPDATE clients SET hostname='iphone-3f9a2c' WHERE mac='AA:00:00:00:00:01'`)
	get("/api/clients/inbox", &inbox)
	if len(inbox.Clients) != 1 || inbox.Clients[0].Suggest != "iphone" {
		t.Fatalf("inbox: %+v", inbox)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/api/clients/review", strings.NewReader(`{"items":[{"mac":"AA:00:00:00:00:01","label":"Max iPhone"}]}`))
	if r, err := c.Do(req); err != nil || r.StatusCode != 200 {
		t.Fatalf("review: %v %v", err, r)
	}
	get("/api/clients/inbox", &inbox)
	if inbox.New != 0 || len(inbox.Clients) != 0 {
		t.Fatalf("after review: %+v", inbox)
	}
	var idx struct {
		Clients  []struct{ Name string } `json:"clients"`
		Devices  []any                   `json:"devices"`
		Services []string                `json:"services"`
	}
	get("/api/search/index", &idx)
	if len(idx.Clients) != 1 || idx.Clients[0].Name != "Max iPhone" || idx.Devices == nil || idx.Services == nil {
		t.Fatalf("index: %+v", idx)
	}
	var diag map[string]any
	get("/api/diagnostics", &diag)
	if diag["neighbors"] == nil || diag["clients"] == nil || diag["version"] == nil {
		t.Fatalf("diagnostics: %v", diag)
	}
}
