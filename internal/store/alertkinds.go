// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"sort"
	"strings"
)

// InfoKinds are the alert kinds the user turned down to info ("make this kind info", #188). A kind in this list
// never raises a warning or critical; the alert is still stored and shown with its info badge.
func (s *Store) InfoKinds() []string {
	out := []string{}
	for _, k := range strings.Split(s.GetMeta("alert_info_kinds"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// IsInfoKind reports whether the kind is turned down to info.
func (s *Store) IsInfoKind(kind string) bool {
	for _, k := range s.InfoKinds() {
		if k == kind {
			return true
		}
	}
	return false
}

// SetInfoKind turns a kind down to info (on) or back to its own severity (off).
func (s *Store) SetInfoKind(kind string, on bool) error {
	set := map[string]bool{}
	for _, k := range s.InfoKinds() {
		set[k] = true
	}
	if on {
		set[kind] = true
	} else {
		delete(set, kind)
	}
	list := make([]string, 0, len(set))
	for k := range set {
		list = append(list, k)
	}
	sort.Strings(list)
	return s.SetMeta("alert_info_kinds", strings.Join(list, ","))
}
