package api

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/napfkuchen1/mtmon/internal/store"
)

type inboxRow struct {
	store.InboxClient
	Suggest string `json:"suggest"`
	Reason  string `json:"reason"`
	Conf    string `json:"conf"`
}

// clientsInbox: GET /api/clients/inbox?mode=new|unnamed – clients to look at, with a proposed label.
func (s *Server) clientsInbox(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode != "unnamed" {
		mode = "new"
	}
	list, err := s.St.Inbox(mode, 500)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	idents := map[string]string{}
	if ns, err := s.St.Neighbors(); err == nil {
		for _, n := range ns {
			if n.Ident != "" {
				idents[store.NormMAC(n.MAC)] = n.Ident
			}
		}
	}
	out := make([]inboxRow, 0, len(list))
	for _, c := range list {
		row := inboxRow{InboxClient: c}
		if c.Label == "" {
			h := c.Hostname
			if id := idents[c.MAC]; h == "" && id != "" {
				h = id
			}
			row.Suggest, row.Reason, row.Conf = GuessLabel(h, c.Vendor, c.MAC, c.IP)
		}
		out = append(out, row)
	}
	jsonOut(w, map[string]any{"clients": out, "new": s.St.UnreviewedCount()})
}

// clientsReview: POST /api/clients/review {items:[{mac,label}]} – label (optional) + mark reviewed.
func (s *Server) clientsReview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Items []store.ReviewItem `json:"items"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&in); err != nil || len(in.Items) == 0 || len(in.Items) > 1000 {
		jerr(w, 400, "bad request")
		return
	}
	for i := range in.Items {
		if len(in.Items[i].Label) > 64 {
			jerr(w, 400, "label too long (max 64)")
			return
		}
	}
	n, err := s.St.ReviewClients(in.Items)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	for _, it := range in.Items {
		if l := strings.TrimSpace(it.Label); l != "" {
			s.Hub.SetName(store.NormMAC(it.MAC), l)
		}
	}
	jsonOut(w, map[string]int{"updated": n})
}

// searchIndex: GET /api/search/index – everything the Ctrl+K palette searches, filtered in the browser.
func (s *Server) searchIndex(w http.ResponseWriter, r *http.Request) {
	type cl struct {
		MAC    string `json:"mac"`
		Name   string `json:"name"`
		IP     string `json:"ip"`
		Vendor string `json:"vendor"`
		Online bool   `json:"online"`
	}
	type dv struct {
		Name string `json:"name"`
		Addr string `json:"addr"`
		Role string `json:"role"`
	}
	clients := []cl{}
	if list, err := s.St.Clients(); err == nil {
		for _, c := range list {
			clients = append(clients, cl{c.MAC, ClientName(c), c.IP, c.Vendor, c.Online})
		}
	}
	devices := []dv{}
	for _, d := range s.Cfg.AllDevices() {
		devices = append(devices, dv{d.Name, d.Addr, d.Role})
	}
	services := []string{}
	for _, t := range must(s.St.TopBy("services", time.Now().Add(-7*24*time.Hour).Unix(), 300, "")) {
		services = append(services, t.Key)
	}
	jsonOut(w, map[string]any{"clients": clients, "devices": devices, "services": services})
}

// exportClients: GET /api/export/clients – every client as CSV (a safety copy before a clean-up).
func (s *Server) exportClients(w http.ResponseWriter, r *http.Request) {
	list, err := s.St.Clients()
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="clients.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"mac", "label", "hostname", "vendor", "ip", "first_seen", "last_seen", "online"})
	for _, c := range list {
		cw.Write([]string{c.MAC, csvSafe(c.Label), csvSafe(c.Hostname), csvSafe(c.Vendor), c.IP,
			time.Unix(c.FirstSeen, 0).Format(time.RFC3339), time.Unix(c.LastSeen, 0).Format(time.RFC3339), strconv.FormatBool(c.Online)})
	}
	cw.Flush()
}
