package alert

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func logEv(dev, topics, level, msg string) store.LogEvent {
	return store.LogEvent{TS: time.Now().Unix(), Device: dev, Topics: topics, Level: level, Msg: msg}
}

func TestRouterLogAlerts(t *testing.T) {
	e, got := newEngine(t)
	e.LogEvent(logEv("gw", "system,info,account", "info", "user admin logged in from 192.168.88.2 via ssh")) // nothing
	e.LogEvent(logEv("gw", "system,error,critical", "critical", "router was rebooted without proper shutdown"))
	e.LogEvent(logEv("gw", "script,error", "error", "script error: failing script"))
	for i := 0; i < 4; i++ {
		e.LogEvent(logEv("gw", "system,error,critical,account", "critical", fmt.Sprintf("login failure for user root from 203.0.113.%d via ssh", i)))
	}
	if kinds(*got) != "system_critical/gw,script_error/gw" {
		t.Fatalf("single events: %s", kinds(*got))
	}
	if (*got)[0].Severity != "critical" {
		t.Errorf("critical topic: %+v", (*got)[0])
	}
	// failed logins: one source, 5 times
	e2, got2 := newEngine(t)
	for i := 0; i < 5; i++ {
		e2.LogEvent(logEv("gw", "system,info,account", "info", "login failure for user admin from 203.0.113.9 via winbox"))
	}
	if kinds(*got2) != "login_failures/gw/203.0.113.9" || !strings.Contains((*got2)[0].Msg, "winbox") {
		t.Fatalf("login failures: %+v", *got2)
	}
	// wifi disconnect storm of one client
	e3, got3 := newEngine(t)
	for i := 0; i < 6; i++ {
		e3.LogEvent(logEv("ap", "wireless,info", "info", "AA:BB:CC:00:11:22@wifi1 disconnected, reason: ap-lost-connection"))
	}
	if kinds(*got3) != "wifi_disconnects/wifi1/AA:BB:CC:00:11:22" || !strings.Contains((*got3)[0].Msg, "ap-lost-connection") {
		t.Fatalf("wifi storm: %+v", *got3)
	}
	// dhcp trouble and wireguard handshakes
	e4, got4 := newEngine(t)
	for i := 0; i < 3; i++ {
		e4.LogEvent(logEv("gw", "dhcp,info", "info", "dhcp1 offering lease 192.168.88.200 for AA:BB:CC:00:11:22 without success"))
	}
	for i := 0; i < 5; i++ {
		e4.LogEvent(logEv("gw", "wireguard,debug", "debug", "peer1: handshake did not complete after 5 seconds"))
	}
	if kinds(*got4) != "dhcp_problem/gw,wireguard_handshake/gw" {
		t.Fatalf("dhcp/wg: %s", kinds(*got4))
	}
}

func TestWifiDisconnectNamesInterfaceAndSignal(t *testing.T) {
	e, got := newEngine(t)
	for i := 0; i < 6; i++ {
		e.LogEvent(logEv("capsman", "wireless,info", "info", "AA:BB:CC:00:11:33@cap-wifi1 disconnected, connection lost, signal strength -52"))
	}
	if len(*got) != 1 {
		t.Fatalf("alerts: %d", len(*got))
	}
	m := (*got)[0].Msg
	if !strings.Contains(m, "capsman (cap-wifi1)") || !strings.Contains(m, "signal is good") {
		t.Errorf("msg: %s", m)
	}
}

func TestCLICommandErrorIsNotAScriptError(t *testing.T) {
	e, got := newEngine(t)
	e.LogEvent(logEv("gw", "script,error,debug", "error", "(ssh-cmd:daniel@192.168.88.2) bad command name foo (line 1 column 1)"))
	e.LogEvent(logEv("gw", "script,error", "error", "executing script from sshd failed, please check it manually"))
	e.LogEvent(logEv("gw", "script,error", "error", "script error: failing script"))
	if kinds(*got) != "cli_error/gw/daniel,script_error/gw" {
		t.Fatalf("kinds: %s", kinds(*got))
	}
	a := (*got)[0]
	if a.Severity != "info" || a.Msg != "Command from daniel@192.168.88.2 failed on gw: bad command name foo (line 1 column 1)" {
		t.Errorf("cli alert: %+v", a)
	}
	if (*got)[1].Severity != "warning" {
		t.Errorf("script error stays a warning: %+v", (*got)[1])
	}
}

func TestDHCPFailAfterRoamIsAWiFiNote(t *testing.T) {
	const mac = "AA:BB:CC:00:11:22"
	e, got := newEngine(t)
	at := func(ts int64, topics, msg string) {
		ev := logEv("gw", topics, "info", msg)
		ev.TS = ts
		e.LogEvent(ev)
	}
	at(1000, "wireless,info", mac+"@wlan1: connected, signal strength -76")
	for i := int64(0); i < 5; i++ {
		at(1001+i*10, "dhcp,info", "dhcp1 offering lease 192.168.88.200 for "+mac+" without success")
	}
	if kinds(*got) != "wifi_roam_dhcp/"+mac {
		t.Fatalf("roam: %s", kinds(*got))
	}
	a := (*got)[0]
	if a.Severity != "info" || !strings.Contains(a.Msg, "roamed to gw") || !strings.Contains(a.Msg, "-76 dBm") || !strings.Contains(a.Msg, "not the DHCP server") {
		t.Errorf("note: %+v", a)
	}
	// the same failures without a roam in front stay a DHCP problem
	e2, got2 := newEngine(t)
	for i := int64(0); i < 3; i++ {
		ev := logEv("gw", "dhcp,info", "info", "dhcp1 offering lease 192.168.88.200 for "+mac+" without success")
		ev.TS = 5000 + i
		e2.LogEvent(ev)
	}
	if kinds(*got2) != "dhcp_problem/gw" {
		t.Fatalf("no roam: %s", kinds(*got2))
	}
	// a connect long before the failures is no roam either
	e3, got3 := newEngine(t)
	for _, ev := range []struct {
		ts     int64
		topics string
		msg    string
	}{{1000, "wireless,info", mac + "@wlan1: connected"}, {1100, "dhcp,info", "x offering lease 1.2.3.4 for " + mac + " without success"}, {1101, "dhcp,info", "x offering lease 1.2.3.4 for " + mac + " without success"}, {1102, "dhcp,info", "x offering lease 1.2.3.4 for " + mac + " without success"}} {
		l := logEv("gw", ev.topics, "info", ev.msg)
		l.TS = ev.ts
		e3.LogEvent(l)
	}
	if kinds(*got3) != "dhcp_problem/gw" {
		t.Fatalf("old connect: %s", kinds(*got3))
	}
}
