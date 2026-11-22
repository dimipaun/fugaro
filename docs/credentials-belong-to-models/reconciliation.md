# Credentials belong to models, not repositories: reconciliation

Checked against `origin/main` at `d57d71ddb` on 2026-11-20. **Where this file disagrees with
[spec.md](spec.md), this file wins.** It cites paths and symbols, never line numbers. Decisions are
**D3060–D3073**, all answered by the owner on 2026-11-20 (he was asked them as D3040–D3053, before
pull request #3308 took that block; F41), every one as recommended; **D3061 was
amended by the owner the same day, and D3064 was amended after review round 1** with his answer
to the question it raised. Findings F24 to F32 are that round's and F33 to F41 round 2's, which also
rewrote R4 and R15. F42 to F50 are round 3's; **its fixes are the last commit and were not
reviewed again** (O12). It carries out
**D3007** of [every model key through the credentials service](../keys-through-service/reconciliation.md)
(the keys plan, merged as #3299) and **D3026** of always a budget (pull request #3302, read
at `8bf13231f` on branch `docs/always-a-budget` and merged since), and answers the keys plan's O4 and that plan's
R12 and O8. It reverses **F24** of [model crafts](../model-crafts/reconciliation.md) ("`agent.auth`
stays per run and picks the path"), reverses **D1624** (an unset access no longer means Vertex), retires
**O2** of the credentials service (the operator's consent switch) and replaces the keys plan's
tasks **KS6, KS9 and KS10**; each is marked where it bites.

**Amended on 2026-11-22 by [model credentials routing](../model-credentials-routing/reconciliation.md)
(D3170).** That plan's routing table supersedes this plan's craft access, so **every task of this
plan that is not merged is withdrawn**, carried by a task there except CM2 (per-repository crafts),
which is not replaced: one table per project, and that plan's RT2 carries admission. **Contract C1
is withdrawn with its draft pull request #3460 still open, to be closed unmerged**, re-cut as that
plan's C1. What is merged
(CM1, CM3, CM22, CM32, CM35), the two-release removal of `agent.auth` and R3's rule for an
unmigrated repository are kept. The withdrawn set, what carries each, and the decisions the
amendment reverses are in §4's amendment below; the withdrawn tasks are stopped by the owner by
hand before any launches (that plan's O1).

## 1. What the spec asks for, and what the repository already has

| The spec's concept | What the repository has |
| --- | --- |
| "A repository's auth mode" | `agent.auth` in a repository's `fugaro.yaml` (`config.Agent.Auth`) and `defaults.agent.auth` in the project layer (`config.LayerAgent.Auth`), one of `config.AgentAuths`: `vertex` (the default, `config.DefaultAgentAuth`), `api-key`, `oauth`, `cascade` |
| "A credential declared once" | Model crafts, 25 of 26 tasks merged: a named, typed credential (`config.Credential`, types `openrouter`, `anthropic`, `claude-oauth` and the built-in `vertex`), declared in the installation's config (`localcfg`'s `credentials`) and published in the policy node (`craft_credentials`) |
| "For the models it reaches" | Not declared anywhere, and derivable: a Claude model is one `config.IsClaudeModel` answers for, and a provider's models are the ones its `providers:` entry claims (`config.ProviderFor`) |
| "A run that uses a model gets whatever credential reaches it" | Half built: each craft has an `access` that names a credential or a pool (`config.Craft`, `craft.Resolve`), but the run's path is still picked by `agent.auth` underneath it (F2) |
| "The order of credentials" | Two things: a pool's listed order (`config.Pool`), and the credentials service's cascade, which orders subscriptions before the paid key (`internal/creds`, `internal/credsvc/cascade.go`) |
| "Every run goes through its gateway" | False for a subscription the service does not serve: `runner.GatewayOn` never answers true for `oauth` (F4) |
| "A cloud identity such as Vertex's" | The built-in credential `vertex` (`config.CredentialVertex`, D1624): no secret, the job's own service account, reached through the run's gateway |

## 2. Assumptions

- **A1.** "Credential" means a model credential: a Claude subscription token, an Anthropic key, a
  provider key and Vertex's identity. Git tokens, the GitHub App key, workflow secrets and signing
  keys are not in scope.
- **A2.** The spec's four kinds are the four credential types the code already ships
  (`config.CredentialTypeClaudeOAuth`, `CredentialTypeAnthropic`, `CredentialTypeOpenRouter`,
  `CredentialTypeVertex`). No new type is added; `openai` and `codex-oauth` stay refused (D1623).
- **A3.** "Declared once" means once per project, by the project's owner or operator, in the
  project's own config. A person's own token at the workspace is out of scope, as the spec says.
- **A4.** A model is still chosen where it is chosen today (a repository's file, the project
  layer, a task). Only the credential stops being a repository's choice.
- **A5.** The review class (a review job's own model path, `runner`'s `reviewClassRun`) keeps its
  own credential rule, the pairing in `internal/credsvc`. It is spelled `cascade` inside the
  runner today (`modelAuth`) and loses that spelling with every other reader; what it is served
  by does not change.

## 3. Findings

### The census

**F1 — Rule: 67 Go files outside tests, in 14 packages, read the auth mode or a value worked out
from it.** By package, with the readers that decide something:

| Package | Readers |
| --- | --- |
| `internal/config` | The field and its vocabulary (`Agent.Auth`, `LayerAgent.Auth`, `AgentAuths`, `KnownAgentAuth`, `DefaultAgentAuth`, `AuthCascade`, `AuthAPIKey`, `AuthOAuth`, `ServiceServedAuths`, the `agent.auth` row of `Scopes`); `applyDefaults`, `AgentOf`, `Validate`, `validateLayer`, `validateCascadeFollowup`, `CheckCascadeGateway`, `CheckProviderAuth`, `AnthropicKeyNeeded`, `CheckStageAccountMix`, `CascadeSkippedStageAccounts`, `CheckMixBudget` |
| `internal/runner` | `GatewayOn`, `ServiceSwitchedAuth`, `RoutingInertReason`, `applyRouting`, `checkReviewClassModelPath` (`routing.go`); `modelAuth`, `servedAuth`, `serviceSwitched`, `gatewayUpstream`, `serviceUpstream`, `checkBudget`, `checkProviderModels`, `cascadeLocalRefusal`, `VertexBudgetRefusal` (`gateway.go`); `subscriptionUsageLimit`, `bootstrap` (`runner.go`); `accountWalkApplies` (`accountchoice.go`); `checkProjectJobAccount` (`projectjob.go`); `resolveMixedStages`, `writeManagedSettings` (`agentenv.go`); `registryEntry` (`budget_session.go`); `stageTiming`, `updateCost` (`cost.go`); `stepTiming` (`stephook.go`); `triageUnserved` (`triage_stage.go`); `messages` (`directcall.go`); `poolMemberHeld` (`poolchoice.go`) |
| `internal/agent` | `EnvSpec.Auth`, `BuildEnv`, `GatewayVars` (`env.go`). `OAuthStageEnv` takes a token and no auth |
| `internal/infra` | `CredentialsServiceServes`, `ModelSecretsServed`, `SecretMounts`, `WorkflowAccounts`, `mixedStageAccountJob`, and `WorkflowSpec.Vertex` (`spec.go`) |
| `internal/cli` | Launch: `checkoutAgentAuth`, `repoServiceAuth`, `claudeAccountChoice`, `claudeAccountAllowed`, `mixedRunAccount`, `mixedStageAccount`, `stageAccountOf`, `credentialsServiceLaunchEnv`, `cascadeLaunchRefusal` (`run.go`); `attachRouteFor` (`run_attach.go`); `switchedServiceAuth`, `serviceAuthGate`, the three budget prechecks (`launch_budget.go`); `projectJobAccountFor` (`project_job.go`). Update: `gateCascadeAuth`, `checkVertexBudget` (`init.go`); `credentialsBundleServes`, `recordCredentialsServiceReady` (`init_credentials.go`); `repoAuthRecord`, `writeRepoAuth` (`update_repoauth.go`); `refuseCascadeSkew` (`recipes_skew.go`); `resolve` (`initsecrets.go`). Reading: `budgetProblems`, `providerProblems`, `cascadeProblems`, `stageAccountProblems`, `routingProblems`, `craftModelProblems` (`validate.go`); `authOf`, `oauthTokenAllowedCheck` (`doctor.go`); `flipServes`, `flipNotServed`, `doctorModelKeyInAgent` (`doctor_brokering.go`); `craftLsServes` (`craft.go`); `modelsUseModelCheck` (`models_use.go`); `refuseModeOffWhileTheServiceServes` (`project_cmd.go`); `jobConfigCopyCredentialsNote` (`upgrade_cloud.go`); `showRun.Auth` (`budget.go`) |
| `internal/creds` | The record `RepoAuth` and `PutRepoAuth`, `ReadRepoAuth` (`repoauth.go`) |
| `internal/credsvc` | `narrowToRepoAuth`, `candidates` (`cascade.go`); `HeaderAuthMode`, `ValidAuthMode`, `RequestFromHTTP` (`wire.go`); `Identity.AuthMode` (`auth.go`) |
| `internal/gateway` | `Upstream.AuthMode` (`gateway.go`); `validateRoutes` (`route.go`); `HeaderAuthMode` (`service.go`) |
| `internal/budget`, `internal/watch` | `AgentEntry.Auth`; `RunRow.Auth`, `JSONRun.Auth` and `Notional` |
| `internal/runstore` | `ModelBasis`, `CredentialForAuth` |
| `internal/settings` | The rows `agent.auth` and `layer.defaults.agent.auth` (`registry.go`) |
| `internal/localcfg` | Two values worked out from it and recorded per repository: `Repo.Vertex` and `Repo.CredentialsServiceReady` |
| `ee/ab` | `launchAgent`, `coderAgent`, `refuseModel` |

Nothing in `cmd/` or `images/` reads it, and no environment variable carries it; two live-check
scripts do (F48).
Consequence: the work is one seam every reader is moved to (contract C1), a task per package or
per file group, and an integration task that proves no reader is left.

**F2 — Exists: the replacement is built, and the auth mode still sits under it.** `craft.Resolve`
(`internal/craft/resolve.go`) answers, per craft, the credential or pool its calls go out on, and
the runner resolves it per stage (`stageCrafts`, `internal/runner/poolchoice.go`). But model crafts
kept the auth mode as what picks the path (its F24), so the two can disagree: a repository whose
runs go out on a subscription, in a project that writes no `access`, is listed by `fugaro craft ls`
with the access `vertex`, the default (`config.DefaultCredential`). Consequence: D3060, D3061.

**F3 — Rule: the wire formats that carry the value.**

| Format | Where | Version |
| --- | --- | --- |
| A repository's `fugaro.yaml` | `agent.auth`, enum of four (`schemas/fugaro.schema.json`) | `version` const 1 |
| The project layer | `defaults.agent.auth`, same enum (`schemas/project-layer.schema.json`) | `version` const 1 |
| The settings document | Rows `agent.auth` and `layer.defaults.agent.auth` (`schemas/settings.schema.json`) | `version` const 1 |
| The repository auth record | `creds/repoauth/<slug>` in the budget database: `{version, auth, accounts}` (`creds.RepoAuth`), written by `fugaro update`, read by the service on every call | `creds.RepoAuthVersion` 1 |
| The call to the credentials service | The headers `X-Fugaro-Auth-Mode` and `X-Fugaro-Auth-Account` | none of its own |
| The live registry entry | `agents/<slug>/<run>`, member `auth` (`budget.AgentEntry`); the database rules validate it as a string where present | none of its own |
| `fugaro watch --json` | `runs[].auth`, always present, `-` when the entry has none (`watch.JSONRun`) | the document's own |
| `fugaro budget show --json` | `auth`, left out when empty (`showRun`) | the document's own |
| The run record and the task | No auth, but two members described by it: `oauth_account` ("this run's agent.auth: oauth workflow", `schemas/result.schema.json`) and the task's `oauth_token` (`schemas/task.schema.json`) | `version` const 1 each |
| The console | Its parser requires `runs[].auth` (`console/src/shared/schema.ts`) and the Live screen shows it | follows `watch --json` |
| Terraform's variables | Never the value: `workflows.*.vertex`, `repo.model_secrets_served` and `workflows.*.stage_account`, each worked out from it in `internal/infra/spec.go` | none |

Not carried: the run record (`schemas/result.schema.json` has no auth; `cost.model_basis`,
`stages[].served_by` and `stages[].credential` are worked out from it), the task
(`schemas/task.schema.json`, which has only `oauth_token`, an account's name), the published policy
node (`projectpolicy.Version` 10; its own comment says it carries no `agent.auth`), and the mobile
app and `shared/` (test fixtures only). Consequence: D3063, D3070, D3072, and R3 to R6.

**F4 — Wrong against the brief's item 4, and verified.** `runner.GatewayOn` answers true only for
`api-key`, `vertex` and `cascade`. `agent.BuildEnv`'s `oauth` arm refuses a gateway ("auth: oauth
never goes through the gateway") and puts `CLAUDE_CODE_OAUTH_TOKEN` in the agent's environment;
`agent.OAuthStageEnv` does the same for a stage's account. A subscription run reaches the gateway
today only where its repository is switched, where the runner spells it `cascade` (`servedAuth`).
Consequence: D3067, CM9, CM28.

**F5 — Rule: the service's boundary is the recorded auth.** `narrowToRepoAuth`
(`internal/credsvc/cascade.go`) narrows what a call is offered to what `creds/repoauth/<slug>`
says: a `cascade` record or none keeps the whole admitted list, `oauth` keeps the one subscription
of the declared account, `api-key` keeps the paid key alone, and any other value is refused. The
record exists because the agent holds the run's identity, so what a call says about itself is not
a boundary. Consequence: the boundary stays and its subject changes: the record comes to name the
credentials a repository's runs may be served by (R4, CM5, CM6).

**F6 — Rule: the switch and the bundle's readiness are judged by auth.**
`infra.CredentialsServiceServes(installed, ready, auth, mode)` answers true for `cascade`, and for
`api-key` and `oauth` under a gateway mode; `credentialsBundleServes`
(`internal/cli/init_credentials.go`) checks the bundle for what that auth needs;
`infra.ModelSecretsServed` turns the auth into `all`, `anthropic` or `none` for Terraform; and
`infra.SecretMounts` and `infra.WorkflowAccounts` choose a job's mounts and its account variants by
it. Consequence: each is asked instead what the repository's crafts resolve to (CM13, CM15).
Terraform's three variables keep their names and meanings, so no `.tf` file changes in this plan.

**F7 — Rule: an older job image reads the key from the checkout.** No environment variable or
task field carries the auth: the runner reads `cfg.Agent.Auth` from the `fugaro.yaml` the run
checks out, and `applyDefaults` gives `vertex` to a file that names none. A job image is rebuilt
behind its own confirmation, so an image of the release behind can run a file of today.
Consequence: deleting the line from a file changes what an older image does, to Vertex. The key
stays readable for one release (D3063), and the release that rejects it gates a launch on the
image's age (D3072, `checkImageSince` in `internal/cli/recipes_skew.go`).

**F8 — Rule: a repository's file names an access today.** `agent.crafts.<craft>.access` is a
repository-only key (`config.Scopes`, with the reason `whyCredentialAccess`); the project's side
of it is the installation config's `crafts` and the node's `crafts`. Consequence: D3062.

**F9 — Gap: the project has no place to say "this repository's crafts".** The installation's
config has one `crafts` block for the project and a `repos.<name>` entry per repository
(`localcfg.Repo`) with no craft in it; the published per-repository node
(`projectpolicy.RepoPolicy`, at `projectpolicy.PathRepo`) holds only the accounts a repository is
admitted to. Consequence: CM2 adds the block to both.

**F10 — Rule: a published member an older image does not know stops its runs.** The node decodes
strictly, and an image of the release behind reads it. The repository has met this before:
`config.AccountStagesAReleasedImageKnows` and `AccountStageHeldBack` hold a new stage name back
from the node for one release. Consequence: CM2 publishes the per-repository block only where
every job image of that repository is at or after the release that reads it, and holds it back,
with one line, elsewhere (U2).

**F11 — Rule: the cascade is an auth mode, a consent switch and an ordering.** As a mode it means
"no credential of the job's own" (`BuildEnv`'s arm, D143). As a switch it is
`routing.claude.via: cascade` in the operator's policy, without which a coding run that asks for
the cascade is refused (`applyRouting`, O2). As an ordering it is the service's own: subscriptions
admitted to the repository first, then the paid key where the policy's `fallback` is `auto`, one
credential sticky per run. A node that publishes crafts already narrows the service's offer to
what the crafts name (`poolOffering`, `internal/credsvc/pool.go`). Consequence: D3064, D3065.

**F12 — Rule: Vertex.** A Vertex call is made by the run's gateway with the job's own service
account (`gatewayUpstream`'s `vertex` kind); nothing is mounted and the service holds nothing.
`VertexBudgetRefusal` refuses it under `enforce`; always a budget's AB1b rewords that refusal and
keeps it. The job's Vertex grant is `workflows.*.vertex`. Consequence: D3066. Vertex under
`enforce` is not this plan's subject.

**F13 — Rule: per-stage accounts are four keys and one special case, all judged by auth.**
`stages.<stage>.claude_account` and `default_claude_account` (the installation's config, published
as `claude_account_stages` and `default_claude_account`), `agent.oauth_token` (a repository's
file) and `--token` on `fugaro run`; `config.CheckStageAccountMix` admits them under `oauth`,
admits exactly one account for the review stages under `api-key` (the mix,
`config.MixedAccountStages`), and refuses them under `vertex` and `cascade`. The craft migration
already turns each named account into a `claude-oauth` credential (`deriveCraftCredentials`,
`internal/cli/init_craft_migrate.go`) and maps a stage to its craft (`stageCraft`). Consequence:
D3068, D3069.

**F14 — Rule: who may launch what is checked by account name.** A run names an account with
`--token`, the launch checks it against `config.AllowedClaudeAccounts`, and the service refuses an
account the record does not name. Consequence: D3069 keeps the flag inside the same list.

**F15 — Rule: what the record and the views work out from the auth.** `runstore.ModelBasis` gives
`subscription` for `oauth` and `api-list` for anything else; `runstore.CredentialForAuth` gives a
stage's `served_by` (`oauth`, `api-key`, or nothing for Vertex and the cascade);
`watch`'s `Notional` is `Auth == "oauth"`; and `subscriptionUsageLimit` and `accountWalkApplies`
turn a usage-limit failure into an account switch only for `oauth`. *(Corrected by review round 1,
F25: the rule each one keeps is stated there.)* Consequence: CM8, CM10, CM11, CM12; no field of
the record changes.

**F16 — Rule: a local invocation.** `fugaro exec --task-file` is the CLI's one local invocation
(`backend.LocalPlatform`); it never reaches the service (D149), and `cascade` is refused there
with "Use agent.auth api-key or oauth locally" (`cascadeLocalRefusal`). Consequence: D3073.

**F17 — Rule: precedents inside `update`.** The local config is converted in place on every write
(`migrateCraftKeys`); the node is migrated by a chain keyed on its version (`policyMigrationFor`,
`projectpolicy`'s `migrateV9ToV10`); a change to a checkout is offered as a diff and written only
on the person's word (`prepareAnchor`, `internal/cli/init_fugaroyaml.go`); and the lines a run in
flight is owed are counted under test (`inFlightSteps`, `internal/cli/update_in_flight.go`,
`TestUpdateLeavesARunInFlightRunning`). Consequence: CM4 and CM19 follow them.

**F18 — Rule: sentences and tests phrased by a repository's auth.** 111 occurrences in 28 files
of "an `oauth` repository", "an `api-key` or `oauth` one" and their like: `SECURITY.md` and the
page generated from it (16 each), `docs/gcp-live-checklist.md` and
`docs/credentials-service/default-on.md` (8 each), `docs/multi-model.md` (4),
`.claude/rules/credentials-service.md` (3), `CLAUDE.md` (1) and plans that are history. Ten
documents test files pin such sentences, among them `docs_security_test.go`,
`docs_credentials_auth_list_test.go`, `docs_mixed_credentials_test.go`,
`docs_usage_limit_switch_test.go` and `docs_credentials_runbook_test.go`. The setup skill asks for
`agent.auth` as its second decision (`plugin/skills/setup/SKILL.md`, `reference/decisions.md`),
pinned by `plugin/setup_skill_test.go`. Dozens of refusals, doctor lines and help texts in
non-test Go name `agent.auth` or "an api-key run". Consequence: the brief's item 3 is four documents tasks (CM24 to CM27) and a clause in every code task: the task that moves a reader
rewrites its messages.

### What this does to the two plans before it

**F19 — Rule: the keys plan's KS6 is replaced.** KS6 makes `config.CheckProviderAuth` admit a
provider pin "under every auth the service serves". With no auth there is nothing to admit under:
the check becomes "a credential that reaches this model is declared and admitted for this
repository" (CM16). KS6 is withdrawn; its refusal of a pin whose keys are not served moves to
CM15.

**F20 — Rule: the keys plan's KS9 and KS10 are replaced.** They serve "a stage account beside the
main credential" through `narrowToRepoAuth` and take the stage's token out of the agent's
environment. Under D3068 a stage's account is the access of that stage's craft, and the service
narrows by the access record (CM6); the token leaves the environment for every subscription, not
only a stage's (CM9). The keys plan's second fact, "the stage account is served" (its C1, KS19),
is not needed: one fact per repository, "its credentials are served", covers it (C1 of this plan).

**F21 — Rule: KS3, KS13 and KS19 are amended, not replaced.** KS3's clause that offers a provider
credential "under every auth the service serves and not only `api-key`" is CM6's; KS3 keeps the
narrowed Check 50 gate and the per-repository Anthropic entry. KS19 and KS13 make
`credentialsBundleServes` require more of the bundle; CM15 changes what it switches on. All three
edit files this plan edits (`internal/credsvc/cascade.go`, `internal/infra/spec.go`,
`internal/cli/init_credentials.go`). Consequence: R9.

**F22 — Rule: always a budget's AB1b and AB15.** AB1b keeps `GatewayOn`'s `oauth` arm and plans a test that pins it
(`TestAnOAuthRunIsUnchanged`); CM8 and CM9 reverse that test once it exists. AB15 takes the budget mode out of
`CredentialsServiceServes`; CM13 then takes the auth out. Consequence: this plan's tasks on those
files start after AB1b and AB15 have merged (D3071, R9).

**F23 — Gap: the settings row cites a document that does not exist.** The registry's `agent.auth`
row names `docs/credentials-service.md`; the folder is `docs/credentials-service/`. Consequence: CM20 points the retired row at this folder.

### Review round 1

**F24 — Wrong (review round 1), corrects D3064's premise.** A pool does not serve what the
cascade serves. `poolOfferingOf` (`internal/credsvc/pool.go`) skips every `claude-oauth` member
while the project's subscription-spread switch is off (`subscriptionSpreadOf`), the runner makes
the same skip from its side (`poolSkipSubscription`), and the switch is off on every installation
and turned on only by the owner (D1622, D1626, D1636). `config.ValidatePublishedPools` refuses a
published pool with no `owner` unless it is `legacy_open`. A cascade turned into a plain pool
would be served its paid key only, or nothing. Consequence: D3064 as amended, CM32.

**F25 — Wrong (review round 1), corrects F15.** The record is not worked out from "the type of
the credential that served". `stageTiming`, `stepTiming` and `recordTriage` call
`runstore.CredentialForAuth(r.servedAuth())`, and `servedAuth` spells every run the service
serves `cascade`, so `served_by` is absent for every such run, as
`schemas/result.schema.json` documents; `ModelBasis` and `Notional` read the repository's own
auth, so they say `subscription` and notional only for `oauth`, and `api-list` for the cascade
even while a subscription serves it; and the usage-limit switch applies to `oauth` alone.
Consequence: each keeps exactly the rule it has, said without the auth word: `served_by` names a
credential the job itself holds and is absent where the service serves; the basis is
`subscription`, the spend notional and the account walk on, where the stage's access is one
subscription credential and not a pool (CM8, CM10, CM11, CM12).

**F26 — Gap (review round 1): a repository `update` has not been run for.** A project holds
several repositories and `fugaro update` migrates the one it is run for. With no reader of the
auth mode left, a second repository launched before its own update would resolve by reach, or be
refused, instead of running as its file says. Consequence: R3 as amended: a repository with no
migrated block is resolved from its file's retired keys by the migration's own rule, read-only,
through release 1.

**F27 — Gap (review round 1): removing the key in release 1 would break older images.**
`repoAuthRecord` writes `cfg.Agent.Auth` into the old record, so once the removal CM19 offers is
committed the old record would say `vertex`, and `narrowToRepoAuth` would refuse every call of an
older image and of a run in flight; that image also reads the file itself as Vertex (F7).
Consequence: CM5 derives the old record from the migrated access, never from the key; CM19
offers the removal only where every job image of the repository is at or after release 1; CM29
waits on CM28's gate.

**F28 — Gap (review round 1): the project layer is a published object.** `defaults.agent.auth`
lives in the layer `fugaro config publish` writes to the runs bucket (`docs/project-layer.md`,
`internal/cli/config_publish.go`), and older images read it. A diff offered in a checkout does
not reach it, and telling a person to remove a line is a manual step. Consequence: R14, CM33.

**F29 — Wrong (review round 1), settles U2 and U3 and corrects F3.** The registry's rules end
`agents/$slug/$run` with `"$other": { ".validate": false }`
(`internal/budget/rules/rules.json.tmpl`), so a new member needs a rules change, with the
drop-on-refusal fallback `AgentEntry.Recipe` already uses for rules not yet deployed. The entry
already carries `credential`, the name of the credential the run's cascade is spending on right
now, printed by `watch --json` and shown by the console. And `projectpolicy.DecodeRepo` is
strict: a new member of the per-repository node is a new version of it. Consequence: R6 as
amended, CM2 and CM12.

**F30 — Gap (review round 1): the old header is a way round the new record.** An agent that
holds the run's identity can send the old header from a new image and be narrowed by the old
record, where a `cascade` record or none is the whole admitted list. Reach would be no wider than
today, but a narrower per-repository access would bound nothing until the old header goes.
Consequence: CM6 intersects an old-header answer with the access record wherever one exists.

**F31 — Gap (review round 1): more files shared with the two plans.** The keys plan's KS5 edits
`internal/gateway/route.go` and `service.go` and `internal/runner/poolchoice.go`; its KS11 edits
`internal/config/stage_accounts.go`; its KS17 and always a budget's AB14 edit
`scripts/live_checks_test.go` and `docs/gcp-live-checklist.md`. Consequence: R9 as amended.

**F32 — Gap (review round 1): what an older image reads in the node must not move.** An older
image resolves an unset access in the node to Vertex and knows no marked pool and no
per-repository block, and the node decodes strictly. Consequence: R15.

### Review round 2

**F33 — Wrong (review round 2), corrects F10, F29's last sentence and R15.** No job image reads the
per-repository node. `config/policy/repos/<slug>` is read by the credentials service
(`RTDBPolicy.Admissions`, `internal/credsvc/server.go`) and by the CLI (`project_cmd.go`,
`project_repos_rm.go`), and `RepoPolicy` has one member and no version. The strict reader a new
member can break is a service not yet redeployed. A new runner has no read of that node at all.
Consequence: R15 as rewritten: the block is gated on the deployed service, not on images; the run
is handed its resolved access by the launcher; the marker stays out of the shared node.

**F34 — Wrong (review round 2), corrects R4.** A record that names only "what the crafts resolve
to" would refuse runs the plan keeps. Today's record under `oauth` names every allowed account
(`repoAuthRecord`, through `infra.WorkflowAccounts`), because a run may name one with `--token`
and the account walk may move to one. And a record of resolutions goes stale whenever the
project's crafts change, for every repository at once, with nothing to rewrite it. Consequence:
R4 as amended, CM34.

**F35 — Gap (review round 2): the account walk has two conditions.** `accountWalkApplies` is off
where an account was chosen explicitly (`--token`, `agent.oauth_token`). After the migration
every access is written, so "one subscription credential" alone would turn the walk on for a
repository pinned to one account. Consequence: C1's answer carries whether the credential was
chosen or is the project's default account, the migration writes it, and CM11 walks only the
second.

**F36 — Gap (review round 2): the cascade's pool has no way to be published.**
`publishedCraftPools` (`internal/cli/project_cmd.go`) clears `legacy_open` on every pool the
config states and refuses one with no owner (D2608), and `doctorLegacyOpenPoolChecks` tells the
owner to name an owner for every `legacy_open` pool. Consequence: CM32 adds the publisher's
exception for the marked pool, and CM18 the doctor's.

**F37 — Wrong (review round 2): the keys plan's KS13 waits on tasks this plan withdraws.** Its
"Depends on" names KS6 and KS10, and KS10 names KS6 and KS9. Withdrawn with nothing put in their
place, KS13 never becomes ready, and this plan's release 2 waits on KS13. Consequence: CM35, a
documents task that amends the keys plan and its graph before anything else of this plan starts.

**F38 — Gap (review round 2): model grades are a second place a file names a credential.**
`model_grades.<grade>.access` may be written in a repository's file (`config.Scopes`),
`applyGrade` (`internal/runner/routing.go`) writes it into the coder's and the merge judge's
access, and `fugaro accounts grade set` writes the repository's file by default
(`internal/cli/accounts_craft.go`). Consequence: D3062's answer reaches it, **which the owner confirmed on 2026-11-20**: a grade's access is
the project's alone, the file's key joins the retired list (R3), the question C1 writes takes
the task's grade, and the command writes the project (CM34). Confirmed by him on 2026-11-20 (O10).

**F39 — Gap (review round 2): one generated file, three tasks.** `TestCliReferenceIsCurrent`
(`ee/clidoc`) holds `website/content/cli-reference.md` to the command tree byte for byte, so a
task that rewords a help string regenerates the page in the same pull request. Consequence: CM23
alone rewords help strings, in every file that has one, and regenerates the page once.

**F40 — Gap (review round 2): readers and writers the census missed.**
`internal/config/example.yaml`, printed by `fugaro config example`, writes `auth: vertex`; this
repository's own `fugaro.yaml` writes `auth: api-key`. Commands write the retired account keys:
`accounts_switch.go`, `accounts_alias.go`, `accounts_rm.go`, `settings_set.go`,
`init_policy_publish.go`. `internal/cli/project_routing.go` refuses removing the routing block
because it would turn the cascade off. Messages phrased by auth sit in
`internal/infra/retire.go`, `internal/runner/halt.go`, `internal/budget/lease.go` and
`session.go`. Documents: `console/docs/json-contract.md` defines `auth` and `notional` by the
auth mode; `docs/project-secrets/spec.md`, `docs/design/v1.md` and `docs/release.md` use the
phrasing. The counts of F1 and F18 are about: a second count gave 65 files and 76 occurrences in
27. Consequence: each is given to a task (CM3, CM8, CM12, CM13, CM14, CM20, CM21, CM26, CM34).

**F41 — Rule (review round 2): the always-a-budget branch moved.** It was read at `8bf13231f` and
merged the same day as `c5310b9a5`, with D3027 to D3029, which change nothing here. The numbers
did move: pull request #3308 reserved D3040–D3059 and checks 800–809 after this plan's first
reading, so this plan's decisions, asked of the owner as D3040–D3053, are recorded as
D3060–D3073, and its checks are 810 and 811.

### Review round 3

**F42 — Gap (review round 3, security): a call with no header went round the record.** F30 closed
the old header only. `narrowToRepoAuth` returns the whole admitted list for a nil or `cascade`
record whatever the call declares, so a hand-built call that sends no header would be served
past the access record, and in release 2, with the old record gone, for every repository.
Consequence: CM6 holds every call that lacks the new header to the access record wherever one
exists, and CM31 refuses a call with no header and no old record.

**F43 — Wrong (review round 3), corrects R4's round 2 amendment against D3061.** Naming, for an
unset craft, every declared credential the project admits would let the service move from a
subscription to the paid key by its own order (`candidates`, `creds.Select`), which is the move
the owner refused. Consequence: R4 as amended again: one credential per model family.

**F44 — Gap (review round 3): the new wire could not say which credential a run chose.** Today
`X-Fugaro-Auth-Account` is how a `--token` run and the account walk select one subscription.
Consequence: C1 (e) declares the craft, the task's grade where it names one, and the chosen
credential where the run chose one, each checked against the record (CM6).

**F45 — Wrong (review round 3), corrects CM32 and the levels.** The service's read of the marker
is the record's (C1, CM6), so one task at level 0 could not hold it; CM5 reads the block CM2
adds; the job's entry for the "served" fact is planned in `internal/infra/spec.go` from a field
CM15 adds; and `internal/craft/legacy.go` is the migration's rule, which a repository first
updated at release 2 still needs. Consequence: CM36 takes the service half of CM32; CM5 waits on
CM2; CM15 writes the job's entry; CM29 removes the launch and runner fallback and leaves the rule.

**F46 — Gap (review round 3): the rejection would reach every older branch.** The launcher reads
`fugaro.yaml` at the run's ref (`agentAtRef`, `internal/cli/run.go`) and the runner the
checked-out file, so in release 2 a follow-up on a branch that predates the removal commit would
be rejected, with a diff offered only for the checkout. Consequence: R3 as amended: the key is
rejected by `validate`, by `update` and at a launch from the default branch; a run of a ref
whose file still writes it reads it as ignored and says so in its log. **Confirmed by
the owner on 2026-11-20** (O10).

**F47 — Wrong (review round 3), corrects F33 and R15 (2).** The service is not the only strict
reader of the per-repository node: `tightenAdmissions` (`internal/cli/project_cmd.go`) decodes
every child with `DecodeRepo`, so a teammate's CLI of the release behind would refuse every
policy publish once the node gained a member, and the node has no version to refuse cleanly on.
Consequence: the block lives in a node of its own, beside the per-repository node, which no
older reader reads; no gate is needed (R15 (2) as rewritten, CM2).

**F48 — Gap (review round 3): more the census missed.** `agentAtRef` reads the auth at a ref;
`internal/cli/followup.go` calls `claudeAccountChoice`; `fugaro review --token`
(`internal/cli/review.go`) defaults to `agent.oauth_token`; help and messages naming the key sit
in `accounts.go`, `doctor_render.go`, `settings_managed.go` and `config_publish_managed.go`;
`scripts/live-checks/check193.sh` reads `creds/repoauth/<slug>` and `check191.sh` prints
`agent.auth: cascade`; and three more documents tests pin the phrasing
(`docs_secrets_brokering_test.go`, `docs_release_notes_test.go`,
`docs_cloud_senior_review_test.go`). Consequence: CM14, CM23, CM26 and CM31 take them.

**F49 — Gap (review round 3): the fallback could land on Vertex.** A new runner with no handed
entry applies the legacy rule to its file; once the offered removal is committed that file names
no auth, which the old default reads as Vertex. Consequence: C1 (c): a file that writes no
retired key means nothing, and a run with no handed entry and no retired key is refused with the
fix, never sent to Vertex (CM8).

**F50 — Rule (review round 3): CM35 puts keys release B behind this plan's release 1.** With
KS13 re-pointed at this plan's I1, the keys plan cannot drop its mounts before this plan's first
release is integrated. And KS6 and KS9 become launchable as soon as their own waits end, so CM35
must not wait on anything. Consequence: CM35 launches first and alone; O8. *Amended 2026-11-22
(D3170):* this plan's I1 is withdrawn with every task of it that is not merged, so KS13 is
re-pointed at model credentials routing's I1 and keys release B is behind that plan's release 1;
CM35 is merged, and its rule that it waited on nothing holds (O8).

## 4. Decisions

All answered by the owner on 2026-11-20, each as recommended; D3061 and D3064 carry a dated
amendment beside the first answer.

| # | Question | Answer | Mark |
| --- | --- | --- | --- |
| **D3060** | What names the credential a stage's calls use, once a repository has no auth mode (F2)? | The craft's access, as built: it names a declared credential or pool, and the run's path follows that credential's type | as recommended. **It reverses model crafts' F24**. **Superseded on 2026-11-22 by D3170 of model credentials routing:** the project's routing table supersedes craft access, and its D3171 keeps one chain per model |
| **D3061** | What does an access no one wrote mean (F2)? | *First answer, kept:* the declared credentials that reach the craft's model, in the order the project lists them, Vertex last where the project has it on. **Amended by the owner, 2026-11-20:** "I don't like the idea of falling through to vertex, it will result in unexpected charges. User should explicitly specify it like 'vertex' or 'native', wdyt?" So: **the first declared credential that reaches the craft's model, and nothing after it. Vertex is never implied**: a craft goes out on it only where its access, or a pool, names `vertex`. An unset access that no declared credential reaches is refused with the fix. Moving on from one credential to another needs a pool someone wrote. `fugaro update` writes today's answer explicitly, so nothing moves on upgrade | as recommended, then **amended by the owner**; the amendment's two parts were asked as questions and answered as recommended (the name stays `vertex`, not `native`; no move to a paid key without a pool). **It reverses D1624**, where unset meant Vertex |
| **D3062** | Does a repository's file keep `agent.crafts.<craft>.access` (F8)? | No. Access is written only in the project's config; `fugaro update` moves a file's existing choice there | as recommended |
| **D3063** | What does the release do with a committed `fugaro.yaml` that still says `agent.auth` (F7)? | It is read, sets nothing and is named: `update`, `validate` and `doctor` say so and offer its removal as a diff. A later release rejects it, behind a gate on the job image's age | as recommended. *Made precise after review round 1 (F26, F27):* "sets nothing" holds from the moment `update` has migrated that repository; until then the key is read once more, by the migration's own rule, so that an unmigrated repository runs as it did (R3). **Confirmed by the owner, 2026-11-20** |
| **D3064** | What becomes of `cascade` (F11)? | *First answer, kept:* a pool that `fugaro update` writes, with the same members in the same order, and the crafts pointed at it; the service's ordering, stickiness and wait rule unchanged. **Amended after review round 1 (F24), by the owner's answer of 2026-11-20:** the pool `update` writes is **marked as the cascade's**. It carries the cascade's existing consent, so its subscriptions are served exactly as the cascade serves them today although `subscription_spread` is off, and it is published as `legacy_open`, so it needs no owner. A pool anyone writes afterwards still obeys the switch | as recommended, then **amended after review**: a plain pool would have stopped serving every subscription (F24). The amendment was asked as a question and answered as recommended. **Reversed on 2026-11-22 by D3183 of model credentials routing:** the existing accounts become that plan's shared pool, with the same members and order, and each person moves their own token later |
| **D3065** | Is the operator's consent switch, `routing.claude.via: cascade`, still required (F11)? | No. The service's admission of each credential for each repository is the consent. The switch stays readable for older images | as recommended. **It retires O2** (the owner's ruling of 2026-10-17) |
| **D3066** | What does "through its gateway and the credentials service" mean for Vertex (F12)? | Vertex is reached through the run's gateway with the job's own identity. The service holds nothing for it and is not called | as recommended. **Amended on 2026-11-22 by D3174 of model credentials routing:** a gateway cap cannot bind Vertex on the job's identity, so the credentials service calls Vertex on its own identity, capped as far as a key is and no further |
| **D3067** | How does the subscription token leave the agent's environment where the service does not yet serve the repository (F4)? | In two releases: the first serves it wherever the bundle is ready and names every repository still on the old path; the second removes that path and refuses such a launch with the fix | as recommended |
| **D3068** | What happens to per-stage accounts and the mixed stage account (F13)? | They become the access of the craft the stage belongs to. The mix stops being a special case | as recommended. **It replaces the keys plan's KS9 and KS10**. **Reversed on 2026-11-22 by D3171 of model credentials routing:** per-stage accounts and the mix are retired, and `update` folds them into one pool |
| **D3069** | Does `fugaro run --token <account>` stay (F14)? | Yes: it names a subscription credential for that run's coder, only among those the project admits for the repository | as recommended. **Reversed on 2026-11-22 by D3176 of model credentials routing:** `--token` is rejected with a fix naming `--pool`, which makes the launcher's pool resolve to a named pool for that run |
| **D3070** | What do new runs write in the registry entry's `auth` (F3)? | Nothing: the views print `-`, its existing value for none, and a new member names what the run goes out on. No version changes | as recommended |
| **D3071** | When does this ship? | After always a budget's integration and the keys plan's serving tasks. Its removal step ships with keys release B | as recommended |
| **D3072** | Does the release that rejects `agent.auth` bump `fugaro.yaml`'s version (F3, F7)? | No. The key is rejected with the fix, and a launch on a job image older than the first release is refused | as recommended. **It sets the rule "a removed field bumps its format's version" aside for this file**, as D3022 did for a value |
| **D3073** | What does a local invocation use (F16)? | What the developer's environment holds, in a fixed order: an Anthropic key, else a Claude subscription token, else the Vertex variables. With none it is refused, naming the three | as recommended |

**What the answers cost, stated once.**

- A project can no longer let one repository's file pick a credential (D3062). A repository that
  needs a different one gets it in the project's config, by the project's owner or operator.
- A craft with no written access goes out on the first declared credential that reaches its
  model, so the order of the project's `credentials:` list now matters, and a repository with
  nothing declared that reaches its model launches nothing until someone declares a credential or
  writes `access: vertex` (D3061 as amended). Vertex was the silent default until now.
  `fugaro update` writes today's answer for every existing repository, and `fugaro craft ls`
  prints what each craft resolves to.
- Triage shares the senior review's credential, because it shares its craft (D3068).
- There is one more kind of pool to explain, the cascade's, and its marker is a second way past
  the subscription-spread switch. Only `fugaro update` writes it, only for a cascade that serves
  today, and the security review of CM32 holds it to that (D3064 as amended).
- The operator's switch for the shared subscriptions is gone (D3065). What stands between a
  repository and a subscription is the admission the project wrote for that credential.
- The subscription token stays in the agent's environment, for a repository the service does not
  serve, through the first release (D3067).
- The removal release sets the version rule aside (D3072): a teammate's older CLI on an older job
  image can still start a run of a file with no `agent.auth`, which that image reads as Vertex.
  Where the job has no Vertex grant the first call fails; where it has one, the run goes out on
  Vertex. `doctor` names an image older than the gate.
- A local invocation follows the project's crafts for its models and not for its credential
  (D3073).
- An older console shows `-` where it showed the auth mode, until a console release reads the
  new member (D3070).

### Amended on 2026-11-22: what model credentials routing replaces (D3170)

The owner's answer of 2026-11-21 (D3170) is that
[model credentials routing](../model-credentials-routing/reconciliation.md) supersedes this plan's
craft access now: how a model is reached is a property of the model, set once in the project's
routing table, and a chain of sources serves each call. **Every task of this plan that is not
merged is withdrawn**, and **contract C1 is withdrawn with its draft pull request #3460, still
open and to be closed unmerged**, re-cut as that plan's C1. What is merged is kept, with the
two-release removal of
`agent.auth` (R3, D3063, D3072) and the rule that an unmigrated repository runs as its file says.
The owner stops the withdrawn tasks by hand before any launches (that plan's O1).

| Earlier task | State | Carried by, in model credentials routing |
| --- | --- | --- |
| CM35, CM1, CM3, CM22, CM32 | merged | kept, as merged |
| C1 (the access contract) | draft #3460 | withdrawn; its C1 |
| CM2 (per-repository crafts) | not started | withdrawn, not replaced: one table per project, and its RT2 carries admission |
| CM4, CM19 | not started | MG1, MG2 |
| CM5, CM31 | not started | RT4 and MG3; X4 |
| CM6, CM36 | not started | CS1, PL3 |
| CM7 | not started | CS2 |
| CM8 | not started | CS6, CS13 |
| CM9, CM11 | not started | CS7 |
| CM10 | not started | CS8 |
| CM12 | not started | CS9 |
| CM13 | not started | CS11 |
| CM14, CM15 | not started | CS10, CS12, CS14, CS15, RT7 |
| CM16 | not started | RT3 |
| CM17, CM18 | not started | RT6, RT7 |
| CM20 | not started | MG5 |
| CM21 | not started | VS1 |
| CM23 | not started | MG6 |
| CM34 | not started | MG4 |
| I1 | not started | I1 |
| CM24 to CM27 | not started | DOC1 to DOC4 |
| CM28, CM29, CM30, CM33 | not started | X1, X2, X3, X5; its X6 to X8 are new |
| Checks 810 and 811 | scripts merged, not run | replaced by its Checks 884 and 885; its DOC2 marks them so in the live checklist |

**Which decisions the amendment touches.** D3060 is superseded by D3170 (the table supersedes
craft access, and its D3171 keeps one chain per model); D3064 is reversed by D3183 (the existing
accounts become the shared pool); D3066 is amended by D3174 (the credentials service calls Vertex
on its own identity); D3068 is reversed by D3171 (per-stage accounts and the mix are retired);
D3069 is reversed by D3176 (`--token` becomes a narrow `--pool`). D3061's intent is kept — Vertex
is reached only where a chain names it, and nothing is implied — and D3065's retired consent
switch stays retired, with the table replacing `routing.claude.via` (its D3173). D3063 and D3072
stand: the retired keys are read and named for one release and then rejected, with no version
bump. D3067's two-release removal of the token from the agent's environment is that plan's CS7.

**The keys plan moves with this one.** Its KS13, which this plan's CM35 had pointed at this plan's
I1, now waits on model credentials routing's I1 instead (its A1), so keys release B still cannot
drop its mounts before a release integrates the work.

## 5. Resolutions

*From 2026-11-22 (D3170) these hold only for what is merged: the resolution of how a credential
is chosen is model credentials routing's, and R1's seam, R2's reach, R4's record, R5's header,
R6's views, R7's migration and R9 to R15 are re-cut there. R3's two-release removal of
`agent.auth` and its rule for an unmigrated repository stand.*

- **R1. One seam.** Every reader of the auth mode is moved to one question: for this stage of
  this repository, which credential or pool, of which type, served by which path. The types are
  the four of A2. Contract C1 writes the question down; no reader keeps a switch on four words.
- **R2. Reach is worked out, never declared.** A `claude-oauth` or `anthropic` credential reaches
  a Claude model (`config.IsClaudeModel`); a provider credential reaches the models its provider
  claims (`config.ProviderFor`); Vertex reaches a Claude model, and is used only
  where it is named (D3061 as amended).
  This is the brief's "declared once, for the models it reaches" without a new key: the
  credential's type and the project's `providers:` block already say it.
- **R3. The retired keys, and their two releases.** `agent.auth`, `defaults.agent.auth`, `agent.crafts.<craft>.access` and
  `model_grades.<grade>.access` in a repository's file (F38), `agent.oauth_token`,
  `stages.<stage>.claude_account`, `default_claude_account` and the node's
  `claude_account_stages` and `default_claude_account`. In the first release each still decodes and is named by
  `update`, `validate` and `doctor`; an older image goes on reading it. *(Amended after review
  round 1: F26, F27.)* For a repository `update` has migrated (the launcher hands its run a resolved access, R15)
  the keys set nothing, the file's `access` included. For one it has not, a new CLI and a new
  runner work out the access from the file's keys by the migration's own rule, in memory, so it
  runs as it did; nothing else reads them. The removal of a key from a checkout is offered only
  where every job image of that repository is at or after release 1. *(Amended after round 3,
  F45, F46.)* In the second release the key is rejected by `validate`, by `update` and at a
  launch from the default branch; a run of a ref whose file still writes it reads it as ignored
  and says so; the launch and runner fallback goes, and the migration's rule stays, so a
  repository first updated at release 2 is still migrated, `update` reading the old keys before
  the file is validated. In the second each is rejected with the fix, behind the image gate (D3072).
  The schemas keep the keys in the first release, with a description that says they are retired.
- **R4. A new record under a new name.** The repository auth record changes subject (F5), so by
  the rule that a field never changes meaning it is a new record, `creds/repoaccess/<slug>`, with
  its own version: the names of the credentials and pools that repository's crafts resolve to,
  and nothing else. `fugaro update` writes both records in the first release, because an older
  image's calls are still narrowed by the old one; *(amended after review round 1, F27, F30)* the
  old one is derived from the migrated access and no longer from the key, and where both exist
  the service never serves an old-header call more than the new record names. *(Amended after
  review rounds 2 and 3: F34, F43.)* The new record names what a repository's runs **may** be
  served by, as the old one does, and not what one run resolves to: for each craft and grade the
  access the project wrote, or, where it wrote none, **one credential per model family**, the
  first the project declares and admits for that repository that reaches a Claude model, and the
  first for each provider, so that for any one model exactly one credential is named and nothing
  moves from a subscription to a paid key without a pool (D3061); and every subscription
  credential a run may name with `--token`. It is worked out
  from the project's config alone, so the commands that change that config rewrite it for every
  enrolled repository (CM34), and no project-level change waits on an update per repository. A
  call declares its craft; the service serves it what the record names for that craft, so a
  run's whole reach is the union over its crafts, and the contract says so. The second release stops writing the old one
  and deletes it.
- **R5. A new header beside the old.** A new runner declares the craft its call is for; the
  service resolves it against the record and never trusts the declaration for more than the
  record allows, as today (G16's direction). A call that carries the old header is an older
  image's and is narrowed by the old record. The names are C1's.
- **R6. What the views show.** *(Amended after review round 1, F29.)* A new run's registry entry carries no
  `auth` and a new member, named by C1, that lists the credentials or pools its crafts resolve
  to; the existing `credential` keeps its meaning, the one credential being spent on right now.
  The database rules gain the member, deployed by `update` as the rules are, and a runner that
  meets rules without it drops the member and writes the rest, as it does for `recipe`. `watch --json` prints `auth`
  as `-` and adds the member; `budget show --json` leaves `auth` out, as it does for an empty one,
  and adds the member; `Notional` is worked out from the coder's credential type. The console
  reads the new member in a console release of its own (CM21) and accepts `-` meanwhile.
- **R7. The migration, in `fugaro update`, in place.** For the project: each named account is
  already a credential (`deriveCraftCredentials`); a stage's account becomes its craft's access
  (D3068); a project whose cascade serves anything gets a pool with the cascade's members in its
  order (D3064), the paid key among them only where the policy's `fallback` is `auto` today. For each repository, read from the configuration a run of it checks out
  (`deploymentConfig`): `oauth` writes that repository's crafts to its account's credential,
  `api-key` to the Anthropic credential, `cascade` to the pool, and `vertex`, written or
  defaulted, to an explicit `vertex`; a
  provider's model keeps the provider credential it already resolves to; a file's own
  `access` is moved as written (D3062). It writes only what a craft does not already say, prints
  one line per repository naming what each craft now resolves to, and names what it could not
  convert on every run until a person handles it, as the craft migration does. It asks nothing.
- **R14. The published layer is rewritten by `update`, never by hand** *(review round 1, F28)*.
  Release 1 leaves `defaults.agent.auth` in the layer, where older images read it, and the
  migration reads it as each repository's inherited value. Release 2's `fugaro update` rewrites
  the layer object in place without the key, where every job image of the project is at or
  after release 1, and says so in one line; elsewhere it leaves it and names the images.
- **R15. An older reader reads what it did** *(review round 1, F32; rewritten after round 2,
  F33)*. Three rules. (1) The shared node, which job images read, gains no member: the migration
  never rewrites its project-level `crafts`, and the cascade's pool is published there as an
  ordinary `legacy_open` pool; the marker lives in the installation's config and in the access
  record, which only the CLI and the service read. (2) *(Rewritten after round 3, F47.)* The per-repository node, which the service
  and every CLI read strictly, gains nothing either: a repository's crafts are published in a
  node of their own beside it, named by C1, which no older reader reads, so nothing is gated or
  held back; the rules that cover the policy tree gain the new node, deployed by `update`. (3) A run learns its access from its launcher: a new launcher resolves each craft's
  and grade's access and puts the answer in the execution's environment, under a name C1
  declares; an older image ignores it and reads its file's keys, and a new runner with no such
  entry (an older launcher, or a repository not migrated) applies the legacy rule (R3), and
  where its file writes no retired key either is refused with the fix, never sent to Vertex
  (F49). The
  runner's own answer is never a boundary: the service serves a call only what the record names.
- **R8. Nothing stops without being told first** (the brief's item 6). Before it changes
  anything, `fugaro update` prints, for the repository it is run for: the access each craft will
  resolve to; each retired key its file still writes, with the removal offered as a diff; and, if
  its subscription is not yet served by the service, that the token stays where it is for this
  release and what makes it served. `doctor` reports the same from one helper.
- **R9. Other plans, same files.** A task of this plan is not launched while a task of the keys
  plan, always a budget, workspace service or secrets brokering that edits the same file is live,
  and whichever lands second rebases. The files are `internal/credsvc/cascade.go` and `wire.go`,
  `internal/gateway/gateway.go` and `service.go`, `internal/runner/routing.go` and `gateway.go`,
  `internal/agent/env.go`, `internal/infra/spec.go`, `internal/cli/init_credentials.go`,
  `run.go` and `recipes_skew.go`, `internal/config/providers.go`, `SECURITY.md` and the rules
  file. *(Amended after review round 1, F31.)* More
  files: `internal/gateway/route.go`, `internal/runner/poolchoice.go`,
  `internal/config/stage_accounts.go`, `scripts/live_checks_test.go` and
  `docs/gcp-live-checklist.md`. Named waits: nothing starts before the keys plan's C1, KS1 to
  KS5, KS12 and KS19 and always a budget's I1 have merged (D3071); CM8 and CM9 after AB1b; CM13
  after AB15; CM22 after KS17 and AB14. KS11 is in keys release B and rebases on CM16.
- **R10. Runs in flight.** A run that started before the update holds the record, the header and
  the environment of its own release, and every one of them goes on being read through the first
  release (R3 to R5). The update's in-flight lines gain one, for a repository whose crafts'
  access the migration writes: a run in flight keeps the credential it started on.
- **R11. The review class keeps its rule** (A5). It stops being spelled `cascade` in the runner
  and is asked by its own name.
- **R12. No Terraform file changes.** `vertex`, `model_secrets_served` and `stage_account` keep
  their names and meanings and are worked out from the crafts (F6). The keys plan's KS14 removes
  the model mounts and their variables in its own time.
- **R13. Two releases of this plan.** The first: the seam, the migration, both records, the
  service and the runner on the new question, the retired keys named, the token served wherever
  the bundle is ready. The second, with keys release B: the token's old path removed (CM28), the
  retired keys rejected (CM29, CM33), the old record no longer written (CM31).

## 6. Open tensions and open items

No two answers pull against each other.

- **O1.** D3065 removes an operator's gate that the owner ruled in on 2026-10-17. The switch is
  enforced by the runner and the launcher (`applyRouting`, `cascadeLaunchRefusal`); nothing in
  `internal/credsvc` or `internal/creds` reads it, so its removal widens nothing the service
  bounds. The independent security review of CM8 is asked to confirm
  that, and that of CM32 that only a pool `update` wrote for a serving cascade carries the
  marker.
- **O2.** D3072 sets the version rule aside. The exposure is the one stated under the decisions:
  an older CLI on an older image.
- **O3.** D3061's order is the order of the project's `credentials:` list, which until now meant
  nothing. A project that listed its paid key before its subscription gets the key, and only
  the key, for an unset craft; the migration writes today's answer explicitly, so this bites
  only a repository or a craft added later.
- **O4.** The keys plan and always a budget are amended by F19 to F22 and by D3068. Their own
  documents are not edited by this pull request; this plan's first task, CM35, amends the keys plan (F37, O11).
- **O5.** `fugaro run --token` keeps a name that no longer describes it (D3069). Renaming it is a
  later change with its own readers (the plugin's skills, the orchestrator).
- **O6.** The cascade's own settings (`fallback`, the wait threshold, the back-off) stay
  project-wide in the node's `credentials` block. Making them a pool's own is model crafts' F24
  second half and is not in this plan.
- **O7.** Vertex stays refused under `enforce` (F12). A craft whose access names `vertex`, in
  a project in `enforce`, launches nothing, as today. A new repository in a project that declares
  no credential launches nothing either, until someone chooses (D3061 as amended); `update`,
  `setup` and the setup skill say so and name the choices (CM19, CM27).
- **O8.** The second release of this plan cannot be cut before keys release B's KS13, which itself
  waits on Check 50 (D3013) and release A. And by CM35 the keys plan's KS13 waits on this plan's
  I1, so keys release B cannot drop its mounts before this plan's release 1 is integrated (F50).
  D3071's "its removal step ships with keys release B" holds; the order inside that release is
  this plan's I1, then KS13, then CM28. *Amended 2026-11-22 (D3170):* every task of this plan that
  is not merged is withdrawn, its I1 among them, so the keys plan's KS13 waits on model credentials
  routing's I1 instead (its A1) — that plan's release 1, in this plan's place — and the order
  inside keys release B is that plan's I1, then KS13, then CM28.

- **O9.** The graph cannot say that I1 and the contract change wait for C1: it records a wait on
  a contract only for an edge the pass moved there. I1 waits on every side, so it loses nothing;
  the contract change is launched only if raised, by hand.
- **O10.** *Closed by the owner on 2026-11-20.* Three readings the reviews added were asked of him
  as questions, and each was confirmed as recommended: D3063 means the key sets nothing once a
  repository is migrated and is read by the migration's rule until then (F26, F27); D3062 reaches
  a model grade's access (F38); and in release 2 a run of an older ref reads the rejected key as
  ignored (F46). D3064's amendment (F24) he had answered already.
- **O11.** The keys plan is amended by CM35, a task of this plan and so not approved. Until it
  merges, the keys plan's documents still list KS6, KS9 and KS10 as tasks, and its KS13 names two
  of them (F37). **The owner stopped KS6, KS9 and KS10 on 2026-11-20**, so none of them runs
  meanwhile; CM35 is what makes the keys plan itself say so.
- **O12.** Round 3's fixes (F42 to F50) are the last commit and were not reviewed again; three
  rounds is the cap. They rewrote R4 and R15 (2) once more and added CM36, so the first reviewer
  of C1 and CM6 reads those against each other.
- **O14.** `main` moved from `d57d71ddb` to `0a8b4e221` under this branch (the keys plan's C1,
  workspace service's WS1 and always a budget's documents landed). The code claims were checked
  at the first and not read again at the second; the first task to touch `internal/credsvc`
  re-reads F5, F24 and F42 against it.
- **O13.** Not verified: that every launch path (run, follow-up, review, the shared project job,
  the orchestrator) goes through the one function CM14 changes and so hands a run its access
  (U8).
- **O15.** Added 2026-11-22. The tasks D3170 withdraws are stopped by the owner by hand, as the
  keys plan's KS6, KS9 and KS10 were: nothing in this repository stops a run, and this plan's
  levels, critical path and checks are the record of what was planned rather than a launch order
  (model credentials routing's O1).

## 7. UNVERIFIED

- **U1.** *Settled by the owner on 2026-11-20:* that every run on a subscription has its token
  attached by the service is within what he has confirmed of the provider's terms; it is the
  mechanism already in use.
- **U2.** *Settled by review rounds 1 and 2 (F29, F33):* `projectpolicy.DecodeRepo` is strict and
  its reader is the service, so the block waits on the deployed service (R15).
- **U8.** Which launch paths build an execution's environment outside `internal/cli/run.go`
  (O13). Settled by CM14's first test, which launches by each path.
- **U7.** Whether the workspace service, under its own plan, reads the per-repository node
  strictly too, and so needs the same guard. Settled by CM2's first test once that service exists.
- **U3.** *Settled by review round 1 (F29):* they do not; a rules change is needed.
- **U4.** That the cascade's pool is offered exactly what the cascade offers, the wait-or-switch
  rule included (D3064 as amended). Settled by CM32's test, which compares what the service
  offers one repository before and after.
- **U5.** That a run in flight on the release behind survives the node gaining the per-repository
  block and the database gaining the new record. Check 810, owner-run.
- **U6.** The exact names of the new record's members, the new header and the registry's new
  member. Contract C1.
