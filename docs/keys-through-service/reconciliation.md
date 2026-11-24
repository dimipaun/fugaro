# Every model key through the credentials service: reconciliation

Checked against `origin/main` at `45658d8a8` on 2026-11-19; the symbols review round 2 named were
read again at `39344123a`. **Where this file disagrees with
[spec.md](spec.md), this file wins.** It cites paths and symbols, never line numbers. Decisions are
**D3000–D3015**, all answered by the owner (D3000–D3011 on 2026-11-19, D3012–D3015 on
2026-11-20; D3003–D3007 were added by review round 1, D3008–D3012 by round 2, D3013–D3015 by
round 3). **D3009 withdraws D3002 and D3004:** budget mode off is removed, under
a spec of its own. It reverses **D1634** and **D1670**
([model crafts](../model-crafts/reconciliation.md)) and narrows the code behind **D150**
([credentials service](../credentials-service/reconciliation.md)); each is marked where it bites.

**Amended 2026-11-20 by [credentials belong to models](../credentials-belong-to-models/README.md)**
(findings F48–F51, open item O13): KS6, KS9 and KS10 are withdrawn and their work is that plan's,
KS13 waits on that plan's I1, C1's second fact is not needed, and KS3's "under every auth" clause
is that plan's CM6. The entries they touch are marked in place.

**Amended 2026-11-22 by [model credentials routing](../model-credentials-routing/README.md)
(D3170).** Credentials belong to models is itself superseded in part: every task of it that is not
merged is withdrawn, its I1 among them, so **KS13 now waits on model credentials routing's I1**
(its A1), which puts the work of the withdrawn CM6, CM9, CM15 and CM16 in the tree. The entries
below that named the earlier plan's I1 are marked in place.

## 1. What the spec asks for, and what the repository already has

| The spec's concept | What the repository has |
| --- | --- |
| "Served by the credentials service" | True today for a Claude OAuth token and an Anthropic key on a switched repository (`config.CredentialServingPath` answers `CredentialPathCredsvc` for `claude-oauth` and `anthropic`) |
| A provider key (OpenRouter and the like) | The runner path: mounted in the job as `FUGARO_PROVIDER_KEY_*` and spent by the run's own gateway (`CredentialPathRunner`; `infra.SecretMounts`, `runnerKeyCredential`, `runner.ProviderCredential`) |
| Vertex | No key at all: the job's own service account, through `google.DefaultTokenSource` |
| "The switch" | Per repository and conditional: `infra.CredentialsServiceServes(installed, ready, auth, mode)` |
| The Terraform that mounts a key | One Secret Manager container per logical secret per repository with operator grants, one accessor per job per secret, one env entry per mount, and a three-valued `model_secrets_served` that selects among them |

## 2. Assumptions

- **A1.** "Keys" means model credentials: Claude OAuth tokens, Anthropic keys and provider keys.
  The voice provider keys' container is not one of them.
  Git tokens, the GitHub App key, workflow secrets and signing keys are not in scope; secrets
  brokering owns them (B4.9, B5.7's git half, C2.3).
- **A2.** Vertex is not a key and nothing is mounted for it (D498). "Served through the service"
  has nothing to move, so Vertex is unchanged.
- **A3.** A local run (`fugaro exec`, `fugaro troubleshoot`) uses the developer's own environment
  and is unchanged (D149).
- **A4.** *(Corrected by review round 1, F25.)* The work ships in two releases, as the earlier
  flips did. The first serves every model key through the service while the mounts are still
  there. The second drops the mounts, behind a launch gate that refuses a run on a job image older
  than the first with the fix printed (secrets brokering's rule G17: a job never loses a mount
  before the service serves that credential to the image it runs).
- **A5.** The provider models a project uses today keep working with no config change by the
  owner: the same `providers:` and `credentials:` blocks, the same pins.

## 3. Findings

**F1 — Exists.** A switched repository's Claude OAuth token and Anthropic key are already served
by the service, and their job mounts are already dropped (`infra.ModelSecretsServed`,
`dropModelMounts` in `internal/infra/readiness.go`, `config.ServiceServedAuths`). Consequence: the
work is the remainder, listed in F2 to F6.

**F2 — Gap: provider keys.** Every `api-key` job still mounts each provider key it uses
(`ModelSecretsServed` answers `anthropic` and keeps them; `TestFilledServiceServedAuthsKeepsProviderKeys`).
`config.CheckProviderAuth` refuses a provider-served pin under every auth but `api-key`.
Consequence: KS1 to KS5.

**F3 — Gap: the service cannot serve a named provider key.** The bundle holds one `provider-key`
entry (`Bundle.providerKey`), where the runner path holds one key per declared provider.
`fugaro creds set` refuses the key of an `openrouter` credential (`credsKeyCredentialHalfRefusal`),
and `fugaro accounts add --type openrouter` writes the job's own container. Consequence: KS1, KS2.

**F4 — Rule: the gateway does the provider's work, and the service does none of it.** *(Corrected
by review round 1, F17.)* For a provider model the run's gateway routes by model pattern
(`routeFor`), refuses unpriced fields (`checkRequest`), adds the route's fee (`Route.FeePct`),
settles on the provider's reported cost (`fromUsage`) and keeps a ledger per route
(`internal/gateway/forward.go`, `route.go`). `allow_data_to` is applied by the runner
(`internal/runner/providers.go`), and the zero-retention and price-tier rules by the config and
policy code, before the run. The service's `providerRoute`
(`internal/credsvc/forward.go`) leaves the models and the fee at zero by its own comment. The
gateway forbids the combination: `validateRoutes` and `errServiceOnRoute` say a provider route is
never reached through the service. Consequence: R1.

**F5 — Wrong against D150's wording.** `creds.checkCandidates` (`internal/creds/select.go`) refuses
every tier C candidate without looking at the model, and a coding caller is never offered one
(`Bundle.Credentials` leaves it out). D150 gates a Claude model through a provider, for the reason
that prompt-cache behaviour there is unmeasured. Check 50 measures exactly that and nothing about
a non-Claude model. Consequence: D3000, KS3.

**F6 — Gap: the other mounts that remain.**
- A repository the switch does not cover: `CredentialsServiceServes` is false for budget mode off
  (D2071), for a root not re-applied and for a bundle that is not ready.
- The mixed stage-account variant keeps its OAuth token in the runner's environment and hands it
  to Anthropic outside the gateway (`WorkflowSpec.StageAccount`; `mixedStageAccount` in
  `internal/cli/run.go`; `TestSwitchedMixedRepositoryKeepsItsStageAccountToken`).
- Every shared project job mounts its account's token (`projectAccountMount`,
  `internal/infra/projectjobs.go`), which is secrets brokering's B5.7, not started.
Consequence: D3009 for the budget mode, KS9 and KS10 for the stage account — withdrawn on
2026-11-20, F48 — and KS16 follows B5.7.

**F7 — Gap: budget mode off has no path to the service.** With the budget off the run has no
gateway, and `agent.BuildEnv` (`internal/agent/env.go`) puts the key in the agent's own
environment. `serviceSwitchModeRefusal` (`internal/runner/gateway.go`) refuses a switched
repository in that mode. Consequence: D3002 at first, withdrawn by D3009.

**F8 — Gap: nothing copies a key into the bundle.** `fugaro creds set` is the bundle's only writer.
An installation's existing keys sit in per-repository containers
(`google_secret_manager_secret.this`, `modules/repo/secrets.tf`, with `prevent_destroy`) and in
the project-wide ones (`google_secret_manager_secret.project`, `modules/installation/secrets.tf`).
Consequence: D3001, KS12.

**F9 — Rule: what Terraform holds per credential.** Per repository: one container and two operator
grants per operator. Per job: one accessor for the job's account
(`google_secret_manager_secret_iam_member.job`, `modules/workflow/sa.tf`), one for the service's
account where it reads the mount (`.service`, D2074), and one env entry (`modules/workflow/job.tf`).
The selection among them is `var.model_secrets_served` and the locals `is_run_model_secret`,
`is_served_model_secret` and `is_service_held`. Shared jobs repeat it in `modules/project`.
Consequence: KS14 and KS15 remove the model half of all of it.

**F10 — Rule: an older image.** A job image built before a release cannot use a route that release
adds, and `internal/cli/recipes_skew.go` already gates on it (`serviceAuthSince`,
`providerEntrySince`, `keyCredentialSince`). Consequence: A4 and R5, both corrected by F25.

**F11 — Rule: a run in flight.** An execution runs the job template it started with
(`docs/design/update-in-flight.md`), so a dropped mount reaches the next execution, never a
running one. Whether a running execution survives its job being replaced is UNVERIFIED there.
Consequence: R6, Check 780.

**F12 — Rule: pinned sentences.** `SECURITY.md`, `docs/multi-model.md` and the credentials runbook
state, under test, that a provider key the job mounts stays its own
(`TestSecurityMdStatesTheCredentialsService`, `TestSecurityMdNamesTheCredentialsServicesKnownLimits`,
`TestDocsMultiModelDocumentsTheCascadeSwitch`, `TestDocsCredentialsRunbookSaysWhatTheLaunchWires`,
`TestSecurityMdStatesTheMixedRunResidual`). The Terraform tests hold the three arms
(`TestServiceHeldSecretsMatchTheCredentialsService`, `TestModelSecretsServedValidationMatchesGo`,
`TestSecretReadersDocMatchesTheTable`). Consequence: the task that makes a sentence false changes
it and its test.

**F13 — Rule: Check 50 is not a script and has not been run.** It is inline shell in
`docs/gcp-live-checklist.md`, marked not run, and it exercises the runner's gateway route, not the
service. Consequence: KS17 writes it as a script, and D3000 keeps only Claude-through-a-provider
behind it.

**F14 — Rule: another plan edits the same files.** The workspace-service plan rewrites
`internal/credsvc/cascade.go` and `forward.go` (its WS1, WS8) and `internal/gateway/service.go`
(its WS11, WS12). Consequence: R8.

**F15 — Rule: D1634's second reason goes away.** D1634 kept provider keys on the runner path partly
so that a pool's secrets would not all sit in one job (its F20). Served by the service, none sits
in any job. A pool's failover between provider members stays at a stage boundary, as today; this
work does not change it.

**F16 — Rule (review round 1).** `SECURITY.md` treats every request as hand-built: the agent holds
the run's identity and can mint the job's token, so it can call the service without the gateway.
The service's own check (`requestRefusal`, `internal/credsvc/request.go`) runs
`gateway.RequestRefusal` with no routes, so the route rules cannot fire there, and an honest
routed model would be refused as unpinned. Consequence: D3003, and KS4 builds the routes from the
published policy.

**F17 — Wrong (review round 1), corrects F4.** As F4 now says. `internal/gateway/forward.go` also
holds `upstreamRequest`'s provider arm and every branch a service call takes (refusals, the
unreachable halt, retry), so KS5 edits it.

**F18 — Gap (review round 1).** A budget-off run has no identity: `startBudgetBackend` returns
before it opens a session, `serviceUpstream` refuses with no run identity, and the launcher mints
no token when the installation has no database (`internal/cli/launch_budget.go`; F37 corrects
the rest of this sentence). The service also refuses a project that publishes no
`budget.allowed_models`. Consequence: D3004 at first, withdrawn by D3009.

**F19 — Rule (review round 1).** `CredentialsServiceServes` calls `ready` "the condition that
makes the switch safe", and `credentialsBundleServes` (`internal/cli/init_credentials.go`) judges
it. Consequence: R10 keeps it, and it comes to require each named provider entry.

**F20 — Wrong (review round 1), corrects R7.** `internal/infra/tf/retire.go` lists
`google_secret_manager_secret` among its retained types, on the owner's ruling of 2026-11-06 that
fugaro never deletes a secret or a secret version. `prevent_destroy` is a literal on the whole
`for_each` resource, and the installation module unions every labelled live container back in.
The retirement table cannot remove one. Consequence: D3005.

**F21 — Settles U1 (review round 1).** Operators hold `secretVersionAdder` and `viewer` on the
containers and never read a value; the bundle has one grant, the service's. Only a project owner
can read a container's value or write the bundle. Consequence: D3006.

**F22 — Rule (review round 1).** The bundle decodes strictly, and a deployed service older than a
new entry kind would refuse the whole document. The writes are already guarded for that
(`bundleReaderRequirement`, `refuseAnEntryTheServiceCannotRead` in `internal/cli/creds.go`, with
the since-constants of `recipes_skew.go`). Consequence: every writer of a named provider entry
uses the guard, and the copy runs after the stage that deploys the service.

**F23 — Gap (review round 1).** The mixed stage account is more than a runner change:
`narrowToRepoAuth` (`internal/credsvc/cascade.go`) keeps the paid tier only for an `api-key`
record and refuses an account declared beside it; the gateway's `Upstream` carries one auth mode
and one account, not one per stage; and `agent.BuildEnv` hands the stage's token to the agent
outside the gateway. Consequence: two tasks, KS9 and KS10 — withdrawn on 2026-11-20, F48: the stage account is the
access of that stage's craft, which credentials belong to models' CM6 and CM9 serve.

**F24 — Gap (review round 1).** `config.CredentialServingPath`
(`internal/config/stage_accounts.go`) still answers `runner` for `openrouter`, and it is read by
the accounts commands, the craft and pool code and `infra/spec.go`. Consequence: KS11 flips it and
moves its readers, in release B (F29). A pool's failover between provider members stays at a stage boundary, because
the gateway names one provider per route (R1).

**F25 — Wrong (review round 1), corrects A4 and R5.** The fact "this job's image is at or after a
release" exists per workflow, in the build record's base (`checkImageSince`), and it is read at
launch, not when the mounts are computed. A catalog image is rebuilt by `fugaro image build`, not
by `fugaro update`. The earlier flips used release separation and a launch gate. Consequence: A4
and R5 as amended.

**F26 — Rule (review round 1).** `internal/gateway` cannot import `internal/credsvc`. Consequence:
the wire's names live in a gateway file the contract owns.

**F27 — Gap (review round 1).** A provider key has more writers and more homes than F3 named:
`fugaro secrets set` of a provider's secret (`internal/cli/secrets.go`, `initsecrets.go`) writes a
container; a provider key is declared in the owner's local config and can come from the
repository's container, a project-wide one or `credential-<name>-key` (`runnerKeyCredential`); and
a shared project job can mount one as a profile secret (`projectSecretEnv`). Consequence: KS2
covers every writer, R2 every source, KS16 the shared job's.

**F28 — Rule (review round 1).** More plans share these files than F14 named: workspace service's
WS11 writes `internal/runner/gateway.go` and `SECURITY.md` and its WS1 `credsvc/server.go`;
secrets brokering's B4.9 and B5.7 write `internal/config/providers.go`, the rules file,
`secret-readers.md`, `deploy/terraform/terraform_test.go` and `modules/project`, and B5.7 itself
drops the shared job's mounted token. Consequence: R8 as amended, and KS16 is what is left after
B5.7.

**F29 — Wrong (review round 2), corrects F24.** `runnerKeyCredentialName` (`internal/infra/spec.go`)
computes a job's provider mounts from `config.CredentialServingPath`. Flipping the path in release
A would drop the mounts that release A still relies on. The path is also read by
`internal/projectpolicy/migrate.go`, `internal/credsvc/bundle.go`, `internal/cli/creds.go`,
`accounts_add.go` and `recipes_skew.go`. Consequence: KS11 moves to release B, after KS13, and
names every reader.

**F30 — Wrong (review round 2), corrects R10.** Requiring the named provider entries in `ready`
during release A would turn the switch off for a repository already switched whose bundle lacks
one. A `cascade` repository has no mount to return to, so its runs would stop. Consequence: R12
keeps release A's `ready` as it is and adds a fact; `ready` widens in KS13.

**F31 — Gap (review round 2).** R12 said "a run whose image and bundle are ready" and named nothing
that carries that to a run. Today's switch is read by the owner's own `fugaro update`
(`credentialsBundleServes` reads the bundle's entry names), recorded on the repository's entry in
the local config (`CredentialsServiceReady`), and planned into the job from there. Consequence:
the fact takes the same road (C1 names it, KS19 records and plans it, KS5 reads it).

**F32 — Gap (review round 2).** `narrowToRepoAuth` (`internal/credsvc/cascade.go`) keeps the paid
tier only for an `api-key` record, so an `oauth` repository is never offered a provider credential,
whatever KS6 admits. `requestNoAllowListRefusal` (`internal/credsvc/request.go`) refuses every call
of a project that publishes no `budget.allowed_models`. Consequence: KS3 offers a named provider
credential wherever the repository's access record names it (that clause is credentials belong to
models' CM6, F51); the fact is true only where the project publishes an
allow-list; the refusal of a craft whose credential is not served is CM15's (KS6 is withdrawn,
F48).

**F33 — Wrong (review round 2), corrects R2.** The bundle loads `credential-<name>-key` as a tier B
Anthropic key. Entering a provider's key under that name would change what the field means for the
deployed service. Consequence: a new entry family for provider keys, named by C1.

**F34 — Gap (review round 2).** The copy said nothing about a repository's own Anthropic key or
OAuth token still held in a container. Consequence: D3008.

**F35 — Wrong (review round 2), corrects R7.** The container resource is a `for_each` over a
variable, so removing one needs no change to a `.tf` file. Terraform's state also holds two
operator grants per operator on each container, and a grant left in state fails the next apply
once its container is gone. `internal/infra/discover.go` unions every labelled live container back
into the variable, and `docs/config-transitions/spec.md` describes the retirement under test.
Consequence: R7's order, and KS15's files.

**F36 — Gap (review round 2).** "A project owner" had no test in the plan. The repository has one
for another purpose: `testIamPermissions` (`internal/cli/doctor_launcher.go`,
`operator_write.go`), and `credsOwnerOnly` (`internal/cli/creds.go`) already refuses a bundle write
three ways. Consequence: KS2 writes the one probe, and KS12 and KS15 use it.

**F37 — Wrong (review round 2), corrects F18.** `budgetMintOn` (`internal/cli/launch_budget.go`)
mints a token with the mode off when the installation has a database and a policy is published.
Only an installation with no database never mints.

**F38 — Rule (the census taken for D3009).** Budget mode off is today's default and not an
exception. `--budget-mode` defaults to unset, a setup with no `--firebase-project` builds no budget
database, and the runner reads unset as off (the local config's `BudgetMode` method, and
`spendFromEnv` in `internal/runner/spend.go`), though the
settings registry says unset means observe. About 32 Go files outside tests read the mode off,
and `schemas/fugaro.schema.json` and `schemas/result.schema.json` list it. With a gateway running,
a model with no price is refused at bootstrap, a provider model needs `enforce`, Vertex with
`enforce` is refused, and the Cloud Batch backend refuses every run that has an identity
(`internal/backend/batch/run.go`). Consequence: removing the mode is larger than this plan's
subject. D3010 and D3011, and R13.

**F39 — Gap (review round 2): files and filters the plan missed.** KS14 also needs
`modules/repo/workflows.tf`, `modules/repo/variables.tf`, `roots/repo/variables.tf` and the
constants in `internal/config/providers.go`. KS10 also needed `internal/runner/agentenv.go`. KS6
also needed `RoutingInertReason` (`internal/runner/routing.go`, `internal/cli/validate.go`) and the
tests that pin `CheckProviderAuth` — both withdrawn on 2026-11-20 (F48), and those files are
credentials belong to models' CM9 and CM16 now. The refusal table's tests are `TestRefusalMatrix*`,
and no
gateway test matched `ProviderWire`. Consequence: each task as amended.

**F40 — Wrong (review round 3), corrects D3000's consequence.** A Claude model routed through a
provider works today on the mounted key: `doctor` only warns that its cost is unmeasured
(`routingCacheMarkersUnmeasured`, `internal/cli/doctor.go`), and the one Check 50 refusal is the
service's (`creds.checkCandidates`). Sending routed calls through the service would stop those
runs in release A. Consequence: D3013, KS20.

**F41 — Wrong (review round 3), corrects R10 and R12.** `ready` asked for less than R12's fact, so
a mount could go while the gateway still fell back to it (a project with no allow-list). And a
repository that never has a model mount, a Vertex one for instance, is never "ready", so D3012's
stop as first written would stop every update. Consequence: KS13 and KS14 as amended.

**F42 — Gap (review round 3).** With one Anthropic entry per repository, `narrowToRepoAuth` keeps
every paid-tier candidate for an `api-key` repository, so one repository could be served
another's key. Consequence: KS3 scopes it. An OAuth token belongs to an account that repositories
share on purpose, and the bundle enters it by account name. Consequence: D3015.

**F43 — Rule (review round 3).** Operators keep the right to add a version to a key's container
until the container is deleted, and the copy follows a newer version. Consequence: D3014.

**F44 — Gap (review round 3).** KS10 took the stage token away wherever the repository was ready,
with nothing saying the deployed service could serve a stage account. Consequence: a second fact
in C1, recorded by KS19. **Both are withdrawn** (F48, F50): one fact per repository covers a
stage's account too, so the second fact is not needed.

**F45 — Rule (review round 3).** The service takes a provider's address from the bundle entry
(`providerRoute`). Nothing said it must never come from the call. Consequence: C1's rule and
KS4's test.

**F46 — Rule (review round 3).** `internal/credsvc/dynamicsecrets.go` states, as a rule of secrets
brokering, that no value is ever copied from one store to the other. The copy of D3001 is the
owner's exception to it. Consequence: KS12 amends the comment, and the independent security
review of KS12 is asked to confirm the exception (O2).

**F47 — Gap (review round 3): files and steps.** `internal/runner/poolchoice.go` reads a pool
member's availability from the key in the environment (KS5). `CheckProviderAuth` is also called
from `internal/cli/models_use.go` and `ee/ab/admit.go` and pinned by two documents tests (KS6 —
withdrawn, F48; those files are credentials belong to models' CM16's now).
The mounts' variable also lives in the repository root's tests and fixtures and in
`deploy/terraform/proxied_git_test.go` (KS14). `scripts/live_checks_test.go` lists every check
script (KS17). The update's in-flight lines are counted under test (KS13, KS15). A deletion after
a state removal passes no plan check, so it needs its own audit line (KS15). The review path's
single `provider-key` entry (`reviewOfferings`) is left as it is.

### Amended by credentials belong to models, 2026-11-20

**F48 — Rule: KS6, KS9 and KS10 are withdrawn.** The owner's ruling D3007, read in full, retires
`agent.auth` as a repository's setting (O4), and the plan that carries that out —
[credentials belong to models](../credentials-belong-to-models/README.md) — replaces what these
three planned, because a task that admits a provider pin "under every auth the service serves", or
serves "a stage account beside the main credential", has no auth mode left to work in (its F19,
F20, D3068). **KS6 is replaced by that plan's CM16 and CM15**: CM16 judges a model by what reaches
it, replacing `CheckProviderAuth`, and CM15 refuses at launch a craft whose credential is not
served, which is the refusal KS6 planned. **KS9 is replaced by CM6**, which narrows every call by
the repository's access record rather than by its auth. **KS10 is replaced by CM9**, which takes a
subscription token out of the agent's environment for every subscription, not only a stage's. The
owner stopped all three on 2026-11-20 (that plan's O11); their numbers are not reused, as KS7's and
KS8's are not.

**F49 — Rule: KS13 waits on that plan's I1.** KS13's "Depends on" named KS6 and KS10, and KS10
named KS6 and KS9; withdrawn with nothing put in their place, KS13 would never become ready, and
that plan's release 2, which waits on KS13, with it (that plan's F37). So KS13 waits on
credentials belong to models' I1, its release 1 integration, where those tasks' work is in the
tree (its F50). The wait is outside this plan's graph, like the release before KS13: the plan's §5
names it beside the other outside waits. *Amended 2026-11-22 (D3170):* credentials belong to
models is itself superseded in part by
[model credentials routing](../model-credentials-routing/README.md), which withdraws its I1 with
every other task of it that is not merged, so the rule stands and the wait moves — **KS13 waits on
model credentials routing's I1**, that plan's release 1 integration, and no longer on a task that
can never merge (its A1).

**F50 — Rule: C1's second fact is not needed.** C1 named R12's two facts, "provider keys are
served" and "the stage account is served" (F44, U6). With a stage's account the access of that
stage's craft (D3068), one fact per repository, "its credentials are served", covers both, and
credentials belong to models' C1 declares the one (its F20). So C1's second fact, and the KS19
half that recorded it, are not needed.

**F51 — Rule: KS3's "under every auth" clause is that plan's CM6.** KS3 planned to offer a
provider credential "under every auth the service serves and not only `api-key`" (F32). That
clause is the service's narrowing, which credentials belong to models' CM6 owns (its F21); KS3
keeps the narrowed Check 50 gate and the per-repository Anthropic entry. KS3, KS13 and KS19 are
amended by that plan, not replaced (its F21).

## 4. Decisions

All answered by the owner on 2026-11-19, D3012 to D3015 on 2026-11-20.

| # | Question | Answer | Mark |
| --- | --- | --- | --- |
| **D3000** | Which provider calls wait for Check 50 (F5)? | Only a Claude model through a provider key. A non-Claude provider model is served by the service without waiting | as recommended. **It replaces the owner's answer of earlier the same day** ("run Check 50 first" for every provider key), given on the first reading of the gate; the two facts of F5 were found afterwards |
| **D3001** | Where do model keys live at the end, and how do they get there (F8)? | In the project's one bundle. `fugaro update` copies each existing key there itself, in memory on the owner's machine, and then retires the per-repository containers | as recommended |
| **D3002** | What happens to budget mode off (F7)? | The gateway always runs: with the budget off it is a pass-through to the service, with no budget enforced and no key in the job | as recommended; **withdrawn by D3009** |

| **D3003** | A hand-built call can reach the service without the gateway, where the cap and the fee are applied (F16). Acceptable? | Yes. The service enforces the policy's model and provider rules on every call; the cap stays in the gateway, and `SECURITY.md` states the limit | added by review round 1; as recommended |
| **D3004** | A budget-off run has no identity (F18). What then? | It opens its identity like any other run. An installation with no database cannot, keeps its mounted keys, and `fugaro update` says so | added by review round 1; as recommended; **withdrawn by D3009** |
| **D3005** | What happens to an old key secret once its value is in the bundle (F20)? | **`fugaro update` deletes it.** | added by review round 1; **not the recommendation** (which was to drop it from Terraform and keep it). **It reverses the owner's ruling of 2026-11-06 for this class of secret** |
| **D3006** | Who runs the copy, and who sets a provider key afterwards (F21)? | A project owner only. An operator's `fugaro update` skips the copy, keeps the mounts and says the owner must run it. Operators still never read a value | added by review round 1; as recommended |
| **D3007** | May a repository on another auth mode pin a provider model? | **The owner's own answer:** "There shouldn't be any repo tie-in to keys. Api-keys, oauth tokens etc are there for the models only which should use the most appropriate way to authenticate." | added by review round 1; **not either option** |
| **D3008** | How are a repository's own Anthropic key and OAuth token, and a key several repositories share, entered in the bundle (F34)? | One entry per repository. A key shared today is copied once for each repository that uses it | added by review round 2; as recommended |
| **D3009** | A budget-off run held on its mounts keeps a key in the agent's environment. Acceptable? | **The owner's own answer:** "I don't think we should have this no-budget case", and then "Yes let's remove it. We should always have a budget. If anyone doesn't care, they can set a high number." Budget mode off is removed | added by review round 2; **not either option; it withdraws D3002 and D3004** |
| **D3010** | With off gone, does `observe` stay (F38)? | Yes. Two modes remain, and `fugaro update` rewrites off to `observe`, so no run starts being refused by a cap nobody chose | added by review round 2; as recommended |
| **D3011** | Where does the removal of off live (F38)? | In a spec of its own, "always a budget", written next. This plan drops its budget-off tasks | added by review round 2; as recommended |
| **D3012** | KS14 removes the Terraform that can mount a key. What does an update do where the owner's copy never ran, given D3006? | The update stops before the Terraform step, changes nothing, and says a project owner must run `fugaro update` once. Every repository keeps running on its mounts | added by review round 2; as recommended |
| **D3013** | A Claude model through a provider works on the mount today and would stop in release A (F40). What then? | **The owner runs Check 50 before release A is cut**, and its result decides the gate | added by review round 3; **not the recommendation** (which was to serve it with today's warning) |
| **D3014** | An operator can still add a version to a key's container, and the copy follows it (F43). Which wins until the containers are deleted? | The copy follows the newer version and prints one line naming the container. Owner-only becomes fully true when the containers are deleted | added by review round 3; as recommended |
| **D3015** | Does D3008 reach an account's OAuth token (F42)? | No. A token stays entered once under its account's name; D3008 applies to Anthropic and provider keys | added by review round 3; as recommended; **it narrows D3008** |

**What D3007 means here, and what it leaves open.** In this plan: a provider model may be pinned
under every auth the service serves, and no check ties a provider key to a repository's auth mode
(KS6, withdrawn on 2026-11-20 — F48: credentials belong to models' CM16 and CM15 replace it). The answer is wider than that: read in full it says a repository should not choose an auth
mode at all, and each model should authenticate the way that suits it. Retiring `agent.auth` as a
repository's setting is a config change of its own, with its own readers, and is **not** in this
plan. It is O4, for the owner.

**Reversed by the owner's ruling D2975:** D1634 (provider members on the runner path) and D1670 (an
`openrouter` key goes to the job's own mounts). D498's Vertex is untouched (A2).

## 5. Resolutions

- **R1. The gateway keeps the provider's work; the service holds the key.** Routing by model, the
  fee, the price check, the settle on the provider's reported cost, `RequireZDR`, `PricePolicy`,
  `allow_data_to` and the per-route ledger stay in the run's gateway, unchanged. What changes is
  the last hop: a routed call goes to the service's provider route, naming the provider credential
  it is for, and the service attaches that key and forwards to the provider's own address. The
  service checks the name against the member's published policy for the calling repository before
  it attaches anything, so a run cannot name a credential its policy does not give it. Contract C1.
- **R2. One entry per repository and provider credential, and the caller never names an entry.**
  *(Amended by review rounds 1 and 2: F33, D3008.)* Every provider key is entered under a name of
  a new family that carries the repository and the provider credential's name, whichever of its
  three sources it came from (F27). No provider key is entered as `credential-<name>-key`, which
  the deployed service reads as an Anthropic key. A key several repositories share is entered once
  for each. The wire carries the provider's name only. The service derives the
  entry from the run's verified repository, so a run cannot ask for another repository's key.
- **R3. The gate, narrowed.** The service refuses a provider credential for a Claude model until
  Check 50 passes, with the refusal that names the check, and serves it for any other model
  (D3000). Which models are Claude's is the existing pin vocabulary, not a new list.
- **R4. The copy.** *(Amended by review round 1.)* `fugaro update` gains one stage, after the stage
  that deploys the service, that for each model key the installation holds outside the bundle
  reads the newest version and writes it into the bundle, through the existing guard for an older
  service (F22). A provider key goes under R2's name. A repository's own Anthropic key goes under
  an entry that carries the repository, which the service derives from the run's verified
  repository as it does a provider's (D3008). An account's OAuth token goes under the account's
  name, as today (D3015). Each entry the copy writes carries the container and version it came
  from; when a container holds a newer one, the copy follows it and names it in one line (D3014). It holds the value only in memory and prints none of it. It is
  idempotent; it copies again when a container holds a newer version than the one it copied; it
  never overwrites a bundle entry it did not write without saying so. It runs only for a project
  owner: an operator's update skips it and says who must run it (D3006). It asks nothing.
- **R5. The mounts go in a second release, behind a launch gate.** *(Amended by review round 1,
  F25.)* The release that drops the mounts refuses to launch a run on a job image older than the
  release that served the keys, with the command that rebuilds it. A repository is never left with
  neither a mount nor a service that serves its image.
- **R6. Runs in flight.** A running execution keeps its template and so its mounts. `fugaro update`
  names, in one short line each, the two steps that can reach a run: none for the copy, and the
  deletion of an old secret, which happens only when no job references it.
- **R7. An old secret is deleted by the update, once copied and unmounted.** *(Amended by review
  rounds 1 and 2: D3005, F35.)* The retirement table cannot do it (F20). In this order: the apply
  that leaves no job mounting the container; then the container and its operator grants leave
  Terraform's state; then the container is deleted through the API. It happens in the retirement
  code (`internal/infra/retire.go`, `internal/cli/init_retire.go`), with discovery taught not to
  bring a deleted container back, and only when the container's newest value is in the bundle, no
  job mounts it and the person running the update is a project owner (F36). Every
  other secret stays under the ruling of 2026-11-06.
- **R8. Several plans, one set of files.** *(Amended by review round 1, F28.)* A task of this plan
  is not launched while a task of workspace service or secrets brokering that edits the same file
  is live, and whichever lands second rebases. The files are `internal/credsvc/cascade.go`,
  `forward.go` and `server.go`, `internal/gateway/service.go` and `gateway.go`,
  `internal/cli/creds.go`,
  `internal/config/providers.go`, `SECURITY.md`, the rules file, `secret-readers.md`,
  `deploy/terraform/terraform_test.go` and `modules/project`. No plan waits for another as a whole.
- **R9. The stage account goes through the service.** The mixed variant's second account is
  selected by the service like any named account, through the gateway, so the runner holds no
  token for it (F6). *(Amended 2026-11-20: KS9 and KS10, which carried this, are withdrawn
  (F48); credentials belong to models' CM6 and CM9 carry it, for every subscription rather than a
  stage's.)*
- **R10. One switch for every auth but Vertex.** *(Amended by review rounds 1 and 2: F19, F30,
  D3009; round 3: F41; 2026-11-20: F50.)* In release B (KS13) `ready` comes to mean R12's fact in
  full, and a ready repository mounts no model key.
  `CredentialsServiceServes` keeps its budget-mode argument in this plan; the plan that removes
  the mode removes the argument (R13).
- **R11. The service checks the route itself.** The service builds the provider routes from the
  published policy and refuses a call the policy's model and provider rules do not allow, whoever
  built the request (D3003, F16).
- **R12. The gateway uses the service when it can and the mount when it must.** *(Amended by
  review round 2: F30, F31, F32.)* Release A adds one recorded fact per repository, "its provider
  keys are served": the bundle holds an entry for every provider credential the repository's
  config names, the deployed service can read that entry kind, and the project publishes an
  allow-list. The owner's `fugaro update` reads it and records it beside today's switch, and the
  job is planned from the record, as the switch is. A run whose job carries the fact sends a routed
  call through the service; any other run uses its mounted key as today. Release A's `ready` does
  not change, so no switched repository is turned off. A launcher that sets the fact falsely gains
  nothing: the service still refuses a credential it does not hold for that repository. *(Amended
  2026-11-20, F50: the second fact of the same kind, "the stage account is served", which gated
  KS10 (F44), is not needed — one fact per repository, "its credentials are served", covers a
  stage's account, which is that stage's craft's access.)*
- **R13. A budget-off repository waits for the other plan.** *(Review round 2: D3009, D3011.)*
  Until "always a budget" lands, a repository in budget mode off, and an installation with no
  budget database, keep their mounted keys through both releases, as today, and
  `fugaro update` says so in one line. KS14 and KS15, which take the mounts' Terraform and the old
  secrets away, are not launched before that plan's removal has merged. The census of F38 is that
  spec's starting material: the database becomes mandatory, unset stops meaning off, an unpriced
  model and the Cloud Batch backend need an answer, and `update` rewrites off to `observe`
  (D3010).

## 6. Open tensions and open items

One tension was found and settled: D3006 (an operator's update keeps the mounts) against KS14
(the Terraform that mounts goes). D3012 answers it.

- **O1.** D3000 replaces an answer the owner gave earlier the same day. It is recorded in
  [workspace credentials](../workspace-credentials/spec.md) as "D150's gate stays"; KS18 amends that
  sentence to the narrowed gate.
- **O2.** D3001 has `fugaro update` read secret values on the owner's machine. The precedent is the
  doctor's token check, which reads the bundle in memory in the owner's terminal. It is named here
  for the independent security review of KS12. The precedent is stronger than first written:
  `fugaro creds set` itself reads and rewrites the bundle on the owner's machine.
- **O3.** Shared project jobs lose their mounted token when secrets brokering's B5.7 lands, which
  is not started. KS16 does what is left after it. Until then "no model key is mounted" is true of
  every job but those.

- **O4.** D3007 read in full retires `agent.auth` as a repository's setting. This plan does only
  the part that touches provider keys. **Answered by the owner on 2026-11-20** (D3026 of
  [always a budget](../always-a-budget/reconciliation.md), in his own words: "The entire notion of
  "key repository" or "oauth repository" is stupid and should die."): the rest is a spec of its
  own, not written yet.
- **O5.** Which models are Claude's is decided by pattern, so a provider's own alias for a Claude
  model would pass R3's gate. The exposure is cost (an uncached Claude call), not access.
- **O6.** After this work only a project owner can set or rotate a provider key; an operator could
  before (D3006).
- **O7.** A hand-built call escapes the cap for a provider model (D3003), stated in `SECURITY.md`.
- **O8.** D3008 multiplies the bundle's entries by the repositories that share a key, and rotating
  a shared key becomes one write per repository. `fugaro creds set` takes the repositories to
  write in one command (KS2), and KS1's worst case is counted that way (U5).
- **O10.** D3013 puts an owner-run live check in front of release A. Until Check 50 is run and KS20
  merged, release A cannot be cut, whatever else is ready.
- **O11.** Review round 3's fixes are the last commit and were not reviewed again; the skill's cap
  is three rounds.
- **O12.** The graph cannot say that KS1 and KS19 wait for C1 (they build its other sides). A
  launcher follows the plan's levels table.
- **O9.** KS14 and KS15 wait on a plan that does not exist yet (R13). If "always a budget" is not
  written or not approved, release B stops after KS13 and KS11: no ready repository mounts a model
  key, and the Terraform that could mount one stays.
- **O13.** KS13 waits on credentials belong to models' I1, which is that plan's release 1
  integration (F49). The wait is outside this graph, which names only this plan's nodes, so a
  launcher follows the plan's levels table and its outside-waits list; the same holds for KS14's
  and KS16's waits (O9, O3). *Amended 2026-11-22 (D3170):* that plan's I1 is withdrawn with every
  other task of it that is not merged, so **KS13 waits on model credentials routing's I1**, and the
  levels table and outside-waits list below name that plan.

## 7. UNVERIFIED

- **U1.** *Settled by review round 1 (F21):* only a project owner can read a container's value.
  Check 780 confirms it live.
- **U2.** How a repository's own provider secret is declared against a project-wide credential of
  the same name, and so the exact form of R2's repository-carrying name. Settled by contract C1.
- **U3.** Whether a running execution survives its job's template being replaced
  (`docs/design/update-in-flight.md` marks it unverified). Settled by Check 780, owner-run.
- **U4.** *Settled by review round 1 (F23).*
- **U5.** The bundle's size against Secret Manager's limit for one secret, for an installation with
  many repositories and provider keys (D3008). Settled by KS1's test of a bundle at a stated worst
  case.
- **U6.** The exact shape of the record and of the job's entry that carry R12's fact. The road is
  verified (F31); the names are C1's.
- **U7.** That a container's operator grants are separate addresses in Terraform's state that the
  retirement code can remove by name (F35). Settled by KS15's first test.
