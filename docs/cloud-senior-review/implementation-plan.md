# The independent senior review as a cloud job: implementation plan

**Nothing here is approved.** This plan exists so that the work is ordered and reviewable.
[reconciliation.md](reconciliation.md) §4's decisions **are all answered** — D891, D892, D893 and D894
with the options named below, and the owner's seven rulings of 2026-11-01 (**O1–O7**) amending the ones
they touch. An answered decision is not an
approval to start: **every task still carries its own gate**, and two of them are hard. **E7** (the
credentials service enabled and admitting a project-scoped job) blocks CSR7, CSR2 and the coordinator's
merge path, and **U7** (an owner-run live observation) gates CSR4b's launch **and the first slice's
signed `ship` path**, through the routing design **#1514**'s **D1074** (its **C3.9e**). A task starts
only when the decisions and the gates it names are satisfied. **CSR4a carries D894(a)'s configuration
posture and waits on nothing** — it is standalone hardening of a stage that exists today — while
**CSR4b carries D885's mutation machinery and does not start until U7's probe has returned**.

**The task ids are prefixed `CSR`, and that is deliberate.** This design's tasks were `R1–R7` in an
earlier draft, and the rigor work stream's own tasks are **R0** and **R1** — so wherever both are
named, an unprefixed `R1` is ambiguous. `CSR1`–`CSR7`, with `CSR4a`/`CSR4b` where the old `CSR4`
split, is the form every document in this design uses.

**Written to:** [spec.md](spec.md) and [reconciliation.md](reconciliation.md). **Where they
disagree, the reconciliation wins.** The findings (**F1–F27**), the dependencies (**E1–E7**) and the
decisions (**D880–D894**, all answered — D891 by the owner with option (a), **D892 with option (a)**
(the verdict is signed, by the **credentials service**, and the coordinator verifies it before it
acts — the signer moved there on the owner's ruling of 2026-11-01, so the key never enters a
container that runs untrusted code), **D893
with option (c)** (the read-only token is fetched from the credentials service, by name) and **D894
with option (a)** (the head is diff content and never configuration, and the base's `CLAUDE.md` and
`.claude/rules` are what the reviewer is given, **O6**)) are that
document's, and each task below names the ones it needs. **E7 is a hard precondition, not a
dependency to schedule around**: D893(c) means the credentials service must be **enabled** and must
**admit the review job's own account**, and until it does, **CSR7, CSR2 and the coordinator's `ship` →
merge path cannot run** (§1 and CSR7 say so).

**Rules every task obeys** (this repository's own, not this plan's invention):

- a red step first: the named test fails for the reason the task exists, and the failure is quoted in
  the pull request;
- targeted tests only, `GOFLAGS=-p=2`, `-count=1`, never a whole package that takes half an hour;
- every new `fugaro.yaml`, task, recipe or wire key gets its JSON Schema in `schemas/` **and** a
  `…Since` gate in `internal/cli/recipes_skew.go` (E6), so an older job image refuses the launch
  rather than ignoring the field;
- hermetic tests only (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`), no live call, no real
  token, no real job;
- no secret-shaped literal anywhere, in a test or a doc.

---

## 1. The order, and what forces it

**The first slice is the smallest thing that lets a cloud run use a named OAuth account end to end:
`fugaro accounts add` plus the reviewer job.** It is two dependencies that must land together — E1
(the account) and CSR1/CSR7/CSR2 (a **job that mounts no secret at all** and fetches its read-only
token by name, and the command that launches it) —
because either alone buys nothing the owner asked for: E1 alone makes `fugaro run --token` work (which
is PR #1470's own subject), and a review job with no named account is a review on the installation's
default token, which the coder's job already had.

```
E1 (accounts add, PR #1470)  ─┐
E2 (yamledit, PR #1467)      ─┤
E3 (publish stages)          ─┴─▶ CSR1 (read-only token) ─▶ CSR7 (the job variant, no App key) ─▶ CSR2 (the command)
                                                                   ▲                              │
                                          E7 (credentials service, │                              └─▶ CSR5 (findings→brief)
                                          enabled + admitting a    │
                                          project-scoped job) ─────┘
   CSR4a (stage config, standalone, first)   CSR3 (the artifact, runner-side) ─▶ CSR4b (mutation, after U7)   CSR6 (gates, docs, release) last
```

**CSR1 before CSR7 before CSR2**, because a command that launches a job must be able to say what that job
may do, and the job it launches must exist: CSR1 names the scope, CSR7 builds the container that can hold
it without the App key, CSR2 starts it. **CSR3 sits outside that chain**: it is the artifact and its
signing, which is runner-side work needing no launch command, so E7 does not reach it (its block says
so). **CSR3 before CSR5**, because the fix brief reads the artifact CSR3
defines. **CSR4b is independent of CSR5** and can ship after CSR3 in either order; **D891 is answered —
option (a) — so CSR4b is unblocked** and carries the machinery the answer names (a token-scrubbed
subprocess with egress denied, and Bash, Edit, Write, NotebookEdit and `Read(/proc/**)` denied for the
rest of the stage), **on one condition: the
answer assumes a fence nobody has measured, so U7 is an owner-run live observation taken before CSR4b
launches — not the task's first step, because it cannot be measured hermetically and this plan's rule
is hermetic tests only** — and **CSR4b does not start until that observation has returned**; if the fence
is not there the owner is asked again and the fallbacks are D891's (b) or (c) (see CSR4b). **CSR4a is
the other half of the old CSR4 and it waits on nothing:** it is D894(a)'s configuration posture —
the reviewer's Claude session loads no configuration from the head it reviews, and is given the base's
`CLAUDE.md` and `.claude/rules` in its place (**O6**) — applied to the `review` stage itself, which
exists today, so it is standalone hardening that **ships first** and does not wait on CSR3 or U7. **CSR6
carries the gates and cannot be split**: a field that lands without its gate is a field an older image
silently ignores.

**E7 gates three things, and it is a precondition rather than a dependency to schedule around.**
**D893(c)** is the owner's answer to how the read-only installation token reaches the job: the job
**fetches it from the credentials service, by name**, and nothing is handed in. That makes the
credentials service — which **ships dormant** — part of the design's own chain of custody. Two halves
must both hold before the chain works, and only one of them is reachable from `fugaro update`:

1. the service **enabled for the installation** (`fugaro update --provision-credentials`, the App key
   in the bundle, plus `fugaro creds set github-app-key`) — the owner's own terminal, and expressible
   in `fugaro update` terms;
2. the service **admitting the review job's own account**, which is a **project-scoped** job and which
   the service refuses today (**Global Constraint 6**; **C3.9**, Slice 3, *unscheduled*, is the task
   that lifts it, and the routing design **#1514** splits it into **C3.9a–C3.9g**) — **no `fugaro
   update` can supply this half**, because it is code in the service, not
   a deployment step. The same work owes the **review class on `/v1/messages`** that **O1** routes the
   reviewer's model calls through, admitted for the review run's account alone.

So **CSR7 cannot run, CSR2 cannot run, and the coordinator's `ship` → merge path cannot be built** until
E7 is met. **And U7 holds the same `ship` path, not CSR4b alone:** the routing design **#1514**'s
**D1074** (its **C3.9e**) makes the owner-run observation a precondition of the first slice's signed
`ship` path, so that path is built after the probe returns as well. **CSR1, CSR3, CSR4a and CSR4b are
unaffected** — none of them launches or authenticates a job, and CSR3 in
particular is **runner-side and needs no command** (it says so in its own block, and it does not
depend on CSR2). **CSR5 and CSR6 are gated transitively, through CSR2**: CSR5 ships the fix brief CSR2's `show`
reads the artifact for, and CSR6 carries the gates and the notes for the command, so neither can finish
before CSR2 can. CSR6 carries the check that reports both halves (see CSR6), and
[reconciliation.md](reconciliation.md) §5's
**E7** is where the dependency is recorded.

**CSR7 is numbered last and comes third, deliberately.** It was added by the fix round after the review
of head `0f5f9a55`, when the design learned the job it had proposed to launch on mounts the App key
(**F3**, **F14**): the numbers here are stable identifiers, and renumbering six tasks and every
reference to them would put a large, purely mechanical diff on top of a design correction. The order
above is the dependency order; the numbering is not a promise about it.

## 2. Shared files

Several files are wanted by more than one task, and three of them are files another work stream also
writes. Every task that touches a shared file takes it **alone**, and the others wait.

| File | Tasks | Note |
| --- | --- | --- |
| `internal/cli/run.go` | CSR2, CSR5 | the launch path. CSR2 adds a sibling command that reuses `claudeAccountFor`/`claudeAccountAllowed` and the job placement; CSR5 adds the findings brief. One task holds it at a time |
| `internal/runner/prompts.go` | CSR3, CSR4a, CSR4b, CSR5 | the review prompt, its template sections, and the fix brief's findings posture |
| `internal/runner/reviewmode.go` | CSR2, CSR3, CSR4a, CSR4b | the review bootstrap and the comment path CSR2 stops taking; CSR4a's D894(a) half reads the base's `fugaro.yaml`, instructions and prompt here |
| `internal/runner/runner.go` | CSR3, CSR4b, **only if** the hook is there — otherwise no task here | **reserved**: one writer across work streams. Every task prefers `reviewmode.go`; a task that needs the stage machine or the finalize path takes this file alone and says so (see the paragraph below) |
| `internal/agent/settings.go` + `settings_test.go` | CSR4a, CSR4b | the one place the launch posture lives: CSR4a writes the managed-hooks-only, no-project-MCP, user-and-managed setting sources and the fresh home (D894(a)), and CSR4b adds D891(a)'s four deny rules to `DenyRules` afterwards. One task holds it at a time, and CSR4a goes first |
| `schemas/` | CSR3, CSR4b | CSR3 **edits the existing `schemas/result.schema.json`** (O4's display-only `independent` tier) and adds `review.schema.json`; CSR4b adds its own new file. The edited schema is one writer's at a time |
| `internal/cli/recipes_skew.go` | CSR2, CSR3, CSR6, CSR7 | one writer (CSR6) for the gate constants; CSR2/CSR3/CSR7 declare what they need |
| `internal/infra/projectjobs.go` + `projectjobs_test.go` | CSR7 | the `ProjectJob` set the installation root deploys. CSR7 adds the review-only variant and its service account; nothing else here reads the set |
| `internal/config/config.go` + `claude_accounts.go` | CSR7, E1 | `config.ProjectJobAccounts` is where a profile's job set is computed, and **E1's `accounts add` converges the same set**. CSR7 must not land a second spelling: it takes these after E1, alone |
| `deploy/terraform/gcp/modules/project/{job,sa,iam,variables}.tf` | CSR7 | the module the variant is an instance of: an entry with an **empty `secret_env`** (no App key, no signing key, and under **O1** no OAuth token either), and a service account with no `secretAccessor` on that key |
| `deploy/terraform/gcp/roots/installation/{main.tf,variables.tf}` | CSR7 | the root that deploys `project_jobs` — the variant's entry travels here |
| `deploy/terraform/gcp/roots/installation/tests/project_job.tftest.hcl` | CSR7 | CSR7's positive test lives beside the retention it contradicts: the variant asserts **no** App key mount and **no** accessor, in the file that pins the opposite for the shared job |
| `internal/cli/doctor.go` + `doctor_secret_readers.go` | CSR7 | the job census and the secret-readers report read `config.ProjectJobAccounts`; a variant the census does not know about is a converged installation `fugaro doctor` calls wrong |

**`internal/runner/runner.go` is reserved, and CSR3/CSR4b may have to take it — the one rule that decides
it.** The release rulings make that file one writer across work streams, and an earlier draft of this
plan said nothing here needs it because neither task adds a stage to the run loop. **That is not safe
to assert, and the review of this branch's fix round is right to say so:** the artifact upload that CSR3
wants to ride (`internal/runner/artifact.go`'s `uploadArtifacts`) is *triggered* from the `mode:
artifact` flow (`collectArtifacts`, driven by a recipe's `outputs:` block) and a `mode: review` recipe
has no such block, and CSR4b's mutation subprocess is the runner's own child rather than a stage. So the
rule each of those tasks follows: **prefer the file it already holds** — a review run's whole flow is
`runReviewMode` in `internal/runner/reviewmode.go`, which CSR2/CSR3 already share — and **if the hook it
needs turns out to be in `runner.go`, the task takes that file alone under the one-writer rule, adds
it to its own Files list and says so in its pull request**; it never edits it silently.

**Not touched by this plan, and named so it is not discovered later:** `internal/cli/root.go`,
`internal/cli/api.go`, `website/**`, `plugin/**`, `console/**`, `mobile/**`, `shared/**`, `ee/**`,
`SECURITY.md` and `docs/design/v1.md`. **Two files this list used to name are in the plan, and saying
so is the point of the list:** `docs/gcp-live-checklist.md` is **CSR2**'s, but only conditionally —
the Check 125 row lands there **only if** the owner's D889 answer changes and the file is in scope,
and otherwise CSR2 records the check instead of writing it (that file is another work stream's, and
its own rule is that a claim is settled at its merge) — and `docs/releases/vX.Y.Z.md` is **CSR6**'s,
for the release this design actually ships in (**0.16.0 or later**, D888/O7), with `docs/releases/**`
otherwise reserved.

## 3. Tasks

### CSR1. A fourth installation-token set: `ReviewTokenPermissions`

**Decision:** D883, **D893 (c)**. **Findings:** F2, F3, **F16**. **Verification:** `internal/gitprov`.

- **Files:** `internal/gitprov/github/apptoken.go`, `internal/gitprov/github/apptoken_test.go`.
  **2 files.**
- **Interfaces:** produces `reviewTokenPermissions` = `{contents: read, pull_requests: read, metadata:
  read}` and its exported spelling beside `RunTokenPermissions` and `BuildTokenPermissions` — the
  **fourth** set, as spec §2 and D883 say. **It builds no mint caller, and F16 is why:** every mint in
  this tree happens where the App key is mounted — `mintBuildToken` (`internal/cli/gitcred.go:92`)
  reads the key from its own environment inside the build that calls it, and `jobGitEnv`
  (`internal/cli/imagecheck.go:530`) does the same inside a check job — so **neither is a precedent
  for a review job, which holds no key**. **D893 is answered with option (c), and that settles what
  this set is for:** the token is minted by the **credentials service** and **fetched by the job from
  the service, by name** — nothing is handed in, and the per-launch secret and the execution override
  this design once weighed are on no path (F16's consequence, reconciliation §2). So this task's set is
  the set **the service's mint must serve for a review identity**; the service serves the *run's* set
  today (`internal/credsvc/github.go:263` hard-codes `RunTokenPermissions`), and growing it is the
  service's own work — **C3.9**, which is not in this plan (§4). **CSR7** carries the variant that
  fetches the token; this task only names the permissions.
- **Tests:** `TestReviewTokenPermissionsAreReadOnly` (every value is `read`, the three keys are
  exactly those, and the map is a clone a caller cannot mutate) and `TestRunTokenPermissionsUnchanged`
  (F2's set is untouched: this task adds a set, it does not narrow one). **No mint test:** there is
  nothing here that mints.
- **Red step:** `TestReviewTokenPermissionsAreReadOnly` fails on an undefined identifier: no
  read-only set beside the run's and the build's exists. **SEC flag:** it is a permission set, and the
  test that matters most is the one asserting the **run's** set did not change.
- **Mutation to hold it:** change one value of the new set to `write` → `TestReviewTokenPermissionsAreReadOnly`,
  the first, fails; narrow the run's set and the second, `TestRunTokenPermissionsUnchanged`, does.
- **Commit:** `gitprov: a read-only installation-token set for the review job`.

### CSR2. `fugaro review --pr N`: launch the review job

**Decisions:** D880, D881, D882, D888, **D893 (c)**. **Findings:** F1, F4, F5, F14, F17. **Depends on:**
E1, CSR1, **CSR7**, and **E7** — see the blocking note below.

- **Files:** `internal/cli/review.go` (new), `internal/cli/review_test.go` (new),
  `internal/cli/run.go`, `internal/runner/reviewmode.go`, `internal/runner/reviewmode_test.go`,
  `internal/cli/recipes_skew.go`, `docs/gcp-live-checklist.md` (the Check 125 row **only if** the
  owner's D889 answer changes and the file is in scope — otherwise this task records it instead).
  **At most 7 files.**
- **Interfaces:** consumes E1's stored account and the job set (`gcp.ProjectJobName`), **CSR7's
  review-only job variant** (this task launches *that* entry, never the shared project job), CSR1's
  read-only set (the permissions the **credentials service** mints for a review identity — **nothing
  is handed in**, D893(c)), and `claudeAccountFor`/`claudeAccountAllowed`. Produces **both verbs of the
  command
  family, in this task**: `fugaro review --pr N [--token NAME] [--json] [--mutate <file:test>]`, which
  writes a task (`mode: review`, the review PRs, the resolved account) and launches it on the
  account's review-only job — `--json` prints the **launch's** result, not the artifact, which does
  not exist until the job has run; and `fugaro review show <run> [--json]`, the reader that prints
  `review.json` and `review.md` (spec §5). CSR5 consumes `show`; it does not add it. Plus a
  `reviewJobSince` constant (E6). The review **job** posts no comment: `reviewmode.go`'s comment path
  is made conditional on a permission the caller states, so a job launched read-only **records** its
  verdict and does not call `Comment`.
- **Blocked on E7, and the refusal is this task's to write.** The launch **refuses before the job
  starts** when the credentials service is not enabled for the installation, and when the service
  would not admit the review job's own account (a project-scoped job; **Global Constraint 6**, C3.9
  unscheduled — §1), naming **which half** is missing rather than reporting one "not configured". The
  refusal is a sentence the owner can act on: half one is `fugaro update --provision-credentials` plus
  `fugaro creds set github-app-key`; half two is not reachable from any `fugaro update` and the
  refusal says so. **Until E7 is met, this task cannot launch a review job at all** — it is written,
  gated and shipped, and the launch is what fails.
- **Tests:** `TestReviewLaunchesOnTheNamedAccount` (the task names the account, the launch targets
  that account's **review-only job entry** and never the shared one), `TestReviewRefusesAnAccountTheRepoMayNotUse` (F4's rule, reused),
  `TestReviewRefusesAnExhaustedAccount` (D882: refused with the mark's reset time, not retried),
  `TestReviewOnAReadOnlyTokenPostsNoComment` (the job's record carries the verdict and the provider
  fake sees no `Comment`), `TestReviewRefusesWhileTheCodingRunIsGoing`,
  `TestReviewShowPrintsTheArtifactAndItsMarkdown`, and
  `TestRecipesSkewRefusesReviewJobOnAnOldImage` (E6), plus two for E7's two halves:
  `TestReviewRefusesWhereTheCredentialsServiceIsOff` and
  `TestReviewRefusesAJobTheCredentialsServiceWillNotAdmit` (both hermetic — a fake service and a fake
  installation, no live call).
- **Red step:** `TestReviewLaunchesOnTheNamedAccount` fails on an unknown subcommand: the command
  does not exist. **SEC flag:** this is the task that decides what the job may do; CSR1's set, this
  task's no-comment path and E7's refusal are reviewed together.
- **Commit:** `review: launch the independent review as its own job on a named account`.

### CSR3. The review artifact: `review.json`, `review.md` and its schema

**Decisions:** D884, D886, D890, **D892 (a)**, **O3** and **O4**. **Findings it acts on:** F11, F12, F13, **F23**. **Findings it is an
answer to, not work it does:** **F7** (the reviewer's model and `agent.max_output_tokens.reviewer`
exist today) is the observation D886 is sized against and the reason this task's schema has to fit a
cap that is already deployed — CSR3 consumes it, it does not address it. **Depends on:** nothing that
launches a job. This task is **runner-side**: it writes an artifact, a schema and a signature client,
and it needs no command, no job variant and no credentials service **running** — the sign endpoint is
called through a small interface a test satisfies with a fake, so E7 does not reach it. It **shares
`internal/runner/reviewmode.go` with CSR2** (§2's shared-files rule: one task holds it at a time), which
is an ordering on a file and not a dependency on the launch.

- **Files:** `internal/runner/reviewartifact.go` (new), `internal/runner/reviewartifact_test.go`
  (new), `schemas/review.schema.json` (new), **`schemas/result.schema.json`** (**O4**: the record's
  `tier` enum gains the display-only `independent` value, so the published shape this task's record
  writes is in this task's files), `schemas/schemas_test.go` (the corpus check),
  `internal/runner/prompts.go`, `internal/runner/verdict.go` (the reader of the **in-run** format,
  to assert it is **not** this one), `internal/runstore/runstore.go` (the record's own artifact list
  and `ReviewedPR`, unchanged in shape), `internal/runner/artifact.go` if a name rule needs it, and
  **`internal/runner/runner.go` only if** the upload hook proves to be in its finalize path rather
  than in `runReviewMode` (§2's one-writer rule; the task says so in its pull request if it takes it).
  **At most 10 files.**
- **Interfaces:** produces the versioned artifact of [spec.md](spec.md) §3 — `schema_version`,
  `review_run`, **`created_at`** (RFC 3339 UTC, the writer's own clock, so the object is
  self-describing without the run record), **`repository`** (the run's own slug, one of the four
  things the signature binds), `pr`, `head_sha`, `base`, `reviewer{account, model,
  effort}`, `verdict` ∈ {`ship`, `fix first`, `reject`}, `overview`, `strengths[]`, `issues[]`
  (`severity`, `class`, `file`, `line`, `summary`, `fix`, `evidence`; the three anchored axes when
  D884's sub-question says carry them), `security`, `performance`, `test_coverage`,
  `mutation_checks[]` (`observed` ∈ {`pass`, `fail`, `error`, `timeout`}, a closed set §3 names) —
  written through the existing artifact stage/upload path, plus `review.md` rendered from it. **The
  stage's own request carries this schema, not `runner.VerdictSchema` (D890, F23)**: the review
  mode's stage call and its `ParseVerdict` are made conditional on the mode being a review *job*, so
  the coding run's `review` stage is byte-for-byte what it is today.
- **The signature, in this task (D892(a)) — and the key is not here.** The owner's ruling of
  2026-11-01 put the signer in the **credentials service**, because this task's job is the one that
  runs a test from the head under review (D885): a key mounted on that variant would share an OS user
  and the `/proc` residual with the account's OAuth token, and a key whose compromise forges every
  verdict of that account is not worth that read. So `reviewartifact.go` carries **no key at all**
  and CSR7 mounts none. What it carries is a **signing client** behind a small interface: it computes
  **SHA-256 over `review.json`'s exact bytes** — the digest, not the object, so the reviewer's prose
  never travels to the service — posts the digest to the service's sign endpoint, and writes the
  **HMAC-SHA-256 over O3's framed message** it gets back (lowercase hex, the algorithm and the frame
  fixed by the design, [spec.md](spec.md) §6) into the **sidecar object**
  `runs/<slug>/<review-run>/artifacts/review.json.sig`,
  one line of hex, so the bytes that were signed are the bytes that are stored. **The frame is this
  task's constant (O3):** what the service signs is the digest **together with the two identities it
  verified on the call** — the run's Firebase identity (`fr`) and the repository claim (`fs`) —
  concatenated in a fixed order with fixed separators, so a signature cannot be lifted from one
  review run to another, and the verify call re-frames from its own copy of the two claims rather than
  from anything the artifact carries. It also carries the
  **verifier's four checks**, which the coordinator calls rather than re-implementing: `review_run`
  equals the run the coordinator launched (the nonce), `repository` and `pr` are that pull request's,
  and `head_sha` equals the pull request's current head — **a replayed verdict from an earlier review
  of the same pull request is refused**, because it is a valid signature over the wrong object. A
  verdict that does not verify is **never a `ship`** (spec §6). **And a sign call that fails ends the
  run `StatusFailed` with the service's reason, never a `ship` with an unsigned artifact (O4)** — the
  failure arm is written with the other refusals rather than left to the caller. Both halves are
  hermetic: the tests
  satisfy the interface with a fake service that holds a `t.TempDir()`-scoped key, never a real secret
  and never a secret-shaped literal. **The residual stays stated:** this closes a *forged or replayed
  artifact*; it does not close an injected reviewer writing a dishonest verdict inside its own run,
  which is why the verdict remains advice (spec §7).
- **The record's own fields, and O4's rule about them.** The review job's record carries **exactly
  one `reviews[]` entry** (`runstore.ReviewSummary`), whose `round`, `findings` and head are the
  existing shape. **O4 is answered: its `verdict` keeps the two words the record has always had
  (`ship`|`changes`, `schemas/result.schema.json:45` and the closed enum at `:291`), and the new value
  goes on `tier` (`:44`, today `first`|`senior`|`adversarial`), which gains **`independent`** —
  display-only, telling a reader which reviewer wrote the entry and carrying no authority.** The
  artifact's three-word verdict never appears in the record, and the signed artifact is the only
  authority on the outcome (spec §3). The coding run's own `review` stage is untouched: it writes
  `VerdictSchema`'s pair as it does today.
- **Tests:** `TestReviewArtifactRoundTrips`, `TestReviewArtifactSchemaMatchesStruct` (the
  schema-vs-Go check `schemas/schemas_test.go` already runs for every shape),
  `TestReviewArtifactRefusesAFourthVerdictWord`, `TestReviewArtifactTruncatedIsAFailure` (D886: a
  clipped result is a failed review, never a short `ship`),
  `TestReviewRecordCarriesTheIndependentTier` (O4: the entry's `tier` is `independent` and its
  `verdict` is one of the record's own two words — the artifact's third word never reaches the record),
  `TestReviewRunFailsWhenTheSignCallFails` (O4: a service that refuses to sign ends the run
  `StatusFailed` with the service's reason and stores no artifact, never a `ship`),
  `TestReviewMarkdownMatchesTheJSON` (section for section),
  `TestReviewArtifactIsSignedByTheCredentialsService` (the writer posts the object's digest and stores
  what the service answers, in the sidecar and not in the object — and a fake service that answers
  under another account's key is refused), `TestVerifyRefusesAForgedArtifact` (a byte changed in the
  object is refused), `TestVerifyRefusesAnUnsignedArtifact` (an object with no sidecar is refused, not
  accepted by default) and `TestVerifyRefusesAReplayedArtifact` (a validly signed artifact of an
  earlier review — a different `review_run`, or a `head_sha` that is no longer the head — is refused,
  which is the check a signature alone does not make), `TestReviewArtifactCarriesItsOwnClock`
  (`created_at` is written, RFC 3339 in
  UTC, and the object round-trips it), and two for F23:
  `TestReviewJobStageIsNotParsedAsAnInRunVerdict` (the review job's result is decoded by the artifact's
  reader and never reaches `ParseVerdict` — and, the other way round, the coding run's `review` stage
  still passes `VerdictSchema`) and `TestUnreadableReviewArtifactEndsTheRunInvalid` (a result with no
  artifact in it ends `StatusFailed`/`OutcomeNone` with the decoder's reason, and **no** synthesized
  `blocker` finding is stored).
- **Red step:** `TestReviewArtifactRoundTrips` fails on an unknown task field: the artifact has no
  writer and no schema; `TestReviewJobStageIsNotParsedAsAnInRunVerdict` fails because the stage's
  request still carries `VerdictSchema`; `TestVerifyRefusesAnUnsignedArtifact` fails because nothing
  verifies; `TestReviewRunFailsWhenTheSignCallFails` fails because a failed sign call is not routed to
  the run's failure arm yet. **SEC flag:** the artifact is the thing a coordinator merges on, so the
  refusal tests are
  this task's security surface — and see CSR4b, whose data this shape carries.
- **Commit:** `runner: the independent review's versioned verdict artifact`.

### CSR4a. The review stage's configuration posture: the head is diff, never configuration (SEC)

**Decision:** **D894 — answered by the owner with option (a)** — and **O6**, which fixes what the
reviewer is given in the head's place. **Findings:** F19, read at what the session **loads** rather
than at what the token may do. **Depends on:** nothing. **This is standalone hardening of a stage that
exists today and it ships first** — it waits on U7 for nothing, on CSR3 for nothing and on E7 for
nothing, and it tightens the `review` stage itself, so `fugaro run --pr N` gets the posture too.

**The posture is review mode's and not the in-loop reviewer's: it applies to the standalone review job
and to the `review` mode of `fugaro run --pr N`, while a coding run's own in-loop review rounds stay
byte-for-byte as they are today until a later task tightens them.**

- **The head's own configuration never loads (D894(a), O6).** The review stage's working directory is
  the pull request's head, and `claude -p` would otherwise read that head's `.claude/settings.json`
  (hooks and permission allows), its `.mcp.json`, its `CLAUDE.md`, its `.claude/rules` and its skills.
  Some of that is already closed in the tree — `stripClaudeConfig` removes a reviewed head's
  `.claude/`, `.claude-plugin/`, `.mcp.json` and `CLAUDE.local.md` before the review stage
  ([reconciliation.md](reconciliation.md) §4, D894) — but `CLAUDE.md` is deliberately left as untrusted
  prompt text, and nothing sets a settings-level posture. **The answer:** the review
  stage's managed settings **allow managed hooks only**, **enable no project MCP**, and take **setting
  sources from user and managed only**; the session's `HOME` is a **fresh directory the job creates**,
  so no user-level settings file baked into the image contributes a rule either; the head's
  `CLAUDE.md` and `.claude/rules/`, at every depth, are diff content and never configuration; and
  **the base's copies of `CLAUDE.md` and `.claude/rules` are supplied in their place (O6)** — read
  from the base ref, not from the head and not out of the head's `.git`, and where the base has none
  the reviewer is given none rather than falling back to the head's. **`fugaro.yaml`, the instructions,
  the review prompt and the account stay the base's and the launcher's**, which is what
  `docs/design/v1.md` already does for follow-ups: `agent.oauth_token` is resolved from `--token` or
  from the configuration at the base ref, so a head that names an account or a model in its own
  settings chooses nothing. **Red test, and it is two halves because only one of them is red:** a head
  that adds a hook and an `.mcp.json` is **already** neutralised by `stripClaudeConfig`, so a test that
  only planted those would pass today and would not be a red step at all — what is red is (i) the
  **settings posture** the stage is launched under, which carries no
  `disableAllHooks`/`allowManagedHooksOnly`/setting-sources keys today, and (ii) the head's
  **`CLAUDE.md`**, which today survives the strip by design and which the base's copy now replaces.
- **Files:** `internal/agent/settings.go` + `settings_test.go` (the managed-hooks-only,
  no-project-MCP, user-and-managed setting sources posture, and the fresh home),
  `internal/runner/reviewmode.go` + `reviewmode_test.go` (the head's `CLAUDE.md` and `.claude/rules`
  stop being loaded, and the base's copies are supplied — `readBaseFile`'s path is where they come
  from), `internal/runner/prompts.go` (the prompt says which tree it is reading). **At most 5 files**,
  and `internal/runner/runner.go` is **not** among them: this task needs no stage-machine hook.
- **Interfaces:** produces a launch posture, not a feature: a `review` stage that loads no
  configuration from the head it reviews and is given the base's `CLAUDE.md` and `.claude/rules`
  instead. It changes nothing for an installation that reviews nothing, and it is the one place this
  design changes behaviour that exists today — the same stage `fugaro run --pr N` runs.
- **Tests:** `TestReviewStageRefusesProjectHooksAndMCP` (the settings the stage is launched under
  allow managed hooks only, enable no project MCP and take setting sources from user and managed
  only), `TestReviewStageLoadsNoHeadConfiguration` (a head that plants a hook, an `.mcp.json` and a
  `CLAUDE.md` contributes none of them — the first two through the posture as well as the strip, the
  third because it is no longer read), and `TestReviewStageIsGivenTheBasesClaudeMd` (**O6**: the
  reviewer's `CLAUDE.md` and `.claude/rules` are the base ref's bytes, and a base that has none gives
  the reviewer none rather than the head's). All hermetic: `t.TempDir()` trees and a fake git.
- **Red step:** `TestReviewStageRefusesProjectHooksAndMCP` fails because the stage's managed settings
  carry none of those keys today, and `TestReviewStageLoadsNoHeadConfiguration` fails because
  `stripClaudeConfig` leaves `CLAUDE.md` in place deliberately
  (`internal/runner/reviewmode.go:238`). **A test that only
  planted a hook and an `.mcp.json` in the head would pass today** — `stripClaudeConfig` already
  removes both — so it is not one of the red steps and is not written as one.
  **SEC flag:** yes — a hook that survives the strip is a shell command run by Claude Code itself,
  with the job's environment and its ordinary egress, and D891(a)'s scrub, Bash denial and egress
  fence are on the agent's own shell and the mutation subprocess rather than on Claude Code's hook
  runner, so the posture is what makes D891(a) a control at all.
- **Commit:** `runner: the review stage loads no configuration from the head it reviews`.

### CSR4b. The mutation check, in its own worktree and never pushed (SEC)

**Decision:** D885 and **D891 — answered by the owner with option (a), which this task is the machinery
for**. **Findings:** F13, **F24**. **Depends on:** CSR3. **Unblocked:** the answer is
(a), so the task is neither dropped nor reduced, and it carries the machinery in its own files — **but
it launches only after U7's owner-run live observation has returned** (below).

- **The gate before this task launches — U7, a standalone owner-run probe, not a test CSR4b can write.**
  D891(a) assumes egress can be denied for a child process inside the container, and **no network
  namespace or `unshare` in Cloud Run's gen2 has been checked** — **nor whether that child can be
  given a `/proc` of its own** (a PID namespace with its own `/proc` mount, or a different uid), which
  is the residual §7 states. That is a property of the **deployed
  execution environment**, so **it cannot be measured hermetically** — and this plan's rule is
  hermetic tests only, no live call and no real job (above), which means the observation is **not the
  task's first step and not something the task does at all**. It is the owner's, taken **before CSR4b
  launches**, and it is **Check 125's step 6** ([spec.md](spec.md) §8). **It is deliberately not asked
  through the mutation check, and the first draft of this bullet got that wrong:** running `fugaro
  review --mutate` to answer U7 would need CSR2's command and this task's mutation code, and CSR4b may not
  start until U7 returns — a question asked by the thing whose existence it decides. So the probe is
  **standalone**: a one-off execution of the deployed job image whose entrypoint forks a child that
  tries to reach the network **and the instance metadata server**, tries to read the parent's
  environment through `/proc`, and is fenced with a network namespace (`unshare -n` where the image has
  it) **and a PID namespace with its own `/proc` mount, or a different uid, whichever the platform
  allows** — recording which of the three outcomes it saw, exactly as [spec.md](spec.md) §8's step 6
  describes it. **Check 125's step 7 is the
  confirmation afterwards**, on the mutation path that actually uses the fence. **CSR4b does not start
  until step 6 has returned**, and **the answer decides the task**: if a child can be fenced, the rest
  of this task proceeds as written; **if it cannot, the task stops and the owner is asked again** —
  the fallbacks are D891's **(b)** (no mutation check in the reviewer at all) or **(c)** (the check in
  a separate execution), both of which are a different task, and the design does not quietly fall back
  to `denyWeb`. **Nothing in this task's Files is opened before that probe returns.**
- **Files:** `internal/runner/mutation.go` (new), `internal/runner/mutation_test.go` (new),
  `internal/runner/reviewartifact.go` (the `mutation_checks` field it fills),
  `internal/runner/prompts.go`, **`internal/agent/settings.go`** + `settings_test.go` (D891(a): the
  review stage's `DenyRules` gains **Bash, Edit, Write, NotebookEdit and `Read(/proc/**)`** for the
  rest of the stage),
  **`internal/runner/reviewmode.go`** + `reviewmode_test.go` (the mutation path's worktree handling),
  `schemas/review.schema.json`,
  and **`internal/runner/runner.go` only if** the subprocess has to be reaped from a stage-machine
  hook rather than from `runReviewMode` (§2's one-writer rule; the task says so in its pull request if
  it takes it). **At most 10 files.** The settings posture itself is **CSR4a's**; this task only adds
  the four rules D891(a) names.
- **Interfaces:** produces the bounded, opt-in check of [spec.md](spec.md) §2 — one mutant, one
  named test, a worktree that is reverted before the stage ends, and **no push ever**
  (`gitops`'s push functions are not reachable from the path). The artefact records what was applied,
  what was observed and that it was reverted. **The mutant is applied, run and reverted by the
  runner, not by the agent's Bash** — a subprocess of the runner's own with **`CLAUDE_CODE_OAUTH_TOKEN`
  and the account's secret scrubbed from its environment and egress denied** (and, under **O1**, no
  token in the container to scrub in the first place) — and the agent's shell
  is denied for the rest of the stage, so the reviewer reads and reasons with no shell to exfiltrate
  through (D891(a), F24). **The revert restores the bytes the runner read before it applied the
  mutant, held in the runner's own memory** — not `git checkout --`, which would let the head's
  `.gitattributes` filters, a configured `core.fsmonitor`, a running fsmonitor daemon and any hook
  the repository configures choose what the revert runs; where a git read is unavoidable it runs with
  hooks and fsmonitor off, filters ignored and `.git` read-only. **The checkout is bounded too**
  (spec §2): every path the checkout and the strip touch is resolved with `Lstat` and confined to the
  worktree root, a symlink is refused rather than followed, the reviewer's input is size-bounded with
  the truncation recorded, and submodules and LFS objects are not fetched under the read-only token.
  **How egress is denied inside a Cloud Run container is U7**
  ([reconciliation.md](reconciliation.md) §6), and it is **the standalone owner-run probe above**:
  until it returns, D891(a) is an answered decision with an unmeasured mechanism, not a fact.
- **Tests:** `TestMutationAppliesRunsRevertsAndPushesNothing` (the git fake records no push, and the
  reverted bytes are the ones the runner read rather than what `git checkout --` would produce),
  `TestMutationRefusesMoreThanOneMutant`, `TestMutationRevertsOnFailure` (a test that errors still
  leaves the worktree clean), `TestMutationAbsentUnlessAskedFor`,
  `TestMutationSubprocessCarriesNoTokenInItsEnvironment`,
  `TestMutationObservedVocabularyIsClosed` (**§3's four words and no fifth**: every value the check
  can write is one of `pass`, `fail`, `error`, `timeout`, and a fifth is refused — the vocabulary a
  coordinator routes on cannot be a free-form string, and the `error` and `timeout` arms are the ones
  a writer would otherwise leave to prose),
  `TestReviewStageDeniesBashForTheRestOfTheStage` (D891(a)'s own red test, and its siblings for the
  other three rules — Edit, Write and NotebookEdit, and the `Read(/proc/**)` denial),
  `TestReviewCheckoutRefusesASymlinkOutOfTheWorktree` (a head that ships
  `CLAUDE.md -> /proc/self/environ` is refused rather than followed), and
  `TestReviewCheckoutBoundsAnOversizedHead` (a diff past the bound is truncated with the truncation
  recorded, and a head too large for the context is a **failed review**, never a short one).
- **Red step:** `TestMutationAppliesRunsRevertsAndPushesNothing` fails on an unknown task field, and
  `TestReviewStageDeniesBashForTheRestOfTheStage` fails because `DenyRules` denies tools only.
  **SEC flag:** the task that runs repository code inside the reviewer, which is the one thing here
  that could look like "the reviewer edits the code" — the revert and the no-push assertions are what
  make §2's "never pushes" a fact, and the scrub, the egress rule and the four deny rules are what
  make D891(a) a fact rather than a sentence. The settings posture that makes it a control at all is
  **CSR4a**'s, and it ships first for exactly that reason.
- **Commit:** `runner: the review job's mutation check, in its own worktree and never pushed`.

### CSR5. The findings reach the fix round's brief, by run id

**Decision:** D887. **Findings:** F20. **Depends on:** CSR2 (which ships `fugaro review show`) and CSR3.
**Holds `internal/cli/run.go` alone.**

- **Files:** `internal/cli/run.go`, `internal/cli/run_test.go`, `internal/runner/prompts.go`,
  `internal/runner/prompts_test.go`, `internal/cli/review.go` + `review_test.go` **only** for the
  reader's findings-rendering helper, if CSR2's `show` does not already expose one. **At most 6 files.**
- **Interfaces:** consumes CSR3's artifact by run id. Produces the brief's findings section: each issue
  as a bounded, quoted block (severity, class, `file:line`, summary, fix), the **review run named**,
  and a prompt sentence that they are findings to weigh and never instructions — the existing
  `internal/runner/prompts.go` posture, extended.
- **Tests:** `TestFixBriefCarriesTheFindingsAndNamesTheReviewRun`,
  `TestFindingsAreBoundedAndQuoted` (an over-long summary is clipped, and nothing reaches a shell),
  `TestFindingsFromAnUnreadableArtifactRefuseTheLaunch` (§6: an unreadable verdict is never a
  `ship`).
- **Red step:** `TestFixBriefCarriesTheFindingsAndNamesTheReviewRun` fails: there is no path from a
  stored artifact to a launched run's brief. **SEC flag:** yes — reviewer output derived from an
  untrusted diff becomes a coder's prompt, which is the injection chain of [spec.md](spec.md) §7.
- **Commit:** `run: a fix round's brief carries the independent review's findings by run id`.

### CSR6. The gates, the docs and the release notes

**Decisions:** D886, D888, D889, **D893 (c)**. **Findings:** F9, F22. **Depends on:** CSR2, CSR3, CSR5.

- **Files:** `internal/cli/recipes_skew.go`, `internal/cli/recipes_skew_test.go`,
  `internal/cli/validate.go` and `validate_test.go` (the notes: the artifact output cap D886, the
  subscription-stage bound F10, and **E7's precondition below**), `docs/cloud-senior-review/spec.md`
  (status), `docs/recipes.md` (the `review-only` recipe's new sibling spelling),
  `docs/project-claude-accounts.md` (the account's job now has a reader), `docs/releases/v0.16.0.md`
  and `docs/releases/README.md` if the release notes are in scope. **At most 9 files.**
- **Interfaces:** produces the `…Since` constants the other tasks declared, the launch refusal naming
  the release whose image reads them, and the owner-facing text. `website/content/cli-reference.md`
  is **regenerated** by the task that ships the command (`ee/clidoc`), and if `website/**` is outside
  that task's scope the task says so in its own pull request (F22, E4).
- **The credentials-service precondition, reported and not fixed (E7).** The design's chain of custody
  runs through a service that **ships dormant** and that refuses a project-scoped job today (§1), so
  the installation must be able to *see* that before it launches anything: a `fugaro validate` note
  and, with CSR7's census, the two facts `fugaro doctor` reports — **is the credentials service enabled
  for this installation** (the `credentials_service` block in localcfg, the opt-in
  `fugaro update --provision-credentials` writes) and **does it admit the review job's own account**
  (Global Constraint 6 / C3.9). The note names **which half** is missing, because only the first is
  reachable from `fugaro update`: it never tells the owner to run a second command for the second
  half, and it never suggests the design will work without it. This is the check CSR2's launch refusal
  mirrors.
- **Tests:** `TestRecipesSkewRefusesReviewJobOnAnOldImage`,
  `TestValidateNotesTheArtifactOutputCap`, `TestValidateNotesTheCredentialsServicePrecondition`
  (both halves, named separately — the service off, and the service refusing the review job's
  account) and the existing
  docs tests for whichever documents this
  task edits (`GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`). **A note about the signing
  key's age is no longer this task's, and that is the ruling's consequence rather than a dropped
  requirement:** the key is the **credentials service's** and the CLI never reads it (D892(a), the
  owner's ruling of 2026-11-01), so the age an operator could act on is the service's to report —
  C3.9's work, with the rotation itself — and a local note here would be a figure the CLI has no way
  to obtain.
- **Red step:** `TestRecipesSkewRefusesReviewJobOnAnOldImage` fails until the constant and the
  refusal exist. **SEC flag:** no.
- **Release notes:** the `### For operators` section says which images must be refreshed before a
  launcher names a reviewer's account, in the release's own words and with no other step. **E7 is the
  one thing the release cannot deliver**: the notes say the cloud reviewer additionally needs the
  credentials service enabled for the installation, and that the service's own admission of a
  project-scoped job is a separate, unscheduled change — a sentence, not an operator step, because
  there is no step the owner can run for it.
- **Commit:** `review: the version gates, the validation notes and the docs`.

### CSR7. The review-only job variant and its service account — no App key (SEC)

**Decisions:** D883, D888, **D893 (c)** — the owner's answer to how the read-only installation token
reaches the job, which **this task implements and does not re-decide** (**UNVERIFIED-2** is closed by
D893) — **D892 (a)**, whose ruling of 2026-11-01 is that **no signing key goes on this variant**:
the credentials service signs — and **O1**, whose ruling of the same day is that the reviewer's model
calls go through the credentials service's review class, so **the variant mounts no secret at all**:
no signing key, and no OAuth token either.
**D891**'s stage posture is **CSR4b**'s, not this task's, and this task only needs the variant to
mount nothing and hand nothing in.
**Findings:**
**F3**, **F14**, **F16**. **Depends on:** E1, CSR1. **Precedes CSR2** (see §1). **SEC flag: yes — this is
the task that makes the read-only scope a boundary instead of a comment on the launcher's intent.**
**Blocked on E7 (§1): this task cannot run until the credentials service is enabled for the
installation *and* admits a project-scoped job — and, under O1, until it also admits the review class
on `/v1/messages`.**

- **Files:** `internal/infra/projectjobs.go` + `projectjobs_test.go` (the `ProjectJob` entry, its
  **empty** `SecretEnv`, and its service
  account), `internal/config/config.go` +
  `internal/config/claude_accounts.go` (`config.ProjectJobAccounts`, the one function the job set is
  computed with — **shared with E1's `accounts add`, taken after it, alone**),
  `deploy/terraform/gcp/modules/project/{job,sa,iam,variables}.tf` (the module instance),
  `deploy/terraform/gcp/roots/installation/{main.tf,variables.tf}` (the root that deploys
  `project_jobs`), `deploy/terraform/gcp/roots/installation/tests/project_job.tftest.hcl` (the
  positive test), `internal/cli/doctor.go` + `doctor_secret_readers.go` (the census and the
  secret-readers report), `internal/cli/project_job.go` + its test (the launcher's entry lookup),
  `internal/cli/recipes_skew.go` (its `…Since`, declared to CSR6). **At most 12 files.**
- **Interfaces:** produces a **second job per `(profile, provider, account)`** — same axis, same
  `gcp.ProjectJobName` shape with a review suffix, its own service account, a **`secret_env` that is
  empty** — no **`github-app-key`**, no review-signing key and, under **O1**, no `CLAUDE_CODE_OAUTH_TOKEN`
  either — and **no `secretAccessor` on the App key** — deployed by `fugaro update`
  with the rest of the job set, and recognised by the census. **Why no signing key on the variant,
  which an earlier draft put there:** this is the container that runs a test from the head under
  review (D885), so a key in its environment would share an OS user and the `/proc` residual with
  whatever else is in there — and the owner's ruling of 2026-11-01 moved the signer to the credentials
  service rather than accept that. **Why no OAuth token either, which the draft before this one put
  there:** the same ruling took the token out of the container altogether (**O1**), so a process that
  reads `/proc` finds the run's own one-hour, read-only installation token and nothing of the
  person's. The variant therefore mounts **nothing**, the account's signing key is a Secret Manager
  secret the **service's own service account** reads
  ([spec.md](spec.md) §6, [reconciliation.md](reconciliation.md) §4's D892 entry), and the token
  mount is kept only as the documented **fallback** if C3.9 slips past the point where the command
  must ship (spec §2, §7).
- **The read-only token, fetched and never handed in (D893 (c)).** The variant carries the
  credentials-service URL in its environment (`FUGARO_CREDENTIALS_SERVICE_URL`, the same variable
  `internal/cli/run.go`'s `credentialsServiceLaunchEnv` sets today), and the job's git provider is
  installed from `github.ServiceTokens` (`internal/gitprov/github/apptoken.go:381`) — the keyless
  source already in the tree — so the token is **fetched by name from the service at the moment it is
  needed and refreshed by the `Token(ctx, minValid)` contract**. Nothing is handed in: no per-launch
  secret is created, and the execution environment carries no token value. The request carries the
  **App ID alone**; the repository and the permissions are the **service's** decision, from the
  verified identity's claims (`internal/credsvc/github.go:245`, `:263`), which is why CSR1's read-only
  set and the service's admission of a project-scoped job are both this task's blockers.
- **Tests:** `TestReviewJobVariantMountsNoAppKey` (the variant's `SecretEnv` names no
  `github-app-key`), `TestReviewJobVariantServiceAccountHasNoAppKeyAccessor` (the account's grant list
  holds no accessor on the key — the exact assertion
  `shared_job_mounting_the_app_key_keeps_the_accessor` makes the other way round),
  `TestReviewJobVariantMountsNoSecretAtAll` (the variant's `SecretEnv` is **empty** — no Claude token
  under **O1**, no signing key under D892(a)'s ruling of 2026-11-01 — and the account's accessor list
  names no secret of the review job's),
  `TestReviewJobVariantIsHandedNoToken` (nothing in the variant's environment or launch object
  carries an installation token; the fetch path is the only one),
  `TestSharedProjectJobKeepsItsAppKey` (the **shared** job's mount and grant are unchanged: this task
  adds a variant, it does not narrow an existing job), `TestProjectJobAccountsKnowsTheVariant` (the job
  set the launcher, the deployer and the runner compute all see it),
  `TestDoctorCensusNamesTheVariant`, and the Terraform test above holds the plan-level assertions.
  All hermetic: no live service, no real token.
- **Red step:** `TestReviewJobVariantMountsNoAppKey` fails: there is no variant, and the only job the
  axis knows mounts the key. **Mutation to hold it:** add `github-app-key` to the variant's
  `SecretEnv` → the first test fails; grant the accessor on it → the second does; add the signing key
  **or the account's Claude token** back to the variant's `SecretEnv` → the third does; drop the App
  key from the **shared** job →
  `TestSharedProjectJobKeepsItsAppKey`, the fifth, does (and the tftest, independently).
- **Commit:** `infra: the review job variant, per account, with no App key`.

## 4. What is deliberately not in this plan

- **`agent.stage_auth`.** D881's answer is that the hold stays, so nothing here touches the held plan
  or its P1–P17.
- **A failover loop.** D882 keeps the retry with the coordinator; the cascade in `internal/creds` is
  the credentials service's and needs no change.
- **The signature is now in this plan; the coordinator's use of it is not.** **D892 is answered with
  option (a)**: the **credentials service** signs `review.json`, and a verdict that does not verify is
  never a `ship`. That answer moved the signature **into** the plan — **CSR3** writes the signing client,
  the verifier and their refusal tests, and **CSR7** keeps the signing key **off** the review variant:
  under **O1** that variant mounts **no secret at all** — no signing key and no Claude token. What is
  still **not** here is the caller: **the
  coordinator** that reads an artifact, verifies it and merges on a verified `ship` is the
  coordinator's own work stream, and it **cannot be built until E7 is met** (§1), since the token the
  whole chain rests on comes from a service that is not yet enabled.
- **The credentials service's admission of a project-scoped job (C3.9).** **D893(c)** makes the
  service a precondition of the whole design, and half of that precondition — the service admitting a
  caller whose account belongs to a project-scoped job, **Global Constraint 6** — is code in
  `internal/credsvc`, scheduled as **C3.9** in the credentials service's own Slice 3 and
  **unscheduled** (the routing design **#1514** splits it into **C3.9a–C3.9g**). It is **not** in
  CSR1–CSR7 and cannot be: it is that work stream's task, and no
  `fugaro update` can supply it. CSR1 names the read-only set the service must serve, **O1** adds the
  **review class on `/v1/messages`** the same work owes, CSR2 and CSR6 report
  when the precondition is unmet, and CSR7 waits on it; none of them builds it.
- **Posting the review as a pull request comment.** CSR2 removes the comment from the review job's
  path; a coordinator-side poster is the coordinator's work.
- **A console or mobile surface for the artifact.** The console contract is another work stream's
  (`console/**`), and the artifact is readable through `fugaro review show --json` first.
- **Re-reviewing automatically after the fix round.** CSR5 says the coordinator asks for a review of
  the new head; a loop that chains fix → review → fix lives in the coordinator, not here.
- **`review_levels` / L3.** E5 is independent, and this design's security-sensitive checklist does
  not wait on it.
- **The per-run bucket-prefix narrowing (F15).** A review job's account reaches the whole of `runs/`,
  so §6's integrity argument rests on ordering rather than on a boundary. Narrowing it is a
  bucket-layout change with an IAM migration, and the owner's 2026-10-17 ruling accepted the reach;
  if the owner ever wants the boundary, it is its own design and it belongs to the project-job work,
  not to this one.
- **An egress rule for the review job as a whole, or a token scoped to one run.** What
  D891(a) chose is narrower than either and is **CSR4b**'s: denial for the **mutation subprocess**
  and **Bash, Edit, Write, NotebookEdit and `Read(/proc/**)` denied for the rest of the stage**, not a
  firewall around the job. A whole-job egress
  rule is a bigger change (it is the job's VPC/egress posture, shared with every other tenant of the
  same image), and it is not what the owner answered.
- **A cap on the fix ↔ review loop (F27), because the cap now exists elsewhere.** The counting lives
  with the **chain**, not with this design: a review job on PR N joins the pull request's chain by its
  PR link, and the chain's `--max-review-rounds` counts senior-review iterations (D1058,
  [docs/run-limit-carry-over/](../run-limit-carry-over/spec.md) §8, merged as #1485; the count is
  **advisory** there — read off the records, no rule behind it — which §7 of the spec states). What
  this plan owes that cap is **CSR3's record** — the PR link and exactly one `reviews[]` entry, which is
  what the chain counts — and nothing else: a counter inside one job could not see the loop, and a
  design that added one would be a second copy of the chain's.

## 5. Release slot

**Recommended: 0.16.0 or later, gated on C3.9 — and that is O7's answer, which moves the slot this
plan first named.** The first slice (E1 + CSR1 + CSR7 + CSR2) needs a new command, a new job shape, a
**new job variant and service account per account**, a fourth permission set and at least one version
gate; CSR3–CSR5 add a new wire format and a new prompt path. **E1 is `fugaro accounts add`'s own three
tasks** — **A1** the local config edit, **A2** the converge half and **A3** `--claude` — and A2's
shared write helper is the settings design's **S5** (`internal/cli/settings_write.go`). A 0.14.2 that
tried to carry any of it would
carry a gate-less field, which is the failure mode E6 exists to prevent — an older image that decodes
a key it ignores and runs the opposite of what the owner asked. **The gate is C3.9, and it is why the
number is "0.16.0 or later" rather than a date:** the whole slice needs the credentials service to
admit the review identity, serve the read-only set, admit the review class on `/v1/messages` and
answer the sign and verify calls — **C3.9a–C3.9g** of that service's Slice 3, another work stream's
and unscheduled (**E7**) — so a release cut before it ships a command that cannot run.

**CSR7 is what makes that release larger than the first draft claimed, and it is not optional.** The
`### For operators` section therefore says two things in the release's own words and asks for nothing
else: **run `fugaro update`**, which deploys the new job variants with the rest of the job set (the
HARD RULE — no second command, no manual repair), and it will ask whether to **refresh the images**
before a launcher can name a reviewer's account on a variant. An installation that never runs a review
converges to the new job set and behaves exactly as it does today.

**The release can ship the chain, but it cannot make it work — and it says so.** The cloud senior
reviewer's read-only token comes from the credentials service (**D893 (c)**), which **ships dormant**
and does not yet admit a project-scoped job (**E7**, §1). So 0.16.0 or later delivers the command, the variant,
the artifact and its signature, and the **launch refuses with the reason** until the service is enabled
and its own admission change lands; the release notes name that as a sentence, not an operator step,
because **there is no step the owner can run for the second half** (CSR6). This is the honest shape of a
design whose last link is another work stream's: nothing here pretends the reviewer runs on the day the
release ships.
