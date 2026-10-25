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
