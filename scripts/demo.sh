#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Local demo without any MikroTik hardware: a mock network (router, switch, two APs, 25 clients), fictional history
# (bin/demoseed: 36 h of traffic with countries, firewall events, alerts) and live synthetic IPFIX traffic.
# Remote addresses are documentation ranges (RFC 5737), clients RFC 1918. The mocks listen on 127.0.0.11-14 (and .20 as
# the spare router for the setup wizard); scripts/screenshots.js shows them as 192.168.88.1-5.
set -euo pipefail
cd "$(dirname "$0")/.."
D=$(mktemp -d); trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$D"' EXIT
# Git Bash on Windows: the data dir inside config.json is not path-converted for native binaries
DD="$D/data"; if command -v cygpath >/dev/null 2>&1; then DD=$(cygpath -m "$D/data"); fi
cat > "$D/config.json" <<CFG
{"listen":"127.0.0.1:18443","no_tls":true,"flow_listen":"127.0.0.1:12055","syslog_listen":"127.0.0.1:15514","data_dir":"$DD",
 "devices":[{"name":"gw-main","addr":"127.0.0.11","port":9001,"scheme":"http","role":"router","user":"admin","pass":"adminpw"},
            {"name":"sw-office","addr":"127.0.0.12","port":9001,"scheme":"http","role":"switch","user":"admin","pass":"adminpw"},
            {"name":"ap-living","addr":"127.0.0.13","port":9001,"scheme":"http","role":"ap","user":"admin","pass":"adminpw"},
            {"name":"ap-garden","addr":"127.0.0.14","port":9001,"scheme":"http","role":"ap","user":"admin","pass":"adminpw"}]}
CFG
chmod 600 "$D/config.json"
mkdir -p "$D/data"
bin/demoseed -dir "$DD"
MTMON_PASSWORD='demo-password-1' bin/mtmon passwd -c "$D/config.json" >/dev/null
bin/mockros -demo & sleep 0.5
bin/mtmon run -c "$D/config.json" & sleep 2
echo "UI: http://127.0.0.1:18443   user: admin   password: demo-password-1   (Ctrl+C to stop)"
echo "Tipp: Setup-Wizard (Devices -> Add device) gegen das freie Mock-Gerät 127.0.0.20, REST-Port 9001 (HTTPS), Login admin / adminpw"
bin/flowgen -target 127.0.0.1:12055 -src 127.0.0.11 -rate 300 -duration 0 -docdests -clientips "$(bin/demoseed -list-ips)"
