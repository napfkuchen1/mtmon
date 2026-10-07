#!/usr/bin/env bash
# shellcheck disable=SC2015
# Tests the interactive dialog of install.sh with a scripted fake whiptail and fake Proxmox tools.
# Usage: deploy/test/test-tui.sh   (run as root: install.sh refuses otherwise)
set -uo pipefail
T=$(cd "$(dirname "$0")" && pwd); D=$(dirname "$T"); R=$(dirname "$D")
WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT
export FAKE_STATE=$WORK/state PATH="$T/stubs:$PATH" MTMON_FORCE_TUI=1 MTMON_BASE_URL="file://$WORK/rel"
# fake release: scripts from this checkout + shell-wrapper "binary"
mkdir -p "$WORK/rel/mtmon" && cp "$D"/*.sh "$D"/*.service "$D"/*.conf "$WORK/rel/mtmon/"
# shellcheck disable=SC2016
printf '#!/bin/sh\n[ "$1" = version ] && echo 1.0.0\n' > "$WORK/rel/mtmon/mtmon"; chmod +x "$WORK/rel/mtmon/mtmon"
(cd "$WORK/rel/mtmon" && sha256sum ./mtmon ./*.sh ./*.service ./*.conf > SHA256SUMS)
(cd "$WORK/rel" && tar czf mtmon-linux-amd64.tar.gz mtmon && sha256sum mtmon-linux-amd64.tar.gz > mtmon-linux-amd64.tar.gz.sha256)
pass=0; fail=0
reset() { rm -rf "$FAKE_STATE"; mkdir -p "$FAKE_STATE"; : > "$FAKE_STATE/calls.log"; rm -f /root/mtmon-*-credentials.txt; }
answers() { printf '%s\n' "$@" > "$FAKE_STATE/answers"; }
ok() { pass=$((pass+1)); echo "PASS  $1"; }
no() { fail=$((fail+1)); echo "FAIL  $1"; [ -z "${2:-}" ] || echo "$2" | tail -12 | sed 's/^/        /'; }
run() { OUT=$("$R/install.sh" "$@" 2>&1 </dev/null); RC=$?; }
has() { grep -q -- "$1" <<<"$OUT" && ok "output has: $1" || no "output missing: $1" "$OUT"; }
called() { grep -q -- "$1" "$FAKE_STATE/calls.log" && ok "called: $1" || no "never called: $1" "$(cat "$FAKE_STATE/calls.log")"; }
notcalled() { grep -q -- "$1" "$FAKE_STATE/calls.log" && no "unexpectedly called: $1" || ok "not called: $1"; }
wtlog() { grep -q -- "$1" "$FAKE_STATE/wt.log" && ok "dialog showed: $1" || no "dialog never showed: $1"; }

echo "== install dialog =="
reset; answers "0|install" "0|212" "0|mtmon" "0|local-lvm" "0|vmbr0" "0|" "0|dhcp" "0|default" "1|" "0|go"
run; [ $RC = 0 ] && ok "default flow exits 0" || no "rc=$RC" "$OUT"
called "pct create 212"; called "rootfs local-lvm:8"; called "ip=dhcp"; has "mtmon is running"; wtlog "211"; wtlog "vmbr1"
reset; answers "0|install" "0|212" "0|web1" "0|local" "0|vmbr1" "0|20" "0|static" "0|192.168.88.50/24" "0|192.168.88.1" "0|1.1.1.1" "0|custom" "0|4" "0|2048" "0|16" "0|9443" "0|2056" "0|" "0|go"
run; [ $RC = 0 ] && ok "custom flow exits 0" || no "rc=$RC" "$OUT"
called "pct create 212"; called "rootfs local:16"; called "ip=192.168.88.50/24,gw=192.168.88.1,tag=20"; called "bridge=vmbr1" || true
reset; touch "$FAKE_STATE/ct_212"; answers "0|install" "0|212" "0|" "0|213" "0|mtmon" "0|local-lvm" "0|vmbr0" "0|" "0|dhcp" "0|default" "1|" "0|go"
run; called "pct create 213"; wtlog "already belongs"
reset; answers "0|install" "0|212" "0|bad_host!" "0|" "0|mtmon" "0|local-lvm" "0|vmbr0" "0|99999" "0|" "0|dhcp" "0|default" "1|" "0|go"
run; wtlog "Invalid hostname"; wtlog "not a valid VLAN"; called "pct create 212"
reset; answers "0|install" "0|212" "0|mtmon" "0|local-lvm" "0|vmbr0" "0|" "0|dhcp" "0|default" "1|" "0|dry"
run; notcalled "pct create"; has "Plan"
reset; answers "0|install" "0|212" "0|mtmon" "1|"
run; [ $RC = 0 ] && ok "cancel mid-way exits 0" || no "rc=$RC"; notcalled "pct create"
reset; answers "1|"
run; [ $RC = 0 ] && ok "cancel at main menu exits 0" || no "rc=$RC"; notcalled "pct create"

echo "== update / uninstall dialog =="
reset; answers "0|install" "0|210" "0|mtmon" "0|local-lvm" "0|vmbr0" "0|" "0|dhcp" "0|default" "1|" "0|go"; run
answers "0|update" "0|210" "0|"; run; has "downloading"; wtlog "mtmon containers"; has "nothing to do"
reset; run; answers "0|update"; run; wtlog "No mtmon container created"
reset; answers "0|install" "0|210" "0|mtmon" "0|local-lvm" "0|vmbr0" "0|" "0|dhcp" "0|default" "1|" "0|go"; run
answers "0|uninstall" "0|210" "0|999"; run; wtlog "did not match"; notcalled "pct destroy"
answers "0|uninstall" "0|210" "0|210" "0|local"; run; called "pct destroy 210"; called "vzdump 210"

echo "== non-interactive =="
reset; run install --dry-run --yes; [ $RC = 0 ] && ok "args pass through" || no "rc=$RC" "$OUT"; notcalled "pct create"
reset; unset MTMON_FORCE_TUI; run; [ $RC != 0 ] && ok "no tty + no args refuses" || no "should refuse"; has "no terminal"
run bogus; [ $RC != 0 ] && ok "unknown action refused" || no "should refuse"
echo; echo "passed=$pass failed=$fail"; [ "$fail" = 0 ]
