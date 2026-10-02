package enrich

import (
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/daniel/mtmon/internal/store"
)

// RuleKinds in precedence order (later = stronger): a rule for one exact IP beats a hostname rule,
// which beats an ASN rule, which beats a plain port rule; the built-in IANA names come last.
var kindRank = map[string]int{"port": 1, "asn": 2, "host": 3, "cidr": 4, "ip": 5}

type compiled struct {
	store.ServiceRule
	prefix netip.Prefix
	port   uint16
	asn    uint32
	rank   int
	spec   int // tie-break: longer prefix / explicit proto
}

// Classifier labels flows with a service name: user rules first, then built-in port names.
type Classifier struct {
	mu    sync.RWMutex
	rules []compiled // sorted weakest → strongest
	cats  map[string]string
}

func NewClassifier() *Classifier { return &Classifier{cats: map[string]string{}} }

// ValidateRule checks and normalises a rule; the returned rule is safe to store.
func ValidateRule(r store.ServiceRule) (store.ServiceRule, error) {
	r.Name = strings.TrimSpace(r.Name)
	r.Category = strings.TrimSpace(r.Category)
	r.Value = strings.TrimSpace(strings.ToLower(r.Value))
	if r.Name == "" || len(r.Name) > 40 || strings.ContainsAny(r.Name, "<>\"'\n\r\t") {
		return r, errBad("name must be 1-40 characters without quotes or markup")
	}
	if len(r.Category) > 30 || strings.ContainsAny(r.Category, "<>\"'\n\r\t") {
		return r, errBad("category too long or invalid")
	}
	switch r.Kind {
	case "port":
		n, err := strconv.Atoi(r.Value)
		if err != nil || n < 1 || n > 65535 {
			return r, errBad("port must be 1-65535")
		}
		if r.Proto != 0 && r.Proto != 6 && r.Proto != 17 {
			return r, errBad("protocol must be 0 (any), 6 (TCP) or 17 (UDP)")
		}
	case "ip":
		a, err := netip.ParseAddr(r.Value)
		if err != nil {
			return r, errBad("invalid IP address")
		}
		r.Value = a.Unmap().String()
	case "cidr":
		p, err := netip.ParsePrefix(r.Value)
		if err != nil {
			return r, errBad("invalid network, use e.g. 17.0.0.0/8")
		}
		r.Value = p.Masked().String()
	case "host":
		r.Value = strings.TrimPrefix(strings.TrimSuffix(r.Value, "."), "*.")
		if r.Value == "" || len(r.Value) > 253 || strings.ContainsAny(r.Value, " %_\\/\"'") {
			return r, errBad("invalid hostname (domain suffix like example.com)")
		}
	case "asn":
		r.Value = strings.TrimPrefix(r.Value, "as")
		if n, err := strconv.Atoi(r.Value); err != nil || n < 1 {
			return r, errBad("invalid AS number")
		}
	default:
		return r, errBad("kind must be port, ip, cidr, host or asn")
	}
	if r.Kind != "port" {
		r.Proto = 0
	}
	return r, nil
}

type errBad string

func (e errBad) Error() string { return string(e) }

func compile(rs []store.ServiceRule) []compiled {
	var out []compiled
	for _, r := range rs {
		c := compiled{ServiceRule: r, rank: kindRank[r.Kind]}
		switch r.Kind {
		case "port":
			n, _ := strconv.Atoi(r.Value)
			c.port = uint16(n)
			if r.Proto != 0 {
				c.spec = 1
			}
		case "asn":
			n, _ := strconv.Atoi(r.Value)
			c.asn = uint32(n)
		case "cidr":
			c.prefix, _ = netip.ParsePrefix(r.Value)
			c.spec = c.prefix.Bits()
		case "ip":
			a, _ := netip.ParseAddr(r.Value)
			c.prefix = netip.PrefixFrom(a, a.BitLen())
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.spec != b.spec {
			return a.spec < b.spec
		}
		return a.ID < b.ID
	})
	return out
}

// Set replaces the rule set.
func (c *Classifier) Set(rs []store.ServiceRule) {
	cs := compile(rs)
	cats := map[string]string{}
	for _, r := range rs {
		if r.Category != "" {
			cats[r.Name] = r.Category
		} else if cats[r.Name] == "" {
			cats[r.Name] = "Custom"
		}
	}
	c.mu.Lock()
	c.rules, c.cats = cs, cats
	c.mu.Unlock()
}

// Classify returns the service label for a flow's remote side.
func (c *Classifier) Classify(proto uint8, rport, cport uint16, rip netip.Addr, host string, asn uint32) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for i := len(c.rules) - 1; i >= 0; i-- { // strongest first
		r := &c.rules[i]
		switch r.Kind {
		case "ip", "cidr":
			if rip.IsValid() && r.prefix.Contains(rip.Unmap()) {
				return r.Name
			}
		case "host":
			if host != "" && (host == r.Value || strings.HasSuffix(host, "."+r.Value)) {
				return r.Name
			}
		case "asn":
			if asn != 0 && asn == r.asn {
				return r.Name
			}
		case "port":
			if r.port == rport && (r.Proto == 0 || r.Proto == int(proto)) && (proto == 6 || proto == 17) {
				return r.Name
			}
		}
	}
	return Service(proto, rport, cport)
}

// Category returns the group a service belongs to (custom rule category, else built-in, else "Other").
func (c *Classifier) Category(svc string) string {
	c.mu.RLock()
	cat := c.cats[svc]
	c.mu.RUnlock()
	if cat != "" {
		return cat
	}
	if cat, ok := builtinCat[svc]; ok {
		return cat
	}
	return "Other"
}

var builtinCat = func() map[string]string {
	m := map[string]string{}
	set := func(cat string, names ...string) {
		for _, n := range names {
			m[n] = cat
		}
	}
	set("Web", "HTTP", "HTTPS", "HTTP-alt", "HTTPS-alt", "QUIC")
	set("Name resolution", "DNS", "DNS-over-TLS", "mDNS", "LLMNR", "NetBIOS")
	set("Time", "NTP")
	set("Mail", "SMTP", "SMTPS", "SMTP-submission", "IMAP", "IMAPS", "POP3", "POP3S")
	set("Remote access", "SSH", "Telnet", "RDP", "VNC", "Winbox", "RouterOS-API", "RouterOS-API-SSL", "SOCKS")
	set("File sharing", "FTP", "FTP-data", "SMB", "NFS", "rsync", "TFTP")
	set("VPN", "WireGuard", "OpenVPN", "IPsec-IKE", "IPsec-NAT-T", "L2TP", "PPTP", "ESP", "GRE", "AH")
	set("Messaging / IoT", "MQTT", "MQTT-TLS", "XMPP", "CoAP", "Google-Push", "Home-Assistant")
	set("Network services", "DHCP", "DHCPv6", "SNMP", "SNMP-trap", "Syslog", "SSDP", "UPnP/Synology", "NetFlow", "ICMP", "ICMPv6", "IGMP", "RIP", "OSPF", "VRRP", "STUN", "TR-069", "WS-Discovery", "Kerberos", "LDAP", "LDAPS", "RADIUS", "RADIUS-acct", "MS-RPC")
	set("Media / Gaming", "Plex", "Steam", "Xbox-Live", "BitTorrent")
	set("Printing", "IPP", "LPD", "JetDirect")
	set("Databases", "MySQL", "PostgreSQL", "MSSQL", "Redis")
	set("Voice", "SIP", "SIP-TLS")
	set("Infrastructure", "Proxmox")
	return m
}()

// OrderRules returns the rules sorted weakest → strongest (the order in which they must be applied
// retroactively so that the strongest match wins).
func OrderRules(rs []store.ServiceRule) []store.ServiceRule {
	cs := compile(rs)
	out := make([]store.ServiceRule, len(cs))
	for i, c := range cs {
		out[i] = c.ServiceRule
	}
	return out
}
