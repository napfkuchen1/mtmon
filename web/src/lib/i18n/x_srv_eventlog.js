// German translations of backend text – router event log
export default {
  "system_critical": "Wichtiges System-Ereignis", "script_error": "Skriptfehler", "cli_error": "Fehler im Befehl", "login_failures": "Fehlgeschlagene Logins",
  "dhcp_problem": "DHCP-Problem", "wifi_disconnects": "WLAN-Verbindungsabbrüche", "wireguard_handshake": "WireGuard-Handshake",
  "{dev} logged an important event ({topics}): {msg}": "{dev} hat ein wichtiges Ereignis protokolliert ({topics}): {msg}",
  "Command from {user}@{src} failed on {dev}: {why}": "Befehl von {user}@{src} auf {dev} fehlgeschlagen: {why}",
  "A script on {dev} failed: {msg}": "Ein Skript auf {dev} ist fehlgeschlagen: {msg}",
  "Repeated failed logins on {dev} from {src} (last user {user} via {via}, 5 or more in 10 min)": "Wiederholte fehlgeschlagene Logins auf {dev} von {src} (zuletzt Benutzer {user} über {via}, 5 oder mehr in 10 Min.)",
  "DHCP on {dev} has trouble handing out addresses: {msg}": "DHCP auf {dev} hat Probleme beim Vergeben von Adressen: {msg}",
  "Client {mac} keeps disconnecting from {dev} (6 or more in 5 min)": "Client {mac} trennt sich ständig von {dev} (6 oder mehr in 5 Min.)",
  "last reason: {why}": "letzter Grund: {why}",
  "WireGuard handshakes on {dev} keep failing: {msg}": "WireGuard-Handshakes auf {dev} schlagen weiter fehl: {msg}",
  "Router event log (logins, DHCP, Wi-Fi, errors)": "Router-Ereignislog (Logins, DHCP, WLAN, Fehler)",
  "The router also sends its system, account, DHCP, Wi-Fi, WireGuard and script log lines to mtmon. They are searchable on the device page, and mtmon alerts on login-failure bursts, critical events, DHCP trouble, Wi-Fi disconnect storms and failing scripts. Needs the log target; it is set up with it if missing.": "Der Router schickt zusätzlich seine System-, Account-, DHCP-, WLAN-, WireGuard- und Skript-Logzeilen an mtmon. Sie sind auf der Geräteseite durchsuchbar, und mtmon alarmiert bei gehäuften Login-Fehlern, kritischen Ereignissen, DHCP-Problemen, WLAN-Abbruch-Stürmen und fehlschlagenden Skripten. Braucht das Log-Ziel; es wird bei Bedarf mit angelegt.",
  "Send the router's event log to mtmon": "Das Ereignislog des Routers an mtmon senden",
  "Login failures, critical system events, DHCP and Wi-Fi trouble, WireGuard handshakes and failing scripts reach mtmon, which shows them per device and raises alerts for the ones that matter. One logging rule per topic is added (a rule with several topics only matches lines that carry all of them). Local logging stays as it is.": "Login-Fehler, kritische Systemereignisse, DHCP- und WLAN-Probleme, WireGuard-Handshakes und fehlschlagende Skripte erreichen mtmon, das sie pro Gerät zeigt und bei den wichtigen alarmiert. Pro Thema wird eine Logging-Regel angelegt (eine Regel mit mehreren Themen trifft nur Zeilen, die alle tragen). Das lokale Logging bleibt unverändert.",
  "The logging rules are deleted": "Die Logging-Regeln werden gelöscht",
}
