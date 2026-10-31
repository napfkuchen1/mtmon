// SPDX-License-Identifier: AGPL-3.0-only

package api

import (
	"crypto/rand"
	"net/netip"
	"strings"
)

// Snippets for setting a router up by hand (Router setup card and Add-device checklist). They are re-runnable:
// every object is only created if it does not exist yet (an existing one is skipped, not overwritten).

const pwAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// randomPassword returns a fresh random password, so a pasted snippet never leaves a well-known one behind.
func randomPassword(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the OS random source failing is not recoverable
	}
	for i := range b {
		b[i] = pwAlphabet[int(b[i])%len(pwAlphabet)]
	}
	return string(b)
}

// guardAdd wraps one command so that it only runs when no object of that name exists yet.
func guardAdd(findPath, name, cmd string) string {
	return `:if ([:len [` + findPath + ` find name=` + name + `]] = 0) do={ ` + cmd + ` }`
}

// flowSnippet enables Traffic Flow and adds the target only if it is not there yet: a second run must not create a
// second target (every flow would be exported, and counted, twice).
func flowSnippet(mtmonIP, port string) string {
	return "/ip traffic-flow set enabled=yes interfaces=all cache-entries=16k active-flow-timeout=30s inactive-flow-timeout=15s\n" +
		":if ([:len [/ip traffic-flow target find dst-address=" + mtmonIP + " port=" + port + "]] = 0) do={ /ip traffic-flow target add dst-address=" + mtmonIP + " port=" + port + " version=ipfix }"
}

// apiUserSnippet: read-only group and user, restricted to the mtmon address. pw is shown in the snippet.
func apiUserSnippet(mtmonIP, pw string) string {
	return "# the password below was generated for this page view: put the same one into mtmon for this device\n" +
		guardAdd("/user group", "mtmon-ro", "/user group add name=mtmon-ro policy=read,api,rest-api,!local,!telnet,!ssh,!ftp,!reboot,!write,!policy,!test,!winbox,!password,!web,!sniff,!sensitive,!romon") + "\n" +
		guardAdd("/user", "mtmon", "/user add name=mtmon group=mtmon-ro password="+pw+" address="+mtmonIP+"/32")
}

// restTLSSnippet: certificates and the www-ssl service. routerIP may be "" (placeholder stays).
func restTLSSnippet(routerIP, mtmonIP string) string {
	r := routerIP
	if _, err := netip.ParseAddr(r); err != nil {
		r = "<ROUTER-IP>"
	}
	return guardAdd("/certificate", "mtmon-ca", "/certificate add name=mtmon-ca common-name=mtmon-ca days-valid=3650 key-usage=key-cert-sign,crl-sign; /certificate sign mtmon-ca") + "\n" +
		guardAdd("/certificate", "mtmon-ssl", "/certificate add name=mtmon-ssl common-name="+r+" subject-alt-name=IP:"+r+" days-valid=3650 key-usage=digital-signature,key-encipherment,tls-server; /certificate sign mtmon-ssl ca=mtmon-ca") + "\n" +
		"# available-from is only set when www-ssl is not restricted yet (an empty list means \"any address\"). A restricted service keeps its list:\n" +
		"# add " + mtmonIP + "/32 to it by hand (check with: /ip service print where name=www-ssl), otherwise mtmon is locked out.\n" +
		":if ([:len [/ip service get www-ssl available-from]] = 0) do={ /ip service set www-ssl certificate=mtmon-ssl disabled=no available-from=" + mtmonIP + "/32 } " +
		"else={ :put \"www-ssl is already restricted: add " + mtmonIP + "/32 to its available-from list by hand\"; /ip service set www-ssl certificate=mtmon-ssl disabled=no }"
}

// cleanRouterIP accepts the router address from the query only if it is an IP (it ends up in a command).
func cleanRouterIP(s string) string {
	s = strings.TrimSpace(s)
	if a, err := netip.ParseAddr(s); err == nil {
		return a.String()
	}
	return ""
}
