# Changelog

All notable changes to mtmon are listed here, newest first, in plain language.
mtmon follows [Semantic Versioning](https://semver.org): `MAJOR.MINOR.PATCH`.
While the major version is `0`, mtmon is in **beta**: it works and is tested, but minor releases may still change behaviour.
Version `1.0.0` will mean "verified on a range of real MikroTik hardware".

Every release lists what is **new**, what **changed**, and what was **fixed**. Upgrading is always the same:
run the installer again and choose *update* (a snapshot of the container is taken first).

## [0.10.0] - 2026-10-03

### New
- **Set up GeoIP/ASN/vendor data with one click** (Settings → Enrichment): mtmon downloads the free country, provider and
  manufacturer lists itself and uses them right away. The reverse-DNS fallback is a switch now.
- **Device icons** (phone, laptop, TV, printer, speaker, camera, server, smart-home …), guessed from name and vendor, in the
  client list, client page, topology, search and the new-devices list.
- **Trend lines on the Overview** (download/upload) and a **world map** next to the top countries.
- **Topology redesigned:** devices are grouped per router/access point, groups can be collapsed, the layout wraps to the window
  width (no sideways scrolling), hovering a device highlights its path to the router, and links that only exist "through"
  another device are no longer drawn.
- **Chart style:** smooth curves, peak values and night shading. Settings → Appearance switches back to the classic look.
- **Density switch** (compact / comfortable) in the header and in Settings.
- Friendlier empty screens that say what to do next (for example "Set up in Settings" when no GeoIP data is installed).

### Fixed
- Download/upload change badges no longer show absurd numbers when the previous period had almost no traffic.

## [0.9.2] - 2026-10-03

### Fixed
- **Countries showed doubled codes ("DEDE") on Windows.** Windows cannot display flag emoji, so they appeared as letters.
  Countries now show a real flag image, the country name in your language and the two-letter code.

## [0.9.1] - 2026-10-03

### Fixed
- **Wi-Fi devices were listed under the router instead of the access point they use** (with CAPsMAN the router lists all
  Wi-Fi clients). A client that an access point sees on its own radio port is now shown on that access point; the signal
  and network name still come from the registration.
- **Devices that had left stayed "online".** DHCP-created ARP entries (they live as long as the lease) no longer count as
  proof that a device is present, and data from a device that stops answering is ignored after 3 minutes.
  The client page shows **"Online via"** – what mtmon currently sees that keeps the device online.
- **Client → Connections** also lists LAN connections other devices opened *to* this client, and no longer mixes in rows of
  the previously opened client when you switch quickly.
- **New devices:** the "unnamed" list now only contains devices with nothing but a MAC address, and a name suggestion is
  only shown when it adds something to the name the device already has.

### New
- **Topology → Diagnostics:** downloads the raw Wi-Fi/bridge/ARP/DHCP tables mtmon received from your devices, to
  find out why a device is shown in the wrong place.

## [0.9.0] - 2026-10-03

### New
- **"New devices" page.** Devices that appear on the network wait there until you have looked at them. mtmon proposes
  a name (from the hostname, or from the vendor if there is none), you accept it with one click, type your own, or fill
  several selected devices at once with a pattern like `Kids-{n}`. A counter in the menu shows how many are waiting.
  Devices that mtmon already knows when you update count as seen, so the list starts empty.
- **Search everywhere (Ctrl+K).** Jump to any client, router/AP, service or page by typing a few letters.
- **Compare with the previous period.** Download, upload and flows on the Overview show how much they changed
  compared with the equally long period before (for example "+12 %").
- **Drill down from a client.** Click a destination, service, port or country in a client's top lists to see exactly
  those connections (also in the CSV export).
- **Clean up offline clients.** Besides "not seen for N days" you can now remove everything that is offline right now,
  optionally only devices without any traffic in the last 7 days. A CSV download of all clients is one click away
  in the dialog as a safety copy.
- **Topology:** ports on the links between routers/APs, wired clients show their port, Wi-Fi clients are coloured and
  sorted by network name (SSID).
- **Better names:** clients without a DHCP host-name get a name from LLDP/MNDP, static DNS or reverse DNS; otherwise the
  list shows "Vendor AB:CD" instead of a bare MAC address.

### Changed
- Suggestions are easier to read: a coloured edge shows the severity, "Problem" and "What to do" are separate blocks.

### Fixed
- **Topology showed wired devices on the wrong access point.** A device cabled to the router could appear connected to an
  AP, because the AP also "sees" it through its uplink. Entries learned on a link to another of your devices are ignored now.
  This needs LLDP/MNDP neighbor discovery between your devices (on by default in RouterOS).

## [0.8.2] - 2026-10-03

### Security
- **Stricter origin check for the live view and for changes.** The check that only lets your own mtmon page talk to
  mtmon compared only the end of the address, so a look-alike address such as `evilmtmon.lan` could pass for
  `mtmon.lan`. It now compares the exact address. The session cookie (`SameSite=Strict`) already blocked this in
  practice; this closes the gap on the server side as well.
- **Sessions end for good after 7 days** even if the page is used continuously (idle sessions still end after
  12 hours). Unused sessions and old failed-login records are cleaned up instead of piling up in memory.
- **The live connection accepts only tiny messages** (it only ever needs "pause"/"resume").
- **Tighter browser policy (CSP):** the UI may only load from mtmon itself, cannot be embedded in other pages, and
  can only connect back to mtmon (before, it could open connections to any address).
- **The setup wizard now checks the address it really connects to.** It only accepts private addresses (so it
  cannot be used to probe the internet), but the check and the connection used two separate name lookups, which a
  prepared DNS server could exploit. Now the name is looked up once, every answer is checked, and exactly that address
  is used. The list of blocked cloud "metadata" addresses is longer, too.
- **Routers can no longer redirect mtmon elsewhere.** mtmon only follows redirects that stay on the same address and
  protocol, so a device cannot send it to another host or to an unencrypted page.

### Fixed
- **Interface alerts for ports whose name starts with "lo".** A port called `lounge`, `lo-bridge` or `lobby-ap` never
  raised an "interface down" alert, because every name starting with `lo` was treated as the loopback interface. Only
  the real loopback `lo` is ignored now.
- Saving the configuration without a file path no longer leaves a stray `.tmp` file behind; it reports an error instead.

### Changed
- Internal clean-up: test-only code moved out of the program, one unused function removed, two libraries updated
  (`go-humanize`, `go-strftime`).
- Alerts (webhook, ntfy, e-mail, device/interface/traffic/DNS/port-scan detection) and the configuration code now have
  automated tests (about 91 % and 95 % of the code), which is how the two fixes above were found.

## [0.8.1] - 2026-10-02

### Security
- **Built with a current Go toolchain (1.26.8).** Earlier releases (up to 0.8.0) were built with Go 1.26.0, whose standard
  library has 19 publicly known vulnerabilities that mtmon's code can reach (TLS/HTTPS server and client, HTTP, certificate
  checks, unpacking of update archives, name parsing). They are fixed by the newer Go version. Please update.
- A vulnerability scan (`govulncheck`) now runs on every change and reports new findings in CI.

### Fixed
- **Phone layout.** The time range buttons (24 h, 7 d, 30 d) no longer break into two lines, the Live page no longer
  scrolls sideways because of the filter row, and the Settings page fits narrow screens in German too.
- Dates in English are unambiguous now (`2 Oct 2026, 14:18:39` instead of `02/10/2026`).
- Charts show fewer time labels on narrow screens, so they no longer overlap.
- The device card shows a space before the separator (`127.0.0.1 · RB5009`).

### Changed
- New installations show "Collecting data…" instead of a single dot until the first chart data exists.
- Releases: the build now requires a successful CI run for the exact commit and publishes a build-provenance
  attestation next to the signature (`gh attestation verify`, see `docs/release-signing.md`).
- Added `SECURITY.md` (how to report a vulnerability) and a quick start at the top of the README.

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
