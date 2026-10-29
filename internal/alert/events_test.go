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
	if kinds(*got3) != "wifi_disconnects/ap/AA:BB:CC:00:11:22" || !strings.Contains((*got3)[0].Msg, "ap-lost-connection") {
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

func TestCLICommandErrorIsNotAScriptError(t *testing.T) {
	e, got := newEngine(t)
	e.LogEvent(logEv("gw", "script,error,debug", "error", "(ssh-cmd:daniel@192.168.88.2) bad command name foo (line 1 column 1)"))
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
