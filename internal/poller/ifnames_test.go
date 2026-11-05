// SPDX-License-Identifier: AGPL-3.0-only

package poller

import "testing"

// RouterOS lists interfaces with a hexadecimal .id; the flow exporter's ifIndex is that number in decimal.
func TestIfIndexFromRouterOSID(t *testing.T) {
	for id, want := range map[string]uint32{"*1": 1, "*A": 10, "*a": 10, "*67": 103, "*FF": 255, " *67 ": 103} {
		got, ok := ifIndexOf(id)
		if !ok || got != want {
			t.Errorf("%q: got %d %v, want %d", id, got, ok, want)
		}
	}
	for _, bad := range []string{"", "*", "*0", "*G1", "ether1", "*FFFFFFFFFF"} {
		if _, ok := ifIndexOf(bad); ok {
			t.Errorf("%q must not map", bad)
		}
	}
}

func TestParseIfNamesSkipsUnusableRows(t *testing.T) {
	got := parseIfNames([]Row{
		{".id": "*67", "name": "vlan20", "type": "vlan"},
		{"name": "no-id", "type": "ether"}, // an older mock or a different shape: nothing to map
		{".id": "*3"},                      // no name
	})
	if len(got) != 1 || got[0].Idx != 103 || got[0].Name != "vlan20" || got[0].Type != "vlan" {
		t.Fatalf("%+v", got)
	}
}
