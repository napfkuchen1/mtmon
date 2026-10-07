package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/enrich"
	"github.com/napfkuchen1/mtmon/internal/live"
	"github.com/napfkuchen1/mtmon/internal/poller"
	"github.com/napfkuchen1/mtmon/internal/provision"
	"github.com/napfkuchen1/mtmon/internal/store"
)

func (s *Server) mgmtRoutes(m *http.ServeMux, a func(http.HandlerFunc) http.HandlerFunc) {
	m.HandleFunc("POST /api/devices/probe", a(s.probe))
	m.HandleFunc("POST /api/devices/plan", a(s.plan))
	m.HandleFunc("POST /api/devices/provision", a(s.provision))
	m.HandleFunc("POST /api/devices/readonly", a(s.addReadonly))
	m.HandleFunc("GET /api/devices/{name}/manifest", a(s.manifest))
	m.HandleFunc("GET /api/devices/{name}/offboard-script", a(s.offboardScript))
	m.HandleFunc("POST /api/devices/{name}/offboard", a(s.offboard))
	m.HandleFunc("GET /api/services", a(s.services))
	m.HandleFunc("GET /api/services/{name}", a(s.serviceDetail))
	m.HandleFunc("GET /api/service-rules", a(s.ruleList))
	m.HandleFunc("POST /api/service-rules", a(s.ruleAdd))
	m.HandleFunc("POST /api/service-rules/preview", a(s.rulePreview))
	m.HandleFunc("DELETE /api/service-rules/{id}", a(s.ruleDelete))
	m.HandleFunc("GET /api/firewall/summary", a(s.fwSummary))
	m.HandleFunc("GET /api/firewall/events", a(s.fwEvents))
	m.HandleFunc("GET /api/clients/{mac}/firewall", a(s.clientFw))
}

// ---- helpers ----

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)

// LoadDevices (re)reads UI-managed devices from the DB, decrypts credentials and tells the poller.
func (s *Server) LoadDevices() error {
	rows, err := s.St.DeviceRows()
	if err != nil {
		return err
	}
	var ds []config.Device
	for _, r := range rows {
		pass := ""
		if s.Box != nil {
			if p, err := s.Box.Open(r.PassEnc); err == nil {
				pass = p
			}
		}
		d := config.Device{Name: r.Name, Addr: r.Addr, Role: r.Role, Site: r.Site, User: r.User, Pass: pass, Port: r.Port,
			Scheme: r.Scheme, Insecure: r.Insecure, Fingerprint: r.Fingerprint, Managed: r.Managed, FromUI: true}
		for _, f := range strings.Split(r.FlowSrc, ",") {
			if f = strings.TrimSpace(f); f != "" {
				d.FlowSources = append(d.FlowSources, f)
			}
		}
		ds = append(ds, d)
	}
	s.Cfg.SetDynamic(ds)
	if s.Mgr != nil {
		s.Mgr.Reload()
	}
	return nil
}

// metadataAddrs are cloud instance-metadata endpoints; never a router.
var metadataAddrs = []netip.Addr{
	netip.MustParseAddr("169.254.169.254"), netip.MustParseAddr("fd00:ec2::254"), netip.MustParseAddr("100.100.100.200"),
}

// addrAllowed is the policy of the setup wizard: private / loopback / link-local / CGNAT addresses and the configured
// local networks only, so it cannot be abused to probe the internet or cloud metadata endpoints. It is enforced
// when a connection is made (see poller.NewGuardedClient), not only once up front.
func (s *Server) addrAllowed(a netip.Addr) error {
	a = a.Unmap()
	for _, m := range metadataAddrs {
		if a == m {
			return errors.New("address not allowed")
		}
	}
	if !(a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || cgnat.Contains(a) || s.Cfg.IsLocal(a)) {
		return fmt.Errorf("%s is a public address - mtmon only sets up devices in private networks", a)
	}
	return nil
}

// targetAllowed resolves host and applies addrAllowed to every address, to give the user an early, clear error
// before any connection is attempted. The connection itself is guarded again at dial time.
func (s *Server) targetAllowed(host string) error {
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("cannot resolve %q", host)
	}
	for _, ip := range ips {
		a, _ := netip.AddrFromSlice(ip)
		if err := s.addrAllowed(a); err != nil {
			return err
		}
	}
	return nil
}

func srcIPFor(addr string, port int) string {
	c, err := net.DialTimeout("udp", net.JoinHostPort(addr, strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		return ""
	}
	defer c.Close()
	h, _, _ := net.SplitHostPort(c.LocalAddr().String())
	return h
}

type connReq struct {
	Addr        string `json:"addr"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	Pass        string `json:"pass"`
	Fingerprint string `json:"fingerprint"`
	Scheme      string `json:"scheme"`
}

func (c *connReq) normalize(s *Server) error {
	c.Addr = strings.TrimSpace(c.Addr)
	if c.Addr == "" || c.User == "" || c.Pass == "" {
		return errors.New("address, user and password are required")
	}
	if strings.ContainsAny(c.Addr, "/@ ?#") {
		return errors.New("address must be a plain IP or hostname")
	}
	if c.Port == 0 {
		c.Port = 443
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("invalid port")
	}
	ip := net.ParseIP(c.Addr)
	if c.Scheme == "http" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("plain HTTP is only allowed for loopback addresses (tests)")
	}
	if c.Scheme != "http" {
		c.Scheme = "https"
	}
	return s.targetAllowed(c.Addr)
}

func (c connReq) device(name string) config.Device {
	return config.Device{Name: name, Addr: c.Addr, Port: c.Port, Scheme: c.Scheme, User: c.User, Pass: c.Pass, Fingerprint: c.Fingerprint}
}

func (s *Server) nameTaken(name string) bool {
	_, ok := s.Cfg.Device(name)
	return ok
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		jerr(w, 400, "bad request")
		return false
	}
	return true
}

func friendlyConnErr(err error) string {
	switch {
	case errors.Is(err, poller.ErrAuth):
		return "Login rejected - check user name and password."
	case strings.Contains(err.Error(), "connection refused"):
		return "Connection refused - is www-ssl (HTTPS, port 443) enabled on the router? (/ip service enable www-ssl)"
	case strings.Contains(err.Error(), "handshake failure") || strings.Contains(err.Error(), "tls: protocol version"):
		return "TLS handshake failed - the www-ssl service on the router probably has no certificate (/ip service set www-ssl certificate=<name>)."
	case strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline"):
		return "Timed out - check the address and firewall (mtmon must be able to reach the router on the REST port)."
	}
	return err.Error()
}

// ---- probe ----

func (s *Server) probe(w http.ResponseWriter, r *http.Request) {
	var in connReq
	if !readJSON(w, r, &in) {
		return
	}
	if err := in.normalize(s); err != nil {
		jerr(w, 400, err.Error())
		return
	}
	if in.Scheme == "https" && in.Fingerprint == "" {
		fp, err := poller.FingerprintGuarded(net.JoinHostPort(in.Addr, strconv.Itoa(in.Port)), s.addrAllowed)
		if err != nil {
			jerr(w, 502, friendlyConnErr(err))
			return
		}
		in.Fingerprint = fp
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	cl := poller.NewGuardedClient(in.device(""), s.addrAllowed)
	caps, err := provision.Probe(ctx, cl)
	if err != nil {
		jerr(w, 502, friendlyConnErr(err))
		return
	}
	_, werr := cl.Get(ctx, "/user/group")
	canWrite := werr == nil
	mtIP := srcIPFor(in.Addr, in.Port)
	opt := provision.DefaultOptions(caps, mtIP)
	_, fp, _ := net.SplitHostPort(s.Cfg.FlowListen)
	if n, _ := strconv.Atoi(fp); n > 0 {
		opt.FlowPort = n
	}
	_, sp, _ := net.SplitHostPort(s.Cfg.SyslogListen)
	if n, _ := strconv.Atoi(sp); n > 0 {
		opt.SyslogPort = n
	}
	if !nameRe.MatchString(opt.Name) {
		opt.Name = strings.Trim(regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(opt.Name, "-"), "-.")
	}
	if opt.Name == "" || len(opt.Name) > 40 {
		opt.Name = "router-" + strings.ReplaceAll(in.Addr, ".", "-")
	}
	base, n := opt.Name, 2
	for s.nameTaken(opt.Name) {
		opt.Name = fmt.Sprintf("%s-%d", base, n)
		n++
	}
	opt.DeviceAddr = in.Addr
	jsonOut(w, map[string]any{"fingerprint": in.Fingerprint, "caps": caps, "can_write": canWrite, "options": opt,
		"plan": provision.BuildPlan(caps, opt), "mtmon_ip": mtIP})
}

// plan re-renders the explanation for changed options (display only; provision re-probes the router).
func (s *Server) plan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Caps    provision.Caps    `json:"caps"`
		Options provision.Options `json:"options"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Options.MonUser == "" {
		in.Options.MonUser = "mtmon"
	}
	jsonOut(w, provision.BuildPlan(&in.Caps, in.Options))
}

// ---- provision (auto setup) ----

type provReq struct {
	connReq
	Options provision.Options `json:"options"`
}

func (s *Server) provision(w http.ResponseWriter, r *http.Request) {
	if s.Box == nil {
		jerr(w, 503, "device management not available")
		return
	}
	var in provReq
	if !readJSON(w, r, &in) {
		return
	}
	if err := in.normalize(s); err != nil {
		jerr(w, 400, err.Error())
		return
	}
	o := in.Options
	if !nameRe.MatchString(o.Name) {
		jerr(w, 400, "device name: letters, digits, . _ - (max 40)")
		return
	}
	if net.ParseIP(o.MtmonIP) == nil {
		jerr(w, 400, "mtmon IP (as seen by the router) is invalid")
		return
	}
	if o.Role != "router" && o.Role != "ap" && o.Role != "switch" {
		o.Role = "router"
	}
	if o.FlowPort <= 0 || o.FlowPort > 65535 {
		o.FlowPort = 2055
	}
	if o.SyslogPort <= 0 || o.SyslogPort > 65535 {
		o.SyslogPort = 5514
	}
	if o.MonUser == "" {
		o.MonUser = "mtmon"
	}
	if !nameRe.MatchString(o.MonUser) {
		jerr(w, 400, "invalid monitoring user name")
		return
	}
	o.DeviceAddr = in.Addr
	s.provMu.Lock()
	defer s.provMu.Unlock()
	if s.nameTaken(o.Name) {
		jerr(w, 409, "a device with this name already exists")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	admin := poller.NewGuardedClient(in.device(""), s.addrAllowed)
	caps, err := provision.Probe(ctx, admin)
	if err != nil {
		jerr(w, 502, friendlyConnErr(err))
		return
	}
	roFor := func(u, p string) *poller.Client {
		d := in.device(o.Name)
		d.User, d.Pass = u, p
		return poller.NewGuardedClient(d, s.addrAllowed)
	}
	res := provision.Apply(ctx, admin, roFor, caps, o, func(e store.ManifestEntry) { s.St.AddManifest(o.Name, e) })
	out := map[string]any{"result": res, "name": o.Name}
	if !res.OK {
		failed := false
		for _, rb := range res.Rollback {
			failed = failed || !rb.OK
		}
		if failed {
			out["manual_script"] = provision.ManualScript(res.Entries)
		} else {
			s.St.DB.Exec(`DELETE FROM manifest WHERE device=?`, o.Name)
		}
		jsonOut(w, out)
		return
	}
	capsJSON, _ := json.Marshal(caps)
	row := store.DeviceRow{Name: o.Name, Addr: in.Addr, Port: in.Port, Scheme: in.Scheme, Role: o.Role, Site: o.Site,
		User: res.MonUser, PassEnc: s.Box.Seal(res.MonPass), Fingerprint: in.Fingerprint, Caps: capsJSON, Managed: true}
	if err := s.St.UpsertDevice(row); err != nil {
		out["manual_script"] = provision.ManualScript(res.Entries)
		jerr(w, 500, "router was configured but saving the device failed: "+err.Error())
		return
	}
	var metas []store.FwRule
	for _, m := range res.FwMeta {
		metas = append(metas, store.FwRule{Device: o.Name, Prefix: m.Prefix, Chain: m.Chain, Action: m.Action, Descr: m.Descr, Managed: m.Managed})
	}
	s.St.SaveFwRules(metas)
	s.LoadDevices()
	jsonOut(w, out)
}

// ---- read-only add ----

func (s *Server) addReadonly(w http.ResponseWriter, r *http.Request) {
	if s.Box == nil {
		jerr(w, 503, "device management not available")
		return
	}
	var in struct {
		connReq
		Name string `json:"name"`
		Role string `json:"role"`
		Site string `json:"site"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := in.normalize(s); err != nil {
		jerr(w, 400, err.Error())
		return
	}
	if !nameRe.MatchString(in.Name) {
		jerr(w, 400, "device name: letters, digits, . _ - (max 40)")
		return
	}
	s.provMu.Lock()
	defer s.provMu.Unlock()
	if s.nameTaken(in.Name) {
		jerr(w, 409, "a device with this name already exists")
		return
	}
	if in.Scheme == "https" && in.Fingerprint == "" {
		fp, err := poller.FingerprintGuarded(net.JoinHostPort(in.Addr, strconv.Itoa(in.Port)), s.addrAllowed)
		if err != nil {
			jerr(w, 502, friendlyConnErr(err))
			return
		}
		in.Fingerprint = fp
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	caps, err := provision.Probe(ctx, poller.NewGuardedClient(in.device(in.Name), s.addrAllowed))
	if err != nil {
		jerr(w, 502, friendlyConnErr(err))
		return
	}
	if in.Role == "" {
		in.Role = caps.Role
	}
	capsJSON, _ := json.Marshal(caps)
	err = s.St.UpsertDevice(store.DeviceRow{Name: in.Name, Addr: in.Addr, Port: in.Port, Scheme: in.Scheme, Role: in.Role, Site: in.Site,
		User: in.User, PassEnc: s.Box.Seal(in.Pass), Fingerprint: in.Fingerprint, Caps: capsJSON})
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	s.LoadDevices()
	jsonOut(w, map[string]any{"name": in.Name, "caps": caps})
}

// ---- manifest / offboarding ----

func (s *Server) manifest(w http.ResponseWriter, r *http.Request) {
	m, err := s.St.Manifest(r.PathValue("name"))
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, m)
}

func (s *Server) offboardScript(w http.ResponseWriter, r *http.Request) {
	m, _ := s.St.Manifest(r.PathValue("name"))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(provision.ManualScript(m)))
}

func (s *Server) offboard(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var in struct {
		User  string `json:"user"`
		Pass  string `json:"pass"`
		Mode  string `json:"mode"` // rollback | forget
		Purge bool   `json:"purge"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	row, err := s.St.DeviceRow(name)
	if err != nil || row == nil {
		if _, ok := s.Cfg.Device(name); ok {
			jerr(w, 400, "this device is defined in config.json - remove it there")
			return
		}
		jerr(w, 404, "unknown device")
		return
	}
	s.provMu.Lock()
	defer s.provMu.Unlock()
	entries, _ := s.St.Manifest(name)
	out := map[string]any{"name": name}
	if in.Mode != "forget" && len(entries) > 0 {
		if in.User == "" || in.Pass == "" {
			jerr(w, 400, "admin user and password are required to undo the router changes")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		d := config.Device{Name: name, Addr: row.Addr, Port: row.Port, Scheme: row.Scheme, User: in.User, Pass: in.Pass, Fingerprint: row.Fingerprint, Insecure: row.Insecure}
		cl := poller.NewGuardedClient(d, s.addrAllowed)
		if _, err := cl.Get(ctx, "/system/resource"); err != nil {
			jerr(w, 502, friendlyConnErr(err))
			return
		}
		res := provision.Rollback(ctx, cl, entries, func(id int64, st string) { s.St.SetManifestState(id, st) })
		out["rollback"] = res
		for _, x := range res {
			if !x.OK {
				out["ok"] = false
				out["manual_script"] = provision.ManualScript(func() []store.ManifestEntry { m, _ := s.St.Manifest(name); return m }())
				jsonOut(w, out)
				return // keep the device so the operator can retry
			}
		}
	} else if len(entries) > 0 {
		out["manual_script"] = provision.ManualScript(entries)
	}
	if err := s.St.DeleteDevice(name, in.Purge); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	s.LoadDevices()
	s.Hub.RemoveDevice(name)
	out["ok"] = true
	jsonOut(w, out)
}

// ---- devices list ----

func (s *Server) devices(w http.ResponseWriter, r *http.Request) {
	states, _ := s.St.DeviceStates()
	liveD := map[string]live.DevLive{}
	for _, d := range s.Hub.Devices() {
		liveD[d.Name] = d
	}
	caps := map[string]json.RawMessage{}
	if rows, err := s.St.DeviceRows(); err == nil {
		for _, r := range rows {
			caps[r.Name] = r.Caps
		}
	}
	rules, _ := s.St.FwRules()
	fwOn := map[string]bool{}
	for _, r := range rules {
		fwOn[r.Device] = true
	}
	res := []map[string]any{}
	for _, d := range s.Cfg.AllDevices() {
		m := map[string]any{"name": d.Name, "addr": d.Addr, "port": d.Port, "scheme": d.Scheme, "role": d.Role, "site": d.Site,
			"state": states[d.Name], "live": liveD[d.Name], "managed": d.Managed, "source": map[bool]string{true: "ui", false: "config"}[d.FromUI],
			"firewall_logging": fwOn[d.Name]}
		if c, ok := caps[d.Name]; ok {
			m["caps"] = c
		}
		if ip, err := netip.ParseAddr(d.Addr); err == nil && s.Col != nil {
			if st := s.Col.Exporter(ip); !st.Last.IsZero() {
				m["flows_last"], m["flows_records"] = st.Last.Unix(), st.Records
			}
		}
		res = append(res, m)
	}
	jsonOut(w, res)
}

// ---- services ----

func (s *Server) services(w http.ResponseWriter, r *http.Request) {
	since, _, rn := parseRange(r)
	rows, err := s.St.Services(since)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	type row struct {
		store.ServiceRow
		Category string `json:"category"`
	}
	out := make([]row, 0, len(rows))
	for _, x := range rows {
		out = append(out, row{x, s.category(x.Name)})
	}
	jsonOut(w, map[string]any{"range": rn, "services": out, "reclassifying": s.reclass.Load()})
}

func (s *Server) category(svc string) string {
	if s.Cls != nil {
		return s.Cls.Category(svc)
	}
	return "Other"
}

func (s *Server) serviceDetail(w http.ResponseWriter, r *http.Request) {
	since, bucket, rn := parseRange(r)
	name := r.PathValue("name")
	d, err := s.St.ServiceDetail(name, since, bucket)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]any{"range": rn, "detail": d, "category": s.category(name)})
}

func (s *Server) ruleList(w http.ResponseWriter, r *http.Request) {
	rs, err := s.St.ServiceRules()
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, rs)
}

func (s *Server) rebuild(reset string) {
	if s.Cls == nil {
		return
	}
	rs, _ := s.St.ServiceRules()
	s.Cls.Set(rs)
	go func() {
		if !s.reclass.CompareAndSwap(false, true) {
			return
		}
		defer s.reclass.Store(false)
		for {
			cur, _ := s.St.ServiceRules()
			s.St.RebuildServices(enrich.OrderRules(cur), reset, func(p uint8, port uint16) string { return enrich.Service(p, port, 0) })
			reset = ""
			again, _ := s.St.ServiceRules()
			if len(again) == len(cur) {
				return
			}
		}
	}()
}

func (s *Server) ruleAdd(w http.ResponseWriter, r *http.Request) {
	var in store.ServiceRule
	if !readJSON(w, r, &in) {
		return
	}
	rule, err := enrich.ValidateRule(in)
	if err != nil {
		jerr(w, 400, err.Error())
		return
	}
	if all, _ := s.St.ServiceRules(); len(all) >= 500 {
		jerr(w, 400, "too many rules (max 500)")
		return
	}
	id, err := s.St.AddServiceRule(rule)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	s.rebuild("")
	rule.ID = id
	jsonOut(w, rule)
}

func (s *Server) rulePreview(w http.ResponseWriter, r *http.Request) {
	var in store.ServiceRule
	if !readJSON(w, r, &in) {
		return
	}
	rule, err := enrich.ValidateRule(in)
	if err != nil {
		jerr(w, 400, err.Error())
		return
	}
	c, d, b, f := s.St.RulePreview(rule, time.Now().Add(-30*24*time.Hour).Unix())
	jsonOut(w, map[string]any{"rule": rule, "clients": c, "dests": d, "bytes": b, "flows": f})
}

func (s *Server) ruleDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rs, _ := s.St.ServiceRules()
	name := ""
	for _, x := range rs {
		if x.ID == id {
			name = x.Name
		}
	}
	if name == "" {
		jerr(w, 404, "unknown rule")
		return
	}
	if err := s.St.DeleteServiceRule(id); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	s.rebuild(name)
	jsonOut(w, map[string]bool{"ok": true})
}

// ---- firewall ----

func (s *Server) fwSummary(w http.ResponseWriter, r *http.Request) {
	since, _, rn := parseRange(r)
	sum, err := s.St.FwSummary(since)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	s.nameTops(sum)
	rules, _ := s.St.FwRules()
	var recv, parsed, unk, drop uint64
	if s.Sys != nil {
		recv, parsed, unk, drop = s.Sys.Received.Load(), s.Sys.Parsed.Load(), s.Sys.Unknown.Load(), s.Sys.Dropped.Load()
	}
	jsonOut(w, map[string]any{"range": rn, "summary": sum, "rules": rules, "enabled": len(rules) > 0,
		"syslog": map[string]uint64{"received": recv, "parsed": parsed, "unparsed": unk, "dropped_unknown_source": drop}})
}

func (s *Server) fwEvents(w http.ResponseWriter, r *http.Request) {
	since, _, _ := parseRange(r)
	q := r.URL.Query()
	lim, _ := strconv.Atoi(q.Get("limit"))
	dp, _ := strconv.Atoi(q.Get("port"))
	ev, err := s.St.FwEvents(store.FwFilter{Since: since, Device: q.Get("device"), Src: q.Get("src"), Dst: q.Get("dst"), Peer: q.Get("peer"),
		DPort: dp, Verdict: q.Get("verdict"), Limit: lim})
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	s.nameEvents(ev)
	jsonOut(w, ev)
}

// clientFw lists firewall events involving the client's current and historic IPs.
func (s *Server) clientFw(w http.ResponseWriter, r *http.Request) {
	mac := store.NormMAC(r.PathValue("mac"))
	since, _, _ := parseRange(r)
	c, err := s.St.Client(mac)
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
