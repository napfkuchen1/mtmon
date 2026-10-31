// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"fmt"
	"testing"
	"time"
)

func TestScoreWifi(t *testing.T) {
	if s, _, _, _, _ := ScoreWifi(-55, 0, 0, 300); s != 100 {
		t.Errorf("good link: %d", s)
	}
	s, sig, weak, roams, rate := ScoreWifi(-80, 1, 6, 10)
	if sig != 40 || weak != 20 || roams != 20 || rate != 16 || s != 4 {
		t.Errorf("bad link: score %d sig %d weak %d roams %d rate %d", s, sig, weak, roams, rate)
	}
	if s, _, _, _, _ := ScoreWifi(-200, 1, 100, 1); s < 0 {
		t.Errorf("negative score %d", s)
	}
}

func TestWifiHealthSince(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().Unix()
	ts := now / wifiBucket * wifiBucket
	st.DB.Exec(`INSERT INTO wifi_hist(ts,mac,device,band,n,sig_sum,sig_min,sig_max,weak,tx_sum,rx_sum) VALUES(?,?,?,?,4,-320,-80,-80,4,40,40)`, ts, "BAD", "ap1", "5ghz")
	st.DB.Exec(`INSERT INTO wifi_hist(ts,mac,device,band,n,sig_sum,sig_min,sig_max,weak,tx_sum,rx_sum) VALUES(?,?,?,?,4,-220,-55,-55,0,1200,1200)`, ts, "GOOD", "ap1", "5ghz")
	st.DB.Exec(`INSERT INTO wifi_hist(ts,mac,device,band,n,sig_sum,sig_min,sig_max,weak,tx_sum,rx_sum) VALUES(?,?,?,?,1,-60,-60,-60,0,1,1)`, ts, "NEW", "ap1", "5ghz")
	h, err := st.WifiHealthSince(now-86400, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 2 || h[0].MAC != "BAD" || !h[0].Poor || h[1].MAC != "GOOD" || h[1].Poor || h[1].Score != 100 {
		t.Errorf("%+v", h)
	}
}

func TestRateMbpsUnits(t *testing.T) {
	for in, want := range map[string]float64{"866.7Mbps-80MHz/2S/SGI": 866.7, "20910513": 20.910513, "300": 300, "1Gbps": 1000, "512kbps": 0.512, "": 0} {
		if got := rateMbps(in); got < want-1e-9 || got > want+1e-9 {
			t.Errorf("rateMbps(%q)=%v want %v", in, got, want)
		}
	}
}

func TestScoreWifiInParts(t *testing.T) {
	base := WifiInput{AvgDBm: -55, AvgMbps: 20, Transfers: true}
	if s, _, _, _, rate, _ := ScoreWifiIn(base); rate <= 0 || s >= 100 {
		t.Errorf("20 Mbit/s with traffic: rate penalty %d score %d", rate, s)
	}
	idle := base
	idle.Transfers = false
	if _, _, _, _, rate, _ := ScoreWifiIn(idle); rate != 0 {
		t.Errorf("idle client got rate penalty %d", rate)
	}
	d := WifiInput{AvgDBm: -55, DiscPerHour: 3}
	if _, _, _, _, _, disc := ScoreWifiIn(d); disc <= 0 {
		t.Errorf("3 disconnects/h: penalty %d", disc)
	}
	d.DiscPerHour = 20
	if _, _, _, _, _, disc := ScoreWifiIn(d); disc != 20 {
		t.Errorf("cap: %d", disc)
	}
	r := WifiInput{AvgDBm: -55, RoamsPerHour: 8}
	_, _, _, roams, _, _ := ScoreWifiIn(r)
	r.Pinned = true
	_, _, _, pinnedRoams, _, _ := ScoreWifiIn(r)
	if roams == 0 || pinnedRoams != 0 {
		t.Errorf("roams %d pinned %d", roams, pinnedRoams)
	}
}

// 22 real-like clients: signal -46..-78 dBm, rates 20..500 Mbit/s; the weak ESP stays worst, slow clients pay for rate.
func TestWifiHealthFixture22(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().Unix()
	ts := now / wifiBucket * wifiBucket
	for i := 0; i < 22; i++ {
		mac := fmt.Sprintf("AA:00:00:00:00:%02X", i)
		dbm := -46 - i*32/21 // -46 .. -78
		rate := 500 - i*480/21
		weak := 0
		if dbm <= -75 {
			weak = 4
		}
		st.DB.Exec(`INSERT INTO wifi_hist(ts,mac,device,band,n,sig_sum,sig_min,sig_max,weak,tx_sum,rx_sum) VALUES(?,?,?,?,4,?,?,?,?,?,?)`,
			ts, mac, "ap1", "5ghz", dbm*4, dbm, dbm, weak, float64(rate*4), float64(rate*4))
		st.DB.Exec(`INSERT INTO rollup_1h_client(ts,mac,b_up,b_down,b_int,flows) VALUES(?,?,?,?,0,1)`, ts/3600*3600, mac, 1e6, 5e6)
	}
	st.AddWifiDisc(now, "AA:00:00:00:00:15", "ap1")
	h, err := st.WifiHealthSince(now-86400, now, nil)
	if err != nil || len(h) != 22 {
		t.Fatalf("%v %d", err, len(h))
	}
	if h[0].MAC != "AA:00:00:00:00:15" && h[0].MAC != "AA:00:00:00:00:14" {
		t.Errorf("worst is %s", h[0].MAC)
	}
	last := h[21]
	if last.Score != 100 || last.Rate != 0 {
		t.Errorf("best: %+v", last)
	}
	if h[0].Rate == 0 || h[0].AvgRateBps > 30e6 {
		t.Errorf("slow client: %+v", h[0])
	}
	for i := 1; i < len(h); i++ {
		if h[i].Score < h[i-1].Score {
			t.Errorf("not sorted at %d", i)
		}
	}
}

func TestWifiRateUnitMigration(t *testing.T) {
	dir := t.TempDir()
	st, _ := Open(dir)
	st.DB.Exec(`INSERT INTO wifi_hist(ts,mac,device,band,n,sig_sum,sig_min,sig_max,weak,tx_sum,rx_sum) VALUES(1,'M','d','5ghz',2,-100,-50,-50,0,41821026,41821026)`)
	st.Close()
	st, _ = Open(dir)
	defer st.Close()
	var tx float64
	st.DB.QueryRow(`SELECT tx_sum FROM wifi_hist WHERE mac='M'`).Scan(&tx)
	if tx < 41 || tx > 42 {
		t.Errorf("tx_sum %v", tx)
	}
}
