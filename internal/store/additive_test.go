// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Schema changes must stay additive (#189): after an update the previous binary has to start on the new database
// again (rollback). That rules out dropping or renaming columns/tables and changing existing columns.
func TestSchemaChangesStayAdditive(t *testing.T) {
	bad := regexp.MustCompile(`(?i)\b(DROP\s+(TABLE|COLUMN|INDEX)|RENAME\s+(TO|COLUMN)|ALTER\s+TABLE\s+\w+\s+(DROP|RENAME|ALTER)\b)`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "//") {
				continue
			}
			if m := bad.FindString(l); m != "" {
				t.Errorf("%s:%d non-additive schema change %q", f, i+1, m)
			}
		}
	}
}
