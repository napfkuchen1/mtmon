#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Demo E2E smoke: mockros + mtmon + flowgen on loopback, then API checks. Needs bin/{mtmon,mockros,flowgen} and curl.
# Used by the nightly workflow; exits non-zero on the first failed check.
set -euo pipefail
cd "$(dirname "$0")/.."
D=$(mktemp -d); PIDS=()
cleanup() { for p in "${PIDS[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null || true; done; rm -rf "$D"; }
trap cleanup EXIT
BASE=http://127.0.0.1:18443
cat > "$D/config.json" <<CFG
{"listen":"127.0.0.1:18443","no_tls":true,"flow_listen":"127.0.0.1:12055","syslog_listen":"127.0.0.1:15514","data_dir":"$D/data",
 "devices":[{"name":"gw-main","addr":"127.0.0.1","port":9001,"scheme":"http","role":"router","user":"admin","pass":"adminpw"},
            {"name":"ap-living","addr":"127.0.0.1","port":9002,"scheme":"http","role":"ap","user":"admin","pass":"adminpw"}]}
CFG
chmod 600 "$D/config.json"
MTMON_PASSWORD='demo-password-1' bin/mtmon passwd -c "$D/config.json" >/dev/null
bin/mockros >"$D/mockros.log" 2>&1 & PIDS+=($!)
bin/mtmon run -c "$D/config.json" >"$D/mtmon.log" 2>&1 & PIDS+=($!)
fail() { echo "FAIL: $*"; echo "--- mtmon.log (tail)"; tail -30 "$D/mtmon.log" || true; exit 1; }

for i in $(seq 1 40); do
  curl -fs "$BASE/healthz" >/dev/null 2>&1 && break
  [ "$i" = 40 ] && fail "healthz never came up"
  sleep 0.5
done
echo "ok   healthz"
bin/flowgen -target 127.0.0.1:12055 -rate 300 -duration 45s -clientips 192.168.88.23,192.168.88.30,192.168.88.41,192.168.88.50 >"$D/flowgen.log" 2>&1 & PIDS+=($!)

J="$D/cookies"
curl -fs -c "$J" -H 'Content-Type: application/json' -d '{"user":"admin","password":"demo-password-1"}' "$BASE/api/login" >/dev/null || fail "login"
echo "ok   login"
get() { curl -fs -m 15 -b "$J" "$BASE$1" || fail "GET $1"; }
# list endpoints must answer a JSON array, never null (the UI crashes on null)
for p in /api/devices '/api/clients?range=1h' /api/alerts; do
  [ "$(get "$p" | head -c1)" = "[" ] || fail "$p is not a JSON array"
  echo "ok   $p is an array"
done
n=$(get /api/devices | grep -o '"name"' | wc -l); [ "$n" -ge 2 ] || fail "expected 2 mock devices, got $n"
echo "ok   2 devices"
# polls and flows need a moment to land in the store: wait for clients to show up
for i in $(seq 1 30); do
  c=$(get "/api/clients?range=1h" | grep -o "\"client\"" | wc -l || true); echo "poll $i clients=$c"
  [ "$c" -ge 1 ] && break
  [ "$i" = 30 ] && fail "no clients after 60 s"
  sleep 2
done
echo "ok   $c clients seen"
for p in '/api/overview?range=1h' /api/topology /api/system /api/setup; do get "$p" >/dev/null; echo "ok   GET $p"; done
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/overview?range=1h")
[ "$code" = 401 ] || fail "unauthenticated /api/overview gave $code, expected 401"
echo "ok   unauthenticated request is refused"
echo "E2E smoke passed"
