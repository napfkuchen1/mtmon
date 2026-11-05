// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"fmt"
	"net/http"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// Per-interface (VLAN) view. The interface is a flow exporter's ifIndex; names come from the router's /interface
// list and are resolved here, at read time (store/iface.go).

// ifaceFilter reads ?iface=dev:idx. set is false when the parameter is absent. An unparseable value or an
// interface the data does not know is not an error: callers answer with empty lists.
func ifaceFilter(r *http.Request) (sel store.IfaceSel, set bool) {
	v := r.URL.Query().Get("iface")
	if v == "" {
		return sel, false
	}
	sel, ok := store.ParseIface(v)
	if !ok {
		return store.IfaceSel{Dev: "\x00"}, true // matches nothing
	}
	return sel, true
}

// topFor is topByOwn with the optional ?iface= filter. A single client's lists (mac) are not split by interface.
func (s *Server) topFor(r *http.Request, what string, since int64, limit int, mac string, own bool) ([]store.TopRow, error) {
	sel, set := ifaceFilter(r)
	if !set || mac != "" {
		return s.topByOwn(what, since, limit, mac, own)
	}
	rows, err := s.St.TopByIface(what, since, limit, own, sel)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []store.TopRow{}
	}
	return rows, nil
}

// IfaceInfo is one interface as the UI shows it.
type IfaceInfo struct {
	Token    string `json:"token"` // value for ?iface=
	Dev      string `json:"dev"`
	Idx      uint32 `json:"idx"`
	Name     string `json:"name"` // "" for index 0: the router itself (the UI translates it)
	Type     string `json:"type,omitempty"`
	Self     bool   `json:"self,omitempty"`
	Known    bool   `json:"known"` // the router lists this index; false shows as if<N>
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
	Internal int64  `json:"internal"`
	Flows    int64  `json:"flows"`
	Clients  int64  `json:"clients"`
}

type ifaceNamer map[string]map[uint32]store.IfName

func (n ifaceNamer) of(s *Server, dev string) map[uint32]store.IfName {
	m, ok := n[dev]
	if !ok {
		m = s.St.IfNames(dev)
		n[dev] = m
	}
	return m
}

func (s *Server) ifaceInfos(n ifaceNamer, rows []store.IfaceRow) []IfaceInfo {
	out := make([]IfaceInfo, 0, len(rows))
	for _, r := range rows {
		names := n.of(s, r.Dev)
		in := names[r.Idx]
		out = append(out, IfaceInfo{
			Token: store.IfaceSel{Dev: r.Dev, Idx: r.Idx}.String(), Dev: r.Dev, Idx: r.Idx,
			Name: store.IfLabel(names, r.Idx), Type: in.Type, Self: r.Idx == 0, Known: s.St.IfKnown(r.Dev, r.Idx),
			Up: r.Up, Down: r.Down, Internal: r.Internal, Flows: r.Flows, Clients: r.Clients,
		})
	}
	return out
}

// interfaces: GET /api/interfaces?range=… – traffic per LAN interface (VLAN) and per WAN interface, and the routed
// traffic between interfaces. since is the first hour with interface data: older traffic has none attached.
func (s *Server) interfaces(w http.ResponseWriter, r *http.Request) {
	since, _, rn := parseRange(r)
	own := wantOwn(r)
	lan, wan, first, err := s.St.IfaceUsage(since, own)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	mx, err := s.St.IfaceMatrix(since, own)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	n := ifaceNamer{}
	type cell struct {
		Dev      string `json:"dev"`
		From     uint32 `json:"from"`
		To       uint32 `json:"to"`
		FromName string `json:"from_name"`
		ToName   string `json:"to_name"`
		Bytes    int64  `json:"bytes"`
	}
	cells := make([]cell, 0, len(mx))
	for _, m := range mx {
		names := n.of(s, m.Dev)
		cells = append(cells, cell{m.Dev, m.From, m.To, store.IfLabel(names, m.From), store.IfLabel(names, m.To), m.Bytes})
	}
	jsonOut(w, map[string]any{
		"range": rn, "since": first,
		"interfaces": s.ifaceInfos(n, lan), "wan": s.ifaceInfos(n, wan), "matrix": cells,
	})
}

// exportInterfaces: GET /api/export/interfaces – the interface table as CSV/XLSX.
func (s *Server) exportInterfaces(w http.ResponseWriter, r *http.Request) {
	since, _, rn := parseRange(r)
	lan, wan, _, err := s.St.IfaceUsage(since, wantOwn(r))
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	n := ifaceNamer{}
	t := exportTable{File: "interfaces-" + rn, Sheet: "Interfaces",
		Head: []string{"side", "device", "interface", "ifindex", "bytes_up", "bytes_down", "bytes_internal", "flows", "clients"}}
	for _, g := range []struct {
		side string
		rows []store.IfaceRow
	}{{"lan", lan}, {"wan", wan}} {
		for _, i := range s.ifaceInfos(n, g.rows) {
			name := i.Name
			if i.Self {
				name = "router"
			}
			t.Rows = append(t.Rows, []any{g.side, i.Dev, name, fmt.Sprint(i.Idx), Bytes(i.Up), Bytes(i.Down), Bytes(i.Internal), i.Flows, i.Clients})
		}
	}
	sendTable(w, r, t)
}
