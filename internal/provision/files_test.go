package provision

import (
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/poller"
)

func TestParseFiles(t *testing.T) {
	f := parseFiles([]poller.Row{
		{"name": "gw-2026-08.backup", "creation-time": "2026-08-01 10:00:00", "size": "180000"},
		{"name": "gw-2026-09.backup", "creation-time": "sep/05/2026 03:00:00", "size": "190000"}, // older firmware format
		{"name": "gw.rsc", "creation-time": "2026-10-02 12:30:00", "size": "21000"},
		{"name": "flash", "type": "disk"},
		{"name": "pub/readme.txt", "creation-time": "2026-10-20 00:00:00"},
	})
	if !f.Readable || f.NewestBackup != time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC).Unix() || f.BackupSize != 190000 ||
		f.NewestExport != time.Date(2026, 10, 2, 12, 30, 0, 0, time.UTC).Unix() || f.ExportSize != 21000 {
		t.Errorf("%+v", f)
	}
	// RouterOS 7.24 lists the time as last-modified
	g := parseFiles([]poller.Row{
		{"name": "gw.backup", "last-modified": "2026-10-24 15:47:11", "size": "200000"},
		{"name": "gw.rsc", "weird-time": "2026-10-24 15:48:00", "size": "9000"},
	})
	if g.NewestBackup != time.Date(2026, 10, 24, 15, 47, 11, 0, time.UTC).Unix() || g.NewestExport == 0 {
		t.Errorf("last-modified: %+v", g)
	}
	if g := parseFiles(nil); !g.Readable || g.NewestBackup != 0 {
		t.Errorf("empty listing: %+v", g)
	}
}
