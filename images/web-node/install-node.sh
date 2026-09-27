#!/bin/sh
# install-node VERSION installs Node.js VERSION (N for the latest N.x, or
# N.N.N) from nodejs.org into /opt/node, replacing any Node already there.
# SHASUMS256.txt is verified with gpgv against its detached signature
# (SHASUMS256.txt.sig), checked against the Node.js Release Team's
# fingerprints pinned below, the way the official docker-library/node
# images do; the tarball is then checked against SHASUMS256.txt. It also
# makes sure corepack is present and enabled, so yarn and pnpm resolve
# through it. It runs as root: in the base image build, and in derived
# images for image.node. It needs curl, gpg, gpgv and dirmngr on PATH
# (installed by the web-node Dockerfile).
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

# Node.js Release Team primary keys
# (https://github.com/nodejs/node#release-keys, checked 2026-09-27). Bump
# this list only from that page, never from a build log or an untrusted
# mirror.
node_release_keys="
5BE8A3F6C8A5C01D106C0AD820B1A390B168D356
DD792F5973C6DE52C432CBDAC77ABFA00DDBF2B7
CC68F5A3106FF448322E48ED27F5E38D5B0A215F
890C08DB8579162FEE0DF9DB8BEAB4DFCF555EF4
C82FA3AE1CBEDC6BE46B9360C43CEC45C17AB93C
108F52B48DB57BB0CC439B2997B01419BD92F80A
655F3B5C1FB3FA8D1A0CA6BDE4A7D232B936D2FD
A363A499291CBBC940DD62E41F10027AF002F8B0
"

tmp=$(mktemp -d)
trap 'gpgconf --kill all 2>/dev/null || true; rm -rf "$tmp"' EXIT

curl -fsSL "$url/SHASUMS256.txt" -o "$tmp/SHASUMS256.txt"
curl -fsSL "$url/SHASUMS256.txt.sig" -o "$tmp/SHASUMS256.txt.sig"
export GNUPGHOME="$tmp/gnupg"
mkdir -m 0700 "$GNUPGHOME"
for key in $node_release_keys; do
  gpg --batch --keyserver hkps://keys.openpgp.org --recv-keys "$key" \
    || gpg --batch --keyserver keyserver.ubuntu.com --recv-keys "$key"
done
# gpgv needs a legacy-format keyring, not the keybox gpg itself keeps.
# shellcheck disable=SC2086
gpg --batch --export $node_release_keys >"$tmp/nodejs.gpg"
gpgv --keyring "$tmp/nodejs.gpg" "$tmp/SHASUMS256.txt.sig" "$tmp/SHASUMS256.txt"

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
