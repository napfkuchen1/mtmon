// German translations of backend text – baseline checks (#190)
export default {
  "{dev} still runs {names}": "{dev} betreibt noch {names}",
  "Telnet and FTP send user name and password unencrypted. Anyone who can read the traffic in between gets the login of the router. SSH and Winbox do the same job encrypted.": "Telnet und FTP senden Benutzername und Passwort unverschlüsselt. Wer den Verkehr dazwischen mitliest, erhält den Router-Login. SSH und Winbox erledigen dasselbe verschlüsselt.",
  "Enabled in /ip service: {names}": "In /ip service aktiviert: {names}",
  "Check that you do not use the service (a backup script may use FTP), then disable it.": "Prüfe, dass du den Dienst nicht benutzt (ein Backup-Skript könnte FTP nutzen), und deaktiviere ihn dann.",
  "Based on the capabilities stored at the last probe of the device.": "Beruht auf den Fähigkeiten, die beim letzten Abruf des Geräts gespeichert wurden.",
  "{dev}: {names} answer on every address": "{dev}: {names} antwortet auf jeder Adresse",
  "A management service without a source address limit can be reached from every network the router is connected to, including the internet side if the firewall lets it through. Limiting it to the management network is free and removes most of the attack surface.": "Ein Verwaltungsdienst ohne Quelladress-Beschränkung ist aus jedem Netz erreichbar, an dem der Router hängt, auch von der Internetseite, wenn die Firewall es durchlässt. Ihn auf das Verwaltungsnetz zu beschränken kostet nichts und beseitigt den größten Teil der Angriffsfläche.",
  "Enabled in /ip service without an address: {names}": "In /ip service ohne Adresse aktiviert: {names}",
  "Decide which network administers this device, set it as the allowed address, keep one way in that you have tested (a second session open), and disable services you do not use.": "Lege fest, aus welchem Netz das Gerät verwaltet wird, trage es als erlaubte Adresse ein, behalte einen getesteten Zugang (zweite Sitzung offen lassen) und deaktiviere Dienste, die du nicht brauchst.",
  "mtmon only sees the service address list, not the firewall. If the input chain already drops these ports from outside, the risk is lower.": "mtmon sieht nur die Adressliste der Dienste, nicht die Firewall. Wenn die Input-Chain diese Ports von außen schon verwirft, ist das Risiko geringer.",
  "{dev} has no time synchronisation": "{dev} hat keine Zeitsynchronisation",
  "Without NTP the clock drifts. Log entries, mtmon's alert times and certificate validity checks then show wrong times.": "Ohne NTP läuft die Uhr weg. Logeinträge, Alarmzeiten in mtmon und Zertifikatsprüfungen zeigen dann falsche Zeiten.",
  "/system/ntp/client is disabled": "/system/ntp/client ist deaktiviert",
  "Enable the NTP client with a server you trust (or the router's gateway).": "Aktiviere den NTP-Client mit einem vertrauenswürdigen Server (oder dem Gateway des Routers).",
}
