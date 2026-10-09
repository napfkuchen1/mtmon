package store

import "testing"

func TestResetData(t *testing.T) {
	seed := func() *Store {
		s, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		seedClient(t, s, "AA:00:00:00:00:01", "", "192.168.1.10", 1, true)
		seedClient(t, s, "AA:00:00:00:00:02", "TV", "192.168.1.11", 1, true)
		s.DB.Exec(`INSERT INTO alerts(ts,kind,subject,severity,msg) VALUES(1,'k','s','warn','m')`)
		s.DB.Exec(`INSERT INTO dev_metrics(ts,device,up) VALUES(1,'r1',1)`)
		s.DB.Exec(`INSERT INTO fw_events(ts,device) VALUES(1,'r1')`)
		s.DB.Exec(`INSERT INTO devices(name,addr) VALUES('r1','10.0.0.1')`)
		s.DB.Exec(`INSERT INTO manifest(device,seq,ts,kind) VALUES('r1',1,1,'create')`)
		s.DB.Exec(`INSERT INTO service_rules(name,category,kind,value) VALUES('x','y','host','z')`)
		s.DB.Exec(`INSERT INTO advice_dismissed(id) VALUES('a')`)
		s.SetMeta("public_url", "https://x")
		return s
	}
	t.Run("data", func(t *testing.T) {
		s := seed()
		defer s.Close()
		s.Enqueue(FlowRow{TS: 1, MAC: "AA:00:00:00:00:01", RIP: "1.1.1.1"})
		res, err := s.ResetData(ResetOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if res.Clients != 2 || res.Alerts != 1 || res.Traffic == 0 {
			t.Fatalf("result %+v", res)
		}
		for _, tb := range []string{"clients", "ip_history", "roam", "seen_dest", "rollup_1h", "alerts", "dev_metrics", "fw_events", "advice_dismissed"} {
			if n := count(t, s, `SELECT count(*) FROM `+tb); n != 0 {
				t.Errorf("%s still has %d rows", tb, n)
			}
		}
		for tb, want := range map[string]int{"devices": 1, "manifest": 1, "service_rules": 1} {
			if n := count(t, s, `SELECT count(*) FROM `+tb); n != want {
				t.Errorf("%s: %d rows, want %d", tb, n, want)
			}
		}
		if s.GetMeta("public_url") != "https://x" {
			t.Error("settings lost")
		}
		if err := s.Flush(); err != nil || count(t, s, `SELECT count(*) FROM flows`) != 0 {
			t.Error("queued flow survived the reset")
		}
	})
	t.Run("keep labels", func(t *testing.T) {
		s := seed()
		defer s.Close()
		if _, err := s.ResetData(ResetOpts{KeepLabels: true}); err != nil {
			t.Fatal(err)
		}
		if n := count(t, s, `SELECT count(*) FROM clients WHERE label='TV' AND online=0`); n != 1 {
			t.Errorf("labelled client not kept offline: %d", n)
		}
		if n := count(t, s, `SELECT count(*) FROM clients`); n != 1 {
			t.Errorf("clients: %d, want 1", n)
		}
	})
	t.Run("full", func(t *testing.T) {
		s := seed()
		defer s.Close()
		res, err := s.ResetData(ResetOpts{Full: true})
		if err != nil {
			t.Fatal(err)
		}
		if res.Devices != 1 {
			t.Fatalf("result %+v", res)
		}
		for _, tb := range []string{"devices", "manifest", "devices_state", "fw_rules"} {
			if n := count(t, s, `SELECT count(*) FROM `+tb); n != 0 {
				t.Errorf("%s still has %d rows", tb, n)
			}
		}
		if count(t, s, `SELECT count(*) FROM service_rules`) != 1 || s.GetMeta("public_url") == "" {
			t.Error("service rules / settings must survive a full reset")
		}
	})
}
