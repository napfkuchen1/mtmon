# Changelog

mtmon follows [Semantic Versioning](https://semver.org): `MAJOR.MINOR.PATCH`.
While the major version is `0`, mtmon is in **beta**: it works and is tested, but minor releases may still change behaviour.
Version `1.0.0` will mean "verified on a range of real MikroTik hardware".

The first section below lists everything mtmon already covers. Later releases add their own section on top
(what is new, changed, fixed). Upgrading is always the same: run the installer again and choose *update*
(a snapshot of the container is taken first).

## [0.19.0] - 2026-10-25

### New
- **Config history** on the device page: once a day (and with "Snapshot now") mtmon reads the router's firewall, NAT, addresses, routes, DHCP, bridge/VLAN, WireGuard, services and users with the read-only login and shows what was added, removed or changed since the last snapshot. Passwords, keys and live counters are never stored; the last 30 snapshots are kept. A change raises a warning alert (info during a maintenance window).
- **Who uses it?** A click on a row in Insights (destinations, ports, ASNs, countries, protocols) or on an application on the Services page opens a list of the clients behind it, busiest first, with a link to each client. It reads the per-client rollups, so it works for every range.
- **Speed tests: start time.** Besides the interval you can set a start time, for example "every 3 hours from 01:30"; tests then land on fixed, known times (plus the random delay).

- **DNS bypass: period, "mark as fixed", ended state.** The DNS-bypass card in Settings has a period (1 hour, 24 hours, 7 days, 30 days). "Mark as fixed" (per client or for all) ignores older traffic; the client shows as "under observation" and is flagged again, with a new alert, only if it bypasses the local DNS again (this can take up to an hour). A bypass without traffic for 24 hours counts as ended and no longer shows as a badge or in the overview count.

- **Topology: the modem / router in front of the gateway** is drawn as its own node above the gateway (vendor, IP, a link to the WAN port labelled with PPPoE / DS-Lite / transfer net), not only as the banner.

### Changed
- **Tables stay put:** the Clients list is sorted by name by default, and the sort you pick is remembered in the browser. A sort by a live value (Now, Usage, Signal) uses a snapshot refreshed every 10 seconds, the Insights rankings keep their order for 30 seconds, and the interfaces of a device are listed by name, so rows no longer jump while you read.
- Speed tests: the latency is now the TCP connect time (median of several), not the time of a full HTTPS request, so it matches the Ookla and ping values much better.
- Speed tests: "auto" history from MySpeed is shown as "MySpeed (Ookla)"; the latency chart has one line per provider; the latency card compares with the average of the same provider. Failed tests show their error text in the table, and the counter reads "142 failed (139 imported)". Raw JSON in imported error texts is reduced to the message.

### Fixed
- Client traffic on a global IPv6 address was invisible: the flows between a LAN client and the internet carry the prefix delegated by the ISP, which is not in `local_nets`, so they were dropped as transit. mtmon now reads the routers' own IPv6 prefixes (`/ipv6/address`, `/ipv6/dhcp-client`, `/ipv6/pool`) and treats them as internal for a week after they were last seen. They are listed under `local_nets` in `/api/diagnostics`. VPN peers (back-to-home) behind masquerade are still attributed incompletely.
- The IPv6 suggestion "old prefixes stay in use" read the wrong lifetimes (`nd prefix default`, which does not apply to the delegated prefix) and recommended changing them. It now reads the lifetimes of the delegated prefix and suggests a static prefix from the provider, a prefix hint, and avoiding reconnects instead.
- The upstream banner flipped between "Modem / router (AVM)" and "ONT / modem (no IP)": an ARP entry that had aged to `stale` was ignored. The modem is now found as long as the entry exists, and the banner and topology share one source.
- Restarting a device no longer raises a warning for its own port: a short link flap inside the reboot grace is one info line ("went down for 6s"), a repeated "up again" message within a minute is dropped, and "rebooted" now says how long ago ("rebooted 31s ago").
- Speed tests against Hetzner failed with `EOF`: their servers reset connections with the default Go user agent. All speed-test requests now send `mtmon/<version>`.
- The WAN-quality probe of mtmon itself (connections to 1.1.1.1, 8.8.8.8, 9.9.9.9 on port 443) was listed as "DoH" in the DNS-bypass list; the probe targets are now ignored.

## [0.18.0] - 2026-10-25

### New
- **Speed tests:** mtmon measures your line from its own host (not from a router). Cloudflare (download, upload, latency) and Hetzner (download) are built in; the official Ookla command-line tool can be used if you install it yourself. Choose the interval, a time window, a random delay, the size of a test and a daily or monthly data budget; tests are skipped while the line is busy. The new **Internet** page shows the history, charts and a table (failed tests are marked as failed, never as 0 Mbit/s), with CSV/JSON export, a "Run now" button and an import of the MySpeed export. Enter your contracted speed to get a warning when several tests in a row stay far below it. The Overview shows the latest result. Switched **off** by default.
- **Modem in front of the router:** the ISP modem (for example a FRITZ!Box) is shown above the topology and on the Internet page with the way the line comes in: PPPoE, DS-Lite, transfer network, double NAT, or "ONT without IP". It is not counted as a client.
- **Clients that bypass your DNS:** mtmon flags clients that use DNS-over-TLS, DNS-over-QUIC, DNS-over-HTTPS or a public DNS server directly. Badge and filter on the Clients page, details and hints (Android, Home Assistant, browser) on the client page, a notice on the Overview. An optional alert (off by default) tells you when a client starts doing it.
- **IPv6 prefix hint:** when your provider changes the IPv6 prefix and the router keeps announcing old ones for weeks, a suggestion explains it, and the client page shows "addresses from old prefixes". No router command is offered yet.
- **Learning period explained:** the Overview banner says why mtmon is learning (update, data reset or reset of the baselines), and `/api/system` reports it.
- **Diagnostics:** `/api/diagnostics` shows how many flows each router interface delivered per direction and the last flows that were dropped (to find missing upload/download of VPN peers), the modem data, and whether the read-only router user may export the configuration (counts only, no text).

### Changed
- The speed test of mtmon itself is labelled "Speed test" in the traffic lists and no longer counts as new destinations or as the host's own traffic spike.
- Overview, Services and the long-range charts are kept warm in the background, so the first page load after a pause is fast.
- The release workflow pins its actions by commit.

### Fixed
- The router event log set-up failed on RouterOS 7.24 because of an unknown log topic; unknown topics are now skipped instead of undoing everything.
- The age of the newest backup was missing on RouterOS 7.24 (the file time comes under a different name).
- Wired devices behind an unmanaged switch were sometimes placed on the uplink of an access point instead of the router.
- The WAN port list is now taken from the interface list "WAN" only (a downstream router's LAN bridge was counted as WAN), access points have no WAN, and old records of the modem are removed.

## [0.17.0] - 2026-10-24

### New
- **Router event log:** with the new feature "Router event log" the router sends system, account, DHCP, Wi-Fi, WireGuard and script lines to mtmon. The device page shows them (kept 7 days) with a level filter and a search. Repeating patterns (login failures, link flaps) raise one alert instead of many.
- **Wi-Fi history:** signal and rate per client are kept per hour. The client page shows roaming and band changes, the AP page shows the clients per band, and the "weak signal" suggestion now looks at the last 24 hours instead of a single reading.
- **Client groups:** put clients into groups (for example *IoT* or *Guests*). A group can switch off new-device alerts, raise the limit for "many connections", show traffic spikes as info, ignore a weak Wi-Fi signal and watch its members by default. Ungrouped clients get a group suggested. The Clients page can be filtered by group.
- **Watched clients:** mark an important client (for example the intercom) and get an alert when it stays offline, and an info when it is back.
- **Internet quality:** mtmon measures loss and latency to a few internet targets from its own host. One warning when the connection is poor for a whole window, one info when it is good again. The Overview shows the current values. Probing can be switched off.
- **More suggestions:** age of the newest backup or export per router, pending RouterOS update, CAPsMAN and AP running different versions, expiring service certificate, idle VPN peers.
- **mtmon looks after itself:** memory, CPU, free disk space and database growth are shown under System. A suggestion warns before the disk is full.
- **Learning period:** after a data reset or an update that changes detection, mtmon marks its suggestions as provisional for 24 hours and rebuilds its learned levels from scratch. A button in Settings starts the period by hand.

### Changed
- **Alerts:** one story per device reboot. The link-down of the port behind it is folded into the device's alert, which is closed as info when the device is back. Spikes that repeat at the same time of day (a speed test) are info, not warning.
- **Suggestions:** fewer repeats. Late replies from blocked origins are one suggestion, recurring spikes are not repeated.
- **New-client alerts:** choose whether unknown devices, all devices or none raise an alert.
- **Setup wizard:** the traffic-flow snippet can be run twice, and the `www-ssl` allow list is no longer replaced.
- Fewer duplicated views in the UI; README and screenshots are current (`make screenshots`).

### Fixed
- Wired clients behind an unmanaged switch on an uplink port were missing or placed on the wrong device.
- The upstream modem on the WAN port was shown as a LAN client.
- The rate limit now counts the real client behind a reverse proxy instead of locking everyone out together.
- The client page crashed on duplicate rows in the connections table.
- IPv6 and bare-address tunnel traffic is attributed to the VPN client.
- Top lists and suggestions no longer lead to an "unknown client".
- The device API never returns `null` lists, zero times or a stale "managed" warning.
- Services, Overview and the firewall summary are much faster on large databases.

## [0.16.0] - 2026-10-24

### New
- **Maintenance window per device:** on the device page you can start a window of 30 minutes, 2 hours or 8 hours. While it is open, the device's offline alerts and the link-down alerts of the port that leads to it are recorded as info instead of warning/critical. Devices in maintenance are not part of the "cannot reach N devices" group alert.
- **VPN peers are clients:** WireGuard peers, including Back to Home, appear on the Clients page with their name and a VPN icon. Back to Home peers are named after the user in the peer comment. Peers that route a whole network (site-to-site) are not shown as clients.
- **Topology root:** the tree starts at the gateway (the router with NAT and DHCP). A "Root" selector on the Topology page overrides the choice.

### Changed
- **Suggestions:** high but steady memory use (for example on small access points) is only an info note. It stays a warning when memory is rising or less than 16 MiB stay free.
- **Suggestions:** `fw-nolog` recognises isolation rules by their structure (a VLAN or interface list blocked from the private ranges), not only by words in the comment, and lists the unlogged drop rules that match packets right now first.
- **Suggestions:** `chatty` recognises VPN tunnels by the interface type, whatever the interface is called, and skips VPN peers.
- **Setup wizard (Router setup and Add device):** both use the same commands now. Existing objects are skipped, so the commands can be run twice. The `mtmon` user gets a random password instead of a fixed one, and the `www-ssl` command uses the mtmon address and warns that it replaces the existing list.
- The Go toolchain is 1.26.10 (security fixes in the standard library).

### Fixed
- The Topology page API never returns `null` lists.

## [0.15.2] - 2026-10-24

### Fixed
- **Suggestions after setup:** `fw-nolog` and `fasttrack` read the router rules from a stored probe that was taken before the setup, so rules that had just been switched to logging were still reported. The device is probed again after Auto setup and after a feature change, a manual refresh reads fresh data, and the evidence says when the router rules were read.
- **Topology:** every device was shown as the uplink of every other. A link is now only drawn between two devices when no third device sits between them, and the arrows follow the tree from the gateway down.
- **Firewall log prefixes:** the same `MTM-xx` prefix on two devices (an accept rule on one, a drop rule on the other) was mixed up in "blocked origins" and the top lists. Rules are now matched by device and prefix.
- **Setup wizard (Router setup):** the commands use the address mtmon detected instead of the public host name (RouterOS does not accept a name in `src-address`/`dst-address`), `www-ssl` uses `available-from=` (`address=` is deprecated since RouterOS 7.24), and the text says that flows and syslog leave the router through `output`, so the `forward` rule is only needed on a router in between.
- **Deep links** such as `/devices` opened the overview; they are forwarded to `#/devices`.

### Changed
- **Suggestions:** `chatty` skips VPN peers (WireGuard and Back to Home): a full-tunnel phone sends all its traffic from one address. `fw-nolog` names isolation rules that match packets but log nothing, because that is where you want to see who tries to get through.
- **Alerts:** a reboot is no longer an outage. A device that answers again within 90 seconds raises one info alert ("updated 7.24.2 → 7.24.4 (reboot, 46s)" or "rebooted") instead of critical offline/online, and the link-down of its neighbour is held back and dropped in that case. A link that stays down is still reported after 90 seconds.
- **Site:** the site field of the add-device wizard and the edit dialog suggests the sites that already exist.

## [0.15.1] - 2026-10-16

### Fixed
- **Rule regression tip** no longer fires when a second accept rule in front of the drop rule does match (for example the WireGuard exception next to the real accept rule). It needs at least 50 drops in 24 hours and names the rule comments.
- **Firewall log:** destination-NAT log lines (`NAT src->(dst->dst)`) were counted as "unreadable"; they are parsed now.
- **Firewall page** opened empty for seconds on long ranges; the summary is cached for 30 seconds.
- **API:** unknown `/api/...` paths answer `404` as JSON instead of the UI page with status 200.

### Changed
- **Blocked origins:** packets whose sender is your own public address get their own card (hairpin, loop or spoofing) instead of "late replies". Every address is checked for LAN contact before a raw-drop command is suggested; the country card shows no commands.
- **Suggestions:** `chatty` ignores the routers' own addresses (WireGuard, IPv6) and servers such as Home Assistant or proxies; `band-24` skips smart-home devices (ESP, Shelly, Bosch/Neff …) and weak signal of such devices is only a note; `fw-nolog` shows the rule counters and, for isolation rules, suggests a rate-limited log-only rule instead of logging every drop.
- **Alerts:** devices that fail together are one alert ("mtmon cannot reach N devices"), and the cause (timeout or refused) is named. A WAN drop at the same time on three earlier days is a note instead of a warning. A traffic spike needs 100 Mbit/s at once or 5 minutes of duration, is only a note when one known client causes it, and alerts at most once per client and day.
- **API tokens:** `GET /api/me` with a token returns its name and `"scope":"read"`; the README and the token list say that tokens are read-only.
- Router probe also reads IPv6 addresses.

## [0.15.0] - 2026-10-09

### Added
- **Reset (Settings → Reset):** *Reset history* empties traffic, clients, alerts, firewall log, metrics and dismissed suggestions so mtmon starts clean; your devices, settings, API tokens and service rules stay, and you can keep clients that have a label. *Full reset* also removes the devices added in the web UI. Both need you to type `RESET`; an Excel list of your clients can be saved first.

### Changed
- **Sidebar:** the most used pages come first (Overview, Live, Clients, Devices, Alerts, Suggestions, Firewall). Services, Insights and Topology sit below a divider. *New devices* moved to the Clients page (button, and its counter now shows on *Clients*).

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
