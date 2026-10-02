# Security policy

## Reporting a vulnerability
Please do **not** open a public issue for security problems. Use GitHub's private reporting:
**Security → Report a vulnerability** on this repository. Include the mtmon version (`mtmon version`), what you
observed and, if possible, steps to reproduce. You will get an answer as soon as the maintainer can look at it.

## Supported versions
mtmon is in beta (0.x). Only the latest release receives fixes.

## Verifying releases
Release archives are signed with minisign and carry a build-provenance attestation. See
[docs/release-signing.md](docs/release-signing.md) for the public key and the verification commands.

## Scope notes
mtmon only writes to a router during the setup/offboarding wizard and only after explicit confirmation. The admin login
used by the wizard is never stored. Details: *Security & privacy* in the README.
