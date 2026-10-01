# Router-Setup (RouterOS 7)

## 0. Automatisch (empfohlen): Devices → Add device
Der Wizard liest per REST: Identität, Modell, Pakete, WLAN-Stack, Bridges/HW-Offload, DHCP/NAT, FastTrack, Firewall-Regeln, Traffic-Flow-/Log-Stand. Danach zeigt er einen **Plan** mit Begründung, Befehlen, Undo und Risiko je Schritt:

| Schritt | Änderung am Router | Undo |
|---|---|---|
| Backup | `/export` als Datei `mtmon-backup-…` | bleibt liegen |
| User | Gruppe `mtmon-ro` (read,api,rest-api) + User `mtmon`, Login nur von mtmon-IP | löschen |
| Traffic Flow | `/ip traffic-flow` enabled + Target → mtmon:2055 (nur Router, nicht APs) | alter Zustand |
| Syslog | Log-Action `mtmon-syslog` → mtmon:5514 + Regel `topics=firewall` | löschen |
| Firewall-Log | bestehende drop/reject-Regeln: `log=yes`, Prefix `MTM-<id>` | alter Wert |
| optional | `MTM-NEW` Passthrough-Logregel (neue Verbindungen), FastTrack aus | löschen / zurück |

Alle Objekte tragen den Kommentar `mtmon-managed`. Admin-Login wird einmal benutzt, nie gespeichert. Jeder Schritt ist im Manifest (Device-Seite → „Änderungen auf dem Router“). Schlägt etwas fehl, rollt mtmon sofort zurück. **Offboarding** (Device-Seite) baut in umgekehrter Reihenfolge zurück; Objekte, die jemand inzwischen geändert hat, werden nicht angefasst (Guard). Fallback: manuelles CLI-Skript (`…/offboard-script`).

Die folgenden Abschnitte sind die manuelle Variante.

Alle Snippets erzeugt die UI unter **Settings** mit der richtigen mtmon-IP. Syntax gegen die MikroTik-Doku geprüft (Traffic Flow, REST API, User).

## 1. Traffic-Flow (IPFIX)
```routeros
/ip traffic-flow set enabled=yes interfaces=all cache-entries=16k active-flow-timeout=30s inactive-flow-timeout=15s
/ip traffic-flow target add dst-address=<MTMON_IP> port=2055 version=ipfix
```
- Default von `active-flow-timeout` ist **30 min** – ohne die Änderung sieht man lange Verbindungen erst sehr spät. 30 s = Live-Raten mit ~30 s Glättung.
- Liegt mtmon hinter einer anderen Quelladresse des Routers: `src-address=` setzen oder die Quelle in `flow_sources` eintragen, sonst werden die Flows verworfen (Zähler „Dropped (unknown exporter)“ in Settings).
- Alle Router/APs mit Routing/NAT exportieren lassen; reine APs im Bridge-Mode liefern kaum Flows (Hardware-Pfad).

## 2. Read-only API-User
```routeros
/user group add name=mtmon-ro policy=read,api,rest-api,!local,!telnet,!ssh,!ftp,!reboot,!write,!policy,!test,!winbox,!password,!web,!sniff,!sensitive,!romon
/user add name=mtmon group=mtmon-ro password=<STARKES_PASSWORT> address=<MTMON_IP>/32
```

## 3. REST über HTTPS
```routeros
/certificate add name=mtmon-ssl common-name=$[/system identity get name] days-valid=3650 key-usage=tls-server
/certificate sign mtmon-ssl
/ip service set www-ssl certificate=mtmon-ssl disabled=no address=<MTMON_IP>/32
```
Fingerprint holen und in die Config eintragen (TLS-Pinning statt `insecure`):
```bash
pct exec 210 -- mtmon fingerprint 192.168.88.1:443
```
Test: `pct exec 210 -- mtmon check` (zeigt je Gerät OK/FAIL mit Ursache: Auth, Zertifikat, Paket fehlt …).

## 4. Sichtbarkeits-Grenzen (wichtig für Erwartungen)
MikroTik-Doku: *Traffic Flow sieht nur Traffic, der von der Router-CPU verarbeitet wird; hardwarebeschleunigter Traffic (HW-Offload) taucht nicht auf.*
- **Bridge-HW-Offload** (`/interface bridge port print` → `hw=yes`): Client↔Client im selben Bridge/Switch-Chip ist unsichtbar. Geroutetes/NAT-Internet-Traffic ist sichtbar.
- **FastTrack**: verkürzt den Paketpfad; Flows können fehlen/unvollständig sein (aus der Community, auf dem eigenen Router prüfen: `/ip firewall filter print where action=fasttrack-connection`). Für volle Sichtbarkeit die Regel deaktivieren (kostet CPU) – Trade-off bewusst wählen.
- Domains: aus dem Router-DNS-Cache. Clients mit eigenem DoH/DoT-Resolver erscheinen nur als IP.

## 5. Entfernen (Rollback, 3 Zeilen)
```routeros
/ip traffic-flow target remove [find dst-address=<MTMON_IP>]
/ip traffic-flow set enabled=no
/user remove mtmon ; /user group remove mtmon-ro
```
