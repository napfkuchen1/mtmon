// SPDX-License-Identifier: AGPL-3.0-only

package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/blake2b"
)

// PublicKey is the minisign public key (second line of minisign.pub) that signs mtmon releases.
// The matching private key lives only in the GitHub secret MINISIGN_KEY and the maintainer's password manager.
const PublicKey = "RWSj18U8+XTlXnicS+OIExn6YF3O9+RC4U9VIaDLVtPFeMkp75YhedSK"

// SigAssetName is the detached signature published next to the archive.
const SigAssetName = AssetName + ".minisig"

// verifyMinisign checks a minisign signature (legacy "Ed" and pre-hashed "ED") over data with the base64 public
// key pubB64 and returns the signed trusted comment. The trusted comment is covered by the global signature.
func verifyMinisign(pubB64 string, sig, data []byte) (trusted string, err error) {
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubB64))
	if err != nil || len(pub) != 42 || string(pub[:2]) != "Ed" {
		return "", errors.New("invalid minisign public key")
	}
	keyID, edPub := pub[2:10], ed25519.PublicKey(pub[10:])

	lines := strings.Split(strings.ReplaceAll(string(sig), "\r\n", "\n"), "\n")
	if len(lines) < 4 || !strings.HasPrefix(lines[0], "untrusted comment:") || !strings.HasPrefix(lines[2], "trusted comment:") {
		return "", errors.New("malformed minisign signature file")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil || len(raw) != 74 {
		return "", errors.New("malformed minisign signature")
	}
	if string(raw[2:10]) != string(keyID) {
		return "", errors.New("signature was made with a different key")
	}
	msg := data
	switch string(raw[:2]) {
	case "Ed":
	case "ED":
		h := blake2b.Sum512(data)
		msg = h[:]
	default:
		return "", errors.New("unsupported minisign signature algorithm")
	}
	if !ed25519.Verify(edPub, msg, raw[10:]) {
		return "", errors.New("signature does not match the download")
	}
	trusted = strings.TrimPrefix(lines[2], "trusted comment:")
	trusted = strings.TrimPrefix(trusted, " ")
	global, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[3]))
	if err != nil || len(global) != 64 {
		return "", errors.New("malformed minisign global signature")
	}
	if !ed25519.Verify(edPub, append(append([]byte{}, raw[10:]...), trusted...), global) {
		return "", errors.New("trusted comment signature invalid")
	}
	return trusted, nil
}

// VerifyStaged is the root-side check of a staged update (`mtmon verify-staged`, called by mtmon-apply-update).
// The staging directory belongs to the unprivileged service user, so nothing in it is trusted: the archive must be
// signed by key, its trusted comment names the tag, the tag must be a release strictly newer than current (no
// downgrade to an older signed release with known bugs), and the binary is taken from the verified archive itself.
func VerifyStaged(key string, archive, sig []byte, current string) (bin []byte, tag string, err error) {
	tc, err := verifyMinisign(key, sig, archive)
	if err != nil {
		return nil, "", err
	}
	tag, ok := strings.CutPrefix(tc, "mtmon ")
	if !ok {
		return nil, "", fmt.Errorf("unexpected trusted comment %q", tc)
	}
	nv, ok := ParseVersion(tag)
	if !ok {
		return nil, "", fmt.Errorf("signed tag %q is not a release version", tag)
	}
	cv, ok := ParseVersion(current)
	if !ok {
		return nil, "", fmt.Errorf("installed version %q is not a release; refusing to replace it", current)
	}
	if Compare(nv, cv) <= 0 {
		return nil, "", fmt.Errorf("%s is not newer than the installed %s", tag, current)
	}
	bin, err = extractBinary(archive)
	if err != nil {
		return nil, "", err
	}
	return bin, tag, nil
}

// verifyRelease requires a valid signature by key over the archive and a trusted comment naming exactly this tag,
// so a signed archive of another release cannot be passed off as this one.
func verifyRelease(key string, sig, archive []byte, tag string) error {
	tc, err := verifyMinisign(key, sig, archive)
	if err != nil {
		return err
	}
	if want := "mtmon " + tag; tc != want {
		return fmt.Errorf("signature belongs to %q, expected %q", tc, want)
	}
	return nil
}
