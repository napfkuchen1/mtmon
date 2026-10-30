package provision

import (
	"context"
	"strings"
)

// EventTopics are the RouterOS topic lists of the logging rules that send the router event log to mtmon. One rule per
// entry: topics within a rule are AND-combined, so "dhcp,!debug" drops the packet dumps and "script,error" still
// matches "script,error,debug (ssh-cmd:...)" (needed for cli_error) but not other script noise. A plain topic would
// also match its debug lines.
var EventTopics = []string{"system,!debug", "account", "critical", "dhcp,!debug", "wireless,!debug", "wireguard,!debug", "script,error"}

// eventRuleComment marks the rules so they can be found reliably.
const eventRuleComment = "mtmon"

// The wifi package logs under "wireless"; "wifi" is not a topic on RouterOS 7.24 and is rejected with HTTP 400.

// eventLogs adds one logging rule per topic that sends to the mtmon syslog action. The manifest descriptions start
// with "Event log rule" so FeatureOf can tell them from the firewall rule.
func (r *runner) eventLogs(ctx context.Context) error {
	for _, t := range EventTopics {
		if _, err := r.create(ctx, "/system/logging", map[string]string{"topics": t, "action": syslogAction, "comment": eventRuleComment},
			"Event log rule "+t+" → "+syslogAction, map[string]string{"action": syslogAction, "topics": t}); err != nil {
			// A topic this RouterOS version does not know must not roll back the whole feature.
			if strings.Contains(err.Error(), "does not match any value of topic") {
				r.res.Skipped = append(r.res.Skipped, "topic "+t+" is not supported on this RouterOS")
				continue
			}
			return err
		}
	}
	return nil
}
