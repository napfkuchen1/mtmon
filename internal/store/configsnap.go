// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Configuration snapshots (#46): the device's scrubbed REST lists as gzip JSON. They are kept for a number of days
// (ConfigKeepDays, #190); configSnapKeep only caps the count per device as a safety net.

const (
	configSnapKeep        = 200
	DefaultConfigKeepDays = 90
	MinConfigKeepDays     = 7
)

// ConfigKeepDays is how long snapshots are kept (default 90, at least 7).
func (s *Store) ConfigKeepDays() int {
	n, err := strconv.Atoi(s.GetMeta("config_keep_days"))
	if err != nil || n <= 0 {
		return DefaultConfigKeepDays
	}
	return max(n, MinConfigKeepDays)
}

// SetConfigKeepDays stores the retention; values below the minimum are refused.
func (s *Store) SetConfigKeepDays(n int) error {
	if n < MinConfigKeepDays || n > 3650 {
		return fmt.Errorf("keep between %d and 3650 days", MinConfigKeepDays)
	}
	return s.SetMeta("config_keep_days", strconv.Itoa(n))
}

// PruneConfigSnaps deletes snapshots older than the retention. The newest snapshot of every device stays, however old.
func (s *Store) PruneConfigSnaps(now int64) (int64, error) {
	cutoff := now - int64(s.ConfigKeepDays())*86400
	res, err := s.DB.Exec(`DELETE FROM config_snap WHERE ts<? AND ts<(SELECT max(ts) FROM config_snap c WHERE c.device=config_snap.device)`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func migrateConfigSnap(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS config_snap(
  device TEXT NOT NULL, ts INTEGER NOT NULL, data BLOB NOT NULL, PRIMARY KEY(device, ts))`)
	return err
}

// ConfigSnap is section -> rows (field -> value).
type ConfigSnap map[string][]map[string]string

// SaveConfigSnap stores a snapshot and drops the oldest beyond the keep limit.
func (s *Store) SaveConfigSnap(device string, ts int64, snap ConfigSnap) error {
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(raw)
	zw.Close()
	if _, err := s.DB.Exec(`INSERT OR REPLACE INTO config_snap(device,ts,data) VALUES(?,?,?)`, device, ts, buf.Bytes()); err != nil {
		return err
	}
	_, err = s.DB.Exec(`DELETE FROM config_snap WHERE device=? AND ts NOT IN
  (SELECT ts FROM config_snap WHERE device=? ORDER BY ts DESC LIMIT ?)`, device, device, configSnapKeep)
	return err
}

// ConfigSnapTimes lists the snapshot times of a device, newest first.
func (s *Store) ConfigSnapTimes(device string) []int64 {
	out := []int64{}
	rows, err := s.DB.Query(`SELECT ts FROM config_snap WHERE device=? ORDER BY ts DESC`, device)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var ts int64
		if rows.Scan(&ts) == nil {
			out = append(out, ts)
		}
	}
	return out
}

// ConfigSnapAt loads one snapshot.
func (s *Store) ConfigSnapAt(device string, ts int64) (ConfigSnap, error) {
	var blob []byte
	if err := s.DB.QueryRow(`SELECT data FROM config_snap WHERE device=? AND ts=?`, device, ts).Scan(&blob); err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	var snap ConfigSnap
	return snap, json.Unmarshal(raw, &snap)
}

// ConfigCheckedAt is when the device was last compared (unix, 0 = never); kept in meta so the daily check survives restarts.
func (s *Store) ConfigCheckedAt(device string) int64 {
	n, _ := strconv.ParseInt(s.GetMeta("cfgchk:"+device), 10, 64)
	return n
}

// SetConfigChecked records a comparison.
func (s *Store) SetConfigChecked(device string, ts int64) {
	s.SetMeta("cfgchk:"+device, strconv.FormatInt(ts, 10))
}

// ConfigField is one changed field of a row.
type ConfigField struct {
	Key string `json:"key"`
	Old string `json:"old,omitempty"`
	New string `json:"new,omitempty"`
}

// ConfigChange is one added, removed or changed row.
type ConfigChange struct {
	Section string        `json:"section"`
	Kind    string        `json:"kind"` // added | removed | changed
	ID      string        `json:"id"`
	Label   string        `json:"label"`
	Fields  []ConfigField `json:"fields"`
}

// rowLabel names a row for humans: comment, name or the first identifying field.
func rowLabel(r map[string]string) string {
	for _, k := range []string{"comment", "name", "address", "interface", "chain", "dst-address", "list", "service"} {
		if r[k] != "" {
			return r[k]
		}
	}
	return r[".id"]
}

// DiffConfig compares two snapshots by router row id. Only sections present in both are compared, so a list that
// failed to read in one run does not show up as "everything removed".
func DiffConfig(old, cur ConfigSnap) []ConfigChange {
	out := []ConfigChange{}
	secs := []string{}
	for sec := range cur {
		if _, ok := old[sec]; ok {
			secs = append(secs, sec)
		}
	}
	sort.Strings(secs)
	for _, sec := range secs {
		om := map[string]map[string]string{}
		for _, r := range old[sec] {
			om[r[".id"]] = r
		}
		seen := map[string]bool{}
		for _, r := range cur[sec] {
			id := r[".id"]
			seen[id] = true
			o, ok := om[id]
			if !ok {
				out = append(out, ConfigChange{Section: sec, Kind: "added", ID: id, Label: rowLabel(r), Fields: fieldsOf(nil, r)})
				continue
			}
			if f := fieldsOf(o, r); len(f) > 0 {
				out = append(out, ConfigChange{Section: sec, Kind: "changed", ID: id, Label: rowLabel(r), Fields: f})
			}
		}
		for _, r := range old[sec] {
			if !seen[r[".id"]] {
				out = append(out, ConfigChange{Section: sec, Kind: "removed", ID: r[".id"], Label: rowLabel(r), Fields: fieldsOf(r, nil)})
			}
		}
	}
	return out
}

// idRenumbering: a reference like interface=*1A that RouterOS resolves differently after a CAP reconnect
// (dynamic interfaces are re-created with new internal ids). Nobody edited the rule, so it is no change.
func idRenumbering(a, b string) bool {
	return a != "" && b != "" && (isRouterID(a) || isRouterID(b)) && (isRouterID(a) && isRouterID(b) || isCapIface(a) || isCapIface(b))
}

func isRouterID(s string) bool {
	if len(s) < 2 || s[0] != '*' {
		return false
	}
	for _, c := range s[1:] {
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func isCapIface(s string) bool { return strings.HasPrefix(s, "cap-") }

func fieldsOf(o, n map[string]string) []ConfigField {
	keys := map[string]bool{}
	for k := range o {
		keys[k] = true
	}
	for k := range n {
		keys[k] = true
	}
	ks := make([]string, 0, len(keys))
	for k := range keys {
		if k != ".id" {
			ks = append(ks, k)
		}
	}
	sort.Strings(ks)
	f := []ConfigField{}
	for _, k := range ks {
		if o[k] != n[k] && !idRenumbering(o[k], n[k]) {
			f = append(f, ConfigField{Key: k, Old: o[k], New: n[k]})
		}
	}
	return f
}
