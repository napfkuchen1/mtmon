// SPDX-License-Identifier: AGPL-3.0-only

package advice

import (
	"fmt"
	"net/netip"
	"strings"
)

// serviceRestrictCmd builds the /ip service command that limits a management service to its current sources plus the
// LAN of the device plus mtmon (#295). RouterOS 7 calls the field available-from, older versions address. Setting the
// field replaces the whole list, so the entries already in it are kept; 0.0.0.0/0 would mean "open" and is dropped.
func serviceRestrictCmd(d Device, name string) string {
	field := "available-from"
	if v, ok := parseVer(d.Version); ok && v.n[0] < 7 {
		field = "address"
	}
	var list []string
	seen := map[string]bool{}
	add := func(e string) {
		e = strings.TrimSpace(e)
		if e == "" || seen[e] {
			return
		}
		if pf, err := netip.ParsePrefix(e); err == nil && pf.Bits() == 0 {
			return
		}
		seen[e] = true
		list = append(list, e)
	}
	if d.Caps != nil {
		for _, e := range strings.Split(d.Caps.ServiceFrom[name], ",") {
			add(e)
		}
		add(lanPrefix(d.Caps.Addresses))
	}
	add("<mtmon-IP>/32")
	return fmt.Sprintf("/ip service set [find name=%s] %s=%s", name, field, strings.Join(list, ","))
}

// lanPrefix returns the first private network the device has an address in ("ip/mask on iface" entries), or a
// placeholder when there is none.
func lanPrefix(addrs []string) string {
	for _, a := range addrs {
		tok, _, _ := strings.Cut(strings.TrimSpace(a), " ")
		pf, err := netip.ParsePrefix(tok)
		if err != nil || !pf.Addr().Is4() || !pf.Addr().IsPrivate() || pf.Bits() == 32 {
			continue
		}
		return pf.Masked().String()
	}
	return "<LAN-prefix>"
}
