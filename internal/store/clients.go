package store

import (
	"database/sql"
	"strings"
	"time"
)

type Client struct {
	MAC       string `json:"mac"`
	Hostname  string `json:"hostname"`
	Vendor    string `json:"vendor"`
	IP        string `json:"ip"`
	Label     string `json:"label"`
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
	Device    string `json:"device"`
	Iface     string `json:"iface"`
	SSID      string `json:"ssid"`
	Band      string `json:"band"`
	Signal    int    `json:"signal"`
	TxRate    string `json:"tx_rate"`
	RxRate    string `json:"rx_rate"`
	WiFi      bool   `json:"wifi"`
	Online    bool   `json:"online"`
	Via       string `json:"via"` // why the client counts as present, e.g. "Wi-Fi registration on wAP"
}

// Observation is what the poller learned about a client in one cycle.
type Observation struct {
	MAC      string
	Hostname string
	Vendor   string
	IP       string
	Device   string
	Iface    string
	SSID     string
	Band     string
	Signal   int
	TxRate   string
	RxRate   string
	WiFi     bool
	Via      string // evidence behind Present
	Present  bool   // seen on the network right now (wifi/bridge-host/ARP), not just a DHCP lease
}

func NormMAC(m string) string { return strings.ToUpper(strings.TrimSpace(m)) }

// UpsertClients merges observations; returns roaming events detected.
func (s *Store) UpsertClients(obs []Observation) ([]string, error) {
	now := time.Now().Unix()
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var roams []string
	for _, o := range obs {
		o.MAC = NormMAC(o.MAC)
		if o.MAC == "" {
			continue
		}
		var prevDev sql.NullString
		var prevWifi sql.NullInt64
		err := tx.QueryRow(`SELECT device, wifi FROM clients WHERE mac=?`, o.MAC).Scan(&prevDev, &prevWifi)
		if err == sql.ErrNoRows {
			tx.Exec(`INSERT INTO clients(mac,hostname,vendor,ip,first_seen,last_seen,device,iface,ssid,band,signal,tx_rate,rx_rate,wifi,online,via)
				VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, o.MAC, o.Hostname, o.Vendor, o.IP, now, now, o.Device, o.Iface, o.SSID,
				o.Band, o.Signal, o.TxRate, o.RxRate, b2i(o.WiFi), b2i(o.Present), o.Via)
		} else if err == nil {
			if o.WiFi && prevWifi.Int64 == 1 && prevDev.String != "" && o.Device != "" && prevDev.String != o.Device {
				tx.Exec(`INSERT INTO roam(ts,mac,from_dev,to_dev) VALUES(?,?,?,?)`, now, o.MAC, prevDev.String, o.Device)
				roams = append(roams, o.MAC)
			}
			// Keep richer info: don't overwrite wifi data with a wired/arp observation.
			if o.WiFi {
				tx.Exec(`UPDATE clients SET last_seen=?, online=1, via=?, device=?, iface=?, ssid=?, band=?, signal=?, tx_rate=?, rx_rate=?, wifi=1,
					hostname=CASE WHEN ?<>'' THEN ? ELSE hostname END, ip=CASE WHEN ?<>'' THEN ? ELSE ip END,
					vendor=CASE WHEN ?<>'' THEN ? ELSE vendor END WHERE mac=?`,
					now, o.Via, o.Device, o.Iface, o.SSID, o.Band, o.Signal, o.TxRate, o.RxRate,
					o.Hostname, o.Hostname, o.IP, o.IP, o.Vendor, o.Vendor, o.MAC)
			} else {
				seen := int64(0)
				if o.Present {
					seen = now
				}
				tx.Exec(`UPDATE clients SET last_seen=CASE WHEN ?>0 THEN ? ELSE last_seen END, online=CASE WHEN ?>0 THEN 1 ELSE online END,
					via=CASE WHEN ?>0 THEN ? ELSE via END,
					hostname=CASE WHEN ?<>'' THEN ? ELSE hostname END, ip=CASE WHEN ?<>'' THEN ? ELSE ip END,
					vendor=CASE WHEN ?<>'' THEN ? ELSE vendor END,
					device=CASE WHEN wifi=1 THEN device ELSE CASE WHEN ?<>'' THEN ? ELSE device END END,
					iface=CASE WHEN wifi=1 THEN iface ELSE CASE WHEN ?<>'' THEN ? ELSE iface END END
					WHERE mac=?`,
					seen, seen, seen, seen, o.Via, o.Hostname, o.Hostname, o.IP, o.IP, o.Vendor, o.Vendor, o.Device, o.Device, o.Iface, o.Iface, o.MAC)
			}
		}
		if o.IP != "" {
			tx.Exec(`INSERT INTO ip_history(ip,mac,first,last) VALUES(?,?,?,?)
				ON CONFLICT(ip,mac) DO UPDATE SET last=excluded.last`, o.IP, o.MAC, now, now)
		}
	}
	return roams, tx.Commit()
}

// MarkOffline flags clients not seen for `after` seconds as offline.
func (s *Store) MarkOffline(after int64) {
	s.DB.Exec(`UPDATE clients SET online=0 WHERE online=1 AND last_seen < ?`, time.Now().Unix()-after)
}

func (s *Store) SetLabel(mac, label string) error {
	_, err := s.DB.Exec(`UPDATE clients SET label=? WHERE mac=?`, label, NormMAC(mac))
	return err
}

const clientCols = `mac,coalesce(hostname,''),coalesce(vendor,''),coalesce(ip,''),coalesce(label,''),first_seen,last_seen,
 coalesce(device,''),coalesce(iface,''),coalesce(ssid,''),coalesce(band,''),coalesce(signal,0),coalesce(tx_rate,''),coalesce(rx_rate,''),wifi,online,coalesce(via,'')`

func scanClient(r interface{ Scan(...any) error }) (Client, error) {
	var c Client
	var w, o int
	err := r.Scan(&c.MAC, &c.Hostname, &c.Vendor, &c.IP, &c.Label, &c.FirstSeen, &c.LastSeen, &c.Device, &c.Iface,
		&c.SSID, &c.Band, &c.Signal, &c.TxRate, &c.RxRate, &w, &o, &c.Via)
	c.WiFi, c.Online = w == 1, o == 1
	return c, err
}

func (s *Store) Clients() ([]Client, error) {
	rows, err := s.DB.Query(`SELECT ` + clientCols + ` FROM clients ORDER BY online DESC, last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Client{}
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Client(mac string) (*Client, error) {
	c, err := scanClient(s.DB.QueryRow(`SELECT `+clientCols+` FROM clients WHERE mac=?`, NormMAC(mac)))
	if err != nil {
		return nil, err
	}
	return &c, nil
}

type IPHist struct {
	IP    string `json:"ip"`
	First int64  `json:"first"`
	Last  int64  `json:"last"`
}

func (s *Store) IPHistory(mac string) ([]IPHist, error) {
	rows, err := s.DB.Query(`SELECT ip,first,last FROM ip_history WHERE mac=? ORDER BY last DESC LIMIT 50`, NormMAC(mac))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IPHist{}
	for rows.Next() {
		var h IPHist
		rows.Scan(&h.IP, &h.First, &h.Last)
		out = append(out, h)
	}
	return out, nil
}

type Roam struct {
	TS   int64  `json:"ts"`
	From string `json:"from"`
	To   string `json:"to"`
}

func (s *Store) Roams(mac string) ([]Roam, error) {
	rows, err := s.DB.Query(`SELECT ts,from_dev,to_dev FROM roam WHERE mac=? ORDER BY ts DESC LIMIT 50`, NormMAC(mac))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Roam{}
	for rows.Next() {
		var r Roam
		rows.Scan(&r.TS, &r.From, &r.To)
		out = append(out, r)
	}
	return out, nil
}

// MACForIPAt resolves historical IP→MAC (for late flows).
func (s *Store) MACForIPAt(ip string, ts int64) string {
	var m string
	s.DB.QueryRow(`SELECT mac FROM ip_history WHERE ip=? AND first<=? ORDER BY last DESC LIMIT 1`, ip, ts+60).Scan(&m)
	return m
}

// NewDest reports whether rip was first seen for mac within the last `within` seconds (and mac is older than a day).
func (s *Store) NewDests(mac string, within int64) ([]string, error) {
	rows, err := s.DB.Query(`SELECT rip FROM seen_dest WHERE mac=? AND first>=? ORDER BY first DESC LIMIT 50`, mac, time.Now().Unix()-within)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var r string
		rows.Scan(&r)
		out = append(out, r)
	}
	return out, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
