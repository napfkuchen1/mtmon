package alert

import (
	"testing"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestConfigChangedAfterOwnChangeIsInfo(t *testing.T) {
	e, got := newEngine(t)
	e.ConfigChanged("r1", 3)
	if len(*got) != 1 || (*got)[0].Severity != "warning" {
		t.Fatalf("foreign change must warn: %+v", *got)
	}
	if err := e.St.AddManifest("r2", store.ManifestEntry{Seq: 1, Kind: "create", Descr: "x"}); err != nil {
		t.Fatal(err)
	}
	e.ConfigChanged("r2", 3)
	a := (*got)[len(*got)-1]
	if a.Subject != "r2" || a.Severity != "info" {
		t.Fatalf("own change must be info: %+v", a)
	}
}
