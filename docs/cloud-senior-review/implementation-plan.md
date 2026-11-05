# The independent senior review as a cloud job: implementation plan

**Nothing here is approved.** This plan exists so that the work is ordered and reviewable.
[reconciliation.md](reconciliation.md) §4's decisions **are all answered** — D891, D892, D893 and D894
with the options named below, and the owner's seven rulings of 2026-11-01 (**O1–O7**) amending the ones
they touch. An answered decision is not an
approval to start: **every task still carries its own gate**, and one of them is hard. **E7** (the
credentials service enabled and admitting a project-scoped job) blocks CSR7, CSR2 and the coordinator's
merge path. **U7** (the owner-run live observation of the deployed fence) **is ANSWERED
(2026-11-01)** — both fences hold — so it gates nothing **for the environment it probed**, an ordinary
coding job on Cloud Run gen2 (if CSR7's review-only variant runs somewhere else, Check 125's step 7
confirms again there), and the same answer is the observation the
routing design **#1514**'s **D1074** (its **C3.9e**) made a precondition of the first slice's signed
`ship` path. A task starts
only when the decisions and the gates it names are satisfied. **CSR4a carries D894(a)'s configuration
posture and waits on nothing** — it is standalone hardening of a stage that exists today — while
**CSR4b carries D885's mutation machinery and U7's probe has returned, so it waits on CSR3 alone**.

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
   CSR4a (stage config, standalone, first)   CSR3 (the artifact, runner-side) ─▶ CSR4b (mutation)   CSR6 (gates, docs, release) last
```

**CSR1 before CSR7 before CSR2**, because a command that launches a job must be able to say what that job
may do, and the job it launches must exist: CSR1 names the scope, CSR7 builds the container that can hold
it without the App key, CSR2 starts it. **CSR3 sits outside that chain**: it is the artifact and its
signing, which is runner-side work needing no launch command, so E7 does not reach it (its block says
so). **CSR3 before CSR5**, because the fix brief reads the artifact CSR3
defines. **CSR4b is independent of CSR5** and can ship after CSR3 in either order; **D891 is answered —
option (a) — so CSR4b is unblocked** and carries the machinery the answer names (a token-scrubbed
subprocess with egress denied, started from an explicit environment rather than an inherited one —
U7's caveat 1 — and Bash, Edit, Write, NotebookEdit and `Read(/proc/**)` denied for the
rest of the stage). **The condition it was written on has been met:** the answer assumes a fence, and
**U7's owner-run live observation — the one measurement that cannot be hermetic, because the fence is a
property of the deployed execution environment — ran on 2026-11-01 and found both fences present and
holding**, so **CSR4b no longer waits on it**; had the fence been absent the owner would have been
asked again and the fallbacks are D891's (b) or (c) (see CSR4b). **CSR4a is
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
E7 is met. **And the `ship` path carried the same observation, not CSR4b alone — and that half is
cleared:** the routing design **#1514**'s
**D1074** (its **C3.9e**) makes an owner-run probe of the sandbox environment a precondition of the
first slice's signed `ship` path, and **U7's probe is that observation** — taken on the sandbox
installation on 2026-11-01, finding both fences present and holding — so **that precondition is met
for the environment the probe ran in and not as a claim about every environment**: it was an ordinary
coding job on Cloud Run gen2, and if **CSR7**'s review-only variant runs anywhere else, **Check 125's
step 7 re-confirms it there** (U7's caveat 4).
**CSR1, CSR3, CSR4a and CSR4b are
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
| `internal/agent/settings.go` + `settings_test.go` | CSR4a, CSR4b | the one place the launch posture lives: CSR4a writes the managed-hooks-only, no-project-MCP, user-and-managed setting sources and the fresh home (D894(a)), and CSR4b adds D891(a)'s five deny rules to `DenyRules` afterwards. One task holds it at a time, and CSR4a goes first |
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
`SECURITY.md` and `docs/design/v1.md`. **§6 amends this list in exactly one place, and says so rather
than leaving it to be found:** **CSR9** registers the command, which is a change to
`internal/cli/root.go`, to the **generated** `website/content/cli-reference.md` and to the working
skill's text — the command's own surface, which cannot be shipped without them (F22, E4), and nothing
else in those trees. `internal/cli/api.go`, `ee/**`, `console/**`, `mobile/**`, `shared/**` and the rest
of the list are untouched by §6 as they are by §3. **Two files this list used to name are in the plan, and saying
so is the point of the list:** `docs/gcp-live-checklist.md` is **CSR2**'s, but only conditionally —
the Check 125 row lands there **only if** the owner's D889 answer changes and the file is in scope,
and otherwise CSR2 records the check instead of writing it (that file is another work stream's, and
its own rule is that a claim is settled at its merge) — and `docs/releases/vX.Y.Z.md` is **CSR6**'s,
for the release this design actually ships in (**0.16.0 or later**, D888/O7), with `docs/releases/**`
otherwise reserved.

## 3. Tasks

### CSR1. A fifth installation-token set: `ReviewTokenPermissions`

**Decision:** D883, **D893 (c)**. **Findings:** F2, F3, **F16**. **Verification:** `internal/gitprov`.

- **Files:** `internal/gitprov/github/apptoken.go`, `internal/gitprov/github/apptoken_test.go`.
  **2 files.**
- **Interfaces:** produces `reviewTokenPermissions` = `{contents: read, pull_requests: read, metadata:
  read}` and its exported spelling `ReviewTokenPermissions` beside `RunTokenPermissions` and
  `BuildTokenPermissions` — the
  **fifth** set, as spec §2 and D883 say. **It builds no mint caller, and F16 is why:** every mint in
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
  one line of hex, so the bytes that were signed are the bytes that are stored. **This task sends only
  the digest; the frame is not its constant but the service's** — built in `internal/credsvc` (C3.9e,
  D1073) from the two identities the service verified on the call — the run's Firebase identity (`fr`)
  and the repository claim (`fs`) — concatenated in a fixed order with fixed separators, so a signature
  cannot be lifted from one review run to another, and a sign request that names a run or a repository
  is refused. The verify call re-frames from its own copy of the two claims rather than from anything
  the artifact carries. It also carries the
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
(a), so the task is neither dropped nor reduced, and it carries the machinery in its own files — and
**U7's owner-run live observation has returned (2026-11-01), so the task waits on CSR3 alone**.

- **U7, the standalone owner-run probe that was this task's gate — ANSWERED (2026-11-01), so the task
  is unblocked.** D891(a) assumes egress can be denied for a child process inside the container, and
  **no network namespace or `unshare` in Cloud Run's gen2 had been checked** — **nor whether that child
  can be given a `/proc` of its own** (a PID namespace with its own `/proc` mount, or a different uid),
  which is the residual §7 states. That is a property of the **deployed
  execution environment**, so **it cannot be measured hermetically** — and this plan's rule is
  hermetic tests only, no live call and no real job (above), which is why the observation was **never
  the task's first step and not something the task does at all**. It is the owner's, and it is
  **Check 125's step 6** ([spec.md](spec.md) §8). **It is deliberately not asked
  through the mutation check, and the first draft of this bullet got that wrong:** running `fugaro
  review --mutate` to answer U7 would need CSR2's command and this task's mutation code, and CSR4b could
  not start until U7 returned — a question asked by the thing whose existence it decides. So the probe is
  **standalone**: a one-off execution of the deployed job image whose entrypoint forks a child that
  tries to reach the network **and the instance metadata server**, tries to read the parent's
  environment through `/proc`, and is fenced with a network namespace (`unshare -n` where the image has
  it) **and a PID namespace with its own `/proc` mount, or a different uid, whichever the platform
  allows** — recording which of the three outcomes it saw, exactly as [spec.md](spec.md) §8's step 6
  describes it. **What it saw is the first outcome:** on the sandbox installation — run
  **`20261101-210556-146e`**, on a real kernel (6.9.12, not gVisor), Cloud Run gen2, uid 1000,
  no capabilities, `NoNewPrivs`
  1 — `unshare -rn` and `unshare -rnpf --mount-proc` leave the child with no network, and
  `unshare -rpf --mount-proc` and `unshare -rnpf --mount-proc` give it a `/proc` of its own, while
  `unshare -n`, `setpriv` and `runuser` are refused; unfenced, the child reached the internet and both
  metadata addresses and read the parent's environment and `/proc/1/environ`. **So the task proceeds as
  written, with U7's caveats as requirements on it:** the mutant/test child **starts from an explicit
  environment** (`env -i` plus an allow-list) and never inherits the runner's, because the namespace
  stops it reading the parent's `/proc` and not the variables it was handed (caveat 1 — the same
  requirement on #1514's **C3.9e**'s sandbox, which is the other place a child is started); the
  "parent environ denied" half is partly an artifact (`$PPID` reads 0 in there) and the evidence is
  the drop in visible PIDs (caveat 2); **only the
  tool or test child is fenced and never the agent**, because `unshare -rn` removes the loopback the
  agent's model calls need (caveat 3); and because the probe ran
  under the agent's shell in an ordinary coding job, **if CSR7's review-only variant runs in a
  different execution environment, Check 125's step 7 confirms again** (caveat 4). Caveat 5 is the
  reason all of this is a requirement rather than hardening: the unfenced baseline exposure is real.
  Had a child been unfenceable, the task would have stopped and the owner been asked again — the
  fallbacks are D891's **(b)** (no mutation check in the reviewer at all) or **(c)** (the check in
  a separate execution), both of which are a different task, and the design does not quietly fall back
  to `denyWeb`. **Check 125's step 7 stays the confirmation afterwards**, on the mutation path that
  actually uses the fence. **Nothing in this task's Files is held by the probe any longer.**
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
  the five rules D891(a) names.
- **Interfaces:** produces the bounded, opt-in check of [spec.md](spec.md) §2 — one mutant, one
  named test, a worktree that is reverted, **the check run after the review stage with only the
  artifact write following it** (the owner's ruling on D891(a), 2026-11-02, §2), and **no push ever**
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
  ([reconciliation.md](reconciliation.md) §6), and **U7 is answered — the standalone owner-run probe
  above found both fences present and holding** — so the mechanism is measured rather than assumed:
  the subprocess runs under a network namespace with a `/proc` of its own (`unshare -rnpf
  --mount-proc`, or `unshare -rn` with the private `/proc` from `unshare -rpf --mount-proc`), and it
  **starts from an explicit environment** (`env -i` plus an allow-list), because the fence does not
  reach the variables a child is handed (U7's caveat 1).
- **Tests:** `TestMutationAppliesRunsRevertsAndPushesNothing` (the git fake records no push, and the
  reverted bytes are the ones the runner read rather than what `git checkout --` would produce),
  `TestMutationRefusesMoreThanOneMutant`, `TestMutationRevertsOnFailure` (a test that errors still
  leaves the worktree clean), `TestMutationAbsentUnlessAskedFor`,
  `TestMutationSubprocessCarriesNoTokenInItsEnvironment` (which per U7's caveat 1 also pins that the
  child's environment is the explicit `env -i` allow-list and not the runner's — a namespace does not
  reach the variables a child is handed),
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
  make §2's "never pushes" a fact, and the scrub, the egress rule and the five deny rules are what
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
  installed from `github.ServiceTokens` (`internal/gitprov/github/apptoken.go:405`) — the keyless
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
**new job variant and service account per account**, a fifth permission set and at least one version
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

**§6's five follow-up tasks do not move this slot, and they are what a release must carry to be worth
cutting.** CSR8–CSR12 are what makes the command reachable (`fugaro review` was built and registered
nowhere), the container placeable and the verdict verified; the release that deploys the variant is also
the release `reviewJobSince` must name, and **Open item 3's answer of 2026-11-05 settles it as 0.20.0
"Quokka"** — the 0.19.0 release in flight does not carry this work, so that release's own change moves
`reviewJobSince` to `"0.20.0"` and writes the `### For operators` sentence (§6.5) for it.

## 6. Follow-up after CSR1–CSR7: what still stands between this plan and a review that runs

**Nothing in this section is approved either, and it adds no decision.** It was written on 2026-11-05,
when **CSR1–CSR5 and CSR7 were merged and CSR6 was in flight** (PR #1750), and its whole reason is that
**the merged tasks do not add up to a working review**: the command CSR2 built is registered nowhere, the
constant E7's second half is still `false`, the container cannot place the job the launcher picks, and the
launcher reads a review's artifact without the signature that makes it a verdict. A task in §3 that
merged is a task that was built and tested; it is not a task whose work is reachable from the surface a
person types at.

**Seven gaps, each with the owner it has after this section.** Six of them were the state this section was
written against; the seventh was found while writing it and is marked as such, because a gap discovered
while documenting the others is exactly the kind of thing a plan loses by not writing down:

| # | the gap | where it is | the owner |
| --- | --- | --- | --- |
| 1 | a review launch is refused unconditionally: the constant E7's second half is `false` | `internal/cli/review.go:39` | **CSR11** (the flip) — and the refusal is the reason the flip has a gate at all |
| 2 | `fugaro review` is built and **registered nowhere**, so no released CLI answers to it: `newReviewCmd` is not in the list `newCoreRoot` adds | `internal/cli/root.go:189`, `internal/cli/review.go:62` | **CSR9** |
| 3 | the runner cannot place the job the launcher picks: the review variant's name is never a candidate, so a review container halts at admission with the client's `cannotPlaceJob` | `internal/runner/projectjob.go:178`, `internal/runner/project.go:1114` | **CSR8** |
| 4 | the launcher reads `review.json` and never reads `review.json.sig`; `runner.VerifyReviewArtifact` exists and **nothing calls it**, because no implementation of `runner.SignatureVerifier` is in the tree | `internal/cli/review.go:455`, `internal/runner/reviewartifact.go:531`, `:223` | **CSR10** |
| 5 | the owner-run live checks have not been run, and **cannot** be while gap 1 holds: the C3.9 check's step 3 launches `fugaro review --pr N`, and Check 125's steps 6 and 7 confirm the fence on the variant | `docs/c39-review-identity/live-check.md`, [spec.md](spec.md) §8 | **the owner** — CSR11 is the task whose gate it is, and Open item 1's answer of 2026-11-05 is **CSR12**, the switch the check's step 3 is reached with |
| 6 | the credentials service **ships dormant**: the opt-in, the App key and the verifier identity are the owner's own terminal (C3.9g1) | `internal/credsvc/auth.go:335` | **the owner** — no task here; it is the release's one conditional operator sentence (§6.5) |
| 7 | *(found while writing this section)* the review variant holds no model credential (O1) and the service has a review class for its calls, but **the runner has no model path that reaches it**: `GatewayOn` is false unless `agent.auth` is one of its three, the service is reached only from the cascade arm, and that arm is refused in bootstrap unless the **operator publishes the coding cascade switch** — the two things [spec.md](spec.md) §2 says a review job does not need | `internal/runner/routing.go:158`, `:60`, `internal/credsvc/cascade.go:105` | **the owner** — Open item 4, and deliberately not a task here |

**CSR8–CSR12 follow §3's convention exactly**: decisions and findings named, an exact file list with its
cap, interfaces, named tests, a **red step** quoted in the pull request, targeted hermetic tests
(`GOFLAGS=-p=2`, `-count=1`), a mutation to hold each, the reviewer tier, and `OWNER-RUN` where a step is
a person's. Each is sized for one run of the coding agent this repository is built to drive, and none of
them takes a file another one holds in the same level (§6.4).

**The owner answered three of §6.6's Open items on 2026-11-05, and this section is followed through on
all three.** Each answer is recorded **in its own Open item**, with the date and the one line of why, and
the rest of §6 is what changed for it: item 1's environment switch is now **CSR12** (a task with the
switch's constraints and its security review) and CSR11 removes it in the same change as the flip; item
2's verdict policy is now part of **CSR10**'s reader, which refuses an artifact whose verdict is not `fix
first`; item 3's release is **0.20.0 "Quokka"**, and §5, §6.4 and §6.5 name it. **The answers are the
owner's and this section decides nothing else**: item 4 (the review run's model path) and item 5 (who may
verify) are open, and neither is chosen here.

### CSR8. The runner places the review variant's execution (SEC)

**Decisions:** D883, **D893 (c)**, **O1**. **Findings:** F3, F14, **F17** (the placement rule the job
name is matched by). **Depends on:** nothing in this plan — **CSR7 is merged** and the variant exists;
this task makes the *container* able to say which job it is in. **E7 does not reach the code**: the
placement is hermetic, the service is not called, and the test is a policy and a job name. **What E7
still decides is whether a placed review run reaches its first call** (§1), and that is not this task's.
**Before CSR11**: a flipped constant launches a run this task is what lets start.

- **Files:** `internal/runner/projectjob.go`, `internal/runner/projectjob_test.go`,
  `internal/runner/project.go`, `internal/runner/project_test.go`,
  `internal/config/claude_accounts.go`, `internal/config/claude_accounts_test.go`. **6 files.**
- **Interfaces:** `projectJobCandidates` (`internal/runner/projectjob.go:178`) produces, beside each
  coding triple, **the review variant's candidate** — the same `(profile, provider)` and the account the
  variant's names spell, computed with the functions the deployer and the launcher already spell it with
  (`credsvc.ReviewJobName`, `internal/credsvc/review.go:88`, and the axis mapping
  `infra.ReviewVariantAccount`, `internal/infra/projectjobs.go:313`, whose `""` →
  `config.DefaultClaudeAccount` is the one line the launcher's `projectJobReviewFor` gets right today,
  `internal/cli/project_job.go:351`) — so `matchJobName` (`:143`) places a run in the variant, `jobIdentity`
  adopts its `(profile, provider, account)` and `admissionCheck` (`internal/runner/project.go:1114`) stops
  refusing it, and **the launcher, the deployer and the runner name one job rather than three**. The
  **mapping lands once**, in `internal/config` beside `ProjectJobTokenAccount`
  (`internal/config/claude_accounts.go:156`) and `ReviewVariantSuffix` (`:215`), which is the file that
  already owns the axis: the runner does not import `internal/infra` today and keeping the container out
  of the deployer's package is why it does not start to. `internal/infra`'s own function becomes that
  mapping's spelling. **`checkProjectJobAccount` (`internal/runner/projectjob.go:406`) gains the review
  arm** and it is the half a name alone does not cover: the variant **holds no token** — `config.ProjectJobTokenAccount`
  knows unsuffixed names only, so `serves` is false and an OAuth review run is refused with "this job
  holds no Claude token" — and the run's own credential is not the account the axis
  names — for a review job the account is the **class's**, and the refusal that sentence belongs to
  (a run authenticating as something its configuration did not choose) does not apply to a job whose
  credential the class decides. The arm is keyed on the run's own marker and on the candidate's class,
  never on the name's suffix parsed back out (R2-5).
- **`ProjectJobOfPolicy` sees the variant too, and what that changes for its two outside callers.**
  `jobTriple` (`internal/runner/projectjob.go:45`) gains a **class** field (`coding` or `review`), because
  the token axis is not the only thing the class decides: `checkProjectJobAccount` above must know whether
  the account it is holding is one whose job mounts a Claude token at all. **The class stays inside the
  package.** `ProjectJobOfPolicy` (`internal/runner/projectjob.go:201`) iterates the same
  `projectJobCandidates` set — that is what makes the two readers unable to disagree (R2-5) — so it
  **does** start answering for a review job's name, and it keeps its three-string return rather than
  growing a fourth: it reports the **variant's own account** (`team-a-review` for the account `team-a`),
  which is the account the variant's job name was computed from. Both of its outside callers are correct
  as they stand and neither file changes:
  - the launcher's `reviewJobFor` (`internal/cli/review.go:251`) passes the **coding** job's name and
    keeps getting the coding axis, which is what it maps to the variant with (`projectJobReviewFor`,
    `internal/cli/project_job.go:351`) — and **a review job's own name never round-trips through it**;
  - watch's `JobRef` (`internal/cli/project_job.go:233`, a `--json` field) resolves the run's own job from
    its `launch.json`, so a **review** run's row starts carrying the variant's account where it carried
    nothing at all before (the name was not a candidate, so `ProjectJobOfPolicy` answered `ok == false`).
    That is **a field gaining a value it never had, not a field changing meaning**, which is why no new
    name is needed for it and why `internal/cli/project_job.go` is deliberately **not** in this task's
    files. A reader that has to tell a review job from a coding one reads the run's own review marker
    (`internal/cli/run.go:3411`), not the account's spelling; **if a later task wants the class in the
    watch JSON, it is a new field on `watch.JobRef` and a file of that task's own**, never the account
    field re-spelled.
- **Command:** `GOFLAGS=-p=2 go test ./internal/runner/ -run 'TestReviewJobVariantIsPlaced|TestCodingRunIsNotHandedTheReviewCandidate|TestReviewRunAdmitsAJobWithNoToken|TestProjectJobOfPolicySeesTheVariant' -count=1`
  and `GOFLAGS=-p=2 go test ./internal/config/ -run TestProjectJobAccounts -count=1`.
- **Tests:** `TestReviewJobVariantIsPlaced` (a policy with one profile and one account: the variant's
  name matches, and the run's identity is the triple the launcher resolved),
  `TestCodingRunIsNotHandedTheReviewCandidate` (a coding run in the coding job still matches the coding
  candidate, and **the variant's name is not a name a coding run is admitted to** — the two directions are
  the red step), `TestReviewRunAdmitsAJobWithNoToken` (an OAuth review run on the variant is admitted
  rather than refused for holding no token), `TestReviewVariantIsNotTheCodingJob` (a run **not** marked as
  a review is refused when it is in the variant's job: the class is decided by the launcher's marker and
  the job, not by the name), `TestProjectJobTokenAccountRefusesTheVariantName` (the token axis still
  answers for unsuffixed accounts only, and says so for the variant's), and
  `TestProjectJobOfPolicySeesTheVariant` (the shared candidate set answers for the variant's name with the
  **variant's** account, and the coding job's name still answers with the coding axis — the two directions
  of the paragraph above).
- **Red step:** `TestReviewJobVariantIsPlaced` fails on the client's refusal: the job the launcher
  chooses is a name the published policy does not compute
  (`cannotPlaceJob`, `internal/runner/projectjob.go:143`). **SEC flag:** yes — this is the check that
  decides **which job a container with no App key and no token believes it is**, and its whole value is
  that the answer comes from the published policy rather than from the job's own name.
- **Mutation to hold it:** list the variant for every account → `TestCodingRunIsNotHandedTheReviewCandidate`
  fails; drop the `""` → default-account mapping → `TestReviewJobVariantIsPlaced` fails (the default
  account's variant is the one no coding triple spells); admit the variant to a run with no marker →
  `TestReviewVariantIsNotTheCodingJob` fails; let `checkProjectJobAccount`'s token axis answer for the
  variant → `TestReviewRunAdmitsAJobWithNoToken` fails.
- **Reviewer tier:** **SEC — opus, with the independent review mandatory** (the repository's rule for a
  change on the admission path).
- **OWNER-RUN:** none. Every assertion here is hermetic.
- **Commit:** `runner: place a review run in its own job variant, and admit a job that holds no token`.

### CSR9. Register `fugaro review`, so the command exists before it can launch

**Decisions:** D880, D888, **D893 (c)**. **Findings:** **F22**, **E4** (the generated reference and the
plugin pin). **Depends on:** CSR2 (merged — the command, its refusal and `review show` are written).
**Nothing here launches anything**, and the launch stays refused exactly as it is: the constant is
CSR11's, and the reason is `checkReviewJobAdmitted` (`internal/cli/review.go:217`), which this task makes
reachable rather than true.

- **Files:** `internal/cli/root.go`, `internal/cli/root_test.go`, `website/content/cli-reference.md`
  (**generated** — `go run ./ee/clidoc`, by the tagged tree, never hand-edited),
  `plugin/skills/working/SKILL.md:52` (its command list), and
  `plugin/skills/routing/SKILL.md` **only if** that skill is where the review flow is routed. **At most
  5 files.**
- **Interfaces:** `newReviewCmd()` joins the list `newCoreRoot` builds
  (`internal/cli/root.go:189`), so `fugaro review --pr N [--token NAME] [--mutate FILE:TEST] [--json]`
  and `fugaro review show RUN [--json]` are names the released binary answers to; the reference page is
  **regenerated** from that tree in the same commit, so the core subset check
  (`internal/clidoc/coreref_test.go`) and the generator's own check pass on the tree as it is; and the
  working skill gains the one paragraph a user needs — a review is its own job, it needs the installation's
  credentials service, and a **refused** review is the sentence to read rather than a retry. The plugin
  pins (`fugaro-version`, the manifest's `version`, the `Fugaro X.Y.Z` sentence) do **not** move here:
  they move through `scripts/bump-plugin-version.sh` with the release that carries the skill's text, and a
  text edit is not a version bump.
- **Command:** `go run ./ee/clidoc`, then `go test ./internal/clidoc/ -count=1`,
  `go test ./ee/clidoc/ -count=1` (the generated page's own check,
  `TestCliReferenceIsCurrent`, `ee/clidoc/cliref_test.go:26`), `go test ./plugin/ -count=1` and
  `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs|Root|Review' -count=1`.
- **Tests:** `TestReviewIsRegisteredInTheCoreRoot` (the name resolves in the core tree, with its
  subcommand and both visible flags — the red step), the **existing**
  `TestReviewRefusesAJobTheCredentialsServiceWillNotAdmit` (`internal/cli/review_test.go:309`, merged with
  CSR2 and **not edited here**: the refusal sentence is what a person gets, unchanged — this task
  registers a command, it does not admit one), `TestCliReferenceNamesTheReviewCommand` (the regenerated
  page carries the section and every visible flag — the existing "names every command and flag" check, now
  with a command it did not have),
  and the plugin suite's own resolution of `fugaro review` against the CLI tree
  (`plugin/skillcli_test.go`'s `resolveSkillCommand`): **a skill may only name a command the tree has**,
  which is why the sentence and the registration are one task and not two.
- **Red step:** `TestReviewIsRegisteredInTheCoreRoot` fails on an unknown command: `fugaro review` is not
  a name this binary answers to, and the whole command file beside it is dead code.
  **SEC flag:** no. It adds no permission and removes no refusal; the one security-relevant sentence in it
  is the one it must **not** weaken — the refusal that stands until CSR11.
- **Mutation to hold it:** drop `newReviewCmd()` from the list → the registration test and the reference
  check both fail; hand-edit the page instead of regenerating it → the generator's check fails on the tree
  it describes.
- **Reviewer tier:** normal, with the plugin and website checks run in the same pull request.
- **OWNER-RUN:** none.
- **Commit:** `review: register the command, its reference rows and the skill's sentence`.

### CSR10. The launcher verifies the verdict before it uses it (SEC)

**Decisions:** **D892 (a)**, **O3** (the framed message the signature is over). **Findings:** **F12**,
**F13**, and [spec.md](spec.md) §6 and §7's chain. **Depends on:** CSR5 (merged — the launcher's reader)
and **CSR3's runner-side half (merged)**: `runner.VerifyReviewArtifact`
(`internal/runner/reviewartifact.go:531`) already reads the sidecar name, refuses an absent signature and
frames the message — **nothing calls it**. **What it needs from C3.9f:** the verify route as deployed
(`POST /v1/review/verify`, `VerifyRequest` at `internal/credsvc/verdict.go:406`) **and a caller the
installation lists**: `AuthConfig.VerifierAccounts` (`internal/credsvc/auth.go:335`) is empty by default,
and an installation with no entry admits no verifier at all. **The verifier the launcher authenticates as
is not a person** — this is the correction §6 was written with, and it is a fact about the service's own
start-up: every `user:` operator is rendered into the service's `FUGARO_PERSON_ACCOUNTS`
(`deploy/terraform/gcp/modules/installation/credentials.tf:497`, from the same `var.operators` the read
mint's person class is), and `NewAuthenticator` refuses an address that is **both** a person and a
verifier ("a person account is also one of the verifiers, so one caller would be two classes",
`internal/credsvc/auth.go:574`) — so an installation whose `credentials_service.verifier_accounts`
named the owner would not start at all. The identity is therefore a **dedicated verifier service account
the launcher impersonates** — an identity of the installation's own, minted for the service's URL the
same way the job's metadata identity is — and **which identity that is, and who may impersonate it, is
Open item 5**: not this task's to choose, and the reason the sentence that used to stand here ("the
owner's own identity has to be in that list") is wrong.
**No `ee/` boundary applies:** the verifier is core
(`internal/runner` and `internal/cli`), it reaches the service over HTTPS and never by importing it into
the tagged tree, and `internal/eeboundary`'s `TestCoreNeverImportsEE` (`internal/eeboundary/boundary_test.go:926`)
is untouched by anything here.
- **Files:** `internal/runner/reviewartifact.go`, `internal/runner/reviewartifact_test.go`,
  `internal/cli/review.go`, `internal/cli/review_test.go`, **`internal/cli/run_test.go`** (the
  `TestFixBrief*` fixtures write `review.json` into the bucket with **no sidecar beside it**
  (`internal/cli/run_test.go:1900`), which a fail-closed reader refuses: those fixtures gain the sidecar and
  the fake verifier, and a reader that fails closed is exactly what breaks them), plus
  `internal/cli/reviewverify.go` (new)
  **only if** the caller's own client does not fit `review.go`'s shape — the task says which it chose.
  **At most 6 files.**
- **Interfaces:** an implementation of `runner.SignatureVerifier`
  (`internal/runner/reviewartifact.go:223`) — a `ServiceVerifier` beside `ServiceSigner`, posting the
  digest, the signature, the run and the repository to the verify route and reading the one refusal bit
  back — and the launcher's reader turned into a **verifying** reader: `reviewFindings`
  (`internal/cli/review.go:455`) reads `review.json.sig` (`runner.ReviewSignName`,
  `internal/runner/reviewartifact.go:69`) from the same prefix it reads the artifact from, verifies the
  **bytes it read** (the digest is over `review.json`'s exact bytes, so the review's prose never travels
  to the service and nothing is re-encoded), and **fails closed**: no sidecar, no service, a reachable
  service that answers `valid: false`, an answer that is not this route's, or a digest that does not
  match — each refuses the launch with the review run named, and **no field of the review's own prose is
  ever printed as a reason** (spec §7). The launcher's identity token is minted for the service's URL from
  the identity of Open item 5 (a verifier service account it impersonates), which is the launcher-side
  twin of the job's metadata identity and **not** the launcher person's own address — see the class
  refusal in **Depends on** above. Two things the verify call needs and this section had not said:
  - **the account the call names** (`VerifyRequest.Account`, which selects **which key** the service
    recomputes under, `internal/credsvc/verdict.go:406`) has **one source: the account of the review job
    the launcher itself resolved**. That is the axis account `reviewJobFor` (`internal/cli/review.go:240`)
    reads back out of `runner.ProjectJobOfPolicy` (`internal/runner/projectjob.go:201`) and hands to
    `projectJobReviewFor` (`internal/cli/project_job.go:351`) — the name the deployer built the variant's
    job from and the one the service keys its own `ReviewJobAccount.Account` and the sign route on
    (`internal/credsvc/review.go:113`: **never empty**, the axis's empty account spelled
    `config.DefaultClaudeAccount`). The launcher **saves it where it saves the rest of the launch: the
    review run's own task**, whose `agent.oauth_token` (`internal/task/task.go:169`) *is* that account and
    whose **absence is the default** — the field may never spell `default` itself
    (`internal/task/task.go:1094`) — so the fix-round launcher reads the review run's task with
    `ReadTask` (`internal/runstore/runstore.go:1283`, which validates what it reads), maps "" through the
    rule the launch already prints with (`reviewAccount`, `internal/cli/review.go:179`), and puts
    `config.DefaultClaudeAccount` on the wire: `readVerifyRequest` refuses an empty account as a bad
    **call**, not as a `valid:false` about an artifact.
    **`oauth_account` in the run's record is deliberately not that source**
    (`internal/runstore/runstore.go:656`): it is documented as informational, never read by anything that
    makes a security decision, and the **job** writes it itself from the credential name the service
    answered with (`recordCredential`, `internal/runner/runner.go:808`) — so it may be "" or a credential
    name, and a value that is neither the account nor always there cannot key a verify call.
    **And never the artifact**, which has no account field at all by design: an artifact that could name
    the account whose key verifies it would choose its own verifier. A review run whose task cannot be read
    refuses the brief (fail closed), and the account the launcher's **own** run uses is irrelevant to the
    call: the fix round's account and the review's are different axes and the verify is about the review's.
    Naming the wrong account cannot pass and does not fail open: the field selects a key and never a
    validity (`VerifyRequest`'s own comment, `internal/credsvc/verdict.go:406`), so anything other than
    the review's own account answers `valid:false` and the brief is refused.
  - **the head the call demands** (`ReviewExpectation.HeadSHA`, compared against the artifact's own
    `head_sha` by `VerifyReviewArtifact`, `internal/runner/reviewartifact.go:531`): the launcher **reads
    the pull request's current head from the forge** with the provider's own
    `FetchPRHead(ctx, number)` (`internal/gitprov/gitprov.go:344`, implemented by github, bitbucket and
    the fake and called by **nothing in `internal/cli` today**), opened through the launcher's existing
    provider seam (`OpenProvider`, `internal/cli/api.go:248`) — **a read this task adds**. The other
    reading — skip the head check — is refused here on purpose: it would admit **a replay**, a validly
    signed verdict for an **earlier head of the same pull request**, which §6 refuses and which §8 words
    as the window a concurrent writer leaves open — "a concurrent job on the same shared job, which can
    list `runs/` and write under the prefix it sees" (`docs/cloud-senior-review/spec.md:982`), **closed by
    the signature** (`:983`). A head the forge cannot be asked for (a closed pull request, a provider that
    refuses the read) refuses the brief rather than passing it.
  - **the verdict policy the owner answered (Open item 2, 2026-11-05)**: the reader refuses an artifact
    whose verdict is **not `fix first`**, in one sentence that names the verdict word, the review run and
    `fugaro review show RUN` as where the findings are read. The set is `DecodeReviewArtifact`'s own closed
    one (`reviewVerdicts`, `internal/runner/reviewartifact.go:98`, held at `:647`), so the task names the
    three words rather than a new vocabulary: **refused — `ship`** (nothing to fix: the reviewer says the
    pull request is ready as it stands) **and `reject`** (the verdict says not to fix it), **accepted —
    `fix first`** (the one verdict that asks for a fix round). A pass-through would build a fix round out
    of a verdict that asked for none, which is the reading the owner refused; a person who wants the
    findings of a `ship` or a `reject` reads them with `fugaro review show`, which prints the artifact
    unchanged and carries no brief. The refusal quotes **the verdict's own word and no prose** (spec §7's
    rule, as above).
- **Command:** `GOFLAGS=-p=2 go test ./internal/runner/ -run 'TestServiceVerifier|TestVerifyReviewArtifact' -count=1`
  and `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestFixBrief' -count=1`.
- **Tests:** `TestFixBriefRefusesAVerdictWithNoSignature` (the sidecar is absent: the launch is refused —
  the red step), `TestFixBriefRefusesASignatureTheServiceDoesNotOwn` (a fake verifier answering invalid),
  `TestFixBriefRefusesWhenTheServiceIsUnreachable` (fail closed rather than fail open: a fix round is not
  launched on an unverified artifact), `TestFixBriefVerifiesTheBytesItReads` (the digest the fake service
  was asked about is the SHA-256 of the stored bytes, byte for byte, and a re-encoded artifact does not
  verify), `TestFixBriefVerifiesADefaultAccountReview` (**the account's second direction**: a review run
  whose task carries **no** `agent.oauth_token` — the unsuffixed default, which is how that field spells it
  — is verified with the call naming `config.DefaultClaudeAccount` and never the empty string, which
  `readVerifyRequest` refuses as a bad call; a review task the launcher cannot read refuses the brief
  rather than guessing an account), `TestFixBriefNamesNoProseFromTheArtifact` (a refusal quotes the review
  run and the artifact's identity fields only — never a summary, a fix or an overview), and
  `TestServiceVerifierReadsOnlyTheRefusalBits` (an answer that is not the route's own is refused rather
  than read generously), `TestFixBriefRefusesAVerdictThatAsksForNoFix` (a **verified** artifact whose
  verdict is `ship` or `reject` refuses the brief, and the sentence names the verdict word and the review
  run and quotes none of the review's prose), and `TestFixBriefBuildsTheBriefForFixFirst` (the one accepted
  verdict: `fix first` reaches `runner.FixFindings` with the artifact's issues, so the refusal above is a
  policy and not a reader that refuses everything — the second direction of the red step). All hermetic: a
  fake service over `httptest`, no live call.
- **Red step:** `TestFixBriefRefusesAVerdictWithNoSignature` fails because the artifact is decoded and its
  findings rendered with no signature read at all: today a review the service never signed becomes a fix
  round's brief, and `TestFixBriefRefusesAVerdictThatAsksForNoFix` fails for the same reader's second half —
  a `reject` today becomes a brief to fix what the verdict said not to. **SEC flag:** yes — this is the link
  that makes a verdict a verdict, and the task's whole
  risk is a reader that verifies *something* (the wrong bytes, the object rather than the file, the run's
  claim about itself) and reports success.
- **Mutation to hold it:** verify a decoded-then-re-encoded artifact → `TestFixBriefVerifiesTheBytesItReads`
  fails; treat a missing service as "nothing to check" → `TestFixBriefRefusesWhenTheServiceIsUnreachable`
  fails; print the artifact's summary in the refusal → `TestFixBriefNamesNoProseFromTheArtifact` fails;
  pass a verified `reject` through to `FixFindings` → `TestFixBriefRefusesAVerdictThatAsksForNoFix` fails;
  refuse every verdict → `TestFixBriefBuildsTheBriefForFixFirst` fails; key the call on the run record's
  `oauth_account` rather than the review's own task → `TestFixBriefVerifiesADefaultAccountReview` fails,
  because a record from a run that took no credential carries nothing there and the call then names no
  account at all.
- **Reviewer tier:** **SEC — opus, with the independent review mandatory.**
- **OWNER-RUN:** **no step of this task is a person's**; the one observation it rests on is the owner's —
  a real verify answer from a real installation is the C3.9 live check's `docs/c39-review-identity/live-check.md`
  run, and **no agent performs it**.
- **Commit:** `review: verify a verdict's signature before a fix round's brief carries it`.

### CSR11. Flip `reviewJobAdmitted` — after the owner's live check, and only then (SEC, OWNER-RUN)

**Decisions:** **D893 (c)** — the flip is E7's second half made true by C3.9a–C3.9g, which are **merged**
on the credentials service's side; **O1**; and **D888/O7** for the release it lands in.
**Findings:** F1, F5, **F9**. **Depends on:** **CSR8** (a flipped constant launches a run the container
must be able to place), **CSR9** (the owner's check types the command), **CSR10** (a flip admits a launch
into a reader that now verifies what it reads: §8's window — a concurrent job on the same shared job
writing under `runs/` — is closed by the signature and not by the order of the runs), **CSR12** (the
live-check switch, which this task **removes**), the **owner-run live checks**
below, and **E7's first half** — the service enabled for the installation.
**This task is the one gate in this section, and it is a person's.**
**It is also the task that takes the switch back out**: the environment switch CSR12 adds exists for the
owner's check and for nothing else, so the commit that makes the constant `true` is the commit that
deletes it — a switch that outlives the gate it was built for is a hole with no reason left to be open.

- **Files:** `internal/cli/review.go`, `internal/cli/review_test.go`. **2 files.**
- **Interfaces:** `reviewJobAdmitted` (`internal/cli/review.go:39`) becomes `true`, with its comment
  rewritten to say **what made it true** — C3.9's merge, the live check's number and date, and the release
  the variant deploys in — rather than deleted: the next reader's question is why a constant that was
  `false` for a reason is now `true`, and the answer is not in the code. `checkReviewJobAdmitted`
  (`:217`) keeps its sentence for the one case that remains: an installation whose service is **not**
  enabled is refused by `checkReviewService`, and the command says which half is missing, exactly as CSR2
  wrote it. The test seam (`review_test.go` sets the constant to reach the launch) stays, because the
  other refusals still need covering. **The switch comes out in this commit**: CSR12's variable and the
  one read of it are deleted here, so the launcher's admission decision has exactly one input again — the
  constant — and no environment can skip the refusal this task is the last one to need. `internal/cli` and
  `plugin/skills` never named the variable in the first place (CSR12's own rule), so nothing else moves.
- **How `fugaro doctor` reports E7's two halves, and why it does not report this constant.** The two
  facts are **CSR6's**, and this task references them rather than adding a third: **is the credentials
  service enabled for this installation**, and **does it admit the review job's own account**. The
  constant is deliberately **not** a doctor fact: a launcher cannot ask the service its configuration
  (there is no admin route, D143) and cannot see the admission decision without making a call that mints a
  credential, which is why it is a constant in the first place. So after this flip `doctor` reports the
  same two facts it reports today, and the honest consequence is stated in its own words: **a green
  `doctor` is the two halves, and the release notes are where the constant's state is written** — a
  binary older than the flip keeps refusing whatever the installation says.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestReviewAdmitted|TestReviewStillRefuses|TestReviewReadsNoAdmissionSwitch' -count=1`.
- **Tests:** `TestReviewLaunchIsAdmittedWhenTheConstantIsFlipped` (the release's own state: the launch
  proceeds and the two halves are still checked first — the constant removes one refusal, not the others),
  `TestReviewStillRefusesWhenTheCredentialsServiceIsOff` (E7's first half, unchanged by the flip), and
  `TestReviewReadsNoAdmissionSwitch` (**CSR12's switch is gone**: with the test seam back at `false` and
  `FUGARO_REVIEW_SKIP_ADMISSION_CHECK` set in the environment, the launch is refused **exactly as with it
  unset** — which is what "no code path reads it" means once the constant is `true`; the mutation is
  re-adding the read, and the test is the one that would catch it). **What no test here can observe, and
  the plan says so rather than writing one that pretends to:** whether **the service** admits the review
  job's own caller. That answer is a 401 inside a job on a live installation, and the only observation of
  it is the owner's C3.9 check — which is why the check comes first and why the constant is not a claim
  the code can make. The hermetic half is the half this task can hold: the two refusals above, and the
  switch's absence.
- **Red step:** `TestReviewLaunchIsAdmittedWhenTheConstantIsFlipped` fails while the constant is `false`:
  a review cannot be launched at all, and the command says so. **SEC flag:** yes — a wrong flip launches a
  job the service answers 401 (a run that spends and fails), and a flip ahead of CSR8 leaves a container
  that halts at admission with a refusal nobody can act on. **Neither is a code defect this task can test
  away**, which is why the gate is a live check rather than a test.
- **Mutation to hold it:** flip the constant **without** CSR8 → the launch is admitted and the run halts
  at admission (the mutation is the red step of CSR8, and this is why CSR8 precedes it); make the flip
  skip the two halves → `TestReviewStillRefusesWhenTheCredentialsServiceIsOff` fails; **leave CSR12's
  switch in the tree** (delete the constant's flip and keep the read) → `TestReviewReadsNoAdmissionSwitch`
  fails, because the variable is read again.
- **Reviewer tier:** **SEC — opus, with the independent review mandatory**, and the reviewer is asked for
  the one thing the tests cannot give: whether the check that justified the flip is recorded where the
  next reader will find it.
- **OWNER-RUN: yes, and no agent performs any step of it.** The owner runs, in their own terminal, on
  their own installation:
  1. **the two halves first** — `fugaro doctor` (the credentials service enabled; the review job's own
     account admitted) and, if the service is not enabled, `fugaro update --provision-credentials`
     followed by `fugaro creds set github-app-key` — the two *commands* this design asks a person for, both
     the credentials service's own documented steps — **and then the third thing E7 needs, which is not a
     command**: `credentials_service.verifier_accounts` must name the identity **Open item 5** settles (a
     verifier service account, **never a person**: the service refuses to start with an operator's own
     address in both lists, §Depends on in CSR10). Until that name is published, no verify call — the
     owner's or the launcher's — is admitted, and CSR10's reader refuses every brief;
  2. **C3.9's live check, in full**: `docs/c39-review-identity/live-check.md`'s eleven steps, whose step 3
     is `fugaro review --pr <N>` — the launch this section exists to make possible, and the one step that
     cannot be reached while the constant is `false`. **The switch of CSR12 is how it is reached** (Open
     item 1's answer of 2026-11-05); **step 8 of that file still writes the owner's own identity into the
     verifier list, which the service refuses to start with** — the check is corrected to Open item 5's
     identity by CSR12, the task that touches the file, and **an agent edits neither the check nor the
     service's configuration**;
  3. **Check 125's steps 6 and 7** on the review-only variant: the mutation check under the fence
     (`unshare -rn`, and the private `/proc` form) — step 7 is the re-confirmation U7's caveat 4 asks for
     **because the variant is a different execution environment from the coding job the probe ran in**;
  4. **then the flip lands as this task's commit**, quoting in the pull request the check's number and the
     date, which is what the constant's comment is rewritten with.
  **An agent never runs any of the four**: not the opt-in, not the live check, not the fence probe, and
  not the flip on the strength of a claim that the others passed. The credentials service's own rule is
  explicit that `fugaro update --provision-credentials` and `fugaro creds set` are refused inside an agent
  or a run, and this task does not work around a refusal.
- **Commit:** `review: admit a review launch, after the live check the flip names, and take the check's switch back out`.

### CSR12. The live-check switch: a launch that starts, and admits nothing (SEC)

**Decisions:** **the owner's answer of 2026-11-05 to Open item 1** — the **environment switch**, and
explicitly **not** that item's recommendation — together with **D893 (c)** and Global Constraint 6 / C3.9.
**Findings:** none of the reconciliation's; this task answers **gap 1** of §6's own table and the owner's
answer to Open item 1, and it is written here rather than left as a paragraph because a switch that a
later commit must remove is a change with its own red step, its own tests and its own review.
**Depends on:** **CSR10** (both hold `internal/cli/review.go`; the switch is level 2's) and, for its
**purpose**, **CSR8** and **CSR9** — the check it exists for types the command, and the run it starts has
to be placeable. **What it is, said so that no reader can take it for more:** the refusal it skips
(`checkReviewJobAdmitted`, `internal/cli/review.go:217`) is a **convenience — failing early** — and
**not the security boundary**. The boundary is the credentials service, which still answers **401** to a
review caller it does not admit (Global Constraint 6, C3.9). **The switch lets a launch start; it makes
the service admit nothing.**

- **Files:** `internal/cli/review.go`, `internal/cli/review_test.go`, and
  `docs/c39-review-identity/live-check.md` (the switch's **only** documentation outside this plan: step 3's
  note carries the variable, that it is off by default, that a coding agent's session is refused it, and
  what it does and does not do. The same task writes **step 8** to Open item 5's identity — **until that
  item is answered, step 8 names the owner's own address and the service refuses to start with it**, so
  the check cannot pass as the file stands). **3 files.**
- **Interfaces:** one environment variable, **`FUGARO_REVIEW_SKIP_ADMISSION_CHECK`**, read at the one
  place the refusal is, and five constraints, each of which is a test:
  - **off by default**: unset, the refusal is what it is today, and the launch goes on to fail early
    exactly as before;
  - **never read from a config file or the shared config**: the read is `os.Getenv` at the launch and
    nothing else, so no `fugaro.yaml`, no project layer and no local config can set it (the file list says
    it by omission: `internal/config`, `internal/projectpolicy` and `internal/localcfg` are untouched);
  - **refused in a coding agent's session**, by the **existing** path and not by a new one:
    `agentMarker(os.Getenv)` (`internal/cli/initsecrets.go:141` → `agentenv.Marker`,
    `internal/agentenv/agentenv.go:33`) is read **before** the variable is, and a marker present refuses
    with `initflow.AgentRefusal(marker)` (`internal/initflow/stage.go:440`) — the same refusal
    `fugaro creds set` and `fugaro update --provision-credentials` answer with
    (`internal/cli/creds.go:118`). It is **read, not weakened, not bypassed, and not extended**;
  - **one warning line**, printed before anything else the launch prints, naming what the variable is for
    and the boundary it does not cross: the launch is not admitted by it and the service's own answer is
    what decides;
  - **documented in two places only** — this plan and that live check — and **never** in
    `website/content/cli-reference.md`, `fugaro settings`' surface or the plugin skills: the generated
    reference is regenerated by CSR9 from a tree this variable must not reach.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestReviewSwitch|TestReviewStillRefusesWithoutTheSwitch|TestTheAdmissionSwitchIsDocumentedWhereThePlanSays' -count=1`
  and `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`.
- **Tests:** `TestReviewSwitchAdmitsTheLaunchAndSaysSo` (with the variable set and the constant `false`
  the launch proceeds, **exactly one warning line is printed and it is the first line**, and the launch
  resolution is otherwise unchanged — the red step), `TestReviewSwitchIsRefusedInAnAgentSession` (a
  marker present refuses the launch **with the variable set**, in `AgentRefusal`'s own sentence),
  `TestReviewStillRefusesWithoutTheSwitch` (neither the variable nor a marker: the refusal is byte for
  byte today's), `TestReviewSwitchIsNotReadFromConfig` (a config file and a project layer carrying a value
  of that name admit nothing: the read is the environment's alone),
  `TestTheAdmissionSwitchIsDocumentedWhereThePlanSays` (the name appears in
  `docs/c39-review-identity/live-check.md` and in **this plan**, and **nowhere else** under `docs/`,
  `website/` or `plugin/` — a reference row or a skill sentence fails it),
  and `TestReviewReadsNoAdmissionSwitch` (**CSR11's own test, named here because the two tasks meet on
  this one assertion**: with the constant flipped and the variable set, the launch is refused exactly as
  with it unset — the switch's reader is gone, and re-adding it is what fails this test).
- **Red step:** `TestReviewSwitchAdmitsTheLaunchAndSaysSo` fails because nothing reads the variable: the
  launch is refused by the constant, which is gap 1 and the whole reason C3.9's step 3 cannot be reached.
- **SEC flag: yes — with an explicit conclusion, because a deliberate hole needs one.** The task's own
  security review states **what an attacker who can set an environment variable on a launcher's machine
  gains**, and it answers **both states of the installation**, because a conclusion that only holds while
  E7 is unmet would be a conclusion about today rather than about the switch. The launcher's early refusal
  is not the control in either: the credential, the read-only token and the model call are the service's
  answers, and the service admits the caller or it does not (Global Constraint 6, C3.9).
  - **E7 is not enabled** — the service admits no review caller, which is the state gap 1 describes and
    the state this task's own live check cannot pass in: what the attacker gains is **a launch the service
    then refuses** — the **spend of a job that dies at its first call**, bounded by
    `budget.per_run_usd` and the job's own timeout — and **no credential, no read and no write they did
    not already have**, since setting the launcher's environment already means controlling the launcher
    (and every other variable it reads).
  - **E7 is enabled and the constant is still `false`** — the state the owner's own live check runs in,
    because the service admits the review caller and the launcher's constant is the one gate left: the
    switch then yields **a working review launch**, end to end — the account's model calls, the read-only
    installation token and a signed verdict all answer, because the service's answer is yes. What the
    attacker gains there is **a review run of a pull request of the repository and the verdict artifact it
    writes: the whole of what the flip (CSR11) makes unconditional, one gate early — and nothing the
    launcher could not already do.** It is not more than that: controlling the launcher's environment
    already means controlling the launcher, which can start any run the repository's configuration admits,
    and the review's own reach (the read-only token, the model calls, one write under its own prefix) is
    the service's answer **to the caller**, not to the switch — the switch changes no credential, no
    admission and no scope, and it is refused in an agent's session like every other launcher-only door.
    A review of a pull request is in any case one `fugaro run --pr N --recipe <the review recipe>` away
    today (`checkReviewJobAdmitted`'s own sentence names that as how a review is run until the flip), and
    the signed verdict the switched launch produces is exactly the artifact CSR10's reader must accept
    **after** the flip — so the second state is the flip's own capability, not a new one.
  So the switch is a convenience in both cases and the boundary in neither. **The one real cost is a
  spend nobody asked for — plus, in the second state, a review the flip was going to allow anyway — and
  that is why the flip removes the switch rather than keeping it.**
- **Mutation to hold it:** read the value from the config instead of the environment →
  `TestReviewSwitchIsNotReadFromConfig` fails; honour it with a marker present →
  `TestReviewSwitchIsRefusedInAnAgentSession` fails; print nothing when it is honoured →
  `TestReviewSwitchAdmitsTheLaunchAndSaysSo` fails; name it in the generated reference →
  `TestTheAdmissionSwitchIsDocumentedWhereThePlanSays` fails; keep the read after the flip →
  `TestReviewReadsNoAdmissionSwitch` fails.
- **Reviewer tier:** **SEC — opus, with the independent review mandatory** (the repository's rule for a
  change on the admission path; this one is a hole with a stated expiry date, and the reviewer is asked
  for that date's enforcement in CSR11).
- **OWNER-RUN:** **none of the change is a person's**; the **use** is, and it is CSR11's step 2 — the
  owner's own terminal, the owner's own installation, no agent.
- **Commit:** `review: the live-check switch — a launch that starts, and admits nothing`.

### 6.3 The edges, the levels and the critical path

```
CSR8  (the runner places the variant) ───┐
CSR9  (the command is registered)     ───┼──▶ CSR12 (the live-check switch) ──▶ [the live checks: C3.9's
CSR10 (the launcher verifies)         ───┘        (a launch starts; the service      eleven steps, Check 125's
   ▲   (the three are parallel, and                admits nothing)                    6 and 7]  (a person's,
   │    no level holds two writers)                                                    and no level holds it)
   │                                                                                          │
   └── CSR10 → CSR11: a flip admits a launch into a reader that verifies (§8's window)         ▼
                                                                                    CSR11 (the flip, and the
                                                                                    switch's removal)
```

- **Edges.** `CSR8 → CSR11` (a flip without the placement is a launch into a halt). `CSR9 → CSR11` (the
  check types the command). **`CSR10 → CSR11`** (the flip admits a launch that a fix round then reads:
  [spec.md](spec.md) §8's window is a concurrent job on the same shared job writing under `runs/`, and
  **the signature is what closes it**, so a flip ahead of the verifier is the ordering the design already
  refuses). **`CSR8 → CSR12`, `CSR9 → CSR12` and `CSR10 → CSR12` are not edges of the code**: the switch's
  own change touches one file, and what needs the other three is the **check** it exists for, which is the
  node the three feed. `CSR12 → CSR11` (the switch exists only for the check, and the flip's commit is the
  one that removes it). `CSR8 ‖ CSR9 ‖ CSR10` — **three parallel tasks and not a chain**: the runner's
  placement, the launcher's registry and the launcher's reader touch three disjoint sets of files (§6.4),
  and **none of the three may be serialised behind another on the strength of this drawing**.
  `CSR11 → nothing`: it is last, and nothing here depends on the flip.
- **Levels.** **Level 1: CSR8, CSR9, CSR10** — none depends on another, and no two of them holds one
  file (§6.4). **Level 2: CSR12** — the switch, after CSR10, because both hold
  `internal/cli/review.go` and level 1 has one writer of it (the verifying reader). **Level 3: CSR11** —
  after all four, and after the live checks, which are a person's and which no level holds.
- **Critical path.** `the longest of (CSR8, CSR9, CSR10) → CSR12 → the owner's live checks → CSR11`: four
  tasks and one person's afternoon, and **the longest wait in it is not code** — it is the live check,
  which needs the credentials service enabled on a real installation (E7's first half), the
  `verifier_accounts` name of Open item 5 published beside it, and an owner at a terminal. That
  is the honest critical path of a design whose last link is another work stream's, and it is why §5 says
  the release ships before the chain works.

### 6.4 Shared files, by level: no level holds two writers of one file

The rule is §2's, applied to the five tasks above: a file is one writer's at a time, and **within a level
no two tasks may hold one**. Every file below belongs to exactly one task in exactly one level:

| File | Level 1 | Level 2 | Level 3 | Note |
| --- | --- | --- | --- | --- |
| `internal/runner/projectjob.go`, `internal/runner/projectjob_test.go` | CSR8 | — | — | the candidate set, the class and the token axis |
| `internal/runner/project.go`, `internal/runner/project_test.go` | CSR8 | — | — | the admission path |
| `internal/config/claude_accounts.go`, `internal/config/claude_accounts_test.go` | CSR8 | — | — | the axis mapping lands once, here (CSR7 and E1 have merged; this is the file after them) |
| `internal/cli/root.go`, `internal/cli/root_test.go` | CSR9 | — | — | the registration |
| `website/content/cli-reference.md` | CSR9 | — | — | **generated** by `go run ./ee/clidoc` |
| `plugin/skills/working/SKILL.md` (+ `routing/SKILL.md` if it names the flow) | CSR9 | — | — | the sentence and the registration are one commit |
| `internal/runner/reviewartifact.go`, `internal/runner/reviewartifact_test.go` | CSR10 | — | — | the verifier's implementation |
| `internal/cli/run_test.go` | CSR10 | — | — | the `TestFixBrief*` fixtures gain the sidecar a fail-closed reader needs |
| `internal/cli/review.go`, `internal/cli/review_test.go` | CSR10 | **CSR12** | **CSR11** | the verifying reader, then the switch, then the flip that removes it — **three levels, one writer each** |
| `internal/cli/reviewverify.go` (new, only if CSR10 chooses it) | CSR10 | — | — | — |

**`internal/cli/review.go` is the one file three tasks want, and that is why their order is a rule rather
than a preference:** the verifying reader (CSR10) writes it in level 1, the live-check switch (CSR12)
adds and reads one variable in level 2, and the flip (CSR11) sets the constant and **deletes the switch**
in level 3. Serialising three writers of one file is what the levels buy, and it is also why CSR12 exists
as its own task rather than as a paragraph of CSR11: a switch that has to be removed by a later commit is
a change with its own red step, its own tests and its own review, and the reviewer of the flip is the
reader who has to see the removal in the same diff.

**Two files this section does not take, deliberately.** `internal/cli/recipes_skew.go` and
`internal/cli/validate.go` remain **CSR6's** (§2), and no task here books them: CSR8–CSR12 add **no
`fugaro.yaml`, task, recipe or wire key**, so the `…Since` rule (E6) gives them nothing to gate. The one
constant whose *value* this section's release moves — `reviewJobSince`
(`internal/cli/recipes_skew.go:379`, today `"0.19.0"`) — is **not** moved by CSR8–CSR12: it is the release
slot's consequence, and **Open item 3's answer of 2026-11-05 is 0.20.0 "Quokka"**, so the release's own
change moves it to `"0.20.0"` and takes that file alone under §2's rule.

### 6.5 The release, the HARD RULE and the operator step

- **The HARD RULE holds, and these five tasks do not add a step to it.** `fugaro update` is the whole
  upgrade: the command's registration, the reference rows, the plugin's skill text, the runner's placement
  and the launcher's verifier all ride the **image refresh** `update` already asks about (CSR6's), and none
  of them is a second command, a deletion or a manual repair. The plugin pins move through
  `scripts/bump-plugin-version.sh` with the release, not by hand and not per task. **CSR12's switch adds
  nothing to `update` either**: it is an environment variable read only in the live check's own session,
  it is off by default, it is never written to a config file, and CSR11 takes it out.
- **The release's `### For operators` entry is conditional, and it names three things, not one.** The one
  sentence applies to the installations that **opt in to the credentials service**: the cloud senior
  reviewer's read-only token and its model calls both come from that service (D893 (c), O1), so an
  installation without one has the command, the variant and the verifier and still cannot run a review —
  and says so in the refusal rather than in a step. For an installation that **does** opt in, the same
  entry names the three halves of E7 as they are: `fugaro update --provision-credentials`, `fugaro creds
  set github-app-key`, **and `credentials_service.verifier_accounts` naming the identity Open item 5
  settles** — the third is a published value and not a command, and without it the launcher's verify call
  is admitted for nobody (Open item 5, §6.5's own list below). An installation that never opts in
  converges to the new job set and behaves exactly as it does today: **no operator step, and nothing to
  uninstall.**
- **Release actions, for the 0.20.0 "Quokka" release Open item 3 answered:** the release's own
  `fugaro update`, that one conditional operator sentence, and **`reviewJobSince` moved to `"0.20.0"` in
  the release's own change** (the file is CSR6's, and the release takes it as §6.4 says). **CSR11's flip
  is not a release action**: it is a commit that lands after the owner's live check, in whichever release
  carries it, and it is the commit that takes CSR12's switch out.
- **The verifier identity is the release's one deployment-shaped fact, and the plan does not settle it.**
  `credentials_service.verifier_accounts` is a **bare-address list rendered into the service's own
  environment** (`deploy/terraform/gcp/modules/installation/credentials.tf:480`, from the optional
  variable of the same module) — the eighth name on a service whose seven are the rest — and **the
  account named may not be a person**: every `user:` operator is already in
  `FUGARO_PERSON_ACCOUNTS` (`:497`) and `NewAuthenticator` refuses one address in two classes
  (`internal/credsvc/auth.go:574`), so the entry is a verifier service account and **Open item 5** says
  which one and who may impersonate it.

### 6.6 Open items — the owner's, and none of them decided here

**1. How the owner reaches the launch the live check's step 3 needs, while the command refuses. —
ANSWERED 2026-11-05: an ENVIRONMENT SWITCH, and not this item's recommendation.** C3.9's check
(`docs/c39-review-identity/live-check.md`) launches `fugaro review --pr <N>`, and the constant refuses
every launch (gap 1). Three readings stood here; the owner took **the third**, knowingly, with the
security consequence this item wrote down. **Why, in one line:** the recommendation (a dev build with the
constant edited to `true`) leaves the only thing between a tree and the flip in a person's memory — an
uncommitted edit — and makes the check run on a binary that is not the released one, while a switch is a
change that **ships, is tested, and is deleted by the flip itself**, so the hole has an expiry date in the
repository rather than in someone's discipline. **The answer is CSR12**, and its constraints are that
task's: one named variable (`FUGARO_REVIEW_SKIP_ADMISSION_CHECK`), off by default, refused in a coding
agent's session by the existing `agentMarker`/`AgentRefusal` path (**read, not weakened**), one warning
line when it is honoured, **never** read from a config file or the shared config, documented only in the
live check and that task, and **removed by CSR11 in the same commit as the flip**, with a test that the
read is gone. **The security consequence this item named is answered in CSR12's own SEC paragraph, and the
answer is the sentence the item asked for:** the refusal is a **convenience — failing early — and not the
security boundary**; the boundary is the credentials service, which still answers 401 to a review caller
it does not admit (Global Constraint 6, C3.9). **The switch lets a launch start; it makes the service
admit nothing.** What an attacker who can set an environment variable on a launcher's machine gains is
**a launch the service then refuses** — a spend bounded by `budget.per_run_usd` and the job's timeout, and
no credential, no read and no write they did not already have, since setting that machine's environment
already means controlling the launcher. **With E7 enabled and the constant still `false` — the state this
item's own check runs in — the same switch yields a working review launch instead, and what it gains is
the flip's own capability one gate early: a review run and its signed verdict, and nothing the launcher
could not already do.** Both states are spelled out in CSR12's own SEC paragraph, because a conclusion
that held only while the service refuses its caller would be a conclusion about today rather than about
the switch.

**2. Whether a verdict policy applies in the launcher. — ANSWERED 2026-11-05: the launcher's own rule
(this item's recommendation).** `reviewFindings` renders `art.Issues` into the brief **whatever the
verdict is** (`runner.FixFindings(ref, art.Issues)`): today a `reject` artifact still becomes a fix
round's brief, and a `ship` with findings does too. The owner took the reading in which **a verdict the
launcher reads is a verdict the launcher acts on**. **Why, in one line:** a brief built from a verdict
that asked for no fix is a fix round nobody asked for, and nothing is lost by refusing it — the findings
of a `ship` or a `reject` are still one `fugaro review show RUN` away, so the refusal costs an operator
nothing they cannot get back, while a brief that fixes what the reviewer said not to is work spent on the
wrong change. **The answer is CSR10's reader**, and the plan names the exact words rather than a new
vocabulary: `DecodeReviewArtifact`'s closed set (`internal/runner/reviewartifact.go:98`) gives **`ship` and
`reject` refused, `fix first` accepted** — refused in one sentence that names the verdict word, the review
run and `review show`, and quotes no prose (spec §7) — with the test, the second direction and the
mutation check in CSR10's own bullets. The verification half stands either way: an unverified artifact
never becomes a brief.

**3. The release that carries this. — ANSWERED 2026-11-05: 0.20.0 "Quokka"** (this item's
recommendation). **Why, in one line:** the 0.19.0 release in flight carries none of this work, and a
release that carries the flip has to carry the tasks that make the flip work — the placement, the
registration, the verifier and the switch's own removal. Two consequences, settled together as the item
said: the release note's `### For operators` sentence is written for that release (§6.5), and
**`reviewJobSince` moves to `"0.20.0"`** in that release's own change — `docs/release.md:165`'s bullet
verifies it against the tag
actually cut, `internal/cli/recipes_skew.go:379` holds it today as `"0.19.0"`, and a wrong value refuses
too late (a launch names a variant the installation has not deployed) or too early (an installation that
holds one stays blocked). The release's own change takes that file alone, under §2's rule and §6.4's
note.

**4. *(found while writing this section)* The review run's model path.** O1 says the reviewer's model
calls go through the credentials service's review class, and that class exists on the service's side
(`internal/credsvc/cascade.go:105`, merged with C3.9a–C3.9g). **The runner side has no arm for it.** The
gateway — the only thing that can forward to the service — is enabled by `GatewayOn`
(`internal/runner/routing.go:158`) only for `api-key`, `vertex` and `cascade`; the service is reached only
from the cascade arm; and a run whose `agent.auth` is `cascade` is refused in bootstrap by O2's arm
(`internal/runner/routing.go:60`) unless the **operator publishes `routing.claude: {via: cascade}` and
`budget.allowed_models`** — the two things [spec.md](spec.md) §2 says a review job does not need, because
that switch is the operator's consent for a *coding* run's cascade while a review job's credential is
decided by its own class. So as the tree stands, a review run reaches a model only by an operator writing
the coding cascade's consent for it. Two readings:

- **an operator step**: a review-enabled repository writes `agent.auth: cascade` and the project policy
  publishes the switch and the allow-list. Cost: the repository's own file changes and the project's
  policy changes; the security consequence is that the switch's **meaning is stretched** — it becomes the
  consent for a review job's credential too, while spec §2 says the review needs no such consent, and the
  next reader cannot tell which one the operator meant.
- **a fourth runner task** (the shape of a further task, **not written here** — CSR12 is the switch's own
  number, and this one would be CSR13's): the review job's model credential is keyed on
  its **class** and not on `agent.auth`, so a marked review run reaches the service's review class without
  the operator's coding-cascade switch, and O2's arm does not fire for it. Cost: another SEC task, with the
  independent review mandatory, over `internal/runner/gateway.go`, `internal/runner/routing.go` and
  `internal/runner/agentenv.go` — and a second path through the bootstrap routing decision, which is the
  most carefully-worded ladder in the tree. **This reading is the one O1 describes**, and it is why the
  gap is an Open item and not a note.

**5. *(found while reviewing the first draft of §6)* Who may verify a verdict — and it cannot be the
owner.** §6 was written with the owner's own address as the verify route's caller ("so the owner's own
identity has to be in that list"). **The service refuses to start with it there.** Every `user:` operator
is rendered into `FUGARO_PERSON_ACCOUNTS`
(`deploy/terraform/gcp/modules/installation/credentials.tf:497`, from the same `var.operators` the read
mint's person class is built from), and `NewAuthenticator` refuses an address that is **both** a person
and one of the verifiers — "a person account is also one of the verifiers, so one caller would be two
classes" (`internal/credsvc/auth.go:574`) — so `credentials_service.verifier_accounts` naming the owner is
an installation whose credentials service never comes up. The same conflict stands in the live check:
**step 8** of `docs/c39-review-identity/live-check.md` has the owner's identity in the verifier list and a
hand-made `POST /v1/review/verify` carrying the owner's own ID token. **This section says so and does not
edit that folder's file** (CSR12 is the task that does, when this item is answered). Who may verify is the
owner's to settle:

- **a dedicated verifier service account the installation's root creates**, named in
  `verifier_accounts`, which the launcher (and the owner's own check, with
  `gcloud auth print-identity-token --impersonate-service-account=…`) impersonates, with the operators
  granted the token-creator role on it. Cost: one service account and one binding in the installation
  root; the security consequence is that **anyone who can impersonate it can ask the verify route** —
  which is that route's own design: it answers **one refusal bit**, echoes no key and names no account
  (D1068), so the class's whole risk is the account oracle the design already refused to build.
- **the coordinator's own identity, once the coordinator exists** ([spec.md](spec.md) §2's resident
  service): the verifier class exists for a non-person caller, and the service's own comments say so where
  they refuse a `serviceAccount:` member in the *person* list "because admitting a `serviceAccount:` one
  would admit exactly the non-person caller the class exists to refuse (the coordinator's resident
  service, G4)" (`deploy/terraform/gcp/modules/installation/credentials.tf:73`). Cost: the check's step
  8 then cannot be run by hand at all until the coordinator exists — and **the owner's check is this
  design's gate**, so a verifier identity that only the coordinator holds blocks the gate it precedes.
- **not the review job's own account**: it is already a class of its own (`credsvc`'s review class), and
  one caller in two classes is the refusal above, in the other direction.

**Nothing above is decided here except what the owner answered on 2026-11-05 — items 1, 2 and 3, each in
its own words above — and nothing above is built.** Items **4** (the review run's model path) and **5**
(who may verify) are **open**, and no task in this section chooses them. CSR8–CSR12 are tasks in this
plan's sense:
a red step, a file list, named tests and a person's gate where a person's gate is what the work needs.
