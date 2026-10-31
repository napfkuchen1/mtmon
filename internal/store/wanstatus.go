// SPDX-License-Identifier: AGPL-3.0-only

package store

// WANStatus is the latest result of one probe target: the error text of the last failed check (empty = it
// answered) and a note for a target that cannot be checked at all (no IPv6 address). Kept in memory only.
type WANStatus struct {
	Err  string
	Note string
}

// SetWANStatus records the latest result of a probe target.
func (s *Store) SetWANStatus(target string, st WANStatus) {
	s.wanStMu.Lock()
	defer s.wanStMu.Unlock()
	if s.wanSt == nil {
		s.wanSt = map[string]WANStatus{}
	}
	s.wanSt[target] = st
}

// ForgetWANStatus drops the status of targets that are no longer probed.
func (s *Store) ForgetWANStatus(keep map[string]bool) {
	s.wanStMu.Lock()
	defer s.wanStMu.Unlock()
	for t := range s.wanSt {
		if !keep[t] {
			delete(s.wanSt, t)
		}
	}
}

// WANStatusOf returns a copy of all target states.
func (s *Store) WANStatusOf() map[string]WANStatus {
	s.wanStMu.Lock()
	defer s.wanStMu.Unlock()
	out := make(map[string]WANStatus, len(s.wanSt))
	for k, v := range s.wanSt {
		out[k] = v
	}
	return out
}
