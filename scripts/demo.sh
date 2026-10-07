#!/usr/bin/env bash
# Local demo without any MikroTik hardware: mock RouterOS REST devices + synthetic IPFIX traffic.
set -euo pipefail
cd "$(dirname "$0")/.."
D=$(mktemp -d); trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$D"' EXIT
cat > "$D/config.json" <<CFG
{"listen":"127.0.0.1:18443","no_tls":true,"flow_listen":"127.0.0.1:12055","syslog_listen":"127.0.0.1:15514","data_dir":"$D/data",
 "devices":[{"name":"gw-main","addr":"127.0.0.1","port":9001,"scheme":"http","role":"router","user":"admin","pass":"adminpw"},
            {"name":"ap-living","addr":"127.0.0.1","port":9002,"scheme":"http","role":"ap","user":"admin","pass":"adminpw"}]}
CFG
chmod 600 "$D/config.json"
MTMON_PASSWORD='demo-password-1' bin/mtmon passwd -c "$D/config.json" >/dev/null
bin/mockros & sleep 0.5
bin/mtmon run -c "$D/config.json" & sleep 2
echo "UI: http://127.0.0.1:18443   user: admin   password: demo-password-1   (Ctrl+C to stop)"
echo "Tipp: Geräte per Web-UI hinzufügen (Devices → Add device) – Mock-Login admin / adminpw auf 127.0.0.1:9001"
bin/flowgen -target 127.0.0.1:12055 -rate 300 -duration 0 -clientips 192.168.88.23,192.168.88.30,192.168.88.41,192.168.88.50
