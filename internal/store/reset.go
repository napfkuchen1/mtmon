package store

// ResetOpts selects what ResetData removes.
type ResetOpts struct {
	Full       bool // also remove the devices registered in the UI (with their state, neighbours, manifest and syslog rules)
	KeepLabels bool // keep clients that carry a user label (name), reset their live state
}

// ResetResult reports how many rows each group held before it was emptied.
type ResetResult struct {
	Traffic  int64 `json:"traffic"` // flows and all rollups
	Clients  int64 `json:"clients"`
	Metrics  int64 `json:"metrics"` // device and interface metrics
	Alerts   int64 `json:"alerts"`
	Firewall int64 `json:"firewall"`
	Devices  int64 `json:"devices"` // only with Full
}

// ResetData empties the monitoring history so mtmon starts clean. It always keeps settings (meta), API tokens,
// user-defined service rules and the registered devices; with Full the devices go too. Queued, not yet written
// rows are dropped as well, otherwise they would reappear right after the reset.
func (s *Store) ResetData(o ResetOpts) (*ResetResult, error) {
	s.mu.Lock()
	s.flowQ = nil
	s.fwQ = nil
	s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res := &ResetResult{}
	del := func(dst *int64, q string) error {
		r, err := tx.Exec(q)
		if err != nil {
			return err
		}
		if n, err := r.RowsAffected(); err == nil && dst != nil {
			*dst += n
		}
		return nil
	}
	for _, q := range []string{`DELETE FROM flows`, `DELETE FROM rollup_1m`, `DELETE FROM rollup_1h`, `DELETE FROM rollup_1h_client`, `DELETE FROM rollup_1d`, `DELETE FROM seen_dest`} {
		if err := del(&res.Traffic, q); err != nil {
			return nil, err
		}
	}
	clientQ := `DELETE FROM clients`
	histQ := `DELETE FROM ip_history`
	if o.KeepLabels {
		clientQ = `DELETE FROM clients WHERE COALESCE(label,'')=''`
		histQ = `DELETE FROM ip_history WHERE mac NOT IN (SELECT mac FROM clients)`
	}
	for _, q := range []struct {
		dst *int64
		sql string
	}{
		{&res.Clients, clientQ}, {nil, histQ}, {nil, `DELETE FROM roam`},
		{&res.Metrics, `DELETE FROM dev_metrics`}, {&res.Metrics, `DELETE FROM if_metrics`},
		{&res.Alerts, `DELETE FROM alerts`}, {nil, `DELETE FROM advice_dismissed`},
		{&res.Firewall, `DELETE FROM fw_events`}, {&res.Firewall, `DELETE FROM fw_hourly`},
	} {
		if err := del(q.dst, q.sql); err != nil {
			return nil, err
		}
	}
	if o.KeepLabels {
		// kept clients start without presence or location; the next poll fills them again
		if err := del(nil, `UPDATE clients SET online=0, device='', iface='', ssid='', band='', signal=0, tx_rate='', rx_rate='', wifi=0`); err != nil {
			return nil, err
		}
	}
	if o.Full {
		for _, q := range []string{`DELETE FROM manifest`, `DELETE FROM fw_rules`, `DELETE FROM neighbors`, `DELETE FROM devices_state`} {
			if err := del(nil, q); err != nil {
				return nil, err
			}
		}
		if err := del(&res.Devices, `DELETE FROM devices`); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// hand the freed pages back; failures here do not undo the reset
	s.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	s.DB.Exec(`VACUUM`)
	return res, nil
}
