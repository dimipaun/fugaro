# The api-key review path — implementation plan

**Status: AK1–AK5 are implemented on `main`, and no task past AK5 is started; D1410–D1429 are answered
2026-11-07 and all tasks are approved 2026-11-07 (owner: "All tasks approved. Jobmax"), except the two
the security review added — AK9 and AK10, which are written and not approved.** AK4 lets the
review class's one credential be the project's own Anthropic API key: a policy naming the sentinel is
publishable and the credentials service serves it, and **AK5 deploys the route** (its own section and
**§6.8** — a profile naming the sentinel gets the job whose account segment is the sentinel, mounting
nothing, and its review-only variant beside it, so an installation that publishes the sentinel and no
Claude account has a review job at all). Every gate this plan carried that a decision or an approval
could settle is satisfied: the answers are in
[reconciliation.md](reconciliation.md) §7 and §9, and §6 below records what each one settled. **One
gate in that list is not of that kind and partly stands** — the mandatory security review owed before
AK4–AK7 land (item 6, which AK4's and AK5's changes are under rather than past): **the review of the
merged AK4 and AK5 is written** ([security-review.md](security-review.md), §7 below), it found no
defect that lets the key or the bundle out of the service, and it owes the same review again for
**AK6a**, **AK6b** and **AK7** — and **E7's gate is
lifted**, item 7's own accounting: its second half reached the tree on 2026-11-08, so `fugaro review`
admits a launch, though **no live installation has been measured** against it.

**The reconciliation with `docs/cloud-senior-review/implementation-plan.md` §6.7 is done, on
2026-11-09** (§1's own paragraph, and §6 items 9 and 10): **§6.7's reading was right and this plan's
was the loser on the one point they disagreed about** — a provider key needs **its own value on the
account axis**, because the axis's value is the admission unit and the pairing, not a selector,
decides which credential serves a review — so **AK6 is now two tasks, AK6a and AK6b** (§3), and
**D1418 is answered: the provider's base URL and auth shape go in the bundle**, which is AK6b's
field. Where this file and
[reconciliation.md](reconciliation.md) disagree about the code, the reconciliation wins.

**Read with:** [spec.md](spec.md) (the owner's ask), [reconciliation.md](reconciliation.md) (what
the tree does today, **D1410–D1429** and **D1418**'s answer of 2026-11-09) and
[security-review.md](security-review.md) (the review of the merged AK4 and AK5, and the conditions it
places on AK6a, AK6b and AK7). Every path and symbol cited here exists on `main` at
`aa26f322f`, and the security review (§7) read the tree the two landed tasks produced (`1d2591d5`,
`0944d4d4` and `9fa5fd90`); the findings in parentheses are the reconciliation's.

## 1. The slices, the levels and the critical path

The plan is **three slices that can be taken independently**, plus the live check. The slices follow
the reconciliation's §4: the in-job half needs no service change, the cloud Anthropic-key half is the
cheap half of `docs/cloud-senior-review/implementation-plan.md` §6.7, and the OpenRouter half is
§6.7 as written. **D1410 answered both of them** (2026-11-07), so the plan keeps all three slices;
a slice the owner does not want is simply not started.

| Level | Tasks | What it is | Why these can run together |
| --- | --- | --- | --- |
| **L1** | **AK1**, **AK2**, **AK3** | the in-job path named and pinned: tests (**F5**), docs (**F9**), a field of the record's own for the credential a stage ran on (**F18**, **D1415**) | no shared file, no service change, no ordering between them |
| **L2** | **AK4**, **AK5** | the cloud Anthropic-key half: the class may be served by the project's key (**F12**, **F13**, **F14**), and the runner admits it, so the review-only variant — already deployed and admitted for a project with no Claude account (**F17**) — is served by it (**F11**). **This was code for a launch E7 refused when it was written; the flip of 2026-11-08 admits it** (**F10**, **U6**) | AK4 is `internal/credsvc` + `internal/config` + `internal/projectpolicy` + `internal/localcfg`, with one refusal's sentence in `internal/infra` (§2's declared exception), and AK5 is `internal/runner` + `internal/cli` + `internal/infra`; the two share that one file, AK4 first |
| **L3** | **AK6a**, **AK6b**, **AK7** | the OpenRouter half: the provider key gets its own value on the account axis, so a profile can publish it and a policy admit it (**F15**, §6 item 9), the bundle holds the key and the service forwards to the provider's endpoint (**F15**, **F16**, **F24**, **D1418**), and the review run's model path reaches it (**F22**) | **No file is written by two of the three** — AK6a is `internal/config`, `internal/projectpolicy`, `internal/localcfg`, `internal/infra`, `internal/cli`, `internal/task`, `schemas/task.schema.json`, `deploy/terraform/gcp/modules/project/variables.tf` (the `job.account` validation) and the **three** `internal/credsvc` rows §2 books for it (`review.go`, `review_test.go` — the two CSR17's sketch named and AK6's six-file list did not — and `sharedjobs_test.go`, the deployer-and-service agreement test); AK6b is the rest of `internal/credsvc`, `internal/creds` and `schemas/creds-bundle.schema.json`; AK7 is `internal/runner` and nothing else, because the provider's base URL never leaves the bundle (**D1418**) — and AK6a is first because AK6b's offer rule matches the axis value AK6a defines (§2 books all of them) |
| **L4** | **AK8** | the live check, **Check 330** (§5) | owner-run, after everything else |

**The critical path is `AK1 → AK4 → AK5 → AK6a → AK6b → AK7 → AK8`**, and it is the shortest chain the
dependencies allow: AK1 is the slice that proves the path the ask names first, AK4 is the service's
side (a runner path to a service that cannot serve it is a halt), AK5 is the runner's admission, AK6a
is the axis value the provider key needs to be publishable and deployable at all, AK6b is the service
holding and serving that key, AK7 is the runner's path to it, and AK8 measures the
result on a real installation. **AK2 and AK3 are off the critical path** — AK2 is documentation and
AK3 is the record — and either may land at any point in L1 without moving it. **AK9 and AK10 are off
it too**: they are the security review's two findings (a wording pair that says a review job's account
is a Claude account, and the credential write that can outrun the service that reads it), and neither
blocks a cloud task. **The OpenRouter half of the critical path carries one more gate, and no task
holds it: Check 50** (§6 item 10) — the live check the tree's own tier-C refusal names, which the owner
has not run — so AK6a, AK6b and AK7 may all land and be unit-tested while a review **served** by a
provider key is still refused, and §5's step C may not be run before it.

**D1410, D1421 and the gates in §6 that a decision settles are settled as of 2026-11-07, before any
task starts; §6's items 6 and 7 are not decisions, and item 6 partly stands.** **D1410**
is answered **both slices**, so L1, L2 and L3 all stand and the plan does not end after AK3.
**D1416** is answered **the Anthropic key first**, so the OpenRouter half follows AK4–AK5 rather than
riding them.

**The reconciliation of 2026-11-09 split AK6 and settled which reading of the pairing was right.**
This plan's AK6 and `docs/cloud-senior-review/implementation-plan.md` §6.7's CSR17 disagreed about one
thing: §6.7 sketched a **second axis value** — the account a review job is served by may be a
provider account rather than a Claude account, which is a change to `internal/credsvc/review.go`'s own
file — and this plan's six-file AK6 did not. **§6.7's reading is right and AK6's file list is the
loser**, for the reason §2's preamble now carries: the account axis's value **is the admission unit**
(`projectpolicy` derives the `accounts` block keyed by it and `credsvc`'s `admittedAccounts`
intersects a repository's child with it), and the pairing — not the cascade's selector — is what fixes
which credential serves a review call. A provider key riding the sentinel
`config.AnthropicKeyAccount` would be a second credential under a value an installation already
spells, carrying the Anthropic key's admissions and its meaning, and `offeredToReview` matching by
name **and** tier would have to offer two credentials where the class's whole rule is exactly one with
no fallback. So the provider key gets a value of its own (**AK6a**, 24 files — AK4's and AK5's readers
for a second value, which is what §6.7's "6–8" estimate was short of) and the service half is
**AK6b** (9 files). §6.7 is amended to say so in the same pass (§6 item 9).

## 2. The shared-files table

**No level has two writers of one file, with one exception this plan declares rather than hides.**
A file that two *levels* touch is fine — the levels run in order — and the table says which level each
is booked in, so a task that wants to move a file between levels has to say so here first. The
exceptions are the ones the last paragraph of this section lists: two tasks of the **same** level
that must touch one file, each declared here rather than hidden. The earlier task landed first in this
branch, so the two never edit the file at once. A task that needs another such exception states it
here before it starts; the review of AK4's own change asked for the first of them (the refusal would
otherwise prescribe a `claude_accounts` key `ValidateClaudeAccounts` refuses, in a file that only AK5
could touch).

| File | Level | Task |
| --- | --- | --- |
| `internal/runner/review_key_test.go` (new) | L1 | AK1 |
| `internal/runner/provider_policy_test.go` | L1 | AK1 |
| `docs/multi-model.md` | L1 | AK2 |
| `docs/design/mixed-credentials.md` | L1 | AK2 |
| `internal/cli/docs_mixed_credentials_test.go` | L1 | AK2 |
| `internal/runstore/runstore.go` | L1 | AK3 |
| `internal/runstore/runstore_test.go` | L1 | AK3 |
| `internal/runner/cost.go` | L1 | AK3 |
| `internal/runner/provider_run_test.go` | L1 | AK3 |
| `schemas/result.schema.json` | L1 | AK3 |
| `internal/credsvc/review.go` | L2 | AK4, then AK6a (the second axis value) |
| `internal/credsvc/review_test.go` | L2 | AK4, then AK6a (the same) |
| `internal/credsvc/cascade.go` | L2 | AK4, then AK6b |
| `internal/credsvc/cascade_test.go` | L2 | AK4, then AK6b |
| `internal/config/claude_accounts.go` | L2 | AK4, then AK5, then AK6a |
| `internal/config/claude_accounts_test.go` | L2 | AK4, then AK5, then AK6a |
| `internal/config/project_secrets.go` | L2 | AK4 (the project's own key's reach), then AK6a |
| `internal/config/project_secrets_test.go` | L2 | AK4 (the same), then AK6a |
| `internal/projectpolicy/accounts.go` | L2 | AK4 (the sentinel's `accounts` entry), then AK6a |
| `internal/projectpolicy/accounts_test.go` | L2 | AK4 (the same), then AK6a |
| `internal/projectpolicy/policy.go` | L2 | AK4 (the account-choice members refuse the sentinel), then AK6a |
| `internal/projectpolicy/stage_accounts_test.go` | L2 | AK4 (the same), then AK6a |
| `internal/localcfg/localcfg.go` | L2 | AK4 (the account-choice keys refuse the sentinel), then AK6a |
| `internal/localcfg/localcfg_test.go` | L2 | AK4 (the same), then AK6a |
| `internal/runner/routing.go` | L2 | AK5, then AK7 |
| `internal/runner/routing_internal_test.go` | L2 | AK5, then AK7 |
| `internal/cli/run.go` | L2 | AK5, then AK6a (the second axis value) |
| `internal/cli/project_job.go` | L2 | AK5 (the sentinel's own job), then AK6a |
| `internal/cli/project_job_test.go` | L2 | AK5 (the same), then AK6a |
| `internal/credsvc/sharedjobs_test.go` | L2 | AK4, then AK5, then AK6a (the agreement test gains the value) |
| `internal/infra/projectjobs.go` | L2 | AK4 (the sentinel's refusal), then AK5, then AK6a |
| `internal/infra/projectjobs_test.go` | L2 | AK4 (the same), then AK5, then AK6a |
| `deploy/terraform/gcp/modules/project/variables.tf` | L2 | AK5 (the `job.account` validation), then AK6a |
| `schemas/task.schema.json` | L2 | AK5 (if the axis value reaches a task's `oauth_token`), then AK6a |
| `schemas/schemas_test.go` | L2 | AK5 (the same), then AK6a |
| `internal/task/task.go` | L2 | AK5 (the same), then AK6a |
| `internal/task/task_test.go` | L2 | AK5 (the same), then AK6a |
| `docs/project-layer.md` | L2 | AK5 (the axis's prose), then AK6a |
| `internal/credsvc/bundle.go` | L3 | AK6b |
| `internal/credsvc/bundle_test.go` | L3 | AK6b |
| `internal/credsvc/forward.go` | L3 | AK6b |
| `internal/credsvc/forward_test.go` | L3 | AK6b |
| `schemas/creds-bundle.schema.json` | L3 | AK6b |
| `internal/creds/select.go` | L3 | AK6b (the tier-C candidate the provider key is offered as) |
| `internal/creds/select_test.go` | L3 | AK6b (the same) |
| `internal/runner/agentenv.go` | L3 | AK7 |
| `internal/runner/agentenv_test.go` | L3 | AK7 |
| `docs/gcp-live-checklist.md` | L4 | AK8 |
| `internal/credsvc/auth.go` | — | AK9 (the two refusals that say Claude account) |
| `internal/credsvc/auth_test.go` | — | AK9 (the same) |
| `internal/credsvc/server.go` | — | AK9 (the same, in the serve path) |
| `internal/credsvc/server_test.go` | — | AK9 (the same) |
| `deploy/terraform/gcp/modules/installation/variables.tf` | — | AK9 (the same, in the module's prose) |
| `internal/cli/creds.go` | — | AK10 (the credential write's guard) |
| `internal/cli/creds_test.go` | — | AK10 (the same) |
| `internal/cli/creds_skew_test.go` (new) | — | AK10 (the same) |
| `internal/cli/recipes_skew.go` | — | AK10 (the constant and the check) |

**A row with `—` in the Level column is a task that is on no level**, which §1 says of AK9 and AK10:
they are off the levels and off the critical path, so their files are booked here only so that a later
task knows they are taken.

`internal/credsvc/cascade.go` is booked twice across two levels — AK4's candidate list and AK6b's tier
map — and `internal/runner/routing.go` with `internal/runner/routing_internal_test.go` twice within
one: AK5's admission and AK7's upstream over it. **AK6a re-enters L2's own readers of the account
axis** and that is the reconciliation's answer, not a lapse: a second value on the axis is read by
every reader of the axis, so `internal/config/claude_accounts.go` and its test,
`internal/infra/projectjobs.go` and its test and `internal/credsvc/sharedjobs_test.go` are marked
`then AK6a` after AK4 and AK5 — and the same is why §1 says AK6a is where §6.7's "6–8" file estimate
was short, and why AK6a and AK6b share no file: AK6a is the axis, AK6b is the service's document.
**The conditional `internal/gateway/gateway.go` row is gone**: §6.7's CSR18 booked it "if the upstream
kind moves", and **D1418** puts the provider's base URL and auth shape in the bundle, so the
gateway's own upstream kind does not move and no task writes that file. The rest of the files two
tasks of one **level** share are the exception the preamble above declares, and AK5 is where the
plan's inventory of them was short: AK4 and AK5 both touch `internal/infra/projectjobs.go` and its
test (AK4 answers what the sentinel is, AK5 deploys it), `internal/config/claude_accounts.go` and its
test (the same: AK4's prose says the sentinel is not deployable, AK5 makes it deployable) and
`internal/credsvc/sharedjobs_test.go` (AK4's agreement test gains the axis value). AK5 also books
`internal/cli/project_job.go` and its test, which no task booked and which the placement it makes is
unreachable without: the account axis is resolved there, so a sentinel the launcher cannot place on
its own job is a sentinel the deployer deploys and no launch reaches. A task that needs a file another
**level** holds adds itself to this table in the same commit.

## 3. The tasks

Each task states its files, its interfaces, its named tests and the command that runs them, its **red
step** (what fails before the change), its **mutation** (what a careless implementation would do, and
which test catches it), its **reviewer tier**, what it depends on and its commit message. **A task
with no red step says so and carries a mutation instead**; a task with neither is not a task.

The command shape is the repository's: `GOFLAGS=-p=2 go test ./<pkg>/ -run '<TestA|TestB>' -count=1`.
`internal/runner` and `internal/cli` each take about 25 minutes in full, so **every command below
names its tests**; the whole package is run only when a change is broad enough to need it.

### AK1 — the in-job api-key review is a pinned path

**Slice:** in-job (L1). **Depends on:** **D1410** — answered **both slices**, so the in-job half is
in the plan, and it is the first of them.

**Files (2):** `internal/runner/review_key_test.go` (new), `internal/runner/provider_policy_test.go`
(one fixture if the existing ones do not already give a provider-coder run with a Claude reviewer).

**Interfaces:** none. This task adds no exported symbol and changes no behaviour; it reads the
existing fixtures — `gwConfig` and `post` (`internal/runner/gateway_test.go`), `providerPinned`,
`providerRun`, `routeRun` and `providerGW` (`internal/runner/provider_policy_test.go`), `micros`
(`gateway_test.go`), `stageNames` and `eq` (`firstline_test.go`), `onlyPR`
(`internal/runner/runner_test.go`), `implement`, `review`, `fixStep` and `step`.

**Tests and command:**
`GOFLAGS=-p=2 go test ./internal/runner/ -run 'TestAPIKeyReviewGoesThroughTheGateway|TestAPIKeyReviewIsChargedRealDollars|TestAPIKeyReviewCountsAgainstTheCap|TestAPIKeyReviewIsNotionalWhenAStageAccountServesIt' -count=1`

- `TestAPIKeyReviewGoesThroughTheGateway` — an `agent.auth: api-key` run whose coder is on the
  provider route and whose reviewer is a Claude ID: the review stage's call reaches **Anthropic's**
  fake and **not** the provider's, and the provider's fake sees only the coder's stages. The mirror
  of `internal/runner/routing_e2e_test.go`'s `TestRoutedSwitchKeepsTheFirstLineLoop`, stated for the
  review rather than for the routing switch.
- `TestAPIKeyReviewIsChargedRealDollars` — the run's `rec.Cost.ModelBy[<reviewer>]` is the table's
  figure for the review stage, `rec.Cost.ModelNotionalUSD` is **zero**, and
  `rec.Cost.RouteBy["openrouter"]` carries the coder's stages alone.
- `TestAPIKeyReviewCountsAgainstTheCap` — a cap the coder's stages alone would not reach is reached
  by the review's call: the run halts with `run_cap` and the provider's fake sees no further call.
- `TestAPIKeyReviewIsNotionalWhenAStageAccountServesIt` — the contrast, and the reason the first
  three are worth writing: with a project policy that publishes a `review` stage account, the same
  configuration records `Credential: "oauth"` and `ModelNotionalUSD == ModelUSD` for the stage
  (**F7**). This is the test that makes the difference between the two configurations visible.

**Red step:** none, and this is stated rather than hidden — the task asserts behaviour that is
already correct (**F1**, **F2**). Its proof is the mutation below, which must fail these tests.

**Mutation:** publish a `review` stage account in the project policy, or drop the gateway from the
review stage. The first makes the review notional and turns `TestAPIKeyReviewIsChargedRealDollars`
red; the second sends the review's call to Anthropic without the gateway and turns
`TestAPIKeyReviewGoesThroughTheGateway` red.

**Reviewer tier:** standard — a person reviews it. The fixtures are credential-shaped, so the review
reads the fixture values against the repository's no-secret-shaped-literals rule.

**Commit:** `runner: the api-key review is a pinned path`

### AK2 — the docs name the path

**Slice:** in-job (L1). **Depends on:** AK1 (the sentences describe what AK1 pins).

**Files (3):** `docs/multi-model.md`, `docs/design/mixed-credentials.md`,
`internal/cli/docs_mixed_credentials_test.go` (the pin).

**Interfaces:** none. `docs/multi-model.md` §3 already states the Anthropic-key rule in the owner's
words and §4 already shows the configuration; the change is a section that says **which credential
the review then runs on** and what it costs, and a correction of §3's stale "Missing" list
(**F4**, **F9**). `internal/cli/docs_mixed_credentials_test.go` is the file that pins
`docs/design/mixed-credentials.md` today, so the corrected sentences are pinned in the same change
(CLAUDE.md: keep the pinned fact, change the wording both places).

**Tests and command:**
`GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs' -count=1`

**Red step:** the new pin fails, because neither sentence is in the tree.

**Mutation:** revert one sentence in `docs/multi-model.md` without touching the pin — the pin fails,
which is the whole reason it is written.

**Reviewer tier:** standard.

**Commit:** `docs(multi-model): the review on the run's own Anthropic key`

### AK3 — the record names the credential a stage ran on, under a name of its own

**Slice:** in-job (L1). **Depends on:** **D1415** — answered **a new field**, so this task stands and
L1 is AK1, AK2 and AK3; the "nothing at all" shape it was priced against is not the answer.

**Files (5):** `internal/runstore/runstore.go`, `internal/runstore/runstore_test.go`,
`internal/runner/cost.go`, `internal/runner/provider_run_test.go`, `schemas/result.schema.json`.

**Interfaces:** `runstore.StageTiming` gains a **new** field — the name is the task's to settle, and
it must not be `credential` — naming the credential that served the stage **including** the case the
existing field excludes, written by the runner's stage-recording path (`internal/runner/cost.go`) for
a stage the run's own **api-key** credential served, and `schemas/result.schema.json` gains the field
beside `credential`.

**`StageTiming.Credential` is not touched, and this is the point of the task.** Its stated meaning —
*the credential that served this stage **when it was not the run's own*** — excludes exactly the case
this task records (**F18**), so a `"api-key"` value there would be a meaning change under an existing
name, which CLAUDE.md forbids (*a field whose meaning must change gets a new name*). The new field is
additive: an absent value means what absence means today, and the old field's enum stays `["oauth"]`.

**Tests and command:**
`GOFLAGS=-p=2 go test ./internal/runstore/ ./internal/runner/ -run 'TestStageNamesTheCredentialThatServedIt|TestStageCredentialKeepsItsMeaning|TestAPIKeyReviewRecordsItsCredential' -count=1`

- `TestStageNamesTheCredentialThatServedIt` — a stage on the run's own api-key credential carries the
  new field; a stage an account served carries it too, beside `credential: "oauth"`.
- `TestStageCredentialKeepsItsMeaning` — the second direction, and the one the review asked for: a
  coding stage's `credential` is still **absent** and the enum is still `["oauth"]`, so the change is
  additive rather than a widening.
- `TestAPIKeyReviewRecordsItsCredential` — the review stage of AK1's fixture carries it, end to end.

**Red step:** `TestAPIKeyReviewRecordsItsCredential` fails today: no field names the credential a
stage the run's own key served ran on (**F18**).

**Mutation:** write the new value into `credential` instead of the new field —
`TestStageCredentialKeepsItsMeaning` goes red, which is exactly the mistake D1415's recommendation
exists to prevent.

**Reviewer tier:** standard, with the record contract called out in the review: the record is a wire
shape the console, the mobile app, the history and the plugin read, and `schemas/result.schema.json`
is the contract. An **additive** field is what CLAUDE.md allows there; a retyped or renamed one is
not.

**Commit:** `runstore: a stage names the credential it ran on`

### AK4 — the review class can be served by the project's Anthropic key (SEC)

**Slice:** cloud, Anthropic key (L2). **Depends on:** AK1 (**D1410**), and the pairing it needs:
**D1416** answered "the Anthropic key first" and **D1417** answered "the project's own key".

**Files (14):** `internal/credsvc/review.go`, `internal/credsvc/review_test.go`,
`internal/credsvc/cascade.go`, `internal/credsvc/cascade_test.go`,
`internal/config/claude_accounts.go`, `internal/config/project_secrets.go`,
`internal/projectpolicy/accounts.go` and the test file of each — the last four because the sentinel has
to be **publishable**, which no task booked: `projectpolicy.DeriveAccounts` writes the `accounts` block
every publish, `fugaro update` and the deployer derive from, and the credentials service intersects a
repository's admission with it (`credsvc`'s `admittedAccounts`), so a sentinel the block cannot carry
is a class no installation could ever be served by, however AK4 builds the candidate list — plus
`internal/infra/projectjobs.go` and its test, §2's declared exception: the sentinel is accepted at
load and published, so the deployer's answer about it is reachable, and the review of this change
found that answer naming a `claude_accounts` key `ValidateClaudeAccounts` refuses. AK4 makes that
refusal honest; AK5 replaces it with the route. And `internal/localcfg/localcfg.go` with its test plus
`internal/projectpolicy/policy.go` with `stage_accounts_test.go`, for the reason the next paragraph
gives: the account-**choice** keys are the other reader of the same value, and both of them had the
same unreachable remedy.

**Interfaces:** the pairing's account axis gains one value that is **not** a Claude account — the
sentinel the admissions spell for "the project's own Anthropic API key" — resolved in
`internal/config/claude_accounts.go` beside the names the axis already derives; and
`internal/credsvc/cascade.go`'s `reviewCandidates` offers the bundle's tier-B entry
(`config.AnthropicSecret`, `CredentialKey`) when the pairing names it, **and still exactly one
credential and no fallback** (**F13**). The sentinel is published like every other axis value and by
the same function: `DeriveAccounts` derives its entry from the profiles that name it (their own
`allow_repos`, kept to the reach the key itself has, `config.ProjectSecretReaches`), refuses the pair
where that list is empty, and the child admissions are the same block inverted — so a policy naming
the sentinel is **writable**, which is the whole of what this task makes it. **No installation could
deploy it until AK5**: on AK4's own landing the sentinel was publishable and *not* deployable, an
installation that published a profile naming it was refused by `fugaro update` rather than
misconfigured, and **AK5**'s deployment claim (**F17**) is the task that closed that gap. Nothing in
`internal/credsvc/forward.go` changes: the tier-B arm already builds
`gateway.Upstream{Kind: upstreamKindAnthropic, APIKey: e.Value()}` and attaches it with
`gateway.UpstreamHeaders` (**F12**), and that is the whole reason this task is small.

**What AK4 does not make possible — the sentinel's deployment route.** Publishing is not deploying,
and every reader of the account axis on the way to a job reads a profile's `claude_accounts` as *the
Claude OAuth tokens this profile's jobs mount*. The sentinel is no job's token, so each of the four
readers below needs an answer this plan has to write somewhere: AK4 writes the two that are claims
about a credential, and AK5 owns the two that are the route itself.

- `internal/infra/projectjobs.go`'s `projectAccountMount` refuses an account whose project-wide
  container the installation does not create — for the sentinel with a sentence prescribing
  `claude_accounts.anthropic-api-key--project.allow_data_to`, the one key `ValidateClaudeAccounts`
  refuses. That is the refusal an installation publishing the sentinel lands in, and it is fail-closed,
  but its remedy is not reachable, so **AK4 fixes this one too** (§2's declared exception): `ProjectJobs`
  refuses the value in words the operator can act on — no job can serve the project's own key in this
  release, take it off the profile — because the true remedy is that move, not a `claude_accounts` key.
  The refusal stands in `ProjectJobs` itself, **ahead of the skip that leaves a Bitbucket profile
  undeployed** (an arm inside the job loop would be reached by shared-job profiles only, so a Bitbucket
  profile would carry the sentinel past `fugaro update` unrefused — with `DeriveAccounts` publishing its
  accounts entry — while the state this section describes is claimed for every profile). **AK5**
  replaces that refusal with the route's own answer (no mount — the review-only variant mounts nothing
  at all, **O1**).
- `config.ProjectJobTokenAccount` answered `(sentinel, true)`: a job holding a token no secret exists
  for, where the true answer is `("", false)`. **AK4 fixes this one**, because it is a claim about a
  credential and the launcher's gate and `doctor` both read it.
- `deploy/terraform/gcp/modules/project/variables.tf` validates `job.account` as *empty or a Claude
  account name*, and the sentinel is neither — so a sentinel-naming profile's coding job **and** its
  review variant fail `terraform plan` the moment the refusal above stops firing. **AK5** books that
  file, and decides with it what the job whose account segment is the sentinel *is*.
- `internal/task/task.go` and `schemas/task.schema.json` hold a spec's `oauth_token` to
  `ClaudeAccountNameRE` too, so the axis value reaches a task's spec only if **AK5** decides it must.
  **AK5** books both.

**The account-choice keys are the same claim from the other side, and AK4 answers them.** A profile's
`claude_accounts` entry says which account a repository's runs may use; the choice keys say which
account a run's stages *choose* — the local config's `default_claude_account` and
`stages.<s>.claude_account` (`localcfg.choiceRefProblem`), the project layer's `default_claude_account`
and `claude_account_stages` (`projectpolicy`'s decoder). Both readers hold a value to
`config.ValidClaudeAccountRef`, which admits the sentinel because the profile entry may name it, so
both fell past it: `localcfg` into the defined-account lookup, whose sentence prescribes
`claude_accounts: {anthropic-api-key--project: {allow_data_to: […]}}` — the entry
`ValidateClaudeAccounts` refuses, the same unreachable remedy; `projectpolicy` into acceptance, a
published default or stage account no job mounts. **AK4 refuses the value at all four sites**, in one
sentence declared beside the sentinel (`config.AnthropicKeyAccountChoiceMessage`), whose move is the one
that works — drop the key. Nothing relaxes these until the route exists: a choice key naming the
sentinel becomes meaningful only if **AK5** decides a stage of a run may be served by the project's own
key, and a task that needs one of these files books itself in §2's table then.

**The unmade decision this exposes belongs to AK5 and is recorded in §6**, not taken here: whether a
profile that names the sentinel gets a coding job at all, what that job's account segment is, and
what `ReviewJobAccountsFor` and the deployer then agree the review variant's segment is.

**Tests and command:**
`GOFLAGS=-p=2 go test ./internal/credsvc/ ./internal/config/ ./internal/projectpolicy/ ./internal/infra/ ./internal/localcfg/ -run 'TestReviewPairingMayNameTheProjectsAnthropicKey|TestReviewCandidateListIsExactlyOneCredential|TestReviewCallerIsNeverOfferedAProviderKey|TestReviewCandidateListStillOffersThePairingAccount|TestAnthropicKeyAccountIsAReferenceAndNotADefinition|TestAnthropicKeyAccountIsNoJobsToken|TestProfileMayNameTheAnthropicKeyWithNothingDefinedForIt|TestProjectSecretReaches|TestDeriveAccountsPublishesTheProjectsAnthropicKey|TestAnthropicKeySentinelRefusedWithAReachableRemedy|TestAccountChoiceKeysRefuseTheProjectsAnthropicKey|TestDecodeRefusesTheProjectsAnthropicKeyAsAStageAccount' -count=1`

- `TestReviewPairingMayNameTheProjectsAnthropicKey` — a review caller whose pairing names the
  sentinel is served by the bundle's `config.AnthropicSecret` entry, and the forwarded call carries
  the key to Anthropic's own address. **The red step.**
- `TestReviewCandidateListIsExactlyOneCredential` — the list is one entry long and the cascade's
  ladder has nothing to fall to, whatever the policy's `fallback` says.
- `TestReviewCallerIsNeverOfferedAProviderKey` — the negative that keeps this task from becoming
  AK6: a bundle holding a provider key offers it to no review caller.
- `TestReviewCandidateListStillOffersThePairingAccount` — the second direction: a pairing that names
  a Claude account is served exactly as it is today.
- `TestDeriveAccountsPublishesTheProjectsAnthropicKey` — the publish half, and the one the plan
  itself did not book: the sentinel's `accounts` entry is derived from the profiles that name it, kept
  to the key's own reach, and the derived block is a node the policy's own rules accept.
- `TestAnthropicKeyAccountIsAReferenceAndNotADefinition`, `TestAnthropicKeyAccountIsNoJobsToken`,
  `TestProfileMayNameTheAnthropicKeyWithNothingDefinedForIt` and `TestProjectSecretReaches` — the
  config half: the sentinel is spelled where an account is referenced, refused where one is defined
  with its own sentence (whose named fix is a different account name, never a profile entry that
  `fugaro update` refuses), answers no job's token question, needs no local entry behind it, and the
  reach the derive reads is the deployer's own eligibility rule.
- `TestAnthropicKeySentinelRefusedWithAReachableRemedy` — the deployer's half of the same honesty, the
  one the review of this change asked for: a profile that names the sentinel is refused with a
  `UserError`, and the refusal names neither `claude_accounts.<sentinel>` nor `.allow_data_to` — the
  keys `ValidateClaudeAccounts` refuses, so naming one would send the operator to a remedy nobody can
  carry out — while it does name `profiles.<profile>.claude_accounts`, the field they edit.
- `TestAccountChoiceKeysRefuseTheProjectsAnthropicKey` and
  `TestDecodeRefusesTheProjectsAnthropicKeyAsAStageAccount` — the choice keys' half, in both readers:
  each key refuses the sentinel naming itself, the sentence names the move that works (drop the key),
  and it contains neither `claude_accounts` nor `allow_data_to` — either string would mean the operator
  was handed the account lookup's forbidden remedy instead.

**Red step:** `TestReviewPairingMayNameTheProjectsAnthropicKey` fails today, for the reason **F13**
names: `reviewCandidates` offers the pairing's tier-A account and nothing else, so a pairing naming
the project's key has nothing to offer.

**Mutation:** offer the whole tier-B list rather than the one named entry —
`TestReviewCandidateListIsExactlyOneCredential` goes red.

**Reviewer tier:** **SEC** — opus, with the independent security review mandatory. This task changes
which credential the service attaches to a caller's request, which is the package's own
security-sensitive surface (`internal/credsvc/review.go` says so in its header).

**Commit:** `credsvc: the review class can be served by the project's Anthropic key`

### AK5 — the runner admits an api-key review, and the variant is launchable without a Claude account (SEC)

**Slice:** cloud, Anthropic key (L2). **Depends on:** AK4 (a runner path to a service that cannot
serve it is a halt), **D1422**.

**Files (16):** `internal/runner/routing.go`, `internal/runner/routing_internal_test.go`,
`internal/cli/run.go`, `internal/infra/projectjobs.go`, `internal/infra/projectjobs_test.go`,
`deploy/terraform/gcp/modules/project/variables.tf`, `internal/task/task.go`,
`internal/task/task_test.go` and `schemas/task.schema.json` — the last four because AK4 makes the
sentinel publishable and servable and **not deployable**, and this is the task that deploys it (AK4's
own section lists the four readers of the account axis that must yield; the last three only if the
axis value is to reach a task's `oauth_token` at all) — plus `docs/project-layer.md` (§2 books it for
this task's prose and the list above left it out) and six files the plan did not book at all, which
this task adds to §2 in this commit, each because the deployment is unreachable or dishonest without
it: `internal/cli/project_job.go` and `internal/cli/project_job_test.go` (the account axis is resolved
there, and a sentinel the launcher cannot place on the job the deployer builds is a job no launch ever
runs on), `internal/config/claude_accounts.go` and `internal/config/claude_accounts_test.go` (AK4's
prose there says the sentinel is deployable by no installation, and its definition message carries the
same claim), `internal/credsvc/sharedjobs_test.go` (the deployer-and-service agreement test gains the
new axis value, so the review variant's name is held to the deployer's) and `schemas/schemas_test.go`
(the task schema's `oauth_token` rule is asserted against the Go rules it mirrors).

**Interfaces:** `internal/runner/routing.go`'s `checkReviewClassModelPath` admits the review class's
new credential — the pairing's sentinel — beside `"oauth"` and `config.AuthCascade`, and refuses
`"api-key"` and `"vertex"` exactly as it does today; `internal/cli/run.go`'s `claudeAccountFor` and
`mixedRunAccount` resolve the sentinel to the review variant that is deployed for it; and
`internal/infra/projectjobs.go` deploys that variant, so an installation that publishes the sentinel
and no Claude account has a review job at all (**F17**) — where today the same profile is refused by
`ProjectJobs` (AK4's sentinel refusal, for every profile and ahead of the Bitbucket skip, whose
sentence names the move that works — take the key off the profile — rather than a `claude_accounts`
key `ValidateClaudeAccounts` refuses; AK5 replaces the refusal with the route's own answer) and, past
it, by `modules/project`'s `job.account` validation
(`ClaudeAccountNameRE`, which the sentinel does not match). **This task takes the decision AK4
booked in §6 and did not take**: whether a profile naming the sentinel gets a coding job at all, what
that job's account segment is (the module admits an empty account — a job that runs as no Claude
account — or a Claude account name, and the sentinel is neither), and what
`credsvc.ReviewJobAccountsFor` and the deployer then agree the review variant's segment is. The
class's door is unchanged: `reviewClassRun`'s conjunction of the run's mode and the job it was
admitted to is what keeps a review job from being traded for a coding one, and nothing here weakens
it.

**Tests and command:**
`GOFLAGS=-p=2 go test ./internal/runner/ ./internal/cli/ ./internal/infra/ -run 'TestReviewRunIsServedByTheProjectsAnthropicKey|TestReviewRunIsNeverServedByAnAPIKeyOfItsOwn|TestReviewJobIsDeployedForAProjectWithNoClaudeAccount|TestReviewAccountsAreTheNamesProjectJobsDeploys' -count=1`

- `TestReviewRunIsServedByTheProjectsAnthropicKey` — a review class run whose pairing names the
  project's key is admitted, its gateway's upstream is the service's, and the agent's environment
  holds no credential of the job's. **The red step.**
- `TestReviewRunIsNeverServedByAnAPIKeyOfItsOwn` — **rewritten, not deleted** (**D1420**): the test
  keeps its name and gains the second direction, so a repository whose own `agent.auth` is `api-key`
  is still refused, and the sentence it asserts names what the class is served from rather than the
  release the path is in.
- `TestReviewJobIsDeployedForAProjectWithNoClaudeAccount` — the installation-shape half: a policy
  that publishes the sentinel and no Claude account deploys exactly one review job, and
  `ReviewJobAccountsFor` and the deployer agree on its name.
- `TestReviewAccountsAreTheNamesProjectJobsDeploys` — the existing agreement test, extended to the
  new axis value.

**Red step:** `TestReviewRunIsServedByTheProjectsAnthropicKey` fails today, in
`checkReviewClassModelPath`'s own refusal (**F11**).

**Mutation:** admit the repository's own `agent.auth` as the class's credential —
`TestReviewRunIsNeverServedByAnAPIKeyOfItsOwn`'s second direction goes red, which is the direction
the whole class exists to hold.

**Reviewer tier:** **SEC** — opus, with the independent security review mandatory. The task changes
which runs a credential-bearing variant is launched for.

**Commit:** `runner: a review may be served by the project's Anthropic key`

### AK6a — the provider key gets a value of its own on the account axis (SEC)

**Slice:** cloud, OpenRouter (L3). **Depends on:** AK4 and AK5 (the axis it extends a second time),
**D1416** — the Anthropic key first, so this half follows AK4–AK5 — **D1418** (the base URL and the
auth shape are the bundle's, which is AK6b's field), and the reconciliation of 2026-11-09 with
`docs/cloud-senior-review/implementation-plan.md` §6.7's **CSR17** (§6 item 9). **CSR17's reading is
the right one and this plan's six-file AK6 was the loser** (§1's own paragraph, §2's preamble): a
provider key needs **its own value on the account axis**, because that value is the admission unit
and the pairing — not the cascade's selector — fixes which credential serves a review call.

**Files (24):** `internal/config/claude_accounts.go` and its test, `internal/config/project_secrets.go`
and its test, `internal/projectpolicy/accounts.go` and its test, `internal/projectpolicy/policy.go`,
`internal/projectpolicy/stage_accounts_test.go`, `internal/localcfg/localcfg.go` and its test,
`internal/infra/projectjobs.go` and its test, `internal/cli/project_job.go` and its test,
`internal/cli/run.go`, `internal/task/task.go` and its test, `schemas/task.schema.json` and
`schemas/schemas_test.go`, `deploy/terraform/gcp/modules/project/variables.tf`, `docs/project-layer.md`,
and `internal/credsvc/review.go`, `internal/credsvc/review_test.go` and
`internal/credsvc/sharedjobs_test.go`. **All twenty-four are rows §2 already books at L2** — the axis's
readers AK4 and AK5 built, which is what its preamble means by `then AK6a` — and two of them,
`internal/credsvc/review.go` and `internal/credsvc/review_test.go` (the pairing's own type and its
offer rule), are the files §6.7's CSR17 booked and this plan's AK6 did not: that is the disagreement
the reconciliation settled in CSR17's favour. The third `internal/credsvc` row,
`sharedjobs_test.go`, was already booked and needs only the new value in its assertion. **No new secret, no new resource, no new grant:** the value is
an axis value, and the credential it names is AK6b's bundle key.

**Interfaces:** the account axis gains **a second value of its own** beside
`config.AnthropicKeyAccount` — a constant in `internal/config/claude_accounts.go`, called here the
provider-key account, whose spelling is the task's to settle and whose one hard rule is that it is a
value no installation spells today (`config.ValidateClaudeAccounts` accepts it as *not* a Claude
account, exactly as it accepts the sentinel, and `config.DefaultClaudeAccount` is not it) — so that
`projectpolicy`'s `DeriveAccounts` writes an `accounts` entry for it, `credsvc`'s `admittedAccounts`
intersects a repository's child with it, the deployer's `ProjectJobs` builds the profile's job and its
review-only variant for it, the launcher places it on that job, and `modules/project`'s `job.account`
validation admits the literal. The three doors AK5 opened for the sentinel open for it in the same
shape and no wider: **admissible at the account flag `--token`** and by inheritance from a launch that
used one, **refused in a repository's own `fugaro.yaml`** (`config.Validate`), and **refused as a
stage account** (AK4's choice-key refusal) — a stage's account is a token the job's environment
already carries, and this value's job carries nothing. `internal/credsvc/review.go`'s
`ReviewJobAccount.Account` and `ReviewJobAccountsFor` take it as the axis value they already document
beside the sentinel, and the pairing's offer rule matches it by **name and tier** exactly as it
matches the sentinel's — one credential, no fallback.

**Tests and command:**
`GOFLAGS=-p=2 go test ./internal/projectpolicy/ ./internal/infra/ ./internal/cli/ ./internal/credsvc/ -run 'TestProviderKeyGetsItsOwnValueOnTheAccountAxis|TestProviderKeyIsNotAClaudeAccount|TestProviderKeyJobMountsNothing|TestReviewAccountsAreTheNamesProjectJobsDeploys' -count=1`

- `TestProviderKeyGetsItsOwnValueOnTheAccountAxis` — a policy that publishes the provider-key account
  derives an `accounts` entry for it, the deployer builds the profile's job and its review-only
  variant for it, and the launcher resolves that job. **The red step.**
- `TestProviderKeyIsNotAClaudeAccount` — the three doors above, in AK5's own three directions: the
  value is admissible at `--token`, refused in a repository's `fugaro.yaml` and refused as a stage
  account.
- `TestProviderKeyJobMountsNothing` — the job whose account segment is the value mounts no token
  (`config.ProjectJobTokenAccount` answers `("", false)`), so no Claude token reaches it.
- `TestReviewAccountsAreTheNamesProjectJobsDeploys` — the deployer-and-service agreement test,
  extended to the new value (the file AK4 and AK5 both booked and this task re-enters).

**Red step:** `TestProviderKeyGetsItsOwnValueOnTheAccountAxis` fails today: the value is not a name
the tree accepts as an axis value — `config.ValidateClaudeAccounts`' rules and `config.ClaudeAccountNameRE`
are Go, and the module's own `job.account` validation
(`deploy/terraform/gcp/modules/project/variables.tf`) admits an empty account, the sentinel as the
literal `anthropic-api-key--project` and a Claude account name, and no third spelling — `ProjectJobs`
has no branch for it, and the installation's policy is refused by `fugaro update` before any of this
exists.

**Mutation:** let the provider key ride `config.AnthropicKeyAccount` — the loser's reading (§1) —
and `TestReviewAccountsAreTheNamesProjectJobsDeploys` finds one axis value where the policy published
two credentials, while the pairing's own test in `internal/credsvc/review_test.go` finds one
credential asked to be two. The sharper mutation is the other direction: admit the value as a *stage*
account, which mounts a token the job has none of and hands a coding run the review's credential —
`TestProviderKeyIsNotAClaudeAccount`'s stage direction goes red.

**Reviewer tier:** **SEC** — opus, with the independent security review mandatory. The axis value is
the admission unit (`DeriveAccounts` keys the `accounts` block by it and `admittedAccounts`
intersects a repository's child with it), so a second value is a second admission.

**Commit:** `config: the provider key is an account axis value of its own`

### AK6b — the bundle holds a provider key, and the service forwards to a provider's endpoint (SEC)

**Slice:** cloud, OpenRouter (L3). **Depends on:** AK6a (the axis value its offer rule matches),
**D1418** — the provider's base URL and auth shape go **in the bundle** — **D1419**, answered "not at
all in the Anthropic-key slice", which leaves the provider endpoint to this half, and **D1416**.
**The other half of §6.7's CSR17**, and the half whose six-file list this plan already had: it is
that list — `bundle.go` and its test, `cascade.go`, `forward.go` and its test and the bundle schema —
plus `internal/credsvc/cascade_test.go` (the tier map's own test, which the six named no test for) and
`internal/creds/select.go` and its test, the tier-C refusal no sketch booked (§2).

**Files (9):** `internal/credsvc/bundle.go`, `internal/credsvc/bundle_test.go`,
`internal/credsvc/cascade.go`, `internal/credsvc/cascade_test.go`, `internal/credsvc/forward.go`,
`internal/credsvc/forward_test.go`, `schemas/creds-bundle.schema.json`, `internal/creds/select.go`,
`internal/creds/select_test.go`.

**Interfaces:** `CredentialKey`'s closed set gains a provider-key kind (**F15**); a bundle entry
carries the routing facts a provider call needs — **a base URL and an auth shape of `bearer` or
`x-api-key`, which are not credentials** — as **additive optional fields** (**D1418**, 2026-11-09): an
older bundle still decodes, because no field is removed and none is retyped, so the format's version
does not move, and a field never changes meaning, so the names this task picks are new ones. A
provider entry **without** those fields is refused by name and an auth shape outside the two is
refused rather than defaulted: **a provider the bundle does not describe fails closed**, which is the
one thing this entry may not do, because a defaulted base URL is a credential sent to an endpoint
nobody named. `cascade.go`'s `wireTier` maps the reserved `creds.TierC` (**F16**) so a provider key is
offered as a tier rather than a name — and `internal/creds/select.go`'s `checkCandidates` refuses
**every** tier-C candidate in a sentence naming **Check 50** (D150, C3.6;
`TestSelectRefusesReservedTierC`), so **this half's served path is refused until Check 50 runs**,
which is the owner's check and not this task's: the task may land its document, its offer and its
forwarding, and its own test asserts that the refusal still stands (**§6 item 10**).
`forward.go`'s `send` builds the provider's upstream and destination rather than
`upstreamKindAnthropic`'s: the destination is the **bundle's** base URL joined to the path
`servedRoute`'s closed set yields, the query is dropped where it carries a caller token, the client
refuses redirects, the answer names the credential's name and tier and never its value, and it is
chained through `gateway.NewRedactWriter` for the credential and every caller token — every one of
those rules is the code on `main` today and this task moves none of them (§7, and the conditions the
review file's §5 lists). **No
Terraform:** the service holds one secret document (Global Constraint 12), so a new credential kind is
a bundle key and never a resource or a grant.

**Tests and command:**
`GOFLAGS=-p=2 go test ./internal/credsvc/ ./internal/creds/ -run 'TestBundleHoldsAProviderKey|TestProviderEntryWithoutItsRoutingFactsIsRefused|TestProviderKeyEntryIsNotOfferedToAnyOtherCaller|TestForwardToAProviderCarriesTheProviderAuthShape|TestProviderDestinationIsTheProvidersOwnHost|TestSelectRefusesReservedTierC' -count=1`

- `TestBundleHoldsAProviderKey` — a bundle holding a provider key loads, and `CredentialKey` names
  its kind and tier. **The red step.**
- `TestProviderEntryWithoutItsRoutingFactsIsRefused` — the fail-closed direction: an entry with no
  base URL, or with an auth shape outside `bearer` and `x-api-key`, refuses the document by name and
  never defaults an endpoint.
- `TestProviderKeyEntryIsNotOfferedToAnyOtherCaller` — a coding caller's candidate list is what it
  was; the provider key is offered to the review pairing alone.
- `TestForwardToAProviderCarriesTheProviderAuthShape` — a `bearer` key goes on `Authorization`, an
  `x-api-key` key on `X-Api-Key`, and neither goes to Anthropic.
- `TestProviderDestinationIsTheProvidersOwnHost` — the destination rule holds: the key reaches the
  host its base URL names and no other, a redirect is refused, and the credential appears in neither
  the answer's headers nor its body.
- `TestSelectRefusesReservedTierC` — re-run, not rewritten: the tier-C refusal that gates this half's
  served path is still there, and still names Check 50.

**Red step:** `TestBundleHoldsAProviderKey` fails today: `CredentialKey`'s closed set has no entry
for a provider key, so the loader refuses the document (**F15**).

**Mutation:** send the provider key to `upstreamKindAnthropic`'s address —
`TestProviderDestinationIsTheProvidersOwnHost` goes red. A second: default the auth shape to `bearer`
for an entry that names none — `TestProviderEntryWithoutItsRoutingFactsIsRefused` goes red and a key
the provider reads as a bearer token is sent one shape further than the installation chose.

**Reviewer tier:** **SEC** — opus, with the independent security review mandatory. This is a
credential the service attaches to a caller's request **and** a destination rule, which is the pair
`internal/credsvc/forward.go` exists to keep apart — and it is the second bundle key to move what the
service can reach, which is why **AK10** stands beside it.

**Commit:** `credsvc: the bundle can hold a provider key`

### AK7 — the review run's model path reaches a provider's endpoint (SEC)

**Slice:** cloud, OpenRouter (L3). **Depends on:** AK6a and AK6b. **This task is §6.7's CSR18, and the
reconciliation of 2026-11-09 amended it in two places** (§6 item 9): CSR18's `internal/gateway/gateway.go`
is **dropped**, because its own clause was "if the upstream kind or the auth shape a provider call
carries moves", and with **D1418** both are the bundle's — the service builds the provider's call from
the document it holds, so the gateway's upstream kind does not move and no task writes that file; and
CSR18's `internal/runner/runner.go` call-site clause is dropped too, because the one call site
`agentenv.go` already has is the call site. `internal/runner/agentenv_test.go`, which CSR18 booked and
this plan had not, is kept.

**Files (4):** `internal/runner/routing.go`, `internal/runner/routing_internal_test.go`,
`internal/runner/agentenv.go`, `internal/runner/agentenv_test.go`. **No file outside
`internal/runner`**: with **D1418** the provider's base URL and auth shape never leave the service's
bundle, so the runner names no provider endpoint and no host — the property §7's third attack question
answers.

**Interfaces:** `checkReviewClassModelPath` stops refusing the provider-served pairing: the class takes
the provider-key account as a pairing it admits (AK6a's value), and the call the service then forwards
is the provider's, which the runner names no part of — `internal/runner/agentenv.go`'s gateway
variables carry what a provider's model list needs and no host. The pins and the allow-list are
unchanged: the review is still held to the project policy's `budget.allowed_models` (**D1425**), and
`agentenv.go` is the only file here that writes an environment.

**Tests and command:**
`GOFLAGS=-p=2 go test ./internal/runner/ -run 'TestReviewRunOnAProviderKeyReachesTheProvider|TestReviewRunIsStillHeldToTheAllowList|TestReviewRunIsServedByTheProjectsAnthropicKey' -count=1`

- `TestReviewRunOnAProviderKeyReachesTheProvider` — a review run served by a provider key reaches
  the provider's endpoint with the provider's auth shape, and Anthropic's fake sees no call. **The
  red step.**
- `TestReviewRunIsStillHeldToTheAllowList` — the gate the class is already held to is not moved by
  this task.
- `TestReviewRunIsServedByTheProjectsAnthropicKey` — the second direction, re-run from AK5: the
  Anthropic-key path is unchanged by the provider one.

**Red step:** `TestReviewRunOnAProviderKeyReachesTheProvider` fails today: the class's model path
admits the service's Anthropic forward and nothing a provider-served pairing reaches.

**Mutation:** carry the provider's base URL into the agent's environment, so the runner picks the
endpoint instead of the service's document — the test's own assertion that the job's environment names
no provider host goes red, and the installation would have two places deciding where a review's prompt
goes (**D1418**, §7's third attack question).

**Reviewer tier:** **SEC** — opus, with the independent security review mandatory.

**Commit:** `runner: a review on a provider key reaches the provider's endpoint`

### AK8 — Check 330 (owner-run)

**Slice:** the live check (L4). **Depends on:** AK1–AK7 as far as the owner's answer to **D1410**
reaches, and on **D1421** for the release the check runs against.

**Files (3):** `docs/gcp-live-checklist.md` (the summary row and the `## Check 330` section, written
under the number free **at that merge**), `docs/api-key-review/implementation-plan.md` (this file,
marking the check run), `docs/api-key-review/README.md` (the status line, when the slice ships).

**Interfaces:** none.

**Tests and command:** none — the check is a live procedure run by the owner, and §5 is its text.

**Red step:** none; a live check has no red step. Its evidence is the owner's run, recorded in the
register.

**Mutation:** not applicable.

**Reviewer tier:** owner-run.

**Commit:** `docs(checklist): check 330, the api-key review`

### AK9 — the service's own sentences stop saying a review is served by a Claude account (SEC)

**Slice:** none — **off the levels and off the critical path** (§1, §2's `—` rows). **Depends on:**
nothing; it was found by the security review of the merged AK4 and AK5 (§7, **F25**).

**Files (5):** `internal/credsvc/auth.go`, `internal/credsvc/auth_test.go`, `internal/credsvc/server.go`,
`internal/credsvc/server_test.go`, `deploy/terraform/gcp/modules/installation/variables.tf`.

**Interfaces:** none. The two refusal sentences in `internal/credsvc/auth.go` ("a review job account
names no Claude account, so no call of it could be served"; "the same review job service account is
bound to two Claude accounts"), the two in `internal/credsvc/server.go` (the serve path's copies, one
of which spells "no email or no Claude account") and the module's prose for `review_job_accounts` (the
variable's description and the comment above its validation) name **a Claude account** where AK4 made
that inexact: the account a review variant's calls are served by may be `config.AnthropicKeyAccount`,
which names the project's own Anthropic API key and, in `internal/credsvc/review.go`'s own words, **is
not an account**. The fix is the words and nothing else — the type, the validation and the one audit
line a refusal writes stay exactly as they are — so the refusal says what the value is: the **account
axis** value a review job is served by.

**Tests and command:**
`GOFLAGS=-p=2 go test ./internal/credsvc/ -run 'TestReviewAccountRefusalsNameTheAxisAndNotAnAccount|TestNewAuthenticatorRefusesAnIncompleteConfig' -count=1`

- `TestReviewAccountRefusalsNameTheAxisAndNotAnAccount` — the four sentences, in the packages that
  write them: each names the axis and does not say that what serves a review call is a Claude account,
  and a review job account naming nothing is still refused. **The red step**, in the narrow sense a
  sentence has one: the test asserts the corrected sentence, so it fails on today's tree and passes
  after the change.
- `TestNewAuthenticatorRefusesAnIncompleteConfig` — gains the case the branch has never had (a review
  job account whose account is empty), so the refusal this task rewrites is exercised by a named test
  and not only by review.

**Red step:** `TestReviewAccountRefusalsNameTheAxisAndNotAnAccount` fails today, on all four
sentences.

**Mutation:** fix `auth.go` and leave `server.go`'s two copies — the service's own serve path then
refuses a provider-served review in words that name a Claude account, and the test's own four-site
check goes red.

**Reviewer tier:** **SEC** — opus. The files are the credentials service's, and its refusals are read
by whoever operates it, so a sentence that misdescribes what was refused is a security-relevant
inaccuracy rather than a wording preference (`.claude/rules/credentials-service.md`).

**Commit:** `credsvc: the review refusals name the account axis, not a Claude account`

### AK10 — the credential write refuses a bundle the deployed service cannot read (SEC)

**Slice:** none — **off the levels and off the critical path** (§1, §2's `—` rows). **Depends on:**
AK6b (the field whose write it guards); it was found by the security review of the merged AK4 and AK5
(§7, **F26**) and it is the guard AK6b's own shape requires rather than a change to AK6b.

**Files (4):** `internal/cli/creds.go`, `internal/cli/creds_test.go`,
`internal/cli/creds_skew_test.go` (new), `internal/cli/recipes_skew.go`.

**Interfaces:** `internal/credsvc`'s `ParseBundle` is strict — **an unknown key refuses the whole
document** (`bundleErr(ErrBundleUnknownKey, …)`), which the service answers as a service-wide refusal
rather than a refused credential — so a bundle written with AK6b's provider entry by a CLI newer than
the installation's deployed credentials service halts every caller of that service, coding runs
included, until `fugaro update` moves the service onto the image that reads it. `fugaro creds set` is
the bundle's only writer and an owner-only command, so the guard belongs at that write:
`internal/cli/recipes_skew.go` gains one `*Since`-style constant and one check — the idiom
`reviewAdmissionSince` established, judged against the installation's own record of its service
(`terraform.tfvars.json`'s `credentials_service.image`, `installationServiceRelease`) rather than
against a build record — and `internal/cli/creds.go` refuses to **write** a document whose newest key
the deployed service predates, naming `fugaro update` as the one command that fixes it. Two things
follow and both are part of this task: the release that ships AK6b carries the sentence under
`### For operators` in its own notes (CLAUDE.md's HARD RULE: `fugaro update` is the whole upgrade, and
what an operator may be asked is spelled there), and an installation whose service is already newer is
written exactly as today.

**Tests and command:**
`GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestCredsSetRefusesAProviderBundleAnOlderServiceCannotRead|TestCredsSetWritesWhenTheServiceIsNewer|TestCredsSetIsUnaffectedByAnInstallationWithNoService' -count=1`

- `TestCredsSetRefusesAProviderBundleAnOlderServiceCannotRead` — the write of a document carrying the
  provider entry against a recorded service image older than the constant is refused, nothing is
  written, and the sentence names `fugaro update`. **The red step.**
- `TestCredsSetWritesWhenTheServiceIsNewer` — the same write against a service at or past the constant
  succeeds, and the resulting document loads through `credsvc.ParseBundle`.
- `TestCredsSetIsUnaffectedByAnInstallationWithNoService` — an installation with no credentials
  service has no such skew (there is no service to refuse the document), and its bundle is written as
  today.

**Red step:** `TestCredsSetRefusesAProviderBundleAnOlderServiceCannotRead` fails today: no write path
reads the installation's service release, so the document is written and the older service refuses
every caller of it.

**Mutation:** judge the guard against the CLI's own version instead of the installation's record —
the test's older-service fixture is then written and the service-wide refusal ships, which is exactly
what **F26** found.

**Reviewer tier:** **SEC** — opus, with the independent security review mandatory. It is a refusal on
the credentials service's only write path.

**Commit:** `cli: creds set refuses a bundle the deployed service cannot read`

## 4. What is deliberately not in this plan

- **No new config key, except the one D1413's answer may need for its spelling.** The ask's own
  instruction — "decide how it fits the existing machinery rather than adding a parallel one" —
  rules out a key of its own for the credential. **D1413** is the one decision that could add a key
  (how a project says "the key, not the account"), and it is **answered: the key wins when the owner
  asks for it explicitly** — the spelling of that request is this plan's to design, and no task here
  names it yet, which **§6 records as work this plan still owes** rather than leaving it unsaid.
- **No second Anthropic key secret** (**D1412**), and no second secret name in
  `internal/config/config.go`'s `ReservedSecrets` or `internal/infra/spec.go`'s `SecretMounts`.
- **No per-key attribution.** `runstore.Cost.ModelBy` and `RouteBy` are what the tree has and what
  the ask's clause 1 means by "attributed"; AK3's new field is the narrower thing the record can
  honestly say, and **`StageTiming.Credential` is not repurposed to say it** (**F18**).
- **No change to the OAuth path.** The mixed run's stage account (**F7**) and the cloud class's
  tier-A credential (**F10**) are what they are; this plan adds a second way to be served and takes
  nothing away.
- **No api-key review for an `oauth` run** (**D1414**, **F8**).
- **No change to `internal/agent`'s environment allow-list.** `agent.BuildEnv` and
  `agent.OAuthStageEnv` are not this plan's files; the credential never reaches the agent in either
  slice, and that is the property the tasks assert rather than change.
- **No release notes in this folder.** The owner cut the path from 0.20.0 on 2026-11-06, and
  **D1421** is where a release is chosen: answered **after 0.20.0**, the release after it, which
  nobody has scheduled yet. A `docs/releases/` file written now would be a release note for a
  release nobody picked (CLAUDE.md: the `### For operators` entry ships in the same PR as the
  step, and there is no step yet).

## 5. Check 330

**Owner-run, on a sandbox installation and a sandbox repository only — never `belong`.** Written here
as the plan's text; the register's own section is written by AK8, under the number free at that
merge. The check has one shape per slice, and the owner runs the ones the answer to **D1410**
reaches.

**A. The in-job path (AK1–AK3).** A sandbox repository whose `fugaro.yaml` writes `agent.auth:
api-key`, a coder on the project's provider route and a Claude `models.reviewer`, with **no**
`claude_accounts` stage account published. Run one task. What to read off the run record and the
log:

1. the review stage's calls reached **Anthropic** and the coder's reached the **provider** — the
   gateway's call log names both;
2. the review stage's `model_usd` is the gateway's figure and `model_notional_usd` is **absent** —
   the dollars are real;
3. the run's `route_by` carries the coder's stages and not the review's;
4. **the contrast**: publish a `review` stage account and run the same task again — the stage's
   `credential` is `oauth` and `model_notional_usd` equals `model_usd`. The two runs side by side
   are what the check is for.

**B. The cloud Anthropic-key path (AK4–AK5).** A sandbox installation that publishes the pairing's
sentinel and has the credentials service on, with a repository whose own `agent.auth` is `api-key`.
The launch is **`fugaro review --pr N`**, or equivalently `fugaro run --pr N --recipe review-only` —
the review-only job, and the fallback the launcher's own refusal names. It is **not**
`fugaro run --review-run`: that flag carries an existing review's findings into a **fix round's**
brief (`internal/cli/run.go`) and needs `--pr` besides, which is a different thing entirely.
**This step was written for a tree on which E7's second half was not built** (**F10**):
`reviewJobAdmitted` was `false`, so the launch was refused outright until the service admitted the
review caller — **the flip of 2026-11-08 made it `true` (`docs/cloud-senior-review` §6's CSR11)**, so
the launch is admitted and what this step measures is the service's own answer on a live installation.
Read off: the review job
was launched for the variant the sentinel names; its model calls were served by the project's
Anthropic key; the job's own environment held no credential of the repository's; and a repository
whose `agent.auth` is `api-key` **without** the pairing naming the key is still refused, with the
sentence AK5 rewrote.

**C. The OpenRouter path (AK6a, AK6b, AK7).** Taken by **D1410** and sequenced after the Anthropic-key
half by **D1416**: the same as B with a provider
route claiming the Claude ID as one explicit `anthropic/<id>` (**F22**), reading off that the key
reached the provider's own host and Anthropic saw no call. **This step may not be run before
Check 50** — the live check **D150** and **C3.6** gate a third-party route for a Claude model on, and
it is **not run**: `internal/creds/select.go`'s `checkCandidates` refuses every tier-C candidate in a
sentence naming it, so a run of this step today would measure that refusal rather than the path, which
is the one thing the paragraph below exists to keep a check from doing (§6 item 10).

**What the check must not be run against.** A repository the project depends on for real work, and
any installation whose budget mode is `off` — the review class refuses a run with no gateway
(**F11**), so a mode-`off` installation measures the refusal rather than the path.

## 6. The gates a task may not start without

**Items 1–5 below are satisfied as of 2026-11-07**, each recording what the owner's answer settled or
the approval of that day gave. **Item 6 is not the owner's answer to give and is now partly settled**:
the security review owed before AK4–AK7 land is **written for the two that landed** —
[security-review.md](security-review.md), §7 below — so AK4 and AK5 landed under it rather than past
it, and it **stands for AK6a, AK6b and AK7**, which have not landed. **Item 7's gate is lifted** — E7's
second half reached the tree on 2026-11-08 (**U6**) — so the launch is admitted where it was refused,
though no live installation has been measured against it. **Item 3's approval covered the tasks that
existed on 2026-11-07: AK9 and AK10 are written and not approved.** **Item 9 records the reconciliation
of 2026-11-09 and item 10 a gate no task carried, which that reconciliation found.**

1. **D1410 answered: both slices** — the plan is about all three (reconciliation §9.1).
2. **D1421 answered: after 0.20.0** — the release for AK8 and for any release note.
3. **The owner's own go: given.** He approved every task on 2026-11-07 (owner: "All tasks approved.
   Jobmax"), so no task here waits on a further approval — unlike the ones in
   `docs/cloud-senior-review`. **That approval is AK1–AK8 as they stood that day**; the two tasks the
   security review added later — **AK9** and **AK10** — are written and **not approved**.
4. **AK4 and AK5 were gated on D1416, D1417 and D1422; AK6a, AK6b and AK7 on D1418, D1419 and a
   reconciliation with §6.7's CSR17–CSR18**, which own the sketch this plan reads. D1416, D1417,
   D1419 and D1422 are answered as recommended. **D1418 is answered, 2026-11-09 — see item 9** — and
   the reconciliation it was deferred to is done: **the provider's base URL and the auth shape go in
   the bundle**. Its shape is the owner's own ruling: an **additive optional field**, so an older
   bundle still decodes and no format version moves (a version would move only if a field were removed
   or retyped, and **a field never changes meaning**), which settles three things for the two tasks
   that carry it: the bundle's field names are **new names** and never a reuse of `secret` or
   `kind`'s existing values, **a provider the bundle does not describe fails closed** (no defaulted
   base URL, no defaulted auth shape), and **no test, fixture or doc carries a secret-shaped literal**
   for either — a base URL and an auth shape are configuration and are written as such.
5. **AK3 gated on D1415 — answered: the record names the credential, in a new field** and not a
   new value on `credential` (**F18**): the record is a wire shape another reader consumes, and
   CLAUDE.md allows an additive field there but not a changed meaning.
6. **The security review owed before AK4–AK7 land**, not after: each is SEC, and the review is
   mandatory rather than advisory. **Partly settled, 2026-11-09**: the review of the merged AK4 and
   AK5 is written (§7, [security-review.md](security-review.md)) — what it read, the three attack
   questions (the bundle, the forwarded key, the base URL) and its findings — and it found **no defect
   that lets the key or the bundle out of the service**. It **stands for AK6a, AK6b and AK7**, and the
   conditions §7 lists are what those tasks keep rather than re-decide.
7. **AK4–AK7 were gated on E7, and it was not this plan's to meet** (**F10**, **U6**;
   `docs/cloud-senior-review` §1) — **that gate is lifted, because E7's second half reached the tree
   on 2026-11-08**: `internal/cli/review.go`'s `reviewJobAdmitted` is `true` (CSR11's flip, resting on
   **CSRW1** and **CSRW2**), so a `fugaro review --pr N` is admitted where it was refused. What the
   flip did not do is measure a live installation — the owner's live checks are still unrun — and what
   these tasks add is the cloud path *served by a key*: a plan that read the flip as having given an
   owner a working api-key review would be selling a path nobody has measured. Nothing here takes
   E7's live measurement, and no release of this design is worth anything to a user until it is met.
8. **AK5 was gated on the sentinel's job shape, and AK5 takes it — taken 2026-11-07.** AK4 made the
   sentinel publishable and servable and no installation could *deploy* it, and the readers of the
   account axis that had to yield — plus the account-choice keys AK4 refuses outright — are listed in
   AK4's section and booked in §2. What was not decided anywhere, and was a product question rather
   than a mechanical one, is decided here: **a profile that names the sentinel gets a coding job**,
   its account segment **is** the sentinel (`gcp.ProjectJobName(profile, provider, sentinel)`, the
   module's own `job.account` validation admitting the literal, pinned in Go by the deployer's test),
   nothing is mounted on it (`config.ProjectJobTokenAccount` answers `("", false)`, so it is a job no
   Claude token reaches, exactly as a profile that lists no `default` has), and beside it the
   installation deploys **that job's review-only variant** — the segment
   `credsvc.ReviewJobAccountsFor` and the deployer agree on, `ReviewVariantAccount(sentinel)`, and the
   name the launcher resolves through `credsvc.ReviewJobName`. That variant is the job the pairing's
   key is served from, and it is what gives a project with no Claude account a review job at all
   (**F17**, **D1422**). Two answers come with it. The **launcher** places the sentinel on that job
   and not on the profile's no-account one (`internal/cli/project_job.go`, a file §2 does not book and
   AK5 adds), and a **stage** of a run may still not be served by the project's own key: AK4's
   choice-key refusal stands, because a stage's account is a token the job's environment already
   carries, while the key reaches a run only as the class's own pairing. The gate, before this task
   started: an installation that published a profile naming the sentinel was refused by
   `fugaro update` — fail-closed, for **every** profile and not only the shared-job ones a Bitbucket
   profile is skipped from deploying, with AK4's own sentence in `internal/infra/projectjobs.go`
   naming the move that worked (take the key off the profile) — so the state was safe and honest, but
   not a state to ship a release on.

9. **The reconciliation with `docs/cloud-senior-review/implementation-plan.md` §6.7 is done, 2026-11-09**,
   and it settled the one point the two plans disagreed about: **§6.7's CSR17 was right and this plan's
   six-file AK6 was the loser**. The reason is §1's and §2's — the account axis's value **is the
   admission unit** (`projectpolicy`'s `DeriveAccounts` keys the `accounts` block by it and `credsvc`'s
   `admittedAccounts` intersects a repository's child with it), the pairing and not the cascade's
   selector fixes which credential serves a review call, and `offeredToReview` matches by name **and**
   tier, so a provider key riding the sentinel would carry the Anthropic key's own admissions and force
   the class to offer two credentials where its rule is exactly one with no fallback. **AK6 is
   therefore two tasks**: **AK6a** (the second axis value — 24 files, and **`internal/credsvc/review.go`
   among them, the file §6.7 booked and this plan did not**) and **AK6b** (the service's document, its
   offer and its forward — 9 files). **AK7 is amended** to CSR18 minus two clauses: `internal/gateway/gateway.go`
   is dropped (its own condition was "if the upstream kind or the auth shape moves", and with D1418
   both are the bundle's, so nothing moves) and `internal/runner/runner.go`'s call site with it, while
   `internal/runner/agentenv_test.go` is kept. **§2 books every file of both tasks, drops the
   conditional gateway row, and says which L2 rows AK6a re-enters**, and **§6.7 itself is amended in
   the same pass** — CSR17 and CSR18 now point at AK6a, AK6b and AK7 rather than at this plan's old
   AK6, and CSR17's "**Files (sketched, 6–8)**" is corrected: it covered the service half only and was
   short of the axis's own readers.
10. **A gate no task carried, found by that reconciliation: Check 50 is not run, and the OpenRouter
    half's served path waits on it** (**D150**, **C3.6**). `internal/creds/select.go`'s `checkCandidates`
    refuses **every** tier-C candidate in a sentence naming Check 50, and `TestSelectRefusesReservedTierC`
    pins that refusal; the provider key is offered as that tier (**F16**), so the tree refuses a review
    served by a provider key today. **The check is the owner's and no task in AK1–AK10 carries it**:
    AK6b may land its document, its offer and its forwarding — each unit-tested — and AK7 its runner
    path, but **§5's step C may not be run** and no release note may present a provider-served review as
    working until Check 50 has run or the owner takes a new decision. Reading AK6b as having shipped a
    working provider path would be selling a refusal, which is the one thing §5's "what the check must
    not be run against" paragraph exists to prevent.

**One answer leaves work that no gate and no task carries yet, and it is named here so it is not
lost:** **D1413** settles *that* the key wins when the owner asks for it explicitly, and **the
spelling of that request is this plan's to design** (reconciliation §7, §9.3), but **no task in
AK1–AK8 names it** (§4). It is not an owner decision waiting to be taken and it blocks no task: it is
design work this plan still owes, and the task that writes the explicit-request path is the one that
will have to spell it. **What AK5 does about it, on the mechanism's side only:** the sentinel is
admissible at the account flag `--token` (and by inheritance from a launch that used one, which is
what `--retry` and a follow-up read), because that is the chain `claudeAccountFor` resolves a launch's
account through — and it is still refused by `config.Validate` where a *repository's* own
`fugaro.yaml` writes it in `agent.oauth_token`, which is the document rule AK4 did not book either: a
document is not where the owner asks for the project's platform-held key, and a repository that wrote
it would put every one of its runs, coding runs included, on the key's job. The user-facing spelling
stays owed, and the door for it is open as of 2026-11-08: the launch it would be spelled at is
admitted since E7's second half reached the tree (item 7, **U6**), so a `fugaro review --pr N` runs,
while what the owner still cannot do is ask for the project's key on that launch explicitly.

## 7. The security review of the merged AK4 and AK5

**Written 2026-11-09, by this pass, as [security-review.md](security-review.md)** — item 6's first
half. It reads the tree the two tasks landed (`1d2591d5`, PR #1860, and `0944d4d4` with `9fa5fd90`, PR
#2033) rather than the plan's sketches, and it is a **review of code that is already on `main`**, so its
findings are tasks or notes and never edits to this plan's own design.

**What it read.** `internal/credsvc`'s whole request path — `server.go` (the routes, the caller's
verification, the class's own door), `auth.go` (`AuthConfig`, the job set and the review set),
`review.go` (`ReviewJobAccount`, `ReviewJobAccountsFor`, the pairing's offer), `bundle.go`
(`CredentialKey`'s closed set and `ParseBundle`), `forward.go` (`send`'s upstream, destination and
redaction), `cascade.go` (the wire tiers and the candidate list), `probe.go` (the sweep's own audit) and
`audit.go`; `internal/creds/select.go` (which candidate a call is offered, and the tier-C refusal);
`internal/config/claude_accounts.go` and `project_secrets.go` (the sentinel and the project's key reach);
`internal/gateway/export.go` and `gateway.go` (the helpers the service reuses and the base it builds);
`internal/runner/routing.go` (the class's model path); `internal/cli/run.go`, `project_job.go` and
`creds.go`; `internal/projectpolicy`'s `accounts.go` and `policy.go`; `internal/task/task.go` with
`schemas/task.schema.json`; and `modules/installation/variables.tf` with `modules/project/variables.tf`.

**The three attack questions, and the answers.** *What a hostile agent can do with the bundle:* **it
cannot reach it** — the job holds no credential, the service's whole Secret Manager reach is one
accessor grant on the one container (Global Constraint 12), the document is written by `fugaro creds
set` alone and that command refuses a coding agent's session, a run's own process and the GODEBUG
leak before it opens the cloud, no workflow may declare the container, and no header, body or audit
line of an answer carries a value (chained through `gateway.NewRedactWriter` for the credential and
every caller token). *What it can do with the forwarded key:* **it gets a call forwarded under it and
nothing else** — the caller cannot steer the host (the destination is a constant base joined to a path
from `servedRoute`'s closed set, and the query is dropped where it carries a caller token), cannot
steer the credential (the pairing names exactly one, `offeredToReview` matches by name and tier with no
fallback, and a repository's own file cannot choose the class's credential), the redirect is refused by
the client, and the mint is scoped to the pairing's canonical repository under the read-only permission
set. *What it can do with the base URL:* **nothing today** — no base URL is in the bundle, because
**D1418** says where it goes and no task has written it yet, so §6.7's and this plan's provider
endpoint is a property of **AK6a/AK6b/AK7** rather than of the merged code, and the review file's §5
states the conditions those tasks keep: the base
URL is the bundle's and never the runner's, an unknown provider fails closed, and the destination rule
and the redaction stay where they are.

**Findings.** **F25** — four refusal sentences in `internal/credsvc/auth.go` and `server.go`, and the
module's prose for `review_job_accounts`, still say a review call is served by a **Claude account**,
which AK4 made inexact (the sentinel is not one); **AK9**. **F26** — `ParseBundle` refuses a **whole
document** on an unknown key, so a bundle written with AK6b's provider entry by a CLI newer than the
installation's deployed service halts every caller of that service until `fugaro update`; **AK10**, with
the release's `### For operators` sentence. **F27** — `README.md`'s status line still read "items 6 and
7 … not met, so those four tasks may not start", and its file table still claimed "at most six" files
per task; **fixed in this docs pass** and not a task. **No finding lets the key or the bundle out of
the service, and nothing found is a defect in the merged behaviour**: F25 is wording, F26 is a guard a
task's own write path needs, F27 was a stale doc.

**What the review could not verify, and says so:** no live installation was measured (the owner's live
checks are unrun, item 7), the service's behaviour against a real Secret Manager, a real Firebase
project and a real provider is not measured anywhere in this folder, and the review read `main`'s code
rather than a running service. **AK9 and AK10 are written and not approved** (item 3), and neither
blocks a task: they are off the levels and off the critical path (§1, §2).
