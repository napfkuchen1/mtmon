package api

import "testing"

func TestPasswordRoundtrip(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword("correct horse battery", h) {
		t.Fatal("valid password rejected")
	}
	for _, bad := range []string{"", "wrong", "correct horse batter"} {
		if CheckPassword(bad, h) {
			t.Fatalf("accepted %q", bad)
		}
	}
	for _, junk := range []string{"", "x", "argon2id$1$2", "md5$1$2$3$4$5$6"} {
		if CheckPassword("x", junk) {
			t.Fatalf("accepted junk hash %q", junk)
		}
	}
}

func TestRateLimit(t *testing.T) {
	s := newSessions()
	for i := 0; i < 5; i++ {
		if !s.allow("1.2.3.4") {
			t.Fatal("blocked too early")
		}
		s.fail("1.2.3.4")
	}
	if s.allow("1.2.3.4") {
		t.Fatal("not blocked after 5 fails")
	}
	if !s.allow("5.6.7.8") {
		t.Fatal("other IP blocked")
	}
}

func TestSessions(t *testing.T) {
	s := newSessions()
	tok := s.create()
	if !s.valid(tok) || s.valid("nope") {
		t.Fatal("session validity")
	}
	s.drop(tok)
	if s.valid(tok) {
		t.Fatal("dropped token still valid")
	}
}
