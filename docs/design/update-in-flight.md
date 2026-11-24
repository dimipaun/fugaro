# `fugaro update` while runs are in flight

Status: the owner's requirement of 2026-11-09 (an addendum to CLAUDE.md's HARD
RULE). `fugaro update` must be safe while runs are in flight, always, and a test
must pin it. Until this document nothing stated it: the claim was believed, the
one place the code acted on it (`refuseRunsInFlight`, `internal/cli`) refused the
write instead, and a person who updated with a run in flight was told to wait.

The question this document answers, step by step: a run that **bootstrapped
under the previous release** and is still running when the update applies — what
can it observe, and what can it lose? Every row names the surface, the exposure
and the evidence. Anything not established from the repository is marked
**UNVERIFIED** and is never stated as fact.

## What a run holds, and what it re-reads

Two facts decide every row below, and both are read off the runner, not assumed.

**The bootstrap reads are memoized.** The project policy node and the deployed
rules generation are read once per run and never again: `policyRead` with its
`done` flag and `readPolicy` (`internal/runner/projectpolicy.go`) memoize
`projectpolicy.Read` (`internal/projectpolicy/read.go`), whose `RulesGeneration`
reads `config/limits/policyRules` (`projectpolicy.PathRulesMarker`). A marker or
a node that moves mid-run does not reach the run through this path.

**The budget inputs are re-read live.** `Session.readConfig`
(`internal/budget/session.go`) re-reads `config/limits`, `config/mode`,
`config/caps/*` and `config/kill/*` on every grant and every report
(`Session.tryGrant`, `Session.plan`, `internal/budget/lease.go`). The rules
themselves are never fetched by the run: the database evaluates whatever document
is deployed against each request, so a rules deploy reaches a running run's *next*
write.

A run's own writes are exactly the run-token locations of the generated rules:
the registry entry `agents/<slug>/<run>` and its heartbeat, the lifetime ledger
under `runs/<slug>/<run>/`, the day counters under `spend/<day>/…`, the
`byTier` split, and the outcome `outcomes/<day>/<slug>/<run>` at the end
(`Session.Start`, `Session.heartbeat`, `Session.Finish`,
`internal/budget/registry.go`; `Session.tryGrant`, `Session.plan`,
`Session.release`, `internal/budget/lease.go`). `TestRunTokenWritesSurviveThePolicyEdit`
(`internal/budget/rules/rules_test.go`) enumerates them against the generated
document, and it is exhaustive in the other direction too.

## One row per step

The steps are `initStages` (`internal/cli/init_stages.go`), in the order the
update door runs them, plus the steps `converge` takes before the first stage
plans — they change what that run's own applies carry, which is why they run
before the loop. "Exposure" is what a run bootstrapped under the previous
release could observe or lose.

| Step | What it changes | Exposure for a run in flight | Verdict | Evidence |
| --- | --- | --- | --- | --- |
| image catalog guard | Reads the owner's local config's `image_catalog` before anything else the run does, and judges every entry through the catalog's own reader — `catalogUnfetchable` and `catalog.Resolve`, the two `fugaro image build` resolves an entry through: the entry's repository at its pinned ref, and the definition's own refusals. The one entry whose fix is unambiguous — a `source.branch` the repository no longer has, whose file the default branch has — is fixed in the file with one `yamledit` splice, the file backed up first and the value that went named in the line; every other finding is one line naming the key to edit | None. The file is this machine's and no run reads it: the catalog reaches a run through the published node and the image its own execution already holds. Its only cloud reads are the entry's repository, through the same read-only credential seam the catalog build reads a Dockerfile with, and the published policy node that seam resolves the secret against — no write, no plan, no apply, no bucket, no job — and a read it cannot make is one warning line and never the run's failure | Safe | `guardCatalogEntries`, `internal/cli/update_catalog_guard.go`; `TestUpdateCatalogGuardRemovesAStalePin`, `TestUpdateCatalogGuardIsSilentOnACleanCatalog`, `TestUpdateConvergesWithACatalog`, `internal/cli/update_catalog_guard_test.go` |
| default profiles | Writes the default job profile of every provider the project's repositories use into the local config, before the first stage plans, so that the installation stage deploys its job in this same update (S5, R1, D2310); a config that can no longer compose the job of one of those profiles — the last GitHub repository moved to Bitbucket, or a repository on a second App added — is planned from the installation's own record of that job instead, which the plan then leaves alone (`installSpec`, `infra.CarriedProjectJobs`) | None. The file is this machine's and no run reads it: the profile reaches a run through the policy stage's node and the shared job the installation stage deploys, and both are this run's own writes (the rows below). The carry changes no job either: it puts back the job the installation already holds, whole, so the plan proposes no change to it | Safe | `createDefaultProfiles`, `installSpec`, `internal/cli/init_policy_migrate.go`, `CarriedProjectJobs`, `internal/infra/tfvars.go` |
| preflight | Reads the environment and the local config; no cloud write | None | Safe | `preflightStage` |
| installation | Terraform root `installation`: the GCS bucket, the Artifact Registry, the credential containers, the history/coordinator/catalog-check jobs, the shared project jobs, the credentials service's own block (its image is moved by the credentials-service row below), and — for an installation whose tfvars carry an `orchestrator` block, which the credentials-service stage writes the first time an update finds the installation able to carry one (a credentials service and a recorded Firebase backend; `ensureOrchestratorBlock`, `internal/cli/update_orchestrator.go`) and every later update carries forward rather than planning it away (`internal/infra/tfvars.go`'s `CarriedOrchestrator`) — the resident orchestrator's own service account and section 4.3's bindings (`modules/installation/orchestrator.tf`; mobile plan task P5.1b) | A create or an update, never a replace: `google_storage_bucket.runs`, the registries and the secret containers carry `prevent_destroy` (`deploy/terraform/gcp/modules/installation/bucket.tf`, `registry.tf`, `secrets.tf`, `credentials.tf`). A job whose *name* would change is ForceNew and is destroyed — see the job row below. The orchestrator's block is creates in a step that already runs, and one revision: the account, its bindings, the pairing container, the service and the tick are new, and the one existing resource that changes is the credentials service, whose new revision names the daemon's account in the orchestrator class (`FUGARO_ORCHESTRATOR_ACCOUNTS`) and drains the one a run started on exactly as an image move does — a call between the two revisions is served by either, and both read the same bundle and node. The same apply creates one binding per operator on the new account (`fugaro_orchestrator_token_creator`), which touches nothing a run holds. An installation that cannot carry the block (no credentials service, no recorded Firebase backend, a development build) plans none and the update says so in one line. Nothing the account holds is anything a run in flight uses, the update names the revision in its in-flight lines (`resident orchestrator: …`), and it waits for no run | Safe, with the job-name caveat | `newInstallationStage`, `deploy/terraform/gcp/roots/installation/main.tf`, `deploy/terraform/gcp/modules/installation/orchestrator.tf` |
| installation, licence renewal (part of the row above) | The renewal job, its service account, the Cloud Scheduler trigger and the empty container the renewal secret is written into, created by the same root under the coordinator's own condition (`local.deploy_coordinator`, `modules/installation/licence.tf`) | A create, once: none of the four names changes after the first apply, the container is created EMPTY and carries `prevent_destroy` (D524 — the core never reads a key), and no run names, reads or writes the job, the account, the schedule or the container. A run's records, ledger and counters are untouched by all four | Safe | `licenceJob`, `internal/cli/update_licence.go`, `deploy/terraform/gcp/modules/installation/licence.tf` |
| installation, the implicit review job (piece 1 of `docs/review-job/spec.md`) | One review-only job per (provider, account) for a project that declares no profile (`ImplicitReviewJobName`), with its own service account and the row the credentials service admits a review caller by. It is composed by the run that finds the `credentials_service` block in the installation's tfvars (`specReviewJobsOfTheService`), so an installation at the previous version — the block deployed by the release behind, no review job beside it — gets the job from ONE `fugaro update`; a config that can no longer compose it keeps the job the record holds (`CarriedProjectReviewJobs`) | None: a create beside the jobs the run's own execution runs on, never a replace of one — a job's template reaches the next execution, and a run in flight holds its own execution's. The next update hands terraform the same entry with two members moved and no others: the base image, because the installation stage composes this root before the images stage pins this release's, and `FUGARO_CREDENTIALS_SERVICE_URL`, because the address the run that deployed the service records reaches a shared job on the apply after that one (`internal/infra/projectjobs.go`); the update after that hands it back byte for byte. The job's name, its service account and its class never move, which is what a destroy would need | Safe | `implicitReviewVariant`, `internal/infra/projectjobs.go`; `specReviewJobsOfTheService`, `internal/cli/init_stages.go`; `CarriedProjectReviewJobs`, `internal/infra/tfvars.go`; `TestUpdateAddsTheImplicitReviewJobAndLeavesARunInFlightRunning`, `internal/cli/update_review_job_test.go` |
| Firebase | The Realtime Database instance, the web API key, the token-signing service account | The run reaches the database with an ID token exchanged from a custom token minted by its own launcher: `token.Exchange`, `internal/budget/token/exchange.go`; the database identity is not an IAM identity (`deploy/terraform/gcp/modules/firebase/iam.tf`). A re-apply of the instance's own fields does not invalidate it | Safe | `newFirebaseStage`, `deploy/terraform/gcp/modules/firebase/firebase.tf` |
| images | Pins `base_images.<kind>` by digest, mirrors the history image, and rebuilds the catalog entries of this checkout whose recorded build was built FROM an older release base (naming the ones another checkout builds, never passing one over); never patches a job | A run's own execution already holds its image; the pin and the rebuilds reach the *next* execution | Safe | `imagesStage`, `internal/cli/init_images.go`, `internal/cli/init_images_catalog.go` |
| installation, second apply | The history job and the catalog check job, which only the images stage makes deployable | Same as the first apply | Safe | `newInstallation2Stage` |
| secrets | Secret containers and their IAM; no version is written | A job's mounts read `version = "latest"`, so a new version reaches the *next* execution (`docs/bitbucket-workspace-token/reconciliation.md`, F3) | Safe | `newSecretsStage`, `deploy/terraform/gcp/modules/repo/secrets.tf` |
| plugin | The plugin pin and the Claude Code install on this machine | None: no run reads either | Safe | `newPluginStage`, `internal/cli/init_plugin_stage.go` |
| repository | Terraform root `repo` for the checkout's repository: the workflow jobs, the job service accounts, the mounts, the check job | A job whose name changes (a workflow added, removed or renamed in `fugaro.yaml`) is ForceNew: Terraform destroys the old job and creates a new one. Whether that reaches a *running* execution of the old job is a property of the platform, not of this repository — **UNVERIFIED** | Needs care, guarded | `newRepositoryStage`, `deploy/terraform/gcp/modules/repo/workflows.tf`, `deletion_protection` in `modules/workflow/job.tf` |
| rules | Generates this release's rules document and deploys it, then writes `config/limits/policyRules`, `…/credsRules`, `…/orchestratorRules` | The generator may only **add**: `TestRulesPreexistingPathsUnchanged` diffs the frozen generation-3 document and fails on any pre-existing rule that changed, and `TestRunTokenWritesSurviveThePolicyEdit` fails when any run-writable location loses its grant. A rule that only adds a deny for a path the old runner never writes is therefore compatible by construction | Safe | `newRulesStage`, `internal/cli/init_rules_stage.go`; `infra.DB.Plan`/`Apply`, `internal/infra/firebasedb.go`; `rules.Generate`, `internal/budget/rules/rules.go` |
| model-prices | Rewrites this machine's local config file in place — through `internal/yamledit`, keeping every value, though a comment written on a renamed key goes with it — where it still writes the old cache-price key names (`cache_read`, `cache_write_5m`, `cache_write_1h`) rather than the `_mult` names this release documents, and before the policy stage as SA4.2 places it: the rename is applied to the config the run holds, so the node that stage publishes is published from the renamed copy (the node is the same either way — the row below has why) | None. The file is this machine's and no run reads it: the prices reach a run through the policy node (the row below), and that node carries the folded multiplier whichever of the names the file wrote (SA4.1), so the rename moves no figure a run pays. A file shape the splice refuses is a warning that leaves the keys as they are — the old names keep their meaning — and never a stopped update | Safe | `newModelPricesStage`, `internal/cli/update_model_prices.go`; `localcfg.LegacyCacheKeys` |
| policy | Migrates and/or publishes `config/policy/shared` in place, from the local config, under the ETag it read | The node is memoized once by the runner, so a non-cascade run keeps the ceiling it bootstrapped with. A **cascade** call does re-read it: the credentials service reads the policy per call (`internal/credsvc/cascade.go`, `bitbucketproxy.go`), so a run of a cascade project is held to the node from its next call. A migration preserves the values it carries, so a plain upgrade changes no run's ceiling; a config the operator changed is the operator's own publish | Safe by intent, named in the line | `newPolicyMigrateStage`, `internal/cli/init_policy_migrate.go`; `policyRead`, `internal/runner/projectpolicy.go` |
| policy, the `bitbucket_tokens` half | The same stage publishes the typed Bitbucket tokens the local config records, each with the coverage it reaches and the identity the creds state store proved (R5, R12). A project naming no record derives no key at all, so an installation holding the legacy `bitbucket-token` alone converges with no step and no read of the store (R3) | A **proxied** call re-reads the node on every request it serves (`bitbucketproxy.go`: the selection is made from the node's coverage), so the first call after the publish may be served by a different token than the call before it — a record the operator added takes over a repository it covers, and one they narrowed stops serving it, which is their own records taking effect. A run of the release behind meets the key too and does not refuse it: its runner tolerates an unknown top-level key and falls back to its single `GET /user` identity (R5). Nothing of the run is rewritten, and no run is asked to finish first | Safe, named in the line | `buildProjectPolicy`, `policyConfigDrift`, `internal/cli/init_policy_publish.go`; `bitbucketTokenIdentities`, `internal/cli/project_cmd.go`; `tokensPublishReachesRuns`, `internal/cli/update_in_flight.go` |
| policy, the Bitbucket proxy switch | The same stage turns the switch on in the operator's local config — `project_secrets.bitbucket-token.proxy`, with the project's Bitbucket repositories added to that entry's `allow_data_to` — for a project with a Bitbucket repository whose installation carries the credentials service and whose runner is at `proxySince` (S6, D2306, D493), and the node's `bitbucket_token` block is published from that very entry, so the file and the block never disagree for `resolveBitbucketProxied`. A bundle holding no Bitbucket token leaves it off, with one line naming `fugaro creds set bitbucket-token--<name>` (R7); two Bitbucket workspaces in one project leave it off too, with one line naming the second (D2314, as amended 2026-11-18: the warning holds the switch back and the update finishes every other step); and a machine whose local config records a `policy_generation` the published node has moved past leaves it off for that run, with one line saying the re-seed records the generation and the next update turns the switch on (S6's fix, 2026-11-18: `bitbucket_token` is a member D854's re-seed splice table has no case for, so a plan built with the switch lands on the fallback that refuses the whole update) | None for a run in flight. Its job's execution is what carries the service's address, and its runner read the node once, at bootstrap: `proxiedBitbucket` is computed there and never again, so a run that was not proxied when it started keeps the credential it has. What this step sets, where it turns the switch at all, is the **address** the repository's jobs are admitted by — the node's `bitbucket_token` block, derived from the entry the same run writes — and not the job's own shape: the mount comes off at the **next** update's repository stage (the drop, `update_proxied.go`), because this update's repository stage ran before the node carried the block. So the launch that fetches its credential from the service is the one after that update, and a launch before it still holds the mount it had. Nothing of the run is rewritten, and no run is asked to finish first | Safe, named in the line | `bitbucketProxyFlip`, `internal/cli/update_proxy_switch.go`; `bitbucketProxySwitchTurns`, `internal/cli/update_in_flight.go` |
| credentials-service | Moves the installation's recorded credentials image onto this release's, then applies the installation root with it | The service reads the published node once per cascade and Bitbucket-proxied call and refuses a node its own binary does not understand, so this apply may not run before the policy stage's migration; it follows it in the stage immediately after, with nothing between the two, and it is an apply of tfvars alone — every image it needs was published by the images stage. What the order cannot reach is the release behind's own service, which refuses the migrated node until this apply lands: the window is that one apply, and the binary that reads it was released. A run that replaces no service raises no node either: the policy stage holds its write at the version it found — a coding agent's session and a development build both replace nothing, and the image the move would pin is resolved **before** that write, so a resolve this run cannot make fails with the node exactly where it was | Safe by intent, named in the line: the policy clause names the cascade read, and this row's window is named when the same run raises the node's version and moves the service | `newCredentialsStage`, `moveCredentialsImage`, `credentialsMoveHeld`, `credentialsResolveBeforeRaise`, `internal/cli/init_stages.go`; `credentialsWindowOpens`, `internal/cli/update_in_flight.go` |
| workspace-project | Terraform root `workspace`: the workspace's own project's two accounts and its one database grant, and — W3's stage, planned before the record it serves — the workspace's own credentials service, a **second** Cloud Run service on the images stage's image, told what it is by `FUGARO_WORKSPACE_MODE`, with one invoke grant per member job account the owner's config names (`deploy/terraform/gcp/roots/workspace`, `internal/creds.WorkspaceServiceName`). The image is this release's, resolved through the installation's remote repository exactly as the installation's own service pins it, and the address is read back from the root's `workspace_service_url` output in a second apply. A workspace whose Firebase is another project's deploys no service at all, and the skip's own sentence says why (R12) | A member's run calls the service at the address it started with, and this apply puts a new revision behind that same address: the old revision keeps serving until its calls drain while the new one takes them as they land, so a call may be served by either binary — both read the same bundle and the same workspace record. Both applies are of tfvars alone; every image they name was published by the images stage. A member the owner's config no longer names loses their invoke grant in this same apply, which is WS4's step of this note and not this row's. Nothing of the run is rewritten, and no run is asked to finish first | Safe by intent, named in the line (R11): the drain is a rolling replacement and not a stop, and a run bootstrapped under the previous release keeps the environment and the route it started with | `newWorkspaceProjectStage`, `serviceBlock`, `fillServiceURL`, `internal/cli/workspace_create.go`; `workspaceServiceRevisionStale`, `internal/cli/update_in_flight.go`; `deploy/terraform/gcp/roots/workspace` |
| workspace | Migrates and/or publishes the workspace record `workspaces/<name>` and the credentials node `workspace_credentials/<name>` in the workspace's own database | No run reads the workspace record: it is the workspace owner's document (`docs/workspaces/spec.md`). A member's run makes no request for it at all. The **credentials node** is another matter: the workspace's own service re-reads it as it serves a call (R13, at most 60 seconds old), so a member the table this run publishes no longer declares, or a pool entry it no longer holds, stops being served at the service's next read of the node, with the refusal that names the workspace owner's `fugaro update` (R11, R19). A run in flight is not stopped and nothing of it is rewritten — its records, its ledger and its counters are untouched, and its every write still lands — and the reach is named in the line | Safe, named in the line | `newWorkspaceStage`, `workspaceDropsReach`, `internal/cli/update_workspace.go`; `internal/cli/update_in_flight.go` |
| layer | Publishes the project layer object and seeds/re-seeds the local file | A run's layer travels with its task and is parsed at bootstrap (`resolveConfig`, `internal/runner/runner.go`); there is no mid-run re-read | Safe | `newLayerStage`, `internal/cli/init_layer_stage.go` |
| checkout | The plugin pin an older release left, the Claude Code plugin and the repository's job images (`fugaro update`'s checkout steps) | The job image is the execution's own. `fugaro image refresh` patches a job's container image in place and never starts an execution — the patch body drops the execution tokens (`TestApplyCheckJobDropsExecutionTokens`, `internal/infra/checkjob_test.go`) | Safe | `newCheckoutStage`, `internal/cli/init_checkout_upgrade.go`; `infra.PlanCheckJob`/`ApplyCheckJob`, `internal/infra/checkjob.go` |
| budget mode | Raises `config/mode` to `enforce` on a managed project, and records `config/modeSet = update` | The mode is re-read live (`Session.readConfig`), so a run that bootstrapped under `observe` is bounded by the caps from its next lease, and a lease the caps refuse halts it. The step already refuses to raise without the caps that would halt every run, and it says what it did | **The one step that can bound or halt a run in flight.** Recorded, not fixed: it is the operator's own caps taking effect | `raiseBudgetMode`, `internal/cli/update_budget_mode.go` |
| budget mode, the word this machine's own config still says | Rewrites this machine's local config in place where it still says `off` — a `yamledit` splice of that one value to `observe`, the file backed up first and both the value it now says and the file it replaced named in the line (AB8b, D3009) — before the first stage plans, beside the catalog guard and for the same reason: every stage of this run reads the config this run writes. The loader reads the word as the file writes it and lets the config load (`acceptedBudgetMode`, F22), so this step is where the word leaves — on the file, and on the config the run holds, because the converge writes the whole file again at its end — and a splice the editor refuses is one warning line naming the edit, never a stopped update | None. The file is this machine's and no run reads it: a run reads the published node (the row above) and its own task. A run that started with the budget off is the one thing the word reaches, and it is not this write that reaches it — that run has no session for the mode to bound, and the note about the runs in flight says so in one line (R5, below) | Safe | `rewriteLocalBudgetMode`, `internal/cli/update_budget_off.go`; `RemovedBudgetMode`, `acceptedBudgetMode`, `internal/localcfg`; `TestTheLocalConfigIsRewrittenInPlace`, `TestAnOffLocalConfigStillLoadsForTheMigration`, `internal/cli/update_budget_off_test.go` |
| the checkout's own `budget.mode` line | Offers the removal of the `budget.mode` line from the checkout's `fugaro.yaml`, as a diff: written with `--yes` or by a person at the terminal, and printed as the line to remove otherwise — the anchor's own pattern (F12, D3022) — after the loop, because the repository stage reads that file and acts only while the working tree holds the default branch's bytes (`resolveRepoTarget`), so an edit before it would turn this run's own repository step into "not the default branch's file". A checkout this run is not in, a file that is not there and a file that does not say the word are all silence, and a splice the editor refuses is one warning line | None: the file is the checkout's, no run reads it, and the removal changes nothing a run holds. A repository whose runs are refused until the line is merged (D3022's cost) is refused at the launch, which is a run that has not started | Safe | `offerBudgetModeRemoval`, `prepareBudgetModeRemoval`, `writeBudgetModeRemoval`, `internal/cli/update_budget_off.go`, `internal/cli/init_fugaroyaml.go`; `TestTheRepositoryFileRemovalIsOfferedAsADiff`, `internal/cli/update_budget_off_test.go` |
| creation fact | Rewrites `fugaro/managed.json` in the runs bucket from version 3 to the version this release writes, which records the budget amount and no image build count (D1543) | A run in flight re-reads the fact per command, and the run is the release behind, whose reader refuses a version it does not know: its own apply keeps the typed confirmation instead of applying under the gate. Nothing of the run is rewritten, and no record, ledger or counter of it is touched | Safe, named in the line | `convergeMarkerVersion`, `internal/cli/update_marker_version.go`; `markerConverges`, `internal/cli/update_in_flight.go` |
| retire (`--retire`, `--allow-delete`, or a managed project's own retirement) | Removes addresses from Terraform's state and deletes orphaned resources, an orphaned job included | A job delete takes its executions with it. `--retire` is behind `--allow-delete`, the job's labels and `deletion_protection`; a **managed** project reaches the same flow with no flag at all (`retireManaged`, issue #1794), behind the managed gate's admission rather than a typed confirmation, and its API delete at the run's last step is the same one | Needs care, named in the line | `readRetirements`, `deleteOrphanedJob`, `retireManaged`, `internal/cli/init_retire.go` |
| the pull-request index (D2965) | Creates, for each of a repository's runs of the last 90 days whose own records say the run opened, followed or reviewed a pull request, that run's entry under `runs/<slug>/pr-index/<N>/`, with the kind the run's own records give it — `opened`, `followed`, or `reviewed` for a review whose artifact stage wrote the number it read into its record — through `runstore.PutPRIndex`, which writes only when the run has none — so the walk creates absent objects and rewrites nothing, on the runs of an installation that predates the index, and a second update writes nothing. A review run whose record names no pull request yet, which is every review still going, gets none: the number it reviews is in its record only once its artifact stage writes it, which is what ends it | None, and it takes no line of the note's own: the index is a hint a reader falls back from and never waits for (`runs ls --pr N` reads it, and scans the window itself when the pull request has no entry, D2964), the walk reads a run's `task.json` and `result.json` and writes neither, and a run in flight has the shape it must skip rather than wait for — the record a running run has written and the task it has not yet, or a task naming no pull request. The entry that run will need is written by the run itself when it opens its pull request — or, for a review, when its artifact stage names the number it read (the runner half of the same work, RL2W of [docs/runs-ls-speed/implementation-plan.md](../runs-ls-speed/implementation-plan.md)) — so the update is never a reason for it to finish | Safe | `backfillPRIndex`, `backfillPRIndexIn`, `internal/cli/update_prindex.go`; `TestUpdateBackfillLeavesRunInFlight`, `TestUpdateBackfillsPRIndex`, `TestUpdateBackfillIdempotent`, `TestBackfillIndexEqualsScan`, `internal/cli/update_prindex_test.go` |

## The surfaces, one at a time

**The Firebase rules document and its generation marker.** The three markers sit
under `config/limits`, which is `.read: TOKEN` and writable by no client
(`rules.json.tmpl`; `TestRunTokenCannotWriteTheCredsRulesMarkers`,
`internal/budget/rules/creds_emulator_test.go`). A run reads
`config/limits/policyRules` once, at bootstrap, and gates its policy read on
`projectpolicy.PolicyRulesVersion` — deliberately not on `RulesVersion`, so a
generation bump does not answer "not found" to a run of any generation
(`projectpolicy.Read`, `version.go`). Forward, the document only adds; the
registry entry keeps its shape because `budget.AgentEntry`'s fields are template
children of the entry node (`TestAgentEntryFieldsAreTemplateChildren`). The
reverse direction — an *older* binary redeploying an older document over a newer
one — is the KNOWN GAP recorded in `internal/budget/rules/marker.go` and is not
something a current release's own update can do: `infra.DB.Plan` refuses to
deploy over a marker above this binary's generation.

**The published policy and the project layer.** Read once by the runner
(memoized), re-read per call by the credentials service for cascade calls, and
carried with the task for the layer. The update's own write is ETag-conditional
and preserves the values a migration moves.

**A version bump is a shape question, and the ordering plus the carry is what
makes it safe.** The
update migrates the node in its policy stage and replaces the credentials
service in the stage immediately after it, with nothing between the two (the
owner's ruling of 2026-11-10): the re-pin cannot stay in the installation stage,
which runs before the image builds, because a version-4 → 5 bump in between
would have made every cascade and Bitbucket-proxied call refuse the node for the
length of those builds. The service this release installs is therefore never
handed a node it has not migrated — and it does not need to be, because the
credentials service's own read carries a node behind it forward in memory
rather than refusing it (`DecodeCarriedForwardShapeFree`, whose floor is
`ShapeFreeFloor` = 4, `server.go`): the steps between the node and this release
are applied to its bytes for the length of one call and written nowhere, so the
service serves a node the release behind published whether the step between
them moves the version member alone or rewrites blocks — which this release's
own step from 8 does (the split, MC21, D1668). What the order cannot close is
the mirror of the same window: until this
stage's apply lands, the process in the project is the one the release behind
deployed, and that binary refuses a node above its own version. The window is
one apply — the two stages are adjacent, and this stage builds no image — and
the binary that reads it was released, so it cannot be taught anything, and it is
named in the update's own note about the runs in flight (`credentialsWindowOpens`)
rather than left to be discovered by a refused call. What such a bump must not
be is a step that needs something OUTSIDE the node: 3 → 4's republish is the
one, no reader can derive the block it adds from the bytes it holds, and
`ShapeFreeFloor` is where the carry stops for exactly that reason.

Two writes are the pair's precondition rather than a second attempt at ordering
them. The image the move pins is resolved and verified **before** the policy
stage's write (`credentialsResolveBeforeRaise`, one memoized resolve the move then
reuses), so a registry read or a signature this run cannot make fails with the
node exactly where it was — the state the review of 2026-11-10 reproduced, where
the write landed first and left this release's node under the previous release's
service. And a run that will replace no service does not raise the node at all:
in a coding agent's session, and in a development build, the policy stage holds
the node at the version it found and says why (`credentialsMoveHeld`), which is
the same question the service's own stage answers with its skip.

**A job image older than the node is refused at the launch, not by the update.**
The update rebuilds the derived images of the checkout it runs in, and — for a
catalog project, whose catalog image *is* the image its jobs run (D1900) — the
catalog entries of that checkout whose own build record says they were built
FROM a base image older than this release's. A catalog entry is otherwise built
by the daily check job and by `fugaro image build`, so an installation whose
schedule is paused, or whose entry nobody pushed, kept an image the launcher
refuses while the stage that had just pinned the new base reported nothing to
do: that is the failure of 2026-11-14, and the record read is what closes it
(`internal/cli/init_images_catalog.go`). What it does not rebuild is anything
else: a repository of the project on another checkout's image keeps the image it
had, and so does a catalog entry whose build runs as another repository's build
account — a catalog build holds the submitting checkout's credential and
secrets, so this run cannot submit it. That one is never passed over in silence:
the images stage prints one line per such entry naming the entry, its checkout
and the exact command (`fugaro image build <entry>`), ends with a line saying
what it did not cover, and leaves the command as a step for the user (exit 1)
rather than reporting a stage that skipped half of itself as done. An entry
whose own definition its rebuild refused — a pinned branch that mounts a
secret — is the one exception, and it is the reader's: the command reads those
same bytes and refuses them the same way, so the step prints the refusal, which
names the line and the change, and no command at all.

A run of that repository, started on an image it kept, would halt at bootstrap
on a node its runner cannot read. The launcher refuses that launch before the
execution exists, from the same `*Since` gates every image lockstep uses
(`internal/cli`'s `policyImageGate`), naming the operator's own move
(`fugaro image refresh …`, or `fugaro update --base base`). What the update never
does is ask a run to stop or to wait for an image: the runs already in flight
keep the memoized read above, and the refusal is about the next launch.

**Job definitions and images.** A Cloud Run job execution runs the template it
started with; a template update — Terraform's, or `fugaro image refresh`'s direct
`jobs.patch` — reaches the next execution. The repository states this as a
consequence (`docs/bitbucket-workspace-token/reconciliation.md`, F3: "Executions
already running keep the old value until they end") and the direct patch as an
unverified-until-live claim (`docs/image-refresh/spec.md`, "The check job and
Terraform"; `docs/gcp-live-checklist.md`, Check 30, USER-RUN). Nothing in the
update cancels, drains or deletes an execution: `fugaro cancel` is the only path
that does (`internal/cli/cancel.go`, `backend.Cancel`, `internal/backend/gcp/run.go`).

**The credentials service and its bundle.** The update deploys a new revision of
the *same* Cloud Run service: the URL and therefore the audience are carried
forward (`CarriedCredentialsService`, `internal/infra/tfvars.go`), and the ID
token is minted per call (`metadataIDToken`, `internal/runner/gateway.go`), so
nothing a run already holds is invalidated. The verifier's job-account table is
refreshed by union (`RefreshCredentialsServiceJobAccounts`), so an accepted
caller is never dropped from it. The bundle is a new **version** of one secret
container (`bundleStore.add`), read through a 30-second cache
(`BundleLoader`, `internal/credsvc/bundle.go`), so a call in flight keeps the
credential it was given. Two operational hazards are worth naming and are not
about runs in flight: a revision rolled out still carrying the placeholder
audience admits nobody (`fillCredentialsAudience`, `internal/cli/init_stages.go`),
and whether Cloud Run keeps serving an in-flight streaming request across a
revision rollout is **UNVERIFIED** — platform behaviour, not a repository fact.

**The budget and lease records.** No step of the update writes a run's ledger,
its registry entry or its day counters; the run's own paths are unchanged by the
generator (the rules row above). The build lease family
(`rules.PathBuildLeases`) is IAM-only and written by the CLI, never by a run.
`config/mode` is the one budget input the update can move under a run, and the
budget-mode row above records it.

**The run store.** The record is `runstore.Record` in `result.json` under
`runs/<slug>/<run>/` in the runs bucket — GCS, not Firestore
(`internal/runstore/runstore.go`; `google_storage_bucket.runs`,
`deploy/terraform/gcp/modules/installation/bucket.tf`). No step of the update
changes its schema or its bucket, and `fugaro update` never writes a record. Its
version is a literal `1` (`runner.go`'s `Record{Version: 1}`, `internal/cli/exec.go`)
and nothing on the read path checks it (`Store.ReadRecord`, `ReadRecordVersion`),
which is why CLAUDE.md's rule — a field never changes meaning, a removal or a
retype bumps the version — is what keeps an old runner's record readable by a new
binary. A shape change is therefore a release's decision and not an update's
hazard; adding a field is safe for both directions.

## What the update must do about it

Nothing in the table asks a person to stop a run, and one row did the opposite:
the policy stage refused the write without `--yes` when a run was in flight and
told the operator to wait (`refuseRunsInFlight`). The owner's requirement of
2026-11-09 makes that unacceptable — a team of ten or twenty always has runs in
flight — so the update door no longer refuses, and it announces the runs once
instead (`announceRunsInFlight`, `internal/cli/update_in_flight.go`, called from
the converge before the first stage). The count is a step of the run like any
other, so the wait it can cost is named while it happens — `Checking for runs in
flight`, the CLI's own spinner and its one line per step — because the owner met
it with fourteen runs going and no output at all (the feedback of 2026-11-15).
What it prints after that is the count's own line: how many runs are in flight,
that the update waits for none of them and changes nothing they hold. Then one
short line per step that can still reach one, indented under it, or a single line
saying no step does (`inFlightSteps`). The note is exactly as long as that count
and no longer — plus the two lines that are not steps and carry the two facts the
count's line cannot. R5's is printed where this machine's config still says the
word (`budgetOffRunFinishes`): a run that started with the budget off has no
session for the mode to bound and holds its key already, so what the note owes
the person is that such a run finishes. MG3's says that this release keeps
writing the repository's old access record beside the routing table, which is
what a run whose image predates the table resolves from (`repoAuthSources`,
`internal/cli/update_repoauth.go`; `legacy_auth.go`, `internal/credsvc`) — and it
counts those runs, because the image a run is running is a fact this machine
does hold: the run's own record carries it (`ImageInfo`, written at bootstrap out
of the statement the image makes about itself), so the line reads each run of the
registry against the moment the table reached the branch (`chainIntroduced`,
`runsOnTheOldRecord`) and says how many of the count above are its readers. A
record that names no build time, and one that cannot be read at all, count —
the direction the image-age gate takes for an image it cannot place — and the
line is printed only when the number is not zero.
Beyond those lines the note is as short as the count: the same feedback
could not find what it was after in the paragraph this used to be, and what each
line stands for — the question its step is gated on — is written where that
step's own code and gate are. It never
asks anybody to wait, and it never claims the flat "the runs are unaffected",
which the budget-mode raise below makes false. It is absent when nothing runs, under `--json` and `--print-vars`
(whose stdout is a document), and under `--plan-only` (which applies nothing).

The door asks nothing of its own: the note is information, printed before the
update's own confirmation and before any stage writes, and it is where a person
deciding whether it is safe to update now reads it. Nothing is added to the
no-prompt policy's surface — a pipe, `--json`, a coding agent's session and a
managed project all proceed exactly as they did, printing the note only
(`docs/no-prompt-policy/spec.md`) — and a run in flight is never a reason for the
update to stop.

The refusal stays where it is not the update door's: `fugaro project policy publish --delete` /
`fugaro project repos rm`, which are not the whole upgrade and are their own
commands. The other two doors that reach this stage through `initStages` —
`fugaro setup`, and `fugaro accounts add` / `accounts rm` — are not the update
door either, so they keep the answer they had before this ruling, which is the
ruling of 2026-11-06: a managed project's own session is told the count as a note
and writes, and every other session is refused
(`inflightManaged`, `policyMigrateStage.inflightAnswer`, which reads the answer
off the door rather than taking it as a parameter). The note is the update
door's and is not printed by those doors.

The rows that are not simply safe are the ones the note has to own, and they are
nine (`inFlightSteps`, `internal/cli/update_in_flight.go`): the budget-mode
raise, which can bound a run that bootstrapped under `observe` from its next
lease and is the operator's own caps becoming effective rather than the update
stopping a run — recorded here rather than deferred, the alternative being the
update asking a person to stop work for it; the published policy, which a
cascade project's every model call re-reads; the window between that write and
the credentials service's own replacement, which is the release behind's service
meeting a node its own binary does not understand; the Bitbucket tokens that same
write carries, which a proxied call re-reads per request, so the call after the
publish may be served by a different token than the call before it; the
workspace's own credentials service, whose new revision this run puts behind the
same address a member's run is already calling — the revision that run started
on keeps serving until its calls drain, and both binaries read the same bundle
and the same workspace record, so the run keeps the environment and the route it
bootstrapped with (R11); the Bitbucket
proxy switch that same stage turns on in the local config, which no run in flight
reads — its runner decided proxiedness once, at bootstrap — and which is named
because every launch of the repository after this update is proxied and holds no
token; the creation
fact, which a run of the release behind re-reads per command and refuses once it
is rewritten, so that run keeps the typed confirmation it would have applied
without; the workspace credentials node the workspace stage publishes, which the
workspace's own service re-reads as it serves, so a member this run takes out of
the table, or an entry it unshares, stops being served at that service's next
read (R13); and a retirement, which deletes an orphaned job and takes the
executions running on it.
Each clause is the question its own step is gated on rather than the whole of its
decision (`budgetModeBindsRuns`, `policyReadsLive`, `credentialsWindowOpens`,
`tokensPublishReachesRuns`, `bitbucketProxySwitchTurns`, `markerConverges`,
`retireTakesExecutions`), so the
note errs towards naming a step that may not end up doing anything over claiming
the update cannot reach a run when it can; the budget raise's shape for the owner
is in the pull request.

The tests are `TestUpdateLeavesARunInFlightRunning` and its siblings
(`internal/cli/update_in_flight_test.go`): an installation one release behind
converges under `fugaro update` while a run of its own repository is in flight,
and that run's later writes — its lease, the ledger and the counters the report
settles, the outcome it ends with, its record and its review session — still
land under the new rules and the migrated policy.
`TestUpdateAddsTheImplicitReviewJobAndLeavesARunInFlightRunning`
(`internal/cli/update_review_job_test.go`) is the same fixture read against the
one job this release adds (the row above): an installation at the previous
version — the credentials service the release behind deployed, no implicit
review job beside it — gets that job from one `fugaro update`, the run in flight
is left running with every write of its own still landing, and the updates after
it hand terraform the same job.
`TestUpdateInFlightLineNamesEveryStepThatCanReachARun` turns each step on alone
and asserts its one short line is there and the others are not, so a step
that can reach a run and is not named fails the suite;
`TestUpdateInFlightLineNamesTheBitbucketTokensPublish` does the same for the
typed tokens a proxied call re-reads.
`TestTheRefusingInflightAnswerStopsTheWrite` and
`TestSetupPolicyStepKeepsTheRunsInFlightAnswer`
(`internal/cli/init_policy_migrate_test.go`) pin the refusal where it is kept —
the answer no door passes any more, and `fugaro setup`, once refused and once
written-with-the-note on a managed project — so the update door's quiet
answer reaching a door that is not it fails the suite.
