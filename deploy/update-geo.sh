#!/bin/sh
# update-geo.sh - fetch offline GeoIP/ASN (DB-IP Lite, CC BY 4.0) and the IEEE OUI vendor list.
# Runs INSIDE the mtmon container (installed as /usr/local/bin/mtmon-update-geo, monthly via systemd timer).
# Atomic: files are only replaced after a successful, plausible download. The service is restarted to reload them.
set -eu
DIR=${MTMON_GEO_DIR:-/var/lib/mtmon/geo}
mkdir -p "$DIR"

fetch() { # url dest
  if command -v curl >/dev/null 2>&1; then curl -fsSL -m 300 -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then wget -q -T 300 -O "$2" "$1"
  else echo "need curl or wget:  apt-get install -y curl" >&2; return 2; fi
}
size() { wc -c < "$1" | tr -d ' '; }

get_dbip() { # kind(country|asn)
  kind=$1
  for ym in "$(date +%Y-%m)" "$(date -d '-1 month' +%Y-%m 2>/dev/null || date -v-1m +%Y-%m)" "$(date -d '-2 month' +%Y-%m 2>/dev/null || echo)"; do
    [ -n "$ym" ] || continue
    tmp="$DIR/.dbip-$kind.gz"
    if fetch "https://download.db-ip.com/free/dbip-$kind-lite-$ym.mmdb.gz" "$tmp" 2>/dev/null; then
      gunzip -f "$tmp"
      raw="${tmp%.gz}"
      if [ "$(size "$raw")" -gt 1000000 ]; then mv -f "$raw" "$DIR/dbip-$kind.mmdb"; echo "ok dbip-$kind ($ym)"; return 0; fi
      rm -f "$raw"
    fi
    rm -f "$tmp"
  done
  echo "FAILED dbip-$kind (keeping previous file if any)" >&2; return 1
}

rc=0
get_dbip country || rc=1
get_dbip asn || rc=1
if fetch "https://standards-oui.ieee.org/oui/oui.csv" "$DIR/.oui.csv" && [ "$(size "$DIR/.oui.csv")" -gt 500000 ]; then
  mv -f "$DIR/.oui.csv" "$DIR/oui.csv"; echo "ok oui.csv"
else rm -f "$DIR/.oui.csv"; echo "FAILED oui.csv" >&2; rc=1; fi

chown -R mtmon:mtmon "$DIR" 2>/dev/null || true
if [ "$rc" = 0 ] && command -v systemctl >/dev/null 2>&1; then systemctl restart mtmon || true; fi
exit $rc
