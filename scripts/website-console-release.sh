#!/usr/bin/env bash
# Moves the website's console downloads to a published console release:
#   scripts/website-console-release.sh 0.118.0 [--verify]
# It copies the release's own console-sha256sums.txt into the site, then sets
# downloads.json's release.version and each download's sha256 from that file.
# No tests: the site's gates run in the website-preview workflow, and a wrong
# hash shows up there. --verify also runs cosign verify-blob on the sums file.
set -euo pipefail

version="${1:?usage: website-console-release.sh X.Y.Z [--verify]}"
repo="${FUGARO_REPO:-dimipaun/fugaro}"
root="$(cd "$(dirname "$0")/.." && pwd)"
tag="console-v${version}"
sums="$root/website/data/console-sha256sums.txt"
json="$root/website/content/downloads.json"

gh release download "$tag" --repo "$repo" --pattern console-sha256sums.txt \
  --output "$sums" --clobber

if [ "${2:-}" = "--verify" ]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  gh release download "$tag" --repo "$repo" --pattern console-sha256sums.txt \
    --pattern 'console-sha256sums.txt.sig' --pattern 'console-sha256sums.txt.pem' \
    --pattern 'console-sha256sums.txt.sigstore.json' --dir "$tmp" --clobber
  bundle=("$tmp"/console-sha256sums.txt.sigstore.json)
  [ -e "${bundle[0]}" ] || { echo "no signature bundle on $tag" >&2; exit 1; }
  cosign verify-blob "$tmp/console-sha256sums.txt" --bundle "${bundle[0]}" \
    --certificate-identity "https://github.com/${repo}/.github/workflows/console-release.yml@refs/tags/${tag}" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com
fi

python3 -I - "$json" "$sums" "$version" <<'PY'
import json, re, sys
path, sums_path, version = sys.argv[1:4]
text = open(path).read()
doc = json.loads(text)
sums = {}
for line in open(sums_path):
    digest, _, name = line.rstrip("\n").partition("  ")
    if name:
        sums[name] = digest
old = doc["release"]["version"]
text = text.replace('"version": "%s"' % old, '"version": "%s"' % version, 1)
for d in doc["downloads"]:
    name = d["file"].replace("{version}", version)
    if name not in sums:
        sys.exit("no checksum for %s in the release's sums file" % name)
    text = text.replace(d["sha256"], sums[name], 1)
open(path, "w").write(text)
print("website: %s -> %s, %d downloads updated" % (old, version, len(doc["downloads"])))
PY
