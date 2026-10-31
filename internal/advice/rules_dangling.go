package advice

import (
	"fmt"
	"strings"
)

// maxDanglingEvidence caps the evidence lines per device.
const maxDanglingEvidence = 8

// ruleDanglingIfaces: RouterOS keeps rules whose interface was deleted and shows them with the internal ID ("*61")
// instead of a name. They never match, so a drop rule or an access-list entry silently stops doing its job.
func ruleDanglingIfaces(s Snapshot) ([]Suggestion, string) {
	read := false
	var out []Suggestion
	for _, d := range s.Devices() {
		if d.Caps == nil || d.Caps.ProbedAt == 0 {
			continue
		}
		read = true
		if len(d.Caps.Dangling) == 0 {
			continue
		}
		sev, blocking := SevTip, 0
		var ev, cmds []string
		wifiRule := false
		for i, r := range d.Caps.Dangling {
			what := r.Menu + " " + r.Field + "=" + r.Value
			if r.Comment != "" {
				what += fmt.Sprintf(" (comment %q)", r.Comment)
			}
			if r.Action != "" {
				what += ", action " + r.Action
			}
			if r.Disabled {
				what += ", disabled"
			}
			if !r.Disabled && (r.Action == "drop" || r.Action == "reject") {
				blocking++
			}
			if strings.Contains(r.Menu, "/wifi") || strings.Contains(r.Menu, "/wireless") || strings.Contains(r.Menu, "capsman") {
				wifiRule = true
			}
			if i < maxDanglingEvidence {
				ev = append(ev, what)
				target := "<interface>"
				if r.Hint != "" {
					target = r.Hint
					ev = append(ev, fmt.Sprintf("last set to %s=%s in the router's history", r.Field, r.Hint))
				}
				cmds = append(cmds, fmt.Sprintf("%s set [find .id=%s] %s=%s", r.Menu, r.ID, r.Field, target), fmt.Sprintf("# only if the rule is no longer wanted: %s remove [find .id=%s]", r.Menu, r.ID))
			}
		}
		if blocking > 0 {
			sev = SevWarning
		}
		if n := len(d.Caps.Dangling); n > maxDanglingEvidence {
			ev = append(ev, fmt.Sprintf("…(+%d)", n-maxDanglingEvidence))
		}
		ev = append(ev, d.Caps.ReadNote())
		why := "These rules point to an interface that no longer exists, so RouterOS shows its internal ID instead of a name and the rule can never match. This is common after CAPsMAN re-created a CAP interface, a CAP was reset, or a USB/LTE/VPN interface came back."
		if blocking > 0 {
			why += fmt.Sprintf(" %d of them are drop/reject rules that were meant to block something and now do nothing.", blocking)
		}
		steps := []string{
			"Open each listed rule and set the current interface name (or interface list) that it was meant for.",
			"If the rule is no longer needed, remove it.",
			"For Wi-Fi access-list entries on CAP interfaces: after a re-provisioning the interface gets a new ID, so check them again.",
		}
		if wifiRule {
			steps = append(steps, "If CAPsMAN provisioning uses action=create-dynamic-enabled, every CAP reconnect re-creates the interfaces with new IDs, so such rules break again after each reboot or power cut. The durable fix is action=create-enabled in /interface wifi provisioning (static interfaces); that re-provisions the radios once, which causes a short Wi-Fi drop.")
		}
		out = append(out, Suggestion{
			Subject: d.Name, Severity: sev, Category: CatHygiene, Link: "device/" + d.Name, Confidence: "high",
			Title:    fmt.Sprintf("%d rules on %s point to deleted interfaces", len(d.Caps.Dangling), d.Name),
			Why:      why,
			Evidence: ev,
			Steps:    steps,
			Commands: strings.Join(cmds, "\n"),
			Limits:   "Where the command shows <interface>, the router's history has no earlier setting of this rule: put in the interface it was meant for. Where it shows a name, that is the last value found in /system/history (RouterOS keeps only about 100 entries), so check it first. The remove line is only for rules that are not needed any more. Rows that RouterOS creates dynamically are ignored.",
		})
	}
	if !read {
		return nil, "no device whose rules were read (router rules are probed hourly)"
	}
	return out, ""
}
