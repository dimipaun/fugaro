# The design: the agent manages a managed project, behind a deterministic gate

**Status: a proposal. Every decision is a recommendation, and the owner has answered none of them.** The decisions are **D1011–D1026** in [reconciliation.md](reconciliation.md) §3; this document is written to the recommendation in each, so an answer of "as recommended" needs no rewrite. Where this document and [reconciliation.md](reconciliation.md) disagree, the reconciliation wins: it is the file that holds what the code actually does.

**The one sentence.** In a **managed** project — both marks present, and each of them the owner's — a coding agent's session runs the owner-level cloud-changing commands itself, and what stands where the typed confirmation stood is a **deterministic gate over the plan of the command being run**, never a model's verdict, never a prompt, and never a flag the session can pass to reach past it.

## 1. What "managed" is, and what the two marks are

A project is managed **only** when both are true, and either one alone is "not managed":

1. **The marker object**, `gs://fugaro-runs-<gcp-project>/fugaro/managed.json`, read by `internal/infra/managed.IsManaged` / `ReadBudget` (`internal/infra/managed/read.go`). The read proves the bucket before it believes the object: the bucket's own project number must be the project's number, the bucket must carry the installation's `fugaro=managed` label, and the project must carry the label below. The object is versioned — version 1 (a creation with no amount), version 2 (a creation or flag that recorded the monthly budget alert amount) — and a version this build does not know is refused.
2. **The project label** `fugaro_managed=true` on the GCP project itself (`internal/infra/managed/marker.go`'s `ProjectLabelKey`/`ProjectLabelValue`), written by the creating run or by `fugaro managed flag`, in the same step that writes the object.

**The label can only narrow.** It is an agreement condition on the object, never a substitute: a project with no label, a label with any other value, and a project the read cannot fetch are each **not managed**, whatever the runs bucket holds. That is the direction the mistake has to fall in — a label that could widen would be a way for anything holding `resourcemanager.projects.update` to talk a run out of its confirmations.

**Which project the read is asked about.** The read's verdict rests entirely on the `gcpProject` it is handed, so every caller of the gate takes it from the **installation's own trusted configuration** — the local project config (`internal/localcfg`), or the shared config the installation published — and never from a repository's `fugaro.yaml`, from a local-config field a run can write, or from an environment variable (`read.go`'s own warning, which this design inherits rather than restates). This is the property that makes a hostile repository unable to flip the answer, and §7 says why.

## 2. What changes: the refusal the gate replaces

Today (`internal/initflow/stage.go`):

- `CanConfirm` returns `false` for **every** class when `Conditions.Agent` is a coding agent's marker, and `AgentRefusal` names the marker and never offers `--yes`.
- `CanConfirmManaged(class, c, m)` is the same function with the managed case added: a step proceeds under an agent's marker only when `m.Removes(class)` — the project is managed, the gate admitted **this step's** plan, the class is one the gate judges (`YesOrTyped`; a `Typed` step is money or permanent and no plan gate speaks about it), and the whole-project inventory did not fall back (D59).
- Exactly one caller threads a non-zero `Managed`: `fugaro managed apply` (`internal/cli/managed_apply.go`, through `internal/managed/apply.go`).

This design generalises that one caller into **one gate used by a named set of commands**, and changes nothing else about the rule:

- A command in the set, in a managed project, whose plan the deterministic gate admitted, **runs** in an agent's session and prints `initflow.AgentProceeded` — the sentence `initflow/stage.go` already carries for exactly this case, which names the marker, says what the step proceeds on, and names no variable or flag a reader could unset to get there.
- Every other combination keeps `AgentRefusal` **word for word**.
- The gate is a **value in the build**, never a flag and never an environment variable: the session that would benefit from opening it is the session that can set one. `internal/managed/enable.go`'s `Enable` is the precedent — unexported fields, one constructor, the safe answer at the zero value — and every gate added here is built the same way, so a caller that reads nothing refuses (D1021).
- **The gate can only narrow.** A step the stage table gives no `--yes` for — a `Typed` step, a `noYes` stage — is not admitted by a clean plan verdict, in a managed project or anywhere else; that is D55's `Covers` rule, unchanged (D1026).

## 3. Which commands the agent may run, and which it never may (D1011)

### 3.1 The set the gate covers

| Command | What the gate reads as "the plan" | The gate's source of truth |
| --- | --- | --- |
| `fugaro update` (the installation's and a checkout's re-converge) | each stage's saved Terraform plan, and each non-Terraform stage's own dry-run document | `tf.Guard` + `tf.Cover` over the embedded roots; the per-stage rules of D1012 |
| `fugaro config publish FILE` (the project layer) | the parsed layer document | the keys and defaults registry and the layer's own schema (settings S1/S2), the generation guard, the executable-key guard |
| `fugaro project policy publish` (the policy node) | the parsed policy node | the policy model's own version and the rules generation it is read under |
| `fugaro image refresh` (derived images) | the refresh's own per-build list | the release's base digests, the skew rules, the build bound of D1013 |
| `fugaro settings set` / `settings unset` | the diff the command already prints | the registry, the scope rules of D1012, the floor/ceiling rule |
| `fugaro rigor set` | the diff the command already prints | `internal/rigor`'s own validation, the rank rule, the hotfix refusal |
| `fugaro accounts add` / `accounts rm` | the account definition and the coverage check | the reserved names, the reference check, the `allow_data_to` rule |

**`fugaro managed apply` stays exactly as it is.** It is not in this table because it needs no change: it is the door the agent-authored root already has, with the gate of D76, the owner's delete list of D74, the deployer account of D75 and the inventory of D59. This design cites it as the parent and leaves every line of it alone.

### 3.2 The set the gate never covers

These are refused in a coding agent's session in a managed project exactly as they are today, and no gate, flag, mark or configuration changes that:

- **`fugaro managed flag` and `fugaro managed unflag`.** The mark is what turns the refusal off, so the command that writes it is the owner's by construction. Both already refuse an agent session and a run's own process (`managed_flag.go`'s two checks), and the second must be built to the first's shape (§6, D1020).
- **`fugaro creds set`, `fugaro secrets set`, and every token entry.** A secret is a value; the gate judges plans, and no plan can show a secret's bytes without putting them in the plan. The secrets stage of `fugaro update` therefore keeps its hidden prompt and its `fugaro secrets set` hand-off even in a managed project.
- **Anything that writes the managed label itself** — including `gcloud projects update … --update-labels fugaro_managed=true` and every other path to a project-label write.
- **Billing.** `fugaro setup --create-project --billing-account`, the `--link-billing` step, and any write to a billing budget or a billing account. The recorded monthly amount is a fact the marker carries, not something the agent may set or change.
- **Deleting the project**, and destroying the installation. `fugaro update --forget` and any address that removes the installation's own objects stay owner-run, and the retired project-retirement flow keeps its own refusals — this design adds none and lifts none.
- **IAM widening**: `--launcher`, `--operator`, `--budget-admin`, and `fugaro allow-delete` (the command that writes the owner's delete list — a session that could write that list could approve its own deletes).
- **`fugaro restore`**, which refuses an agent session today and keeps it.
- **`fugaro setup` itself**, including `--create-project --managed`: creating a project, linking billing and recording the budget amount are the owner's decisions at their own terminal (D900's answer for the rigor question is the same shape). The agent's door is `update`.

The rule behind the list, so a later command is decided rather than argued: **a command is in §3.1 only when its whole effect is visible in a document the command produces before it writes** — a Terraform plan, a parsed layer, a policy node, a per-build list, a diff. A command whose effect is a secret, an identity, a billing relationship or a deletion is never in the set, because there is nothing for a deterministic gate to read.

## 4. The gate's rules, per command, from the plan (D1012)

### 4.1 The rules every command shares

**What "the plan" is** (D1022): the document the command already produces before it writes — a saved Terraform plan for the Terraform commands, the parsed layer for `config publish`, the parsed policy node for `policy publish`, the per-build list for `image refresh`, the diff the `set` commands already print. The gate **hashes the exact bytes it read** and §6 records the digest, so the document that was judged and the document that was applied are provably one artifact and not two that happened to agree.

Read over that document, before the write, fail-closed — an unreadable plan, a plan with no configuration, a field the gate does not know, and a value the gate cannot resolve at plan time are each a **refusal**, never a pass:

1. **Additive converge only.** Creates and in-place updates. A **delete** or a **replace** is refused unless an entry of the owner's own list (`fugaro/allow-delete.json`, D74) names the exact address *and* the identity of the object the change removes — `tf.Approves`' rule, matched byte for byte. The list is never an argument of the command.
2. **A change is judged on what it produces, not on its action name.** A resource the plan *changes* is judged on its `after` view by the same rules as a resource the plan creates, and a resource the plan did not create must carry the managed identity the rules below require *and* be in this project. An action set of `["update"]` whose before/after differ in the resource's own identity (a bucket's name, a job's name, a secret's id) is refused as a replace.
3. **No deletes of data, buckets or databases.** Beyond rule 1: the runs bucket, the installation's state bucket and D77's managed state bucket are refused **by name** before any prefix is read; a bucket or an object whose retention is locked is refused whatever the list says, because a lock can never be undone.
4. **No IAM change beyond the installation's own service accounts.** A project-level IAM member is refused outright (the managed list drops `google_project_iam_member` and `google_project_iam_custom_role` entirely); a grant on a resource is admitted only on a resource this plan's own rules admit, only to a member the review listed or a managed account, and only with the role spelled out.
5. **No widening of an allow list.** A change whose effect is to admit more traffic, more callers, more repositories or more data than before is refused: an invoker or object grant that adds a member, a routing row that adds a destination, `project policy publish --seed-repos`, `--remove-routing`, and a layer change that adds a repository to a profile's scope.
6. **No increase of a budget cap, and no write to one.** Caps and kill switches live in the Realtime Database and are the owner's. A command that would raise a cap is refused, whichever command it is.
7. **No new provider and no new data path.** A provider block that is not Google's two, a provisioner, a data source, an object uploaded from a local path, an action, a deferred change and an ephemeral resource are each refused (`ManagedCover`'s existing rules, `managedConfiguration`, `managedPlanFields`).
8. **The identity that applies is the installation's own.** An apply runs as the deployer account (D75) where the root is agent-authored, and as the operator's own credential where the thing being applied is not a Terraform root; a plan that names another account, another project or another billing account is refused.
9. **A plan's cost-bearing attributes are flagged and blocked, and no rule here prices a plan.** `internal/infra/tf/managed_cost.go` (`ManagedCosts`, `ManagedCostRefusals`, `managedCostList`) is the existing control: the four properties the owner named — min instances, machine classes, SQL tiers, GPU families — each blocking or flagging by a decision, each refusal a fact a reviewer can check by reading the plan. This design reads that list and adds no entry to it, and it never estimates a cost: a gate that pretends to price is a gate whose refusals nobody can check (D62). What is **not** in a plan is §5's, and it is the only cost this design creates.

### 4.2 The rules per command

**`fugaro update` (D1016's other half).** The door the HARD RULE makes the whole upgrade is the door the agent must be able to run, so its stages each carry a gate rather than the run carrying one confirmation with no view:

- The Terraform stages (Installation, Firebase, Images, Installation2, Repository) are judged by `tf.Cover` over the embedded roots plus `tf.Guard` with the owner's list — the same pair `init` uses, because the roots are Fugaro's own modules and not the agent's.
- The **rules** stage is admitted only when the generation it deploys is this binary's own (`projectpolicy.RulesVersion`); it is a document the binary carries, so the plan *is* the generation.
- The **policy** stage is admitted only when the migration's own guards hold: the database is this installation's (`budgetDBUnadopted`, #1460 — §5, D1017), and the published node's generation has not moved past the local file's (`D854`).
- The **secrets** stage is never admitted, per §3.2: it does no cloud write in an agent's session beyond what it already leaves for the owner.
- The **Credentials** and **Compute** stages are `noYes` in the stage table today (`internal/initflow/stage.go`'s `order`), and they stay `noYes` under a managed project: they create an identity and provision money-bearing infrastructure, and neither is a plan the gate can bound.

**`fugaro config publish FILE`.** Admitted only when every key is in the registry, no key is one of the reserved ones (`credentials.*`, `clone_urls`, `budget.*`, `agent.oauth_token`, `governance.*`), no executable key changes (`commands.*`, `image.apt`, `image.setup` without the owner's `--executable-changes`), the diff widens no scope rule, and the local file's recorded generation is the published one's (D854). Otherwise refused, and the refusal names the owner's command.

**`fugaro project policy publish`.** Admitted for a node under the deployed rules generation, whose `repos` list is unchanged or narrower, whose routing table is unchanged, and whose credentials block is unchanged. `--delete`, `--seed-repos` and `--remove-routing` are never admitted.

**`fugaro image refresh`.** Admitted per build: the base image's digest is the release's (with `--expect-digest` satisfied where it is given), the derived tag moves in the direction the skew rules allow, the build account is the project's own, and the count is within the bound of §5 (D1013). A base copy to a new registry, a new repository and a tag replacement that is not this release's are refused.

**`fugaro settings set|unset`.** Admitted only for a key the registry knows, at a scope inside the project (never `--layer local` writing outside it), where the new value does not lower a floor or widen a ceiling (which the designs give the typed project name and no agent answer) and the target is not one of the reserved keys above.

**`fugaro rigor set`.** Admitted only for a mode that ranks at or above the resolved mode, never `hotfix` (D834's refusal stands), with no duplicate area glob (D838), and only through the layer write the same run's `update` publishes (F18's HARD RULE).

**`fugaro accounts add|rm`.** `add` is admitted only for a name that is not reserved and a definition whose `allow_data_to` does not widen; `rm` only when no profile and no repository still references the account. No token value is ever written by either.

### 4.3 The refusal the agent reads

Two sentences, and their shape is a decision (D1023):

- **For the session**: the refusal names the command, the rule that refused, and the fact that a person is not being asked. It names **no variable, flag or marker** a reader could change to get past it (`IDEHint`'s rule, `internal/initflow/stage.go`).
- **For the owner**: the refusal carries the **one command the owner runs themselves** — the same command they would have run before this design existed — so an agent's report to its owner is actionable without the owner reading the design. Where the refusal is a delete the owner did not approve, it prints the identity the plan's `before` view carries, in the order the owner's list holds it, byte for byte: a retyped identity matches nothing and the delete stays refused.

## 5. The cost bound on billable builds (D1013)

`fugaro image refresh` starts **Cloud Build** jobs, which are GCP spend and not model spend. They are a different system from the model caps and always will be (`docs/design/managed-projects.md` finding 26): the gateway's cap governs the model, `config/caps/*` and `config/kill/*` govern a run's model spend, and nothing today bounds the number of images a session may build — `--yes` "covers every build here, with **no cap** on how many run" (`internal/cli/image_refresh.go`).

A **plan's** cost is already controlled, and not here: §4.1 rule 9's `tf.ManagedCosts` flags and blocks the cost-bearing attributes the owner named, and D62's answer is a list of properties a reviewer can check rather than an estimate (reconciliation F8). What that leaves is the cost that is **not** in a plan, and in this design's set there is exactly one: the Cloud Build jobs a refresh starts. That is what this section bounds.

The bound, and where it lives so the agent cannot move it:

- **The count comes from the marker, not from the layer.** `fugaro/managed.json` gains a **version 3** that records `max_image_builds` beside the amount and currency. It is written only by the owner's own paths — the creation that wrote version 2, and `fugaro managed flag` — and it is read through the same number-and-label proof as the rest of the object. A version this build does not know, a value out of the object's own checked range, and an object that cannot be read are each a refusal; a version-1 or version-2 object (a project marked before this ships) has **no recorded bound**, which is read as the default below, and never as "no bound".
- **The default is one build per session.** A marker that records no `max_image_builds` admits one billable build per agent-session invocation, and the second is refused with the owner's command named.
- **Every other control is read before each build, not once per command**: the Realtime Database kill switch (`internal/budget/kill.go` — an unreadable switch counts as ON), and the recorded budget amount. A kill switch that is on, and an amount that cannot be read, each stop the refresh before the build; the amount is not a cost estimate and is never compared with a figure this design computes.
- **Every admitted build is audited** (§6): the build's image reference, its base digest, its recipe and the bound in force are part of the record, so a runaway leaves a trail even where it is not prevented.
- The **outer** bound is unchanged and is not this design's: the installation's own billing budget alerts at the thresholds `fugaro setup` already creates. **This design does not claim a hard stop on GCP spend** — the designs of record say what a real one would cost in privilege (`docs/design/managed-projects.md` finding 25 and D63), and nothing here adds it.

The alternative — leaving the refresh unbounded as `--yes` leaves it — costs one Cloud Build job per repository per invocation, started by a session whose only other cost control is a switch the owner may not have set; a retry loop, a stale checkout and a busy agent are each enough to run it repeatedly.

## 6. Audit: every managed agent apply is recorded (D1014)

**What is recorded.** One object per applied command, at a new family under the runs bucket's owner-only prefix: `gs://fugaro-runs-<gcp-project>/fugaro/managed-audit/<utc-date>/<id>.json`. The prefix matters: `settings` objects under `runs/` are deleted at 90 days and the bucket is unversioned, so an audit that must outlive a quarter cannot live there; `fugaro/` has no lifecycle rule and is written only by operators.

**What it holds**, and each field is there because a reader acts on it:

| Field | Why |
| --- | --- |
| `version` | the object is versioned like the marker, and an unknown version is refused rather than guessed |
| `command`, `argv` | which command, with every secret-bearing argument absent rather than redacted — the gate never admits a command that takes a secret |
| `project`, `gcp_project` | the installation the record is about |
| `plan_digest` | sha256 of the exact plan bytes the gate read, so the applied plan and the judged plan are provably one |
| `verdict`, `rules` | admitted or refused, and which rules the gate applied |
| `marker_version`, `marker_generation` | which creation fact was in force |
| `delete_list_generation` | which version of the owner's list admitted the deletes, where it was read |
| `agent_marker` | which environment marker was present — the session's own claim, recorded rather than trusted |
| `identity` | the account the write ran as (the deployer account for `managed apply`, the operator's own elsewhere) |
| `at` | when, in UTC |

**Who writes it.** The command itself, with the operator's credentials, **after** the write, and it is written for a refusal too — a gate that refused is the record an owner most wants. A failure to write the record fails the command: an unattended apply that leaves no trail is the state this section exists to end. The record is never written by the agent's job account, whose grants are conditioned to `runs/`, `cache/` and `locks/`.

**Where it is shown.**

- **`fugaro doctor`** gains one check: the last managed agent apply, its command, its verdict, its plan digest and its age, with the count of records in the last day. It reads the same object the record's reader parses, so what doctor prints and what the file holds cannot drift.
- **`fugaro report`** gains one section, listing the records in the period the report already covers. The report is an `ee` command reached through `internal/cli/api.go` and this design adds no core symbol for it: the reader lives in core, the section lives where the report lives.

**What it is written for** (D1025): **admissions and refusals alike**, in a managed project and in **no other**. A refusal is recorded because an unattended refusal is exactly what an owner wants to see; a project that is not managed writes nothing, because its ordinary typed confirmations are already a person's decision at a terminal and a log of them would be a new data path for a property this design did not create.

**What it is not**: it is not a tamper-proof log. The object is written with the operator's own credentials into a bucket that is unversioned and that any project-level storage writer can rewrite (`SECURITY.md:56`). It is a record of what a session *through this program* did, and §8 says so in the threat model rather than leaving it implied.

## 7. How a session learns it is in a managed project (D1015)

**It reads the marks, per command, never an environment variable.** The reasons are the ones `read.go` already writes down, and they are worth repeating because the temptation to do otherwise is real:

- An environment variable is a fact about the **session**, not about the project. A session can set it, unset it, and inherit it from anywhere, and a variable that said "managed" would be a variable that said "skip your confirmations" — the exact inversion of a control.
- A file in the repository — `fugaro.yaml`, a config file, a checked-in flag — is **content the agent writes**. The agent opens and edits pull requests in that repository (`SECURITY.md:81`), so a repo-resident mark is a mark the untrusted side can set.
- The mark is a fact of the **project**, written by an owner-run path with the owner's credentials, and read fail-closed.

**Why a repository's hostile content cannot flip it.** Stated mechanically, because "the label is the owner's" is a promise and this has to be a property:

1. The only writers of either mark are `fugaro setup --create-project --managed` and `fugaro managed flag`, both owner-run, both refusing a coding agent's session and a run's own process (`managed_flag.go`), both writing a project label the agent's job account holds no `resourcemanager.projects.update` for.
2. The object's read proves the bucket before it believes it: a bucket's name is global, so an attacker's own bucket named `fugaro-runs-<victim>` fails the project-number comparison, and a bucket without the installation's label fails the label check.
3. The label can only narrow: a project without it is not managed whatever the object says, so the untrusted side cannot widen by adding anything.
4. The `gcpProject` the read is asked about never comes from a repository or an environment variable (§1), so no injected text can point the read at a project the attacker marked.
5. A repository's content can, at most, **cause** a command to be attempted. What it cannot do is change what the gate admits: the plan the gate judges is produced by the program from the command's own arguments and configuration, and §4's rules are the ones that decide.

## 8. The threat model

### 8.1 Prompt injection from repository content driving a managed apply

The threat: the agent reads a repository — its issues, its code, a task file, a dependency's README — and injected text tells it to run a cloud command. In a managed project the refusal is gone, so the injection reaches a program that will act.

What holds:

- The gate is a predicate over the **plan**, not over the agent's intent. An injected "delete the runs bucket" reaches a plan naming `google_storage_bucket` with a name outside `ManagedPrefix` — refused before the prefix is even read, on the runs bucket, by name.
- An injected "grant me Owner" reaches `google_project_iam_member`, which the managed allowlist **drops entirely** with a stated reason (`managedDropped`).
- An injected "raise the budget" reaches no command at all: the caps are the owner's, and no command in §3.1 writes one.
- An injected "read the secret" reaches an apply whose identity holds no `secretmanager.versions.access` (D75), so the plan could not read it even if it were admitted.
- The rules the gate applies are a **fixed list in the build**, derived and tested (`TestManagedAllowedTypesIsDecided`, `TestAllowedTypesCoverTheModules`): a module that adds a type fails a test until someone decides it, so the list cannot drift open by accident.

What does not hold, and is the whole residual: **the plan the agent authors is itself attacker-influenced.** In `fugaro managed apply` the configuration under review is the agent's, written in a repository an injection can reach. So the allowlist is not one control among several — it *is* the security boundary, and every entry on it is a decision with a stated cost, which is why the parent design made each one a decision rather than a list.

### 8.2 A stale or forged mark

- **Forged**: the object's read requires the bucket's project number to be the project's (`read.go`), the installation's label on the bucket, the label on the project, and `gcp_project` inside the object to be the project asked about. A hand-made bucket and a hand-made object fail the first.
- **Stale**: the mark is read per command and never cached, so an owner who removes it revokes at the next command. A marker written by a newer Fugaro and read by an older one is a version the old build refuses — the fail-closed direction.
- **The write path is the boundary, not the object.** An identity holding a project-level storage role can write `fugaro/managed.json` directly (`SECURITY.md:56`), and the deployer fence (D75) is what keeps the *applying* identity out of that set. **That fence is UNVERIFIED live** — Check 68 is written, owner-run and NOT RUN — so this design inherits the same unproven edge the parent carries, and says so rather than assuming it.
- **The window a revocation does not close** (D1024): the mark is read, then a plan is made, then the apply runs. An owner who unflags between the read and the apply still has the apply happen. This design narrows the window rather than claiming to close it — the mark is read per command and never cached, and **re-read immediately before the write**, so the gap is the plan-to-apply interval and no longer the whole command — and it records `marker_generation` in the audit so the owner can see which fact was in force. A revocation that is a lock would need the write and the read to be one transaction, which neither the object nor the label offers.

### 8.3 A plan that looks additive but is not

This is the threat the gate's attribute rules exist for, and the honest statement is that it is the hard one:

- **A destructive change behind an `update`.** A plan whose action set is `["update"]` but whose after view widens a grant, moves an invoker, or changes a job's service account. Rule 4.1.2 exists for it: a changed resource is judged on what it produces, with the same rules as a created one, and its before view must be a resource this project's own rules admit.
- **A replace spelled as an update.** A bucket renamed, a job renamed, a secret's id changed — the identity in `before` and `after` differs, and it is refused as a replace.
- **A value computed at apply time.** `bucket = "${substr(google_storage_bucket.b.name,0,0)}fugaro-runs-<p>"` is unknown at plan time, references a created resource and applies as the runs bucket. A saved plan records which resources an expression references, never the expression, so a computed value can never be proved equal to what the rules want: `managedNames` refuses every computed value in a managed plan, and this design keeps that rule for every command's plan.
- **Something that runs at apply.** A provisioner, a `local_file`, a data source, a third provider — refused before any resource rule is read.
- **A plan swapped between the gate and the apply.** The gate hashes the plan bytes it read and the apply applies that file; the digest is recorded (§6). This is why the audit's `plan_digest` is a control and not a courtesy.
- **What the gate cannot see.** A change whose effect is a *Google-side* default — enabling an API that makes Google add a project-level grant no plan shows (`google_project_service`'s own stated exposure) — is recorded as an accepted exposure in the managed list. This design adds no new type to that list.

### 8.4 Cost runaway

Bounded by §5: a marker-written count, default one, with the kill switch and the recorded amount read before each build, and the installation's own budget alerts outside it. **Not bounded**: GCP spend generally, and model spend outside the gateway's own caps. The honest statement is that this design bounds the one cost it creates — image builds started by a newly-unattended command — and claims no more.

### 8.5 The gate itself

- Its refusals are fail-closed, its zero values are closed (`Enable`, `InventoryCheck`), and a caller that read nothing refuses.
- The allowlist is derived and pinned, with a reason required per excluded entry, so an omission cannot open a hole.
- **What no type in the package can prevent**: a caller that goes out of its way to construct a clean verdict and hand it in. `internal/managed/check.go` says so about `CheckedInventory` and the same is true here; the tests that pin the *wiring* — that the command reads before it writes — are what catch it, which is why every task in [implementation-plan.md](implementation-plan.md) names one.

### 8.6 The agent that goes around the program

Unchanged and stated again, because a reader of this document might otherwise take the gate for a wall: the agent shares the container, the user and the metadata server with the runner; it can run `terraform` or `gcloud` directly, write the marker with the operator's own credentials, unset its marker and run any command here itself. `SECURITY.md` places an agent doing harmful things in its own container with the credentials its job account holds **out of scope**, and this design moves no boundary: it decides what a session **through the program** may apply, and what it stops is the accident and the injection.

## 9. Interaction with the rest of the tree (D1016–D1019)

### 9.1 The HARD RULE: `fugaro update` is the whole upgrade

`CLAUDE.md`'s HARD RULE says a release must not ship an operator step that is another command, and the settings, rigor and accounts designs each end a `set` command at a write the same run's `update` publishes. A gate that made the agent stop at "now run `fugaro update` yourself" would be that forbidden step, one command later. So:

- **Every command in §3.1 keeps the HARD RULE's property**: it makes its local change and the same run's `update` publishes it, or it *is* the update. No command in the set requires a second command to take effect.
- **`update` is in the set** (§4.2), so the agent that can change a setting can also publish it.
- **The one thing an agent still cannot do is create or re-budget.** That is not a second step for a change the agent made — it is a decision the agent is not allowed to make.

### 9.2 The ownership gate (#1460, `budgetDBUnadopted`)

Unchanged, unconditional, and asked **before** the managed gate rather than instead of it. A managed project whose config names a Firebase project that is not the installation's, or a database carrying another installation's mark, is refused or skipped exactly as it is today, in an agent's session and in a person's. The gate licenses a write into this installation's own database and nothing else: an ownership failure is not a plan defect the agent can fix, so the refusal names the owner's own command (`fugaro update --firebase-project <id>`) and the session stops.

### 9.3 The settings, rigor and accounts chains (D1018)

Each design's `set` command inherits this gate rather than growing one:

- A `set` that writes **inside a repository** is allowed today and stays allowed — an agent may edit the repository (settings SD9's own line).
- A `set` that writes the **local layer or the project layer**, or a **project-scoped** value, is refused in an agent's session today; under this design it is admitted in a managed project only through the rules of §4.2, and admitted *as one write* with `update`'s publish, not as two commands.
- A `set` whose effect is a **floor removed or a ceiling widened** keeps its typed project name, which has no agent answer: refused in a managed project too. A gate over a plan cannot stand in for a decision about how much review a project gets.
- The **accounts** design's reserved names, reference checks and `allow_data_to` rule are the gate's rules for that command; nothing here re-derives them.

### 9.4 The console and `fugaro setup` (D1019)

- **The console** is read-only by allow-list and holds no credential (`docs/design/desktop-console.md`), and a managed project does not change that. This design grants the console **nothing**: no write, no new node, no new backend. The audit records of §6 are read by `doctor` and `report` on a machine that already holds an operator credential, and a console that wanted to show them would read the same objects through the credential it already has for runs.
- **`fugaro setup`** stays the owner's wizard. It is where a project becomes managed (`--create-project --managed`), its questions keep the terminal they need, and none of its Typed steps — a new project, billing, a secret, a Firestore location, a billable first build — gains an agent answer. The agent's door after this design is `update`, exactly as the HARD RULE intends.

## 10. Revocation (D1020)

**`fugaro managed unflag` does not exist today.** `fugaro managed` registers exactly two subcommands, `apply` and `flag` (`internal/cli/managed_apply.go`'s `newManagedCmd`), and the undo `fugaro managed flag` prints is two manual writes: delete `gs://<runs bucket>/fugaro/managed.json` yourself as the operator — *"no command of ours removes the marker in this release"* — and take the label off the project with `gcloud projects update <gcp-project-id> --remove-labels=fugaro_managed`, which the design of record marks UNVERIFIED until an operator has run it.

**What revocation does today, and it is already the fail-closed direction**: because **both** marks are required, removing **either one** makes the project unmanaged at once. The read is made per command and never cached, so the next command sees it.

**The recommendation** is to build **`fugaro managed unflag PROJECT_ID`**, the third subcommand of the noun group D76 already left room for:

- Owner-run, refused in a coding agent's session and from a run's own process, with the same two checks `flag` carries, the project's exact ID typed at the terminal, and no `--yes`, no `--prefix`, no wildcard and no bulk form.
- It removes **both** marks in one command — the object and the label — and says which half was already gone rather than failing on it, so a half-removed project (the state a manual removal leaves) converges instead of needing a second command.
- It records the revocation in the audit family of §6.
- It does **not** remove the deployer fence: the seven scoped roles and their seven conditioned bindings stay in the project and in the installation, `fugaro update --forget` stays refused for a fenced installation, and the command says so before it writes (`docs/design/managed-projects.md` §10.2).

The alternative is to keep the two manual lines. It costs an owner two commands instead of one, in two different tools, one of them UNVERIFIED, and it leaves the state the parent design already had to write a paragraph about: a project whose object is gone and whose label is not, or the reverse, is unmanaged either way but *reads* as broken to every later command.

**What revocation does not close** is §8.2's window: the mark is read before the write, so a revocation racing a command in flight lands at the next read. The design narrows it (a re-read immediately before the write) and records `marker_generation`, and says plainly that a lock would need a transaction neither mark offers.

## 11. What this design is not

- **Not a prompt removal in a project that is not managed.** Two marks, both the owner's, or nothing changes.
- **Not a new identity, role or permission.** The apply identities are D75's deployer and the operator's own; the fence, the roles and their conditions are the parent design's and this one adds none. Adding a command to §3.1 adds no IAM: the operator's credential is what runs these commands, and the operator already holds it.
- **Not a hard stop on cloud spend.** §5 bounds the one cost this design creates and says what it does not bound.
- **Not a model's review.** The parent design refused to treat the `review` stage as a control (its finding 8) — a same-container, same-model, same-user model call whose input the reviewed party writes. Nothing in this design reintroduces one: the gate is a program.
- **Not a change to `managed apply`, `managed flag`, the delete list, the state bucket or the inventory.** They are cited, not touched.
