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
npm run record-fixture -- --help               # the recorder; OWNER-RUN, see "Recorded, redacted fixtures"
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

## The IPC allow-list (Task 1.2)

Design section 5.1 rule 5: "**an IPC allow-list, not an IPC bridge**". The
console has no bridge. It has a fixed table of named operations, and three
prohibitions that are the whole of the rule: **no `invoke(channel, ...)`**
(nothing in the console takes a channel from a caller), **no channel name from
the renderer** (the preload exposes one *method* per operation and the renderer
passes arguments to a method, never a name), and **no dynamic `require`**.

`src/main/ipc.ts` is the table and the gate. Twelve operations — the eleven CLI
reads of design section 2.1 (`watch`, `budget show`, `report`, `runs ls`,
`diagnose`, `logs` for the document, `logs` for the link, `secrets ls`,
`accounts ls`, `version`, `doctor`) plus the one validated external link — each
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
table is declared `as const`, with **no type annotation**, so its twelve rows
stay literal types: `ExposedName` and `ExposedMethod` are unions of twelve
literals rather than `string`, `ConsoleBridge` is twelve *named* methods rather
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
file and line each entry was grepped from on 2026-10-16. It carries every
command the CLI has that writes (including `verify`, which runs a workflow's own
build/test/lint commands in the checkout, and `image build`/`gate`/`record`/
`refresh`), the `secrets` commands, `init`, `upgrade` and the hidden `exec`, the
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
| 5 — the working directory is set explicitly | the installation's checkout when it has one, otherwise a private `0700` directory the app owns under the system temporary directory, checked with `lstat` (not a link, owned by this uid, mode `0700`) rather than trusted to `mkdir`. Never `/`, which is nobody's checkout. `doctor` is called with `--dir <that directory>` and **never `--strict`**: outside a checkout its `plugin-pin` check is a warning that `--strict` turns into a failure about the checkout rather than about the environment on screen |
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
- **Persistence.** The installation store is in memory for one run; see the note
  above on rule 10.
- **The fixtures.** `FugaroSource`'s second implementation is Task 1.4's
  recorder and replay, and it slots in behind the same interface — see the
  section below. What landed there is the tool and one synthetic fixture; the
  design's eight recordings are still the owner's to make.
- **`version --json` and a schema version.** The CLI has neither (contract §4.3
  gaps 1 and 3), so the handshake is the one human line and the console refuses a
  binary whose line it cannot read rather than guessing.

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
npm run record-fixture -- --out test/fixtures/<name>.ndjson --project <project>
```

Run **by the owner**, from the sandbox checkout, with the **sandbox**
installation selected — never `belong`. The eight recordings are the owner's to
produce, and the plan attaches them to this task's pull request by hand or in a
follow-up commit. `--project` is **required and never defaulted**: falling back
to whatever the ambient selection happens to be is precisely how a recording of
the wrong project gets made (design §3.4 rule 3), so its absence is a refusal
that names the flag. The rest of the flags, and the refusals, are in
`--help` / `USAGE`.

The script is `tsc -p tsconfig.record-fixture.json && node
.record-fixture/tools/record-fixture/index.js`: the tool is TypeScript like
everything else here, and `.record-fixture/` is a build output directory that
`.gitignore` names. The recorder spawns through the console's **own**
`planFor('live.watch', …)`, so the argv, the child environment
(`CHILD_ENV_KEYS`), the working directory and `shell: false` are the console's
rules and not a second copy of them — a recording cannot be of a command the
console would refuse to run. It records for a bounded time (`--duration`, 120 s
by default), then stops the child the way the console does, `SIGTERM` and then
`SIGKILL` after `SHUTDOWN_GRACE_MS`; a torn last line is dropped rather than
recorded as garbage.

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
the only naming `fixture.test.ts` accepts beside `synthetic.ndjson` — a stray
file fails a test by name, while the owner's recordings do not have to be
preceded by an edit to the suite. Until they land,
`test/fixtures/` holds one file, and it is labelled for what it is:

```
{"fugaro_fixture":{"version":1,"redactor":"fugaro-console-redact/1","case":"synthetic",
 "project":"<project>","synthetic":true,"records":6,"sha256":"9c5effc8…",
 "note":"synthetic: invented documents put through the same redaction path as a recording; never recorded from any installation or cloud"}}
```

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
  *Field classes*, where the key names what the value is (`repo`, `slug`,
  `pr_url`, `requested_by`, `by`, …), and *pattern classes*, which apply to
  **every** string whatever its key is: an address, a token, a URL, an absolute
  path, a branch name, a repository path, and one of the installation's own
  literals. A document with a key nobody anticipated still cannot carry an
  address out of the room, and a test per class holds each one — a run title, a
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
  as it is in the object form.
- **Redaction is a marker, not a deletion, and the markers are a closed set**
  (`<path>`, `<project>`, `<host>`, `<branch>`, `[redacted-token]`,
  `[redacted-url]`, `redacted@example.invalid`, `[redacted]`). A marked value is
  unambiguous on sight, so nothing in a fixture can be mistaken for real data,
  and a value that is *already* marked is left alone — which is what makes the
  whole function **idempotent**, asserted as `redact(redact(x)) == redact(x)`.
  Two classes keep a per-value pseudonym instead of a fixed marker — a
  repository (`r-1a2b3c4d/r-5e6f7a8b`) and a slug (`slug-1a2b3c4d`) — because a
  fixture with several repositories is only worth having if the rows stay
  distinguishable. A pseudonym is a SHA-256 of the value with a fixed, public
  domain separator: one-way, so it reveals nothing, and stable, so the same
  repository is the same pseudonym in every fixture.
- **It fails closed, twice.** A line that is not a JSON object is refused rather
  than passed through, and every document is **audited** after redaction: if any
  class is still present after the rules have run — an address, a token, a URL, a
  path, a branch, a repository path, a literal, or a reserved key whose value is
  not a marker — the call throws. A bug in a rule is therefore a failed
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
written is refused too, with `FixtureRefusal` naming the class.

### The format, and what the loader refuses

A fixture is NDJSON: one header line and one line per record. The header is the
file's paperwork — the format version, **the redactor that made it**
(`redactor: "fugaro-console-redact/1"`), whether it is synthetic, which case it
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
screen as the installation's name — would otherwise pass every check above it. The redactor check is not decoration: **a
digest alone proves only that the file is the one the recorder wrote — it says
nothing about whether the recorder was the one in this repository**, so a
fixture stamped by an older or unknown redactor is refused rather than trusted.

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
misusing. `parseRecorderArgs` refuses, by name: a missing `--project`; the
project `belong`; **any `--out` outside a `fixtures/` directory or not ending
`.ndjson`**; an existing fixture without `--force`; an unknown flag; a
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
this tool will not create. The child's stderr is **drained and discarded**:
drained, because `planFor` gives the child a pipe and a pipe nobody reads fills
up and then blocks the child's write, which would stall a recording of a CLI
that logs; discarded, because a stderr line is raw CLI text rather than a feed
frame, and putting it in the fixture as a *notice* would make the fixture claim
`watch --json` wrote something it did not. It writes exactly one file: the
fixture `--out` names, and only after the text has gone back through the real
`loadFixture`.

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
| a repository pseudonym replaced by the value itself | "uses stable, one-way pseudonyms and never the value itself" |
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
  `FugaroSource` and no screen exists to consume one until Task 2.0.
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
  no `paths:` filter and no job-level `if:`, and its name is exactly `console`
  — the string Task 4.6 adds to `scripts/release-gate.sh`'s `names` list (that
  line reads `names="test terraform rules"` today, so the check is not required
  yet). From that edit on, the gate requires a check named `console` on a tag's
  commit and, in its `identical_tree_checks` fallback, the same names on the
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
and this paragraph is not a claim that it already happened: the pinned
toolchain, the hardened window, the IPC allow-list, `FugaroSource` and the
fixtures were written, reviewed and merged on the strength of a reviewer's
machine, and no workflow ran a console test on any of their pull requests.
