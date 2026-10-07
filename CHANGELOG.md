# Changelog

mtmon follows [Semantic Versioning](https://semver.org): `MAJOR.MINOR.PATCH`.
While the major version is `0`, mtmon is in **beta**: it works and is tested, but minor releases may still change behaviour.
Version `1.0.0` will mean "verified on a range of real MikroTik hardware".

The first section below lists everything mtmon already covers. Later releases add their own section on top
(what is new, changed, fixed). Upgrading is always the same: run the installer again and choose *update*
(a snapshot of the container is taken first).

## [0.13.1] - 2026-10-07

### Included
- **Monitoring:** realtime view of MikroTik routers and access points (RouterOS 7, REST API, Traffic Flow/IPFIX, syslog), trend lines, world map, period comparison, drill-down from a client, Ctrl+K search.
- **Add-device wizard:** automatic setup (read-only user, Traffic Flow, firewall logging, FastTrack off) with full rollback; edit and remove devices later.
- **Router features per device:** flow, firewall logs, allow-log, log-new, FastTrack, each with a preview and an exact undo.
- **Suggestions:** weak Wi-Fi, high RAM/CPU, insecure protocols, DNS bypass, dominant client, missing flow/syslog, unnamed devices, chatty clients and more; snooze or hide; *Fix automatically* where mtmon can resolve it itself.
- **Traffic insight:** firewall verdicts, service and app classification, per-device apps, GeoIP/ASN/vendor enrichment with one-click setup, optional reverse DNS.
- **Clients and devices:** new-devices page with naming patterns, client location (Wi-Fi vs. router, "Online via"), cleanup of offline clients with CSV export, device icons and custom icons.
- **Topology:** ports, SSID colours, groups, uplink colours, diagnostics download.
- **Alerts:** webhook, ntfy and e-mail for device, interface, traffic, DNS and port-scan events.
- **Interface:** English by default with German UI, collapsible sidebar and mobile menu, chart style, density and colour styles.
- **Deployment:** installer for Proxmox, reverse proxy / nginx support, port setting, read-only API tokens.
- **Updates and security:** signed releases (minisign) with build provenance, in-app updater (Off / Notify / Auto-install nightly), origin checks, sessions, CSP, SSRF-safe wizard, same-host redirects, `govulncheck` in CI, `SECURITY.md`.
- **Known limits:** tested on hEX S, wAP ax, Audience with RouterOS 7.24; other models may differ.
