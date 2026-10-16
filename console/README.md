# console/

**Nothing under `console/` is importable by, or imports, the Go module.**

This directory holds the Fugaro desktop console — the Electron application the
owner's spec asks for in its section 6, designed in
[docs/design/desktop-console.md](../docs/design/desktop-console.md) (which placed
it here under `console/`, D3) and built by
[docs/plans/2026-10-15-desktop-console.md](../docs/plans/2026-10-15-desktop-console.md).

It is not a Go package and it never becomes one. `go build ./...`,
`go vet ./...` and `gofmt -l .` take no notice of it, `go.mod` and `go.sum` gain
nothing from it, and no Go file in this module builds against a path under
`console/` (design §8.1 item 4). Three Go tests hold that, in
`internal/cli/docs_console_boundary_test.go`:
`TestConsoleIsOutsideTheGoModule`, `TestGoModHasNoConsoleDependency` and
`TestNoGoFileEmbedsFromConsole`. A `.go` file or a `go.mod` in this directory, an
import of a `console/` path, a `go:embed` over one, or a require line added to
the root module without the baseline being edited fails one of them by name.

One Go file may *mention* a `console/` path without importing anything from it,
because the console's JSON contract is a document a test reads as bytes:
`internal/cli/docs_desktop_console_test.go`, which reads
`console/docs/json-contract.md`. It is the single entry on that boundary test's
allow-list; a second entry is a deliberate edit to the list, with the case for it
written down, rather than a quiet new reference somewhere else.

That mention check is a heuristic, and it is honest about both halves. Its floor
is the plan's literal rule: a Go file whose text contains the string `console/`
is a mention, wherever that string appears — so a string like
`"github.com/x/console/y"` counts too, and the answer is an allow-list entry or a
reworded string, decided on purpose. Above the floor, the file is parsed and read
as paths, which catches the spellings no single literal holds: a `console` *path
segment* next to `docs`, written as a directory, inside a
`filepath.Join("..", "..", "console", "docs", "x")` argument list, or written
out as the bare relative path `"../../console"` — that last one holds no
`console/` for the floor to find and is neither an import nor a `go:embed`
pattern, so the detector is the only thing that sees it. What the detector does
not do: follow a path assembled from variables, or prove that a `console` segment
is *this* repository's console. The limits are on the safe side — a missed
spelling costs nothing until the file does something with the path, and both the
import check and the `go:embed` check see a path that is actually used — and an
over-reported one costs an entry in the list, said out loud.

The scan reads every `.go` file of the module except two kinds of directory: a
name beginning with `.` or `_` (ignored by the go tool everywhere, so a nested
checkout under `.worktrees/` or `.claude/worktrees/` cannot fail it) and one
`go.mod`'s `ignore` directive names. `node_modules` is neither unless `go.mod`
says so — the go tool descends into it otherwise, `go list ./...` prints its
packages as this module's, and so a `.go` file put under
`console/node_modules/` is a `.go` file in this directory and fails a test by
name. `testdata/` is read deliberately.

What is here today: this file, the contract the console reads
(`docs/json-contract.md`, pinned by plan Task 0.1), and the pinned toolchain
(plan Task 1.0): `package.json`, `package-lock.json`, `tsconfig.json`,
`playwright.config.ts`, `.npmrc`, `.gitignore`, and the three unit tests that
hold the pinning rule, the lockfile, and the typecheck's coverage of the root
config files (`test/unit/{pinning,lockfile,tsconfig}.test.ts`).

## The toolchain, pinned (Task 1.0)

**Electron is pinned to exactly `44.7.2`, and it is bumped on purpose**, with the
toolchain task and the lockfile in the same commit — the same rule
`internal/budget/rules/emulator/package.json` states for its own `firebase-tools`
pin. Electron is not an ordinary dependency: it pulls a large prebuilt runtime
from its own download host rather than from npm, so the pin, the download and
the checksum get the hand-bumped, documented treatment the repository already
gives the Trivy digest and the Dockerfile frontend digest (design section 5.2).

The rest of the pinned set, all exact. One of them is a runtime dependency and
the rest are build and test tooling:

| Package | Version | Where | Why it is here |
|---|---|---|---|
| `electron` | `44.7.2` | `devDependencies` | the app's runtime, bumped on purpose |
| `typescript` | `7.0.2` | `devDependencies` | `tsc --noEmit`; the type checker, not the build |
| `vitest` | `5.0.5` | `devDependencies` | `npm run test:unit` |
| `@playwright/test` | `1.64.1` | `devDependencies` | `npm run test:e2e`, Electron driven by `electron.launch` (Task 1.1) |
| `@types/node` | `22.20.5` | `devDependencies` | the Node the main process and the tests run on |
| `electron-updater` | `6.8.9` | `dependencies` | the auto-update client (Task 4.4) — the one runtime dependency, pinned here because the plan puts its pin in this task: "the component that downloads and installs code" |

`electron-updater` is the only entry in `dependencies`, and that is a decision
rather than a symptom: the renderer bundles no framework runtime and no charting
library (design D9), so nothing the renderer needs is a runtime dependency, and
the pinning test asserts the `dependencies` set is exactly the one name above.
`6.8.9` is the version npm's `latest` tag resolves to; `6.8.10` is published
under a `v26` tag and is deliberately not taken here.

**The Node is pinned exactly too: `engines.node` is `22.23.3`, not a range.**
It is the version every command in this task was run on, and it is a member of
both pinned tools' own engine ranges (Electron 44.7.2 declares `>= 22.12.0`,
Vitest 5.0.5 declares `^22.12.0 || ^24.0.0 || >=26.0.0`), so nothing in the
toolchain is asking for anything narrower. A range here would have been the one
float left in a task whose whole subject is exact pins — and the plan already
hands this field to CI (Task 1.4's `actions/setup-node` takes its version from
it), so an exact value is what makes the runner's Node the same Node the tests
were written against. Bumping it is a deliberate edit, like every other pin, and
`test/unit/pinning.test.ts` refuses a wildcard, a tag or an operator in this
field the way it refuses one in a specifier. `.npmrc` sets `engine-strict=true`,
so a reader whose Node is another version finds out at install time rather than
in a test failure nobody can explain.

### Commands

```
npm ci --ignore-scripts --no-audit --no-fund   # the install; never `npm install`
npm run test:unit                              # vitest, test/unit/*.test.ts
npm run test:e2e                               # playwright, test/e2e/*.spec.ts; passes with no specs
npm run typecheck                              # tsc --noEmit over src/, test/, tools/ and the root config files
```

`npm ci` — never `npm install` — is the install step in this file, in the plan's
command and in the staged console workflow, because it installs exactly the
lockfile and refuses when the lockfile and the manifest disagree.
`--ignore-scripts` is passed on the command rather than set in `.npmrc` because
npm has no per-package escape hatch: `ignore-scripts` in that file would turn
off every lifecycle script of every command run here with no way to allow one
package back, and design section 5.2 asks for the other shape — an allow-list of
the packages whose postinstall is genuinely required, which is a decision made
at each call site.

**And what that costs, stated rather than left to be inferred: nothing at the
install step, because Electron's runtime is not fetched by a lifecycle script
here.** Electron is not an ordinary dependency — the pin, the download and the
checksum are hand-bumped and documented rather than floated (design section
5.2) — but with `--ignore-scripts` the download simply happens later: Electron
44.7.2 fetches its runtime on the first `require('electron')` (its `index.js`
spawns `install.js` when `path.txt` and `dist/` are missing, printing
`Downloading Electron binary...`), which is what Playwright's `electron.launch`
does in Task 1.1's e2e specs. Verified on this tree: `npm ci
--ignore-scripts --no-audit --no-fund`, then `require('electron')`, downloads
and resolves `node_modules/electron/dist/electron`. So the e2e path works with
scripts ignored, the runtime arrives on the machine that runs the tests rather
than on the machine that installs, and by packaging time (Task 4.1) it is
already on disk.

### The pinning rule is a test, not a habit

`test/unit/pinning.test.ts` reads `package.json` and fails on any specifier that
is not an exact version — a `^`, a `~`, a `*`, `latest`, or any other range —
naming the dependency and the marker, and it holds the checker itself to a table
of specifiers it must accept and refuse, so "package.json is clean" cannot pass
on a checker that accepts everything. `test/unit/lockfile.test.ts` holds the
other half: the lockfile exists, is `lockfileVersion` 3, declares exactly the
names and specifiers `package.json` declares, locks each of them to its pinned
version with an `integrity` hash, and resolves **every** entry — the names the
manifest declares and every package that arrives through them, which is code
that runs just the same — from `https://registry.npmjs.org/`, with nothing
floating anywhere. `.npmrc`'s `save-exact=true` is the same rule enforced by the
client, so a later `npm install <package>` writes an exact pin and not the first
range a test would have to catch. `test/unit/tsconfig.test.ts` is the third
piece: every TypeScript file at the console's root — today
`playwright.config.ts`, whose `testDir` is what keeps Playwright from collecting
the vitest unit tests — is asserted to be inside `tsconfig.json`'s `include`,
because a file outside `include` is not typechecked at all.

### Dependabot's scope is not extended here (a decision, not an oversight)

Recorded as the plan words it, because the plan hands this decision to this
task: "**Dependabot's scope is not extended here.** §5.2 notes that the console
adds a second thing Dependabot should watch — `console/package.json` and its
lockfile — and that Electron's own binary is not an ordinary npm dependency at
all. `.github/dependabot.yml` is not under `.github/workflows/`, so an agent may
edit it, and this plan deliberately does not: it belongs with Task 1.0's pins,
and the owner may prefer to keep Dependabot's scope where it is rather than have
a weekly PR stream behind an approval gate. **Listed so that the omission is a
decision, not an oversight.**"
(`docs/plans/2026-10-15-desktop-console.md`, "Open items, listed rather than
fixed", item 5.)

So `.github/dependabot.yml` still tracks one ecosystem — `github-actions` from
`/`, weekly — and nothing in this task edits it. What that costs is visible
rather than hidden: the console's pins are bumped by hand, by a person, with the
lockfile in the same commit, and the only things watching them are
`test/unit/pinning.test.ts` and `test/unit/lockfile.test.ts`. Dependabot could
not fully watch this tree even if it were pointed at it: **Electron's own binary
is not an npm dependency** — npm carries the package, but the runtime comes from
Electron's download host, so its pin, download and checksum get the hand-bumped
treatment the repository already gives the Trivy digest and the Dockerfile
frontend digest (design section 5.2).
