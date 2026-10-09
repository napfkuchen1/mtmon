package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestResetEndpoint(t *testing.T) {
	ts, s := newTestServer(t)
	s.St.DB.Exec(`INSERT INTO alerts(ts,kind,subject,severity,msg) VALUES(1,'k','s','warn','m')`)
	s.St.DB.Exec(`INSERT INTO devices(name,addr) VALUES('r1','10.0.0.1')`)
	_, c := login(t, ts, "test-password-1")
	post := func(body, origin string) int {
		req, _ := http.NewRequest("POST", ts.URL+"/api/reset", strings.NewReader(body))
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return r.StatusCode
	}
	count := func(tb string) (n int) {
		s.St.DB.QueryRow(`SELECT count(*) FROM ` + tb).Scan(&n)
		return
	}
	if r, _ := http.Post(ts.URL+"/api/reset", "application/json", strings.NewReader(`{"mode":"data","confirm":"RESET"}`)); r.StatusCode != 401 {
		t.Errorf("no session: %d", r.StatusCode)
	}
	if code := post(`{"mode":"data","confirm":"RESET"}`, "https://evil.example"); code != 403 {
		t.Errorf("cross-origin: %d", code)
	}
	for _, b := range []string{`{"mode":"data"}`, `{"mode":"data","confirm":"reset"}`, `{"mode":"nope","confirm":"RESET"}`} {
		if code := post(b, ""); code != 400 {
			t.Errorf("%s: %d, want 400", b, code)
		}
	}
	if count("alerts") != 1 {
		t.Fatal("rejected requests must not delete anything")
	}
	if code := post(`{"mode":"data","confirm":"RESET"}`, ""); code != 200 || count("alerts") != 0 || count("devices") != 1 {
		t.Errorf("data reset: code %d alerts %d devices %d", code, count("alerts"), count("devices"))
	}
	if code := post(`{"mode":"full","confirm":"RESET"}`, ""); code != 200 || count("devices") != 0 {
		t.Errorf("full reset: code %d devices %d", code, count("devices"))
	}
}
