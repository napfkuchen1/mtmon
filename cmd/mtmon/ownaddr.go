// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"net"
	"sync"
	"time"
)

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

// hostAddrs answers "is this one of the addresses of the machine mtmon runs on", from a list that is refreshed once a minute.
func hostAddrs() func(ip string) bool {
	var (
		mu   sync.Mutex
		set  map[string]bool
		when time.Time
	)
	return func(ip string) bool {
		mu.Lock()
		defer mu.Unlock()
		if set == nil || time.Since(when) > time.Minute {
			set = map[string]bool{}
			when = time.Now()
			if as, err := net.InterfaceAddrs(); err == nil {
				for _, a := range as {
					if n, ok := a.(*net.IPNet); ok {
						set[n.IP.String()] = true
					}
				}
			}
		}
		return set[ip]
	}
}
