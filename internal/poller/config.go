package poller

import (
	"context"
	"strconv"
	"strings"
)

// ConfigSections are the read-only REST lists that make up a configuration snapshot. They are plain GETs of the
// monitoring login; a list the router does not offer (missing package, no permission) is left out of the snapshot.
var ConfigSections = []string{
	"/ip/address", "/ip/firewall/filter", "/ip/firewall/nat", "/ip/firewall/mangle", "/ip/firewall/address-list",
	"/ip/route", "/ip/pool", "/ip/dhcp-server", "/ip/dhcp-server/network", "/ip/dns", "/ip/service",
	"/interface", "/interface/bridge", "/interface/bridge/port", "/interface/bridge/vlan", "/interface/vlan",
	"/interface/wireguard", "/interface/wireguard/peers", "/interface/wifi", "/interface/wireless",
	"/interface/wifi/access-list", "/interface/wifi/provisioning", "/interface/wifi/configuration",
	"/interface/wifi/security", "/interface/wifi/channel", "/interface/wifi/datapath", "/interface/wifi/steering",
	"/interface/wifi/capsman", "/interface/wifi/cap",
	"/system/identity", "/system/ntp/client", "/system/scheduler", "/user", "/snmp",
}

// ConfigSnap is one device's configuration: section path -> rows.
type ConfigSnap map[string][]Row

// volatileKeys change without anyone editing the router (counters, timestamps, live state).
var volatileKeys = map[string]bool{
	"bytes": true, "packets": true, "last-seen": true, "running": true, "slave": true, "actual-mtu": true,
	"last-link-up-time": true, "last-link-down-time": true, "link-downs": true, "last-logged-in": true,
	"rx-byte": true, "tx-byte": true, "rx-packet": true, "tx-packet": true, "fp-rx-byte": true, "fp-tx-byte": true,
	"fp-rx-packet": true, "fp-tx-packet": true, "rx-drop": true, "tx-drop": true, "rx-error": true, "tx-error": true,
	"tx-queue-drop": true, "rx-bits-per-second": true, "tx-bits-per-second": true, "uptime": true, "expires-after": true,
	"active": true, "inactive": true, "ecmp": true, "hw-offloaded": true, "gateway-status": true, "distance-active": true,
	"cache-used": true, "last-handshake": true, "current-endpoint-address": true, "current-endpoint-port": true,
	// bridge port / STP runtime state
	"debug-info": true, "tx-bpdu": true, "rx-bpdu": true, "last-topology-change": true, "topology-change-count": true,
	"role": true, "port-number": true, "root-path-cost": true, "designated-bridge": true,
	"designated-port-number": true, "designated-cost": true, "sent-tc": true, "received-tc": true, "forwarding": true,
	"discard-transitions": true, "forward-transitions": true, "rx-tc": true, "tx-tc": true, "topology-changes": true,
	"external-fdb-status": true, "inactive-port-cost": true, "fast-leave-active": true,
	// NTP client runtime state
	"freq-drift": true, "system-offset": true, "synced-server": true, "synced-stratum": true,
	// scheduler runtime state
	"next-run": true, "run-count": true,
}

// volatileBySection are runtime fields whose names are only live values inside one section (elsewhere the same
// name can be a setting, e.g. "status", "used" or "available").
var volatileBySection = map[string]map[string]bool{
	"/ip/pool":           {"used": true, "available": true},
	"/system/ntp/client": {"status": true},
	"/interface/bridge/port": {"status": true, "edge-port": true, "point-to-point-port": true,
		"edge-port-discovery-active": true},
}

// secretKey: such fields never reach a snapshot, whatever the router's policy lets the login read.
func secretKey(k string) bool {
	return strings.Contains(k, "password") || strings.Contains(k, "private-key") || strings.Contains(k, "secret") ||
		strings.Contains(k, "pre-shared-key") || strings.Contains(k, "passphrase") || k == "preshared-key"
}

// ScrubRow drops secrets and volatile fields. ok=false for rows the router creates by itself (dynamic ones),
// because they come and go without a configuration change.
func ScrubRow(r Row) (Row, bool) { return ScrubRowIn("", r) }

// ScrubRowIn is ScrubRow with the section-specific runtime fields of that section removed as well.
func ScrubRowIn(section string, r Row) (Row, bool) {
	if r["dynamic"] == "true" {
		return nil, false
	}
	out := make(Row, len(r))
	vs := volatileBySection[section]
	for k, v := range r {
		if volatileKeys[k] || vs[k] || secretKey(k) || k == "dynamic" || k == "invalid" {
			continue
		}
		out[k] = v
	}
	return out, true
}

// HistoryMark is a cheap fingerprint of /system/history: the entry count and the newest entry. It changes whenever
// someone edits the router, so a poll of it tells when a new snapshot is worth taking. "" when the call fails.
func (c *Client) HistoryMark(ctx context.Context) string {
	rows, err := c.Get(ctx, "/system/history")
	if err != nil {
		return ""
	}
	return historyMark(rows)
}

func historyMark(rows []Row) string {
	if len(rows) == 0 {
		return "0"
	}
	l := rows[len(rows)-1]
	return strconv.Itoa(len(rows)) + "|" + l[".id"] + "|" + l["time"] + "|" + l["action"] + "|" + l["by"]
}

// ChangedBy names who made the newest changes according to /system/history (the "by" of the last few entries,
// newest first, without repeats). Empty when the router lists nobody or the call fails.
func (c *Client) ChangedBy(ctx context.Context) []string {
	rows, err := c.Get(ctx, "/system/history")
	if err != nil {
		return nil
	}
	return changedBy(rows, 5)
}

func changedBy(rows []Row, n int) []string {
	var out []string
	seen := map[string]bool{}
	for i := len(rows) - 1; i >= 0 && i >= len(rows)-n; i-- { // RouterOS lists oldest first
		by := rows[i]["by"]
		if by != "" && !seen[by] {
			seen[by] = true
			out = append(out, by)
		}
	}
	return out
}

// ConfigSnapshot reads every section. Sections that fail are skipped; err is set only when none could be read.
func (c *Client) ConfigSnapshot(ctx context.Context) (ConfigSnap, error) {
	snap := ConfigSnap{}
	var last error
	for _, p := range ConfigSections {
		rows, err := c.Get(ctx, p)
		if err != nil {
			last = err
			continue
		}
		list := []Row{}
		for _, r := range rows {
			if s, ok := ScrubRowIn(p, r); ok {
				list = append(list, s)
			}
		}
		snap[p] = list
	}
	if len(snap) == 0 && last != nil {
		return nil, last
	}
	return snap, nil
}
