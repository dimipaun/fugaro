# Config transitions that retire cloud resources: implementation plan

**Goal:** a user who changes `fugaro.yaml` (or the local config) in a way that retires cloud resources gets a plain-words retirement summary, a computed list, and one supported, guarded command — instead of Terraform's `prevent_destroy` text before any plan list, and a hand-run `terraform state rm`. **This PR (the one that adds this plan and its design doc) is docs-only: no Go, Terraform or product-behavior file under any "Files" list below is touched by it.**

**Spec:** [docs/design/config-transitions.md](../design/config-transitions.md). Its decisions (§2 inventory, §3 detection, §4 mechanics, §5 where users learn, §6 safety and testing, §7 placement) are settled there; this plan's own decisions continue as P1 onward, each independently vetoable. Its §8 lists the six open questions the owner rules on; Tasks 1–2 are unblocked by any of them, Tasks 3–7 depend on Q1 (§7 placement) and Q4 (§3.1), and Task 5 additionally on Q2/Q3 (Task 4, the test fakes, is independent of all six).

**Depends on:** release 0.7.x on `main`. **Task 2 is not independent of [project-secrets.md](project-secrets.md) (#329):** its row-F test (`TestRetirementsProviderDroppedIsTheUnionCase`) is written against #329's `Existing.ExtraSecrets`, which is design-only and **not on `main`**. Until #329 merges, that test either waits or is written against the then-current `DiscoverRepo` output and renamed — see P6.

**Checked before this plan was written:** every fact the design cites was read from the code at this branch's base — `internal/infra/spec.go` (`Repo` at `:747`, the `tokens` loop at `:797-800`, `SecretMounts` at `:559`), `internal/infra/discover.go` (`Clients` at `:45` and `NewClients` at `:100` — the operator-ADC client `Orphans` and the API delete both use — `DiscoverRepo` at `:541`, which `Get`s each expected logical name at `:546-572` and does **not** list by label, and the variant loop at `:624-664`), `internal/infra/readiness.go` (`Readiness` at `:74`), `internal/infra/forget.go` (`ForgetAllowDelete` at `:44`), `internal/infra/imports.go` (`importTable`, `WriteImports` at `:141`), `internal/infra/workdir.go` (`PrepareWorkdir`), `internal/infra/tf/tf.go` (`Plan` *type* at `:27`, the `(*TF).Plan` *method* at `:149`, the version floor at `:81`, `ShowState` at `:246`, `State.Addresses` at `:219`, `StateRm` at `:302`), `internal/infra/tf/guard.go`, `internal/infra/tf/summary.go`, `internal/infra/tf/cover.go` (`NotCovered` at `:200`), `internal/infra/tf/real_terraform_test.go` (the `forgotten` fixture at `:58-63` and its golden `internal/infra/tf/testdata/plan_forget.json`), `internal/cli/init.go` (`planRepo` at `:2297`, `guard` at `:1092` (its doc comment is `:1090`), the flags at `:370`/`:380`, `ask`/`typed` at `:983`/`:1000`, `StateRm` callers at `:1751`/`:2588`), `internal/cli/init_repo_gate.go` (`confirmRepo` at `:133`), `internal/cli/init_review.go`, `internal/cli/initsecrets.go` (`agentMarkers` at `:132`, `agentEnv` at `:146`), `internal/cli/init_test.go` (`fakeTerraform` at `:42`), `deploy/terraform/gcp/modules/{repo,workflow}/*.tf`, `docs/gcp-setup.md`, `docs/design/v1.md` §8, `docs/design/project-secrets.md`, `SECURITY.md`, `docs/gcp-live-checklist.md`. If the merged code differs when a task starts, that task's implementer adapts its edits to the then-current file, not the other way round (the same rule `docs/plans/2026-10-08-upgrade.md` uses).

## Decisions (veto any before execution starts)

- **P1. Detection is a set difference in Go, computed before `terraform plan` runs — `Retirements(state, spec)` — and it is a *prediction for consent*, never a substitute for the guard.** The state's addresses (`TF.ShowState` → `State.Addresses()`, `tf.go:219`) minus the addresses the new spec produces (`RepoSpec.Addresses()`, new). Terraform's own plan stays authoritative for what is applied, and `tf.Guard` still runs on it (P4). *Veto alternative:* always run a two-phase plan (remove the state, plan, read the real delete list), authoritative but slower and it touches the state on every run; rejected because it makes the common case pay for the rare one, and because the abort the origin hit happens *before* a plan exists at all. **Owner question Q4.**
- **P2. The retirement mode is a flag on `fugaro init --repo` (`--retire`), not a new command and not documentation alone** (design §4.1). It reuses the state, workdir, spec, plan file, guard and confirmation that already live in `init --repo`, and keeps the config change and the apply in the same checkout. *Veto alternatives:* a dedicated `fugaro repo retire` (duplicates the plumbing and splits one intention into two sequenced commands), and document-only (the origin is the evidence it does not work).
- **P3. Forgetting a retired address is `(*TF).StateRm` over the computed addresses — the mechanism that already exists, is already used by `--forget`, and is exactly what the origin's owner ran by hand.** `StateRm` (`tf.go:302`) takes exact instance addresses, refuses `""` and anything starting with `-`, and runs `terraform state rm`, which destroys nothing and is idempotent per address (a second `state rm` of an address no longer in state fails with "No matching objects found" — **the filter in `Removals` is the resume path's mechanism and tolerating that error is its second line of defence, in that order; Task 3 spells out which is which**, Review Focus 6). It is the same call `init.go` already makes at `:1751` and `:2588`. **No guard is weakened and no new plan action is introduced:** no `removed` block, so no new `forget` action for `tf.Guard` to consider. *Veto alternative, not this PR's plan:* a generated `removed` block with `destroy = false` (Terraform ≥ 1.7, already the floor at `tf.go:81`) also produces a `forget` action that `tf.Guard` already allows (`guard.go:12-16`), and would keep the removal inside the plan — but it is **unverified offline** for the addresses this transition actually retires: the only `removed` usage proven in this repository is a bare whole-resource address (`removed { from = terraform_data.d }`, `real_terraform_test.go:58-63`), and the review's reading is that `removed.from` takes a configuration address with no instance keys and **fails when the resource or module is still declared in the configuration** — which both `google_secret_manager_secret.this` (a `for_each` over `var.repo.secrets`, still declared for the surviving keys) and `module.workflow["go_bmb"]` (still declared for the surviving variants) are. **Check 42 carries that as a hard precondition FACT** (Task 8): the removed-block variant is adopted only if the live check proves it against the real provider, and `StateRm` is the primary mechanic either way.
- **P4. The `--allow-delete` list is computed from the real plan, intersected with `Retirements`, and shown for the one typed confirmation.** The user never pastes addresses; an address the summary did not name is still refused by the guard. Same shape as the two shipped precedents, `ForgetAllowDelete` (`forget.go:44`) and `HardeningAllowDelete` (`hardening.go:160`).
- **P5. A job whose workflow has already left the configuration is state-removed, then deleted through the API, never "unprotected".** Once the job is out of the configuration there is no resource for `--allow-job-delete` (a tfvars *variable*) to act on, so its `deletion_protection = true` in state makes the provider refuse the delete. **Order: `StateRm` the job's address → plan and apply → delete the live job through the Cloud Run API** (removal before the plan, API delete after the apply; the reverse order leaves the provider refusing). The delete reads the job's labels first and refuses if they are not ours (`fugaro=managed`, `fugaro_repo`, `fugaro_workflow`; `modules/workflow/job.tf:11-14`), the same ownership rule discovery already applies (`v1.md` §8.2). **Owner questions Q2 (delete vs print the `gcloud` command) and Q3 (offer the `--allow-job-delete` pre-step when the file has not yet changed).**
- **P6. Row F of the design's inventory (a provider or named account dropped from the local config) gets no mechanism here.** It is the identical hazard project-secrets' N3/N4 fix closes by discovering and unioning labelled secrets back into the computed map so it can only grow. This plan adds nothing for it and must not; Task 2 only *pins* it with a test. That test names #329's `Existing.ExtraSecrets`, which is **design-only and not on `main`**, so Task 2 is not independent of #329 (see **Depends on**): the pin is written against whatever `DiscoverRepo` returns at the time, and gains its final form when #329 lands. The release placement (P7) keeps the two from shipping in a way that leaves the invariant owned by neither. *Veto alternative:* a second union here; rejected as two mechanisms for one invariant, which is exactly what the design's §2 refuses.
- **P7. Release placement: one release, 0.7.2, carrying the change tasks — Tasks 1–7, the test fakes of Task 4 included — together, and one PR for them.** The split the first draft of this plan proposed (detection and explanation in 0.7.1, mechanics in 0.7.2) is not a real split: the summary's next command is `--retire` (§3.2, §4.1), so a detection-only release would print a command that does not exist and leave the user exactly where the origin started, only with better prose. Design §7 reaches the same conclusion. **Owner question Q1.** The dependency on #329 is therefore a release-order dependency only for row F's *pin*, not for the mechanism: Task 2's test is written against `main`'s `DiscoverRepo` today and tightened when #329 lands. *Veto alternative:* detection-only 0.7.1, whose next command is the two manual sections in `docs/gcp-setup.md` rather than `--retire` — documented in design §7, viable, but it does not make the transition supported. No new `fugaro.yaml` key in any case, so the three-edit rule is not triggered; a `*Since` gate is added only if Task 3 finds the `state rm` path must be gated on a repository's last `init --repo` having been run by a binary that knows about retirement.

## Global Constraints

- **Nothing weakens an existing guard.** `tf.Guard`, `--allow-delete`'s meaning, `--allow-job-delete`'s meaning, and `cover.go`'s allowlist are all unedited. The retirement mode *computes* an allow-list from a list the user confirmed.
- **`--yes`, `--non-interactive` and `--json` never cover a retirement**, and a coding agent's session refuses it through the existing `agentEnv(os.Getenv)` marker (`initsecrets.go:146`, markers at `:132`), the same rule and the same stated reason as every other apply today (`init.go:371`).
- **Fugaro never deletes a secret or a secret version.** No new code path calls `secrets.delete`, `versions.destroy` or `versions.delete`; the retirement removes the container's address from *state* (`terraform state rm`, which destroys nothing) and the container keeps `prevent_destroy` in the configuration. A test asserts the fake Secret Manager's delete/destroy methods are never reached (`gcpfake`, Task 6 Step 3).
- **A state-removed secret stays re-adoptable by label.** No new label, mark or registry: re-adoption is `DiscoverRepo`'s existing path, which `Get`s each logical name the spec expects (`discover.go:546-572`), unchanged, because `state rm` leaves the live Secret Manager secret — and its `fugaro_secret` label — exactly as it was. (Listing *orphans* by label is a different call, Task 4's `secrets.list`: `DiscoverRepo` cannot find a secret the spec no longer names, which is the definition of one.)
- **No live cloud or network call in any automated test** — `gcpfake` and the fake terraform runner only.
- **Docs must match code.** `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m` stays green throughout. **A proposed command or flag is never written into a doc the lint scans as if it existed**: `TestBaseImageDocCommandFlagsAreReal` (`docs_base_test.go:173`) resolves every `--flag` near a `trackedDocCommands` mention in `docs/gcp-setup.md` (among others) against the real cobra flag set, so `--retire` in `docs/gcp-setup.md` fails until Task 5 ships it, and the skill lint (`plugin/skills_lint_test.go`) resolves every `fugaro …` mention in `plugin/skills/` against the tree. Task 7's docs edits either land after Task 5, or are written with the flag marked proposed in a form the lint does not resolve as a command line.
- Project rules carried over: every task's test run is in the foreground, `-count=1`; fakes only; `gofmt -l .` clean; commit per task with the repository's `Co-Authored-By`/`Claude-Session` lines.

## Review Focus (mutation-proof notes)

1. **A retirement the summary did not name is allowed through.** Pinned by a test asserting `RetireAllowDelete` returns only addresses in `Retirements` — feed it a plan containing an extra, unlisted delete and assert it is *not* returned, so the guard still refuses it (Task 3, `TestRetireAllowDeleteNeverAllowsAnUnnamedAddress`).
2. **A secret is destroyed.** Pinned three ways: a golden test that the plan produced in retirement mode contains **no** `delete` action for any `google_secret_manager_secret.*` address (and that the addresses the retirement removes are gone from `terraform state list`, not from the cloud); a test that `StateRm` was called with exactly the computed **protected** addresses — the containers and the jobs, and never a retired service account or IAM member, which the plan must delete (design §4.2(a), Task 3's `TestRemovalsKeepsTheAddressesTheApplyMustDelete`) — and that no `terraform destroy`/`apply -destroy` was ever run; and a test asserting the fake Secret Manager's delete/destroy methods were never called (Tasks 3, 6 — the counter it reads is Task 4's).
3. **A named-account variant is retired when it should not be, or survives when it should not.** Pinned by a spec-level test: the variant key set for `oauth` with one allowed account is `{wf, wf_acct}` and for `api-key` is `{wf}` (Task 1, `TestRepoAddressesNamedVariantsOnlyUnderOAuth`), and by a `Retirements` test using the origin's real addresses (Task 2, `TestRetirementsOriginTransition`).
4. **The check job is retired when a workflow merely changes image, or survives when the last checked workflow goes clone.** Pinned by a `Retirements` test over both `baked`→`clone` and `clone`→`baked` (Task 2, `TestRetirementsBakedCloneCheckJob`) **and, for the label half, by `Orphans`' own pair in Task 4** (`TestOrphansNeverReturnsTheCheckJobItStillNeeds` / `TestOrphansCheckJobIsAnOrphanWhenSpecCheckIsNil`): `Retirements` can only see a check job the **state** still holds, so the two tests together are what cover a check job the state has already lost. The check job carries `fugaro_role=check` and no `fugaro_workflow` (`check.tf:22-26`), so an orphan rule written for workflow jobs alone gets it wrong in both directions (review B2, design §3.1a).
5. **The raw Terraform `prevent_destroy` text reaches the user, and the matcher does not silently never fire.** Pinned by a golden test over the origin transcript as a fixture: `init --repo`'s output contains the retirement summary and does **not** contain `lifecycle.prevent_destroy set` (Task 6, `internal/cli/testdata/init_retire.golden`) — and by a test whose input is the real **wrapped** error text (`internal/cli/testdata/prevent_destroy_wrapped.txt`, where Terraform breaks after `has`), asserting both that the whitespace-normalized match fires and that the unnormalized one does not. The fixture is regenerated by the build-tagged real-Terraform test, so the fake-based CI test cannot pass against text Terraform never produces.
6. **State and cloud are left disagreeing silently after a half-failed retirement.** Pinned by two failure-injection tests — the plan/apply fails after the state removals have run, and the API delete fails after a successful apply — asserting the run names exactly which addresses were removed from state and which were not, that the orphaned-job delete is named as the remaining step, and that a rerun treats an already-removed address as done rather than re-failing (Task 6, `TestInitRepoRetireHalfFailureNamesWhatWasRemoved`, `TestInitRepoRetireResumeDoesNotReremove`).
7. **The ownership check on the orphaned job is skipped, or applied with the wrong label for the job's kind.** Pinned by three tests: the live job lacks our labels at all — the delete must refuse and print the `gcloud` command instead (`TestDeleteOrphanedJobRefusesAJobThatIsNotOurs`); a job with our repo label but **another** `fugaro_workflow` is refused, not deleted (`TestDeleteOrphanedJobRefusesAnotherVariantsJob`); and the **check job** — `fugaro_role=check`, no `fugaro_workflow` — **is** deleted, under its own label set (`TestDeleteOrphanedJobDeletesTheCheckJobUnderTheRoleLabel`), because a rule demanding a workflow label of it would refuse every legitimate check-job delete and leave a retired check job alive forever (review B2, design §4.2(c)). Task 5.
8. **`RepoSpec.Addresses()` silently drifts from the Terraform modules.** It is a hand-maintained mirror, so a module gaining a resource makes `Addresses()` under-report, and `Retirements` would then call a live address retired (or miss one). Pinned by a golden test comparing `Addresses()` to the `resource_changes` of a **real** plan of the real `repo` root (`-tags terraform`, `real_terraform_test.go`'s pattern and `-update` flag), so the mirror fails loudly when a module changes (Task 1).

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/infra/spec.go`, `internal/infra/spec_test.go` | `RepoSpec.Addresses()` — every address the spec will produce, built like `imports.go`'s `importTable`; the variant-key assertions; the completeness check against the committed real-plan golden | 1 |
| `internal/infra/real_addresses_test.go` (new, `//go:build terraform`), `internal/infra/testdata/repo_root_addresses.golden` (new), `internal/infra/testdata/repo_root.tfvars.json` (new), `internal/infra/testdata/repo_root_vertex_addresses.golden` (new), `internal/infra/testdata/repo_root_vertex.tfvars.json` (new) | the generator that plans the real `repo` root in a temp copy and rewrites the goldens with `-update` — **build-time only: it reaches the network only to download the provider binary from the registry, uses no project credentials, applies nothing and is never run in CI** | 1 |
| `internal/infra/retire.go` (new), `internal/infra/retire_test.go` (new), `internal/infra/testdata/state_origin.json` (new) | `Retirement`, `Retirements(state, spec)`, `FillFromState(state, retirements)`, the per-row cause/kept text, `Removals`, `RetireAllowDelete(plan, retirements)` | 2, 3 |
| `internal/infra/retire.go`, `internal/infra/retire_test.go` (the `Orphans` tests) | `Orphans(ctx, c, spec) (retirements, notes, error)` — the retirement set's second source of truth, read from the labels (design §3.1a): two job rules (workflow job by `fugaro_workflow`, check job by `fugaro_role=check` **and** `spec.Check == nil`) and a `secrets.list` label filter whose already-released containers come back as notes, never as retirements — and `RetireSet`, the union the callers use | 4 |
| `internal/infra/tf/faketerraform/main.go` (+ a new `main_test.go`), `internal/gcpfake/run.go` (+ `run_test.go`), `internal/gcpfake/secrets.go` (+ a new `secrets_test.go`) | **the fake infrastructure this feature needs and the repository does not have yet**: per-call sequencing and per-subcommand answers in `faketerraform` (so `show -json` and `show <plan>` differ and a second call can answer differently), a job DELETE handler + label rendering in `listJobs` + call recording in `gcpfake/run.go`, delete/destroy counters in `gcpfake/secrets.go` | 4 |
| `internal/cli/init.go`, `internal/cli/init_retire.go` (new), `internal/cli/init_retire_test.go` (new) | `--retire` flag (accepting `--allow-delete`, intersected), the `⚠ RETIRE` banner, the **`askTyped`** confirmation naming every secret left behind, the state-rm-then-plan-then-apply flow inside `planRepo` (`init.go:2297`), the orphaned-job delete through the Cloud Run API with the label check and the operation wait | 5 |
| `internal/cli/init_retire.go`, `internal/cli/init.go` (the `t.Plan` failure path at `:2326`), `internal/cli/testdata/init_retire.golden` (new), `internal/cli/testdata/prevent_destroy_wrapped.txt` (new), `internal/infra/tf/real_terraform_test.go` (build-tagged) | the whitespace-normalized `prevent_destroy` re-framing, the origin transcript and the real wrapped error as golden fixtures, the `--json` `retire` field (asserted in `init_retire_test.go`; **no edit to `internal/cli/init_output_test.go`**) | 6 |
| `internal/cli/init_retire_test.go`, `internal/infra/retire_test.go`, `internal/cli/init_repo_stage_test.go` | the round-trip, the reverse transition, the half-failure and resume cases (including resume **by label**, without the state), the secret-safety assertion over `gcpfake` — `init_repo_stage_test.go` is the existing staging/`state rm` test file the round-trip extends, so the list names it rather than inventing a new one | 5, 6 |
| `docs/gcp-setup.md`, `docs/design/v1.md`, `docs/design/config-transitions.md`, `plugin/skills/{setup,upgrade}/SKILL.md`, `docs/gcp-live-checklist.md`, `docs/release.md` | the docs section, the cross-references, the skills' refusal text, Check 42's row and section, the release bullet — **including the correction of `docs/gcp-setup.md:551`'s stale `removed { from = … }` advice** | 7 |

No task edits `internal/infra/tf/tf.go`: `StateRm` (`:302`) already exists and is already correct for this use, and the `prevent_destroy` re-framing is in `internal/cli` (Task 6). No task creates `internal/infra/removed.go`: the `removed`-block variant is an alternative in design §4.2(a), adopted only if Check 42 proves it, and it would be a follow-up task, not one of these.

## PR group

One PR, Tasks 1–7, released together as 0.7.2 (P7). **Task 8 is the owner-run live check and is not part of it**; Task 9 is the repository's full suite and the `fugaro verify test` run that closes every task.

---

### Task 1: `RepoSpec.Addresses()` — every address the spec produces

**Files:**
- Edit: `internal/infra/spec.go`
- Test: `internal/infra/spec_test.go` (the CI-visible golden diff, **untagged**)
- Create: `internal/infra/real_addresses_test.go` (build-tagged **and gated on `-update`**: CI runs `go test -tags terraform ./internal/infra/...`, `.github/workflows/ci.yml:115`, so without the gate it would download a provider there), `internal/infra/testdata/repo_root_addresses.golden`, `internal/infra/testdata/repo_root.tfvars.json`, `internal/infra/testdata/repo_root_vertex_addresses.golden`, `internal/infra/testdata/repo_root_vertex.tfvars.json`

**Interfaces:**
```go
// package infra
// Addresses are the Terraform addresses spec produces, in the repository
// root. Deterministic from the spec alone: the same spec always yields the
// same set, which is what makes Retirements a set difference.
func (rs RepoSpec) Addresses() []string
```
Built from the same facts `imports.go`'s `importTable` already encodes (`imports.go:83-105`): `module.repo.google_secret_manager_secret.this[<logical>]` per `rs.Secrets` key; the operator grants; the registry and its grants; the build account and its grants; the check job, invoker grant and Scheduler job when `rs.Check != nil`; and, per `rs.Workflows` key, `module.repo.module.workflow[<key>]`'s job, service account, operator grants, bucket grant, secret accessors, vertex grant and launcher grants. Keys are quoted with `strconv.Quote`, as `newImport` does. Sorted.

**`Addresses()` is a hand-maintained mirror of the Terraform modules, so it is pinned, not trusted** (Review Focus 8): `internal/infra/testdata/repo_root_addresses.golden` holds the sorted address list of a **real** `terraform plan` of `deploy/terraform/gcp/roots/repo`, planned from `internal/infra/testdata/repo_root.tfvars.json` — a spec that turns everything on (one workflow with `checkout: baked`, `rebuild.check: on`, `agent.auth: oauth` with one named account, one workflow-only secret and one provider key). The golden is generated by the build-tagged `real_addresses_test.go`, which follows `internal/infra/tf/real_terraform_test.go`'s pattern (`//go:build terraform`, a `-update` flag, a fresh `HOME`) — **and, unlike that file, whose fixture needs neither a download nor a credential, this one must be skipped outright without `-update`, because CI's `-tags terraform ./internal/infra/...` run would otherwise have to reach the registry** and reads the addresses out of `resource_changes` (which excludes data sources, and excludes zero-key `for_each`/`count` instances — so equality is the right assertion). **No filtering is needed beyond that, and that is a checked fact rather than an assumption** (review D9): neither `deploy/terraform/gcp/roots/repo` (whose single `module "repo"` is at `main.tf:28`) nor `modules/repo` nor `modules/workflow` contains a **`data` block** at all — the only one in the tree is `modules/installation/budget.tf:3`, and the installation module is not part of this root — so there is no data-source address that could slip into the golden through a nested module. A later `data` block added under this root would appear in `resource_changes` as a `read` action, not as an address in `Addresses()`, and the golden diff is what would catch it.

**There is a second fixture, because one spec cannot cover the address set.** `google_project_iam_member.vertex[0]` exists only when the workflow's `agent.auth` is `vertex`, which is **mutually exclusive with `oauth`** — so the `oauth` fixture above can never pin it, and an `Addresses()` that forgot vertex would still match that golden. `internal/infra/testdata/repo_root_vertex.tfvars.json` is the same spec with `agent.auth: vertex` and no named account, and `internal/infra/testdata/repo_root_vertex_addresses.golden` is its real-plan address list; the same build-tagged generator writes both, and the CI test asserts `Addresses()` against each in turn. The two fixtures together are what make the mirror complete: every branch of `agent.auth` that changes the address set is planned at least once.

**This generator is the one step in this plan that reaches outside the repository, and it runs at build time on the owner's machine — and, since CI does reach its file, only when `-update` asks for it.** `.github/workflows/ci.yml:115` runs `go test -tags terraform ./internal/infra/...`, so `internal/infra/real_addresses_test.go` is **compiled and run in CI**; the generator therefore **returns early (`t.Skip`) unless `-update` is set**, and every network step — `terraform init`, the provider registry download — sits behind that skip. CI compiles and vets it (so it cannot rot) without a download and without a credential, which is what keeps Global Constraints' "no live cloud or network call in any automated test" true by construction rather than by where the file happens to live. When it does run (`-update`, on the owner's machine) its only network use is the **provider registry download** of the google provider binary; it carries **no project credentials**, calls no Google API and applies nothing. **The fixture it plans must be import-free, and that is a deliberate part of the fixture, not an accident of it** (review D5): Terraform reads `import` blocks at **plan** time and an importer may need credentials for the resource it adopts (`internal/infra/imports.go` writes exactly such a block into `imports.tf.json`), so the temp copy the generator plans contains **no** `imports.tf.json` — the fixture is the address set, and an import block would make the golden depend on a credential the generator is specified not to carry. If the plan turns out to need one anyway, the fallback in the next paragraph (read-only credentials for a sandbox project, plan only) covers it, and the golden's own comment records which of the two produced it. It copies the root into a temp dir and plans it there (the committed root is not edited; the root's own comment says it reads no remote state, because `init --repo` writes the installation's outputs into its tfvars), and it writes a generator-owned `provider_override.tf` in that copy setting the google provider's `skip_credentials_validation`, `skip_requesting_account_id` and `skip_provider_registry`, so the plan needs the provider binary and no credentials. **Whether that override is enough for this root to plan with no credentials is something to verify at build time** — it could not be verified offline. If it is not enough, the fallback is the same generator run with read-only credentials for a sandbox project (plan only; nothing is ever applied), and either way the committed golden is the only artifact that leaves that machine. Until the golden exists, `TestRepoAddressesMatchTheRealPlanGolden` fails with the generator command in its message rather than skipping silently.

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
func TestRepoAddressesMatchTheRealPlanGolden(t *testing.T) {
    // Review Focus 8: Addresses() for the golden's spec == every line of
    // internal/infra/testdata/repo_root_addresses.golden, in order. On
    // mismatch, print the generator command from real_addresses_test.go.
}
func TestRepoAddressesMatchTheVertexGolden(t *testing.T) {
    // the same assertion against repo_root_vertex_addresses.golden, so
    // google_project_iam_member.vertex[0] cannot be missing from Addresses()
    // without a failure: it is the one address the oauth fixture can never
    // contain (agent.auth: vertex and oauth exclude one another).
}
func TestRepoAddressesVertexIsNotUnderOAuth(t *testing.T) {
    // the negative half: the oauth spec's Addresses() contains no
    // google_project_iam_member.vertex address and no named variants.
}
```
Run: `go test ./internal/infra -run 'TestRepoAddresses' -count=1`
Expected: **FAIL** — `rs.Addresses undefined` (and, once it exists, the golden test fails until the golden is generated).

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.
- [ ] **Step 3: Generate both goldens at build time** (the one step in this plan that reaches outside the repository: the network use is the provider registry download only, there are no project credentials, it plans a temp copy of the root and applies nothing): `go test -tags terraform ./internal/infra -run 'TestRealRepoRootAddresses' -update -count=1`, then commit `internal/infra/testdata/repo_root_addresses.golden` and `internal/infra/testdata/repo_root_vertex_addresses.golden`. Confirm each golden has one line per address its spec's modules declare and **no** data-source or provider address, and that the vertex golden's list differs from the oauth one exactly by the vertex grant and the named-account variants, and record in the test's comment whether the credential-skipping provider override was enough.

    Run the same command **without** `-update` once and record it: `go test -tags terraform ./internal/infra -run 'TestRealRepoRootAddresses' -count=1` must be `ok` with the generator **skipped**, not run. That is the CI case (`.github/workflows/ci.yml:115` runs `go test -tags terraform ./internal/infra/...`), and it is the assertion that keeps this step the only one in the plan that touches the network: on a machine with no network and no provider cache the tagged, un-gated run still passes because the generator returns before it plans anything. A failure here means the skip guard is missing or is checked after the temp root is built.
- [ ] **Step 4: `gofmt -l .`** Expected: no output.
- [ ] **Step 5: Commit.**

---

### Task 2: `Retirements` — the set difference and its plain-words explanation

**Files:**
- Create: `internal/infra/retire.go`, `internal/infra/retire_test.go`, `internal/infra/testdata/state_origin.json`

**Interfaces:**
```go
// package infra
type Retirement struct {
    Address string
    Kind    string // "secret container", "workflow job", "check job", "Scheduler job", …
    Cause   string // the config change, in the user's words
    Kept    string // what survives, or "" when nothing does
    Logical  string // a secret container's logical name, "" otherwise
    SecretID string // its Secret Manager ID, "" otherwise
    Job      string // a job's Cloud Run job name, "" otherwise
    Region   string // its region, "" otherwise

    // Protected is whether an ordinary apply can delete this address at all,
    // which is what Removals (Task 3) keys on: true for a secret container
    // (its module carries prevent_destroy), and for a job whose state says
    // deletion_protection = true. It is NOT implied by the address alone -
    // a job whose protection is already false is one the apply deletes, and
    // state-removing it would leave it alive in GCP with nothing managing it.
    Protected bool
}
// FillFromState fills Logical, SecretID, Job, Region and Protected for every
// Retirement from the state's own resource attribute values. A field that
// cannot be read is left empty, never guessed.
func FillFromState(st *tf.State, rets []Retirement)
```
`Retirements` takes `state []string` (the addresses from `TF.ShowState` → `State.Addresses()`, `tf.go:219`) rather than a `*tf.State`, so it stays a pure set difference testable without a Terraform binary. The four optional fields cannot come from `spec` — a retired address by definition is not in the spec any more — so they come from the state's **own attribute values**, read by `FillFromState`, which has its home here in `internal/infra/retire.go` and **needs no change to `internal/infra/tf`**: `tf.State` keeps each resource as raw JSON (`StateModule.Resources []json.RawMessage`, `tf.go:199`), which is exported, so this package decodes `address` and `values` itself (`internal/infra` already imports `internal/infra/tf` for `RetireAllowDelete`'s `*tf.Plan`). The attribute names are the provider schema's:

| Field | Source (the state's own `values` for that address) | Empty when |
|---|---|---|
| `Logical` | the container address's `for_each` key, which is the logical secret name | not a `google_secret_manager_secret.this` address |
| `SecretID` | the container's **`secret_id`** attribute — never re-derived from the logical name, and never `values.id` (that is the full resource ID, a different string) | the state does not carry it |
| `Job` | the job's **`name`** attribute | not a job address |
| `Region` | the job's **`location`** attribute — where the job actually is, which is what the delete must name, not the spec's `region` (the two differ for a job moved between regions) | the state does not carry it |
| `Protected` | the job's **`deletion_protection`** attribute, read as `true`/`false`; for a `google_secret_manager_secret.this` address it is `true` by the address's **kind** (the module's `prevent_destroy` is not a state attribute) | for a job, when the state's `values` cannot be read — **and then it counts as protected**, the conservative direction (Task 3) |

**A field that cannot be read is left empty, never guessed** — the typed confirmation then names the logical name and the address instead of an ID, and says so, and the delete falls back to the spec's `region` and says which it used. **`Protected` is the one field whose empty value is not "unknown but harmless":** a job whose `values` cannot be read is treated as protected, so `Removals` takes it out of the state and the §4.2(c) API delete finishes it — the API delete works whatever the flag says, so the conservative reading can never leave a resource alive, while the optimistic one can. The *gate* (§3.3) is the other way round and only refuses on a value it actually read as `true`. `Retirements` itself leaves every optional field at its zero value. Check 42's live run is what confirms the attribute names against a real state (§6).

`Cause` is derived, not guessed: a `module.repo.module.workflow["<wf>_<acct>"]` address whose `<wf>` is still in `spec.Workflows` under another key is "the named Claude account <acct> is no longer a variant this repository deploys"; one whose `<wf>` is gone entirely is "workflow <wf> was removed from fugaro.yaml". A secret container whose logical name is a Claude account secret is "agent.auth no longer mounts <logical>"; one that is a workflow-only secret is "no workflow mounts <logical> any more". `Kept` is non-empty for every `google_secret_manager_secret.*`: "the secret and its value stay in Secret Manager; its container carries the fugaro labels, so re-adding it re-adopts it".

**The origin fixture, enumerated once** (the design's §1 list; the fixture Task 2 asserts, Task 4's `Orphans` test reuses, and Task 6's golden transcript prints — **14 retired addresses**, of which **12 go away** and **2 are removed from state and kept**):

1. `module.repo.module.workflow["go_bmb"].google_cloud_run_v2_job.this[0]`
2. `module.repo.module.workflow["go_bmb"].google_service_account.job`
3. `module.repo.module.workflow["go_bmb"].google_service_account_iam_member.operator[<the one operator>]` — **one per operator** (`modules/workflow/sa.tf:12`, `for_each = toset(var.operators)`), so the retired variant grants it too; with this installation's single operator that is one address, and this is the address the design's §2 list names and the first draft of this fixture dropped
4. `module.repo.module.workflow["go_bmb"].google_cloud_run_v2_job_iam_member.launcher[<launcher SA>]`
5. `module.repo.module.workflow["go_bmb"].google_secret_manager_secret_iam_member.job["claude-oauth-token-bmb"]`
6. `module.repo.module.workflow["go_bmb"].google_secret_manager_secret_iam_member.job["<the git credential>"]`
7. `module.repo.module.workflow["go_bmb"].google_storage_bucket_iam_member.job`
8. `module.repo.module.workflow["go"].google_secret_manager_secret_iam_member.job["claude-oauth-token"]` — the *surviving* default variant loses its accessor to the OAuth secret
9. `module.repo.google_secret_manager_secret.this["claude-oauth-token"]`
10. `module.repo.google_secret_manager_secret.this["claude-oauth-token-bmb"]`
11. the two OAuth containers' operator grants, 2 each (4 addresses: `secretVersionAdder` and `viewer` per container)

**Counting it: the retired variant is rows 1–7, which is 7 addresses** (the job, the account, the operator grant, the launcher grant, two secret accessors, the bucket grant — the design's §2 "what one workflow variant owns", minus `google_project_iam_member.vertex[0]`, which an `oauth` variant does not have); rows 8 and 11 are the 5 bindings tied to the two OAuth secrets; rows 9 and 10 are the containers. **7 + 5 = 12 go away, + 2 kept containers = 14 retired**, and that is the same 12 the design's §1 enumeration lists (seven, plus the surviving variant's one accessor, plus four operator grants). The owner's own corroboration is in §1 step 3: they passed **11** addresses to `--allow-delete`, the job already removed from state — 12 going away minus the 1 the API deletes.

**The retirement set this task produces is not the set the state removal consumes.** All 14 addresses here are *retired*; only the protected 3 are *removed from state before the plan* (Task 3's `Removals`, design §4.2(a)) — the other 11 stay in state and are deleted by the apply.

Rows 9 and 10 are the two **containers** — the only addresses whose retirement is a *kept* secret (Review Focus 2). Rows 1–8 and 11 are grants, jobs and service accounts: nothing kept.

**The counts of design §1, read off this fixture — write them into the test, not just the prose.** Of the 14 retired addresses: **12 go away** (rows 1–8 and 11; **11** of them deleted by the apply and row 1, the job, deleted through the Cloud Run API) and **2 are removed from state and kept** (rows 9 and 10); and **3 are state-removed before the plan** — rows 9, 10 and row 1, the orphaned job. `14 = 12 + 2` and `12 = 11 + 1`.

**The `Kind`/`Cause`/`Kept` strings, per inventory row, because the golden transcript prints them and Task 6's fixture asserts them** (review D7). These are the strings this task's `Retirements` must produce; the design's §2 row letters are in the left column (**review round 6: the letters in the first draft of this table named the wrong rows** — `agent.auth` oauth→api-key is **A**, not B; removing a workflow from `fugaro.yaml` entirely is **C**, not D, because D is removing one `secrets:` entry; a secret container no longer mounted falls out of **A, C or D** depending on which edit unmounted it; **E** is `baked`↔`clone` and **F** is a provider or named account dropped from the local config) and the task's own fixture rows in the right, and a row whose cause the implementation cannot derive is a bug in this task, not a golden to edit:

| Design §2 row | Fixture rows | `Kind` | `Cause` | `Kept` |
|---|---|---|---|---|
| **A** — `agent.auth` oauth→api-key, named account dropped | 1–7 (the `go_bmb` variant) | `"workflow job"`, `"service account"`, `"IAM member"`, `"secret accessor"`, `"bucket grant"` — one per address kind | `the named Claude account <acct> is no longer a variant this repository deploys` | `""` for every row 1–7: an accessor to a kept secret is still a grant that goes away, and only the **container** is kept |
| **A** — the surviving variant's accessor to a now-unmounted secret | 8 | `"secret accessor"` | `agent.auth no longer mounts <logical>` | `""` |
| **C** — a workflow removed from `fugaro.yaml` entirely | (the `"<wf> was removed from fugaro.yaml"` cause; same shape as rows 1–7 with `<wf>` gone) | as A | `workflow <wf> was removed from fugaro.yaml` | `""` |
| **E** — the last checked workflow leaves (`baked`→`clone`) | the check job, its invoker grant, its Scheduler job | `"check job"`, `"IAM member"`, `"Scheduler job"` | `no workflow is checked any more (rebuild.check: off everywhere)` | `""` |
| **A/C/D** — a secret container no longer mounted (whichever edit unmounted it) | 9, 10 | `"secret container"` | `agent.auth no longer mounts <logical>` (a Claude-account secret) or `no workflow mounts <logical> any more` (a workflow-only secret) | `the secret and its value stay in Secret Manager; its container carries the fugaro labels, so re-adding it re-adopts it` |

The **check job** row is the one the design's §3.1a gives its own retirement rule (`spec.Check == nil`), and the row-E `Cause` above is written for a whole-repository transition; a *per-variant* last-check case (one workflow stops being checked while others still are) must say which workflow left, and the test asserts the string it produces rather than a generic one.

- [ ] **Step 1: Write the failing tests**

```go
// internal/infra/retire_test.go
func TestRetirementsOriginTransition(t *testing.T) {
    // The real 2026-10-14 case: a state holding the two OAuth containers and
    // the go_bmb variant's addresses, and an api-key spec. Assert that all 14
    // addresses listed above are returned — the 12 going away (the seven of
    // the retired variant, including its google_service_account_iam_member
    // .operator[...], plus the surviving variant's accessor plus the four
    // operator grants) and the 2 kept containers — that the counts themselves
    // are 14 / 12 / 2 (so design §1's table cannot drift from the fixture),
    // that both containers carry a non-empty Kept and that no accessor does,
    // and that nothing the spec still produces is returned.
}
func TestRetirementsBakedCloneCheckJob(t *testing.T)  // design row E, both directions, for a check job the STATE still holds - the label half (a check job the state has lost) is Task 4's Orphans pair
func TestRetirementsReverseAdoptsCleanly(t *testing.T) // api-key -> oauth: zero secret containers retire
func TestRetirementsProviderDroppedIsTheUnionCase(t *testing.T) // P6: pins that this design adds no mechanism
func TestFillFromStateReadsTheProvidersOwnAttributes(t *testing.T) {
    // the fixture is a state JSON (internal/infra/testdata/state_origin.json)
    // built from the provider schema: the container's secret_id becomes
    // SecretID, the job's name and location become Job and Region, and the
    // for_each key becomes Logical. A resource with no `values`, a renamed
    // attribute and an address that is not in the state each leave the field
    // empty rather than guessing.
}
```
`TestRetirementsProviderDroppedIsTheUnionCase` is the row-F pin, and it names #329's `Existing.ExtraSecrets`, which is **not on `main`** — write it against `DiscoverRepo`'s current output and note the dependency in the test comment; it is tightened when #329 lands (P6, **Depends on**).

Run: `go test ./internal/infra -run 'TestRetirements|TestFillFromState' -count=1`
Expected: **FAIL** — `Retirements undefined`, `FillFromState undefined`.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.
- [ ] **Step 3: `gofmt -l .`** Expected: no output.
- [ ] **Step 4: Commit.**

---

### Task 3: The removal set and the computed allow-list

**Files:**
- Edit: `internal/infra/retire.go`, `internal/infra/retire_test.go`

**Interfaces:**
```go
// package infra
// Removals is the addresses in rets that must leave Terraform's state
// BEFORE the plan, sorted — what a retirement passes to (*TF).StateRm.
// It is NOT the whole retirement set, and that is the point: only the
// addresses an ordinary apply CANNOT delete qualify. Both halves are
// per-address facts:
//   - still in stateAddrs, so a rerun never passes an address that is gone
//     (terraform state rm of one fails with "No matching objects found").
//   - Protected (Retirement.Protected, Task 2), which is true for
//       - google_secret_manager_secret.this[*]: prevent_destroy
//         (modules/repo/secrets.tf:22), so the plan aborts before it lists
//         anything;
//       - a Cloud Run job whose state says deletion_protection = true
//         (modules/workflow/job.tf:15, modules/repo/check.tf:27):
//         deletion_protection is !var.allow_job_delete, a tfvars variable
//         that cannot be lowered once the resource has left the
//         configuration.
// A retired job whose state says deletion_protection = false is NOT
// returned: it is exactly the job an apply must delete (the user who
// followed gcp-setup.md's order, design 3.3), and taking its address out
// here would leave it alive in GCP with nothing managing it. The same goes
// for everything else - a retired variant's service account, its IAM
// members, its bucket grant - which stays IN state so the plan deletes it.
func Removals(stateAddrs []string, rets []Retirement) []string

// RetireAllowDelete is the guard's allow-list for p: each delete or replace
// of an address in rets (the union of §3.1a), and nothing else.
func RetireAllowDelete(p *tf.Plan, rets []Retirement) []string
```
**The resume path has two defences and this task says which is which, because P3's prose ("a second `state rm` … fails with \"No matching objects found\", so the resume path treats that as done") and `Removals`'s filter say the same thing two ways** (review D2). **`Removals`'s filter is the mechanism**: it returns only protected addresses *still in the state*, so an address an earlier run already removed is never passed to `state rm` again, and the resume path does not depend on parsing Terraform's error text. **Tolerating the error is the second line of defence, not the first**: if an address disappears from the state between `ShowState` and the `state rm` call (a hand-run `terraform state rm` in another terminal, or a partial earlier failure), `(*TF).StateRm` reports "No matching objects found" — the runner's existing behaviour is to fail, so this task's caller catches exactly that text, reports the address as **already removed**, and continues with the rest. The filter is what keeps the common path clean; the tolerance is what keeps a race from turning into a broken run. Neither replaces the other, and the test for each is separate (`TestRemovalsSkipsWhatIsAlreadyGone` for the filter, `TestStateRmToleratesAnAddressThatWentAwayBetweenShowStateAndTheCall` for the tolerance), so removing the filter cannot pass on the strength of the tolerance.

**`rets` in this task is the retirement set of design §3.1a, not `Retirements` alone** — the deduplicated union `Orphans ∪ Retirements(state, spec)`, which is what the caller in Task 5 hands both functions. `Orphans` itself, and the fake work its tests need, is **Task 4**; it is split out because it is the one piece that cannot be tested against the fakes as they stand.

**The mechanic is `(*TF).StateRm` (`tf.go:302`), unchanged and unextended** (P3): `t.StateRm(ctx, infra.Removals(state, rets)...)` — the same call `init.go` already makes at `:1751` and `:2588`, taking exact instance addresses, destroying nothing. **`Removals` is narrow on purpose: it returns only the protected addresses (design §4.2(a)), not the retirement set — and "protected" is `Retirement.Protected`, read by `FillFromState` from the state's own `deletion_protection`, never inferred from a job's address.** A `Removals` that keyed on the address kind alone would state-remove a job the user had already unprotected — the documented `gcp-setup.md` order — and orphan it; that is the case `TestRemovalsKeepsAnUnprotectedJobForTheApply` pins. In the origin that is 3 of the 14 — the two `prevent_destroy` secret containers and the orphaned Cloud Run job — and the other 11 stay in state so the plan deletes them, which is what makes the origin's plan 11 deletes rather than zero. A `Removals` that returned the whole set would be the worst kind of bug: the plan succeeds, prints a clean run, and leaves a service account and its IAM grants running in GCP with nothing managing them. It is per-address, so a failure part-way leaves the earlier addresses removed and the later ones not, which is exactly the state Task 6's half-failure test asserts and the resume path keys on (`Removals` returns only protected addresses still in state). `StateRm` already refuses `""` and any address starting with `-`, so no user or config string can turn a removal into a flag.

**No `removed` block is generated and no `removed.tf.json` is written** — so no `forget` plan action is introduced, `tf.Guard`'s `knownActions` needs no change, and the workdir needs no new file (`PrepareWorkdir` `RemoveAll`s and rewrites `dir/gcp`, so a hand-written file would not survive anyway). **The `removed { from = … lifecycle { destroy = false } }` variant stays documented in design §4.2(a) as an alternative and is adopted only if Check 42 proves it** (Task 8, hard precondition FACT); the repository's only evidence for it is a bare whole-resource address (`removed { from = terraform_data.d }`, `real_terraform_test.go:58-63`, golden `internal/infra/tf/testdata/plan_forget.json`), and whether `removed.from` accepts an instance-key address of a resource or module that is still declared in the configuration is **unverified offline** — so it is not this plan's mechanic.

**Flagged for opus review (Terraform-sensitive):** this is the code that decides what leaves Terraform's state, and a wrong address set is a wrong apply.

- [ ] **Step 1: Write the failing tests**

```go
// internal/infra/retire_test.go
func TestRemovalsSkipsWhatIsAlreadyGone(t *testing.T)     // resume: never passes an already-removed address again
func TestRemovalsKeepsTheAddressesTheApplyMustDelete(t *testing.T) {
    // the origin's 14 retired addresses in: Removals returns exactly the
    // protected 3 (the 2 containers and the job). The retired variant's
    // service account, its operator grant, its launcher/secret/bucket IAM
    // members and the OAuth operator grants are NOT returned - the plan
    // deletes those 11, and removing them here would leave them alive in
    // GCP (design 4.2(a)).
}
func TestRemovalsReturnsEveryProtectedAddressStillInState(t *testing.T) {
    // the other direction: every retired google_secret_manager_secret.this
    // address still in state IS returned (prevent_destroy), and so is every
    // retired job whose state says deletion_protection = true - because
    // neither can be deleted by an apply.
}
func TestRemovalsKeepsAnUnprotectedJobForTheApply(t *testing.T) {
    // review finding: the invariant is per-address. A retired job whose
    // state says deletion_protection = false, still in state, is NOT
    // returned - the apply deletes it, and state-removing it would leave it
    // alive and unmanaged. Two Retreatments differing only in
    // Protected must give different answers.
}
func TestRemovalsIsSortedAndNeverTakesAFlagLookingAddress(t *testing.T)
func TestStateRmToleratesAnAddressThatWentAwayBetweenShowStateAndTheCall(t *testing.T) {
    // review D2: the second-line defence. Removals filters, but a race (a hand-run
    // state rm, a partial earlier failure) can still make one address vanish
    // between ShowState and the call - the runner reports "No matching
    // objects found", the caller reports that address as already removed and
    // finishes the rest, and the run is not turned into a failure by it.
}
func TestRetireAllowDeleteNeverAllowsAnUnnamedAddress(t *testing.T) // Review Focus 1
func TestRetireAllowDeleteAllowsTheReplacesTheTransitionNeeds(t *testing.T) // and nothing else
```
Run: `go test ./internal/infra -run 'TestRemovals|TestRetireAllowDelete' -count=1`
Expected: **FAIL** — undefined.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.
- [ ] **Step 3: `gofmt -l .`** Expected: no output.
- [ ] **Step 4: Commit.**

---

### Task 4: The orphan set read from labels, and the fake support it needs

**Files:**
- Edit: `internal/infra/retire.go` (add `Orphans` and `RetireSet`), `internal/infra/retire_test.go`
- Edit: `internal/infra/tf/faketerraform/main.go` and **create** `internal/infra/tf/faketerraform/main_test.go` (the package has no test file today; its behaviour is only exercised indirectly through `internal/cli/init_test.go`)
- Edit: `internal/gcpfake/run.go` (+ `internal/gcpfake/run_test.go`, which exists) and `internal/gcpfake/secrets.go` (+ **create** `internal/gcpfake/secrets_test.go`)

**Why this is a task of its own.** Design §3.1a makes the labels the second source of truth for the retirement set, and §6 lists the three pieces of **test infrastructure that do not exist in this repository yet**. They are not an implementation detail of some later step: without them the resume path, the delete, the 404, the ownership refusal and the never-delete-a-secret invariant are all untestable, and an untestable invariant is the thing this design refuses. So the extensions are planned here, reviewed here and committed here.

**Interfaces:**
```go
// package infra
// Orphans are the cloud resources the labels say are ours that spec no longer
// produces, in two groups:
//   - Retirements: a workflow job whose fugaro_workflow is no longer a
//     variant key, and the check job (fugaro_role=check) when spec.Check is
//     nil. Each has its own rule because the two job kinds carry different
//     labels: a workflow job has no fugaro_role and a check job has no
//     fugaro_workflow at all (modules/workflow/job.tf:11-14,
//     modules/repo/check.tf:22-26).
//   - Notes: a labelled secret container whose logical name is no longer
//     mounted and which the state no longer holds. Informational only:
//     RetireSet never counts one as a retirement, so it cannot make the
//     summary, the gate or --retire fire on their own. Without this split
//     every later `fugaro init --repo` would re-report the two OAuth tokens
//     forever, because a kept container is forgotten by design.
// Read-only: one jobs.list, one secrets.list with a label filter, and one
// describe per job - the same *Clients and the same cloud-platform scope the
// repository root already uses (NewClients, discover.go:100). Runs under
// --plan-only too.
func Orphans(ctx context.Context, c *Clients, spec RepoSpec) (retirements []Retirement, notes []Retirement, err error)

// The retirement set, deduplicated by address, Orphans winning on the fields
// it fills (Job, Region, SecretID, Logical) because it read them live. It
// takes Orphans' *retirements* only, never its notes.
func RetireSet(orphans, retired []Retirement) []Retirement
```
**What must change in the fakes, and why each change is required by a test in this plan** (design §6):
- **`internal/infra/tf/faketerraform/main.go` is stateless and keyed by subcommand alone** — one scripted response per key (`FAKE_TERRAFORM_SCRIPT`), with `<subcommand>@<root>` variants, so `show -json` (the state) and `show <plan>` are indistinguishable, `state rm` is all-or-nothing, and no second call can answer differently from the first. It gains **sequenced answers** (a script entry that applies to invocation *n*, or a list of responses consumed in order) and **a way to distinguish the two `show` forms** (key on `show -json` vs `show`). Tests that need it: state before vs after a removal, a `state rm` that fails on one address and not another, a plan that fails and then succeeds on the rerun, "the removal was recorded before the plan" (Tasks 5, 6).
- **`internal/gcpfake/run.go` has no job DELETE handler**, and `listJobs` returns `{"name": parent + "/jobs/" + n}` — **names only, no labels** — so neither the delete, nor the 404, nor the `fugaro_workflow` ownership refusal, nor `Orphans`' discovery by label can be tested. It gains a `DELETE /jobs/<name>` handler (404 for a job that is not there), labels in the `listJobs` response, and the operation + `jobs.delete` **call recording** in the shape `Run.RunRequests()`/`Run.Patches()` already use.
- **`internal/gcpfake/secrets.go` has a delete handler but records no calls**, so "Fugaro never deletes a secret or a secret version" cannot be asserted. It gains per-method counters the test can read, of the same shape as `Run.RunRequests()`. Its **list** handler already honours a `filter` query parameter, which is what `Orphans`' `secrets.list` label filter is asserted against; the counters and the label filter are separate additions and each has its own failing test.

**`Orphans` reads, in this order:** `jobs.list` for the project and region, keep every job carrying `fugaro=managed` **and** `fugaro_repo=<this repository's label>`, then split it by kind — a job with **`fugaro_workflow`** is an orphan when that value is **not** a key of `spec.Workflows` (the repo label alone is shared by every variant, so a job of a *surviving* variant is never an orphan), and a job with **`fugaro_role=check`** is an orphan only when `spec.Check == nil` (`rs.Check` is set only when at least one workflow is checked, `spec.go:816-823`). **A rule that asked every job for `fugaro_workflow` would get the check job wrong in both directions** (review B2, design §3.1a): the check job carries `fugaro_role=check` and no `fugaro_workflow` (`modules/repo/check.tf:22-26`), so the workflow rule reads it as an orphan on **every** run while the check is still wanted, and a genuinely retired check job — design row E, `baked`→`clone` — would be state-removed by `Removals` but **never deleted**, because the delete's own label check would refuse it for a missing `fugaro_workflow`. State and cloud would then disagree silently, which is exactly what design §6 forbids. Task 5's `deleteOrphanedJob` takes the same split as an expected-label map, so the two rules cannot drift apart.

Then the secret containers. **This needs a call that does not exist yet: a `secrets.list` with a label filter** (`fugaro=managed`, `fugaro_repo=<label>`), keeping the containers whose `fugaro_secret` logical name is no longer in `rs.Secrets`. **The permission is not established, and this task must not assume it.** `secretmanager.secrets.list` and `secretmanager.versions.list` are in the installation's **launcher** custom role (`modules/installation/iam.tf:8-20`) — but that role is granted to `var.launchers` only (`iam.tf:58-62`), and the run uses the **operator's own ADC** through `infra.NewClients` (`discover.go:100`), the same client `Orphans`' `jobs.list` and every `Run` lookup already use. Whether an operator's ADC holds `secretmanager.secrets.list`, or `run.jobs.list` at project scope, is **not verified offline**; it is a Check 42 FACT (`§6`, Task 8 step 3) and the degradation below is what happens when it does not hold. **Neither half of this may be built on `DiscoverRepo`'s secret path:** that function does not list by label at all — it `Get`s each logical name in `spec.Secrets` one by one (`discover.go:546-572`) — so it can only find an *expected* secret, which by definition is not what an orphan is. The design's §3.1a was corrected to say so, and this call is new. The gcpfake already supports the filter for its secrets list, so this is testable without a new fake capability.

**A kept container the state no longer holds is a note, not a retirement** (review B3, the convergence rule of design §3.1a). The containers a *current* retirement releases have their addresses in the state while the run starts, so they are retirements with `Kept` text; a container an *earlier* retirement already released is remembered by nothing but its labels, and there is nothing left to do about it — the run tells the user their value is safe and moves on. That is what stops the summary, the gate and the `--retire` next command from recurring on every later `fugaro init --repo` for a project that has nothing left to retire.

**A refused label call degrades loudly, and that is part of this task's contract** (design §3.1a): `Orphans` runs on every `fugaro init --repo`, including under `--plan-only`, so a `PERMISSION_DENIED` from `run.jobs.list` or `secrets.list` is a normal outcome, not an exceptional one — and it must not be treated like the delete's 403 (Task 5), because a read that fails only narrows what the run can see. The rule here: print a warning line naming the call, the permission, the `gcloud` equivalent and the consequence ("orphaned jobs/secrets cannot be checked; anything the state no longer holds will not be named"), **continue with the state-derived set**, and **never let "nothing to retire" and "could not look" read the same** — when a label half failed and the state half is empty, the run says the check could not be made. It is a false-negative risk, never a false positive. `TestOrphansReportsARefusedLabelCallInsteadOfSilence` pins it (a `gcpfake` 403 on each call, asserting the warning, the continued run and the distinct wording), and Task 8's step 3 confirms the permissions on a live project.

Labels come from the live resource, so `Job`, `Region` and (via `secrets.get` for each listed container) `SecretID`/`Logical` are filled without reading the state — which is exactly what makes the set survive the state removal (design §3.1a, §3.2's `KEPT` list).

- [ ] **Step 1: Extend the fakes, with a failing test each.** `internal/infra/tf/faketerraform`: a test that a script can answer the second `show -json` differently from the first and that `show -json` and `show <plan>` are told apart. `internal/gcpfake`: a test that a job created with labels is listed with them, that `DELETE` on a present job is recorded and on an absent job is a 404, and that the Secret Manager delete/destroy counters read zero after a run.
- [ ] **Step 2: Write the `Orphans` tests, run them, watch them fail, then implement.**

```go
// internal/infra/retire_test.go
func TestOrphansReadsTheLabelsNotTheState(t *testing.T) {
    // a job labelled fugaro_repo=<ours> with fugaro_workflow=go_bmb, and an
    // api-key spec: Orphans returns it, with Job/Region from the live job,
    // even though no state mentions it at all (design 3.1a).
}
func TestOrphansNeverReturnsASurvivingVariant(t *testing.T) {
    // the same repo label, fugaro_workflow=<a key of spec.Workflows>: not an
    // orphan, never returned, whatever the state says - the repo label alone
    // is shared by every variant (Review Focus 7).
}
func TestOrphansNeverReturnsTheCheckJobItStillNeeds(t *testing.T) {
    // review B2, one direction: spec.Check != nil, and a job labelled
    // fugaro_repo=<ours> + fugaro_role=check with NO fugaro_workflow. It is
    // not an orphan, is not in retirements, and is never deleted - the job
    // the repository still schedules the daily image check with.
}
func TestOrphansCheckJobIsAnOrphanWhenSpecCheckIsNil(t *testing.T) {
    // review B2, the other direction, and design row E (baked -> clone): the same
    // job, with spec.Check == nil. It IS an orphan, it is in retirements with
    // Kind "check job", and Task 5's delete takes it under fugaro_role=check
    // rather than refusing it for the missing fugaro_workflow.
}
func TestOrphansWorkflowJobRuleIgnoresAFugaroRole(t *testing.T) {
    // the two rules do not bleed: a job with fugaro_workflow=<a variant key>
    // AND a fugaro_role is judged by the workflow label (still wanted -> not
    // an orphan), and a job with neither label is not ours and is ignored.
}
func TestOrphansKeptContainerAloneIsANoteNotARetirement(t *testing.T) {
    // review B3, the convergence rule: a labelled container whose logical name is no
    // longer mounted, with no state holding it (an earlier retirement already
    // released it) comes back in `notes` only. The union RetireSet builds is
    // empty, so the summary is not printed, the gate does not open and
    // --retire is not the next command - the test that stops the recurring
    // summary.
}
func TestOrphansReturnsALabelledContainerNoLongerMounted(t *testing.T) {
    // the same container while its address IS still in the state: a
    // retirement, with Kept text, and it does make the summary print.
}
func TestOrphansListsSecretsByLabelNotByExpectedName(t *testing.T) {
    // review B3: the call carries the fugaro_repo label filter and finds a container
    // that is in no spec.Secrets entry; assert the recorded request's filter,
    // not merely the result, so an implementation that re-used DiscoverRepo's
    // Get-by-logical-name path fails.
}
func TestOrphansReportsARefusedLabelCallInsteadOfSilence(t *testing.T) {
    // review finding: gcpfake answers 403 on jobs.list and on secrets.list.
    // Orphans warns with the call, the permission and the gcloud equivalent,
    // returns the state-derived rows, and (state half empty) says the check
    // could not be made rather than that there is nothing to retire.
}
func TestOrphansIsReadOnlyAndNeedsOneClient(t *testing.T) // same *Clients, cloud-platform scope, runs under --plan-only
func TestRetireSetDeduplicatesAndPrefersTheLiveFields(t *testing.T)
func TestRetireSetNeverCountsANote(t *testing.T)
```
Run: `go test ./internal/infra -run 'TestOrphans|TestRetireSet' -count=1`
Expected: **FAIL** — Step 1's fake tests fail against the unextended fakes, and this run fails on `Orphans undefined` / `RetireSet undefined`.
- [ ] **Step 3: Implement, then run again.** Expected: `ok`.
- [ ] **Step 4: `gofmt -l .`** Expected: no output.
- [ ] **Step 5: Commit** (the fake extensions and `Orphans` may be two commits, fakes first — Tasks 5 and 6 depend on both).

---

### Task 5: `fugaro init --repo --retire` — the guarded flow

**Files:**
- Create: `internal/cli/init_retire.go`, `internal/cli/init_retire_test.go`
- Edit: `internal/cli/init.go` (`planRepo` at `:2297`, the flag block near `:370`/`:380`, the `--repo`-only validation at `:685`, the mutual-exclusion rule at `:709` for `--retire` × `--plan-only`)

**Interfaces:**
```go
// internal/cli — initOptions
retire bool // --retire

// init_retire.go
// retirePlan is the retirement's own discover/read-state/confirm/remove/plan/
// apply, called from planRepo in place of the ordinary plan when Retirements
// is non-empty.
func (r *initRun) retirePlan(ctx context.Context, c *infra.Clients, t *tf.TF, wd *infra.Workdir, spec infra.RepoSpec, rets []infra.Retirement, opts infra.RepoRootOptions) ([]infra.Missing, error)
// printRetirements is the ⚠ RETIRE summary (design §3.2).
func (r *initRun) printRetirements(spec infra.RepoSpec, rets []infra.Retirement) error
// deleteOrphanedJob deletes a job the state removal left unmanaged, after
// checking its labels; it refuses and prints the gcloud command for a job
// that is not ours, treats "not found" as done, and polls the delete's
// long-running operation to a terminal state before reporting success.
func deleteOrphanedJob(ctx context.Context, c *infra.Clients, project, region, job string, want map[string]string) error
// want is the expected label set for this job's kind, never a workflow name
// (review B2, design 4.2(c)): {fugaro: managed, fugaro_repo: <label>,
// fugaro_workflow: <retired variant>} for a workflow job, and
// {fugaro: managed, fugaro_repo: <label>, fugaro_role: check} for the check
// job - which carries no fugaro_workflow at all (check.tf:22-26). Every pair
// in want must match the live job's labels exactly.
```
The set this flow works from is **the retirement set of design §3.1a** — `orphans, notes, err := infra.Orphans(ctx, c, spec)`, then `infra.RetireSet(orphans, infra.Retirements(state, spec))` (Task 4) — not the state's prediction alone: `Orphans`' **retirements** are what still name the orphaned job after a rerun, when the state no longer holds it. Its **notes** are not part of the set (Task 4's convergence rule): they are the kept containers an earlier run already released, and the run prints them as one informational line, so a project with nothing left to retire does not re-open the gate or re-suggest `--retire` on every run.
**The flow runs from one of `planRepo`'s two call sites, and this plan names it** (review D3). `planRepo` already takes a `first bool` (`init.go:2297`), and the run calls it twice: `init.go:2001` with `first = true` — the root's own discover/readiness/plan/guard/confirm/apply — and `:2039`, with `first = false` and only when images were actually built, which re-plans and re-applies the same root so the freshly built images are deployed (`what += ", deploying the images just built"`). **The retirement flow hangs off the `first == true` call**: that is the call whose plan is the transition's plan, and it is the only one that exists in a `--plan-only` run. **The `first == false` call must not re-enter it**, and that is a real hazard rather than a tidiness point — by then the state removal has run (so a re-entry would re-prompt the user for a transition they already confirmed) and the orphan is still alive in the cloud (the API delete is the run's last step, after every apply), so a re-entry could attempt the same delete a second time. `retirePlan` is therefore gated on `first`, plus a run-level flag so the flow is entered once even if a later refactor changes the call graph, and `TestInitRepoRetireRunsTheFlowOnce` asserts `Orphans`, the typed confirmations and the API delete each ran exactly once across both calls. The API delete is the run's final step, after the last apply, so the image-deploying re-plan cannot race it.

Flow inside the `first == true` `planRepo` call, before `t.Plan` (`init.go:2326`): read the state (`t.ShowState`), read the orphan set from the labels (`infra.Orphans`, Task 4 — read-only, and it runs under `--plan-only` too), union the two into the retirement set, fill the optional fields of the state-derived rows with `infra.FillFromState(st, rets)` (Task 2's table — `Logical` from the `for_each` key, `SecretID` from the container's `secret_id` attribute, `Job`/`Region` from the job's `name` and `location`, empty when unreadable, never guessed; the `Orphans` rows already carry theirs, read live), and if the set is non-empty print `printRetirements`. **A non-empty set is a warning, not a refusal, and this paragraph is where the two documents say the same thing** (design §3.3). Without `--retire` the run refuses with exit 1 **only where today's path already dies**, which is a strictly narrower condition than "the set is non-empty":

  - **a state-held job whose `values.deletion_protection` is `true` whose variant has left the configuration** — seen from the state, *before* the plan, because no apply can lower a protection whose resource has left the configuration. The run prints the summary, names `gcloud run jobs delete`/`--retire`, and exits 1;
  - **a plan that aborts on a `prevent_destroy` resource** — the lazy case of step 3 below: `t.Plan` fails, the failure matches the §3.3 signature, and the summary and the next command are printed instead of Terraform's text, exit 1.

**Everything else continues into the ordinary path**, exactly as `gcp-setup.md` documents today: the summary prints, the plan runs, `tf.Guard` refuses the deletes it always refused, `--allow-delete <addresses>` does what it has always done, and the exit status is unchanged. A retired job whose state says `deletion_protection = false` — the user who followed the documented order — is that case, not a refusal, and `TestInitRepoRetireNonBlockingSetPrintsAndContinues` pins it beside `TestInitRepoRetireDoesNotRefuseAJobAlreadyUnprotected`. The summary still names `--retire` as the one-command alternative; offering it is not the same as refusing the manual route.

With `--retire`:

1. **The typed confirmation — `askTyped`, and never `ask`.** The primitive is `r.askTyped(what)` (`init.go:1023`), which prints the `⚠ CONFIRM (project %s, GCP project %s): %s` banner and calls `canConfirmTyped(initflow.Typed, r.conditions())` (`init.go:938`); the answer it accepts is **the project's name**, the only thing `typed` (`init.go:1000`) takes. **`ask` (`init.go:983`) is the wrong primitive and must not be used for this:** it is **covered by `--yes`** — it checks only the agent marker and returns true — so a retirement confirmed through `ask` would be yes-able, which Global Constraints and design §3.3 forbid. The `%s` the banner prints names **every secret that will be left in Secret Manager** by logical name and Secret Manager ID, and the job that will be deleted; the typed answer is the project name, so that listing is the **banner**, not the thing typed (which is also what `TestInitRepoRetireConfirmationNamesEveryKeptSecret` asserts: the names appear, and `--yes` still refuses). `confirmRepo`'s typed owner/name pattern (`init_repo_gate.go:133`) is the precedent for the phrasing.
2. **Remove the state — the protected addresses only** — `t.StateRm(ctx, infra.Removals(state, rets)...)` (Task 3, design §4.2(a)): the `prevent_destroy` secret containers and the Cloud Run jobs whose variant has left the configuration, and nothing else. The retirement's unprotected addresses (the retired variant's service account, its IAM members, its bucket grant) **stay in state**, so the plan that follows still contains their deletes — that is what makes the origin's plan 11 deletes, and taking them out here would orphan them in GCP outside fugaro's view. Nothing is destroyed by this step; what it buys is the plan's ability to run at all, which the protected resources otherwise prevent.
3. **Plan, and guard** — `guard(plan, infra.RetireAllowDelete(plan, rets))` (`init.go:1092`'s wrapper, unchanged). The plan's output is not shown yet: step 4 is where the user reads it and consents to it.
4. **Show the real plan, and confirm it a second time — typed, exactly like the first, and never covered by `--yes`** (review B5, design §4.2). Step 1's confirmation is the *prediction*: it is asked before the protected addresses leave the state, so no plan exists yet and the summary it accompanies cannot contain the plan's counts (§3.2). This second confirmation sits at the ordinary path's own point of consent — `confirmOrdinary` is called at `init.go:2357`, immediately before `t.Apply` at `:2360` (defined in `init_review.go:163`) — **but it is not `confirmOrdinary`, and it must not be.** `confirmOrdinary` goes through `askOrdinary` (`init_review.go:142`), which returns `true` with **no prompt at all** when `runCovers()` says the plan is covered by the run's one review confirmation (`init_review.go:152-155`), and otherwise calls `r.ask` (`init.go:983`), which `--yes` satisfies outright. Both outcomes would make the second confirmation yes-able or skippable, so the retirement calls **`askTyped` (`init.go:1023`) explicitly** here, with the same project-name answer and the same "never `--yes`, never `--non-interactive`, never `--json`, never a pipe, never an agent session" rule — a second use of the primitive, not a reuse of the ordinary confirmation. **The consequence for the `--yes` guarantee is that it rests on the two typed prompts and nothing else**: neither is covered by the run confirmation, `--yes` or a non-interactive stdin. It shows what the first one could not: **the plan's own counts and its full delete list** — the real 2 imports, 6 creates, 3 updates and 11 deletes of the origin, not the prediction — plus any change the plan carries that the retirement did not predict. **A retirement is not a way to apply an unread plan:** if the plan contains a change outside the retirement set, the user sees it and confirms it here, and the only thing this step *adds* over the ordinary path is the retirement's own delete list and the kept secrets, which the banner repeats.
5. **Apply.**
6. **Delete each orphaned job** through the Cloud Run API — `infra.Clients` (`NewClients`, `discover.go:100`), the same client discovery and the same `project`, with the region taken from the job's own `location` (Task 2's `Region`, falling back to the spec's `region` with the summary saying which it used), no new credential path and no `gcloud` shell-out. **The delete's long-running operation is polled to a terminal state before the run reports success** — `jobs.delete` returns an operation, not a finished delete, and reporting success on the operation's *creation* would be the silent disagreement design §6 forbids. **Three outcomes, not two:** operation `DONE` with no error → deleted; operation error, or a delete that fails → "apply ok, job delete failed", exit non-zero, rerun re-attempts; **operation still running when the poll times out → "still deleting"**, printed as its own outcome (not success, not failure), exit non-zero, the job and the operation named, and a rerun re-attempts — a job that finished on its own in the meantime is the 404-is-success case. **`deletion_protection` does not block this API delete and this plan does not claim it does** (design §4.2(c)): it is a *provider-side* flag, so what the state removal buys is Terraform's permission, not Google's. **Never reached** under `--json`, `--non-interactive`, `--yes` or `agentEnv(os.Getenv)` — those all stop before step 2, so no state is removed and no job is deleted.

**The delete needs `run.jobs.delete`, and no fugaro role holds it — so the plan says plainly what it runs as** (review D4). The installation's launcher custom role carries `run.jobs.get`, `run.jobs.list`, the `run.executions.*` pair, `run.operations.get` and the two secret-list permissions, and **no `run.jobs.delete`** (`modules/installation/iam.tf:8-20`), so this is not a launcher-permission operation and the plan must not pretend it is. The delete runs under **the operator's own ADC**, exactly as the Terraform apply does and through the same `infra.NewClients` client — an operator who can already `terraform apply` this repository's root can already delete its jobs. **The 403 outcome is specified, not left to the API:** if the call comes back `PERMISSION_DENIED`, the run reports "apply ok, job delete refused (permission)", names the job, prints the exact `gcloud run jobs delete …` command and the missing permission, and **exits 1** — the same exit status as every other refusal in this task, because the job is out of state, alive in the cloud and unmanaged. A rerun re-attempts it, and the manual `gcloud` command always finishes the work, so the 403 is a guided ending rather than a dead one. Check 42's step 4 is where the live run confirms the permission assumption.

**The refusal gate is narrow, and every documented `--allow-delete` flow keeps working** (design §3.3). `init --repo` refuses a retirement in **exactly two** cases — the two the flow's pre-`--retire` paragraph names, and a non-empty retirement set is not one of them — and neither is a new precondition on the ordinary run: (i) a plan that **fails at plan time on a `prevent_destroy` resource**, matched lazily against the whitespace-normalized signature of Task 6 — never against a pre-computed list of addresses, so a plan that Terraform accepts is never refused on this repo's prediction; and (ii) **a job the state still holds whose `values.deletion_protection` is `true` and whose variant has left the configuration**, where no apply can lower the protection because there is no resource left to carry the `--allow-job-delete` tfvars **variable**.

**The gate reads the attribute, and it must — the address or the retirement set alone is not the gate** (review B4). `FillFromState` (Task 2) already decodes the state's `values` map for exactly this reason, and `deletion_protection` is a job's own attribute in it, so the test is `values.deletion_protection == true` and **not** "the retirement contains a job address". The two are different users: a job whose protection is **already false** is a user who followed `docs/gcp-setup.md` — lowered it with `--allow-job-delete` **before** editing `fugaro.yaml` — and is now running `init --repo --allow-delete <addresses>`; that flow works today, the design promises it keeps working, and refusing it would break precisely the user who did the right thing. **A non-empty retirement set never refuses anything on its own:** it prints the summary (that is a warning, not a gate) and, without `--retire`, the run names the next command and exits 1 — which is not the same thing as the gate and is what every retirement does. `TestInitRepoRetireDoesNotRefuseAJobAlreadyUnprotected` pins the lowered-first flow end to end: a state whose job carries `deletion_protection = false`, an api-key spec and a plain `init --repo --allow-delete <the addresses>` run, asserted to reach the ordinary plan unchanged, with no refusal and no exit 1. `TestInitRepoRetireGateReadsTheStateNotTheAddressList` pins the attribute half directly, with two states that differ only in that value.

**`--retire` accepts `--allow-delete`, and the two are intersected** (design §4.2(d), §3.3). The allow-list the guard receives is `RetireAllowDelete(plan, rets) ∩ --allow-delete` — so naming addresses can only **narrow** what the retirement is allowed to delete, never widen it, and a user coming from the documented manual flow can keep their flag in the command line. `--retire` alone is enough (P4): `--allow-delete` is the escape hatch for the plan's **replaces** (the rows the transition must replace rather than delete), not a requirement.

**`--retire` and `--plan-only` exclude one another, and `--plan-only` stays read-only** (design §3.3): `--retire` joins the existing mutual-exclusion rule at `init.go:709` (`--plan-only`, `--print-vars`, `--config-only`, `--forget`, `--publish-config`, `--anchor`), and a plain `fugaro init --repo --plan-only` reads the state, prints the predicted `printRetirements` summary **first**, and then **continues into the ordinary read-only path** — it shows the ordinary plan, with its counts and its delete list, whenever one can be produced, and exits, having removed no state, deleted no job and applied nothing. **It is not "print the summary and stop"** (review D1): a run whose plan can be produced must still show it, and the summary is an addition to `--plan-only`'s output, not a replacement for it. What it cannot do is show the *retirement's* delete list, because in the case that matters no plan exists until the protected addresses have left the state — and the summary says so when the state removal is what is blocking it. That list comes from `--retire` itself, which prints the plan after the first confirmation and confirms it a second time before the apply. `terraform state rm` **is** a change to the state, so no `--plan-only` path may perform it. **`fugaro doctor` is not changed** (design §5.4): no task here touches `internal/cli/doctor.go`.

`deleteOrphanedJob`'s contract, case by case (Review Focus 7):
- **Ownership first, and the expected labels depend on the job's kind** (review B2): read the job's labels and refuse unless **every pair in `want`** matches. For a **workflow job** `want` is `fugaro=managed`, `fugaro_repo=<this repo's label>` and **`fugaro_workflow=<the retired variant>`** (`modules/workflow/job.tf:11-14`), and the `fugaro_workflow` check is the one that matters: the repo label alone is shared by every variant of the same repository, so matching on it would delete a *surviving* variant's job. For the **check job** `want` is `fugaro=managed`, `fugaro_repo=<this repo's label>` and **`fugaro_role=check`** (`modules/repo/check.tf:22-26`) — there is no `fugaro_workflow` on it, so a rule that demanded one would refuse every legitimate check-job delete and leave a retired check job alive forever. The rule and `Orphans`' rule (Task 4) are the same split, which is why both take labels rather than a workflow name. On mismatch: refuse, print the exact `gcloud run jobs delete …` command, name the label that did not match, and **exit 1** — the same exit status as a failed delete, because a refusal here leaves exactly the condition design §6 forbids to be silent: the job is out of state, alive in the cloud and still protected. The apply has already succeeded, so a rerun is safe (`Removals` filters the state removal, the plan is clean, the delete is re-attempted). It is reported as a leftover with the command to finish it, never as success.
- **404 is success:** a job already deleted (a resumed run, or a hand-run `gcloud` delete) is done, not an error.
- **The delete waits for its operation:** the operation is polled to a terminal state, and a poll that times out reports **"still deleting"** as a third outcome, distinct from success and from failure — the run exits non-zero, names the job and the operation, and a rerun re-attempts without starting a second delete on a job that is already going away.
- **The delete fails:** the run reports "apply ok, job delete failed", names the job, prints the command to retry, and exits non-zero with the state already consistent — the job is out of state and out of the configuration, so a rerun plans cleanly and only re-attempts the delete (Task 6's resume test).
- **The job is gone from state but the workflow is back in the configuration** (the user re-added it between runs): the job is not retired at all — `Retirements` does not list it, so no delete is attempted.

**Refused** under `--yes`, `--non-interactive`, `--json` and `agentEnv(os.Getenv)` — the gate is `askTyped`'s (`init.go:1023`, `canConfirmTyped` at `:938`), which `--yes` cannot cover, with the same agent-marker list every other apply uses (`initsecrets.go:132`/`:146`). **`ask` (`init.go:983`) is explicitly not the gate:** `--yes` satisfies it, so using it here would make a retirement yes-able. **Flagged for opus review (security- and Terraform-sensitive):** the consent gate, the API delete, and the label ownership check.

- [ ] **Step 1: Write the failing tests**

```go
// internal/cli/init_retire_test.go — fakeTerraform (init_test.go:42) and gcpfake
func TestInitRepoRetireRefusesWithoutTheFlag(t *testing.T)          // the origin: the prevent_destroy abort -> summary, exit 1, nothing removed
func TestInitRepoRetireNonBlockingSetPrintsAndContinues(t *testing.T) {
    // design 3.3: a retired job whose state says deletion_protection=false,
    // run without --retire: the summary prints, the ordinary plan runs and
    // the guard behaves as it always did - no refusal and no exit 1, so
    // "the set is non-empty" is not the gate.
}
func TestInitRepoRetireConfirmationNamesEveryKeptSecret(t *testing.T) // logical name and Secret Manager ID
func TestInitRepoRetireConfirmsTwice(t *testing.T) {
    // review B5: two typed prompts - the pre-plan prediction, and the plan's own
    // counts and full delete list at the point the ordinary path confirms
    // (init.go:2357, before t.Apply at :2360). Both are askTyped calls, NOT
    // confirmOrdinary: askOrdinary short-circuits to true when the run
    // confirmation covers the step and --yes satisfies r.ask, so the test
    // also asserts the second prompt is reached with --yes set and with
    // runCovers() true. Assert the order: state rm, plan, prompt, apply.
}
func TestInitRepoRetireShowsThePlanBeforeTheSecondConfirmation(t *testing.T) {
    // review B5: the origin transition - 2 imports, 6 creates, 3 updates, 11
    // deletes, none of them in the pre-plan summary - asserted to be printed
    // before the second prompt, and a plan carrying a non-retirement change
    // is shown and confirmed the same way, so a retirement is not a way to
    // apply an unread plan.
}
func TestInitRepoRetireSecondConfirmationIsNeverCoveredByYes(t *testing.T) // and --non-interactive, --json, agentEnv
func TestInitRepoRetireRunsTheFlowOnce(t *testing.T) {
    // review D3: planRepo is called twice (init.go:2001 first=true and
    // :2039 first=false, the latter only when images were built). The flow is
    // entered from the first==true call only: Orphans, each typed
    // confirmation and the API delete run exactly once, and the second call
    // re-plans and re-applies without prompting again or deleting twice.
}
func TestInitRepoRetireNeverCoveredByYes(t *testing.T)              // and by --non-interactive, --json, agentEnv
func TestInitRepoRetireAndPlanOnlyExcludeOneAnother(t *testing.T)   // design 3.3, init.go:709's rule
func TestInitRepoPlanOnlyRemovesNothing(t *testing.T)               // summary printed; no `state rm` recorded, no job deleted
func TestInitRepoPlanOnlyStillShowsThePlanWhenItCanBeProduced(t *testing.T) {
    // review D1: the summary comes first, and the ordinary plan is still shown (its
    // counts and its delete list) - "print the summary and stop" must not
    // creep back in.
}
func TestInitRepoRetireDoesNotRefuseAJobAlreadyUnprotected(t *testing.T) {
    // review B4: the user who followed gcp-setup.md - deletion_protection already
    // false in the state, then --allow-delete - reaches the ordinary plan
    // unchanged, with no refusal and no exit 1.
}
func TestInitRepoRetireGateReadsTheStateNotTheAddressList(t *testing.T) {
    // review B4: two states differing only in values.deletion_protection - true is
    // refused, false is not - so the gate cannot be "the set is non-empty".
}
func TestInitRepoRetireRemovesStateBeforeThePlan(t *testing.T)      // the recorded terraform invocations, in order
func TestInitRepoRetireUserChangedFileFirstUsesStateRmThenAPI(t *testing.T) // Review Focus: the origin order
func TestDeleteOrphanedJobRefusesAJobThatIsNotOurs(t *testing.T)    // Review Focus 7
func TestDeleteOrphanedJobRefusesAnotherVariantsJob(t *testing.T)   // same repo label, different fugaro_workflow
func TestDeleteOrphanedJobDeletesTheCheckJobUnderTheRoleLabel(t *testing.T) {
    // review B2: fugaro_role=check and no fugaro_workflow - deleted, not refused for
    // the missing workflow label. The direction that leaves a retired check
    // job alive if it is wrong.
}
func TestDeleteOrphanedJobRefusesAWorkflowJobWithoutTheWorkflowLabel(t *testing.T) // and the reverse: fugaro_role=check on a workflow-job deletion
func TestDeleteOrphanedJobReportsAPermissionDenied(t *testing.T) {
    // review D4: 403 -> "apply ok, job delete refused (permission)", the job named,
    // the gcloud command and the missing permission printed, exit 1, and a
    // rerun re-attempts.
}
func TestDeleteOrphanedJobRefusalExitsOne(t *testing.T)             // design 4.2(c): out of state, alive, protected -> exit 1
func TestDeleteOrphanedJobTreatsNotFoundAsDone(t *testing.T)
func TestDeleteOrphanedJobReportsStillDeletingOnTimeout(t *testing.T) // the operation poll: not success, not failure
func TestInitRepoRetireIntersectsAllowDelete(t *testing.T)  // design 3.3: --allow-delete can only narrow the computed list
func TestInitRepoRetireResumeFindsTheOrphanWithoutTheState(t *testing.T) {
    // design 3.1a: the job is already out of state (a failed earlier run),
    // the labels still say it is ours - Orphans returns it, the run still
    // prints it in the summary and still attempts the delete.
}
func TestInitRepoRetireOrphansSurvivingVariantIsNotDeleted(t *testing.T) // Review Focus 7, both directions
func TestInitRepoRetireReportsAnApplyThatSucceededAndADeleteThatFailed(t *testing.T)
func TestInitRepoRetireLeavesTheUnreadableIDEmptyRatherThanGuessing(t *testing.T)
```
The fake Terraform runner is `fakeTerraform` (`init_test.go:42`, a real binary built once by `fakeTFOnce`/`fakeTFBin`); the Cloud Run/Secret Manager fake is `gcpfake`. Both are recorded, so the state-removal-before-plan order and the never-called delete/destroy assertions read the recorded calls, not a mock's expectations. **`TestInitRepoRetireResumeFindsTheOrphanWithoutTheState` and the surviving-variant test are the reason Task 4 exists** — the fake cannot render job labels, or answer a second `show -json` differently, until Task 4 lands, so Task 4 is done first.

Run: `go test ./internal/cli -run 'TestInitRepoRetire|TestInitRepoPlanOnly|TestDeleteOrphanedJob' -count=1`
Expected: **FAIL**.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.
- [ ] **Step 3: `gofmt -l .`** Expected: no output.
- [ ] **Step 4: Commit.**

---

### Task 6: The early abort, the golden transcript, and resume after a half-failure

**Files:**
- Edit: `internal/cli/init_retire.go`, `internal/cli/init.go` (the `t.Plan` failure path at `:2326`). **No edit to `internal/cli/init_output_test.go`:** the `--json` `retire` field is asserted in `init_retire_test.go` (`TestInitRepoJSONCarriesRetirements`), beside the flow it comes from, so this task's file list names only what it changes.
- Create: `internal/cli/testdata/init_retire.golden`, `internal/cli/testdata/prevent_destroy_wrapped.txt`
- Edit: `internal/infra/tf/real_terraform_test.go` (build-tagged) — the fixture generator for the wrapped text
- Test: `internal/cli/init_retire_test.go`, `internal/infra/retire_test.go`

**Interfaces:** no new exported API. The `--json` result document gains `retire` (`[]infra.Retirement`); `failed[].error` keeps the raw Terraform text, so nothing is hidden, only re-framed.

**Same PR and same release as Tasks 1–5** (P7): the summary's next command is `--retire`, so there is no detection-only release to sequence against. This task is last because its golden transcript and its `--json` field are both produced by Task 5's flow, and because its re-framing reads `t.Plan`'s error, which only exists once the retirement path can reach a plan.

`internal/cli/testdata/init_retire.golden` follows the shape of the existing `internal/cli/testdata/init_commands.golden` and `init_review.golden`: the origin transcript as the fixture, produced by `fakeTerraform` and `gcpfake`, with the project IDs and secret IDs normalised to placeholders so the golden carries no real identifier.

**The `prevent_destroy` match is whitespace-normalized, and this task is where that is pinned** (design §3.3). Terraform wraps that message to the terminal width, and the design's §1 quote — real output — breaks after `has`:

```
Resource module.repo.google_secret_manager_secret.this["claude-oauth-token"] has
lifecycle.prevent_destroy set, but the plan calls for this resource to be destroyed.
```

A literal substring match on `has lifecycle.prevent_destroy set, but the plan calls for this resource to be destroyed` **never fires on that text**. So the matcher collapses every run of whitespace (newlines included) to a single space in both the haystack and the needle before testing, and reads the resource address from the same normalized text. `internal/cli/testdata/prevent_destroy_wrapped.txt` is that real wrapped output as a literal fixture — so CI, which uses `fakeTerraform`, still catches a regression: `TestInitRepoPreventDestroyReframed` feeds the fixture through the CLI and asserts the summary is printed and Terraform's text is not, and `TestPreventDestroySignatureIsWhitespaceNormalized` asserts the *unnormalized* match does **not** fire on the same fixture, so the normalization cannot be dropped without failing a test. The build-tagged generator in `internal/infra/tf/real_terraform_test.go` (`-tags terraform`, the file's existing `-update` pattern) really runs Terraform against a `prevent_destroy` resource and rewrites the fixture, so it cannot go stale unnoticed.

- [ ] **Step 1: Write the failing tests**

```go
// internal/cli/init_retire_test.go
func TestInitRepoPreventDestroyReframed(t *testing.T)   // Review Focus 5: the wrapped fixture, summary printed, raw text not
func TestPreventDestroySignatureIsWhitespaceNormalized(t *testing.T) // the unnormalized match must NOT fire on it
func TestInitRepoRetireHalfFailureNamesWhatWasRemoved(t *testing.T) // Review Focus 6, both phases
func TestInitRepoRetireResumeDoesNotReremove(t *testing.T) {
    // Removals filters the state rm; the orphaned job is found again by its
    // labels (Task 4's Orphans), not by the state - design 3.1a.
}
func TestInitRepoRetireResumeNamesTheKeptSecretsFromTheLabels(t *testing.T) {
    // a rerun after a failed delete still lists the two kept containers by
    // logical name and Secret Manager ID, though no state holds them any more.
}
func TestInitRepoRetireReverseReimportsKeptContainersOnlyWhenMountedAgain(t *testing.T) {
    // review D8, the converse of review B3's convergence rule, and the pair the reverse
    // transition test is not enough on its own to pin: with agent.auth back
    // to oauth, DiscoverRepo's Get-by-logical-name path re-adopts both kept
    // containers as imports; with the secrets still released (no state, no
    // config entry), the same containers stay notes - neither imported nor
    // reported as retirements - so a project that re-mounts nothing sees an
    // empty retirement set and no re-import, run after run.
}
func TestInitRepoJSONCarriesRetirements(t *testing.T)
```
Run: `go test ./internal/cli -run 'TestInitRepoPreventDestroy|TestPreventDestroySignature|TestInitRepoRetireHalf|TestInitRepoRetireResume|TestInitRepoJSONCarries' -count=1`
Expected: **FAIL**.

- [ ] **Step 2: Implement, then run again.** Expected: `ok`.
- [ ] **Step 3: The secret-safety assertion** (Review Focus 2): a test that `gcpfake`'s Secret Manager `Delete` and `Destroy` methods were never called across every retirement test, and that the recorded `fakeTerraform` invocations contain no `destroy` or `apply -destroy`.
- [ ] **Step 4: Regenerate the goldens** — `internal/cli/testdata/init_retire.golden` from the fixture (the test writes it under **`-update-retire`**, and it must be a *new* flag: `internal/cli` already registers `-update` for `init_commands.golden` (`init_output_test.go:20`) and `-update-review` for `init_review.golden` (`init_review_test.go:27`), and a second `flag.Bool("update", …)` in the same test binary panics with "flag redefined"; confirm the golden carries no real project or secret ID) and, at build time with Terraform on PATH, `internal/cli/testdata/prevent_destroy_wrapped.txt` via `go test -tags terraform ./internal/infra/tf -run 'TestRealPreventDestroyText' -update -count=1` (it really runs Terraform against a `prevent_destroy` resource; nothing is applied and no cloud is touched). Commit both.
- [ ] **Step 5: `gofmt -l .`** Expected: no output.
- [ ] **Step 6: Commit.**

---

### Task 7: Docs, the skills, Check 42 and the release bullet

**Files:**
- **Edit: `docs/gcp-setup.md`** — a new section, "Changing auth, or removing a workflow, secret or provider", with the ordered steps and the 2026-10-14 worked example. **Ordering constraint: this lands after Task 5**, because `TestBaseImageDocCommandFlagsAreReal` (`docs_base_test.go:173`) resolves every `--flag` near a `trackedDocCommands` mention in this file against the real flag set, so `--retire` must exist in the cobra tree first (or the section must describe the command without a resolvable flag token). The two existing manual sections ("Turning checks off, or removing a workflow", "Offboarding a repository") are edited to point at it, keeping their "the order matters" advice and adding that `fugaro init --repo` now says when you have passed it. **`docs/gcp-setup.md:551`'s stale advice is corrected, not cross-referenced.** Its "Keep the secrets" step tells the reader to hand-write `removed { from = … } lifecycle { destroy = false }` blocks "for each secret in the repository's working directory (`$XDG_STATE_HOME/fugaro/terraform/<gcp-project>/repos/<slug>`)". That advice is wrong on three counts and must go: that mechanic is **not** this plan's primary path (P3 picks `StateRm`), the directory it names is **wiped and rewritten by `PrepareWorkdir`** (`RemoveAll` + `writeGCP`), so a hand-written file there does not survive to the next run, and whether `removed.from` accepts an instance-key address of a still-declared resource is **unverified** (Task 8's Check 42 FACT). The step is rewritten to `fugaro init --repo --retire`, with the manual `terraform state rm <address>` sequence kept as the documented fallback for a binary older than 0.7.2, and a sentence saying the secrets themselves are never deleted. The section stays inside the lint's rules: it describes `--retire` as a command line only once Task 5 ships it.
- **Edit: `docs/design/v1.md`** §8.2 — one cross-reference sentence beside the existing "Offboarding isn't a command yet" paragraph, and the `--retire` flag added to the flag list.
- **Edit: `docs/design/config-transitions.md`** — flip its Status line from "design" to "implemented in 0.7.x" and record the owner's rulings on its §8 questions.
- **Edit: `plugin/skills/setup/SKILL.md`** (and `plugin/skills/upgrade/SKILL.md` if the refusal can appear there) — the refusal text an agent will see, with the instruction not to attempt the transition from an agent session and to hand the user the `--retire` command. The skill lint (`plugin/skills_lint_test.go`, which resolves every `fugaro …` mention against the tree) is what makes this safe only after Task 5.
- **Edit: `docs/gcp-live-checklist.md`** — Check 42's row in the table and its section (below). **The row's own text must say the sequencing, not just the design (review D6):** the check runs `gcp-setup.md`'s "lower the protection **before** changing `fugaro.yaml`" path first, and only then the transition that has already passed that point — so the checklist item is "run the documented order, confirm it still works unchanged (`--allow-job-delete`, then the config edit, then `--allow-delete`), **and then** run the `--retire` path from the state the first run left", and step 1's FACT line records which of the two the summary was printed for. A checklist that only exercises `--retire` would never notice if the documented order had been broken by this change, which is the whole point of the gate being narrow (§3.3). It also lands **after Task 5**, like the section itself: the row quotes `--retire`.
- **Edit: `docs/release.md`** — one plan-shape bullet: confirm the release note names the retirement sequence, and that a repository applying the `repo` root for the first time after this ships sees no unexpected diff. No `*Since` constant unless Task 3 finds the `state rm` path must be gated (P7).

**No task changes `internal/cli/validate.go`, and none is needed.** The design's first draft had `fugaro validate` warn by comparing the configuration against the project's state; `validate.go` has no Terraform, state or `Clients` access at all, so it cannot, and the design drops the claim (its §5.4 and §8 open question 5). Nothing in this plan depends on it: the detection lives in `init --repo`, where the state and the workdir already are. If the owner rules for a validate-side warning, it is a separate change with its own plan and its own plan-file entry — it is not an orphan of this one.

- [ ] **Step 1: Write the docs edits.**
- [ ] **Step 2: Run the docs tests**

Run: `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m`
Expected: `ok`. Also `go test ./plugin -run 'Lint|Skill' -count=1` for the skill lint.

- [ ] **Step 3: Commit.**

---

### Task 8: Owner-run live check 42 (not part of this PR)

**`docs/gcp-live-checklist.md` Check 42: the config-transition round trip (installation `fugaro`, sandbox repository only, run by you; NOT RUN).**

Confirms [docs/design/config-transitions.md](../design/config-transitions.md) §3 and §4 against the real provider, not only the fakes. **Never `belong`.** Register restoring the sandbox's pre-check state as your own cleanup step, first.

1. **The documented order first (review D6), then the abort, reproduced.** With the sandbox's `agent.auth: oauth` and one named account configured, note the two secret IDs (`fugaro secrets ls`) and the variant's job (`gcloud run jobs list --filter labels.fugaro_repo=<sandbox>`). **Run `gcp-setup.md`'s own sequence as written first** — `fugaro init --repo . --allow-job-delete`, then the `agent.auth` edit, then `fugaro init --repo . --allow-delete <addresses>` — and record that it completes unchanged, with the new summary printed and nothing else different. Only then reproduce the abort: with `agent.auth: oauth` again and one named account configured, change `agent.auth` to `api-key` and run `fugaro init --repo .` **with no flags**. **FACT:** the retirement summary's full text, confirming it appears **before** any Terraform error and names the config change, the addresses, the kept secrets and the next command; and that no `lifecycle.prevent_destroy set` text reaches the terminal. Also: the **raw** Terraform error as the terminal actually wrapped it, so `internal/cli/testdata/prevent_destroy_wrapped.txt` is confirmed against a real terminal width rather than only against the build-tagged generator. **FACT (the label calls' permission — the one thing this design could not verify offline; review round 6, design §3.1a):** with the operator's own ADC (no `gcloud auth application-default login` as a launcher), run `gcloud run jobs list --project <project> --region <region> --filter labels.fugaro_repo=<sandbox>` and `gcloud secrets list --project <project> --filter labels.fugaro_repo=<sandbox>`, and paste whether each is permitted or `PERMISSION_DENIED`. The installation's launcher custom role holds both (`modules/installation/iam.tf:8-20`) but is granted to `var.launchers` only (`iam.tf:58-62`), so the operator running `init --repo` may hold neither. If either is refused, record the **exact warning `Orphans` prints** (the call, the permission, the `gcloud` equivalent, the consequence) and confirm the run **continues** with the state-derived set and that the summary distinguishes "nothing to retire" from "could not look". Whichever way it goes, the checklist row records it, and a refusal that is silently ignored is a Task 4 bug to file rather than a fact to paste.
2. **The supported path.** `fugaro init --repo . --retire`. **FACT:** the exact typed confirmation (naming both secrets by logical name **and** Secret Manager ID), the plan's counts, and the resulting `terraform state list` — and, since this is what confirms §3.1's attribute names against a real state, that each ID equals the one `gcloud secrets describe <logical>` reports and that the job name and region the confirmation prints equal `gcloud run jobs describe <job>` — confirming no `google_secret_manager_secret.*` address for either OAuth secret remains, and that no `google_secret_manager_secret` was destroyed. Also: the exact `terraform state rm` invocation fugaro ran, and that the recorded output shows the state removal happened **before** the plan.
3. **The secrets survived.** `gcloud secrets describe <id>` for both. **FACT:** both still exist, still carry `fugaro=managed`, `fugaro_repo`, `fugaro_secret`, and still have an **enabled version** (the version number only, never the value).
4. **The orphan is gone and ours.** **FACT:** the retired variant's job is absent from `gcloud run jobs list`; **the delete's operation reached `DONE`** before fugaro reported success (paste `gcloud run jobs describe` / the operation's terminal status — the fact being checked is that **the operation completes**, not that Cloud Run enforced anything: `deletion_protection` is a provider-side flag and does not block this API delete, design §4.2(c)); a deliberately mislabelled job (create one with no `fugaro` labels, if the project allows) is refused by the delete, with the printed `gcloud` command (Review Focus 7); a **surviving** variant's job — same `fugaro_repo`, different `fugaro_workflow` — is refused too, not deleted; and each refusal **exits 1** with the job named as out of state, alive and still protected (design §4.2(c)).
5. **The reverse transition.** Change `agent.auth` back to `oauth` and run `fugaro init --repo .`. **FACT:** the plan is **imports and creates only, with zero deletes of any `google_secret_manager_secret.*`**; both containers are re-adopted by label; the named variant's job is re-created; and the previously stored value still works (one sandbox run, or `fugaro doctor`).
6. **The `removed`-block alternative, proved or dropped — a hard precondition for adopting it (design §4.2(a), P3).** In the sandbox workdir, hand-write a `removed` block naming an instance-key address of a resource and of a module instance that are **still declared** in the configuration — `removed { from = module.repo.google_secret_manager_secret.this["claude-oauth-token"] lifecycle { destroy = false } }` and the same for a `module.workflow["<variant>"]` child — and run `terraform plan` against the same state. **FACT:** whether Terraform accepts an instance-key `from` for a still-declared resource/module and emits `forget` (with the exact error text if it refuses). **If it refuses, the `removed`-block variant is dropped from design §4.2(a) and `StateRm` stands as the only mechanic; if it accepts, §4.2(a) is updated with the FACT and a follow-up task may generate the block.** No other outcome changes this plan's tasks.
7. **Restore** the pre-check state.

**Paste back:** every `FACT` line above, never a secret value.

- [ ] **Step 1: Run it at your own terminal, on the sandbox only.**
- [ ] **Step 2: Paste the `FACT` lines into the checklist's Check 42 section and its table row.**
- [ ] **Step 3: Commit.**

---

### Task 9: Full suite

- [ ] **Step 1:** `go test ./... -count=1 -timeout 20m`
- [ ] **Step 2:** `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m`
- [ ] **Step 3:** `gofmt -l .`
- [ ] **Step 4:** `fugaro verify test` (the repository's configured command), then commit.
