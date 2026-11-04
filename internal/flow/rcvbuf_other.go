// SPDX-License-Identifier: AGPL-3.0-only

//go:build !windows

package flow

import (
	"net"
	"syscall"
)

// readRcvbuf returns the effective SO_RCVBUF of the socket, 0 when it cannot be read.
// Linux reports twice the requested value (bookkeeping overhead), which is what the kernel really queues.
func readRcvbuf(c *net.UDPConn) int {
	rc, err := c.SyscallConn()
	if err != nil {
		return 0
	}
	n := 0
	_ = rc.Control(func(fd uintptr) {
		v, err := syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF)
		if err == nil {
			n = v
		}
	})
	return n
}
