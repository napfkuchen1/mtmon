# Changelog

mtmon follows [Semantic Versioning](https://semver.org): `MAJOR.MINOR.PATCH`.
While the major version is `0`, mtmon is in **beta**: it works and is tested, but minor releases may still change behaviour.
Version `1.0.0` will mean "verified on a range of real MikroTik hardware".

The first section below lists everything mtmon already covers. Later releases add their own section on top
(what is new, changed, fixed). Upgrading is always the same: run the installer again and choose *update*
(a snapshot of the container is taken first).

## [0.14.0] - 2026-10-09

### Added
- **Alerts page:** buttons *Acknowledge all* and *Clear alerts* (removes acknowledged alerts after a confirmation), and the exact date and time instead of "1h ago" (the relative time moves to the tooltip).
- **Excel export:** every CSV export (top lists, connections of a client, client list, alerts) is also available as a readable `.xlsx` workbook with a bold header row, filter, column widths, local date/time and sizes shown as KB/MB/GB.
- **Rule regression tip:** mtmon warns when an accept rule never matches (counter stays at 0) while a drop rule right behind it blocks the same port again and again. Counters are read on the hourly device probe, so an existing device shows the tip after its next probe.
- **Firewall page:** a skeleton while it loads, and a "Why are N lines unreadable?" section with examples (other log topics such as login messages are harmless and now explained).
- **Config:** `alert_ignore` (alert kinds or `kind:subject` to mute), `port_scan_ports` (default 100) and `port_scan_hosts` (default 200) tune the port-scan alert.

### Changed
- **Traffic spike alert** now names the client and the remote host that cause the spike, **port scan alert** names the client, the target and the ports, and both say when a WAN flap just happened (likely a side effect).
- **Firewall page** no longer counts mtmon's own access (rules whose label contains "mtmon") in the totals; a toggle shows it, and a hint offers the RouterOS command to switch that rule's logging off.
- **IPv6:** addresses are merged into the client they belong to, using the router's IPv6 neighbor table and the MAC embedded in SLAAC (EUI-64) addresses, so Insights no longer lists every `fe80::`/`fd00::` address on its own. Older flows keep the address they were stored with.
- **Suggestions page:** the text uses the full width and long RouterOS commands wrap instead of scrolling.

## [0.13.4] - 2026-10-09

### Fixed
- **DNS traffic counted twice:** when the router redirects DNS (dst-nat), MikroTik exports the same query twice (original and redirected target). mtmon now counts it once.

### Changed
- **WAN flaps bundled:** when an uplink (e.g. `pppoe-out1`) goes down and up repeatedly, mtmon raises one down alert and one up alert, and the up alert states how long the outage lasted.

## [0.13.3] - 2026-10-09

### Fixed
- **False roaming:** with a CAPsMAN controller, a Wi-Fi client that stayed on one access point could be counted as "roaming" between the controller and the access point whenever the access point did not list it for a single poll. mtmon now keeps the access point the client was last seen on, and only counts a roam when the client really moves to a different one.

## [0.13.2] - 2026-10-09

### Fixed
- **DNS bypass tip:** no longer a false alarm when the router already redirects port-53 traffic to itself. In that case mtmon shows an info card instead of a security tip. Encrypted DNS (DoT, port 853) is judged separately.
- **Blocked-origin tip:** blocked packets from an address that your own devices talked to are now shown as late replies after a connection loss, not as a scan, and mtmon no longer suggests a raw drop rule for them. When one address causes almost all blocks, the tip names that address instead of the whole provider.

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
