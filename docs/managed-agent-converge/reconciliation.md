# Reconciliation: every claim of the spec against the code, and the decisions this design needs

**Status: a design, and nothing is built.** The decisions are **D1011–D1026** (§3), each with a recommendation and the cost of every alternative. The owner has answered none of them. **Where this file and [spec.md](spec.md) disagree, this file wins.**

Reconciled against `origin/main` at **`15f4d8ab`**, on 2026-11-01, by reading the files rather than the designs that describe them. Line numbers are the head this branch was cut from.

The shape of the reading: **almost everything the spec needs already exists, in `fugaro managed apply`.** The gate, the allowlist, the owner's delete list, the deployer account, the inventory fallback, the marker's fail-closed read and the sentence an agent's session reads when it proceeds are all in the tree and tested. What is missing is not a control — it is the **reach**: exactly one command threads the gate, and every other command still asks a person or refuses a session. So this design is mostly a wiring design, and the honest part of it is saying which commands the wiring may reach and which it may never.

---

## 1. Findings: what exists

### F1. The refusal is one function, and exactly one command threads a managed gate

**Claim of the spec:** §2, that the agent's refusal is a single rule and that `fugaro managed apply` is the only caller that lifts it.

**What the code does.** `internal/initflow/stage.go`: `CanConfirm(class, c)` (`:581`) returns false for every class when `c.Agent` is a marker, and `CanConfirmManaged(class, c, m)` (`:564`) is the same function with a `Managed` value added — it proceeds only when `m.Removes(class)`, and `Removes` (`:549`) is `m.Managed && m.Admitted && m.Covers(class) && !m.Fallback`. `Covers` is true only for `YesOrTyped`, so a `Typed` step — money or permanent — is never admitted by a plan gate. `AgentRefusal` (`:424`) names the marker and offers no `--yes`; `AgentProceeded` (`:452`) is the one sentence for the case where a session proceeds, and it names the marker, says the step proceeds on a gate, and names nothing a reader could unset.

**Consequence.** The generalisation this design needs is not a new rule: it is the same rule with a `Managed` value supplied by more callers. Nothing in `stage.go` has to change — which is the single most important fact in this reconciliation, because it means the change is additive to a rule that is already property-tested.

### F2. `fugaro managed` has two subcommands, and `unflag` is not one of them

**Claim of the spec:** §10, that revocation is two manual writes today.

**What the code does.** `internal/cli/managed_apply.go:87`'s `newManagedCmd` registers `newManagedApplyCmd()` and `newManagedFlagCmd()` and nothing else. `internal/cli/managed_flag.go`'s own output prints the undo: delete `gs://<runs bucket>/fugaro/managed.json` yourself as the operator — *"no command of ours removes the marker in this release"* — and `gcloud projects update <gcp-project-id> --remove-labels=fugaro_managed`, which `docs/design/managed-projects.md` §10.2 and `plugin/skills/setup/reference/managed.md` both mark unverified until an operator has run it.

**Consequence.** The command the owner would expect to exist does not. D1020 is about building it, and the spec's §10 is written to that recommendation.

### F3. A project is managed on two marks, both required, and the read fails closed

**Claim of the spec:** §1.

**What the code does.** `internal/infra/managed/read.go`'s `IsManaged` / `ReadBudget`: the marker object `fugaro/managed.json` must exist, the **bucket's own project number** must be the project's number, the bucket must carry the installation's label, the project must carry `fugaro_managed=true` (`marker.go`'s `ProjectLabelKey`/`ProjectLabelValue`), and the object's `gcp_project` must be the project asked about. A version this build does not know is refused. `read.go`'s own comment is explicit that every caller must take `gcpProject` from the installation's own trusted configuration and never from a repository's `fugaro.yaml`, a local-config field a run can write, or an environment variable.

**Consequence.** The spec's §7 property — that a repository's content cannot flip the answer — is not a promise this design makes. It is the read's own contract, and the design's job is to not break it: every new caller of the gate takes `gcpProject` the same way `managed apply` does.

### F4. The two writers of the mark both refuse an agent's session and a run's own process

**Claim of the spec:** §3.2 and §7.

**What the code does.** `internal/cli/managed_flag.go:194` refuses a coding agent's session (`initflow`'s markers) and `:358–365` refuses a Fugaro run's own process (`managedFlagRunMarkers`: `FUGARO_RUN`, `CLOUD_RUN_JOB`, `CLOUD_RUN_EXECUTION`, `BATCH_TASK_INDEX`) — the second is what stops a run credential running the command inside a job. `fugaro setup --create-project --managed` is the other writer, and it is the owner's wizard. `internal/cli/creds.go:119` carries the same run-marker check, for the same reason.

**Consequence.** The mark's write path is already the boundary the spec's threat model needs (§8.2). D1021's question is only whether `unflag` is built to the same shape — and the answer must be yes, because a session that could remove the mark could also remove it mid-command.

### F5. The allowlist is derived, pinned and refuses computed values

**Claim of the spec:** §4.1 rules 7, and §8.1 and §8.3.

**What the code does.** `internal/infra/tf/managed_cover.go`: `ManagedPrefix = "fmgd-"` (`:52`), `managedTypes` (`:59`, fourteen entries), `managedDropped` (`:101`, seventeen entries **each carrying a required reason**, `google_project_iam_member` and `google_project_iam_custom_role` among them), `managedNames` (the attribute each type's name is read from), `managedGrantRules` (the five `*_iam_member` types), `managedPlanFields`, `managedConfiguration`, `managedProviderAllowed`, `managedProject`, `managedBilling`. `NotCovered` (`:305`) is the refusal list. Computed values are never trusted: an unknown attribute is a refusal, not a pass.

**Consequence.** Rules 4.1.3 (tables refused by name), 4.1.4 (no project-level IAM), 4.1.7 (no new provider or data path) and 4.1.8 (no foreign identity) are **already implemented and tested** — `TestManagedAllowedTypesIsDecided`, `TestManagedAllowedTypesGolden`, `TestManagedPrefixDoesNotMatchFugarosOwnNames`, `TestAllowedTypesCoverTheModules`, and the `TestManagedCover*` family. The design's §4.1 is not a proposal for new rules so much as a statement that the existing ones apply to more commands.

### F6. The owner's delete list, and the identity rule that cannot be retyped

**Claim of the spec:** §4.1 rule 1, and §4.3.

**What the code does.** `internal/infra/managed/deletelist.go` (`AllowDeleteObject = "fugaro/allow-delete.json"`, and `GuardAllowList` under a project-number and bucket-label proof); `internal/infra/tf/identity.go` writes the identity a removal's `before` view carries — `type`, `project`, `name`, `id` and the per-instance value where the type has one (a bucket's `time_created`, a Cloud Run job's `uid`, a secret's `create_time`) — for the owner to copy into `fugaro allow-delete`. The command `fugaro allow-delete` is itself agent-refused (`internal/cli/allow_delete.go:198`), which is the property that matters: **a session that could write the delete list could approve its own deletes.**

**Consequence.** Rule 4.1.1 needs nothing new, and §4.3's "print the identity byte for byte" is a rule the program already follows. The design's only addition is to say that the list is never an argument of any command in §3.1 — which is already true, since `--allow-delete` addresses the list rather than carrying it.

### F7. The managed apply's gate is a value in the build, and the inventory fails closed

**Claim of the spec:** §2.

**What the code does.** `internal/managed/enable.go`: `Enable` has unexported fields, `Budget()`'s and `Enable`'s zero values are closed, and `Refuse()` (`:89`) is the first thing `Apply` reads. The one opener is `ApplyGate`, whose acceptance is the answer of the apply-time budget read. `internal/managed/check.go`: `InventoryCheck`'s zero value is `read == false`, which **falls back** — `CheckedInventory` (`:46`) is the only way to say a read happened, and the file states in its own words that a caller who constructs a clean `Decision` by hand is lying about a read and no type here can prevent it; the wiring tests are what catch it (`TestApplyConsultsTheInventory`). `internal/managed/budget.go`, `apply.go`, `marker.go` (`MarkerVersion = 1`, `MarkerVersionBudget = 2`, `Budget{Amount, Currency}`, `MinBudgetAmount = 1`, `MaxBudgetAmount = 10_000_000`).

**Consequence.** D1021's answer ("the gate is a value in the build, never a flag or an environment variable") is already the code's own shape, and §2's claim that a caller who reads nothing refuses is a property `enable.go` and `check.go` already hold. What this design adds is a **third** zero-value-closed value: the per-command plan verdict.

### F8. The cost-bearing attributes of a plan are already blocked (D62)

**Claim of the spec:** §5 does not claim it, and it should.

**What the code does.** `internal/infra/tf/managed_cost.go` — `ManagedCosts(p)`, `ManagedCostRefusals(p)`, `ManagedCostEntries()`, over `managedCostList` (`:178`): the four properties the owner named (min instances above a stated constant, machine classes above shared-core, SQL tiers above shared-core, GPU families), each blocking or flagging by a decision, and each refusal **a fact a reviewer can check by reading the plan**. The file's own header refuses to price a plan at all (D62): *"a gate that pretends to price is a gate whose refusals nobody can check."*

**Consequence.** D1013's question is not "how do we bound a plan's cost" — that is answered. It is narrower and should be asked narrowly: **what bounds the cost that is not in a plan**, which for this design is the Cloud Build jobs `fugaro image refresh` starts. Saying that plainly is the correction this finding makes to the spec.

### F9. The model budget plane and GCP spend are different systems, and the design must not merge them

**Claim of the spec:** §5.

**What the code does.** `internal/budget` holds `config/caps/*`, `config/kill/*`, `config/limits` (`Limits.MaxReserveMicros` only), `config/mode`, `spend/*` and `runs/*`, in the Firebase Realtime Database. Enforcement is the **run's own `Session`**. `managed_cost.go`'s header repeats the separation from the other side: the budget amount the marker records is read at apply time (Task 4.4), and it is **not** an estimate.

**Consequence.** §5 is right to bound image builds through the marker and not through a cap, and the design must say — as it does — that it does not reconcile the two systems into one number, because finding 26 of the parent design says a number that pretends to would be wrong.

### F10. The kill switch is read fail-closed, and an unreadable switch counts as ON

**Claim of the spec:** §5.

**What the code does.** `internal/budget/kill.go`, and finding 26's own statement: a switch that cannot be read counts as ON. The switch lives in the Realtime Database, which is the owner's; no CLI write path in §3.1 touches `config/kill/*`.

**Consequence.** §5's "read before each build, not once per command" is the ruling the file already applies to a run, extended to a build a run does not make.

### F11. Where an audit record can outlive a quarter

**Claim of the spec:** §6.

**What the code does.** The runs bucket `fugaro-runs-<gcp-project>` is unversioned, holds `runs/` (deleted at 90 days), `builds/` (deliberately no expiry), `cache/`, `locks/`, `tokens/<slug>/<run>/budget-token`, and the operator-only-writable prefix `fugaro/` (`fugaro/project.json`, `fugaro/project-layer.yaml`, `fugaro/config.yaml`, `fugaro/recipes/`, `fugaro/managed.json`, `fugaro/allow-delete.json`). The job account's grants are conditioned to prefixes **below** `fugaro/`.

**Consequence.** `runs/` cannot hold an audit meant to outlive 90 days, and the bucket is unversioned so nothing there is tamper-proof. `fugaro/managed-audit/` under the operator-only prefix is the one place the record is written by the operator's credential and not by the job account — and `SECURITY.md:56` still means a project-level storage writer can rewrite it, which §6 and §8.2 say rather than imply.

### F12. The marker is versioned, which is what makes a new bounded field possible

**Claim of the spec:** §5.

**What the code does.** `internal/infra/managed/marker.go`: `MarkerVersion = 1` (a creation with no amount), `MarkerVersionBudget = 2` (the amount and currency), read through `ReadBudget`, which refuses a version it does not know. `Budget.Valid()` bounds the amount to `[MinBudgetAmount, MaxBudgetAmount]`.

**Consequence.** A version-3 object carrying `max_image_builds` is an additive change the existing reader already has the shape for — and, critically, **an older Fugaro reading a version-3 object refuses**, which is the fail-closed direction: a project marked by a newer CLI and managed by an older one keeps its confirmations rather than applying with no bound.

### F13. The ownership gate is one question, asked before any writer, and it is not a plan defect

**Claim of the spec:** §9.2.

**What the code does.** `internal/cli/init_rules_stage.go:177`, `budgetDBUnadopted(ctx, lc, db, declined, program)`: two halves **in order** — the config's own facts (`rulesUnavailable`), then the database's mark (`infra.DB.Check`) **always**. The one ownership question every writer into the budget database asks: the rules stage, the policy migration of `fugaro update`, `fugaro upgrade`'s policy step, the history job's rollover. An empty *unmarked* database with a separate Firebase project named is a **skip**, not a licence.

**Consequence.** A managed gate cannot stand in for this, and must not try: an ownership failure is a fact about *whose database this is*, not a defect in a plan an agent can fix. §9.2's ruling — ask it first, and keep the session stopped — is the only reading consistent with the code.

### F14. `fugaro update` is the whole upgrade, and it creates nothing

**Claim of the spec:** §9.1.

**What the code does.** `internal/cli/update.go`: `updateLong` states that update creates nothing, and `--forget` is refused for a fenced installation. `CLAUDE.md`'s HARD RULE makes the property normative: a release must not ship an operator step that is another command, a deletion or a manual repair, and a change that breaks an installed version ships its migration **inside** `update`.

**Consequence.** §9.1's ruling — every command in §3.1 keeps the HARD RULE's property, and the agent that may change a setting may also publish it — is the HARD RULE read in the one direction that matters here. A gate that stopped the agent at "now run `fugaro update` yourself" **would be** the forbidden step.

### F15. `fugaro config publish` and `fugaro project policy publish` exist, and each already refuses an agent's session

**Claim of the spec:** §3.1 and §4.2.

**What the code does.** `internal/cli/config_publish.go:31` (`Use: "publish FILE"`, the marker check at `:36`); `internal/cli/project_cmd.go:95` (`Use: "publish [--seed-repos] [--delete] [--yes] [--remove-routing]"`), whose own long text records that a publish refuses without the rules marker `config/limits/policyRules`, that `--delete` removes the children first and the shared node last so a launch mid-delete still sees the policy, and that `config/policy/repos/$slug` holds the **names** of a repository's admitted accounts.

**Consequence.** Every flag §4.2 rules out (`--delete`, `--seed-repos`, `--remove-routing`) is real, and the "the policy is read by a run that is launching" property is why a delete is refused **and why a widening is refused too**: the node bounds runs that are already starting, so an agent that widened it would widen what a run in flight is allowed to do.

### F16. `fugaro image refresh` is the one command in the set whose cost is not in a plan

**Claim of the spec:** §5.

**What the code does.** `internal/cli/image_refresh.go:365` — `--yes` "confirm[s] every step without asking … with **no cap** on how many builds run"; `:377` — `--yes` is refused in a coding agent's session, *"nothing is applied there, --yes included"*. Each build is a Cloud Build job: GCP spend, not model spend.

**Consequence.** This is the one place in §3.1, §4 and §5 where the design **creates** a cost rather than bounding one that exists: today an agent's session cannot reach the refresh at all, and after this design it can. D1013 exists for exactly that, and it is the only decision here whose answer changes what an installation spends.

### F17. The settings, rigor and accounts chains are designs approved for 0.15.0, and are not built

**Claim of the spec:** §3.1 and §4.2, which name `fugaro settings set`, `fugaro rigor set` and `fugaro accounts add`.

**What the code does.** `docs/settings-commands/` (SD1–SD18 answered 2026-10-31; decisions D840–D854 all answered as recommended; D854 the stale-local-copy guard, all answered 2026-10-31); `docs/rigor-command/` (D830–D839: D834 refuses `hotfix` as a default in any of its four forms and names `fugaro run --mode hotfix` as the operator's door; D838 addresses an area rule by its glob and refuses a duplicate glob; D900 makes the setup wizard ask the mode explicitly and **publish it in the same run**); `docs/accounts-add/` (D860–D872 answered 2026-10-31; `reservedClaudeAccountName` refuses `bitbucket-token`, `ValidClaudeAccountRef` accepts `default`, D868 makes `--yes` cover "everything", including a widening of `allow_data_to`, and says plainly it is *still refused in a coding agent's session*). `internal/cli/root.go:190` registers **no** `settings`, `rigor` or `accounts add` command: `internal/cli/accounts.go`'s `newAccountsCmd` is "read-only lookups over the local config".

**Consequence.** Two things the spec must say rather than imply: **the commands in §3.1 that do not exist yet are covered by this gate when they land**, and the gate's rules for them are those designs' own rules — the design here must not re-derive a registry, a rank rule or a reserved name that another design already owns and has already had answered. And **D868 is the precedent for §3.2**: `--yes` covering "everything" is still refused in an agent's session today, and this design changes that only for the named commands in a managed project.

### F18. The parent design's fence is shipped, and unverified live

**Claim of the spec:** §8.2.

**What the code does.** `internal/cli/recipes_skew.go:206` — `managedDeployerShipped = true` as of 0.10.0 — gates every managed entry point; seven scoped custom roles, one per service, each bound once under its own `fmgd-` prefix IAM condition, applied as a dedicated deployer service account holding no project-level storage role, no project-level `setIamPolicy` and no `iam.roles.*`. `plugin/skills/setup/reference/managed.md` states the fence has not been measured live, and `docs/gcp-live-checklist.md` Check 68 measures it: written, owner-run **after** the release, and **NOT RUN**.

**Consequence.** §8.2's honest statement — that the "the applying identity cannot write the marker" half of the forged-mark threat rests on an unproven edge — is inherited, and this design must not claim more. It does not propose to verify it: the brief forbids live steps, and Check 68 is the owner's.

### F19. The console is read-only and holds no credential; the report is an `ee` command reached through core

**Claim of the spec:** §9.4 and §6.

**What the code does.** `docs/design/desktop-console.md` (read-only by allow-list, no credential); `internal/cli/api.go:286` (`ReportIntentTimeout`), `:290`, `:299`, `:321`, `:338` — the moved report command's window, degraded path and by-mode bounds, re-exported from core for the `ee` report to read.

**Consequence.** §6's "the reader lives in core, the section lives where the report lives" is the tree's own arrangement and adds no core symbol for a report section. And the console gains nothing at all — which is a ruling, not an omission.

### F20. `fugaro doctor` is the surface a refusal reaches a person through

**Claim of the spec:** §6.

**What the code does.** `internal/cli/root.go:191` registers `newDoctorCmd()`; `recipes_skew.go`'s `managedDeployerShipped` and the skew checks show the shape a doctor check takes — a fact read from the installation, a sentence, and what to do.

**Consequence.** A doctor check that reads the last managed agent apply (§6) fits an existing surface rather than needing a new one. The design adds a **check**, not a command.

### F21. No test pins the set of `docs/` folders, and the one all-docs number walker is three digits wide

**Claim of the spec:** the existence of this folder at all.

**What the code does.** `internal/cli/docs_backlog_test.go` says in its own words: *"There is no registry test over `docs/` in this package: nothing pins the set of docs folders."* `internal/cli/docs_mobile_telemetry_test.go` walks every document with `telemetryDNumberRE = \bD([0-9]{3})\b` and refuses `D253`–`D260` outside its citeable set — **three digits**, so this design's four-digit decisions are invisible to it. `internal/cli/docs_compute_backend_test.go` walks all docs for one five-digit port number (permitted in exactly one plan, and written nowhere in this folder — this sentence named it once and the test caught it, which is the test doing its job) and for one release series plus a set of release words restricted to its named list.

**Consequence.** A new folder under `docs/` is safe, four-digit decision numbers cannot trip the one D-number walker, and the two literals F21 names are ones this design never writes. Checked rather than assumed, because the brief's wider pin gate runs `-run Docs` over the whole package.

---

## 2. Findings: what the spec inherits and does not touch

Stated separately, because the temptation to "fix while here" is the failure mode of a design like this:

| The parent's decision | What this design does with it |
| --- | --- |
| **D55** (`Managed{Managed, Admitted, Fallback}`, `Covers` true only for `YesOrTyped`) | **Reads it.** The gate here supplies the same value; no rule changes |
| **D59** (the whole-project inventory, and its fallback) | **Inherits it.** The fallback keeps the confirmations, which is the direction this design wants and must not weaken |
| **D61/D62** (the type list; cost flagged and blocked as a list, never an estimate) | **Reads it.** `tf.ManagedCosts` and `tf.ManagedCostRefusals` are the plan's cost control; D1013 is about the cost that is not in a plan (F8) |
| **D74** (the owner's per-address delete approval) | **Reads it.** Rule 4.1.1 is this design's restatement, not a new mechanism |
| **D75** (the deployer fence) | **Inherits it, unverified** (F18). The design adds no role, no binding and no identity |
| **D76** (`fugaro managed apply`) | **Leaves it exactly as it is.** §3.1 says so; no task names a file of it |
| **D77** (the managed state bucket) | **Reads it by name** in rule 4.1.3. Not touched |
| **D78** (the monitoring types dropped) | **Reads it.** The dropped list is the parent's; this design adds no entry to it |
| **D834, D838, D854, D868, D900** | **Inherits them as the gate's own rules** for those commands (F17), rather than re-deriving a rank rule, a glob rule, a generation guard or a reserved name |
| **The #1460 ownership gate** (`budgetDBUnadopted`) | **Unchanged and first** (F13). Never replaced by a plan verdict |

---

## 3. The owner decisions D1011–D1026

Each row: the question, the recommendation, and what each alternative costs. **No decision here is answered.** The spec is written to the recommendation, so "as recommended" needs no rewrite.

| # | Question | Recommendation | What the alternatives cost | Answer |
| --- | --- | --- | --- | --- |
| **D1011** | **Which commands may the agent run in a managed project, and which may it never?** | **The set is: `fugaro update`, `fugaro config publish`, `fugaro project policy publish`, `fugaro image refresh`, and the `set`/`add`/`rm` of the settings, rigor and accounts designs when they land.** `fugaro managed apply` stays exactly as it is. **Never, managed or not**: `managed flag`/`unflag`, `creds set` and every token entry, anything writing the managed label, billing, deleting the project, IAM widening (`--launcher`/`--operator`/`--budget-admin`, `allow-delete`), `fugaro restore`, and `fugaro setup` itself. The rule behind the list, so a later command is decided rather than argued: **a command is in the set only when its whole effect is visible in a document it produces before it writes.** | **(b) The whole CLI in a managed project:** a session could enter a secret, widen IAM or delete the project, and the gate would have nothing to read; §3.2's list is what makes the design a boundary rather than a gesture. **(c) `update` only:** the smallest set, and the owner still drives every publish, image and setting — half the ruling's point. **(d) The set plus `setup`:** `setup` creates a project, links billing and records the budget amount; those are owner decisions at their own terminal (D900's answer is the same shape), and the agent's door after `update` exists. **(e) The set minus `image refresh`:** costs nothing to add later and leaves the one command whose cost the design cannot see in a plan (§5, F16) — so it is in, **with D1013's bound**, not out | |
| **D1012** | **What are the gate's rules, per command, and where do the per-command rules come from?** | **§4.1's eight shared rules** over the plan (additive converge only; a change judged on what it produces, not its action name; tables and databases refused by name; no IAM beyond the installation's own accounts; no widening of an allow list; no cap increase; no new provider or data path; the installation's own identity), **plus the per-command rules of §4.2, which are the owning design's own rules** (the registry, the generation guard, D854; the policy node's generation and its `--delete`/`--seed-repos`/`--remove-routing` refusals; the release digest and skew rules; the rigor rank rule, D834's hotfix refusal and D838's glob rule; the accounts reserved names, reference checks and `allow_data_to` rule). Fail-closed on an unreadable plan, a missing configuration, an unknown field or an unresolvable value. | **(b) One rule set for every command:** the gate would have to re-derive a registry, a rank rule and a reserved name that another design owns and has already had answered (F17) — the drift is the cost. **(c) The rules left to each command to enforce as it goes:** the gate stops being one predicate over one document and becomes N behaviours, and §8.5's wiring tests stop being able to name what they pin. **(d) Admitting an unknown plan field instead of refusing it:** a field a later Terraform adds would be admitted unread — the exact hole `tf`'s `knownActions` and `managedPlanFields` exist to close | |
| **D1013** | **What bounds the cost of billable image builds in an unattended run?** | **A `max_image_builds` count recorded in a version-3 `fugaro/managed.json`**, written only by the owner's own paths (`setup --create-project --managed` and `managed flag`), read through the same number-and-label proof as the rest of the object, **fail-closed** — an unknown version, an out-of-range value or an unreadable object is a refusal, and **a version-1 or version-2 object (no recorded bound) is read as the default, one build per invocation, never as "no bound"**. The RTDB kill switch (unreadable counts as ON) and the recorded amount are re-read before **each** build, not once per command. | **(b) Leave the refresh as `--yes` leaves it — no cap:** one Cloud Build job per repository per invocation, from a session whose only other cost control is a switch the owner may not have set; a retry loop, a stale checkout and a busy agent are each enough (F16). **(c) A project-layer key:** the command being gated (`config publish`) is the command that would write it — a bound the bounded party can raise. **(d) A hard GCP spend stop:** the parent design's finding 25 and D63 say what that costs in privilege, and this design adds none of it; **(e) A cost estimate of the plan:** `managed_cost.go`'s own header refuses to price a plan at all (F8), and a number this repository invented would be wrong in every installation but one | |
| **D1014** | **Is every managed agent apply recorded, and where, and who reads it?** | **Yes — one object per applied command at `gs://fugaro-runs-<gcp-project>/fugaro/managed-audit/<utc-date>/<id>.json`**, written with the **operator's** credentials (never the job account's, whose grants are conditioned below `fugaro/`), holding the fields §6 lists — command and argv with no secret-bearing argument (the gate admits none), the **plan digest** the gate read, the verdict and rules, the marker version and generation, the delete-list generation, the agent marker, the identity, the timestamp. **Written for a refusal too, and a failure to write it fails the command.** Read by a new `fugaro doctor` check and a new `fugaro report` section. | **(b) Under `runs/`:** deleted at 90 days, and the bucket is unversioned, so the record a quarter-old audit needs is gone (F11). **(c) Only on success:** the refusal is the record an owner most wants, and an unattended refusal that leaves no trail is indistinguishable from a command never run. **(d) Written by the job account:** its grants are conditioned to prefixes below `fugaro/`, so it cannot write this prefix — which is the point, and also why the command itself must do it. **(e) Called tamper-proof:** it is not — `SECURITY.md:56` means any project-level storage writer can rewrite it; §6 says so and §8.2 does too | |
| **D1015** | **How is the agent told it is in a managed project, and why can repository content not flip it?** | **It reads the marks, per command, through `internal/infra/managed`'s own read — never an environment variable, never a file in the repository, never a cached answer — with `gcpProject` taken only from the installation's own trusted configuration** (`internal/localcfg` or the published shared config), as `read.go` requires of every caller. §7 states the mechanism rather than the promise: the only writers are owner-run, the read proves the bucket's own project number, the label can only narrow, and the project the read is asked about never comes from the repository. | **(b) An environment variable:** a variable about the *session*, not the project — a session can set it, unset it and inherit it, and a variable meaning "managed" means "skip your confirmations", which inverts the control. **(c) A field in `fugaro.yaml`:** repository content the agent writes and opens pull requests in (`SECURITY.md:81`). **(d) Cache the answer in the run record:** an owner who unflags mid-command keeps the permission for the rest of the command, which is the window D1024 is about — and caching makes it the whole run. **(e) Let a flag open it:** the session that benefits is the session that can pass it | |
| **D1016** | **How does this sit with the HARD RULE — `fugaro update` is the whole upgrade?** | **`update` is in the set, and every other command in the set keeps the HARD RULE's property: it makes its local change and the same run's `update` publishes it, or it *is* the update.** §4.2 gates `update`'s stages individually (`tf.Cover` plus `tf.Guard` over the embedded roots; the rules stage against the binary's own generation; the policy stage against the ownership gate and D854; the secrets stage never; Credentials and Compute stay `noYes`). | **(b) Gate `update` as one opaque step:** the biggest-stakes command in the set with the least visible gate, and the one whose stages each carry a different rule. **(c) Leave `update` out of the set:** the agent that may change a setting may not publish it — the forbidden second command, one step later. **(d) Admit the secrets and Credentials stages:** a secret is a value no plan can show (§3.2), and an identity and money-bearing infrastructure are not a plan the gate can bound | |
| **D1017** | **How does this sit with the ownership gate (#1460, `budgetDBUnadopted`)?** | **Unchanged, unconditional, and asked *before* the managed gate rather than instead of it.** A managed project whose config names a Firebase project that is not the installation's, or a database carrying another installation's mark, is refused or skipped exactly as today, in a session and in a person. The refusal names the owner's own command and the session stops. | **(b) Let a clean plan verdict stand in for it:** an ownership failure is a fact about whose database this is, not a plan defect an agent can fix (F13) — and the gate would then be licensing a write into someone else's database. **(c) Ask it after the plan is judged:** the plan would be read against a database the run is about to refuse, which wastes an inventory read and prints a verdict about a write that cannot happen. **(d) Weaken it in a managed project:** the mark is not a statement about ownership; the parent design says so and the code asks both halves every time | |
| **D1018** | **How do the settings, rigor and accounts `set` chains inherit the gate?** | **Each `set` that writes outside a repository inherits this gate rather than growing its own, and is admitted as *one write* with `update`'s publish.** A repository-scoped `set` is allowed today and stays allowed (settings SD9). A floor-removing or ceiling-widening `set` keeps its typed project name, which has **no agent answer** in a managed project either. The gate's per-command rules are those designs' own rules, not new ones (F17). | **(b) A separate gate per chain:** three gates for one property, and the drift between them is the cost. **(c) Admit a floor or ceiling change on a clean plan:** how much review a project gets is not a plan defect, it is the owner's decision, and D900/D834 say so from the other side. **(d) Let a repository-scoped `set` stay agent-refused:** it is allowed today, and refusing it in a managed project would be this design taking a permission away — the opposite of the ruling | |
| **D1019** | **What do the console and `fugaro setup` get?** | **The console gets nothing** — it is read-only by allow-list and holds no credential, and a managed project does not change that. **`fugaro setup` stays the owner's wizard**: it is where a project becomes managed, its questions keep their terminal, and none of its Typed steps (a new project, billing, a secret, a Firestore location, a billable first build) gains an agent answer. The agent's door after this design is `update`. | **(b) A console write path:** the console's whole design is that it holds no credential; adding one for a managed project inverts it, and no console task is proposed here. **(c) `setup --managed` in the skill:** the one command that *creates* the mark, in an agent's session — the mark is what turns the refusal off (F4, §7). **(d) An agent-answerable `setup`:** its Typed steps are money and permanence, which `Covers` already refuses (F1) | |
| **D1020** | **How does the owner revoke managed mode, and what happens when they do?** | **Build `fugaro managed unflag PROJECT_ID`.** Owner-run, refused in a coding agent's session **and** from a run's own process (the same two checks `flag` carries, F4), the project's exact ID typed at the terminal, no `--yes`, no `--prefix`, no wildcard, no bulk form. It removes **both** marks in one command, says which half was already gone rather than failing on it, records the revocation in §6's audit family, and **does not remove the deployer fence**. | **(b) Keep the two manual writes:** two commands in two tools, one of them UNVERIFIED, and it leaves the half-removed state the parent design already had to write §10.2 about. **(c) `unflag` removes only the object:** the label would stay, and the label from the read's own direction is the one that can only narrow — an owner would think they had revoked and had not. **(d) `unflag` also tears down the fence:** the fence is D75's, `update --forget` is refused for a fenced installation, and a revocation command that removed IAM would be the IAM-widening door §3.2 closes. **(e) No command, document the gcloud line only:** it is UNVERIFIED, and the HARD RULE's spirit — one documented command, no manual repair — is against it | |
| **D1021** | **Where does the gate live, and what is its shape in the build?** | **In `internal/managed`, beside `Enable` and `InventoryCheck`, with the same shape: a type with unexported fields, one constructor, a safe answer at the zero value, the whole behaviour in one predicate and one sentence.** A caller that reads nothing refuses. The per-command plan rule lives in `internal/infra/tf` where the plan's other rules live (`ManagedCover`, `ManagedCosts`, `Guard`). | **(b) A flag or environment variable:** the session that would benefit is the session that can set it (F7, D1015). **(c) An exported struct a caller fills in:** the zero value would be the *admit* direction, which is what `check.go`'s header exists to prevent. **(d) Inside `internal/cli`:** the CLI would hold the security predicate and `ee` or another caller could not reuse the read, and the package that writes would also be the package that judges | |
| **D1022** | **What *is* "the plan" for a command that produces no Terraform plan, and how is the judged plan proved to be the applied one?** | **The document the command already produces before it writes** — a saved Terraform plan for the Terraform commands; the parsed layer for `config publish`; the parsed policy node for `policy publish`; the per-build list for `image refresh`; the diff the `set` commands already print. The gate **hashes the exact bytes it read** and §6 records the digest, so the judged document and the applied one are provably one file. | **(b) Judge a re-rendered document:** between the render and the write the world can move — the digest is what makes "judged" and "applied" the same artifact rather than two documents that happened to agree. **(c) Judge the command's arguments only:** the arguments are not the effect; `--seed-repos` and a layer key both look unremarkable on a command line and are the change the rules are about. **(d) No digest in the record:** an audit that says "a plan was judged" without saying which one cannot be checked against anything | |
| **D1023** | **What does a refusal say, to the session and to the owner?** | **Two sentences.** To the **session**: the command, the rule that refused, and the fact that **no person is being asked** — naming **no variable, flag or marker** a reader could change (`IDEHint`'s rule, `stage.go`). To the **owner**: the **one command they run themselves**, the same command they would have run before this design existed; where the refusal is an unapproved delete, the identity the plan's `before` view carries, printed **byte for byte** in the order the owner's list holds it (F6). | **(b) One sentence naming the flag that would override it:** that is the shape the refusal exists to avoid, and `stage.go` refuses it for `IDEHint` already. **(c) Print the plan's address as the owner's key:** a retyped or truncated identity matches nothing (the skill's own warning about the 512-character cut), so the delete stays refused and the owner learns the wrong lesson. **(d) Tell the agent to ask its owner in prose:** an unattended run has nobody to ask, and a prose refusal is not actionable — the owner's command is | |
| **D1024** | **Is the mark read once per command or re-read before the write, and what about a revocation racing a command?** | **Read per command, never cached, and re-read immediately before the write**, so the gap a revocation can fall into is the plan-to-apply interval and not the whole command. The audit records `marker_generation` so the owner can see which fact was in force. **The design claims to narrow the window, not to close it.** | **(b) Read once at the start:** an owner who unflags in the middle keeps the permission for the rest of the command. **(c) Claim the window is closed:** a revocation that is a lock would need the write and the read to be one transaction, which neither the object nor the label offers, and claiming otherwise is the kind of overclaim the threat model exists to prevent. **(d) A per-write re-read for every resource:** the mark is a property of the project and the command, not of each write; N reads buy nothing the one re-read does not | |
| **D1025** | **Is the audit written only for admissions, and is anything recorded for a project that is not managed?** | **Written for admissions *and* refusals, and written for a managed project only.** A refusal is recorded because an unattended refusal is exactly what an owner wants to see; a project that is not managed writes **nothing** — the ordinary typed confirmations are already a person's decision, in the terminal, and a log of them would be a new data path for a property this design did not create. | **(b) Admissions only:** a run that refused every command and then reported "done" leaves no trace of why nothing happened. **(c) Record every project:** a new write on every ordinary installation, for a property (`managed`) that is false there — and it is the one direction the read's own fail-closed rule exists to keep narrow. **(d) Record in the run record:** a run record is per-run and does not outlive a command run by hand at a terminal | |
| **D1026** | **When the gate's verdict and the stage's confirmation class disagree, which wins?** | **The class wins.** `Covers` is true only for `YesOrTyped`, so a **`Typed` step is never admitted by a plan gate**, in a managed project or anywhere else — the existing rule, unchanged (F1). A `noYes` stage (Credentials, Compute) is likewise not admitted, and a stage with **no** confirmation at all needs no gate. The gate can only **narrow**: a step the table refuses a `--yes` for never becomes agent-runnable because a plan looks clean. | **(b) Let a clean plan admit a Typed step:** `Typed` is money or permanence by definition, which is the one thing a plan gate cannot judge (F1, D1013's own boundary). **(c) Let the gate override a `noYes` stage:** those stages create an identity and provision money-bearing infrastructure, and the design of record puts them outside a plan (F17, §4.2). **(d) Let the gate widen as well as narrow:** a plan verdict is a statement about a document, not a licence to grant a class the table refused | |

**D1027–D1040 are reserved and unused.**

---

## 4. The number block

Read on **2026-11-01**, above every `D<number>` **on `main` and in every open pull request**, by fetching each branch and reading it rather than trusting a neighbouring design's table.

| Where | Block | Read how |
| --- | --- | --- |
| `main` at `15f4d8ab` | highest **written** is **D900** (`docs/rigor-command/reconciliation.md`, the setup wizard's rigor question); `docs/rigor-command/README.md` names **D901** as the next task's number | `git grep` over the tree, then reading the files the matches name |
| **#1472** `docs/cloud-senior-review` | D880–D899 | branch fetched, README read |
| **#1481** `docs/run-without-checkout` | D901–D919 | branch fetched, README read |
| **#1485** `docs/run-limit-carry-over` | D901–**D920** — **overlaps #1481, and neither is this work's to fix** | branch fetched, README read |
| **#1487** `docs/mcp-server` | D920–D934 | branch fetched, README read |
| **#1488** `docs/plugin-user-scope` | D940–D953 | branch fetched, README read |
| **#1490** `docs/periodic-reviews` | D960–D979 | branch fetched, README read |
| **#1492** `docs/workspaces` | D980–D995 | branch fetched, README read |
| **#1491** `docs/binary-allow-rule` | **D981–D1010** — the highest claimed anywhere | branch fetched, README read |
| Every other open pull request | no new block: code, or a document that writes none | branch list read |

**Highest number claimed anywhere: `D1010`.** The next free block above it is **D1011–D1040**, the thirty-wide width `docs/ee-reorg` took for D520–D549 and `docs/secrets-brokering` for D490–D519 before it. **This work takes D1011–D1040: D1011–D1026 written here, D1027–D1040 reserved.**

Two notes a later task needs, recorded rather than acted on:

- **The false positive.** A scan for four-digit numbers is a scan for `D1…`, and `UID1000` appears in `docs/plans/2026-09-30-m9a-gateway-and-project-identity.md`. It is not a decision; `docs/models-commands/README.md` records the same shape of false positive for `D800` (`U+D800`).
- **The overlap in the open pull requests** (#1481 and #1485 both write D901–D919/D920) is **noticed and not done here**: both are other runs' pull requests, and the brief reserves their files. Whoever merges second re-checks.

---

## 5. The residual: what this design does not close

Written here and not softened, because a design that reads as a wall is worse than one that says where it stops.

1. **The agent that goes around the program.** Unchanged and inherited: the agent shares the container, the user and the metadata server with the runner; it can run `terraform` or `gcloud` directly, write the marker with the operator's own credentials, unset its own marker and run any command here itself. `SECURITY.md` places an agent doing harmful things in its own container with its job account's credentials **out of scope**. This design decides what a session **through the program** may apply; what it stops is the accident and the injection. `internal/cli/managed_apply.go`'s own header says the same about the gate it already has.
2. **The plan an agent authored is attacker-influenced.** In `managed apply` the configuration under review is the agent's, written in a repository an injection can reach — so the allowlist is not one control among several, it **is** the boundary, and every entry on it is a decision with a stated cost.
3. **The fence is unverified live.** D75's seven scoped roles and their conditions are shipped and the "the applying identity cannot write the marker" half of the forged-mark threat rests on an edge GCP has not been measured to honour (Check 68, written, owner-run, NOT RUN). This design inherits the edge and does not claim to close it.
4. **The audit is not tamper-proof.** The object is written with the operator's credentials into an unversioned bucket any project-level storage writer can rewrite (`SECURITY.md:56`).
5. **The revocation window.** The mark is read before the write; a revocation racing a command in flight lands at the next read. Narrowed (D1024), not closed — closing it would need a transaction neither mark offers.
6. **No hard stop on cloud spend.** §5 bounds the one cost this design creates (billable image builds) and claims no more; a real GCP spend stop costs privilege the parent design's finding 25 and D63 put out of scope.
7. **A gate bug that admits.** Fail-closed zero values, a derived and pinned allowlist and named wiring tests make an accidental opening hard; a caller that constructs a clean verdict by hand is lying about a read and no type in the package can prevent it — the tests that pin the wiring are what catch it.
8. **Model spend is a different system.** The gateway's cap, `config/caps/*`, `config/kill/*` and `config/limits` govern the model; GCP spend is not them and is never reconciled into one number with them.
