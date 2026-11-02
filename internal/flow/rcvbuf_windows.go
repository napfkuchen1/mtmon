// SPDX-License-Identifier: AGPL-3.0-only

package flow

import (
	"net"
	"syscall"
)

// readRcvbuf returns the effective SO_RCVBUF of the socket, 0 when it cannot be read.
func readRcvbuf(c *net.UDPConn) int {
	rc, err := c.SyscallConn()
	if err != nil {
		return 0
	}
	n := 0
	_ = rc.Control(func(fd uintptr) {
		v, err := syscall.GetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF)
		if err == nil {
			n = v
		}
	})
	return n
}
