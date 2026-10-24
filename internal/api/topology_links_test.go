package api

import (
	"sort"
	"testing"

	"github.com/napfkuchen1/mtmon/internal/store"
)

func TestDeviceLinksTree(t *testing.T) {
	// hEX ether5 → wAP; hEX ether2 → PoE switch → Audience ether1; Audience ether2 → RB4011 ether1.
	// MNDP crosses the bridges, so every device sees all others behind its uplink port.
	n := func(dev, iface, ident string) store.Neighbor {
		return store.Neighbor{Device: dev, Iface: iface, Ident: ident}
	}
	ns := []store.Neighbor{
		n("hEX", "ether5", "wAP"), n("hEX", "ether2,bridge", "Audience"), n("hEX", "ether2,bridge", "RB4011"),
		n("wAP", "ether1", "hEX"), n("wAP", "ether1", "Audience"), n("wAP", "ether1", "RB4011"),
		n("Audience", "ether1", "hEX"), n("Audience", "ether1", "wAP"), n("Audience", "ether2", "RB4011"),
		n("RB4011", "ether1", "Audience"), n("RB4011", "ether1", "hEX"), n("RB4011", "ether1", "wAP"),
	}
	managed := map[string]bool{"hEX": true, "wAP": true, "Audience": true, "RB4011": true}
	var got []string
	for _, l := range deviceLinks(ns, managed, "hEX") {
		got = append(got, l.From+">"+l.To)
	}
	sort.Strings(got)
	want := []string{"Audience>RB4011", "hEX>Audience", "hEX>wAP"}
	if len(got) != len(want) {
		t.Fatalf("links %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("links %v, want %v", got, want)
		}
	}
}

func TestNeighborPort(t *testing.T) {
	for in, want := range map[string]string{"ether2,bridge": "ether2", "bridge/ether5": "ether5", "bridge": "bridge", "": ""} {
		if got := neighborPort(in); got != want {
			t.Errorf("neighborPort(%q) = %q, want %q", in, got, want)
		}
	}
}
