package flow

import (
	"net/netip"
	"testing"
	"time"
)

func sample() []Record {
	return []Record{
		{Src: netip.MustParseAddr("192.168.88.23"), Dst: netip.MustParseAddr("1.1.1.1"), SrcPort: 51000, DstPort: 443, Proto: 6, Bytes: 12345, Packets: 20, InIf: 2, OutIf: 1, TCPFlags: 0x18},
		{Src: netip.MustParseAddr("8.8.8.8"), Dst: netip.MustParseAddr("192.168.88.23"), SrcPort: 53, DstPort: 40000, Proto: 17, Bytes: 99, Packets: 1, InIf: 1, OutIf: 2},
	}
}

func roundtrip(t *testing.T, ipfix bool) {
	e := NewEncoder(ipfix)
	d := NewDecoder()
	exp := netip.MustParseAddr("10.0.0.1")
	now := time.Now()
	// data before template must not crash and yields nothing
	if r, _ := d.Decode(exp, e.Data(now, sample())); len(r) != 0 {
		t.Fatalf("expected 0 records without template, got %d", len(r))
	}
	if d.NoTemplate == 0 {
		t.Fatal("NoTemplate counter not bumped")
	}
	if r, err := d.Decode(exp, e.Template(now)); err != nil || len(r) != 0 {
		t.Fatalf("template: %v %d", err, len(r))
	}
	recs, err := d.Decode(exp, e.Data(now, sample()))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 records got %d", len(recs))
	}
	got, want := recs[0], sample()[0]
	if got.Src != want.Src || got.Dst != want.Dst || got.DstPort != 443 || got.Bytes != 12345 || got.Proto != 6 || got.InIf != 2 || got.TCPFlags != 0x18 {
		t.Fatalf("mismatch: %+v", got)
	}
}

func TestIPFIX(t *testing.T) { roundtrip(t, true) }
func TestV9(t *testing.T)    { roundtrip(t, false) }

func TestGarbage(t *testing.T) {
	d := NewDecoder()
	exp := netip.MustParseAddr("10.0.0.1")
	for _, p := range [][]byte{nil, {0}, {0, 9}, {0, 10, 0, 4}, {0, 5, 1, 2, 3, 4, 5, 6}, make([]byte, 30)} {
		d.Decode(exp, p) // must not panic
	}
	// truncated valid packet
	e := NewEncoder(true)
	p := e.Template(time.Now())
	for i := 0; i < len(p); i++ {
		d.Decode(exp, p[:i])
	}
}

func TestTemplatePerExporter(t *testing.T) {
	e := NewEncoder(true)
	d := NewDecoder()
	a, b := netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.0.0.2")
	d.Decode(a, e.Template(time.Now()))
	if r, _ := d.Decode(b, e.Data(time.Now(), sample())); len(r) != 0 {
		t.Fatal("template leaked across exporters")
	}
	if r, _ := d.Decode(a, e.Data(time.Now(), sample())); len(r) != 2 {
		t.Fatal("exporter a failed")
	}
}

func BenchmarkDecode(b *testing.B) {
	e := NewEncoder(true)
	d := NewDecoder()
	exp := netip.MustParseAddr("10.0.0.1")
	d.Decode(exp, e.Template(time.Now()))
	recs := make([]Record, 20)
	for i := range recs {
		recs[i] = sample()[0]
	}
	p := e.Data(time.Now(), recs)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.Decode(exp, p)
	}
}
