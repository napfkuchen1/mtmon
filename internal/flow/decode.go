// Package flow decodes NetFlow v9 and IPFIX datagrams (MikroTik Traffic-Flow).
package flow

import (
	"encoding/binary"
	"errors"
	"fmt"
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
	mu   sync.RWMutex
	tmpl map[tkey]*template

	// Counters (read with atomic-free best effort; guarded by mu for writes).
	Packets, Records, Errors, NoTemplate uint64
}

func NewDecoder() *Decoder { return &Decoder{tmpl: make(map[tkey]*template)} }

var (
	ErrShort   = errors.New("flow: short packet")
	ErrVersion = errors.New("flow: unsupported version")
)

// Decode parses one UDP payload from exporter and returns the flow records.
func (d *Decoder) Decode(exp netip.Addr, b []byte) ([]Record, error) {
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
	return d.sets(exp, domain, b[20:], ctx, false)
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
	return d.sets(exp, domain, b[16:l], ctx, true)
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
	d.mu.Lock()
	d.tmpl[tkey{exp, domain, id}] = t
	d.mu.Unlock()
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
		var startMs, endMs uint64
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
