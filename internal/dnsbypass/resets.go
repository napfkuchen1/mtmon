package dnsbypass

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// MetaReset holds the "mark as fixed" times: {"<mac>": unix seconds}. Only flows after that time count for the client.
const MetaReset = "dns_bypass_reset"

// EndedAfter is how long a client must be quiet before its bypass counts as ended (no longer a current problem).
const EndedAfter = 24 * time.Hour

// resetKeep drops reset entries that are older than this; they no longer hide anything.
const resetKeep = 90 * 24 * time.Hour

// LoadResets returns the reset times per MAC (never nil).
func LoadResets(st *store.Store) map[string]int64 {
	m := map[string]int64{}
	if raw := st.GetMeta(MetaReset); raw != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	return m
}

// SaveResets stores the reset times; entries older than 90 days are dropped.
func SaveResets(st *store.Store, m map[string]int64, now time.Time) error {
	cut := now.Add(-resetKeep).Unix()
	for k, v := range m {
		if v < cut {
			delete(m, k)
		}
	}
	b, _ := json.Marshal(m)
	return st.SetMeta(MetaReset, string(b))
}

// ForgetSeen removes the "already reported" marks of the given MACs, so the alert fires again if a bypass returns.
func ForgetSeen(st *store.Store, macs map[string]bool) {
	var list []string
	raw := st.GetMeta(metaSeen)
	if raw == "" || json.Unmarshal([]byte(raw), &list) != nil {
		return
	}
	keep := list[:0]
	for _, k := range list {
		mac, _, _ := strings.Cut(k, "|")
		if !macs[mac] {
			keep = append(keep, k)
		}
	}
	b, _ := json.Marshal(keep)
	_ = st.SetMeta(metaSeen, string(b))
}

// resetFrom is the first hourly bucket that counts after a reset at ts: the bucket that holds the reset itself also
// holds flows from before it.
func resetFrom(ts int64) int64 { return ts/3600*3600 + 3600 }
