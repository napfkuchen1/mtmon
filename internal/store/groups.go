// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"database/sql"
	"errors"
	"regexp"
	"sort"
	"strings"
)

// Group is a label for clients (IoT, Family, Servers, Guests, Lab, …) with its own alert behaviour: one threshold
// for phones, servers and smart plugs produces false alarms and blind spots at the same time.
type Group struct {
	Name         string  `json:"name"`
	NewAlert     bool    `json:"new_alert"`     // a new client of this group raises a new_client alert
	ChattyMult   float64 `json:"chatty_mult"`   // multiplies the "unusually many connections" limit (1 = normal)
	SpikeInfo    bool    `json:"spike_info"`    // traffic spikes caused by its clients are info, not a warning
	IgnoreWeak   bool    `json:"ignore_weak"`   // weak Wi-Fi signal is normal (battery gadgets around -75 dBm)
	WatchDefault bool    `json:"watch_default"` // clients put into the group are watched ("must be online")
}

var groupNameRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ._-]{0,31}$`)

// ValidGroupName: 1–32 characters, letters/digits first, then also space . _ -
func ValidGroupName(n string) bool { return groupNameRe.MatchString(n) }

// GroupPresets are offered with one click; sensible defaults per kind of device.
var GroupPresets = []Group{
	{Name: "IoT", NewAlert: true, ChattyMult: 3, IgnoreWeak: true},
	{Name: "Family", NewAlert: true, ChattyMult: 1},
	{Name: "Servers", NewAlert: false, ChattyMult: 10, SpikeInfo: true, WatchDefault: true},
	{Name: "Guests", NewAlert: false, ChattyMult: 2, SpikeInfo: true},
	{Name: "Lab", NewAlert: false, ChattyMult: 10, SpikeInfo: true},
}

func (s *Store) Groups() ([]Group, error) {
	rows, err := s.DB.Query(`SELECT name,new_alert,chatty_mult,spike_info,ignore_weak,watch_default FROM client_groups ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		var na, si, iw, wd int
		if err := rows.Scan(&g.Name, &na, &g.ChattyMult, &si, &iw, &wd); err != nil {
			return nil, err
		}
		g.NewAlert, g.SpikeInfo, g.IgnoreWeak, g.WatchDefault = na == 1, si == 1, iw == 1, wd == 1
		out = append(out, g)
	}
	return out, rows.Err()
}

// SaveGroup creates or updates a group.
func (s *Store) SaveGroup(g Group) error {
	g.Name = strings.TrimSpace(g.Name)
	if !ValidGroupName(g.Name) {
		return errors.New("invalid group name")
	}
	if g.ChattyMult <= 0 || g.ChattyMult > 1000 {
		g.ChattyMult = 1
	}
	_, err := s.DB.Exec(`INSERT INTO client_groups(name,new_alert,chatty_mult,spike_info,ignore_weak,watch_default) VALUES(?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET new_alert=excluded.new_alert, chatty_mult=excluded.chatty_mult, spike_info=excluded.spike_info,
		ignore_weak=excluded.ignore_weak, watch_default=excluded.watch_default`,
		g.Name, b2i(g.NewAlert), g.ChattyMult, b2i(g.SpikeInfo), b2i(g.IgnoreWeak), b2i(g.WatchDefault))
	return err
}

// DeleteGroup removes a group; its clients become ungrouped.
func (s *Store) DeleteGroup(name string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE clients SET grp='' WHERE grp=?`, name); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM client_groups WHERE name=?`, name); err != nil {
		return err
	}
	return tx.Commit()
}

// SetClientGroup puts clients into a group ("" = none). A group with watch_default also watches them.
func (s *Store) SetClientGroup(macs []string, group string) error {
	var g *Group
	if group != "" {
		gs, err := s.Groups()
		if err != nil {
			return err
		}
		for i := range gs {
			if gs[i].Name == group {
				g = &gs[i]
			}
		}
		if g == nil {
			return errors.New("unknown group")
		}
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, m := range macs {
		if _, err := tx.Exec(`UPDATE clients SET grp=? WHERE mac=?`, group, NormMAC(m)); err != nil {
			return err
		}
		if g != nil && g.WatchDefault {
			if _, err := tx.Exec(`UPDATE clients SET watch=1 WHERE mac=?`, NormMAC(m)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// ClientGroups returns mac → group settings for every grouped client.
func (s *Store) ClientGroups() (map[string]Group, error) {
	gs, err := s.Groups()
	if err != nil {
		return nil, err
	}
	by := map[string]Group{}
	for _, g := range gs {
		by[g.Name] = g
	}
	rows, err := s.DB.Query(`SELECT mac, grp FROM clients WHERE grp<>''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Group{}
	for rows.Next() {
		var mac, name string
		if err := rows.Scan(&mac, &name); err != nil {
			return nil, err
		}
		if g, ok := by[name]; ok {
			out[mac] = g
		}
	}
	return out, rows.Err()
}

// GroupStat is the size of a group.
type GroupStat struct {
	Group
	Clients int   `json:"clients"`
	Online  int   `json:"online"`
	Up      int64 `json:"up"`
	Down    int64 `json:"down"`
}

// GroupStats lists every group with its client counts and the traffic since `since`.
func (s *Store) GroupStats(since int64) ([]GroupStat, error) {
	gs, err := s.Groups()
	if err != nil {
		return nil, err
	}
	tot, _ := s.ClientTotals(since)
	traffic := map[string][2]int64{}
	for _, t := range tot {
		traffic[t.MAC] = [2]int64{t.Up, t.Down}
	}
	rows, err := s.DB.Query(`SELECT mac, grp, online FROM clients WHERE grp<>''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stat := map[string]*GroupStat{}
	out := make([]GroupStat, len(gs))
	for i, g := range gs {
		out[i].Group = g
		stat[g.Name] = &out[i]
	}
	for rows.Next() {
		var mac, name string
		var on sql.NullInt64
		if err := rows.Scan(&mac, &name, &on); err != nil {
			return nil, err
		}
		if st := stat[name]; st != nil {
			st.Clients++
			if on.Int64 == 1 {
				st.Online++
			}
			st.Up += traffic[mac][0]
			st.Down += traffic[mac][1]
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Up+out[i].Down > out[j].Up+out[j].Down })
	return out, rows.Err()
}
