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
	// IPv6Prefixes14d: per client the /64 prefixes its global IPv6 addresses were seen in (newest first).
	IPv6Prefixes14d() map[string][]V6Prefix
	BlockedSources24h() []BlockedSrc
	// ContactedByLAN: which of the remote addresses exchanged traffic with a LAN client in the last 24 h.
	ContactedByLAN(ips []string) map[string]bool
	FwRules() []FwRule
	// FwHistory: how long firewall log history exists (0 = none).
	FwHistory() time.Duration
	// SelfHealth: free space of mtmon's own data directory and database growth.
	SelfHealth() SelfHealth
	// GhostDNS7d: clients that sent DNS to a local address no device owns (answered only by the redirect of the router).
	GhostDNS7d() []GhostDNS
}

// GhostDNS is one client that uses a DNS server that does not exist.
type GhostDNS struct {
	MAC, IP     string
	Flows, Last int64
}

// SelfHealth is mtmon's own resource situation. Known is false where the file system cannot be asked.
type SelfHealth struct {
	Known                           bool
	FreeBytes, TotalBytes, DBBytes  int64
	GrowthPerDay                    int64 // 0 = not growing or not enough samples yet
	RawRetentionDays, RetentionDays int
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
	LogPrefix, Proto, DPort         string
	Packets                         int64
	Counted                         bool // Packets is a real reading
}

// Caps is what the provisioning probe stored for UI-managed devices; nil for config-file devices.
type Caps struct {
	Addresses                  []string // own interface addresses, "ip/mask on iface"
	Fasttrack, HWOffload       bool
	FlowSupported, FlowEnabled bool
	FlowTargets                []string
	LogActions                 int // remote logging actions
	WwwSSL                     bool
	WwwSSLAddr                 string
	Filter                     []FilterRule
	DNSRedirectUDP             bool // active dst-nat rule sends UDP/53 to the router (rule existence counts, not its packet counter)
	DNSRedirectTCP             bool
	DNSRedirectUDP6            bool // the same for IPv6 (/ipv6/firewall/nat)
	DNSRedirectTCP6            bool
	DNSRedirectVPNUDP          bool // rule limited to a tunnel interface (UDP v4 / v6 below)
	DNSRedirectVPNTCP          bool
	DNSRedirectVPNUDP6         bool
	DNSRedirectVPNTCP6         bool
	WifiIfs                    []WifiIf
	ProbedAt                   int64 // unix time the router was read, 0 = unknown (older stored probe)
	FilesReadable              bool  // the router lets mtmon list its files
	NewestBackup, NewestExport int64 // unix time of the newest *.backup / *.rsc file, 0 = none
	Certs                      []Cert
	UpdateInstalled            string // RouterOS the router reported at its last update check
	UpdateLatest               string // newest version in the router's channel at that check ("" = never checked)
	UpdateChannel              string
	CAP, CAPsMAN               bool  // device is a CAP / runs the CAPsMAN controller
	IPv6PD                     bool  // a DHCPv6 client requests a prefix: the IPv6 prefix is dynamic
	RAValid, RAPreferred       int64 // lifetimes of the router advertisements ("nd prefix default"), seconds, 0 = unknown
}

// Cert is a certificate used by a router service (www-ssl, api-ssl).
type Cert struct {
	Name, Service string
	Expires       int64 // unix, 0 = unknown
}

// ReadNote says when the router was last read, for the evidence of rules that depend on its rule set.
func (c *Caps) ReadNote() string {
	if c == nil || c.ProbedAt == 0 {
		return "router rules read at an unknown time (older stored probe, refreshed hourly)"
	}
	return "router rules read " + time.Unix(c.ProbedAt, 0).Format("2006-01-02 15:04")
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
	MemTrend                 float64 // RAM % of the 2nd half of the window minus the 1st half
	MinFreeMiB               float64 // smallest free RAM in the window, 0 = unknown
	Firmware, FirmwareUpg    string
	FlowKnown                bool  // collector could be asked about this device
	FlowLast                 int64 // last flow packet (unix), 0 = never since mtmon started
	FwLogging                bool  // firewall log rules are registered for the device
	Caps                     *Caps
	TunnelIfs                []string // interfaces of a tunnel type (wg, ovpn, l2tp, sstp ...), whatever their name
}

type Client struct {
	MAC, Label, Hostname, Vendor, IP string
	Device, SSID, Band               string
	Signal                           int // dBm, 0 = unknown
	WiFi, Online                     bool
	FirstSeen, LastSeen              int64
	SigSamples                       int64   // Wi-Fi signal readings of the last 24 h (0 = no history)
	WeakFrac                         float64 // share of those readings weaker than WeakSignal

	Group      string  // client group ("" = none)
	ChattyMult float64 // the group's factor for the "many connections" limit (0 = 1)
	IgnoreWeak bool    // the group says a weak Wi-Fi signal is normal
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
	Hits7d, Hits24h                      int64
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

// ownPublicIPs: public addresses that belong to the managed routers themselves (WAN / PPPoE interface).
func ownPublicIPs(s Snapshot) map[string]bool {
	out := map[string]bool{}
	for _, d := range s.Devices() {
		if d.Caps == nil {
			continue
		}
		for _, a := range d.Caps.Addresses {
			ip, _, _ := strings.Cut(a, " ")
			ip, _, _ = strings.Cut(ip, "/")
			if IsPublic(ip) {
				out[ip] = true
			}
		}
	}
	return out
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

// V6Prefix is one /64 a client had global addresses in, with the first and last time it was seen.
type V6Prefix struct {
	Prefix      string
	Addrs       int
	First, Last int64
}
