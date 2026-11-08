# The Fugaro base image

Every workflow's image is built on one base, `ghcr.io/dimipaun/fugaro-base` (for `linux/amd64` and `linux/arm64`), plus the repository's checkout, its runtimes and its dependencies. The base carries what Fugaro and the agents need, and a generous tool kit; **your project declares its language runtimes in `mise.toml`**, and the image installs them. The design is [design/base-image.md](design/base-image.md).

**Published from 0.7.0.** `ghcr.io/dimipaun/fugaro-base` is built and published by the `images` workflow (`fugaro-base (amd64|arm64)` and `publish-base`) from the first release tag that carries it, 0.7.0; a development build or an earlier release has no released image to resolve, so `fugaro update --base base` and `fugaro image refresh` refuse with a clear error there; [Bring your own](#bring-your-own) below (your own build of `images/base/`) works with any build.

## The published tags, and what the nightly does

A release tag publishes the base under `:<version>` (with `:<version>-amd64` and `:<version>-arm64` beside it, and a moving `:<major>`), and writes `:<version>-pinned` at the same digest **only after** verifying the signature over that digest. The `images` workflow also runs on a nightly schedule; it rebuilds the latest release tag's tree, so a night can genuinely produce a different image — the build installs Debian's current security fixes, and it fetches the harnesses at build time — but a night where those upstream bytes have not moved rebuilds the same image byte for byte, because the build is reproducible for identical inputs. **On such a night the nightly leaves the tags where they are**: it compares what it just built with what `:<version>-<arch>` already names — the image's config digest, and, when the digests differ, the pins the build recorded, the tree's own `fugaro` binary and the installed package list — and when they agree it pushes nothing, so the tags do not move and nothing is signed. A night that does find different bytes publishes them, and the tags move as they always could. **This change is staged, not yet in `.github/workflows/`** (issue #1745, `docs/pending-workflows/images.yml`): until the owner copies it in on the branch a release is tagged from, every nightly republishes what it rebuilt.

**What that changes for an installation.** The daily image check's "the base moved" rule reads the base reference your config pins and the digest that reference resolves to, and rebuilds a repository's image when that digest differs from the one its last build recorded — the same read `fugaro image refresh` makes when it decides a workflow is `current`. Two consequences:

- An installation pinned at `base_images.base` (a digest through your own registry's remote repository) is unaffected by any of this: a digest cannot move, which is what the pin is for.
- An installation still pointing at a tag no longer sees the tag move on a quiet night, because nothing is pushed then. It can still move on a night that picks up an upstream change, so the check can still fire for a real reason.

**Installations still pin `-pinned`, and the reason is the signature, not the moving tag.** `fugaro update` and `fugaro image refresh` resolve `:<version>-pinned` (the tag written by the release's own signed push, never by the nightly), verify the cosign signature over that digest, read it back through your remote repository and record *that digest* in the config; jobs then pull a digest, so no tag — quiet or moved — changes what a run uses. The nightly's skip is hygiene: it keeps a quiet night from re-publishing an image nobody asked to change, and from moving a major tag that installations and the catalog check read. It is not what makes a job's base trustworthy.

## What is in it

- **Debian 13 (trixie) slim**, pinned by digest, with Debian's current security fixes at every rebuild.
- **The kit:** git, git-lfs, gh, openssh-client, curl, wget, httpie, dig, netcat, ripgrep (`rg`), `fd`, fzf, tree, `bat`, eza, jq, yq, sqlite3, build-essential, clang, make, cmake, pkg-config, autoconf, automake, libtool, the dev headers mise needs to build runtimes, shellcheck, vim (`vi`), nano, zip, unzip, xz, zstd, rsync, procps, gnupg, tzdata and UTF-8 locales.
- **Cloud:** the gcloud CLI and the Docker CLI (client only: a Cloud Run job has no Docker daemon).
- **[mise](https://mise.jdx.dev)**, the one way to install a language runtime.
- **Agent harnesses:** `claude` (Claude Code), `codex` (Codex CLI), `gemini` (Gemini CLI), `pi` (Pi), `qwen` (Qwen Code), `opencode` (OpenCode), `goose` (Goose) and `crush` (Crush). **Today only Claude Code is driven by Fugaro**; the others are installed and answer `--version`, and choosing one per run is a later feature. DeepSeek now publishes a harness too (`dsh`, `@deepseek-ai/dsh`); it is not bundled yet because its exact npm version isn't pinned ([design/base-image.md](design/base-image.md) section 6 has the full account).
- **`graphify`:** a Python CLI (PyPI `graphifyy`) that turns a folder into a queryable knowledge graph (HTML, GraphRAG JSON, `GRAPH_REPORT.md`); it runs on its own private Python in `/opt/fugaro/graphify`, never the base's system Python or a project-visible `pip`/`uv`. The base installs bare `graphifyy`, so only its code-only graph (AST/tree-sitter parsing, clustering, the report, HTML/JSON export, querying) works: LLM-backed semantic extraction needs a `graphifyy[extra]` this image does not install, so it is **not available** here and no LLM key is read or injected; a project that wants it installs its own `graphifyy[extra]` through mise/uv in its own layer. A Fugaro cloud run can use `graphify` directly for codebase questions; nothing in Fugaro installs or invokes it automatically. Every invocation except `install`, `uninstall`, `hook-check` and `hook-guard` (so including the presence check's `--version`, which is what CI actually runs) scans `$HOME` for per-platform skill directories and rewrites any stale `graphify` skill copy it finds there, and the wrapper does not set `HOME` itself, so whatever `HOME` the caller has is what gets scanned.
- **Fugaro's plumbing:** `fugaro`, `tini` (PID 1), `finalize-checkout`, `fugaro-services`, the `fugaro` user (uid 1000) and `/work/repo`.

No credential is in the image. Every key and token reaches a run at run time, through the environment and the runner; never bake one into your image either. What each tool reads, for whoever wires a harness later (names marked *unverified* come from each tool's documentation, not from running it):

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

Only Claude Code's row is wired today; the rest exist for whoever adds that harness later (above). The agent's own environment is an allowlist; none of these variables is added to it by this doc. The design's reasoning is in [design/base-image.md](design/base-image.md) section 6.

## Your runtimes: `mise.toml`

Put a `mise.toml` at the repository root:

```toml
[tools]
node = "24.19.0"
python = "3.12"
java = "temurin-21"
```

Pin exact versions where your repository pins them (`.nvmrc`, `go.mod`, a CI image). The image runs `mise install` in your checkout, as the unprivileged `fugaro` user, after cloning it, and the build fails if a tool is still missing. In a run, mise's cache is writable, so its commands do not warn about it. Changing `mise.toml` (or `.tool-versions`, `mise.lock`, `.config/mise/…`) always rebuilds the image. mise's shims are first on `PATH`, so every command (`commands.build`, `commands.test`, the agent's own) sees these versions. pnpm and Yarn come from corepack, through `package.json`'s `packageManager`. An existing `.tool-versions` works as it is. A `.nvmrc` or `.python-version` alone is not read: write it into `mise.toml`.

**mise's registry carries more than language runtimes, and a tool that lints or scans what your runs change belongs in the same list.** This repository's own `mise.toml` is the example: it pins `tflint` at the version CI lints with and `trivy` at the version `images/scan.sh` scans with, so a run that changes Terraform can do what `.claude/rules/terraform.md` asks — `tflint --chdir=deploy/terraform/gcp --init && tflint --chdir=deploy/terraform/gcp --recursive`, and `sh deploy/terraform/scan.sh`. That script runs the `trivy` on `PATH` when the image carries one, and `images/scan.sh`'s pinned Trivy image through plain docker otherwise (CI, a developer's machine), with the same flags and the same `.trivyignore` either way. `tflint`'s google plugin is fetched at image build time, under the `TFLINT_PLUGIN_DIR` the image sets, so the `--init` a run runs makes no GitHub API call and needs no token.

Without a `mise.toml`, list the tools in `fugaro.yaml`'s `image.tools` instead (never both):

```yaml
workflows:
  web:
    image:
      tools: { node: "24.19.0" }
    commands: { build: npm run build, test: npm test }
```

Without either, the image has no language runtime, and `fugaro validate` warns.

**The key-file layer.** The image installs your dependencies above the source: it checks out, at the commit it bakes, only what the install reads — your `mise.toml` and its lockfile, your lockfile and `package.json`, each workspace member's `package.json`, the registry configuration, and the install files no lockfile carries (`.yarnrc`, `.yarnrc.yml`, `.pnpmfile.cjs`, `.yarn/releases/`, `.yarn/plugins/`, `.yarn/patches/`). `image.key_files` adds the paths of your own that list cannot know — a patch a postinstall applies, a vendored toolchain — each checked out as the path it is, a directory as the files under it. A change to any of them changes the image, so the daily image check rebuilds it. **One entry contributes at most 256 files:** a directory holding more refuses the render, naming the entry, and a repository that large is one whose Dockerfile is its own (below).

## System packages and services

Add Debian packages with `image.apt`, and steps that need the checkout with `image.setup` (they run as `fugaro`, with `sudo` for those steps only). Services start in `commands.test`. A server whose tests need PostgreSQL with PostGIS and pgvector, Redis and the Firebase emulators:

```toml
# mise.toml
[tools]
java = "temurin-25"
node = "24"
"npm:firebase-tools" = "15.32.1"
```

```yaml
# fugaro.yaml
workflows:
  server:
    image:
      apt: [postgresql-17, postgresql-17-postgis-3, postgresql-17-pgvector, redis-server]
      setup: ["firebase setup:emulators:database && firebase setup:emulators:storage", "./gradlew --no-daemon testClasses"]
    commands:
      build: ./gradlew --no-daemon testClasses
      test: fugaro-services start && ./gradlew --no-daemon integrationTest
    resources: { cpu: 4, memory: 16Gi }
```

`fugaro-services start` runs them as `fugaro`, on loopback, under `/tmp` (which is memory on Cloud Run: count it in `resources`). Its settings are at the top of `fugaro-services`.

**An agent cannot `apt-get install` during a run.** It has no sudo, by design: a root-capable agent could rewrite the runner it shares the container with. Bake what your runs need into the image with `image.apt`, or install a standalone tool in user space mid-run with `mise use -g <tool>@<version>`.

**A command-line tool that is not a language runtime comes from `image.apt` too, and an artifact recipe is the case that needs it most.** A `mode: artifact` run renders a file with a tool the base does not carry — ffmpeg is the standard example, and neither `image.tools` (mise's registry, for runtimes) nor a mid-run `mise use -g` fits a render that must work the same on every run. Put it in `apt`, where a Debian package exists and is pinned by the image's own build:

```yaml
# fugaro.yaml
workflows:
  sizzle:
    image:
      apt: [ffmpeg]                     # the render's tool, baked in at build time
    commands:
      render: |
        set -eu
        mkdir -p out
        ffmpeg -y -framerate 1 -pattern_type glob -i 'inputs/*.png' \
          -c:v libx264 -pix_fmt yuv420p out/sizzle.mp4
    resources: { cpu: 4, memory: 8Gi }
```

Changing `image.apt` needs a rebuild, and the command for it is `fugaro image build` in the repository's checkout, in your own terminal window (it asks for the project's name typed there, and a coding-agent session never confirms one). **`fugaro image refresh` is not that rebuild:** refresh rebuilds a workflow only when the base image the workflow was built from is not the one the config pins now — it reads the build record's `base_ref` and never the image config (`refreshVerdict`, `internal/cli/image_refresh_rules.go`) — so on a repository already built from the current base it prints `current: built from …`, submits no Cloud Build, and the package never arrives. Until a rebuild from the changed config has run, a job still lacks the package and the render fails with `ffmpeg: not found`. The recipe side of this — `mode: artifact`, `steps`, `outputs` and the probe — is [artifact-runs.md](artifact-runs.md).

## When `image:` is not enough: a Dockerfile

Run `fugaro image render --workflow <name>`, save it as `.fugaro/<name>.Dockerfile`, point `dockerfile:` at it, and add your lines: system packages in a `USER root` block above the clone, steps that need the checkout after the `mise install` step. Keep everything else, which carries the security-critical parts (the credential handling, the sudo removal, `finalize-checkout`). A minimal one is exactly the render's output.

## Building it locally

`fugaro image build --local` builds and smoke-tests the image with your Docker. It defaults to `linux/amd64`, what Cloud Run runs; on Apple silicon, `--platform linux/arm64` is much faster, and the cloud build is then the amd64 test. With no `--base`, it pulls the published base matching your `fugaro` version — which, until `fugaro-base` is published (above), it won't find; pass `--base <image>` with your own build of `images/base/` meanwhile.

## Bring your own

- A Dockerfile (above) for anything the template can't express.
- A base of your own: `fugaro update --base-image base=<image>` points the installation at it. It must keep the base contract the smoke test checks: the `fugaro` user (uid 1000), `tini` as entrypoint, `/work` with its modes, `/etc/claude-code` empty and owned by `fugaro`, `fugaro`, `claude`, `git`, `gh`, `mise` and `finalize-checkout` on their paths, no passwordless sudo, and no unexpected setuid, setgid or file capability. Check it with `fugaro image build --local --base <image>` first.
- Another compute environment altogether is a backend ([backends.md](backends.md)).

**What a catalog image's `ENV` does in a run.** A job whose image is one of the project's `image_catalog` entries passes that image's own `ENV` to the run's agent — every name except the ones Fugaro reserves (so nothing an image sets can shadow the routing, the credentials or a declared secret) and except a secret name the job mounts that the run's own task does not declare. Everything Fugaro sets is applied last, so the run's routing, auth and declared secrets always win. `fugaro doctor --all` names, per job, the names that job sets itself that a run drops — an image's own `ENV` lives in the image and not in the job, so no read of a job can list it — and `--json` carries the same list. **What is not promised:** a per-repository image — a `dockerfile:` of your own or `checkout: clone`'s base image — passes nothing at all. A job deployed before the stamp existed passes nothing either, **unless its image sets `FUGARO_IMAGE_ENTRY` itself**: the run reads whatever its environment holds under that name, and a variable on the job is what overrides an image's, so redeploying that repository closes it: `fugaro update --repo .` in the checkout, or the repository stage of a plain `fugaro update` run in the same checkout. And a value in an image is **not a secret** and is not redacted, so never bake a credential into one (above).

## Coming from `go`, `web-node` or `java-services`

Those bases were removed in 0.7.0. [base-image-migration.md](base-image-migration.md) says what to write for each (the path every refusal names is `docs/base-image-migration.md`).
