package update

import (
	"regexp"
	"strconv"
	"strings"
)

// Version is a parsed SemVer 2.0.0 version.
type Version struct {
	Major, Minor, Patch uint64
	Pre                 []string
}

var semverRe = regexp.MustCompile(`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// git-describe output such as v0.6.0-3-gabc1234 or ...-dirty is a development build, not a release.
var describeRe = regexp.MustCompile(`(-\d+-g[0-9a-f]{4,}|-dirty)(-dirty)?$`)

// ParseVersion parses "v1.2.3", "1.2.3-rc.1" etc. Development builds ("dev", git-describe) are rejected.
func ParseVersion(s string) (Version, bool) {
	s = strings.TrimSpace(s)
	if describeRe.MatchString(s) {
		return Version{}, false
	}
	m := semverRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, false
	}
	var v Version
	v.Major, _ = strconv.ParseUint(m[1], 10, 64)
	v.Minor, _ = strconv.ParseUint(m[2], 10, 64)
	v.Patch, _ = strconv.ParseUint(m[3], 10, 64)
	if m[4] != "" {
		v.Pre = strings.Split(m[4], ".")
	}
	return v, true
}

// Compare returns -1, 0 or 1 following SemVer precedence rules (build metadata ignored).
func Compare(a, b Version) int {
	for _, p := range [][2]uint64{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if p[0] != p[1] {
			if p[0] < p[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a.Pre) == 0 && len(b.Pre) == 0:
		return 0
	case len(a.Pre) == 0:
		return 1
	case len(b.Pre) == 0:
		return -1
	}
	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		x, y := a.Pre[i], b.Pre[i]
		xn, xe := strconv.ParseUint(x, 10, 64)
		yn, ye := strconv.ParseUint(y, 10, 64)
		switch {
		case xe == nil && ye == nil:
			if xn != yn {
				if xn < yn {
					return -1
				}
				return 1
			}
		case xe == nil:
			return -1 // numeric identifiers sort before alphanumeric ones
		case ye == nil:
			return 1
		case x != y:
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a.Pre) < len(b.Pre):
		return -1
	case len(a.Pre) > len(b.Pre):
		return 1
	}
	return 0
}
