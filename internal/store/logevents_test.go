// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"testing"
	"time"
)

func TestLogEventsStoreSearchAndDedup(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now().Unix()
	add := func(dev, topics, level, msg string, age int64) bool {
		return st.AddLogEvent(LogEvent{TS: now - age, Device: dev, Topics: topics, Level: level, Msg: msg})
	}
	if !add("gw", "system,info,account", "info", "user admin logged in", 10) || add("gw", "account,info", "info", "user admin logged in", 10) {
		t.Fatal("the same line from one device within seconds is one event")
	}
	add("gw", "system,error,critical", "critical", "router was rebooted", 5)
	add("ap", "wireless,info", "info", "AA:BB:CC:00:11:22 disconnected", 3)
	add("gw", "dhcp,info", "info", "100%_odd", 2)
	if es, _ := st.LogEvents("", "", "", now-60, 50); len(es) != 4 || es[0].Msg != "100%_odd" {
		t.Errorf("newest first: %+v", es)
	}
	if es, _ := st.LogEvents("gw", "", "error", now-60, 50); len(es) != 1 || es[0].Level != "critical" {
		t.Errorf("min level error on gw: %+v", es)
	}
	if es, _ := st.LogEvents("", "DISCONNECTED", "", now-60, 50); len(es) != 1 || es[0].Device != "ap" {
		t.Errorf("search is case-insensitive: %+v", es)
	}
	if es, _ := st.LogEvents("", "100%", "", now-60, 50); len(es) != 1 {
		t.Errorf("LIKE wildcards are escaped: %+v", es)
	}
	if es, _ := st.LogEvents("", "", "", now-4, 50); len(es) != 2 {
		t.Errorf("window: %+v", es)
	}
	st.AddLogEvent(LogEvent{TS: now - 8*86400, Device: "gw", Msg: "old"})
	st.Cleanup(3, 30)
	if es, _ := st.LogEvents("", "old", "", 0, 50); len(es) != 0 {
		t.Errorf("retention: %+v", es)
	}
}
