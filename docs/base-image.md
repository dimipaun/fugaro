# The Fugaro base image

Every workflow's image is built on one base, `ghcr.io/dimipaun/fugaro-base` (for `linux/amd64` and `linux/arm64`), plus the repository's checkout, its runtimes and its dependencies. The base carries what Fugaro and the agents need, and a generous tool kit; **your project declares its language runtimes in `mise.toml`**, and the image installs them. The design is [design/base-image.md](design/base-image.md).

## What is in it

- **Debian 13 (trixie) slim**, pinned by digest, with Debian's current security fixes at every rebuild.
- **The kit:** git, git-lfs, gh, openssh-client, curl, wget, httpie, dig, netcat, ripgrep (`rg`), `fd`, fzf, tree, `bat`, eza, jq, yq, sqlite3, build-essential, clang, make, cmake, pkg-config, autoconf, automake, libtool, the dev headers mise needs to build runtimes, shellcheck, vim (`vi`), nano, zip, unzip, xz, zstd, rsync, procps, gnupg, tzdata and UTF-8 locales.
- **Cloud:** the gcloud CLI and the Docker CLI (client only: a Cloud Run job has no Docker daemon).
- **[mise](https://mise.jdx.dev)**, the one way to install a language runtime.
- **Agent harnesses:** `claude` (Claude Code), `codex` (Codex CLI), `gemini` (Gemini CLI), `pi` (Pi), `qwen` (Qwen Code), `opencode` (OpenCode), `goose` (Goose) and `crush` (Crush). **Today only Claude Code is driven by Fugaro**; the others are installed and answer `--version`, and choosing one per run is a later feature.
- **Fugaro's plumbing:** `fugaro`, `tini` (PID 1), `finalize-checkout`, `fugaro-services`, the `fugaro` user (uid 1000) and `/work/repo`.

No credential is in the image. Every key and token reaches a run at run time, through the environment and the runner; never bake one into your image either. The tables of what each tool reads are in the design, section 6.

## Your runtimes: `mise.toml`

Put a `mise.toml` at the repository root:

```toml
[tools]
node = "24.19.0"
python = "3.12"
java = "temurin-21"
```

Pin exact versions where your repository pins them (`.nvmrc`, `go.mod`, a CI image). The image runs `mise install` in your checkout, as the unprivileged `fugaro` user, after cloning it, and the build fails if a tool is still missing. Changing `mise.toml` (or `.tool-versions`, `mise.lock`, `.config/mise/…`) always rebuilds the image. mise's shims are first on `PATH`, so every command (`commands.build`, `commands.test`, the agent's own) sees these versions. pnpm and Yarn come from corepack, through `package.json`'s `packageManager`. An existing `.tool-versions` works as it is. A `.nvmrc` or `.python-version` alone is not read: write it into `mise.toml`.

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

## When `image:` is not enough: a Dockerfile

Run `fugaro image render --workflow <name>`, save it as `.fugaro/<name>.Dockerfile`, point `dockerfile:` at it, and add your lines: system packages in a `USER root` block above the clone, steps that need the checkout after the `mise install` step. Keep everything else, which carries the security-critical parts (the credential handling, the sudo removal, `finalize-checkout`). A minimal one is exactly the render's output.

## Building it locally

`fugaro image build --local` builds and smoke-tests the image with your Docker. It defaults to `linux/amd64`, what Cloud Run runs; on Apple silicon, `--platform linux/arm64` is much faster, and the cloud build is then the amd64 test.

## Bring your own

- A Dockerfile (above) for anything the template can't express.
- A base of your own: `fugaro init --base-image base=<image>` points the installation at it. It must keep the base contract the smoke test checks: the `fugaro` user (uid 1000), `tini` as entrypoint, `/work` with its modes, `/etc/claude-code` empty and owned by `fugaro`, `fugaro`, `claude`, `git`, `gh`, `mise` and `finalize-checkout` on their paths, no passwordless sudo, and no unexpected setuid, setgid or file capability. Check it with `fugaro image build --local --base <image>` first.
- Another compute environment altogether is a backend ([backends.md](backends.md)).

## Coming from `go`, `web-node` or `java-services`

Those bases were removed in 0.7.0. [base-image-migration.md](base-image-migration.md) says what to write for each (the path every refusal names is `docs/base-image-migration.md`).
