# syntax=docker/dockerfile:1.10
# Fugaro web-node base image (design §7.1): Node.js LTS with corepack, git,
# zstd, tini, gh, Claude Code (native installer) and fugaro. It runs as the
# non-root user fugaro (uid 1000) in /work/repo. Build it from the repository
# root with images/build-base.sh web-node.

FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS fugaro
ARG TARGETOS
ARG TARGETARCH
ARG FUGARO_VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY images ./images
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/dimipaun/fugaro/internal/cli.Version=${FUGARO_VERSION}" \
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
# steps only and remove the grant before the image is finished.
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      ca-certificates curl git procps sudo tini xz-utils zstd \
 && rm -rf /var/lib/apt/lists/* \
 && userdel --remove ubuntu \
 && useradd --uid 1000 --user-group --create-home --shell /bin/bash fugaro \
 && install -d -o fugaro -g fugaro -m 0755 /work /work/repo /work/state \
 && install -d -o fugaro -g fugaro -m 0700 /work/creds \
 && install -d -o fugaro -g fugaro -m 0755 /home/fugaro/.cache "$COREPACK_HOME" "$PLAYWRIGHT_BROWSERS_PATH"
# gh (the GitHub CLI), installed from a pinned release tarball with its
# published sha256sum verified, per release (design §7.1: the runner
# configures git and gh with the token so the agent can push, edit the PR,
# and read issues and PRs).
RUN set -eu; \
    case "$(dpkg --print-architecture)" in \
      amd64) arch=amd64; sum=9bca2d1c16825f109907a23307628a2f0698fbf99662b73a5cf0b020293072b8 ;; \
      arm64) arch=arm64; sum=9aec87f9a011b1521556b06cb003776e7e214144c8efd2144924a28d90c23057 ;; \
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
COPY --chmod=0755 images/web-node/install-node.sh /usr/local/lib/fugaro/install-node
COPY --chmod=0755 images/common/finalize-checkout.sh /usr/local/lib/fugaro/finalize-checkout
RUN /usr/local/lib/fugaro/install-node "$NODE_VERSION"
USER fugaro
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
RUN curl -fsSL https://claude.ai/install.sh | bash -s "$CLAUDE_CODE_VERSION" \
 && claude --version
SHELL ["/bin/sh", "-c"]
# Copied last so a fugaro change doesn't re-download Node or Claude Code.
COPY --from=fugaro /out/fugaro /usr/local/bin/fugaro
WORKDIR /work/repo
LABEL org.opencontainers.image.source=https://github.com/dimipaun/fugaro \
      org.opencontainers.image.description="Fugaro web-node base image"
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["fugaro", "exec"]
