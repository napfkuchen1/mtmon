package sysinfo

// Disk is unknown on Windows (development machines only).
func Disk(dir string) (free, total int64, ok bool) { return 0, 0, false }
