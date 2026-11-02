#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# shellcheck disable=SC2016
# Tests deploy/update-geo.sh with a fake curl (no network). As root it also checks that the downloads run as the
# service user and never write through links planted in the geo directory.
# Usage: deploy/test/test-update-geo.sh
set -uo pipefail
T=$(cd "$(dirname "$0")" && pwd); D=$(dirname "$T")
G=$D/update-geo.sh
WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT
chmod 0755 "$WORK"
pass=0; fail=0
ok() { pass=$((pass+1)); echo "PASS  $1"; }
no() { fail=$((fail+1)); echo "FAIL  $1"; [ -z "${2:-}" ] || echo "$2" | tail -12 | sed 's/^/        /'; }
check() { if eval "$2"; then ok "$1"; else no "$1" "${3:-}"; fi; }

# fake curl: -o DEST URL -> plausible content (gzip for .gz, big csv otherwise); FAKE_FAIL=1 -> fail
mkdir -p "$WORK/fakebin"
cat > "$WORK/fakebin/curl" <<'C'
#!/bin/sh
out=; url=
while [ $# -gt 0 ]; do case $1 in -o) out=$2; shift 2;; -m) shift 2;; -*) shift;; *) url=$1; shift;; esac; done
[ -z "${FAKE_FAIL:-}" ] || exit 22
case $url in
  *.gz) head -c 1200000 /dev/zero | gzip -c > "$out" ;;
  *) head -c 600000 /dev/zero | tr '\0' 'a' > "$out" ;;
esac
C
chmod 0755 "$WORK/fakebin/curl"
export PATH="$WORK/fakebin:$PATH" MTMON_GEO_NO_RESTART=1

run() { OUT=$(sh "$G" 2>&1); RC=$?; }

echo "== download =="
export MTMON_GEO_DIR=$WORK/a/geo
mkdir -p "$WORK/a"
if [ "$(id -u)" = 0 ]; then chown nobody "$WORK/a"; export MTMON_GEO_USER=nobody; fi
run
check "exit 0 on plausible downloads" '[ $RC = 0 ]' "$OUT"
check "country, asn and oui files in place" '[ -s "$MTMON_GEO_DIR/dbip-country.mmdb" ] && [ -s "$MTMON_GEO_DIR/dbip-asn.mmdb" ] && [ -s "$MTMON_GEO_DIR/oui.csv" ]'
check "no temp files left" '[ -z "$(ls -A "$MTMON_GEO_DIR" | grep "^\.")" ]'
FAKE_FAIL=1 run
check "failed download: non-zero exit, previous files kept" '[ $RC != 0 ] && [ -s "$MTMON_GEO_DIR/oui.csv" ]'

if [ "$(id -u)" = 0 ] && id nobody >/dev/null 2>&1 && command -v setpriv >/dev/null 2>&1; then
  echo "== as root: drop to the service user, never write through planted links =="
  check "files are owned by the service user, not root" '[ "$(stat -c %U "$MTMON_GEO_DIR/oui.csv")" = nobody ]'
  echo keep > "$WORK/victim"; chmod 0644 "$WORK/victim"
  for n in .oui.csv .dbip-country.gz .dbip-country.mmdb .dbip-asn.gz .dbip-asn.mmdb; do ln -sf "$WORK/victim" "$MTMON_GEO_DIR/$n"; done
  run
  check "planted symlinks at every temp name: root-owned target unchanged" '[ "$(cat "$WORK/victim")" = keep ]' "$OUT"
  check "...and the download still succeeds" '[ $RC = 0 ] && [ -s "$MTMON_GEO_DIR/oui.csv" ] && [ ! -L "$MTMON_GEO_DIR/oui.csv" ]' "$OUT"
  rm -rf "$WORK/b"; mkdir -p "$WORK/b"
  MTMON_GEO_DIR=$WORK/b/state/geo run
  check "missing state dir is created for the service user" '[ $RC = 0 ] && [ "$(stat -c %U "$WORK/b/state")" = nobody ] && [ -s "$WORK/b/state/geo/oui.csv" ]' "$OUT"
  MTMON_GEO_USER=root run
  check "service user root is refused (exit 2)" '[ $RC = 2 ]'
fi
check "script passes shellcheck (-s sh)" '! command -v shellcheck >/dev/null || shellcheck -s sh "$G"'

echo; echo "passed=$pass failed=$fail"; [ "$fail" = 0 ]
