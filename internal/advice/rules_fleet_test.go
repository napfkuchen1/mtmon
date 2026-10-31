// SPDX-License-Identifier: AGPL-3.0-only

package advice

import "testing"

func TestCleartextServicesRule(t *testing.T) {
	d := &Data{Devs: []Device{
		{Name: "r1", Caps: &Caps{ProbedAt: 1, Cleartext: []string{"telnet", "ftp"}}},
		{Name: "r2", Caps: &Caps{ProbedAt: 1}},
		{Name: "r3"},
	}}
	got, why := ruleCleartextServices(d)
	if len(got) != 1 || got[0].Subject != "r1" || got[0].Severity != SevWarning || why != "" {
		t.Fatalf("got %+v %q", got, why)
	}
	if _, why := ruleCleartextServices(&Data{}); why == "" {
		t.Fatal("no devices must be skipped")
	}
}

func TestBaselineOpenMgmtAndNTP(t *testing.T) {
	d := &Data{Devs: []Device{
		{Name: "ap1", Caps: &Caps{ProbedAt: 1, Role: "ap", OpenMgmt: []string{"api", "winbox"}, NTPOff: true}},
		{Name: "r1", Caps: &Caps{ProbedAt: 1, Role: "router", OpenMgmt: []string{"ssh"}}},
		{Name: "ok", Caps: &Caps{ProbedAt: 1}},
	}}
	got, _ := ruleOpenMgmt(d)
	if len(got) != 2 || got[0].Severity != SevWarning || got[1].Severity != SevTip {
		t.Fatalf("open mgmt: %+v", got)
	}
	n, _ := ruleNTPOff(d)
	if len(n) != 1 || n[0].Subject != "ap1" {
		t.Fatalf("ntp: %+v", n)
	}
	if _, why := ruleNTPOff(&Data{}); why == "" {
		t.Fatal("no devices must be skipped")
	}
}

func TestAPOnAPRule(t *testing.T) {
	d := &Data{Devs: []Device{
		{Name: "ap1", Caps: &Caps{ProbedAt: 1, Role: "ap", APIOn: []string{"api"}}},
		{Name: "r1", Caps: &Caps{ProbedAt: 1, Role: "router", APIOn: []string{"api"}}},
		{Name: "ap2", Caps: &Caps{ProbedAt: 1, Role: "ap"}},
	}}
	got, _ := ruleAPOnAP(d)
	if len(got) != 1 || got[0].Subject != "ap1" || got[0].Severity != SevTip {
		t.Fatalf("got %+v", got)
	}
	if _, why := ruleAPOnAP(&Data{}); why == "" {
		t.Fatal("no devices must be skipped")
	}
}
