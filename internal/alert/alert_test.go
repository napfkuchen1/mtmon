package alert

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/live"
	"github.com/napfkuchen1/mtmon/internal/store"
)

// newEngine returns an engine with a real store/hub, a collector for raised alerts and the warm-up already over.
func newEngine(t *testing.T) (*Engine, *[]store.Alert) {
	t.Helper()
	cfg := &config.Config{DataDir: t.TempDir()}
	cfg.Defaults()
	cfg.Validate()
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := New(cfg, st, live.NewHub(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.start = time.Now().Add(-10 * time.Minute)
	var mu sync.Mutex
	var got []store.Alert
	e.Notify = func(a store.Alert) { mu.Lock(); got = append(got, a); mu.Unlock() }
	return e, &got
}

func kinds(as []store.Alert) string {
	var k []string
	for _, a := range as {
		k = append(k, a.Kind+"/"+a.Subject)
	}
	return strings.Join(k, ",")
}

func TestRaiseDeduplicatesWithinCooldown(t *testing.T) {
	e, got := newEngine(t)
	if !e.Raise("k", "s", "info", "m", time.Hour) {
		t.Fatal("first alert suppressed")
	}
	if e.Raise("k", "s", "info", "m", time.Hour) {
		t.Fatal("duplicate within cooldown raised")
	}
	if !e.Raise("k", "other", "info", "m", time.Hour) || !e.Raise("other", "s", "info", "m", time.Hour) {
		t.Fatal("different subject/kind must not be deduplicated")
	}
	e.mu.Lock()
	e.last["k|s"] = time.Now().Add(-2 * time.Hour)
	e.mu.Unlock()
	if !e.Raise("k", "s", "info", "again", time.Hour) {
		t.Fatal("alert still suppressed after the cooldown")
	}
	if len(*got) != 4 {
		t.Fatalf("%d alerts notified, want 4: %s", len(*got), kinds(*got))
	}
	stored, err := e.St.Alerts(10)
	if err != nil || len(stored) != 4 {
		t.Fatalf("stored alerts: %d %v", len(stored), err)
	}
	if (*got)[0].ID == 0 {
		t.Error("notified alert has no database id")
	}
}

func TestDeviceStateTransitions(t *testing.T) {
	e, got := newEngine(t)
	e.DeviceState("gw", true, "", 5, 40) // first sighting, up: nothing
	if len(*got) != 0 {
		t.Fatalf("alert for a healthy device: %s", kinds(*got))
	}
	e.DeviceState("gw", false, "timeout", 0, 0)
	if kinds(*got) != "device_offline/gw" || (*got)[0].Severity != "critical" || !strings.Contains((*got)[0].Msg, "timeout") {
		t.Fatalf("offline: %+v", *got)
	}
	e.DeviceState("gw", false, "timeout", 0, 0) // still down: no repeat
	if len(*got) != 1 {
		t.Fatalf("repeated offline alert: %s", kinds(*got))
	}
	e.mu.Lock()
	delete(e.last, "device_offline|gw")
	e.mu.Unlock()
	e.DeviceState("gw", true, "", 5, 40)
	if kinds(*got) != "device_offline/gw,device_online/gw" || (*got)[1].Severity != "info" {
		t.Fatalf("online: %s", kinds(*got))
	}
}

func TestDeviceDownAtFirstSighting(t *testing.T) {
	e, got := newEngine(t)
	e.DeviceState("ap", false, "refused", 0, 0)
	if kinds(*got) != "device_offline/ap" {
		t.Fatalf("a device that is down when mtmon starts must alert: %s", kinds(*got))
	}
}

func TestCPUHighNeedsThreePollsInARow(t *testing.T) {
	e, got := newEngine(t)
	e.DeviceState("gw", true, "", 95, 0)
	e.DeviceState("gw", true, "", 96, 0)
	e.DeviceState("gw", true, "", 20, 0) // recovered: counter resets
	e.DeviceState("gw", true, "", 97, 0)
	e.DeviceState("gw", true, "", 98, 0)
	if len(*got) != 0 {
		t.Fatalf("alert without 3 consecutive polls: %s", kinds(*got))
	}
	e.DeviceState("gw", true, "", 99, 0)
	if kinds(*got) != "cpu_high/gw" {
		t.Fatalf("no cpu_high after 3 consecutive polls: %s", kinds(*got))
	}
}

func TestTemperature(t *testing.T) {
	e, got := newEngine(t)
	e.DeviceState("gw", true, "", 5, 79)
	if len(*got) != 0 {
		t.Fatal("alert below 80 °C")
	}
	e.DeviceState("gw", true, "", 5, 80)
	if kinds(*got) != "temp_high/gw" {
		t.Fatalf("no temp_high at 80 °C: %s", kinds(*got))
	}
}

func TestInterfaceState(t *testing.T) {
	e, got := newEngine(t)
	for _, dyn := range []string{"pppoe-out1", "l2tp-vpn", "sstp-x", "ovpn-client", "<ovpn-user>", "veth1", "lo", "wifi-cap-1", "wg-peer-7"} {
		e.InterfaceState("gw", dyn, false)
	}
	if len(*got) != 0 {
		t.Fatalf("dynamic interfaces must not alert: %s", kinds(*got))
	}
	e.InterfaceState("gw", "ether1", false)
	e.InterfaceState("gw", "ether2", true)
	if kinds(*got) != "iface_down/gw/ether1,iface_up/gw/ether2" || (*got)[0].Severity != "warning" || (*got)[1].Severity != "info" {
		t.Fatalf("static interfaces: %+v", *got)
	}
}

// Ports named by the user that merely start with "lo" (lounge, lo-bridge, lobby) are normal interfaces.
func TestInterfaceNamesStartingWithLoAreNotLoopback(t *testing.T) {
	e, got := newEngine(t)
	for _, n := range []string{"lounge", "lo-bridge", "lobby-ap", "local-lan"} {
		e.InterfaceState("gw", n, false)
	}
	if len(*got) != 4 {
		t.Fatalf("%d of 4 user-named interfaces alerted (a 'lo' prefix swallows them): %s", len(*got), kinds(*got))
	}
}

func TestNewClientWarmupAndNaming(t *testing.T) {
	e, got := newEngine(t)
	e.start = time.Now() // just started: the initial inventory import must stay silent
	e.NewClient("aa:bb:cc:00:00:01", "phone", "192.168.88.5")
	if len(*got) != 0 {
		t.Fatal("alert during warm-up")
	}
	e.start = time.Now().Add(-10 * time.Minute)
	e.NewClient("aa:bb:cc:00:00:01", "phone", "192.168.88.5")
	e.NewClient("aa:bb:cc:00:00:02", "", "192.168.88.6") // no hostname: falls back to the IP
	if len(*got) != 2 || !strings.Contains((*got)[0].Msg, "phone") || !strings.Contains((*got)[1].Msg, "(192.168.88.6)") {
		t.Fatalf("new client alerts: %+v", *got)
	}
}

func TestChecksWarmup(t *testing.T) {
	e, got := newEngine(t)
	e.start = time.Now()
	e.Hub.AddFlow(store.FlowRow{MAC: "m", Dir: "d", Bytes: 5 << 30})
	e.Checks()
	if len(*got) != 0 || e.baseline != 0 {
		t.Fatalf("checks ran during warm-up: %s baseline=%v", kinds(*got), e.baseline)
	}
}

func TestTrafficSpike(t *testing.T) {
	e, got := newEngine(t)
	e.Hub.AddFlow(store.FlowRow{MAC: "m", Dir: "d", Bytes: 10_000}) // quiet network: becomes the baseline
	e.Checks()
	if len(*got) != 0 {
		t.Fatalf("alert on quiet traffic: %s", kinds(*got))
	}
	e.Hub.AddFlow(store.FlowRow{MAC: "m", Dir: "d", Bytes: 2 << 30}) // ~570 Mbit/s over the 30 s window
	e.Checks()
	if kinds(*got) != "traffic_spike/total" {
		t.Fatalf("no spike alert: %s", kinds(*got))
	}
}

func TestSteadyHighTrafficIsNotASpike(t *testing.T) {
	e, got := newEngine(t)
	e.Hub.AddFlow(store.FlowRow{MAC: "m", Dir: "d", Bytes: 1 << 30})
	e.Checks() // 1 GiB is already the baseline
	e.Hub.AddFlow(store.FlowRow{MAC: "m", Dir: "d", Bytes: 1 << 20})
	e.Checks()
	if len(*got) != 0 {
		t.Fatalf("steady high traffic raised %s", kinds(*got))
	}
}

func insertFlow(t *testing.T, st *store.Store, mac, rip string, rport int, dir string) {
	t.Helper()
	_, err := st.DB.Exec(`INSERT INTO flows(ts,exporter,mac,cip,rip,cport,rport,proto,dir,bytes,pkts) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		time.Now().Unix(), "gw", mac, "192.168.88.5", rip, 40000, rport, 17, dir, 100, 1)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRogueDNS(t *testing.T) {
	e, got := newEngine(t)
	e.Cfg.DNSResolvers = []string{"1.1.1.1"}
	insertFlow(t, e.St, "aa:00", "1.1.1.1", 53, "u")      // allowed resolver
	insertFlow(t, e.St, "aa:01", "192.168.88.1", 53, "u") // local resolver (the router)
	insertFlow(t, e.St, "aa:02", "8.8.8.8", 53, "u")      // rogue
	insertFlow(t, e.St, "aa:03", "8.8.4.4", 443, "u")     // not DNS
	insertFlow(t, e.St, "aa:04", "9.9.9.9", 53, "d")      // download direction is not a query
	e.Checks()
	if kinds(*got) != "rogue_dns/aa:02" {
		t.Fatalf("rogue DNS detection: %s", kinds(*got))
	}
}

func TestRogueDNSDisabledWithoutResolvers(t *testing.T) {
	e, got := newEngine(t)
	insertFlow(t, e.St, "aa:02", "8.8.8.8", 53, "u")
	e.Checks()
	if len(*got) != 0 {
		t.Fatalf("rogue DNS alert although no resolvers are configured: %s", kinds(*got))
	}
}

func TestPortScan(t *testing.T) {
	e, got := newEngine(t)
	for i := 0; i < 250; i++ {
		insertFlow(t, e.St, "aa:99", "203.0.113."+itoa(i%250), 1000+i, "u")
	}
	insertFlow(t, e.St, "aa:01", "203.0.113.1", 80, "u")
	e.Checks()
	if kinds(*got) != "port_scan/aa:99" {
		t.Fatalf("port scan detection: %s", kinds(*got))
	}
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}

// ---- delivery ----

func TestDeliverWebhook(t *testing.T) {
	e, _ := newEngine(t)
	type req struct {
		ct   string
		body store.Alert
	}
	ch := make(chan req, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var a store.Alert
		json.NewDecoder(r.Body).Decode(&a)
		ch <- req{r.Header.Get("Content-Type"), a}
	}))
	defer srv.Close()
	e.Cfg.Webhook = srv.URL
	e.deliver(store.Alert{ID: 7, Kind: "device_offline", Subject: "gw", Severity: "critical", Msg: "gw unreachable"})
	r := <-ch
	if r.ct != "application/json" || r.body.ID != 7 || r.body.Kind != "device_offline" || r.body.Msg != "gw unreachable" {
		t.Fatalf("webhook payload: %+v", r)
	}
}

func TestDeliverNtfyHeaders(t *testing.T) {
	e, _ := newEngine(t)
	type req struct{ title, prio, body string }
	ch := make(chan req, 3)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ch <- req{r.Header.Get("Title"), r.Header.Get("Priority"), string(b)}
	}))
	defer srv.Close()
	e.Cfg.NtfyURL = srv.URL
	for _, sev := range []string{"critical", "warning", "info"} {
		e.deliver(store.Alert{Kind: "k", Severity: sev, Msg: "msg " + sev})
	}
	want := map[string]string{"critical": "urgent", "warning": "high", "info": ""}
	for i := 0; i < 3; i++ {
		r := <-ch
		sev := strings.TrimPrefix(r.body, "msg ")
		if r.title != "mtmon: k" || r.prio != want[sev] {
			t.Errorf("%s: title %q priority %q, want priority %q", sev, r.title, r.prio, want[sev])
		}
	}
}

func TestDeliverSurvivesFailingTargets(t *testing.T) {
	e, _ := newEngine(t)
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	e.Cfg.Webhook = dead.URL
	e.Cfg.NtfyURL = dead.URL
	e.Cfg.SMTP = &config.SMTP{Host: "127.0.0.1", Port: 1, To: "a@example.org", From: "m@example.org"}
	done := make(chan struct{})
	go func() { e.deliver(store.Alert{Kind: "k", Severity: "info", Msg: "m"}); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("deliver hangs on unreachable targets")
	}
}

// fakeSMTP accepts one message and records it.
type fakeSMTP struct {
	ln   net.Listener
	from string
	rcpt []string
	data string
	done chan struct{}
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, done: make(chan struct{})}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		defer close(f.done)
		r := bufio.NewReader(c)
		say := func(s string) { c.Write([]byte(s + "\r\n")) }
		say("220 fake ESMTP")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			up := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
				say("250 fake")
			case strings.HasPrefix(up, "MAIL FROM:"):
				f.from = strings.TrimSpace(line[len("MAIL FROM:"):])
				say("250 ok")
			case strings.HasPrefix(up, "RCPT TO:"):
				f.rcpt = append(f.rcpt, strings.TrimSpace(line[len("RCPT TO:"):]))
				say("250 ok")
			case up == "DATA":
				say("354 go")
				var b strings.Builder
				for {
					l, err := r.ReadString('\n')
					if err != nil || l == ".\r\n" {
						break
					}
					b.WriteString(l)
				}
				f.data = b.String()
				say("250 queued")
			case up == "QUIT":
				say("221 bye")
				return
			default:
				say("250 ok")
			}
		}
	}()
	return f
}

func TestDeliverSMTP(t *testing.T) {
	e, _ := newEngine(t)
	f := newFakeSMTP(t)
	host, port, _ := net.SplitHostPort(f.ln.Addr().String())
	p := 0
	for _, ch := range port {
		p = p*10 + int(ch-'0')
	}
	e.Cfg.SMTP = &config.SMTP{Host: host, Port: p, From: "mtmon@example.org", To: "a@example.org,b@example.org"}
	e.deliver(store.Alert{Kind: "device_offline", Severity: "critical", Msg: "gw unreachable"})
	select {
	case <-f.done:
	case <-time.After(10 * time.Second):
		t.Fatal("no mail received")
	}
	if f.from != "<mtmon@example.org>" || len(f.rcpt) != 2 || f.rcpt[0] != "<a@example.org>" || f.rcpt[1] != "<b@example.org>" {
		t.Errorf("envelope: from %q rcpt %q", f.from, f.rcpt)
	}
	for _, want := range []string{"Subject: mtmon: device_offline", "From: mtmon@example.org", "To: a@example.org,b@example.org", "[CRITICAL] gw unreachable"} {
		if !strings.Contains(f.data, want) {
			t.Errorf("mail lacks %q:\n%s", want, f.data)
		}
	}
}

func TestDeliverSMTPIncomplete(t *testing.T) {
	e, _ := newEngine(t)
	for _, s := range []*config.SMTP{nil, {}, {Host: "127.0.0.1"}, {To: "a@example.org"}} {
		e.Cfg.SMTP = s
		done := make(chan struct{})
		go func() { e.deliver(store.Alert{Kind: "k", Severity: "info", Msg: "m"}); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("deliver tried to send mail with incomplete SMTP config %+v", s)
		}
	}
}
