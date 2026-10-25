package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/enrich"
	"github.com/napfkuchen1/mtmon/internal/flow"
	"github.com/napfkuchen1/mtmon/internal/live"
	"github.com/napfkuchen1/mtmon/internal/store"
)

func newTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	cfg := &config.Config{DataDir: t.TempDir(), NoTLS: true, AdminUser: "admin"}
	cfg.Defaults()
	cfg.AdminHash, _ = HashPassword("test-password-1")
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hub := live.NewHub()
	en := enrich.New("", "", "", false)
	s := &Server{Cfg: cfg, St: st, Hub: hub, En: en, Col: &flow.Collector{Dec: flow.NewDecoder()},
		Pipe: live.NewPipeline(cfg, st, en, hub), Version: "test",
		DiskFn: func(string) (int64, int64, bool) { return 80 << 30, 100 << 30, true }, // plenty free, whatever this host has
		UI:     fstest.MapFS{"index.html": {Data: []byte("<html>spa</html>")}, "assets/a.js": {Data: []byte("x")}}}
	ts := httptest.NewServer(New(s))
	t.Cleanup(ts.Close)
	return ts, s
}

func login(t *testing.T, ts *httptest.Server, pw string) (*http.Response, *http.Client) {
	t.Helper()
	jar := &cookieJar{}
	c := &http.Client{Jar: jar}
	resp, err := c.Post(ts.URL+"/api/login", "application/json", strings.NewReader(`{"user":"admin","password":"`+pw+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	return resp, c
}

type cookieJar struct{ cs []*http.Cookie }

func (j *cookieJar) SetCookies(_ *url.URL, cs []*http.Cookie) { j.cs = append(j.cs, cs...) }
func (j *cookieJar) Cookies(_ *url.URL) []*http.Cookie        { return j.cs }

func TestAuthRequired(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, p := range []string{"/api/overview", "/api/clients", "/api/top/hosts", "/api/system", "/api/live", "/api/topology", "/api/export/top/hosts"} {
		resp, _ := http.Get(ts.URL + p)
		if resp.StatusCode != 401 {
			t.Errorf("%s without session: %d, want 401", p, resp.StatusCode)
		}
	}
	resp, _ := http.Get(ts.URL + "/api/me")
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	if resp.StatusCode != 200 || m["user"] != nil {
		t.Errorf("/api/me logged out: %d %v", resp.StatusCode, m)
	}
	if r, _ := http.Get(ts.URL + "/healthz"); r.StatusCode != 200 {
		t.Error("healthz must be public")
	}
}

func TestLoginFlow(t *testing.T) {
	ts, _ := newTestServer(t)
	if resp, _ := login(t, ts, "wrong"); resp.StatusCode != 401 {
		t.Fatalf("bad password: %d", resp.StatusCode)
	}
	resp, c := login(t, ts, "test-password-1")
	if resp.StatusCode != 200 {
		t.Fatalf("good password: %d", resp.StatusCode)
	}
	ck := resp.Header.Get("Set-Cookie")
	for _, want := range []string{"HttpOnly", "SameSite=Strict", "mtmon_session="} {
		if !strings.Contains(ck, want) {
			t.Errorf("cookie missing %s: %s", want, ck)
		}
	}
	r, _ := c.Get(ts.URL + "/api/clients")
	var arr []any
	json.NewDecoder(r.Body).Decode(&arr)
	if r.StatusCode != 200 || arr == nil {
		t.Fatalf("clients: %d %v (must be [] not null)", r.StatusCode, arr)
	}
	for _, p := range []string{"/api/overview?range=24h", "/api/devices", "/api/alerts", "/api/setup", "/api/system", "/api/topology", "/api/series?range=30d"} {
		r, _ := c.Get(ts.URL + p)
		if r.StatusCode != 200 {
			t.Errorf("%s: %d", p, r.StatusCode)
		}
	}
	if r, _ := c.Get(ts.URL + "/api/top/bogus"); r.StatusCode != 400 {
		t.Errorf("unknown top list should be 400, got %d", r.StatusCode)
	}
	if r, _ := c.Get(ts.URL + "/api/clients/ZZ:ZZ"); r.StatusCode != 404 {
		t.Errorf("unknown client should be 404, got %d", r.StatusCode)
	}
	// cross-origin state change is blocked
	req, _ := http.NewRequest("POST", ts.URL+"/api/alerts/1/ack", nil)
	req.Header.Set("Origin", "https://evil.example")
	if r, _ := c.Do(req); r.StatusCode != 403 {
		t.Errorf("cross-origin POST: %d, want 403", r.StatusCode)
	}
}

func TestLoginRateLimit(t *testing.T) {
	ts, _ := newTestServer(t)
	var last int
	for i := 0; i < 8; i++ {
		resp, _ := login(t, ts, "nope")
		last = resp.StatusCode
	}
	if last != 429 {
		t.Fatalf("after 8 failures want 429, got %d", last)
	}
	if resp, _ := login(t, ts, "test-password-1"); resp.StatusCode != 429 {
		t.Fatalf("even the right password is throttled during lockout: %d", resp.StatusCode)
	}
}

func TestNoPasswordNoLogin(t *testing.T) {
	ts, s := newTestServer(t)
	s.Cfg.AdminHash = ""
	if resp, _ := login(t, ts, ""); resp.StatusCode != 401 {
		t.Fatalf("empty hash must never allow login: %d", resp.StatusCode)
	}
}

func TestStaticAndHeaders(t *testing.T) {
	ts, _ := newTestServer(t)
	r, _ := http.Get(ts.URL + "/some/spa/route")
	if r.StatusCode != 200 {
		t.Fatalf("spa fallback: %d", r.StatusCode)
	}
	for h, want := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY"} {
		if r.Header.Get(h) != want {
			t.Errorf("header %s = %q", h, r.Header.Get(h))
		}
	}
	if !strings.Contains(r.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Error("CSP missing")
	}
	if r, _ := http.Get(ts.URL + "/../../etc/passwd"); r.StatusCode == 200 && strings.Contains(r.Header.Get("Content-Type"), "text/plain") {
		t.Error("path traversal served a file")
	}
}

func TestCSVInjection(t *testing.T) {
	for in, want := range map[string]string{"=cmd()": "'=cmd()", "+1": "'+1", "-2": "'-2", "@x": "'@x", "normal": "normal", "": ""} {
		if got := csvSafe(in); got != want {
			t.Errorf("csvSafe(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSanitize(t *testing.T) {
	if got := sanitize(`hosts"; rm -rf /`); strings.ContainsAny(got, `";/ `) {
		t.Errorf("unsafe filename %q", got)
	}
}

func TestDeviceMaintenanceWindow(t *testing.T) {
	ts, s := newTestServer(t)
	s.Cfg.Devices = append(s.Cfg.Devices, config.Device{Name: "ap1", Addr: "192.0.2.10", Role: "ap"})
	_, c := login(t, ts, "test-password-1")
	put := func(path, body string) int {
		req, _ := http.NewRequest("PUT", ts.URL+path, strings.NewReader(body))
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if code := put("/api/devices/nope/maintenance", `{"minutes":30}`); code != 404 {
		t.Errorf("unknown device: %d", code)
	}
	if code := put("/api/devices/ap1/maintenance", `{"minutes":999999}`); code != 400 {
		t.Errorf("too long: %d", code)
	}
	if code := put("/api/devices/ap1/maintenance", `{"minutes":30}`); code != 200 || s.St.Maintenance()["ap1"] == 0 {
		t.Errorf("start: %d %v", code, s.St.Maintenance())
	}
	if code := put("/api/devices/ap1/maintenance", `{"minutes":0}`); code != 200 || len(s.St.Maintenance()) != 0 {
		t.Errorf("end: %d %v", code, s.St.Maintenance())
	}
}

func TestSystemReportsOwnHealth(t *testing.T) {
	ts, s := newTestServer(t)
	s.Started = time.Now()
	s.St.DB.Exec(`INSERT INTO db_size_log VALUES(?,?),(?,?)`, time.Now().Unix()-86400, 1<<30, time.Now().Unix(), 2<<30)
	_, c := login(t, ts, "test-password-1")
	r, _ := c.Get(ts.URL + "/api/system")
	var m map[string]float64
	json.NewDecoder(r.Body).Decode(&m)
	for _, k := range []string{"rss_bytes", "cpu_pct", "data_dir_free_bytes", "data_dir_total_bytes", "db_growth_bytes_per_day", "disk_days_left"} {
		if _, ok := m[k]; !ok {
			t.Errorf("/api/system lacks %s: %v", k, m)
		}
	}
	if m["data_dir_free_bytes"] != float64(80<<30) || m["db_growth_bytes_per_day"] < 1e9 || m["disk_days_left"] != 80 {
		t.Errorf("%v", m)
	}
}
