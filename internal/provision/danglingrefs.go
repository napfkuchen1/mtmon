package provision

import (
	"context"
	"regexp"

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
}

// probeDanglingRefs finds rows that point to an interface ID that no longer resolves to a name (best effort).
func (k *Caps) probeDanglingRefs(ctx context.Context, c *poller.Client) {
	k.Dangling = []DanglingRef{}
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
				k.Dangling = append(k.Dangling, DanglingRef{Menu: m.menu, ID: r[".id"], Field: f, Value: r[f],
					Comment: r["comment"], Action: r["action"], Disabled: r["disabled"] == "true" || r["disabled"] == "yes"})
			}
		}
	}
}
