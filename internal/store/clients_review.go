package store

import (
	"database/sql"
	"strings"
)

// migrateClients adds columns introduced after the first release. "reviewed": clients that already exist when
// the column is created count as reviewed, so the inbox starts empty and only fills with devices that appear
// afterwards. "via": which observation made mtmon consider the client present (shown in the client details).
func migrateClients(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(clients)`)
	if err != nil {
		return err
	}
	has := map[string]bool{}
	for rows.Next() {
		var cid, nn, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &nn, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		has[name] = true
	}
	rows.Close()
	if !has["reviewed"] {
		if _, err := db.Exec(`ALTER TABLE clients ADD COLUMN reviewed INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
		if _, err := db.Exec(`UPDATE clients SET reviewed=1`); err != nil {
			return err
		}
	}
	if !has["via"] {
		if _, err := db.Exec(`ALTER TABLE clients ADD COLUMN via TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	return nil
}

// InboxClient is one row of the "new / unnamed devices" inbox.
type InboxClient struct {
	MAC       string `json:"mac"`
	Hostname  string `json:"hostname"`
	Vendor    string `json:"vendor"`
	IP        string `json:"ip"`
	Label     string `json:"label"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
	Online    bool   `json:"online"`
	WiFi      bool   `json:"wifi"`
	Device    string `json:"device"`
	SSID      string `json:"ssid"`
	Reviewed  bool   `json:"reviewed"`
}

// Inbox lists clients to look at: mode "new" = not yet reviewed, "unnamed" = no label and no hostname (reviewed or not).
// Online devices first, then newest first.
func (s *Store) Inbox(mode string, limit int) ([]InboxClient, error) {
	where := `reviewed=0`
	if mode == "unnamed" { // really nameless: neither a label nor a hostname from DHCP/DNS/neighbors
		where = `coalesce(label,'')='' AND coalesce(hostname,'')=''`
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.DB.Query(`SELECT mac,coalesce(hostname,''),coalesce(vendor,''),coalesce(ip,''),coalesce(label,''),first_seen,last_seen,
		online,wifi,coalesce(device,''),coalesce(ssid,''),reviewed FROM clients WHERE `+where+` ORDER BY online DESC, first_seen DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InboxClient{}
	for rows.Next() {
		var c InboxClient
		var on, wf, rv int
		if err := rows.Scan(&c.MAC, &c.Hostname, &c.Vendor, &c.IP, &c.Label, &c.FirstSeen, &c.LastSeen, &on, &wf, &c.Device, &c.SSID, &rv); err != nil {
			return nil, err
		}
		c.Online, c.WiFi, c.Reviewed = on == 1, wf == 1, rv == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// UnreviewedCount is the number of clients waiting in the inbox.
func (s *Store) UnreviewedCount() (n int) {
	s.DB.QueryRow(`SELECT count(*) FROM clients WHERE reviewed=0`).Scan(&n)
	return
}

// ReviewItem marks one client as reviewed and optionally gives it a label.
type ReviewItem struct {
	MAC   string `json:"mac"`
	Label string `json:"label"`
}

// ReviewClients applies labels (non-empty only) and marks the clients reviewed, atomically.
func (s *Store) ReviewClients(items []ReviewItem) (int, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for _, it := range items {
		mac := NormMAC(it.MAC)
		if mac == "" {
			continue
		}
		lbl := strings.TrimSpace(it.Label)
		q, args := `UPDATE clients SET reviewed=1 WHERE mac=?`, []any{mac}
		if lbl != "" {
			q, args = `UPDATE clients SET reviewed=1, label=? WHERE mac=?`, []any{lbl, mac}
		}
		r, err := tx.Exec(q, args...)
		if err != nil {
			return 0, err
		}
		if a, _ := r.RowsAffected(); a > 0 {
			n++
		}
	}
	return n, tx.Commit()
}

// TotalsBetween sums traffic in [from,to). ok is false when retention no longer covers the whole window.
func (s *Store) TotalsBetween(from, to int64) (up, down, flows int64, ok bool) {
	tbl, al := seriesTable(from, "")
	var oldest sql.NullInt64
	s.DB.QueryRow(`SELECT min(ts) FROM ` + tbl).Scan(&oldest)
	if !oldest.Valid || oldest.Int64 > from+al {
		return 0, 0, 0, false
	}
	s.DB.QueryRow(`SELECT coalesce(sum(b_up),0),coalesce(sum(b_down),0),coalesce(sum(flows),0) FROM `+tbl+` WHERE ts>=? AND ts<?`,
		from/al*al, to/al*al).Scan(&up, &down, &flows)
	return up, down, flows, true
}
