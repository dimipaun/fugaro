# Task C1.4 — the deliverable is already on the base

This run was asked to implement **Task C1.4** of
[implementation-plan.md](implementation-plan.md) — *the owner-run helper for
Check 92 — SECURITY-SENSITIVE (test only)* — whose whole deliverable is one new
file:

> **Files.** `internal/e2e/live_creds_rules_test.go` (**new**, build tag `live`,
> on the `live_budget_test.go` precedent).

It is already on the base (`origin/integration-0.9.6`). This run's brief directs
that when the deliverable is already on the base, nothing is implemented and the
finding is recorded in a docs-only note instead. So **nothing was implemented and
nothing in the deliverable was changed**: this run adds only the note below. (The
plan itself carries no first-check rule — not in conventions 1–11, Global
Constraints 1–12, the Review Focus or the owner approval gate; the instruction is
the brief's.) No code, rules, schema, Terraform, skill or workflow file is
touched.

## What was checked, and what it answered

| The plan says | On `origin/integration-0.9.6` |
| --- | --- |
| `internal/e2e/live_creds_rules_test.go` is **new** | Present, 918 lines, first line `//go:build live` |
| It declares `TestLiveCredsRules` | Present, `internal/e2e/live_creds_rules_test.go:164` |
| Commit message `e2e: the owner-run live helper for Check 92` | `e186dd75` — *"e2e: the owner-run live helper for Check 92 (#733)"*, 2026-10-20 |
| **Depends on.** C1.3 | C1.3 is on the base too (see below) |
| **Verification.** `go vet -tags live ./internal/e2e/... && go test ./internal/e2e/... -count=1` (the untagged package still builds) | Both halves pass (run for this note; see below) |

## Evidence

### 1. The file is on the base, byte for byte

```
$ git ls-tree -r origin/integration-0.9.6 --name-only | grep internal/e2e/live_creds_rules_test.go
internal/e2e/live_creds_rules_test.go

$ git diff origin/integration-0.9.6 -- internal/e2e/live_creds_rules_test.go
(empty)
```

The working tree at the start of this run was clean and `HEAD` was
`8a063c902ff80ddc70fa9c34d489f16c721f9568`, the same commit as
`origin/integration-0.9.6`, so the file in the tree is the file on the base.

### 2. The commit is the task's own commit

```
$ git log --format='%h %ad %s' --date=short origin/integration-0.9.6 -- internal/e2e/live_creds_rules_test.go
75738ee7 2026-10-21 # Task: leftover minors before the 0.10.0 tag: credentials / managed le… (#783)
e186dd75 2026-10-20 e2e: the owner-run live helper for Check 92 (#733)
```

`e186dd75`'s subject is the plan's `**Commit.**` line for C1.4 verbatim; the
later `75738ee7` is the 0.10.0 leftover-minors pass, which edited the file but
did not add it.

### 3. The interfaces the plan names are all there

The plan's **Interfaces** line for C1.4 asks the helper to mint a sandbox run's
identity from the owner's environment variables (database URL, Firebase API key,
token signer — Check 21's shape), exchange it, attempt each denied read and write
under `creds/` and each allowed and refused write of the new children, print
`FACT:` lines, and write nothing that survives. The file does each:

- **The environment, and the hard-coded guard.** `crTargetFrom(os.Getenv)` reads
  `FUGARO_LIVE_CREDS_RULES_OK`, `FUGARO_LIVE_INSTALLATION`,
  `FUGARO_LIVE_RTDB_URL`, `FUGARO_LIVE_FIREBASE_API_KEY`,
  `FUGARO_LIVE_TOKEN_SIGNER` (and the optional `FUGARO_LIVE_REQUESTED_BY`), the
  same variables as Check 21's helper; an aimed run from a coding agent's
  session, any installation but `fugaro`, any database host but the sandbox
  project's, and any signer that is not a sandbox service account are refused
  before a single request, by `crTargetFrom` in `creds_rules_guard_test.go` (no
  build tag, so ordinary tests cover it). An environment aimed at a database
  without `FUGARO_LIVE_CREDS_RULES_OK=1` is refused; an unaimed one skips.
- **Minting and exchanging.** The check mints a run token, exchanges it for an
  ID token, and records the session (`facts.fact(...)` naming the uid, the
  expiry and the `fs`/`fr`/`fx`/`fp`/`rb` claims).
- **Every denied read and write under `/creds`.** It seeds the `/creds` nodes as
  the owner, then has the run's identity attempt every read and every write:
  `"a run's identity read nothing under /creds: %d reads refused (permission
  denied), including the marks, the leases, its own sticky credential and the
  round-robin counter"` and `"the rules refused %d writes a run's identity must
  not make: ..."`. A second identity carrying another launcher's `rb` is refused
  the same way.
- **The new children, allowed and refused.** It writes the run's own `credential`
  child (accepted), pins the RBOK refusal (a token of the run carrying another
  launcher's `rb` is refused that child while the entry names the other
  launcher, then accepted once the entry names it), and reads
  `config/limits/credsRules` and `config/limits/policyRules` as the run's
  identity.
- **`FACT:` lines.** Printed through `crFacts.fact` (`crFacts.fact` at
  `internal/e2e/live_creds_rules_test.go:126`), each checked against the secrets
  by `defer facts.checkSecrets()` — no credential value can reach a line the
  owner reads.
- **Nothing survives.** A throwaway run identity and two scratch repositories,
  with every written path collected up front (`crWritten(n)`) and deleted as the
  owner by `crCleanup.run`, which reads each back absent and reports it; the
  Firebase Auth uids the exchanges can create are derived from the names before
  the first exchange and deleted with `accounts:batchDelete`, so an exchange that
  creates an account and then fails still leaves nothing behind.

### 4. C1.4's dependency, C1.3, is on the base too

The rules task C1.4 depends on is implemented, so the helper is not orphaned:

- `internal/budget/rules/marker.go` — `PathCredsRulesMarker()` returns
  `"config/limits/credsRules"` (line 65) and `CredsRulesVersion = 1` (line 77).
- `internal/budget/rules/creds_emulator_test.go` — carries the plan's named
  cases: `TestCredsRunCannotReadAnything`, `TestCredsRunCannotWriteAnything`,
  `TestCredsRunCannotDelete`, `TestCredsTokenOfAnotherRepositoryDenied`,
  `TestRunTokenCannotWriteTheCredsRulesMarkers`,
  `TestAgentEntryCredentialNameValidated`, `TestAgentEntryWaitingUntilValidated`,
  `TestByTierCountersOwnRunOnly`, `TestByTierCountersMonotonic`.

### 5. The helper is already pinned by an untagged test

`internal/e2e/creds_rules_pure_unit_test.go` (1202 lines, no build tag, so it
runs in ordinary CI) reads `live_creds_rules_test.go`'s source and holds its
decisions — including that the check builds its Firebase Auth user list from
`crAuthUIDs(n)` before its first exchange. The plan's "no run in CI" is met by
the `live` tag; the file is still compiled in CI's lint.

## Verification run for this note

The repository's configured commands, recorded by `fugaro verify`:

```
fugaro verify build   # go build ./... && go vet ./... && gofmt check — PASSED
fugaro verify test    # go test over every package but runner/e2e/image/images — FAILED
```

**`fugaro verify test` fails on this base, and the failure is not this note's.**
The one failing test is
`internal/cli`'s `TestDocsCoordinatorNumbersTableIsReadFromItsSources`:

```
docs_coordinator_test.go:1609: docs/coordinator/README.md's checks row says the
highest check section written is 90, and the register's highest `## Check`
section is 91: the row is a reading of the register, and the two move together
```

It is a pre-existing inconsistency of `integration-0.9.6`, and this note's file
is not one it reads (the test reads `docs/coordinator/README.md` and
`docs/gcp-live-checklist.md`). Proof — the same test on a clean checkout of the
base, with this note's commit absent:

```
$ git worktree add /tmp/basecheck origin/integration-0.9.6
$ cd /tmp/basecheck && go test ./internal/cli/ -count=1 -run TestDocsCoordinatorNumbersTableIsReadFromItsSources
--- FAIL: TestDocsCoordinatorNumbersTableIsReadFromItsSources (0.04s)
    docs_coordinator_test.go:1609: ... the highest check section written is 90,
    and the register's highest `## Check` section is 91 ...
FAIL
```

The cause is C0.3's own commit, `2d424f30` *"docs(credentials-service): Task C0.3
— Checks 90 and 91 written into the live-check register (#727)"*: it wrote
`## Check 91` into `docs/gcp-live-checklist.md` but left
`docs/coordinator/README.md`'s Checks row reading *"The highest check section
written is 90 — the credentials service's header swap, written and run on
2026-10-21"*. `origin/main` carries neither that row's claim nor the
`coordinatorHighestWrittenRE` rule that reads it (its `docs_coordinator_test.go`
has no such rule and passes), so the rule arrives with this branch; the row and
the register disagree on it. This run does not touch it: the row is another
work's document, and the plan's Global Constraint 1 scopes a credentials-service
docs change to `docs/credentials-service/`.

The plan's own C1.4 verification is both halves of its line, and both were run
directly for this note:

```
go vet -tags live ./internal/e2e/...   # PASSES: the live file compiles, and no check runs
go test ./internal/e2e/... -count=1    # PASSES: ok, 47.8s — the untagged package builds
                                       #   and its 66 tests pass
```

The repository's configured test command leaves `internal/e2e` out, so
`fugaro verify test` does not cover it. `fugaro.yaml` gives the reason as time:
it names `internal/runner` and `internal/e2e` as "about 12 and 6 minutes each",
and attaches the Docker-and-a-git-checkout clause to `internal/image` and
`images` — the base-image builds, whose tests are the ones that cannot run in
this container. The untagged `e2e` package needs neither Docker nor a checkout,
which is why the two commands above run here at all. A first draft of this note
called the untagged run "CI's, not this container's" and skipped it; that was
wrong, and the commands are now run and reported as the plan writes them.

## What this run does not do

Per the plan's Global Constraint 1 and this run's brief: no Go, rules, schema,
Terraform, skill or workflow change; no live cloud action; no secret read or
handled; `belong` untouched. The owner-run Check 92 itself (C1.6) remains the
owner's, at the owner's terminal, on installation `fugaro` and the sandbox.
