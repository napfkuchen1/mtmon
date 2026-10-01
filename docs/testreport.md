# Testbericht

Stand: 2026-10-01 · getestet in Cloud-Sandbox (Linux), **nicht** auf echter Proxmox-/RouterOS-Hardware.

## Ergebnisse
| Bereich | Methode | Ergebnis |
|---|---|---|
| Flow-Decoder | Unit-Tests: IPFIX, NetFlow v9, Müll-Pakete, Templates pro Exporter, Benchmark | grün |
| Store/API/Auth | Unit- + API-Tests (Login, Rate-Limit, CSV-Guard, Tiering) | grün |
| Enrichment | Test mit echten DB-IP-Lite- und IEEE-OUI-Daten | grün |
| End-to-End | Mock-RouterOS + synthetische Flows → DB → API → WebSocket | grün |
| Race Detector | `go test -race ./...` | sauber |
| Lint | `go vet`, staticcheck | sauber |
| Deploy-Skripte | Fake-Proxmox-Stubs (`make deploy-test`) | 64/64 |
| UI | Playwright-Screenshots aller Seiten inkl. Wizard, Firewall, Services | ok |
| v2 Provisioning | Probe, Apply+Rollback = exakter Ursprungszustand, Auto-Rollback bei Fehler/Verify-Fehler/Backup-Fehler, Guard schützt fremde Objekte | grün |
| v2 End-to-End | Gerät hinzufügen → Flows+Syslog → blockiert/durchgelassen → NTP-Service → Regel+Relabel → Offboarding | grün |
| Syslog-Parser | Unit + Fuzz | grün |

## Performance
| Messung | Wert |
|---|---|
| RAM idle | 16 MB RSS |
| RAM bei 5.000 Flows/s | ~42 MB RSS, 0 Drops |
| Scale-Test | 100 Clients × 30 Tage, DB 548 MB |
| Abfragen | alle < 320 ms, 30-Tage-Ansichten < 40 ms |
| Binary | ~19 MB, UI-JS 40 KB gzip |

## Nicht getestet (ehrlich)
- Echtes Proxmox VE 9: Skripte nur gegen Stubs. Erst `--dry-run`, Snapshot vorher.
- Echte MikroTik-Hardware: REST/Flow-Formate gegen Doku + Mock, nicht gegen Geräte. Hardware-Offload/FastTrack-Verhalten ist Community-Wissen.
- systemd-Sandboxing im unprivilegierten LXC: Fallback `relax.conf` vorhanden, aber ungetestet.
- v2-Schreibzugriffe (`PUT`/`POST <menu>/set`/`POST /export`, `src-address`, Syslog-Zeilenformat) nur gegen Mock und synthetische Zeilen; Erstlauf an einem Test-Router mit Backup, Bambu-Drucker-Szenario real prüfen.
- Legacy-`wireless`-Paket nur teilweise; `www-ssl` muss vorher aktiv sein.
