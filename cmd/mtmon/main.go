// mtmon – realtime monitoring for MikroTik routers and access points.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/napfkuchen1/mtmon/internal/alert"
	"github.com/napfkuchen1/mtmon/internal/api"
	"github.com/napfkuchen1/mtmon/internal/config"
	"github.com/napfkuchen1/mtmon/internal/enrich"
	"github.com/napfkuchen1/mtmon/internal/flow"
	"github.com/napfkuchen1/mtmon/internal/live"
	"github.com/napfkuchen1/mtmon/internal/poller"
	"github.com/napfkuchen1/mtmon/internal/secret"
	"github.com/napfkuchen1/mtmon/internal/store"
	"github.com/napfkuchen1/mtmon/internal/syslog"
	"github.com/napfkuchen1/mtmon/internal/update"
	"github.com/napfkuchen1/mtmon/web"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "run":
		err = run(args)
	case "passwd":
		err = passwd(args)
	case "fingerprint":
		err = fingerprint(args)
	case "check":
		err = check(args)
	case "init":
		err = initCfg(args)
	case "selftest":
		err = selftest(args)
	case "version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
	if errors.Is(err, errUpdateRestart) {
		os.Exit(update.ExitCode)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `mtmon <command>
  run         [-c /etc/mtmon/config.json]   start the service
  check       [-c ...]                      validate config and test router API access
  init        [-c ...] [-devices file.json] [-ui-port N] [-flow-port N]   create a default config (never overwrites)
  selftest    [-c ...]                      verify the running service (UI, /healthz, flow port)
  passwd      [-c ...] [-u admin]           set the UI password (reads MTMON_PASSWORD env or stdin)
  fingerprint <host:port>                   print TLS cert SHA-256 of a router (for pinning)
  version
`)
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cp := fs.String("c", "/etc/mtmon/config.json", "config file")
	fs.Parse(args)
	cfg, err := config.Load(*cp)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return err
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	applyStoredSettings(cfg, st, log)
	en := enrich.New(cfg.GeoCityDB, cfg.GeoASNDB, cfg.OUIFile, cfg.ReverseDNS)
	hub := live.NewHub()
	box, err := secret.Open(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("secret key: %w", err)
	}
	cls := enrich.NewClassifier()
	if rs, err := st.ServiceRules(); err == nil {
		cls.Set(rs)
	}
	pipe := live.NewPipeline(cfg, st, en, hub)
	pipe.Cls = cls
	col := &flow.Collector{Addr: cfg.FlowListen, Allow: cfg.ExporterAllowed, Handler: pipe.Handle, Dec: flow.NewDecoder()}
	al := alert.New(cfg, st, hub, log)
	pm := poller.NewManager(cfg, st, hub, en, al, log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	sys := &syslog.Server{Addr: cfg.SyslogListen, Handler: func(es []syslog.Event) { st.EnqueueFw(toFw(es)...) },
		Allow: func(ip netip.Addr) string { return cfg.SourceDevice(ip) }}
	srv := &api.Server{Cfg: cfg, St: st, Hub: hub, En: en, Col: col, Pipe: pipe, UI: web.FS(), Version: version, TLS: !cfg.NoTLS,
		Box: box, Mgr: pm, Cls: cls, Sys: sys}
	if err := srv.LoadDevices(); err != nil {
		return fmt.Errorf("devices: %w", err)
	}
	errc := make(chan error, 4)
	srv.Restart = func() { errc <- errUpdateRestart }
	srv.Upd, err = newUpdater(cfg, st, log, func() { errc <- errUpdateRestart })
	if err != nil {
		return fmt.Errorf("updater: %w", err)
	}
	h := api.New(srv)
	hs := &http.Server{Addr: cfg.Listen, Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	if !cfg.NoTLS {
		var cert tls.Certificate
		if cfg.TLSCert != "" {
			cert, err = tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		} else {
			cert, err = api.SelfSigned(cfg.DataDir)
		}
		if err != nil {
			return fmt.Errorf("tls: %w", err)
		}
		hs.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	}

	backup := filepath.Join(cfg.DataDir, "backups")
	os.MkdirAll(backup, 0o750)
	go st.Writer(ctx)
	go srv.Upd.Run(ctx)
	go st.Maintain(ctx, cfg.RawRetention, cfg.Retention, backup)
	go en.RunReverseDNS(ctx)
	go pm.Run(ctx)
	go al.Run(ctx.Done())
	go func() {
		if err := sys.Run(ctx); err != nil {
			log.Warn("syslog receiver not running (firewall log view disabled)", "addr", cfg.SyslogListen, "err", err)
		}
	}()
	go func() {
		if err := col.Run(ctx); err != nil {
			errc <- fmt.Errorf("flow collector on %s: %w", cfg.FlowListen, err)
		}
	}()
	go func() {
		var err error
		if cfg.NoTLS {
			err = hs.ListenAndServe()
		} else {
			err = hs.ListenAndServeTLS("", "")
		}
		if !errors.Is(err, http.ErrServerClosed) {
			errc <- fmt.Errorf("http: %w", err)
		}
	}()
	if cfg.AdminHash == "" {
		log.Warn("no admin password set – run `mtmon passwd` (UI login is disabled until then)")
	}
	log.Info("mtmon started", "version", version, "ui", cfg.Listen, "flows", cfg.FlowListen, "devices", len(cfg.AllDevices()))
	select {
	case <-ctx.Done():
	case err := <-errc:
		stop()
		if errors.Is(err, errUpdateRestart) {
			log.Info("update staged: exiting so systemd restarts mtmon", "code", update.ExitCode)
		} else {
			log.Error("fatal", "err", err)
		}
		shutdown(hs, st)
		return err
	}
	log.Info("shutting down")
	shutdown(hs, st)
	return nil
}

func toFw(es []syslog.Event) []store.FwEvent {
	out := make([]store.FwEvent, len(es))
	for i, e := range es {
		out[i] = store.FwEvent{TS: e.TS, Device: e.Device, Prefix: e.Prefix, Chain: e.Chain, InIf: e.InIf, OutIf: e.OutIf, Proto: e.Proto,
			Flags: e.Flags, Src: e.Src, SPort: e.SPort, Dst: e.Dst, DPort: e.DPort, NAT: e.NAT, Len: e.Len, MAC: e.MAC, CState: e.CState}
	}
	return out
}

func shutdown(hs *http.Server, st *store.Store) {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hs.Shutdown(c)
	st.Flush()
}

func passwd(args []string) error {
	fs := flag.NewFlagSet("passwd", flag.ExitOnError)
	cp := fs.String("c", "/etc/mtmon/config.json", "config file")
	user := fs.String("u", "", "admin user name (default: keep)")
	gen := fs.Bool("generate", false, "generate a random password and print it")
	fs.Parse(args)
	cfg, err := config.Load(*cp)
	if err != nil {
		return err
	}
	pw := os.Getenv("MTMON_PASSWORD")
	if *gen {
		b := make([]byte, 15)
		rand.Read(b)
		pw = base64.RawURLEncoding.EncodeToString(b)
	}
	if pw == "" {
		fmt.Fprint(os.Stderr, "New password (min 10 chars), then Enter: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		pw = strings.TrimRight(line, "\r\n")
	}
	if len(pw) < 10 {
		return errors.New("password must have at least 10 characters")
	}
	h, err := api.HashPassword(pw)
	if err != nil {
		return err
	}
	cfg.AdminHash = h
	if *user != "" {
		cfg.AdminUser = *user
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	if *gen {
		fmt.Printf("user: %s\npassword: %s\n", cfg.AdminUser, pw)
	} else {
		fmt.Println("password updated for user", cfg.AdminUser)
	}
	return nil
}

func fingerprint(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: mtmon fingerprint host:port")
	}
	fp, err := poller.Fingerprint(args[0])
	if err != nil {
		return err
	}
	fmt.Println(fp)
	return nil
}

// check validates the config and tries each router's REST API once.
func check(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	cp := fs.String("c", "/etc/mtmon/config.json", "config file")
	fs.Parse(args)
	cfg, err := config.Load(*cp)
	if err != nil {
		return err
	}
	fmt.Println("config OK:", len(cfg.Devices), "device(s)")
	bad := 0
	for _, d := range cfg.Devices {
		c := poller.NewClient(d)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		rows, err := c.Get(ctx, "/system/resource")
		cancel()
		if err != nil {
			bad++
			fmt.Printf("  FAIL %-16s %s: %v\n", d.Name, d.Addr, err)
			continue
		}
		fmt.Printf("  OK   %-16s %s  RouterOS %s  %s\n", d.Name, d.Addr, rows[0]["version"], rows[0]["board-name"])
	}
	if bad > 0 {
		return fmt.Errorf("%d device(s) unreachable", bad)
	}
	return nil
}

// selftest verifies a running instance from the outside: UI answers, /healthz is ok, flow UDP port is bound.
func selftest(args []string) error {
	fs := flag.NewFlagSet("selftest", flag.ExitOnError)
	cp := fs.String("c", "/etc/mtmon/config.json", "config file")
	fs.Parse(args)
	cfg, err := config.Load(*cp)
	if err != nil {
		return err
	}
	fail := 0
	report := func(name string, err error) {
		if err != nil {
			fail++
			fmt.Printf("  FAIL %-28s %v\n", name, err)
		} else {
			fmt.Printf("  OK   %s\n", name)
		}
	}
	host := cfg.Listen
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	scheme := "https"
	if cfg.NoTLS {
		scheme = "http"
	}
	hc := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} // local loopback probe only
	resp, err := hc.Get(scheme + "://" + host + "/healthz")
	if err == nil {
		b := make([]byte, 8)
		n, _ := resp.Body.Read(b)
		resp.Body.Close()
		if resp.StatusCode != 200 || strings.TrimSpace(string(b[:n])) != "ok" {
			err = fmt.Errorf("unexpected response %d %q", resp.StatusCode, b[:n])
		}
	}
	report("UI/API /healthz on "+cfg.Listen, err)
	fh := cfg.FlowListen
	if strings.HasPrefix(fh, ":") {
		fh = "0.0.0.0" + fh
	}
	if c, err := net.ListenPacket("udp", fh); err == nil {
		c.Close()
		report("flow collector UDP "+cfg.FlowListen, errors.New("port is NOT bound - collector not running"))
	} else {
		report("flow collector UDP "+cfg.FlowListen+" is bound", nil)
	}
	sh := cfg.SyslogListen
	if strings.HasPrefix(sh, ":") {
		sh = "0.0.0.0" + sh
	}
	if c, err := net.ListenPacket("udp", sh); err == nil {
		c.Close()
		fmt.Println("WARN  syslog receiver UDP " + cfg.SyslogListen + " is NOT bound (firewall log view disabled; the rest works)")
	} else {
		report("syslog receiver UDP "+cfg.SyslogListen+" is bound", nil)
	}
	_, err = os.Stat(filepath.Join(cfg.DataDir, "mtmon.db"))
	report("database file", err)
	if cfg.AdminHash == "" {
		report("admin password", errors.New("not set - run: mtmon passwd"))
	} else {
		report("admin password set", nil)
	}
	if fail > 0 {
		return fmt.Errorf("%d check(s) failed", fail)
	}
	return nil
}

// initCfg writes a fresh config (0600). It refuses to overwrite so installers stay idempotent.
func initCfg(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	cp := fs.String("c", "/etc/mtmon/config.json", "config file to create")
	devs := fs.String("devices", "", "JSON file with an array of devices")
	ui := fs.Int("ui-port", 8443, "UI/API port")
	fp := fs.Int("flow-port", 2055, "IPFIX/NetFlow UDP port")
	fs.Parse(args)
	if _, err := os.Stat(*cp); err == nil {
		fmt.Println("config exists, leaving untouched:", *cp)
		return nil
	}
	cfg := &config.Config{Listen: fmt.Sprintf(":%d", *ui), FlowListen: fmt.Sprintf(":%d", *fp), DataDir: "/var/lib/mtmon"}
	if *devs != "" {
		b, err := os.ReadFile(*devs)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &cfg.Devices); err != nil {
			return fmt.Errorf("devices file: %w", err)
		}
	}
	cfg.Defaults()
	if err := cfg.Validate(); err != nil {
		return err
	}
	cfg.SetPath(*cp)
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Println("config written:", *cp)
	return nil
}

// applyStoredSettings lets values changed in the UI (kept in the database because the service user usually cannot
// write /etc/mtmon/config.json) win over the config file: reverse DNS and the UI port. A port that cannot be bound
// is ignored, so a wrong value can never lock the operator out.
func applyStoredSettings(cfg *config.Config, st *store.Store, log *slog.Logger) {
	if v := st.GetMeta("reverse_dns"); v != "" {
		cfg.ReverseDNS = v == "1"
	}
	if p := st.GetMeta("listen_port"); p != "" {
		host, _, _ := net.SplitHostPort(cfg.Listen)
		cand := net.JoinHostPort(host, p)
		if cand == cfg.Listen {
			return
		}
		if l, err := net.Listen("tcp", cand); err != nil {
			log.Warn("stored UI port cannot be used, keeping the configured one", "port", p, "err", err)
		} else {
			l.Close()
			cfg.Listen = cand
		}
	}
}
