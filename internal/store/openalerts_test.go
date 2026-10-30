package store

import "testing"

// One warning and eight notes: the status counts one problem, the notes are counted apart (#195).
func TestOpenProblemsIgnoreInfo(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.AddAlert(Alert{TS: 1, Kind: "cpu_high", Subject: "a", Severity: "warning", Msg: "x"})
	for i := 0; i < 8; i++ {
		st.AddAlert(Alert{TS: int64(2 + i), Kind: "traffic_spike", Subject: "b", Severity: "info", Msg: "y"})
	}
	if p, n := st.OpenProblems(), st.OpenInfo(); p != 1 || n != 8 {
		t.Fatalf("problems=%d info=%d, want 1 and 8", p, n)
	}
	if st.OpenAlerts() != 9 {
		t.Fatalf("all=%d", st.OpenAlerts())
	}
}
