package enrich

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

// Download sources (fixed, never user supplied). Variables so tests can point them at a local server.
var (
	dbipURL = "https://download.db-ip.com/free/dbip-%s-lite-%s.mmdb.gz" // kind, YYYY-MM  (DB-IP Lite, CC BY 4.0)
	ouiURL  = "https://standards-oui.ieee.org/oui/oui.csv"
)

const maxDownload = 256 << 20

// Dataset names understood by Fetch.
const (
	DataGeo = "geo"
	DataASN = "asn"
	DataOUI = "oui"
)

// Fetch downloads one dataset into dir (atomic: the old file stays until the new one is complete and plausible)
// and returns the path of the final file. Same files and names as deploy/update-geo.sh.
func Fetch(ctx context.Context, dir, what string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	switch what {
	case DataGeo:
		return fetchDBIP(ctx, dir, "country", "dbip-country.mmdb")
	case DataASN:
		return fetchDBIP(ctx, dir, "asn", "dbip-asn.mmdb")
	case DataOUI:
		return fetchFile(ctx, dir, ouiURL, "oui.csv", false, 500_000, func(string) error { return nil })
	}
	return "", fmt.Errorf("unknown dataset %q", what)
}

func fetchDBIP(ctx context.Context, dir, kind, name string) (string, error) {
	var last error
	t := time.Now().UTC()
	for i := 0; i < 3; i++ { // the current month's file appears a few days into the month
		ym := t.AddDate(0, -i, 0).Format("2006-01")
		p, err := fetchFile(ctx, dir, fmt.Sprintf(dbipURL, kind, ym), name, true, 1_000_000, func(path string) error {
			r, err := maxminddb.Open(path)
			if err != nil {
				return err
			}
			return r.Close()
		})
		if err == nil {
			return p, nil
		}
		last = err
		if ctx.Err() != nil {
			break
		}
	}
	return "", last
}

func fetchFile(ctx context.Context, dir, url, name string, gz bool, minSize int64, check func(string) error) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "mtmon")
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	var src io.Reader = io.LimitReader(resp.Body, maxDownload)
	if gz {
		zr, err := gzip.NewReader(src)
		if err != nil {
			return "", err
		}
		defer zr.Close()
		src = io.LimitReader(zr, maxDownload)
	}
	tmp, err := os.CreateTemp(dir, ".dl-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, src)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if n < minSize {
		return "", errors.New("downloaded file is too small to be valid")
	}
	if err := check(tmp.Name()); err != nil {
		return "", fmt.Errorf("downloaded file is not valid: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return "", err
	}
	final := filepath.Join(dir, name)
	if err := os.Rename(tmp.Name(), final); err != nil {
		return "", err
	}
	return final, nil
}
