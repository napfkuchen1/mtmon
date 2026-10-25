package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/napfkuchen1/mtmon/internal/dnsbypass"
)

// bypassCache keeps the DNS-bypass detection for a few minutes: it reads the hourly rollup for a week and is shown on
// the clients list, the client page and the overview.
type bypassCache struct {
	mu   sync.Mutex
	at   time.Time
	list []dnsbypass.Client
	by   map[string]dnsbypass.Client
}

const bypassTTL = 5 * time.Minute

func (s *Server) dnsBypass() ([]dnsbypass.Client, map[string]dnsbypass.Client) {
	c := &s.bypass
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.by != nil && time.Since(c.at) < bypassTTL {
		return c.list, c.by
	}
	list, err := dnsbypass.Detect(s.St, time.Now().Add(-dnsbypass.Window).Unix())
	if err != nil {
		return c.list, c.by // keep the old answer, an empty map is fine for callers
	}
	c.list, c.at = list, time.Now()
	c.by = make(map[string]dnsbypass.Client, len(list))
	for _, x := range list {
		c.by[x.MAC] = x
	}
	return c.list, c.by
}

// dnsBypassList: GET /api/dns-bypass – clients that do not use the router's DNS (last 7 days), the alert setting.
func (s *Server) dnsBypassList(w http.ResponseWriter, r *http.Request) {
	list, _ := s.dnsBypass()
	names := map[string]string{}
	cl, _ := s.St.Clients()
	for _, c := range cl {
		names[c.MAC] = ClientName(c)
	}
	type row struct {
		dnsbypass.Client
		Name string `json:"name"`
	}
	out := make([]row, 0, len(list))
	for _, c := range list {
		n := names[c.MAC]
		if n == "" {
			n = c.MAC
		}
		out = append(out, row{c, n})
	}
	jsonOut(w, map[string]any{"clients": out, "total": len(out), "window_days": int(dnsbypass.Window.Hours() / 24),
		"alert": s.St.GetMeta(dnsbypass.MetaAlert) == "on"})
}

// dnsBypassSave: PUT /api/dns-bypass {alert} – switches the "a client started bypassing the local DNS" alert on or off.
func (s *Server) dnsBypassSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Alert bool `json:"alert"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	v := "off"
	if in.Alert {
		v = "on"
	}
	if err := s.St.SetMeta(dnsbypass.MetaAlert, v); err != nil {
		jerr(w, 500, err.Error())
		return
	}
	if in.Alert { // start from the current state: enabling the alert must not report every existing client
		s.St.SetMeta("dns_bypass_seen", "")
	}
	jsonOut(w, map[string]bool{"alert": in.Alert})
}
