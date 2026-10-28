package poller

import "testing"

func TestScrubRowDropsSecretsVolatileAndDynamic(t *testing.T) {
	if _, ok := ScrubRow(Row{".id": "*1", "dynamic": "true"}); ok {
		t.Error("dynamic row kept")
	}
	r, ok := ScrubRow(Row{".id": "*2", "name": "wg0", "private-key": "xyz", "bytes": "5", "last-handshake": "1s", "preshared-key": "k", "disabled": "false"})
	if !ok || r["name"] != "wg0" || r["disabled"] != "false" {
		t.Fatalf("row: %v %v", r, ok)
	}
	for _, k := range []string{"private-key", "bytes", "last-handshake", "preshared-key"} {
		if _, has := r[k]; has {
			t.Errorf("%s survived", k)
		}
	}
}

func TestScrubRowInDropsRuntimeFieldsOfRealSections(t *testing.T) {
	cases := []struct {
		sec  string
		a, b Row
	}{
		{"/interface/bridge/port", Row{".id": "*1", "interface": "ether1", "pvid": "1", "debug-info": "a-b", "tx-bpdu": "5", "last-topology-change": "1s", "role": "root-port", "status": "in-bridge", "edge-port": "no"},
			Row{".id": "*1", "interface": "ether1", "pvid": "1", "debug-info": "c", "tx-bpdu": "9", "last-topology-change": "9s", "role": "designated-port", "status": "inactive", "edge-port": "yes"}},
		{"/system/ntp/client", Row{"enabled": "true", "freq-drift": "1", "system-offset": "2", "status": "synchronized", "synced-server": "x"},
			Row{"enabled": "true", "freq-drift": "3", "system-offset": "4", "status": "started", "synced-server": "y"}},
		{"/ip/pool", Row{".id": "*1", "name": "dhcp", "ranges": "10.0.0.2-10.0.0.9", "used": "3", "available": "5"},
			Row{".id": "*1", "name": "dhcp", "ranges": "10.0.0.2-10.0.0.9", "used": "4", "available": "4"}},
		{"/system/scheduler", Row{".id": "*1", "name": "pppoe", "interval": "1d", "next-run": "a", "run-count": "1"},
			Row{".id": "*1", "name": "pppoe", "interval": "1d", "next-run": "b", "run-count": "2"}},
	}
	for _, c := range cases {
		a, _ := ScrubRowIn(c.sec, c.a)
		b, _ := ScrubRowIn(c.sec, c.b)
		if len(a) != len(b) {
			t.Fatalf("%s: %v vs %v", c.sec, a, b)
		}
		for k, v := range a {
			if b[k] != v {
				t.Errorf("%s: %s differs: %q vs %q", c.sec, k, v, b[k])
			}
		}
	}
	// a real setting with the same name elsewhere stays
	if r, _ := ScrubRowIn("/ip/firewall/filter", Row{"status": "x", "used": "1"}); r["status"] != "x" || r["used"] != "1" {
		t.Errorf("setting dropped outside its section: %v", r)
	}
	if r, _ := ScrubRowIn("/interface/bridge/port", Row{"pvid": "10", "edge": "yes"}); r["pvid"] != "10" || r["edge"] != "yes" {
		t.Errorf("setting dropped: %v", r)
	}
}
