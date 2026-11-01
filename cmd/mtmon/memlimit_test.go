// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"errors"
	"testing"
)

func TestCgroupLimit(t *testing.T) {
	files := map[string]string{
		"/sys/fs/cgroup/memory.max":                             "max\n",
		"/sys/fs/cgroup/system.slice/memory.max":                "1073741824\n", // LXC / slice: 1 GiB
		"/sys/fs/cgroup/system.slice/mtmon.service/memory.max":  "805306368\n",  // MemoryMax=768M
		"/sys/fs/cgroup/user.slice/memory.max":                  "max",
		"/sys/fs/cgroup/system.slice/other.service/memory.max":  "garbage",
		"/sys/fs/cgroup/system.slice/broken.service/memory.max": "-1",
	}
	read := func(p string) (string, error) {
		if v, ok := files[p]; ok {
			return v, nil
		}
		return "", errors.New("missing")
	}
	for _, c := range []struct {
		name, self string
		want       int64
	}{
		{"service limit is the smallest", "0::/system.slice/mtmon.service\n", 805306368},
		{"parent limit applies when the service has none", "0::/system.slice/other.service\n", 1073741824},
		{"unparsable values are skipped", "0::/system.slice/broken.service\n", 1073741824},
		{"no limit anywhere", "0::/user.slice\n", 0},
		{"root cgroup", "0::/\n", 0},
		{"cgroup v1 only", "12:memory:/system.slice/mtmon.service\n", 0},
		{"v2 line among v1 lines", "12:memory:/x\n0::/system.slice/mtmon.service\n", 805306368},
		{"empty", "", 0},
	} {
		if got := cgroupLimit(c.self, read); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
}

func TestParseMemoryMax(t *testing.T) {
	for in, want := range map[string]int64{"max": 0, "": 0, "0": 0, "x": 0, "1024\n": 1024} {
		got, ok := parseMemoryMax(in)
		if got != want || ok != (want > 0) {
			t.Errorf("%q: got %d,%v", in, got, ok)
		}
	}
}
