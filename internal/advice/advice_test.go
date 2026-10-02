package advice

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func base() *Data {
	return &Data{NowT: t0, Uptime: time.Hour}
}

func find(res Result, rule string) []Suggestion {
	var out []Suggestion
	for _, s := range res.Items {
		if s.Rule == rule {
			out = append(out, s)
		}
	}
	return out
}

func run(t *testing.T, d *Data, rule string) ([]Suggestion, string) {
	t.Helper()
	for _, r := range Rules {
		if r.ID == rule {
			return r.Eval(d)
		}
	}
	t.Fatalf("unknown rule %s", rule)
	return nil, ""
}

func expect(t *testing.T, d *Data, rule string, n int) []Suggestion {
	t.Helper()
	out, _ := run(t, d, rule)
	if len(out) != n {
		t.Fatalf("%s: got %d suggestions, want %d: %+v", rule, len(out), n, out)
	}
	for _, s := range out {
		if s.Title == "" || s.Why == "" || s.Category == "" || s.Severity == "" {
			t.Fatalf("%s: incomplete suggestion %+v", rule, s)
		}
	}
	return out
}

func skipped(t *testing.T, d *Data, rule string) {
	t.Helper()
	out, skip := run(t, d, rule)
	if len(out) != 0 || skip == "" {
		t.Fatalf("%s: expected skip, got %d items, skip=%q", rule, len(out), skip)
	}
}

func TestEveryRuleRunsOnEmptyData(t *testing.T) {
	res := Evaluate(base())
	if res.Items == nil || res.Skipped == nil {
		t.Fatal("lists must not be nil")
	}
	if len(res.Items) != 0 {
		t.Fatalf("empty data must give no suggestions: %+v", res.Items)
	}
}

func TestMakeID(t *testing.T) {
	if got := MakeID("ram", "wAP Büro/1"); got != "ram:wap-b-ro-1" {
		t.Fatal(got)
	}
	if MakeID("x", "") != "x" {
		t.Fatal()
	}
}

func TestRAMAndCPU(t *testing.T) {
	d := base()
	d.Devs = []Device{{Name: "wAP", Up: true, Samples: 300, AvgMem: 91, MaxMem: 95, HighMemFrac: 0.95, AvgCPU: 20, HighCPUFrac: 0}}
	s := expect(t, d, "ram", 1)
	if s[0].Severity != SevWarning || !strings.Contains(strings.Join(s[0].Evidence, " "), "91%") {
		t.Fatalf("%+v", s[0])
	}
	expect(t, d, "cpu", 0)
	// below the sustained fraction
	d.Devs[0].HighMemFrac = 0.3
	expect(t, d, "ram", 0)
	// no history: current reading only, lower confidence
	d.Devs = []Device{{Name: "hEX", Up: true, LiveOK: true, Mem: 90, CPU: 80}}
	s = expect(t, d, "ram", 1)
	if s[0].Severity != SevTip || s[0].Confidence != "low" || s[0].Limits == "" {
		t.Fatalf("%+v", s[0])
	}
	expect(t, d, "cpu", 1)
	d.Devs[0].CPU, d.Devs[0].Mem = 10, 30
	expect(t, d, "cpu", 0)
	skipped(t, base(), "ram")
}

func TestVersion(t *testing.T) {
	d := base()
	d.Devs = []Device{{Name: "wAP", Version: "7.24.3 (stable)"}, {Name: "hEX", Version: "7.24.4 (stable)"}}
	s := expect(t, d, "version", 1)
	if s[0].Subject != "wAP" || s[0].Severity != SevInfo || !strings.Contains(s[0].Evidence[1], "hEX") {
		t.Fatalf("%+v", s[0])
	}
	d.Devs[0].Version = "7.22.1 (stable)"
	if s = expect(t, d, "version", 1); s[0].Severity != SevTip {
		t.Fatal("minor difference should be a tip")
	}
	d.Devs[0].Version = "7.24.4 (stable)"
	expect(t, d, "version", 0)
	// a pre-release never counts as the reference
	d.Devs = []Device{{Name: "a", Version: "7.20 (stable)"}, {Name: "b", Version: "7.21rc1 (testing)"}}
	expect(t, d, "version", 0)
	d.Devs = d.Devs[:1]
	skipped(t, d, "version")
}

func TestFirmware(t *testing.T) {
	d := base()
	d.Devs = []Device{{Name: "a", Firmware: "7.20", FirmwareUpg: "7.24"}}
	expect(t, d, "firmware", 1)
	d.Devs[0].FirmwareUpg = "7.20"
	expect(t, d, "firmware", 0)
	skipped(t, base(), "firmware")
}

func TestFasttrackAndOffload(t *testing.T) {
	d := base()
	d.Devs = []Device{{Name: "gw", Role: "router", Caps: &Caps{Fasttrack: true, HWOffload: true}}}
	expect(t, d, "fasttrack", 1)
	expect(t, d, "hw-offload", 1)
	d.Devs[0].Caps = &Caps{}
	expect(t, d, "fasttrack", 0)
	expect(t, d, "hw-offload", 0)
	d.Devs[0].Caps = nil
	skipped(t, d, "fasttrack")
}

func wifiClient(name, ap, ssid, band string, sig int) Client {
	return Client{MAC: "AA:00:00:00:00:" + strings.ToUpper(name[:2]), Hostname: name, Device: ap, SSID: ssid, Band: band, Signal: sig, WiFi: true, Online: true}
}

func TestWeakSignal(t *testing.T) {
	d := base()
	d.Cls = []Client{wifiClient("phone", "ap1", "Home", "5ghz-ac", -81), wifiClient("tablet", "ap1", "Home", "5ghz-ac", -60), wifiClient("unknown", "ap1", "Home", "5ghz-ac", 0)}
	s := expect(t, d, "weak-signal", 1)
	if !strings.Contains(strings.Join(s[0].Evidence, " "), "phone (-81 dBm)") || !strings.Contains(s[0].Evidence[0], "1 of 3") {
		t.Fatalf("%+v", s[0])
	}
	d.Cls[0].Signal = -70
	expect(t, d, "weak-signal", 0)
	skipped(t, base(), "weak-signal")
}

func TestBand24(t *testing.T) {
	d := base()
	d.Cls = []Client{wifiClient("aa1", "ap1", "Home", "2ghz-n", -55), wifiClient("bb1", "ap1", "Home", "2ghz-n", -60),
		wifiClient("cc1", "ap1", "Home", "5ghz-ac", -50)}
	expect(t, d, "band-24", 1)
	// weak 2.4 GHz clients are left alone; one client alone is not enough
	d.Cls[0].Signal = -80
	expect(t, d, "band-24", 0)
	d.Cls[0].Signal = -55
	// SSID without any 5 GHz offer
	d.Cls[2].SSID = "Other"
	expect(t, d, "band-24", 0)
	// 5 GHz known from the stored capabilities
	d.Devs = []Device{{Name: "ap1", Caps: &Caps{WifiIfs: []WifiIf{{Name: "wifi2", SSID: "Home", Band: "5ghz-ax"}}}}}
	expect(t, d, "band-24", 1)
}

func TestRoamingAndAPLoad(t *testing.T) {
	d := base()
	d.Cls = []Client{{MAC: "AA:AA:AA:AA:AA:01", Hostname: "laptop"}}
	d.RoamV = []RoamStat{{MAC: "AA:AA:AA:AA:AA:01", N: 45, APs: 2}}
	s := expect(t, d, "roaming", 1)
	if !strings.Contains(s[0].Title, "laptop") {
		t.Fatal(s[0].Title)
	}
	d.RoamV[0].N = 5
	expect(t, d, "roaming", 0)

	d = base()
	for i := 0; i < APClientsMax; i++ {
		d.Cls = append(d.Cls, Client{MAC: "AA:00:00:00:00:" + string(rune('A'+i/26)) + string(rune('A'+i%26)), Device: "ap1", WiFi: true, Online: true})
	}
	expect(t, d, "ap-load", 1)
	d.Cls = d.Cls[1:]
	expect(t, d, "ap-load", 0)
}

func TestUnidentified(t *testing.T) {
	d := base()
	for i := 0; i < 6; i++ {
		d.Cls = append(d.Cls, Client{MAC: "6A:00:00:00:00:0" + string(rune('0'+i)), Online: true})
	}
	for i := 0; i < 10; i++ {
		d.Cls = append(d.Cls, Client{MAC: "00:11:22:33:44:0" + string(rune('0'+i)), Hostname: "host", Online: true})
	}
	s := expect(t, d, "unidentified", 1)
	if !strings.Contains(s[0].Evidence[0], "6 of 16") || !strings.Contains(s[0].Evidence[0], "6 use a randomized") {
		t.Fatalf("%v", s[0].Evidence)
	}
	d.Cls = d.Cls[4:] // 2 unnamed left
	expect(t, d, "unidentified", 0)
	skipped(t, base(), "unidentified")
}

func TestStaleClients(t *testing.T) {
	d := base()
	old := t0.Add(-100 * 24 * time.Hour).Unix()
	for i := 0; i < StaleMin; i++ {
		d.Cls = append(d.Cls, Client{MAC: "00:00:00:00:00:" + string(rune('A'+i)), LastSeen: old})
	}
	expect(t, d, "stale-clients", 1)
	d.Cls[0].LastSeen = t0.Unix()
	expect(t, d, "stale-clients", 0)
	skipped(t, base(), "stale-clients")
}

func TestStaticLease(t *testing.T) {
	d := base()
	d.Cls = []Client{{MAC: "DC:A6:32:10:00:02", Hostname: "nas", Online: true, FirstSeen: t0.Add(-30 * 24 * time.Hour).Unix()},
		{MAC: "3C:22:FB:10:00:01", Hostname: "iphone", WiFi: true, Online: true, FirstSeen: t0.Add(-30 * 24 * time.Hour).Unix()}}
	d.Churn = map[string]int{"DC:A6:32:10:00:02": 4, "3C:22:FB:10:00:01": 9}
	s := expect(t, d, "static-lease", 1)
	if !strings.Contains(s[0].Commands, "DC:A6:32:10:00:02") || strings.Contains(s[0].Commands, "3C:22") {
		t.Fatalf("%s", s[0].Commands)
	}
	d.Churn["DC:A6:32:10:00:02"] = 2
	expect(t, d, "static-lease", 0)
	skipped(t, base(), "static-lease")
}

func portd(mac, rip string, port int, flows, bytes int64) PortUse {
	return PortUse{MAC: mac, RIP: rip, Port: port, Proto: 6, Flows: flows, Bytes: bytes}
}

func withFlows(d *Data) *Data {
	d.Traffic = []ClientTraffic{{MAC: "A", Up: 1000, Down: 1000, Fl: 10}}
	return d
}

func TestCleartext(t *testing.T) {
	d := withFlows(base())
	d.Cls = []Client{{MAC: "A", Hostname: "cam1"}}
	d.Ports = []PortUse{portd("A", "203.0.113.5", 23, 10, 5000)}
	s := expect(t, d, "proto-cleartext", 1)
	if s[0].Severity != SevWarning || s[0].Subject != "telnet" {
		t.Fatalf("%+v", s[0])
	}
	// only inside the LAN: informational
	d.Ports = []PortUse{portd("A", "192.168.88.2", 21, 10, 5000)}
	if s = expect(t, d, "proto-cleartext", 1); s[0].Severity != SevInfo {
		t.Fatal("LAN-only FTP should be info")
	}
	d.Ports = []PortUse{portd("A", "203.0.113.5", 23, 1, 10)}
	expect(t, d, "proto-cleartext", 0)
	skipped(t, base(), "proto-cleartext")
}

func TestSMBAndRDP(t *testing.T) {
	d := withFlows(base())
	d.Ports = []PortUse{portd("A", "198.51.100.7", 445, 5, 1000), portd("A", "192.168.88.5", 445, 50, 1000), portd("A", "198.51.100.8", 3389, 9, 1000)}
	expect(t, d, "proto-smb", 1)
	expect(t, d, "proto-rdp", 1)
	d.Ports = []PortUse{portd("A", "192.168.88.5", 445, 50, 1000), portd("A", "192.168.88.5", 3389, 50, 1000)}
	expect(t, d, "proto-smb", 0)
	expect(t, d, "proto-rdp", 0)
}

func TestHTTPIoT(t *testing.T) {
	d := base()
	d.Cls = []Client{{MAC: "A", Vendor: "Hikvision", Hostname: "cam-garage"}, {MAC: "B", Vendor: "Dell", Hostname: "pc"}}
	d.Traffic = []ClientTraffic{{MAC: "A", Up: 900_000, Down: 100_000}, {MAC: "B", Up: 900_000, Down: 100_000}}
	d.Ports = []PortUse{portd("A", "203.0.113.9", 80, 40, 800_000), portd("B", "203.0.113.9", 80, 40, 800_000)}
	s := expect(t, d, "proto-http-iot", 1)
	if !strings.Contains(strings.Join(s[0].Evidence, " "), "cam-garage") || strings.Contains(strings.Join(s[0].Evidence, " "), "pc:") {
		t.Fatalf("%v", s[0].Evidence)
	}
	d.Ports[0].Bytes = 100_000 // 10 % of its traffic
	expect(t, d, "proto-http-iot", 0)
}

func TestInbound(t *testing.T) {
	d := base()
	d.Cls = []Client{{MAC: "A", Hostname: "winpc"}}
	d.In = []Inbound{{MAC: "A", IP: "192.168.88.9", Port: 3389, Proto: 6, Flows: 20, Hosts: 3}}
	s := expect(t, d, "exposed-inbound", 1)
	if s[0].Severity != SevWarning || !strings.Contains(s[0].Title, "RDP on winpc") {
		t.Fatalf("%+v", s[0])
	}
	d.In[0].Flows = 1
	expect(t, d, "exposed-inbound", 0)
	d.In[0].Flows, d.In[0].Port = 20, 8080
	expect(t, d, "exposed-inbound", 0)
}

func TestBlockOrigin(t *testing.T) {
	d := base()
	d.Blocked = []BlockedSrc{
		{IP: "203.0.113.1", Hits: 400, CC: "XX", ASN: 64500, ASOrg: "Badnet"},
		{IP: "203.0.113.2", Hits: 200, CC: "XX", ASN: 64500, ASOrg: "Badnet"},
		{IP: "198.51.100.1", Hits: 100, CC: "YY", ASN: 64501},
		{IP: "198.51.100.2", Hits: 100, CC: "ZZ", ASN: 64502},
	}
	out := expect(t, d, "fw-origin", 2) // country XX (75 %) and AS64500
	if !strings.Contains(out[0].Commands, "203.0.113.1") || !strings.Contains(out[0].Commands, "/ip firewall raw add") {
		t.Fatalf("%s", out[0].Commands)
	}
	// spread over many countries: nothing to say
	d.Blocked = []BlockedSrc{{IP: "203.0.113.1", Hits: 100, CC: "A1", ASN: 1}, {IP: "203.0.113.2", Hits: 100, CC: "B1", ASN: 2},
		{IP: "203.0.113.3", Hits: 100, CC: "C1", ASN: 3}, {IP: "203.0.113.4", Hits: 100, CC: "D1", ASN: 4}}
	expect(t, d, "fw-origin", 0)
	// no GeoIP data at all
	d.Blocked = []BlockedSrc{{IP: "203.0.113.1", Hits: 900}}
	skipped(t, d, "fw-origin")
	skipped(t, base(), "fw-origin")
}

func TestMgmtOpen(t *testing.T) {
	d := base()
	d.Devs = []Device{{Name: "gw", Role: "router", Caps: &Caps{WwwSSL: true}}}
	expect(t, d, "mgmt-open", 1)
	d.Devs[0].Caps.WwwSSLAddr = "192.168.88.0/24"
	expect(t, d, "mgmt-open", 0)
	d.Devs[0].Caps.WwwSSLAddr = ""
	d.Devs[0].Caps.Filter = []FilterRule{{Chain: "input", Action: "drop"}}
	expect(t, d, "mgmt-open", 0)
	d.Devs[0].Caps = nil
	skipped(t, d, "mgmt-open")
}

func TestUnusedRules(t *testing.T) {
	d := base()
	d.FwHist = 10 * 24 * time.Hour
	d.Rules = []FwRule{{Device: "gw", Prefix: "a", Chain: "input", Action: "drop", Descr: "old", Hits7d: 0},
		{Device: "gw", Prefix: "b", Chain: "input", Action: "drop", Hits7d: 50},
		{Device: "gw", Prefix: "m", Chain: "input", Action: "drop", Managed: true, Hits7d: 0}}
	expect(t, d, "fw-unused", 1)
	d.FwHist = 2 * 24 * time.Hour
	skipped(t, d, "fw-unused")
	d.FwHist = 10 * 24 * time.Hour
	d.Rules[1].Hits7d = 0 // no rule logs anything: probably syslog problem, not unused rules
	expect(t, d, "fw-unused", 0)
}

func TestDropNoLog(t *testing.T) {
	d := base()
	d.Devs = []Device{{Name: "gw", Caps: &Caps{Filter: []FilterRule{{Chain: "input", Action: "drop", Comment: "final"}, {Chain: "forward", Action: "drop", Log: true}}}}}
	s := expect(t, d, "fw-nolog", 1)
	if !strings.Contains(s[0].Evidence[0], "1 of 2") {
		t.Fatal(s[0].Evidence)
	}
	d.Devs[0].Caps.Filter[0].Log = true
	expect(t, d, "fw-nolog", 0)
	d.Devs[0].Caps = nil
	skipped(t, d, "fw-nolog")
}

func TestDNSBypass(t *testing.T) {
	d := withFlows(base())
	d.Devs = []Device{{Name: "gw", Role: "router", Addr: "10.0.0.1"}}
	d.Ports = []PortUse{{MAC: "A", RIP: "8.8.8.8", Port: 53, Proto: 17, Flows: 200}, {MAC: "B", RIP: "192.168.88.1", Port: 53, Proto: 17, Flows: 900}}
	s := expect(t, d, "dns-bypass", 1)
	if !strings.Contains(s[0].Commands, "to-addresses=10.0.0.1") || !strings.Contains(s[0].Commands, "dst-address=!10.0.0.1") {
		t.Fatal(s[0].Commands)
	}
	d.Ports[0].Flows = 5
	expect(t, d, "dns-bypass", 0)
	skipped(t, base(), "dns-bypass")
}

func TestDoH(t *testing.T) {
	d := withFlows(base())
	d.DoH = []PortUse{{MAC: "A", RIP: "8.8.8.8", Host: "dns.google", Port: 443, Flows: 100}}
	expect(t, d, "dns-doh", 1)
	d.DoH[0].Flows = 3
	expect(t, d, "dns-doh", 0)
}

const GB = 1_000_000_000

func TestDominantClient(t *testing.T) {
	d := base()
	d.Cls = []Client{{MAC: "A", Hostname: "nas"}}
	d.TopSvc = map[string]string{"A": "Backblaze"}
	d.Traffic = []ClientTraffic{{MAC: "A", Down: 8 * GB}, {MAC: "B", Down: GB}, {MAC: "C", Down: GB / 2}}
	s := expect(t, d, "dominant-client", 1)
	if !strings.Contains(strings.Join(s[0].Evidence, " "), "Backblaze") {
		t.Fatal(s[0].Evidence)
	}
	d.Traffic[0].Down = GB
	expect(t, d, "dominant-client", 0)
	d.Traffic = d.Traffic[:2] // too few clients to talk about shares
	d.Traffic[0].Down = 8 * GB
	expect(t, d, "dominant-client", 0)
	skipped(t, base(), "dominant-client")
}

func TestUploadHeavy(t *testing.T) {
	d := base()
	d.Traffic = []ClientTraffic{{MAC: "A", Up: 5 * GB, Down: GB / 2}, {MAC: "B", Up: 5 * GB, Down: 4 * GB}, {MAC: "C", Up: 100, Down: 0}}
	expect(t, d, "upload-heavy", 1)
	d.Traffic[0].Up = GB / 10
	expect(t, d, "upload-heavy", 0)
}

func TestChatty(t *testing.T) {
	d := base()
	for _, m := range []string{"A", "B", "C", "D", "E"} {
		d.Traffic = append(d.Traffic, ClientTraffic{MAC: m, Fl: 1000})
	}
	d.Traffic = append(d.Traffic, ClientTraffic{MAC: "Z", Fl: 90000})
	expect(t, d, "chatty", 1)
	d.Traffic[5].Fl = 5000
	expect(t, d, "chatty", 0)
}

func TestFlowRules(t *testing.T) {
	d := base()
	d.Devs = []Device{{Name: "gw", Role: "router", Up: true, Managed: true, Caps: &Caps{FlowSupported: true}}}
	s := expect(t, d, "flow-disabled", 1)
	if s[0].Severity != SevWarning || !strings.Contains(s[0].Steps[0], "wizard") {
		t.Fatalf("%+v", s[0])
	}
	d.Devs[0].Caps.FlowEnabled, d.Devs[0].Caps.FlowTargets = true, []string{"10.0.0.2 (ipfix)"}
	expect(t, d, "flow-disabled", 0)
	d.Devs[0].Caps = nil
	skipped(t, d, "flow-disabled")

	// silent exporter
	d.Devs[0].FlowKnown = true
	d.Devs[0].FlowLast = t0.Add(-30 * time.Minute).Unix()
	expect(t, d, "flow-silent", 1)
	d.Devs[0].FlowLast = t0.Add(-2 * time.Minute).Unix()
	expect(t, d, "flow-silent", 0)
	d.Devs[0].FlowLast = 0
	if s = expect(t, d, "flow-silent", 1); !strings.Contains(s[0].Evidence[0], "since mtmon started") {
		t.Fatal(s[0].Evidence)
	}
	d.Uptime = 3 * time.Minute // just restarted: never conclude anything
	skipped(t, d, "flow-silent")
	d.Uptime = time.Hour
	d.Devs[0].Up = false
	expect(t, d, "flow-silent", 0)
	// APs are not expected to export
	d.Devs = []Device{{Name: "ap", Role: "ap", Up: true, FlowKnown: true}}
	expect(t, d, "flow-silent", 0)
}

func TestNoSyslog(t *testing.T) {
	d := base()
	d.Devs = []Device{{Name: "gw", Role: "router"}}
	s := expect(t, d, "no-syslog", 1)
	if s[0].Confidence != "medium" {
		t.Fatal("without caps the verdict is less certain")
	}
	d.Devs[0].FwLogging = true
	expect(t, d, "no-syslog", 0)
	d.Devs[0].FwLogging = false
	d.Devs[0].Caps = &Caps{LogActions: 1}
	expect(t, d, "no-syslog", 0)
	skipped(t, base(), "no-syslog")
}

func TestEvaluateSortsAndDefaults(t *testing.T) {
	d := base()
	d.Devs = []Device{{Name: "gw", Role: "router", Caps: &Caps{Fasttrack: true, FlowSupported: true}, Up: true, Samples: 100, HighCPUFrac: 1, AvgCPU: 90, MaxCPU: 99}}
	res := Evaluate(d)
	if len(res.Items) < 3 {
		t.Fatalf("%+v", res.Items)
	}
	if res.Items[0].Severity != SevWarning {
		t.Fatalf("warnings first: %+v", res.Items[0])
	}
	for _, it := range res.Items {
		if it.ID == "" || it.Rule == "" || it.Evidence == nil || it.Steps == nil || it.Confidence == "" {
			t.Fatalf("defaults missing: %+v", it)
		}
	}
	if len(find(res, "cpu")) != 1 {
		t.Fatal("cpu rule should fire")
	}
}

func TestPanicIsContained(t *testing.T) {
	old := Rules
	defer func() { Rules = old }()
	Rules = []Rule{{"boom", func(Snapshot) ([]Suggestion, string) { panic("x") }}}
	res := Evaluate(base())
	if len(res.Skipped) != 1 {
		t.Fatal("panic must be reported as skipped")
	}
}

func TestIsPublic(t *testing.T) {
	for ip, want := range map[string]bool{"8.8.8.8": true, "192.168.1.1": false, "10.1.1.1": false, "100.64.1.1": false, "127.0.0.1": false,
		"169.254.1.1": false, "2001:db8::1": true, "fd00::1": false, "bogus": false, "224.0.0.1": false} {
		if IsPublic(ip) != want {
			t.Errorf("%s: want %v", ip, want)
		}
	}
}
