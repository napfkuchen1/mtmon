// Package enrich adds service names, domain names, geo/ASN and vendor info.
package enrich

import (
	"context"
	"encoding/csv"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

type Enricher struct {
	mu      sync.RWMutex
	names   map[netip.Addr]nameEntry // from router DNS caches and reverse DNS
	oui     map[string]string
	geo     *maxminddb.Reader
	asn     *maxminddb.Reader
	rdns    bool
	pending chan netip.Addr
	queued  map[netip.Addr]struct{}
}

type nameEntry struct {
	name string
	exp  time.Time
}

func New(geoDB, asnDB, ouiFile string, reverseDNS bool) *Enricher {
	e := &Enricher{names: map[netip.Addr]nameEntry{}, oui: map[string]string{}, rdns: reverseDNS,
		pending: make(chan netip.Addr, 512), queued: map[netip.Addr]struct{}{}}
	if geoDB != "" {
		if r, err := maxminddb.Open(geoDB); err == nil {
			e.geo = r
		}
	}
	if asnDB != "" {
		if r, err := maxminddb.Open(asnDB); err == nil {
			e.asn = r
		}
	}
	if ouiFile != "" {
		e.LoadOUI(ouiFile)
	}
	return e
}

// SetReverseDNS switches the reverse-lookup fallback on or off at runtime.
func (e *Enricher) SetReverseDNS(on bool) {
	e.mu.Lock()
	e.rdns = on
	e.mu.Unlock()
}

// ReloadGeo opens (new) country / ASN databases without a restart. Empty paths keep what is loaded.
// The previous readers are left for the garbage collector: lookups may still be using them.
func (e *Enricher) ReloadGeo(geoDB, asnDB string) error {
	var g, a *maxminddb.Reader
	var err error
	if geoDB != "" {
		if g, err = maxminddb.Open(geoDB); err != nil {
			return err
		}
	}
	if asnDB != "" {
		if a, err = maxminddb.Open(asnDB); err != nil {
			return err
		}
	}
	e.mu.Lock()
	if g != nil {
		e.geo = g
	}
	if a != nil {
		e.asn = a
	}
	e.mu.Unlock()
	return nil
}

// Status tells the UI which enrichment sources are active.
func (e *Enricher) Status() map[string]bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return map[string]bool{"geo": e.geo != nil, "asn": e.asn != nil, "oui": len(e.oui) > 0, "reverse_dns": e.rdns}
}

// LoadOUI reads IEEE oui.csv (Registry,Assignment,Organization Name,...).
func (e *Enricher) LoadOUI(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	recs, err := r.ReadAll()
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, rec := range recs {
		if len(rec) >= 3 && len(rec[1]) == 6 {
			e.oui[strings.ToUpper(rec[1])] = rec[2]
		}
	}
	return nil
}

// Vendor returns the vendor for a MAC; flags randomized (locally administered) MACs.
func (e *Enricher) Vendor(mac string) string {
	hex := strings.ToUpper(strings.NewReplacer(":", "", "-", "").Replace(mac))
	if len(hex) < 6 {
		return ""
	}
	if b := hexByte(hex[:2]); b&0x02 != 0 {
		return "Private (randomized MAC)"
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.oui[hex[:6]]
}

func hexByte(s string) byte {
	var v byte
	for _, c := range s {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= byte(c - '0')
		case c >= 'A' && c <= 'F':
			v |= byte(c-'A') + 10
		}
	}
	return v
}

// SetName stores a hostname for ip (from a router's DNS cache).
func (e *Enricher) SetName(ip netip.Addr, name string, ttl time.Duration) {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if name == "" {
		return
	}
	e.mu.Lock()
	e.names[ip] = nameEntry{name, time.Now().Add(ttl)}
	e.mu.Unlock()
}

// Name returns a known domain for ip. It schedules a reverse lookup on a miss.
func (e *Enricher) Name(ip netip.Addr) string {
	e.mu.RLock()
	n, ok := e.names[ip]
	rdns := e.rdns
	e.mu.RUnlock()
	if ok && time.Now().Before(n.exp) {
		return n.name
	}
	if rdns && !ok {
		e.mu.Lock()
		if _, q := e.queued[ip]; !q {
			select {
			case e.pending <- ip:
				e.queued[ip] = struct{}{}
			default:
			}
		}
		e.mu.Unlock()
	}
	return n.name
}

// NameCached returns a known, unexpired name for ip without ever scheduling a lookup. Request
// handlers use it so that listing pages never trigger (or wait for) network traffic.
func (e *Enricher) NameCached(ip netip.Addr) string {
	e.mu.RLock()
	n, ok := e.names[ip]
	e.mu.RUnlock()
	if ok && time.Now().Before(n.exp) {
		return n.name
	}
	return ""
}

// RunReverseDNS resolves queued addresses (rate limited) until ctx is done.
func (e *Enricher) RunReverseDNS(ctx context.Context) {
	lim := time.NewTicker(50 * time.Millisecond) // 20 lookups/s max
	defer lim.Stop()
	res := &net.Resolver{}
	for {
		select {
		case <-ctx.Done():
			return
		case ip := <-e.pending:
			<-lim.C
			c, cancel := context.WithTimeout(ctx, 2*time.Second)
			names, err := res.LookupAddr(c, ip.String())
			cancel()
			ttl := 6 * time.Hour
			name := ""
			if err == nil && len(names) > 0 {
				name = names[0]
			} else {
				ttl = 30 * time.Minute // negative cache
			}
			e.mu.Lock()
			delete(e.queued, ip)
			e.names[ip] = nameEntry{strings.TrimSuffix(strings.ToLower(name), "."), time.Now().Add(ttl)}
			e.mu.Unlock()
		}
	}
}

// Sweep removes expired names (call occasionally).
func (e *Enricher) Sweep() {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	for k, v := range e.names {
		if now.After(v.exp.Add(time.Hour)) {
			delete(e.names, k)
		}
	}
}

type Geo struct {
	CC    string
	ASN   uint32
	ASOrg string
}

// Lookup returns country/ASN for public addresses (empty when DBs are absent).
func (e *Enricher) Lookup(ip netip.Addr) Geo {
	var g Geo
	e.mu.RLock()
	geo, asn := e.geo, e.asn
	e.mu.RUnlock()
	if geo != nil {
		var rec struct {
			Country struct {
				ISO string `maxminddb:"iso_code"`
			} `maxminddb:"country"`
		}
		if err := geo.Lookup(ip).Decode(&rec); err == nil {
			g.CC = rec.Country.ISO
		}
	}
	if asn != nil {
		var rec struct {
			N uint32 `maxminddb:"autonomous_system_number"`
			O string `maxminddb:"autonomous_system_organization"`
		}
		if err := asn.Lookup(ip).Decode(&rec); err == nil {
			g.ASN, g.ASOrg = rec.N, rec.O
		}
	}
	return g
}
