package flow

import (
	"bytes"
	"encoding/binary"
	"time"
)

// Encoder builds synthetic IPFIX / NetFlow v9 packets (used by flowgen and tests).
type Encoder struct {
	IPFIX  bool
	Domain uint32
	seq    uint32
	start  time.Time
}

const tplID = 256

var tplFields = [][2]uint16{
	{8, 4}, {12, 4}, {7, 2}, {11, 2}, {4, 1}, {1, 8}, {2, 8}, {10, 4}, {14, 4}, {6, 1},
}

func NewEncoder(ipfix bool) *Encoder {
	return &Encoder{IPFIX: ipfix, start: time.Now().Add(-time.Hour)}
}

func (e *Encoder) header(buf *bytes.Buffer, count int, now time.Time) {
	if e.IPFIX {
		binary.Write(buf, binary.BigEndian, uint16(10))
		binary.Write(buf, binary.BigEndian, uint16(0)) // length patched later
		binary.Write(buf, binary.BigEndian, uint32(now.Unix()))
		binary.Write(buf, binary.BigEndian, e.seq)
		binary.Write(buf, binary.BigEndian, e.Domain)
		return
	}
	binary.Write(buf, binary.BigEndian, uint16(9))
	binary.Write(buf, binary.BigEndian, uint16(count))
	binary.Write(buf, binary.BigEndian, uint32(now.Sub(e.start).Milliseconds()))
	binary.Write(buf, binary.BigEndian, uint32(now.Unix()))
	binary.Write(buf, binary.BigEndian, e.seq)
	binary.Write(buf, binary.BigEndian, e.Domain)
}

// Template returns a packet containing only the template set.
func (e *Encoder) Template(now time.Time) []byte {
	var b bytes.Buffer
	e.header(&b, 1, now)
	setID := uint16(0)
	if e.IPFIX {
		setID = 2
	}
	body := new(bytes.Buffer)
	binary.Write(body, binary.BigEndian, uint16(tplID))
	binary.Write(body, binary.BigEndian, uint16(len(tplFields)))
	for _, f := range tplFields {
		binary.Write(body, binary.BigEndian, f[0])
		binary.Write(body, binary.BigEndian, f[1])
	}
	binary.Write(&b, binary.BigEndian, setID)
	binary.Write(&b, binary.BigEndian, uint16(4+body.Len()))
	b.Write(body.Bytes())
	return e.finish(b.Bytes())
}

// Data returns a packet with the given records (template must be sent before).
func (e *Encoder) Data(now time.Time, recs []Record) []byte {
	var b bytes.Buffer
	e.header(&b, len(recs), now)
	body := new(bytes.Buffer)
	for _, r := range recs {
		s, d := r.Src.As4(), r.Dst.As4()
		body.Write(s[:])
		body.Write(d[:])
		binary.Write(body, binary.BigEndian, r.SrcPort)
		binary.Write(body, binary.BigEndian, r.DstPort)
		body.WriteByte(r.Proto)
		binary.Write(body, binary.BigEndian, r.Bytes)
		binary.Write(body, binary.BigEndian, r.Packets)
		binary.Write(body, binary.BigEndian, r.InIf)
		binary.Write(body, binary.BigEndian, r.OutIf)
		body.WriteByte(r.TCPFlags)
	}
	binary.Write(&b, binary.BigEndian, uint16(tplID))
	binary.Write(&b, binary.BigEndian, uint16(4+body.Len()))
	b.Write(body.Bytes())
	e.seq += uint32(len(recs))
	return e.finish(b.Bytes())
}

func (e *Encoder) finish(p []byte) []byte {
	if e.IPFIX {
		binary.BigEndian.PutUint16(p[2:], uint16(len(p)))
	}
	return p
}
