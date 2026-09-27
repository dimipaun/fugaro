#!/bin/sh
# install-node keyring builds the Node.js release keyring at $keyring below,
# once, while the base image is built. install-node VERSION then installs
# Node.js VERSION (N for the latest N.x, or N.N.N) from nodejs.org into
# /opt/node, replacing any Node already there: in the base image build, and
# in derived images for image.node. VERSION never touches a keyserver, so a
# derived build doesn't depend on one being up.
#
# SHASUMS256.txt is verified with gpgv against its detached signature
# (SHASUMS256.txt.sig) and the baked keyring, the way the official
# docker-library/node images do; the tarball is then checked against
# SHASUMS256.txt. It also makes sure corepack is present and enabled, so yarn
# and pnpm resolve through it. It runs as root and needs curl and gpgv on
# PATH, plus gpg (and dirmngr, for the keyserver fallback) for keyring
# (installed by the web-node Dockerfile).
set -eu
usage="usage: install-node keyring | install-node VERSION"
want=${1:?$usage}
keyring=/usr/local/lib/fugaro/nodejs.gpg

# The Node.js release keys, pinned by full fingerprint: the Release Team's
# primary keys, and the "Other keys used to sign some previous releases",
# so an older image.node pin verifies too
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
C0D6248439F1D5604AAFFB4021D900FFDB233756
4ED778F539E3634C779C87C6D7062848A1AB005C
141F07595B7B3FFE74309A937405533BE57C7D57
9554F04D7259F04124DE6B476D5A82AC7E37093B
94AE36675C464D64BAFA68DD7434390BDBE9B9C5
1C050899334244A8AF75E53792EF661D867B9DFA
74F12602B6F1C4E913FAA37AD3A89613643B6201
B9AE9905FFD7803F25714661B63B535A4C206CA9
77984A986EBC2AA786BC0F66B01FBB92821C587A
93C7E9E91B49E432C2F75674B0A78B0A6C481CF6
56730D5401028683275BD23C23EFEFE93C4CFFFE
71DCFD284A79C3B38668286BC97EC7A07EDE3FC1
FD3A5288F042B6850C66B31F09FE44734EB7990E
61FC681DFB92A079F1685E77973F295594EC4689
114F43EE0176B71C7BC219DD50A3051F888C628D
8FCCA13FEF1D0C2E91008E09770F7A9A5AE15600
C4F0DFFF4E8C1A8236409D08E73BC641CC11F4C8
DD8F2338BAE7501E3DD5AC78C273792F7D83545D
A48C2BEE680E841632CD4E44F07496B3EB3C1762
B9E2F5981AA6E0CD28160D9FF13993A75599653C
7937DFD2AB06298B2293C3187D33FF9D0246406D
"

tmp=$(mktemp -d)
trap 'gpgconf --kill all 2>/dev/null || true; rm -rf "$tmp"' EXIT

if [ "$want" = keyring ]; then
  # Each key comes from the Node.js project's own release-keys repository,
  # falling back to the keyservers. The fingerprints above are the trust
  # anchor, not the source: only those exact keys are exported, and every
  # one must be present.
  export GNUPGHOME="$tmp/gnupg"
  mkdir -m 0700 "$GNUPGHOME"
  for key in $node_release_keys; do
    if curl -fsSL "https://raw.githubusercontent.com/nodejs/release-keys/main/keys/$key.asc" -o "$tmp/key.asc" \
        && gpg --batch --quiet --import "$tmp/key.asc"; then
      continue
    fi
    gpg --batch --keyserver hkps://keys.openpgp.org --recv-keys "$key" \
      || gpg --batch --keyserver keyserver.ubuntu.com --recv-keys "$key"
  done
  for key in $node_release_keys; do
    if ! gpg --batch --with-colons --fingerprint "$key" 2>/dev/null | grep -q "^fpr:::::::::$key:"; then
      echo "install-node: Node.js release key $key could not be fetched" >&2
      exit 1
    fi
  done
  # gpgv needs a legacy-format keyring, not the keybox gpg itself keeps.
  mkdir -p "$(dirname "$keyring")"
  # shellcheck disable=SC2086
  gpg --batch --export $node_release_keys >"$tmp/nodejs.gpg"
  install -m 0644 "$tmp/nodejs.gpg" "$keyring"
  echo "install-node: wrote the Node.js release keyring to $keyring"
  exit 0
fi

case "$(uname -m)" in
  x86_64) arch=x64 ;;
  aarch64) arch=arm64 ;;
  *) echo "install-node: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
case "$want" in
  *.*.*) dist="v$want" ;;
  [0-9]*) dist="latest-v$want.x" ;;
  *) echo "install-node: $usage" >&2; exit 1 ;;
esac
url="https://nodejs.org/dist/$dist"
if [ ! -s "$keyring" ]; then
  echo "install-node: no Node.js release keyring at $keyring; run install-node keyring first (the base image does)" >&2
  exit 1
fi

curl -fsSL "$url/SHASUMS256.txt" -o "$tmp/SHASUMS256.txt"
curl -fsSL "$url/SHASUMS256.txt.sig" -o "$tmp/SHASUMS256.txt.sig"
gpgv --keyring "$keyring" "$tmp/SHASUMS256.txt.sig" "$tmp/SHASUMS256.txt"

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
