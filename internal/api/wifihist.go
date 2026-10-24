package api

import "net/http"

func init() {
	routeHooks = append(routeHooks, func(s *Server, m *http.ServeMux, a func(http.HandlerFunc) http.HandlerFunc) {
		m.HandleFunc("GET /api/clients/{mac}/wifi", a(s.clientWifi))
		m.HandleFunc("GET /api/devices/{name}/wifi", a(s.deviceWifi))
	})
}

// clientWifi: GET /api/clients/{mac}/wifi?range=… – signal and rates of a Wi-Fi client over time, plus its AP/band
// changes (roaming). Empty lists for clients without Wi-Fi history.
func (s *Server) clientWifi(w http.ResponseWriter, r *http.Request) {
	mac := r.PathValue("mac")
	since, bucket, rn := parseRange(r)
	pts, err := s.St.WifiHistory(mac, since, bucket)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	ev, err := s.St.WifiEvents(mac, since)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]any{"range": rn, "points": pts, "events": ev})
}

// deviceWifi: GET /api/devices/{name}/wifi?range=… – clients per band over time for an access point (or router with
// Wi-Fi). Channel and frequency are in the device capabilities.
func (s *Server) deviceWifi(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := s.Cfg.Device(name); !ok {
		jerr(w, 404, "unknown device")
		return
	}
	since, bucket, rn := parseRange(r)
	bands, err := s.St.WifiBands(name, since, bucket)
	if err != nil {
		jerr(w, 500, err.Error())
		return
	}
	jsonOut(w, map[string]any{"range": rn, "bands": bands})
}
