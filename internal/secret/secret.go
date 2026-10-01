// Package secret encrypts device credentials at rest (AES-256-GCM, key file next to the DB, 0600).
// This is defence in depth: whoever can read both key and DB can decrypt. It keeps credentials out of
// DB backups/copies that are moved without the key file.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

type Box struct{ aead cipher.AEAD }

// Open loads (or creates) DataDir/secret.key.
func Open(dir string) (*Box, error) {
	p := filepath.Join(dir, "secret.key")
	key, err := os.ReadFile(p)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, key, 0o600); err != nil {
			return nil, err
		}
	}
	if len(key) != 32 {
		return nil, errors.New("secret.key must be 32 bytes")
	}
	return New(key)
}

func New(key []byte) (*Box, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	return &Box{g}, nil
}

func (b *Box) Seal(plain string) []byte {
	n := make([]byte, b.aead.NonceSize())
	rand.Read(n)
	return b.aead.Seal(n, n, []byte(plain), nil)
}

func (b *Box) Open(enc []byte) (string, error) {
	ns := b.aead.NonceSize()
	if len(enc) < ns {
		return "", errors.New("ciphertext too short")
	}
	p, err := b.aead.Open(nil, enc[:ns], enc[ns:], nil)
	return string(p), err
}
