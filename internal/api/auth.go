// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
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

// Each argon2 check allocates aMemory (32 MiB). Unbounded parallel logins would let anyone who reaches the UI drive
// the process into its memory limit, so at most hashSlots checks run at once; the rest wait up to hashWait.
const (
	hashSlots = 2
	hashWait  = 10 * time.Second
)

var hashSem = make(chan struct{}, hashSlots)

// errBusy: all password-check slots stayed taken for hashWait.
var errBusy = errors.New("busy")

// checkPasswordLimited is CheckPassword behind the hashSem semaphore. It returns errBusy (and false) when no slot
// became free within hashWait or the request was cancelled meanwhile.
func checkPasswordLimited(r *http.Request, pw, enc string) (bool, error) {
	t := time.NewTimer(hashWait)
	defer t.Stop()
	select {
	case hashSem <- struct{}{}:
	case <-t.C:
		return false, errBusy
	case <-r.Context().Done():
		return false, errBusy
	}
	defer func() { <-hashSem }()
	return CheckPassword(pw, enc), nil
}

// CheckPassword verifies pw against a hash from HashPassword (constant time). Request handlers use
// checkPasswordLimited instead.
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
	cred            string // fingerprint of the password hash the session was opened with (credFingerprint)
}

type sessions struct {
	mu     sync.Mutex
	tokens map[string]session
	fails  map[string][]time.Time
	now    func() time.Time
	st     *store.Store // nil: memory only
	// credOf returns the current credential fingerprint of a role. A session whose fingerprint differs (password
	// changed, viewer removed) is no longer valid. nil: no binding (unit tests of the session mechanics).
	credOf func(role string) string
}

// credFingerprint identifies a password hash without revealing it. An account without password gets a value no
// stored session can carry.
func credFingerprint(hash string) string {
	if hash == "" {
		return "none"
	}
	h := sha256.Sum256([]byte(hash))
	return hex.EncodeToString(h[:8])
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

// reserve counts an attempt for ip before the (slow) credential check runs and reports whether it may go ahead.
// Check and count happen under one lock, so parallel requests cannot all pass before the first failure is recorded.
// A successful attempt hands its slot back with release.
func (s *sessions) reserve(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := s.now().Add(-failWindow)
	if len(s.fails) > 256 {
		for k := range s.fails {
			s.prune(k, cut)
		}
	}
	if len(s.prune(ip, cut)) >= failLimit {
		return false
	}
	s.fails[ip] = append(s.fails[ip], s.now())
	return true
}

// release gives back one attempt counted by reserve (the check succeeded).
func (s *sessions) release(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.fails[ip]
	switch {
	case len(f) > 1:
		s.fails[ip] = f[:len(f)-1]
	case len(f) == 1:
		delete(s.fails, ip)
	}
}

// open starts a session for role ("" = admin). Role and credential fingerprint are stored in one write; if the
// session cannot be stored, there is no session (the caller refuses the login).
func (s *sessions) open(role string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	cred := ""
	if s.credOf != nil {
		cred = s.credOf(role)
	}
	now := s.now()
	if s.st != nil {
		s.st.PruneSessions(now.Add(-time.Hour).Unix(), now.Add(-sessionMax).Unix())
		if err := s.st.CreateSession(hashToken(tok), now.Add(sessionIdle).Unix(), now.Unix(), role, cred); err != nil {
			return "", err
		}
	}
	s.mu.Lock()
	for k, v := range s.tokens { // drop sessions that can no longer be used (never touched again after expiry)
		if s.expired(v, now) {
			delete(s.tokens, k)
		}
	}
	s.tokens[tok] = session{idleUntil: now.Add(sessionIdle), born: now, role: role, cred: cred}
	s.mu.Unlock()
	return tok, nil
}

// create: an admin session for tests of the session mechanics (memory or a store that cannot fail).
func (s *sessions) create() string { tok, _ := s.open(""); return tok }

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
		if x, found := s.st.LoadSession(hashToken(tok)); found {
			v, ok = session{idleUntil: time.Unix(x.IdleUntil, 0), born: time.Unix(x.Born, 0), role: x.Role, cred: x.Cred}, true
		}
	}
	if !ok {
		return false
	}
	now := s.now()
	// expired, or opened with a password that is no longer the account's (changed, account removed)
	if s.expired(v, now) || (s.credOf != nil && v.cred != s.credOf(v.role)) {
		delete(s.tokens, tok)
		if s.st != nil {
			s.st.DropSession(hashToken(tok))
		}
		return false
	}
	if s.st != nil && now.Add(sessionIdle).Sub(v.idleUntil) > time.Minute { // persist the slide, but not on every request
		s.st.TouchSession(hashToken(tok), now.Add(sessionIdle).Unix())
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

// sourceIP is the address the request really comes from: the peer itself, or – behind a trusted proxy (loopback or
// Settings → Reverse proxy → trusted proxies) – the last address in X-Forwarded-For that is not itself a trusted
// proxy hop (X-Real-IP as fallback). Rate limits use it so one noisy client does not lock out everybody behind the
// same proxy. Any other peer is used as is: a LAN client must not pick its own rate-limit bucket via headers.
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
		if err == nil && !isTrustedProxy(a) {
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
