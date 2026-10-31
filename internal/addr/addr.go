// SPDX-License-Identifier: AGPL-3.0-only

// Package addr answers one question in one place: what kind of address is this? Lists, advice rules, alerts and the
// poller used to carry their own copies of these checks, and a fix in one copy missed the others (#87, #122, #135).
// The package has no dependencies on the rest of mtmon so every layer can use it.
package addr

import (
	"net/netip"
	"strings"
)

// Kind is the coarse class of an IP address.
type Kind int

const (
	Invalid     Kind = iota // not an address
	Unspecified             // 0.0.0.0, ::
	Loopback
	LinkLocal // fe80::/10, 169.254.0.0/16
	Multicast
	Private // RFC 1918 and unique-local IPv6 (fc00::/7)
	CGNAT   // 100.64.0.0/10 (carrier-grade NAT, shared address space)
	Public  // everything else: routable on the internet
)

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// Classify returns the Kind of an address (IPv4-in-IPv6 is unmapped first).
func Classify(a netip.Addr) Kind {
	if !a.IsValid() {
		return Invalid
	}
	a = a.Unmap()
	switch {
	case a.IsUnspecified():
		return Unspecified
	case a.IsLoopback():
		return Loopback
	case a.IsLinkLocalUnicast():
		return LinkLocal
	case a.IsMulticast():
		return Multicast
	case a.IsPrivate():
		return Private
	case cgnat.Contains(a):
		return CGNAT
	}
	return Public
}

// ClassifyString is Classify for text; unparsable input is Invalid.
func ClassifyString(s string) Kind {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return Invalid
	}
	return Classify(a)
}

// IsPublic: a routable internet address (not LAN, loopback, link-local, multicast, unspecified or CGNAT).
func IsPublic(s string) bool { return ClassifyString(s) == Public }

// IsLocal: an address that stays inside a network (private, loopback, link-local, multicast or CGNAT).
func IsLocal(a netip.Addr) bool {
	switch Classify(a) {
	case Loopback, LinkLocal, Multicast, Private, CGNAT:
		return true
	}
	return false
}

// IsCGNAT reports whether a lies in 100.64.0.0/10.
func IsCGNAT(a netip.Addr) bool { return cgnat.Contains(a.Unmap()) }

// IsULA reports whether s is a unique-local IPv6 address (fc00::/7).
func IsULA(s string) bool {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	return err == nil && a.Is6() && !a.Is4In6() && a.As16()[0]&0xFE == 0xFC
}

// IsGlobalIPv6 reports whether s is a global unicast IPv6 address (2000::/3): link-local and ULA addresses do not
// come from the ISP prefix.
func IsGlobalIPv6(s string) bool {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	return err == nil && a.Is6() && !a.Is4In6() && a.IsGlobalUnicast() && a.As16()[0]&0xE0 == 0x20
}

// IsULAOrLinkLocalPrefix reports whether a prefix text starts in fc00::/7 or fe80::/10 (not the ISP's delegated prefix).
func IsULAOrLinkLocalPrefix(p string) bool {
	p = strings.ToLower(strings.TrimSpace(p))
	for _, s := range []string{"fc", "fd", "fe8", "fe9", "fea", "feb"} {
		if strings.HasPrefix(p, s) {
			return true
		}
	}
	return false
}
