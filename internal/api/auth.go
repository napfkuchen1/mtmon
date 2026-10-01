package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
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

type sessions struct {
	mu     sync.Mutex
	tokens map[string]time.Time
	fails  map[string][]time.Time
}

func newSessions() *sessions {
	return &sessions{tokens: map[string]time.Time{}, fails: map[string][]time.Time{}}
}

var ErrRate = errors.New("too many attempts")

func (s *sessions) allow(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := time.Now().Add(-time.Minute)
	f := s.fails[ip][:0]
	for _, t := range s.fails[ip] {
		if t.After(cut) {
			f = append(f, t)
		}
	}
	s.fails[ip] = f
	return len(f) < 5
}

func (s *sessions) fail(ip string) {
	s.mu.Lock()
	s.fails[ip] = append(s.fails[ip], time.Now())
	s.mu.Unlock()
}

func (s *sessions) create() string {
	b := make([]byte, 32)
	rand.Read(b)
	tok := base64.RawURLEncoding.EncodeToString(b)
	s.mu.Lock()
	s.tokens[tok] = time.Now().Add(12 * time.Hour)
	s.mu.Unlock()
	return tok
}

func (s *sessions) valid(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.tokens[tok]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.tokens, tok)
		return false
	}
	s.tokens[tok] = time.Now().Add(12 * time.Hour) // sliding
	return true
}

func (s *sessions) drop(tok string) { s.mu.Lock(); delete(s.tokens, tok); s.mu.Unlock() }

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}
