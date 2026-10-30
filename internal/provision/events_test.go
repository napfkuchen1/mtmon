package provision

import "testing"

// #236: the event-log rules exclude debug messages of chatty topics, but keep "script,error" (cli_error needs it).
func TestEventTopicsExcludeDebug(t *testing.T) {
	want := map[string]bool{"system,!debug": true, "account": true, "critical": true, "dhcp,!debug": true,
		"wireless,!debug": true, "wireguard,!debug": true, "script,error": true}
	if len(EventTopics) != len(want) {
		t.Fatalf("%v", EventTopics)
	}
	for _, e := range EventTopics {
		if !want[e] {
			t.Errorf("unexpected rule %q", e)
		}
	}
}
