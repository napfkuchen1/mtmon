// Package mockros is a tiny fake RouterOS 7 REST server for tests and demos.
package mockros

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type Client struct {
	MAC, IP, Host string
	WiFi          bool
	Signal        int
	SSID          string
}

type Server struct {
	Name, Role string
	User, Pass string
	Clients    []Client
	DNS        map[string]string // name → ip
	CPU        int
	Fail       bool // return 500 on everything (simulate outage)
	start      time.Time
	mu         sync.Mutex
	lists      map[string][]obj
	singles    map[string]obj
	nextID     int
	Exports    int    // number of /export calls (tests)
	FailExport bool   // make /export fail (tests)
	FailMenu   string // make PUT on this menu fail (tests)
	rx, tx     int64
}

func New(name, role, user, pass string, clients []Client) *Server {
	s := &Server{Name: name, Role: role, User: user, Pass: pass, Clients: clients, CPU: 7,
		DNS: map[string]string{"example.com": "93.184.216.34", "dns.google": "8.8.8.8"}, start: time.Now().Add(-26 * time.Hour)}
	s.initState()
	return s
}

func (s *Server) SetFail(f bool) { s.mu.Lock(); s.Fail = f; s.mu.Unlock() }
func (s *Server) SetCPU(c int)   { s.mu.Lock(); s.CPU = c; s.mu.Unlock() }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	j := func(v any) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(v)
		}
	}
	get := func(path string, f func() any) {
		mux.HandleFunc("/rest"+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(f())
		})
	}
	_ = j
	get("/system/identity", func() any { return obj{"name": s.Name} })
	get("/system/routerboard", func() any {
		return obj{"model": "RB5009UG+S+", "serial-number": "ABC123", "current-firmware": "7.15", "upgrade-firmware": "7.16"}
	})
	get("/system/resource", func() any {
		s.mu.Lock()
		defer s.mu.Unlock()
		return obj{"uptime": "1d2h3m4s", "cpu-load": fmt.Sprint(s.CPU), "free-memory": "700000000", "total-memory": "1024000000",
			"board-name": "RB5009UG+S+", "version": "7.15.3 (stable)"}
	})
	get("/system/health", func() any {
		return []obj{{"name": "temperature", "value": "41", "type": "C"}, {"name": "voltage", "value": "24", "type": "V"}}
	})
	get("/interface", func() any {
		s.mu.Lock()
		s.rx += 125000 // 1 Mbit/s at 1 poll per second-ish
		s.tx += 62500
		rx, tx := s.rx, s.tx
		s.mu.Unlock()
		return []obj{
			{"name": "ether1", "type": "ether", "running": "true", "disabled": "false", "rx-byte": fmt.Sprint(rx), "tx-byte": fmt.Sprint(tx)},
			{"name": "bridge", "type": "bridge", "running": "true", "disabled": "false", "rx-byte": fmt.Sprint(rx / 2), "tx-byte": fmt.Sprint(tx / 2)},
			{"name": "wifi1", "type": "wifi", "running": "true", "disabled": "false", "rx-byte": fmt.Sprint(rx / 3), "tx-byte": fmt.Sprint(tx / 3)},
		}
	})
	get("/interface/wifi/registration-table", func() any {
		if s.Role != "ap" {
			return nil // handled below as 404
		}
		var out []obj
		for _, c := range s.Clients {
			if c.WiFi {
				out = append(out, obj{"mac-address": c.MAC, "interface": "wifi1", "ssid": c.SSID, "signal": fmt.Sprint(c.Signal),
					"tx-rate": "866.7Mbps-80MHz/2S/SGI", "rx-rate": "780Mbps-80MHz/2S", "band": "5ghz-ax"})
			}
		}
		return out
	})
	get("/ip/dhcp-server/lease", func() any {
		var out []obj
		for _, c := range s.Clients {
			out = append(out, obj{"address": c.IP, "mac-address": c.MAC, "host-name": c.Host, "status": "bound", "active-address": c.IP, "active-mac-address": c.MAC})
		}
		return out
	})
	get("/ip/arp", func() any {
		var out []obj
		for _, c := range s.Clients {
			out = append(out, obj{"address": c.IP, "mac-address": c.MAC, "interface": "bridge", "status": "reachable"})
		}
		return out
	})
	get("/interface/bridge/host", func() any {
		var out []obj
		for _, c := range s.Clients {
			if !c.WiFi {
				out = append(out, obj{"mac-address": c.MAC, "on-interface": "ether3", "local": "false"})
			}
		}
		return out
	})
	get("/ip/dns/cache", func() any {
		var out []obj
		for n, ip := range s.DNS {
			out = append(out, obj{"name": n, "type": "A", "data": ip, "ttl": "1h"})
		}
		return out
	})
	get("/ip/neighbor", func() any {
		return []obj{{"interface": "ether2", "mac-address": "AA:BB:CC:00:00:01", "identity": "ap-living", "address": "192.168.88.2", "platform": "MikroTik"}}
	})
	// unknown path → 404
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	// wrap: auth, failure injection, wifi 404 on non-AP
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		fail := s.Fail
		s.mu.Unlock()
		if fail {
			http.Error(w, `{"error":500}`, 500)
			return
		}
		canWrite, ok := s.authz(r)
		if !ok {
			http.Error(w, `{"error":401}`, http.StatusUnauthorized)
			return
		}
		if s.dynamic(w, r, canWrite) {
			return
		}
		if s.Role != "ap" && r.URL.Path == "/rest/interface/wifi/registration-table" {
			http.NotFound(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
}
