# Moving to the Fugaro base (0.7.0)

Fugaro 0.7.0 replaces the `go`, `web-node` and `java-services` bases with one base image (see [base-image.md](base-image.md)). There is no transition release: a 0.7.0 CLI refuses `base:`, `image.node` and `image.jdk`, and a 0.6 one refuses a `fugaro.yaml` without `base:`. So each repository moves in one pull request, and the installation follows right after, in the order below.

**Already enforced in this tree, not yet in a release.** The base-image plan's hard cut (Task 17) is merged on this branch: a `fugaro` built from this source already refuses `base:`, `image.node` and `image.jdk` in any `fugaro.yaml` it validates, ahead of a 0.7.0 release tag. **This repository's own `fugaro.yaml` is the one deliberate exception**: its `go` workflow still reads `base: go` (see "This repository's own dogfood run" below) — don't take that as evidence the refusal is inactive elsewhere.

## This repository's own dogfood run is broken until Task 22

Fugaro's own `fugaro.yaml` (project `fugaro`, workflow `go`) still reads `base: go` — deliberately not migrated yet: Task 22 ("Release 0.7.0, Fugaro's switch") reserves that flip for the owner, run only after `fugaro-base` is published and `fugaro image refresh` has moved this checkout onto it (today, `fugaro-base` is not published: the `images.yml` workflow's `fugaro-base`/`publish-base` jobs exist, but no release has triggered them yet).

**From the moment the base-image hard cut (Task 17) is in this checkout, `fugaro run --workflow go "<task>"` (docs/dogfooding.md) fails immediately at the config-validate stage, before any agent work starts**, with `workflows.go.base: base: was removed in 0.7.0: ... (docs/base-image-migration.md)`. This is not a bug in the cut: it is `internal/config`'s refusal doing exactly what it is meant to do, applied to a `fugaro.yaml` nobody has migrated yet. It does mean no self-hosted Fugaro run can execute against this repository's `go` workflow between the cut landing and Task 22's flip — any further work on this plan's remaining tasks in that window has to be done directly (a local session or a person, not `fugaro run`), exactly as `docs/dogfooding.md`'s "What went through runs, and what did not" section already describes for security-sensitive code.

## The order

1. Every operator upgrades the CLI to 0.7.0 (`fugaro upgrade --local`, then Homebrew for the binary).
2. Per repository, one pull request: the `mise.toml` and the `fugaro.yaml` change below (the `/fugaro:setup` skill writes both). Before opening it, `fugaro validate` must pass and `fugaro image build --local` must pass the smoke test.
3. Merge it, then **at once**, in that checkout: `fugaro image refresh`. It copies `fugaro-base` into your registry, records `base_images.base`, moves the daily check job onto it, and rebuilds every workflow's image. **This step needs `fugaro-base` to be published on ghcr.io first** (not yet true as of this writing — `docs/pending-workflows/README.md`); until then `fugaro image refresh` finds no release image and refuses with a clear error.
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

`image.jdk` was refused on every base (already true today). Declare Java in `mise.toml` (`java = "temurin-21"`, `java = "zulu-17"`, …). `base:` and `image.node` get the same treatment once 0.7.0 ships (see the note at the top); the fixes above apply either way.

## Checklist for a repository with services (Belong-style)

Sandbox first, then the real repository:
- [ ] `fugaro validate` passes; `fugaro image build --local --platform linux/arm64` (Apple silicon) then `--platform linux/amd64` pass the smoke test, `mise-tools` included.
- [ ] A frontend that uses Playwright: `npx playwright install --with-deps chromium` succeeds in `image.setup` on Debian 13.
- [ ] A server: `fugaro-services start` brings up PostgreSQL with `CREATE EXTENSION postgis` and `vector`, Redis answers `PONG`, and the emulators the tests use listen on loopback.
- [ ] One sandbox run, end to end, then the real repository's PR, then `fugaro image refresh`.
