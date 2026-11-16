#!/usr/bin/env bash
# Renders docs/status.md to one self-contained HTML file (default
# ~/Downloads/status.html). Pandoc writes a <col width=N%> per column from the
# dash widths of each table's separator row, so without status.css's
# `col{width:auto !important}` the layout changes whenever the markdown source
# does; keep the stylesheet next to this script.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
out=${1:-$HOME/Downloads/status.html}
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
{ echo '<style>'; cat "$here/status.css"; echo '</style>'; } > "$tmp"
pandoc "$here/../docs/status.md" -s -H "$tmp" -o "$out"
echo "wrote $out"
