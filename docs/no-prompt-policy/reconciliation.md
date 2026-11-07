# No-prompt policy — reconciliation

**Checked against** `origin/main` as of 2026-11-07, after #1847 (the workspace publish stage) and #1870 (NP3) merged. Where this
file disagrees with [spec.md](spec.md), this file wins. The spec is the owner's, as received.

The spec is right about the principle and about what is wrong today, and it is written without the code
in view in four places that matter: it says Terraform should run with auto-approve (it already runs
non-interactively, and `-auto-approve` is forbidden on purpose), it says an image build should use "the
same budget as jobs" (jobs lease model spend; an image build is bounded by a **count**, not dollars), it
says "managed resources never need confirmation" (the managed gate has refusals that are not
confirmations), and it says agents may run every command (raising a cap is the one command where that
defeats the cap). The owner has answered every question below that needed an answer; two answers are
**not the recommendation** and opened tensions (§5); D1550 is a third (tension T5). The owner ruled the
tensions on 2026-11-07 — T1, T2, T3, T4 and T6 — and held T5 until the T8 spike (task T8S) reports.

## 1. Headline: spec concept → what the repo has

| Spec concept | What the repo already has |
| --- | --- |
| "No command waits for input" | About twenty files of confirmation helpers, value readers and data reads in `internal/cli` (F1); `stdinIsTerminal` decides terminal-or-not per call site, 59 call sites. |
| "Managed projects only" | A managed/unmanaged split: `internal/managed`, `internal/cli/managed_gate.go`, `managed_flag.go` (flagging is a typed-GCP-ID step); an unmanaged project keeps every prompt (F2). |
| "Agents may run all commands" | `agentMarker` / `initflow.Conditions.Agent`: a coding agent's session confirms nothing (`CanConfirmManaged`); on a managed project `GateConfirms`/`Removes` stand in for the prompt (F3). |
| "Budget caps replace cost prompts" | `internal/budget` leases **model spend** per run (`evaluate.go`, `ReasonMaxReserve`); `fugaro budget set` raises caps and asks a typed name; an image build is bounded by **`ImageBuilds`** (`internal/managed/builds.go`, `internal/infra/managed/marker.go` `MinImageBuilds`/`MaxImageBuilds`/`DefaultImageBuilds`), a count per `fugaro image refresh` (F5). |
| "Terraform with auto-approve" | `internal/infra/tf/tf.go` `Apply` runs `apply -input=false` on a **saved plan**; `-auto-approve` is refused by design (a comment names it). Exists (F6). |
| "gcloud with the quiet flag" | Some build-step scripts pass `--quiet` (`internal/backend/gcp/build.go`); `catalog.go` has several gcloud calls that do not, inside Cloud Build step scripts a CLI harness cannot reach (F-UNVERIFIED §6). |
| "Over-cap error: cost, cap, spent, one command" | Not present for builds; `budget` has `Reason*` codes (`internal/budget/evaluate.go`) and an observe mode in which caps do not bite (F7). |
| "Tests: every command, stdin closed" | None. `NewRootCmd` (`internal/cli/root.go`) builds the tree, so a walker is possible (F12). |
| "Fugaro project repo (meta repo)" | Designed, not built; the audit record (`internal/managed/audit.go`, read by `doctor_managed_audit.go`) is what exists (F11). |

## 2. Findings

**F1 — The prompts.** `stdinIsTerminal(cmd.InOrStdin())` appears at 59 call sites in `internal/cli`; the
confirmation helpers sit in about twenty files: `confirmTyped`/`confirmTypedStrict` (`budget.go`,
`project_cmd.go`, `project_enroll.go`, `restore.go`, `init_policy_migrate.go`, `init_policy_publish.go`
`confirmEmptyAllowList`), `confirmRemoval`/`confirmRemovalStrict` (`project_cmd.go`, `upgrade.go`),
`confirmBuild` and `buildNotConfirmable` (`image.go`), `confirmTypedGCPProject` (`managed_flag.go`,
`managed_unflag.go`), `confirmSettingsWrite` (`settings_write.go`, `secrets_rm.go`), `settingsAsk`
(`settings_managed.go`), `askOrdinary` (`init_review.go`), `managedAsk` (`managed_apply.go`), a y/N and a
typed line in `restore.go` (`readConfirmLine`), and readers in `init.go`, `init_firebase.go`,
`init_fugaroyaml.go`, `init_inputs.go`, `init_plugin_stage.go`, `init_project.go`, `init_repo_gate.go`,
`init_layer_stage.go`, `init_retire.go`, `accounts_*`, `allow_delete.go`, `config_init.go`,
`settings_set.go`. **Value entry**, which is not a confirmation: hidden secret values (`secrets.go`,
`term.ReadPassword`; `creds.go` `readBundleValue`), the billing-account choice (`billing_prompt.go`), the
pairing code (`pair.go`). **Data reads**, which are neither: `fugaro run --task-file -` (`run.go`),
`exec.go`, and every `--…-file -` form. Consequence: the spec's "no command waits for input" and "never
hangs on closed stdin" are the same for confirmations, differ for values (D1540) and do not apply to data
reads, which fail on EOF rather than wait. The tasks own every file above (plan T5–T7).

**F2 — Unmanaged projects exist and are the prompt's last home.** The code serves a project that is not
managed with every confirmation; the spec says Fugaro supports managed projects only. Consequence: the
unmanaged case needs a defined behaviour that is not a prompt — D1541.

**F3 — The confirmation machinery is a class system.** `initflow.ConfirmClass` has `Typed` (money and
permanence: no flag, a terminal only, never an agent) and `YesOrTyped`. `CanConfirmManaged`,
`GateConfirms`, `ConfirmsByGate`, `Managed.Covers/Removes`, `Conditions{Terminal, Agent, Yes,
NonInteractive, JSON, Ask}` and `NeedsTerminal` are the decision, and `agentMarker` is the agent
detector. The owner's earlier rulings are in force and consistent with the spec: no prompt on a managed
project, terminal or agent (memory *feedback-no-prompts-on-managed-project*; #1845 removed the typed
policy prompt for a person and is merged; **#1870 (NP3, image build), #1804 (a person at a real terminal on a
managed project) and #1891 (CV1, the cover-table row for `catalog_check_records`) are open pull requests**, so on
`main` `image.go` still asks through `confirmBuild` and `cover.go` still lacks that row; the plan is ordered against
them — T4 and T9 start after #1870 and #1891 merge, or absorb them). What survives today even on a managed
project is the `Typed` class — exactly "money and permanence" — and the `--ask` reversal (`Conditions.Ask`,
`GateConfirms` answers false under it). Consequence: removal is a deletion of the class system's
confirm paths, last, after every caller has stopped asking (plan T12).

**F4 — The flags.** `--yes` is registered in `accounts.go`, `accounts_add.go`, `accounts_rm.go`, `budget.go`,
`config_init.go`, `image_refresh.go`, `init.go`, `models_add.go`, `models_rm.go`, `models_use.go`,
`project_cmd.go`, `project_enroll.go`, `secrets.go`, `secrets_rm.go`, `settings_set.go`, `upgrade.go` and
`workspace.go`; `--non-interactive` in `init.go`; `--ask` in `accounts_add.go`, `image_refresh.go`, `init.go`
and `settings_set.go` (note: `budget set` registers no `--ask`). They are named in the plugin skills
(`plugin/skills/{setup,upgrade,working,parallelism}` and their `reference/`), in docs (`docs/gcp-live-checklist.md`
alone has 31 mentions of `--yes`), in `plugin/` tests that compare every flag a skill names with the CLI, and
**in the console**: `console/src/main/spawn.ts` lists `--yes` in the confirmed-write phase of settings edit and
`console/src/renderer/confirm.ts` is the console's own confirm dialog. Consequence: removing a flag is a
skill, doc, console and test change in the same task (CLAUDE.md, plugin paragraph) — D1544, plan T11.

**F5 — A build is not a job's spend.** Jobs spend through the gateway's lease (`internal/budget`,
`Session.Lease`): model dollars, per run, daily and per-run caps, a `max_reserve`, observe/enforce mode.
A Cloud Build's cost is never in that ledger. The bound on builds is a **count**: `managed.ImageBuilds`
(D1013), read from the project's creation fact (`fugaro/managed.json`, `MinImageBuilds`…`MaxImageBuilds`,
default `DefaultImageBuilds`), applied per invocation of `image refresh` (`image_refresh_managed.go`,
`ImageBuildsBound`) and per run of the catalog check job (`MaxImageBuilds` in
`internal/cli/image_check_catalog.go` and `internal/infra/catalogcheck.go`). Consequences: (a) "same
budget and cap" needs a **cost estimator** that does not exist (D1543: build timeout × machine rate, an
upper bound) and a way for a build to take a lease outside a model session; (b) removing the count
removes a field the marker's reader and writer share, which is a **format change** (a field removal
bumps that format's version — CLAUDE.md "A field never changes meaning"), so the marker's version moves
and `fugaro update` must converge an installation at the previous version, with a test (HARD RULE:
`fugaro update` is the whole upgrade); (c) the catalog check job's own `MaxImageBuilds` spec field goes
the same way.

**F6 — Terraform is already non-interactive.** `internal/infra/tf/tf.go` runs `init -input=false`,
`plan -input=false -detailed-exitcode -out=…`, `apply -input=false <planfile>`. The comment above `Apply` says
only that there is "no way to apply without [a saved plan], or to add -auto-approve, -target, -var or
-replace"; that applying the exact plan the gate saw is the reason is this document's reading, not the
comment's. The spec's "auto-approve" is **Exists** under another form and is not implemented literally.

**F7 — Over-cap error and observe mode.** `budget` carries the reason codes, but there is no error that
gives estimated cost, cap, spend and the raising command in one message, and in **observe** mode caps
are advisory (`evaluate.go`: "advisory in observe like every cap"). Consequence: the spec's "over the
cap: stop" is only true in enforce mode. An image build under observe records its estimate and proceeds;
`doctor` should say that observe + unattended builds means no stop — D1548.

**F8 — Raising the cap asks today, and an agent that may raise its own cap has no cap.** `budget set`
asks a typed project name for a raise ("Raising a cap … loosens what runs may spend"). Spec §3 puts "the
exact single command to raise the cap" in the error and §4 lets an agent run every command: an agent
that reads the error runs the command. Consequence: D1542 (owner session only; refuse in an agent
session, print the command — a refusal is not a prompt).

**F9 — The managed gate has refusals that are not confirmations.** The gate (`internal/cli/managed_gate.go`
`admit`, `internal/managed/converge.go` `Converge.Refuse`, `internal/managed/plan.go`, `apply.go`,
`internal/infra/tf/cover.go` `NotCovered`/`GrantsNotCovered`, `guard.go`, `managed_cost.go`,
`managed_cover.go`) refuses: a plan resource type or a role grant outside the cover tables (`allowedTypes`,
`grantRules`); a delete or replace no owner list approves (`TestApplyRefusesADeleteNoOwnerListApproves`);
a blocked cost attribute (D62); a removal the rule cannot identify; a saved plan that changed after the
gate (`TestApplyRefusesASavedPlanThatChangedAfterTheGate`); a creation fact that does not say managed; a
Terraform environment that injects impersonation; symlinked or deeply nested Terraform input; the budget
evidence (`internal/managed/budget.go`: no recorded amount refuses an apply); the deployer's
impersonation. The owner's D1545 answer is "remove the gate", scoped by the owner to **audit-only: the plan applies, the cover
tables stay as a tripwire that records and warns, nothing is refused**. That is a decision about the
**content** refusals: the cover tables (`allowedTypes`, `grantRules`), the delete/replace refusal
(`guard.go`, `TestApplyRefusesADeleteNoOwnerListApproves`), the blocked cost attribute (D62,
`TestGateRefusesABlockedCostAttribute`), the removal rule, and the budget evidence. The **integrity**
refusals are not judgements about what the plan does, and D1549 (answered 2026-11-07) keeps them: a saved plan
that changed after the gate (`TestApplyRefusesASavedPlanThatChangedAfterTheGate`), a creation fact that does
not say managed, environment injection; and, **by name** because they are the most dangerous refusals and the
first draft left them unclassified: the **managed configuration rule** in `internal/infra/tf/managed_cover.go`
(no provisioner, only Google providers, no data source, no upload from a local path, a provider block that
cannot redirect an endpoint, project or billing account), the **pre-init refusal** of `backend`, `cloud`,
`provider_meta`, `ephemeral` and `action` blocks (`TestApplyRefusesTheBlocksThePlanCannotShow`), the refusal of
**an address named on the command line** (`TestApplyRefusesAnAddressTheCommandLineNamed`), the symlink and
nesting bounds (`preflight.go`), the deployer-impersonation checks, and the marker checks in
`internal/managed/budget.go` (an **unreadable** marker, an **unknown version**, a marker that names **another
project**), which must **fail closed**. All of them stay refusals and are in
`TestIntegrityRefusalsStillRefuse` (plan T9).

**F10 — Everything that leans on the refusal** (these all change or lose their enforcement point under
D1545; each is a plan task or an open tension):
`CLAUDE.md` (the HARD RULE paragraph: the skill "never works around the refusal of cloud writes in a coding agent's session"),
`.claude/rules/terraform.md` ("`cover.go` decides what one `fugaro update` confirmation covers… Never widen
those tables to make a plan pass"; "IAM, the cover tables and the credentials service's grants are
security-sensitive. Get an independent review"; "Never run … `fugaro update`… from an agent run"),
`.claude/rules/credentials-service.md`, `SECURITY.md` (managed-project statements), `docs/design/managed-projects.md`,
`docs/design/managed-projects-spec.md`, `docs/managed-agent-converge/*`, `docs/console-managed-screens/*`,
`docs/design/image-refresh.md`, `plugin/skills/upgrade` ("never works around the refusal of cloud writes"),
`plugin/setup_skill_test.go` (asserts the managed gate is open), `internal/managed/*_test.go` (the
`TestGateRefuses…` and `TestApplyRefuses…` families), `internal/initflow/stage.go`
(`CanConfirmManaged`, `GateConfirms`, `Managed.Removes`, the `Typed` class and D1026 which pins it),
`internal/cli/managed_gate.go`, `doctor_managed_audit.go`. The workflow in `.claude/rules/terraform.md`
"Never run terraform apply, fugaro setup, fugaro update … from an agent run" is about a **cloud run's
job** (no credentials), not the managed gate, and is unchanged; spec §4's "agents may run … updates"
concerns a coding agent's session on the owner's machine.

**F11 — The meta repo is not built.** The safety net the spec cites for removing prompts ("the history is
kept and the project can be rebuilt") does not exist. What exists: the plan is printed, the managed
apply writes an `AuditRecord` (`internal/managed/audit.go`) to the project's bucket and `doctor` reads
it. D1546: not a prerequisite.

**F12 — Enforcement is buildable.** `NewRootCmd` constructs the whole command tree. A walker can set
every leaf's stdin to a closed pipe and run it against `internal/gcpfake`/`testutil` fakes under a short
timeout, failing on a hang; flags the command needs to reach its first read must be supplied per
command, so each leaf has a fixture or an explicit, listed exemption (a leaf that never reads stdin needs
none). The static half is a test that fails on any use of `os.Stdin`, `cmd.InOrStdin()`, `bufio.NewReader/
NewScanner` over stdin or `term.ReadPassword` outside the two sanctioned helpers (D1547).

**F13 — Agent detection is a mitigation, not a barrier.** `CanConfirmManaged`'s own comment says the
agent marker is "a mitigation against the accident, not a barrier against the adversary". Consequence for
D1542: "owner session only" for a cap raise is a fence against the loop that raises its own cap by
accident, and is exactly as strong as `agentMarker` — it does not stop a hostile process with the owner's
credentials. The spec's "budget caps are the only spending limit" therefore has the same limit.

**F14 — Value entry with no terminal.** `NeedsTerminal` already produces a refusal naming what needs a
terminal; D1540 turns each value reader into one that names the flag or environment variable that
supplies the value, and fails at once (no read attempted when stdin is not a terminal).

**F15 — The audit record is not tamper-proof, and warnings reach only the acting session.** `AuditRecordFor` /
`WriteAudit` (`internal/managed/audit.go`) write the record with the operator's own credentials to the project's
bucket, which is not versioned. Under audit-only the record and the warning are the whole control: whoever
applies can delete the record, and the warning prints to the session that applied. Consequence: T2 states it;
plan T9 adds bucket versioning or an append-only object naming for the record (a Terraform change in the
installation module) and sends the warning to `doctor` as well as to the session — or the owner accepts the
weaker record (T2).

**F16 — Every form of loosening a cap, not only `budget set`.** `budget set` raising a cap (`confirmTyped`),
`budget resume` (`confirmTyped`), the policy/settings ceilings `PerRunUSD`/`PerDayUSD` (the `Typed` class today:
`settings_write.go`, `update_managed.go`, the comment there says a write that loosens a ceiling takes the `Typed` class (D846)), and a
direct `/config` write by a budget admin through Firebase IAM (`deploy/terraform/gcp/modules/firebase`'s
`iam.tf`) all raise what runs may spend. D1542 covers the first; the others lose their only fence when the
`Typed` class goes (T12) with no replacement. Consequence: plan T3 lists every raise form, and **D1552** (answered 2026-11-07) asks what the policy-ceiling loosening becomes.

**F17 — "Unmanaged refuses" and the HARD RULE.** `fugaro update` is the whole upgrade; under D1541 every
unmanaged installation's `update` refuses until its owner runs the flag command, which today needs `--budget`
and `--budget-currency` (`managed_flag.go`) — an operator step that the HARD RULE forbids unless `update`
absorbs it. Also undefined: what `fugaro setup` does (always create managed? an existing unmanaged GCP project?),
and whether flagging is still refused in an agent session (`managed_flag.go`). Under audit-only the flag is the
**one-way door** whose guards would be removed. Consequence: **D1553** (answered 2026-11-07).

**F18 — The catalog check job loses its only build bound.** The nightly job (`internal/cli/image_check_catalog.go`,
`MaxImageBuilds`; `deploy/terraform/gcp/modules/installation/catalogcheck.tf`) runs as an account with no
project role and bounds itself by that count alone. T10 removes the count; the job then submits builds with no
bound. Consequence: the job takes a lease too (same estimator, its own per-run cap) or T10 keeps a bound for it;
the plan states the first and flags it (D1554, answered 2026-11-07).

**F19 — A build lease has no ledger path today.** Leases are keyed `runs/<slug>/<run>` in the budget database and
enforced by the Firebase rules (`internal/budget/rules`, the rules' emulator tests). A CLI that leases for a
build needs: a rules change, a generation bump with an `update` migration (HARD RULE), an emulator test, a named
writer of the lease (the CLI, as the launching identity), the per-run cap it counts against, the settle/release
on completion, and what happens when the CLI dies mid-build (the lease expires and is reclaimed, never held
forever). The estimate is client-side only: a direct `gcloud builds submit` bypasses it (T2). Plan T8.

**F20 — Trust probes.** (a) "Owner session" is identified only by environment markers (`internal/agentenv`:
`CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT`, …); unsetting the markers (`agentenv.Markers` has six, five of them agent markers; `env -u CLAUDECODE -u
CLAUDE_CODE_ENTRYPOINT …` strips two, a person or loop that strips all five and `CLAUDE_CODE_SSE_PORT` defeats it) or a
direct `/config` write defeats it, and today `budget set --yes` has **no** agent check at all — D1542 *adds* a
fence, a courtesy one. (b) Audit-only lets a hostile agent apply an out-of-table IAM grant (the `admins` /
`budget_admins` tfvars feed `iam.tf`), delete objects outside D1551's list, and then erase the record (F15). (c)
The lease estimate is client-side and bypassable. These are accepted limits of the owner's rulings and are
written into the plan's review focus.

**F21 — The `Typed` class fences more than money ceilings.** `update_managed.go` `workspaceStage.gateStep` makes a
publish that adds a workspace member or allow-list entry (`p.widens`) `Typed` — "never by a mark"; `init_credentials.go`
(the credentials-service opt-in) and `init_compute.go` are `Typed` too; `update_workspace.go` asks
(`confirmRemoval`, `confirmTypedStrict`). D1552 replaces only the `PerRunUSD`/`PerDayUSD` fence, so after T12 an agent
session admits workspace members and switches on credential minting with no fence at all. Consequence: **D1555**
(answered, recommended-delegated 2026-11-07: the same owner-session-only fence as D1552, for every `Typed` step that
is not a money ceiling) and plan T15; T12 is blocked on it.

**F22 — The lease must not look like a run.** A build lease under `runs/<slug>/<run>` would be read as a run by every
record reader (`runstore`, `watch`, the console). It gets its own key (`buildleases/<slug>/<id>`), its own rules path
and its own writer scoping (F19, T8). Separately, the budget rules admit a writer only with a **run token** whose
`fs`/`fr` claims match `$slug/$run`; the CLI as a launching identity and the catalog check service account have no
such token, so T8 must scope their writer and prove with an emulator test that it cannot touch another run's
reserved/released or `config/caps`.

## 3. Decisions D1540–D1559

D1540–D1559 are reserved for this work (highest in use on `main` and in open pull requests on
2026-11-07 was D1539). D1540–D1554 are all answered (2026-11-07).

| # | Question | Recommendation and reason | Alternative and its cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D1540** | What happens to value entry (hidden secrets, billing-account/name choice, pairing code) when every confirmation goes? | Values stay at a terminal and never hang: with no terminal the command fails at once naming the flag or environment variable that supplies the value. A secret is not a confirmation and cannot be defaulted. | Remove every read: each value from flag/env/file only; the wizard and `accounts add` login lose their guided flow. | **2026-11-07: as recommended** — the spec's "never hangs" and a value that only a person can type are reconciled by failing fast. |
| **D1541** | What are unmanaged projects under "managed projects only"? | A mutating command on a project that is not managed refuses and prints the one command that flags it managed, the GCP project ID given as an **argument** (consent by argument, not by typing at a prompt). | Auto-manage every project (acts on a project nobody declared); keep unmanaged with prompts (contradicts the spec). | **2026-11-07: as recommended** — an explicit, one-time declaration without a prompt. |
| **D1542** | Who may raise a cap? | Owner session only: in an agent session `budget set` raising a cap refuses (not a prompt) and prints the exact command for the owner; everything else stays open to agents. | Anyone, as spec §4 says (an agent can lift its own cap); an agent may raise up to an owner-set ceiling (a second number). | **2026-11-07: as recommended** — a deliberate exception to spec §4: otherwise the cap is not a limit (F8, F13). |
| **D1543** | How do image builds join the budget? | A build estimates its cost (build timeout × machine rate, an upper bound) and leases it from the same daily/per-run caps; over the cap it fails with the spec's error. The build-count bound (`ImageBuilds`, D1013) is removed. | Keep the count and add the lease (the spec says no special handling for builds). | **2026-11-07: as recommended** — one budget; consequences F5 (estimator, marker format change). |
| **D1544** | What happens to `--yes`, `--non-interactive` and `--ask`? | Remove all three (pre-1.0, no back-compat). Proposed detail if `--ask` is kept: opt-in per invocation, owner terminal only, never an agent session or `--json`; lint exempts only the `--ask` helper and the value-entry helper. | Keep as no-ops (dead flags suggest a prompt exists). | **2026-11-07: NOT the recommendation** — "We should honor the --ask and prompt if told to do so"; and yes to "`--ask` only, nothing else survives". Tension T1. |
| **D1545** | Do the managed gate's refusals stay? | Refusals stay (admitted plans apply silently and are audited; a plan outside the cover tables fails with the fix named). | Remove the gate entirely (every rule that leans on it loses its enforcement point, F10). | **2026-11-07: NOT the recommendation** — "Remove the gate", scoped by the owner's follow-up to **audit-only**: the plan applies, the cover tables stay as a tripwire that records and warns (`AuditRecord`, `doctor` flags out-of-table grants), nothing is refused. Tensions T2, T3. |
| **D1546** | Is the meta repo a prerequisite? | No: the printed plan and the audit record are the safety net until it lands. | Prerequisite (delays everything behind a feature with no plan). | **2026-11-07: as recommended** (answered; the independent review wrongly read it as open). |
| **D1547** | How is "no prompt left" enforced? | A harness that runs every registered command with stdin closed and a short timeout against fakes, plus a static lint failing any confirmation-or-value read of stdin outside the sanctioned helpers (an AST rule, with a data-read exemption). | The closed-stdin test only (misses a prompt in a branch the harness does not reach). | **2026-11-07: as recommended** (answered; the independent review wrongly read it as open). |
| **D1548** | In observe mode, does an over-cap image build proceed? | Follow the mode, as jobs do: in observe the estimate is recorded and the build proceeds; in enforce it fails with the spec's error. **An absent mode is observe** (`internal/budget/model.go`: "anything else, absent included, behaves as observe"), so a default installation plus D1543 has **unbounded builds**; in observe only `maxReserve` and the kill switches bite. `doctor` says so (a check `internal/cli/doctor_build_budget.go`, new, test `TestDoctorWarnsObserveWithUnattendedBuilds`, plan T8). | Builds always enforce (a second meaning of "mode"). | **2026-11-07: as recommended.** |
| **D1549** | Do the integrity refusals stay under audit-only? | They stay: saved plan changed after the gate, creation fact not managed, environment injection, the managed configuration rule (`managed_cover.go`), the pre-init block refusal, an address named on the command line, symlink/nesting bounds, impersonation, and the marker checks in `internal/managed/budget.go` (unreadable marker, unknown version, another project's: fail closed). Only the content refusals become record-and-warn. | Audit-only for everything. | **2026-11-07: as recommended.** The owner named the saved-plan, creation-fact and environment-injection refusals and then the managed configuration rule, the pre-init block refusal and the address-on-the-command-line refusal; what remains for the owner to read is the symlink/nesting bounds, the impersonation checks and the marker checks (recommended; owner delegated reading to the PR, 2026-11-07). |
| **D1550** | Where does the build cost rate come from? | A small table in the repo keyed by machine type, an upper bound; no billing call. | A live Cloud Billing lookup (a network read and an IAM grant on the launch path). | **2026-11-07: NOT the recommendation** — the rate is **queried from Cloud Billing at run time**: a new network call and a new IAM grant on the launch path; a missing or unknown price **refuses**, never defaults. Tension T5. |
| **D1551** | After D1545, what do delete/replace do for **data-bearing types**? | The owner said *types*, so every entry of `allowedTypes` (`cover.go`) is classified, and a type that is not classified, including one added later, **refuses delete/replace**. Data-bearing (refuse): `google_storage_bucket`, `google_storage_bucket_object`, `google_secret_manager_secret`, `google_firebase_database_instance` (the budget ledger), `google_artifact_registry_repository`, `google_logging_project_bucket_config`, `google_logging_log_view`, `google_service_account` (an identity whose keys and grants are lost), `google_apikeys_key`. Not data-bearing (apply, recorded and warned): the `*_iam_member` types, `google_cloud_run_v2_job`/`_service`, `google_cloud_scheduler_job`, `google_compute_firewall`/`_router`/`_router_nat`, `google_monitoring_*`, `google_billing_budget`, `google_project_iam_custom_role`, `google_project_service`, `google_logging_project_sink`/`_exclusion`, `google_firebase_project`. Secret **versions** and Firestore are not in the table today (Firestore is not deployed), so they are unclassified and refuse. | Everything applies; or a five-name denylist (a new type is open by default). | **2026-11-07: as recommended** (the review replaces the denylist by a classification of every type; recommended; owner delegated reading to the PR, 2026-11-07). |
| **D1552** | What becomes of the policy/settings ceiling loosening (`PerRunUSD`/`PerDayUSD`, the `Typed` class today) when the class goes? | The same fence as D1542: owner session only, refused with the command in an agent session; the direct `/config` write stays an IAM matter (`budget_admins`). | No fence (the cap is then a courtesy); a typed prompt (contradicts the spec). | **2026-11-07: as recommended** — the same owner-session-only fence as cap raising, so the Typed class can go without leaving a ceiling unfenced. |
| **D1553** | What does "Fugaro supports managed projects only" do to `setup`, `update` on an existing unmanaged installation, and flagging in an agent session? | `setup` always creates managed; `update` on an unmanaged installation flags it itself when given its budget once (no second command); flagging is refused in an agent session. | `update` refuses until the owner runs `managed flag` (breaks the HARD RULE). | **2026-11-07: as recommended** — setup always creates managed; update flags an unmanaged installation itself given its budget once (no second command, HARD RULE); flagging is refused in an agent session. |
| **D1554** | Does the catalog check job take a lease or keep a build bound? | A lease through the same estimator with its own per-run cap. | Keep a count for that job only (a second mechanism, "no special handling for builds"). | **2026-11-07: as recommended** — a lease through the same estimator with its own per-run cap, so the nightly job keeps a bound without a second mechanism. |
| **D1555** | What fences the `Typed` steps that are not money ceilings (workspace member/allow-list publish `p.widens`, the credentials-service opt-in, `init_compute`, workspace removal) once the class is deleted? | The same owner-session-only fence as D1552: refused in an agent session with the owner's command printed; no prompt at the owner's terminal. | No fence (an agent session admits members and switches on credential minting); a prompt (contradicts the spec). | **2026-11-07: as recommended; recommended; owner delegated reading to the PR.** |

## 4. Decisions the owner did not have to make (stated, not asked)

- Terraform stays non-interactive by the saved-plan route; `-auto-approve` is not added (F6).
- A refusal printed with the fix is not a prompt; a value reader that fails at once is not a prompt (F14).
- `fugaro` commands run in a cloud job's container are unchanged: the cloud run never runs `update`/`setup`.
- The spec's "agents may run all commands" is amended in four places, all one fence: raising a cap (D1542), loosening a policy ceiling (D1552), flagging a project managed (D1553) and the non-money `Typed` steps (D1555) — each refuses in an agent session and prints the owner's command.
- Release notes carry `### For operators`: "run `fugaro update`" plus what it will now do without asking (the HARD RULE in CLAUDE.md).

## 5. Tensions: the owner's rulings (2026-11-07)

The owner ruled T1–T4 and T6 on 2026-11-07 and held T5 open until the T8 spike (task T8S) reports. Each
ruling is written here and carried into [implementation-plan.md](implementation-plan.md).

**T1 (D1544 against the spec and D1547).** The spec says any prompt is a bug; the owner wants `--ask` honoured.
**RULED 2026-11-07: yes, as proposed.** `--ask` is the single sanctioned exemption — opt-in per invocation,
owner terminal only (never an agent session, never `--json`); the lint allows reads only in that helper and the
value-entry helper, and data reads (`--task-file -`) by an explicit exemption. `budget set` registers no `--ask`
today; a task adds it (T3).

**T2 (D1545 against the review and security rules).** Audit-only removes the refusal that
`.claude/rules/terraform.md` leans on ("a role granted to a new kind of principal is not covered until you add
it on purpose"; "Never widen those tables to make a plan pass"), and that the credentials-service rule and
`SECURITY.md` describe. Under audit-only a plan that grants an unexpected IAM role to an unexpected principal
applies. **The audit record is not tamper-proof** (written with operator credentials to an unversioned bucket,
F15) and the warning is printed only to the acting session, so a hostile agent can apply, then erase the record
(F20). The independent review of IAM, cover and credentials grants (in-run level 3) is the only pre-apply
control, and it reviews code, not each plan. **RULED 2026-11-07: choice (a)** — accept, harden the record
(versioning, `doctor` reads) and rewrite the rules to say the table is a tripwire (T9, T13); that level-3 review
of the IAM and cover code is the pre-apply control. T9 is therefore unblocked.

**T3 (D1549 and D1551, confirmation).** The owner named the first six integrity refusals; the plan keeps the
whole class listed in D1549, the marker checks of `internal/managed/budget.go`, and the D1551 type
classification. **RULED 2026-11-07: confirmed as listed** — the class stands whole, the owner having delegated
reading the rest to the PR.

**T4 (D1542, D1552 and F20).** The cap fence is as strong as `agentenv`'s markers: a courtesy against the
accidental loop, not against a hostile one. **RULED 2026-11-07: confirmed** — the limit is accepted and written
into the plan's review focus.

**T5 (D1550 against the "no billing read" assumption and the lease design).** The reconciliation, the plan and
the budget design assumed no billing read exists on the launch path. A Cloud Billing price lookup adds a network
call, an IAM grant to whichever identity launches a build (a new grant in a security-sensitive module), and a
failure mode on every build; **a missing or unknown price refuses** (never defaults). The lease estimate is still
client-side only (F19). **OPEN, held 2026-11-07:** the D1550 answer (the rate is queried from Cloud Billing at
run time) is held until the T8 spike (task T8S) reports, and T8 waits on the spike only. The spike decides the
mechanism before any grant is written; the grant's shape, if there is one, still takes the level-3 security
review (T2's pre-apply control).

**T6 (D1554 against D1548).** An absent mode is observe, and in observe a lease does not bite: on a default
installation the nightly catalog check job (D1554's lease) and every other build is **unbounded**; only
`maxReserve` and the kill switches bite. D1554 gives the job a lease, not a limit. **RULED 2026-11-07: choice
(b)** — `fugaro update` sets the mode to enforce on a managed project (observe stays an explicit choice), with
one `doctor` line saying so. T10 and the lease design follow it: the enforce-on-update step is its own task
(T16, one file group), with a converge test (HARD RULE: `fugaro update` is the whole upgrade; an installation at
the previous mode converges). The trigger cannot be an absent mode node: `internal/infra/firebasedb.go` **seeds**
`config/mode` = `observe` at install, so T16 records the owner's choice instead, and raises where there is none.

**Open items:** T5 above, until the T8S spike reports; whether `doctor`'s observe warning (D1548) is a
warning or an error; the meta repo's own design (out of this plan). Every decision D1540–D1555 is answered
(2026-11-07).

## 6. UNVERIFIED

- Whether any `gcloud` call exists outside `internal/backend/gcp` and without `--quiet`: **`catalog.go` has
  several that carry no `--quiet`** (for example `gcloud storage objects describe`, `gcloud auth
  print-access-token`), and they run **inside Cloud Build step scripts and the preload pipeline**, which a CLI
  harness cannot exercise; the question is settled by a static scan (T2), not by the harness.
- That every one of the 59 `stdinIsTerminal` call sites guards a confirmation and not a value or data read; T1's
  audit classifies each.
- The Cloud Build machine types and rates (D1550 now queries them from billing).
- Whether the marker format version can be moved without a console/mobile reader (the console reads the marker
  through the CLI only — not verified).
- **D1550's Billing grant.** That a Cloud Billing price read needs an IAM grant in `installation/iam.tf` is not
  verified: list prices need the Cloud Billing API enabled, not a project role; contract prices need a role on the
  billing account that a project module cannot grant; and the CLI runs on the person's own credentials, so the
  grant may not be needed at all. T8's first step is the spike T8S, which decides the mechanism before any
  grant is written; T5 stays open until it reports.
