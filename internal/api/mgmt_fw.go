package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/poller"
	"github.com/napfkuchen1/mtmon/internal/provision"
	"github.com/napfkuchen1/mtmon/internal/store"
	"github.com/napfkuchen1/mtmon/internal/syslog"
)

// ---- firewall ----

// fwSummaryCached: the firewall summary groups the whole raw log of the range (seconds for 7 days), so a page that
// is opened or refreshed repeatedly reuses the result for 30 s. The cache key is the range name, the data is shared.
func (s *Server) fwSummaryCached(rn string, since int64) (*store.FwSummary, error) {
	s.fwMu.Lock()
	defer s.fwMu.Unlock()
	if c, ok := s.fwCache[rn]; ok && time.Since(c.at) < 30*time.Second {
		return c.sum, nil
	}
	sum, err := s.St.FwSummary(since)
	if err != nil {
		return nil, err
	}
	s.nameTops(sum)
	if s.fwCache == nil {
		s.fwCache = map[string]fwCacheEntry{}
	}
	s.fwCache[rn] = fwCacheEntry{at: time.Now(), sum: sum}
	return sum, nil
}

type fwCacheEntry struct {
	at  time.Time
	sum *store.FwSummary
}

func (s *Server) fwSummary(w http.ResponseWriter, r *http.Request) {
	since, _, rn := parseRange(r)
	sum, err := s.fwSummaryCached(rn, since)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	rules, _ := s.St.FwRules()
	var recv, parsed, unk, drop uint64
	samples := []syslog.Sample{}
	if s.Sys != nil {
		recv, parsed, unk, drop = s.Sys.Received.Load(), s.Sys.Parsed.Load(), s.Sys.Unknown.Load(), s.Sys.Dropped.Load()
		samples = s.Sys.Samples()
	}
	jsonOut(w, map[string]any{"range": rn, "summary": sum, "rules": rules, "enabled": len(rules) > 0,
		"syslog":          map[string]uint64{"received": recv, "parsed": parsed, "unparsed": unk, "dropped_unknown_source": drop},
		"unparsed_sample": samples})
}

// isOwnEvent: log line of mtmon's own management access (rule label or host name mentions mtmon).
func isOwnEvent(e store.FwEvent) bool {
	return store.IsOwnRule(e.Rule) || strings.HasPrefix(strings.ToLower(e.SrcName), "mtmon.") || strings.HasPrefix(strings.ToLower(e.DstName), "mtmon.")
}

func (s *Server) fwEvents(w http.ResponseWriter, r *http.Request) {
	since, _, _ := parseRange(r)
	q := r.URL.Query()
	lim, _ := strconv.Atoi(q.Get("limit"))
	dp, _ := strconv.Atoi(q.Get("port"))
	own := q.Get("own") == "1"
	fl := store.FwFilter{Since: since, Device: q.Get("device"), Src: q.Get("src"), Dst: q.Get("dst"), Peer: q.Get("peer"),
		DPort: dp, Verdict: q.Get("verdict"), Limit: lim}
	if !own {
		// fetch extra rows: mtmon's own traffic is dropped below and must not eat the page
		if fl.Limit <= 0 {
			fl.Limit = 200
		}
		fl.Limit *= 3
	}
	ev, err := s.St.FwEvents(fl)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	s.nameEvents(ev)
	if !own {
		keep := lim
		if keep <= 0 {
			keep = 200
		}
		out := ev[:0]
		for _, e := range ev {
			if isOwnEvent(e) {
				continue
			}
			if out = append(out, e); len(out) >= keep {
				break
			}
		}
		ev = out
	}
	jsonOut(w, ev)
}

// clientFw lists firewall events involving the client's current and historic IPs.
func (s *Server) clientFw(w http.ResponseWriter, r *http.Request) {
	mac := store.NormMAC(r.PathValue("mac"))
	since, _, _ := parseRange(r)
	c, _, err := s.clientOrAddr(mac)
	if err != nil {
		jerr(w, 404, "unknown client")
		return
	}
	ips := map[string]bool{}
	if c.IP != "" {
		ips[c.IP] = true
	}
	for _, h := range must(s.St.IPHistory(mac)) {
		if h.Last >= since {
			ips[h.IP] = true
		}
	}
	out := []store.FwEvent{}
	verdict := r.URL.Query().Get("verdict")
	for ip := range ips {
		ev, _ := s.St.FwEvents(store.FwFilter{Since: since, Src: ip, Verdict: verdict, Limit: 300})
		out = append(out, ev...)
	}
	sortFw(out)
	if len(out) > 300 {
		out = out[:300]
	}
	s.nameEvents(out)
	jsonOut(w, out)
}

func sortFw(a []store.FwEvent) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j].TS > a[j-1].TS; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

var protoNum = map[string]int{"TCP": 6, "UDP": 17, "ICMP": 1}

// annotateConns adds the router name and the firewall verdict to raw connections.
//
// Verdict logic (honest about what is known): a flow only exists if the packet passed the router, so
// when firewall logging is active for that router and no logged DROP/REJECT rule matched the
// connection, it is "allowed". A logged drop within the connection's time window is "blocked".
func (s *Server) annotateConns(mac string, conns []store.Conn, since int64) {
	if len(conns) == 0 {
		return
	}
	rules, _ := s.St.FwRules()
	fwDev := map[string]bool{}
	for _, r := range rules {
		fwDev[r.Device] = true
	}
	for i := range conns {
		if a, err := netip.ParseAddr(conns[i].Exporter); err == nil {
			conns[i].Device = s.Cfg.SourceDevice(a)
		}
	}
	if len(fwDev) == 0 {
		return
	}
	c, err := s.St.Client(mac)
	if err != nil {
		return
	}
	ips := map[string]bool{}
	if c.IP != "" {
		ips[c.IP] = true
	}
	for _, h := range must(s.St.IPHistory(mac)) {
		if h.Last >= since {
			ips[h.IP] = true
		}
	}
	type key struct {
		peer string
		port int
	}
	evs := map[key][]store.FwEvent{}
	oldest := conns[len(conns)-1].TS - 300
	for ip := range ips {
		out, _ := s.St.FwEvents(store.FwFilter{Since: oldest, Src: ip, Limit: 2000})
		for _, e := range out {
			evs[key{e.Dst, e.DPort}] = append(evs[key{e.Dst, e.DPort}], e)
		}
		in, _ := s.St.FwEvents(store.FwFilter{Since: oldest, Dst: ip, Limit: 2000})
		for _, e := range in {
			evs[key{e.Src, e.DPort}] = append(evs[key{e.Src, e.DPort}], e)
		}
	}
	for i := range conns {
		cn := &conns[i]
		port := cn.RPort
		if cn.Dir == "d" {
			port = cn.CPort
		}
		best := store.FwEvent{}
		for _, e := range evs[key{cn.RIP, port}] {
			if e.TS < cn.TS-300 || e.TS > cn.TS+60 {
				continue
			}
			if n, ok := protoNum[e.Proto]; ok && n != cn.Proto {
				continue
			}
			if rank(e.Verdict) > rank(best.Verdict) {
				best = e
			}
		}
		switch {
		case best.Verdict != "":
			cn.Verdict, cn.Rule = best.Verdict, best.Rule
		case fwDev[cn.Device]:
			cn.Verdict, cn.Rule = "allowed", "no logged drop rule matched"
		}
	}
}

func rank(v string) int {
	switch v {
	case "blocked":
		return 3
	case "allowed":
		return 2
	case "logged":
		return 1
	}
	return 0
}

// editDevice: PUT /api/devices/{name} – change address / port / role / site / credentials of a UI-managed device.
// The new settings are tested against the router first; nothing is saved when the connection fails. Router-side
// settings are never touched here (use offboarding for that).
func (s *Server) editDevice(w http.ResponseWriter, r *http.Request) {
	if s.Box == nil {
		jerr(w, 503, "device management not available")
		return
	}
	name := r.PathValue("name")
	var in struct {
		Addr   string `json:"addr"`
		Port   int    `json:"port"`
		Scheme string `json:"scheme"`
		Role   string `json:"role"`
		Site   string `json:"site"`
		User   string `json:"user"`
		Pass   string `json:"pass"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	s.provMu.Lock()
	defer s.provMu.Unlock()
	row, err := s.St.DeviceRow(name)
	if err != nil || row == nil {
		if _, ok := s.Cfg.Device(name); ok {
			jerr(w, 400, "this device is defined in config.json - edit it there")
			return
		}
		jerr(w, 404, "unknown device")
		return
	}
	old := *row
	if a := strings.TrimSpace(in.Addr); a != "" {
		if strings.ContainsAny(a, "/@ ?#") {
			jerr(w, 400, "address must be a plain IP or hostname")
			return
		}
		if err := s.targetAllowed(a); err != nil {
			jerr(w, 400, err.Error())
			return
		}
		row.Addr = a
	}
	if in.Port != 0 {
		if in.Port < 1 || in.Port > 65535 {
			jerr(w, 400, "invalid port")
			return
		}
		row.Port = in.Port
	}
	if in.Scheme == "http" || in.Scheme == "https" {
		if in.Scheme == "http" {
			if ip := net.ParseIP(row.Addr); ip == nil || !ip.IsLoopback() {
				jerr(w, 400, "plain HTTP is only allowed for loopback addresses (tests)")
				return
			}
		}
		row.Scheme = in.Scheme
	}
	if in.Role == "router" || in.Role == "ap" || in.Role == "switch" {
		row.Role = in.Role
	}
	row.Site = strings.TrimSpace(in.Site)
	if in.User != "" {
		row.User = in.User
	}
	if in.Pass != "" {
		row.PassEnc = s.Box.Seal(in.Pass)
	}
	moved := row.Addr != old.Addr || row.Port != old.Port || row.Scheme != old.Scheme
	if moved && row.Scheme == "https" {
		fp, err := poller.FingerprintGuarded(net.JoinHostPort(row.Addr, strconv.Itoa(row.Port)), s.addrAllowed)
		if err != nil {
			jerr(w, 502, friendlyConnErr(err))
			return
		}
		row.Fingerprint = fp
	}
	pass, _ := s.Box.Open(row.PassEnc)
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	d := config.Device{Name: name, Addr: row.Addr, Port: row.Port, Scheme: row.Scheme, User: row.User, Pass: pass, Fingerprint: row.Fingerprint, Insecure: row.Insecure}
	caps, err := provision.Probe(ctx, poller.NewGuardedClient(d, s.addrAllowed))
	if err != nil {
		jerr(w, 502, friendlyConnErr(err))
		return
	}
	if moved || len(row.Caps) == 0 {
		row.Caps, _ = json.Marshal(caps)
	}
	if err := s.St.UpsertDevice(*row); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	s.LoadDevices()
	jsonOut(w, map[string]any{"name": name, "ok": true})
}
