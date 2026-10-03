package api

import "testing"

func TestGuessLabel(t *testing.T) {
	cases := []struct {
		host, vendor, mac, ip string
		want, conf            string
	}{
		{"Daniels-iPhone", "Apple", "AA:BB:CC:00:3F:A2", "", "Daniels iPhone", "medium"},
		{"printer.fritz.box", "HP", "AA:BB:CC:00:3F:A2", "", "printer", "medium"},
		{"ESP_A1B2C3", "Espressif", "AA:BB:CC:00:3F:A2", "", "ESP", "medium"},
		{"192.168.1.5", "Sonos Inc", "AA:BB:CC:00:3F:A2", "", "Sonos speaker 3F:A2", "low"},
		{"", "Private (randomized)", "AA:BB:CC:00:3F:A2", "", "Phone/laptop 3F:A2", "low"},
		{"", "", "AA:BB:CC:00:3F:A2", "", "", ""},
	}
	for _, c := range cases {
		got, _, conf := GuessLabel(c.host, c.vendor, c.mac, c.ip)
		if got != c.want || conf != c.conf {
			t.Errorf("GuessLabel(%q,%q) = %q/%q, want %q/%q", c.host, c.vendor, got, conf, c.want, c.conf)
		}
	}
}
