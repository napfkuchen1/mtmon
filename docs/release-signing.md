# Release signing (maintainer notes)

Releases are signed with [minisign](https://jedisct1.github.io/minisign/).

| Item | Where |
|---|---|
| Public key | `minisign.pub` in the repo and `PublicKey` in `internal/update/minisign.go` (+ `MTMON_PUBKEY` in `install.sh`) |
| Private key | GitHub secret `MINISIGN_KEY` (complete `minisign.key` file, both lines) and an offline copy in a password manager |
| Key password | GitHub secret `MINISIGN_PASSWORD` and the password manager |

The release workflow signs `mtmon-linux-amd64.tar.gz` with trusted comment `mtmon <tag>` and refuses to publish if the
secret is missing or the signature does not verify against `minisign.pub`. The updater requires exactly that comment.

## Verify a download by hand
```bash
minisign -Vm mtmon-linux-amd64.tar.gz -P RWSj18U8+XTlXnicS+OIExn6YF3O9+RC4U9VIaDLVtPFeMkp75YhedSK
```

## Build provenance
The workflow also publishes a GitHub build-provenance attestation for the archive (proves which workflow run on which
commit built it). Check it with:
```bash
gh attestation verify mtmon-linux-amd64.tar.gz --repo napfkuchen1/mtmon
```

## CI gate
The release workflow does not repeat tests and lint: it requires a successful `ci` run for exactly the commit being
released (merge to `main`, wait for CI, then release). Without one, the run stops before building anything.

## Key rotation / loss
Installed versions only trust the key compiled into them, so a lost or leaked key needs a two-step release:
1. Release N is signed with the **old** key and embeds the **new** public key.
2. From release N+1 on, sign with the new key.
If the old key is lost, installations cannot update in-app any more: users re-run the installer *update* once
(it verifies only the checksum, plus the signature if `minisign` is installed and the key matches).

## Releasing without a local Git checkout
*Actions → release → Run workflow* with input `tag` (e.g. `v0.8.1`) on branch `main`. The run validates the tag,
builds with `VERSION=<tag>` (the binary must report exactly the tag, otherwise the updater refuses it), signs, and
creates the tag together with the GitHub release. The CHANGELOG section `## [x.y.z]` must exist on `main` first.
