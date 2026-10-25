package store

import (
	"strings"
	"sync"
	"time"
)

// LogRetentionDays is how long the router event log (system, account, dhcp, wifi, … lines) is kept.
const LogRetentionDays = 7

// LogEvent is one stored router log line.
type LogEvent struct {
	TS     int64  `json:"ts"`
	Device string `json:"device"`
	Topics string `json:"topics"`
	Level  string `json:"level"`
	Msg    string `json:"msg"`
}

var logDedup = struct {
	sync.Mutex
	seen map[string]time.Time
}{seen: map[string]time.Time{}}

// AddLogEvent stores one line. A router that matches a message with several logging rules sends it once per rule;
// the same text from the same device within two seconds is stored once. Returns false for such a duplicate.
func (s *Store) AddLogEvent(e LogEvent) bool {
	key := e.Device + "|" + e.Msg
	logDedup.Lock()
	now := time.Now()
	if t, ok := logDedup.seen[key]; ok && now.Sub(t) < 2*time.Second {
		logDedup.Unlock()
		return false
	}
	logDedup.seen[key] = now
	if len(logDedup.seen) > 2000 {
		for k, t := range logDedup.seen {
			if now.Sub(t) > 10*time.Second {
				delete(logDedup.seen, k)
			}
		}
	}
	logDedup.Unlock()
	_, err := s.DB.Exec(`INSERT INTO log_events(ts,device,topics,level,msg) VALUES(?,?,?,?,?)`, e.TS, e.Device, e.Topics, e.Level, e.Msg)
	return err == nil
}

// LogEvents lists stored lines, newest first. q filters by a case-insensitive substring of the message or topics,
// level by a minimum level ("" = all), device "" = all devices.
func (s *Store) LogEvents(device, q, minLevel string, since int64, limit int) ([]LogEvent, error) {
	query := `SELECT ts,device,topics,level,msg FROM log_events WHERE ts>=?`
	args := []any{since}
	if device != "" {
		query += ` AND device=?`
		args = append(args, device)
	}
	if q = strings.TrimSpace(q); q != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(q)) + "%"
		query += ` AND (lower(msg) LIKE ? ESCAPE '\' OR lower(topics) LIKE ? ESCAPE '\')`
		args = append(args, like, like)
	}
	switch minLevel {
	case "warning":
		query += ` AND level IN ('warning','error','critical')`
	case "error":
		query += ` AND level IN ('error','critical')`
	}
	query += ` ORDER BY ts DESC, rowid DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogEvent{}
	for rows.Next() {
		var e LogEvent
		if err := rows.Scan(&e.TS, &e.Device, &e.Topics, &e.Level, &e.Msg); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
