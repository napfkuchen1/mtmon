package advice

import "testing"

// A group can raise the "many connections" limit (IoT 3x, servers 10x) and silence the weak-signal tip.
func TestGroupSettingsChangeSuggestions(t *testing.T) {
	d := base()
	for i := 0; i < 6; i++ {
		mac := "AA:00:00:00:00:0" + string(rune('1'+i))
		d.Cls = append(d.Cls, Client{MAC: mac, Hostname: "dev" + mac[len(mac)-1:], Online: true})
		d.Traffic = append(d.Traffic, ClientTraffic{MAC: mac, Fl: 100})
	}
	d.Traffic[0].Fl = ChattyMinFlows * 4 // far above the median
	expect(t, d, "chatty", 1)
	d.Cls[0].ChattyMult = 1000 // its group allows much more
	expect(t, d, "chatty", 0)

	w := base()
	w.Cls = []Client{{MAC: "AA:00:00:00:00:01", Hostname: "sensor", Device: "ap1", WiFi: true, Online: true, Signal: -80, LastSeen: t0.Unix()}}
	expect(t, w, "weak-signal", 1)
	w.Cls[0].IgnoreWeak = true
	expect(t, w, "weak-signal", 0)
}
