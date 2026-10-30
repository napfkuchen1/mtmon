package advice

import (
	"fmt"
	"strings"
)

// DebugLogMin is the number of debug log lines per hour from which a router counts as flooding mtmon (#236).
const DebugLogMin = 100

// debugLogAction is the logging action the setup wizard creates; the commands address the rules by it, not by index.
const debugLogAction = "mtmonsyslog"

// debugLogTopics maps a log topic to the topic of the logging rule that carries it.
var debugLogTopics = map[string]string{"dhcp": "dhcp", "wireguard": "wireguard", "wireless": "wireless", "wifi": "wireless"}

func ruleDebugLogs(s Snapshot) ([]Suggestion, string) {
	rs := routers(s)
	if len(rs) == 0 {
		return nil, "no router devices"
	}
	var out []Suggestion
	for _, d := range rs {
		if d.DebugPerHour < DebugLogMin {
			continue
		}
		var cmds, shown []string
		seen := map[string]bool{}
		for _, t := range d.DebugTopics {
			rt := debugLogTopics[t]
			if rt == "" || seen[rt] {
				continue
			}
			seen[rt] = true
			shown = append(shown, t)
			cmds = append(cmds, fmt.Sprintf("/system logging set [find action=%s topics=%s] topics=%s,!debug", debugLogAction, rt, rt))
		}
		if len(cmds) == 0 {
			continue
		}
		out = append(out, Suggestion{
			Subject: d.Name, Severity: SevTip, Category: CatMonitoring, Link: "device/" + d.Name, Confidence: "high",
			Title:    fmt.Sprintf("%s sends debug logs to mtmon", d.Name),
			Why:      "A logging rule with a plain topic also matches that topic's debug messages (DHCP packet dumps, WireGuard keepalives). They fill the device event log and add nothing. mtmon drops them on arrival, but the router still sends them.",
			Evidence: []string{fmt.Sprintf("About %d debug log lines per hour, from the topics: %s", d.DebugPerHour, strings.Join(shown, ", "))},
			Steps: []string{"Run the commands on the router: they exclude debug messages from the rules the setup wizard created for mtmon.",
				"Rules you made yourself for the same action with other topics are not touched; check them with /system logging print."},
			Commands: strings.Join(cmds, "\n"),
			Limits:   "The commands address the rules by their action name (" + debugLogAction + ") and topic; if you renamed the action, adjust it.",
		})
	}
	return out, ""
}
