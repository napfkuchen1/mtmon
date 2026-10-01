#!/usr/bin/env bash
# mtmon bootstrap - run as root ON THE PROXMOX HOST. Downloads the latest GitHub release,
# verifies its checksum and hands over to install-mtmon.sh / update-mtmon.sh / uninstall-mtmon.sh.
#
#   MTMON_REPO=<owner>/mtmon bash -c "$(curl -fsSL https://raw.githubusercontent.com/<owner>/mtmon/main/install.sh)" -- install --dry-run
#   ... -- install --ctid 210 --storage local-lvm --bridge vmbr0 --with-geo
#   ... -- update --ctid 210
#   ... -- uninstall --confirm 210
#
# Env: MTMON_REPO (required, owner/name) · MTMON_VERSION (tag, default: latest) · MTMON_BASE_URL (override, for tests)
set -Eeuo pipefail
REPO=${MTMON_REPO:-}
VER=${MTMON_VERSION:-latest}
ASSET=mtmon-linux-amd64.tar.gz
die() { echo "ERROR: $*" >&2; exit 1; }

ACTION=${1:-}
case $ACTION in
  install|update|uninstall) shift;;
  ""|-h|--help) sed -n '2,10p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//'; exit 0;;
  *) die "first argument must be install | update | uninstall (got '$ACTION')";;
esac

[ "$(id -u)" = 0 ] || die "run as root on the Proxmox host"
for c in curl tar sha256sum; do command -v "$c" >/dev/null || die "missing command: $c"; done
if [ -n "${MTMON_BASE_URL:-}" ]; then BASE=$MTMON_BASE_URL
else
  [ -n "$REPO" ] || die "set MTMON_REPO=<owner>/<repo>, e.g. MTMON_REPO=daniel/mtmon"
  case $REPO in */*) ;; *) die "MTMON_REPO must look like owner/name";; esac
  if [ "$VER" = latest ]; then BASE="https://github.com/$REPO/releases/latest/download"
  else BASE="https://github.com/$REPO/releases/download/$VER"; fi
fi

T=$(mktemp -d /tmp/mtmon-boot.XXXXXX); trap 'rm -rf "$T"' EXIT
echo "==> downloading $BASE/$ASSET"
curl -fsSL --retry 3 -o "$T/$ASSET" "$BASE/$ASSET" || die "download failed (release exists? repo public?)"
curl -fsSL --retry 3 -o "$T/$ASSET.sha256" "$BASE/$ASSET.sha256" || die "checksum download failed"
want=$(awk '{print $1}' "$T/$ASSET.sha256"); got=$(sha256sum "$T/$ASSET" | awk '{print $1}')
if [ -z "$want" ] || [ "$want" != "$got" ]; then die "checksum mismatch - aborting (expected $want, got $got)"; fi
echo "==> checksum ok"
tar xzf "$T/$ASSET" -C "$T"
[ -x "$T/mtmon/install-mtmon.sh" ] || die "unexpected archive layout"
( cd "$T/mtmon" && sha256sum -c SHA256SUMS >/dev/null ) || die "SHA256SUMS inside archive do not match"
cd "$T/mtmon"
# keep the unpacked files until the child finished (no exec, trap cleans up afterwards)
./"$ACTION"-mtmon.sh "$@"
