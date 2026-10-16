# Fugaro-managed projects: implementation plan

**Goal:** build what the owner's spec asks for — autonomous cloud operations in a project Fugaro created — in the shape the owner chose on 2026-10-16: **a deterministic gate decides, a recorded model review advises, and in a managed project a coding agent's session may apply — with the gate, the cloud marker, the owner-written delete list (the object Task 1.4 defines — its home, its format and its one writer — D74) and the budget as the controls, and the residual risk stated in the design's §6 item 4.** The reconciled design is [docs/design/managed-projects.md](../design/managed-projects.md). **Nothing here is approved and nothing here is built: no line of this plan has been written or run, and this PR (the one that adds this plan) is docs-only — it touches exactly three files, the owner's spec kept verbatim (`docs/design/managed-projects-spec.md`), the design and this plan, and nothing else at all**, not `docs/gcp-live-checklist.md` (the check number this plan reserves, **67**, is *named* here and its rows and sections are written by Task 5.3 when the phase that needs them ships), not the checklist's summary table, not `README.md`'s Roadmap (the repository keeps no index of `docs/design/` or `docs/plans/`; the design's §1.1 shows the evidence), not `go.mod`, not any Go, Terraform or product-behavior file under the "Files" lists below. Task 0.1 onward is future work, and every one of it is gated on the OWNER APPROVAL GATE at the end of this file.

**Status: five decisions are answered, and no task is approved.** The design's §5 carries twenty decisions, **D55 to D74** — the eighteen reconciled from the spec, **D73**, which was added when the owner's one-line addendum turned out to be ambiguous, and **D74**, added on review because the owner-written delete list D55's answer relies on had no home, format or writer written down — continuing the numbering from the D21-D54 blocks the open desktop-console v2 and voice work hold. **The owner answered five of them on 2026-10-16 — D55, D56, D57, D63 and D64 — and every other owner-answer column is still empty.** **D55 is the central one and the answer is against the design's recommendation: the coding-agent refusal is removed for a managed project**, so the managed apply happens in the agent's session and the controls are the deterministic gate, the cloud marker, the owner-written per-address delete list (Task 1.4: an object in the project's own runs bucket that no credential a run holds can write, read fail-closed, D74), the budget and D72's scope rules; the design's §1 recommendation 4, D55's own row and §6 item 4 state that consequence and what is **not** protected, and **Tasks 3.1 and 3.3 are rewritten to it** — 3.1 builds the removal, and 3.3 is the separation the design used to recommend, kept as the alternative so the owner can read what the answer gave up. Every other task whose whole shape depends on a decision still says so in its own heading and carries the number — **D56**/**D57** (the marker's cloud form) in Task 3.0, **D56**–**D58** (what "Fugaro created it" means, and where the fact is recorded) in Task 0.1, **D58** again in Task 6.1, and **D63** (the hard stop) in Task 6.0 — and **an answer is not an approval**: none of them starts until the owner passes the OWNER APPROVAL GATE at the end of this file.

**Spec:** [docs/design/managed-projects-spec.md](../design/managed-projects-spec.md), the owner's draft of Oct 15 2026 kept verbatim, and the reconciliation [docs/design/managed-projects.md](../design/managed-projects.md) written from it. The reconciliation's §3 findings are the plan's working list: each **Gap** is a task below or a row in "Open items" saying why it is not, each **Rule** is a Global Constraint, and each **Wrong** is a thing this plan does *not* build (the model review as the gate; a raw plan file in the repository; a repository config as the authority; "disposable" as a licence to lose state). The design's §9 UNVERIFIED list is the plan's first tasks.

**Architecture — what the seven phases build, in one paragraph (Phase 0 to Phase 6).** Phase 0 makes "Fugaro created this project" a fact the program can read after the run that created it has ended, and makes the read **fail closed** — a marker that cannot be checked means *not managed*, exactly as `infra.ReadProjectMarker` already behaves for the runs bucket. Phase 1 turns the two deterministic gates that exist for Fugaro's own embedded modules (`internal/infra/tf/guard.go`'s `Guard` and `internal/infra/tf/cover.go`'s `Cover`) into the gate a managed project's applies run behind: `Guard` unchanged, `Cover` re-derived as a managed-project allowlist whose widest entries are decided rather than inherited, **and `Guard`'s allow-list read from a place the applying command cannot write** — an owner-written object at the root of the managed project's runs bucket, absent or empty meaning every delete and replace is refused (Task 1.4, D74). Phase 2 makes the model's review **advisory and recorded** — it runs, its output is stored, its verdict can only *add* a refusal, and it is never the reason a plan is applied — and puts the redaction rule in place so no plan file ever reaches a repository. Phase 3 is the apply path, and it is where D55's answer lands: **the coding-agent refusal is removed for a managed project and the apply happens inside the agent's session**, on a saved plan that passed the deterministic gate, with the cloud marker, the owner-written delete list (Task 1.4's object, which this apply reads and whose own command-line flags it refuses) and the budget as what replace the refusal; it ships **inert until phase 4's inventory and required budget land**, and the shape the design used to recommend — keep the refusal, and apply where no agent is — is a task of its own, kept as the alternative. Phase 4 builds the two things the spec's Cost safeguards and "single purpose" need that do not exist: a whole-project inventory read, and the budget's **required** amount with the existing alerts. Phase 5 is secrets and disaster recovery: one new column on `fugaro secrets ls`, and a restore that promises a **from-zero apply**, not a state restore, because the state bucket lives in the project that was lost. Phase 6 is deliberately empty of work: it lists the two things a reader will otherwise look for (the billing hard stop, and moving an existing project into managed mode) and says which decision each waits on.

**What this plan does not do, stated once so it is not read as an omission.** It removes `CanConfirm`'s agent-marker refusal **for a managed project only** (D55's answer of 2026-10-16) — the one place where a refusal is deleted rather than narrowed, and the task that does it (3.1) is SECURITY-SENSITIVE, carries the design's residual-risk paragraph in its PR description, and is unapproved until the owner passes the gate; it does not put a plan file in the repository; it does not introduce a Cloud Billing Budgets API client, a Pub/Sub topic or a billing-admin function (D63); it does not add a credential anywhere the console, the launcher or the agent can read one; and it does not touch `belong`.

**Builds on merged code (what each phase stands on).** Verified on `origin/main` at **`a573870`**, which is also this plan's writing baseline (the branch this was written on was identical to it, working tree clean):

| Phase | Merged work it builds on |
|---|---|
| 0 | `internal/infra/project.go` (`ProjectMarkerObject`, `ProjectMarker`, `ReadProjectMarker`), `internal/infra/projectcreate.go` (`CreateProject`, `AddFirebase`, `LinkBilling`), `internal/cli/init_project.go` (the `Apply` stage, `canType`), `internal/initflow/stage.go` (`ConfirmClass`, `CanConfirm`, `Conditions`), `internal/cli/initsecrets.go` (`agentMarkers`, `agentMarker`) |
| 1 | `internal/infra/tf/cover.go` (`allowedTypes`, `grantRules`, `Cover`, `RolePermissions`, `NotCovered`, `attributes`), `internal/infra/tf/guard.go` (`Guard`, `knownActions`), `internal/infra/tf/tf.go` (`Plan`, `Show`, `Apply`), `internal/infra/workdir.go` (`PlanFile`, `InstallationWorkdir`), the fixture corpus `internal/infra/tf/{cover_test,cover_golden_test,cover_grants_test,fake_test}.go`; and, for Task 1.4, `internal/infra/project.go` (`ProjectMarker`, `ReadProjectMarker` — the read discipline the delete list copies) and `deploy/terraform/gcp/modules/installation/bucket.tf` (the `fugaro/`-root objects and the jobs' prefix-conditioned grants) |
| 2 | `internal/runner/runner.go` (the `review` stage, `ReviewerIsCoder`), `internal/runner/verdict.go` (`VerdictSchema`), `internal/runner/prompts.go` (`defaultReview`), `internal/runstore`, `deploy/terraform/gcp/modules/installation/bucket.tf` (the `runs/` prefix and its 90-day rule) |
| 3 | phase 0's marker read, phase 1's gate, `internal/cli/init_review.go` (`runCovers`, `planOnlyReview`), `internal/cli/init.go` (`--plan-only`, `--yes`), `internal/cli/root.go` (the exit codes) |
| 4 | `internal/infra/discover.go` (label discovery), `internal/backend/gcp/names.go` (`LabelManaged`, `LabelProject`), `deploy/terraform/gcp/modules/installation/budget.tf`, `internal/cli/init.go` (`--budget`), `internal/budget/{model,kill}.go` |
| 5 | `internal/cli/secrets.go` (`newSecretsLsCmd`), `internal/config/config.go` (`ReservedSecrets`), `internal/recipe/recipe.go` (`ObjectPrefix`), `internal/cli/recipes.go`, `internal/infra/statebucket.go`, `docs/design/project-centric.md` §4 (the migration) |
| 6 | everything above |

## Phase summary

| Phase | What ships | Owner-run steps | Depends on |
|---|---|---|---|
| **0. The fact** | the creation fact recorded at creation and a marker read that fails closed; **the property test that a non-managed project never skips a confirmation** | nothing | nothing |
| **1. The gate** | `Guard` wired into a managed apply path, `Cover` re-derived as a managed-project allowlist, the plan summary, the cost attributes, **and the owner-written delete list the guard's allow-list is read from (Task 1.4, D74)** | nothing | phase 0 |
| **2. The review, advisory** | the review stored as an artifact, the redaction rule, the retention decision written down | nothing | phase 1 |
| **3. The apply** | the apply path of **D55's answer**: the coding-agent refusal is removed **for a managed project**, the apply runs inside the agent's session on a saved plan that passed the deterministic gate, and the cloud marker, the owner-written delete list (Task 1.4) and the budget are the controls — **shipped inert until phase 4's inventory and required budget land** — plus the exit codes and the refusals | nothing until the owner passes the approval gate; Task 3.3 is the shape the design used to recommend (keep the refusal, apply where no agent is) and no agent starts it | phase 2 |
| **4. Single purpose and the budget** | the whole-project inventory read with its confirmations fallback; the budget **required** at creation, alerts as they exist | nothing | phase 3 |
| **5. Secrets and restore** | the missing-secret column on `secrets ls`; `fugaro restore`, a from-zero apply into a fresh ID; **Check 67**, written as 67a and 67b | **all of phase 5's check**: the sandbox managed project, the apply, the refusal on a marker mismatch, the restore round trip (Check 67) | phase 4 |
| **6. Later, unscheduled** | the billing hard stop; moving an existing project into managed mode | each, when the owner decides | phase 5 |

**Nothing in this table depends on a later row.** Phase 6 is deliberately empty of work: it lists the two things a reader will look for and gates each on a decision this plan does not presume.

## Check numbers this plan takes (67)

`docs/gcp-live-checklist.md` on `main` (`a573870`) has its highest written `## Check N` **section** at **47** (`## Check 47`), and its **register** — the paragraph that says which numbers are reserved — ends at **57** (`:250-257`: "48–56 are reserved by the project-centric plan … and 57 is reserved by the compute-backend design"). Every number above 57 is spoken for, and this is the allocation, applied in full here rather than re-derived:

| Number | Claimed by | State |
|---|---|---|
| 42, 43 (43a-43f), 44, 45, 46, 47 | the config-transitions, registry-remote-repository, price-catalog, mixed-credentials and OAuth-reviewer work | **written on `main`, not run** (`## Check 42` … `## Check 47`) |
| **48-56** | **`docs/plans/2026-10-14-project-centric.md`**, §"Check numbers this plan takes (48 to 56)" | **reserved in that plan** (PR #366, merged as `c86eb92`), one number per phase — except that phase 1 and phase 1b hold **49 and 50** between them and `## Phase 3` holds **52 and 53**, so the nine numbers 48-56 are spread over **eight phase sections** (0, 1, 1b, 2, 3, 4, 5, 6) rather than one each over nine |
| 57 | the compute-instance Docker backend, design `compute-backend.md` §7 (PR #375) and Check 57's row on `main` | **the summary row is on `main`; the section is not** — it lives in `docs/design/compute-backend.md` §7.2. A number whose section lives in another document is still taken |
| **58** | **`docs/design/artifact-runs.md`** §11, "Check 58, in outline" | **taken by that design**, merged on `main` (`docs/design/artifact-runs.md` exists at `a573870` and its §11 is written) |
| **59**, written as **59a** and **59b** | **`docs/plans/2026-10-15-desktop-console.md`** (console v1), §"Check numbers this plan takes (59)" | **reserved in that plan**, and its rows are written by its Task 4.3 |
| **60** | **`docs/design/budget-token-channel.md`** — the project-centric plan's Task 3b.1 (**PR #399**, based on the `next` line), §"The check outline: Check 60, owner-run, sandbox only" | **reserved in that design**, read on **`origin/next`** at `e591c78`; its row and section are written by the implementation task that design describes, and `main` at `a573870` does not carry the claim at all |
| **61** | **`docs/plans/2026-10-14-project-centric.md`**, §"Check numbers this plan takes (48 to 56, and 61)", phase 6 — the folder export's fence (`fugaro project export`, Task 6.5; the 2026-10-15 revision, **PR #405**) | **reserved in that plan**, read on **`origin/next`** at `e591c78`; its row and section are written by that plan's Task 6.5 when phase 6 ships |
| **62-63** | the desktop-console v2 work (**PR #426**) | **reserved by that design** |
| **64-66** | the voice work (**PR #428**) | **reserved by that design** |
| **67**, written as **67a** and **67b** | **this plan, phase 5**: **67a** the managed apply on a real sandbox project — the apply happening with no confirmation, a non-managed project still refusing, the marker mismatch refused, and the run's credential unable to write the marker; **67b** the restore round trip into a fresh project ID | **reserved here** |

**Rows 62 to 66 are reservations this tree cannot read, and that is not a reason to take their numbers.** 60 and 61 *are* readable — on the `next` line rather than on this branch's `main`: **60** in `docs/design/budget-token-channel.md` (the project-centric plan's Task 3b.1, PR #399) and **61** in the project-centric plan's phase 6 (the folder export's fence, Task 6.5, the 2026-10-15 revision, PR #405), both read here with `git fetch origin next` and `git show FETCH_HEAD:<path>` — a read and not a merge, and nothing on this branch's baseline changes because of it. **62-63** and **64-66** are held by the two open D-series PRs (#426 and #428), whose documents are on neither `main` at this baseline nor `next`, so `grep` finds nothing for them — but the register's ruling is about the number, not about what a reader can see: a number, once reserved, is never reused or renumbered, even when its section lands out of order (`docs/gcp-live-checklist.md:182-185`), and "a number, once reserved, is never reused or renumbered" (`:224-225`). The block 60-66 is therefore taken, and the next free number is 67.

**How 67 was chosen, in the order the register reads.** "The next free number" cannot be read off `main`'s headings alone, because a number is taken three ways — a written section, a written summary row whose section lives in another document, and a **reservation** in a plan or a design whose phase has not shipped — and because the reservations for 60 to 66 live in documents that are not on this branch at all. The register's ruling settles it, and the steps are:

1. **The `## Check N` sections written in the checklist run 19 to 38 and 40 to 47**, with **39** absent (it was split into 39a-39f, which are sub-letters inside the register rather than `## Check` sections) and **43a-43f** likewise sub-letters of Check 43, not sections of their own. `grep -n '^## Check' docs/gcp-live-checklist.md` answers this directly: the first heading is `## Check 19`, at line 419.
2. **The summary table's rows go to 47 and 57.** 57's row is on `main` and its section is in `docs/design/compute-backend.md` §7.2.
3. **The register closes at 57** (`:250-257`): 48-56 are the project-centric plan's and 57 is the compute backend's.
4. **58 to 66 are reserved past it**: 58 by the merged artifact-runs design, 59 (as 59a, 59b) by the desktop-console plan, **60 by `docs/design/budget-token-channel.md`** (the project-centric plan's Task 3b.1, PR #399) and **61 by the project-centric plan's phase 6** (the folder export's fence, Task 6.5, PR #405) — the last two read on `origin/next` at `e591c78` — and **62-63 and 64-66 by the desktop-console v2 design (PR #426) and the voice design (PR #428) respectively**, whose documents are on neither `main` at this baseline nor `next`, so those two claims are read from the pull requests rather than from a file this tree holds.
5. **Which leaves 67 free, and this plan takes it.**

**A reserved number is never reused or renumbered, and a later reconciliation takes the next free block.** That is the register's own ruling (`docs/gcp-live-checklist.md:182-185`, `:224-225`), and it is what this plan follows: it does **not** repeat the "whichever of two contending PRs merges second renumbers itself" rule that the artifact-runs plan used for Check 58 (`docs/plans/2026-10-15-artifact-runs.md:990`) — an earlier revision of this section did, and it is superseded, because a rule that makes a number move at merge time makes every document that cites it wrong for as long as the race lasts. Under the ruling, the loser of such a race takes the next free block and changes nothing it already wrote.

**This plan takes one number, not a range**, because its live steps belong to one feature and one phase; **it is written as two sections, 67a and 67b**, which is the 43a-43f sub-lettering convention rather than a second reservation. The split is not decoration: **67a needs a managed project to exist and 67b needs one to have been destroyed**, so they cannot run at the same moment, and 67a's assertion is the safety one (that a non-managed project still refuses and that the marker cannot be forged) while 67b's is the durability one. **Following the project-centric plan's convention, neither this PR nor any docs-only task here writes the row or the section**: Task 5.3 writes both when phase 5 ships, so exactly one writer owns the number and a rebase cannot produce two.

**The re-check this section invites is a `git log` away**, and the commands are: `git show origin/main:docs/gcp-live-checklist.md | grep -n '^## Check 4[5-9]'` for step 1, `grep -n '^| 4[0-9] \|^| 5[0-9] ' docs/gcp-live-checklist.md` for step 2, `sed -n '250,257p' docs/gcp-live-checklist.md` for step 3, a grep of `docs/plans/` and `docs/design/` for the reservations this tree does carry (58, 59) and for the two open D-series PRs for the rest, and this plan's own heading for step 5. **Rows 60 and 61 cannot be checked from `main` at all**, and the commands for them are the ones used here: `git fetch origin next` then `git show FETCH_HEAD:docs/design/budget-token-channel.md | grep -n 'Check 60'` and `git show FETCH_HEAD:docs/plans/2026-10-14-project-centric.md | grep -n 'Check 61'`.

## Global Constraints

These hold for every task below, and the plan's own review checks them:

1. **The coding-agent refusal is removed for a managed project and for nothing else — and the marker, the gate and the delete rule are what replace it.** D55's answer of 2026-10-16 is this plan's shape: in a **managed** project `initflow.CanConfirm` stops returning `false` for a marker on the apply classes the gate covered, so the agent's session applies; in **every other** project the refusal stays exactly as it is today (`internal/initflow/stage.go:334-342`, `AgentRefusal` at `:298-304`), and no task here adds a path around it. Three things replace the refusal and each is a task rather than a sentence: **the gate** — no apply without a `tf.Guard`/managed-`Cover` verdict, the caller's only permitted action on a refusal being to fall back to a confirmation (Tasks 1.0, 1.1, 1.2); **the delete rule** — a managed apply takes no `--allow-delete` of its own: the addresses come from an **owner-written** list an agent cannot write, so an agent can never authorize its own delete or replace — an object at the root of the managed project's runs bucket, written only by an owner-run command, read with the marker's own project-number check, and absent, empty or unreadable meaning every delete and replace is refused (**Task 1.4** builds and defines it; Task 3.1 reads it and refuses the flag; D74); and **the marker** — the read that decides whether managed mode applies at all, writable by no credential a run holds (Tasks 0.1, 0.2). **The residual risk is part of this constraint, not a footnote**: the agent shares the container and the user with the runner and reads the runner's environment through `/proc` and the job's service account through the metadata server (`SECURITY.md:47-53`), so none of the three is a boundary against an agent that goes around the program — the design's §6 item 4 says what holds and what does not, and Task 3.1's PR description carries that paragraph because Task 3.1 is the change it is about.
2. **The cloud marker is the authority and every config may only narrow.** No task lets a repository's `fugaro.yaml`, the local project config, or any agent-written file turn managed mode **on**. Reading one may turn it **off**.
3. **A marker that cannot be read means "not managed".** Every read fails closed, in the shape `infra.ReadProjectMarker` already has (`internal/infra/project.go:51-84`: the bucket must be the expected project's, proved by its own project number, before its contents are believed).
4. **No credential a run holds, and no credential the apply identity holds, may be able to write the marker — or the delete list a managed apply reads (Task 1.4, D74)** — or, in the full version of this feature, to widen its own cloud permissions. A task that changes what a run's identity can do carries a test asserting what it can no longer do. **The limit is the IAM, not the marker**: `SECURITY.md:36` records that Project Owners, Editors and anyone with a project-level storage role bypass the bucket's own policy regardless, so the apply identity's roles are part of the task, not an assumption (Task 0.1).
5. **A plan file is never committed, and never stored outside the local workdir in unredacted form.** The saved `fugaro.tfplan` is a data structure that carries attribute values; what may enter a repository or a run record is a redacted JSON summary. `.gitignore` gains a rule for `*.tfplan` as its own task.
6. **The model review is advisory, recorded, and never the gate.** No task makes a verdict of `ship` a precondition of an apply, and none makes `changes` the only thing that stops one.
7. **`belong` is never touched** by any step, agent-run or owner-run. No task names it as a target, no check reads it, no fixture is recorded from it.
8. **Never handle a secret.** No task reads, writes, prints or transmits a secret *value*; names and metadata only, as `fugaro secrets ls` already does (`internal/cli/secrets.go:533-571`).
9. **Nothing under `.github/workflows/` is created or edited by an agent.** No task in this plan needs one; if a later task does, its workflow text is written as a staged file under `docs/pending-workflows/` for the owner to copy (`docs/pending-workflows/README.md`), which is the mechanism this repo already uses.
10. **Security-sensitive tasks are flagged `SECURITY-SENSITIVE — independent opus review`** in their own heading and are not merged on a self-review. **This list is the headings, and nothing else is on it**: 0.1, 0.2, 0.3, 1.1, **1.4**, 2.1, 2.2, **3.1, 3.2, 3.3**, 4.1. **A separate flag, `OWNER-RUN`, marks the tasks no agent performs at all** — Task 5.3 (Check 67) — and the two flags are independent: a task may carry neither, either, or both.
11. **Docs-only tasks keep the docs suite green**: every task that touches a `.md` file under `docs/` runs `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`, the command this repository's docs tests are reached by.
12. **Every task's verification runs without a live cloud.** No task's command needs a real GCP project, a billing account or a credential; the live steps are Check 67 and nothing else. Task 0.3's property test and phase 1's fixture corpus are what make the gate testable at all.

## Review Focus

What a reviewer of each phase should attack, in the design's own terms:

- **Phase 0**: **can the marker be made true by anything the agent can do?** Two questions, and both must be answered from the code rather than from the prose: what identity writes it, and what reads it. A read that trusts a string, or that treats "cannot read" as "managed", is the defect that turns this whole feature into a privilege escalation.
- **Phase 1**: **is the gate the gate?** `Cover.NotCovered`'s managed-project allowlist is the widest entry in the design — a plan the agent wrote, judged by a list someone chose. A reviewer should ask which type on the list is there because the embedded modules use it and which is there because a managed project might legitimately need it, and treat an unexplained second category as a finding. Then: does every refusal actually stop the apply, or merely return an error someone ignores? **And, for Task 1.4: is the delete allow-list's provenance anything more than a sentence?** The two failures to look for are a `Guard` call fed from the apply's own flag in a managed project, and a read that treats an absent or unreadable list as "nothing to check" instead of "refuse every delete".
- **Phase 2**: **can a job-written string become a decision, and can a plan secret become a repository file?** Look for a verdict interpolated into a refusal list as though it were a rule, and for any path that writes a plan rather than a summary.
- **Phase 3**: **is the removal narrow?** D55's answer removes the refusal **for a managed project only**, so the first attack is the other direction: can a **non-managed** project — or a marker that is absent, unreadable or names another project — reach an apply while a marker is set? Then: can an agent authorize its own delete or replace, by passing `--allow-delete` from its own command line rather than from the owner-written list — which Task 1.4's object defines, and which a managed apply must **refuse** the flag for rather than silently ignore? And does every refusal fall back to a confirmation rather than applying? **The phase has failed if a marker plus a non-managed project reaches an apply, or if the delete allow-list is anything an agent can write** — the second is asserted in Task 1.4 and read in Task 3.1.
- **Phase 4**: **does the inventory check fail in the safe direction?** A false positive must fall back to confirmations, never halt; a false negative must not be reachable by a resource the installation's state does not know about. And: does the budget amount come from the operator, or from a default nobody chose?
- **Phase 5**: **what does restore actually promise, and is it true?** The state bucket lives in the project that was lost; a restore that reads it is a restore that will fail exactly when it is needed. Check whether the plan's promise is "from-zero apply" and whether anything in it quietly assumes the old state.
- **Every phase**: the refusal stays **for every project that is not managed** — D55's answer narrows it and does not delete it — the marker fails closed, no plan file is committed, `belong` is untouched, and no task's command needs a live cloud.

## File Structure

Created by this plan (nothing here exists yet, and **none of it is created by this PR**):

```
internal/infra/managed/
  marker.go             # Task 0.1: the managed marker object, its shape, and its write
  marker_test.go        # Task 0.1: the round trip, the shape, and the one-writer refusal
  deletelist.go         # Task 1.4: the owner-written delete list - the object's shape and its one writer
  deletelist_test.go    # Task 1.4: the round trip, the fail-closed read, and the job account's lack of a write path
  read.go               # Task 0.2: the read, project-number-checked, failing closed
  read_test.go          # Task 0.2: the read against every mismatch
internal/cli/
  managed_test.go       # Task 0.3: the property test - a non-managed project never skips a confirmation
  allow_delete.go       # Task 1.4: `fugaro allow-delete`, the owner-run writer of the delete list (D74)
  allow_delete_test.go  # Task 1.4: the agent-marker refusal, the whole-list write and the print
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
internal/initflow/stage.go                    # Task 3.1: the managed-mode case in CanConfirm (the refusal removed for a managed project only); Task 0.3's tests read it, and an unexplained change here is still a review finding
internal/cli/init_project.go                  # Task 0.1: pass the created flag (r.res.Created) to the installation Terraform; Task 4.2: require the budget
internal/infra/projectcreate.go               # Task 0.1 only, if a fact has to be read back at creation
deploy/terraform/gcp/modules/installation/bucket.tf  # Task 0.1: the marker object, beside project_marker (:78), counted on the created flag; Task 1.4 asserts the root-prefix property the delete list inherits and adds no object
internal/cli/init.go                          # Task 4.2: the budget becomes required, not optional
internal/cli/root.go                          # Task 3.2: the exit codes the managed refusals return; Task 1.4: registers `fugaro allow-delete`, the owner-run delete-list writer
internal/cli/secrets.go                       # Task 5.1: the required-and-missing column on `secrets ls`
internal/infra/statebucket.go                 # Task 5.2 reads it, and changes nothing
.gitignore                                     # Task 2.2: `*.tfplan`
docs/gcp-live-checklist.md                    # Task 5.3 only: Check 67's rows and sections (67a, 67b); 67a exercises the delete list
docs/design/managed-projects.md               # Tasks 2.3, 4.3, 5.4: the retention, alert-reach and decision rows
docs/plans/2026-10-16-managed-projects.md     # Task 5.4 only, if an answer changes a task's shape
```

## PR groups

Each row is one mergeable PR, green on its own:

| PR | Tasks | Flag |
|---|---|---|
| A | 0.1, 0.2, 0.3 | **SECURITY-SENSITIVE** (the marker is the privilege boundary) |
| B | 1.0, 1.1, 1.2, 1.3, 1.4 | **SECURITY-SENSITIVE** (1.1: the allowlist is the widest entry in the design; 1.4: the delete allow-list's provenance is what D72 rests on) |
| C | 2.0, 2.1, 2.2, 2.3 | **SECURITY-SENSITIVE** (2.1, 2.2: the review as a channel, the redaction) |
| D | 3.0, 3.1, 3.2 | **SECURITY-SENSITIVE** (the apply path, D55's answer: the refusal is removed for a managed project only), and **it merges after PR B**: Task 3.1's apply has no delete allow-list without Task 1.4's object, writer and read. **The apply ships unreachable**: refused by config until PR F lands, because the spec's own safeguards — the inventory fallback (4.1) and the required budget (4.2) — merge after it |
| E | 3.3 | **SECURITY-SENSITIVE**, and **unscheduled unless the owner reverses D55** — it is the alternative now (keep the refusal; apply where no agent is), written so the owner can read what the answer of 2026-10-16 gave up rather than only its cost |
| F | 4.1, 4.2, 4.3 | 4.2's commit **opens the apply PR D shipped closed**, once the inventory fallback and the required budget are in the build |
| G | 5.1, 5.2 | |
| H | 5.3, 5.4 | **OWNER-RUN** (Check 67), no agent performs any step of 5.3 |

---

## Phase 0 — The fact

**Why this is first.** Everything else in this feature is downstream of one question: **how does the program know, in a run that did not create the project, that Fugaro created the project?** Today it does not. `fugaro init --create-project` creates the GCP project and adds Firebase (`internal/cli/init_project.go:412-444`, `internal/infra/projectcreate.go:374-418`), the fact lives in the run's memory for the length of that run, and nothing persists it (design finding 19). The nearest durable ownership mark is the runs bucket's `fugaro/project.json`, and it is read with a project-number check precisely because a bucket name proves nothing on its own (`internal/infra/project.go:16-84`). Phase 0 makes the creation fact durable, in that shape, and writes the one property test whose failure would be silent.

### Task 0.1: The creation fact, recorded where a run cannot write it — SECURITY-SENSITIVE, independent opus review — D56 AND D57 ANSWERED 2026-10-16 ("cloud marker, owner-run"); D58 STILL OPEN

**Files:** `internal/infra/managed/marker.go`, `internal/infra/managed/marker_test.go`, `deploy/terraform/gcp/modules/installation/bucket.tf`, `internal/cli/init_project.go`, `internal/infra/projectcreate.go`

**Steps.** The fact is "**this run created this project**", and it has to be written where the thing that holds it exists. Two corrections to the shape this task is written in, both read from the code rather than assumed:

1. **The write belongs to the installation's Terraform, not to a Go call after `AddFirebase`.** `init`'s project stage runs first, and the runs bucket does not exist while it runs: the bucket is created by the **installation** module (`deploy/terraform/gcp/modules/installation/bucket.tf`, `google_storage_bucket.runs`), at the `Installation` stage, later in the same `init`. So the marker is a second `google_storage_bucket_object` beside the one that already writes `fugaro/project.json` (`bucket.tf:78`, `google_storage_bucket_object.project_marker`) — `fugaro/managed.json`, holding the same shape `ProjectMarker` has (`Version`, `Name`, `GCPProject`, `internal/infra/project.go:32-36`) plus the creation timestamp — created **only when this run created the project**.
2. **The creation signal is `r.res.Created`, not `AddFirebase` returning.** `AddFirebase` runs on **existing** projects too: `internal/cli/init_project.go`'s `Apply` sets `r.res.Created = s.id` and `created = true` only inside the `if s.create` branch (`:393-425`), and the `else if s.addFirebase` branch (`:426-444`) is a project this run did **not** create, where Firebase was merely missing. `AddFirebase` is then called once, for both (`:435-444`). A marker keyed on "Firebase was added" would mark the owner's pre-existing projects — the `belong`-shaped case the whole design excludes — and **D73 asks the owner which of the two the addendum means**. The task therefore passes the created flag through to the installation Terraform as its own variable (e.g. `fugaro_managed_project`, empty unless `r.res.Created` is set) and the `count` on the object is driven by it.

**The writer's IAM is part of the task, not an assumption about it.** `internal/infra/project.go:20-22` says "no job account can write it, and since 0.7.0 no launcher", and that is the property this task needs — but `SECURITY.md:36` records how narrow it is: "Project Owners, Editors and anyone with a project-level storage role bypass the bucket's own policy regardless, hardened or not". So the task must do one of two things and say which in the code: **(a) limit the apply identity's roles** so that the identity that applies a managed plan holds no project-level storage role (and, in the full version of this feature, no role that can grant itself one) — asserted by a test that reads the IAM or by a documented, owner-run IAM check; **or (b) state plainly that it can write the marker** and record what that means — the marker is then an authority the applying identity can forge for itself, and D57's recommendation (only an owner-run command may write it) is not met. The recommendation is (a); this task does not proceed on (b) without the owner choosing it in D57.

**The delete list Task 1.4 adds is a different case and this task does not write it**: it is written after creation, when the bucket exists, so it is a CLI-written object rather than a Terraform-managed one — and it is deliberately not a Terraform variable, because `fugaro init` rewrites the installation's `terraform.tfvars.json` from its own flags (`internal/cli/init.go:1132-1139`) and a later `init` would therefore plan to drop it (D74).

**A marker object, not a project label, is what this task writes**, and D57's answer of 2026-10-16 (“cloud marker, owner-run”) is what settles it: the authority is the runs-bucket object, written only by this owner-run path, and no credential a run holds may have a write path to it. A project label is `resourcemanager.projects.update`, which any project editor holds, so it cannot be the authority — **the label, if the owner wants one at all, is Task 3.0's and is additional to the object, never a substitute for it**, and whether it is wanted is C3, a question of this plan's own that is still open.

**Tests:** the marker's round trip; the JSON shape against a fixture; the Terraform side asserted from the module's own plan fixture (the object is absent when the created flag is empty, and present exactly once when it is set); and **the write path refuses when the project it is asked to mark is not the project `init` just created** — the fact is written once, from the one place that knows it, and a second call is an error rather than an update.

**Command:** `GOFLAGS=-p=2 go test ./internal/infra/managed ./internal/cli -run 'Managed|Marker|Project' -count=1`

**Depends on:** nothing to build; **D56 and D57 were answered on 2026-10-16** (“cloud marker, owner-run”), which fixes the marker's form and its writer, and **D58 and D73 must still be answered before it starts**, because they decide what “created” means (D73 if the answer is the looser reading).

**Commit:** `managed: the creation fact, recorded where a run cannot write it`

**Red step — `Expected: FAIL`** — the "not the project this run created" test against a marker write that accepts any project name, and the module fixture's "no marker for a project this run did not create" against a `count` that is unconditional.

### Task 0.2: The read, project-number-checked, failing closed — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/infra/managed/read.go`, `internal/infra/managed/read_test.go`

**Steps.** `IsManaged(ctx, project, gcpProject) (bool, reason string, err error)`, built as `ReadProjectMarker` is (`internal/infra/project.go:51-84`) and for the same stated reason: **a name is global, so a mark proves nothing by itself.** The read locates the runs bucket for the installation, asserts the bucket's own project number is the expected one, and only then believes `fugaro/managed.json`. Every failure mode returns `false` with a reason a person can act on and **never an error the caller might treat as "proceed"**: no bucket, a bucket in another project, an absent object, an unreadable object, a `gcp_project` field that is not the one asked about, a version it does not know — all `false`. The reason strings are one line each, in the shape `Status.Detail` uses.

**Tests:** the read against every mismatch above, each asserting `false` and the reason; the happy path against a fake bucket; and **a test that the returned error is never used as a "managed" signal** — the signature makes that a compile-time property, and the test asserts that a nil error and a false bool are both required.

**Command:** `GOFLAGS=-p=2 go test ./internal/infra/managed -run 'Read|Managed' -count=1`

**Depends on:** Task 0.1.

**Commit:** `managed: the marker read, project-number-checked and failing closed`

**Red step — `Expected: FAIL`** — the "a `gcp_project` field naming another project" test against a read that compares only the project name.

### Task 0.3: The property test — a non-managed project never skips a confirmation — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/cli/managed_test.go`

**Steps.** The one test this feature's safety rests on, written as a property rather than a case: **for every combination of the conditions a run can be in** — a marker set or not, `--yes`, `--non-interactive`, `--json`, a terminal or not, a managed marker present, absent, unreadable or naming another project — **an applying step in a project that is not managed never proceeds without a confirmation, and an applying step in a coding agent's session in a project that is not managed never proceeds at all.** The middle clause is the one D55's answer of 2026-10-16 makes load-bearing: what the property establishes is that a **non-managed** project is untouched, and Task 3.1 extends it with the managed case (a managed project may apply, but only what the gate admitted). The test is built over `initflow.Conditions` and `initflow.ConfirmClass` (`internal/initflow/stage.go:312-351`) and over the managed read, enumerating the product of the two rather than sampling it. It is a test of the program's *decision*, not of a UI, so it needs no cloud and no terminal: it asserts `CanConfirm`/`CanConfirmRun` and the managed-mode predicate, and it fails the day a new condition is added without a case for it.

**Tests:** the property itself, plus a completeness assertion — a new field on `Conditions` or a new `ConfirmClass` fails a test named for the enumeration, so the property cannot be silently narrowed by an added condition.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Managed|Confirm' -count=1`

**Depends on:** Task 0.2.

**Commit:** `managed: the property test that a non-managed project never skips a confirmation`

**Red step — `Expected: FAIL`** — the property against a predicate that returns "managed" for an unreadable marker.

---

## Phase 1 — The gate

**Why this is second.** The spec asks for a plan gate (its steps 2 to 4) and treats the model's review as it. The reconciliation's finding is that the gate already exists — `tf.Guard` refuses every delete and replace unless the exact address is allow-listed (`internal/infra/tf/guard.go:16-44`) and `tf.Cover` decides what may share a confirmation at all (`internal/infra/tf/cover.go:15-47`, `:63-112`, `:169-182`, `:198-266`, `:609-728`) — and that it was built for Fugaro's own embedded modules, whose resource-type allowlist is tested against them. Reusing it for an agent-authored plan means **a new allowlist, and choosing it is a decision, not a copy** (design finding 22). Phase 1 builds the gate and the choice — the delete/replace rule inherited unchanged, and the one thing it has never had written down: **where `Guard`'s allow-list comes from** once the applying command's own `--allow-delete` is no longer allowed to be it (Task 1.4, D74).

### Task 1.0: The plan's path through the gate, and what a refusal does

**Files:** `internal/managed/plan.go`, `internal/managed/plan_test.go`

**Steps.** One function, `Gate(ctx, planPath, spec) (Verdict, error)`, that runs `Guard` first and `Cover`-managed second, in that order, and returns a verdict carrying **every** refusal with the address and the rule that refused it — the shape `Guard` already uses (it lists every refused address rather than the first). `Verdict.OK` is false if either gate refuses, and **the caller's only permitted action on a false verdict is to fall back to a confirmation, never to apply**: the API offers no "apply anyway" and a test asserts the refusal path returns before any apply call. The `spec` is the managed project, its GCP project, the region, the buckets, the registry and the billing account the review showed — the same inputs `Cover` takes (`cover.go:127-154`).

**Tests:** a refusal from `Guard` alone; a refusal from the managed `Cover` alone; both refusing; a plan both allow — asserting only that the function returns `OK` and that no apply was invoked, since applying is Task 3.1's.

**Command:** `GOFLAGS=-p=2 go test ./internal/managed -run 'Gate|Plan' -count=1`

**Depends on:** Task 0.3.

**Commit:** `managed: the plan's path through the guard and the cover, and what a refusal does`

**Red step — `Expected: FAIL`** — the "refused gate still returns OK" case — `Guard` refusing and `Gate` reporting it — against a `Gate` that returns only the last gate's verdict.

### Task 1.1: The managed allowlist — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/infra/tf/managed_cover.go`, `internal/infra/tf/managed_cover_test.go`

**Steps.** The decision this phase exists for: `ManagedAllowedTypes()` — the resource types a plan in a **managed** project may contain — derived from `allowedTypes` (`cover.go:15-47`) but **not identical to it**, because the embedded modules' list answers "what does Fugaro's own installation need" and this one answers "what may an agent-authored plan contain in a project Fugaro owns". The task writes the list with **one line of justification per entry**, in two groups: **inherited** (the entry is on `allowedTypes` and its reason still holds) and **added** (the entry is here because a managed project legitimately needs it, and the justification names the case). `google_billing_budget` and `google_firebase_project` stay, inherited. **Anything the task cannot justify is left out**, and leaving it out is the correct default: a refused plan falls back to a confirmation, which is a working program, while a permitted plan that should not have been is the failure this feature exists to avoid. `grantRules` and `RolePermissions` (`cover.go:63-112`, `:169-182`) are inherited unchanged, and the task's tests assert that: a `*_iam_member` granting a role outside the list, or a custom role whose permission set differs from its pin, is refused exactly as it is today.

**Tests:** one fixture per **added** entry (allowed) and one per entry deliberately **excluded** (refused), with the fixture named for the entry so a reviewer can see the list and its tests side by side; plus a golden test asserting the list is exactly what the source says, so a widening is a visible diff.

**Command:** `GOFLAGS=-p=2 go test ./internal/infra/tf -run 'Managed|Cover' -count=1`

**Depends on:** Task 1.0.

**Commit:** `tf: the managed project's resource allowlist, one justification per entry`

**Red step — `Expected: FAIL`** — the fixture for an excluded entry against a list that inherits `allowedTypes` wholesale.

### Task 1.2: The attributes a managed plan must still satisfy

**Files:** `internal/infra/tf/managed_cover.go`, `internal/infra/tf/managed_cover_test.go`

**Steps.** `Cover`'s attribute checks (`attributes`, `cover.go:609-728`) already cover the images' registry, schedulers calling this run's jobs, the sink's project, a private bucket and the billing account. This task re-derives them for a managed project the same way Task 1.1 re-derives the type list — **each check re-justified for an agent-authored plan rather than inherited by default, with the ones that only make sense for Fugaro's own embedded modules dropped and said to be dropped** — and it keeps the two that carry the spec's step 3: **the project every resource is in** (`Cover.Projects`, kept as the *wider* rule it is rather than narrowed to "must be in the managed project", since the wider one catches more, including a resource in a second project Fugaro owns), and **the billing account the review showed**, which is what makes a plan that moves billing refuse. **No check outside this set is added.** The design's D61 fixes the checkable set as an enumeration and closes it: a rule that is not on that list is not a check this design has, and adding one is a new decision, not an implementation detail. (An earlier revision of this task smuggled in a `lifecycle.prevent_destroy` check and attributed it to the spec. The spec asks for no such check, the design's D61 does not list one, and it is not buildable as written — `prevent_destroy` is a lifecycle meta-argument that does not appear in a saved plan's JSON, and Terraform refuses such a change at plan time. It is dropped, and the design's D61 row now says the set is closed.)

**Tests:** each retained attribute check with a violating fixture and a passing one; a check **deliberately dropped** with a fixture that now passes, so the dropping is visible in a diff rather than silent; and the project-scope check with a resource in a **second** project, which must refuse — this is the spec's step 3's own case and the task asserts the refusal is by address, not by count.

**Command:** `GOFLAGS=-p=2 go test ./internal/infra/tf -run 'Managed|Cover|Attribute' -count=1`

**Depends on:** Task 1.1.

**Commit:** `tf: the managed plan's attribute checks, re-derived and closed`

**Red step — `Expected: FAIL`** — the "a resource in a second project must refuse" fixture against an attribute set that checks only the managed project's name.

### Task 1.3: The cost attributes, flagged and blocked

**Files:** `internal/infra/tf/managed_cost.go`, `internal/infra/tf/managed_cost_test.go`

**Steps.** The spec's step 4 asks the agent to flag "new or upsized resources that bill continuously, such as Cloud Run min instances, Cloud SQL tiers, GPUs and large VMs". This task builds it as a **list, not an estimate** — because Fugaro cannot price an arbitrary plan and must not pretend to (design D62) — and it takes the spec's four examples as the list, one entry each, with the threshold or class stated as a constant in one place: a Cloud Run service or job whose **minimum instance count** is above the stated number, a Cloud SQL tier above the named class, any **GPU attachment**, and a machine type above the named class. **An entry is a property a plan can be above or below, never a resource type.** "A continuously-billing resource of any type the allowlist admits" is deliberately not an entry: nearly every resource a managed project creates bills continuously by existing — a Cloud Run service with min instances 0 included — so that criterion flags essentially every plan, and a gate that flags everything is one that gets switched off (which is the false-positive test below, stated as a rule rather than as a hope). An entry has a **flag** behaviour (recorded in the verdict and shown to whoever confirms) and the ones the owner names have a **block** behaviour (the plan is refused and falls back to a confirmation).

**Tests:** one fixture per entry, each asserting flag-or-block as the list says; a plan that is over the threshold blocked and recorded; and a **below-the-threshold** plan — an ordinary plan creating a Cloud Run job with min instances 0 and a small VM — passing with **no** entry fired, since a cost gate that refuses ordinary work is a cost gate that gets switched off.

**Command:** `GOFLAGS=-p=2 go test ./internal/infra/tf -run 'Managed|Cost' -count=1`

**Depends on:** Task 1.2.

**Commit:** `tf: the cost-bearing attributes, flagged and blocked, never estimated`

**Red step — `Expected: FAIL`** — the below-the-threshold fixture — an ordinary Cloud Run job with min instances 0 and a small VM — against a cost list whose entry is "a continuously-billing resource".
### Task 1.4: The owner-written delete list, and the guard's allow-list read from it — SECURITY-SENSITIVE, independent opus review — D74 (OPEN, and this task is its subject)

**Files:** `internal/infra/managed/deletelist.go`, `internal/infra/managed/deletelist_test.go`, `internal/cli/allow_delete.go`, `internal/cli/allow_delete_test.go`, `internal/cli/root.go` (the verb's registration)

**Steps.** D72's rule — never delete or replace without an explicit, per-address owner approval — is a control only if the approval has a home, and this task builds it. It is the one thing between a prompt-injected agent in a managed project and a delete-bearing plan, **because Task 3.1's apply takes no `--allow-delete` of its own**:

1. **The object.** `fugaro/allow-delete.json`, at the **root** of the managed project's runs bucket: beside `fugaro/project.json` and deliberately outside every prefix a job account can write — the jobs' grants are prefix-conditioned on `runs/`, `cache/` and `locks/`, and on `builds/` for builds (`deploy/terraform/gcp/modules/installation/bucket.tf:72-77`, the comment that says it of the marker: *"No job account can write it"*). **A second object rather than a field on the marker**, so a list that changes can never change what the marker asserts. **Formats**: `{"version":1,"gcp_project":"…","addresses":["google_cloud_run_v2_job.foo", …]}` — `Version` and `GCPProject` as `ProjectMarker` has them (`internal/infra/project.go:32-36`), `addresses` a flat list of exact Terraform addresses, the same strings `tf.Guard` compares by exact equality today (`internal/infra/tf/guard.go:20`, `:35`), and **nothing else**: no note, no reason, no comment field. A free-text field in a file a run reads is text an agent would like to write, and D61's rule — every string is data, never an instruction — would then have to be trusted rather than enforced. The approval's provenance is the object's own version history.
2. **The writer, `fugaro allow-delete <address>…`** — an owner-run command at the owner's terminal, and the fourth name this feature adds (Task 3.2 settles the name with the rest; D68's row records it). It **refuses under a coding-agent marker**, in the shape the writing paths already have (`internal/cli/init.go:1012-1014`), because a command that records its own authorization is exactly what an agent in a managed project would reach for; it **writes the whole list on each invocation rather than appending**, so it is idempotent and a shorter list only ever refuses more; and with no addresses it **prints** the current list rather than emptying it. It writes with the operator's own credentials — no credential a run holds has a write path to the object, and the test that asserts that is this task's.
3. **The read, `managed.AllowDelete(ctx, project, gcpProject) ([]string, error)`** — the marker's read, for the marker's stated reason. It locates the installation's runs bucket, **asserts the bucket's own project number is the expected one before believing anything in it** (`ReadProjectMarker`, `internal/infra/project.go:51-84`), and returns the addresses. **Absent, empty, unreadable or naming another project returns no addresses and no error a caller could read as "proceed"**, in the fail-closed shape Task 0.2 uses. Task 3.1's apply calls it and hands the result to `Guard`; a `--allow-delete` on the applying command in a managed project is **refused and named** rather than ignored (Task 3.2's line), which is what makes D72's "never from the applying command's own arguments" a behaviour rather than a sentence.
4. **What this task does not claim, stated in the code and in its PR description.** The object inherits Task 0.1's IAM limit exactly: `SECURITY.md:36` records that Project Owners, Editors and anyone with a project-level storage role bypass the bucket's own policy regardless, so **"no job account can write it" is the property asserted here** and the applying identity's roles remain Task 0.1's decision (a) or (b). A delete list whose writer can forge it is not a delete list, and the PR description says which of the two this task is built on.

**Tests:** the round trip — written by the owner-run path, read back through the project-number check; **a test asserting the run's job account holds no write path to the object**, in the shape Task 0.1's marker test uses (Global Constraint 4); **absent, empty and unreadable each refuse every delete and replace**, with a `Guard` fixture plan that carries a delete, so the assertion is about the gate rather than about the reader's return value; **an address passed on the applying command line is refused, not honoured**; the writer refused under a coding-agent marker; and the shape tests — a `gcp_project` naming another project is refused, and an unknown `version` is refused rather than read optimistically.

**Command:** `GOFLAGS=-p=2 go test ./internal/infra/managed ./internal/cli -run 'Managed|Delete|Allow' -count=1`

**Depends on:** Task 0.1 (the object's writer property and the IAM assertion it shares) and Task 0.2 (the read discipline it copies); and **D74, which is this task's own question** — if the owner's answer is the local config file instead, this task is rewritten rather than adjusted. **Its place in this phase is because it is the guard's input**: `Guard` is inherited unchanged, and what changes is where its allow-list comes from.

**Commit:** `managed: the owner-written delete list, and the guard's allow-list read from it`

**Red step — `Expected: FAIL`** — the "an address the applying command passes is refused" test against an apply that feeds its own flag into `Guard` in a managed project, and the "an absent list refuses the delete" test against a read that returns an empty list as "nothing to check".


---

## Phase 2 — The review, advisory

**Why this is third.** The spec's step 5 makes the agent the plan reviewer and its step 7 keeps the plan and the review for audit. The reconciliation refuses the first as a control (design findings 8 and 9: the reviewer is the coder's own model in the coder's own container, reading text the reviewed party writes) and keeps the second, with one correction: **a plan file is not a summary** and must not be committed (finding 28). Phase 2 makes the review advisory and recorded, and puts the redaction rule in place before anything is written anywhere.

### Task 2.0: What the review is and is not

**Files:** `internal/managed/review.go`, `internal/managed/review_test.go`

**Steps.** The review is produced by the existing `review` stage — `r.stage(ctx, "review", agent.Request{Prompt: reviewPrompt, SessionID: agent.NewSessionID(), JSONSchema: VerdictSchema}, stageOpts{})`, `internal/runner/runner.go:1559`, its prompt `internal/runner/prompts.go:72`, its schema `internal/runner/verdict.go:13` — the quote is the whole call, `SessionID` and `stageOpts{}` included — given the **redacted plan summary** (Task 2.2) rather than the plan file, and its output is stored as an artifact the run record points at. This task changes nothing about the stage's own behaviour (it runs when the recipe says so, and its verdict still gates pull-request readiness exactly as before) and adds the managed-mode rule: **the review's verdict may never be the reason an apply happens.** `managed.MayApply(gateVerdict, reviewVerdict) bool` returns `gateVerdict.OK && reviewVerdict != "changes"` — the review can refuse, never permit — and it is the only function in the package that decides, so a caller cannot compose its own rule.

**Tests:** the truth table of `MayApply` exhaustively, with the case that matters named: **gate OK and review `ship` applies, gate refused and review `ship` does not, gate OK and review `changes` does not.** Plus a test that the prompt handed to the stage is the summary and never a path to the plan file.

**Command:** `GOFLAGS=-p=2 go test ./internal/managed -run 'Review|MayApply' -count=1`

**Depends on:** Task 1.3.

**Commit:** `managed: the review is advisory, recorded, and can only refuse`

**Red step — `Expected: FAIL`** — the truth-table case "gate refused and review `ship` does not apply" against a `MayApply` that ORs the two verdicts.

### Task 2.1: The review artifact — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/managed/review_store.go`, `internal/managed/review_store_test.go`

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

**Steps.** The audit trail's lifetime is a decision the design leaves to the owner (D65). This task writes the answer **where it is, in the design's D65 row**, once the owner gives it, and — if the answer is "longer than 90 days" — says in the same row that the retention change belongs to the bucket module and to a task that does not yet exist. The task's substance is the *statement*, not a code change: a record that expires is not an audit record, and the plan's job here is to make that impossibility visible rather than let a future reader assume the runs bucket keeps things forever.

**Tests:** the docs suite (Global Constraint 11).

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** Task 2.2.

**Commit:** `docs: managed projects - what the audit record's lifetime is, and what it would cost to change`

---

## Phase 3 — The apply

**Why this is fourth, and why it is a phase of its own.** This is **D55**, the central decision, and it was answered on **2026-10-16**: **the refusal of live applies from a coding-agent session is removed for a managed project**, so the agent's session applies a plan that passed the gate. Task 3.1 builds it, and what replaces the refusal — the gate, the marker, and the owner-written delete list (Task 1.4's object, which this apply reads and whose own command-line flags it refuses) — is written into that task rather than assumed here; 3.2 is the refusals and the exit codes. **Task 3.3 is the shape the design used to recommend** (keep the refusal; the agent proposes and the apply runs where no agent is), kept as a task so the owner can read what the answer gave up rather than only its cost.

### Task 3.0: The marker's cloud form, if the owner wants a label — D56 AND D57 ANSWERED 2026-10-16 ("cloud marker, owner-run"); the label itself is C3

**Files:** `internal/infra/managed/marker.go`, `internal/cli/init_project.go`, `internal/infra/managed/marker_test.go`

**Steps.** **D56 and D57 were answered on 2026-10-16 (“cloud marker, owner-run”), so the authority and its writer are settled and this task is no longer waiting on them: what it waits on is C3 — whether the owner wants a second, independent mark at all** — and on Task 3.1, whose marker read it agrees with. The spec asks for the mark in two places: "a GCP project label (for example `fugaro-managed=true`) and the project config in the project Git repo. Fugaro treats a project as managed only when both agree." The reconciliation rejects the second half as written — the repository config is content the agent writes (finding 17) — and this task builds the first half, **if** the owner wants a label at all: `fugaro_managed=true` on the project, written by the same operator-run path that writes the marker object (Task 0.1), read as an **additional** condition that must agree with the object and never as a substitute for it. The task's tests assert the direction of the disagreement: **the object says managed and the label says nothing → refuse; the label says managed and the object says nothing → refuse**, because either alone is forgeable by a different identity than the one the other requires.

**Tests:** the agreement table; the label write from the operator path; and a test asserting the run's job account holds no permission that can set the label (Global Constraint 4).

**Command:** `GOFLAGS=-p=2 go test ./internal/infra/managed ./internal/cli -run 'Managed|Label' -count=1`

**Depends on:** Task 2.3, **Task 0.1's object** (the label is additional to it, never a substitute) and **C3** — whether a label is wanted at all; D56 and D57 are already answered, and their answer is why the label is optional rather than the authority.

**Commit:** `managed: the project label, if the owner wants one, agreeing with the marker object`

**Red step — `Expected: FAIL`** — the disagreement table — the object says managed and the label says nothing, and it must refuse — against a read that treats either mark as sufficient.

### Task 3.1: The apply of a saved plan, with the coding-agent refusal removed for a managed project — SECURITY-SENSITIVE, independent opus review — D55 ANSWERED 2026-10-16 ("remove it for managed"), NOT APPROVED

**Files:** `internal/initflow/stage.go`, `internal/managed/apply.go`, `internal/managed/apply_test.go`

**Steps.** D55's answer of 2026-10-16, built. The removal has to be **one predicate wide, not one file wide**: in a managed project the apply happens **in the agent's session** — the agent proposes (it writes the configuration and opens the pull request) and, when the plan has passed the deterministic gate, the same session applies it. The apply keeps the sequence the repo already has for its own installation: plan into a saved file (`internal/infra/tf/tf.go:150`, `--out=`), `Show` it (`:160`), gate it (Task 1.0), and `Apply` the **saved file** (`:263-278`, which already refuses to apply without one).

**The removal is one predicate, and the predicate is this task's review subject.** `initflow.CanConfirm` (`internal/initflow/stage.go:334-342`) stops returning `false` for a marker **only when all three hold**: the marker read returned managed, the gate returned a verdict that admits the plan, and the step's class is one the gate actually covered. Every other combination — a marker absent, unreadable or naming another project; a gate that refused; a class the gate does not cover; a project that is not managed — keeps today's `AgentRefusal` (`:298-304`) exactly. The managed case is **a case inside that function, not a second copy of its rule**, and `AgentRefusal` gains the branch that says why the step proceeded, so the sentence a person reads is still produced where the reason is decided. A predicate that removes the refusal for *any* project is the red step below, not a bug to be found later.

**What replaces the refusal, in the code rather than in prose:**

- **the gate** — no apply without a `tf.Guard`/managed-`Cover` verdict, and the caller's only permitted action on a refusal is to fall back to a confirmation, never to apply (Tasks 1.0, 1.1, 1.2);
- **the delete rule** — the plan is gated with the addresses `managed.AllowDelete` read from the owner's object, and **never** with the applying command's own `--allow-delete`, which in a managed project is refused rather than ignored, so an agent cannot authorize its own delete or replace (Task 1.4 builds the object, its writer and its read; D72, and the reason D72 is not empty text once the refusal is gone);
- **the marker** — the fail-closed read that decides whether managed mode applies at all, writable by no credential a run holds (Tasks 0.1, 0.2), asserted by a test.

**What this task does not claim.** The agent shares the container and the user with the runner and reads the runner's environment through `/proc` and the job's service account through the metadata server (`SECURITY.md:47-53`), so **none of the three replacements is a boundary against an agent that goes around the program**: the refusal removed here stops the *accident*, not the adversary. **The design's §6 item 4 states what holds anyway and what is not protected, and this task's PR description carries that paragraph**, because this is the change it is about — the description is not allowed to say "no prompts" without saying what the prompts were doing.

**Three additions that survive the removal:** **the gate runs between the show and the apply, and its refusal is a hard return**; **the apply requires a marker read that returned a reason a person could read**, so an unreadable marker is not an apply; and **the apply asserts that its identity can write the resources phase 1's allowlist admits and nothing wider** — and, from Task 0.1, that it cannot write the marker, and from Task 1.4 that it cannot write the delete list.

**The apply is inert until phase 4 lands.** Task 4.1 (the whole-project inventory and its fallback to confirmations) and Task 4.2 (the budget amount, required at creation) are **later** phases — phase 4 merges after phase 3's PR D — so an apply that shipped with phase 3 would run in a managed project with no inventory fallback and no budget, which are two of the spec's own safeguards. This task therefore ships the apply **unreachable**: it is refused by config, with one named flag-or-config gate that only phase 4's tasks lift, and the refusal says so in as many words ("the managed apply is not enabled in this release: the whole-project inventory (Task 4.1) and the required budget (Task 4.2) are not in it"). A property test asserts the apply cannot be reached with that gate closed — including through every one of `initflow.Conditions` — and Task 4.2 is where the gate is opened, in the same commit that requires the budget. **The alternative — shipping the apply live and asking operators to wait — is not a control**, and this is the ordering the plan is written to.

**Tests:** **the scoping property — over the whole product of `initflow.Conditions`, the managed read and the gate's verdict, the refusal is removed only when the project is managed and the gate admitted the plan, and a non-managed project never loses a prompt**; the gate's refusal stopping the apply (asserting no `terraform apply` was invoked, by the fake's own call log); the unreadable-marker refusal; **the delete list's provenance — an address passed on the command line is refused rather than honoured, and an absent or empty list refuses every delete**; the saved-plan requirement, which is the existing behaviour and gets a test here so a future refactor cannot drop it; and **the inert-until-phase-4 refusal, over the whole `Conditions` product**.

**Command:** `GOFLAGS=-p=2 go test ./internal/initflow ./internal/managed -run 'Apply|Managed|Refusal' -count=1`

**Depends on:** **Task 1.4** (the delete list's object, writer and read — this apply has no allow-list without it), Task 3.0 (or Task 2.3, if the owner declines a label), **D55's answer (given 2026-10-16) and the OWNER APPROVAL GATE** — an answer is not an approval.

**Commit:** `managed: the coding-agent refusal removed for a managed project, with the gate, the marker, and the owner's delete list read (Task 1.4)`

**Red step — `Expected: FAIL`** — the scoping property against a predicate that removes the refusal for any project, and the "no apply after a refused gate" test against a sequence that logs the refusal and applies anyway.

### Task 3.2: The refusals a person reads — SECURITY-SENSITIVE, independent opus review — D55 ANSWERED 2026-10-16; NOT APPROVED

**Files:** `internal/managed/apply.go`, `internal/cli/root.go`, `internal/managed/apply_test.go`

**Steps.** The spec's proposed CLI surface (`fugaro init project --managed`, `fugaro restore`, `fugaro secrets status`) collides with what exists: `init` has `--create-project` and no `project` noun (`internal/cli/init.go:344-386`), and `secrets` has `ls` (`internal/cli/secrets.go:40-62`). This task settles the **names and the exit codes** the way the design recommends (D68): `--managed` as a flag on the existing `init --create-project`, `fugaro restore` at the top level, the missing-secret column folded into `secrets ls` (Task 5.1), **and the owner-run delete-list verb `fugaro allow-delete` that Task 1.4 builds and D74 defines** — a name the spec does not propose and this feature needs, so D68's row records four rather than three. Exit codes stay `0/1/2` (`internal/cli/root.go:15-20`, `:41-50`): **a managed-mode refusal — an out-of-scope plan, a marker mismatch, an unreadable marker; and, for a project that is **not** managed, the coding-agent refusal, which D55's answer leaves exactly where it is — is `1`**, because it is the user's to act on, and a read that fails is `2`. Every refusal is one line naming what refused and what to do, in the shape `initflow`'s refusals use (`stage.go:158-166`, `:275-304`), and **no refusal ever names a way around the marker**, because a recipe for getting past the check has no business in text an agent reads (`IDEHint`'s own reasoning, `stage.go:286-293`).

**Tests:** each refusal's exit code; the one-line shape; **the managed `--allow-delete` refusal, one line naming the owner's list and the command that writes it**; and a test that no refusal string matches the marker names as an instruction — the same spirit as `IDEHint`, asserted rather than intended.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Managed|Refus|Exit' -count=1`

**Depends on:** Task 3.1.

**Commit:** `cli: the managed refusals, their names and their exit codes`

**Red step — `Expected: FAIL`** — the "no refusal names a way around the marker" test against a refusal string that repeats `IDEHint`'s sentence for every marker.

### Task 3.3: The alternative — keep the refusal, and apply where no agent is — SECURITY-SENSITIVE, independent opus review, UNSCHEDULED

**Files:** `internal/managed/apply.go`, `internal/managed/apply_test.go`, `internal/initflow/stage.go`, documented in `docs/design/managed-projects.md` §5

**Steps.** **This is the shape the design recommended and the owner did not choose on 2026-10-16** (D55 was answered "remove it for managed"), written out as a task so what the answer gave up can be read rather than only its cost. **It does not start unless the owner reverses D55.** It keeps the refusal and makes the separation real: the agent proposes — it writes the configuration and opens the pull request — and the apply runs **where no agent is**, on a saved plan that passed the deterministic gate. Two shapes satisfy it, and the task would build **one** of them, named in the code:

- **(a) an owner-run command at a real terminal** — the apply is a step a person runs, so it keeps `initflow`'s typed confirmation and is refused under a coding agent's marker as everything else in `init` is; the spec's "no prompts" is not delivered, and the apply is trivially outside any agent's session.
- **(b) a separate job with its own service account that never shares a container, an environment or a filesystem with an agent** — it delivers the spec's "no prompts" for everything the gate covers, and it is the shape the task's "applies as" question is about: the job's service account, its roles, and a documented refusal to run in the run's own container.

**What it would buy, stated so the comparison is fair.** Under (a) or (b) `CanConfirm` (`internal/initflow/stage.go:334-342`) keeps its marker rule untouched, so the agent's session cannot reach an apply by construction rather than by a predicate; the compensating controls Task 3.1 has to build and test (the delete list's provenance, the marker's unwritability, the gate's verdict recorded before the apply) would still be needed, but they would be defence in depth rather than the only thing between a prompt-injected agent and the operator's cloud.

**"The runner's own apply path" is not a third shape, and this task does not build it.** The agent shares the container and the user with the runner, reads the runner's environment through `/proc` and the job's service account through the metadata server (`SECURITY.md:47-53`), so an in-container apply path's credential is the agent's credential: the agent applies directly and skips the gate. That is the same argument the design uses against the model reviewer (finding 8), and it is why §2 of the design carries it as the misreading to make against itself — and it is also why the owner's answer to D55, once the refusal is gone, leaves the deterministic gate rather than the container as the load-bearing control.

**Cost of the alternative, plainly** (the design's D55 row carries the same figure): it does not deliver the spec's "every cloud operation runs without prompts", and shape (b) costs a second job, a second service account and a second IAM surface per installation.

**Tests:** whatever the chosen shape requires, plus the property test's extension — the property must stay "a non-managed project never skips a confirmation" in either world, and gain "a managed project applies only what the gate admitted".

**Command:** `GOFLAGS=-p=2 go test ./internal/cli ./internal/managed -count=1`

**Depends on:** Task 3.2, and on **D55 being reversed** — the owner answering the other way.

**Commit:** `managed: the apply where no agent is, with the refusal kept (D55, the shape not chosen)`

**Red step — `Expected: FAIL`** — the extension "a managed project applies only what the gate admitted" against a marker-plus-managed predicate that skips the gate.

---

## Phase 4 — Single purpose and the budget

**Why this is fifth.** Two of the spec's promises have nothing built under them: "single purpose" — "if Fugaro finds resources it didn't create, it warns and falls back to confirmations" (design finding 20: discovery runs the other way today, finding Fugaro's resources by label and never foreign ones) — and a budget **amount** that comes from the operator rather than from a default (finding 23: the budget and its 50/90/100% alerts exist; the amount is optional and the alerts email). Phase 4 builds both, and deliberately does **not** build the hard stop (D63).

### Task 4.1: The whole-project inventory, and the fallback — SECURITY-SENSITIVE, independent opus review

**Files:** `internal/managed/inventory.go`, `internal/managed/inventory_test.go`

**Steps.** A read-only inventory of everything in the project, compared against what the installation's own state accounts for, with the spec's own consequence: **a resource the state does not account for falls the run back to confirmations and warns** — it never halts and never deletes. The direction is a Global Constraint in spirit and a test here: **a false positive must not stop the operator's work**, because a check that halts on an unrelated service is a check the operator disables. The inventory is built from the project's own resources by list-and-label, in the shape `internal/infra/discover.go` already uses (`:325`, `:392`, `:506`), and it reports what it found and why it thinks so, one line per resource, so a person can disagree with it. It reads and writes nothing. **This task is one of the two the managed apply waits for**: Task 3.1 ships the apply refused by config, and it becomes reachable only once this fallback and Task 4.2's required budget are in the build — which is the safe order, since the spec's own "single purpose" safeguard *is* this fallback.

**Tests:** an inventory with a foreign resource → confirmation fallback plus a warning naming the resource; every resource accounted for → no fallback; **an inventory that cannot be read → confirmation fallback** (the fail-closed rule, Global Constraint 3, applied to this check too); and a test that the check performs no write, asserted from the fake's call log.

**Command:** `GOFLAGS=-p=2 go test ./internal/managed -run 'Inventory' -count=1`

**Depends on:** Task 3.2.

**Commit:** `managed: the whole-project inventory, and the fallback to confirmations`

**Red step — `Expected: FAIL`** — the "cannot read → fall back" test against a check that treats an unreadable inventory as clean.

### Task 4.2: The budget amount, required and written down — D64 ANSWERED 2026-10-16 (the amount is required, no silent default)

**Files:** `internal/cli/init.go`, `internal/cli/init_project.go`, `internal/cli/init_budget_test.go`

**Steps.** Today the budget is opt-in and its three flags must come together (`--budget`, `--budget-currency`, `--billing-account`, refused unless all three: `internal/cli/init.go:356-358`, `:800`), and the module's thresholds default to `[0.5, 0.9, 1.0]` (`deploy/terraform/gcp/modules/installation/variables.tf:182`). For a **managed** project this task makes the amount **required at creation** — D64, which the owner answered on 2026-10-16 as this design recommended — with a refusal that prints a suggested value and says why it is being asked for; and it writes the amount and the currency into the project's own persisted record (Task 0.1's marker, extended) so a later run and a restore can read what was chosen. Non-managed projects keep today's opt-in behaviour exactly, and a test asserts that, because the change must not reach them. **This task also opens the gate Task 3.1 shipped closed**: the managed apply is refused by config until the inventory fallback (Task 4.1) and this required budget are in the build, and **the same commit that requires the budget lifts the refusal** — with a test asserting the ordering, so an apply cannot become reachable because someone reordered two merges.

**Tests:** a managed creation with no amount refused, with the suggestion in the message; a managed creation with an amount recorded; a **non-managed** creation unchanged; and a test asserting the managed default is absent — there is no constant a caller can fall back on.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Budget|Managed' -count=1`

**Depends on:** Task 4.1.

**Commit:** `init: a managed project's budget amount is required, and no default exists`

**Red step — `Expected: FAIL`** — the "no default" test against a constant default of 50.

### Task 4.3: The budget alerts, stated as they are

**Files:** `docs/design/managed-projects.md`

**Steps.** The spec promises "budget alerts at set fractions of a monthly amount … sent to the project owner", and the repo already has the budget with `[0.5, 0.9, 1.0]` emailing the billing account's admins (`deploy/terraform/gcp/modules/installation/budget.tf:11-36`). This task writes the two differences into the design's D62/D63 rows rather than into code: **what the alerts actually reach is the billing account's admins, not "the project owner"** — an installation's owner is a Fugaro concept and the billing account's admins are a GCP one, and the two need not be the same people; and **the budget's `projects` filter is already project-scoped**, so the managed project's budget covers what the spec wants it to cover. No code changes.

**Tests:** the docs suite (Global Constraint 11).

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** Task 4.2.

**Commit:** `docs: managed projects - who a budget alert actually reaches`

---

## Phase 5 — Secrets and restore

**Why this is sixth.** The spec's Secrets and Disaster recovery sections are the two that depend on decisions about *promises*, and both are cheap once the fact (phase 0) and the gate (phase 1) exist. One correction is load-bearing: **the state bucket lives in the managed project** (`fugaro-tfstate-<gcp-project>`, `internal/cli/init.go:348`, `:1198`; `internal/infra/statebucket.go:77`), so a restore cannot restore state — it can only apply the repository's configuration from zero (design finding 31). Phase 5 builds the two commands and the check that runs them.

### Task 5.1: The missing-secret column on `secrets ls`

**Files:** `internal/cli/secrets.go`, `internal/cli/secrets_test.go`

**Steps.** The spec's `fugaro secrets status` "shows which required secrets are missing after a restore" (design finding 34: nothing reports this today). Built as the design recommends (D67), **one column on the command that exists**: `fugaro secrets ls` already reads metadata only, with no access call, and reports each name's scope and version count (`internal/cli/secrets.go:533-571`). The new column answers "required by something this project knows about and absent", where "required" is the union of what the repository's `fugaro.yaml` declares and `config.ReservedSecrets` (`internal/config/config.go:419-427`). **Nothing about the value is read, at any point** — the command's own doc comment says so today and the task keeps that sentence true with a test that asserts no access call is made.

**Tests:** the column over a fake secret set with one missing; the "required" union from both sources; and a test asserting the command makes no value-access call, which is the existing promise held as a test.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Secrets' -count=1`

**Depends on:** Task 4.3.

**Commit:** `secrets: ls says which required secret is missing, and still never reads a value`

**Red step — `Expected: FAIL`** — the "required by both sources" case — a name declared in `fugaro.yaml` and one in `ReservedSecrets` — against a column that reads only one of them.

### Task 5.2: `fugaro restore`, a from-zero apply

**Files:** `internal/cli/restore.go`, `internal/cli/restore_test.go`

**Steps.** What the command does, in the order that keeps it honest: take the new project ID (the old one stays reserved about 30 days, so a restore always creates a new ID and updates the repository's `gcp_project:` line — the line `init` itself writes into a nine-key `fugaro.yaml` (`schemas/fugaro.schema.json`; `internal/cli/init_fugaroyaml.go:24-28`, `:54-112`), and the only field the restore changes); create the project through the same path `init --create-project` uses; **apply the repository's configuration from zero, reading no state** — the task asserts that no state read happens, because the state bucket is in the project that was lost and a restore that needs it will fail exactly when it is needed (finding 31); re-publish the GCS objects the repository does not hold (the project's recipes, `internal/recipe/recipe.go:35-36`, `internal/cli/recipes.go:327-362`, and the project layer) or refuse, naming them; and end by printing what was **not** restored, as a list rather than as a caveat: in-flight runs and RTDB counters (the spec's own "live state is lost"), the spend history and kill-switch state (`internal/budget/model.go:30-38`, `:57-72`), **the Firestore and RTDB application data** — the from-zero apply re-creates the databases, never their contents — **the Firestore database's location**, which is permanent and can never be changed (`internal/cli/init_firebase.go:525-544`: "a database's location can NEVER be changed"), so a restore into another region is not the same installation, any secret value (finding 35), and the plan files that were never stored (Task 2.1). It is a typed-confirmation command throughout: every step in it is money or permanent, which is `initflow.Typed`'s definition (`stage.go:315-324`), and it is refused in a coding agent's session like everything else.

**Tests:** the sequence over fakes, asserting no state read; the fresh-ID path and the `gcp_project:` update; the not-restored list printed; the typed confirmation required for each permanent step; and the coding-agent refusal.

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Restore' -count=1`

**Depends on:** Task 5.1.

**Commit:** `restore: a from-zero apply into a fresh project ID, and the list of what is not restored`

**Red step — `Expected: FAIL`** — the "no state read" assertion against a restore that reads the state bucket.

### Task 5.3: Check 67, the managed apply and the restore round trip — OWNER-RUN, no agent performs any step

**Files:** `docs/gcp-live-checklist.md`

**Steps.** **This task is the plan's one owner-run step, and no agent performs any part of it.** It writes `## Check 67a` and `## Check 67b` into the checklist, with **Check 43's guard block verbatim** re-run from a fresh shell before every step (three hard stops; `--project fugaro` on every `fugaro` command; `--project "${P:?}"` on every `gcloud` command), and with the summary table's row, since this task is the number's one writer. **67a** is the managed apply: a sandbox managed project created at the owner's terminal, a plan the gate admits applied **with no confirmation**, the same plan with one resource moved to a second project **refused**, a plan with a delete **refused** while the owner's delete list is absent or empty and **applied** once that exact address has been written into the list with the owner-run command, a **non-managed** project still asking for its confirmation, a marker whose `gcp_project` names another project read as **not managed**, and the FACT that the run's own identity **could not write the marker or the delete list** (the negative assertion, and the one that makes the rest meaningful). **67b** is the restore round trip: the sandbox managed project destroyed, `fugaro restore` producing a **new** project ID, the `gcp_project:` line updated, the configuration applying from zero with **no state read**, the missing-secret column naming the third-party keys to re-enter by hand, and the not-restored list printed. The restore step deletes only what the check created, and its final assertion's every line must read `0` — counts, not an empty-looking line — in the shape Check 57's restore already uses.

**Tests:** the future `TestChecklistChecksAreGuarded` (project-centric plan Task 6.3) will assert both sections carry Check 43's three hard stops and its project pinning; this task writes them so that assertion can pass.

**Command:** none — this is an owner-run live check, and it is the only step in this plan that touches a cloud.

**Depends on:** Task 5.2, and on every phase above it.

**Commit:** `checklist: Check 67, the managed apply and the restore round trip (owner-run)`

### Task 5.4: The design follows the owner's decisions

**Files:** `docs/design/managed-projects.md`, `docs/plans/2026-10-16-managed-projects.md`

**Steps.** The desktop-console plan's Task 5.0, applied here. **Part of it is done and part is not.** The design's §5 table already carries the owner's five answers of 2026-10-16 in its last column (D55, D56, D57, D63 and D64), and the documents and tasks those answers change have already been rewritten to them — the design's §1 item 4, D55's row and §6 item 4; this plan's Goal, Status, Global Constraint 1, Tasks 3.1, 3.3, 3.0, 4.2 and 6.0 and the OWNER APPROVAL GATE. **What remains is the other fifteen rows (D58 to D62 and D65 to D74)** and any task whose shape a later answer changes: when the owner answers them, this task writes each into the table's last column, moves any answer that changes a recommendation into the affected section of both documents, and — where an answer reverses a recommendation — rewrites the tasks that depended on it before anything is built from them. The rule the plan inherits: **answering a decision is not approving the tasks**, and this task changes no code.

**Tests:** the docs suite (Global Constraint 11).

**Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|Lint' -count=1`

**Depends on:** Task 5.3, or on the owner's remaining answers arriving earlier, whichever comes first. **The five answers of 2026-10-16 were written in by the docs round that recorded them**, so this task's own first run is about the fifteen rows still empty.

**Commit:** `docs: managed projects - the owner's answers, in the design's own table`

---

## Phase 6 — Later, unscheduled

### Task 6.0: The billing hard stop — D63 ANSWERED 2026-10-16 ("no hard stop in the first managed release"): UNSCHEDULED

**Files:** a design of its own, if the owner wants one.

**Steps.** The spec's optional hard stop — "on 100%, a small function disables billing or scales services down" — is **not built here**, and the plan says why in the design's terms (D63, finding 25): it needs the Cloud Billing Budgets API, a Pub/Sub topic and a function holding billing-account admin, which is a **new always-on credential with money-adjacent power** in a project whose premise is that the run there has broad rights — and the budget's notification path today has no client, no topic and no enforcement in this tree. If the owner wants it, it gets its own design and its own decision, because what it adds is a credential, not a flag. **This row exists so that its absence is a decision rather than an oversight — and it now is one**: the owner answered D63 on 2026-10-16 **not to build it**, so the absence is chosen rather than pending, and it is reversible only by the owner asking for the design this task would produce. Nothing in phase 3 or phase 4's apply depends on it: with no hard stop the spend control is the budget and its alerts (Tasks 4.2, 4.3) plus Fugaro's own cap and kill switch, which is what the answer says.

### Task 6.1: Moving an existing project into managed mode — UNSCHEDULED until D58

**Files:** a design of its own, if the owner wants one.

**Steps.** The spec's own open question, and the answer this plan is written to is **no**: a project is managed because **Fugaro created it and recorded that fact** (phase 0), so an existing project cannot be flagged after the fact, and **both of the owner's installations keep today's confirmations in full** — including `belong`, which is the reason that rule is worth having (design §2, finding 19). If the owner wants a path, it is an owner-run, typed-confirmation-per-project procedure with its own check, and the design's D58 row carries the cost of the alternative: the marker stops meaning "Fugaro made this" and starts meaning "someone said so", and there is no mechanical difference left between the sandbox installation and `belong`. **Unscheduleable without that answer.**

## Cross-task dependencies

Every task's "Depends on" is a *build* dependency, and none of them is a merge dependency beyond its own phase: each phase leaves a working artifact even if the next never ships. **Phase 0 is the one exception, and it is not an exception to the rule but the rule's own statement**: phases 1 to 5 read the marker phase 0 writes, so 0 is a real precondition — and that is deliberate, because the marker is the privilege boundary and there is no safe way to build the gate before the fact it is gated on exists.

**Four dependencies that are real and are *merge* dependencies, listed here because a reader checking the "nothing in phase *n* is required by phase *n-1*'s merge" claim would otherwise read them as exceptions it missed:**

- **Task 0.1 must merge before Task 0.2.** The writer and the reader are one shape; merged the other way, the read has nothing to read and its tests would be written against a fixture that the real writer then contradicts.
- **Task 1.0 must merge before Task 3.1.** The apply path calls the gate; without it there is an apply path with no verdict to check.
- **Task 5.2 must merge before Task 5.3 (Check 67).** The check exercises `fugaro restore`; merged the other way the check cannot run, and — unlike the console's staged workflows — there is no gate or release that depends on it, so the inversion costs nothing but a re-run.
- **Tasks 4.1 and 4.2 must merge before the apply is reachable — not before Task 3.1 merges.** Task 3.1 ships the apply refused by config, so the merge order is D, then F, and a property test asserts the apply cannot be reached while the gate is closed. Reversed — an apply reachable with no inventory fallback and no required budget — the managed project would apply plans with two of the spec's own safeguards missing, which is the one inversion in this list that costs more than a re-run.

**And the fact a reader should not have to infer, narrowed to what is actually established: no task in this plan changes a confirmation path for a project that is not managed, and no non-managed apply loses a prompt.** That is Task 0.3's property test, and it is the single place the claim is made once and exhaustively rather than per-task: over the whole product of `initflow.Conditions`, a non-managed project never proceeds without its confirmation, and the day a new condition is added without a case for it the test fails. **It is not a claim that this plan adds nothing anywhere else**, and the two tasks that deliberately do are named here rather than left for a reviewer to find: **Task 5.1 adds a column to `fugaro secrets ls`** (a new field on an existing read, for a non-managed project too) and **Task 5.2 adds a new top-level command, `fugaro restore`** (a new verb, reachable in any project). Neither touches a confirmation and neither is a shortcut past one; both change the surface a reader sees, and the sentence a reviewer should hold this plan to is only the first one. **That, narrowed, is the plan's answer to the risk this feature carries**: the change that would be catastrophic is not a managed project applying the wrong thing, it is an ordinary project quietly stopping asking.

## Decisions this plan defers to the owner

Its own, continuing the design's D-series with **C1 onward**, each vetoable before its phase starts. **None of the six below is answered**, and neither is any of the design's rows except the five the owner settled on 2026-10-16 (**D55, D56, D57, D63, D64**): nothing below stands as recommended-and-accepted, and neither does anything the answered rows did not touch. The differences the answers make to this plan are folded into the tasks they belong to — D55 into Tasks 3.1 and 3.3 and Global Constraint 1, D56/D57 into Task 3.0 and phase 0's marker, D63 into Task 6.0, D64 into Task 4.2 — and Task 5.4 is where the design's own table is written from them.

| # | Question | Recommendation | Owner's answer |
|---|---|---|---|
| **C1** | Does phase 0's marker live beside the runs bucket's `fugaro/project.json` (the recommendation), or in a bucket of its own? | **Beside it**, because the read is the same read: the bucket's project number is the check that makes the mark mean anything (`internal/infra/project.go:51-84`), and a second bucket is a second IAM surface for no extra assurance. | |
| **C2** | Does the marker record the budget amount and the creation time (Task 4.2 extends it), or stay the three fields `ProjectMarker` has? | **Extend it**, in a versioned shape as `ProjectMarker.Version` already is, because a restore needs to know what the budget was and a version field is how a reader refuses a shape it does not know. | |
| **C3** | Is Task 3.0 (the project label) wanted at all? | **Only if the owner wants a second, independent mark.** The object is enough and the label adds a second identity that must be trusted; the recommendation is that the label is optional and, if present, is an *additional* agreement condition rather than a substitute. | |
| **C4** | Does the review's prompt get the redacted summary or the plan JSON? | **The summary, always.** The summary is what the deterministic gate and the review both read, so there is one artifact to redact and one to test (Task 2.2); handing the review a richer document would create a second, less-tested path out of the plan. | |
| **C5** | Does this plan ship all seven phases in one release, or phase by phase? | **Phase by phase**, each independently mergeable and each useful alone: phase 0 alone makes "Fugaro created this" a readable fact, phase 1 alone is a reusable managed gate, and phase 5's restore is useful before anyone opts into managed mode. D69 places the *feature* after 0.8.0; it does not require the phases to land together. | |
| **C6** | Does `fugaro restore` need its own Check number, or does it share 67? | **It shares 67, as 67b** — the 43a-43f sub-lettering convention, and the two sections genuinely cannot run at the same moment (67a needs a managed project, 67b needs one destroyed). A second number would be a second reservation for one feature and one phase. | |

## Open items, listed rather than fixed

Each is something this plan knows it has not settled, with the reason it is left open rather than answered by assertion:

1. **The residual risk D55's answer accepts is stated, not fixed.** The owner removed the coding-agent refusal for a managed project on 2026-10-16, and the design's §1 item 4, D55's row and §6 item 4 say what replaces it and what is **not** protected — the agent shares the container, the user, `/proc` and the metadata server with the runner, so a gate that lives only in the runner is a gate the agent can go around. Task 3.1's PR description is required to carry that paragraph. **What is open is the reconciliation itself**: this is a risk the owner accepted with the arguments in front of them, not one this plan has closed, and no task below closes it. Task 3.3 is the alternative they did not choose, kept as a task so what the answer gave up can be read beside its cost.
2. **Whether a project-level label can be made unwritable by the run's identity is unverified (design U1).** Task 3.0 asserts it with a test that reads the IAM; if the test cannot be written without a live read, Task 3.0 says so and the label is refused instead. **The recommendation does not depend on the label, which is why this is an open item and not a blocker.**
3. **What a plan JSON of a managed-project shape actually contains is not measured (design U3).** Task 2.2's redaction is written against Terraform's documented plan structure and a fixture; the fixture is synthetic. The task's by-value leak test is the mitigation, not a guarantee about every provider.
4. **The recipes' and the project layer's re-publication path in a restore is named, not designed (Task 5.2's step).** It is the piece of the restore most likely to be wrong, because those objects live in the bucket that died with the project. **The task's instruction is to refuse and name them rather than guess**, which is a correct behaviour and not a complete restore; if the owner wants a complete one, that is a task in its own right.
5. **`fugaro init --create-project`'s own confirmation is unchanged by this plan.** A managed project is still created by a typed confirmation at a terminal, because it is a new project and billing (`initflow.Typed`, `stage.go:315-324`). **The spec's "no prompts for cloud operations in managed projects" therefore starts after creation, not at it** — stated here because the design's D55 discussion is about applies and a reader could reasonably think creation is included.
6. **D74 is open, and Task 1.4 is written to one of its two answers.** If the owner chooses the local config file over the runs-bucket object, Task 1.4 is **rewritten rather than adjusted**: the object, its one writer, its format and its read are all that answer's, and the flag `--allow-delete`'s refusal in a managed project is the one part that survives either answer.
7. **The console's Projects screen will not show anything about managed mode until a `--json` shape carries it**, and this plan adds no shape. That is the design's D71/D72 answer (the console holds no credential and stays read-only) and it is also a deliberate omission: a fact whose whole purpose is to change what gets applied is a poor thing to render in a view-only window with no way to change it.

## OWNER APPROVAL GATE

**Nothing in this plan is approved and nothing in it is built. No task starts until the owner gives an explicit go — including the five whose decisions the owner has already answered.** The deliverable of the pull request that adds this file is the two documents: the reconciliation ([docs/design/managed-projects.md](../design/managed-projects.md)) and this plan. **Concretely:** no code exists for any of it; no file under Task 0.1 onward has been created; `docs/gcp-live-checklist.md` is untouched, which is why Check 67 is *named* and reserved here and its rows and sections are written by Task 5.3; and this plan's own commands have not been run, because there is nothing to run them against.

**The five decisions that gate the most, in the order they matter — nine of the twenty numbers, in five groups: five are answered and four are still open, and no answer is an approval.**

1. **D55 — does a managed project remove the refusal of live applies from a coding-agent session? — ANSWERED 2026-10-16: "Remove it for managed."** The central security decision in the feature, and the answer is **against this design's recommendation**: the agent's session applies, and what replaces the refusal is the deterministic gate (never the model), the cloud marker, the per-address owner-written delete list (D72, and D74 for where each approval lives and who may write it — Task 1.4) and the budget (D63/D64). **Task 3.1 builds that and is where the review should look first** — its scoping predicate (managed **and** a gate verdict that admits the plan **and** a covered class, nothing else), the delete list's provenance, and the inert-until-phase-4 gate; **Task 3.3 keeps the shape the design recommended** (keep the refusal; the agent proposes and the apply runs where no agent is — an owner-run terminal, or a separate job with its own service account sharing no container, environment or filesystem with an agent), unscheduled unless the owner reverses this. **"The runner applies it for the agent" was never the recommended separation**, because the agent shares the runner's container, user, `/proc` and metadata-server identity (`SECURITY.md:47-53`) — and that fact does not stop being true once the refusal is removed for managed projects: it is exactly the residual risk the design's §6 item 4 states, and Task 3.1's PR description is required to carry that paragraph.
2. **D56 and D57 — the marker's form and who may write it. — ANSWERED 2026-10-16: "Cloud marker, owner-run"** — the design's recommendation, unchanged: the runs-bucket marker read with the project-number check, written only by an owner-run command, no credential a run holds having any write path to it (with a test), and a repository's `fugaro.yaml` able to turn managed mode **off** but never **on**. Tasks 0.1 and 0.2 build the read and the writer, Task 3.0 the optional label, and Task 1.0's gate assumes none of it.
3. **D58 and D73 — what "Fugaro created it" means, and where the fact is recorded. — STILL OPEN.** Every phase below reads phase 0's marker, so these decide what phase 0 builds. **D73 is the same question asked about the owner's addendum**: whether "if we create the firebase project" means a GCP project Fugaro created or any project it added Firebase to, which is the difference between `belong` staying exactly as it is and becoming a managed project by an operator's side effect — and it is the one of the two whose wrong answer is a privilege escalation rather than a gap.
4. **D63 and D64 — the hard stop, and the budget amount. — ANSWERED 2026-10-16, both as recommended.** **D63: no hard stop in the first managed release** — alerts plus Fugaro's own spend cap and kill switch, and the reason is the privilege: a hard stop needs a new always-on credential with billing-admin power, which is the largest privilege consequence in the feature after D55. **D64: the budget amount is required at creation, with no silent default.** Task 6.0 carries D63's answer, Task 4.2 D64's, and Task 4.1's inventory fallback and Task 4.2's amount are what Task 3.1's apply stays refused until.
5. **D60 and D61 — the review's role. — STILL OPEN.** The recommendation makes the deterministic checks the gate and the model review advisory and recorded — a verdict can only *add* a refusal, never be the reason a plan is applied. The spec's step 5 read as a control is the alternative, and it is the one the reconciliation refuses on the evidence of the code: same model, same container, attacker-writable input. **D55's answer makes this row more load-bearing rather than less**: with the agent's session applying, the only thing that must never be the gate is the model whose input the agent can write, and Task 2.2's recorded-verdict rule is that sentence in code.

**Answering a decision is not approving the tasks.** When the owner answers D55 to D74, Task 5.4 writes the answers into the design's own table first, and any task whose shape the answers change is rewritten before it is built. **Until then this plan is a document, and every step in it is someone else's future work.**
