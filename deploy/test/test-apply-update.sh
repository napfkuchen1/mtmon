#!/usr/bin/env bash
# shellcheck disable=SC2015,SC2016,SC2034
# Tests deploy/apply-update.sh (the root-run swap/rollback helper) against temp dirs and fake binaries.
# Usage: deploy/test/test-apply-update.sh      (needs no root, no systemd)
set -uo pipefail
T=$(cd "$(dirname "$0")" && pwd); D=$(dirname "$T")
H=$D/apply-update.sh
WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT
pass=0; fail=0
ok() { pass=$((pass+1)); echo "PASS  $1"; }
no() { fail=$((fail+1)); echo "FAIL  $1"; [ -z "${2:-}" ] || echo "$2" | tail -12 | sed 's/^/        /'; }
check() { if eval "$2"; then ok "$1"; else no "$1"; fi; }

# fake binaries are shell scripts that print a version (or misbehave)
mkbin() { # mkbin FILE VERSION|bad|hang
  case $2 in
    bad) printf '#!/bin/sh\nexit 3\n' > "$1" ;;
    hang) printf '#!/bin/sh\nsleep 30\n' > "$1" ;;
    empty) printf '#!/bin/sh\nexit 0\n' > "$1" ;;
    *) printf '#!/bin/sh\n[ "$1" = version ] && echo %s\n' "$2" > "$1" ;;
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
  export MTMON_BIN=$S/bin/mtmon MTMON_UPDATE_DIR=$S/upd MTMON_SVC_USER=nonexistent-svc-user MTMON_LOGGER=$WORK/logger LOGFILE=$S/log
}
stage() { mkbin "$S/upd/mtmon.new" "$1"; [ "${2:-}" = nomarker ] || printf 'version=%s\nts=1\n' "$1" > "$S/upd/pending"; }
runh() { OUT=$("$H" 2>&1); RC=$?; }
ver() { "$S/bin/mtmon" version; }

echo "== swap =="
setup 1.0.0; stage 1.1.0; runh
check "helper exits 0" '[ $RC = 0 ]'
check "binary swapped to 1.1.0" '[ "$(ver)" = 1.1.0 ]'
check "previous binary kept as mtmon.prev" '[ "$("$S/upd/mtmon.prev" version)" = 1.0.0 ]'
check "staged file and pending marker removed" '[ ! -e "$S/upd/mtmon.new" ] && [ ! -e "$S/upd/pending" ]'
check "confirm-pending written with version" 'grep -q "^version=1.1.0$" "$S/upd/confirm-pending" && grep -q "^ts=[0-9]" "$S/upd/confirm-pending"'
check "installed binary is 0755" '[ "$(stat -c %a "$S/bin/mtmon")" = 755 ]'
check "no temp files left in bin dir" '[ "$(ls -A "$S/bin")" = mtmon ]'
check "journal line logged" 'grep -q "^daemon.notice .*installed mtmon 1.1.0" "$S/log"'

echo "== bad binary refused =="
for kind in bad empty; do
  setup 1.0.0; stage "$kind" nomarker; runh
  check "$kind: exit 0, old binary untouched" '[ $RC = 0 ] && [ "$(ver)" = 1.0.0 ]'
  check "$kind: staged file removed, no confirm marker, no prev" '[ ! -e "$S/upd/mtmon.new" ] && [ ! -e "$S/upd/confirm-pending" ] && [ ! -e "$S/upd/mtmon.prev" ]'
  check "$kind: refusal recorded and logged" 'grep -q "refused" "$S/upd/last-rollback" && grep -q "^daemon.err .*refused" "$S/log"'
done
setup 1.0.0; stage 9.9.9 nomarker; printf 'version=1.1.0\n' > "$S/upd/pending"; runh
check "version differing from the expected tag refused" '[ "$(ver)" = 1.0.0 ] && [ ! -e "$S/upd/confirm-pending" ] && grep -q "expected" "$S/upd/last-rollback"'
setup 1.0.0; mkbin "$S/upd/mtmon.new" hang; printf 'version=1.1.0\n' > "$S/upd/pending"
start=$SECONDS; runh
check "hanging binary is killed after ~5 s and refused" '[ "$(ver)" = 1.0.0 ] && [ $((SECONDS - start)) -lt 15 ]'
setup 1.0.0; mkbin "$S/real" 1.1.0; ln -s "$S/real" "$S/upd/mtmon.new"; runh
check "symlinked staged file refused" '[ "$(ver)" = 1.0.0 ] && [ ! -e "$S/upd/confirm-pending" ]'

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
check "second update before confirmation keeps the older known-good prev" '[ "$(ver)" = 1.2.0 ] && [ "$("$S/upd/mtmon.prev" version)" = 1.0.0 ]'
runh
check "...and a failed start then rolls back to 1.0.0" '[ "$(ver)" = 1.0.0 ]'
setup 1.0.0; stage 1.1.0; runh; rm -f "$S/upd/last-rollback"; echo "ts=1" > "$S/upd/last-rollback"; rm -f "$S/upd/confirm-pending"; stage 1.2.0; runh
check "successful swap clears an old last-rollback record" '[ ! -e "$S/upd/last-rollback" ]'

echo "== no-op / hardening =="
setup 1.0.0; runh
check "nothing staged: no-op, exit 0, no output" '[ $RC = 0 ] && [ "$(ver)" = 1.0.0 ] && [ -z "$OUT" ] && [ "$(ls -A "$S/upd")" = "" ]'
setup 1.0.0; rm -rf "$S/upd"; runh
check "missing update dir: exit 0" '[ $RC = 0 ] && [ "$(ver)" = 1.0.0 ]'
setup 1.0.0; mkdir "$S/elsewhere"; rm -rf "$S/upd"; ln -s "$S/elsewhere" "$S/upd"; mkbin "$S/elsewhere/mtmon.new" 1.1.0; runh
check "update dir that is a symlink is ignored" '[ "$(ver)" = 1.0.0 ] && [ -e "$S/elsewhere/mtmon.new" ]'
setup 1.0.0; stage 1.1.0; ln -s "$S/victim" "$S/upd/confirm-pending"; ln -s "$S/victim" "$S/upd/last-rollback"; echo keep > "$S/victim"; runh
check "planted symlinks are not followed when writing markers" '[ "$(cat "$S/victim")" = keep ] && [ -f "$S/upd/confirm-pending" ] && [ ! -L "$S/upd/confirm-pending" ]'
check "helper works without logger" 'setup 1.0.0; stage 1.1.0; MTMON_LOGGER=/nonexistent/logger "$H" >/dev/null 2>&1 && [ "$(ver)" = 1.1.0 ]'
check "script passes shellcheck (-s sh)" '! command -v shellcheck >/dev/null || shellcheck -s sh "$H"'

echo; echo "passed=$pass failed=$fail"; [ "$fail" = 0 ]
