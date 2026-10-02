// Package store is the SQLite persistence layer (WAL, batched writes, rollups).
package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS meta(k TEXT PRIMARY KEY, v TEXT);
CREATE TABLE IF NOT EXISTS clients(
  mac TEXT PRIMARY KEY, hostname TEXT, vendor TEXT, ip TEXT, label TEXT,
  first_seen INTEGER, last_seen INTEGER,
  device TEXT, iface TEXT, ssid TEXT, band TEXT, signal INTEGER, tx_rate TEXT, rx_rate TEXT,
  wifi INTEGER DEFAULT 0, online INTEGER DEFAULT 0);
CREATE TABLE IF NOT EXISTS ip_history(
  ip TEXT, mac TEXT, first INTEGER, last INTEGER, PRIMARY KEY(ip, mac));
CREATE INDEX IF NOT EXISTS ip_history_mac ON ip_history(mac);
CREATE TABLE IF NOT EXISTS roam(
  ts INTEGER, mac TEXT, from_dev TEXT, to_dev TEXT);
CREATE INDEX IF NOT EXISTS roam_mac ON roam(mac, ts);
CREATE TABLE IF NOT EXISTS flows(
  ts INTEGER, exporter TEXT, mac TEXT, cip TEXT, rip TEXT, cport INTEGER, rport INTEGER,
  proto INTEGER, dir TEXT, bytes INTEGER, pkts INTEGER, flags INTEGER,
  host TEXT, cc TEXT, asn INTEGER, asorg TEXT, svc TEXT);
CREATE INDEX IF NOT EXISTS flows_ts ON flows(ts);
CREATE INDEX IF NOT EXISTS flows_mac_ts ON flows(mac, ts);
CREATE TABLE IF NOT EXISTS rollup_1m(
  ts INTEGER, mac TEXT, rip TEXT, rport INTEGER, proto INTEGER,
  svc TEXT, host TEXT, cc TEXT, asn INTEGER, asorg TEXT,
  b_up INTEGER, b_down INTEGER, b_int INTEGER, flows INTEGER,
  PRIMARY KEY(ts, mac, rip, rport, proto)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS rollup_mac ON rollup_1m(mac, ts);
CREATE TABLE IF NOT EXISTS rollup_1h(
  ts INTEGER, mac TEXT, rip TEXT, rport INTEGER, proto INTEGER,
  svc TEXT, host TEXT, cc TEXT, asn INTEGER, asorg TEXT,
  b_up INTEGER, b_down INTEGER, b_int INTEGER, flows INTEGER,
  PRIMARY KEY(ts, mac, rip, rport, proto)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS rollup_h_mac ON rollup_1h(mac, ts);
-- tiny per-client hourly totals: long-range client lists, totals and charts
CREATE TABLE IF NOT EXISTS rollup_1h_client(
  ts INTEGER, mac TEXT, b_up INTEGER, b_down INTEGER, b_int INTEGER, flows INTEGER,
  PRIMARY KEY(ts, mac)) WITHOUT ROWID;
-- per-destination daily totals (no client dimension): long-range destination/port/service/country lists
CREATE TABLE IF NOT EXISTS rollup_1d(
  ts INTEGER, rip TEXT, rport INTEGER, proto INTEGER,
  svc TEXT, host TEXT, cc TEXT, asn INTEGER, asorg TEXT,
  b_up INTEGER, b_down INTEGER, b_int INTEGER, flows INTEGER,
  PRIMARY KEY(ts, rip, rport, proto)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS dev_metrics(
  ts INTEGER, device TEXT, cpu REAL, mem_used INTEGER, mem_total INTEGER,
  temp REAL, uptime INTEGER, clients INTEGER, up INTEGER);
CREATE INDEX IF NOT EXISTS dev_metrics_k ON dev_metrics(device, ts);
CREATE TABLE IF NOT EXISTS if_metrics(
  ts INTEGER, device TEXT, iface TEXT, rx_bps REAL, tx_bps REAL, errs INTEGER);
CREATE INDEX IF NOT EXISTS if_metrics_k ON if_metrics(device, iface, ts);
CREATE TABLE IF NOT EXISTS devices_state(
  name TEXT PRIMARY KEY, model TEXT, version TEXT, board TEXT, identity TEXT,
  last_ok INTEGER, last_err TEXT, info TEXT);
CREATE TABLE IF NOT EXISTS alerts(
  id INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER, kind TEXT, subject TEXT,
  severity TEXT, msg TEXT, acked INTEGER DEFAULT 0);
CREATE INDEX IF NOT EXISTS alerts_ts ON alerts(ts);
CREATE TABLE IF NOT EXISTS neighbors(
  device TEXT, iface TEXT, mac TEXT, ident TEXT, addr TEXT, platform TEXT, seen INTEGER,
  PRIMARY KEY(device, iface, mac));
CREATE INDEX IF NOT EXISTS rollup_h_svc ON rollup_1h(svc, ts);
-- devices added via the UI; credentials are encrypted (internal/secret)
CREATE TABLE IF NOT EXISTS devices(
  name TEXT PRIMARY KEY, addr TEXT, port INTEGER, scheme TEXT, role TEXT, site TEXT,
  user TEXT, pass_enc BLOB, fingerprint TEXT, insecure INTEGER DEFAULT 0, flow_src TEXT,
  caps TEXT, managed INTEGER DEFAULT 0, created INTEGER);
-- every change mtmon made on a router; used for rollback / offboarding
CREATE TABLE IF NOT EXISTS manifest(
  id INTEGER PRIMARY KEY AUTOINCREMENT, device TEXT, seq INTEGER, ts INTEGER,
  kind TEXT, descr TEXT, path TEXT, rid TEXT, undo TEXT, state TEXT);
CREATE INDEX IF NOT EXISTS manifest_dev ON manifest(device, seq);
-- firewall log events received via syslog
CREATE TABLE IF NOT EXISTS fw_events(
  ts INTEGER, device TEXT, prefix TEXT, chain TEXT, in_if TEXT, out_if TEXT, proto TEXT, flags TEXT,
  src TEXT, sport INTEGER, dst TEXT, dport INTEGER, nat TEXT, len INTEGER, mac TEXT, cstate TEXT);
CREATE INDEX IF NOT EXISTS fw_events_ts ON fw_events(ts);
CREATE INDEX IF NOT EXISTS fw_events_src ON fw_events(src, ts);
CREATE INDEX IF NOT EXISTS fw_events_dst ON fw_events(dst, ts);
CREATE TABLE IF NOT EXISTS fw_rules(
  device TEXT, prefix TEXT, chain TEXT, action TEXT, descr TEXT, managed INTEGER DEFAULT 1,
  PRIMARY KEY(device, prefix));
CREATE TABLE IF NOT EXISTS fw_hourly(
  ts INTEGER, device TEXT, prefix TEXT, n INTEGER, PRIMARY KEY(ts, device, prefix)) WITHOUT ROWID;
-- user-defined service classification
CREATE TABLE IF NOT EXISTS service_rules(
  id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, category TEXT, kind TEXT, value TEXT,
  proto INTEGER DEFAULT 0, created INTEGER);
CREATE TABLE IF NOT EXISTS seen_dest(
  mac TEXT, rip TEXT, first INTEGER, PRIMARY KEY(mac, rip)) WITHOUT ROWID;
`

type Store struct {
	DB *sql.DB

	mu        sync.Mutex
	flowQ     []FlowRow
	Dropped   uint64
	fwQ       []FwEvent
	FwDropped uint64
}

// Open opens (and migrates) the DB in dir.
func Open(dir string) (*Store, error) {
	dsn := "file:" + filepath.Join(dir, "mtmon.db") +
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=cache_size(-49152)&_pragma=temp_store(2)&_pragma=mmap_size(268435456)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO meta(k,v) VALUES('schema','1')`); err != nil {
		return nil, err
	}
	if err := migrateAdvice(db); err != nil {
		return nil, fmt.Errorf("advice schema: %w", err)
	}
	return &Store{DB: db}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

// Maintain runs retention cleanup + periodic backup until ctx is done.
func (s *Store) Maintain(ctx context.Context, rawDays, days int, backupDir string) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	lastBackup := time.Time{}
	for {
		s.Cleanup(rawDays, days)
		if backupDir != "" && time.Since(lastBackup) > 23*time.Hour {
			if err := s.Backup(backupDir, 7); err == nil {
				lastBackup = time.Now()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// MinuteRollupDays is how long 1-minute rollups are kept (hourly rollups follow `days`).
const MinuteRollupDays = 3

// RawRowCap bounds the raw flow table regardless of retention (protects the disk on busy networks).
const RawRowCap = 15_000_000

func (s *Store) Cleanup(rawDays, days int) {
	now := time.Now()
	rawCut := now.AddDate(0, 0, -rawDays).Unix()
	cut := now.AddDate(0, 0, -days).Unix()
	minCut := now.AddDate(0, 0, -MinuteRollupDays).Unix()
	if minCut < cut {
		minCut = cut
	}
	s.DB.Exec(`DELETE FROM flows WHERE ts < ?`, rawCut)
	var span int64
	if s.DB.QueryRow(`SELECT coalesce(max(rowid)-min(rowid),0) FROM flows`).Scan(&span); span > RawRowCap {
		s.DB.Exec(`DELETE FROM flows WHERE rowid <= (SELECT max(rowid) FROM flows) - ?`, RawRowCap)
	}
	s.cleanupFw(rawCut, cut)
	s.DB.Exec(`DELETE FROM rollup_1m WHERE ts < ?`, minCut)
	s.DB.Exec(`DELETE FROM rollup_1h WHERE ts < ?`, cut)
	s.DB.Exec(`DELETE FROM rollup_1h_client WHERE ts < ?`, cut)
	s.DB.Exec(`DELETE FROM rollup_1d WHERE ts < ?`, cut)
	s.DB.Exec(`DELETE FROM dev_metrics WHERE ts < ?`, cut)
	s.DB.Exec(`DELETE FROM if_metrics WHERE ts < ?`, cut)
	s.DB.Exec(`DELETE FROM alerts WHERE ts < ?`, cut)
	s.DB.Exec(`DELETE FROM roam WHERE ts < ?`, cut)
	s.DB.Exec(`DELETE FROM ip_history WHERE last < ?`, cut)
	s.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
}

// Backup writes a consistent copy via VACUUM INTO and keeps `keep` generations.
func (s *Store) Backup(dir string, keep int) error {
	name := filepath.Join(dir, "mtmon-"+time.Now().Format("20060102-150405")+".db")
	if _, err := s.DB.Exec(`VACUUM INTO ?`, name); err != nil {
		return err
	}
	return pruneBackups(dir, keep)
}
