#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-only
# update-geo.sh - fetch offline GeoIP/ASN (DB-IP Lite, CC BY 4.0) and the IEEE OUI vendor list.
# Runs INSIDE the mtmon container (installed as /usr/local/bin/mtmon-update-geo, monthly via systemd timer).
# Atomic: files are only replaced after a successful, plausible download. The service is restarted to reload them.
#
# The geo directory belongs to the service user, so the downloads never run as root: started as root, the script
# runs itself again as that user (setpriv) and keeps only the service restart for root. Writing as root into a
# directory another user controls would follow links planted there.
# Env (tests): MTMON_GEO_DIR, MTMON_GEO_USER (default mtmon), MTMON_GEO_NO_RESTART=1
set -eu
DIR=${MTMON_GEO_DIR:-/var/lib/mtmon/geo}
GEO_USER=${MTMON_GEO_USER:-mtmon}

restart_service() {
  [ -z "${MTMON_GEO_NO_RESTART:-}" ] || return 0
  if command -v systemctl >/dev/null 2>&1; then systemctl restart mtmon || true; fi
}

if [ "$(id -u)" = 0 ] && [ -z "${MTMON_GEO_CHILD:-}" ] && id "$GEO_USER" >/dev/null 2>&1; then
  uid=$(id -u "$GEO_USER"); gid=$(id -g "$GEO_USER")
  if [ "$uid" = 0 ] || ! command -v setpriv >/dev/null 2>&1; then
    echo "refusing to download as root into $DIR: need setpriv (util-linux) to run as $GEO_USER" >&2; exit 2
  fi
  # fresh install: the state dir may not exist yet (systemd creates it on first start). Its parent is root-only, so
  # creating it here is safe; the geo dir inside is then created by the service user.
  parent=$(dirname "$DIR")
  if [ ! -e "$parent" ]; then install -d -o "$uid" -g "$gid" -m 0750 "$parent"; fi
  rc=0
  MTMON_GEO_CHILD=1 setpriv --reuid="$uid" --regid="$gid" --clear-groups sh "$0" || rc=$?
  if [ "$rc" = 0 ]; then restart_service; fi
  exit "$rc"
fi

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
    rm -f "$tmp" "${tmp%.gz}"
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
rm -f "$DIR/.oui.csv"
if fetch "https://standards-oui.ieee.org/oui/oui.csv" "$DIR/.oui.csv" && [ "$(size "$DIR/.oui.csv")" -gt 500000 ]; then
  mv -f "$DIR/.oui.csv" "$DIR/oui.csv"; echo "ok oui.csv"
else rm -f "$DIR/.oui.csv"; echo "FAILED oui.csv" >&2; rc=1; fi

# started directly as the service user (or as root without that user, e.g. a bare test box): restart here
if [ "$rc" = 0 ] && [ -z "${MTMON_GEO_CHILD:-}" ]; then restart_service; fi
exit $rc
