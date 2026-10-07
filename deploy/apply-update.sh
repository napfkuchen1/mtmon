#!/bin/sh
# mtmon-apply-update - installed as /usr/local/bin/mtmon-apply-update and run by systemd as root before every
# start of mtmon.service (ExecStartPre=-+/usr/local/bin/mtmon-apply-update).
#
#   1. A new release was staged by the service (<update dir>/mtmon.new): verify it runs, keep the current binary
#      as mtmon.prev, swap it in atomically and write confirm-pending.
#   2. No staged release, but confirm-pending is still there: the previous start after a swap never became
#      healthy (the service deletes the marker after 60 s of uptime and a successful self-check) -> restore
#      mtmon.prev and record last-rollback, so a broken release can never brick the service.
#   3. Otherwise: nothing to do.
#
# The helper never fails the start: it always exits 0. All paths can be overridden for tests:
#   MTMON_BIN MTMON_UPDATE_DIR MTMON_SVC_USER MTMON_LOGGER
set -u

BIN=${MTMON_BIN:-/usr/local/bin/mtmon}
DIR=${MTMON_UPDATE_DIR:-/var/lib/mtmon/update}
SVC_USER=${MTMON_SVC_USER:-mtmon}
LOGGER=${MTMON_LOGGER:-logger}
NEW=$DIR/mtmon.new
PREV=$DIR/mtmon.prev
PENDING=$DIR/pending
CONFIRM=$DIR/confirm-pending
ROLLBACK=$DIR/last-rollback
BINDIR=$(dirname "$BIN")
TMP=

# log PRIORITY MESSAGE... : journal (via logger) and stderr (captured by the unit as well)
log() {
  prio=$1
  shift
  echo "mtmon-apply-update: $*" >&2
  if command -v "$LOGGER" >/dev/null 2>&1; then
    "$LOGGER" -t mtmon-apply-update -p "daemon.$prio" -- "$*" >/dev/null 2>&1 || true
  fi
}

trap '[ -z "$TMP" ] || rm -f "$TMP"' EXIT

# The update directory belongs to the unprivileged service user, so never follow links planted in it:
# remove the target first and create it with noclobber (O_EXCL).
safe_write() { # FILE ; content on stdin ; the file is handed to the service user so the app can delete it
  rm -f "$1"
  if (set -C; cat >"$1") 2>/dev/null; then
    if [ "$(id -u)" = 0 ] && id "$SVC_USER" >/dev/null 2>&1; then chown -h "$SVC_USER" "$1" 2>/dev/null || true; fi
    return 0
  fi
  return 1
}

field() { sed -n "s/^$2=//p" "$1" 2>/dev/null | head -n 1; }

# version_of FILE : print the output of `FILE version`, run with a 5 s timeout and (when possible) as the
# unprivileged service user, because the staged file was written by that user.
version_of() {
  if [ "$(id -u)" = 0 ] && command -v setpriv >/dev/null 2>&1 && id "$SVC_USER" >/dev/null 2>&1 &&
    setpriv --reuid="$SVC_USER" --regid="$SVC_USER" --clear-groups true >/dev/null 2>&1; then
    timeout 5 setpriv --reuid="$SVC_USER" --regid="$SVC_USER" --clear-groups "$1" version 2>/dev/null
  else
    timeout 5 "$1" version 2>/dev/null
  fi
}

# atomic_install SRC : copy SRC next to $BIN and rename it over $BIN
atomic_install() {
  TMP=$BINDIR/.mtmon.swap.$$
  cp "$1" "$TMP" && chmod 0755 "$TMP" && mv -f "$TMP" "$BIN"
  rc=$?
  [ "$rc" = 0 ] && TMP=
  return "$rc"
}

record_rollback() { # VERSION REASON
  printf 'ts=%s\nversion=%s\nreason=%s\n' "$(date +%s)" "$1" "$2" | safe_write "$ROLLBACK"
}

if [ -L "$DIR" ] || [ ! -d "$DIR" ]; then exit 0; fi

# ---- 1. staged update ----
if [ -e "$NEW" ] || [ -L "$NEW" ]; then
  want=$(field "$PENDING" version)
  reject() {
    log err "staged update refused: $1 - keeping the installed version"
    rm -f "$NEW" "$PENDING"
    record_rollback "${want:-unknown}" "update refused before installing: $1"
    exit 0
  }
  if [ ! -f "$NEW" ] || [ -L "$NEW" ]; then reject "staged file is not a regular file"; fi
  # verify a root-owned private copy (the service user cannot swap it between check and install)
  TMP=$BINDIR/.mtmon.verify.$$
  if ! cp "$NEW" "$TMP" || ! chmod 0755 "$TMP"; then reject "cannot copy the staged binary"; fi
  got=$(version_of "$TMP" | head -n 1)
  if [ -z "$got" ]; then reject "the staged binary does not run"; fi
  if [ -n "$want" ] && [ "$got" != "$want" ]; then reject "staged binary reports '$got', expected '$want'"; fi
  # keep the last known-good binary; if the previous swap was never confirmed, the older .prev stays authoritative
  if [ -f "$BIN" ] && [ ! -e "$CONFIRM" ]; then
    rm -f "$PREV.tmp"
    if ! cp -p "$BIN" "$PREV.tmp" || ! mv -f "$PREV.tmp" "$PREV"; then log warn "could not save the current binary as $PREV"; fi
  fi
  if mv -f "$TMP" "$BIN"; then
    TMP=
    chmod 0755 "$BIN"
    rm -f "$NEW" "$PENDING" "$ROLLBACK"
    printf 'version=%s\nts=%s\n' "$got" "$(date +%s)" | safe_write "$CONFIRM"
    log notice "installed mtmon $got (previous binary kept as $PREV; confirmation pending)"
  else
    rm -f "$TMP"
    TMP=
    reject "could not replace $BIN"
  fi
  exit 0
fi

# ---- 2. rollback of an unconfirmed update ----
if [ -e "$CONFIRM" ]; then
  failed=$(field "$CONFIRM" version)
  if [ -f "$PREV" ] && [ ! -L "$PREV" ]; then
    if atomic_install "$PREV"; then
      rm -f "$CONFIRM"
      record_rollback "${failed:-unknown}" "mtmon ${failed:-the new version} did not become healthy within 60 s after the update; the previous version was restored"
      log err "ROLLBACK: mtmon ${failed:-new version} never became healthy - restored the previous binary from $PREV"
    else
      log err "ROLLBACK FAILED: could not restore $PREV to $BIN"
    fi
  else
    rm -f "$CONFIRM"
    log warn "unconfirmed update marker found but no previous binary ($PREV) exists - cannot roll back"
  fi
fi
exit 0
