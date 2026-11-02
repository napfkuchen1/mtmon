// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/napfkuchen1/mtmon/internal/update"
)

// verifyStaged: `mtmon verify-staged <archive> <signature> <output>` – run as root by mtmon-apply-update with the
// INSTALLED (root-owned) binary. Checks the staged release with the embedded signing key, refuses anything that is
// not strictly newer than this binary, writes the binary from the verified archive to <output> (which must not
// exist yet) and prints the release tag.
func verifyStaged(args []string) error {
	if len(args) != 3 {
		return errors.New("usage: mtmon verify-staged <archive> <signature> <output>")
	}
	archive, err := readCapped(args[0], update.MaxDownload)
	if err != nil {
		return err
	}
	sig, err := readCapped(args[1], 4096)
	if err != nil {
		return err
	}
	bin, tag, err := update.VerifyStaged(update.PublicKey, archive, sig, version)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(args[2], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return err
	}
	if _, err := f.Write(bin); err != nil {
		f.Close()
		os.Remove(args[2])
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(args[2])
		return err
	}
	fmt.Println(tag)
	return nil
}

// readCapped reads a regular file of at most max bytes.
func readCapped(name string, max int64) ([]byte, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", name, max)
	}
	return b, nil
}
