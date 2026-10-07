package enrich

import "fmt"

var tcpudp = map[uint16]string{
	20: "FTP-data", 21: "FTP", 22: "SSH", 23: "Telnet", 25: "SMTP", 53: "DNS", 67: "DHCP", 68: "DHCP", 69: "TFTP",
	80: "HTTP", 88: "Kerberos", 110: "POP3", 119: "NNTP", 123: "NTP", 135: "MS-RPC", 137: "NetBIOS", 138: "NetBIOS",
	139: "NetBIOS", 143: "IMAP", 161: "SNMP", 162: "SNMP-trap", 389: "LDAP", 443: "HTTPS", 445: "SMB", 465: "SMTPS",
	500: "IPsec-IKE", 514: "Syslog", 515: "LPD", 520: "RIP", 546: "DHCPv6", 547: "DHCPv6", 587: "SMTP-submission", 631: "IPP",
	636: "LDAPS", 853: "DNS-over-TLS", 873: "rsync", 993: "IMAPS", 995: "POP3S", 1080: "SOCKS", 1194: "OpenVPN",
	1433: "MSSQL", 1701: "L2TP", 1723: "PPTP", 1812: "RADIUS", 1813: "RADIUS-acct", 1883: "MQTT", 1900: "SSDP",
	2049: "NFS", 2055: "NetFlow", 3074: "Xbox-Live", 3306: "MySQL", 3389: "RDP", 3478: "STUN", 3702: "WS-Discovery",
	4500: "IPsec-NAT-T", 5000: "UPnP/Synology", 5060: "SIP", 5061: "SIP-TLS", 5222: "XMPP", 5228: "Google-Push", 5353: "mDNS",
	5355: "LLMNR", 5432: "PostgreSQL", 5683: "CoAP", 5900: "VNC", 6379: "Redis", 6881: "BitTorrent", 7547: "TR-069",
	8006: "Proxmox", 8080: "HTTP-alt", 8123: "Home-Assistant", 8291: "Winbox", 8443: "HTTPS-alt", 8728: "RouterOS-API",
	8729: "RouterOS-API-SSL", 8883: "MQTT-TLS", 9000: "HTTP-alt", 9100: "JetDirect", 27015: "Steam", 32400: "Plex", 51820: "WireGuard",
}

var protoNames = map[uint8]string{1: "ICMP", 2: "IGMP", 6: "TCP", 17: "UDP", 41: "IPv6-in-IP", 47: "GRE", 50: "ESP", 51: "AH", 58: "ICMPv6", 89: "OSPF", 112: "VRRP"}

// ProtoName returns a short protocol name.
func ProtoName(p uint8) string {
	if n, ok := protoNames[p]; ok {
		return n
	}
	return fmt.Sprintf("proto-%d", p)
}

// Service guesses the service from the remote (server) port; non-TCP/UDP gets the protocol name.
func Service(proto uint8, rport, cport uint16) string {
	if proto != 6 && proto != 17 {
		return ProtoName(proto)
	}
	if n, ok := tcpudp[rport]; ok {
		return n
	}
	if n, ok := tcpudp[cport]; ok && rport > 1023 {
		return n
	}
	if proto == 17 && rport == 443 {
		return "QUIC"
	}
	return ""
}
