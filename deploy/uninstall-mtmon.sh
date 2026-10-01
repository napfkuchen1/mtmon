#!/usr/bin/env bash
# uninstall-mtmon.sh - back up, stop and destroy an mtmon CT. Destructive: needs --confirm <CTID>.
set -Eeuo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
. "$HERE/lib.sh"
CTID=""; CONFIRM=""; NO_BACKUP=0
usage() { echo "Usage: $0 --ctid N --confirm N [--no-backup] [--dry-run]"; }
while [ $# -gt 0 ]; do
  case $1 in
    --ctid) CTID=$2; shift 2;; --confirm) CONFIRM=$2; shift 2;; --no-backup) NO_BACKUP=1; shift;;
    --dry-run) DRY_RUN=1; shift;; -h|--help) usage; exit 0;; *) usage >&2; die "unknown option $1";;
  esac
done
[ -n "$CTID" ] || { usage >&2; die "--ctid is required"; }
need_root
need_cmds pct
ct_exists "$CTID" || die "CT $CTID does not exist"
pct config "$CTID" | grep -qi 'mtmon' || die "CT $CTID does not look like an mtmon container - refusing to destroy it"
[ "$CONFIRM" = "$CTID" ] || die "this DESTROYS CT $CTID and all its monitoring data. Re-run with --confirm $CTID"
if [ "$NO_BACKUP" = 1 ]; then warn "--no-backup: no final backup will be made"; else
  need_cmds vzdump
  info "final backup with vzdump (restore later with: pct restore <newid> <archive>)"
  if [ "$DRY_RUN" = 1 ]; then info "[dry] vzdump $CTID --mode stop"; else
    vzdump "$CTID" --mode stop --compress zstd --storage "${DUMP_STORAGE:-local}" || die "backup failed - nothing was destroyed"
  fi
fi
run pct stop "$CTID" || true
run pct destroy "$CTID" --purge 1
ok "CT $CTID removed. Routers are untouched - remove on each MikroTik if desired:"
cat <<'RO'
  /ip traffic-flow target remove [find dst-address=<mtmon-ip>]
  /user remove mtmon ; /user group remove mtmon-ro
RO
