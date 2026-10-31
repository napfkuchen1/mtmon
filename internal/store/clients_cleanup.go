// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"errors"
	"strings"
	"time"
)

// MinCleanupDays is the lower bound for the offline threshold of CleanupClients (0 = every client that is
// offline right now). Online clients are never removed, so there is no safety need for a higher floor.
const MinCleanupDays = 0

// CleanupOpts selects which stale client identities CleanupClients removes.
type CleanupOpts struct {
	Days        int      // not seen for at least this many days (>= MinCleanupDays)
	UnusedDays  int      // >0: additionally require that no traffic was recorded for the client in the last N days
	KeepLabeled bool     // keep clients that carry a user label
	DryRun      bool     // only count / preview, delete nothing
	ProtectMACs []string // never remove these (managed devices, known neighbours)
	ProtectIPs  []string // never remove clients currently using these addresses (managed devices)
	Preview     int      // number of candidates to list in the result (default 15)
}

// StaleClient is one cleanup candidate.
type StaleClient struct {
	MAC      string `json:"mac"`
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	Label    string `json:"label"`
	Vendor   string `json:"vendor"`
	IP       string `json:"ip"`
	LastSeen int64  `json:"last_seen"`
}

// CleanupResult reports what was (or, for a dry run, would be) removed.
type CleanupResult struct {
	Count   int           `json:"count"`
	Removed int           `json:"removed"`
	DryRun  bool          `json:"dry_run"`
	Sample  []StaleClient `json:"sample"`
}

// CleanupClients removes identity/metadata rows of clients that have not been seen for Days days:
// clients, ip_history, roam and seen_dest. Traffic aggregates (rollup_*, flows, fw_events) are keyed by
// MAC but are not identity data, so they stay: totals and charts remain correct and a returning
// device simply shows up as a new client with its old traffic still attributed to the same MAC.
// Online clients, and protected (managed-device) clients, are never touched.
func (s *Store) CleanupClients(o CleanupOpts) (*CleanupResult, error) {
	if o.Days < MinCleanupDays || o.UnusedDays < 0 {
		return nil, errors.New("days must not be negative")
	}
	if o.Preview <= 0 {
		o.Preview = 15
	}
	cut := time.Now().Add(-time.Duration(o.Days) * 24 * time.Hour).Unix()
	where := ` online=0 AND last_seen>0 AND last_seen<=?`
	args := []any{cut}
	if o.UnusedDays > 0 {
		// rollup_1h is indexed by (mac, ts): one index probe per candidate
		where += ` AND NOT EXISTS (SELECT 1 FROM rollup_1h r WHERE r.mac=clients.mac AND r.ts>=?)`
		args = append(args, time.Now().Add(-time.Duration(o.UnusedDays)*24*time.Hour).Unix())
	}
	if o.KeepLabeled {
		where += ` AND coalesce(label,'')=''`
	}
	for _, m := range o.ProtectMACs {
		if m = NormMAC(m); m != "" {
			where += ` AND mac<>?`
			args = append(args, m)
		}
	}
	for _, ip := range o.ProtectIPs {
		if ip = strings.TrimSpace(ip); ip != "" {
			where += ` AND coalesce(ip,'')<>?`
			args = append(args, ip)
		}
	}
	res := &CleanupResult{DryRun: o.DryRun, Sample: []StaleClient{}}
	if err := s.DB.QueryRow(`SELECT count(*) FROM clients WHERE`+where, args...).Scan(&res.Count); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(`SELECT mac, coalesce(nullif(label,''),nullif(hostname,''),mac), coalesce(hostname,''), coalesce(label,''),
		coalesce(vendor,''), coalesce(ip,''), last_seen FROM clients WHERE`+where+` ORDER BY last_seen ASC LIMIT ?`, append(append([]any{}, args...), o.Preview)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c StaleClient
		if err := rows.Scan(&c.MAC, &c.Name, &c.Hostname, &c.Label, &c.Vendor, &c.IP, &c.LastSeen); err != nil {
			rows.Close()
			return nil, err
		}
		res.Sample = append(res.Sample, c)
	}
	rows.Close()
	if o.DryRun || res.Count == 0 {
		return res, nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Select the victims once inside the transaction so the dependent deletes use exactly the same set.
	sub := `(SELECT mac FROM clients WHERE` + where + `)`
	for _, q := range []string{
		`DELETE FROM ip_history WHERE mac IN ` + sub,
		`DELETE FROM roam WHERE mac IN ` + sub,
		`DELETE FROM seen_dest WHERE mac IN ` + sub,
	} {
		if _, err := tx.Exec(q, args...); err != nil {
			return nil, err
		}
	}
	r, err := tx.Exec(`DELETE FROM clients WHERE`+where, args...)
	if err != nil {
		return nil, err
	}
	n, _ := r.RowsAffected()
	res.Removed = int(n)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

// RemoveWANClients deletes offline, unlabeled client records that were last seen on a WAN interface of device
// (the upstream modem learned before WAN ports were excluded). Their flow history stays.
func (s *Store) RemoveWANClients(device string, ifaces []string) int {
	if device == "" || len(ifaces) == 0 {
		return 0
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ifaces)), ",")
	args := []any{device}
	for _, i := range ifaces {
		args = append(args, i)
	}
	where := ` online=0 AND device=? AND iface IN (` + ph + `) AND coalesce(label,'')='' AND mac NOT LIKE 'VPN:%'`
	tx, err := s.DB.Begin()
	if err != nil {
		return 0
	}
	defer tx.Rollback()
	sub := `(SELECT mac FROM clients WHERE` + where + `)`
	for _, t := range []string{"ip_history", "roam", "seen_dest"} {
		if _, err := tx.Exec(`DELETE FROM `+t+` WHERE mac IN `+sub, args...); err != nil {
			return 0
		}
	}
	r, err := tx.Exec(`DELETE FROM clients WHERE`+where, args...)
	if err != nil || tx.Commit() != nil {
		return 0
	}
	n, _ := r.RowsAffected()
	return int(n)
}
