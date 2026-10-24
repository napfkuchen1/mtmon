package advice

import (
	"fmt"
	"strings"
)

func init() { Rules = append(Rules, Rule{"vpn-peer-idle", ruleVPNPeerIdle}) }

// VPNPeerIdleDays: a WireGuard peer without a handshake for this long is probably not used any more.
const VPNPeerIdleDays = 30

// ruleVPNPeerIdle flags WireGuard peers (clients keyed "VPN:<address>") that never connected or were not seen for
// 30 days: every enabled peer is a door into the network.
func ruleVPNPeerIdle(s Snapshot) ([]Suggestion, string) {
	cut := since(s.Now(), VPNPeerIdleDays*24*3600*1e9)
	var out []Suggestion
	peers := 0
	for _, c := range s.Clients() {
		if !strings.HasPrefix(c.MAC, "VPN:") {
			continue
		}
		peers++
		if c.Online || c.LastSeen == 0 || c.LastSeen >= cut || c.FirstSeen >= cut {
			continue // connected, or watched for less than the idle time
		}
		never := c.LastSeen-c.FirstSeen < 300 // last seen right when mtmon first listed it: no handshake since
		ev := []string{fmt.Sprintf("WireGuard peer %s (tunnel address %s) on %s", c.Display(), c.IP, c.Device)}
		if never {
			ev = append(ev, fmt.Sprintf("No handshake since mtmon first saw the peer more than %d days ago", VPNPeerIdleDays))
		} else {
			ev = append(ev, fmt.Sprintf("Last handshake more than %d days ago", VPNPeerIdleDays))
		}
		out = append(out, Suggestion{
			Subject: c.MAC, Severity: SevTip, Category: CatHygiene, Link: "client/" + c.MAC, Confidence: "medium",
			Title:    fmt.Sprintf("VPN peer %s is not used", c.Display()),
			Why:      "A WireGuard peer that never connects (a Back to Home invitation nobody accepted, a phone that is gone) is still a valid key into your network. Removing unused peers keeps that door count small.",
			Evidence: ev,
			Steps:    []string{"If nobody uses this peer any more, remove it on the router (command below). A Back to Home user is removed in the MikroTik app under Back to Home → Users.", "Keep it if it is a seldom-used device, such as a holiday laptop."},
			Commands: fmt.Sprintf("/interface wireguard peers print where allowed-address~\"%s/\"\n# then, with the number from that list:\n/interface wireguard peers remove <number>", c.IP),
			Limits:   "mtmon only knows the handshake time the router reports; a peer that connects once a year would also show up here.",
		})
	}
	if peers == 0 {
		return nil, "no WireGuard peers known"
	}
	return out, ""
}
