# The shared package

Fugaro's shared **pure-logic** package (mobile plan, Task **P0.1**; decision **D119**). It is
a **top-level directory of this repository** — `shared/` — outside the Go module and outside
`console/`, because it has two consumers rather than one: the console's voice module (Voice
D50, D53) and the mobile app (mobile plan phase 3). D119 amends D50's **location** and keeps
D50's operative rulings: the code lives **in this repository**, it is **not an npm package** —
`package.json` is `private` and the version is `0.0.0`, so nothing here is published and
nothing here is a public API — and there is **no `fugaro voice` command**.

**Status: the skeleton, and `mailbox-schema/` (P0.2, the message schema).** The
URL validator (P0.3), the mailbox state machine (P1.1), the consumer lease
(P1.2), the digest and its redactor (P1.3), the staleness rule (P1.4) and the mailbox
simulator (P1.5) are planned, unbuilt, and each is its own directory. What exists today is
the runner, the skeleton module and `mailbox-schema/`, each with its test, and this document. No task of that plan is
approved, and this directory is not evidence that one is.

`digest/` (P1.3) is the digest, the severity rule and the redactor: built, with its own tests; see `digest/README.md`.

`lease/` (P1.2) is the consumer lease, in the shape of `runstore.Claim`: built, with no consumer yet; see `lease/README.md`.

`presence/` (P1.4) is the staleness and offline rule: built; see `presence/README.md`.

`url/` (P0.3) is the URL validator: built, with an empty host allow-list until the owner answers D124; see `url/README.md`.

`mailbox/` (P1.1) is the mailbox state machine, its dedupe ledger and its replay window: built, with its own tests; see `mailbox/README.md`.

`mailbox-sim/` (P1.5) is the deterministic mailbox simulator: built, with its own tests and its own mutation proof; see `mailbox-sim/README.md`.

`mailbox-schema/` (P0.2, extended by P6.3) is the message schema and its validation, and
now the required `mac` on every message of every kind, the `agent`, `kind`, `options`,
`reply_to` and action-record fields, the canonical serialisation the tag covers and its
vectors: built, with its own tests, its own mutation proof (78 guards) and
`vectors.json`, the contract the Go consumer reimplements against; see
`mailbox-schema/README.md`. The package has one builtin import — `node:crypto`, for
HMAC-SHA-256 — and it is deliberately **not** in the module's entry point: it is in
`mailbox-schema/mac.js`, which `mailbox-schema/index.js` never imports, because React
Native's bundler resolves no `node:` builtin and the phone must still be able to import
the validator and the canonical serialisation. `index.js` imports nothing at all, a test
asserts it, and the phone implements HMAC-SHA-256 itself against `vectors.json`. There
is still no dependency: nothing is installed, nothing is fetched. Note that P6.3 changed
the schema, and P6.4 has since carried it into
`internal/budget/rules`' template: the mailbox subtree now gates the same field set —
`mac` required on every message of every kind and on the heartbeat — and
`TestMailboxRulesAgreeWithTheSharedSchema` holds the two artifacts' bounds together, and
`TestMailboxRulesCheckTheSchemasCharacters` holds the rules' character patterns to this
module's own byte for byte. What the rules still cannot do — and cannot be made to do —
is check a tag's **value**: they hold no key, the key is D171's and never passes through
the database, so P6.7 (`fugaro mailbox`) is what verifies a tag rather than merely
requiring a well-formed one.

`command/` (P6.1) is the wake-word router and the app-command grammar: built, with its own tests and its own mutation proof; see `command/README.md`.

`confirm/` (P6.2) is the spoken-confirmation state machine — the read-back, the fixed phrase, the timeout, the replay ledger and D128's refusals — with its own tests, its own mutation proof and `vectors.json` for P6.7's Go parity test; see `confirm/README.md`. It reuses `lease/`'s timestamp bound, recognises the phrase through `command/` (P6.1) and authorises `mailbox-schema/`'s action record (P6.3) field for field.

`prompts/` (P6.5) is the prompt queue — queue by default, the count across accounts at a
natural pause, "what's pending" per installation, expiry at D118's mailbox TTL, D173's two
windows, the reminder schedule and the pending cap — and the interrupt policy: D160's set of
four events read through D127's local switch. Built on `mailbox/`, `mailbox-schema/`,
`digest/` and `presence/`, with its own tests (55) and its own mutation proof (93 guards);
see `prompts/README.md`.

## The runner, and the glob

`npm test` runs `node --test "**/*.test.js"` — Node's own test runner, so the package has
**no dependencies at all** and nothing to install.

**The glob is the point of P0.1.** It is what lets every later module land in its own new
directory without any of them editing this package's manifest: the plan's "shared files"
table gives `shared/package.json` and the runner config to P0.1 alone, and says why — a
script that named its test files would collapse the plan's parallelism, because every module
would then edit one file.

- **The plan spells the glob `shared/**/*.test.js`.** This script runs with the package
  directory as its working directory (npm sets it), so `**/*.test.js` is the same set of
  files, and it is quoted so the shell cannot expand it: node expands it itself, recursively,
  which is what reaches `shared/<module>/<anything>.test.js`.
- **The naming rule: every test file in this package is `<something>.test.js`, inside the
  module's own directory.** A file named anything else — `*.spec.js`, `test-*.js` — is a file
  the runner never runs.
- **One caveat, stated because it is the one way this runner can lie:** `node --test` exits
  **0** when a glob matches no file at all, having run nothing. A green run over an empty
  directory is not evidence. That is why the skeleton's test was written before its module
  (`shared/skeleton/index.test.js`, whose red run is P0.1's red step): the first test file is
  what makes the glob real, and a module whose tests are misnamed would look exactly like a
  module that passes.

## Toolchain

**Node 22**, pinned in `package.json`'s `engines`: `>=22 <23`. The floor is the major line
this package's runner is written and checked on, and the ceiling is the same line, which is
what CI's `setup-node` pins (`node-version: "22"`). The runner matters here rather than being
a detail: the script hands `node --test` a **glob pattern** rather than a file list, and the
expansion is node's own — Node's documentation states the pattern quotedly
(`node --test "**/*.test.js"`) so that the shell cannot expand it first. The skeleton was
checked on **Node 22.22.0 / npm 10.9.4**, the version this directory was written with.

`shared/package-lock.json` is **committed**, as every other package in this repository
commits one (`website/`, `internal/budget/rules/emulator/`, `deploy/sandbox/`), so that
`npm ci` is a valid command for a CI job. It pins nothing: the package has no `dependencies`
and no `devDependencies`, and the lockfile carries the root entry alone. There is no
`node_modules/` and no ignore file for one, because there is nothing to install — a task that
adds a dependency adds both, with its reason.

```sh
cd shared && npm test   # the package's tests, with no install and no network
```

**Nothing here needs the network, a cloud, a device or a credential**, and nothing here reads
or writes a database. The tests are functions over values, which is what D126 asks of
everything that can be one.

## What belongs here

Pure logic and nothing that needs a runtime (D119): the message schema and its validation,
the mailbox's semantics, the digest, the classifier, the redactor, the staleness rule and the
URL validator. Nothing in this package imports Electron, React Native, a database client or a
network client — and the reason is not tidiness: the console and the phone must not be able
to disagree about what is safe to say out loud, which is why the redactor lives here rather
than in either consumer (P1.3).

## Why the Go module does not see this directory

`shared/` is a second artifact outside the Go module, like `console/` and `website/`:
`go build ./...`, `go vet ./...` and `gofmt -l .` take no notice of it, `go.mod` and `go.sum`
gain nothing from it, and no Go file in this module imports it. The Go boundary tests
(`internal/cli/docs_console_boundary_test.go`, `internal/cli/docs_website_boundary_test.go`)
are about Go **code** — imports, `go.mod` and `go:embed` — and this directory holds none, so
P0.1 changes no Go file and extends no allow-list. The check the plan names for that claim
is `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`.

## Two honest gaps

- **No CI job runs these tests yet.** P4.3 is the job that will, and it is **owner-made** —
  the mobile plan's Global Constraint 8: no agent edits `.github/workflows/`, because GitHub
  refuses Fugaro's push of workflow changes. Until it exists, this package's tests run only
  where someone runs them, which the plan records as a real gap in its coverage rather than a
  detail.
- **The package has no consumer today.** "Shared" is a claim about the future: the console's
  voice module does not exist (Voice D50, D53), so the second consumer is a plan, not a
  directory (plan open item 7).

Where the reasoning lives: [implementation-plan.md](../docs/mobile/implementation-plan.md)
(P0.1, and the "shared files" table that gives this package's manifest to this task alone),
[reconciliation.md](../docs/mobile/reconciliation.md) (D119, and D50's amendment),
[voice.md](../docs/design/voice.md) (D50, D53).
