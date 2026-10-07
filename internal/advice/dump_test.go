package advice

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// kitchenSink fires every rule (and the main variants) so the UI translations can be checked for gaps:
//
//	MTMON_DUMP=/tmp/advice.json go test ./internal/advice -run TestDumpTexts
func kitchenSink() []*Data {
	var out []*Data
	a := base()
	a.Uptime = time.Hour
	old := t0.Add(-100 * 24 * time.Hour).Unix()
	a.Devs = []Device{
		{Name: "gw", Addr: "10.0.0.1", Role: "router", Up: true, Version: "7.24.4 (stable)", Firmware: "7.20", FirmwareUpg: "7.24", Samples: 300, AvgMem: 91, MaxMem: 96, HighMemFrac: .95, AvgCPU: 75, MaxCPU: 99, HighCPUFrac: .9,
			FlowKnown: true, FlowLast: 0, Managed: true,
			Caps: &Caps{Fasttrack: true, HWOffload: true, FlowSupported: true, WwwSSL: true, Filter: []FilterRule{{Chain: "input", Action: "accept"}, {Chain: "forward", Action: "drop", Comment: "final"}, {Chain: "input", Action: "drop", Log: true}}}},
		{Name: "wAP", Role: "ap", Up: true, LiveOK: true, Version: "7.24.3 (stable)", Mem: 90, CPU: 80, Caps: &Caps{WifiIfs: []WifiIf{{SSID: "Home", Band: "5ghz-ax"}}}},
		{Name: "old", Role: "ap", Version: "7.20.1 (stable)"},
	}
	a.Cls = []Client{wifiClient("phone", "wAP", "Home", "5ghz-ac", -82), wifiClient("tv", "wAP", "Home", "2ghz-n", -55), wifiClient("sw1", "wAP", "Home", "2ghz-n", -60),
		{MAC: "AA:AA:AA:AA:AA:01", Hostname: "laptop", WiFi: true, Online: true, Device: "wAP"},
		{MAC: "DC:A6:32:10:00:02", Hostname: "nas", Online: true, FirstSeen: old},
		{MAC: "A", Hostname: "cam-garage", Vendor: "Hikvision", Online: true},
		{MAC: "B", Hostname: "winpc", Online: true}, {MAC: "H", Hostname: "cam-door", Vendor: "Reolink", Online: true}}
	for i := 0; i < 6; i++ {
		a.Cls = append(a.Cls, Client{MAC: "6A:00:00:00:00:0" + string(rune('0'+i)), Online: true})
	}
	for i := 0; i < StaleMin; i++ {
		a.Cls = append(a.Cls, Client{MAC: "00:00:00:00:00:" + string(rune('A'+i)), LastSeen: old})
	}
	for i := 0; i < APClientsMax; i++ {
		a.Cls = append(a.Cls, Client{MAC: "CC:00:00:00:00:" + string(rune('A'+i/26)) + string(rune('A'+i%26)), Device: "wAP", WiFi: true, Online: true})
	}
	a.RoamV = []RoamStat{{MAC: "AA:AA:AA:AA:AA:01", N: 45, APs: 2}}
	a.Churn = map[string]int{"DC:A6:32:10:00:02": 4}
	a.Traffic = []ClientTraffic{{MAC: "A", Up: 5 * GB, Down: GB / 2, Fl: 90000}, {MAC: "B", Up: 100, Down: 100, Fl: 100}, {MAC: "C", Fl: 100, Down: 1000}, {MAC: "D", Fl: 100, Down: 1000}, {MAC: "E", Fl: 100}, {MAC: "F", Fl: 100}, {MAC: "H", Up: 9_000_000, Down: 1_000_000, Fl: 50}}
	a.TopSvc = map[string]string{"A": "Backblaze"}
	a.Ports = []PortUse{portd("A", "203.0.113.5", 23, 10, 5000), portd("B", "203.0.113.6", 21, 10, 5000), portd("A", "198.51.100.7", 445, 5, 1000),
		portd("B", "198.51.100.8", 3389, 9, 1000), {MAC: "A", RIP: "8.8.8.8", Port: 53, Proto: 17, Flows: 200}, portd("H", "203.0.113.9", 80, 40, 8_000_000)}
	a.Traffic[0].Up, a.Traffic[0].Down = 5*GB, GB/2
	a.DoH = []PortUse{{MAC: "A", RIP: "8.8.8.8", Host: "dns.google", Port: 443, Flows: 100}}
	a.In = []Inbound{{MAC: "B", IP: "192.168.88.9", Port: 3389, Proto: 6, Flows: 20, Hosts: 3}}
	a.Blocked = []BlockedSrc{{IP: "203.0.113.1", Hits: 400, CC: "XX", ASN: 64500, ASOrg: "Badnet"}, {IP: "198.51.100.1", Hits: 100, CC: "YY", ASN: 64501}}
	a.FwHist = 10 * 24 * time.Hour
	a.Rules = []FwRule{{Device: "gw", Prefix: "a", Chain: "input", Action: "drop", Descr: "old"}, {Device: "gw", Prefix: "b", Chain: "input", Action: "drop", Hits7d: 50}}
	out = append(out, a)

	// variants
	b := base()
	b.Devs = []Device{{Name: "gw2", Role: "router", Up: true, FlowKnown: true, FlowLast: t0.Add(-30 * time.Minute).Unix(), Caps: &Caps{FlowSupported: true, FlowEnabled: true, FlowTargets: []string{"x"}}},
		{Name: "gw3", Role: "router", Up: true, Caps: &Caps{FlowSupported: true, WwwSSL: true}}}
	b.Ports = []PortUse{portd("A", "192.168.88.2", 21, 10, 5000)}
	b.Traffic = []ClientTraffic{{MAC: "A", Up: 1, Down: 1}}
	b.Cls = []Client{{MAC: "A", Hostname: "x"}}
	out = append(out, b)
	return out
}

func TestDumpTexts(t *testing.T) {
	path := os.Getenv("MTMON_DUMP")
	if path == "" {
		t.Skip("set MTMON_DUMP=file.json")
	}
	seen := map[string]bool{}
	var texts []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			texts = append(texts, s)
		}
	}
	rules := map[string]bool{}
	for _, d := range kitchenSink() {
		res := Evaluate(d)
		for _, x := range res.Items {
			rules[x.Rule] = true
			add(x.Title)
			add(x.Why)
			add(x.Limits)
			for _, e := range x.Evidence {
				add(e)
			}
			for _, e := range x.Steps {
				add(e)
			}
		}
		for _, k := range res.Skipped {
			add(k.Reason)
		}
	}
	for _, r := range Rules {
		if !rules[r.ID] {
			t.Errorf("kitchen sink does not fire rule %s", r.ID)
		}
	}
	// skip reasons that need other data
	for _, d := range []*Data{base()} {
		res := Evaluate(d)
		for _, k := range res.Skipped {
			add(k.Reason)
		}
	}
	b, _ := json.MarshalIndent(texts, "", " ")
	os.WriteFile(path, b, 0o644)
}
