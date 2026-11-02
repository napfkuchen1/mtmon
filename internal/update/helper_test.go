// SPDX-License-Identifier: AGPL-3.0-only

package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHelperVersion(t *testing.T) {
	d := t.TempDir()
	w := func(name, body string) string {
		p := filepath.Join(d, name)
		os.WriteFile(p, []byte(body), 0o755)
		return p
	}
	if v := HelperVersion(filepath.Join(d, "none")); v != 0 {
		t.Errorf("missing helper: %d", v)
	}
	if v := HelperVersion(w("old", "#!/bin/sh\n# mtmon-apply-update\nset -u\n")); v != 1 {
		t.Errorf("helper without marker: %d, want 1", v)
	}
	if v := HelperVersion(w("new", "#!/bin/sh\n# mtmon-helper-version: 2\nset -u\n")); v != 2 {
		t.Errorf("marked helper: %d, want 2", v)
	}
	for name, want := range map[string]bool{"none": false, "old": true, "new": false} {
		m := &Manager{o: Options{HelperPath: filepath.Join(d, name)}}
		if _, out := m.HelperOutdated(); out != want {
			t.Errorf("%s: outdated=%v, want %v", name, out, want)
		}
	}
	if _, out := (&Manager{}).HelperOutdated(); out {
		t.Error("no HelperPath (demo) must not flag the helper")
	}
	// the shipped script carries the version this release requires
	if v := HelperVersion(filepath.Join("..", "..", "deploy", "apply-update.sh")); v != RequiredHelperVersion {
		t.Errorf("deploy/apply-update.sh marker %d, want %d", v, RequiredHelperVersion)
	}
}
