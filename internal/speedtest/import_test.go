// SPDX-License-Identifier: AGPL-3.0-only

package speedtest

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

const mySpeedCSV = `id,ping,jitter,download,upload,time,type,created,error
1,12.5,1.1,487.3,96.2,14,ookla,2025-03-01T10:00:00.000Z,
2,0,0,0,0,0,ookla,2025-03-01T11:00:00.000Z,Could not reach the server
3,11,2,90,30,10,librespeed,2025-03-01 12:00:00,
4,11,2,90,30,10,ookla,not-a-date,
`

func TestParseMySpeedCSV(t *testing.T) {
	rows, skipped, err := ParseMySpeed([]byte(mySpeedCSV))
	if err != nil || len(rows) != 3 || skipped != 1 {
		t.Fatalf("%v %d %+v", err, skipped, rows)
	}
	if r := rows[0]; r.DownBps != 487.3e6 || r.UpBps != 96.2e6 || r.PingMS != 12.5 || r.Provider != "ookla" ||
		r.TS != time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC).Unix() {
		t.Errorf("first: %+v", r)
	}
	if r := rows[1]; r.Err == "" || r.DownBps != 0 {
		t.Errorf("an error row is a failed test, not 0 Mbit/s: %+v", r)
	}
	if rows[2].Provider != "librespeed" {
		t.Errorf("provider: %+v", rows[2])
	}
}

func TestParseMySpeedJSONForms(t *testing.T) {
	arr := `[{"id":1,"ping":10,"download":100.5,"upload":20,"type":"ookla","created":"2025-03-01T10:00:00Z","error":null}]`
	wrapped := `{"speedtests":` + arr + `}`
	for name, in := range map[string]string{"array": arr, "wrapper": wrapped} {
		rows, _, err := ParseMySpeed([]byte(in))
		if err != nil || len(rows) != 1 || rows[0].DownBps != 100.5e6 || rows[0].Err != "" {
			t.Errorf("%s: %v %+v", name, err, rows)
		}
	}
	ms := `[{"download":50,"created":1740823200000}]` // unix milliseconds
	if rows, _, err := ParseMySpeed([]byte(ms)); err != nil || len(rows) != 1 || rows[0].TS != 1740823200 || rows[0].Provider != "myspeed" {
		t.Errorf("ms: %v %+v", err, rows)
	}
	for _, bad := range []string{"", "   ", "{}", "[1,2", "not,a\nspeedtest file"} {
		if rows, _, err := ParseMySpeed([]byte(bad)); err == nil && len(rows) > 0 {
			t.Errorf("%q must not import: %+v", bad, rows)
		}
	}
}

func TestImportTwiceAddsNothing(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	defer st.Close()
	rows, _, _ := ParseMySpeed([]byte(mySpeedCSV))
	if a, d, err := st.ImportSpeedtests(rows); err != nil || a != 3 || d != 0 {
		t.Fatalf("first: %d %d %v", a, d, err)
	}
	if a, d, err := st.ImportSpeedtests(rows); err != nil || a != 0 || d != 3 {
		t.Fatalf("second: %d %d %v", a, d, err)
	}
	// imported history shows up but does not count against the data budget of mtmon's own tests
	if n := st.SpeedtestBytesSince(0); n != 0 {
		t.Errorf("imports use no budget: %d", n)
	}
}

func TestOoklaParseAndMissingCLI(t *testing.T) {
	out := []byte(`{"ping":{"latency":9.5,"jitter":1.2},"download":{"bandwidth":62500000,"bytes":300000000},"upload":{"bandwidth":12500000,"bytes":60000000},"server":{"host":"speedtest.example.net"}}`)
	res := parseOokla(out, Result{Server: "ookla"})
	if res.Err != "" || res.DownBps != 500e6 || res.UpBps != 100e6 || res.PingMS != 9.5 || res.Server != "speedtest.example.net" || res.Bytes != 360000000 {
		t.Errorf("%+v", res)
	}
	if r := parseOokla([]byte(`{"error":"Configuration - Couldn't resolve host"}`), Result{}); r.Err == "" {
		t.Error("CLI error must be an error")
	}
	if r := parseOokla([]byte(`nope`), Result{}); r.Err == "" {
		t.Error("garbage must be an error")
	}
	// no CLI installed: a clear failure, nothing is downloaded or run
	t.Setenv("PATH", t.TempDir())
	if r := (Ookla{DataDir: t.TempDir()}).Run(context.Background(), Opts{MaxDur: time.Second}); r.Err == "" || r.DownBps != 0 {
		t.Errorf("missing CLI: %+v", r)
	}
	var gotArgs []string
	fake := Ookla{ServerID: 4711, RunCmd: func(_ context.Context, _ string, args []string) ([]byte, error) { gotArgs = args; return out, nil }}
	if r := fake.Run(context.Background(), Opts{MaxDur: time.Second}); r.Err != "" || r.DownBps != 500e6 {
		t.Errorf("run: %+v", r)
	}
	if len(gotArgs) != 4 || gotArgs[3] != "--server-id=4711" {
		t.Errorf("args are fixed, only the numeric id varies: %v", gotArgs)
	}
	if r := (Ookla{RunCmd: func(context.Context, string, []string) ([]byte, error) { return nil, errors.New("boom") }}).Run(context.Background(), Opts{MaxDur: time.Second}); r.Err == "" {
		t.Error("exec failure must be an error")
	}
}

func TestOoklaNeedsAcceptedLicence(t *testing.T) {
	s := DefaultSettings()
	s.Providers = []string{"cloudflare", "ookla"}
	if _, err := s.Validate(); err == nil {
		t.Fatal("Ookla without accepting the licence must be refused")
	}
	s.OoklaOK = true
	if _, err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTrackerMatchesRecentPairsOnly(t *testing.T) {
	var tr Tracker
	l, _ := net.ResolveTCPAddr("tcp", "192.168.1.5:50000")
	r, _ := net.ResolveTCPAddr("tcp", "198.51.100.7:443")
	tr.Note(l, r)
	if !tr.Match("192.168.1.5", "198.51.100.7") {
		t.Error("just noted")
	}
	if tr.Match("192.168.1.6", "198.51.100.7") || tr.Match("192.168.1.5", "198.51.100.8") {
		t.Error("only the exact pair")
	}
	tr.mu.Lock()
	tr.m[[2]string{"192.168.1.5", "198.51.100.7"}] = time.Now().Add(-tail - time.Second)
	tr.mu.Unlock()
	if tr.Match("192.168.1.5", "198.51.100.7") {
		t.Error("expired")
	}
}
