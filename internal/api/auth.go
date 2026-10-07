package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

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

// Sessions live in memory (a restart logs everybody out). A session expires after sessionIdle without use and in any
// case sessionMax after login, even if it is used continuously.
const (
	sessionIdle = 12 * time.Hour
	sessionMax  = 7 * 24 * time.Hour
	failWindow  = time.Minute
	failLimit   = 5
)

type session struct{ idleUntil, born time.Time }

type sessions struct {
	mu     sync.Mutex
	tokens map[string]session
	fails  map[string][]time.Time
	now    func() time.Time
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
	return tok
}

func (s *sessions) expired(v session, now time.Time) bool {
	return now.After(v.idleUntil) || now.After(v.born.Add(sessionMax))
}

func (s *sessions) valid(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.tokens[tok]
	if !ok {
		return false
	}
	now := s.now()
	if s.expired(v, now) {
		delete(s.tokens, tok)
		return false
	}
	v.idleUntil = now.Add(sessionIdle) // sliding, but never beyond born+sessionMax
	s.tokens[tok] = v
	return true
}

func (s *sessions) drop(tok string) { s.mu.Lock(); delete(s.tokens, tok); s.mu.Unlock() }

// sameOrigin reports whether the Origin header of a browser request names exactly this server (scheme is not
// compared: the listener decides http/https). An empty Origin (non-browser client) is the caller's decision.
func sameOrigin(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}
