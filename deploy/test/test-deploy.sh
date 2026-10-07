#!/usr/bin/env bash
# shellcheck disable=SC2015,SC2016
# Tests install/update/uninstall against fake Proxmox tools (stubs/). Needs a built mtmon binary.
# Usage: deploy/test/test-deploy.sh [path/to/mtmon-binary]   (the binary is only used for its `version` output)
set -uo pipefail
T=$(cd "$(dirname "$0")" && pwd); D=$(dirname "$T")
BIN=${1:-$D/mtmon}
[ -x "$BIN" ] || { echo "binary not found: $BIN"; exit 2; }
WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT
export FAKE_STATE=$WORK/state PATH="$T/stubs:$PATH"
mkdir -p "$WORK/d" && cp "$D"/*.sh "$D"/*.service "$D"/*.conf "$WORK/d/"
# two fake "release" binaries with distinct versions built from the same file: shell wrappers
mk() { printf '#!/bin/sh\n[ "$1" = version ] && echo %s\n' "$2" > "$1"; chmod +x "$1"; }
mk "$WORK/mtmon-1.0.0" 1.0.0; mk "$WORK/mtmon-9.9.9-bad" 9.9.9-bad; mk "$WORK/mtmon-0.9.0" 0.9.0
cp "$WORK/mtmon-1.0.0" "$WORK/d/mtmon"
pass=0; fail=0
t() { # t NAME EXPECT_RC CMD...
  local name=$1 want=$2; shift 2
  local out rc; out=$("$@" 2>&1); rc=$?
  if [ "$rc" = "$want" ]; then pass=$((pass+1)); echo "PASS  $name"; else fail=$((fail+1)); echo "FAIL  $name (rc=$rc want=$want)"; echo "$out" | tail -15 | sed 's/^/        /'; fi
  LAST=$out
}
has() { if grep -q -- "$1" <<<"$LAST"; then pass=$((pass+1)); echo "PASS  ...output contains: $1"; else fail=$((fail+1)); echo "FAIL  ...output missing: $1"; fi; }
called() { if grep -q -- "$1" "$FAKE_STATE/calls.log" 2>/dev/null; then pass=$((pass+1)); echo "PASS  ...called: $1"; else fail=$((fail+1)); echo "FAIL  ...never called: $1"; fi; }
notcalled() { if grep -q -- "$1" "$FAKE_STATE/calls.log" 2>/dev/null; then fail=$((fail+1)); echo "FAIL  ...unexpectedly called: $1"; else pass=$((pass+1)); echo "PASS  ...not called: $1"; fi; }
reset() { rm -rf "$FAKE_STATE"; mkdir -p "$FAKE_STATE"; : > "$FAKE_STATE/calls.log"; rm -f /root/mtmon-210-credentials.txt; }
I="$WORK/d/install-mtmon.sh"; U="$WORK/d/update-mtmon.sh"; X="$WORK/d/uninstall-mtmon.sh"

echo "== install =="
reset; t "dry-run changes nothing" 0 "$I" --dry-run --yes; notcalled "pct create"; has "Plan"
reset; t "PVE 8 rejected" 1 env STUB_PVE=8.4.1 "$I" --yes; has "Proxmox VE 9"
reset; touch "$FAKE_STATE/ct_210"; t "existing CT refused" 1 "$I" --yes; has "already exists"; notcalled "pct create"
reset; t "unknown storage rejected" 1 "$I" --yes --storage nope; notcalled "pct create"
reset; t "inactive storage rejected" 1 "$I" --yes --storage cold; notcalled "pct create"
reset; t "unknown bridge rejected" 1 "$I" --yes --bridge vmbr9; notcalled "pct create"
reset; t "missing binary rejected" 1 "$I" --yes --binary /nonexistent; notcalled "pct create"
reset; t "downloads template if missing" 0 env STUB_NO_TEMPLATE=1 "$I" --yes; called "pveam download"
reset; t "full install" 0 "$I" --yes --ip 192.168.88.50/24 --gw 192.168.88.1 --vlan 20
has "mtmon is running"; has "Fake-Pass-123456"
called "pct create 210"; called "--unprivileged 1"; called "--features nesting=1"; called "--rootfs local-lvm:8"
called "ip=192.168.88.50/24,gw=192.168.88.1,tag=20"; called "--onboot 1"; called "pct push 210"; called "systemctl enable --now mtmon"
called "pct push 210 .*apply-update.sh /usr/local/bin/mtmon-apply-update --perms 0755"
[ "$(stat -c %a /root/mtmon-210-credentials.txt 2>/dev/null)" = 600 ] && { pass=$((pass+1)); echo "PASS  credentials file is 0600"; } || { fail=$((fail+1)); echo "FAIL  credentials file mode"; }
reset; t "failed create keeps host clean (no destroy by default)" 1 env STUB_CREATE_FAIL=1 "$I" --yes; notcalled "pct destroy"
reset; t "cleanup-on-fail removes only our CT" 1 env STUB_CREATE_FAIL=1 "$I" --yes --cleanup-on-fail; called "pct destroy 210"

echo "== update =="
reset; "$I" --yes >/dev/null 2>&1; : > "$FAKE_STATE/calls.log"
cp "$WORK/mtmon-1.0.0" "$WORK/d/mtmon"; "$WORK/d/install-mtmon.sh" --yes --ctid 210 >/dev/null 2>&1 || true
reset; touch "$FAKE_STATE/ct_210"; echo "mtmon" > "$FAKE_STATE/ct_210.conf"; echo 0.9.0 > "$FAKE_STATE/ct_version"
t "update to good version" 0 "$U" --ctid 210 --binary "$WORK/mtmon-1.0.0"; has "updated to 1.0.0"; called "pct snapshot 210"; called "systemctl stop mtmon"
called "mtmon-apply-update --perms 0755"; called "/etc/systemd/system/mtmon.service --perms 0644"; called "rm -f /var/lib/mtmon/update/mtmon.new"; called "systemctl daemon-reload"
[ "$(cat "$FAKE_STATE/ct_version")" = 1.0.0 ] && { pass=$((pass+1)); echo "PASS  CT now runs 1.0.0"; } || { fail=$((fail+1)); echo "FAIL  CT version"; }
t "same binary is a no-op" 0 "$U" --ctid 210 --binary "$WORK/mtmon-1.0.0"; has "nothing to do"
reset; touch "$FAKE_STATE/ct_210"; echo "mtmon" > "$FAKE_STATE/ct_210.conf"; echo 1.0.0 > "$FAKE_STATE/ct_version"
t "bad update rolls back automatically" 1 env STUB_BAD_VERSION=9.9.9-bad "$U" --ctid 210 --binary "$WORK/mtmon-9.9.9-bad"; has "ROLLING BACK"; has "old version 1.0.0 is running again"
[ "$(cat "$FAKE_STATE/ct_version")" = 1.0.0 ] && { pass=$((pass+1)); echo "PASS  rollback restored 1.0.0"; } || { fail=$((fail+1)); echo "FAIL  version after rollback: $(cat "$FAKE_STATE/ct_version")"; }
reset; touch "$FAKE_STATE/ct_210"; echo "mtmon" > "$FAKE_STATE/ct_210.conf"; echo 1.0.0 > "$FAKE_STATE/ct_version"
t "snapshot unsupported -> vzdump fallback" 0 env STUB_NO_SNAPSHOT=1 "$U" --ctid 210 --binary "$WORK/mtmon-0.9.0"; called "vzdump 210"
reset; touch "$FAKE_STATE/ct_210"; echo "mtmon" > "$FAKE_STATE/ct_210.conf"; echo 1.0.0 > "$FAKE_STATE/ct_version"
t "no backup possible aborts before touching CT" 1 env STUB_NO_SNAPSHOT=1 STUB_VZDUMP_FAIL=1 "$U" --ctid 210 --binary "$WORK/mtmon-0.9.0"; notcalled "systemctl stop mtmon"
reset; touch "$FAKE_STATE/ct_300"; echo "some other vm" > "$FAKE_STATE/ct_300.conf"
t "refuses a CT that is not mtmon" 1 "$U" --ctid 300 --binary "$WORK/mtmon-1.0.0"; notcalled "pct push"

echo "== uninstall =="
reset; touch "$FAKE_STATE/ct_210"; echo mtmon > "$FAKE_STATE/ct_210.conf"
t "requires --confirm" 1 "$X" --ctid 210; notcalled "pct destroy"
t "wrong confirm refused" 1 "$X" --ctid 210 --confirm 211; notcalled "pct destroy"
t "dry-run" 0 "$X" --ctid 210 --confirm 210 --dry-run; notcalled "pct destroy"
reset; touch "$FAKE_STATE/ct_300"; echo "other" > "$FAKE_STATE/ct_300.conf"
t "refuses non-mtmon CT" 1 "$X" --ctid 300 --confirm 300; notcalled "pct destroy"
reset; touch "$FAKE_STATE/ct_210"; echo mtmon > "$FAKE_STATE/ct_210.conf"
t "backup failure aborts (nothing destroyed)" 1 env STUB_VZDUMP_FAIL=1 "$X" --ctid 210 --confirm 210; notcalled "pct destroy"
t "confirmed uninstall: backup first, then destroy" 0 "$X" --ctid 210 --confirm 210; called "vzdump 210"; called "pct destroy 210"

rm -f /root/mtmon-210-credentials.txt
echo; echo "passed=$pass failed=$fail"; [ "$fail" = 0 ]
