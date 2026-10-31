// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"testing"
	"time"
)

func TestIPv6PrefixesPerClient(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().Unix()
	old := now - 3*86400
	st.NoteIPv6(map[string]string{
		"2001:db8:1:1::10": "aa:00:00:00:00:01",
		"2001:db8:1:1::11": "aa:00:00:00:00:01", // same /64
		"fe80::1":          "aa:00:00:00:00:01", // link-local: ignored
		"fd00::5":          "aa:00:00:00:00:01", // ULA: ignored
		"192.168.0.5":      "aa:00:00:00:00:01", // not IPv6
	}, old)
	st.NoteIPv6(map[string]string{"2001:db8:1:2::10": "AA:00:00:00:00:01", "2001:db8:1:2::77": "AA:00:00:00:00:02"}, now)
	got, err := st.IPv6Prefixes("", now-7*86400)
	if err != nil {
		t.Fatal(err)
	}
	ps := got["AA:00:00:00:00:01"]
	if len(ps) != 2 || ps[0].Prefix != "2001:db8:1:2::/64" || ps[1].Prefix != "2001:db8:1:1::/64" || ps[1].Addrs != 2 {
		t.Fatalf("prefixes: %+v", ps)
	}
	if len(got["AA:00:00:00:00:02"]) != 1 {
		t.Errorf("second client: %+v", got)
	}
	one, _ := st.IPv6Prefixes("aa:00:00:00:00:02", now-7*86400)
	if len(one) != 1 {
		t.Errorf("single client query: %+v", one)
	}
}
