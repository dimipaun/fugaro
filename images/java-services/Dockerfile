# syntax=docker/dockerfile:1.10@sha256:865e5dd094beca432e8c0a1d5e1c465db5f998dca4e439981029b3b81fb39ed5
# The frontend pin above (also in images/derived/Dockerfile.tmpl and the
# render_test goldens) isn't covered by Dependabot, which doesn't update
# "# syntax" lines: bump it by hand monthly, alongside the base refreshes,
# with `docker buildx imagetools inspect docker/dockerfile:<version>`.
# Fugaro java-services base image (design §7.1): for repositories whose tests
# need real backing services next to a JDK, such as a Spring + Gradle server
# (EdgeServer). It carries
#   - Temurin JDK 25 (pinned, checksummed); Gradle is NOT baked in, the
#     repository's own wrapper brings its Gradle into ~/.gradle,
#   - PostgreSQL 17 (PGDG, major pinned) with PostGIS and pgvector, and
#   - Redis, Node.js (the web-node pin) and firebase-tools (pinned) with every
#     Firebase emulator the pinned release can download already in the image,
#   - fugaro-services, which starts and stops all of these as the non-root user
#     (see images/common/fugaro-services),
# plus git, zstd, tini, gh, Claude Code (native installer) and fugaro, as in the
# other bases. It runs as the non-root user fugaro (uid 1000) in /work/repo.
# Build it from the repository root with images/build-base.sh java-services.
#
# Nothing here needs a writable root filesystem: the services keep their state
# under /tmp (FUGARO_SERVICES_DIR), the Gradle caches and Node's live under
# $HOME, and the emulator jars are in the image (/opt/firebase-emulators).

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


FROM ubuntu:24.04
# Temurin, from github.com/adoptium/temurin25-binaries (tag
# jdk-${JDK_VERSION}+${JDK_BUILD}); the build checks a sha256 per architecture.
ARG JDK_VERSION=25.0.4.1
ARG JDK_BUILD=1
# PostgreSQL's major version, from the PGDG apt repository (apt.postgresql.org),
# which carries postgis and pgvector for it. The minor version floats with the
# repository, as it does for any security refresh of the base.
ARG POSTGRES_MAJOR=17
# Node.js, for firebase-tools; the same pin as the web-node base (a test checks it).
ARG NODE_VERSION=24
# firebase-tools, pinned; its emulator jars are downloaded for this version below.
ARG FIREBASE_TOOLS_VERSION=15.32.1
# Claude Code is pinned; bump it deliberately, never float it.
ARG CLAUDE_CODE_VERSION=2.1.283
# gh is pinned and checksummed below, as in the other bases.
ARG GH_VERSION=2.101.0
ENV HOME=/home/fugaro \
    JAVA_HOME=/opt/java \
    PATH=/home/fugaro/.local/bin:/opt/java/bin:/opt/node/bin:/usr/lib/postgresql/${POSTGRES_MAJOR}/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    LANG=C.UTF-8 \
    DISABLE_AUTOUPDATER=1 \
    FIREBASE_EMULATORS_PATH=/opt/firebase-emulators \
    NO_UPDATE_NOTIFIER=1
# sudo, libcap2-bin and /etc/claude-code are as in the other bases. gnupg and
# dirmngr are for install-node and the PGDG key. The postgresql-common
# create_main_cluster=false setting stops the package from creating a cluster
# at install time (we create one per run, as fugaro, in fugaro-services), and
# RUNLEVEL=1 keeps the Redis and Postgres packages from trying to start a
# service during the build.
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      ca-certificates curl dirmngr git gnupg libcap2-bin procps sudo tini xz-utils zstd \
 && rm -rf /var/lib/apt/lists/* \
 && userdel --remove ubuntu \
 && useradd --uid 1000 --user-group --create-home --shell /bin/bash fugaro \
 && install -d -o fugaro -g fugaro -m 0755 /work /work/repo /work/state /etc/claude-code \
 && install -d -o fugaro -g fugaro -m 0700 /work/creds \
 && install -d -o fugaro -g fugaro -m 0755 /home/fugaro/.cache /home/fugaro/.gradle
# PostgreSQL, PostGIS and pgvector for ${POSTGRES_MAJOR} from PGDG. The repository
# signing key is fetched over TLS and refused unless its fingerprint is the
# one PostgreSQL publishes (https://www.postgresql.org/download/linux/ubuntu/,
# key ACCC4CF8, checked 2026-10-03). Redis is Ubuntu's (7.0). PostgreSQL's
# own `postgres` account exists after the install but nothing runs as it: the
# cluster belongs to fugaro.
RUN set -eu; \
    mkdir -p /etc/postgresql-common /etc/apt/keyrings; \
    printf 'create_main_cluster = false\n' > /etc/postgresql-common/createcluster.conf; \
    tmp=$(mktemp -d); \
    export GNUPGHOME="$tmp/gnupg"; install -d -m 0700 "$GNUPGHOME"; \
    curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc -o "$tmp/pgdg.asc"; \
    keys=$(gpg --show-keys --with-colons "$tmp/pgdg.asc" | grep -c '^pub:'); \
    fpr=$(gpg --show-keys --with-colons "$tmp/pgdg.asc" | awk -F: '/^fpr:/ {print $10; exit}'); \
    [ "$keys" = 1 ] && [ "$fpr" = B97B0AFCAA1A47F044F244A07FCC7D46ACCC4CF8 ] \
      || { echo "java-services: the PGDG key file has $keys primary keys, the first with fingerprint '$fpr'" >&2; exit 1; }; \
    gpg --import "$tmp/pgdg.asc"; \
    gpg --export B97B0AFCAA1A47F044F244A07FCC7D46ACCC4CF8 > /etc/apt/keyrings/pgdg.gpg; \
    rm -rf "$tmp"; \
    . /etc/os-release; \
    echo "deb [signed-by=/etc/apt/keyrings/pgdg.gpg] https://apt.postgresql.org/pub/repos/apt ${VERSION_CODENAME}-pgdg main" > /etc/apt/sources.list.d/pgdg.list; \
    apt-get update; \
    RUNLEVEL=1 DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      "postgresql-${POSTGRES_MAJOR}" "postgresql-${POSTGRES_MAJOR}-postgis-3" "postgresql-${POSTGRES_MAJOR}-pgvector" \
      redis-server redis-tools; \
    rm -rf /var/lib/apt/lists/*; \
    [ -x "/usr/lib/postgresql/${POSTGRES_MAJOR}/bin/initdb" ]
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
      *) echo "java-services: unsupported architecture $(dpkg --print-architecture) for gh" >&2; exit 1 ;; \
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
# Node.js (installed and verified by web-node's script, from nodejs.org into
# /opt/node), then firebase-tools and the emulator jars. `firebase
# setup:emulators:X` downloads the jar of emulator X into
# FIREBASE_EMULATORS_PATH; auth, functions, hosting and eventarc run inside
# firebase-tools itself and have no jar. The jars are fetched as root into a
# root-owned directory outside $HOME, so a read-only root filesystem with a
# fresh $HOME still finds them. The version checks run with HOME=/root.
RUN /usr/local/lib/fugaro/install-node keyring \
 && /usr/local/lib/fugaro/install-node "$NODE_VERSION"
# Temurin, checksummed per architecture; taken on 2026-10-03 from
# api.adoptium.net/v3/assets/latest/25/hotspot (over TLS).
RUN set -eu; \
    case "$(dpkg --print-architecture)" in \
      amd64) arch=x64; sum=dbb698396d478e7fa2b1e50f4103324b2a99b90569ee27c33f2261f9215cf41e ;; \
      arm64) arch=aarch64; sum=69df11a02cfa3ef7d7ca645e03edce6778ec090e100f6ae2b42097865730ac52 ;; \
      *) echo "java-services: unsupported architecture $(dpkg --print-architecture) for the JDK" >&2; exit 1 ;; \
    esac; \
    tmp=$(mktemp -d); \
    file="OpenJDK25U-jdk_${arch}_linux_hotspot_${JDK_VERSION}_${JDK_BUILD}.tar.gz"; \
    curl -fsSL "https://github.com/adoptium/temurin25-binaries/releases/download/jdk-${JDK_VERSION}%2B${JDK_BUILD}/${file}" -o "$tmp/$file"; \
    echo "$sum  $tmp/$file" | sha256sum -c -; \
    mkdir -p /opt/java; \
    tar -xzf "$tmp/$file" -C /opt/java --strip-components=1; \
    rm -rf "$tmp"; \
    HOME=/root java -version
RUN set -eu; \
    # npm install runs as root at build time; firebase-tools' own version is
    # pinned but its transitive dependencies float (accepted: there is no
    # lockfile for a global install, and the nightly rebuild picks up fixes).
    HOME=/root npm install -g --no-audit --no-fund "firebase-tools@${FIREBASE_TOOLS_VERSION}"; \
    install -d -m 0755 "$FIREBASE_EMULATORS_PATH"; \
    for e in database firestore storage pubsub ui dataconnect; do \
      HOME=/root firebase "setup:emulators:$e"; \
    done; \
    v=$(HOME=/root firebase --version); echo "firebase-tools $v"; \
    [ "$v" = "$FIREBASE_TOOLS_VERSION" ] || { echo "java-services: firebase --version reports '$v', not the pinned $FIREBASE_TOOLS_VERSION" >&2; exit 1; }; \
    rm -rf /root/.npm /root/.config /root/.cache/firebase "$FIREBASE_EMULATORS_PATH"/*.log; \
    du -sh "$FIREBASE_EMULATORS_PATH"
COPY --chmod=0755 images/common/fugaro-services /usr/local/bin/fugaro-services
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
      *) echo "java-services: unsupported architecture $(dpkg --print-architecture) for Claude Code" >&2; exit 1 ;; \
    esac; \
    tmp=$(mktemp -d); \
    curl -fsSL "https://downloads.claude.ai/claude-code-releases/${CLAUDE_CODE_VERSION}/linux-${carch}/claude" -o "$tmp/claude"; \
    echo "$csum  $tmp/claude" | sha256sum -c -; \
    mkdir -p "$HOME/.local/bin"; \
    install -m 0755 "$tmp/claude" "$HOME/.local/bin/claude"; \
    rm -rf "$tmp"; \
    v=$(claude --version); echo "$v"; \
    rm -rf "$HOME/.claude" "$HOME/.claude.json" "$HOME/.npm" "$HOME/.cache/claude"; \
    case "$v" in "${CLAUDE_CODE_VERSION} "*) ;; *) echo "java-services: claude --version reports '$v', not the pinned ${CLAUDE_CODE_VERSION}" >&2; exit 1 ;; esac
# Copied last so a fugaro change doesn't re-download the JDK or the emulators.
COPY --from=fugaro /out/fugaro /usr/local/bin/fugaro
WORKDIR /work/repo
LABEL org.opencontainers.image.source=https://github.com/dimipaun/fugaro \
      org.opencontainers.image.description="Fugaro java-services base image"
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["fugaro", "exec"]
