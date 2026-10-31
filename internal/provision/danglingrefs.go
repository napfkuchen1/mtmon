package provision

import (
	"context"
	"regexp"
	"strings"

	"github.com/napfkuchen1/mtmon/internal/poller"
)

// DanglingRef is a rule or setting that names an interface by its internal ID ("*61") because the interface itself
// no longer exists. RouterOS keeps such rows silently; they never match. Typical after a CAP interface was re-created
// by CAPsMAN, a CAP reset or when a USB/LTE/VPN interface came back.
type DanglingRef struct {
	Menu     string `json:"menu"`
	ID       string `json:"id"`
	Field    string `json:"field"`
	Value    string `json:"value"`
	Comment  string `json:"comment,omitempty"`
	Action   string `json:"action,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
	// Hint is the interface the rule was last set to according to /system/history, "" when the history has no such entry.
	Hint string `json:"hint,omitempty"`
}

// maxDanglingRefs keeps the stored probe small on a router with a badly broken rule set.
const maxDanglingRefs = 40

var reRouterOSID = regexp.MustCompile(`^\*[0-9A-Fa-f]+$`)

// danglingMenus lists the menus whose rows reference interfaces, and the fields that hold the reference.
var danglingMenus = []struct {
	menu   string
	fields []string
}{
	{"/interface/wifi/access-list", []string{"interface"}},
	{"/interface/wireless/access-list", []string{"interface"}},
	{"/caps-man/access-list", []string{"interface"}},
	{"/ip/firewall/filter", []string{"in-interface", "out-interface"}},
	{"/ip/firewall/nat", []string{"in-interface", "out-interface"}},
	{"/ip/firewall/mangle", []string{"in-interface", "out-interface"}},
	{"/ipv6/firewall/filter", []string{"in-interface", "out-interface"}},
	{"/ipv6/firewall/nat", []string{"in-interface", "out-interface"}},
	{"/ipv6/firewall/mangle", []string{"in-interface", "out-interface"}},
	{"/interface/list/member", []string{"interface"}},
	{"/interface/bridge/port", []string{"interface", "bridge"}},
	{"/ip/dhcp-server", []string{"interface"}},
	{"/ip/route", []string{"gateway"}},
	{"/queue/tree", []string{"parent"}},
}

// historyHint finds the interface a rule was last pointed to: the newest /system/history entry whose redo command
// names the rule ID and sets <field>=<name>. RouterOS lists history oldest first; "" when nothing matches.
func historyHint(history []poller.Row, id, field string) string {
	if id == "" {
		return ""
	}
	re := regexp.MustCompile(`(?:^|[\s;])` + regexp.QuoteMeta(field) + `=("[^"]+"|[^\s;]+)`)
	for i := len(history) - 1; i >= 0; i-- {
		redo := history[i]["redo"]
		if !hasWord(redo, id) {
			continue
		}
		if m := re.FindStringSubmatch(redo); m != nil {
			if v := strings.Trim(m[1], `"`); v != "" && !reRouterOSID.MatchString(v) {
				return v
			}
		}
	}
	return ""
}

// hasWord reports whether s contains tok as a whole word (so "*1" does not match "*1A").
func hasWord(s, tok string) bool {
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ';' || r == '=' || r == ',' || r == '"' || r == '[' || r == ']' }) {
		if w == tok {
			return true
		}
	}
	return false
}

// probeDanglingRefs finds rows that point to an interface ID that no longer resolves to a name (best effort).
func (k *Caps) probeDanglingRefs(ctx context.Context, c *poller.Client) {
	k.Dangling = []DanglingRef{}
	var history []poller.Row
	historyRead := false
	for _, m := range danglingMenus {
		for _, r := range get(ctx, c, m.menu) {
			if r["dynamic"] == "true" || r["dynamic"] == "yes" {
				continue
			}
			for _, f := range m.fields {
				if !reRouterOSID.MatchString(r[f]) {
					continue
				}
				if len(k.Dangling) >= maxDanglingRefs {
					return
				}
				if !historyRead { // only needed when something is dangling
					history, historyRead = get(ctx, c, "/system/history"), true
				}
				k.Dangling = append(k.Dangling, DanglingRef{Menu: m.menu, ID: r[".id"], Field: f, Value: r[f],
					Comment: r["comment"], Action: r["action"], Disabled: r["disabled"] == "true" || r["disabled"] == "yes",
					Hint: historyHint(history, r[".id"], f)})
			}
		}
	}
}
