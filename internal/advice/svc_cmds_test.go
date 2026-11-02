// SPDX-License-Identifier: AGPL-3.0-only

package advice

import (
	"strings"
	"testing"
)

// #295: the service restriction commands use available-from on RouterOS 7, keep the current list and add the real LAN.
func TestServiceCommandsUseAvailableFrom(t *testing.T) {
	d := base()
	d.Devs = []Device{
		{Name: "gw", Role: "router", Version: "7.24.4 (stable)", Caps: &Caps{ProbedAt: 1, Role: "router", WwwSSL: true,
			Addresses:   []string{"203.0.113.9/29 on ether1", "10.20.30.1/24 on bridge"},
			ServiceFrom: map[string]string{"ssh": "198.51.100.5/32,0.0.0.0/0"},
			OpenMgmt:    []string{"ssh", "winbox"}}},
		{Name: "old", Role: "router", Version: "6.49.10", Caps: &Caps{ProbedAt: 1, Role: "router", OpenMgmt: []string{"ssh"}}},
	}
	var cmds []string
	for _, rule := range []func(Snapshot) ([]Suggestion, string){ruleOpenMgmt, ruleMgmtOpen} {
		got, _ := rule(d)
		for _, s := range got {
			cmds = append(cmds, s.Commands)
		}
	}
	all := strings.Join(cmds, "\n")
	if !strings.Contains(all, "/ip service set [find name=ssh] available-from=198.51.100.5/32,10.20.30.0/24,<mtmon-IP>/32") {
		t.Errorf("ssh command lacks current list + LAN + mtmon:\n%s", all)
	}
	if !strings.Contains(all, "/ip service set [find name=winbox] available-from=10.20.30.0/24,<mtmon-IP>/32") {
		t.Errorf("winbox command:\n%s", all)
	}
	if !strings.Contains(all, "/ip service set [find name=www-ssl] available-from=10.20.30.0/24,<mtmon-IP>/32") {
		t.Errorf("www-ssl command:\n%s", all)
	}
	if strings.Contains(all, "192.168.88.0") {
		t.Errorf("hard coded example network left:\n%s", all)
	}
	// RouterOS 7: no suggestion may carry the old field
	for _, dev := range d.Devs[:1] {
		for _, n := range dev.Caps.OpenMgmt {
			if c := serviceRestrictCmd(dev, n); strings.Contains(c, " address=") {
				t.Errorf("address= on RouterOS 7: %s", c)
			}
		}
	}
	// older versions keep the old field name
	if c := serviceRestrictCmd(d.Devs[1], "ssh"); !strings.Contains(c, " address=<LAN-prefix>,<mtmon-IP>/32") {
		t.Errorf("pre-7 command: %s", c)
	}
}
