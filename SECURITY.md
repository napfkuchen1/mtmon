# Security policy

## Reporting a vulnerability
Please do **not** open a public issue for security problems. Use GitHub's private reporting:
**Security → Report a vulnerability** on this repository. Include the mtmon version (`mtmon version`), what you
observed and, if possible, steps to reproduce. You will get an answer as soon as the maintainer can look at it.

## Supported versions
mtmon is in beta (0.x). Only the latest release receives fixes.

## Verifying releases
Release archives are signed with minisign and carry a build-provenance attestation and SBOMs (CycloneDX, Go and npm) with their own attestation. See
[docs/release-signing.md](docs/release-signing.md) for the public key and the verification commands.

When `minisign` is not installed, an interactive installer run offers to install it with `apt-get`. If you decline,
or without a terminal, it refuses a signed release (`apt install minisign`). Override with
`MTMON_ALLOW_UNVERIFIED=1` to accept the SHA-256 checksum only.

## Network ingest
The IPFIX (UDP 2055) and syslog (UDP 5514) listeners accept packets only from the IP addresses of configured routers;
everything else is dropped. UDP has no authentication, so a spoofed source address on the same L2 segment is accepted.
Restrict both ports to the router addresses on the container/host firewall and do not expose them to untrusted networks.

## Scope notes
mtmon only writes to a router during the setup/offboarding wizard and only after explicit confirmation. The admin login
used by the wizard is never stored. Details: *Security & privacy* in the README.
