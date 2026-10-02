package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// targetEntry is the only archive member that is ever extracted.
const targetEntry = "mtmon/mtmon"

// extractBinary returns the content of mtmon/mtmon from a .tar.gz. Every other entry is ignored, but any
// entry with an absolute path, a ".." element or a link type makes the whole archive invalid.
func extractBinary(data []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("not a gzip archive: %w", err)
	}
	defer zr.Close()
	tr := tar.NewReader(io.LimitReader(zr, maxExtract))
	var found []byte
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("corrupt archive: %w", err)
		}
		if bad := badName(h.Name); bad != "" {
			return nil, fmt.Errorf("unsafe archive entry %q (%s)", h.Name, bad)
		}
		if h.Typeflag == tar.TypeSymlink || h.Typeflag == tar.TypeLink {
			return nil, fmt.Errorf("unsafe archive entry %q (link)", h.Name)
		}
		if path.Clean(h.Name) != targetEntry || h.Typeflag != tar.TypeReg {
			continue
		}
		if found != nil {
			return nil, errors.New("archive contains " + targetEntry + " twice")
		}
		if h.Size <= 0 || h.Size > MaxDownload*2 {
			return nil, fmt.Errorf("unexpected size of %s: %d", targetEntry, h.Size)
		}
		found, err = io.ReadAll(io.LimitReader(tr, h.Size+1))
		if err != nil {
			return nil, fmt.Errorf("corrupt archive: %w", err)
		}
		if int64(len(found)) != h.Size {
			return nil, errors.New("corrupt archive: size mismatch")
		}
	}
	if found == nil {
		return nil, errors.New("archive does not contain " + targetEntry)
	}
	return found, nil
}

func badName(n string) string {
	switch {
	case n == "":
		return "empty name"
	case strings.HasPrefix(n, "/"):
		return "absolute path"
	case strings.ContainsRune(n, 0) || strings.Contains(n, "\\"):
		return "illegal character"
	}
	for _, p := range strings.Split(n, "/") {
		if p == ".." {
			return "path traversal"
		}
	}
	return ""
}
