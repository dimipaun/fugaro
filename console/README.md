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
config files (`test/unit/{pinning,lockfile,tsconfig}.test.ts`); and the hardened
window (plan Task 1.1, below): `src/main/window.ts`, `src/main/protocol.ts`,
`src/main/index.ts`, `src/renderer/index.html`, `tsconfig.build.json`, the
two test files that try to leave it, and a third that holds the e2e harness's
own premise (`test/unit/playwright.test.ts`); the IPC allow-list (plan Task 1.2,
which this file's history carries without a section of its own):
`src/main/ipc.ts`, `src/preload/index.ts`, and `test/unit/ipc.test.ts`; and the
one child process (plan Task 1.3, the last section): `src/main/spawn.ts`,
`src/main/spawnlog.ts`, `src/source/source.ts`, `src/source/cli.ts`, and the
three suites that hold them (`test/unit/{spawn,spawnlog,configread}.test.ts`)
with the fake CLI they spawn (`test/unit/stub.ts`).

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
| `typescript` | `7.0.2` | `devDependencies` | `tsc --noEmit` for `npm run typecheck`, and `tsc -p tsconfig.build.json` for `npm run build` (Task 1.1: the build is `tsc` too, because the pinning rule leaves no room for a bundler) |
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
npm run build                                  # tsc -p tsconfig.build.json, src/ -> dist/
npm run test:e2e                               # playwright, test/e2e/*.spec.ts; builds first
npm run test:e2e -- security                   # just the window's spec
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

## The hardened window (Task 1.1)

The console's shell is one window, and everything it is allowed to do is
decided in two modules. `src/main/window.ts` holds the window's construction
options and the four policies on it:

| Rule (design §5.1) | What the code does |
|---|---|
| 1 — the renderer is untrusted by construction | `contextIsolation: true`, `nodeIntegration: false`, `nodeIntegrationInWorker: false`, `sandbox: true`, `webviewTag: false`, `allowRunningInsecureContent: false`, `experimentalFeatures: false` — and nothing else, asserted key by key |
| 2 — no remote content, ever | the only load is `app://console/index.html`; `will-navigate` denies every navigation; `setWindowOpenHandler` returns `{ action: 'deny' }`; `will-attach-webview` refuses; every permission is denied in both of Electron's handlers; the one `openExternal` path validates `https:`, no credentials, no port, host on an allow-list |
| 3 — a deny-by-default CSP, as a real header | `src/main/protocol.ts` registers `app` as a standard, secure scheme (no `bypassCSP`) and serves the bundle through `protocol.handle('app', …)`, so every response carries the design's CSP — header first, `<meta>` copy beside it |
| D14 — the allow-list is the guarantee | the `openExternal` allow-list is exactly three hosts: `github.com`, `bitbucket.org` (rule 2's "both providers' web hosts") and `console.cloud.google.com` (where `fugaro logs RUN --url` points). Rule 2 also names "the docs" and no document on this tree names a docs host, so none is on the list; a fourth entry is a deliberate edit, with the case for it, in the code and in the test that holds the list |

The protocol handler is deny-by-default in the same shape: one scheme, one
host, no port, no credentials, no query, no dot segment in the raw request URL,
no NUL, no backslash, and a resolved path that must stay under the bundle root.
The two refusals worth knowing about are the ones the URL parser makes
interesting — `app://console/../../etc/passwd` is refused as a traversal by a
check on the *raw* URL, because the WHATWG parser normalises dot segments away
before anything parsed can see them, and `%2e%2e%2f%2e%2e%2f` survives that
normalisation as a single segment and is caught by the containment check after
the one decode. Both are asserted, with their statuses and reasons.

Two things are here that the plan's Files column for Task 1.1 does not name,
said out loud rather than slipped in:

- **`src/main/index.ts`, the entry point.** `window.ts` and `protocol.ts` are
  libraries; without a caller the scheme is never registered, `protocol.handle`
  never runs, and the e2e spec has no application to launch. It registers the
  scheme before `whenReady`, installs the three denials on every
  `web-contents-created`, serves `src/renderer` and builds the window. It holds
  no decision the unit tests cannot reach.

  Its two halves are deliberately separate, and `activate` is the reason: macOS
  keeps the process alive when the window closes and `activate` (the dock icon)
  has to build the window again, but the scheme is registered **once** per
  process — `protocol.handle` refuses a second registration with "Failed to
  register protocol: app", and `reportAndQuit` would turn that into
  `app.exit(1)`, quitting the console exactly when the user asked for it back.
  `activate` therefore calls `openConsoleWindow` and never `main`. The e2e spec
  runs that sequence for real: it checks that the second registration really is
  an error on this Electron, closes the window, emits `activate`, and asserts a
  window is there again.
- **`tsconfig.build.json` and package.json's `main`/`build`.** Electron runs a
  file, and `tsc` is the only transform available: the pinning rule holds
  `dependencies` to the one entry Task 1.0 installed, so a bundler would be a
  dependency this task is not allowed to gain. `npm run test:e2e` builds before
  Playwright starts, so the spec always launches what the tree currently says.

`tsc` copies nothing, so the bundle's files stay where they were written and the
handler's root is `src/renderer` under the app's own path
(`app.getAppPath()`), not `dist/`. Packing the renderer into a packaged app is
Task 4.1's, and this task builds no package.

What is deliberately **not** here: the preload and its `contextBridge` surface
(Task 1.2), the spawn allow-list (Task 2.1), and the Electron fuses (Task 4.1).
The window is built so that each of those lands inside something already
switched off — `consoleWebPreferences()` asserts the absence of a `preload`
today precisely so that adding one is a decision rather than an oversight.

### The security properties are tests, and each one fails under a mutation

`test/unit/window.test.ts` never loads Electron: `src/main/*` imports *types*
only and takes the runtime as an argument, so the window's decisions are driven
under plain Node with a fake `webContents`, `session`, `protocol` and
BrowserWindow class, and nothing is mocked that could hide a decision.
`test/e2e/security.spec.ts` launches the real app and asserts what the browser
does with them.

| Mutation | What fails |
|---|---|
| any of the seven flags flipped (e.g. `contextIsolation: false`, `sandbox: false`) | the named row of the flag table |
| a flag *added* beside the right ones | "sets exactly those flags and no others" |
| a `preload` appearing | "no preload yet" |
| `will-navigate` that does not deny | the navigation table, the wiring test, and the e2e navigation test |
| `setWindowOpenHandler` returning `{ action: 'allow' }` | the window-open test, the wiring test, and the e2e `window.open` test |
| a permission handler calling back `true`, or a check handler returning `true` | the two halves of the permission test |
| `validateExternalUrl` accepting `javascript:`, `file:` or `data:` | three rows of its table |
| accepting an off-allow-list host, credentials or a port | the rows that name each |
| `openExternal` called on a refused link | "calls nothing at all for a refused link" |
| the protocol handler serving another scheme or host | the row's status and reason |
| serving a path outside the bundle root | the traversal row, the `%2e%2e%2f` row, and `isInsideBundleRoot`'s own cases |
| `registerSchemesAsPrivileged` without `standard`/`secure`, or with `bypassCSP` | the registration test |
| the CSP losing a directive, or `connect-src` relaxed to `'self'` | the directive test, and the two e2e probes: the remote one (a `securitypolicyviolation` naming `connect-src`, and nothing recorded by the route handler) and the same-origin one (a fetch for the console's own document is refused — the plan's red step) |
| `activate` calling the module's `main` again (the scheme registered twice) | the e2e `activate` test: `protocol.handle` throws, `reportAndQuit` exits the app |
| the `<meta>` copy drifting from the header | "byte for byte" |
| `loadURL` to anything but the bundled document | `createConsoleWindow`'s test |

The e2e network probes are where two gates stand behind one assertion, and it is
worth being precise about which is which *and* about which probe catches which
mutation. `connect-src 'none'` refuses a request inside Blink and produces a
`securitypolicyviolation` event naming the directive, while a Playwright route
handler aborts every remote request that gets that far and records it: a CSP
that stops refusing remote requests fails on both counts — the violation never
fires, and the request shows up in the handler's record, which is asserted
empty, and which is also the assertion to revisit first if a future Chromium
starts reporting CSP-blocked requests to Playwright's network events.

A CSP relaxed to `connect-src 'self'` is a different mutation and needs a
different probe, which is the plan's Task 1.1 red step: the remote target is not
this origin, so `'self'` refuses it exactly as `'none'` does — same violation
event, and nothing for the route handler to abort. The second probe therefore
asks for the console's own document, `app://console/index.html`, and asserts the
**violation event**, which is the only thing that separates the two directives
here: under `'none'` Blink refuses the fetch and dispatches a violation naming
`connect-src`, and under `'self'` it permits the request and dispatches nothing
(the fetch still fails, because Chromium's `fetch()` does not support a custom
scheme, so an assertion on the failure alone would pass under the mutation).
That test fails under the red step, and the remote one does not.

One more unit test holds a premise the e2e file depends on and cannot see:
`test/unit/playwright.test.ts` asserts that `playwright.config.ts` keeps
`fullyParallel` at Playwright's default, `false`. `security.spec.ts` launches
Electron once in `beforeAll` and is written against that one instance — one app,
one page, tests in order in a worker, the `activate` test last because it takes
the window down — and with `fullyParallel: true` Playwright spreads one file's
tests across workers and runs that file's `beforeAll` **once per worker**, so
the file would launch one Electron per worker and the ordering would stop
meaning anything. No assertion in the spec can notice that (measured on the
pinned Playwright 1.64.1 with a scratch spec of four tests in one file: `false`
ran all four in worker 0, `true` split them over two workers and would use more
where more are free), so the flag is held where it can be: flipping it back to
`true` fails `test/unit/playwright.test.ts`, which reads the config the way
Playwright does and needs no display and no Electron runtime to do it.

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

## The one child process, and the spawn log (Task 1.3)

The console reaches the data one way: it spawns the installed `fugaro` and reads
its JSON (design D1, Option A). It never reads a database, never holds a
credential and never opens the operator's config. Four files say so —
`src/main/spawn.ts` decides every spawn, `src/main/spawnlog.ts` is the one thing
the app writes, `src/source/source.ts` is the data plane every screen talks to,
and `src/source/cli.ts` is the v1 implementation over the child.

`FugaroSource` **yields `JSONDoc`-shaped snapshots and emits change events, and
knows nothing about how they were produced**: v1 spawns the CLI, the fixtures of
Task 1.4 replay a recording, and Option C's loopback API slots in behind the same
interface without a screen changing. A snapshot is the CLI's document *as it
arrived* plus the console's own stamp — when it arrived, which one it is in the
series, which day it belongs to, and whether that day just changed — and the one
field this task reads out of a document is `day`, which the rollover needs.

| Rule (design §3.4 and §5.1) | What the code does |
|---|---|
| 1 — argv is an array, `shell: false`, never built from a job-written field | `argvFor` is the only place an argv is built. It walks the row's *declared* arguments, refuses an undeclared key by name, and passes every value through a conservative character whitelist, so a value that arrived from a document cannot become a command word. Every emitted element is its own array entry, every emitted flag is asserted against `SPAWN_FLAGS`, and the spawner is always called with the literal `shell: false`. The schema's kinds and the table's kinds are related by exactly one mapping, `SPAWN_KIND_FOR`, and `assertSpawnSurface` refuses a row whose kind disagrees with the operation's schema — so a validated value that argv cannot spell is a table-load failure rather than a refusal a user reads |
| 3 — `--project` on every command that takes it | `SPAWN_COMMANDS` carries `takesProject` per row: the nine commands of `addCloudFlags` (`internal/cli/cloud.go:51`) plus a tenth row (the console has two operations over `logs`, `--json` and `--url`, and neither may be the one that forgets). The handshake's argv is exactly `["version"]`, because `newVersionCmd` is `cobra.NoArgs` with no flags. `--config <path>` follows `--project` whenever the installation declares one |
| 4 — the binary is resolved explicitly | the operator's configured path, then `/opt/homebrew/bin`, `/usr/local/bin`, `~/.local/bin`, `/usr/bin`, and only then every entry of the app's own `PATH`; the resolved path and version are what the header draws. **A candidate that exists but whose version cannot be read is refused rather than skipped** — falling through to the next `fugaro` on the machine would silently change which installation the numbers came from |
| 5 — the child's environment is built, not inherited | exactly `PATH`, `HOME`, `TMPDIR`, `LANG`, `LC_ALL`, the five `XDG_*` variables and the chosen installation's `FUGARO_PROJECT`. A hostile `GOOGLE_APPLICATION_CREDENTIALS`, `AWS_*`, `SSH_AUTH_SOCK` or ambient `FUGARO_PROJECT` in the app's environment cannot reach the child |
| 5 — the child's `PATH` is a list, not one directory | the resolved binary's own directory first, then `/opt/homebrew/bin`, `/usr/local/bin`, `/usr/bin`, `/bin`, `/usr/sbin`, `/sbin` — because the CLI looks up external tools itself (`exec.LookPath`, `internal/cli/doctor.go:147`), and `terraform` and `cosign` do not sit beside the `fugaro` binary even under Homebrew. The app's inherited `PATH` still never reaches the child, and a tool outside that list is reported as *not on the `PATH` the console gives the CLI* rather than as "not installed" |
| 5 — the working directory is set explicitly | the installation's checkout when it has one, otherwise a private `0700` directory the app owns under the system temporary directory. Never `/`, which is nobody's checkout. `doctor` is called with `--dir <that directory>` and **never `--strict`**: outside a checkout its `plugin-pin` check is a warning that `--strict` turns into a failure about the checkout rather than about the environment on screen |
| 7 — the app never reads the operator's config | the config's *path* is a string on the installation, passed to the child as `--config`; the CLI is the reader. The add-an-installation check is a **spawn** for the same reason: one allow-listed `doctor --json` with the checkout as the child's working directory, and a checkout that belongs to another project is refused with the CLI's own `this checkout belongs to project %s; --project says %s` — and **not stored**, because storing it would leave every screen refused later with nothing on screen explaining why |
| 10 — the spawn log (D20) | `src/main/spawnlog.ts`, documented below |
| Task 0.2's four settled behaviours | restart with the CLI's own backoff (1 s doubling to 30 s) except after a refusal, a signal read as the ordinary end of a feed, the UTC-midnight rollover marked as a **gap** rather than a zero, and the reader rule that a stdout line which is not a JSON object is **skipped** and surfaced as a notice — the degraded mode writes a plain sentence on the same stream as the documents (`runWatchDegraded`, `internal/cli/watch.go`) |

### The spawn log, and the only thing the app writes

**The console writes nothing by default.** Set `FUGARO_CONSOLE_SPAWN_LOG` to a
path and the main process appends one JSON object per line there, one line per
child process, **created with mode `0600`**:

```json
{"ts":"2026-10-15T14:22:31Z","argv":["/opt/homebrew/bin/fugaro","watch","--json","--project","fugaro"],"cwd":"/Users/owner/src/fugaro","project":"fugaro"}
```

Exactly those four keys. `ts` is RFC 3339 UTC at second precision, `argv` is the
array as spawned with the binary's absolute path first, `cwd` is absolute, and
`project` is the installation's project name — **the value passed as
`--project`, so the bare `version` handshake still records which installation it
was for**. It holds no command output, no environment, no credential and nothing
a job wrote. It is **bounded at 1000 records**: at the 1001st spawn the writer
appends one `{"truncated":true,"limit":1000}` line and stops, rather than
rotating or truncating in place. It is never read back, never rotated, never
deleted and never transmitted — `test/unit/spawnlog.test.ts` holds the first and
the last as scans over `console/src/`, so a screen, a source or an IPC handler
that started reading it would fail a named test. It is what Check 59's
assertion (4) reads and what Task 4.3's FACT commands read the key names from.

**It is the only thing the app ever writes to disk.** The one other write in this
task is a directory rather than a record: `privateWorkingDirectory()` creates an
empty `0700` directory under the system temporary directory for an installation
that has no checkout, so that `doctor --dir` is a directory and the child's `cwd`
is not `/`. Section 5.1 rule 10 is about *records* — no history, no cached
document, no crash dump, nothing that outlives the process — and this holds
nothing at all. A later task that persists the installation list (the store here
is in memory) would be a second thing the app writes and would have to be
reconciled with rule 10 explicitly rather than growing by accident.

### The spawn properties are tests, and each one fails under a mutation

The tests spawn **a fake `fugaro` in a temporary directory — never the real CLI
and never the network** (`test/unit/stub.ts`: a shell shim that `exec`s the Node
running the suite by absolute path, a generated script, and a `scenario.json`).
The report file the stub appends to is the child's *own* account of its argv,
`cwd` and environment, which is what makes these tests about what a real machine
sees rather than about the console's self-consistency. Seventeen mutations were
run against the committed tree, and each made a named test red:

| Mutation | What fails |
|---|---|
| the child's environment merged from the app's own | "gives the child exactly `CHILD_ENV_KEYS` and nothing else" |
| the child's `PATH` reduced to the binary's directory alone | "builds the child's PATH from the binary's directory and every well-known prefix" |
| `--project` appended to the `version` handshake | "runs the version handshake bare" |
| `--project` dropped from `watch` | "live.watch puts --project on its argv, from the chosen installation" |
| `shell: true` on the real spawner | "hands the real spawner `shell: false`, as a literal, and never `true`" |
| the argument whitelist removed | the hostile-value table (`; rm -rf /`, `$(id)`, quotes, spaces, leading dashes) |
| a non-JSON stdout line parsed instead of skipped | "skips a degraded-mode sentence rather than failing the read" |
| `cwd` left as the app's own | "spawns from the installation's checkout" and "spawns from a private directory the app owns" |
| `--strict` added to `doctor` | "passes `--dir <that directory>` and never `--strict`" |
| a candidate with an unreadable version skipped rather than refused | "refuses a candidate whose version cannot be read, rather than skipping it" |
| a refusal restarted anyway | "does not restart after a refusal, and keeps the CLI's sentence" |
| the spawn log enabled without the variable | "unset means nothing on disk: no path, no descriptor, no file" (asserted on the injected `fs`, not only on the filesystem) |
| the spawn log's bound removed | "stops at the bound: one truncation line at record 1001, then nothing" |
| an `env` field added to a log line | "writes exactly the four keys, checked against a planted fifth" |
| a `readFileSync(join(homedir(), '.config', 'fugaro', …))` anywhere under `console/src/` | "is clean: no file under console/src/ opens a config, anywhere" — and the planted file that *is* that mutation is a fixture in `test/unit/configread.test.ts`, so the checker is tested against what it must catch |
| `runs.list`'s `pr` row back to `kind: 'value'` — the bug a review found here: the schema hands over a *number* and that case required a string, so every PR-filtered run list was refused on the way to a spawn that never happened | "what the schema validates is what the spawn table accepts > spawns runs.list from the payload validateArguments returns", and 29 others: the kind disagreement makes `assertSpawnSurface` refuse the table, so `CliSource.create` and everything behind it fails too |
| `spend.report`'s `since` back to the runs grammar (`kind: 'since'`) | the report-range rows of "the day and window arguments" (`12w`, `522w`, `0d`, `3h`, `3661d`) and "carries the report range 12w through to argv unchanged" |

### What is deliberately not here

- **The wiring.** Nothing registers `handlersFor(source, …)` with
  `registerConsoleHandlers` and nothing attaches a preload: that is the Live
  screen's task, and doing it here would flip Task 1.1's named "no preload yet"
  assertion (and its mutation row) before there is a surface to attach. The
  pieces are all reachable from a test, which is where they are held.
- **Persistence.** The installation store is in memory for one run; see the note
  above on rule 10.
- **The fixtures.** `FugaroSource`'s second implementation is Task 1.4's
  recorder and replay, and it slots in behind the same interface.
- **`version --json` and a schema version.** The CLI has neither (contract §4.3
  gaps 1 and 3), so the handshake is the one human line and the console refuses a
  binary whose line it cannot read rather than guessing.

