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
