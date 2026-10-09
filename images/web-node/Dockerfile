# syntax=docker/dockerfile:1.10@sha256:865e5dd094beca432e8c0a1d5e1c465db5f998dca4e439981029b3b81fb39ed5
# The frontend pin above (also in images/derived/Dockerfile.tmpl and the
# render_test goldens) isn't covered by Dependabot, which doesn't update
# "# syntax" lines: bump it by hand monthly, alongside the base refreshes,
# with `docker buildx imagetools inspect docker/dockerfile:<version>`.
# Fugaro web-node base image (design §7.1): Node.js LTS with corepack, git,
# zstd, tini, gh, Claude Code (native installer) and fugaro. It runs as the
# non-root user fugaro (uid 1000) in /work/repo. Build it from the repository
# root with images/build-base.sh web-node.

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

# Ubuntu 24.04 matches the images Playwright publishes, so
# `playwright install --with-deps` finds the packages it expects.
FROM ubuntu:24.04
# Node.js LTS major; a workflow's image.node replaces it in the derived image.
ARG NODE_VERSION=24
# Claude Code is pinned; bump it deliberately, never float it.
ARG CLAUDE_CODE_VERSION=2.1.283
# gh (the GitHub CLI) is pinned; bump it deliberately, never float it. The
# release tarball is checked against its published sha256sum below, so the
# pin holds even though cli.github.com's apt repo only serves the latest
# release (it has no stable per-version index to apt-pin against).
ARG GH_VERSION=2.101.0
ENV HOME=/home/fugaro \
    PATH=/home/fugaro/.local/bin:/opt/node/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    LANG=C.UTF-8 \
    DISABLE_AUTOUPDATER=1 \
    COREPACK_ENABLE_DOWNLOAD_PROMPT=0 \
    COREPACK_HOME=/home/fugaro/.cache/corepack \
    PLAYWRIGHT_BROWSERS_PATH=/home/fugaro/.cache/ms-playwright
# sudo is installed with no rules; derived builds grant it to image.setup
# steps only and remove the grant before the image is finished. libcap2-bin
# provides getcap, which fugaro image selftest uses to check that no file
# carries capabilities.
#
# /etc/claude-code is where Claude Code reads its managed settings. It is
# created empty and owned by fugaro, because the runner is not root and
# writes managed-settings.json there at run time; fugaro image selftest
# refuses it if anything is baked into it.
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      ca-certificates curl dirmngr git gnupg libcap2-bin procps sudo tini xz-utils zstd \
 && rm -rf /var/lib/apt/lists/* \
 && userdel --remove ubuntu \
 && useradd --uid 1000 --user-group --create-home --shell /bin/bash fugaro \
 && install -d -o fugaro -g fugaro -m 0755 /work /work/repo /work/state /etc/claude-code \
 && install -d -o fugaro -g fugaro -m 0700 /work/creds \
 && install -d -o fugaro -g fugaro -m 0755 /home/fugaro/.cache "$COREPACK_HOME" "$PLAYWRIGHT_BROWSERS_PATH"
# gh (the GitHub CLI), installed from a pinned release tarball with its
# published sha256sum verified per architecture, checked ourselves against
# https://github.com/cli/cli/releases/download/v2.101.0/gh_2.101.0_checksums.txt
# on 2026-09-27 (design §7.1: the runner configures git and gh with the
# token, so the agent can push, edit the PR, and read issues and PRs). The
# apt route was rejected: cli.github.com's apt repo publishes only the
# Packages index for the current release, with no per-version pool to pin
# an older `apt-get install gh=X` against.
RUN set -eu; \
    case "$(dpkg --print-architecture)" in \
      amd64) arch=amd64; sum=9bca2d1c16825f109907a23307628a2f0698fbf99662b73a5cf0b020293072b8 ;; \
      arm64) arch=arm64; sum=b57e8063f18862647c9d22727c32e9da1b963f8bf9db648fe123a6975695640f ;; \
      *) echo "web-node: unsupported architecture $(dpkg --print-architecture) for gh" >&2; exit 1 ;; \
    esac; \
    tmp=$(mktemp -d); \
    file="gh_${GH_VERSION}_linux_${arch}.tar.gz"; \
    curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/${file}" -o "$tmp/$file"; \
    echo "$sum  $tmp/$file" | sha256sum -c -; \
    tar -xzf "$tmp/$file" -C "$tmp"; \
    install -m 0755 "$tmp/gh_${GH_VERSION}_linux_${arch}/bin/gh" /usr/local/bin/gh; \
    rm -rf "$tmp"; \
    HOME=/root gh --version
COPY --chmod=0755 images/base/install-node.sh /usr/local/lib/fugaro/install-node
COPY --chmod=0755 images/common/finalize-checkout.sh /usr/local/lib/fugaro/finalize-checkout
# The Node.js release keyring is baked once here, so derived builds that set
# image.node verify against it without a keyserver.
RUN /usr/local/lib/fugaro/install-node keyring \
 && /usr/local/lib/fugaro/install-node "$NODE_VERSION"
USER fugaro
# Claude Code (native build, pinned), downloaded directly (no curl | bash)
# and verified against a per-arch sha256 taken from that release's own
# manifest.json (the same one https://claude.ai/install.sh reads) and
# checked ourselves against the freshly downloaded binary on 2026-09-27:
#   https://downloads.claude.ai/claude-code-releases/2.1.283/manifest.json
#     linux-x64:   1859583ce32920595c61ef868bee52e1b1594f7486db209935e01f1e5e804ae2
#     linux-arm64: 346d294f0103d6fc0de11ac953579b5c62dfa90698a4cfc486b6f927c615e697
# The verified binary itself is installed at ~/.local/bin/claude. `claude
# install` is deliberately not used: it downloads the current release rather
# than installing the binary it was run from, so the pinned version silently
# floated (2.1.284 replaced the pinned 2.1.283). Any state or identity files
# a run of the binary leaves behind (~/.claude, ~/.claude.json, which holds a
# fixed machineID/userID, ~/.npm, ~/.cache/claude) are removed straight
# after so the image carries no shared per-build identity, and `claude
# --version` must report exactly the pinned version or the build fails.
RUN set -eu; \
    case "$(dpkg --print-architecture)" in \
      amd64) carch=x64; csum=1859583ce32920595c61ef868bee52e1b1594f7486db209935e01f1e5e804ae2 ;; \
      arm64) carch=arm64; csum=346d294f0103d6fc0de11ac953579b5c62dfa90698a4cfc486b6f927c615e697 ;; \
      *) echo "web-node: unsupported architecture $(dpkg --print-architecture) for Claude Code" >&2; exit 1 ;; \
    esac; \
    tmp=$(mktemp -d); \
    curl -fsSL "https://downloads.claude.ai/claude-code-releases/${CLAUDE_CODE_VERSION}/linux-${carch}/claude" -o "$tmp/claude"; \
    echo "$csum  $tmp/claude" | sha256sum -c -; \
    mkdir -p "$HOME/.local/bin"; \
    install -m 0755 "$tmp/claude" "$HOME/.local/bin/claude"; \
    rm -rf "$tmp"; \
    v=$(claude --version); echo "$v"; \
    rm -rf "$HOME/.claude" "$HOME/.claude.json" "$HOME/.npm" "$HOME/.cache/claude"; \
    case "$v" in "${CLAUDE_CODE_VERSION} "*) ;; *) echo "web-node: claude --version reports '$v', not the pinned ${CLAUDE_CODE_VERSION}" >&2; exit 1 ;; esac
# Copied last so a fugaro change doesn't re-download Node or Claude Code.
COPY --from=fugaro /out/fugaro /usr/local/bin/fugaro
WORKDIR /work/repo
LABEL org.opencontainers.image.source=https://github.com/dimipaun/fugaro \
      org.opencontainers.image.description="Fugaro web-node base image"
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["fugaro", "exec"]
