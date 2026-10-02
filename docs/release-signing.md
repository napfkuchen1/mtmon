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

## Key rotation / loss
Installed versions only trust the key compiled into them, so a lost or leaked key needs a two-step release:
1. Release N is signed with the **old** key and embeds the **new** public key.
2. From release N+1 on, sign with the new key.
If the old key is lost, installations cannot update in-app any more: users re-run the installer *update* once
(it verifies only the checksum, plus the signature if `minisign` is installed and the key matches).
