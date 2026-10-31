// SPDX-License-Identifier: AGPL-3.0-only

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
	g("mtmon_alerts_open", "Unacknowledged warnings and critical alerts.")
	fmt.Fprintf(&b, "mtmon_alerts_open %d\n", s.St.OpenProblems())
	g("mtmon_alerts_open_info", "Unacknowledged info notes.")
	fmt.Fprintf(&b, "mtmon_alerts_open_info %d\n", s.St.OpenInfo())
	g("mtmon_alerts_open_critical", "Unacknowledged critical alerts.")
	fmt.Fprintf(&b, "mtmon_alerts_open_critical %d\n", s.St.OpenCritical())
	g("mtmon_alerts_open_by_severity", "Unacknowledged alerts by severity.")
	crit, all, info := s.St.OpenCritical(), s.St.OpenProblems(), s.St.OpenInfo()
	fmt.Fprintf(&b, "mtmon_alerts_open_by_severity{severity=\"critical\"} %d\n", crit)
	fmt.Fprintf(&b, "mtmon_alerts_open_by_severity{severity=\"warning\"} %d\n", max(all-crit, 0))
	fmt.Fprintf(&b, "mtmon_alerts_open_by_severity{severity=\"info\"} %d\n", info)
	// Internet probes of the last 5 minutes: at most the built-in targets plus MaxCustom own ones x 2 families
	var loss, rtt strings.Builder
	for _, t := range s.St.WANTargets(time.Now().Unix() - 300) {
		if t.Sent == 0 || t.Note != "" {
			continue
		}
		l := fmt.Sprintf("{target=%s}", lbl(t.Target))
		fmt.Fprintf(&loss, "mtmon_probe_loss_percent%s %g\n", l, t.LossPct)
		fmt.Fprintf(&rtt, "mtmon_probe_latency_ms%s %g\n", l, t.AvgMS)
	}
	g("mtmon_probe_loss_percent", "Packet or request loss of an internet probe target over the last 5 minutes.")
	b.WriteString(loss.String())
	g("mtmon_probe_latency_ms", "Average response time of an internet probe target over the last 5 minutes.")
	b.WriteString(rtt.String())
	if t := s.St.SpeedtestLastOK(); t.ID != 0 {
		g("mtmon_speedtest_down_bps", "Download rate of the last successful speed test.")
		fmt.Fprintf(&b, "mtmon_speedtest_down_bps %g\n", t.DownBps)
		g("mtmon_speedtest_up_bps", "Upload rate of the last successful speed test.")
		fmt.Fprintf(&b, "mtmon_speedtest_up_bps %g\n", t.UpBps)
		g("mtmon_speedtest_latency_ms", "Latency of the last successful speed test.")
		fmt.Fprintf(&b, "mtmon_speedtest_latency_ms %g\n", t.PingMS)
		g("mtmon_speedtest_timestamp_seconds", "Time of the last successful speed test.")
		fmt.Fprintf(&b, "mtmon_speedtest_timestamp_seconds %d\n", t.TS)
	}
	// Wi-Fi score per client (#191): only clients with a poor score (worst 100), so a large network stays cheap to scrape.
	g("mtmon_client_wifi_score", "Wi-Fi connection score (0-100) of a client with a poor connection over the last 24 hours.")
	now := time.Now().Unix()
	if h, err := s.St.WifiHealthSince(now-86400, now, s.pinnedClients()); err == nil {
		n := 0
		for _, c := range h {
			if !c.Poor || n >= 100 {
				continue
			}
			n++
			fmt.Fprintf(&b, "mtmon_client_wifi_score{mac=%s,name=%s} %d\n", lbl(c.MAC), lbl(c.Name), c.Score)
		}
	}
	g("mtmon_uptime_seconds", "Uptime of mtmon itself.")
	fmt.Fprintf(&b, "mtmon_uptime_seconds %d\n", int64(time.Since(s.Started).Seconds()))
	fmt.Fprintf(&b, "# HELP mtmon_build_info Version of the running mtmon.\n# TYPE mtmon_build_info gauge\nmtmon_build_info{version=%s} 1\n", lbl(s.Version))
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Write([]byte(b.String()))
}

// lbl quotes a label value as the exposition format wants it.
func lbl(v string) string {
	v = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
	return `"` + v + `"`
}
