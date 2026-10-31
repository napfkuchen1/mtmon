// SPDX-License-Identifier: AGPL-3.0-only

package advice

import (
	"fmt"
	"time"
)

// BackupMaxDays: a device whose newest backup or export file is older than this gets a suggestion.
const BackupMaxDays = 30

// ruleBackupAge: a router without a recent backup is the most expensive failure in a small network. Only the files
// on the device are visible (read-only); a copy sent elsewhere (cloud backup, a script with fetch/e-mail) is not.
func ruleBackupAge(s Snapshot) ([]Suggestion, string) {
	var out []Suggestion
	checked := 0
	for _, d := range s.Devices() {
		if d.Caps == nil || !d.Caps.FilesReadable {
			continue
		}
		checked++
		newest := d.Caps.NewestBackup
		if d.Caps.NewestExport > newest {
			newest = d.Caps.NewestExport
		}
		var ev []string
		switch {
		case newest == 0:
			ev = []string{fmt.Sprintf("No *.backup or *.rsc file on %s", d.Name)}
		case s.Now().Sub(time.Unix(newest, 0)) > BackupMaxDays*24*time.Hour:
			age := int(s.Now().Sub(time.Unix(newest, 0)).Hours() / 24)
			ev = []string{fmt.Sprintf("Newest backup or export file on %s is %d days old (%s)", d.Name, age, time.Unix(newest, 0).Format("2006-01-02"))}
		default:
			continue
		}
		ev = append(ev, "Source: "+d.Caps.ReadNote())
		out = append(out, Suggestion{
			Subject: d.Name, Severity: SevTip, Category: CatHygiene, Link: "device/" + d.Name, Confidence: "medium",
			Title:    fmt.Sprintf("%s has no recent backup", d.Name),
			Why:      "When a router dies or a change goes wrong, the backup decides whether it is back in minutes or after hours of rebuilding the configuration. A backup file on the router itself is lost with the router: copy it away as well.",
			Evidence: ev,
			Steps:    []string{"Create a backup and a text export now (commands below).", "Download both files (WinBox → Files, or SFTP) and keep them off the router. A scheduler script can repeat this monthly."},
			Commands: "/system backup save name=" + d.Name + "\n/export file=" + d.Name,
			Limits:   "mtmon only sees the files on the router (read-only). A backup that is sent elsewhere right away (cloud backup, a script with fetch or e-mail) is not visible, so this can be a false alarm.",
		})
	}
	if checked == 0 {
		return nil, "the routers did not let mtmon list their files (or no capabilities are stored yet)"
	}
	return out, ""
}
