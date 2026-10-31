# Changelog

mtmon follows [Semantic Versioning](https://semver.org): `MAJOR.MINOR.PATCH`.
While the major version is `0`, mtmon is in **beta**: it works and is tested, but minor releases may still change behaviour.
Version `1.0.0` will mean "verified on a range of real MikroTik hardware".

The first section below lists everything mtmon already covers. Later releases add their own section on top
(what is new, changed, fixed). Upgrading is always the same: run the installer again and choose *update*
(a snapshot of the container is taken first).

## [0.20.8] - 2026-10-31

### New
- Wi-Fi score counts disconnects (3 or more per hour cost up to 20 points), ignores roams of clients pinned by an access-list rule and only judges the link rate of clients that actually move data. The score list now names client, access point, band and network.
- Client page: Wi-Fi score card with the breakdown (signal, weak readings, roams, link rate, disconnects) and the score of the last 24 hours (`GET /api/wifi/health/{mac}`).
- Suggestion "router sends debug logs": shows when a device floods mtmon with debug lines and gives the commands to exclude them.
- Related events of one client are now one alert that is updated in place (count, first/last time, last reason). After 15 minutes of quiet it closes with a summary and one closing notification. First story: a client that keeps disconnecting, including the failed DHCP offers after its reconnects.
- Alerts page: any alert type can be turned down to info ("Make this type info") and restored again.
- Automatic updates wait until a release is 24 hours old (a quick follow-up release resets the clock); Settings shows the earliest install time (#189).
- Own internet targets: Settings has a new card where you add up to 10 DNS names or http(s) URLs. They are checked every 30 s over IPv4 and IPv6 separately (a site without an IPv6 address shows "no IPv6 address", not a failure). The Internet card shows the last error per target (NXDOMAIN, timeout, TLS error, HTTP status, "blocked by the router" for a 0.0.0.0 answer). An https server with a self-signed certificate can be added with the certificate check off. Three failed checks in a row raise one warning per target, the next answer an info; this is not counted as a WAN outage.
- Notifications: webhook, ntfy and e-mail can be set up in Settings with a test button per channel, and each channel has its own quiet hours (time window and weekdays). In quiet hours warnings and info are held and sent as one summary afterwards; critical alerts always go out at once.
- `/metrics` now also reports internet probe loss and latency per target, the last speed test (down, up, latency, time) and open alerts by severity.
- Config history: download any snapshot as a readable text file (secrets and runtime values left out) and choose how long snapshots are kept (default 90 days, newest per device always stays). The README explains it is a change log, not a restorable backup.
- Audit log (Settings): who logged in, changed settings, created or used API tokens, added devices, applied fixes or installed updates – with old and new values, kept 180 days. Passwords and request contents are never logged.
- Two-factor login (Settings → Two-factor login): authenticator app (TOTP) with QR code and 10 single-use recovery codes. When on, sign-in asks for the code, and actions that use a router admin login (adding or removing devices, applying fixes) ask for a fresh one. Lost your phone: run `mtmon passwd --reset-2fa` on the server.
- Read-only account (#192): a second login that sees everything operational but cannot change anything and does not see settings, audit log, tokens or 2FA. Managed under Settings.
- `/metrics` has `mtmon_client_wifi_score` for clients with a poor Wi-Fi connection (worst 100), so Grafana or Prometheus can alert on it. (#191)
- Baseline check: the API service enabled on an access point is shown as a tip (mtmon itself uses REST). (#190)
- Each notification channel (webhook, ntfy, e-mail) can get a minimum severity, so a phone channel can be limited to critical alerts.

### Changed
- The overview status line and the "Open warnings" card count only warnings and critical alerts; info notes show as a small "n notes" link, so one warning no longer reads as "9 open alerts". /metrics gets `mtmon_alerts_open_info`.
- Alerts can now carry a message key with parameters (first: traffic spikes); the Alerts page renders them in the language you use, old alerts keep their stored text.
- Planned events: a router scheduler entry that runs rarely (at least hourly interval, with a clock start time) now also marks link-down and configuration-change alerts as info ("planned: scheduler NAME"). Watchdog jobs that run every few minutes never count as planned.
- A Wi-Fi disconnect burst now names the access point (not the CAPsMAN controller) as the subject, and says when a reject rule for the client in the access-list kept matching during the burst – a likely reject loop – with the ways out (keep, signal-range on the unwanted AP, remove the pin) (#211).
- The first update check runs 2 minutes after start and is retried once after 5 minutes; an error is shown only if the retry fails too (#189).
- After an update mtmon checks HTTP, the database and the data sources; if it is still unhealthy after 5 minutes it restarts into the previous version, and one update_failed warning is raised. A database backup is taken before the restart (#189).
- Config history keeps snapshots by age instead of a fixed count of 30; tokens are now stripped from stored configuration too.
- Suggestion "Rules point to deleted interfaces" (#176): the command now names the interface the rule was last set to (read from the router history), and the rules of a device are read again right after its configuration changed, so a fixed rule disappears within minutes. Queue-tree parents are checked too.
- More alerts are now stored as message key plus values (device online/offline, CPU, temperature, watched clients, interface up/down, router log events, WAN quality): they show in the language you pick, old alerts included. (#195)

### Fixed
- No more false "imaginary DNS server" warnings for addresses a managed router owns itself (LAN, link-local, bridge addresses), and old sightings of them disappear at the next advice run.
- The "management service open to everyone" hint now reads the RouterOS 7 "available-from" list correctly, ignores active-session rows, lists each service once, and treats private-only ranges as restricted.
- The "move clients to 5 GHz" hint only lists devices seen on 5 GHz before (or of a typical dual-band class) and skips clients pinned by a Wi-Fi access-list rule.
- Config change alerts now name the real source from the router history ("claude via SSH from 192.168.0.200", "scheduler PPPoE_Reconnect", "script") instead of the script owner, list only changes since the last check, skip scheduler-only runs, no longer count Wi-Fi/interface runtime fields (bound, running, counters, derived channel) as changes, and say "1 entry" correctly (#222).
- German UI: singular/plural counts ("1 offene Warnung"), all alert types in the Type column, the traffic-spike texts (group and "↔ host" parts), always-open "What to do" in suggestions, and a correct time-zone hint (browser zone is taken over at every login while no zone is set).
- Wi-Fi score: the link-rate penalty never applied because rates in bit/s were read as Mbit/s; older stored readings are converted on update.
- Event-log rules created by the setup wizard no longer match debug lines (DHCP packet dumps, WireGuard keepalives). mtmon also drops those lines on arrival unless "Store debug log lines" is switched on in Settings.
- The scheduler time match was an hour off for daily jobs after a daylight-saving change since their start date.
- The reason at the end of a "device unreachable" alert (timeout, refused, login) is now translated too. (#194)

## [0.20.7] - 2026-10-30

### New
- Baseline checks: management services open to every address (warning on APs for the API) and a disabled NTP client (#190)
- Alert webhook can be signed: set alert_webhook_secret and every POST carries X-Mtmon-Signature: sha256=<HMAC of the body> (#193)
- Own synthetic tests on the Internet page: `wan_dns` sets the names resolved through the router, `wan_http` adds HTTP(S) requests (time to the response, status 500+ or no answer counts as a failure). Both are listed per target and stay out of the line loss (#194)
- Path view: the Internet access hop shows the address family of the target and whether the gateway translates the client address (NAT) (#147)

### Changed
- Config history: mtmon polls the routers change history every 2 minutes and snapshots as soon as it changes (daily check stays as safety net); STP counters no longer count as changes; Wi-Fi access-list, provisioning, configuration, security, channel, datapath, steering and CAP settings are part of the snapshot (#222)
- Folded cards keep a one-line summary and a health colour (Traffic, Groups, Internet, Speed, Path, top lists); Groups, Speed and a healthy Internet card start folded; top lists show 8 rows with "show more"; suggestions fold "why and what we saw" by default (#178)
- Long lists are shortened: alerts (30), firewall rules by hits (10) and the configuration history of a device (12) show a "Show N more" button (#178)
- Device and client pages: secondary cards (capabilities, neighbors, config history, router changes, IP history, roaming, blocked attempts) start folded with a one-line summary.
- Path view: the VPN hop now shows tunnel interface, handshake state and tunnel address, and turns to a warning when no WireGuard handshake happened in the last 3 minutes.

### Fixed
- German translations for suggestion texts that were still English: the new baseline checks (open management services, NTP off), DNS redirect and DNS-over-TLS, hairpin traffic, ghost DNS, IPv6 prefix change, Back to Home (#195)
- Phone layout: the Clients filter tabs and the DNS bypass tables no longer run past the screen edge at 375 px; the alert texts for device state, interfaces, new clients and config changes now have a German translation.
- Remaining suggestion texts (DNS server that does not exist, blocked-event evidence, isolation rules) now appear in German with their addresses and counts.

## [0.20.6] - 2026-10-30

### New
- Auto-update install window is a setting (hour of day, default 03:00) (#189)
- Installer aborts on a signed release when `minisign` is missing (override `MTMON_ALLOW_UNVERIFIED=1`); SECURITY.md documents the UDP ingest limits (#192)
- README known limits now state measured scale numbers (100 clients x 30 days, 637 MB, all queries < 120 ms) (#197)
- Client list shows the 24 h Wi-Fi score next to the signal, with the penalty breakdown as tooltip (#191)

### Changed
- `wifi_disconnects` names the client and the AP interface, and says so when the signal is good (#211)
- Advice "rules point to deleted interfaces" now offers a `set ... <field>=<current name>` command first (remove only as alternative) and, for Wi-Fi/CAPsMAN rules, explains the root cause (`create-dynamic-enabled` re-creates interfaces on reconnect; durable fix `create-enabled`, one short Wi-Fi drop) (#176)

### Fixed
- `script_error` no longer fires for the second log line of a failed SSH/telnet command (already reported as `cli_error`) (#175)
- Time zone: saved on every login when not yet set (not only when offsets differ); Settings shows "not set" and a warning instead of a placeholder that looks like a value (#170)

## [0.20.5] - 2026-10-30

### New
- Back to Home default peer is recognised ("BTH default peer (router config / QR code)"): never connected is normal and it is no unused-peer tip; a handshake on it raises a warning (#186).
- Alerts page lists noisy alert types: kinds with 5+ warnings in 7 days that were mostly acknowledged by hand; `GET /api/alerts/noise` (#188 slice 2).
- Config change alerts name who made the latest changes (`by` from `/system/history`) (#188).
- Baseline check (Suggestions, Security): a router with Telnet or FTP enabled gets a warning with the disable commands (#190).
- Auto-update waits while a critical alert is open (#189).
- Prometheus endpoint GET /metrics (device health, interface rates, clients, open alerts, uptime). Use a read-only API token as bearer token (#193).
- Internet page: DNS resolution time and failures through the system resolver (usually the router) for example.com and cloudflare.com, listed per target but kept out of the line-loss totals (#194).
- Wi-Fi connection score 0-100 per client (signal, weak readings, roams per hour, link rate) at GET /api/wifi/health, and a status line at the top of the overview ("Everything OK" or the list of problems) (#191, #195).

### Changed
- Docs: known limits now state the row cap and the one-instance-per-site recommendation, a tested-devices list and a "Report your device" issue template (#197).
- A config change right after mtmon itself applied one (wizard, provisioning, within 30 min) is an info "made through mtmon" instead of a warning (#188).
- Login sessions now survive a restart or update (only a hash is stored); nobody is logged out by an update any more (#189).

### Fixed
- No more `config_changed` warning after a CAP reconnect: references that RouterOS re-resolves (`*1A` to `*2B` or to a `cap-…` interface name) are not counted as a configuration change (#187).
- Database growth and "disk full in N days" use a trend over all stored samples; after restarts the Settings card shows "not enough data yet (n h of samples)" instead of 0 (#196).

## [0.20.4] - 2026-10-29

### New
- New suggestion "Rules point to deleted interfaces": firewall, NAT, mangle, Wi-Fi/CAPsMAN access-list, interface-list members, bridge ports, DHCP servers and routes that name an interface by its internal ID (`*61`) because the interface was removed or re-created (typical after CAP re-provisioning). Such rules never match. Tip, or warning when a drop/reject rule is affected; evidence lists menu, field, comment and action (#176).
- Cards on Overview, Device, Client, Internet, Firewall and Suggestions fold by clicking the title (keyboard: Enter/Space, aria-expanded); state is kept per page in the browser, header buttons collapse/expand all (#178).

### Changed
- Roaming tip (#177) is concrete: shows the mean signal per access point and the last roam; a fixed client on two APs with one ≥ 15 dB stronger gets "pin to the strong AP" with an access-list command, a client seen on 3+ APs is classed as mobile (info only).

### Fixed
- A command typed in an SSH/Winbox session that fails (log line tagged `(ssh-cmd:user@ip)`) is no longer reported as a failing script: it raises its own `cli_error` alert, severity info, "Command from user@ip failed". `script_error` stays a warning for scripts, scheduler and netwatch (#175).
- DHCP offers that fail within seconds after a Wi-Fi connect of the same client (a roam to a weak access point) no longer raise the `dhcp_problem` warning. They become one info note per client, "<MAC> roamed to <AP> and got no DHCP address for N s", with the signal when the log has it. Real DHCP trouble (no roam in front) is unchanged (#174).
- Speed-test start time, window and other clock times now follow a configurable time zone (#170): Settings → Time zone, the browser's zone is stored on first login (the container usually runs in UTC) and applied at the next restart; the running zone is shown next to the time inputs.
- Path view (#147): facts show readable values (`576.5 Mbit/s`, `2.4 GB`, `TCP/443`) and a target without traffic in the range says so instead of showing an empty endpoint.

## [0.20.3] - 2026-10-29

### New
- `GET /api/path?client=&dst=` (#147): the hops from a client to a destination (access link, devices, WAN, network, endpoint) with the facts mtmon already has and a health colour per hop; the weakest hop is flagged. Read-only, no new router queries.
- Client page: a "Path" card shows that route as a chain of hops (horizontal on desktop, vertical on phones) with a health colour per hop, a destination picker and the weakest hop called out (#147).

### Changed
- Internal: the topology map and the path view share one device-tree helper (`deviceTree`), `api.topology` is shorter (#146).
- Internal: more address checks use the shared `addr` package (own IPv6 prefixes, device address check, upstream gateway); the biggest functions are split into steps (`deviceFeaturesApply`, `provision.Probe`, `ruleBlockOrigin`, `mtmon run`) without changing behaviour (#146).
- Internal: the largest files are split by topic (`rules_sec` into firewall/DNS, `mgmt` into services/firewall, `alert` and `poller/manager` into smaller files); no behaviour change (#146).
- Tests: synthetic RouterOS 7.16 / 6.49 REST answers under `internal/provision/testdata/routeros/` and a probe test that reads them (#146).
- Internal: one address classifier (`internal/addr`: private, CGNAT, link-local, multicast, public, ULA, global IPv6) replaces seven private copies in advice, API, poller, provision and store; behaviour is unchanged (#146).
- Internal: the 280-line client merge in the poller is split into small, tested steps (`internal/poller/merge.go`); behaviour is unchanged.
- CI: a non-blocking `health` job lists very complex functions (gocyclo) and copy-pasted blocks (dupl).
- Internet page: new Quick test card with its own provider choice (runs once, never changes the schedule); the long settings form is now a collapsible Scheduled tests card with a one-line summary and sections for Schedule, Test size and limits, Contract and alerts, Providers and licences and Import, each with its own Save and Discard. Open/closed state is remembered, phone friendly.

## [0.20.2] - 2026-10-29

### Fixed
- A router cascaded behind another managed router (default route via that router's address, #155) is no longer treated as an internet WAN: not in the speed-test `wan` field, not an upstream/modem hop, not a depth-0 gateway.
- DNS redirect detection (#140): `redirected` no longer flips when the router's rule counters reset (an active dst-nat/redirect rule is enough). Rules limited to a tunnel interface (back-to-home, WireGuard) now cover VPN clients (`VPN:<ip>`), not LAN clients.
- Suggestion "uses a DNS server that does not exist": the router's own link-local address (announced as DNS server in router advertisements) is no longer reported, also for sightings stored before.
- Configuration history: the change list also hides runtime fields (STP, NTP, pool, scheduler) for snapshots stored before the filter existed, so the old entries disappear at once instead of only for new snapshots.
- DNS bypass and usage lists: any address with the interface id of an interface without MAC (the PPPoE address of the router, also old ones) counts as the router itself, so stored rows from before the update disappear at once.

### Changed
- Outage alert (#157): when several devices fail together and they form one connected group in the topology, the single `devices_unreachable` alert names the device nearest the gateway as cause and lists the others as dependents (their uplink runs through it). The generic shared-cause text stays for unconnected failures.

## [0.20.1] - 2026-10-29

### New
- Speed test provider "M-Lab NDT7": open protocol, the nearest server comes from the M-Lab locate API, download, upload and latency, no extra binary. It is opt-in because M-Lab publishes every result including your public IP address; the settings say so. Cloudflare stays the default.
- Suggestion "<client> uses a DNS server that does not exist": a device with a stale static DNS entry (for example an old IPv6 address) only works because the router redirects DNS and answers for the missing server. mtmon names the client, the missing address and the fix (set DNS to automatic).

### Changed
- Topology: the ISP modem node shows only vendor and IP; the connection modes (PPPoE, DS-Lite, transfer net) sit on the line to the WAN port. The duplicate modem banner above the diagram is gone (it stays on the Internet page).
- Faster first load: only Login and Overview are in the first download, every other page loads when you open it (the main script shrank from 555 kB to 82 kB).
- Ookla stays an opt-in you install yourself: mtmon does not ship the Ookla CLI (its licence allows only personal, non-commercial use on a single personal computer and forbids redistribution). The settings now show this in short next to the checkbox and the README explains why.

### Fixed
- The router's own traffic is no longer booked on a client: a PPPoE or DHCPv6 address that changes (nightly reconnect) is picked up on the next poll instead of the next daily probe, `00:00:00:00:00:00` (PPPoE and tunnel interfaces have no MAC) is never used as a router key, and the DNS-bypass suggestion, the other port-based suggestions and the traffic-spike alert skip or name the router itself instead of listing its WAN-port MAC.
- Phones: button rows and the Topology toolbar wrap, wide tables scroll sideways instead of running off the page, small links and buttons get a bigger tap area. On very wide screens (1900 px and up) the content is centred and wider.
- DNS bypass: clients that ask a public DNS server on port 53 while the router redirects those queries (dst-nat with matched packets, IPv4 and IPv6 checked separately) are marked "redirected – harmless", listed below the real bypasses and no longer counted or alerted. DoT, DoQ, DoH and plain DNS without a redirect still count. The API gets `redirected` per client and resolver and a `redirected` count; the router probe now also reads `/ipv6/firewall/nat`.
- The tip "Old IPv6 prefixes stay in use" counts only prefixes a client was seen with in the last 24 hours (was 14 days), so it disappears on its own once the prefix has been stable for a day.
- A WAN drop right after a router scheduler run that switches the WAN interface (nightly PPPoE reconnect) is an info "planned reconnect (scheduler <name>)" instead of a warning. mtmon reads the scheduler list from the stored configuration snapshot; a drop at any other time stays a warning.
- The imaginary DNS server address of such a redirect is no longer listed as a client (it showed up in "chatty" with tens of thousands of flows). The answers are skipped, the traffic stays with the client that asked.
- Unique-local IPv6 addresses (`fd..`) of a client are remembered like other addresses, so DNS-bypass and usage lists show the client instead of the bare address after the router has forgotten the neighbour entry.
- Tunnel traffic is no longer counted as usage. With DS-Lite (IPv4 in IPv6, very common in Germany) the outer packets between the router and the provider's AFTR repeat every byte of the real client traffic, so totals were doubled and `traffic_spike` blamed the router. IPIP/DS-Lite (protocol 4), 6in4 (41) and the router's own GRE tunnels are now skipped like other transit traffic.
- Config history no longer raises a daily "configuration changed" warning for values that change on their own: bridge/STP state, NTP drift and offset, DHCP pool usage, scheduler next-run and run count. Older stored snapshots are compared with the same filter, so the existing noise disappears at once.

## [0.20.0] - 2026-10-25

### Fixed
- The number of Wi-Fi clients shown for an access point (CAPsMAN) now counts only the clients currently connected through that device, instead of a stale value.
- Suggestions judged against baselines that are still being re-learned now also carry `provisional: true` in the API (next to `learning`).
- Speed tests: when Ookla is selected but its CLI is not installed, mtmon no longer stores a failed Ookla result with every round. The round skips Ookla, "Run now" says why if nothing else is selected, and the settings show a hint.
- The suggested label for a client with a dot inside the hostname, like `TV-Room (Mr.Name)`, was cut at the first dot ("TV Room (Mr"). The domain is now only removed from plain host names (`nas.fritz.box` still gives "nas"). Suggestions are computed on the fly, so existing clients get the corrected suggestion at once; labels you set yourself are never touched.
- Traffic from the router's own addresses (including a dynamic PPPoE address) is now attributed to the router itself instead of staying without a client, and the router is no longer reported as a DNS-bypass client.
- The suggestion "Some clients use encrypted DNS (DNS over HTTPS)" listed the mtmon host itself because of its WAN-quality probe (TCP 443 to 1.1.1.1 / 8.8.8.8 / 9.9.9.9 or your own targets). The advice now skips the probe targets, like the DNS-bypass list already did.

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
