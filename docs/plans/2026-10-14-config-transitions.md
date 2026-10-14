# Config transitions that retire cloud resources: implementation plan

**Goal:** a user who changes `fugaro.yaml` (or the local config) in a way that retires cloud resources gets a plain-words retirement summary, a computed list, and one supported, guarded command — instead of Terraform's `prevent_destroy` text before any plan list, and a hand-run `terraform state rm`. **This PR (the one that adds this plan and its design doc) is docs-only: no Go, Terraform or product-behavior file under any "Files" list below is touched by it.**

**Spec:** [docs/design/config-transitions.md](../design/config-transitions.md). Its decisions (§2 inventory, §3 detection, §4 mechanics, §5 where users learn, §6 safety and testing, §7 placement) are settled there; this plan's own decisions continue as P1 onward, each independently vetoable. Its §8 lists the five open questions the owner rules on; Tasks 1–2 are unblocked by any of them, Tasks 3–6 depend on Q1 (§7 placement) and Q4 (§3.1), and Task 4 additionally on Q2/Q3.

**Depends on:** release 0.7.x on `main`. Task 2's row-F work depends on [project-secrets.md](project-secrets.md) (#329) — see P6.

**Checked before this plan was written:** every fact the design cites was read from the code at this branch's base — `internal/infra/spec.go` (`Repo` at `:747`, the `tokens` loop at `:797-800`, `SecretMounts` at `:559`), `internal/infra/discover.go` (`DiscoverRepo` at `:541`, the variant loop at `:624-664`), `internal/infra/readiness.go` (`Readiness` at `:74`), `internal/infra/forget.go`, `internal/infra/imports.go` (`importTable`, `WriteImports` at `:141`), `internal/infra/workdir.go` (`PrepareWorkdir`), `internal/infra/tf/tf.go` (`Plan` at `:27`, `ShowState` at `:246`, `State.Addresses` at `:219`, `StateRm` at `:302`), `internal/infra/tf/guard.go`, `internal/infra/tf/summary.go`, `internal/infra/tf/cover.go` (`NotCovered` at `:200`), `internal/cli/init.go` (`planRepo` at `:2297`, `guard` at `:1090`, the flags at `:370`/`:380`, `ask`/`typed` at `:983`/`:1000`), `internal/cli/init_repo_gate.go`, `internal/cli/init_review.go`, `internal/cli/initsecrets.go` (`agentMarkers` at `:132`), `deploy/terraform/gcp/modules/{repo,workflow}/*.tf`, `docs/gcp-setup.md`, `docs/design/v1.md` §8, `docs/design/project-secrets.md`, `SECURITY.md`, `docs/gcp-live-checklist.md`. If the merged code differs when a task starts, that task's implementer adapts its edits to the then-current file, not the other way round (the same rule `docs/plans/2026-10-08-upgrade.md` uses).

## Decisions (veto any before execution starts)

- **P1. Detection is a set difference in Go, computed before `terraform plan` runs — `Retirements(state, spec)` — and it is a *prediction for consent*, never a substitute for the guard.** The state's addresses (`TF.ShowState` → `State.Addresses()`, `tf.go:219`) minus the addresses the new spec produces (`RepoSpec.Addresses()`, new). Terraform's own plan stays authoritative for what is applied, and `tf.Guard` still runs on it (P4). *Veto alternative:* always run a two-phase plan (write the forgets, plan, read the real delete list), authoritative but slower and it touches the workdir on every run; rejected because it makes the common case pay for the rare one, and because the abort the origin hit happens *before* a plan exists at all. **Owner question Q4.**
- **P2. The retirement mode is a flag on `fugaro init --repo` (`--retire`), not a new command and not documentation alone** (design §4.1). It reuses the state, workdir, spec, plan file, guard and confirmation that already live in `init --repo`, and keeps the config change and the apply in the same checkout. *Veto alternatives:* a dedicated `fugaro repo retire` (duplicates the plumbing and splits one intention into two sequenced commands), and document-only (the origin is the evidence it does not work).
- **P3. Forgetting a retired container is a generated `removed` block with `destroy = false`, written by fugaro into its own workdir, addresses computed, never typed.** `removed` needs Terraform ≥ 1.7, already the floor (`tf.go:83`), and produces the plan action `forget`, which `tf.Guard`'s `knownActions` already includes and already allows (`guard.go:12-16`: "forget, from a removed block, destroys nothing and is allowed"). **No guard is weakened and no new plan action is introduced.** The file is `removed.tf.json`, written and emptied exactly like `imports.tf.json` (`WriteImports`, `imports.go:141`) so an earlier run's file never lingers.
- **P4. The `--allow-delete` list is computed from the real plan, intersected with `Retirements`, and shown for the one typed confirmation.** The user never pastes addresses; an address the summary did not name is still refused by the guard. Same shape as the two shipped precedents, `ForgetAllowDelete` (`forget.go:44`) and `HardeningAllowDelete` (`hardening.go:160`).
- **P5. A job whose workflow has already left the configuration is forgotten, then deleted, never "unprotected".** Once the job is out of the configuration there is no resource for `--allow-job-delete` (a tfvars *variable*) to act on, so its `deletion_protection = true` in state makes the provider refuse the delete. Order: `removed` block → apply → delete the live job. The delete reads the job's labels first and refuses if they are not ours (`fugaro=managed`, `fugaro_repo`, `fugaro_workflow`; `modules/workflow/job.tf:11-14`), the same ownership rule discovery already applies (`v1.md` §8.2). **Owner questions Q2 (delete vs print the `gcloud` command) and Q3 (offer the `--allow-job-delete` pre-step when the file has not yet changed).**
- **P6. Row F of the design's inventory (a provider or named account dropped from the local config) gets no mechanism here.** It is the identical hazard project-secrets' N3/N4 fix closes by discovering and unioning labelled secrets back into the computed map so it can only grow. This plan adds nothing for it and must not; Task 2 only *pins* it with a test, and the release placement (P7) keeps the two from shipping in a way that leaves the invariant owned by neither. *Veto alternative:* a second union here; rejected as two mechanisms for one invariant, which is exactly what the design's §2 refuses.
- **P7. Release placement: the detection and explanation half (Tasks 1, 2, 5, 6) is shippable as 0.7.1 with no dependency on #329; the retirement mechanics (Tasks 3, 4) follow as 0.7.2 once #329's discovery change is in.** **Owner question Q1.** No new `fugaro.yaml` key in either half, so the three-edit rule is not triggered; a `*Since` gate is added only if Task 3 finds the forget file must be gated on a repository's last `init --repo` having been run by a binary that writes it.

## Global Constraints

- **Nothing weakens an existing guard.** `tf.Guard`, `--allow-delete`'s meaning, `--allow-job-delete`'s meaning, and `cover.go`'s allowlist are all unedited. The retirement mode *computes* an allow-list from a list the user confirmed.
- **`--yes`, `--non-interactive` and `--json` never cover a retirement**, and a coding agent's session refuses it through the existing `agentEnv(os.Getenv)` marker (`initsecrets.go:146`, markers at `:132`), the same rule and the same stated reason as every other apply today (`init.go:371`).
- **Fugaro never deletes a secret or a secret version.** No new code path calls `secrets.delete`, `versions.destroy` or `versions.delete`; every forget carries `destroy = false`; the containers keep `prevent_destroy`. A test asserts the fake Secret Manager's delete/destroy methods are never reached.
- **A forgotten secret stays re-adoptable by label.** No new label, mark or registry: re-adoption is `DiscoverRepo`'s existing labelled-secret path (`discover.go:551-572`), unchanged.
- **No live cloud or network call in any automated test** — `gcpfake` and the fake terraform runner only.
- **Docs must match code.** `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m` stays green throughout. **A proposed command or flag is never written into a doc the lint scans as if it existed**: `TestBaseImageDocCommandFlagsAreReal` (`docs_base_test.go:173`) resolves every `--flag` near a `trackedDocCommands` mention in `docs/gcp-setup.md` (among others) against the real cobra flag set, so `--retire` in `docs/gcp-setup.md` fails until Task 4 ships it, and the skill lint (`plugin/skills_lint_test.go`) resolves every `fugaro …` mention in `plugin/skills/` against the tree. Task 6's docs edits either land after Task 4, or are written with the flag marked proposed in a form the lint does not resolve as a command line.
- Project rules carried over: every task's test run is in the foreground, `-count=1`; fakes only; `gofmt -l .` clean; commit per task with the repository's `Co-Authored-By`/`Claude-Session` lines.

## Review Focus (mutation-proof notes)

1. **A retirement the summary did not name is allowed through.** Pinned by a test asserting `RetireAllowDelete` returns only addresses in `Retirements` — feed it a plan containing an extra, unlisted delete and assert it is *not* returned, so the guard still refuses it (Task 3).
2. **A secret is destroyed.** Pinned by a golden test that the plan produced in retirement mode contains the action `forget` and **never** `delete`, for every `google_secret_manager_secret.*` address, plus a test asserting the fake's delete/destroy methods were never called (Tasks 3, 5).
3. **A named-account variant is retired when it should not be, or survives when it should not.** Pinned by a spec-level test: the variant key set for `oauth` with one allowed account is `{wf, wf_acct}` and for `api-key` is `{wf}` (Task 1), and by a `Retirements` test using the origin's real addresses (Task 2).
4. **The check job is retired when a workflow merely changes image, or survives when the last checked workflow goes clone.** Pinned by a `Retirements` test over both `baked`→`clone` and `clone`→`baked` (Task 2).
5. **The raw Terraform `prevent_destroy` text reaches the user.** Pinned by a golden test over the origin transcript as a fixture: `init --repo`'s output contains the retirement summary and does **not** contain `lifecycle.prevent_destroy set` (Task 5).
6. **State and cloud are left disagreeing silently after a half-failed retirement.** Pinned by a failure-injection test (the apply fails after the forgets are written; the API delete fails after the apply) asserting the run names exactly which addresses were forgotten and which were not, and that a rerun resumes rather than re-forgetting (Task 5).
7. **The ownership check on the orphaned job is skipped.** Pinned by a test where the live job lacks our labels: the delete must refuse and print the `gcloud` command instead (Task 4).

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/infra/spec.go`, `spec_test.go` | `RepoSpec.Addresses()` — every address the spec will produce, built like `imports.go`'s `importTable`; the variant-key assertions | 1 |
| `internal/infra/retire.go` (new), `retire_test.go` (new) | `Retirement`, `Retirements(state, spec)`, the per-row cause/kept text, `RetireAllowDelete(plan, retirements)` | 2 |
| `internal/infra/removed.go` (new), `removed_test.go` (new) | `RemovedFile`, `WriteRemoved(dir, addrs)` — the `removed.tf.json` writer, `imports.go`'s twin | 3 |
| `internal/cli/init.go`, `init_retire.go` (new), `init_retire_test.go` (new) | `--retire` flag, the `⚠ RETIRE` banner, the typed confirmation naming every secret left behind, the two-phase flow inside `planRepo` (`init.go:2297`) | 4 |
| `internal/cli/init.go`, `init_retire.go`, `internal/infra/tf/tf.go` | the `prevent_destroy` re-framing when `t.Plan` fails, and the orphaned-job delete through the Cloud Run API with the label check | 4 |
| `internal/cli/init_output_test.go`, `testdata/` | the origin transcript as a golden fixture; the `--json` `retire` field | 5 |
| `docs/gcp-setup.md`, `docs/design/v1.md`, `docs/design/config-transitions.md`, `plugin/skills/{setup,upgrade}/SKILL.md`, `docs/gcp-live-checklist.md`, `docs/release.md` | the docs section, the cross-references, the skills' refusal text, Check 42's row and section, the release bullet | 6 |
| `internal/cli/init_retire_test.go`, `internal/infra/retire_test.go`, `internal/cli/init_repo_stage_test.go` | the round-trip, the reverse transition, the half-failure resume | 5 |

## PR group

One PR, Tasks 1–6. Task 7 is the owner-run live check and is **not** part of it.

---

### Task 1: `RepoSpec.Addresses()` — every address the spec produces

**Files:**
- Edit: `internal/infra/spec.go`
- Test: `internal/infra/spec_test.go`

**Interfaces:**
```go
// package infra
// Addresses are the Terraform addresses spec produces, in the repository
// root. Deterministic from the spec alone: the same spec always yields the
// same set, which is what makes Retirements a set difference.
func (rs RepoSpec) Addresses() []string
```
Built from the same facts `imports.go`'s `importTable` already encodes (`imports.go:83-105`): `module.repo.google_secret_manager_secret.this[<logical>]` per `rs.Secrets` key; the operator grants; the registry and its grants; the build account and its grants; the check job, invoker grant and Scheduler job when `rs.Check != nil`; and, per `rs.Workflows` key, `module.repo.module.workflow[<key>]`'s job, service account, operator grants, bucket grant, secret accessors, vertex grant and launcher grants. Keys are quoted with `strconv.Quote`, as `newImport` does. Sorted.

- [ ] **Step 1: Write the failing tests**

```go
// internal/infra/spec_test.go
func TestRepoAddressesNamedVariantsOnlyUnderOAuth(t *testing.T) {
    // oauth with one allowed account: the variant keys are {wf, wf_acct};
    // api-key: {wf}. Assert against Addresses()'s
    // module.repo.module.workflow["<key>"] prefixes, not against RepoSpec.Workflows
    // alone, so a variant that is in the map but missing from the addresses
    // (or the reverse) fails.
}
func TestRepoAddressesIncludesCheckOnlyWhenChecked(t *testing.T) {
    // a repo whose only workflow is checkout: clone has no
    // module.repo.google_cloud_run_v2_job.check[0]; baked has it.
}
```
Run: `go test ./internal/infra -run 'TestRepoAddresses' -count=1`
Expected: **FAIL** — `rs.Addresses undefined`.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.
- [ ] **Step 3: `gofmt -l .`** Expected: no output.
- [ ] **Step 4: Commit.**

---

### Task 2: `Retirements` — the set difference and its plain-words explanation

**Files:**
- Create: `internal/infra/retire.go`, `internal/infra/retire_test.go`

**Interfaces:**
```go
// package infra
type Retirement struct {
    Address string
    Kind    string // "secret container", "workflow job", "check job", "Scheduler job", …
    Cause   string // the config change, in the user's words
    Kept    string // what survives, or "" when nothing does
}
func Retirements(state []string, spec RepoSpec) []Retirement
```
`Cause` is derived, not guessed: a `module.repo.module.workflow["<wf>_<acct>"]` address whose `<wf>` is still in `spec.Workflows` under another key is "the named Claude account <acct> is no longer a variant this repository deploys"; one whose `<wf>` is gone entirely is "workflow <wf> was removed from fugaro.yaml". A secret container whose logical name is a Claude account secret is "agent.auth no longer mounts <logical>"; one that is a workflow-only secret is "no workflow mounts <logical> any more". `Kept` is non-empty for every `google_secret_manager_secret.*`: "the secret and its value stay in Secret Manager; its container carries the fugaro labels, so re-adding it re-adopts it".

- [ ] **Step 1: Write the failing tests**

```go
// internal/infra/retire_test.go
func TestRetirementsOriginTransition(t *testing.T) {
    // The real 2026-10-14 case: a state holding the two OAuth containers and
    // the go_bmb variant's addresses, and an api-key spec. Assert every one
    // of the 11 delete addresses is returned, that both containers carry a
    // non-empty Kept, and that nothing the spec still produces is returned.
}
func TestRetirementsBakedCloneCheckJob(t *testing.T)  // design row E, both directions
func TestRetirementsReverseAdoptsCleanly(t *testing.T) // api-key -> oauth: zero secret containers retire
func TestRetirementsProviderDroppedIsTheUnionCase(t *testing.T) // P6: pins that this design adds no mechanism
```
Run: `go test ./internal/infra -run 'TestRetirements' -count=1`
Expected: **FAIL** — `Retirements undefined`.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.
- [ ] **Step 3: `gofmt -l .`** Expected: no output.
- [ ] **Step 4: Commit.**

---

### Task 3: `removed.tf.json` and the computed allow-list

**Files:**
- Create: `internal/infra/removed.go`, `internal/infra/removed_test.go`
- Edit: `internal/infra/retire.go`, `internal/infra/retire_test.go`

**Interfaces:**
```go
// package infra
const RemovedFile = "removed.tf.json"
// WriteRemoved writes the forgets for addrs into dir, sorted, or an empty
// configuration when there are none, so an earlier run's file is replaced.
// Every entry carries destroy = false.
func WriteRemoved(dir string, addrs []string) error

// RetireAllowDelete is the guard's allow-list for p: each delete or replace
// of an address in retirements, and nothing else.
func RetireAllowDelete(p *tf.Plan, retirements []Retirement) []string
```
`removed.tf.json` is `{"removed":[{"from":"<addr>","lifecycle":{"destroy":false}}]}`, exactly `WriteImports`'s shape (`imports.go:141-155`). **Flagged for opus review (Terraform-sensitive):** this is the file that decides what Terraform forgets, and `destroy = false` is the one attribute standing between a retired container and a destroyed secret.

- [ ] **Step 1: Write the failing tests**

```go
func TestWriteRemovedGolden(t *testing.T)      // sorted; {} when empty; destroy=false on every entry
func TestRetireAllowDeleteNeverAllowsAnUnnamedAddress(t *testing.T) // Review Focus 1
```
Run: `go test ./internal/infra -run 'TestWriteRemoved|TestRetireAllowDelete' -count=1`
Expected: **FAIL** — undefined.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.
- [ ] **Step 3: `gofmt -l .`** Expected: no output.
- [ ] **Step 4: Commit.**

---

### Task 4: `fugaro init --repo --retire` — the guarded flow

**Files:**
- Create: `internal/cli/init_retire.go`, `internal/cli/init_retire_test.go`
- Edit: `internal/cli/init.go` (`planRepo` at `:2297`, the flag block near `:370`/`:380`, the `--repo`-only validation at `:685`)

**Interfaces:**
```go
// internal/cli — initOptions
retire bool // --retire

// init_retire.go
// retirePlan is the retirement's own discover/plan/confirm/apply, called from
// planRepo in place of the ordinary plan when Retirements is non-empty.
func (r *initRun) retirePlan(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, spec infra.RepoSpec, rets []infra.Retirement, opts infra.RepoRootOptions) ([]infra.Missing, error)
// printRetirements is the ⚠ RETIRE summary (design §3.2).
func (r *initRun) printRetirements(spec infra.RepoSpec, rets []infra.Retirement) error
// deleteOrphanedJob deletes a job the forget left unmanaged, after checking
// its labels; it refuses and prints the gcloud command for a job that is not
// ours.
func deleteOrphanedJob(ctx context.Context, c *infra.Clients, project, region, job string) error
```
Flow inside `planRepo`, before `t.Plan` (`init.go:2326`): read the state (`t.ShowState`), compute `Retirements`, and if non-empty print `printRetirements` and — without `--retire` — refuse with exit 1, naming the next command. With `--retire`: write `removed.tf.json` (Task 3), plan, show, `guard(plan, infra.RetireAllowDelete(plan, rets))` (`init.go:1090`'s wrapper, unchanged), print the `⚠ RETIRE` banner and the counts, take **one typed confirmation naming every secret that will be left in Secret Manager**, apply, then delete each orphaned job. `removed.tf.json` is emptied on the next run. **Refused** under `--yes`, `--non-interactive`, `--json` and `agentEnv(os.Getenv)`, following `ask`'s existing refusals (`init.go:983-994`). **Flagged for opus review (security- and Terraform-sensitive):** the consent gate, the API delete, and the label ownership check.

- [ ] **Step 1: Write the failing tests**

```go
func TestInitRepoRetireRefusesWithoutTheFlag(t *testing.T)          // prints the summary, exits 1, applies nothing
func TestInitRepoRetireConfirmationNamesEveryKeptSecret(t *testing.T)
func TestInitRepoRetireNeverCoveredByYes(t *testing.T)              // and by --non-interactive, --json, agentEnv
func TestInitRepoRetireUserChangedFileFirstUsesForgetThenAPI(t *testing.T) // Review Focus: the origin order
func TestDeleteOrphanedJobRefusesAJobThatIsNotOurs(t *testing.T)    // Review Focus 7
```
Run: `go test ./internal/cli -run 'TestInitRepoRetire|TestDeleteOrphanedJob' -count=1`
Expected: **FAIL**.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.
- [ ] **Step 3: `gofmt -l .`** Expected: no output.
- [ ] **Step 4: Commit.**

---

### Task 5: The early abort, the golden transcript, and resume after a half-failure

**Files:**
- Edit: `internal/cli/init_retire.go`, `internal/cli/init.go` (the `t.Plan` failure path at `:2326`), `internal/cli/init_output_test.go`, `testdata/`
- Test: `internal/cli/init_retire_test.go`, `internal/infra/retire_test.go`

**Interfaces:** no new exported API. The `--json` result document gains `retire` (`[]infra.Retirement`); `failed[].error` keeps the raw Terraform text, so nothing is hidden, only re-framed.

- [ ] **Step 1: Write the failing tests**

```go
func TestInitRepoPreventDestroyReframed(t *testing.T)   // Review Focus 5: the origin's exact stderr
func TestInitRepoRetireHalfFailureNamesWhatWasForgotten(t *testing.T) // Review Focus 6, both phases
func TestInitRepoRetireResumeDoesNotReforget(t *testing.T)
func TestInitRepoJSONCarriesRetirements(t *testing.T)
```
Run: `go test ./internal/cli -run 'TestInitRepoPreventDestroy|TestInitRepoRetireHalf|TestInitRepoRetireResume|TestInitRepoJSONCarries' -count=1`
Expected: **FAIL**.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.
- [ ] **Step 3: The secret-safety assertion** (Review Focus 2): a test that the fake Secret Manager's `Delete` and `Destroy` are never called across every retirement test.
- [ ] **Step 4: `gofmt -l .`** Expected: no output.
- [ ] **Step 5: Commit.**

---

### Task 6: Docs, the skills, Check 42 and the release bullet

**Files:**
- **Edit: `docs/gcp-setup.md`** — a new section, "Changing auth, or removing a workflow, secret or provider", with the ordered steps and the 2026-10-14 worked example. **Ordering constraint: this lands after Task 4**, because `TestBaseImageDocCommandFlagsAreReal` (`docs_base_test.go:173`) resolves every `--flag` near a `trackedDocCommands` mention in this file against the real flag set, so `--retire` must exist in the cobra tree first (or the section must describe the command without a resolvable flag token). The two existing manual sections ("Turning checks off, or removing a workflow", "Offboarding a repository") are edited to point at it, keeping their "the order matters" advice and adding that `fugaro init --repo` now says when you have passed it.
- **Edit: `docs/design/v1.md`** §8.2 — one cross-reference sentence beside the existing "Offboarding isn't a command yet" paragraph, and the `--retire` flag added to the flag list.
- **Edit: `docs/design/config-transitions.md`** — flip its Status line from "design" to "implemented in 0.7.x" and record the owner's rulings on its §8 questions.
- **Edit: `plugin/skills/setup/SKILL.md`** (and `plugin/skills/upgrade/SKILL.md` if the refusal can appear there) — the refusal text an agent will see, with the instruction not to attempt the transition from an agent session and to hand the user the `--retire` command. The skill lint (`plugin/skills_lint_test.go`, which resolves every `fugaro …` mention against the tree) is what makes this safe only after Task 4.
- **Edit: `docs/gcp-live-checklist.md`** — Check 42's row in the table and its section (below).
- **Edit: `docs/release.md`** — one plan-shape bullet: confirm the release note names the retirement sequence, and that a repository applying the `repo` root for the first time after this ships sees no unexpected diff. No `*Since` constant unless Task 3 finds one is needed (P7).

- [ ] **Step 1: Write the docs edits.**
- [ ] **Step 2: Run the docs tests**

Run: `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m`
Expected: `ok`. Also `go test ./plugin -run 'Lint|Skill' -count=1` for the skill lint.

- [ ] **Step 3: Commit.**

---

### Task 7: Owner-run live check 42 (not part of this PR)

**`docs/gcp-live-checklist.md` Check 42: the config-transition round trip (installation `fugaro`, sandbox repository only, run by you; NOT RUN).**

Confirms [docs/design/config-transitions.md](../design/config-transitions.md) §3 and §4 against the real provider, not only the fakes. **Never `belong`.** Register restoring the sandbox's pre-check state as your own cleanup step, first.

1. **The abort, reproduced.** With the sandbox's `agent.auth: oauth` and one named account configured, note the two secret IDs (`fugaro secrets ls`) and the variant's job (`gcloud run jobs list --filter labels.fugaro_repo=<sandbox>`). Change `agent.auth` to `api-key` and run `fugaro init --repo .` **with no flags**. **FACT:** the retirement summary's full text, confirming it appears **before** any Terraform error and names the config change, the addresses, the kept secrets and the next command; and that no `lifecycle.prevent_destroy set` text reaches the terminal.
2. **The supported path.** `fugaro init --repo . --retire`. **FACT:** the exact typed confirmation (naming both secrets), the plan's counts, and the resulting `terraform state list` — confirming no `google_secret_manager_secret.*` address for either OAuth secret remains, and that no `google_secret_manager_secret` was destroyed.
3. **The secrets survived.** `gcloud secrets describe <id>` for both. **FACT:** both still exist, still carry `fugaro=managed`, `fugaro_repo`, `fugaro_secret`, and still have an **enabled version** (the version number only, never the value).
4. **The orphan is gone and ours.** **FACT:** the retired variant's job is absent from `gcloud run jobs list`; and a deliberately mislabelled job (create one with no `fugaro` labels, if the project allows) is refused by the delete, with the printed `gcloud` command (Review Focus 7).
5. **The reverse transition.** Change `agent.auth` back to `oauth` and run `fugaro init --repo .`. **FACT:** the plan is **imports and creates only, with zero deletes of any `google_secret_manager_secret.*`**; both containers are re-adopted by label; the named variant's job is re-created; and the previously stored value still works (one sandbox run, or `fugaro doctor`).
6. **Restore** the pre-check state.

**Paste back:** every `FACT` line above, never a secret value.

- [ ] **Step 1: Run it at your own terminal, on the sandbox only.**
- [ ] **Step 2: Paste the `FACT` lines into the checklist's Check 42 section and its table row.**
- [ ] **Step 3: Commit.**

---

### Task 8: Full suite

- [ ] **Step 1:** `go test ./... -count=1 -timeout 20m`
- [ ] **Step 2:** `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m`
- [ ] **Step 3:** `gofmt -l .`
- [ ] **Step 4:** `fugaro verify test` (the repository's configured command), then commit.
