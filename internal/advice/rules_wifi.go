package advice

import (
	"fmt"
	"sort"
	"strings"
)

const (
	WeakSignal   = -75 // dBm
	RoamsPerDay  = 20
	APClientsMax = 30
	// a client counts as "online" for Wi-Fi rules when it is flagged online and has a Wi-Fi association
)

func wifiOnline(s Snapshot) []Client {
	var out []Client
	for _, c := range s.Clients() {
		if c.WiFi && c.Online && c.Device != "" {
			out = append(out, c)
		}
	}
	return out
}

func ruleWeakSignal(s Snapshot) ([]Suggestion, string) {
	cls := wifiOnline(s)
	if len(cls) == 0 {
		return nil, "no Wi-Fi clients online"
	}
	byAP := map[string][]Client{}
	total := map[string]int{}
	for _, c := range cls {
		total[c.Device]++
		if c.Signal != 0 && c.Signal < WeakSignal {
			byAP[c.Device] = append(byAP[c.Device], c)
		}
	}
	var out []Suggestion
	for ap, weak := range byAP {
		sort.Slice(weak, func(i, j int) bool { return weak[i].Signal < weak[j].Signal })
		var names []string
		for _, c := range weak {
			names = append(names, fmt.Sprintf("%s (%d dBm)", c.Display(), c.Signal))
		}
		out = append(out, Suggestion{
			Subject: ap, Severity: SevTip, Category: CatWifi, Link: "clients", Confidence: "medium",
			Title: fmt.Sprintf("Some Wi-Fi clients have a weak signal on %s", ap),
			Why:   "Below about -75 dBm a Wi-Fi link falls back to low speeds, needs many retransmissions and slows down everyone sharing the channel. Streaming, calls and cloud sync of those devices suffer first.",
			Evidence: []string{
				fmt.Sprintf("%d of %d Wi-Fi clients on %s are weaker than %d dBm", len(weak), total[ap], ap, WeakSignal),
				fmt.Sprintf("Weakest: %s", list(names, 6)),
			},
			Steps: []string{
				"Move the access point closer to those devices, or add another access point where the coverage is thin.",
				"Check the transmit power and channel width of the access point; 5 GHz reaches less far through walls than 2.4 GHz.",
				"Metal, mirrors and aquariums between device and access point reduce the signal a lot.",
			},
			Limits: "mtmon shows the signal at the moment of the last poll; it does not keep signal history, so this may be a short dip of a moving device.",
		})
	}
	return out, ""
}

func is24(band string) bool {
	b := strings.ToLower(band)
	return strings.Contains(b, "2ghz") || strings.Contains(b, "2.4")
}
func is5(band string) bool {
	b := strings.ToLower(band)
	return strings.Contains(b, "5ghz") || strings.HasPrefix(b, "5g")
}

func ruleBand24(s Snapshot) ([]Suggestion, string) {
	cls := wifiOnline(s)
	if len(cls) == 0 {
		return nil, "no Wi-Fi clients online"
	}
	has5 := map[string]bool{}
	for _, c := range cls {
		if c.SSID != "" && is5(c.Band) {
			has5[c.SSID] = true
		}
	}
	for _, d := range s.Devices() {
		if d.Caps == nil {
			continue
		}
		for _, w := range d.Caps.WifiIfs {
			if w.SSID != "" && is5(w.Band) {
				has5[w.SSID] = true
			}
		}
	}
	bySSID := map[string][]Client{}
	for _, c := range cls {
		if c.SSID == "" || !is24(c.Band) || !has5[c.SSID] {
			continue
		}
		if c.Signal != 0 && c.Signal < -70 { // weak devices are often better off on 2.4 GHz
			continue
		}
		bySSID[c.SSID] = append(bySSID[c.SSID], c)
	}
	var out []Suggestion
	for ssid, cs := range bySSID {
		if len(cs) < 2 {
			continue
		}
		var names []string
		for _, c := range cs {
			names = append(names, c.Display())
		}
		sort.Strings(names)
		out = append(out, Suggestion{
			Subject: ssid, Severity: SevTip, Category: CatWifi, Link: "clients", Confidence: "medium",
			Title: fmt.Sprintf("Clients on 2.4 GHz could use 5 GHz on \"%s\"", ssid),
			Why:   "2.4 GHz has only three usable channels, is shared with many neighbours and gadgets, and is much slower than 5 GHz. Devices that sit on 2.4 GHz although the same network also offers 5 GHz often just picked the first band they found.",
			Evidence: []string{
				fmt.Sprintf("%d clients are connected on 2.4 GHz with a decent signal although \"%s\" also offers 5 GHz", len(cs), ssid),
				fmt.Sprintf("Clients: %s", list(names, 8)),
			},
			Steps: []string{
				"Many smart-home devices only support 2.4 GHz; leave those as they are.",
				"For phones, laptops and TVs, enable band steering on the access points or offer a separate 5 GHz network name and connect them to it once.",
			},
			Limits: "mtmon cannot see whether a device is 5 GHz capable.",
		})
	}
	return out, ""
}

func ruleRoaming(s Snapshot) ([]Suggestion, string) {
	rs := s.Roams24h()
	if len(rs) == 0 {
		return nil, ""
	}
	idx := clientIndex(s)
	var out []Suggestion
	for _, r := range rs {
		if r.N < RoamsPerDay {
			continue
		}
		name := nameOf(idx, r.MAC)
		out = append(out, Suggestion{
			Subject: r.MAC, Severity: SevTip, Category: CatWifi, Link: "client/" + r.MAC, Confidence: "medium",
			Title: fmt.Sprintf("%s switches between access points very often", name),
			Why:   "A client that hops between access points many times a day is usually sitting between two cells with similar signal strength. Every hop interrupts the connection for a moment, which shows up as stutter in calls and video.",
			Evidence: []string{
				fmt.Sprintf("%s roamed %d times in the last 24 h between %d access points", name, r.N, max(r.APs, 2)),
			},
			Steps: []string{
				"Lower the transmit power of the access points near that spot so the cells overlap less.",
				"Enable fast roaming (802.11r) and neighbour reports/BSS transition (802.11k/v) in the Wi-Fi configuration if all clients support them.",
				"Alternatively move the device or an access point so one of them clearly wins.",
			},
		})
	}
	return out, ""
}

func ruleAPLoad(s Snapshot) ([]Suggestion, string) {
	cls := wifiOnline(s)
	if len(cls) == 0 {
		return nil, "no Wi-Fi clients online"
	}
	n := map[string]int{}
	for _, c := range cls {
		n[c.Device]++
	}
	var out []Suggestion
	for ap, k := range n {
		if k < APClientsMax {
			continue
		}
		out = append(out, Suggestion{
			Subject: ap, Severity: SevInfo, Category: CatWifi, Link: "device/" + ap, Confidence: "medium",
			Title: fmt.Sprintf("%s carries many Wi-Fi clients", ap),
			Why:   "Every client shares the airtime of its access point. Beyond a few dozen active clients, speed and latency drop for everyone, especially with older or 2.4 GHz devices in the mix.",
			Evidence: []string{
				fmt.Sprintf("%d Wi-Fi clients are connected to %s right now", k, ap),
			},
			Steps: []string{
				"Add an access point nearby and lower the transmit power of both, so clients spread out.",
				"Move fixed devices (TVs, consoles, PCs) to a network cable.",
			},
			Limits: "The number of connected clients says nothing about how much traffic they produce.",
		})
	}
	return out, ""
}

// ---- hygiene ----

const (
	UnidentMin   = 4
	UnidentShare = 0.2
	StaleDays    = 90
	StaleMin     = 20
)

func ruleUnidentified(s Snapshot) ([]Suggestion, string) {
	var online, un []Client
	rnd := 0
	for _, c := range s.Clients() {
		if !c.Online {
			continue
		}
		online = append(online, c)
		if c.Label == "" && c.Hostname == "" {
			un = append(un, c)
			if c.Randomized() {
				rnd++
			}
		}
	}
	if len(online) == 0 {
		return nil, "no clients online"
	}
	if len(un) < UnidentMin || float64(len(un)) < UnidentShare*float64(len(online)) {
		return nil, ""
	}
	var names []string
	for _, c := range un {
		n := c.MAC
		if c.IP != "" {
			n += " (" + c.IP + ")"
		}
		if c.Vendor != "" {
			n += " " + c.Vendor
		}
		names = append(names, n)
	}
	return []Suggestion{{
		Severity: SevInfo, Category: CatHygiene, Link: "clients", Confidence: "high",
		Title: "Many devices have no name",
		Why:   "Devices without a hostname or label show up as bare MAC addresses in lists, charts and alerts, which makes it hard to see who is using the network. Phones and laptops with a randomized (private) Wi-Fi address look different on every network and are especially hard to recognise.",
		Evidence: []string{
			fmt.Sprintf("%d of %d online devices have neither a hostname nor a label (%d use a randomized MAC address)", len(un), len(online), rnd),
			fmt.Sprintf("Examples: %s", list(names, 5)),
		},
		Steps: []string{
			"Open the Clients page, click a device and give it a label. The label stays attached to its MAC address.",
			"For phones, labelling once per Wi-Fi network is enough; the private address stays the same for that network.",
		},
	}}, ""
}

func ruleStaleClients(s Snapshot) ([]Suggestion, string) {
	cut := since(s.Now(), StaleDays*24*3600*1e9)
	n, total := 0, 0
	for _, c := range s.Clients() {
		total++
		if !c.Online && c.LastSeen > 0 && c.LastSeen < cut {
			n++
		}
	}
	if total == 0 {
		return nil, "no clients known yet"
	}
	if n < StaleMin {
		return nil, ""
	}
	return []Suggestion{{
		Severity: SevInfo, Category: CatHygiene, Link: "clients", Confidence: "high",
		Title: "Old devices can be cleaned up",
		Why:   "Devices that have not been seen for months only clutter the client list and the statistics. Removing them keeps the lists short and makes new, unknown devices easier to spot.",
		Evidence: []string{
			fmt.Sprintf("%d of %d known devices were last seen more than %d days ago", n, total, StaleDays),
		},
		Steps: []string{"Open the Clients page and use the clean-up option to remove devices that are no longer around."},
	}}, ""
}

var serverHint = []string{"nas", "server", "srv", "pi", "home", "hass", "proxmox", "pve", "docker", "printer", "camera", "cam", "nvr", "plex", "synology", "diskstation", "unraid", "truenas"}

func serverLike(c Client) bool {
	h := strings.ToLower(c.Hostname + " " + c.Label)
	for _, k := range serverHint {
		if strings.Contains(h, k) {
			return true
		}
	}
	return false
}

func ruleStaticLease(s Snapshot) ([]Suggestion, string) {
	churn := s.IPChurn30d()
	if churn == nil {
		return nil, "no IP history"
	}
	now := s.Now().Unix()
	var hit []Client
	for _, c := range s.Clients() {
		if churn[c.MAC] < 3 || !c.Online || c.FirstSeen == 0 || now-c.FirstSeen < 7*86400 {
			continue
		}
		if c.WiFi && !serverLike(c) {
			continue
		}
		hit = append(hit, c)
	}
	if len(hit) == 0 {
		return nil, ""
	}
	sort.Slice(hit, func(i, j int) bool { return churn[hit[i].MAC] > churn[hit[j].MAC] })
	var names, cmds []string
	for i, c := range hit {
		names = append(names, fmt.Sprintf("%s (%d addresses)", c.Display(), churn[c.MAC]))
		if i < 5 {
			cmds = append(cmds, fmt.Sprintf("/ip dhcp-server lease make-static [find mac-address=%s]", c.MAC))
		}
	}
	return []Suggestion{{
		Severity: SevInfo, Category: CatMonitoring, Link: "clients", Confidence: "high",
		Title: "Always-on devices keep changing their IP address",
		Why:   "Servers, printers, cameras and similar devices should keep one address. When it changes, port forwards, firewall rules and bookmarks break, and mtmon has to stitch the history of one device together from several addresses.",
		Evidence: []string{
			fmt.Sprintf("%d devices used 3 or more different IP addresses in the last 30 days", len(hit)),
			fmt.Sprintf("Devices: %s", list(names, 6)),
		},
		Steps: []string{
			"Turn the current DHCP lease of each device into a static lease (Winbox: IP > DHCP Server > Leases > Make Static).",
		},
		Commands: strings.Join(cmds, "\n"),
		Limits:   "Wired devices and devices with a server-like name are checked; mtmon cannot see whether a device is always on.",
	}}, ""
}
