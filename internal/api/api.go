// Package api serves the JSON/WebSocket API and the embedded UI.
package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/enrich"
	"github.com/napfkuchen1/mtmon/internal/flow"
	"github.com/napfkuchen1/mtmon/internal/live"
	"github.com/napfkuchen1/mtmon/internal/poller"
	"github.com/napfkuchen1/mtmon/internal/secret"
	"github.com/napfkuchen1/mtmon/internal/store"
	"github.com/napfkuchen1/mtmon/internal/syslog"
	"github.com/napfkuchen1/mtmon/internal/update"
)

type Server struct {
	Cfg     *config.Config
	St      *store.Store
	Hub     *live.Hub
	En      *enrich.Enricher
	Col     *flow.Collector
	Pipe    *live.Pipeline
	UI      fs.FS // embedded web/dist (may be nil in tests)
	Version string
	Started time.Time
	sess    *sessions
	mux     *http.ServeMux
	up      websocket.Upgrader
	TLS     bool

	Box     *secret.Box        // credential encryption (nil in tests that do not use device management)
	Mgr     *poller.Manager    // device workers; Reload() after device changes
	Cls     *enrich.Classifier // service classification
	Sys     *syslog.Server     // firewall log receiver
	Upd     *update.Manager    // self-update (nil in tests that do not use it)
	provMu  sync.Mutex         // one provisioning / offboarding at a time
	reclass atomic.Bool        // service relabelling in progress
	adv     adviceState        // cached suggestion evaluation (suggestions.go)
}

func New(s *Server) http.Handler {
	s.sess = newSessions()
	s.Started = time.Now()
	s.up = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
		o := r.Header.Get("Origin")
		return o == "" || sameOrigin(o, r.Host)
	}}
	m := http.NewServeMux()
	s.mux = m
	m.HandleFunc("POST /api/login", s.login)
	m.HandleFunc("POST /api/logout", s.logout)
	a := s.auth
	m.HandleFunc("GET /api/me", s.me)
	m.HandleFunc("GET /api/overview", a(s.overview))
	m.HandleFunc("GET /api/devices", a(s.devices))
	s.mgmtRoutes(m, a)
	s.featureRoutes(m, a) // clients clean-up, app detection
	s.updateRoutes(m, a)
	s.adviceRoutes(m, a) // suggestions
	m.HandleFunc("GET /api/devices/{name}", a(s.deviceDetail))
	m.HandleFunc("GET /api/clients", a(s.clients))
	m.HandleFunc("GET /api/clients/{mac}", a(s.clientDetail))
	m.HandleFunc("GET /api/clients/{mac}/connections", a(s.clientConns))
	m.HandleFunc("PUT /api/clients/{mac}/label", a(s.setLabel))
	m.HandleFunc("GET /api/top/{what}", a(s.top))
	m.HandleFunc("GET /api/series", a(s.series))
	m.HandleFunc("GET /api/topology", a(s.topology))
	m.HandleFunc("GET /api/alerts", a(s.alerts))
	m.HandleFunc("POST /api/alerts/{id}/ack", a(s.ack))
	m.HandleFunc("GET /api/setup", a(s.setup))
	m.HandleFunc("GET /api/system", a(s.system))
	m.HandleFunc("GET /api/export/top/{what}", a(s.exportTop))
	m.HandleFunc("GET /api/export/connections/{mac}", a(s.exportConns))
	m.HandleFunc("GET /api/live", a(s.liveWS))
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	m.HandleFunc("/", s.static)
	return secure(m)
}

// csp: everything from this origin only (the UI is one embedded bundle); no framing, no <base>, forms only to ourselves.
// style-src needs 'unsafe-inline' because Svelte sets element styles; scripts stay strictly 'self'.
const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; " +
	"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func secure(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", csp)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if s.UI == nil {
		http.NotFound(w, r)
		return
	}
	p := strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}
	if f, err := s.UI.Open(p); err == nil {
		f.Close()
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		http.ServeFileFS(w, r, s.UI, p)
		return
	}
	http.ServeFileFS(w, r, s.UI, "index.html") // SPA fallback
}

// ---- helpers ----

func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func jerr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

var ranges = map[string]struct {
	d      time.Duration
	bucket int64
}{
	"live": {5 * time.Minute, 60},
	"1h":   {time.Hour, 60},
	"24h":  {24 * time.Hour, 900},
	"7d":   {7 * 24 * time.Hour, 3600},
	"30d":  {30 * 24 * time.Hour, 14400},
}

func parseRange(r *http.Request) (since int64, bucket int64, name string) {
	name = r.URL.Query().Get("range")
	rg, ok := ranges[name]
	if !ok {
		name, rg = "1h", ranges["1h"]
	}
	return time.Now().Add(-rg.d).Unix(), rg.bucket, name
}

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("mtmon_session")
		if err != nil || !s.sess.valid(c.Value) {
			jerr(w, 401, "unauthorized")
			return
		}
		// CSRF: state-changing requests must be same-origin JSON (SameSite=Strict cookie is the primary defence)
		if r.Method != "GET" && r.Method != "HEAD" {
			if o := r.Header.Get("Origin"); o != "" && !sameOrigin(o, r.Host) {
				jerr(w, 403, "cross-origin request blocked")
				return
			}
		}
		h(w, r)
	}
}

// ---- auth ----

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.sess.allow(ip) {
		jerr(w, 429, "too many attempts, wait a minute")
		return
	}
	var in struct{ User, Password string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		jerr(w, 400, "bad request")
		return
	}
	if s.Cfg.AdminHash == "" || in.User != s.Cfg.AdminUser || !CheckPassword(in.Password, s.Cfg.AdminHash) {
		s.sess.fail(ip)
		jerr(w, 401, "invalid credentials")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "mtmon_session", Value: s.sess.create(), Path: "/", HttpOnly: true,
		Secure: s.TLS, SameSite: http.SameSiteStrictMode, MaxAge: 12 * 3600})
	jsonOut(w, map[string]string{"user": in.User})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("mtmon_session"); err == nil {
		s.sess.drop(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "mtmon_session", Value: "", Path: "/", MaxAge: -1})
	jsonOut(w, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie("mtmon_session")
	if err != nil || !s.sess.valid(c.Value) {
		jsonOut(w, map[string]any{"user": nil}) // 200 so the login screen causes no console noise
		return
	}
	jsonOut(w, map[string]any{"user": s.Cfg.AdminUser, "version": s.Version})
}

// ---- data ----

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	since, bucket, rn := parseRange(r)
	up, down, flows := s.St.Totals(since)
	on, total := s.St.ClientCounts()
	devs := s.Hub.Devices()
	du := 0
	for _, d := range devs {
		if d.Up {
			du++
		}
	}
	series, _ := s.St.Series(since, bucket, "")
	jsonOut(w, map[string]any{
		"range": rn, "bytes_up": up, "bytes_down": down, "flows": flows,
		"clients_online": on, "clients_total": total,
		"devices_up": du, "devices_total": len(s.Cfg.AllDevices()), "alerts_open": s.St.OpenAlerts(),
		"series":        series,
		"top_clients":   must(s.St.TopBy("clients", since, 5, "")),
		"top_hosts":     must(s.St.TopBy("hosts", since, 5, "")),
		"top_services":  must(s.St.TopBy("services", since, 5, "")),
		"top_countries": must(s.St.TopBy("country", since, 5, "")),
	})
}

func must[T any](v T, err error) T { return v }

func (s *Server) deviceDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	dd, found := s.Cfg.Device(name)
	if !found {
		jerr(w, 404, "unknown device")
		return
	}
	dev := &dd
	since, _, rn := parseRange(r)
	states, _ := s.St.DeviceStates()
	cl, _ := s.St.Clients()
	mine := []store.Client{}
	for _, c := range cl {
		if c.Device == name && c.Online {
			mine = append(mine, c)
		}
	}
	ns, _ := s.St.Neighbors()
	nb := []store.Neighbor{}
	for _, n := range ns {
		if n.Device == name {
			nb = append(nb, n)
		}
	}
	var caps json.RawMessage
	if row, _ := s.St.DeviceRow(name); row != nil {
		caps = row.Caps
	}
	rules, _ := s.St.FwRules()
	fwOn := false
	for _, ru := range rules {
		fwOn = fwOn || ru.Device == name
	}
	jsonOut(w, map[string]any{"name": name, "addr": dev.Addr, "role": dev.Role, "range": rn,
		"caps": caps, "managed": dev.Managed, "source": map[bool]string{true: "ui", false: "config"}[dev.FromUI], "firewall_logging": fwOn,
		"state": states[name], "ifaces": s.Hub.Ifaces(name), "clients": mine, "neighbors": nb,
		"metrics": must(s.St.DevMetrics(name, since))})
}

func (s *Server) clients(w http.ResponseWriter, r *http.Request) {
	cl, err := s.St.Clients()
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	since, _, _ := parseRange(r)
	rates := map[string]live.ClientRate{}
	for _, c := range s.Hub.Snapshot(s.Hub.LastSeq(), 1000).Clients {
		rates[c.Key] = c
	}
	usage := map[string][2]int64{}
	for _, t := range must(s.St.TopBy("clients", since, 10000, "")) {
		usage[t.Key] = [2]int64{t.Up, t.Down}
	}
	out := make([]map[string]any, 0, len(cl))
	for _, c := range cl {
		rt := rates[c.MAC]
		u := usage[c.MAC]
		out = append(out, map[string]any{"client": c, "up_bps": rt.UpBps, "down_bps": rt.DownBps, "bytes_up": u[0], "bytes_down": u[1]})
	}
	jsonOut(w, out)
}

func (s *Server) clientDetail(w http.ResponseWriter, r *http.Request) {
	mac := store.NormMAC(r.PathValue("mac"))
	c, err := s.St.Client(mac)
	if err != nil {
		jerr(w, 404, "unknown client")
		return
	}
	since, bucket, rn := parseRange(r)
	// "first seen" is only meaningful once we have watched this client for >24 h, else everything is new
	nd := []string{}
	if time.Since(time.Unix(c.FirstSeen, 0)) > 24*time.Hour {
		nd, _ = s.St.NewDests(mac, int64(ranges["24h"].d.Seconds()))
	}
	jsonOut(w, map[string]any{
		"client": c, "range": rn,
		"ip_history":       must(s.St.IPHistory(mac)),
		"roams":            must(s.St.Roams(mac)),
		"series":           must(s.St.Series(since, bucket, mac)),
		"top_hosts":        must(s.St.TopBy("hosts", since, 10, mac)),
		"top_services":     must(s.St.TopBy("services", since, 10, mac)),
		"top_ports":        must(s.St.TopBy("ports", since, 10, mac)),
		"top_countries":    must(s.St.TopBy("country", since, 8, mac)),
		"new_destinations": nd,
	})
}

// connFilter reads the optional drill-down filters: svc, rip, port ("proto/port" as in the ports top list), cc.
func connFilter(r *http.Request) store.ConnFilter {
	q := r.URL.Query()
	f := store.ConnFilter{Svc: q.Get("svc"), RIP: q.Get("rip"), CC: strings.ToUpper(q.Get("cc"))}
	if p, pt, ok := strings.Cut(q.Get("port"), "/"); ok {
		f.Proto, _ = strconv.Atoi(p)
		f.RPort, _ = strconv.Atoi(pt)
	}
	return f
}

func (s *Server) clientConns(w http.ResponseWriter, r *http.Request) {
	since, _, _ := parseRange(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	c, err := s.St.ConnectionsFiltered(r.PathValue("mac"), since, limit, after, connFilter(r))
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	s.annotateConns(r.PathValue("mac"), c, since)
	jsonOut(w, c)
}

func (s *Server) setLabel(w http.ResponseWriter, r *http.Request) {
	var in struct{ Label string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil || len(in.Label) > 64 {
		jerr(w, 400, "bad request")
		return
	}
	mac := store.NormMAC(r.PathValue("mac"))
	if err := s.St.SetLabel(mac, in.Label); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	if in.Label != "" {
		s.Hub.SetName(mac, in.Label)
	}
	jsonOut(w, map[string]bool{"ok": true})
}

func (s *Server) top(w http.ResponseWriter, r *http.Request) {
	since, _, rn := parseRange(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 20
	}
	rows, err := s.St.TopBy(r.PathValue("what"), since, limit, r.URL.Query().Get("mac"))
	if err != nil {
		jerr(w, 400, err.Error())
		return
	}
	jsonOut(w, map[string]any{"range": rn, "rows": rows})
}

func (s *Server) series(w http.ResponseWriter, r *http.Request) {
	since, bucket, _ := parseRange(r)
	p, err := s.St.Series(since, bucket, r.URL.Query().Get("mac"))
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, p)
}

func (s *Server) alerts(w http.ResponseWriter, r *http.Request) {
	a, err := s.St.Alerts(200)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, a)
}

func (s *Server) ack(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.St.AckAlert(id); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]bool{"ok": true})
}

// topology returns nodes (devices + online clients) and edges for the map view.
func (s *Server) topology(w http.ResponseWriter, r *http.Request) {
	type node struct {
		ID     string  `json:"id"`
		Type   string  `json:"type"` // router | ap | client
		Label  string  `json:"label"`
		Status string  `json:"status"`
		Bps    float64 `json:"bps"`
		Sub    string  `json:"sub"`
		WiFi   bool    `json:"wifi"`
	}
	type edge struct {
		From string `json:"from"`
		To   string `json:"to"`
		Kind string `json:"kind"`
	}
	var nodes []node
	var edges []edge
	devByName := map[string]live.DevLive{}
	for _, d := range s.Hub.Devices() {
		devByName[d.Name] = d
	}
	roleOf := map[string]string{}
	var root string
	for _, d := range s.Cfg.AllDevices() {
		roleOf[d.Name] = d.Role
		st := "down"
		if devByName[d.Name].Up {
			st = "up"
		}
		var bps float64
		for _, i := range s.Hub.Ifaces(d.Name) {
			if i.Type == "ether" {
				bps += i.RxBps + i.TxBps
			}
		}
		nodes = append(nodes, node{ID: "d:" + d.Name, Type: d.Role, Label: d.Name, Status: st, Bps: bps, Sub: d.Addr})
		if d.Role == "router" && root == "" {
			root = d.Name
		}
	}
	// device↔device links from /ip/neighbor (identity match)
	ns, _ := s.St.Neighbors()
	seen := map[string]bool{}
	for _, n := range ns {
		if _, ok := roleOf[n.Ident]; ok && n.Ident != n.Device {
			a, b := n.Device, n.Ident
			if a > b {
				a, b = b, a
			}
			if !seen[a+"|"+b] {
				seen[a+"|"+b] = true
				edges = append(edges, edge{From: "d:" + n.Device, To: "d:" + n.Ident, Kind: "uplink"})
			}
		}
	}
	// APs without a discovered uplink hang off the first router
	linked := map[string]bool{}
	for _, e := range edges {
		linked[e.From], linked[e.To] = true, true
	}
	for _, d := range s.Cfg.AllDevices() {
		if d.Name != root && root != "" && !linked["d:"+d.Name] {
			edges = append(edges, edge{From: "d:" + root, To: "d:" + d.Name, Kind: "uplink"})
		}
	}
	rates := map[string]float64{}
	for _, c := range s.Hub.Snapshot(s.Hub.LastSeq(), 1000).Clients {
		rates[c.Key] = c.UpBps + c.DownBps
	}
	cl, _ := s.St.Clients()
	for _, c := range cl {
		if !c.Online {
			continue
		}
		name := ClientName(c)
		sub := c.IP
		if c.WiFi && c.Signal != 0 {
			sub = fmt.Sprintf("%s · %d dBm", c.IP, c.Signal)
		}
		nodes = append(nodes, node{ID: "c:" + c.MAC, Type: "client", Label: name, Status: "up", Bps: rates[c.MAC], Sub: sub, WiFi: c.WiFi})
		if c.Device != "" {
			edges = append(edges, edge{From: "d:" + c.Device, To: "c:" + c.MAC, Kind: map[bool]string{true: "wifi", false: "wired"}[c.WiFi]})
		}
	}
	jsonOut(w, map[string]any{"nodes": nodes, "edges": edges})
}

func localIP(r *http.Request) string {
	if a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if h, _, err := net.SplitHostPort(a.String()); err == nil {
			return h
		}
	}
	return "MTMON_IP"
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	ip := localIP(r)
	_, port, _ := net.SplitHostPort(s.Cfg.FlowListen)
	if port == "" {
		port = "2055"
	}
	jsonOut(w, map[string]any{
		"mtmon_ip": ip, "flow_port": port,
		"flow": "/ip traffic-flow set enabled=yes interfaces=all cache-entries=16k active-flow-timeout=30s inactive-flow-timeout=15s\n" +
			"/ip traffic-flow target add dst-address=" + ip + " port=" + port + " version=ipfix",
		"api_user": "/user group add name=mtmon-ro policy=read,api,rest-api,!local,!telnet,!ssh,!ftp,!reboot,!write,!policy,!test,!winbox,!password,!web,!sniff,!sensitive,!romon\n" +
			"/user add name=mtmon group=mtmon-ro password=CHANGE_ME address=" + ip + "/32",
		"rest_tls": "/certificate add name=mtmon-ca common-name=mtmon-ca days-valid=3650 key-usage=key-cert-sign,crl-sign\n" +
			"/certificate sign mtmon-ca\n" +
			"/certificate add name=mtmon-ssl common-name=<ROUTER-IP> subject-alt-name=IP:<ROUTER-IP> days-valid=3650 key-usage=digital-signature,key-encipherment,tls-server\n" +
			"/certificate sign mtmon-ssl ca=mtmon-ca\n" +
			"/ip service set www-ssl certificate=mtmon-ssl disabled=no address=" + ip + "/32",
		"fasttrack": "Traffic-Flow only sees traffic that is processed by the router CPU (MikroTik docs: hardware-offloaded traffic, e.g. bridge HW offload/switch-chip forwarding, is not exported). " +
			"FastTrack-ed connections also bypass most of the packet path, so their flows can be missing or incomplete (community-reported; verify on your router). " +
			"Check: /ip firewall filter print where action=fasttrack-connection  and  /interface bridge port print (hw=yes). " +
			"If you need full visibility: disable the fasttrack rule and/or HW offload for the bridge (more CPU), or accept that only CPU-processed traffic (typically all routed/NAT traffic) is shown.",
		"fingerprint": "mtmon fingerprint <router-ip>:443   → put the result into device.fingerprint",
		"enrich":      s.En.Status(),
		"exporters":   s.exporterStatus(),
	})
}

func (s *Server) exporterStatus() []string {
	var out []string
	for _, d := range s.Cfg.AllDevices() {
		out = append(out, d.Name+" ("+d.Addr+")")
	}
	return out
}

func (s *Server) system(w http.ResponseWriter, r *http.Request) {
	var dbSize int64
	if fi, err := os.Stat(filepath.Join(s.Cfg.DataDir, "mtmon.db")); err == nil {
		dbSize = fi.Size()
	}
	var pk, rec, er, nt uint64
	if d := s.Col.Dec; d != nil {
		pk, rec, er, nt = d.Stats()
	}
	jsonOut(w, map[string]any{
		"version": s.Version, "uptime_s": int64(time.Since(s.Started).Seconds()),
		"flow_packets": pk, "flow_records": rec, "flow_errors": er, "flow_no_template": nt,
		"flow_dropped_unknown_exporter": s.Col.Dropped.Load(),
		"flows_ps":                      s.Hub.FlowsPS, "queue": s.St.QueueLen(), "db_dropped": s.St.Dropped,
		"ignored_transit": s.Pipe.IgnoredCount(), "db_bytes": dbSize, "enrich": s.En.Status(),
		"retention_days": s.Cfg.Retention, "raw_retention_days": s.Cfg.RawRetention,
	})
}

func (s *Server) exportTop(w http.ResponseWriter, r *http.Request) {
	since, _, rn := parseRange(r)
	rows, err := s.St.TopBy(r.PathValue("what"), since, 10000, r.URL.Query().Get("mac"))
	if err != nil {
		jerr(w, 400, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="top-%s-%s.csv"`, sanitize(r.PathValue("what")), rn))
	cw := csv.NewWriter(w)
	cw.Write([]string{"key", "label", "detail", "bytes_up", "bytes_down", "bytes_internal", "flows"})
	for _, t := range rows {
		cw.Write([]string{csvSafe(t.Key), csvSafe(t.Label), csvSafe(t.Sub), i(t.Up), i(t.Down), i(t.Internal), i(t.Flows)})
	}
	cw.Flush()
}

func (s *Server) exportConns(w http.ResponseWriter, r *http.Request) {
	since, _, _ := parseRange(r)
	rows, err := s.St.ConnectionsFiltered(r.PathValue("mac"), since, 100000, 0, connFilter(r))
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="connections.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"time", "dir", "remote_ip", "remote_port", "proto", "service", "host", "country", "asn", "bytes", "packets"})
	for _, c := range rows {
		cw.Write([]string{time.Unix(c.TS, 0).Format(time.RFC3339), c.Dir, c.RIP, strconv.Itoa(c.RPort), enrich.ProtoName(uint8(c.Proto)),
			csvSafe(c.Svc), csvSafe(c.Host), c.CC, strconv.Itoa(c.ASN), i(c.Bytes), i(c.Pkts)})
	}
	cw.Flush()
}

func i(n int64) string { return strconv.FormatInt(n, 10) }

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '_'
	}, s)
}

// csvSafe neutralises spreadsheet formula injection (hostnames come from the network).
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// ---- websocket ----

func (s *Server) liveWS(w http.ResponseWriter, r *http.Request) {
	c, err := s.up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close()
	c.SetReadLimit(1024) // the only message the client sends is {"Pause":bool}
	done := make(chan struct{})
	paused := make(chan bool, 4)
	go func() { // reader: handles close + pause/resume messages
		defer close(done)
		for {
			_, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			var m struct{ Pause bool }
			if json.Unmarshal(msg, &m) == nil {
				select {
				case paused <- m.Pause:
				default:
				}
			}
		}
	}()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	since := s.Hub.LastSeq()
	for p := false; ; {
		select {
		case <-done:
			return
		case p = <-paused:
		case <-t.C:
			snap := s.Hub.Snapshot(since, 25)
			if len(snap.Conns) > 0 {
				since = snap.Conns[0].Seq
			}
			if p {
				snap.Conns = nil
			}
			c.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := c.WriteJSON(snap); err != nil {
				return
			}
		}
	}
}

var _ = sort.Strings
