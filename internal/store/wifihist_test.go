package store

import (
	"testing"
	"time"
)

func TestWifiHistorySeriesEventsAndStats(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const mac = "AA:BB:CC:00:11:22"
	base := time.Now().Unix()/wifiBucket*wifiBucket - 6*wifiBucket
	put := func(bucket int, dev, band string, sig int, tx string) {
		// UpsertClients stamps "now"; write the bucket directly through the same helper
		tx2, _ := st.DB.Begin()
		addWifiSample(tx2, base+int64(bucket)*wifiBucket, Observation{MAC: mac, WiFi: true, Present: true, Device: dev, Band: band, Signal: sig, TxRate: tx, RxRate: "100Mbps"})
		tx2.Commit()
	}
	put(0, "ap1", "5ghz-ax", -60, "866.7Mbps-80MHz/2S/SGI")
	put(1, "ap1", "5ghz-ax", -62, "866.7Mbps-80MHz/2S/SGI")
	put(2, "ap1", "2ghz-ax", -78, "72Mbps")
	put(2, "ap1", "2ghz-ax", -80, "72Mbps")
	put(3, "ap2", "2ghz-ax", -70, "72Mbps")
	put(4, "ap2", "2ghz-ax", -76, "72Mbps")
	pts, err := st.WifiHistory(mac, base, 300)
	if err != nil || len(pts) != 5 || pts[2].Avg != -79 || pts[2].Min != -80 || pts[0].Tx < 866 || pts[0].Device != "ap1" {
		t.Fatalf("series: %+v %v", pts, err)
	}
	ev, _ := st.WifiEvents(mac, base)
	if len(ev) != 2 || ev[0].Kind != "band" || ev[0].To != "2ghz-ax" || ev[1].Kind != "roam" || ev[1].To != "ap2 2ghz-ax" {
		t.Fatalf("events: %+v", ev)
	}
	stat, _ := st.WifiSignalStats(base)
	if s := stat[mac]; s.Samples != 6 || s.WeakFrac < 0.49 || s.WeakFrac > 0.51 { // -78, -80, -76 are weak
		t.Fatalf("stats: %+v", s)
	}
	bands, _ := st.WifiBands("ap1", base, 600)
	if len(bands) == 0 || bands[0].Clients != 1 {
		t.Fatalf("bands: %+v", bands)
	}
	// readings without a signal or of offline clients are not history
	tx3, _ := st.DB.Begin()
	addWifiSample(tx3, base, Observation{MAC: "X", WiFi: true, Present: true, Signal: 0})
	addWifiSample(tx3, base, Observation{MAC: "Y", WiFi: true, Present: false, Signal: -50})
	tx3.Commit()
	if s, _ := st.WifiSignalStats(base); len(s) != 1 {
		t.Fatalf("only real readings count: %+v", s)
	}
	if rateMbps("866.7Mbps-80MHz/2S/SGI") != 866.7 || rateMbps("") != 0 || rateMbps("garbage") != 0 {
		t.Error("rate parsing")
	}
}
