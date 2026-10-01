package store

import (
	"encoding/json"
	"time"
)

// ---- UI-managed devices ----

type DeviceRow struct {
	Name, Addr, Scheme, Role, Site, User string
	Port                                 int
	PassEnc                              []byte
	Fingerprint, FlowSrc                 string
	Insecure, Managed                    bool
	Caps                                 json.RawMessage
	Created                              int64
}

func (s *Store) UpsertDevice(d DeviceRow) error {
	if len(d.Caps) == 0 {
		d.Caps = json.RawMessage("{}")
	}
	if d.Created == 0 {
		d.Created = time.Now().Unix()
	}
	_, err := s.DB.Exec(`INSERT INTO devices(name,addr,port,scheme,role,site,user,pass_enc,fingerprint,insecure,flow_src,caps,managed,created)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET addr=excluded.addr, port=excluded.port, scheme=excluded.scheme, role=excluded.role,
		site=excluded.site, user=excluded.user, pass_enc=excluded.pass_enc, fingerprint=excluded.fingerprint,
		insecure=excluded.insecure, flow_src=excluded.flow_src, caps=excluded.caps, managed=excluded.managed`,
		d.Name, d.Addr, d.Port, d.Scheme, d.Role, d.Site, d.User, d.PassEnc, d.Fingerprint, b2i(d.Insecure), d.FlowSrc,
		string(d.Caps), b2i(d.Managed), d.Created)
	return err
}

func (s *Store) DeviceRows() ([]DeviceRow, error) {
	rows, err := s.DB.Query(`SELECT name,addr,port,scheme,role,coalesce(site,''),user,pass_enc,coalesce(fingerprint,''),insecure,coalesce(flow_src,''),coalesce(caps,'{}'),managed,created FROM devices ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeviceRow
	for rows.Next() {
		var d DeviceRow
		var ins, man int
		var caps string
		if err := rows.Scan(&d.Name, &d.Addr, &d.Port, &d.Scheme, &d.Role, &d.Site, &d.User, &d.PassEnc, &d.Fingerprint, &ins, &d.FlowSrc, &caps, &man, &d.Created); err != nil {
			return nil, err
		}
		d.Insecure, d.Managed, d.Caps = ins == 1, man == 1, json.RawMessage(caps)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) DeviceRow(name string) (*DeviceRow, error) {
	all, err := s.DeviceRows()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Name == name {
			return &all[i], nil
		}
	}
	return nil, nil
}

func (s *Store) SetDeviceCaps(name string, caps json.RawMessage) {
	s.DB.Exec(`UPDATE devices SET caps=? WHERE name=?`, string(caps), name)
}

// DeleteDevice removes the device registration (history is kept unless purge).
func (s *Store) DeleteDevice(name string, purge bool) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	tx.Exec(`DELETE FROM devices WHERE name=?`, name)
	tx.Exec(`DELETE FROM devices_state WHERE name=?`, name)
	tx.Exec(`DELETE FROM neighbors WHERE device=?`, name)
	tx.Exec(`DELETE FROM manifest WHERE device=?`, name)
	tx.Exec(`DELETE FROM fw_rules WHERE device=?`, name)
	if purge {
		tx.Exec(`DELETE FROM dev_metrics WHERE device=?`, name)
		tx.Exec(`DELETE FROM if_metrics WHERE device=?`, name)
		tx.Exec(`DELETE FROM fw_events WHERE device=?`, name)
	}
	return tx.Commit()
}

// ---- change manifest ----

type ManifestEntry struct {
	ID    int64  `json:"id"`
	Seq   int    `json:"seq"`
	TS    int64  `json:"ts"`
	Kind  string `json:"kind"` // create | modify
	Descr string `json:"descr"`
	Path  string `json:"path"`
	RID   string `json:"rid"`
	Undo  string `json:"undo"`  // JSON: {method,path,body}
	State string `json:"state"` // applied | reverted | revert-failed
}

func (s *Store) AddManifest(device string, e ManifestEntry) error {
	_, err := s.DB.Exec(`INSERT INTO manifest(device,seq,ts,kind,descr,path,rid,undo,state) VALUES(?,?,?,?,?,?,?,?,?)`,
		device, e.Seq, time.Now().Unix(), e.Kind, e.Descr, e.Path, e.RID, e.Undo, "applied")
	return err
}

func (s *Store) Manifest(device string) ([]ManifestEntry, error) {
	rows, err := s.DB.Query(`SELECT id,seq,ts,kind,descr,path,rid,undo,state FROM manifest WHERE device=? ORDER BY seq`, device)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ManifestEntry{}
	for rows.Next() {
		var e ManifestEntry
		rows.Scan(&e.ID, &e.Seq, &e.TS, &e.Kind, &e.Descr, &e.Path, &e.RID, &e.Undo, &e.State)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) SetManifestState(id int64, st string) {
	s.DB.Exec(`UPDATE manifest SET state=? WHERE id=?`, st, id)
}

// ---- service rules ----

type ServiceRule struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Kind     string `json:"kind"` // port | ip | cidr | host | asn
	Value    string `json:"value"`
	Proto    int    `json:"proto"` // 0 = any (port kind only)
	Created  int64  `json:"created"`
}

func (s *Store) ServiceRules() ([]ServiceRule, error) {
	rows, err := s.DB.Query(`SELECT id,name,coalesce(category,''),kind,value,proto,created FROM service_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServiceRule{}
	for rows.Next() {
		var r ServiceRule
		rows.Scan(&r.ID, &r.Name, &r.Category, &r.Kind, &r.Value, &r.Proto, &r.Created)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) AddServiceRule(r ServiceRule) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO service_rules(name,category,kind,value,proto,created) VALUES(?,?,?,?,?,?)`,
		r.Name, r.Category, r.Kind, r.Value, r.Proto, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) DeleteServiceRule(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM service_rules WHERE id=?`, id)
	return err
}
