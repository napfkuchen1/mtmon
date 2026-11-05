#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only
# Turns the Playwright recording (bin/recording/demo.webm, see scripts/recording.js) into docs/img/demo.gif, at most 4 MB.
# It tries smaller sizes and frame rates until the GIF fits. If none fits it writes docs/img/demo.mp4 plus
# docs/img/demo-poster.png instead. The last line printed is "gif" or "mp4": scripts/readme-media.js takes it as argument.
set -euo pipefail
cd "$(dirname "$0")/.."
SRC=bin/recording/demo.webm
OUT=docs/img
MAX=$((4 * 1024 * 1024))
[ -s "$SRC" ] || { echo "missing $SRC (run scripts/recording.js first)" >&2; exit 1; }
# skip the first half second (blank page while the first route loads), cap at 20 s
trim=(-ss 0.5 -t 20)
for spec in "900 12 96" "800 10 80" "700 10 64" "640 8 48"; do
  read -r w fps colors <<<"$spec"
  ffmpeg -v error -y "${trim[@]}" -i "$SRC" \
    -vf "fps=$fps,scale=$w:-1:flags=lanczos,split[a][b];[a]palettegen=max_colors=$colors:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=5" \
    -loop 0 "$OUT/demo.gif"
  size=$(stat -c %s "$OUT/demo.gif")
  echo "gif ${w}px ${fps}fps ${colors} colours: $size bytes" >&2
  if [ "$size" -le "$MAX" ]; then
    rm -f "$OUT/demo.mp4" "$OUT/demo-poster.png"
    echo gif
    exit 0
  fi
done
echo "no GIF under 4 MB, writing MP4 with poster" >&2
rm -f "$OUT/demo.gif"
ffmpeg -v error -y "${trim[@]}" -i "$SRC" -vf "scale=1280:-2,fps=25" -c:v libx264 -crf 28 -preset slow -pix_fmt yuv420p -movflags +faststart -an "$OUT/demo.mp4"
ffmpeg -v error -y -ss 2 -i "$OUT/demo.mp4" -frames:v 1 "$OUT/demo-poster.png"
echo mp4
