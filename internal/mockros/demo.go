// SPDX-License-Identifier: AGPL-3.0-only

package mockros

import "fmt"

// DemoDevice is one mock device of the demo network. Listen is where the mock really listens (a loopback
// address, so no setup is needed); Show is the LAN address a real network would give it (used by the screenshot
// run, which rewrites the loopback addresses it sees; see scripts/screenshots.js).
type DemoDevice struct {
	Name, Role, Listen, Show string
}

// DemoDevices is the demo topology: router, switch, two access points. rtr-spare is not configured in the demo;
// it is the router the setup wizard is pointed at.
func DemoDevices() []DemoDevice {
	return []DemoDevice{
		{"gw-main", "router", "127.0.0.11:9001", "192.168.88.1"},
		{"sw-office", "switch", "127.0.0.12:9001", "192.168.88.2"},
		{"ap-living", "ap", "127.0.0.13:9001", "192.168.88.3"},
		{"ap-garden", "ap", "127.0.0.14:9001", "192.168.88.4"},
		{"rtr-spare", "router", "127.0.0.20:9001", "192.168.88.5"},
	}
}

// DemoNeighbors is the link tree gw-main - sw-office - {ap-living, ap-garden}.
func DemoNeighbors(name string) []Neighbor {
	mac := map[string]string{"gw-main": "AA:BB:CC:00:00:11", "sw-office": "AA:BB:CC:00:00:12", "ap-living": "AA:BB:CC:00:00:13", "ap-garden": "AA:BB:CC:00:00:14"}
	addr := map[string]string{"gw-main": "192.168.88.1", "sw-office": "192.168.88.2", "ap-living": "192.168.88.3", "ap-garden": "192.168.88.4"}
	n := func(iface, peer string) Neighbor {
		return Neighbor{Iface: iface, MAC: mac[peer], Ident: peer, Addr: addr[peer]}
	}
	switch name {
	case "gw-main":
		return []Neighbor{n("ether2", "sw-office")}
	case "sw-office":
		return []Neighbor{n("ether1", "gw-main"), n("ether2", "ap-living"), n("ether3", "ap-garden")}
	case "ap-living", "ap-garden":
		return []Neighbor{n("ether1", "sw-office")}
	}
	return nil
}

// DemoClients are 25 fictional LAN clients (RFC 1918 addresses) spread over the demo devices.
func DemoClients() []Client {
	type c struct {
		host, ip, on string
		wifi         bool
		sig          int
		port         string
	}
	list := []c{
		{"homeassistant", "192.168.88.30", "gw-main", false, 0, "ether5"},
		{"nas-synology", "192.168.88.12", "gw-main", false, 0, "ether6"},
		{"wg-laptop-remote", "10.20.0.2", "gw-main", false, 0, "wireguard1"},
		{"printer-hp", "192.168.88.50", "sw-office", false, 0, "ether5"},
		{"pc-office", "192.168.88.60", "sw-office", false, 0, "ether6"},
		{"thinkpad-work", "192.168.88.61", "sw-office", false, 0, "ether7"},
		{"appletv-living", "192.168.88.70", "sw-office", false, 0, "ether8"},
		{"iphone-anna", "192.168.88.23", "ap-living", true, -52, ""},
		{"galaxy-s24", "192.168.88.41", "ap-living", true, -61, ""},
		{"macbook-ben", "192.168.88.24", "ap-living", true, -48, ""},
		{"ipad-kids", "192.168.88.25", "ap-living", true, -66, ""},
		{"samsung-tv", "192.168.88.71", "ap-living", true, -58, ""},
		{"sonos-kitchen", "192.168.88.72", "ap-living", true, -55, ""},
		{"echo-kitchen", "192.168.88.73", "ap-living", true, -63, ""},
		{"kindle-ben", "192.168.88.26", "ap-living", true, -70, ""},
		{"ps5-living", "192.168.88.74", "ap-living", true, -50, ""},
		{"hue-bridge", "192.168.88.80", "sw-office", false, 0, "ether9"},
		{"roborock-s7", "192.168.88.81", "ap-living", true, -72, ""},
		{"cam-driveway", "192.168.88.90", "ap-garden", true, -57, ""},
		{"cam-garden", "192.168.88.91", "ap-garden", true, -64, ""},
		{"doorbell-front", "192.168.88.92", "ap-garden", true, -60, ""},
		{"shelly-plug-garden", "192.168.88.93", "ap-garden", true, -69, ""},
		{"esp32-irrigation", "192.168.88.94", "ap-garden", true, -74, ""},
		{"thermostat-hall", "192.168.88.82", "ap-living", true, -65, ""},
		{"iphone-guest", "192.168.88.27", "ap-garden", true, -68, ""},
	}
	out := make([]Client, 0, len(list))
	for i, x := range list {
		cl := Client{MAC: fmt.Sprintf("02:AA:BB:10:00:%02X", i+1), IP: x.ip, Host: x.host, WiFi: x.wifi, Signal: x.sig, On: x.on, Port: x.port}
		if x.wifi {
			cl.SSID = "Home"
		}
		out = append(out, cl)
	}
	return out
}
