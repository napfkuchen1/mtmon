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
