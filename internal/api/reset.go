package api

import (
	"encoding/json"
	"net/http"

	"github.com/napfkuchen1/mtmon/internal/store"
)

// resetConfirm is the word the caller must send, so a stray request or a double click cannot wipe the history.
const resetConfirm = "RESET"

// dataReset: POST /api/reset {mode: "data"|"full", keep_labels, confirm: "RESET"}.
// "data" empties all monitoring history and keeps devices and settings; "full" removes the devices registered in
// the UI as well. Devices from config.json are not touched. Session + same-origin protection come from the auth
// wrapper like every other mutating endpoint; API tokens are read-only and cannot reach this.
func (s *Server) dataReset(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode       string `json:"mode"`
		KeepLabels bool   `json:"keep_labels"`
		Confirm    string `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in); err != nil {
		jerr(w, 400, "bad request")
		return
	}
	if in.Mode != "data" && in.Mode != "full" {
		jerr(w, 400, "mode must be data or full")
		return
	}
	if in.Confirm != resetConfirm {
		jerr(w, 400, "type "+resetConfirm+" to confirm")
		return
	}
	s.provMu.Lock() // not while a router is being provisioned
	defer s.provMu.Unlock()
	res, err := s.St.ResetData(store.ResetOpts{Full: in.Mode == "full", KeepLabels: in.Mode == "data" && in.KeepLabels})
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	s.Hub.Reset()
	s.adv.mu.Lock()
	s.adv.at = s.adv.at.AddDate(-1, 0, 0) // cached suggestions are stale now
	s.adv.mu.Unlock()
	if in.Mode == "full" {
		if err := s.LoadDevices(); err != nil {
			jerr(w, 500, err.Error())
			return
		}
	}
	jsonOut(w, res)
}
