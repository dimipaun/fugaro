# Always a budget: reconciliation

Checked against `origin/main` at `fb0aeb9aa` on 2026-11-20. **Where this file disagrees with
[spec.md](spec.md), this file wins.** It cites paths and symbols, never line numbers. Decisions are
**D3020–D3022**, all answered by the owner on 2026-11-20. It carries out **D3009, D3010 and
D3011** of [every model key through the credentials service](../keys-through-service/reconciliation.md),
and it reverses the part of **D2071** that guards a mode that no longer exists.

## 1. What the spec asks for, and what the repository already has

| The spec's concept | What the repository has |
| --- | --- |
| "Budget mode off" | `policy.ModeOff`, with the aliases `config.BudgetOff` and `localcfg.BudgetOff`. About 32 Go files outside tests read it |
| "Unset" | Read as off by the runner and the local config, and as observe by the settings registry and the console (F2) |
| "The budget database" | A Firebase Realtime Database in the installation's own project, built only when `--firebase-project` is given (F3) |
| "A run's identity" | A token the launcher mints and a session the run opens in the database (`internal/cli/launch_budget.go`, `internal/runner/budget_session.go`) |
| "A run without the gateway" | `runner.GatewayOn` false: the key goes into the agent's own environment (`internal/agent/env.go`) |

## 2. Assumptions

- **A1.** `observe` and `enforce` keep their meaning. Nothing here changes a cap, a default cap or
  what `enforce` refuses (D3010).
- **A2.** The kill switch's own "off" (`internal/budget/kill.go`) is a different word for a
  different thing and is untouched.
- **A3.** `fugaro troubleshoot` on the developer's machine starts no gateway and reads no mode, and
  is unchanged. `fugaro exec` is the container's entry point, not a local command, and follows the
  run.
- **A4.** The work ships in one CLI release (R1).

## 3. Findings

**F1 — Rule: off is the default.** `--budget-mode` defaults to unset, a setup with no
`--firebase-project` builds no budget backend and writes no budget block, and every merge of an
empty mode gives off at the run. Consequence: the migration reaches most installations, not a few.

**F2 — Wrong against itself: unset has two meanings.** The local config's `BudgetMode` method,
`spendFromEnv` (`internal/runner/spend.go`) and `resolvePolicy` (`internal/runner/policy.go`) read
unset as off. `internal/settings/registry.go`, `schemas/settings.schema.json` and the console say
unset acts as observe. The database's own mode (`budget.PathMode`) has only observe and enforce.
Consequence: R3 makes the second reading true everywhere.

**F3 — Gap: the database is optional.** The Firebase stage is skipped unless `--firebase-project`
is given (`newFirebaseStage`, `internal/cli/init_stages.go`), on `setup` and on `update` alike.
`var.enable_budget` gates the backend's Terraform in `modules/installation`. The value of
`--firebase-project` must be the installation's own project; any other is refused
(`checkFirebaseProject`, `internal/cli/init.go`). Consequence: the stage can run with no flag (AB7),
and the gate goes (AB6).

**F4 — Rule: what creating the database needs.** `initFirebase` (`internal/cli/init_firebase.go`)
checks that the project is active and has billing (`infra.CheckFirebaseProject`), reads the
project's owners and editors as the budget's admins, applies the Firebase root (a database fixed
in `us-central1`, a web key, a signer account, a custom role and project IAM grants), writes the
rules and the mode over REST, and records the result in the local config (`localcfg.Budget`).
Nothing links billing. The Firestore location is confirmed only by a person at a terminal, and a
piped run leaves that step undone, as today. Consequence: R2.

**F5 — Rule: creating it takes more than an operator holds.** The Firebase root creates a custom
role, project IAM grants and service accounts. No code gates the stage on ownership today, and the
repository's own messages disagree on who runs it ("an operator" in `launch_budget.go`, "an
owner" in `init_new_machine.go`). Whether an operator's roles suffice is UNVERIFIED (U1).
Consequence: R2 probes instead of assuming.

**F6 — Rule: "none" and "unknown here" are different.** The backend is recorded only in the local
config; an empty `RTDBURL` means none. Only the new-machine path tells "no backend" from "not
known on this machine" (`newMachineBudget`, `internal/cli/init_new_machine.go`, which reads the
installation's outputs and whether the Firebase state exists). Consequence: AB7 uses that probe
on every update, so a second machine adopts the database and never builds a second one.

**F7 — Rule: the identity.** `budgetBackendOn` mints when the mode is not off and a database is
configured; `budgetMintOn` also mints with the mode off when a policy is published; with no
database nothing mints. `startBudgetBackend` returns before it opens a session when spend is off.
Consequence: AB4 and AB1a make both unconditional.

**F8 — Rule: every run comes to depend on the database.** A run that cannot reach the database at
its start retries until its grace runs out and then halts (`HaltBudgetUnavailable`); mid-run the
gateway answers 503, spends the lease it holds, and halts after the grace (`internal/budget/grace.go`).
That holds for observe too: the budget design's D14 says off is the only mode that ignores the
backend. Consequence: stated as O2. It cannot be softened here, because the run's identity is the
database's.

**F9 — Rule: Cloud Batch.** `internal/backend/batch/run.go` refuses any launch that carries a
sealed token, because Batch has no per-execution place for the key
(`docs/design/budget-token-channel.md`). The backend is wired (`doctor_batch.go`,
`validate_batch.go`, `docs/backends.md`) and its live Check 124 has never been run. Once every
launch mints, Batch can launch nothing. Consequence: D3021, AB9.

**F10 — Rule: a model with no price.** With a gateway, `CheckPins` (`internal/config/pins.go`)
refuses a model that cannot be priced, an alias and an empty pin, and the fix it names is a
`model_prices` entry. Prices come from the embedded table (`internal/pricing/table.go`), the
provider catalog and the project's `model_prices`. Mode off skipped the check. Consequence: D3020.

**F11 — Rule: Vertex.** `VertexBudgetRefusal` (`internal/runner/gateway.go`) refuses Vertex under
`enforce` and tells the reader to "use budget.mode observe or off". Under observe a Vertex run goes
through the gateway and its pins are priced. `config.DefaultAgentAuth` is `vertex`, so a default
repository's runs start going through the gateway. Consequence: the text changes (AB1b), and such a
repository is in AB8b's listing if its pin has no price.

**F12 — Rule: a repository's own file.** `policy.ValidMode` and `modeRank`
(`internal/policy/merge.go`) rank off below observe, and the higher rank wins, so a project that
says observe already overrides a file that says off. `schemas/fugaro.schema.json` lists off. The
precedent for writing into a checkout is `prepareAnchor` and `writeAnchor`
(`internal/cli/init_fugaroyaml.go`): a diff, written with `--yes` or by a person at the terminal,
printed otherwise. Consequence: D3022, AB2, AB8b.

**F13 — Rule: the job says its mode only when it is not off.** `budgetEnv` (`internal/infra/spec.go`)
writes `FUGARO_BUDGET_MODE` only then. A runner in an older image reads a missing entry as off.
Consequence: AB5 always writes the entry, so an older image under the new release runs as observe,
a path it already has. No image gate is needed (R6).

**F14 — Rule: formats that list off.** `schemas/fugaro.schema.json` (the repository's file),
`schemas/result.schema.json` (`policy.effective.mode`, in a record of version 1 that old runs
already wrote), and the published policy node, through `ValidMode` (`projectpolicy.Version` is
10). `budget.mode` in the record, `budget show --json` and `project show --json` never carried it.
Consequence: R4, R7.

**F15 — Rule: guards that die with the mode.** `refuseModeOffWhileTheServiceServes`
(`internal/cli/project_cmd.go`, D2071), `serviceSwitchModeRefusal` and the review-class refusal
(`internal/runner/gateway.go`, `routing.go`), the compression refusal
(`internal/runner/compression.go`), and the mode argument of `infra.CredentialsServiceServes`.
Consequence: AB8a, AB1b, AB15.

**F16 — Rule: precedents inside `update`.** The published policy is migrated in place
(`policyMigrationFor`, `internal/cli/init_policy_migrate.go`; `projectpolicy.Migrate`). The local
config is rewritten in place (`migrateCraftKeys`). `raiseBudgetMode`
(`internal/cli/update_budget_mode.go`) changes the database's mode and prints one line, and
`internal/cli/update_in_flight.go` prints the line a run in flight is owed. Consequence: AB8a and
AB8b follow them.

**F17 — Rule: documents that present off as a feature.** `README.md` ("off by default"),
`docs/how-to/budgets.md`, `docs/design/m9-budget-and-dashboard.md` (its rollback is
`--budget-mode off`), `docs/design/v1.md`, `docs/multi-model.md`, Check 47's row in
`docs/gcp-live-checklist.md`, `plugin/skills/setup/reference/decisions.md` and
`website/content/security.md`. No documents test was found that pins those sentences (U3).
Consequence: AB12, AB13.

**F18 — Rule: the cost.** The budget design's own estimate for 20 repositories and 100 runs a day
is about two dollars a month for the database, with Firestore and Auth in their free tiers, and
the pay-as-you-go plan is required. Consequence: stated in the release notes (AB12).

**F19 — Rule: the enterprise code.** `ee/ab/admit.go` maps an empty mode to off for its provider
check, and `ee/coordinator/cli/declare.go` skips declaring when the mode is off or there is no
database. Consequence: AB10.

## 4. Decisions

Inherited from the keys plan: **D3009** (budget mode off is removed; the owner's words are in
[spec.md](spec.md)), **D3010** (`observe` stays, and the migration lands on it) and **D3011** (this
spec).

Answered by the owner on 2026-11-20:

| # | Question | Answer | Mark |
| --- | --- | --- | --- |
| **D3020** | Under `observe`, what happens to a model the gateway cannot price (F10)? | **It is refused, with the fix**, in every mode. `fugaro update` and `doctor` list the repositories it will stop | **not the recommendation** (which was to run it under observe and mark its spend unpriced) |
| **D3021** | What happens to the Cloud Batch backend (F9)? | Every Batch launch is refused with one stated reason; `doctor` and the docs say so; a carrier for the token is a design of its own | as recommended |
| **D3022** | What does the release do with a repository's `fugaro.yaml` that says `off` (F12)? | **It is rejected at once**, with the fix | **not the recommendation** (which was to read it as unset for one release) |

**What D3020 and D3022 cost, stated once.** The brief's line 7 says nobody starts being refused by
a cap they did not choose. That still holds: no cap refuses anyone. But two things that ran under
off stop after the upgrade until a person acts: a run whose model has no price, and a run whose
committed `fugaro.yaml` says off. `fugaro update` names each such repository and the one-line fix
before it changes anything (R8).

## 5. Resolutions

- **R1. One release, and no release in the middle.** A no-database installation has no safe
  half-way state, so the work is not split across releases. No CLI release is cut while this plan
  is merged past its level 1 and I1 has not merged.
- **R2. `fugaro update` creates the database, and only where it can.** The Firebase stage runs with
  no flag, in the installation's own project. It first probes for a database that exists and is
  only unknown on this machine, and adopts it (F6). Where there is none, it probes whether the
  person can make the project's IAM changes (`testIamPermissions`). If they can, it creates the
  database, in `observe`. If they cannot, the update stops before it changes anything and says
  that a project owner must run `fugaro update` once; the installation keeps running as it was,
  on the release it was applied at. It asks nothing new. `--firebase-project` stays accepted with
  the one value it could already take.
- **R3. Unset means `observe`.** In the local config, the launcher, the job's environment (always
  written, F13) and the run.
- **R4. The policy node keeps its version.** `fugaro update` rewrites a published `off` to
  `observe` in place. Every node the new release writes is a valid version 10 node, so no reader
  of version 10 breaks. A new run that meets a node that still says off, because the update has
  not run, is refused with the fix. This is O1, for the owner to confirm against the rule that a
  removed value bumps a format's version.
- **R5. Runs in flight.** A run that started with the budget off has no session and holds its key
  already; nothing the update does reaches it, and it finishes. `fugaro update` says so in one
  line when runs are running.
- **R6. An older job image needs no gate.** The job's environment names `observe` explicitly, and
  an older runner has that path. A job whose root was not re-applied still has no entry and still
  runs as off on an older image, with the key it still mounts.
- **R7. Old records keep the word.** `schemas/result.schema.json` keeps `off` in
  `policy.effective.mode` for the records old runs wrote, and its description says nothing writes
  it any more.
- **R8. The update says what will stop before it changes anything.** One line per repository whose
  pinned model has no price (with the `model_prices` fix), per repository whose `fugaro.yaml` says
  off (with the removal offered as a diff, by the anchor's precedent), and one line if the
  installation's backend is Cloud Batch. `doctor` reports the same.
- **R9. The guards of the removed mode are deleted, not kept as dead code** (F15), and D2071's
  refusal with them.
- **R10. Other plans, same files.** The keys plan's KS19 and KS13 edit
  `internal/cli/init_credentials.go` and `internal/infra/spec.go`, and its KS14 and KS15 wait on
  this plan's I1. A task of this plan is not launched while a task of that plan that edits the
  same file is live, and whichever lands second rebases.

## 6. Open tensions and open items

No two answers pull against each other.

- **O1.** R4 keeps the policy node at version 10 while a value leaves its vocabulary. For the owner
  to confirm.
- **O2.** Every run now halts if the budget database stays unreachable past its grace, in observe
  too (F8). Today only installations that chose a budget carry that dependency.
- **O3.** D3020 and D3022 stop some runs that work today until a person acts (§4).
- **O4.** Cloud Batch can launch nothing after this work, until a design of its own (D3021).
- **O5.** The database costs about two dollars a month at the design's own estimate and needs
  billing on the project (F18). An installation with no billing gets R2's refusal with that fix.

## 7. UNVERIFIED

- **U1.** Whether an operator's roles suffice to apply the Firebase root. Settled by AB7's probe and
  Check 790.
- **U2.** What a run in flight does when the database's rules are deployed for the first time
  underneath it. It holds no session, so nothing is expected; Check 790 runs one.
- **U3.** That no documents test pins a sentence of F17. Settled by AB12's first run of the Docs
  filter.
- **U4.** That an older runner under an explicit `observe` on a database created minutes earlier
  behaves as any observe run. Check 790.
