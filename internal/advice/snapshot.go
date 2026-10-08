package advice

import (
	"net/netip"
	"strings"
	"time"
)

// Snapshot is the read-only view of mtmon's data that rules evaluate. Every method must be cheap to call
// repeatedly (the real implementation memoises); "no data" is expressed with empty results.
type Snapshot interface {
	Now() time.Time
	// ProcessUptime: how long mtmon itself has been running (guards "never seen" conclusions).
	ProcessUptime() time.Duration
	Devices() []Device
	Clients() []Client
	// Traffic24h: per-client totals of the last 24 h (empty when no flow data exists).
	Traffic24h() []ClientTraffic
	// TopService names the dominant service of a client in the last 24 h ("" if unknown).
	TopService(mac string) string
	// PortUse24h: per client/destination sums for a fixed set of "interesting" remote ports (see Ports*).
	PortUse24h() []PortUse
	// DoH24h: traffic to well-known DNS-over-HTTPS resolvers (by resolved host name).
	DoH24h() []PortUse
	// InboundRecent: remote hosts opening connections to LAN services on the Inbound ports.
	InboundRecent() []Inbound
	Roams24h() []RoamStat
	IPChurn30d() map[string]int
	BlockedSources24h() []BlockedSrc
	// ContactedByLAN: which of the remote addresses exchanged traffic with a LAN client in the last 24 h.
	ContactedByLAN(ips []string) map[string]bool
	FwRules() []FwRule
	// FwHistory: how long firewall log history exists (0 = none).
	FwHistory() time.Duration
}

// Ports queried from the flow rollups / raw flows.
var (
	PortsOut     = []int{21, 23, 53, 69, 80, 139, 445, 853, 3389}
	PortsInbound = []int{21, 23, 139, 445, 3389, 5900}
)

type WifiIf struct{ Name, SSID, Band string }

type FilterRule struct {
	Chain, Action, Comment, Summary string
	Log, Disabled, Managed          bool
}

// Caps is what the provisioning probe stored for UI-managed devices; nil for config-file devices.
type Caps struct {
	Fasttrack, HWOffload       bool
	FlowSupported, FlowEnabled bool
	FlowTargets                []string
	LogActions                 int // remote logging actions
	WwwSSL                     bool
	WwwSSLAddr                 string
	Filter                     []FilterRule
	DNSRedirectUDP             bool // active dst-nat rule sends UDP/53 to the router and has matched packets
	DNSRedirectTCP             bool
	WifiIfs                    []WifiIf
}

type Device struct {
	Name, Addr, Role string // role: router | ap | switch
	Model, Version   string
	Managed          bool // provisioned by mtmon (has a change manifest)
	Up               bool // currently reachable
	LiveOK           bool // CPU/Mem below are a current reading
	CPU, Mem         float64
	// History of the last 6 h (Samples = number of 1-minute samples).
	Samples                  int
	AvgCPU, MaxCPU           float64
	AvgMem, MaxMem           float64
	HighCPUFrac, HighMemFrac float64
	Firmware, FirmwareUpg    string
	FlowKnown                bool  // collector could be asked about this device
	FlowLast                 int64 // last flow packet (unix), 0 = never since mtmon started
	FwLogging                bool  // firewall log rules are registered for the device
	Caps                     *Caps
}

type Client struct {
	MAC, Label, Hostname, Vendor, IP string
	Device, SSID, Band               string
	Signal                           int // dBm, 0 = unknown
	WiFi, Online                     bool
	FirstSeen, LastSeen              int64
}

// Display is the best human name for a client.
func (c Client) Display() string {
	switch {
	case c.Label != "":
		return c.Label
	case c.Hostname != "":
		return c.Hostname
	case c.Vendor != "":
		m := c.MAC
		if len(m) > 5 {
			m = m[len(m)-5:]
		}
		return c.Vendor + " " + m
	}
	return c.MAC
}

// Randomized reports a locally administered (private/randomised) MAC address.
func (c Client) Randomized() bool {
	if len(c.MAC) < 2 {
		return false
	}
	switch c.MAC[1] {
	case '2', '6', 'A', 'E', 'a', 'e':
		return true
	}
	return false
}

type ClientTraffic struct {
	MAC               string
	Up, Down, Int, Fl int64
}

type PortUse struct {
	MAC, RIP, Host string
	Port, Proto    int
	Bytes, Flows   int64
}

type Inbound struct {
	MAC, IP      string
	Port, Proto  int
	Flows, Hosts int64
}

type RoamStat struct {
	MAC  string
	N    int
	APs  int
	Last int64
}

type BlockedSrc struct {
	IP    string
	Hits  int64
	CC    string
	ASN   int
	ASOrg string
}

type FwRule struct {
	Device, Prefix, Chain, Action, Descr string
	Managed                              bool
	Hits7d                               int64
}

// IsPublic: a routable internet address (not LAN, loopback, link-local, multicast or CGNAT).
func IsPublic(s string) bool {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return false
	}
	a = a.Unmap()
	if a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsMulticast() || a.IsUnspecified() {
		return false
	}
	if a.Is4() {
		b := a.As4()
		if b[0] == 100 && b[1]&0xC0 == 64 { // 100.64.0.0/10
			return false
		}
	}
	return true
}

// clientIndex maps MAC → client for evidence lines.
func clientIndex(s Snapshot) map[string]Client {
	m := map[string]Client{}
	for _, c := range s.Clients() {
		m[c.MAC] = c
	}
	return m
}

// nameOf resolves a MAC to a display name even if the client is unknown.
func nameOf(idx map[string]Client, mac string) string {
	if c, ok := idx[mac]; ok {
		return c.Display()
	}
	return mac
}

func routers(s Snapshot) []Device {
	var out []Device
	for _, d := range s.Devices() {
		if d.Role == "router" {
			out = append(out, d)
		}
	}
	return out
}
