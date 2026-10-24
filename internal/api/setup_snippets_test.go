package api

import (
	"strings"
	"testing"
)

func TestSetupSnippets(t *testing.T) {
	u := apiUserSnippet("192.0.2.5", "pw123")
	for _, want := range []string{":if ([:len [/user group find name=mtmon-ro]] = 0) do={ /user group add name=mtmon-ro", ":if ([:len [/user find name=mtmon]] = 0) do={ /user add name=mtmon group=mtmon-ro password=pw123 address=192.0.2.5/32 }"} {
		if !strings.Contains(u, want) {
			t.Errorf("api_user lacks %q:\n%s", want, u)
		}
	}
	if strings.Contains(u, "CHANGE_ME") {
		t.Error("fixed password")
	}
	tls := restTLSSnippet("192.0.2.1", "192.0.2.5")
	for _, want := range []string{"/certificate find name=mtmon-ca]] = 0) do={ /certificate add name=mtmon-ca", "/certificate find name=mtmon-ssl]] = 0)", "common-name=192.0.2.1 subject-alt-name=IP:192.0.2.1", "REPLACES", "available-from=192.0.2.5/32"} {
		if !strings.Contains(tls, want) {
			t.Errorf("rest_tls lacks %q:\n%s", want, tls)
		}
	}
	if strings.Contains(tls, "<LAN-NETZ>") {
		t.Error("placeholder network")
	}
	// the router address ends up in a command: anything but an IP is replaced
	if got := restTLSSnippet(cleanRouterIP("1.2.3.4; /system reset-configuration"), "192.0.2.5"); !strings.Contains(got, "<ROUTER-IP>") || strings.Contains(got, "reset") {
		t.Errorf("injection: %s", got)
	}
	a, b := randomPassword(20), randomPassword(20)
	if len(a) != 20 || a == b {
		t.Errorf("passwords: %q %q", a, b)
	}
}
