// SPDX-License-Identifier: AGPL-3.0-only

package syslog

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

// Firewall lines and other log lines arrive on the same UDP port and go to their own handlers.
func TestServerRoutesFirewallAndLogLines(t *testing.T) {
	var mu sync.Mutex
	var fw []Event
	var logs []LogEvent
	s := &Server{Addr: "127.0.0.1:0", Allow: func(netip.Addr) string { return "gw" },
		Handler:    func(es []Event) { mu.Lock(); fw = append(fw, es...); mu.Unlock() },
		LogHandler: func(e LogEvent) { mu.Lock(); logs = append(logs, e); mu.Unlock() }}
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	c, err := net.Dial("udp", s.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("firewall,info MTM-11 forward: in:bridge out:ether1, connection-state:new proto TCP (SYN), 192.168.88.50:51234->93.184.216.34:443, len 60"))
	c.Write([]byte("system,info,account user admin logged in from 192.168.88.2 via ssh"))
	c.Write([]byte("complete nonsense"))
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := len(fw) == 1 && len(logs) == 1 && s.Unknown.Load() == 1
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(fw) != 1 || fw[0].Prefix != "MTM-11" || len(logs) != 1 || logs[0].Device != "gw" || !logs[0].HasTopic("account") || s.Unknown.Load() != 1 || s.Logs.Load() != 1 {
		t.Fatalf("fw=%+v logs=%+v unknown=%d", fw, logs, s.Unknown.Load())
	}
}
