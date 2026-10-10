# syntax=docker/dockerfile:1.10@sha256:865e5dd094beca432e8c0a1d5e1c465db5f998dca4e439981029b3b81fb39ed5
# The Fugaro base image (docs/design/base-image.md): one language-agnostic
# image for every workflow. Debian 13 slim, a maximalist tool kit, gcloud and
# the Docker CLI (client only), mise (projects install their runtimes with it),
# the agent harnesses (only Claude Code is driven by the runner) and fugaro.
# It runs as the non-root user fugaro (uid 1000) in /work/repo, under tini,
# with sudo installed but granting nothing. Build it from the repository root
# with images/build-base.sh base [TAG], for linux/amd64 or linux/arm64.
#
# Every pin is one "ARG NAME=value" line, which images/base/bump.sh rewrites
# monthly (design section 7): a version and, per architecture, the sha256
# fetch.sh checks. Debian's own packages float: the first RUN upgrades them.
# Layers go from the slowest-changing to the fastest: the OS and kit, the
# single-binary tools, mise, the harnesses, then fugaro.

# debian:trixie-slim's multi-platform index, read on 2026-10-08; bump.sh
# moves it.
ARG DEBIAN_DIGEST=sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f

FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS fugaro
ARG TARGETOS
ARG TARGETARCH
ARG FUGARO_VERSION=dev
ARG FUGARO_COMMIT=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY images ./images
COPY deploy/terraform ./deploy/terraform
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/dimipaun/fugaro/internal/cli.Version=${FUGARO_VERSION} -X github.com/dimipaun/fugaro/internal/cli.Commit=${FUGARO_COMMIT}" \
      -o /out/fugaro ./cmd/fugaro

FROM debian:trixie-slim@${DEBIAN_DIGEST}
ARG DEBIAN_DIGEST
ARG MISE_VERSION=2026.10.4
ARG MISE_SHA256_AMD64=2fc793020b442d08163603400236b2c693f8cede810c7e432fc77220e6c9e8a1
ARG MISE_SHA256_ARM64=8760841cdbf964ecf9902a50c94716c77185a99af7f8eb55c9c51ec73ecd8880
ARG CLAUDE_CODE_VERSION=2.1.283
ARG CLAUDE_CODE_SHA256_AMD64=1859583ce32920595c61ef868bee52e1b1594f7486db209935e01f1e5e804ae2
ARG CLAUDE_CODE_SHA256_ARM64=346d294f0103d6fc0de11ac953579b5c62dfa90698a4cfc486b6f927c615e697
ARG GH_VERSION=2.101.0
ARG GH_SHA256_AMD64=9bca2d1c16825f109907a23307628a2f0698fbf99662b73a5cf0b020293072b8
ARG GH_SHA256_ARM64=b57e8063f18862647c9d22727c32e9da1b963f8bf9db648fe123a6975695640f
ARG YQ_VERSION=4.54.1
ARG YQ_SHA256_AMD64=8e34fc298390875de416e6a4afcb8cabeceb25d9aa8506c1a2f9353cf702ea5f
ARG YQ_SHA256_ARM64=189088da0c6429ec5178dfaab1a114805f6cab0b61b165ab236efedf1d57a71b
ARG GCLOUD_VERSION=588.0.0
ARG GCLOUD_SHA256_AMD64=e38ceac43022bb5a4d94d5a4a9c9c51f90f28c910b59a018ae07011e220c4412
ARG GCLOUD_SHA256_ARM64=15d0365242d72caebda77647642ae73f68bde10b1f71338c8a704aea200d8ab1
ARG DOCKER_CLI_VERSION=29.8.2
ARG DOCKER_CLI_SHA256_AMD64=995d1ef289677f74fd58d8d2c35727b6a4ee389c69db8638a3e42d0487aa5b0f
ARG DOCKER_CLI_SHA256_ARM64=76a624e4a8e5da654d1150e808175125efb5a6f1b6aa1cbd9caee18f51047a50
ARG HARNESS_NODE_VERSION=24.21.0
ARG CODEX_VERSION=0.161.0
ARG CODEX_SHA256_AMD64=b1efb95097660d7f2e5a3887618a23f2ea1b0d548078bf92b0f7a5d229a0cef2
ARG CODEX_SHA256_ARM64=5c90bf4be3c97fc62fcafb28ec2335ee318e6ecb332e1e8ee3d2c63814a5a26a
ARG OPENCODE_VERSION=1.18.35
ARG OPENCODE_SHA256_AMD64=c8f888b451f5494a18f858fffb0e0b68f4e4baa9c241761c5f206884f0fa640d
ARG OPENCODE_SHA256_ARM64=f7f2ba59ee8aa94d388f9696575a32d20e71c2ee48def9f80fc693a60fec6c72
ARG GOOSE_VERSION=1.53.0
ARG GOOSE_SHA256_AMD64=2d010c66dfd4348bb437b3a94001882468a5db1aa556858920481522f57752d7
ARG GOOSE_SHA256_ARM64=e156799760a174bfbc5d14443aacef064ce7592f9099dc9ce04c3db74e72224b
ARG CRUSH_VERSION=0.97.1
ARG CRUSH_SHA256_AMD64=1b7cbe0600a3797538a74dc00dd8c4bac54ac4b8f4455ba5ff2312b29c7bd598
ARG CRUSH_SHA256_ARM64=16c9477a178d69e02a51bf4babe6174e62051314b2cd7f36b1ec5fea47e25a88
ENV HOME=/home/fugaro \
    PATH=/home/fugaro/.local/share/mise/shims:/home/fugaro/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    LANG=C.UTF-8 \
    TZ=UTC \
    DISABLE_AUTOUPDATER=1 \
    COREPACK_ENABLE_DOWNLOAD_PROMPT=0 \
    COREPACK_HOME=/home/fugaro/.cache/corepack \
    PLAYWRIGHT_BROWSERS_PATH=/home/fugaro/.cache/ms-playwright

# 1. The OS and the kit (design section 3), Debian's current packages. sudo
# is installed with no rules: a derived build grants it to image.setup only
# and strips its setuid bit (D1). libcap2-bin gives the selftest getcap.
# ssh-keysign's setuid bit is stripped (host-based ssh auth is never used).
# policy-rc.d stops a package's service from starting during a build, and no
# PostgreSQL package may create a root-owned cluster: fugaro-services makes
# its own as fugaro. /etc/claude-code is empty and fugaro's: the runner writes
# managed settings there, and the selftest refuses anything baked into it.
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get upgrade -y \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      autoconf automake bash bat bind9-dnsutils build-essential bzip2 ca-certificates clang cmake curl \
      diffutils dirmngr eza fd-find file fzf git git-lfs gnupg gpgv httpie jq less libbz2-dev libcap2-bin \
      libffi-dev libgdbm-dev liblzma-dev libncurses-dev libreadline-dev libsqlite3-dev libssl-dev libtool \
      libxml2-dev libxslt1-dev libyaml-dev locales make nano netcat-openbsd openssh-client patch pkg-config \
      procps ripgrep rsync shellcheck sqlite3 sudo tini tk-dev tree tzdata unzip uuid-dev vim-tiny wget \
      xz-utils zip zlib1g-dev zstd \
 && rm -rf /var/lib/apt/lists/* \
 && ln -s /usr/bin/fdfind /usr/local/bin/fd \
 && ln -s /usr/bin/batcat /usr/local/bin/bat \
 && sed -i 's/^# *en_US.UTF-8 UTF-8/en_US.UTF-8 UTF-8/' /etc/locale.gen && locale-gen \
 && chmod u-s /usr/lib/openssh/ssh-keysign \
 && printf '#!/bin/sh\n# Containers start no services while a package installs.\nexit 101\n' > /usr/sbin/policy-rc.d \
 && chmod 0755 /usr/sbin/policy-rc.d \
 && mkdir -p /etc/postgresql-common \
 && printf 'create_main_cluster = false\n' > /etc/postgresql-common/createcluster.conf \
 && useradd --uid 1000 --user-group --create-home --shell /bin/bash fugaro \
 && install -d -o fugaro -g fugaro -m 0755 /work /work/repo /work/state /etc/claude-code \
 && install -d -o fugaro -g fugaro -m 0700 /work/creds \
 && install -d -o fugaro -g fugaro -m 0755 /home/fugaro/.cache /home/fugaro/.cache/corepack \
      /home/fugaro/.cache/ms-playwright /home/fugaro/.config /home/fugaro/.local /home/fugaro/.local/bin \
      /home/fugaro/.local/share

# 1b. Single-binary tools, each checksummed per architecture by fetch.sh: gh
# (the runner configures it with the run's token at run time), Mike Farah's
# yq, the Docker CLI (client only: no daemon, no plugins; inert on Cloud Run)
# and gcloud (prompts, usage reporting and update checks off for every user).
# The version checks run with HOME=/root and their state is removed.
RUN --mount=type=bind,source=images/base/fetch.sh,target=/tmp/fetch.sh \
    set -eu; arch=$(dpkg --print-architecture); \
    case "$arch" in amd64|arm64) ;; *) echo "base: unsupported architecture $arch" >&2; exit 1 ;; esac; \
    pick() { if [ "$arch" = amd64 ]; then echo "$1"; else echo "$2"; fi; }; \
    tmp=$(mktemp -d); \
    sh /tmp/fetch.sh "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${arch}.tar.gz" \
      "$(pick "$GH_SHA256_AMD64" "$GH_SHA256_ARM64")" "$tmp/gh.tgz"; \
    tar -xzf "$tmp/gh.tgz" -C "$tmp"; \
    install -m 0755 "$tmp/gh_${GH_VERSION}_linux_${arch}/bin/gh" /usr/local/bin/gh; \
    sh /tmp/fetch.sh "https://github.com/mikefarah/yq/releases/download/v${YQ_VERSION}/yq_linux_${arch}" \
      "$(pick "$YQ_SHA256_AMD64" "$YQ_SHA256_ARM64")" "$tmp/yq"; \
    install -m 0755 "$tmp/yq" /usr/local/bin/yq; \
    sh /tmp/fetch.sh "https://download.docker.com/linux/static/stable/$(pick x86_64 aarch64)/docker-${DOCKER_CLI_VERSION}.tgz" \
      "$(pick "$DOCKER_CLI_SHA256_AMD64" "$DOCKER_CLI_SHA256_ARM64")" "$tmp/docker.tgz"; \
    tar -xzf "$tmp/docker.tgz" -C "$tmp" docker/docker; \
    install -m 0755 "$tmp/docker/docker" /usr/local/bin/docker; \
    sh /tmp/fetch.sh "https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-cli-${GCLOUD_VERSION}-linux-$(pick x86_64 arm).tar.gz" \
      "$(pick "$GCLOUD_SHA256_AMD64" "$GCLOUD_SHA256_ARM64")" "$tmp/gcloud.tgz"; \
    tar -xzf "$tmp/gcloud.tgz" -C /opt; \
    printf '[core]\ndisable_usage_reporting = true\ndisable_prompts = true\n[component_manager]\ndisable_update_check = true\n' \
      > /opt/google-cloud-sdk/properties; \
    for b in gcloud gsutil bq; do ln -s "/opt/google-cloud-sdk/bin/$b" "/usr/local/bin/$b"; done; \
    rm -rf "$tmp"; \
    HOME=/root gh --version; yq --version; docker --version; HOME=/root gcloud --version; \
    rm -rf /root/.config /root/.cache

# 2. mise (design section 4), with its system config. Projects install their
# runtimes with it, as fugaro, into ~/.local/share/mise; its shims are first
# on PATH, and interactive bash activates it too (below).
COPY images/base/mise-system.toml /etc/mise/config.toml
RUN --mount=type=bind,source=images/base/fetch.sh,target=/tmp/fetch.sh \
    set -eu; arch=$(dpkg --print-architecture); \
    pick() { if [ "$arch" = amd64 ]; then echo "$1"; else echo "$2"; fi; }; \
    tmp=$(mktemp -d); \
    sh /tmp/fetch.sh "https://github.com/jdx/mise/releases/download/v${MISE_VERSION}/mise-v${MISE_VERSION}-linux-$(pick x64 arm64).tar.gz" \
      "$(pick "$MISE_SHA256_AMD64" "$MISE_SHA256_ARM64")" "$tmp/mise.tgz"; \
    tar -xzf "$tmp/mise.tgz" -C "$tmp"; \
    install -m 0755 "$tmp/mise/bin/mise" /usr/local/bin/mise; \
    rm -rf "$tmp"; \
    HOME=/root mise --version; \
    rm -rf /root/.local /root/.cache /root/.config

# 3. The agent harnesses (design section 6). Only Claude Code is driven by the
# runner; the others are present for later. The Node ones run on a private
# Node in /opt/fugaro/node, never on PATH, through wrappers that exec it
# without touching PATH, so neither side sees the other's Node. They come
# from the committed lockfile, without install scripts, into root-owned /opt.
COPY --chmod=0755 images/base/install-node.sh /usr/local/lib/fugaro/install-node
COPY images/base/harnesses/package.json images/base/harnesses/package-lock.json /opt/fugaro/harnesses/
RUN set -eu; \
    NODE_PREFIX=/opt/fugaro/node /usr/local/lib/fugaro/install-node keyring; \
    NODE_PREFIX=/opt/fugaro/node /usr/local/lib/fugaro/install-node "$HARNESS_NODE_VERSION"; \
    cd /opt/fugaro/harnesses; \
    HOME=/root PATH=/opt/fugaro/node/bin:$PATH npm ci --ignore-scripts --no-audit --no-fund --omit=dev; \
    rm -rf /root/.npm; \
    for h in gemini:@google/gemini-cli/bundle/gemini.js pi:@mariozechner/pi-coding-agent/dist/cli.js qwen:@qwen-code/qwen-code/cli-entry.js; do \
      name=${h%%:*}; entry=/opt/fugaro/harnesses/node_modules/${h#*:}; \
      [ -f "$entry" ] || { echo "base: $name's entry point $entry is missing" >&2; exit 1; }; \
      printf '#!/bin/sh\n# %s on the harnesses'"'"' own Node, which is not on PATH (design base-image.md section 6).\nexec /opt/fugaro/node/bin/node %s "$@"\n' \
        "$name" "$entry" > "/usr/local/bin/$name"; \
      chmod 0755 "/usr/local/bin/$name"; \
    done
# The binary harnesses: each release archive is checksummed per architecture
# and its one executable installed into /usr/local/bin.
RUN --mount=type=bind,source=images/base/fetch.sh,target=/tmp/fetch.sh \
    set -eu; arch=$(dpkg --print-architecture); \
    pick() { if [ "$arch" = amd64 ]; then echo "$1"; else echo "$2"; fi; }; \
    one() { name=$1; url=$2; sum=$3; d=$(mktemp -d); \
      sh /tmp/fetch.sh "$url" "$sum" "$d/archive"; mkdir "$d/x"; tar -xf "$d/archive" -C "$d/x"; \
      bin=$(find "$d/x" -type f \( -name "$name" -o -name "$name-*" \) -perm -u+x | head -n 1); \
      [ -n "$bin" ] || { echo "base: no $name executable in $url" >&2; exit 1; }; \
      install -m 0755 "$bin" "/usr/local/bin/$name"; rm -rf "$d"; }; \
    one codex "https://github.com/openai/codex/releases/download/rust-v${CODEX_VERSION}/codex-$(pick x86_64 aarch64)-unknown-linux-musl.tar.gz" \
      "$(pick "$CODEX_SHA256_AMD64" "$CODEX_SHA256_ARM64")"; \
    one opencode "https://github.com/sst/opencode/releases/download/v${OPENCODE_VERSION}/opencode-linux-$(pick x64 arm64).tar.gz" \
      "$(pick "$OPENCODE_SHA256_AMD64" "$OPENCODE_SHA256_ARM64")"; \
    one goose "https://github.com/block/goose/releases/download/v${GOOSE_VERSION}/goose-$(pick x86_64 aarch64)-unknown-linux-gnu.tar.bz2" \
      "$(pick "$GOOSE_SHA256_AMD64" "$GOOSE_SHA256_ARM64")"; \
    one crush "https://github.com/charmbracelet/crush/releases/download/v${CRUSH_VERSION}/crush_${CRUSH_VERSION}_Linux_$(pick x86_64 arm64).tar.gz" \
      "$(pick "$CRUSH_SHA256_AMD64" "$CRUSH_SHA256_ARM64")"
# 4. Fugaro's plumbing, and what the base is (design section 3).
COPY --chmod=0755 images/common/finalize-checkout.sh /usr/local/lib/fugaro/finalize-checkout
COPY --chmod=0755 images/common/fugaro-services /usr/local/bin/fugaro-services
RUN mkdir -p /etc/fugaro \
 && printf '{"debian":"trixie-slim@%s","mise":"%s","claude_code":"%s","gh":"%s","yq":"%s","gcloud":"%s","docker_cli":"%s","harness_node":"%s","codex":"%s","opencode":"%s","goose":"%s","crush":"%s"}\n' \
      "$DEBIAN_DIGEST" "$MISE_VERSION" "$CLAUDE_CODE_VERSION" "$GH_VERSION" "$YQ_VERSION" "$GCLOUD_VERSION" "$DOCKER_CLI_VERSION" \
      "$HARNESS_NODE_VERSION" "$CODEX_VERSION" "$OPENCODE_VERSION" "$GOOSE_VERSION" "$CRUSH_VERSION" > /etc/fugaro/base.json \
 && chmod 0644 /etc/fugaro/base.json
USER fugaro
WORKDIR /work/repo
# Claude Code (native build, pinned), as in the old bases: the verified
# binary itself at ~/.local/bin/claude (never `claude install`, which fetches
# the current release), with every state or identity file a run of it leaves
# removed, and its reported version must be the pin. mise is activated for
# interactive bash; non-interactive shells use the shims on PATH.
RUN --mount=type=bind,source=images/base/fetch.sh,target=/tmp/fetch.sh \
    set -eu; arch=$(dpkg --print-architecture); \
    pick() { if [ "$arch" = amd64 ]; then echo "$1"; else echo "$2"; fi; }; \
    sh /tmp/fetch.sh "https://downloads.claude.ai/claude-code-releases/${CLAUDE_CODE_VERSION}/linux-$(pick x64 arm64)/claude" \
      "$(pick "$CLAUDE_CODE_SHA256_AMD64" "$CLAUDE_CODE_SHA256_ARM64")" "$HOME/.local/bin/claude"; \
    chmod 0755 "$HOME/.local/bin/claude"; \
    v=$(claude --version); echo "$v"; \
    rm -rf "$HOME/.claude" "$HOME/.claude.json" "$HOME/.npm" "$HOME/.cache/claude"; \
    case "$v" in "${CLAUDE_CODE_VERSION} "*) ;; *) echo "base: claude --version reports '$v', not the pinned ${CLAUDE_CODE_VERSION}" >&2; exit 1 ;; esac; \
    printf '\n# mise (design base-image.md section 4)\neval "$(mise activate bash)"\n' >> "$HOME/.bashrc"
LABEL org.opencontainers.image.source=https://github.com/dimipaun/fugaro \
      org.opencontainers.image.description="Fugaro base image"
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["fugaro", "exec"]
COPY --from=fugaro /out/fugaro /usr/local/bin/fugaro
