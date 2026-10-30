package alert

import (
	"strings"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestConfigChangedAfterOwnChangeIsInfo(t *testing.T) {
	e, got := newEngine(t)
	e.ConfigChanged("r1", 3, []string{"admin", "bob"})
	if len(*got) != 1 || (*got)[0].Severity != "warning" {
		t.Fatalf("foreign change must warn: %+v", *got)
	}
	if m := (*got)[0].Msg; !strings.Contains(m, "changed by: admin; bob") {
		t.Fatalf("author missing: %s", m)
	}
	if err := e.St.AddManifest("r2", store.ManifestEntry{Seq: 1, Kind: "create", Descr: "x"}); err != nil {
		t.Fatal(err)
	}
	e.ConfigChanged("r2", 3, nil)
	a := (*got)[len(*got)-1]
	if a.Subject != "r2" || a.Severity != "info" {
		t.Fatalf("own change must be info: %+v", a)
	}
}
