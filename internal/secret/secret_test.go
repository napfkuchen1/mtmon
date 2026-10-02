package secret

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	b, err := New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	enc := b.Seal("s3cret")
	if bytes.Contains(enc, []byte("s3cret")) {
		t.Fatal("ciphertext contains plaintext")
	}
	got, err := b.Open(enc)
	if err != nil || got != "s3cret" {
		t.Fatalf("got %q, %v", got, err)
	}
	if bytes.Equal(enc, b.Seal("s3cret")) {
		t.Fatal("nonce reused: identical ciphertexts")
	}
}

func TestOpenRejectsTamperedAndShort(t *testing.T) {
	b, _ := New(bytes.Repeat([]byte{1}, 32))
	enc := b.Seal("x")
	enc[len(enc)-1] ^= 1
	if _, err := b.Open(enc); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	if _, err := b.Open([]byte{1, 2}); err == nil {
		t.Fatal("short ciphertext accepted")
	}
}

func TestWrongKeyFails(t *testing.T) {
	a, _ := New(bytes.Repeat([]byte{1}, 32))
	c, _ := New(bytes.Repeat([]byte{2}, 32))
	if _, err := c.Open(a.Seal("x")); err == nil {
		t.Fatal("decrypted with wrong key")
	}
}

func TestOpenCreatesKeyFile0600AndReuses(t *testing.T) {
	dir := t.TempDir()
	b1, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "secret.key"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", st, err)
	}
	b2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := b2.Open(b1.Seal("persist")); err != nil || got != "persist" {
		t.Fatalf("key not reused: %q %v", got, err)
	}
}

func TestOpenRejectsBadKeyLength(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "secret.key"), []byte("short"), 0o600)
	if _, err := Open(dir); err == nil {
		t.Fatal("accepted 5-byte key")
	}
}
