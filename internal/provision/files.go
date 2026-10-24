package provision

import (
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/poller"
)

// Files is what the read-only user sees in /file: the newest binary backup and the newest text export (.rsc).
// Readable is false when the router does not let the monitoring user list files (nothing is claimed then).
type Files struct {
	Readable     bool  `json:"readable"`
	NewestBackup int64 `json:"newest_backup,omitempty"` // unix time of the newest *.backup, 0 = none
	BackupSize   int64 `json:"backup_size,omitempty"`
	NewestExport int64 `json:"newest_export,omitempty"` // unix time of the newest *.rsc, 0 = none
	ExportSize   int64 `json:"export_size,omitempty"`
}

var fileTimeLayouts = []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "Jan/02/2006 15:04:05", "2006-01-02 15:04"}

// parseFileTime reads RouterOS creation-time values ("2026-10-01 12:00:00", or "oct/01/2026 12:00:00" on older
// builds). The router's time zone is unknown; UTC is used, which is plenty for an age counted in days.
func parseFileTime(s string) int64 {
	s = strings.TrimSpace(s)
	if len(s) > 3 && s[3] == '/' { // month names are lower case on RouterOS
		s = strings.ToUpper(s[:1]) + s[1:]
	}
	for _, l := range fileTimeLayouts {
		if t, err := time.ParseInLocation(l, s, time.UTC); err == nil {
			return t.Unix()
		}
	}
	return 0
}

// parseFiles summarises the /file listing.
func parseFiles(rows []poller.Row) Files {
	f := Files{Readable: true}
	for _, r := range rows {
		name := strings.ToLower(r["name"])
		ts := parseFileTime(r["creation-time"])
		if ts == 0 {
			continue
		}
		switch {
		case strings.HasSuffix(name, ".backup") && ts > f.NewestBackup:
			f.NewestBackup, f.BackupSize = ts, r.Int("size")
		case strings.HasSuffix(name, ".rsc") && ts > f.NewestExport:
			f.NewestExport, f.ExportSize = ts, r.Int("size")
		}
	}
	return f
}
