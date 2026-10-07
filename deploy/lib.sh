# shellcheck shell=bash disable=SC2034
# Shared helpers for install/update/uninstall. Sourced, not executed.

DRY_RUN=${DRY_RUN:-0}
RED=$'\033[31m'; GRN=$'\033[32m'; YLW=$'\033[33m'; CYN=$'\033[36m'; DIM=$'\033[2m'; BLD=$'\033[1m'; RST=$'\033[0m'
[ -t 1 ] || { RED=; GRN=; YLW=; CYN=; DIM=; BLD=; RST=; }
case "${LC_ALL:-${LC_CTYPE:-${LANG:-}}}" in
  *UTF-8*|*utf8*|*UTF8*) G_OK='✔'; G_INFO='▸'; G_WARN='▲'; G_ERR='✖'; G_DOT='·'; B_TL='╭'; B_TR='╮'; B_BL='╰'; B_BR='╯'; B_H='─'; B_V='│' ;;
  *) G_OK='ok'; G_INFO='>>'; G_WARN='!!'; G_ERR='xx'; G_DOT='-'; B_TL='+'; B_TR='+'; B_BL='+'; B_BR='+'; B_H='-'; B_V='|' ;;
esac

info()  { printf '  %s%s%s %s\n' "$CYN" "$G_INFO" "$RST" "$*"; }
ok()    { printf '  %s%s%s %s\n' "$GRN" "$G_OK" "$RST" "$*"; }
warn()  { printf '  %s%s%s %s\n' "$YLW" "$G_WARN" "$RST" "$*" >&2; }
die()   { printf '\n  %s%s %s%s\n\n' "$RED" "$G_ERR" "$*" "$RST" >&2; exit 1; }
# section TITLE : bold heading with a rule
section() { printf '\n%s%s%s\n' "$BLD" "$*" "$RST"; }

# box LINE... : rounded box around the given lines (no colour codes inside the lines)
box() {
  local w=0 l n
  for l in "$@"; do n=${#l}; [ "$n" -gt "$w" ] && w=$n; done
  local rule; rule=$(printf '%*s' $((w + 2)) '' | sed "s/ /$B_H/g")
  printf '  %s%s%s%s%s\n' "$CYN" "$B_TL" "$rule" "$B_TR" "$RST"
  for l in "$@"; do printf '  %s%s%s %-*s %s%s%s\n' "$CYN" "$B_V" "$RST" "$w" "$l" "$CYN" "$B_V" "$RST"; done
  printf '  %s%s%s%s%s\n' "$CYN" "$B_BL" "$rule" "$B_BR" "$RST"
}

# banner [ACTION] : product header
banner() {
  printf '\n'
  box "mtmon  ${MTMON_VERSION_STR:-}" "Realtime monitor for MikroTik routers & access points${1:+  $G_DOT  $1}"
  printf '\n'
}

# run CMD... : execute, or only print in --dry-run mode.
run() {
  if [ "$DRY_RUN" = 1 ]; then printf '  %s%s dry%s %s\n' "$DIM" "$G_DOT" "$RST" "$*"; return 0; fi
  "$@"
}

need_root() { [ "$(id -u)" -eq 0 ] || die "run as root on the Proxmox host"; }

need_cmds() {
  local c
  for c in "$@"; do command -v "$c" >/dev/null 2>&1 || die "missing command: $c (is this a Proxmox VE host?)"; done
}

# pve_major : prints the PVE major version (e.g. 9)
pve_major() {
  pveversion 2>/dev/null | sed -n 's|^pve-manager/\([0-9]\+\)\..*|\1|p' | head -n1
}

ct_exists() { pct status "$1" >/dev/null 2>&1; }
ct_running() { [ "$(pct status "$1" 2>/dev/null | awk '{print $2}')" = running ]; }

# ct_exec CTID cmd... (respects dry run)
ct_exec() { local id=$1; shift; run pct exec "$id" -- "$@"; }

# backup_ct CTID LABEL : snapshot if storage supports it, else vzdump (stop mode). Returns 0 on success.
backup_ct() {
  local id=$1 label=$2 snap
  # shellcheck disable=SC2034  # BACKUP_REF is read by the calling scripts
  snap="mtmon-${label}-$(date +%Y%m%d-%H%M%S)"
  if [ "$DRY_RUN" = 1 ]; then info "[dry] would snapshot CT $id as $snap (fallback: vzdump)"; BACKUP_REF="snapshot:$snap"; return 0; fi
  if pct snapshot "$id" "$snap" --description "mtmon $label" >/dev/null 2>&1; then
    BACKUP_REF="snapshot:$snap"; ok "snapshot created: $snap  (rollback: pct rollback $id $snap)"; return 0
  fi
  warn "snapshot not supported on this storage - falling back to vzdump"
  local dumpstore=${DUMP_STORAGE:-local}
  if vzdump "$id" --mode stop --compress zstd --storage "$dumpstore" >/tmp/mtmon-vzdump.log 2>&1; then
    BACKUP_REF="vzdump:$dumpstore"; ok "vzdump backup written to storage '$dumpstore' (see /tmp/mtmon-vzdump.log)"; return 0
  fi
  return 1
}

sha256_of() { sha256sum "$1" | awk '{print $1}'; }

# wait_for CMD... : retry a command for up to $WAIT_SECS (default 40) seconds
wait_for() {
  local n=0 max=${WAIT_SECS:-40}
  while ! "$@" >/dev/null 2>&1; do
    n=$((n + 1)); [ "$n" -ge "$max" ] && return 1; sleep 1
  done
}
