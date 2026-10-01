# mtmon – Realtime-Monitoring für MikroTik Router & Access Points

Leichtgewichtiger Monitor (ein Go-Binary, ~19 MB, SQLite, eingebettete Svelte-UI) für einen **unprivilegierten LXC auf Proxmox VE 9**.
Funktionen wie UniFi Network (Geräte, Clients, Topologie, Insights), Optik wie das Cloudflare-Dashboard.

| | |
|---|---|
| Flows | MikroTik **Traffic-Flow (IPFIX / NetFlow v9)** → pro Client: Ziel-IP, Port, Service, Domain, Land, ASN |
| Geräte | RouterOS 7 **REST-API** (read-only): CPU/RAM/Temp, Interfaces, WLAN-Registrierungen (`/interface/wifi`), DHCP, ARP, Bridge-Hosts, Neighbors |
| Zero-Touch | Gerät in der **Web-UI hinzufügen** (IP + einmaliger Admin-Login): mtmon liest Capabilities aus, zeigt den Plan, richtet Read-only-User, Traffic Flow, Syslog und Firewall-Log ein – Admin-Login wird nicht gespeichert. **Offboarding** baut alles zurück |
| Firewall | Verdikt je Verbindung (blockiert / durchgelassen + Regel), Ports src→dst, blockierte Versuche, Top-Regeln |
| Services | Klassifizierung per Klick (Port/IP/CIDR/Domain/ASN) – „NTP“ anklicken → alle Clients/Ziele; rückwirkend auf alle Daten |
| UI | Overview · Live-Feed · Devices (+Add) · Clients · Services · Firewall · Insights · Topologie · Alerts · Settings |
| Alerts | Gerät offline, Interface down, CPU/Temp, neuer Client, Traffic-Spike, Port-Scan-Muster, (optional) fremder DNS – per Webhook / ntfy / SMTP |
| Footprint | ~16 MB RAM idle, ~45 MB bei 5.000 Flows/s, Container: 2 vCPU / 1 GB / 8 GB |

```
MikroTik ──IPFIX UDP 2055──┐
MikroTik ──REST :443 (ro)──┼──▶ LXC "mtmon" ── Go: Collector · Poller · Enrichment · SQLite · HTTP/WS+UI ──▶ Browser (HTTPS :8443)
```

## Schnellstart (Proxmox-Host, als root) – direkt aus GitHub

`<owner>` = dein GitHub-Benutzername. Nichts manuell kopieren: `install.sh` lädt das neueste Release, prüft die Prüfsumme und legt den LXC an.

```bash
export MTMON_REPO=<owner>/mtmon
bash -c "$(curl -fsSL https://raw.githubusercontent.com/$MTMON_REPO/main/install.sh)" -- install --dry-run   # erst ansehen
bash -c "$(curl -fsSL https://raw.githubusercontent.com/$MTMON_REPO/main/install.sh)" -- install --ctid 210 --storage local-lvm --bridge vmbr0 --with-geo
# später:  ... -- update --ctid 210      |      ... -- uninstall --confirm 210
```
Release erzeugen: Tag pushen (`git tag v2.0.1 && git push origin v2.0.1`) → GitHub Actions testet, baut und veröffentlicht `mtmon-linux-amd64.tar.gz` (+ `.sha256`). Offline/manuell geht weiterhin: Archiv entpacken, `./install-mtmon.sh --dry-run`.

Ausgabe: URL, Benutzer `admin`, generiertes Passwort. Danach in der UI **Devices → Add device**: Router-IP + einmaliger Admin-Login → Plan prüfen → Auto-Setup. Voraussetzung am Router: `www-ssl` (HTTPS) aktiv. Manuell geht weiterhin per `config.json` / Settings-Snippets.

Firewall: UDP 2055 (Flows) und UDP 5514 (Syslog) vom Router zu mtmon erlauben.

**Backup/Rollback:** `update-mtmon.sh` macht vorher Snapshot (Fallback vzdump) + DB-Kopie und rollt bei fehlgeschlagenem Health-Check selbst zurück. `uninstall-mtmon.sh` braucht `--confirm <CTID>` und sichert vorher. Details: [docs/runbook.md](docs/runbook.md).

## Entwickeln

```bash
make test         # vet + Tests mit Race-Detector
make lint         # staticcheck, shellcheck, gofmt
make scale        # 100 Clients × 30 Tage, jede Abfrage < 500 ms
make deploy-test  # Install/Update/Uninstall gegen simulierte Proxmox-Tools
make release      # dist/mtmon-<ver>-linux-amd64.tar.gz + SHA256
make run-demo     # lokale Demo mit Mock-Routern, http://127.0.0.1:18443
```

Weitere Doku: [Architektur](docs/architecture.md) · [Router-Setup](docs/router-setup.md) · [Runbook](docs/runbook.md) · [Testreport](docs/testreport.md)

## Wichtige Grenzen (ehrlich)
- Traffic-Flow sieht nur Traffic, den die **Router-CPU** verarbeitet. HW-Offload (Bridge-Switch-Chip) und FastTrack verfälschen/verstecken Flows → [Router-Setup](docs/router-setup.md).
- Domainnamen stammen aus dem DNS-Cache der Router (+ optional Reverse-DNS). HTTPS-SNI wird nicht gesehen; DoH/DoT-Clients erscheinen nur als IP.
- **Blockierte Pakete** sieht Traffic Flow nie; sie kommen nur über Syslog (Log-Regeln, die der Wizard auf bestehende drop/reject-Regeln setzt). „Durchgelassen“ ist ein Rückschluss (kein Drop-Log gefunden), exakt nur mit der optionalen MTM-NEW-Logregel.
- Auto-Setup ändert den Router (nur nach Bestätigung): Backup (/export) + Manifest + Auto-Rollback + Offboarding + manuelles CLI-Skript als Fallback. Gegen echte RouterOS-Hardware noch **nicht** getestet (nur Mock).
- UI gemischt Englisch/Deutsch (neue Seiten deutsch); Doku deutsch.
- **Datenschutz:** Verbindungsdaten pro Client sind personenbezogen (DSGVO). Im Firmenumfeld vorab Betriebsrat/Datenschutz klären; Retention ist einstellbar (`raw_retention_days`, `retention_days`).
