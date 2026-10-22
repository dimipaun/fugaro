# Moving to the Fugaro base (0.7.0)

Fugaro 0.7.0 replaces the `go`, `web-node` and `java-services` bases with one base image (see [base-image.md](base-image.md)). There is no transition release: a 0.7.0 CLI refuses `base:`, `image.node` and `image.jdk`, and a 0.6 one refuses a `fugaro.yaml` without `base:`. So each repository moves in one pull request, and the installation follows right after, in the order below.

**Released in 0.7.0.** A 0.7.0 `fugaro` refuses `base:`, `image.node` and `image.jdk` in any `fugaro.yaml` it validates, and `fugaro-base` is published on ghcr.io from the 0.7.0 tag.

## This repository's own switch

Fugaro's own `fugaro.yaml` (project `fugaro`, workflow `go`) dropped its `base: go` line in the change that follows the 0.7.0 release; its runtimes were already in the repository's `mise.toml`. After that change merges, run `fugaro image refresh` in the checkout (in your own terminal) before launching a run: the old 0.6 job image refuses the new `fugaro.yaml` until its image is rebuilt on `fugaro-base`, and the command takes a few minutes.

## The order

1. Every operator upgrades the CLI to 0.7.0 (`fugaro upgrade --local`, then Homebrew for the binary).
2. Per repository, one pull request: the `mise.toml` and the `fugaro.yaml` change below (the `/fugaro:setup` skill writes both). Before opening it, `fugaro validate` must pass and `fugaro image build --local` must pass the smoke test.
3. Merge it, then **at once**, in that checkout: `fugaro image refresh`. It verifies the release's signed digest and pins it through your registry's remote repository, records `base_images.base`, moves the daily check job onto it, and rebuilds every workflow's image — nothing is copied. **This step needs `fugaro-base` to be published on ghcr.io first**, which the 0.7.0 release tag does; before it (a development build, or an earlier release) `fugaro image refresh` finds no release image and refuses with a clear error.
4. **Between the merge and the refresh finishing, don't launch runs**: the old image's 0.6 runner refuses the new `fugaro.yaml`. It takes minutes.
5. `fugaro doctor` in the checkout reports nothing about bases.

The local config: delete every `base_images` line other than `base`; `fugaro image refresh` writes `base_images.base`. A 0.7.0 CLI refuses a local config with `base_images.go`, `base_images.web-node` or `base_images.java-services`, naming this doc. Old entries in the project's shared config are meant to be dropped with a warning until the next `fugaro setup` or `fugaro image refresh` republishes it; **in this tree today they are refused outright, the same as the local config**, since `internal/localcfg` does not yet parse a published shared config any differently from a local one. Until that split lands, an installation whose shared config still has a legacy `base_images` key cannot parse its own config at all.

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

- List only the services and emulators your tests use, and set `FUGARO_SERVICES` to match (space separated) if it is not all three: `fugaro-services start`'s default, `postgres redis firebase`, is unchanged, so installing fewer than all three without restricting `FUGARO_SERVICES` tries to start the ones you didn't install too.
- `fugaro-services start` works as before: it tries every service `FUGARO_SERVICES` names (every one, by default) and fails, naming the apt package or mise tool to add, on whichever one you did not install.
- `memory: 16Gi` was the base's default; write it explicitly.
- Gradle still comes from your wrapper.

## `image.jdk` and other removed settings

`image.jdk`, `base:` and `image.node` are all already refused in this tree (see the note at the top), not merely scheduled for 0.7.0: `image.jdk` was refused on every base even before the cut; `base:` and `image.node` are refused now too. Declare Java in `mise.toml` (`java = "temurin-21"`, `java = "zulu-17"`, …); the fixes above apply either way.

## What happens to the already-published `fugaro-go`/`fugaro-web-node`/`fugaro-java-services` tags

They stay on `ghcr.io` forever: a published tag is immutable history, never deleted. With 0.7.0 the nightly workflow stops refreshing them: there is no `base` build job left to run, so no release after that point republishes a `:X` moving tag for any of the three, and no `fugaro` command accepts `go`, `web-node` or `java-services` as a kind any more (not even `--expect-digest`, which only takes `base` and `history`) to pin one by hand.

## Checklist for a repository with services (Belong-style)

Sandbox first, then the real repository:
- [ ] `fugaro validate` passes; `fugaro image build --local --platform linux/arm64` (Apple silicon) then `--platform linux/amd64` pass the smoke test, `mise-tools` included.
- [ ] A frontend that uses Playwright: `npx playwright install --with-deps chromium` succeeds in `image.setup` on Debian 13.
- [ ] A server: `fugaro-services start` brings up PostgreSQL with `CREATE EXTENSION postgis` and `vector`, Redis answers `PONG`, and the emulators the tests use listen on loopback.
- [ ] One sandbox run, end to end, then the real repository's PR, then `fugaro image refresh`.
