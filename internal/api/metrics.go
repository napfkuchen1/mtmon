package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// metrics serves Prometheus text format (version 0.0.4). It sits behind the normal auth,
// so a read-only API token works as `bearer_token` in a scrape config.
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	g := func(name, help string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
	}
	g("mtmon_device_up", "1 when the last poll of the device succeeded.")
	devs := s.Hub.Devices()
	for _, d := range devs {
		v := 0
		if d.Up {
			v = 1
		}
		fmt.Fprintf(&b, "mtmon_device_up{device=%s} %d\n", lbl(d.Name), v)
	}
	g("mtmon_device_cpu_percent", "CPU load of the device.")
	for _, d := range devs {
		fmt.Fprintf(&b, "mtmon_device_cpu_percent{device=%s} %g\n", lbl(d.Name), d.CPU)
	}
	g("mtmon_device_memory_percent", "Memory use of the device.")
	for _, d := range devs {
		fmt.Fprintf(&b, "mtmon_device_memory_percent{device=%s} %g\n", lbl(d.Name), d.MemPct)
	}
	g("mtmon_device_uptime_seconds", "Uptime of the device.")
	for _, d := range devs {
		fmt.Fprintf(&b, "mtmon_device_uptime_seconds{device=%s} %d\n", lbl(d.Name), d.Uptime)
	}
	g("mtmon_device_clients", "Clients seen on the device.")
	for _, d := range devs {
		fmt.Fprintf(&b, "mtmon_device_clients{device=%s} %d\n", lbl(d.Name), d.Clients)
	}
	g("mtmon_interface_rx_bps", "Receive rate of an interface in bits per second.")
	g("mtmon_interface_tx_bps", "Transmit rate of an interface in bits per second.")
	var rx, tx strings.Builder
	for _, d := range devs {
		for _, i := range s.Hub.Ifaces(d.Name) {
			l := fmt.Sprintf("{device=%s,interface=%s}", lbl(d.Name), lbl(i.Iface))
			fmt.Fprintf(&rx, "mtmon_interface_rx_bps%s %g\n", l, i.RxBps)
			fmt.Fprintf(&tx, "mtmon_interface_tx_bps%s %g\n", l, i.TxBps)
		}
	}
	// HELP/TYPE of both families come first, then their samples (families must not interleave).
	b.WriteString(rx.String())
	b.WriteString(tx.String())
	on, total := s.St.ClientCounts()
	g("mtmon_clients_online", "Clients currently online.")
	fmt.Fprintf(&b, "mtmon_clients_online %d\n", on)
	g("mtmon_clients_total", "Clients ever seen.")
	fmt.Fprintf(&b, "mtmon_clients_total %d\n", total)
	g("mtmon_alerts_open", "Unacknowledged alerts.")
	fmt.Fprintf(&b, "mtmon_alerts_open %d\n", s.St.OpenAlerts())
	g("mtmon_alerts_open_critical", "Unacknowledged critical alerts.")
	fmt.Fprintf(&b, "mtmon_alerts_open_critical %d\n", s.St.OpenCritical())
	g("mtmon_uptime_seconds", "Uptime of mtmon itself.")
	fmt.Fprintf(&b, "mtmon_uptime_seconds %d\n", int64(time.Since(s.Started).Seconds()))
	fmt.Fprintf(&b, "# HELP mtmon_build_info Version of the running mtmon.\n# TYPE mtmon_build_info gauge\nmtmon_build_info{version=%s} 1\n", lbl(s.Version))
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Write([]byte(b.String()))
}

// lbl quotes a label value as the exposition format wants it.
func lbl(v string) string {
	v = strings.NewReplacer(`\`, `\`, `"`, `\"`, "\n", `\n`).Replace(v)
	return `"` + v + `"`
}
