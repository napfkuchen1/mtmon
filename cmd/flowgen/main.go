// flowgen sends synthetic IPFIX/NetFlow v9 traffic for load tests and demos.
//
//	flowgen -target 127.0.0.1:2055 -rate 5000 -duration 10s -clients 50
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/daniel/mtmon/internal/flow"
)

var dests = []struct {
	ip   string
	port uint16
	p    uint8
}{
	{"1.1.1.1", 443, 6}, {"8.8.8.8", 53, 17}, {"142.250.185.78", 443, 6}, {"151.101.1.69", 443, 6}, {"31.13.72.36", 443, 6},
	{"52.84.150.39", 443, 6}, {"93.184.216.34", 80, 6}, {"185.199.108.153", 443, 6}, {"104.16.132.229", 443, 17}, {"17.253.144.10", 443, 6},
	{"91.189.91.83", 80, 6}, {"162.159.135.234", 443, 6}, {"192.0.2.55", 51820, 17}, {"198.51.100.7", 993, 6}, {"203.0.113.80", 8883, 6},
}

func main() {
	target := flag.String("target", "127.0.0.1:2055", "collector host:port")
	rate := flag.Int("rate", 1000, "flows per second")
	dur := flag.Duration("duration", 10*time.Second, "how long to send (0 = forever)")
	nclients := flag.Int("clients", 20, "number of simulated LAN clients (192.168.88.x)")
	ipfix := flag.Bool("ipfix", true, "IPFIX (false = NetFlow v9)")
	ips := flag.String("clientips", "", "comma separated client IPs to use instead of 192.168.88.x")
	src := flag.String("src", "", "local source IP to bind (must match an allowed exporter)")
	flag.Parse()

	d := net.Dialer{}
	if *src != "" {
		d.LocalAddr = &net.UDPAddr{IP: net.ParseIP(*src)}
	}
	conn, err := d.Dial("udp", *target)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer conn.Close()
	var fixed []netip.Addr
	for _, p := range strings.Split(*ips, ",") {
		if a, err := netip.ParseAddr(strings.TrimSpace(p)); err == nil {
			fixed = append(fixed, a)
		}
	}
	e := flow.NewEncoder(*ipfix)
	rng := rand.New(rand.NewSource(1))
	start := time.Now()
	sent := 0
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	lastTpl := time.Time{}
	perTick := float64(*rate) / 100
	carry := 0.0
	for range tick.C {
		now := time.Now()
		if *dur > 0 && now.Sub(start) > *dur {
			break
		}
		if now.Sub(lastTpl) > 10*time.Second {
			conn.Write(e.Template(now))
			lastTpl = now
		}
		carry += perTick
		n := int(carry)
		carry -= float64(n)
		for n > 0 {
			k := min(n, 20)
			n -= k
			recs := make([]flow.Record, 0, k)
			for i := 0; i < k; i++ {
				c := netip.AddrFrom4([4]byte{192, 168, 88, byte(10 + rng.Intn(*nclients))})
				if len(fixed) > 0 {
					c = fixed[rng.Intn(len(fixed))]
				}
				dst := dests[rng.Intn(len(dests))]
				r := netip.MustParseAddr(dst.ip)
				b := uint64(200 + rng.Intn(200000))
				if rng.Intn(2) == 0 {
					recs = append(recs, flow.Record{Src: c, Dst: r, SrcPort: uint16(32768 + rng.Intn(28000)), DstPort: dst.port, Proto: dst.p, Bytes: b / 10, Packets: b / 1400, InIf: 2, OutIf: 1})
				} else {
					recs = append(recs, flow.Record{Src: r, Dst: c, SrcPort: dst.port, DstPort: uint16(32768 + rng.Intn(28000)), Proto: dst.p, Bytes: b, Packets: b / 1400, InIf: 1, OutIf: 2})
				}
			}
			conn.Write(e.Data(now, recs))
			sent += k
		}
	}
	el := time.Since(start).Seconds()
	fmt.Printf("sent %d flows in %.1fs (%.0f flows/s)\n", sent, el, float64(sent)/el)
}
