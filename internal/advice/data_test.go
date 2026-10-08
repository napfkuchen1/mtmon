package advice

import "time"

// Data is a plain in-memory Snapshot (test fixture; the live Snapshot is built in the API layer).
type Data struct {
	NowT    time.Time
	Uptime  time.Duration
	Devs    []Device
	Cls     []Client
	Traffic []ClientTraffic
	TopSvc  map[string]string
	Ports   []PortUse
	DoH     []PortUse
	In      []Inbound
	RoamV   []RoamStat
	Churn   map[string]int
	Blocked []BlockedSrc
	Contact map[string]bool
	Rules   []FwRule
	FwHist  time.Duration
}

func (d *Data) Now() time.Time {
	if d.NowT.IsZero() {
		return time.Now()
	}
	return d.NowT
}
func (d *Data) ContactedByLAN(ips []string) map[string]bool {
	m := map[string]bool{}
	for _, ip := range ips {
		if d.Contact[ip] {
			m[ip] = true
		}
	}
	return m
}
func (d *Data) ProcessUptime() time.Duration    { return d.Uptime }
func (d *Data) Devices() []Device               { return d.Devs }
func (d *Data) Clients() []Client               { return d.Cls }
func (d *Data) Traffic24h() []ClientTraffic     { return d.Traffic }
func (d *Data) TopService(mac string) string    { return d.TopSvc[mac] }
func (d *Data) PortUse24h() []PortUse           { return d.Ports }
func (d *Data) DoH24h() []PortUse               { return d.DoH }
func (d *Data) InboundRecent() []Inbound        { return d.In }
func (d *Data) Roams24h() []RoamStat            { return d.RoamV }
func (d *Data) IPChurn30d() map[string]int      { return d.Churn }
func (d *Data) BlockedSources24h() []BlockedSrc { return d.Blocked }
func (d *Data) FwRules() []FwRule               { return d.Rules }
func (d *Data) FwHistory() time.Duration        { return d.FwHist }
