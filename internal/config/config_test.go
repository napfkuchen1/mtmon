package config

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(write(t, `{"data_dir":"`+t.TempDir()+`","devices":[{"name":"gw","addr":"192.168.88.1","user":"u","pass":"p"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":8443" || c.FlowListen != ":2055" || c.SyslogListen != ":5514" {
		t.Errorf("listen defaults: %q %q %q", c.Listen, c.FlowListen, c.SyslogListen)
	}
	if c.AdminUser != "admin" || c.Retention != 30 || c.RawRetention != 3 {
		t.Errorf("admin/retention defaults: %q %d %d", c.AdminUser, c.Retention, c.RawRetention)
	}
	d := c.Devices[0]
	if d.Port != 443 || d.Scheme != "https" || d.Role != "router" {
		t.Errorf("device defaults: %+v", d)
	}
}

func TestExplicitValuesWin(t *testing.T) {
	c, err := Load(write(t, `{"listen":"127.0.0.1:9","flow_listen":":1","retention_days":90,"raw_retention_days":9,"admin_user":"root",
		"devices":[{"name":"ap","addr":"10.0.0.2","role":"ap","port":8728,"scheme":"http","user":"u","pass":"p"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:9" || c.FlowListen != ":1" || c.Retention != 90 || c.RawRetention != 9 || c.AdminUser != "root" {
		t.Errorf("explicit values overwritten: %+v", c)
	}
	if d := c.Devices[0]; d.Port != 8728 || d.Scheme != "http" || d.Role != "ap" {
		t.Errorf("explicit device values overwritten: %+v", d)
	}
}

func TestGeoFilesAutoDetected(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "geo"), 0o750)
	os.WriteFile(filepath.Join(dir, "geo", "dbip-country.mmdb"), []byte("x"), 0o600)
	c, err := Load(write(t, `{"data_dir":"`+dir+`","oui_file":"/custom/oui.csv"}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.GeoCityDB != filepath.Join(dir, "geo", "dbip-country.mmdb") {
		t.Errorf("country db not detected: %q", c.GeoCityDB)
	}
	if c.GeoASNDB != "" {
		t.Errorf("asn db invented: %q", c.GeoASNDB)
	}
	if c.OUIFile != "/custom/oui.csv" {
		t.Errorf("explicit oui_file replaced: %q", c.OUIFile)
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("missing file accepted")
	}
	if _, err := Load(write(t, `{not json`)); err == nil || !strings.Contains(err.Error(), "config") {
		t.Errorf("bad JSON: %v", err)
	}
}

func TestValidate(t *testing.T) {
	for name, body := range map[string]string{
		"bad cidr":       `{"local_nets":["10.0.0.0/33"]}`,
		"not a cidr":     `{"local_nets":["192.168.1.1"]}`,
		"no name":        `{"devices":[{"addr":"1.2.3.4"}]}`,
		"no addr":        `{"devices":[{"name":"gw"}]}`,
		"duplicate name": `{"devices":[{"name":"gw","addr":"10.0.0.1"},{"name":"gw","addr":"10.0.0.2"}]}`,
	} {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestIsLocal(t *testing.T) {
	c, err := Load(write(t, `{}`))
	if err != nil {
		t.Fatal(err)
	}
	for ip, want := range map[string]bool{
		"192.168.88.23": true, "10.1.2.3": true, "172.16.5.5": true, "172.32.0.1": false,
		"8.8.8.8": false, "fd00::1": true, "fe80::1": true, "2001:4860:4860::8888": false,
		"::ffff:192.168.1.5": true, // IPv4-mapped IPv6 must not bypass the check
		"::ffff:8.8.8.8":     false,
	} {
		if got := c.IsLocal(netip.MustParseAddr(ip)); got != want {
			t.Errorf("IsLocal(%s) = %v, want %v", ip, got, want)
		}
	}
	c2, _ := Load(write(t, `{"local_nets":["203.0.113.0/24"]}`))
	if !c2.IsLocal(netip.MustParseAddr("203.0.113.9")) || c2.IsLocal(netip.MustParseAddr("192.168.1.1")) {
		t.Error("custom local_nets not honoured / defaults not replaced")
	}
}

func TestDevicesMergeFileWins(t *testing.T) {
	c, _ := Load(write(t, `{"devices":[{"name":"gw","addr":"10.0.0.1"}]}`))
	ui := []Device{{Name: "gw", Addr: "9.9.9.9"}, {Name: "ap", Addr: "10.0.0.2", FromUI: true}}
	c.SetDynamic(ui)
	ui[1].Name = "mutated-by-caller" // SetDynamic must have copied
	all := c.AllDevices()
	if len(all) != 2 || all[0].Addr != "10.0.0.1" || all[1].Name != "ap" {
		t.Fatalf("merge: %+v", all)
	}
	if d, ok := c.Device("gw"); !ok || d.Addr != "10.0.0.1" {
		t.Errorf("file device must win a name clash: %+v %v", d, ok)
	}
	if _, ok := c.Device("nope"); ok {
		t.Error("unknown device found")
	}
	c.AllDevices()[0].Name = "x" // returned slice is a copy
	if c.Devices[0].Name != "gw" {
		t.Error("AllDevices leaks the internal slice")
	}
}

func TestSourceDeviceAndExporterAllowed(t *testing.T) {
	c, _ := Load(write(t, `{"devices":[
		{"name":"gw","addr":"192.168.88.1","flow_sources":["10.10.10.1","2001:db8::1"]},
		{"name":"byname","addr":"router.lan"}]}`))
	c.SetDynamic([]Device{{Name: "ap", Addr: "192.168.88.2"}})
	for ip, want := range map[string]string{
		"192.168.88.1": "gw", "10.10.10.1": "gw", "2001:db8::1": "gw", "::ffff:192.168.88.1": "gw",
		"192.168.88.2": "ap", "192.168.88.99": "",
	} {
		if got := c.SourceDevice(netip.MustParseAddr(ip)); got != want {
			t.Errorf("SourceDevice(%s) = %q, want %q", ip, got, want)
		}
	}
	if !c.ExporterAllowed(netip.MustParseAddr("192.168.88.2")) || c.ExporterAllowed(netip.MustParseAddr("1.1.1.1")) {
		t.Error("ExporterAllowed")
	}
	// documents current behaviour: a device configured by hostname is not a known exporter unless flow_sources lists its IP
	if c.ExporterAllowed(netip.MustParseAddr("192.168.88.77")) {
		t.Error("hostname device matched an arbitrary IP")
	}
}

func TestSaveRoundtrip(t *testing.T) {
	p := write(t, `{"data_dir":"`+t.TempDir()+`","devices":[{"name":"gw","addr":"10.0.0.1","user":"u","pass":"secret"}],"alert_webhook":"http://x/y"}`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	c.Retention = 45
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("config file mode %v, want 0600 (it holds device passwords)", st.Mode().Perm())
	}
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Error("temporary file left behind")
	}
	c2, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Retention != 45 || c2.Webhook != "http://x/y" || c2.Devices[0].Pass != "secret" || c2.Devices[0].Name != "gw" {
		t.Errorf("roundtrip lost data: %+v", c2)
	}
}

func TestSaveWithoutPathFailsCleanly(t *testing.T) {
	wd, _ := os.Getwd()
	dir := t.TempDir()
	os.Chdir(dir)
	defer os.Chdir(wd)
	c := &Config{}
	c.Defaults()
	if err := c.Save(); err == nil {
		t.Fatal("Save without a path must fail")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("Save without a path left files behind: %v", ents)
	}
	c.SetPath(filepath.Join(dir, "new.json"))
	if err := c.Save(); err != nil {
		t.Fatalf("Save after SetPath: %v", err)
	}
}
