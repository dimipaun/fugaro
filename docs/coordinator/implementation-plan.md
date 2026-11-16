# The coordinator — the implementation plan

**Status (2026-10-24): approved to build on the branch `coordinator`, target 0.11.0; partly implemented — P0, P1, P2, P3, P4 and P5 are on `coordinator`, and P6–P8 are not started.** Each part carries its own **Status** line below, and this one moves with them: the status line and the pins that hold it change in the commit that lands a part, which is what the P0 section says of itself and what every earlier plan in this repository did (`docs/mobile/README.md`, `docs/setup-wizard/README.md`). This plan follows from
[reconciliation.md](reconciliation.md), which wins wherever it disagrees with
[spec.md](spec.md). Every path and symbol below was re-read against `main` at `1e0953b7`
(`docs(central-orchestrator): spec, reconciliation and plan for central orchestrators (#775)`)
on **2026-10-21**, and is cited by **path and symbol, never by line number**. A path marked
**new** does not exist yet. **Owner: Dimi Paun. Date: 2026-10-21.**
**Amended 2026-10-21**: the owner answered **D193–D197**, four as recommended and **D196 against
the recommendation**, and passed this plan's **owner approval gate** for the parts that need only
those decisions — **P0–P6**. **P7 and P8 still wait**, on the front-door design review and on
nothing else, and §11 says so in full — with the owner's **U2/U3** checks staying open beside
them.

**Every part is independently mergeable**, on the long-lived branch **`coordinator`** that this
work creates: a part lands, it is verified, and the tree works with it and without the parts
after it. **Target release: 0.11.0**, and no part assumes a release that has not shipped, so the
branch is mergeable into whatever release is current when the owner asks for it.

**The plan is written to two things, and the owner has answered both.** The first is the owner's
own answers to **Q1–Q16** of the reconciliation, given on **2026-10-19**, each taking **option
(A)** — that is what the parts mean by "the front door is a service", "the store is the Realtime
Database", "there is no model credential in v1". **An answer is not an approval**: the owner
answered a question, and this plan is the document that turns those answers into work. The second
is the **five new decisions the planning found** — **D193–D197**, recorded in
[reconciliation.md](reconciliation.md) §6 — which the answers do not settle, and which the owner
**answered on 2026-10-21**: four by taking the recommendation and **D196 against it**. On those
answers the owner passed this plan's **gate** (§11) for the parts that need only them, **P0–P6**;
**P7 and P8 still wait**, and each part names what it waits on.

**Re-planned 2026-11-07 (D434, D435; T10, T20): the parts still ahead are business-licensed, and
land behind `ee/register`.** The owner's rulings of 2026-10-27 put this work in the
business-licensed folder — the **command and the library** first (D434/T10, landed as the ee-reorg
plan's E2.1b: `ee/coordinator/`, its `ee/coordinator/cli/` package and `ee/coordinator/testdata/`),
and everything
still ahead of it now (D435/T20). **P6, P7 and P8 are therefore re-targeted to `ee/`**, and this is
what that changes:

- **The Go half of each part lands under `ee/coordinator/`**, reached only through `ee/register`:
  the seam is `cmd/fugaro/ee_on.go`, the one file the `ee` build tag compiles and whose blank import
  runs the registry's `init`, and the registry is what fills the core's set-once setters
  (`internal/cli/extend.go`'s `RegisterCommand` and `SetLaunchDeclarer`). An untagged build keeps the
  core's stub for the name — it exits non-zero and says why (E1.1, D522) — so the console's sidecar
  and a launch's declarer reach the real command only in the tagged binary.
- **The Terraform does not move.** `deploy/terraform/` is embedded in the Go binary and applied by
  the MIT `fugaro update`: the installation's own root is not licensed code, so the service a part
  creates is written under `deploy/terraform/gcp/modules/installation/`, and the allow-list that
  admits a resource type stays the core's own (`internal/infra/tf/cover.go`).
- **The console half stays in `console/`**, which is business-licensed already, reading the moved
  command through the CLI sidecar it spawns — which is why P6 still adds no credential, no network
  surface and no CSP change.
- **What the parts have been overtaken by is recorded under each of them**, in a dated note rather
  than by rewriting the part: the front door's service, its authorization and its MCP half have all
  moved on since P7 and P8 were written, and the MCP half is planned in
  `docs/mcp-server/implementation-plan.md` rather than here. **A question the re-plan finds genuinely
  open stays open** — the two P7 has are named under it rather than silently decided.
- **#831 closes as superseded (T20), and that is OWNER-RUN.** The pull request is already closed —
  the census of 2026-11-07 (`docs/subscriptions/status-2026-11-07.md`) records it closed on
  2026-10-27 — but the comment T20 names is not on it, and posting one is a person's:
  `gh pr close 831 --comment "Superseded: re-planned in ee/ (T20)"`. This re-plan neither posts it
  nor records it as done.

## 1. What changed since the reconciliation, and what this plan does about it

The reconciliation read the code at `c6009b5f` on 2026-10-19. Four things have landed or moved
since, and each one changes this plan rather than decorating it:

1. **`docs/central-orchestrator/` exists** (merged as #775, the commit this plan is based on). It
   plans a **resident service per installation** that consumes work and launches jobs, and its
   **P9** says the orchestrator *"reports to the coordinator (D14): what it launched, what prompts
   are open, and who holds which lease — a writer of the project-wide record, taking nothing back,
   defined in the coordinator's documents rather than invented here"*. That plan recorded P9's
   coordinator half as **blocked on this document existing**, and its §3 names the coordinator's
   plan as one of the two siblings it depends on. **P4 below is that missing definition**: the
   report path, its record and its idempotency, written so that one writer serves every
   orchestrator — the local one today and the central one when it exists. **Superseded
   2026-10-21**: that P9's "Depends on" line now records that this document exists and that its
   P4/P5 no longer block the coordinator's half — the sibling plan was edited by this change, and
   a reader following the citation above must not find the opposite of what this item says.
2. **The same plan's P4 builds the first Cloud Run *service* in Fugaro — and its P1 admits the
   type.** Q10(A) answered that one service per installation would serve both, the coordinator
   being a role of it, and this plan was written to that reading until **2026-10-21**, when the
   owner answered **D196 option (B)**: the coordinator is **not** tied to the central
   orchestrator, because *"there can be multiple central orchestrators in the near future"* —
   **the owner's words of 2026-10-21**, recorded in the reconciliation and nowhere else, since
   the answer arrived as a ruling rather than as a document. So
   the coordinator gets **its own resident service**, built by **P7** of this plan, with its own
   identity, its own scale-to-zero configuration and its own ID-token front door — and **P7 and
   P8 no longer wait on that plan's P4 and P5**. One thing is still shared, and it is the one
   thing two plans cannot both do: the allow-list admission of `google_cloud_run_v2_service`,
   which **whichever part lands first admits it and the other cites it** — that plan's **P1** or
   this plan's **P7**. Q10's recorded answer of 2026-10-19 is **amended, not rewritten**, in the
   reconciliation, which is where the owner's own words stay.
3. **`docs/credentials-service/` is still plan-only** — its own README says so, its D130–D154
   were answered on 2026-10-17 and its Q6 was ruled on 2026-10-19, and nothing is implemented.
   Q4(A)'s "no model credential in v1" is therefore not a deferral this plan invented: it is the
   only option that does not depend on unbuilt work, and §3 below keeps the model half out.
4. **The mobile work's pairing and allowlist landed** (D165/D166; Phase 6's P6.4 merged as #601
   with the device node and the pairing nodes, P6.5 as #602). The machine that authenticates the
   people this plan grants a read to is `config/admin/devices/$uid`: `internal/budget/rules/rules.go`'s
   `userClaim` makes the rules' `USER` macro require that node to exist, revocation is deleting
   it, and **the node itself is unreadable to the person it names** (the template writes
   `.read: false` on it). That last part is why the coordinator's state is a **separate** subtree
   and not a child of the allowlist (D195): Q15(A)'s read grant is a question about a subtree,
   and the identity behind it already exists and is already revocable.
5. **The rigor modes work is a plan in open PR #773 and is not on `main`.** It is named here
   because the register's numbering turns on it (§9) and because its guard — the pinning test
   this folder's own guard would copy from — is not in the tree to copy from. The models to
   follow are `internal/cli/docs_coordinator_test.go` and `internal/cli/docs_windows_cli_test.go`,
   both on `main`.

Two things the reconciliation recorded are **still true and still shape this plan**: the
sibling's *"never a model credential in the resident service"* (its §6 rule 4, which is Q4(A) and
is why §3 is short), and the register's rule that a check number is taken by the phase that ships
it and never renumbered (which is why §9 proposes checks by name).

## 2. The order, and why it is this order

Five dependencies force the order, and nothing else in the plan is arbitrary:

1. **Nothing can be built on documents nobody has pinned.** Every other plan folder in this tree
   guards its own documents with a test — 49 of them live in `internal/cli/` today, from
   `internal/cli/docs_artifact_runs_test.go` to `internal/cli/docs_windows_cli_test.go`. This
   folder has one, and it holds the spec, the reconciliation and the index — **and not this
   plan**, which is why this plan's own arrival is the first part. That is **P0**.
2. **Nothing can be decided about the state until the view's shape is known.** The store, the
   nodes and the writer identity (P3) are all downstream of what the coordinator actually
   records, and the smallest true statement of that is the **join** of the project's pull requests
   with its run records — which needs no store, no service and no credential (Q13 A). That is
   **P1**, and it is deliberately the part that can start before anything else exists.
3. **Nothing can be read project-wide until the credential's reach is decided.** One repository
   per token is what `internal/gitprov/github/apptoken.go` can express today; a project-wide
   token is not, and whether it can be is **U3**, which this container could not verify. That is
   **P2**, and it is the first part behind a new decision (**D193**).
4. **The store arrives with its first consumer.** The coordinator's state (P3) is not built
   because the spec has a State section; it is built because a record has a writer (P4) and a
   job that reconciles it (P5). Building the nodes first would be building an empty store.
5. **The service comes last, and on purpose.** Q1(C) — "the service first" — is the option the
   reconciliation refused: it makes the hardest part, authentication, the first thing built,
   before any deterministic value exists. So the deterministic half (P1–P6) ships first, and the
   front door (P7–P8) is the part that waited on **D196** — answered on 2026-10-21, and answered
   as **this work's own service**, so it waits on nothing else now but the front-door design
   review §7 asks for.

## 3. What is deliberately not in this plan

Named, so that a reader does not look for it and a later writer does not re-add it by accident:

- **The model half in any form.** Q4(A) chose no model credential in v1; that removes conflict
  detection by a model, the *"ask the coordinator"* answer (Q8 A defers it to a second ruling),
  the reasoning tier, and any feeding of override reasons into a prompt (Q14 A). The spec's §8,
  and the two-tier config with it (Q9 A), arrive in the release that has a model — which is the
  release after `docs/credentials-service/`'s first slice exists.
- **The spend cap** (Q11 A): there is nothing to cap while there is no model credential, and the
  reconciliation's finding #25 says the mechanism does not exist either way.
- **Firestore, its listeners and its transactions** (Q5 A, Q6 A, finding #7, #16): the store is
  the Realtime Database, and the concurrency idiom is the one the tree has.
- **Webhooks, a public endpoint, and the MCP specification's OAuth 2.1 authorization** (Q7 B,
  Q16 B): polling is the intake, and Cloud Run IAM invoker is the authorization, recorded as a
  deliberate departure.
- **Any `fugaro run` authority in the coordinator** (Q10 C, refused): the coordinator is advisory,
  it never blocks or reorders a merge, and it holds no write to GitHub (Q3 A). **The second
  resident service is NOT on this list any more**: it was, while the plan was written to Q10(A),
  and D196's answer of 2026-10-21 is what took it off — P7 builds it, and it is the coordinator's
  own.
- **Bitbucket** (Q12 A): refused with a line naming the reason, because its credential is per
  repository and cannot be widened to a project (finding #30).
- **The phone's own reads** (Q15 A grants them, and D112's direct reads are the mobile work's
  port design, not this one's), and **the pilot of `fugaro init`'s replacement**: this plan adds
  a stage to `internal/initflow/stage.go`'s list wherever it needs one, and does not touch the
  wizard work's own parts.

## 4. What the owner must answer before any part starts

**Answered 2026-10-21: all five.** The plan was written to the recommendation in each decision,
and a part whose decision was answered the other way is rewritten before it starts — which is what
**P7** and **P8** are, rewritten here for **D196's option (B)**. **Five of these were new** — they
were found by planning and are numbered in [reconciliation.md](reconciliation.md) §6 — and **one is
the set of answers the plan already stands on**. The answers are in the table's first column and in
full under each decision in §6 of the reconciliation.

| Decision | What it blocks | The recommendation the plan is written to |
| --- | --- | --- |
| **Q1–Q16**, answered 2026-10-19, option (A) each | the plan's whole shape | the front door is a service in the installation root with the aggregation a scheduled job (Q1); the caller is a per-person IAM invoker with a CLI that mints the ID token (Q2); the credential is the existing App and a named read-only set, never a write (Q3); no model credential in v1 (Q4); the Realtime Database (Q5); deterministic IDs, ETag CAS, no new lock (Q6); Cloud Scheduler polling (Q7); the console reads through the CLI sidecar (Q8); two named model slots and an explicit `allow_data_to` (Q9); **Q10's answer is amended by D196** — the plan no longer reads it as one service playing both roles; no cap yet (Q11); GitHub only, Bitbucket refused by name (Q12); v0 is a read-only aggregation (Q13); `record_override` writes and is shown, nothing is fed back (Q14); the coordinator is the only writer and the CLI and phone get a read grant (Q15); streamable HTTP with IAM invoker as the authorization (Q16) |
| **D193** — answered 2026-10-21, option (A) | P2 | **one read-only installation token per repository**, minted through `MintInstallationToken` and cached per repository; the project-wide token only once U3 is settled |
| **D194** — answered 2026-10-21, option (A) | P3, P5 | **the aggregation job's own service account holds `roles/firebasedatabase.admin`** beside the history account's, and the rules change is the read grant only |
| **D195** — answered 2026-10-21, option (A) | P3, P4, P5 | **a new top-level `coordinator/` subtree**, `coordinator/project/` and `coordinator/repos/<slug>/`, deterministic document IDs, `GetETag` + `PutIfMatch` |
| **D196** — answered 2026-10-21, option (B), against the recommendation | P7, P8 | **the coordinator's own resident service**, built by this work in P7, with its own identity, its own scale-to-zero configuration and its own ID-token front door — the recommendation was the sibling plan's service, and the owner ruled the other way, so **P7 and P8 no longer wait on that plan's P4/P5** |
| **D197** — answered 2026-10-21, option (A) | P8 | **the last 200 overrides per project**, newest first, no expiry in v1, readable by the CLI and shown as plain text |

Two things the table cannot express, said here so they are not lost:

- **D193, D194 and D197 are decisions about a compromise, not about convenience.** D193 decides
  what one stolen read token reaches; D194 decides what one compromised service account can write
  in the project's database; D197 decides how much attacker-writable text the coordinator keeps.
  Each recommendation is the smallest of its options, and each alternative is a real security
  posture rather than a worse default. **D196 is not one of those**: it is a decision about
  ownership, and the owner took the option that costs a second identity, a second scale-to-zero
  configuration and two things to keep in step.
- **The gate's passing is not a permission to start P7 and P8.** They still wait — on the
  **front-door design review** (§7 asks for an independent review of each, and the service's own
  design is what that review has to see), and on nothing else. The owner's **U2** and **U3**
  checks stay open beside them (§9: the App's actual `pull_requests: read` grant, which P2's
  credential turns on, and whether an installation token may omit `repositories`, which is what
  D193 leaves the project-wide token waiting for). **No part waits on the central orchestrator
  plan**, which was the other half of P7's and P8's block before 2026-10-21.

## 5. The parts

Every part has the same shape: **Scope**, **Files** (the paths and symbols it touches),
**Red step** (the failing test written first, and the mutation that proves it), **Verification**,
**Depends on**, **Reviewer tier**, **Security-sensitive**, **Commit**.

Reviewer tiers are this plan's own vocabulary: **standard** (one reviewer, the usual path),
**careful** (a reviewer who reads the record, the rules or the plan), and **SECURITY-SENSITIVE**
(an **independent review by a reviewer who did not write it, on a different model** — this plan
asks for Opus — with its own paragraph in the pull request body saying what was reviewed and what
was not).

**P0–P6 are approved to build**, on the answers of 2026-10-19 and 2026-10-21, and P1 is the part
to start with. **P7 and P8 still wait** — on the front-door design review §7 asks for, and on
nothing else; the owner's U2/U3 checks (§9) stay open beside them.

---

### P0 — Pin this folder's plan, its parts and its new decisions

**Scope.** The folder's guard (`internal/cli/docs_coordinator_test.go`) holds the spec, the
reconciliation and the index, and does not hold this plan. The part extends it: the plan exists
and carries this document's status line; its parts are `P0`…`P8`, contiguous and in order; the
gate section is headed exactly as §4 heads it; every citation the plan makes resolves on disk,
with the paths the plan marks **new** excused **by name and only while they do not exist**; every
`` `path`'s `symbol` `` pairing the plan writes is held to that file — the shape the plan uses for
almost every symbol it names; and the reconciliation's new decisions are **D193–D197**,
contiguous, each with a recommendation first. **What the pair of checks does not hold is stated
rather than implied**: a symbol named beside a citation without the possessive form ("minted
through `MintInstallationToken`" after a path) is outside both, because pairing an identifier with
a citation there needs a notion of scope the prose does not carry — the same hole the
reconciliation's citation check records in its own comment, and the reason this part does not
claim to check "every symbol". **The edit to the guard is part of this part, not a side effect of it**: the index's status
line and its "no D-number" sentence change with the plan (below), and the pins that hold them
change with it.

**Files.** `internal/cli/docs_coordinator_test.go` (the new plan test, the widened citation check
and the two rewritten pins), `docs/coordinator/implementation-plan.md` (this file),
`docs/coordinator/reconciliation.md` (§6), `docs/coordinator/README.md` (the status line, the
file table and the numbers table).

**Red step.** Two mutations on citations, and they are different checks. Rename a **path** one of
the plan's citations names — `internal/gitprov/github/apptoken.go` moved to a name that does not
exist, in a scratch tree — and the citation check must fail, naming the path. Rename a **symbol** one of the plan's
possessive pairings names — `GetETag`, in `internal/rtdb/client.go`, which the plan cites as
`` `internal/rtdb/client.go`'s `GetETag` `` — and the symbol check must fail, naming the symbol
and the file: a path that still resolves while the symbol behind it has moved is the half of this
that a path check alone cannot see, and it is why the pairing exists. Restore. Then: delete a part
heading; change one byte of `docs/coordinator/spec.md`; put a line number on a plan citation;
delete one of the new decisions from the reconciliation. Each must fail. Then the two pins that
moved: restore the old status sentence, and restore the "no D-number" sentence, and see each fail.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./internal/cli/ -run
TestDocsCoordinator`; and the register-pinning tests of the same package unchanged (this part
touches no file outside `docs/coordinator/` and its own guard, so they are run to prove it).

**Depends on.** Nothing. This is the only part that depends on nothing at all.

**Reviewer tier.** Standard. **Security-sensitive.** No.

**Commit.** `cli: pin the coordinator's plan, its parts and its new decisions`

**Status: written.** The guard this part describes is in the tree already — it landed in the
same change as the plan, because the pins that hold the index's status line and its numbers
table would otherwise have been left red while this document sat in review. It is a part here
for the reason every other part is: the plan has to account for the work the plan itself needed,
and it would be dishonest to list eight future parts and quietly leave out the one that was
already done.

---

### P1 — The view, computed: the shape, and the one-repository join

**Scope.** The deterministic half of Q13(A), and nothing else: what the coordinator *shows*, as
data, with no store, no service, no MCP and no model. A new package `ee/coordinator/` holds
the view's types — one repository's record: its pull requests, the runs behind them, the branch
and the target release each declares, the files and services each touches, and the **collisions**
between records that are still in flight — and the pure join that builds it from two readers the
tree already has: a `gitprov.Provider` (`internal/gitprov/gitprov.go`), opened exactly as
`internal/cli/exec.go`'s `providerOptions` opens one, and the run store
(`internal/runstore/list.go`). One new read command, **`fugaro coordinator status [--json]`**
(new `ee/coordinator/cli/coordinator.go`, registered in `internal/cli/root.go` beside `newWatchCmd`
and `newProjectCmd`), renders it. It reads; it writes nothing, anywhere.

**Files.** New `ee/coordinator/` (the view's types, the join, the collision rule); new
`ee/coordinator/cli/coordinator.go` and one line of `internal/cli/root.go`; fixtures under
`ee/coordinator/testdata/`.

**Red step.** Three tests, each failing before the code exists: two in-flight pull requests whose
tasks name the same file produce **one** collision and the test names both; a run whose
`launched_by` is the self-declared string finding #11 describes is reported as **declared** and
never as an identity; a pull request with no run behind it is listed as such rather than dropped
(mutate the join to skip it and the test must fail).

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./ee/coordinator/...
./internal/cli/ -run TestCoordinator`; and a fixture-driven goldens read for the rendered view.

**Depends on.** **Nothing but its own answer.** Q13(A) is answered, and this part reads only what
`fugaro` already reads. **It needs no new decision, and it is the first part that produces
something a person can look at.**

**Reviewer tier.** Standard. **Security-sensitive.** No — it adds no network surface, no
credential and no write; its GitHub access is the per-repository read the CLI already performs.

**Commit.** `coordinator: the project view's shape, and the one-repository join`

**Status: built.** On the long-lived `coordinator` branch, in this plan's own commit message. The
package, the join, the collision rule, the fixtures, the command and its registration are in the
tree, with the command's own twelve tests and the package's ten; the plan's status line above moved
with them. The review of this part left four things, and they are in that count: the service a path
belongs to is two segments under the trees that hold more than one unit (`internal/`, `cmd/`) and
one segment everywhere else — the first segment alone put nearly every path in this repository in
the one service `internal`, so every pair of pull requests working in unrelated packages was
reported as colliding — the pull requests are read a few at a time rather than one after another,
the command's own help and the reference say which pull requests it can list at all, and a run
whose stored task and result records are half-written is listed as work in flight rather than
dropped. The one thing this part does NOT do is stated where a reader meets it rather than
here: `gitprov.Provider` reads one pull request by number and cannot list a repository's
(reconciliation **#10**), so this part's pull requests are the numbers the runs name, and **P2** is
the part that replaces the reader — behind the same input to the join, as D193 asks.

---

### P2 — Every repository: the project-wide read (D193)

**Scope.** The reader that turns one repository's view into the project's (Q12 A, Q3 A). It names
the project's repositories, mints a **read-only installation token per repository** — a new named
permission set `coordinatorTokenPermissions`, `{contents: read, pull_requests: read, issues:
read, metadata: read}` and **no `write` anywhere**, built beside `buildTokenPermissions` in
`internal/gitprov/github/apptoken.go` and minted through `MintInstallationToken` — and **caches
it per repository** for the token's life, because the same token call posts one repository at a
time. A Bitbucket project is **refused with a line naming the reason** (Q12 A), in the way
`fugaro validate` refuses things. The reader is behind one interface, so **D193 changes its
implementation and not its shape**.

**Files.** `internal/gitprov/github/apptoken.go` (`coordinatorTokenPermissions` and its exported
accessor, beside `BuildTokenPermissions`); `internal/gitprov/providers/providers.go` (nothing new
expected); `ee/coordinator/` (the reader, the token cache, the refusal);
`ee/coordinator/cli/coordinator.go` (the project-wide view).

**Red step.** Four tests: the set a coordinator mints contains no value the word `write` — plant
`contents: write` and the test must fail (this is Q3's hard rule made mechanical, and finding #14
is why); a Bitbucket project is refused, and the failure names the reason rather than the
repository; two repositories under one App mint two tokens; and a second read of the same
repository reuses the cached token rather than minting again.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./internal/gitprov/...
./ee/coordinator/... ./internal/cli/ -run 'TestCoordinator|TestAppToken'`; and the golden
the permission set is asserted against, read rather than accepted.

**Depends on.** **D193.** P1 is not required for the code, but the view it widens is P1's.

**Reviewer tier.** **SECURITY-SENSITIVE.** **Yes — this is the read-only rule of Q3(A) written as
code, and it decides what one stolen token reaches.** Independent review required, and the pull
request body must state: the permission set and that it holds no `write`; which function mints it;
whether the token is per repository or project-wide, and D193's answer; the cache's key and
lifetime; and what a coordinator whose token leaked can read.

**Commit.** `coordinator: the project-wide read, and the read-only GitHub credential`

**Status: built.** On the long-lived `coordinator` branch, in this plan's own commit message. The
permission set and its exported accessor (`internal/gitprov/github/apptoken.go`), the project-wide
reader with its per-repository token cache and its Bitbucket refusal (`ee/coordinator/`), the
opener and the mint that install the read-only token source on it, and the command's `--all`
(`ee/coordinator/cli/coordinator.go`) are in the tree, with the permission set held to the mint's request
body by a golden and the read itself held to the token it was minted — the fixture's own
`Authorization` header is the read-only one; the plan's status line above moved with them. The
reader reads a repository's pull requests a few at a time, the width P1's review asked for and the
reader it replaced carried. One thing in the tree is not in this part's own Files line, and it is
recorded here rather than left to be found: `internal/gitprov/providers/providers.go` gains one
exported name, `AppKey`, because the mint and the opener must not disagree about where the App's key
is (`EnvGitHubAppKey`, else `EnvGitHubAppKeyFile`); what `FromEnv` did with the environment moved
into an unexported `envVars`, unchanged. The one thing this part does NOT do
is stated where a reader meets it rather than here: D193's answer is **one read-only installation
token per repository**, so there is no single project-wide credential to steal, and whether an
installation token may omit `repositories` at all is **U3**, still an owner check.

**The follow-up of 2026-10-25 narrowed this part's credential and gave it an end, and the two
are recorded here because the Scope line above says what was planned rather than what is.** The
set is `{pull_requests: read, metadata: read}`, and those are the two reads the project-wide
reader makes: every pull request is read one at a time through `gitprov.Provider.PullRequest`,
and `metadata: read` is what GitHub requires of any request. `contents` and `issues` were asked
for and never used — this reader opens no file and reads no issue, and a permission nobody uses
is reach a stolen token would have for nothing; P5's aggregation job re-reads through this same
reader, so it asks for no more. The token also has an end: the command revokes every token it
minted when it is done (`DELETE /installation/token`,
`internal/gitprov/github/apptoken.go`'s `RevokeInstallationToken`), in the order it minted them,
best-effort and silent — a revoke that failed changes no exit code and prints nothing, because
a credential that outlives the read it was minted for is the one thing this part's posture is
against. P5's sweep takes back the tokens of its tick the same way. With
`FUGARO_CREDENTIALS_SERVICE_URL` set, the service holds the App's key and this process holds
none, so `coordinator status` refuses: exit 1, before it reads the App ID or the key, because the
service mints a run's token and this read is not a run.

---

### P3 — The state: the nodes, the writer and the read grant (D194, D195)

**Scope.** The Realtime Database half of Q5(A), Q15(A) and Q6(A). A new top-level `coordinator/`
subtree (D195's recommendation): project-wide facts under `coordinator/project/`, per-repository
facts under `coordinator/repos/<slug>/`, every document keyed by a **deterministic ID derived
from the thing it describes** and written with `internal/rtdb/client.go`'s `GetETag` +
`PutIfMatch` — the house idiom of finding #16/#17, and **no new lock**. The writer is the
aggregation job's own service account holding `roles/firebasedatabase.admin` beside the history
account's (D194's recommendation): `deploy/terraform/gcp/modules/firebase/iam.tf`'s
`google_project_iam_member.history_database` is the exact shape, including the comment that says
IAM cannot enforce what its code does not write, and **that comment's own claim — that only the
history account holds such a role — must be amended in this part, because it stops being true
here.** The rules change is a **read** grant only, for the people the CLI and the phone already
authenticate as (Q15 A, D112's shape), added to `internal/budget/rules/rules.json.tmpl` and the
macros in `internal/budget/rules/rules.go`, with `internal/projectpolicy/version.go`'s
`RulesVersion` bumped and `internal/budget/rules/testdata/rules.golden.json` regenerated.

**Files.** `ee/coordinator/` (the records and the CAS writes over `internal/rtdb`);
`deploy/terraform/gcp/modules/firebase/iam.tf` (the writer grant and the amended comment);
`internal/budget/rules/rules.go`, `internal/budget/rules/rules.json.tmpl` and the golden;
`internal/projectpolicy/version.go` (`RulesVersion`); the golden plans under
`deploy/terraform/gcp/` and the `internal/infra/tf/` testdata they are read from.

**Red step.** Four tests: a signed-in reader on the people list **may** read `coordinator/**`;
the same reader **may not** write it (mutate the grant to `write` and the second test must fail);
a **run's** token may not read it (finding #25's rule is that no run identity reaches a node it
has no business in); and a write carrying a stale ETag is refused and the document does not move
— the mutation-proving test for Q6(A)'s CAS.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./ee/coordinator/...
./internal/rtdb/... ./internal/budget/rules/... ./internal/cli/ -run 'TestCoordinator|TestRules'`;
the rules goldens **regenerated and read diff by diff**; `terraform fmt -check -recursive
deploy/terraform/` and `terraform validate`, with the golden plans regenerated and read.

**Depends on.** **D194** (the writer identity) and **D195** (the nodes). P1 is not required, but
nothing is written here that P1's view does not define.

**Reviewer tier.** **SECURITY-SENSITIVE.** **Yes — this part changes the database's security
rules and adds a second project-wide write identity.** Independent review required, and the pull
request body must state: the read grant's extent and that it is a read; what the writer account
can reach (it can write the whole database, `config/` included, and IAM cannot stop it); the
generation bump and what a client with an older marker sees; and the amended claim in `deploy/terraform/gcp/modules/firebase/iam.tf`.

**Commit.** `coordinator: the project record, its writer identity and the read grant`

**Status: built.** Lands on the long-lived `coordinator` branch when its pull request merges, in
this plan's own commit message, and the plan's status line above moves with it. The subtree, the
deterministic IDs, the ETag CAS, the read grant and the writer's grant are in the tree. Three
things are worth a reader's attention here rather than in the diff. The first is that the writer
account is **named, not created, and not yet granted**: the installation root creates it in
**P5**, as it creates the history account's, and IAM refuses a grant to a member that does not
exist, so the Firebase root's `coordinator_account` defaults to empty and the grant
(`google_project_iam_member.coordinator_database`) has a count of 0 until **P5** sets it — which P5
now does only for an installation whose owner opts in (`fugaro update --firebase-project <id>
--coordinator`, P5's **For operators**), so the default golden plans still hold no such row, the
opt-in goldens (`installation-coordinator`, `firebase-coordinator`) hold it, and the module's own
`terraform test` runs cover it on and off. The
second is that the writer bypasses the rules, so `ee/coordinator`'s `Store` refuses every
path outside `coordinator/` itself: that check, not IAM and not the rules, is the fence (D194).
The third is the rules generation: `RulesVersion` moves from 3 to 4, and once an installation has
deployed generation 4, an older Fugaro's `fugaro update --firebase-project` is refused rather than
deploying its older document over it.

**For operators.** When this part ships, the release notes' `### For operators` section carries
one step, per installation: run `fugaro update --firebase-project` from the new CLI to deploy the
generation-4 rules (until then `fugaro doctor` reports `policy-rules-skew` and a signed-in reader
is refused every read of `coordinator/`); runs, the project policy and the mobile app are
unaffected either way. Nothing is needed per repository.

---

### P4 — The report path: what every orchestrator declares

**Scope.** The writer every orchestrator reports through, and **the coordinator's half of the
central orchestrator plan's P9** — the half that plan recorded as blocked on this document
existing, until **2026-10-21**, when its own "Depends on" line was amended to say this document
exists and its P4/P5 no longer block that half (§1 item 1). **`fugaro coordinator report`** writes one intent record under
`coordinator/repos/<slug>/intents/<id>`: the services and files the caller is about to touch, the
branch, the target release, who declares it and when. It is the spec's §6 *"intent signals …
sent before code exists"* with the CLI as the transport, and it takes **nothing back** — the
command prints what it wrote and issues no advice. The record's ID is **derived from the caller
and the declaration**, so the same report twice writes one document (Q6 A's idempotency, finding
#17's house idiom). The local run path calls the same writer from `internal/cli/run.go`'s
`launchRun`, beside `takeClaim`, so a launch declares itself without a second command; and
`report_intent` (P8) is **this same writer behind the front door**, so the record's shape is
defined once, here.

**Files.** `ee/coordinator/` (the intent record, its deterministic ID, its validation);
`ee/coordinator/cli/coordinator.go` and one line of `internal/cli/root.go`; `internal/cli/run.go`'s
`launchRun`; fixtures.

**Red step.** Three tests: a report from a caller the project cannot name is refused, and the
refusal names what is missing; the same declaration from the same caller twice writes **one**
document (mutate the ID to include the wall clock and the test must fail — that is the mutation
that makes this an idempotency test rather than a round-trip); and the command's output carries no
advice and reads no state back, asserted against the writer's own dependency list.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./ee/coordinator/...
./internal/cli/ -run 'TestCoordinator|TestReport'`.

**Depends on.** P3 (the nodes and the writer), **D195**.

**Reviewer tier.** **Careful.** **Security-sensitive.** **No** — it writes and reads nothing back,
its text is written by whoever can already run `fugaro run` (finding #11's trust level), and it
adds no network surface.

**Commit.** `coordinator: the intent record, and the report path every orchestrator uses`

**Status: built.** Lands on the long-lived `coordinator` branch when its pull request merges, in
this plan's own commit message, and the plan's status line above moves with it. The record, its
deterministic ID (`IntentID`, over `DocumentID`'s length-prefixed parts), its two refusals and the
writer are in `ee/coordinator/intent.go`; the node is
`ee/coordinator/state.go`'s `PathIntents` and `PathIntent`; `fugaro coordinator report` is
the transport; and `launchRun` calls the same writer beside `takeClaim`, so a launch declares
itself without a second command. What a launch can
declare before any code exists is the caller the task names and the branch the run is about to
work on — the files and the services are what the run's own record holds once it has worked, and
the join is P1's. The declaration is **advisory in the launch as it is everywhere**: a project
with no database is skipped, a task that names no requester is not declared (the report path's own
refusal is right when a person asks and pointless when nobody did), a failure is a warning on
stderr and never a failed launch, and the whole thing is bounded at 15 seconds so a database that
does not answer cannot eat the time the launch needs. The declaration also **follows the launch's
own database switch**: `budget.mode: off`, a project with no `budget.rtdb_url`, and
`--no-budget-check` all declare nothing, because the coordinator's state lives in the database the
budget uses and a launch that was told not to use it may not be the one call that reaches it —
beyond the policy read a launch always makes. Nothing is lost by it: the declaration is advisory,
and the view P1 renders reads the runs bucket, where the launch's own record lands either way.

**What this part does not do, and the one tension it leaves.** The record is written through the
**caller's own credentials**, on the project's `budget.rtdb_url` database, because there is no
other credential in the tree: P2's reader is read-only and P3's writer account does not exist until
P5 creates it. But the rules P3 deployed grant `/coordinator` a **read** and `.write: false` to
everybody (Q15 A), so what admits a report is the **database role its caller holds** —
`roles/firebasedatabase.admin`, which `terraform.budget_admins` have — and nothing else. So
`fugaro coordinator report` works for a person who is already a budget admin of the installation,
and refuses, saying exactly that, for a launcher who is not: the local orchestrators Q15(C) is
about report through the front door, `report_intent` (**P8**), which writes as the coordinator
rather than as the person. That is the gap Q15(A) leaves between "the CLI is the transport" and
"no client writes the subtree", and it is recorded here rather than papered over: this part is
built as the P4 section specifies, and **a later part that wants the non-admin path builds it in
P8**, not by widening the rules. Nothing in this part mints a credential, changes the rules,
deploys anything or grants anybody: the grant and the rules deployment stay owner-run.

**One deviation from the section above, and one proposal this part does not take.** The **Files**
line names "one line of `internal/cli/root.go`"; the subcommand is registered beside the others in
`newCoordinatorCmd` (`ee/coordinator/cli/coordinator.go`), which is where every coordinator subcommand
is added, and the root command's own file is untouched. And the record carries **no plan task
id**: `docs/console-dependency-graph/reconciliation.md` **GD5** proposes one recorded in the
launch record a run already writes, and its §4 item (f) says recording it in this record is
*"proposed here and not made"*, so the record is the
shape this plan gives it — the caller, the clock, the branch, the target release, the files and
the services — and a later part that wants a plan task id adds a field and adds it to the ID.

---

### P5 — The aggregation job and the drift reconciliation (Q7 A)

**Scope.** The engine of the deterministic half: a scheduled Cloud Run **job** in the installation
root, on the pattern `deploy/terraform/gcp/modules/installation/history.tf` already runs — its
`google_cloud_scheduler_job "history_sweep"` fires on a tick as an HTTP POST to the Cloud Run
Admin API's `jobs:run`. The job re-reads GitHub through P2's reader, recomputes P1's view, writes
it into P3's nodes, and records what it could not reconcile rather than half a view. **No new
resource type** — `google_cloud_run_v2_job` and `google_cloud_run_v2_job_iam_member` are already
on `internal/infra/tf/cover.go`'s `allowedTypes` — no new credential beyond P2's reader and P3's
writer, and no reachable surface. This is the cheapest half of the spec (finding #18) and it is
what makes P4's records readable rather than merely writable.

**Files.** New Terraform in `deploy/terraform/gcp/modules/installation/` beside the history job and
its scheduler entry, with the golden plans; a new entry point under `cmd/fugaro` and its package
(the sweep); `ee/coordinator/` (the sweep's own logic: the recompute, the stale marker, the
write set); `internal/cli/` for the manual invocation the owner's live check runs.

**Red step.** Three tests: a second sweep over unchanged GitHub writes the same documents and
moves no generation (mutate the write to stamp a fresh timestamp and the test must fail); a GitHub
read that fails leaves the previous view in place and writes a `stale` marker rather than a
partial view; and the plan's generated diff carries no credential material in the job's
environment.

**Verification.** `terraform fmt -check -recursive deploy/terraform/`; `terraform validate`; the
golden plans regenerated and read; `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test
./ee/coordinator/... ./internal/cli/ -run 'TestCoordinator|TestSweep'`.

**Depends on.** P2 (the reader), P3 (the writer and the nodes), P4 (the record it reconciles),
**D195**.

**Reviewer tier.** **Careful.** **Security-sensitive.** **Yes, in one respect** — the job runs as
the database-wide writer account, so the review must confirm what it may write (**only
`coordinator/**` in its code, which IAM cannot enforce**), that its GitHub credential is P2's
read-only set, and that its schedule and timeout carry no unbounded retry.

**Commit.** `coordinator: the aggregation job and the drift reconciliation`

**Status: built.** Lands on the long-lived `coordinator` branch when its pull request merges, in
this plan's own commit message, and the plan's status line above moves with it. The sweep is
`ee/coordinator/sweep.go` (the recompute, the `stale` marker, the write set, and the
pruning P4's review asked for) and `ee/coordinator/view.go`; `fugaro coordinator sweep` is
the manual invocation the owner's live check runs, in `ee/coordinator/cli/coordinator.go`; the job, its
account, the runs-bucket reader, the App-key grant and the schedule are
`deploy/terraform/gcp/modules/installation/coordinator.tf`, wired through the root's variables and
outputs and the `Coordinator` spec in `internal/infra/firebase.go`, with the golden plans
regenerated. The job's environment is the whole of its configuration
(`FUGARO_PROJECT`, `FUGARO_FIREBASE_PROJECT`, `FUGARO_RTDB_URL`, `FUGARO_RUNS_BUCKET`,
`FUGARO_COORDINATOR_REPOS`, `FUGARO_GITHUB_APP_ID`, and the App key as a `secret_key_ref`, so no
plan, no `terraform show` and no job describe carries the credential), it runs as
`fugaro-coordinator` with `max_retries = 0` and a 600s timeout under an 8-minute in-code bound, and
its schedule is `*/15 * * * *` with `retry_count = 0` — a failed sweep logs its failure and the next
tick is the retry.

**The fixes the independent security review asked for, and what they bound.** The job, its
account and every grant are **off by default**: `infra.Installation` plans them only when the owner
has opted in (`InstallOptions.Coordinator`), and the Firebase root is handed no account otherwise,
so the plans of every existing installation are unchanged (the default goldens hold no
coordinator resource; `installation-coordinator` and `firebase-coordinator` are the opt-in's). A
tick is **bounded**: it reads the runs of the last 30 days (`coordinator.RunsWindow`), at most 200
pull requests from the host across every repository (`DefaultReadBudget`; a merged pull request is
carried from the last view and never read again), and it starts with the repository a cursor in
`coordinator/project/sweep_cursor` names, so a project whose work is more than one tick can read
is read in turns; on the 8-minute deadline the repository being read is marked stale under a
30-second grace of its own and the next tick starts after it. **One pull request** the host
refuses is recorded in the view on its own (`unread`, with the last state a sweep read, if any)
and the others are read, because the numbers a view reads are written by the runs themselves;
the entry counts the consecutive ticks the host refused it (`failures`), and after three
(`MaxReadFailures`) it is asked for once a day (`retry_after`, `ReadRetryAfter`) rather than on
every tick, so a number no pull request has stops spending the repository's share of the budget.
The view holds at most 500 records and 500 collisions (`MaxViewRecords`, `MaxViewCollisions`) and
says how many it left out (`omitted`, `omitted_collisions`): the finished records whose newest run
is oldest go first, never one in flight while a finished one is kept. The intent records are
pruned by age (30 days) as well as by count, a record dated more than five minutes ahead of the
sweep's clock is ordered as the oldest, and one record — or a stale marker, or a view — that does
not decode is stepped over and counted (`undecodable`) instead of stopping the tick.

**Two deviations from the section above, and the follow-up it carries.** The **Files** line names
"a new entry point under `cmd/fugaro` and its package"; `cmd/fugaro` is only `main` in this tree,
so the sweep lives in `ee/coordinator` with its command in `internal/cli`, beside every
other coordinator subcommand. And the job's image is `HistoryImage` — the history job's own
`fugaro-base/history:latest` — rather than a new one, because the sweep is the same binary the CLI
is and a second image would be a second thing to build, pin and publish for no difference in what
runs. On the review P4's pull request left for this part: the sweep **prunes** the
oldest intent records of a repository, keeping the newest 200 (D197's bound, `DefaultIntents`), each
removal through the store's compare-and-set on the record's own ETag so a record that changed
between the read and the removal is left alone; and the report path **refuses a declaration larger
than a record** (`ErrIntentTooLarge`: 512 characters in one name, 100 names in one list, 8192
characters of text in all), because the caller writes all of it and the ID that makes a report
idempotent would not collide with anything.

**What this part does not do.** Nothing here is a live action, and nothing is on by default: the
job, the account and the schedule are declared in Terraform and applied only by the owner's own
`fugaro update --firebase-project <id> --coordinator`, and the account's
`roles/firebasedatabase.admin` on the Firebase project is granted by the Firebase root in that same
run — the grant P3 gated off, turned on by this part only where the owner opted in and the account
it names therefore exists. What
the account may write is still not IAM's to say (D194): the fence is `ee/coordinator`'s own
subtree check and the deployed rules' floor, which is the one respect the section above calls
security-sensitive.

#### For operators

The coordinator's aggregation job is **OFF by default**: an installation that has not opted in
has no coordinator account, no grant, no job and no schedule, and `fugaro update` plans none of
them. The owner opts in once, at a real terminal (no flag covers it, and a coding agent's
session is refused), per installation:

```sh
fugaro update --firebase-project <firebase-project-id> --coordinator
```

That run creates, in the installation's GCP project:

- the service account `fugaro-coordinator`;
- its grants: `roles/firebasedatabase.admin` on the Firebase project (project-wide, because IAM
  cannot scope it to `coordinator/`; the job's own code does), `roles/storage.objectViewer` on the
  runs bucket, and `roles/secretmanager.secretAccessor` on the GitHub App key's one secret;
- the Cloud Run job `fugarocoord`, which runs `fugaro coordinator sweep` as that account with the
  App key mounted from Secret Manager, `max_retries = 0` and a 600s timeout;
- the Scheduler job `fugaro-coordinator-sweep`, every 15 minutes, with `retry_count = 0`, and the
  scheduler account's `fugaroJobRunner` on that one job.

The job, its key grant and its schedule wait until the history image exists and the local config
records a GitHub App ID (`fugaro update --repo <owner/name> --github-app-id <id>`); until then the
run creates the account and its two reads, warns, and the next `fugaro update --firebase-project`
deploys the rest. The opt-in is recorded as `terraform.coordinator: true` in the local config once
the apply has created the account, and a new machine's config takes it from the installation's
outputs, so later runs keep it. There is no opt-out command in this release: removing that key
makes the next plan list the coordinator's resources as deletions, which `fugaro update` refuses
without `--allow-delete`, and the job itself carries deletion protection.

---

### P6 — The console's project view (Q8 A)

**Scope.** The console gains the project view as **read** operations over the CLI sidecar it
already spawns, exactly as every other screen works (Q8 A, finding #27): `CONSOLE_OPERATIONS` and
`ALLOWED_OPERATION_FLAGS` in `console/src/main/ipc.ts`, `SPAWN_COMMANDS` in
`console/src/main/spawn.ts`, `EXPOSED_OPERATIONS` in `console/src/preload/index.ts`, and a nav
entry in `console/src/renderer/entry.ts`. **No new credential, no new network surface, and the
CSP is unchanged**: `console/src/main/protocol.ts`'s `connect-src 'none'` stays exactly as it is,
because the renderer reaches data only through IPC. The view is the one P1/P2 compute, so this
part adds a window onto it and computes nothing.

**Files.** `console/src/main/ipc.ts`, `console/src/main/spawn.ts`, `console/src/preload/index.ts`,
`console/src/renderer/entry.ts`, `console/src/source/cli.ts` if a source method is needed, and the
console's own tests including `console/test/unit/window.test.ts`.

**Red step.** Three mutations, each of which must fail a test: widen the CSP in a scratch tree and
`console/test/unit/window.test.ts` must fail; add an operation with a flag outside `ALLOWED_OPERATION_FLAGS` and the
IPC test must fail; render one field through `innerHTML` instead of `element.textContent` and the
renderer test must fail — that is finding #28's rule, and Q8(A) is what puts a pull request's text
on this screen.

**Verification.** `npm test` and `npm run typecheck` in `console/` (whose `console/test/unit/window.test.ts` must
still see `connect-src 'none'`); `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .` for the CLI half.

**Depends on.** P1 and P2 (the commands) and P3 (the state). If the console is asked for the view
before P3 lands, the console half is written against P1's local view and the read grant follows.

**Reviewer tier.** Standard. **Security-sensitive.** **No** — it adds an IPC read beside an
existing allow-list and changes no CSP, and the review's job is to confirm exactly that.

**Commit.** `console: the project view, read through the CLI sidecar`

**Re-planned 2026-11-07 (D435; T20).** The commands this window reads are the moved ones —
`ee/coordinator/cli/coordinator.go`, registered by `ee/register` — so **the sidecar the console
spawns has to be the tagged binary**: an untagged `fugaro coordinator` is the core's stub and
refuses, which is the reading a console that spawns whatever `fugaro` it finds has to make
explicitly (E1.1, D522). Nothing else about the part changes, and the move costs it nothing: it adds
an IPC read beside an existing allow-list, computes nothing, holds no credential and leaves
`console/src/main/protocol.ts`'s `connect-src 'none'` alone — and `console/` is itself
business-licensed, so this half was already on the paid side of the line.

---

### P7 — The coordinator's own service: the type, the module, and the front door's credential (D196)

**Scope.** Three halves, and after **D196's answer of 2026-10-21** all three are this work's.

- **The type.** `google_cloud_run_v2_service` and its `*_iam_member` enter
  `internal/infra/tf/cover.go`'s `allowedTypes` and `internal/infra/tf/managed_cover.go`'s
  `managedTypes`, which hold `google_cloud_run_v2_job` and its own `*_iam_member` today and, when
  this part was written, **did not hold the service** — that absence is finding #2, and admitting a
  type is exactly the act its allow-list exists to make deliberate, so
  `internal/cli/init_review.go`'s `notCovered` must still fire the typed confirmation the owner
  designed. **The admission has since been made, by another root**: the credentials service's own
  root (D146) put `google_cloud_run_v2_service` and its own `*_iam_member` into `allowedTypes`, and
  `internal/infra/tf/managed_cover.go`'s `managedDropped` keeps both out of a managed plan with a
  reason of its own — so this half is a **citation** now rather than an addition, which is what the
  shared-admission sentence further down already rules. **The cost table already knows the type**:
  `internal/infra/tf/managed_cost.go` carries the service's rows, and its own comment says they
  *"become load-bearing the moment the allowlist admits a service"* — so this half adds the type
  to the two refusal lists and changes no cost row. **This admission is shared with the central
  orchestrator plan's P1, which admits the same type**: **whichever part lands first admits it and
  the other cites it** rather than re-adding it, and the part that lands second says in its own
  commit that the admission is already there.
- **The module, and the service.** D196's answer is **option (B)**: the coordinator is **not** the
  central orchestrator's service, because *"there can be multiple central orchestrators in the
  near future"* — **the owner's words of 2026-10-21** (reconciliation §6), recorded there and
  nowhere else. So this part writes the Terraform the earlier draft of it deliberately did not: a
  new `deploy/terraform/gcp/modules/coordinator/` holding the service, **its own service account**,
  its IAM, and its `min_instance_count = 0` configuration — the shape
  `deploy/terraform/gcp/modules/installation/history.tf` gives the history job and
  `deploy/terraform/gcp/modules/project/job.tf` gives a project job, wired into
  `deploy/terraform/gcp/modules/installation/` beside them. The service is the tenant of P8's
  handler and it holds **no model credential** (Q4 A). Two services per installation is now the
  design, not an accident: this one and the central orchestrator's, with separate identities,
  separate scale-to-zero configurations and separate grants.
- **The credential.** Q2(A)'s plumbing: a `fugaro` command that mints the Google ID token for the
  coordinator's URL from the CLI's own user ADC (`internal/backend/gcp/gcp.go`), so nobody types
  `gcloud auth print-identity-token`; and the per-person Cloud Run IAM invoker member, in the
  shape `deploy/terraform/gcp/modules/project/iam.tf`'s `google_cloud_run_v2_job_iam_member
  "launcher"` uses — **one person per member, no `allUsers`, no `allAuthenticatedUsers`, no
  `domain:`**, which is the rule `internal/cli/init_review.go`'s `reviewScreen` already refuses in
  a plan and `internal/cli/doctor_launcher.go` reports as a problem when it finds one.

**Where the service is built is D196, and it is answered.** The plan was written to D196's
recommendation — that a sibling plan's resident service would serve as the front door, with this
work admitting the type and minting the token only — and the owner **ruled the other way**.
This part is therefore the rewritten one, and it is the reason the plan's own status moved from
"approval gate open" to "approved to build": **the front door has no other author**.

**Files.** `internal/infra/tf/cover.go` (`allowedTypes`), `internal/infra/tf/managed_cover.go`
(`managedTypes`), `internal/infra/tf/managed_cost.go`, `internal/cli/init_review.go`
(`notCovered`), new `deploy/terraform/gcp/modules/coordinator/` (the service, its account, its
IAM, its scale-to-zero settings) wired into `deploy/terraform/gcp/modules/installation/`,
`ee/coordinator/cli/coordinator.go` (the ID token), `internal/backend/gcp/gcp.go`,
`internal/initflow/stage.go` (the stage that carries the grant), and the goldens under
`internal/infra/tf/`, `internal/cli/` and `deploy/terraform/gcp/`.

**Red step.** Four tests: a plan holding a `google_cloud_run_v2_service` is **admitted** (it fails
today, because the type is refused) and a plan holding an unrelated unknown type is **still
refused** (it passes today and must keep passing); the generated plan carries **the coordinator's
own service and its own service account**, and the account is not the central orchestrator's
(mutate the module to reuse that account and the test must fail); a minted ID token carries the
coordinator's audience, and a token minted for a second audience is **refused** by the client that
checks it (mutate the audience and the test must fail); and the generated plan's invoker members
are one person each, asserted from the plan rather than from the code.

**Verification.** `terraform fmt -check -recursive deploy/terraform/`; `terraform validate` in
every module that has a golden; the golden plans **regenerated and read diff by diff**;
`GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./internal/infra/tf/... ./internal/cli/
-run 'TestCoordinator|TestAllowedTypes|TestInitReview'`.

**Depends on.** **D196** (answered), and on **P3** for the state the service reads. **It no longer
depends on the central orchestrator plan** — that dependency is what D196 removed. The type
admission is reviewable on its own, and it is the one half another plan may land first.

**Reviewer tier.** **SECURITY-SENSITIVE.** **Yes — this admits the first request-serving resource
type into an allow-list that exists to make such admissions deliberate, and it builds the tenant
of the coordinator's front door.** Independent review required, and the pull request body must
state: which types were added and which stayed refused; that the typed confirmation still fires;
that the service has its own identity and is not the central orchestrator's; the audience the ID
token is minted for and how the client checks it; and that every invoker member is a person.

**Commit.** `infra: the coordinator's own service, and its ID token`

**Re-planned 2026-11-07 (D435; T20), and the three things that moved under it.** The part's Go half
is `ee/`, reached through `ee/register` like every other part after P5: the minting and the
per-person checks land in `ee/coordinator/cli/coordinator.go`, and an untagged build's `coordinator`
is the core's stub that says why (E1.1, D522). Two questions the section above answers are recorded
here as **open**, because the work re-plans a part and does not re-decide a front door:

- **The service has been built, and not in a module of its own.** The coordinator's
  `google_cloud_run_v2_service`, its own account and its public invoker binding are in
  `deploy/terraform/gcp/modules/installation/coordinator.tf`, beside the aggregation job P5 added —
  landed with the front-door work (`docs/mcp-server/`, its M4, #1716). The module the Files line
  below names, `deploy/terraform/gcp/modules/coordinator/`, is **not in the tree**, and this re-plan
  does not put it there: **which of the two layouts the remaining Terraform follows is a decision to
  take with the owner**, and taking it changes the Files line, the module's goldens and
  `coordinatorPlanNewDirs` in one commit.
- **The front door's authorization is no longer settled by this part's reading.** The owner answered
  **D920** on 2026-11-04 — a **public** endpoint whose gate is the bearer token, recorded in
  `docs/mcp-server/reconciliation.md`, where that folder's finding F23 reads the Cloud Run IAM
  invoker this part and P8 choose as unable to coexist with the owner's spec's OAuth flow on one
  endpoint. Against that, `docs/mcp-server/implementation-plan.md` §7 leaves **P7's ID-token
  minting, its per-person invoker grants and its IAM checks to this work** and forbids its own M4
  from taking them over. The two cannot both be read as the plan of record, so **it is recorded here
  rather than picked**: which one survives the front door's build is the front-door work's to settle
  with the owner, and the sections above are kept as the reading they were written to.

---

### P8 — The MCP front door, its tools and the override log (D197)

**Scope.** The spec's §7 interface, over the streamable HTTP transport, with Cloud Run IAM invoker
as the authorization — recorded in the design as a **deliberate departure** from the MCP
specification's OAuth 2.1-based model (Q16 A, U1). The tools: **`report_intent`** (P4's writer),
**`check_conflicts`** and **`get_project_state`** / **`get_release_status`** (P1's and P2's view,
read), and **`record_override`** — the override log, which **writes and is shown** and is **never
fed back into a prompt in v1** (Q14 A). The injection rules are code here, not prose: GitHub text
and intent signals are read as **data**, an instruction found in them is never acted on, the
coordinator **never writes to GitHub**, and every answer is **plain text, never markup and never a
link the user did not ask for** (finding #28). The override log's retention is **D197**. The
service this runs in is **this work's own**, from P7 — no other document defines its protocol, so
the protocol and the tools are specified and built together here.

**Files.** `ee/coordinator/` (the handler, the route, the tools, the data-boundary
convention the prompt-free v1 still needs for its text, the override record and its cap); the
service's entry point under `cmd/fugaro` and its package, beside P7's module; the `record_override`
reader in `ee/coordinator/cli/coordinator.go`; and the design text that records the departure.

**Red step.** Four tests: a tool call whose input carries an instruction-shaped string behaves
**identically** to one without it (the mutation-proof for the data boundary — make the handler
echo an instruction into a decision and the test must fail); a request with no IAM invoker grant
is refused by the platform and the service never sees it (asserted, not assumed); the override
log keeps the last 200 and drops the 201st, with D197's number in one place (mutate the cap and
the test must fail); and an answer containing a markdown link is rendered as text and the link is
not followed — the renderer's rule, asserted here because Q8(A) is what puts this text on a
screen.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./ee/coordinator/...
./internal/cli/ -run 'TestCoordinator|TestTools'`; the console's suite if any of the text reaches
it; and the Terraform checks if the route changes the service's plan.

**Depends on.** P7 (the service, the type and the credential) and **D197**. **It no longer depends
on the central orchestrator plan's P4 and P5** — D196's answer is what removed that, and it is the
whole reason this part can be built in this work.

**Reviewer tier.** **SECURITY-SENSITIVE.** **Yes — this is the only place in this work that
accepts a request from another process, and everything it reads is text somebody else wrote.**
Independent review required, and the pull request body must state: what authenticates a caller and
where; which tools write and which read; the data-boundary convention and the test that proves it;
the override cap; and that no answer is markup.

**Commit.** `coordinator: the MCP front door, its tools and the override log`

**Re-planned 2026-11-07 (D435; T20), and half of this part is now another folder's.** The land is
`ee/coordinator/` — the Files line above already reads that way, and the re-plan is what says out
loud that the handler, the tools and the override log are reached only through `ee/register`, so an
untagged build has none of them (E1.1, D522). Two things have moved since the section was written:

- **The front door itself — its transport, its authorization server and its tool registry — is
  planned in `docs/mcp-server/implementation-plan.md`**, whose tasks land under
  `ee/coordinator/mcpauth/`, and whose §7 builds **no second surface** for the tools this part
  defines: the coordinator's own tools are served by that front door, under the names written here.
  The one thing of this part's that did not survive is its authorization: an IAM-invoked endpoint,
  chosen here as a deliberate departure from the MCP specification (Q16 A, U1), is what **D920's**
  answer of 2026-11-04 replaced with a public endpoint whose gate is the bearer token
  (`docs/mcp-server/reconciliation.md`, finding F23).
- **What stays this part's is what the front door calls**: the tools, the override log with its cap,
  and the data-boundary rule they are built around — an instruction found in GitHub text or in an
  intent signal is read as data, and every answer is text rather than markup (finding #28). And the
  part is still the only place in this work that accepts a request from another process, so its
  reviewer tier and the independent review §7 asks for are unchanged by any of this.

---

### The roadmap record: P9a–P9g (added 2026-11-11, at the owner's request; revised the same day on the owner's answers)

**Why these parts exist.** The console dependency graph's **GV3.1** (the roadmap write), **GV3.2**
(drag, preview, confirm, undo) and **GV4.1** (cross-plan and cross-stream edges) wait on *"the
coordinator's record carrying tasks, releases and cross-plan edges"* — that plan's rulings (a) and
(d) of 2026-10-22, and its **GD1**, which chose this record over a roadmap file
(`docs/console-dependency-graph/reconciliation.md` §4). Nothing on `main` carries them:
`ee/coordinator/state.go` holds one repository's view and its intent records (`RepoView`,
`StoredProjectView`, `StoredIntent`, `RepoIntents`, `StoredProjectIntents`) and nothing else; no part
P0–P8 adds them; and P7 and P8 were re-planned under `docs/mcp-server/implementation-plan.md`. The
owner asked on 2026-11-11 for the task that adds them. These parts are it. **One consequence to
read before approving them**: the record holds the plans somebody publishes into it (D2111), and a
plan is published from its graph artifact; on 2026-11-11 `main` carries exactly one —
`docs/console-dependency-graph/implementation-plan.graph.json` — because the other plans are
Markdown and no scraper reads them (the console plan's F3). A whole-project graph needs the
planning skill to write an artifact beside each plan, and somebody holding `roadmap:write` to
publish it.

**Why here and not in another plan.** The record is this work's: D195's subtree, P3's `Store` and its
compare-and-set and P4's deterministic-ID idiom are what it extends, and the console's
plan names "the coordinator's record" as the thing it waits for. The front-door half (P9d, P9e) adds
a scope and tools to the MCP plan's registry; it is written here because the tools are this record's
writers, the way P8 defines the coordinator's own tools that the MCP front door serves (that plan's
§7), and the MCP plan's §7 said the console's work "comes back to ask" for the scope. The parts are
written in this plan's own part shape (Scope, Files, Red step, Verification, Depends on, Reviewer
tier, Security-sensitive, Commit), with **Interfaces** and **Tests** lines added because the console's
tasks consume them by name. Like P6–P8 since the re-plan of 2026-11-07, they land under `ee/` on
`main`, reached only through `ee/register` (D435). **None of them is approved**: §11's gate passed
for P0–P6, and each of these needs the owner's explicit go, by name.

**The decisions: D2110–D2127 answered by the owner on 2026-11-11.** They are taken
from the block **D2110–D2129**, reserved by this work above **D2109**, the highest number written
or reserved on `main` and in any open pull request on 2026-11-11 (`docs/account-aliases/README.md`
reserves D2090–D2109); **D2128–D2129** stay reserved and unused. Each was written recommendation
first, and the column "Answer" says whether the owner took it.

| Decision | The question | Recommendation as written | Answer (owner, 2026-11-11) | Blocks |
| --- | --- | --- | --- | --- |
| **D2110** | Who may write the roadmap record, and through what? | (A) the caller's own database role, as P4's report path writes; rules unchanged. (B) a rules write grant to the signed-in reader. (C) the MCP front door with a `roadmap:write` scope. | **(C), not the recommendation: a front-door `roadmap:write` scope.** Every person-written change to the record — a move, an edge, a release — goes through the MCP front door under that scope, and never through a person's own database role. **This amends the MCP plan's D927** (the scope vocabulary): `roadmap:write` joins it, as D1227 added `state:read` and `state:write`. **The consequences, which the parts below carry rather than paper over:** (1) the front door is **off on every installation** (D934) and **no installation serves it** — the task that mounts it on a binary, the MCP plan's **M12**, was written on 2026-11-11 and is built, dormant: the guard that held it (`ee/coordinator/mcpauth/server_test.go`'s `TestNoBinaryLinksTheFrontDoorYet`) is deleted by it, no release writes the switch that turns the door on, and a build without M12b's delivery refuses to enable it (**D2126**) — so the write half cannot be served until that delivery lands (**D2119**, answered the same day: the MCP plan's **M12**); (2) the front door's account holds **no database write** — D922's answer, amended by H3, is "no database admin on the public service" — so how its writes reach the database was a new question (**D2118**, answered the same day, finally: a queue the front door writes and a non-public apply job running as the coordinator's account; D922/H3 stands); (3) the CLI and the console, which reach nothing but through the CLI, need a bearer token holding the scope (**D2122**); (4) who may be granted the scope is the MCP plan's lists question again (**D2121**). | P9b, P9d, P9e, P9f, GV3.1 |
| **D2111** | Who populates the record's tasks from the plan files? | (A) the sweep, from each repository's default branch, through P2's token with `contents: read` added. (B) an admin-run refresh from a checkout. | **First answered (A), then changed the same day (2026-11-11) to a PUSH MODEL WITH NO REPOSITORY — closest to (B), but through the front door and not as an admin.** The roadmap record is **project-level** and works with no particular repository: a project has many, and which one the coordinator should read is not a question it can answer. **Plans, releases and cross-plan edges are all written through the `roadmap:write` scope (D2110) by whoever holds it, from any checkout of any repository**: `fugaro coordinator roadmap publish` (P9c) reads the plan's graph artifact in the current checkout and writes the plan document. Each plan document carries its source — repository, path, commit — **as provenance only, never as a dependency**, and **nothing reads a repository on the coordinator's behalf**: no sweep read, no `contents: read` on P2's token, no Bitbucket path. | P9c |
| **D2112** | Where do cross-plan and cross-stream edges come from? | (A) the record itself, written by a command. (B) a schema-4 artifact field. | **(A), the recommendation**, written through the front door (D2110). | P9a, P9b, P9d |
| **D2113** | Does the record store releases and streams? | (A) no document of their own; checked against `docs/release-streams.yaml`. (B) mirror the catalogue into the record. | **Neither as written: releases are stored in the record, as documents of their own, and are NOT checked against `docs/release-streams.yaml`**, which exists only in the Fugaro repository itself; a watched repository without such a file must work. A release is `id`, `stream`, `name`, `state` and `order`, written through the same `roadmap:write` scope; a move into a release the record calls finished is refused by the record's own state. Whether the Fugaro repository's catalogue may seed them is **D2120**. | P9a, P9b, P9d |
| **D2114** | What is a task's release before anybody moves it? | (A) the plan's own `version`, read and marked "from the plan", never written. (B) the refresh seeds an assignment. | **Follows from D2113; the reading this plan takes is (A)'s, with one change it forces.** A task with no assignment is **unassigned**: nothing is written into the record for it, and the graph draws it in its stream's **unassigned** lane, with the plan's own `version` shown beside it as text marked "from the plan". It is **not** placed in a release box, because a stored release carries a name and no version, so the plan's `0.24` cannot be matched to a release without inventing a rule — and a watched repository's plans may name versions no release carries. `--expect` for an unassigned task is the word `none`, not the plan's value. | P9a, P9b, GV4.1 |
| **D2115** | What happens to an assignment or an edge whose task leaves every plan? | (A) kept and shown `dangling`, pruned 30 days later. | **(A), the recommendation.** Under D2111's push model a task leaves every plan when a publish drops it or an unpublish removes its plan, and the prune runs lazily inside publish and unpublish, applied by the apply job (D2125, answered). | P9a, P9c |
| **D2116** | The record's size bounds. | (A) 200 plan documents per repository, 1,000 tasks and 256 KiB per plan document, 2,000 cross-plan edges per project, titles of 200 characters and reasons of 500. | **(A), the recommendation.** A bound for releases was not in the question; **D2120** proposes one. | P9a, P9b, P9c |
| **D2117** | Where does the console read the record? | (A) `fugaro graph`'s coordinator scope. | **(A), the recommendation.** The read stays a database read through the person's own credential (`roles/firebasedatabase.viewer`), as §6.22 reads today; only the writes go through the front door. | GV4.1 |

**The decisions D2110's, D2111's and D2113's answers raised (D2118–D2123), and the ones those raised
(D2124–D2126): all answered by the owner on 2026-11-11.** Each was written recommendation first;
the column "Answer" says whether the owner took it. **D2118 was answered twice**: first with a
database write role on the front door's account (against the recommendation), and then, once the
owner had read that role's blast radius under D2124, **back to the original recommendation — the
queue and a non-public apply job**. So D922/H3 stands: the public service holds no database write.

| Decision | The question | Recommendation as written | Answer (owner, 2026-11-11) | Blocks |
| --- | --- | --- | --- | --- |
| **D2118** | The front door's account may not hold a database write (D922, H3: "no database admin on the public service"), and the subtree's rules are `.write: false` to every client. How does a `roadmap:write` call reach the record? | **(A)** the front door applies nothing itself; it queues the request and a non-public apply job, running as the coordinator's account, applies it. (B) a database write role on the front door's account. (C) the person-written documents in the front door's bucket. | **(A), the recommendation — in the end.** The owner first answered (B); under D2124 he read what that role would reach (below) and went back to (A). **The front door holds no database write, and D922/H3 is unchanged.** The queue, the apply job and what each may do are P9e. | P9b, P9d, P9e, P9g |
| **D2119** | No task mounts the MCP front door on a binary: the MCP plan's M7 and M8 and the stateless plan's O16b all wait on that task, and the MCP README says no task of that plan carries it. P9e waits on it too. Who writes it? | (A) the MCP plan writes it, as a task of its own. (B) this plan writes it. | **(A), the recommendation — the owner's ruling of 2026-11-11.** It is **M12** in `docs/mcp-server/implementation-plan.md`: the front door mounted on a binary, dormant until enabled. M7's and M8's unserved halves, the stateless plan's O16b and this plan's P9e all depend on it. | P9e, P9f, GV3.1, GV3.2 |
| **D2120** | Releases' bounds, and may a repository's `docs/release-streams.yaml` seed them? | **(A) At most 200 releases per project and 32 streams**, a release `id` of `^[a-z0-9][a-z0-9.-]{0,39}$` and a `name` of 64 characters; a stream name of `^[a-z0-9][a-z0-9-]{0,31}$`. **Seeding is an optional flag of the publish** — `fugaro coordinator roadmap publish --releases <file>` (P9c), for a repository that carries such a catalogue, writing one release per catalogue line through the same scope and skipping any that exists — **never automatic**, so a repository without the file loses nothing and the file is not a second source after the import. | **(A), the recommendation.** | P9a, P9c |
| **D2121** | Which of the front door's identities may hold `roadmap:write`? | **(A) An operator only** (D933's operator list), and **never by default**: `roadmap:write` is not in `DefaultScopes` (the stateless plan's O16a sets that list to `tasks:read, tasks:write`), so a sign-in that names no scope never holds it, and a paired phone's approval page names it in words when it is asked for. A launcher asking for it is refused. | **(A), the recommendation.** | P9d |
| **D2122** | How does the CLI — and so the console, which reaches nothing but through the CLI — get a bearer token holding `roadmap:write`? No client of the front door exists in the tree. | **(A) A CLI client of the front door**: `fugaro coordinator login --scope roadmap:write` runs the authorization-code flow with PKCE M5 serves, on a loopback redirect, as a client registered for the installation; the refresh token is kept in the operating system's keychain and never in a file; the console spawns the CLI, which presents it. It is part **P9f**. | **(A), the recommendation.** | P9f, GV3.1 |
| **D2123** | Under the push model a plan id is a project-wide name (D2111), so two repositories can publish a plan of the same id. What does a publish do when the stored document's provenance names another repository? | **(A) Refuse it**, naming the repository the stored plan came from, unless the publish says `--replace-source`; a publish from the same repository replaces as usual. Provenance stays provenance — nothing reads it — but a silent overwrite of another repository's plan is the one place it is worth a sentence. | **(A), the recommendation.** | P9b, P9c |
| **D2124** | Confirm D2118 against the one path-scoped alternative: a database-wide IAM role on the public service, or a rules grant on `coordinator/project/roadmap` reached by a custom-token claim the front door mints for itself? | (B) the claim-scoped rules grant. (A) keep D2118's database-wide role. | **Neither: the owner went back to D2118's original recommendation, the queue and the apply job**, after seeing the role's blast radius. No database role on the front door, no custom-token claim, no rules change. | P9b, P9e, P9g |
| **D2125** | With no apply job and no sweep in the roadmap's path, where does D2115's 30-day prune of dangling assignments and edges run? | (A) lazily inside publish and unpublish, at most 100 deletions per call. | **(A), the recommendation — run by the apply job** when it applies a publish or an unpublish request, as the coordinator's account; the front door deletes nothing. | P9b, P9e |
| **D2126** | M12 owes M5b's delivery of a sign-in flow to the paired device (a QR of the flow's binding on the launcher's page, the phone reading it), which touches the mobile app and the console. Does M12 carry it? | (A) a task of its own, M12b; M12 refuses to enable a door that lacks it. | **(A), the recommendation.** M12b is in `docs/mcp-server/implementation-plan.md`. | M12, P9e |

**Considered and declined on 2026-11-11: a database write role on the front door.** Recorded so a
later reader finds it priced rather than lost. The role would have been a custom one holding
`firebasedatabase.instances.get` and `.update` on the `mcp_server` account. Realtime Database IAM has
no path-level scope (`SECURITY.md`, the credentials service's paragraph), so it would have reached
the whole database — the policy, the stop switches, the devices and pairing nodes, spend, outcomes,
the credentials service's nodes — from the one service that answers the internet, with the front
door's own code as the only fence. The path-scoped alternative (a rules grant reached by a claim the
front door mints) would have reversed D923 and been forgeable by any launcher. The owner declined
both and kept the queue.

**The decision these answers raised (D2127), answered by the owner on 2026-11-11: number matching,
the recommendation.**

| Decision | The question | Recommendation | Answer (owner, 2026-11-11), and the alternative as written | Blocks |
| --- | --- | --- | --- | --- |
| **D2127** | M12b delivers a sign-in flow to the paired phone, and finding M-a admits two ways to bind the approval to the sign-in in front of the person: a **QR** of the flow's binding on the launcher's page that the phone scans, or **number matching** (the page and the phone show the same short number and the person confirms they agree). Which does M12b build? | **(A) Number matching.** It needs no new dependency (a QR encoder for the page and a camera scanner in the app would be two, which the repository adds only with the owner's go), it works when the launcher's page is on another machine, and the phone already shows the client marked unverified and the redirect host (M-a). | **(A), the recommendation — number matching** (owner, 2026-11-11). The alternative as written: **(B) QR**: proves the phone is in front of the page, which number matching proves only through the person; costs a Go QR encoder and a mobile scanner dependency, each to be approved. | M12b |

---

### P9a — The roadmap record: its shape, its nodes and its reader (D2112–D2116, D2120)

**Scope.** The record's shape, **version 1**, and its reader; no writer and no publish. Four kinds of
document under `internal/coordpath/coordpath.go`'s `PathProject`, each keyed by
`internal/coordpath/coordpath.go`'s `DocumentID` over what it describes, so the same thing described
twice is one node:

| Node | Key: `DocumentID` over | Holds | Written by |
| --- | --- | --- | --- |
| `coordinator/project/roadmap/plans/<id>` | `roadmap-plan`, the plan id | one published plan artifact: `schema` (1), `plan` (GV1.6's plan id, the project-wide name), `stream` (the artifact's own, a display input), `source` (`repo`, `path`, `commit`, `graph_schema`: **provenance only**, D2111 — nothing reads it back and no write depends on it), `tasks` (`id`, `title`, `kind`, `size`, and `version`, the artifact's own target, a display input), `edges` (`task`, `needs`, both inside the plan) | the publish (P9c), through the front door |
| `coordinator/project/roadmap/releases/<id>` | `roadmap-release`, the stream, the release id | one release (D2113): `schema`, `id`, `stream`, `name`, `state` (`open` or `finished`), `order` (its position in its stream), `by`, `at` | the release tool (P9d), applied per D2118 |
| `coordinator/project/roadmap/assign/<id>` | `roadmap-assign`, the qualified task id | where a task ships: `schema`, `task` (`<plan>/<task>`), `release` (a release's key), `prev` (the release it replaced, or null — what an undo inverts), `by`, `at` | the move tool (P9d), applied per D2118 |
| `coordinator/project/roadmap/edges/<id>` | `roadmap-edge`, both endpoints | one cross-plan or cross-stream dependency: `schema`, `task` and `needs` (each a qualified id), `reason`, `by`, `at` | the edge tools (P9d), applied per D2118 |

The node names match `internal/plan/document.go`'s `Document`, `Node` and `Edge` so a schema-3 graph
(`kind`, `size`) is carried without renaming; `stubs`, `decoupling`, `contract_changes` and the
gain stay in the artifact, where the planner reviews them. **Streams are not a document**: a stream
is the `stream` field its releases carry, so the set of streams is read from the releases, and a
watched repository that has no `docs/release-streams.yaml` has exactly the streams its releases name
(D2113). The plan artifact's own `stream` stays the parser's closed set of three
(`internal/streams/streams.go`'s `Known`); it is a display input and never matched against a
release. **A plan id is a project-wide name** (D2111): two repositories that publish a plan of the same id are publishing one plan, and what a publish does when the stored document's provenance names another repository is **D2123**. There is **no run-derived state**: running, done and blocked are computed by the reader from
runs, as `internal/plan/state.go`'s `State` already does, so the record holds no second answer that
ages. A task with no assignment is **unassigned** and has no document (D2114). An assignment or an
edge whose endpoint no plan document carries is `dangling` (D2115); an assignment naming a release
that is gone is `dangling` too.

**Interfaces.** `coordinator.Roadmap` (the plans, the releases, the assignments, the edges, an
`unreadable` count and the `dangling` ids) and a `Store` method that reads it; a qualified-id helper
that splits at the last slash, as `internal/plan/state.go`'s `splitPlanTask` does; node-path helpers
beside `PathIntents`; the shape checks for a release id, a stream name and a name (D2120). Every
document carries `schema: 1` and is decoded strictly; a document of a newer schema, or one that does
not decode, is skipped and counted in `unreadable` rather than read as empty and rather than failing
the whole read (the graph's `unreadable` source state, `console/docs/json-contract.md` §6.22). Every
text field (titles, names, reasons, `by`) passes through `internal/safetext/safetext.go`'s `Field` on
read as well as on write. A field never changes meaning: a later shape is `schema: 2`.

**Files (at most 4).** A new roadmap.go and roadmap_test.go in `ee/coordinator/` (each with the
two-line notice), and their fixtures under `ee/coordinator/testdata/` with the licence siblings the
fixtures there carry.

**Tests.** `TestRoadmapDocumentsRoundTripStrictly`, `TestRoadmapKeysAreDeterministic` (one
description, one key; another plan, task or stream, another key; the provenance is in no key),
`TestRoadmapStreamsAreReadFromTheReleases` (no catalogue file is read anywhere in the package),
`TestRoadmapSkipsANewerSchemaAndCountsIt`, `TestRoadmapUndecodableDocumentIsCountedNotFatal`,
`TestRoadmapDanglingEndpointIsNamed`, `TestRoadmapUnassignedTaskHasNoDocument`,
`TestRoadmapTextIsSanitisedOnRead`, `TestRoadmapPathsStayInTheSubtree`,
`TestQualifiedIDSplitsAtTheLastSlash` (`plans/2026-10-15-desktop-console-v2/V2.5.3`).

**Red step.** `TestRoadmapSkipsANewerSchemaAndCountsIt` fails against a reader that decodes
leniently. Mutations, each must fail a test: drop the strict decoder's unknown-field refusal; put a
clock into a key; return the whole read's error on one bad document; read the streams from a file.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./ee/coordinator/ -run
'Roadmap|Qualified' -count=1`; `go test ./internal/eeboundary/ -count=1`.

**Depends on.** P3 (the subtree and `Store`) and P4 (the ID idiom), both built; D2112–D2116
and D2120 (answered).

**Reviewer tier.** Careful. **Security-sensitive.** No — it reads, and adds no writer, no grant and no
rule; the review confirms the strict decode and the sanitising.

**Commit.** `coordinator: the roadmap record's shape, its keys and its reader`

---

### P9b — The roadmap writers: compare-and-set, the refusals, and the identity that applies them (D2110, D2118)

**Scope.** The writer library over P9a's shape, and the database half of the fence. It is called by
one thing: the **apply job** (P9e), a non-public job running as the coordinator's account, applying
the requests the front door's `roadmap:write` tools queue (D2118 as finally answered). No CLI command,
no person's credential and not the front door calls it: every change — a published plan, a move, an
edge, a release — reaches the record only through the front door's queue and the apply job.

- **The plan document's publish and unpublish**: read, compare, and put on the document's own ETag;
  an unchanged document writes nothing and moves no ETag (P5's idempotency); a publish carrying
  `expect` (the document version the publisher last read) is refused when the stored version moved;
  a publish whose provenance names another repository than the stored one follows D2123.
- **The move**: read the assignment and its ETag; refuse, writing nothing, when the stored release is
  not the request's `expect` (`none` for an unassigned task, D2114); refuse a task no plan document
  carries, a release the record does not hold, and a release whose own `state` is `finished`
  (D2113); then put on the ETag it read, with `prev` set to the release it replaced. **A lost ETag
  race is re-read and re-compared against `expect`, never retried blind** — which is why the move
  does not call `ee/coordinator/state.go`'s `Write`, whose loop retries the put whatever the new
  value is. The unassign is the same move to `none`.
- **The release set**: create a release, or change its `name`, `state` or `order`, on its ETag;
  refuse a change of its `id` or `stream` (a new release is a new document), a delete while an
  assignment names it, and a write past D2120's bounds.
- **The edge add and remove**: refuse a self-edge, an edge inside one plan (that is the artifact's,
  D2112), an endpoint no plan document carries, an edge that closes a cycle across the record and
  the plan documents (a cycle is a refusal, not a render), and a write past D2116's bounds.

**The fence.** The rules do not change: `.write: false` stays on the whole subtree, and three client
identities are shown unable to write any roadmap node — the signed-in reader (the phone's and the
console's claim), a project token and a run's token. The one writer is the coordinator's account,
which already holds the database role (D194) and is reachable from no network; its code reaches the
database only through `Store`, whose subtree check keeps it under `coordinator/` (P3). **The front
door's account holds no database write** (D922/H3, unchanged), and no job's account holds a database
role.

**Interfaces.** The writers as `Store` methods taking an `Actor` (the verified principal the front
door recorded on the queued request, which becomes `by` on every document); `ErrMoveStale`, `ErrUnknownTask`,
`ErrUnknownRelease`, `ErrReleaseFinished`, `ErrEdgeRefused` (with the reason) and
`ErrRoadmapTooLarge`, each a sentence naming what to change.

**Files (at most 6).** A new roadmap_write.go and roadmap_write_test.go in `ee/coordinator/`;
`internal/budget/rules/coordinator_test.go` and `internal/budget/rules/emulator_coordinator_test.go`
(the three identities at each roadmap node); `internal/infra/firebase_test.go` (the Firebase root's
admin members are the budget admins, the history account and the coordinator's account, never a
job's account and never the front door's). `SECURITY.md` is P9g's, not this part's.

**Tests.** `TestMoveRefusesAStaleExpectAndMovesNothing`, `TestMoveLostRaceIsReComparedNotRetriedBlind`
(a second writer between the read and the put), `TestMoveRecordsPrevSoTheInverseRestores` (a move and
its inverse leave the document as it was but for `by` and `at`), `TestMoveRefusesAnUnknownTaskOrRelease`,
`TestMoveIntoAFinishedReleaseIsRefusedByTheRecord`, `TestReleaseIDAndStreamNeverChange`,
`TestReleaseInUseIsNotDeleted`, `TestEdgeRefusesSelfInPlanAndCycle`, `TestRoadmapBoundsAreEnforced`,
`TestPublishUnchangedWritesNothing`, `TestPublishWithAStaleExpectIsRefused`, `TestRoadmapNodesAreWrittenByNoClient` (the generated rules),
the emulator's three identities refused a write at each node, and
`TestNoJobAccountHoldsTheDatabase` and `TestFrontDoorHoldsNoDatabaseWrite`, and
`TestPruneDeletesAtMostOneHundredDanglingItems` (D2125).

**Red step.** `TestMoveLostRaceIsReComparedNotRetriedBlind` fails when the move is built on `Write`
— the mutation that makes this a compare-and-set test rather than a round trip. Then, in a scratch
tree: add `.write: "USER"` under the roadmap node of `internal/budget/rules/rules.json.tmpl`, and both
rules tests must fail; grant a job's account, or the front door's, a database role in the Firebase
root, and the IAM tests must fail.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./ee/coordinator/
./internal/budget/rules/ ./internal/infra/ -run 'Roadmap|Move|Release|Edge|Publish|Coordinator|NoJobAccount'
-count=1`; the emulator half under the `firebase` tag (`go test -tags firebase
./internal/budget/rules/ -run Coordinator -count=1`), which CI's firebase job runs.

**Depends on.** P9a; D2110, D2112–D2114, D2116, D2118, D2120, D2124 and D2125 (answered).

**Reviewer tier.** **SECURITY-SENSITIVE** — **opus + mandatory independent review**, and the run asks
for **level 3 in the run** (`--review-levels`), never a separate local reviewer.
**Security-sensitive.** **Yes — the writers into the subtree the rules keep every client out of.**
The pull request body must state: that the rules are unchanged; which identities were shown unable
to write; that the one writer is the coordinator's account through the apply job and the front
door holds no database write (D2118, D922/H3); why the move does not retry blind; the prune's
bound; and the size bounds.

**Commit.** `coordinator: the roadmap writers, compare-and-set, and the identities kept out`

---

### P9c — `fugaro coordinator roadmap publish`: a plan into the record, from any checkout (D2111, D2120, D2123)

**Scope, to D2111 as the owner answered it (the push model, no repository).** The command that fills
the record's plan documents. Run in any checkout of any repository by a person holding a
`roadmap:write` token (P9f's login), `fugaro coordinator roadmap publish [<plan>]` reads the plan's
graph artifact in the current checkout — named by `internal/plan/document.go`'s `ArtifactSuffix`,
under `docs/`, through GV1.6's confined walk (no symlink out of the checkout, no non-regular file,
the 1 MiB per-artifact limit) — parses it with `internal/plan/read.go`'s `Parse` (a refused artifact
is refused whole, never half-published), trims it to P9a's shape, gives it its plan id by
`internal/plan/read.go`'s `IDFromPath` — the rule the plan-status command computed for itself until
this part moved it into `internal/plan`, so the CLI's two readers share one copy — and calls P9d's
`roadmap_publish_plan` tool with the document and its
**provenance**: the checkout's origin repository, the artifact's path and the commit `HEAD` names, a
working tree with changes to the artifact being refused (D30's "clobber uncommitted work", turned
around: what is published is what is committed). The provenance is recorded and never read back; no
write depends on it except D2123's one sentence.

**Idempotent, and compare-and-set.** Publishing the same artifact twice writes nothing the second
time (P9b's publish compares before it puts); a publish carries the document version it last read as
`expect`, so two people publishing one plan from two checkouts cannot silently overwrite each other —
the second is refused and told to read again. `--all` publishes every artifact under the checkout's
`docs/`, each its own call; `unpublish <plan>` removes a plan document through the same scope (and
its tasks' assignments and edges become `dangling`, D2115).

**Releases, optionally (D2120).** `--releases <file>` imports a repository's release catalogue — the
Fugaro repository's `docs/release-streams.yaml` is the one known — as one `roadmap_release_set` call
per line, skipping a release that exists. It is a flag a person passes, never a default, and a
repository without such a file publishes its plans exactly the same way (D2113).

**What is not in this part, deliberately.** No sweep reads a repository, no installation token gains
`contents: read`, and no Bitbucket path is involved: D2111's answer is that nothing reads a
repository on the coordinator's behalf. The coordinator's sweep (P5) is unchanged by these parts.

**Interfaces.** `publish` and `unpublish` and their `--json` documents (what was sent, the version
written, or the refusal), in the `roadmap` group P9f creates; the plan-id rule as an exported function
of `internal/plan`.

**Files (at most 7).** The roadmap command file in `ee/coordinator/cli/` and its test (P9f's, edited),
a new publish file and its test there, `internal/plan/read.go` and `internal/plan/read_test.go` (the
moved plan-id rule), `internal/cli/plan_status.go` (calls it).

**Tests.** Against a fake front door: `TestPublishSendsTheTrimmedArtifactAndItsProvenance`,
`TestPublishTwiceWritesOnce`, `TestPublishWithAStaleVersionIsRefused`,
`TestPublishRefusesAnUncommittedArtifact`, `TestPublishRefusesARefusedArtifactWhole`,
`TestPublishStaysInTheCheckout` (a symlinked artifact or `docs/` pointing outside is refused),
`TestPublishFromAnotherRepositoryIsRefusedWithoutReplaceSource` (D2123),
`TestPublishReleasesFlagSkipsWhatExists`, `TestPublishWithoutACatalogueNeedsNone`,
`TestPublishNeverWritesTheDatabaseItself`, and `TestPlanIDFromPath` where the rule moved to.

**Red step.** `TestPublishTwiceWritesOnce` fails against a publish that stamps a time into the
document. Mutations: let the command call P9b's writer with the person's credential, and
`TestPublishNeverWritesTheDatabaseItself` must fail; follow a symlink in the walk, and
`TestPublishStaysInTheCheckout` must fail.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./ee/coordinator/cli/ -run
'Publish|Roadmap' -count=1`; `go test ./internal/plan/ -count=1`; `go test ./internal/cli/ -run
'PlanStatus|PlanID' -count=1`.

**Depends on.** P9d (the publish tool) and P9f (the login and the `roadmap` group); D2111 (answered);
D2120, D2122, D2123 and D2125 (answered). It builds against a
fake front door; it writes a real record only once **P9e** serves the tools.

**Reviewer tier.** **SECURITY-SENSITIVE** — **opus + mandatory independent review**, level 3 in the
run. **Security-sensitive.** **Yes — it reads files from a checkout and sends them to a write scope.**
The pull request body must state: the walk's confinement; that nothing but the artifact leaves the
checkout; that the provenance is never read back; and that the command holds no database writer.

**Commit.** `coordinator: roadmap publish, a plan into the record from any checkout (P9c)`


---

### P9d — The `roadmap:write` scope and the roadmap tools on the front door, dormant (D2110, D2121)

**Scope.** The front door's half of D2110's answer, built the way the stateless plan's **O16a** builds
its state group: the scope, the catalogue rows and the handlers, **registered by a function nothing
calls**, so nothing is served. In `ee/coordinator/mcpauth/`:

- **The scope.** `roadmap:write` joins the vocabulary in `ee/coordinator/mcpauth/limits.go`
  (`ScopeVocabulary`, which `KnownScope` reads), additively — no stored token record changes shape
  and no name changes meaning — and the metadata document's `scopes_supported` lists it. It is
  **not** in `DefaultScopes` (O16a's list), so an authorization that names no scope never holds it.
- **The verifier's check.** `ee/coordinator/mcpauth/scopes.go`'s `CheckScopes` already refuses an
  unknown scope whole; the new rule is the identity's: a token holding `roadmap:write` is served only
  to an identity on the **operator** list (D2121), checked where M7 resolves the identity, and a
  launcher asking for the scope at the authorize endpoint is refused there, before a code is issued.
  The approval page (M5, M5b) names the scope in words, so a paired phone that approves it is shown
  what it approves.
- **The tools.** Catalogue rows in `ee/coordinator/mcpauth/tools.go`, each mapped to `roadmap:write`:
  `roadmap_publish_plan` (the trimmed plan document, its provenance, `expect`) and
  `roadmap_unpublish_plan` (`plan`, `expect`), `roadmap_move` (`task`, `to`, `expect`),
  `roadmap_need` and `roadmap_unneed` (`task`, `needs`, `reason`), `roadmap_release_set` (`id`,
  `stream`, `name`, `state`, `order`, `expect`). The handlers
  validate the arguments with P9a's shape checks and D2116's and D2120's bounds, read the record to
  refuse a stale `expect` at once, and **queue** the request (P9e): the answer is the request's id, never a database path. A seventh
  tool, `roadmap_request` (`id`), reads one request's outcome back — `applied`, `refused` with the
  reason and the current value, or `failed` — for the caller that queued it. The front door writes
  nothing to the database (D2118, D922/H3). The answer is the request's id and what it asked for,
  and never a database path (the MCP spec's §9: the node layout stays private).
- **Dormant, and licensed.** The package is business-licensed (`ee/`, `.claude/rules/ee.md`); the front
  door is off on every installation (D934), and the front door's own command is the only thing outside the package that names it: a door is served only where an installation turns it on (D934), and a build without M12b's delivery refuses to enable one (D2126).

**Interfaces.** `ScopeRoadmapWrite = "roadmap:write"`; the seven catalogue rows; `type RoadmapQueue
interface` (submit one request, read one request's outcome) — **no database write method**;
`RegisterRoadmapTools(reg, queue, reader)`.

**Files (at most 7).** `ee/coordinator/mcpauth/limits.go`, `ee/coordinator/mcpauth/limits_test.go`,
`ee/coordinator/mcpauth/authorize.go` and `ee/coordinator/mcpauth/authorize_test.go` (the operator
check at the authorize endpoint), `ee/coordinator/mcpauth/tools.go`, and a new roadmap tool file and
its test in `ee/coordinator/mcpauth/`.

**Tests.** `TestRoadmapWriteIsKnownAndNotADefault`, `TestAnAuthorizationWithNoScopeNeverHoldsRoadmapWrite`,
`TestALauncherCannotObtainRoadmapWrite` (refused at authorize) and `TestALauncherTokenWithRoadmapWriteIsRefused`
(a record that says it holds the scope is refused at the door for an identity off the operator list),
`TestApprovalPageNamesRoadmapWrite` (the paired phone's view of the scope list),
`TestARunTokenOrAProjectTokenIsNotAFrontDoorToken` (a database custom token of the RUN or TOKEN shape
presented as a bearer is refused before any scope is read), `TestATaskScopeTokenCannotReachTheRoadmapTools`
and `TestAStateScopeTokenCannotReachTheRoadmapTools`, `TestRoadmapToolsMapToRoadmapWrite`,
`TestRoadmapToolsRefuseAStaleExpectBeforeQueueing`, `TestRoadmapQueueHasNoDatabaseWriteMethod`
(reflective), `TestARequestOutcomeIsReadOnlyByItsCaller`, `TestARoadmapAnswerNamesNoDatabasePath`, `TestATokenMintedBeforeTheRoadmapScopeStillServes`
(the HARD RULE's converge test: a stored record holding today's scopes is served unchanged), and `TestTheFrontDoorIsServedOnlyWhenEnabled` unchanged.

**Red step.** `TestRoadmapWriteIsKnownAndNotADefault` fails to build (the constant is undefined); then
`TestAnAuthorizationWithNoScopeNeverHoldsRoadmapWrite` fails once the vocabulary grows and the
no-scope answer still grants the whole vocabulary — the silent widening O16a's R1 exists to stop.
Mutation: drop the operator check, and both launcher tests must fail.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./ee/coordinator/mcpauth/ -run
'Scope|Roadmap|Authoriz|Catalogue|Metadata|Vocabulary|NoBinary' -count=1`; `go test
./internal/eeboundary/ -count=1`.

**Depends on.** P9a, P9b; M7 (built); the stateless plan's **O16a** (it edits the same three files and
sets `DefaultScopes`; this part serializes after it); D2110, D2118 and D2121 (answered).

**Reviewer tier.** **SECURITY-SENSITIVE** — **opus + mandatory independent review**, **level 3 in the
run** (`--review-levels`). **Security-sensitive.** **Yes — a write scope on a public authorization
server's vocabulary, its default, and who may hold it.** The pull request body must state: the
vocabulary before and after; that the default is unchanged; the operator check and where it runs;
what a paired phone is shown; and that nothing is served.

**For operators.** None: nothing is served and no record changes shape.

**Commit.** `feat(mcpauth): the roadmap:write scope and the roadmap tools, dormant (P9d)`

---

### P9e — The roadmap tools served, the queue and the apply job (D2118, D2119, D2125) — WAITS on M12

**Waits on, and the trigger for each.** (1) **M12** in `docs/mcp-server/implementation-plan.md`, the
front door mounted on a binary — the owner's ruling of 2026-11-11 (D2119); M12 in turn enables a door
only once **M12b** has landed (D2126). **Until M12 lands this part cannot be built to a served door,
and neither can GV3.1's or GV3.2's live half.** (2) P9d merged.

**The queue: its store and its retention.** A bucket of its own, `fugaro-roadmap-queue-<project>`,
created under the same `var.mcp_server` switch as the front door — **not** the D935 record store,
whose IAM names the front door's account alone and must keep doing so. Uniform bucket-level access,
public access prevention enforced, and exactly two bindings: the front door's account
`roles/storage.objectCreator` and `roles/storage.objectViewer` (it writes a request and reads its
outcome; it cannot overwrite or delete either), and the coordinator's account
`roles/storage.objectAdmin`. One object per request, named by the request's id, holding the
verified principal, the tool, its arguments and the `expect` the tool already checked; its outcome is a
second object beside it. **Retention: seven days**, by a lifecycle rule on the bucket, so a request
and its outcome are deleted whatever happens to the job; the project viewers' convenience bindings
are stripped with `internal/infra.RemoveProjectViewers`, as the D935 store's are.

**The apply job: its identity, its trigger, what it may write.**
- **Identity:** the coordinator's account (`fugaro-coordinator`, P5), which already holds the
  database role and the subtree check (D194, P3) and is not reachable from any network. The front
  door's account holds **no database write** (D922/H3).
- **Trigger:** the front door starts one execution of the job (`fugaro coordinator roadmap apply`)
  after it queues a request, through **one** grant — `run.jobs.run` on that job alone, no overrides
  (the execution reads the queue itself, so nothing a caller sends rides on the execution). A
  Scheduler entry every 5 minutes, `retry_count = 0`, applies anything a failed start left. Two
  executions at once are harmless: each claims a request by writing its outcome object with a
  generation precondition of zero, and the loser skips it, so **a request is applied at most once**.
- **What it may write:** the roadmap's nodes, through P9b's writers and nothing else (asserted on its
  dependency list), and the outcome objects; it reads the record and the queue. It reads no
  repository (D2111).
- **Validation:** each request is applied by P9b's compare-and-set rules, so `expect` is checked
  **again** at apply time, a lost ETag race is re-read and re-compared, and every refusal P9b names
  (unknown task or release, a finished release, a cycle, a bound) is applied here as there.
- **What is reported back:** the outcome object — `applied` (with the new version), `refused` (the
  error's sentence and the value the record holds now, so the caller can re-read and try again), or
  `failed` (the database could not be reached; the job tries a request at most 3 times across
  executions and then writes `failed`, so nothing stays pending past its retention). The front door's
  `roadmap_request` tool reads it; the CLI waits for it (P9f).
- **The prune (D2125):** when the job applies a publish or an unpublish request, it also deletes, as
  the coordinator's account, at most 100 assignments and edges that have been `dangling` for 30 days.

**Files and Terraform.** The apply command and its test in `ee/coordinator/cli/`, the queue reader and
writer in `ee/coordinator/` and the front door's `RoadmapQueue` in `ee/coordinator/mcpauth/`; in
`deploy/terraform/gcp/modules/installation/`, beside the coordinator's job: the queue bucket and its
two bindings, the apply job, its schedule, and the front door's `run.jobs.run` on that job; a
`grantRules` row in `internal/infra/tf/cover.go` added on purpose (`.claude/rules/terraform.md`); the
golden plans regenerated by `scripts/gen-golden-plans.sh`; P9d's tools registered beside M8's in the
file M12 names; the release's notes under `docs/releases/` (`### For operators`: "run `fugaro
update`", and that the managed plan gate asks its own typed confirmation for an IAM grant).

**Tests named now.** `TestFrontDoorHoldsNoDatabaseWrite` (still, after this part),
`TestApplyJobIsNotPublic` (no invoker binding), `TestFrontDoorMayRunOnlyTheApplyJob` (and without
overrides), `TestQueueBucketHasExactlyTwoMembers`, `TestTheD935StoreKeepsItsOneMember`,
`TestApplyRechecksExpectAtApplyTime`, `TestARequestIsAppliedAtMostOnce` (two executions, one
request), `TestAFailedRequestIsReportedAfterThreeAttempts`, `TestQueueRetentionIsSevenDays`,
`TestApplyWritesOnlyTheRoadmap` (dependency list), `TestApplyPrunesAtMostOneHundred`, and
`TestUpdateConvergesAnInstallationAtThePreviousRelease` (the HARD RULE's converge test: an
installation with the front door on at the release before converges in one `fugaro update`, and a
second writes nothing).

**No conflict with the stateless plan's O16b.** O16b gives the front door's account a get-only
database role (D2020 (a)); this part gives that account no database role at all, and the writer is
the coordinator's account. The two land in either order.

**Runs in flight.** The job, the bucket and the grant are the front door's and the coordinator's; no
run holds them, and a run in flight reads and writes exactly what it did.

**Reviewer tier.** **SECURITY-SENSITIVE** — **opus + mandatory independent review**, level 3 in the
run. **Security-sensitive.** **Yes — the path by which a request from outside reaches the database.**
The pull request body must state: the front door still holds no database write; the queue's two
bindings and its retention; the one `run.jobs.run` grant and that it carries no override; that each
request is re-checked and applied at most once; and what the apply job may write.

**Commit.** `coordinator: the roadmap tools served, queued and applied by a job that is not public (P9e)`

---

### P9g — `SECURITY.md` and the website describe the roadmap queue and its apply job (D2118)

**Scope.** Docs only, landing with P9e or right after it, in the same release. The section of
`SECURITY.md` on the hosted MCP front door **keeps saying that the public service holds only the
database viewer** — it still does — and gains what is new: the roadmap queue bucket and its two
members, that the front door may start the apply job and nothing else of it, and that the apply job
runs as the coordinator's account, re-checks each request and writes only the roadmap.
`website/content/security.md` is regenerated from it with `go run ./internal/clidoc`
(`.claude/rules/credentials-service.md`), and `docs/mcp-server/spec.md` §8 gets a dated note naming
the queue beside the D935 store.

**Files (at most 3).** `SECURITY.md`, `website/content/security.md` (generated), `docs/mcp-server/spec.md`.

**Tests.** The existing website-security drift tests, the MCP folder's docs tests, and the website's
`npm run check` for the page.

**Verification.** `go test ./internal/cli/ -run Docs -count=1`; `cd website && npm run check`.

**Depends on.** P9e (the text describes what P9e builds); D2118 and D2124 (answered).

**Reviewer tier.** **SECURITY-SENSITIVE** — **opus + mandatory independent review**, level 3 in the
run: it is the document a reader trusts about who can write what. **Security-sensitive.** Yes.

**Commit.** `docs(security): the roadmap queue and its apply job (P9g)`

---

### P9f — The CLI's front-door client, and `fugaro coordinator roadmap` (D2122)

**Scope, to D2122 (A).** The transport the console spawns. `fugaro coordinator login --scope
roadmap:write` runs the authorization-code flow with PKCE that M5 serves, on a loopback redirect,
with the installation's front-door URL read from its outputs; the refresh token is kept in the
operating system's keychain and never in a file, and `fugaro coordinator logout` deletes it. The
`roadmap` group's writes are calls to P9d's tools with that token: `need` and `unneed`, and
`releases set`. The group is created here; `publish` (P9c) and GV3.1's `move` join it. Each prints the request id the front door
queued, then waits up to 60 seconds for its outcome (`roadmap_request`, P9e) and prints it; past
that it prints the id as `pending`, and `fugaro coordinator roadmap request <id>` reads it later; each refuses with one sentence when no token is held (naming `fugaro
coordinator login`) and when the front door is off.

**Interfaces.** The subcommands and their `--json` documents; a new §6 section of
`console/docs/json-contract.md` for the outcome document the console reads after a write.

**Files (at most 8).** A new roadmap.go and roadmap_test.go in `ee/coordinator/cli/`, a new login file
and its test there, `ee/coordinator/cli/coordinator.go` (the group's registration),
`console/docs/json-contract.md`, `website/content/cli-reference.md` (regenerated), and the
expectations in `ee/clidoc/` if the generator asks.

**Tests.** Against a fake front door: `TestLoginUsesPKCEAndALoopbackRedirect`,
`TestTheRefreshTokenNeverTouchesAFile`, `TestRoadmapNeedCallsTheToolAndReadsTheOutcome`,
`TestRoadmapRefusesWithoutATokenAndNamesLogin`, `TestReleasesSetRefusesAChangedIDOrStream`,
`TestRoadmapNeverWritesTheDatabaseItself` (the package holds no database writer), and the CLI
reference test.

**Red step.** `TestRoadmapNeverWritesTheDatabaseItself` fails against a command that calls P9b's
writer directly with the person's credential — the route D2110's answer refused. Mutation: write the
refresh token into the local config, and `TestTheRefreshTokenNeverTouchesAFile` must fail.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./ee/coordinator/cli/ -run
'Roadmap|Login' -count=1`; `go test ./ee/clidoc/ -run TestCliReferenceIsCurrent -count=1`.

**Depends on.** P9d (the tool names and arguments); D2122 (answered). It builds and tests against
a fake; it works against a real installation only once **P9e** serves the tools (and so once the MCP
plan's M12 has landed).

**Reviewer tier.** **SECURITY-SENSITIVE** — **opus + mandatory independent review**, level 3 in the
run. **Security-sensitive.** **Yes — a new credential the CLI holds, and the console's write path.**

**Commit.** `coordinator: the CLI's front-door login and the roadmap commands (P9f)`

## 6. The critical path, and the effort

```
P0 ──────────────────────────────────────┐
P1 ── P2 ── P5 ───────────────────────────┘
      │
P3 ── P4 ── P5
P1, P3 ── P6
P7(was blocked: D196, answered) ── P8(was blocked: D197, answered)
```

- **The critical path is P1 → P3 → P4 → P5.** P1 is the view; P3 is where it is kept; P4 is who
  writes it; P5 is what keeps it true. Everything a person can see before the front door exists
  is on this path.
- **P0 and P1 can start the day the owner says so**, and P1 is deliberately the part with no new
  decision, no store and no credential in it at all — which is why it is worth starting first.
  Since 2026-10-21 that is true of P2–P6 as well: the owner's answers cleared the gate for them
  (§11).
- **P7 and P8 no longer wait on another plan.** D196's answer of 2026-10-21 gave the coordinator
  **its own** service, so the chain is P7 → P8 inside this work and the critical path above is
  the whole of it: **no part of this plan is blocked on the central orchestrator plan's P4 or
  P5**, and the only thing the two plans share is the allow-list admission of
  `google_cloud_run_v2_service`, which whichever part lands first makes and the other cites.
- **Effort, in parts:** 9 parts; **5 SECURITY-SENSITIVE** (P2, P3, P5 partly, P7, P8 — §7 lists
  each and what an independent review must cover); **2 waiting on a review rather than on a
  decision** (P7 and P8, on the front-door design review — §11); **2 that need no new decision**
  (P0, P1).
- **The first cut a user can feel is P1 + P2 + P3 + P4 + P6**: a project view, every repository's
  pull requests in it, the intent records the orchestrators write, and a console screen that draws
  it. Nothing in it can block a merge, write to GitHub, or spend a cent on a model — which is the
  point (Q13 A), and it is the whole of v1 that this plan would ship without a service.

**Added 2026-11-11, revised the same day on the owner's answers: the roadmap record's parts, and the
console tasks they unblock.** With P9a–P9g the plan has **16 parts**, **13 of them SECURITY-SENSITIVE**
(P9b–P9g join §7's five; P9a is careful). Every write — a published plan, a move, an edge, a release
— goes through the front door's `roadmap:write` scope (D2110, D2111) and is written by the front
door's queue and applied by a non-public job as the coordinator's account (D2118), so the write
half waits on **the MCP plan's
M12, the front door mounted on a binary (D2119)**. The edges, from each part's
**Depends on**, with the console's two consumers (`docs/console-dependency-graph/implementation-plan.md`)
and the external predecessors beside them:

```
P3, P4 (built) ── P9a ── P9b ── P9d ── P9f ── P9c
                     O16a (stateless plan) ── P9d
        M12b ── M12 (MCP plan, D2119, D2126) ── P9e ── P9g ←── P9d
                                      P9f ── GV3.1 ── GV3.2        (live only once P9e serves)
                     P9a ── GV4.1 (with GV2.3; real data once P9c publishes through a served P9e)
```

| Level | Parts | Why this level |
| --- | --- | --- |
| R0 | P9a | the shape and the reader; needs only P3/P4, which are built |
| R1 | P9b, GV4.1 (console plan, with GV2.3) | the writers write P9a's shape; GV4.1 reads it, through `fugaro graph`, and builds against fixtures |
| R2 | P9d | the scope and the tools over P9b, dormant; after the stateless plan's O16a, which edits the same three files |
| R3 | P9f | the CLI's login and the `roadmap` group, against a fake front door |
| R4 | P9c, GV3.1 (console plan) | both join P9f's group; their command files are disjoint |
| blocked | P9e, then P9g | P9e waits on the MCP plan's M12, which waits on M12b (D2119, D2126); P9g documents what P9e builds; nothing written above them is served until P9e lands |

**The critical path is M12 (MCP plan) → P9e**, because every write is served only through it, and
M12 itself waits on M12b (D2126); inside this plan it is **P9a → P9b → P9d → P9f → P9c**, five parts, and the console's
write is **… → P9f → GV3.1 → GV3.2**. GV4.1 can be built at R1 and shows real data once a plan has
been published through a served door. No decision of these parts is open: D2127 (M12b's binding)
was answered number matching on 2026-11-11.

**Shared files** (every file more than one of these tasks writes):

| File | Writers (level) |
| --- | --- |
| `ee/coordinator/mcpauth/limits.go`, `ee/coordinator/mcpauth/authorize.go`, `ee/coordinator/mcpauth/tools.go` | the stateless plan's O16a, then P9d (R2) |
| the roadmap command file in `ee/coordinator/cli/` and its test | P9f (R3, creates), P9c (R4), GV3.1 (R4) — P9c and GV3.1 add files of their own and touch the group's file one after the other |
| `ee/coordinator/cli/coordinator.go` | P9f (R3) only |
| `internal/cli/plan_status.go` | P9c (R4: the moved plan-id rule); GV4.1 no longer writes it under D2117 |
| `console/docs/json-contract.md` | GV4.1 (R1: §6.22), P9f (R3: the outcome document), GV3.1 (R4: the move's `--json`) |
| `website/content/cli-reference.md` | P9f (R3), P9c (R4), GV3.1 (R4) — regenerated by each, one after the other |

## 7. Security-sensitive parts, and what an independent review must cover

| Part | Why | The independent review must confirm |
| --- | --- | --- |
| **P2** | a read-only GitHub credential, and its reach | the permission set holds no `write`; which function mints it; per repository or project-wide, per D193; the cache's key and lifetime; what a leaked token reads |
| **P3** | a rules change and a second project-wide write identity | the grant is a read and its extent; what the writer account can reach (`config/` included — IAM cannot stop it); the generation bump; the amended claim in `deploy/terraform/gcp/modules/firebase/iam.tf` |
| **P5** | the job runs as that write identity | what its code writes against what its account may write; its GitHub credential is P2's set; no unbounded retry |
| **P7** | the first request-serving type, and the front door's tenant | which types were added and which stayed refused; the typed confirmation still fires; the ID token's audience and its check; every invoker member is a person |
| **P8** | the only inbound request, carrying text somebody else wrote | what authenticates a caller; which tools write; the data boundary and its test; the override cap; no answer is markup |
| **P9b** | the writers into the subtree no client writes (added 2026-11-11) | the rules are unchanged and no client writes a roadmap node; the one writer is the coordinator's account through the apply job; the front door holds no database write (D922/H3); the move re-compares after a lost race; the prune's bound; the size bounds |
| **P9c** | files from a checkout sent to a write scope | the walk's confinement; only the artifact leaves the checkout; the provenance is never read back; the command holds no database writer |
| **P9d** | a write scope on a public authorization server | the vocabulary before and after; `roadmap:write` is not a default; only an operator holds it (D2121); what a paired phone's approval shows; a run or project token is not a front-door token; nothing is served |
| **P9e** | the path by which a request from outside reaches the database | the front door holds no database write; the queue bucket's two members and seven-day retention; the D935 store keeps its one member; the one `run.jobs.run` grant, without overrides; each request re-checked and applied at most once; what the apply job writes |
| **P9g** | the security document readers trust | it still says the public service holds only the database viewer; it describes the queue and the apply job exactly; the website page is regenerated, not edited |
| **P9f** | a credential the CLI now holds, and the console's write path | PKCE and a loopback redirect; the refresh token never in a file; the command holds no database writer |

An independent review means **a reviewer who did not write the change, on a different model (this
plan asks for Opus), with the diff in front of them and the part's own claim about what it does
not guarantee**. Each of those pull requests carries a paragraph saying what was reviewed and what
was not.

## 8. What each part's tests must do (the rules this plan holds itself to)

1. **Test-first, and mutation-proven.** Every red step above names the bug it is written for; the
   part plants it, sees the test fail, and removes it. A test that cannot be made to fail is not a
   test.
2. **`GOFLAGS=-p=2 go vet ./...`, `gofmt -l .`, and the tests of every package touched**, before
   the part is finished. Terraform changes add `terraform fmt -check -recursive` and
   `terraform validate`, with the golden plans **regenerated and read**.
3. **No secret-shaped literal, ever** — in a test, a fixture, a golden or a document.
4. **The rules are generated, not hand-edited**: `internal/budget/rules/rules.json.tmpl` plus a `RulesVersion` bump in
   `internal/projectpolicy/version.go`, with the goldens regenerated.
5. **Nothing under `.github/workflows/`** — if a part needs one, it goes to
   `docs/pending-workflows/` for the owner to copy.
6. **Targeted tests only, and docs pins stay green**: `go test ./internal/cli/ -run TestDocsCoordinator`
   and the register-pinning tests of the same package are run by every part that touches
   `docs/coordinator/`, because a citation in this folder that stops resolving is a claim nobody
   can check.

## 9. The owner-run live checks, proposed by name and not numbered

The register's own rule is that **the phase that ships the check writes it** — a sentence of
`docs/design/voice.md` §5, quoted by work after work and **not** in the register itself — so
this plan proposes its checks **by name** and takes **no number**. The reading, name by name,
against `docs/gcp-live-checklist.md` at `1e0953b7`:

**1–18 exist only as summary rows; 19–47 are written** as `## Check N` sections — **with one
number missing from that run: 39**, which is reserved by the retire-image-mirror plan's Task 7
live checks (39a–39f) and has **no section and no row at all**, because the registry-remote
repository plan dropped 39b and 39f and amended 39a; the written sections run 19–38 and 40–47.
**48–56** are the
project-centric plan's, **57** the compute backend's (its section lives in
`docs/design/compute-backend.md`), **58** the artifact-runs work's, **59a/59b** the desktop
console's, **60 and 61 owner-allocated** (the budget-token channel and the monorepo folder
export), **62–63** the console v2 pair's, **64–66** this repository's voice work (64 written, 65
and 66 held), **67–68** managed projects', **69a–69c** and **70a–70c** the website and the website
redesign's, **71–79** inside the website work's held range with no check written in them, and
**80–89** the mobile app's. Above those: **90–99 are the credentials service's** (none written;
93 reserved and unused), **100–109 are the mobile functional spec's** (100–104 approved
2026-10-18 and written by its P6.11, 105–109 reserved and unused), **110 is claimed twice and
written by nobody** — by the voice evaluation switch's design (its P5.3, named and never written)
and by the always-on orchestrator design in **open PR #632** — and the register's rule is that
*whichever of two open claims merges second renumbers itself*. **111 is written**, by the setup
wizard (its Task W0.3, whose rewritten Check 27 is its end-to-end pass). **112 is proposed** by
`docs/windows-cli/`, **113–118 are taken** by `docs/mobile/voice-session/` (its own README works
the 112/113 collision out and yields 112), and **119 is taken** by the rigor modes work in **open
PR #773**, which is not on `main` and whose claim this folder can therefore only record and not
read. **So the next free number depends on how 110 and 119 settle, and this plan takes none.**
Each check below is **owner-run, on a real project, by the owner** unless it is marked
**runner**; there is no live action of any kind in this work.

| Proposed check, by name | Part | Kind | What it proves |
| --- | --- | --- | --- |
| the project view against a real project's pull requests | P1, P2 | owner | the join is right about real data, and every repository is reached |
| the read-only token really is read-only | P2 | owner | Q3(A)'s hard rule: a write through the coordinator's own token is refused |
| one repository per token, and the cache's lifetime | P2 | owner | D193's answer on the real API, and U3 if the owner takes the project-wide token |
| the read grant as a signed-in reader, and as a run | P3 | owner | Q15(A)'s grant on the deployed rules, and finding #25's fence |
| a report survives the job, and the view shows it | P4, P5 | owner | the write path end to end, on the real database clock |
| the sweep against GitHub's rate limits | P5 | owner | that a tick's read volume is affordable at the spec's scale |
| the sweep when GitHub is unreachable | P5 | owner | the stale marker, and that the previous view stays |
| the coordinator opt-in, and one sweep end to end | P5 | owner | `fugaro update --firebase-project --coordinator` creates exactly the account, grants, job and schedule P5's **For operators** names and nothing on an installation that did not opt in; one manual `fugaro coordinator sweep` tick (the job's own execution) writes each repository's view, records a refused pull request under `unread` without staling the repository, moves `coordinator/project/sweep_cursor`, and stays inside its read budget |
| the first service deploys and scales to zero | P7 | owner | the type is admitted, a service runs, and an idle service costs nothing |
| an ID token from the CLI is accepted, and one for another project is not | P7 | owner | Q2(A)'s credential on the real platform |
| a per-person invoker grant, and its removal | P7 | owner | offboarding is the launcher-list edit finding #5 describes |
| a non-Claude MCP client against the front door | P8 | owner | Q16(A)'s departure, from the outside, by a client this repository does not ship |
| the console's project view with the CSP intact | P6 | **runner** | the console suite, not a live check |
| a roadmap write through the front door, and the identities it refuses | P9d, P9e | owner | an operator's `roadmap:write` move is queued, applied by the apply job within its start time and reported back; a launcher cannot obtain the scope; a signed-in reader's and a run's direct database writes are refused; the front door's account has no database write (added 2026-11-11) |
| a plan published from a checkout of a second repository | P9c, P9e | owner | the plan document carries that repository as provenance, a re-publish writes nothing, and a same-id plan from another repository is refused without `--replace-source` (added 2026-11-11) |

## 10. The numbers this plan would hold

- **Decision numbers: D193–D197**, and they are new. The reconciliation's Q1–Q16 took none — an
  answer picks an option and allocates nothing — and this plan's five findings do not fit that
  shape: they are decisions, not questions, and the owner's register records **D193 onward as
  free** ([README.md](README.md)). They are taken from that block rather than from a folder-local
  one, deliberately: this folder holds the register that lists every block, four other folders
  have taken a folder-local `D1`, and a second `D1` here would be a fifth meaning for one number.
  Each is written in [reconciliation.md](reconciliation.md) §6, **recommendation first**, with the
  alternative and its cost, and each carries **the owner's answer of 2026-10-21** under it —
  four as recommended and **D196 against the recommendation**, which is what moved this work from
  admitting a type and minting a token to **building the service itself** (P7).
- **Decision numbers, added 2026-11-11: D2110–D2129** are reserved for the roadmap record's parts
  (P9a–P9f), above **D2109**, the highest written or reserved on `main` and in the open pull
  requests that day. **D2110–D2117** are written in §5 (the tables before P9a) and **answered by the
  owner on 2026-11-11** — D2110 (C) and D2113 against the recommendation, D2111 changed the same day
  to the push model, the other five as recommended; **D2118–D2123** are written there and **answered
  the same day** — D2118 first (B), then back to the recommendation (A) under D2124 — and
  **D2124–D2126** likewise, D2124 by returning to D2118's recommendation and D2125 and D2126 as
  recommended; **D2127** is written there and **answered** the same day (number matching, the
  recommendation); **D2128–D2129** are reserved and unused.
- **Check numbers: none claimed**, for the reason in §9. The part that ships a check takes the
  number free at its own merge, which is the register's rule and not this plan's.
- **No Go code exists in this work yet.** The only Go it has added is the folder's own guard —
  three new tests and two rewritten pins in `internal/cli/docs_coordinator_test.go` — and that
  landed **with this plan rather than after it**, because the pins holding the index's status
  line and its numbers table cannot be left red while the plan is in review. P0 is written into
  this document anyway, because a plan whose parts do not account for the work the plan itself
  needed is a plan that starts with a lie of omission.

## 11. The owner approval gate

**Passed 2026-10-21 for P0–P6.** The owner answered all five of §4's decisions — four as
recommended and **D196 the other way** — and named the branch and the release, so the parts that
needed only those answers may start. **P7 and P8 still wait**, and on a review rather than on a
decision: the front-door design review, because D196(B) is what makes this work the author of a
request-serving service, and that design has not been reviewed by anyone but its writer. **U2 and
U3 remain owner checks** and are not answered by anything here: U2 is whether the GitHub App
already holds `pull_requests: read` (which P2's credential needs), and U3 is whether an
installation token may omit `repositories` (which is what the project-wide token D193 leaves
aside waits on).

What follows is the gate as it was written before the owner passed it, kept in place so the
answer can be read against what it was answering.

**No part of this plan starts before the owner passes this gate.** Passing it means, at minimum:

1. **Answering D193–D197**, or explicitly deferring the parts they block (P2, P3, P4, P5, P7, P8).
   The plan is written to the recommendations, and every part names the decision it would be
   rewritten for.
2. **Ruling on D196 explicitly**, because it is the one decision that allocates work *between two
   documents*: it says whether the resident service the central orchestrator plan builds is the
   coordinator's front door, or whether this work builds a second one. The two plans cannot both
   build it, and shipping either silently decides the other's question — the same shape as the
   collision the central orchestrator plan records (its D20).
3. **Saying what the coordinator may hold** (D193, D194): a read-only token per repository and a
   database-wide write identity is the recommendation, and both are statements about a compromise
   rather than about convenience.
4. **Confirming that v1 is the credential-free half** (Q4 A, Q11 A): P1–P6 ship no model, no cap
   and no spend, and the model half waits for `docs/credentials-service/`'s first slice — which is
   this plan's recommendation and the reason §3 is short.
5. **Naming the release and the branch**: this plan recommends **0.11.0** on the long-lived
   **`coordinator`** branch, and says no part assumes a release that has not shipped.

**What this gate is not.** It is not an approval of any part: approving the plan answers §4's
decisions, and each part is still reviewed on its own — five of them by an independent reviewer on
a different model. It is not a promise about ordering beyond §2's five dependencies. And it is not
a substitute for the register's own rules about numbers: a decision number, once allocated, is
never reused or renumbered, and a check number is taken by the phase that writes it.
