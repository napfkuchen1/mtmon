// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"net/netip"
	"sort"

	"github.com/napfkuchen1/mtmon/internal/addr"
)

// IPv6HistDays is how long a seen IPv6 address is remembered.
const IPv6HistDays = 30

// IsGlobalIPv6 reports whether s is a global unicast IPv6 address (2000::/3): link-local and ULA addresses do not come
// from the ISP prefix and are ignored.
func IsGlobalIPv6(s string) bool { return addr.IsGlobalIPv6(s) }

// NoteIPv6 records global IPv6 addresses (address -> client MAC) seen now. Never creates clients.
func (s *Store) NoteIPv6(addrs map[string]string, now int64) {
	if len(addrs) == 0 {
		return
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	for ip, mac := range addrs {
		if !IsGlobalIPv6(ip) || mac == "" {
			continue
		}
		tx.Exec(`INSERT INTO ipv6_hist(mac,ip,first,last) VALUES(?,?,?,?) ON CONFLICT(mac,ip) DO UPDATE SET last=excluded.last`, NormMAC(mac), ip, now, now)
	}
	tx.Commit()
}

// V6Prefix is one /64 a client has had addresses in.
type V6Prefix struct {
	Prefix string `json:"prefix"`
	Addrs  int    `json:"addrs"`
	First  int64  `json:"first"`
	Last   int64  `json:"last"`
}

// IPv6Prefixes groups the remembered global addresses per client by /64, newest prefix first. mac "" = all clients.
func (s *Store) IPv6Prefixes(mac string, since int64) (map[string][]V6Prefix, error) {
	q := `SELECT mac, ip, first, last FROM ipv6_hist WHERE last>=?`
	args := []any{since}
	if mac != "" {
		q += ` AND mac=?`
		args = append(args, NormMAC(mac))
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	by := map[string]map[string]*V6Prefix{}
	for rows.Next() {
		var m, ip string
		var first, last int64
		if err := rows.Scan(&m, &ip, &first, &last); err != nil {
			return nil, err
		}
		a, err := netip.ParseAddr(ip)
		if err != nil {
			continue
		}
		pf, _ := a.Prefix(64)
		key := pf.Masked().String()
		if by[m] == nil {
			by[m] = map[string]*V6Prefix{}
		}
		p := by[m][key]
		if p == nil {
			p = &V6Prefix{Prefix: key, First: first, Last: last}
			by[m][key] = p
		}
		p.Addrs++
		if first < p.First {
			p.First = first
		}
		if last > p.Last {
			p.Last = last
		}
	}
	out := map[string][]V6Prefix{}
	for m, ps := range by {
		list := make([]V6Prefix, 0, len(ps))
		for _, p := range ps {
			list = append(list, *p)
		}
		sort.Slice(list, func(i, j int) bool {
			return list[i].Last > list[j].Last || list[i].Last == list[j].Last && list[i].Prefix < list[j].Prefix
		})
		out[m] = list
	}
	return out, rows.Err()
}
