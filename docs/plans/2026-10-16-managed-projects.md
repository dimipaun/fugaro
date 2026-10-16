# Fugaro-managed projects: implementation plan

**Goal:** build what the owner's spec asks for — autonomous cloud operations in a project Fugaro created — in the only shape this repository can honestly ship it: **a deterministic gate decides, a recorded model review advises, and no coding-agent session ever holds the authority to apply.** The reconciled design is [docs/design/managed-projects.md](../design/managed-projects.md). **Nothing here is approved and nothing here is built: no line of this plan has been written or run, and this PR (the one that adds this plan) is docs-only — it touches exactly three files, the owner's spec kept verbatim (`docs/design/managed-projects-spec.md`), the design and this plan, and nothing else at all**, not `docs/gcp-live-checklist.md` (the check number this plan reserves, **60**, is *named* here and its rows and sections are written by Task 5.3 when the phase that needs them ships), not the checklist's summary table, not `README.md`'s Roadmap (the repository keeps no index of `docs/design/` or `docs/plans/`; the design's §1.1 shows the evidence), not `go.mod`, not any Go, Terraform or product-behavior file under the "Files" lists below. Task 0.1 onward is future work, and every one of it is gated on the OWNER APPROVAL GATE at the end of this file.

**Status: no decision is answered and no task is approved.** The design's §5 carries eighteen decisions, **D21 to D38**, continuing the numbering from `desktop-console.md`'s D20, and **every owner-answer column in that table is empty.** Nothing below presumes an answer: the tasks are written so that the answer to a decision lands as a *task*, not as an assumption hidden in one, and the four tasks whose whole shape depends on an unanswered decision (**D21** who applies, **D22**/**D23** the marker, **D24** what "Fugaro created it" means, **D29** the hard stop) say so in their headings and stay unscheduled until it is given. **D21 is the central one**: the spec's effect is to remove the refusal of live applies from a coding-agent session, and this plan does not take that as decided. Where the recommendation is "keep the refusal, narrow it", the plan is written to that recommendation and the alternative is written down as a cost in the design's D21 row, so the owner can choose with the cost in front of them.

**Spec:** [docs/design/managed-projects-spec.md](../design/managed-projects-spec.md), the owner's draft of Oct 15 2026 kept verbatim, and the reconciliation [docs/design/managed-projects.md](../design/managed-projects.md) written from it. The reconciliation's §3 findings are the plan's working list: each **Gap** is a task below or a row in "Open items" saying why it is not, each **Rule** is a Global Constraint, and each **Wrong** is a thing this plan does *not* build (the model review as the gate; a raw plan file in the repository; a repository config as the authority; "disposable" as a licence to lose state). The design's §9 UNVERIFIED list is the plan's first tasks.

**Architecture — what the six phases build, in one paragraph.** Phase 0 makes "Fugaro created this project" a fact the program can read after the run that created it has ended, and makes the read **fail closed** — a marker that cannot be checked means *not managed*, exactly as `infra.ReadProjectMarker` already behaves for the runs bucket. Phase 1 turns the two deterministic gates that exist for Fugaro's own embedded modules (`internal/infra/tf/guard.go`'s `Guard` and `internal/infra/tf/cover.go`'s `Cover`) into the gate a managed project's applies run behind: `Guard` unchanged, `Cover` re-derived as a managed-project allowlist whose widest entries are decided rather than inherited. Phase 2 makes the model's review **advisory and recorded** — it runs, its output is stored, its verdict can only *add* a refusal, and it is never the reason a plan is applied — and puts the redaction rule in place so no plan file ever reaches a repository. Phase 3 is the apply path, and it is where D21 lands: the plan is written to the recommendation (the agent proposes, a step that is not a coding agent applies a saved plan that passed the gate), and the alternative is a task of its own that does not start until the owner chooses it. Phase 4 builds the two things the spec's Cost safeguards and "single purpose" need that do not exist: a whole-project inventory read, and the budget's **required** amount with the existing alerts. Phase 5 is secrets and disaster recovery: one new column on `fugaro secrets ls`, and a restore that promises a **from-zero apply**, not a state restore, because the state bucket lives in the project that was lost. Phase 6 is deliberately empty of work: it lists the two things a reader will otherwise look for (the billing hard stop, and moving an existing project into managed mode) and says which decision each waits on.

**What this plan does not do, stated once so it is not read as an omission.** It does not remove `CanConfirm`'s agent-marker refusal (D21's recommendation); it does not put a plan file in the repository; it does not introduce a Cloud Billing Budgets API client, a Pub/Sub topic or a billing-admin function (D29); it does not add a credential anywhere the console, the launcher or the agent can read one; and it does not touch `belong`.

**Builds on merged code (what each phase stands on).** Verified on `origin/main` at **`a573870`**, which is also this plan's writing baseline (the branch this was written on was identical to it, working tree clean):

| Phase | Merged work it builds on |
|---|---|
| 0 | `internal/infra/project.go` (`ProjectMarkerObject`, `ProjectMarker`, `ReadProjectMarker`), `internal/infra/projectcreate.go` (`CreateProject`, `AddFirebase`, `LinkBilling`), `internal/cli/init_project.go` (the `Apply` stage, `canType`), `internal/initflow/stage.go` (`ConfirmClass`, `CanConfirm`, `Conditions`), `internal/cli/initsecrets.go` (`agentMarkers`, `agentMarker`) |
| 1 | `internal/infra/tf/cover.go` (`allowedTypes`, `grantRules`, `Cover`, `RolePermissions`, `NotCovered`, `attributes`), `internal/infra/tf/guard.go` (`Guard`, `knownActions`), `internal/infra/tf/tf.go` (`Plan`, `Show`, `Apply`), `internal/infra/workdir.go` (`PlanFile`, `InstallationWorkdir`), the fixture corpus `internal/infra/tf/{cover_test,cover_golden_test,cover_grants_test,fake_test}.go` |
| 2 | `internal/runner/runner.go` (the `review` stage, `ReviewerIsCoder`), `internal/runner/verdict.go` (`VerdictSchema`), `internal/runner/prompts.go` (`defaultReview`), `internal/runstore`, `deploy/terraform/gcp/modules/installation/bucket.tf` (the `runs/` prefix and its 90-day rule) |
| 3 | phase 0's marker read, phase 1's gate, `internal/cli/init_review.go` (`runCovers`, `planOnlyReview`), `internal/cli/init.go` (`--plan-only`, `--yes`), `internal/cli/root.go` (the exit codes) |
| 4 | `internal/infra/discover.go` (label discovery), `internal/backend/gcp/names.go` (`LabelManaged`, `LabelProject`), `deploy/terraform/gcp/modules/installation/budget.tf`, `internal/cli/init.go` (`--budget`), `internal/budget/{model,kill}.go` |
| 5 | `internal/cli/secrets.go` (`newSecretsLsCmd`), `internal/config/config.go` (`ReservedSecrets`), `internal/recipe/recipe.go` (`ObjectPrefix`), `internal/cli/recipes.go`, `internal/infra/statebucket.go`, `docs/design/project-centric.md` §4 (the migration) |
| 6 | everything above |

## Phase summary

| Phase | What ships | Owner-run steps | Depends on |
|---|---|---|---|
| **0. The fact** | the creation fact recorded at creation and a marker read that fails closed; **the property test that a non-managed project never skips a confirmation** | nothing | nothing |
| **1. The gate** | `Guard` wired into a managed apply path, `Cover` re-derived as a managed-project allowlist, the plan summary, the cost attributes | nothing | phase 0 |
| **2. The review, advisory** | the review stored as an artifact, the redaction rule, the retention decision written down | nothing | phase 1 |
| **3. The apply** | the apply path of **D21**: the agent proposes, a non-agent step applies a saved plan that passed the gate — plus the exit codes and the refusals | **nothing, until D21 is answered**; if D21 chooses the spec's shape, Task 3.3 is the alternative and no agent starts it | phase 2 |
| **4. Single purpose and the budget** | the whole-project inventory read with its confirmations fallback; the budget **required** at creation, alerts as they exist | nothing | phase 3 |
| **5. Secrets and restore** | the missing-secret column on `secrets ls`; `fugaro restore`, a from-zero apply into a fresh ID; **Check 60**, written as 60a and 60b | **all of phase 5's check**: the sandbox managed project, the apply, the refusal on a marker mismatch, the restore round trip (Check 60) | phase 4 |
| **6. Later, unscheduled** | the billing hard stop; moving an existing project into managed mode | each, when the owner decides | phase 5 |

**Nothing in this table depends on a later row.** Phase 6 is deliberately empty of work: it lists the two things a reader will look for and gates each on a decision this plan does not presume.

## Check numbers this plan takes (60)

`docs/gcp-live-checklist.md` on `main` (`a573870`) has its highest written `## Check N` section at **47** (`## Check 47`), and its summary table's highest rows at **47** and **57**. The numbers above 47 are spoken for, and this is the chain, each step re-readable from the tree:

| Number | Claimed by | State |
|---|---|---|
| 42, 43 (43a-43f), 44, 45, 46, 47 | the config-transitions, registry-remote-repository, price-catalog, mixed-credentials and OAuth-reviewer work | **written on `main`, not run** (`## Check 42` … `## Check 47`) |
| **48-56** | **`docs/plans/2026-10-14-project-centric.md`**, §"Check numbers this plan takes (48 to 56)" | **reserved in that plan**, one number per phase, each written by its own phase's task |
| 57 | the compute-instance Docker backend, design `compute-backend.md` §7 and Check 57's row on `main` | **the summary row is on `main`; the section is not** — it lives in `docs/design/compute-backend.md` §7.2. A number whose section lives in another document is still taken |
| **58** | **`docs/design/artifact-runs.md`** (§11, "Check 58, in outline"), whose row that plan commits | **taken by that design**, which is merged on `main` (`docs/design/artifact-runs.md` exists at `a573870` and its §11 is written) |
| **59**, written as **59a** and **59b** | **`docs/plans/2026-10-15-desktop-console.md`**, §"Check numbers this plan takes (59)" | **reserved in that plan**, and its rows are written by its Task 4.3 |
| **60**, written as **60a** and **60b** | **this plan, phase 5**: **60a** the managed apply on a real sandbox project — the apply happening with no confirmation, a non-managed project still refusing, the marker mismatch refused, and the run's credential unable to write the marker; **60b** the restore round trip into a fresh project ID | **reserved here** |

**How 60 was chosen, and the rule that stops two open PRs colliding, stated so a later reader can re-check it.** "The next free number" cannot be read off `main`'s headings alone, because a number is taken three ways — a written section, a written summary row whose section lives in another document, and a reservation in a plan or design whose phase has not shipped — and because two open PRs can both correctly believe they take the same next free number. What settles it is which lands first: **a number is chosen at the moment of merge, not at the moment of writing.** So, in this order, on `origin/main` at `a573870`:

1. **Every `## Check N` section written in the checklist runs 1 to 47**, consecutively through 43a-43f. `grep -n '^## Check' docs/gcp-live-checklist.md` answers this directly.
2. **The summary table's rows go to 47 and 57.** 57's row is on `main` and its section is in `docs/design/compute-backend.md` §7.2.
3. **`docs/plans/2026-10-14-project-centric.md` reserves 48 to 56**, in a table with a `State` column, writing each row with its own phase — so those nine are spoken for even though `main` carries none of them yet.
4. **58 is artifact-runs' and 59 is desktop-console's.** Both are merged-or-open reservations recorded in their own documents, not on the checklist; neither uses 60.
5. **Which leaves 60 free, and this plan takes it.**

**The rule, so no PR has to guess at review time: whichever of two contending PRs merges second renumbers**, checking `main`'s sections and summary rows before it writes anything, because the loser's number is the one that was never free. A number already written into a summary table and a task heading is expensive to move and free to leave alone; the loser's is cheap.

**This plan takes one number, not a range**, because its live steps belong to one feature and one phase; **it is written as two sections, 60a and 60b**, which is the 43a-43f sub-lettering convention rather than a second reservation. The split is not decoration: **60a needs a managed project to exist and 60b needs one to have been destroyed**, so they cannot run at the same moment, and 60a's assertion is the safety one (that a non-managed project still refuses and that the marker cannot be forged) while 60b's is the durability one. **Following the project-centric plan's convention, neither this PR nor any docs-only task here writes the row or the section**: Task 5.3 writes both when phase 5 ships, so exactly one writer owns the number and a rebase cannot produce two.

**The re-check this section invites is a `git log` away**, and the commands are: `git show origin/main:docs/gcp-live-checklist.md | grep -n '^## Check 4[5-9]'` for step 1, `grep -n '^| 4[0-9] \|^| 5[0-9] ' docs/gcp-live-checklist.md` for step 2, a grep of `docs/plans/` and `docs/design/` for steps 3 and 4, and this plan's own heading for step 5.

**Check 60 — both its sections — is owner-run, on the sandbox installation and the sandbox repository only, and never touches the `belong` project** (`belong` holds `edgeappinc/edgeserver` and `edgeappinc/edgeweb`: no agent touches it, ever). It is written with FACT lines, a restore step, and **Check 43's full guard, verbatim** — the three hard stops, `--project fugaro` on every `fugaro` command and `--project "${P:?}"` on every `gcloud` command, in a block re-run from a fresh shell before every step (`docs/gcp-live-checklist.md`, Check 43's guard block; the rule the project-centric plan spells once and requires of every check 48 onward, `docs/plans/2026-10-14-project-centric.md:228`). **This check needs that guard more than most, because its whole subject is a project whose confirmations are off**: 60a's hard-stop line asserts that the checkout is the sandbox and that `--project` is `fugaro` before any command runs, and 60a's first FACT is that no managed project exists yet in the installation. The future `TestChecklistChecksAreGuarded` (project-centric plan Task 6.3) asserts that every `## Check 48`-onward section carries Check 43's three hard stops and its project pinning, not merely a `:?` guard, and Checks 60a and 60b are `## Check 60a` and `## Check 60b` sections.

## Global Constraints

These hold for every task below, and the plan's own review checks them:

1. **No coding-agent session ever gains the ability to apply.** `initflow.CanConfirm` keeps returning `false` for every class under a marker (`internal/initflow/stage.go:334-343`), `AgentRefusal` keeps naming the marker and never offering `--yes` (`:298-305`), and no task here adds a path around either. **A task that needs to apply a plan does it in a step that is not a coding agent** — the runner's own apply path or an owner-run command — and a test asserts it. This is Global Constraint 1 because D21 is the central decision and this plan is written to its recommendation.
2. **The cloud marker is the authority and every config may only narrow.** No task lets a repository's `fugaro.yaml`, the local project config, or any agent-written file turn managed mode **on**. Reading one may turn it **off**.
3. **A marker that cannot be read means "not managed".** Every read fails closed, in the shape `infra.ReadProjectMarker` already has (`internal/infra/project.go:42-56`: the bucket must be the expected project's, proved by its own project number, before its contents are believed).
4. **No credential a run holds may be able to write the marker** — or, in the full version of this feature, to widen its own cloud permissions. A task that changes what a run's identity can do carries a test asserting what it can no longer do.
5. **A plan file is never committed, and never stored outside the local workdir in unredacted form.** The saved `fugaro.tfplan` is a data structure that carries attribute values; what may enter a repository or a run record is a redacted JSON summary. `.gitignore` gains a rule for `*.tfplan` as its own task.
6. **The model review is advisory, recorded, and never the gate.** No task makes a verdict of `ship` a precondition of an apply, and none makes `changes` the only thing that stops one.
7. **`belong` is never touched** by any step, agent-run or owner-run. No task names it as a target, no check reads it, no fixture is recorded from it.
8. **Never handle a secret.** No task reads, writes, prints or transmits a secret *value*; names and metadata only, as `fugaro secrets ls` already does (`internal/cli/secrets.go:531-547`).
9. **Nothing under `.github/workflows/` is created or edited by an agent.** No task in this plan needs one; if a later task does, its workflow text is written as a staged file under `docs/pending-workflows/` for the owner to copy (`docs/pending-workflows/README.md`), which is the mechanism this repo already uses.
10. **Security-sensitive tasks are flagged `SECURITY-SENSITIVE — independent opus review`** in their own heading and are not merged on a self-review. **This list is the headings, and nothing else is on it**: 0.1, 0.2, 0.3, 1.1, 2.1, 2.2, **3.1, 3.2, 3.3**, 4.1. **A separate flag, `OWNER-RUN`, marks the tasks no agent performs at all** — Task 5.3 (Check 60) — and the two flags are independent: a task may carry neither, either, or both.
11. **Docs-only tasks keep the docs suite green**: every task that touches a `.md` file under `docs/` runs `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`, the command this repository's docs tests are reached by.
12. **Every task's verification runs without a live cloud.** No task's command needs a real GCP project, a billing account or a credential; the live steps are Check 60 and nothing else. Task 0.3's property test and phase 1's fixture corpus are what make the gate testable at all.

## Review Focus

What a reviewer of each phase should attack, in the design's own terms:

- **Phase 0**: **can the marker be made true by anything the agent can do?** Two questions, and both must be answered from the code rather than from the prose: what identity writes it, and what reads it. A read that trusts a string, or that treats "cannot read" as "managed", is the defect that turns this whole feature into a privilege escalation.
- **Phase 1**: **is the gate the gate?** `Cover.NotCovered`'s managed-project allowlist is the widest entry in the design — a plan the agent wrote, judged by a list someone chose. A reviewer should ask which type on the list is there because the embedded modules use it and which is there because a managed project might legitimately need it, and treat an unexplained second category as a finding. Then: does every refusal actually stop the apply, or merely return an error someone ignores?
- **Phase 2**: **can a job-written string become a decision, and can a plan secret become a repository file?** Look for a verdict interpolated into a refusal list as though it were a rule, and for any path that writes a plan rather than a summary.
- **Phase 3**: **who holds the authority to apply, and can a coding agent reach it?** This is D21 made concrete, and a reviewer should try the bypass: an environment variable, a config key, a `--yes`, a `--non-interactive`, a pipe. If any of them reaches an apply in a managed project while a marker is set, the phase has failed.
- **Phase 4**: **does the inventory check fail in the safe direction?** A false positive must fall back to confirmations, never halt; a false negative must not be reachable by a resource the installation's state does not know about. And: does the budget amount come from the operator, or from a default nobody chose?
- **Phase 5**: **what does restore actually promise, and is it true?** The state bucket lives in the project that was lost; a restore that reads it is a restore that will fail exactly when it is needed. Check whether the plan's promise is "from-zero apply" and whether anything in it quietly assumes the old state.
- **Every phase**: the refusal stays, the marker fails closed, no plan file is committed, `belong` is untouched, and no task's command needs a live cloud.

## File Structure

Created by this plan (nothing here exists yet, and **none of it is created by this PR**):

```
internal/infra/managed/
  marker.go             # Task 0.1: the managed marker object, its shape, and its write
  marker_test.go        # Task 0.1: the round trip, the shape, and the one-writer refusal
  read.go               # Task 0.2: the read, project-number-checked, failing closed
  read_test.go          # Task 0.2: the read against every mismatch
internal/cli/
  managed_test.go       # Task 0.3: the property test - a non-managed project never skips a confirmation
  init_budget_test.go   # Task 4.2: the budget required for a managed project, and unchanged for the rest
  restore.go            # Task 5.2: fugaro restore
  restore_test.go       # Task 5.2: no state read, the fresh ID, the not-restored list
internal/infra/tf/
  managed_cover.go      # Tasks 1.1, 1.2: the managed-project instantiation of Cover and its attributes
  managed_cover_test.go # Tasks 1.1, 1.2: a refused fixture per rule
  managed_cost.go       # Task 1.3: the cost-bearing attribute list, its flags and its blocks
  managed_cost_test.go  # Task 1.3: one fixture per entry, and the below-the-threshold pass
internal/managed/
  plan.go               # Task 1.0: the saved plan's path through the guard and the cover
  plan_test.go
  review.go             # Task 2.0: MayApply, and the review as a refusal only
  review_test.go
  review_store.go       # Task 2.1: the review artifact in the run's own record prefix
  review_store_test.go
  redact.go             # Task 2.2: the plan -> summary redaction
  redact_test.go
  apply.go              # Tasks 3.1, 3.2: the apply of a saved plan, and its refusals
  apply_test.go
  inventory.go          # Task 4.1: the whole-project inventory and its fallback
  inventory_test.go
```

Edited by this plan:

```
internal/initflow/stage.go                    # Task 0.3 only: no behaviour change; the tests read it (a change here is a review finding)
internal/cli/init_project.go                  # Task 0.1: record the creation fact; Task 4.2: require the budget
internal/infra/projectcreate.go               # Task 0.1 only, if the fact is written beside the project's creation
internal/cli/init.go                          # Task 4.2: the budget becomes required, not optional
internal/cli/root.go                          # Task 3.2: the exit codes the managed refusals return
internal/cli/secrets.go                       # Task 5.1: the required-and-missing column on `secrets ls`
internal/infra/statebucket.go                 # Task 5.2 reads it, and changes nothing
.gitignore                                     # Task 2.2: `*.tfplan`
docs/gcp-live-checklist.md                    # Task 5.3 only: Check 60's rows and sections (60a, 60b)
docs/design/managed-projects.md               # Tasks 2.3, 4.3, 5.4: the retention, alert-reach and decision rows
docs/plans/2026-10-16-managed-projects.md     # Task 5.4 only, if an answer changes a task's shape
```

## PR groups

Each row is one mergeable PR, green on its own:

| PR | Tasks | Flag |
|---|---|---|
| A | 0.1, 0.2, 0.3 | **SECURITY-SENSITIVE** (the marker is the privilege boundary) |
| B | 1.0, 1.1, 1.2, 1.3 | **SECURITY-SENSITIVE** (1.1: the allowlist is the widest entry in the design) |
| C | 2.0, 2.1, 2.2, 2.3 | **SECURITY-SENSITIVE** (2.1, 2.2: the review as a channel, the redaction) |
| D | 3.0, 3.1, 3.2 | **SECURITY-SENSITIVE** (the apply path, D21's recommendation) |
| E | 3.3 | **SECURITY-SENSITIVE**, and **unscheduled until D21 is answered the other way** — it is the alternative, written so the owner can see its shape before choosing it |
| F | 4.1, 4.2, 4.3 | |
| G | 5.1, 5.2 | |
| H | 5.3, 5.4 | **OWNER-RUN** (Check 60), no agent performs any step of 5.3 |

---

## Phase 0 — The fact

**Why this is first.** Everything else in this feature is downstream of one question: **how does the program know, in a run that did not create the project, that Fugaro created the project?** Today it does not. `fugaro init --create-project` creates the GCP project and adds Firebase (`internal/cli/init_project.go:412-444`, `internal/infra/projectcreate.go:376-390`), the fact lives in the run's memory for the length of that run, and nothing persists it (design finding 19). The nearest durable ownership mark is the runs bucket's `fugaro/project.json`, and it is read with a project-number check precisely because a bucket name proves nothing on its own (`internal/infra/project.go:23-56`). Phase 0 makes the creation fact durable, in that shape, and writes the one property test whose failure would be silent.

### Task 0.1: The creation fact, recorded where a run cannot write it — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/infra/managed/marker.go`, `internal/infra/managed/marker_test.go`, `internal/cli/init_project.go`, `internal/infra/projectcreate.go`

**Steps.** At the moment `init --create-project` finishes creating a project — after `AddFirebase` returns and before the stage reports `Changed` (`internal/cli/init_project.go:412-444`) — record the fact in two places that cannot disagree: **the installation's Terraform state** (the operator owns it, and a run's job account cannot write it) and **a marker object written beside the runs bucket's `fugaro/project.json`**, `fugaro/managed.json`, holding the same shape `ProjectMarker` already has (`Version`, `Name`, `GCPProject`) plus the creation timestamp. The write happens on the **operator's own apply path**, not from a job: `CreateProject` is called by `fugaro init` at a terminal (or under `--yes` in the run that creates it), which is the one moment the fact is knowable. The object's writer is the operator's identity, and the task's test asserts the run's job account is not in the bucket's writer list. **A marker object, not a project label, is what this task writes**: a project label is `resourcemanager.projects.update`, which any project editor holds, and D23 is not answered — the label, if the owner wants one, is Task 3.0's, after the answer.

**Tests:** the marker's round trip; the JSON shape against a fixture; and **the write path refuses when the project it is asked to mark is not the project `init` just created** — the fact is written once, from the one place that knows it, and a second call is an error rather than an update.

**Command:** `GOFLAGS=-p=2 go test ./internal/infra/managed ./internal/cli -run 'Managed|Marker|Project' -count=1`

**Depends on:** nothing.

**Commit:** `managed: the creation fact, recorded where a run cannot write it`

**Red step — `Expected: FAIL`** — the "not the project this run created" test against a marker write that accepts any project name.

### Task 0.2: The read, project-number-checked, failing closed — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/infra/managed/read.go`, `internal/infra/managed/read_test.go`

**Steps.** `IsManaged(ctx, project, gcpProject) (bool, reason string, err error)`, built as `ReadProjectMarker` is (`internal/infra/project.go:42-56`) and for the same stated reason: **a name is global, so a mark proves nothing by itself.** The read locates the runs bucket for the installation, asserts the bucket's own project number is the expected one, and only then believes `fugaro/managed.json`. Every failure mode returns `false` with a reason a person can act on and **never an error the caller might treat as "proceed"**: no bucket, a bucket in another project, an absent object, an unreadable object, a `gcp_project` field that is not the one asked about, a version it does not know — all `false`. The reason strings are one line each, in the shape `Status.Detail` uses.

**Tests:** the read against every mismatch above, each asserting `false` and the reason; the happy path against a fake bucket; and **a test that the returned error is never used as a "managed" signal** — the signature makes that a compile-time property, and the test asserts that a nil error and a false bool are both required.

**Command:** `GOFLAGS=-p=2 go test ./internal/infra/managed -run 'Read|Managed' -count=1`

**Depends on:** Task 0.1.

**Commit:** `managed: the marker read, project-number-checked and failing closed`

**Red step — `Expected: FAIL`** — the "a `gcp_project` field naming another project" test against a read that compares only the project name.

### Task 0.3: The property test — a non-managed project never skips a confirmation — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/cli/managed_test.go`

**Steps.** The one test this feature's safety rests on, written as a property rather than a case: **for every combination of the conditions a run can be in** — a marker set or not, `--yes`, `--non-interactive`, `--json`, a terminal or not, a managed marker present, absent, unreadable or naming another project — **an applying step in a project that is not managed never proceeds without a confirmation, and an applying step in a coding agent's session never proceeds at all.** The test is built over `initflow.Conditions` and `initflow.ConfirmClass` (`internal/initflow/stage.go:312-351`) and over the managed read, enumerating the product of the two rather than sampling it. It is a test of the program's *decision*, not of a UI, so it needs no cloud and no terminal: it asserts `CanConfirm`/`CanConfirmRun` and the managed-mode predicate, and it fails the day a new condition is added without a case for it.

**Tests:** the property itself, plus a completeness assertion — a new field on `Conditions` or a new `ConfirmClass` fails a test named for the enumeration, so the property cannot be silently narrowed by an added condition.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Managed|Confirm' -count=1`

**Depends on:** Task 0.2.

**Commit:** `managed: the property test that a non-managed project never skips a confirmation`

**Red step — `Expected: FAIL`** — the property against a predicate that returns "managed" for an unreadable marker.

---

## Phase 1 — The gate

**Why this is second.** The spec asks for a plan gate (its steps 2 to 4) and treats the model's review as it. The reconciliation's finding is that the gate already exists — `tf.Guard` refuses every delete and replace unless the exact address is allow-listed (`internal/infra/tf/guard.go:16-44`) and `tf.Cover` decides what may share a confirmation at all (`internal/infra/tf/cover.go:15-46`, `:63-113`, `:169-182`, `:198-266`, `:609-728`) — and that it was built for Fugaro's own embedded modules, whose resource-type allowlist is tested against them. Reusing it for an agent-authored plan means **a new allowlist, and choosing it is a decision, not a copy** (design finding 22). Phase 1 builds the gate and the choice, with the delete/replace rule inherited unchanged.

### Task 1.0: The plan's path through the gate, and what a refusal does

**Files:** `internal/managed/plan.go`, `internal/managed/plan_test.go`

**Steps.** One function, `Gate(ctx, planPath, spec) (Verdict, error)`, that runs `Guard` first and `Cover`-managed second, in that order, and returns a verdict carrying **every** refusal with the address and the rule that refused it — the shape `Guard` already uses (it lists every refused address rather than the first). `Verdict.OK` is false if either gate refuses, and **the caller's only permitted action on a false verdict is to fall back to a confirmation, never to apply**: the API offers no "apply anyway" and a test asserts the refusal path returns before any apply call. The `spec` is the managed project, its GCP project, the region, the buckets, the registry and the billing account the review showed — the same inputs `Cover` takes (`cover.go:127-162`).

**Tests:** a refusal from `Guard` alone; a refusal from the managed `Cover` alone; both refusing; a plan both allow — asserting only that the function returns `OK` and that no apply was invoked, since applying is Task 3.1's.

**Command:** `GOFLAGS=-p=2 go test ./internal/managed -run 'Gate|Plan' -count=1`

**Depends on:** Task 0.3.

**Commit:** `managed: the plan's path through the guard and the cover, and what a refusal does`

### Task 1.1: The managed allowlist — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/infra/tf/managed_cover.go`, `internal/infra/tf/managed_cover_test.go`

**Steps.** The decision this phase exists for: `ManagedAllowedTypes()` — the resource types a plan in a **managed** project may contain — derived from `allowedTypes` (`cover.go:15-46`) but **not identical to it**, because the embedded modules' list answers "what does Fugaro's own installation need" and this one answers "what may an agent-authored plan contain in a project Fugaro owns". The task writes the list with **one line of justification per entry**, in two groups: **inherited** (the entry is on `allowedTypes` and its reason still holds) and **added** (the entry is here because a managed project legitimately needs it, and the justification names the case). `google_billing_budget` and `google_firebase_project` stay, inherited. **Anything the task cannot justify is left out**, and leaving it out is the correct default: a refused plan falls back to a confirmation, which is a working program, while a permitted plan that should not have been is the failure this feature exists to avoid. `grantRules` and `RolePermissions` (`cover.go:63-113`, `:169-182`) are inherited unchanged, and the task's tests assert that: a `*_iam_member` granting a role outside the list, or a custom role whose permission set differs from its pin, is refused exactly as it is today.

**Tests:** one fixture per **added** entry (allowed) and one per entry deliberately **excluded** (refused), with the fixture named for the entry so a reviewer can see the list and its tests side by side; plus a golden test asserting the list is exactly what the source says, so a widening is a visible diff.

**Command:** `GOFLAGS=-p=2 go test ./internal/infra/tf -run 'Managed|Cover' -count=1`

**Depends on:** Task 1.0.

**Commit:** `tf: the managed project's resource allowlist, one justification per entry`

**Red step — `Expected: FAIL`** — the fixture for an excluded entry against a list that inherits `allowedTypes` wholesale.

### Task 1.2: The attributes a managed plan must still satisfy

**Files:** `internal/infra/tf/managed_cover.go`, `internal/infra/tf/managed_cover_test.go`

**Steps.** `Cover`'s attribute checks (`attributes`, `cover.go:609-728`) already cover the images' registry, schedulers calling this run's jobs, the sink's project, a private bucket and the billing account. This task re-derives them for a managed project the same way Task 1.1 re-derives the type list — **each check re-justified for an agent-authored plan rather than inherited by default, with the ones that only make sense for Fugaro's own embedded modules dropped and said to be dropped** — and it keeps the two that carry the spec's step 3: **the project every resource is in** (`Cover.Projects`, kept as the *wider* rule it is rather than narrowed to "must be in the managed project", since the wider one catches more, including a resource in a second project Fugaro owns), and **the billing account the review showed**, which is what makes a plan that moves billing refuse. **No check outside this set is added.** The design's D27 fixes the checkable set as an enumeration and closes it: a rule that is not on that list is not a check this design has, and adding one is a new decision, not an implementation detail. (An earlier revision of this task smuggled in a `lifecycle.prevent_destroy` check and attributed it to the spec. The spec asks for no such check, the design's D27 does not list one, and it is not buildable as written — `prevent_destroy` is a lifecycle meta-argument that does not appear in a saved plan's JSON, and Terraform refuses such a change at plan time. It is dropped, and the design's D27 row now says the set is closed.)

**Tests:** each retained attribute check with a violating fixture and a passing one; a check **deliberately dropped** with a fixture that now passes, so the dropping is visible in a diff rather than silent; and the project-scope check with a resource in a **second** project, which must refuse — this is the spec's step 3's own case and the task asserts the refusal is by address, not by count.

**Command:** `GOFLAGS=-p=2 go test ./internal/infra/tf -run 'Managed|Cover|Attribute' -count=1`

**Depends on:** Task 1.1.

**Commit:** `tf: the managed plan's attribute checks, re-derived and closed`

### Task 1.3: The cost attributes, flagged and blocked

**Files:** `internal/infra/tf/managed_cost.go`, `internal/infra/tf/managed_cost_test.go`

**Steps.** The spec's step 4 asks the agent to flag "new or upsized resources that bill continuously, such as Cloud Run min instances, Cloud SQL tiers, GPUs and large VMs". This task builds it as a **list, not an estimate** — because Fugaro cannot price an arbitrary plan and must not pretend to (design D28) — and it takes the spec's four examples as the list, one entry each, with the threshold or class stated as a constant in one place: a Cloud Run service or job whose **minimum instance count** is above the stated number, a Cloud SQL tier above the named class, any **GPU attachment**, and a machine type above the named class. **An entry is a property a plan can be above or below, never a resource type.** "A continuously-billing resource of any type the allowlist admits" is deliberately not an entry: nearly every resource a managed project creates bills continuously by existing — a Cloud Run service with min instances 0 included — so that criterion flags essentially every plan, and a gate that flags everything is one that gets switched off (which is the false-positive test below, stated as a rule rather than as a hope). An entry has a **flag** behaviour (recorded in the verdict and shown to whoever confirms) and the ones the owner names have a **block** behaviour (the plan is refused and falls back to a confirmation).

**Tests:** one fixture per entry, each asserting flag-or-block as the list says; a plan that is over the threshold blocked and recorded; and a **below-the-threshold** plan — an ordinary plan creating a Cloud Run job with min instances 0 and a small VM — passing with **no** entry fired, since a cost gate that refuses ordinary work is a cost gate that gets switched off.

**Command:** `GOFLAGS=-p=2 go test ./internal/infra/tf -run 'Managed|Cost' -count=1`

**Depends on:** Task 1.2.

**Commit:** `tf: the cost-bearing attributes, flagged and blocked, never estimated`

---

## Phase 2 — The review, advisory

**Why this is third.** The spec's step 5 makes the agent the plan reviewer and its step 7 keeps the plan and the review for audit. The reconciliation refuses the first as a control (design findings 8 and 9: the reviewer is the coder's own model in the coder's own container, reading text the reviewed party writes) and keeps the second, with one correction: **a plan file is not a summary** and must not be committed (finding 28). Phase 2 makes the review advisory and recorded, and puts the redaction rule in place before anything is written anywhere.

### Task 2.0: What the review is and is not

**Files:** `internal/managed/review.go`, `internal/managed/review_test.go`

**Steps.** The review is produced by the existing `review` stage — `r.stage(ctx, "review", agent.Request{Prompt: reviewPrompt, JSONSchema: VerdictSchema}, …)`, `internal/runner/runner.go:1559`, its prompt `internal/runner/prompts.go:72`, its schema `internal/runner/verdict.go:13` — given the **redacted plan summary** (Task 2.2) rather than the plan file, and its output is stored as an artifact the run record points at. This task changes nothing about the stage's own behaviour (it runs when the recipe says so, and its verdict still gates pull-request readiness exactly as before) and adds the managed-mode rule: **the review's verdict may never be the reason an apply happens.** `managed.MayApply(gateVerdict, reviewVerdict) bool` returns `gateVerdict.OK && reviewVerdict != "changes"` — the review can refuse, never permit — and it is the only function in the package that decides, so a caller cannot compose its own rule.

**Tests:** the truth table of `MayApply` exhaustively, with the case that matters named: **gate OK and review `ship` applies, gate refused and review `ship` does not, gate OK and review `changes` does not.** Plus a test that the prompt handed to the stage is the summary and never a path to the plan file.

**Command:** `GOFLAGS=-p=2 go test ./internal/managed -run 'Review|MayApply' -count=1`

**Depends on:** Task 1.3.

**Commit:** `managed: the review is advisory, recorded, and can only refuse`

### Task 2.1: The review artifact — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/managed/review.go`, `internal/managed/review_test.go`

**Steps.** What is stored, and where. The review's text and verdict go into the **run's own record prefix** in the runs bucket — `runs/<slug>/<run>/`, where `task.json`, `launch.json`, `result.json`, transcripts and `report.md` already live and where `runs/` is deleted at 90 days (`deploy/terraform/gcp/modules/installation/bucket.tf:16-64`) — under a name that says what it is, with the plan's **summary** and not the plan. **The task states plainly what it does not do**: it does not extend retention, because the retention rule belongs to the bucket and changing it is a different decision; and it does not write to any repository, because Task 2.2's redaction rule is not a licence to commit a plan and the spec's step 7 as written is refused (design finding 28). A human-readable line — what changed, why, the concerns — goes into `report.md`, which is the artifact a person actually reads.

**Tests:** the artifact's shape and its prefix; a test that nothing under `runs/` written by this task contains a field of the plan file (the assertion reads the plan fixture's sensitive attribute names and greps the stored bytes for their values); and a test that the task writes nothing outside the run's prefix.

**Command:** `GOFLAGS=-p=2 go test ./internal/managed -run 'Review|Artifact' -count=1`

**Depends on:** Task 2.0.

**Commit:** `managed: the review artifact, in the run's record and never in a repository`

**Red step — `Expected: FAIL`** — the "no plan value in the stored bytes" test against an artifact that stores the plan's raw attributes.

### Task 2.2: The redaction rule — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/managed/redact.go`, `internal/managed/redact_test.go`, `.gitignore`

**Steps.** `Redact(planJSON []byte) (Summary, error)`, keeping **the shape the gate reads and nothing else**: for each resource its address, type, name, the action (`create`, `update`, `replace`, `delete`, `no-op`), the project it lands in, and the allowlisted cost-bearing attributes — and **no attribute value that is not on an explicit list of what may be kept**. Terraform's `Show` output is a plan file's structure, so the default must be "drop the value", with the kept list short and justified line by line, the shape Task 1.1 uses for its allowlist. `.gitignore` gains `*.tfplan` in the same commit, with a comment saying what it is and why (the repository already ignores `/deploy/terraform/**/*.tfstate` and lifts that rule for the state's own `isState` exclusions, `internal/infra/statebucket.go:159-162` — the plan file has no analogous need).

**Tests:** the redaction over a fixture plan with values in it, asserting the sensitive names' values are absent from the output **by value, not by key** — the assertion is a grep of the produced bytes for each sensitive literal, so a rename cannot hide a leak; a test that every kept attribute is on the explicit list; and a test reading `.gitignore` and failing if `*.tfplan` is not in it.

**Command:** `GOFLAGS=-p=2 go test ./internal/managed -run 'Redact' -count=1`

**Depends on:** Task 2.1.

**Commit:** `managed: the plan is redacted by default, and a plan file is never tracked`

**Red step — `Expected: FAIL`** — the by-value leak test against a redaction that keeps every attribute whose name it recognizes.

### Task 2.3: The retention question, written down rather than assumed

**Files:** `docs/design/managed-projects.md`

**Steps.** The audit trail's lifetime is a decision the design leaves to the owner (D31). This task writes the answer **where it is, in the design's D31 row**, once the owner gives it, and — if the answer is "longer than 90 days" — says in the same row that the retention change belongs to the bucket module and to a task that does not yet exist. The task's substance is the *statement*, not a code change: a record that expires is not an audit record, and the plan's job here is to make that impossibility visible rather than let a future reader assume the runs bucket keeps things forever.

**Tests:** the docs suite (Global Constraint 11).

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** Task 2.2.

**Commit:** `docs: managed projects - what the audit record's lifetime is, and what it would cost to change`

---

## Phase 3 — The apply

**Why this is fourth, and why it is a phase of its own.** This is **D21**, the central decision, made concrete: in a managed project, who holds the authority to apply a plan that passed the gate? The plan is written to the design's recommendation — **the run's agent writes the configuration and opens the pull request; a step that is not a coding agent applies the saved plan** — and the alternative the spec proposes (the agent session applies) is Task 3.3, unscheduled until the owner answers D21 the other way. Phases 1 and 2 are useful on their own if 3.1's recommendation is taken; 3.2 is what makes the boundary mechanical; and 3.3 is deliberately a task, not a paragraph, so the owner can read its shape before choosing it.

### Task 3.0: The marker's cloud form, if the owner wants a label

**Files:** `internal/infra/managed/marker.go`, `internal/cli/init_project.go`, `internal/infra/managed/marker_test.go`

**Steps.** **This task is unscheduled until D22 and D23 are answered.** The spec asks for the mark in two places: "a GCP project label (for example `fugaro-managed=true`) and the project config in the project Git repo. Fugaro treats a project as managed only when both agree." The reconciliation rejects the second half as written — the repository config is content the agent writes (finding 17) — and this task builds the first half, **if** the owner wants a label at all: `fugaro_managed=true` on the project, written by the same operator-run path that writes the marker object (Task 0.1), read as an **additional** condition that must agree with the object and never as a substitute for it. The task's tests assert the direction of the disagreement: **the object says managed and the label says nothing → refuse; the label says managed and the object says nothing → refuse**, because either alone is forgeable by a different identity than the one the other requires.

**Tests:** the agreement table; the label write from the operator path; and a test asserting the run's job account holds no permission that can set the label (Global Constraint 4).

**Command:** `GOFLAGS=-p=2 go test ./internal/infra/managed ./internal/cli -run 'Managed|Label' -count=1`

**Depends on:** Task 2.3, and on D22/D23.

**Commit:** `managed: the project label, if the owner wants one, agreeing with the marker object`

### Task 3.1: The apply of a saved plan, by a step that is not a coding agent — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/managed/apply.go`, `internal/managed/apply_test.go`

**Steps.** The recommendation of D21, built: a managed apply runs the sequence the repo already has for its own installation — plan into a saved file (`internal/infra/tf/tf.go:150`, `--out=`), `Show` it (`:160`), gate it (Task 1.0), and `Apply` the **saved file** (`:263-278`, which already refuses to apply without one) — with three additions: **the gate runs between the show and the apply, and its refusal is a hard return**; **the apply refuses when the process is a coding agent's**, reading the same six markers `internal/cli/initsecrets.go:132` defines, through `initflow.CanConfirm`'s own rule rather than a second copy of it; and **the apply requires a marker read that returned a reason a person could read**, so an unreadable marker is not an apply. The identity that applies is the **runner's own apply path** — a step of the work, not of the agent's session — and the task states which service account it runs as and asserts that account can write the resources phase 1's allowlist admits and nothing wider.

**Tests:** the gate's refusal stopping the apply (asserting no `terraform apply` was invoked, by the fake's own call log); the agent-marker refusal, using the same table Task 0.3's property test enumerates; the unreadable-marker refusal; and the saved-plan requirement, which is the existing behaviour and gets a test here so a future refactor cannot drop it.

**Command:** `GOFLAGS=-p=2 go test ./internal/managed -run 'Apply' -count=1`

**Depends on:** Task 3.0 (or Task 2.3, if the owner declines a label).

**Commit:** `managed: the apply of a saved plan, in a step that is not a coding agent`

**Red step — `Expected: FAIL`** — the "no apply after a refused gate" test against a sequence that logs the refusal and applies anyway.

### Task 3.2: The refusals a person reads — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/managed/apply.go`, `internal/cli/root.go`, `internal/managed/apply_test.go`

**Steps.** The spec's proposed CLI surface (`fugaro init project --managed`, `fugaro restore`, `fugaro secrets status`) collides with what exists: `init` has `--create-project` and no `project` noun (`internal/cli/init.go:344-386`), and `secrets` has `ls` (`internal/cli/secrets.go:40-61`). This task settles the **names and the exit codes** the way the design recommends (D34): `--managed` as a flag on the existing `init --create-project`, `fugaro restore` at the top level, the missing-secret column folded into `secrets ls` (Task 5.1). Exit codes stay `0/1/2` (`internal/cli/root.go:16-28`, `:42-51`): **a managed-mode refusal — an out-of-scope plan, a marker mismatch, an unreadable marker, a coding agent's session — is `1`**, because it is the user's to act on, and a read that fails is `2`. Every refusal is one line naming what refused and what to do, in the shape `initflow`'s refusals use (`stage.go:160-166`, `:275-305`), and **no refusal ever names a way around the marker**, because a recipe for getting past the check has no business in text an agent reads (`IDEHint`'s own reasoning, `stage.go:286-293`).

**Tests:** each refusal's exit code; the one-line shape; and a test that no refusal string matches the marker names as an instruction — the same spirit as `IDEHint`, asserted rather than intended.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Managed|Refus|Exit' -count=1`

**Depends on:** Task 3.1.

**Commit:** `cli: the managed refusals, their names and their exit codes`

### Task 3.3: The alternative — the agent session applies — SECURITY-SENSITIVE, independent opus review, UNSCHEDULED

**Files:** `internal/initflow/stage.go` (or its successor), documented in `docs/design/managed-projects.md` §5

**Steps.** **This task is the alternative in design D21, and it does not start unless the owner answers D21 in favour of the spec's shape.** Written out rather than left implicit, so the decision is made with its shape visible: `CanConfirm` (or the managed-mode predicate) gains a case where a marker plus a managed project plus a passed gate still returns `false` for every class **except** the Terraform apply classes the gate covered, and `AgentRefusal` gains a branch explaining that the step applied because the project is managed. The task must also carry, in the same PR, **the compensating controls the design says would be needed in that world**: the marker read asserted against forgeability by the run's own identity (a test, not a sentence), the gate's verdict recorded in the run record before the apply, and the review recorded, since in that version the review is the only thing a human will read. **The plan states its own opinion, as the design does: the recommendation is not taken here, and if it is taken the cost is that the marker becomes the single factor between a prompt-injected agent and the operator's cloud.**

**Tests:** whatever the answer requires, plus the property test's extension — the property must stay "a non-managed project never skips a confirmation" in either world, and gain "a managed project applies only what the gate admitted".

**Command:** `GOFLAGS=-p=2 go test ./internal/cli ./internal/managed -count=1`

**Depends on:** Task 3.2, and on **D21 answered against the recommendation**.

**Commit:** `managed: the agent session applies in a managed project, with the compensating controls (D21, alternative)`

---

## Phase 4 — Single purpose and the budget

**Why this is fifth.** Two of the spec's promises have nothing built under them: "single purpose" — "if Fugaro finds resources it didn't create, it warns and falls back to confirmations" (design finding 20: discovery runs the other way today, finding Fugaro's resources by label and never foreign ones) — and a budget **amount** that comes from the operator rather than from a default (finding 23: the budget and its 50/90/100% alerts exist; the amount is optional and the alerts email). Phase 4 builds both, and deliberately does **not** build the hard stop (D29).

### Task 4.1: The whole-project inventory, and the fallback — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/managed/inventory.go`, `internal/managed/inventory_test.go`

**Steps.** A read-only inventory of everything in the project, compared against what the installation's own state accounts for, with the spec's own consequence: **a resource the state does not account for falls the run back to confirmations and warns** — it never halts and never deletes. The direction is a Global Constraint in spirit and a test here: **a false positive must not stop the operator's work**, because a check that halts on an unrelated service is a check the operator disables. The inventory is built from the project's own resources by list-and-label, in the shape `internal/infra/discover.go` already uses (`:325`, `:392`, `:506`), and it reports what it found and why it thinks so, one line per resource, so a person can disagree with it. It reads and writes nothing.

**Tests:** an inventory with a foreign resource → confirmation fallback plus a warning naming the resource; every resource accounted for → no fallback; **an inventory that cannot be read → confirmation fallback** (the fail-closed rule, Global Constraint 3, applied to this check too); and a test that the check performs no write, asserted from the fake's call log.

**Command:** `GOFLAGS=-p=2 go test ./internal/managed -run 'Inventory' -count=1`

**Depends on:** Task 3.2.

**Commit:** `managed: the whole-project inventory, and the fallback to confirmations`

**Red step — `Expected: FAIL`** — the "cannot read → fall back" test against a check that treats an unreadable inventory as clean.

### Task 4.2: The budget amount, required and written down

**Files:** `internal/cli/init.go`, `internal/cli/init_project.go`, `internal/cli/init_budget_test.go`

**Steps.** Today the budget is opt-in and its three flags must come together (`--budget`, `--budget-currency`, `--billing-account`, refused unless all three: `internal/cli/init.go:356-358`, `:800`), and the module's thresholds default to `[0.5, 0.9, 1.0]` (`deploy/terraform/gcp/modules/installation/variables.tf:182`). For a **managed** project this task makes the amount **required at creation** — the design's D30 recommendation — with a refusal that prints a suggested value and says why it is being asked for; and it writes the amount and the currency into the project's own persisted record (Task 0.1's marker, extended) so a later run and a restore can read what was chosen. Non-managed projects keep today's opt-in behaviour exactly, and a test asserts that, because the change must not reach them.

**Tests:** a managed creation with no amount refused, with the suggestion in the message; a managed creation with an amount recorded; a **non-managed** creation unchanged; and a test asserting the managed default is absent — there is no constant a caller can fall back on.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Budget|Managed' -count=1`

**Depends on:** Task 4.1.

**Commit:** `init: a managed project's budget amount is required, and no default exists`

**Red step — `Expected: FAIL`** — the "no default" test against a constant default of 50.

### Task 4.3: The budget alerts, stated as they are

**Files:** `docs/design/managed-projects.md`

**Steps.** The spec promises "budget alerts at set fractions of a monthly amount … sent to the project owner", and the repo already has the budget with `[0.5, 0.9, 1.0]` emailing the billing account's admins (`deploy/terraform/gcp/modules/installation/budget.tf:11-36`). This task writes the two differences into the design's D28/D29 rows rather than into code: **what the alerts actually reach is the billing account's admins, not "the project owner"** — an installation's owner is a Fugaro concept and the billing account's admins are a GCP one, and the two need not be the same people; and **the budget's `projects` filter is already project-scoped**, so the managed project's budget covers what the spec wants it to cover. No code changes.

**Tests:** the docs suite (Global Constraint 11).

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** Task 4.2.

**Commit:** `docs: managed projects - who a budget alert actually reaches`

---

## Phase 5 — Secrets and restore

**Why this is sixth.** The spec's Secrets and Disaster recovery sections are the two that depend on decisions about *promises*, and both are cheap once the fact (phase 0) and the gate (phase 1) exist. One correction is load-bearing: **the state bucket lives in the managed project** (`fugaro-tfstate-<gcp-project>`, `internal/cli/init.go:348`, `:1198`; `internal/infra/statebucket.go:77`), so a restore cannot restore state — it can only apply the repository's configuration from zero (design finding 31). Phase 5 builds the two commands and the check that runs them.

### Task 5.1: The missing-secret column on `secrets ls`

**Files:** `internal/cli/secrets.go`, `internal/cli/secrets_test.go`

**Steps.** The spec's `fugaro secrets status` "shows which required secrets are missing after a restore" (design finding 34: nothing reports this today). Built as the design recommends (D33), **one column on the command that exists**: `fugaro secrets ls` already reads metadata only, with no access call, and reports each name's scope and version count (`internal/cli/secrets.go:531-547`). The new column answers "required by something this project knows about and absent", where "required" is the union of what the repository's `fugaro.yaml` declares and `config.ReservedSecrets` (`internal/config/config.go:419-427`). **Nothing about the value is read, at any point** — the command's own doc comment says so today and the task keeps that sentence true with a test that asserts no access call is made.

**Tests:** the column over a fake secret set with one missing; the "required" union from both sources; and a test asserting the command makes no value-access call, which is the existing promise held as a test.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Secrets' -count=1`

**Depends on:** Task 4.3.

**Commit:** `secrets: ls says which required secret is missing, and still never reads a value`

### Task 5.2: `fugaro restore`, a from-zero apply

**Files:** `internal/cli/restore.go`, `internal/cli/restore_test.go`

**Steps.** What the command does, in the order that keeps it honest: take the new project ID (the old one stays reserved about 30 days, so a restore always creates a new ID and updates the repository's `gcp_project:` line — the one field the committed `fugaro.yaml` carries, `internal/cli/init_fugaroyaml.go:24-28`); create the project through the same path `init --create-project` uses; **apply the repository's configuration from zero, reading no state** — the task asserts that no state read happens, because the state bucket is in the project that was lost and a restore that needs it will fail exactly when it is needed (finding 31); re-publish the GCS objects the repository does not hold (the project's recipes, `internal/recipe/recipe.go:35-36`, `internal/cli/recipes.go:327-362`, and the project layer) or refuse, naming them; and end by printing what was **not** restored, as a list rather than as a caveat: in-flight runs and RTDB counters (the spec's own "live state is lost"), the spend history and kill-switch state (`internal/budget/model.go:30-38`, `:57-72`), any secret value (finding 35), and the plan files that were never stored (Task 2.1). It is a typed-confirmation command throughout: every step in it is money or permanent, which is `initflow.Typed`'s definition (`stage.go:319-323`), and it is refused in a coding agent's session like everything else.

**Tests:** the sequence over fakes, asserting no state read; the fresh-ID path and the `gcp_project:` update; the not-restored list printed; the typed confirmation required for each permanent step; and the coding-agent refusal.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Restore' -count=1`

**Depends on:** Task 5.1.

**Commit:** `restore: a from-zero apply into a fresh project ID, and the list of what is not restored`

**Red step — `Expected: FAIL`** — the "no state read" assertion against a restore that reads the state bucket.

### Task 5.3: Check 60, the managed apply and the restore round trip — OWNER-RUN, no agent performs any step

**Files:** `docs/gcp-live-checklist.md`

**Steps.** **This task is the plan's one owner-run step, and no agent performs any part of it.** It writes `## Check 60a` and `## Check 60b` into the checklist, with **Check 43's guard block verbatim** re-run from a fresh shell before every step (three hard stops; `--project fugaro` on every `fugaro` command; `--project "${P:?}"` on every `gcloud` command), and with the summary table's row, since this task is the number's one writer. **60a** is the managed apply: a sandbox managed project created at the owner's terminal, a plan the gate admits applied **with no confirmation**, the same plan with one resource moved to a second project **refused**, a plan with a delete **refused** without `--allow-delete`, a **non-managed** project still asking for its confirmation, a marker whose `gcp_project` names another project read as **not managed**, and the FACT that the run's own identity **could not write the marker** (the negative assertion, and the one that makes the rest meaningful). **60b** is the restore round trip: the sandbox managed project destroyed, `fugaro restore` producing a **new** project ID, the `gcp_project:` line updated, the configuration applying from zero with **no state read**, the missing-secret column naming the third-party keys to re-enter by hand, and the not-restored list printed. The restore step deletes only what the check created, and its final assertion's every line must read `0` — counts, not an empty-looking line — in the shape Check 57's restore already uses.

**Tests:** the future `TestChecklistChecksAreGuarded` (project-centric plan Task 6.3) will assert both sections carry Check 43's three hard stops and its project pinning; this task writes them so that assertion can pass.

**Command:** none — this is an owner-run live check, and it is the only step in this plan that touches a cloud.

**Depends on:** Task 5.2, and on every phase above it.

**Commit:** `checklist: Check 60, the managed apply and the restore round trip (owner-run)`

### Task 5.4: The design follows the owner's decisions

**Files:** `docs/design/managed-projects.md`, `docs/plans/2026-10-16-managed-projects.md`

**Steps.** The desktop-console plan's Task 5.0, applied here: when the owner answers **D21 to D38**, this task rewrites the design's §5 table with the answers in its last column, moves any answer that changes a recommendation into the affected section of both documents, and — where an answer reverses a recommendation (D21 and D29 are the two that can) — rewrites the tasks that depended on it before anything is built from them. The rule the plan inherits: **answering a decision is not approving the tasks**, and this task changes no code.

**Tests:** the docs suite (Global Constraint 11).

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** Task 5.3, or on the owner's answers arriving earlier, whichever comes first.

**Commit:** `docs: managed projects - the owner's answers, in the design's own table`

---

## Phase 6 — Later, unscheduled

### Task 6.0: The billing hard stop — UNSCHEDULED until D29

**Files:** a design of its own, if the owner wants one.

**Steps.** The spec's optional hard stop — "on 100%, a small function disables billing or scales services down" — is **not built here**, and the plan says why in the design's terms (D29, finding 25): it needs the Cloud Billing Budgets API, a Pub/Sub topic and a function holding billing-account admin, which is a **new always-on credential with money-adjacent power** in a project whose premise is that the run there has broad rights — and the budget's notification path today has no client, no topic and no enforcement in this tree. If the owner wants it, it gets its own design and its own decision, because what it adds is a credential, not a flag. **This row exists so that its absence is a decision rather than an oversight.**

### Task 6.1: Moving an existing project into managed mode — UNSCHEDULED until D24

**Files:** a design of its own, if the owner wants one.

**Steps.** The spec's own open question, and the answer this plan is written to is **no**: a project is managed because **Fugaro created it and recorded that fact** (phase 0), so an existing project cannot be flagged after the fact, and **both of the owner's installations keep today's confirmations in full** — including `belong`, which is the reason that rule is worth having (design §2, finding 19). If the owner wants a path, it is an owner-run, typed-confirmation-per-project procedure with its own check, and the design's D24 row carries the cost of the alternative: the marker stops meaning "Fugaro made this" and starts meaning "someone said so", and there is no mechanical difference left between the sandbox installation and `belong`. **Unscheduleable without that answer.**

## Cross-task dependencies

Every task's "Depends on" is a *build* dependency, and none of them is a merge dependency beyond its own phase: each phase leaves a working artifact even if the next never ships. **Phase 0 is the one exception, and it is not an exception to the rule but the rule's own statement**: phases 1 to 5 read the marker phase 0 writes, so 0 is a real precondition — and that is deliberate, because the marker is the privilege boundary and there is no safe way to build the gate before the fact it is gated on exists.

**Three dependencies that are real and are *merge* dependencies, listed here because a reader checking the "nothing in phase *n* is required by phase *n-1*'s merge" claim would otherwise read them as exceptions it missed:**

- **Task 0.1 must merge before Task 0.2.** The writer and the reader are one shape; merged the other way, the read has nothing to read and its tests would be written against a fixture that the real writer then contradicts.
- **Task 1.0 must merge before Task 3.1.** The apply path calls the gate; without it there is an apply path with no verdict to check.
- **Task 5.2 must merge before Task 5.3 (Check 60).** The check exercises `fugaro restore`; merged the other way the check cannot run, and — unlike the console's staged workflows — there is no gate or release that depends on it, so the inversion costs nothing but a re-run.

**And the fact a reader should not have to infer: no task in this plan changes any behaviour for a project that is not managed.** Every phase's tests assert the non-managed path unchanged, and Task 0.3's property test is the single place that claim is made once and exhaustively rather than per-task. **That is the plan's answer to the risk this feature carries**: the change that would be catastrophic is not a managed project applying the wrong thing, it is an ordinary project quietly stopping asking.

## Decisions this plan defers to the owner

Its own, continuing the design's D-series with **C1 onward**, each vetoable before its phase starts. **None is answered**: the design's D21 to D38 have empty answer columns, and this list is the same — nothing below stands as recommended-and-accepted.

| # | Question | Recommendation | Owner's answer |
|---|---|---|---|
| **C1** | Does phase 0's marker live beside the runs bucket's `fugaro/project.json` (the recommendation), or in a bucket of its own? | **Beside it**, because the read is the same read: the bucket's project number is the check that makes the mark mean anything (`internal/infra/project.go:42-56`), and a second bucket is a second IAM surface for no extra assurance. | |
| **C2** | Does the marker record the budget amount and the creation time (Task 4.2 extends it), or stay the three fields `ProjectMarker` has? | **Extend it**, in a versioned shape as `ProjectMarker.Version` already is, because a restore needs to know what the budget was and a version field is how a reader refuses a shape it does not know. | |
| **C3** | Is Task 3.0 (the project label) wanted at all? | **Only if the owner wants a second, independent mark.** The object is enough and the label adds a second identity that must be trusted; the recommendation is that the label is optional and, if present, is an *additional* agreement condition rather than a substitute. | |
| **C4** | Does the review's prompt get the redacted summary or the plan JSON? | **The summary, always.** The summary is what the deterministic gate and the review both read, so there is one artifact to redact and one to test (Task 2.2); handing the review a richer document would create a second, less-tested path out of the plan. | |
| **C5** | Does this plan ship all six phases in one release, or phase by phase? | **Phase by phase**, each independently mergeable and each useful alone: phase 0 alone makes "Fugaro created this" a readable fact, phase 1 alone is a reusable managed gate, and phase 5's restore is useful before anyone opts into managed mode. D35 places the *feature* after 0.8.0; it does not require the phases to land together. | |
| **C6** | Does `fugaro restore` need its own Check number, or does it share 60? | **It shares 60, as 60b** — the 43a-43f sub-lettering convention, and the two sections genuinely cannot run at the same moment (60a needs a managed project, 60b needs one destroyed). A second number would be a second reservation for one feature and one phase. | |

## Open items, listed rather than fixed

Each is something this plan knows it has not settled, with the reason it is left open rather than answered by assertion:

1. **The full cost of D21's alternative is not written as a design, only as a task.** Task 3.3 sketches it. If the owner chooses it, the design's D21 row is where its consequences belong — the marker becoming the single factor, the compensating controls it needs — and a task is not a design. **Listed because a reader might otherwise take Task 3.3's existence as evidence the alternative has been thought through as far as the recommendation.**
2. **Whether a project-level label can be made unwritable by the run's identity is unverified (design U1).** Task 3.0 asserts it with a test that reads the IAM; if the test cannot be written without a live read, Task 3.0 says so and the label is refused instead. **The recommendation does not depend on the label, which is why this is an open item and not a blocker.**
3. **What a plan JSON of a managed-project shape actually contains is not measured (design U3).** Task 2.2's redaction is written against Terraform's documented plan structure and a fixture; the fixture is synthetic. The task's by-value leak test is the mitigation, not a guarantee about every provider.
4. **The recipes' and the project layer's re-publication path in a restore is named, not designed (Task 5.2's step).** It is the piece of the restore most likely to be wrong, because those objects live in the bucket that died with the project. **The task's instruction is to refuse and name them rather than guess**, which is a correct behaviour and not a complete restore; if the owner wants a complete one, that is a task in its own right.
5. **`fugaro init --create-project`'s own confirmation is unchanged by this plan.** A managed project is still created by a typed confirmation at a terminal, because it is a new project and billing (`initflow.Typed`, `stage.go:319-323`). **The spec's "no prompts for cloud operations in managed projects" therefore starts after creation, not at it** — stated here because the design's D21 discussion is about applies and a reader could reasonably think creation is included.
6. **The console's Projects screen will not show anything about managed mode until a `--json` shape carries it**, and this plan adds no shape. That is the design's D37/D38 answer (the console holds no credential and stays read-only) and it is also a deliberate omission: a fact whose whole purpose is to change what gets applied is a poor thing to render in a view-only window with no way to change it.

## OWNER APPROVAL GATE

**Nothing in this plan is approved and nothing in it is built. No task starts until the owner gives an explicit go, and no decision it depends on has been answered.** The deliverable of the pull request that adds this file is the two documents: the reconciliation ([docs/design/managed-projects.md](../design/managed-projects.md)) and this plan. **Concretely:** no code exists for any of it; no file under Task 0.1 onward has been created; `docs/gcp-live-checklist.md` is untouched, which is why Check 60 is *named* and reserved here and its rows and sections are written by Task 5.3; and this plan's own commands have not been run, because there is nothing to run them against.

**The four decisions that gate the most, in the order they matter:**

1. **D21 — does a managed project remove the refusal of live applies from a coding-agent session?** This is the central security decision in the whole feature, and it is the one the spec answers implicitly ("every cloud operation runs without prompts") and this plan does not. The recommendation is **no**: keep the refusal, and move *where the apply happens* rather than *whether an agent may apply*. Its alternative's cost is in the design's row, and Task 3.3 is that alternative written as a task so it can be read before it is chosen.
2. **D22, D23 and D24 — the marker: where the authority lives, who may write it, and what "Fugaro created it" means.** Every phase below reads phase 0's marker, so these decide what phase 0 builds. The recommendation keeps the authority in the cloud, fail-closed, unwritable by anything a run holds, and means both of the owner's existing installations — **including `belong`** — keep today's confirmations in full.
3. **D29 — the hard stop.** The recommendation is **not to build it**: it needs a new always-on credential with billing-admin power, and the alerts already exist. This is the decision with the largest privilege consequence after D21.
4. **D26 and D27 — the review's role.** The recommendation makes the deterministic checks the gate and the model review advisory and recorded. The spec's step 5 read as a control is the alternative, and it is the one the reconciliation refuses on the evidence of the code: same model, same container, attacker-writable input.

**Answering a decision is not approving the tasks.** When the owner answers D21 to D38, Task 5.4 writes the answers into the design's own table first, and any task whose shape the answers change is rewritten before it is built. **Until then this plan is a document, and every step in it is someone else's future work.**
