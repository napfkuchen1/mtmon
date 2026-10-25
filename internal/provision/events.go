package provision

import (
	"context"
	"strings"
)

// EventTopics are the RouterOS log topics sent to mtmon as the router event log. One rule per topic: a rule that
// lists several topics only matches lines carrying all of them.
var EventTopics = []string{"system", "account", "critical", "dhcp", "wireless", "wireguard", "script"}

// The wifi package logs under "wireless"; "wifi" is not a topic on RouterOS 7.24 and is rejected with HTTP 400.

// eventLogs adds one logging rule per topic that sends to the mtmon syslog action. The manifest descriptions start
// with "Event log rule" so FeatureOf can tell them from the firewall rule.
func (r *runner) eventLogs(ctx context.Context) error {
	for _, t := range EventTopics {
		if _, err := r.create(ctx, "/system/logging", map[string]string{"topics": t, "action": syslogAction},
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
