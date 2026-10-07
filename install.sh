#!/usr/bin/env bash
# mtmon installer - run as root ON THE PROXMOX VE HOST.
#
#   bash -c "$(curl -fsSL https://raw.githubusercontent.com/napfkuchen1/mtmon/main/install.sh)"
#
# Without arguments it opens a small dialog (install / update / uninstall) that asks for
# container ID (next free one suggested), storage, bridge, network and resources.
# Non-interactive use:
#   ... install.sh install --ctid 210 --storage local-lvm --bridge vmbr0 --yes
#   ... install.sh update --ctid 210
#   ... install.sh uninstall --ctid 210 --confirm 210
#   ... install.sh install --dry-run          (show every step, change nothing)
#
# Env: MTMON_REPO (owner/name, default napfkuchen1/mtmon) - MTMON_VERSION (tag, default latest)
#      MTMON_BASE_URL (override download location, for tests)
set -Eeuo pipefail
REPO=${MTMON_REPO:-napfkuchen1/mtmon}
VER=${MTMON_VERSION:-latest}
ASSET=mtmon-linux-amd64.tar.gz
WT=${MTMON_WHIPTAIL:-whiptail}
BT="mtmon - realtime monitor for MikroTik routers & access points"
if [ -t 1 ]; then C_CY=$'\033[36m'; C_GR=$'\033[32m'; C_RD=$'\033[31m'; C_YL=$'\033[33m'; C_BD=$'\033[1m'; C_RS=$'\033[0m'; else C_CY=; C_GR=; C_RD=; C_YL=; C_BD=; C_RS=; fi
die() { printf '\n  %s✖ %s%s\n\n' "$C_RD" "$*" "$C_RS" >&2; exit 1; }
say() { printf '  %s▸%s %s\n' "$C_CY" "$C_RS" "$*"; }
# dark theme with cyan accents for the whiptail dialogs
export NEWT_COLORS=${NEWT_COLORS:-'root=,black window=white,black border=cyan,black title=cyan,black button=black,cyan actbutton=black,white compactbutton=white,black checkbox=white,black actcheckbox=black,cyan entry=white,blue label=white,black listbox=white,black actlistbox=black,cyan sellistbox=white,black actsellistbox=black,cyan textbox=white,black acttextbox=black,cyan helpline=white,black roottext=cyan,black emptyscale=,black fullscale=,cyan'}

# ------------------------------------------------------------------ dialog helpers
wt_menu() { # title text menu-height tag item ... (WT_DEFAULT = preselected tag)
  "$WT" --backtitle "$BT" --title "$1" ${WT_DEFAULT:+--default-item "$WT_DEFAULT"} --menu "$2" 20 74 "$3" "${@:4}" 3>&1 1>&2 2>&3
}
wt_input() { "$WT" --backtitle "$BT" --title "$1" --inputbox "$2" 11 70 "${3:-}" 3>&1 1>&2 2>&3; }
wt_msg() { "$WT" --backtitle "$BT" --title "$1" --msgbox "$2" 12 70; }
wt_yesno() { "$WT" --backtitle "$BT" --title "$1" --yesno "$2" 14 70; }

id_in_use() { pct status "$1" >/dev/null 2>&1 || qm status "$1" >/dev/null 2>&1; }
next_ctid() {
  local id
  id=$(pvesh get /cluster/nextid 2>/dev/null | tr -d '"[:space:]') || id=""
  if ! [[ $id =~ ^[0-9]+$ ]]; then
    id=100
    while id_in_use "$id"; do id=$((id + 1)); done
  fi
  echo "$id"
}

# our containers: created by install-mtmon.sh (marker in the CT description)
list_mtmon_cts() {
  local id name status
  while read -r id status; do
    [ -n "$id" ] || continue
    pct config "$id" 2>/dev/null | grep -q 'by install-mtmon.sh' || continue
    name=$(pct list | awk -v i="$id" '$1==i {print $NF}')
    printf '%s\n%s\n' "$id" "$name ($status)"
  done < <(pct list 2>/dev/null | awk 'NR>1 {print $1, $2}')
}

tui_install() {
  local ctid host storage bridge vlan net ip="dhcp" gw="" dns="" cores=2 mem=1024 disk=8 uip=8443 fport=2055 geo="" sug geotxt=no
  local -a items
  sug=$(next_ctid); ctid=$sug
  while :; do
    ctid=$(wt_input "Container ID" "ID of the new LXC container.\nSuggestion: next free ID on this cluster ($sug)." "$ctid") || exit 0
    if ! [[ $ctid =~ ^[0-9]+$ ]] || [ "$ctid" -lt 100 ]; then wt_msg "Invalid ID" "Please enter a number >= 100."; continue; fi
    if id_in_use "$ctid"; then wt_msg "ID in use" "ID $ctid already belongs to another guest. Pick a free one (suggested: $sug)."; continue; fi
    break
  done

  host=mtmon
  while :; do
    host=$(wt_input "Hostname" "Hostname of the container." "$host") || exit 0
    [[ $host =~ ^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$ ]] && break
    wt_msg "Invalid hostname" "Letters, digits and '-' only (no leading/trailing '-')."
  done

  mapfile -t items < <(pvesm status --content rootdir 2>/dev/null | awk 'NR>1 && $3=="active" {printf "%s\n%s, %.0f GiB free\n", $1, $2, $6/1048576}')
  [ "${#items[@]}" -gt 0 ] || die "no active storage that can hold container disks (pvesm status --content rootdir)"
  WT_DEFAULT=""
  if printf '%s\n' "${items[@]}" | grep -qx 'local-lvm'; then WT_DEFAULT=local-lvm; fi
  storage=$(wt_menu "Storage" "Where should the container disk live?" $(( ${#items[@]} / 2 )) "${items[@]}") || exit 0
  WT_DEFAULT=""

  mapfile -t items < <(ip -br link show type bridge 2>/dev/null | awk '{printf "%s\nLinux bridge (%s)\n", $1, $2}')
  if [ "${#items[@]}" -gt 0 ]; then
    if printf '%s\n' "${items[@]}" | grep -qx 'vmbr0'; then WT_DEFAULT=vmbr0; fi
    bridge=$(wt_menu "Network bridge" "Which bridge should the container use?\nIt must reach your MikroTik devices." $(( ${#items[@]} / 2 )) "${items[@]}") || exit 0
    WT_DEFAULT=""
  else
    bridge=$(wt_input "Network bridge" "No bridge detected automatically. Bridge name:" vmbr0) || exit 0
  fi

  vlan=$(wt_input "VLAN tag" "Optional VLAN tag for the container interface.\nLeave empty for none." "") || exit 0
  if [ -n "$vlan" ] && { ! [[ $vlan =~ ^[0-9]+$ ]] || [ "$vlan" -lt 1 ] || [ "$vlan" -gt 4094 ]; }; then
    wt_msg "Ignored" "'$vlan' is not a valid VLAN (1-4094) - continuing without VLAN."; vlan=""
  fi

  net=$(wt_menu "IP address" "How should the container get its address?\nA fixed address is recommended: your routers send flows to it." 2 \
    dhcp "DHCP (needs a reservation, otherwise the IP may change)" static "Static address") || exit 0
  if [ "$net" = static ]; then
    while :; do
      ip=$(wt_input "Static address" "Address with prefix length, e.g. 192.168.88.50/24" "") || exit 0
      [[ $ip =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}/[0-9]{1,2}$ ]] && break
      wt_msg "Invalid" "Format: A.B.C.D/NN"
    done
    gw=$(wt_input "Gateway" "Default gateway (e.g. 192.168.88.1)" "") || exit 0
    dns=$(wt_input "DNS server" "Optional DNS server for the container (empty = use host settings)." "") || exit 0
  fi

  net=$(wt_menu "Resources" "Recommended is enough for ~100 clients (about 45 MB RAM at 5,000 flows/s)." 2 \
    default "2 cores, 1024 MB RAM, 8 GB disk, UI port 8443, flow port 2055" custom "Choose values myself") || exit 0
  if [ "$net" = custom ]; then
    cores=$(wt_input "CPU cores" "Number of cores" "$cores") || exit 0
    mem=$(wt_input "Memory" "RAM in MB" "$mem") || exit 0
    disk=$(wt_input "Disk" "Disk size in GB" "$disk") || exit 0
    uip=$(wt_input "UI port" "TCP port of the web interface (HTTPS)" "$uip") || exit 0
    fport=$(wt_input "Flow port" "UDP port for IPFIX/NetFlow from the routers" "$fport") || exit 0
    for v in "$cores" "$mem" "$disk" "$uip" "$fport"; do
      [[ $v =~ ^[0-9]+$ ]] || { wt_msg "Invalid value" "'$v' is not a number. Please start again."; exit 1; }
    done
  fi

  if wt_yesno "GeoIP / vendor data" "Download free country/ASN data (DB-IP Lite) and the IEEE vendor list now and update them monthly?\n\nNeeds internet access from the container. Without it the UI shows no countries or vendors."; then
    geo=--with-geo; geotxt=yes
  fi

  local sum="Container : $ctid ($host)\nStorage   : $storage\nBridge    : $bridge${vlan:+ (VLAN $vlan)}\nAddress   : $ip${gw:+ via $gw}\nResources : $cores cores / $mem MB / $disk GB\nPorts     : UI tcp/$uip, flows udp/$fport\nGeo data  : $geotxt"
  net=$(wt_menu "Summary" "$sum\n\nNothing has been changed yet." 3 \
    go "Install now" dry "Preview only (shows every step, changes nothing)" cancel "Cancel") || exit 0
  [ "$net" != cancel ] || exit 0

  ARGS=(--ctid "$ctid" --hostname "$host" --storage "$storage" --bridge "$bridge" --ip "$ip" --cores "$cores" --memory "$mem" --disk "$disk" --ui-port "$uip" --flow-port "$fport" --yes)
  [ -z "$vlan" ] || ARGS+=(--vlan "$vlan")
  [ -z "$gw" ] || ARGS+=(--gw "$gw")
  [ -z "$dns" ] || ARGS+=(--dns "$dns")
  [ -z "$geo" ] || ARGS+=("$geo")
  [ "$net" != dry ] || ARGS+=(--dry-run)
  ACTION=install
}

tui_pick_ct() { # $1 = update | uninstall
  local -a items bk
  local id verb=$1 typed
  mapfile -t items < <(list_mtmon_cts)
  if [ "${#items[@]}" -eq 0 ]; then wt_msg "Nothing found" "No mtmon container created by this installer was found on this node."; exit 0; fi
  id=$(wt_menu "mtmon containers" "Which container do you want to $verb?" $(( ${#items[@]} / 2 )) "${items[@]}") || exit 0
  if [ "$verb" = update ]; then
    wt_yesno "Update CT $id" "The update takes a snapshot first, swaps the binary and rolls back automatically if the health check fails.\n\nContinue?" || exit 0
    ARGS=(--ctid "$id"); ACTION=update; return
  fi
  typed=$(wt_input "Remove CT $id" "This DESTROYS container $id including ALL monitoring data.\nA final backup (vzdump) is made first. Your routers are not touched.\n\nType the container ID to confirm:" "") || exit 0
  [ "$typed" = "$id" ] || { wt_msg "Aborted" "ID did not match - nothing was removed."; exit 0; }
  mapfile -t bk < <(pvesm status --content backup 2>/dev/null | awk 'NR>1 && $3=="active" {printf "%s\n%s\n", $1, $2}')
  if [ "${#bk[@]}" -eq 0 ]; then
    wt_yesno "No backup storage" "No storage that accepts backups was found. Remove the container WITHOUT a final backup?" || exit 0
    ARGS=(--ctid "$id" --confirm "$id" --no-backup)
  else
    DUMP_STORAGE=$(wt_menu "Backup storage" "Where should the final backup go?" $(( ${#bk[@]} / 2 )) "${bk[@]}") || exit 0
    export DUMP_STORAGE
    ARGS=(--ctid "$id" --confirm "$id")
  fi
  ACTION=uninstall
}

tui_main() {
  command -v pct >/dev/null || die "pct not found - run this on the Proxmox VE host"
  command -v "$WT" >/dev/null || die "whiptail not found - use the non-interactive form (install.sh --help)"
  local c
  c=$(wt_menu "mtmon" "Realtime monitoring for MikroTik routers and access points.\nRuns as a small unprivileged LXC on this Proxmox node.\n\nWhat do you want to do?" 3 \
    install "Create a new mtmon container" \
    update "Update an existing mtmon container" \
    uninstall "Remove an mtmon container (final backup first)") || exit 0
  case $c in install) tui_install ;; update | uninstall) tui_pick_ct "$c" ;; esac
  clear 2>/dev/null || true
}

# ------------------------------------------------------------------ entry
ACTION=${1:-}
ARGS=()
case $ACTION in
  install | update | uninstall) shift; ARGS=("$@") ;;
  -h | --help | help) sed -n '2,17p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//' || true; exit 0 ;;
  "") if [ -t 0 ] || [ -n "${MTMON_FORCE_TUI:-}" ]; then ACTION=menu; else die "no terminal for the dialog - pass install | update | uninstall (see --help)"; fi ;;
  *) die "first argument must be install | update | uninstall (got '$ACTION')" ;;
esac

[ "$(id -u)" = 0 ] || die "run as root on the Proxmox host"
for c in curl tar sha256sum; do command -v "$c" >/dev/null || die "missing command: $c"; done
[ "$ACTION" != menu ] || tui_main

if [ -n "${MTMON_BASE_URL:-}" ]; then BASE=$MTMON_BASE_URL
else
  case $REPO in */*) ;; *) die "MTMON_REPO must look like owner/name" ;; esac
  if [ "$VER" = latest ]; then BASE="https://github.com/$REPO/releases/latest/download"
  else BASE="https://github.com/$REPO/releases/download/$VER"; fi
fi

T=$(mktemp -d /tmp/mtmon-boot.XXXXXX); trap 'rm -rf "$T"' EXIT
printf '\n  %smtmon%s - installer\n\n' "$C_BD" "$C_RS"
say "downloading $ASSET"
curl -fsSL --retry 3 -o "$T/$ASSET" "$BASE/$ASSET" || die "download failed (release exists? repo public?)"
curl -fsSL --retry 3 -o "$T/$ASSET.sha256" "$BASE/$ASSET.sha256" || die "checksum download failed"
want=$(awk '{print $1}' "$T/$ASSET.sha256"); got=$(sha256sum "$T/$ASSET" | awk '{print $1}')
if [ -z "$want" ] || [ "$want" != "$got" ]; then die "checksum mismatch - aborting (expected $want, got $got)"; fi
printf '  %s✔%s checksum verified\n' "$C_GR" "$C_RS"
# Release signature (minisign). The checksum only proves the download is intact; the signature proves it was built by the maintainer.
MTMON_PUBKEY=RWSj18U8+XTlXnicS+OIExn6YF3O9+RC4U9VIaDLVtPFeMkp75YhedSK
if curl -fsSL --retry 3 -o "$T/$ASSET.minisig" "$BASE/$ASSET.minisig" 2>/dev/null; then
  if command -v minisign >/dev/null; then
    minisign -Vqm "$T/$ASSET" -x "$T/$ASSET.minisig" -P "$MTMON_PUBKEY" || die "signature check FAILED - aborting (the archive was not signed by the mtmon maintainer)"
    printf '  %s✔%s signature verified (minisign)\n' "$C_GR" "$C_RS"
  else
    printf '  %s!%s release is signed, but minisign is not installed here: signature not checked (apt install minisign)\n' "$C_YL" "$C_RS"
  fi
else
  printf '  %s!%s this release has no signature (older than v0.8.0): only the checksum was verified\n' "$C_YL" "$C_RS"
fi
tar xzf "$T/$ASSET" -C "$T"
[ -x "$T/mtmon/install-mtmon.sh" ] || die "unexpected archive layout"
( cd "$T/mtmon" && sha256sum -c SHA256SUMS >/dev/null ) || die "SHA256SUMS inside archive do not match"
cd "$T/mtmon"
# keep the unpacked files until the child finished (no exec, trap cleans up afterwards)
./"$ACTION"-mtmon.sh "${ARGS[@]}"
