package store

// KindNoise is how a kind of warning was handled in a period: a warning that people only acknowledge, over and
// over, is probably noise. Alerts that resolve themselves become info and do not count.
type KindNoise struct {
	Kind     string  `json:"kind"`
	Warnings int     `json:"warnings"` // warnings and criticals raised in the period
	Acked    int     `json:"acked"`    // of those, acknowledged by hand
	Share    float64 `json:"share"`    // Acked / Warnings
	Noisy    bool    `json:"noisy"`
}

// noisyMin and noisyShare: a kind is listed as noisy from this many warnings with at least this share acknowledged.
const (
	noisyMin   = 5
	noisyShare = 0.8
)

// AlertNoise returns the warning counts per alert kind since the given Unix time, the most frequent kind first.
func (s *Store) AlertNoise(since int64) []KindNoise {
	rows, err := s.DB.Query(`SELECT kind, count(*), sum(acked) FROM alerts
		WHERE ts>=? AND severity IN ('warning','critical') GROUP BY kind ORDER BY count(*) DESC, kind`, since)
	if err != nil {
		return []KindNoise{}
	}
	defer rows.Close()
	out := []KindNoise{}
	for rows.Next() {
		var k KindNoise
		rows.Scan(&k.Kind, &k.Warnings, &k.Acked)
		k.Share = float64(k.Acked) / float64(k.Warnings)
		k.Noisy = k.Warnings >= noisyMin && k.Share >= noisyShare
		out = append(out, k)
	}
	return out
}
