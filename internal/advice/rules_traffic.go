package advice

import (
	"fmt"
	"sort"
)

const (
	DominantShare  = 0.60
	DominantMin    = 1_000_000_000 // total internet bytes before shares mean anything
	UploadFactor   = 5
	UploadMin      = 1_000_000_000
	ChattyMinFlows = 20000
	ChattyFactor   = 8
)

func ruleDominant(s Snapshot) ([]Suggestion, string) {
	tr := s.Traffic24h()
	if len(tr) == 0 {
		return nil, "no flow data in the last 24 h"
	}
	var total int64
	active := 0
	var top ClientTraffic
	for _, t := range tr {
		v := t.Up + t.Down
		if v > 0 {
			active++
			total += v
		}
		if v > top.Up+top.Down {
			top = t
		}
	}
	if active < 3 || total < DominantMin {
		return nil, ""
	}
	share := float64(top.Up+top.Down) / float64(total)
	if share <= DominantShare {
		return nil, ""
	}
	idx := clientIndex(s)
	name := nameOf(idx, top.MAC)
	ev := []string{fmt.Sprintf("%s caused %s%% of the internet traffic in the last 24 h (%s of %s)", name, pct(share*100), humanBytes(top.Up+top.Down), humanBytes(total))}
	if svc := s.TopService(top.MAC); svc != "" {
		ev = append(ev, fmt.Sprintf("Mostly %s", svc))
	}
	return []Suggestion{{
		Subject: top.MAC, Severity: SevInfo, Category: CatHygiene, Link: "client/" + top.MAC, Confidence: "high",
		Title:    fmt.Sprintf("%s uses most of your internet traffic", name),
		Why:      "One device taking the larger part of the line is often harmless (a backup, a large download, a stream), but it can also be a device that misbehaves. It is worth knowing, because it slows down everyone else when the line is busy.",
		Evidence: ev,
		Steps: []string{
			"Open the client page to see where the traffic goes and when it started.",
			"If it is a regular job, schedule it for the night; if it is a surprise, find out which program is responsible.",
			"A queue or bandwidth limit on the router can protect the rest of the network.",
		},
	}}, ""
}

func ruleUploadHeavy(s Snapshot) ([]Suggestion, string) {
	tr := s.Traffic24h()
	if len(tr) == 0 {
		return nil, "no flow data in the last 24 h"
	}
	idx := clientIndex(s)
	var out []Suggestion
	for _, t := range tr {
		if t.Up < UploadMin || float64(t.Up) < UploadFactor*float64(max(t.Down, 1)) {
			continue
		}
		name := nameOf(idx, t.MAC)
		ev := []string{fmt.Sprintf("%s sent %s and received %s in the last 24 h (%.0f times more upload than download)", name, humanBytes(t.Up), humanBytes(t.Down), float64(t.Up)/float64(max(t.Down, 1)))}
		if svc := s.TopService(t.MAC); svc != "" {
			ev = append(ev, fmt.Sprintf("Mostly %s", svc))
		}
		out = append(out, Suggestion{
			Subject: t.MAC, Severity: SevTip, Category: CatHygiene, Link: "client/" + t.MAC, Confidence: "medium",
			Title:    fmt.Sprintf("%s uploads much more than it downloads", name),
			Why:      "Most devices download far more than they upload. A device that mainly sends data is usually a camera, a backup or sync job, or a server. If you do not expect that, it can also mean a program that leaks or shares data.",
			Evidence: ev,
			Steps: []string{
				"Open the client page and check the destinations: a known cloud or backup service is fine.",
				"If the destinations are unknown, disconnect the device and check it for unwanted software.",
			},
			Limits: "Cameras, NAS backups and photo sync legitimately upload a lot.",
		})
	}
	return out, ""
}

func ruleChatty(s Snapshot) ([]Suggestion, string) {
	tr := s.Traffic24h()
	if len(tr) == 0 {
		return nil, "no flow data in the last 24 h"
	}
	var fl []int64
	for _, t := range tr {
		if t.Fl > 0 {
			fl = append(fl, t.Fl)
		}
	}
	if len(fl) < 5 {
		return nil, ""
	}
	sort.Slice(fl, func(i, j int) bool { return fl[i] < fl[j] })
	med := fl[len(fl)/2]
	idx := clientIndex(s)
	var out []Suggestion
	for _, t := range tr {
		if t.Fl < ChattyMinFlows || t.Fl < ChattyFactor*max(med, 1) {
			continue
		}
		name := nameOf(idx, t.MAC)
		out = append(out, Suggestion{
			Subject: t.MAC, Severity: SevInfo, Category: CatHygiene, Link: "client/" + t.MAC, Confidence: "medium",
			Title: fmt.Sprintf("%s opens an unusually high number of connections", name),
			Why:   "A device with many times more connections than the others can be a torrent or sync program, a gadget that retries a failing server in a loop, or a scanner. It also fills the mtmon database faster than necessary.",
			Evidence: []string{
				fmt.Sprintf("%s produced %d flow records in the last 24 h; the typical client produced %d", name, t.Fl, med),
			},
			Steps: []string{
				"Open the client page and look at the destinations and ports it contacts.",
				"If it is a smart-home device that keeps hammering one address, check its firmware and its configuration.",
			},
		})
	}
	return out, ""
}

// ---- monitoring quality ----

const FlowSilent = 10 * 60 // seconds without flow packets before a router counts as silent

func ruleFlowDisabled(s Snapshot) ([]Suggestion, string) {
	var out []Suggestion
	seen := false
	for _, d := range routers(s) {
		if d.Caps == nil {
			continue
		}
		seen = true
		if !d.Caps.FlowSupported {
			continue
		}
		var ev []string
		switch {
		case !d.Caps.FlowEnabled:
			ev = []string{fmt.Sprintf("Traffic Flow is switched off on %s", d.Name)}
		case len(d.Caps.FlowTargets) == 0:
			ev = []string{fmt.Sprintf("Traffic Flow is on, but %s has no target configured", d.Name)}
		default:
			continue
		}
		step := "Enable Traffic Flow and add mtmon as target (commands below), or run the setup wizard from the Devices page."
		if d.Managed {
			step = "This device was set up by mtmon: run the setup wizard from the Devices page again to repair it."
		}
		out = append(out, Suggestion{
			Subject: d.Name, Severity: SevWarning, Category: CatMonitoring, Link: "device/" + d.Name, Confidence: "high",
			Title:    fmt.Sprintf("%s does not send Traffic Flow to mtmon", d.Name),
			Why:      "Without Traffic Flow mtmon cannot see who talks to whom: no client volumes, destinations, services or countries. The device is only monitored for its health.",
			Evidence: ev,
			Steps:    []string{step, "Use the IP address of the mtmon host and the flow port from the mtmon settings."},
			Commands: "/ip traffic-flow set enabled=yes interfaces=all\n/ip traffic-flow target add dst-address=<mtmon-IP> port=<flow-port> version=ipfix",
			Limits:   "Based on the capabilities stored when the device was set up.",
		})
	}
	if !seen {
		return nil, "no stored router capabilities"
	}
	return out, ""
}

func ruleFlowSilent(s Snapshot) ([]Suggestion, string) {
	if s.ProcessUptime().Seconds() < FlowSilent {
		return nil, "mtmon was started less than 10 minutes ago"
	}
	now := s.Now().Unix()
	var out []Suggestion
	for _, d := range routers(s) {
		if !d.FlowKnown || !d.Up {
			continue
		}
		if d.Caps != nil && d.Caps.FlowSupported && !d.Caps.FlowEnabled {
			continue // reported by flow-disabled
		}
		var ev string
		switch {
		case d.FlowLast == 0:
			ev = fmt.Sprintf("No Traffic Flow packet from %s has arrived since mtmon started (%d min ago)", d.Name, int(s.ProcessUptime().Minutes()))
		case now-d.FlowLast > FlowSilent:
			ev = fmt.Sprintf("The last Traffic Flow packet from %s arrived %d min ago", d.Name, (now-d.FlowLast)/60)
		default:
			continue
		}
		out = append(out, Suggestion{
			Subject: d.Name, Severity: SevWarning, Category: CatMonitoring, Link: "device/" + d.Name, Confidence: "high",
			Title:    fmt.Sprintf("%s is reachable, but no flow data arrives", d.Name),
			Why:      "The router answers mtmon's status polls, so it is up, but its Traffic Flow packets do not arrive. As long as that is the case, traffic statistics, services and alerts based on flows are frozen or empty.",
			Evidence: []string{ev},
			Steps: []string{
				"Check that the Traffic Flow target points to the mtmon host and the correct port, and that the status is enabled.",
				"Check that no firewall rule between the router and mtmon blocks UDP on the flow port.",
				"If the router sends from a different source address than the one in the device list, add it as flow source in the device settings.",
			},
			Commands: "/ip traffic-flow print\n/ip traffic-flow target print",
		})
	}
	return out, ""
}

func ruleNoSyslog(s Snapshot) ([]Suggestion, string) {
	rs := routers(s)
	if len(rs) == 0 {
		return nil, "no router devices"
	}
	var out []Suggestion
	for _, d := range rs {
		if d.FwLogging || (d.Caps != nil && d.Caps.LogActions > 0) {
			continue
		}
		step := "Add a remote logging action that points to the mtmon host and log the firewall topic, or run the setup wizard from the Devices page."
		if d.Managed {
			step = "This device was set up by mtmon: run the setup wizard from the Devices page again."
		}
		conf, lim := "high", ""
		if d.Caps == nil {
			conf, lim = "medium", "The device capabilities are not stored (device comes from the config file), so this is judged from the log events mtmon has received."
		}
		out = append(out, Suggestion{
			Subject: d.Name, Severity: SevTip, Category: CatMonitoring, Link: "device/" + d.Name, Confidence: conf, Limits: lim,
			Title: fmt.Sprintf("%s sends no firewall logs to mtmon", d.Name),
			Why:   "Without syslog, the Firewall page stays empty: you cannot see what is blocked, which clients are refused, or whether a rule does what you expect.",
			Evidence: []string{
				fmt.Sprintf("No firewall log rules or remote logging action were found for %s", d.Name),
			},
			Steps:    []string{step, "Then enable logging with a prefix on the firewall rules you care about."},
			Commands: "/system logging action add name=mtmon target=remote remote=<mtmon-IP> remote-port=<syslog-port>\n/system logging add topics=firewall action=mtmon",
		})
	}
	return out, ""
}
