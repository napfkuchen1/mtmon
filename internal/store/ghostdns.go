// SPDX-License-Identifier: AGPL-3.0-only

package store

import "github.com/napfkuchen1/mtmon/internal/addr"

// GhostDNSDays is how long a "client uses a DNS server that does not exist" sighting is remembered.
const GhostDNSDays = 7

// GhostDNS is one client that sends DNS to an address nobody owns (the router DNS redirect answers instead).
type GhostDNS struct {
	MAC, IP     string
	First, Last int64
	Flows       int64
}

// NoteGhostDNS adds sightings (key: client MAC, address of the DNS server that does not exist) seen now.
func (s *Store) NoteGhostDNS(n map[[2]string]int64, now int64) {
	if len(n) == 0 {
		return
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	for k, c := range n {
		tx.Exec(`INSERT INTO ghost_dns(mac,ip,first,last,flows) VALUES(?,?,?,?,?)
			ON CONFLICT(mac,ip) DO UPDATE SET last=excluded.last, flows=flows+excluded.flows`, NormMAC(k[0]), k[1], now, now, c)
	}
	tx.Commit()
}

// GhostDNSSince lists the sightings with activity since the given time.
func (s *Store) GhostDNSSince(since int64) ([]GhostDNS, error) {
	rows, err := s.DB.Query(`SELECT mac, ip, first, last, flows FROM ghost_dns WHERE last>=? ORDER BY flows DESC LIMIT 500`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GhostDNS{}
	for rows.Next() {
		var g GhostDNS
		if err := rows.Scan(&g.MAC, &g.IP, &g.First, &g.Last, &g.Flows); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// IsULA reports whether s is a unique-local IPv6 address (fc00::/7): stable inside the network, so worth remembering
// for a client although it is not part of the ISP prefix.
func IsULA(s string) bool { return addr.IsULA(s) }

// NoteULA remembers unique-local addresses of known clients (address -> client MAC) in ip_history, so traffic from
// such an address is still attributed to the client when the router neighbour entry is gone. Never creates clients.
func (s *Store) NoteULA(addrs map[string]string, now int64) {
	tx, err := s.DB.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	n := 0
	for ip, mac := range addrs {
		if !IsULA(ip) || mac == "" {
			continue
		}
		tx.Exec(`INSERT INTO ip_history(ip,mac,first,last) VALUES(?,?,?,?) ON CONFLICT(ip,mac) DO UPDATE SET last=excluded.last`, ip, NormMAC(mac), now, now)
		n++
	}
	if n > 0 {
		tx.Commit()
	}
}
