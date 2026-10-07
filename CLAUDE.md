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

## Arbeitsablauf (gilt lokal und in der Cloud)
Ablauf immer gleich: **Branch → kleine Conventional Commits → Tests/Lint → PR → Auto-Merge (Squash) → `ci` auf `main` grün → Release → CHANGELOG-Eintrag vorher.**
- **Vor jedem Push:** `make test lint` (lint = staticcheck v0.8.1 + gofmt, siehe unten). Nach UI-Änderungen: `cd web && npm run build`, bei Layout/Verhalten kurz im Browser prüfen (Demo + Playwright, siehe Erkenntnisse).
- **GitHub-Regeln (in den Repo-Einstellungen, nicht im Code):** Ruleset auf `main` verlangt den Check **`test`** (`vuln` ist bewusst nicht Pflicht), „Allow auto-merge“ ist an. Merge-Art immer **Squash**. Nie direkt auf `main` pushen.
- **PR + Merge:** lokal mit `gh pr create` → `gh pr merge --auto --squash`; in der Cloud-Session gibt es kein `gh`, dort die GitHub-MCP-Tools (`create_pull_request`, `enable_pr_auto_merge` mit `SQUASH`, `subscribe_pr_activity`). PR-Text: Summary / Test plan / Rollback. Keine Modellnamen in Commits/PRs.
- **Release (Minor/Patch, Beta bis 1.0):** 1) `## [x.y.z] - Datum` in `CHANGELOG.md` (New/Changed/Fixed, einfache Sprache) 2) PR gemergt, `ci` auf dem **neuesten** `main`-Commit grün 3) lokal `gh workflow run release.yml -f tag=vX.Y.Z --ref main`, Cloud: `actions_run_trigger run_workflow release.yml ref=main inputs.tag=vX.Y.Z` 4) Release prüfen (Assets `.tar.gz`, `.sha256`, `.minisig`). Der Workflow verweigert ohne grünes `ci` oder ohne CHANGELOG-Eintrag. Tags nie selbst löschen/umschreiben. Reine Doku-/Test-PRs brauchen kein Release.
- **Roter `main`-CI:** Job-Log lesen; nur bei klarem Flake **einmal** `rerun_failed_jobs`, sonst Ursache beheben; nie Tests überspringen. Kein Release auf rotem `ci`.
- **Nach einem gemergten PR** den Branch frisch von `main` neu aufsetzen (`git checkout -B <branch> origin/main`), nie weitere Commits auf die alte Historie stapeln.
- **Echte Router/Proxmox gibt es in der Entwicklung nicht** (nur Mock + Demo). Verhalten gegen echte Geräte holt man über **Topology → Diagnostics** bzw. Screenshots des Nutzers; neue Router-Schreibpfade immer erst mit Vorschau/`dry_run` und Test gegen `mockros`.
- **Sprache:** Antworten an den Nutzer deutsch, kurz, in Schritten; Code, Commits, CHANGELOG und UI-Texte (EN) mit deutscher Übersetzung in `web/src/lib/i18n/` (neue Texte in `v08_ui.js` o. Ä. ergänzen).

## Live-Instanz lesen (NUR lokale Sessions)
Eine lokale Claude-Session kann die echte mtmon-Instanz des Nutzers **nur lesen** – mit einem API-Token (Einstellungen → API-Tokens, nur GET).
- **Variablen:** `MTMON_URL` (z. B. `https://192.168.0.248:8443`, ohne Pfad) und `MTMON_TOKEN` (beginnt mit `mtm_`). Nur als Umgebungsvariablen des lokalen Rechners, **nie** in `CLAUDE.md`, Code, Commits, PRs oder Logs. Ist `MTMON_TOKEN` leer, gibt es keine Live-Abfragen – nicht raten, nicht nach dem Token fragen, stattdessen den Nutzer um Screenshot bzw. **Topology → Diagnostics** bitten.
- **Cloud-Sessions:** Die Variablen sind dort **nicht gesetzt** und die Instanz (private LAN-Adresse) ist von dort nicht erreichbar. Den Token nie in die Secrets einer Cloud-Umgebung legen. Dieser Abschnitt gilt dort nicht.
- **Aufruf** (mtmon nutzt standardmäßig ein selbstsigniertes Zertifikat, daher `-k` im LAN):
  `curl -sk -H "Authorization: Bearer $MTMON_TOKEN" "$MTMON_URL/api/diagnostics"`
- **Nützliche Endpunkte (alle GET):** `/api/diagnostics` (Rohdaten der Router: WLAN, Bridge-Hosts, ARP, DHCP, Neighbors, Clients samt `via`) · `/api/topology` · `/api/clients?range=1h` · `/api/clients/{mac}` · `/api/devices` · `/api/overview?range=24h` · `/api/system` · `/api/suggestions`.
- **Grenzen:** Der Token kann nichts ändern (jedes POST/PUT/DELETE liefert 401) und nicht an `/api/tokens` oder `/api/access`. Router-Änderungen laufen nur über die UI mit Admin-Login. Antworten enthalten MACs, IPs und Hostnamen: lokal auswerten, nicht in Commits, Issues oder PR-Texte kopieren.
- **Widerrufen:** Pro Rechner einen eigenen, benannten Token anlegen; bei Verdacht in den Einstellungen widerrufen (wirkt sofort).

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
- Keine Flaggen-Emojis (Windows zeigt „DE“ als Buchstaben): `lib/Flag.svelte` (SVG aus `country-flag-icons`, lazy je Land) + `countryName()` in `format.js`.
- Enrichment-Download läuft in-App (`enrich.Fetch`, feste Quellen, atomar, Hot-Reload); `deploy/update-geo.sh` bleibt der Fallback. Neue UI-Bausteine: `Avatar`/`DeviceIcon` (`deviceKind` in `format.js`), `Spark`, `WorldMap` (world-atlas, lazy), `Empty`; Nutzerpräferenzen `density`/`chart` in `state.svelte.js` (localStorage).
- Topology-Layout ist reines Frontend (`pages/Topology.svelte`): BFS-Baum über Neighbor-Kanten, Gruppen-Container, Umbruch nach Breite – keine festen Pixelbreiten.
- UI-Einstellungen, die der Dienst-User speichern können muss, gehören in `meta` (`GetMeta/SetMeta`, Beispiele: `reverse_dns`, `public_url`, `listen_port`), **nie** in `config.json` (unter /etc/mtmon nicht schreibbar); `applyStoredSettings` (main.go) legt sie beim Start über die Config.
- Reverse-Proxy: Origin-Prüfung läuft über `Server.originOK` (Host, `X-Forwarded-Host` nur von privaten Peers, `public_url`). Neue Origin-Checks nie direkt mit `sameOrigin(…, r.Host)`.
- API-Tokens: nur GET, nie auf `/api/tokens`/`/api/access`; Klartext nur einmal bei der Erstellung, gespeichert wird der SHA-256.
- Layout: Seitenleiste hat Rail- (Desktop) und Drawer-Modus (<860px); Live-Tabellen mit `table.fixed` + `<colgroup>`, damit nichts springt. Responsiv-Check: Playwright-Skript über alle Routen bei 390/820/1400 px auf `scrollWidth > innerWidth`.
- Tests, die im Hintergrund etwas starten (z. B. `update apply`), müssen auf dessen Ende warten, bevor `t.TempDir()` aufgeräumt wird – sonst sporadisch „directory not empty“ nur auf CI (so geschehen bei `TestUpdateFlow`). Ein roter `main`-CI-Lauf: Job-Log lesen, einmal `rerun_failed_jobs`, Ursache härten, nie Test überspringen.
- Router-Features pro Gerät: `provision/features.go` (`ApplyFeatures`, `PlanFeatures`, `FeatureOf` ordnet Manifest-Einträge Funktionen zu) + `api/features.go` (GET Zustand, POST mit `dry_run`). Schreibt am Router nur nach expliziter Aktion mit Admin-Login (nie gespeichert), jeder Schritt im Manifest, „aus“ = `Rollback` der Einträge dieser Funktion; `Managed` des Geräts folgt den noch angewendeten Manifest-Einträgen. Neue Funktion = Eintrag in `FeatureKeys`, `stepKeys`, `FeatureOf`, `featureText` + Test in `TestDeviceFeaturesOnOff`.
