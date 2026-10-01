package store

import (
	"os"
	"path/filepath"
	"sort"
)

func pruneBackups(dir string, keep int) error {
	m, err := filepath.Glob(filepath.Join(dir, "mtmon-*.db"))
	if err != nil {
		return err
	}
	sort.Strings(m)
	for len(m) > keep {
		if err := os.Remove(m[0]); err != nil {
			return err
		}
		m = m[1:]
	}
	return nil
}
