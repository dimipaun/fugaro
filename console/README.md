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
`playwright.config.ts`, `.npmrc`, `.gitignore`, and the two unit tests that hold
the pinning rule and the lockfile.

## The toolchain, pinned (Task 1.0)

**Electron is pinned to exactly `44.7.2`, and it is bumped on purpose**, with the
toolchain task and the lockfile in the same commit — the same rule
`internal/budget/rules/emulator/package.json` states for its own `firebase-tools`
pin. Electron is not an ordinary dependency: its postinstall downloads a large
prebuilt runtime, so the pin, the download and the checksum are hand-bumped and
documented rather than floated (design section 5.2).

The rest of the pinned set, all exact, all in `devDependencies` — the app ships
no runtime dependency at all, because the renderer bundles no framework and no
charting library (design D9, and `test/unit/pinning.test.ts` asserts the
`dependencies` section is empty):

| Package | Version | Why it is here |
|---|---|---|
| `electron` | `44.7.2` | the app's runtime, bumped on purpose |
| `typescript` | `7.0.2` | `tsc --noEmit`; the type checker, not the build |
| `vitest` | `5.0.5` | `npm run test:unit` |
| `@playwright/test` | `1.64.1` | `npm run test:e2e`, Electron driven by `electron.launch` (Task 1.1) |
| `@types/node` | `22.20.5` | the Node the main process and the tests run on |

`engines.node` is `>=22.12.0`, and it is read off the pinned tools rather than
guessed: Electron 44.7.2 declares `>= 22.12.0` and Vitest 5.0.5 declares
`^22.12.0 || ^24.0.0 || >=26.0.0`, so a Node below the intersection is a Node
that was never going to run this toolchain. The console's CI job takes its
version from this field (Task 1.4), and `.npmrc` sets `engine-strict=true` so a
mismatched reader finds out at install time.

### Commands

```
npm ci --ignore-scripts --no-audit --no-fund   # the install; never `npm install`
npm run test:unit                              # vitest, test/unit/*.test.ts
npm run test:e2e                               # playwright, test/e2e/*.spec.ts; passes with no specs
npm run typecheck                              # tsc --noEmit over src/, test/, tools/
```

`npm ci` — never `npm install` — is the install step in this file, in the plan's
command and in the staged console workflow, because it installs exactly the
lockfile and refuses when the lockfile and the manifest disagree.
`--ignore-scripts` is passed on the command rather than set in `.npmrc` on
purpose: Electron's binary download is the one lifecycle script this app is
expected to need, and the packaging phase (Task 4.1) is where it is allowed,
package by package.

### The pinning rule is a test, not a habit

`test/unit/pinning.test.ts` reads `package.json` and fails on any specifier that
is not an exact version — a `^`, a `~`, a `*`, `latest`, or any other range —
naming the dependency and the marker, and it holds the checker itself to a table
of specifiers it must accept and refuse, so "package.json is clean" cannot pass
on a checker that accepts everything. `test/unit/lockfile.test.ts` holds the
other half: the lockfile exists, is `lockfileVersion` 3, declares exactly the
names and specifiers `package.json` declares, locks each of them to its pinned
version with a `resolved` URL and an `integrity` hash, and floats nothing.
`.npmrc`'s `save-exact=true` is the same rule enforced by the client, so a later
`npm install <package>` writes an exact pin and not the first range a test would
have to catch.
