package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/speedtest"
	"github.com/napfkuchen1/mtmon/internal/store"
)

type apiFake struct{ block chan struct{} }

func (apiFake) Name() string { return "fake" }
func (f apiFake) Run(context.Context, speedtest.Opts) speedtest.Result {
	if f.block != nil {
		<-f.block
	}
	return speedtest.Result{Server: "=cmd|x", DownBps: 90e6, UpBps: 30e6, PingMS: 10, Bytes: 1 << 20}
}

func TestSpeedtestAPI(t *testing.T) {
	ts, s := newTestServer(t)
	block := make(chan struct{})
	s.Speed = &speedtest.Runner{St: s.St, Providers: func(speedtest.Settings) []speedtest.Provider { return []speedtest.Provider{apiFake{block}} }}
	_, c := login(t, ts, "test-password-1")
	do := func(method, path, body string, cl *http.Client, hdr map[string]string) (int, []byte) {
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		r, err := cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, b
	}
	// empty history: lists are [] not null
	code, b := do("GET", "/api/internet/speedtests", "", c, nil)
	if code != 200 || strings.Contains(string(b), `"items":null`) || !strings.Contains(string(b), `"items":[]`) {
		t.Fatalf("empty: %d %s", code, b)
	}
	// settings are validated
	if code, _ := do("PUT", "/api/internet/speedtest/settings", `{"interval_min":5}`, c, nil); code != 400 {
		t.Errorf("interval below 15 min: %d", code)
	}
	if code, b := do("PUT", "/api/internet/speedtest/settings", `{"enabled":true,"interval_min":60,"window_from":"01:00","window_to":"06:00","providers":["fake"]}`, c, nil); code != 400 {
		t.Errorf("unknown provider: %d %s", code, b)
	}
	if code, b := do("PUT", "/api/internet/speedtest/settings", `{"enabled":true,"interval_min":60,"window_from":"01:00","window_to":"06:00"}`, c, nil); code != 200 {
		t.Fatalf("save: %d %s", code, b)
	}
	// a token reads but cannot start a test or change the settings
	_, b = do("POST", "/api/tokens", `{"name":"ha"}`, c, nil)
	var tk struct {
		Token string `json:"token"`
	}
	json.Unmarshal(b, &tk)
	bearer := map[string]string{"Authorization": "Bearer " + tk.Token}
	anon := &http.Client{}
	if code, _ := do("GET", "/api/internet/speedtests", "", anon, bearer); code != 200 {
		t.Errorf("token read: %d", code)
	}
	if code, _ := do("POST", "/api/internet/speedtest", "", anon, bearer); code != 401 {
		t.Errorf("token must not start a test: %d", code)
	}
	if code, _ := do("PUT", "/api/internet/speedtest/settings", `{}`, anon, bearer); code != 401 {
		t.Errorf("token must not change settings: %d", code)
	}
	// run now: accepted, a second click is refused while it runs
	if code, b := do("POST", "/api/internet/speedtest", "", c, nil); code != 202 {
		t.Fatalf("run: %d %s", code, b)
	}
	if code, _ := do("POST", "/api/internet/speedtest", "", c, nil); code != 409 {
		t.Errorf("second run: %d", code)
	}
	close(block)
	var list struct {
		Items  []store.Speedtest `json:"items"`
		Status speedtest.Status  `json:"status"`
	}
	for i := 0; i < 200; i++ {
		_, b = do("GET", "/api/internet/speedtests", "", c, nil)
		json.Unmarshal(b, &list)
		if len(list.Items) == 1 && !list.Status.Running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(list.Items) != 1 || list.Items[0].DownBps != 90e6 {
		t.Fatalf("result: %+v", list)
	}
	// export: CSV neutralises a formula in text that came from a server
	code, b = do("GET", "/api/internet/speedtests/export?range=24h", "", c, nil)
	if code != 200 || !strings.Contains(string(b), "'=cmd|x") || !strings.Contains(string(b), "90.00") {
		t.Errorf("csv: %d %s", code, b)
	}
}

func TestSpeedtestImportAPI(t *testing.T) {
	ts, _ := newTestServer(t)
	_, c := login(t, ts, "test-password-1")
	post := func(body string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", ts.URL+"/api/internet/speedtests/import", strings.NewReader(body))
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		return r.StatusCode, m
	}
	csv := "id,ping,jitter,download,upload,time,type,created,error\n1,12,1,480.5,95,10,ookla,2025-03-01T10:00:00Z,\n2,0,0,0,0,0,ookla,2025-03-01T11:00:00Z,timeout\n"
	if code, m := post(csv); code != 200 || m["added"] != float64(2) || m["duplicates"] != float64(0) {
		t.Fatalf("first import: %d %v", code, m)
	}
	if code, m := post(csv); code != 200 || m["added"] != float64(0) || m["duplicates"] != float64(2) {
		t.Fatalf("second import: %d %v", code, m)
	}
	if code, _ := post("this is not a speed test export"); code != 400 {
		t.Errorf("garbage: %d", code)
	}
}

// The upstream list is part of the topology and the internet answer and is never null (the UI iterates it).
func TestUpstreamListIsNeverNull(t *testing.T) {
	ts, _ := newTestServer(t)
	_, c := login(t, ts, "test-password-1")
	for _, p := range []string{"/api/topology", "/api/internet"} {
		r, err := c.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if !strings.Contains(string(b), `"upstream":[]`) {
			t.Errorf("%s: %.200s", p, b)
		}
	}
}

func TestTimeZoneAPI(t *testing.T) {
	ts, s := newTestServer(t)
	_, c := login(t, ts, "test-password-1")
	orig := time.Local
	t.Cleanup(func() { time.Local = orig })
	put := func(body string) (int, string) {
		req, _ := http.NewRequest("PUT", ts.URL+"/api/timezone", strings.NewReader(body))
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}
	if code, _ := put(`{"zone":"Mars/Base"}`); code != 400 {
		t.Fatalf("unknown zone: %d", code)
	}
	code, b := put(`{"zone":"Europe/Berlin"}`)
	if code != 200 || !strings.Contains(b, `"zone":"Europe/Berlin"`) || !strings.Contains(b, `"set":true`) || s.St.GetMeta("timezone") != "Europe/Berlin" {
		t.Fatalf("%d %s", code, b)
	}
	if time.Local.String() != "Europe/Berlin" {
		t.Fatal(time.Local)
	}
	// an anchor read through time.Local follows the setting, including across the DST switch of 2026-10-25 (03:00 -> 02:00)
	before := time.Date(2026, 10, 24, 18, 21, 0, 0, time.Local)
	after := time.Date(2026, 10, 26, 18, 21, 0, 0, time.Local)
	if _, o := before.Zone(); o != 7200 {
		t.Fatalf("summer offset %d", o)
	}
	if _, o := after.Zone(); o != 3600 || before.UTC().Hour() != 16 || after.UTC().Hour() != 17 {
		t.Fatalf("winter offset %d, %v %v", o, before.UTC(), after.UTC())
	}
	if code, _ := put(`{"zone":""}`); code != 200 || time.Local != orig {
		t.Fatalf("reset: %d %v", code, time.Local)
	}
}
