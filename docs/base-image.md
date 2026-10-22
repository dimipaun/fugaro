# The Fugaro base image

Every workflow's image is built on one base, `ghcr.io/dimipaun/fugaro-base` (for `linux/amd64` and `linux/arm64`), plus the repository's checkout, its runtimes and its dependencies. The base carries what Fugaro and the agents need, and a generous tool kit; **your project declares its language runtimes in `mise.toml`**, and the image installs them. The design is [design/base-image.md](design/base-image.md).

**Published from 0.7.0.** `ghcr.io/dimipaun/fugaro-base` is built and published by the `images` workflow (`fugaro-base (amd64|arm64)` and `publish-base`) from the first release tag that carries it, 0.7.0; a development build or an earlier release has no released image to resolve, so `fugaro update --base base` and `fugaro image refresh` refuse with a clear error there; [Bring your own](#bring-your-own) below (your own build of `images/base/`) works with any build.

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

Without a `mise.toml`, list the tools in `fugaro.yaml`'s `image.tools` instead (never both):

```yaml
workflows:
  web:
    image:
      tools: { node: "24.19.0" }
    commands: { build: npm run build, test: npm test }
```

Without either, the image has no language runtime, and `fugaro validate` warns.

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

## Coming from `go`, `web-node` or `java-services`

Those bases were removed in 0.7.0. [base-image-migration.md](base-image-migration.md) says what to write for each (the path every refusal names is `docs/base-image-migration.md`).
