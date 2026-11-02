// SPDX-License-Identifier: AGPL-3.0-only

package addr

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
)

// metadata are cloud instance-metadata endpoints; never a legitimate target, whatever the address class.
var metadata = []netip.Addr{
	netip.MustParseAddr("169.254.169.254"), netip.MustParseAddr("169.254.170.2"),
	netip.MustParseAddr("fd00:ec2::254"), netip.MustParseAddr("100.100.100.200"),
}

// OutboundDenied is the one policy for connections mtmon makes on an operator's behalf (setup wizard, custom
// probes, webhook and ntfy): no unspecified, multicast, link-local or metadata addresses, and no loopback unless
// allowLoopback. Private, CGNAT and public addresses are the caller's call (the wizard adds its own LAN check).
func OutboundDenied(a netip.Addr, allowLoopback bool) error {
	a = a.Unmap().WithZone("")
	for _, m := range metadata {
		if a == m {
			return errors.New("address not allowed")
		}
	}
	switch Classify(a) {
	case Invalid, Unspecified, Multicast, LinkLocal:
		return fmt.Errorf("%s is not a valid target (unspecified, multicast or link-local)", a)
	case Loopback:
		if !allowLoopback {
			return fmt.Errorf("%s is a loopback address", a)
		}
	}
	return nil
}

// GuardControl is a net.Dialer.Control that applies OutboundDenied to the address actually being connected to, so
// redirects, DNS answers that change between lookup and dial and literal IPs are all covered.
func GuardControl(allowLoopback bool) func(network, address string, c syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		h, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		a, err := netip.ParseAddr(h)
		if err != nil {
			return err
		}
		return OutboundDenied(a, allowLoopback)
	}
}

// IsMetadata reports whether a is a cloud instance-metadata endpoint.
func IsMetadata(a netip.Addr) bool {
	a = a.Unmap()
	for _, m := range metadata {
		if a == m {
			return true
		}
	}
	return false
}
