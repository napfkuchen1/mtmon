package provision

import (
	"net/netip"
	"strings"

	"github.com/napfkuchen1/mtmon/internal/poller"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// serviceFrom returns the source restriction of an /ip/service row: RouterOS 7.x calls the field "available-from",
// older versions "address". Empty = no restriction.
func serviceFrom(s poller.Row) string {
	if v := strings.TrimSpace(s["available-from"]); v != "" {
		return v
	}
	return strings.TrimSpace(s["address"])
}

// privateOnly reports whether every entry of a comma separated source list is a private range (RFC 1918, ULA,
// CGNAT 100.64/10, link-local, loopback). 0.0.0.0/0, ::/0 or any public range makes the service open.
func privateOnly(list string) bool {
	n := 0
	for _, e := range strings.Split(list, ",") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		n++
		var a netip.Addr
		if pf, err := netip.ParsePrefix(e); err == nil {
			if pf.Bits() == 0 {
				return false
			}
			a = pf.Masked().Addr()
			// the whole range must be private: its first and last address both count
			if !privateAddr(a) || !privateAddr(lastAddr(pf)) {
				return false
			}
			continue
		}
		var err error
		if a, err = netip.ParseAddr(e); err != nil || !privateAddr(a) {
			return false
		}
	}
	return n > 0
}

func lastAddr(pf netip.Prefix) netip.Addr {
	b := pf.Masked().Addr().AsSlice()
	for i := pf.Bits(); i < len(b)*8; i++ {
		b[i/8] |= 0x80 >> (i % 8)
	}
	a, _ := netip.AddrFromSlice(b)
	return a
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

func privateAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLoopback() || cgnat.Contains(a)
}

// openMgmtNames lists the enabled management services of an /ip/service print that answer from public sources. Rows
// of active sessions (connection=true) and other dynamic rows are not configuration; every name appears once.
func openMgmtNames(rows []poller.Row) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range rows {
		if s["dynamic"] == "true" || s["connection"] == "true" || s["disabled"] == "true" || !mgmtServices[s["name"]] || seen[s["name"]] {
			continue
		}
		if from := serviceFrom(s); from != "" && privateOnly(from) {
			continue
		}
		seen[s["name"]] = true
		out = append(out, s["name"])
	}
	return out
}

// pinnedMACs returns the client MACs that a Wi-Fi access-list rule names (an intentional pin or block, so a hint to
// "move to another band" does not apply). Dynamic rows are skipped.
func pinnedMACs(rows []poller.Row) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range rows {
		if r["dynamic"] == "true" {
			continue
		}
		mac := store.NormMAC(r["mac-address"])
		if mac == "" || seen[mac] {
			continue
		}
		seen[mac] = true
		out = append(out, mac)
	}
	return out
}
