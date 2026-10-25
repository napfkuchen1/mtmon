package speedtest

import (
	"net"
	"sync"
	"time"
)

// Traffic remembers the address pairs of mtmon's own speed-test connections for a while, so the flow pipeline can
// label the router's flow records of those connections (they show up as traffic of the mtmon host to the test
// servers) instead of treating them as client behaviour.
var Traffic = &Tracker{}

// tail covers the router's flow export delay (active/inactive timeouts) after a connection ended.
const tail = 2 * time.Minute

// Tracker is a small set of (local, remote) address pairs with a last-seen time.
type Tracker struct {
	mu sync.Mutex
	m  map[[2]string]time.Time
}

// Note records a connection of a test (called when it is established).
func (t *Tracker) Note(local, remote net.Addr) {
	l, r := host(local), host(remote)
	if l == "" || r == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = map[[2]string]time.Time{}
	}
	now := time.Now()
	t.m[[2]string{l, r}] = now
	if len(t.m) > 256 {
		for k, v := range t.m {
			if now.Sub(v) > tail {
				delete(t.m, k)
			}
		}
	}
}

// Match reports whether a flow between client address cip and remote address rip belongs to a recent test.
func (t *Tracker) Match(cip, rip string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	v, ok := t.m[[2]string{cip, rip}]
	return ok && time.Since(v) < tail
}

func host(a net.Addr) string {
	if a == nil {
		return ""
	}
	h, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return ""
	}
	return h
}
