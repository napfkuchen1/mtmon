package api

import (
	"net/netip"
	"strings"

	"github.com/napfkuchen1/mtmon/internal/enrich"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// IPInfo is a human-readable description of one IP address.
type IPInfo struct {
	Name    string // client label / hostname / vendor + MAC suffix, or cached DNS name; "" when unknown
	Org     string // AS organisation (external addresses)
	Country string // ISO country code (external addresses)
	Local   bool
}

// nameSources are the lookups resolveIP needs. All of them must be cheap and non-blocking
// (in-memory caches or indexed SQLite lookups): they run inside request handlers.
type nameSources struct {
	clientByIP  func(ip string) (store.Client, bool)
	clientByMAC func(mac string) (store.Client, bool)
	deviceByIP  func(ip string) string // managed router / AP name
	dns         func(netip.Addr) string
	geo         func(netip.Addr) enrich.Geo
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

func isLocalAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() || a.IsMulticast() || cgnat.Contains(a)
}

// macSuffix returns the last two octets, e.g. "3F:A2".
func macSuffix(mac string) string {
	m := strings.ToUpper(mac)
	if len(m) >= 5 {
		return m[len(m)-5:]
	}
	return m
}

// ClientName picks the best display name for a client: label, else hostname, else vendor + short MAC
// suffix, else the full MAC.
func ClientName(c store.Client) string {
	switch {
	case strings.TrimSpace(c.Label) != "":
		return strings.TrimSpace(c.Label)
	case strings.TrimSpace(c.Hostname) != "":
		return strings.TrimSpace(c.Hostname)
	case strings.HasPrefix(c.Vendor, "Private"):
		return "Randomized MAC " + macSuffix(c.MAC)
	case c.Vendor != "":
		return c.Vendor + " " + macSuffix(c.MAC)
	}
	return c.MAC
}

// resolveIP names an address. For local addresses it uses the client table (label > hostname >
// vendor + MAC suffix) and managed device names; for external ones the cached DNS name plus the
// AS organisation and country. Unknown stays empty – the caller shows the bare IP.
func resolveIP(src nameSources, ip, mac string) IPInfo {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return IPInfo{}
	}
	if isLocalAddr(a) {
		info := IPInfo{Local: true}
		if src.deviceByIP != nil {
			if n := src.deviceByIP(a.Unmap().String()); n != "" {
				info.Name = n
				return info
			}
		}
		if src.clientByIP != nil {
			if c, ok := src.clientByIP(a.Unmap().String()); ok {
				info.Name = ClientName(c)
				return info
			}
		}
		if mac != "" && src.clientByMAC != nil {
			if c, ok := src.clientByMAC(mac); ok {
				info.Name = ClientName(c)
			}
		}
		return info
	}
	var info IPInfo
	if src.dns != nil {
		info.Name = strings.TrimSuffix(strings.ToLower(src.dns(a.Unmap())), ".")
	}
	if src.geo != nil {
		g := src.geo(a.Unmap())
		info.Org, info.Country = g.ASOrg, g.CC
	}
	return info
}

// sources builds the lookups for one request, with per-request caches.
func (s *Server) sources() nameSources {
	byIP := map[string]*store.Client{}
	if cl, err := s.St.Clients(); err == nil { // ordered online first, newest first: first match wins
		for i := range cl {
			if cl[i].IP != "" {
				if _, dup := byIP[cl[i].IP]; !dup {
					byIP[cl[i].IP] = &cl[i]
				}
			}
		}
	}
	histMiss := map[string]bool{}
	devs := map[string]string{}
	for _, d := range s.Cfg.AllDevices() {
		devs[d.Addr] = d.Name
	}
	return nameSources{
		clientByIP: func(ip string) (store.Client, bool) {
			if c, ok := byIP[ip]; ok {
				return *c, true
			}
			if histMiss[ip] {
				return store.Client{}, false
			}
			// an address a client held earlier (ip_history is indexed by ip)
			if mac := s.St.MACForIPAt(ip, 1<<40); mac != "" {
				if c, err := s.St.Client(mac); err == nil {
					byIP[ip] = c
					return *c, true
				}
			}
			histMiss[ip] = true
			return store.Client{}, false
		},
		clientByMAC: func(mac string) (store.Client, bool) {
			c, err := s.St.Client(mac)
			if err != nil {
				return store.Client{}, false
			}
			return *c, true
		},
		deviceByIP: func(ip string) string { return devs[ip] },
		dns: func(a netip.Addr) string {
			if s.En == nil {
				return ""
			}
			return s.En.NameCached(a)
		},
		geo: func(a netip.Addr) enrich.Geo {
			if s.En == nil {
				return enrich.Geo{}
			}
			return s.En.Lookup(a)
		},
	}
}

// nameTops fills name/org/country on the firewall top lists.
func (s *Server) nameTops(sum *store.FwSummary) {
	src := s.sources()
	for _, list := range [][]store.FwTop{sum.TopSrc, sum.TopDst} {
		for i := range list {
			in := resolveIP(src, list[i].Key, "")
			list[i].Name, list[i].Org, list[i].Country, list[i].Local = in.Name, in.Org, in.Country, in.Local
		}
	}
}

// nameEvents fills the *_name / *_org / *_country fields of firewall events.
func (s *Server) nameEvents(ev []store.FwEvent) {
	if len(ev) == 0 {
		return
	}
	src := s.sources()
	cache := map[string]IPInfo{}
	get := func(ip, mac string) IPInfo {
		k := ip + "|" + mac
		if v, ok := cache[k]; ok {
			return v
		}
		v := resolveIP(src, ip, mac)
		cache[k] = v
		return v
	}
	for i := range ev {
		e := &ev[i]
		a := get(e.Src, e.MAC)
		e.SrcName, e.SrcOrg, e.SrcCC = a.Name, a.Org, a.Country
		b := get(e.Dst, "")
		e.DstName, e.DstOrg, e.DstCC = b.Name, b.Org, b.Country
	}
}
