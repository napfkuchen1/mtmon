package store

import (
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
	h, err := st.WifiHealthSince(now-86400, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 2 || h[0].MAC != "BAD" || !h[0].Poor || h[1].MAC != "GOOD" || h[1].Poor || h[1].Score != 100 {
		t.Errorf("%+v", h)
	}
}
