# Reconciliation: the order against the code, and the decisions this work needs

**Status: a design, all tasks approved by the owner on 2026-11-15 and nothing built yet.** The decisions are **D2400–D2417** (§4), each with a recommendation and the cost of every alternative, **all eighteen answered by the owner on 2026-11-15, each as recommended**; the number block is read in §5 and this work holds **D2400–D2429**. The owner's order is recorded verbatim in [spec.md](spec.md) §0. **Where this file and [spec.md](spec.md) disagree, this file wins.**

Reconciled against `origin/main` at **`5c170e5c`**, on 2026-11-14, by reading the files rather than the designs that describe them.

**The shape of the reading: almost every piece of the four verbs already exists, and what is missing is the reach.** `fugaro project repos` already has the group, the gate, the ETag-guarded allow-list writer, the in-flight-run read and the local-config writer. `fugaro doctor` already computes most of the rows `status` needs, per repository, in `doctorGitHubApps`, `doctorCredentialsServiceDeployed`, `doctorBitbucketTokenValid`, `doctorSecretRotationAge` and `project-secret-stored`. `fugaro managed status` is already a read-only status with a closed JSON document a console reads. `fugaro update --repo` already converges a repository's whole root, job, grants and all. **What is missing is that no verb of the group puts those pieces together**: `add` writes one of the six things, `rm` removes one, `ls` prints one and has no `--json`, and `status` does not exist. So this is mostly a wiring design, and the honest part of it is saying which of the built pieces the wiring may reuse, which it must not fork, and where the order of the six steps is the whole feature.

---

## 1. Assumptions

- **A1.** "Repos management" means **a repository's enrolment in a project**, not repositories as such: the verbs address the pair (project, repository), which is the pair the allow-list, the `repos:` entry, the image map and the repository root all address. Nothing here manages a repository outside a project.
- **A2.** "remove" in the owner's order is the **inverse of the converge**, not a delete of anything that holds history. `fugaro project repos rm` already exists and already means the narrower allow-list removal; the order widens the verb rather than adding a second one.
- **A3.** "status (sort of a doctor)" means a **per-repository** doctor: the subject is one repository's readiness to launch, which is not what `fugaro doctor`'s project-wide screen is about, and the two must not become two readers of the same fact.
- **A4.** The owner runs these commands **at their own terminal, in their own checkout**. `add` reads the repository's `fugaro.yaml` from a checkout, so a machine with no checkout of the repository cannot run it — the same limitation `fugaro update --repo` already has.
- **A5.** The provider list is the two the repository already has: GitHub (through the App) and Bitbucket (through a workspace or project access token). No third provider is implied.
- **A6.** The credential the empty container is about is the repository's **`github-app-key`**; the Bitbucket equivalent is the token the repository's runs read. Both are **per-repository by default** unless the project's config hoists one.
- **A7.** The console is a **reader** of the CLI's documents and never a writer of the cloud; the order asks for `status` and `ls` in the console, not for `add` and `rm` there.
- **A8.** `docs/status.md` carries this spec's row from **this design's own sweep** — `repos-management`, `0/7`, nothing merged and no run — and **no task of the plan edits that file**: whoever merges the spec moves the row as tasks land, the `CLAUDE.md` rule for a spec or a plan and the file's own sorting rule 7.

An assumption that no finding contradicts and no decision rests on stays in this list.

---

## 2. What the repository already has

| The order's concept | What is already in the tree |
| --- | --- |
| the group the four verbs live in | `newProjectReposCmd` (`internal/cli/project_enroll.go`): `ls`, `add`, `rm`, `set` |
| the allow-list write | `putAllowRepos` (`project_enroll.go`): `patchPolicyJSON`, `checkPolicyBytes`, `db.PutIfMatch` under the ETag, `recordPublishedGeneration` |
| the gate a write asks | `accessGateProject` → `accesslib.ActionManageProject` → `RoleAdmin` (`internal/cli/access_gate.go`); `accessGateProjectRead` for a read |
| the agent refusal | `agentMarker` + `initflow.AgentRefusal` at each writer's door |
| a typed confirmation | `confirmTyped` and `confirmTypedStrict` (`internal/cli/budget.go`); `removalWarning` (`project_enroll.go`) |
| whether a run is going | `runsInFlight(ctx, db, lc)`, already called on the `rm` path |
| the local `repos:` entry | `fugaro project repos set` (`internal/cli/project_repos_set.go`, D1173), writing through `applySettingsEdit` (`internal/cli/project_repos_set.go:510`) |
| the image map entry | `runImageMapSet` / `imageMapWith` / `userSave` (`internal/cli/image_map.go`), with the D1906 keep rule in `localcfg.ImageMapEffective` |
| which catalog entry a workflow gets | `catalog.JobImage` / `ImageRequest` / `Selection` (`internal/catalog/select.go`) |
| the repository's cloud resources | the `Repository` stage of `internal/initflow` → `repoEngine` (`internal/cli/init.go`) → `deploy/terraform/gcp/roots/repo`, `modules/repo`, `modules/workflow` |
| the job account's grant on the credentials service | `google_cloud_run_v2_service_iam_member.credentials` (`modules/workflow/sa.tf`), in state as `RepoSpec.Addresses()` names it |
| `credentials_service_ready` | `infra.CredentialsServiceServes(installed, ready, auth)` (`internal/infra/spec.go`) over `localcfg.Repo.CredentialsServiceReady` |
| a per-repository secret with no version | `infra.MissingSecret` / `hasEnabledVersion` (`internal/infra/readiness.go`); doctor's `project-secret-stored` (`internal/cli/doctor.go`); the App's own "not stored yet" (`internal/cli/githubapp.go`) |
| a read-only status with a closed document | `managedStatusDoc` and `newManagedStatusCmd` (`internal/cli/managed_status.go`), read by the console per `console/docs/json-contract.md` §6.21 |
| the console's reach | `CONSOLE_OPERATIONS` (`console/src/main/spawn.ts`), one literal `words:` array per operation |
| the removal of a repository's resources | `infra.Retirements` (`internal/infra/retire.go`), `deleteOrphanedJob` (`internal/cli/init_retire.go`), the state removal and `ForgetRepoState` (`internal/infra/repo.go:191`, called from `internal/cli/init.go:5641`) |
| secrets never deleted | `lifecycle.prevent_destroy` (`modules/repo/secrets.tf`) and `internal/infra/tf/retire.go` |

Every row of that table is a thing this work **reuses**; none is a thing it should build again. §3 is about the handful that are missing, and about the one thing the order gets wrong about the code.

---

## 3. Findings

### F1. The group exists, and its Short says what it is today: the allow-list

**What the code does.** `newProjectReposCmd` (`internal/cli/project_enroll.go`) registers `newProjectReposLsCmd()`, `newProjectReposAddCmd()`, `newProjectReposRmCmd()` and `newProjectReposSetCmd()`; the group's `Short` is *"The project's allow-list: which repositories may run"*, and `add`'s `Use` is `"add <provider/owner/name | owner/name | *>"`.

**Consequence.** The order does not need a new group, and it does need the group's own description to stop being narrower than the group. A verb that converges a repository while the group says "the allow-list" is a verb an operator finds by accident.

### F2. **Wrong: `add` today is one of the six steps, and it is step 4**

**Claim of the order (spec §2.1).** `add` is the whole converge.

**What the code does.** `runProjectReposEdit` writes the published allow-list and nothing else; the other five steps of spec §1 are five other commands, three of which write a different store.

**Consequence.** The design has to say what happens to the narrow behaviour, and it cannot simply delete it: a **pattern** (`owner/*`) has no checkout, no `fugaro.yaml` and no image, so the converge is not defined for it and the allow-list write is the whole of what an operator can want. §4's **D2404** is that question, and the recommendation keeps the narrow path reachable behind one flag.

### F3. **Exists: the allow-list write is one ETag-guarded writer, and it stays the one**

**What the code does.** `putAllowRepos` splits `before` from `allow` so the diff prints the pre-edit list, `patchPolicyJSON`s the node, runs `checkPolicyBytes`, does `db.PutIfMatch` under the ETag — answering `ErrPrecondition` with *"the policy changed while this ran; nothing was written: run it again"* — prints `allow_repos: before → allow`, and records the published generation **only** when `lc.PolicyGeneration == etag` (D854).

**Consequence.** Every writer this design adds — `add`'s converge, `rm`'s removal — calls **this** function and never a second one. The generation guard is what keeps a converge from publishing over a policy someone else published while it ran, and a task that re-implemented the write would lose it silently.

### F4. **Exists: the gate the order asks for is already the group's gate**

**What the code does.** `accessGateProject` (`internal/cli/access_gate.go`) decides through `accesslib.ActionManageProject`, which resolves to `RoleAdmin` **at project scope** under roles-scopes (F19 of that design, D1581). `accessGateProjectRead` is the read's own gate.

**Consequence.** D2414 is not a new gate; it is the statement that the four verbs take the gates the group already has, and that `ls`/`status` take the read's.

### F5. **Rule: `--yes` and `--non-interactive` are gone, and a plan that puts `--yes` back contradicts a merged ruling**

**The rule.** **T11** of `docs/no-prompt-policy/implementation-plan.md` removed both flags together, landed as commit `fe0d59d5` (#2633). There is no `BoolVar(…, "yes", …)` left in non-test Go. `internal/cli/project_enroll.go` and `internal/cli/project_cmd.go` still carry **dead `yes bool` struct fields** the removal left behind, and `internal/cli/noprompt_lint_test.go` carries the `pendingSites` fence for the sites whose class is not yet removed.

**What this means for the design.** `add`'s confirmation is `confirmTyped`, and **nothing on the command line stands in for it** except `--ask`, which is the no-prompt policy's own flag for putting confirmations back. **D2403** is this finding's decision, and it is a correction to the draft this work adopts (F22).

### F6. **Exists: the confirmation primitives, including the one no flag stands in for**

**What the code does.** `confirmTyped(cmd, in, lc, yes, what)` answers the ordinary typed confirmation; `confirmTypedStrict(cmd, lc, why, what)` is the one **no flag stands in for**, and it is what the `rm` path already calls when a run is in flight.

**Consequence.** Spec §2.2's "an in-flight run is never disturbed" is not a new control: the read that decides it (`runsInFlight`) and the confirmation that follows it already exist on this exact path.

### F7. **Exists: the local writes `add` and `rm` need are each already one function**

**What the code does.** The `repos:` entry is `fugaro project repos set`'s subject (D1173), writing through `applySettingsEdit` behind the settings fence; the image map entry is `imageMapWith` + `userSave`; two writes into one local document are done by re-reading the file's bytes for the second, which is the pattern the settings writer already uses.

**Consequence.** `add` performs two local writes into one document and one map write, and each has an owner already. Neither task writes a new local-config writer.

### F8. **Gap: nothing in the CLI reads the image map back**

**What the code does.** `fugaro image map` has **only `set`** (`internal/cli/image_map.go`); its own header says the map is *"written and never read here"*. The effective map is `localcfg.ImageMapEffective(published)` (D1906): a file that **names no map** keeps the published node's, and a file that writes one — even empty — **states the whole map**.

**Consequence.** `ls` cannot print a repository's image by asking the image family; it reads `ImageMapEffective` itself, and `status`'s image-map row likewise. This is a read, not a second writer, and the two verbs share it rather than each deriving it.

### F9. **Exists: which catalog entry a workflow gets is already decided in one place**

**What the code does.** `catalog.JobImage(ImageRequest{Catalog, ImageMap, Canonical, Workflow, Backend})` resolves a workflow's own `image_name:`/`needs:` first, then the project map (`mappedEntry`), then the catalog default, then the repository's own image (`SourceRepo`), returning a `Selection{Source, Entry, Reference, Warnings}`. `doctor_layer.go`'s `catalogImageChecks` already warns that an entry the workflow names is unbuilt or was built from a different definition than the bucket holds.

**Consequence.** `add` does not invent the image resolution for a repository it is enrolling, and `status`'s image row reuses `catalogImageChecks`' own reading rather than computing freshness a second way.

### F10. **Exists: the repository's root, job and grant are one stage of update**

**What the code does.** `fugaro update --repo` takes a checkout path, and `runInitRepo` calls `repoEngine`, which plans and applies `deploy/terraform/gcp/roots/repo` in a per-slug workdir. The `Repository` stage is `initflow`'s stage 9, needing `Installation2` and `Secrets`. The job is `module.repo.workflow[<name>].google_cloud_run_v2_job.this[0]`; the check job and the Scheduler job are beside it; and the job account's grant on the credentials service is `google_cloud_run_v2_service_iam_member.credentials`, produced only when `credentials_service` is not null.

**Consequence.** `add`'s step 6 is **this**, run programmatically — the same engine, the same root, the same state. It is not a second deployment path and the plan must not build one.

### F11. **Gap, and it is the failure that started this: nothing puts the empty container in front of the operator**

**What the code does.** The `github-app-key` **container** is `modules/repo/secrets.tf`'s (`for_each` over the repository's logical secret names, `prevent_destroy`); a **version** is written by a person or by the credentials service, never by Terraform. `infra.MissingSecret` then makes the readiness gate plan **no job** for a workflow that mounts an unresolved secret — `internal/infra/readiness.go` says *"secret %s has no version"* — and the operator learns it at launch, from `internal/backend/gcp/run.go`: *"launching: job %s does not exist; create it with `fugaro update`"*.

**The readings that do exist.** Doctor's `project-secret-stored` is a red check whose own text is *"neither its own container nor the project's holds an enabled version: no run of a workflow mounting one can start"*; `githubapp.go` says the App's private key *"is not stored yet (the github-app-key secret has no version)"*; `docs/gcp-live-checklist.md` already records that the first `update --repo` on a fresh repository **does not fail** on the empty credential secret, it plans no job.

**Consequence.** The fact is known, computed and tested — and it is **not where an operator enrolling a repository looks**, and `fugaro doctor` is not per-repository. Spec §2.4's credential row is this finding's answer, and its requirement that a failing row name the fixing command is the part that matters: `fugaro secrets set` for the container, not "run `fugaro update`".

### F12. **Exists: most of `status`'s rows are already doctor checks**

**What the code does.** `doctorGitHubApps` (the App's installation over the allow-list), `doctorCredentialsServiceDeployed`, `doctorBitbucketTokenValid`, `doctorSecretRotationAge`, `doctorCanonicalRepos` and `doctorAllowListUncovered` all exist, over the same stores `status` needs. `doctorCheck{ID, OK, Problem, Fix, Severity, Class, Skipped}` and `emitDoctor` already carry the shape spec §2.4 asks for, `checksFail` already decides the exit status, and `TestDoctorReadOnly` already pins that the screen writes nothing.

**Consequence.** `status` is a **view over existing checks plus four rows that do not exist** (the image map, `credentials_service_ready`, the job and its grant, and the last run), rendered under the one-screen rule `doctorDefaultMaxLines`/`doctorLineLimit` set. It is not a second doctor: the checks keep their IDs, their sentences and their fixes, and a task that copies one is a defect.

### F13. **Exists: `credentials_service_ready` is a recorded fact with one reader**

**What the code does.** `localcfg.Repo.CredentialsServiceReady *bool` (a pointer, because "no record" and a recorded `false` are different documents) is written by `recordCredentialsServiceReady` at the repository door and `backfillCredentialsServiceReady` at the installation door, and read through `CredentialsServiceOn` and `infra.CredentialsServiceServes(installed, ready, auth)` — all three conditions, with `auth == cascade`.

**Consequence.** `status` prints the recorded fact and the three conditions behind it; it does not re-derive "ready" from the bundle itself, which is `credentialsBundleReady`'s job.

### F14. **Exists: `managed status` is the precedent for a read-only status with a versioned document**

**What the code does.** `newManagedStatusCmd` reads and prints; `managedStatusDoc` is a closed set of JSON tags, each carrying its own comment about what a null, an empty string and a zero mean; `console/docs/json-contract.md` §6.21 documents it field by field, and `console/src/main/spawn.ts`'s `CONSOLE_OPERATIONS` allow-lists `['managed','status','--json']` as literal words.

**Consequence.** `ls --json` and `status --json` are built to this shape, with their own §6.x sections in the console's contract and two new `CONSOLE_OPERATIONS` entries — after this design, not before it (D2413).

### F15. **Gap: `ls` has no `--json` at all**

**What the code does.** `newProjectReposLsCmd` takes only `addCloudFlags`; it prints header lines, entries with a source suffix, notes, and a bounded list of repositories seen in the runs bucket's `runs/<slug>/` prefixes and Firestore's `spendDaily` over `seenWindowDays`.

**Consequence.** A `--json` shape is a **new wire format** and takes a version (the "a field never changes meaning" rule in `CLAUDE.md`). D2410 is that decision, and it must say what the seen-in-the-window half means in the document, since it is a reading with a window rather than a fact about enrolment.

### F16. **Exists: the removal machinery, and its two hard limits**

**What the code does.** `infra.Retirements(state, spec)` is the addresses in state that the spec no longer produces, classified by kind (`workflow job`, `check job`); `deleteOrphanedJob` deletes a Cloud Run job through the API, label-checked, refusing jobs that are not ours; `RetireAllowDelete` is the owner's delete list; `ForgetRepoState` removes the state objects for a repository. **Secrets are never deleted** — `prevent_destroy` in `modules/repo/secrets.tf` and `internal/infra/tf/retire.go`'s own words — and `fugaro update --repo --forget` prints *"Terraform no longer manages %s; nothing was destroyed."*

**Consequence.** `rm`'s resource half is this path, and spec §2.2's "deliberately leaves the secrets, the images and the history" is not a promise this design adds — it is the machinery's own contract, which the command must **say** rather than merely obey.

### F17. **Exists, and it is what makes `rm` safe while runs are going**

**What the code does.** `runsInFlight(ctx, db, lc)` is already called on the `rm` path, and `confirmRemovalStrict` is what follows it when a run is going. Nothing on that path rewrites a run's record.

**Consequence.** D2407 needs no control; it needs the property stated and pinned, in the shape `TestUpdateLeavesARunInFlightRunning` already gives the update.

### F18. **Gap: `config/policy/repos/<slug>` is written by a publish, not by the group**

**What the code does.** `projectpolicy.PathRepo(slug)` addresses a per-repository node holding the **names** of the accounts and tokens admitted to that repository, derived at publish from `claude_accounts.<name>.allow_data_to` and `project_secrets.<secret>.allow_data_to` (`DeriveAccounts`, `DeriveBitbucketToken`), and read back by `RepoPolicy.AdmittedAccounts()`.

**Consequence.** `rm` must remove this node too, and the group does not own the publish. Either `rm` calls the publish with a removal (the shape `--remove-routing` already has) or it leaves a stale node naming a repository that is no longer enrolled. D2405 decides, and the recommendation calls the publish.

### F19. **Rule: the HARD RULE and the update-in-flight property**

**The rule.** `CLAUDE.md`: `fugaro update` is the whole upgrade and works perfectly; a release must not ship an operator step that is another command, a deletion or a manual repair; and it is **safe while runs are in flight, always** — the update asks no run to finish first, never refuses a policy write over one, and never rewrites what one holds.

**Consequence.** `add` may not leave an installation that `update` would then correct (D2416), and no step of `add` or `rm` may refuse over a run in flight. A converge that said "finish your runs, then run `fugaro update`" would be the forbidden step in the one place the order cares most.

### F20. **Gap: the docs and the census a new pin moves**

**What the code does.** `docs/rigor-modes/reconciliation.md`'s census row states the count of `internal/cli/docs_*_test.go` files (**139** against the tree today), and **six** tests assert it. `website/content/cli-reference.md` is generated — its first line says so — and `ee/clidoc/cliref_test.go`'s `TestCliReferenceIsCurrent` requires it byte-for-byte, so any **help-text** change must be regenerated with `go run ./ee/clidoc`. `plugin/skills_lint_test.go` resolves every `fugaro …` line and every `--flag` in every skill against the cobra tree.

**Consequence.** The plan's docs task adds one `internal/cli/docs_repos_management_test.go` and therefore **moves the census row from 139 to 140** in a folder this work does not own, with its own amendment sentence — and every task that changes a command's help regenerates the CLI reference in the same commit.

### F21. **Open: no plugin skill names the group**

**What the code does.** A grep over `plugin/` for `fugaro project repos` returns nothing; the skills that touch adjacent ground are `setup` (the budget block, the allow-list's existence) and `working/reference/status.md` and `diagnose.md` (which name `fugaro project policy show` and say the allow-list is the owner's). `plugin/skills/setup/reference/decisions.md` (line 43) already carries the rule that a project-wide cap *"is the owner's, in their own terminal, never by you"*.

**Consequence.** There is no skill to correct — there is a skill **to write**, and its content is constrained by that existing rule: a skill may tell a session to run `fugaro project repos ls` and `status` and to read the fix a failing row names, and may never tell it to run `add` or `rm`.

### F22. **The draft this work adopts, and the two things about it that are wrong**

**What the draft is.** Another run (`fugaro/20261114-183001-d201`, head `2bcda85d`, draft pull request **#2690**, its own task named **RE1**) built `fugaro project repos add` as the full converge and **ended at writeback**: its status block reads *"Draft · stage implement timed out after 3h0m0s · tests failed on `2bcda85` · model cost $0.19 of $10 cap"*. Its diff is `a524f30d..2bcda85d`: a new `internal/cli/project_enroll_converge.go` (877 lines) and `internal/cli/project_enroll_converge_test.go` (377), plus edits to `project_enroll.go`, `noprompt_lint_test.go`, `report_exempt.go`, seven other test files, `website/content/cli-reference.md`, `docs/project-layer.md` and `docs/releases/v0.25.0.md`.

**What it got right, and this plan keeps.** The order of the six steps printed as a numbered list before anything is written; the refusal of `*`; the refusal of a coding agent's session **first**, before any read; `reposEnrolmentRead` deriving the provider from the config, then `fugaro.yaml`, then the origin host; the merge with the published image map under D1906's keep rule; the two local writes into one document; the allow-list write through `putAllowRepos`; the three `initflow` stages (Policy, Installation, Repository-when-there-is-a-checkout) run with `noBuild`; the report; and the explicit line that **the first image build is not part of the converge**.

**What is wrong, and this plan corrects.**

- **It re-adds `--yes`.** F5: T11 removed it. A task may not re-add a flag a merged ruling removed, and `--ask` is what the no-prompt policy provides instead. The correction is D2403, and it is a change to the draft's `project_enroll.go` registration and its Long text.
- **It parks its command in `noprompt_lint_test.go`'s `pendingSites`** with the note *"T15 (the non-money Typed steps, whose fence replaces this confirmation)"*. That is the right shape for a site whose class another task still owes — and it means **this plan's `add` task inherits a fence it does not own**: the plan must name the dependency (the `pendingSites` entry is removed by T15 of the no-prompt policy, not by this work) rather than silently keep it.

**Consequence.** This plan's **RE3** is **adopting and correcting the draft**, not starting over, and the two corrections above are the difference. The reviewer reads the draft's diff against this reconciliation.

### F23. **Rule: a repository's `fugaro.yaml` is the only place for repo-specific behaviour**

**The rule.** `CLAUDE.md`: *"A repository's `fugaro.yaml` (`internal/config`) is the only place for repo-specific behaviour; the engine stays generic."* The project layer sets shared defaults; `internal/policy` merges layers so only tightening wins.

**Consequence.** `add` **reads** a repository's `fugaro.yaml` and never writes it, and `status` reports a `fugaro.yaml` that no longer matches what the project config's `repos:` entry holds rather than converging the file. The one file the converge writes on the repository's side is the project's own record, and the plan says so.

### F24. **Exists: one run's in-flight state is already this repository's tested property**

**What the code does.** `TestUpdateLeavesARunInFlightRunning` and `docs/design/update-in-flight.md` pin that an update converges around a run rather than over it: the run keeps its records, its ledger and its counters, and every write it makes still lands.

**Consequence.** D2407's answer is to extend that property's test to the two new writers, not to invent a mechanism.

### F25. **Open: what the owner means by "status (sort of a doctor)" for a repository with no record**

**What the code does.** `doctor` on an installation that has never enrolled anything prints what it has and exits zero; `managed status` distinguishes a read that did not answer (`AuditState`, `RetirementsState`) from one that answered nothing, in its own words.

**Consequence.** A repository named on the command line that is not enrolled at all is a **third** state — not a failing row and not a passing one — and D2411 fixes which. The recommendation follows `managed status`: say which state it is, and make "not enrolled" a nonzero exit rather than a silent empty screen.

---

## 4. Decisions

Each decision carries its **recommendation** and **what the alternatives cost**. The owner's answer is the last line of each; he answered all eighteen on 2026-11-15, each as recommended, so the plan's tasks are written to the answers. The owner approved every task on 2026-11-15.

### D2400. `add` is the whole converge behind one confirmation, and the confirmation covers the five writes and not the build

**Recommendation.** Yes. `add <repo>` prints the six steps as a numbered list with the value it resolved for each, states which it will perform and which are already done, and asks **one** typed confirmation for the writes it is about to make: the two local writes, the allow-list write, and the run of the `initflow` stages that converge and deploy the repository's root. The **first image build is not covered** and the list says so, naming `fugaro image build <entry>`; the `initflow` stages run with `noBuild`.

**What the alternatives cost.** *One confirmation per step* — this is the six-command order with a shorter keyboard, and it is exactly what the owner got wrong; it also makes the "which are already done" reading impossible to take once. *One confirmation covering the build too* — the confirmation then authorises an unbounded per-build-minute cost from a list that cannot state it, which is the shape every decision in this repository about billable work refuses (the update's build confirmation is `Typed` and separate). *No confirmation at all* — the command writes the published policy, the machine's config and the cloud, which is more than any one-line answer can stand for.

**Answer: Answered 2026-11-15** -- the owner took the recommendation (recommended): one confirmation for the writes, the first image build excluded.

### D2401. `add` reads what it can and refuses, naming the fixing command, for what it cannot

**Recommendation.** Read: the provider from the machine's project config, then the repository's `fugaro.yaml`, then the origin's host; the canonical name from the argument and the origin; the workflows and the base branch from the repository's `fugaro.yaml`; the GitHub App id and the Claude accounts from the project config; the image from the workflow's catalog reference, then the effective image map, then the catalog's default. Refuse, each naming one command: no checkout (*"run it from a checkout of the repository, or `fugaro project repos add --allow-list-only`"*), a `fugaro.yaml` naming no workflow, a named catalog entry the catalog does not hold, a project config naming no GitHub App id where the provider needs one, and a coding agent's session.

**What the alternatives cost.** *Ask a question for each missing value* — the command becomes the wizard the six-step order already is, and an unattended run cannot finish it. *Guess and warn* — a guessed image or App id is written into the local config and the policy, where a later reader cannot tell it from a decided one. *Refuse without naming the fix* — the state the empty container already put the owner in, which spec §2.4's fix rule exists to end.

**Answer: Answered 2026-11-15** -- the owner took the recommendation (recommended): `add` refuses, naming the fixing command, for what it cannot read.

### D2402. `add` is idempotent and resumable, and says which it was

**Recommendation.** Both, and they are different readings the command prints: over a **completed** enrolment it writes nothing and prints the sentence `runProjectReposEdit` already prints for an entry the list holds — *"%s is already on the allow-list; nothing changed"*, the format string at `internal/cli/project_enroll.go:512`, where `%s` is the canonical name; over a **half-done** one it converges the missing steps and names the ones it skipped as done. The test for each is a fixture state, not a re-run of the converge.

**What the alternatives cost.** *Idempotent only* — the state the owner actually has, a repository half-enrolled by a failure or a Ctrl-C, is the state the command then cannot fix, which is the failure it exists for. *Resumable only* — a command that always writes cannot be run twice by an operator who is unsure, and an operator who is unsure is the operator this is for.

**Answer: Answered 2026-11-15** -- the owner took the recommendation (recommended): `add` is idempotent and resumable.

### D2403. `--yes` does not come back; `--ask` is what the command line has

**Recommendation.** The draft's `--yes` flag is **dropped** (F5, F22). The confirmation is `confirmTyped` and nothing on the command line answers it except `--ask`, which the no-prompt policy owns and which puts confirmations back rather than removing them. The `pendingSites` entry the draft added to `noprompt_lint_test.go` **stays** and is removed by the no-prompt policy's own T15, which the plan names as a dependency rather than as this work's edit.

**What the alternatives cost.** *Keep `--yes`* — it contradicts a merged ruling, it re-introduces the flag on a command that writes the published policy and the cloud, and it loads a flag the next release's no-prompt sweep would have to remove again. *Drop the confirmation instead* — a converge that writes four stores with no confirmation is the shape `confirmTypedStrict` exists to prevent.

**Answer: Answered 2026-11-15** -- the owner took the recommendation (recommended): `--yes` is dropped; `--ask` is the only knob.

### D2404. The allow-list-only behaviour survives as one flag, because a pattern has no converge

**Recommendation.** `fugaro project repos add --allow-list-only <pattern>` keeps today's write exactly and prints today's output. The converge (D2400) is what the bare `add` does. The flag's own text says why: a pattern such as `owner/*` has no checkout, no `fugaro.yaml` and no image, so there is nothing to converge, and refusing patterns outright would make the wildcard unreachable — which the allow-list's own matching rule exists to allow.

**What the alternatives cost.** *Drop the flag, converge or refuse* — patterns become unwritable through the group, and `fugaro project sync` writes concrete names only, so the wildcard loses its last door. *Keep today's behaviour as the default and put the converge behind `--converge`* — the owner's order says `add` is the converge; a default that still writes one of six steps is the order unimplemented. *Name it `--no-converge`* — a negative flag named for what it does not do reads as the omission rather than the intent.

**Answer: Answered 2026-11-15** -- the owner took the recommendation (recommended): the allow-list-only behaviour survives as the `--allow-list-only` flag.

### D2405. `rm` removes six things, and the published policy node is one of them

**Recommendation.** `rm <repo>` removes: the published allow-list entry (`putAllowRepos`); the machine's `repos:` entry; the machine's image map entry (only when the effective map is the file's own — a published map is not the file's to change); the published `config/policy/repos/<slug>` node, through `fugaro project policy publish`'s removal path rather than a second writer (F18); the repository's state under `RepoStatePrefix`; and the repository's cloud resources through the retire path — its workflow job, its check job, its Scheduler job and its job account's grants — with `deleteOrphanedJob` doing the API deletion. Reuses `fugaro update --repo --forget`'s own two halves rather than a new removal engine.

**What the alternatives cost.** *Leave the policy node* — a published node keeps naming a repository that is no longer enrolled, and a run of another repository reads it; the `allow_data_to` derivation has already moved on, so the node is the only stale copy left. *Remove the state and not the jobs* — the resources stay and `fugaro update` no longer knows they are ours, which is the orphan the retirement machinery exists to avoid. *A second removal engine* — it would not carry `deleteOrphanedJob`'s label check, which is what refuses to delete a job that is not ours.

**Answer: Answered 2026-11-15** -- the owner took the recommendation (recommended): `rm` removes six things.

**Amended 2026-11-17 (the owner's ruling; D2406 stands).** `rm` keeps the repository's Terraform state under `RepoStatePrefix` and keeps the repository root's own IAM members, and says so — the two things this decision had it remove are the two it no longer touches. What it removes is the enrolment and the repository's cloud resources: the published allow-list entry, this machine's `repos:` entry and image mapping, the published `config/policy/repos/<slug>` node, and its workflow job, its check job and its Scheduler job. The ruling's reason: the state is what offboarding steps 1 and 3 (`docs/how-to/offboarding.md`) plan from — step 1 lowers the jobs' `deletion_protection` by applying that root and step 3's destroy takes the job accounts, the build account and their grants — so a removal that deleted the state left those resources untracked and those steps unrunnable, which is the orphan the retirement machinery exists to avoid; and the grants are members only an apply of that root deletes, so this command never could remove them and the sentence it prints no longer claims to. The kept list `rm` prints before the confirmation and after the write therefore names the state with D2406's six, and names the sequence that removes both, `docs/how-to/offboarding.md`.

### D2406. `rm` deliberately leaves six things, and says so twice

**Recommendation.** It leaves, and names before the confirmation **and** after the write: the provider's App installation (a person's, on the provider's side, and removing it ends access for every repository the App serves); the built images (never deleted — `fugaro image rm` never deletes one either); the secrets and their versions (`prevent_destroy`, and `internal/infra/tf/retire.go`'s own promise); the run history in the bucket (deleted at its own 90-day expiry, not by this command); every pull request a run opened; and the remote branch. The sentence is `removalWarning`'s shape, extended, and it is printed whether or not the confirmation is answered.

**What the alternatives cost.** *Say nothing* — the owner removes a repository believing its history went with it, or believing its images did, and finds out later; both are the empty container's failure mode again. *Leave them silently and document it in `docs/`* — an operator at the confirmation is not reading `docs/`.

**Answer: Answered 2026-11-15** -- the owner took the recommendation (recommended): `rm` leaves six things and says so twice.

### D2407. An in-flight run is never disturbed, and the confirmation is the strict one

**Recommendation.** `rm` reads `runsInFlight` first; when a run of that repository is going, the confirmation is `confirmTypedStrict` (no flag stands in) and the sentence names the run and says the resources it holds stay until it ends. No step of `rm` — or of `add` — rewrites a run's record, its ledger or its counters. The property is pinned by a test in the shape of `TestUpdateLeavesARunInFlightRunning`.

**What the alternatives cost.** *Refuse while a run is going* — the HARD RULE's in-flight property forbids exactly this: an update asks no run to finish first, and `rm` is the same class of convergence. *Remove anyway* — the job the run is executing is deleted under it, which is the one outcome the owner's "never disturb" is about.

**Answer: Answered 2026-11-15** -- the owner took the recommendation (recommended): an in-flight run is never disturbed, and the confirmation is strict.

### D2408. `--purge` is out of scope

**Recommendation.** No `--purge` in this work. A purge that deletes the history, the images or the secrets is a **different promise** — it deletes things this repository has decided never to delete through any command (`internal/infra/tf/retire.go`, `image rm`), and it needs its own design, its own delete list and its own owner-run check. `rm`'s output names `--purge` nowhere, so nobody looks for it.

**What the alternatives cost.** *Ship `--purge` here* — it is a deletion of a run's history and of a container `prevent_destroy` protects, designed in the same breath as the verbs and reviewed at the same tier, which is how a delete list gets widened by accident. *Ship it as `--purge` on the existing `rm` with the same confirmation* — the confirmation for a policy-entry removal would then stand for the history of every run, which is not what a typed name can mean.

**Answer: Answered 2026-11-15** -- the owner took the recommendation (recommended): no `--purge`.

### D2409. One `ls` row per repository, and the two halves of its state are columns

**Recommendation.** One row per repository with: canonical name; provider; base branch; workflows; the image (the entry and whether it is catalog, mapped or the repository's own); its Claude accounts; `published` (in the allow-list, and the pattern that admits it); and `local` (whether this machine's config holds the `repos:` entry). The existing `seen` list — repositories read from the runs bucket's `runs/<slug>/` prefixes **and** Firestore's `spendDaily` — keeps its own header and its own bounded rendering, and is **not** merged into the per-repository rows: it is a reading of what has run here, and a reader who took it for enrolment would draw the wrong conclusion. **Its two sources are not one window and the design must not call them one**: the runs bucket lists a repository's prefixes with **no window at all** (`runstore.ListSlugs`, whose own comment says *"the window bounds a query rather than the history: a repository whose runs are older than it is still named by the runs bucket"*), and only the `spendDaily` half is read over `seenWindowDays`. Each entry already carries the **sources** it was read from (`seenRepo.Sources`), which is what `seenNote` prints — *"seen in …"* is **where**, not when — and that member, not a window, is what the document carries forward.

**What the alternatives cost.** *One row per allow-list entry only* — the current output, which is one of the two states and says nothing about the other; an operator cannot see a repository that is configured locally and not admitted. *Merge the seen list into the rows* — a repository seen running here and no longer admitted would read as enrolled, and a repository admitted and never run would read as unenrolled (`ls` would then have to explain, per row, which source named it).

**Answer: Answered 2026-11-15** -- the owner took the recommendation (recommended): one `ls` row per repository.

### D2410. `ls --json` is a new versioned document, and it carries the window as a window

**Recommendation.** `fugaro project repos ls --json` prints one versioned document (a `version` member, this design's value) with three parts: `repos` (the per-repository rows of D2409), `allow` (the published entry list with the generation it was read at, and the state of a read that did not answer), and `seen` (that list, never inside `repos`, with **each entry carrying the sources it was read from** — the runs bucket, `spendDaily`, or both — and **no window-length member over the list as a whole**, because only one of the two sources is windowed and a member read as covering both would label a runs-bucket entry older than 30 days as seen inside the window). Every member that can be absent carries its own state sentence rather than a zero, following `managedStatusDoc`'s rule (`AuditState`, `RetirementsState`). The shape is documented in `console/docs/json-contract.md` as its own §6.x section **in the same pull request**, and the console's operation allow-list gains nothing until D2413 says so.

**What the alternatives cost.** *No `--json`* — the console and the history have nothing to read, and the group's answers are parsed from a text listing, which is the drift the wire-format rule in `CLAUDE.md` exists to prevent. *`--json` without a version* — the first added field is a silent change for a reader this repository does not control. *One `repos` object with a `seen` boolean* — it makes a reading of what has run a property of an enrolment, which is the misreading D2409 refuses. *A single window-length member beside the whole `seen` list* — it describes the runs-bucket half, which has no window, so the document would state a window the data does not have (the field-meaning rule in `CLAUDE.md`).

**Answer: Answered 2026-11-15** -- the owner took the recommendation (recommended): `ls --json` is a new versioned document.

### D2411. `status [REPO]` carries nine rows, and "not enrolled" is its own answer

**Recommendation.** The nine rows of spec §2.4, in this order: App installation reachable; credential version enabled (per mounted secret, `github-app-key` or the Bitbucket token); Claude account tokens; image map entry and freshness; `allow_data_to` naming it; `credentials_service_ready`; job deployed and `run.invoker` granted; project-layer copy present; last run and its outcome. With no argument it prints one line per repository plus the failing rows; with `REPO` it prints that repository's rows in full. **A repository named that is not enrolled at all** is its own line saying so — following `managed status`'s rule that a read which answered nothing and a read that did not answer are different facts — and it is a **nonzero exit**.

**What the alternatives cost.** *Fewer rows* — the row dropped is a launch refusal the operator meets later, and the empty container is exactly a row that was not there. *"Not enrolled" folded into a failing row* — an operator who mistyped a name is told a repository they do not have is misconfigured. *One line per repository with no per-repository detail* — the four facts that are per-workflow (image, grant, credential, account) cannot fit, and the fix sentence would name a command without saying which of several subjects it is for.

**Answer: Answered 2026-11-15** -- the owner took the recommendation (recommended): `status` carries nine rows, and "not enrolled" is its own answer.

### D2412. `status` is read-only, exits nonzero when a launch is blocked, and carries `--json`

**Recommendation.** Read-only on every path including `--json`; exit nonzero when a row that blocks a launch fails, zero otherwise, through `checksFail`'s own rule and with `ExitUserError` for a usage failure; `--json` printing a versioned document whose per-row members are the check's own `ID`, its `OK`, its `Problem`, its `Fix` and its `Severity` — the console reads it, per §6.x. A row that is a **warning** (an entry the catalog holds but was built from another definition, a secret older than the rotation age) does not change the exit status.

**What the alternatives cost.** *Exit nonzero on a warning* — a working installation is reported as broken to every script that calls it, and the fix sentence is then a suggestion dressed as a failure. *Write the fixes* — a status that can converge is a status whose output depends on when it ran, and the doctor's read-only property is pinned by `TestDoctorReadOnly` for this reason.

**Answer: Answered 2026-11-15** -- the owner took the recommendation (recommended): `status` is read-only and exits nonzero only on a launch blocker.

### D2413. The verbs live in `fugaro project repos`; the console reads and never writes

**Recommendation.** All four verbs stay in the existing group, whose `Short` and `Long` are rewritten to say what the group now is. The console may call `ls --json` and `status --json` only: two new literal `words:` entries in `CONSOLE_OPERATIONS` (`console/src/main/spawn.ts`), added in the pull request that documents their shapes. The console never calls `add` or `rm`, and no console code constructs their arguments. The plugin skill gets a new reference file under `plugin/skills/working/`, telling a session to read `ls` and `status` and **never** to run `add` or `rm`, which is the rule `plugin/skills/setup/reference/decisions.md` (line 43) already states for the owner's own commands.

**What the alternatives cost.** *A new top-level group* — the allow-list, the `repos:` entry, the map and the root are one subject, and two groups for one subject is two places an operator looks. *Let the console write* — it would run Terraform and write the published policy from a desktop app, with the gate's answer taken from a session that is not the owner's terminal. *No skill change* — a session asked to "add the repository" invents the six steps again, which is where this started.

**Answer: Answered 2026-11-15** -- the owner took the recommendation (recommended): the verbs stay in `project repos`, and the console only reads.

### D2414. The writes ask admin at the project; the reads ask what a read asks

**Recommendation.** `add` (both modes) and `rm` take `accessGateProject` → `ActionManageProject` → `RoleAdmin` at **project** scope, exactly as `runProjectReposEdit` does today (F4), and take it **before** any cloud read. `ls` and `status` take `accessGateProjectRead`. The agent refusal (`agentMarker` + `AgentRefusal`) is taken **first** on `add` and `rm`, before the gate and before any read — the order `fugaro project sync` already uses, for the reason it gives there.

**What the alternatives cost.** *A repository-scoped gate for `add`* — enrolling a repository writes the published policy, which every repository of the project reads; a scoped role that could write it could widen what another repository's runs admit. *The read gate on `add`* — it is a write. *The gate after the reads* — the reads are of the project's own config and the provider, and a session without the role has no business making them.

**Answer: Answered 2026-11-15** -- the owner took the recommendation (recommended): writes need admin at project scope.

### D2415. Anything that puts a secret in front of a person follows the hidden-prompt rule

**Recommendation.** The Bitbucket token, and any App key read this design adds, are read from **stdin or a hidden prompt, never from an argument**, held in memory for the call only, never printed and never in an error, and refused in a coding agent's session — which is what `fugaro project sync --bitbucket` and `--github-app-key` already do and what `internal/cli`'s secrets paths all do. `status` prints **presence and never a value**: a secret's name, its version's state, its rotation age. Where the *fix* for a row needs a value, the row names the command and the command asks for the value itself.

**What the alternatives cost.** *A `--token` flag* — the value is in the process's command line, where every process on the machine reads it, and in the shell's history. *Read a value to decide a row* — a read that can put a credential in a log is a read that eventually does. *Say "not stored" without the fixing command* — the empty container's failure, which is the whole reason this row exists.

**Answer: Answered 2026-11-15** -- the owner took the recommendation (recommended): hidden prompt or stdin only, and `status` shows presence.

### D2416. `add` never leaves an installation that `fugaro update` would then change

**Recommendation.** The converge writes only what `update` re-derives: the local config through the settings writer, the published allow-list through `putAllowRepos`, and the repository's root through the `Repository` stage's own engine. It runs the **same stages** in the **same order** `update` does (Policy, then Installation where needed, then Repository), it records what it did in the same place, and a subsequent plain `fugaro update` over a converged installation writes nothing new — a property the plan pins with a fixture, not with prose. `update` gains no knowledge of `add`: an installation that never ran it and one that ran it converge to the same tree.

**What the alternatives cost.** *A second deployment path for the root* — two paths that must agree forever, and the one that drifts is found by a launch refusal. *`add` tells the operator to run `fugaro update` afterwards* — that is the operator step the HARD RULE forbids, and it is the six-step order with a shorter keyboard. *`update` learns about enrolments* — update re-derives from the config and the policy; a third input is a state update did not have, and it would then have to be reconciled on every run.

**Answer: Answered 2026-11-15** -- the owner took the recommendation (recommended): `add` runs `update`'s stages in the same order, and `update` writes nothing after.

### D2417. One owner-run live check, in the shape of Check 313

**Recommendation.** **Check 670** (written as 620 and renumbered by the owner's ruling of 2026-11-21 — see §5's amendment), owner-run, one bash script plus its `_test.sh` driving fake tools on PATH, one row and one `## Check 670` section in `docs/gcp-live-checklist.md`, and its registration in `scripts/live_checks_test.go`. It enrols `dimipaun/fugarosandbox` end to end on a throwaway installation at the owner's own terminal, reads every one of the six steps' own output, **deliberately leaves the `github-app-key` container empty at the first attempt** and asserts that `status` says so and names the fixing command (F11 — the failure this whole order answers), then fills it, launches one run, removes the repository while a second one is going and asserts the six things `rm` leaves. **Never `belong`**, never an agent. Numbers **620–629** are this work's reserve (§5), of which **621–629 are unused**; 670 is the only check this plan writes.

**What the alternatives cost.** *No live check* — the six steps and the empty container are facts about a real installation, and no hermetic test can say that `status` names the right fixing command on a real one. *A check per verb* — four scripts, four rows, four passes by the owner, for one flow whose whole subject is that the steps are one flow. *Run it from an agent's session* — the command refuses one, and the value needs the terminal that can answer the hidden prompt.

**Answer: Answered 2026-11-15** -- the owner took the recommendation (recommended): one owner-run check, written as Check 620 and numbered **Check 670** since the owner's ruling of 2026-11-21.

---

## 5. The number block

The block is read **above every `D<number>` written on `main` and in every open work stream**, read on **2026-11-14** at `origin/main` = **`5c170e5c`**, rather than inherited from a neighbour's line.

- **On `main`**, the highest *written* decision is **D2399**, in `docs/review-job/reconciliation.md`; `docs/review-job/README.md` states that design's block as **D2380 to D2399** and its check block as **610 to 619**. No four-digit decision above D2399 appears anywhere under `docs/` at this head.
- **The check register.** The highest check number written is **611** (`docs/review-job/implementation-plan.md`, RJ10), and `docs/status.md` names *"owner Checks 610 and 611"* as that work's. **610–619 are review-job's**; **620** is therefore the next free check number, and this work's reserve is **620–629**. **Amended 2026-11-21 (the owner's ruling):** the reading above was taken on 2026-11-14 and **620 was not free** — by the time this plan's own check was written, **620 to 627 were the mobile-auth work's** (the register's own paragraph, and `docs/status.md`'s rows for both specs), so this plan's one check is **Check 670** and is never 620. **621 to 629 stay reserved and unused, and no number is reused**: the renumbered check is the one this plan writes, and it is written as 670 in the plan, in the register's row and section, and in the script's own name.
- **In the open work streams**, each branch that writes a decision block was fetched and read rather than trusted to a table: `git fetch origin <branch>` then `git grep -h -o -E 'D2[34][0-9]{2}' FETCH_HEAD -- docs/`. **No open work stream writes a decision above D2399, and none reserves a check in 620–629.** The draft this plan adopts (`fugaro/20261114-183001-d201`, head `2bcda85d`) writes **no decision block** — it is a task branch — so it claims nothing here.
- **The commands that re-check all of it** (a later task that needs a number re-checks these facts rather than this conclusion):

```sh
git log -1 --format='%H %ad' --date=short origin/main
git grep -hoE '\bD2[0-9]{3}\b' origin/main -- docs/ | sort -u | tail -1
git grep -n 'D2380 to D2399\|610 to 619' origin/main -- docs/review-job/
grep -rhoE '\b[Cc]heck ?[0-9]{3,4}\b' docs/ scripts/live-checks/ | grep -oE '[0-9]{3,4}' | sort -n | tail -1
for b in $(git ls-remote --heads origin | awk '{print $2}' | sed 's#refs/heads/##'); do
  git fetch -q origin "$b" && git grep -qE '\bD2[3-9][0-9]{2}\b' FETCH_HEAD -- docs/ && echo "$b claims one"
done
```

**A number reserved by an unmerged plan is invisible to a grep of `main`**, and a number reserved in a merged plan is invisible to a grep of `docs/gcp-live-checklist.md` — `docs/voice/implementation-plan.md` §2 (line 58) states both, and it is why the fourth command above reads the checklist and the designs' own files rather than one file. (`§5.3` is [voice.md](../voice/spec.md)'s section, not the plan's: it works the same claims through, and the plan is where the allocation is written.)

**This work takes D2400–D2429: D2400–D2417 are written here and every one is answered (2026-11-15, as recommended), and D2418–D2429 are reserved and unused.** Its checks are **620–629**, of which **621–629** are reserved and unused, and the one check it writes is **670** — the renumber of 2026-11-21, recorded above, because 620 to 627 are the mobile-auth work's.

---

## 6. The residual, stated rather than hidden

- **This design does not make a hostile agent safe.** `add` and `rm` write the published policy, the machine's config and a Terraform root. The agent refusal at their door stops the accident and the injection, and `SECURITY.md`'s own reading — the agent shares the container, the user and the metadata server with the runner — is not softened by anything here.
- **This design does not make the six steps atomic.** `add` performs four writes and a Terraform converge; a failure in the fourth leaves the first three done. That is the resumable state D2402 exists for, and the command's own output names what is left — but the installation is half-converged for as long as it takes the operator to run it again, and nothing here shortens that.
- **This design does not remove the empty container's possibility.** A repository whose credential a person has not stored yet still plans no job, and the first `update --repo` on a fresh repository still succeeds while planning nothing. What changes is that `status` **says so** and names the command; the fact itself is the readiness gate's and is not this work's to change.
- **This design does not verify the Bitbucket half end to end.** Check 670 uses the GitHub sandbox. The Bitbucket rows of `status` reuse `doctorBitbucketTokenValid` and the allow-list checks, which are built and tested, but no owner-run check here reads them on a real workspace. If the owner wants one, it is a second check and a second number in this work's block.
- **This design does not price anything.** `add` starts no build, and no step of it spends money beyond what `fugaro update`'s own stages spend. D2400 is written so that stays true.
- **It keeps `docs/status.md`'s row for this spec, and gives that file to no task.** The row is written in this design's own sweep and moved by whoever merges the spec; no task of the plan edits the file. It edits nothing under `.github/workflows/` and adds no dependency.

## 7. UNVERIFIED

- **U1.** Whether `deleteOrphanedJob` and the retirement flow can be driven from a `project repos rm` **without a plain `fugaro update`** in between, or whether the removal needs the update's own stage to notice the state change. What settles it: reading `internal/cli/init_retire.go` and `internal/infra/retire.go` against a fixture in the task that builds `rm`. Who can: the task's own red step.
- **U2.** Whether the owner wants `status` to **also** cover the `fugaro.yaml`-versus-project-config mismatch (F23) as a row, or to leave a repository's own file entirely alone. What settles it: the owner's answer to D2411. Who can: the owner.
- **U3.** The exact cost of Check 670 on the sandbox — one `fugaro update --repo`, the **two runs the check launches** (one it only watches start, one it removes the repository under, which is what makes the strict confirmation the one that fires), and whatever the first image build costs if the owner takes it. What settles it: the owner's own reading of the register section's cost paragraph before running it. Who can: the owner.
- **U4.** Whether the console's `CONSOLE_OPERATIONS` entries for the two new documents belong in this work's pull requests or in the console's own repository sweep. What settles it: reading `console/docs/json-contract.md`'s §6.22 note about how a section and its operation arrive together. Who can: the task that documents the shapes.
- **U5.** Whether any open work stream reserves a check in 620–629 in a plan that has not merged. The §5 commands read every fetched branch, and a plan that has never been pushed is invisible to them. What settles it: the owner's own knowledge of what is in flight. Who can: the owner.
