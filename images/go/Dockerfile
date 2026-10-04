# syntax=docker/dockerfile:1.10@sha256:865e5dd094beca432e8c0a1d5e1c465db5f998dca4e439981029b3b81fb39ed5
# The frontend pin above (also in images/derived/Dockerfile.tmpl and the
# render_test goldens) isn't covered by Dependabot, which doesn't update
# "# syntax" lines: bump it by hand monthly, alongside the base refreshes,
# with `docker buildx imagetools inspect docker/dockerfile:<version>`.
# Fugaro go base image (design §7.1): the Go toolchain, gcc (the race detector
# needs cgo), make, git, zstd, tini, gh, Terraform, Claude Code (native
# installer) and fugaro. It runs as the non-root user fugaro (uid 1000) in
# /work/repo. Build it from the repository root with images/build-base.sh go.
#
# It is the base the Fugaro repository dogfoods on, so it carries what that
# repository's verify step runs and no more: not Node.js or Java (the
# Firebase rules emulator job), tflint, or Docker (Cloud Run has no Docker
# daemon, so the docker-tests job can't run there).

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
# The Go toolchain, pinned and verified per architecture below. Keep it in
# the go.mod's `go` line's minor series (a test checks it).
ARG GO_VERSION=1.27.1
# Terraform, the version .github/workflows/ci.yml uses (a test checks it).
ARG TERRAFORM_VERSION=1.16.4
# Claude Code is pinned; bump it deliberately, never float it.
ARG CLAUDE_CODE_VERSION=2.1.283
# gh (the GitHub CLI) is pinned; bump it deliberately, never float it. The
# release tarball is checked against its published sha256sum below, so the
# pin holds even though cli.github.com's apt repo only serves the latest
# release (it has no stable per-version index to apt-pin against).
ARG GH_VERSION=2.101.0
ENV HOME=/home/fugaro \
    PATH=/home/fugaro/.local/bin:/home/fugaro/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    LANG=C.UTF-8 \
    DISABLE_AUTOUPDATER=1 \
    GOTOOLCHAIN=local \
    GOPATH=/home/fugaro/go
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
      ca-certificates curl gcc git libc6-dev libcap2-bin make procps sudo tini unzip xz-utils zstd \
 && rm -rf /var/lib/apt/lists/* \
 && userdel --remove ubuntu \
 && useradd --uid 1000 --user-group --create-home --shell /bin/bash fugaro \
 && install -d -o fugaro -g fugaro -m 0755 /work /work/repo /work/state /etc/claude-code \
 && install -d -o fugaro -g fugaro -m 0700 /work/creds \
 && install -d -o fugaro -g fugaro -m 0755 /home/fugaro/.cache /home/fugaro/go
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
      *) echo "go: unsupported architecture $(dpkg --print-architecture) for gh" >&2; exit 1 ;; \
    esac; \
    tmp=$(mktemp -d); \
    file="gh_${GH_VERSION}_linux_${arch}.tar.gz"; \
    curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/${file}" -o "$tmp/$file"; \
    echo "$sum  $tmp/$file" | sha256sum -c -; \
    tar -xzf "$tmp/$file" -C "$tmp"; \
    install -m 0755 "$tmp/gh_${GH_VERSION}_linux_${arch}/bin/gh" /usr/local/bin/gh; \
    rm -rf "$tmp"; \
    HOME=/root gh --version
COPY --chmod=0755 images/common/finalize-checkout.sh /usr/local/lib/fugaro/finalize-checkout
# The Go toolchain and Terraform, each from its vendor's release with a
# sha256 per architecture, taken on 2026-10-03 from go.dev/dl/?mode=json and
# https://releases.hashicorp.com/terraform/1.16.4/terraform_1.16.4_SHA256SUMS
# (both over TLS). GOTOOLCHAIN=local keeps `go` from downloading some other
# toolchain at run time. The version checks run with HOME=/root so they leave
# no root-owned state under fugaro's home.
RUN set -eu; \
    case "$(dpkg --print-architecture)" in \
      amd64) arch=amd64; gosum=63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445; tfsum=dc94af0eef1147718ad7c8daea792ed199e3e0492eec180d0adafa2a65a879df ;; \
      arm64) arch=arm64; gosum=3450b45a3f9ee8568792736a5c5e70a1f2e9b36c35a8f74958c03e51d7d92bec; tfsum=8263f301cb1a24489a4adeed147bf28504053f77237b3ea97a0ef2972659de30 ;; \
      *) echo "go: unsupported architecture $(dpkg --print-architecture) for Go and Terraform" >&2; exit 1 ;; \
    esac; \
    tmp=$(mktemp -d); \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz" -o "$tmp/go.tgz"; \
    echo "$gosum  $tmp/go.tgz" | sha256sum -c -; \
    tar -xzf "$tmp/go.tgz" -C /usr/local; \
    curl -fsSL "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_${arch}.zip" -o "$tmp/tf.zip"; \
    echo "$tfsum  $tmp/tf.zip" | sha256sum -c -; \
    unzip -q "$tmp/tf.zip" terraform -d /usr/local/bin; \
    rm -rf "$tmp"; \
    HOME=/root CHECKPOINT_DISABLE=1 go version; HOME=/root CHECKPOINT_DISABLE=1 terraform version
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
      *) echo "go: unsupported architecture $(dpkg --print-architecture) for Claude Code" >&2; exit 1 ;; \
    esac; \
    tmp=$(mktemp -d); \
    curl -fsSL "https://downloads.claude.ai/claude-code-releases/${CLAUDE_CODE_VERSION}/linux-${carch}/claude" -o "$tmp/claude"; \
    echo "$csum  $tmp/claude" | sha256sum -c -; \
    mkdir -p "$HOME/.local/bin"; \
    install -m 0755 "$tmp/claude" "$HOME/.local/bin/claude"; \
    rm -rf "$tmp"; \
    v=$(claude --version); echo "$v"; \
    rm -rf "$HOME/.claude" "$HOME/.claude.json" "$HOME/.npm" "$HOME/.cache/claude"; \
    case "$v" in "${CLAUDE_CODE_VERSION} "*) ;; *) echo "go: claude --version reports '$v', not the pinned ${CLAUDE_CODE_VERSION}" >&2; exit 1 ;; esac
# Copied last so a fugaro change doesn't re-download Go or Claude Code.
COPY --from=fugaro /out/fugaro /usr/local/bin/fugaro
WORKDIR /work/repo
LABEL org.opencontainers.image.source=https://github.com/dimipaun/fugaro \
      org.opencontainers.image.description="Fugaro go base image"
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["fugaro", "exec"]
