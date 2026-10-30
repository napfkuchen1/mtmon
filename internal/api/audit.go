package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// Audit log (#192): every successful change made through the UI, logins and token use. Request bodies are never
// logged (they can hold router logins); a settings change is found by comparing the meta table before and after.

func init() {
	routeHooks = append(routeHooks, func(s *Server, m *http.ServeMux, a func(http.HandlerFunc) http.HandlerFunc) {
		m.HandleFunc("GET /api/audit", a(s.auditList))
	})
}

// actions with a stable name; every other change is logged as "<verb> <path>".
var auditNames = map[string]string{
	"POST /api/devices/provision":              "device.add",
	"POST /api/devices/readonly":               "device.add",
	"PUT /api/devices/{name}":                  "device.edit",
	"POST /api/devices/{name}/offboard":        "device.offboard",
	"POST /api/devices/{name}/features":        "fix.apply",
	"POST /api/update/apply":                   "update.install",
	"PUT /api/update/mode":                     "update.mode",
	"POST /api/system/restart":                 "system.restart",
	"POST /api/tokens":                         "token.create",
	"DELETE /api/tokens/{id}":                  "token.revoke",
	"POST /api/reset":                          "data.reset",
	"PUT /api/access":                          "settings.access",
	"PUT /api/notify":                          "settings.notify",
	"PUT /api/config-history":                  "settings.config-history",
	"PUT /api/timezone":                        "settings.timezone",
	"POST /api/enrich/reverse_dns":             "settings.reverse-dns",
	"POST /api/settings/debug_logs":            "settings.debug-logs",
	"POST /api/devices/{name}/config/snapshot": "config.snapshot",
}

// metaQuiet are internal bookkeeping keys that change without anyone editing a setting.
func metaQuiet(k string) bool {
	switch k {
	case "totp_last", "last_run_version", "notify_held", "cfgprune", "dns_bypass_seen", "baseline_since":
		return true
	}
	return strings.Contains(k, ":")
}

func metaSecret(k string) bool {
	k = strings.ToLower(k)
	return k == "notify" || strings.HasPrefix(k, "totp") || strings.Contains(k, "pass") || strings.Contains(k, "hash") || strings.Contains(k, "secret") || strings.Contains(k, "token") || strings.Contains(k, "key")
}

func auditShort(v string) string {
	v = strings.Join(strings.Fields(v), " ")
	if v == "" {
		return "–"
	}
	if r := []rune(v); len(r) > 80 {
		return string(r[:80]) + "…"
	}
	return v
}

// metaDiff describes what changed between two meta snapshots: "key: old → new", secrets masked.
func metaDiff(before, after map[string]string) string {
	var keys []string
	for k, v := range after {
		if old, ok := before[k]; (!ok || old != v) && !metaQuiet(k) {
			keys = append(keys, k)
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok && !metaQuiet(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		if metaSecret(k) {
			parts = append(parts, k+": changed (value hidden)")
			continue
		}
		parts = append(parts, k+": "+auditShort(before[k])+" → "+auditShort(after[k]))
	}
	return strings.Join(parts, "; ")
}

type statusRec struct {
	http.ResponseWriter
	status int
}

func (w *statusRec) WriteHeader(c int)           { w.status = c; w.ResponseWriter.WriteHeader(c) }
func (w *statusRec) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func auditAction(r *http.Request) string {
	if n, ok := auditNames[r.Pattern]; ok {
		return n
	}
	verb, path, _ := strings.Cut(r.Pattern, " ")
	if path == "" {
		verb, path = r.Method, r.URL.Path
	}
	return strings.ToLower(verb) + " " + strings.TrimPrefix(path, "/api/")
}

// audited runs a state-changing handler of a logged-in user and records it when it succeeded.
func (s *Server) audited(w http.ResponseWriter, r *http.Request, h http.HandlerFunc) {
	before := s.St.MetaAll()
	rec := &statusRec{ResponseWriter: w, status: 200}
	h(rec, r)
	if rec.status >= 400 {
		return
	}
	detail := metaDiff(before, s.St.MetaAll())
	var p []string
	for _, n := range []string{"name", "id", "mac"} {
		if v := r.PathValue(n); v != "" {
			p = append(p, n+"="+v)
		}
	}
	if len(p) > 0 {
		detail = strings.TrimSpace(strings.Join(p, " ") + " " + detail)
	}
	s.audit(s.Cfg.AdminUser, sourceIP(r), auditAction(r), detail)
}

var (
	auditPruneMu sync.Mutex
	auditPruned  int64
	tokenSeenMu  sync.Mutex
	tokenSeen    = map[string]time.Time{}
)

// audit writes one entry; auditing must never break the action itself, so errors are ignored.
func (s *Server) audit(user, ip, action, detail string) {
	now := time.Now().Unix()
	s.St.AddAudit(store.AuditEntry{TS: now, User: user, IP: ip, Action: action, Detail: detail})
	auditPruneMu.Lock()
	due := now-auditPruned >= 86400
	if due {
		auditPruned = now
	}
	auditPruneMu.Unlock()
	if due {
		s.St.PruneAudit(now)
	}
}

// auditTokenUse logs the use of a read-only API token, at most once an hour per token and address.
func (s *Server) auditTokenUse(r *http.Request) {
	tok := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	name := s.St.TokenName(hashToken(tok))
	ip := sourceIP(r)
	key := name + "|" + ip
	tokenSeenMu.Lock()
	last, ok := tokenSeen[key]
	if ok && time.Since(last) < time.Hour {
		tokenSeenMu.Unlock()
		return
	}
	if len(tokenSeen) > 512 {
		tokenSeen = map[string]time.Time{}
	}
	tokenSeen[key] = time.Now()
	tokenSeenMu.Unlock()
	s.audit("token:"+name, ip, "token.use", r.URL.Path)
}

// auditUser shortens a login name typed by someone who failed to log in (it is attacker input).
func auditUser(u string) string {
	u = strings.Join(strings.Fields(u), " ")
	if r := []rune(u); len(r) > 40 {
		u = string(r[:40])
	}
	return u
}

// auditList: GET /api/audit?limit=&before=&action= – newest first, `before` is the id of the last entry seen.
func (s *Server) auditList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	jsonOut(w, map[string]any{"entries": s.St.Audit(limit, before, q.Get("action")), "keep_days": store.AuditKeepDays})
}
