# Contributing to mtmon

Thanks for helping. mtmon is licensed under the [AGPL-3.0-only](LICENSE). By contributing you agree that your contribution is released under the same license.

## Sign-off (DCO)

Every commit needs a `Signed-off-by` line, which certifies the [Developer Certificate of Origin](https://developercertificate.org/): you wrote the change or have the right to submit it under the project license.

```bash
git commit -s -m "fix(alert): ..."
```

## Before opening a PR

- New source files (`.go`, `.js`, `.svelte`, `.sh`) start with `SPDX-License-Identifier: AGPL-3.0-only`. `node scripts/spdx.js --write` adds it where missing.
- New dependencies must be under MIT, BSD, Apache-2.0, ISC, MPL-2.0 or a similar permissive license. `node scripts/licenses.js --check` verifies this (CI runs it too).
- `make test` and `gofmt -l .` are clean. Commit messages follow Conventional Commits; add a line to `CHANGELOG.md` under `[Unreleased]`.
- Reference the issue in the PR description (`Fixes #NN`).
