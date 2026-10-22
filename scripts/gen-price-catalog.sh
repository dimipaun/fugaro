#!/usr/bin/env bash
# gen-price-catalog.sh regenerates the embedded provider price catalog
# (internal/pricing/catalog/catalog.json) from OpenRouter's public API and
# prints what changed (design provider-price-catalog.md §5).
#
#   scripts/gen-price-catalog.sh -date 2026-10-20
#   scripts/gen-price-catalog.sh -from internal/pricing/catalog/testdata/openrouter -date 2026-10-20
#
# The second form reads the snapshots the checked-in file was generated
# from, so a reviewer can regenerate it offline and get identical bytes.
#
# No credential of any kind is used: the generator makes three public GETs.
# Nothing regenerates the file in CI — a price must never change because a
# workflow ran. It is run by hand, and by docs/release.md's checklist.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)

# The allowlist is here, not in the generator, so growing it is a reviewable
# diff (design §3 D8). Phase 1 is the DeepSeek family plus one Qwen and one
# Moonshot row. A line is "<model id>" or, for a Claude row, "<model id>
# <canonical id>" — the route's own key beside the direct spelling a pin
# carries (plan Task 1b.2 step 1: the generator refuses a Claude row with no
# canonical ID, or two rows claiming one).
#
# There is still no anthropic/<id> row here. The shape is legal now — such a
# key can be claimed by a route and pinned (phase 1b) — but a row for one
# would need this script to run against the network for a Claude snapshot the
# testdata does not carry, and its prices are the D8 measurement phase 1b
# defers. Until then the canonical-to-route map (internal/pricing/
# canonical.go) carries the pairs, with an explicit reason for the Claude rows
# nobody has measured a route for, and a project that routes one prices the
# route under model_prices.
# qwen/qwen3.8-flash has no ZDR-eligible endpoint, so the generator writes it
# an "all" tier and no "zdr" tier, and a require_zdr route (the default)
# refuses that pin — that row is the one to try with require_zdr: false.
allow=$(mktemp)
trap 'rm -f "$allow"' EXIT
cat > "$allow" <<'EOF'
deepseek/deepseek-v4.1-flash
deepseek/deepseek-v4-flash
deepseek/deepseek-v4-pro
qwen/qwen3.8-flash
moonshotai/kimi-k3
EOF

cd "$root"
go run ./internal/pricing/catalog/gencatalog \
  -allow "$allow" \
  -o internal/pricing/catalog/catalog.json \
  "$@"
