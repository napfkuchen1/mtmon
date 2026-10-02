# Changelog

All notable changes to mtmon are listed here, newest first, in plain language.
mtmon follows [Semantic Versioning](https://semver.org): `MAJOR.MINOR.PATCH`.
While the major version is `0`, mtmon is in **beta**: it works and is tested, but minor releases may still change behaviour.
Version `1.0.0` will mean "verified on a range of real MikroTik hardware".

Every release lists what is **new**, what **changed**, and what was **fixed**. Upgrading is always the same:
run the installer again and choose *update* (a snapshot of the container is taken first).

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

First public beta. This release resets the version numbering: the earlier tags `v2.0.0` to `v2.0.3`
were internal milestones and are superseded by this one (same code, plus the changes below).

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
