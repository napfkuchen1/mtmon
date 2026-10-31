// SPDX-License-Identifier: AGPL-3.0-only

package poller

import (
	"net/netip"
	"sort"
	"strings"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// uplinkPorts returns "device|port" for every port on which another managed device was discovered as a
// neighbor (LLDP/MNDP). A bridge host entry on such a port only says "reachable through that link", so it must
// not decide where a client is attached.
func (m *Manager) uplinkPorts() map[string]bool {
	out := map[string]bool{}
	ns, err := m.St.Neighbors()
	if err != nil {
		return out
	}
	managed := map[string]bool{}
	for _, d := range m.Cfg.AllDevices() {
		managed[d.Name] = true
	}
	for _, n := range ns {
		if !managed[n.Ident] || n.Ident == n.Device {
			continue
		}
		for _, p := range strings.Split(n.Iface, ",") {
			p = strings.TrimSpace(p)
			if i := strings.LastIndex(p, "/"); i >= 0 {
				p = p[i+1:]
			}
			if p != "" {
				out[n.Device+"|"+p] = true
			}
		}
	}
	return out
}

// Depths is deviceDepths for callers outside the manager's lock.
func (m *Manager) Depths() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deviceDepths()
}

// deviceDepths counts the hops of every managed device from the gateway (the device with WAN interfaces, else the
// routers) along the neighbor links. Needs m.mu held for m.snaps. Unreachable devices are absent.
func (m *Manager) deviceDepths() map[string]int {
	out := map[string]int{}
	ns, err := m.St.Neighbors()
	if err != nil {
		return out
	}
	adj := map[string][]string{}
	managed := map[string]bool{}
	for _, d := range m.Cfg.AllDevices() {
		managed[d.Name] = true
	}
	for _, n := range ns {
		if managed[n.Device] && managed[n.Ident] && n.Device != n.Ident {
			adj[n.Device] = append(adj[n.Device], n.Ident)
			adj[n.Ident] = append(adj[n.Ident], n.Device)
		}
	}
	var queue []string
	for _, d := range m.Cfg.AllDevices() {
		if s := m.snaps[d.Name]; s != nil && len(s.wan) > 0 && !m.cascaded(d.Name) {
			out[d.Name] = 0
			queue = append(queue, d.Name)
		}
	}
	if len(queue) == 0 { // no WAN info yet: routers are the best guess
		for _, d := range m.Cfg.AllDevices() {
			if d.Role == "router" {
				out[d.Name] = 0
				queue = append(queue, d.Name)
			}
		}
	}
	for len(queue) > 0 {
		d := queue[0]
		queue = queue[1:]
		for _, n := range adj[d] {
			if _, seen := out[n]; !seen {
				out[n] = out[d] + 1
				queue = append(queue, n)
			}
		}
	}
	return out
}

// nearer reports whether device a is closer to the gateway than b (unknown devices are farthest; ties by name so
// the result does not depend on map order).
func nearer(depth map[string]int, a, b string) bool {
	da, oka := depth[a]
	db, okb := depth[b]
	switch {
	case oka != okb:
		return oka
	case da != db:
		return da < db
	}
	return a < b
}

// Diagnostics returns the raw tables the last poll cycle got from every device plus the derived uplink ports, so a
// wrong location or presence can be explained from real data. No credentials are involved.
func (m *Manager) Diagnostics() map[string]any {
	up := []string{}
	for k, v := range m.uplinkPorts() {
		if v {
			up = append(up, k)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	devs := map[string]any{}
	for name, s := range m.snaps {
		devs[name] = map[string]any{"role": s.role, "polled": s.at.Unix(), "wifi": rowsOrEmpty(s.wifi), "bridge_hosts": rowsOrEmpty(s.bridge),
			"arp": rowsOrEmpty(s.arp), "dhcp_leases": rowsOrEmpty(s.leases), "wan_interfaces": wanList(s.wan),
			"ip_addresses": rowsOrEmpty(s.addrs), "pppoe_clients": rowsOrEmpty(s.pppoe), "routes": rowsOrEmpty(s.routes)}
	}
	ups := []Upstream{}
	for name, s := range m.snaps {
		if !m.cascaded(name) {
			ups = append(ups, upstreamsFor(name, s, m.En.Vendor)...)
		}
	}
	return map[string]any{"devices": devs, "uplink_ports": up, "upstream": ups}
}

func rowsOrEmpty(r []Row) []Row {
	if r == nil {
		return []Row{}
	}
	return r
}

// capHint finds a managed device named in a registration row, e.g. the CAP a controller-side row belongs to.
// RouterOS field names for that differ between versions, so any value that equals a managed device name counts
// (the controller itself and the "interface"/"ssid" values are ignored).
func capHint(r Row, names map[string]string, self string) string {
	for k, v := range r {
		if k == "interface" || k == "ssid" || k == "mac-address" {
			continue
		}
		if n, ok := names[strings.ToLower(strings.TrimSpace(v))]; ok && n != self {
			return n
		}
	}
	return ""
}

// neighborIdents maps MAC -> identity for devices announced via LLDP/MNDP (MikroTik, many APs and switches).
func (m *Manager) neighborIdents() map[string]string {
	out := map[string]string{}
	if ns, err := m.St.Neighbors(); err == nil {
		for _, n := range ns {
			if n.Ident != "" && n.MAC != "" {
				out[store.NormMAC(n.MAC)] = n.Ident
			}
		}
	}
	return out
}

// fallbackName names a client the DHCP server gave no host-name: neighbor identity first, then a
// (static or reverse) DNS name for its address. Reverse lookups run in the background and show up on a later cycle.
func (m *Manager) fallbackName(mac, ip string, idents map[string]string) string {
	if id := idents[mac]; id != "" {
		return id
	}
	if a, err := netip.ParseAddr(ip); err == nil {
		return m.En.Name(a)
	}
	return ""
}

// wifiPerDevice counts the Wi-Fi clients that are present on each managed device, as the client merge placed them
// (the AP a client talks to, not the CAPsMAN controller that lists all registrations).
func wifiPerDevice(obs []store.Observation, devs []config.Device) map[string]int {
	out := make(map[string]int, len(devs))
	for _, d := range devs {
		out[d.Name] = 0
	}
	for _, o := range obs {
		if o.WiFi && o.Present && o.Device != "" {
			if _, ok := out[o.Device]; ok {
				out[o.Device]++
			}
		}
	}
	return out
}

func containsClient(cs []store.Client, mac string) bool {
	for _, c := range cs {
		if c.MAC == mac {
			return true
		}
	}
	return false
}

// rekeyAlias moves traffic that was stored under a bare address of a VPN peer (before the peer was known by all of
// its addresses) to the peer's client key. Runs once per address and process, in the background.
func (m *Manager) rekeyAlias(key, addr string) {
	m.rekeyMu.Lock()
	k := key + "|" + addr
	if m.rekeyed[k] {
		m.rekeyMu.Unlock()
		return
	}
	if m.rekeyed == nil {
		m.rekeyed = map[string]bool{}
	}
	m.rekeyed[k] = true
	m.rekeyMu.Unlock()
	go func() {
		if err := m.St.RekeyTraffic(addr, key); err != nil {
			m.Log.Error("re-key vpn traffic", "from", addr, "to", key, "err", err)
		}
	}()
}

// WANSummary names the WAN interfaces of the gateways ("gw: ether1, pppoe-out1"), stored with each speed test so
// results after a failover or a changed uplink are not mixed up.
func (m *Manager) WANSummary() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var parts []string
	for name, s := range m.snaps {
		if m.cascaded(name) { // its "WAN" is an uplink to another managed router, not the internet
			continue
		}
		if l := wanList(s.wan); len(l) > 0 {
			parts = append(parts, name+": "+strings.Join(l, ", "))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

// cascaded reports whether a router's active default route points at an address of another managed device: it sits
// behind that device (lab router, double NAT), so its WAN list is an internal uplink and no internet edge. Needs
// m.mu held for m.snaps.
func (m *Manager) cascaded(name string) bool {
	s := m.snaps[name]
	if s == nil {
		return false
	}
	for _, r := range s.routes {
		if r["dst-address"] != "0.0.0.0/0" || r.Bool("disabled") || (r["active"] != "" && !r.Bool("active")) {
			continue
		}
		gw, _, _ := strings.Cut(r["gateway"], "%")
		for other, o := range m.snaps {
			if other == name || o == nil {
				continue
			}
			for _, a := range o.addrs {
				if ip, _, _ := strings.Cut(a["address"], "/"); ip == gw && gw != "" {
					return true
				}
			}
		}
	}
	return false
}

func wanList(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
