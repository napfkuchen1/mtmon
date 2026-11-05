// SPDX-License-Identifier: AGPL-3.0-only

// mockros runs fake RouterOS REST devices for demos: mockros -router :9001 -ap :9002
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/napfkuchen1/mtmon/internal/api"
	"github.com/napfkuchen1/mtmon/internal/mockros"
)

func main() {
	rt := flag.String("router", "127.0.0.1:9001", "router listen addr")
	ap := flag.String("ap", "127.0.0.1:9002", "ap listen addr")
	tlsDir := flag.String("tls-dir", "", "serve HTTPS with a self-signed cert stored here (like a real router) instead of plain HTTP")
	demo := flag.Bool("demo", false, "richer demo network instead of -router/-ap: router, switch, two APs, a spare router for the setup wizard, ~25 clients (mockros.DemoDevices)")
	flag.Parse()
	cl := DemoClients()
	serveTLS := func(addr string, h http.Handler, dir string) error {
		cert, err := api.SelfSigned(dir)
		if err != nil {
			return err
		}
		if strings.HasPrefix(filepath.Base(dir), "mockros-tls") {
			os.RemoveAll(dir) // throw-away dir of the demo; the certificate is in memory now
		}
		srv := &http.Server{Addr: addr, Handler: h, TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}}}
		return srv.ListenAndServeTLS("", "")
	}
	serve := func(addr string, h http.Handler) error {
		if *tlsDir == "" {
			return http.ListenAndServe(addr, h)
		}
		return serveTLS(addr, h, *tlsDir)
	}
	if *demo {
		devs := mockros.DemoDevices()
		dc := mockros.DemoClients()
		for _, d := range devs[:len(devs)-1] {
			s := mockros.New(d.Name, d.Role, "admin", "adminpw", dc)
			s.Neighbors = mockros.DemoNeighbors(d.Name)
			demoHardware(s, d.Name)
			go func(addr string, h http.Handler) { log.Fatal(serve(addr, h)) }(d.Listen, s.Handler())
		}
		// the spare router speaks HTTPS like a real one: the setup wizard only talks TLS
		last := devs[len(devs)-1]
		dir, err := os.MkdirTemp("", "mockros-tls")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("mockros demo network:", len(devs), "devices (admin / adminpw); the spare router", last.Listen, "uses HTTPS")
		log.Println(serveTLS(last.Listen, mockros.New(last.Name, last.Role, "admin", "adminpw", nil).Handler(), dir))
		return
	}
	// admin / adminpw can provision; the demo is a fresh router without a monitoring user
	go func() { log.Fatal(serve(*rt, mockros.New("gw-main", "router", "admin", "adminpw", cl).Handler())) }()
	fmt.Println("mockros router on", *rt, "ap on", *ap, "(admin / adminpw; https if -tls-dir)")
	log.Fatal(serve(*ap, mockros.New("ap-living", "ap", "admin", "adminpw", cl).Handler()))
}

// demoHardware gives the demo devices SFP, PoE and LTE data: the switch has a PoE port without power figures (a
// small model), one with, and an SFP module; the router has an LTE backup link.
func demoHardware(s *mockros.Server, name string) {
	switch name {
	case "sw-office":
		s.Poe = []mockros.PoePort{{Name: "ether2", Status: "powered-on", Pair: "b"}, {Name: "ether3", Status: "powered-on", Pair: "a", Watts: "6.4"}}
		s.Sfp = []mockros.SfpPort{{Name: "sfp1", Present: true, Rx: "-8.2", Tx: "-2.1", Temp: "41"}}
	case "gw-main":
		s.Lte = []mockros.LteIface{{Name: "lte1", Tech: "lte", Status: "connected", RSRP: "-96", RSRQ: "-11", SINR: "9", RSSI: "-68"}}
	}
}

func DemoClients() []mockros.Client {
	return []mockros.Client{
		{MAC: "3C:22:FB:10:00:01", IP: "192.168.88.23", Host: "alex-iphone", WiFi: true, Signal: -52, SSID: "Home"},
		{MAC: "DC:A6:32:10:00:02", IP: "192.168.88.30", Host: "homeassistant", WiFi: false},
		{MAC: "02:AA:BB:10:00:03", IP: "192.168.88.41", Host: "galaxy-s24", WiFi: true, Signal: -67, SSID: "Home"},
		{MAC: "B8:27:EB:10:00:04", IP: "192.168.88.50", Host: "printer", WiFi: false},
	}
}
