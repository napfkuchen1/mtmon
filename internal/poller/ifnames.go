// SPDX-License-Identifier: AGPL-3.0-only

package poller

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// RouterOS exports flows with the SNMP ifIndex of the interfaces. REST lists the interfaces with their internal
// id ("*67", hexadecimal), and that id is the ifIndex in decimal: "*67" is 103. The mapping is read-only data from
// /interface, refreshed with the slow poll and whenever a flow names an index that is not listed.

// ifIndexOf converts a RouterOS .id ("*67") to the ifIndex (103). ok=false for anything else.
func ifIndexOf(id string) (uint32, bool) {
	id = strings.TrimPrefix(strings.TrimSpace(id), "*")
	if id == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(id, 16, 32)
	if err != nil || n == 0 {
		return 0, false
	}
	return uint32(n), true
}

// parseIfNames reads the (.id, name, type) of every interface of an /interface listing.
func parseIfNames(rows []Row) []store.IfName {
	out := make([]store.IfName, 0, len(rows))
	for _, r := range rows {
		idx, ok := ifIndexOf(r[".id"])
		if !ok || r["name"] == "" {
			continue
		}
		out = append(out, store.IfName{Idx: idx, Name: r["name"], Type: r["type"]})
	}
	return out
}

// noteIfNames stores the interface list of a device (an empty or unusable list changes nothing).
func (m *Manager) noteIfNames(dev string, rows []Row) {
	list := parseIfNames(rows)
	if len(list) == 0 {
		return
	}
	if err := m.St.SetIfNames(dev, list); err != nil {
		m.Log.Warn("interface names not stored", "device", dev, "err", err)
	}
}

// RequestIfRefresh asks the worker of a device to re-read its interface list on its next tick. Requests for the
// same device coalesce into one read.
func (m *Manager) RequestIfRefresh(dev string) {
	m.mu.Lock()
	if m.ifReq == nil {
		m.ifReq = map[string]bool{}
	}
	m.ifReq[dev] = true
	m.mu.Unlock()
}

func (m *Manager) takeIfRefresh(dev string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ifReq[dev] {
		return false
	}
	delete(m.ifReq, dev)
	return true
}

// refreshIfNames reads /interface once (GET only) and stores the names.
func (w *worker) refreshIfNames(ctx context.Context) {
	cc, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if rows, err := w.c.Get(cc, "/interface"); err == nil {
		w.m.noteIfNames(w.d.Name, rows)
	}
}
