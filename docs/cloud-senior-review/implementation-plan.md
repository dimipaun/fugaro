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
must both hold before the chain works, and only one of them is an operator's own step; the other was
code the service did not have when this was written, and it has since landed (§6's CSR11 is the flip it
unblocked):

1. the service **enabled for the installation** (`fugaro update --provision-credentials`, the App key
   in the bundle, plus `fugaro creds set github-app-key`) — the owner's own terminal, and expressible
   in `fugaro update` terms;
2. the service **admitting the review job's own account**, which is a **project-scoped** job and which
   the service refused while this was written (**Global Constraint 6**; **C3.9**, Slice 3, was the task
   that lifted it, and the routing design **#1514** splits it into **C3.9a–C3.9g**) — **no `fugaro
   update` could supply this half while it was missing**, because it was code in the service rather than
   a deployment step. **It is in the tree now**: the service reads `FUGARO_REVIEW_JOB_ACCOUNTS`
   (**CSRW1**, PR #1932) and the installation module renders it (**CSRW2**, PR #1934), which the same
   `fugaro update` applies. The work carries the **review class on `/v1/messages`** that **O1** routes
   the reviewer's model calls through, admitted for the review run's account alone.

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
  would not admit the review job's own account (a project-scoped job; **Global Constraint 6**, C3.9 —
  then unscheduled, §1), naming **which half** is missing rather than reporting one "not configured". The
  refusal is a sentence the owner can act on: half one is `fugaro update --provision-credentials` plus
  `fugaro creds set github-app-key`; half two is not reachable from any `fugaro update` and the
  refusal says so — **CSR11's flip of 2026-11-08 rewrote that half, because the admission is the
  BINARY's**: what supplies it is upgrading Fugaro and running `fugaro update` with the newer binary,
  and the sentence now names both rather than sending an operator after a command that does not exist.
  **Until E7 was met, this task could not launch a review job at all** — it was written, gated and
  shipped, and the launch was what failed; **it launches now**, and what is still unrun is the owner's
  live check rather than the code.
- **Tests:** `TestReviewLaunchesOnTheNamedAccount` (the task names the account, the launch targets
  that account's **review-only job entry** and never the shared one), `TestReviewRefusesAnAccountTheRepoMayNotUse` (F4's rule, reused),
  `TestReviewRefusesAnExhaustedAccount` (D882: refused with the mark's reset time, not retried),
  `TestReviewOnAReadOnlyTokenPostsNoComment` (the job's record carries the verdict and the provider
  fake sees no `Comment`), `TestReviewRefusesWhileTheCodingRunIsGoing`,
  `TestReviewShowPrintsTheArtifactAndItsMarkdown`, and
  `TestRecipesSkewRefusesReviewJobOnAnOldImage` (E6), plus two for E7's two halves:
  `TestReviewStillRefusesWhenTheCredentialsServiceIsOff` and
  `TestReviewStillRefusesWhenTheConstantIsOff` (both hermetic — a fake service and a fake
  installation, no live call; the names are CSR11's, which left the first refusal standing and moved the
  second to the pre-flip binary, where the test seam puts it back).
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
  (`internal/runner/reviewmode.go:264`). **A test that only
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
  project-scoped job was, when this was written, a separate and unscheduled change — a sentence, not an
  operator step, because
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
  **unscheduled** when this was written (the routing design **#1514** splits it into
  **C3.9a–C3.9g**). It is **not** in
  CSR1–CSR7 and cannot be: it is that work stream's task. **Its admission half has since landed** —
  the service reads `FUGARO_REVIEW_JOB_ACCOUNTS` (**CSRW1**, PR #1932) and the installation module
  renders it (**CSRW2**, PR #1934), which the same `fugaro update` applies — and **CSR11** is the flip
  it unblocked (2026-11-08). CSR1 names the read-only set the service must serve, **O1** adds the
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
and unscheduled when this was written (**E7**) — so a release cut before it ships a command that
cannot run. **The admission half has landed since** (CSRW1/CSRW2, §6) and the gate a release waits on
is no longer a task but the owner's live check.

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

**§6's nine follow-up tasks do not move this slot, and they are what a release must carry to be worth
cutting.** CSR8–CSR16 are what makes the command reachable (`fugaro review` was built and registered
nowhere), the container placeable, the reviewer's calls reachable at all and the verdict verified; the
release that deploys the variant is also
the release `reviewJobSince` must name, and **Open item 3's answer of 2026-11-05 settles it as 0.20.0
"Quokka"** — the 0.19.0 release in flight does not carry this work. **The constant itself waited for
CSR11's flip**: it named `"0.19.0"` while `reviewJobAdmitted` was `false` and this gate was never
reached, and the commit that made it decide anything (2026-11-08) is the one that moved it to
`"0.20.0"` — 0.19.0's image holds the variant and has no model path for a container that carries no
token (§6.5).

## 6. Follow-up after CSR1–CSR7: what still stands between this plan and a review that runs

**Nothing in this section is approved either, and it adds no decision.** It was written on 2026-11-05,
when **CSR1–CSR5 and CSR7 were merged and CSR6 was in flight** (PR #1750), and its whole reason is that
**the merged tasks do not add up to a working review**: the command CSR2 built is registered nowhere, the
constant E7's second half is still `false`, the container cannot place the job the launcher picks, and the
launcher reads a review's artifact without the signature that makes it a verdict. **CSR8 has merged since**
(PR #1761, the placement and the class), **and so have CSR9 (PR #1762, the command registered), CSR10
(PR #1764, the launcher's verifying reader)
and CSR12 (PR #1771, the live-check switch)**, so the paragraph above is a snapshot of 2026-11-05 rather
than the state of the tree today: gaps 2, 3 and 4 below are **closed**, and where the rest of §6 said a task
waits on CSR8 it now says what it reads from the merged code (§6.3, §6.4). A task in §3 that
merged is a task that was built and tested; it is not a task whose work is reachable from the surface a
person types at.

**Seven gaps, each with the owner it has after this section.** Six of them were the state this section was
written against; the seventh was found while writing it and is marked as such, because a gap discovered
while documenting the others is exactly the kind of thing a plan loses by not writing down:

| # | the gap | where it is | the owner |
| --- | --- | --- | --- |
| 1 | a review launch is refused unconditionally: the constant E7's second half is `false` | `internal/cli/review.go:58` | **CSR11 — CLOSED: flipped 2026-11-08.** The constant is `true` and the switch CSR12 added is deleted in the same commit; the flip was **released from its gate by the owner's ruling of 2026-11-07**, because the reason it was held — the service admitting no review caller — is gone (CSRW1, PR #1932, the service reads `FUGARO_REVIEW_JOB_ACCOUNTS`; CSRW2, PR #1934, the installation module renders it) and `fugaro update` deploys the service on every installation (DON1), so the launch is served with no operator step between the flip and it. **The OWNER-RUN live checks below are the owner's and are still unrun**: the flip is not a claim that they passed |
| 2 | `fugaro review` is built and **registered nowhere**, so no released CLI answers to it: `newReviewCmd` is not in the list `newCoreRoot` adds | `internal/cli/root.go:189`, `internal/cli/review.go:81` | **CSR9 — CLOSED: merged as PR #1762.** `newReviewCmd()` is in the list `newCoreRoot` adds (`internal/cli/root.go:198`), so the command answers and its launch refusal is a sentence a person reads rather than a name nobody can type; what CSR11 inherits from it is that refusal alone |
| 3 | the runner cannot place the job the launcher picks: the review variant's name is never a candidate, so a review container halts at admission with the client's `cannotPlaceJob` | `internal/runner/projectjob.go:178`, `internal/runner/project.go:1114` | **CSR8 — CLOSED: merged as PR #1761.** The variant is a candidate and the class is keyed on the run's own mode, not on the job name; what CSR13 inherits from it is a **fact to read**, `r.runClass()` (`internal/runner/projectjob.go:82`) and `jobTriple.Class` (`:50`), and never a mark to wait for |
| 4 | the launcher reads `review.json` and never reads `review.json.sig`; `runner.VerifyReviewArtifact` exists and **nothing calls it**, because no implementation of `runner.SignatureVerifier` is in the tree | `internal/cli/review.go:522`, `internal/runner/reviewartifact.go:675`, `:223` | **CSR10 — CLOSED: merged as PR #1764.** The launcher's reader is a verifying one and `ServiceVerifier` implements `runner.SignatureVerifier`; what CSR15 inherits from it is the seam and the fail-closed refusal in `internal/cli/reviewverify.go` |
| 5 | the owner-run live checks have not been run, and could not be while gap 1 held: the C3.9 check's step 3 launches `fugaro review --pr N`, and Check 125's steps 6 and 7 confirm the fence on the variant | `docs/c39-review-identity/live-check.md`, [spec.md](spec.md) §8 | **the owner**, and **the check is runnable now**: gap 1 closed on 2026-11-08, so step 3 is reached with the plain launch it names — the switch CSR12 added for it (Open item 1's answer of 2026-11-05) is deleted, and the step no longer sets anything |
| 6 | the credentials service **ships dormant**: the opt-in, the App key and the verifier identity are the owner's own terminal (C3.9g1) | `internal/credsvc/auth.go:335` | **the owner** for the opt-in and the App key — no task here; it is the release's one conditional operator sentence (§6.5) — and **CSR14** for the verifier identity, which is a service account the installation creates rather than a value a person types (Open item 5, answered 2026-11-05) |
| 7 | *(found while writing this section)* the review variant holds no model credential (O1) and the service has a review class for its calls, but **the runner has no model path that reaches it**: `GatewayOn` is false unless `agent.auth` is one of its three, the service is reached only from the cascade arm, and that arm is refused in bootstrap unless the **operator publishes the coding cascade switch** — the two things [spec.md](spec.md) §2 says a review job does not need | `internal/runner/routing.go:158`, `:60`, `internal/credsvc/cascade.go:105` | **CSR13** — Open item 4, answered 2026-11-05 with the fourth runner task |

**CSR8–CSR16 follow §3's convention exactly**: decisions and findings named, an exact file list with its
cap, interfaces, named tests, a **red step** quoted in the pull request, targeted hermetic tests
(`GOFLAGS=-p=2`, `-count=1`), a mutation to hold each, the reviewer tier, and `OWNER-RUN` where a step is
a person's. Each is sized for one run of the coding agent this repository is built to drive, and none of
them takes a file another one holds in the same level (§6.4).

**The owner answered all five of the Open items §6 was written with, on 2026-11-05, and this section is
followed through on all five.** Each answer is recorded **in its own Open item**, with the date and the one line of why, and
the rest of §6 is what changed for it: item 1's environment switch is now **CSR12** (a task with the
switch's constraints and its security review) and CSR11 removes it in the same change as the flip —
**done on 2026-11-08, when the owner's ruling of 2026-11-07 lifted the flip's gate**; item
2's verdict policy is now part of **CSR10**'s reader, which refuses an artifact whose verdict is not `fix
first`; item 3's release is **0.20.0 "Quokka"**, and §5, §6.4 and §6.5 name it; item 4's fourth runner
task is **CSR13**, whose class decides the review run's model path rather than `agent.auth`; and item 5's
verifier identity is **CSR14** (the installation root renders the account's name and `fugaro update` writes
it into the local config and publishes it) with
**CSR15** (the launcher impersonates it), because the task CSR10 became — **merged as PR #1764** — leaves
`reviewVerifierIdentity` refusing for want of that answer (`internal/cli/reviewverify.go`). **The answers
are the owner's and this section decides nothing else**: what is left open is the *approval* of §6's
tasks, which §6.6's last paragraph states in its own words — **and the sixth item, found while CSR13 was
re-read against CSR8's merged code, was answered by the owner on 2026-11-05/06**: **§6.6's item 6**, the
budget mode a review run runs under, is answered with **the refusal** — a cloud review does not run with
the installation's budget off, and the sentence the run gets names the setting to turn on — so CSR13
carries it and its named test, and the item no longer blocks. **The owner also cut the api-key review
from 0.20.0 on 2026-11-06** (OpenRouter and Anthropic): 0.20.0 ships the OAuth review alone, and the
api-key path is a named, ordered slice **outside** 0.20.0 — **CSR17 and CSR18, §6.7** — which §6.3's
levels, §6.4's table and §6.5's release notes do not carry.

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
  (`internal/config/claude_accounts.go:156`) and `ReviewVariantSuffix` (`:237`), which is the file that
  already owns the axis: the runner does not import `internal/infra` today and keeping the container out
  of the deployer's package is why it does not start to. `internal/infra`'s own function becomes that
  mapping's spelling. **`checkProjectJobAccount` (`internal/runner/projectjob.go:502`) gains the review
  arm** and it is the half a name alone does not cover: the variant **holds no token** — `config.ProjectJobTokenAccount`
  knows unsuffixed names only, so `serves` is false and an OAuth review run is refused with "this job
  holds no Claude token" — and the run's own credential is not the account the axis
  names — for a review job the account is the **class's**, and the refusal that sentence belongs to
  (a run authenticating as something its configuration did not choose) does not apply to a job whose
  credential the class decides. The arm is keyed on the run's own marker — `r.runClass()`, the **task's
  mode**, and never the environment marker `FUGARO_REVIEW_JOB`, which is a permission the caller states
  (`internal/runner/reviewmode.go:207`) — and on the candidate's class,
  never on the name's suffix parsed back out (R2-5).
- **`ProjectJobOfPolicy` sees the variant too, and what that changes for its two outside callers.**
  `jobTriple` (`internal/runner/projectjob.go:46`) gains a **class** field (`coding` or `review`), because
  the token axis is not the only thing the class decides: `checkProjectJobAccount` above must know whether
  the account it is holding is one whose job mounts a Claude token at all. **The class stays inside the
  package.** `ProjectJobOfPolicy` (`internal/runner/projectjob.go:297`) iterates the same
  `projectJobCandidates` set — that is what makes the two readers unable to disagree (R2-5) — so it
  **does** start answering for a review job's name, and it keeps its three-string return rather than
  growing a fourth: it reports the **variant's own account** (`team-a-review` for the account `team-a`),
  which is the account the variant's job name was computed from. Both of its outside callers are correct
  as they stand and neither file changes:
  - the launcher's `reviewJobFor` (`internal/cli/review.go:261`) passes the **coding** job's name and
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
  (`cannotPlaceJob`, `internal/runner/projectjob.go:373`). **SEC flag:** yes — this is the check that
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
CSR11's, and the reason is `checkReviewJobAdmitted` (`internal/cli/review.go:238`), which this task makes
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
  `TestReviewStillRefusesWhenTheConstantIsOff` (`internal/cli/review_test.go:396`, CSR2's test under the
  name CSR11's flip gave it — merged with CSR2 and **not edited here**: the refusal sentence is what a
  person gets, unchanged — this task registers a command, it does not admit one),
  `TestCliReferenceNamesTheReviewCommand` (the regenerated
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
(`internal/runner/reviewartifact.go:675`) already reads the sidecar name, refuses an absent signature and
frames the message — **nothing called it when this task was written; it is merged now (PR #1764), with
the launcher's reader calling it and `ServiceVerifier` implementing the seam**. **What it needs from
C3.9f:** the verify route as deployed
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
same way the job's metadata identity is — and the sentence that used to stand here ("the
owner's own identity has to be in that list") is wrong. **Open item 5 answered this on 2026-11-05:** the
account is **CSR14's** (the installation's root creates it, the operators hold token-creator on it and one
`fugaro update` names it) and the launcher's mint is **CSR15's**, so this task builds the seam and
**leaves it failing closed** — `reviewVerifierIdentity` refuses until CSR15
fills it, with the sentence PR #1764 merged in: "the launcher has no verifier identity to call the
credentials service's verify route as: the installation root creates a dedicated verifier service account
and names it in credentials_service.verifier_accounts, which this launcher impersonates once that account
is real (docs/cloud-senior-review/implementation-plan.md CSR14/CSR15: neither exists yet). No fix round is
launched on a verdict nobody verified" (`internal/cli/reviewverify.go:81`), which is the state PR #1764
merged in. **CSR10 does not do the launcher half**: the account it would impersonate did not exist
when this task was written, and one task that built an installation's identity and the launcher's use of
it would be two trees and two reviews.
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
  (`internal/cli/review.go:479`) reads `review.json.sig` (`runner.ReviewSignName`,
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
    the launcher itself resolved**. That is the axis account `reviewJobFor` (`internal/cli/review.go:261`)
    reads back out of `runner.ProjectJobOfPolicy` (`internal/runner/projectjob.go:297`) and hands to
    `projectJobReviewFor` (`internal/cli/project_job.go:351`) — the name the deployer built the variant's
    job from and the one the service keys its own `ReviewJobAccount.Account` and the sign route on
    (`internal/credsvc/review.go:113`: **never empty**, the axis's empty account spelled
    `config.DefaultClaudeAccount`). The launcher **saves it where it saves the rest of the launch: the
    review run's own task**, whose `agent.oauth_token` (`internal/task/task.go:169`) *is* that account and
    whose **absence is the default** — the field may never spell `default` itself
    (`internal/task/task.go:1094`) — so the fix-round launcher reads the review run's task with
    `ReadTask` (`internal/runstore/runstore.go:1549`, which validates what it reads), maps "" through the
    rule the launch already prints with (`reviewAccount`, `internal/cli/review.go:198`), and puts
    `config.DefaultClaudeAccount` on the wire: `readVerifyRequest` refuses an empty account as a bad
    **call**, not as a `valid:false` about an artifact.
    **`oauth_account` in the run's record is deliberately not that source**
    (`internal/runstore/runstore.go:852`): it is documented as informational, never read by anything that
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
    `head_sha` by `VerifyReviewArtifact`, `internal/runner/reviewartifact.go:675`): the launcher **reads
    the pull request's current head from the forge** with the provider's own
    `FetchPRHead(ctx, number)` (`internal/gitprov/gitprov.go:344`, implemented by github, bitbucket and
    the fake and called by **nothing in `internal/cli` today**), opened through the launcher's existing
    provider seam (`OpenProvider`, `internal/cli/api.go:248`) — **a read this task adds**. **The owner's
    ruling of 2026-11-05/06 corrects where the provider's credential comes from, and it is the answer to
    the recommendation this bullet wrote**: the launcher reads the head **through the credentials
    service's read mint** — the person class operators already have (`github.ServiceReadTokens`,
    `/v1/github/read-token`, `internal/gitprov/github/serviceread.go:58`, installed on the provider with
    `UseServiceTokens`, `internal/gitprov/github/github.go:163`) — and **not** from `OpenProvider(os.Environ(), nil)`
    (`internal/cli/reviewverify.go:117`), which is what the merged task does and which **cannot work when
    the service URL is set**: the launcher's machine holds no GitHub App key (C2.2, D145), the App ID and
    key are the *job's* (`FUGARO_GITHUB_APP_ID`, the App key in the credentials service's bundle), and
    `github.ServiceReadTokens`' own comment says so — "the machine that reads holds no App key at all"
    (`internal/gitprov/github/serviceread.go:93`). So the head read is a **task of its own**, `CSR16` below,
    ordered after CSR15 because the two touch one file, and the release notes carry the sentence that
    `--review-run` refuses until both exist (§6.5). The other
    reading — skip the head check — is refused here on purpose: it would admit **a replay**, a validly
    signed verdict for an **earlier head of the same pull request**, which §6 refuses and which §8 words
    as the window a concurrent writer leaves open — "a concurrent job on the same shared job, which can
    list `runs/` and write under the prefix it sees" (`docs/cloud-senior-review/spec.md:982`), **closed by
    the signature** (`:983`). A head the forge cannot be asked for (a closed pull request, a provider that
    refuses the read) refuses the brief rather than passing it.
  - **the verdict policy the owner answered (Open item 2, 2026-11-05)**: the reader refuses an artifact
    whose verdict is **not `fix first`**, in one sentence that names the verdict word, the review run and
    `fugaro review show RUN` as where the findings are read. The set is `DecodeReviewArtifact`'s own closed
    one (`reviewVerdicts`, `internal/runner/reviewartifact.go:98`, held at `:800`), so the task names the
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
must be able to place — **merged, PR #1761**, so what this task depends on is a fact in the tree rather
than a task ahead of it), **CSR9** (the owner's check types the command), **CSR10** (a flip admits a launch
into a reader that now verifies what it reads: §8's window — a concurrent job on the same shared job
writing under `runs/` — is closed by the signature and not by the order of the runs), **CSR12** (the
live-check switch, which this task **removes**), **CSR13** (a flipped constant also launches a run whose
reviewer has no credential of its own: without the class's model path the launch this task admits reaches
no model at all, gap 7), **CSR14** and **CSR15** (the check this task gates **names the verifier
identity** in step 8, and the account it names is CSR14's: a flip admits a launch whose verdict the
launcher's own verify call is admitted for nobody until that identity exists and the launcher impersonates
it — `internal/cli/reviewverify.go`'s `reviewVerifierIdentity` refuses for want of it today), the
**owner-run live checks**
below, and **E7's first half** — the service enabled for the installation.
**This task is the one gate in this section, and it is a person's.**
**It is also the task that takes the switch back out**: the environment switch CSR12 adds exists for the
owner's check and for nothing else, so the commit that makes the constant `true` is the commit that
deletes it — a switch that outlives the gate it was built for is a hole with no reason left to be open.

**DONE 2026-11-08 — the constant is `true` and CSR12's switch is deleted, in the one commit this task
was.** The gate this section put in front of the flip was **the live checks below, and it was lifted
without them** by the owner's ruling of **2026-11-07**: the reason the flip was held at all is that **the
credentials service admitted no review caller**, and that reason is gone — **CSRW1 (PR #1932)** makes the
service read `FUGARO_REVIEW_JOB_ACCOUNTS`, **CSRW2 (PR #1934)** makes the installation module render it,
and `fugaro update` deploys the credentials service on every installation (**DON1**) — so nothing an
operator runs stands between the flip and a launch the service serves. **What the ruling did not do is
run the checks**: the OWNER-RUN block below is still a person's and still unrun, and **this flip is not a
claim that it passed** — a wrong flip spends on a job the service answers 401, and the only observation of
that is still the check's step 3.

- **Files:** `internal/cli/review.go`, `internal/cli/review_test.go`, `internal/cli/validate.go`,
  `internal/cli/validate_test.go`, `internal/cli/root.go`, `internal/cli/root_test.go`,
  `internal/cli/recipes_skew.go`, `internal/cli/recipes_skew_test.go`, `internal/cli/run.go`,
  `docs/release.md`, `docs/recipes.md`, `docs/rigor-modes/reconciliation.md`,
  `plugin/skills/working/SKILL.md`, `docs/c39-review-identity/live-check.md`,
  `docs/cloud-senior-review/implementation-plan.md`.
  **15 files** (the plan's two named files plus the refusal the flip leaves behind: `validate.go`'s
  precondition note, which said a fugaro update could not supply the admission half, and the two
  documents that told a reader to set the switch; plus the two gates and their release bullet, below,
  and the three places the installation half below made false: `run.go`'s refusal, `docs/recipes.md`'s
  review paragraph and the working skill's sentence about a service that does not admit the caller).
- **The gate the flip made load-bearing, and the value it moved with it.** `reviewJobSince`
  (`internal/cli/recipes_skew.go:467`) named `"0.19.0"` — the release that deploys the review-only
  variant — and no launch ever reached it while `reviewJobAdmitted` was `false`; the flip is what makes
  the value decide anything, and a `"0.19.0"` value then admits an image that **cannot serve the
  launch**: the variant is there and the review run's own model path (CSR13, PR #1777, first tagged in
  0.20.0) is not, so the review starts, bills for the container and dies inside the job on the runner's
  missing-token refusal — the one failure a launch refusal exists to keep in front of the spend. So the
  commit that admits the class moves the value to `"0.20.0"`, taking **CSR6's own file** and nothing else
  (§2's rule, §6.4's note): the constant and its comment, `TestRecipesSkewRefusesReviewJobOnAnOldImage`
  (whose `0.19.0` row is a refused one now and whose admitted row is `"0.20.0"`, derived image and clone
  alike), two new tests — `TestReviewJobSinceHasAReleaseBullet`, the reader this constant never had,
  since `docs/release.md:228`'s checklist bullet is what holds it to a release and every sibling
  `…Since` constant has that test and this one did not, and `TestReviewJobSinceCitationsPointAtItsOwnLine`,
  because the citation checks resolve a line against its file and never against its subject, which is
  how this constant's own citations here went stale in the change that moved it — the same file's
  `run.go` comment, and the bullet itself.
- **The installation's half of the same gate, and the second constant.** A review launch is served by
  **two releases**, and the flip's review found the other one unguarded: `reviewJobSince` judges the job
  image (0.20.0, whose runner can serve a launch), and nothing judged the **credentials service**, which
  admits the review job's own caller only from `reviewAdmissionSince = "0.21.0"` on — the release that
  carries CSRW1 (PR #1932) and CSRW2 (PR #1934). A 0.20.x installation after `brew upgrade` and before
  `fugaro update` was therefore admitted by every gate and died inside the job on the service's 401,
  which is the failure the other gate's own rationale says a launch refusal exists to prevent. So
  `checkReviewAdmission` (`internal/cli/recipes_skew.go`, beside `checkCloneReviewJobImage`) judges the
  installation and is called from `run.go`'s `mode: review` block after the image gates — the block a
  review launch reaches, since `spec.Mode` is what makes one and only `fugaro review` writes it
  (`internal/cli/review.go:153`; `run`'s `--pr` path is a different launch, which is why the refusal
  names it as the way to review meanwhile rather than as a spelling of the same gate). **The judge is the
  installation's own record of that service**, and that is not a proxy by choice: the launcher can ask the
  service nothing (D143, `reviewJobAdmitted`'s comment), while every `fugaro update` writes the
  installation root's `terraform.tfvars.json` with the service's image in it — `pinCredentialsImage`,
  whether that run deploys the service or carries it forward — so the record names the release the run's
  account actually comes from (`installationServiceRelease`); a record naming no image, or one whose
  reference carries no release, only warns (`installationServiceUnknown`). **That record and not
  `base_images.base`, which is what the gate was first written on** (the fourth review round's finding
  2): `imagesStage.kinds` names `base` only for `--base`, for a checkout's workflows or for a catalog
  build, and for nothing else, and `record` never re-pins a custom `--base-image` ref — so the ordinary
  update, run from a directory that is not a repository, moves the service and leaves the base pin
  exactly where the last in-checkout update put it, and a gate on the pin refuses every review on an
  installation that same update has already converged, with `fugaro update` as a remedy that changes
  nothing. That is the loop CLAUDE.md's HARD RULE forbids, and
  `TestUpdateOutsideACheckoutConvergesTheReviewAdmissionGate` is the run that holds it shut: an
  installation whose service record predates the class, with an older base pin to match, is refused
  before one ordinary update and admitted after it, with the pin asserted unmoved. The refusal names
  `fugaro update` as the whole upgrade, and it names the one way to review before that update has run — `fugaro run --pr N --recipe review-only`,
  which launches the repository's ordinary coding job, whose container carries its own account (only
  `fugaro review` sets `spec.Mode`, so this spelling never enters the `mode: review` block). It is **not**
  `reviewJobSince`'s `fugaro image refresh`, which moves no credentials service, and that is why this is a
  second constant rather than that value raised. Tests:
  `TestRecipesSkewRefusesAReviewServiceOlderThanTheClass` (0.20.x refused, 0.21.0 admitted, an unreadable
  or absent service record warned about), `TestRecipesSkewAdmitsACurrentServiceBesideAnOlderBasePin` (the
  older pin beside a current service, admitted),
  `TestReviewRefusesAnInstallationWhoseServicePredatesTheClass` (the command's own refusal, on the
  fixture whose service record is the older release — the installation between the upgrade and the update
  the gate exists for), `TestReviewAdmissionSinceHasAReleaseBullet` for the checklist bullet, and the
  review fixtures moved to `reviewAdmissionSince` (`newReviewFixtureAt` writes the installation's service
  record under the test's own state home). `docs/recipes.md`'s review paragraph,
  `plugin/skills/working/SKILL.md`'s sentence about a service that does not admit the caller, and the
  rigor-modes census row that counts the file's `…Since` constants (29, not 28) follow the same commit.
- **Interfaces:** `reviewJobAdmitted` (`internal/cli/review.go:58`) is `true`, with its comment rewritten
  to say **what made it true** — C3.9's merge, CSRW1's and CSRW2's, the owner's ruling of 2026-11-07 and
  the release the variant deploys in — rather than deleted: the next reader's question is why a constant
  that was `false` for a reason is now `true`, and the answer is not in the code. `checkReviewJobAdmitted`
  (`:238`) keeps its sentence, and the current tree reaches it for no installation at all: it is the
  refusal a binary built **before** this flip prints, and the note that sentence's installation half
  belongs to is `checkReviewService` (`:288`), which refuses an installation whose service is **not**
  enabled exactly as CSR2 wrote it. The test seam (`review_test.go`'s `unadmitReviewJob` puts the constant
  back to `false` for one test) stays, because that refusal is the only way to reach the sentence and the
  other refusals still need covering. **The switch came out in this commit**: CSR12's variable and the one
  read of it are deleted here, so the launcher's admission decision has exactly one input again — the
  constant — and no environment can skip the refusal this task was the last one to need. `internal/cli` and
  `plugin/skills` never named the variable in the first place (CSR12's own rule), so nothing else moved;
  the live check's step 3 now reads the plain `fugaro review --pr <N>` it always named.
- **How `fugaro doctor` reports E7's two halves, and why it does not report this constant.** The two
  facts are **CSR6's**, and this task references them rather than adding a third: **is the credentials
  service enabled for this installation**, and **does it admit the review job's own account**. The
  constant is deliberately **not** a doctor fact: a launcher cannot ask the service its configuration
  (there is no admin route, D143) and cannot see the admission decision without making a call that mints a
  credential, which is why it is a constant in the first place. So after this flip `doctor` reports the
  same two facts it reports today, and the honest consequence is stated in its own words: **a green
  `doctor` is the two halves, and the release notes are where the constant's state is written** — a
  binary older than the flip keeps refusing whatever the installation says.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestReviewLaunchIsAdmitted|TestReviewStillRefuses|TestReviewReadsNoAdmissionSwitch|TestTheRemovedAdmissionSwitch' -count=1`.
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
  job the service answers 401 (a run that spends and fails), and a flip ahead of CSR8 would have left a
  container that halts at admission with a refusal nobody can act on — **that risk is closed rather than
  pending, because CSR8 merged (PR #1761)**. **The remaining risk is not a code defect this task can test
  away**, which is why the gate is a live check rather than a test.
- **Mutation to hold it:** revert CSR8's placement (drop the review variant from
  `projectJobCandidates`) **and** flip the constant → the launch is admitted and the run halts
  at admission; make the flip
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
     the credentials service's own documented steps — **and the third thing E7 needs, which is not a
     command and is no longer a person's either**: `credentials_service.verifier_accounts` must name the
     verifier service account **Open item 5** settled, and **`fugaro update` supplies that name itself**
     (CSR14: the installation root creates the account, the update writes its address into the local
     config's `verifier_accounts` **when nothing anywhere names one** — the config silent and the published
     node empty — and the policy stage publishes it), so the owner **reads it back**
     rather than typing it — and **never a person's address**: the service refuses to start with an
     operator's own address in both lists (CSR10's **Depends on**, `internal/credsvc/auth.go:574`). Until
     that name is published, no verify call — the owner's or the launcher's — is admitted, and CSR10's
     reader refuses every brief;
  2. **C3.9's live check, in full**: `docs/c39-review-identity/live-check.md`'s eleven steps, whose step 3
     is `fugaro review --pr <N>` — the launch this section exists to make possible, and the one step that
     could not be reached while the constant was `false`. **It is reached with the plain launch now that
     the flip has landed** (the run of 2026-11-08; while the flip was held, CSR12's switch was how Open
     item 1's answer of 2026-11-05 reached it, and that switch is deleted); **step 8 of that file is written to the verifier service account by
     CSR12**, the task that touches the file — the hand-made `POST /v1/review/verify` of that step carries
     an ID token **the owner mints by impersonating that account**
     (`gcloud auth print-identity-token --impersonate-service-account=<the account>`, the impersonation
     CSR14's operators' grant on the account is for, and the same identity the launcher's own verify call
     mints through — CSR15) — and **an agent edits neither the check nor the service's configuration**;
  3. **Check 125's steps 6 and 7** on the review-only variant: the mutation check under the fence
     (`unshare -rn`, and the private `/proc` form) — step 7 is the re-confirmation U7's caveat 4 asks for
     **because the variant is a different execution environment from the coding job the probe ran in**;
  4. **then the flip lands as this task's commit**, quoting in the pull request the check's number and the
     date, which is what the constant's comment is rewritten with. **On 2026-11-08 the owner ruled the
     gate lifted and it landed without them** (the DONE line above): the comment records that ruling
     rather than a check's number, and the checks are owed **after** the flip rather than before it —
     which is why step 2's launch reads as the plain one, and why the flip's pull request says in as many
     words that they are unrun.
  **An agent never runs any of the four**: not the opt-in, not the live check, not the fence probe, and
  not the flip on the strength of a claim that the others passed. The credentials service's own rule is
  explicit that `fugaro update --provision-credentials` and `fugaro creds set` are refused inside an agent
  or a run, and this task does not work around a refusal.
- **Commit:** `review: admit a review launch, after the live check the flip names, and take the check's switch back out`.

### CSR12. The live-check switch: a launch that starts, and admits nothing (SEC)

**REMOVED 2026-11-08 — the variable, its one read and its warning line are gone from the tree**, deleted
by CSR11's flip in the same commit that made the constant `true`, which is what this section's own last
paragraph asked for. **What follows is the record of the task that added it**, test names included: those
tests were deleted with the switch, and the assertions that outlive it are CSR11's own
(`TestReviewReadsNoAdmissionSwitch` above all, which is the one that fails if the read comes back).

**Decisions:** **the owner's answer of 2026-11-05 to Open item 1** — the **environment switch**, and
explicitly **not** that item's recommendation — together with **D893 (c)** and Global Constraint 6 / C3.9.
**Findings:** none of the reconciliation's; this task answers **gap 1** of §6's own table and the owner's
answer to Open item 1, and it is written here rather than left as a paragraph because a switch that a
later commit must remove is a change with its own red step, its own tests and its own review.
**Depends on:** **CSR10** (both hold `internal/cli/review.go`; the switch is level 2's) and, for its
**purpose**, **CSR8** (merged, PR #1761) and **CSR9** — the check it exists for types the command, and
the run it starts has
to be placeable. **What it is, said so that no reader can take it for more:** the refusal it skips
(`checkReviewJobAdmitted`, `internal/cli/review.go:238`) is a **convenience — failing early** — and
**not the security boundary**. The boundary is the credentials service, which still answers **401** to a
review caller it does not admit (Global Constraint 6, C3.9). **The switch lets a launch start; it makes
the service admit nothing.**

- **Files:** `internal/cli/review.go`, `internal/cli/review_test.go`, and
  `docs/c39-review-identity/live-check.md` (the switch's **only** documentation outside this plan: step 3's
  note carries the variable, that it is off by default, that a coding agent's session is refused it, and
  what it does and does not do. The same task writes **step 8** to Open item 5's answer, which the owner
  gave on 2026-11-05: the verifier list names the **verifier service account** the installation creates
  (CSR14) and the step's hand-made `POST /v1/review/verify` carries an ID token the owner mints by
  impersonating it (`gcloud auth print-identity-token --impersonate-service-account=<the account>`) — the
  owner's own address, which the step named while the item was open, is the one entry the service refuses
  to start with (`internal/credsvc/auth.go:574`), so the check cannot pass as the file stands and this task
  is the one that corrects it). **3 files.**
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

### CSR13. The review run's model path is its class, not `agent.auth` (SEC)

**Decisions:** **O1** (the reviewer's model calls route through the credentials service's review class, so
the review container holds no OAuth token), **D143** (a run that reaches the service holds no credential of
its own), **D893 (c)** (the credential is the service's, by name), **Open item 6's answer of
2026-11-05/06** (a review run does **not** run with the installation's budget off: the class refuses and
the sentence names `budget.mode`) and **the owner's cut of 2026-11-06** (0.20.0 ships the OAuth review
alone; the api-key review is §6.7's later release). **Findings:** none of the
reconciliation's; this task answers **gap 7** of §6's own table, found while §6 was written, and it is the
task **Open item 4** became. **Depends on:** nothing unmerged — **CSR8 has merged** (PR #1761), and the
class this task reads is the one CSR8 landed: `r.runClass()` (`internal/runner/projectjob.go:82`, the
**task's mode**) and the class of the candidate the run was admitted to, never `r.reviewJob()`
(`internal/runner/reviewartifact.go:934`, the environment marker `FUGARO_REVIEW_JOB` the **launcher** writes
at `internal/cli/run.go:3560`) — see *the keying* below, which is the correction this task was re-read for.
**E7 does not reach the code**: the
review class exists on the service's side (C3.9a–C3.9g, merged) and every test here is hermetic; what E7
still decides is whether a placed review run's **first call is served** (§1). **Before CSR11**: a flipped
constant launches a run whose reviewer this task is what gives a model to reach.

- **Files:** `internal/runner/gateway.go`, `internal/runner/gateway_test.go`, `internal/runner/routing.go`,
  `internal/runner/routing_internal_test.go`, `internal/runner/agentenv.go`,
  `internal/runner/agentenv_test.go`, and `internal/runner/runner.go` — **one call site**, taken **alone**
  under §2's one-writer rule, which this task says in its pull request rather than editing silently.
  **7 files.**
- **Interfaces:** one accessor, and the class spelled with an auth the tree already has.
  - **`r.modelAuth()`** is the run's answer to *which auth this run's model path takes*: `config.AuthCascade`
    when **`r.runClass() == classReview` and the admitted job's class is the same class**
    (`r.job.Class == classReview`), and `r.cfg.Agent.Auth` otherwise. Five places ask that question and all
    five read it: `gatewayOn` (`internal/runner/gateway.go:134`), `gatewayUpstream`'s switch
    (`:211`, whose cascade arm is already the service — `:232`, `serviceUpstream` at `:293`), `applyRouting`'s
    `auth` (`internal/runner/routing.go:63`), `GatewayVars` (`internal/runner/agentenv.go:317`) and
    `agent.BuildEnv`'s `EnvSpec.Auth` (`internal/runner/runner.go:2295`).
  - **The keying, and it is the half that decides where a container's model calls go. The class is the
    run's MODE and the job it was admitted into — never the environment marker, which is a different
    fact and a spoofable one.** The class selects a **credential**: `cascade` is the one auth whose
    upstream is the credentials service, and the service serves a review-class call from the pairing
    account's tier-A credential and from nothing else (`reviewCandidates`,
    `internal/credsvc/cascade.go:105`). `r.reviewJob()` reads `r.d.Env` — the whole of the function is
    `envLookup(r.d.Env, EnvReviewJob) != ""` (`internal/runner/reviewartifact.go:934`, the read at `:935`)
    — which is the container's environment, the one input the launcher writes for the execution
    (`backend.LaunchSpec.Env`, `internal/cli/run.go:3553`) and the one
    a job definition carries — and CSR8's own rule for the class is the opposite in as many words:
    `r.runClass()` is the **task's mode** (`internal/runner/projectjob.go:82`), "never anything the job's
    environment says, so a container cannot be told which class it is by the name it was started under",
    and the two doors on the credential the variant holds read it — `checkJobClass` (`:172`) and
    `checkProjectReviewJobAccount` (`:597`). The marker's own job is a *permission the caller states*, not
    a credential: `reviewMayComment` (`internal/runner/reviewmode.go:207`) reads it so a review run knows
    it may not write to the pull request it reads. **Keyed on the marker, a run in the shared coding job
    whose environment carries `FUGARO_REVIEW_JOB=1` passes both doors** — its mode is coding and the
    candidate's class matches, so nothing refuses it — **and would then take the review class's path**:
    O2's arm skipped, the gateway's upstream the service, its calls served by the review class's
    credential, while the service holds it to the allow-list alone. A coding run holds the App key and a
    Claude token; a container must not be able to trade them for another class's credential by writing one
    variable. **The second half of the conjunction is the same fact from the other side**: `r.job.Class`
    is the class of the candidate the run was **admitted** to (`jobTriple.Class`,
    `internal/runner/projectjob.go:50`, set at `:148`), so a run whose mode is review but which was not
    placed in the review variant — a per-repository job, which adopts no triple at all
    (`internal/runner/projectjob.go:140`) — is not given the review class's credential either. Both
    halves fail **closed**: the run keeps its own configuration's auth, which for the review job is
    `oauth` with a token its container does not mount (`internal/runner/agentenv.go:294`), so the failure
    is a refusal and never a call on a credential nobody chose.
  - **The spelling is `cascade` because every arm the class needs already exists for it, and none of them is
    a credential of the job's.** A cascade run carries none (D143): `agent.BuildEnv`'s arm requires no
    secret and refuses a run with no gateway (`internal/agent/env.go:169`), `GatewayVars` sets the gateway's
    per-run token (`:318`), the gateway's arm is the service, and the **oauth** arm the review job's own
    configuration would otherwise take is the one that demands a mounted `CLAUDE_CODE_OAUTH_TOKEN`
    (`:164`) — the token CSR7's variant deliberately does not mount, and the reason
    `internal/runner/agentenv.go:294` describes the review job as a run whose gateway is off. **So
    `internal/agent` is not touched by this task at all**, and no fourth auth name is invented:
    `GatewayOn` (`internal/runner/routing.go:158`) already answers true for `cascade`, so the predicate
    keeps its two inputs and the readers that share it — `validate`'s prices read
    (`internal/cli/validate.go:1251`) — keep answering for a coding run, which is the only question a
    repository's configuration can be asked: whether a run is the independent review job is the **task's
    mode** (`internal/cli/run.go:3411`, the launcher's own decision — the same fact `r.runClass()` reads)
    and never a file's own claim.
  - **What the class does not decide, and this is the half the item's own reading left open.** **O2's arm is
    skipped for a review class run** (`internal/runner/routing.go:87`): the class is the consent that
    switch stands for in a coding run, which is [spec.md](spec.md) §2's own sentence ("a review job needs no
    `routing.claude: {via: cascade}` to be admitted — that switch is the operator's consent for a coding
    run's *cascade*, while a review job's credential is decided by its own class"). **The allow-list arm is
    not** (`:105`): the service holds **every** `/v1/messages` call it carries to the project's
    `budget.allowed_models` and refuses a project that publishes none — before the class is resolved
    (`internal/credsvc/server.go:775`) — so a class that skipped that arm would admit a run the service then
    refuses on its first call. Both directions are tests below, because "the switch does not apply" and "no
    rule applies" are one word apart in the wrong direction.
  - **The OAuth review is the only one 0.20.0 ships, and the class is where that is true.** The class's
    credential is the pairing account's **tier-A** credential and nothing else (`reviewCandidates`,
    `internal/credsvc/cascade.go:105`), and that account is a **Claude OAuth account** by construction:
    `ReviewJobAccount.Account` is "the Claude account's name as the policy's accounts block spells it"
    (`internal/credsvc/review.go:112`), and every name on that axis derives from `claude_accounts`
    (`internal/config/claude_accounts.go`, the `claude-oauth-token` family). The bundle's only model
    credential that is **not** an OAuth token is the Anthropic API key, at **tier B**
    (`internal/credsvc/bundle.go:677`), which a review caller is never offered — and there is no
    provider key in the bundle at all (`CredentialKey`'s closed set, `internal/credsvc/bundle.go:668`).
    **So a review whose account/auth is not OAuth refuses rather than being served by something else**,
    in the class's own bootstrap refusal, whose sentence names the path and the release it is in: "the
    independent review's model calls are served by the credentials service's review class from the
    installation's Claude OAuth account, and the api-key review — a provider key, OpenRouter or
    Anthropic — is a later release (CSR17–CSR18 of §6.7): 0.20.0 ships the OAuth review alone". **Both
    halves are refused**: a repository whose `agent.auth` is a credential of its own (`api-key`, or
    `vertex`) would need a key the review variant deliberately does not mount (CSR7), and a pairing that
    names anything but a Claude account is one `pairReview` never builds. `oauth` and `cascade` are the
    OAuth path and are served. The owner cut that path from 0.20.0 on 2026-11-06, and **nothing in
    this task, and nothing in 0.20.0, may read as though the api-key review works**: the class offers one
    credential and no fallback (spec §10), and `agent.BuildEnv`'s cascade arm refuses a run with no
    gateway rather than falling back to a credential of the job's (`internal/agent/env.go:169`), so the
    refusal is fail-closed in both directions.
  - **What stays out, and why, so no reader takes the accessor for a general rewrite.** The class does not
    move the cost basis (`runstore.ModelBasis(r.cfg.Agent.Auth)`, `internal/runner/cost.go:104`): a review
    class call is served by the pairing account's **tier A** credential and by nothing else
    (`reviewCandidates`, `internal/credsvc/cascade.go:105`), which is what the config's own `oauth` spelling
    already says. Nor the account walk (`accountWalkApplies`, `internal/runner/accountchoice.go:150`), whose
    guard already requires an `oauth` run that names no account and whose moving of a stage to another
    account is something a one-account class never asks for. Neither file is this task's; a later task that
    wants the class there reads the accessor.
- **Command:** `GOFLAGS=-p=2 go test ./internal/runner/ -run 'TestReviewRunIsGatewayOnWhateverItsAuth|TestReviewRunSkipsTheCodingCascadeSwitch|TestReviewRunIsStillHeldToTheAllowList|TestCodingRunKeepsItsOwnModelPath|TestReviewRunCarriesNoCredentialOfItsOwn|TestReviewRunUpstreamRefusesWithoutAService|TestReviewJobModelPathIsKeyedOnTheRunClass|TestReviewRunWithTheBudgetOffNeverReachesACredentialOfItsOwn|TestReviewRunIsNeverServedByAnAPIKeyOfItsOwn' -count=1`.
- **Tests:** `TestReviewRunIsGatewayOnWhateverItsAuth` (a review class run whose `agent.auth` is `oauth`,
  **with the budget on**: the gateway is on, the upstream is the service's `KindService`, and the env spec
  the agent is built with carries the class's spelling and requires no secret of the job's — **the red
  step**; *whatever its auth* ranges over the auths the class serves (`oauth` and `cascade`) and is about
  the repository's own `agent.auth` and not about the budget mode,
  which has its own test below — a repository whose auth is a credential of its own is **refused**, and
  that is the api-key test below),
  `TestReviewRunSkipsTheCodingCascadeSwitch` (the same run with a project policy that publishes no
  `routing.claude` at all: bootstrap proceeds, while a run of the **coding** class with
  `agent.auth: cascade` in the same tree is still refused with O2's own sentence — the second direction of
  that arm, and with the class keyed on the mode it is also the run that **carries the environment marker**
  and gains nothing by it),
  `TestReviewRunIsStillHeldToTheAllowList` (a review class run whose project publishes no
  `budget.allowed_models` is refused in bootstrap, because the service would refuse its first call),
  `TestCodingRunKeepsItsOwnModelPath` (a coding run's gateway state, upstream and gateway variables are
  what they are today, for each of `oauth`, `api-key`, `vertex` and `cascade` — the second direction of the
  whole task, and the one a careless accessor breaks),
  `TestReviewRunCarriesNoCredentialOfItsOwn` (the
  agent's environment holds neither `ANTHROPIC_API_KEY` nor `CLAUDE_CODE_OAUTH_TOKEN`, and the managed
  settings hold the gateway's address and its per-run token: O1's shape, which is what CSR7's variant was
  built to make true), `TestReviewRunUpstreamRefusesWithoutAService` (no
  `FUGARO_CREDENTIALS_SERVICE_URL`: the cascade arm's own refusal — the installation has not opted in
  (D147) — and never a fallback to a credential of the job's; a test upstream paired with a service run is
  refused the same way), `TestReviewJobModelPathIsKeyedOnTheRunClass` — the test that used to say *on the
  marker* and now holds the other direction, because the marker is not what the class is read from: a run
  **in the review variant, mode: review, whose environment carries no marker at all** is given the class
  (nothing reads the variable), a run **in the review variant whose environment carries the marker and
  whose mode is not review** is refused admission by CSR8's own door before this accessor is asked (so the
  marker buys no class), and a **coding-mode run in the shared job whose environment carries
  `FUGARO_REVIEW_JOB=1`** is not given the class: it keeps its own configuration's auth, and with
  `agent.auth: cascade` O2's arm then refuses it — the spoof this correction closes), and
  `TestReviewRunWithTheBudgetOffNeverReachesACredentialOfItsOwn` (**Open item 6's answer of
  2026-11-05/06**: the class with the installation's budget mode **off** refuses in bootstrap with a
  sentence that names **`budget.mode`**, published as `observe` or `enforce`, as the fix — and the run
  reaches no credential of the job's on the way out. The gateway is off (`GatewayOn(cascade, "")` is
  false, `internal/runner/routing.go:158`) and the run **does not** fall through to `agent.BuildEnv`'s
  cascade arm, whose sentence is written for a coding run and names a fix a review job cannot apply
  (`internal/agent/env.go:169`). **The class does not force the gateway on**: a mode the installation set
  to `off` keeps its one meaning, which is the reading the owner refused), and
  `TestReviewRunIsNeverServedByAnAPIKeyOfItsOwn` (a review run whose repository writes
  `agent.auth: api-key`: the class **refuses in bootstrap**, the run's environment carries no
  `ANTHROPIC_API_KEY` and no provider key, and the sentence names the api-key review and the release it
  is in — so a repository's own api-key configuration cannot produce an api-key review, and nothing in
  0.20.0 reads as though it can; see *the OAuth review is the only one 0.20.0 ships* above).
- **Red step:** `TestReviewRunIsGatewayOnWhateverItsAuth` fails today for the reason gap 7 names:
  `GatewayOn("oauth", mode)` is false (`internal/runner/routing.go:158`), so a review class run's gateway
  is never started — with the budget **on**: this is the auth's doing and not the mode's, and the mode has
  the case of its own that the budget-off test above holds — `stagePins` returns nil
  (`internal/runner/agentenv.go:259`), the managed settings carry
  no gateway variable (`:309`), and `agent.BuildEnv`'s oauth arm demands a token the variant does not mount
  (`internal/agent/env.go:164`) — the reviewer's calls have **no path to a model at all** unless the
  operator publishes the coding cascade's consent and the repository writes `agent.auth: cascade`, which is
  what [spec.md](spec.md) §2 says a review job does not need. **SEC flag:** yes — this is the arm that
  decides **where a container holding no credential sends its model calls**, and its failure mode is a call
  that reaches Anthropic on a credential nobody chose, or a run that dies on its first call with a
  sentence naming the wrong fix. **And the branch this step does not reach, which the review of this task
  is asked to hold in view:** with the class in place and the installation's budget mode **off**, the run
  has a credential selected and no gateway to send it to, so **the class refuses there**, with the
  sentence the budget-off test above asserts. That refusal is **Open item 6's answer of 2026-11-05/06**,
  and it is why the test is named in this list: without it the run would stop at `agent.BuildEnv`'s
  cascade refusal (`internal/agent/env.go:169`), a sentence about the cascade written for a coding run,
  which a review job cannot act on.
- **Mutation to hold it:** key the class on the **environment marker** instead of the run's mode →
  `TestReviewJobModelPathIsKeyedOnTheRunClass` fails, and the spoof in its third direction is admitted;
  key it on the job name's suffix instead → the same test fails the other way, which is why it says the
  mode and never the name (R2-5); skip the allow-list arm along with O2's →
  `TestReviewRunIsStillHeldToTheAllowList` fails; make the class a rewrite of `r.cfg.Agent.Auth` rather
  than an accessor → `TestCodingRunKeepsItsOwnModelPath` fails (and the record's basis and
  `internal/runner/budget_session.go:407`'s auth would move with it, which is why the accessor and not
  the field);
  let the class answer with no service URL → `TestReviewRunUpstreamRefusesWithoutAService` fails; force the
  gateway on in the class's own path — the reading Open item 6 refused — →
  `TestReviewRunWithTheBudgetOffNeverReachesACredentialOfItsOwn`
  fails, because the run's gateway would be "on" in a mode the gateway is not built for and the refusal
  that test asserts is never reached; let a repository's own `agent.auth: api-key` give the review run a
  key → `TestReviewRunIsNeverServedByAnAPIKeyOfItsOwn` fails.
- **Reviewer tier:** **SEC — opus, with the independent review mandatory** (the repository's rule for a
  change on the model path, and this one is a second path through the most carefully-worded ladder in the
  tree).
- **OWNER-RUN:** none. Every assertion here is hermetic: a fake service over `httptest` and a policy in a
  `t.TempDir()`. The one observation behind it — a real review run's call served by the review class — is
  C3.9's live check, which is a person's, and **no agent performs it**.
- **Commit:** `runner: a review run's model path is its class, not agent.auth`.

### CSR14. The installation creates the verifier service account, and one `fugaro update` names it (SEC)

**Decisions:** **D1068** (the verify route answers one refusal bit, echoes no key and names no account, so
the class's whole risk is the account oracle the design refused to build), **D1075** with C3.9g1–g3 (the
verifier list is the owner's, published by `fugaro update`'s policy stage and read back by the credentials
stage into the tfvars) and **the owner's answer of 2026-11-05 to Open item 5**: a **dedicated verifier
service account**. **Findings:** none of the reconciliation's; this task answers **gap 6**'s third half —
the verifier identity — which §6's table had put on the owner and which is a service account an installation
creates rather than a value a person types. **Depends on:** nothing in this plan: the module, the guards and
the writers of the name are the installation's own, and **E7 is not this task's** — the account is created by
the same apply that enables the service, so an installation whose `credentials_service` is null gains no
resource, no grant and no golden that moves. **That state stays populated through 0.20.0**: the owner cut
`docs/credentials-service/default-on.md` (PR #1769 — the ruling of 2026-11-05 that `fugaro update` deploys
the service for **every** installation) **from 0.20.0 on 2026-11-06**, so for this release the credentials
service remains **opt-in**, and an installation that never opted in is exactly the state the "off" half of
the guard is for. **What that ruling does not change is this task's guard**: `credentials_service_on` is
`var.credentials_service != null`
(`deploy/terraform/gcp/modules/installation/credentials.tf:25`), the variable a default-on release would
make non-null everywhere, and the expression is untouched — so `count` is right in both states, and the
only thing a default-on release changes is **who** is in the off state, from "an installation that never
opted in" to "nobody". **This task's premise is today's state, and it is the one 0.20.0 ships**: the guard
reads the block, so an installation that has opted in gets the account and the name in the same update, and
one that has not gets neither — and if #1769 later lands, that release's DON1 **renders the name this task
publishes** for every installation, which is why this task is its precondition and not the other way
round. **Nothing here is a bet on #1769's timing**: the guard, the default and the writer are correct with
the service opt-in, and they stay correct the day it is not.

- **Files:** `deploy/terraform/gcp/modules/installation/verifier.tf` (new: the account),
  `deploy/terraform/gcp/modules/installation/iam.tf` (the two grants),
  `deploy/terraform/gcp/modules/installation/credentials.tf` (the `FUGARO_VERIFIER_ACCOUNTS` entry: the
  account this module creates **as the block's default when the block names none**, and the block's own
  list, rendered as it is, when it names one),
  `deploy/terraform/gcp/roots/installation/tests/verifier.tftest.hcl` (new),
  `deploy/terraform/terraform_test.go` (the two guards, deliberately widened),
  `internal/infra/tf/cover.go` + `internal/infra/tf/cover_test.go` (the one confirmation's tables,
  deliberately widened), `internal/infra/tfvars.go` + `internal/infra/credentialsvars_test.go` (the
  account's ID, beside `CredentialsAccountID`, and the carry that spells the effective list into the
  block, with the carry's own test beside it),
  `internal/cli/init.go` + `internal/cli/init_credentials_test.go` (the update names it, and the
  convergence test below), and `internal/infra/tf/testdata/golden/installation-credentials.plan.json`
  (**regenerated** by `scripts/gen-golden-plans.sh`, never hand-edited). **12 files**, inside §3's twelve —
  and the reason is the task's own subject: the account, the name the service is rendered with and the one
  command that puts it in the owner's published policy are **one change**, because an installation holding
  an account no update names is an operator step, and the HARD RULE does not allow one.
- **Interfaces:**
  - **The account.** `google_service_account.fugaro_verifier` in `verifier.tf`, `account_id = "fugaro-verifier"` —
    a **literal**, and that is the load-bearing half of this task's answer — with
    `count = local.credentials_service_on ? 1 : 0`, the switch `google_service_account.credentials` is on
    (`deploy/terraform/gcp/modules/installation/credentials.tf:111`), for its own reason: a verifier is the
    service's caller class, and an installation with no service has no route to verify against. **A literal
    account ID in a module that knows `var.project` makes the address a fact of the configuration rather
    than of the apply**: `<id>@<project>.iam.gserviceaccount.com`, computable by the Go side **before**
    anything is planned (the spelling `internal/cli/doctor_secret_readers.go:468` already uses for the
    credentials account, over the helper `internal/infra/spec.go:761`), which is what lets the same
    `fugaro update` both name the account in the owner's published policy and have the service admit it.
    **The account is created and granted nothing of a run's**: no bucket, no secret, no job account, no
    `secretAccessor`.
  - **The two grants, and both are deliberate widenings of a fence that exists to make exactly one grant
    possible.**
    - **`roles/iam.serviceAccountTokenCreator` on it, for `var.operators`** — the impersonation the owner's
      `gcloud auth print-identity-token --impersonate-service-account=…` and the launcher's own mint (CSR15)
      both stand on. It is the **second** grant of that role in the tree: the first is the deployer's
      one-time setup (`google_service_account_iam_member.fugaro_deployer_token_creator`,
      `deploy/terraform/gcp/modules/installation/deployer.tf:501`, on the literal deployer account, with
      `for_each = toset(var.operators)`), and **two guards refuse a second site today** —
      `TestServiceAccountTokenCreatorIsTheDeployersImpersonationGrant`
      (`deploy/terraform/terraform_test.go:1834`) requires the literal role to appear **exactly once**, in
      that block, and `deployerTokenCreator` (`internal/infra/tf/cover.go:1558`) refuses a plan granting it
      on any account but the deployer's ("D75's impersonation grant is on that account alone"). Both become
      a **table of the two sites**: each still literal, each still on one named account of this run's
      project, each still with `var.operators` as its only member. `TestInstallationServiceAccountGrantsNameTheirAccount`
      (`:1902`) keeps holding every `google_service_account_iam_member` in the module to a literal account
      path built from `var.project` — the shape `scheduler.tf:28` already uses for the operators'
      `serviceAccountUser` on the scheduler's account — so the new grant is written the way the module
      already writes one and the guard needs no exception for it. **This widening is the task's reason to
      exist and not a side effect of it**: with `deployerTokenCreator` unchanged, `fugaro update` refuses
      its own plan and the account is never created — an update that does not converge, which is the one
      failure CLAUDE.md's HARD RULE is about.
    - **`roles/run.invoker` on the credentials service, for the verifier account** — in `iam.tf`, where the
      service's two invoker grants already live (`google_cloud_run_v2_service_iam_member.credentials_invoker`,
      `deploy/terraform/gcp/modules/installation/iam.tf:163`, the operators' user members, and
      `.credentials_coordinator`, `:194`, the coordinator's own account), and **not** in `credentials.tf`,
      whose own test refuses any such resource in that file. Without it the service's front end refuses the
      verifier's ID token before the container runs (`invoker_iam_disabled = false`), so the verify route
      would be unreachable for the one caller this design builds it for. `cover.go`'s `grantRules` gains the
      principal kind beside `toCoordinator` — whose own comment (`internal/infra/tf/cover.go:236`) is the
      precedent — and the rule it applies is spelled where the coordinator's grant itself is:
      "a role granted to a new kind of principal is not covered by an ordinary update's confirmation until
      grantRules says so" (`deploy/terraform/gcp/modules/installation/iam.tf:190`).
  - **The name has two places to reach, and one `fugaro update` reaches both — which is the whole reason
    this task was re-read.** The name has to be in **the service's own environment** (or its callers are
    admitted for nobody) and in **the owner's published policy node** (or CSR15's reader refuses, and the
    owner's `doctor` reports no verifier). The old draft of this task took the first from the module's
    **output** after the apply and the second from a local-config write, and that does not converge:
    - **the service's list is rendered by the module, and the account is its DEFAULT — the case where the
      block names nothing.** `FUGARO_VERIFIER_ACCOUNTS`
      (`deploy/terraform/gcp/modules/installation/credentials.tf:482`) is rendered today as
      `var.credentials_service.verifier_accounts == null ? [] : [jsonencode(…)]`; it becomes
      **`var.credentials_service.verifier_accounts == null ? [google_service_account.fugaro_verifier[0].email] : [jsonencode(var.credentials_service.verifier_accounts)]`**,
      which is the shape the coordinator's own entry already has (`:517`,
      `local.coordinator_on ? [google_service_account.coordinator[0].email] : []` — the module rendering an
      account **it** creates, conditionally). So an installation whose block names no verifier admits the
      account **this same apply creates**, and an installation whose block names a list admits **exactly
      that list**. **The account is a default and not an addition, and the difference is load-bearing:**
      a union of the two would admit an identity the owner cannot remove with `[]`,
      contradict the owner's own wording of 2026-11-05 (the account is "named in `verifier_accounts`") and
      D1075 (the list is the owner's), and leave the service admitting a caller the launcher never uses —
      **CSR15 mints as the one account the published list names and refuses a list of none or two**, so a
      union would put the service and the launcher on two different identities in every installation whose
      owner wrote their own list. **Why the module and not the Go side**, which is checked against the
      tree rather than assumed: an ordinary `fugaro update` carries the `credentials_service` entry
      forward as it is found (`CarriedCredentialsService`, `internal/infra/tfvars.go:612`, whose own
      comment is that every ordinary run rewrites the tfvars from a spec that never sets the key) — **its
      one `verifier_accounts` member aside, which the carry writes from the effective list, below** — the
      writer that recomputes the list is `prepare`, which every ordinary `fugaro update` runs
      (`internal/cli/init.go:2089`) and which reads the node the **previous** update published
      (`internal/cli/init.go:2561`) — so between those runs the block is frozen **except for the
      one member the effective list decides** (the carry's own half, below), and the module
      is the only writer the apply that creates the account has. **The carried-null block converges in one
      ordinary update because of exactly this**: null is the state the default is for, and the account is
      admitted by the apply that creates it rather than by a later run nobody was told to make.
    - **the clear is spelled, not dropped, and that is what keeps `[]` authoritative.** The module has to
      tell three states apart — *nothing said anywhere* (null → the default account), *the owner's list*
      (rendered as it is) and *the owner's clear* (`[]` → no verifier at all) — and today the writer cannot
      express the third: `CredentialsServiceSpec.VerifierAccounts` carries `omitempty`
      (`internal/infra/tfvars.go:263`), so an explicit empty list is written as **no key at all** and the
      module reads it as null, which under the default above would admit the installation's account on an
      installation whose owner had just turned the route off. So the field becomes **three-state** (a nil
      pointer omits the key, a pointer to an empty slice writes `[]`), and the existing test
      `TestCredentialsServiceClearsTheListOnAnExplicitEmptyOne` (`internal/cli/init_credentials_test.go`)
      moves with it: the entry is **present as an empty array** rather than absent. **The outcome it pins
      is the same** — the service starts with an empty verifier list and admits nobody — and the spelling
      is what leaves null free for the default.
    - **And the carried block is the other half, which the three-state field alone does not reach.** The
      block is carried forward **verbatim**, present or absent (`CarriedCredentialsService`,
      `internal/infra/tfvars.go:612`, whose comment is that the entry is taken as it is found), and the
      stage that recomputes the list is registered only under `--provision-credentials` — so on an ordinary
      update the module reads the block exactly as the last one held it, and `verifier_accounts` is a
      **new** member no installation's block carries yet. Left there, the default above would admit the
      installation's account on **every** opted-in installation whose local config says otherwise: an owner
      who wrote `verifier_accounts: []` — the clear `VerifierAccountsEffective` honours
      (`internal/localcfg/localcfg.go:1402`) and the publisher keeps — would have the route switched back
      on at the service, and the published node and the service would disagree in the one direction
      CSR15's reader sees: the reader refuses every fix round on an installation whose service admits the
      account. So the **carry writes that one member from the effective list** — the owner's list as it is,
      an explicit `[]` as `[]`, and the key absent **only when the effective list is nil**, which is the
      one state the default is for — and every other member of the block stays taken as it is found. It is
      the same value the local-config write and the publish read, so the tfvars, the published node and
      the service's own environment cannot disagree; and it is the ordinary update that writes it, not a
      step.
    - **the published policy is written by the same update, before the policy stage publishes, and only
      when the EFFECTIVE list is nil.** `fugaro update` writes the deterministic address into the local
      config's `verifier_accounts` when — and only when — `lc.VerifierAccountsEffective(node.VerifierAccounts) == nil`:
      this machine's config silent **and** the published node naming no verifier. The precedent
      `credentialsServiceBlock` already sets (`internal/cli/init.go:2622`, called at `:2581`: the update
      writes the block itself — its presence is the fact `fugaro run` reads — rather than asking the owner
      to type a URL) and the rule `VerifierAccountsEffective` states
      (`internal/localcfg/localcfg.go:1402`: a written list is the owner's, `[]` included). **The gate is
      the effective list, not the local field**, and the difference is not academic: a silent local field
      is not a project that names nothing. `fugaro update` runs on any operator's machine, and a
      teammate's config — or the owner's second laptop — predates the key, so a write gated on the local
      field alone fires there and puts the verifier's address
      (`fugaro-verifier@<project>.iam.gserviceaccount.com`) into a file that said nothing. The publish then
      sees it: `policyConfigDrift` compares `verifier_accounts` whenever the local config **states** one
      (`internal/cli/init_policy_publish.go:102`), and the publisher takes the file's list over the node's
      (`internal/cli/project_cmd.go:917`, `VerifierAccountsEffective`) — so the node that named the owner's
      verifier is replaced by the default, the credentials stage hands the service that list, and the
      owner's verifier is refused on the next credentials run. That is the failure the keep rule exists to
      prevent (C3.9g3, D1075), reached from a machine that never touched the list — and the publisher's own
      comment already says it, of the keep rule: the publish that dropped the list "would leave a service
      that refuses every verifier the owner named, from a machine whose config merely predates the key"
      (`internal/cli/project_cmd.go:909`). **With the gate on the
      effective list, a machine whose node already names a list writes nothing**, the drift comparison sees
      no member, and the node is byte for byte what it was. **Because the address is computed and not read
      from an output, this write does not wait on the apply**, so the policy stage of the *same* run
      publishes a node that names the account — which is the half CSR15's reader and the owner's `doctor`
      see. So the owner's own list stays theirs, an explicit `verifier_accounts: []` stays their clear, and
      the installation's account is the default they get **without typing anything**.
  - **What the default does and does not mean, in one paragraph, because the owner's list is a policy.** The
    account this module creates is the list's value **only while the block's member is absent**, and the
    member is absent only while the **effective list is nil** — nothing written in the local config and
    nothing named in the published node. The owner's list is rendered as it is, `[]` is rendered as `[]`
    (nobody), and the account is never added to either: the carry above writes the effective list into the
    block, so an owner's list or clear reaches the service as the same value the node carries, and the
    default is left for the one state in which nothing anywhere names a verifier. So
    the list the owner writes is the list the service admits, and the identity the verify route exists for
    is one the owner **can** switch off — with the published node carrying the same list the keep rule
    produces, which in the default case is the same single address the module renders. **No person is ever
    in either**: `policy.ValidVerifierAccounts` refuses an IAM member spelling and takes a **bare address**
    (`internal/policy/policy.go:526`), and `NewAuthenticator` refuses to start with one address in two
    classes (`internal/credsvc/auth.go:574`) while every operator is already a person account
    (`FUGARO_PERSON_ACCOUNTS`,
    `deploy/terraform/gcp/modules/installation/credentials.tf:497`) — **the task writes no person into the
    list**, and a list that named one is an installation whose credentials service never comes up.
- **Command:** `terraform fmt -check -recursive deploy/terraform`, then
  `go test ./deploy/terraform/ -run 'TestServiceAccountTokenCreator|TestInstallationServiceAccountGrants|TestJobAccountsProjectRoles' -count=1`,
  `GOFLAGS=-p=2 go test ./internal/infra/tf/ -run 'Cover|Grant|Golden' -count=1`, and
  `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestOrdinaryInit|TestCredentialsService|TestOrdinaryUpdateAdmitsTheVerifierAccount' -count=1`.
- **Tests:** the module's own positive test, `verifier.tftest.hcl` (with the service on: the account
  exists, **the service's own environment names it** — once with the block's `verifier_accounts` null (the
  variable carries the account **alone**), once with a list of the owner's (the variable carries **exactly**
  that list, the account **not** added) and once with an explicit `[]` (the variable is `[]` — nobody,
  which is the case a union would get wrong) — the operators hold the token-creator grant on it **and on
  no other account**, and the account holds `run.invoker` on
  the service; with the service off: **none of the four exists** — the plan is byte for byte what it is
  today, which is the promise the release's operator sentence makes);
  `TestServiceAccountTokenCreatorIsTheDeployersImpersonationGrant` (widened: the role
  appears at exactly the two sites, each literal, each on one account of this project, each for
  `var.operators` — and a third site, or one on a computed account, still fails);
  `TestInstallationServiceAccountGrantsNameTheirAccount` (unchanged in what it refuses: the verifier's
  grant names its account the way the module already spells one);
  `TestVerifierTokenCreatorIsCoveredByOneUpdate` (the cover table accepts the second site **and** still
  refuses a token-creator grant on any third account — the fence is narrowed to two, not opened);
  `TestOrdinaryInitAfterTheOptInNamesTheVerifierAccount` (the local config is silent **and the published
  node names nothing** — the effective list is nil, which is the gate — and the update writes
  the **computed** address into `verifier_accounts` — the same shape as
  `TestOrdinaryInitAfterTheOptInKeepsTheCredentialsService`, and with **no output in the loop**: a rig
  whose installation outputs carry nothing still gets the name, which is the half the old draft got from
  the apply);
  **`TestOrdinaryUpdateAdmitsTheVerifierAccountInOneRun` — the convergence test CLAUDE.md's HARD RULE
  requires, and the one this correction exists for.** An installation **at the previous version with the
  credentials service already on** — so its block is carried and the credentials stage is **not**
  registered, because the owner runs the one documented command with no flag — takes **one** ordinary
  `fugaro update` to the state this release ships, and both halves have to hold at the end of that one
  run: the service's own list names the account (the module's default, in the apply that creates it — the
  block is carried **null**, which is the state the default is for) **and**
  the policy node the same run published names it (CSR15's reader's source). It is the sibling of
  `TestInstallationAtThePreviousVersionConvergesInOneUpdateCredentials`
  (`internal/cli/init_credentials_test.go:1391`, the release-to-release convergence the credentials
  service already has), and it fails on the tree as it stands for the two reasons the *one `update`* bullet
  above names — the carried block and the stage that is not registered; and
  `TestCredentialsServiceKeepsTheOwnersVerifierList` (**both directions of the keep rule, held at the
  writer that can lose the list** — the update's own, where the tree's sibling
  `TestCredentialsServiceKeepsThePublishedVerifierList` (`internal/cli/init_credentials_test.go:939`)
  holds the credentials stage's: a config that **writes** a list — an explicit `[]` included — keeps it,
  and a config that is
  **silent while the published node holds the owner's list** writes nothing, so `fugaro update` on a second
  operator's machine leaves that node byte for byte as it was — the direction this correction added,
  because a write gated on the local field alone replaces the owner's list from a machine that never
  touched it), `TestOrdinaryUpdateCarriesTheOwnersClearIntoTheBlock` (**the carried-block direction of the
  same rule, and the one the review of this task found**: an installation whose local config writes
  `verifier_accounts: []` and whose carried block has **no** such member — the state every opted-in
  installation is in, because the member is new — takes **one** ordinary `fugaro update` to a block whose
  member is `[]` **and** a published node that is `[]`, so the service admits nobody and CSR15's reader
  and the service agree; it fails on the tree as it stands, where the member stays absent and the module's
  default admits the account the owner cleared), and a golden that moves for an installation **without**
  the service fails
  the module's own test: the five `installation*.plan.json` fixtures CSR7 touched carry no service and must
  not move, and `installation-credentials.plan.json` is the one that does.
- **Red step:** `TestVerifierTokenCreatorIsCoveredByOneUpdate` fails on the cover table as it stands: a
  plan that grants `roles/iam.serviceAccountTokenCreator` on the verifier account is refused
  ("which is not the deployer account of this run"), so `fugaro update` would refuse its own apply and the
  account would never exist — the HARD RULE's failure in its purest form. **And the second red step, which
  is the one the correction of this task added:** `TestOrdinaryUpdateAdmitsTheVerifierAccountInOneRun`
  fails on the tree as it stands for the reason the *one `update`* bullet names — with the block carried
  (`internal/infra/tfvars.go:612`) and the credentials stage unregistered
  (`internal/cli/init_stages.go:181`), **one ordinary update creates the account and admits it for
  nobody**, so the launcher's verify call and the owner's own check are both refused until a second run
  nobody was told to make. **SEC flag:** yes — the task adds
  **an identity an operator can become**, and its whole risk is a grant that is wider than the one route it
  exists for: the account must hold nothing else (no bucket, no secret, no invoker on anything but the
  credentials service), and the impersonation must be the operators' and nobody else's.
- **Mutation to hold it:** grant the verifier account the invoker role on a **job** rather than the service
  → the module's own test and `TestJobAccountsProjectRolesAreExactly`
  (`deploy/terraform/terraform_test.go:1943`) fail; take the address from the module's **output** instead of
  the literal account ID → `TestOrdinaryUpdateAdmitsTheVerifierAccountInOneRun` fails, because the name
  reaches the policy node a run late and the service's list a run later still; write the address from a
  flag or a config value → `TestOrdinaryInitAfterTheOptInNamesTheVerifierAccount` fails;
  overwrite a written list → `TestCredentialsServiceKeepsTheOwnersVerifierList` fails; **gate the write on
  the local field instead of the effective list** → that test's second direction fails (the node's list is
  replaced from a machine whose config is silent); render the account **beside** the block's list, the
  union → the module's own list case fails (the variable holds the owner's list and the account) and CSR15's
  single-caller rule is broken at the service, which is the finding this correction answers; drop the key
  instead of writing `[]` for the owner's clear → the module's empty-list case fails, because the default
  would be admitted on a route the owner had switched off; **carry the block verbatim and leave the member
  absent** → `TestOrdinaryUpdateCarriesTheOwnersClearIntoTheBlock` fails, because the default is read on an
  installation whose local config writes `[]` and whose node keeps `[]`, so the service admits a caller the
  node does not name and CSR15's reader refuses every fix round; render **only** the block's list and not the
  account the module creates → the module's null case fails and the convergence test fails with it (the
  carried block stays empty and the account is admitted for nobody); create the account
  unconditionally → the five goldens without the service move, and the module's negative case fails.
- **Reviewer tier:** **SEC — opus, with the independent review mandatory** (the repository's rule for a
  change on an IAM grant, and this one deliberately widens a fence that refused a second impersonation
  target).
- **OWNER-RUN:** none. Every assertion is hermetic: `terraform test` with the module's fakes, the static
  guards, a golden plan, and a `t.TempDir()` installation for the config writer. **The owner runs nothing
  new for this account** — it is created and named by the `fugaro update` the release already asks for — and
  the one command that uses it, `gcloud auth print-identity-token --impersonate-service-account=…`, is the
  live check's step 8, which is CSR12's file and CSR11's gate.
- **Commit:** `infra: the installation's verifier service account, and the update that names it`.

### CSR15. The launcher impersonates the verifier account (SEC)

**Decisions:** **the owner's answer of 2026-11-05 to Open item 5** (a dedicated verifier service account
the launcher impersonates) and **D1068** (what the verify route answers). **Findings:** none of the
reconciliation's; this is **gap 6**'s third half on the launcher's side, and it is a task of its own
because the task CSR10 became does **not** do it: PR #1764, now merged, leaves `reviewVerifierIdentity`
(`internal/cli/reviewverify.go:81`) returning the refusal "the launcher has no verifier identity to call
the credentials service's verify route as: the installation root creates a dedicated verifier service
account and names it in credentials_service.verifier_accounts, which this launcher impersonates once that
account is real (docs/cloud-senior-review/implementation-plan.md CSR14/CSR15: neither exists yet). No fix
round is launched on a verdict nobody verified" — **a sentence that names the verifier service account and
CSR14/CSR15 and never Open item 5**, which is the fail-closed stub the answer now replaces. **Depends on:** **CSR10** (the file, the
seam and the reader that calls it) and **CSR14** (the account it impersonates and the operators' grant on
it). **Before CSR11**: the flip admits a launch whose fix round the launcher cannot start, because its
verify call is admitted for nobody until this lands.

- **Files:** `internal/cli/reviewverify.go` (CSR10's, merged as PR #1764 — this task rewrites its stub),
  `internal/cli/reviewverify_test.go` (new). **2 files.**
- **Interfaces:** `reviewVerifierIdentity` stops refusing and mints the token, through the machinery
  `fugaro managed apply` already has for the same call:
  - **Which identity.** The **one** account the installation's published `verifier_accounts` names — the
    list `env.policy.read` already returns once per run (`policyReadOnce`,
    `internal/cli/launch_budget.go:226`/`:239`, `projectpolicy.Policy.VerifierAccounts`,
    `internal/projectpolicy/policy.go:323`), which is the same list the service's own environment is
    rendered from and therefore the only list whose caller the service admits. **A list of none refuses**
    ("this installation names no verifier, and a fix round is not launched on a verdict nobody verified")
    and **a list of two refuses** ("two identities, and the launcher's verify call is one caller"): a
    launcher that picked one of two would be choosing which identity verifies a verdict, which is the
    owner's to say and is exactly what the item settled.
  - **Who can actually do it, which follows from the owner's own wording and has to be said.** The mint is
    an **impersonation by the launcher's own person credential**, so it needs
    `roles/iam.serviceAccountTokenCreator` on the verifier account — and CSR14 grants that to
    `var.operators`, **and to nobody else**. A launcher who is **not** an operator holds no such grant:
    `local.launchers_only` is `setsubtract(toset(var.launchers), toset(var.operators))`
    (`deploy/terraform/gcp/modules/installation/iam.tf:217`, whose own comment is "A launcher who is also
    an operator needs nothing more (H4)") and their roles are the two bucket grants and nothing on this
    account. **So every fix round a launcher-only person launches is refused** — by IAM, before the verify
    call leaves the machine — and the refusal must be **CSR15's own sentence naming the fix**: the
    installation's operators are who may verify a verdict, and an owner who wants a launcher to verify adds
    them to `operators` (which is the same set the read mint's person class is built from,
    `FUGARO_PERSON_ACCOUNTS`). **That is a statement about the design and not a defect in it**: the item's
    answer named the operators as the identity's holders, the two sets are the owner's, and the alternative
    — a verifier the installation's launchers may impersonate — is a wider grant than the one route it
    exists for. The task **says it in the refusal and in the docs**, so a launcher-only person meets a
    sentence rather than a 403.
  - **How.** One `POST /v1/projects/-/serviceAccounts/<the account>:generateIdToken` to
    `impersonationEndpoint` (`internal/cli/managed_apply.go:642`, a variable so tests point it at their own
    server) with the **launcher person's own** credential and the audience the service checks — its own URL,
    the audience CSR10's `reviewVerifier` already holds — the same call `deployerImpersonator.identity`
    makes (`:823`, through `call`, `:872`) with a different audience and a different caller. **The
    deployer's own impersonation is deliberately not reused**: that credential exists to assume the
    deployer (D75), the deployer holds the installation's authority rather than one route's, and a launcher
    that reached the verify route *as the deployer* would be one caller in two classes at the service —
    the same refusal that keeps a person out of the verifier list (`internal/credsvc/auth.go:574`).
    `includeEmail: true`, and the email the token names is checked against the account the call was made
    for, the way `:823` checks it: a token for another identity is a refusal, never a credential.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestReviewVerifierIdentity|TestFixBrief' -count=1`.
- **Tests:** `TestReviewVerifierIdentityImpersonatesTheInstallationsVerifier` (a stand-in IAM Credentials
  server: the call names the published account, the audience is the service's URL, and the token the verify
  call then carries is the one that server minted — the red step),
  `TestReviewVerifierIdentityRefusesWithNoVerifierNamed` (an installation whose published list is empty, and
  one that published no policy at all: the refusal, and **no call to the impersonation endpoint**),
  `TestReviewVerifierIdentityRefusesTwoNames` (a list of two: the launcher does not choose),
  `TestReviewVerifierIdentityRefusesATokenForAnotherAccount` (the endpoint answers a token whose email is
  not the account asked for: refused, so a misconfigured endpoint or a proxy cannot become a credential),
  `TestReviewVerifierIdentityRefusesWhenTheImpersonationIsDenied` (the endpoint answers **403**, which is
  what **a launcher who is not one of the installation's operators** gets from IAM —
  `deploy/terraform/gcp/modules/installation/iam.tf:217`'s
  `launchers_only` holds no token-creator on the account: the refusal names the operators as the holders
  and the `operators` list as the fix, and never reads as a broken installation),
  and `TestFixBriefVerifiesWithTheImpersonatedIdentity` (**CSR10's own reader, one level down**: the fix
  brief's verify call carries the impersonated token and the refusal it gets back is the reader's, so the
  identity and the reader are one path and not two).
- **Red step:** `TestReviewVerifierIdentityImpersonatesTheInstallationsVerifier` fails because the function
  refuses today: `reviewVerifierIdentity` returns "" and the sentence above
  (`internal/cli/reviewverify.go:81`), which names the verifier service account and CSR14/CSR15 and never
  Open item 5, so every fix-round
  launch refuses with "the launcher has no verifier identity" — a fix round is never launched, whatever the
  installation publishes. **SEC flag:** yes — this is the launcher's one impersonation, and its risk is the
  pair CSR14's own review is asked for: that the identity is the installation's verifier and nothing else,
  and that the token is minted for the service's URL and for no other audience.
- **Mutation to hold it:** mint as the launcher person instead of impersonating → the stand-in server sees
  no impersonation call and the test fails; take the account from the local config rather than the published
  node → `TestReviewVerifierIdentityRefusesWithNoVerifierNamed` fails, because a config that names a
  verifier the node does not is a caller the service refuses; pick the first of two names → the two-name
  test fails; accept a token whose email is another account → the mismatched-token test fails.
- **Reviewer tier:** **SEC — opus, with the independent review mandatory.**
- **OWNER-RUN:** none. The impersonation is a call to a stand-in endpoint in every test; the owner's own
  `gcloud auth print-identity-token --impersonate-service-account=…` is the live check's step 8, which no
  agent runs.
- **Commit:** `review: the launcher's verify call as the installation's verifier account`.

### CSR16. The launcher reads the head through the credentials service's read mint (SEC)

**Decisions:** **the owner's ruling of 2026-11-05/06**, and it is the answer to the recommendation CSR10's
own head bullet wrote: the pull request's current head is read **through the credentials service's read
mint** — BB4's person class, **merged** (`internal/credsvc/githubread.go`) — and **not** from the
launcher's own App ID and key. **Findings:** none of the reconciliation's; the finding is in the merged
code: `reviewHead` (`internal/cli/reviewverify.go:97`) opens the provider with
`OpenProvider(os.Environ(), nil)` (`:117`), which resolves the App ID and the App key out of the
launcher's environment — and **a person's machine holds neither** (C2.2, D145: "the machine that reads
holds no App key at all", `internal/gitprov/github/serviceread.go:93`). The read works today only where
the launcher happens to hold a job's App credential, which is not the shape this design gives a person's
machine, and it cannot work at all once the installation's `credentials_service` URL is what the read
goes through. **Depends on:** **CSR10** (the file, the reader and `reviewHead` itself, merged as PR
#1764) and **CSR15** — **the same file**, `internal/cli/reviewverify.go`, which this task is ordered
after so that no level holds two writers of it (§6.4). **Before CSR11's release is worth anything to a
fix round**: the flip admits a review launch whose verdict the launcher reads, and the reader refuses
every brief until the head it demands can be read — which is why §6.5's operator sentence says
`--review-run` refuses until this task and CSR15 both exist.

- **Files:** `internal/cli/reviewverify.go` (CSR10's and CSR15's — this task rewrites `reviewHead` and
  its seam and nothing else in the file), `internal/cli/reviewverify_test.go` (CSR15's new file — this
  task adds the head read's tests to it). **2 files.**
- **Interfaces:**
  - **What changes.** `reviewHead` keeps its signature, its job and its refusals; only the credential it
    reads with changes. Instead of `OpenProvider(os.Environ(), nil)`, it resolves the installation's
    service URL from the local config's published block — the same source and the same refusal
    `reviewVerifier` already reads (`internal/cli/reviewverify.go:47`, `:51`) — and opens the provider
    with **a read-mint token source installed on it**: `github.ServiceReadTokens`
    (`internal/gitprov/github/serviceread.go:105`, the GitHub half) or
    `bitbucket.ServiceReadTokens` (`internal/gitprov/bitbucket/service.go:120`, the Bitbucket half),
    installed with `Provider.UseServiceTokens` (`internal/gitprov/github/github.go:163`). The service
    mints one read-only token per repository and the client checks the answer names the repository that
    asked — GitHub's `Repo(repo, appID)` (`internal/gitprov/github/serviceread.go:168`, which also carries
    the App the mint is signed as) and Bitbucket's `Repo(repo)` (`internal/gitprov/bitbucket/service.go:197`,
    which has no App at all) — and that is the same shape the coordinator's person read already uses
    (`ee/coordinator/cli/coordinator.go:1145`, `:1162`).
  - **The three values the mint needs, and each has one source.** The **service URL** from the
    installation's published `credentials_service` block (`localcfg.Config.CredentialsService`), never
    an environment variable — F23's rule for a person's read, and the same value `reviewVerifier`
    resolves. The **GitHub App ID** from the project's own configuration
    (`localcfg.Repo.GitHubAppID`), which is not a secret and is what the service signs the mint as
    (`internal/credsvc/githubread.go`: "the App ID is the caller's"). The **caller's own Google ID
    token**, minted with `gcloud auth print-identity-token` — the person class's own audience,
    `GcloudAudience` (`internal/credsvc/auth.go:258`), which is the only audience a **user** account can
    mint for and which the service's person verifier admits (`VerifyPersonCaller`,
    `internal/credsvc/server.go:335`, for the GitHub mint; `VerifyReadCaller`,
    `internal/credsvc/auth.go:890`, for the Bitbucket one). **The mint is written here rather than
    imported**: the tree's person mint is the coordinator's
    (`ee/coordinator/cli/coordinator.go:1380`), and
    `ee/coordinator/cli` imports `internal/cli` (`coordinator_intents.go:16`), so this package cannot
    import it — the two either duplicate the small mint or share one lifted into `internal/`.
  - **A Bitbucket repository takes the same path** with its own mint (`/v1/bitbucket/read-token`), and a
    provider with no read mint at all refuses the brief rather than falling back to the environment's App
    credential: the fallback is the bug this task removes.
  - **What does not change.** The head is still read with `FetchPRHead(ctx, number)`
    (`internal/gitprov/gitprov.go:344`) and still compared against the artifact's own `head_sha` by
    `VerifyReviewArtifact`; the replay refusal, the "a head the forge cannot be asked for refuses the
    brief" rule and `reviewPRHeadSeam` (the tests' seam, `:87`) all stay as CSR10 left them. **No new
    grant is needed for this read**: the read mint's permissions are the coordinator's read-only set
    (contents and pull requests read) and the caller is a person the installation already lists, so the
    task adds no Terraform, no IAM and no operator step.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestReviewHead|TestFixBrief' -count=1`.
- **Tests:** `TestReviewHeadReadsThroughTheReadMint` (a stand-in credentials service over `httptest`: the
  call is `POST /v1/github/read-token`, it names the repository and the project's App ID, it carries the
  caller's own ID token, and the token it answers with is the one the head read then sends to a stand-in
  forge — **the red step**), `TestReviewHeadRefusesWithoutACredentialsService` (a project whose config
  publishes no `credentials_service` block: the refusal, and **no call to the forge** — the same
  fail-closed shape `reviewVerifier` has), `TestReviewHeadNeverFallsBackToTheEnvironment` (the
  environment carries a job's App ID and key and the installation publishes no service: the read is
  refused rather than served by them, which is the mutation this task exists for),
  `TestReviewHeadRefusesATokenForAnotherRepository` (the service answers a token for a repository other
  than the one asked for: refused, so a misconfigured or hostile answer cannot become a read of another
  repository's head), `TestReviewHeadRefusesWhenTheMintIsDenied` (the service answers 403 or 401 — a
  person the installation does not list, or a service the caller holds no `run.invoker` on: the refusal
  names the read mint and the person's own identity, and never reads as a broken pull request), and
  `TestFixBriefReadsTheHeadThroughTheReadMint` (**CSR10's own reader, one level down**: the brief's head
  check uses the minted token end to end, so the credential and the reader are one path and not two).
- **Red step:** `TestReviewHeadReadsThroughTheReadMint` fails on the tree as it stands:
  `reviewHead` calls `OpenProvider(os.Environ(), nil)` (`internal/cli/reviewverify.go:117`), so with no
  App ID and no App key in the environment the provider is opened with no credential and the head read
  fails — and with the installation's service URL published the read has nothing to do with it at all.
  **SEC flag:** yes — this is a **read of the forge made with a credential the credentials service
  mints**, and its risk is the pair the read mint's own review is asked for: that the token is read-only
  and one repository's (the service's own permission set and its repository check are what make that
  true), and that the launcher's fallback path is gone rather than merely unused.
- **Mutation to hold it:** keep `OpenProvider(os.Environ(), nil)` and add the mint beside it →
  `TestReviewHeadNeverFallsBackToTheEnvironment` fails; take the service URL from an environment variable
  instead of the published block → the same test's second direction fails, and a machine that exports a
  URL reads differently from one that does not (F23); take the App ID from the environment rather than
  the project's own config → `TestReviewHeadReadsThroughTheReadMint` fails, because the call names the
  wrong App; accept the token the service answers without checking the repository it names →
  `TestReviewHeadRefusesATokenForAnotherRepository` fails.
- **Reviewer tier:** **SEC — opus, with the independent review mandatory.**
- **OWNER-RUN:** none. The mint and the forge are stand-ins over `httptest` in every test; the owner's
  own `gcloud auth print-identity-token` is the same mint by hand, and it is the live check's step 8,
  which no agent runs.
- **Commit:** `review: the launcher reads the head through the credentials service's read mint`.

### 6.3 The edges, the levels and the critical path

**Re-drawn for the 0.20.0 set the owner cut on 2026-11-06**, and the cut is what the drawing shows:
**CSR13, CSR14, CSR15, CSR16, then the owner's live checks and CSR11** — the OAuth review alone — with
**CSR17 and CSR18 (§6.7, the api-key review) outside every level below, outside the critical path and
outside §6.4**, because they are a later release's and nothing in 0.20.0 waits on them.

```
MERGED AND CLOSED (each a task that landed; none of them is a level's work any more)
  CSR8  (the runner places the variant)      **MERGED, #1761** — CSR13 reads the class it landed
  CSR9  (the command is registered)          **MERGED, #1762** — CSR11 inherits its launch refusal
  CSR10 (the launcher verifies the verdict)  **MERGED, #1764** — CSR15 fills the seam it left
  CSR12 (the live-check switch)              **MERGED, #1771** — deleted by CSR11's flip, 2026-11-08

LEVEL 1 (no dependency between them, disjoint files)
  CSR13 (the review run's model path — the OAuth review, and the refusal when the budget is off)
  CSR14 (the installation creates the verifier account, and one update names it)
LEVEL 2
  CSR15 (the launcher impersonates that account)          after CSR14 — and CSR10 has merged
LEVEL 3
  CSR16 (the launcher reads the head through the read mint) after CSR15 — the SAME file, so ordered

then, and not a level:  [the live checks: C3.9's eleven steps, Check 125's 6 and 7]  (a person's, unrun)
DONE 2026-11-08:        CSR11 (the flip, and the switch's removal) — the owner's ruling of 2026-11-07
                        lifted the live-check gate, so the checks are owed AFTER the flip, not before

OUTSIDE ALL OF IT, and NOT in 0.20.0:  CSR17 (the service serves a provider key) -> CSR18 (the runner
                                       reaches a provider's endpoint)   -- §6.7
```

- **Edges.** `CSR8 → CSR11` and `CSR9 → CSR11` are **satisfied**: both have merged, so the flip's two
  oldest dependencies are facts in the tree rather than tasks ahead of it. **`CSR10 → CSR11`** (the flip
  admits a launch that a fix round then reads: [spec.md](spec.md) §8's window is a concurrent job on the
  same shared job writing under `runs/`, and **the signature is what closes it**, so a flip ahead of the
  verifier is the ordering the design already refuses) — and CSR10 has merged, so what CSR11 waits on
  there is CSR15, which is the launcher's half of that reader. **`CSR13 → CSR11`** (a flipped constant
  launches a run whose reviewer has no credential of its own: without the class's model path the launch
  this task admits reaches no model at all, gap 7). **`CSR14 → CSR15`** (Open item 5's answer): the
  launcher impersonates the account the installation creates, so the account exists first — and the two
  are separate tasks because they touch disjoint trees (§6.4), one an installation's Terraform and the Go
  that covers it, the other the launcher's reader. **`CSR15 → CSR16`** (the same file,
  `internal/cli/reviewverify.go`: the impersonation fills one seam and the head read rewrites the other,
  and §6.4's one-writer rule is why they are two levels and not one task). **`CSR14 → CSR11`** (the third
  half of E7 is a name the installation publishes; the live check CSR11 waits on is admitted for nobody
  without it). **`CSR15 → CSR11`** (the flip admits a launch whose verdict the launcher's own verify call
  cannot be admitted for until it impersonates that account). **`CSR16 → CSR11`'s release, and not
  `CSR16 → CSR11` the task**: the flip admits a **review launch**, which needs no head read; what needs
  one is the **fix round** the launcher starts from a review's verdict (`--review-run`), which is the
  path §6.5's operator sentence is about. **`CSR8 → CSR12`, `CSR9 → CSR12` and `CSR10 → CSR12` are not
  edges of the code**: the switch's own change touches one file, and CSR12 has merged anyway.
  `CSR12 → CSR11` (the switch exists only for the check, and the flip's commit is the one that removes
  it). `CSR13 ‖ CSR14` — **two parallel tasks and not a chain**: the runner's class and the installation's
  verifier account touch two disjoint sets of files (§6.4), and **neither may be serialised behind the
  other on the strength of this drawing**. `CSR11 → nothing`: it is last, and nothing here depends on the
  flip. **No edge reaches CSR17 or CSR18**, and neither of them is an edge's source.
- **Levels.** **Level 1: CSR13, CSR14** — none depends on another, and no two of them holds one file
  (§6.4); the three tasks that used to be drawn at this level are **merged and closed** (CSR8, CSR9,
  CSR10), and so is the one the drawing before this one put a level below them (CSR12, merged as #1771),
  and CSR13 is here rather than deeper because its only stated dependency was the class CSR8's
  merged code already keys on — **Open item 6**, the budget mode, was the last thing that held it and it
  is answered (the refusal, 2026-11-05/06). **Level 2: CSR15** — after CSR14, because the account is
  CSR14's. **Level 3: CSR16** — after CSR15, because the two hold one file. **Then CSR11** — after the
  four, and after the live checks, which are a person's and which no level holds; **it landed on
  2026-11-08 without them**, on the owner's ruling of 2026-11-07 that lifted that gate (the DONE line of
  the drawing above), so what the path below calls the checks are owed **after** the flip now.
- **Critical path.** `the longest of (CSR13, CSR14) → CSR15 → the owner's live checks → CSR11`: **three
  tasks deep and one person's afternoon**, with **five of §6's nine tasks still to be built and four
  merged** (CSR8, CSR9, CSR10, CSR12). **CSR16 is a fourth task and it is deliberately not on that path**:
  the flip does not need a head read — the fix round the reader exists for does, and the release notes say
  so (§6.5) rather than a level holding it. The longest wait in the path is not code: it is the live check,
  which needs the credentials service enabled on a real installation (E7's first half), the verifier
  account **CSR14 creates and names** published beside it (E7's third half), and an owner at a terminal.
  That is the honest critical path of a design whose last link is another work stream's, and it is why §5
  says the release ships before the chain works.

### 6.4 Shared files, by level: no level holds two writers of one file

The rule is §2's, applied to the **0.20.0** set of §6.3: a file is one writer's at a time, and **within a
level no two tasks may hold one**. Every file below belongs to exactly one task in exactly one level — the
four merged tasks are **in the `merged, where it ran` column and not under a level**, because their levels
have already run and the three level columns beside it are what is left to build — and **§6.7's CSR17 and
CSR18 are not in this table at all**, because no 0.20.0 level holds them and they are a later release's:

| File | merged, where it ran | Level 1 (CSR13, CSR14) | Level 2 (CSR15) | Level 3 (CSR16) | then (CSR11) | Note |
| --- | --- | --- | --- | --- | --- | --- |
| `internal/runner/projectjob.go`, `internal/runner/projectjob_test.go` | **CSR8 — merged (#1761)**, its level 1 | — | — | — | — | the candidate set, the class and the token axis — **CSR13 reads what it left** |
| `internal/runner/project.go`, `internal/runner/project_test.go` | **CSR8 — merged (#1761)**, its level 1 | — | — | — | — | the admission path |
| `internal/config/claude_accounts.go`, `internal/config/claude_accounts_test.go` | **CSR8 — merged (#1761)**, its level 1 | — | — | — | — | the axis mapping lands once, here (CSR7 and E1 have merged; this is the file after them) |
| `internal/cli/root.go`, `internal/cli/root_test.go` | **CSR9 — merged (#1762)**, its level 1 | — | — | — | — | the registration |
| `website/content/cli-reference.md` | **CSR9 — merged**, its level 1 | — | — | — | — | **generated** by `go run ./ee/clidoc` |
| `plugin/skills/working/SKILL.md` (+ `routing/SKILL.md` if it names the flow) | **CSR9 — merged**, its level 1 | — | — | — | — | the sentence and the registration are one commit |
| `internal/runner/reviewartifact.go`, `internal/runner/reviewartifact_test.go` | **CSR10 — merged (#1764)**, its level 1 | — | — | — | — | the verifier's implementation |
| `internal/cli/run_test.go` | **CSR10 — merged (#1764)**, its level 1 | — | — | — | — | the `TestFixBrief*` fixtures gain the sidecar a fail-closed reader needs |
| `internal/cli/review.go`, `internal/cli/review_test.go` | **CSR10 — merged (#1764)**, its level 1, and **CSR12 — merged (#1771)**, its level 2 | — | — | — | **CSR11 — DONE 2026-11-08**, its level | the verifying reader and the live-check switch have run, and so has the flip that removed the switch — **three writers, one per level, and all three have run** |
| `internal/cli/reviewverify.go` | **CSR10 — merged (#1764)**, its level 1 | — | **CSR15** | **CSR16** | — | the seam and the fail-closed stub (PR #1764), then the impersonation that fills it, then the head read that stops reading with the machine's own App credential — **three writers, one per level** |
| `internal/cli/reviewverify_test.go` (new in CSR15) | — | — | **CSR15** | **CSR16** | — | the impersonation's own tests, then the head read's |
| `internal/runner/gateway.go`, `internal/runner/gateway_test.go`, `internal/runner/routing.go`, `internal/runner/routing_internal_test.go`, `internal/runner/agentenv.go`, `internal/runner/agentenv_test.go` | — | **CSR13** | — | — | — | the class accessor and its five readers — **level 1, and disjoint from CSR8's merged files** |
| `internal/runner/runner.go` | — | **CSR13** | — | — | — | **taken alone, for one call site**: §2's one-writer rule, and the task says so in its pull request |
| `deploy/terraform/gcp/modules/installation/verifier.tf` (new), `…/iam.tf`, `…/credentials.tf`, `deploy/terraform/gcp/roots/installation/tests/verifier.tftest.hcl` (new) | — | CSR14 | — | — | — | the account, its two grants, the `FUGARO_VERIFIER_ACCOUNTS` entry and the address the installation is named by |
| `deploy/terraform/terraform_test.go` | — | CSR14 | — | — | — | the two static guards, **deliberately** widened to a table of two sites |
| `internal/infra/tf/cover.go`, `internal/infra/tf/cover_test.go` | — | CSR14 | — | — | — | the one confirmation's tables: without them `update` refuses its own plan |
| `internal/infra/tfvars.go`, `internal/infra/credentialsvars_test.go` | — | CSR14 | — | — | — | the account's ID constant, beside `CredentialsAccountID` — the Go side computes the address from it **before** the apply — and the carry that writes the block's `verifier_accounts` member from the effective list, so an owner's written `[]` reaches the module as a clear rather than as an absent key the default fills |
| `internal/cli/init.go`, `internal/cli/init_credentials_test.go` | — | CSR14 | — | — | — | the update names the account only when the **effective** list is nil (the config silent and the published node empty) |
| `internal/infra/tf/testdata/golden/installation-credentials.plan.json` | — | CSR14 | — | — | — | **regenerated** by `scripts/gen-golden-plans.sh`; the five fixtures without the service must not move |

**`internal/cli/review.go` is the one file three tasks wanted, and that is why their order was a rule rather
than a preference:** the verifying reader (CSR10) wrote it in level 1, the live-check switch (CSR12)
added and read one variable in level 2, and the flip (CSR11) set the constant and **deleted the switch**
in the level after — **and all three have run**, the flip on 2026-11-08, so the row is closed and no
level ever held two writers of it. Serialising three writers of one file is what the levels buy, and it is also why CSR12 existed
as its own task rather than as a paragraph of CSR11: a switch that has to be removed by a later commit is
a change with its own red step, its own tests and its own review, and the reviewer of the flip is the
reader who has to see the removal in the same diff. **`internal/cli/reviewverify.go` is the same shape at
three levels** (CSR10's seam, merged; CSR15's impersonation; CSR16's head read), and it is three tasks
rather than one because the second was an Open item while the first was not — the seam could be built
fail-closed, and the identity it needs is the owner's to settle, which is exactly what the stub's own
refusal sentence says — and because the third is a **different credential of the same file's other
function**: the verify call is minted by impersonation and the head read is minted by the read mint, and
one writer per level is what keeps the two reviews apart.

**Two files this section does not take, deliberately.** `internal/cli/recipes_skew.go` and
`internal/cli/validate.go` remain **CSR6's** (§2), and no task here books them: CSR8–CSR16 add **no
`fugaro.yaml`, task, recipe or wire key**, so the `…Since` rule (E6) gives them nothing to gate. The one
constant whose *value* the release slot decides — `reviewJobSince`
(`internal/cli/recipes_skew.go:467`, `"0.20.0"` since CSR11's flip of 2026-11-08) — is **not** moved by
CSR8–CSR16: it is the release slot's consequence, and **Open item 3's answer of 2026-11-05 is 0.20.0
"Quokka"**, so the value names that release — and **CSR11's flip of 2026-11-08 is the change that moved
it**, taking that file alone under §2's rule: 0.20.0's own change had nothing to move while every launch
was refused, since this gate was never reached. **§6.7's CSR17 and CSR18 are not in this table and not in
these levels**: their own release books their files, and the only 0.20.0 file they could have touched —
`internal/credsvc`'s class — is nobody's in 0.20.0.

### 6.5 The release, the HARD RULE and the operator step

- **The HARD RULE holds, and these nine tasks do not add a step to it.** `fugaro update` is the whole
  upgrade: the command's registration, the reference rows, the plugin's skill text, the runner's placement
  and the launcher's verifier all ride the **image refresh** `update` already asks about (CSR6's), and none
  of them is a second command, a deletion or a manual repair. The plugin pins move through
  `scripts/bump-plugin-version.sh` with the release, not by hand and not per task. **CSR12's switch added
  nothing to `update` either**: it was an environment variable read only in the live check's own session,
  it was off by default, it was never written to a config file, and **CSR11 took it out on 2026-11-08**.
  **Nor does CSR14's
  verifier account**: it is a resource the same apply creates beside the credentials service, and the same
  `fugaro update` writes its address into the local config — so the account is **created and
  named by one command**, and the owner's list, written or empty, stays the owner's (the keep rule,
  `internal/localcfg/localcfg.go:1402`). **The credentials service's own opt-in is not what gates that**:
  CSR14's guard is `credentials_service_on = var.credentials_service != null`
  (`deploy/terraform/gcp/modules/installation/credentials.tf:25`), and while that expression is unchanged,
  **what it evaluates to is a premise another work stream is moving** — `docs/credentials-service/default-on.md`
  (PR #1769) answers that the service is deployed for **every** installation, so an installation that today
  is in the "off" half of that condition would converge into the "on" half. **Both states are correct as
  written**, because the guard reads the block and not a flag: an installation with no service gets no
  verifier account and no admission to lose, and one that has it gets the account and the name in the same
  update. **And the "on" half is not 0.20.0's**: the owner **cut #1769 from 0.20.0 on 2026-11-06**, so for
  this release the credentials service stays **opt-in**, the "off" half stays populated, and the owner
  **opts in on their own installation** for the live check CSR11 gates the flip on. Default-on is a later
  release's, and CSR14's premise is today's state — `count = credentials_service_on`, correct under either,
  as its own bullets say. Two forward references follow from #1769 and are recorded here rather than
  discovered later: **#1769's own task renders the verifier name** for every
  installation and names CSR14 as its precondition, and its §4.3 changes the premises of CSR10, CSR12 and
  CSR13 — the launcher's verifier, the live check's switch and the class's model path all assume a service
  that is deployed and serving rather than one an operator chose. **None of the three is a 0.20.0
  premise**: in 0.20.0 the service is the operator's choice, the live check's step 3 runs with the switch
  (CSR12, merged) on an installation the owner opted in, and the class's model path reaches a service the
  installation published.
- **The release's `### For operators` entry is conditional today, and 0.20.0 keeps it conditional.** The one
  sentence applies to the installations that **opt in to the credentials
  service**: the cloud senior reviewer's read-only token and its model calls both come from that service
  (D893 (c), O1), so an installation without one has the command, the variant and the verifier and still
  cannot run a review — and says so in the refusal rather than in a step. **`docs/credentials-service/default-on.md`
  (PR #1769) would answer that the service is on for every installation, and the owner cut it from 0.20.0
  on 2026-11-06**: so the entry stays conditional for this release, and the sentence changes from "the
  installations that opt in" to "every installation" only in whichever later release lands #1769, with the
  credentials service's own documentation moving with it. **The owner's own installation is one of the
  installations that opts in**, which is what makes the live check's step 3 reachable at all: the check is
  run by the owner, on their own installation, after they have enabled the service. For an installation
  that **does** have the service, the same entry names the three halves of E7
  as they are: `fugaro update --provision-credentials`, `fugaro creds
  set github-app-key`, **and `credentials_service.verifier_accounts` naming the verifier service account
  (Open item 5's answer, 2026-11-05)** — and of those three, **the first two are commands the credentials
  service's own documentation already asks a person for** (its opt-in flag, and the App key that was never
  a value this design could supply — §1, gap 6), while **the third is not a step at all** — the
  installation root creates the account and the
  same update writes its address into the local config **when nothing has named a verifier yet** (CSR14),
  which the policy stage then publishes and
  the credentials stage renders into the service's own environment. So the operator sentence is a
  statement of what `update` will do and what it will ask, never a list of commands to run after it, and
  the one thing it asks for is the confirmation `update` already asks about its plan. **The one case that
  is a step is the owner's own list**: an installation whose local config **writes** `verifier_accounts`
  keeps it, `[]` included, and an owner who wants a verifier other than the installation's writes it
  themselves — that is the keep rule doing its job rather than a repair, and it is the same thing the
  credentials-service block already does for a value nobody can type by hand. **The same update carries
  that list into the block the module reads** (CSR14), the owner's clear included, so the service admits
  exactly what the published node names, rather than the module's default stepping in
  over it. An installation that never
  opts in converges to the new job set and behaves exactly as it does today: **no operator step, and
  nothing to uninstall.** **One sentence the entry carries for 0.20.0 and no later release does:** a
  **fix round** launched from a review's verdict — `fugaro run --review-run RUN` — **refuses until the
  launcher can both impersonate the installation's verifier (CSR15) and read the pull request's head
  through the credentials service's read mint (CSR16)**, because the reader refuses every brief it cannot
  verify against the current head; the review launch itself, which is what CSR11 flips, needs neither. It
  is a sentence and not a step: both halves ride the same `fugaro update` and the same image refresh, and
  nothing in the release asks the owner to do anything about it.
- **One tension to record rather than resolve, because the file is another folder's.**
  `docs/c39-review-identity/implementation-plan.md:713` says of the credentials-service work "**`fugaro
  update` owes nothing new** … no new grant" — written before Open item 5 was answered, and **no longer
  true of this design**: the verifier account is one new service account, two new grants (the operators'
  token-creator on it, its own `run.invoker` on the service) and the cover tables that let the one
  confirmation carry them. Nothing is broken by the difference — that plan's sentence is about C3.9's own
  scope, and the account is CSR14's — but the next reader who checks one against the other should find the
  difference named here rather than discover it.
- **Release actions, for the 0.20.0 "Quokka" release Open item 3 answered:** the release's own
  `fugaro update`, that one conditional operator sentence, and the file §6.4 says the slot decides. That
  file's constant names **`"0.20.0"`** and **CSR11's change of 2026-11-08 is what moved it, not 0.20.0's
  own change**: while `reviewJobAdmitted` was `false` the gate was never reached, and 0.19.0's image
  holds the variant with no model path to serve a launch on it (§6.4), so the flip took CSR6's file as
  §2's rule allows. **CSR11's flip
  is not a release action**: it is a commit that lands in whichever release carries it — **it landed on
  2026-11-08**, on the owner's ruling of 2026-11-07 that lifted its live-check gate, and 0.21.0 is the
  release that carries it — and it is the commit that took CSR12's switch out. **The owner's live checks
  are the one thing the flip left behind rather than in front of it**: they are still unrun, and the
  release notes say the launch is admitted rather than that the check passed.
- **The verifier identity is the release's one deployment-shaped fact, and Open item 5 answered it on
  2026-11-05.** `credentials_service.verifier_accounts` is a **bare-address list rendered into the
  service's own environment** (`deploy/terraform/gcp/modules/installation/credentials.tf:482`, from the
  optional variable of the same module) — the eighth name on a service whose seven are the rest — and **the
  account named may not be a person**: every `user:` operator is already in
  `FUGARO_PERSON_ACCOUNTS` (`:497`) and `NewAuthenticator` refuses one address in two classes
  (`internal/credsvc/auth.go:574`). **So the entry is a verifier service account the installation's own
  root creates (CSR14), the operators hold token-creator on it, and the launcher impersonates it (CSR15) —
  and the owner's own check does the same thing by hand**,
  `gcloud auth print-identity-token --impersonate-service-account=<the account>`: the same identity, minted
  two ways, which is what makes the check a check of the launcher's own path rather than of a path nobody
  runs.

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
**`reviewJobSince` is `"0.20.0"`** — `docs/release.md:228`'s bullet verifies it against the release
whose runner can serve the launch, `internal/cli/recipes_skew.go:467` holds it, and a wrong value refuses
too late (a launch names a variant the installation has not deployed, or a runner that cannot serve one)
or too early (an installation that holds both stays blocked). **The move waited for CSR11's flip**:
0.20.0's own change did not take it, because while `reviewJobAdmitted` was `false` this gate was never
reached, and 0.19.0's image holds the variant with no model path to serve a launch on it — a value that
admits it starts the review, bills for it and kills it inside the job. **CSR11's change of 2026-11-08
moved it**, taking that file alone, under §2's rule and §6.4's note.

**4. *(found while writing this section)* The review run's model path. — ANSWERED 2026-11-05: a FOURTH
RUNNER TASK, `CSR13`, and not reading 1.** O1 says the reviewer's model
calls go through the credentials service's review class, and that class exists on the service's side
(`internal/credsvc/cascade.go:105`, merged with C3.9a–C3.9g). **The runner side has no arm for it.** The
gateway — the only thing that can forward to the service — is enabled by `GatewayOn`
(`internal/runner/routing.go:158`) only for `api-key`, `vertex` and `cascade`; the service is reached only
from the cascade arm; and a run whose `agent.auth` is `cascade` is refused in bootstrap by O2's arm
(`internal/runner/routing.go:60`) unless the **operator publishes `routing.claude: {via: cascade}` and
`budget.allowed_models`** — the two things [spec.md](spec.md) §2 says a review job does not need, because
that switch is the operator's consent for a *coding* run's cascade while a review job's credential is
decided by its own class. So as the tree stands, a review run reaches a model only by an operator writing
the coding cascade's consent for it. Two readings stood here, and **the owner took the second**:

- **an operator step**: a review-enabled repository writes `agent.auth: cascade` and the project policy
  publishes the switch and the allow-list. Cost: the repository's own file changes and the project's
  policy changes; the security consequence is that the switch's **meaning is stretched** — it becomes the
  consent for a review job's credential too, while spec §2 says the review needs no such consent, and the
  next reader cannot tell which one the operator meant.
- **a fourth runner task — and this is the answer, written below as `CSR13`**: the review job's model
  credential is keyed on
  its **class** and not on `agent.auth`, so a marked review run reaches the service's review class without
  the operator's coding-cascade switch, and O2's arm does not fire for it. Cost: another SEC task, with the
  independent review mandatory, over `internal/runner/gateway.go`, `internal/runner/routing.go` and
  `internal/runner/agentenv.go` — and a second path through the bootstrap routing decision, which is the
  most carefully-worded ladder in the tree. **This reading is the one O1 describes**, and it is why the
  gap was an Open item and not a note. **Why, in one line:** reading 1 makes a repository's own file the
  place a *review* job's credential is consented to, which is exactly the sentence spec §2 refuses, while
  the class is a fact the launcher already stamps on the run — so the answer is the one O1 describes, and
  the cost the item named (a second path through the ladder) is what CSR13's SEC review and its
  second-direction tests are for. **CSR13 reads the class CSR8's merged code already keys on** —
  `r.runClass()`, the task's mode, and the class of the job the run was admitted to
  (`internal/runner/projectjob.go:82`, `:50`) — **and never the launcher's environment marker**
  (`internal/runner/reviewartifact.go:934`), which is a fact about what the run may write to the pull
  request rather than about which credential serves its calls: the environment is the one input a launch
  writes, so a class keyed on it is a class a container can claim. CSR13 has no unmerged dependency, and
  **must precede CSR11** (a flipped constant launches a reviewer with no credential of its own).
  **The budget mode was the one half of the class this item did not settle**, and it was raised as
  **item 6** below rather than decided here — **and the owner answered it on 2026-11-05/06**: the class
  refuses a review run whose merged mode is `off`, in a sentence that names `budget.mode`.

**5. *(found while reviewing the first draft of §6)* Who may verify a verdict — and it cannot be the
owner.** **ANSWERED 2026-11-05: reading 1, a DEDICATED VERIFIER SERVICE ACCOUNT the installation's root
creates; `CSR14` builds it and `CSR15` makes the launcher impersonate it.** §6 was written with the
owner's own address as the verify route's caller ("so the owner's own
identity has to be in that list"). **The service refuses to start with it there.** Every `user:` operator
is rendered into `FUGARO_PERSON_ACCOUNTS`
(`deploy/terraform/gcp/modules/installation/credentials.tf:497`, from the same `var.operators` the read
mint's person class is built from), and `NewAuthenticator` refuses an address that is **both** a person
and one of the verifiers — "a person account is also one of the verifiers, so one caller would be two
classes" (`internal/credsvc/auth.go:574`) — so `credentials_service.verifier_accounts` naming the owner is
an installation whose credentials service never comes up. The same conflict stood in the live check:
**step 8** of `docs/c39-review-identity/live-check.md` had the owner's identity in the verifier list and a
hand-made `POST /v1/review/verify` carrying the owner's own ID token — **and CSR12 is the task that writes
step 8 to the account below**, so the file stops contradicting the design in the same commit as the switch
it is written for. Three readings stood here, and **the owner took the first**:

- **a dedicated verifier service account the installation's root creates — and this is the answer, built
  by `CSR14` and impersonated by `CSR15`**, named in
  `verifier_accounts`, which the launcher (and the owner's own check, with
  `gcloud auth print-identity-token --impersonate-service-account=…`) impersonates, with the operators
  granted the token-creator role on it. Cost: one service account and one binding in the installation
  root; the security consequence is that **anyone who can impersonate it can ask the verify route** —
  which is that route's own design: it answers **one refusal bit**, echoes no key and names no account
  (D1068), so the class's whole risk is the account oracle the design already refused to build. **Why, in
  one line:** it is the only reading in which the owner's own check can run at all — the second waits on a
  service that does not exist yet, and the third is the refusal above in the other direction — and it is
  the reading that keeps `fugaro update` the whole upgrade, because the account is created and named by
  the one command rather than typed by a person (§6.5). **The three parts the answer names, each with its
  task:** the account and its two grants (the operators' token-creator on it, and its own `run.invoker`
  on the credentials service) are **CSR14's**; the launcher's mint is **CSR15's**, because the task CSR10
  became does not do it — PR #1764's `reviewVerifierIdentity` refuses with the sentence at
  `internal/cli/reviewverify.go:81`, which names the verifier service account and CSR14/CSR15; and the
  owner's hand-run check is **CSR12's** step 8, which is the same identity minted the same way by hand.
  **No person is in that list**, which is the one thing this reading may not do.
- **the coordinator's own identity, once the coordinator exists** ([spec.md](spec.md) §2's resident
  service): the verifier class exists for a non-person caller, and the service's own comments say so where
  they refuse a `serviceAccount:` member in the *person* list "because admitting a `serviceAccount:` one
  would admit exactly the non-person caller the class exists to refuse (the coordinator's resident
  service, G4)" (`deploy/terraform/gcp/modules/installation/credentials.tf:73`). Cost: the check's step
  8 then cannot be run by hand at all until the coordinator exists — and **the owner's check is this
  design's gate**, so a verifier identity that only the coordinator holds blocks the gate it precedes.
  This is the reading the owner did **not** take, for exactly that reason.
- **not the review job's own account**: it is already a class of its own (`credsvc`'s review class), and
  one caller in two classes is the refusal above, in the other direction.

**6. *(found while the class of item 4 was re-read after CSR8 merged)* What a review run does when the
installation's budget is OFF. — ANSWERED 2026-11-05/06: THE REFUSAL — a cloud review does NOT run with
the budget off, and the sentence names the setting to turn on. Not the class forcing the gateway on.**
The owner ruled it on 2026-11-05/06, and the ruling is reading (b) below: a review class run whose
merged mode is `off` stops in bootstrap with a sentence that names **`budget.mode`**, published as
`observe` or `enforce`, as the thing to turn on — a fix an operator can perform, and one the review
job's own file has nothing to do with. **The class does not force the gateway on** (reading (a), which
the ruling refuses): the mode keeps one meaning for every class of run, and the ledger never runs on a
run whose installation set no budget. **CSR13 carries the refusal and its named test**, and the item is
**no longer blocking** for that task.
CSR13's class is `cascade`, and `cascade` reaches a model **only** through the gateway: `GatewayOn`
(`internal/runner/routing.go:158`) requires the budget mode to be `observe` or `enforce`, an unset mode is
`off` (`internal/runner/spend.go:97`, `:98`), and a cascade run with no gateway is refused in bootstrap by
`agent.BuildEnv`'s own arm — "auth: cascade runs through the gateway, and this agent has none: a cascade run
has no credential of its own to fall back to (D143)" (`internal/agent/env.go:169`). So on an installation
whose mode is off, the class selects a credential and gives the run no way to use it: **the reviewer's first
call dies with a sentence about the cascade, written for a coding run and naming a fix a review job cannot
apply** — it has no `agent.auth` of its own to change and no token to fall back to. `applyRouting` judges
the review class's two arms (O2's switch and the allow-list, both above) and neither of them is about the
mode, so today nothing in the class's own path says what the mode must be.

**How narrow this is, and why the refusal is the right size for it.** The mode a run has is the **merged policy's**, not
the job environment's alone (`internal/runner/policy.go:151`, where `""` becomes `off`): the published
node replaces the ceiling key by key, so an installation that publishes `budget.mode: observe` (or
`enforce`) turns the gateway on for a review run even when the job it was deployed with carries no mode at
all (`internal/infra/spec.go:1266` sets the deploy-time mode only when it is not `off`, and
`internal/localcfg/localcfg.go:622` is where "no budget block" means `off`). The state that breaks is
therefore **an installation that has configured no budget anywhere** — no deploy-time mode and no node
mode — which is a legal and quietly common state: nothing in the credentials-service work or in this
section requires an owner to have a budget, and the service's own opt-in was never conditioned on one.
**Two readings stood here, and the owner took the second:**

- **(a) the class forces the gateway on — the reading the owner refused —**, in the installation's own
  mode when it is on and in the gateway's non-enforcing mode when it is off (`internal/runner/gateway.go:145` already starts the gateway
  in `Observe` unless the mode is `enforce`). The review run reaches the service on every installation, and
  "off" keeps meaning what the owner set — no ceiling enforced. Cost: the mode gains a **second meaning**
  for one class of run — a run whose gateway is on while its installation's budget is off — and the ledger
  then runs on a run whose price table the tree deliberately does not build when the mode is off and no
  overrides exist (`internal/runner/spend.go:121`, `:129`, against `spend.go:154`'s rule that a budget-off
  run is priced by the same rules as one with it on).
- **(b) the class refuses, naming the mode — and this is the owner's answer of 2026-11-05/06.** A review class run whose
  merged mode is `off` stops in bootstrap with a sentence that names the fix: the review job's model calls
  are served by the credentials service, the run reaches it through the gateway, and the gateway is on only
  in `observe` or `enforce`, so the operator publishes `budget.mode` and publishes the policy. Cost: a
  review cannot run at all on an installation with no budget — including, today, the live check CSR11 gates
  the flip on, unless the owner's own installation has one — and the refusal is the *second* thing a review
  launch can need an operator for. **Why the owner took it:** one meaning for the mode, no new
  gateway state, and the sentence the run would otherwise die with is exactly the failure CLAUDE.md's HARD RULE
  cares about in miniature — a refusal naming a fix nobody can perform — which is why the refusal CSR13
  writes names the setting and not the cascade.

**What is not in question either way:** the review class never falls back to a credential of its own (D143,
O1) and the budget mode never decides *which* credential serves the call — that is the class's, and this
item is about the path and not the credential. **The test that holds the answer is
CSR13's `TestReviewRunWithTheBudgetOffNeverReachesACredentialOfItsOwn`**, which is why CSR13's test list
names it: it asserts the refusal, the setting the sentence names, and that no credential of the job's is
reached on the way out.

**Nothing above is decided here except what the owner answered on 2026-11-05 — items 1, 2, 3, 4 and 5,
each in its own words above — and on 2026-11-05/06, when item 6 was answered and the api-key path was cut
from 0.20.0 — and nothing above is built.** With item 4 answered as `CSR13` and item 5 as
`CSR14` and `CSR15`, **all six items are closed**: the five §6 was written with, and item 6, found
afterwards while the class of item 4 was re-read against the tree CSR8 had just merged into and answered
on 2026-11-05/06 as the refusal above. The section's task list is
**CSR8–CSR16: nine tasks**, each in this
plan's sense:
a red step, a file list, named tests and a person's gate where a person's gate is what the work needs —
**and §6.7 names a tenth and an eleventh, `CSR17` and `CSR18`, the api-key review, which are NOT in
0.20.0** and are therefore outside the levels, the critical path and the shared-files table below.
**An answer is not an approval** (README.md): every task above still waits for the owner to approve it,
and the tasks that were written because an item was answered are no more started than the ones that were
already here.

### 6.7 The api-key review: named, ordered, and NOT in 0.20.0 (CSR17, CSR18)

**The owner's ruling of 2026-11-05 was that both credentials should be supported — the OAuth review and the
api-key review, the key being an OpenRouter key or an Anthropic key — and the owner cut the api-key path
from 0.20.0 on 2026-11-06.** So 0.20.0 ships the **OAuth review alone**: `CSR13` (the class's model path),
`CSR14` (the verifier account), `CSR15` (the launcher's impersonation), `CSR16` (the head read), the
owner's live checks and `CSR11` (the flip). The api-key review is a **later release** — the two tasks below,
`CSR17` and `CSR18`, ordered `CSR17 → CSR18` — and **it is outside §6.3's levels, §6.3's critical path and
§6.4's shared-files table**, which are about the 0.20.0 set and nothing else. Nothing in this subsection is
a 0.20.0 file: no task above holds any of these files, and no file here is booked in §6.4.

**What 0.20.0 does about it, so nothing implies the api-key review works.** 0.20.0 refuses it **at the one
place that decides a review run's credential**, and nowhere else needs to: `CSR13`'s class offers the
pairing account's tier-A credential and no other, refuses a review whose account/auth is not OAuth, and
carries the sentence that names the path and the release it is in (*the OAuth review is the only one
0.20.0 ships*, in its own bullets); `CSR11`'s flip admits the OAuth review alone; and
`CSR13`'s `TestReviewRunIsNeverServedByAnAPIKeyOfItsOwn` holds the direction a repository's own file could
otherwise take: a review run whose repository writes `agent.auth: api-key` carries no provider key and is
**refused** rather than served by the class. **No other 0.20.0 task names the path at all**: CSR14's
account, CSR15's impersonation and CSR16's head read are the OAuth path's, and no 0.20.0 task books a file
this subsection names.

**What the tree can hold and mint today, verified rather than assumed — this is the slice's whole cost.**
The review class's candidates are the pairing's **ONE** account at tier A and nothing else, with no
fallback even where the policy's `fallback` would allow one (`reviewCandidates`,
`internal/credsvc/cascade.go:105`), and that account is a **Claude OAuth account**: `ReviewJobAccount.Account`
is the Claude account's name (`internal/credsvc/review.go:112`), the axis is built from `claude_accounts`
(`internal/config/claude_accounts.go`) and its secrets are the `claude-oauth-token` family. The service's
model credentials are exactly two kinds: the Claude OAuth tokens at **tier A** and the Anthropic API key at
**tier B** (`config.AnthropicSecret`, `internal/credsvc/bundle.go:677`), and the two the cascade selects are
those two (`internal/creds/select.go:17`, `:18`) — `internal/creds` defines a **third** tier, `TierC`
(`internal/creds/select.go:26`), and it is **"reserved and never selected"**: it is the third-party route
(an OpenRouter key) this release does not build, the constant exists so a later release's node reads as a
tier rather than a name, and a caller offering one is refused rather than quietly served by something else
— so neither a tier-B credential nor that reserved tier is a provider key the service holds today. **There is
no provider key in the service's document at all**: `CredentialKey`'s closed set has no entry for one
(`internal/credsvc/bundle.go:668`), and a provider key today lives in the local config's `providers` block
(`config.ModelProvider`, `internal/config/providers.go:41`: a base URL, an auth shape of `bearer` or
`x-api-key`, a logical secret name and a model list) and is mounted into a **run's job**, not held by the
service. Nor is there an upstream for one: the model path forwards to Anthropic's own API and to nothing
else (`upstreamKindAnthropic`, `internal/credsvc/forward.go:66`). So the api-key review is not a switch
somebody forgot to flip — **the service has nowhere to hold the key, no pairing that can name one, no
candidate list that may offer one and no upstream to send it to**, and the runner has no path that would
carry a provider's auth shape or base URL. That is why it is a slice and not a paragraph of CSR13.

**CSR17. The service can hold and serve a provider key for a review caller (SEC). — NOT IN 0.20.0.**
**Depends on:** nothing in 0.20.0's levels, and it is **after** the OAuth review path only in the sense
that it must not disturb it: `CSR13`'s class, `CSR15`'s identity and `CSR16`'s read are the OAuth path and
stay as they are. **Files (sketched, 6–8):** `internal/credsvc/bundle.go` + `bundle_test.go` (a provider-key
kind in the closed key set and in the schema), `schemas/creds-bundle.schema.json`,
`internal/credsvc/review.go` + its tests (the pairing gains a second axis: the account a review job is
served by may be a **provider** account rather than a Claude account), `internal/credsvc/cascade.go` + tests
(`reviewCandidates` offers that one credential — still exactly one, still no fallback), and
`internal/credsvc/forward.go` + tests (the upstream is the **provider's** base URL and auth shape rather
than Anthropic's). **No Terraform**: the service holds one secret document (Global Constraint 12), so a new
credential kind is a bundle key and never a resource or a grant. **The design point this task must settle
first, and it is why it is not a small task:** the bundle holds **credentials** and the service reads
nothing else, while a provider's base URL, auth shape and model list live in the project's configuration —
so either the published policy node grows the routing facts the service needs, or the bundle grows a
section that is not a credential, and both are a change to a contract another reader consumes (CLAUDE.md's
rule: a field never changes meaning). **Red step (sketched):** a service whose bundle holds a provider key
and whose review pairing names a provider account serves a review caller with **that** key — a test that
cannot pass today because the pairing refuses the account and the candidate list has nothing to offer.
**SEC flag:** yes — this is a model credential the service attaches to a caller's request. **Reviewer
tier:** SEC — opus, with the independent review mandatory. **Commit:** `credsvc: the review class can serve
a provider key`.

**CSR18. The review run's model path reaches a provider's endpoint (SEC). — NOT IN 0.20.0.** **Depends
on:** `CSR17` (the service side first: a runner path to a service that cannot serve it is a halt).
**Files (sketched, 3–5):** `internal/runner/routing.go` + `routing_internal_test.go` (the class's upstream
is the provider's, not the service's Anthropic forward), `internal/runner/agentenv.go` +
`agentenv_test.go` (the gateway variables a provider's model list needs), `internal/gateway/gateway.go`
(the upstream kind and the auth shape a provider call carries) and `internal/runner/runner.go`'s one call
site if it moves. **Red step (sketched):** a review run served by a provider key reaches the provider's
endpoint with the provider's auth shape, and `TestReviewRunIsNeverServedByAnAPIKeyOfItsOwn` is **rewritten**
rather than deleted — the refusal is 0.20.0's answer and this task is the one that makes it a path, so the
test keeps its name and gains the second direction. **SEC flag:**
yes — the second path through the routing ladder, which is why CSR13's review is asked for it again.
**Reviewer tier:** SEC — opus, with the independent review mandatory. **Commit:** `runner: a review run
served by a provider key`.

**What this subsection does not do.** It does not move `reviewJobSince` (that is the release slot's, §6.4),
it does not change 0.20.0's `### For operators` sentence (§6.5), and it does not add a level, an edge or a
shared-files row: the two tasks above are a later release's, and their own release writes the sentence that
names them.
