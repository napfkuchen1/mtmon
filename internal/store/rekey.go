// SPDX-License-Identifier: AGPL-3.0-only

package store

// RekeyTraffic moves stored traffic that is keyed by `from` (a bare address, used while an address could not be
// matched to a client) to the client key `to`. Rollup rows that already exist under `to` are summed. Idempotent;
// does nothing when there is no such traffic.
func (s *Store) RekeyTraffic(from, to string) error {
	if from == "" || to == "" || from == to {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range []string{"rollup_1m", "rollup_1h"} {
		if _, err := tx.Exec(`INSERT INTO `+t+`(ts,mac,rip,rport,proto,svc,host,cc,asn,asorg,b_up,b_down,b_int,flows)
			SELECT ts,?1,rip,rport,proto,svc,host,cc,asn,asorg,b_up,b_down,b_int,flows FROM `+t+` WHERE mac=?2
			ON CONFLICT(ts,mac,rip,rport,proto) DO UPDATE SET b_up=b_up+excluded.b_up, b_down=b_down+excluded.b_down,
			b_int=b_int+excluded.b_int, flows=flows+excluded.flows`, to, from); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM `+t+` WHERE mac=?`, from); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO rollup_1h_client(ts,mac,b_up,b_down,b_int,flows)
		SELECT ts,?1,b_up,b_down,b_int,flows FROM rollup_1h_client WHERE mac=?2
		ON CONFLICT(ts,mac) DO UPDATE SET b_up=b_up+excluded.b_up, b_down=b_down+excluded.b_down,
		b_int=b_int+excluded.b_int, flows=flows+excluded.flows`, to, from); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM rollup_1h_client WHERE mac=?`, from); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE flows SET mac=? WHERE mac=?`, to, from); err != nil {
		return err
	}
	return tx.Commit()
}
