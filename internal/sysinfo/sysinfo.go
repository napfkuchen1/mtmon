// Package sysinfo reads mtmon's own resource use (Linux /proc and statfs). Every reading degrades to "unknown" (0)
// where the platform does not provide it.
package sysinfo

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// RSS is the resident memory of this process in bytes (0 = unknown).
func RSS() int64 {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(l, "VmRSS:"); ok {
			f := strings.Fields(rest)
			if len(f) >= 1 {
				kb, _ := strconv.ParseInt(f[0], 10, 64)
				return kb * 1024
			}
		}
	}
	return 0
}

// cpuTicks returns utime+stime of this process in clock ticks (100 per second on Linux).
func cpuTicks() (float64, bool) {
	b, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')') // the command name may contain spaces and parentheses
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 13 {
		return 0, false
	}
	u, e1 := strconv.ParseFloat(f[11], 64)
	k, e2 := strconv.ParseFloat(f[12], 64)
	return u + k, e1 == nil && e2 == nil
}

// CPU measures the process CPU use between two calls, in percent of one core.
type CPU struct {
	mu    sync.Mutex
	at    time.Time
	ticks float64
	last  float64
}

// Percent returns the use since the previous call (the first call: since the process started). Calls closer than
// two seconds return the previous value, so several viewers do not make the window too short to mean anything.
func (c *CPU) Percent() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := cpuTicks()
	if !ok {
		return 0
	}
	now := time.Now()
	if c.at.IsZero() {
		c.at, c.ticks = now.Add(-time.Duration(t/100*float64(time.Second))), 0
	}
	if dt := now.Sub(c.at).Seconds(); dt >= 2 {
		c.last = (t - c.ticks) / 100 / dt * 100
		c.at, c.ticks = now, t
	}
	return c.last
}

// Disk returns the space available to us and the total (used + available) of the file system holding dir.
func Disk(dir string) (free, total int64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0, false
	}
	// like df: the reserved blocks (root only) are neither used nor free for us, so total = used + available
	bs := int64(st.Bsize)
	return int64(st.Bavail) * bs, (int64(st.Blocks) - int64(st.Bfree) + int64(st.Bavail)) * bs, true
}
