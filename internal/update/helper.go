// SPDX-License-Identifier: AGPL-3.0-only

package update

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// RequiredHelperVersion is the version of the root swap helper (deploy/apply-update.sh) this release expects.
// 2: the helper verifies the release signature itself (v0.20.15). Bump it together with the marker line in the script.
const RequiredHelperVersion = 2

// helperMarker is the line in the installed helper that states its version ("# mtmon-helper-version: 2").
const helperMarker = "# mtmon-helper-version:"

// HelperVersion reads the version of the installed helper: 0 when there is no helper file, 1 when it has no marker
// (installed before the marker existed), otherwise the marked number.
func HelperVersion(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for i := 0; i < 60 && sc.Scan(); i++ {
		if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), helperMarker); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
				return n
			}
		}
	}
	return 1
}

// HelperOutdated reports an installed helper that is older than this release requires. No helper at all (container
// without one, demo) is not "outdated": the Updates card already says why updates cannot be installed there.
func (m *Manager) HelperOutdated() (have int, outdated bool) {
	if m == nil || m.o.HelperPath == "" {
		return 0, false
	}
	have = HelperVersion(m.o.HelperPath)
	return have, have > 0 && have < RequiredHelperVersion
}
