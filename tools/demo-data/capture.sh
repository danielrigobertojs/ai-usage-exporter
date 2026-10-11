#!/usr/bin/env bash
# Creates compact, dark demo dashboard visuals. All labels and values are synthetic.
set -euo pipefail
out=${1:-docs/images}
mkdir -p "$out"
render() {
  local file=$1 title=$2 subtitle=$3 value=$4
  magick -size 1440x810 xc:'#111827' \
    -fill '#1f2937' -draw 'roundrectangle 42,98 1398,760 16,16' \
    -fill '#f9fafb' -font /System/Library/Fonts/SFNS.ttf -pointsize 38 -annotate +50+52 'AI Usage — Demo' \
    -fill '#9ca3af' -font /System/Library/Fonts/SFNS.ttf -pointsize 19 -annotate +1120+52 'Last 1 hour' \
    -fill '#f3f4f6' -font /System/Library/Fonts/SFNS.ttf -pointsize 30 -annotate +74+145 "$title" \
    -fill '#9ca3af' -font /System/Library/Fonts/SFNS.ttf -pointsize 20 -annotate +74+180 "$subtitle" \
    -fill '#0f172a' -draw 'roundrectangle 74,225 1366,698 12,12' \
    -fill '#60a5fa' -draw 'rectangle 132,550 270,620 rectangle 360,460 498,620 rectangle 588,375 726,620 rectangle 816,300 954,620 rectangle 1044,410 1182,620' \
    -fill '#f9fafb' -font /System/Library/Fonts/SFNS.ttf -pointsize 54 -annotate +104+290 "$value" \
    -fill '#9ca3af' -font /System/Library/Fonts/SFNS.ttf -pointsize 20 -annotate +104+325 'Synthetic demonstration data' \
    -strip -quality 85 "$out/$file"
}
render dashboard-live.png 'Usage moving now' 'Tokens and cost from the last hour' '42.8k tokens'
render dashboard-by-instance.png 'Fleet by instance' 'demo-laptop and demo-workstation' '2 instances'
render dashboard-cost-by-model.png 'Cost by model' 'See which model receives spend' '$1.84'
render dashboard-token-classes.png 'Token classes' 'Input, output, cache read, cache write, reasoning' '38% cached'
render dashboard-health.png 'Exporter health' 'Sources, errors, availability and scan freshness' '3 providers'
