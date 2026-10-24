package store

import (
	"testing"
	"time"
)

func TestNeedsRelearn(t *testing.T) {
	for _, c := range []struct {
		prev, cur string
		want      bool
	}{
		{"v0.16.0", "v0.17.0", true}, {"0.16.0", "0.17.1", true}, {"v0.15.0", "v0.18.0", true},
		{"v0.17.0", "v0.17.1", false}, {"v0.16.0", "v0.16.1", false}, {"", "v0.17.0", false}, {"v0.17.0", "dev", false},
	} {
		if got := NeedsRelearn(c.prev, c.cur); got != c.want {
			t.Errorf("%s → %s: %v want %v", c.prev, c.cur, got, c.want)
		}
	}
}

func TestLearningPeriodAndBaselineReset(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if st.LearningUntil() != 0 {
		t.Fatal("no learning on a fresh database")
	}
	st.SetMeta("last_run_version", "v0.16.0")
	if !st.NoteStart("v0.17.0") || st.LearningUntil() == 0 {
		t.Fatal("update across 0.17.0 must start a learning period")
	}
	if st.NoteStart("v0.17.0") {
		t.Fatal("same version again")
	}
	st.SetMeta("learning_until", "0")
	st.SetMeta("last_run_version", "")
	st.DB.Exec(`INSERT INTO clients(mac,first_seen,last_seen) VALUES('AA:00:00:00:00:01',1,1)`)
	if !st.NoteStart("v0.17.0") {
		t.Fatal("an existing database from before the version was recorded must relearn")
	}
	st.AddAlert(Alert{TS: time.Now().Unix() - 3600, Kind: "wan_down", Subject: "gw", Severity: "warning", Msg: "x"})
	if as, _ := st.BaselineAlerts(10); len(as) != 1 {
		t.Fatal("history before any reset")
	}
	st.StartLearning(time.Hour, true)
	if as, _ := st.BaselineAlerts(10); len(as) != 0 {
		t.Fatalf("history must be forgotten for patterns after the reset: %d", len(as))
	}
	if as, _ := st.Alerts(10); len(as) != 1 {
		t.Fatal("the alerts themselves stay")
	}
}
