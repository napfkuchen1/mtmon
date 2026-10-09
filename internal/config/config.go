// Package config loads /etc/mtmon/config.json.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
)

type Device struct {
	Name        string   `json:"name"`
	Addr        string   `json:"addr"` // mgmt IP or host
	Role        string   `json:"role"` // router | ap | switch
	Site        string   `json:"site,omitempty"`
	User        string   `json:"user"`
	Pass        string   `json:"pass"`           // plain in file (0600) – see docs/security
	Port        int      `json:"port,omitempty"` // REST port, default 443
	Insecure    bool     `json:"insecure,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`  // sha256 hex of leaf cert (pinning)
	Scheme      string   `json:"scheme,omitempty"`       // https (default) | http (tests only)
	FlowSources []string `json:"flow_sources,omitempty"` // extra exporter source IPs (if different from addr)
	Managed     bool     `json:"-"`                      // provisioned via the UI (has a change manifest)
	FromUI      bool     `json:"-"`                      // stored in the DB (editable in the UI) vs. config file
}

type SMTP struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
	Pass string `json:"pass"`
	From string `json:"from"`
	To   string `json:"to"`
}

type Config struct {
	Listen       string   `json:"listen"`   // UI/API, default :8443
	TLSCert      string   `json:"tls_cert"` // empty => self-signed in DataDir
	TLSKey       string   `json:"tls_key"`
	NoTLS        bool     `json:"no_tls"`      // behind reverse proxy / tests
	FlowListen   string   `json:"flow_listen"` // default :2055
	DataDir      string   `json:"data_dir"`    // default /var/lib/mtmon
	LocalNets    []string `json:"local_nets"`  // default RFC1918
	Devices      []Device `json:"devices"`
	GeoCityDB    string   `json:"geo_country_db,omitempty"` // mmdb path (optional)
	GeoASNDB     string   `json:"geo_asn_db,omitempty"`
	OUIFile      string   `json:"oui_file,omitempty"`
	RawRetention int      `json:"raw_retention_days"` // default 3
	Retention    int      `json:"retention_days"`     // default 30
	ReverseDNS   bool     `json:"reverse_dns"`
	// AlertIgnore lists client MAC addresses that never raise traffic-spike or port-scan alerts
	// (backup servers, vulnerability scanners, monitoring hosts that legitimately look like one).
	AlertIgnore []string `json:"alert_ignore,omitempty"`
	// PortScanPorts / PortScanHosts: how many ports on one host, or hosts on one port, within 60 s count as a scan (default 100 / 200).
	PortScanPorts int      `json:"port_scan_ports,omitempty"`
	PortScanHosts int      `json:"port_scan_hosts,omitempty"`
	Webhook       string   `json:"alert_webhook,omitempty"`  // generic JSON POST
	NtfyURL       string   `json:"alert_ntfy_url,omitempty"` // e.g. https://ntfy.sh/topic or self-hosted; also works for Gotify-compatible text endpoints
	SMTP          *SMTP    `json:"alert_smtp,omitempty"`
	SyslogListen  string   `json:"syslog_listen,omitempty"` // default :5514 (UDP, RouterOS firewall logs)
	DNSResolvers  []string `json:"dns_resolvers,omitempty"` // allowed resolvers; empty = disable "rogue DNS" alert
	AdminHash     string   `json:"admin_hash,omitempty"`    // argon2id, set via `mtmon passwd`
	AdminUser     string   `json:"admin_user,omitempty"`
	UpdateRepo    string   `json:"update_repo,omitempty"` // GitHub owner/name for update checks (default napfkuchen1/mtmon)

	localPrefixes []netip.Prefix
	path          string
	mu            sync.RWMutex
	dyn           []Device // devices added via the UI (DB), merged with Devices
}

func Load(path string) (*Config, error) {
	c := &Config{}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	c.path = path
	c.Defaults()
	return c, c.Validate()
}

func (c *Config) Defaults() {
	if c.Listen == "" {
		c.Listen = ":8443"
	}
	if c.FlowListen == "" {
		c.FlowListen = ":2055"
	}
	if c.SyslogListen == "" {
		c.SyslogListen = ":5514"
	}
	if c.DataDir == "" {
		c.DataDir = "/var/lib/mtmon"
	}
	if c.RawRetention == 0 {
		c.RawRetention = 3
	}
	if c.Retention == 0 {
		c.Retention = 30
	}
	if c.AdminUser == "" {
		c.AdminUser = "admin"
	}
	// auto-detect databases fetched by update-geo.sh (explicit config values win)
	geo := filepath.Join(c.DataDir, "geo")
	for _, d := range []struct {
		field *string
		file  string
	}{{&c.GeoCityDB, "dbip-country.mmdb"}, {&c.GeoASNDB, "dbip-asn.mmdb"}, {&c.OUIFile, "oui.csv"}} {
		if *d.field == "" {
			if _, err := os.Stat(filepath.Join(geo, d.file)); err == nil {
				*d.field = filepath.Join(geo, d.file)
			}
		}
	}
	if len(c.LocalNets) == 0 {
		c.LocalNets = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7", "fe80::/10"}
	}
	for i := range c.Devices {
		if c.Devices[i].Port == 0 {
			c.Devices[i].Port = 443
		}
		if c.Devices[i].Scheme == "" {
			c.Devices[i].Scheme = "https"
		}
		if c.Devices[i].Role == "" {
			c.Devices[i].Role = "router"
		}
	}
}

func (c *Config) Validate() error {
	c.localPrefixes = nil
	for _, n := range c.LocalNets {
		p, err := netip.ParsePrefix(n)
		if err != nil {
			return fmt.Errorf("local_nets %q: %w", n, err)
		}
		c.localPrefixes = append(c.localPrefixes, p)
	}
	seen := map[string]bool{}
	for _, d := range c.Devices {
		if d.Name == "" || d.Addr == "" {
			return fmt.Errorf("device needs name and addr")
		}
		if seen[d.Name] {
			return fmt.Errorf("duplicate device %q", d.Name)
		}
		seen[d.Name] = true
	}
	return nil
}

// IsLocal reports whether ip belongs to a configured internal network.
func (c *Config) IsLocal(ip netip.Addr) bool {
	ip = ip.Unmap()
	for _, p := range c.localPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func (c *Config) Path() string { return c.path }

// Save writes atomically with 0600.
func (c *Config) Save() error {
	if c.path == "" {
		return errors.New("config has no file path (use SetPath)")
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, c.path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// SetDynamic replaces the set of UI-managed devices.
func (c *Config) SetDynamic(ds []Device) {
	c.mu.Lock()
	c.dyn = append([]Device(nil), ds...)
	c.mu.Unlock()
}

// AllDevices returns file-configured plus UI-managed devices (file wins on name clashes).
func (c *Config) AllDevices() []Device {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := append([]Device(nil), c.Devices...)
	have := map[string]bool{}
	for _, d := range out {
		have[d.Name] = true
	}
	for _, d := range c.dyn {
		if !have[d.Name] {
			out = append(out, d)
		}
	}
	return out
}

// Device returns one device by name.
func (c *Config) Device(name string) (Device, bool) {
	for _, d := range c.AllDevices() {
		if d.Name == name {
			return d, true
		}
	}
	return Device{}, false
}

// SourceDevice maps an exporter / syslog source IP to a device name ("" if unknown).
func (c *Config) SourceDevice(ip netip.Addr) string {
	ip = ip.Unmap()
	for _, d := range c.AllDevices() {
		if a, err := netip.ParseAddr(d.Addr); err == nil && a == ip {
			return d.Name
		}
		for _, fs := range d.FlowSources {
			if a, err := netip.ParseAddr(fs); err == nil && a == ip {
				return d.Name
			}
		}
	}
	return ""
}

// ExporterAllowed is true if ip is a known device address (config file or UI).
func (c *Config) ExporterAllowed(ip netip.Addr) bool { return c.SourceDevice(ip) != "" }

// SetPath sets the file used by Save (for freshly created configs).
func (c *Config) SetPath(p string) { c.path = p }
