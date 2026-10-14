# Project-level secrets: implementation plan

**Goal:** let `anthropic-api-key`, `bitbucket-token`, `github-app-key`, `claude-oauth-token` (default), a model provider's key, or a named Claude account's token be stored **once per project** and mounted by every repository the owner allows, while a repository's own stored value still overrides it. No code in this plan has been written or run; this is a design-to-code plan, the same status as `docs/plans/2026-10-09-named-oauth-tokens.md` before its Task 1 started. **This PR (the one that adds this plan and its design doc) is docs-only: no Go, Terraform, or product-behavior file under this plan's "Files" lists is touched by it — corrected, small item 12: `docs/gcp-live-checklist.md`, Task 10's own list, IS one of the three files this PR itself edits (alongside this plan and the design doc), since reserving Check 40's number and writing its not-yet-run steps is itself a docs-only act, same as writing Tasks 1-12 here; the check's own code doesn't exist until Task 10 actually ships.** Tasks 1 onward are future work, gated on the owner approving the design.

**Spec:** [docs/design/project-secrets.md](../design/project-secrets.md). Its decisions (§3 opt-in model, §4 storage, §5 mounting/resolution, §6 migration) are settled there; this plan's own decisions continue as P1 onward, each independently vetoable. **§3a's open question is now resolved, not merely defaulted (round-4 fix, owner's final ruling):** an absent `allow_data_to`, or the explicit marker `allow_data_to: ["*"]`, means "any repository of the project" for BOTH a provider's key availability AND the data-policy gate — the Option A/B framing and SF4's Task-2 gate (an earlier round's safeguard against *starting* Task 2 while the question sat unanswered) are both moot: the question has an explicit answer, so there is no unanswered-question window left to protect Task 2 from.

**Depends on:** release 0.7.x on `main` (the base-image hard cut already merged). No functional dependency on any other in-flight design.

**Checked before this plan was written:** every fact the design doc cites was read from the code at this branch's base — `internal/config/providers.go`, `internal/config/claude_accounts.go`, `internal/config/config.go`, `internal/localcfg/localcfg.go`, `internal/localcfg/shared.go`, `internal/infra/spec.go`, `internal/infra/readiness.go`, `internal/backend/gcp/names.go`, `internal/backend/gcp/secrets.go`, `internal/cli/secrets.go`, `internal/cli/initsecrets.go`, `deploy/terraform/gcp/modules/{installation,repo,workflow}/*.tf`, `internal/infra/tf/guard.go`, `docs/design/layered-config.md`, `docs/project-layer.md`, `docs/multi-model.md`, `SECURITY.md`, `docs/gcp-live-checklist.md`. If the merged code differs when a task starts, that task's implementer adapts its edits to the then-current file, not the other way round (the same rule `docs/plans/2026-10-08-upgrade.md` and `named-oauth-tokens.md`'s plan use).

## Decisions (veto any before execution starts)

- **P1. `project_secrets` is keyed by the exact logical secret name and may hold only a `config.ReservedSecrets` key.** No separate allow-list for a provider's or a named Claude account's secret — their own `allow_data_to` governs project-sharing too (design §3). **`ValidateProjectSecrets` checks membership in `config.ReservedSecrets` only — no collision check against `providers`/`claude_accounts` (S1, corrected: neither can structurally collide with a `ReservedSecrets` key in the first place, so the check this decision originally proposed was dead code; see design §3).** *Veto alternative:* one allow-list block covering every kind of secret uniformly (including providers/accounts), accepting the "two lists that must agree" risk the design rejected.
- **P2. No secret container's for_each key is ever pruned by a config edit alone, in *either* module — `rs.Secrets` (repo) and `InstallationSpec.ProjectSecrets` (installation) both.** The repo-owned container keeps existing, inert if unused, in every repository's Terraform state (design §5, §6) — unaffected by a scope move (today's original P2). **Extended per N3/N4 (review round 3): the SAME invariant must also hold when *eligibility itself* changes — emptying an `allow_data_to` list, removing a provider/account, or narrowing which repositories a no-list provider reaches — not only when a name becomes project-shared.** Both modules achieve this the identical way: a live discovery pass (already needed for adoption, S2) additionally **unions** every correctly-labelled, already-existing secret it finds into the Go-computed map before it reaches tfvars, so the map can only ever grow from live state, never shrink from a config edit. *Veto alternative:* drop a name from either map once it is no longer eligible/project-shared, which would require `terraform state rm`-style handling before any apply could proceed — rejected as a strictly worse migration story for no operational gain, and (N3/N4) because `prevent_destroy` turns that "worse migration story" into a hard `terraform plan`-time failure, not merely a `tf.Guard` prompt.
- **P3. Precedence (repo overrides project) is resolved once, in `internal/infra.Readiness`, at `fugaro init --repo` apply time — never at run launch.** Cloud Run Jobs mount secrets at the job, not the execution (the same fact `named-oauth-tokens.md` §1 establishes); re-resolving at every run would need a live Secret Manager read on every launch, which nothing in this codebase does today. *Veto alternative:* resolve in the runner at boot (a live call each run); rejected as a new, unprecedented live dependency on the hot path, and because it would not change which secret a *job's IAM* can reach — only the job's own grant does that, and that is fixed at apply time regardless.
- **P4. `build.tf`'s build-account grant is rewired to go through the same Go-computed map `sa.tf` already uses**, instead of indexing `google_secret_manager_secret.this` directly (design §5). This is the one genuine Terraform-shape change this feature needs; everything else is new Go computation over an unmodified module shape. **Flagged for opus review (security- and Terraform-sensitive): Task 5.**
- **P5. No command deletes or migrates a now-redundant repo-owned secret.** Decommissioning is a manual, owner-paced `terraform state rm`/delete, outside this plan (design §6 step 5, §9 non-goals).
- **P6. No `*Since` version-gate constant is added for a workflow's own mount — with one narrow, genuine exception, B2 (round 5, corrected again from round 4's own wrong gate mechanism).** This feature changes which secret ID a job's existing env var is mounted from, never a new field the runner's `task.Parse` must know about (design §7). A repository needs `fugaro init --repo` re-applied before project-sharing takes effect for it, exactly like any other `infra` change — no new constant, no new skew test family, for every mount Cloud Run's own `secret_key_ref` resolves. **The one exception: `gitSecretIDSince` (Task 8, B2), gating only the daily check job's own rebuild** — its image is always the base image, independent of which `fugaro` last ran `init --repo`, a genuinely different version-skew shape nothing else in this feature has. Scoped to this one credential and job; every other mount still needs none.
- **P7 (rewritten, round 4 — owner's final ruling, 2026-10-13). An empty/absent `allow_data_to`, or the explicit marker `allow_data_to: ["*"]`, means "available to any repository" for a provider's key, for BOTH mounting/storage AND the data-policy gate (design §3a) — implemented entirely inside `ModelProvider.AllowsData` (`providers.go:62-64`) itself, not a second method.** `AllowsData` returns true when `AllowDataTo` is empty/absent, or contains the literal entry `"*"` (exact match, not a glob), or lists the repository (case-insensitive, unchanged); `ValidateModelProviders` (`providers.go:147-151`) accepts `"*"` as a valid entry alongside a real repository slug. **`CheckProviderPolicy` (`providers.go:334`, and its callers `internal/runner/gateway.go:401`, `internal/cli/validate.go:180,182`), `runner.ProvidersEnv` (`internal/runner/providers.go:32`), `SecretMounts`'s mount condition (`spec.go:534`), and `secretsSet`'s repo-scope validation (`internal/cli/secrets.go:222`) all already call `p.AllowsData(repo)` directly and need ZERO call-site edits** — they inherit the new rule automatically once `AllowsData` itself changes (Task 1). **`ModelProvider.ProviderKeyAvailable` — the prior round's second predicate, needed only because the gate question was still open — is deleted, not kept alongside the new rule**; so are the three call-site switches that prior version of this bullet described (`SecretMounts`, `Readiness`'s precedence check, `secretsSet`), since all three call sites already read `AllowsData`. `runner.ProvidersEnv` additionally already narrows every accepted provider's own `AllowDataTo` to `[]string{repo}` before mounting it (`providers.go:33-36`, unchanged) — a no-list or `["*"]` provider's env never reaches a job in either spelling, so there is no runner version-skew problem to gate. `project_secrets`/`claude_accounts` resolution is unaffected: both keep `AllowsData`-based, non-widened validation, and `repoSlugRE` already refuses the literal `"*"` for both — pinned by two new tests (Task 1) so a future shared-helper change can't accidentally copy the wildcard into either. *Veto alternative:* keep `ProviderKeyAvailable` as a second predicate even though the two questions now have the same answer — rejected: a predicate kept "just in case" the two answers diverge again is exactly the kind of thing that silently drifts from the one it duplicates; deleting it removes that risk entirely, and restoring it later (should the owner ever want the two to diverge again) is a smaller change than maintaining dead-but-not-quite-dead parallel logic in the meantime.
- **P7a. An existing, already-deployed job must never carry a secret_env entry with no version anywhere (B5(a)) — the rule is "any unresolvable entry," not "any *new* one" (corrected, SF — a mismatch between the design's own §3a wording and this plan's Task 3 text, caught in review: `Existing` has no way to tell "new" from "already mounted" apart in the first place, `ex.Jobs` carries only the job's current image, `discover.go:118,511-514`).** `Readiness`'s existing-job branch (`readiness.go:121-122`, `case exists: ws.DeployJob = true`, unconditional) predates this feature and was never designed to protect a live job from an update that adds (or keeps) an unresolvable secret reference — which configuring a provider with no list, or narrowing one (N4), is now enough to trigger. Fixed by having `Readiness` omit *any* such entry from the workflow's spec for that one apply (reporting `Missing`, not refusing the whole update), **and recomputing `FUGARO_SECRET_ENVS` from the post-omission set**, rather than ever setting `DeployJob` to `false` for an existing job (job.tf's `count = var.workflow.deploy_job ? 1 : 0` would destroy it outright — strictly worse). Omitting an *already-mounted* secret this way plans an IAM-member delete for that logical name, `tf.Guard`-protected like every other delete/replace in this design — accepted, not a gap: it is the exact mechanism N4's own revocation story already relies on. *Veto alternative:* refuse the whole `fugaro init --repo` apply outright whenever any workflow would gain an unresolvable mount; rejected as a worse ergonomic regression than simply not adding the one mount yet, and inconsistent with how a missing image is already handled for an existing job (`readiness.go:122-130`, it keeps the current image rather than refusing).

## Global Constraints

From the design:
- `project_secrets` (and every existing `providers`/`claude_accounts` key reused for this) is local-config, operator-only; `localcfg.Config.Shared()` strips it (design §7) — a test pins this the same way `TestMergeSharedNeverCarriesProviders`/`...ClaudeAccounts` already does.
- No secret value is ever read back, echoed, or logged, by any new code path (`fugaro secrets set --project-wide`, `ls --project-wide`, `doctor`'s two new checks all use `List`/metadata only, never `Access`).
- No `fugaro.yaml` key, JSON Schema entry, or project-layer scope-table row is touched: this feature is local-config and Terraform/Go only.
- `rs.Secrets`'s `for_each` never shrinks because of this feature (P2) — a golden test diffs it.
- No live GCP call in any automated test; Check 40's items (design §8) are the owner's own task, run once, sandbox/`fugaro` installations only, never `belong`.
- `CheckProviderPolicy` (`providers.go:334`) and `runner.ProvidersEnv` (`internal/runner/providers.go:32`) are never edited by this plan (P7, design §3a) — **not because their behavior stays the same (it doesn't: both now accept a no-list/`["*"]` provider, the owner's final ruling), but because both already call `AllowsData` directly and inherit the new rule the moment Task 1 changes it, with no call-site edit of their own needed.** `ModelProvider.ProviderKeyAvailable` no longer exists for any task to touch.
- An existing, already-deployed job never gains a secret_env entry with no version anywhere (P7a) — pinned by a dedicated Task 3 test, not merely implied by the `MissingSecret` gate (which only ever protected a brand-new job before this feature).

Project rules (carried over from this repository's existing plans):
- Every task's own test run is in the foreground, `-count=1`, `-race` where the package's existing tests use it.
- Fakes only in tests: `gcpfake`, no real Secret Manager, no real Terraform apply.
- Docs must match behavior: `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m` stays green throughout (this plan adds no `fugaro.yaml` key, so no schema/docs-lint edit is expected — a task that finds otherwise stops and reports rather than loosening the lint).

## Review Focus (mutation-proof notes)

1. **A repository outside an allow-list gets a project secret's variable anyway**, or `secrets set --project-wide` accepts a (non-provider) name with an empty allow-list — **a provider's own name is explicitly the opposite case, round 4/5: an absent or `["*"]` list is accepted on purpose, and an explicit empty list is refused outright (round 5, MUST-FIX 2) — this focus item is about `project_secrets`/`claude_accounts`, which keep "empty means nobody" unchanged.** Pinned by `TestProjectSecretAllowedRepos` (Task 1), `TestReadinessOmitsProjectSecretForUnlistedRepo` (Task 3), `TestSecretsSetProjectWideRefusesEmptyAllowList` (Task 4).
2. **A repo-level override stops winning even though it still has a version**, or a project copy is chosen while the repo copy is live — the precise bug an earlier draft of Task 3 had (B2 in review: "populated unconditionally wherever eligible" plus a Terraform-side `merge()` that lets the second map win would always resolve to the project copy, inverting "repo > project" for every eligible name, not just the unversioned ones). Pinned by `TestReadinessRepoVersionMeansNoProjectSecretsEntry` and `TestReadinessFallsBackOnceRepoVersionGone` (Task 3) — both assert `out.ProjectSecrets` has no entry (or gains one only once the repo version is gone), not merely that the job deploys or that some other field looks right.
3. **A scope change plans a delete of an existing repo-owned secret.** Pinned by `TestRepoNeverPrunesSecretsButGainsCandidates` (Task 2, a golden diff of `rs.Secrets`'s key set before/after a name becomes project-shared, made to fail first per S6 via its second assertion — named for `infra.Repo`, the function's actual current name; an earlier draft of this item called it `TestBuildRepoSpecNeverPrunes...`, `BuildRepoSpec` being that function's name before a rename this plan's authoring predates having noticed) and the real two-step golden plan (Task 6, B3) that observes the real Terraform plan's resource actions — the only way to see a secret-resource delete (or lack of one) that pure Go cannot.
4. **The build account's grant (or the check job's secret reference, B4) drifts from the job account's resolved secret** (P4): the `repo.tftest.hcl` `run "build_and_check_use_the_resolved_secret"` block (Task 5) asserts all **three** `local.secrets`-consuming paths (job, build, check) resolve the identical ID for the identical logical name, for every fixture — through the real Terraform test framework, not a Go unit test, since only it can observe what a Terraform `local` actually resolves to.
5. **A value leaks through `ls --project-wide`, `doctor`, or a plan/state print.** Pinned by `TestSecretsLsProjectWideNeverPrintsValue`, `TestDoctorProjectSecretChecksReadNoValue` (Task 4/7), both asserting the fake Secret Manager's `Access` is never called.
6. **The P7 widening (a no-list/`["*"]` provider's key becomes mountable and routable everywhere) silently narrows back to only the non-empty-list case, or `AllowsData`'s wildcard/empty branch leaks where it shouldn't (round 4, rewritten — `ProviderKeyAvailable` no longer exists to name here).** Pinned by `TestAllowsDataEmptyMeansAnyRepo`/`TestAllowsDataWildcardMeansAnyRepo` and `TestAllowsDataNonEmptyListUnaffectedByWildcardRule` (Task 1, asserting `AllowsData` — the one function `CheckProviderPolicy`, `ProvidersEnv`, `SecretMounts` and `secretsSet` all call — returns `true` for empty/`["*"]` and still refuses an unlisted repository under a real, non-empty list) and the paired `TestSecretMountsNoListProviderAvailableToAnyRepo`/`TestSecretMountsListedProviderStillRefusesUnlistedRepo` (Task 2) and `TestReadinessProviderNoListEligibleForEveryRepo`/`TestReadinessProviderListedExcludesUnlistedRepo` (Task 3), each pair covering both the empty-list and non-empty-list branches so a mutation that collapses the wildcard rule to always-true or deletes it entirely always fails at least one test in its pair.

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/config/project_secrets.go` (new), `project_secrets_test.go` (new) | `ProjectSecret`, `ValidateProjectSecrets` (reserved-name membership only, no collision check — S1), `AllowedForProjectSecret` | 1 |
| `internal/localcfg/localcfg.go`, `localcfg_test.go`, `internal/localcfg/shared.go`, `shared_test.go` | `Config.ProjectSecrets` field, `Shared()` strips it, `Validate` wiring | 1 |
| `internal/config/providers.go`, `providers_test.go` | `AllowsData` absorbs the no-list/`["*"]`-means-any-repo rule (P7, owner's final ruling, design §3a); `ValidateModelProviders` accepts `"*"` | 1 |
| `internal/backend/gcp/names.go`, `names_test.go` | `ProjectSecretID` | 2 |
| `internal/infra/spec.go`, `spec_test.go` | `rs.Secrets` unchanged (golden); `RepoSpec.ProjectSecretCandidates` and its population in `workflow()`; no `SecretMounts` collision check (S1, dead code — removed from this task's scope); `SecretMounts`'s per-provider mount condition (`spec.go:534`) stays unedited and automatically does the right thing once Task 1's `AllowsData` change lands (P7, round 4) | 2 |
| `internal/infra/readiness.go`, `discover_test.go` (S6: `Readiness`'s own tests live here, not a `readiness_test.go` that doesn't exist) | the live precedence check; populates `RepoSpec.ProjectSecrets` (the tfvars-reaching output, never `ws.SecretIDs`); the existing-job gating fix (P7a); a provider's own name resolves eligibility via `AllowsData`, uniform with `project_secrets`/`claude_accounts` now (P7, round 4) | 3 |
| `internal/cli/secrets.go`, `secrets_test.go` | `--project-wide` flag, validation branch (3 call sites, S1 order fix), `ls` SCOPE column, `ls --project-wide` | 4 |
| `deploy/terraform/gcp/modules/installation/secrets.tf` (new), `variables.tf`, roots/installation | `google_secret_manager_secret.project`, operator grants, `var.project_secrets` input | 4 |
| `internal/infra/tfvars.go`, `imports.go`, `discover.go` | `InstallationSpec.ProjectSecrets`, `importProjectSecret`, `DiscoverInstallation`'s adoption step (S2) | 4 |
| `deploy/terraform/gcp/modules/repo/build.tf`, `workflows.tf`, `check.tf`, `variables.tf`, `internal/infra/tfvars.go`/`spec.go`, `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl` | **P4, opus review:** `local.secrets`; `build.tf` AND `check.tf` (B4) rewired through it; the Go-to-tfvars wire for `RepoSpec.ProjectSecrets` | 5 |
| `internal/infra/installation_terraform_test.go`, `deploy/terraform/gcp/roots/installation/tests/`, `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl`, `internal/infra/spec_test.go` golden fixtures | real `terraform test` plan-level fixtures: no secret-resource deletes, but the expected IAM-member replacements, `tf.Guard` refusing/accepting them (B3) | 6 |
| `internal/cli/doctor.go`, `doctor_test.go` | two per-repository checks plus a project-wide revocation-lag one (design §5, S3) | 7 |
| `internal/cli/image.go`, `internal/cli/imagecheck.go`, `internal/cli/doctor.go`, `internal/cli/init.go`, `internal/cli/githubapp.go`, `internal/cli/recipes_skew.go`, `internal/cli/init_fugaroyaml.go`, `internal/infra/readiness.go`, `internal/infra/spec.go`, `internal/infra/discover.go` | `ResolveSecretID`/`GrantedSecretID` helpers; `cloudBuildSpec` takes a resolved `gitSecretID` parameter (`gcp.BuildSpec`'s own field already exists, `internal/backend/gcp/build.go` itself is unedited); `CheckJobSpec.GitSecretID`; the GitHub App doctor/init checks resolve too (N1, small item 5); `planRepo` threads `ready` through and gates on the check job's base image, reusing `baseRefReleaseRE`/`imagePredates` (B1/B2, round 5) | 8 |
| `internal/cli/initsecrets.go`, `initsecrets_test.go` | `find()` consults `infra.ProjectSecretCandidate` before calling a name missing; never prompts/stores to the repo copy when a project copy already has a version (N2) | 9 |
| `docs/design/v1.md`, `docs/multi-model.md`, `docs/design/m10-multi-model.md`, `docs/gcp-live-checklist.md`, `docs/release.md`, `docs/gcp-setup.md`, `internal/cli/docs_*_test.go` | cross-references, Check 40, the release checklist bullet, the shared-config list (S6); `m10-multi-model.md`'s own stale `allow_data_to` claims (MUST-FIX 3) | 10 |
| none | full suite | 11 |
| `docs/gcp-live-checklist.md` (results pasted in) | the owner's own run, Check 40a-j | 12 |

## PR group

One PR, branch `project-secrets`, Tasks 1 to 11 in order for the merge; Task 12 is the owner's own run, not blocking (the same relationship `named-oauth-tokens.md`'s Task 11 has to its own PR).

Parallelizable: **Task 2 needs Task 1** (its `ProjectSecretCandidate` helper reads `AllowsData`, fixed by Task 1's P7 change; it has no collision check to read `lc.ProjectSecrets` for any more, S1), so they are no longer independent — Task 1 goes first, Task 2 follows directly after. Task 3 needs 1 and 2 (its precedence resolution also reads `AllowsData`, uniform across all three name sources now, P7). Task 4 needs 1, 2 and the installation Terraform half of itself (its Go and Terraform halves can split further if useful). Task 5 needs 2 (and is reviewed separately from the rest, per P4). Task 6 needs 4 and 5. Task 7 needs 3. **Task 8 needs 3 and 5 (N1, new: `Readiness`'s `CheckJobSpec.GitSecretID` and the reusable resolve helper it extracts, design §5). Task 9 needs 2's `infra.ProjectSecretCandidate` helper (N2, new: `fugaro init --repo`'s secrets stage reuses it).** Task 10 needs 1 through 9 settled. Task 11 is last. (Task 12, the owner's own live run, is separate and non-blocking, as always.)

## Known follow-ups before implementation (round 4, recounted round 5 — noted, not reworked into the tasks above; each is a real gap or correction a future implementer must check against the then-current code before writing the task it touches)

**Recounted again, round 5 (must-fix, 3 of 4 review passes flagged this independently): the true open count is 6, not 7 — item 2 below (round 4's own list) described Task 8 never naming `runImageBuildCloud` as a third caller of `cloudBuildSpec`, and this round's own Task 8 rewrite (B1) already names and fixes it. Removed, not left stale alongside the fix it describes as missing; the remaining 6 are renumbered, not merely relettered, so no gap is left implying a deleted item.**

1. **A shared git credential's reach goes beyond the mount (threat model gap).** A project-wide `bitbucket-token` (a workspace token) or `github-app-key` (an App installation that mints tokens for every repository it is installed on) is readable, by design, from any allowed repository's job or build — including a PR's own code running inside that job. §3's fourth "must stay impossible" bullet ("a PR author's code reading more than a repo-level secret already lets it read today") holds for *the mount itself* but says nothing about what the *value* can then reach: a shared git credential can plausibly grant access to repositories *other than* the one whose job holds it, a strictly bigger blast radius than a repo-scoped token ever had. State this explicitly in the threat model (§3) before this design ships, not as an implicit consequence of §5's mounting mechanics.
2. **N3's union mechanics need one more wiring step than the design states.** `DiscoverInstallation` returns only `Imports` today; the live union this design needs must additionally be threaded into a field on `spec` itself, merged in *before* `prepare()` writes the tfvars (`internal/cli/init.go:1143-1152`: `im, err := infra.DiscoverInstallation(...)`, then `spec.AdoptLegacyRegistry = im.AdoptLegacyRegistry`, then `prepare(wd, spec, im)` — the exact shape a new `ProjectSecrets`-union field needs to follow). The union step must also check a discovered secret's live ID equals `gcp.ProjectSecretID(project, <its fugaro_secret label>)` before unioning it in — not just that the labels match — so a secret some other name happens to carry matching labels for (a different kind of accident than SF7's, same family) can't be unioned under the wrong logical name.
3. **N4's `ExtraSecrets` must also be imported in `DiscoverRepo`, and added to `secretPolicies`, not only unioned into `out.Secrets`.** The existing per-secret loop (`discover.go:547-571`) populates both the Terraform import (`d.add(importSecret, ...)`) and `secretPolicies[logical]` together, in one pass; `ExtraSecrets`' own, separate discovery step does neither today as drafted. Without the import, Terraform's next apply tries to *create* a secret that already exists (a 409); without `secretPolicies`, the later grant checks (`checkGrants`, `noteExtraAccessors`) that read `secretPolicies[logical]` silently see nothing for that name.
4. **P7a's broadened "any unresolvable entry" rule changes behavior for every installation, not only the ones this feature is for — and the stated reason it couldn't be narrower may be wrong.** The design argues `Existing` cannot tell "new" from "already-mounted" apart without a larger discovery addition — but `DiscoverRepo` already reads the full live job object (`j, err := d.job(spec.Region, ws.Job)`, `discover.go:653`), which carries the job's *current* mounted env vars, before this omission logic would ever run. A future implementer should check whether `j` already has enough information to narrow the omission back to "a name not mounted today" (limiting the behavior change to genuinely new mounts, the original, narrower intent) before accepting the broader rule as the only option.
5. **`DiscoverRepo`'s existing grant-correctness checks (`checkGrants`, `noteExtraAccessors`, binding counts) only ever look at repo-scoped secret IDs.** The "conditional-accessor refusal" these checks perform for a repo-owned secret's live IAM policy never extends to a *project* secret's policy at all — a gap in coverage this design's own SF3/S3 project-wide revocation-lag check (Task 7) only partially closes (that one is project-wide but warning-only, not the same hard-refusal shape `checkGrants` gives a repo secret).
6. **Task 6's goldens may not support the scope-move scenario as drafted.** The existing golden plans hold only `resource_changes` (no `planned_values`), `repo.plan.json` is generated from the `github-vertex.tfvars.json` fixture while Task 5's own fixture is `bitbucket-oauth`, and a fresh-create plan leaves every computed attribute (service account emails, generated IDs) unknown — none of which a synthesized prior state (Task 6's own two-step approach) can simply copy forward. Building the prior state needs to start from the *same* tfvars Task 6's scope-move scenario actually tests, and fill in or derive the computed attributes a fresh plan leaves unknown, not treat `repo.plan.json` as a drop-in base state.

### Task 1: `config.ProjectSecret`, its validation, the local config field, and `AllowsData`'s no-list/`["*"]` rule (P7, round 4)

**Files:**
- Create: `internal/config/project_secrets.go`
- Test: `internal/config/project_secrets_test.go`
- Edit: `internal/localcfg/localcfg.go`, `internal/localcfg/localcfg_test.go`, `internal/localcfg/shared.go`, `internal/localcfg/shared_test.go`
- Edit: `internal/config/providers.go`, `internal/config/providers_test.go` (P7, the owner's 2026-10-13 ruling and its same-day final extension, design §3a — independent of `ProjectSecret` above; both are local-config-layer additions this task delivers together)
- Edit: `internal/config/claude_accounts_test.go` (one new test pinning that `repoSlugRE` already refuses the literal `"*"`, so `ValidateClaudeAccounts` never accidentally inherits `ValidateModelProviders`' new wildcard acceptance)

**Interfaces (signature corrected per S1: `providers`/`accounts` parameters removed — a provider's `.Secret` and a named account's derived secret can never collide with a `config.ReservedSecrets` key in the first place, by construction, so a collision check needs no access to either object; see below):**
```go
// package config
type ProjectSecret struct{ AllowDataTo []string }
func (p ProjectSecret) AllowsData(repo string) bool
func AllowedForProjectSecret(secrets map[string]ProjectSecret, name, repo string) bool
func ValidateProjectSecrets(secrets map[string]ProjectSecret) []Problem

// providers.go — AllowsData ITSELF changes (round 4); no second method
func (p ModelProvider) AllowsData(repo string) bool
```
`ValidateProjectSecrets` refuses (P1): a key not in `config.ReservedSecrets`; a malformed `allow_data_to` entry (reusing `repoSlugRE`/`hasDotSegment`, exactly as `ValidateClaudeAccounts` does, `claude_accounts.go:136-140`) — **`repoSlugRE` refuses the literal `"*"`, unchanged by this task: `project_secrets:` does not get the wildcard rule, only `providers:` does (design §3a).** **No collision check against `providers`/`claude_accounts` is needed or added (S1):** `ValidateModelProviders` already refuses a provider's `.Secret` from ever being a `config.ReservedSecrets` key (`providers.go:127-129`, unconditional), and no named claude account's derived secret (`claude-oauth-token-<name>`) is shaped like any of the 4 reserved keys either — so once `project_secrets`'s own keyspace is `ReservedSecrets`-only, neither kind of collision can structurally occur. An earlier draft of this interface built such a check with `ClaudeAccountBySecret`, which would have incorrectly refused `project_secrets.claude-oauth-token` itself (the bare, intended default-account case, design §3's own example) — `ClaudeAccountBySecret(accounts, "claude-oauth-token")` always returns `(DefaultClaudeAccount, true)`, configured or not (`claude_accounts.go:91-93`) — caught in review and removed rather than special-cased, since the check itself was unnecessary.

**`AllowsData` (round 4, rewritten — a prior round's `ProviderKeyAvailable` is deleted, not kept as a second method; round 5, corrected again — MUST-FIX 2: absent and explicit-empty are no longer the same case):**
```go
func (p ModelProvider) AllowsData(repo string) bool {
	if p.AllowDataTo == nil {
		return true
	}
	return slices.ContainsFunc(p.AllowDataTo, func(r string) bool { return r == "*" || strings.EqualFold(r, repo) })
}
```
True for any repo when the list is **absent** (`nil` — the key never written at all), or when the list contains the literal entry `"*"` (exact match, never a glob), otherwise an exact case-insensitive match against a listed repository, as before (design §3a, the owner's final ruling). **An explicit, non-nil `allow_data_to: []` is a different, refused state (round 5's ruling, below) — checking `p.AllowDataTo == nil` here, not `len(p.AllowDataTo) == 0`, is deliberate**: `gopkg.in/yaml.v3` preserves the nil/non-nil distinction on decode, and this keeps `AllowsData` itself fail-closed (an empty-but-non-nil list matches nothing in the `ContainsFunc` below) even if `ValidateModelProviders`'s own refusal were ever bypassed. **Every existing caller — `CheckProviderPolicy` (`providers.go:334`), `runner.ProvidersEnv` (`internal/runner/providers.go:32`), `SecretMounts` (`spec.go:534`), `secretsSet` (`internal/cli/secrets.go:222`) — needs zero edits**: all four already call this exact method. `ValidateModelProviders` (`providers.go:147-151`) additionally accepts the literal `"*"` entry (today's `repoSlugRE.MatchString(r)` check gains an `r == "*" ||` short-circuit ahead of it, or an equivalent early-accept — the implementer's choice, pinned by the tests below either way), **and refuses an explicit, non-nil, zero-length `AllowDataTo` outright** (`p.AllowDataTo != nil && len(p.AllowDataTo) == 0`), with a message naming the ambiguity: write `["*"]` for "any repository, on purpose," or remove the key for "not decided yet."

Edit `internal/config/providers.go:44-45`'s `AllowDataTo` field comment, stale since this ruling (**MUST-FIX 3**): `// AllowDataTo are the repositories (owner/name) whose code may be sent here; empty means none.` becomes something like `// AllowDataTo are the repositories (owner/name) whose code may be sent here. Absent (the zero value) or ["*"] mean every repository (owner ruling, 2026-10-13 and its follow-ups); an explicit, non-nil, empty list is refused by ValidateModelProviders as ambiguous.`

- [ ] **Step 1: Write the failing tests**

```go
// internal/config/project_secrets_test.go
package config

import "testing"

func TestProjectSecretAllowedRepos(t *testing.T) {
	secrets := map[string]ProjectSecret{
		"anthropic-api-key": {AllowDataTo: []string{"acme/app", "Acme/Sandbox"}},
	}
	if !AllowedForProjectSecret(secrets, "anthropic-api-key", "acme/app") {
		t.Fatal("an allowed repo was refused")
	}
	if !AllowedForProjectSecret(secrets, "anthropic-api-key", "acme/sandbox") {
		t.Fatal("case-insensitive match failed")
	}
	if AllowedForProjectSecret(secrets, "anthropic-api-key", "acme/other") {
		t.Fatal("an unlisted repo was allowed")
	}
	if AllowedForProjectSecret(secrets, "bitbucket-token", "acme/app") {
		t.Fatal("a name with no entry was allowed")
	}
}

func TestValidateProjectSecretsRefusesNonReservedName(t *testing.T) {
	ps := ValidateProjectSecrets(map[string]ProjectSecret{"db-password": {AllowDataTo: []string{"acme/app"}}})
	if len(ps) != 1 {
		t.Fatalf("ValidateProjectSecrets = %+v, want one problem", ps)
	}
}

func TestValidateProjectSecretsAcceptsReservedName(t *testing.T) {
	if ps := ValidateProjectSecrets(map[string]ProjectSecret{"anthropic-api-key": {AllowDataTo: []string{"acme/app"}}}); len(ps) != 0 {
		t.Fatalf("a valid block was refused: %+v", ps)
	}
}

func TestValidateProjectSecretsAcceptsBareClaudeOAuthToken(t *testing.T) {
	// S1, the regression guard: this is the exact case a naive
	// ClaudeAccountBySecret-based collision check would have wrongly
	// refused (ClaudeAccountBySecret(accounts, "claude-oauth-token")
	// always matches the implicit default account, configured or not).
	// project_secrets.claude-oauth-token is design §3's own documented,
	// intended configuration and must never be refused.
	if ps := ValidateProjectSecrets(map[string]ProjectSecret{"claude-oauth-token": {AllowDataTo: []string{"acme/app"}}}); len(ps) != 0 {
		t.Fatalf("the bare default claude-oauth-token name was refused: %+v", ps)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestProjectSecret|TestValidateProjectSecrets' ./internal/config/`
Expected: FAIL, build failed: `undefined: ProjectSecret`.

- [ ] **Step 3: Write the implementation**

`internal/config/project_secrets.go`, mirroring `claude_accounts.go`'s shape exactly (`AllowsData` case-folded via `strings.EqualFold`, `sortedKeys`, `pluginwire.Printable` on every echoed value). `ValidateProjectSecrets` iterates `sortedKeys(secrets)`; for each name checks `_, reserved := ReservedSecrets[name]` — nothing else (S1: no `ProviderBySecret`/`ClaudeAccountBySecret` call, since neither parameter exists on this function any more).

Edit `internal/localcfg/localcfg.go`: add, next to `ClaudeAccounts`:
```go
	// ProjectSecrets are the reserved secret names (config.ReservedSecrets)
	// the owner has opted into project-wide storage for, by allow-listed
	// repository (design docs/design/project-secrets.md §3). Only the
	// owner sets them, here, never in fugaro.yaml. A provider's or a named
	// Claude account's own allow_data_to governs its own secret instead;
	// neither can collide with a ReservedSecrets key in the first place
	// (S1), so ValidateProjectSecrets checks membership in ReservedSecrets
	// only, nothing more.
	ProjectSecrets map[string]config.ProjectSecret `yaml:"project_secrets,omitempty"`
```
Wire `config.ValidateProjectSecrets(c.ProjectSecrets)` next to the existing `ValidateModelProviders`/`ValidateClaudeAccounts` calls.

Edit `internal/localcfg/shared.go`: add `s.ProjectSecrets = nil` next to `s.Providers = nil`/`s.ClaudeAccounts = nil` (`shared.go:37-38`).

- [ ] **Step 4: Write the failing tests for `AllowsData`'s no-list/`["*"]` rule (P7, round 4 — rewritten: `ProviderKeyAvailable` no longer exists to test separately)**

```go
// internal/config/providers_test.go
func TestAllowsDataEmptyMeansAnyRepo(t *testing.T) {
	for _, p := range []ModelProvider{{}, {AllowDataTo: nil}, {AllowDataTo: []string{}}} {
		if !p.AllowsData("acme/anything") {
			t.Fatalf("AllowsData(%+v) = false, want true (no list set, owner's final ruling)", p)
		}
	}
}

func TestAllowsDataWildcardMeansAnyRepo(t *testing.T) {
	p := ModelProvider{AllowDataTo: []string{"*"}}
	for _, repo := range []string{"acme/app", "ANYONE/anything"} {
		if !p.AllowsData(repo) {
			t.Errorf("AllowsData(%q) = false, want true (explicit [\"*\"] marker)", repo)
		}
	}
}

func TestAllowsDataNonEmptyListUnaffectedByWildcardRule(t *testing.T) {
	// The mutation-proof case: the wildcard/empty-list rule must be purely
	// additive, never weakening a real, non-empty, non-wildcard list.
	p := ModelProvider{AllowDataTo: []string{"acme/app", "Acme/Sandbox"}}
	for _, repo := range []string{"acme/app", "acme/sandbox"} {
		if !p.AllowsData(repo) {
			t.Errorf("AllowsData(%q) = false, want true (listed)", repo)
		}
	}
	if p.AllowsData("acme/other") {
		t.Fatal("an unlisted repo was allowed against a real, non-empty, non-wildcard list")
	}
}

func TestAllowsDataWildcardIsExactNotGlob(t *testing.T) {
	// "acme/*" is NOT the wildcard: only the literal entry "*" is.
	p := ModelProvider{AllowDataTo: []string{"acme/*"}}
	if p.AllowsData("acme/app") {
		t.Fatal("\"acme/*\" was treated as a glob; only the literal \"*\" entry is the wildcard")
	}
}

func TestValidateModelProvidersRefusesExplicitEmptyList(t *testing.T) {
	// MUST-FIX 2, orchestrator's ruling (round 5): an absent key and ["*"]
	// both mean "any repository," but an EXPLICIT allow_data_to: [] is
	// ambiguous (did the operator mean "any repo" or "I haven't decided
	// yet") and must be refused, not silently accepted as either meaning.
	p := okProvider()
	p.AllowDataTo = []string{} // non-nil, explicit, zero-length -- NOT the same input as omitting the key
	ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": p})
	if len(ps) == 0 {
		t.Fatal("an explicit allow_data_to: [] was accepted; it must be refused (write [\"*\"] or remove the key)")
	}
	if !strings.Contains(problemsText(ps), "ambiguous") {
		t.Fatalf("problems = %q, want a message naming the ambiguity", problemsText(ps))
	}
	// The absent-key case (the zero value, never touched) must still be
	// accepted -- this test would also catch a mutation that refused BOTH
	// states instead of only the explicit one.
	if ps := ValidateModelProviders(map[string]ModelProvider{"openrouter": okProvider()}); len(ps) != 0 {
		t.Fatalf("an absent allow_data_to (the zero value) was wrongly refused too: %+v", ps)
	}
}
```

**One EXISTING test flips, and must be updated in this task, not left red:** `TestAllowDataToRequired` (`internal/config/model_providers_test.go:211-215`) asserts `CheckProviderPolicy` returns exactly one problem for a provider with `AllowDataTo: nil` — under the owner's final ruling this is no longer refused at all (`len(got) != 1` becomes `len(got) != 0`), and the test's own comment ("Nothing lists the repository when the provider has no allow_data_to") must be rewritten to say the opposite. **Audited, not flipped (every other candidate checked, none hard-codes empty-list-means-refused behavior):** `internal/runner/providers_test.go`, `internal/runner/provider_policy_test.go` (`TestNoCallBeforeRefusal`'s subtests all use either an explicit non-empty list excluding the tested repo, or `providers = nil` entirely — a different case, no provider at all, unaffected), `internal/cli/validate_test.go` (`validateProviderBlock`'s `allow_data_to: ["acme/sandbox"]` is non-empty throughout), `internal/infra/secretmounts_test.go` (the existing `TestSecretMounts` fixture uses two providers, both with explicit non-empty lists), `internal/cli/secrets_test.go` (its `AllowDataTo` fixtures are `claude_accounts`, not `providers`, unaffected by this provider-only ruling). **Existing installs (`dimipaun/fugaro`, the sandbox) are unaffected in practice**, independent of this audit: both configure every provider today with an explicit, non-empty `allow_data_to` (design §6, "existing providers... nothing changes"), so nothing about their own behavior moves.

**Two more new tests, pinning that `claude_accounts:`/`project_secrets:` do NOT get the wildcard rule:**
```go
// internal/config/claude_accounts_test.go
func TestValidateClaudeAccountsRefusesWildcard(t *testing.T) {
	ps := ValidateClaudeAccounts(map[string]ClaudeAccount{"acme-personal": {AllowDataTo: []string{"*"}}})
	if len(ps) == 0 {
		t.Fatal("the literal \"*\" was accepted as a repository slug for a claude account; only providers: gets the wildcard rule (design §3a)")
	}
}

// internal/config/project_secrets_test.go
func TestValidateProjectSecretsRefusesWildcard(t *testing.T) {
	ps := ValidateProjectSecrets(map[string]ProjectSecret{"anthropic-api-key": {AllowDataTo: []string{"*"}}})
	if len(ps) == 0 {
		t.Fatal("the literal \"*\" was accepted as a repository slug for project_secrets; only providers: gets the wildcard rule (design §3a)")
	}
}
```

- [ ] **Step 5: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestAllowsData|TestAllowDataToRequired|TestValidateModelProvidersRefusesExplicitEmptyList|TestValidateClaudeAccountsRefusesWildcard|TestValidateProjectSecretsRefusesWildcard' ./internal/config/`
Expected: FAIL — `TestAllowsData*` on undefined behavior (the wildcard/absent-means-any-repo rule doesn't exist yet); `TestAllowDataToRequired` fails once updated to expect 0 problems, since today's `AllowsData` still refuses an empty/nil list; `TestValidateModelProvidersRefusesExplicitEmptyList` fails since `ValidateModelProviders` does not yet refuse an explicit `[]` (round 5, MUST-FIX 2); the two `RefusesWildcard` tests already PASS today (that is the point — they pin a property that must survive this task untouched, not one this task changes).

- [ ] **Step 6: Write the `AllowsData` implementation**

```go
// providers.go — the existing method, rewritten (P7, round 4; no second
// method. Round 5: p.AllowDataTo == nil, not len(...) == 0 -- MUST-FIX 2)
func (p ModelProvider) AllowsData(repo string) bool {
	if p.AllowDataTo == nil {
		return true
	}
	return slices.ContainsFunc(p.AllowDataTo, func(r string) bool { return r == "*" || strings.EqualFold(r, repo) })
}
```
And, in `ValidateModelProviders`'s `allow_data_to` loop (`providers.go:147-151`): accept the literal `"*"` entry alongside a real slug, e.g. `if r != "*" && !repoSlugRE.MatchString(r) { add(...) }`; and, before that loop, refuse an explicit, non-nil, empty list (round 5, MUST-FIX 2): `if p.AllowDataTo != nil && len(p.AllowDataTo) == 0 { add(at+".allow_data_to", "an explicit empty list is ambiguous: write [\"*\"] for any repository, or remove the key entirely"); break }` (or equivalent — the implementer's choice of exact message wording, pinned by `TestValidateModelProvidersRefusesExplicitEmptyList` either way).

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go vet ./internal/config/... ./internal/localcfg/... && go test -count=1 ./internal/config/... ./internal/localcfg/...`
Expected: `ok`.

Also add, in `internal/localcfg/shared_test.go`, `TestMergeSharedNeverCarriesProjectSecrets`, the same shape as the two existing tests it sits next to (`shared_test.go:217,245`).

- [ ] **Step 8: Commit**

```bash
git add internal/config/project_secrets.go internal/config/project_secrets_test.go internal/config/providers.go internal/config/providers_test.go internal/config/claude_accounts_test.go internal/config/model_providers_test.go internal/localcfg/localcfg.go internal/localcfg/localcfg_test.go internal/localcfg/shared.go internal/localcfg/shared_test.go
git commit -m "project secrets task 1: config.ProjectSecret, its validation, the local config field, and AllowsData's no-list/[\"*\"] rule (owner's final ruling, design §3a)"
```

---

### Task 2: `gcp.ProjectSecretID` (domain-separated, nit); `rs.Secrets` stays unconditional (golden); `SecretMounts` mounts a no-list provider's key everywhere (P7)

**SF4's gate is satisfied, round 4: the owner has given the explicit, final answer a prior round's gate was waiting for** (§3a: absent/`["*"]` means every repository, for both mounting and the data-policy gate — there is no longer an "Option A, left unanswered" state for this task's mount-widening to combine with into a worst-of-both-worlds gap). This task may proceed without further owner input.

**Files:**
- Edit: `internal/backend/gcp/names.go`, `names_test.go`
- Edit: `internal/infra/spec.go`, `spec_test.go`

**Interfaces:**
- Produces: `func ProjectSecretID(fugaroProject, logical string) string` — same `derive()` call as `SecretID` (`names.go:143-145`), same length/hash budget (`maxSecretID`, `secretHashHex`), domain-separated from it (a nit from review: hash `"project\x00"+fugaroProject+"\x00"+logical`, not `fugaroProject+"\x00"+logical`, so a Fugaro project name that happened to equal some repository's own slug could never make `ProjectSecretID`/`SecretID` collide on the same byte string).
- **Produces: `RepoSpec.ProjectSecretCandidates map[string]string` (`json:"-"`) and `infra.ProjectSecretCandidate(lc *localcfg.Config, logical, repo string) (id string, ok bool)`, an exported, reusable helper (not inlined into `workflow()` only)** — one place computes "is `logical` eligible for `repo` to use project-wide, and what ID would it resolve to": `config.AllowedForProjectSecret(lc.ProjectSecrets, logical, repo)`, or `config.ClaudeAccountBySecret(lc.ClaudeAccounts, logical)` resolving to an account whose own `AllowsData(repo)` is true, or (provider names) `config.ProviderBySecret(lc.Providers, logical)` resolving to a provider whose own `AllowsData(repo)` is true — **uniform across all three name sources now (round 4): `AllowsData` itself absorbs the no-list/`["*"]` rule, so there is no longer a separate `ProviderKeyAvailable` branch for this helper to call instead** — `ok` is false and `id` is `""` otherwise. `workflow()`'s own population of `ProjectSecretCandidates` (for each workflow's resolved logical secrets, `uniqueSecrets(ws)`, and the check job's) calls this helper once per logical name, rather than repeating the three-way check inline. **This is the same helper Task 9 (N2) reuses** for `fugaro init --repo`'s secrets stage, which needs the identical eligibility answer but has no `RepoSpec` to read it from yet (the secrets stage runs *before* the repository stage, §6).
- **No `SecretMounts` collision check for `project_secrets` is added (S1 — an earlier draft of this task proposed one; it was dead code, already covered unconditionally by `config.Validate`, see design §3).**
- **No change needed at all (P7, round 4 — corrected from an earlier draft's "becomes `ProviderKeyAvailable`"): `SecretMounts`'s existing per-provider mount condition (`spec.go:534`, `if p := lc.Providers[pn]; p.AllowsData(repo) {`) stays exactly as it is, byte for byte.** `AllowsData` itself now implements the no-list/`["*"]`-means-any-repo rule (Task 1), so this unedited line automatically widens eligibility the moment Task 1 lands — nothing in this task touches `spec.go:534`. The two tests below exist to *prove* this automatic consequence (a regression/integration proof, not a pin on a deliberate code change here): the loop still appends exactly one `SecretMount{p.Secret, p.SecretEnv()}` per eligible provider, only *which* providers are eligible widens for a no-list/`["*"]` provider.

- [ ] **Step 1: Write the failing test**

```go
// internal/backend/gcp/names_test.go
func TestProjectSecretIDDistinctFromRepoSecretID(t *testing.T) {
	a := ProjectSecretID("acme", "anthropic-api-key")
	b := SecretID("some-repo-slug-0123456789abcdef", "anthropic-api-key")
	if a == b {
		t.Fatal("a project secret ID collided with a repo secret ID")
	}
	if ProjectSecretID("acme", "anthropic-api-key") != ProjectSecretID("acme", "anthropic-api-key") {
		t.Fatal("ProjectSecretID is not pure")
	}
	if ProjectSecretID("acme", "x") == ProjectSecretID("acme2", "x") {
		t.Fatal("two different projects collided")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -count=1 -run TestProjectSecretID ./internal/backend/gcp/`
Expected: FAIL, `undefined: ProjectSecretID`.

- [ ] **Step 3: Write the implementation**

```go
// names.go, next to SecretID
// ProjectSecretID is the Secret Manager secret holding a project-wide
// logical secret (design docs/design/project-secrets.md §4), built like
// SecretID but keyed by the Fugaro project name instead of a repository
// slug. The hash input is domain-separated with a literal "project\x00"
// prefix (a nit from review) so a Fugaro project name that happened to
// equal some repository's own slug could never make this collide with
// SecretID's hash of the identical (name, logical) pair.
func ProjectSecretID(fugaroProject, logical string) string {
	return derive(sanitize("fugaro-project-"+fugaroProject+"-"+logical), "project\x00"+fugaroProject, logical, maxSecretID, secretHashHex)
}
```

- [ ] **Step 4: The golden test (P2) — rewritten per S6 so it genuinely fails before this task's implementation, not just "passes trivially"**

```go
// internal/infra/spec_test.go
func TestRepoNeverPrunesSecretsButGainsCandidates(t *testing.T) {
	before, err := Repo(sandboxInputs(t, m5Additions))
	if err != nil {
		t.Fatal(err)
	}
	if len(before.ProjectSecretCandidates) != 0 {
		t.Fatalf("ProjectSecretCandidates = %+v, want none with no project_secrets configured", before.ProjectSecretCandidates)
	}
	// bitbucket-token is the sandbox's git credential, mounted by every
	// workflow (SecretMounts's unconditional first entry) -- allow-listing
	// it project-wide here is the design's own worked example (§6).
	after, err := Repo(sandboxInputs(t, m5Additions+"project_secrets:\n  bitbucket-token:\n    allow_data_to: [\"acme/sandbox\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	// (a) P2's golden property: the key SET (and every ID) is identical,
	// unaffected by project_secrets existing at all.
	if !maps.Equal(before.Secrets, after.Secrets) {
		t.Fatalf("rs.Secrets changed:\nbefore: %+v\nafter:  %+v", before.Secrets, after.Secrets)
	}
	// (b) ProjectSecretCandidates gains EXACTLY the expected new entry --
	// this is the assertion that makes the test fail before this task's
	// code exists (S6): nothing populates ProjectSecretCandidates yet, so
	// it would be empty in "after" too, failing this half even though (a)
	// alone would pass trivially (nothing has ever pruned rs.Secrets, so
	// there is nothing yet to regress).
	want := map[string]string{"bitbucket-token": gcp.ProjectSecretID("aurora", "bitbucket-token")}
	if !maps.Equal(after.ProjectSecretCandidates, want) {
		t.Fatalf("ProjectSecretCandidates = %+v, want %+v", after.ProjectSecretCandidates, want)
	}
}
```

- [ ] **Step 5: Write the tests proving the P7 mount-condition widening (round 4, reframed: this is no longer a red-green TDD cycle *within this task* — `AllowsData` itself already implements the rule once Task 1 lands, so these tests exist to prove the automatic consequence, not to drive a code change Task 2 makes)**

```go
// internal/infra/secretmounts_test.go (the existing file SecretMounts's
// own tests already live in — S6, not spec_test.go)
func TestSecretMountsNoListProviderAvailableToAnyRepo(t *testing.T) {
	lc := &localcfg.Config{Providers: map[string]config.ModelProvider{
		"openrouter": {Secret: "openrouter-api-key"}, // no AllowDataTo at all
	}}
	w := config.Workflow{}
	ms, err := SecretMounts("bitbucket-token", &config.Config{Agent: config.Agent{Auth: "api-key"}}, "app", config.DefaultClaudeAccount, w, lc, "acme/never-named")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range ms {
		if m.Logical == "openrouter-api-key" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mounts = %+v, want openrouter-api-key present for a repo the no-list provider never named (design §3a)", ms)
	}
}

func TestSecretMountsListedProviderStillRefusesUnlistedRepo(t *testing.T) {
	lc := &localcfg.Config{Providers: map[string]config.ModelProvider{
		"openrouter": {Secret: "openrouter-api-key", AllowDataTo: []string{"acme/app"}}, // explicit, non-empty, excludes the repo below
	}}
	w := config.Workflow{}
	ms, err := SecretMounts("bitbucket-token", &config.Config{Agent: config.Agent{Auth: "api-key"}}, "app", config.DefaultClaudeAccount, w, lc, "acme/other")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.Logical == "openrouter-api-key" {
			t.Fatalf("mounts = %+v, want no openrouter-api-key for a repo an explicit, non-empty list excludes (P7 must not weaken this case)", ms)
		}
	}
}
```

- [ ] **Step 6: Run the tests — expect BOTH to already pass (round 4: not a failure confirmation; Task 1's `AllowsData` change already landed by the time this task starts, per the Parallelizable note, so there is nothing left for Step 7 to implement)**

Run: `go test -count=1 -run 'TestSecretMountsNoListProviderAvailableToAnyRepo|TestSecretMountsListedProviderStillRefusesUnlistedRepo' ./internal/infra/`
Expected: `ok`, both tests green immediately. If either fails here, that is itself the signal that `spec.go:534` was wrongly edited (e.g. reverted to a literal, hard-coded check) or that Task 1's `AllowsData` change did not actually land first — stop and fix that before continuing, rather than writing new code to force these tests green.

- [ ] **Step 7: No implementation to write (round 4) — `spec.go:534` stays byte-for-byte unedited by this task; confirm this with `git diff internal/infra/spec.go` showing no hunk touching that line before committing**

- [ ] **Step 8: Run the full package once more, to confirm nothing else regressed**

Run: `go vet ./internal/backend/gcp/... ./internal/infra/... && go test -count=1 ./internal/backend/gcp/... ./internal/infra/...`
Expected: `ok`.

- [ ] **Step 9: Commit**

```bash
git add internal/backend/gcp/names.go internal/backend/gcp/names_test.go internal/infra/spec.go internal/infra/spec_test.go internal/infra/secretmounts_test.go
git commit -m "project secrets task 2: gcp.ProjectSecretID (domain-separated); pin that rs.Secrets stays unconditional; prove SecretMounts automatically mounts a no-list/[\"*\"] provider's key to every repo via Task 1's AllowsData change, no edit of its own (owner's final ruling, design §3a)"
```

---

### Task 3: `Readiness` resolves precedence (the live check), using `AllowsData` uniformly for all three name sources now (P7, round 4); `Existing.ExtraSecrets` so narrowing eligibility never bricks a repo (N4); `Existing.ProjectSecretsOK` guards against an accidental label collision (SF7, corrected — not a squatting defense, small item 3)

**Files:**
- Edit: `internal/infra/readiness.go`, `discover.go`, `discover_test.go` (S6: `Readiness`'s own tests already live in `internal/infra/discover_test.go`, not a `readiness_test.go`, which this package does not have; `discover.go` is new scope for this task, N4 — the `DiscoverRepo` step that finds `ExtraSecrets`)

**Interfaces (corrected after review B1/B2: the original draft's `ws.SecretIDs` rewrite never reached Terraform at all — `WorkflowSpec.SecretIDs` is `json:"-"`, internal/infra/spec.go:148 — and its "populated unconditionally wherever eligible" wording would, once wired to Terraform, have made the project copy win even when a repo override exists, inverting "repo > project." Both are fixed together below with two separate fields — one input, one output — not one field doing both jobs):**
- **Input, computed in `infra.Repo()`/`workflow()` (where the local config is in scope), not by `Readiness`:** `RepoSpec.ProjectSecretCandidates map[string]string` (`json:"-"`, never reaches tfvars) — one entry per logical name this repository is *eligible* to use project-wide, value `gcp.ProjectSecretID(fugaroProject, name)`. **Eligibility is uniform across all three name sources now (round 4, corrected — a prior round split this into `AllowsData` for `project_secrets`/`claude_accounts` and a separate `ProviderKeyAvailable` for a provider's own `.Secret`; `ProviderKeyAvailable` no longer exists, since `AllowsData` itself absorbs its rule):** `project_secrets`/`claude_accounts`-derived names use `AllowedForProjectSecret`/`ClaudeAccountBySecret`+`AllowsData`; a provider's own `.Secret` name uses `ModelProvider.AllowsData` too — the identical function name, same call shape, no special case for providers left anywhere in this helper. Being a candidate says nothing about resolution yet.
- **Output, computed by `Readiness` (`Readiness` has no `*localcfg.Config` parameter — `func Readiness(ctx context.Context, c *Clients, spec RepoSpec, ex Existing) (RepoSpec, []Missing, error)` — so it can only consult the candidates map above, never recompute eligibility itself):** `RepoSpec.ProjectSecrets map[string]string` (`json:"project_secrets"` — **this is the field that reaches tfvars as `repo.project_secrets`**, a new Terraform variable, Task 5). `Readiness`'s per-workflow loop (`readiness.go:89-137`) *and* its check-job branch (`readiness.go:139-151`, B4 — the check job mounts a subset of the same logical names and needs the identical treatment, since it was missed in the design's first draft): for each `logical` in `uniqueSecrets(ws)` (or `slices.Compact(slices.Sorted(maps.Values(out.Check.SecretEnv)))` for the check branch) that also has a `spec.ProjectSecretCandidates[logical]` entry, check `hasVersion(logical)` against the **repo** ID (today's `spec.Secrets[logical]`) first.
  - **Repo has a version → add nothing to `out.ProjectSecrets` for this `logical`.** This is the entire "repo overrides project" rule: the absence of an entry, not a flag or a comparison Terraform has to make. A dedicated test (`TestReadinessRepoVersionMeansNoProjectSecretsEntry`, below) asserts `out.ProjectSecrets` has **no key** for `logical` in this case — not merely that `ws.SecretIDs` or any other field looks like the repo won, since the earlier draft's bug was exactly this kind of indirect assertion passing while the real (then-nonexistent) wiring would have inverted precedence.
  - **Repo has no version → `out.ProjectSecrets[logical] = spec.ProjectSecretCandidates[logical]`.** If the project copy *also* has no version, `Missing{Kind: MissingSecret}` names `fugaro secrets set NAME --project-wide` instead of today's `--repo NAME`, and `logical` still gets no `ProjectSecrets` entry.
- **Only names present in `spec.ProjectSecretCandidates` for this repository are ever considered** — an unlisted repository's `ProjectSecretCandidates` simply has no entry for that provider's/name's logical name, so `ProjectSecrets` can never gain one either, regardless of what Terraform later does with the map.
- **N4 BLOCKER, the repo-module twin of N3: `Existing` (`internal/infra/discover.go:511`) gains `ExtraSecrets map[string]string`**, populated by a new `DiscoverRepo` step (`internal/infra/discover.go`, same file Task 3 already lists): a `List` call under `{LabelRepo: spec.Label}` finds every correctly-labelled repo secret, and any logical name **not** already a key of `spec.Secrets` (one `SecretMounts`'s current, narrower config no longer computes — e.g. a provider that was eligible everywhere under §3a's no-list rule, now excluded by a newly-added list) goes into `ExtraSecrets`. `Readiness` unions `ex.ExtraSecrets` into `out.Secrets` before returning — the **container** stays (P2, extended), while `SecretMounts`'s own output still governs the **mount**, so a name `ExtraSecrets` restores to `out.Secrets` but that no workflow currently mounts plans **no** IAM-member grant for it at all (nothing in `values(var.workflow.secret_env)` names it) — only the now-unwanted grant for a name that *was* mounted and no longer is plans a `delete`, caught by `tf.Guard` exactly like every other replace/delete this design introduces.
- **SF7, not caught by any other test here, and corrected this round (small item 3: this is an accident/drift guard, not a squatting defense — labels are set by whoever creates the secret, so a principal able to pre-create one at the guessable `ProjectSecretID` address can set the correct labels too, and the import step would adopt it exactly as it would the legitimate one; the check is worth having anyway, for the same consistency reason `DiscoverRepo`'s equivalent repo-scoped check exists): `Existing` also gains `ProjectSecretsOK map[string]bool`, from the same new `DiscoverRepo` step.** For each `logical` in `spec.ProjectSecretCandidates`, `Get` the secret at that ID; if it exists, check it carries `{LabelManaged, LabelProject: spec.Installation.ProjectName, LabelSecret: logical}` (`hasMarks`, the identical helper the repo-secret loop just above already uses) and call `d.foreign(...)` — a hard refusal, same as any other accidental-collision case this function finds — if it does not; if it does, set `ex.ProjectSecretsOK[logical] = true`. Nothing is added if the secret does not exist at all (not yet created, same as today). **`Readiness` only resolves `spec.ProjectSecretCandidates[logical]` into `out.ProjectSecrets[logical]` when `ex.ProjectSecretsOK[logical]` is true** — this is the same consistency check this design already makes everywhere else it reads a secret by a derived ID, not a boundary against an adversary.
- **P7a: an existing job never carries an unresolvable secret_env entry — "any unresolvable entry," not only one that is new.** When `logical` has no version anywhere (repo nor project) *and* `exists` is true for the workflow it belongs to (`readiness.go:118-120`'s existing `current, exists := ex.Jobs[name]`), `Readiness` removes `logical`'s entry from `ws.SecretEnv` (and, if it was the only env var naming it, `ws.SecretIDs`) for this apply — it does **not** set `ws.DeployJob = false` (that field's only job is "does this job resource exist at all," `job.tf`'s `count = var.workflow.deploy_job ? 1 : 0`; flipping it for an existing job would delete the job outright). The `Missing` diagnostic is still reported, unconditionally, so `fugaro init --repo`'s own output tells the owner exactly what is missing and why nothing was mounted. A brand-new job (`exists` false) is unaffected: it already refuses to deploy at all until every mount resolves (`ws.DeployJob = len(reasons) == 0`, unchanged). **After every such omission, `ws.Env[runner.SecretEnvsVar]` (`FUGARO_SECRET_ENVS`, `spec.go:638`) is recomputed from the post-omission `ws.SecretEnv`**, not left holding the name `SecretMounts` originally wrote — otherwise the runner's own redaction list names a variable the job's environment no longer has.

**Tests (every assertion is now on `out.ProjectSecrets`, the tfvars-reaching output map — never on `ws.SecretIDs`, which the corrected design no longer touches at all):**
```go
func TestReadinessRepoVersionMeansNoProjectSecretsEntry(t *testing.T) {
	// B2, the precedence-inversion finding: both the repo and the project
	// secret have an enabled version (fake Secret Manager), and the name is
	// an eligible ProjectSecretCandidates entry. Assert out.ProjectSecrets
	// has NO key for this logical name at all (not that some other field
	// "looks like" the repo won) and that no Missing is reported. This is
	// the test the earlier, inverted draft of this task would have failed:
	// a Terraform-side merge(repoMap, projectMap) always prefers the second
	// map's value when a key is present in both, so if ProjectSecrets were
	// (wrongly) populated for every eligible name regardless of the repo's
	// own version, this test would catch the project copy winning anyway.
}

func TestReadinessFallsBackToProjectWhenRepoHasNoVersion(t *testing.T) {
	// Repo secret has 0 versions, project secret has 1. Assert
	// out.ProjectSecrets[logical] equals spec.ProjectSecretCandidates[logical]
	// (the project ID) and no Missing is reported.
}

func TestReadinessOmitsProjectSecretForUnlistedRepo(t *testing.T) {
	// spec.ProjectSecretCandidates has no entry for this logical name (the
	// repo isn't allow-listed): out.ProjectSecrets gets no entry either,
	// and Missing (if any) still names --repo, never --project-wide.
}

func TestReadinessMissingNamesProjectWideFix(t *testing.T) {
	// Neither has a version, and the repo IS allow-listed (a candidate
	// exists): the Missing reason names `fugaro secrets set NAME
	// --project-wide`, not --repo, and out.ProjectSecrets still gets no
	// entry (there is nothing to resolve to).
}

func TestReadinessFallsBackOnceRepoVersionGone(t *testing.T) {
	// Simulates P3/design §5's "falls back on the next apply" rule: a
	// first Readiness call with the repo version present produces no
	// out.ProjectSecrets entry; a second call (simulating a later init
	// --repo) with that version now disabled produces one, pointing at the
	// project ID. Two calls, not one, since Readiness never re-runs within
	// a single apply.
}

func TestReadinessProviderNoListEligibleForEveryRepo(t *testing.T) {
	// P7: a provider with no AllowDataTo, a project-wide secret stored with
	// an enabled version, and the repo's OWN copy has no version. Assert
	// out.ProjectSecrets[logical] equals the PROJECT ID for a repository
	// that provider's config names nowhere at all — the no-list-means-any-
	// repo case, distinct from TestReadinessFallsBackToProjectWhenRepoHasNoVersion
	// above (which uses a project_secrets/claude_accounts-style name whose
	// eligibility is unaffected by P7). This requires the repository to
	// have been given a ProjectSecretCandidates entry via ModelProvider's own
	// AllowsData (round 4: the same function every other name source uses,
	// no longer a separate ProviderKeyAvailable) in infra.Repo() first
	// (Task 2's fixture plumbing).
}

func TestReadinessProviderListedExcludesUnlistedRepo(t *testing.T) {
	// P7, the non-empty-list case: a provider WITH an explicit AllowDataTo
	// that does not name this repo. Assert spec.ProjectSecretCandidates has
	// no entry for that provider's logical name for this repo at all (not
	// merely that resolution skips it) — mirrors
	// TestReadinessOmitsProjectSecretForUnlistedRepo but specifically
	// exercises config.ProviderBySecret+ModelProvider.AllowsData (round 4:
	// the same function name every name source calls, just a different
	// receiver type), not config.AllowedForProjectSecret, so a mutation
	// that accidentally routes a provider's name through the
	// project_secrets-specific check (which reads an entirely different
	// map, lc.ProjectSecrets, never lc.Providers) is caught by one of
	// these two provider-specific tests.
}

func TestReadinessCheckJobResolvesProjectSecretsToo(t *testing.T) {
	// B4: the daily check job's secret_env (bitbucket-token/github-app-key,
	// typically) must go through the identical repo-wins-else-project
	// resolution as a workflow's, writing into the SAME out.ProjectSecrets
	// map (check.tf and sa.tf/job.tf all read the one merged Terraform
	// local, Task 5). Repo has no version, project does: assert
	// out.ProjectSecrets gains the entry from the check branch
	// (readiness.go's spec.Check != nil block), not only from a workflow's.
}

func TestReadinessOmitsUnresolvableMountFromExistingJob(t *testing.T) {
	// P7a/B5(a): ex.Jobs[name] reports the workflow's job as already
	// existing; a newly-eligible logical name (a no-list provider's
	// secret) has no version anywhere, repo or project. Assert: (1)
	// ws.SecretEnv (and SecretIDs) for that workflow has NO entry for this
	// logical name — the mount is omitted, not left dangling; (2)
	// ws.DeployJob is still true (the job's other properties keep
	// updating); (3) a MissingSecret Missing is still reported, Deployed:
	// true; (4) ws.Env[runner.SecretEnvsVar] (FUGARO_SECRET_ENVS) does NOT
	// name this logical name's env var either — the recompute, not the
	// value SecretMounts originally wrote before the omission. This is the
	// test that would catch a regression back to "the job updates with a
	// secret_key_ref pointing at a version-less secret," the exact
	// failure mode B5(a) describes, and a second regression where the
	// mount is correctly omitted but the redaction list still names it.
}

func TestReadinessNewJobStillRefusesOnUnresolvableMount(t *testing.T) {
	// The companion case: ex.Jobs[name] reports no existing job at all.
	// Assert the existing, unchanged behavior holds: ws.DeployJob is
	// false while any mount (this one included) is unresolved — P7a does
	// not weaken the brand-new-job gate, only the existing-job one.
}

func TestReadinessUnionsExtraSecretsNeverShrinksRsSecrets(t *testing.T) {
	// N4 BLOCKER: spec.Secrets (as infra.Repo() computed it, from today's
	// narrower, current config) is missing a logical name that
	// ex.ExtraSecrets reports as a correctly-labelled, already-existing
	// repo secret (simulating a provider that used to be mounted
	// everywhere under §3a's no-list rule, now excluded by a newly added
	// list). Assert out.Secrets (the tfvars-reaching map) gains that
	// entry from ex.ExtraSecrets, with its existing ID unchanged -- the
	// container is never dropped from Terraform's for_each.
}

func TestReadinessDropsMountButKeepsContainerOnNarrowedEligibility(t *testing.T) {
	// The companion assertion: for the SAME scenario above, assert the
	// workflow's own ws.SecretEnv/SecretIDs do NOT include the no-longer-
	// eligible logical name (SecretMounts's current output governs the
	// mount, unaffected by the ExtraSecrets union) -- out.Secrets has the
	// container, the workflow spec does not have the mount. This is the
	// test that would catch a wrong fix that keeps BOTH (silently
	// re-granting access the owner just revoked) or drops BOTH (bricking
	// the repo's own apply, N4's original bug).
}

func TestReadinessRefusesAccidentalLabelMismatch(t *testing.T) {
	// SF7, renamed this round (small item 3, round 4: the original name
	// and comment framed this as a defense against a squatting attacker,
	// which is wrong -- labels are set by whoever creates the secret, so
	// a principal able to pre-create one at the guessable ProjectSecretID
	// address could set the correct labels too, and the import would
	// adopt it exactly as it would the legitimate one. This guards
	// against ACCIDENT AND DRIFT instead -- the same reason DiscoverRepo's
	// equivalent repo-scoped check exists -- not an attacker). A secret
	// already exists at gcp.ProjectSecretID(fugaroProject, logical) in the
	// fake Secret Manager, WITHOUT the {LabelManaged, LabelProject,
	// LabelSecret} marks DiscoverRepo's new step checks for (e.g. a
	// coincidental name collision with something else entirely, not
	// fugaro secrets set --project-wide). Assert DiscoverRepo returns a
	// *ForeignError (the same path an unmarked repo secret already takes)
	// and that Readiness, given ex.ProjectSecretsOK with no entry for
	// logical, does NOT populate out.ProjectSecrets[logical] even though
	// the repo copy has no version and the name is an eligible candidate
	// -- the two facts that alone would otherwise resolve to the project
	// ID. This is the test an implementation that skips the label check
	// (resolving on hasEnabledVersion alone) would fail: it would happily
	// mount an unrelated secret's value into every eligible repository.
}

func TestReadinessResolvesCorrectlyLabelledProjectSecret(t *testing.T) {
	// The companion positive case: the identical scenario, but the
	// project secret carries the expected labels (as fugaro secrets set
	// --project-wide always sets them). Assert DiscoverRepo returns no
	// error and ex.ProjectSecretsOK[logical] is true, and Readiness
	// resolves out.ProjectSecrets[logical] normally -- proving the label
	// check is additive (it does not also have to be disabled for the
	// legitimate case to keep working).
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestReadiness' ./internal/infra/` (SF, corrected: the previous regex named `TestReadinessPrefersRepoOverride`, which no longer exists after the B1/B2 fix, and omitted the check-job, P7a and N4 tests entirely — a plain `TestReadiness` prefix covers every test this task adds, present or renamed)
Expected: FAIL.

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/infra/... && go test -count=1 ./internal/infra/...`
Expected: `ok` (the full package, to catch any regression in the existing `Readiness` tests from the edit).

- [ ] **Step 5: Commit**

```bash
git add internal/infra/readiness.go internal/infra/discover.go internal/infra/discover_test.go
git commit -m "project secrets task 3: Readiness resolves repo-vs-project precedence at apply time, AllowsData uniform for a provider's own name now (owner's final ruling, design §3a), Existing.ExtraSecrets so narrowed eligibility never bricks a repo (N4), Existing.ProjectSecretsOK guards an accidental label collision (SF7)"
```

---

### Task 4: `fugaro secrets set/ls --project-wide`; the installation root's container (provider validation per P7)

**Files:**
- Edit: `internal/cli/secrets.go`, `internal/cli/secrets_test.go`
- Create: `deploy/terraform/gcp/modules/installation/secrets.tf`
- Edit: `deploy/terraform/gcp/modules/installation/variables.tf`, `deploy/terraform/gcp/roots/installation/{main,variables}.tf`
- Edit: `internal/infra/tfvars.go` (`InstallationSpec.ProjectSecrets`, populated by `Installation(lc, o)`), `internal/infra/imports.go` (a new `importProjectSecret` kind), `internal/infra/discover.go` (`DiscoverInstallation` gains the symmetric import step `DiscoverRepo` already has, S2) — **this is new scope the design's first draft missed (S2): without it, `fugaro secrets set NAME --project-wide` run before the installation root's first apply makes that apply fail with a 409, not a graceful adoption.**

**Interfaces (Go):**
- `secretsOptions` gains `projectWide bool`; `--project-wide` on `secrets set` and `secrets ls`.
- `secretsSet`: when `--project-wide`, skip `resolveSecretRepo` entirely (there is no `--repo` to resolve); validate `name` is one of: a `config.ReservedSecrets` key with a non-empty `project_secrets[name].AllowDataTo`; a claude-account's derived name whose own `allow_data_to` is non-empty; or **a provider's `.Secret` — accepted unconditionally, with no check on `allow_data_to` at all (P7, design §3a: an empty list is a valid, intentional "available to everyone" state for a provider, not a reason to refuse storage, unlike the other two kinds)**. The secret ID is `gcp.ProjectSecretID(env.lc.Name, name)`; labels are `{fugaro: managed, fugaro_project: env.lc.Name, fugaro_secret: name}` (`gcp.LabelProject`, already defined, `names.go:66`, reused here for the first time outside the runs bucket).
- **No change needed (P7, round 4 — corrected from an earlier draft's "becomes `ProviderKeyAvailable`"): `secretsSet`'s existing REPO-scope branch (`internal/cli/secrets.go:222`: `if !env.lc.Providers[p].AllowsData(r.repo) {`) stays exactly as it is.** Before Task 1, this line refused `fugaro secrets set NAME --repo X` (no `--project-wide`) for *every* repository whenever the provider's list was empty — including the repository an operator was specifically trying to give its own override to (§5, "repo overrides project"). `AllowsData` itself fixes this (Task 1: an empty list now means every repository, not nobody), so this unedited line automatically stops refusing the moment Task 1 lands. A regression test, not a pin on a deliberate code change here, is added below (mirroring Task 2's `TestSecretMountsNoListProviderAvailableToAnyRepo`).
- `secretsLs`: `--project-wide` lists by `{LabelProject: env.lc.Name}` instead of `{LabelRepo: r.label}`, and needs no `resolveSecretRepo` either. The existing (non-`--project-wide`) `ls` gains a `SCOPE` column: for each repo-owned entry, look up whether `config.AllowedForProjectSecret`-equivalent logic would also let this repo use a project copy of the same name, and print `project, repo override stored` when both exist; print a synthetic row (no version info beyond what a second `List` call under `{LabelProject}` returns) for a name the repo is allowed to use project-wide but has no repo-owned entry for at all, scope `project`.
- **`InstallationSpec.ProjectSecrets map[string]string` (`json:"project_secrets"`), populated by `Installation(lc, o)` (`internal/infra/tfvars.go:150`), for exactly these names (S2's second ask — stating precisely which, not leaving it implicit):**
  - every key of `lc.ProjectSecrets` whose own `AllowDataTo` is non-empty, value `gcp.ProjectSecretID(lc.Name, key)`;
  - every `lc.Providers[name].Secret`, **unconditionally** — regardless of that provider's own `AllowDataTo` (§3a: a provider's secret is eligible for project storage whether or not a list exists; the container must exist in Terraform either way for `--project-wide` storage to have somewhere to adopt into);
  - every `lc.ClaudeAccounts[name]` whose own `AllowDataTo` is non-empty, value `gcp.ProjectSecretID(lc.Name, config.ClaudeAccountSecret(name))`.
- **A new import kind, `importProjectSecret` (`internal/infra/imports.go`, next to `importSecret` at `imports.go:87`), template `module.installation.google_secret_manager_secret.project[{key}]`.** `DiscoverInstallation` (`internal/infra/discover.go:401`) gains a step mirroring `DiscoverRepo`'s existing one (`discover.go:540-570`, `for _, logical := range slices.Sorted(maps.Keys(spec.Secrets))`): for each `logical` in `spec.ProjectSecrets`, read it from GCP (`c.Secrets.Projects.Secrets.Get`), skip if absent, check it carries `{LabelManaged, LabelProject: spec.FugaroProject, LabelSecret: logical}` (`d.foreign(...)` otherwise, the existing ownership-mismatch path), and `d.add(importProjectSecret, spec.Region, logical, id)` if it matches — so a value stored with `--project-wide` before the installation root's first apply is adopted, not conflicted with.
- **N3 BLOCKER, a gap the design's first two drafts had: the config-computed `ProjectSecrets` map above (the three bullets just before this one) *shrinks* the moment the owner empties a `project_secrets`/`claude_accounts` entry's `allow_data_to` list (the natural way to "revoke everyone" for those two kinds — corrected this round, MUST-FIX 2: a provider's own list cannot be emptied at all any more, `ValidateModelProviders` refuses it; a provider's eligibility instead shrinks only by narrowing to a real, non-empty list or by deleting the provider's entry entirely, §3a) or removes a provider/claude-account entry entirely — and `google_secret_manager_secret.project` has `prevent_destroy = true` (§4), so the very next `fugaro init` does not merely get a `tf.Guard`-refusable delete, it fails *at `terraform plan` itself*, a hard Terraform-core error, before `tf.Guard` or any Fugaro code ever runs, blocking every future `fugaro init` for the whole installation until an operator manually runs `terraform state rm` on that exact resource address. The identical bricking happens when a second operator's own local config simply lacks an entry the first operator added (`claude_accounts`/`providers`/`project_secrets` are each operator-local, S5 — nothing synchronizes them) and that operator runs `fugaro init`.** Fixed the same way P2 already protects the repo module's own containers: `DiscoverInstallation`'s new discovery pass (above) does not only drive imports — it also **unions** every correctly-labelled project secret it finds live (`{LabelManaged, LabelProject: spec.FugaroProject}`, a `List` call, not per-name `Get`s) into the final `ProjectSecrets` map handed to Terraform, so the map **never shrinks below what already exists**, regardless of what the current config says is still eligible. A name's container can therefore only ever be *added to* the map by this union, never dropped by a config edit alone — exactly P2's own invariant, extended from the repo module to the installation module, which this design's own P2 decision should have covered from the start and did not.

**Interfaces (Terraform, installation module):**
```hcl
variable "project_secrets" {
  description = "Project-wide secret containers: logical name -> secret ID (gcp.ProjectSecretID), fugaro init creates/adopts each; values are never set by Terraform."
  type        = map(string)
  default     = {}
  nullable    = false
}
```
`secrets.tf` as drafted in the design §4 (`google_secret_manager_secret.project`, `for_each = var.project_secrets`, `prevent_destroy = true`, operator `secretVersionAdder`+`viewer` grants mirroring `modules/repo/secrets.tf`'s `operator_secrets` local).

**Tests:**
```go
func TestSecretsSetProjectWideRefusesEmptyAllowList(t *testing.T) { /* a RESERVED name (project_secrets has no entry, or an entry with no repos) or a claude-account name with an empty allow_data_to: refused, no network call made for the value (assert the fake prompt/reader was never invoked). Must NOT cover a provider's secret — that is the opposite case, next. */ }
func TestSecretsSetProjectWideAcceptsProviderSecret(t *testing.T) { /* openrouter-api-key, provider has a non-empty allow_data_to: stored under gcp.ProjectSecretID */ }
func TestSecretsSetProjectWideAcceptsProviderSecretWithNoList(t *testing.T) { /* P7, design §3a: openrouter-api-key, provider has NO allow_data_to at all: accepted (not refused) — the one case that behaves oppositely to TestSecretsSetProjectWideRefusesEmptyAllowList above for a non-provider name, so both tests exist specifically to pin that the two kinds of name diverge here */ }
func TestSecretsSetRepoScopeAcceptsProviderSecretWithNoList(t *testing.T) { /* B5(a), the third call site: fugaro secrets set openrouter-api-key --repo acme/app (NOT --project-wide), provider has no allow_data_to at all: accepted, not refused -- today's code (AllowsData) would refuse every repo here, which this test would have caught */ }
func TestSecretsLsAddsScopeColumn(t *testing.T) { /* fixture with both a repo-owned and project-shared entry for one name: two distinct SCOPE values appear */ }
func TestSecretsLsProjectWideNeverPrintsValue(t *testing.T) { /* fake Secret Manager's Access never called, same as every existing *NeverPrintsValue test */ }
func TestDiscoverInstallationImportsPreExistingProjectSecret(t *testing.T) { /* S2: a project secret already exists in the fake GCP (created by a --project-wide set run before the installation root's first apply), correctly labelled; assert DiscoverInstallation's Imports includes an importProjectSecret entry for it, so the subsequent plan adopts rather than conflicts with it */ }
func TestDiscoverInstallationRefusesForeignProjectSecret(t *testing.T) { /* S2, the mirror of DiscoverRepo's existing foreign-secret path: a secret exists at the computed ID but its labels don't match -- refused as foreign, never silently adopted or overwritten */ }
func TestDiscoverInstallationNeverShrinksProjectSecrets(t *testing.T) {
	// N3 BLOCKER: a name config-eligible on a FIRST call (fed a project
	// secret ID, discovered as already-existing and labelled) must still
	// appear in the final ProjectSecrets map on a SECOND call where the
	// config no longer makes it eligible at all (list emptied, or the
	// provider/account removed entirely from lc). Assert the map's key
	// set on the second call is a SUPERSET of the first's, never a
	// subset -- the exact property P2 already guarantees for the repo
	// module, extended here to the installation module.
}
func TestDiscoverInstallationSecondOperatorMissingEntryDoesNotShrink(t *testing.T) {
	// The S5 case: a local config with NO claude_accounts/providers/
	// project_secrets entries at all, run against an installation where a
	// FIRST operator's config already created project secrets for several
	// names. Assert the computed map still includes every name the live
	// discovery finds, even though this operator's own lc has nothing
	// that would make any of them eligible.
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestSecretsSetProjectWide|TestSecretsLsAddsScope|TestSecretsLsProjectWide' ./internal/cli/ && go test -count=1 -run 'TestDiscoverInstallation' ./internal/infra/`
Expected: FAIL.

- [ ] **Step 3: Write the Go implementation** (`internal/cli/secrets.go`, `internal/infra/tfvars.go`'s `InstallationSpec.ProjectSecrets` population, `internal/infra/imports.go`'s new kind, `internal/infra/discover.go`'s new import step)
- [ ] **Step 4: Write the Terraform (installation module)**, then validate it offline:

Run: `terraform -chdir=deploy/terraform/gcp/modules/installation fmt -check && terraform -chdir=deploy/terraform/gcp/modules/installation validate` (against a local init with no backend/credentials — the existing pattern this repository's Terraform tests already use; the implementer checks `deploy/terraform/gcp/roots/installation/tests/` for the harness in place).

- [ ] **Step 5: Run the Go tests to verify they pass**

Run: `go vet ./internal/cli/... ./internal/infra/... && go test -count=1 -run 'TestSecretsSetProjectWide|TestSecretsLsAddsScope|TestSecretsLsProjectWide' ./internal/cli/ && go test -count=1 -run 'TestDiscoverInstallation' ./internal/infra/`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/secrets.go internal/cli/secrets_test.go internal/infra/tfvars.go internal/infra/imports.go internal/infra/discover.go internal/infra/*_test.go deploy/terraform/gcp/modules/installation/secrets.tf deploy/terraform/gcp/modules/installation/variables.tf deploy/terraform/gcp/roots/installation/main.tf deploy/terraform/gcp/roots/installation/variables.tf
git commit -m "project secrets task 4: fugaro secrets set/ls --project-wide; the installation root's container and its import/adoption step (S2); a provider's empty allow_data_to is accepted, not refused (owner ruling, design §3a)"
```

---

### Task 5: `local.secrets`, the tfvars wire, and `build.tf`/`check.tf`'s grants rewired through it (P4 — flagged for opus review; B1/B4)

**This task is security- and Terraform-sensitive: a build service account's `secretAccessor` grant (and the daily check job's own secret reference) is being re-pointed from a resource this module creates to a value that may come from a different Terraform root entirely. Route this task's review through opus, not the default reviewer, before merge.**

**Files:**
- Edit: `deploy/terraform/gcp/modules/repo/build.tf`, `workflows.tf`, `check.tf`, `variables.tf`
- Edit: `internal/infra/tfvars.go` (or wherever `RepoSpec`'s JSON tags live, `internal/infra/spec.go`) — the Go side of the tfvars wire
- Edit: `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl`, `deploy/terraform/gcp/roots/repo/tests/testdata/*.tfvars.json` (a new golden fixture with a resolved `project_secrets` entry — **not** producible via `spec_test.go`'s `-update` flag, see below)
- Edit: `internal/infra/discover_test.go` gains one more test (`TestReadinessVarsIncludeProjectSecrets`, listed below, using the file's existing `newCloud(t)` harness) — a `RepoVars`-level check that a `Readiness`-produced `RepoSpec` actually serializes `project_secrets`

**Interfaces:**
- `RepoSpec.ProjectSecrets map[string]string` (`json:"project_secrets"`, Task 3's output field) is the **only** new tfvars key this task wires through — it is Go-computed entirely by `Readiness`, never by this module.
- `workflows.tf` currently computes, inline, `secrets = { for k, s in google_secret_manager_secret.this : k => s.secret_id }` (`workflows.tf:14`) and passes it to each `module "workflow"` call. Hoist this into a `locals` block in the repo module: `locals { secrets = merge({ for k, s in google_secret_manager_secret.this : k => s.secret_id }, var.repo.project_secrets) }`, where `var.repo.project_secrets map(string)` is a new `repo` object field (logical name → an **already-existing** project secret ID, fed straight from `RepoSpec.ProjectSecrets` — never a name this module creates a resource for).
- `build.tf:17` changes from `google_secret_manager_secret.this[each.value].secret_id` to `local.secrets[each.value]`.
- **`check.tf:71` changes identically**, from `google_secret_manager_secret.this[env.value].secret_id` to `local.secrets[env.value]` (B4 — missed in the design's first draft; the check job is a third site, not covered by "build.tf is the one place" as that draft claimed).
- `variables.tf`'s existing precondition (`variables.tf:150-155`, "every secret that build_secrets... name must be one of repo.secrets") is widened to accept a name that is a key of either `repo.secrets` or `repo.project_secrets` — not narrowed: every name P2 already guarantees stays a key of `repo.secrets` too (P2: `rs.Secrets` never prunes), so in practice this precondition's existing form already holds; the widening is defensive, for a future change that might relax P2.

**Tests (corrected per S6/B1: pure Go cannot observe a Terraform `local`'s resolved value, so asserting the three readers *agree* cannot be an `internal/infra` unit test — it must run through the real Terraform-test framework, the same one Task 6 drives. A narrower claim — that `Readiness`'s output actually reaches the tfvars JSON at all, the thing SF2 found missing from this task's fixture plan — IS a plain Go assertion on `RepoVars`'s own output, no Terraform involved, and belongs here too):**
```go
func TestReadinessVarsIncludeProjectSecrets(t *testing.T) {
	// Uses the same newCloud(t)/fake Secret Manager harness as
	// TestReadinessKeepsExistingJob (discover_test.go): seed a project
	// secret with an enabled version and leave the repo copy without one,
	// run DiscoverRepo then Readiness, and call RepoVars on the result.
	// Assert the JSON contains "project_secrets" with the expected
	// logical-name key — not merely that got.ProjectSecrets (the Go
	// struct field, already covered by Task 3's tests) is populated. This
	// is the test that would have caught the original gap: a RepoSpec
	// field with a correct json tag that nothing ever exercises through
	// RepoVars end to end.
}
```
```hcl
# deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl, a new run block added
# to the existing file (reusing its mock_provider and testdata pattern)
run "build_and_check_use_the_resolved_secret" {
  # Using a golden tfvars fixture whose repo.project_secrets has one entry
  # (a logical name both a workflow's secret_env, build_secrets and the
  # check job's secret_env all name — the git credential is the natural
  # choice, since it is in all three sets already). Assert, from the plan's
  # own resource attributes (not Go): the workflow module's job mount, the
  # build account's grant, and the check job's secret_key_ref all resolve
  # to the IDENTICAL secret ID string (the project one, matching
  # repo.project_secrets, not google_secret_manager_secret.this's repo ID).
  # A divergence here means one of the three paths is still reading a
  # stale, repo-only value.
}
```

- [ ] **Step 1: Write the failing test** (add the `run` block above to `repo.tftest.hcl`, the new `TestReadinessVarsIncludeProjectSecrets` Go test below, and the new golden fixture; see "building the fixture," below, for how — reviewing the diff either way)

**Building the fixture (corrected, SF2 — the original draft's instruction to regenerate it via `spec_test.go`'s `-update` flag cannot work: `TestGoldenTfvars` calls `Repo(in)` only, never `Readiness`, and `ProjectSecrets` is populated exclusively by `Readiness` (Task 3); a fixture `Repo()` alone produces can never have a `project_secrets` entry, resolved or otherwise):** take the committed `bitbucket-oauth.tfvars.json` golden (already produced by `TestGoldenTfvars`) and hand-add one `"project_secrets"` entry for `bitbucket-token` (the name already present in that fixture's `secret_env`/`build_secrets`/check sets, per Task 5's own `run` block above), pointing at a plausible `gcp.ProjectSecretID` value — noted with a comment-adjacent `README` line in `testdata/` (JSON has no comments) that this one key is hand-maintained, not `-update`-regenerated, and must be re-added by hand if the fixture is ever regenerated for an unrelated reason. This is sufficient for the Terraform-side `run` block, which only needs some resolved value present in the tfvars to assert all three readers agree on it.
- [ ] **Step 2: Run the test to verify it fails**

Run: `terraform -chdir=deploy/terraform/gcp/roots/repo init -backend=false -lockfile=readonly -input=false && terraform -chdir=deploy/terraform/gcp/roots/repo test -no-color && go test -count=1 ./internal/infra/... -run TestReadinessVarsIncludeProjectSecrets`
Expected: both FAIL — today, `build.tf`/`check.tf` read `google_secret_manager_secret.this` directly, so they'd resolve the repo ID, not the project one the new fixture's `secret_env` expects; `workflows.tf` has no `var.repo.project_secrets` input to even accept yet, so the init step itself may fail first, confirming the field doesn't exist; the Go test fails because `RepoVars`'s output has no `project_secrets` key yet (the field exists from Task 3 but nothing threads it into this fixture's expectations).

- [ ] **Step 3: Write the Terraform and the Go tfvars-shape change** (the `RepoSpec.ProjectSecrets` JSON tag, Task 3's output field, threaded into the tfvars this test's golden fixture encodes; this is also where the hand-built `project_secrets` entry described above gets added to the fixture)
- [ ] **Step 4: Run the test to verify it passes, then the full Terraform validate**

Run: `terraform -chdir=deploy/terraform/gcp/roots/repo test -no-color && go vet ./internal/infra/... && go test -count=1 ./internal/infra/... && terraform -chdir=deploy/terraform/gcp/modules/repo fmt -check && terraform -chdir=deploy/terraform/gcp/modules/repo validate`
Expected: `ok` / no diff / valid.

- [ ] **Step 5: Commit**

```bash
git add deploy/terraform/gcp/modules/repo/build.tf deploy/terraform/gcp/modules/repo/workflows.tf deploy/terraform/gcp/modules/repo/check.tf deploy/terraform/gcp/modules/repo/variables.tf internal/infra/tfvars.go internal/infra/spec.go internal/infra/discover_test.go deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl deploy/terraform/gcp/roots/repo/tests/testdata/
git commit -m "project secrets task 5: local.secrets; build.tf AND check.tf (B4) both rewired through it (opus-reviewed)"
```

---

### Task 6: plan-level fixtures (no secret deletes, but real IAM-member replacements, on a scope-move plan — B3)

**Rewritten, SF3 — the original draft could not be built: `repo.tftest.hcl` (and `installation_terraform_test.go`'s generated `vars.tftest.hcl`) use `mock_provider`/`command = plan` runs whose `assert`/`condition` blocks evaluate planned *values*, never a resource's plan *action* (none of this repo's three existing `.tftest.hcl` files assert `create`/`update`/`replace`/`delete` today — grep confirms the only `action` fields anywhere in them are an artifact-registry cleanup policy's `KEEP`/`DELETE` tag enum, an unrelated homonym). Terraform's native test framework has no HCL-level way to read `resource_changes[].change.actions`, and `tf.Guard` is a Go function (`internal/infra/tf/guard.go`) a `.tftest.hcl` run block cannot call at all. This needs the same machinery `internal/infra/tf` already uses for the three existing root goldens (`cover_golden_test.go`'s `goldenPlan`, real `terraform show -json` output, parsed into `*tf.Plan`), extended with one new, genuinely two-step golden for the scope-move scenario specifically, since every existing golden is a fresh-create plan (`TestGoldenPlansAreCovered` already asserts `actions == ["create"]` for every one of their resource changes) and a replace/delete diff can only come from a plan with a PRIOR state to diff against.**

**Files:**
- Edit: `scripts/gen-golden-plans.sh` — one new golden, `repo-scope-move.plan.json`, produced in two real (not jq-synthesized) steps, offline throughout:
  1. Build a synthetic prior `terraform.tfstate` for the repo root from `repo.plan.json`'s own `planned_values` (the already-committed fresh-create golden, repo-only config: the name has a repo secret, no project one) — a `jq` transform (a sibling of `adopt_variant`, call it `state_from_plan`) that wraps each resource's planned `values` into a minimal v4 state file's `resources[].instances[].attributes`, the same shape a real `terraform apply` of that plan would have written. This is legitimate the same way `adopt_variant` is: it encodes "this already exists," not "what the next diff looks like" — the diff itself is computed for real.
  2. Run a genuine second `terraform plan -refresh=false -state=<that state> -out=plan2.bin` against Task 5's hand-built `project_secrets`-bearing tfvars (the scope-move config: the same name's repo version now disabled/absent, the project copy resolved) — `-refresh=false` means no provider read, so this stays within the script's existing no-network guarantee without new flags. `terraform show -json plan2.bin`, cut to `resource_changes` exactly like every other golden, becomes `repo-scope-move.plan.json`.
- New: `internal/infra/tf/scope_move_golden_test.go` (a sibling of `cover_golden_test.go`, same `goldenPlan` helper, reused not reinvented)

**Tests (named for what is actually true, not "zero destroys" — corrected per B3; written in Go against the real golden plan, not HCL):**
```go
func TestScopeMoveReplacesIAMMembersNeverDeletesSecrets(t *testing.T) {
	p := goldenPlan(t, "repo-scope-move")
	// The scenario in design §6 step 4: a name moves from repo-only to
	// project-shared (repo has no version, project does). Assert, reading
	// p.ResourceChanges directly (rc.Address, rc.Change.Actions):
	//   - 0 "delete"-only actions on any google_secret_manager_secret.this[*]
	//     address, and 0 "replace" ([delete,create] or [create,delete])
	//     on one either — the secret container itself is never destroyed
	//     or recreated (P2/P2-extended);
	//   - EXACTLY the expected google_secret_manager_secret_iam_member.job[*]
	//     (one per workflow/account variant that mounted the name under the
	//     OLD, repo-only config) and, if the name is a BuildSecret,
	//     google_secret_manager_secret_iam_member.build[*] addresses show a
	//     "replace" action — asserting this is PRESENT, not merely
	//     permitted, is the point: a plan that silently kept the OLD
	//     secret_id (a bug) would show no replacement at all and must also
	//     fail this test;
	//   - the workflow's (and, if applicable, the check job's) Cloud Run
	//     job resource itself shows an "update" action, never "replace" or
	//     "delete" (new revision, same resource).
	// Then, the same loaded plan through the real Guard, proving the two
	// facts together rather than asserting Guard's generic behavior again
	// (guard_test.go already covers that against hand-built fixtures):
	//   - tf.Guard(p, nil) is refused, naming every expected address;
	//   - tf.Guard(p, <those addresses>) succeeds.
}
```

- [ ] **Step 1: Write the failing test** (and, first, the new golden: extend `gen-golden-plans.sh` per "Files" above, run it where a provider plugin cache exists, and commit the resulting `testdata/golden/repo-scope-move.plan.json`)
- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -count=1 ./internal/infra/tf/ -run TestScopeMoveReplacesIAMMembersNeverDeletesSecrets`
Expected: FAIL at `goldenPlan`, with no `testdata/golden/repo-scope-move.plan.json` to read — confirming the fixture does not exist yet, not a false pass from stale data.

- [ ] **Step 3: Make it pass** (this task should need no new production code if Tasks 2-5 are correct; it exists to prove the golden claim end to end against a real plan, not a unit-level stand-in)
- [ ] **Step 4: Run it again to verify it passes**

Run the same command as Step 2.
Expected: `ok`, with the replace actions and the `tf.Guard` refusal/success asserted directly, not merely "0 destroys."

- [ ] **Step 5: Commit**

```bash
git add scripts/gen-golden-plans.sh internal/infra/tf/scope_move_golden_test.go internal/infra/tf/testdata/golden/repo-scope-move.plan.json
git commit -m "project secrets task 6: a real two-step golden plan proving no secret deletes, but real IAM-member replacements that tf.Guard catches and --allow-delete clears (B3)"
```

**If no provider plugin cache is available when this task is implemented** (the recurring constraint noted elsewhere for this script, e.g. `docs/plans/2026-10-08-bucket-iam.md:85`): skip regenerating the golden, note "golden plans not regenerated: no provider cache" in the PR body, and leave this task's test uncommitted rather than hand-written — a hand-built `*tf.Plan` fixture would only prove `tf.Guard`'s already-tested generic behavior again, not that the real module wiring produces this scenario's actions, which is the entire point of this task.

---

### Task 7: `fugaro doctor`'s two per-repository checks, plus a project-wide revocation-lag check (S3)

**Files:**
- Edit: `internal/cli/doctor.go`, `internal/cli/doctor_test.go`

**Interfaces:**
- `project-secret-stored`: for every name `config.ReservedSecrets`/a provider's/an account's own list allow-lists this repository for, if neither the repo's own secret nor the project's has an enabled version, red, naming both candidate fix commands.
- `project-secret-scope-stale`: the repository's **last deployed** job's `secret_env` (read the same way `doctor`'s existing image-config checks already read a deployed job's spec) resolves a different scope than `Readiness` would compute right now — a warning (fails only `--strict`), mirroring `doctor`'s existing project-layer shape, "flags a run that launched without it or on an older generation" (`internal/cli/doctor.go:148-149`).
- **`project-secret-extra-accessors` (S3, new, project-wide — not per-repository like the two above; a normal `fugaro doctor` check, no `--project-wide` flag or separate surface, MUST-FIX 4):** for each project secret, read its live IAM policy (`GetIamPolicy`, metadata-level, never `Access`); for each `secretAccessor` member, resolve the repository slug it belongs to — **corrected, small item 12: `gcp.ServiceAccountID` hashes a repository slug into an account ID; a hash cannot be inverted, so this must be computed forward, not "looked up backward" as an earlier draft said** — by computing `gcp.ServiceAccountID(slug, workflow)` for every repository/workflow pair in `localcfg.Config.Repos` (already enumerates every onboarded repository), **and, MUST-FIX 4, extended this round: also `gcp.ServiceAccountID(slug, gcp.WorkflowVariant(workflow, account))` for every (repository, workflow, allowed claude account) combination — a named account's job runs under its own, variant-keyed service account, distinct from the bare workflow's; omitting these from the precomputed set would leave every named account's own accessor unmatched, either missing a real finding or misreporting one** — and matching each live member against that full precomputed set, not by trying to decode the member string itself; flag any accessor whose repository is **not currently** allowed by the name's own eligibility rule (`AllowedForProjectSecret`/`AllowsData` — uniform across all three name sources now, round 4; `ProviderKeyAvailable` no longer exists) — a warning, since this is revocation *lag* (expected until the affected repository's own `fugaro init --repo` catches up), not a failure. Mirrors `noteExtraAccessors` (`internal/infra/discover.go:813`), which only ever compares one repository's own spec against its own secrets' live bindings — this check is the project-wide version nothing today does, since removing a repository from a list or deleting a provider/account leaves its job SA's grant live until that **one** repository's own apply runs again, and no single repository's `init --repo` has reason to look at another repository's bindings.
- **SF9, decided: stays a warning (design §7's own reasoning — red would fail every installation mid-revocation, the expected, transient case), but its message is specified, not left to the implementer:** must end with the literal fix, naming both the repository and the follow-up command — `"re-run fugaro init --repo for <repo> and pass --allow-delete for the address the plan's refusal names"` — not merely "repository X still has access to secret Y." `tf.Guard`'s own refusal message (`internal/infra/tf/guard.go`) is deliberately **not** changed to mention "revocation": it is a feature-agnostic safety primitive shared by every root, and this is the only one of its callers where a delete means that; the doctor warning is the one place that already knows the cause, so that is where the fix belongs.

**Tests:**
```go
func TestDoctorProjectSecretChecksReadNoValue(t *testing.T) { /* fake Secret Manager's Access never called, for all three checks */ }
func TestDoctorFlagsUnstoredProjectSecret(t *testing.T) {
	// S6, fleshed out from a placeholder: a name config.AllowedForProjectSecret
	// (or the owning provider's/account's own AllowsData) lists this
	// repository for, with NEITHER the repo's own secret NOR the project's
	// holding an enabled version (both report 0 versions from the fake
	// Secret Manager's List). Assert the check is red (fails doctor
	// outright, not only --strict) and its message names BOTH candidate
	// fix commands (`fugaro secrets set NAME --repo X` and `... --project-wide`),
	// not just one.
}
func TestDoctorFlagsStaleScope(t *testing.T) { /* deployed job still mounts the repo ID; Readiness would now resolve the project ID (repo version was deleted since the last apply): warning, not red */ }
func TestDoctorFlagsExtraProjectSecretAccessor(t *testing.T) {
	// S3: a project secret's live IAM policy carries a secretAccessor
	// member for a repository that project_secrets'/the provider's/the
	// account's current allow_data_to no longer lists. Assert a warning
	// (not red, SF9 -- this is expected revocation lag, not a failure)
	// names the repository and the secret -- not the value, not the
	// service account's raw email beyond what GetIamPolicy already
	// returns -- AND, SF9's own fix, that the warning text contains both
	// "fugaro init --repo" and "--allow-delete", so the operator reading
	// it needs no cross-referencing to act on it.
}
func TestDoctorProjectSecretExtraAccessorsReadsNoValue(t *testing.T) { /* GetIamPolicy only, never Access -- the project-wide check's own mutation-proof case, since it is new code this round */ }
func TestDoctorFlagsExtraAccessorForNamedAccountVariant(t *testing.T) {
	// MUST-FIX 4: a named Claude account's own job (gcp.ServiceAccountID(
	// slug, gcp.WorkflowVariant(workflow, account)), not the bare
	// workflow's service account) holds a secretAccessor grant the
	// account's current allow_data_to no longer lists. Assert this is
	// flagged too -- the test that would catch a precomputed set built
	// only from bare repository/workflow pairs, which would leave every
	// named account's own accessor unmatched (and either silently miss
	// this finding, or -- worse -- misreport an innocent variant account
	// as an unknown/extra one it cannot resolve at all).
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestDoctorProjectSecret|TestDoctorFlagsUnstoredProjectSecret|TestDoctorFlagsStaleScope|TestDoctorFlagsExtraProjectSecretAccessor|TestDoctorFlagsExtraAccessorForNamedAccountVariant' ./internal/cli/`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/cli/... && go test -count=1 -run 'TestDoctor' ./internal/cli/`
Expected: `ok` (the full `TestDoctor*` set, to catch any regression from the edit).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/doctor.go internal/cli/doctor_test.go
git commit -m "project secrets task 7: fugaro doctor's two per-repository checks, plus a project-wide revocation-lag check (S3)"
```

---

### Task 8: the CLI's own build/check/doctor paths resolve the real secret ID, not always the repo one (N1, plus a fifth call site, small item 5); the apply-time-vs-live mismatch (B1); the check job's own base-image version skew (B2)

**This task did not exist in the design's first two drafts: `fugaro image build`, `init`'s first build, the in-job daily check's own rebuild, and `doctor`'s GitHub App check all still name the repo-scoped secret ID unconditionally, with no idea `Readiness` may have resolved that logical name to the project copy instead. Left unfixed, the moment `bitbucket-token`/`github-app-key` moves to project scope (design §6 step 4a), every build and every daily check fails, silently, the next time either runs. A round-4 review found two further blocking gaps in this task's first draft; a round-5 review then found the round-4 fixes themselves could not be built as written. Both corrected below: (1) B1's fix cannot compare against a deployed job, since the most common case (a first build) has no deployed job to compare against; (2) B2's gate cannot live in `internal/infra` (an import cycle) or compare a build record's own submitting-CLI version (the wrong version entirely).**

**Files:**
- Edit: `internal/infra/readiness.go` (the reusable resolve helper, extracted from `Readiness`'s own per-logical-name precedence check; `CheckJobSpec.GitSecretID` population and the `FUGARO_CHECK_SPEC` re-marshal — **no gate here any more, B2's correction: that check cannot live in this package**)
- Edit: `internal/infra/spec.go` (`CheckJobSpec.GitSecretID` field)
- Edit: `internal/infra/discover.go` or a new `internal/infra/grants.go` (**B1's corrected mechanism:** a small, exported helper reading `GetIamPolicy` on one or more candidate secret IDs and reporting which one, if any, grants `roles/secretmanager.secretAccessor` to a given member — the identical call `DiscoverRepo`'s own `secretPolicies` population already makes, `discover.go:567`, factored out so this task's new call sites and that existing one share it rather than duplicating the `GetIamPolicy` call)
- Edit: `internal/cli/image.go` (`cloudBuildSpec` takes a `gitSecretID string` parameter instead of computing it from `rs.Secrets[ws.GitSecret]`; `submitAndWait` and `runImageBuildCloud` — **named explicitly, a third caller an earlier draft omitted** — both resolve live and compare against the grant, B1)
- Edit: `internal/cli/imagecheck.go` (`submitRebuild` passes `s.GitSecretID`, read from the already-resolved `CheckJobSpec`, never recomputed; **the `""` fallback, B2 — unaffected by this round's B1/B2 corrections, already correct**)
- Edit: `internal/cli/init.go` (`planRepo`'s signature gains the resolved `ready` as a return value, threaded to `buildFirst`/`submitAndWait` with **no live comparison at all**, B1's other half; `appPreCheck`, `init.go:2114` — **small item 5, a fifth hard-coded-repo-ID call site missed by the design's first drafts**, resolves live and compares against the grant like `doctor.go`'s; **B2's gate itself lives here too**, in `planRepo`, after `Readiness` returns `ready` and before `t.Apply`)
- Edit: `internal/cli/doctor.go` (the GitHub App check, `checkGitHubApp`, resolves live too, instead of hard-coding `gcp.SecretID(slug, "github-app-key")`, **and compares against the grant, B1**)
- Edit: `internal/cli/githubapp.go` (**small item 5, second half:** `checkGitHubApp`'s own "Fix" messages, `githubapp.go:134,143`, gain a warning when `--repo`/plain `secrets set` would override a name this repository currently gets from the project copy)
- Edit: `internal/cli/recipes_skew.go` (a new `gitSecretIDSince` `*Since` constant, next to `recipesSince`/`layeredSince` — **B2, a genuine, narrow exception to P6**)
- Edit: `internal/cli/init_fugaroyaml.go` (no new function needed — **B2 reuses `baseRefReleaseRE`/`imagePredates` directly**, the same pair `baseRefReason` already calls for `gcpProjectFieldSince`; mention this reuse in the commit message so a reviewer checks it is not quietly duplicated instead)
- Test: `internal/infra/readiness_resolve_test.go` (new — the extracted helper's own tests), `internal/infra/grants_test.go` (new, or folded into `discover_test.go` next to `secretPolicies`'s own tests), `internal/cli/image_test.go`, `internal/cli/imagecheck_test.go`, `internal/cli/doctor_test.go`, `internal/cli/init_test.go`, `internal/cli/githubapp_test.go`

**Interfaces:**
```go
// internal/infra/readiness.go, next to Readiness
// ResolveSecretID is the one place Readiness's own repo-wins-else-project
// precedence lives, extracted so the CLI's own live call sites (image
// builds, doctor) can resolve a single logical name without going through
// a full Readiness pass. repoID is always checked first; projectID is
// checked only if repoID has no enabled version, projectID == "" meaning
// no candidate exists for this repo (not eligible at all) -- in which
// case repoID is returned regardless of its own version state, the same
// as today's unconditional repo-only behavior. Readiness's own loop (§5)
// calls this once per logical name instead of duplicating the two
// hasEnabledVersion calls inline.
func ResolveSecretID(ctx context.Context, c *Clients, repoID, projectID string) (id string, hasVersion bool, err error)

// GrantedSecretID (B1, corrected mechanism, internal/infra/grants.go or
// discover.go) reports which of ids (repoID, projectID -- projectID may be
// "") currently grants member (rs.BuildServiceAccountEmail, typically)
// secretAccessor, reading GetIamPolicy on each in turn -- the identical
// call DiscoverRepo's own secretPolicies population already makes
// (discover.go:567). "" (no error) when neither does: a state that is
// only valid before any apply has ever granted either, not a case the
// caller should treat as a refusal by itself.
func GrantedSecretID(ctx context.Context, c *Clients, member string, ids ...string) (id string, err error)
```
- `cloudBuildSpec(rs infra.RepoSpec, cfg *config.Config, name, base, machineType, bucket, gitSecretID string) (gcp.BuildSpec, error)` — `gitSecretID` replaces the internal `rs.Secrets[ws.GitSecret]` read; `GitSecretID: gitSecretID` in the returned `gcp.BuildSpec`.
- `CheckJobSpec` (`internal/infra/spec.go:183`) gains `GitSecretID string `json:"git_secret_id"``, populated by `Readiness`'s check-job branch (§5) via the same `ResolveSecretID` call, which then **re-marshals** `out.Check.Env[CheckSpecEnv]` with the updated `CheckJobSpec` before returning — the one piece of `Readiness` logic that touches an env var's *value* rather than only which secret ID it mounts.
- `planRepo`'s signature gains the resolved spec as a return value: `func (r *initRun) planRepo(...) (infra.RepoSpec, []infra.Missing, error)` (was `([]infra.Missing, error)`) — its two existing call sites (`init.go:2001,2039`) capture it; `offerBuilds`/`buildFirst`/`submitAndWait` take that `ready` spec instead of the pre-`Readiness` `spec` they use today.

**B1, corrected this round — a live resolve at build time can disagree with the apply-time choice the build account's own IAM grant was fixed for, but comparing against a *deployed job* (the previous round's fix) cannot be built: the most common case, a first build, has no deployed job to compare against.** `planRepo`'s own apply already resolves and grants `ready`'s choice by the time it returns — `offerBuilds`/`buildFirst` currently call `submitAndWait` with the **pre-`Readiness`** `spec` instead (`init.go`'s flow: `spec, err = infra.Repo(in)` → `planRepo(..., spec, ...)` computes and applies a local `ready`, discarded → `offerBuilds(ctx, lc, buildCfg, spec, names)`, still the old `spec`) — for a workflow whose job does **not exist yet**, the exact case `buildFirst` exists for, there is no live job at all to read a `secret_key_ref` from (`readiness.go:121`'s own existing-job branch never ran for it either). `fugaro image build --repo X --workflow W` run for that same new workflow, independently of `init`, has the identical problem. **Fix, two mechanisms for two call shapes:**
- **Inside `init`'s own flow: no live comparison at all.** Thread the apply-time `ready` out of `planRepo` (signature change above) instead of discarding it; `buildFirst`'s call to `submitAndWait` uses `ready.ProjectSecrets[ws.GitSecret]` if present, else `ready.Secrets[ws.GitSecret]` — the exact precedence `Readiness` already decided and the apply that just ran already granted, read back rather than re-derived or re-checked live.
- **For a standalone invocation with nothing just applied** (`fugaro image build --repo X --workflow W`, `fugaro image refresh` — i.e. `runImageBuildCloud`, `internal/cli/image.go:156`, calling `cloudBuildSpec` at `image.go:232`, a **third caller an earlier draft of this task never named** — and `doctor`'s/`init`'s GitHub App checks): resolve live via `ResolveSecretID(ctx, c, rs.Secrets[ws.GitSecret], rs.ProjectSecretCandidates[ws.GitSecret])`, and separately call `GrantedSecretID(ctx, c, rs.BuildServiceAccountEmail, rs.Secrets[ws.GitSecret], rs.ProjectSecretCandidates[ws.GitSecret])` to find which ID **the build account's own grant** currently names — not a deployed job, which may not exist. Agreement (live resolve == granted ID): proceed. Disagreement: refuse, naming both IDs and telling the operator to run `fugaro init --repo` first. `checkGitHubApp`'s two callers (`doctor.go:401`, `init.go:2114`'s `appPreCheck` — **small item 5's fifth call site**, identical hard-coded-repo-ID bug, fixed the same way) do the identical live-vs-granted comparison before constructing `appKeySource{SecretID: ...}` (a mismatch there is a misleading diagnostic, not an access error, but the fix is the same shape).
- `submitRebuild` (`internal/cli/imagecheck.go:710`, in-job) is **not** affected by B1 at all: it already reads `s.GitSecretID` from the apply-time-resolved `CheckJobSpec`, never a live resolve, so there is no live-vs-granted drift possible for the in-job path by construction.
- **Small item 5, second half (unaffected by this round's B1 correction, carried over): `checkGitHubApp`'s own "Fix" messages (`internal/cli/githubapp.go:134,143`) unconditionally suggest a `--repo` override, and plain `fugaro secrets set NAME` already defaults to repo scope (`resolveSecretRepo(cmd, env, "")`) — if `github-app-key` is currently resolving from the project copy for this repository, following either suggested command creates a repo override that wins forever (§5, "repo overrides project" has no override-the-override), the identical footgun N2 closes for the secrets stage's own prompt.** Both Fix strings gain a one-line warning when `infra.ProjectSecretCandidate` reports this repository eligible for the project copy, naming `--project-wide` as the alternative — never silently omitted.

**B2, corrected this round — the daily check job's own rebuild has a real, narrow version-gate exception to P6's "no `*Since` constant" decision, but the previous round's gate mechanism was wrong in three separate ways and had to be replaced, not patched.** It compared `imagecheck.Record.FugaroVersion` — the version of the `fugaro` **CLI that submitted this repository's own derived-image build** (`internal/cli/image.go:541`), not the `fugaro` baked into the **base** image the check job runs (`spec.go:857`, `Image: bases[...]`) — an entirely different, irrelevant version. It placed the gate inside `internal/infra.Readiness`, which **cannot import `internal/cli`** (`gitSecretIDSince`/`imagePredates` live there, and `internal/cli` already imports `internal/infra` — a cycle). And it described gating "a `fugaro secrets set` operation that would disable the repo version" — **no such operation exists**: §6 step 4a's disable is a raw `gcloud` command outside fugaro's control (SF5), so there is nothing for a gate to intercept there. **Fix, reusing an existing, shipped mechanism rather than inventing a new one:** the check job's own base image reference is a plain string already sitting in `ready.Check.Image` after `Readiness` runs — parseable the exact way `baseRefReason` (`internal/cli/init_fugaroyaml.go:377-392`) already parses a base ref string to judge its age for a *different* `*Since` constant, `gcpProjectFieldSince`, using `baseRefReleaseRE`/`imagePredates` (`init_fugaroyaml.go:369`, `:133`). No live call, no build record, no import cycle: in `planRepo` (`internal/cli`), **after** `Readiness` returns `ready` but **before** `t.Apply`:
- When `ready.ProjectSecrets[gitSecret] != ""` (the repo copy has no version; resolution already fell through to the project copy) **and** `ready.Check.Image`'s parsed release predates `gitSecretIDSince`: **refuse the run outright, before writing tfvars or applying anything** — name both fixes (a newer base image, `fugaro image refresh`/`base_images`; or re-enabling the repo-scoped version so resolution falls back to it). This is a genuine refusal, not a soft fallback: `local.secrets` is one repo-wide Terraform local every one of `job.tf`/`sa.tf`/`build.tf`/`check.tf` reads from, so the check branch cannot resolve a *different* value than the workflow jobs do for the same logical name — and the gate's own precondition (the repo copy has no version at all) means "keep the check branch on the repo ID" would mount a secret with zero versions, not a safe fallback.
- When `ready.Check.Image`'s ref does not parse as a release at all (`baseRefReleaseRE` finds no match — a custom or dev base): **warn, don't refuse** — the version is genuinely unknown, the identical distinction `baseRefReason` already draws between `reasonBaseRefOld` (refuse-worthy) and `reasonBaseRefDev` (warn-worthy) for its own constant.
- **The `""` fallback (unaffected by this round's correction, carried over from the previous round, still correct):** a newer binary decoding a stale, pre-this-feature spec gets `s.GitSecretID == ""`; `submitRebuild` falls back to `rs.Secrets[ws.GitSecret]` (still always computed by `infra.Repo()`, unconditionally, P2), recovering this separate case with no operator action needed.

**Tests:**
```go
func TestResolveSecretIDPrefersRepoWhenVersioned(t *testing.T) { /* repoID has a version: returned, hasVersion true, even when projectID also has one -- precedence, matching Readiness's own rule */ }
func TestResolveSecretIDFallsBackToProject(t *testing.T) { /* repoID has none, projectID has one: projectID returned, hasVersion true */ }
func TestResolveSecretIDNoCandidateStaysRepoOnly(t *testing.T) { /* projectID == "": repoID returned regardless of its own version state -- byte-for-byte today's behavior for a non-eligible name */ }
func TestResolveSecretIDNeitherHasVersion(t *testing.T) { /* repoID returned (today's existing-error-path default), hasVersion false, so the caller can report a Missing-style error the same way it already does */ }
func TestGrantedSecretIDFindsRepoGrant(t *testing.T) { /* fake Secret Manager: repoID's policy grants rs.BuildServiceAccountEmail secretAccessor, projectID's does not (or projectID == ""): assert repoID returned */ }
func TestGrantedSecretIDFindsProjectGrant(t *testing.T) { /* the reverse: projectID's policy grants it, repoID's does not: assert projectID returned */ }
func TestGrantedSecretIDNeitherGranted(t *testing.T) { /* neither policy names the member: assert "" returned, no error -- the pre-first-apply state, not a refusal by itself */ }
func TestCloudBuildSpecUsesPassedGitSecretID(t *testing.T) { /* image.go: cloudBuildSpec's GitSecretID equals the parameter, never rs.Secrets[ws.GitSecret] directly -- catches a regression back to the old internal lookup */ }
func TestPlanRepoThreadsReadyToFirstBuild(t *testing.T) { /* B1: a brand-new workflow, no job deployed yet, repo copy has no version, project copy does (already stored before this init run, an edge case within reach even for a first build): assert planRepo returns ready with ProjectSecrets[git] set, and that the subsequent first build (buildFirst/submitAndWait) uses that value with NO live GrantedSecretID/ResolveSecretID call of its own for this purpose -- proves the apply-time value is read back, not re-resolved */ }
func TestRunImageBuildCloudResolvesLiveAndMatchesGrant(t *testing.T) { /* standalone fugaro image build: repo copy has no version, project copy does, and the build account's grant (from a prior apply) already names the project ID: assert the submitted Cloud Build request's AvailableSecrets reference the PROJECT id, no refusal (live and granted agree) */ }
func TestRunImageBuildCloudRefusesOnGrantMismatch(t *testing.T) { /* B1: a fresh repo override stored after the last apply (live resolve now prefers the repo ID), but the build account's grant still names the project ID (unchanged since the last apply). Assert refusal, naming both IDs and "fugaro init --repo", and that NO Cloud Build request is ever submitted */ }
func TestCheckJobSpecCarriesResolvedGitSecretID(t *testing.T) { /* Readiness's check branch, repo has no version, project does: assert the re-marshaled FUGARO_CHECK_SPEC's GitSecretID is the project ID, and that out.Check.Env[CheckSpecEnv] actually changed (not just a field nobody reads) -- Readiness itself still does this resolution; only the B2 GATE moves to the CLI, not this */ }
func TestPlanRepoRefusesProjectScopeOnOldBaseImage(t *testing.T) { /* B2: ready.ProjectSecrets[git] set (repo copy has no version), ready.Check.Image a release ref that predates gitSecretIDSince. Assert planRepo refuses BEFORE writing tfvars or applying, naming both fixes (image refresh, re-enable the repo version) */ }
func TestPlanRepoWarnsOnUnparseableBaseImage(t *testing.T) { /* B2: identical setup, but ready.Check.Image is a custom/dev ref baseRefReleaseRE does not match. Assert planRepo WARNS, proceeds with the apply -- not a refusal, the age is genuinely unknown */ }
func TestSubmitRebuildUsesCheckJobSpecGitSecretIDNeverRecomputes(t *testing.T) { /* in-job path: s.GitSecretID set to a deliberately-distinct fake value; assert the submitted build request uses exactly that value, not rs.Secrets[ws.GitSecret] (which infra.Repo() alone would have produced) -- proves the in-job code no longer recomputes */ }
func TestSubmitRebuildFallsBackOnEmptyGitSecretID(t *testing.T) { /* s.GitSecretID == "" (simulating a stale, pre-feature spec): assert the submitted build request uses rs.Secrets[ws.GitSecret] (the repo ID infra.Repo() computes), and that the rebuild succeeds rather than resolving to no secret at all */ }
func TestDoctorGitHubAppCheckResolvesLive(t *testing.T) { /* repo copy has no version, project copy does, correctly labelled, the build account's grant already matches: assert checkGitHubApp reads the PROJECT secret's value, never the repo one's absent version */ }
func TestDoctorGitHubAppCheckRefusesOnGrantMismatch(t *testing.T) { /* B1's doctor case: live resolve and the build account's actual grant disagree. Assert checkGitHubApp reports the mismatch (naming both IDs and fugaro init --repo) rather than silently reading whichever resolves live */ }
func TestInitAppPreCheckResolvesLiveNotAlwaysRepoID(t *testing.T) { /* small item 5: appPreCheck (init.go:2114) is a fifth call site; repo copy has no version, project copy does, grant already matches: assert appPreCheck's own checkGitHubApp call resolves the PROJECT secret, mirroring TestDoctorGitHubAppCheckResolvesLive for this separate call site */ }
func TestInitAppPreCheckRefusesOnGrantMismatch(t *testing.T) { /* appPreCheck's own mismatch case, distinct from doctor's: a fresh repo override stored with no apply in between, the build account's grant still names the project ID. Assert appPreCheck reports the mismatch (naming both IDs and fugaro init --repo) the identical way TestDoctorGitHubAppCheckRefusesOnGrantMismatch does for doctor's own call site -- both callers share checkGitHubApp but each does its own live-vs-granted comparison before calling it, so each needs its own test, not just the happy-path one above */ }
func TestCheckGitHubAppFixWarnsOnProjectCoveredName(t *testing.T) { /* small item 5, second half: github-app-key currently resolves from the project copy for this repo (infra.ProjectSecretCandidate reports it eligible, project copy has a version, repo copy does not). Trigger a Fix message (a bad/parse-error key) and assert the returned Fix string contains both "--repo" and a warning naming the project-wide copy and --project-wide as the alternative -- not merely the bare "secrets set github-app-key --repo ..." command an earlier draft of this check always returned. */ }
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestResolveSecretID|TestGrantedSecretID|TestCloudBuildSpecUsesPassedGitSecretID|TestPlanRepoThreadsReadyToFirstBuild|TestRunImageBuildCloudResolvesLiveAndMatchesGrant|TestRunImageBuildCloudRefusesOnGrantMismatch|TestCheckJobSpecCarriesResolvedGitSecretID|TestPlanRepoRefusesProjectScopeOnOldBaseImage|TestPlanRepoWarnsOnUnparseableBaseImage|TestSubmitRebuildUsesCheckJobSpecGitSecretIDNeverRecomputes|TestSubmitRebuildFallsBackOnEmptyGitSecretID|TestDoctorGitHubAppCheckResolvesLive|TestDoctorGitHubAppCheckRefusesOnGrantMismatch|TestInitAppPreCheckResolvesLiveNotAlwaysRepoID|TestInitAppPreCheckRefusesOnGrantMismatch|TestCheckGitHubAppFixWarnsOnProjectCoveredName' ./internal/infra/ ./internal/cli/`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/infra/... ./internal/cli/... && go test -count=1 ./internal/infra/... ./internal/cli/...`
Expected: `ok` (the full packages, since this task changes two shared function signatures, `cloudBuildSpec` and `planRepo`, with several existing callers to re-check).

- [ ] **Step 5: Commit**

```bash
git add internal/infra/readiness.go internal/infra/spec.go internal/infra/discover.go internal/infra/grants_test.go internal/cli/image.go internal/cli/imagecheck.go internal/cli/doctor.go internal/cli/init.go internal/cli/githubapp.go internal/cli/recipes_skew.go internal/infra/readiness_resolve_test.go internal/cli/image_test.go internal/cli/imagecheck_test.go internal/cli/doctor_test.go internal/cli/init_test.go internal/cli/githubapp_test.go
git commit -m "project secrets task 8: image builds, the daily check, doctor's and init's GitHub App checks resolve the real secret ID (N1, small item 5); compare against the build account's own grant, not a possibly-nonexistent deployed job (B1); gate on the check job's own base image ref in the CLI, not a build record in infra (B2)"
```

---

### Task 9: `fugaro init --repo`'s secrets stage stops ignoring the project copy (N2)

**This task did not exist in the design's first two drafts: the secrets stage (`internal/cli/initsecrets.go`) decides a name is "missing" by checking only the repo-scoped ID's version state — it has no idea a project copy might already have one. At a terminal this prompts the operator for a value they don't need to re-enter (storing it into the repo copy, which then wins forever — a silent precedence flip, and exactly the re-entry this whole feature exists to remove); without a terminal (`--yes`/CI) it returns `NeedsYou` and blocks the init. Under §3a, this also means `init` prompts every `agent.auth: api-key` repository for a no-list provider's key, even though that key is already available project-wide.**

**Files:**
- Edit: `internal/cli/initsecrets.go`, `internal/cli/initsecrets_test.go`

**Interfaces:**
- `(s *secretsStage) find(ctx context.Context, st secretStore) ([]string, error)` (`initsecrets.go:361-371` today: lists `st.List` under `{gcp.LabelRepo: s.tg.label}` and checks each wanted name's repo ID alone) gains a second `List` call, under `{gcp.LabelProject: s.e.lc.Name}` (the same, already-granted project-wide metadata read `fugaro secrets ls --project-wide` uses, §4 — no new IAM), and for each `name` in `s.tg.wanted`: resolve `infra.ProjectSecretCandidate(s.e.lc, name, s.tg.repo)` (Task 2's exported helper); if `ok` and the project list shows that ID with `Latest != ""`, the name is **not** missing, regardless of the repo copy's own state. Only a name with neither a project candidate-with-a-version nor its own repo version is added to `out` (today's existing rule, unchanged for every non-project-shareable name — an installation with no `project_secrets`/no provider/no claude account configured sees byte-for-byte no change, since `infra.ProjectSecretCandidate` then always returns `ok == false`).
- The prompt/store path itself (`set`, `initsecrets.go:574`-ish) is **unchanged**: a name genuinely missing everywhere still prompts and stores to the repo copy, exactly as today — that is the correct, legitimate bootstrap case (nothing stored anywhere yet), not a bug. The fix is entirely in `find()` correctly recognizing "already available via the project copy" as not missing in the first place, so `set()` is never reached for such a name at all.

**Tests:**
```go
func TestInitSecretsFindSkipsNameWithProjectVersion(t *testing.T) { /* a project-shared name (any of the three kinds) has a project-copy version, repo copy has none: find() does not include it in "missing" */ }
func TestInitSecretsDoesNotPromptForProjectCoveredName(t *testing.T) { /* end-to-end: canPrompt() true, but the stage never calls readSecret/set for the project-covered name -- asserted via a fake prompt reader that must never be invoked for that one name, the same mutation-proof pattern every other *NeverPrintsValue-style test in this plan already uses */ }
func TestInitSecretsPrecedenceUnchangedWhenRepoAlreadyStored(t *testing.T) { /* repo copy already has a version too: find() does not treat it as missing either (today's existing behavior) -- a regression guard that this fix does not accidentally make the project copy look preferred */ }
func TestInitSecretsProviderNoListNeverPromptsAnyRepo(t *testing.T) { /* §3a: a provider configured with no allow_data_to, with a value already stored --project-wide: assert find() does not list that provider's secret as missing for ANY repository's init --repo, not just a listed one */ }
func TestInitSecretsStillPromptsWhenNothingStoredAnywhere(t *testing.T) { /* the legitimate bootstrap case: neither repo nor project has a version -- find() still lists it as missing, canPrompt()/set() behavior is byte-for-byte unchanged */ }
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestInitSecretsFindSkips|TestInitSecretsDoesNotPrompt|TestInitSecretsPrecedenceUnchanged|TestInitSecretsProviderNoListNeverPrompts|TestInitSecretsStillPrompts' ./internal/cli/`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/cli/... && go test -count=1 -run 'TestInitSecrets' ./internal/cli/`
Expected: `ok` (the full `TestInitSecrets*` set, to catch any regression in the existing secrets-stage tests).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/initsecrets.go internal/cli/initsecrets_test.go
git commit -m "project secrets task 9: init --repo's secrets stage consults the project copy before calling a name missing (N2)"
```

---

### Task 10: Docs, cross-references, Check 40, the release checklist bullet

**Files:**
- **Edit: `docs/design/v1.md`** (one cross-reference sentence, §5.4 local config area, no rewrite) **and `docs/design/v1.md:717`'s own `allow_data_to` example comment (MUST-FIX 3, a places-to-update gap): `# repositories whose code may be sent here; none when absent` becomes `# repositories whose code may be sent here; every repository when absent or ["*"]` — stale since §3a's final ruling, the identical fix Task 10's own `multi-model.md:30` bullet already makes.**
- **Edit: `docs/design/m10-multi-model.md`** (MUST-FIX 3, missing from this list entirely until this round): `:55`'s `allow_data_to` example comment, `# Fugaro-project/repository slugs allowed to send code here; absent = none`, becomes `# ...; every repository when absent or ["*"]` — the identical stale claim as `v1.md:717`/`multi-model.md:30`. `:130`'s threat-model table row, `"Repository sends code to a provider the owner did not approve | allow_data_to ceiling; refused at bootstrap"`, is **not simply wrong, it is historical**: true as designed in M10 (this row predates project-level secrets entirely), false once a provider is configured with an absent/`["*"]` list under this feature. Add a dated footnote under the table (not a rewrite of the row itself, which still describes the listed case correctly): "Updated 2026-10-13 (project-secrets, §3a): an absent or `[\"*\"]` `allow_data_to` is now a deliberate exception to this row, not a bootstrap refusal — see `docs/design/project-secrets.md` §3a."
- **Edit: `docs/multi-model.md` — a one-paragraph note that a provider's key may now also be stored `--project-wide`, with a link, AND four existing lines corrected for the owner's final ruling (round 4, and round 5's `[]`-refusal ruling; these are pre-existing, already-published doc claims that are simply wrong once Task 1 ships, not new content this feature adds):**
  - `multi-model.md:30`'s `allow_data_to` example comment, `# repositories whose code may go here; none when absent`, becomes `# repositories whose code may go here; every repository when absent or ["*"] (an explicit [] is refused)` — **corrected this round, MUST-FIX 2: "empty" dropped from the exception list, since an explicit empty list is no longer a third meaning at all, it is refused by `ValidateModelProviders`.**
  - `multi-model.md:41`'s `"A repository not in allow_data_to gets neither, and a run on it that names a provider model is refused"` needs an explicit exception clause for the absent/`["*"]` case — every repository gets both the mount and the routing then, not refusal.
  - `multi-model.md:55`'s `"It is refused for a repository the provider does not list in allow_data_to"` (§3, the key-storage section) needs the same exception: refused only when the list is a real, non-empty, non-wildcard list that excludes the repository.
  - `multi-model.md:78`'s refusal-reasons bullet, `"No provider claims the model, or the provider's allow_data_to does not name this repository"` (§5), needs the identical qualifier — this reason only fires for a real, non-empty, non-wildcard list.
- **`docs/gcp-live-checklist.md`'s Check 40 (40a-40j, design §8) is already added — small item 12, corrected: an earlier draft of this bullet described it as this task's own future work, but reserving the check number and writing its not-yet-run steps is itself docs-only, so this very PR already did it (the section, the row, and the "Numbering ruling" update naming 40 as taken all already exist in the file at this PR's head).** This task's own remaining work on the checklist, once the code above ships, is only to **fill in** the `FACT:`/owner-run results the section already has placeholders for — not to write the section itself again.
- Edit: `docs/release.md` — **two** bullets, not one (corrected this round, B2: P6's narrow exception means this feature DOES add one `*Since` constant after all):
  1. A plan-shape bullet, in the same family and immediately alongside the existing `gcpProjectFieldSince`/`recipesSince`/`layeredSince`/`namedTokensSince` bullets (`docs/release.md:55-58`, design §7): confirm the release note for the shipping version names the owner's migration sequence (design §6), and that an installation applying the `installation` root for the first time after this feature ships sees the new `secrets.tf` resources as **creates only** — never an update to an unrelated resource (a regression elsewhere in the module would show as an unexpected diff there). This one names no `*Since` Go constant — it is a plan-shape check, not a constant-equality one, and its wording should say so rather than mimic the others' "verify `X` equals Y" phrasing where it would be inaccurate.
  2. **A normal `*Since`-equality bullet, in the exact format every other entry in that list already uses** (e.g. `"**Verify `namedTokensSince`** (`internal/cli/recipes_skew.go`) equals the release that ships `task.Spec.OAuthToken`..."`): **Verify `gitSecretIDSince`** (`internal/cli/recipes_skew.go`) equals the release that ships `imagecheck.go`'s awareness of `CheckJobSpec.GitSecretID` (Task 8, B2). `fugaro init --repo` gates moving the git credential to project scope on it, parsing the check job's own base image ref the same way `gcpProjectFieldSince`'s own check already parses a base ref — a wrong value refuses too late (an old base image's daily rebuild still silently fails) or too early (a repository that could safely move stays blocked).
- **Edit: `docs/gcp-setup.md:515`'s shared-config sentence (S6, a gap in an earlier draft's Task 8 file list) — add `project_secrets` to the list of things `fugaro/config.yaml` never holds, alongside the existing `providers:` it already names** ("It never holds the `terraform:` section, `user:`, `endpoints:`, `providers:`, a repository's `base_branch`... or any secret"): this design's own §7 already states `project_secrets` is local-config-only, never published — this doc should say so in the one place it already makes the identical claim for `providers:`.
- New/extended test in whichever file already asserts the checklist's numbering/doc shape (the implementer greps for a test covering the "Numbering ruling" claims, if one exists; otherwise this task adds none beyond the existing docs-lint suite)

- [ ] **Step 1: Write the docs edits**
- [ ] **Step 2: Run the docs tests**

Run: `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m`
Expected: `ok`.

- [ ] **Step 3: Commit**

```bash
git add docs/design/v1.md docs/multi-model.md docs/design/m10-multi-model.md docs/gcp-live-checklist.md docs/release.md docs/gcp-setup.md
git commit -m "project secrets task 10: docs cross-references, Check 40, the release checklist bullet, gcp-setup.md's shared-config list (S6), m10-multi-model.md's stale allow_data_to claims (MUST-FIX 3)"
```

---

### Task 11: Full suite

- [ ] **Step 1: Full focused suite**

Run (foreground, split by package if any call would exceed 9 minutes):
```bash
go vet ./...
gofmt -l . # expect no output
go test -race -count=1 ./internal/config/... ./internal/localcfg/... ./internal/backend/gcp/... ./internal/infra/... ./internal/cli/...
go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m
terraform -chdir=deploy/terraform/gcp/modules/installation fmt -check
terraform -chdir=deploy/terraform/gcp/modules/repo fmt -check
terraform -chdir=deploy/terraform/gcp/roots/installation init -backend=false -lockfile=readonly -input=false && terraform -chdir=deploy/terraform/gcp/roots/installation test -no-color
terraform -chdir=deploy/terraform/gcp/roots/repo init -backend=false -lockfile=readonly -input=false && terraform -chdir=deploy/terraform/gcp/roots/repo test -no-color
```
Expected: `ok` everywhere, no `gofmt`/`terraform fmt` output, `Success!` from both `terraform test` runs (Task 6's B3 fixtures, re-run here as the final cross-check).

- [ ] **Step 2: Commit**

```bash
git commit -m "project secrets task 11: full suite"
```

---

### Task 12: Owner-run live check (not part of the PR; `fugaro`/sandbox installations only, never `belong`)

This task is never run by an implementer or by CI. Paste the `FACT:` lines into the PR that eventually implements Tasks 1-11 and into `docs/gcp-live-checklist.md` itself, under Check 40.

**This list is kept in sync with `docs/gcp-live-checklist.md`'s own Check 40 section by hand, round by round — it must-fix a review caught drifting stale after round 5's B1/B2 rework (an earlier version of this list still used "BLOCKING-1/BLOCKING-2," described the abandoned deployed-job comparison, and repeated the stale provider-emptying framing for 40j; pasting that version's `FACT:` lines into the real checklist would have overwritten already-corrected text with stale instructions). The checklist file is the source of truth; if the two ever disagree again, trust it, not this copy.**

- [ ] **40a.** Add a `project_secrets` entry (or reuse an existing provider's/claude account's `allow_data_to`) listing the sandbox repository; apply the installation root (expect a plan of creates only); `fugaro secrets set NAME --project-wide` a throwaway value; apply `fugaro init --repo` for the sandbox. **FACT:** `gcloud run jobs describe`'s `secret_key_ref` names the project's secret ID (read the ID, never the value).
- [ ] **40b (corrected, round 4/SF6 — `--project-wide` takes no `--repo`/repository argument at all, so "a repository outside the allow-list is refused at --project-wide" describes a command that cannot be issued).** Two separate confirmations, neither needing a second repository: (1) with a `project_secrets`/claude-account-backed `NAME` whose `allow_data_to` is empty or absent (not 40a's own `NAME`), attempt `fugaro secrets set NAME --project-wide` naming no repository at all — **FACT:** the exact refusal text, naming the missing/empty entry, at store time, before any repository is involved; (2) with 40a's `NAME` stored and its `allow_data_to` then edited to **exclude** the sandbox repository specifically (narrowing a list that used to include it, the same transition 40g exercises for a provider), re-apply and re-run `fugaro init --repo` for the sandbox — **FACT:** the sandbox's deployed job has no such variable, confirming an excluded repository mounts nothing even though the project secret itself still exists.
- [ ] **40c.** Confirm precedence: a repository with its own stored version keeps mounting it even though the project copy exists and lists it; disable the repo version's last enabled version **(project-owner only, SF5 — an operator's grant does not include `secretmanager.versions.disable`)**, re-run `fugaro init --repo` with no `--allow-delete` (confirm it is refused, naming the replaced IAM-member address(es), B3), then with the named address(es) passed, confirm the job now mounts the project copy.
- [ ] **40d.** Confirm the data-policy gate still refuses a provider model for a repository whose `allow_data_to` lists other repositories but not this one, with the provider's key stored `--project-wide` (the non-empty-list case; the empty/absent/`["*"]`-list case is 40f's own check — the owner's final ruling opens the gate there too, round 4).
- [ ] **40e.** Confirm no value appears in any log, `fugaro diagnose`, `secrets ls`/`ls --project-wide` output, or a `terraform plan`/`state show` of either root.
- [ ] **40f (owner's final ruling, design §3a, rewritten round 4 — the gate now opens too, it no longer stays closed).** Configure a provider with **no** `allow_data_to` at all (repeat with `allow_data_to: ["*"]`, confirming the two behave identically); confirm `fugaro secrets set <provider-secret> --project-wide` succeeds (not refused); confirm, via `jobs.describe`, that an `agent.auth: api-key` repository never named by this provider anywhere still mounts the key; confirm that `fugaro validate` (or a run attempt) **now accepts** that same repository's pin to it — the data-policy gate opened on the identical condition as the mount, not merely "none refused."
- [ ] **40g (fixed, N4).** Starting from 40f's no-list provider, add a list excluding the sandbox repo, re-run `fugaro init --repo` with no `--allow-delete` (confirm refused, naming the dropped IAM-member address), then with it (confirm applies, job no longer mounts the key, container still present in state — N4).
- [ ] **40h (N1, extended round 4, corrected again round 5 after the round-4 fixes themselves turned out unbuildable as drafted — B1/B2).** With `bitbucket-token` project-shared and the repo copy's version disabled: confirm `fugaro image build`/`image refresh` succeed (**the CLI resolves live and confirms it matches the build account's own grant, read from `GetIamPolicy` — not a deployed job, round 5's correction**), the daily check's own rebuild, and `fugaro doctor` all still succeed. **B1:** run `fugaro init --repo` for a brand-new workflow with no job deployed yet, confirm its first build succeeds using the spec `fugaro init` itself just applied (no live-vs-deployed comparison — there is no deployed job yet); separately, store a fresh repo-scoped override with no apply in between, confirm a standalone `fugaro image build` refuses, naming the live-resolve-vs-granted mismatch and telling you to run `fugaro init --repo` first (not a bare Cloud-Build-denied error); re-apply, confirm the build succeeds. **B2:** confirm `fugaro init --repo` itself refuses, before writing anything or applying, when moving the git credential to project scope would leave the check job's own base image predating the release shipping `CheckJobSpec.GitSecretID` (naming both fixes: a newer base image, or re-enabling the repo version); confirm a custom/dev base image only warns, not refuses; separately, confirm a deliberately old-shaped `FUGARO_CHECK_SPEC` (no `git_secret_id` key) still rebuilds successfully against a current binary, falling back to the repo ID (the `""` fallback, a different case from the gate). **Nit, acknowledged:** the grant-mismatch refusal above is GitHub-App-specific for `doctor`/`appPreCheck`'s own paths and the sandbox is Bitbucket-based — exercise it on a GitHub-based repository if the installation has one, else it stays covered only by the hermetic suite.
- [ ] **40i (N2).** With a different project-shared name stored `--project-wide` and the sandbox's own repo copy for it never stored at all, run `fugaro init --repo` at a real terminal: confirm the secrets stage does not prompt for that name; run it again with `--yes`: confirm no `NeedsYou` block for that name either; confirm a name missing everywhere still does both, unchanged.
- [ ] **40j (N3, corrected — a provider here tests nothing: `Installation()` includes every provider's secret unconditionally, regardless of its list's state, and an explicit `[]` is refused for a provider anyway).** Use a `project_secrets`/claude-account-backed name instead: store one `--project-wide` with a non-empty `allow_data_to` listing the sandbox, apply, then empty that entry's `allow_data_to` to `[]` (or remove the entry entirely — both are still valid "revoke everyone" actions for these two kinds), run plain `fugaro init`: confirm the plan is not a hard `prevent_destroy` Terraform error and the container's key is still present (discover-then-union, Task 4).
