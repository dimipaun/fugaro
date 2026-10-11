# Moving to the Fugaro base (0.7.0)

Fugaro 0.7.0 replaces the `go`, `web-node` and `java-services` bases with one base image (see [base-image.md](base-image.md)). There is no transition release: a 0.7.0 CLI refuses `base:`, `image.node` and `image.jdk`, and a 0.6 one refuses a `fugaro.yaml` without `base:`. So each repository moves in one pull request, and the installation follows right after, in the order below.

**Not enforced yet.** The refusals this document describes (`base:`, `image.node`, `base_images.go`/`web-node`/`java-services`) arrive together with the 0.7.0 release; the work that adds them is in flight in this tree but not merged. Until 0.7.0 ships, `base:` still accepts `go`, `web-node` and `java-services`, and `image.node` still works on `web-node`. This guide is written ahead of that enforcement, so every repository can move in one step once it lands; `image.jdk` is the one exception already refused today, on every base.

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
