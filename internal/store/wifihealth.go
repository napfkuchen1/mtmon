package store

import (
	"math"
	"sort"
)

// WifiHealth is the 0-100 connection score of one Wi-Fi client over a window, with what cost the points.
// Every part is a plain penalty, nothing is hidden: score = 100 - Signal - Weak - Roams - Rate.
type WifiHealth struct {
	MAC     string  `json:"mac"`
	Score   int     `json:"score"`
	Poor    bool    `json:"poor"`
	Signal  int     `json:"signal_penalty"` // average signal: 0 at -60 dBm or better, 40 at -80 dBm or worse
	Weak    int     `json:"weak_penalty"`   // share of readings below WifiWeakDBm, up to 20
	Roams   int     `json:"roam_penalty"`   // more than 2 roams per hour, up to 20
	Rate    int     `json:"rate_penalty"`   // average link rate under 50 Mbps, up to 20
	AvgDBm  float64 `json:"avg_dbm"`
	RoamsH  float64 `json:"roams_per_hour"`
	AvgMbps float64 `json:"avg_mbps"`
}

// WifiPoorBelow: a score under this counts as a poor connection.
const WifiPoorBelow = 60

// ScoreWifi turns the raw numbers into a score (pure, so the thresholds are testable and documented in one place).
func ScoreWifi(avgDBm, weakFrac, roamsPerHour, avgMbps float64) (score, sig, weak, roams, rate int) {
	sig = int(math.Round(clampF((-60-avgDBm)/20*40, 0, 40)))
	weak = int(math.Round(clampF(weakFrac*20, 0, 20)))
	roams = int(math.Round(clampF((roamsPerHour-2)*5, 0, 20)))
	if avgMbps > 0 {
		rate = int(math.Round(clampF((50-avgMbps)/50*20, 0, 20)))
	}
	score = 100 - sig - weak - roams - rate
	if score < 0 {
		score = 0
	}
	return
}

func clampF(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// WifiHealthSince scores every Wi-Fi client with readings since `since`, worst first. Needs at least 3 readings
// (15 minutes) so a client that just connected is not judged.
func (s *Store) WifiHealthSince(since, now int64) ([]WifiHealth, error) {
	rows, err := s.DB.Query(`SELECT mac, sum(n), sum(weak), sum(sig_sum), sum(tx_sum), sum(rx_sum) FROM wifi_hist WHERE ts>=? GROUP BY mac HAVING sum(n)>=3`, since/wifiBucket*wifiBucket)
	if err != nil {
		return nil, err
	}
	type raw struct {
		n, weak, sig int64
		tx, rx       float64
	}
	byMAC := map[string]raw{}
	for rows.Next() {
		var mac string
		var r raw
		if err := rows.Scan(&mac, &r.n, &r.weak, &r.sig, &r.tx, &r.rx); err != nil {
			rows.Close()
			return nil, err
		}
		byMAC[mac] = r
	}
	rows.Close()
	roamN := map[string]int{}
	rr, err := s.DB.Query(`SELECT mac, count(*) FROM roam WHERE ts>=? GROUP BY mac`, since)
	if err == nil {
		for rr.Next() {
			var mac string
			var n int
			rr.Scan(&mac, &n)
			roamN[mac] = n
		}
		rr.Close()
	}
	hours := float64(now-since) / 3600
	if hours < 1 {
		hours = 1
	}
	out := []WifiHealth{}
	for mac, r := range byMAC {
		h := WifiHealth{MAC: mac, AvgDBm: float64(r.sig) / float64(r.n), RoamsH: float64(roamN[mac]) / hours}
		h.AvgMbps = (r.tx + r.rx) / 2 / float64(r.n)
		h.Score, h.Signal, h.Weak, h.Roams, h.Rate = ScoreWifi(h.AvgDBm, float64(r.weak)/float64(r.n), h.RoamsH, h.AvgMbps)
		h.Poor = h.Score < WifiPoorBelow
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score < out[j].Score
		}
		return out[i].MAC < out[j].MAC
	})
	return out, nil
}
