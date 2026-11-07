# The api-key review path — implementation plan

**Status: plan only; nothing is implemented; no task approved.** Every task below needs the owner's
own explicit go, and the first of them needs an answer before it — see §6 and
[reconciliation.md](reconciliation.md) §9. Where this file and
[reconciliation.md](reconciliation.md) disagree about the code, the reconciliation wins.

**Read with:** [spec.md](spec.md) (the owner's ask) and [reconciliation.md](reconciliation.md) (what
the tree does today, and **D1410–D1429**). Every path and symbol cited here exists on `main` at
`aa26f322f`; the findings in parentheses are the reconciliation's.

## 1. The slices, the levels and the critical path

The plan is **three slices that can be taken independently**, plus the live check. The slices follow
the reconciliation's §4: the in-job half needs no service change, the cloud Anthropic-key half is the
cheap half of `docs/cloud-senior-review/implementation-plan.md` §6.7, and the OpenRouter half is
§6.7 as written. **D1410 decides which of them the owner wants**; nothing here presumes the answer,
and a slice the owner does not want is simply not started.

| Level | Tasks | What it is | Why these can run together |
| --- | --- | --- | --- |
| **L1** | **AK1**, **AK2**, **AK3** | the in-job path named and pinned: tests (**F5**), docs (**F9**), a field of the record's own for the credential a stage ran on (**F18**, **D1415**) | no shared file, no service change, no ordering between them |
| **L2** | **AK4**, **AK5** | the cloud Anthropic-key half: the class may be served by the project's key (**F12**, **F13**, **F14**), and the runner admits it and the variant is launchable with no Claude account (**F11**, **F17**) | AK4 is `internal/credsvc` and AK5 is `internal/runner` + `internal/cli` + `internal/infra`; the two share no file |
| **L3** | **AK6**, **AK7** | the OpenRouter half: the bundle holds a provider key (**F15**, **F16**, **F24**), and the review run's model path reaches a provider's endpoint | AK6 is `internal/credsvc` and `schemas/`; AK7 is `internal/runner` and `internal/gateway` |
| **L4** | **AK8** | the live check, **Check 330** (§5) | owner-run, after everything else |

**The critical path is `AK1 → AK4 → AK5 → AK6 → AK7 → AK8`**, and it is the shortest chain the
dependencies allow: AK1 is the slice that proves the path the ask names first, AK4 is the service's
side (a runner path to a service that cannot serve it is a halt), AK5 is the runner's admission, AK6
is the provider key the OpenRouter half needs, AK7 is the runner's path to it, and AK8 measures the
result on a real installation. **AK2 and AK3 are off the critical path** — AK2 is documentation and
AK3 is the record — and either may land at any point in L1 without moving it.

**D1410, D1421 and the gates in §6 are answered before a task starts, not during one.** If the owner
takes **D1410** as "the in-job half alone", L2 and L3 are not started and the plan ends after AK3.
If the owner takes **D1416** as "both keys at once", AK6 and AK7 become one SEC task and the plan
grows a level.

## 2. The shared-files table

**No level has two writers of one file.** A file that two *levels* touch is fine — the levels run in
order — and the table says which level each is booked in, so a task that wants to move a file between
levels has to say so here first.

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
| `internal/credsvc/review.go` | L2 | AK4 |
| `internal/credsvc/review_test.go` | L2 | AK4 |
| `internal/credsvc/cascade.go` | L2 | AK4, then AK6 |
| `internal/credsvc/cascade_test.go` | L2 | AK4 |
| `internal/config/claude_accounts.go` | L2 | AK4 |
| `internal/runner/routing.go` | L2 | AK5, then AK7 |
| `internal/runner/routing_internal_test.go` | L2 | AK5, then AK7 |
| `internal/cli/run.go` | L2 | AK5 |
| `internal/infra/projectjobs.go` | L2 | AK5 |
| `internal/infra/projectjobs_test.go` | L2 | AK5 |
| `internal/credsvc/bundle.go` | L3 | AK6 |
| `internal/credsvc/bundle_test.go` | L3 | AK6 |
| `internal/credsvc/forward.go` | L3 | AK6 |
| `internal/credsvc/forward_test.go` | L3 | AK6 |
| `schemas/creds-bundle.schema.json` | L3 | AK6 |
| `internal/runner/agentenv.go` | L3 | AK7 |
| `internal/gateway/gateway.go` | L3 | AK7, if the upstream kind moves |
| `docs/gcp-live-checklist.md` | L4 | AK8 |

Four files are booked twice, each across two levels: `internal/credsvc/cascade.go` (AK4's candidate
list, AK6's tier map), `internal/runner/routing.go` (AK5's admission, AK7's upstream),
`internal/runner/routing_internal_test.go` (the same two tasks' tests) and
`internal/gateway/gateway.go` (AK7 only, listed here so a later task knows it is taken). A task that
needs a file another **level** holds adds itself to this table in the same commit.

## 3. The tasks

Each task states its files, its interfaces, its named tests and the command that runs them, its **red
step** (what fails before the change), its **mutation** (what a careless implementation would do, and
which test catches it), its **reviewer tier**, what it depends on and its commit message. **A task
with no red step says so and carries a mutation instead**; a task with neither is not a task.

The command shape is the repository's: `GOFLAGS=-p=2 go test ./<pkg>/ -run '<TestA|TestB>' -count=1`.
`internal/runner` and `internal/cli` each take about 25 minutes in full, so **every command below
names its tests**; the whole package is run only when a change is broad enough to need it.

### AK1 — the in-job api-key review is a pinned path

**Slice:** in-job (L1). **Depends on:** **D1410** answered as the in-job half or as both.

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

**Slice:** in-job (L1). **Depends on:** **D1415** answered as "a new field". **If the owner answers
D1415 as "nothing at all", this task is not started and L1 is AK1 and AK2.**

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

**Slice:** cloud, Anthropic key (L2). **Depends on:** AK1's slice decided (**D1410**), **D1416** as
"Anthropic key first", **D1417** as "the project's own key".

**Files (5):** `internal/credsvc/review.go`, `internal/credsvc/review_test.go`,
`internal/credsvc/cascade.go`, `internal/credsvc/cascade_test.go`,
`internal/config/claude_accounts.go`.

**Interfaces:** the pairing's account axis gains one value that is **not** a Claude account — the
sentinel the admissions spell for "the project's own Anthropic API key" — resolved in
`internal/config/claude_accounts.go` beside the names the axis already derives; and
`internal/credsvc/cascade.go`'s `reviewCandidates` offers the bundle's tier-B entry
(`config.AnthropicSecret`, `CredentialKey`) when the pairing names it, **and still exactly one
credential and no fallback** (**F13**). Nothing in `internal/credsvc/forward.go` changes: the
tier-B arm already builds `gateway.Upstream{Kind: upstreamKindAnthropic, APIKey: e.Value()}` and
attaches it with `gateway.UpstreamHeaders` (**F12**), and that is the whole reason this task is
small.

**Tests and command:**
`GOFLAGS=-p=2 go test ./internal/credsvc/ ./internal/config/ -run 'TestReviewPairingMayNameTheProjectsAnthropicKey|TestReviewCandidateListIsExactlyOneCredential|TestReviewCallerIsNeverOfferedAProviderKey|TestReviewCandidateListStillOffersThePairingAccount' -count=1`

- `TestReviewPairingMayNameTheProjectsAnthropicKey` — a review caller whose pairing names the
  sentinel is served by the bundle's `config.AnthropicSecret` entry, and the forwarded call carries
  the key to Anthropic's own address. **The red step.**
- `TestReviewCandidateListIsExactlyOneCredential` — the list is one entry long and the cascade's
  ladder has nothing to fall to, whatever the policy's `fallback` says.
- `TestReviewCallerIsNeverOfferedAProviderKey` — the negative that keeps this task from becoming
  AK6: a bundle holding a provider key offers it to no review caller.
- `TestReviewCandidateListStillOffersThePairingAccount` — the second direction: a pairing that names
  a Claude account is served exactly as it is today.

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

**Files (5):** `internal/runner/routing.go`, `internal/runner/routing_internal_test.go`,
`internal/cli/run.go`, `internal/infra/projectjobs.go`, `internal/infra/projectjobs_test.go`.

**Interfaces:** `internal/runner/routing.go`'s `checkReviewClassModelPath` admits the review class's
new credential — the pairing's sentinel — beside `"oauth"` and `config.AuthCascade`, and refuses
`"api-key"` and `"vertex"` exactly as it does today; `internal/cli/run.go`'s `claudeAccountFor` and
`mixedRunAccount` resolve the sentinel to the review variant that is deployed for it; and
`internal/infra/projectjobs.go` deploys that variant, so an installation that publishes the sentinel
and no Claude account has a review job at all (**F17**). The class's door is unchanged:
`reviewClassRun`'s conjunction of the run's mode and the job it was admitted to is what keeps a
review job from being traded for a coding one, and nothing here weakens it.

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

### AK6 — the bundle holds a provider key, and the service forwards to a provider's endpoint (SEC)

**Slice:** cloud, OpenRouter (L3). **Depends on:** AK5, **D1416** as "both keys", **D1418** and
**D1419** answered. **This task is `docs/cloud-senior-review/implementation-plan.md` §6.7's CSR17,
and it must be reconciled with it before it starts** — §6.7 owns the sketch and this task owns the
reconciliation's reading of it.

**Files (6):** `internal/credsvc/bundle.go`, `internal/credsvc/bundle_test.go`,
`internal/credsvc/cascade.go`, `internal/credsvc/forward.go`, `internal/credsvc/forward_test.go`,
`schemas/creds-bundle.schema.json`.

**Interfaces:** `CredentialKey`'s closed set gains a provider-key kind (**F15**); the bundle gains
the routing facts a provider call needs — a base URL and an auth shape of `bearer` or `x-api-key`,
which are **not** credentials (**D1418**); `cascade.go`'s `wireTier` maps the reserved
`creds.TierC` (**F16**) so a provider key is offered as a tier rather than a name; and
`forward.go`'s `send` builds the provider's upstream and destination rather than
`upstreamKindAnthropic`'s. **No Terraform:** the service holds one secret document (Global
Constraint 12), so a new credential kind is a bundle key and never a resource or a grant.

**Tests and command:**
`GOFLAGS=-p=2 go test ./internal/credsvc/ -run 'TestBundleHoldsAProviderKey|TestProviderKeyEntryIsNotOfferedToAnyOtherCaller|TestForwardToAProviderCarriesTheProviderAuthShape|TestProviderDestinationIsTheProvidersOwnHost' -count=1`

- `TestBundleHoldsAProviderKey` — a bundle holding a provider key loads, and `CredentialKey` names
  its kind and tier. **The red step.**
- `TestProviderKeyEntryIsNotOfferedToAnyOtherCaller` — a coding caller's candidate list is what it
  was; the provider key is offered to the review pairing alone.
- `TestForwardToAProviderCarriesTheProviderAuthShape` — a `bearer` key goes on `Authorization`, an
  `x-api-key` key on `X-Api-Key`, and neither goes to Anthropic.
- `TestProviderDestinationIsTheProvidersOwnHost` — the destination rule holds: the key reaches the
  host its base URL names and no other.

**Red step:** `TestBundleHoldsAProviderKey` fails today: `CredentialKey`'s closed set has no entry
for a provider key, so the loader refuses the document (**F15**).

**Mutation:** send the provider key to `upstreamKindAnthropic`'s address —
`TestProviderDestinationIsTheProvidersOwnHost` goes red.

**Reviewer tier:** **SEC** — opus, with the independent security review mandatory. This is a
credential the service attaches to a caller's request **and** a destination rule, which is the pair
`internal/credsvc/forward.go` exists to keep apart.

**Commit:** `credsvc: the bundle can hold a provider key`

### AK7 — the review run's model path reaches a provider's endpoint (SEC)

**Slice:** cloud, OpenRouter (L3). **Depends on:** AK6. **This task is §6.7's CSR18.**

**Files (4):** `internal/runner/routing.go`, `internal/runner/agentenv.go`,
`internal/runner/routing_internal_test.go`, `internal/gateway/gateway.go` (only if the upstream kind
or the auth shape a provider call carries moves).

**Interfaces:** the class's model path stops being the service's Anthropic forward alone — the
upstream a provider-served review call carries is the provider's, and `internal/runner/agentenv.go`'s
gateway variables carry what a provider's model list needs. The pins and the allow-list are
unchanged: the review is still held to the project policy's `budget.allowed_models` (**D1425**).

**Tests and command:**
`GOFLAGS=-p=2 go test ./internal/runner/ ./internal/gateway/ -run 'TestReviewRunOnAProviderKeyReachesTheProvider|TestReviewRunIsStillHeldToTheAllowList|TestReviewRunIsServedByTheProjectsAnthropicKey' -count=1`

- `TestReviewRunOnAProviderKeyReachesTheProvider` — a review run served by a provider key reaches
  the provider's endpoint with the provider's auth shape, and Anthropic's fake sees no call. **The
  red step.**
- `TestReviewRunIsStillHeldToTheAllowList` — the gate the class is already held to is not moved by
  this task.
- `TestReviewRunIsServedByTheProjectsAnthropicKey` — the second direction, re-run from AK5: the
  Anthropic-key path is unchanged by the provider one.

**Red step:** `TestReviewRunOnAProviderKeyReachesTheProvider` fails today: the class's upstream is
Anthropic's and there is no path that carries a provider's base URL.

**Mutation:** drop the provider's base URL and let the call fall to Anthropic's address — the test's
own destination assertion goes red, and the review's prompt reaches a vendor the project did not
choose.

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

## 4. What is deliberately not in this plan

- **No new config key.** The ask's own instruction — "decide how it fits the existing machinery
  rather than adding a parallel one" — rules out a key of its own. **D1413** is the one decision
  that could add one (how a project says "the key, not the account"), and the plan waits for it.
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
- **No release notes.** The owner cut the path from 0.20.0 on 2026-11-06, and **D1421** is where a
  release is chosen; a `docs/releases/` file written before that answer would be a release note for
  a release nobody picked (CLAUDE.md: the `### For operators` entry ships in the same PR as the
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
Run `fugaro run --review-run` and read: the review job was launched for the variant the sentinel
names; its model calls were served by the project's Anthropic key; the job's own environment held no
credential of the repository's; and a repository whose `agent.auth` is `api-key` **without** the
pairing naming the key is still refused, with the sentence AK5 rewrote.

**C. The OpenRouter path (AK6–AK7).** Only if the owner takes it: the same as B with a provider
route claiming the Claude ID as one explicit `anthropic/<id>` (**F22**), reading off that the key
reached the provider's own host and Anthropic saw no call.

**What the check must not be run against.** A repository the project depends on for real work, and
any installation whose budget mode is `off` — the review class refuses a run with no gateway
(**F11**), so a mode-`off` installation measures the refusal rather than the path.

## 6. The gates a task may not start without

1. **D1410 answered** — which slice the plan is about (reconciliation §9.1). **Blocking, and first.**
2. **D1421 answered** — the release, before AK8 and before any release note.
3. **The task's own go from the owner.** An answer is not an approval, and this plan's tasks are no
   more started than the ones in `docs/cloud-senior-review` are.
4. **AK4 and AK5 gated on D1416, D1417 and D1422; AK6 and AK7 on D1418, D1419 and a reconciliation
   with §6.7's CSR17–CSR18**, which own the sketch this plan reads.
5. **AK3 gated on D1415** — whether the record names the credential at all, and if it does, that it
   is a **new field** and not a new value on `credential` (**F18**): the record is a wire shape
   another reader consumes, and CLAUDE.md allows an additive field there but not a changed meaning.
6. **The security review owed before AK4–AK7 land**, not after: each is SEC, and the review is
   mandatory rather than advisory.
