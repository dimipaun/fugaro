#!/bin/sh
# install-node VERSION installs Node.js VERSION (N for the latest N.x, or
# N.N.N) from nodejs.org into /opt/node, replacing any Node already there. It
# checks the tarball against the release's SHASUMS256.txt, then makes sure
# corepack is present and enabled, so yarn and pnpm resolve through it. It
# runs as root: in the base image build, and in derived images for image.node.
set -eu
want=${1:?usage: install-node VERSION}
case "$(uname -m)" in
  x86_64) arch=x64 ;;
  aarch64) arch=arm64 ;;
  *) echo "install-node: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
case "$want" in
  *.*.*) dist="v$want" ;;
  *) dist="latest-v$want.x" ;;
esac
url="https://nodejs.org/dist/$dist"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$url/SHASUMS256.txt" -o "$tmp/SHASUMS256.txt"
file=$(grep -o "node-v[0-9.]*-linux-$arch\.tar\.xz" "$tmp/SHASUMS256.txt" | head -n 1)
if [ -z "$file" ]; then
  echo "install-node: nodejs.org has no linux-$arch build for Node $want" >&2
  exit 1
fi
if [ -x /opt/node/bin/node ] && [ "node-$(/opt/node/bin/node -v)-linux-$arch.tar.xz" = "$file" ]; then
  echo "install-node: Node $(/opt/node/bin/node -v) is already installed"
  exit 0
fi
curl -fsSL "$url/$file" -o "$tmp/$file"
(cd "$tmp" && grep "  $file\$" SHASUMS256.txt | sha256sum -c -)
rm -rf /opt/node
mkdir -p /opt/node
tar -xJf "$tmp/$file" -C /opt/node --strip-components=1 --no-same-owner
if [ ! -x /opt/node/bin/corepack ]; then
  # Node 25 and later no longer bundle corepack. HOME=/root keeps npm's cache
  # out of the fugaro user's home, where root-owned files would break it.
  HOME=/root /opt/node/bin/npm install --global --no-fund --no-audit corepack@0.36.0
fi
/opt/node/bin/corepack enable
echo "install-node: installed Node $(/opt/node/bin/node -v)"
