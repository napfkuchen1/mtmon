#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Code health report (gocyclo + dupl) for the job summary. Needs gocyclo and dupl on PATH.
# Fails only for functions above complexity 25 that are not listed in .github/gocyclo-baseline.txt
# ("<package> <function>", one per line). Existing offenders stay listed but do not fail; fixing one
# means deleting its line from the baseline, never adding a line to hide a new one.
set -euo pipefail
cd "$(dirname "$0")/.."
BASE=.github/gocyclo-baseline.txt
OUT=${GITHUB_STEP_SUMMARY:-/dev/stdout}
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT

grep -Ev '^\s*(#|$)' "$BASE" | sort > "$T/base"
{ gocyclo -over 25 -ignore '_test\.go$' . || true; } | sort -rn > "$T/cur" # gocyclo exits 1 when it finds any
# fields: complexity package function file:line:col
awk 'NR==FNR{b[$0]=1; next} !(($2" "$3) in b)' "$T/base" "$T/cur" > "$T/new"
awk '{print $2" "$3}' "$T/cur" | sort > "$T/curkeys"
comm -23 "$T/base" "$T/curkeys" > "$T/fixed"

{
  echo "### Functions with cyclomatic complexity > 25"
  echo "New (not in baseline, fails the job): $(wc -l < "$T/new" | tr -d ' ')  |  known (baseline): $(( $(wc -l < "$T/cur") - $(wc -l < "$T/new") ))"
  if [ -s "$T/new" ]; then echo; echo '**New offenders**'; echo '```'; cat "$T/new"; echo '```'; fi
  echo; echo '<details><summary>All functions above 25</summary>'; echo; echo '```'; cat "$T/cur"; echo '```'; echo '</details>'
  if [ -s "$T/fixed" ]; then
    echo; echo "Baseline entries that are now at or below 25 (delete these lines from $BASE):"; echo '```'; cat "$T/fixed"; echo '```'
  fi
  echo "### Duplicated blocks (>= 100 tokens, informational)"
  echo '```'
  # shellcheck disable=SC2046
  dupl -threshold 100 -plumbing $(git ls-files '*.go' | grep -v _test.go) | head -40 || true
  echo '```'
} >> "$OUT"

if [ -s "$T/new" ]; then
  while read -r c pkg fn loc; do
    echo "::error file=${loc%%:*}::new function above complexity 25: $pkg $fn ($c). Split it, or (deliberately) add \"$pkg $fn\" to $BASE"
  done < "$T/new"
  exit 1
fi
echo "no new functions above complexity 25"
