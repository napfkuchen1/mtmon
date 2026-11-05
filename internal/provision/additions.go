// SPDX-License-Identifier: AGPL-3.0-only

package provision

// An addition is a sub-step of a feature that is on, missing because the feature was set up before mtmon knew about it
// (#298: IPv6 drop rules on a router whose firewall log dates from an IPv4-only version). Enabling a feature that is on
// stays a no-op, and switching it off and on would touch every rule. So an addition is offered on its own: it plans
// only its missing steps, nothing else on the router changes, and switching the feature off undoes it with the rest.

// AddIPv6Firewall logs on the IPv6 drop rules of a router that has the firewall log on.
const AddIPv6Firewall = "ipv6_firewall"

// Addition is one missing sub-step, as the device dialog offers it.
type Addition struct {
	Key     string   `json:"key"`
	Feature string   `json:"feature"`
	Rules   []string `json:"rules"` // the rules it would change
}

// Additions lists what is missing on a router, given the features that are on. Never nil.
func Additions(k *Caps, on map[string]bool) []Addition {
	out := []Addition{}
	if k == nil || !on[FeatFwLog] {
		return out
	}
	var drops []string
	for _, f := range k.Filter6 {
		if !f.Disabled && !f.Managed && (f.Action == "drop" || f.Action == "reject") {
			drops = append(drops, f.ID)
		}
	}
	if rules := pendingRules(k, drops, false); len(rules) > 0 {
		out = append(out, Addition{Key: AddIPv6Firewall, Feature: FeatFwLog, Rules: rules})
	}
	return out
}

// ValidAddition tells whether mtmon knows this addition.
func ValidAddition(key string) bool { return key == AddIPv6Firewall }
