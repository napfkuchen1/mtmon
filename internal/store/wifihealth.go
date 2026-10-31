// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"math"
	"sort"
)

// WifiHealth is the 0-100 connection score of one Wi-Fi client over a window, with what cost the points.
// Every part is a plain penalty, nothing is hidden: score = 100 - Signal - Weak - Roams - Rate - Disc.
type WifiHealth struct {
	MAC        string  `json:"mac"`
	Name       string  `json:"name"`
	AP         string  `json:"ap"`
	Band       string  `json:"band"`
	SSID       string  `json:"ssid"`
	Pinned     bool    `json:"pinned"` // an access-list rule names the client: roams are a decision, not a fault
	Score      int     `json:"score"`
	Poor       bool    `json:"poor"`
	Signal     int     `json:"signal_penalty"` // average signal: 0 at -60 dBm or better, 40 at -80 dBm or worse
	Weak       int     `json:"weak_penalty"`   // share of readings below WifiWeakDBm, up to 20
	Roams      int     `json:"roam_penalty"`   // more than 2 roams per hour, up to 20 (none for pinned clients)
	Rate       int     `json:"rate_penalty"`   // average link rate under 50 Mbit/s while the client moves data, up to 20
	Disc       int     `json:"disc_penalty"`   // 3 or more disconnects per hour, up to 20
	AvgDBm     float64 `json:"avg_dbm"`
	RoamsH     float64 `json:"roams_per_hour"`
	DiscH      float64 `json:"disconnects_per_hour"`
	AvgRateBps float64 `json:"avg_rate_bps"` // link rate in bit/s (average of TX and RX)
	Bytes      int64   `json:"bytes"`        // transferred in the window; the rate penalty needs more than WifiRateMinBytes
}

// WifiRateMinBytes: below this the link rate says nothing (an idle ESP negotiates 1 Mbit/s by design).
const WifiRateMinBytes = 100 * 1000

// WifiPoorBelow: a score under this counts as a poor connection.
const WifiPoorBelow = 60

// WifiInput is everything the score needs; AvgMbps is in Mbit/s.
type WifiInput struct {
	AvgDBm, WeakFrac, RoamsPerHour, AvgMbps, DiscPerHour float64
	Pinned, Transfers                                    bool
}

// ScoreWifiIn turns the raw numbers into a score (pure, so the thresholds are testable and documented in one place).
func ScoreWifiIn(in WifiInput) (score, sig, weak, roams, rate, disc int) {
	sig = int(math.Round(clampF((-60-in.AvgDBm)/20*40, 0, 40)))
	weak = int(math.Round(clampF(in.WeakFrac*20, 0, 20)))
	if !in.Pinned {
		roams = int(math.Round(clampF((in.RoamsPerHour-2)*5, 0, 20)))
	}
	if in.AvgMbps > 0 && in.Transfers {
		rate = int(math.Round(clampF((50-in.AvgMbps)/50*20, 0, 20)))
	}
	disc = int(math.Round(clampF((in.DiscPerHour-2)*5, 0, 20)))
	score = 100 - sig - weak - roams - rate - disc
	if score < 0 {
		score = 0
	}
	return
}

// ScoreWifi is the score without disconnects, pinning and the transfer gate (rate counts whenever it is known).
func ScoreWifi(avgDBm, weakFrac, roamsPerHour, avgMbps float64) (score, sig, weak, roams, rate int) {
	score, sig, weak, roams, rate, _ = ScoreWifiIn(WifiInput{AvgDBm: avgDBm, WeakFrac: weakFrac, RoamsPerHour: roamsPerHour, AvgMbps: avgMbps, Transfers: true})
	return
}

func clampF(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// WifiHealthSince scores every Wi-Fi client with readings since `since`, worst first. Needs at least 3 readings
// (15 minutes) so a client that just connected is not judged.
func (s *Store) WifiHealthSince(since, now int64, pinned map[string]bool) ([]WifiHealth, error) {
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
	discN := s.wifiDiscPeak(since)
	bytesBy := map[string]int64{}
	if ct, err := s.ClientTotals(since); err == nil {
		for _, c := range ct {
			bytesBy[c.MAC] = c.Up + c.Down
		}
	}
	clients := map[string]Client{}
	if cl, err := s.Clients(); err == nil {
		for _, c := range cl {
			clients[c.MAC] = c
		}
	}
	hours := float64(now-since) / 3600
	if hours < 1 {
		hours = 1
	}
	out := []WifiHealth{}
	for mac, r := range byMAC {
		h := WifiHealth{MAC: mac, AvgDBm: float64(r.sig) / float64(r.n), RoamsH: float64(roamN[mac]) / hours, DiscH: float64(discN[mac]),
			Pinned: pinned[mac], Bytes: bytesBy[mac]}
		if c, ok := clients[mac]; ok {
			h.Name, h.AP, h.Band, h.SSID = firstNonEmptyS(c.Label, c.Hostname), c.Device, c.Band, c.SSID
		}
		mbps := (r.tx + r.rx) / 2 / float64(r.n)
		h.AvgRateBps = mbps * 1e6
		h.Score, h.Signal, h.Weak, h.Roams, h.Rate, h.Disc = ScoreWifiIn(WifiInput{AvgDBm: h.AvgDBm, WeakFrac: float64(r.weak) / float64(r.n),
			RoamsPerHour: h.RoamsH, AvgMbps: mbps, DiscPerHour: h.DiscH, Pinned: h.Pinned, Transfers: h.Bytes > WifiRateMinBytes})
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

func firstNonEmptyS(v ...string) string {
	for _, x := range v {
		if x != "" {
			return x
		}
	}
	return ""
}

// WifiScorePoint is the score of one hour (for the 24 h sparkline on the client page).
type WifiScorePoint struct {
	TS    int64 `json:"ts"`
	Score int   `json:"score"`
}

// WifiScoreSeries scores one client hour by hour. Hours with fewer than 3 readings are left out; the transfer gate
// uses the whole window (hourly byte counts per client are not kept at that resolution).
func (s *Store) WifiScoreSeries(mac string, since, now int64, pinned, transfers bool) []WifiScorePoint {
	out := []WifiScorePoint{}
	rows, err := s.DB.Query(`SELECT ts/3600*3600, sum(n), sum(weak), sum(sig_sum), sum(tx_sum), sum(rx_sum) FROM wifi_hist
		WHERE mac=? AND ts>=? GROUP BY ts/3600 HAVING sum(n)>=3 ORDER BY 1`, mac, since/wifiBucket*wifiBucket)
	if err != nil {
		return out
	}
	type hr struct {
		ts, n, weak, sig int64
		tx, rx           float64
	}
	var hs []hr
	for rows.Next() {
		var h hr
		rows.Scan(&h.ts, &h.n, &h.weak, &h.sig, &h.tx, &h.rx)
		hs = append(hs, h)
	}
	rows.Close()
	count := func(q string) map[int64]int {
		m := map[int64]int{}
		r, err := s.DB.Query(q, mac, since)
		if err != nil {
			return m
		}
		defer r.Close()
		for r.Next() {
			var t int64
			var n int
			r.Scan(&t, &n)
			m[t] = n
		}
		return m
	}
	roams := count(`SELECT ts/3600*3600, count(*) FROM roam WHERE mac=? AND ts>=? GROUP BY 1`)
	discs := count(`SELECT ts/3600*3600, count(*) FROM wifi_disc WHERE mac=? AND ts>=? GROUP BY 1`)
	for _, h := range hs {
		sc, _, _, _, _, _ := ScoreWifiIn(WifiInput{AvgDBm: float64(h.sig) / float64(h.n), WeakFrac: float64(h.weak) / float64(h.n),
			RoamsPerHour: float64(roams[h.ts]), AvgMbps: (h.tx + h.rx) / 2 / float64(h.n), DiscPerHour: float64(discs[h.ts]),
			Pinned: pinned, Transfers: transfers})
		out = append(out, WifiScorePoint{TS: h.ts, Score: sc})
	}
	return out
}
