// SPDX-License-Identifier: AGPL-3.0-only
// German UI texts – trusted reverse proxies (login hardening)
export default {
  "Trusted reverse proxies": "Vertrauenswürdige Reverse-Proxys",
  "IP addresses or networks of your reverse proxy, separated by commas. Only these (and mtmon’s own machine) may pass the visitor’s address and host name in X-Forwarded-* headers. Leave empty if the proxy runs on the same machine as mtmon.":
    "IP-Adressen oder Netze deines Reverse-Proxys, durch Kommas getrennt. Nur diese (und mtmons eigener Rechner) dürfen Adresse und Hostnamen des Besuchers in X-Forwarded-*-Headern übergeben. Leer lassen, wenn der Proxy auf demselben Rechner wie mtmon läuft.",
  "Sender is a trusted proxy": "Absender ist vertrauenswürdiger Proxy",
  "This request carries X-Forwarded-* headers from {ip}, which is not a trusted proxy, so mtmon ignores them. If {ip} is your reverse proxy, add it under “Trusted reverse proxies”.":
    "Diese Anfrage enthält X-Forwarded-*-Header von {ip}. Das ist kein vertrauenswürdiger Proxy, deshalb ignoriert mtmon sie. Ist {ip} dein Reverse-Proxy, trag die Adresse unter „Vertrauenswürdige Reverse-Proxys“ ein.",
}
