// SPDX-License-Identifier: AGPL-3.0-only

package alert

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// A client that roams to a weak access point often cannot answer the DHCP server for a while. The router then logs
// "offering lease … without success", but the fault is the Wi-Fi link, not the DHCP server. mtmon remembers the last
// Wi-Fi connect of every MAC and folds DHCP failures that follow it closely into one Wi-Fi note per client.
const (
	roamDHCPWindow = 10  // seconds between the connect and the first failed offer for it to count as "after the roam"
	roamDHCPGap    = 120 // seconds without a failed offer that end such an episode
	roamDHCPMin    = 3   // failed offers before the note is raised (a single retry is normal)
)

var (
	reWiFiConn = regexp.MustCompile(`(?i)([0-9a-f]{2}(?::[0-9a-f]{2}){5})\S*\s+(?:\S+\s+)*?connected(?:.*?signal strength:?\s*(-?\d+))?`)
	reMAC      = regexp.MustCompile(`(?i)[0-9a-f]{2}(?::[0-9a-f]{2}){5}`)
)

type roamConn struct {
	ts     int64
	ap     string
	signal string
}

type roamDHCP struct {
	roamConn
	last   int64
	n      int
	raised bool
}

// noteWiFiConnect remembers a client that just connected to an access point (also the second half of a roam).
func (e *Engine) noteWiFiConnect(ts int64, ap, msg string) {
	m := reWiFiConn.FindStringSubmatch(msg)
	if m == nil || strings.Contains(strings.ToLower(msg), "disconnected") {
		return
	}
	mac := strings.ToUpper(m[1])
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.roams == nil {
		e.roams = map[string]roamConn{}
		e.roamDHCP = map[string]*roamDHCP{}
	}
	if len(e.roams) > 2000 {
		for k, r := range e.roams {
			if ts-r.ts > roamDHCPGap {
				delete(e.roams, k)
				delete(e.roamDHCP, k)
			}
		}
	}
	e.roams[mac] = roamConn{ts: ts, ap: ap, signal: m[2]}
	delete(e.roamDHCP, mac)
}

// dhcpAfterRoam reports whether a failed DHCP offer belongs to a client that has just connected to an access point.
// Such offers are not counted as DHCP trouble; after roamDHCPMin of them one Wi-Fi note is raised for the client.
func (e *Engine) dhcpAfterRoam(ts int64, msg string) bool {
	mac := strings.ToUpper(reMAC.FindString(msg))
	if mac == "" {
		return false
	}
	e.mu.Lock()
	ep := e.roamDHCP[mac]
	if ep != nil && ts-ep.last > roamDHCPGap {
		delete(e.roamDHCP, mac)
		ep = nil
	}
	if ep == nil {
		r, ok := e.roams[mac]
		if !ok || ts < r.ts || ts-r.ts > roamDHCPWindow {
			e.mu.Unlock()
			return false
		}
		ep = &roamDHCP{roamConn: r}
		if e.roamDHCP == nil {
			e.roamDHCP = map[string]*roamDHCP{}
		}
		e.roamDHCP[mac] = ep
	}
	ep.n++
	ep.last = ts
	raise := ep.n >= roamDHCPMin && !ep.raised
	if raise {
		ep.raised = true
	}
	snap := *ep
	e.mu.Unlock()
	if raise && e.foldExtra("wifi_disconnects", "/"+mac, time.Unix(ts, 0), snap.n) {
		return true // the client has an open disconnect story: the failed offers are part of it
	}
	if raise {
		text := fmt.Sprintf("%s roamed to %s and got no DHCP address for %d s (%d offers without success)", mac, snap.ap, snap.last-snap.ts, snap.n)
		text += " – the Wi-Fi link after the roam is the problem, not the DHCP server"
		if snap.signal != "" {
			text += fmt.Sprintf(" – signal on the new AP: %s dBm", snap.signal)
		}
		e.Raise("wifi_roam_dhcp", mac, "info", text, 30*time.Minute)
	}
	return true
}
