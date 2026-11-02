// SPDX-License-Identifier: AGPL-3.0-only

// Package flow decodes NetFlow v9 and IPFIX datagrams (MikroTik Traffic-Flow).
package flow

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"
)

// Record is one decoded flow.
type Record struct {
	Exporter netip.Addr
	Src, Dst netip.Addr
	SrcPort  uint16
	DstPort  uint16
	Proto    uint8
	Bytes    uint64
	Packets  uint64
	InIf     uint32
	OutIf    uint32
	TCPFlags uint8
	Start    time.Time
	End      time.Time
}

type field struct {
	id     uint16
	length uint16 // 0xFFFF = variable (IPFIX)
	ent    uint32
}

type template struct {
	fields  []field
	fixed   int // total length if no variable fields, else -1
	options bool
}

type tkey struct {
	exp    netip.Addr
	domain uint32
	id     uint16
}

// Decoder keeps the per-exporter template cache. Safe for concurrent use.
type Decoder struct {
	mu    sync.RWMutex
	tmpl  map[tkey]*template
	order []tkey                       // insertion order, oldest first, for eviction
	now   func() time.Time             // test clock; nil = time.Now
	skew  map[netip.Addr]time.Duration // receive time minus the exporter header time, per exporter (#292)

	// Counters (read with atomic-free best effort; guarded by mu for writes).
	Packets, Records, Errors, NoTemplate uint64
	Panics                               uint64 // packets whose decoding panicked (recovered, dropped)
}

// Template cache bounds (#284): template ids are 16 bit and the observation domain 32 bit, so a spoofed or buggy
// exporter could otherwise grow the cache without limit. The oldest template is evicted first.
const (
	maxTemplatesPerDomain = 256
	maxTemplatesTotal     = 4096
)

func NewDecoder() *Decoder { return &Decoder{tmpl: make(map[tkey]*template)} }

var (
	ErrShort   = errors.New("flow: short packet")
	ErrVersion = errors.New("flow: unsupported version")
)

// Decode parses one UDP payload from exporter and returns the flow records.
func (d *Decoder) Decode(exp netip.Addr, b []byte) (recs []Record, err error) {
	defer func() {
		if p := recover(); p != nil { // unauthenticated input must never end the process
			d.mu.Lock()
			d.Panics++
			n := d.Panics
			d.Errors++
			d.mu.Unlock()
			if n <= 5 {
				slog.Error("flow decoder panic, packet dropped", "exporter", exp.String(), "panic", p, "len", len(b))
			}
			recs, err = nil, errors.New("flow: decoder panic")
		}
	}()
	if len(b) < 4 {
		d.bump(&d.Errors)
		return nil, ErrShort
	}
	d.bump(&d.Packets)
	switch binary.BigEndian.Uint16(b) {
	case 9:
		return d.decodeV9(exp, b)
	case 10:
		return d.decodeIPFIX(exp, b)
	default:
		d.bump(&d.Errors)
		return nil, ErrVersion
	}
}

// Skews reports, per exporter, how far its clock is behind (positive) or ahead (negative) of this host, measured
// from the export header time of the last packet. Flow times are corrected by it (#292).
func (d *Decoder) Skews() map[netip.Addr]time.Duration {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make(map[netip.Addr]time.Duration, len(d.skew))
	for k, v := range d.skew {
		out[k] = v
	}
	return out
}

// minSkew is the offset that is treated as real: the header carries whole seconds and the packet needs time to arrive.
const minSkew = 3 * time.Second

// correct moves the flow times of one packet from the router clock to this host's clock. A router without working
// NTP would otherwise book every flow minutes or hours off, and client-to-IP lookups by time would go wrong.
func (d *Decoder) correct(exp netip.Addr, export time.Time, recs []Record) {
	now := time.Now
	if d.now != nil {
		now = d.now
	}
	skew := now().Sub(export)
	d.mu.Lock()
	if d.skew == nil {
		d.skew = map[netip.Addr]time.Duration{}
	}
	if _, ok := d.skew[exp]; ok || len(d.skew) < 1000 {
		d.skew[exp] = skew
	}
	d.mu.Unlock()
	if skew > -minSkew && skew < minSkew {
		return
	}
	for i := range recs {
		recs[i].Start = recs[i].Start.Add(skew)
		recs[i].End = recs[i].End.Add(skew)
	}
}

func (d *Decoder) bump(c *uint64) { d.mu.Lock(); *c++; d.mu.Unlock() }

func (d *Decoder) decodeV9(exp netip.Addr, b []byte) ([]Record, error) {
	if len(b) < 20 {
		d.bump(&d.Errors)
		return nil, ErrShort
	}
	uptime := binary.BigEndian.Uint32(b[4:])
	secs := binary.BigEndian.Uint32(b[8:])
	domain := binary.BigEndian.Uint32(b[16:])
	ctx := timeCtx{export: time.Unix(int64(secs), 0), uptimeMs: uptime, v9: true}
	recs, err := d.sets(exp, domain, b[20:], ctx, false)
	d.correct(exp, ctx.export, recs)
	return recs, err
}

func (d *Decoder) decodeIPFIX(exp netip.Addr, b []byte) ([]Record, error) {
	if len(b) < 16 {
		d.bump(&d.Errors)
		return nil, ErrShort
	}
	l := int(binary.BigEndian.Uint16(b[2:]))
	if l > len(b) || l < 16 {
		d.bump(&d.Errors)
		return nil, ErrShort
	}
	secs := binary.BigEndian.Uint32(b[4:])
	domain := binary.BigEndian.Uint32(b[12:])
	ctx := timeCtx{export: time.Unix(int64(secs), 0)}
	recs, err := d.sets(exp, domain, b[16:l], ctx, true)
	d.correct(exp, ctx.export, recs)
	return recs, err
}

type timeCtx struct {
	export   time.Time
	uptimeMs uint32
	v9       bool
}

func (d *Decoder) sets(exp netip.Addr, domain uint32, b []byte, ctx timeCtx, ipfix bool) ([]Record, error) {
	var out []Record
	for len(b) >= 4 {
		id := binary.BigEndian.Uint16(b)
		l := int(binary.BigEndian.Uint16(b[2:]))
		if l < 4 || l > len(b) {
			d.bump(&d.Errors)
			return out, fmt.Errorf("flow: bad set length %d", l)
		}
		body := b[4:l]
		b = b[l:]
		tplSet, optSet := uint16(0), uint16(1)
		if ipfix {
			tplSet, optSet = 2, 3
		}
		switch {
		case id == tplSet:
			d.parseTemplates(exp, domain, body, ipfix, false)
		case id == optSet:
			d.parseTemplates(exp, domain, body, ipfix, true)
		case id >= 256:
			d.mu.RLock()
			t := d.tmpl[tkey{exp, domain, id}]
			d.mu.RUnlock()
			if t == nil {
				d.bump(&d.NoTemplate)
				continue
			}
			if t.options {
				continue
			}
			recs := d.parseData(exp, t, body, ctx)
			out = append(out, recs...)
		}
	}
	d.mu.Lock()
	d.Records += uint64(len(out))
	d.mu.Unlock()
	return out, nil
}

func (d *Decoder) parseTemplates(exp netip.Addr, domain uint32, b []byte, ipfix, options bool) {
	for len(b) >= 4 {
		tid := binary.BigEndian.Uint16(b)
		fc := int(binary.BigEndian.Uint16(b[2:]))
		b = b[4:]
		scopeCnt := 0
		if options {
			if ipfix { // template id, field count, scope field count
				if len(b) < 2 {
					return
				}
				scopeCnt = int(binary.BigEndian.Uint16(b))
				b = b[2:]
			} else { // v9: scope length(bytes), option length(bytes)
				// v9 option template: id, optScopeLen, optLen
				scopeLen := fc
				if len(b) < 2 {
					return
				}
				optLen := int(binary.BigEndian.Uint16(b))
				b = b[2:]
				skip := scopeLen + optLen
				if skip > len(b) {
					return
				}
				b = b[skip:]
				d.store(exp, domain, tid, &template{options: true, fixed: -1})
				continue
			}
		}
		_ = scopeCnt
		t := &template{options: options}
		for i := 0; i < fc; i++ {
			if len(b) < 4 {
				return
			}
			f := field{id: binary.BigEndian.Uint16(b), length: binary.BigEndian.Uint16(b[2:])}
			b = b[4:]
			if ipfix && f.id&0x8000 != 0 {
				f.id &^= 0x8000
				if len(b) < 4 {
					return
				}
				f.ent = binary.BigEndian.Uint32(b)
				b = b[4:]
			}
			t.fields = append(t.fields, f)
		}
		t.fixed = 0
		for _, f := range t.fields {
			if f.length == 0xFFFF {
				t.fixed = -1
				break
			}
			t.fixed += int(f.length)
		}
		d.store(exp, domain, tid, t)
		// v9 sets may be padded to 4 bytes; stop on zero padding.
		if len(b) < 4 {
			return
		}
	}
}

func (d *Decoder) store(exp netip.Addr, domain uint32, id uint16, t *template) {
	k := tkey{exp, domain, id}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.tmpl[k]; ok {
		d.tmpl[k] = t // refresh in place
		return
	}
	per := 0
	for _, o := range d.order {
		if o.exp == exp && o.domain == domain {
			per++
		}
	}
	for per >= maxTemplatesPerDomain { // evict this domain's oldest
		for i, o := range d.order {
			if o.exp == exp && o.domain == domain {
				d.evict(i)
				per--
				break
			}
		}
	}
	for len(d.order) >= maxTemplatesTotal {
		d.evict(0)
	}
	d.tmpl[k] = t
	d.order = append(d.order, k)
}

// evict removes d.order[i] from the cache; the caller holds d.mu.
func (d *Decoder) evict(i int) {
	delete(d.tmpl, d.order[i])
	d.order = append(d.order[:i], d.order[i+1:]...)
}

// templateCount is the number of cached templates (tests).
func (d *Decoder) templateCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.tmpl)
}

func uintOf(b []byte) uint64 {
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	return v
}

func (d *Decoder) parseData(exp netip.Addr, t *template, b []byte, ctx timeCtx) []Record {
	var out []Record
	for len(b) > 0 {
		if t.fixed > 0 && len(b) < t.fixed {
			break // padding
		}
		if t.fixed == 0 {
			break
		}
		r := Record{Exporter: exp, Start: ctx.export, End: ctx.export}
		var first, last uint32
		var haveFL bool
		var startMs, endMs, sysInitMs uint64
		pos := 0
		ok := true
		for _, f := range t.fields {
			n := int(f.length)
			if f.length == 0xFFFF {
				if pos >= len(b) {
					ok = false
					break
				}
				n = int(b[pos])
				pos++
				if n == 255 {
					if pos+2 > len(b) {
						ok = false
						break
					}
					n = int(binary.BigEndian.Uint16(b[pos:]))
					pos += 2
				}
			}
			if pos+n > len(b) {
				ok = false
				break
			}
			v := b[pos : pos+n]
			pos += n
			if f.ent != 0 {
				continue
			}
			switch f.id {
			case 8: // sourceIPv4Address
				if a, ok2 := netip.AddrFromSlice(v); ok2 && n == 4 {
					r.Src = a
				}
			case 12:
				if a, ok2 := netip.AddrFromSlice(v); ok2 && n == 4 {
					r.Dst = a
				}
			case 27:
				if a, ok2 := netip.AddrFromSlice(v); ok2 && n == 16 {
					r.Src = a
				}
			case 28:
				if a, ok2 := netip.AddrFromSlice(v); ok2 && n == 16 {
					r.Dst = a
				}
			case 7:
				r.SrcPort = uint16(uintOf(v))
			case 11:
				r.DstPort = uint16(uintOf(v))
			case 4:
				r.Proto = uint8(uintOf(v))
			case 1, 85:
				r.Bytes = uintOf(v)
			case 2, 86:
				r.Packets = uintOf(v)
			case 10:
				r.InIf = uint32(uintOf(v))
			case 14:
				r.OutIf = uint32(uintOf(v))
			case 6:
				r.TCPFlags = uint8(uintOf(v))
			case 21:
				last = uint32(uintOf(v))
				haveFL = true
			case 22:
				first = uint32(uintOf(v))
				haveFL = true
			case 160: // systemInitTimeMilliseconds: the base of 21/22 in IPFIX
				sysInitMs = uintOf(v)
			case 152:
				startMs = uintOf(v)
			case 153:
				endMs = uintOf(v)
			}
		}
		if !ok {
			break
		}
		b = b[pos:]
		if ctx.v9 && haveFL {
			r.Start = ctx.export.Add(-time.Duration(int64(ctx.uptimeMs)-int64(first)) * time.Millisecond)
			r.End = ctx.export.Add(-time.Duration(int64(ctx.uptimeMs)-int64(last)) * time.Millisecond)
		}
		if !ctx.v9 && haveFL && sysInitMs > 0 {
			r.Start = time.UnixMilli(int64(sysInitMs) + int64(first))
			r.End = time.UnixMilli(int64(sysInitMs) + int64(last))
		}
		if startMs > 0 {
			r.Start = time.UnixMilli(int64(startMs))
		}
		if endMs > 0 {
			r.End = time.UnixMilli(int64(endMs))
		}
		if r.Src.IsValid() && r.Dst.IsValid() {
			out = append(out, r)
		}
		if t.fixed > 0 && len(b) < t.fixed {
			break
		}
	}
	return out
}

// Stats returns the decoder counters (safe for concurrent use).
func (d *Decoder) Stats() (packets, records, errs, noTemplate uint64) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.Packets, d.Records, d.Errors, d.NoTemplate
}
