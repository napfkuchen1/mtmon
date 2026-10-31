// SPDX-License-Identifier: AGPL-3.0-only

package alert

import (
	"strconv"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// Reject-loop detection (#211): a Wi-Fi access-list rule with action=reject that names the client and whose
// match-count rose while the client keeps disconnecting. The counters reset on a rule edit or reboot, so only
// deltas between two reads inside one burst count, never absolute values.
const (
	aclEvery   = 45 * time.Second // minimum gap between two reads of one device
	aclBaseTTL = 20 * time.Minute // a baseline older than this is replaced instead of compared
)

type aclState struct {
	at     time.Time
	baseAt time.Time
	base   map[string]int
}

// aclRule is the part of an access-list row that names a reject loop.
type aclRule struct{ Comment, Iface string }

// matchCount reads the match-count of a row; RouterOS leaves the field out while it is 0.
func matchCount(r map[string]string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(r["match-count"]))
	return n
}

// macMatches reports whether a rule's mac-address names mac. A rule with a mask (a/b form) covers a range and is
// matched on the masked bytes.
func macMatches(rule, mac string) bool {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return false
	}
	addr, mask, hasMask := strings.Cut(rule, "/")
	a, m := store.NormMAC(addr), store.NormMAC(mask)
	if !hasMask || m == "" {
		return a == store.NormMAC(mac)
	}
	ab, mb, cb := macBytes(a), macBytes(m), macBytes(store.NormMAC(mac))
	if ab == nil || mb == nil || cb == nil {
		return false
	}
	for i := range ab {
		if ab[i]&mb[i] != cb[i]&mb[i] {
			return false
		}
	}
	return true
}

func macBytes(mac string) []byte {
	p := strings.Split(strings.ReplaceAll(strings.ToLower(mac), "-", ":"), ":")
	if len(p) != 6 {
		return nil
	}
	out := make([]byte, 6)
	for i, h := range p {
		v, err := strconv.ParseUint(h, 16, 8)
		if err != nil {
			return nil
		}
		out[i] = byte(v)
	}
	return out
}

// rejectLoop reads the access-list of dev (at most once per aclEvery) and returns the reject rule for mac whose
// match-count grew since the baseline of this burst. The first read of a burst only sets the baseline.
func (e *Engine) rejectLoop(dev, mac string, now time.Time) (aclRule, bool) {
	if e.AccessList == nil {
		return aclRule{}, false
	}
	e.mu.Lock()
	if e.acl == nil {
		e.acl = map[string]*aclState{}
	}
	st := e.acl[dev]
	if st != nil && now.Sub(st.at) < aclEvery {
		e.mu.Unlock()
		return e.aclHitLocked(dev, mac)
	}
	e.mu.Unlock()
	rows, err := e.AccessList(dev)
	if err != nil {
		return aclRule{}, false
	}
	e.mu.Lock()
	if st == nil {
		st = &aclState{}
		e.acl[dev] = st
	}
	st.at = now
	cur := map[string]int{}
	if e.aclHits == nil {
		e.aclHits = map[string]aclRule{}
	}
	fresh := st.base == nil || now.Sub(st.baseAt) > aclBaseTTL
	hits := map[string]aclRule{}
	for i, r := range rows {
		if r["disabled"] == "true" {
			continue
		}
		id := r[".id"]
		if id == "" {
			id = "#" + strconv.Itoa(i)
		}
		n := matchCount(r)
		cur[id] = n
		if fresh || r["action"] != "reject" || !macMatches(r["mac-address"], mac) {
			continue
		}
		if b, ok := st.base[id]; ok && n > b {
			hits[mac] = aclRule{Comment: r["comment"], Iface: r["interface"]}
		}
	}
	if fresh {
		st.base, st.baseAt = cur, now
	} else {
		for id, n := range cur { // a counter that went down was reset: take it as the new baseline
			if b, ok := st.base[id]; !ok || n < b {
				st.base[id] = n
			}
		}
	}
	if h, ok := hits[mac]; ok {
		e.aclHits[dev+"|"+mac] = h
	}
	e.mu.Unlock()
	return e.aclHitLocked(dev, mac)
}

func (e *Engine) aclHitLocked(dev, mac string) (aclRule, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	h, ok := e.aclHits[dev+"|"+mac]
	return h, ok
}

// rejectTail is the story tail of a reject loop with the ways out.
func rejectTail(r aclRule) string {
	where := r.Iface
	if where == "" {
		where = "an access point"
	}
	rule := r.Comment
	if rule == "" {
		rule = "reject"
	}
	return " – the client is rejected on " + where + " by access-list rule '" + rule + "', probably a reject loop" +
		" – options: keep it if the burst is short, replace the reject by signal-range on the unwanted AP, or remove the pin"
}
