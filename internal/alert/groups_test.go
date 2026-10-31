// SPDX-License-Identifier: AGPL-3.0-only

package alert

import (
	"strings"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestNewClientGroupDecidesTheAlert(t *testing.T) {
	e, got := newEngine(t)
	e.Suggest = func(c store.Client) string {
		if strings.Contains(c.Hostname, "plug") {
			return "IoT"
		}
		return ""
	}
	e.St.SaveGroup(store.Group{Name: "IoT", NewAlert: false, ChattyMult: 1})
	e.St.SaveGroup(store.Group{Name: "Family", NewAlert: true, ChattyMult: 1})
	e.St.DB.Exec(`INSERT INTO clients(mac,hostname,first_seen,last_seen) VALUES('02:00:00:00:00:01','plug',1,1),('02:00:00:00:00:02','phone',1,1),('02:00:00:00:00:03','x',1,1)`)
	e.NewClient("02:00:00:00:00:01", "plug", "10.0.0.5") // suggested IoT, group says no alert
	if len(*got) != 0 {
		t.Fatalf("suggested group without new-client alert: %s", kinds(*got))
	}
	e.NewClient("02:00:00:00:00:02", "phone", "10.0.0.6") // no group, randomised MAC: alerts
	e.St.SetClientGroup([]string{"02:00:00:00:00:03"}, "IoT")
	e.NewClient("02:00:00:00:00:03", "x", "10.0.0.7") // assigned to IoT: no alert
	if kinds(*got) != "new_client/02:00:00:00:00:02" {
		t.Fatalf("got %s", kinds(*got))
	}
}

func TestSpikeOfAServersGroupClientIsInfo(t *testing.T) {
	e, got := newEngine(t)
	e.St.SaveGroup(store.Group{Name: "Servers", NewAlert: false, ChattyMult: 10, SpikeInfo: true})
	e.St.DB.Exec(`INSERT INTO clients(mac,first_seen,last_seen) VALUES('AA:00:00:00:00:99',?,?)`, time.Now().Unix(), time.Now().Unix()) // new client: a warning otherwise
	e.St.SetClientGroup([]string{"AA:00:00:00:00:99"}, "Servers")
	e.Hub.AddFlow(store.FlowRow{MAC: "AA:00:00:00:00:99", Dir: "d", Bytes: 10_000})
	e.Checks()
	e.Hub.AddFlow(store.FlowRow{MAC: "AA:00:00:00:00:99", Dir: "d", Bytes: 2 << 30})
	e.Checks()
	if len(*got) != 1 || (*got)[0].Severity != "info" || !strings.Contains((*got)[0].Msg, "group Servers") {
		t.Fatalf("spike: %+v", *got)
	}
}
