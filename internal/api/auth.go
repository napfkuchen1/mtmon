// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
	"golang.org/x/crypto/argon2"
)

const (
	aTime    = 2
	aMemory  = 32 * 1024
	aThreads = 2
	aKeyLen  = 32
)

// HashPassword returns an argon2id hash string.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, aTime, aMemory, aThreads, aKeyLen)
	return fmt.Sprintf("argon2id$%d$%d$%d$%s$%s", aTime, aMemory, aThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// CheckPassword verifies pw against a hash from HashPassword (constant time).
func CheckPassword(pw, enc string) bool {
	p := strings.Split(enc, "$")
	if len(p) != 6 || p[0] != "argon2id" {
		return false
	}
	var t, m uint32
	var th uint8
	fmt.Sscan(p[1], &t)
	fmt.Sscan(p[2], &m)
	var thr uint32
	fmt.Sscan(p[3], &thr)
	th = uint8(thr)
	salt, err1 := base64.RawStdEncoding.DecodeString(p[4])
	want, err2 := base64.RawStdEncoding.DecodeString(p[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, th, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// Sessions live in memory and, when st is set, also in the database (hash only), so a restart or update keeps
// everybody logged in. A session expires after sessionIdle without use and in any
// case sessionMax after login, even if it is used continuously.
const (
	sessionIdle = 12 * time.Hour
	sessionMax  = 7 * 24 * time.Hour
	failWindow  = time.Minute
	failLimit   = 5
)

type session struct {
	idleUntil, born time.Time
	role            string // "" = admin, "viewer" = read-only account
}

type sessions struct {
	mu     sync.Mutex
	tokens map[string]session
	fails  map[string][]time.Time
	now    func() time.Time
	st     *store.Store // nil: memory only
}

func newSessions() *sessions {
	return &sessions{tokens: map[string]session{}, fails: map[string][]time.Time{}, now: time.Now}
}

var ErrRate = errors.New("too many attempts")

// allow reports whether ip may try to log in. Stale failure records are removed here, so the map cannot grow
// without bound when many different addresses fail once.
func (s *sessions) allow(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := s.now().Add(-failWindow)
	if len(s.fails) > 256 {
		for k := range s.fails {
			s.prune(k, cut)
		}
	}
	return len(s.prune(ip, cut)) < failLimit
}

func (s *sessions) prune(ip string, cut time.Time) []time.Time {
	f := s.fails[ip][:0]
	for _, t := range s.fails[ip] {
		if t.After(cut) {
			f = append(f, t)
		}
	}
	if len(f) == 0 {
		delete(s.fails, ip)
		return nil
	}
	s.fails[ip] = f
	return f
}

func (s *sessions) fail(ip string) {
	s.mu.Lock()
	s.fails[ip] = append(s.fails[ip], s.now())
	s.mu.Unlock()
}

func (s *sessions) create() string {
	b := make([]byte, 32)
	rand.Read(b)
	tok := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	now := s.now()
	for k, v := range s.tokens { // drop sessions that can no longer be used (never touched again after expiry)
		if s.expired(v, now) {
			delete(s.tokens, k)
		}
	}
	s.tokens[tok] = session{idleUntil: now.Add(sessionIdle), born: now}
	s.mu.Unlock()
	if s.st != nil {
		s.st.PruneSessions(now.Add(-time.Hour).Unix(), now.Add(-sessionMax).Unix())
		s.st.SaveSession(hashToken(tok), now.Add(sessionIdle).Unix(), now.Unix())
	}
	return tok
}

// createRole starts a session for a non-admin account.
func (s *sessions) createRole(role string) string {
	tok := s.create()
	s.mu.Lock()
	v := s.tokens[tok]
	v.role = role
	s.tokens[tok] = v
	s.mu.Unlock()
	if s.st != nil {
		s.st.SetSessionRole(hashToken(tok), role)
	}
	return tok
}

// roleOf is the role of a (valid) session.
func (s *sessions) roleOf(tok string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens[tok].role
}

func (s *sessions) expired(v session, now time.Time) bool {
	return now.After(v.idleUntil) || now.After(v.born.Add(sessionMax))
}

func (s *sessions) valid(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.tokens[tok]
	if !ok && s.st != nil { // not known in memory: a session from before the restart
		if idle, born, found := s.st.LoadSession(hashToken(tok)); found {
			v, ok = session{idleUntil: time.Unix(idle, 0), born: time.Unix(born, 0), role: s.st.SessionRole(hashToken(tok))}, true
		}
	}
	if !ok {
		return false
	}
	now := s.now()
	if s.expired(v, now) {
		delete(s.tokens, tok)
		if s.st != nil {
			s.st.DropSession(hashToken(tok))
		}
		return false
	}
	if s.st != nil && now.Add(sessionIdle).Sub(v.idleUntil) > time.Minute { // persist the slide, but not on every request
		s.st.SaveSession(hashToken(tok), now.Add(sessionIdle).Unix(), v.born.Unix())
	}
	v.idleUntil = now.Add(sessionIdle) // sliding, but never beyond born+sessionMax
	s.tokens[tok] = v
	return true
}

func (s *sessions) drop(tok string) {
	s.mu.Lock()
	delete(s.tokens, tok)
	s.mu.Unlock()
	if s.st != nil {
		s.st.DropSession(hashToken(tok))
	}
}

// sameOrigin reports whether the Origin header of a browser request names exactly this server (scheme is not
// compared: the listener decides http/https). An empty Origin (non-browser client) is the caller's decision.
func sameOrigin(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

// sourceIP is the address the request really comes from: the peer itself, or – behind a trusted (private) proxy –
// the last address in X-Forwarded-For that is not itself a local proxy hop (X-Real-IP as fallback). Rate limits use
// it so one noisy client does not lock out everybody behind the same proxy.
func sourceIP(r *http.Request) string {
	peer := clientIP(r)
	if !proxyTrusted(r) {
		return peer
	}
	var hops []string
	for _, p := range strings.Split(r.Header.Get("X-Forwarded-For"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			hops = append(hops, p)
		}
	}
	if len(hops) == 0 {
		if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
			hops = append(hops, v)
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(hops[i])
		if err == nil && !isLocalAddr(a) {
			return a.String()
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		if a, err := netip.ParseAddr(hops[i]); err == nil {
			return a.String()
		}
	}
	return peer
}

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}
