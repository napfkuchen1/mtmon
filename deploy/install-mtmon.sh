#!/usr/bin/env bash
# install-mtmon.sh - create a lightweight unprivileged LXC on Proxmox VE 9 and install mtmon.
# Run as root ON THE PROXMOX HOST. Safe to re-run: refuses to touch an existing CT.
set -Eeuo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
. "$HERE/lib.sh"

# ---- defaults (override with options or MTMON_* env) ----
CTID=${MTMON_CTID:-210}
HOSTNAME_CT=${MTMON_HOSTNAME:-mtmon}
STORAGE=${MTMON_STORAGE:-local-lvm}
TPL_STORAGE=${MTMON_TEMPLATE_STORAGE:-local}
BRIDGE=${MTMON_BRIDGE:-vmbr0}
VLAN=${MTMON_VLAN:-}
IPCFG=${MTMON_IP:-dhcp}
GW=${MTMON_GW:-}
DNS=${MTMON_DNS:-}
CORES=${MTMON_CORES:-2}
MEMORY=${MTMON_MEMORY:-1024}
SWAP=${MTMON_SWAP:-256}
DISK=${MTMON_DISK:-8}
UI_PORT=${MTMON_UI_PORT:-8443}
FLOW_PORT=${MTMON_FLOW_PORT:-2055}
BINARY=${MTMON_BINARY:-$HERE/mtmon}
DEVICES_FILE=${MTMON_DEVICES_FILE:-}
WITH_GEO=0
ASSUME_YES=0
CLEANUP_ON_FAIL=0
CREATED=0

usage() {
  cat <<USG
Usage: $0 [options]
  --ctid N               container id (default $CTID)
  --hostname NAME        (default $HOSTNAME_CT)
  --storage NAME         rootfs storage (default $STORAGE)
  --template-storage N   where CT templates live (default $TPL_STORAGE)
  --bridge vmbrX         (default $BRIDGE)       --vlan TAG
  --ip dhcp|A.B.C.D/NN   (default dhcp)          --gw A.B.C.D     --dns A.B.C.D
  --cores N --memory MB --swap MB --disk GB      (default $CORES / $MEMORY / $SWAP / $DISK)
  --ui-port N            (default $UI_PORT)      --flow-port N (UDP, default $FLOW_PORT)
  --binary PATH          mtmon binary (default: ./mtmon next to this script)
  --devices-file FILE    JSON array of routers/APs to monitor (see config.example.json); can be added later
  --with-geo             also fetch GeoIP/ASN (DB-IP Lite) + OUI vendor list now and monthly (needs internet in the CT)
  --yes                  do not ask for confirmation
  --cleanup-on-fail      destroy the CT if installation fails (default: keep it for debugging)
  --dry-run              print every change instead of doing it
USG
}

while [ $# -gt 0 ]; do
  case $1 in
    --ctid) CTID=$2; shift 2;; --hostname) HOSTNAME_CT=$2; shift 2;; --storage) STORAGE=$2; shift 2;;
    --template-storage) TPL_STORAGE=$2; shift 2;; --bridge) BRIDGE=$2; shift 2;; --vlan) VLAN=$2; shift 2;;
    --ip) IPCFG=$2; shift 2;; --gw) GW=$2; shift 2;; --dns) DNS=$2; shift 2;;
    --cores) CORES=$2; shift 2;; --memory) MEMORY=$2; shift 2;; --swap) SWAP=$2; shift 2;; --disk) DISK=$2; shift 2;;
    --ui-port) UI_PORT=$2; shift 2;; --flow-port) FLOW_PORT=$2; shift 2;;
    --binary) BINARY=$2; shift 2;; --devices-file) DEVICES_FILE=$2; shift 2;;
    --with-geo) WITH_GEO=1; shift;; --yes|-y) ASSUME_YES=1; shift;; --cleanup-on-fail) CLEANUP_ON_FAIL=1; shift;;
    --dry-run) DRY_RUN=1; shift;; -h|--help) usage; exit 0;;
    *) usage >&2; die "unknown option: $1";;
  esac
done

on_error() {
  local rc=$? line=$1
  warn "installation failed at line $line (exit $rc)"
  if [ "$CREATED" = 1 ] && ct_exists "$CTID"; then
    if [ "$CLEANUP_ON_FAIL" = 1 ]; then
      # only ever remove a CT that carries this installer's marker (never a foreign CT that appeared meanwhile)
      if pct config "$CTID" 2>/dev/null | grep -q 'by install-mtmon.sh'; then
        warn "--cleanup-on-fail: removing the half-installed CT $CTID that this run created"
        pct stop "$CTID" >/dev/null 2>&1 || true
        pct destroy "$CTID" --purge 1 >/dev/null 2>&1 || true
      else
        warn "CT $CTID does not carry the installer marker - NOT removing it"
      fi
    else
      warn "CT $CTID was kept for debugging:  pct enter $CTID   |  journalctl -u mtmon"
      warn "to remove it:  pct stop $CTID; pct destroy $CTID --purge 1     (or re-run with --cleanup-on-fail)"
    fi
  fi
  exit "$rc"
}
trap 'on_error $LINENO' ERR

# ---------------- preflight ----------------
info "preflight checks"
need_root
need_cmds pct pveam pvesm pveversion ip awk sed sha256sum
MAJOR=$(pve_major || true)
[ -n "${MAJOR:-}" ] || die "cannot determine Proxmox VE version (pveversion)"
[ "$MAJOR" -ge 9 ] || die "Proxmox VE 9 or newer required (found $MAJOR.x). Older versions may work, but Debian 13 templates are expected"
ok "Proxmox VE $MAJOR.x"

[[ $CTID =~ ^[0-9]+$ ]] && [ "$CTID" -ge 100 ] || die "invalid --ctid '$CTID'"
if ct_exists "$CTID"; then die "CT/VM $CTID already exists. Choose another --ctid (free id: $(pvesh get /cluster/nextid 2>/dev/null || echo '?')). Use update-mtmon.sh to upgrade an existing install"; fi
ok "CT id $CTID is free"

pvesm status 2>/dev/null | awk -v s="$STORAGE" 'NR>1 && $1==s && $3=="active" {f=1} END{exit !f}' \
  || die "storage '$STORAGE' not found or not active (pvesm status)"
pvesm status --content rootdir 2>/dev/null | awk -v s="$STORAGE" 'NR>1 && $1==s {f=1} END{exit !f}' \
  || die "storage '$STORAGE' cannot hold container root disks"
ok "storage $STORAGE"

ip link show "$BRIDGE" >/dev/null 2>&1 || die "bridge '$BRIDGE' not found (ip link show)"
ok "bridge $BRIDGE"

[ -f "$BINARY" ] || die "binary not found: $BINARY (build with 'make release' or pass --binary)"
if [ -f "$HERE/SHA256SUMS" ] && grep -q " \*\?$(basename "$BINARY")\$" "$HERE/SHA256SUMS"; then
  (cd "$(dirname "$BINARY")" && sha256sum -c --ignore-missing "$HERE/SHA256SUMS" >/dev/null) || die "SHA256 mismatch for $BINARY"
  ok "checksum verified"
fi
chmod +x "$BINARY" 2>/dev/null || true
VERSION=$("$BINARY" version 2>/dev/null) || die "cannot execute $BINARY on this host (wrong CPU architecture?)"
ok "binary runs (version $VERSION)"

if [ -n "$DEVICES_FILE" ]; then [ -f "$DEVICES_FILE" ] || die "devices file not found: $DEVICES_FILE"; fi
[ "$IPCFG" = dhcp ] || [ -n "$GW" ] || warn "static IP without --gw: the container will have no default route"

# template: newest debian-13-standard in $TPL_STORAGE, download if missing
TEMPLATE=$(pveam list "$TPL_STORAGE" 2>/dev/null | awk '/debian-13-standard/ {print $1}' | sort -V | tail -n1 || true)
if [ -z "$TEMPLATE" ]; then
  info "no Debian 13 template in '$TPL_STORAGE' - downloading"
  run pveam update >/dev/null
  NAME=$(pveam available --section system 2>/dev/null | awk '/debian-13-standard/ {print $2}' | sort -V | tail -n1 || true)
  if [ -z "$NAME" ] && [ "$DRY_RUN" != 1 ]; then die "no debian-13-standard template available (pveam available)"; fi
  NAME=${NAME:-debian-13-standard_DRYRUN_amd64.tar.zst}
  run pveam download "$TPL_STORAGE" "$NAME"
  TEMPLATE="$TPL_STORAGE:vztmpl/$NAME"
fi
ok "template $TEMPLATE"

NET="name=eth0,bridge=$BRIDGE,firewall=1"
[ "$IPCFG" = dhcp ] && NET="$NET,ip=dhcp" || NET="$NET,ip=$IPCFG"
[ -n "$GW" ] && NET="$NET,gw=$GW"
[ -n "$VLAN" ] && NET="$NET,tag=$VLAN"

cat <<PLAN

  Plan
  ----
  CT id / hostname   : $CTID / $HOSTNAME_CT   (unprivileged, Debian 13, nesting=1, onboot)
  Resources          : $CORES vCPU, ${MEMORY} MB RAM, ${SWAP} MB swap, ${DISK} GB on $STORAGE
  Network            : $NET
  mtmon $VERSION      : UI :$UI_PORT (TLS self-signed), IPFIX/NetFlow UDP :$FLOW_PORT
  Devices            : ${DEVICES_FILE:-none yet (add in /etc/mtmon/config.json inside the CT)}
  Rollback           : pct stop $CTID; pct destroy $CTID --purge 1   (nothing else on the host is modified)

PLAN
if [ "$ASSUME_YES" != 1 ] && [ "$DRY_RUN" != 1 ]; then
  read -r -p "Proceed? [y/N] " ans; [[ $ans =~ ^[Yy]$ ]] || die "aborted by user"
fi

# ---------------- create ----------------
info "creating container $CTID"
CREATED=1
run pct create "$CTID" "$TEMPLATE" \
  --hostname "$HOSTNAME_CT" --unprivileged 1 --features nesting=1 \
  --cores "$CORES" --memory "$MEMORY" --swap "$SWAP" \
  --rootfs "$STORAGE:$DISK" --net0 "$NET" ${DNS:+--nameserver "$DNS"} \
  --onboot 1 --ostype debian --tags mtmon \
  --description "mtmon - MikroTik realtime monitor (installed $(date -u +%F) by install-mtmon.sh)"
run pct start "$CTID"
if [ "$DRY_RUN" != 1 ]; then
  wait_for pct exec "$CTID" -- true || die "container did not start"
  WAIT_SECS=60 wait_for pct exec "$CTID" -- systemctl is-system-running --wait >/dev/null 2>&1 || true
fi
ok "container running"

# ---------------- install inside ----------------
info "installing mtmon"
ct_exec "$CTID" sh -c 'id mtmon >/dev/null 2>&1 || useradd --system --user-group --home-dir /var/lib/mtmon --shell /usr/sbin/nologin mtmon'
ct_exec "$CTID" install -d -m 0750 -o root -g mtmon /etc/mtmon
run pct push "$CTID" "$BINARY" /usr/local/bin/mtmon --perms 0755
run pct push "$CTID" "$HERE/mtmon.service" /etc/systemd/system/mtmon.service --perms 0644
INIT_ARGS=(-c /etc/mtmon/config.json -ui-port "$UI_PORT" -flow-port "$FLOW_PORT")
if [ -n "$DEVICES_FILE" ]; then
  run pct push "$CTID" "$DEVICES_FILE" /root/devices.json --perms 0600
  INIT_ARGS+=(-devices /root/devices.json)
fi
ct_exec "$CTID" /usr/local/bin/mtmon init "${INIT_ARGS[@]}"
PASS=""
if [ "$DRY_RUN" = 1 ]; then
  info "[dry] would generate the admin password inside the CT"
else
  PASS=$(pct exec "$CTID" -- /usr/local/bin/mtmon passwd -c /etc/mtmon/config.json --generate | awk -F': ' '/^password:/ {print $2}')
  [ -n "$PASS" ] || die "could not generate admin password"
fi
ct_exec "$CTID" chown root:mtmon /etc/mtmon/config.json
ct_exec "$CTID" chmod 0640 /etc/mtmon/config.json
[ -n "$DEVICES_FILE" ] && ct_exec "$CTID" rm -f /root/devices.json
if [ "$WITH_GEO" = 1 ]; then
  run pct push "$CTID" "$HERE/update-geo.sh" /usr/local/bin/mtmon-update-geo --perms 0755
  run pct push "$CTID" "$HERE/mtmon-geo.service" /etc/systemd/system/mtmon-geo.service --perms 0644
  run pct push "$CTID" "$HERE/mtmon-geo.timer" /etc/systemd/system/mtmon-geo.timer --perms 0644
fi
ct_exec "$CTID" systemctl daemon-reload
if [ "$WITH_GEO" = 1 ]; then
  # best effort: a failed download must not fail the installation (no internet in the CT, etc.)
  run pct exec "$CTID" -- /usr/local/bin/mtmon-update-geo || warn "GeoIP download failed - run mtmon-update-geo inside the CT later"
  ct_exec "$CTID" systemctl enable --now mtmon-geo.timer
fi
ct_exec "$CTID" systemctl enable --now mtmon

# ---------------- verify (with sandbox fallback) ----------------
if [ "$DRY_RUN" != 1 ]; then
  if ! WAIT_SECS=15 wait_for pct exec "$CTID" -- systemctl is-active --quiet mtmon; then
    if pct exec "$CTID" -- journalctl -u mtmon -n 30 --no-pager 2>/dev/null | grep -Eq 'NAMESPACE|status=226|Failed to set up'; then
      warn "the container rejected systemd sandbox options - applying the relaxed drop-in (see docs/runbook.md)"
      pct exec "$CTID" -- mkdir -p /etc/systemd/system/mtmon.service.d
      pct push "$CTID" "$HERE/relax.conf" /etc/systemd/system/mtmon.service.d/00-relax.conf --perms 0644
      pct exec "$CTID" -- systemctl daemon-reload
      pct exec "$CTID" -- systemctl restart mtmon
    fi
  fi
  WAIT_SECS=30 wait_for pct exec "$CTID" -- /usr/local/bin/mtmon selftest -c /etc/mtmon/config.json \
    || { pct exec "$CTID" -- journalctl -u mtmon -n 40 --no-pager || true; pct exec "$CTID" -- /usr/local/bin/mtmon selftest -c /etc/mtmon/config.json || true; die "post-install self-test failed"; }
  ok "self-test passed"
  CT_IP=$(pct exec "$CTID" -- hostname -I 2>/dev/null | awk '{print $1}')
  CRED=/root/mtmon-$CTID-credentials.txt
  ( umask 077; printf 'url: https://%s:%s\nuser: admin\npassword: %s\n' "${CT_IP:-<ct-ip>}" "$UI_PORT" "$PASS" > "$CRED" )
  cat <<DONE

  ${GRN}mtmon is running.${RST}
    UI        : https://${CT_IP:-<ct-ip>}:$UI_PORT      (self-signed certificate)
    user      : admin
    password  : $PASS
    (also saved to $CRED - mode 0600 - delete it after storing the password safely)

  Next steps
    1. Open the UI -> Devices -> Add device: enter router IP + one-time admin login.
       mtmon shows the plan, sets up read-only user + Traffic Flow + syslog, forgets the admin login.
       (Removal later: device page -> Offboarding restores everything.)
    2. Allow from your routers: UDP $FLOW_PORT (flows) and UDP 5514 (firewall syslog); TCP $UI_PORT from your admin network
    Rollback: pct stop $CTID; pct destroy $CTID --purge 1
DONE
fi
