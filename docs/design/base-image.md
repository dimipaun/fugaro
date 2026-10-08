# One base image, runtimes through mise

Status: design for review (2026-10-08), from the owner's spec (`fugaro-base-image-spec.md`, 133 lines) and the owner's three rulings on it (D1 to D3 below). Every other choice here is a decision the owner can veto in the plan ([plans/2026-10-08-base-image.md](../plans/2026-10-08-base-image.md)). Delivery: release **0.7.0**, after layered config Phase 1.

## 1. Problem

Fugaro publishes three language bases today: `go`, `web-node` and `java-services` (design v1 §7.1). Each pins one toolchain. A repository on Node 20, Java 17 or Python has no base, and every new language or major version is another image to build, scan, publish, copy and document. The bases also disagree on what a container has: `go` has `make` and `gcc` and no `gnupg`; `web-node` has `gnupg` and no `make`. An agent that reaches for a tool the base lacks wastes tokens working around it.

What we want: **one** language-agnostic base that carries everything Fugaro and the agent harnesses need plus a generous general-purpose tool kit, and a single declarative way for each project to add its own runtimes: [mise](https://mise.jdx.dev), driven by the project's `mise.toml`.

## 2. The single base

One image, `ghcr.io/dimipaun/fugaro-base:<version>` (and `:<major>`), built for `linux/amd64` and `linux/arm64`. No language variants, no OS variants.

- **OS: Debian 13 (trixie) slim**, pinned by index digest (§7). Today's bases are Ubuntu 24.04, chosen because Playwright's `--with-deps` knew it; Playwright also supports Debian 12 and 13, so the switch keeps that path (the plan's dogfood checklist runs a Playwright install to prove it). No Alpine: musl breaks prebuilt binaries and native modules. A team with hard constraints brings its own base (§11).
- **Base kind name: `base`.** Internally, Fugaro keeps its "base kind" machinery (the local config's `base_images` map, `--expect-digest KIND=…`, `--replace-image KIND`, the check job's spec) with exactly one kind, `base`, plus the unrelated `history` image. The installation's copy is `<region>-docker.pkg.dev/<project>/fugaro-base/fugaro-base:<version>`. This keeps image refresh, the mirror and the check job working with one-line changes, and leaves room for layered config's image catalog (Phase 2) to add named images later.
- **`fugaro.yaml` loses `base:`.** Every workflow builds on the one base, so the key has nothing to choose. After the cut, any `base:` value is refused with a message naming the migration doc (§10).

## 3. Contents

Layered from slowest-changing to fastest-changing (the spec's order), so a routine rebuild only redoes the top:

| Layer | What | Notes |
|---|---|---|
| 1. OS and kit | `apt-get upgrade`, then: git, git-lfs, openssh-client, ca-certificates, curl, wget, httpie, bind9-dnsutils (`dig`), netcat-openbsd, ripgrep, fd-find (`fd`), fzf, tree, bat (`bat`), eza, jq, sqlite3, build-essential, clang, make, cmake, pkg-config, autoconf, automake, libtool, the dev headers mise needs to build runtimes (libssl, libffi, zlib1g, libxml2, libxslt1, libyaml, libreadline, libbz2, libsqlite3, liblzma, libncurses, libgdbm, uuid, tk), bash, shellcheck, diffutils, patch, less, vim-tiny, nano, unzip, zip, xz-utils, zstd, tar, rsync, procps, tzdata, locales, gnupg, dirmngr, tini, sudo (no rules), libcap2-bin (`getcap`, for the selftest) | Debian names `fdfind` and `batcat`; the base links `fd` and `bat`. `yq` is Mike Farah's Go `yq` (pinned release), not Debian's Python wrapper. `/usr/bin/python3` is Debian's (httpie needs it), with no `pip`; a project's mise Python shadows it. |
| 1b. Cloud | gcloud CLI (pinned tarball, installation properties turn off prompts, usage reporting and update checks), Docker CLI client only (pinned static binary, no daemon, no buildx or compose plugins) | §9 has the threat model. |
| 2. mise | `mise` (pinned, checksummed) in `/usr/local/bin`, its system config `/etc/mise/config.toml`, its shims first on `PATH`, `mise activate` in the user's `.bashrc` | §4. |
| 3. Harnesses | Claude Code (native binary, as today), Codex CLI, Gemini CLI, Pi, OpenCode, Qwen Code, Goose, Crush | §6. |
| 4. Fugaro | `fugaro`, `finalize-checkout`, `fugaro-services`, `/etc/fugaro/base.json` (the pins), the user, `/work` layout, `/etc/claude-code`, `tini` as entrypoint | Unchanged contract (design v1 §7.1). |

Carried over unchanged from today's bases (the audit): the `fugaro` user (uid and gid 1000, home `/home/fugaro`, shell bash); `/work`, `/work/repo`, `/work/state` (0755) and `/work/creds` (0700) owned by `fugaro`; `/etc/claude-code` empty and owned by `fugaro` (the runner writes managed settings there; the selftest refuses baked content); `/usr/local/lib/fugaro/finalize-checkout`; `ENTRYPOINT ["/usr/bin/tini", "--"]`, `CMD ["fugaro", "exec"]`, `WORKDIR /work/repo`, `USER fugaro`; `DISABLE_AUTOUPDATER=1`, `LANG=C.UTF-8`; the OCI source label. New: `/usr/sbin/policy-rc.d` (exit 101, so a package's service never starts during a build, the standard container practice), and `/etc/postgresql-common/createcluster.conf` with `create_main_cluster = false` (so a project that installs PostgreSQL gets no root-owned cluster; `fugaro-services` makes its own as `fugaro`). Dropped: `install-node` (a project's Node comes from mise), the Go toolchain and Terraform (the Fugaro repository declares them in its `mise.toml`), the JDK, PostgreSQL, Redis, firebase-tools and the emulator jars (each project that needs them adds them, §5).

The image's `/etc/fugaro/base.json` records the base's own pins (`{"debian": "sha256:…", "mise": "2026.10.4", "claude_code": "2.1.283", …}`), so `fugaro doctor` and a person reading an image can tell what is inside without running each tool.

## 4. The mise contract

mise is the one sanctioned way to install a language runtime. No `nvm`, `pyenv`, `sdkman` or `asdf`; no runtime or runtime-bound package manager (npm, pnpm, yarn, pip, uv, cargo, maven, gradle) in the base as a system package: they come with the runtime mise installs, so their versions never skew.

**Where a project declares its tools.** In priority order:

1. **The repository's own mise config at its root**: `mise.toml`, `.mise.toml`, `mise/config.toml`, `.mise/config.toml`, `.config/mise.toml`, `.config/mise/config.toml`, their `conf.d/*.toml` fragments, `.tool-versions`, and `mise.lock` beside them. The developers' file is the image's file: one source of truth. `mise.local.toml` is never read (it is not committed).
2. **`image.tools` in `fugaro.yaml`** (new), a map of mise tool to version, for a team that doesn't want a `mise.toml` in its repository:

   ```yaml
   image:
     tools: { node: "24.19.0", python: "3.12", "npm:firebase-tools": "15.32.1" }
   ```

   It is rendered into the image's global mise config, `~/.config/mise/config.toml`. Using both is refused by `fugaro validate` ("keep one: the repository's mise config, which developers use too"), because mise would merge them silently. Keys match `^[a-z0-9][a-z0-9._/:@-]*$` (backends such as `npm:` and `aqua:` included), values `^[A-Za-z0-9][A-Za-z0-9._+~:-]*$`; nothing else can reach the Dockerfile. `image.tools` lives in the `image:` block, so it is exclusive with `dockerfile:` like the rest.

**The system config** (`/etc/mise/config.toml`, in the base), which reaches every process because it is a file, not an environment variable (the agent's environment is an allowlist, and `MISE_*` variables are not on it):

```toml
[settings]
trusted_config_paths = ["/work/repo"]
idiomatic_version_file_enable_tools = []
auto_install = false
not_found_auto_install = false
yes = true

[settings.node]
corepack = true
```

- `/work/repo` is trusted: a mise config can run code (`[env]` `_.source`, hooks, tasks), and so can everything else in the repository, which the agent already runs.
- Idiomatic version files (`.nvmrc`, `.python-version`) are off, mise's own default, stated so it can't drift: only the files listed above decide what is installed, which is what the image config hash covers (§8). The setup skill translates `.nvmrc` and friends into `mise.toml`.
- No auto-install: a tool missing at run time fails loudly (`mise: node@24 is not installed`) instead of downloading in the middle of a run with a different result than the image's. The agent can still run `mise install` itself.
- `node.corepack` installs the corepack shims with every Node, so `packageManager` pnpm and Yarn work as they do on `web-node` today.

**Shims and activation.** `PATH` starts with `/home/fugaro/.local/share/mise/shims`, then `/home/fugaro/.local/bin` (Claude Code), then the system directories. Shims resolve the version from the current directory's config, so `sh -c` commands, the verify step, Claude Code's tool calls and an interactive bash all see the same tools. `.bashrc` also runs `mise activate bash` for interactive shells.

**In the derived image** (`images/derived/Dockerfile.tmpl`), the steps become:

1. `FROM ${FUGARO_BASE}` (unchanged).
2. As root: `image.apt` (unchanged; system packages come first so a runtime mise builds from source can link against them).
3. As `fugaro`: `image.tools`, if set, written to `~/.config/mise/config.toml` (validated text, `printf` with one argument per line).
4. The clone into `/work/repo`, reset to `FUGARO_COMMIT` (unchanged).
5. **`mise install`** in `/work/repo`, as `fugaro`, into `~/.local/share/mise`, with a BuildKit cache mount on `~/.cache/mise` (downloads only; a local rebuild reuses them, Cloud Build starts empty) and the workflow's secrets mounted exactly as for the warm-up (a private npm registry for `npm:` tools; `GITHUB_TOKEN`, which mise uses to lift GitHub's anonymous rate limit). It runs only when the checkout has a mise config or `image.tools` is set. Then `mise ls --current --missing` must print nothing, or the build fails naming the missing tools.
6. The dependency warm-up (unchanged commands), now chosen by what the checkout holds rather than by base: a `package.json` with a supported lockfile gets the Node warm-up. `image.skip_build_scripts` is valid whenever a Node warm-up runs.
7. `image.setup` with the build-only sudo rule (unchanged).
8. The sudo rule removed, setuid bits of sudo and su stripped, `finalize-checkout`, the image record (unchanged).

`mise install` runs after the clone rather than before it, so a saved render (`.fugaro/<workflow>.Dockerfile`) always installs what the checkout's file says, never a copy frozen at render time. The cost is caching: a local rebuild after any commit reruns `mise install`, which the download cache keeps to unpacking. Cloud Build has no layer cache either way.

**A monorepo** gets the tools of the root config. mise honours a subdirectory's own `mise.toml` at run time, but the image installs only the root's: a subdirectory's tools are installed by an `image.setup` step (`cd web && mise install`), or, better, declared at the root.

**A project's Dockerfile** (`dockerfile:`) keeps working: it starts from `fugaro image render`, which now contains the mise step; the lint (`LintDockerfile`) is unchanged except that the only published base it accepts is `ghcr.io/dimipaun/fugaro-base` (or `${FUGARO_BASE}`), and a `FROM` of a removed base names the migration doc. A hand-written Dockerfile that never runs `mise install` builds, and its selftest fails on the missing tools (§8), which says what to add.

## 5. User, sudo and system packages (D1)

**Owner's ruling D1: no sudo for the agent.** The spec's open decision 1 recommended a dedicated user with passwordless sudo "for package installs". The owner rejected that. Today's hardening stays exactly as it is:

- The agent runs as the unprivileged `fugaro` user (uid 1000). The spec's name `agent` is not adopted: uid 1000 named `fugaro` is part of the contract `LintDockerfile`, the selftest, the Cloud Build `prep` step and every existing project Dockerfile rely on.
- `sudo` is installed with **no rules**. A derived build grants `fugaro ALL=(root) NOPASSWD: ALL` for its `image.setup` steps only (what `playwright install --with-deps` needs), then removes the rule and strips the setuid bits of `sudo` and `su`, unconditionally.
- No setuid or setgid binary beyond Debian's standard set and no file capability. The base strips `ssh-keysign`'s setuid bit (host-based ssh authentication is never used). Debian 13's standard sets match the selftest's current allowlists (setuid: `chfn`, `chsh`, `gpasswd`, `mount`, `newgrp`, `passwd`, `umount`; setgid: `chage`, `expiry`, `unix_chkpwd`, a subset of Ubuntu's), and the plan's first Docker check confirms it on the built image.
- `fugaro image selftest` keeps asserting all of it on every derived image: the user is not root, `sudo -n true` fails, `/etc/sudoers.d` holds only its README, PID 1 is `tini`, the root scan finds no unexpected setuid, setgid or capability, `claude` is on `PATH`; and now also the presence checks (§6).

Runtimes need no root: `mise install` runs as `fugaro` into its home. **System packages and services** go into the image while it is built, as root, before the hardening steps: through `image.apt` (and `image.setup` with the build-only sudo), or in the project's Dockerfile above its `USER fugaro` line.

**The deliberate limit.** An agent cannot `apt-get install` during a run. This is a safety limit, not an omission: a root-capable agent could rewrite the runner, the managed settings or the CA store it shares the container with. The documented way around it: bake the package into the image (`image.apt`), or install it in user space mid-run with `mise use -g <tool>@<version>` (mise's aqua and GitHub backends cover most standalone CLIs without root).

**Services (the `java-services` replacement).** `fugaro-services` moves into the base unchanged in behaviour: it starts PostgreSQL, Redis and the Firebase emulators as `fugaro`, on loopback, under `/tmp`. It no longer assumes they are installed: it finds PostgreSQL's newest `/usr/lib/postgresql/<major>/bin`, `redis-server` and `firebase` on `PATH`, and a service that is selected but not installed fails `start` with the one line that installs it. A project that used `java-services` declares:

```toml
# mise.toml
[tools]
java = "temurin-25"
node = "24"
"npm:firebase-tools" = "15.32.1"
```

```yaml
# fugaro.yaml
image:
  apt: [postgresql-17, postgresql-17-postgis-3, postgresql-17-pgvector, redis-server]
  setup: ["firebase setup:emulators:database && firebase setup:emulators:storage && firebase setup:emulators:ui", "./gradlew --no-daemon testClasses"]
```

Debian 13 ships PostgreSQL 17 with PostGIS and pgvector in its main archive, so the PGDG repository and its key are no longer needed. The emulator jars land in `~/.cache/firebase/emulators`, which `fugaro-services` uses when `FIREBASE_EMULATORS_PATH` is unset.

## 6. Agent harnesses

Bundled so that choosing a harness can later be a configuration decision. **Only Claude Code is driven by Fugaro today** (`internal/agent` runs `claude`, the runner writes Claude Code's managed settings, the gateway speaks Anthropic's API). The other harnesses are **present but unsupported by the runner**: they are on `PATH`, they answer `--version` without credentials, and nothing in Fugaro starts them. Selecting a different harness for a run is a separate, later project; this design builds no runner support for it.

| Harness | Install | Arches | Licence | Tier |
|---|---|---|---|---|
| Claude Code 2.1.283 | native binary, sha256 per arch (as today), `~/.local/bin/claude` | amd64, arm64 | proprietary (already redistributed today) | critical |
| Codex CLI 0.161.0 | `openai/codex` musl tarball, sha256 per arch | amd64, arm64 | Apache-2.0 | harness |
| Gemini CLI 0.63.0 | npm, from a committed lockfile | both (Node) | Apache-2.0 | harness |
| Pi 0.73.1 (`@mariozechner/pi-coding-agent`, `pi`) | npm, lockfile | both | MIT | harness |
| Qwen Code 0.25.0 | npm, lockfile | both | Apache-2.0 (repository) | harness |
| OpenCode 1.18.35 | `sst/opencode` tarball, sha256 per arch | amd64, arm64 | MIT | harness |
| Goose 1.53.0 | `block/goose` tarball, sha256 per arch | amd64, arm64 | Apache-2.0 | harness |
| Crush 0.97.1 | `charmbracelet/crush` tarball, sha256 per arch | amd64, arm64 | FSL-1.1-MIT (redistribution allowed; flagged) | harness |

Left out, each a veto-able decision:
- **DeepSeek**: DeepSeek publishes no coding-agent CLI (its GitHub organisation has none; npm's `deepseek-cli` is a third party's, untouched since January 2025). DeepSeek models stay reachable through OpenRouter from any harness above.
- **GitHub Copilot CLI and Amp**: proprietary licences ("see LICENSE.md") that need reading before a public image redistributes them.
- **Cursor CLI**: installs only through a `curl | bash` script, which the base never runs.
- **Aider, Kimi CLI**: Python applications; they would need a private Python runtime. A candidate for a later bump.

**The Node-based harnesses** (Gemini, Pi, Qwen) run on a private Node 24.21.0 in `/opt/fugaro/node`, verified with the existing Node release keyring logic and **not on `PATH`**. Each gets a two-line wrapper in `/usr/local/bin` that execs that Node on the package's entry point, without touching `PATH`, so a harness never sees the project's Node and a project's `node` never resolves to the harness's. They are installed by `npm ci --ignore-scripts` from `images/base/harnesses/package-lock.json` (integrity hashes for every dependency, Dependabot-visible), as root, into `/opt/fugaro/harnesses`, read-only to the agent. `--ignore-scripts` keeps install scripts out of a root build; an optional native helper a harness would fetch at install time is then missing, which presence checks accept (the spec scopes verification to presence).

**Presence checks** come from one table, `images/base/tools.tsv`, embedded in `fugaro` (`images.BaseTools`). Each line names a tool, the argv that must exit 0 (`-` for "on `PATH` only"), the architectures it exists on, and a tier: `critical` (fugaro, claude, git, gh, mise, tini, finalize-checkout), `kit` or `harness`. `fugaro image selftest` runs them with `HOME` pointing at an empty directory and, in every smoke, `--network none`, so a tool that needs credentials or the network to print its version fails. The base's CI smoke runs every row; a derived image's selftest runs the `critical` rows. A tool with no arm64 build gets `amd64` in its row and its install step skips arm64 with a build-log note, rather than failing the image; at design time every row is `all`.

**Runtime injection** (no credential is ever baked; §9). What each tool reads, for whoever wires a harness later. Names marked *unverified* come from each tool's documentation as known at design time and are checked by the plan's docs task against the pinned version's own `--help` or docs:

| Tool | Environment | Files |
|---|---|---|
| Claude Code | `ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`; Vertex: `CLAUDE_CODE_USE_VERTEX`, `CLOUD_ML_REGION`, `ANTHROPIC_VERTEX_PROJECT_ID` | `/etc/claude-code/managed-settings.json` (the runner writes it), `~/.claude/` |
| Codex CLI | `OPENAI_API_KEY`, `CODEX_HOME`; OpenRouter through a `model_providers` entry with `base_url` and `env_key` | `~/.codex/config.toml`, `~/.codex/auth.json` |
| Gemini CLI | `GEMINI_API_KEY`; Vertex: `GOOGLE_GENAI_USE_VERTEXAI`, `GOOGLE_CLOUD_PROJECT`, `GOOGLE_CLOUD_LOCATION` | `~/.gemini/settings.json` |
| Pi | the provider's key variable (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `OPENROUTER_API_KEY`, …) *unverified* | `~/.pi/agent/` *unverified* |
| Qwen Code | `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL` | `~/.qwen/settings.json` *unverified* |
| OpenCode | provider key variables, `OPENROUTER_API_KEY` | `~/.config/opencode/opencode.json`, `~/.local/share/opencode/auth.json` *unverified* |
| Goose | `GOOSE_PROVIDER`, `GOOSE_MODEL`, provider keys | `~/.config/goose/config.yaml` |
| Crush | provider key variables | `~/.config/crush/crush.json` *unverified* |
| gh | `GH_TOKEN` (or `GITHUB_TOKEN`), `GH_HOST`, `GH_ENTERPRISE_TOKEN` | `~/.config/gh/hosts.yml` (the runner configures gh at run time) |
| gcloud | the metadata server (the job's service account, nothing to set on Cloud Run), `CLOUDSDK_CORE_PROJECT`, `CLOUDSDK_CONFIG`, `CLOUDSDK_AUTH_ACCESS_TOKEN_FILE` | `~/.config/gcloud/` |
| Docker CLI | `DOCKER_HOST`, `DOCKER_CONFIG` | `~/.docker/config.json` |
| mise | `GITHUB_TOKEN` or `MISE_GITHUB_TOKEN` (rate limits) | `/etc/mise/config.toml`, `~/.config/mise/config.toml` |

The agent's environment is an allowlist (`internal/agent/env.go`); none of these is added to it by this design. The selftest's list of credential files that must not be baked into `HOME` grows with this table's credential files (`~/.codex/auth.json`, `~/.gemini/oauth_creds.json`, `~/.local/share/opencode/auth.json`, `~/.config/gcloud/credentials.db`, `~/.config/gcloud/application_default_credentials.json`, `~/.claude/.credentials.json`).

**The spec's "git credential helper that consumes runtime-injected credentials"** is already the runner's job: it configures git and gh with the run's token at run time (design v1 §6.2). The base deliberately ships no helper configuration, because `finalize-checkout` and the selftest refuse one in the image.

## 7. Pinning, reproducibility and the monthly bump (D3)

**Owner's ruling D3: the spec's open decision 2, as recommended.**

- **Pinned:** the Debian base by index digest (`ARG DEBIAN_DIGEST=sha256:a29215f6…`, used in the `FROM`), and every Fugaro-critical component by version and per-architecture sha256 as `ARG` lines in `images/base/Dockerfile`: mise, Claude Code, gh, gcloud, the Docker CLI, yq, the harness tarballs and the harness Node. The npm harnesses are pinned by their lockfile. Every pin is one `ARG NAME=value` line (checksums included: `ARG MISE_SHA256_AMD64=…`), so a script can rewrite them and tests can read them.
- **Floating:** Debian packages. The first `RUN` does `apt-get update && apt-get upgrade`, so a rebuild picks up Debian's security fixes on top of the pinned starting point. The nightly images run (today's patch refresh of the latest release) keeps doing that.
- **The monthly bump:** a scheduled workflow (`base-bump.yml`, the 3rd of each month, and on demand) runs `images/base/bump.sh`. It resolves each pinned component's latest release and the Debian index digest, computes each download's sha256 itself (Claude Code's comes from its release manifest; the harness Node keeps being verified at build time against the Node release keys), refreshes the harness lockfile (`npm update --package-lock-only --ignore-scripts`), rewrites the `ARG` lines and opens one pull request listing every change. **It never merges.** The `# syntax` frontend digest and the Trivy digest, which neither Dependabot nor the script rewrites, stay hand-bumped monthly, and the PR's body reminds the reviewer. A pull request opened with the workflow's `GITHUB_TOKEN` does not trigger other workflows (GitHub's rule), so the bump job builds and smoke-tests both architectures itself before opening it, and its body says to close and reopen the pull request to run CI.

**No secrets in any layer.** Nothing secret is a build argument (the `ARG`s are versions and checksums). CI scans every built image, both architectures, on every pull request, with **Trivy's secret scanner** over every layer and the image config (`trivy image --scanners secret --image-config-scanners secret --exit-code 1`). Trivy is chosen because it is already pinned by digest and run by `images/scan.sh`, it analyses each layer separately (so a file added in one layer and deleted in a later one is still seen), it reads the image config's history and environment (where a leaked build argument would show), and it runs as one container on a GitHub runner with no account. Gitleaks scans git history and directories, not image layers, so it would need an export step and would miss the config. The scan fails the job on any finding; a false positive is allowed only by a path entry in `images/trivy-secret.yaml` with a comment saying why.

## 8. The derived image, the record and the checks

- **The image config hash** (`imagecheck.ImageConfigHash`) gains the blob IDs of the root mise config files and `image.tools`. A changed `mise.toml` makes the image wrong (a runtime of another version), not just stale, so it always rebuilds, like a changed Dockerfile. `image.node` and `image.jdk` leave the hash.
- **Key files** (the lockfile trigger and the default cache) are chosen by what the checkout holds, not by base: the Node package manager's lockfile when there is one.
- **Defaults** stop depending on the base: every workflow gets `cpu: 4`, `memory: 8Gi` and reports `["**/junit*.xml", "**/build/test-results/**/*.xml"]` unless it sets its own. A former `java-services` workflow that relied on 16Gi sets `resources.memory: 16Gi` (the migration doc says so).
- **The selftest** (`fugaro image selftest`) drops the `node` and `image.node` check and gains: the presence checks of §6 (`tools: critical` in a derived image's smoke, `all` in the base's CI smoke); a `mise-tools` check that runs `mise ls --current --missing --json` in `/work/repo` with `MISE_OFFLINE=1` and passes only when nothing is missing; and the wider credential-file list. Everything else is unchanged, including the root scan in a second container.
- **Unchanged:** the BuildKit secret mounts (now also on the mise step), the git credential as a build secret, `FUGARO_COMMIT`, the candidate, smoke, gate, promote, record and untag steps, and the smoke's `--network none`.

## 9. Security

- **The trust boundary is still the container** (design v1 §6). Nothing here gives the agent a capability it lacks today: no sudo, no setuid, no Docker socket, no new environment variable.
- **gcloud.** The job's service account token is already reachable through the metadata server (v1 §6.2: "it can use anything the job's service account can reach"); `curl` and the Go clients use it today. gcloud makes that easier, not possible. IAM on the job's account remains the control. Accepted.
- **The Docker CLI** is inert on Cloud Run: there is no daemon and no socket, and the selftest refuses a baked `~/.docker/config.json`. It is included because the spec asks for it and a future VM backend may provide a daemon; it costs about 40 MB. **Flag:** on today's only backend it does nothing at all, so it is a veto-able decision (dropping it is one deleted block).
- **More third-party code in the image.** Eight harnesses and a larger kit. Each is pinned by checksum or lockfile at a bump that a person reviews; none runs unless something invokes it, and Fugaro invokes only Claude Code. The Node harnesses live in root-owned `/opt`, so the agent can't replace them; Claude Code stays in `~/.local/bin` as today (moving it is a separate change).
- **mise downloads** are verified per backend (Node's SHASUMS and OpenPGP signature, aqua's checksums and attestations, the Java metadata's checksums), which is weaker than a hand-pinned sha256 per file. A project that wants hand-pinning commits `mise.lock`, which records each tool's URL and checksum; the base reads an existing lockfile (mise's default). Making `mise.lock` mandatory is not in scope.
- **Build-time network.** `mise install` runs in the derived build with network, as the warm-up does; like it, it runs without the metadata server (the build's `RUN` steps are not on the `cloudbuild` network, live check 11b).
- **No secrets in the image** (§7): the build arguments are public pins, the secret scan gates every build, the selftest refuses credential files in `HOME`.

## 10. Migration: a hard cut (D2)

**Owner's ruling D2: hard cut at the consolidation release.** In 0.7.0 the legacy kinds are gone everywhere, with no deprecated alias and no overlap period:

- `config.Bases` is `["base"]`; `fugaro.yaml` has no `base:`; the schema's `base` enum is gone; `image.node` and `image.jdk` are gone.
- `fugaro init --base` accepts only `base`; `--expect-digest` and `--replace-image` accept `base` and `history`; `--base-image` takes `base=IMAGE` or an image named `fugaro-base`.
- The local config's `base_images` accepts only the key `base`.
- `images/go`, `images/web-node`, `images/java-services`, `install-node.sh` and their tests are deleted; the images workflow builds `base` and `history`; the CI jobs `base (go)`, `base (java-services)` and `base (web-node)` are gone and `fugaro-base (amd64)` and `fugaro-base (arm64)` remain; the release footer and `verify-public` list `fugaro-base` and `fugaro-history`.
- Already-published `fugaro-go`, `fugaro-web-node` and `fugaro-java-services` tags stay in ghcr.io (they are immutable history), but the nightly run stops refreshing them, and the docs say so.

**What the strict parsers say.** The refusals name the doc, `docs/base-image-migration.md`, and the exact fix:

- `fugaro.yaml` with `base:` → `workflows.web.base: base: was removed in 0.7.0: every workflow builds on the one Fugaro base image. Delete this line and declare the workflow's runtimes in mise.toml (docs/base-image-migration.md)`.
- `image.node` → `workflows.web.image.node: was removed in 0.7.0: declare node in mise.toml ([tools] node = "24.19.0") or in image.tools (docs/base-image-migration.md)`. `image.jdk` gets the Java form.
- A local config `base_images` key other than `base` → `local config: base_images.go: the go, web-node and java-services bases were removed in 0.7.0; delete the line and run fugaro image refresh in each repository, which copies fugaro-base and records base_images.base (docs/base-image-migration.md)`.
- A `.fugaro/*.Dockerfile` `FROM ghcr.io/dimipaun/fugaro-web-node` → `builds FROM fugaro-web-node, a base removed in 0.7.0: build FROM ${FUGARO_BASE} (docs/base-image-migration.md)`.
- `fugaro doctor` reports each of these through the same messages (it already runs the parsers), and adds one line when `base_images.base` is missing: `run fugaro image refresh in the repository's checkout`.
- The **published shared config** (in the runs bucket) is not the operator's file: legacy keys found there are dropped with a warning (`the project's shared config still lists base_images.go; the next fugaro init or fugaro image refresh republishes it`), so one teammate's old publish can't lock everyone out.
- A **check job** whose spec still names legacy kinds is rewritten by `fugaro image refresh` to `{"base": <ref>}` and the new image, in the one update it already makes.

**The migration doc, per kind:**

| Was | `mise.toml` to write | `image:` | `resources` |
|---|---|---|---|
| `go` | `go = "<go.mod's go line, exact>"`, plus `terraform = "<ver>"` if the repository used the base's Terraform; `[env] GOTOOLCHAIN = "local"` | `setup` unchanged | unchanged |
| `web-node` | `node = "<image.node, or the base's 24>"`; pnpm and Yarn come from corepack (`packageManager`) as before | `node` moves to `mise.toml`; `apt`, `setup`, `skip_build_scripts` unchanged | unchanged |
| `java-services` | `java = "temurin-25"` (the old base's pinned JDK, since `image.jdk` was refused), `node = "24"` and `"npm:firebase-tools" = "15.32.1"` if the tests use the emulators | `apt: [postgresql-17, postgresql-17-postgis-3, postgresql-17-pgvector, redis-server]` (only the services used), `setup` gains the `firebase setup:emulators:<name>` lines the tests need | `memory: 16Gi` explicitly (the old base default) |

In every case: delete `base:`; a `.fugaro/*.Dockerfile` is re-rendered with `fugaro image render` and the project's edits reapplied; `fugaro validate` then `fugaro image build --local` prove it. `fugaro-services start` in `commands.test` keeps working unchanged.

**The upgrade order** (strict parsers on both sides mean no `fugaro.yaml` is valid for 0.6 and 0.7 at once):

1. Every operator upgrades the CLI to 0.7.0 (`fugaro upgrade --local` for the pin, Homebrew for the binary). A 0.7.0 CLI refuses an old `fugaro.yaml`, so step 2 follows at once.
2. Per repository, one pull request with the `mise.toml` and the `fugaro.yaml` change (the setup skill writes it), validated with `fugaro image build --local`. Merge it.
3. Right after the merge, in that checkout: `fugaro image refresh`. It copies `fugaro-base`, rewrites the check job's image and spec, and rebuilds every workflow from the new base. Until it finishes, a run launched against the repository uses the old image, whose 0.6 runner refuses the new `fugaro.yaml`: don't launch runs in that window (minutes).
4. `fugaro doctor` in the checkout says nothing about bases.

## 11. Bring your own image

The base doesn't suit everyone. The supported paths, in increasing effort:

1. `image.apt`, `image.tools`, `image.setup`: most needs.
2. A project Dockerfile (`dockerfile:`) built `FROM ${FUGARO_BASE}`: anything, starting from `fugaro image render`.
3. A **custom base**: `fugaro init --base-image base=<image>` points the installation at an image of the team's own, which every derived build then starts from. It must keep the base contract the selftest checks: the `fugaro` user (uid 1000), `tini` as entrypoint, `/work` with its modes, `/etc/claude-code` empty and owned by `fugaro`, `fugaro`, `claude`, `git`, `gh`, `mise` and `finalize-checkout` on their paths, no passwordless sudo, no unexpected setuid, setgid or capability. `fugaro image build --local --base <image>` proves it before anything goes to the cloud. A wholly different compute environment is a backend (`docs/backends.md`); only Cloud Run is implemented.

## 12. Multi-architecture

- **Build:** `linux/amd64` on `ubuntu-latest` and `linux/arm64` on GitHub's native `ubuntu-24.04-arm` runner (free for public repositories), one job per architecture, no QEMU. Each builds with `images/build-base.sh base` (`PLATFORM` set), smoke-tests, scans and saves its image as an artifact, as today.
- **Publish:** the publish job pushes `fugaro-base:<v>-amd64` and `fugaro-base:<v>-arm64`, then creates the multi-platform index `fugaro-base:<v>` (and `:<major>` when it moves) with `docker buildx imagetools create` from the two pushed digests. `history` stays amd64-only (its jobs run on Cloud Run).
- **verify-public** requires both `linux/amd64` and `linux/arm64` in `fugaro-base`'s index, and `linux/amd64` for `fugaro-history`.
- **The installation's copy stays amd64-only, and that is correct.** The mirror (`internal/mirror`) already reads an index: `--expect-digest` is compared with the **index** digest (what the release tag resolves to), the `linux/amd64` child is selected, its config is read to prove the platform, and only that manifest and its blobs are copied and verified by digest. Cloud Run runs amd64. No change is needed; a test pins that an amd64+arm64 index copies only the amd64 child.
- **Local builds:** `fugaro image build --local` keeps `--platform linux/amd64` as the default (the rehearsal of the cloud build; on Apple silicon it runs under emulation). `--platform linux/arm64` now works, because the published base has an arm64 build: a much faster loop for the setup skill, at the cost of not exercising the architecture the cloud runs. The setup skill uses arm64 to iterate on an Apple-silicon machine and says that the cloud build is the amd64 test.
- **Gaps:** every bundled tool has an arm64 build at design time. The tools table's `arches` column and the install steps' architecture `case` are the mechanism for one that doesn't.

## 13. Size, build time and cold start

This is a real risk. Estimates for amd64, uncompressed (the plan's first Docker check replaces them with measurements):

| Part | Size |
|---|---|
| Debian 13 slim | 0.08 GB |
| apt kit (compilers, clang, headers, git, python3 for httpie, editors, shell tools, locales) | 1.1 GB |
| gcloud (bundled Python) | 0.55 GB |
| Harnesses (Claude Code 0.24, Goose 0.2, OpenCode 0.15, Codex 0.1, Crush 0.07, Node and npm harnesses 0.45) | 1.2 GB |
| mise, gh, yq, Docker CLI, fugaro | 0.2 GB |
| **Total** | **about 3.1 GB** (1.1 to 1.3 GB compressed) |

Today: `web-node` 0.7 GB, `java-services` 2.1 GB.

- **Cloud Build:** each derived build pulls the base (no layer cache): about a minute more than `web-node` on the default machine, plus `mise install` (Node about 30 MB, Temurin about 200 MB): one to two minutes. `java-services` builds already pulled 2.1 GB.
- **The copy:** `fugaro init --base` and `image refresh` move the base through the operator's machine: about 1.2 GB instead of about 0.3 GB. At 50 Mbit/s up that is about 3.5 minutes; at 10 Mbit/s about 16. The confirmation already shows the megabytes. Registry storage is about $0.12 a month per copy.
- **Cold start:** a run's job pulls its derived image, base layers included. Whether Cloud Run jobs stream images lazily or pull them whole before start is **not verified**; the plan's live checklist item measures the time from execution start to the runner's first log line on the sandbox, before and after, on the same workflow. Until measured, the mitigations are structural:
  - layer order (§3): the large, stable layers come first and change only at a monthly bump, so derived images built on one base share them byte for byte in the installation's registry;
  - the derived image's own layers stay small (checkout, warm caches);
  - a CI size budget: `images/size-budget.sh` fails the build above 4.0 GB uncompressed per architecture and warns above 3.5 GB, so growth is a decision rather than drift; the publish job records each architecture's compressed size in the run summary.
- **If the measurement is bad** (the veto-able fallback): gcloud is the largest optional piece and the first to move to an on-demand `mise use -g gcloud` (mise has a gcloud backend); the harness tier is the second. The spec's "size is not a concern" stands unless the measurement says otherwise.
- **The daily check job** runs from the base image too, so it pulls the larger image once a day per repository; its CPU time is the cost (cents). Moving it to the small `history` image is a follow-up.

## 14. The setup skill

The setup skill (`plugin/skills/setup`) changes in four places:

- **Discovery** detects languages and versions from `.nvmrc`, `.node-version`, `.python-version`, `.ruby-version`, `.tool-versions`, `package.json` `engines` and `packageManager`, `go.mod` (`go` and `toolchain`), `pom.xml` (`maven.compiler.release`, `java.version`), `build.gradle(.kts)` toolchains, `rust-toolchain.toml`, CI `setup-*` actions and CI images, each cited by file and line.
- **It writes `mise.toml`** at the repository root, pinning what it found (exact versions when the repository pins one, else the major), or **respects** an existing `mise.toml` or `.tool-versions`: it adds a missing tool only with the user's agreement and never rewrites a pinned version. `image.tools` is offered only when the user doesn't want a `mise.toml` in the repository.
- **One Dockerfile shape.** When a Dockerfile is needed, it is always `fugaro image render` saved as `.fugaro/<workflow>.Dockerfile` with the edits in the two marked places (system packages above `USER fugaro`; checkout-dependent steps in the setup section), so the mise step, the secret mounts and the hardening are always the template's. Runtimes are never installed by `apt-get install <language>` or a `curl | sh` installer: they are mise tools. Services (databases, emulators) are layered after the mise step: `image.apt` for the packages, `image.setup` for their data downloads, `fugaro-services start` in `commands.test`.
- **The loop** stays `fugaro validate --json` then `fugaro image build --local --json`, on `--platform linux/arm64` on Apple silicon (§12).

The base kinds table, the per-base examples and the "no base for Python, Rust, Ruby" row disappear: every language is a `mise.toml`.

## 15. Dogfood verification

- **Fugaro itself** (the owner's installation `fugaro-dev`): a `mise.toml` with `go = "1.27.1"`, `terraform = "1.16.4"` and `[env] GOTOOLCHAIN = "local"` (a test pins them to `go.mod` and `ci.yml`, as the `go` base's ARGs are pinned today); `fugaro.yaml` drops `base: go` and keeps its setup step. The switch lands with the release (§16), because the owner's current jobs run 0.6.
- **The Belong React frontend and the Belong server** are the owner's to verify, sandbox first, with the checklist the migration doc carries (and the plan's release task repeats). The checklist: render and `fugaro validate`; `fugaro image build --local` on arm64, then amd64; selftest passes; a Playwright install (`--with-deps`) succeeds on Debian 13 where the frontend uses it; `fugaro-services start` brings up PostgreSQL with PostGIS and pgvector, Redis and the emulators the server's tests use; one sandbox run end to end; then the real repositories. Fugaro's agents never touch EdgeWeb or EdgeServer.

## 16. Rollout and release

- **Version 0.7.0.** A breaking change before 1.0 is a minor bump. Layered config Phase 1 ships first (presumably 0.6.0) and this design is consistent with it: the base is installation-level data (`base_images.base` in the local and shared config), which Phase 1's environment and profile layers carry wherever they put `base_images`; a workflow's runtime choice lives in the repository (`mise.toml` or `image.tools`), never in a profile. Layered config's image catalog (its Phase 2) comes after this: it builds on one base and a `base_images` map that can grow named entries again. (`docs/design/layered-config.md` was not yet on `origin` when this was written; the plan's Global Constraints say how to reconcile.)
- **Order of work:** the base image and its CI first (additive: `fugaro-base` is built and may even be published by a 0.6.x release, unused); then the Go and config changes (additive in `main`: `base` is accepted next to the legacy kinds); then docs and the setup skill; **then the removal of the legacy kinds, last**, and the release that ships it. No release is cut from `main` between the Go changes and the removal (the release freeze), so no published version accepts both.
- **Operator notes** for the release Highlights: the upgrade order of §10, the migration doc, the copy size, and "don't launch runs between merging the migration pull request and `fugaro image refresh` finishing".

## 17. Non-goals

- Runner support for any harness but Claude Code, and choosing a harness per run.
- Passwordless sudo, or any root at run time (D1).
- Alpine, distroless or OS variants; a slim variant of the base.
- Deprecated aliases or an overlap period for the old kinds (D2).
- Auto-merging the monthly bump.
- A mandatory `mise.lock`; installing a monorepo's subdirectory configs automatically.
- Signing the images (the existing F1 follow-up still applies, to `fugaro-base`).
- Moving the check job off the base image; moving Claude Code out of `HOME`.
- Touching EdgeWeb or EdgeServer.

## 18. Decisions

The owner's rulings, binding: **D1** no sudo for the agent (spec open decision 1 rejected), **D2** hard cut at 0.7.0, **D3** pinning plus a monthly bump plus an image secret scan (spec open decision 2 accepted). The plan lists them with the veto-able decisions this design makes: Debian 13; the kind name `base` and the image name `fugaro-base`; `base:` removed rather than kept with one value; the user stays `fugaro`; the mise system settings; `image.tools` and its exclusivity; `mise install` after the clone; warm-ups by content; the hash covering mise files; uniform defaults; `fugaro-services` in the base with services installed by projects; the harness list and its exclusions; the private harness Node; the Docker CLI kept; native per-architecture runners and per-architecture tags; the amd64-only installation copy; the local build's default platform; the size budget; Trivy for secrets; the bump workflow's PR without auto-merge; the shared-config and check-job handling of legacy keys; the release freeze; 0.7.0.

## 19. Delivery

Four pull request groups and a release (the plan has the tasks):

1. **The base image and its CI** (Docker): the tools table and presence checks, `images/base/` (Dockerfile, mise config, harness lockfile, wrappers), `fugaro-services` generalised, the smoke, size budget and secret scan, the images workflow (per-architecture build, index publish, verify-public), the sample-project Docker test, the monthly bump. A Fugaro run can write and unit-test all of it but cannot build an image (Cloud Run has no Docker daemon): CI's `images` and `docker-tests` jobs and the owner verify the builds.
2. **Go and config** (additive): `base` as a kind, `base:` optional, `image.tools`, mise detection and the `validate` rules, the template's mise step and warm-ups by content, the hash, the selftest, `fugaro.yaml`'s schema, Fugaro's own `mise.toml`.
3. **Docs and skills:** `docs/base-image.md` (the user-facing contract), `docs/base-image-migration.md`, design v1 §7, the other docs, the setup skill and its fixtures, docs tests.
4. **The cut:** the legacy kinds removed everywhere, the tombstone refusals, the local and shared config handling, the check-job rewrite, the image directories, the workflows and release files.
5. **Release 0.7.0** with `/new-release`, the operator notes, Fugaro's own switch, and the Belong checklist handed to the owner.
