// Package live keeps realtime state (IP↔MAC map, rates, recent connections) and the flow pipeline.
package live

import (
	"fmt"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

const rateWindow = 30 * time.Second

type ev struct {
	t        time.Time
	up, down uint64
}

type clientLive struct {
	events []ev
	lastC  time.Time
}

// LiveConn is one entry of the live connection tail.
type LiveConn struct {
	Seq    uint64 `json:"seq"`
	TS     int64  `json:"ts"`
	Client string `json:"client"`
	Name   string `json:"name"`
	RIP    string `json:"rip"`
	RPort  uint16 `json:"rport"`
	Proto  uint8  `json:"proto"`
	Dir    string `json:"dir"`
	Bytes  uint64 `json:"bytes"`
	Host   string `json:"host"`
	CC     string `json:"cc"`
	Svc    string `json:"svc"`
}

type IfRate struct {
	Device  string  `json:"device"`
	Iface   string  `json:"iface"`
	RxBps   float64 `json:"rx_bps"`
	TxBps   float64 `json:"tx_bps"`
	Running bool    `json:"running"`
	Type    string  `json:"type"`
}

type DevLive struct {
	Name    string  `json:"name"`
	Up      bool    `json:"up"`
	CPU     float64 `json:"cpu"`
	MemPct  float64 `json:"mem_pct"`
	Temp    float64 `json:"temp"`
	Uptime  int64   `json:"uptime"`
	Clients int     `json:"clients"`
	LastOK  int64   `json:"last_ok"`
	Err     string  `json:"err,omitempty"`
}

type Hub struct {
	mu       sync.Mutex
	clients  map[string]*clientLive
	conns    []LiveConn
	seq      uint64
	ifaces   map[string]IfRate // device|iface
	devices  map[string]DevLive
	ipmac    map[string]string // ip → mac
	names    map[string]string // mac → display name
	totalUp  []ev
	FlowsIn  uint64
	FlowsPS  float64
	lastCnt  uint64
	lastCntT time.Time
}

func NewHub() *Hub {
	return &Hub{clients: map[string]*clientLive{}, ifaces: map[string]IfRate{}, devices: map[string]DevLive{},
		ipmac: map[string]string{}, names: map[string]string{}}
}

// SetIPMap replaces the IP→MAC mapping (from DHCP/ARP/WiFi) and display names.
func (h *Hub) SetIPMap(m map[string]string, names map[string]string) {
	h.mu.Lock()
	for ip, mac := range m {
		h.ipmac[ip] = mac
	}
	for mac, n := range names {
		h.names[mac] = n
	}
	h.mu.Unlock()
}

func (h *Hub) MACFor(ip string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if m := h.ipmac[ip]; m != "" {
		return m
	}
	// SLAAC address built from the MAC (EUI-64): belongs to that client if we know it.
	if m := eui64MAC(ip); m != "" {
		if _, ok := h.names[m]; ok {
			return m
		}
	}
	return ""
}

// eui64MAC returns the MAC embedded in an EUI-64 IPv6 address (fe80::9a22:efff:fe7a:7927 -> 98:22:EF:7A:79:27), or "".
func eui64MAC(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is6() || a.Is4In6() {
		return ""
	}
	b := a.As16()
	if b[11] != 0xff || b[12] != 0xfe {
		return ""
	}
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", b[8]^0x02, b[9], b[10], b[13], b[14], b[15])
}

func (h *Hub) SetName(mac, name string) { h.mu.Lock(); h.names[mac] = name; h.mu.Unlock() }

func (h *Hub) SetDevice(d DevLive) { h.mu.Lock(); h.devices[d.Name] = d; h.mu.Unlock() }

// RemoveDevice forgets live state of a device that was removed.
func (h *Hub) RemoveDevice(name string) {
	h.mu.Lock()
	delete(h.devices, name)
	for k, v := range h.ifaces {
		if v.Device == name {
			delete(h.ifaces, k)
		}
	}
	h.mu.Unlock()
}

func (h *Hub) SetIfaces(device string, rs []IfRate) {
	h.mu.Lock()
	for k, v := range h.ifaces {
		if v.Device == device {
			delete(h.ifaces, k)
		}
	}
	for _, r := range rs {
		h.ifaces[device+"|"+r.Iface] = r
	}
	h.mu.Unlock()
}

// Devices / Ifaces snapshots for the API.
func (h *Hub) Devices() []DevLive {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]DevLive, 0, len(h.devices))
	for _, d := range h.devices {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (h *Hub) Ifaces(device string) []IfRate {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []IfRate
	for _, r := range h.ifaces {
		if device == "" || r.Device == device {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device+out[i].Iface < out[j].Device+out[j].Iface })
	return out
}

// AddFlow records a stored flow row into the live structures.
func (h *Hub) AddFlow(r store.FlowRow) {
	now := time.Now()
	key := r.MAC
	if key == "" {
		key = r.CIP
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.FlowsIn++
	c := h.clients[key]
	if c == nil {
		c = &clientLive{}
		h.clients[key] = c
	}
	c.lastC = now
	e := ev{t: now}
	switch r.Dir {
	case "u":
		e.up = r.Bytes
		h.totalUp = append(h.totalUp, ev{t: now, up: r.Bytes})
	case "d":
		e.down = r.Bytes
		h.totalUp = append(h.totalUp, ev{t: now, down: r.Bytes})
	}
	c.events = append(c.events, e)
	h.seq++
	name := h.names[key]
	if name == "" {
		name = key
	}
	h.conns = append(h.conns, LiveConn{Seq: h.seq, TS: r.TS, Client: key, Name: name, RIP: r.RIP, RPort: r.RPort,
		Proto: r.Proto, Dir: r.Dir, Bytes: r.Bytes, Host: r.Host, CC: r.CC, Svc: r.Svc})
	if len(h.conns) > 3000 {
		h.conns = append([]LiveConn(nil), h.conns[len(h.conns)-2000:]...)
	}
}

type ClientRate struct {
	Key     string  `json:"key"`
	Name    string  `json:"name"`
	UpBps   float64 `json:"up_bps"`
	DownBps float64 `json:"down_bps"`
}

// Snapshot is what the WebSocket pushes every second.
type Snapshot struct {
	TS      int64        `json:"ts"`
	UpBps   float64      `json:"up_bps"`
	DownBps float64      `json:"down_bps"`
	Clients []ClientRate `json:"clients"`
	Conns   []LiveConn   `json:"conns"`
	Devices []DevLive    `json:"devices"`
	Ifaces  []IfRate     `json:"ifaces"`
	FlowsPS float64      `json:"flows_ps"`
}

func sumWin(evs []ev, cut time.Time) (up, down uint64, kept []ev) {
	i := 0
	for i < len(evs) && evs[i].t.Before(cut) {
		i++
	}
	kept = evs[i:]
	for _, e := range kept {
		up += e.up
		down += e.down
	}
	return
}

// Snapshot computes current rates. sinceSeq returns only connections newer than that sequence.
func (h *Hub) Snapshot(sinceSeq uint64, topN int) Snapshot {
	now := time.Now()
	cut := now.Add(-rateWindow)
	h.mu.Lock()
	defer h.mu.Unlock()
	s := Snapshot{TS: now.Unix()}
	w := rateWindow.Seconds()
	for k, c := range h.clients {
		up, down, kept := sumWin(c.events, cut)
		c.events = kept
		if len(kept) == 0 && now.Sub(c.lastC) > 5*time.Minute {
			delete(h.clients, k)
			continue
		}
		if up+down == 0 {
			continue
		}
		name := h.names[k]
		if name == "" {
			name = k
		}
		s.Clients = append(s.Clients, ClientRate{Key: k, Name: name, UpBps: float64(up) * 8 / w, DownBps: float64(down) * 8 / w})
	}
	tu, td, kept := sumWin(h.totalUp, cut)
	h.totalUp = kept
	s.UpBps, s.DownBps = float64(tu)*8/w, float64(td)*8/w
	sort.Slice(s.Clients, func(i, j int) bool {
		return s.Clients[i].UpBps+s.Clients[i].DownBps > s.Clients[j].UpBps+s.Clients[j].DownBps
	})
	if len(s.Clients) > topN {
		s.Clients = s.Clients[:topN]
	}
	for i := len(h.conns) - 1; i >= 0 && h.conns[i].Seq > sinceSeq && len(s.Conns) < 100; i-- {
		s.Conns = append(s.Conns, h.conns[i])
	}
	if h.lastCntT.IsZero() {
		h.lastCntT, h.lastCnt = now, h.FlowsIn
	} else if d := now.Sub(h.lastCntT).Seconds(); d >= 5 {
		h.FlowsPS = float64(h.FlowsIn-h.lastCnt) / d
		h.lastCntT, h.lastCnt = now, h.FlowsIn
	}
	s.FlowsPS = h.FlowsPS
	for _, d := range h.devices {
		s.Devices = append(s.Devices, d)
	}
	sort.Slice(s.Devices, func(i, j int) bool { return s.Devices[i].Name < s.Devices[j].Name })
	for _, r := range h.ifaces {
		if r.Running && (r.RxBps+r.TxBps) > 0 {
			s.Ifaces = append(s.Ifaces, r)
		}
	}
	sort.Slice(s.Ifaces, func(i, j int) bool { return s.Ifaces[i].RxBps+s.Ifaces[i].TxBps > s.Ifaces[j].RxBps+s.Ifaces[j].TxBps })
	if len(s.Ifaces) > 12 {
		s.Ifaces = s.Ifaces[:12]
	}
	return s
}

// LastSeq returns the newest connection sequence number.
func (h *Hub) LastSeq() uint64 { h.mu.Lock(); defer h.mu.Unlock(); return h.seq }

// FlowRate returns the flows/s estimate (updated by Snapshot).
func (h *Hub) FlowRate() float64 { h.mu.Lock(); defer h.mu.Unlock(); return h.FlowsPS }
