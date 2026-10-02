# Changelog

All notable changes to mtmon are listed here, newest first, in plain language.
mtmon follows [Semantic Versioning](https://semver.org): `MAJOR.MINOR.PATCH`.
While the major version is `0`, mtmon is in **beta**: it works and is tested, but minor releases may still change behaviour.
Version `1.0.0` will mean "verified on a range of real MikroTik hardware".

Every release lists what is **new**, what **changed**, and what was **fixed**. Upgrading is always the same:
run the installer again and choose *update* (a snapshot of the container is taken first).

## [0.8.0] - 2026-10-02

### New
- **Signed releases.** Every release is now signed by the maintainer (minisign, file `*.tar.gz.minisig`). This proves an
  update really comes from the mtmon project and was not swapped on the way. The checksum alone only proves that the
  download is intact.
  - The in-app updater (*Settings → Updates*) only installs a release whose signature matches the mtmon key built into
    the program. The signature is tied to the exact version, so an older signed file cannot be passed off as a new one.
  - The installer checks the signature as well when `minisign` is installed (`apt install minisign`); otherwise it
    says so and falls back to the checksum.
  - You can verify a download by hand, see `docs/release-signing.md`.

### Changed
- Updates from the web UI now refuse releases without a valid signature. Updating from 0.7.x to 0.8.0 works as before
  (0.7.x does not check signatures); from 0.8.0 on, every update is verified.
- Internal: the Go module path is now `github.com/napfkuchen1/mtmon`. No effect on installed systems.

### Fixed
- README examples no longer mention versions that do not exist (v2.x).

## [0.7.0] - 2026-10-02

### New
- **Updates from the web UI.** The sidebar footer shows the installed version (and a badge when a newer release exists).
  *Settings → Updates* can check GitHub (every 6 hours), show the release notes and install the new version with one click.
  Three modes: *Off*, *Notify only* (default) and *Auto-install nightly* (03:00-04:00). Downloads are checksum-verified and
  mtmon rolls back by itself if the new version does not start. Existing installations need one normal installer
  *update* first so the small helper for this feature gets installed.
- **Suggestions tab.** mtmon looks at its own data and proposes improvements, each with the reason, the numbers behind it
  and copy-ready RouterOS commands: high RAM/CPU, weak or 2.4 GHz-stuck Wi-Fi clients, insecure protocols (Telnet, FTP, SMB, RDP),
  clients bypassing your DNS, one client using most of the traffic, missing flow/syslog setup, unnamed devices, and more.
  You can hide or snooze a suggestion; the sidebar shows how many warnings there are.
- **Apps per device.** The client page lists which apps a device uses (YouTube, Spotify, Netflix, Teams, WhatsApp ...)
  with an "active now" marker, and the Services page has an *Apps* view. Detection uses the names routers see in DNS,
  so it cannot see apps behind encrypted DNS (DoH).
- **Clean up.** *Clients → Clean up…* removes devices that were not seen for 30/90/180/365 days, with a preview first.
  Devices with a label can be kept.
- **Names in the Firewall tab.** Blocked sources and destinations now show a readable name (device name or DNS name,
  plus organisation and country) above the IP address.

### Changed
- Release titles are now short ("mtmon v0.7.0").

## [0.6.0] - 2026-10-02

### New
- **English by default, German on request.** The whole interface is now in standard English. A language switch
  (English / Deutsch) sits at the bottom of the sidebar and remembers your choice. Dates, numbers and the texts
  that mtmon shows about your routers (setup steps, warnings, rollback messages) follow the chosen language.

### Fixed
- **Routers and access points no longer show up as clients.** A device could appear in the client list (and trigger a
  "new client" notice) when another device saw it on the network. mtmon now recognises the addresses of its own managed
  devices and ignores them; entries created by earlier versions are cleaned up automatically.
- The manual certificate commands shown under Settings now work (they create a small certificate authority first,
  which RouterOS requires).

### Changed
- Screenshots in the README are English.

## [0.5.0] - 2026-10-02

First public beta. This release resets the version numbering: the earlier internal builds were renumbered
`0.1.0` to `0.4.0` (listed below); this release adds the changes shown here.

### New
- **Requirements checklist on the "Add device" page.** Before you connect a router it now shows what the router needs
  (HTTPS service with certificate, firewall access for mtmon, admin account, UDP reachability), with ready-to-copy
  RouterOS commands. Your router address is filled in automatically; `<...>` marks what you still have to replace.
- **Installer redesign.** The console installer has a clear banner, a dark theme with cyan accents in the dialogs,
  grouped steps, a plan box and a final summary with the next steps (including the DHCP reservation reminder).
- **Changelog and release notes.** Every release now ships with a human-readable summary taken from this file.

### Changed
- Version numbering now starts at `0.5.0` and follows Semantic Versioning.
- When a router's HTTPS service has no certificate, the error now says exactly that and how to fix it
  instead of showing a raw "tls: handshake failure".

### Fixed
- Setting up firewall logging no longer fails on RouterOS because of the log-action name
  (RouterOS only accepts letters and numbers; the action is now called `mtmonsyslog`).
- Setting up firewall logging no longer tries to modify dynamic firewall rules, which RouterOS does not allow.
  If anything in a setup fails, mtmon still rolls back every change it made.

### Known limits
- Tested against a RouterOS simulator and a first set of real devices (hEX S, wAP ax, Audience on RouterOS 7.24).
  Other models may behave differently. Please report issues.

## [0.4.0] - 2026-10-01

### Changed
- When a router's HTTPS service has no certificate, the error now says exactly that and how to fix it
  instead of a raw "tls: handshake failure".

## [0.3.0] - 2026-10-01

### Fixed
- Enabling firewall logging no longer tries to change dynamic firewall rules (RouterOS refuses this).
  If any setup step fails, everything done so far is rolled back automatically.

## [0.2.0] - 2026-10-01

### Fixed
- The firewall log action got a name RouterOS refuses (letters and numbers only); it is now called `mtmonsyslog`.

## [0.1.0] - 2026-10-01

First complete build (internal, published here for the record): realtime monitoring of MikroTik routers and access points,
add-device wizard with automatic setup and full rollback (read-only user, Traffic Flow, firewall logging), firewall verdicts,
service classification, GeoIP and vendor names, and the one-command installer for Proxmox VE.
