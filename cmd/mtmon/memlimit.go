// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"log/slog"
	"os"
	"path"
	"runtime/debug"
	"strconv"
	"strings"
)

// memLimitShare is the part of the cgroup memory limit the Go runtime aims to stay under. The rest is headroom for
// memory the runtime does not manage (SQLite page cache in modernc is Go memory, but stacks and fragmentation are not
// counted exactly).
const memLimitShare = 85

// applyMemoryLimit makes the Go garbage collector work harder before the process reaches its cgroup memory limit
// (systemd MemoryMax=, the LXC memory setting). Without it the runtime only collects at heap doubling and a burst of
// allocations can get the service OOM-killed although most of that memory is already garbage. An operator-set
// GOMEMLIMIT always wins. Returns the limit applied (0: none).
func applyMemoryLimit(log *slog.Logger) int64 {
	if os.Getenv("GOMEMLIMIT") != "" {
		return 0
	}
	self, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return 0
	}
	lim := cgroupLimit(string(self), func(p string) (string, error) {
		b, err := os.ReadFile(p)
		return string(b), err
	})
	if lim <= 0 {
		return 0
	}
	soft := lim / 100 * memLimitShare
	debug.SetMemoryLimit(soft)
	log.Info("memory limit from cgroup", "cgroup_bytes", lim, "go_limit_bytes", soft)
	return soft
}

// cgroupLimit returns the smallest memory.max on the path from the process's cgroup (v2, line "0::/path" in
// /proc/self/cgroup) up to the root, or 0 when there is none. read reads a file below /sys/fs/cgroup.
func cgroupLimit(procSelfCgroup string, read func(string) (string, error)) int64 {
	rel := ""
	found := false
	for _, l := range strings.Split(procSelfCgroup, "\n") {
		if p, ok := strings.CutPrefix(strings.TrimSpace(l), "0::"); ok {
			rel, found = p, true
			break
		}
	}
	if !found {
		return 0
	}
	var best int64
	dir := path.Clean("/" + rel)
	for {
		if s, err := read(path.Join("/sys/fs/cgroup", dir, "memory.max")); err == nil {
			if n, ok := parseMemoryMax(s); ok && (best == 0 || n < best) {
				best = n
			}
		}
		if dir == "/" {
			break
		}
		dir = path.Dir(dir)
	}
	return best
}

// parseMemoryMax reads a cgroup v2 memory.max value ("max" = no limit).
func parseMemoryMax(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "max" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
