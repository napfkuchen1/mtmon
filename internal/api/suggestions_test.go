package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/config"
)

func TestSuggestionsAPI(t *testing.T) {
	ts, s := newTestServer(t)
	for _, p := range []string{"/api/suggestions", "/api/suggestions/summary"} {
		if r, _ := http.Get(ts.URL + p); r.StatusCode != 401 {
			t.Errorf("%s without session: %d", p, r.StatusCode)
		}
	}
	if r, _ := http.Post(ts.URL+"/api/suggestions/x/dismiss", "application/json", strings.NewReader("{}")); r.StatusCode != 401 {
		t.Errorf("dismiss without session: %d", r.StatusCode)
	}
	_, c := login(t, ts, "test-password-1")
	get := func(p string) map[string]any {
		t.Helper()
		resp, err := c.Get(ts.URL + p)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("%s: %v %v", p, err, resp)
		}
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		return m
	}
	m := get("/api/suggestions")
	if _, ok := m["suggestions"].([]any); !ok {
		t.Fatalf("suggestions must be a list, never null: %v", m)
	}
	if _, ok := m["skipped"].([]any); !ok {
		t.Fatalf("skipped must be a list: %v", m)
	}
	// seed a RAM warning: 100 samples at 95 % memory
	for i := 0; i < 100; i++ {
		s.St.DB.Exec(`INSERT INTO dev_metrics(ts,device,cpu,mem_used,mem_total,temp,uptime,clients,up) VALUES(strftime('%s','now')-?,?,?,?,?,?,?,?,1)`,
			i*60, "wAP", 10.0, 95, 100, 0, 1000, 0)
	}
	s.Cfg.Devices = append(s.Cfg.Devices, config.Device{Name: "wAP", Addr: "10.0.0.9", Role: "ap"})
	m = get("/api/suggestions?refresh=1")
	var id string
	for _, x := range m["suggestions"].([]any) {
		it := x.(map[string]any)
		if it["rule"] == "ram" {
			id = it["id"].(string)
			if it["severity"] != "warning" || it["hidden"] != false {
				t.Fatalf("%v", it)
			}
		}
	}
	if id != "ram:wap" {
		t.Fatalf("ram suggestion missing: %v", m)
	}
	if w := get("/api/suggestions/summary")["warnings"].(float64); w != 1 {
		t.Fatalf("badge count %v", w)
	}
	post := func(path, body string, want int) {
		t.Helper()
		resp, err := c.Post(ts.URL+path, "application/json", strings.NewReader(body))
		if err != nil || resp.StatusCode != want {
			t.Fatalf("POST %s: %v %v (want %d)", path, err, resp, want)
		}
	}
	post("/api/suggestions/"+id+"/dismiss", `{"days":7}`, 200)
	m = get("/api/suggestions")
	if m["counts"].(map[string]any)["hidden"].(float64) != 1 || get("/api/suggestions/summary")["warnings"].(float64) != 0 {
		t.Fatalf("dismissed suggestion must be hidden: %v", m["counts"])
	}
	post("/api/suggestions/"+id+"/restore", ``, 200)
	if get("/api/suggestions/summary")["warnings"].(float64) != 1 {
		t.Fatal("restore failed")
	}
	post("/api/suggestions/"+id+"/dismiss", ``, 200) // forever
	until := s.St.DB.QueryRow(`SELECT until FROM advice_dismissed WHERE id=?`, id)
	var u int64 = -1
	until.Scan(&u)
	if u != 0 {
		t.Fatalf("forever = until 0, got %d", u)
	}
	post("/api/suggestions/BAD%20ID/dismiss", `{}`, 400)
	post("/api/suggestions/"+id+"/dismiss", `{"days":-1}`, 400)
	// cross-origin POSTs are refused like on every other mutating endpoint
	req, _ := http.NewRequest("POST", ts.URL+"/api/suggestions/"+id+"/restore", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://evil.example")
	if resp, _ := c.Do(req); resp.StatusCode != 403 {
		t.Fatalf("CSRF: %d", resp.StatusCode)
	}
}
