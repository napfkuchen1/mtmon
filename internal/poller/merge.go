// SPDX-License-Identifier: AGPL-3.0-only

package poller

import (
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// edgeLoc is where a client is attached: a device port, and whether that device is an AP.
type edgeLoc struct {
	dev, iface string
	ap         bool
}

// mergeState collects, for one merge cycle, what the device snapshots say about each client.
// Each collect* method reads one kind of evidence from one device; mergeAll puts them together.
type mergeState struct {
	byMAC map[string]*store.Observation
	nd6   map[string]string
	rank  map[string]int // higher wins for location (Device/Iface)
	// where a client is really attached: a bridge host entry on a port that does not face another managed
	// device (an "edge" port). With CAPsMAN the controller lists every Wi-Fi client, but the AP the client talks
	// to learns its MAC on a non-uplink port, which is the better answer.
	edge map[string]edgeLoc
	hint map[string]string // MAC -> managed device named inside the registration row (CAP identity)
	// hosts learned on an uplink port (an unmanaged switch may sit in between) and MACs of the managed devices
	// themselves; see resolveShared
	shared  map[string]edgeLoc
	depth   map[string]int // hops from the gateway; the shared port nearest to it wins
	ownMAC  map[string]string
	names   map[string]string
	uplinks map[string]bool
}

func (m *Manager) newMergeState() *mergeState {
	st := &mergeState{
		byMAC:   map[string]*store.Observation{},
		nd6:     map[string]string{},
		rank:    map[string]int{},
		edge:    map[string]edgeLoc{},
		hint:    map[string]string{},
		shared:  map[string]edgeLoc{},
		depth:   m.deviceDepths(),
		ownMAC:  m.neighborIdents(),
		names:   map[string]string{},
		uplinks: m.uplinkPorts(),
	}
	for _, d := range m.Cfg.AllDevices() {
		st.names[strings.ToLower(d.Name)] = d.Name
	}
	return st
}

// upd applies f to the observation of a MAC (creating it). With loc set the call also claims the client's location
// and is ignored when a higher priority already did.
func (st *mergeState) upd(mac string, loc bool, prio int, f func(o *store.Observation)) {
	mac = store.NormMAC(mac)
	if mac == "" || mac == "00:00:00:00:00:00" {
		return
	}
	o := st.byMAC[mac]
	if o == nil {
		o = &store.Observation{MAC: mac}
		st.byMAC[mac] = o
	}
	if loc {
		if prio < st.rank[mac] {
			return
		}
		st.rank[mac] = prio
	}
	f(o)
}

// bridgeVia names the evidence for a bridge host unless a better one is already there.
func setBridgeVia(o *store.Observation, via string) {
	if o.Via == "" || strings.HasPrefix(o.Via, "ARP") {
		o.Via = via
	}
}

func (st *mergeState) collectBridge(dev string, s *devSnap, apBonus int) {
	for _, r := range s.bridge {
		if r.Bool("invalid") {
			continue
		}
		if r.Bool("local") {
			st.ownMAC[store.NormMAC(r["mac-address"])] = dev
			continue
		}
		iface := r["on-interface"]
		if s.wan[iface] {
			continue // upstream side (modem, ISP): not a LAN client
		}
		mac := store.NormMAC(r["mac-address"])
		if st.uplinks[dev+"|"+iface] {
			// learned via a trunk to another managed device: not where the host is attached, unless no
			// device behind that trunk has it on a port of its own (then an unmanaged switch is in between)
			if old, ok := st.shared[mac]; !ok || nearer(st.depth, dev, old.dev) {
				st.shared[mac] = edgeLoc{dev, iface, false}
			}
			continue
		}
		if _, ok := st.edge[mac]; !ok || s.role == "ap" {
			st.edge[mac] = edgeLoc{dev, iface, s.role == "ap"}
		}
		via := "bridge host on " + dev + "/" + iface
		st.upd(r["mac-address"], true, 1+apBonus, func(o *store.Observation) {
			o.Device, o.Iface, o.Present = dev, iface, true
			setBridgeVia(o, via)
		})
	}
}

func (st *mergeState) collectND6(s *devSnap) {
	for _, r := range s.nd6 {
		if r["status"] != "failed" && r["status"] != "incomplete" && r["address"] != "" {
			st.nd6[strings.TrimSuffix(r["address"], "/128")] = store.NormMAC(r["mac-address"])
		}
	}
}

// arpPresent tells whether an ARP row proves the host is there now. DHCP-created entries are "permanent" for as
// long as the lease lives: no sign of life.
func arpPresent(r Row) bool {
	return r["status"] == "reachable" || r["status"] == "delay" || r["status"] == "probe" ||
		(r["status"] == "permanent" && !r.Bool("dhcp") && !r.Bool("dynamic"))
}

func (st *mergeState) collectARP(dev string, s *devSnap) {
	for _, r := range s.arp {
		if r.Bool("invalid") || r["status"] == "failed" || r["status"] == "incomplete" || s.wan[r["interface"]] {
			continue
		}
		pres := arpPresent(r)
		addr := r["address"]
		iface := r["interface"]
		via := "ARP " + r["status"] + " on " + dev
		st.upd(r["mac-address"], false, 0, func(o *store.Observation) {
			if o.IP == "" {
				o.IP = addr
			}
			if pres {
				o.Present = true
				if o.Via == "" {
					o.Via = via
				}
				if o.Device == "" {
					o.Device, o.Iface = dev, iface
				}
			}
		})
	}
}

func (st *mergeState) collectLeases(s *devSnap) {
	for _, r := range s.leases {
		mac := r["active-mac-address"]
		if mac == "" {
			mac = r["mac-address"]
		}
		ip := r["active-address"]
		if ip == "" {
			ip = r["address"]
		}
		host := r["host-name"]
		if r["status"] != "bound" && r["status"] != "" {
			continue
		}
		st.upd(mac, false, 0, func(o *store.Observation) {
			o.IP = ip
			if host != "" {
				o.Hostname = host
			} else if r["comment"] != "" && o.Hostname == "" {
				o.Hostname = r["comment"]
			}
		})
	}
}

func (st *mergeState) collectWifi(dev string, s *devSnap, apBonus int) {
	for _, r := range s.wifi {
		sig := parseSignal(r["signal"])
		iface, ssid, band, tx, rx := r["interface"], r["ssid"], r["band"], r["tx-rate"], r["rx-rate"]
		via := "Wi-Fi registration on " + dev
		if h := capHint(r, st.names, dev); h != "" {
			st.hint[store.NormMAC(r["mac-address"])] = h
		}
		st.upd(r["mac-address"], true, 20+apBonus, func(o *store.Observation) {
			o.WiFi, o.Present, o.Via = true, true, via
			o.Device, o.Iface, o.SSID, o.Band, o.Signal, o.TxRate, o.RxRate = dev, iface, ssid, band, sig, tx, rx
		})
	}
}

// resolveShared places a MAC seen only on uplink ports, that no managed device reports on a port or radio of its own
// and that is not a managed device itself: it hangs on an unmanaged switch at the uplink port.
func (st *mergeState) resolveShared() {
	for mac, e := range st.shared {
		if _, ok := st.edge[mac]; ok || st.ownMAC[mac] != "" {
			continue
		}
		if o := st.byMAC[mac]; o != nil && o.WiFi {
			continue
		}
		e := e
		st.upd(mac, true, 1, func(o *store.Observation) {
			o.Device, o.Iface, o.Present = e.dev, e.iface, true
			setBridgeVia(o, "bridge host on "+e.dev+"/"+e.iface+" (shared port)")
		})
	}
}

// resolveWifiAP moves a Wi-Fi client reported only by a controller (router) but sitting on an AP's edge port to
// that AP. prevAP looks up where the client was last seen (device, iface) in the stored clients.
func (st *mergeState) resolveWifiAP(roleOf map[string]string, prevAP func(mac string) (dev, iface string, ok bool)) {
	for mac, o := range st.byMAC {
		if !o.WiFi {
			continue
		}
		if h := st.hint[mac]; h != "" {
			o.Device = h
		} else if e, ok := st.edge[mac]; ok && e.ap && roleOf[o.Device] != "ap" {
			o.Device, o.Iface = e.dev, e.iface
		} else if roleOf[o.Device] != "ap" && o.Device != "" {
			// Only the controller reports this client and no AP showed it on an edge port this cycle (aged-out
			// bridge host, slow poll). Keep the AP it was last seen on instead of flipping to the controller,
			// otherwise every gap counts as a roam.
			if prev, iface, ok := prevAP(mac); ok && prev != o.Device && roleOf[prev] == "ap" {
				o.Device, o.Iface = prev, iface
			}
		}
	}
}

// collect reads every fresh device snapshot. The caller holds m.mu.
func (m *Manager) collect(st *mergeState) {
	for dev, s := range m.snaps {
		if time.Since(s.at) > 3*time.Minute {
			continue // device not answering: its last answers say nothing about the network now
		}
		apBonus := 0
		if s.role == "ap" {
			apBonus = 10 // a wifi registration on the AP itself beats the CAPsMAN manager's copy
		}
		st.collectBridge(dev, s, apBonus)
		st.collectND6(s)
		st.collectARP(dev, s)
		st.collectLeases(s)
		st.collectWifi(dev, s, apBonus)
	}
	st.resolveShared()
	roleOf := map[string]string{}
	for dev, s := range m.snaps {
		roleOf[dev] = s.role
	}
	st.resolveWifiAP(roleOf, func(mac string) (string, string, bool) {
		var dev, iface string
		if m.St.DB.QueryRow(`SELECT device, iface FROM clients WHERE mac=? AND wifi=1`, mac).Scan(&dev, &iface) != nil {
			return "", "", false
		}
		return dev, iface, true
	})
}

// addVPN adds the VPN peers and drops the routers' own addresses. The caller holds m.mu.
func (m *Manager) addVPN(st *mergeState) {
	for dev, s := range m.snaps {
		if time.Since(s.at) > 3*time.Minute {
			continue
		}
		for _, v := range vpnObservations(dev, s.peers) {
			v := v
			st.byMAC[v.MAC] = &v
		}
	}
	for mac, o := range st.byMAC {
		if m.isSelf(mac, o.IP) {
			delete(st.byMAC, mac)
			m.St.DB.Exec(`DELETE FROM clients WHERE mac=?`, mac) // cleanup of rows created by earlier versions
		}
	}
}

// merge builds client observations from all device snapshots and writes them.
func (m *Manager) merge() {
	m.mu.Lock()
	st := m.newMergeState()
	m.collect(st)
	m.mu.Unlock()

	m.mu.Lock()
	m.addVPN(st)
	m.mu.Unlock()
	m.publish(st)
}

// publish names the clients, tells the hub, stores the observations and raises the new-client events.
func (m *Manager) publish(st *mergeState) {
	byMAC := st.byMAC
	obs := make([]store.Observation, 0, len(byMAC))
	ipmac := map[string]string{}
	hubNames := map[string]string{}
	for ip, mac := range st.nd6 {
		if _, ok := byMAC[mac]; ok { // never creates a client, only attaches IPv6 addresses to known ones
			ipmac[ip] = mac
		}
	}
	v6 := map[string]string{}
	for ip, mac := range st.nd6 {
		if _, ok := byMAC[mac]; ok && store.IsGlobalIPv6(ip) {
			v6[ip] = mac
		}
	}
	m.St.NoteIPv6(v6, time.Now().Unix()) // evidence for addresses from old (dynamic) prefixes
	m.St.NoteULA(ipmac, time.Now().Unix())
	var newOnes []store.Observation
	idents := m.neighborIdents()
	for mac, o := range byMAC {
		if !strings.HasPrefix(mac, "VPN:") {
			o.Vendor = m.En.Vendor(mac)
		}
		if o.Hostname == "" {
			o.Hostname = m.fallbackName(mac, o.IP, idents)
		}
		obs = append(obs, *o)
		if o.IP != "" {
			ipmac[o.IP] = mac
		}
		for _, a := range o.Aliases {
			ipmac[a] = mac
			m.rekeyAlias(mac, a)
		}
		n := o.Hostname
		if n == "" {
			n = o.IP
		}
		if n != "" {
			hubNames[mac] = n
		}
		if !m.known[mac] {
			m.known[mac] = true
			newOnes = append(newOnes, *o)
		}
	}
	if len(obs) == 0 {
		return
	}
	if !m.primed {
		// load existing clients so a restart doesn't alert for everyone
		if cs, err := m.St.Clients(); err == nil {
			for _, c := range cs {
				m.known[c.MAC] = true
			}
			newOnes = newOnes[:0]
			for _, o := range obs {
				if !containsClient(cs, o.MAC) {
					newOnes = append(newOnes, o)
				}
			}
		}
		m.primed = true
		if len(m.known) > 0 && len(newOnes) == len(obs) {
			newOnes = nil // first ever run: everything is "new", suppress alerts
		}
	}
	m.Hub.SetIPMap(ipmac, hubNames)
	m.Hub.SetWifiClients(wifiPerDevice(obs, m.Cfg.AllDevices()))
	if _, err := m.St.UpsertClients(obs); err != nil {
		m.Log.Error("upsert clients", "err", err)
	}
	m.St.MarkOffline(120)
	for _, o := range newOnes {
		m.Ev.NewClient(o.MAC, o.Hostname, o.IP)
	}
}
