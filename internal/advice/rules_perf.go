package advice

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Thresholds (documented in docs and the UI footer).
const (
	MemHigh        = 85.0 // % RAM in use
	CPUHigh        = 70.0 // % CPU load
	SustainedFrac  = 0.8  // share of samples above the threshold that counts as "sustained"
	MinSamples     = 30   // 1-minute samples needed to trust the history (30 min)
	MetricWindowHr = 6
)

func ruleRAM(s Snapshot) ([]Suggestion, string) { return loadRule(s, "ram") }
func ruleCPU(s Snapshot) ([]Suggestion, string) { return loadRule(s, "cpu") }

func loadRule(s Snapshot, kind string) ([]Suggestion, string) {
	devs := s.Devices()
	if len(devs) == 0 {
		return nil, "no devices"
	}
	var out []Suggestion
	for _, d := range devs {
		var thr, avg, max, frac, cur float64
		if kind == "ram" {
			thr, avg, max, frac, cur = MemHigh, d.AvgMem, d.MaxMem, d.HighMemFrac, d.Mem
		} else {
			thr, avg, max, frac, cur = CPUHigh, d.AvgCPU, d.MaxCPU, d.HighCPUFrac, d.CPU
		}
		sg := Suggestion{Subject: d.Name, Category: CatPerformance, Link: "device/" + d.Name}
		if kind == "ram" {
			sg.Title = fmt.Sprintf("%s is using most of its memory", d.Name)
			sg.Why = "When RAM stays nearly full, a RouterOS device can become sluggish, drop connections or restart on its own. Typical causes are very large connection tables, big log buffers, or packages and features that are not needed."
			sg.Steps = []string{
				"Open the device page and look at the memory graph to see whether the usage grows steadily (a leak) or is flat.",
				"Check how many connections are tracked and which packages are installed; disable what you do not use.",
				"If the device is small (for example a 64 MB access point), consider a restart in a quiet hour or a newer RouterOS version.",
			}
			sg.Commands = "/system resource print\n/ip firewall connection print count-only\n/system package print"
		} else {
			sg.Title = fmt.Sprintf("%s has a high CPU load", d.Name)
			sg.Why = "A device that runs at a high CPU load for hours reacts slowly, may lose packets and delays monitoring queries. It usually means that too much traffic is processed by the CPU instead of the switch chip or FastTrack, or that a heavy feature (VPN, logging, proxy) is active."
			sg.Steps = []string{
				"Run the profiler for a few seconds to see which process uses the CPU.",
				"Check whether the bridge uses hardware offload and whether FastTrack is active on routers.",
				"Look at the busiest clients on the Live page; a single heavy download or camera stream can be enough.",
			}
			sg.Commands = "/tool profile duration=10\n/interface bridge port print\n/ip firewall filter print where action=fasttrack-connection"
		}
		switch {
		case d.Samples >= MinSamples && frac >= SustainedFrac:
			sg.Severity = SevWarning
			avgFmt := "CPU averaged %s%% over the last %d h (peak %s%%, %d samples)"
			if kind == "ram" {
				avgFmt = "Memory averaged %s%% over the last %d h (peak %s%%, %d samples)"
			}
			sg.Evidence = []string{
				fmt.Sprintf(avgFmt, pct(avg), MetricWindowHr, pct(max), d.Samples),
				fmt.Sprintf("%s%% of the samples were above %s%%", pct(frac*100), pct(thr)),
			}
			sg.Confidence = "high"
		case d.Samples < MinSamples && d.Up && d.LiveOK && cur > thr:
			sg.Severity = SevTip
			curFmt := "Current CPU reading is %s%% (limit %s%%)"
			if kind == "ram" {
				curFmt = "Current memory reading is %s%% (limit %s%%)"
			}
			sg.Evidence = []string{fmt.Sprintf(curFmt, pct(cur), pct(thr))}
			sg.Confidence = "low"
			sg.Limits = "Based on a single current reading because less than 30 minutes of history exists. It may be a short peak."
		default:
			continue
		}
		out = append(out, sg)
	}
	return out, ""
}

// ---- RouterOS versions ----

var verRe = regexp.MustCompile(`^\s*(\d+)\.(\d+)(?:\.(\d+))?`)

type ver struct {
	n   [3]int
	pre bool // beta / rc / testing / development
	raw string
}

func parseVer(v string) (ver, bool) {
	m := verRe.FindStringSubmatch(v)
	if m == nil {
		return ver{}, false
	}
	var x ver
	x.raw = strings.TrimSpace(v)
	for i := 0; i < 3; i++ {
		x.n[i], _ = strconv.Atoi(m[i+1])
	}
	l := strings.ToLower(v)
	x.pre = strings.Contains(l, "beta") || strings.Contains(l, "rc") || strings.Contains(l, "testing") || strings.Contains(l, "development")
	return x, true
}

func (a ver) less(b ver) bool {
	for i := 0; i < 3; i++ {
		if a.n[i] != b.n[i] {
			return a.n[i] < b.n[i]
		}
	}
	return false
}

func (a ver) short() string { return fmt.Sprintf("%d.%d.%d", a.n[0], a.n[1], a.n[2]) }

func ruleVersion(s Snapshot) ([]Suggestion, string) {
	type dv struct {
		d Device
		v ver
	}
	var all []dv
	for _, d := range s.Devices() {
		if v, ok := parseVer(d.Version); ok {
			all = append(all, dv{d, v})
		}
	}
	if len(all) < 2 {
		return nil, "fewer than two devices report a RouterOS version"
	}
	// reference: newest non-pre-release version of the fleet (never claims what is "latest" on the internet)
	var ref *dv
	for i := range all {
		if all[i].v.pre {
			continue
		}
		if ref == nil || ref.v.less(all[i].v) {
			ref = &all[i]
		}
	}
	if ref == nil {
		return nil, ""
	}
	var out []Suggestion
	for _, x := range all {
		if x.v.pre || x.d.Name == ref.d.Name || !x.v.less(ref.v) || x.v.n[0] != ref.v.n[0] {
			continue
		}
		sev := SevInfo
		if x.v.n[1] != ref.v.n[1] {
			sev = SevTip
		}
		out = append(out, Suggestion{
			Subject: x.d.Name, Severity: sev, Category: CatHygiene, Link: "device/" + x.d.Name, Confidence: "high",
			Title: fmt.Sprintf("%s runs an older RouterOS than the rest of your network", x.d.Name),
			Why:   "Devices on different RouterOS versions behave slightly differently, and bug or security fixes only reach the ones you update. Keeping the fleet on one version also makes problems easier to compare and reproduce.",
			Evidence: []string{
				fmt.Sprintf("%s runs %s", x.d.Name, x.v.short()),
				fmt.Sprintf("%s runs %s (newest version among your devices)", ref.d.Name, ref.v.short()),
			},
			Steps: []string{
				"Read the release notes for the target version on mikrotik.com and make a backup first.",
				"Update the device during a quiet hour; it reboots once.",
			},
			Commands: "/system backup save name=before-upgrade\n/system package update check-for-updates\n/system package update install",
			Limits:   "mtmon only compares your own devices with each other. It does not know whether newer versions exist on the internet.",
		})
	}
	return out, ""
}

func ruleFirmware(s Snapshot) ([]Suggestion, string) {
	var out []Suggestion
	have := false
	for _, d := range s.Devices() {
		if d.Firmware == "" || d.FirmwareUpg == "" {
			continue
		}
		have = true
		if d.Firmware == d.FirmwareUpg {
			continue
		}
		out = append(out, Suggestion{
			Subject: d.Name, Severity: SevInfo, Category: CatHygiene, Link: "device/" + d.Name, Confidence: "high",
			Title: fmt.Sprintf("%s: the RouterBOARD firmware can be updated", d.Name),
			Why:   "RouterOS updates ship a matching RouterBOARD firmware, but it is not applied automatically. Until it is upgraded and the device rebooted, the device runs the older firmware.",
			Evidence: []string{
				fmt.Sprintf("Running firmware %s, available with the installed RouterOS: %s", d.Firmware, d.FirmwareUpg),
			},
			Steps:    []string{"Upgrade the firmware and reboot the device once during a quiet hour."},
			Commands: "/system routerboard upgrade\n/system reboot",
		})
	}
	if !have {
		return nil, "no firmware information stored yet"
	}
	return out, ""
}

func ruleFasttrack(s Snapshot) ([]Suggestion, string) {
	var out []Suggestion
	seen := false
	for _, d := range s.Devices() {
		if d.Caps == nil {
			continue
		}
		seen = true
		if !d.Caps.Fasttrack {
			continue
		}
		out = append(out, Suggestion{
			Subject: d.Name, Severity: SevInfo, Category: CatMonitoring, Link: "device/" + d.Name, Confidence: "high",
			Title: fmt.Sprintf("FastTrack on %s makes traffic statistics incomplete", d.Name),
			Why:   "FastTrack lets established connections skip most of the router's packet processing. That is great for speed, but those packets are no longer seen by Traffic Flow, so per-client volumes and some connections may be undercounted in mtmon. It is a trade-off between speed and accuracy, not an error.",
			Evidence: []string{
				fmt.Sprintf("An enabled FastTrack rule exists on %s (read from the device capabilities)", d.Name),
			},
			Steps: []string{
				"If exact numbers matter more than raw routing speed, disable the FastTrack rule (the router CPU will be busier).",
				"If speed matters more, keep it and treat volumes in mtmon as lower bounds.",
			},
			Fix:      &Fix{Device: d.Name, Features: []string{"fasttrack"}},
			Commands: "/ip firewall filter print where action=fasttrack-connection\n# optional: switch FastTrack off\n/ip firewall filter disable [find action=fasttrack-connection]",
			Limits:   "Based on the capabilities stored when the device was set up or last re-checked.",
		})
	}
	if !seen {
		return nil, "no stored device capabilities (devices added from the config file only)"
	}
	return out, ""
}

func ruleHWOffload(s Snapshot) ([]Suggestion, string) {
	var out []Suggestion
	seen := false
	for _, d := range s.Devices() {
		if d.Caps == nil || d.Role != "router" {
			continue
		}
		seen = true
		if !d.Caps.HWOffload {
			continue
		}
		out = append(out, Suggestion{
			Subject: d.Name, Severity: SevInfo, Category: CatMonitoring, Link: "device/" + d.Name, Confidence: "high",
			Title: fmt.Sprintf("Hardware offload on %s hides traffic between LAN ports", d.Name),
			Why:   "With bridge hardware offload the switch chip forwards frames between LAN ports without involving the CPU. This is fast, but that traffic never appears in Traffic Flow, so device-to-device traffic in your LAN is not visible. Internet traffic still passes the router and is counted.",
			Evidence: []string{
				fmt.Sprintf("Bridge ports with hardware offload were found on %s", d.Name),
			},
			Steps: []string{
				"Nothing to do if you only care about internet traffic.",
				"To see LAN-to-LAN traffic, turn hardware offload off on the relevant bridge ports (slower switching).",
			},
			Commands: "/interface bridge port print where hw=yes\n# optional, slows down LAN switching:\n/interface bridge port set [find] hw=no",
		})
	}
	if !seen {
		return nil, "no stored router capabilities"
	}
	return out, ""
}
