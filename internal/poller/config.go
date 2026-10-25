package poller

import (
	"context"
	"strings"
)

// ConfigSections are the read-only REST lists that make up a configuration snapshot. They are plain GETs of the
// monitoring login; a list the router does not offer (missing package, no permission) is left out of the snapshot.
var ConfigSections = []string{
	"/ip/address", "/ip/firewall/filter", "/ip/firewall/nat", "/ip/firewall/mangle", "/ip/firewall/address-list",
	"/ip/route", "/ip/pool", "/ip/dhcp-server", "/ip/dhcp-server/network", "/ip/dns", "/ip/service",
	"/interface", "/interface/bridge", "/interface/bridge/port", "/interface/bridge/vlan", "/interface/vlan",
	"/interface/wireguard", "/interface/wireguard/peers", "/interface/wifi", "/interface/wireless",
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
}

// secretKey: such fields never reach a snapshot, whatever the router's policy lets the login read.
func secretKey(k string) bool {
	return strings.Contains(k, "password") || strings.Contains(k, "private-key") || strings.Contains(k, "secret") ||
		strings.Contains(k, "pre-shared-key") || strings.Contains(k, "passphrase") || k == "preshared-key"
}

// ScrubRow drops secrets and volatile fields. ok=false for rows the router creates by itself (dynamic ones),
// because they come and go without a configuration change.
func ScrubRow(r Row) (Row, bool) {
	if r["dynamic"] == "true" {
		return nil, false
	}
	out := make(Row, len(r))
	for k, v := range r {
		if volatileKeys[k] || secretKey(k) || k == "dynamic" || k == "invalid" {
			continue
		}
		out[k] = v
	}
	return out, true
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
			if s, ok := ScrubRow(r); ok {
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
