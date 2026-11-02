#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# mtmon-apply-update - installed as /usr/local/bin/mtmon-apply-update and run by systemd as root before every
# start of mtmon.service (ExecStartPre=-+/usr/local/bin/mtmon-apply-update).
#
#   1. A new release was staged by the service (<update dir>/mtmon.new + release.tar.gz + release.minisig). The
#      update dir belongs to the unprivileged service user, so nothing in it is trusted: the INSTALLED root-owned
#      binary verifies the signed archive (`mtmon verify-staged`: release key, tag, strictly newer) and extracts the
#      binary into a root-owned directory; only that binary is installed, never mtmon.new itself. The current
#      binary is kept as <lib dir>/mtmon.prev (root-owned), the new one is swapped in atomically, confirm-pending
#      is written.
#   2. No staged release, but confirm-pending is still there: the previous start after a swap never became
#      healthy (the service deletes the marker after 60 s of uptime and a successful self-check) -> restore the
#      root-owned mtmon.prev and record last-rollback, so a broken release can never brick the service. A forged
#      marker can therefore only bring back the previous genuine release.
#   3. Otherwise: nothing to do.
#
# The helper never runs a staged binary as root, and it never fails the start: it always exits 0.
# All paths can be overridden for tests: MTMON_BIN MTMON_UPDATE_DIR MTMON_LIB_DIR MTMON_SVC_USER MTMON_LOGGER
set -u

BIN=${MTMON_BIN:-/usr/local/bin/mtmon}
DIR=${MTMON_UPDATE_DIR:-/var/lib/mtmon/update}
LIB=${MTMON_LIB_DIR:-/usr/local/lib/mtmon}
SVC_USER=${MTMON_SVC_USER:-mtmon}
LOGGER=${MTMON_LOGGER:-logger}
NEW=$DIR/mtmon.new
ARCH=$DIR/release.tar.gz
SIG=$DIR/release.minisig
PREV=$LIB/mtmon.prev
PENDING=$DIR/pending
CONFIRM=$DIR/confirm-pending
ROLLBACK=$DIR/last-rollback
BINDIR=$(dirname "$BIN")
TMP=
WORK=

# log PRIORITY MESSAGE... : journal (via logger) and stderr (captured by the unit as well)
log() {
  prio=$1
  shift
  echo "mtmon-apply-update: $*" >&2
  if command -v "$LOGGER" >/dev/null 2>&1; then
    "$LOGGER" -t mtmon-apply-update -p "daemon.$prio" -- "$*" >/dev/null 2>&1 || true
  fi
}

trap '[ -z "$TMP" ] || rm -f "$TMP"; [ -z "$WORK" ] || rm -rf "$WORK"' EXIT

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

# can_run_unprivileged : true when not root, or when root can drop to the service user with setpriv
can_run_unprivileged() {
  [ "$(id -u)" != 0 ] && return 0
  command -v setpriv >/dev/null 2>&1 && SVC_UID=$(id -u "$SVC_USER" 2>/dev/null) && SVC_GID=$(id -g "$SVC_USER" 2>/dev/null) &&
    [ "$SVC_UID" != 0 ] && setpriv --reuid="$SVC_UID" --regid="$SVC_GID" --clear-groups true >/dev/null 2>&1
}

# version_of FILE : print the output of `FILE version` with a 5 s timeout, never as root (callers check
# can_run_unprivileged first, which sets SVC_UID/SVC_GID).
version_of() {
  if [ "$(id -u)" = 0 ]; then
    timeout 5 setpriv --reuid="$SVC_UID" --regid="$SVC_GID" --clear-groups "$1" version 2>/dev/null
  else
    timeout 5 "$1" version 2>/dev/null
  fi
}

# regular FILE : exists, is a regular file and not a symlink
regular() { [ -f "$1" ] && [ ! -L "$1" ]; }

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

# root-owned place for the verification work dir and the known-good previous binary (not writable by the service)
if ! mkdir -p "$LIB" || ! chmod 0755 "$LIB"; then log err "cannot create $LIB - updates and rollbacks are disabled"; exit 0; fi
# a mtmon.prev in the update dir is from an older helper (or planted): never use it
rm -f "$DIR/mtmon.prev"

# ---- 1. staged update ----
if [ -e "$NEW" ] || [ -L "$NEW" ] || [ -e "$ARCH" ] || [ -L "$ARCH" ]; then
  want=$(field "$PENDING" version)
  reject() {
    log err "staged update refused: $1 - keeping the installed version"
    rm -f "$NEW" "$PENDING" "$ARCH" "$SIG"
    record_rollback "${want:-unknown}" "update refused before installing: $1"
    exit 0
  }
  if ! regular "$ARCH" || ! regular "$SIG"; then reject "signed release archive or signature missing (update the container once with the installer)"; fi
  # root-owned private copies: the service user cannot swap them between verification and install
  WORK=$(mktemp -d "$LIB/.verify.XXXXXX") || reject "cannot create a work directory in $LIB"
  chmod 0755 "$WORK"
  if ! cp "$ARCH" "$WORK/release.tar.gz" || ! cp "$SIG" "$WORK/release.minisig"; then reject "cannot copy the staged release"; fi
  # the INSTALLED binary checks signature, tag and version and extracts the binary from the verified archive
  if ! got=$("$BIN" verify-staged "$WORK/release.tar.gz" "$WORK/release.minisig" "$WORK/mtmon" 2>"$WORK/err"); then
    reject "verification failed: $(head -c 300 "$WORK/err" | tr '\n' ' ')"
  fi
  got=$(printf '%s\n' "$got" | head -n 1)
  if [ -z "$got" ] || ! regular "$WORK/mtmon"; then reject "verification produced no binary"; fi
  if [ -n "$want" ] && [ "$got" != "$want" ]; then reject "signed release is '$got', expected '$want'"; fi
  # sanity: the verified binary runs and reports its tag (never as root; skipped when privileges cannot be dropped)
  if can_run_unprivileged; then
    ran=$(version_of "$WORK/mtmon" | head -n 1)
    if [ -z "$ran" ]; then reject "the new binary does not run"; fi
    if [ "$ran" != "$got" ]; then reject "new binary reports '$ran', expected '$got'"; fi
  else
    log warn "cannot drop privileges to $SVC_USER (setpriv missing?) - skipping the run check of $got"
  fi
  # keep the last known-good binary; if the previous swap was never confirmed, the older .prev stays authoritative
  if [ -f "$BIN" ] && [ ! -e "$CONFIRM" ]; then
    rm -f "$PREV.tmp"
    if ! cp -p "$BIN" "$PREV.tmp" || ! mv -f "$PREV.tmp" "$PREV"; then log warn "could not save the current binary as $PREV"; fi
  fi
  if atomic_install "$WORK/mtmon"; then
    rm -f "$NEW" "$PENDING" "$ARCH" "$SIG" "$ROLLBACK"
    printf 'version=%s\nts=%s\n' "$got" "$(date +%s)" | safe_write "$CONFIRM"
    log notice "installed mtmon $got (signature verified; previous binary kept as $PREV; confirmation pending)"
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
      record_rollback "${failed:-unknown}" "mtmon ${failed:-the new version} did not become healthy within 5 min after the update; the previous version was restored"
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
