package mockros

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

type obj = map[string]any

// listMenus are collection menus ("print" returns many records with .id); singles hold one record.
var listMenus = []string{"/user", "/user/group", "/ip/traffic-flow/target", "/system/logging", "/system/logging/action",
	"/ip/firewall/filter", "/ip/firewall/nat", "/ip/service", "/system/package", "/interface/wifi", "/interface/wifi/configuration",
	"/interface/bridge", "/interface/bridge/port", "/ip/address", "/ip/dhcp-server"}
var singleMenus = []string{"/ip/traffic-flow", "/interface/wifi/cap", "/interface/wifi/capsman"}

func (s *Server) initState() {
	s.lists = map[string][]obj{}
	s.singles = map[string]obj{}
	s.nextID = 1
	add := func(menu string, o obj) { s.lists[menu] = append(s.lists[menu], s.withID(o)) }
	for _, p := range []string{"routeros", "dhcp", "ppp", "security", "system", "advanced-tools"} {
		add("/system/package", obj{"name": p, "disabled": "false"})
	}
	add("/user/group", obj{"name": "full", "policy": "local,telnet,ssh,ftp,reboot,read,write,policy,test,winbox,password,web,sniff,sensitive,api,romon,rest-api"})
	add("/user", obj{"name": s.User, "group": "full"})
	add("/ip/service", obj{"name": "www-ssl", "disabled": "false", "address": ""})
	add("/interface/bridge", obj{"name": "bridge"})
	add("/interface/bridge/port", obj{"interface": "ether3", "bridge": "bridge", "hw": "true"})
	s.singles["/ip/traffic-flow"] = obj{"enabled": "false", "interfaces": "all", "cache-entries": "32k", "active-flow-timeout": "30m", "inactive-flow-timeout": "15s"}
	s.singles["/interface/wifi/cap"] = obj{"enabled": "no"}
	s.singles["/interface/wifi/capsman"] = obj{"enabled": "no"}
	if s.Role == "ap" {
		add("/system/package", obj{"name": "wifi-qcom", "disabled": "false"})
		add("/interface/wifi/configuration", obj{"name": "cfg1", "ssid": "HomeNet"})
		add("/interface/wifi", obj{"name": "wifi1", "configuration": "cfg1", "channel.band": "5ghz-ax"})
		add("/interface/wifi", obj{"name": "wifi2", "configuration": "cfg1", "channel.band": "2ghz-ax"})
	} else {
		add("/ip/dhcp-server", obj{"name": "dhcp1", "interface": "bridge"})
		add("/ip/address", obj{"address": "192.168.88.1/24", "interface": "bridge", "disabled": "false"})
		add("/ip/firewall/nat", obj{"chain": "srcnat", "action": "masquerade", "out-interface-list": "WAN", "disabled": "false"})
		add("/ip/firewall/filter", obj{"chain": "forward", "action": "fasttrack-connection", "connection-state": "established,related", "log": "false", "disabled": "false", "comment": "defconf: fasttrack"})
		add("/ip/firewall/filter", obj{"chain": "forward", "action": "accept", "connection-state": "established,related,untracked", "log": "false", "disabled": "false", "comment": "defconf: accept established"})
		add("/ip/firewall/filter", obj{"chain": "forward", "action": "drop", "connection-state": "invalid", "log": "false", "disabled": "false", "comment": "defconf: drop invalid"})
		add("/ip/firewall/filter", obj{"chain": "forward", "action": "drop", "connection-state": "new", "connection-nat-state": "!dstnat", "in-interface-list": "WAN", "log": "false", "disabled": "false", "comment": "defconf: drop all from WAN not DSTNATed"})
		add("/ip/firewall/filter", obj{"chain": "forward", "action": "passthrough", "dynamic": "true", "log": "false", "disabled": "false", "comment": "special dummy rule to show fasttrack counters"})
		add("/ip/firewall/filter", obj{"chain": "input", "action": "drop", "in-interface-list": "!LAN", "log": "false", "disabled": "false", "comment": "defconf: drop all not coming from LAN"})
	}
}

func (s *Server) withID(o obj) obj {
	o[".id"] = fmt.Sprintf("*%X", s.nextID)
	s.nextID++
	return o
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func strMap(o obj) obj {
	out := obj{}
	for k, v := range o {
		out[k] = str(v)
	}
	return out
}

// Lists returns a copy of a menu (tests inspect router state with it).
func (s *Server) Lists(menu string) []map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]string
	for _, o := range s.lists[menu] {
		m := map[string]string{}
		for k, v := range o {
			m[k] = str(v)
		}
		out = append(out, m)
	}
	return out
}

func (s *Server) Single(menu string) map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := map[string]string{}
	for k, v := range s.singles[menu] {
		m[k] = str(v)
	}
	return m
}

func isIn(list []string, p string) bool {
	for _, x := range list {
		if x == p {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if v != nil {
		json.NewEncoder(w).Encode(v)
	}
}

func rosErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, obj{"error": code, "message": msg})
}

// authz resolves the caller. Returns (canWrite, ok).
func (s *Server) authz(r *http.Request) (bool, bool) {
	u, p, ok := r.BasicAuth()
	if !ok {
		return false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if u == s.User && p == s.Pass {
		return true, true
	}
	for _, usr := range s.lists["/user"] {
		if str(usr["name"]) != u || str(usr["password"]) != p {
			continue
		}
		if addr := str(usr["address"]); addr != "" {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			_, cidr, err := net.ParseCIDR(addr)
			if err != nil || !cidr.Contains(net.ParseIP(host)) {
				return false, false
			}
		}
		write := false
		for _, g := range s.lists["/user/group"] {
			if str(g["name"]) == str(usr["group"]) && strings.Contains(str(g["policy"]), "write") {
				write = true
			}
		}
		return write, true
	}
	return false, false
}

// dynamic serves the writable menus. Returns false if the path is not one of them.
func (s *Server) dynamic(w http.ResponseWriter, r *http.Request, canWrite bool) bool {
	path := strings.TrimPrefix(r.URL.Path, "/rest")
	if path == "/export" && r.Method == "POST" {
		if !canWrite {
			rosErr(w, 403, "forbidden")
			return true
		}
		io.Copy(io.Discard, r.Body)
		s.mu.Lock()
		s.Exports++
		fail := s.FailExport
		s.mu.Unlock()
		if fail {
			rosErr(w, 500, "export failed")
			return true
		}
		writeJSON(w, 200, []obj{})
		return true
	}
	menu, id := path, ""
	if i := strings.LastIndex(path, "/"); i > 0 {
		if isIn(listMenus, path[:i]) && strings.HasPrefix(path[i+1:], "*") {
			menu, id = path[:i], path[i+1:]
		}
	}
	isSet := false
	if strings.HasSuffix(path, "/set") {
		m := strings.TrimSuffix(path, "/set")
		if isIn(listMenus, m) || isIn(singleMenus, m) {
			menu, isSet = m, true
		}
	}
	list, single := isIn(listMenus, menu), isIn(singleMenus, menu)
	if !list && !single {
		return false
	}
	if r.Method != "GET" && !canWrite {
		rosErr(w, 403, "forbidden")
		return true
	}
	// the wifi menus behave like a missing package on devices without wifi
	if strings.HasPrefix(menu, "/interface/wifi") && s.Role != "ap" && r.Method == "GET" {
		http.NotFound(w, r)
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var in obj
	if r.Method == "PUT" || r.Method == "POST" {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if len(b) > 0 {
			if err := json.Unmarshal(b, &in); err != nil {
				rosErr(w, 400, "bad json")
				return true
			}
		}
	}
	switch {
	case r.Method == "GET" && single:
		writeJSON(w, 200, strMap(s.singles[menu]))
	case r.Method == "GET" && list && id == "":
		out := []obj{}
		for _, o := range s.lists[menu] {
			c := strMap(o)
			delete(c, "password")
			out = append(out, c)
		}
		writeJSON(w, 200, out)
	case r.Method == "GET" && list:
		for _, o := range s.lists[menu] {
			if str(o[".id"]) == id {
				c := strMap(o)
				delete(c, "password")
				writeJSON(w, 200, c)
				return true
			}
		}
		rosErr(w, 404, "Not Found")
	case r.Method == "PUT" && list:
		if (menu == "/user" || menu == "/user/group" || menu == "/system/logging/action") && s.dupName(menu, str(in["name"])) {
			rosErr(w, 400, "failure: item with the same name already exists")
			return true
		}
		if menu == "/system/logging/action" && strings.ContainsAny(str(in["name"]), "-_. ") {
			rosErr(w, 400, "failure: action name can contain only letters and numbers")
			return true
		}
		if menu == "/user" && !s.hasGroup(str(in["group"])) {
			rosErr(w, 400, "failure: no such group")
			return true
		}
		if s.FailMenu == menu {
			rosErr(w, 400, "failure: injected error")
			return true
		}
		o := s.withID(obj{})
		for k, v := range in {
			o[k] = str(v)
		}
		if menu == "/ip/firewall/filter" {
			for k, v := range map[string]string{"log": "false", "disabled": "false", "log-prefix": ""} {
				if _, ok := o[k]; !ok {
					o[k] = v
				}
			}
		}
		s.lists[menu] = append(s.lists[menu], o)
		c := strMap(o)
		delete(c, "password")
		writeJSON(w, 200, c)
	case r.Method == "POST" && isSet && single:
		for k, v := range in {
			s.singles[menu][k] = str(v)
		}
		writeJSON(w, 200, []obj{})
	case r.Method == "POST" && isSet && list:
		tid := str(in[".id"])
		for _, o := range s.lists[menu] {
			if str(o[".id"]) == tid {
				if str(o["dynamic"]) == "true" {
					rosErr(w, 400, "failure: can't edit dynamic object")
					return true
				}
				for k, v := range in {
					if k != ".id" {
						o[k] = str(v)
					}
				}
				writeJSON(w, 200, []obj{})
				return true
			}
		}
		rosErr(w, 400, "failure: no such item")
	case r.Method == "DELETE" && list && id != "":
		for i, o := range s.lists[menu] {
			if str(o[".id"]) == id {
				s.lists[menu] = append(s.lists[menu][:i], s.lists[menu][i+1:]...)
				w.WriteHeader(http.StatusNoContent)
				return true
			}
		}
		rosErr(w, 404, "no such item")
	default:
		rosErr(w, 400, "unsupported")
	}
	return true
}

func (s *Server) dupName(menu, name string) bool {
	for _, o := range s.lists[menu] {
		if str(o["name"]) == name {
			return true
		}
	}
	return false
}

func (s *Server) hasGroup(name string) bool { return s.dupName("/user/group", name) }
