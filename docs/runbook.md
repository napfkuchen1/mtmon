# Runbook

## 0. Vor jedem Eingriff: Backup-Plan
| Was | Wie | Rückweg |
|---|---|---|
| ganzer Container | `pct snapshot 210 pre-change` (LVM-thin/ZFS) oder `vzdump 210 --mode stop --storage local` | `pct rollback 210 pre-change` bzw. `pct restore <id> <archiv>` |
| nur Daten | tägliche Kopie `VACUUM INTO` nach `/var/lib/mtmon/backups/` (7 Generationen, automatisch) | siehe „DB wiederherstellen“ |
| Router | mtmon ändert nie Router-Config; manuelle Änderungen per `/export` sichern | Abschnitt 5 in [router-setup.md](router-setup.md) |

## 1. Installation
```bash
./install-mtmon.sh --dry-run                      # Plan prüfen
./install-mtmon.sh --ctid 210 --storage local-lvm --bridge vmbr0 \
    --ip 192.168.88.10/24 --gw 192.168.88.1 --devices-file devices.json --with-geo
```
Was passiert: Preflight (PVE ≥ 9, CTID frei, Storage aktiv, Bridge da, Binary lauffähig + Checksumme) → Debian-13-Template (wird geladen, falls fehlend) → `pct create` (unprivileged, nesting=1, 2 vCPU/1 GB/256 MB Swap/8 GB, onboot) → Benutzer `mtmon`, Binary, systemd-Unit, Config, Admin-Passwort → Start → **Self-Test**. Schlägt etwas fehl, bleibt der CT zur Analyse erhalten (`--cleanup-on-fail` entfernt ihn – nur wenn er die Installer-Markierung trägt).
Idempotent: Läuft auf eine belegte CTID, bricht das Skript ab ohne etwas zu ändern.
`nesting=1` ist der PVE-GUI-Standard für unprivilegierte Debian-CTs und vermeidet Namespace-Fehler bei systemd-Sandboxing.

`devices.json` (Array wie in `config.example.json` → `devices`):
```json
[{"name":"gw-main","addr":"192.168.88.1","role":"router","user":"mtmon","pass":"…","fingerprint":"<sha256>"},
 {"name":"ap-living","addr":"192.168.88.2","role":"ap","user":"mtmon","pass":"…","fingerprint":"<sha256>"}]
```

## 2. Betrieb
```bash
pct exec 210 -- systemctl status mtmon
pct exec 210 -- journalctl -u mtmon -f
pct exec 210 -- mtmon check            # REST-Zugriff je Router testen
pct exec 210 -- mtmon selftest         # UI, /healthz, Flow-Port, DB, Passwort
pct exec 210 -- mtmon passwd -u admin  # Passwort ändern (Env MTMON_PASSWORD oder Eingabe)
pct exec 210 -- systemctl restart mtmon   # nach Config-Änderung
```
Config: `/etc/mtmon/config.json` (Referenz: `deploy/config.example.json`). Felder: `local_nets`, `raw_retention_days`, `retention_days`, `reverse_dns`, `alert_webhook`, `alert_ntfy_url`, `alert_smtp`, `dns_resolvers` (Liste erlaubter Resolver → „rogue DNS“-Alert), `tls_cert/tls_key` (eigenes Zertifikat statt self-signed), `no_tls` (hinter Reverse-Proxy).
Firewall-Empfehlung: UDP 2055 nur von Router-IPs; TCP 8443 nur aus dem Admin-Netz.

### GeoIP / ASN / Hersteller
`--with-geo` bzw. später `pct exec 210 -- mtmon-update-geo` lädt DB-IP-Lite (Country + ASN, CC BY 4.0 – Namensnennung steht in der UI) und die IEEE-OUI-Liste, monatlich per systemd-Timer. Braucht `curl` oder `wget` im CT (`apt-get install -y curl`) und Internet. Ohne diese Dateien laufen Länder/ASN/Hersteller einfach leer.

## 3. Update mit Auto-Rollback
```bash
./update-mtmon.sh --ctid 210 --binary ./mtmon --dry-run
./update-mtmon.sh --ctid 210 --binary ./mtmon
```
Ablauf: Prüfung (ist es ein mtmon-CT? Binary lauffähig?) → Snapshot (Fallback vzdump; ohne Backup **Abbruch**) → Dienst stoppen, Kopie von Binary + DB + Config nach `/var/lib/mtmon/backups/pre-update-<ts>/` → neues Binary → Start → Self-Test + Versionsprüfung. **Bei Fehler:** altes Binary und DB-Kopie werden automatisch zurückgespielt und erneut geprüft. Gleiche Binary-Version ⇒ „nothing to do“.

## 4. DB wiederherstellen
```bash
pct exec 210 -- systemctl stop mtmon
pct exec 210 -- sh -c 'ls /var/lib/mtmon/backups/'                   # mtmon-YYYYMMDD-HHMMSS.db
pct exec 210 -- sh -c 'cd /var/lib/mtmon && rm -f mtmon.db-wal mtmon.db-shm && cp backups/mtmon-<ts>.db mtmon.db && chown mtmon:mtmon mtmon.db'
pct exec 210 -- systemctl start mtmon
```

## 5. Deinstallation
`./uninstall-mtmon.sh --ctid 210 --confirm 210` – prüft, dass es ein mtmon-CT ist, macht vzdump, erst dann `pct destroy`. `--dry-run` zeigt nur an. Router-Reste entfernen: [router-setup.md](router-setup.md) §5.

## 6. Troubleshooting
| Symptom | Ursache / Prüfung | Lösung |
|---|---|---|
| UI ok, keine Flows | Settings → „Flow packets“ bleibt 0 | Router-Target/Port/Firewall; `tcpdump -ni eth0 udp port 2055` im CT |
| „Dropped (unknown exporter)“ steigt | Router sendet von anderer Quell-IP | `flow_sources` ergänzen oder `src-address` im Target setzen |
| „packets without template“ hoch | Template-Paket noch nicht gesehen (nach Neustart normal, bis ~2 min) | warten; bei Dauer: `v9-template-refresh` am Router verkleinern |
| Gerät „authentication failed“ | falscher User/Passwort oder `address=` im User passt nicht zur mtmon-IP | `mtmon check`; User-Adresse prüfen |
| „certificate fingerprint mismatch“ | Router-Zertifikat erneuert | `mtmon fingerprint <ip>:443`, Config aktualisieren |
| Gerät „… not found (package missing?)“ | kein `wifi`-Paket (Switch/Router ohne WLAN) | harmlos, WLAN-Abfrage wird alle 10 min neu versucht |
| Clients fehlen/immer offline | weder WLAN-Reg. noch Bridge-Host/ARP sichtbar | Benutzer darf `/interface/bridge/host`, `/ip/arp` lesen? Bridge-Name? |
| Traffic deutlich zu niedrig | HW-Offload/FastTrack | [router-setup.md](router-setup.md) §4 |
| `status=226/NAMESPACE` im Journal | CT verbietet Sandbox-Option | Installer setzt Fallback selbst; manuell: `deploy/relax.conf` → `/etc/systemd/system/mtmon.service.d/00-relax.conf` |
| DB wächst zu stark | sehr viele Flows | `raw_retention_days` senken (Zeilenobergrenze 15 Mio greift ohnehin) |
| Zeit falsch | Flow-Zeitstempel kommen vom Router | NTP auf Routern **und** Proxmox-Host |

## 7. Datenschutz / Organisation
Pro Client werden Ziel-IP/-Port/Zeit gespeichert (personenbezogen). Im Unternehmenskontext: Zweckbindung, Betriebsrat/Datenschutzbeauftragten einbinden, Zugriff auf die UI einschränken, Retention so kurz wie nötig (`raw_retention_days`, `retention_days`). Kein Payload, keine URLs – nur Flow-Metadaten.

## v2: Geräte per UI, Offboarding, Services
- **Gerät hinzufügen:** UI → Devices → Add device. Bei Fehler im Setup rollt mtmon automatisch zurück; Backup-Datei `mtmon-backup-…` liegt auf dem Router (Files).
- **Gerät entfernen:** Device-Seite → Offboarding (Admin-Login nötig) → baut alle `mtmon-managed`-Änderungen zurück. „Nur vergessen“ löscht nur den mtmon-Eintrag (Router bleibt konfiguriert). Router nicht erreichbar → „Manuelles Skript“ im CLI einfügen.
- **Notfall am Router:** `/user remove [find comment~"mtmon-managed"]`, `/system logging remove [find comment~"mtmon-managed"]`, `/system logging action remove [find comment~"mtmon-managed"]`, `/ip traffic-flow target remove [find]`; Firewall-Regeln: `log=no` bei Prefix `MTM-*`. Oder Backup einspielen: `/import mtmon-backup-….rsc` (prüfen!).
- **Secret-Key:** `/var/lib/mtmon/secret.key` mitsichern; ohne ihn sind UI-Geräte-Passwörter nicht lesbar (Gerät neu hinzufügen).
- **Syslog kommt nicht an:** Settings/Selftest prüfen (UDP 5514 gebunden?), Router-Firewall input-Chain bzw. Pfad zu mtmon, Gerät muss als Absender bekannt sein.
- **Service-Regel falsch:** Services → Regel löschen → Label wird auf Standard zurückgesetzt.
