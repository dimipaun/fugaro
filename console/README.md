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

Two Go files may *mention* a `console/` path without importing anything from it,
because the console's JSON contract is a document they read as bytes:
`internal/cli/docs_desktop_console_test.go`, which reads
`console/docs/json-contract.md` and pins every section whose command is still in
the core, and `ee/coordinator/cli/console_contract_test.go`, which moved out of
the core with the coordinator's command and pins the one section that describes
it. Each is an entry on that boundary test's allow-list; a further entry is a
deliberate edit to the list, with the case for it written down, rather than a
quiet new reference somewhere else.

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
`src/main/ipc.ts`, `src/preload/index.ts`, and `test/unit/ipc.test.ts`; the
one child process (plan Task 1.3): `src/main/spawn.ts`,
`src/main/spawnlog.ts`, `src/source/source.ts`, `src/source/cli.ts`, and the
three suites that hold them (`test/unit/{spawn,spawnlog,configread}.test.ts`)
with the fake CLI they spawn (`test/unit/stub.ts`); and the recorded fixtures
(plan Task 1.4, the last section): `src/source/redact.ts`,
`src/source/fixture.ts`, the recorder (`tools/record-fixture/index.ts`),
the one committed fixture (`test/fixtures/synthetic.ndjson`, **synthetic**),
the suite that invents it (`test/unit/synthetic.ts`), and the three suites that
hold the three (`test/unit/{redact,fixture,recorder}.test.ts`).

## The toolchain, pinned (Task 1.0)

**Electron is pinned to exactly `45.0.0`, and it is bumped on purpose**, with the
toolchain task and the lockfile in the same commit — the same rule
`internal/budget/rules/emulator/package.json` states for its own `firebase-tools`
pin. Electron is not an ordinary dependency: it pulls a large prebuilt runtime
from its own download host rather than from npm, so the pin, the download and
the checksum get the hand-bumped, documented treatment the repository already
gives the Trivy digest and the Dockerfile frontend digest (design section 5.2).

The rest of the pinned set, all exact, all build and test tooling apart from
the one runtime dependency (`electron-updater`, below):

| Package | Version | Where | Why it is here |
|---|---|---|---|
| `electron` | `45.0.0` | `devDependencies` | the app's runtime, bumped on purpose |
| `typescript` | `7.0.2` | `devDependencies` | `tsc --noEmit` for `npm run typecheck`, and `tsc -p tsconfig.build.json` for `npm run build` (Task 1.1: the build is `tsc` too, because the pinning rule leaves no room for a bundler) |
| `vitest` | `5.0.5` | `devDependencies` | `npm run test:unit` |
| `@playwright/test` | `1.64.1` | `devDependencies` | `npm run test:e2e`, Electron driven by `electron.launch` (Task 1.1) |
| `@types/node` | `22.20.5` | `devDependencies` | the Node the main process and the tests run on |
| `@electron/fuses` | `2.1.3` | `devDependencies` | the `afterPack` hook's fuse writer (Task 4.1) |
| `electron-builder` | `26.18.0` | `devDependencies` | the packer, run from its local binary by `npm run package` (Task 4.1) |
| `elkjs` | `0.11.0` | `devDependencies` | the graph view's layout engine: the build copies its `elk-worker.min.js` verbatim into the served root (GV2.2); its EPL-2.0 licence is `THIRD_PARTY_NOTICES.md` |

`dependencies` holds exactly one entry — `electron-updater` pinned to `6.8.10`,
the console's one runtime dependency — and that is a decision rather than a
symptom: the renderer bundles no framework runtime and no charting library
(design D9); its one third-party file is elkjs's layout worker, a devDependency
whose `elk-worker.min.js` the build copies verbatim into the served root and
whose EPL-2.0 licence is `console/THIRD_PARTY_NOTICES.md`, and
everything a `dependencies` entry names is packed into the shipped asar. Task 1.0
pinned `electron-updater` there for Task 4.4's auto-update; Task 4.1's review
found it shipped in the bundle with nothing importing it, so it was removed; and
Task 4.4 added it back, exactly pinned, with the code that uses it.
`pinning.test.ts`'s runtime allow-list holds the set to that one entry, and a
second one is a deliberate edit there with the case for it written down.

**The Node is pinned exactly too: `engines.node` is `22.23.3`, not a range.**
It is the version every command in this task was run on, and it is a member of
both pinned tools' own engine ranges (Electron 45.0.0 declares `>= 22.12.0`,
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
npm run record-fixture -- --help               # the recorder; OWNER-RUN, see "Recorded, redacted fixtures"
npm run screenshots                            # the website's pictures; OWNER-RUN on a Mac with a display, see "The console's screenshots for the website"
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
45.0.0 fetches its runtime on the first `require('electron')` (its `index.js`
spawns `install.js` when `path.txt` and `dist/` are missing, printing
`Downloading Electron binary...`). Verified on this tree: `npm ci
--ignore-scripts --no-audit --no-fund`, then `require('electron')`, downloads
and resolves `node_modules/electron/dist/electron`. So the e2e path works with
scripts ignored, the runtime arrives on the machine that runs the tests rather
than on the machine that installs, and by packaging time (Task 4.1) it is
already on disk.

**That lazy fetch is a writer, and it has to finish before anything execs what
it writes.** `install.js` unpacks the archive into `node_modules/electron/dist`
as it goes, so a launch that reaches the executable while the unpack is still
running finds a file open for writing: Linux refuses that with ETXTBSY (and a
launch that gets there before the file exists at all gets ENOENT). Two
Playwright workers on a cold `node_modules` did exactly that, which is what made
the `console (ubuntu-latest)` check — a required check of
`scripts/release-gate.sh` — fail intermittently (`Error: electron.launch: spawn
ETXTBSY`, run 39174336894 attempt 1, then a red gate). The fetch is therefore
**hoisted out of the workers**: `playwright.config.ts` names
`test/support/globalSetup.ts`, which resolves the binary once in Playwright's
main process before the first worker is forked, so the writer exits — every
descriptor closed — before a launch exists to race it. Every spec starts the
console through `test/support/launch.ts` (`launchConsole`), which resolves the
binary again on the way into each launch and retries ETXTBSY three times, 250 ms
apart (`test/support/textFileBusy.ts`); no other launch error is retried, and
`test/unit/playwright.test.ts` holds both the config line and the rule that no
spec calls `electron.launch` itself.

## Automatic updates (Task 4.4)

The console keeps itself up to date, and this is the whole of it. It checks the
console's own release stream at launch and then every six hours, and downloads a
newer version **quietly**; nothing is installed until you press **Restart to
update** on the non-blocking notice that names the version on disk, and **nothing
is ever installed on quit** — the update installs only the download that notice
named. The client is `electron-updater` `6.8.10`, the console's one runtime
dependency, pinned exactly, and `src/main/updater-electron.ts`'s `hardenUpdater`
rewrites it where the library would otherwise identify the machine: it neither
mints nor writes `.updaterId` under `userData`, and no request carries
`x-user-staging-id`. The off switch is the application menu's **Check for Updates
Automatically** (with **Check for Updates Now** beside it), whose state is the one
boolean `<userData>/updates.json` holds, written only when the switch changes
(`src/main/updatestore.ts`); setting `FUGARO_NO_UPDATE_CHECK=1` turns the checks
off as well. The console honours those two and never the CLI's `update_check`
(owner ruling D769). Two locations under `~/Library/Caches` are the updater's and
not the app's — the download cache `fugaro-console-updater`, named by
`hardenUpdater` through `configOnDisk`, and Squirrel.Mac's ShipIt staging
directory — and both appear only after a download, are replaced or removed by the
install, and hold no identifier (owner rulings D768 and D770; design sections
5.1 rule 10, 7.3 and 7.4).

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

What is deliberately **not** here: the preload attached to the window (Task
1.2 writes `src/preload/index.ts` — see "The IPC allow-list" below — and
deliberately does not wire it in), the spawn allow-list (Task 2.1), and the
Electron fuses (Task 4.1). The window is built so that each of those lands
inside something already switched off — `consoleWebPreferences()` asserts the
absence of a `preload` today precisely so that adding one is a decision rather
than an oversight.

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

### Dependabot watches the console (the owner's decision, applied 2026-10-23)

The plan handed this decision to the owner rather than to this task: "**Dependabot's scope is not extended here.** §5.2 notes that the console
adds a second thing Dependabot should watch — `console/package.json` and its
lockfile — and that Electron's own binary is not an ordinary npm dependency at
all. `.github/dependabot.yml` is not under `.github/workflows/`, so an agent may
edit it, and this plan deliberately does not: it belongs with Task 1.0's pins,
and the owner may prefer to keep Dependabot's scope where it is rather than have
a weekly PR stream behind an approval gate. **Listed so that the omission is a
decision, not an oversight.**"
(`docs/plans/2026-10-15-desktop-console.md`, "Open items, listed rather than
fixed", item 5.)

The owner ruled on 2026-10-22 to extend the scope, and on 2026-10-23 the staged
file was copied over `.github/dependabot.yml`. It now tracks two ecosystems —
`github-actions` from `/` and `npm` from `/console`, both weekly — with the
console's minor and patch bumps grouped into one pull request a week and a major
left outside the group so it arrives on its own (see
`docs/pending-workflows/README.md`, `## dependabot-console.yml`).
`test/unit/pinning.test.ts` and `test/unit/lockfile.test.ts` still hold every
bump to an exact pin with its lockfile. Dependabot cannot fully watch this tree
even so: **Electron's own binary
is not an npm dependency** — npm carries the package, but the runtime comes from
Electron's download host, so its pin, download and checksum get the hand-bumped
treatment the repository already gives the Trivy digest and the Dockerfile
frontend digest (design section 5.2).

## The IPC allow-list (Task 1.2)

Design section 5.1 rule 5: "**an IPC allow-list, not an IPC bridge**". The
console has no bridge. It has a fixed table of named operations, and three
prohibitions that are the whole of the rule: **no `invoke(channel, ...)`**
(nothing in the console takes a channel from a caller), **no channel name from
the renderer** (the preload exposes one *method* per operation and the renderer
passes arguments to a method, never a name), and **no dynamic `require`**.

`src/main/ipc.ts` is the table and the gate. Fifteen operations — the twelve CLI
reads of design section 2.1 (`watch`, `budget show`, `report`, `runs ls`,
`runs stats` (the Spend screen's by-person view, console L5b), `diagnose`, `logs`
for the document, `logs` for the link, `secrets ls`, `accounts ls`, `version`,
`doctor`), the startup handshake's own answer, the dashboard's read of the
installation the operator chose, and the one validated external link — each
with the command it drives, the flags it fixes and a **closed argument schema**
validated in the main process before any handler sees it. Closed means every key
is declared (an extra key is refused by name), `__proto__`/`constructor`/
`prototype` are refused by name and a payload whose prototype is not a plain
object's is refused whole, every string is bounded and refused when it carries a
control character, an invisible format character, an unpaired surrogate or
anything outside its grammar, and the validated arguments are rebuilt into a
frozen null-prototype object. What an operation may **not** take is the other
half: no path (the installation's directory and `doctor --dir` are main-process
state), no command (the verb is a constant in the table) and no URL beyond the
one link, which is `validateExternalUrl` from `src/main/window.ts` — imported,
not re-implemented, so the two cannot drift.

**A kind belongs to a command, not to a flag name.** Two commands' `--since` are
two grammars: `report --since` takes a UTC day, `30d` or `12w`, bounded by the
CLI's own `reportMaxDays`, while `runs ls --since` takes `0`, `7d` or a Go
duration in hours or minutes (`36h`, `90m`) and has no date branch at all. One
kind shared between them was wrong in both directions at once — refusing `12w`,
which `report`'s own help advertises, and admitting a calendar day that
`parseSince` rejects — so each command has its own kind (`reportSince`,
`runSince`), and `test/unit/ipc.test.ts` carries a table per command in which
every row is a value that command's own parser accepts or refuses. Where a kind
is narrower than its flag it says so in the kind's own doc comment (`runSince`
does not carry `7s`, and no kind carries a path).

**The sender is checked before the arguments are, and the check is not
optional.** A registered channel is reachable by any frame the application has
loaded, so arriving on the right channel is not a capability. Every listener
refuses a message unless three things hold — the event has a sending frame at
all, that frame *is* `event.sender.mainFrame` (identity, not equality of URL
strings: a subframe inside a document the console did not write would otherwise
pass an origin test by living in a frame that did), and the frame's URL is on
the console's own origin `app://console` — and it answers one fixed sentence
that repeats nothing the sender wrote. This is the Electron security
checklist's item 17, "validate the sender of all IPC messages". The origin is
not spelled again here: `APP_SCHEME` and `APP_HOST` come from `src/main/protocol.ts`,
and the test builds its accepted event from `APP_ENTRY_URL`, so the origin the
gate checks and the origin the console serves cannot drift apart. The
webContents *id* is deliberately not a separate check — handlers are registered
before any window exists and macOS `activate` builds a new one afterwards, so a
captured id is stale by design, and the frame identity already decides the same
thing: a frame is on `app://console` only if this process served it out of its
own bundle.

**A refusal's text is bounded, and the reason is a hostile `error.message`.**
Design §6.3 wants the CLI's own sentence shown verbatim, and a CLI sentence is
short and dull; a handler that threw with four megabytes in it, or with a
newline, an escape sequence or `/home/you/.fugaro/secrets.yaml` in it, would put
all of that in a renderer that is entitled to treat a reply as the console's own
words. `replyReason` therefore flattens a message to one line, drops control
characters and invisible format characters (bidi overrides and isolates,
zero-width characters), replaces absolute paths (POSIX, `~/`, drive-letter and
UNC, after a space, quote, backtick, bracket, `;` or `:`, and a `file://` URL
whole) with `<path>` while leaving a web URL whole, and cuts at 2 KB with an
ellipsis, never inside a surrogate pair. The test that an ordinary CLI
sentence comes through byte for byte is what keeps those three from eating the
case the design asks for. The same shape applies to what the console *accepts*:
after the argument-count bound, a payload's own width (every key plus every
scalar value) is bounded at `MAX_PAYLOAD_WIDTH`, set deliberately above what the
per-kind bounds can add up to, so it can only refuse what no screen would send.
It cannot bound Electron's own deserialisation of the message, which happens
before the listener runs; that limit is stated where the constant is declared.

`src/preload/index.ts` puts **one frozen, versioned object** on `window.fugaro`,
one method per operation. It is deliberately a phone book and not a switch: the
channel a method calls is a constant concatenated from that operation's name,
the argument object is forwarded untouched (there is one validating gate and it
is in main), and the reply is checked for the promised envelope before a screen
sees it.

**The surface is closed twice, and the second half is the compiler's.** The
table is declared `as const`, with **no type annotation**, so its fifteen rows
stay literal types: `ExposedName` and `ExposedMethod` are unions of fifteen
literals rather than `string`, `ConsoleBridge` is fifteen *named* methods rather
than a string index signature, and `bridge.showBudgte(...)` is a compile error
instead of an `undefined` call somewhere inside a screen's render. Nothing at
runtime can see this — the object is built *from* the table and the suite pins
the same two sets against `CONSOLE_OPERATIONS`, which is exactly why an
annotated table was silent. So the claim is held where it is decided:
`test/unit/ipc.test.ts` carries `assertClosedSurface`, whose `@ts-expect-error`
directives *become* `Unused '@ts-expect-error' directive` errors — a red
`npm run typecheck` — the moment the table is widened again. Re-adding the
annotation on 2026-10-16 produced eight errors. `satisfies readonly (readonly
[string, string])[]` checks each row's shape without widening anything, and is
load-bearing in its own right: with it removed, a row carrying a third element
typechecks clean, because only `[number][0]` and `[number][1]` are ever read.

### Why the preload writes the surface out a second time

The window runs `sandbox: true`, and in a sandboxed preload Electron replaces
`require` with a polyfill that resolves `electron`, `events`, `timers` and `url`
and **nothing else** — a relative `require('../main/ipc')`, which is what `tsc`
emits for a value import, would fail the moment Electron ran the file. The
alternatives were a bundler (a dependency this task may not add: `package.json`'s
dependency set is exact and `test/unit/pinning.test.ts` asserts it) or writing
the list twice. So it is written twice, and `test/unit/ipc.test.ts` closes the
duplication in both directions: the methods on the frozen object must be exactly
the table's methods, each must `invoke` the channel `channelForOperation(name)`
computes for that same operation, and the version in the bridge must be the
table's. **Types are imported from the table; values are not** — a type-only
import is erased, which is why the preload can still be typed against
`ConsoleReply` and `RendererLike` shape-checked against Electron's own types.

### The never-reachable table, and the mutation-proof half

`NEVER_REACHABLE` names what the console must never reach, read off the code
rather than off a paragraph — every `where` columns names the `internal/cli`
file and line each entry was grepped from on 2026-10-16 (the three converge rows
were re-read on 2026-10-21, after task W1.2 of
`docs/setup-wizard/implementation-plan.md` split the old first-run `init` into
`fugaro setup` and `fugaro update`). It carries every
command the CLI has that writes (including `verify`, which runs a workflow's own
build/test/lint commands in the checkout, and `image build`/`gate`/`record`/
`refresh`), the `secrets` commands, the converge — `setup`, which creates the
project and the installation, and `update`, which applies Terraform over one —
the retired `init` tombstone, `upgrade` and the hidden `exec`, the
hidden `budget history` job, `image git-credential` (it prints a credential),
the project-centric phase 1 commands that write or publish (which do not exist
on this tree yet, and are listed anyway so that the day one appears the
allow-list is already closed to it), and the flags that turn a read into
something else: `--follow`, `--check-github-app`, `--strict`, `--csv`,
`--watch`, and the watch renderer's `--plain`/`--poll`/`--ascii`/`--no-color`.

Two things read that table. `assertReachableSurface` — called by
`registerConsoleHandlers` before it registers anything — throws at startup if an
operation drives a row of it, fixes a flag the surface may not fix, or shares a
name; a console that cannot be built with `budget kill` in it cannot be talked
into running it later. And the test names **one test per row**: nothing in the
allow-list drives `budget kill`, or `--follow`, or any other row.

| Mutation | What fails |
|---|---|
| `budget kill` (or any row) added to `CONSOLE_OPERATIONS` | the test named for that row, the "argv for argv" test, and `assertReachableSurface` at registration — the plan's red step |
| a command the snapshot does not classify as one the allow-list admits | "drives *command* only because the design's allow-list admits it" |
| a writer missing from `NEVER_REACHABLE` | "knows that *command* is on the never table, or is simply unused" |
| `--follow`, `--check-github-app`, `--strict`, `--csv`, `--watch` or a watch-renderer flag fixed by an operation | the never-table test named for the flag, and the "fixes only the two flags" test |
| `--url` and `--json` on one operation | "never --url with --json" (the CLI refuses that argv: `internal/cli/logs.go:63`) |
| an operation with no preload method, or a preload method with no operation | the two-way surface test |
| a method invoking anything but `channelForOperation(name)` | the channel-routing test |
| a schema accepting an extra key, a prototype-pollution key, a path, a command, a control or format character, an oversize string or a wrong type | the table entry that names it |
| one kind shared by two commands whose flags are not one grammar | the per-command window table, row by row, and "gives each command its own window kind" |
| the sender check removed, or any of its three checks (frame present, frame *is* the main frame, frame URL on the console's origin) | the "sender gate" tests: 12 failures for the check as a whole, and the row named for each individual check |
| a handler's message left unbounded, or its control characters or absolute paths left in | "bounds a handler's own message", "keeps a refusal to one line", "takes an absolute path out of a handler's message" |
| `assertReachableSurface(CONSOLE_OPERATIONS)` removed from registration, or moved after the channels are on | "checks the surface before it registers anything" |
| the allowed-flags half of `assertReachableSurface` removed (a flag the never table does not even list) | "refuses a table with a flag the never table does not even list" |
| a never-table *flag* row renamed away | "carries a never-table row for *flag*", one test per flag |
| the `PROTOTYPE_KEYS` branch removed | "names the protection key it refused" |
| the payload-width bound removed | "refuses a payload wider than the width bound" |
| a schema that refuses *everything* (the way to pass every refusal test vacuously) | the well-formed-payload table, one row per operation |
| an unfrozen bridge, a wrong version, a `length > 1` method, an `invoke`/`send`/`on` property | the surface-shape tests |
| `EXPOSED_OPERATIONS` given a type annotation, which widens `ExposedName`, `ExposedMethod` and `ConsoleBridge` back to `string` | `tsc --noEmit`: eight errors — seven unused `@ts-expect-error` directives in `assertClosedSurface` and the bridge no longer matching `ConsoleBridge` — plus "declares the table with literal types" |
| `as const`, or the `satisfies` clause, dropped from the table | the same directives, and the same source scan |
| a row of the table given a third element (with `satisfies` removed to permit it) | nothing — which is why the `satisfies` clause is itself asserted by the scan |
| a second `invoke`, a computed `require`, an `eval`, a `child_process`, or a value import in the preload | the source scans |

The red step was run as the plan writes it: `budget kill` added to the
allow-list fails 21 tests, three of them named for it — "nothing in the
allow-list drives budget kill", "drives budget kill only because the design's
allow-list admits it", and the registration throw, which surfaces as "accepts
the table the console actually has" reporting `budget.kill drives budget kill,
which the console must never reach`. It also found a real weakness in the test
file, which is why it is recorded here: the "throws on an operation name that is
not in the table" test used `budget.kill` as its unknown name, so adding
`budget.kill` to the table made *that* test pass and hid one of the failures
behind it. It now uses a name no screen would ever add.

### What is deliberately not here (Task 1.2)

The **handler implementations** — Task 1.3 spawns the CLI; this task defines
what a handler may be asked for and nothing about how it answers.
`registerConsoleHandlers` takes the handlers as an argument, so the surface is
complete and testable today and gains its one implementation without an edit to
the allow-list. And the **window's `preload` option**: `test/unit/window.test.ts`
asserts its absence by name, and attaching a bridge over handlers that do not
exist yet would be the one edit that makes this surface reachable before there
is anything behind it.

Nothing here loads Electron, and nothing here spawns anything. The whole section
is driven by `test/unit/ipc.test.ts` under plain Node — a fake `ipcMain` that
records the channels it was given, a fake `ipcRenderer` that delivers each
`invoke` to the listener for that channel, and a `codeOnly` helper that strips
comments before the source scans, so a scan cannot be satisfied by prose that
happens to name the thing it is looking for.

## The one child process, and the spawn log (Task 1.3)

The console reaches the data one way: it spawns the installed `fugaro` and reads
its JSON (design D1, Option A). It never reads a database, never holds a
credential and never opens the operator's config. Four files say so —
`src/main/spawn.ts` decides every spawn, `src/main/spawnlog.ts` is one of the
two things the app writes, `src/source/source.ts` is the data plane every screen
talks to, and `src/source/cli.ts` is the v1 implementation over the child.

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
| 4 — the binary is resolved explicitly | an installation's *configured* path if this console's store already has one (an **optional override**, and no migration: an entry that predates this keeps working), then the **platform's own well-known prefixes** — and **never the app's inherited `PATH`** (amended 2026-10-20 by the owner's ruling: the PATH step is gone, and the console asks the operator for no binary at all); the resolved path and version are what the header draws. **The prefixes are a constant per platform, chosen by `process.platform` in one place** (`PLATFORM_LOCATIONS`, read through `locationsFor`) and never read out of the environment: macOS gets `/opt/homebrew/bin`, `/usr/local/bin`, `~/.local/bin`, `/usr/bin`, and Linux gets `/usr/local/bin`, `/usr/bin`, `~/.local/bin`, `/home/linuxbrew/.linuxbrew/bin`, `~/.linuxbrew/bin` — including the per-user Homebrew prefix neither list may take from the other. **A candidate that exists but whose version cannot be read is refused rather than skipped** — falling through to the next `fugaro` on the machine would silently change which installation the numbers came from |
| 5 — the child's environment is built, not inherited | exactly `PATH`, `HOME`, `TMPDIR`, `LANG`, `LC_ALL`, the five `XDG_*` variables and the chosen installation's `FUGARO_PROJECT`. A hostile `GOOGLE_APPLICATION_CREDENTIALS`, `AWS_*`, `SSH_AUTH_SOCK` or ambient `FUGARO_PROJECT` in the app's environment cannot reach the child |
| 5 — the child's `PATH` is a list, not one directory | the resolved binary's own directory first, then the platform's tool prefixes — macOS's `/opt/homebrew/bin`, `/usr/local/bin`, `/usr/bin`, `/bin`, `/usr/sbin`, `/sbin`; Linux's `/usr/local/bin`, `/usr/bin`, `/bin`, `/usr/sbin`, `/sbin`, `/home/linuxbrew/.linuxbrew/bin`, again from the one `PLATFORM_LOCATIONS` table — because the CLI looks up external tools itself (`exec.LookPath`, `internal/cli/doctor.go:147`), and `terraform` and `cosign` do not sit beside the `fugaro` binary even under Homebrew. The app's inherited `PATH` still never reaches the child, and a tool outside that list is reported as *not on the `PATH` the console gives the CLI* rather than as "not installed" |
| 5 — the working directory is set explicitly | the installation's checkout when it has one, otherwise a private `0700` directory the app owns under the system temporary directory, checked with `lstat` (not a link, owned by this uid, mode `0700`) rather than trusted to `mkdir`. Never `/`, which is nobody's checkout. `doctor` is called with `--dir <that directory>` and **never `--strict`**: outside a checkout its `plugin-pin` check is a warning that `--strict` turns into a failure about the checkout rather than about the environment on screen |
| 7 — the app never reads the operator's config | the config's *path* is a string on the installation, passed to the child as `--config`; the CLI is the reader. The checkout's pairing check is a **spawn** for the same reason: one allow-listed `doctor --json` with the checkout as the child's working directory, and a checkout that belongs to another project is refused with the CLI's own `this checkout belongs to project %s; --project says %s` — the only screen that asks for a checkout at all, and it asks at that moment, because the checkout is optional and nothing in the first run wants one — and **not stored**, because storing it would leave every screen refused later with nothing on screen explaining why |
| 10 — the spawn log (D20) | `src/main/spawnlog.ts`, documented below |
| 10 — the installations file (D13, C8) | `src/main/installations.ts`, documented below: the second and last file the app writes |
| Task 0.2's four settled behaviours | restart with the CLI's own backoff (1 s doubling to 30 s) except after a refusal, a signal read as the ordinary end of a feed, the UTC-midnight rollover marked as a **gap** rather than a zero, and the reader rule that a stdout line which is not a JSON object is **skipped** and surfaced as a notice — the degraded mode writes a plain sentence on the same stream as the documents (`runWatchDegraded`, `internal/cli/watch.go`) |

### The spawn log, and the two files the app writes

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
a job wrote. **A spawn that produced no process records nothing**: the line is
written when the child's own `spawn` event says the process exists, so a binary
that is missing or not executable — which `spawn` answers with a `ChildProcess`
and then an `error`, never with a throw — is not a line in this file. It is **bounded at 1000 records**: at the 1001st spawn the writer
appends one `{"truncated":true,"limit":1000}` line and stops, rather than
rotating or truncating in place. It is never read back, never rotated, never
deleted and never transmitted — `test/unit/spawnlog.test.ts` holds the first and
the last as scans over `console/src/`, so a screen, a source or an IPC handler
that started reading it would fail a named test. It is what Check 59's
assertion (4) reads and what Task 4.3's FACT commands read the key names from.

**It is one of the two files the app ever writes to disk, and the other one is
off by default too.** The other write in this task is a directory rather than a
record: `privateWorkingDirectory()` creates an empty `0700` directory under the
system temporary directory for an installation that has no checkout, so that
`doctor --dir` is a directory and the child's `cwd` is not `/`. Section 5.1 rule
10 is about *records* — no history, no cached document, no crash dump, nothing
that outlives the process — and this holds nothing at all. The **other record**
is the installation list, `installations.json`, which the owner ruled in on
2026-10-18 (C8) and which is documented in its own section below: rule 10 was
amended to name it rather than having it grow by accident.

### The spawn properties are tests, and each one fails under a mutation

The tests spawn **a fake `fugaro` in a temporary directory — never the real CLI
and never the network** (`test/unit/stub.ts`: an executable whose shebang
names the Node running the suite by absolute path, a generated script, and a
`scenario.json`).
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
- **Cleanup on quit or crash.** Nothing calls `CliSource.close()` on
  `before-quit`, and nothing stops the live children if the main process
  crashes. Today no source is ever constructed by the app, so there is nothing
  to leak; **the task that wires `handlersFor` must, as a precondition, stop
  every feed on quit** (`close()` from `before-quit`, awaited before exit).
  What this task does bound: every one-shot read has a deadline
  (`READ_TIMEOUT_MS`) and a stdout cap, the feed's line buffer is capped, and a
  source keeps at most `MAX_LIVE_FEEDS` (4) live children, stopping the least
  recently used.
- **Persistence.** `createInstallationStore` holds the list in memory for one
  run, and it took a `persistence` seam rather than a file: the store's
  read-and-write half is `src/main/installations.ts` (Task 3.4b), which owns
  `installations.json` and is documented below. The in-memory default is what
  the unit tests and the e2e harness use, and nothing here writes.
- **The fixtures.** `FugaroSource`'s second implementation is Task 1.4's
  recorder and replay, and it slots in behind the same interface — see the
  section below. What landed there is the tool and one synthetic fixture; the
  design's eight recordings are still the owner's to make.
- **`version --json` and a schema version.** The CLI has neither (contract §4.3
  gaps 1 and 3), so the handshake is the one human line and the console refuses a
  binary whose line it cannot read rather than guessing. **The one line it reads
  without a version in it is `dev`**, which `var Version = "dev"`
  (`internal/cli/root.go`) prints for a binary built without the release
  `-ldflags`: that is a *recognised* line and it is not one of the refusals — the
  console accepts it in the **reduced** mode, with the banner naming it as
  unplaceable, because refusing it would block the person building the CLI and
  the console together. Nothing else is guessed at: a word the console does not
  recognise stays the refusal above (plan open item 3, answered by the owner
  2026-10-22; `src/shared/version.ts`'s `classifyCliVersion`, pinned in
  `test/unit/version.test.ts` and `test/unit/window.test.ts`).

## Recorded, redacted fixtures, and the recorder (Task 1.4)

**The tool is agent-written; the recordings are OWNER-RUN.** This is the
distinction the whole section turns on, and it is a boundary rather than a
division of labour: an agent may not run `fugaro watch` against a real
installation, may not read `~/.config/fugaro`, may not handle a credential and
may not touch `belong` (Global Constraints 2 and 6), so **no agent ran the
recorder in this task, and no file under `test/fixtures/` was recorded.** What
is agent-written is the recorder, the redactor, the fixture format, its loader
and the replaying source; what is the owner's is the eight recordings design
§9.1 asks for.

Three files make up the data plane's second implementation, and they have three
different jobs:

| File | What it is |
|---|---|
| `src/source/redact.ts` | the redactor: what a recording may carry into this repository |
| `src/source/fixture.ts` | the format, the loader that verifies it, and `FixtureSource`, the replay |
| `tools/record-fixture/index.ts` | the recorder: one `fugaro watch --json` → one fixture file |

### The owner's one command

```
npm run record-fixture -- --out test/fixtures/<name>.ndjson --project <project> --gcp-project <gcp-project-id>
```

Run **by the owner**, from the sandbox checkout, with the **sandbox**
installation selected — never `belong`. The eight recordings are the owner's to
produce, and the plan attaches them to this task's pull request by hand or in a
follow-up commit. `--project` is **required and never defaulted**: falling back
to whatever the ambient selection happens to be is precisely how a recording of
the wrong project gets made (design §3.4 rule 3), so its absence is a refusal
that names the flag. `--gcp-project` is required too: the recorder reads no
config, so it is told the GCP project ID, and the ID, its runs bucket
(`fugaro-runs-<id>`), an optional `--project-number` and any `--literal`
(repeatable) are handed to the redactor as literals. The rest of the flags,
and the refusals, are in `--help` / `USAGE`.

The script is `tsc -p tsconfig.record-fixture.json && node
.record-fixture/tools/record-fixture/index.js`: the tool is TypeScript like
everything else here, and `.record-fixture/` is a build output directory that
`.gitignore` names. The recorder spawns through the console's **own**
`planFor('live.watch', …)`, so the argv, the child environment
(`CHILD_ENV_KEYS`), the working directory and `shell: false` are the console's
rules and not a second copy of them — a recording cannot be of a command the
console would refuse to run. It records for a bounded time (`--duration`, 120 s
by default), then stops the child the way the console does, `SIGTERM` and then
`SIGKILL` after `SHUTDOWN_GRACE_MS`. It reads until the child's pipes **close**
(not until the process exits: Node may report the exit while the last stdout
chunks are still in the pipe), with one more grace period after the `SIGKILL` for
a pipe that never closes. A torn last line is dropped rather than
recorded as garbage, while a last line that is a whole JSON document — a CLI
that wrote its final frame without a newline — is recorded like any other.

### The `normal` fixture: the real CLI's shape, every value invented

`test/fixtures/normal.ndjson` is the one fixture of the eight that exists. The
owner ruled on 2026-10-19 "record from the real installation, scrub by hand". One
snapshot (`fugaro watch --json --once`) was read from a real installation with
the 0.8.6 CLI (header `cli: 0.8.6`), outside this repository, and
`tools/scrub-fixture/` replaced every value that could identify it. **The shape
is the real CLI's** (every key, its type, its order, which optional keys are
present); **every value in the file is invented**, so the header says
`synthetic: true` and its note says every value is invented (the two agree: no
installation produced these values). The raw recording is never committed and is
deleted after the scrub. **It is not reproducible from this repository, by
design:** the committed fixture is the only artefact, and nothing is kept that
could lead back to a removed value (no list, no hash, no salt). *A file of
digests of the removed values was in PR #643's branch until review removed it;
the paragraph below says what that leaves exposed, and it is not nothing.*

```
node node_modules/typescript/bin/tsc -p tsconfig.record-fixture.json
node .record-fixture/tools/scrub-fixture/index.js --in <raw.json, outside the repo> \
  --out test/fixtures/normal.ndjson --cli 0.8.6
```

The command prints the COUNT of values it replaced and nothing else, and the test
gets the removed values in memory, through a callback the scrub run is given, on a
scrub of a synthetic input: nothing derived from a removed value is written
anywhere. One file of digests did exist on PR #643's branch, and **it is still in
the pull request's history — this is the honest account of it.** PR #643's first
commit (`d41f726b`) added `console/tools/scrub-fixture/normal.scrubbed.json`, a
JSON array of the unsalted SHA-256 digests of the values the scrub had replaced,
and a later commit on the same branch (`2fc37c7fd8`) removed it again. The PR was
**squash-merged**, so neither commit is on `main` and no commit on `main` ever
held the file — but the pull request's own history on GitHub still does, and a
squash does not delete anything from the remote. (Those two commits are not in a
clone of `main` either: `git cat-file` does not know them, and this branch was
created after the squash, so nothing was ever in its history either. So this
paragraph is the account of what the PR's history holds, and a reader here cannot
read the file itself to check it.) An unsalted SHA-256 digest is a dictionary: a
small integer, a dollar amount or a repository name is recovered from a bare
digest by trying candidates, so the values that file covered must be treated as
**exposed**, not merely removed. Those values were a public repository's name, the
project's name and some development spend figures. Nothing here undoes that; the
note is here because a reader of this file is the person most likely to believe
the opposite.

The scrub (`scrub.ts`) is fail-closed: an unknown key is a refusal, and every key
it copies has its own typed rule (numbers are copied; an enum is copied only if
the value is in the CLI's vocabulary; a free string is refused unless the key has
an explicit replacement). It replaces the project, repo, slug, run ID, title
(`# Task: example task N`), models, requester and kill actor, PR URL, workflow,
recipe, the 0.9.0 job triple (`job`, `profile`, `provider`) with the two people
beside it (`account`, `launched_by`), the credential's **name** (`credential`,
0.10.0) and every dollar amount, and **shifts the whole clock by a random offset
drawn for the run** (`now`, `day`, the header's `recorded`, `kill.at`,
`waiting_until`, and the run IDs' timestamps, which are the shifted `now` minus
the run's own age, so every `age_seconds` stays consistent). Totals,
percentages, `runs` and `run_hours` are recomputed from the invented rows so the
document's invariants hold. The result then goes through the console's redactor with a public key (as
`synthetic.ndjson` does), which is why the repo and slug read as pseudonyms
(`r-...`, `slug-...`) and the project as `<project>`. Kept as the CLI wrote
them, and only if in the fixed vocabulary listed in `scrub.ts` (`VOCAB`,
`VERIFY_KINDS`, `QUEUED_NOTE_SHAPE`): mode, stage, round, health, deadline, auth,
verify, connection state, `queued_note`, and the ages and burn numbers. A
connection `reason` is the one value with a free-text branch: the two sentences
the CLI writes itself are kept, and the transport's own error text is replaced
rather than copied (see the vocabularies below).

**The clock shift is drawn, not committed** (owner review of PR #643, item 2).
`scrubDocument` takes the offset as an argument and, given none, draws a
crypto-random one of up to ±400 days; the tool never passes one and never prints
it. So the scrub is **not reproducible byte for byte between runs**, and that is
the point: `normalFixture` takes the offset as an optional argument too, so a
test can hold the clock still and scan a scrub run that is the same on every
machine — the tool never passes one, and the tests that hold the drawn clock's
randomness use the scrub's own draw (`scrub-vocabulary.test.ts`: the offset is an
integer inside the range; `normal-fixture.test.ts`: two unsaid draws differ).
The fixture of PR #643 was shifted by `TIME_SHIFT_MS`, a constant in
the file, and that constant added back to its `now` gave the real recording
instant exactly. The committed fixture was re-scrubbed from its own structure
under a drawn offset (`console: re-scrub the normal fixture…`), so the file on
`main` is not a shift of anything and the old constant is dead against it — it
still recovers the instant from the *history* of that write, which is why the
paragraph above says what it says. Nothing but the clock, the run IDs, the
header's `recorded` and its checksum moved in that re-scrub: the invented
project, repos, slugs, titles, amounts and verify lines are the ones they were.

**The vocabularies are derived from the Go source, and a Go test holds them
there.** Every list in `scrub.ts` names the writer it came from, and
`TestDocsDesktopConsoleScrubVocabularyMatchesTheGoSource` — in
`internal/cli/docs_desktop_console_test.go`, one of the two Go files Task 0.3's
boundary test allows to mention a `console/` path (the other is the coordinator
command's moved copy of that check, `ee/coordinator/cli/console_contract_test.go`),
reading this file as bytes and importing nothing from it — reads the lists back
out of the file and compares them with the constants the source writes
them from: the runs[].stage vocabulary, the config's allowed `agent.auth`, the
budget's modes, the three verify kinds, the two status words and the rerun flag,
the one warning sentence and the clean-tree note, the connection, health,
deadline, status and outcome tables, the two connection `reason` sentences
`internal/watch/state.go` writes itself (`CONNECTION_REASONS`), the two
`commands.reports` globs the warning
sentence carries (`VERIFY_REPORT_GLOBS`, against `internal/config/defaults.go`),
and the 120-rune clip the registry puts on the summary (`VERIFY_CLIP_RUNES`,
against internal/runner's `verifySummaryRunes`) — the scrub needs the number
because a clipped line is a PREFIX of the writer's, which is what its matchers
are built to accept. The verify line's *head* is not a list but a
grammar, so it is held differently: the test reads `VERIFY_HEAD`'s regex source
out of the file, fills its `${...}` interpolations with the values it derived,
compiles it and tries it against lines built from `verify.go`'s own format
string — with the exit code `internal/procgroup/procgroup.go` leaves for a
process it killed, which is `-1`, so a timed-out verify writes `(exit -1; timed
out)` and a pattern narrowed to `\d+` fails there. The *tail* is held the same
way, and was the gap this round closed: the test renders `Record.Summary`'s
segments and the file's `VERIFY_TAIL` from their own sources — a literal as
written, the writer's `%d` as a digits marker, each `strings.Join` as a
test-names marker, and `r.Warning` as the warning sentence the Go source builds
plus the globs marker — and compares them segment by segment, in order. So a
segment added to `verify.go` (a `; N skipped` for the `Skipped` count the summary
does not print today, say) fails here instead of leaving the scrub refusing an
ordinary recording. The segment scan skips the `if` line a segment sits under (a
condition names what it guards without writing it), so the one condition a
segment *is* decided by is read on its own: the counts are written under
`r.Kind == KindTest` and no other kind, which is what lets the grammar slice that
group off for a build or a lint and require it for a test. The list that was too *short* is
the failure that review found: a value the CLI writes and the scrub does
not know is not a leak, it is a recorder that throws on an ordinary run.
`bootstrap` and `review_first` were missing from `stage`, `vertex`
from `auth`, and `warn` was a mode no CLI writes.

**A list that is too *long* is the same failure from the other side, and the
scan has to read the right field to see it.** `stage` held five names
`runs[].stage` cannot carry — `provision`, `check`, `render`, `writeback`,
`finalize` — because the scan took every stage string in `internal/runner`. Only
one of those is the row's: `internal/watch/build.go` writes `dash(e.Stage)` from
the run's **registry entry**, which is given a stage by exactly two writers — the
literal it is created with, `bootstrap`
(`internal/runner/budget_session.go`'s `registryEntry`), and `beginBudgetStage`,
whose only caller is `r.stage(ctx, name, ...)`, so the rest are that helper's
call sites. The others are other documents: `r.rec.Stage` (`render`,
`writeback`, `check`, `finalize`) is the run record's own stage in `result.json`,
and `provisionStage` names the timing row provisioning records. The scan now
reads the entry's literal plus `r.stage`'s call sites plus the `queued`
`internal/watch/queued.go` gives a runs-bucket row with no entry yet, and pins
that `beginBudgetStage` still has that one caller taking its own `name`
argument. `sameSet` compared the two lists in both directions already — the
premise, not the comparison, was what a too-long list slipped past.

**A connection reason is kept only when the CLI wrote it.** `connection.reason`
had no rule at all, so any recording made while the link was not live failed the
scrub — `internal/watch/state.go` sets `access refused` for a permission error
and `connecting` before the first event, and its third branch is the transport's
own error, one-lined and clipped there. Those two sentences are
`CONNECTION_REASONS`, held against the source by the drift test; anything else
that is a string is **replaced** with `INVENTED_CONNECTION_REASON`, so the free
text never reaches the fixture, and anything that is not a string is refused.
The scan that gates a commit carries all three values in its allow-list, and
`normal-fixture.test.ts` holds the whole path — scrub, redactor, writer, scan —
for each of them: accepting a reason the scan then refuses would leave the
recording the rule exists for uncommittable, which is the shape of the finding.
That test is one case of a **round-trip property** the file also holds in full:
every value of every list in `VOCAB`, and every invented value the scrub writes
in its place, goes through the whole tool and the result is scanned, so a
vocabulary value the allow-list cannot carry fails there rather than at the
first real recording. (The verify line is the one value the property does not
sweep: the scrub keeps it as the writer wrote it, while the scan's matcher pins
the globs slot to the CLI's own two defaults on purpose, so the two matchers are
not meant to agree.)

**Not recorded, and stay invented**: `silent-then-lost`, `kill-mid-feed` and
`repo-at-cap` (they need cloud writes), and the other cases still await a
recording. `normal` is one snapshot: no `finished`, `ready` or queued runs (the
runs bucket did not answer within 3 s on that read), so those shapes are covered
by `synthetic.ndjson` and the Go types, not by this file.

**No spec consumes `normal.ndjson` yet**: only its own tests
(`test/unit/normal-fixture.test.ts`, the Go
`TestDocsDesktopConsoleNormalFixtureMatchesTheGoTypes`) and the recorder tests
read it.

Held by `test/unit/normal-fixture.test.ts`: the shape, the invariants, a scan of
every decoded key and string value (after NFKC, case and zero-width folding)
against forbidden substrings and a positive allow-list of shapes, and the scrub's
refusals, each mutation-proven. One rule in that scan refuses a run of twelve or
more decimal digits — a leaked amount, identifier or epoch-millisecond stamp the
allow-list would otherwise take for an invented value. It reads a number, not a
digest: the header's SHA-256 is hex, so twelve consecutive digits occur inside it
about 7% of the time (1504 of 20000 digests, and 145 of 2000 scrubs, measured),
and the digest is redrawn every run because the scrub draws a fresh clock
offset. As a bare `\d{12}` the rule failed the scan at random, about one run in
fourteen; it now matches a run only with no hex digit beside it, so the digits
inside a longer hex token (a digest, a pseudonym, a commit) are not a number
standing on its own — 0 of 20000 digests, measured — while a planted
`123456789012`, `1234567890123` or `acme-123456789012` still is. The run is the
whole maximal one (`\d{12,}`): a fixed twelve would match neither lookaround
inside a thirteen-digit stamp, since every twelve-character window there has a
digit on both sides, and that stamp is exactly what the rule is for — the verify
line's name slot is open in the allow-list, because `junit.go` copies an ID
verbatim, so this rule is the only guard against a number riding in it. The proof
is a deterministic case (a 64-character digest with a twelve-digit run planted in
it, plus runs of 12, 13 and 20 digits standing on their own) and a loop that
scrubs and scans 300 drawings of the clock.

**The removed-values rule is a substring rule, and it does not run over tokens
the scrub drew.** The second flake of the same loop, and the one that failed on
`main` (CI run 39231293918, `iteration 252`): the scan looks for each removed
value as a substring of every string in the fixture, and a plain recording's
`kill.at` sits an hour before its `now` — so an offset of between 22 and 23 hours
(the shift is uniform over ±400 days, one offset in 19200) puts the shifted
`kill.at` back on the recording's own date while the shifted `now` lands on the
next one. The recording's `day` is a removed value as soon as the offset leaves
it, so the rule read that day inside a timestamp that is a function of the
offset, and the run failed: about one run in sixty of the 300 draws, which is
exactly the shape of a flake nobody can reproduce. Inside a token the scrub draws
— the shifted clock (`now`, `day`, `kill.at`, the header's `recorded` and `at`),
the run IDs stamped from it, and the body's SHA-256 — nothing of the recording is
present, so only coincidence can put a removed value there, and the rule is not
run over them. It is still run over `removed.includes(raw)`, and the exemption is
the shape of the value the *fixture carries*, never which removed value it is: a
removed day riding in a verify line's name slot (the one slot the allow-list
leaves open) is refused, and that case is held.

**The draws are committed, so the loop is deterministic.** The 300 offsets no
longer come from the scrub's own draw but from `CLOCK_SEED` in the test (a
mulberry32 walk, written out there): a test that draws its own randomness passes
on every machine but the unlucky one, which is what happened. The walk asserts
what it is made of — that at least one of its 300 headers carries a digest with
twelve digits in it, and that at least one of its offsets crosses the date — so a
seed that stopped covering either flake fails the walk instead of passing
quietly, and the crossing draw is a named regression case that fails when the
rule is restored to the substring-only one (both mutations were run: restoring
the rule fails the walk at `iteration 13` and the regression case; exempting the
removed value's shape instead of the fixture's fails the name-slot case). The
scrub's own draw is unchanged and still random — `scrub-vocabulary.test.ts` holds
its range and `normal-fixture.test.ts` holds that two unsaid draws differ. Held by `test/unit/scrub-vocabulary.test.ts`: that
the scrub accepts every stage, auth and mode the CLI writes and refuses what it
does not — the stage list it loops over is `VOCAB.stage` itself, so this file
keeps no second copy of it — that the connection `reason` the CLI wrote is kept
and the transport's own error text replaced, that the verify shape is the one
`internal/verify/verify.go` writes
(every tail variant, the `FAILED` spelling, the `exit -1` a killed process
leaves, and the 120-rune clip) and refuses a line it cannot, that the pinned
matcher takes the IDs `junit.go` builds verbatim — pytest parametrisations, names
with spaces or a bare comma, `TestFoo#01`, an escaped bidi character — and holds
the warning globs to the CLI's own two, that the nullable Go
fields may be null, and that the clock shift is drawn per run — all
mutation-proven, and the vocabularies themselves pinned against the Go source by
the drift test named above.

**A test line carries its counts; no other kind carries any.** `Record.Summary`
appends `, %d tests, %d failures` under `if r.Kind == KindTest` and nowhere else,
so the grammar's first tail group is sliced off entirely for a build or a lint
and required **whole** for a test. Requiring it whole is what the writer does, not
a tightening for its own sake: every run writes it, and the 120-rune clip lands
far past it, so a test line that stops at `(exit 0)` — or part way through the
counts — is one no run can produce, and an earlier grammar took it as a clipped
one. Both directions are tested (`fugaro verify build #5: passed (exit 0)` passes,
the same line with counts does not), the clip test's cuts now start where the
counts end, and the drift test reads the `KindTest` condition itself, since its
segment scan skips the `if` line.

**The scan pins the verify line's globs slot; nothing can pin its name slots.**
The verify line is the one value whose middle the CLI fills from the repository
it ran in — the test names its reports carry and the globs it was configured to
read — so `scrub.ts` has two matchers for it. `isVerifySummary` is the scrub's
rule and leaves those slots open on purpose: a real recording's test names are
the writer's own output, and a vocabulary that refused one would refuse an
ordinary run. The fixture's scan uses `isKnownVerifySummary`, the same grammar
with the **globs** slot pinned to the two `commands.reports` defaults the CLI
writes there with no configuration — any other glob is text the CLI was never
given, and with an open slot `**/acme-payroll/*.xml` passed the scan too.

The name slots are open in **both** matchers, and that is not a leftover: an
earlier round pinned them to a character class that looked like Go test names
(`[A-Za-z0-9_.:/\-#]` and `, `), which was wrong twice over. `internal/verify/
junit.go` builds each ID as `classname + "." + name`, verbatim from the JUnit
XML, so a pytest parametrisation (`test_mod.py::test_add[1-2]`, and one carrying
`, ` of its own), a name with a space, the `#01` `go-junit-report` appends to a
duplicated subtest, and the escape `internal/pluginwire` writes in place of a
control or bidi character are all ordinary names — and a recording the pinned
matcher refuses cannot be committed, which is the failure these vocabularies
exist to avoid. What guards that slot is the fixture scan's forbidden-substring
pass, which runs over the **whole** verify value, name list included: a
`dimipaun/fugaro` planted in a failed-name slot is caught by that pass, not by
the shape matcher.

The warning sentence is not a slot in either matcher —
`Record.Summary` writes one sentence there, and the drift test fails first if
`verify.go` ever gains a second one.

### The eight recordings, and why seven of them are missing

Design §9.1 asks for eight cases, one recording each, and `FIXTURE_CASES` in
`src/source/fixture.ts` is that list in that order:

`normal`, `silent-then-lost`, `kill-mid-feed`, `repo-at-cap`, `oauth-notional`,
`absent-field`, `gap`, `hostile-title`.

**None of the eight exists on this tree, and the reason is not effort.** Three
of them cannot be produced without a live sandbox doing real work: a run that
stops heartbeating and then goes `lost` needs a real stalled run, a kill switch
flipping mid-feed needs a real `budget kill` against the sandbox, and a
repository at its cap needs real spend to accumulate. The other five could be
recorded from a sandbox checkout, and they are missing for the same reason:
**the recorder is the owner's to run, full stop.** They land here as
`test/fixtures/<case>.ndjson`, one file per name in `FIXTURE_CASES`, which is
the only naming `fixture.test.ts` accepts beside the two fixtures this tree
writes from invented documents, `synthetic.ndjson` and `degraded.ndjson` — a
stray file fails a test by name, while the owner's recordings do not have to be
preceded by an edit to the suite. Until they land, `test/fixtures/` holds those
two files and no recording at all, and each is labelled for what it is:

```
{"fugaro_fixture":{"version":1,"redactor":"fugaro-console-redact/2","case":"synthetic",
 "project":"<project>","synthetic":true,"records":6,"sha256":"183119f546d9b1875aac902b79f68817af8fe9e93b0659b7d731adf552ad5f0f",
 "note":"synthetic: invented documents put through the same redaction path as a recording; never recorded from any installation or cloud"}}
```

(`degraded.ndjson` is Task 3.1's: a project with no budget backend, which the
sandbox every recording is made against cannot be put into, so the degraded
screen and the recorder cannot be exercised from the same fixture. Its header
says `"case":"degraded"`, and `FixtureCaseName` names it for the same reason.)

`synthetic` is deliberately **not** a member of `FIXTURE_CASES`: the eight are
the recordings, and a reader who saw nine names would have to work out which one
was real. The invented documents live in `test/unit/synthetic.ts`, not only in
the committed file, and `fixture.test.ts` regenerates the fixture from them and
asserts the committed bytes are exactly what comes out — **byte-identity, not a
diff** — so a hand-edited fixture fails a named test even if its digest were
recomputed by hand, and a reader can see which documents the file was made from
without a decoder in their head. Those documents cover the axes a replay has to
reproduce (a normal document, the next tick with the numbers moved, the degraded
mode's sentence as a *notice*, a silent run, a 160-second gap with the run
`lost`, and a run with fields the CLI omits), which is why one synthetic fixture
is enough to make the harness exercisable while the real eight are missing —
**and it is not a substitute for them.** No screen has ever claimed to be tested
against real CLI output, and this section does not claim it either.

### The redactor: deny-by-default, and fail-closed twice

A recording is made on a real installation against a real project from a real
checkout, so every document in it can carry the operator's world — their project
name, their machine, their home directory, their repository names, their
colleagues' addresses and branch names, and whatever a job wrote into a title.
None of that belongs in a commit. `src/source/redact.ts` is what stands between
a recording and `test/fixtures/`, and it is written to be told nothing:

- **Every string is hostile until a rule has passed over it.** Two kinds of rule.
  *Field classes*, where the key names what the value is and **the whole value
  is replaced**: a repository (`repo`), a slug, a project (`project`,
  `gcp_project`, `project_id`, `project_number`), a bucket, a path (`path`,
  `dir`, `cwd`, `checkout`, `binary`, `config`, …), a host (`host`,
  `hostname`, `ip`, …), a URL (`url`, `pr_url`, `log_url`, any `*_url`), a
  credential (`token`, `secret`, `password`, `private_key_id`, any `*_token`,
  `*_secret`, `*_password`, `*_api_key`, `*_session_id`, …; keys are read
  through camelCase and hyphens, so `githubToken`, `X-Api-Key` and `sessionId`
  are credentials too), a person or account (`requested_by`,
  `author`, `user`, `actor`, `owner`, `account`, `oauth_account`, any `*_by`,
  `*_email`; `by` everywhere except a report's break-down dimension), a branch
  (`branch`, `base_branch`, `ref`, any `*_branch`), an opaque identifier
  (`execution`), and a digest (`sha256`, `pushed_head`, …, kept only when it
  is hex). A reserved key's **whole subtree** is its class (`{"branch":
  {"name": …}}` is a branch all the way down), a **number** under a reserved
  key becomes its marker (`project_number: 123456789012` is `<project>`), and a
  number that is one of the literals becomes `[redacted]`. *Pattern classes*
  apply to **every** string whatever its key is, and they read its **prepared
  form**: NFKC first, then every default-ignorable, bidi and control character
  stripped — so a fullwidth `＠` *is* an `@` before the address rule looks
  (`ana.smith＠acme.com` is `redacted@example.invalid`, local part and all), and
  a zero-width space, a soft hyphen or a bidi override cannot split an address
  or a literal. A percent-encoded run is decoded (up to three rounds, so
  `%2540` is an `@`) and anything a rule finds in it replaces the run:
  `u=ana.smith%40acme.com` is `u=redacted@example.invalid`. The classes: a URL; a token (GitHub, OpenAI/Anthropic, Slack,
  GitLab, npm, AWS, Google API key, access and refresh token, JWT, bearer
  header, PEM block, a percent- or base64-wrapped one decoded first, a
  `name=value` assignment whose name says secret, and any long high-entropy
  run, so a token split across two fields loses both halves, and one split
  across adjacent strings of an array loses every piece); an address in
  any script, IDN domains included; a branch by its prefix or `refs/`; an
  absolute path, consumed whole with its spaces and non-ASCII characters; a
  repository path; a GCP project or bucket named in a sentence and the runs
  bucket by name; an IPv4 or IPv6 address; a hostname; and the installation's
  own literals, matched case-insensitively, with Cyrillic and Greek
  look-alikes folded (`acme-dеvel` with a Cyrillic `е` is the literal), and
  with a host literal's stem (`Zoras-Laptop-Pro.local` is also
  `Zoras-Laptop-Pro`). **A person is identity wherever the document spells
  them**: every person-class value is collected before anything is redacted,
  and the same name anywhere else in the document — matched on whole words,
  in any case, in NFKC, look-alikes folded, with any separator between its
  words and last word first (`Ana  Smith`, `Ana-Smith`, `Smith, Ana`) — gets
  the same pseudonym its field got. The CLI titles a queued run with no
  workflow by its requester (`internal/watch/queued.go`), and `requested_by` is
  the config's free-form `user:`, so without this a queued run's title carried
  the name its `requested_by` had lost. **People and literals are replaced
  after every pattern rule**, never before: a requester called `ya29`,
  `password`, `local` or `42` would otherwise switch off the rule for a token,
  an assignment, a host or an IP address. The names are a hash table of word
  sequences, one scan of each string's words whatever the number of people, and
  a document naming more than 4096 distinct persons (or a person in more than
  16 words) is refused. A requester that is a contract keyword (`queued`,
  `run`, `api`, ...) or the `-` placeholder is not a name, and the contract
  fields (`run`, `id`, `stage`, `status`, `auth`, `health`, `deadline`,
  `verify`, `outcome`, `mode`, `state`) are never rewritten by the person pass.
  **A notice gets the person pass too, with the recording's people**: a notice
  is free text with no fields, so the only names the redactor can know in one
  are the ones the recording's documents named. The redactor keeps every person
  any document named, and the recorder re-runs every notice through it once the
  recording is over, so a notice written *before* the document that names its
  person loses the name as well. A name no document in the recording ever
  named is not found in a notice — there is nothing to find it by.
  Every redacted document must be a **fixed point**: redacting it again must
  change nothing, or the recording is refused. A document
  with a key nobody anticipated still cannot carry an address out of the room, and a test per class holds each one — a run title, a
  requester address, a repository name in the field *and* in a sentence, a
  branch name, a POSIX and a Windows path (including the `/usr`, `/etc`,
  `/System` and firmlink forms a prefix whitelist would have missed), a token by
  shape and by key, a URL with userinfo, the installation's own literals, and an
  address under a key nobody anticipated.
- **A key is a string too, and it is the one string a rule cannot replace.**
  Renaming a key would change a document's shape — `repo`, `title`, `day` and
  `burn` are names the console reads — so a key that carries identity is a
  **refusal** instead: `{"ana@example.com": …}`, `{"acme/secret-repo": …}`,
  `{"/Users/ana/src/fugaro": 1}` and a key naming the installation's own project
  all end a recording rather than travelling inside one that redacts to itself.
  The audit walks keys with the same rules as values, and reads a key's *parent*
  through an array index, so `{"kill":[{"by":"Ana Smith"}]}` is a person there
  as it is in the object form. A `__proto__` key is refused rather than
  silently dropped by the copy. **A refusal names no identity**: it names the
  class and the path, prints a value as its length, and prints a key that is
  not plain snake_case (or that itself carries a class) as its position
  (`<key 3>`).
- **Every rule runs in time linear in its input, and the input is capped.** A
  pattern that can fail after scanning a run starts only at the start of that
  run; the percent-encoded rule is a single character-class scan and a
  hand-written decoder. At 96b81997 three rules were quadratic — 100 KB of
  `a` took half a minute, and the recorder's 4 MiB line bound made that about
  twelve hours inside the stdout handler, where the `--duration` timer cannot
  fire. Now a megabyte of every shape that was quadratic takes well under a
  second, and `redact-final.test.ts` holds each shape to a 4x-input / under-10x
  time ratio. On top of that, a string or key longer than
  `MAX_REDACTABLE_STRING_LENGTH` (64 KiB) and a feed line longer than
  `MAX_REDACTABLE_LINE_LENGTH` (1 MiB) are **refused before any rule runs**,
  never truncated; the loader holds the same string cap and a record line cap
  of twice the line cap. A document nested deeper than 256 levels, a string
  whose NFKC form is more than twice its length, and a document whose strings
  together grow by more than 64 KiB under NFKC are refused too (a `RedactionRefusal`,
  or a `FixtureRefusal` on load), never a raw `RangeError`.
- **Redaction is a marker, not a deletion, and the markers are a closed set**
  (`<path>`, `<project>`, `<bucket>`, `<host>`, `<branch>`, `[redacted-token]`,
  `[redacted-url]`, `redacted@example.invalid`, `[redacted]`). A marked value is
  unambiguous on sight, so nothing in a fixture can be mistaken for real data,
  and a value that is *already* marked is left alone — which is what makes the
  whole function **idempotent**, asserted as `redact(redact(x)) == redact(x)`.
  Three classes keep a per-value pseudonym instead of a fixed marker — a
  repository (`r-1a2b3c4d/r-5e6f7a8b`), a slug (`slug-1a2b3c4d`) and a person
  (`person-1a2b3c4d`) — because a fixture with several repositories is only
  worth having if the rows stay distinguishable. **A pseudonym is an
  HMAC-SHA-256 under a per-recording random key**, drawn when the recorder
  builds its redactor, held in a closure and never written anywhere: the same
  repository is the same pseudonym within one recording and a different one in
  the next, and the loader never needs the key. (An earlier version used
  SHA-256 with a public salt cut to 32 bits; for a guessable name — an
  organisation, a product — that is a lookup table away from the name, so it
  was not one-way in any sense that mattered.) The synthetic fixture uses a
  fixed, public, synthetic-only key so its bytes are reproducible; its names
  are invented.
- **It fails closed, twice.** A line that is not a JSON object is refused rather
  than passed through, and every document is **audited** after redaction: if any
  class is still present after the rules have run — any pattern class above, a
  literal, or a reserved key whose value is not its class's marker — the call
  throws. A bug in a rule is therefore a failed
  recording, not a leaked fixture. The refusal names the class it refused on, so
  a failed recording can be read rather than guessed at.
- **It redacts identity, not hostility, and it does not invent data.** A
  `<script>` in a title stays a `<script>`: the design wants a hostile-title
  *recording*, and making text safe to *render* is the display layer's job (Task
  2.0's `src/shared/sanitize.ts`), not the recorder's. Numbers, booleans, `null`
  and an **empty string** pass through untouched — `pr_url: ""` is a run with no
  pull request, not a URL to redact — because a fixture whose absent fields had
  been turned into markers would be a fixture that lies about absence.

**And the same audit runs on the way back in.** `loadFixture` audits every
document and every notice it loads, so a fixture edited by hand after it was
written is refused too, with `FixtureRefusal` naming the class. The loader is
told no literals, but every other class — addresses, hosts, IPs, GCP names,
branch and person fields, tokens, paths — needs no list, and is checked. **What
the loader cannot check is a person's name in free text** — a title or a
notice: it has no person table, so a name is only a name to it in a person
field. A name in free text is kept out by the recorder's person pass above,
not by the load-time audit, and a hand edit that writes one into a title or a
notice is not refused on the way back in.

### The format, and what the loader refuses

A fixture is NDJSON: one header line and one line per record. The header is the
file's paperwork — the format version, **the redactor that made it**
(`redactor: "fugaro-console-redact/2"`), whether it is synthetic, which case it
is, the project *as redacted*, how many records follow, and **a SHA-256 of the
body**. The records are what the console replays: an arrival stamp and either a
document or a notice, which is exactly the pair `decodeFeedLine` produces from a
child's stdout.

`loadFixture` verifies **before** it returns anything, and refuses by name at
each step: the header parses; the format version is one this build knows; the
redaction marker names a redactor this build knows; the body's digest matches
the header's; **the header itself is redaction-clean**; the record count matches
the header's; every record is a well-formed envelope with a non-decreasing
timestamp; and every document and notice passes the redaction audit. The
header's own audit is not decoration either: **the digest covers the body, not
the header**, so a hand-edited `"project"` — a value `FixtureSource` hands a
screen as the installation's name — would otherwise pass every check above it.
**The digest is an unkeyed SHA-256 and proves nothing about who wrote the
file**: anyone who edits a fixture can recompute it. It catches an accidental
truncation or a partial edit, nothing more. What stands between a hand-edited
fixture and a replay is the redaction audit, and, for the synthetic fixture
only, the byte-identity test against `test/unit/synthetic.ts`. The redactor
check is a version check: a fixture stamped by an older or unknown redactor is
refused, because its contents were judged by rules that are not the ones in
front of it.

`FixtureSource` then implements the whole `FugaroSource` interface over one
fixture, so every screen and every later test can be driven with no child
process, no network and no installation. **The live feed replays the recording's
own clock**: ninety seconds between two records is ninety seconds in the stream,
the documents go through the same `FeedTracker` a real child's frames go
through, and the notices arrive on the same stream in the same place. A fixture
that replayed its records back-to-back would answer the wrong question — the
eight cases are *about* time, and "a gap in the recording is a gap in the
replayed stream" is what makes Task 3.1's `stale` tests mean anything.

### The recorder's refusals are the point

Everything the recorder does before it spawns is a refusal rather than a
guess, and this is what makes it a tool an agent cannot be talked into
misusing. `parseRecorderArgs` refuses, by name: a missing `--project` or
`--gcp-project`; the project `belong`, however it is spaced or spelled (values
are NFKC-normalised, stripped of whitespace and control characters, folded
for Cyrillic and Greek look-alikes, lower-cased, and stripped of a trailing dot
per segment before the check, so `bеlong` with a Cyrillic `е` and `belong.` are
refused too); **any `--out` outside this repository's
`console/test/fixtures/`** (any directory merely *named* `fixtures` was
accepted once) or not ending `.ndjson`; an existing fixture without `--force`; an unknown flag; a
`--duration` that is not a positive number; and a `--case` outside
`FIXTURE_CASES` — a `--case wibble` would write `test/fixtures/wibble.ndjson`, a
file `fixture.test.ts` then refuses, so the refusal would land on whoever
replays the recording rather than on the invocation that made it. `OUT_OF_BOUNDS` also refuses a path
carrying the fragment `.config/fugaro`, normalised for case and for Windows
separators, so an invocation pointed at the operator's config stops before it
spawns anything. And a recording that read **no document at all** is a refusal
rather than an empty fixture: an empty file would look like a quiet installation
rather than a failed run.

The recorder never prints the child's documents, and it never echoes its
stderr either — the operator's terminal is theirs, but a scrollback is a leak
this tool will not create. A stdout line longer than
`MAX_REDACTABLE_LINE_LENGTH` (1 MiB, the redactor's own bound) ends the
recording before it reaches the redactor, so the buffer cannot grow without
limit and no single line can hold the event loop. The child's stderr is **drained and discarded**:
drained, because `planFor` gives the child a pipe and a pipe nobody reads fills
up and then blocks the child's write, which would stall a recording of a CLI
that logs; discarded, because a stderr line is raw CLI text rather than a feed
frame, and putting it in the fixture as a *notice* would make the fixture claim
`watch --json` wrote something it did not. It writes exactly one file: the
fixture `--out` names, and only after the text has gone back through the real
`loadFixture`. **The write is safe against links** (`writeFixtureFile`): every
component from the console package down to `test/fixtures/` is `lstat`ed and
refused if it is a symbolic link, the target's parent must be that directory by
real path, a symbolic link at the target — dangling or not — is refused, and so
is anything that is not a regular file **even with `--force`**. The text goes
to a temporary file created exclusively (`wx`) in the same directory, then is
linked into place (a new fixture; fails if something appeared meanwhile) or
renamed over the regular file `--force` names.

### The properties are tests, and each one fails under a mutation

| Mutation | What fails |
|---|---|
| a title passed through untouched | "redacts run titles: the words stay, the people and the places go" |
| the path rule narrowed back to a whitelist of first segments, or its leading-context lookbehind restored | "redacts the paths a whitelist of prefixes would have missed" |
| the literal rule's lookbehind restored, so a literal glued to a word character or a slash is missed | "redacts a literal whose first character is not a word character, glued into a word" |
| the repository rule back to two-character segments, so `and/or` becomes a repository | "finds a repository behind a host, and never invents one out of prose" |
| keys left outside the rules and the audit | "refuses a key that carries identity, because a key cannot be renamed" |
| `parentKeyOf` reading the raw trail, so an array index hides the parent | "reads the class of a key through an array index" |
| a fixture in `test/fixtures/` with a planted address | "refuses a fixture whose records carry what the redactor removes" |
| a fixture committed into `test/fixtures/` with a stale digest or a planted address | "loads every file in test/fixtures/, and refuses one that does not verify" |
| `redactDocument` made non-idempotent | "redacting twice changes nothing" |
| a frame that cannot be parsed passed through | "fails closed on a frame it cannot parse" |
| the audit's throw turned into a return of the problems | "the audit accepts what redaction produces, and refuses what it does not" |
| a repository pseudonym replaced by the value itself | "uses pseudonyms that are stable within a recording, and never the value itself" |
| the pseudonym key made fixed or public again | "are keyed per recording, so a name list cannot rebuild them" (`redact-hardening.test.ts`) |
| any rule or field class the security review of `b65fff5` added, removed or narrowed back | the test named after its class in `redact-hardening.test.ts` (addresses in any script, branch and account fields, hosts and GCP names, folded literals, token families, people and paths, the loader with no literals) |
| `--gcp-project` optional again, or `belong` with a leading space accepted | "requires the GCP project, …" / "normalises a value before the out-of-bounds check" |
| an `--out` in any directory named `fixtures` | "refuses an --out outside this repository's console/test/fixtures" |
| the write following a symbolic link, overwriting a non-file, or `stat` in place of `lstat` | the four tests under "the write" |
| the line buffer unbounded | "refuses a line longer than a feed line can be, before it reaches the redactor" |
| the header's `sha256` left unchecked on load | "refuses a fixture whose hash does not verify" |
| the header's `redactor` left unchecked on load | "refuses a fixture whose redaction marker this build does not know" |
| the header left out of the redaction audit, so an edited `project` passes the digest and every check above it | "refuses a header that carries what the redactor removes" |
| a fixture edited after it was written to carry an address | "refuses a fixture whose records carry what the redactor removes" |
| the replay firing records back-to-back instead of on their stamps | "replays the recording on the recording's own clock" |
| the committed synthetic fixture hand-edited, digest and all | "is exactly what the redaction path writes from the documents in source" |
| a stray file in `test/fixtures/` — a hand-made copy, a recording under a name no case holds | "holds exactly the synthetic fixture plus, at most, one file per recorded case" |
| a `--project` defaulted to the ambient project, `belong` accepted, an `--out` outside `fixtures/`, or a `--case` outside `FIXTURE_CASES` | "refuses an invocation that would record the wrong thing" (one table, one row per refusal) |
| an existing fixture overwritten without `--force` | "never overwrites a fixture without --force" |
| the spawn built from a hand-written argv instead of `planFor` | "is the console's live.watch plan, --project and all" |
| the child's environment inherited instead of built | "gives the child CHILD_ENV_KEYS and nothing else" |
| the redaction refusal swallowed, so the recording continues | "refuses rather than records when the redactor will not vouch for a frame" |
| the fixture written before it is verified | "verifies the whole fixture before a byte is written" |
| the child's stderr left undrained | "drains the child's stderr, and never records a byte of it" |
| `SIGKILL` never sent to a child that will not go | "stops the child at the end of the duration: SIGTERM, then SIGKILL" |
| the child left running after a refusal | "reports a refusal, and the child is stopped rather than left running" |
| an empty recording written as a quiet-looking fixture | "refuses an empty recording rather than writing a quiet-looking fixture" |

The red step was run as the plan writes it, on this tree: the title-passthrough
mutation and the planted-address fixture both failed the tests named above, and
the two workflow mutations the plan says to run first (a `console` job renamed,
and one carrying `paths: ['console/**']`) were run with them — see the
`docs/pending-workflows/console-tests.yml` section below for those two.

Seven more were run after a review of this task, one per row above that names a
mutation of the redactor or the recorder's arguments: the path rule restored to
its whitelist and lookbehind, the literal rule's lookbehind restored, the
repository rule's segments shortened to two, `auditKey` removed, `parentKeyOf`
left reading the raw trail, `--case` accepted unchecked, and a `test/fixtures/`
file written with a stale digest. Each failed exactly the test named beside it
and nothing else, which is what makes the row a promise rather than a note. One
mutation is *not* in the table because nothing failed it: dropping the
repository rule's dotted host prefix. The widened path class reaches
`github.com/acme/secret-repo` on its own, so that half of the rule is the second
of two rather than the only one, and `src/source/redact.ts` says so where the
rule is written.

### What is deliberately not here (Task 1.4)

- **The recordings.** Eight cases, owner-run, listed above and in
  `FIXTURE_CASES`; one synthetic fixture stands in until they land.
- **Any screen.** Nothing renders a fixture yet: `FixtureSource` is a
  `FugaroSource` and no screen exists to consume one until Task 2.1 (Task 2.0
  builds the text path and the document shapes those screens must go through).
- **The wiring.** Nothing chooses a fixture, and nothing switches a running
  console between the live source and a replay — that belongs with the Live
  screen's task, beside the `handlersFor` wiring Task 1.3 left open.

### Deviations from the plan's file list, said out loud

Three additions and one move, each with its reason:

- **`src/source/redact.ts`, not `tools/record-fixture/redact.ts`.** The plan puts
  the recorder in `tools/`, and the recorder is the *writer* of a fixture — but
  the rules are also the *reader*'s, because `loadFixture` audits every document
  it loads with exactly these classes, and a fixture is only as safe as the rules
  that made it. `src/source/fixture.ts` therefore has to import the module, and
  `tsconfig.build.json` has `rootDir: "src"`: a file under `tools/` cannot be in
  the app's build. The redactor lives next to the loader that depends on it, and
  the recorder imports it from there. The plan's split is otherwise exactly as
  written: recorder in `tools/`, format and loader in `src/`.
- **`tsconfig.record-fixture.json`, `.gitignore`'s `.record-fixture/`, and
  package.json's `record-fixture` script.** The plan's owner step is
  `npm run record-fixture -- --out test/fixtures/<name>.ndjson`, and a command
  that runs a TypeScript tool needs a build and a script to be that command.
  The config is Node16 with `rootDir: "."` over `src/**` and `tools/**`, which
  is what makes the tool's imports of `src/source/*` resolve as written at
  runtime. `test/unit/pinning.test.ts` asserts the `test:unit` and `test:e2e`
  scripts exactly and allows other scripts to exist, so this adds no exception
  to a pin.
- **`test/unit/synthetic.ts`** — the fixture suite's own module rather than a
  helper inside `fixture.test.ts`, because it is also what the byte-identity
  assertion of the committed file regenerates from.
- **`test/unit/recorder.test.ts`** — the plan's file list names the redactor's
  and the fixture's suites but not the recorder's. A recorder whose *refusals*
  are untested is a recorder that can be talked into recording the wrong
  project, and the refusals are most of what the file does before it spawns;
  the suite holds every one of them, plus the spawn, the redaction of one line,
  the header, a recording in progress and the whole tool end to end, against
  stubs and never a real child.

## Availability, sanitization and the text path (Task 2.0)

Four modules, and the rule is the same rule four times: **a number the document
does not carry has no number to draw, and a string a job wrote is text.** The
desktop console's worst failure is not a crash — it is a screen that looks fine
and says something the CLI never said: a subscription's spend drawn as billed
money, a `0` for a cap nobody set, a hostile title turned into markup, a
degraded document rendered as an empty feed. Each of those is now a type or a
door rather than a convention a screen is asked to remember.

The plan's red steps for this task are both here and both are real: removing the
`textContent` writer (or planting an assignment to a markup-parsing property in
`src/renderer/`) fails `sanitize.test.ts`, and making a missing number render as
`0` fails `availability.test.ts`.

### The five states, in one type every screen goes through

`src/shared/availability.ts` is the contract's five states as a discriminated
union — **measured**, **notional**, **estimated**, **unavailable**, **gap** —
with this console's own spellings for each (`~$`, `est.`, `n/a`, an em
dash for a gap, `≥` for a floor) and one `formatReading` that is the only place a
`Reading` becomes a string. A reading that is not measured carries a *sentence*
saying why instead of a value, and the constructors refuse to build one without
it, so `{ state: 'unavailable', value: 0 }` is not expressible.

Two consequences worth naming, because both are bugs the tests were written to
catch:

- **`readNumber` is the one door.** The contract carries most numbers as
  `*float64` (`internal/watch/json.go:54`), which makes `null` a normal value —
  "no cap set", "no spend recorded" — and `0` a measured zero. One `JSON.parse`
  apart, and a formatter that read `.value` without reading `.state` would draw
  the wrong one.
- **The sign is decided after rounding** (commit `670314f`). `-0.001` used to
  draw as `-$0.00`, which is a claim that a repository spent below nothing. The
  sign now comes from the rounded text a person actually reads, so a negative
  that rounds away is drawn as zero and one that survives keeps its sign.

### Eleven classes, deny-by-default

`src/shared/sanitize.ts` is the display layer's **copy** of the recorder's rules
(design §5.1 rule 7: the sanitization is re-applied here rather than trusted to
have survived the subprocess boundary). It is a copy rather than an import
because `src/source/redact.ts` reaches for `node:crypto` and the renderer has no
Node; `sanitize.test.ts` holds the two vocabularies **equal by test** — the
markers, the placeholders and the bounds by importing both, and the keep-or-drop
decision over **free text** (the `sentence` class) by running both modules over
the same inputs, one-directionally: this layer may change more than the recorder,
never less — so drift on either side fails a test rather than a screen. The
validating classes are not held to that equality, because they decide
differently on purpose: the recorder replaces **every** `url` field with
`[redacted-url]`, while the `url` class keeps a clean `https` link (and refuses
one whose host, path, query or fragment carries anything the credential rules
match, whose host has a random-looking label or an `xn--` label that is not
valid punycode, or that is one of the well-known webhook URLs whose secret is a
path segment, matched after the path's slash runs, dot segments and percent
escapes are resolved and with any subdomain or trailing dots on the host);
the recorder writes a keyed pseudonym where this layer, with no key, accepts the
recorder's pseudonym or validates the value's shape.

The host rule has a trade-off, said out loud: a host label with an unbroken run
of 20 or more letters and digits that mixes both at 3.5 bits per character or
more reads as a key, not a name, so a legitimate host built that way — a Netlify
deploy preview's hash-prefixed subdomain, for one — is refused and drawn as
`n/a` rather than as a link. A run broken by hyphens (`us-east-1`,
`edgeweb-abc123xyz-ue`) is read piece by piece and kept.

Every value is held to 64 KiB twice — as it arrives, and again after NFKC (which
can grow a string eighteen-fold) — and a value past either bound is refused, not
truncated.

| class | admits | on anything else |
| --- | --- | --- |
| `sentence` | free text a job wrote, one line, invisible characters stripped, credential-shaped runs neutralised, every URL replaced | clipped at 512 characters, never refused (an empty title is a title) |
| `label` | a short rendered field (`stage`, `round`, `verify`, `models`, `recipe`, `workflow`) | refused past 200 characters, or if it is not a single line |
| `repository` | `owner/name`, at most 200 characters, no `..` | refused |
| `slug` | the console's own slug grammar | refused |
| `branch` | a git ref, no leading/trailing slash, no `.lock` | refused |
| `project` | a GCP project name | refused |
| `person` | a login or a pseudonym | an email address becomes `redacted@example.invalid`; anything else refused |
| `runId` | the run-id grammar, or a marker | refused |
| `host` | a machine name: every value, hostname, IP address or anything else, is drawn as `<host>` (a recorder marker is kept) | — |
| `path` | a filesystem path | redacted to `<path>` |
| `url` | `https`, a non-empty host, no whitespace, no credential in the host, a path segment, the query or the fragment (each read as written and percent-decoded: the token families, a credential-named assignment such as `access_token=…`, and — in the host, the query and the fragment — the high-entropy rule, which there also reads a run that follows a dot), no random-looking host label (the host is read as written and as parsed, whole and label by label, each `xn--` label also punycode-decoded; a run of 20+ letters and digits with both, at 3.5+ bits per character, is refused even case-folded), and not a well-known webhook URL (Slack `hooks.slack.com/services/…`, Discord `…/api/webhooks/…`, Microsoft Teams, Zapier, IFTTT, Telegram); userinfo is dropped | refused — including `http:`, `javascript:` and a URL carrying a credential |

**One key is drawn in a class other than the one `WIRE_TEXT_CLASSES` gives it,
and it is deliberate.** `launched_by` — who started a run — is classified
`person`, and the Live and Projects rows draw its **short form** (the CLI's own
cut, `runview.LauncherShort`: the part before an address's first `@`, the whole
value for anything else — the cut `fugaro runs ls`'s own `BY` column makes)
through the **`label`** class. `person` is a validating class: it draws a login
or a pseudonym and the console's marker for an address, and refuses everything
else, so `dana.lopez@example.com` — the commonest corporate local part — and
every display name would be a refused cell reading `n/a`, on nearly every row of
a column whose whole purpose is to say who started the run. The short form is
**not an address**: the cut is at the first `@`, and what the `label` class does
here is what a cell needs — one line, the 200-character bound, the invisible
characters stripped, a credential shape masked. The **whole** recorded value is
drawn in the Run screen's header, through `person` as the map says, so an
address is the console's marker there and **no address is drawn anywhere**.
`src/shared/launcher.ts` is the module and the reasoning;
`docs/design/desktop-console.md` §6.2 records the decision. This is not a
narrowing of **D15**: that decision is about `report --by person`'s cost
breakdown on the Spend screen, and the console already names people on Live and
Projects without a switch — a killed repository's row reads `KILLED by <who>`.

**The trade-off in the path, said out loud.** A path segment is not held to the
high-entropy rule, because a commit link's 40-hex digest is a path segment and
refusing it would refuse the forge's own URLs. So a secret that lives in a URL's
path is caught only by a token family or by the webhook table: a service that is
not on the table and keeps a shapeless secret in its path is drawn as it came.
The table is the well-known shapes, matched on the parsed host (case-folded,
trailing dot dropped) and the percent-decoded path, and a listed host's other
pages (`discord.com/channels/…`) are kept.

Every refusal is a **sentence about the class** ("it is not https") that names
the field and never the value: a refusal that echoed its input would put the
input on screen through the back door, which is the same failure it exists to
prevent.

### The entropy guard is copied with its guard, so a model name stays a model name

The recorder's credential net ends with a high-entropy rule over runs of 20 or
more token characters, and that rule has a guard (`isHighEntropy`): a run is a
token only if it is a 32+ character hexadecimal run with both letters and
digits, or if it mixes upper case, lower case **and** digits at 3.5 bits of
entropy per character or more. A model name, a run id, a slug and a workflow
name mix at most two of those three kinds, so the guard keeps them. Without it,
`claude-sonnet-4-5-20250929` is a 26-character run of the net's alphabet and
every run's model pair would be drawn as `[redacted-token]` —
a redaction where there is nothing to redact, which is worse than a leak of a
model name. The console's copy carries the guard, and the fixture's model pair is
the standing witness: it is in the committed recording, and it survives decoding
(`test/fixtures/synthetic.ndjson`).

### The text path: three writers, and no fourth way

`src/renderer/app.ts` is the only code in the console that touches an element,
and it is deliberately narrow (`ElementLike`: a text slot and attributes — no
markup slot, no style, no event handler):

- **`putText`** writes a string through `textContent` and nothing else. A refusal
  is drawn as `n/a` with the console's sentence in the `title` attribute.
- **`putReading`** writes a number through `formatReading`, so a screen cannot
  draw a bare `0` where the document said "not known".
- **`putLink`** takes its destination from `safeUrl` and puts it in `data-href`
  — **never** in the navigating attribute. The renderer therefore cannot
  navigate even if a screen is written badly later: an element carrying
  `data-href` is inert until the main process, behind Task 1.2's allow-list,
  decides the URL may be opened at all. A redaction marker offered as a link is
  text with a title instead, because "the CLI redacted this URL" is a different
  fact from "there is no URL".

`sanitize.test.ts` drives the plan's hostile-title corpus through all three
writers into a **fake element whose every markup-parsing property and every
navigational or event attribute throws**, so "the text path cannot become markup"
is a fact a test holds rather than a property a reviewer reads for. The fake also
records every mutation, so a test can assert that the *only* write that happened
was a text write. The same file scans `src/renderer/` for the markup-parsing
properties (and for `toFixed`, `toLocaleString` and `Intl.NumberFormat`, which
are how a number escapes the availability model), and checks that the scan sees a
planted one — a lint that cannot fail is not a lint.

### Two document shapes, and neither is a guess

`src/shared/schema.ts` is the door every document comes through. It admits
exactly the two shapes the CLI writes and nothing else:

- the live feed (`internal/watch/json.go`), and
- `watchDegradedDoc` — `lsDoc` **embedded** plus `degraded: true` and a `notice`
  (`internal/cli/watch.go:401`), for a project with no budget backend.

The discrimination is the `degraded` key itself, not a guess about which fields
happen to be present: a document with `degraded` must carry `true` (no version of
`watch` writes anything else), a document carrying `degraded: true` *and* the
live feed's identifying keys is refused as a contradiction, and a document with
neither shape's keys is the **skew state Task 3.2 renders** — never an empty
screen, which is the failure that looks like a working console.

Three rules a reader would not guess, all taken from the wire:

- **The identifying keys are the eight the degraded shape does not have.**
  `project` and `runs` are in *both* (`lsDoc` carries them), so a marker list
  including them refuses a real degraded document as a contradiction. This is
  exactly the bug the first draft of the test caught.
- **Numbers are the tolerant axis and keys are not.** An absent or `null` number
  is an unavailable reading with a sentence; an absent *string* key a screen
  reads is a refusal by name (`runs[0].round`, `missing`).
- **An unknown key is ignored; a missing or retyped one is a refusal** (the
  owner's ruling of 2026-10-30, which replaced "an unknown key is a refusal").
  The CLI's compatibility rule is that its JSON only grows: a field is added,
  never re-meant — a field whose meaning changes gets a new name — and removing
  or retyping one is a format change. So a key a newer CLI added is skipped, and
  the value is built from the keys the console names, so the key reaches no
  screen; a field the console reads that is absent or of the wrong type is still
  refused by name, on the screen whose read it is.

The tests hold the plan's claims: reading a degraded document as a live one,
reading a missing number as `0`, carrying an unknown key into the value, and
accepting a missing or retyped field each fail a named test.

### Version skew: an older console reads a newer CLI

`src/shared/version.ts` places the `fugaro version` line. A member of
`SUPPORTED_CLI_MINORS` (`0.7`, `0.8`, `0.11`, `0.12`, `0.13`: the minors whose
documents this console has read) **or any plain release above the newest
member** answers `ok`, and the console draws everything it can read — it is the
older of the two, and says so in its own words if asked. The reduced mode (a
banner, and the spend screen, the caps and the kill/resume controls withheld)
is for a CLI older than the newest member and above the floor (`0.9`, `0.10`,
`0.6`), for a line that is not a plain `major.minor.patch` release number (a
prerelease or build tail, a leading zero, a missing patch), and for a `dev`
build; below `CLI_HARD_FLOOR_VERSION` the console refuses. A read that fails to
decode against a newer CLI — a field removed or retyped — is refused on its own
screen with the decoder's sentence. `version.test.ts` reads the CLI's
`current` line from `docs/release-streams.yaml` and fails when the latest
released CLI does not answer `ok` as a member, which is the release
checklist's reminder to re-read that minor and add it.

### The corpus was corrected against the wire, not against itself

`synthetic.ts` and the committed recording now say what the Go source writes,
which is not what the previous draft of the fixture said:

- **`round` is a string**, not an int: `strconv.Itoa(e.Round)`, and `"-"` for a
  run whose round is not known (`internal/watch/build.go:403`). A console that
  typed it as a number would pass every fixture until a queued run arrived.
  `schema.test.ts` has that near-miss as its own red step.
- **`kill` is `null` when the switch is off**, not absent (`json.go:134`: the
  field has no `omitempty`).
- **The `omitempty` keys are absent, not empty** (`recipe`, `halted`, `queued`,
  `stuck`, `workflow`, `requested_by`, `queued_note`), and `queued_note` is
  therefore the one live key the corpus does not carry — asserted, so that a key
  added to the shape and never carried by a document fails the walk.
- **A queued row carries a dash in each of the five fields it has no value for**
  (`round`, `verify`, `models`, `auth`, `internal/watch/queued.go:40`), with
  `spent_usd: null`.

The fixture is regenerated from `synthetic`, and `fixture.test.ts` asserts the
committed bytes are exactly what the module produces, so an edit to the corpus
that is not committed (or the reverse) fails.

### The fake secrets, and why every one is built at run time

`test/support/fakeSecrets.ts` is the one addition to the plan's file list for
this task, and it exists for the reason below. It assembles every secret-shaped
test input by concatenation — `gh` + `p_` + 36 characters, a PEM header from pieces and a body
of repeated `A` — and no line of this task's files carries a shape a scanner
matches. A fake token that is a *literal* is indistinguishable from a leaked one
to GitHub's push protection and to a reader, and the console's own tests would be
the last place to find out.

The rule is now a test rather than a habit: `test/unit/secret-hygiene.test.ts`
walks `src/`, `test/`, `tools/`, `docs/`, `README.md` and `package.json` and
fails on the first line matching a credential shape — the forges' prefixes, the
vendors' keys, a JWT's first segment, a PEM header — with the patterns themselves
assembled from fragments and the scan checked against a planted line, so a scan
that cannot fail is not mistaken for one that passes. Turning the rule on it
found and removed twenty literal lines left by Task 1.4's own suites, across
five files, one of them a 36-character body that is exactly what a leaked GitHub
token looks like -- and every test that had been running against them still runs
against the same strings, assembled at run time. And
`TOKEN_RES`'s first pattern in `src/source/redact.ts` is now built from
fragments, character-for-character the expression it was.

### What is deliberately not here (Task 2.0)

- **Any screen, and the wiring to the bridge.** `app.ts` has the writers and
  nothing else: no screen, no entry point, no decision about where the compiled
  script is served from (`tsc` emits `dist/`, `src/main/index.ts` serves
  `src/renderer`, and one of the two has to move). Those are Task 2.1's.
- **The skew banner itself.** `schema.ts` produces the state Task 3.2 draws; this
  task does not draw it.
- **The loader's share of the fixture decision.** `loadFixture` already refuses a
  fixture whose header, redaction marker or hash does not verify (Task 1.4); this
  task reads none of that and adds no second format.

### The deep-nesting failure, and how it was closed

`test/unit/redact-persons.test.ts` — "refuses a document nested 10000 deep as a
`RedactionRefusal`, and the loader as a `FixtureRefusal`" — used to fail with
`RangeError: Maximum call stack size exceeded` from `JSON.stringify` in
`src/source/fixture.ts`. It was inherited: it failed identically at `43c6dd7`,
this branch's head before Task 2.0 began. It is **now green**, and the fix is in
the writer rather than in the test: `writeFixture` serialises with
`stringifyDeep`, which walks the document with an explicit stack instead of the
engine's recursion, so a document deep enough to exhaust the stack is
serialised and the loader refuses it as the `FixtureRefusal` the suite expects.
`fixture.test.ts` holds the new writer to `JSON.stringify`'s bytes on a table of
awkward cases — empty containers, `undefined` in objects and in arrays,
non-finite numbers, escaping, `toJSON` nested and returning `undefined` — and
carries the 10 000-deep document as its own case.

## The installation store, and the picker window (Task 3.4b)

The console is pointed at an **installation** — a project name, the path to a
`fugaro` binary, the path to that installation's config, and optionally the
checkout directory (design D13). This task is where the list of them is kept and
where the operator picks one.

### The store: one bounded, schema-checked, `0600` file

`src/main/installations.ts` owns it. It is the second of the **two files the app
writes** (design §5.1 rule 10, §7.4), and the owner ruled it in on 2026-10-18
(**C8**) rather than letting it grow by accident:

- **`installations.json`, in the app's own `userData`**. Electron names that
  directory after `package.json`'s `productName`, else its `name`; this
  package sets no top-level `productName`, so a development run (`electron .`,
  the e2e specs) uses `fugaro-console`, and a packaged build may use
  electron-builder's product name, `Fugaro Console`. Neither is the CLI's
  `fugaro`, and `test/unit/installations-store.test.ts` reads both names from
  `package.json` and checks them. It is written `0600` inside a `0700`
  directory.
- **Tuples only** — `id`, `project`, `binaryPath`, an optional `configPath` and
  an optional `checkout`. **Never a credential**, and **never read from the
  operator's CLI config**: rule 7 forbids that outright, and the store's only
  touch of a config path is a `stat` (see `test/unit/configread.test.ts`).
- **It refuses to live under the CLI's own configuration.** `installationStorePath`
  throws if `userData` is inside `~/.config/fugaro`, `$XDG_CONFIG_HOME/fugaro`
  or the directory `$FUGARO_CONFIG` names, so the console's file and the CLI's
  cannot be confused for one another. The picker catches that refusal: the
  console still starts, with **no store**, and the picker and the dashboard say
  why (a `$FUGARO_CONFIG` of `~/fugaro.yaml` inherited from a shell is the case
  that reaches it, since every `userData` is under the home directory). `cliConfigDirectories` takes `home` as an
  argument rather than calling `homedir()`, because the console's own guard
  forbids that call outside binary resolution.
- **Schema-checked on read, and closed.** `readInstallationFile` refuses a file
  whose top-level keys are not exactly `version`, `active`, `installations`, an
  entry with a key outside `INSTALLATION_KEYS`, a duplicate id, an id that is
  not 16 hex characters, a project name that is not one, a path that is not
  absolute or is too long — **an unknown key refuses the whole file** rather
  than being ignored, because a file the console only partly understands is a
  file it must not act on. A missing file is an empty list, not a failure.
- **A regular file, or nothing.** The read `lstat`s the name and refuses a
  symbolic link, a FIFO or anything else that is not a regular file, then opens
  it `O_NOFOLLOW | O_NONBLOCK`, re-checks it with `fstat`, and reads at most one
  byte past the bound from that descriptor — so a FIFO cannot hang startup and
  a file that grows between the checks is still refused.
- **Bounded.** `MAX_INSTALLATIONS` (16) and `MAX_INSTALLATION_FILE_BYTES`
  (64 KiB, refused unread past it). The bound is re-checked **after** the
  `await` in `add`, because validation spawns a child process and two adds can
  be in flight at once — the pre-await check alone let a 17th through.
- **Written atomically.** A temporary file in the same directory, then a
  `rename` over the old one, so a crash mid-write leaves the previous list
  intact rather than a truncated one. The test that proves it injects an `fs`
  whose `rename` throws `ENOSPC` and asserts the previous bytes are untouched.
- **Nothing is written on the refusing path, and nothing on a read.** The
  `recordingFs()` in `test/unit/installations-store.test.ts` asserts **no write
  at all** until a `choose`, a `checkout` or a `remove` — a load, a `list` and a
  `current` write nothing. (The `add` this sentence named until 2026-10-20 is
  gone with the form; the property is the same one, over the three mutations that
  are left.)

The file is Electron-free, and the store's filesystem is injected as
`InstallationFs`, so every property above is a plain-Node unit test.

### Validation: the binary is proved, not trusted

`validateInstallation` refuses anything that is not an **absolute** path to a
file that **exists**, is a **file** and is **executable**, and then runs that
installation's own `version` handshake through `probeInstallation` — so a
candidate is stored with the `CliCompatibility` the console actually read from
*that* binary, not from whichever `fugaro` happens to be on `PATH` (rule 4). A
binary below the hard floor is refused and **nothing is stored**; a binary in
the reduced range is stored and its row says `reduced`. The CLI's own pairing
check (`doctor --json`) then runs on every **checkout** as one allow-listed
spawn — the one thing that makes a checkout a mutation, since 2026-10-20 and the
form's removal; it used to run on every add — and a checkout that belongs to
another project is refused **with the CLI's sentence shown** and not stored — the
console never reads `fugaro.yaml` itself (rule 7).
Every probe of an add or a choose runs from the checkout when one is given and
from the console's private directory otherwise — never from the binary's own
directory — and is in the spawn log, with the candidate's project, when the
operator has turned the log on.

`isProjectName` is deliberately **stricter** than the spawn layer's
`isSafeInstallationValue`: the latter allows a space, because a path may contain
one, while a project name may not.

### D13's semantics, which the store implements

`--project` is passed on **every allow-listed command except `version`** — and
`--config` follows it whenever the installation declares one and is not the
ambient installation. The `version` handshake stays bare (`COMMANDS_WITHOUT_PROJECT`),
because `newVersionCmd` is `cobra.NoArgs` with no flags, so `--project` on it
would be refused by the CLI rather than ignored.

### The picker: a separate window, its own document, its own preload (C9)

The owner fixed the shape on 2026-10-18 (**C9**): the installation picker is
**its own small window, opened from the app menu** — not a control in the
dashboard's header. `src/main/picker.ts` builds it:

- `installations.html` is a **second document on the same origin**
  (`app://console`), served by the same protocol handler, with the same
  byte-identical CSP and the same hardened `webPreferences`. It needs no
  protocol change: the bundle handler already serves any path under the root.
- **Its own preload**, `src/preload/installations.ts`, exposing exactly the
  eight rows of `INSTALLATION_OPERATIONS` under `fugaroInstallations`. It
  imports nothing from `src/main/` — the mirror is written out a second time,
  the same way the dashboard's preload is, and `test/unit/installations-ipc.test.ts`
  asserts the two lists are equal.
- **The channels are `fugaro:installations/…`, and the sender gate is
  per-document.** The two documents share an origin, so an origin check alone
  would let the dashboard's frame call the picker's channels and vice versa.
  `isSenderOnDocument(event, entryUrl)` adds the document path to the check, and
  the test drives it **both ways**: every picker channel refuses the dashboard's
  frame, every dashboard channel refuses the picker's.
- **The renderer never names a path.** `add` takes its three paths as **pick
  tokens** — single-use, kind-bound capabilities the main process issues for a
  path *it* chose in a native dialog. A literal path is refused by shape
  (`binary is not a path this console offered`) before any redemption, and a
  replayed token is refused (`binary is not an outstanding choice`). At most
  `MAX_OUTSTANDING_PICKS` (8) are outstanding, the oldest is dropped past the
  bound, and every token is dropped when the window closes.
- **The menu is the only way in.** `installationsMenuTemplate` adds
  `Installations…` (and `Quit`), beside Electron's own Edit and Window menus so
  the editing and window shortcuts keep working; the window is opened by that
  item and by the first run (`openOnFirstRun`, which opens only when no
  installation has been chosen).
- **One mutation at a time.** A choose, a checkout or a remove runs only after
  the one before it has finished — for a choose, after the window it asked for is
  up — and the picker's controls are disabled while a request is in flight, so
  two quick chooses cannot leave two dashboards open.

**Amended 2026-10-20, by the owner's ruling.** The bullets above are the record of
Task 3.4b and three of them describe a window that no longer exists: the surface is
**five** rows now (`list`, `choose`, `checkout`, `remove`, `openExternal`), not
eight; there is **no `add`, no `pickBinary`, no `pickConfig` and no `pickCheckout`**,
so `createPickTokens`, `MAX_OUTSTANDING_PICKS` and the one `dialog.showOpenDialog`
call are gone with them, and the picker window is where `dialog` is no longer a dep;
and a **path is not a subject** of any row any more — a row is keyed by a project
name, plus a directory out of that installation's own `checkouts`. The resolution and
the version handshake are still spawns, but they run on a **choose**
(`probeInstallation`, which runs no `doctor`) and the pairing check runs when a
**checkout** is set (`validateInstallation`, which is where rule 7's `doctor --json`
is spawned) rather than on an add, which is the next section's own record.
The full account of what replaced the form is in "The first run asks for nothing"
below.

`src/main/index.ts` wires it in: `installInstallationPicker(...)` with Electron's
own `BrowserWindow`, `session.defaultSession`, `ipcMain`, `shell`, `Menu` and
`app`, then `installMenu()`, then `openOnFirstRun()` after the handshake has
settled. The picker keeps the registry, and `picker.installation()` is the
seam the renderer entry (Task 3.4a) needs to answer the dashboard's
`installation.current` row.

### What is deliberately not here

- **The dashboard's join, and the choose.** `src/source/source.ts` answers
  `installation.current` from an **optional** `installation` dep, defaulting to
  D13's "no installation has been chosen" state; since the merge with Task
  3.4a, `src/main/index.ts` passes an `installation` reader in — **amended
  2026-10-20**: `installationState`, not `picker.installation`, because the
  header draws the binary *this run resolved* (`withResolvedBinary`, unit-tested
  in `test/unit/installations-store.test.ts`; `test/unit/startup.test.ts` pins
  the wiring). A choose runs the
  handshake for that installation in the picker and hands the answer to
  `lifecycle.choose` (`src/main/window.ts`), which registers the surface if it
  was not yet and reopens the window through `openWindowFor`. The startup
  handshake reads the stored active installation — **amended 2026-10-20**: rule
  4 with that record's `binaryPath` as its first *candidate*, so a stored
  override that is missing or not executable falls through to the well-known
  locations rather than refusing, and the record of a console-written entry is
  an id and a project with no path at all — with its project,
  and its answer is what `installation.current` and the picker's rows report
  (an installation nothing has read in this run says `unknown`, never `ok`).
  The dashboard draws one line from that read and no figure: the project, the
  binary, the version and mode, or the no-installation sentence and, when the
  store's file was refused, its path and why. Binding
  a `CliSource` to it is Task 3.4c's: until then the dashboard's reads answer
  `NO_ACTIVE_INSTALLATION`.
- **`build.files`.** The packaged app must carry everything it loads at runtime.
  Task 4.1 (#540) wrote the list and Task 3.4a moved the served root to the
  bundler's output; this task adds its two files to it. `build.files` carries
  the documents the handler serves, as the bundler's copies of
  `src/renderer/index.html`, `src/renderer/refusal.html`,
  `src/renderer/installations.html` and `src/renderer/confirm.html` in
  `dist/bundle/`; the three bundles,
  `dist/bundle/console.js`, `dist/bundle/installations.js` and
  `dist/bundle/confirm.js`; **the served
  stylesheet, `dist/bundle/console.css`** (Task 6.2 — every document links it,
  so an app packaged without it opens a window with no style at all); the three
  preloads a window loads by absolute path, `dist/preload/index.js`,
  `dist/preload/installations.js` and `dist/preload/confirm.js` (the launch
  confirmation window's, V2.5.1a part 2b-1); and the compiled main process,
  shared and
  source modules under `dist/**` except the two Task 4.1 excludes by name.
  `test/unit/build-files.test.ts` reads that list off the tree rather than
  repeating it, so the next document or the next preload is covered when it is
  added, and a file that works here but is missing from the shipped bundle
  fails a named test instead of shipping — the stylesheet is the one
  `bundlerOutputs` names by hand, and that is why a `build.files` that dropped
  it fails by name.
- **The picker's bundle entry** is the bundler's second entry
  (`pickerBundlePaths` in `tools/bundle-renderer/index.ts`) and the launch
  confirmation window's the third (`confirmBundlePaths`, V2.5.1a part 2b-2a):
  `npm run build`
  writes `dist/bundle/installations.js` and `dist/bundle/confirm.js` beside
  `console.js`, each bundle step
  told the others' names so the served root still refuses any file it did not
  write, and `SERVED_FILES` names all four documents, the three window scripts,
  the worker and the stylesheet.
- **A picker e2e spec.** The picker's coverage is
  `test/unit/installations-store.test.ts`, `test/unit/installations-ipc.test.ts`
  and `test/unit/installations-view.test.ts` (the last drives the view over a
  fake document built from the ids `installations.html` declares, because the
  console has no jsdom dependency);
  a Playwright spec that drives the second window is a follow-up, because a
  window-opening spec cannot be checked by hand in a worker with no display.

## `docs/pending-workflows/console-tests.yml`

The console's own CI is **staged, not applied**, and Task 1.4 is the task that
stages it — see `docs/pending-workflows/README.md` for the file itself and the
one command that applies it. Two things about it belong here, because they are
about this directory rather than about the workflow:

- **The file is `console-tests.yml`, tests only, and it holds no secret of any
  kind.** The signing, notarising and stapling workflow is Task 4.2's
  **separate** `console-release.yml`, precisely so that a file which runs on
  every pull request from any branch never sits next to the Apple material
  (design §9.4).
- **The job runs unconditionally, and that is what makes it gate-able.** It has
  no `paths:` filter and no job-level `if:`, and its key is `console`. The two
  check runs it reports are `console (ubuntu-latest)` and
  `console (macos-latest)` — GitHub appends a matrix job's values to its
  check-run name, so there is no check run named exactly `console` — and **Task
  4.6 adds those two names to `scripts/release-gate.sh`'s `names` array**. From
  that edit on, the gate requires both on a tag's commit and, in its
  `identical_tree_checks` fallback, the same names on the
  pull request head whose tree matches; **a check that does not exist is not a
  success and neither is a skipped one**, so a `console` job filtered to
  `console/**` would then make the gate refuse **every** release tag in this
  repository, including the ones that never touch the console (design §9.4;
  plan Task 4.6). Two tests in
  `internal/cli/docs_desktop_console_test.go` —
  `TestConsoleTestsWorkflowIsStagedAndPinned` and `TestConsoleWorkflowJobIsGateable`
  — assert the pins, the absence of any secret, the job's name and the absence
  of a filter or a guard, and byte-identity with the copy under
  `.github/workflows/` once the owner has applied it.

**Tasks 1.0 to 1.4 merged with no console CI at all.** The copy-in is Task 1.5,
and it has now happened (the line below names the commit): the pinned
toolchain, the hardened window, the IPC allow-list, `FugaroSource` and the
fixtures were written, reviewed and merged on the strength of a reviewer's
machine, and no workflow ran a console test on any of their pull requests.

Tests workflow applied at commit 8a17d941 (`Add console workflow`, on the `console` branch): the owner copied `docs/pending-workflows/console-tests.yml` to `.github/workflows/console-tests.yml` unchanged. Tasks 1.0 to 1.4 merged before that commit and had no console CI; commits after it are covered by the console's checks (`console (ubuntu-latest)` and `console (macos-latest)`).

## The macOS bundle, its entitlements and its fuses (Task 4.1)

The console is packaged by **electron-builder**, configured entirely in
`package.json`'s `build` key, and `src/main/fuses.ts` is the `afterPack` hook
that flips the Electron fuses into the binary before the packer signs it.
`test/unit/entitlements.test.ts` and `test/unit/fuses.test.ts` assert every part
of that: the entitlement set exactly, each fuse by name, what the hook passes
and what it verifies, and the rest of the build configuration.

### The minimum entitlements, and why each withheld key is withheld

`entitlements.mac.plist` grants **one** entitlement:

| Key | Why |
|---|---|
| `com.apple.security.cs.allow-jit` | The hardened runtime denies memory that is both writable and executable, and V8 needs it for its JIT — without it Electron aborts at startup on arm64. The JIT is V8's, inside the framework this app is built on; the console has none of its own. |

Everything else is deliberately absent, and the plist says so in its own comment
rather than leaving a reader to infer it from a short file — the argument for
each, in one line:

- **`com.apple.security.cs.disable-library-validation`** and
  **`com.apple.security.cs.allow-unsigned-executable-memory`** are the pair every
  Electron tutorial adds "just in case", for plug-ins that load their own
  libraries and bring their own JIT. The console loads **no plug-in and no native
  module of its own**: its runtime surface is Node's own modules, the Electron
  framework, and the processes of the CLI it spawns. This is the entitlement the
  design's §12 item 4 leaves open, and the answer here is "not needed, and here
  is the reason" — not "probably not needed".
- **`com.apple.security.cs.allow-dyld-environment-variables`** is the macOS twin
  of the `NODE_OPTIONS` hole the fuses close: an environment variable that turns
  into code in our process.
- **`com.apple.security.cs.debugger`** and **`com.apple.security.get-task-allow`**
  let another process attach to a hardened-runtime app; `get-task-allow` is a
  development entitlement, and nothing here builds a debug variant.
- **`com.apple.security.app-sandbox`** is a Mac App Store key (§D4), and a
  sandbox key with no container and no file entitlements is a claim with nothing
  behind it.
- **The device, network and file keys** are refused by design rather than
  omitted: the renderer reaches no network and no device and every permission
  request is denied in the main process (§5.1 rule 9), and the only process that
  talks to a network or reads a checkout is the CLI, which is a separate binary
  with its own signature.

`entitlementsInherit` points at the **same file**, and that is what makes the
minimum a property of the whole bundle rather than of its outermost binary:
electron-builder's own `templates/entitlements.mac.plist` — the file it uses for
**both** `entitlements` and `entitlementsInherit` when neither is set
(`macPackager.js`, `getEntitlements`/`getEntitlementsInherit`) — grants all
three of `allow-jit`, `allow-unsigned-executable-memory` and
`disable-library-validation`. Naming this file for both is therefore the edit
that takes the plug-in pair out of the signature, not a tidiness preference; the
suite asserts both keys, so deleting one is a red test rather than a quiet
return to the template.

**Unverified, and named as such in the plist.** This repository has never
produced a signed build. The claim above is an argument from the bundle's
contents; Check 59a — owner-run, on a Mac with a Developer ID certificate — is
where it becomes a measurement, and `codesign -d --entitlements -` on the built
bundle is the command that prints what was actually granted.

### The six fuses, and the three left alone

Flipped in `afterPack` over `@electron/fuses` 2.1.3, and these are the six the
design's §5.1 rule 4 names — with the three this build does *not* touch named too,
because "left at its upstream default" is a decision and not an oversight:

| Fuse | Value | Why |
|---|---|---|
| `RunAsNode` | **disabled** | `ELECTRON_RUN_AS_NODE=1` must not turn the shipped console into a Node interpreter — for anyone who can execute the file, not just the owner. |
| `EnableNodeOptionsEnvironmentVariable` | **disabled** | `NODE_OPTIONS=--require …` is code injected through the environment. |
| `EnableNodeCliInspectArguments` | **disabled** | `--inspect` must not attach a debugger. |
| `EnableEmbeddedAsarIntegrityValidation` | **enabled** | The app loads only if its asar is the one that was packed. |
| `OnlyLoadAppFromAsar` | **enabled** | The app loads from `app.asar` and nowhere else. |
| `GrantFileProtocolExtraPrivileges` | **disabled** | `file://` keeps no extra privileges; the console serves itself over `app://` (Task 1.1). |
| `EnableCookieEncryption` | *left alone* | The console keeps no cookie store: no sign-in is stored. |
| `LoadBrowserProcessSpecificV8Snapshot` | *left alone* | A startup optimisation for a browser process that loads web content. |
| `WasmTrapHandlers` (index 8) | *left alone, enabled* | Upstream default kept: V8's WebAssembly bounds-check strategy loads no code, reads no environment variable or flag, and the console runs no WebAssembly. It was beyond the pin until `@electron/fuses` 2.1 named it; `BEYOND_PIN_FUSES` is now empty, because Electron 45.0.0's nine fuses are all named by the package. |

Three properties of the hook are worth stating here, because they are what make
the six a fact rather than an intention:

- **It verifies what it wrote.** `flipFuses` reports how many fuses it changed,
  not what the binary now holds — so a table entry carrying the index of the fuse
  above it flips the wrong byte with nothing from the packer to say so. The hook
  reads the fuse wire back and fails — naming the fuse and the binary — if a flip
  did not take. (It is *not* a guard against a path that is not an Electron
  binary: the pinned `@electron/fuses` refuses that itself, both calls rejecting
  with "Could not find sentinel in the provided Electron binary", which
  `fuses.test.ts` asserts rather than assumes.)
- **It refuses a platform this build is not packaged for** instead of skipping.
  A build on Windows would produce no bundle to flip — no `Contents/MacOS` on
  macOS, no bare executable in the pack directory on Linux — and a silently
  skipped hook is the one outcome that ships the fuses unset. So `fuseTarget`
  resolves the binary **per platform in one place**, from
  `electronPlatformName`: darwin's
  `<appOutDir>/<productName>.app/Contents/MacOS/<productName>`, and Linux's
  `<appOutDir>/<executableName>` (the name `build.linux.executableName` sets,
  or the sanitized product name lowercased when it sets none), and it
  **refuses anything that is not darwin or linux**, naming the platform and the
  two it packages. `SUPPORTED_PLATFORMS` is the one list both this hook and
  `src/main/spawn.ts`'s location table are keyed on, so "which platforms does
  this build support" has one answer. **The fuse set does not vary by
  platform**: the same six flips, byte for byte, on both executables — including
  `EnableEmbeddedAsarIntegrityValidation`, which Electron enforces on macOS and
  ignores on Linux, because a per-platform fuse table would be a second policy
  to keep in step. The one macOS-shaped word in the hook is the
  `resetAdHocDarwinSignature` option, documented below as deliberately unset
  for both platforms.
- **It refuses a wire it has not accounted for.** After the flips, the hook
  compares the wire's length with the fuses `fuses.ts` decides about (six
  flipped, three left alone, none beyond the pin: nine) and stops the build if they
  differ, so an Electron bump that adds a fuse cannot reach the signer
  unexamined. The three fuses it leaves alone are checked **by value** too: the
  cookie and snapshot fuses must still be disabled and `WasmTrapHandlers` enabled,
  the defaults the pinned Electron 45.0.0 ships, or the build stops.
  `fuses.test.ts` also reads the real binary's wire when
  `node_modules/electron/dist` is installed, and on macOS runs the real hook over
  a copy of it in a temporary directory; without the binary those two tests skip
  and say why. **CI never runs those two**: the console job's unit step runs
  after `npm ci --ignore-scripts` and before anything downloads Electron, so
  there they skip. They are owner-run verification: step 2 of the owner's
  commands below runs them by name, on the Mac, after the download.
- **It is the only flipper.** `build.electronFuses` is deliberately unset, so
  electron-builder's own fuse support cannot run a second, untested
  configuration over the same binary.

`resetAdHocDarwinSignature` is deliberately **not** set, and the reason is an
ordering fact: `@electron/fuses` requires it only when the app is not signed
immediately after the flip ("otherwise the app will refuse to launch with code
signature validation errors" — its README, Apple Silicon section), and
electron-builder signs *after* the `afterPack` hook, in the same run. The
consequence for the owner is the next section's first line.

### The owner's commands, exactly

Run from `console/` on the Mac that holds the Developer ID certificate. The
variables are **names only** — no value of any of them appears in this
repository, and `package.json` names no signing identity at all. The order is
the point: everything that installs or downloads runs **first**, in a shell with
no secret in it, and the signing material exists only in the environment of the
one command that packages — an environment built from nothing (`env -i`), so an
ambient `GH_TOKEN`, a cloud credential or anything else the owner's login shell
carries is not in it.

```sh
# 1. Install, with no secret in the environment. Lifecycle scripts stay off:
#    every package, the packer included, comes from package-lock.json (exact
#    version, integrity hash, registry host -- lockfile.test.ts).
npm ci --ignore-scripts --no-audit --no-fund

# 2. The one install step the tree needs, run explicitly: Electron's own
#    download of its distribution, verified against the checksums.json in the
#    pinned package. Packaging does not use it (see below); the tests do.
node node_modules/electron/install.js
npm run test:unit
# The two real-binary fuse tests, on their own: they need the distribution
# step 2 downloaded, and CI never runs them (its unit job runs before anything
# downloads Electron, so there they skip). The output must read
# "2 passed", with neither skipped.
npm run test:unit -- test/unit/fuses.test.ts -t 'the pinned Electron binary itself'

# 3. Package, in a subshell (so the `exit` below ends it, not your terminal),
#    with an environment built from nothing. Only files and identifiers go on
#    a command line: CSC_LINK is a FILE PATH to the exported .p12 (Developer ID
#    Application), never its base64 inline, and APPLE_API_KEY is the path of the
#    .p8. The one secret VALUE, the certificate's password, never touches argv:
#    the inner shell reads it from the terminal and exports it to the packager.
#    Notarisation activates by itself when one credential triple is present
#    (electron-builder 26 has no "turn it on" flag, only a disable switch); the
#    App Store Connect API key is the recommended one -- the alternatives are
#    APPLE_ID + APPLE_APP_SPECIFIC_PASSWORD + APPLE_TEAM_ID, or APPLE_KEYCHAIN +
#    APPLE_KEYCHAIN_PROFILE: one triple, not three.
(
  test ! -e electron-builder.env || { echo 'remove console/electron-builder.env first' >&2; exit 1; }
  env -i \
    PATH="$(dirname "$(command -v node)"):/usr/bin:/bin:/usr/sbin:/sbin" \
    HOME="$HOME" TMPDIR="${TMPDIR:-/tmp}" \
    NO_UPDATE_NOTIFIER=1 CI=1 \
    CSC_LINK="$HOME/<private dir>/developer-id-application.p12" \
    APPLE_API_KEY="$HOME/<private dir>/AuthKey_<key id>.p8" \
    APPLE_API_KEY_ID="<key id>" \
    APPLE_API_ISSUER="<issuer uuid>" \
    /bin/bash --noprofile --norc -c '
      IFS= read -rs -p "Developer ID certificate password: " CSC_KEY_PASSWORD </dev/tty
      echo >&2
      export CSC_KEY_PASSWORD
      exec npm run package   # npm run build && electron-builder --mac --universal --publish never
    '
)
```

Why each of the variables that are not secrets is there:

- `NO_UPDATE_NOTIFIER=1` and `CI=1`: electron-builder's CLI (`cli-util.js`)
  runs `simple-update-notifier`, which contacts `registry.npmjs.org` from the
  shell holding the secrets, unless `NO_UPDATE_NOTIFIER` is set or `ci-info`
  reports CI. Both are set; `CI` turns nothing on here that matters, because
  publishing is off from both sides (below).
- `PATH` is the Node that installed the tree plus the system directories
  `codesign`, `xcrun` and `hdiutil` live in, and nothing from the owner's
  profile.

**`console/electron-builder.env` must not exist.** The same `cli-util.js` loads
`electron-builder.env` from the working directory into the environment before
it builds, so a file there would add variables the `env -i` above was written to
keep out — and, if it held secrets, put them in a file in the checkout.
`fuses.test.ts` asserts the file is absent and that `build.files` could not
ship it.

**The window that remains, said plainly.** The password is in the environment
of the packaging processes (the inner shell, npm, `tsc`, electron-builder and
what they start) for the length of the build, where any process running as the
owner's user can read it (`ps eww`, for one); and electron-builder itself hands
it to `/usr/bin/security import … -P <password>` on a command line for the
moment it imports the certificate into its temporary keychain
(`codeSign/macCodeSign.js`), which the pinned packer does and this repository
cannot change. The `.p12` and the `.p8` are files whose paths, not contents,
are passed; keep them `0600` in a directory only the owner can read.

**What still runs with the secrets present**, said plainly: `npm run package`
runs `tsc` (`npm run build`) and then the whole electron-builder tree, and every
line of both can read the environment. The mitigation is the pins, not
isolation: the tree is exactly the lockfile's (exact versions, integrity
hashes, one registry host, installed with scripts off in step 1, before any
secret existed), `npmRebuild` is off so no install script runs at pack time,
the Electron archives are checked against the pinned hashes, and the toolsets
the packer fetches are checked against hashes inside the locked packages.

**The build is universal (arm64 + x64)**, by `--universal` in the script, so one
DMG and one zip run on every Mac this release supports. electron-builder packs
each architecture from the official Electron archive for that architecture,
verifies both archives against the two hashes in `build.electronDownload` (which
`fuses.test.ts` requires to equal the pinned `electron` package's own
`checksums.json`, so they move with the Electron pin and nowhere else), and
merges the two apps with `@electron/universal`. The archives are downloaded at
pack time, once, into electron-builder's cache, and a mismatched hash stops the
build (mutation-checked: a zeroed x64 hash fails with "Generated checksum …
did not match expected checksum"). The `afterPack` hook runs on each thin app
and again on the merged one, before signing. An unsigned universal pack of this
configuration was run locally (`-c.mac.identity=null`, no credentials in the
environment): it produced the artifacts below, the asar held exactly the files
`build.files` names, the merged app's fuse wire read
`0,0,0,0,1,1,0,0,1` for indices 0 to 8 (six flipped, the cookie and snapshot
fuses at Electron's defaults, `WasmTrapHandlers` on), `Info.plist` carried the
merged asar's integrity hash, and `Contents/Resources` held no `app-update.yml`.

`npm run package` therefore produces:

- `release/mac-universal/Fugaro Console.app` — signed with the hardened runtime
  and this directory's entitlements, **notarised and stapled by electron-builder**
  (it submits the `.app` through `@electron/notarize` and staples the ticket to
  the `.app`);
- `release/Fugaro Console-<version>-universal.dmg` and
  `release/Fugaro Console-<version>-universal-mac.zip` — built from that stapled
  app. **electron-builder 26 neither signs (`dmg.sign` defaults to false), nor
  notarises, nor staples the DMG itself.** Notarising and stapling the DMG is
  Task 4.2's workflow step: `xcrun notarytool submit` the DMG `--wait`, then
  `xcrun stapler staple` it.

**The certificate has to be in the packaging command's environment**: the fuses are flipped
before the packer signs, and a bundle left with the ad-hoc signature the fuse
flip invalidated will not launch on arm64. A build with no signing material is a
build with no signature and no notarisation — `spctl` refuses it, which is the
correct outcome and not a failure of this configuration.

Then, on the artifact (these are also the FACT lines Check 59a is written
around, and the checks Task 4.5 re-runs after installing):

```sh
APP="release/mac-universal/Fugaro Console.app"
codesign --verify --deep --strict --verbose=2 "$APP"
codesign -d --entitlements - "$APP"          # must print allow-jit and nothing else
xcrun stapler validate "$APP"
spctl --assess --type execute --verbose=4 "$APP"
lipo -archs "$APP/Contents/MacOS/Fugaro Console"   # x86_64 arm64
# The DMG checks hold only after Task 4.2's notarise-and-staple step on the DMG:
#   xcrun stapler validate release/*.dmg
#   spctl --assess --type open --context context:primary-signature -v release/*.dmg
```

Two things this section is *not*: it is not Task 4.2's workflow (the staged
`docs/pending-workflows/console-release.yml` names its own secrets and runs the
same steps in CI, and it is the owner who copies it in), and it is not a claim
that a *signed* build has run — no agent has signed or notarised a bundle in
this repository; the unsigned pack above is the most that ran.

### The hook's import: what was run rather than assumed

`src/main/fuses.ts` imports `@electron/fuses`, which is an ES module, and the
console's build emits CommonJS — so `tsc` refuses that import for its *format*
(TS1479) in `tsconfig.build.json`, while the type checker (`tsconfig.json`,
`module: ESNext` + `moduleResolution: bundler`) resolves it and reports nothing.
The import therefore carries a `@ts-ignore`, which is why it is `@ts-ignore` and
not `@ts-expect-error`: the expect-error form fails the type checker as *unused*.
It suppresses the format rule and nothing else — the package's types still land
on every binding — but a suppressed import line is one `tsc` will not flag if
this package changes its API, so `fuses.test.ts` carries that check instead: it
imports the same package and asserts both calls are exported, that every fuse
name in the table maps to the index the table gives it, and that the wire states
the verification compares against are still spelled the same way.

The two facts this rests on were **run, not assumed**, after `npm run build`:

```sh
$ node -e "console.log(Object.keys(require('./dist/main/fuses.js')).join(','))"
FLIPPED_FUSES,LEFT_ALONE_FUSES,BEYOND_PIN_FUSES,ACCOUNTED_FUSE_COUNT,SUPPORTED_PLATFORMS,fuseConfig,macExecutable,linuxExecutable,fuseTarget,afterPack
$ node -e "console.log(JSON.stringify(require('./dist/main/fuses.js').fuseConfig()))"
{"0":false,"2":false,"3":false,"4":true,"5":true,"7":false,"version":"1"}
```

— the emitted `require("@electron/fuses")` resolves under Node 22.12+'s
`require(esm)` (it is the same mechanism electron-builder's own fuse support
uses), the six flips arrive at their documented indices with nothing else set,
and calling the hook for a darwin context reaches the real `flipFuses` rather
than a stub: it failed inside the Electron framework lookup for a path that has
no bundle, which is what a real flip attempt against a non-bundle path does.
**`fuseTarget` and the two platform functions are exported for the same reason
the table is**: the resolution is the rule, and a test that could not call it
with an injected platform could only assert one platform's answer.

### The packer is a pinned devDependency, and nothing it installs runs

`electron-builder` `26.18.0` is in `devDependencies`, recorded in
`package-lock.json` with its whole tree (exact versions, integrity hashes, every
entry from `https://registry.npmjs.org/`), and `npm run package` runs its local
binary — never `npx`, which resolves a tree outside the lockfile and runs its
install scripts in the shell the signing secrets are exported into. An earlier
revision did exactly that (`npx --yes electron-builder@26.0.12`), because the
26.0 line's tree carried a git-sourced `@electron/node-gyp` that
`lockfile.test.ts` refuses; from the 26.1 line the packer depends on `@electron/rebuild`
4, whose `node-gyp` comes from the registry, so the 26.18.0 tree passes the
lockfile test with no exception and none was added. Three settings keep the
packer from reaching outside what is pinned:

- `npmRebuild: false` — the console has no native module, and a rebuild runs
  `node-gyp` and install scripts;
- `electronDownload.checksums` — the two Electron archives a universal build
  downloads are verified against hashes in this file, equal to the pinned
  `electron` package's `checksums.json`;
- the toolsets the packer fetches for the DMG and the zip (`dmgbuild`, `7zip`)
  are verified against SHA-256 hashes hard-coded in the locked `dmg-builder` and
  `app-builder-lib` packages.

**Publishing is off from both sides.** `build.publish` is `null`, not merely
absent — absent, electron-builder infers a GitHub provider whenever `GH_TOKEN` or
`GITHUB_TOKEN` is in the environment, writes `app-update.yml` into the bundle and
uploads on a CI tag — and the script passes `--publish never`.

### The seam Task 1.1 left, settled

Task 1.1's section above records the one thing it handed to packaging: `tsc`
emits JavaScript into `dist/` and copies nothing, while the protocol handler
serves `src/renderer` (`rendererRoot` in `src/main/index.ts`), so "a packaged
console needs `src/renderer` on disk". Task 4.1 settles it by naming the file
rather than moving it: `build.files` is

```json
["THIRD_PARTY_NOTICES.md", "dist/main/**/*.js", "!dist/main/fuses.js",
 "dist/preload/index.js", "dist/preload/installations.js",
 "dist/preload/confirm.js",
 "dist/shared/**/*.js", "dist/source/**/*.js", "!dist/source/fixture.js",
 "dist/bundle/confirm.html", "dist/bundle/confirm.js",
 "dist/bundle/console.css", "dist/bundle/console.js",
 "dist/bundle/elk-worker.min.js", "dist/bundle/index.html",
 "dist/bundle/installations.html", "dist/bundle/installations.js",
 "dist/bundle/refusal.html"]
```

(as amended when Task 3.4a moved the served root to `dist/bundle/`, Task 3.4b
added the picker's document, script and preload, Task 6.2 added the served
stylesheet, GV2.2 added the pinned worker the build copies verbatim and the
notices file packed beside the app, and V2.5.1a part 2b-2a added the launch
confirmation window's document and bundle; the paragraph below is Task 4.1's
record of its own first list)

— the compiled main process, preload, shared and source modules, the document
the window actually loads, and nothing else. Two compiled files are excluded by
name because the app never loads them: `dist/main/fuses.js` is the packing hook
(the packer loads it from the project directory, and it needs a devDependency)
and `dist/source/fixture.js` is the fixture loader the tests and the recorder
use. No source map ships (every include names `.js`, one `.html` file or the notices
`.md`, and a map in the asar is a second copy of the sources with a pointer to
it).
`fuses.test.ts` walks the import closure of `src/main/index.ts` and
`src/preload/index.ts` and asserts every module in it is shipped and the two
excluded files are not. The
renderer's *scripts* are compiled into `dist/renderer/` and are not served from
there: no task serves a script to the document yet (the e2e specs draw their
screens through `executeJavaScript`), and whichever task does it has to put the
compiled file inside the served root — the allow-list above is where that
becomes visible rather than being discovered by an empty packaged window.

Two smaller decisions belong to this list. `directories.output` is `release`
rather than electron-builder's default `dist`, because `dist` is the compiler's
output directory here — the packer would otherwise write the app it is packing
into the tree it is packing from — and `release/` is in `.gitignore` for the
same reason. `appId` is `io.github.dimipaun.fugaro-console`: electron-builder's
default is `com.electron.<name>`, which claims upstream's namespace, and this
repository has no domain, so the project's GitHub home reversed is the honest
choice. It is a deliberate edit if it ever changes, because the bundle id is the
identity Gatekeeper and the notary record keep.

### What is deliberately not here

- **A signing workflow.** Task 4.2 stages it under `docs/pending-workflows/`,
  and that task's file, not this one, names the CI secrets. This section's
  commands are the owner's local run.
- **Automatic updates.** `build.publish` is `null`, the script passes
  `--publish never`, there is no feed, and `electron-updater` is not a
  dependency: Task 4.4 is the auto-update task and brings all of that with it.
- **An icon** — *filled in on 2026-10-19, by the icon task at the end of this
  document.* This bullet used to read that there is no `build/icon.icns`, so the
  bundle carries Electron's default icon, and that it is a packaging wart rather
  than a security property needing an asset file no task had produced. The asset
  file now exists: `build.mac.icon` names `console/build/icon.png`, the
  website's own mark, and a packaged bundle carries it.
- **Any signed build.** No bundle has been signed or notarised here; one
  unsigned universal pack was run (above). The signed build is Check 59a's job,
  and design §12 item 4 stays open until it has run.

### The properties are tests, and each one fails under a mutation

Every claim above is asserted, and the two that matter most were re-run by hand
against a planted change in the shipped files — recorded here the way the other
tasks record theirs, because a test that has never been seen red is a test
nobody should trust:

| Mutation | What went red |
|---|---|
| `com.apple.security.cs.disable-library-validation` added to `entitlements.mac.plist` | three named tests: "grants com.apple.security.cs.allow-jit and nothing else", "grants nothing that the set above does not name", and the named test for that one key |
| `RunAsNode` flipped to `true` in `FLIPPED_FUSES` | "leaves RunAsNode disabled: the binary is never a Node interpreter", and the wire-verification test (the fake wire now agrees with the mutated configuration, so the hook no longer refuses it) |
| `EnableNodeCliInspectArguments` given the index of the fuse above it (a copy-paste) | four named tests, including "knows every fuse this build names, at the index this build names it at" and "accounts for every fuse this pin knows" |
| a value the plist parser does not understand (`<integer>`, a nested dict, a root array) | the parser fixtures: each throws rather than reporting an empty set |
| a wire with `RunAsNode` left enabled, or with a fuse "removed" rather than set | the two `afterPack` verification tests, whose failure messages name the fuse and the binary |
| `electronPlatformName: 'win32'` handed to the hook | "refuses a platform this build does not package, rather than shipping an unflipped binary", naming the platform and the two it packages, and `flipFuses` is never called |
| `macExecutable` returned for a linux context, or the linux branch made to read `productName` instead of `executableName` | "names the Linux executable electron-builder produced, by its executableName", "falls back to the packager's sanitized name when no executableName is configured" and "refuses a Linux context that names no binary, rather than guessing a path to flip" |
| `SUPPORTED_PLATFORMS` cut back to `['darwin']`, or `PLATFORM_LOCATIONS`'s `linux` row deleted from `src/main/spawn.ts` | that half's own list test: "refuses a platform this build does not package…" in `fuses.test.ts`, and the location-table test in `spawn.test.ts` — each module asserts the platform list it is keyed on, so neither can lose a platform unnoticed |
| any entry added to `build.files`, or `asar` turned off, or `afterPack` repointed, or `productName` renamed | the packaging-configuration tests in `fuses.test.ts`, one named test each |
| `asarUnpack: ["**/*"]`, `extraResources`, `extraFiles` or a `mas` block added to `build`; `extraResources` added to `build.mac` | "has exactly the build keys this task decided…" / "has exactly the mac keys this task decided" |
| `publish` deleted (absent rather than `null`) | "publishes nowhere: publish is null, not merely absent" and the exact-keys test |
| the `package` script back to `npx --yes electron-builder@26.0.12 …`, or `--publish always` | "packages with the pinned local electron-builder, universal, and never publishing" |
| `dist/renderer/**/*.js` added to `build.files`, or `!dist/source/fixture.js` removed | the exact-list test and "ships every module the main process and the preload load, and neither excluded file" |
| a zeroed Electron x64 hash in `electronDownload`; `npmRebuild: true` | the checksum test (and, at pack time, electron-builder's own "did not match expected checksum"); the rebuild test |
| `WasmTrapHandlers` dropped from `LEFT_ALONE_FUSES` (undecided) | eight tests, including both real-binary tests: the wire holds 9 fuses and the file accounts for 8 |

## The signing and notarisation pipeline, staged (Task 4.2)

The workflow that signs, notarises and staples this bundle is **staged, not
applied**: `docs/pending-workflows/console-release.yml`, copied into
`.github/workflows/` by the owner as Task 4.5, with the file's row, the one
command that applies it and its "Not verified" note in
`docs/pending-workflows/README.md`. What belongs *here* is the part that is
about this directory: what the workflow has to line up with, what the owner has
to create before it can run, and what nobody has run yet.

**It is a separate file from `console-tests.yml`, and that separation is the
security boundary, not tidiness.** The release workflow reads the Apple signing
certificate and the App Store Connect key; the tests workflow runs on every pull
request from any branch of this repository and holds no secret at all. One file
carrying both jobs would put the signing job's definition next to a job any
branch can run, where the only thing between them is GitHub's rule about
*forks* — and a branch is not a fork (design §9.4). So the release file's `on:`
is one event, the push of a strict `vX.Y.Z` tag, and it carries no
`pull_request`, `pull_request_target`, `workflow_run` or `workflow_dispatch`
trigger of any kind.

**The six secrets, by name.** These are the names the workflow reads, the names
the protected environment holds, and the names listed in
`docs/pending-workflows/README.md`. No value of any of them is in this
repository, in the workflow, or in any run an agent performs:

| Secret | What it is |
|---|---|
| `APPLE_CERTIFICATE` | base64 of the Developer ID Application `.p12`, one line (the G2 Sub-CA certificate) |
| `APPLE_CERTIFICATE_PASSWORD` | that `.p12`'s password |
| `APPLE_TEAM_ID` | the team identifier `ZM8Y2LC7JL` — an identifier and not a credential, which is why the workflow can check it against the signature it built |
| `APPLE_API_KEY` | the App Store Connect API key's `.p8` contents |
| `APPLE_API_KEY_ID` | the key's id |
| `APPLE_API_ISSUER` | the issuer uuid |

**The notarisation credential is the App Store Connect API key, not an
app-specific password.** D4 leaves the choice open; this is the decision, so
`APPLE_ID` and `APPLE_APP_PASSWORD` are deliberately absent. The workflow
writes the `.p8` under `umask 077` (created private, never tightened after) in
the runner's temp directory and hands on its *path*, which is what both
readers take: `APPLE_API_KEY` is a file to electron-builder and `--key` is a
file to notarytool. The key's value never reaches a command line.

**Two jobs, and the write token is never in the job that runs the packer.**
`release` (the protected environment, the Apple secrets, the packer, on
`macos-15`) holds `contents: read` and `checks: read` only: it gates, builds,
signs, notarises, staples, verifies, and uploads the `.dmg`, the `.zip`, the
record and the SHA-256 sums as the workflow artifact `console-release-assets`.
`publish` (`needs: release`, on `ubuntu-24.04`: no environment, no Apple
secret, no checkout, no packer) is the only job with `contents: write`: it
downloads the artifact, re-checks every file with `sha256sum --strict --check`
and that the `.dmg` and `.zip` are named for the tag's version, and publishes.
The artifact actions carry the pins `release.yml` and `images.yml` already use.
`GH_TOKEN` is in exactly two steps' `env:`: the gate (read-only) and the
publish step.

**The version is the tag's.** `package.json` says `0.0.0` and nothing in it is
a release version, so a step before anything is built refuses a tag that is
not a strict `vX.Y.Z`, and every pack is stamped with
`-c.extraMetadata.version=X.Y.Z` (through `env:`). The `.dmg` and `.zip` names
therefore carry the real version (`Fugaro Console-X.Y.Z-universal.dmg`,
`…-universal-mac.zip`), the signing step refuses artifacts not named for it,
and the update feed of plan Tasks 4.3/4.4 depends on it: a console that called
itself 0.0.0 would never see an update as newer.

**No secret is placed in any step's environment or on disk while anything
installs or downloads, and one step holds all six.** (The runner process
receives the environment's secrets when the job starts; what the file controls
is where they are exposed.) The checkout, setup-node (no `cache:`: a restored
cache is written by other runs), `npm ci --ignore-scripts --no-audit
--no-fund`, Electron's own download (`node node_modules/electron/install.js`)
and an unsigned warm-up `npm run package -- -c.mac.identity=null
-c.mac.notarize=false -c.extraMetadata.version=X.Y.Z` (whose output is
deleted) run first, with no
secret in any step's environment: the warm-up is what downloads the two Electron archives the
universal build needs and the packer's DMG and zip toolsets, into the packer's
cache. Then **one step** — `Sign, notarise and staple the console` — reads the
six secrets into its own `env:` (never a workflow- or job-level `env:`) and
does everything that needs them, in this order: the fail-closed check, the
temporary keychain, the certificate's team checked against `APPLE_TEAM_ID`,
the `.p8` file, the first `npm run package` (with the certificate, its
password and the key's contents unset — which keeps them out of the packer's
environment, not out of the signing shell's own starting environment — and
`APPLE_API_KEY` re-exported as the key file's path), the signature's
TeamIdentifier checked, the bundle notarised, stapled and validated by
`console/scripts/notarize-and-staple.sh`, the `.dmg` and the `.zip` packed
from that stapled bundle, and the `.dmg` signed and then notarised, stapled and
validated by the same script. Its exit trap deletes the keychain and
both key files whether it passed or failed, so no later step runs with key
material on disk; an `if: always()` step at the end is the backstop.

**The order inside that step is the load-bearing part: the bundle is notarised
and stapled *before* the `.dmg` and the `.zip` are built, and what is verified
is the bundle inside them.** An earlier revision of the staged file notarised
after packing, which ships two artifacts carrying an unstapled bundle (the
review of Task 4.2 caught it). The current revision goes further, because the
packer's own notarisation turned out to be a place nothing can retry: **the
workflow now does the notarising, and the packer does none of it.**

The packer is run twice in the signing step, with `-c.mac.notarize=false` both
times — `npm run package` for the first, and `npm run package:targets` (the
same packer without its `npm run build`) for the second:

1. `npm run package -- --dir -c.mac.notarize=false
   -c.extraMetadata.version=X.Y.Z` builds and signs the universal `.app` and
   **nothing else** — no `.dmg`, no `.zip` — and, with notarisation off, does
   not submit it or staple it. That call is the one that failed: electron-builder's
   `@electron/notarize` step submits the bundle *and staples the ticket* inside
   `npm run package`, so a lost connection to Apple's ticket delivery there is
   one call in the middle of the packer's process, where nothing can retry it
   and the pack simply fails (error 68).
2. `console/scripts/notarize-and-staple.sh app "release/mac-universal/Fugaro
   Console.app"` does the three calls this file owns: `ditto -c -k
   --keepParent` the bundle into a `.zip` and `xcrun notarytool submit … --wait
   --output-format json` it **once**, refusing at once (no staple, the answer
   printed) unless `notarytool` **exited 0 and** Apple's status is exactly
   `Accepted` — neither alone is the whole answer; then `xcrun stapler staple`
   and `xcrun stapler validate`, both retried.
3. `npm run package:targets -- --prepackaged "$APP" -c.mac.notarize=false
   -c.extraMetadata.version=X.Y.Z` builds the `.dmg` and the `.zip` **from the
   bundle that now carries its ticket**, so both artifacts contain a stapled
   `.app`. This is the one-of-the-two pack that does not rebuild: `package` is
   `npm run build && electron-builder …`, and with `--prepackaged` the bundle
   is already built, so the compile would only be spent a second time — here,
   in the step that holds the unlocked keychain and the exported `.p8` path.
   `package:targets` is the same pinned local packer alone. What the first pack
   builds is unchanged.
4. The `.dmg` is **signed** (`codesign --sign <identity> --timestamp`:
   electron-builder leaves `dmg.sign` false, Apple's guidance is to sign a disk
   image before notarising it, and a hardened runtime is for executables, not a
   `.dmg`), then handed to the same script as `dmg`, which submits it (a
   notarytool submission takes a `.dmg` as itself — no zip step) and staples
   and validates it with the retry.

The owner's own `npm run package` on their Mac (Task 4.1, above) is unchanged:
no `-c.mac.notarize=false`, no `--dir`, so it still signs, notarises and
staples the `.app` in one command and then builds the targets. Nothing in
`console/package.json`'s **build configuration** changed — the switch is on the
workflow's command lines, which is also what keeps `entitlements.test.ts`'s
"does not turn the notariser off" assertion true of the checked-in
configuration. The manifest did gain one script, `package:targets`,
`electron-builder --mac --universal --publish never` with no `npm run build`,
which is what the workflow's second pack uses; `fuses.test.ts` still pins
`package` exactly as it was.

**Every `xcrun stapler` call in the pipeline is retried, bounded — the retry
and the move out of the packer landed together, on 2026-10-19, after the
release run failed there.** `xcrun stapler staple` and `xcrun stapler validate`
reach Apple's ticket-delivery service over the network, and the run printed
`CloudKit … error is Error Domain=NSURLErrorDomain Code=-1005 "The network
connection was lost"` against
`.../com.apple.gk.ticket-delivery/production/public/records/lookup`, error 68,
after which the signing step refused with "carries no stapled ticket: refusing
to publish artifacts that would carry it unstapled". **Which call failed
matters, and it moved the notarisation out of the packer**: release run
39163351646 was attempted five times, and of those only the fourth and the
fifth reached Apple — the fourth attempt's failure was `@electron/notarize`'s
staple *inside* `npm run package` ("Failed to staple your application with
code: 68"), and the fifth's a `xcrun stapler validate` **on the bundle**, in
the workflow, before any target was packed. The three earlier attempts never
reached Apple: the first failed within about a second with no step having
started (its cause is not recorded, and this section does not guess one), the
second on missing `APPLE_API_*` secrets, the third on a certificate import with
the wrong password. So "three consecutive runs failed at the stapler" is not
what happened, and this section said so until 2026-10-19. The notarisation
itself had been accepted where it was submitted: what was lost was the
connection, not the ticket.

So one shell function, `retry_apple_network`, defined once in
`console/scripts/notarize-and-staple.sh` and once — character for character the
same — in the verification step, makes up to five attempts at every `xcrun
stapler` call on the way to a release (the bundle's and the disk image's in the
script; the bundle inside the mounted `.dmg`, the one inside the extracted
`.zip` and the disk image itself in the verification step), with a growing
pause between them (15, 30, 60, 90 seconds), printing each attempt's number and
exit code, and returns the failure only after the fifth: a ticket that is
genuinely absent still refuses to publish. In the verification step the calls
sit inside `record`, so every attempt is in `console-verification.txt` too.

**The submission is deliberately not retried, and the file is not pretending
otherwise.** `xcrun notarytool submit --wait` is the submission itself — a
rejection is final, and re-submitting is not this file's business — so the
script runs it once, reads Apple's own answer as JSON (`--output-format json`)
and refuses at once, with the answer printed and no staple, unless `notarytool`
exited 0 **and** the status is exactly `Accepted`. Both halves are needed:
`notarytool` can exit non-zero having printed an answer, and it can exit 0
having printed something other than `Accepted`, and neither case is a
submission with a ticket to staple. **Nothing outside the `xcrun stapler` calls
is retried at all** — `codesign`, the submission and its result, the TeamIdentifier
checks, `scripts/release-gate.sh` and the fail-closed secret check all still
fail at once, with no retry — and the tests read it both ways over the script
and over every step: every line naming the helper is one of those calls, and
every line that calls `xcrun stapler` names the helper.

**The environment is `console-release`, and the owner creates it before the
copy-in.** Required reviewers on the owner, deployment branches restricted to
tags. A staging file cannot create an environment, which is why Task 4.5 is the
copy-in *and* the environment. **That reviewer, with a tag ruleset, is the real
control, not the file's gate**: a tag push runs the workflow as the *tagged*
commit holds it, so a tag on a branch whose copy of the workflow or of
`scripts/release-gate.sh` was edited runs the edited copy. The approver
confirms the tag's commit is on `main` before approving the deployment. The
job's first step checks the same (`git merge-base --is-ancestor "$GITHUB_SHA"
origin/main`), which catches a mistake, not someone who controls the tagged
tree.

**It fails closed, and that is the first property to check when reading it.**
The signing step's first act is to refuse to continue unless all six secrets
are non-empty, naming the missing one. No step carries an `if:` except the
cleanup's `always()`, nothing carries `continue-on-error`, and `|| true` is the
cleanup's alone: a signing job that skipped its signing when a secret was
absent would publish an **unsigned** console under a signed release's name —
worse than publishing nothing.

**Two signing paths, documented separately, and neither borrows the other's
mechanism.** The **owner's manual packaging** is Task 4.1's "The owner's
commands, exactly" above: on the owner's Mac, `npm run package` under `env -i`,
`CSC_LINK` the *file path* of the `.p12`, and the certificate's password read
from the terminal inside that `env -i` shell. The **CI workflow** is this
section: on the `macos-15` runner, the `.p12` imported from the
`APPLE_CERTIFICATE` secret into a temporary keychain, and the identity named
with `CSC_NAME` + `CSC_KEYCHAIN`; `CSC_LINK` and `CSC_KEY_PASSWORD` are never
set there. Nothing in this section changes the owner's commands, and they are
not a fallback for the workflow.

**Where the two agree, and where the workflow deliberately differs.** Both call
the same script, `console/scripts/notarize-and-staple.sh`, and the same pinned
local electron-builder (`--mac --universal --publish never`, through `npm run
package`, or through `package:targets` where the workflow has no reason to
rebuild), so the hardened runtime, the `.dmg` and `.zip` targets,
`entitlements.mac.plist` and the fuse hook are one configuration and not two.
The differences are on purpose:

- **The certificate goes into a keychain the step creates**, never the runner's
  login keychain: a random password from `openssl rand`, `security
  create-keychain`, `security import -T /usr/bin/codesign`, `security
  set-key-partition-list -S apple-tool:,apple:` (codesign is an Apple-signed
  tool). `/usr/bin/security` is deliberately **not** a trusted application of
  the key: with it, any later command could `security export` the private key
  without a prompt.
- **The signing identity is named explicitly**, through `CSC_NAME` set to the
  SHA-1 of the identity in that keychain (`CSC_KEYCHAIN`). The SHA-1 of a
  certificate is public; a runner carrying more than one identity cannot pick
  another.
- **The team is checked before the notary is spent**: the identity's name must
  end in `(APPLE_TEAM_ID)` before anything is packed, and `codesign -dv` must
  report the same TeamIdentifier on the bundle after, so a development
  certificate or another team's is refused rather than submitted.
- **The packer's own notarisation is off** (`-c.mac.notarize=false` on every
  pack, with `--dir` for the bundle-only one and `--prepackaged` for the
  targets, which go through `npm run package:targets` rather than `npm run
  package` because the bundle they pack is already built), and the workflow's
  own `console/scripts/notarize-and-staple.sh`
  submits, staples and validates instead. That is the one deliberate difference
  in the *packing* as well as in the signing: the packer's staple is a call
  inside its own process, so a lost connection there cannot be retried, and one
  did fail a release run. The owner's manual flow keeps the packer's
  notarisation, so this is a property of the workflow's command lines and of
  nothing checked in.

**The verification is recorded, not just performed, and it is about the
shipped bytes.** The SHA-256 of the `.dmg` and the `.zip` opens the record
(and is published as `console-sha256sums.txt`), so the record is tied to the
files a user downloads. The `.dmg` is mounted read-only and the bundle inside it
is what `codesign --verify --deep --strict`, `codesign -d --entitlements -`,
`spctl --assess --type execute` and `xcrun stapler validate` run against; the
`.zip` is extracted and its bundle gets the same treatment; the disk image's own
signature, ticket and Gatekeeper assessment (`codesign --verify`, `xcrun
stapler validate`, `spctl --assess --type open`) close it. All of it, with the
commands' real output, goes into `console-verification.txt`. Each command must
succeed: `record()` exits 1 on the first failure.

**The console publishes its own release, `console-vX.Y.Z`, and never touches
the CLI's** (the owner's ruling of 2026-10-17, "Separate console release").
GoReleaser publishes the CLI's `vX.Y.Z` release from the same tag push, and
with immutable releases on nothing can be added to it once published. So the
`publish` job creates `console-vX.Y.Z` — X.Y.Z from the CLI tag that triggered it, the
same commit and the same gate (D11: one gate, the CLI's tag; its own release) —
as a **draft**, puts the `.dmg`, the `.zip`, the verification record and the
sums on it, and publishes it in the same step (`gh release edit --draft=false`,
`--latest=false` so the CLI's release stays the latest). A re-run never
clobbers: an existing `console-vX.Y.Z` (published, or a draft left by a failed
attempt, which the owner deletes before re-running) or tag stops the job, and
only a 404 counts as "no such tag": any other answer (a 5xx, a rate limit)
stops it too rather than publishing on a guess. The
tag ruleset must let the workflow create `console-v*` tags. The console's
Homebrew cask, when there is one, points at this release.

**Nothing about it has run, and this section claims nothing else.** No bundle
has been signed, notarised or verified by any agent, here or in CI: the
workflow has never been applied. Check 59a and 59b (plan Task 4.3, owner-run)
are the first real run, and the workflow's own `Not verified` note in
`docs/pending-workflows/README.md` says the same. **Not verified, and Check
59a's to settle:** that electron-builder accepts `CSC_NAME` as a SHA-1 together
with `CSC_KEYCHAIN`; that codesign signs with only `apple-tool:,apple:` in the
partition list; that the warm-up pack leaves everything the signed pack needs
in the cache; that `-c.extraMetadata.version` reaches the bundle's version
and the artifact names as described; and that the signed, stapled DMG passes
`spctl --assess --type open --context context:primary-signature`. To that the
2026-10-19 retry adds one more: that a `xcrun stapler` call which really does
meet a transient CloudKit failure recovers inside the five attempts (the fake
`xcrun` the tests run the helper and the script with proves the *control flow* —
five attempts, the pauses, the refusal, the no-retry on a submission whose exit
status or status is not good — and nothing about Apple's service). The same
change's move of the notarisation out of the packer adds three more, all about
electron-builder's own flags rather than the retry: that `--dir` builds the
universal `.app` and no targets, that `--prepackaged "$APP"` packs the `.dmg`
and the `.zip` from that bundle with the tag's version still in their names
under `--mac --universal` and with `-c.mac.notarize=false`, and that a
prepackaged pack needs no `npm run build` before it (which is what
`package:targets` leaves out). None of these can be checked offline. **Accepted residuals:** `security` takes the
keychain password (`create-keychain -p`, `unlock-keychain -p`,
`set-key-partition-list -k`) and the `.p12` password (`import -P`) only on its
command line, so another process of the same user can read them in the process
list while those commands run, and the signing shell's starting environment
holds the six secrets for the step's lifetime (`ps -E`, same user); the runner
is a single-job VM and the packer is the only third-party code in that window.

### What is deliberately not here (Task 4.2)

- **A `workflow_dispatch` trigger**, or any other way to run the signing job by
  hand. A manual trigger is a second way into a job that reads a private key,
  and the release train this repository already has — one tag, `release.yml` and
  `images.yml` and now this — does not need one.
- **Any action that could see the key material.** The signing, notarising,
  stapling and verifying are `run:` calls to `xcrun`, `codesign`, `security`
  and `openssl`. Checkout and setup-node run before the signing step;
  upload-artifact runs after it, when its exit trap has removed the key
  material; download-artifact runs in `publish`. All four are pinned to full
  commits with version comments, the artifact pair at the pins the applied
  workflows already use.
- **An auto-update feed.** `build.publish` is `null` and this workflow publishes
  the console's own release and nowhere else: Task 4.4 is the auto-update task
  and it is not part of this MVP.
- **A copy under `.github/workflows/`.** No agent creates, edits, renames or
  deletes anything there — GitHub rejects a push that touches it, so the copy-in
  is the owner's Task 4.5. While both files exist,
  `TestDocsConsoleReleaseWorkflowIsStagedAndPinned` asserts they are
  byte-identical, so an edit to one without the other fails a named test rather
  than drifting silently.

### The properties are tests, and each one fails under a mutation (Task 4.2)

Every claim above is asserted in
`internal/cli/docs_desktop_console_test.go` (`TestDocsConsoleReleaseWorkflowIsStagedAndPinned`,
`TestDocsConsoleReleaseWorkflowHoldsNoPullRequestTrigger`,
`TestDocsConsoleReleasePublishRefusesToClobber` and
`TestDocsConsoleReleaseTagStepStampsOnlyStrictVersions` (these two run the
staged scripts under bash with a faked `gh`), and
`TestDocsPendingWorkflowsReadmeNamesTheConsoleRows`, all selected by the
`-run 'Docs|Lint'` filter), reading the file structurally with yaml.v3 where a
substring scan would let a mutation through. Each mutation below was applied to
a scratch copy of the staged file and turned the named test red:

| Mutation | What went red |
|---|---|
| `pull_request:` added to the staged file's `on:` | `TestDocsConsoleReleaseWorkflowHoldsNoPullRequestTrigger`, on the trigger block's own text and on the parsed trigger keys |
| `uses: actions/checkout@v5` (a tag instead of a full commit) | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`'s pin scan |
| `if [ -n "$missing" ]` changed to `if false` in the fail-closed check; or its `exit 1` removed | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the fail-closed lines read in order |
| `if: false` on the ancestry step, the gate or the signing step | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the parsed steps: no `if:` except the cleanup's `always()` |
| `continue-on-error: true` on a step or on the job | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the parsed step and job keys, and on the text |
| `|| true` added in the verification or the signing step | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on `|| true` outside the cleanup step |
| a workflow-level or job-level `env:` holding a secret; a secret in the warm-up step's `env:` | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the parsed `env:` levels, and on secrets being in exactly one step |
| `${{ github.ref_name }}` or a `${{ steps.… }}` output inlined into a `run:` | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on any `${{` in a script |
| `set -x` / `set -eux` added | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the shell-trace scan |
| `exit 1` removed from `record()` | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on `record()`'s body, line by line |
| `cat "$KEY"`, or `echo "$APPLE_CERTIFICATE_PASSWORD"`, added | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the allow-list of lines that may name the key material |
| `-T /usr/bin/security` added to the import | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the import line and the text scan |
| the `.p8` written then `chmod 600` (not under `umask 077`) | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the umask line |
| the `.dmg` `codesign --sign` removed | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the disk image's sign, notarise, staple order |
| the packer back to `npx --yes electron-builder@26.0.12` | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on `npx` / `electron-builder` in any script |
| `runs-on: macos-latest` | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the pinned `macos-15` |
| the app path back to `release/mac-*` with `head -n1` | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the exact `release/mac-universal` path |
| the ancestry check replaced with `if false` | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the first run step |
| `gh release upload "$TAG" --clobber` added (the CLI's release) | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on `gh release upload`, `--clobber` and the CLI tag |
| the published-release refusal's `exit 1` removed | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on each re-run refusal |
| the certificate's team check emptied (team checked only after notarising) | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the team check before `npm run package` |
| the SHA-256 line removed from the record | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on `record cat "$SUMS"` |
| the signing step moved before the install and the warm-up | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the step order |
| the version stamp removed from the signed pack, or from the warm-up pack | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on every `npm run package` carrying `-c.extraMetadata.version="$VERSION"` |
| the tag-shape check disabled (`if false && [[ … ]]`) | `TestDocsConsoleReleaseTagStepStampsOnlyStrictVersions`, on `v1.2` passing |
| the version-named-artifact check disabled | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the signing step's refusal |
| a pass-all `*) ;;` default in the existing-release `case` | `TestDocsConsoleReleasePublishRefusesToClobber`, on publishing over a published release and a draft |
| the tag lookup replaced by `if false` | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the tag refusal's shape |
| a 5xx tag lookup read as "absent" (`*) ;;` in the answer `case`) | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on that `case`'s default branch |
| `contents: write` on the release job; `GH_TOKEN` in the warm-up step | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on each job's permissions and on which steps hold the token |
| `environment: console-release` on the publish job | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the publish job holding no environment |
| `cache: npm` restored on setup-node | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on setup-node's keys |
| `curl … | sh`, `eval`, `env`, `printenv | sort`, `set | grep` added | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the download-and-run and environment-dump scans |
| the publish job's `sha256sum --strict --check` removed | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the publish job's steps |
| the upload-artifact pin changed | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the repository's existing pin |
| `retry_apple_network` removed from `console/scripts/notarize-and-staple.sh` or from the verification step | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the helper's definition in each of the two places and on every call site it must have |
| the retry wrapped around a step's `codesign --sign`, or around the script's `xcrun notarytool submit` | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the only-lines-that-may-name-the-helper clause ("retries … the helper wraps the `xcrun stapler` calls this pipeline makes and nothing else") |
| the script's `xcrun stapler staple` or `xcrun stapler validate` left unwrapped | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the two retried calls the script must run |
| the attempt bound lowered to 3, in the script only | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the two definitions differing, and on the five-attempt clause; and `TestDocsConsoleReleaseRetryHelperIsBounded` and `TestDocsConsoleNotarizeAndStapleScript`, on the fifth attempt never being made |
| the first pause changed (15 → 5) in the script only | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the pause clause; and `TestDocsConsoleReleaseRetryHelperIsBounded`, on the pause the helper asked for |
| the helper made to return after its first failure | `TestDocsConsoleReleaseRetryHelperIsBounded`, on the retried case and on the five-attempt case; and `TestDocsConsoleNotarizeAndStapleScript`, on the staple that recovers after two failures |
| `exit 1` removed from the script's refusal after the fifth failure, at the staple or at the validation | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the refusal's lines; and `TestDocsConsoleNotarizeAndStapleScript`, on the two never-gets-there cases |
| a fresh, un-retried `xcrun stapler validate` added to the script or to a step | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the both-directions clause — every line that calls `xcrun stapler` must name the helper |
| `-c.mac.notarize=false` removed from any `npm run package` (the packer notarising and stapling again) | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on every pack carrying the switch |
| `--dir` dropped from the bundle-only pack, or `--prepackaged "$APP"` from the targets pack | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the bundle/script/targets order |
| the script's call for the `.app`, or the one for the `.dmg`, removed from the signing step | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on that order and on the `.dmg`'s sign-then-notarise order |
| the status check neutered (`if false`), or a status that is not `Accepted` accepted | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the refusal's lines; and `TestDocsConsoleNotarizeAndStapleScript`, on the rejected-submission case (no staple, no pause, non-zero exit) |
| the exit-status half of the submission's result dropped (`[ "$status" != "Accepted" ]` alone, so an Accepted body over a non-zero `notarytool` exit is stapled) | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the refusal's lines; and `TestDocsConsoleNotarizeAndStapleScript`, on the Accepted-body-over-a-non-zero-exit case |
| `npm run build` added to `package:targets` (the second pack rebuilding under the unlocked keychain) | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the manifest's `package:targets` being the packer alone |
| `npm run build` removed from `package` (the first pack, and the owner's command, with nothing to pack) | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on `package` still building |
| the second pack back to plain `npm run package -- --prepackaged "$APP"` | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the targets pack's exact line |
| `--output-format json` dropped from the submission | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on the submission line |
| the `ditto -c -k --keepParent` zip of the bundle dropped | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on that line; and `TestDocsConsoleNotarizeAndStapleScript`, on the calls the fake `xcrun` and `ditto` log |
| the script left non-executable | `TestDocsConsoleReleaseWorkflowIsStagedAndPinned`, on its file mode (the signing step runs it directly) |
| the "Re-copy required" note deleted from `docs/pending-workflows/README.md` while the staged and applied files differ | `TestDocsConsoleReleaseStagedChangeSaysItNeedsACopyIn`: the owner is the one who has to copy the file in, and that section is where they read it |

## The e2e goldens: how to re-record them, and what is committed today

Six screens take goldens, each writing one file per screen per theme per platform
under `test/e2e/__screenshots__/` (`playwright.config.ts`'s
`snapshotPathTemplate` adds `-{platform}`):

| Screen | Spec | Files on one platform |
|---|---|---|
| Live | `test/e2e/live.spec.ts` | `live-light-<platform>.png`, `live-dark-<platform>.png` |
| Spend | `test/e2e/spend.spec.ts` | `spend/light-<platform>.png`, `spend/dark-<platform>.png` |
| Projects | `test/e2e/projects.spec.ts` | `projects/light-<platform>.png`, `projects/dark-<platform>.png` |
| Run detail | `test/e2e/run.spec.ts` | `run/light-<platform>.png`, `run/dark-<platform>.png` |
| Graph | `test/e2e/graph.spec.ts` | `graph/light-<platform>.png`, `graph/dark-<platform>.png` |
| Coordinator | `test/e2e/coordinator.spec.ts` | `coordinator/light-<platform>.png`, `coordinator/dark-<platform>.png` |

`<platform>` is `process.platform` — `darwin` or `linux`. macOS and Linux draw
different pixels, so **each platform's set is recorded on that platform**, and
`updateSnapshots: 'none'` means a missing baseline fails a comparison run rather
than being written by one. Recording is an explicit act:

```
cd console && npm run build && npx playwright test live.spec.ts --update-snapshots
cd console && npm run build && npx playwright test spend.spec.ts --update-snapshots
cd console && npm run build && npx playwright test projects.spec.ts --update-snapshots
cd console && npm run build && npx playwright test run.spec.ts --update-snapshots
cd console && npm run build && npx playwright test graph.spec.ts --update-snapshots
cd console && npm run build && npx playwright test coordinator.spec.ts --update-snapshots
```

On Linux, Electron needs a display, so each line takes `xvfb-run --auto-servernum`
before `npx playwright test`. **`npm run build` is part of the command, not a
convenience**: the specs load the compiled renderer and the served bundle out of
`dist/`, and `npx playwright test` does not build, so a re-record run without it
photographs whatever `dist/` happened to hold. `--update-snapshots` is what
overrides `updateSnapshots: 'none'` for that one run. Add `--workers=1` for a
recording run: `fullyParallel: false` keeps one file's tests in one worker, and
one worker keeps the whole run in one.

`test/support/goldens.ts` is what the six specs share, and it is why a golden
fails legibly rather than with a bare pixel diff:

- **`expectGolden`** takes every recording. On a **comparison** run,
  `assertGoldenFresh` refuses a golden that is **missing** or **older than the
  theme directory** (compared by the commit that last touched each, which is the
  only clock a checkout carries — a clone writes every file at the same moment),
  and prints the exact command above, for that spec, for the platform it is
  running on. A comparison that then fails is re-reported with Playwright's own
  message kept and the same command appended: the matcher's own suggestion
  (`npx playwright test <file> --update-snapshots`) is not that command. A
  **recording** run (any `--update-snapshots` mode, which is what the commands
  above use) is the one case the preflight stands aside for: it is the state that
  fixes every reason a golden cannot be compared, and the matcher is what writes
  the file, so a guard that fired there would refuse to let its own command work.
- **`expectGrounded`** is the assertion that the screens are painted at all. It
  draws each screen once per theme and holds the app's ground
  (`getComputedStyle(document.body).backgroundColor`), the screen's own `h1`
  colour and the two tokens they are painted from (`--ground`, `--text`) to the
  literals `src/renderer/theme/palette.css` ships, and — because one value set
  ships (the owner's Q1 ruling of 2026-10-19) — holds the two runs to the same
  ground as each other. **It is not a golden test and
  `FUGARO_CONSOLE_GOLDENS=skip` does not skip it**: it reads no PNG, and it is the
  one case that is red when the stylesheet stops being served, linked or applied —
  the regression that could otherwise be recorded as a fresh golden and never
  noticed. (Every other read in those specs passes on a tree nothing paints: the
  theme travels as a `data-theme` attribute the renderer stamps.)

**What is committed today is darwin only, and it is superseded.** The eight
`*-darwin.png` files — the Live, Spend, Projects and Run pairs; the Graph and the
Coordinator have **no** baseline on either platform, their cases having landed
after this recording — are the recording of **2026-10-18**: macOS 26.7 (Darwin
25.6.0, x64), Node 22.23.3, `@playwright/test` 1.64.1, Electron 44.7.2 (the
lockfile's versions), device scale factor 1 as the specs launch the window (the
PNGs are 1164 px wide), at main `82ba600d`, recorded with `npm run build` then
`playwright test --workers=1 --update-snapshots`, and compared twice in a plain
run (93 passed both times). Every frame is drawn from the specs' own invented
documents (`example-owner/...`, `acme/...`, `redacted@example.invalid`); none
holds a real name. **They are pictures of the unstyled tree**: they were taken
before `src/renderer/theme/` existed, so no frame carries a rule of CSS. Three of
the four light/dark pairs say so in the plainest way — the two themes' frames of
the Live, Spend and Projects screens are the **same file byte for byte**
(`live-light-darwin.png` and `live-dark-darwin.png` are both 38305 bytes of
`f4b55fed…`, and so on) — while the Run screen's pair differs
(`run/light-darwin.png` 129088 bytes, `run/dark-darwin.png` 129359) for a reason
that has nothing to do with styling: `run.spec.ts` draws its light frame with a
scrub time (`2026-10-15T12:05:00Z`) and its dark frame with `null`, so the two
frames are different content either way. What all eight share is the absence of
styling, and that is what makes them superseded: the theme landed after them
(Tasks 6.1-6.5; the newest file in
`src/renderer/theme/`, `base.css`, was last touched by `6d107527` on 2026-10-19),
which is why they are superseded: re-record them on the Mac with the command above
and commit the result. Until that is done the four superseded screens' golden
tests fail on darwin with that command in the message rather than with a bare
pixel diff — which is the intended state, and not a regression to chase. The
Graph's and the Coordinator's six golden cases (three each) have nothing to
compare against anywhere, so they fail with that same command on darwin and with
its `xvfb-run` form on Linux until the owner records them in the release PR.
**Linux baselines are not committed** and have never been recorded; they need a
Linux machine with a display (`xvfb-run`).

**CI does not compare any golden today.** `.github/workflows/console-tests.yml`
sets `FUGARO_CONSOLE_GOLDENS: skip` at job level, so on both
`console (ubuntu-latest)` and `console (macos-latest)` the golden tests are
skipped. That stays until both platforms' sets are committed: a skip that hides a
comparison is better than a comparison against a picture of a tree that no longer
exists, and the computed-ground assertion above is not skipped, so a tree with no
styling is still red in CI.

## The theme's tokens, and the palette they are painted in (Task 6.1)

`src/renderer/theme/` holds the console's styling, in the files the plan's File Structure
tree promised and the design's section 3.1 lays out — the directory's whole contents, which
is what `THEME_STYLESHEETS` names. Every one of them exists as of Task 6.4, and the column
below names the task that wrote each — which is also the phase's order, and the order the
bundler concatenates them in:

| File | Holds | Written by |
|---|---|---|
| `src/renderer/theme/tokens.css` | the **vocabulary**: spacing, radii, type sizes, leading, weights, the two row heights, the focus ring's width and offset, and the two motion values. **No colour.** | Task 6.1 (this section) |
| `src/renderer/theme/palette.css` | the **value set**: every colour token the console paints, each written once. **No size, no spacing, no motion.** | Task 6.1 (this section) |
| `src/renderer/theme/base.css` | the reset, the document, the three bands, the nav, focus, the responsive layers and the chrome's forced-colors block | Task 6.3 (the section at the foot of this file) |
| `src/renderer/theme/screens.css` | the per-screen rules, in the order the design's section 2 lists the screens | Task 6.4 (the section at the foot of this file) |

**As of Task 6.2 the theme's stylesheets are read, served and linked, and the paragraph
that follows is the record of the stage before that.** Each is named by `THEME_STYLESHEETS` in
`tools/bundle-renderer/index.ts`, the bundler's stylesheet step concatenates them (in list
order) into `dist/bundle/console.css`, `/console.css` is one of the entries `SERVED_FILES`
names — the list `test/unit/bundle.test.ts` holds the served root to, not a filter the
handler consults — and each of the four documents links it — see the section at the foot of
this file.
`test/unit/bundle.test.ts` fails if a stylesheet `THEME_STYLESHEETS` names still says in its
own prose that nothing reads it, which is the sentence this paragraph used to open with and
the reason it was rewritten rather than left standing.

> **What this paragraph said at Task 6.1, kept as the record of that state.** "Nothing reads
> these two files yet … They are inert until Task 6.2 lands the bundler's stylesheet step,
> the `/console.css` entry in `SERVED_FILES`, the three `<link>` elements and the unit tests
> that pin them: a merge of this file pair alone changes nothing on screen. When 6.2 lands,
> the files become **one** served artifact, `dist/bundle/console.css`, concatenated in
> the design's order (vocabulary, values, then the rules that read them) — and the
> packaged-file list in this README and in `package.json`'s `build.files` has to name it,
> which `test/unit/build-files.test.ts` holds (`bundlerOutputs` gains it in that task's
> commit, so an app that ships without its stylesheet fails a test rather than drawing an
> unstyled window)." Every one of those consequences is what 6.2 did.

### Dark only, which is the owner's ruling of 2026-10-19

The design's Q1 asked whether the console ships a light theme and recommended light and
dark with the OS deciding. **The owner ruled dark only**: the console ships one value set,
matching the `CONSOLE_THEME` its screens are drawn with today, and the light set is
deferred — the design's section 3.3 table stays as the record of it, for whichever later
task the owner hands it to. So `palette.css` sets `color-scheme: dark` outright, and no
theme file uses `light-dark()`, reads `prefers-color-scheme`, declares the OS-driven
`light dark` pair or keys a block on `data-theme='light'`. Each of those four spellings is
the way a light theme could arrive without a task owning it, and
`test/unit/theme-tokens.test.ts` fails by name on every one of them.

### The colour contract is a test, and it reads the file rather than a copy of it

`test/unit/theme-tokens.test.ts` parses `palette.css`'s hex literals out of the stylesheet
itself and computes every pair the design's section 3.4 tabulates — 4.5:1 for body-size
text and the five state colours on all three surfaces, 3:1 for the accent and the control
border as UI colours, 4.5:1 for a label on a filled control — with WCAG 2.1's
relative-luminance definition, the same arithmetic `website/test/a11y.mjs` uses. It also
holds the value set to the design's section 3.2 table name by name and literal by literal,
keeps `--border` below 3:1 (it is a decorative hairline, never a control's extent), keeps
the five state colours at least 50° apart in hue, and refuses a colour in the vocabulary.
The red step is kept as a case: a deliberately failing fixture, `#646464` on `#14141F`,
must print `--text-muted on --surface is 3.1:1, needs 4.5:1` — one decimal, because that is
the line `a11y.mjs`'s own committed fixture prints.

**The design's section 6.3 names three cases beyond Task 6.1's palette cases, and all three
are accounted for**: the **two that read a file other than `palette.css` are both in the file
now** — each arrived in the task that writes what it reads — and the third is *replaced*
rather than deferred:

- the **"vocabulary complete and identical in both sets"** case is *replaced*: under the
  owner's dark-only ruling there is one value set, so it would compare a set with itself.
  What catches what it was written for is here instead — the vocabulary is declared
  **exactly** (every token of the design's section 3.1 table is present, and no name
  outside it), no colour is spelled in `tokens.css`, and no step is spelled in
  `palette.css`. It is the one of the three that is still not in the file, and its reason is
  the ruling rather than a later task;
- the **`--border`-is-decorative scan** of `base.css` and `screens.css` was Task 6.4's,
  because both files are created later than Task 6.1 — the case landed in the task that
  writes what it reads, and it is in the file since Task 6.4;
- the **window-background comparison** against `consoleWindowOptions` was Task 6.5's, for
  the same reason: `backgroundColor` is a key that task added to `src/main/window.ts`, and
  the case reads the literal out of the options the window is built with — under the ruling
  one literal, `#0A0E16`, checked against the one `--ground`. It is in the file since
  Task 6.5.

## The wiring: the stylesheet is bundled, served and linked (Task 6.2)

Task 6.1 wrote the theme's tokens and palette and nothing read them. This task makes them a
file the console **serves** and every document **links**, and it is the SECURITY-SENSITIVE
one of the phase, because the thing it touches is the served root and the policy that
governs it. What it does, in four pieces:

| Where | What changed |
|---|---|
| `tools/bundle-renderer/index.ts` | a **stylesheet step**: `THEME_STYLESHEETS`, the refusals below, and `dist/bundle/console.css` |
| `src/main/protocol.ts` | **one** new `SERVED_FILES` entry, `/console.css` — six where there were five |
| `src/renderer/{index,installations,refusal}.html` | **one** `<link rel="stylesheet" href="./console.css">` in each `<head>`, beside the CSP `<meta>` |
| `package.json`, this file | `build.files` and the served-files list gain `dist/bundle/console.css` |

### One file, in cascade order, and the list is the directory's whole contents

The theme is a set of source files under `src/renderer/theme/` and the served root is a flat
allow-list, so they are emitted as **one** file. `THEME_STYLESHEETS` is the list, and
it is **the directory's whole contents**, which is what makes both of its refusals hold:

- a `.css` file in the theme directory that is **not** on the list is a `BundleRefusal`,
  so a stylesheet nobody reviewed cannot be served;
- a listed file that is **missing** is a `BundleRefusal`, so a rename fails the build
  rather than silently dropping a layer of the cascade — and, equally, so the list cannot
  run ahead of the files it names.

**That second refusal is why the list is introduced with the files the directory already
holds, and each later task appends its own.** A task that named a file before the file exists
would turn `npm run build` red the moment it merged, so `THEME_STYLESHEETS` names the files
Task 6.1 wrote — Task 6.2 introduces the list, and the bundler step with it — and each later
task appends the file it creates in the commit that creates it: every intermediate state is a
real build of the files that exist. `test/unit/bundle.test.ts` reads the list rather than
hard-coded names, so its case is true of the directory as it stands and stays true as each
task appends its file.

The step also refuses four things in any theme file, and the first is what makes the
other three hold: a **backslash**, an **`@import`**, a **quoted string that names a URL**
and a **remote `url()`**. The bundle is one file and the console is offline
(`connect-src 'none'`), so an import would either fail at runtime or be a request the CSP
forbids — and CSS reads `\` as the start of an **escape**, so `@\69mport "./base.css";` is
an `@import`, `u\72l(...)` is a `url(...)` and `"\68ttp://…"` is a remote URL. Every scan in
this tool is a scan and not a parse (the reason a *comment* that mentions `@import` is
refused for it), so none of them unescapes, and the escape itself is what is refused.
Neither theme file holds one, and a file that wants an escaped character writes the
character — a scan that tried to tell the harmless escapes from the load-bearing ones would
be the parser this tool does not have. `test/unit/bundle.test.ts` plants all four spellings
of an escaped rule, `content: "\2014"` among them, which is the case that makes the rule a
rule rather than a list of three shapes.

The quoted-string scan is separate from the `url()` one because `image-set()` and
`-webkit-image-set()` take a **bare string** as a URL, which is a remote load that never
writes `url(`: `image-set("https://cdn.example/x.png" 1x)` is a request the `url()` scan and
the at-rule scan both miss. Both scans read a target the same way — a scheme that leaves
this machine, or the protocol-relative `//` — and the `url()` scan runs first, so a quoted
`url('http://…')` is refused as the call that made the request. A quoted `<string>` may hold
the *other* mark inside it, because CSS allows it — `image-set("https://cdn.example/a'b.png"
1x)` is a valid URL — and the scan reads it as one.

The target test is **anchored at the start of the string**, which is what keeps the cost
narrow: a string that merely *mentions* a URL is not a URL, so `content: "see the design's
notes, https://…"` passes. What is refused is a string that *begins* as a URL and is not one
— `content: "Note: hello"` reads as the scheme `Note:` — and the fix is to reword the string.
That is the whole of the false-positive surface, and it is the direction the tool errs in
everywhere else.

Both scans read the target **as the URL parser would read it**, and not as the bytes are
written: ASCII tab and newline are removed wherever they are, and the leading and trailing C0
controls and spaces are stripped, before the scheme test. So `url(" https://…")` and
`image-set("\thttps://…" 1x)` are remote loads with the scheme behind a byte the test used to
require at character 0, and both are refused now. The refusal still quotes the call **as it is
written**, so the message names the bytes to change. One spelling is not reached and is stated
rather than solved: a raw LF *inside* a quoted string, which `QUOTED_STRING_RE`'s body cannot
cross — the section below carries the measurement that settles why the body stays bounded,
and the `url()` scan, whose quoted body does cross lines, refuses that spelling.

`url(./mark.png)` and `url(data:...)` are admitted — a path on this origin, and a byte
string rather than a request (`img-src 'self' data:` allows the second). Two asymmetries
are admitted with them and are stated rather than solved, because neither weakens anything:
`data:` is allowed by `img-src` and by **no** other directive, so an `@font-face
src: url(data:…)` would pass the build and be blocked when it is painted (`font-src 'self'`);
and `blob:` is admitted by the same reading of `REMOTE_SCHEME_RE` while **no** directive
allows it at all. Both are a silent failure mode — a rule that builds and does not paint —
and not a hole: the CSP refuses the load either way.

The `@import` scan is **ASCII case-insensitive**, matching `CSS_URL_RE` and
`REMOTE_SCHEME_RE` beside it and matching CSS itself, where at-rule names are
case-insensitive: `@IMPORT url("./base.css");` is the same rule, and it is the spelling that
would otherwise slip through — its target is relative, so the remote-URL refusal does not
see it, and the emitted `console.css` would carry an import at a path that is not in
`dist/bundle/` and drop that layer out of the cascade with nothing failing.
`test/unit/bundle.test.ts` plants `@import`, `@IMPORT` and `@Import`, and controls with the
word spelled without its `@`. The stray-`.css` refusal beside these is case-insensitive too:
`stray.CSS` is a stylesheet, and which checkout made the bundle should not decide whether
the list is the directory's whole contents.

**No CSS is parsed, minified, autoprefixed or rewritten.** The bytes on disk are the bytes
served, which is what makes `theme-tokens.test.ts`'s contrast arithmetic meaningful: it
reads the same literals the window paints. The only bytes added are comment banners — one
naming the build, one per layer so a reader can see the seam — and a layer that does not end
in a newline gains exactly one before the next layer's banner, so the seam is always a line
of its own (`test/unit/bundle.test.ts` plants an unterminated stylesheet for it; neither real
file reaches that arm).

**A listed stylesheet may not say in its own prose that nothing reads it.**
`test/unit/bundle.test.ts` scans every file `THEME_STYLESHEETS` names for the wording its
Task 6.1 stage used — `inert`, "nothing reads", "no task reads", "not read by anything",
"nothing links" — and fails on it. That is what rewrote `tokens.css`'s opening paragraph in
this task: the sentence was true of a tree where nothing read the directory and is false of
one where the bundler reads it, the served root serves it and every document links it. The
scan is over the **list** and not the directory, so a stylesheet the list does not name is
left to the stray-`.css` refusal that owns it, and the case is proved on a planted tree in
both directions.

### The CSP is unchanged, and the stylesheet is admitted by a directive that was always there

`style-src 'self'` is the **third directive** of `CONTENT_SECURITY_POLICY` and has been
since Task 1.1. A `<link rel="stylesheet">` to `app://console/console.css` is a stylesheet
from this origin, so `'self'` admits it. `default-src 'none'` is not consulted for it;
`font-src 'self'` is not consulted (no web font is loaded — the font stack opens with
`system-ui`); `connect-src 'none'` is not consulted (a `<link>` is not a fetch
`connect-src` governs, and no script fetches the CSS). **No directive is added, none is
loosened, and there is no inline style, no `<style>` element and no `'unsafe-inline'`
anywhere.** `mediaTypeFor` already mapped `.css`, so the protocol handler's change is the
`SERVED_FILES` entry and nothing else — **and that entry is a list the handler never
reads**: `resolveBundleRequest` serves any file under the root a URL names, deliberately
(`protocol.ts` says so itself: `SERVED_FILES` is "deliberately not a second filter"), so the
entry is the *test-held mirror* of `dist/bundle/` and not a runtime allow-list. The
enforcement is the directory's contents, which the build decides; the list is how a reader
and `test/unit/bundle.test.ts` see what the build decided.

Four things hold that, and each is a test rather than a promise:

- `csp-document.test.ts` reads `style-src` on its own and refuses `'unsafe-inline'`,
  `'unsafe-eval'`, `'unsafe-hashes'`, `*`, `http:` and `https:` in it; it asserts **one**
  `<link>` per document, `rel="stylesheet"`, `href="./console.css"`, with no `media`, no
  `onload`, no `integrity` and no `crossorigin`; and it serves the file through the real
  `serveAppRequest` and holds the response to `text/css; charset=utf-8`, `nosniff` and the
  CSP header;
- `window.test.ts` holds the whole policy string to the design's wording and
  `APP_SCHEME_PRIVILEGES` to having no `bypassCSP`, unchanged by this task;
- `screen-literals.test.ts` gains a scan over **every** module under `src/renderer`: a
  `.style` property write (computed keys included) and `setAttribute('style', …)` are
  offences with no vocabulary behind them, because `style-src 'self'` backs no inline style
  at all;
- `bundle.test.ts` holds `dist/bundle/` equal to `SERVED_FILES` in both directions, so a
  file that appears beside the bundle — or a name dropped from the list — fails rather
  than being served by a handler that would happily serve it.

### The mutations this task is proved against

Each was planted for real, run, and removed. The red step is the failure, not the diff.

| Mutation | What fails |
|---|---|
| a stray `.css` planted in `src/renderer/theme/` | `npm run build`, with `… holds stray.css, which THEME_STYLESHEETS does not list` |
| a listed stylesheet deleted from the theme directory | `npm run build`, with `THEME_STYLESHEETS names tokens.css, which … does not hold` |
| `@import` added to a theme file | `npm run build`, with `… holds an @import` |
| the same rule spelled `@IMPORT` | `bundle.test.ts`, on the case-insensitive scan — the mutant was planted (the `i` flag dropped) and the case observed failing |
| `url(https://…)` added to a theme file | `npm run build`, with `… loads url(https://…) from another origin` |
| `/console.css` dropped from `SERVED_FILES` | `bundle.test.ts`, on the directory equality |
| a `<link>`'s `href` changed in one document | `csp-document.test.ts`, on that document's one stylesheet |
| the `<link>` removed from one document | `csp-document.test.ts`, on the count |
| `'unsafe-inline'` added to `style-src` | `csp-document.test.ts` and `window.test.ts` |
| `style="…"` in a served document | `csp-document.test.ts`, on the inline-style scan |
| `el.style.color` written in `entry.ts` | `screen-literals.test.ts`, on the renderer-wide scan |
| `dist/bundle/console.css` removed from `build.files` | `build-files.test.ts` (`bundlerOutputs`) and `fuses.test.ts` |
| "Nothing reads this file yet" put back into a listed theme file | `bundle.test.ts`, on the unwired-claim pin — planted in `tokens.css` and observed failing, then reworded |
| the newline append removed from `stylesheetText` | `bundle.test.ts`, on the planted unterminated stylesheet — the mutant was planted (`source.css` without the `${…}\n` arm) and the case observed failing on the `a { color: red; }/* palette.css */` seam |

### The follow-up's own mutations (review of #694, 2026-10-19)

The independent review of the merged task left four minors and three nits, and each fix
landed with a case that was mutated first. Each mutant below was planted in
`tools/bundle-renderer/index.ts`, run, and reverted; in every run exactly the cases named
failed and no other case did.

| Mutation | What fails |
|---|---|
| the backslash refusal removed | `bundle.test.ts`, "refuses a backslash, which CSS reads as an escape" — the four escaped spellings, `@\69mport` first |
| the quoted-string refusal removed | `bundle.test.ts`, "refuses a quoted string that names a URL" — `image-set("https://…")` is admitted with it gone |
| the `isFile()` check on a listed theme file removed (`statSync` in its place) | `bundle.test.ts`, "refuses a listed theme file that is a symlink" |
| `buildConsole` put back to the old order (stylesheet before the foreign-file refusal) | `bundle.test.ts`, "refuses a foreign file in the served root before it writes anything" — the refusal still lands, but `console.css` is already there |
| `refuseSymlinkedOutput` dropped from either writer | `bundle.test.ts`, "refuses to write the stylesheet through a symlink" (and "…its bundle through a symlink") |
| `THEME_STYLESHEETS`' first two entries swapped | `bundle.test.ts`, "names the cascade in the design's order" — **and nothing else**, which is the finding: before that case, this mutant left the whole suite green |
| the stray-file extension test made case-sensitive again | `bundle.test.ts`, "refuses a .css the list does not name whatever its extension's case is" |

**A second pass over those fixes** (same review, 2026-10-19) closed two gaps the cases
above left, each reproduced against the compiled tool before it was fixed and mutated
after, and one overclaim in the prose:

| Mutation | What fails |
|---|---|
| the three `refuseSymlinkedDirectory` calls removed | `bundle.test.ts`, both "…into a symlinked directory" cases and the `buildConsole` half of "refuses a foreign file in the served root before it writes anything" |
| `QUOTED_STRING_RE`'s body narrowed to `[^"'\n]*` (excluding both marks, as it first shipped) | `bundle.test.ts`, "refuses a quoted string that names a URL" and the probe case — `image-set("https://…/a'b.png" 1x)` is admitted by the narrow body |

The directory half was the one that mattered: with the file check alone, `dist/bundle` as a
symlink to a directory outside the tree built **cleanly** and left `console.css` outside the
served root. Two of these shapes a case can only reach by planting the link itself, which is
what those cases do. A symlink *above* the output directory is still followed, and that is
stated in `refuseSymlinkedDirectory` rather than left to be discovered: resolving the whole
chain would refuse `/var`-style paths the platform ships (`os.tmpdir()` is under `/var` on
macOS), and `dist/` is gitignored, so this shape needs a tampered working tree rather than a
reviewed commit.

### The two leftovers #701 left open (2026-10-19)

The review of the follow-up above left two non-structural findings open, on the owner's
merge-then-follow-up rule. Each was reproduced against the compiled tool before it was
fixed, and each mutant below was planted in `tools/bundle-renderer/index.ts`, run, and
reverted; in every run exactly the cases named failed and no other case did.

| Mutation | What fails |
|---|---|
| the pre-write `refuseSymlinkedOutput` pass removed from `buildConsole` | `bundle.test.ts`, "refuses a symlinked output before it writes anything" — the refusal still lands inside `bundleRenderer`, with `console.css` already written, and for the document with `console.js` too |
| the pre-write `moduleClosure` pass removed from `buildConsole` | `bundle.test.ts`, "refuses a module the walk will not bundle before it writes anything" — `require('electron')` is refused one writer later, with `console.css` already written |
| `isRemoteTarget` put back to the raw `startsWith('//') \|\| REMOTE_SCHEME_RE` test | `bundle.test.ts`, "refuses a url() on another origin…", "refuses a quoted string that names a URL…" and the probe case — `url(" https://…")` and `image-set("\thttps://…" 1x)` are admitted |
| `URL_STRIPPED_RE`'s replace dropped, the edge strip kept | `bundle.test.ts`, the same two cases, on `url("/\t/x.png")` alone — the "tab and newline are removed *wherever* they are" half of the parser's rule |

**The one byte left open, and the measurement that settles it.** A raw LF *inside* a quoted
string is still not refused by the quoted-string scan, because `QUOTED_STRING_RE`'s body is
bounded by the newline. Widening the body to `[\s\S]` was tried, observed and reverted: it
buys `image-set("\nhttps://…" 1x)` — a `<bad-string-token>` the tokenizer drops before any
URL is resolved — and pays for it with `image-set('http://cdn.example/x.png' 1x)` and
`-webkit-image-set('//cdn.example/a"b.png' 2x)`, which both stop being refused the moment an
*unbalanced* quote precedes them in the same file. The theme files hold an apostrophe in
their prose, so those two cases failed on the widened body and passed on the narrow one. The
`url()` scan, whose quoted body is not bounded by the newline, refuses the LF spelling;
`bundle.test.ts`'s probe asserts both halves so the asymmetry is a decision rather than an
accident.

### What this task does not own

`base.css` and `screens.css` are Tasks 6.3 and 6.4, and each appends its own file to
`THEME_STYLESHEETS` in the commit that creates it. The golden frames are re-recorded by
Task 6.6, which is owner-run, and the goldens stay held until then — held, not unchanged.

**The frames have already moved, and this task is one of the things that moved them.** An
earlier draft of this paragraph offered "a stylesheet that paints nothing yet is not a
picture change" as the reason the goldens need no re-record before 6.6. That is false twice
over. The shipped `palette.css` declares `color-scheme: dark` on `:root`, and its own
comment says what that does: the picker and the refusal document — neither of which reads the
theme the entry chose — paint dark rather than in the platform's default, and the canvas's
own background follows it. A frame taken now differs from the same frame taken before this
task, before a single layout rule exists. And Task 6.3's rules and 6.4's screens move the
picture again, far more than this.

So the honest reason the goldens wait is 6.6's own: **every frame recorded before the last
stylesheet lands is a picture of an intermediate tree, and the next task invalidates it** —
which is exactly the regression 6.6 exists to stop, because a baseline re-recorded mid-phase
is a baseline nobody reviewed. What holds them is two things already in the tree, and neither
is this README: `playwright.config.ts`'s `updateSnapshots: 'none'` (`test/unit/playwright.test.ts`
pins it), so a run with no baseline **fails** rather than writing one, and each golden spec's
`test.skip(SKIP_GOLDENS, …)` on `FUGARO_CONSOLE_GOLDENS=skip`, which the staged
console-tests workflow sets. This task commits no golden PNG, and the ones that follow it
in this phase do not either: the darwin set PR #629 committed in the meantime is a picture
of the tree as this task found it — no stylesheet in the bundle — and is superseded rather
than kept ("The e2e goldens", above). Task 6.6 is where all eight are recorded at once, on
each platform.

This task re-records nothing, and no frame is compared or written by it: the golden tests
are among the ones the skip covers, and the eight baselines are Task 6.6's single recording
act.

The `--border`-is-decorative scan was Task 6.4's and the window-background comparison was
Task 6.5's, both for the reasons Task 6.1's section above gives, and both are in
`theme-tokens.test.ts` now.

## The layout, and the installation rail (Task 6.3)

Task 6.1 wrote the vocabulary and Task 6.2 made the stylesheet a file the console serves and
every document links; nothing painted yet. This task is `base.css`, the layer that paints
first: the reset, the document as a two-column grid, the rules for the
chrome, the focus ring, the responsive layers, and the chrome's `@media (forced-colors:
active)` block. It is also the task that **draws the installation rail** — the canvas's one
structural addition, which the owner approved on 2026-10-19.

| Where | What changed |
|---|---|
| `src/renderer/theme/base.css` | **new**: the reset, the grid, the chrome, focus, responsive, forced colors — 567 lines, no colour literal of its own |
| `src/renderer/entry.ts` | **the rail's elements**: twelve exported class hooks, `drawRail`, and the three places the dashboard tells it something (`setState`, `setSpend`) |
| `tools/bundle-renderer/index.ts` | `THEME_STYLESHEETS` gains `'base.css'`, in the commit that creates the file |
| `test/unit/theme-selectors.test.ts` | **new**: the §2-inventory selector scan over `base.css` |
| `test/e2e/entry.spec.ts` | the forced-colors smoke's chrome half, the rail's rows, and the body-children assertion the rail moves |
| `docs/design/console-styling.md` | §2.1's tables gain `data-collapsed` and `data-state`, and the banner row's order — hooks this task's rules select, added in the PR that wanted them |
| this file | the section you are reading |

### The document is a two-column grid, and the banner's row can be empty

`body` is `grid-template-columns: auto minmax(0, 1fr)` and `grid-template-rows: auto auto auto
minmax(0, 1fr)`. The five places are: row 1 spanning both columns for `section.console-skew`,
row 2 for the rail (column 1) and the installation line (column 2), row 3 for the nav, row 4
for the panel — the one row that takes what is left, and the one region that scrolls
(`overflow-y: auto`, with `min-height: 0` so a dense screen does not push the window open).

The banner is drawn only when the mode is not `ok`, so the trick is the **empty row**: an
`auto` row with no items sizes to nothing, and the four rows below keep their places whether
the banner is there or not. In **refused** mode the banner is the body's only child and takes
the whole window, which is `:only-child { grid-row: 1 / -1 }` — a structural pseudo-class,
not a hook invented for the occasion. The rail spans `2 / -1`, so it is full height under the
banner and full height without it.

**The rail's 216 px and 56 px are two literals and not tokens.** Like `CONSOLE_WINDOW_SIZE`'s
1180 x 780 they are facts about this window rather than values a screen reads, so no §3.1
table gains a row for them. The first column is `auto` and the rail carries its own width, so
the collapsed state is one rule on the rail rather than a second grid definition.

### Narrow windows: what a layer drops, and what it keeps announcing

Design §4.2's layers are written in `em` against the root size, so an operator who has raised
their default text size gets the narrow layout at the same measure of text rather than at the
same pixel width. This file carries the **chrome's** share of them — the nav wraps and the
installation line drops to project · mode at 48.75em, and the chrome's padding tightens at
35em; the tables' and the panels' share is `screens.css`'s and lands with Task 6.4.

**The one cell this task's layer takes out of the layout is the binary path, and it is taken
out of the layout only.** It is clipped to a 1 px box exactly as the rail's collapse clips its
spans (design §4.1) — never `display: none`, which would take it out of the accessibility tree
as well as out of sight. §4.2's rule for a dropped column ("a dropped column is one whose value
is repeated in a `title`") is read here as the same rule for a dropped line: a value this
console drops from sight is not a value it stops announcing. The first cut of this rule said
`display: none` and justified it with a `title` the entry does not write, which a review of the
branch caught: the path was dropped with nothing left to reach it, and the comment was the only
thing claiming otherwise.

### The rail: twelve hooks on thirteen elements, and the entry draws them

`aside.console-rail` holds a head (`console-rail-head`, with the `console-rail-title` and the
`console-rail-collapse` control), the list (`console-rail-list`, with `id="console-rail-list"`
for the collapse control's `aria-controls`), the foot row (`div.console-rail-add` and the
`button.console-rail-add` inside it — **one class, two elements**, which is why the count is
twelve hooks on thirteen elements) and the hint (`p.console-rail-hint`). A row is a
`button.console-rail-row` carrying `span.console-rail-name`, `span.console-rail-project`,
`span.console-rail-state` and `span.console-rail-spend`.

**The elements are drawn by `entry.ts` because the rules that select them are this task's.**
That is §2's licence rule read the other way: a hook nothing emits is not a hook, and a rule
written against a class a screen declares cannot be reviewed against a class nobody draws.
The same rule is why §2.1's tables gained `data-collapsed` and `data-state` **in this PR**:
the collapse rule selects the first and the state's tone selects the second, and a rule that
wants a hook adds it to the inventory first.

The row's state is the rail's own mark and not a field of any read: the rail knows whether
the console is reading this installation now, so it opens at `running` and goes `silent` when
the Live read was refused or the feed has gone quiet. The span carries the **glyph and the
word** (`● running`, `▲ silent`, `○ idle`) plus `data-state`, so the tone is a second channel
and never the only one (design §3.5) — which is also the whole of what §4.4's rule 1 asks for
when the system takes the colours away.

**The selected row's rule is here before the attribute is.**
`button.console-rail-row[aria-selected='true']` draws the raised surface and the 3 px
`--accent` left border, and the row reserves that border at the selected width
(`border-left: 3px solid transparent`) so selecting a row moves no text. The attribute itself
— with `role="tablist"`, `role="tab"` and the keys — was Task 6.5's, which is the design's
split: the border is this task's and the mark that turns it on arrived with the tablist
behaviour.

The collapse control is this task's, so its behaviour is too: it flips the rail's
`data-collapsed` — the one attribute the 216 px and 56 px rules are selected by — and keeps
`aria-expanded`, its own `aria-label` and its glyph in step with what it drew. The collapse is
**name-preserving**: `clip-path: inset(50%)` and a 1 px box rather than `display: none`, so
the row's accessible name is still the whole of what it draws and the state span keeps its
glyph visible while its word is clipped away. Nothing the collapsed rail removes from sight
is a fact it stops announcing.

### What the rail reads: nothing

`installation.current` is the only installation read this document has, it takes no argument
and names no id, and its answer is the installation the operator chose — so the rail draws
**one row per installation the dashboard can read, which is one row**, and a console with
nothing chosen or a store that could not be read draws **no row** rather than an invented
one. A row per *stored* installation would need a bounded list read on the dashboard's
bridge, which the design withholds (§4.1, §11) and this task adds no operation for.

So the rail makes **no read of its own**, and that is a test rather than a promise: the
entry spec's spawn-log case holds a healthy console to three kinds of spawn (`version`,
`watch --json --project P`, and the Live screen's rigor join `runs ls --json --project P`),
and a read at mount would be a fourth. The two figures the row
carries come from reads the console was already making:

- the **state** from the Live feed (`rail.setState`), which is the same read the panel draws;
- the **spend** from the console's own `budget.show` reads — the Spend and Projects paths —
  through `rail.setSpend`, so the rail shows `n/a` with the caps panel's own sentence
  (`no budget show document was read`) until one of those screens has been opened.

`setSpend` clears the span's `title` before it writes the reading. The rail is the one
drawing in this file that is **updated in place** rather than redrawn, and `putReading` writes
a `title` only for a reading with no value — so without that line the `n/a`'s reason would
sit under a figure that is now measured. It is a mutation in the table below.

### `+ Add installation` is drawn and not wired, and that is the honest state

The foot row is the design's control and it is drawn where the design draws it, with the label
the design gives it. The click is the one thing this task cannot add: `+ Add installation`
opens the add-installation flow, which is the **picker window**, and the dashboard cannot open
it. The picker is reached from the application menu's `Installations…` item (ruling C9), the
dashboard's bridge has fifteen operations and none of them opens a window
(`denyWindowOpen`), and both the design (§11: no Phase-6 task owns the preload or the IPC
allow-list) and this task's Files keep it that way. So the control is drawn with no handler
rather than with one that does nothing, and `drawRail`'s docstring says so at the place a
reader would look. **The console stays read only, and the CSP is unchanged**: no directive is
added, no `style` attribute is written anywhere, and `screen-literals.test.ts` refuses a
`.style` write under `src/renderer/` as it did before this task.

### The tests

- `test/unit/theme-selectors.test.ts` is the **§2-inventory selector scan**, and it reads the
  inventory **out of the design document** rather than keeping a second copy of §2's tables
  here: every backticked token of every table row in `docs/design/console-styling.md`'s §2 is
  a hook, every `.name` and `[name` in `base.css` (comments stripped, so the file's own long
  header is not read as a selector) is checked against it, and two further cases pin the rail
  at **twelve hooks and no thirteenth** and pin `SCANNED` against `THEME_STYLESHEETS` so a
  later task cannot add a stylesheet and leave it unscanned.
- `test/unit/entry-wiring.test.ts`'s rail cases: one row for the readable installation (name,
  project, `● running`, `data-state`, the `n/a` spend with its reason, and
  `calls.map(name) === ['subscribeLive']` — no read of the rail's own); **no row** for
  `{ok: false}`, for `{chosen: false}` and for an unreadable shape, with the rail itself still
  drawn; the collapse control's `aria-controls`, `aria-expanded` and label in both states; and
  the two ways the rail goes `silent` (a refused Live read, a stale feed).
- `test/unit/bundle.test.ts`'s body-children case now reads the rail first, and the planted
  stray stylesheet is `screens.css` — the file that is still unlisted, since `base.css` is
  listed now.
- `test/e2e/entry.spec.ts` gains the **forced-colors smoke's chrome half** and the rail's
  rows. **Neither could be run in this container**: Electron does not start here
  (`libglib-2.0.so.0: cannot open shared object file`), so every e2e case in the file fails at
  launch, this task's included. They are written and typechecked, their expected values are
  the ones the unit cases above already pin, and they are listed in the pull request as
  unverified. The smoke is a **smoke and not a golden**: forced-colors rendering is the
  platform's, and what is asserted is what §4.4 promises — the current view still marked, the
  chrome's boundary still a boundary (a width that is not `0px`, a colour that is not
  transparent), and the rail's rows read with them, word and glyph together. The screens' half
  is Task 6.4's, in each screen's own spec.

### The mutations this task is proved against

Each was planted for real, run, and removed; the failure is the red step.

| Mutation | What fails |
|---|---|
| `readable` ignores `chosen` (a row drawn for an unreadable installation) | `entry-wiring.test.ts`, the no-row case — 1 failed |
| the state span draws the word without its glyph | `entry-wiring.test.ts`, 3 cases |
| `setSpend` returns without writing (the rail never fills its spend) | `entry-wiring.test.ts`, the spend case — 1 failed |
| the feed's staleness never reaches the rail | `entry-wiring.test.ts`, the stale-feed case — 1 failed |
| the collapse control stops announcing (`aria-expanded`/label left alone) | `entry-wiring.test.ts`, the collapse case — 1 failed |
| the stale `title` is left on the spend span | `entry-wiring.test.ts`, the spend case — 1 failed |
| `.console-rail-badge` added to `base.css` | `theme-selectors.test.ts`, the unknown-class case — 2 failed |
| `[data-badge]` selected in `base.css` | `theme-selectors.test.ts`, the unknown-attribute case — 1 failed |
| a thirteenth `console-rail-*` hook in `base.css` | `theme-selectors.test.ts`, the twelve-and-no-thirteenth case — 2 failed |
| `span.console-rail-project` dropped from §2.1's table | `theme-selectors.test.ts`, 2 cases |
| `console-skew` dropped from §2.1's table | `theme-selectors.test.ts`, the unknown-class case — 1 failed |
| every `data-collapsed` in §2 renamed | `theme-selectors.test.ts`, 2 cases |
| `base.css` left unlisted in `THEME_STYLESHEETS` | `npm run build`, with `… holds base.css, which THEME_STYLESHEETS does not list` — observed while this task was being written |
| the stray-`.css` case still planting `base.css` | `bundle.test.ts`, on the planted file — it is listed now, so the case was moved to `screens.css` |
| the newline-append case destructuring exactly two names | `bundle.test.ts`, on `screens.css` — the case is generic over the list now |

### What this task does not own

`screens.css` is Task 6.4's, and the rows' and badges' `@media (forced-colors: active)` block
with it — each stylesheet carries the block for the rules it writes. The `--border`-is-
decorative scan was 6.4's, the rows' `role`/`aria-selected`, the keys and the
window-background comparison were 6.5's — all three are in the tree since it — and the
eight golden frames are 6.6's, which is owner-run. **The goldens are held, not unchanged**: this task's layout is exactly the kind of
change 6.6 exists to record once, at the end, rather than mid-phase.

## The screens (Task 6.4)

Task 6.3 drew the layout and the rail, and nothing had yet styled a screen: the four screens,
the degraded and unavailable screens, the refusal document and the picker were drawing into a
document whose reset, grid and chrome were painted and whose contents were not. This task is
the last layer, `src/renderer/theme/screens.css`, plus the **one code change the
whole styling work makes** — the fifth number hook.

| Where | What changed |
|---|---|
| `src/renderer/theme/screens.css` | **new**: the per-screen rules, in the design's §2 order, ending with the rows' and badges' forced-colors block |
| `src/renderer/screens/degraded.ts` | `numberCell` prepends `degraded-num`, which is the class §2.10 says this task adds |
| `tools/bundle-renderer/index.ts` | `THEME_STYLESHEETS` gains `'screens.css'`, in the commit that creates the file — the list is now the theme directory's whole contents and stays still |
| `test/unit/theme-selectors.test.ts` | the scan covers `screens.css`, and reads §2 **whole** rather than its tables; two more cases check every element-qualified selector against the tags the console draws, and every attribute-qualified one against the attributes it sets |
| `test/unit/theme-tokens.test.ts` | the `--border`-is-decorative scan of `base.css` and `screens.css`, and the case that pins `--ok` to the readings §3.5 rule 4 licenses |
| `test/unit/degraded.test.ts` | every figure the degraded and unavailable screens draw carries `degraded-num`, and no cell that draws a document string does |
| `test/unit/bundle.test.ts` | the planted-tree case's unlisted file moves out of the theme directory, because after this task there is no unlisted file in it |
| `test/e2e/{live,spend,projects,run,degraded}.spec.ts` | the forced-colors smoke's per-screen half |
| this file | the section you are reading |

### The five number hooks, and the one that did not exist

Design §2.10 is the licence for the only code change here: Live, Spend, Projects and Run each
have a `numberCell` that prepends `live-num`, `spend-num`, `projects-number` or `run-number`,
and the degraded screen's prepended **nothing** — so its feed age, its counts and its money
were the only figures in the console with no tabular-figures and no right-align hook. It
prepends `degraded-num` now, `screens.css` carries one rule for all five, and two cases in
`degraded.test.ts` fail if the prepend is removed (one of them reads `span`s only, because
§2.7 gives the `ol` region and the count span the same class).

### The selector scan reads §2 whole, and expands its shorthand

`theme-selectors.test.ts` was narrowed to §2's **tables** in Task 6.3, which is where every
hook of the four screens and of the chrome is listed. §2.7 and §2.8 have **no tables at all**
and name the degraded, unavailable and refusal hooks in their prose, and §2.4's `data-*` list
is a prose line under its table — so a scan left as it was would refuse `section.degraded`,
`div.unavailable-body` and `[data-panel]` while claiming the design does not name hooks it
names two paragraphs further down. The reading is widened, not loosened: `HOOK` keeps only
tokens shaped like a hook, and `NOT_A_LICENCE` keeps out the two attributes §2 names **in
order to refuse them** (`data-hidden`, `data-skew`).

§2.7 also abbreviates: `(degraded-run-id, -repo, -status, …, -total)` and `degraded-since` +
`-word`/`-at`/`-age`. Two conventions in one paragraph — the suffix replaces the last segment
in the first and is appended in the second — so the scan expands **both** readings. A licence
that names a hook too many is harmless; one that names a hook too few refuses a rule the
design asked for.

**`section.run` is deliberately not in `screens.css`.** §2.6's table starts at `run-header`
and names no class for the Run screen's root, so a rule spelled `section.run` would select a
hook §2 does not name — and the licence is the licence. Nothing is lost: the reset gives a
`section` its own block box and `base.css` sets the document's colour.

### The tags the rules name, and the eleven that named the wrong one

§2's licence is about hooks and says nothing about the element in front of one, and §2.8 is
where the design warns what that costs: "**The tags are the code's and they matter** … a rule
spelled `pre.unavailable-install-command` styles nothing at all". A rule with the right class
and the wrong tag passes the selector scan, because the class *is* a hook §2 names — and then
matches nothing.

Eleven rules here were written that way, and the tag scan is what found them. It reads the
`tag.class` pairs out of the code that draws them — the four emitter helpers (`make`, `cell`,
`textCell`, `numberCell`) with their spread class lists, the raw `factory.create` +
`setAttribute('class', …)` shape of `entry.ts` (a literal, a module constant such as
`RAIL_ROW_CLASS`, or the class a local helper was handed at its call sites), and the two roots
`degraded.ts` picks with a conditional class — and refuses a selector whose pair is not among
them. `src/shared/version.ts`'s skew section is read too, because `base.css` selects it.

What the eleven were: `p.run-halt-detail` (a `span`), `p.unavailable-searched` (a `div`, as
§2.8 spells it), `span.projects-enrolled-repo`/`-seen-repo` and `span.run-log-tail-line`
(`li`s), `span.run-report-path`/`span.run-comments-path` (the containers are `p`s — the
`-value` hooks their rules meant already have their own mono rule), `span.run-id` (an `h1`),
and `span.run-ledger-model`/`-compute`/`-total` (the labels are `p`s, in both the ledger rule
and the forced-colors list). Each kept its rule's intent: the tail line moved beside
`run-log-message`, its cousin in the log list, and the two diagnosis containers moved to the
prose rule.

### The attributes that were on another element, and a tone that could not be right

The tag scan has a sibling, and the review that found the eleven tags is why it exists: a rule
may name a class the code draws **and** an attribute §2 lists, and still select nothing,
because the attribute is on a **different element**. The new case reads which attributes the
code sets on which elements — walking each file in source order, because a screen reuses
`element` in every function it has — and refuses a compound selector whose class does not
carry the attribute it qualifies the class with. It found four rules:

- `span.spend-partial[data-partial='true']` — §2.4 puts `data-partial` (and `data-hollow`) on
  `li.spend-point`, `li.spend-row` and `div.spend-totals`; the marker span inside them holds
  the text `(partial)` and no attribute, so a partial day never got the `--warn` tone the
  rule intended. The tone is on the three elements that know it now.
- `span.spend-persons-state[data-state='off']` — the knob writes `on`/`off` as the span's
  **text** and `data-enabled` on the `div.spend-persons` (§2.4's "person view withheld"), so
  the span had no rule at all. It carries the type now and the parent's rule carries the tone.
- `div.projects-kill-row[data-killed='true']`, in the rule and in its forced-colors entry —
  the attribute is the **banner's** (§2.5's `data-*` line and §3.5's states table: "the
  attribute is on the banner, and on Live's repository block itself"), so the row's own mark
  was never drawn. The row is selected by `div.projects-kill-row:has(div.projects-kill)`,
  which is the one selector that can see the banner inside.
- `button.console-rail-row[aria-selected='true']` is the **fourth**, and it is deliberate:
  `base.css`'s header records Task 6.3 writing the design's own rule (§4.1) before the
  attribute §2.1 handed to Task 6.5. **Task 6.5 arrived and emptied the list**: the tablist
  behaviour writes `aria-selected`, so the case reads the write off the code like any other
  compound selector's, and `AHEAD_OF_THE_CODE` is an empty list rather than a one-entry one.
  An entry there would be a rule ahead of its attribute, which is this design's own split,
  and a second one would be a rule nothing emits.

**And one tone.** `span.run-stage-passed` was painted `--ok` for both of the stage's words:
the class holds `passed` **or** `failed` with no attribute (§2.6), so the rule read a failed
stage green — and `--ok` is reserved for measured-and-inside-cap and the rail's running
(§3.5 rule 4). No tone can be right for a class that carries two states, so the stage's name,
word and deadline are labels; the failure's alarm is the diagnosis panel's `run-failed-word`,
the element that knows which it is. A new case in `theme-tokens.test.ts` pins `--ok` to its
two licensed readings (a `measured` sparkline bar, the rail's `running` row), which is what
that mistake would have to get past.

**That case held a third entry, and the follow-up to this task removed it.** The list of
licensed selectors carried `section.projects-panel[data-state='read']` — the rule that painted
a read Projects panel's left border `--ok` — so the case whose whole purpose is to keep `--ok`
to the readings §3.5 rule 4 names accepted a reading the design licenses nowhere: a read panel
is not "measured and inside the cap" and is not the rail's `running` row, and §6.1 rule 3
forbids the tone decoratively. The list had been transcribed from the stylesheet rather than
read off the design, which is exactly how a guard stops guarding. Both ends moved: the panel's
rule draws `--accent` (a rule is **structure**, which is §3.2's role cell for that token, and
§4.1 draws the rail's selected row's own 3 px left border in it), and the entry left the list —
so the list and the stylesheet still hold each other in both directions, and a reading added
to §3.5 rule 4 means amending the design in the same edit and adding its selector here.

**The ink token has the mirror case, added with the same follow-up.** `--accent-ink` is the
accent **as text** (§3.2's role cell: "links, an accent heading") and `--accent` is the accent
as structure, so a rule that paints a border, an outline or a fill with the ink token has used
the text tone for structure — which `li.live-run-queued`'s left border did, and which is why
it draws `--accent` now. The case reads every declaration of `base.css` and `screens.css`
rather than a list of selectors, so a border added later is caught without anyone remembering
to name it, and it is proved against a planted `--accent-ink` border that is refused and the
same border in `--accent` that is not.

### The `--border`-is-decorative scan, and the clause that had to be a box

Design §6.3 places this case in this task because it reads `base.css` and `screens.css`, and
both exist now. Three clauses: `--border` in an `outline`; `--border` at any width other than
`--rule-width` (§3.4: "1 px hairlines only"); and `--border` drawing a **box** on a control,
whose extent is `--border-strong`.

The third clause is a box and not any border, and the rail is why: its rows are
`button.console-rail-row` and the design draws the hairline **between** them with
`border-bottom` on that button. Read as "no border on a control", the scan would refuse the
rail Task 6.3 drew and the design specifies. A one-sided border is a rule; only an all-sides
one is an extent.

### Two figures the canvas draws that a stylesheet cannot compute

The cap bar's fill (`data-fill`, `"12.5%"` or `"n/a"`) and a sparkline bar's height
(`data-height`) are **strings in a `data-*` attribute**: CSS cannot read one as a length, and
the console writes no inline style (`style-src 'self'`, and `screen-literals.test.ts` refuses
a `.style` write anywhere under `src/renderer/`). So the cap is drawn as its percentage with a
state-coloured rule beneath it — the number and the tone, which is the two channels §3.5 rule
3 requires of exactly that reading — and the burn window is drawn as a row of ticks whose tone
says measured or gap, with the rate in each bar's `title`. Neither figure is dropped and
neither is invented; what is lost is the *length* of a bar, which is a drawing the code does
not publish as a number a stylesheet could use.

### Narrow windows: what the screens' layer does, and the rule that outranks it

§4.2's first row would have the widest tables drop their least load-bearing columns. **This
file wraps instead, and says why**: a dropped column is allowed only when its value is
repeated in a `title`, and the cells a Log, Jobs or `spend-rows` table would drop — tokens in
and out, a job's model and compute — carry a value with no `title` behind it, because
`putReading` writes one only for a reading with no number. Hiding them would be the console
lying about spend, which §6.1 rule 1 forbids more strongly than any layout does. What the
layer does do is §4.2's own: the three-column figure groups become two at 48.75em and stack at
35em, and the burn window narrows at 62.5em.

### What this task does not own

The eight golden frames are Task 6.6's, which is owner-run and records once at the end of the
phase; the computed-background assertion belongs with that recording, for the reason Task 6.3
gives. The rail's `role`/`aria-selected`, the keys and the window background were Task 6.5's,
and all three are in the tree since it.

**The five forced-colors cases are written but not run in the container this task was
implemented in**: Playwright's Electron launch needs a display and a GTK/NSS/glib stack the
image does not have. `npm run test:unit` is green there and `tsc --noEmit` typechecks the
specs; the e2e suite is the one verification this branch could not complete, and the pull
request says so.

## The window's ground, and the keyboard (Task 6.5)

Task 6.4 was the last stylesheet, so this task paints nothing: it is the two things the
design's section 4.1 asks of the **code** — the colour the window paints *before* the
document does, and the keyboard the canvas's two tablists and the rail's shortcut keys need.
It is also the task the owner's Q1 ruling of 2026-10-19 lands in, which is why what it adds
is smaller than the task it was written as: **one** value set ships, dark, so `CONSOLE_THEME`
stays the literal `'dark'`, no `matchMedia` read and no `nativeTheme` query comes with it, and
the window paints **one** background literal against the one `--ground`.

| Where | What changed |
|---|---|
| `src/main/window.ts` | **new**: `CONSOLE_WINDOW_BACKGROUND` (`#0A0E16`) and the `backgroundColor` key in `consoleWindowOptions` |
| `src/renderer/entry.ts` | **the keyboard**: the view navigation and the rail's list as tablists (`role`, `aria-selected`, a roving `tabindex`, each on its own axis, Home and End, Enter on the rail, the focus following the move), `isMacPlatform`, and the rail's shortcut keys on the document |
| `src/renderer/theme/base.css` | the header's account of the selected row's rule, which now selects an attribute the code writes |
| `test/unit/theme-tokens.test.ts` | three cases: the window background against `consoleWindowOptions`, the focus ring's one `:focus-visible` rule, and the collapsed rail's names |
| `test/unit/entry-wiring.test.ts`, `test/support/fakeDom.ts` | the keyboard's cases, and the fake's `press`/`focus` and its two key fields |
| `test/unit/theme-selectors.test.ts` | `drawnAttributes` reads a tablist's write made in a loop over a container the drawing filled — which is what retires `AHEAD_OF_THE_CODE`'s one entry |
| `test/unit/window.test.ts` | the options list gains the one ground literal |
| `docs/coordinator/reconciliation.md` | the two `window.ts` citations follow the line they name |
| this file | the section you are reading |

### The window paints the ground before the document draws, and the duplicate is a case

`backgroundColor` is what the platform fills the window with in the frame between the window
existing and the stylesheet's first paint, so a console without it flashes the platform's
default white on every launch — a defect no screenshot of a **painted** window can see, which
is why the guard is the only place it can be. The value is `--ground`, and it is written out
in `src/main/window.ts` **as a literal rather than read from the stylesheet**: the main
process has no way to resolve a custom property, so the equality is held by a test instead of
by the compiler — `theme-tokens.test.ts` reads the literal **out of the options the window is
built with** (not out of the exported constant, so a `backgroundColor` dropped from
`consoleWindowOptions` fails the same case as one that drifted) and fails by name when the
two differ. One literal and one direction, because one set ships: the two-set design's second
literal and its `nativeTheme` read are the record of the question the ruling answered.

### The keyboard: two tablists, and the shortcut keys are the document's

The design's section 4.3 gives the WAI-ARIA tablist pattern to the view navigation **and** to
the rail's list (section 4.1), and they are two tablists with two axes: the navigation is a
row of buttons driven with Left and Right, the rail's list is a column of rows driven with
Arrow Up and Arrow Down, and Home and End are on both. The arrows move the focus **and** the
roving `tabindex` together, and the rail's Enter selects the row — the pattern's
manual-activation form, since what a selection means on this rail is its own mark rather than
a read the document could make. `tabStop` is one function both tablists write through,
because the rail's list holds one row today: a rail whose write stopped roving would still
draw exactly one tab stop, so the rule is held where a case can drive it rather than where it
would be unobservable.

The rail's **shortcut keys** are the document's and not the row's, which is what section 4.3
means by "without walking the tab order at all": the key reaches a row from wherever the
operator is standing. The design grants **Command-1 to Command-3** on macOS and
**Control-1 to Control-3** elsewhere — the first three rows the rail draws, which is the
hint's own sentence — so the mapping answers `null` above the third (and the key is then
neither selected nor prevented: the window keeps it), answers `null` for the modifier the
platform does not use, and reads the **physical** key (`event.code`): on AZERTY the unshifted
`1` key produces `&`, and a mapping that read the character the layout produces would do
nothing to the very keystroke the hint asks that operator for. `Numpad1` is a different
physical key and is not a second spelling of `Digit1` — the design grants the number row and
names no keypad key.

### The mutations this task is proved against

Each was planted in the shipped code, run, and removed; the failure is the red step. The first
three are Task 6.5's own and the last three were planted by the follow-up of 2026-10-20, which
is the PR that moved the mapping onto the physical key and bounded it to three rows.

| Mutation | What fails |
|---|---|
| `CONSOLE_WINDOW_BACKGROUND` drifted from `--ground` (`#FFFFFF`) | `theme-tokens.test.ts`, the window-background case, and `window.test.ts`'s options case — 2 failed |
| `backgroundColor` dropped from `consoleWindowOptions` | the same two cases — 2 failed |
| `tabStop` answers `'0'` for both states | `entry-wiring.test.ts`, 3 cases — the navigation's two and the rule's own |
| `shortcutRow` reads `event.key` again, so the layout decides | `entry-wiring.test.ts`, the physical-key case and the rail's wiring case — 2 failed |
| the three-row bound dropped, so `Digit4` to `Digit9` select again | `entry-wiring.test.ts`, 3 cases — the three-row case, the physical-key case and the rail's wiring case |
| `Numpad` admitted as a second spelling of the number row | `entry-wiring.test.ts`, the physical-key case and the rail's wiring case — 2 failed |

### What this task does not own

The eight golden frames are Task 6.6's, which is owner-run and records once at the end of the
phase — **and this task moved the picture once**: `base.css` has drawn the selected rail row's
raised surface and 3 px `--accent` border since Task 6.3, and the `aria-selected` that turns
them on is written here, so a frame taken now differs from the same frame taken before it. The
reason the goldens wait is still Task 6.6's own. The theme switch this task was named for is
**not** deferred work: the owner's Q1 ruling removed it, and a later task that wants it needs
a light set to switch to and a new ruling to ship it (plan Task 6.5's record, design section
3.6). No token, no rule and no served file is this
task's: `THEME_STYLESHEETS`, `SERVED_FILES` and the CSP are exactly what Task 6.4 left, and
`ElementLike` needed no widening — `role`, `aria-*` and `tabindex` are attributes, and
`event.code` is a read of an event rather than a write to the DOM.

**The e2e suite was not run in the container this task was implemented in** (Playwright's
Electron launch needs a display and a GTK/NSS/glib stack the image does not have);
`npm run test:unit` and `tsc --noEmit` are the two verifications that ran, and the pull
request says so.

## The application icon: the website's mark (the owner's brief of 2026-10-19)

The console's icon is the **website's own mark**, not a second drawing of it, and there is
one file on disk rather than two to keep in step: `console/build/icon.png` is
byte-for-byte `brand/app-icon-macos-1024.png`, the macOS app-icon master that
`scripts/gen-brand-icons.py` writes from `brand/fugaro-logo.png` — the file
`brand/README.md` and `website/README.md` both name as the source of truth for every icon
the site serves. Its body is the composition of the website's own home-screen icon,
`website/public/android-chrome-512x512.png` — the artwork cropped to its alpha bounding box,
centred at 80 % extent on the opaque ground the generator paints (`--ground` of the palette
in force when it last ran) — so the console and a visitor's browser tab are showing the same
picture.

**The shape is macOS's (the owner's report of 2026-10-30).** Until then the file was the
opaque square `brand/app-icon-1024.png`, and the Dock showed it as a square beside every
rounded system icon: macOS draws an app's icon exactly as the file is and does not mask it.
The macOS master follows Apple's icon template since Big Sur: a 1024 transparent canvas, the
body an **824 px squircle** centred in a **100 px margin**, and a soft black shadow inside
the margin. The outline is the superellipse |x|^n + |y|^n = 1 with n = 5.1, the
least-squares fit to the system apps' icons on macOS 26 (Notes, Calculator; within 2.4 px
RMS), and the shadow (10 px down, Gaussian sigma 16, 20 % black) is measured off the same
icons. The same file is the Linux icon (`build.linux.icon`), where a rounded icon on a
transparent ground is as usual as a square one.

### 1024, and the artwork's real size

**The 1024 is the size the packager needs, and since 2026-10-20 it is also a size the
source holds.** The owner replaced `brand/fugaro-logo.png` with a 2048 x 2048 canvas
carrying the same picture at twice the real pixels, and the mark grew with the canvas: the
alpha bounding box of the file (alpha > 8, the generator's own `ALPHA_THRESHOLD`) is
**1089 x 738 pixels**, with the rest transparent margin. `scripts/gen-brand-icons.py` crops
to that box and then scales it per output, so the generator's own
`fit(art, 824, 0.80, ground)` for the macOS body — factor **0.6053**, artwork **659 x 447**
— *down*scales it now, and the site's `android-chrome-512x512.png` (factor 0.3761, artwork
410 x 278) is the same artwork downscaled further. `brand/app-icon-macos-1024.png` is
therefore a native-resolution rendering of the mark rather than an interpolation of a
smaller one, and the console's icon inherits exactly that.

**This section used to ask for that change, and the figures above are the answer to it.**
Until 2026-10-20 the source was a 1024 x 1024 canvas whose mark was only **548 x 371 px**,
so the same call scaled it *up* by 1.4949 and this file was an ~1.5x interpolation; the
section ended by naming what would fix it — "artwork that holds it: a
`brand/fugaro-logo.png` whose visible mark is about 1024 px across" — and the owner supplied
exactly that. An earlier revision of this section also said the master was "a genuine 1024
render" and that "nothing here is an upscale": that was wrong for the file it described
(the mark was 548 px across) and it is true of the one in the tree now (1089 px across, so
the 819 px the master draws is a reduction). Both readings are the measurement above rather
than a claim read off a filename.

The **file choice stands**, for two reasons that never depended on the artwork's size:

- **the packager wants 1024.** electron-builder converts the PNG to the bundle's `.icns`
  and refuses anything under 512 for that format; the `.icns` carries every slot from 1024
  down to 16, and each one below 512 is a downscale of this file. Converting the site's 512
  instead would leave the 1024 slot filled by a second resampler's guess at the file the
  generator already wrote;
- **one source of truth.** The site's 512 and this 1024 come from one artwork, one ground
  and one generator call; resampling the 512 up here would be a *second* derivation of the
  mark in the console's tree, which is the thing this task exists to avoid.

**A source change is still the only thing that moves this**, and it is a one-liner when it
happens: replace `brand/fugaro-logo.png`, then
`python3 -I scripts/gen-brand-icons.py && cp brand/app-icon-macos-1024.png console/build/icon.png`,
which is the whole change — `website/test/brand.mjs` and `test/unit/icon.test.ts` are what
check it. Nothing here smooths the 512 up and calls it a 1024.

`test/unit/icon.test.ts` asserts the size, the alpha channel (colour type 6), the template's
shape (transparent canvas corners and margin, rounded body corners, an opaque centre, the
body's bounding box exactly 824 px at 100) and the bytes; the PR records the same figures.

### Where the packager takes it, and where a running console does

- **The bundle.** `build.mac.icon` in `console/package.json` names
  `build/icon.png` — the string `CONSOLE_ICON_RELATIVE_PATH` writes once, in
  `src/main/icon.ts` — and electron-builder converts the 1024 PNG to the bundle's `.icns`
  on the macOS release runner (`docs/pending-workflows/console-release.yml` runs
  `npm run package` on `macos-15`). **No `.icns` is committed**: it is a derived binary
  only the packing step needs, and committing one would be a second copy of the artwork to
  regenerate by hand. The icon is deliberately *not* in `build.files`: it goes into the
  bundle as the application's icon, not into the asar as an asset, and `fuses.test.ts`'s
  exact-list test still holds every pattern there to `.js`, `.html` or `.css`.
- **The dock, while unpackaged.** `npm run build && npx electron .` — what a developer
  does, and what `test/e2e/` launches — has no bundle, so the dock would show Electron's
  own icon. `installDevDockIcon` (`src/main/icon.ts`) gives the process the console's icon
  through `app.dock.setIcon`, and `src/main/index.ts` calls it as the first thing `main()`
  does, before any window exists. It answers `false` — and touches nothing — in three
  cases, each a real one rather than a defensive check: not macOS (`app.dock` is
  `undefined`), already packaged (the bundle carries the icon), and no dock handed in.
- **`BrowserWindow`'s `icon` option is not used, and that is deliberate.** macOS ignores
  it: a window's chrome and the dock take the *application's* icon, which on macOS is the
  bundle's. A key that does nothing on the only platform this console runs on would be
  worse than absent, and pointing it at a file the packaged app does not carry is exactly
  the broken pointer the `<link>` rule in this document refuses. The dev-time dock icon is
  macOS's own door for the same job.

### Refresh, when the mark changes

The copy is a copy, so a regenerated brand master — the generator paints the ground colour
the palette in force gives it, so re-running it after a palette change rewrites every icon,
and the committed ones' ground is in any case older than `tokens.css`'s current `--ground` —
leaves the console's icon stale **until the equality test says so**:

```sh
python3 -I scripts/gen-brand-icons.py && cp brand/app-icon-macos-1024.png console/build/icon.png
```

That is the whole sync. It is a byte copy rather than a build step on purpose: electron's
packing runs on a macOS runner that has no Pillow, `build/icon.png` is a committed file the
dev-time dock reads from the checkout, and a derivation script would be a second
implementation of the generator's own decision to keep in step.

### The tests, and the mutations each one was run against

`test/unit/icon.test.ts` (10 tests), and every one of them was seen red against a planted
change before it was committed:

| Claim | Mutation it was run against |
|---|---|
| the bytes are the master's | a 1024 x 1024 RGB PNG of the ground colour alone planted as `build/icon.png` — "is the app-icon master, byte for byte" red |
| the picture is the site's | the same planted file: "is the picture the website serves…" red, naming block 421 at 218.06 against a tolerance of 8. The site's *maskable* icon, a different composition of the same mark, measures 218.87 the same way; the body of the macOS master against the site's 512 measures **3.65** over its 208 opaque blocks, which is what the tolerance is set from (see the test) |
| it is a 1024 PNG with alpha | the site's 512 icon planted in its place — the size assertion red ("expected 512 to be 1024"); the opaque square `brand/app-icon-1024.png` planted — the colour type red (2, not 6) |
| it has the macOS shape | the same opaque square planted: "has the macOS icon shape…" red on the first transparent canvas corner; it fails "is the picture the website serves…" too, because its body is at the wrong extent |
| the packaging config names it | `build.mac.icon` pointed at `build/icon.icns` — "is the icon the packaging config names…" red |
| the dock icon is set on macOS, unpackaged | `deps.packaged` dropped from `installDevDockIcon`'s guard — "does nothing once packaged" red; `deps.platform` dropped — "does nothing off macOS" red |
| the entry point wires it | `packaged: app.isPackaged` in `src/main/index.ts` replaced with `packaged: true` — "is the icon the entry point actually installs" red |

The last row is a source scan, the same shape as `startup.test.ts`'s: `src/main/index.ts`
imports Electron's *runtime* and cannot be imported under vitest, so "the entry point
installs it" is read off the file's text.

### What this task does not own

Nothing under `.github/workflows/` — the release workflow is staged in
`docs/pending-workflows/` and is the owner's to copy in, so the PNG-to-`.icns` conversion
is asserted as *what that file's `npm run package` will do*, not as something this branch
ran. The mark itself: the macOS master puts the website's composition on Apple's body
unchanged, and it is not redrawn or recoloured. (This paragraph used to say macOS draws its
own rounded-rect mask over a full-bleed icon; it does not, and the square the owner saw in
the Dock on 2026-10-30 is the evidence.) And any signed or notarised build: unchanged from Task 4.1's record
above.

## The first run asks for nothing (the owner's ruling of 2026-10-20)

The console's first run used to open a form: a project name, **Choose binary…**,
**Choose config…**, **Choose checkout…**, **Add**. The owner ruled it out on
2026-10-20, and the ruling is the whole of this work: *"The console should never
ask the user the binary, the config. All of this is purely discoverable
automatically. Just use the Fugaro binary that's installed on the system. … the
console should just list all the installations / projects that it finds locally.
As for the repo, that should be optional: we ask for it only for the stuff that
we need. … If we don't find the Fugaro binary, or there's no installation that we
can find locally, we should have a nice page directing the user to the website to
help them get started."*

So the picker window is a **list and two screens**, and the operator is asked for
exactly one thing, once, and only where it is genuinely needed: a checkout, at the
moment a row offers one (rule 7's pairing).

| Where | What changed |
|---|---|
| `src/main/installlist.ts` | **new**: the reader for `fugaro config list --json` — `MAX_DISCOVERED_INSTALLATIONS` (256), `MAX_WARNING_LINES` (64), `MAX_DOCUMENT_STRING` (4096), a tolerant `checkouts` array whose length is nonetheless bounded (a row past 256 checkouts refuses the document rather than drawing a shortened one), and the CLI's own `warnings` lines carried through: every line the CLI printed is drawn, and past `MAX_WARNING_LINES` the console appends one line saying how many it did not keep, so the bound is visible rather than a silent omission |
| `src/main/onboarding.ts` | **new**: the two screens' facts — `FUGARO_INSTALL_COMMAND` (re-exported from `src/source/cli.ts`), `FUGARO_SETUP_COMMAND`, `GETTING_STARTED_URL`, `installCliState`, `firstInstallationState`, `NO_ONBOARDING` |
| `src/main/picker.ts` | the registry is **loaded** (`registry.load()`: without it a store full of installations drew as an empty list, `refusal` was never drawn, and the first-run screen opened over an installation the operator already had), the rows are the CLI's list merged with the store's records, `listReply` gates the first-installation screen on `rows.length === 0`, and the checkouts a row offers are the ones the CLI printed |
| `src/main/installations.ts` | the surface is **five** rows; a row's subject is a project name or a directory out of that installation's `checkouts`; `createPickTokens`, `MAX_OUTSTANDING_PICKS`, the `add`/`pickBinary`/`pickConfig`/`pickCheckout` rows, the `externalUrl` argument kind and the one `dialog.showOpenDialog` call are gone |
| `src/main/spawn.ts` | `config list` joins `COMMANDS_WITHOUT_PROJECT` and the spawn table; `PICKER_COMMANDS` is the picker's own one read |
| `src/main/window.ts`, `src/source/cli.ts` | the external-link path is the one `shell.openExternal` door (`validateExternalUrl`, `ALLOWED_EXTERNAL_HOSTS`), and the install command the source module already carried is re-exported rather than spelled twice |
| `src/renderer/installations.html`, `src/renderer/installations.ts` | the form's ids are gone with the form; the view draws `#picker-list`, `#picker-warnings` and `#picker-onboarding`, and the link is a **button**, so the document holds no `href` and no URL at all |
| `src/renderer/theme/screens.css` | the removed form's id-scoped rules leave, and the picker's real hooks are styled: `#picker-warnings li`, the onboarding screen's `h2`/`code`/link button, and one `--border-strong` boundary rule over every control the view draws (`#picker-list button`, `#picker-onboarding button`) — no new token and no new colour |
| `test/unit/installlist.test.ts`, `test/unit/onboarding.test.ts`, `test/unit/cli-stub.test.ts` | **new**, and the pins described below |
| `test/unit/installations-ipc.test.ts`, `installations-store.test.ts`, `installations-view.test.ts`, `spawn.test.ts`, `stub.test.ts`, `stub.ts`, `configread.test.ts`, `window.test.ts` | rewritten or amended to the surface that exists |
| `test/e2e/installations.spec.ts`, `test/e2e/degraded.spec.ts` | the picker spec drives the list, the warning row, the refused 0.5.0 and the unlisted directory; the degraded spec's `resolveBinary` gained its `appEnv` |
| `docs/design/desktop-console.md` | §3.4 rules 4 and 7, D13 and §12 item 8, each amended where the form was named |
| this file | the section you are reading |

### The list is the CLI's, and the console's own records sit beside it

`fugaro config list --json` is the list (the CLI's own document, contract §6.8). The
console adds one kind of row the CLI cannot print — **a record in its own store that
the CLI did not list** — and marks it `in this console's list only`, because there is
no migration and an installation the operator chose yesterday must not vanish because
today's CLI cannot enumerate it. The reverse never happens: a row the CLI listed is
drawn even when its config could not be read, **with the CLI's own warning line**
underneath and `the config could not be read` where its GCP project would be, because
hiding an installation the CLI itself still counts is the console deciding what the
operator may see.

The **first-installation screen is gated on the console having no rows at all**, not
on the CLI's list being empty: an operator with a legacy store entry has an
installation, and a screen telling them to create one would be the console forgetting
what it holds.

### The binary is resolved, and the store is an override

Rule 4 is amended in the design document and in the table above: a *configured* path
(if this console's store already has one — the field is an **optional override** and
there is no migration), then the platform's well-known prefixes (macOS:
`/opt/homebrew/bin`, `/usr/local/bin`, `~/.local/bin`, `/usr/bin`; Linux: see "Resolution
on Linux" below). **The app's inherited `PATH` is never searched**, which was the previous
rule's last step and is exactly the "discoverable automatically" the ruling asks for.
A candidate that exists but whose version cannot be read is refused rather than
skipped, as before.

**A stored override that has gone is not a refusal** (owner's ruling of 2026-10-20):
the record's `binaryPath` is rule 4's first *candidate*, so the walk carries on to
the well-known locations and the console drives whichever binary it resolved. The
path it resolved is remembered **for the run and never written to the store**
(`resolvedBinaries` in `src/main/index.ts`, keyed by project, written by the startup
handshake and by a choose), and it is what the dashboard's header draws: a store that
carries an id and a project and no path — the shape this console writes — still gets
a line naming the binary the console is running. `withResolvedBinary` in
`src/main/installations.ts` is that join, and it is unit-tested.

The source bound to the installation is handed **the record's override and nothing
else** (`activeBinding` in `src/main/index.ts`): `sourceFor` gives that field to the
resolver as the configured path — rule 4's first *candidate* — and the source walks
the well-known locations itself when there is none, exactly as the startup handshake
does. So `InstallationRef.source` says `configured` when the record names a path and
`well-known` when it names none, which is every record this console writes, and the
*built* source reports the resolution's own word (`resolution.source` in
`src/main/spawn.ts`), where an override that has gone falls through and the word is
the location that answered. **The run's own resolution is not passed down**: a path
handed to the resolver as the configured one would be read as the operator's choice,
and every installation this console wrote would be drawn as `configured`.

### The checkout is optional, and asked for late

Nothing in the first run wants a repository. The one place a repository is genuinely
needed is the pairing check, so that is where the question lives now: a row offers
the checkouts **the CLI listed for that installation**, one button each, and setting
one runs the allow-listed `doctor --json` with that directory as the child's working
directory. That check is `validateInstallation`, and it is handed the same two facts
a **choose** is probed with: the record's own `binaryPath` — rule 4's first
*candidate*, so an installation whose only `fugaro` is its override is checked
against the CLI the console drives rather than against whatever the walk finds — and
the record's `configPath`, which is rule 3's `--config` for the `doctor`. A record
with no override passes none, and the walk decides. A directory the CLI never listed
is refused by name — `not one of the checkouts the CLI listed` — and a row that
already has one offers to drop it, which is how an operator gets back to the
ordinary no-checkout state. `~/.config/fugaro` is still not read by the console: the
CLI is the reader.

### The absent-binary screen carries the resolution's own sentence

`OnboardingState.reason` — the sentence `resolveBinary`, or the version probe inside
it, answered with — is drawn by the renderer as a paragraph of its own, above the
install command. The screen's **fixed** sentence is silent about the cause on
purpose: `installCliState` is built for **every** failure of rule 4's resolution, and
only one of the three is "nothing was found". A candidate that exists and exits
non-zero or times out on `version`, and a `binaryPath` in `installations.json` that
is not absolute, draw this same screen — so a fixed sentence that said the CLI was
not found sent an operator to install one they already had, and the sentence naming
the real problem was dropped. The reason reaches no warning line while a screen is
drawn (`listReply` forwards it only for D13's no-installation state), which is why
the screen is the one place it is said. `installations-view.test.ts` pins both
halves: the reason is drawn, and the fixed words claim no cause.

### The two screens, and the two constants they copy

- **No `fugaro` binary: "Install the Fugaro CLI first"** — the fixed sentence, the
  resolution's **own** sentence (`state.reason`, drawn as it arrived: this screen is
  what every failure of rule 4's resolution draws, and only one of the three is "it
  found none"), the places the resolution looked (the answer's own `searched`, drawn
  as the answer gave it), `brew install dimipaun/tap/fugaro`, and a link button to
  `https://fugaro.dev/getting-started/`.
- **A binary and no installation: "Set up your first installation"** — one sentence,
  `fugaro setup`, and the same link button.

Both commands live in **one constant each** in `src/main/onboarding.ts`, and
`test/unit/onboarding.test.ts` pins them to the repository's own copies: the install
command to the `brew install ` lines of `website/data/install.yaml` (whose own Go test
pins those in turn to the `README.md` line each `source:` names), to `README.md`, and
to `FUGARO_INSTALL_COMMAND`, the constant `src/source/cli.ts` has carried since Task
4.4 (which `src/main/onboarding.ts` re-exports, so the console spells the command
once); the setup command to `README.md`'s getting-started step (`1. Run …`) and to
`internal/cli/setup.go`'s `setupDoor` (`name: "setup"`), which is the thing that would
have to change for the string to be wrong — and the module names `docs/setup-wizard/`,
whose plan renamed the command to `fugaro setup` and retired its old spelling, so that
rename was one line here. The
comment-stripped module is asserted to carry `fugaro setup` **exactly once**, because
two spellings of one command is precisely the drift the file exists to catch. The URL
is pinned to `website/src/pages/getting-started.astro` and to
`website/data/site.yaml`'s `canonical_url` and `allowed_hosts`.

The screens are drawn in the approved visual language — dark, the shipped tokens,
no new colour, no new font, no image, no remote content — and **the CSP does not
change and nothing embeds the website**: the link is a button whose click the main
process answers through `openExternalValidated`, and the picker's `openExternal` row
takes **no arguments at all**, so a frame that sends a URL has sent a key no row
declares and is refused before any handler runs. Its **boolean is the answer**: a
handover the platform declined is `openExternal: { ok: false }` with the console's own
`the console could not open the link in the browser` (`OPEN_EXTERNAL_FAILED`), which
the screen draws, rather than a success that drew nothing. `will-navigate` and
`setWindowOpenHandler` still deny everything else.

### The startup's answer is remembered under the project, never the id

The compatibility a handshake read is held **by project name**: `picker.remember`
is `registry.remember(project, …)` -> `compatibility.set(project, value)`, and
every reader asks for that key — `current()` and `activeCompatibility()` do
`compatibility.get(active.project)`, `rows()` does
`compatibility.get(installation.project)`. The entry point handed it the
installation's **id**, so on every launch with a stored active installation the
dashboard's header drew "version not read · unknown" and the picker's active row
said `unknown`, with the answer the handshake had just read thrown away. The
variable is `handshakeProject` now, assigned from `chosen.target.project`, and
the id is not carried at all (`resolveHandshakeBinary` takes an override and a
target, so the field was dead weight that invited the mix-up).

A screen a reply carries whose `kind` this build does not know is **no screen**
(`drawnScreen` in `src/renderer/installations.ts`): `ONBOARDING_COPY` is a closed
map, so an unknown kind reached `copy.title` on `undefined` and threw while the
window was being drawn — taking the rows and the CLI's own warning lines with it.

### The empty document is the first run, and the stub prints it

The state this whole change is about — a machine with a `fugaro` and no
installation — is one document: `{"installations":[],"warnings":[]}`, the
`config list --json` a CLI that found nothing prints. It reaches
`firstInstallationState()` in `src/main/picker.ts` **only** by parsing: a CLI that
printed nothing at all is text `parseInstallationsDocument` refuses
(`JSON.parse('')` throws), and the picker draws the refusal line instead of the
screen.

The e2e stub therefore prints that document when no `installations.ndjson` was
seeded (`test/fixtures/bin/fugaro`), rather than printing nothing — a stub whose
`config list --json` succeeds must print what a successful one prints — and
`test/unit/cli-stub.test.ts` runs the script in plain Node and puts the answer
through the console's own parser. `test/e2e/installations.spec.ts` gained the
case that state never had: an unseeded profile, the empty document, the picker
D13 opens beside the dashboard, the "Set up your first installation" screen with
its title, its one sentence, `fugaro setup` and one link button, **no** row and
**no** warning line — and the same answer over real IPC
(`onboard: { kind: 'first-installation', command: FUGARO_SETUP_COMMAND }`,
`rows: []`). It is the one case in that file that can skip, because an unseeded
profile has no stored override to point rule 4 at: it skips by name when this
machine's own `fugaro` is what resolved.

### The store is addressed by two keys, and both are unique

`readInstallationEntry` refused two entries sharing an **id** but not two sharing
a **project name**, while every mutation and every reader of the registry is
keyed by the project (`entryOf`, `choose`, `setCheckout`, `remove`, `remember`).
A file with two records for one project loaded, and then `replace` — which writes
a record over *every* entry whose project matches — put the same id in the file
twice, so the **next** load refused the store outright: a console whose picker
had nothing to draw it with, for a state no screen caused and no screen could
repair. The uniqueness check now follows the key the store is actually addressed
by, and the refusal names it (`two entries share the project …`).

### A warning line the CLI printed is never dropped silently

`installlist.ts`'s third rule said "this module keeps every line and bounds only
its length", and the code did the opposite: `warned.slice(0, MAX_WARNING_LINES)`
dropped the tail of a longer document without a word. The bound stays — a
document may not make the picker's list unbounded — and the **omission is now
named**: past `MAX_WARNING_LINES` the console appends one line carrying the count
it did not keep. The two things that are still dropped without a line of their own
are named in the rule where they happen: an element that is not a non-empty
string (not a sentence, and not a line the CLI printed — its `Warnings` is a
`[]string`), and a `checkouts` of the wrong type (rule 2's field, which no screen
draws). A `warnings` that is *present* and is not an array is now a refusal like
every other drawn field of the wrong type; an **absent** `warnings` reads as none,
which is the direction that hides nothing the CLI sent.

### The binding carries the record's override, and the source resolves the rest

`activeBinding` was the one place the run's own resolution reached the source layer:
it put `chosen.binary ?? resolvedBinaries.get(chosen.project)` into the
`BoundInstallation`'s `binaryPath`. That field is documented as the installation's
**optional override**, and `sourceFor` hands it to the resolver as the *configured*
path, so the console's ordinary record — an id and a project and no path at all —
reached `configuredSource` as if the operator had named
`/opt/homebrew/bin/fugaro`, and `installationRef`'s word for it was `configured`.
The provenance design section 3.4 adds, so that "why is this the wrong fugaro?" has
an answer, therefore said *the operator's own record* for a path that came from a
well-known prefix. `activeBinding` now carries `chosen.binary` or nothing, which is
what the startup handshake has always passed (`handshakeInstallation`), so both
readers of rule 4 — the header's resolution and the source's — are asked the same
question and the word is honest at both layers: `configured` for a record that names
a path, `well-known` for one that names none. The two can differ only if the machine
changes between the handshake and a read (an upgrade that moves `fugaro`), and then
the header is stale and the child is right.

The guard is at both ends: `startup.test.ts` pins that the binding carries the
record's override and does not read `resolvedBinaries`, and
`bound-source.test.ts` pins `InstallationRef.source` for the no-override case — an
empty `binaryPath` and `well-known` — and for a record that does carry one.

### The mutations each guard was proved against

Every property below was planted as a change to the shipped code, run, and removed;
the failure is the red step, and each test goes red **by name**.

| Mutation | What fails |
|---|---|
| `registry.load()` dropped from `src/main/picker.ts` | `installations-ipc.test.ts`, 3 cases — the rows, the refusal and the first-run screen |
| the `choose` membership guard removed, so any project may be named | `installations-ipc.test.ts`, the unlisted-project case |
| the `openExternal` row regains its `url` argument | `installations-ipc.test.ts`, 5 cases — the frozen table, the preload mirror, the schema and the two link cases |
| the checkout membership check removed | `installations-ipc.test.ts`, the directory-the-CLI-never-listed case |
| one character of `FUGARO_INSTALL_COMMAND` changed | `onboarding.test.ts`, the install-command cases — and by name |
| one character of `FUGARO_SETUP_COMMAND` changed | `onboarding.test.ts`, the setup-command and two-states cases |
| `GETTING_STARTED_URL` pointed at another path or host | `onboarding.test.ts`, the link case |
| a form rule put back in `screens.css` (`#picker-add { … }`) | `installations-view.test.ts`, "styles the containers this document declares, and no id it does not" |
| the header/resolution join reversed (`active.binary ?? resolved`) | `installations-store.test.ts`, "names the binary this run resolved, and not the path the store remembers" |
| the header read from the store alone (`installationState` dropping `resolvedBinaries`) | `startup.test.ts`, "answers the header from this run's resolution, and keeps the record for the source" |
| the `openExternal` row's boolean ignored (`if (!opened)` removed) | `installations-ipc.test.ts`, "reports a handover the platform declined, rather than a success that drew nothing" |
| the old refusal sentence restored ("give an absolute path…") | `spawn.test.ts`, the non-absolute configured path case |
| the startup's answer remembered under the installation **id** (`chosen.id`) instead of its project | `startup.test.ts`, "hands the startup handshake to the picker under the installation's project, never its id" |
| the compatibility read back by id (`compatibility.get(active.id)`) | `installations-ipc.test.ts`, "carries the startup's handshake into the dashboard's read and the picker's row" |
| `drawnScreen` removed, so any `kind` reaches `ONBOARDING_COPY` | `installations-view.test.ts`, "draws no screen for a kind it does not know, and draws the rows anyway" and the `viewOf` case beside it |
| `internal/cli/config_init.go`'s `Use:` renamed (the setup-wizard rename) | `onboarding.test.ts`, "is the command the CLI itself declares" |
| a wrong repository root in the test's own path arithmetic | `onboarding.test.ts`, "is a small closed union, and `none` is shared" |
| the stub printing **nothing** for an absent `installations.ndjson` (the pre-review behaviour) | `cli-stub.test.ts`, "prints the empty document when no list was seeded, and the console reads it as \"none\"" |
| `config list --json` dropped from the stub's allow-list sentence | `cli-stub.test.ts`, "refuses a command outside the allow-list, and the sentence names every one it answers" |
| the tail of a long document dropped silently again (`warned.slice(0, MAX_WARNING_LINES)`) | `installlist.test.ts`, "bounds what one document may make the picker hold" |
| a `warnings` that is not an array read as "no warnings" | `installlist.test.ts`, "refuses a `warnings` that is there and is not an array, and reads an absent one as none" |
| the store's project-uniqueness check removed (the id one kept) | `installations-store.test.ts`, "refuses two entries for one project" |
| the source binding given the run's resolution again (`chosen.binary ?? resolvedBinaries.get(chosen.project)`) | `startup.test.ts`, "answers the header from this run's resolution, and keeps the record for the source" |
| `InstallationRef.source` read from anything but the record's override (`'configured'` in both cases) | `bound-source.test.ts`, "says `configured` only for a record that carries an override, and `well-known` otherwise" |
| a checkout validated without the record's own binary (`validateInstallation({ project, checkout }, …)`) | `installations-ipc.test.ts`, "probes the record's own override, and its config, rather than a second walk of rule 4" — the installation whose only `fugaro` is its override is refused |
| a checkout validated without the record's `configPath` | the same case, on the pairing check's `--config` |
| the absent-binary screen dropping the resolution's own sentence | `installations-view.test.ts`, "draws the sentence that names the real problem, for a CLI that was found and refused" |
| the screen's fixed sentence claiming a cause again ("it found none at the places it looks") | the same case — the cause is the reason line's, never the fixed words' |

### The verification, and what it did not cover

`npx vitest run --dir test/unit` under Node 22 is green — **56 files, 2028 passed, 1
skipped** (the console's own suite; the verify container runs this repository's Go
build and test, so the console's tests are run locally and the pull request says so)
— and `npx tsc --noEmit` is clean, which typechecks the Playwright specs too.

**The e2e suite was rewritten and was not run in the container this work was done
in**: Playwright's Electron launch needs a display and a GTK/NSS/glib stack the image
does not have (`libglib-2.0.so.0` is missing and there is no root to install it), so
`test/e2e/installations.spec.ts` is compiler-checked and reviewed rather than
executed here. It is the one verification this branch could not complete. The same
holds for the three specs the review of this branch asked for: `entry.spec.ts`'s
rewritten B1 case (a stored override that has gone falls through, and the dashboard
names the binary the spawn log shows it ran), its new no-override case (a record with
an id and a project and no `binaryPath`, which is the shape this console writes —
seeded through `seedActiveInstallation(project, null)`, which writes the field as
absent), and `installations.spec.ts`'s rewrite. They are typechecked, and their
claim that the header's line and the child's binary agree is the one the unit suite
pins without a window. **A later review round** made those two cases ask
`resolveConsoleBinary(env)` for the binary they expect rather than spelling
`~/.local/bin/fugaro`: rule 4 asks `/opt/homebrew/bin` and `/usr/local/bin` first,
so on a machine with a real `fugaro` in either, the console drives that one — and
a spec that hardcoded the scratch stub would fail there for a fact about the
machine. A machine where nothing this console can drive resolves skips them by
name. `resolveConsoleBinary` itself now takes `HOME` out of the environment it is
handed, so its answer is the app's own question rather than the spec runner's.
The **last review round** added the first-run case described above to
`installations.spec.ts` — the empty document and the "set up your first
installation" screen — which is likewise typechecked and reviewed rather than
executed here; what the unit suite proves about that state without a window is the
stub's answer and the console's own parse of it (`cli-stub.test.ts`).

**The checkouts a row offers are the ones the CLI's document carries.** The contract
is tolerant of the array's absence and of a malformed entry, so a `config list` that
prints no `checkouts` offers none — and the console then offers no way to set one at
all, which is the truthful state rather than an invented directory. The stub's
`seedInstallationsList` writes none today, so the e2e path for the button is unit
coverage rather than a window.

## The console's screenshots for the website (the owner's run, fixture data only)

The website's Console page carries pictures of the real application (owner ruling of
2026-10-20), and they are taken from **fixture data** by one command the owner runs:

```sh
./scripts/console-screenshots.sh        # from the repository root
npm run screenshots                     # the same run, from console/ (it builds the console first)
```

The script installs the console's pinned toolchain (`npm ci`) and then runs the same
`screenshots` script the second command names, which **builds the console first** (`npm run
build`) and then runs `tools/screenshots/capture.ts`. The build is inside the script rather
than in its caller because what the run launches is the **built** app
(`package.json`'s `main`, `dist/main/index.js`) and `dist/` is git-ignored and untracked: a
`screenshots` script that did not build would launch a file that is not there on a fresh
checkout, or one from a branch ago on the machine of whoever runs it next (review of
2026-10-20, high finding). It **needs a display**: Electron opens a real window, so this
is a run on a Mac with a desktop session and never a run in CI — the script's own header says
so, and with no display the launch fails and the command exits non-zero. It refuses to start
on any Node but the exact one `package.json`'s `engines.node` pins (`.npmrc` sets
`engine-strict=true`, so `npm ci` is the check) with a one-line error rather than a stack
trace — and the version it wants is *read from that file* and compared exactly, because a
major-only guard lets Node 22.11 through to precisely the `npm ci` EBADENGINE failure the
guard exists to prevent, with a message that said the version was fine.

### What it launches, and the checks that keep it fake

The console under the camera is the **built** application, launched through the e2e suite's
own Playwright/Electron path (`test/support/launch.ts`'s `launchConsole`, with the ETXTBSY
retry) so its real startup handshake, resolver, `watch` supervisor and screens all run. What
it is *not* is a console pointed at an installation. The fake CLI is the unit suite's own
`test/unit/stub.ts`, installed into this run's scratch tree from the documents in
`test/fixtures/screenshots/`, and it **refuses** (exit 2) any command the run did not prepare
— so a screen whose read was not prepared is a picture of a refusal rather than of an empty
document. It makes **one launch and takes four pictures from it**, and three rules are checked
before the window exists, in `tools/screenshots/isolation.ts`:

1. **the binary, asked the way the launch asks it.** Every launch seeds the console's own
   store with one installation *chosen*, naming the fake's absolute path — and before the
   window opens, `assertLaunchIsFixture` reads that store back with the console's own reader
   (`readInstallationFile`) and resolves the chosen row with the console's own **one-path**
   resolution (`resolveInstalledBinary`), refusing the launch unless the path is the fixture's
   exactly, naming the path it resolved. The handshake itself treats that path as rule 4's
   first candidate, so a path that is there and answers is the one it runs. A store that chooses *no* installation is refused
   too, because the handshake of a console with nothing chosen walks
   `/opt/homebrew/bin`, `/usr/local/bin`, `~/.local/bin` and `/usr/bin` — the directories where
   the operator's own `fugaro` lives. It deliberately does **not** ask the machine's resolver
   what it would find: that walk probes the winner by *executing* it, so the question itself
   would run the operator's real CLI and then refuse the run — a check that both touched the
   thing it exists to avoid and made the run impossible on the one machine it is for (review of
   2026-10-20, high finding). The picker picture is opened from the console's own
   `Installations…` menu item for the same reason: the second launch this run used to make —
   a console with nothing chosen — was that walk;
2. **the paths.** `PATH`, `HOME`, `XDG_CONFIG_HOME` and `TMPDIR` are written into one fresh
   scratch tree, and the window is given a fresh `--user-data-dir` under it; a launch whose
   environment or profile names anything else — or that names the operator's real `HOME` or
   `XDG_CONFIG_HOME` — is refused, naming the key and the path. Nothing here reads
   `~/.config`, and the seeded `installations.json` is written into the fresh profile.
   `TMPDIR` is redirected rather than carried because the console builds the private working
   directory its children run in as `$TMPDIR/fugaro-console-<uid>/cwd`: an ambient one is a
   directory this run would write to and never remove;
3. **the rest of the environment.** The child's environment is built from an allow-list of
   eight window-drawing keys, not a denylist: `FUGARO_CONFIG`, `GOOGLE_APPLICATION_CREDENTIALS`
   and `SSH_AUTH_SOCK` cannot reach the app or the CLI it spawns, and the run's own log ends
   with the fake child's account of every spawn. **The only `fugaro` any child of this run
   executes is the fake** — the binary check above is one path, and the probe it makes is that
   fake's own `version`.

### What comes back: four pictures, a manifest, and the check on the bytes

The captures land in `console/screenshots/` — **git-ignored, never committed**: a picture of a
window is an artefact of a machine and a day, and the website's owner copies the one they want
into `website/` deliberately. Each is 1280x800 CSS pixels at device scale factor 2 (2560x1600
pixels), dark theme, the platform's title bar excluded (a `page.screenshot` reads the window's
web contents). The four captures — three screens of one window and the picker's own window,
all from the **same launch** — are:

| File | Screen |
|---|---|
| `console-live.png` | the Live screen: three runs (one in review with its verification passed, one in review and near its deadline, one queued), two repositories, and the day's spend at 34.85% of a $120 cap |
| `console-run.png` | the run detail the Live screen's first row names: stages, verification, findings, the log tail |
| `console-spend.png` | the Spend screen: thirty days of daily spend, the totals and the caps |
| `console-installations.png` | the picker, opened from the console's own `Installations…` menu item: three installations, each naming the fake, with `acme-platform` the chosen one |
| `console-first-run.png` | **not taken.** The no-installation onboarding page is #751, which is not on `main`, so there is no such screen in this console; the manifest carries the omission and the reason rather than a silence |

The fifth row is the omission, and it is a row because that is where the four are: the manifest
carries it as a fact (`omissions[]`), and a reader who expected the owner's fifth picture finds
the reason beside the pictures that exist rather than after two paragraphs about alt text.

**One picture carries a machine-specific path, and that is a decision.** The picker's rows draw
each installation's stored binary path, and the path in these rows is the fake the run installed
in its own scratch tree — `/var/folders/…/fugaro-console-screenshots-XXXXXX/bin/fugaro` on macOS,
a different path on every run, and a directory that is gone when the run finishes. The
alternative was to hide it, and hiding it was rejected: the console draws the stored path
verbatim (`rowText`/`viewOf`), so a picture with a path-shaped hole in it would be a picture of
something the console does not draw, and the point of this capture is that a real store is being
drawn. What the path does *not* name: no home directory, no checkout, no real `fugaro`, and no
project or repository that exists — the projects are invented. The alt text that travels with
the picture therefore describes the path rather than quoting it (asserted in
`test/unit/screenshots.test.ts`). **If the owner would rather not publish a path at all,
`console-installations.png` is the one to leave out of `website/`**: it is the only capture whose
subject is a path, and the other three carry none.

Every alt text says only what its screen **draws**: the Live screen draws `runs`, `repos` and
`total`, and the frame's `finished[]` list is decoded and drawn nowhere — an earlier version of
that sentence counted it ("five runs … one that opened pull request acme/platform-core#412 and
one that failed"), which was a fact about the fixture and in no picture, shipping to the
website inside `MANIFEST.json` (review of 2026-10-20, medium finding). `test/unit/screenshots.test.ts`
now asserts the *absences* as well as the figures.

**And every figure in every alt text is read out of the screen's own model**, built from the same
fixture the capture is taken of: the run detail's "build, test and lint verification" is the kinds
of `buildRunModel(...).stages`, its "three findings" is that model's finding count, the spend
picture's "thirty days" is `buildSpendModel(...).rows.length`, and the Live picture's round, stage,
verification word, deadline flag and queued row are the last frame's own (review of 2026-10-20,
medium finding — two of the four sentences were asserted by nothing). The run both pictures are of
is held to one story as well: the Live row's `stage` and `verify` are the ones `diagnose.json`
tells, because the run-detail capture is reached by clicking that row and the two screens draw the
same run's words (review of 2026-10-20, medium finding). The picker's status line is *read out of*
`src/renderer/installations.ts` rather than written into the test, so the sentence the alt quotes is
the renderer's and not a copy of it (review of 2026-10-20, low finding).

`tools/screenshots/png.ts` reads the PNG bytes itself and refuses a capture that is blank (the
standard deviation of its pixel luminance, sampled, must be above `BLANK_STDDEV_FLOOR`) or is
not exactly the size the manifest declares. It reads PNGs rather than adding an image library
because every specifier in `package.json` is an exact pin (`test/unit/pinning.test.ts`), and
reading its own bytes is forty lines of `node:zlib`. `tools/screenshots/manifest.ts` writes
`screenshots/MANIFEST.json`: one entry per picture with its pixel size, its sha256 and a
one-sentence alt text drafted from the fixture, plus what was omitted. And
`tools/screenshots/failure.ts` is the run's failure path: the one sentence a failed run ends
with, and the handler that prints it for the rejections `main()` never awaits — Playwright
reports a failed launch on promises nobody holds, and Node 22's default
`--unhandled-rejections=throw` would otherwise end the process on a playwright-core stack
before the run's own words were printed (review of 2026-10-20, high finding).

### The tests, and the mutations each one was run against

`test/unit/screenshots.test.ts` holds everything the run decides **without a window** — 63
tests, and every rule below was seen red against a planted change before it was committed:

| Claim | Mutation it was run against |
|---|---|
| the launch's store is the only thing the handshake may read | `assertLaunchIsFixture`'s `assertFixtureBinary` call dropped — "refuses a store that names another binary" red |
| a store that chooses no installation is refused | the `active === null` refusal removed — red, and it is the rule that keeps the machine's `fugaro` out of the walk |
| the check runs its probe nowhere real | the probe's working directory changed from `isolation.userData` to `/tmp` — the child's own `cwd` in the fake's report red (it is taken from the `Isolation`, so no caller can point it elsewhere) |
| the resolver must find the fixture binary | `assertFixtureBinary`'s equality check replaced by `return` — "the same path is not a refusal" and the store cases both red |
| a path outside the scratch tree is refused | `assertIsolated`'s `inside.some(...)` check disabled — red, naming the planted `HOME` |
| the operator's own `HOME` is refused **even inside** a scratch tree | the `forbidden` entry not set — red |
| `TMPDIR` is redirected, not carried | `env['TMPDIR'] = isolation.tmp` removed — "drops an ambient key that is empty" / the allow-list case red, and `assertIsolated`'s `TMPDIR` check disabled — the refusal case red |
| an empty ambient value is an absent one | `&& value !== ''` dropped from the allow-list loop — red |
| no credential-shaped key reaches the child | `launchEnvironment`'s allow-list loop replaced by `Object.keys(ambient)` — the `GOOGLE_APPLICATION_CREDENTIALS`/`FUGARO_CONFIG`/`SSH_AUTH_SOCK`/`AWS_PROFILE` case red |
| the alt text describes the screen, not the frame | `#412` restored to `LIVE_SCREEN.alt` — the absence assertions red; the picker's "none of them chosen" restored — red too |
| every document the dashboard draws names one project | `diagnose.json`'s `project` key removed — the agreement test red (it was the key the fixture was missing) |
| the fixtures are documents the console reads | the five fixtures driven through the console's **own** readers: `decodeSnapshot` for the feed's frames, `buildSpendModel` for `report`/`budget show`, `buildRunModel` for `diagnose` + `logs`. `max_reserve_usd` read from `global` instead of the top level — red |
| the log window has no refused line | the fixture read as a bare list rather than through `parseLogEntries` — the `refused` assertion red |
| a seed row must be sixteen hex characters | the id pattern loosened to `/^.*$/` — red |
| two seed rows may not share an id | the duplicate check disabled — red |
| the feed fixture must hold frames | the non-empty check disabled — red |
| a missing fixture document is named | the wrapper's sentence replaced by the raw `ENOENT` — red |
| the fake refuses an unknown command | `default.code` 2 → 0 — red |
| the fake prints the version the console was built against | the version line replaced by `0.1.0` — red |
| a manifest may not list one file twice | the duplicate check disabled — red |
| a capture's name carries no path | `FILE_RE` loosened to `/^.*\.png$/` — red |
| a size is a positive whole number | the `<= 0` half of the check dropped — red |
| an entry carries an alt sentence | the empty-alt check disabled — red |
| the manifest's key order is canonical | `screens` serialized before `window` — red |
| a blank rectangle is refused | the `spread < BLANK_STDDEV_FLOOR` gate disabled — red |
| a capture of the wrong size is refused | (driven directly: the test plants a 64x64 file against a 128x128 expectation) |
| a Sub-filtered scanline is reversed | `case 1: value = raw + left` → `value = raw` — red |
| an RGB file's channels are read apart | the green and blue bytes read from the red byte — red, two tests |
| the entry point handles the rejection nobody awaits | `installFailureHandler(process)` not installed — red; the event name changed to `uncaughtException` — red; `String(reason)` instead of the `Error`'s message — red |
| a run that printed a failure line does not report success | `target.exitCode = 1` dropped from the handler — red; `exitCodeFor` returning the work's own code — red |
| the documented `screenshots` script builds the app it launches | `npm run build &&` removed from the script — red; the build moved after the capture — red; the README's sentence about it withdrawn — red |
| the picture table is one table | the omission row moved back after the prose — red; the row dropped — red |
| the shell script's header says what it reaches | the old "nothing here talks to GitHub, the cloud, or the network" restored — red; the registry read left unnamed — red |
| the frames carry the e2e suite's ten keys | a key added to a frame — red; a key added to the recording the claim names — red |
| the capture size is a number, not its own definition | the device scale factor set to 1 — red; the window set to 1440x800 — red |
| the selectors are the renderers' own class names | `drawnBy` naming the wrong renderer — red; a selector renamed — red; a renderer renaming its class — red; the picker's list losing its id — red |
| the picker's alt describes its path rather than quoting one | a path put into the sentence — red; the description removed — red |
| the two pictures of one run tell one story | the feed's first row put back to `implement`/`pending` while `diagnose.json` says `review` with every verify record passed — the cross-file test red; the alt's "in review with its verification passed" changed — red |
| every alt figure is the fixture's | `RUN_SCREEN`'s "three findings" → "four findings" — red; "build, test and lint" → "build and test" — red; `SPEND_SCREEN`'s "thirty days" → "thirty-one days" — red; the Live alt's round, deadline flag and queued row each changed — red |
| the picker's sentence is the renderer's own | the alt's quoted sentence changed to `(reduced)` — red; `viewOf`'s own template reworded in `src/renderer/installations.ts` — red (the alt still quotes the old one) |
| this file's index names its sections | the allow-list's `// 2.` banner removed — red, naming the header item with no banner |
| the picture table says what the fixture holds, not what it used to | the Live row's "one in review with its verification passed" put back to "one implementing" — "describes each picture with the fixture's own words" red (the table test compared row names only, so the drift was unchecked; review of 2026-10-21, medium finding) |
| the build's `include` list is the run's own import graph | `test/support/cliStub.ts` — the *e2e specs'* fake, which nothing the run reaches imports — put back into `tsconfig.screenshots.json`'s `include` — "compiles exactly the files the run reaches" red, naming the entry (review of 2026-10-21, low finding) |

The blank check is driven by a PNG **encoder** written in the test file (`node:zlib`'s
`deflateSync`, a CRC per chunk, filters 0 and 1 really applied), because the only way to drive
a check that reads pixels is with a picture whose pixels are known, and an image library is a
dependency the console is not allowed to gain.

### What this run cannot prove from here

**The Electron launch itself.** No test in this repository opens the console's window — there
is no display in CI, and the container this was written in has none either — so "the window
comes up, the screen draws, the capture is not blank" is exercised by the owner's own run of
`./scripts/console-screenshots.sh`, which is also the only thing that can produce the pictures.
The first run is the check: a screen that does not draw fails its `waitForSelector` with the
selector it waited for, a blank capture fails on its own numbers, and the fake's spawn report
prints which commands the console actually asked for. The menu click that opens the picker is
in the same category: `test/e2e/installations.spec.ts` drives that same click, but only a run
with a display exercises it against the built app and the frozen menu this run installs.

## The Linux build, unsigned (the owner's ruling of 2026-10-20)

**The owner ruled: *"yes, Linux for 0.10.0, unsigned"*.** Windows stays out. The
ruling is recorded where the answer is — the design's **D10** cell carries it as
a dated amendment after the 2026-10-15 acceptance it changes — and in its own
section, [design §7.6](../../docs/design/desktop-console.md), with the four
places that had read "later" amended in the same commit (§4.1 item 5, §4.2,
§7.5's owner-run row, §9.4's runner bullet and staged-file table). The plan's
Task 5.1 carries a dated note too, because two phrases of its own text are
withdrawn by the ruling: "two **signed** deliverables", and Linux "via the
distro channels this repo would have to choose".

**One sentence is the whole of the security story here, and it is the one a
reader must not have to infer: an AppImage is not code-signed.** Nothing on
Linux signs, notarises or staples anything. There is no Linux key, no
certificate, no owner-run signing step and no secret in any Linux job, because
there is nothing to sign *with* — and the release's own words, the design's and
this file's say unsigned rather than leaving it to be deduced.

### What ships, and the configuration that produces it

| | |
|---|---|
| Targets | `AppImage` and `deb`, **x64**, pinned in `build.linux.target` rather than passed on a command line |
| Artifact names | `Fugaro.Console-<version>-linux-x86_64.AppImage` and `Fugaro.Console-<version>-linux-amd64.deb` — `build.linux.artifactName` is `Fugaro.Console-${version}-linux-${arch}.${ext}`, so the name comes from the tag's X.Y.Z and never from a file name typed into a workflow. **The packer writes the target's own word for the architecture and not the build's `x64`**: the AppImage is `x86_64` and the `.deb` is `amd64`, and those two spellings are what the release serves and what its `console-sha256sums.txt` lists — a page or a command that looks for `linux-x64` matches neither (this file, `docs/release.md`'s "Verifying the console's release" and `docs/design/desktop-console.md` §7.6's artifact sentence said `linux-x64` until 2026-10-24, which is what a published release corrected; **the workflow comments still say it**, in three places — `console-release.yml`'s staged and applied copies and `console-linux.yml` — and are not corrected here, because no agent run may edit `.github/workflows/`: `docs/pending-workflows/README.md` is where the owner reads what to correct, and when) |
| Icon | `console/build/icon.png` — **the same file `build.mac.icon` names**, so the two platforms cannot show different marks |
| Desktop entry | `executableName: fugaro-console`, `category: "Development;Monitor"`, a `synopsis` and a `description`; the deb's `maintainer` is required rather than decorative — this `package.json` has no `author` with an email, so without it the deb leg fails at pack time |
| Command | `npm run package:linux` — `electron-builder --linux --publish never`, the pinned local packer, never `npx`, and never publishing |

**The bundle is exactly as strict on Linux as on macOS, and that is the point of
putting the Linux keys beside the macOS ones rather than in a second file.**
`asar` stays `true`, `build.files` is the same allow-list `build-files.test.ts`
holds (the compiled main process, the preload, the shared and source modules,
the renderer bundle and its two documents — and *not* `dist/main/fuses.js`,
which is a build-time hook and not shipped code), `publish` stays `null`, and
`npmRebuild` stays `false`. **`electronDownload.checksums` grew the Linux
archive**: `electron-v44.7.2-linux-x64.zip` joins the two darwin zips, so every
Electron archive this build downloads is verified against a hash in this
repository — a platform whose download was unverified would be the one place the
pinning story did not hold.

### The same six fuses, on the Linux executable

**`src/main/fuses.ts` resolves the binary per platform in one place.**
`fuseTarget` reads `electronPlatformName` and returns either darwin's
`<appOutDir>/<productName>.app/Contents/MacOS/<productName>` or Linux's
`<appOutDir>/<executableName>` — `linuxExecutable` falls back to the packer's
own `sanitizedName.toLowerCase()`, the name `LinuxPackager` uses when
`executableName` is unset, and **refuses a context that names no binary at all**
rather than guessing a path to flip. Anything that is not `darwin` or `linux` is
**refused, naming the platform and the two this build packages**: a hook that
skipped would ship the fuses unset, which is the one outcome it exists to
prevent. `SUPPORTED_PLATFORMS` is that list, and it is the same list
`src/main/spawn.ts`'s location table is keyed on.

**The fuse set does not vary by platform.** The same six flips are written into
the Linux executable, with byte-for-byte the same `fuseConfig()`, and the wire
is read back on Linux too — a `RunAsNode` that came back enabled stops the build
before the packer turns `linux-unpacked` into an AppImage or a deb.
`EnableEmbeddedAsarIntegrityValidation` is enforced by Electron on macOS and
ignored on Linux; it is set on Linux anyway, because a per-platform fuse table
would be a second policy to keep in step with the first.

### Resolution on Linux: two lists, chosen by the platform, never the inherited `PATH`

`src/main/spawn.ts` has one table — `PLATFORM_LOCATIONS`, read through
`locationsFor` — and it is the only place the platform is consulted:

- **Rule 4's prefixes**, in order: macOS `/opt/homebrew/bin`, `/usr/local/bin`,
  `~/.local/bin`, `/usr/bin`; **Linux** `/usr/local/bin`, `/usr/bin`,
  `~/.local/bin`, `/home/linuxbrew/.linuxbrew/bin`, `~/.linuxbrew/bin`.
  **Neither list is read out of the environment** and neither may take the
  other's Homebrew prefix: `/opt/homebrew/bin` is the macOS prefix and
  `/home/linuxbrew/.linuxbrew/bin` is Linux's, and a directory that does not
  exist on the machine is not a place to look for a binary. The app's own
  `PATH` is not a step at all (the owner's ruling of 2026-10-20, "The binary is
  resolved, and the store is an override" above): the walk ends with the
  platform's list.
- **Rule 5's child `PATH`**, rebuilt from the resolved binary's own directory
  and the platform's tool directories: for Linux `/usr/local/bin`, `/usr/bin`,
  `/bin`, `/usr/sbin`, `/sbin`, `/home/linuxbrew/.linuxbrew/bin`. `~/.linuxbrew/bin`
  is deliberately **not** in that list: nothing expands a tilde inside a `PATH`,
  and the per-user case is covered by the resolved binary's own directory, which
  always comes first. The app's inherited `PATH` never reaches the child on
  either platform.
- **A developer platform that is neither** (running the sources on Windows, which
  this build does not package) falls back to the POSIX list rather than to the
  environment: the fallback cannot find a binary that is not on the machine, and
  the one thing it must not do is reach for the app's `PATH`.

### The owner's commands, and what they do not need

```sh
cd console
npm ci --ignore-scripts                      # exact pins, registry host, from the lockfile
node node_modules/electron/install.js        # the distribution, verified against the pinned checksums
npm run build && npm run package:linux       # AppImage and deb into release/, unsigned
sha256sum release/*.AppImage release/*.deb   # what a reader checks against console-sha256sums.txt
```

**There is no signing step in that list and no secret in it** — no `CSC_LINK`, no
keychain, no `--sign`, no environment. The macOS section below is the one with
the owner-only pipeline, and none of it applies here.

### CI: staged for the owner, and outside the release gate

Two files, both staged under `docs/pending-workflows/` because no Fugaro run may
create or edit anything under `.github/workflows/`:

- **`console-linux.yml`** — one job, `console-linux`, on `ubuntu-latest`:
  `contents: read`, **no environment and no secret**, the pinned toolchain,
  Electron's own download verified against the three checksums `package.json`
  pins, `npm run build`, `npm run package:linux`, an assertion that exactly one
  `.AppImage` and one `.deb` came out, and `upload-artifact`. It runs on every
  pull request and every push to `main`, with no path filter.
- **`console-release-linux.yml`** — **the staged `console-release.yml` with the
  Linux legs added**, so it supersedes that file and is copied over both the
  staged copy and the workflow. Its third job, `linux`, holds no secret either;
  it packs on the pinned `ubuntu-24.04` and then verifies **what ships** — the
  SHA-256 of both artifacts, the `.deb` through `dpkg-deb`, the AppImage through
  its own runtime's `--appimage-extract`, and the fuse wire read back with
  `@electron/fuses` out of the executable *inside each artifact*, which is the
  one claim the pack-time hook cannot make about the packaged result. `publish`
  now merges both legs into **one** `console-sha256sums.txt`, re-checks it in the
  directory that ships, signs that file keyless with cosign into
  `console-sha256sums.txt.sigstore.json` (this workflow's own OIDC identity — not
  a key of the owner's) and attaches everything to `console-vX.Y.Z`.

**Neither file is in `scripts/release-gate.sh`'s required-check list, and that is
a decision rather than an omission.** The gate's list is derived from
`console-tests.yml`'s job set and asserted by `TestReleaseGateRequiresTheConsoleCheck`,
which refuses a workflow with more than one job — so adding the Linux build to
the gate is not a packaging change at all, it is a change to which checks gate a
release tag, and that is the owner's call. `docs/pending-workflows/README.md`'s
`## console-linux.yml` section carries the three edits it would take, and the
copy-in commands for both files.

### The website offers the Linux pair, and says they are unsigned (2026-10-24)

**The site shipped macOS only until the release that carried a Linux asset, and
that is what the ruling waited for.** `website/content/downloads.json` carries an
optional per-platform block, and the reader that validates it
(`website/src/lib/console-downloads.mjs`) holds it to the file's own assets: the
block names the ids of the downloads it links, and every id must resolve to an
entry of the file's `downloads` list — which is itself held to the release's
published names and to the checksum its own `console-sha256sums.txt` records. So
**a download link can only exist for an asset the data file names**, and **the
block renders only when it is there** (`Downloads.astro` draws nothing for it
otherwise: no heading, no sentence, no link, not even a word saying which
platform is missing).

`console-v0.110.0` publishes `Fugaro.Console-0.110.0-linux-x86_64.AppImage` and
`Fugaro.Console-0.110.0-linux-amd64.deb`, so the file carries the block: the
sentence a Linux reader is owed (**the build is not signed at all** — an AppImage
is not code-signed, this project has no Linux signing key, and neither file has a
signature of its own), the steps that stand behind an unsigned download (the
release's own `console-sha256sums.txt`, verified against its Sigstore bundle with
`cosign verify-blob`, then the Linux entries checked against it — the commands
`docs/release.md`'s "Verifying the console's release" gives) and the two ids. The
console page's Downloads section renders the block — the sentence, the steps and
the two rows — and Getting Started's console option renders the same sentence and
the same two rows from the same reader, without the steps, as it renders a macOS
link without its checksum. The data file's `linux.verify` is the one place those
commands are written, and the version in them is the same `{version}` placeholder
the asset names carry, so a bump moves them together.

### What was verified here, and what was not

- **Verified in this container**: the fuse hook's per-platform resolution, the
  refusal of a platform this build does not package, the same-six-fuses claim,
  the Linux wire read-back, the Linux executable-name fallback and its refusal
  of a nameless context (`test/unit/fuses.test.ts`); both location lists and the
  child's `PATH` on both platforms (`test/unit/spawn.test.ts`); the packaging
  configuration — the two targets, x64, the artifact name, the icon, the
  desktop-entry fields, the absence of any signature setting, and the Linux key
  set (`test/unit/fuses.test.ts`); the two staged workflows' shape, their
  pins and their secret-lessness (`internal/cli/docs_desktop_console_test.go`).
  Every new guard was mutation-proven.
- **Not verified here, and not verifiable in this container**: no
  `electron-builder` run at all — this task's own rule is that the packaging is
  unit-tested as a configuration rather than executed, and a real Linux pack
  needs a Linux runner with a display-less AppImage toolchain. **Nothing in this
  container has produced an AppImage or a `.deb`**, and every claim above about
  what the packer writes is a claim about its configuration. The pair the website
  now links came off the Linux job of the release that published them: both names
  and both checksums in `website/data/console-sha256sums.txt` are that release's
  own, and the `x86_64`/`amd64` spellings above are what it served.

### What is not here

**No Linux auto-update** (design §7.3 is Squirrel.Mac and an `electron-updater`
feed; the AppImage has no such feed here), **no arm64 Linux build** until one can
be built and verified on a runner that has it, **no Flatpak, snap or rpm**, **no
Linux Homebrew cask**, **no Windows build**, and **no signature of any kind**.
Each is a later decision and not an oversight: this ruling is the smallest thing
that makes "Linux is supported" true.
