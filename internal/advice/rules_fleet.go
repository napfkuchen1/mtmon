package advice

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// CertWarnDays: warn when a service certificate expires within this many days.
const CertWarnDays = 30

// ruleRouterOSUpdate groups the devices whose router-side update check found a newer version in their channel.
// RouterOS only fills this after "check for updates" ran on the router (manually or via a scheduler), so a device
// that never checked says nothing.
func ruleRouterOSUpdate(s Snapshot) ([]Suggestion, string) {
	var lines, names []string
	checked := 0
	for _, d := range s.Devices() {
		if d.Caps == nil || d.Caps.UpdateLatest == "" {
			continue
		}
		checked++
		have, ok1 := parseVer(d.Caps.UpdateInstalled)
		if !ok1 {
			have, ok1 = parseVer(d.Version)
		}
		latest, ok2 := parseVer(d.Caps.UpdateLatest)
		if !ok1 || !ok2 || !have.less(latest) {
			continue
		}
		names = append(names, d.Name)
		ch := d.Caps.UpdateChannel
		if ch != "" {
			ch = " (channel " + ch + ")"
		}
		lines = append(lines, fmt.Sprintf("%s: %s installed, %s available%s", d.Name, have.short(), latest.short(), ch))
	}
	if checked == 0 {
		return nil, "no router has run an update check yet (/system package update check-for-updates)"
	}
	if len(lines) == 0 {
		return nil, ""
	}
	sort.Strings(lines)
	title := fmt.Sprintf("RouterOS update available on %s", names[0])
	if len(names) > 1 {
		title = fmt.Sprintf("RouterOS update available on %d devices", len(names))
	}
	return []Suggestion{{
		Subject: "update", Severity: SevTip, Category: CatHygiene, Link: "devices", Confidence: "medium", Title: title,
		Why:      "Updates carry security and bug fixes. Check the release notes, take a backup first, and update access points and the CAPsMAN router in one maintenance window so their versions stay equal.",
		Evidence: lines,
		Steps:    []string{"Read the release notes of the target version on mikrotik.com.", "Make a backup (see the backup suggestion), then update during a quiet hour. After the update, upgrade the RouterBOARD firmware if offered."},
		Commands: "/system package update check-for-updates\n/system package update install",
		Limits:   "Based on the router's own last update check; mtmon never reaches the internet for this. A router that has not checked recently may know of an older latest version.",
	}}, ""
}

// ruleCapsmanVersion: a CAPsMAN controller and its CAPs on different RouterOS versions break setups with
// require-same-version and cause provisioning trouble.
func ruleCapsmanVersion(s Snapshot) ([]Suggestion, string) {
	var ctrl []Device
	var caps []Device
	for _, d := range s.Devices() {
		if d.Caps == nil {
			continue
		}
		if d.Caps.CAPsMAN {
			ctrl = append(ctrl, d)
		}
		if d.Caps.CAP {
			caps = append(caps, d)
		}
	}
	if len(ctrl) == 0 || len(caps) == 0 {
		return nil, "no CAPsMAN controller with CAPs among the devices"
	}
	var out []Suggestion
	for _, c := range ctrl {
		cv, ok := parseVer(c.Version)
		if !ok {
			continue
		}
		var diff []string
		majorMinor := false
		for _, p := range caps {
			pv, ok := parseVer(p.Version)
			if !ok || pv.short() == cv.short() {
				continue
			}
			diff = append(diff, fmt.Sprintf("%s runs %s", p.Name, pv.short()))
			majorMinor = majorMinor || pv.n[0] != cv.n[0] || pv.n[1] != cv.n[1]
		}
		if len(diff) == 0 {
			continue
		}
		sev := SevInfo
		if majorMinor {
			sev = SevWarning
		}
		out = append(out, Suggestion{
			Subject: c.Name, Severity: sev, Category: CatHygiene, Link: "devices", Confidence: "high",
			Title:    fmt.Sprintf("The CAPsMAN controller %s and its access points run different RouterOS versions", c.Name),
			Why:      "A CAPsMAN controller and its CAPs should run the same RouterOS version: with require-same-version the CAPs refuse to join, and mismatches cause provisioning and roaming problems that are hard to trace.",
			Evidence: append([]string{fmt.Sprintf("%s (controller) runs %s", c.Name, cv.short())}, diff...),
			Steps:    []string{"Update the controller and all CAPs to the same version in one maintenance window (the controller first, then the CAPs)."},
			Limits:   "Based on the versions the devices report; a patch-level difference is rated lower than a different minor version.",
		})
	}
	return out, ""
}

// ruleCertExpiry: a certificate used by www-ssl / api-ssl that runs out stops the REST/API connection of clients
// that check the certificate (browsers, scripts) and, with TLS verification, mtmon itself.
func ruleCertExpiry(s Snapshot) ([]Suggestion, string) {
	var out []Suggestion
	seen := false
	for _, d := range s.Devices() {
		if d.Caps == nil || len(d.Caps.Certs) == 0 {
			continue
		}
		for _, c := range d.Caps.Certs {
			if c.Expires == 0 {
				continue
			}
			seen = true
			left := time.Unix(c.Expires, 0).Sub(s.Now())
			if left > CertWarnDays*24*time.Hour {
				continue
			}
			var ev, title string
			if left < 0 {
				title = fmt.Sprintf("The %s certificate of %s has expired", c.Service, d.Name)
				ev = fmt.Sprintf("Certificate %s expired on %s", c.Name, time.Unix(c.Expires, 0).Format("2006-01-02"))
			} else {
				title = fmt.Sprintf("The %s certificate of %s expires in %d days", c.Service, d.Name, int(left.Hours()/24))
				ev = fmt.Sprintf("Certificate %s is valid until %s", c.Name, time.Unix(c.Expires, 0).Format("2006-01-02"))
			}
			out = append(out, Suggestion{
				Subject: d.Name + "/" + c.Service, Severity: SevWarning, Category: CatMonitoring, Link: "device/" + d.Name, Confidence: "high",
				Title:    title,
				Why:      "When the certificate of the REST/API service runs out, HTTPS clients refuse the connection: monitoring over REST can stop silently the day it expires.",
				Evidence: []string{ev, "Source: " + d.Caps.ReadNote()},
				Steps:    []string{"Create a new certificate and assign it to the service (commands below), then update the pinned fingerprint of the device in mtmon if it changed."},
				Commands: strings.Join([]string{"/certificate print where name=" + c.Name, "# create and sign a new one (adjust the name and address), then:", "/ip service set " + c.Service + " certificate=<new certificate>"}, "\n"),
			})
		}
	}
	if !seen {
		return nil, "no service certificate with a known expiry date"
	}
	return out, ""
}

// ruleCleartextServices: baseline check – Telnet and FTP send the login in clear text and are not needed when
// SSH / SFTP / Winbox are available.
func ruleCleartextServices(s Snapshot) ([]Suggestion, string) {
	var out []Suggestion
	seen := false
	for _, d := range s.Devices() {
		if d.Caps == nil || d.Caps.ProbedAt == 0 {
			continue
		}
		seen = true
		if len(d.Caps.Cleartext) == 0 {
			continue
		}
		names := strings.Join(d.Caps.Cleartext, ", ")
		out = append(out, Suggestion{
			Subject: d.Name, Severity: SevWarning, Category: CatSecurity, Link: "device/" + d.Name, Confidence: "high",
			Title:    fmt.Sprintf("%s still runs %s", d.Name, names),
			Why:      "Telnet and FTP send user name and password unencrypted. Anyone who can read the traffic in between gets the login of the router. SSH and Winbox do the same job encrypted.",
			Evidence: []string{"Enabled in /ip service: " + names, "Source: " + d.Caps.ReadNote()},
			Steps:    []string{"Check that you do not use the service (a backup script may use FTP), then disable it."},
			Commands: "/ip service disable [find name=telnet]\n/ip service disable [find name=ftp]",
			Limits:   "Based on the capabilities stored at the last probe of the device.",
		})
	}
	if !seen {
		return nil, "no stored router capabilities"
	}
	return out, ""
}
