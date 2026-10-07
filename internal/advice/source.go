package advice

import (
	"encoding/json"
	"net/netip"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/enrich"
	"github.com/napfkuchen1/mtmon/internal/live"
	"github.com/napfkuchen1/mtmon/internal/provision"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// Live builds a Snapshot from the running system. Every query is bounded (rollup range scans, small
// aggregates); results are memoised for the lifetime of one evaluation.
type Live struct {
	St      *store.Store
	Cfg     *config.Config
	Hub     *live.Hub
	En      *enrich.Enricher
	Started time.Time
	// ExporterLast returns the time of the last flow packet from an exporter address (zero = never).
	// ok=false when no collector is running (tests).
	ExporterLast func(addr string) (last time.Time, ok bool)
	// IsSelf reports whether a MAC belongs to a managed router/AP itself; its own traffic is no client behaviour.
	IsSelf func(mac string) bool

	now     time.Time
	devs    []Device
	devsOK  bool
	cls     []Client
	clsOK   bool
	tr      []ClientTraffic
	trOK    bool
	ports   []PortUse
	portsOK bool
}

func NewLive(st *store.Store, cfg *config.Config, hub *live.Hub, en *enrich.Enricher, started time.Time) *Live {
	return &Live{St: st, Cfg: cfg, Hub: hub, En: en, Started: started, now: time.Now()}
}

func (l *Live) Now() time.Time               { return l.now }
func (l *Live) ProcessUptime() time.Duration { return l.now.Sub(l.Started) }

func (l *Live) Devices() []Device {
	if l.devsOK {
		return l.devs
	}
	l.devsOK = true
	states, _ := l.St.DeviceStates()
	stats, _ := l.St.DevMetricStats(l.now.Add(-MetricWindowHr*time.Hour).Unix(), CPUHigh, MemHigh)
	livev := map[string]live.DevLive{}
	if l.Hub != nil {
		for _, d := range l.Hub.Devices() {
			livev[d.Name] = d
		}
	}
	caps := map[string]json.RawMessage{}
	if rows, err := l.St.DeviceRows(); err == nil {
		for _, r := range rows {
			caps[r.Name] = r.Caps
		}
	}
	fwDev := map[string]bool{}
	if rs, err := l.St.FwRules(); err == nil {
		for _, r := range rs {
			fwDev[r.Device] = true
		}
	}
	for _, c := range l.Cfg.AllDevices() {
		st := states[c.Name]
		lv := livev[c.Name]
		d := Device{Name: c.Name, Addr: c.Addr, Role: c.Role, Model: st.Model, Version: st.Version, Managed: c.Managed,
			Up: lv.Up, LiveOK: lv.Up && l.now.Unix()-lv.LastOK < 180, CPU: lv.CPU, Mem: lv.MemPct, FwLogging: fwDev[c.Name]}
		if m, ok := stats[c.Name]; ok && m.N > 0 {
			d.Samples, d.AvgCPU, d.MaxCPU, d.AvgMem, d.MaxMem = m.N, m.AvgCPU, m.MaxCPU, m.AvgMem, m.MaxMem
			d.HighCPUFrac = float64(m.HighCPU) / float64(m.N)
			d.HighMemFrac = float64(m.HighMem) / float64(m.N)
		}
		var info struct{ Firmware, Upgrade string }
		if json.Unmarshal(st.Info, &info) == nil {
			d.Firmware, d.FirmwareUpg = info.Firmware, info.Upgrade
		}
		if l.ExporterLast != nil {
			addrs := append([]string{c.Addr}, c.FlowSources...)
			for _, a := range addrs {
				if t, ok := l.ExporterLast(a); ok {
					d.FlowKnown = true
					if t.Unix() > d.FlowLast {
						d.FlowLast = t.Unix()
					}
				}
			}
		}
		if raw := caps[c.Name]; len(raw) > 2 {
			var k provision.Caps
			if json.Unmarshal(raw, &k) == nil && (k.Version != "" || k.Role != "") {
				d.Caps = convCaps(k)
			}
		}
		l.devs = append(l.devs, d)
	}
	return l.devs
}

func convCaps(k provision.Caps) *Caps {
	c := &Caps{Fasttrack: k.Fasttrack, HWOffload: k.HWOffload, FlowSupported: k.Flow.Supported, FlowEnabled: k.Flow.Enabled,
		FlowTargets: k.Flow.Targets, LogActions: len(k.LogActions), WwwSSL: k.WwwSSL, WwwSSLAddr: k.WwwSSLAddr}
	for _, f := range k.Filter {
		c.Filter = append(c.Filter, FilterRule{Chain: f.Chain, Action: f.Action, Comment: f.Comment, Summary: f.Summary,
			Log: f.Log, Disabled: f.Disabled, Managed: f.Managed})
	}
	for _, w := range k.WifiIfs {
		c.WifiIfs = append(c.WifiIfs, WifiIf{Name: w.Name, SSID: w.SSID, Band: w.Band})
	}
	return c
}

func (l *Live) Clients() []Client {
	if l.clsOK {
		return l.cls
	}
	l.clsOK = true
	rows, _ := l.St.Clients()
	for _, c := range rows {
		l.cls = append(l.cls, Client{MAC: c.MAC, Label: c.Label, Hostname: c.Hostname, Vendor: c.Vendor, IP: c.IP,
			Device: c.Device, SSID: c.SSID, Band: c.Band, Signal: c.Signal, WiFi: c.WiFi, Online: c.Online,
			FirstSeen: c.FirstSeen, LastSeen: c.LastSeen})
	}
	return l.cls
}

func (l *Live) Traffic24h() []ClientTraffic {
	if l.trOK {
		return l.tr
	}
	l.trOK = true
	rows, _ := l.St.ClientTotals(l.now.Add(-24 * time.Hour).Unix())
	for _, r := range rows {
		if l.IsSelf != nil && l.IsSelf(r.MAC) {
			continue
		}
		l.tr = append(l.tr, ClientTraffic{MAC: r.MAC, Up: r.Up, Down: r.Down, Int: r.Int, Fl: r.Fl})
	}
	return l.tr
}

func (l *Live) TopService(mac string) string {
	rows, err := l.St.TopBy("services", l.now.Add(-24*time.Hour).Unix(), 1, mac)
	if err != nil || len(rows) == 0 {
		return ""
	}
	return rows[0].Key
}

func conv(rows []store.PortRow) []PortUse {
	out := make([]PortUse, 0, len(rows))
	for _, r := range rows {
		out = append(out, PortUse{MAC: r.MAC, RIP: r.RIP, Host: r.Host, Port: r.RPort, Proto: r.Proto, Bytes: r.Bytes, Flows: r.Flows})
	}
	return out
}

func (l *Live) PortUse24h() []PortUse {
	if l.portsOK {
		return l.ports
	}
	l.portsOK = true
	rows, _ := l.St.PortUse(l.now.Add(-24*time.Hour).Unix(), PortsOut)
	l.ports = conv(rows)
	return l.ports
}

func (l *Live) DoH24h() []PortUse {
	rows, _ := l.St.HostUse(l.now.Add(-24*time.Hour).Unix(), 443, DoHSuffixes())
	return conv(rows)
}

func (l *Live) InboundRecent() []Inbound {
	rows, _ := l.St.InboundUse(l.now.Add(-time.Hour).Unix(), PortsInbound)
	var out []Inbound
	for _, r := range rows {
		out = append(out, Inbound{MAC: r.MAC, IP: r.CIP, Port: r.CPort, Proto: r.Proto, Flows: r.Flows, Hosts: int64(r.Sources)})
	}
	return out
}

func (l *Live) Roams24h() []RoamStat {
	rows, _ := l.St.RoamStats(l.now.Add(-24*time.Hour).Unix(), RoamsPerDay)
	var out []RoamStat
	for _, r := range rows {
		out = append(out, RoamStat{MAC: r.MAC, N: r.N, APs: r.APs, Last: r.Last})
	}
	return out
}

func (l *Live) IPChurn30d() map[string]int {
	m, _ := l.St.IPChurn(l.now.Add(-30*24*time.Hour).Unix(), 3)
	return m
}

func (l *Live) BlockedSources24h() []BlockedSrc {
	rows, _, _ := l.St.BlockedSources(l.now.Add(-24*time.Hour).Unix(), 300)
	var out []BlockedSrc
	for _, r := range rows {
		b := BlockedSrc{IP: r.IP, Hits: r.Hits}
		if ip, err := netip.ParseAddr(r.IP); err == nil && l.En != nil && IsPublic(r.IP) {
			g := l.En.Lookup(ip)
			b.CC, b.ASN, b.ASOrg = g.CC, int(g.ASN), g.ASOrg
		}
		out = append(out, b)
	}
	return out
}

func (l *Live) FwRules() []FwRule {
	rs, err := l.St.FwRules()
	if err != nil || len(rs) == 0 {
		return nil
	}
	hits, _, _ := l.St.FwHits(l.now.Add(-7 * 24 * time.Hour).Unix())
	out := make([]FwRule, 0, len(rs))
	for _, r := range rs {
		out = append(out, FwRule{Device: r.Device, Prefix: r.Prefix, Chain: r.Chain, Action: r.Action, Descr: r.Descr,
			Managed: r.Managed, Hits7d: hits[[2]string{r.Device, r.Prefix}]})
	}
	return out
}

func (l *Live) FwHistory() time.Duration {
	_, oldest, _ := l.St.FwHits(l.now.Unix())
	if oldest == 0 {
		return 0
	}
	return l.now.Sub(time.Unix(oldest, 0))
}
