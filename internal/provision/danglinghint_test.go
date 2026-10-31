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
		{".id": "*16", "redo": "/interface set pppoe-out1 disabled=yes\r\n/interface wifi access-list set *2 interface=cap-wifi3\r\n", "undo": "/system script set x source=\"secret\""},
		{".id": "*17", "redo": "/interface wifi access-list set *3 comment=a-very-long-comm\\r\n    ent interface=cap-wifi4\r\n"},
	}
	for _, c := range []struct{ id, field, want string }{
		{"*1", "interface", "cap-wifi1"}, // newest named entry wins; *1A and an ID value are skipped
		{"*1", "in-interface", "my lan"},
		{"*1A", "interface", "other"},
		{"*2", "interface", "cap-wifi3"}, // several commands in one redo, CRLF separated
		{"*3", "interface", "cap-wifi4"}, // wrapped line is joined before matching
		{"*9", "interface", ""},
		{"", "interface", ""},
	} {
		if got := historyHint(h, c.id, c.field); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.id, c.field, got, c.want)
		}
	}
}
