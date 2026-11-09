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
`projectpolicy.Read` (`internal/projectpolicy/read.go`), whose `rulesGeneration`
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
update door runs them. "Exposure" is what a run bootstrapped under the previous
release could observe or lose.

| Step | What it changes | Exposure for a run in flight | Verdict | Evidence |
| --- | --- | --- | --- | --- |
| preflight | Reads the environment and the local config; no cloud write | None | Safe | `preflightStage` |
| installation | Terraform root `installation`: the GCS bucket, the Artifact Registry, the credential containers, the history/coordinator/catalog-check jobs, the shared project jobs, the credentials service | A create or an update, never a replace: `google_storage_bucket.runs`, the registries and the secret containers carry `prevent_destroy` (`deploy/terraform/gcp/modules/installation/bucket.tf`, `registry.tf`, `secrets.tf`, `credentials.tf`). A job whose *name* would change is ForceNew and is destroyed — see the job row below | Safe, with the job-name caveat | `newInstallationStage`, `deploy/terraform/gcp/roots/installation/main.tf` |
| Firebase | The Realtime Database instance, the web API key, the token-signing service account | The run reaches the database with an ID token exchanged from a custom token minted by its own launcher: `token.Exchange`, `internal/budget/token/exchange.go`; the database identity is not an IAM identity (`deploy/terraform/gcp/modules/firebase/iam.tf`). A re-apply of the instance's own fields does not invalidate it | Safe | `newFirebaseStage`, `deploy/terraform/gcp/modules/firebase/firebase.tf` |
| images | Pins `base_images.<kind>` by digest and mirrors the history image; never patches a job | A run's own execution already holds its image; the pin reaches the *next* execution | Safe | `imagesStage`, `internal/cli/init_images.go` |
| installation, second apply | The history job and the catalog check job, which only the images stage makes deployable | Same as the first apply | Safe | `newInstallation2Stage` |
| secrets | Secret containers and their IAM; no version is written | A job's mounts read `version = "latest"`, so a new version reaches the *next* execution (`docs/bitbucket-workspace-token/reconciliation.md`, F3) | Safe | `newSecretsStage`, `deploy/terraform/gcp/modules/repo/secrets.tf` |
| plugin | The plugin pin and the Claude Code install on this machine | None: no run reads either | Safe | `newPluginStage`, `internal/cli/init_plugin_stage.go` |
| repository | Terraform root `repo` for the checkout's repository: the workflow jobs, the job service accounts, the mounts, the check job | A job whose name changes (a workflow added, removed or renamed in `fugaro.yaml`) is ForceNew: Terraform destroys the old job and creates a new one. Whether that reaches a *running* execution of the old job is a property of the platform, not of this repository — **UNVERIFIED** | Needs care, guarded | `newRepositoryStage`, `deploy/terraform/gcp/modules/repo/workflows.tf`, `deletion_protection` in `modules/workflow/job.tf` |
| rules | Generates this release's rules document and deploys it, then writes `config/limits/policyRules`, `…/credsRules`, `…/orchestratorRules` | The generator may only **add**: `TestRulesPreexistingPathsUnchanged` diffs the frozen generation-3 document and fails on any pre-existing rule that changed, and `TestRunTokenWritesSurviveThePolicyEdit` fails when any run-writable location loses its grant. A rule that only adds a deny for a path the old runner never writes is therefore compatible by construction | Safe | `newRulesStage`, `internal/cli/init_rules_stage.go`; `infra.DB.Plan`/`Apply`, `internal/infra/firebasedb.go`; `rules.Generate`, `internal/budget/rules/rules.go` |
| policy | Migrates and/or publishes `config/policy/shared` in place, from the local config, under the ETag it read | The node is memoized once by the runner, so a non-cascade run keeps the ceiling it bootstrapped with. A **cascade** call does re-read it: the credentials service reads the policy per call (`internal/credsvc/cascade.go`, `bitbucketproxy.go`), so a run of a cascade project is held to the node from its next call. A migration preserves the values it carries, so a plain upgrade changes no run's ceiling; a config the operator changed is the operator's own publish | Safe by intent, named in the line | `newPolicyMigrateStage`, `internal/cli/init_policy_migrate.go`; `policyRead`, `internal/runner/projectpolicy.go` |
| workspace | Migrates and/or publishes the workspace record `workspaces/<name>` in the workspace's own database | No run reads the workspace record: it is the workspace owner's document (`docs/workspaces/spec.md`). A member's run makes no request for it at all | Safe | `newWorkspaceStage`, `internal/cli/update_workspace.go` |
| layer | Publishes the project layer object and seeds/re-seeds the local file | A run's layer travels with its task and is parsed at bootstrap (`resolveConfig`, `internal/runner/runner.go`); there is no mid-run re-read | Safe | `newLayerStage`, `internal/cli/init_layer_stage.go` |
| checkout | The plugin pin, the Claude Code plugin and the repository's job images (`fugaro upgrade`'s three steps) | The job image is the execution's own. `fugaro image refresh` patches a job's container image in place and never starts an execution — the patch body drops the execution tokens (`TestApplyCheckJobDropsExecutionTokens`, `internal/infra/checkjob_test.go`) | Safe | `newCheckoutStage`, `internal/cli/init_checkout_upgrade.go`; `infra.PlanCheckJob`/`ApplyCheckJob`, `internal/infra/checkjob.go` |
| budget mode | Raises `config/mode` to `enforce` on a managed project, and records `config/modeSet = update` | The mode is re-read live (`Session.readConfig`), so a run that bootstrapped under `observe` is bounded by the caps from its next lease, and a lease the caps refuse halts it. The step already refuses to raise without the caps that would halt every run, and it says what it did | **The one step that can bound or halt a run in flight.** Recorded, not fixed: it is the operator's own caps taking effect | `raiseBudgetMode`, `internal/cli/update_budget_mode.go` |
| retire (`--retire`, `--allow-delete`, or a managed project's own retirement) | Removes addresses from Terraform's state and deletes orphaned resources, an orphaned job included | A job delete takes its executions with it. `--retire` is behind `--allow-delete`, the job's labels and `deletion_protection`; a **managed** project reaches the same flow with no flag at all (`retireManaged`, issue #1794), behind the managed gate's admission rather than a typed confirmation, and its API delete at the run's last step is the same one | Needs care, named in the line | `readRetirements`, `deleteOrphanedJob`, `retireManaged`, `internal/cli/init_retire.go` |

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

**Job definitions and images.** A Cloud Run job execution runs the template it
started with; a template update — Terraform's, or `fugaro image refresh`'s direct
`jobs.patch` — reaches the next execution. The repository states this as a
consequence (`docs/bitbucket-workspace-token/reconciliation.md`, F3: "Executions
already running keep the old value until they end") and the direct patch as an
unverified-until-live claim (`docs/design/image-refresh.md`, "The check job and
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
the converge before the first stage). The line is one sentence, it is not a
question, and it never asks anybody to wait. What it claims is what holds
whatever this update turns out to do — the update asks no run to finish first
and rewrites nothing a run holds — and never the flat "the runs are unaffected",
which the budget-mode raise below makes false; what it can still reach is named
instead. It is absent when nothing runs, under `--json` and `--print-vars`
(whose stdout is a document), and under `--plan-only` (which applies nothing).
The refusal stays where it is not the update door's: `fugaro upgrade`'s own
policy step, and `fugaro project policy publish --delete` /
`fugaro project repos rm`, which are not the whole upgrade and are their own
commands. The other two doors that reach this stage through `initStages` —
`fugaro setup`, and `fugaro accounts add` / `accounts rm` — are not the update
door either, so they keep the answer they had before this ruling, which is the
ruling of 2026-11-06: a managed project's own session is told the count as a note
and writes, and every other session is refused without `--yes`
(`inflightManaged`, `policyMigrateStage.inflightAnswer`, which reads the answer
off the door rather than taking it as a parameter). The one line above is the
update door's and is not printed by those doors.

The rows that are not simply safe are the ones the line has to own, and they are
three (`inFlightSteps`, `internal/cli/update_in_flight.go`): the budget-mode
raise, which can bound a run that bootstrapped under `observe` from its next
lease and is the operator's own caps becoming effective rather than the update
stopping a run — recorded here rather than deferred, the alternative being the
update asking a person to stop work for it; the published policy, which a
cascade project's every model call re-reads; and a retirement, which deletes an
orphaned job and takes the executions running on it. Each clause is the question
its own step is gated on rather than the whole of its decision
(`budgetModeBindsRuns`, `policyReadsLive`, `retireTakesExecutions`), so the line
errs towards naming a step that may not end up doing anything over claiming the
update cannot reach a run when it can; the budget raise's shape for the owner is
in the pull request.

The tests are `TestUpdateLeavesARunInFlightRunning` and its siblings
(`internal/cli/update_in_flight_test.go`): an installation one release behind
converges under `fugaro update` while a run of its own repository is in flight,
and that run's later writes — its lease, the ledger and the counters the report
settles, the outcome it ends with, its record and its review session — still
land under the new rules and the migrated policy.
`TestUpdateInFlightLineNamesEveryStepThatCanReachARun` turns each of the three
steps on alone and asserts the clause is there and the others are not, so a step
that can reach a run and is not named fails the suite.
`TestUpgradePolicyStepRefusesTheWriteWhileARunIsInFlight` and
`TestSetupPolicyStepKeepsTheRunsInFlightAnswer`
(`internal/cli/init_policy_migrate_test.go`) pin the refusal on the doors that
keep it — `fugaro upgrade`'s own policy step, and `fugaro setup`, once refused
and once written-with-the-note on a managed project — so the update door's quiet
answer reaching a door that is not it fails the suite.
