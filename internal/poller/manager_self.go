package poller

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// noteSelfMACs remembers the MAC addresses of a managed device's own interfaces, so the router/AP itself
// (seen by other devices through ARP or the bridge host table) is not listed as a client.
func (m *Manager) noteSelfMACs(device string, rows []Row) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range rows {
		if mac := store.NormMAC(r["mac-address"]); mac != "" && !zeroMAC(mac) {
			m.self[mac] = device
		}
	}
}

// zeroMAC: tunnel and PPPoE interfaces report 00:00:00:00:00:00 – no real hardware address, so never a key.
func zeroMAC(mac string) bool {
	return strings.Trim(mac, "0:-") == ""
}

// liveAddrs returns the addresses the managed devices have right now: the ones read from the router on each poll
// (/ip/address, /ipv6/address, so a PPPoE address that changed after the nightly reconnect is known at once), plus
// the stored capability list as a fallback. device -> addresses.
func (m *Manager) liveAddrs() map[string][]netip.Addr {
	out := map[string][]netip.Addr{}
	add := func(dev, s string) {
		if ad, ok := addrOf(s); ok {
			out[dev] = append(out[dev], ad)
		}
	}
	m.mu.Lock()
	for dev, s := range m.snaps {
		for _, r := range s.addrs {
			if r["disabled"] != "true" {
				add(dev, r["address"])
			}
		}
		for _, r := range s.addrs6 {
			if r["disabled"] != "true" {
				add(dev, r["address"])
			}
		}
	}
	m.mu.Unlock()
	if rows, err := m.St.DeviceRows(); err == nil {
		for _, r := range rows {
			var c struct {
				Addresses []string `json:"addresses"`
			}
			if json.Unmarshal(r.Caps, &c) != nil {
				continue
			}
			for _, x := range c.Addresses {
				add(r.Name, x)
			}
		}
	}
	return out
}

// SelfDevice returns the managed device that owns this MAC address on one of its own interfaces.
func (m *Manager) SelfDevice(mac string) (string, bool) {
	mac = store.NormMAC(mac)
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.self[mac]; ok {
		return d, true
	}
	if mac != "" && zeroMAC(mac) { // older data booked a router's PPPoE/tunnel traffic on the zero MAC: that is a router, too
		if ds := m.Cfg.AllDevices(); len(ds) > 0 {
			return ds[0].Name, true
		}
	}
	return "", false
}

// SelfByAddr returns the managed device that owns this IP address: its management address, an address configured on
// it (from the stored capabilities), or a link-local address built from one of its interface MACs.
func (m *Manager) SelfByAddr(ip string) (string, bool) {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return "", false
	}
	for _, d := range m.Cfg.AllDevices() {
		if d.Addr == ip {
			return d.Name, true
		}
	}
	for dev, list := range m.liveAddrs() {
		for _, ad := range list {
			if ad == a {
				return dev, true
			}
		}
	}
	if a.Is6() { // EUI-64 interface id: link-local, SLAAC or an old PPPoE address (zero MAC = 0200:00ff:fe00:0000) of a router
		b := a.As16()
		if b[11] == 0xff && b[12] == 0xfe {
			mac := fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", b[8]^0x02, b[9], b[10], b[13], b[14], b[15])
			return m.SelfDevice(mac)
		}
	}
	return "", false
}

// OwnsAddr reports whether any managed device owns this address (management address, any /ip/address or
// /ipv6/address row incl. dynamic and link-local ones, EUI-64 of an own interface). It is the shared answer to
// "is this a real host or an imaginary one", independent of whether the device's own MAC is known.
func (m *Manager) OwnsAddr(ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	if a.Unmap().IsLinkLocalUnicast() {
		return true // every router has link-local addresses per interface; clients learn them from RDNSS
	}
	_, ok := m.SelfByAddr(a.Unmap().String())
	return ok
}

// addrOf parses an entry of the stored capabilities ("192.168.1.1/24 on bridge", "2001:db8::1/64 on pppoe-out1").
func addrOf(s string) (netip.Addr, bool) {
	if i := strings.IndexByte(s, ' '); i > 0 {
		s = s[:i]
	}
	if pf, err := netip.ParsePrefix(s); err == nil {
		return pf.Addr(), true
	}
	ad, err := netip.ParseAddr(s)
	return ad, err == nil
}

// SelfMACByAddr returns one of the own interface MACs of the managed device that has this IP address configured
// (WAN / PPPoE addresses included, dynamic ones too), or "". The flow pipeline books the device's own traffic on
// that MAC, so it never turns into a pseudo-client. The address list is rebuilt at most every 30 s.
func (m *Manager) SelfMACByAddr(ip string) string {
	m.addrMu.Lock()
	defer m.addrMu.Unlock()
	if m.addrMAC == nil || time.Since(m.addrAt) > 10*time.Second {
		macs := map[string]string{} // device -> smallest own MAC
		m.mu.Lock()
		for mac, dev := range m.self {
			if old, ok := macs[dev]; !ok || mac < old {
				macs[dev] = mac
			}
		}
		m.mu.Unlock()
		out := map[string]string{}
		for dev, list := range m.liveAddrs() {
			mac := macs[dev]
			if mac == "" {
				continue
			}
			for _, ad := range list {
				if !ad.IsLinkLocalUnicast() {
					out[ad.String()] = mac
				}
			}
		}
		m.addrMAC, m.addrAt = out, time.Now()
	}
	if a, err := netip.ParseAddr(ip); err == nil {
		return m.addrMAC[a.String()]
	}
	return ""
}

// isSelf reports whether the observation is one of the managed devices themselves.
func (m *Manager) isSelf(mac, ip string) bool {
	if _, ok := m.self[store.NormMAC(mac)]; ok { // callers hold m.mu
		return true
	}
	if ip == "" {
		return false
	}
	for _, d := range m.Cfg.AllDevices() {
		if d.Addr == ip {
			return true
		}
	}
	return false
}
