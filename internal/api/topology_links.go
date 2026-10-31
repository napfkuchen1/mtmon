// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"sort"
	"strings"

	"github.com/napfkuchen1/mtmon/internal/store"
)

type deviceLink struct{ From, To, Port string } // Port = port on the From side

// neighborPort reduces an /ip/neighbor "interface" value ("ether2,bridge", "bridge/ether5") to the physical port.
func neighborPort(iface string) string {
	var ports, bridges []string
	for _, p := range strings.Split(iface, ",") {
		p = strings.TrimSpace(p)
		if i := strings.LastIndex(p, "/"); i >= 0 {
			p = p[i+1:]
		}
		switch {
		case p == "":
		case strings.HasPrefix(p, "bridge"):
			bridges = append(bridges, p)
		default:
			ports = append(ports, p)
		}
	}
	if len(ports) == 0 {
		ports = bridges
	}
	sort.Strings(ports)
	return strings.Join(ports, ",")
}

// deviceLinks turns the LLDP/MNDP neighbor tables into a tree of direct links, oriented from root.
// MNDP crosses every bridge, so a device sees all others of the segment on its uplink port. Two devices are
// directly connected only if nobody sits between them: a third device that is behind A's port towards B and
// behind B's port towards A would be the hop in the middle. Links that are only visible "through" another
// device are dropped, and each pair keeps one edge (parent → child).
func deviceLinks(ns []store.Neighbor, managed map[string]bool, root string) []deviceLink {
	type key struct{ dev, port string }
	behind := map[key]map[string]bool{} // (device, port) → managed devices seen on that port
	raw := map[key]string{}             // original interface text for the label
	for _, n := range ns {
		if !managed[n.Ident] || n.Ident == n.Device || !managed[n.Device] {
			continue
		}
		k := key{n.Device, neighborPort(n.Iface)}
		if behind[k] == nil {
			behind[k] = map[string]bool{}
			raw[k] = n.Iface
		}
		behind[k][n.Ident] = true
	}
	adj := map[string]map[string]string{} // a → b → port on a
	for ka, sa := range behind {
		for b := range sa {
			a := ka.dev
			direct := true
			for kb, sb := range behind { // every port of b that sees a
				if kb.dev != b || !sb[a] {
					continue
				}
				for c := range sa {
					if c != b && sb[c] {
						direct = false
					}
				}
			}
			if !direct {
				continue
			}
			if adj[a] == nil {
				adj[a] = map[string]string{}
			}
			if adj[b] == nil {
				adj[b] = map[string]string{}
			}
			adj[a][b] = raw[ka]
			if _, ok := adj[b][a]; !ok {
				adj[b][a] = "" // port on b is filled when b reports a itself
			}
		}
	}
	var out []deviceLink
	if root == "" {
		return out
	}
	seen := map[string]bool{root: true}
	q := []string{root}
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		var next []string
		for b := range adj[cur] {
			next = append(next, b)
		}
		sort.Strings(next)
		for _, b := range next {
			if seen[b] {
				continue
			}
			seen[b] = true
			q = append(q, b)
			out = append(out, deviceLink{From: cur, To: b, Port: adj[cur][b]})
		}
	}
	return out
}
