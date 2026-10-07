package enrich

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestFetchOUIAtomicAndSizeCheck(t *testing.T) {
	big := "Registry,Assignment,Organization Name,Organization Address\n" + strings.Repeat("MA-L,001122,Example Corp,Somewhere\n", 20000)
	small := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if small {
			w.Write([]byte("tiny"))
			return
		}
		w.Write([]byte(big))
	}))
	defer srv.Close()
	old := ouiURL
	ouiURL = srv.URL
	defer func() { ouiURL = old }()
	dir := t.TempDir()
	p, err := Fetch(context.Background(), dir, DataOUI)
	if err != nil || !strings.HasSuffix(p, "oui.csv") {
		t.Fatalf("fetch: %q %v", p, err)
	}
	e := New("", "", "", false)
	if err := e.LoadOUI(p); err != nil || e.Vendor("00:11:22:AA:BB:CC") != "Example Corp" {
		t.Fatalf("OUI not usable after fetch: %v", err)
	}
	// a broken download must keep the previous file
	small = true
	if _, err := Fetch(context.Background(), dir, DataOUI); err == nil {
		t.Fatal("tiny download must be refused")
	}
	if b, _ := os.ReadFile(p); len(b) != len(big) {
		t.Fatal("previous file was replaced by a bad download")
	}
	if _, err := Fetch(context.Background(), dir, "nope"); err == nil {
		t.Fatal("unknown dataset")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("temp files left behind: %v", ents)
	}
}

func TestSetReverseDNSAndReloadErrors(t *testing.T) {
	e := New("", "", "", false)
	e.SetReverseDNS(true)
	if !e.Status()["reverse_dns"] {
		t.Fatal("toggle")
	}
	if err := e.ReloadGeo(t.TempDir()+"/missing.mmdb", ""); err == nil {
		t.Fatal("missing file must be an error and keep the old state")
	}
}
