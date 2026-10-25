//go:build !windows

package sysinfo

import "syscall"

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
