package syslog

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in                                    string
		prefix, chain, src, dst, proto, flags string
		sport, dport                          int
		mac, nat, cstate, inif, outif         string
	}{
		{"firewall,info MTM-11 forward: in:bridge out:ether1, connection-state:new src-mac aa:bb:cc:00:11:22, proto TCP (SYN), 192.168.88.50:51234->93.184.216.34:443, len 60",
			"MTM-11", "forward", "192.168.88.50", "93.184.216.34", "TCP", "SYN", 51234, 443, "AA:BB:CC:00:11:22", "", "new", "bridge", "ether1"},
		{"<134>Oct  1 12:00:00 gw firewall,info forward: in:bridge out:ether1, proto UDP, 192.168.88.7:40000->129.6.15.28:123, NAT (192.168.88.7:40000->203.0.113.5:40000)->129.6.15.28:123, len 76",
			"", "forward", "192.168.88.7", "129.6.15.28", "UDP", "", 40000, 123, "", "192.168.88.7:40000->203.0.113.5:40000", "", "bridge", "ether1"},
		{"firewall,info my own prefix input: in:ether1 out:(unknown 0), src-mac 00:00:00:00:00:01, proto ICMP (type 8, code 0), 198.51.100.9->192.0.2.1, len 84",
			"my own prefix", "input", "198.51.100.9", "192.0.2.1", "ICMP", "type 8, code 0", 0, 0, "00:00:00:00:00:01", "", "", "ether1", "(unknown 0)"},
		{"firewall,info MTM-34 input: in:bridge out:(unknown 0), connection-state:new,dnat src-mac 02:00:5E:10:20:30, proto UDP, 192.168.0.100:59194->192.168.0.1:53, NAT 192.168.0.100:59194->(198.51.100.53:53->192.168.0.1:53), len 62",
			"MTM-34", "input", "192.168.0.100", "192.168.0.1", "UDP", "", 59194, 53, "02:00:5E:10:20:30", "198.51.100.53:53->192.168.0.1:53", "new,dnat", "bridge", "(unknown 0)"},
		{"gw firewall,info MTM-NEW forward: in:bridge out:ether1, connection-state:new proto TCP (SYN), 10.0.0.2:1->1.1.1.1:853, len 52",
			"MTM-NEW", "forward", "10.0.0.2", "1.1.1.1", "TCP", "SYN", 1, 853, "", "", "new", "bridge", "ether1"},
	}
	for i, c := range cases {
		e, ok := ParseFirewall(c.in)
		if !ok {
			t.Fatalf("case %d: no parse: %s", i, c.in)
		}
		if e.Prefix != c.prefix || e.Chain != c.chain || e.Src != c.src || e.Dst != c.dst || e.Proto != c.proto || e.Flags != c.flags ||
			e.SPort != c.sport || e.DPort != c.dport || e.MAC != c.mac || e.NAT != c.nat || e.CState != c.cstate || e.InIf != c.inif || e.OutIf != c.outif {
			t.Fatalf("case %d: %+v", i, e)
		}
	}
	for _, bad := range []string{"", "system,info,account user admin logged in", "firewall,info garbage", "dhcp,info lease"} {
		if _, ok := ParseFirewall(bad); ok {
			t.Fatalf("should not parse %q", bad)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add("firewall,info MTM-1 forward: in:a out:b, proto TCP (SYN), 1.1.1.1:1->2.2.2.2:2, len 5")
	f.Fuzz(func(t *testing.T, s string) { ParseFirewall(s) })
}

func TestParseLog(t *testing.T) {
	for _, c := range []struct {
		line, topics, level, msg string
		ok                       bool
	}{
		{"system,info,account user admin logged in from 192.0.2.5 via ssh", "system,info,account", "info", "user admin logged in from 192.0.2.5 via ssh", true},
		{"<86>Oct  1 12:00:00 gw system,error,critical router rebooted because of power failure", "system,error,critical", "critical", "router rebooted because of power failure", true},
		{"critical something broke", "critical", "critical", "something broke", true},
		{"script,error script scheduler failed", "script,error", "error", "script scheduler failed", true},
		{"firewall,info MTM-1 forward: in:a out:b", "", "", "", false}, // firewall lines are not log events
		{"hello world this is not routeros", "", "", "", false},
		{"Oct  1 12:00:00 gw dhcp,info dhcp1 deassigned 192.168.88.5 from AA:BB", "dhcp,info", "info", "dhcp1 deassigned 192.168.88.5 from AA:BB", true},
	} {
		e, ok := ParseLog(c.line)
		if ok != c.ok || (ok && (e.Topics != c.topics || e.Level != c.level || e.Msg != c.msg)) {
			t.Errorf("%q: %+v %v", c.line, e, ok)
		}
	}
	e, _ := ParseLog("system,info,account x")
	if !e.HasTopic("account") || e.HasTopic("acc") {
		t.Error("HasTopic")
	}
}
