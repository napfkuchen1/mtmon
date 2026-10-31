// SPDX-License-Identifier: AGPL-3.0-only

package advice

import "fmt"

// DiskFreeWarn is the free fraction of mtmon's data file system below which a warning is raised.
const DiskFreeWarn = 0.15

// DiskDaysWarn: warn when the forecast says the disk is full within this many days.
const DiskDaysWarn = 14

func gib(b int64) string { return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30)) }

// ruleMtmonDisk: mtmon's own database fills the disk it lives on (SQLite then fails writes, monitoring stops).
func ruleMtmonDisk(s Snapshot) ([]Suggestion, string) {
	h := s.SelfHealth()
	if !h.Known {
		return nil, "free space of the data directory is unknown on this system"
	}
	frac := float64(h.FreeBytes) / float64(h.TotalBytes)
	var daysLeft float64 = -1
	if h.GrowthPerDay > 0 {
		daysLeft = float64(h.FreeBytes) / float64(h.GrowthPerDay)
	}
	low := frac < DiskFreeWarn
	soon := daysLeft >= 0 && daysLeft < DiskDaysWarn
	if !low && !soon {
		return nil, ""
	}
	ev := []string{fmt.Sprintf("Free space where mtmon stores its data: %s of %s (%.0f %%)", gib(h.FreeBytes), gib(h.TotalBytes), frac*100),
		fmt.Sprintf("Database size: %s", gib(h.DBBytes))}
	if h.GrowthPerDay > 0 {
		ev = append(ev, fmt.Sprintf("Growth: about %s per day – the disk would be full in about %.0f days", gib(h.GrowthPerDay), daysLeft))
	}
	title := "mtmon's disk is filling up"
	if frac < 0.05 {
		title = "mtmon's disk is almost full"
	}
	return []Suggestion{{
		Subject: "disk", Severity: SevWarning, Category: CatMonitoring, Link: "settings", Confidence: "high",
		Title:    title,
		Why:      "When the disk is full, SQLite cannot write any more: flows, alerts and history are lost and monitoring effectively stops.",
		Evidence: ev,
		Steps: []string{
			fmt.Sprintf("Lower raw_retention_days (now %d) in /etc/mtmon/config.json and restart mtmon; the raw flows are the largest part of the database.", h.RawRetentionDays),
			fmt.Sprintf("Or lower retention_days, the history of the rollups (now %d).", h.RetentionDays),
			"Or give the container more disk (Proxmox: resize the root disk, then restart the container).",
		},
		Limits: "The forecast is a straight line over the last days; it is only shown once mtmon has watched the database for 6 hours.",
	}}, ""
}
