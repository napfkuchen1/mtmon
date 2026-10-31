package provision

import (
	"testing"

	"github.com/napfkuchen1/mtmon/internal/poller"
)

// The redo strings are synthetic (the #237 fixture has no interface edits); they follow RouterOS command syntax.
func TestHistoryHint(t *testing.T) {
	h := []poller.Row{
		{".id": "*10", "redo": "/interface wifi access-list set *1 interface=cap-wifi0"},
		{".id": "*11", "redo": "/interface wifi access-list set *1A interface=other"},
		{".id": "*12", "redo": "/interface wifi access-list set *1 interface=cap-wifi1"},
		{".id": "*13", "redo": "/ip firewall filter set *1 in-interface=\"my lan\""},
		{".id": "*14", "redo": "/interface wifi access-list set *1 interface=*61"},
		{".id": "*15", "action": "device changed"},
	}
	for _, c := range []struct{ id, field, want string }{
		{"*1", "interface", "cap-wifi1"}, // newest named entry wins; *1A and an ID value are skipped
		{"*1", "in-interface", "my lan"},
		{"*1A", "interface", "other"},
		{"*9", "interface", ""},
		{"", "interface", ""},
	} {
		if got := historyHint(h, c.id, c.field); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.id, c.field, got, c.want)
		}
	}
}
