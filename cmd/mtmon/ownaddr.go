// SPDX-License-Identifier: AGPL-3.0-only

package main

import "net"

// localAddrFor returns the address of this host that routes towards `host` ("" if unknown): what a router sees as the
// source of mtmon's API logins. A UDP "connect" only picks the route, nothing is sent.
func localAddrFor(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	c, err := net.Dial("udp", net.JoinHostPort(host, "9"))
	if err != nil {
		return ""
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return ""
}
