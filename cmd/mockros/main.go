// mockros runs fake RouterOS REST devices for demos: mockros -router :9001 -ap :9002
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/napfkuchen1/mtmon/internal/api"
	"github.com/napfkuchen1/mtmon/internal/mockros"
)

func main() {
	rt := flag.String("router", "127.0.0.1:9001", "router listen addr")
	ap := flag.String("ap", "127.0.0.1:9002", "ap listen addr")
	tlsDir := flag.String("tls-dir", "", "serve HTTPS with a self-signed cert stored here (like a real router) instead of plain HTTP")
	flag.Parse()
	cl := DemoClients()
	serve := func(addr string, h http.Handler) error {
		if *tlsDir == "" {
			return http.ListenAndServe(addr, h)
		}
		cert, err := api.SelfSigned(*tlsDir)
		if err != nil {
			return err
		}
		srv := &http.Server{Addr: addr, Handler: h, TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}}}
		return srv.ListenAndServeTLS("", "")
	}
	// admin / adminpw can provision; the demo is a fresh router without a monitoring user
	go func() { log.Fatal(serve(*rt, mockros.New("gw-main", "router", "admin", "adminpw", cl).Handler())) }()
	fmt.Println("mockros router on", *rt, "ap on", *ap, "(admin / adminpw; https if -tls-dir)")
	log.Fatal(serve(*ap, mockros.New("ap-living", "ap", "admin", "adminpw", cl).Handler()))
}

func DemoClients() []mockros.Client {
	return []mockros.Client{
		{MAC: "3C:22:FB:10:00:01", IP: "192.168.88.23", Host: "alex-iphone", WiFi: true, Signal: -52, SSID: "Home"},
		{MAC: "DC:A6:32:10:00:02", IP: "192.168.88.30", Host: "homeassistant", WiFi: false},
		{MAC: "02:AA:BB:10:00:03", IP: "192.168.88.41", Host: "galaxy-s24", WiFi: true, Signal: -67, SSID: "Home"},
		{MAC: "B8:27:EB:10:00:04", IP: "192.168.88.50", Host: "printer", WiFi: false},
	}
}
