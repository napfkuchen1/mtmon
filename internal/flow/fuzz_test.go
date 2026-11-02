// SPDX-License-Identifier: AGPL-3.0-only

package flow

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"
)

// FuzzDecode feeds arbitrary datagrams to the decoder; the only requirement is that it neither panics nor hangs.
func FuzzDecode(f *testing.F) {
	now := time.Now()
	for _, ipfix := range []bool{true, false} {
		e := NewEncoder(ipfix)
		f.Add(e.Template(now))
		f.Add(e.Data(now, sample()))
	}
	f.Add([]byte{0, 10, 0, 16, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{0, 9, 0, 1})
	exp := netip.MustParseAddr("10.0.0.1")
	f.Fuzz(func(t *testing.T, b []byte) {
		d := NewDecoder()
		d.Decode(exp, b)
		if d.Panics != 0 {
			t.Fatalf("decoder panicked on %x", b)
		}
	})
}

// #284: template ids and domains from one exporter cannot grow the cache without bound; the newest survive.
func TestTemplateCacheBounded(t *testing.T) {
	d := NewDecoder()
	exp := netip.MustParseAddr("10.0.0.1")
	for id := 0; id < 1000; id++ {
		d.store(exp, 1, uint16(id), &template{})
	}
	if n := d.templateCount(); n != maxTemplatesPerDomain {
		t.Fatalf("per domain: %d templates, want %d", n, maxTemplatesPerDomain)
	}
	if d.tmpl[tkey{exp, 1, 999}] == nil || d.tmpl[tkey{exp, 1, 0}] != nil {
		t.Error("oldest must be evicted, newest kept")
	}
	for dom := uint32(2); dom < 100; dom++ {
		for id := 0; id < 100; id++ {
			d.store(exp, dom, uint16(id), &template{})
		}
	}
	if n := d.templateCount(); n > maxTemplatesTotal {
		t.Fatalf("total: %d templates, want <= %d", n, maxTemplatesTotal)
	}
	d.store(exp, 1, 999, &template{}) // a refresh does not grow or evict
	if d.templateCount() > maxTemplatesTotal {
		t.Error("refresh grew the cache")
	}
}

// A short or hostile header must be an error, not a crash.
func TestDecodeHostileHeaders(t *testing.T) {
	d := NewDecoder()
	exp := netip.MustParseAddr("10.0.0.1")
	for _, n := range []int{0, 1, 3, 4, 8, 16, 20, 24} {
		for _, v := range []uint16{9, 10} {
			b := make([]byte, n)
			if n >= 2 {
				binary.BigEndian.PutUint16(b, v)
			}
			d.Decode(exp, b)
		}
	}
	if d.Panics != 0 {
		t.Errorf("panics: %d", d.Panics)
	}
}
