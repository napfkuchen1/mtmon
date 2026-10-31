// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"database/sql"
	"regexp"
	"sort"
	"strconv"
)

// WifiWeakDBm: below this a link counts as weak (the Suggestions page uses the same limit).
const WifiWeakDBm = -75

// wifiBucket is the width of one history row; wifi_hist keeps min/avg/max signal per client, AP and band.
const wifiBucket = 300

var reRate = regexp.MustCompile(`^\s*([0-9]+(?:\.[0-9]+)?)\s*(Gbps|Mbps|kbps|bps)?`)

// rateMbps reads a RouterOS rate string and returns Mbit/s (0 when it cannot be read). Older versions say
// "866.7Mbps-80MHz/2S/SGI"; the wifi package of 7.x can report a bare number in bit/s ("20910513"). A bare number
// of 10000 or more can only be bit/s, a smaller one is taken as Mbit/s.
func rateMbps(s string) float64 {
	m := reRate.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	v, _ := strconv.ParseFloat(m[1], 64)
	switch m[2] {
	case "Gbps":
		return v * 1000
	case "kbps":
		return v / 1000
	case "bps":
		return v / 1e6
	case "":
		if v >= 10000 {
			return v / 1e6
		}
	}
	return v
}

// addWifiSample adds one reading of a connected Wi-Fi client to its 5-minute row.
func addWifiSample(tx *sql.Tx, now int64, o Observation) {
	if !o.WiFi || !o.Present || o.Signal == 0 || o.MAC == "" {
		return
	}
	weak := 0
	if o.Signal < WifiWeakDBm {
		weak = 1
	}
	tx.Exec(`INSERT INTO wifi_hist(ts,mac,device,band,n,sig_sum,sig_min,sig_max,weak,tx_sum,rx_sum) VALUES(?,?,?,?,1,?,?,?,?,?,?)
		ON CONFLICT DO UPDATE SET n=n+1, sig_sum=sig_sum+excluded.sig_sum, sig_min=min(sig_min,excluded.sig_min),
		sig_max=max(sig_max,excluded.sig_max), weak=weak+excluded.weak, tx_sum=tx_sum+excluded.tx_sum, rx_sum=rx_sum+excluded.rx_sum`,
		now/wifiBucket*wifiBucket, o.MAC, o.Device, o.Band, o.Signal, o.Signal, o.Signal, weak, rateMbps(o.TxRate), rateMbps(o.RxRate))
}

// WifiPoint is one point of a client's signal history.
type WifiPoint struct {
	TS     int64   `json:"ts"`
	Avg    float64 `json:"avg"`
	Min    int     `json:"min"`
	Max    int     `json:"max"`
	Tx     float64 `json:"tx_mbps"`
	Rx     float64 `json:"rx_mbps"`
	Device string  `json:"device"`
	Band   string  `json:"band"`
}

// WifiHistory returns the client's rows since `since`, oldest first, merged to `bucket` seconds (≥ 300).
func (s *Store) WifiHistory(mac string, since, bucket int64) ([]WifiPoint, error) {
	if bucket < wifiBucket {
		bucket = wifiBucket
	}
	rows, err := s.DB.Query(`SELECT ts/?1*?1, device, band, sum(n), sum(sig_sum), min(sig_min), max(sig_max), sum(tx_sum), sum(rx_sum)
		FROM wifi_hist WHERE mac=?2 AND ts>=?3 GROUP BY 1, device, band ORDER BY 1`, bucket, NormMAC(mac), since/wifiBucket*wifiBucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	best := map[int64]*WifiPoint{}
	count := map[int64]int64{}
	var order []int64
	for rows.Next() {
		var p WifiPoint
		var n, sum int64
		var tx, rx float64
		if err := rows.Scan(&p.TS, &p.Device, &p.Band, &n, &sum, &p.Min, &p.Max, &tx, &rx); err != nil {
			return nil, err
		}
		p.Avg = float64(sum) / float64(n)
		p.Tx, p.Rx = tx/float64(n), rx/float64(n)
		if cur, ok := best[p.TS]; !ok {
			best[p.TS], count[p.TS] = &p, n
			order = append(order, p.TS)
		} else if n > count[p.TS] { // the AP/band the client spent most of the bucket on
			*cur, count[p.TS] = p, n
		}
	}
	out := make([]WifiPoint, 0, len(order))
	for _, ts := range order {
		out = append(out, *best[ts])
	}
	return out, rows.Err()
}

// WifiEvent is a change of AP or band of one client.
type WifiEvent struct {
	TS   int64  `json:"ts"`
	Kind string `json:"kind"` // roam (other AP) | band (other band on the same AP)
	From string `json:"from"`
	To   string `json:"to"`
}

// WifiEvents derives the AP and band changes of a client from its history rows.
func (s *Store) WifiEvents(mac string, since int64) ([]WifiEvent, error) {
	pts, err := s.WifiHistory(mac, since, wifiBucket)
	if err != nil {
		return nil, err
	}
	out := []WifiEvent{}
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		switch {
		case a.Device != b.Device:
			out = append(out, WifiEvent{TS: b.TS, Kind: "roam", From: a.Device + " " + a.Band, To: b.Device + " " + b.Band})
		case a.Band != b.Band:
			out = append(out, WifiEvent{TS: b.TS, Kind: "band", From: a.Band, To: b.Band})
		}
	}
	return out, nil
}

// WifiSeen5 returns the clients that were ever seen on a 5 GHz (or 6 GHz) band: they demonstrably support it.
func (s *Store) WifiSeen5() (map[string]bool, error) {
	rows, err := s.DB.Query(`SELECT DISTINCT mac FROM wifi_hist WHERE band LIKE '5%' OR band LIKE '6%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out[m] = true
	}
	return out, rows.Err()
}

// WifiSignalStat summarises the signal of one client over a window.
type WifiSignalStat struct {
	Samples  int64
	WeakFrac float64 // share of readings below WifiWeakDBm
	Avg      float64
}

// WifiSignalStats returns the stats of every client with readings since `since`.
func (s *Store) WifiSignalStats(since int64) (map[string]WifiSignalStat, error) {
	rows, err := s.DB.Query(`SELECT mac, sum(n), sum(weak), sum(sig_sum) FROM wifi_hist WHERE ts>=? GROUP BY mac`, since/wifiBucket*wifiBucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]WifiSignalStat{}
	for rows.Next() {
		var mac string
		var n, weak, sum int64
		if err := rows.Scan(&mac, &n, &weak, &sum); err != nil {
			return nil, err
		}
		if n > 0 {
			out[mac] = WifiSignalStat{Samples: n, WeakFrac: float64(weak) / float64(n), Avg: float64(sum) / float64(n)}
		}
	}
	return out, rows.Err()
}

// APSignal is the mean signal of one client on one access point over a window.
type APSignal struct {
	Device string
	Avg    float64
	N      int64
}

// WifiAPSignals returns per client the mean signal on each access point since `since` (strongest first).
func (s *Store) WifiAPSignals(since int64, macs []string) (map[string][]APSignal, error) {
	out := map[string][]APSignal{}
	for _, mac := range macs {
		rows, err := s.DB.Query(`SELECT device, sum(n), sum(sig_sum) FROM wifi_hist WHERE mac=? AND ts>=? GROUP BY device ORDER BY sum(sig_sum)*1.0/sum(n) DESC`, mac, since/wifiBucket*wifiBucket)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var d string
			var n, sum int64
			if err := rows.Scan(&d, &n, &sum); err != nil {
				rows.Close()
				return nil, err
			}
			if n > 0 {
				out[mac] = append(out[mac], APSignal{Device: d, Avg: float64(sum) / float64(n), N: n})
			}
		}
		rows.Close()
	}
	return out, nil
}

// BandPoint counts the clients of one AP on one band in one bucket.
type BandPoint struct {
	TS      int64  `json:"ts"`
	Band    string `json:"band"`
	Clients int    `json:"clients"`
}

// WifiBands returns how many distinct clients an AP served per band and bucket since `since`.
func (s *Store) WifiBands(device string, since, bucket int64) ([]BandPoint, error) {
	if bucket < wifiBucket {
		bucket = wifiBucket
	}
	rows, err := s.DB.Query(`SELECT ts/?1*?1, band, count(DISTINCT mac) FROM wifi_hist WHERE device=?2 AND ts>=?3 GROUP BY 1, band ORDER BY 1, band`,
		bucket, device, since/wifiBucket*wifiBucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BandPoint{}
	for rows.Next() {
		var p BandPoint
		if err := rows.Scan(&p.TS, &p.Band, &p.Clients); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out, rows.Err()
}
