package api

import (
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestSessionSurvivesRestart(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := newSessions()
	a.st = st
	tok := a.create()
	b := newSessions() // "restart": fresh memory, same database
	b.st = st
	if !b.valid(tok) {
		t.Fatal("session lost across restart")
	}
	if b.valid("not-a-token") {
		t.Fatal("unknown token accepted")
	}
	b.drop(tok)
	c := newSessions()
	c.st = st
	if c.valid(tok) {
		t.Fatal("dropped session still valid after restart")
	}
}

func TestStoredSessionExpires(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := newSessions()
	a.st = st
	tok := a.create()
	b := newSessions()
	b.st = st
	b.now = func() time.Time { return time.Now().Add(sessionIdle + time.Hour) }
	if b.valid(tok) {
		t.Fatal("idle-expired session accepted")
	}
	if _, _, ok := st.LoadSession(hashToken(tok)); ok {
		t.Fatal("expired session not removed")
	}
}
