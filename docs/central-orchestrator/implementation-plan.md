# Central orchestrators — the implementation plan

**Status: plan only; no part is approved and no code is implemented.** This plan follows from
[reconciliation.md](reconciliation.md), which wins wherever it disagrees with
[spec.md](spec.md). Every part below is **independently mergeable**: it lands, it is verified,
and the repository works with it and without the parts after it. Every part cites **paths and
symbols**, never line numbers. **Owner: Dimi Paun. Date: 2026-10-20. Base: `main` at
`2f8b4dca`.**

**The plan is written against decisions D1–D26, and every one of them is open.** The parts are
therefore written to the **recommendation** in each decision, and each part names the decision
it would have to be rewritten for. **No part starts before its decisions are answered** — the
gate is at the end of this document.

Reading the register for this plan: the D-numbers are folder-local (**D23**), and the
owner-run live checks are **proposed by name and not numbered** (**D24**) — the reasoning, with
the register read name by name, is §7 of [reconciliation.md](reconciliation.md) and §8 below.

## 1. The order, and why it is this order

The order is forced by four dependencies, and nothing else in the plan is arbitrary:

1. **Nothing can deploy a service until the resource type is admitted.** `internal/infra/tf/cover.go`'s
   `allowedTypes` admits `google_cloud_run_v2_job` and `google_cloud_run_v2_job_iam_member` only,
   and `Cover.NotCovered` refuses everything else (reconciliation #1). That is **P1**, and it is
   first because every later part stands on it.
2. **Nothing can be authorised until the access record exists** — and the record has to decide
   what the allowlist *is* (a device node with an email field, per D6), because the phone's
   allowlist and the orchestrator's are the same one in the spec's own words. That is **P2**.
3. **Nothing can be handed over until the lease exists**, and the lease is the one part of this
   work that is pure protocol with no cloud credential in it — so it is buildable and
   verifiable entirely locally and in the emulator. That is **P3**, and it comes before the
   service on purpose: the service is the *consumer* of the lease, and building the consumer
   first would mean building the invariant twice.
4. **The credential questions come last**, because their answer is not this work's: the
   security service owns them (`docs/credentials-service/`, plan-only) and mobile D115 defers
   them. So the parts that need a model credential in the cloud (P8) sit behind a gate, and
   v1 is the credential-free half (D16).

**What is deliberately not in this plan** (D16, D18, D21, D25, and the spec's own words):
merging from the cloud; a pre-built hotfix (*"Out of scope for v1"*); archiving the session to
Git; push notifications; a *per-fix* secret narrowing (not expressible — reconciliation #46);
more than one orchestrator per installation in v1; and any part of the coordinator other than
the reporting path (D14).

## 2. The parts

Every part has the same shape: **Scope**, **Files** (the paths and symbols it touches),
**Red step** (the failing test written first), **Verification**, **Depends on**, **Reviewer
tier**, **Security-sensitive**, **Commit**.

Reviewer tiers are this plan's own vocabulary: **standard** (one reviewer, the usual path),
**careful** (a reviewer who reads the migration and the rules), and **SECURITY-SENSITIVE** (an
**independent review by a second reviewer who did not write it, on a different model** — the
plan asks for Opus — and its own paragraph in the pull request body saying what was reviewed
and what was not).

---

### P0 — Pin this folder, and pin the citations

**Scope.** Every other plan folder in this tree pins itself with a guard
(`internal/cli/docs_coordinator_test.go`, `internal/cli/docs_rigor_modes_test.go`,
`internal/cli/docs_windows_cli_test.go`). This folder has none, because the pull request that
wrote it was docs-only (reconciliation C1). P0 is that guard.

**Files.** New `internal/cli/docs_central_orchestrator_test.go`, whose tests hold: (a)
`docs/central-orchestrator/spec.md` byte-for-byte against the owner's text, the way the
coordinator's guard holds its own spec; (b) the README's status line carrying *"plan only; no
code is implemented"*; (c) the README's numbers table carrying the D-block it claims and the
statement that no check number is claimed; (d) every citation in `reconciliation.md` that
names a file in this repository anchored by **content** — the phrase the citation turns on,
the convention `internal/cli/docs_coordinator_test.go`'s own comment describes — with the
citation that names a document only in an open pull request (PR #632's
`docs/mobile/design/always-on-orchestrator.md`, PR #773's `docs/rigor-modes/`) excused **by
name, with its reason**, in the test itself.

**Red step.** Write the content anchors, then rename the symbol one of them names (for
example `takeClaim` in `internal/cli/run.go`) in a scratch tree: the test must fail, naming
the finding it could not anchor. Restore. **Second red step:** change one byte of `spec.md`
and see the verbatim test fail.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./internal/cli/ -run
TestDocsCentralOrchestrator`.

**Depends on.** Nothing. This is the only part that depends on nothing.

**Reviewer tier.** Standard. **Security-sensitive.** No.

**Commit.** `cli: pin the central-orchestrator documents and their content anchors`

---

### P1 — Admit the Cloud Run service type, deliberately

**Scope.** D19. Add `google_cloud_run_v2_service` (and the IAM member the design needs, if it
is not already covered) to the installation's resource allow-list, so `fugaro init` admits a
plan holding one. This is a code change to a list that exists to make such additions
deliberate: `internal/infra/tf/cover.go`'s `allowedTypes`, `internal/infra/tf/managed_cover.go`'s
`managedTypes`, and — for the managed path — the cost tables in
`internal/infra/tf/managed_cost.go` that already carry rows for the service type. The typed
confirmation path (`internal/cli/init_review.go`'s `notCovered`, and `fugaro init`'s
confirmation matrix) is where the owner's deliberate step lives; the part adds the type to
the **refusal's** allow-list and to nothing else. **No Terraform module in this part**: the
module that creates a service belongs to P4.

**Files.** `internal/infra/tf/cover.go` (`allowedTypes`), `internal/infra/tf/managed_cover.go`
(`managedTypes`), `internal/cli/init_review.go` (`notCovered`) and the goldens its tests read
(`testdata/` plans under `internal/infra/tf/` and `internal/cli/`).

**Red step.** Add a test that a plan holding a `google_cloud_run_v2_service` is **admitted**
(it fails today, because the type is refused), and a second that a plan holding an unrelated
unknown type is **still refused** (it passes today, and must keep passing).

**Verification.** `terraform fmt -check -recursive deploy/terraform/`; `terraform validate` in
each module that has a golden; `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test
./internal/infra/tf/... ./internal/cli/...`; and the golden plans **regenerated** and read
diff by diff, not accepted wholesale.

**Depends on.** D19's answer. Reviewable on its own before P2.

**Reviewer tier.** **Careful.** **Security-sensitive.** **Yes — the allow-list is an
infrastructure blast-radius control, and widening it is the kind of change that should be
reviewed by someone who did not write it.** The pull request body must say which types were
added, which stayed refused, and that the typed confirmation still fires.

**Commit.** `infra: admit google_cloud_run_v2_service to the installation's plan allow-list`

---

### P2 — Identity, the access record, and the management commands

**Scope.** D6, D12. The project gains an **orchestrator access record** and the commands that
write it. The record is the authorisation the service checks on every connection and every
submission: the **device node's** shape extended with the email and the policy fields
(recommendation D6), not a new email list. `fugaro orchestrator add|rm|ls` are project-level
commands in the shape of the existing project-level commands (`internal/cli/budget.go`'s
`budget set`, `internal/projectpolicy/`), writing the project policy through the same publish
path, with `add` refused unless it can name the identity, the allowlist and the cap. **No
`attach` in this part** — that is P5 and waits on D5.

**Files.** `internal/projectpolicy/` (the policy schema and its publish path, with the new
orchestrator block), `internal/cli/` (a new `orchestrator.go` with `newOrchestratorCmd`, plus
its registration in `internal/cli/root.go`), `schemas/fugaro.schema.json` (the policy's
orchestrator fields), `deploy/terraform/gcp/modules/firebase/` (nothing new: the access record
lives in the database the module already creates), and the RTDB rules
(`internal/budget/rules/rules.go` and its `rules.json.tmpl` template, with a `RulesVersion`
bump).

**Red step.** First: a rules test that reads `config/admin/devices/<uid>` with an email field
absent and asserts the **documented** refusal; then a CLI test that `fugaro orchestrator add`
refuses a project with no policy, and one that `ls` prints the record. Both fail before the
change.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./internal/cli/...
./internal/projectpolicy/... ./internal/budget/rules/...`; the rules goldens regenerated; and
`fugaro project policy publish --dry-run` against a fixture policy.

**Depends on.** D6, D12, D23. P1 not required (nothing deploys here).

**Reviewer tier.** **Careful.** **Security-sensitive.** **Yes — this is the authorisation
record, and a rules mistake here is a project-wide grant.** Independent review required, and
the pull request body must state the rule's shape, the generation bump, and what a uid with
no device node gets.

**Commit.** `cli: the orchestrator access record and fugaro orchestrator add|rm|ls`

---

### P3 — The lease, takeover, and what a local orchestrator sees

**Scope.** D1, D2, D3, D15. The ownership record, its takeover protocol and the way the local
side learns it lost the seat. Per the recommendations: the record lives in the **Realtime
Database** under an `orchestrators/<slug>/<job>/lease` subtree, written with
`internal/rtdb/client.go`'s `GetETag` + `PutIfMatch` (there is no transaction — reconciliation
#8); expiry is judged on the **database's clock** (`ServerNow`); a takeover is a
compare-and-set on (`generation`, `expires_at`) and the winner increments `generation`; the
holder is a **named identity**, never `host/pid/rand`; heartbeat 60 s, grace 10 minutes. The
local side sees the change in the stream `fugaro watch` already carries
(`internal/watch/source.go`'s `Supervisor.paths()`), and a return is an **explicit** takeover
through the same compare-and-set.

**Files.** New `internal/lease/` (the record, `Take`/`Renew`/`Release`/`Holder`, a clock
interface fed by `internal/rtdb`'s `ServerNow`), `internal/rtdb/client.go` (no new method
expected beyond what `GetETag`/`PutIfMatch`/`Patch` give), `internal/watch/source.go` and
`internal/watch/build.go` (the lease in the existing stream), `internal/cli/watch.go` (its
rendering), the rules template and `RulesVersion`, and the CLI's `fugaro run` path where a
launch takes the lease (`internal/cli/run.go`'s `launchRun`, beside the existing
`takeClaim`).

**Red step.** A test that two takers race for one lease and exactly one wins; a test that a
stale holder's renew is **refused** (the generation moved); a test that the clock used is the
database's by feeding a fake `ServerNow` that disagrees with the wall clock by an hour, and
asserting the outcome does not change. All three fail before the code exists.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./internal/lease/...
./internal/rtdb/... ./internal/watch/... ./internal/cli/...`; the rules goldens regenerated;
and an emulator run of the race (see U3 in the reconciliation — the rate this protocol implies
is unverified on the real database, and the emulator is where the race is made deterministic).

**Depends on.** D1, D2, D3, D15.

**Reviewer tier.** **SECURITY-SENSITIVE.** **Yes — this is the one-job-one-shepherd invariant,
and a wrong compare-and-set is two orchestrators acting on one job.** Independent review
required; the body must state the CAS, what happens on a lost race, and what the old holder's
next action gets.

**Commit.** `lease: one job, one shepherd, in the Realtime Database`

---

### P4 — The resident service: consume and launch, and nothing else

**Scope.** D13, D19, D20. The service itself — and it is deliberately the **small** one: it
consumes what it is told to do and it launches Cloud Run jobs, holding **no model credential**
(recommendation D20, and PR #632's §4.2 shape). One service per installation (`D19`),
`min-instances = 0`, request-based billing, woken by an event. The agent stays in a job. The
Terraform that creates it lives beside the installation's other modules, with the service's
own service account and the launcher grants the existing `google_cloud_run_v2_job_iam_member`
grants already express (`deploy/terraform/gcp/modules/project/iam.tf`).

**Files.** A new `deploy/terraform/gcp/modules/orchestrator/` (the service, its service
account, its IAM, its scheduler or webhook trigger), wired into
`deploy/terraform/gcp/modules/installation/`; a new Go entry point under `cmd/` and its
package (the consumer and launcher); `internal/runstore/launch.go` and `internal/cli/run.go`'s
launch path reused as the library a service launch calls, not re-implemented; the golden
plans; `internal/infra/tf/managed_cost.go` if a cost row is missing.

**Red step.** The service's own test: given a request, it **launches a job and nothing else** —
no model call, no PR write, no merge — asserted by the service account's grants in the
generated plan (the test reads the plan, not the code) and by the service's own dependency
list. It fails before the module exists.

**Verification.** `terraform fmt -check -recursive deploy/terraform/`; `terraform validate`;
goldens regenerated and read; `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./cmd/...
./internal/...`; and the plan's diff showing no credential material in the service's
environment.

**Depends on.** P1 (the type), P3 (the lease it consumes).

**Reviewer tier.** **SECURITY-SENSITIVE.** **Yes — a new, internet-reachable service with a
launcher's grants is the largest new attack surface in this work.** Independent review
required; the body must state the service account's grants, what it can reach, and the
evidence that no model credential is in its environment or its secrets.

**Commit.** `orchestrator: the resident service consumes and launches, and holds no model credential`

---

### P5 — The WebSocket protocol, and `fugaro orchestrator attach`

**Scope.** D5, D12. One protocol, three clients, authenticated by a Firebase Auth ID token at
connection **and** re-checked on every submission, against the access record of P2. The CLI
gets `fugaro orchestrator attach`; the console reaches the service **through the CLI it
already spawns** (`console/src/main/spawn.ts`'s `SPAWN_COMMANDS`), because its renderer's CSP
is `connect-src 'none'` and that is load-bearing (reconciliation #29).

**Files.** A new package (the protocol's frames, its version, its handshake) and the service's
handler (P4); the token verification, reusing the Identity Platform path
`internal/infra/identityplatform.go` already establishes and **not** inventing a second one;
the access check against P2's record with its own red test for "valid token, no device node";
`internal/cli/`'s `attach` command and its registration in `internal/cli/root.go`; the
console's `console/src/main/ipc.ts` (`CONSOLE_OPERATIONS`, `channelForOperation`,
`ALLOWED_OPERATION_FLAGS`) and `console/src/preload/index.ts` (`EXPOSED_OPERATIONS`) for the
new operation, plus its `console/test/unit/window.test.ts` mention of the CSP left
**unchanged**.

**Red step.** A handshake test with a valid token whose uid has **no device node** — refused,
naming the record; a test with a token minted for another project — refused; a test that a
submission re-checks the token rather than trusting the connection (mutate the token after
connect; the submission fails). All three fail before the code exists.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test` for the new package,
`./internal/cli/...`, and the console's own suite (`npm test` in `console/`, whose
`window.test.ts` must still see `connect-src 'none'`); `gofmt` and `tsc` clean.

**Depends on.** P2 (the access record), P4 (the service).

**Reviewer tier.** **SECURITY-SENSITIVE.** **Yes — it is the only network-facing protocol in
this work and it is where "who may speak" is decided.** Independent review required; the body
must state the authentication at connect and at submission, what an unverified or
allowlist-less identity gets, and why the console's CSP did not change.

**Commit.** `orchestrator: the shared session protocol, and fugaro orchestrator attach`

---

### P6 — The shared session, its memory, and the audit trail

**Scope.** D7, D11, D26. One shared session per central orchestrator; messages stored in
Firestore, reachable by the service account only (`internal/infra/firestore.go`'s
`FirestoreRules` is `allow read, write: if false`); a compacted summary that survives a cold
start; presence derived from the connection and not stored; and an **audit trail** — every
message and command carrying its sender, per the spec's Multi-user access section. The
expiry field and the `ttl_config` resource are **part of this part**, written per D11: expiry
is written **only once the message is archived**, and the TTL policy is the backstop, because
a TTL policy deletes on time and cannot express a condition (reconciliation #49). **The Git
archive itself is not in this part** (D25: a stated non-goal for v1), so v1 writes no expiry
and the TTL policy ships with the archive part or not at all.

**Files.** New `internal/session/` (message records, the compacted summary, the ordering
rule — *"no hard locks … each tagged with its sender"*), an extension of
`internal/firestore/client.go` if a needed operation is missing (**not** a transaction: there
is none), the service's session handler (P4/P5), Terraform
(`deploy/terraform/gcp/modules/firebase/` for the Firestore database and field TTL, if D11
says yes), and the CLI/console rendering of presence.

**Red step.** A test that a cold start reloads the compacted summary plus recent messages and
answers a new turn with the same context (a fake Firestore that fails the test if any message
is dropped); a test that a client that **disconnects** loses presence and its messages remain;
a test that an unarchived message carries **no** expiry (this is the mutation-proving test for
D11's rule).

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./internal/session/...
./internal/firestore/...`; `terraform fmt -check -recursive` and `validate` if Terraform
changed, goldens regenerated.

**Depends on.** P5.

**Reviewer tier.** **Careful.** **Security-sensitive.** **Yes, in one respect:** the session
holds conversation content and an audit trail, so the read path must be the service account's
alone. Independent review required for the storage and the retention fields.

**Commit.** `session: the shared session, its memory and its audit trail`

---

### P7 — Prompts, claiming, escalation and the callback contract

**Scope.** D8, D9. A prompt tied to a job; routing by submitter (a person's job to that person
first; an automated job to its callback, or straight to humans with none); claiming by
compare-and-set, first answer wins, everyone else sees who answered; escalation on the
mode-keyed timeout table (5/15/60 minutes), stored **here** and read through the project's
policy; the signed callback contract (HMAC-SHA-256 over the mailbox's canonical bytes —
`shared/mailbox-schema/mac.js`'s `macOf`, the tree's one signature convention — with a
`prompt_id`, a timestamp and a replay window); a short callback timeout, after which the
prompt escalates to humans; and the mailbox vocabulary reused rather than reinvented
(`internal/mailbox/message.go`'s `Kinds`, `Action`, `Confirmation`, `ValidateMessage`).
**Notifications in this part are the session marker and nothing else** — push and desktop
notifications are D21 and are not v1.

**Files.** New `internal/prompt/` (the prompt, its claim CAS, its escalation clock, its
routing rule), the mode-keyed table in `internal/projectpolicy/`'s orchestrator block and
`schemas/fugaro.schema.json`, the callback sender and its verifier, the service's HTTP
endpoint for answers (P4), the session writer (P6) for the record of who answered, and the
CLI's rendering.

**Red step.** A test that two claimants race and exactly one wins; a test that a callback
whose tag is **one byte wrong** is refused and the prompt escalates to humans; a test that a
replayed `(prompt_id, timestamp)` inside the window is refused; a test that a job whose
submitter is automated and has no callback escalates **immediately** (not after the timeout).
All fail before the code exists.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./internal/prompt/...
./internal/mailbox/... ./internal/cli/...`; and a fixture-driven test of the mode table, one
case per mode name, so a mode added later cannot silently get the default.

**Depends on.** P2 (the policy), P6 (the session it records into).

**Reviewer tier.** **SECURITY-SENSITIVE.** **Yes — the callback verifier is a signature check
on an inbound request, and the escalation table decides who may act on a job.** Independent
review required; the body must state the tag's construction, the replay window, and what a
timed-out prompt does.

**Commit.** `prompt: routing, claiming, the mode-keyed escalation table and signed callbacks`

---

### P8 — Alerts: ingest, de-duplication, the automated budget and the breaker

**Scope.** D10, and the spec's Alert storm protection and Prompt injection safeguards.
Sentry's webhook into the service's HTTP endpoint, verified with the provider's signing secret
before **any** job is created; grouping by issue fingerprint so new events attach to an open
job; a per-project **automated** hourly allowance and daily cap in new `config/caps/automated`
nodes (not `RepoCaps` — reconciliation #18, #42); the breaker as a stored state
(`config/breaker/<slug>`) that only a human clears, in the shape
`internal/budget/kill.go`'s `SetKill` already uses, with **one** summary notification when it
opens; and the **injection boundary**: alert content is passed to the job as data under a
documented convention written where the prompt is built (`internal/runner/prompts.go`'s
`SystemPrompt`), never as instructions; the job's toolset narrowed at the level
`internal/agent/settings.go`'s `WriteManagedSettings` already writes; and **no merge, in any
mode** (D18).

**Files.** The webhook receiver and its signature check; `internal/budget/model.go`
(`Caps`/`Path*` constants) and the rules template for the automated nodes; a new
`internal/breaker/` or an extension of `internal/budget/kill.go`'s switch pattern;
`internal/runner/prompts.go` (the data-boundary convention) and the alert job's prompt
builder; `internal/agent/settings.go` (the narrowed writes); the profile the alert job runs
under (documented, not code — and **not** a per-fix secret narrowing, which is not expressible,
reconciliation #46).

**Red step.** A webhook with a bad signature creates **nothing**; two events with one
fingerprint start **one** job; the 11th launch in an hour trips the breaker and produces
**one** summary; a launch while the breaker is open is refused until a human clears it; and a
mutation-proving test for the injection boundary — put an instruction-shaped string in the
alert body and assert it lands in the job's prompt as **data**, not as a system instruction.

**Verification.** `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`; `go test ./internal/...` for the
touched packages; the rules goldens regenerated; and the Terraform checks if the alert job's
profile adds infrastructure.

**Depends on.** P4, P7, **and the credential question** — which is why it is last:

**⚠️ Gate.** P8 starts only when the owner has answered **D4** and **D16**, and only after the
credentials service's first slice exists (`docs/credentials-service/`, plan-only today) or the
owner rules that an alert job may run with no destination credential in v1. Until then P8 is
**proposed and blocked**, and its own entry in the reconciliation is #21.

**Reviewer tier.** **SECURITY-SENSITIVE.** **Yes — this is the component that lets an
unauthenticated third party (a provider webhook, carrying attacker-controlled text) cause
code execution in the installation.** Independent review by a second reviewer on a different
model is **mandatory**, and the pull request body must state: the signature check and what it
covers, the fingerprint key, the caps, the breaker's clearance, and an explicit list of what
the alert job still can reach (the account is the blast radius — `docs/project-claude-accounts.md`
§10).

**Commit.** `alerts: verified ingest, de-duplication, the automated budget and the breaker`

---

### P9 — The other two clients, and the path to the coordinator

**Scope.** D5, D14, D21. The console joins through its own CLI-mediated operation (P5) — a
**separate, small part** because the console has its own suite, its own review and its own
CSP test. The phone joins only when its own work allows a network client (reconciliation #28:
`mobile/src/demo/safety.test.js` fences `WebSocket` outright), so v1's phone path is **the
existing mailbox and feed, unchanged**. And the orchestrator **reports** to the coordinator
(D14): what it launched, what prompts are open, and who holds which lease — a writer of the
project-wide record, taking nothing back, defined in the coordinator's documents rather than
invented here.

**Files.** `console/src/main/ipc.ts`, `console/src/preload/index.ts`, `console/src/source/`
(a new source beside `console/src/source/cli.ts`'s `CliSource`, or an extension of it), the
console's tests; the phone's documents (a **note**, not an edit — the phone's port design is
`mobile/src/feed/index.js`'s and it stays); the reporter (a new small writer) into whatever
the coordinator's plan defines.

**Red step.** `console/test/unit/window.test.ts` still asserts `connect-src 'none'` after the
change (this is the mutation-proving test: widen the CSP and the suite must fail); a test that
the reporter writes the project record and **reads nothing back** into a launch decision.

**Verification.** `npm test` and `npm run typecheck` in `console/`; `node --test` in `mobile/`
(whose safety test must still pass **unchanged**); `GOFLAGS=-p=2 go vet ./...`; `gofmt -l .`.

**Depends on.** P5 (the protocol), and for the coordinator half, the coordinator's own plan —
which does not exist. **Half of this part is therefore blocked** (reconciliation #51).

**Reviewer tier.** Standard for the console; **careful** for the reporter. **Security-sensitive.**
No — the console half adds no network surface (it adds an IPC operation beside an existing
allow-list), and the reporter writes only.

**Commit.** `console: the orchestrator session, and the orchestrator's report to the coordinator`

## 3. The critical path, and the effort

```
P0 ──────────────────────────────────┐
P1 ── P4 ── P5 ── P6 ── P7 ── P8(blocked)
      │                        │
P2 ───┴────────────────────────┘
P3 ── P4
                     P5 ── P9(blocked, half)
```

- **The critical path is P1 → P4 → P5 → P6 → P7 → P8.** P3 can land in parallel with P1/P2 and
  is the part with no cloud credential in it at all — which is why it is worth starting first
  whatever the owner answers about credentials.
- **P0 can land immediately** and needs no decision but its own.
- **Effort, in parts:** 10 parts; **2** blocked (P8 on D4/the credentials service, half of P9
  on the coordinator's plan); **8 SECURITY-SENSITIVE** (P1–P8; only P0 and P9 are not —
  §4 lists all eight and what each independent review must cover); **1** that needs no
  decision at all (P0).
- **The first cut a user can feel** is P1 + P2 + P3 + P4 + P5: a configured project, an
  access record, a lease, a service that launches jobs, and a CLI that can attach. Everything
  it can do is something `fugaro run` can already do today — which is the point: no new
  credential, no merge, and the invariant proved before anything is automated.

## 4. Security-sensitive parts, and what an independent review must cover

| Part | Why | The independent review must confirm |
| --- | --- | --- |
| **P1** | widening an infrastructure allow-list | which types were added and which stayed refused; the typed confirmation still fires; goldens read |
| **P2** | the authorisation record and its rules | the rule's shape; the generation bump; what a uid with no device node gets |
| **P3** | one job, one shepherd | the compare-and-set; a lost race; what a stale holder's next action gets; the clock's source |
| **P4** | a new internet-reachable service with launcher grants | the service account's grants; no model credential in the environment or secrets |
| **P5** | the only network-facing protocol | auth at connect **and** at submission; the refusal paths; the console's CSP unchanged |
| **P6** | conversation content and an audit trail | the read path is the service account's alone; the retention fields |
| **P7** | an inbound signature check and who may act | the tag's construction; the replay window; the timeout's consequence |
| **P8** | attacker-controlled text causing code execution | the webhook's signature; the fingerprint; the caps and the breaker; what the job still reaches |

An independent review means **a reviewer who did not write the change, on a different model
(this plan asks for Opus), with the diff in front of them and the part's own claim about what
it does not guarantee**. Each of those pull requests carries a paragraph saying what was
reviewed and what was not.

## 5. What each part's tests must do (the rules this plan holds itself to)

1. **Test-first, and mutation-proven.** Every test above names the bug it is written for; the
   part plants it, sees the test fail, and removes it. A test that cannot be made to fail is
   not a test.
2. **`GOFLAGS=-p=2 go vet ./...`, `gofmt -l .`, and the tests of every package touched**,
   before the part is finished. Terraform changes add `terraform fmt -check -recursive` and
   `terraform validate`, with the golden plans regenerated **and read**.
3. **No secret-shaped literal, ever** — in a test, a fixture, a golden or a document. The
   rules' own tests use shapes (`internal/budget/rules/rules.go`'s `MACOK` *"checks a tag's
   shape, never its value"*).
4. **The rules are generated, not hand-edited**: `rules.json.tmpl` plus a `RulesVersion` bump,
   with the goldens regenerated.
5. **Nothing under `.github/workflows/`** — if a part needs one, it goes to
   `docs/pending-workflows/` for the owner to copy.

## 6. What the owner must answer before any part starts

The plan is written to the recommendations; each part names the decision it would be rewritten
for. The decisions that **block** parts outright, and the part each blocks:

| Decision | Blocks | The recommendation the plan is written to |
| --- | --- | --- |
| **D19** | P1, P4 | per installation, one service in v1; add the type deliberately |
| **D6** | P2, P5 | device node + administered email; domain rule at administration |
| **D12** | P2, P5 | `fugaro orchestrator add|rm|ls` project-level; `attach` after D5 |
| **D1, D2, D3** | P3, P4 | RTDB, generation CAS, `ServerNow`, 60 s / 10 min |
| **D13, D20** | P4 | `min-instances = 0`; the agent in a job, no model credential in the service |
| **D5** | P5, P9 | one protocol; ID token at connect and per submission; console through the CLI |
| **D7, D11, D26** | P6 | one session; Firestore, service-account only; presence from the connection |
| **D8, D9** | P7 | routing by submitter; the mode table lives here; HMAC over the mailbox's canonical bytes |
| **D10, D4** | P8 | fingerprint + attach; new automated caps; stored breaker; a dedicated profile |
| **D14** | half of P9 | the orchestrator reports; takes nothing back |
| **D15** | P3's watch half | seen in `watch`; an explicit return through the same CAS |
| **D16, D17, D18** | the plan's shape | the credential-free half is v1; 0.11.0 or later; no merge |
| **D21, D22, D23, D24, D25** | scope, wording and numbers | per the recommendations in reconciliation §5 |

## 7. The owner-run live checks, proposed by name and not numbered

The register's own instruction is that *"the phase that ships the check writes them"*
(`docs/gcp-live-checklist.md`), so this plan proposes the checks **by name** and takes **no
number** (D24). The reasoning, with the register read name by name, is reconciliation §7: the
numbers around where this work would sit are claimed twice (**110**), written by two other
works (**111**, **112**), taken by two more (**113–118**, **119**), so the next free number
depends on how open claims settle. Each check below is **owner-run, on a real project, with a
real service** — the ones that a runner can do are marked **runner** and are not register
material.

| Proposed check, by name | Part | Kind | What it proves |
| --- | --- | --- | --- |
| the first service deploys and scales to zero | P1, P4 | owner | the type is admitted, a service runs, and an idle service costs nothing |
| a socket survives the service's idle timeout | P5 | owner | the protocol's real connection duration (reconciliation U2) |
| two orchestrators race for one lease | P3 | owner | the invariant on the real database, not the emulator (U3) |
| a takeover at the minute boundary, with a skewed clock | P3 | owner | that the lease uses the database's clock |
| a valid token whose uid has no device node | P5 | owner | the refusal's real shape (U6) |
| a cold start reloads a shared session | P6 | owner | D11's assumption about Firestore at real sizes (U11) |
| an alert job on a dedicated profile | P8 | owner | that D4's narrowing is what it claims (U10) |
| a real provider webhook's signature and retries | P8 | owner | the provider's actual behaviour (U9) |
| the TTL policy's real deletion latency | P6 | owner | that it is late enough for the archive rule (U5) |
| the alert job's narrowed toolset | P8 | owner-adjacent | the toolset's real limits (U8) |
| a rules change against a live database | P2, P3 | owner | the generation bump's real effect on a signed-in phone |
| the console's session with the CSP intact | P9 | **runner** | the console suite, not a live check |
| the CLI attaches from a laptop, over a proxy | P5 | **runner + owner** | the CLI half in CI, the proxy half live |

## 8. The numbers this plan would hold

- **D-numbers: D1–D26**, folder-local and distinct by folder (**D23**). None is allocated by
  this work: the decisions are the owner's to answer, and the answers are recorded in
  [reconciliation.md](reconciliation.md) §5/§6 rather than by rewriting them.
- **Check numbers: none claimed** (**D24**), for the reason in §7 above and reconciliation §7.
- **No Go test is added by this work** (reconciliation C1): the plan is docs-only, and P0 —
  the guard — is the first part that adds one.

## 9. The owner approval gate

**No part of this plan starts before the owner passes this gate.** Passing it means, at
minimum:

1. **Answering D1–D26**, or explicitly deferring the parts they block. The plan is written to
   the recommendations, and every part names the decision it would be rewritten for (§6).
2. **Ruling on the sibling collision (§3 #52, D20)** — this spec's agent-in-the-service versus
   the always-on orchestrator design's agent-in-a-job (PR #632, its §6 rule 4, and mobile
   D115's conditions). The two cannot both be built, and shipping either silently decides the
   other's question.
3. **Ruling on the merge authority (D18)** — the tree's non-goal is *"Auto-merge. Fugaro never
   merges."* (`docs/design/v1.md` §2), and the spec's opening sentence contradicts it.
4. **Saying what happens to the credential path (D4)** — whether v1 ships with the
   credential-free half only (P0–P7) and P8 waits for
   [docs/credentials-service/](../credentials-service/README.md)'s first slice, which is this
   plan's recommendation.
5. **Naming the release (D17)** — this plan recommends **0.11.0 or later**, and not before the
   credentials service's first slice.

**What this gate is not.** It is not an approval of any part: approving the plan starts §6's
answers, and each part is still reviewed on its own. It is not a promise about ordering beyond
§1's four dependencies. And it is not a substitute for the register's own rule about check
numbers — that is settled at merge time, by whichever open claim merges second.
