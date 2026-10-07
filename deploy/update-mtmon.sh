#!/usr/bin/env bash
# update-mtmon.sh - upgrade mtmon in an existing CT with automatic snapshot and automatic rollback.
# Run as root ON THE PROXMOX HOST.
set -Eeuo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
. "$HERE/lib.sh"

CTID=${MTMON_CTID:-210}
BINARY=${MTMON_BINARY:-$HERE/mtmon}
NO_BACKUP=0
usage() { echo "Usage: $0 [--ctid N] [--binary PATH] [--no-backup] [--dry-run]"; }
while [ $# -gt 0 ]; do
  case $1 in
    --ctid) CTID=$2; shift 2;; --binary) BINARY=$2; shift 2;; --no-backup) NO_BACKUP=1; shift;;
    --dry-run) DRY_RUN=1; shift;; -h|--help) usage; exit 0;; *) usage >&2; die "unknown option $1";;
  esac
done

banner "update"
need_root
need_cmds pct sha256sum
ct_exists "$CTID" || die "CT $CTID does not exist"
pct config "$CTID" | grep -qi 'mtmon' || die "CT $CTID does not look like an mtmon container (no 'mtmon' in its config) - refusing"
ct_running "$CTID" || die "CT $CTID is not running (pct start $CTID)"
[ -f "$BINARY" ] || die "binary not found: $BINARY"
chmod +x "$BINARY" 2>/dev/null || true
NEW_VER=$("$BINARY" version 2>/dev/null) || die "new binary does not run on this host"
OLD_VER=$(pct exec "$CTID" -- /usr/local/bin/mtmon version 2>/dev/null || echo unknown)
if [ "$DRY_RUN" != 1 ] && [ "$(sha256_of "$BINARY")" = "$(pct exec "$CTID" -- sha256sum /usr/local/bin/mtmon | awk '{print $1}')" ]; then
  ok "CT $CTID already runs this exact binary ($OLD_VER) - nothing to do"; exit 0
fi
info "updating CT $CTID: $OLD_VER -> $NEW_VER"

# 1. full-CT backup (snapshot, or vzdump fallback)
BACKUP_REF=none
if [ "$NO_BACKUP" = 1 ]; then warn "--no-backup: skipping CT snapshot"; else
  backup_ct "$CTID" "update" || die "could not create a snapshot or vzdump backup - aborting (use --no-backup only if you accept the risk)"
fi

TS=$(date +%Y%m%d-%H%M%S)
PRE=/var/lib/mtmon/backups/pre-update-$TS
# 2. app-level backup: stop, copy binary + DB (incl. WAL) so rollback is exact
ct_exec "$CTID" systemctl stop mtmon
ct_exec "$CTID" sh -c "mkdir -p $PRE && cp -a /usr/local/bin/mtmon $PRE/mtmon.prev && cp -a /var/lib/mtmon/mtmon.db* $PRE/ 2>/dev/null; cp -a /etc/mtmon/config.json $PRE/config.json; chown -R mtmon:mtmon /var/lib/mtmon/backups"
rollback() {
  warn "update failed - ROLLING BACK to $OLD_VER"
  pct exec "$CTID" -- systemctl stop mtmon >/dev/null 2>&1 || true
  pct exec "$CTID" -- sh -c "cp -a $PRE/mtmon.prev /usr/local/bin/mtmon && rm -f /var/lib/mtmon/mtmon.db* && cp -a $PRE/mtmon.db* /var/lib/mtmon/ && chown mtmon:mtmon /var/lib/mtmon/mtmon.db*" || true
  pct exec "$CTID" -- systemctl start mtmon || true
  if WAIT_SECS=30 wait_for pct exec "$CTID" -- /usr/local/bin/mtmon selftest -c /etc/mtmon/config.json; then
    ok "rolled back; old version $OLD_VER is running again"
  else
    warn "rollback self-test failed too. Full restore: $BACKUP_REF  (pct rollback $CTID <snapshot>) ; files in CT: $PRE"
  fi
}
trap 'rollback; exit 1' ERR

# 3. swap binary, start, verify
run pct push "$CTID" "$BINARY" /usr/local/bin/mtmon.new --perms 0755
ct_exec "$CTID" mv -f /usr/local/bin/mtmon.new /usr/local/bin/mtmon
# in-app update support: root helper + unit (ExecStartPre); drop any half-finished in-app update so the helper
# cannot swap a stale staged binary over the one we just installed
run pct push "$CTID" "$HERE/apply-update.sh" /usr/local/bin/mtmon-apply-update --perms 0755
run pct push "$CTID" "$HERE/mtmon.service" /etc/systemd/system/mtmon.service --perms 0644
ct_exec "$CTID" rm -f /var/lib/mtmon/update/mtmon.new /var/lib/mtmon/update/pending /var/lib/mtmon/update/confirm-pending /var/lib/mtmon/update/last-rollback
ct_exec "$CTID" systemctl daemon-reload
ct_exec "$CTID" systemctl start mtmon
if [ "$DRY_RUN" != 1 ]; then
  WAIT_SECS=40 wait_for pct exec "$CTID" -- /usr/local/bin/mtmon selftest -c /etc/mtmon/config.json \
    || { pct exec "$CTID" -- journalctl -u mtmon -n 30 --no-pager || true; false; }
  [ "$(pct exec "$CTID" -- /usr/local/bin/mtmon version)" = "$NEW_VER" ] || false
  ok "updated to $NEW_VER and self-test passed"
  echo "  pre-update copy (binary, DB, config) inside the CT: $PRE"
  echo "  full CT rollback if ever needed: $BACKUP_REF"
fi
trap - ERR
