#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# shellcheck disable=SC2015,SC2016,SC2034
# Tests deploy/apply-update.sh (the root-run swap/rollback helper) against temp dirs and fake binaries.
# Usage: deploy/test/test-apply-update.sh      (needs no root, no systemd)
set -uo pipefail
T=$(cd "$(dirname "$0")" && pwd); D=$(dirname "$T")
H=$D/apply-update.sh
WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT
# The helper runs new binaries only as the service user. As root (CI deploy job, dev container) use "nobody" so the
# run checks happen, and make the sandbox reachable for it; as a normal user the helper runs them directly.
SVC=nonexistent-svc-user
if [ "$(id -u)" = 0 ] && id nobody >/dev/null 2>&1 && command -v setpriv >/dev/null 2>&1; then SVC=nobody; chmod 0755 "$WORK"; fi
pass=0; fail=0
ok() { pass=$((pass+1)); echo "PASS  $1"; }
no() { fail=$((fail+1)); echo "FAIL  $1"; [ -z "${2:-}" ] || echo "$2" | tail -12 | sed 's/^/        /'; }
check() { if eval "$2"; then ok "$1"; else no "$1"; fi; }

# fake binaries are shell scripts that print a version (or misbehave). Every fake also implements
# `verify-staged ARCHIVE SIG OUT` like the real binary does in spirit: a signature file "sig:<tag>" counts as valid
# (the real cryptographic checks are unit-tested in internal/update); the "archive" is the binary itself.
VERIFY='[ "$1" = verify-staged ] && { s=$(cat "$3"); case $s in sig:*) cp "$2" "$4" && chmod 0755 "$4" && echo "${s#sig:}"; exit $?;; *) echo "signature does not match" >&2; exit 1;; esac; }'
mkbin() { # mkbin FILE VERSION|bad|hang|empty
  case $2 in
    bad) printf '#!/bin/sh\n%s\nexit 3\n' "$VERIFY" > "$1" ;;
    hang) printf '#!/bin/sh\n%s\nsleep 30\n' "$VERIFY" > "$1" ;;
    empty) printf '#!/bin/sh\n%s\nexit 0\n' "$VERIFY" > "$1" ;;
    *) printf '#!/bin/sh\n%s\n[ "$1" = version ] && echo %s\n' "$VERIFY" "$2" > "$1" ;;
  esac
  chmod 0755 "$1"
}
# fake logger records "priority message"
cat > "$WORK/logger" <<'LOG'
#!/bin/sh
prio=; while [ $# -gt 0 ]; do case $1 in -p) prio=$2; shift 2;; -t) shift 2;; --) shift; break;; *) shift;; esac; done
echo "$prio $*" >> "$LOGFILE"
LOG
chmod +x "$WORK/logger"

setup() { # fresh sandbox: installed binary $1
  S=$WORK/s; rm -rf "$S"; mkdir -p "$S/bin" "$S/upd"
  mkbin "$S/bin/mtmon" "$1"
  : > "$S/log"
  export MTMON_BIN=$S/bin/mtmon MTMON_UPDATE_DIR=$S/upd MTMON_LIB_DIR=$S/lib MTMON_SVC_USER=$SVC MTMON_LOGGER=$WORK/logger LOGFILE=$S/log
}
# stage TAG|bad|hang|empty [nomarker] : what the service writes: signed archive + signature + mtmon.new + pending
stage() {
  mkbin "$S/upd/release.tar.gz" "$1"; printf 'sig:%s\n' "$1" > "$S/upd/release.minisig"
  mkbin "$S/upd/mtmon.new" "$1"
  [ "${2:-}" = nomarker ] || printf 'version=%s\nts=1\n' "$1" > "$S/upd/pending"
}
runh() { OUT=$("$H" 2>&1); RC=$?; }
ver() { "$S/bin/mtmon" version; }
prev() { "$S/lib/mtmon.prev" version; }

echo "== swap =="
setup 1.0.0; stage 1.1.0; runh
check "helper exits 0" '[ $RC = 0 ]'
check "binary swapped to 1.1.0" '[ "$(ver)" = 1.1.0 ]'
check "previous binary kept as root-side mtmon.prev" '[ "$(prev)" = 1.0.0 ] && [ ! -e "$S/upd/mtmon.prev" ]'
check "staged files and pending marker removed" '[ ! -e "$S/upd/mtmon.new" ] && [ ! -e "$S/upd/pending" ] && [ ! -e "$S/upd/release.tar.gz" ] && [ ! -e "$S/upd/release.minisig" ]'
check "no work dir left in lib dir" '[ "$(ls -A "$S/lib")" = mtmon.prev ]'
check "confirm-pending written with version" 'grep -q "^version=1.1.0$" "$S/upd/confirm-pending" && grep -q "^ts=[0-9]" "$S/upd/confirm-pending"'
check "installed binary is 0755" '[ "$(stat -c %a "$S/bin/mtmon")" = 755 ]'
check "no temp files left in bin dir" '[ "$(ls -A "$S/bin")" = mtmon ]'
check "journal line logged" 'grep -q "^daemon.notice .*installed mtmon 1.1.0" "$S/log"'

echo "== bad binary refused =="
for kind in bad empty; do
  setup 1.0.0; stage "$kind" nomarker; runh
  check "$kind: exit 0, old binary untouched" '[ $RC = 0 ] && [ "$(ver)" = 1.0.0 ]'
  check "$kind: staged file removed, no confirm marker, no prev" '[ ! -e "$S/upd/mtmon.new" ] && [ ! -e "$S/upd/release.tar.gz" ] && [ ! -e "$S/upd/confirm-pending" ] && [ ! -e "$S/lib/mtmon.prev" ]'
  check "$kind: refusal recorded and logged" 'grep -q "refused" "$S/upd/last-rollback" && grep -q "^daemon.err .*refused" "$S/log"'
done
setup 1.0.0; stage 9.9.9 nomarker; printf 'version=1.1.0\n' > "$S/upd/pending"; runh
check "version differing from the expected tag refused" '[ "$(ver)" = 1.0.0 ] && [ ! -e "$S/upd/confirm-pending" ] && grep -q "expected" "$S/upd/last-rollback"'
setup 1.0.0; stage 1.1.0; mkbin "$S/upd/release.tar.gz" hang; printf 'sig:1.1.0\n' > "$S/upd/release.minisig"
start=$SECONDS; runh
check "hanging binary is killed after ~5 s and refused" '[ "$(ver)" = 1.0.0 ] && [ $((SECONDS - start)) -lt 15 ]'
setup 1.0.0; stage 1.1.0; mkbin "$S/real" 1.1.0; rm -f "$S/upd/release.tar.gz"; ln -s "$S/real" "$S/upd/release.tar.gz"; runh
check "symlinked staged archive refused" '[ "$(ver)" = 1.0.0 ] && [ ! -e "$S/upd/confirm-pending" ]'

echo "== forged updates (service user controls the update dir) =="
setup 1.0.0; mkbin "$S/upd/mtmon.new" 1.1.0; printf 'version=1.1.0\nts=1\n' > "$S/upd/pending"; runh
check "mtmon.new + matching pending without a signed archive refused" '[ "$(ver)" = 1.0.0 ] && [ ! -e "$S/upd/confirm-pending" ] && grep -q "archive or signature missing" "$S/upd/last-rollback"'
setup 1.0.0; stage 1.1.0; printf 'forged\n' > "$S/upd/release.minisig"; runh
check "bad signature refused, installed binary untouched" '[ "$(ver)" = 1.0.0 ] && grep -q "verification failed: signature does not match" "$S/upd/last-rollback" && [ ! -e "$S/upd/release.tar.gz" ]'
setup 1.0.0; stage 1.1.0; rm -f "$S/upd/release.minisig"; runh
check "missing signature refused" '[ "$(ver)" = 1.0.0 ] && [ ! -e "$S/upd/confirm-pending" ]'
setup 1.0.0; stage 1.1.0; printf '#!/bin/sh\necho 1.1.0\n' > "$S/upd/mtmon.new"; echo EVIL >> "$S/upd/mtmon.new"; runh
check "installed binary comes from the verified archive, not from mtmon.new" '[ "$(ver)" = 1.1.0 ] && ! grep -q EVIL "$S/bin/mtmon"'
setup 1.0.0; stage 1.1.0; runh; mkbin "$S/upd/mtmon.prev" 6.6.6; printf 'version=1.1.0\nts=1\n' > "$S/upd/confirm-pending"; runh
check "planted mtmon.prev in the update dir is never installed by a rollback" '[ "$(ver)" = 1.0.0 ] && [ ! -e "$S/upd/mtmon.prev" ]'
setup 1.0.0; mkbin "$S/upd/mtmon.prev" 6.6.6; printf 'version=1.1.0\nts=1\n' > "$S/upd/confirm-pending"; runh
check "forged confirm-pending without a root-side prev changes nothing" '[ "$(ver)" = 1.0.0 ] && [ ! -e "$S/upd/confirm-pending" ]'
check "lib dir is root-side: 0755, not group/world writable" '[ "$(stat -c %a "$S/lib")" = 755 ]'
if [ "$(id -u)" = 0 ]; then
  # root without a usable service user: the new binary must never run as root; the run check is skipped with a warning
  setup 1.0.0; stage 1.1.0
  printf '#!/bin/sh\n%s\n[ "$1" = version ] && { id -u > "%s"; echo 1.1.0; }\n' "$VERIFY" "$S/ran-as" > "$S/upd/release.tar.gz"
  MTMON_SVC_USER=nonexistent-svc-user "$H" >/dev/null 2>&1
  check "root without setpriv/service user: new binary never executed as root" '[ ! -e "$S/ran-as" ] || [ "$(cat "$S/ran-as")" != 0 ]'
  check "...and the skipped run check is logged" 'grep -q "^daemon.warn .*skipping the run check" "$S/log"'
fi

echo "== rollback =="
setup 1.0.0; stage 1.1.0; runh                       # swap
runh                                                  # next start: confirm-pending still there -> rollback
check "rollback restored 1.0.0" '[ "$(ver)" = 1.0.0 ]'
check "confirm-pending removed after rollback" '[ ! -e "$S/upd/confirm-pending" ]'
check "last-rollback records version and reason" 'grep -q "^version=1.1.0$" "$S/upd/last-rollback" && grep -q "^reason=.*previous version was restored" "$S/upd/last-rollback" && grep -q "^ts=[0-9]" "$S/upd/last-rollback"'
check "rollback logged loudly" 'grep -q "^daemon.err ROLLBACK" "$S/log"'
runh
check "third start is a no-op" '[ "$(ver)" = 1.0.0 ] && [ $RC = 0 ]'
setup 1.0.0; stage 1.1.0; runh; rm -f "$S/upd/confirm-pending"; runh
check "confirmed update (marker deleted by app) is not rolled back" '[ "$(ver)" = 1.1.0 ]'
setup 1.0.0; printf 'version=1.1.0\nts=1\n' > "$S/upd/confirm-pending"; runh
check "marker without prev: no crash, marker cleared, binary untouched" '[ $RC = 0 ] && [ ! -e "$S/upd/confirm-pending" ] && [ "$(ver)" = 1.0.0 ]'
setup 1.0.0; stage 1.1.0; runh; stage 1.2.0; runh
check "second update before confirmation keeps the older known-good prev" '[ "$(ver)" = 1.2.0 ] && [ "$(prev)" = 1.0.0 ]'
runh
check "...and a failed start then rolls back to 1.0.0" '[ "$(ver)" = 1.0.0 ]'
setup 1.0.0; stage 1.1.0; runh; rm -f "$S/upd/last-rollback"; echo "ts=1" > "$S/upd/last-rollback"; rm -f "$S/upd/confirm-pending"; stage 1.2.0; runh
check "successful swap clears an old last-rollback record" '[ ! -e "$S/upd/last-rollback" ]'

echo "== no-op / hardening =="
setup 1.0.0; runh
check "nothing staged: no-op, exit 0, no output" '[ $RC = 0 ] && [ "$(ver)" = 1.0.0 ] && [ -z "$OUT" ] && [ "$(ls -A "$S/upd")" = "" ]'
setup 1.0.0; rm -rf "$S/upd"; runh
check "missing update dir: exit 0" '[ $RC = 0 ] && [ "$(ver)" = 1.0.0 ]'
setup 1.0.0; mkdir "$S/elsewhere"; rm -rf "$S/upd"; ln -s "$S/elsewhere" "$S/upd"; mkbin "$S/elsewhere/mtmon.new" 1.1.0; mkbin "$S/elsewhere/release.tar.gz" 1.1.0; printf 'sig:1.1.0\n' > "$S/elsewhere/release.minisig"; runh
check "update dir that is a symlink is ignored" '[ "$(ver)" = 1.0.0 ] && [ -e "$S/elsewhere/mtmon.new" ]'
setup 1.0.0; stage 1.1.0; ln -s "$S/victim" "$S/upd/confirm-pending"; ln -s "$S/victim" "$S/upd/last-rollback"; echo keep > "$S/victim"; runh
check "planted symlinks are not followed when writing markers" '[ "$(cat "$S/victim")" = keep ] && [ -f "$S/upd/confirm-pending" ] && [ ! -L "$S/upd/confirm-pending" ]'
check "helper works without logger" 'setup 1.0.0; stage 1.1.0; MTMON_LOGGER=/nonexistent/logger "$H" >/dev/null 2>&1 && [ "$(ver)" = 1.1.0 ]'
check "script passes shellcheck (-s sh)" '! command -v shellcheck >/dev/null || shellcheck -s sh "$H"'

echo; echo "passed=$pass failed=$fail"; [ "$fail" = 0 ]
