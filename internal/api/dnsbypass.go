package api

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/napfkuchen1/mtmon/internal/advice"
	"github.com/napfkuchen1/mtmon/internal/dnsbypass"
	"github.com/napfkuchen1/mtmon/internal/probe"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// bypassCache keeps the DNS-bypass detection for a few minutes: it reads the hourly rollup for a week and is shown on
// the clients list, the client page and the overview.
type bypassCache struct {
	mu   sync.Mutex
	at   time.Time
	list []dnsbypass.Client
	by   map[string]dnsbypass.Client
}

const bypassTTL = 5 * time.Minute

func (s *Server) dnsBypass() ([]dnsbypass.Client, map[string]dnsbypass.Client) {
	c := &s.bypass
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.by != nil && time.Since(c.at) < bypassTTL {
		return c.list, c.by
	}
	list, err := s.detectBypass(dnsbypass.Window)
	if err != nil {
		return c.list, c.by // keep the old answer, an empty map is fine for callers
	}
	c.list, c.at = list, time.Now()
	c.by = make(map[string]dnsbypass.Client, len(list))
	for _, x := range list {
		c.by[x.MAC] = x
	}
	return c.list, c.by
}

// detectBypass runs the detection over the given period, honouring the "mark as fixed" times.
func (s *Server) detectBypass(window time.Duration) ([]dnsbypass.Client, error) {
	now := time.Now()
	return dnsbypass.DetectOpts(s.St, now.Add(-window).Unix(), dnsbypass.Options{
		Ignore: dnsbypass.ProbeHosts(s.probeTargets()), Skip: s.isSelfMAC, Resets: dnsbypass.LoadResets(s.St), Now: now,
		Redirected: dnsbypass.RedirectsFrom(advice.NewLive(s.St, s.Cfg, nil, nil, now).Devices())})
}

// bypassRanges are the periods the DNS-bypass view can look at.
var bypassRanges = map[string]time.Duration{"1h": time.Hour, "24h": 24 * time.Hour, "7d": dnsbypass.Window, "30d": 30 * 24 * time.Hour}

// isSelfMAC reports whether a MAC is an interface of a managed router/AP.
func (s *Server) isSelfMAC(mac string) bool {
	if s.Mgr == nil {
		return false
	}
	_, ok := s.Mgr.SelfDevice(mac)
	return ok
}

// probeTargets are the host:port pairs of mtmon's WAN-quality probe (configured or the default resolvers).
func (s *Server) probeTargets() []string {
	if len(s.Cfg.WANTargets) > 0 {
		return s.Cfg.WANTargets
	}
	return probe.DefaultTargets
}

// dnsBypassList: GET /api/dns-bypass?range=1h|24h|7d|30d – clients that do not use the router's DNS (default 7 days),
// split into active and ended, plus clients under observation after "mark as fixed"; the alert setting.
func (s *Server) dnsBypassList(w http.ResponseWriter, r *http.Request) {
	rn := r.URL.Query().Get("range")
	win, ok := bypassRanges[rn]
	if !ok {
		rn, win = "7d", dnsbypass.Window
	}
	var list []dnsbypass.Client
	if rn == "7d" {
		list, _ = s.dnsBypass()
	} else if l, err := s.detectBypass(win); err == nil {
		list = l
	}
	names := map[string]string{}
	cl, _ := s.St.Clients()
	for _, c := range cl {
		names[c.MAC] = ClientName(c)
	}
	type row struct {
		dnsbypass.Client
		Name string `json:"name"`
	}
	nameOf := func(mac string) string {
		if n := names[mac]; n != "" {
			return n
		}
		return mac
	}
	out := make([]row, 0, len(list))
	active, redirected := 0, 0
	listed := map[string]bool{}
	for _, c := range list {
		out = append(out, row{c, nameOf(c.MAC)})
		listed[c.MAC] = true
		if c.Redirected {
			redirected++ // listed below the real ones, never counted
		} else if c.Active {
			active++
		}
	}
	// marked as fixed and nothing found since: show as "under observation" so the reset is visible
	type watch struct {
		MAC   string `json:"mac"`
		Name  string `json:"name"`
		Since int64  `json:"since"`
	}
	obs := []watch{}
	for mac, ts := range dnsbypass.LoadResets(s.St) {
		if !listed[mac] {
			obs = append(obs, watch{mac, nameOf(mac), ts})
		}
	}
	sort.Slice(obs, func(i, j int) bool { return obs[i].Since > obs[j].Since })
	jsonOut(w, map[string]any{"clients": out, "total": active, "ended": len(out) - active - redirected, "redirected": redirected, "observing": obs, "range": rn,
		"window_days": int(win.Hours() / 24), "ended_after_h": int(dnsbypass.EndedAfter.Hours()),
		"alert": s.St.GetMeta(dnsbypass.MetaAlert) == "on"})
}

// dnsBypassReset: POST /api/dns-bypass/reset {mac | "all"} – "mark as fixed": only flows after now count for the
// client. A bypass that returns is flagged again (and alerted, if the alert is on); the flag can take up to an hour
// to come back because the evidence is read from hourly buckets.
func (s *Server) dnsBypassReset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MAC string `json:"mac"`
		All bool   `json:"all"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.MAC = strings.TrimSpace(in.MAC)
	if !in.All && in.MAC == "" {
		jerr(w, 400, "mac or all required")
		return
	}
	now := time.Now()
	resets := dnsbypass.LoadResets(s.St)
	macs := map[string]bool{}
	if in.All {
		list, _ := s.dnsBypass()
		for _, c := range list {
			if !c.Redirected {
				macs[c.MAC] = true
			}
		}
	} else {
		macs[in.MAC] = true
	}
	for m := range macs {
		resets[m] = now.Unix()
	}
	if err := dnsbypass.SaveResets(s.St, resets, now); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	dnsbypass.ForgetSeen(s.St, macs)
	s.bypass.mu.Lock()
	s.bypass.by = nil
	s.bypass.mu.Unlock()
	jsonOut(w, map[string]int{"reset": len(macs)})
}

// dnsBypassSave: PUT /api/dns-bypass {alert} – switches the "a client started bypassing the local DNS" alert on or off.
func (s *Server) dnsBypassSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Alert bool `json:"alert"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	v := "off"
	if in.Alert {
		v = "on"
	}
	if err := s.St.SetMeta(dnsbypass.MetaAlert, v); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	if in.Alert { // start from the current state: enabling the alert must not report every existing client
		s.St.SetMeta("dns_bypass_seen", "")
	}
	jsonOut(w, map[string]bool{"alert": in.Alert})
}

// v6prefixes: the /64 prefixes a client had global IPv6 addresses in during the last 14 days, newest first (never nil).
func v6prefixes(st *store.Store, mac string) []store.V6Prefix {
	m, err := st.IPv6Prefixes(mac, time.Now().AddDate(0, 0, -14).Unix())
	if err != nil || len(m[mac]) == 0 {
		return []store.V6Prefix{}
	}
	return m[mac]
}
