# Always a budget: reconciliation

Checked against `origin/main` at `fb0aeb9aa` on 2026-11-20. **Where this file disagrees with
[spec.md](spec.md), this file wins.** It cites paths and symbols, never line numbers. Decisions are
**D3020–D3029**, all answered by the owner on 2026-11-20 (D3023–D3025 were added by review
round 1, D3026 by round 2, D3027 and D3028 after round 3, on its open items O10 and O6). It carries out **D3009, D3010 and
D3011** of [every model key through the credentials service](../keys-through-service/reconciliation.md),
and it reverses the part of **D2071** that guards a mode that no longer exists.

## 1. What the spec asks for, and what the repository already has

| The spec's concept | What the repository has |
| --- | --- |
| "Budget mode off" | `policy.ModeOff`, with the aliases `config.BudgetOff` and `localcfg.BudgetOff`. 19 Go files outside tests name the constants, more read the mode as a string, and 11 test files name them as words |
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
- **A4.** The work ships in one CLI release (D3023, R1).

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
(`checkFirebaseProject`, `internal/cli/init.go`). Consequence: the stage can run with no flag (AB7).
The gate stays (F28).

**F4 — Rule: what creating the database needs.** `initFirebase` (`internal/cli/init_firebase.go`)
checks that the project is active and has billing (`infra.CheckFirebaseProject`), reads the
project's owners and editors as the budget's admins, applies the Firebase root (a database fixed
in `us-central1`, a web key, a signer account, a custom role and project IAM grants), writes the
rules and the mode over REST, and records the result in the local config (`localcfg.Budget`).
Nothing links billing. *(Corrected by review round 3, and again when O10 was put to the owner.)*
The stage has two confirmations before it writes. The ordinary one (`confirmOrdinary`) is
answered by a person at their terminal or by a run that carries `--yes`. The second
(`confirmLocation`) is for the Firestore database, the run history's, whose location is permanent
by the owner's ruling of 2026-10-20: at a terminal the person reads a note and is asked nothing;
through `update` a carried answer covers it; through `setup` no carried answer ever does. Both
come before the budget database's own write (`db.Apply` in `deployDatabase`), so where the second
is left for a person the budget database is not written either. Consequence: D3027.

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
(`docs/budget-token-channel/spec.md`). The backend is wired (`doctor_batch.go`,
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
`docs/how-to/budgets.md`, `docs/m9-budget-and-dashboard/spec.md` (its rollback is
`--budget-mode off`), `docs/design/v1.md`, `docs/multi-model.md`, Check 47's row in
`docs/gcp-live-checklist.md`, `plugin/skills/setup/reference/decisions.md` and
`website/content/security.md`. *(Corrected by review round 2.)*
`internal/cli/docs_security_test.go` pins two such sentences of `SECURITY.md` ("Budget mode
defaults to off." and the one that begins "With budget off there is no managed file"), and
`docs/go-to-market/security-check.md` quotes the second. Other rows of the checklist than Check
47's also present off. Consequence: AB12, AB13.

**F18 — Rule: the cost.** The budget design's own estimate for 20 repositories and 100 runs a day
is about two dollars a month for the database, with Firestore and Auth in their free tiers, and
the pay-as-you-go plan is required. Consequence: stated in the release notes (AB12).

**F19 — Rule: the enterprise code.** `ee/ab/admit.go` maps an empty mode to off for its provider
check, and `ee/coordinator/cli/declare.go` skips declaring when the mode is off or there is no
database. Consequence: AB10.

**F20 — Wrong (review round 1), corrects F13 and R6.** Two kinds of job do not get their mode
from `budgetEnv`. A shared project job's environment (`projectJobEnv`,
`internal/infra/projectjobs.go`) never names a mode, and no launcher puts one on the execution, so
such a job takes its mode from the published policy alone. And a repository's
job is re-applied only by `fugaro update` run for that repository, so a job applied before the
database existed carries no database address, and a minted launch on it is refused by the runner
(`strayTokenNoBackend`, `internal/runner/budget_session.go`). Consequence: AB5 gives the shared
job's environment the same entry, and AB4 refuses such a launch itself, with the fix.

**F21 — Rule (review round 1): the database has a mode of its own.** A run's effective mode is the
stricter of the database's `config/mode` and the repository's (`internal/budget/evaluate.go`).
`--budget-mode off` leaves the database's mode as it was, and `raiseBudgetMode` raises a managed
project that has caps to enforce. So an installation with a database and a local off does not
land on observe: it lands on whatever its database says. Consequence: D3024.

**F22 — Wrong (review round 1), corrects F16.** `projectpolicy.Migrate` keys on the version, so
for a node already at version 10 the existing migration does nothing. And both decoders call
`ValidMode` (`validateBudget` in `internal/localcfg/localcfg.go`; the node's decoder in
`internal/projectpolicy/policy.go`), so once off is refused, a local config or a node that says
off no longer loads, and nothing could rewrite it. Consequence: AB8a and AB8b read the old word
before validation, and AB2 follows them.

**F23 — Gap (review round 1): readers the plan did not own.** `infra.CredentialsServiceServes` is
also called from `internal/cli/run.go`. `enable_budget` is also set in
`roots/installation/main.tf` and declared in its `variables.tf`, and tested in
`tests/installation.tftest.hcl` and `licence.tftest.hcl`; dropping a `count` moves a resource's
address, which destroys and recreates it unless a `moved` block says otherwise. Five live-check
scripts assert facts about a published mode of off (`check-580.sh`, `check193.sh`, `check197.sh`,
`check610.sh`, `check611.sh`). `internal/cli/init_firebase.go` warns about a mode of off in three
places. `internal/cli/exec.go` (`checkTestHooks`) and `settings_cloud.go` read the mode.
11 test files name the constants as words. Consequence: AB15, AB7, AB14, AB3 and I1 as amended;
AB6 was later dropped (F28).

**F24 — Rule (review round 1): the adoption probe reads state anyone with the state bucket can
write.** `CheckFirebaseOutputsFor` accepts whatever project the Firebase state's outputs name.
Run on every update, an unpinned probe would let a write to the state bucket point every run's
session at a foreign database. Consequence: AB7 adopts only a database in the installation's own
project.

**F25 — Gap (review round 1): managed projects.** Today the Firebase stage runs only with
`--firebase-project`, a flag that takes the run out of the managed gate (`shapesTheInstallation`).
What the gate does with the stage's IAM changes when no flag is given is UNVERIFIED (U5).
Consequence: R11, O6.

**F26 — Rule (review round 1): the switch widens.** With the mode argument gone, an `api-key` or
`oauth` repository that was in mode off passes `CredentialsServiceServes` where its bundle is
ready, and its next apply drops its model key from the job (`internal/cli/doctor_brokering.go`).
That is the credentials service's existing behaviour for every other repository. It first shows
at AB2's merge, where unset stops meaning off (`flipServes` passes the local config's mode), and
is complete at AB15's. Consequence: AB15 states it and R8's listing names it.

**F27 — Gap (review round 2): an `oauth` run has no gateway in any mode.** `runner.GatewayOn`
never answers true for `oauth`, and `agent.BuildEnv`'s `oauth` arm puts the subscription's token
in the agent's environment, by its own comment; `OAuthStageEnv` does the same for a stage's
account. Such a run is metered by nothing unless the credentials service serves the repository.
So "every run has a gateway" was false as written. Consequence: D3026, R12.

**F28 — Wrong (review round 2), corrects F3.** With `enable_budget` gone, every apply of the
installation root would create the history account, its custom role and a project IAM grant,
outside R2's probe, so an operator's update would fail at the installation stage, and the managed
gate's rule on added members would meet it too. Consequence: AB6 is dropped. The variable stays
and is set once the database exists, as today.

**F29 — Gap (review round 2): the flag has more readers.** The second installation stage is
skipped with "no --firebase-project given" (`newInstallation2Stage`), and it is what deploys the
history job and the shared project jobs that carry the database's address.
`internal/cli/init_images.go` mirrors the history image only when the flag is given, and
`internal/cli/init_review.go` holds the budget admins' review. Consequence: AB7 owns all three.

**F30 — Rule (review round 2): what an older CLI does after the upgrade.** After an owner's update
has re-applied a job with its mode and database, a teammate's older CLI, whose local config knows
no database, mints nothing, and the run is refused by the runner. A long-running
`fugaro orchestrator serve` on the old binary does the same. Consequence: stated in §4's costs;
the fix is the upgrade.

**F31 — Rule (review round 2): the new database is seeded as today's.** A database created with no
mode chosen is seeded observe with no `config/modeSet`, so `doctor` gives its existing note that
the mode was seeded and not chosen (`internal/cli/doctor_budget_mode.go`), and `raiseBudgetMode`
raises a managed project to enforce once it has caps, in the same update if the caps are already
there, with the line it prints today. Consequence: stated, unchanged.

**F32 — Rule (review round 2): the state can be deleted.** The adoption probe learns that a
database exists from the Firebase state alone. With that state gone, an update would try to create
over a database that exists. `internal/infra/firebase.go` already lists a project's databases.
Consequence: AB7 checks the listing before it creates.

**F33 — Gap (review round 3): the tests assume no budget.** Almost no hermetic test of the runner
or the CLI sets up a database: one runner test file in about two hundred names a backend, and
about two dozen CLI test files name a database address, while the end-to-end tests run the
container's entry point with none. Once a run with no database is refused, they fail. The fake
project API (`internal/gcpfake/crm.go`) has no permission-test endpoint either. Consequence: AB0
comes first, and AB1a, AB4 and AB7 run their whole packages.

**F34 — Gap (review round 3): the stage on an installation that has its database.** Today the
Firebase stage is skipped without its flag. Run on every update, it would re-apply the Firebase
root and read the project's owners, which an operator's update does not need today and may not be
able to do. Consequence: AB7's stage acts only to adopt or to create, and is skipped as today
where the database is known.

Amended by console-fixes RI2 (2026-11-22): the skip holds only while the Firebase root's state
already carries a run index composite index — the one thing such an installation can still owe,
and which nothing but an apply of that root creates (console-fixes F10) — and the state's own read
decides it, so where that read answers nothing (no state, or one that cannot be read) the skip
stands as written here and the installation is left exactly as it was. The run this finding is
about is the one it stands for in full: an operator's update, whose credential may not make the
project's IAM changes an apply of that root makes, keeps the skip and converges, and a project
owner's update creates the indexes.

## 4. Decisions

Inherited from the keys plan: **D3009** (budget mode off is removed; the owner's words are in
[spec.md](spec.md)), **D3010** (`observe` stays, and the migration lands on it) and **D3011** (this
spec).

Answered by the owner on 2026-11-20:

| # | Question | Answer | Mark |
| --- | --- | --- | --- |
| **D3020** | Under `observe`, what happens to a model the gateway cannot price (F10)? | **It is refused, with the fix**, in every mode. (That `fugaro update` and `doctor` name it first is this document's addition, R8) | **not the recommendation** (which was to run it under observe and mark its spend unpriced) |
| **D3021** | What happens to the Cloud Batch backend (F9)? | Every Batch launch is refused with one stated reason; `doctor` and the docs say so; a carrier for the token is a design of its own | as recommended |
| **D3022** | What does the release do with a repository's `fugaro.yaml` that says `off` (F12)? | **It is rejected at once**, with the fix | **not the recommendation** (which was to read it as unset for one release) |

| **D3023** | With one release, a no-database installation launches nothing after the upgrade until a project owner runs `fugaro update`. Ship it in two releases? | **No: one release, and the stop is accepted** | added by review round 1; **not the recommendation** (two releases) |
| **D3024** | An installation whose database says `enforce` while a machine's local config says off comes under the database's caps (F21). What does the migration do? | The database's mode stands. (The one line that names the mode and caps first was part of the option as asked) | added by review round 1; as recommended |
| **D3025** | Does the published policy get a new version, now that off leaves its vocabulary? | **No: it keeps its version** | added by review round 1; **not the recommendation** (a bump, which would fence an older CLI from publishing off) |

| **D3026** | An `oauth` repository the credentials service does not serve has no gateway in any mode (F27). What does this plan do about it? | **The owner's own answer:** "The entire notion of "key repository" or "oauth repository" is stupid and should die. There should be no such thing. keys/oauth should be there for models to get access to their servers, that's it." | added by review round 2; **not either option** |

**What D3026 means here, and what it leaves open.** It is D3007 of the keys plan said again, and it
answers that plan's O4: a repository should not have an auth mode at all. That is a change to the
config's shape, with its own readers and migration, and it is a spec of its own (R12). In this
plan it means that no new rule is written in terms of a repository's auth: an `oauth` run opens
its identity and its session like every other run, and what it holds and what meters it stay as
they are until that spec. "Every run has a gateway" is true of every run but those.

**Three answers set the hard rule aside, knowingly.** `CLAUDE.md` says `fugaro update` is the whole
upgrade, with no manual repair. D3020 leaves a `model_prices` entry for a person to add, D3022 a
one-line removal for a person to commit, and D3023 an update that only a project owner can
complete. The owner chose each against the recommendation that would have kept the rule. The
update still does everything it can and names the rest.

| **D3027** | The Firestore location's confirmation comes before the budget database's write (F4). What changes? | The stage writes the budget database first. The ruling on the permanent location stands, and where that confirmation is left for a person only the history database waits | answered after review round 3, on O10; as recommended |
| **D3028** | On a managed project, does creating the budget database happen under the managed gate, once the stage needs no flag (F25)? | **Yes: it is created under the gate**, like any converging step | answered after review round 3, on O6; **not the recommendation** (adopt under the gate, create outside it) |

| **D3029** | The budget database's location is a constant (`us-central1`) and can never be changed once created, and AB7 creates one on every installation. Should it follow a location the installation chose? | Yes. A location preference set at setup drives where the databases are created; it is a spec of its own, and AB7 waits on it | the owner's ruling of 2026-11-20, on the coordinator's recommendation |

**What D3029 means here.** AB7 is not launched before the location-preference spec's plan has
merged the part that says where a new budget database goes. Otherwise this plan would create a
database in the United States, permanently, for an installation that would have chosen Europe.

**What D3028 means.** On a managed project `fugaro update` creates the budget database, with its
custom role and project IAM grants, with no confirmation, and from a coding agent's session too,
because that is what the gate allows a converging step. The gate's own conditions still decide
whether the run may proceed at all.

**What D3020 and D3022 cost, stated once.** The brief's line 7 says nobody starts being refused by
a cap they did not choose. That still holds: no cap refuses anyone. But two things that ran under
off stop after the upgrade until a person acts: a run whose model has no price, and a run whose
committed `fugaro.yaml` says off. `fugaro update` names each such repository and the one-line fix
before it changes anything (R8).

**What D3023, D3024 and D3025 cost, stated once.** After `brew upgrade`, an installation with no
budget database launches nothing until a person who can make the project's IAM changes, on a
project with billing, runs `fugaro update`; a second machine launches nothing until its own
`fugaro update` has adopted the database; and a repository whose job was applied before the
database existed launches nothing until `fugaro update` is run for it. Each refusal names that
fix. A machine that relied on local off to stay outside the database's caps comes under them
(D3024). A teammate on an older CLI can still publish a policy that says off, and every new run
is then refused, with the fix, until the policy is published again (D3025). A teammate's older
CLI, and an orchestrator left running on the old binary, launch runs the runner refuses, until
they upgrade (F30). And a pull request that adds `budget.mode: off` to a repository's
`fugaro.yaml` stops that repository's runs once merged, where the project's mode used to outrank
it (D3022).

## 5. Resolutions

- **R1. One release, and no release in the middle** (D3023). No CLI release, a hotfix included, is
  cut from `main` between the merge of AB7 and I1. A hotfix needed in that window is cut from the
  last release's tag.
- **R2. `fugaro update` creates the database, and only where it can.** The Firebase stage runs with
  no flag, in the installation's own project. It first probes for a database that exists and is
  only unknown on this machine, and adopts it (F6). Where there is none, it probes whether the
  person can make the project's IAM changes (`testIamPermissions`). If they can, it creates the
  database, in `observe`. If they cannot, or the project has no billing, or nothing answers the
  stage's ordinary confirmation (a run with no terminal that carries no answer, F4), the update
  skips that stage, says in one line that a project owner must run `fugaro update` once (or that billing must
  be linked), and carries on with the rest. The cloud side is left as it was; the new CLI's
  launches are refused with that fix until the database exists (D3023). Adoption is pinned to the
  installation's own project (F24), and the stage lists the project's databases before it creates
  one (F32). The budget database is written before the Firestore location's note is reached, so a
  location left for a person holds back only the run history (D3027). The stages that follow the
  database, and were skipped with it, run with it (F29).
  It asks nothing new. `--firebase-project` stays accepted with
  the one value it could already take.
- **R3. Unset means `observe`.** In the local config, the launcher, the job's environment (always
  written, F13) and the run.
- **R4. The policy node keeps its version** (D3025). `fugaro update` rewrites a published `off` to
  `observe` in place, in a same-version step that is new code (F22). Every node the new release
  writes is a valid version 10 node, so no reader of version 10 breaks and no image is rebuilt for
  it. A new run that meets a node that still says off, because the update has not run or an older
  CLI published it, is refused with the fix, and `doctor` names it.
- **R5. Runs in flight.** A run that started with the budget off has no session and holds its key
  already; nothing the update does reaches it, and it finishes. `fugaro update` says so in one
  line when runs are running.
- **R6. An older job image needs no gate.** *(Amended by review round 1, F20.)* A re-applied
  job's environment names `observe` explicitly, a shared project job's execution carries it, and
  an older runner has that path. A repository's job that was applied before the database existed
  is refused at launch with the fix, `fugaro update` run for that repository, instead of reaching
  a runner that would refuse it less clearly.
- **R7. Old records keep the word.** `schemas/result.schema.json` keeps `off` in
  `policy.effective.mode` for the records old runs wrote, and its description says nothing writes
  it any more.
- **R8. The update says what will change before it changes anything.** For the repository it is
  run for, read from its checkout: one line if a pinned model has no price (with the
  `model_prices` fix), one if its `fugaro.yaml` says off (with the removal offered as a diff, by
  the anchor's precedent), and one if its model key will move to the credentials service at this
  apply (F26). For the installation: one line naming the database's mode and caps where a local
  off is being removed (D3024), and one if the backend is Cloud Batch. `doctor` reports the same,
  from one helper.
- **R9. The guards of the removed mode are deleted, not kept as dead code** (F15), and D2071's
  refusal with them.
- **R10. Other plans, same files.** The keys plan's KS19 and KS13 edit
  `internal/cli/init_credentials.go`, `internal/infra/spec.go` and `readiness.go`,
  `internal/localcfg/localcfg.go`, `internal/cli/update_in_flight.go` with
  `docs/design/update-in-flight.md`, `SECURITY.md` and `internal/cli/recipes_skew.go`, and its
  KS14 and KS15 wait on this plan's I1. A task of this plan is not launched while a task of that plan that edits the
  same file is live, and whichever lands second rebases.
- **R11. A managed project** (D3028). The database stage runs under the managed gate with no
  flag, and creates the database there as it adopts one: with no confirmation, and from a coding
  agent's session where the gate admits the run. R2's probe still applies: a person who cannot
  make the project's IAM changes gets its one line.
- **R12. Auth is not this plan's subject** (D3026). Retiring a repository's auth mode, so that a
  key or a subscription belongs to the models that use it, is a spec of its own, the third of this
  series. Until it lands an `oauth` run the credentials service does not serve has no gateway
  (F27).

## 6. Open tensions and open items

No two answers pull against each other.

- **O1.** D3025 leaves an older CLI able to publish off into a version 10 node, which stops every
  new run until the policy is published again. Accepted by the owner; `doctor` names it.
- **O2.** Every run now halts if the budget database stays unreachable past its grace, in observe
  too (F8). Today only installations that chose a budget carry that dependency.
- **O3.** D3020, D3022 and D3023 stop some runs that work today until a person acts (§4).
- **O4.** Cloud Batch can launch nothing after this work, until a design of its own (D3021).
  That design is [batch-run-identity](../batch-run-identity/README.md).
- **O5.** The database costs about two dollars a month at the design's own estimate and needs
  billing on the project (F18). An installation with no billing gets R2's line with that fix, and
  launches nothing until it is linked.
- **O6.** *Answered by D3028.* What remains is the security review of AB7: a coding agent's
  session on a managed project creates a custom role and project IAM grants with nobody
  confirming, by the owner's choice.
- **O7.** R1's freeze on releases is a rule for whoever cuts them; nothing in the repository
  enforces it.
- **O8.** "Every run has a gateway" waits on the spec R12 names for `oauth` runs the credentials
  service does not serve.
- **O10.** *Answered by D3027.* What remains: a run with no terminal that carries no answer still
  writes nothing, because the stage's ordinary confirmation is unanswered (F4). D3027 helps where
  that one is answered and the location's is not.
- **O13.** AB7 waits on a spec that is not written yet (D3029). AB0, C1, AB14 and the level 1
  tasks that do not need AB7 can go ahead.
- **O11.** Review round 3's fixes are the last commit and were not reviewed again; three rounds is
  the cap.
- **O12.** The graph cannot say that AB2, AB8a and AB8b wait for C1. A launcher follows the plan's
  levels table.

## 7. UNVERIFIED

- **U1.** Whether an operator's roles suffice to apply the Firebase root. Settled by AB7's probe and
  Check 790.
- **U2.** What a run in flight does when the database's rules are deployed for the first time
  underneath it. It holds no session, so nothing is expected; Check 790 runs one.
- **U3.** *Settled by review round 2 (F17):* two sentences are pinned.
- **U4.** That an older runner under an explicit `observe` on a database created minutes earlier
  behaves as any observe run. Check 790.
- **U5.** Whether the managed gate's rule on added members admits the Firebase stage's IAM grants
  as it stands, or needs them named (F25, D3028). Settled by AB7's first test.
- **U6.** Which record says a repository's job was applied before the database existed (R6). The
  local config's repository entry is the candidate; settled by AB4.
- **U7.** The exact entry points of the runner's and the CLI's test harnesses that AB0 changes.
  Settled by AB0.
