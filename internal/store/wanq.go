package store

import (
	"sort"
	"time"
)

// wan_quality (schema in store.go): one row per minute and probe target (TCP connect probes from mtmon, internal/probe).

// AddWANSample records one probe: ok=false is a loss (rtt ignored).
func (s *Store) AddWANSample(at time.Time, target string, ok bool, rttMS float64) {
	ts := at.Unix() / 60 * 60
	lost, rtt := 1, 0.0
	if ok {
		lost, rtt = 0, rttMS
	}
	s.DB.Exec(`INSERT INTO wan_quality(ts,target,sent,lost,rtt_sum,rtt_max) VALUES(?,?,1,?,?,?)
		ON CONFLICT DO UPDATE SET sent=sent+1, lost=lost+excluded.lost, rtt_sum=rtt_sum+excluded.rtt_sum,
		rtt_max=CASE WHEN excluded.rtt_max>rtt_max THEN excluded.rtt_max ELSE rtt_max END`, ts, target, lost, rtt, rtt)
}

// WANWindow is the quality over a window, for all targets together.
type WANWindow struct {
	Sent    int64   `json:"sent"`
	Lost    int64   `json:"lost"`
	LossPct float64 `json:"loss_pct"`
	AvgMS   float64 `json:"avg_ms"` // of the probes that were answered, 0 = none
	MaxMS   float64 `json:"max_ms"`
}

func (s *Store) WANQualitySince(since int64) WANWindow {
	var w WANWindow
	var sum float64
	s.DB.QueryRow(`SELECT coalesce(sum(sent),0), coalesce(sum(lost),0), coalesce(sum(rtt_sum),0), coalesce(max(rtt_max),0) FROM wan_quality WHERE ts>=?`, since/60*60).
		Scan(&w.Sent, &w.Lost, &sum, &w.MaxMS)
	if w.Sent > 0 {
		w.LossPct = float64(w.Lost) * 100 / float64(w.Sent)
	}
	if ok := w.Sent - w.Lost; ok > 0 {
		w.AvgMS = sum / float64(ok)
	}
	return w
}

// WANTarget is one target's quality over a window.
type WANTarget struct {
	Target  string  `json:"target"`
	LossPct float64 `json:"loss_pct"`
	AvgMS   float64 `json:"avg_ms"`
	Sent    int64   `json:"sent"`
}

func (s *Store) WANTargets(since int64) []WANTarget {
	out := []WANTarget{}
	rows, err := s.DB.Query(`SELECT target, sum(sent), sum(lost), sum(rtt_sum) FROM wan_quality WHERE ts>=? GROUP BY target ORDER BY target`, since/60*60)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var t WANTarget
		var lost int64
		var sum float64
		rows.Scan(&t.Target, &t.Sent, &lost, &sum)
		if t.Sent > 0 {
			t.LossPct = float64(lost) * 100 / float64(t.Sent)
		}
		if ok := t.Sent - lost; ok > 0 {
			t.AvgMS = sum / float64(ok)
		}
		out = append(out, t)
	}
	return out
}

// WANPoint is one bucket of the quality chart.
type WANPoint struct {
	TS      int64   `json:"ts"`
	AvgMS   float64 `json:"avg_ms"`
	LossPct float64 `json:"loss_pct"`
}

// WANSeries returns buckets of `bucket` seconds (multiple of 60) since `since`.
func (s *Store) WANSeries(since, bucket int64) []WANPoint {
	if bucket < 60 {
		bucket = 60
	}
	out := []WANPoint{}
	rows, err := s.DB.Query(`SELECT ts/?1*?1, sum(sent), sum(lost), sum(rtt_sum) FROM wan_quality WHERE ts>=?2 GROUP BY 1 ORDER BY 1`, bucket, since/60*60)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var p WANPoint
		var sent, lost int64
		var sum float64
		rows.Scan(&p.TS, &sent, &lost, &sum)
		if sent > 0 {
			p.LossPct = float64(lost) * 100 / float64(sent)
		}
		if ok := sent - lost; ok > 0 {
			p.AvgMS = sum / float64(ok)
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out
}

// AlertKindStats returns how many alerts of a kind exist since a time and the newest one's time (0 = none).
func (s *Store) AlertKindStats(kind string, since int64) (n int, last int64) {
	s.DB.QueryRow(`SELECT count(*), coalesce(max(ts),0) FROM alerts WHERE kind=? AND ts>=?`, kind, since).Scan(&n, &last)
	if n == 0 {
		s.DB.QueryRow(`SELECT coalesce(max(ts),0) FROM alerts WHERE kind=?`, kind).Scan(&last)
	}
	return
}
