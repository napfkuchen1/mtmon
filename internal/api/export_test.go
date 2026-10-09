package api

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestXLSXWorkbook(t *testing.T) {
	tb := exportTable{File: "x", Sheet: "Top: hosts/1", Head: []string{"name", "size", "when", "n", "ok"},
		Rows: [][]any{{"a <b> & \x01c", Bytes(1536), time.Date(2026, 10, 9, 12, 30, 0, 0, time.Local), 7, true}}}
	b, err := tb.xlsx()
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		d, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = string(d)
	}
	for _, n := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/styles.xml", "xl/worksheets/sheet1.xml"} {
		if got[n] == "" {
			t.Errorf("missing part %s", n)
		}
	}
	sh := got["xl/worksheets/sheet1.xml"]
	for _, want := range []string{`a &lt;b&gt; &amp; c`, `<v>1536</v>`, `<v>7</v>`, `>yes<`, `A1:E2`} {
		if !strings.Contains(sh, want) {
			t.Errorf("sheet lacks %q:\n%s", want, sh)
		}
	}
	if !strings.Contains(got["xl/workbook.xml"], `name="Top- hosts-1"`) {
		t.Errorf("sheet name not sanitised: %s", got["xl/workbook.xml"])
	}
	if colName(0) != "A" || colName(25) != "Z" || colName(26) != "AA" {
		t.Error("colName wrong")
	}
}

func TestAlertBulkAndExport(t *testing.T) {
	ts, s := newTestServer(t)
	_, c := login(t, ts, "test-password-1")
	for i := 0; i < 3; i++ {
		s.St.AddAlert(store.Alert{TS: time.Now().Unix(), Kind: "device_down", Subject: "x", Severity: "warning", Msg: "m"})
	}
	post := func(p string) int {
		req, _ := http.NewRequest("POST", ts.URL+p, nil)
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return r.StatusCode
	}
	if post("/api/alerts/ack-all") != 200 || s.St.OpenAlerts() != 0 {
		t.Fatalf("ack-all failed, open=%d", s.St.OpenAlerts())
	}
	s.St.AddAlert(store.Alert{TS: time.Now().Unix(), Kind: "device_down", Subject: "y", Severity: "warning", Msg: "new"})
	if post("/api/alerts/clear") != 200 {
		t.Fatal("clear failed")
	}
	if l, _ := s.St.Alerts(10); len(l) != 1 || l[0].Acked {
		t.Fatalf("clear must keep open alerts, got %+v", l)
	}
	r, _ := c.Get(ts.URL + "/api/export/alerts?format=xlsx")
	body, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 || !strings.Contains(r.Header.Get("Content-Type"), "spreadsheetml") || !bytes.HasPrefix(body, []byte("PK")) {
		t.Fatalf("xlsx export: %d %s", r.StatusCode, r.Header.Get("Content-Type"))
	}
	r, _ = c.Get(ts.URL + "/api/export/alerts")
	body, _ = io.ReadAll(r.Body)
	if !strings.HasPrefix(string(body), "time,severity,type") || !strings.Contains(string(body), "new") {
		t.Fatalf("csv export: %s", body)
	}
	if post("/api/alerts/clear?all=1") != 200 {
		t.Fatal("clear all failed")
	}
	if l, _ := s.St.Alerts(10); len(l) != 0 {
		t.Fatalf("clear all left %d", len(l))
	}
}
