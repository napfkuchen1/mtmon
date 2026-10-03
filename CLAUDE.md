# mtmon – Projektregeln für Claude Code

Go-Backend (single static binary, SQLite via modernc, kein CGO) + Svelte-5-UI (`web/`, per `go:embed` eingebettet). Ziel: unprivilegierter LXC auf Proxmox VE 9.

## Befehle
`make test` · `make lint` · `make scale` · `make deploy-test` · `make build` · `make release` · `make run-demo`

## Layout
`cmd/{mtmon,flowgen,mockros}` · `internal/{flow,poller,enrich,store,live,alert,api,config,mockros,integration,provision,syslog,secret}` · `web/` · `deploy/` (Proxmox-Skripte + Stubs-Tests) · `docs/`

## Regeln
- **Read-only gegenüber Routern** – einzige Ausnahme: `internal/provision` (Wizard Auto-Setup/Offboarding, nur nach expliziter Nutzeraktion). Jede Änderung braucht Manifest-Eintrag + Undo + Backup (/export); Admin-Zugangsdaten nie speichern/loggen. Poller bleibt read-only (`poller.Client.Do` nur für provision).
- Keine Secrets committen (config.json, *.db, *.mmdb stehen in .gitignore).
- Neue Abfragen an `rollup_*`-Tabellen: Tier-Logik in `store/queries.go` beachten; `make scale` muss grün bleiben (alle Queries < 500 ms).
- Listen aus der API nie `null` liefern (`[]`), sonst crasht die UI.
- Deploy-Skripte: `--dry-run` muss funktionieren; Änderungen immer mit `deploy/test/test-deploy.sh` absichern; shellcheck sauber.
- RouterOS-Syntax nur gegen help.mikrotik.com verifiziert verwenden.
- Commits: Conventional Commits, klein; vor Commit `make test lint`.
- Kein Zugriff auf echte Proxmox-Hosts/Router aus der Entwicklungsumgebung; Skripte laufen nur gegen die Stubs.

## Releases
- SemVer, tag `vMAJOR.MINOR.PATCH` (beta until 1.0.0). Add a `## [x.y.z] - date` section to CHANGELOG.md first (New / Changed / Fixed, plain language):
  the release workflow fails without it and uses it as the GitHub release notes.
- Tag only after CI is green on main. Never delete or rewrite published tags/releases.
- Release start: Actions -> release -> Run workflow on `main` with input `tag` (creates tag + signed release). Automated sessions cannot push tags (HTTP 403). The workflow requires a green `ci` run for the commit. The binary version is pinned to the tag (`VERSION`), otherwise the updater refuses it.
- Releases are signed with minisign (`minisign.pub`, secrets `MINISIGN_KEY`/`MINISIGN_PASSWORD`, see `docs/release-signing.md`). Never print or commit the private key.

## Erkenntnisse (Dev-Umgebung & Betrieb)
- Cloud-Session hat **kein** `help.mikrotik.com` (Egress geblockt): RouterOS-Feldnamen nicht raten. Bei Unsicherheit defensiv
  implementieren (z. B. `capHint` matcht Werte statt Feldnamen) und echte Daten über **Topology → Diagnostics** (`GET /api/diagnostics`) vom User holen.
- Vor UI-Tests: `web` bauen (`cd web && npm ci && npm run build`), sonst scheitert `go build` an `go:embed all:dist`.
- Demo/E2E: `go build -o bin/{mtmon,mockros,flowgen} ./cmd/...`, dann `scripts/demo.sh` (UI `http://127.0.0.1:18443`, admin / demo-password-1);
  Playwright-Chromium liegt unter `/opt/pw-browsers/chromium`. Prozesse mit `pkill -x name` beenden (nie `pkill -f demo.sh`, killt die eigene Shell).
- `store.Open(dir)` nimmt ein **Verzeichnis**. Schema-Erweiterungen additiv in `migrateClients`/`migrateAdvice` (Spalte prüfen, dann `ALTER`).
- Lint wie CI: `go install honnef.co/go/tools/cmd/staticcheck@v0.8.1 && staticcheck ./...`, dazu `gofmt -l .` (leer).
- Release ohne lokale Tag-Rechte: PR mergen (squash) → `ci` auf main grün abwarten → `actions_run_trigger run_workflow release.yml ref=main inputs.tag=vX.Y.Z`.
- Standort-Logik (`poller.merge`): Bridge-Hosts auf Ports, an denen ein anderes verwaltetes Gerät als Neighbor hängt (Uplink), zählen nicht;
  Wi-Fi-Clients, die nur der Controller (CAPsMAN) meldet, wandern zum AP, der sie auf einem Edge-Port lernt. DHCP-ARP-Einträge (`permanent`+`dhcp`) beweisen keine Präsenz.
- „Online via“ (`clients.via`) zeigt, welche Beobachtung einen Client online hält – erste Anlaufstelle bei „zeigt online, ist aber weg“.
- Verbindungslisten: `ConnectionsFiltered` (Filter svc/rip/port/cc) – bei neuen Filtern Index/`make scale` prüfen. UI-Polls mit `stop`-Flag nach jedem `await` prüfen (Race beim Client-Wechsel).
