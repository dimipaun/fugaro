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
(`docs/json-contract.md`, pinned by plan Task 0.1), the pinned toolchain (plan
Task 1.0): `package.json`, `package-lock.json`, `tsconfig.json`,
`playwright.config.ts`, `.npmrc`, `.gitignore`, and the two unit tests that hold
the pinning rule and the lockfile; and the hardened window (plan Task 1.1,
below): `src/main/window.ts`, `src/main/protocol.ts`, `src/main/index.ts`,
`src/renderer/index.html`, `tsconfig.build.json`, and the two test files that
try to leave it.

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
npm run build                                  # tsc -p tsconfig.build.json, src/ -> dist/
npm run test:e2e                               # playwright, test/e2e/*.spec.ts; builds first
npm run test:e2e -- security                   # just the window's spec
npm run typecheck                              # tsc --noEmit over src/, test/, tools/
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

### The pinning rule is a test, not a habit

`test/unit/pinning.test.ts` reads `package.json` and fails on any specifier that
is not an exact version — a `^`, a `~`, a `*`, `latest`, or any other range —
naming the dependency and the marker, and it holds the checker itself to a table
of specifiers it must accept and refuse, so "package.json is clean" cannot pass
on a checker that accepts everything. `test/unit/lockfile.test.ts` holds the
other half: the lockfile exists, is `lockfileVersion` 3, declares exactly the
names and specifiers `package.json` declares, locks each of them to its pinned
version with an `integrity` hash, and resolves **every** entry — the manifest's
six names and all ~95 transitive packages, which are code that runs just the
same — from `https://registry.npmjs.org/`, with nothing floating anywhere.
`.npmrc`'s `save-exact=true` is the same rule enforced by the client, so a later
`npm install <package>` writes an exact pin and not the first range a test would
have to catch.
