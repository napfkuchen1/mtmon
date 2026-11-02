// SPDX-License-Identifier: AGPL-3.0-only

package flow

import (
	"encoding/binary"
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

// A router clock that is 40 minutes behind must not shift the flows: times follow the receive time (#292).
func TestRouterClockSkewIsCorrected(t *testing.T) {
	for _, ipfix := range []bool{true, false} {
		e := NewEncoder(ipfix)
		d := NewDecoder()
		exp := netip.MustParseAddr("10.0.0.1")
		now := time.Now()
		d.now = func() time.Time { return now }
		behind := now.Add(-40 * time.Minute)
		d.Decode(exp, e.Template(behind))
		recs, err := d.Decode(exp, e.Data(behind, sample()))
		if err != nil || len(recs) != 2 {
			t.Fatalf("ipfix=%v: %v %d", ipfix, err, len(recs))
		}
		if dt := now.Sub(recs[0].End); dt < -2*time.Second || dt > 2*time.Second {
			t.Errorf("ipfix=%v: end is %v off from receive time", ipfix, dt)
		}
		if s := d.Skews()[exp]; s < 39*time.Minute || s > 41*time.Minute {
			t.Errorf("ipfix=%v: skew %v", ipfix, s)
		}
		// a correct clock is left alone
		recs, _ = d.Decode(exp, e.Data(now.Add(-time.Second), sample()))
		if dt := now.Sub(recs[0].End); dt < 0 || dt > 3*time.Second {
			t.Errorf("ipfix=%v: correct clock moved by %v", ipfix, dt)
		}
	}
}

func ipfixMsg(export time.Time, sets ...[]byte) []byte {
	var body []byte
	for _, s := range sets {
		body = append(body, s...)
	}
	h := make([]byte, 16)
	binary.BigEndian.PutUint16(h, 10)
	binary.BigEndian.PutUint16(h[2:], uint16(16+len(body)))
	binary.BigEndian.PutUint32(h[4:], uint32(export.Unix()))
	return append(h, body...)
}

func ipfixSet(id uint16, body []byte) []byte {
	h := make([]byte, 4)
	binary.BigEndian.PutUint16(h, id)
	binary.BigEndian.PutUint16(h[2:], uint16(4+len(body)))
	return append(h, body...)
}

func u16(v ...uint16) []byte {
	var b []byte
	for _, x := range v {
		b = binary.BigEndian.AppendUint16(b, x)
	}
	return b
}

// flowTemplate: src, dst, sport, dport, proto and one 8-byte counter (field cnt: 1 delta or 85 total).
func flowTemplate(tid, cnt uint16) []byte {
	return ipfixSet(2, u16(tid, 6, 8, 4, 12, 4, 7, 2, 11, 2, 4, 1, cnt, 8))
}

func flowData(tid uint16, bytes uint64) []byte {
	b := []byte{192, 168, 0, 5, 1, 1, 1, 1}
	b = binary.BigEndian.AppendUint16(b, 40000)
	b = binary.BigEndian.AppendUint16(b, 443)
	b = append(b, 6)
	b = binary.BigEndian.AppendUint64(b, bytes)
	return ipfixSet(tid, b)
}

// Packet sampling announced in options data scales the volumes (#293).
func TestSamplingRateScalesVolumes(t *testing.T) {
	d := NewDecoder()
	exp := netip.MustParseAddr("10.0.0.1")
	now := time.Now()
	// options template 257: scope observationDomainId (149), samplingPacketInterval (305), samplingPacketSpace (306)
	opt := ipfixSet(3, append(u16(257, 3, 1, 149, 4, 305, 4, 306, 4)))
	d.Decode(exp, ipfixMsg(now, flowTemplate(256, 1), opt))
	rec := binary.BigEndian.AppendUint32(nil, 0)
	rec = binary.BigEndian.AppendUint32(rec, 1)
	rec = binary.BigEndian.AppendUint32(rec, 99) // keep 1, skip 99: 1 out of 100
	d.Decode(exp, ipfixMsg(now, ipfixSet(257, rec)))
	recs, err := d.Decode(exp, ipfixMsg(now, flowData(256, 1500)))
	if err != nil || len(recs) != 1 {
		t.Fatalf("%v %d", err, len(recs))
	}
	if recs[0].Bytes != 150000 {
		t.Errorf("bytes %d, want 150000", recs[0].Bytes)
	}
	if d.Sampling()[exp] != 100 {
		t.Errorf("sampling %v", d.Sampling())
	}
	// an exporter without options data stays unscaled
	other := netip.MustParseAddr("10.0.0.2")
	d.Decode(other, ipfixMsg(now, flowTemplate(256, 1)))
	if r, _ := d.Decode(other, ipfixMsg(now, flowData(256, 1500))); len(r) != 1 || r[0].Bytes != 1500 {
		t.Errorf("unsampled exporter: %+v", r)
	}
}

// An exporter that sends running totals (field 85) is counted by delta, not once per export (#293).
func TestTotalCountersBecomeDeltas(t *testing.T) {
	d := NewDecoder()
	exp := netip.MustParseAddr("10.0.0.1")
	now := time.Now()
	d.Decode(exp, ipfixMsg(now, flowTemplate(256, 85)))
	var got []uint64
	for _, total := range []uint64{1000, 1500, 1600} {
		r, _ := d.Decode(exp, ipfixMsg(now, flowData(256, total)))
		if len(r) != 1 {
			t.Fatalf("records %d", len(r))
		}
		got = append(got, r[0].Bytes)
	}
	if got[0] != 1000 || got[1] != 500 || got[2] != 100 {
		t.Errorf("deltas %v", got)
	}
	if d.TotalsConverted != 3 {
		t.Errorf("counter %d", d.TotalsConverted)
	}
}
