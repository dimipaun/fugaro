# Base Image Consolidation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the three language bases (`go`, `web-node`, `java-services`) with one language-agnostic, multi-architecture base image, `ghcr.io/dimipaun/fugaro-base`. It carries:
- Fugaro's plumbing;
- a maximalist tool kit;
- gcloud and the Docker CLI;
- eight agent harnesses (only Claude Code is driven by the runner);
- mise.

Each project declares its runtimes in `mise.toml` (or `image.tools`), and the derived image installs them with `mise install` as the unprivileged `fugaro` user. The legacy kinds are removed outright in 0.7.0, with refusals that name the migration doc.

**Architecture:**
- **The image** (`images/base/`). A Debian 13 slim `Dockerfile` pinned by digest, with every critical component pinned by `ARG` version and per-architecture sha256. It also holds:
  - a `fetch.sh` that verifies each download;
  - mise's system config;
  - the harness lockfile;
  - the presence-check table `tools.tsv`, embedded in `fugaro` as `images.BaseTools`.

  CI builds it natively on amd64 and arm64, smoke-tests it (`fugaro image selftest` with `tools_only`), checks a size budget and scans every layer for secrets with Trivy. It publishes per-architecture tags and a multi-platform index. A monthly workflow opens a bump PR.
- **Config** (`internal/config`):
  - the kind `base` (`config.BaseKind`) and `Workflow.BaseKind()`;
  - `image.tools` and mise config detection (`MiseConfigFiles`);
  - uniform defaults;
  - in the cut, tombstone refusals for `base:`, `image.node` and `image.jdk`.
- **The derived template** (`images/derived/Dockerfile.tmpl`):
  - an `image.tools` step before the clone;
  - a `mise install` step after it, with a cache mount and the workflow's secrets;
  - a warm-up chosen by content.
- **Checks:**
  - `imagecheck` hashes the mise config files and `image.tools`;
  - the selftest gains the presence checks and a `mise-tools` check.
- **CLI and infra.** Every `w.Base` becomes `w.BaseKind()`. In the cut:
  - the local config refuses old `base_images` keys;
  - the published shared config drops them with a warning;
  - `RewriteCheckSpec` replaces a legacy spec wholesale.

**Tech Stack:**
- Go 1.27, Docker with BuildKit and buildx, POSIX sh and bash, GitHub Actions (with `ubuntu-24.04-arm` runners);
- Trivy, pinned by digest;
- mise 2026.10.4 on Debian 13 (trixie) slim.

Tests:
- fake `docker`, `curl` and `mise` scripts on `PATH` for the shell scripts and the selftest;
- the fake registries in `internal/mirror`;
- `gcpfake` for the check job;
- `-tags docker` tests, which run only in CI's `docker-tests` job.

**Spec:** [docs/design/base-image.md](../design/base-image.md), from the owner's `fugaro-base-image-spec.md` and rulings D1 to D3.

**Facts checked while writing (2026-10-08):**
- The pins below are the current releases, read from each vendor:
  - `debian:trixie-slim` index `sha256:a29215f6a35e51e22adffa17f89e9d2ef06214e64a2bad10d765c46aea49f11f`;
  - mise v2026.10.4, Codex rust-v0.161.0, yq v4.54.1, OpenCode v1.18.35, Goose v1.53.0, Crush v0.97.1 (GitHub's asset digests);
  - gcloud 588.0.0 and the Docker CLI 29.8.2 (sha256 computed here from the downloads);
  - Node 24.21.0 (nodejs.org SHASUMS256.txt);
  - Gemini CLI 0.63.0, Pi 0.73.1, Qwen Code 0.25.0 (npm).
- The mise setting names (`trusted_config_paths`, `idiomatic_version_file_enable_tools`, `auto_install`, `not_found_auto_install`, `yes`, `node.corepack`, `offline`) and `mise ls --missing --json` were read from mise's `settings.toml` and `src/cli/ls.rs` at v2026.10.4.
- The Debian 13 package names were checked on packages.debian.org, including `postgresql-17-postgis-3`, `postgresql-17-pgvector`, `redis-server` (8.0.2), `eza`, `fd-find`, `bat`, `bind9-dnsutils` and `vim-tiny`.
- DeepSeek publishes no coding-agent CLI. npm's `deepseek-cli` belongs to a third party and has not been touched since 2025-01.
- The mirror already reads an amd64+arm64 index, compares `--expect-digest` with the index digest, and copies only the amd64 child: `TestMirrorCopiesTheAmd64ImageDigestVerified` publishes exactly such an index.
- `docs/design/layered-config.md` was not on `origin` when this was written.

## Decisions (veto any before execution starts)

The owner's rulings (binding):

- **D1. No sudo for the agent.** The spec's open decision 1 (passwordless sudo) is **rejected**, and today's hardening stays:
  - the agent runs as the unprivileged `fugaro` user;
  - `sudo` is installed with no rules, and a derived build grants it to `image.setup` only, then removes the rule and strips the setuid bits of sudo and su;
  - there is no setuid or setgid binary beyond the allowlists and no file capability;
  - the selftest keeps asserting all of it.

  Runtimes come from `mise install` as `fugaro` into its home. System packages and services are baked at build time, as root, before the hardening step: through `image.apt` and `image.setup`, or in a project Dockerfile above its `USER fugaro`. An agent cannot `apt-get install` mid-run. That is a documented safety limit; the workarounds are to bake the package in, or `mise use -g <tool>` in user space.
- **D2. A hard cut at 0.7.0.** `web-node`, `java-services` and `go` are removed everywhere:
  - `config.Bases`, the schema, `validate`;
  - `init --base`, `--expect-digest`, `--replace-image` and `--base-image` kinds;
  - `base_images` keys, image refresh and the check job;
  - the docs, `build-base.sh` usage, the CI jobs `base (go|java-services|web-node)`, the images workflow, the release footer and `verify-public`.

  There are no deprecated aliases and no overlap period. The strict parsers refuse the old values with an error naming `docs/base-image-migration.md`.
- **D3. Pin and bump.** The Debian base is pinned by index digest, and mise, the harnesses, gh, gcloud, the Docker CLI, yq and the harness Node by `ARG` version and per-architecture sha256. Debian packages float (`apt-get upgrade` in the first `RUN`). A monthly scheduled workflow opens a bump PR and never merges it. CI scans every layer and the image config for secrets with Trivy.

Decisions this plan makes:

- **B1. Debian 13 (trixie) slim** replaces Ubuntu 24.04. Debian 13's setuid set equals the selftest's current allowlist, and its setgid set is a subset. Playwright's `--with-deps` supports Debian 12 and 13; the Belong frontend checklist (Task 22) proves it. Veto alternative: `ubuntu:24.04` pinned by digest (the spec says Debian).
- **B2. The kind is named `base`.** The image is `fugaro-base`, and the installation's copy `<host>/fugaro-base/fugaro-base:<v>`. The kind machinery stays: `base_images: {base: …}`, `--expect-digest base=…`, `--replace-image base`, `--base-image base=…`. `fugaro init --base base` reads oddly, but it keeps `init`, image refresh, the mirror and the check job generic. Veto alternative: a single `base_image:` key and boolean flags (about four more tasks).
- **B3. `base:` leaves `fugaro.yaml`** in the cut. Any value is refused with the migration message (a tombstone field). Veto alternative: keep `base:` optional with the single value `base`.
- **B4. The user stays `fugaro` (uid 1000)**, not the spec's `agent`. `LintDockerfile`, the selftest, Cloud Build's `prep` and every project Dockerfile depend on it.
- **B5. mise system settings** go in `/etc/mise/config.toml`, a file, because `MISE_*` variables are not on the agent's environment allowlist:
  - `trusted_config_paths = ["/work/repo"]`;
  - `idiomatic_version_file_enable_tools = []`;
  - `auto_install = false` and `not_found_auto_install = false`;
  - `yes = true`;
  - `node.corepack = true`.

  Shims come first on `PATH`.
- **B6. `image.tools`** (a map of mise tool to version) is the in-`fugaro.yaml` alternative to a repository mise config. Using both is refused by `fugaro validate`. It is rendered into `~/.config/mise/config.toml` before the clone.
- **B7. `mise install` runs after the clone,** in `/work/repo`, as `fugaro`. It gets a BuildKit cache mount on `~/.cache/mise` and the workflow's secrets (the warm-up's mounts), and ends with `mise ls --current --missing` empty. A saved render therefore never freezes an old `mise.toml`. Only the root config is installed; subdirectories use `image.setup`.
- **B8. Warm-ups and the default cache follow content, not base:** a `package.json` with a supported lockfile gets the Node warm-up. `image.skip_build_scripts` is valid when a Node warm-up exists.
- **B9. A changed mise config rebuilds always.** The image config hash covers the root mise files' blob IDs (including `mise.lock`) and `image.tools`.
- **B10. Uniform defaults:** `cpu: 4`, `memory: 8Gi`, reports `["**/junit*.xml", "**/build/test-results/**/*.xml"]`. A former `java-services` workflow sets `memory: 16Gi` itself (the migration doc says so).
- **B11. `fugaro-services` moves into the base.** It now discovers PostgreSQL's newest `/usr/lib/postgresql/<major>/bin`, `redis-server` and `firebase` on `PATH`, and names the package to install when a selected service is missing. The services themselves come from the project: `image.apt` on Debian 13 (`postgresql-17`, `postgresql-17-postgis-3`, `postgresql-17-pgvector`, `redis-server`); firebase-tools as a mise `npm:` tool; the emulator jars from `image.setup`. The base adds `policy-rc.d` (exit 101) and `create_main_cluster = false`.
- **B12. The harnesses:**
  - critical: Claude Code;
  - bundled but unsupported by the runner: Codex CLI, Gemini CLI, Pi, Qwen Code, OpenCode, Goose, Crush;
  - excluded: DeepSeek (no official CLI), GitHub Copilot CLI and Amp (proprietary licences need reading before redistribution), Cursor CLI (only a `curl | bash` installer), Aider and Kimi (need a Python runtime).

  Crush's FSL-1.1-MIT licence is flagged.
- **B13. The Node harnesses** run on a private Node 24.21.0 in `/opt/fugaro/node`, not on `PATH`, through wrappers. They are installed by `npm ci --ignore-scripts` from a committed lockfile into root-owned `/opt/fugaro/harnesses`.
- **B14. The Docker CLI client** is included (spec), though inert on Cloud Run (no daemon, no socket). gcloud is included: the metadata server already gives the agent the job's account, so gcloud adds convenience, not capability. Veto alternative for the Docker CLI: drop it (one deleted block in Task 2, one row in `tools.tsv`).
- **B15. Multi-arch:**
  - native runners (`ubuntu-latest`, `ubuntu-24.04-arm`), no QEMU;
  - the publish job pushes `:<v>-amd64` and `:<v>-arm64`, then `imagetools create` makes the index `:<v>` (and `:<major>`);
  - `history` stays amd64-only;
  - the installation's copy stays amd64-only (the mirror, unchanged);
  - `fugaro image build --local` keeps `--platform linux/amd64` as its default, and `--platform linux/arm64` now works.
- **B16. A size budget:** `images/size-budget.sh` fails above 4000 MB uncompressed and warns above 3500 MB, per architecture. The publish job writes compressed sizes to the run summary. Cold start is measured live before the release (Task 22). The fallback if it is bad is to move gcloud to `mise use -g` on demand.
- **B17. Trivy's secret scanner** runs over every layer and the image config, on every PR build of the base, both architectures, and fails on a finding. Allowances live in `images/trivy-secret.yaml`, each with a reason.
- **B18. The monthly bump** runs on the 3rd of each month and on dispatch. It builds and smoke-tests both architectures itself, then opens a PR with `GITHUB_TOKEN`, which doesn't trigger CI. Its body says to close and reopen the PR. No auto-merge.
- **B19. Legacy keys in the cut:**
  - a local config `base_images` key other than `base` is refused, with the one-line fix;
  - the published shared config's legacy keys are dropped with a warning;
  - `fugaro image refresh` rewrites a check job whose spec names legacy kinds to `{"base": <ref>}`.
- **B20. The base bakes no git credential helper.** The runner configures git and gh at run time, and `finalize-checkout` refuses a baked helper. This is how the spec's "credential helper that consumes runtime-injected credentials" is met.
- **B21. Claude Code stays in `~/.local/bin/claude`,** installed as today.
- **B22. Release 0.7.0, after layered config Phase 1.** Groups 1 to 4 land on `main` in order, and **no release is cut from `main` between Group 2's merge and Group 4's merge** (the release freeze), so no published version accepts both. Group 1 alone is additive and may ship in a 0.6.x release (it publishes `fugaro-base`, unused). Veto alternative: an integration branch for Groups 2 to 4.

## Global Constraints

Every task's requirements include these.

From the design:
- The agent never has passwordless sudo, a setuid or setgid binary outside the allowlists, a file capability, a Docker socket or a baked credential, in the base or in any derived image. The selftest enforces each.
- No secret in any layer, build argument or baked config file. Every `ARG` of `images/base/Dockerfile` is a version, a checksum or a Fugaro build fact.
- Every download in `images/base/Dockerfile` goes through `fetch.sh` (https, sha256 checked) or the Node release keyring. There is no `curl | sh` and no unpinned `latest`. The npm harnesses come only from `package-lock.json` with `npm ci --ignore-scripts`.
- Every pin is one `ARG NAME=value` line: `bump.sh` and the tests read and write nothing else.
- Only Claude Code is driven by the runner. No task adds runner support for another harness.
- The installation's copy of the base stays the amd64 image, digest-verified by the mirror. The index digest is what `--expect-digest` pins.
- After Group 4, no file outside `docs/plans/`, `docs/releases/v0.[1-6]*`, `docs/design/` history sections and `docs/base-image-migration.md` names `web-node`, `java-services` or `fugaro-go` as a current thing. Task 20 pins this with a test.

Project rules:
- Dogfood runs work in git worktrees (`.worktrees/<branch>`), never in the main checkout.
- Every CI check is read before merge, each job's log and not only the summary: `test`, `rules`, `terraform`, `images` (`base (amd64)`, `base (arm64)`, `history`) and `docker-tests`.
- Docs must match behaviour. The docs tests in Tasks 15 and 16, the skills lint (`plugin/*_test.go`) and the refresh docs test stay green.
- A `fugaro.yaml` key needs three edits: the Go struct, `Validate`, and `schemas/fugaro.schema.json`, plus a `testdata/config` corpus file, which `TestFugaroSchemaCorpus` holds to both. `image.tools` (Task 9) and the tombstones (Task 17) follow the rule.
- Releases go through `/new-release` with `docs/releases/vX.Y.Z.md` merged first.
- `internal/cli` and `internal/runner` are slow (about 15 and 19 minutes in full). Each task runs only its focused tests (`-run`) **in the foreground**. Each PR group ends with one full `go test ./...` (Tasks 8, 14, 16, 21). Focused `internal/runner` tests run with `-race`, as CI does.
- No live cloud in any test: fakes only. Never touch EdgeWeb or EdgeServer, and handle no real secrets. The Belong checks are the owner's (Task 22's checklist), sandbox first.
- **What a Fugaro run cannot verify.** A Cloud Run job has no Docker daemon. Tasks 2, 6 (the workflow's real run), 7, 8 (the real bump) and the Docker tests of Tasks 12 and 20 are written and unit-tested in a run, but their builds are verified only by CI's `images` and `docker-tests` jobs and by the owner. The task says so where it applies, and the PR body lists them.
- **Layered config.** This plan starts after layered config Phase 1 merges. If Phase 1 moved `base_images` (into an environment or profile layer), Tasks 13 and 19 apply to its new location with the same shape (`{base: <ref>}`); the task's tests follow the moved type. Image catalog (its Phase 2) starts after Group 4.

## Review Focus

The five failure modes most likely to hit a user, each pinned by a test:

1. **A derived image builds but the project's runtime is missing or the wrong version at run time.** Causes: `mise install` skipped, mise's precedence surprising, a shim not on `PATH` for `sh -c` or the agent, a `mise.toml` change not triggering a rebuild. Expected:
   - the template runs `mise install` whenever the checkout has a mise config or `image.tools` is set, and fails the build if anything is still missing;
   - the selftest's `mise-tools` check fails an image with a missing tool;
   - the hash changes when any root mise file or `image.tools` changes.

   Pinned by:
   - `TestRenderMiseInstallWhenTheCheckoutHasAConfig`, `TestRenderNoMiseStepWithoutAConfig` and `TestRenderImageToolsBeforeTheClone` (Task 10);
   - `TestSelftestMiseToolsMissing` (Task 12);
   - `TestImageConfigHashCoversMiseFiles` (Task 11);
   - `TestValidateRefusesToolsWithARepositoryMiseConfig` (Task 9);
   - the Docker test `TestSampleProjectNodePythonOnTheBase` (Task 12).
2. **The base loses hardening in the move to Debian** (sudo usable, a new setuid binary, a baked credential). Expected:
   - the base's root scan matches the allowlists with `ssh-keysign` and `ssh-agent` stripped;
   - `sudo -n true` fails;
   - the selftest refuses each new harness credential file in `HOME`;
   - a derived image still strips sudo and su.

   Pinned by:
   - `TestBaseImageHardening` (Task 7, Docker);
   - `TestSelftestRefusesHarnessCredentialFiles` (Task 12);
   - `TestBaseDockerfileStripsKeysignAndPinsEverything` (Task 2);
   - the existing `TestCheckSetuid*` tests, unchanged.
3. **The hard cut locks a user out with no way forward, or lets an old value through.** Expected:
   - every legacy value (`base:`, `image.node`, `image.jdk`, a legacy `base_images` key, a legacy `FROM`) is refused with a message naming `docs/base-image-migration.md` and the fix;
   - the published shared config's legacy keys are dropped with a warning, not refused;
   - `fugaro image refresh` rewrites a legacy check job spec to `{"base": …}`.

   Pinned by:
   - `TestLegacyBaseRefusedWithTheMigrationDoc`, `TestLegacyImageNodeAndJDKRefused` and `TestLintNamesTheMigrationDocForALegacyFrom` (Task 17);
   - `TestLocalConfigRefusesLegacyBaseImageKeys`, `TestSharedConfigDropsLegacyBaseImageKeys` and `TestRewriteCheckSpecReplacesLegacyKinds` (Task 19);
   - `TestMigrationDocCoversEveryKind` (Task 15).
4. **The multi-arch publish breaks the copy or the local build.** Expected:
   - the published index holds linux/amd64 and linux/arm64, and `verify-public` fails without either;
   - the mirror copies only the amd64 child and pins the index digest;
   - `image build --local --platform linux/arm64` passes the platform to both build and selftest.

   Pinned by:
   - `TestVerifyPublicRequiresArm64ForTheBase` (Task 6);
   - `TestImagesWorkflowPublishesAMultiArchBase` (Task 6);
   - `TestMirrorLeavesTheArm64ImageBehind` (Task 13);
   - `TestBuildLocalPassesThePlatformToSelftest` (Task 13).
5. **The base silently loses a tool or grows without anyone deciding.** Expected:
   - every row of `tools.tsv` answers in the base's CI smoke, with `--network none` and an empty `HOME`;
   - a pinned component reports its pinned version;
   - the image stays under the size budget;
   - the Trivy secret scan passes.

   Pinned by:
   - `TestToolsTableParses` and `TestSelftestToolsOnly` (Task 1);
   - `TestSmokeBaseChecksEveryPin` and `TestSmokeBaseFailsWhenAToolIsMissing` (Task 4);
   - `TestSizeBudget` (Task 4);
   - `TestScanSecretsFailsOnAFinding` (Task 5);
   - CI's `base (amd64)` and `base (arm64)` jobs.

---

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `images/base/tools.tsv` (new), `images/images.go`, `internal/image/tools.go` (new), `internal/image/tools_test.go` (new), `internal/image/selftest.go` | the presence table, `images.BaseTools`, `ParseTools`, `SelftestSpec.Tools`/`ToolsOnly` | 1 |
| `images/base/Dockerfile`, `fetch.sh`, `mise-system.toml`, `harnesses/package.json`, `harnesses/package-lock.json` (all new), `images/base/install-node.sh` (moved from `images/web-node/`), `images/web-node/Dockerfile`, `images/java-services/Dockerfile`, `images/base_test.go` (new) | the base image | 2 |
| `images/common/fugaro-services` (moved from `images/java-services/`), `images/java-services/Dockerfile`, `images/services_test.go` (renamed from `java_services_test.go`'s services half) | services discovered, not assumed | 3 |
| `images/smoke.sh`, `images/size-budget.sh` (new), `images/smoke_test.go`, `images/size_budget_test.go` (new) | the base smoke and the size budget | 4 |
| `images/scan.sh`, `images/trivy-secret.yaml` (new), `images/scan_test.go` (new) | the secret scan | 5 |
| `.github/workflows/images.yml`, `images/verify-public.sh`, `.goreleaser.yaml`, `images/verify_public_test.go`, `images/base_workflow_test.go` (new) | per-arch build, index publish, verify | 6 |
| `images/base_docker_test.go`, `internal/testutil/docker.go`, `.github/workflows/ci.yml` | Docker tests: the base's hardening and presence | 7 |
| `.github/workflows/base-bump.yml` (new), `images/base/bump.sh` (new), `images/bump_test.go` (new), `.github/dependabot.yml` | the monthly bump | 8 |
| `internal/config/config.go`, `defaults.go`, `image.go`, `validate.go`, `mise.go` (new), `mise_test.go` (new), `image_test.go`, `config_test.go`, `example.yaml`, `schemas/fugaro.schema.json`, `testdata/config/valid/full.yaml`, `testdata/config/valid/base-tools.yaml` (new), `internal/cli/validate.go` | `BaseKind`, `image.tools`, mise detection, rules | 9 |
| `images/derived/Dockerfile.tmpl`, `internal/image/render.go`, `render_test.go`, `internal/config/nodepm.go` | the mise step, content-based warm-up | 10 |
| `internal/imagecheck/record.go`, `record_test.go` | the hash covers mise files and tools | 11 |
| `internal/image/selftest.go`, `selftest_test.go`, `local.go`, `docker_test.go` | `mise-tools`, credential files, critical presence; the mise sample project (Docker) | 12 |
| `internal/cli/*.go` (the `w.Base` callers), `internal/infra/spec.go`, `internal/runner/lockcache.go`, `internal/mirror/mirror_test.go`, `internal/image/local_test.go`, `internal/cli/init_images_test.go` | `BaseKind()` everywhere, `base` flows | 13 |
| `mise.toml` (new, repository root), `images/repo_pins_test.go` (new) | Fugaro's own runtimes | 14 |
| `docs/base-image.md` (new), `docs/base-image-migration.md` (new), `docs/design/v1.md`, `docs/gcp-setup.md`, `docs/release.md`, `docs/dogfooding.md`, `docs/backends.md`, `README.md`, `internal/cli/docs_base_test.go` (new) | docs | 15 |
| `plugin/skills/setup/SKILL.md`, `reference/discovery.md`, `reference/services-and-images.md`, `reference/validation.md`, `reference/decisions.md`, `plugin/testdata/repos/*`, `plugin/setup_fixtures_test.go`, `plugin/setup_skill_test.go` | the setup skill | 16 |
| `internal/config/*` (tombstones), `schemas/fugaro.schema.json`, `testdata/config/**`, `internal/config/dockerfile.go` | the cut: config | 17 |
| `internal/image/render.go`, `selftest.go`, `local.go`, `internal/config/nodepm.go`, `internal/imagecheck/record.go`, `images/derived/Dockerfile.tmpl` | the cut: render, selftest, hash | 18 |
| `internal/localcfg/localcfg.go`, `internal/cli/sharedcfg.go`, `internal/infra/checkjob.go`, `internal/cli/init.go`, `init_images.go`, `doctor.go`, their tests, `deploy/sandbox/fugaro.yaml`, `testdata/fixture-repo/fugaro.yaml` | the cut: CLI, local and shared config, check job, doctor | 19 |
| `images/go/`, `images/web-node/`, `images/java-services/` (deleted), `images/*_test.go`, `internal/image/docker_test.go`, `.github/workflows/images.yml`, `.goreleaser.yaml`, `images/verify-public.sh`, `.github/dependabot.yml`, `docs/release.md`, `docs/gcp-live-checklist.md`, `images/legacy_names_test.go` (new) | the cut: images, CI, release | 20 |
| none | full suite, PR | 21 |
| `docs/releases/v0.7.0.md`, `fugaro.yaml` | release, Fugaro's switch, owner checklist | 22 |

## PR groups

- **Group 1, branch `base-image`: Tasks 1 to 8.** The image and its CI. It is additive: nothing uses `fugaro-base` yet. A Fugaro run can do Tasks 1, 3, 4 and 5 entirely; Tasks 2, 6, 7 and 8 are written and statically tested in a run, and CI builds the images.
- **Group 2, branch `base-kind`: Tasks 9 to 14.** Go and config. It is additive: `base` is accepted next to the legacy kinds. **The release freeze starts at its merge.**
- **Group 3, branch `base-docs`: Tasks 15 and 16.** Docs and the setup skill.
- **Group 4, branch `base-cut`: Tasks 17 to 21.** The legacy kinds removed. **The freeze ends with Task 22's release.**
- **Task 22** runs after Group 4's merge: release 0.7.0.

Inside Group 1, Tasks 1, 3 and 5 are independent; 2 needs 1 (the table it copies); 4 needs 1 and 2; 6 needs 4 and 5; 7 needs 2; 8 needs 2. Inside Group 2, 9 comes first; 10, 11 and 12 need 9; 13 needs 9 to 12; 14 is independent.

---
## Group 1: the base image and its CI (branch `base-image`)

### Task 1: The presence table and the selftest's presence checks

**Files:**
- Create: `images/base/tools.tsv`, `internal/image/tools.go`, `internal/image/tools_test.go`
- Modify: `images/images.go`, `internal/image/selftest.go`

**Interfaces:**
- Consumes: `images` (embed), `Selftest`'s `add`, `Report`.
- Produces:
  ```go
  // images/images.go
  //go:embed base/tools.tsv
  var BaseTools string

  // internal/image/tools.go
  type Tool struct {
  	Name   string   // a command on PATH, or an absolute path
  	Argv   []string // must exit 0; nil means presence only
  	Arches []string // GOARCH values; nil means every architecture
  	Tier   string   // critical | kit | harness
  }
  func ParseTools(table string) ([]Tool, error)
  var baseTools = images.BaseTools // tests replace it
  func checkTools(ctx context.Context, tier string, add func(string, bool, string, ...any))

  // SelftestSpec gains
  Tools     string `json:"tools,omitempty"`      // "critical" | "all" | "" (none)
  ToolsOnly bool   `json:"tools_only,omitempty"` // only the presence checks (the base itself)
  ```

- [ ] **Step 1: Write the table**

`images/base/tools.tsv` (fields separated by one tab):

```
# The Fugaro base image's presence checks (design base-image.md section 6),
# run by fugaro image selftest: "critical" rows in every derived image's
# smoke, every row in the base's CI smoke, each with HOME set to an empty
# directory and, in the smokes, no network. Fields, tab-separated: the
# command (on PATH, or an absolute path); the argv that must exit 0, or "-"
# for presence only; the architectures it exists on, "all" or a comma list
# of amd64 and arm64; the tier: critical, kit or harness.
fugaro	fugaro version	all	critical
claude	claude --version	all	critical
git	git --version	all	critical
gh	gh --version	all	critical
mise	mise --version	all	critical
tini	tini --version	all	critical
/usr/local/lib/fugaro/finalize-checkout	-	all	critical
fugaro-services	-	all	kit
git-lfs	git-lfs version	all	kit
ssh	ssh -V	all	kit
curl	curl --version	all	kit
wget	wget --version	all	kit
http	http --version	all	kit
dig	dig -v	all	kit
nc	-	all	kit
rg	rg --version	all	kit
fd	fd --version	all	kit
fzf	fzf --version	all	kit
tree	tree --version	all	kit
bat	bat --version	all	kit
eza	eza --version	all	kit
jq	jq --version	all	kit
yq	yq --version	all	kit
sqlite3	sqlite3 --version	all	kit
make	make --version	all	kit
cmake	cmake --version	all	kit
gcc	gcc --version	all	kit
clang	clang --version	all	kit
pkg-config	pkg-config --version	all	kit
autoconf	autoconf --version	all	kit
automake	automake --version	all	kit
libtoolize	libtoolize --version	all	kit
shellcheck	shellcheck --version	all	kit
bash	bash --version	all	kit
diff	diff --version	all	kit
patch	patch --version	all	kit
less	less --version	all	kit
vi	-	all	kit
nano	nano --version	all	kit
zip	zip -v	all	kit
unzip	-	all	kit
xz	xz --version	all	kit
zstd	zstd --version	all	kit
tar	tar --version	all	kit
rsync	rsync --version	all	kit
ps	ps --version	all	kit
gpg	gpg --version	all	kit
python3	python3 --version	all	kit
gcloud	gcloud --version	all	kit
docker	docker --version	all	kit
codex	codex --version	all	harness
gemini	gemini --version	all	harness
pi	pi --version	all	harness
qwen	qwen --version	all	harness
opencode	opencode --version	all	harness
goose	goose --version	all	harness
crush	crush --version	all	harness
```

- [ ] **Step 2: Write the failing tests**

`internal/image/tools_test.go`:

```go
package image

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/images"
	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestToolsTableParses(t *testing.T) {
	tools, err := ParseTools(images.BaseTools)
	if err != nil {
		t.Fatal(err)
	}
	tier := map[string]string{}
	for _, tool := range tools {
		tier[tool.Name] = tool.Tier
	}
	for _, name := range []string{"fugaro", "claude", "git", "gh", "mise", "tini", "/usr/local/lib/fugaro/finalize-checkout"} {
		if tier[name] != "critical" {
			t.Errorf("%s is %q, want critical", name, tier[name])
		}
	}
	// The harnesses of design section 6, present but unsupported by the runner.
	for _, name := range []string{"codex", "gemini", "pi", "qwen", "opencode", "goose", "crush"} {
		if tier[name] != "harness" {
			t.Errorf("%s is %q, want harness", name, tier[name])
		}
	}
	for _, name := range []string{"gcloud", "docker", "rg", "fd", "bat", "yq", "jq", "fugaro-services"} {
		if tier[name] != "kit" {
			t.Errorf("%s is %q, want kit", name, tier[name])
		}
	}
}

func TestParseToolsRefuses(t *testing.T) {
	for name, table := range map[string]string{
		"three fields":  "git\tgit --version\tall\n",
		"bad arch":      "git\tgit --version\tamd64,s390x\tkit\n",
		"bad tier":      "git\tgit --version\tall\toptional\n",
		"repeated name": "git\t-\tall\tkit\ngit\t-\tall\tkit\n",
		"empty argv":    "git\t \tall\tkit\n",
	} {
		if _, err := ParseTools(table); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

// presenceTable is a table for the selftest tests: one tool that answers,
// one missing, one that fails, and one on the other architecture only.
func presenceTable(t *testing.T) {
	t.Helper()
	other := map[string]string{"amd64": "arm64", "arm64": "amd64"}[runtime.GOARCH]
	old := baseTools
	baseTools = "ok\tok --version\tall\tcritical\n" +
		"absent\t-\tall\tkit\n" +
		"fails\tfails --version\tall\tharness\n" +
		"elsewhere\t-\t" + other + "\tkit\n"
	t.Cleanup(func() { baseTools = old })
	bin := t.TempDir()
	testutil.WriteFiles(t, bin, map[string]string{
		// ok proves the check's HOME is an empty directory of its own.
		"ok":        "#!/bin/sh\n[ -d \"$HOME\" ] && [ -z \"$(ls -A \"$HOME\")\" ] && [ \"$HOME\" != \"$REAL_HOME\" ] || { echo \"HOME=$HOME is not empty\"; exit 1; }\necho 'ok 1.0'\n",
		"fails":     "#!/bin/sh\necho 'needs a login' >&2\nexit 3\n",
		"elsewhere": "#!/bin/sh\nexit 0\n",
	})
	for _, f := range []string{"ok", "fails", "elsewhere"} {
		if err := os.Chmod(filepath.Join(bin, f), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("REAL_HOME", os.Getenv("HOME"))
}

func TestSelftestToolsOnly(t *testing.T) {
	presenceTable(t)
	var log bytes.Buffer
	r := Selftest(context.Background(), SelftestSpec{ToolsOnly: true, Tools: "all"}, &log)
	got := map[string]bool{}
	for _, c := range r.Checks {
		got[c.Name] = c.OK
	}
	want := map[string]bool{"tool:ok": true, "tool:absent": false, "tool:fails": false}
	for name, ok := range want {
		if v, present := got[name]; !present || v != ok {
			t.Errorf("%s = %v (present %v), want %v; report %+v", name, v, present, ok, r)
		}
	}
	if _, ok := got["tool:elsewhere"]; ok {
		t.Error("a tool of the other architecture was checked")
	}
	if r.Passed {
		t.Error("passed with a missing and a failing tool")
	}
	if names := slices.Collect(func(yield func(string) bool) {
		for _, c := range r.Checks {
			if !yield(c.Name) {
				return
			}
		}
	}); slices.Contains(names, "user") || slices.Contains(names, "checkout") {
		t.Errorf("tools_only ran other checks: %v", names)
	}
	c, _ := checkNamed(r, "tool:fails")
	if !strings.Contains(c.Detail, "needs a login") {
		t.Errorf("the failure detail lacks the tool's output: %q", c.Detail)
	}
}

func TestSelftestToolsCriticalOnly(t *testing.T) {
	presenceTable(t)
	r := Selftest(context.Background(), SelftestSpec{ToolsOnly: true, Tools: "critical"}, &bytes.Buffer{})
	if !r.Passed || len(r.Checks) != 1 || r.Checks[0].Name != "tool:ok" {
		t.Fatalf("critical: %+v", r)
	}
}
```

- [ ] **Step 3: Run the tests to see them fail**

Run: `go test ./internal/image/ -run 'TestToolsTableParses|TestParseToolsRefuses|TestSelftestTools' -count=1`
Expected: build failure, `undefined: ParseTools` and `unknown field ToolsOnly`.

- [ ] **Step 4: Implement**

`images/images.go`, after `DerivedTemplate`:

```go
// BaseTools is images/base/tools.tsv, the base image's presence checks,
// which fugaro image selftest runs (internal/image.ParseTools).
//
//go:embed base/tools.tsv
var BaseTools string
```

`internal/image/tools.go`:

```go
package image

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/images"
)

// Tool is one row of images/base/tools.tsv: a command the base image must
// have (design base-image.md section 6).
type Tool struct {
	Name   string   // a command on PATH, or an absolute path
	Argv   []string // run, and must exit 0; nil means presence only
	Arches []string // GOARCH values it exists on; nil means every one
	Tier   string   // critical | kit | harness
}

var toolTiers = []string{"critical", "kit", "harness"}

// baseTools is the table checkTools reads; tests replace it.
var baseTools = images.BaseTools

// toolTimeout bounds one presence check; a version command is instant.
const toolTimeout = 30 * time.Second

// ParseTools reads tools.tsv: one tool per line, four tab-separated fields
// (name; argv, or "-" for presence only; "all" or a comma list of amd64 and
// arm64; tier). Blank lines and # comments are skipped.
func ParseTools(table string) ([]Tool, error) {
	var tools []Tool
	seen := map[string]bool{}
	for i, line := range strings.Split(table, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 4 {
			return nil, fmt.Errorf("tools.tsv line %d: want 4 tab-separated fields, have %d", i+1, len(f))
		}
		t := Tool{Name: f[0], Tier: f[3]}
		if t.Name == "" || seen[t.Name] {
			return nil, fmt.Errorf("tools.tsv line %d: the name %q is empty or repeated", i+1, t.Name)
		}
		seen[t.Name] = true
		if f[1] != "-" {
			if t.Argv = strings.Fields(f[1]); len(t.Argv) == 0 {
				return nil, fmt.Errorf("tools.tsv line %d: an empty argv; write - for presence only", i+1)
			}
		}
		if f[2] != "all" {
			for _, a := range strings.Split(f[2], ",") {
				if a != "amd64" && a != "arm64" {
					return nil, fmt.Errorf("tools.tsv line %d: architecture %q is not amd64 or arm64", i+1, a)
				}
				t.Arches = append(t.Arches, a)
			}
		}
		if !slices.Contains(toolTiers, t.Tier) {
			return nil, fmt.Errorf("tools.tsv line %d: tier %q is not one of %s", i+1, t.Tier, strings.Join(toolTiers, ", "))
		}
		tools = append(tools, t)
	}
	return tools, nil
}

// checkTools adds a "tool:<name>" check for each tool of tier ("critical",
// or "all" for every tier) that exists on this architecture: the command is
// found, and its argv exits 0 within toolTimeout with HOME set to a fresh
// empty directory, mise offline and no colour. That is presence only: no
// credentials and no provider is ever involved.
func checkTools(ctx context.Context, tier string, add func(string, bool, string, ...any)) {
	tools, err := ParseTools(baseTools)
	if err != nil {
		add("tools", false, "%v", err)
		return
	}
	home, err := os.MkdirTemp("", "fugaro-tools-home-")
	if err != nil {
		add("tools", false, "creating an empty HOME: %v", err)
		return
	}
	defer os.RemoveAll(home)
	for _, t := range tools {
		if (t.Arches != nil && !slices.Contains(t.Arches, runtime.GOARCH)) || (tier == "critical" && t.Tier != "critical") {
			continue
		}
		name := "tool:" + t.Name
		path := t.Name
		if filepath.IsAbs(path) {
			if fi, err := os.Stat(path); err != nil || fi.Mode()&0o111 == 0 {
				add(name, false, "%s is missing or not executable", path)
				continue
			}
		} else if p, err := exec.LookPath(t.Name); err != nil {
			add(name, false, "%s is not on PATH", t.Name)
			continue
		} else {
			path = p
		}
		if t.Argv == nil {
			add(name, true, "%s", path)
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, toolTimeout)
		cmd := exec.CommandContext(cctx, t.Argv[0], t.Argv[1:]...)
		// The last value of a repeated key wins (os/exec).
		cmd.Env = append(os.Environ(), "HOME="+home, "MISE_OFFLINE=1", "NO_COLOR=1")
		out, err := cmd.CombinedOutput()
		cancel()
		first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
		if err != nil {
			add(name, false, "%s: %v: %s", strings.Join(t.Argv, " "), err, first)
			continue
		}
		add(name, true, "%s", first)
	}
}
```

`internal/image/selftest.go`: add the two fields to `SelftestSpec` (after `SkipVerify`):

```go
	// Tools runs the presence checks of images/base/tools.tsv: "critical"
	// (a derived image's smoke), "all" (the base's CI smoke) or "" (none).
	Tools string `json:"tools,omitempty"`
	// ToolsOnly runs the presence checks alone (Tools, default "all"), for
	// the base image itself, which has no checkout. Every other field is
	// ignored.
	ToolsOnly bool `json:"tools_only,omitempty"`
```

In `Selftest`, right after the `if spec.RootChecks { … }` block:

```go
	if spec.ToolsOnly {
		checkTools(ctx, cmp.Or(spec.Tools, "all"), add)
		r.Passed = len(r.Checks) > 0
		for _, c := range r.Checks {
			r.Passed = r.Passed && c.OK
		}
		return r
	}
```

and after the `claude` check:

```go
	if spec.Tools != "" {
		checkTools(ctx, spec.Tools, add)
	}
```

(add `"cmp"` to the imports).

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/image/ -run 'TestToolsTableParses|TestParseToolsRefuses|TestSelftest' -count=1`
Expected: PASS, including the existing selftest tests (an empty `Tools` changes nothing).

- [ ] **Step 6: Commit**

```bash
git add images/base/tools.tsv images/images.go internal/image/tools.go internal/image/tools_test.go internal/image/selftest.go
git commit -m "base image task 1: the presence table and the selftest's presence checks"
```

---

### Task 2: The base image (`images/base/`)

A Fugaro run writes the files and runs the static tests. **The image build is verified by CI's `fugaro-base` jobs (Task 6) and the Docker tests (Task 7), not in a run.**

**Files:**
- Create: `images/base/Dockerfile`, `images/base/fetch.sh`, `images/base/mise-system.toml`, `images/base/harnesses/package.json`, `images/base/harnesses/package-lock.json`, `images/base_test.go`
- Move: `images/web-node/install-node.sh` → `images/base/install-node.sh` (`git mv`), then point `images/web-node/Dockerfile` and `images/java-services/Dockerfile` at the new path.
- Modify: `images/base/install-node.sh` (a `NODE_PREFIX`), `images/base_docker_test.go` (its `install-node` path)

**Interfaces:**
- Consumes: `images/common/finalize-checkout.sh`, `images/common/fugaro-services` (Task 3 moves it; until then, `images/java-services/fugaro-services`), `images/base/tools.tsv` (Task 1).
- Produces: `images/base/Dockerfile`, which `images/build-base.sh base TAG` builds unchanged. It also produces `fetch.sh URL SHA256 OUT` and `install-node` honouring `NODE_PREFIX` (default `/opt/node`).

- [ ] **Step 1: Write the failing static tests**

`images/base_test.go`:

```go
package images_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func baseDockerfile(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("base/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Every pin is one ARG line, every checksum 64 hex digits, every pinned
// download has both architectures, the OS is pinned by digest, and the
// hardening and the layer order of design sections 3, 5 and 7 hold.
func TestBaseDockerfileStripsKeysignAndPinsEverything(t *testing.T) {
	df := baseDockerfile(t)
	if !regexp.MustCompile(`(?m)^ARG DEBIAN_DIGEST=sha256:[0-9a-f]{64}$`).MatchString(df) || !strings.Contains(df, "\nFROM debian:trixie-slim@${DEBIAN_DIGEST}\n") {
		t.Error("the final stage is not debian:trixie-slim pinned by digest")
	}
	args := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^ARG ([A-Z0-9_]+)=(\S+)$`).FindAllStringSubmatch(df, -1) {
		args[m[1]] = m[2]
	}
	for name, v := range args {
		if strings.Contains(name, "_SHA256_") && !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(v) {
			t.Errorf("ARG %s=%s is not a sha256", name, v)
		}
	}
	for _, p := range []string{"MISE", "CLAUDE_CODE", "GH", "YQ", "GCLOUD", "DOCKER_CLI", "CODEX", "OPENCODE", "GOOSE", "CRUSH"} {
		if args[p+"_VERSION"] == "" || args[p+"_SHA256_AMD64"] == "" || args[p+"_SHA256_ARM64"] == "" {
			t.Errorf("%s lacks its version or a per-architecture sha256", p)
		}
	}
	if args["HARNESS_NODE_VERSION"] == "" {
		t.Error("no HARNESS_NODE_VERSION pin")
	}
	for _, must := range []string{
		"apt-get upgrade -y",
		"chmod u-s /usr/lib/openssh/ssh-keysign",
		"/usr/sbin/policy-rc.d",
		"create_main_cluster = false",
		"useradd --uid 1000 --user-group --create-home --shell /bin/bash fugaro",
		"install -d -o fugaro -g fugaro -m 0700 /work/creds",
		"COPY images/base/mise-system.toml /etc/mise/config.toml",
		`ENTRYPOINT ["/usr/bin/tini", "--"]`,
		`CMD ["fugaro", "exec"]`,
	} {
		if !strings.Contains(df, must) {
			t.Errorf("the Dockerfile lacks %q", must)
		}
	}
	// Every download goes through fetch.sh or install-node: no curl of its
	// own, and nothing piped into a shell.
	for _, line := range strings.Split(df, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if regexp.MustCompile(`\bcurl\s+-`).MatchString(line) || regexp.MustCompile(`\|\s*(ba)?sh\b`).MatchString(line) {
			t.Errorf("a download outside fetch.sh: %s", line)
		}
	}
	// Layer order: fugaro is the last thing copied, and the image ends as
	// fugaro in /work/repo.
	instr := regexp.MustCompile(`(?m)^(FROM|RUN|COPY|USER|WORKDIR)\b.*$`).FindAllString(df, -1)
	last := ""
	for _, in := range instr {
		if strings.HasPrefix(in, "RUN") || strings.HasPrefix(in, "COPY") {
			last = in
		}
	}
	if last != "COPY --from=fugaro /out/fugaro /usr/local/bin/fugaro" {
		t.Errorf("the last layer is %q, want the fugaro binary", last)
	}
	if !strings.Contains(df, "USER fugaro\nWORKDIR /work/repo") {
		t.Error("the image does not end as fugaro in /work/repo")
	}
}

func TestBaseHarnessLockfileMatchesPackageJSON(t *testing.T) {
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	var lock struct {
		LockfileVersion int `json:"lockfileVersion"`
		Packages        map[string]struct {
			Version      string            `json:"version"`
			Integrity    string            `json:"integrity"`
			Dependencies map[string]string `json:"dependencies"`
		} `json:"packages"`
	}
	for path, v := range map[string]any{"base/harnesses/package.json": &pkg, "base/harnesses/package-lock.json": &lock} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, v); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	if lock.LockfileVersion < 3 {
		t.Errorf("lockfileVersion %d, want 3", lock.LockfileVersion)
	}
	for name, v := range pkg.Dependencies {
		if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(v) {
			t.Errorf("%s@%s is not an exact version", name, v)
		}
		got := lock.Packages["node_modules/"+name]
		if got.Version != v || got.Integrity == "" {
			t.Errorf("the lockfile has %s at %q (integrity %q), package.json says %s", name, got.Version, got.Integrity, v)
		}
	}
	df := baseDockerfile(t)
	for _, entry := range []string{"@google/gemini-cli/bundle/gemini.js", "@mariozechner/pi-coding-agent/dist/cli.js", "@qwen-code/qwen-code/cli-entry.js"} {
		if !strings.Contains(df, entry) {
			t.Errorf("no wrapper for %s", entry)
		}
		pkgName := strings.Join(strings.Split(entry, "/")[:2], "/")
		if _, ok := pkg.Dependencies[pkgName]; !ok {
			t.Errorf("%s has a wrapper but is not a dependency", pkgName)
		}
	}
}

func TestMiseSystemConfig(t *testing.T) {
	data, err := os.ReadFile("base/mise-system.toml")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		`trusted_config_paths = ["/work/repo"]`,
		`idiomatic_version_file_enable_tools = []`,
		`auto_install = false`,
		`not_found_auto_install = false`,
		`yes = true`,
		`corepack = true`,
	} {
		if !strings.Contains(string(data), line) {
			t.Errorf("mise-system.toml lacks %s", line)
		}
	}
}

// fetch.sh refuses anything but https and a 64-digit sha256 before it
// downloads, and removes a download whose sum is wrong.
func TestFetchRefusesHTTPAndBadSums(t *testing.T) {
	bin := t.TempDir()
	// The fake curl writes "hello" to its -o argument.
	fake := "#!/bin/sh\nwhile [ $# -gt 0 ]; do if [ \"$1\" = -o ]; then printf hello > \"$2\"; fi; shift; done\n"
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	const helloSum = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	run := func(url, sum, out string) (string, error) {
		cmd := exec.Command("sh", "base/fetch.sh", url, sum, out)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	out := filepath.Join(t.TempDir(), "f")
	if msg, err := run("http://example.com/x", helloSum, out); err == nil || !strings.Contains(msg, "is not https") {
		t.Errorf("http: err=%v %s", err, msg)
	}
	if msg, err := run("https://example.com/x", "abc", out); err == nil || !strings.Contains(msg, "bad sha256") {
		t.Errorf("short sum: err=%v %s", err, msg)
	}
	if msg, err := run("https://example.com/x", strings.Repeat("0", 64), out); err == nil || !strings.Contains(msg, "does not match") {
		t.Errorf("wrong sum: err=%v %s", err, msg)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a download with the wrong sum was kept")
	}
	if msg, err := run("https://example.com/x", helloSum, out); err != nil {
		t.Errorf("right sum: %v %s", err, msg)
	}
}

func TestInstallNodeHonoursThePrefix(t *testing.T) {
	data, err := os.ReadFile("base/install-node.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `prefix=${NODE_PREFIX:-/opt/node}`) {
		t.Error("install-node has no NODE_PREFIX")
	}
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "/opt/node") && !strings.Contains(line, "NODE_PREFIX") && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			t.Errorf("a hard-coded /opt/node: %s", line)
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./images/ -run 'TestBaseDockerfile|TestBaseHarness|TestMiseSystemConfig|TestFetchRefuses|TestInstallNodeHonours' -count=1`
Expected: FAIL, `open base/Dockerfile: no such file or directory` (and the same for the other new files).

- [ ] **Step 3: Write `images/base/fetch.sh`**

```sh
#!/bin/sh
# fetch.sh URL SHA256 OUT downloads URL to OUT over https and keeps it only
# if its sha256 is SHA256 (design base-image.md section 7). The base image
# build bind-mounts it; it is never in the image.
set -eu
usage="usage: fetch.sh URL SHA256 OUT"
url=${1:?$usage}
sum=${2:?$usage}
out=${3:?$usage}
case "$url" in
  https://*) ;;
  *) echo "fetch: $url is not https" >&2; exit 1 ;;
esac
case "$sum" in
  *[!0-9a-f]*) echo "fetch: bad sha256 '$sum' for $url" >&2; exit 1 ;;
esac
[ "${#sum}" -eq 64 ] || { echo "fetch: bad sha256 '$sum' for $url" >&2; exit 1; }
curl -fsSL --proto '=https' --tlsv1.2 --retry 3 -o "$out" "$url"
if ! echo "$sum  $out" | sha256sum -c - >/dev/null 2>&1; then
  rm -f "$out"
  echo "fetch: $url does not match its pinned sha256 $sum" >&2
  exit 1
fi
```

- [ ] **Step 4: Write `images/base/mise-system.toml`**

```toml
# mise's system config in the Fugaro base image (design base-image.md section
# 4): a file, because the agent's environment is an allowlist that carries no
# MISE_* variable.
[settings]
# A repository's mise config can run code (env sources, hooks, tasks); so can
# everything else in the checkout the agent works on.
trusted_config_paths = ["/work/repo"]
# Only mise.toml, .tool-versions and their siblings decide what is installed:
# the image config hash covers exactly those files.
idiomatic_version_file_enable_tools = []
# A tool missing at run time fails loudly rather than downloading mid-run.
auto_install = false
not_found_auto_install = false
yes = true

[settings.node]
# pnpm and Yarn through package.json's packageManager, as on web-node.
corepack = true
```

- [ ] **Step 5: Write the harness package and its lockfile**

`images/base/harnesses/package.json`:

```json
{
  "name": "fugaro-base-harnesses",
  "private": true,
  "description": "The Node-based agent harnesses of the Fugaro base image (docs/design/base-image.md section 6), installed with npm ci --ignore-scripts and bumped by images/base/bump.sh. Present but not driven by the Fugaro runner.",
  "dependencies": {
    "@google/gemini-cli": "0.63.0",
    "@mariozechner/pi-coding-agent": "0.73.1",
    "@qwen-code/qwen-code": "0.25.0"
  }
}
```

Generate the lockfile with the harness Node itself. A Fugaro run's container has no Node, so fetch it into `/tmp`, verified:

```bash
cd /tmp && curl -fsSLO https://nodejs.org/dist/v24.21.0/node-v24.21.0-linux-x64.tar.xz \
  && echo "fd8e59d5a511510f6a298afb548f18c7d2b1be404d8b4a27d94fbe49f56cb2d6  node-v24.21.0-linux-x64.tar.xz" | sha256sum -c - \
  && tar -xJf node-v24.21.0-linux-x64.tar.xz
cd <worktree>/images/base/harnesses \
  && PATH=/tmp/node-v24.21.0-linux-x64/bin:$PATH npm install --package-lock-only --ignore-scripts --no-audit --no-fund
```

On an arm64 machine, use `linux-arm64` with sha256 `6ad1325edbdb5649c379b75a237147a666c95d4f9ae8d340fef2d1575d289ad2`. Commit the generated `package-lock.json` unedited.

- [ ] **Step 6: Give `install-node` a prefix**

`git mv images/web-node/install-node.sh images/base/install-node.sh`. In it:
- add `prefix=${NODE_PREFIX:-/opt/node}` after `keyring=…`;
- replace every `/opt/node` in the code (not the comments) with `"$prefix"`: the `[ -x … ]` check, `rm -rf`, `mkdir -p`, the `tar -C`, and the npm and corepack paths. For example, `rm -rf "$prefix"`, `mkdir -p "$prefix"`, `tar -xJf "$tmp/$file" -C "$prefix" --strip-components=1 --no-same-owner`, `HOME=/root "$prefix/bin/npm" install --global …` and `"$prefix/bin/corepack" enable`;
- change its header comment to say it installs "into $NODE_PREFIX (default /opt/node)".

In `images/web-node/Dockerfile`, `images/java-services/Dockerfile` and `images/base_docker_test.go`, change `images/web-node/install-node.sh` to `images/base/install-node.sh`.

- [ ] **Step 7: Write `images/base/Dockerfile`**

```dockerfile
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
      diffutils dirmngr eza fd-find file fzf git git-lfs gnupg httpie jq less libbz2-dev libcap2-bin \
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
```

The archive layouts were checked on 2026-10-08:
- mise has `mise/bin/mise`;
- Codex has a single `codex-<target>` file;
- OpenCode has `opencode`;
- Goose has `./goose`;
- Crush has `crush_<v>_Linux_<arch>/crush`.

`one`'s `find` covers all of them. `images/common/fugaro-services` exists after Task 3. If Task 2 runs first, use `COPY --chmod=0755 images/java-services/fugaro-services /usr/local/bin/fugaro-services`; Task 3 changes the path.

- [ ] **Step 8: Run the static tests**

Run: `go test ./images/ -run 'TestBaseDockerfile|TestBaseHarness|TestMiseSystemConfig|TestFetchRefuses|TestInstallNodeHonours|TestSmoke|TestGoBase|TestJavaServices' -count=1`
Expected: PASS. The old bases' tests still pass with the moved `install-node.sh`.

If Docker is available (it is not in a Fugaro run), also run `sh images/build-base.sh base fugaro-base:dev`. Then run `docker run --rm --user 0 fugaro-base:dev find / -xdev -perm /6000 -type f`. Expected: `chfn chsh gpasswd mount newgrp passwd su umount sudo chage expiry unix_chkpwd` (in `/usr/bin` and `/usr/sbin`), and nothing else. If Debian 13 differs, adjust the selftest's allowlists in Task 12 to the observed set **minus** `sudo` and `su`, with the observation in a comment. Otherwise CI's Task 7 test reports the difference.

- [ ] **Step 9: Commit**

```bash
git add images/base images/web-node/Dockerfile images/java-services/Dockerfile images/base_test.go images/base_docker_test.go
git commit -m "base image task 2: the Debian 13 base with the kit, mise and the harnesses"
```

---

### Task 3: `fugaro-services` finds what the project installed

**Files:**
- Move: `images/java-services/fugaro-services` → `images/common/fugaro-services` (`git mv`)
- Modify: `images/common/fugaro-services`, `images/java-services/Dockerfile` (its `COPY` path), `images/base/Dockerfile` (its `COPY` path, if Task 2 used the old one)
- Test: move the `runServices`, `listenOnFreePort`, `portOf` helpers and the `TestFugaroServices*` tests from `images/java_services_test.go` to a new `images/services_test.go`, pointing at `common/fugaro-services`.

**Interfaces:**
- Produces: the new setting `FUGARO_POSTGRES_LIB` (default `/usr/lib/postgresql`), `pg_bin`, and the missing-service messages below. The emulator jars default to `${FIREBASE_EMULATORS_PATH:-$HOME/.cache/firebase/emulators}`.

- [ ] **Step 1: Write the failing tests** (append to `images/services_test.go`)

```go
// A selected service that is not installed fails start at once, naming what
// to install (design base-image.md section 5).
func TestFugaroServicesNamesTheMissingPackage(t *testing.T) {
	empty := t.TempDir()
	out, err := runServices(t, []string{"FUGARO_SERVICES=postgres", "FUGARO_POSTGRES_LIB=" + empty}, "start")
	if err == nil || !strings.Contains(out, "postgres: not installed; add postgresql-17 to image.apt") {
		t.Errorf("postgres missing: err=%v\n%s", err, out)
	}
	if _, err := exec.LookPath("firebase"); err == nil {
		t.Skip("this machine has firebase on PATH")
	}
	out, err = runServices(t, []string{"FUGARO_SERVICES=firebase"}, "start")
	if err == nil || !strings.Contains(out, `firebase: not installed; add "npm:firebase-tools" to mise.toml`) {
		t.Errorf("firebase missing: err=%v\n%s", err, out)
	}
}

// The newest PostgreSQL major installed is the one used.
func TestFugaroServicesFindsTheNewestPostgres(t *testing.T) {
	lib := t.TempDir()
	marker := filepath.Join(t.TempDir(), "which")
	for _, major := range []string{"16", "17"} {
		bin := filepath.Join(lib, major, "bin")
		testutilWrite(t, filepath.Join(bin, "initdb"), "#!/bin/sh\necho "+major+" > "+marker+"\nexit 1\n")
		testutilWrite(t, filepath.Join(bin, "pg_ctl"), "#!/bin/sh\nexit 1\n")
	}
	out, err := runServices(t, []string{"FUGARO_SERVICES=postgres", "FUGARO_POSTGRES_LIB=" + lib, "FUGARO_SERVICES_DIR=" + t.TempDir()}, "start")
	if err == nil || !strings.Contains(out, "postgres: initdb failed") {
		t.Fatalf("err=%v\n%s", err, out)
	}
	if got, _ := os.ReadFile(marker); strings.TrimSpace(string(got)) != "17" {
		t.Errorf("ran initdb of %q, want 17", got)
	}
}

func testutilWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./images/ -run TestFugaroServices -count=1`
Expected: FAIL. The first fails on the message (today `initdb: command not found`); the second runs the `initdb` of `PATH`, so the marker is empty.

- [ ] **Step 3: Implement**

In `images/common/fugaro-services`:
- Replace the first header line's "the backing services a Java test suite wants" with "the backing services a test suite wants, installed by the project (image.apt for PostgreSQL and Redis, mise for firebase-tools; docs/base-image.md)".
- Add to the settings list: `#   FUGARO_POSTGRES_LIB        where PostgreSQL majors live (default /usr/lib/postgresql)`.
- After the variable block, add:

```bash
PG_LIB=${FUGARO_POSTGRES_LIB:-/usr/lib/postgresql}

# pg_bin prints the bin directory of the newest PostgreSQL major installed
# under PG_LIB (Debian's postgresql-<major>), or fails when there is none.
pg_bin() {
  local d
  d=$(ls -d "$PG_LIB"/*/bin 2>/dev/null | sort -V | tail -n 1)
  [ -n "$d" ] && [ -x "$d/initdb" ] || return 1
  echo "$d"
}
```

- At the top of `start_postgres`:

```bash
  local bin
  bin=$(pg_bin) || die "postgres: not installed; add postgresql-17 to image.apt (with postgresql-17-postgis-3 and postgresql-17-pgvector if the tests use PostGIS or pgvector)"
  PATH=$bin:$PATH
```

- At the top of `start_redis`: `command -v redis-server >/dev/null 2>&1 || die "redis: not installed; add redis-server to image.apt"`.
- At the top of `start_firebase`: `command -v firebase >/dev/null 2>&1 || die 'firebase: not installed; add "npm:firebase-tools" to mise.toml, with java and node'`.
- In `stop_emulator_jars`: `local jars=${FIREBASE_EMULATORS_PATH:-$HOME/.cache/firebase/emulators}`.
- In `cmd_stop`, before `pg_ctl`: `local bin; bin=$(pg_bin) && PATH=$bin:$PATH`.

- [ ] **Step 4: Run the tests**

Run: `go test ./images/ -run 'TestFugaroServices|TestSmokeJavaServices|TestJavaServices' -count=1`
Expected: PASS. The `java-services` image keeps working: its PostgreSQL is in `/usr/lib/postgresql/17/bin`, and it sets `FIREBASE_EMULATORS_PATH`.

- [ ] **Step 5: Commit**

```bash
git add images/common/fugaro-services images/java-services images/base/Dockerfile images/services_test.go images/java_services_test.go
git commit -m "base image task 3: fugaro-services moves to common and finds what the project installed"
```

---

### Task 4: The base's smoke and the size budget

**Files:**
- Create: `images/size-budget.sh`, `images/size_budget_test.go`, `images/base_smoke_test.go`
- Modify: `images/smoke.sh`, `images/smoke_test.go` (the fake docker's new cases)

**Interfaces:**
- Produces: `smoke.sh IMAGE base`, which checks every pin of `images/base/Dockerfile` and runs `fugaro image selftest` with `{"tools":"all","tools_only":true}` under `--network none`. Also `size-budget.sh IMAGE [MAX_MB [WARN_MB]]`.

- [ ] **Step 1: Write the failing tests**

Add these cases to `fakeDockerScript`'s `case "$cmd" in` in `images/smoke_test.go`, before the default:

```sh
  "mise --version")
    [ "$missing" = mise ] && fail_missing mise
    echo "$FAKE_MISE_VERSION linux-x64 (2026-10-07)" ;;
  "gcloud --version")
    [ "$missing" = gcloud ] && fail_missing gcloud
    printf 'Google Cloud SDK %s\nbq 2.1.0\n' "$FAKE_GCLOUD_VERSION" ;;
  "docker --version")
    echo "Docker version $FAKE_DOCKER_CLI_VERSION, build 1a2b3c4" ;;
  "yq --version")
    echo "yq (https://github.com/mikefarah/yq/) version v$FAKE_YQ_VERSION" ;;
  "codex --version") echo "codex-cli $FAKE_CODEX_VERSION" ;;
  "opencode --version") echo "$FAKE_OPENCODE_VERSION" ;;
  "goose --version") echo " $FAKE_GOOSE_VERSION" ;;
  "crush --version") echo "crush version v$FAKE_CRUSH_VERSION" ;;
  "/opt/fugaro/node/bin/node -v") echo "v$FAKE_HARNESS_NODE_VERSION" ;;
  "cat /etc/fugaro/base.json") printf '{"mise":"%s"}\n' "$FAKE_MISE_VERSION" ;;
  "fugaro image selftest")
    cat >/dev/null
    if [ "$missing" = selftest ]; then echo '{"passed":false,"checks":[{"name":"tool:gemini","ok":false}]}'; exit 1; fi
    echo '{"passed":true,"checks":[{"name":"tool:gemini","ok":true}]}' ;;
```

`images/base_smoke_test.go`:

```go
package images_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var basePins = []string{"CLAUDE_CODE_VERSION", "GH_VERSION", "MISE_VERSION", "GCLOUD_VERSION", "DOCKER_CLI_VERSION",
	"YQ_VERSION", "CODEX_VERSION", "OPENCODE_VERSION", "GOOSE_VERSION", "CRUSH_VERSION", "HARNESS_NODE_VERSION"}

// runSmokeBase runs smoke.sh against the base with a fake docker whose image
// reports the Dockerfile's pins unless fakeEnv overrides one.
func runSmokeBase(t *testing.T, missing string, fakeEnv ...string) (string, error) {
	t.Helper()
	df := baseDockerfile(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(fakeDockerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "smoke.sh", "fake-image", "base")
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !strings.HasSuffix(name, "_VERSION") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+dir+":"+os.Getenv("PATH"))
	for _, pin := range basePins {
		fake := "FAKE_" + strings.TrimSuffix(pin, "_VERSION") + "_VERSION"
		if pin == "CLAUDE_CODE_VERSION" {
			fake = "FAKE_CLAUDE_VERSION"
		}
		cmd.Env = append(cmd.Env, fake+"="+argIn(t, df, pin))
	}
	cmd.Env = append(cmd.Env, fakeEnv...)
	if missing != "" {
		cmd.Env = append(cmd.Env, "FAKE_DOCKER_MISSING="+missing)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestSmokeBaseSucceedsAgainstAHealthyImage(t *testing.T) {
	if out, err := runSmokeBase(t, ""); err != nil {
		t.Fatalf("smoke failed: %v\n%s", err, out)
	}
}

func TestSmokeBaseChecksEveryPin(t *testing.T) {
	for _, fake := range []string{"FAKE_MISE_VERSION", "FAKE_GCLOUD_VERSION", "FAKE_DOCKER_CLI_VERSION", "FAKE_YQ_VERSION",
		"FAKE_CODEX_VERSION", "FAKE_OPENCODE_VERSION", "FAKE_GOOSE_VERSION", "FAKE_CRUSH_VERSION", "FAKE_HARNESS_NODE_VERSION"} {
		out, err := runSmokeBase(t, "", fake+"=0.0.1")
		if err == nil || !strings.Contains(out, "not pinned") {
			t.Errorf("%s=0.0.1 passed: %v\n%s", fake, err, out)
		}
	}
}

func TestSmokeBaseFailsWhenAToolIsMissing(t *testing.T) {
	for _, missing := range []string{"mise", "gcloud", "selftest"} {
		if out, err := runSmokeBase(t, missing); err == nil {
			t.Errorf("passed with %s missing:\n%s", missing, out)
		}
	}
}
```

`images/size_budget_test.go`:

```go
package images_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runBudget(t *testing.T, bytes string, args ...string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	fake := "#!/bin/sh\n[ \"$1 $2\" = \"image inspect\" ] || exit 2\necho " + bytes + "\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", append([]string{"size-budget.sh", "fake-image"}, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestSizeBudget(t *testing.T) {
	if out, err := runBudget(t, "3100000000"); err != nil || !strings.Contains(out, "3100 MB") || strings.Contains(out, "warning") {
		t.Errorf("under: err=%v %s", err, out)
	}
	if out, err := runBudget(t, "3600000000"); err != nil || !strings.Contains(out, "::warning::") {
		t.Errorf("warn: err=%v %s", err, out)
	}
	if out, err := runBudget(t, "4100000000"); err == nil || !strings.Contains(out, "over its budget") {
		t.Errorf("over: err=%v %s", err, out)
	}
	if out, err := runBudget(t, "1500000000", "1000"); err == nil {
		t.Errorf("a custom budget is ignored: %s", out)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./images/ -run 'TestSmokeBase|TestSizeBudget' -count=1`
Expected: FAIL. `smoke: unknown base base`, and `size-budget.sh: No such file`.

- [ ] **Step 3: Implement**

In `images/smoke.sh`, update the header comment: the pins now include `MISE_VERSION`, `GCLOUD_VERSION`, `DOCKER_CLI_VERSION`, `YQ_VERSION`, the harness versions and `HARNESS_NODE_VERSION` for `base`. Then add, before `*) fail "unknown base $base" ;;`:

```sh
  base)
    # pinned NAME CMD... asserts CMD's output names the Dockerfile's pin NAME.
    pinned() {
      name=$1; shift
      want=$(eval "printf '%s' \"\${$name:-}\"")
      [ -n "$want" ] || want=$(arg_default "$name")
      [ -n "$want" ] || fail "$dockerfile has no ARG $name=... pin"
      got=$(first_line "$(run "$@" 2>&1)") || fail "$* failed"
      echo "$got"
      case "$got" in *"$want"*) ;; *) fail "$* reports '$got', not pinned $name=$want" ;; esac
    }
    pinned MISE_VERSION mise --version
    pinned GCLOUD_VERSION gcloud --version
    pinned DOCKER_CLI_VERSION docker --version
    pinned YQ_VERSION yq --version
    pinned CODEX_VERSION codex --version
    pinned OPENCODE_VERSION opencode --version
    pinned GOOSE_VERSION goose --version
    pinned CRUSH_VERSION crush --version
    pinned HARNESS_NODE_VERSION /opt/fugaro/node/bin/node -v
    pinned MISE_VERSION cat /etc/fugaro/base.json
    # Every row of images/base/tools.tsv, presence only: no network, and the
    # selftest gives each tool an empty HOME.
    report=$(printf '%s' '{"tools":"all","tools_only":true}' | docker run --rm -i --network none "$image" fugaro image selftest) \
      || { printf '%s\n' "$report"; fail "the presence checks failed (report above)"; }
    case "$report" in *'"passed":true'*) ;; *) printf '%s\n' "$report"; fail "the presence checks did not pass" ;; esac
    echo "presence checks passed"
    ;;
```

`images/size-budget.sh`:

```sh
#!/bin/sh
# size-budget.sh IMAGE [MAX_MB [WARN_MB]] fails when IMAGE's uncompressed size
# (docker image inspect's .Size, in MB of 10^6 bytes) is over MAX_MB (default
# 4000) and warns over WARN_MB (default 3500), so the base grows only by
# decision (design base-image.md section 13). It prints the size either way.
set -eu
image=${1:?usage: size-budget.sh IMAGE [MAX_MB [WARN_MB]]}
max=${2:-4000}
warn=${3:-3500}
bytes=$(docker image inspect --format '{{.Size}}' "$image")
mb=$((bytes / 1000000))
echo "size-budget: $image is $mb MB (budget $max MB, warning at $warn MB)"
if [ "$mb" -gt "$max" ]; then
  echo "size-budget: $image is over its budget of $max MB: drop something or raise the budget in images.yml on purpose" >&2
  exit 1
fi
if [ "$mb" -gt "$warn" ]; then
  echo "::warning::size-budget: $image is $mb MB, over the $warn MB warning line"
fi
```

- [ ] **Step 4: Run the tests**

Run: `go test ./images/ -run 'TestSmoke|TestSizeBudget' -count=1`
Expected: PASS, including the legacy bases' smoke tests.

- [ ] **Step 5: Commit**

```bash
git add images/smoke.sh images/size-budget.sh images/smoke_test.go images/base_smoke_test.go images/size_budget_test.go
git commit -m "base image task 4: the base's smoke checks every pin and tool; a size budget"
```

---

### Task 5: The secret scan over every layer

**Files:**
- Create: `images/trivy-secret.yaml`, `images/scan_test.go`
- Modify: `images/scan.sh`

**Interfaces:**
- Produces: `scan.sh --secrets IMAGE`, a Trivy secret scan of every layer and the image config that fails on any finding. `scan.sh IMAGE` is unchanged.

- [ ] **Step 1: Write the failing test**

`images/scan_test.go`:

```go
package images_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The fake docker logs its arguments and exits FAKE_EXIT.
func runScan(t *testing.T, exit string, args ...string) (string, string, error) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	fake := "#!/bin/sh\necho \"$*\" >> \"$FAKE_LOG\"\nexit ${FAKE_EXIT:-0}\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", append([]string{"scan.sh"}, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "FAKE_LOG="+log, "FAKE_EXIT="+exit, "TRIVY_CACHE_DIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	logged, _ := os.ReadFile(log)
	return string(out), string(logged), err
}

func TestScanSecretsScansLayersAndConfig(t *testing.T) {
	_, log, err := runScan(t, "0", "--secrets", "fugaro-base:ci")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--scanners secret", "--image-config-scanners secret", "--secret-config /trivy-secret.yaml", "--exit-code 1", "fugaro-base:ci"} {
		if !strings.Contains(log, want) {
			t.Errorf("the trivy call lacks %q: %s", want, log)
		}
	}
	if strings.Contains(log, "--scanners vuln") {
		t.Errorf("--secrets ran the vulnerability scan: %s", log)
	}
}

func TestScanSecretsFailsOnAFinding(t *testing.T) {
	if _, _, err := runScan(t, "1", "--secrets", "fugaro-base:ci"); err == nil {
		t.Fatal("a secret finding passed")
	}
}

func TestScanVulnUnchanged(t *testing.T) {
	_, log, err := runScan(t, "0", "fugaro-base:ci")
	if err != nil || !strings.Contains(log, "--scanners vuln --ignore-unfixed") {
		t.Fatalf("err=%v log=%s", err, log)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./images/ -run TestScan -count=1`
Expected: FAIL. `--secrets` is taken as the image name, so the log lacks `--scanners secret`.

- [ ] **Step 3: Implement**

`images/trivy-secret.yaml`:

```yaml
# Allowances for Trivy's secret scanner over the Fugaro images (images/scan.sh
# --secrets, design base-image.md section 7). Each entry allows one path
# pattern for a reason written next to it: a test fixture inside a vendored
# package, never a rule turned off everywhere. An empty list is the goal.
allow-rules: []
```

`images/scan.sh` becomes:

```sh
#!/bin/sh
# scan.sh IMAGE scans a built image for vulnerabilities with Trivy, run
# through plain docker. It prints HIGH and CRITICAL findings and fails only
# on CRITICAL ones that have a fix available.
#
# scan.sh --secrets IMAGE scans every layer (each one separately, so a file a
# later layer deletes is still seen) and the image config (its history and
# environment, where a leaked build argument would show) for secrets, and
# fails on any finding not allowed by images/trivy-secret.yaml.
set -eu
mode=vuln
if [ "${1:-}" = --secrets ]; then
  mode=secrets
  shift
fi
image=${1:?usage: scan.sh [--secrets] IMAGE}
cache=${TRIVY_CACHE_DIR:-$HOME/.cache/trivy}
mkdir -p "$cache"
here=$(cd "$(dirname "$0")" && pwd)
# Pinned by digest (not just the 0.65.0 tag, which is mutable): checked with
# `docker inspect aquasec/trivy:0.65.0 --format '{{index .RepoDigests 0}}'`
# on 2026-09-27. images/base/bump.sh bumps it monthly.
trivy_image=aquasec/trivy@sha256:a22415a38938a56c379387a8163fcb0ce38b10ace73e593475d3658d578b2436
trivy() {
  docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v "$cache:/root/.cache/trivy" \
    -v "$here/trivy-secret.yaml:/trivy-secret.yaml:ro" "$trivy_image" image "$@" "$image"
}
if [ "$mode" = secrets ]; then
  trivy --scanners secret --image-config-scanners secret --secret-config /trivy-secret.yaml --exit-code 1
  exit 0
fi
trivy --scanners vuln --ignore-unfixed --severity HIGH,CRITICAL --exit-code 0
trivy --scanners vuln --ignore-unfixed --severity CRITICAL --exit-code 1 --quiet
```

(The vulnerability calls keep their flags; `--scanners vuln --ignore-unfixed` moved from the function into each call.)

- [ ] **Step 4: Run the tests**

Run: `go test ./images/ -run 'TestScan|TestCIWorkflowUsesScripts' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add images/scan.sh images/trivy-secret.yaml images/scan_test.go
git commit -m "base image task 5: scan.sh --secrets, Trivy over every layer and the image config"
```

---

### Task 6: The images workflow: per-architecture builds, an index, verify-public

A Fugaro run edits and statically tests the workflow; **the workflow's real run is CI's** (a PR touching `images/**` runs it).

**Files:**
- Modify: `.github/workflows/images.yml`, `images/verify-public.sh`, `.goreleaser.yaml`, `images/verify_public_test.go`, `images/go_test.go` (`TestImagesWorkflowBuildsAndPublishesEveryBase`)
- Create: `images/base_workflow_test.go`

**Interfaces:**
- Produces: the jobs `fugaro-base` (matrix `arch: [amd64, arm64]`) and `publish-base`; `verify-public.sh [--arm64] REF...`; the release footer's `fugaro-base` line.

- [ ] **Step 1: Write the failing tests**

`images/base_workflow_test.go`:

```go
package images_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestImagesWorkflowPublishesAMultiArchBase(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/images.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Jobs map[string]struct {
			RunsOn      string            `yaml:"runs-on"`
			Permissions map[string]string `yaml:"permissions"`
			Strategy    struct {
				Matrix struct {
					Include []map[string]string `yaml:"include"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
			Steps []struct {
				Run string            `yaml:"run"`
				Env map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatal(err)
	}
	b, ok := wf.Jobs["fugaro-base"]
	if !ok {
		t.Fatal("no fugaro-base job")
	}
	runners := map[string]string{}
	for _, inc := range b.Strategy.Matrix.Include {
		runners[inc["arch"]] = inc["runner"]
	}
	if runners["amd64"] != "ubuntu-latest" || runners["arm64"] != "ubuntu-24.04-arm" {
		t.Errorf("the base builds on %v, want native amd64 and arm64 runners", runners)
	}
	if b.Permissions["packages"] != "" {
		t.Error("the build job has a packages permission")
	}
	var runs []string
	for _, s := range b.Steps {
		runs = append(runs, s.Run)
	}
	all := strings.Join(runs, "\n")
	for _, want := range []string{
		"images/build-base.sh base fugaro-base:ci",
		"images/smoke.sh fugaro-base:ci base",
		"images/size-budget.sh fugaro-base:ci",
		"images/scan.sh --secrets fugaro-base:ci",
		`fugaro-base-$ARCH.tar.gz`,
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the fugaro-base job never runs %q", want)
		}
	}
	p, ok := wf.Jobs["publish-base"]
	if !ok || p.Permissions["packages"] != "write" {
		t.Fatal("no publish-base job with packages: write")
	}
	pub := ""
	for _, s := range p.Steps {
		pub += s.Run
	}
	for _, want := range []string{`"$repo:$VERSION-$arch"`, "docker buildx imagetools create", `--tag $repo:$VERSION`, `--tag $repo:$MAJOR`} {
		if !strings.Contains(pub, want) {
			t.Errorf("publish-base lacks %q", want)
		}
	}
	v := ""
	for _, s := range wf.Jobs["verify-public"].Steps {
		v += s.Run
	}
	if !strings.Contains(v, "images/verify-public.sh --arm64") {
		t.Error("verify-public does not require arm64 for the base")
	}
}
```

Append to `images/verify_public_test.go`:

```go
const indexAmd64Only = `{"manifests":[{"platform":{"architecture":"amd64","os":"linux"}}]}`

func TestVerifyPublicRequiresArm64ForTheBase(t *testing.T) {
	if out, _, err := runVerify(t, indexAmd64, nil, "--arm64", "ghcr.io/o/base:1"); err != nil {
		t.Fatalf("an amd64+arm64 index failed: %v\n%s", err, out)
	}
	out, _, err := runVerify(t, indexAmd64Only, nil, "--arm64", "ghcr.io/o/base:1")
	if err == nil || !strings.Contains(out, "no linux/arm64 entry") {
		t.Fatalf("an amd64-only index passed --arm64: %v\n%s", err, out)
	}
	out, _, err = runVerify(t, singleManifest, nil, "--arm64", "ghcr.io/o/base:1")
	if err == nil || !strings.Contains(out, "is not a multi-platform index") {
		t.Fatalf("a single manifest passed --arm64: %v\n%s", err, out)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./images/ -run 'TestImagesWorkflowPublishesAMultiArchBase|TestVerifyPublicRequiresArm64' -count=1`
Expected: FAIL, `no fugaro-base job`, and `--arm64` read as a reference.

- [ ] **Step 3: Implement**

`images/verify-public.sh`:
- Usage becomes `verify-public.sh [--arm64] REF...`. After `set -eu`, add:

```sh
arm64=false
if [ "${1:-}" = --arm64 ]; then
  arm64=true
  shift
fi
```

- In the index branch, after the amd64 check:

```sh
      if [ "$arm64" = true ] && ! printf '%s' "$manifest" | tr -d ' \n\t' | grep -q '"architecture":"arm64","os":"linux"\|"os":"linux","architecture":"arm64"'; then
        echo "verify-public: $ref has no linux/arm64 entry" >&2
        bad=1
        continue
      fi
```

- In the single-manifest branch, first:

```sh
      if [ "$arm64" = true ]; then
        echo "verify-public: $ref is not a multi-platform index (--arm64 needs linux/amd64 and linux/arm64)" >&2
        bad=1
        continue
      fi
```

- Update the header comment to say `--arm64` also requires a linux/arm64 entry, for `fugaro-base`.

`.github/workflows/images.yml`:
- In the `kinds` step, `for k in web-node go java-services history base; do`.
- Add the job (after `history`):

```yaml
  # The one base (design base-image.md): built natively per architecture,
  # smoke-tested (every pin and every tools.tsv row), held to its size
  # budget and scanned for secrets on every run, scanned for
  # vulnerabilities off pull requests, and saved for publish-base.
  fugaro-base:
    needs: version
    permissions:
      contents: read
    strategy:
      fail-fast: false
      matrix:
        include:
          - arch: amd64
            runner: ubuntu-latest
          - arch: arm64
            runner: ubuntu-24.04-arm
    runs-on: ${{ matrix.runner }}
    timeout-minutes: 90
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        if: contains(needs.version.outputs.kinds, ',base,')
        with:
          persist-credentials: false
          fetch-depth: 0
      - name: Checkout the resolved tag
        if: contains(needs.version.outputs.kinds, ',base,') && needs.version.outputs.tag != ''
        env:
          TAG: ${{ needs.version.outputs.tag }}
        run: git checkout --quiet "refs/tags/$TAG"
      # The runner's disk is about 14 GB free; the base and its saved
      # archive need room.
      - name: Free disk space
        if: contains(needs.version.outputs.kinds, ',base,')
        run: sudo rm -rf /usr/share/dotnet /usr/local/lib/android /opt/ghc /opt/hostedtoolcache/CodeQL && df -h /
      - name: Build
        if: contains(needs.version.outputs.kinds, ',base,')
        env:
          FUGARO_VERSION: ${{ needs.version.outputs.version }}
          ARCH: ${{ matrix.arch }}
        run: PLATFORM="linux/$ARCH" images/build-base.sh base fugaro-base:ci
      - name: Smoke test
        if: contains(needs.version.outputs.kinds, ',base,')
        run: images/smoke.sh fugaro-base:ci base
      - name: Size budget
        if: contains(needs.version.outputs.kinds, ',base,')
        run: images/size-budget.sh fugaro-base:ci
      - name: Secret scan
        if: contains(needs.version.outputs.kinds, ',base,')
        run: images/scan.sh --secrets fugaro-base:ci
      - name: Vulnerability scan
        if: contains(needs.version.outputs.kinds, ',base,') && github.event_name != 'pull_request'
        run: images/scan.sh fugaro-base:ci
      - name: Save the image for publish-base
        if: contains(needs.version.outputs.kinds, ',base,') && needs.version.outputs.publish == 'true'
        env:
          ARCH: ${{ matrix.arch }}
        run: docker save fugaro-base:ci | gzip > "fugaro-base-$ARCH.tar.gz"
      - uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
        if: contains(needs.version.outputs.kinds, ',base,') && needs.version.outputs.publish == 'true'
        with:
          name: fugaro-base-${{ matrix.arch }}-image
          path: fugaro-base-${{ matrix.arch }}.tar.gz
          retention-days: 1
  # Pushes each architecture under <version>-<arch>, then the
  # multi-platform index <version> (and <major> when it moves) from the two
  # pushed digests. The only job besides publish with packages: write.
  publish-base:
    needs: [version, fugaro-base]
    if: needs.version.outputs.publish == 'true' && contains(needs.version.outputs.kinds, ',base,')
    concurrency:
      group: images-publish-base
      cancel-in-progress: false
    permissions:
      contents: read
      packages: write
    runs-on: ubuntu-latest
    timeout-minutes: 30
    steps:
      - uses: actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8.0.1
        with:
          pattern: fugaro-base-*-image
          merge-multiple: true
      - name: Push per architecture and publish the index
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          GH_ACTOR: ${{ github.actor }}
          VERSION: ${{ needs.version.outputs.version }}
          MAJOR: ${{ needs.version.outputs.major }}
          MOVE_MAJOR: ${{ needs.version.outputs.move_major }}
        run: |
          set -eu
          echo "$GH_TOKEN" | docker login ghcr.io -u "$GH_ACTOR" --password-stdin
          repo=ghcr.io/dimipaun/fugaro-base
          digests=""
          for arch in amd64 arm64; do
            gunzip -c "fugaro-base-$arch.tar.gz" | docker load
            docker tag fugaro-base:ci "$repo:$VERSION-$arch"
            docker push "$repo:$VERSION-$arch"
            digests="$digests $(docker image inspect --format '{{index .RepoDigests 0}}' "$repo:$VERSION-$arch")"
            docker image rm fugaro-base:ci "$repo:$VERSION-$arch" >/dev/null
          done
          tags="--tag $repo:$VERSION"
          if [ "$MOVE_MAJOR" = true ]; then
            tags="$tags --tag $repo:$MAJOR"
          fi
          # shellcheck disable=SC2086
          docker buildx imagetools create $tags $digests
          {
            echo '### fugaro-base'
            for arch in amd64 arm64; do
              mb=$(docker buildx imagetools inspect "$repo:$VERSION-$arch" --raw | jq '[.layers[].size] | add / 1000000 | floor')
              echo "- $arch: $mb MB compressed"
            done
          } >> "$GITHUB_STEP_SUMMARY"
```

- `verify-public`: `needs: [version, publish, publish-base]`. In its script, after the existing loop, add:

```sh
          if case "$KINDS" in *",base,"*) true ;; *) false ;; esac; then
            base="ghcr.io/dimipaun/fugaro-base:$VERSION"
            if [ "$MOVE_MAJOR" = true ]; then base="$base ghcr.io/dimipaun/fugaro-base:$MAJOR"; fi
            # shellcheck disable=SC2086
            images/verify-public.sh --arm64 $base | tee -a verify.log
          fi
```

  and move the `if [ -z "$refs" ]` early exit so it skips only the first call.

`.goreleaser.yaml` footer, after the `fugaro-java-services` line:

```
    - `ghcr.io/dimipaun/fugaro-base:{{ .Version }}` and `ghcr.io/dimipaun/fugaro-base:{{ .Major }}` (one index for linux/amd64 and linux/arm64; per-architecture tags `{{ .Version }}-amd64` and `{{ .Version }}-arm64`)
```

and its last sentence becomes "…fails if it is not public or lacks `linux/amd64` (and, for `fugaro-base`, `linux/arm64`)…".

- [ ] **Step 4: Run the tests**

Run: `go test ./images/ ./scripts/ -run 'TestImagesWorkflow|TestVerifyPublic|TestCIWorkflow|TestWorkflowActionsPinnedBySHA|TestRelease' -count=1`
Expected: PASS. `TestCIWorkflowSkipsImagesTheTagTreeDoesNotHold` and `TestCIWorkflowJobsHaveTimeouts` cover the new jobs: every step carries the `kinds` condition, and both jobs have timeouts.

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/images.yml images/verify-public.sh .goreleaser.yaml images/verify_public_test.go images/base_workflow_test.go images/go_test.go
git commit -m "base image task 6: build per architecture, publish an index, verify arm64"
```

---

### Task 7: Docker tests: the base's hardening and its tools

**Runs only in CI's `docker-tests` job** (`-tags docker`). A Fugaro run compiles it with `go vet -tags docker ./images/` and stops there. The mise sample project needs Group 2's template, so it is Task 12's Docker test.

**Files:**
- Modify: `internal/testutil/docker.go` (`BaseImageOf(t, kind)`), `images/base_docker_test.go`, `.github/workflows/ci.yml` (the `docker-tests` timeout)

**Interfaces:**
- Consumes: `images/build-base.sh`, `testutil.Docker`, `testutil.DockerPlatform`.
- Produces: `testutil.BaseImageOf(t *testing.T, kind string) string`, which builds `images/<kind>` once per test binary and kind as `fugaro-<kind>:test`. `BaseImage(t)` stays, as `BaseImageOf(t, "web-node")`, until Task 20.

- [ ] **Step 1: Write the tests**

Append to `images/base_docker_test.go`:

```go
// The base keeps D1's hardening on Debian 13: not root, no passwordless sudo,
// the expected setuid and setgid sets (sudo and su are still setuid here;
// every derived build strips them), ssh-keysign and ssh-agent stripped, no
// capability.
func TestBaseImageHardening(t *testing.T) {
	img := testutil.BaseImageOf(t, "base")
	if got := testutil.Docker(t, "run", "--rm", img, "id", "-u"); got != "1000" {
		t.Errorf("uid %s", got)
	}
	if out, err := exec.Command("docker", "run", "--rm", img, "sudo", "-n", "true").CombinedOutput(); err == nil {
		t.Errorf("passwordless sudo works: %s", out)
	}
	found := testutil.Docker(t, "run", "--rm", "--user", "0", img, "sh", "-c", "find / -xdev -perm /6000 -type f | sort")
	want := "/usr/bin/chage\n/usr/bin/chfn\n/usr/bin/chsh\n/usr/bin/expiry\n/usr/bin/gpasswd\n/usr/bin/mount\n/usr/bin/newgrp\n/usr/bin/passwd\n/usr/bin/su\n/usr/bin/sudo\n/usr/bin/umount\n/usr/sbin/unix_chkpwd"
	if found != want {
		t.Errorf("setuid/setgid files:\n%s\nwant:\n%s", found, want)
	}
	if caps := testutil.Docker(t, "run", "--rm", "--user", "0", img, "sh", "-c", "getcap -r / 2>/dev/null || true"); caps != "" {
		t.Errorf("file capabilities: %s", caps)
	}
	if pid1 := testutil.Docker(t, "run", "--rm", img, "sh", "-c", `tr "\000" " " </proc/1/cmdline`); !strings.HasPrefix(pid1, "/usr/bin/tini ") {
		t.Errorf("PID 1 is %q", pid1)
	}
}

// Every tools.tsv row answers with no network and an empty HOME, as the CI
// smoke runs it, and mise and the harness Node stay apart.
func TestBaseImageTools(t *testing.T) {
	img := testutil.BaseImageOf(t, "base")
	cmd := exec.Command("docker", "run", "--rm", "-i", "--network", "none", img, "fugaro", "image", "selftest")
	cmd.Stdin = strings.NewReader(`{"tools":"all","tools_only":true}`)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), `"passed":true`) {
		t.Fatalf("presence checks: %v\n%s", err, out)
	}
	if out, err := exec.Command("docker", "run", "--rm", img, "sh", "-c", "command -v node").CombinedOutput(); err == nil {
		t.Errorf("a node is on PATH in the bare base: %s", out)
	}
	if got := testutil.Docker(t, "run", "--rm", img, "mise", "settings", "get", "trusted_config_paths"); !strings.Contains(got, "/work/repo") {
		t.Errorf("mise does not trust /work/repo: %s", got)
	}
}
```

- [ ] **Step 2: Implement the helper**

In `internal/testutil/docker.go`, generalise `BaseImage`:

```go
var baseImages sync.Map // kind -> *baseBuild

type baseBuild struct {
	once sync.Once
	ref  string
	err  error
}

// BaseImageOf returns the base image images/<kind> built from this checkout
// with images/build-base.sh, once per test binary and kind, for the daemon's
// platform.
func BaseImageOf(t *testing.T, kind string) string {
	t.Helper()
	RequireDocker(t)
	v, _ := baseImages.LoadOrStore(kind, &baseBuild{})
	b := v.(*baseBuild)
	b.once.Do(func() {
		b.ref = "fugaro-" + kind + ":test"
		cmd := exec.Command("sh", filepath.Join(ModuleRoot(), "images", "build-base.sh"), kind, b.ref)
		cmd.Env = append(os.Environ(), "PLATFORM="+DockerPlatform(t))
		if out, err := cmd.CombinedOutput(); err != nil {
			b.err = fmt.Errorf("building %s: %v\n%s", kind, err, Tail(string(out)))
		}
	})
	if b.err != nil {
		t.Fatal(b.err)
	}
	return b.ref
}

// BaseImage is BaseImageOf(t, "web-node").
func BaseImage(t *testing.T) string { return BaseImageOf(t, "web-node") }
```

Keep whatever extra environment the current `BaseImage` body passes (copy it into `BaseImageOf`). In `.github/workflows/ci.yml`, raise the `docker-tests` job's `timeout-minutes` from 60 to 90, and its `-timeout 45m` to `-timeout 75m`. The base build adds about 10 to 15 minutes.

- [ ] **Step 3: Compile**

Run: `go vet -tags docker ./images/ ./internal/testutil/ ./internal/image/`
Expected: clean. **The tests run in CI's `docker-tests` job.** If `TestBaseImageHardening`'s list differs on Debian 13, the log prints the observed list. Update `want`, and the selftest allowlists (Task 12), to the observation, keeping `sudo` and `su` out of the selftest's set.

- [ ] **Step 4: Commit**

```bash
git add internal/testutil/docker.go images/base_docker_test.go .github/workflows/ci.yml
git commit -m "base image task 7: Docker tests for the base's hardening and tools"
```

---

### Task 8: The monthly bump

A Fugaro run writes the script and tests its rewriting offline. **The real bump runs in GitHub Actions.**

**Files:**
- Create: `images/base/bump.sh`, `.github/workflows/base-bump.yml`, `images/bump_test.go`
- Modify: `.github/dependabot.yml` (the comment, and an npm entry for the harness lockfile)

**Interfaces:**
- Produces:
  - `bump.sh [--offline FILE]`, which rewrites the `ARG` pins of `images/base/Dockerfile` and prints a Markdown table of each change. `--offline FILE` reads `NAME=value` lines instead of resolving releases (for tests).
  - The Trivy digest (`images/scan.sh`) and the `# syntax` frontend digest stay hand-bumped, as `.github/dependabot.yml`'s comment says. The bump PR's body reminds the reviewer of them.
  - The workflow `base-bump`: on the 3rd at 04:17 UTC and on dispatch, it builds and smokes both architectures, then opens one PR.

- [ ] **Step 1: Write the failing test**

`images/bump_test.go`:

```go
package images_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bump.sh --offline rewrites exactly the named ARG lines and reports each.
func TestBumpRewritesOnlyArgLines(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"base/Dockerfile", "base/bump.sh", "scan.sh"} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(root, "images", f)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pins := filepath.Join(root, "pins")
	if err := os.WriteFile(pins, []byte("MISE_VERSION=2026.11.1\nMISE_SHA256_AMD64="+strings.Repeat("a", 64)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", filepath.Join(root, "images/base/bump.sh"), "--offline", pins)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	before, _ := os.ReadFile("base/Dockerfile")
	after, _ := os.ReadFile(filepath.Join(root, "images/base/Dockerfile"))
	if !strings.Contains(string(after), "ARG MISE_VERSION=2026.11.1\n") || !strings.Contains(string(after), "ARG MISE_SHA256_AMD64="+strings.Repeat("a", 64)+"\n") {
		t.Errorf("the pins were not rewritten:\n%s", after)
	}
	changed := 0
	b, a := strings.Split(string(before), "\n"), strings.Split(string(after), "\n")
	if len(a) != len(b) {
		t.Fatalf("line count %d -> %d", len(b), len(a))
	}
	for i := range a {
		if a[i] != b[i] {
			changed++
			if !strings.HasPrefix(a[i], "ARG ") {
				t.Errorf("a non-ARG line changed: %q", a[i])
			}
		}
	}
	if changed != 2 || !strings.Contains(string(out), "| MISE_VERSION | 2026.10.4 | 2026.11.1 |") {
		t.Errorf("changed %d lines; summary:\n%s", changed, out)
	}
	if err := exec.Command("sh", filepath.Join(root, "images/base/bump.sh"), "--offline", filepath.Join(root, "nope")).Run(); err == nil {
		t.Error("a missing pins file passed")
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./images/ -run TestBumpRewritesOnlyArgLines -count=1`
Expected: FAIL, `open base/bump.sh: no such file or directory`.

- [ ] **Step 3: Implement**

`images/base/bump.sh`:

```sh
#!/bin/sh
# bump.sh resolves the latest release of every component images/base/Dockerfile
# pins (design base-image.md section 7), downloads each to compute its sha256,
# and rewrites the ARG lines, then prints a Markdown table of the changes for
# the pull request. bump.sh --offline FILE takes NAME=value lines from FILE
# instead (tests). It changes ARG lines only; the harness lockfile is
# refreshed by the workflow with npm. Run it from the repository root.
set -eu
df=images/base/Dockerfile
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
pins=$tmp/pins
if [ "${1:-}" = --offline ]; then
  [ -f "${2:-}" ] || { echo "bump: --offline needs a pins file" >&2; exit 1; }
  cp "$2" "$pins"
else
  : > "$pins"
  # debian:trixie-slim's index digest, read anonymously from Docker Hub.
  tok=$(curl -fsSL "https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/debian:pull" | jq -r .token)
  d=$(curl -fsSI -H "Authorization: Bearer $tok" -H 'Accept: application/vnd.oci.image.index.v1+json' \
    https://registry-1.docker.io/v2/library/debian/manifests/trixie-slim | tr -d '\r' | sed -n 's/^[Dd]ocker-[Cc]ontent-[Dd]igest: //p')
  printf 'DEBIAN_DIGEST=%s\n' "$d" >> "$pins"
  gh_latest() { curl -fsSL "https://api.github.com/repos/$1/releases/latest" | jq -r .tag_name; }
  sum_of() { curl -fsSL --proto '=https' "$1" | sha256sum | cut -d' ' -f1; }
  add() { printf '%s=%s\n' "$1" "$2" >> "$pins"; }
  v=$(gh_latest jdx/mise); v=${v#v}; add MISE_VERSION "$v"
  add MISE_SHA256_AMD64 "$(sum_of "https://github.com/jdx/mise/releases/download/v$v/mise-v$v-linux-x64.tar.gz")"
  add MISE_SHA256_ARM64 "$(sum_of "https://github.com/jdx/mise/releases/download/v$v/mise-v$v-linux-arm64.tar.gz")"
  v=$(gh_latest cli/cli); v=${v#v}; add GH_VERSION "$v"
  add GH_SHA256_AMD64 "$(sum_of "https://github.com/cli/cli/releases/download/v$v/gh_${v}_linux_amd64.tar.gz")"
  add GH_SHA256_ARM64 "$(sum_of "https://github.com/cli/cli/releases/download/v$v/gh_${v}_linux_arm64.tar.gz")"
  v=$(gh_latest mikefarah/yq); v=${v#v}; add YQ_VERSION "$v"
  add YQ_SHA256_AMD64 "$(sum_of "https://github.com/mikefarah/yq/releases/download/v$v/yq_linux_amd64")"
  add YQ_SHA256_ARM64 "$(sum_of "https://github.com/mikefarah/yq/releases/download/v$v/yq_linux_arm64")"
  v=$(curl -fsSL https://dl.google.com/dl/cloudsdk/channels/rapid/components-2.json | jq -r .version); add GCLOUD_VERSION "$v"
  add GCLOUD_SHA256_AMD64 "$(sum_of "https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-cli-$v-linux-x86_64.tar.gz")"
  add GCLOUD_SHA256_ARM64 "$(sum_of "https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-cli-$v-linux-arm.tar.gz")"
  v=$(curl -fsSL https://download.docker.com/linux/static/stable/x86_64/ | grep -o 'docker-[0-9.]*\.tgz' | sed 's/docker-//; s/\.tgz//' | sort -V | tail -n 1); add DOCKER_CLI_VERSION "$v"
  add DOCKER_CLI_SHA256_AMD64 "$(sum_of "https://download.docker.com/linux/static/stable/x86_64/docker-$v.tgz")"
  add DOCKER_CLI_SHA256_ARM64 "$(sum_of "https://download.docker.com/linux/static/stable/aarch64/docker-$v.tgz")"
  v=$(curl -fsSL https://nodejs.org/dist/index.json | jq -r '[.[] | select(.lts != false)][0].version'); add HARNESS_NODE_VERSION "${v#v}"
  v=$(gh_latest openai/codex); v=${v#rust-v}; add CODEX_VERSION "$v"
  add CODEX_SHA256_AMD64 "$(sum_of "https://github.com/openai/codex/releases/download/rust-v$v/codex-x86_64-unknown-linux-musl.tar.gz")"
  add CODEX_SHA256_ARM64 "$(sum_of "https://github.com/openai/codex/releases/download/rust-v$v/codex-aarch64-unknown-linux-musl.tar.gz")"
  v=$(gh_latest sst/opencode); v=${v#v}; add OPENCODE_VERSION "$v"
  add OPENCODE_SHA256_AMD64 "$(sum_of "https://github.com/sst/opencode/releases/download/v$v/opencode-linux-x64.tar.gz")"
  add OPENCODE_SHA256_ARM64 "$(sum_of "https://github.com/sst/opencode/releases/download/v$v/opencode-linux-arm64.tar.gz")"
  v=$(gh_latest block/goose); v=${v#v}; add GOOSE_VERSION "$v"
  add GOOSE_SHA256_AMD64 "$(sum_of "https://github.com/block/goose/releases/download/v$v/goose-x86_64-unknown-linux-gnu.tar.bz2")"
  add GOOSE_SHA256_ARM64 "$(sum_of "https://github.com/block/goose/releases/download/v$v/goose-aarch64-unknown-linux-gnu.tar.bz2")"
  v=$(gh_latest charmbracelet/crush); v=${v#v}; add CRUSH_VERSION "$v"
  add CRUSH_SHA256_AMD64 "$(sum_of "https://github.com/charmbracelet/crush/releases/download/v$v/crush_${v}_Linux_x86_64.tar.gz")"
  add CRUSH_SHA256_ARM64 "$(sum_of "https://github.com/charmbracelet/crush/releases/download/v$v/crush_${v}_Linux_arm64.tar.gz")"
  # Claude Code: the release's own manifest carries the per-platform sums.
  v=$(curl -fsSL https://downloads.claude.ai/claude-code-releases/stable); add CLAUDE_CODE_VERSION "$v"
  m=$(curl -fsSL "https://downloads.claude.ai/claude-code-releases/$v/manifest.json")
  add CLAUDE_CODE_SHA256_AMD64 "$(printf '%s' "$m" | jq -r '.platforms["linux-x64"].checksum')"
  add CLAUDE_CODE_SHA256_ARM64 "$(printf '%s' "$m" | jq -r '.platforms["linux-arm64"].checksum')"
fi
echo "| Pin | Was | Now |"
echo "|---|---|---|"
while IFS='=' read -r name value; do
  [ -n "$name" ] || continue
  case "$name" in *[!A-Z0-9_]*) echo "bump: bad pin name $name" >&2; exit 1 ;; esac
  case "$value" in *[!A-Za-z0-9.+_:-]*|"") echo "bump: bad value for $name" >&2; exit 1 ;; esac
  old=$(sed -n "s/^ARG $name=\(.*\)$/\1/p" "$df")
  [ -n "$old" ] || { echo "bump: $df has no ARG $name" >&2; exit 1; }
  [ "$old" = "$value" ] && continue
  sed -i.bak "s/^ARG $name=.*$/ARG $name=$value/" "$df" && rm -f "$df.bak"
  echo "| $name | $old | $value |"
done < "$pins"
```

The `stable` pointer (2.1.286 on 2026-10-08) and `manifest.json`'s `platforms["linux-x64"].checksum` were checked on 2026-10-08. They are what `https://claude.ai/install.sh` reads. If either changes shape, the job fails with curl's or jq's error and opens no PR.

`.github/workflows/base-bump.yml`:

```yaml
name: base-bump
# The monthly rebuild-and-bump of the Fugaro base image (design
# base-image.md section 7): resolves every pinned component's latest
# release, rewrites the pins, refreshes the harness lockfile, builds and
# smoke-tests both architectures, and opens one pull request. It never
# merges. A pull request opened with GITHUB_TOKEN starts no other workflow,
# so the body asks the reviewer to close and reopen it, which runs CI.
on:
  schedule:
    - cron: "17 4 3 * *"
  workflow_dispatch:
permissions:
  contents: read
jobs:
  bump:
    permissions:
      contents: write
      pull-requests: write
    runs-on: ubuntu-latest
    timeout-minutes: 30
    outputs:
      changed: ${{ steps.bump.outputs.changed }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - id: bump
        run: |
          set -eu
          images/base/bump.sh > /tmp/summary.md
          (cd images/base/harnesses && npm update --package-lock-only --ignore-scripts --no-audit --no-fund)
          if git diff --quiet; then echo "changed=false" >> "$GITHUB_OUTPUT"; else echo "changed=true" >> "$GITHUB_OUTPUT"; fi
          git diff > /tmp/bump.patch
      - uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
        if: steps.bump.outputs.changed == 'true'
        with:
          name: bump
          path: |
            /tmp/bump.patch
            /tmp/summary.md
  build:
    needs: bump
    if: needs.bump.outputs.changed == 'true'
    permissions:
      contents: read
    strategy:
      fail-fast: false
      matrix:
        include:
          - arch: amd64
            runner: ubuntu-latest
          - arch: arm64
            runner: ubuntu-24.04-arm
    runs-on: ${{ matrix.runner }}
    timeout-minutes: 90
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - uses: actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8.0.1
        with:
          name: bump
          path: /tmp/bump
      - run: git apply /tmp/bump/bump.patch
      - run: sudo rm -rf /usr/share/dotnet /usr/local/lib/android /opt/ghc /opt/hostedtoolcache/CodeQL
      - env:
          ARCH: ${{ matrix.arch }}
        run: PLATFORM="linux/$ARCH" images/build-base.sh base fugaro-base:bump
      - run: images/smoke.sh fugaro-base:bump base
      - run: images/size-budget.sh fugaro-base:bump
      - run: images/scan.sh --secrets fugaro-base:bump
  open:
    needs: [bump, build]
    permissions:
      contents: write
      pull-requests: write
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8.0.1
        with:
          name: bump
          path: /tmp/bump
      - env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        run: |
          set -eu
          branch="base-bump-$(date -u +%Y-%m)"
          git switch -c "$branch"
          git apply /tmp/bump/bump.patch
          git -c user.name="github-actions[bot]" -c user.email="41898282+github-actions[bot]@users.noreply.github.com" \
            commit -am "base image: the monthly bump ($(date -u +%Y-%m))"
          git push origin "$branch"
          {
            echo "The monthly bump of the Fugaro base image's pins (docs/design/base-image.md, section 7)."
            echo "Both architectures were built, smoke-tested, held to the size budget and scanned for secrets in the base-bump run."
            echo
            cat /tmp/bump/summary.md
            echo
            echo "Also due monthly, by hand: the Trivy digest in images/scan.sh and the # syntax frontend digest (images/base/Dockerfile, images/derived/Dockerfile.tmpl)."
            echo
            echo "This pull request was opened with GITHUB_TOKEN, which starts no other workflow: close and reopen it to run CI. Read each changed component's release notes before merging. Never merged automatically."
          } > /tmp/body.md
          gh pr create --base main --head "$branch" --title "base image: the monthly bump ($(date -u +%Y-%m))" --body-file /tmp/body.md
```

`.github/dependabot.yml`:
- the comment now names `images/base/Dockerfile` and `images/derived/Dockerfile.tmpl` for the `# syntax` digest, and says `images/base/bump.sh` covers the base's pins;
- add an `npm` entry for `/images/base/harnesses`, `interval: monthly`.

- [ ] **Step 4: Run the tests**

Run: `go test ./images/ -run 'TestBump|TestWorkflowActionsPinnedBySHA|TestCIWorkflow' -count=1`
Expected: PASS. If `TestCIWorkflowPermissionsScoped` or `TestCIWorkflowCheckoutsDropCredentials` lists the workflows by name, add `base-bump.yml` to its list. The `open` job's checkout keeps credentials, because it pushes the branch: add it to that test's allowed exceptions with the comment "pushes the bump branch".

- [ ] **Step 5: Run the group's full suite and open the PR**

Run: `go test ./... 2>&1 | tail -30` (foreground; about 25 minutes).
Expected: PASS.

Push the branch and open the PR (title: "Base image: one Debian 13 base with mise and the harnesses (group 1 of 4)"). The body lists what CI verifies that a Fugaro run could not: the base build on both architectures, the Docker tests, the secret scan and the real bump. Read every CI job's log before merging: `fugaro-base (amd64)`, `fugaro-base (arm64)`, `docker-tests`, `test`, `rules`, `terraform`. A secret-scan finding is triaged in this PR: an allowance in `images/trivy-secret.yaml` with its reason for a fixture, or a fix for a real secret.

```bash
git add images/base/bump.sh .github/workflows/base-bump.yml images/bump_test.go .github/dependabot.yml
git commit -m "base image task 8: the monthly bump opens a PR, never merges"
```

---
## Group 2: Go and config, additive (branch `base-kind`)

### Task 9: The `base` kind, `image.tools` and mise detection (`internal/config`)

**Files:**
- Create: `internal/config/mise.go`, `internal/config/mise_test.go`, `testdata/config/valid/base-tools.yaml`, `testdata/config/invalid/image-tools-bad-version.yaml`, `testdata/config/invalid/image-tools-on-web-node.yaml`
- Modify: `internal/config/config.go`, `defaults.go`, `image.go`, `validate.go`, `image_test.go`, `config_test.go`, `schemas/fugaro.schema.json`, `internal/cli/validate.go`

**Interfaces:**
- Produces:
  ```go
  const BaseKind = "base"
  const MigrationDoc = "docs/base-image-migration.md"
  var Bases = []string{BaseKind, "go", "java-services", "web-node"} // Task 17: []string{BaseKind}
  func (w Workflow) BaseKind() string                               // w.Base, or BaseKind when empty
  // Image gains
  Tools map[string]string `yaml:"tools"`
  func ValidMiseTool(name, version string) bool
  var MiseConfigPaths = []string{"mise.toml", ".mise.toml", "mise/config.toml", ".mise/config.toml", ".config/mise.toml", ".config/mise/config.toml", ".tool-versions"}
  var MiseConfigDirs = []string{"mise/conf.d", ".mise/conf.d", ".config/mise/conf.d"}
  const MiseLockfile = "mise.lock"
  func MiseConfigFiles(root string) ([]string, error)
  func CheckWarnings(c *Config, root string) []Problem
  ```

- [ ] **Step 1: Write the failing tests**

`internal/config/mise_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestMiseConfigFiles(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{
		"mise.toml":                    "[tools]\nnode = \"24\"\n",
		".tool-versions":               "python 3.12\n",
		".config/mise/conf.d/a.toml":   "[tools]\ngo = \"1.27\"\n",
		".config/mise/conf.d/.hidden.toml": "",
		"mise.local.toml":              "[tools]\nnode = \"22\"\n", // never committed: not read
		"mise.lock":                    "",                         // pins, declares nothing
		"web/mise.toml":                "",                         // a subdirectory: not the root's
	})
	if err := os.Symlink("mise.toml", filepath.Join(root, ".mise.toml")); err != nil {
		t.Fatal(err)
	}
	got, err := MiseConfigFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".config/mise/conf.d/a.toml", ".tool-versions", "mise.toml"}
	if !slices.Equal(got, want) {
		t.Fatalf("MiseConfigFiles = %v, want %v", got, want)
	}
	if got, err := MiseConfigFiles(t.TempDir()); err != nil || len(got) != 0 {
		t.Fatalf("empty checkout: %v %v", got, err)
	}
}

func TestBaseKind(t *testing.T) {
	if got := (Workflow{}).BaseKind(); got != BaseKind {
		t.Errorf("no base: = %q", got)
	}
	if got := (Workflow{Base: "go"}).BaseKind(); got != "go" {
		t.Errorf("base: go = %q", got)
	}
}

const baseYAML = `version: 1
project: acme
git: { provider: github }
workflows:
  app:
    commands: { build: make, test: make test }
`

func TestWorkflowWithoutBaseGetsUniformDefaults(t *testing.T) {
	c, problems := Parse([]byte(baseYAML))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	w := c.Workflows["app"]
	if w.Resources.CPU != 4 || w.Resources.Memory != "8Gi" ||
		!slices.Equal(w.Commands.Reports, []string{"**/junit*.xml", "**/build/test-results/**/*.xml"}) {
		t.Errorf("defaults: %+v %v", w.Resources, w.Commands.Reports)
	}
}

func TestValidateImageTools(t *testing.T) {
	for _, tc := range []struct {
		image, want string // want: "" for valid, else a substring of the problem
	}{
		{`{ tools: { node: "24.19.0", python: "3.12", "npm:firebase-tools": "15.32.1", java: temurin-25 } }`, ""},
		{`{ tools: { "Node!": "24" } }`, "image.tools.Node!"},
		{`{ tools: { node: "24; rm -rf /" } }`, "must be a version"},
		{`{ tools: { node: "'24'" } }`, "must be a version"},
	} {
		y := strings.Replace(baseYAML, "    commands:", "    image: "+tc.image+"\n    commands:", 1)
		_, problems := Parse([]byte(y))
		got := ""
		for _, p := range problems {
			got += p.String() + "\n"
		}
		if (tc.want == "") != (got == "") || (tc.want != "" && !strings.Contains(got, tc.want)) {
			t.Errorf("%s: problems %q, want %q", tc.image, got, tc.want)
		}
	}
	y := strings.Replace(baseYAML, "    commands:", "    base: web-node\n    image: { tools: { node: \"24\" } }\n    commands:", 1)
	if _, problems := Parse([]byte(y)); len(problems) == 0 || !strings.Contains(problems[0].Message, "the Fugaro base only") {
		t.Errorf("tools on web-node: %v", problems)
	}
}

func TestValidateRefusesToolsWithARepositoryMiseConfig(t *testing.T) {
	y := strings.Replace(baseYAML, "    commands:", "    image: { tools: { node: \"24\" } }\n    commands:", 1)
	c, problems := Parse([]byte(y))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{"mise.toml": "[tools]\nnode = \"22\"\n"})
	ps := Check(c, root)
	if len(ps) != 1 || ps[0].Path != "workflows.app.image.tools" || !strings.Contains(ps[0].Message, "mise.toml") {
		t.Fatalf("Check = %v", ps)
	}
	if ps := Check(c, t.TempDir()); len(ps) != 0 {
		t.Fatalf("tools alone: %v", ps)
	}
}

func TestCheckWarningsNoRuntime(t *testing.T) {
	c, _ := Parse([]byte(baseYAML))
	ws := CheckWarnings(c, t.TempDir())
	if len(ws) != 1 || !strings.Contains(ws[0].Message, "installs no language runtime") {
		t.Fatalf("CheckWarnings = %v", ws)
	}
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{".tool-versions": "nodejs 24.19.0\n"})
	if ws := CheckWarnings(c, root); len(ws) != 0 {
		t.Fatalf("with .tool-versions: %v", ws)
	}
}

func TestSkipBuildScriptsOnTheBaseNeedsANodeWarmUp(t *testing.T) {
	y := strings.Replace(baseYAML, "    commands:", "    image: { skip_build_scripts: true }\n    commands:", 1)
	c, problems := Parse([]byte(y))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if ps := Check(c, t.TempDir()); len(ps) != 1 || !strings.Contains(ps[0].Message, "package.json and a lockfile") {
		t.Fatalf("no package.json: %v", ps)
	}
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{"package.json": `{"name":"x"}`, "package-lock.json": `{"lockfileVersion":3}`, "mise.toml": "[tools]\nnode = \"24\"\n"})
	if ps := Check(c, root); len(ps) != 0 {
		t.Fatalf("with npm: %v", ps)
	}
}
```

`testdata/config/valid/base-tools.yaml`:

```yaml
version: 1
project: acme
git: { provider: github, base_branch: main }
workflows:
  app:
    image:
      tools: { node: "24.19.0", python: "3.12", "npm:firebase-tools": "15.32.1", java: "temurin-25" }
      apt: [postgresql-17, redis-server]
    commands: { build: npm run build, test: fugaro-services start && npm test }
```

`testdata/config/invalid/image-tools-bad-version.yaml`: the same, with `node: "24 && curl x"`. `testdata/config/invalid/image-tools-on-web-node.yaml`: the same, with `base: web-node` above `image:`.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/config/ ./schemas/ -run 'TestMiseConfigFiles|TestBaseKind|TestWorkflowWithoutBase|TestValidateImageTools|TestValidateRefusesTools|TestCheckWarnings|TestSkipBuildScriptsOnTheBase|TestFugaroSchemaCorpus' -count=1`
Expected: build failure, `undefined: MiseConfigFiles`, then (once it compiles) `workflows.app.base: must be one of go, java-services, web-node`.

- [ ] **Step 3: Implement**

`internal/config/defaults.go`:

```go
// BaseKind is the one Fugaro base image's kind (design base-image.md): what a
// workflow without base: builds on, and its key in the local config's
// base_images.
const BaseKind = "base"

// MigrationDoc is the guide every refusal of a removed base setting names.
const MigrationDoc = "docs/base-image-migration.md"

// Bases are the base kinds: BaseKind and, until the 0.7.0 cut, the legacy
// kinds a workflow's base: may still name.
var Bases = []string{BaseKind, "go", "java-services", "web-node"}
```

Add `BaseKind: {reports: []string{"**/junit*.xml", "**/build/test-results/**/*.xml"}, cpu: 4, memory: "8Gi"},` to `baseDefaults`. In `applyDefaults`, look the defaults up with `baseDefaults[w.BaseKind()]`.

`internal/config/config.go`, in `Workflow`:

```go
	// Base is a legacy base kind (go, java-services, web-node); empty means
	// the one Fugaro base. Read it through BaseKind.
	Base string `yaml:"base"`
```

and after the struct:

```go
// BaseKind is the base kind w builds on: its legacy base:, or BaseKind.
func (w Workflow) BaseKind() string {
	if w.Base != "" {
		return w.Base
	}
	return BaseKind
}
```

In `Image`, after `SkipBuildScripts`:

```go
	// Tools are mise tools, name to version, written to the image's global
	// mise config for a repository without a mise config of its own (design
	// base-image.md section 4). The Fugaro base only.
	Tools map[string]string `yaml:"tools"`
```

`IsZero` adds `&& len(i.Tools) == 0`.

`internal/config/image.go`:

```go
var (
	miseToolRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:@-]*$`)
	miseVersionRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~:-]*$`)
)

// ValidMiseTool reports whether name and version may be written into a mise
// config: nothing outside these characters ever reaches a Dockerfile.
func ValidMiseTool(name, version string) bool {
	return miseToolRE.MatchString(name) && miseVersionRE.MatchString(version)
}
```

In `validateImage`, after the `JDK` check:

```go
	if len(img.Tools) > 0 && w.BaseKind() != BaseKind {
		add(p+".image.tools", "applies to the Fugaro base only: remove base: %s (%s)", w.Base, MigrationDoc)
	}
	for _, name := range sortedKeys(img.Tools) {
		tp := p + ".image.tools." + name
		switch v := img.Tools[name]; {
		case !miseToolRE.MatchString(name):
			add(tp, "must be a mise tool name such as node, python or npm:firebase-tools")
		case !miseVersionRE.MatchString(v):
			add(tp, "must be a version such as 24.19.0, 3.12 or temurin-25")
		}
	}
```

and `skip_build_scripts` becomes `if img.SkipBuildScripts && w.BaseKind() != "web-node" && w.BaseKind() != BaseKind { add(…, "only applies to base web-node or the Fugaro base") }`.

`internal/config/mise.go`:

```go
package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// MiseConfigPaths are the root mise config files a derived image installs
// from and its hash covers (design base-image.md section 4). mise.local.toml
// is never committed, and idiomatic version files are off in the base.
var MiseConfigPaths = []string{"mise.toml", ".mise.toml", "mise/config.toml", ".mise/config.toml",
	".config/mise.toml", ".config/mise/config.toml", ".tool-versions"}

// MiseConfigDirs hold the conf.d fragments (*.toml) mise also reads.
var MiseConfigDirs = []string{"mise/conf.d", ".mise/conf.d", ".config/mise/conf.d"}

// MiseLockfile pins the configs' versions; the hash covers it, and it
// declares no tool.
const MiseLockfile = "mise.lock"

// MiseConfigFiles are the root mise config files of the checkout at root, as
// sorted slash-separated relative paths: regular files only (a link is not
// followed), the lockfile and hidden conf.d fragments left out.
func MiseConfigFiles(root string) ([]string, error) {
	var out []string
	for _, rel := range MiseConfigPaths {
		switch fi, err := os.Lstat(filepath.Join(root, rel)); {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return nil, err
		case fi.Mode().IsRegular():
			out = append(out, rel)
		}
	}
	for _, dir := range MiseConfigDirs {
		matches, err := filepath.Glob(filepath.Join(root, dir, "*.toml"))
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			if fi, err := os.Lstat(m); err != nil || !fi.Mode().IsRegular() || strings.HasPrefix(filepath.Base(m), ".") {
				continue
			}
			rel, err := filepath.Rel(root, m)
			if err != nil {
				return nil, err
			}
			out = append(out, filepath.ToSlash(rel))
		}
	}
	slices.Sort(out)
	return out, nil
}
```

`internal/config/validate.go`:
- The base check: `if w.Base != "" && !slices.Contains(Bases, w.Base) { add(p+".base", "must be one of %s, or left out for the Fugaro base", strings.Join(Bases[1:], ", ")) }`.
- In `Check`, replace the `if w.Dockerfile != "" … else if w.Base == "web-node" …` with:

```go
		switch {
		case w.Dockerfile != "":
			ps = append(ps, checkDockerfile(p, root, w)...)
		case w.BaseKind() == "web-node":
			// The generated image's warm-up needs a package manager it can name.
			if _, err := DetectNodePM(root); err != nil {
				ps = append(ps, Problem{Path: p, Message: err.Error()})
			}
		case w.BaseKind() == BaseKind:
			ps = append(ps, checkBaseWorkflow(p, root, w)...)
		}
```

  with

```go
// checkBaseWorkflow is Check for a generated image on the Fugaro base: one
// source of tools, and a package manager for image.skip_build_scripts.
func checkBaseWorkflow(p, root string, w Workflow) []Problem {
	var ps []Problem
	files, err := MiseConfigFiles(root)
	switch {
	case err != nil:
		ps = append(ps, Problem{Path: p, Message: "reading the repository's mise config: " + err.Error()})
	case len(files) > 0 && len(w.Image.Tools) > 0:
		ps = append(ps, Problem{Path: p + ".image.tools", Message: "the repository has its own mise config (" + strings.Join(files, ", ") +
			"): keep one; the repository's file, which developers use too, is the usual choice"})
	}
	pm, err := DetectNodePM(root)
	switch {
	case err != nil:
		ps = append(ps, Problem{Path: p, Message: err.Error()})
	case pm == nil && w.Image.SkipBuildScripts:
		ps = append(ps, Problem{Path: p + ".image.skip_build_scripts", Message: "applies only when the repository has a package.json and a lockfile (the Node warm-up)"})
	}
	return ps
}

// CheckWarnings are what Check finds that doesn't make the config wrong, for
// fugaro validate to print as warnings: a generated image on the Fugaro base
// with no mise config and no image.tools installs no language runtime.
func CheckWarnings(c *Config, root string) []Problem {
	var ps []Problem
	for _, name := range sortedKeys(c.Workflows) {
		w := c.Workflows[name]
		if w.BaseKind() != BaseKind || w.Dockerfile != "" || len(w.Image.Tools) > 0 {
			continue
		}
		if files, err := MiseConfigFiles(root); err == nil && len(files) == 0 {
			ps = append(ps, Problem{Path: "workflows." + name, Message: "no mise.toml, .tool-versions or image.tools: the image installs no language runtime (docs/base-image.md)"})
		}
	}
	return ps
}
```

`internal/cli/validate.go`: right after `problems, warnings = append(problems, bp...), append(bw, rw...)`, add `warnings = append(warnings, config.CheckWarnings(cfg, filepath.Dir(path))...)`.

`schemas/fugaro.schema.json`, in `workflow`:
- `"required": ["commands"]`;
- `"base": { "enum": ["base", "go", "java-services", "web-node"] }`;
- in `image.properties`:

```json
            "tools": {
              "type": "object",
              "propertyNames": { "pattern": "^[a-z0-9][a-z0-9._/:@-]*$" },
              "additionalProperties": { "type": "string", "pattern": "^[A-Za-z0-9][A-Za-z0-9._+~:-]*$" }
            },
```

- and to `allOf`:

```json
        {
          "if": { "required": ["base"], "properties": { "base": { "enum": ["go", "java-services", "web-node"] } } },
          "then": { "properties": { "image": { "not": { "required": ["tools"] } } } }
        }
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/config/ ./schemas/ -count=1` then `go test ./internal/cli/ -run 'TestValidate' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config schemas/fugaro.schema.json testdata/config internal/cli/validate.go
git commit -m "base kind task 9: the base kind, image.tools and mise detection"
```

---

### Task 10: The derived template installs the project's tools

**Files:**
- Modify: `images/derived/Dockerfile.tmpl`, `internal/image/render.go`, `internal/image/render_test.go`, `internal/config/nodepm.go` (`DefaultCache`)

**Interfaces:**
- Consumes: `config.MiseConfigFiles`, `config.ValidMiseTool`, `Workflow.BaseKind`.
- Produces: `RenderInput.Mise bool`; the template data's `ToolLines []string`; `Dockerfile` setting `Mise` and the warm-up for the `base` kind; `config.DefaultCache` serving `BaseKind` as well as `web-node`.

- [ ] **Step 1: Write the failing tests** (append to `internal/image/render_test.go`)

```go
func TestRenderMiseInstallWhenTheCheckoutHasAConfig(t *testing.T) {
	pm := config.NodePM{Name: "npm", Lockfile: "package-lock.json", Install: "npm ci"}
	got, err := Render(RenderInput{Workflow: "app", Base: config.BaseKind, Mise: true, PM: &pm, Secrets: []string{"NPM_TOKEN"}, Version: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	step := `RUN --mount=type=cache,id=fugaro-mise,target=/home/fugaro/.cache/mise,uid=1000,gid=1000 --mount=type=secret,id=NPM_TOKEN,uid=1000,mode=0400,required=false if test -e /run/secrets/NPM_TOKEN; then NPM_TOKEN="$(cat /run/secrets/NPM_TOKEN)" || exit 1; export NPM_TOKEN; fi; mise install \` + "\n" +
		` && missing="$(mise ls --current --missing)" \` + "\n" +
		` && if [ -n "$missing" ]; then printf 'mise left these tools missing:\n%s\n' "$missing" >&2; exit 1; fi`
	i, clone, warm := strings.Index(s, step), strings.Index(s, `git remote set-url origin -- "$REPO_ORIGIN"`), strings.Index(s, "# Dependency warm-up for package-lock.json.")
	if i < 0 || clone < 0 || warm < 0 || !(clone < i && i < warm) {
		t.Fatalf("the mise step is missing or out of order (clone %d, mise %d, warm-up %d):\n%s", clone, i, warm, s)
	}
	if msgs := config.LintDockerfile(got, config.BaseKind); len(msgs) > 0 {
		t.Errorf("the rendered Dockerfile breaks the contract: %v", msgs)
	}
}

func TestRenderNoMiseStepWithoutAConfig(t *testing.T) {
	got, err := Render(RenderInput{Workflow: "app", Base: config.BaseKind, Version: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "mise install") || strings.Contains(string(got), ".config/mise") {
		t.Errorf("a mise step without a mise config or image.tools:\n%s", got)
	}
}

func TestRenderImageToolsBeforeTheClone(t *testing.T) {
	got, err := Render(RenderInput{Workflow: "app", Base: config.BaseKind, Mise: true, Version: "dev",
		Image: config.Image{Tools: map[string]string{"node": "24.19.0", "npm:firebase-tools": "15.32.1"}}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	line := `printf '%s\n' '[tools]' '"node" = "24.19.0"' '"npm:firebase-tools" = "15.32.1"' > /home/fugaro/.config/mise/config.toml`
	if i, clone := strings.Index(s, line), strings.Index(s, "clone --quiet"); i < 0 || i > clone {
		t.Fatalf("image.tools is not written before the clone:\n%s", s)
	}
}

func TestRenderRefusesABadTool(t *testing.T) {
	for _, tools := range []map[string]string{{"node": "24'; rm -rf / #"}, {"no de": "24"}} {
		if _, err := Render(RenderInput{Workflow: "app", Base: config.BaseKind, Mise: true, Version: "dev", Image: config.Image{Tools: tools}}); err == nil {
			t.Errorf("rendered %v", tools)
		}
	}
}

func TestDockerfileOnTheBaseDetectsMiseAndNode(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n", "package.json": `{"name":"x"}`, "package-lock.json": `{"lockfileVersion":3}`})
	cfg, problems := config.Parse([]byte("version: 1\nproject: acme\ngit: { provider: github }\nworkflows:\n  app: { commands: { build: npm run build, test: npm test } }\n"))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	got, repoFile, err := Dockerfile(root, cfg, "app", "dev")
	if err != nil || repoFile != "" {
		t.Fatalf("%v %q", err, repoFile)
	}
	for _, want := range []string{"mise install", "# Dependency warm-up for package-lock.json.", "npm ci"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("lacks %q", want)
		}
	}
	if strings.Contains(string(got), "install-node") {
		t.Error("the base installs Node through install-node")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/image/ -run 'TestRenderMise|TestRenderNoMise|TestRenderImageTools|TestRenderRefusesABadTool|TestDockerfileOnTheBase' -count=1`
Expected: build failure, `unknown field Mise in struct literal of type RenderInput`.

- [ ] **Step 3: Implement**

`images/derived/Dockerfile.tmpl`. After the `{{- if or .Image.Apt .Image.Node}} … {{- end}}` block, insert:

```
{{- if .ToolLines}}

# image.tools: the workflow's mise tools, as the image's global mise config
# (design base-image.md section 4). A mise.toml in the repository is the
# alternative; fugaro validate refuses both.
RUN mkdir -p /home/fugaro/.config/mise \
 && printf '%s\n' '[tools]'{{range .ToolLines}} '{{.}}'{{end}} > /home/fugaro/.config/mise/config.toml
{{- end}}
```

After the clone `RUN` (the line ending `&& git remote set-url origin -- "$REPO_ORIGIN"`), insert:

```
{{- if .Mise}}

# The runtimes: the repository's root mise config and image.tools, installed
# as fugaro into ~/.local/share/mise (design base-image.md section 4). The
# download cache survives local rebuilds; nothing may be left missing.
RUN --mount=type=cache,id=fugaro-mise,target=/home/fugaro/.cache/mise,uid=1000,gid=1000{{.SecretMounts}} {{.SecretEnv}}mise install \
 && missing="$(mise ls --current --missing)" \
 && if [ -n "$missing" ]; then printf 'mise left these tools missing:\n%s\n' "$missing" >&2; exit 1; fi
{{- end}}
```

The build-arguments comment at the top gains "mise's download cache (fugaro-mise) is a BuildKit cache mount".

`internal/image/render.go`:
- `RenderInput` gains `Mise bool // install the checkout's mise config and image.tools`.
- The anonymous data struct gains `ToolLines []string`.
- In `Render`, before executing the template:

```go
	for _, name := range slices.Sorted(maps.Keys(in.Image.Tools)) {
		v := in.Image.Tools[name]
		if !config.ValidMiseTool(name, v) {
			return nil, fmt.Errorf("image.tools.%s: %q is not a mise tool and version this template can write", name, v)
		}
		data.ToolLines = append(data.ToolLines, fmt.Sprintf("%q = %q", name, v))
	}
```

- In `Dockerfile`, replace the `in := RenderInput{…}` line and the `if w.Base == "web-node"` block with:

```go
	kind := w.BaseKind()
	in := RenderInput{Workflow: name, Base: kind, Image: w.Image, Version: version}
	for _, s := range w.Secrets {
		in.Secrets = append(in.Secrets, s.Env)
	}
	if kind == "web-node" || kind == config.BaseKind {
		if in.PM, err = config.DetectNodePM(root); err != nil {
			return nil, "", fmt.Errorf("workflows.%s: %w", name, err)
		}
	}
	if kind == config.BaseKind {
		files, err := config.MiseConfigFiles(root)
		if err != nil {
			return nil, "", fmt.Errorf("workflows.%s: reading the mise config: %w", name, err)
		}
		in.Mise = len(files) > 0 || len(w.Image.Tools) > 0
	}
```

  and `checkBase(w.Base)` above it becomes `checkBase(w.BaseKind())`. Add `"maps"` to the imports.

`internal/config/nodepm.go`, `DefaultCache`: `if base != "web-node" && base != BaseKind { return nil, nil }`. Its comment says the Node default applies to `web-node` and the Fugaro base.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/image/ -run 'TestRender|TestDockerfile|TestTemplate' -count=1` and `go test ./internal/config/ -count=1`
Expected: PASS. The legacy goldens are unchanged, because both new blocks are conditional.

- [ ] **Step 5: Commit**

```bash
git add images/derived/Dockerfile.tmpl internal/image/render.go internal/image/render_test.go internal/config/nodepm.go
git commit -m "base kind task 10: the derived image installs the project's mise tools"
```

---

### Task 11: The image config hash covers mise

**Files:**
- Modify: `internal/imagecheck/record.go`, `internal/imagecheck/record_test.go`

**Interfaces:**
- Consumes: `config.MiseConfigPaths`, `config.MiseConfigDirs`, `config.MiseLockfile`, `Tree`.
- Produces: `imageConfig` gains `Tools map[string]string` (`json:"tools,omitempty"`) and `MiseFiles map[string]string` (`json:"mise_files,omitempty"`). `omitempty` keeps every legacy workflow's hash unchanged. `KeyFiles` gives the `base` kind the Node default cache.

- [ ] **Step 1: Write the failing test** (append to `record_test.go`)

```go
const baseWorkflowYAML = `version: 1
project: aurora
git: { provider: github }
workflows:
  app: { commands: { build: make, test: make test } }
`

// A changed root mise file or image.tools makes the image wrong: the hash
// moves. Files mise does not read at the root leave it alone.
func TestImageConfigHashCoversMiseFiles(t *testing.T) {
	hash := func(t *testing.T, yaml string, files map[string]string) string {
		t.Helper()
		dir := t.TempDir()
		testutil.WriteFiles(t, dir, files)
		h, err := ImageConfigHash(parse(t, yaml), "app", Dir{Root: dir})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	ref := hash(t, baseWorkflowYAML, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"})
	for _, tc := range []struct {
		name    string
		yaml    string
		files   map[string]string
		changes bool
	}{
		{"same", baseWorkflowYAML, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"}, false},
		{"mise.toml", baseWorkflowYAML, map[string]string{"mise.toml": "[tools]\nnode = \"22\"\n"}, true},
		{"mise.lock", baseWorkflowYAML, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n", "mise.lock": "x"}, true},
		{"conf.d", baseWorkflowYAML, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n", ".config/mise/conf.d/a.toml": "x"}, true},
		{".tool-versions", baseWorkflowYAML, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n", ".tool-versions": "go 1.27\n"}, true},
		{"subdirectory", baseWorkflowYAML, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n", "web/mise.toml": "x"}, false},
		{"mise.local.toml", baseWorkflowYAML, map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n", "mise.local.toml": "x"}, false},
		{"image.tools", strings.Replace(baseWorkflowYAML, "app: {", "app: { image: { tools: { node: \"24\" } },", 1), map[string]string{"mise.toml": "[tools]\nnode = \"24\"\n"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hash(t, tc.yaml, tc.files); (got != ref) != tc.changes {
				t.Errorf("changed = %v, want %v", got != ref, tc.changes)
			}
		})
	}
}

func TestImageConfigHashLegacyUnchanged(t *testing.T) {
	// The canonical form of a legacy workflow is what it was: no new keys.
	c := imageConfig{Base: "web-node"}
	data, _ := json.Marshal(c)
	if strings.Contains(string(data), "tools") || strings.Contains(string(data), "mise_files") {
		t.Errorf("legacy canonical form gained keys: %s", data)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/imagecheck/ -run 'TestImageConfigHash' -count=1`
Expected: FAIL. The `mise.toml`, `mise.lock`, `conf.d`, `.tool-versions` and `image.tools` cases report `changed = false, want true`.

- [ ] **Step 3: Implement**

In `record.go`:

```go
type imageConfig struct {
	Base           string            `json:"base"`
	Image          imageSettings     `json:"image"`
	Dockerfile     string            `json:"dockerfile"`
	DockerfileBlob string            `json:"dockerfile_blob"`
	Tools          map[string]string `json:"tools,omitempty"`
	MiseFiles      map[string]string `json:"mise_files,omitempty"`
}
```

`ImageConfigHash`: `c := imageConfig{Base: w.BaseKind(), …, Tools: w.Image.Tools}`. Before marshalling:

```go
	if w.BaseKind() == config.BaseKind {
		files, err := miseFiles(tree)
		if err != nil {
			return "", fmt.Errorf("workflows.%s: %w", workflow, err)
		}
		if len(files) > 0 {
			c.MiseFiles = files
		}
	}
```

with

```go
// miseFiles are the root mise config files and lockfile in tree, with their
// blob IDs: what the derived image's mise install read.
func miseFiles(tree Tree) (map[string]string, error) {
	out := map[string]string{}
	add := func(p string) error {
		id, err := tree.BlobID(p)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil:
			return fmt.Errorf("%s: %w", p, err)
		}
		out[p] = id
		return nil
	}
	for _, p := range append(slices.Clone(config.MiseConfigPaths), config.MiseLockfile) {
		if err := add(p); err != nil {
			return nil, err
		}
	}
	for _, d := range config.MiseConfigDirs {
		matches, err := tree.Glob(d + "/*.toml")
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			if strings.HasPrefix(path.Base(m), ".") {
				continue
			}
			if err := add(m); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}
```

(`path` and `strings` imports.) In `KeyFiles`, `defaultCache(w.BaseKind(), tree)`. In `defaultCache`, `if base != "web-node" && base != config.BaseKind { return config.DefaultCache(base, "") }`.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/imagecheck/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/imagecheck
git commit -m "base kind task 11: a changed mise config or image.tools always rebuilds"
```

---

### Task 12: The selftest checks mise and the harness credential files; the sample project

**Files:**
- Modify: `internal/image/selftest.go`, `selftest_test.go`, `local.go`, `local_test.go`, `docker_test.go`

**Interfaces:**
- Consumes: `checkTools` (Task 1), `Workflow.BaseKind`.
- Produces: `SelftestSpec.Mise bool` (`json:"mise,omitempty"`), the `mise-tools` check, the wider `homeCredentialFiles`; `SpecForCloud` and the local spec set `Tools: "critical", Mise: true` on the `base` kind.

- [ ] **Step 1: Write the failing tests**

Append to `selftest_test.go`:

```go
// fakeMise puts a mise on PATH that prints out for `ls --current --missing --json`.
func fakeMise(t *testing.T, out string) {
	t.Helper()
	bin := t.TempDir()
	testutil.WriteFiles(t, bin, map[string]string{"mise": "#!/bin/sh\n[ \"$MISE_OFFLINE\" = 1 ] || { echo 'not offline' >&2; exit 2; }\nprintf '%s\\n' '" + out + "'\n"})
	if err := os.Chmod(filepath.Join(bin, "mise"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestSelftestMiseToolsMissing(t *testing.T) {
	selftestEnv(t, 1)
	spec := selftestFixture(t)
	spec.Mise = true
	fakeMise(t, `{"node":[{"version":"24.19.0","requested_version":"24","install_path":"/x"}]}`)
	r := Selftest(context.Background(), spec, &bytes.Buffer{})
	c, ok := checkNamed(r, "mise-tools")
	if !ok || c.OK || !strings.Contains(c.Detail, "node@24.19.0") || r.Passed {
		t.Fatalf("mise-tools = %+v (passed %v)", c, r.Passed)
	}
	fakeMise(t, `{}`)
	r = Selftest(context.Background(), spec, &bytes.Buffer{})
	if c, ok := checkNamed(r, "mise-tools"); !ok || !c.OK {
		t.Fatalf("nothing missing: %+v", c)
	}
}

func TestSelftestRefusesHarnessCredentialFiles(t *testing.T) {
	for _, rel := range []string{".codex/auth.json", ".gemini/oauth_creds.json", ".local/share/opencode/auth.json",
		".config/gcloud/credentials.db", ".config/gcloud/application_default_credentials.json", ".claude/.credentials.json"} {
		t.Run(rel, func(t *testing.T) {
			selftestEnv(t, 1)
			spec := selftestFixture(t)
			testutil.WriteFiles(t, os.Getenv("HOME"), map[string]string{rel: "x"})
			r := Selftest(context.Background(), spec, &bytes.Buffer{})
			if c, _ := checkNamed(r, "home-credentials"); c.OK || !strings.Contains(c.Detail, rel) {
				t.Fatalf("home-credentials = %+v", c)
			}
		})
	}
}

func TestSpecForCloudOnTheBase(t *testing.T) {
	cfg, problems := config.Parse([]byte("version: 1\nproject: acme\ngit: { provider: github }\nworkflows:\n  app: { commands: { build: make, test: make test } }\n  web: { base: web-node, image: { node: \"24\" }, commands: { build: make, test: make test } }\n"))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	base, _ := SpecForCloud(cfg, "app", "c0ffee", "https://github.com/acme/app.git")
	if base.Tools != "critical" || !base.Mise || base.Base != config.BaseKind {
		t.Errorf("base: %+v", base)
	}
	web, _ := SpecForCloud(cfg, "web", "c0ffee", "https://github.com/acme/app.git")
	if web.Tools != "" || web.Mise || web.Node != "24" {
		t.Errorf("web-node: %+v", web)
	}
}
```

Append to `local_test.go`:

```go
func TestBuildLocalPassesThePlatformToSelftest(t *testing.T) {
	root, cfg := localFixture(t)
	f := &fakeDocker{t: t, report: &Report{Passed: true, Checks: []Check{{Name: "claude", OK: true}}}}
	var log bytes.Buffer
	o := localOptions(root, cfg, f, &log)
	o.Platform = "linux/arm64"
	if _, err := BuildLocal(context.Background(), o); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if !slices.Contains(f.builds[0], "linux/arm64") {
		t.Errorf("build: %q", f.builds[0])
	}
	for i, r := range f.runs {
		if !slices.Contains(r, "linux/arm64") {
			t.Errorf("selftest run %d lacks the platform: %q", i, r)
		}
	}
}

func TestBuildLocalSpecOnTheBase(t *testing.T) {
	root, cfg := localFixture(t)
	w := cfg.Workflows["web"]
	w.Base, w.Image.Node = "", ""
	cfg.Workflows["web"] = w
	f := &fakeDocker{t: t, report: &Report{Passed: true, Checks: []Check{{Name: "claude", OK: true}}}}
	var log bytes.Buffer
	if _, err := BuildLocal(context.Background(), localOptions(root, cfg, f, &log)); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if f.spec.Tools != "critical" || !f.spec.Mise || f.spec.Node != "" {
		t.Errorf("spec = %+v", f.spec)
	}
}
```

Append to `docker_test.go` (Docker; CI only):

```go
// The acceptance criterion (design base-image.md): a project with Node and
// Python in mise.toml builds on the base, its image passes the selftest,
// mise-tools included, and its tests run on the declared versions.
func TestSampleProjectNodePythonOnTheBase(t *testing.T) {
	base := testutil.BaseImageOf(t, "base")
	testutil.IsolateGit(t)
	files := map[string]string{
		"mise.toml":         "[tools]\nnode = \"24.21.0\"\npython = \"3.12\"\n",
		"package.json":      `{"name":"sample","version":"1.0.0","private":true,"scripts":{"test":"node test.js"}}`,
		"package-lock.json": `{"name":"sample","version":"1.0.0","lockfileVersion":3,"requires":true,"packages":{"":{"name":"sample","version":"1.0.0"}}}`,
		"test.js":           `if (!process.version.startsWith("v24.21.")) { console.error("node " + process.version); process.exit(1) }` + "\n",
		"test_sample.py":    "import sys\nassert sys.version_info[:2] == (3, 12), sys.version\n",
		"fugaro.yaml": `version: 1
project: aurora
git: { provider: github }
workflows:
  app:
    commands: { build: node --version && python3 --version, test: npm test && python3 test_sample.py }
`,
	}
	res := buildImage(t, checkout(t, files), base, "fugaro-test-sample:local")
	for _, name := range []string{"mise-tools", "tool:mise", "tool:claude", "verify-build"} {
		if c := smokeCheck(res, name); !c.OK {
			t.Errorf("%s: %+v", name, c)
		}
	}
}
```

- [ ] **Step 2: Run the unit tests to see them fail**

Run: `go test ./internal/image/ -run 'TestSelftestMiseToolsMissing|TestSelftestRefusesHarnessCredentialFiles|TestSpecForCloudOnTheBase|TestBuildLocalPassesThePlatform|TestBuildLocalSpecOnTheBase' -count=1`
Expected: build failure, `unknown field Mise`. Once that compiles, the credential-file cases fail.

- [ ] **Step 3: Implement**

`selftest.go`:
- `SelftestSpec` gains `Mise bool \`json:"mise,omitempty"\`` (comment: "check that every tool the checkout's mise config and image.tools declare is installed: the base kind").
- `homeCredentialFiles` gains `".codex/auth.json", ".gemini/oauth_creds.json", ".local/share/opencode/auth.json", ".config/gcloud/credentials.db", ".config/gcloud/application_default_credentials.json", ".claude/.credentials.json"`, with a comment pointing at design base-image.md section 6's table.
- `SpecForCloud`: after building `spec`,

```go
	if w.BaseKind() == config.BaseKind {
		spec.Base, spec.Tools, spec.Mise = config.BaseKind, "critical", true
	}
```

  (`Base: w.Base` becomes `Base: w.BaseKind()`.)
- In `Selftest`, after `checkoutOK := checkCheckout(ctx, spec, add)`:

```go
	if spec.Mise && checkoutOK {
		checkMise(ctx, spec.RepoDir, add)
	}
```

- Add:

```go
// checkMise reports "mise-tools": every tool the checkout's mise config and
// image.tools declare is installed. It reads mise offline in dir, so it
// works in the smoke's container, which has no network.
func checkMise(ctx context.Context, dir string, add func(string, bool, string, ...any)) {
	cmd := exec.CommandContext(ctx, "mise", "ls", "--current", "--missing", "--json")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "MISE_OFFLINE=1", "NO_COLOR=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		add("mise-tools", false, "mise ls --current --missing: %v: %s", err, strings.TrimSpace(stderr.String()))
		return
	}
	var missing map[string][]struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out, &missing); err != nil {
		add("mise-tools", false, "mise ls printed no JSON: %v", err)
		return
	}
	var names []string
	for _, tool := range slices.Sorted(maps.Keys(missing)) {
		for _, v := range missing[tool] {
			names = append(names, tool+"@"+v.Version)
		}
	}
	if len(names) > 0 {
		add("mise-tools", false, "not installed: %s (the image's mise install did not run or failed; start from fugaro image render)", strings.Join(names, ", "))
		return
	}
	add("mise-tools", true, "every tool the mise config declares is installed")
}
```

(imports: `encoding/json`, `maps`, `slices`.)

`local.go`: the spec's `Base: w.Base` becomes `Base: w.BaseKind()`. Replace `if w.Base == "web-node" { spec.Node = w.Image.Node }` with:

```go
	switch w.BaseKind() {
	case "web-node":
		spec.Node = w.Image.Node
	case config.BaseKind:
		spec.Tools, spec.Mise = "critical", true
	}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/image/ -count=1` (the package's non-Docker tests; about 2 minutes) and `go vet -tags docker ./internal/image/`
Expected: PASS, and vet is clean. `TestSampleProjectNodePythonOnTheBase` runs in CI's `docker-tests`.

- [ ] **Step 5: Commit**

```bash
git add internal/image
git commit -m "base kind task 12: the selftest checks mise tools and harness credentials; a mise sample project"
```

---

### Task 13: `BaseKind()` everywhere, and the `base` kind through init, build and refresh

**Files:**
- Modify (each `w.Base` read becomes `w.BaseKind()`):
  - `internal/infra/spec.go` (lines 787 and 823 in `check`);
  - `internal/runner/lockcache.go` (195);
  - `internal/cli/run.go` (216);
  - `internal/cli/imagecheck.go` (193, 381 to 382, 599, 640);
  - `internal/cli/image_refresh.go` (148, 179);
  - `internal/cli/init_images.go` (124 to 125);
  - `internal/cli/image.go` (122, 203, 206, 222);
  - `internal/cli/init_fugaroyaml.go` (478);
  - `internal/config/validate.go` (`LintDockerfile(data, w.BaseKind())`).
- Modify: `internal/cli/init.go` (flag help)
- Test: `internal/cli/init_images_test.go`, `internal/mirror/mirror_test.go`, `internal/infra/spec_test.go`

**Interfaces:**
- Consumes: `Workflow.BaseKind`, `image.BaseRef` (unchanged: `ghcr.io/dimipaun/fugaro-` + kind), the mirror (unchanged).
- Produces: `fugaro init --base base` copies `ghcr.io/dimipaun/fugaro-base:<v>` to `<host>/fugaro-base/fugaro-base:<v>` and records `base_images.base`. A checkout whose workflows have no `base:` asks for the `base` kind.

- [ ] **Step 1: Write the failing tests**

In `internal/cli/init_images_test.go`, `newImagesRigFor` publishes `"fugaro-base"` too (add it to the list). Append:

```go
func TestBaseKindIsCopiedAsFugaroBase(t *testing.T) {
	r := newImagesRig(t, "v1.2.3")
	out, _, err := executeStdin(t, "", "init", "--yes", "--base", "base")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := "us-east5-docker.pkg.dev/" + initProject + "/fugaro-base/fugaro-base:1.2.3"
	if bi := r.localConfig(t).BaseImages; bi["base"] != want || len(bi) != 1 {
		t.Errorf("base_images = %v, want base: %s", bi, want)
	}
	if r.dst.tags[initProject+"/fugaro-base/fugaro-base:1.2.3"] == "" {
		t.Errorf("not copied: %v", r.dst.tags)
	}
}

// A checkout whose workflow has no base: names the base kind.
func TestCheckoutWithoutBaseAsksForTheBaseKind(t *testing.T) {
	r := newImagesRig(t, "v1.2.3")
	testutil.Git(t, ".", "init", "--quiet")
	if err := os.WriteFile("fugaro.yaml", []byte("version: 1\nproject: "+initProjectName+"\ngit: { provider: github }\nworkflows:\n  app: { commands: { build: make, test: make test } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeStdin(t, "", "init", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r.localConfig(t).BaseImages["base"] == "" {
		t.Errorf("the checkout's base kind was not copied: %v\n%s", r.localConfig(t).BaseImages, out)
	}
}
```

`initProjectName` is the project name the rig's local config has (`githubapp_test.go` uses it the same way). The `git init` gives `init` a checkout root to read `fugaro.yaml` from.

Append to `internal/mirror/mirror_test.go`:

```go
// A multi-platform index copies its linux/amd64 image only: the arm64
// image's blobs and manifest never reach the project's registry, and the
// digest --expect-digest pins is the index's.
func TestMirrorLeavesTheArm64ImageBehind(t *testing.T) {
	e := newEnv(t)
	arm := makeImage("arm64", "arm-layer")
	p, err := e.plan()
	if err != nil {
		t.Fatal(err)
	}
	if p.SourceDigest == p.Digest || p.Digest != e.img.digest {
		t.Errorf("source %s, copied %s: want the index and its amd64 child", p.SourceDigest, p.Digest)
	}
	if _, err := e.m.Copy(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	for _, b := range append([][]byte{arm.cfg}, arm.layers...) {
		if _, ok := e.dst2.blobs[dg(b)]; ok {
			t.Errorf("the arm64 blob %s was copied", dg(b))
		}
	}
	if _, ok := e.dst2.manifests[arm.digest]; ok {
		t.Error("the arm64 manifest was copied")
	}
}
```

Append to `internal/infra/spec_test.go`:

```go
// A checked workflow without base: builds from base_images.base, and the
// check job runs from it.
func TestCheckJobOnTheBaseKind(t *testing.T) {
	in := webappInputs(t)
	for name, w := range in.Cfg.Workflows {
		w.Base, w.Image.Node = "", ""
		w.Rebuild.Check = "auto"
		in.Cfg.Workflows[name] = w
	}
	base := "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:1.2.3"
	in.LC.BaseImages = map[string]string{"base": base}
	rs, err := Repo(in)
	if err != nil {
		t.Fatal(err)
	}
	if rs.Check.Image != base {
		t.Errorf("check image = %s, want %s", rs.Check.Image, base)
	}
	var spec CheckJobSpec
	if err := json.Unmarshal([]byte(rs.Check.Env[CheckSpecEnv]), &spec); err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"base": base}; !maps.Equal(spec.BaseImages, want) {
		t.Errorf("job spec base images = %v, want %v", spec.BaseImages, want)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run 'TestBaseKindIsCopiedAsFugaroBase|TestCheckoutWithoutBaseAsksForTheBaseKind' -count=1`, `go test ./internal/mirror/ -run TestMirrorLeavesTheArm64ImageBehind -count=1`, `go test ./internal/infra/ -run TestCheckJobOnTheBaseKind -count=1`
Expected:
- the second CLI test FAILS (`w.Base` is empty, so the kind set is empty);
- the infra test FAILS with `no base_images entry for `;
- the mirror test PASSES at once. It pins existing behaviour, and the design relies on it.

- [ ] **Step 3: Implement**

Replace each listed `.Base` read with `.BaseKind()`. In `init_images.go`:

```go
		for _, w := range cfg.Workflows {
			if k := w.BaseKind(); slices.Contains(config.Bases, k) {
				set[k] = true
			}
		}
```

In `init.go`, the `--base` help becomes "base kinds whose release image init copies into the project's registry now: base (the Fugaro base), or until 0.7.0 go, java-services, web-node; besides the ones the checkout's fugaro.yaml names". The `--expect-digest` and `--replace-image` help name `base` first in their kind lists.

- [ ] **Step 4: Run the tests**

Run, in the foreground:
- `go test ./internal/cli/ -run 'TestBaseKind|TestCheckoutWithoutBase|TestBaseImages|TestImagesStage|TestImageRefresh|TestImageCheck|TestImageBuild|TestRun' -count=1`
- `go test ./internal/infra/ ./internal/mirror/ ./internal/imagecheck/ -count=1`
- `go test -race ./internal/runner/ -run 'TestLockCache|TestCacheLink' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal
git commit -m "base kind task 13: BaseKind everywhere; the base kind flows through init, build, check and refresh"
```

---

### Task 14: Fugaro's own `mise.toml`

**Files:**
- Create: `mise.toml` (the repository root), `images/repo_pins_test.go`

**Interfaces:**
- Produces: Fugaro's runtimes for the day `fugaro.yaml` drops `base: go` (Task 22). Until then the file only serves developers who use mise.

- [ ] **Step 1: Write the failing test**

`images/repo_pins_test.go`:

```go
package images_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Fugaro's mise.toml pins the Go of go.mod's go line's minor series and the
// Terraform CI tests with, and keeps GOTOOLCHAIN local, as the go base did.
func TestRepositoryMisePins(t *testing.T) {
	mise, err := os.ReadFile("../mise.toml")
	if err != nil {
		t.Fatal(err)
	}
	pin := func(name string) string {
		m := regexp.MustCompile(`(?m)^` + name + ` = "([^"]+)"$`).FindStringSubmatch(string(mise))
		if m == nil {
			t.Fatalf("mise.toml has no %s pin", name)
		}
		return m[1]
	}
	mod, _ := os.ReadFile("../go.mod")
	gm := regexp.MustCompile(`(?m)^go (\d+\.\d+)`).FindStringSubmatch(string(mod))
	if gm == nil || !strings.HasPrefix(pin("go"), gm[1]+".") {
		t.Errorf("mise.toml go %s is not in go.mod's %v series", pin("go"), gm)
	}
	ci, _ := os.ReadFile("../.github/workflows/ci.yml")
	tm := regexp.MustCompile(`terraform_version: ([\d.]+)`).FindStringSubmatch(string(ci))
	if tm == nil || pin("terraform") != tm[1] {
		t.Errorf("mise.toml terraform %s, ci.yml %v", pin("terraform"), tm)
	}
	if !strings.Contains(string(mise), "GOTOOLCHAIN = \"local\"") {
		t.Error("mise.toml does not keep GOTOOLCHAIN local")
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./images/ -run TestRepositoryMisePins -count=1`
Expected: FAIL, `open ../mise.toml: no such file or directory`.

- [ ] **Step 3: Write `mise.toml`**

```toml
# Fugaro's own runtimes (docs/base-image.md): the Fugaro base installs them
# into this repository's derived image, and developers who use mise get the
# same. images/repo_pins_test.go keeps go in go.mod's series and terraform at
# ci.yml's version.
[tools]
go = "1.27.1"
terraform = "1.16.4"

[env]
GOTOOLCHAIN = "local"
```

- [ ] **Step 4: Run the test and the group's suite**

Run: `go test ./images/ -run 'TestRepositoryMisePins|TestGoBasePins' -count=1`, then `go test ./... 2>&1 | tail -30` (foreground; about 25 minutes).
Expected: PASS.

- [ ] **Step 5: Commit and open the PR**

```bash
git add mise.toml images/repo_pins_test.go
git commit -m "base kind task 14: Fugaro's own mise.toml"
```

Open the PR, titled "Base image: the base kind, image.tools and mise in the derived image (group 2 of 4)". The body says that the **release freeze starts at this merge** (decision B22) and that `TestSampleProjectNodePythonOnTheBase` runs in `docker-tests`. Read every CI job's log before merging.

---
## Group 3: docs and the setup skill (branch `base-docs`)

Group 3's docs describe 0.7.0: the one base, with the legacy kinds removed. They land on `main` before Group 4 removes the code. The release freeze (B22) guarantees no release ships the docs ahead of the behaviour, and Task 20's test pins that no doc presents a legacy kind as current.

### Task 15: The user docs, the migration doc and design v1 §7

**Files:**
- Create: `docs/base-image.md`, `docs/base-image-migration.md`, `internal/cli/docs_base_test.go`
- Modify: `docs/design/v1.md` (§7.1, §7.1.1 and §7.2's steps 2, 3 and 5), `docs/gcp-setup.md`, `docs/release.md`, `docs/dogfooding.md`, `docs/backends.md`, `README.md`

**Interfaces:**
- Consumes: `config.MigrationDoc`, `images.BaseTools`.
- Produces: the two docs every refusal and the setup skill point at.

- [ ] **Step 1: Write the failing docs tests**

`internal/cli/docs_base_test.go`:

```go
package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/images"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/image"
)

func readDoc(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile("../../" + path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestMigrationDocCoversEveryKind: the doc every refusal names exists and
// says, per removed kind, what to write and what moves (design section 10).
func TestMigrationDocCoversEveryKind(t *testing.T) {
	doc := readDoc(t, config.MigrationDoc)
	for _, want := range []string{
		"## From `go`", "## From `web-node`", "## From `java-services`",
		`GOTOOLCHAIN = "local"`, `java = "temurin-25"`, `"npm:firebase-tools"`, "memory: 16Gi",
		"postgresql-17-postgis-3", "postgresql-17-pgvector", "redis-server",
		"image.node", "image.jdk", "image.tools", "skip_build_scripts", "base_images.base",
		"fugaro image refresh", "fugaro image render", "fugaro validate", "fugaro image build --local",
		"don't launch runs", "## The order",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q", config.MigrationDoc, want)
		}
	}
}

// TestBaseImageDocNamesEveryTool: the user doc lists every harness and
// critical tool of the presence table, says which harnesses Fugaro drives,
// and states the contract (mise, the Dockerfile shape, no sudo).
func TestBaseImageDocNamesEveryTool(t *testing.T) {
	doc := readDoc(t, "docs/base-image.md")
	tools, err := image.ParseTools(images.BaseTools)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if tool.Tier == "kit" {
			continue
		}
		name := tool.Name
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		if !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("docs/base-image.md never names `%s`", name)
		}
	}
	for _, want := range []string{
		"only Claude Code is driven by Fugaro", "mise.toml", "image.tools", "mise install",
		"fugaro image render", "apt-get install", "image.apt", "mise use -g", "linux/arm64",
		"fugaro-services start", "never bake", config.MigrationDoc,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/base-image.md lacks %q", want)
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/cli/ -run 'TestMigrationDocCoversEveryKind|TestBaseImageDocNamesEveryTool' -count=1`
Expected: FAIL, `open ../../docs/base-image-migration.md: no such file or directory`.

- [ ] **Step 3: Write `docs/base-image.md`**

````markdown
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

Without a `mise.toml`, list the tools in `fugaro.yaml` instead (never both):

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
````

- [ ] **Step 4: Write `docs/base-image-migration.md`**

````markdown
# Moving to the Fugaro base (0.7.0)

Fugaro 0.7.0 replaces the `go`, `web-node` and `java-services` bases with one base image (see [base-image.md](base-image.md)). There is no transition release: a 0.7.0 CLI refuses `base:`, `image.node` and `image.jdk`, and a 0.6 one refuses a `fugaro.yaml` without `base:`. So each repository moves in one pull request, and the installation follows right after, in the order below.

## The order

1. Every operator upgrades the CLI to 0.7.0 (`fugaro upgrade --local`, then Homebrew for the binary).
2. Per repository, one pull request: the `mise.toml` and the `fugaro.yaml` change below (the `/fugaro:setup` skill writes both). Before opening it, `fugaro validate` must pass and `fugaro image build --local` must pass the smoke test.
3. Merge it, then **at once**, in that checkout: `fugaro image refresh`. It copies `fugaro-base` into your registry, records `base_images.base`, moves the daily check job onto it, and rebuilds every workflow's image.
4. **Between the merge and the refresh finishing, don't launch runs**: the old image's 0.6 runner refuses the new `fugaro.yaml`. It takes minutes.
5. `fugaro doctor` in the checkout reports nothing about bases.

The local config: delete every `base_images` line other than `base`; `fugaro image refresh` writes `base_images.base`. A 0.7.0 CLI refuses a local config with `base_images.go`, `base_images.web-node` or `base_images.java-services`, naming this doc. Old entries in the project's shared config are ignored with a warning until the next `fugaro init` or `fugaro image refresh` republishes it.

## Everywhere

- Delete the workflow's `base:` line.
- `image.apt`, `image.setup` and `image.skip_build_scripts` keep their meaning (`skip_build_scripts` needs a `package.json` and a lockfile).
- `resources` defaults are now 4 CPUs and 8Gi for every workflow.
- A `.fugaro/<workflow>.Dockerfile`: run `fugaro image render --workflow <workflow>` again, then reapply your own lines to the new output. Its `FROM` must be `${FUGARO_BASE}`: a `FROM ghcr.io/dimipaun/fugaro-web-node` (or `-go`, `-java-services`) is refused.
- The base is Debian 13, not Ubuntu 24.04: an `image.apt` package name may differ (Debian's package search: packages.debian.org).

## From `go`

```toml
# mise.toml
[tools]
go = "1.27.1"          # go.mod's go line, exact
terraform = "1.16.4"   # only if your commands used the base's Terraform

[env]
GOTOOLCHAIN = "local"  # what the go base set: no toolchain downloads at run time
```

`image.setup: ["go mod download && go build ./..."]` stays as it is. gcc and make are in the base.

## From `web-node`

```toml
# mise.toml
[tools]
node = "24.19.0"       # image.node's value; without image.node, the base had 24
```

- `image.node` moves to `mise.toml` (or `image.tools: { node: "24.19.0" }` if you don't want a `mise.toml`); a 0.7.0 CLI refuses it.
- pnpm and Yarn keep coming from corepack and `packageManager`.
- The dependency warm-up and the default cache are unchanged.
- Playwright's `--with-deps` works on Debian 13; keep the `image.setup` line.

## From `java-services`

```toml
# mise.toml
[tools]
java = "temurin-25"              # the JDK the base pinned (image.jdk was never allowed)
node = "24"                      # only for the Firebase emulators
"npm:firebase-tools" = "15.32.1" # the version the base pinned
```

```yaml
# fugaro.yaml
workflows:
  server:
    image:
      apt: [postgresql-17, postgresql-17-postgis-3, postgresql-17-pgvector, redis-server]
      setup:
        - firebase setup:emulators:database && firebase setup:emulators:storage && firebase setup:emulators:ui
        - ./gradlew --no-daemon testClasses
    commands:
      test: fugaro-services start && ./gradlew --no-daemon integrationTest
    resources: { cpu: 4, memory: 16Gi }
```

- List only the services and emulators your tests use.
- `fugaro-services start` works as before. It now looks for what you installed and names the package when one is missing.
- `memory: 16Gi` was the base's default; write it explicitly.
- Gradle still comes from your wrapper.

## `image.jdk` and other removed settings

`image.jdk` was refused on every base. Declare Java in `mise.toml` (`java = "temurin-21"`, `java = "zulu-17"`, …). `base:` and `image.node` are refused with the fixes above.

## Checklist for a repository with services (Belong-style)

Sandbox first, then the real repository:
- [ ] `fugaro validate` passes; `fugaro image build --local --platform linux/arm64` (Apple silicon) then `--platform linux/amd64` pass the smoke test, `mise-tools` included.
- [ ] A frontend that uses Playwright: `npx playwright install --with-deps chromium` succeeds in `image.setup` on Debian 13.
- [ ] A server: `fugaro-services start` brings up PostgreSQL with `CREATE EXTENSION postgis` and `vector`, Redis answers `PONG`, and the emulators the tests use listen on loopback.
- [ ] One sandbox run, end to end, then the real repository's PR, then `fugaro image refresh`.
````

- [ ] **Step 5: Update the other docs**

- `docs/design/v1.md`:
  - **§7.1:** replace the table and the paragraph after it with: "Published as `ghcr.io/dimipaun/fugaro-base:<version>`, with a moving `:<major>` tag, for linux/amd64 and linux/arm64, and rebuilt nightly for Debian's security fixes. It carries Debian 13 slim, the tool kit, gcloud, the Docker CLI, mise, the agent harnesses (only Claude Code is driven) and fugaro; projects install their runtimes with mise ([design/base-image.md](base-image.md)). The `go`, `web-node` and `java-services` bases were removed in 0.7.0." Keep the paragraph about the `fugaro` user's HOME and `/work/creds`. The scripts sentence becomes "…`images/build-base.sh`, `images/smoke.sh`, `images/size-budget.sh` and `images/scan.sh` (with `--secrets`)…".
  - **§7.1.1:** becomes "`fugaro-services`": keep its paragraph on what `start` does, replace "the base carries the services themselves" with "the project installs the services (`image.apt` on Debian 13; firebase-tools as a mise tool) and the base carries the script". Delete the Versions, Emulators and Size bullets; keep Loopback only and Layout.
  - **§7.2:** step 2 becomes "applies the `image:` settings: extra `apt` packages as root, and `image.tools` as the global mise config"; step 3 gains "then runs `mise install` in the checkout as `fugaro`, with a download cache mount and the workflow's secrets, and fails when a declared tool is still missing"; the warm-up in step 4 is chosen "from the checkout's files: a `package.json` with a lockfile"; the Local check paragraph's "`node -v` work, with Node matching `image.node`" becomes "the critical tools of `images/base/tools.tsv` answer and every mise tool is installed (`mise-tools`)".
- `docs/gcp-setup.md`:
  - Line 159's migration note becomes "**0.7.0:** `base_images` holds one key, `base`; [base-image-migration.md](base-image-migration.md) says how to move."
  - In the "Release images" paragraph: `fugaro init --base base` (and "the base kind your checkout's `fugaro.yaml` implies"); `ghcr.io/dimipaun/fugaro-base:<version>`; `--expect-digest base=sha256:<hex>`, "pins the digest of the release tag's index; init copies the linux/amd64 image from it"; `--replace-image KIND` lists "`history` or `base`". Drop the "Which images exist" history sentence; add "About 1.2 GB is copied through your machine".
  - The build-from-checkout block: `tag=<region>-docker.pkg.dev/<gcp-project>/fugaro-base/fugaro-base:dev-$commit` and `sh <fugaro checkout>/images/build-base.sh base "$tag"`.
  - The `init --base-image` bullet: "`--base-image base=IMAGE` (or an image named `fugaro-base`)"; "A project needs the one entry `base`".
- `docs/release.md`:
  - Line 10: the images list is `fugaro-base` (one index for linux/amd64 and linux/arm64, plus `X.Y.Z-amd64` and `X.Y.Z-arm64`) and `fugaro-history`; `verify-public` requires arm64 for `fugaro-base`.
  - Line 16: "both packages".
  - Line 30: KIND is `base` or `history`.
  - Lines 133 to 135: the pull checks become `docker pull --platform linux/amd64 ghcr.io/dimipaun/fugaro-base:$V` and `docker pull --platform linux/arm64 ghcr.io/dimipaun/fugaro-base:$V`.
  - Add a section "**The monthly base bump**": the `base-bump` workflow, close and reopen its PR to run CI, read each component's release notes, never merge unread.
- `docs/dogfooding.md` line 21: `fugaro-base` and `fugaro-history`; build `images/build-base.sh base`; `fugaro init --base base`; the repository's runtimes are in `mise.toml`.
- `docs/backends.md`: add a short "Your own image" paragraph pointing at [base-image.md](base-image.md)'s "Bring your own".
- `README.md`:
  - line 54 becomes "…from the image of the repository's workflow: the one Fugaro base plus its checkout, its runtimes (from `mise.toml`) and its dependencies…";
  - in the example at line 104, delete the `base:` line and add a `mise.toml` comment;
  - line 135's checklist item becomes "the base image (one, multi-architecture, with mise)".

- [ ] **Step 6: Run the docs tests**

Run: `go test ./internal/cli/ -run 'TestDocs|TestMigrationDoc|TestBaseImageDoc' -count=1` and `go test ./plugin/ -count=1`
Expected: PASS. `TestDocsNameImageRefresh` still finds `fugaro image refresh` with only real flags.

- [ ] **Step 7: Commit**

```bash
git add docs README.md internal/cli/docs_base_test.go
git commit -m "base docs task 15: the base image, the migration guide, design v1 section 7"
```

---

### Task 16: The setup skill writes `mise.toml` and one Dockerfile shape

**Files:**
- Modify: `plugin/skills/setup/SKILL.md`, `reference/discovery.md`, `reference/services-and-images.md`, `reference/validation.md`, `plugin/setup_fixtures_test.go`, `plugin/setup_skill_test.go`
- Modify fixtures:
  - `plugin/testdata/repos/{web-node,go,java-services,monorepo,existing-fugaro-yaml,hostile}/expected/fugaro.yaml`;
  - add `expected/mise.toml` to each;
  - re-render `plugin/testdata/repos/monorepo/expected/.fugaro/api.Dockerfile`.

**Interfaces:**
- Consumes: `config.ValidMiseTool`, `config.MiseConfigFiles`, `image.Render`'s `Mise` field.
- Produces: fixtures whose expected output is `mise.toml` plus a `fugaro.yaml` without `base:`. `fixtureFacts.tools` (`map[string]string`) replaces `bases`, `versionFile` and `node`.

- [ ] **Step 1: Change the fixture tests first**

In `plugin/setup_fixtures_test.go`:
- `fixtureFacts` drops `bases`, `node` and `versionFile`, and gains `workflows []string`, `tools map[string]string` (the expected `mise.toml`) and `toolEvidence map[string]string` (tool → the repository file that pins it, empty for a decision taken with the user).
- `fixtures` becomes:

```go
var fixtures = []fixtureFacts{
	{name: "web-node", workflows: []string{"web"}, tools: map[string]string{"node": "24.19.0"}, toolEvidence: map[string]string{"node": ".nvmrc"},
		aptEvidence: ".github/workflows/ci.yml", apt: []string{"libvips-dev"}},
	{name: "go", workflows: []string{"go"}, tools: map[string]string{"go": "1.25"}, toolEvidence: map[string]string{"go": "go.mod"}},
	{name: "java-services", workflows: []string{"server"}, tools: map[string]string{"java": "temurin-25"},
		apt: []string{"postgresql-17"},
		servicesFile: "bitbucket-pipelines.yml", servicesText: "postgres", testPrefix: "fugaro-services start"},
	{name: "monorepo", workflows: []string{"api", "web"}, dockerfiles: []string{"api"},
		tools: map[string]string{"go": "1.25", "node": "24"}, toolEvidence: map[string]string{"go": "services/api/go.mod"}},
	{name: "existing-fugaro-yaml", workflows: []string{"go"}, tools: map[string]string{"go": "1.25"}, toolEvidence: map[string]string{"go": "go.mod"}},
	{name: "hostile", workflows: []string{"web"}, tools: map[string]string{"node": "22"}, toolEvidence: map[string]string{"node": ".nvmrc"}},
}
```

  The values were checked against the fixtures: both `go.mod`s say `go 1.25`, the web-node `.nvmrc` holds `24.19.0` and hostile's `22`. The `java-services` fixture's `postgresql-17` comes from its pipeline's `postgres` service (`image: postgres:17`) through the skill's services table, so it has no `aptEvidence`: the `servicesFile` check covers it. Its `java` has no pin in the repository (the pipeline's `eclipse-temurin:25` image is CI's), so it has no `toolEvidence` entry, and the skill asks the user.
- In `checkFixture`:
  - section (b) calls `config.LintDockerfile(df, w.BaseKind())` and `checkDerivedFromRender(t, wf, string(df), checkout)`;
  - section (c) compares the workflow names with `facts.workflows`;
  - `detectBases` becomes `detectRuntimes`, which returns the sorted mise tools the discovery table derives (`node` for a `package.json` with a lockfile, `java` for Gradle, `go` for `go.mod`) and must equal the sorted keys of `facts.tools`;
  - the `image.node` checks become checks on `expected/mise.toml`:

```go
	mise := parseMiseTools(t, fixtureFile(t, name, "expected/mise.toml"))
	if !equalMaps(mise, facts.tools) {
		t.Errorf("mise.toml tools %v, want %v", mise, facts.tools)
	}
	for tool, file := range facts.toolEvidence {
		if ev := fixtureFile(t, name, "repo/"+file); !strings.Contains(ev, mise[tool]) {
			t.Errorf("mise.toml %s = %q, but %s does not say it", tool, mise[tool], file)
		}
	}
	for wf, w := range c.Workflows {
		if w.Base != "" || w.Image.Node != "" || len(w.Image.Tools) > 0 {
			t.Errorf("workflow %s: base %q, image.node %q, image.tools %v: the skill writes mise.toml instead", wf, w.Base, w.Image.Node, w.Image.Tools)
		}
	}
```

    with

```go
// parseMiseTools reads the [tools] table of a mise.toml as the skill writes
// it: one `name = "version"` per line, each a tool and version config allows.
func parseMiseTools(t *testing.T, text string) map[string]string {
	t.Helper()
	out := map[string]string{}
	in := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "["):
			in = line == "[tools]"
		case in:
			k, v, ok := strings.Cut(line, " = ")
			k, v = strings.Trim(k, `"`), strings.Trim(v, `"`)
			if !ok || !config.ValidMiseTool(k, v) {
				t.Errorf("mise.toml line %q is not name = \"version\"", line)
				continue
			}
			out[k] = v
		}
	}
	return out
}
```

  - `checkDerivedFromRender(t, wf, df, checkout string)` renders with `image.RenderInput{Workflow: wf, Base: config.BaseKind, Mise: hasMise, Version: fixtureVersion}`, where `hasMise` is true when `config.MiseConfigFiles(checkout)` returns at least one file (a returned error fails the test);
  - the secret check also covers `expected/mise.toml`.

In `plugin/setup_skill_test.go`:
- `TestSetupSkillExamplesValidate` collects `w.BaseKind()`, and requires every example to be on `config.BaseKind`, at least one with `image.tools`, one without (a `mise.toml` workflow), and one with `dockerfile:`.
- It also checks every ```` ```toml mise.toml ```` example with `parseMiseTools`.
- `checkExample` writes `package.json` and `package-lock.json` when `w.Image.SkipBuildScripts` is set, renders with `Base: config.BaseKind`, and passes the example's `mise.toml` (when the skill file shows one before it) into the scratch checkout.
- The `wantPhrases` list at line 275 drops `TEST_SERVICES=local` and gains `mise.toml`, `mise install`, `image.tools`, `apt-get install`, `fugaro-services start`, `docs/base-image-migration.md` and `--platform linux/arm64`.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./plugin/ -count=1`
Expected: FAIL. There is no `expected/mise.toml`, the fixtures still name `base:`, and the skill's examples name legacy bases.

- [ ] **Step 3: Update the fixtures**

For each fixture:
- remove `base:` (and `image.node`) from `expected/fugaro.yaml`;
- write `expected/mise.toml` with `[tools]` and the `tools` above, one `name = "version"` per line;
- for `java-services`, add `apt: [postgresql-17]` under `image:`.

For `existing-fugaro-yaml`, `repo/fugaro.yaml` keeps its `base: go` (the skill is fixing it) and the expected one drops it.

Re-render the monorepo's Dockerfile:

```bash
d=$(mktemp -d) && cp -R plugin/testdata/repos/monorepo/repo/. "$d" && cp plugin/testdata/repos/monorepo/expected/fugaro.yaml plugin/testdata/repos/monorepo/expected/mise.toml "$d" \
 && (cd "$d" && git init -q && git add -A && git -c user.email=t@t -c user.name=t commit -qm fixture) \
 && (cd "$d" && go run -C "$OLDPWD" -ldflags "-X github.com/dimipaun/fugaro/internal/cli.Version=0.0.0" ./cmd/fugaro image render --workflow api) > /tmp/api.render
```

(`go run -C` builds from the Fugaro checkout while the command runs in `$d`; if your Go lacks `-C`, build `fugaro` first and run it in `$d`.) Then replace `plugin/testdata/repos/monorepo/expected/.fugaro/api.Dockerfile` with `/tmp/api.render` plus the fixture's own `USER root` block (the checksummed `examplectl` install), unchanged, at the same place: after `ARG BASE_BRANCH`.

- [ ] **Step 4: Rewrite the skill text**

`reference/discovery.md`:
- the "base kind" section becomes "## Workflows and runtimes", with this table:

| Evidence | Workflow | `mise.toml` |
|---|---|---|
| `package.json` with a lockfile | `web` | `node`: `.nvmrc`, `.node-version`, `engines.node`, `actions/setup-node`'s `node-version`, a CI image `node:X` (exact when pinned, else the major) |
| `go.mod` | `go` | `go`: the `toolchain` line if present, else the `go` line; `[env] GOTOOLCHAIN = "local"` |
| `gradlew`, `settings.gradle(.kts)`, `pom.xml` | `server` | `java`: a Gradle `toolchain { languageVersion }`, `maven.compiler.release`, `actions/setup-java`'s `java-version` and `distribution` (`temurin-21`); without evidence, ask the user (a former `java-services` repository: `temurin-25`) |
| `pyproject.toml`, `requirements*.txt`, `.python-version` | `app` | `python`: `.python-version`, `requires-python`, `actions/setup-python` |
| `Gemfile`, `.ruby-version` | `app` | `ruby` |
| `rust-toolchain.toml`, `Cargo.toml` | `app` | `rust`: the toolchain file's `channel` |
| an existing `mise.toml` or `.tool-versions` | as above | **respect it**: add a missing tool only with the user's agreement, never change a pinned version |

- the rule "anything else: there is no base" is deleted;
- the "Versions" table and the apt sentence about the bases' packages are replaced by "The base has the kit listed in docs/base-image.md; `image.apt` adds Debian 13 packages it lacks";
- the "Machine size" defaults line becomes "Defaults: 4 CPUs and 8Gi; a server whose tests start services usually needs 16Gi".

`reference/services-and-images.md`:
- "## The base kinds" becomes "## The base and `mise.toml`", saying: one base; runtimes from `mise.toml` at the root (`fugaro image build` runs `mise install`); `image.tools` only when the user does not want a `mise.toml`, never both; **never** `apt-get install <language>` or a `curl | sh` installer for a runtime.
- The services table's first row becomes "Postgres (PostGIS, pgvector), Redis, Firebase emulators | `image.apt: [postgresql-17, postgresql-17-postgis-3, postgresql-17-pgvector, redis-server]` (only those used), firebase-tools as `"npm:firebase-tools"` in `mise.toml` with `java` and `node`, `firebase setup:emulators:<name>` in `image.setup`, and `fugaro-services start` first in `commands.test`". The "on another base" row is deleted.
- The `image:` table's `image.node` row becomes "a pinned Node version → `node` in `mise.toml`".
- The examples become four, all without `base:`:
  1. a web workflow with a `mise.toml` example (```` ```toml mise.toml ````, `node = "24.19.0"`) and `apt` plus Playwright `setup`;
  2. a Go workflow with `go` and `GOTOOLCHAIN`;
  3. a server with the services above, `memory: 16Gi`;
  4. a `dockerfile:` workflow.
- "The Dockerfile rule": step 3 becomes "system packages in a `USER root` block above the clone; steps that need the checkout after the `mise install` step"; add "Always the same shape: the render, plus your lines in those two places".

`SKILL.md`:
- Step 2's list gains "the language versions the repository pins (for `mise.toml`)".
- Step 3's first sentence becomes "One workflow per buildable unit, all on the one Fugaro base. Write the runtimes into `mise.toml` at the root (or respect the one there), and the image's extras in `image:`."
- Step 3's Services bullet becomes the new services row.
- Step 4 gains "`mise.toml` is part of the same decision".
- Step 6 lists `mise.toml`'s tools as executed text: each is a download.
- Step 7 gains "On Apple silicon, iterate with `--platform linux/arm64`; the cloud build is the amd64 test".
- A new ground rule: "A repository with `base:`, `image.node` or `image.jdk` is a 0.6 configuration: follow docs/base-image-migration.md".
- Step 9's PR holds `mise.toml` too.

`reference/validation.md`:
- the apt row's "The base is Ubuntu 24.04" becomes "The base is Debian 13 (trixie)";
- the "Smoke `node` fails" row becomes "Smoke `mise-tools` fails | A declared tool did not install: check its name and version in `mise.toml` (`mise ls-remote <tool>` lists versions), and that nothing removed the `mise install` step";
- add "Smoke `tool:<name>` fails | The image lost a base tool or harness: restore the Dockerfile's contract";
- the development-build bullet says `images/build-base.sh base`;
- the Apple Silicon bullet points at `--platform linux/arm64`.

- [ ] **Step 5: Run the tests**

Run: `go test ./plugin/ -count=1`
Expected: PASS, including the skills lint.

- [ ] **Step 6: Run the group's suite, commit, open the PR**

Run: `go test ./... 2>&1 | tail -30` (foreground).
Expected: PASS.

```bash
git add plugin
git commit -m "base docs task 16: the setup skill writes mise.toml and one Dockerfile shape"
```

Open the PR, titled "Base image: docs, the migration guide and the setup skill (group 3 of 4)". The body says the docs describe 0.7.0 and the freeze holds until Group 4.

---

## Group 4: the cut (branch `base-cut`)

### Task 17: The config refuses every legacy setting

**Files:**
- Modify: `internal/config/defaults.go`, `config.go`, `image.go`, `validate.go`, `dockerfile.go`, `example.yaml`, `schemas/fugaro.schema.json`, `internal/config/*_test.go`, `testdata/config/**`
- Create: `testdata/config/invalid/legacy-base.yaml`, `legacy-image-node.yaml`, `legacy-image-jdk.yaml`, `legacy-base-dockerfile.yaml`, `testdata/config/valid/services.yaml`
- Delete: `testdata/config/invalid/image-jdk-on-java-services.yaml`, `image-node-on-java-services.yaml`, `image-node-on-jvm.yaml`, `image-skip-build-scripts-on-jvm.yaml`, `image-bad-node.yaml`, `image-tools-on-web-node.yaml`; `testdata/config/valid/java-services.yaml` (replaced by `services.yaml`)

**Interfaces:**
- Produces:
  ```go
  var Bases = []string{BaseKind}
  func (w Workflow) BaseKind() string // always BaseKind
  func LintDockerfile(data []byte) []string // the base parameter is gone
  ```
  `Workflow.Base`, `Image.Node` and `Image.JDK` stay only as tombstones that `Validate` refuses.

- [ ] **Step 1: Write the failing tests** (`internal/config/legacy_test.go`)

```go
package config

import (
	"strings"
	"testing"
)

func TestLegacyBaseRefusedWithTheMigrationDoc(t *testing.T) {
	for _, base := range []string{"go", "web-node", "java-services", "base"} {
		y := strings.Replace(baseYAML, "    commands:", "    base: "+base+"\n    commands:", 1)
		_, problems := Parse([]byte(y))
		if len(problems) != 1 || problems[0].Path != "workflows.app.base" ||
			!strings.Contains(problems[0].Message, "removed in 0.7.0") || !strings.Contains(problems[0].Message, MigrationDoc) {
			t.Errorf("base: %s: %v", base, problems)
		}
	}
}

func TestLegacyImageNodeAndJDKRefused(t *testing.T) {
	y := strings.Replace(baseYAML, "    commands:", "    image: { node: \"24.19.0\" }\n    commands:", 1)
	_, problems := Parse([]byte(y))
	if len(problems) != 1 || !strings.Contains(problems[0].Message, `node = "24.19.0"`) || !strings.Contains(problems[0].Message, MigrationDoc) {
		t.Errorf("image.node: %v", problems)
	}
	y = strings.Replace(baseYAML, "    commands:", "    image: { jdk: \"21\" }\n    commands:", 1)
	_, problems = Parse([]byte(y))
	if len(problems) != 1 || !strings.Contains(problems[0].Message, `java = "temurin-21"`) {
		t.Errorf("image.jdk: %v", problems)
	}
}

func TestLintNamesTheMigrationDocForALegacyFrom(t *testing.T) {
	df := []byte("FROM ghcr.io/dimipaun/fugaro-web-node:0.6.0\nRUN git clone x /work/repo\n")
	msgs := LintDockerfile(df)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "a base removed in 0.7.0") || !strings.Contains(msgs[0], MigrationDoc) {
		t.Errorf("LintDockerfile = %v", msgs)
	}
	if msgs := LintDockerfile([]byte("FROM ghcr.io/dimipaun/fugaro-base:0.7.0\nRUN git clone x /work/repo\n")); len(msgs) != 0 {
		t.Errorf("fugaro-base: %v", msgs)
	}
}

func TestOnlyTheBaseKind(t *testing.T) {
	if len(Bases) != 1 || Bases[0] != BaseKind || (Workflow{Base: "go"}).BaseKind() != BaseKind {
		t.Errorf("Bases = %v", Bases)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/config/ -run 'TestLegacy|TestLint|TestOnlyTheBaseKind' -count=1`
Expected: build failure (`LintDockerfile` takes two arguments). Once that compiles, the refusals are missing.

- [ ] **Step 3: Implement**

`defaults.go`:
- `var Bases = []string{BaseKind}`, with the comment "the base kinds: the one Fugaro base (the legacy kinds were removed in 0.7.0)";
- delete `baseDefaults`, and set the uniform defaults in `applyDefaults` directly:

```go
		if len(w.Commands.Reports) == 0 {
			w.Commands.Reports = []string{"**/junit*.xml", "**/build/test-results/**/*.xml"}
		}
		if w.Resources.CPU == 0 {
			w.Resources.CPU = 4
		}
		if w.Resources.Memory == "" {
			w.Resources.Memory = "8Gi"
		}
```

`config.go`: `BaseKind()` returns `BaseKind`. The comments on `Base`, `Node` and `JDK` become "Removed in 0.7.0: kept so Validate can refuse it with the migration (MigrationDoc)".

`validate.go`, replacing the base check:

```go
		if w.Base != "" {
			add(p+".base", "base: was removed in 0.7.0: every workflow builds on the one Fugaro base image. Delete this line and declare the workflow's runtimes in mise.toml (%s)", MigrationDoc)
		}
```

In `Check`, the `web-node` case goes, and `checkDockerfile` calls `LintDockerfile(data)`.

`image.go`:
- the `Node` block becomes `if img.Node != "" { add(p+".image.node", "was removed in 0.7.0: declare node in mise.toml ([tools] node = %q) or in image.tools (%s)", img.Node, MigrationDoc) }`;
- the `JDK` block becomes `add(p+".image.jdk", "was removed in 0.7.0: declare Java in mise.toml ([tools] java = %q) (%s)", "temurin-"+img.JDK, MigrationDoc)`;
- the `skip_build_scripts` base check and the tools-on-legacy check are deleted;
- `nodeVersionRE` is deleted.

`dockerfile.go`: `LintDockerfile(data []byte) []string`, and the `FROM` switch:

```go
	case m != nil && m[1] == BaseKind:
	case m != nil && slices.Contains([]string{"go", "java-services", "web-node"}, m[1]):
		msgs = append(msgs, fmt.Sprintf("builds FROM fugaro-%s, a base removed in 0.7.0: build FROM ${FUGARO_BASE} (%s)", m[1], MigrationDoc))
	default:
		msgs = append(msgs, fmt.Sprintf("must build its final stage FROM ${FUGARO_BASE} or ghcr.io/dimipaun/fugaro-base, not %s", root))
```

Its callers drop the argument: `internal/config/validate.go`, `internal/image/render_test.go`, `plugin/setup_fixtures_test.go` and `plugin/setup_skill_test.go` (`grep -rn 'LintDockerfile(' --include='*.go' .` lists them).

`schemas/fugaro.schema.json`:
- `base` becomes `{ "not": {}, "description": "Removed in 0.7.0: delete it and declare runtimes in mise.toml (docs/base-image-migration.md)" }`;
- `image.node` and `image.jdk` get the same shape with their own descriptions;
- the `allOf` with the four `if`/`then`s is deleted.

`example.yaml`:
- the `web` workflow loses `base:` and `node:`, and gains `# tools: { node: "24" }   # or a mise.toml at the root (docs/base-image.md)`;
- the server workflow loses `base: java-services`, and its comment becomes `# image: { apt: [postgresql-17, redis-server], setup: ["./gradlew --no-daemon testClasses"] }   # runtimes in mise.toml; services: docs/base-image.md`.

The corpus:

```bash
# Drop every base: line and flow-style base: entry in the config corpus.
grep -rlE 'base: (web-node|go|java-services)' testdata/config | xargs sed -i.bak -E \
  -e '/^[[:space:]]*base: (web-node|go|java-services)[[:space:]]*(#.*)?$/d' \
  -e 's/base: (web-node|go|java-services), //'
# image.node becomes image.tools.
grep -rl 'node: "' testdata/config | xargs sed -i.bak -E 's/(^|[{ ,])node: "([0-9.]+)"/\1tools: { node: "\2" }/'
find testdata/config -name '*.bak' -delete
git rm -q testdata/config/invalid/image-jdk-on-java-services.yaml testdata/config/invalid/image-node-on-java-services.yaml \
  testdata/config/invalid/image-node-on-jvm.yaml testdata/config/invalid/image-skip-build-scripts-on-jvm.yaml \
  testdata/config/invalid/image-bad-node.yaml testdata/config/invalid/image-tools-on-web-node.yaml testdata/config/valid/java-services.yaml
```

Then the new corpus files, each a copy of `valid/minimal.yaml` with one change:
- `invalid/legacy-base.yaml`: `base: web-node` in the workflow;
- `legacy-image-node.yaml`: `image: { node: "24" }`;
- `legacy-image-jdk.yaml`: `image: { jdk: "21" }`;
- `legacy-base-dockerfile.yaml`: `base: go` with `dockerfile: .fugaro/app.Dockerfile`;
- `valid/services.yaml`: the server workflow of `docs/base-image.md` ("System packages and services").

Fix the unit tests in `internal/config` that asserted per-base defaults, `image.node` validation or `LintDockerfile(…, base)`:
- `TestValidateImage*` drops its node and jdk cases, which `legacy_test.go` now covers;
- the defaults tests expect 8Gi everywhere.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/config/ ./schemas/ -count=1`
Expected: PASS. The other packages fail to build or fail tests until Tasks 18 and 19. That is expected inside the group; the group's PR merges only after Task 21.

- [ ] **Step 5: Commit**

```bash
git add -A internal/config schemas testdata/config
git commit -m "base cut task 17: the config refuses base:, image.node and image.jdk, naming the migration doc"
```

---

### Task 18: Render, selftest and the hash on the base alone

**Files:**
- Modify: `images/derived/Dockerfile.tmpl`, `internal/image/render.go`, `render_test.go`, `selftest.go`, `selftest_test.go`, `local.go`, `local_test.go`, `internal/config/nodepm.go`, `internal/imagecheck/record.go`, `record_test.go`, `internal/runner/lockcache.go`, `internal/runner/*_test.go`

**Interfaces:**
- Produces: the base-only render. The `install-node` and `image.node` template block is gone. `SelftestSpec.Node` and its `node` check are gone. `SpecForCloud` always sets `Tools: "critical", Mise: true`. `config.DefaultCache(root string)` loses its `base` argument, and `imageSettings` loses `Node` and `JDK`.

- [ ] **Step 1: Write the failing tests**

In `render_test.go`:
- delete `TestRenderGoBase`, `TestRenderJavaServicesBase` and `TestRenderRefusesUnpublishedBase`'s legacy cases;
- `TestRenderMinimal` and `TestRenderFull` now use `Base: config.BaseKind`, with `Image.Node` removed and `Mise: true` in the full case;
- regenerate their goldens (`wantMinimal` and the full one) from the new output, and read the diff: it must be exactly the removed `install-node` line and the added mise step.

Add:

```go
func TestRenderHasNoInstallNode(t *testing.T) {
	got, err := Render(RenderInput{Workflow: "app", Base: config.BaseKind, Mise: true, Version: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "install-node") {
		t.Error("the template still installs Node by itself")
	}
}
```

In `selftest_test.go`:
- `selftestFixture` drops `Base: "web-node"` and `Node: "24"`;
- `TestSelftestPasses`' list drops `node`;
- `selftestEnv` drops the fake `node` and adds a fake `mise` printing `{}`;
- `TestSpecForCloudOnTheBase` keeps only the base case.

In `record_test.go`:
- `TestKeyFilesDefaultsWebNode` becomes `TestKeyFilesDefaultsNode`, with `webYAML` losing `base: web-node`;
- the `image.node` case of `TestImageConfigHashChanges` becomes an `image.tools` case;
- the canonical-form `Base` comparison uses `BaseKind` against `""`.

In `internal/runner`, the test YAML loses its `base:` lines. Use the same `sed` as Task 17, over `internal/runner/*_test.go`.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/image/ ./internal/imagecheck/ -count=1`
Expected: FAIL. The goldens still carry `install-node`, `Node` is still a field, and `DefaultCache` has two arguments.

- [ ] **Step 3: Implement**

- `Dockerfile.tmpl`: the `{{- if or .Image.Apt .Image.Node}}` block becomes `{{- if .Image.Apt}}`, without the `install-node` line.
- `render.go`:
  - `checkBase` and the package `bases` var are deleted, and `RenderInput.Base` stays for the header only;
  - `Dockerfile` always detects the PM and mise (no kind switch);
  - `Render` refuses a non-empty `Image.Node` with `fmt.Errorf("image.node was removed in 0.7.0 (%s)", config.MigrationDoc)`, for safety.
- `selftest.go`: delete the `Node` field and the `node` block. `SpecForCloud` sets `Base: config.BaseKind, Tools: "critical", Mise: true`.
- `local.go`: the spec is `SelftestSpec{Base: config.BaseKind, …, Tools: "critical", Mise: true}`. `BaseRef(version string)` (the kind argument goes) returns `"ghcr.io/dimipaun/fugaro-base:" + v`, and its dev-build message says `images/build-base.sh base`. Its two callers in `internal/cli/image.go` (lines 122 and 206) become `image.BaseRef(Version)`, and `internal/image/local_test.go`'s `BaseRef` cases follow.
- `nodepm.go`: `DefaultCache(root string)`, without the base check. Its callers are `internal/runner/lockcache.go` and `internal/imagecheck/record.go`.
- `record.go`:
  - `imageSettings` drops `Node` and `JDK`;
  - `defaultCache(tree)` drops the base argument;
  - `ImageConfigHash` always computes `MiseFiles`, and `Base: config.BaseKind`.

- [ ] **Step 4: Run the tests**

Run, in the foreground:
- `go test ./internal/image/ ./internal/imagecheck/ ./internal/config/ -count=1`
- `go test -race ./internal/runner/ -run 'TestLockCache|TestCacheLink|TestPolicy' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A images/derived internal/image internal/imagecheck internal/config internal/runner
git commit -m "base cut task 18: render, selftest and the hash know only the Fugaro base"
```

---

### Task 19: The CLI, the local and shared config, and the check job

**Files:**
- Modify: `internal/localcfg/localcfg.go`, `localcfg_test.go`, `testdata/shared.golden.yaml`; `internal/cli/sharedcfg.go`, `sharedcfg_validate_test.go`; `internal/infra/checkjob.go`, `checkjob_test.go`, `spec_test.go`, `tf/testdata/golden/*.json`; `internal/cli/init.go`, `init_images.go`, `image.go`, `image_refresh_rules.go`, `doctor.go`, `sharedcfg.go`; every `internal/cli/*_test.go`, `internal/e2e/*_test.go` and `internal/backend/gcp/*_test.go` that names a legacy kind; `deploy/sandbox/fugaro.yaml`, `testdata/fixture-repo/fugaro.yaml`, `deploy/terraform/gcp/roots/repo/tests/testdata/*.tfvars.json`
- Regenerate: `internal/infra/tf/testdata/golden/repo*.plan.json` with `scripts/gen-golden-plans.sh`

**Interfaces:**
- Produces:
  - `localcfg` refuses a `base_images` key other than `base` (`legacyBaseImageKey`);
  - `ParseShared` (the read of the published shared config) drops legacy keys and adds a warning for each, through `localcfg.Config.AddWarning`;
  - `infra.RewriteCheckSpec` replaces a spec whose `base_images` names a legacy kind with `{"base": bases["base"]}` and the new image;
  - `doctor` reports a missing `base_images.base`.

- [ ] **Step 1: Write the failing tests**

`internal/localcfg/localcfg_test.go`:

```go
func TestLocalConfigRefusesLegacyBaseImageKeys(t *testing.T) {
	for _, kind := range []string{"go", "web-node", "java-services"} {
		_, err := Parse([]byte(sample + "base_images:\n  " + kind + ": us-east5-docker.pkg.dev/p/fugaro-base/fugaro-" + kind + ":0.6.0\n"))
		if err == nil || !strings.Contains(err.Error(), "base_images."+kind) || !strings.Contains(err.Error(), "fugaro image refresh") ||
			!strings.Contains(err.Error(), config.MigrationDoc) {
			t.Errorf("%s: err = %v", kind, err)
		}
	}
}
```

(`sample` is the file's valid local config, which has no `base_images`.)

`internal/cli/sharedcfg_validate_test.go`:

```go
// A published shared config written before the cut still lists the removed
// kinds: they are dropped with a warning, never a refusal that would lock
// every teammate out until an operator republishes.
func TestSharedConfigDropsLegacyBaseImageKeys(t *testing.T) {
	data := strings.Replace(validShared(), "base_images:\n", "base_images:\n    go: us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-go:0.6.0\n", 1)
	c, err := ParseShared([]byte(data), sharedAnchor)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.BaseImages["go"]; ok {
		t.Errorf("base_images = %v", c.BaseImages)
	}
	ws := strings.Join(c.Warnings(), "\n")
	if !strings.Contains(ws, "base_images.go") || !strings.Contains(ws, "republishes") {
		t.Errorf("warnings = %q", ws)
	}
}
```

`validShared()` and `sharedAnchor` are the file's fixture and anchor. In the sweep, its `base_images` line `web-node: …fugaro-web-node:0.3.1` becomes `base: us-east5-docker.pkg.dev/fugaro-belong/fugaro-base/fugaro-base:0.7.0`, and the refusal cases that name `fugaro-web-node:0.3.1` follow.

`internal/infra/checkjob_test.go`:

```go
func TestRewriteCheckSpecReplacesLegacyKinds(t *testing.T) {
	old := CheckJobSpec{Repo: "acme/app", Registry: "h/p/r", BaseImages: map[string]string{"go": "h/p/fugaro-base/fugaro-go:0.6.0", "web-node": "h/p/fugaro-base/fugaro-web-node:0.6.0"}}
	raw, _ := json.Marshal(old)
	spec, image, err := RewriteCheckSpec(string(raw), "h/p/fugaro-base/fugaro-go:0.6.0", map[string]string{"base": "h/p/fugaro-base/fugaro-base:0.7.0"})
	if err != nil {
		t.Fatal(err)
	}
	var got CheckJobSpec
	if err := json.Unmarshal([]byte(spec), &got); err != nil {
		t.Fatal(err)
	}
	if image != "h/p/fugaro-base/fugaro-base:0.7.0" || !maps.Equal(got.BaseImages, map[string]string{"base": "h/p/fugaro-base/fugaro-base:0.7.0"}) {
		t.Errorf("image %s, base_images %v", image, got.BaseImages)
	}
	if got.Repo != "acme/app" || got.Registry != "h/p/r" {
		t.Errorf("other fields changed: %+v", got)
	}
}
```

`internal/cli/doctor_test.go`:

```go
// With no base_images.base, doctor names the one fix, as a warning: an
// installation mid-migration still works for everything else.
func TestDoctorSaysWhenTheBaseIsMissing(t *testing.T) {
	newDoctorRig(t)
	out, _, _ := execute(t, "doctor", "--json", "--dir", t.TempDir())
	var o doctorOutput
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	for _, c := range o.Checks {
		if c.ID == "base-image" {
			if c.OK || c.Severity != "warning" || !strings.Contains(c.Fix, "fugaro image refresh") {
				t.Errorf("base-image = %+v", c)
			}
			return
		}
	}
	t.Errorf("no base-image check in %+v", o.Checks)
}
```

(The rig's local config has no `base_images`. If it gains one in the sweep, this test clears it.)

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/localcfg/ ./internal/infra/ -run 'Legacy' -count=1` and `go test ./internal/cli/ -run 'TestSharedConfigDropsLegacy|TestDoctorSaysWhenTheBase' -count=1`
Expected: FAIL. Today the local config refuses `go` with the generic message, the shared config refuses it outright, and `RewriteCheckSpec` keeps `go`, because it moves only the kinds present in `bases`. There is no `base-image` doctor check yet.

- [ ] **Step 3: Implement**

`localcfg.go`, in the `base_images` loop:

```go
		case !slices.Contains(config.Bases, kind):
			bad("%s", legacyBaseImageKey(kind))
```

with

```go
// legacyBaseImageKey refuses a base_images key of a base removed in 0.7.0
// (or any other unknown kind), with the one fix.
func legacyBaseImageKey(kind string) string {
	return fmt.Sprintf("base_images.%s: the go, web-node and java-services bases were removed in 0.7.0; delete the line and run fugaro image refresh in each repository's checkout, which copies fugaro-base and records base_images.base (%s)", kind, config.MigrationDoc)
}
```

`oldBaseImageKey` (the pre-map refusal) now says `base_images: {base: %s}`. `BaseImageNameKind` and `ParseBaseImageFlag` are unchanged in code; their messages list `base`.

`localcfg.go` gains `func (c *Config) AddWarning(w string)`, which appends to a new unexported `notes []string` that `Warnings()` returns after its own. `internal/cli/sharedcfg.go`, in `ParseShared`, replaces `c, err := localcfg.Parse(data)` with:

```go
	data, dropped, err := dropLegacyBaseImages(data)
	if err != nil {
		return refuse("is not valid YAML: %v", err)
	}
	c, err := localcfg.Parse(data) // strict: an unknown key is refused here
```

and, after its existing error handling:

```go
	for _, k := range dropped {
		c.AddWarning(fmt.Sprintf("the project's shared config still lists base_images.%s, a base removed in 0.7.0; ignored (the next fugaro init or fugaro image refresh republishes the shared config)", k))
	}
```

with

```go
// dropLegacyBaseImages removes the base_images entries of kinds removed in
// 0.7.0 from a published shared config, whose writer may be an older
// fugaro: one stale publish must not lock every teammate out (design
// base-image.md section 10). It returns the data unchanged when nothing
// was dropped.
func dropLegacyBaseImages(data []byte) ([]byte, []string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return data, nil, nil
	}
	top := doc.Content[0]
	var dropped []string
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != "base_images" || top.Content[i+1].Kind != yaml.MappingNode {
			continue
		}
		m := top.Content[i+1]
		var kept []*yaml.Node
		for j := 0; j+1 < len(m.Content); j += 2 {
			if slices.Contains(config.Bases, m.Content[j].Value) {
				kept = append(kept, m.Content[j], m.Content[j+1])
			} else {
				dropped = append(dropped, m.Content[j].Value)
			}
		}
		m.Content = kept
	}
	if len(dropped) == 0 {
		return data, nil, nil
	}
	out, err := yaml.Marshal(&doc)
	return out, dropped, err
}
```

`MergeShared` (the publish side) needs no change: the next publish writes the local config's map, which after the cut holds only `base`.

`checkjob.go`, at the top of `RewriteCheckSpec` after unmarshalling:

```go
	legacy := false
	for k := range s.BaseImages {
		if !slices.Contains(config.Bases, k) {
			legacy = true
		}
	}
	if legacy && bases[config.BaseKind] != "" {
		// The 0.7.0 cut: a spec of the removed kinds becomes the one base,
		// with the job's image, in this one update (design base-image.md
		// section 10).
		s.BaseImages = map[string]string{config.BaseKind: bases[config.BaseKind]}
		out, err := json.Marshal(s)
		if err != nil {
			return "", "", err
		}
		return string(out), bases[config.BaseKind], nil
	}
```

The strict-form checks below it still run for the non-legacy path. (`config` is imported in `infra` already; if not, add it.)

`doctor.go`, right after `o.Project = …`: `o.Checks = append(o.Checks, doctorBaseImage(lc))`, with

```go
// doctorBaseImage warns when the installation has no Fugaro base recorded
// (design base-image.md section 10): every image build starts from it.
func doctorBaseImage(lc *localcfg.Config) doctorCheck {
	if lc.BaseImage(config.BaseKind) != "" {
		return doctorCheck{ID: "base-image", OK: true}
	}
	return doctorCheck{ID: "base-image", OK: false, Severity: "warning",
		Problem: "the local config has no base_images.base (" + config.MigrationDoc + ")",
		Fix:     "fugaro image refresh   # in the repository's checkout"}
}
```

(`checksFail` fails on a `warning` only with `--strict`, so an installation in mid-migration still passes `doctor`.)

`init.go`'s flag help:
- `--base`: "copy the release's base image (the only kind: base) into the project's registry now, besides what the checkout's fugaro.yaml needs";
- `--expect-digest`: "KIND is base or history";
- `--replace-image`: "history or base".

`image.go`: the `--base` help says "the published fugaro-base" (`image.BaseRef(Version)` changed in Task 18).

**The test sweep.** Every test that names a legacy kind, in YAML or in Go:

```bash
files=$(grep -rlE 'web-node|java-services|fugaro-go|"go",|base: go' --include='*_test.go' internal deploy | sort)
sed -i.bak -E \
  -e '/^[[:space:]]*base: (web-node|go|java-services)[[:space:]]*$/d' \
  -e 's/base: (web-node|go|java-services), //' \
  -e 's/fugaro-(web-node|go|java-services)/fugaro-base/g' \
  -e 's/"--base", "(go|web-node|java-services)(,(go|web-node|java-services))*"/"--base", "base"/g' \
  -e 's/BaseImages\["(go|web-node|java-services)"\]/BaseImages["base"]/g' \
  -e 's/BaseImage\("(go|web-node|java-services)"\)/BaseImage("base")/g' \
  $files
find internal deploy -name '*_test.go.bak' -delete
```

Then run `go test ./internal/... 2>&1 | grep -E '^(--- FAIL|FAIL|ok)'`. Rewrite each remaining failure to the one-kind world. That means a test whose point was two kinds keeps its point with `base` and `history`. A test of a rule that no longer exists is deleted. For each one, append a line to `/tmp/deleted-tests.txt` ("Deleted <test name>: it covered <the rule>"). Step 5's commit uses that file as its body. Three named rewrites:
- `TestBaseImagesRecordedInConfig` uses `--base base` and expects `{"base": want+"fugaro-base:1.2.3"}`, with `len(bi) == 1`;
- `TestCheckUsesTheBaseOfEachCheckedKind` becomes `TestCheckJobOnTheBaseKind` (already written in Task 13), and the old test is deleted;
- `newImagesRigFor` publishes `fugaro-history` and `fugaro-base` only.

The same `sed` runs over `deploy/sandbox/fugaro.yaml` and `testdata/fixture-repo/fugaro.yaml`. Then:
- the sandbox's `fugaro.yaml` gets a `mise.toml` beside it (`[tools]\nnode = "24"`), which the owner commits to the sandbox repository with the live check;
- `deploy/terraform/gcp/roots/repo/tests/testdata/*.tfvars.json` and `internal/localcfg/testdata/shared.golden.yaml` replace their `web-node` and `java-services` keys with `base`;
- `sh scripts/gen-golden-plans.sh` regenerates the plan goldens. Read their diff: it must be only `base_images` keys and image names.

- [ ] **Step 4: Run the tests**

Run, in the foreground:
- `go test ./internal/localcfg/ ./internal/infra/... ./internal/mirror/ ./internal/backend/... -count=1`
- `go test ./internal/cli/ -count=1` (about 15 minutes)
- `go test ./internal/e2e/ -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A internal deploy testdata
{ echo "base cut task 19: one base kind in the CLI, the local and shared config, and the check job"; echo; cat /tmp/deleted-tests.txt; } > /tmp/task19-msg.txt
git commit -F /tmp/task19-msg.txt
```

---

### Task 20: Delete the legacy images, their CI and their release lines

**Files:**
- Delete: `images/go/`, `images/web-node/`, `images/java-services/`, `images/go_test.go`, `images/java_services_test.go` (the services tests moved in Task 3), the legacy parts of `images/smoke.sh` and `images/smoke_test.go`
- Modify:
  - `images/images.go` (its package comment);
  - `images/base_docker_test.go` (`TestBaseImageSmoke` and `TestBaseImageManagedSettingsDir` run on `base`; `TestInstallNodeUsesTheBakedKeyring` uses `BaseImageOf(t, "base")` with `NODE_PREFIX=/opt/fugaro/node`);
  - `internal/image/docker_test.go` (every `testutil.BaseImage(t)` becomes `testutil.BaseImageOf(t, "base")`, every inline `fugaro.yaml` loses `base:`, and a `mise.toml` with `node = "24"` is added to each Node fixture);
  - `internal/testutil/docker.go` (`BaseImage` deleted);
  - `.github/workflows/images.yml` (the `base` matrix job and the legacy part of `publish` deleted; `kinds` lists `history base`; `verify-public` loops over `history` only, then the `--arm64` call);
  - `.goreleaser.yaml` (the three legacy lines deleted);
  - `images/verify-public.sh` (the header);
  - `.github/dependabot.yml` (the comment);
  - `docs/release.md`.
- Create: `images/legacy_names_test.go`

**Interfaces:**
- Produces: the repository with no current legacy kind. The images workflow's jobs are `version`, `history`, `fugaro-base (amd64)`, `fugaro-base (arm64)`, `publish` (history), `publish-base`, `verify-public` and `docker-tests`.

- [ ] **Step 1: Write the failing test**

`images/legacy_names_test.go`:

```go
package images_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// After the 0.7.0 cut, no file presents a removed base as current. History
// (plans, old release notes, the design docs' history) and the migration
// guide may name them.
func TestNoLegacyBaseOutsideHistory(t *testing.T) {
	legacy := regexp.MustCompile(`web-node|java-services|fugaro-go\b|base: go\b`)
	// A line that says the bases were removed (a refusal, a test of one) is
	// about the cut, not a current use.
	about := func(line string) bool { return strings.Contains(line, "0.7.0") || strings.Contains(line, "MigrationDoc") }
	allowed := []string{
		"docs/plans/", "docs/releases/", "docs/design/", "docs/base-image-migration.md", "docs/base-image.md",
		"images/legacy_names_test.go", "internal/config/legacy_test.go", "internal/localcfg/localcfg.go", "internal/config/dockerfile.go",
		"internal/localcfg/localcfg_test.go", "internal/cli/sharedcfg_validate_test.go", "internal/infra/checkjob_test.go",
		"testdata/config/invalid/legacy-", "plugin/testdata/repos/existing-fugaro-yaml/repo/fugaro.yaml",
		"plugin/testdata/repos/web-node/", "plugin/testdata/repos/java-services/", "plugin/setup_fixtures_test.go",
		".git/", ".worktrees/", "internal/cli/docs_base_test.go",
	}
	root := ".."
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator)))
		for _, a := range allowed {
			if strings.HasPrefix(rel, a) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if d.IsDir() || strings.HasSuffix(rel, ".png") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if legacy.MatchString(line) && !about(line) {
				t.Errorf("%s:%d names a removed base: %s", rel, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
```

(The fixture directories named `web-node` and `java-services` are kept as fixture names describing where a repository came from. Only the directory names are allowed: their contents are checked through the setup tests.)

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./images/ -run TestNoLegacyBaseOutsideHistory -count=1`
Expected: FAIL, listing `images/web-node/Dockerfile`, `images.yml`, `.goreleaser.yaml`, `README.md` lines and so on.

- [ ] **Step 3: Implement**

```bash
git rm -r -q images/go images/web-node images/java-services images/go_test.go images/java_services_test.go
```

- `images/smoke.sh`: delete the `web-node`, `go` and `java-services` cases and `check_node`. The header describes the `base` and `history` checks only. `images/smoke_test.go` drops the fake's `node`, `corepack`, `go`, `terraform`, `make`, `gcc`, `java`, `psql`, `redis-server` and `firebase` cases and the tests that used them.
- `images.yml`:
  - delete the `base` job;
  - `publish`'s matrix becomes `base: [history]` (and `needs: [version, history]`);
  - `verify-public`'s loop is `for img in history; do`;
  - `TestImagesWorkflowBuildsAndPublishesEveryBase` becomes `TestImagesWorkflowBuildsTheBaseAndHistory`, asserting the job ids `history`, `fugaro-base`, `publish` and `publish-base`;
  - `TestImagesWorkflowPublishesAndVerifiesEveryImage` expects `publish`'s matrix `[history]`.
- `.goreleaser.yaml`: the footer lists `fugaro-base` and `fugaro-history` only.
- `docs/gcp-live-checklist.md`: its checks that name `web-node` (the sandbox's `web` workflow) say "the sandbox's `web` workflow (Node, from its `mise.toml`)".
- Every other hit the test lists is fixed or, for a history file, added to the test's allow-list with the reason in a comment.

- [ ] **Step 4: Run the tests**

Run: `go test ./images/ ./scripts/ -count=1` and `go vet -tags docker ./...`
Expected: PASS, and vet is clean.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "base cut task 20: delete the go, web-node and java-services images, their CI and release lines"
```

---

### Task 21: The full suite and the PR

**Files:** none.

- [ ] **Step 1: Run everything, in the foreground**

Run: `go build ./... && go vet ./... && go vet -tags docker ./... && test -z "$(gofmt -l .)" && go test ./... 2>&1 | tail -40` (about 30 minutes).
Expected: PASS everywhere.

- [ ] **Step 2: Self-check the cut**

Run: `grep -rnE 'web-node|java-services|fugaro-go' --include='*.go' --include='*.yml' --include='*.yaml' --include='*.json' --include='*.sh' --include='*.tmpl' . | grep -v -e docs/plans -e docs/releases -e testdata/config/invalid/legacy- -e _test.go`
Expected: only the refusal messages in `internal/config/dockerfile.go` and `internal/localcfg/localcfg.go`.

- [ ] **Step 3: Open the PR**

Open the PR, titled "Base image: remove the go, web-node and java-services bases (group 4 of 4, the 0.7.0 cut)". Its body lists:
- what is refused and the migration doc;
- the tests deleted with the rules they covered;
- the CI jobs that changed names (the `base (…)` legacy jobs are gone, `fugaro-base (amd64|arm64)` stay);
- that the release freeze ends with Task 22.

Read every CI job's log: `test`, `rules`, `terraform`, `history`, `fugaro-base (amd64)`, `fugaro-base (arm64)`, `docker-tests`.

---

## Task 22: Release 0.7.0, Fugaro's switch, and the owner's checklist

Run after Group 4's merge, with the owner's go-ahead.

**Files:**
- Create: `docs/releases/v0.7.0.md`
- Modify (after the release): `fugaro.yaml` (Fugaro's own)

- [ ] **Step 1: Measure before releasing (the owner, sandbox)**

The cold-start check of design section 13. On the sandbox installation, with the current (0.6) image of the sandbox's `web` workflow:
1. Launch one run and note the time from `fugaro run` to the run's first runner log line (the `started` line `fugaro logs` shows).
2. After the release and the sandbox's migration (Step 4), launch one more run of the same task and compare.

The result goes into `docs/gcp-live-checklist.md` as a new check. If it is more than 30 seconds worse, the fallback (B16: gcloud to on-demand) becomes a 0.7.1 task.

- [ ] **Step 2: Write `docs/releases/v0.7.0.md`**

```markdown
## Highlights

**One base image, runtimes from mise.** The `go`, `web-node` and `java-services` bases are replaced by one base, `ghcr.io/dimipaun/fugaro-base` (linux/amd64 and linux/arm64): Debian 13 with a large tool kit, gcloud, the Docker CLI, mise and eight agent harnesses (only Claude Code is driven by Fugaro). Your repository declares its runtimes in `mise.toml` (or `image.tools`); the image installs them. See docs/base-image.md.

**This is a breaking release.** A 0.7.0 CLI refuses `base:`, `image.node`, `image.jdk` and the old `base_images` keys, naming docs/base-image-migration.md; a 0.6 CLI refuses the new `fugaro.yaml`. Move each repository in this order: upgrade the CLI; one PR with `mise.toml` and the `fugaro.yaml` change (the `/fugaro:setup` skill writes both); merge it; run `fugaro image refresh` in the checkout at once, and don't launch runs until it finishes. The base copy is about 1.2 GB through your machine.

**No sudo, still.** The agent has no root at run time. Bake system packages with `image.apt`; install a standalone tool mid-run with `mise use -g`.
```

- [ ] **Step 3: Release**

`/new-release 0.7.0`. Verify that the `images` run published `fugaro-base:0.7.0` as an index with both architectures (`verify-public` passed), and that the release footer lists `fugaro-base` and `fugaro-history`.

- [ ] **Step 4: Fugaro's own switch (the owner's installation)**

In a worktree of Fugaro, on a branch, delete `base: go` from `fugaro.yaml`. `mise.toml` is already there (Task 14). Run `fugaro validate` and `fugaro image build --local`, open the PR, merge it, and run `fugaro image refresh` at once (the owner, in their terminal). Then launch one small dogfood run in a worktree.

- [ ] **Step 5: Hand the Belong checklist to the owner**

The checklist in `docs/base-image-migration.md` ("Checklist for a repository with services") is the owner's, for the Belong React frontend and the Belong server, sandbox first. Fugaro's agents never touch EdgeWeb or EdgeServer.

---

## Self-review

Checked against the design ([design/base-image.md](../design/base-image.md)):

| Design section | Covered by |
|---|---|
| §2 one base, kind `base`, `base:` removed | Tasks 2, 9, 13, 17 |
| §3 contents and layering, the audit | Task 2 (Dockerfile and its static test), Task 7 |
| §4 the mise contract, `image.tools`, the template, monorepos | Tasks 2 (system config), 9, 10, 12, 16 |
| §5 D1: user, sudo, setuid; services | Tasks 2, 3, 7, 12 |
| §6 harnesses, the presence table, credential files, injection docs | Tasks 1, 2, 12, 15 |
| §7 D3: pins, the bump, the secret scan | Tasks 2, 5, 8 |
| §8 hash, key files, defaults, selftest | Tasks 9, 11, 12, 18 |
| §9 security | Tasks 2, 5, 7, 12 (the agent environment is unchanged: no task touches `internal/agent/env.go`) |
| §10 D2: the cut, messages, shared config, check job, migration doc, upgrade order | Tasks 15, 17, 18, 19, 20, 22 |
| §11 bring your own | Task 15 (`docs/base-image.md`) |
| §12 multi-arch: runners, index, mirror, local platform | Tasks 6, 12 (`TestBuildLocalPassesThePlatformToSelftest`), 13 (`TestMirrorLeavesTheArm64ImageBehind`) |
| §13 size, build time, cold start | Tasks 4 (budget), 6 (compressed sizes), 22 (measurement) |
| §14 the setup skill | Task 16 |
| §15 dogfood | Tasks 14, 22 |
| §16 rollout, freeze, layered config | Global Constraints, PR groups, Task 22 |

**Type consistency.** These names are used the same way everywhere:
- `config.BaseKind` (`"base"`), `Workflow.BaseKind()`, `config.Bases`, `config.MigrationDoc`;
- `Image.Tools`, `config.ValidMiseTool`, `config.MiseConfigFiles`, `MiseConfigPaths`, `MiseConfigDirs`, `MiseLockfile`;
- `RenderInput.Mise`;
- `SelftestSpec.Tools`, `ToolsOnly` and `Mise`, `images.BaseTools`, `image.ParseTools`, `checkTools`, `checkMise`;
- `testutil.BaseImageOf`;
- `LintDockerfile(data)` from Task 17 on (`(data, base)` before it).

**Placeholders.** None:
- every pin is a real value read on 2026-10-08, except the harness lockfile, which Task 2 generates with the given command;
- every script and workflow is written out;
- the one sweep (Task 19) gives its exact `sed`, the rule for what remains, and three named rewrites.

**Known limits, said in the tasks:**
- Docker-only verification (Tasks 2, 6, 7, 8, 12, 20) happens in CI, never in a run;
- the setuid observation on Debian 13 (Task 7) may adjust two allowlists;
- a harness whose `--version` needs something the presence check lacks changes its row to `-` with a comment, in the PR that finds it.
