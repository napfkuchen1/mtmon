# Architektur

```
                         ┌───────────────────────── LXC "mtmon" (Debian 13, unprivileged) ─────────────────────────┐
MikroTik ─ UDP 2055 ────▶│ flow.Collector ─▶ flow.Decoder (IPFIX / NetFlow v9, Template-Cache je Exporter)          │
  (Allowlist)            │        │                                                                                │
                         │        ▼                                                                                │
                         │ live.Pipeline: Richtung · Dedup · Client-Zuordnung (IP→MAC) · Enrichment ──┬─▶ live.Hub │──WS──▶ UI (1 s)
MikroTik ─ HTTPS REST ◀──│ poller.Manager (1 Worker je Gerät) ─▶ Clients/Inventar/Metriken ─▶ store  ─┘            │
  (read-only)            │ enrich: Service(IANA) · Router-DNS-Cache · Reverse-DNS · GeoIP/ASN(mmdb) · OUI           │
                         │ store: SQLite WAL, Batch-Writer (1 s), Rollups, Retention, tägl. Backup                  │
                         │ alert.Engine: Dedup+Cooldown → Webhook / ntfy / SMTP                                     │
Browser ◀─ HTTPS :8443 ──│ api: Session-Login (argon2id), JSON-API, WebSocket, eingebettete Svelte-UI                │
                         └──────────────────────────────────────────────────────────────────────────────────────────┘
```

## Flow-Verarbeitung (Regeln, die das Ergebnis bestimmen)

| Quelle → Ziel | Richtung | Client |
|---|---|---|
| lokal → extern | `u` (Upload) | Quelle |
| extern → lokal | `d` (Download) | Ziel |
| lokal → lokal | `i` (intern) | Quelle; Duplikate (Flow wird je durchlaufener Schnittstelle exportiert) werden per 5-s-Fenster entfernt |
| extern → extern | verworfen | WAN-seitige NAT-Kopie bzw. Transit |

„Lokal“ = `local_nets` (Default RFC1918 + ULA/Link-Local). Da nur die lokale Seite gezählt wird, ist `interfaces=all` auf dem Router sicher: die NAT-Kopie am WAN-Interface zählt nicht doppelt.
IP→MAC: aktuelle Zuordnung aus DHCP-Leases/ARP/WLAN (Hub), bei Lücken aus `ip_history` zum Flow-Zeitpunkt, sonst Fallback auf die IP.
Live-Raten sind ein 30-s-Mittel (Router exportieren aktive Flows alle `active-flow-timeout`).

## Client-Erkennung (Poller)
- Jede Sekunde-Tick je Gerät; Intervalle: Interfaces 2 s, System/Health 10 s, WLAN 5 s, DHCP/ARP/Bridge-Host 15 s, DNS-Cache 30 s, Neighbors 60 s (×2 bei Router-CPU > 70 %).
- Fehler: exponentielles Backoff (2,4,8,16,30 s), Offline-Alert nach 3 Fehlern, Dedup gegen Alert-Spam.
- „Online“ = WLAN-Registrierung, Bridge-Host oder ARP (reachable/delay/probe). Nur-DHCP-Lease ≠ online.
- WLAN-Eintrag eines **AP** schlägt die Kopie des CAPsMAN-Managers (kein Roaming-Flattern). Roaming-Events werden protokolliert.

## Speicher (SQLite, `modernc.org/sqlite`, WAL)
| Tabelle | Inhalt | Aufbewahrung |
|---|---|---|
| `flows` | Rohflows je Client (Timeline) | `raw_retention_days` (3), Zeilenobergrenze 15 Mio |
| `rollup_1m` | Minute × Client × Ziel × Port | 3 Tage |
| `rollup_1h` | Stunde × Client × Ziel × Port | `retention_days` (30) |
| `rollup_1h_client` | Stunde × Client (klein) | 30 Tage |
| `rollup_1d` | Tag × Ziel × Port (ohne Client) | 30 Tage |
| `clients`, `ip_history`, `roam`, `seen_dest` | Inventar, IP-Historie, Roaming, „erstmals gesehen“ | bis Retention |
| `dev_metrics`, `if_metrics` | 1 Sample/min | 30 Tage |
| `alerts`, `neighbors`, `devices_state` | | 30 Tage |

**Abfrage-Stufen** (`store/queries.go`): ≤ 2 h → Minute; ≤ 8 d → Stunde; darüber → Tag (Ziele) bzw. `rollup_1h_client` (Clients, Charts, Summen). Grund: gemessen auf 100 Clients × 30 Tage kostete eine einzelne Stufe bis zu 4,4 s; gestuft bleibt alles < 320 ms ([Testreport](testreport.md)). Bei 24 h/7 d sind Top-Listen auf die volle Stunde ausgerichtet (bis zu 59 min mehr Daten als „24 h“).
Schreibpfad: Channel-freier Puffer (max. 200 000 Zeilen, danach Drop mit Zähler), 1-s-Flush, Rollups werden vor dem Upsert im Speicher voraggregiert.

## Sicherheit
- Login: argon2id, 12-h-Session (Cookie HttpOnly, SameSite=Strict, Secure bei TLS), Rate-Limit 5 Fehlversuche/min/IP, Origin-Check bei schreibenden Calls, CSP/nosniff/frame-deny. Hinter einem Reverse-Proxy teilen sich alle Clients die Proxy-IP (gemeinsames Limit).
- Router-Zugang: eigener Read-only-User (`read,api,rest-api`, ohne write/policy/ssh/…), Quelladresse auf mtmon beschränkt, **Zertifikats-Pinning** (`fingerprint`), kein stilles `insecure`.
- Flows nur von konfigurierten Exporter-IPs (`addr` oder `flow_sources`); andere werden gezählt und verworfen.
- systemd-Härtung (`ProtectSystem=strict`, `NoNewPrivileges`, `SystemCallFilter` …) mit automatischem Fallback-Drop-in, falls der Container Sandbox-Optionen ablehnt.
- Zugangsdaten der Router liegen im Klartext in `/etc/mtmon/config.json` (0640 root:mtmon). Das ist bewusst einfach; die Datei nicht in Backups außer Haus geben, Read-only-User nutzen.
- CSV-Export neutralisiert Formel-Injection (`= + - @`).

## v2: Geräteverwaltung, Firewall, Services
- **Geräte** liegen zusätzlich in SQLite (`devices`, Passwort AES-256-GCM, Schlüssel `secret.key` 0600 im Datadir); `config.json`-Geräte bleiben (nur lesend). `poller.Manager.Reload()` startet/stoppt Worker zur Laufzeit.
- **provision** (`internal/provision`): Probe (Capabilities) → Plan → Apply mit Manifest (`manifest`-Tabelle, Undo-JSON + Guard), Auto-Rollback, Verify (Login mit neuem User), Rollback/Offboarding, ManualScript. Einziger Code, der schreibend auf RouterOS zugreift. SSRF-Schutz: nur private/lokale Ziele; TLS-Fingerprint wird gezeigt und bestätigt (TOFU).
- **syslog** (`internal/syslog`, UDP :5514): Parser für RouterOS-Firewall-Logzeilen, Absender-Allowlist = bekannte Geräte → `fw_events`, `fw_hourly`, Regel-Meta `fw_rules`.
- **Verdikt** je Verbindung: blockiert, wenn ein Drop-Event zu Peer-IP/Port/Proto im Fenster −300 s/+60 s passt; sonst „durchgelassen“ (nur wenn der Router Firewall-Logging hat).
- **Services**: Regeln (`service_rules`: port/ip/cidr/host/asn; Vorrang ip > cidr > host > asn > port > IANA-Default) im Classifier der Live-Pipeline; Änderungen werden rückwirkend in `rollup_*`/Flow-Tabellen in Tageshäppchen neu gelabelt (Hintergrund, blockiert den Writer nicht).
