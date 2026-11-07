# No-prompt policy — reconciliation

**Checked against** `origin/main` as of 2026-11-07 (the commit the worktree was cut from). Where this
file disagrees with [spec.md](spec.md), this file wins. The spec is the owner's, as received.

The spec is right about the principle and about what is wrong today, and it is written without the code
in view in four places that matter: it says Terraform should run with auto-approve (it already runs
non-interactively, and `-auto-approve` is forbidden on purpose), it says an image build should use "the
same budget as jobs" (jobs lease model spend; an image build is bounded by a **count**, not dollars), it
says "managed resources never need confirmation" (the managed gate has refusals that are not
confirmations), and it says agents may run every command (raising a cap is the one command where that
defeats the cap). The owner has answered every question below that needed an answer; two answers are
**not the recommendation** and open tensions (§5).

## 1. Headline: spec concept → what the repo has

| Spec concept | What the repo already has |
| --- | --- |
| "No command waits for input" | Fourteen confirmations and several value readers in `internal/cli` (F1); `stdinIsTerminal` decides terminal-or-not per call site, 58 call sites. |
| "Managed projects only" | A managed/unmanaged split: `internal/managed`, `internal/cli/managed_gate.go`, `managed_flag.go` (flagging is a typed-GCP-ID step); an unmanaged project keeps every prompt (F2). |
| "Agents may run all commands" | `agentMarker` / `initflow.Conditions.Agent`: a coding agent's session confirms nothing (`CanConfirmManaged`); on a managed project `GateConfirms`/`Removes` stand in for the prompt (F3). |
| "Budget caps replace cost prompts" | `internal/budget` leases **model spend** per run (`evaluate.go`, `ReasonMaxReserve`); `fugaro budget set` raises caps and asks a typed name; an image build is bounded by **`ImageBuilds`** (`internal/managed/builds.go`, `internal/infra/managed/marker.go` `MinImageBuilds`/`MaxImageBuilds`/`DefaultImageBuilds`), a count per `fugaro image refresh` (F5). |
| "Terraform with auto-approve" | `internal/infra/tf/tf.go` `Apply` runs `apply -input=false` on a **saved plan**; `-auto-approve` is refused by design (a comment names it). Exists (F6). |
| "gcloud with the quiet flag" | The two gcloud commands in the build step scripts already pass `--quiet` (`internal/backend/gcp/build.go`, `catalog.go`); no other `gcloud` call was found in the tree (UNVERIFIED as a whole, §6). |
| "Over-cap error: cost, cap, spent, one command" | Not present for builds; `budget` has `Reason*` codes (`internal/budget/evaluate.go`) and an observe mode in which caps do not bite (F7). |
| "Tests: every command, stdin closed" | None. `NewRootCmd` (`internal/cli/root.go`) builds the tree, so a walker is possible (F12). |
| "Fugaro project repo (meta repo)" | Designed, not built; the audit record (`internal/managed/audit.go`, read by `doctor_managed_audit.go`) is what exists (F11). |

## 2. Findings

**F1 — The prompts.** Confirmations (each a typed name, a typed GCP project ID or a y/N):
`confirmTyped` / `confirmTypedStrict` (`budget.go`; `budget set` raising a cap, `kill`), `confirmBuild`
and `buildNotConfirmable` (`image.go`), `confirmTypedGCPProject` (`managed_flag.go`),
`confirmSettingsWrite` (`settings_write.go`), `managedAsk` (`managed_apply.go`), and readers in
`init.go`, `init_firebase.go`, `init_fugaroyaml.go`, `init_inputs.go`, `init_plugin_stage.go`,
`init_project.go`, `init_repo_gate.go`, `accounts_*`, `config_init.go`. **Value entry**, which is not a
confirmation: hidden secret values (`secrets.go`, `term.ReadPassword`), the billing-account choice
(`billing_prompt.go`), the pairing code (`pair.go`). Consequence: the spec's two statements "no command
waits for input" and "never hangs on closed stdin" are the same for confirmations and different for
values — D1540.

**F2 — Unmanaged projects exist and are the prompt's last home.** The code serves a project that is not
managed with every confirmation; the spec says Fugaro supports managed projects only. Consequence: the
unmanaged case needs a defined behaviour that is not a prompt — D1541.

**F3 — The confirmation machinery is a class system.** `initflow.ConfirmClass` has `Typed` (money and
permanence: no flag, a terminal only, never an agent) and `YesOrTyped`. `CanConfirmManaged`,
`GateConfirms`, `ConfirmsByGate`, `Managed.Covers/Removes`, `Conditions{Terminal, Agent, Yes,
NonInteractive, JSON, Ask}` and `NeedsTerminal` are the decision, and `agentMarker` is the agent
detector. The owner's earlier rulings are in force and consistent with the spec: no prompt on a managed
project, terminal or agent (memory *feedback-no-prompts-on-managed-project*; #1845 removed the typed
policy prompt for a person; NP3 extended it to image build). What survives today even on a managed
project is the `Typed` class — exactly "money and permanence" — and the `--ask` reversal (`Conditions.Ask`,
`GateConfirms` answers false under it). Consequence: removal is a deletion of the class system's
confirm paths, last, after every caller has stopped asking (plan T12).

**F4 — The flags.** `--yes` (`accounts.go`, `accounts_add.go`, `accounts_rm.go`, `budget.go`,
`config_init.go`, `image_refresh.go`, `init.go`), `--non-interactive` (`init.go`), `--ask`
(`accounts_add.go`, `image_refresh.go`, `init.go`), and their text in plugin skills
(`plugin/skills/{setup,upgrade,working,parallelism}`), docs and `plugin/` tests that check every flag a
skill names against the CLI. Consequence: removing a flag is a skill/doc/test change in the same task
(CLAUDE.md "plugin" paragraph) — D1544.

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
`plan -input=false -detailed-exitcode -out=…`, `apply -input=false <planfile>`. The comment above
`Apply` says why `-auto-approve` is never added: applying the exact saved plan the gate saw is the
equivalent, and a flag-based apply could apply a different plan. The spec's "auto-approve" is
**Exists** under another form and must not be implemented literally.

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
impersonation. The owner's D1545 answer is "remove the gate", scoped by the owner to **audit-only: the plan
applies, the cover tables stay as a tripwire that records and warns, nothing is refused**. That is a
decision about the **content** refusals (cover tables, delete/replace, cost attribute, removal rule, budget
evidence). The **integrity** refusals (saved plan changed after the gate, creation fact not managed,
environment injection, symlink and nesting bounds, impersonation) are not judgements about what the plan
does; whether they stay is D1549, **pending**.

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

## 3. Decisions D1540–D1559

D1540–D1559 are reserved for this work (highest in use on `main` and in open pull requests on
2026-11-07 was D1539). D1548 onward are **pending**; none is decided by the plan.

| # | Question | Recommendation and reason | Alternative and its cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D1540** | What happens to value entry (hidden secrets, billing-account/name choice, pairing code) when every confirmation goes? | Values stay at a terminal and never hang: with no terminal the command fails at once naming the flag or environment variable that supplies the value. A secret is not a confirmation and cannot be defaulted. | Remove every read: each value from flag/env/file only; the wizard and `accounts add` login lose their guided flow. | **2026-11-07: as recommended** — the spec's "never hangs" and a value that only a person can type are reconciled by failing fast. |
| **D1541** | What are unmanaged projects under "managed projects only"? | A mutating command on a project that is not managed refuses and prints the one command that flags it managed, the GCP project ID given as an **argument** (consent by argument, not by typing at a prompt). | Auto-manage every project (acts on a project nobody declared); keep unmanaged with prompts (contradicts the spec). | **2026-11-07: as recommended** — an explicit, one-time declaration without a prompt. |
| **D1542** | Who may raise a cap? | Owner session only: in an agent session `budget set` raising a cap refuses (not a prompt) and prints the exact command for the owner; everything else stays open to agents. | Anyone, as spec §4 says (an agent can lift its own cap); an agent may raise up to an owner-set ceiling (a second number). | **2026-11-07: as recommended** — a deliberate exception to spec §4: otherwise the cap is not a limit (F8, F13). |
| **D1543** | How do image builds join the budget? | A build estimates its cost (build timeout × machine rate, an upper bound) and leases it from the same daily/per-run caps; over the cap it fails with the spec's error. The build-count bound (`ImageBuilds`, D1013) is removed. | Keep the count and add the lease (the spec says no special handling for builds). | **2026-11-07: as recommended** — one budget; consequences F5 (estimator, marker format change). |
| **D1544** | What happens to `--yes`, `--non-interactive` and `--ask`? | Remove all three (pre-1.0, no back-compat). | Keep as no-ops (dead flags suggest a prompt exists). | **2026-11-07: NOT the recommendation** — keep `--ask`: prompt only when the owner passes it, per invocation, owner terminal only; `--yes` and `--non-interactive` are removed. The lint exempts exactly two helpers: the `--ask` helper and the value-entry helper. Owner: "We should honor the --ask and prompt if told to do so". Tension T1. |
| **D1545** | Do the managed gate's refusals stay? | Refusals stay (admitted plans apply silently and are audited; a plan outside the cover tables fails with the fix named). | Remove the gate entirely (every rule that leans on it loses its enforcement point, F10). | **2026-11-07: NOT the recommendation** — "Remove the gate", scoped by the owner's follow-up to **audit-only**: the plan applies, the cover tables stay as a tripwire that records and warns (`AuditRecord`, `doctor` flags out-of-table grants), nothing is refused. Tensions T2, T3. |
| **D1546** | Is the meta repo a prerequisite? | No: the printed plan and the audit record are the safety net until it lands. | Prerequisite (delays everything behind a feature with no plan). | **2026-11-07: as recommended.** |
| **D1547** | How is "no prompt left" enforced? | A harness that runs every registered command with stdin closed and a short timeout against fakes, plus a static lint failing any stdin read outside the sanctioned helpers. | The closed-stdin test only (misses a prompt in a branch the harness does not reach). | **2026-11-07: as recommended.** |
| **D1548** | In observe mode, does an over-cap image build proceed? | Follow the mode: in observe the estimate is recorded and the build proceeds, `doctor` warns that observe plus unattended builds has no stop; in enforce it fails with the spec's error. | Builds always enforce, whatever the project's mode (a second meaning of "mode"). | *(pending)* |
| **D1549** | Do the **integrity** refusals (saved plan changed after the gate, creation fact not managed, environment injection, symlink/nesting bounds, impersonation) stay under audit-only? | They stay: they are not judgements on what a plan does, they protect that "the plan the gate saw is the plan applied". Only the content refusals (cover tables, delete/replace, blocked cost attribute, removal rule, budget evidence) become record-and-warn. | Audit-only for everything (the saved-plan check and env-injection defence go too). | *(pending)* |
| **D1550** | Where does the build cost rate come from? | A small table in the repo keyed by machine type, an upper bound, tested against the published build config; no live billing call. | A live Cloud Billing lookup (a network read in a no-prompt path; a second failure mode). | *(pending)* |
| **D1551** | After D1545, what does a plan that touches **delete/replace** do on an installation with data (state bucket, Firestore, secrets)? | Applies, with the delete recorded and warned; the guard (`internal/infra/tf/guard.go`) keeps a **hard list** of objects whose delete is never applied (state bucket, runs bucket, Firestore database, secrets). | Everything applies (a bad plan can delete the project's history). | *(pending)* |

## 4. Decisions the owner did not have to make (stated, not asked)

- Terraform stays non-interactive by the saved-plan route; `-auto-approve` is not added (F6).
- A refusal printed with the fix is not a prompt; a value reader that fails at once is not a prompt (F14).
- `fugaro` commands run in a cloud job's container are unchanged: the cloud run never runs `update`/`setup`.
- The spec's "agents may run all commands" is amended in exactly one place: raising a cap (D1542).
- Release notes carry `### For operators`: "run `fugaro update`" plus what it will now do without asking (the HARD RULE in CLAUDE.md).

## 5. Open tensions and open items for the owner

**T1 (D1544 against the spec and D1547).** The spec says any prompt is a bug and review rejects any
confirm dialog; the owner wants `--ask` honoured. Resolution proposed in the plan (needs the owner's
word): `--ask` is the single sanctioned exemption — opt-in per invocation, owner terminal only (never an
agent session, never `--json`), and the lint allows reads only in that helper and the value-entry helper.
The review rule and CLAUDE.md say so by name. *Held back:* nothing, if confirmed; if the owner would rather
drop `--ask` the lint loses one exemption.

**T2 (D1545 against the review and security rules).** Audit-only removes the refusal that
`.claude/rules/terraform.md` leans on ("a role granted to a new kind of principal is not covered until you
add it on purpose"; "Never widen those tables to make a plan pass"), and that the credentials-service rule
and `SECURITY.md` describe. Under audit-only a plan that grants an unexpected IAM role to an unexpected
principal applies; the record and the warning are after the fact. The independent review of IAM, cover
and credentials grants (in-run level 3) is then the only pre-apply control, and it reviews code, not each
plan. The choices: (a) accept, and rewrite the rules to say the table is a tripwire (plan T13); (b) keep
the refusal for IAM grants only (the plan's T9 would then keep `grantRules`'s refusal and drop the rest;
this is the option the owner's earlier alternative offered and declined). *Held back:* the plan's gate
task (T9) states the answer as the owner gave it (audit-only) and writes (a); it needs the owner's word
that (a) stands after reading this.

**T3 (D1545 against D1549/D1551).** "Nothing is refused" read literally also removes the delete/replace
guard and the saved-plan-changed check. D1549 and D1551 are pending and are the owner's to rule; until
then the plan's T9 changes only the content refusals named in D1549 and keeps the guard's hard list.

**T4 (D1542 against spec §4 and D1544's `--ask`).** A cap raise in an owner terminal needs no prompt and no
`--ask`; in an agent session it refuses. An owner who has `--ask` set wants the raise asked too. Proposed:
`--ask` applies to the raise like any other command; nothing else changes.

**Open items:** D1548–D1551 above; the meta repo's own design (out of this plan); whether `doctor`'s observe
warning (D1548) is a warning or an error.

## 6. UNVERIFIED

- That no `gcloud` call other than the two build-step scripts exists outside the paths searched
  (`internal/backend/gcp`); a repository-wide search for `exec.Command("gcloud"` and shell scripts was
  not run to completion. The plan's T2 harness and T14 audit settle it.
- That every one of the 58 `stdinIsTerminal` call sites guards a confirmation and not a value read; T1's
  audit classifies each.
- The Cloud Build machine rate and the timeout the project's build config uses (D1550's table) were not
  read; T8 reads them from the build config in `internal/backend/gcp/build.go`.
- Whether the marker format version can be moved without a console/mobile reader (the console reads the
  marker through the CLI only — not verified here).
