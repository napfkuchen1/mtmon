package store

import (
	"encoding/json"
	"time"
)

type DeviceState struct {
	Name    string          `json:"name"`
	Model   string          `json:"model"`
	Version string          `json:"version"`
	Board   string          `json:"board"`
	Ident   string          `json:"identity"`
	LastOK  int64           `json:"last_ok"`
	LastErr string          `json:"last_err"`
	Info    json.RawMessage `json:"info"`
}

func (s *Store) SaveDeviceState(d DeviceState) error {
	info := string(d.Info)
	if info == "" {
		info = "{}"
	}
	_, err := s.DB.Exec(`INSERT INTO devices_state(name,model,version,board,identity,last_ok,last_err,info) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET model=excluded.model, version=excluded.version, board=excluded.board,
		identity=excluded.identity, last_ok=CASE WHEN excluded.last_ok>0 THEN excluded.last_ok ELSE last_ok END,
		last_err=excluded.last_err, info=excluded.info`,
		d.Name, d.Model, d.Version, d.Board, d.Ident, d.LastOK, d.LastErr, info)
	return err
}

func (s *Store) DeviceStates() (map[string]DeviceState, error) {
	rows, err := s.DB.Query(`SELECT name,model,version,board,identity,last_ok,last_err,info FROM devices_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]DeviceState{}
	for rows.Next() {
		var d DeviceState
		var info string
		rows.Scan(&d.Name, &d.Model, &d.Version, &d.Board, &d.Ident, &d.LastOK, &d.LastErr, &info)
		d.Info = json.RawMessage(info)
		out[d.Name] = d
	}
	return out, rows.Err()
}

type DevMetric struct {
	TS       int64   `json:"ts"`
	Device   string  `json:"device"`
	CPU      float64 `json:"cpu"`
	MemUsed  int64   `json:"mem_used"`
	MemTotal int64   `json:"mem_total"`
	Temp     float64 `json:"temp"`
	Uptime   int64   `json:"uptime"`
	Clients  int     `json:"clients"`
	Up       bool    `json:"up"`
}

func (s *Store) SaveDevMetric(m DevMetric) {
	s.DB.Exec(`INSERT INTO dev_metrics(ts,device,cpu,mem_used,mem_total,temp,uptime,clients,up) VALUES(?,?,?,?,?,?,?,?,?)`,
		m.TS, m.Device, m.CPU, m.MemUsed, m.MemTotal, m.Temp, m.Uptime, m.Clients, b2i(m.Up))
}

func (s *Store) DevMetrics(device string, since int64) ([]DevMetric, error) {
	rows, err := s.DB.Query(`SELECT ts,device,cpu,mem_used,mem_total,temp,uptime,clients,up FROM dev_metrics
		WHERE device=? AND ts>=? ORDER BY ts`, device, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DevMetric{}
	for rows.Next() {
		var m DevMetric
		var up int
		rows.Scan(&m.TS, &m.Device, &m.CPU, &m.MemUsed, &m.MemTotal, &m.Temp, &m.Uptime, &m.Clients, &up)
		m.Up = up == 1
		out = append(out, m)
	}
	return out, nil
}

type IfMetric struct {
	TS     int64   `json:"ts"`
	Device string  `json:"device"`
	Iface  string  `json:"iface"`
	RxBps  float64 `json:"rx_bps"`
	TxBps  float64 `json:"tx_bps"`
	Errs   int64   `json:"errs"`
}

func (s *Store) SaveIfMetrics(ms []IfMetric) {
	tx, err := s.DB.Begin()
	if err != nil {
		return
	}
	for _, m := range ms {
		tx.Exec(`INSERT INTO if_metrics(ts,device,iface,rx_bps,tx_bps,errs) VALUES(?,?,?,?,?,?)`, m.TS, m.Device, m.Iface, m.RxBps, m.TxBps, m.Errs)
	}
	tx.Commit()
}

func (s *Store) IfSeries(device, iface string, since int64) ([]IfMetric, error) {
	rows, err := s.DB.Query(`SELECT ts,device,iface,rx_bps,tx_bps,errs FROM if_metrics WHERE device=? AND iface=? AND ts>=? ORDER BY ts`, device, iface, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IfMetric{}
	for rows.Next() {
		var m IfMetric
		rows.Scan(&m.TS, &m.Device, &m.Iface, &m.RxBps, &m.TxBps, &m.Errs)
		out = append(out, m)
	}
	return out, nil
}

type Neighbor struct {
	Device   string `json:"device"`
	Iface    string `json:"iface"`
	MAC      string `json:"mac"`
	Ident    string `json:"ident"`
	Addr     string `json:"addr"`
	Platform string `json:"platform"`
}

func (s *Store) SaveNeighbors(device string, ns []Neighbor) {
	now := time.Now().Unix()
	tx, err := s.DB.Begin()
	if err != nil {
		return
	}
	for _, n := range ns {
		tx.Exec(`INSERT INTO neighbors(device,iface,mac,ident,addr,platform,seen) VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(device,iface,mac) DO UPDATE SET ident=excluded.ident, addr=excluded.addr, platform=excluded.platform, seen=excluded.seen`,
			device, n.Iface, NormMAC(n.MAC), n.Ident, n.Addr, n.Platform, now)
	}
	tx.Exec(`DELETE FROM neighbors WHERE device=? AND seen<?`, device, now-600)
	tx.Commit()
}

func (s *Store) Neighbors() ([]Neighbor, error) {
	rows, err := s.DB.Query(`SELECT device,iface,mac,ident,addr,platform FROM neighbors`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Neighbor{}
	for rows.Next() {
		var n Neighbor
		rows.Scan(&n.Device, &n.Iface, &n.MAC, &n.Ident, &n.Addr, &n.Platform)
		out = append(out, n)
	}
	return out, nil
}

type Alert struct {
	ID       int64  `json:"id"`
	TS       int64  `json:"ts"`
	Kind     string `json:"kind"`
	Subject  string `json:"subject"`
	Severity string `json:"severity"`
	Msg      string `json:"msg"`
	Acked    bool   `json:"acked"`
}

func (s *Store) AddAlert(a Alert) (int64, error) {
	r, err := s.DB.Exec(`INSERT INTO alerts(ts,kind,subject,severity,msg) VALUES(?,?,?,?,?)`, a.TS, a.Kind, a.Subject, a.Severity, a.Msg)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) Alerts(limit int) ([]Alert, error) {
	rows, err := s.DB.Query(`SELECT id,ts,kind,subject,severity,msg,acked FROM alerts ORDER BY ts DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		var a Alert
		var ack int
		rows.Scan(&a.ID, &a.TS, &a.Kind, &a.Subject, &a.Severity, &a.Msg, &ack)
		a.Acked = ack == 1
		out = append(out, a)
	}
	return out, nil
}

func (s *Store) AckAlert(id int64) error {
	_, err := s.DB.Exec(`UPDATE alerts SET acked=1 WHERE id=?`, id)
	return err
}

// AckAllAlerts acknowledges every open alert and returns how many it touched.
func (s *Store) AckAllAlerts() (int64, error) {
	r, err := s.DB.Exec(`UPDATE alerts SET acked=1 WHERE acked=0`)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

// ClearAlerts removes acknowledged alerts (all=true: every alert) and returns how many were removed.
func (s *Store) ClearAlerts(all bool) (int64, error) {
	q := `DELETE FROM alerts WHERE acked=1`
	if all {
		q = `DELETE FROM alerts`
	}
	r, err := s.DB.Exec(q)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

func (s *Store) OpenAlerts() int {
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM alerts WHERE acked=0`).Scan(&n)
	return n
}
