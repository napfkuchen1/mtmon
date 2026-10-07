package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ---- reverse-proxy friendliness ----------------------------------------------------------------------------------
// Behind nginx & co. the browser's Origin names the public host while r.Host is whatever the proxy forwards. An
// origin is accepted when it equals the request Host, the X-Forwarded-Host of a proxy on a private address, or the
// "public URL" the operator entered in Settings. Everything else (a foreign site) is still refused.

// proxyTrusted: forwarded-* headers are only honoured from loopback / private peers (a proxy in front of mtmon).
func proxyTrusted(r *http.Request) bool {
	a, err := netip.ParseAddr(clientIP(r))
	return err == nil && isLocalAddr(a)
}

func (s *Server) publicHost() string {
	if s.St == nil {
		return ""
	}
	u, err := url.Parse(s.St.GetMeta("public_url"))
	if err != nil {
		return ""
	}
	return u.Host
}

func (s *Server) originOK(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	hosts := []string{r.Host, s.publicHost()}
	if proxyTrusted(r) {
		hosts = append(hosts, strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0]))
	}
	for _, h := range hosts {
		if h != "" && sameOrigin(o, h) {
			return true
		}
	}
	return false
}

// secureRequest: the browser talks HTTPS to mtmon itself or to a trusted proxy in front of it.
func (s *Server) secureRequest(r *http.Request) bool {
	return s.TLS || (proxyTrusted(r) && strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https"))
}

// accessInfo: GET /api/access – listener settings and what mtmon sees of *this* request (to debug a proxy).
func (s *Server) accessInfo(w http.ResponseWriter, r *http.Request) {
	_, port, _ := net.SplitHostPort(s.Cfg.Listen)
	fh := r.Header.Get("X-Forwarded-Host")
	jsonOut(w, map[string]any{
		"listen": s.Cfg.Listen, "port": port, "tls": s.TLS,
		"public_url": s.St.GetMeta("public_url"), "port_override": s.St.GetMeta("listen_port"),
		"can_restart": s.Restart != nil,
		"request": map[string]any{
			"remote": clientIP(r), "host": r.Host, "origin": r.Header.Get("Origin"),
			"forwarded_host": fh, "forwarded_proto": r.Header.Get("X-Forwarded-Proto"), "forwarded_for": r.Header.Get("X-Forwarded-For"),
			"via_proxy":     fh != "" || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Forwarded-Proto") != "",
			"trusted_proxy": proxyTrusted(r), "origin_ok": s.originOK(r), "secure": s.secureRequest(r),
		},
	})
}

// accessSave: PUT /api/access {public_url, listen_port}
func (s *Server) accessSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PublicURL  string `json:"public_url"`
		ListenPort int    `json:"listen_port"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	pu := strings.TrimRight(strings.TrimSpace(in.PublicURL), "/")
	if pu != "" {
		u, err := url.Parse(pu)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
			jerr(w, 400, "public URL must look like https://mtmon.example.com (no path)")
			return
		}
	}
	if in.ListenPort != 0 && (in.ListenPort < 1024 || in.ListenPort > 65535) {
		jerr(w, 400, "port must be between 1024 and 65535 (mtmon runs without root rights)")
		return
	}
	port := ""
	if in.ListenPort != 0 {
		port = strconv.Itoa(in.ListenPort)
	}
	if err := s.St.SetMeta("public_url", pu); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	if err := s.St.SetMeta("listen_port", port); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	s.accessInfo(w, r)
}

// systemRestart: POST /api/system/restart – systemd starts mtmon again (same path as an update restart).
func (s *Server) systemRestart(w http.ResponseWriter, r *http.Request) {
	if s.Restart == nil {
		jerr(w, 501, "restart is not available here")
		return
	}
	jsonOut(w, map[string]bool{"ok": true})
	go func() { time.Sleep(500 * time.Millisecond); s.Restart() }()
}

// ---- read-only API tokens ------------------------------------------------------------------------------------------

func hashToken(tok string) string { h := sha256.Sum256([]byte(tok)); return hex.EncodeToString(h[:]) }

// bearerOK: a valid API token on a read-only request (GET/HEAD) – never on token / access management.
func (s *Server) bearerOK(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") || (r.Method != "GET" && r.Method != "HEAD") {
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/api/tokens") || strings.HasPrefix(r.URL.Path, "/api/access") {
		return false
	}
	ip := clientIP(r)
	if !s.sess.allow(ip) {
		return false
	}
	if !s.St.TokenValid(hashToken(strings.TrimSpace(h[7:]))) {
		s.sess.fail(ip)
		return false
	}
	return true
}

func (s *Server) tokensList(w http.ResponseWriter, r *http.Request) {
	t, err := s.St.Tokens()
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, t)
}

// tokensCreate: POST /api/tokens {name} → {id, token}. The token is shown exactly once; only its hash is stored.
func (s *Server) tokensCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 40 {
		jerr(w, 400, "give the token a name (max 40 characters)")
		return
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		jerr(w, 500, "no randomness available")
		return
	}
	tok := "mtm_" + hex.EncodeToString(b)
	id, err := s.St.CreateToken(in.Name, hashToken(tok), tok[:8])
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]any{"id": id, "token": tok})
}

func (s *Server) tokensDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.St.DeleteToken(id); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]bool{"ok": true})
}

// ---- client icon ---------------------------------------------------------------------------------------------------

var iconNames = map[string]bool{"": true, "phone": true, "tablet": true, "laptop": true, "desktop": true, "tv": true, "printer": true, "printer3d": true,
	"speaker": true, "camera": true, "iot": true, "server": true, "network": true, "ap": true, "console": true, "bulb": true, "watch": true, "car": true, "generic": true}

// clientIcon: PUT /api/clients/{mac}/icon {icon} – "" = automatic.
func (s *Server) clientIcon(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Icon string `json:"icon"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !iconNames[in.Icon] {
		jerr(w, 400, "unknown icon")
		return
	}
	if err := s.St.SetIcon(r.PathValue("mac"), in.Icon); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]bool{"ok": true})
}
