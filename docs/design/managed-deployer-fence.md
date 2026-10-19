# The managed deployer's fence, redesigned for 0.9.0

Status: **design + implementation, GATED OFF.** `managed_deployer_scoped`
defaults to false and `internal/cli`'s `managedDeployerShipped` stays false, so
no installation plans the new shape and no command uses the account. This
document is the design an independent review gates; §9 is the live check the
owner runs before the gate can be flipped, and it has not been run.

It supersedes nothing: D75 (`docs/design/managed-projects.md` §5) still says
what the deployer may hold and why. What changes is *how the fence is
expressed* — the shape of the bindings — because the shape D75 was written
against cannot be applied. Where this document and D75 differ, §7 lists the
difference and §8 asks the owner to rule.

**What this document does not claim.** It does not claim the redesigned fence
is accepted by IAM, or that it fences anything on Google's side. Every
statement about Google's behaviour that the repository cannot demonstrate is
labelled UNVERIFIED and is what §9 measures. The claim "it applies cleanly"
belongs to the owner's live check and to nothing here.

## 1. The problem, as the tree records it

**F1 — the fence is off because it cannot be applied.** `deploy/terraform/gcp/
modules/installation/deployer.tf:29-41` records the owner's 2026-10-18 ruling
("Gate it off by default; redesign for 0.9.0") and the three defects:

1. the expression joins 9 clauses with **17 logical operators** where IAM allows
   **12**;
2. it calls **`resource.name.contains()`**, which is not a supported function
   (`startsWith`, `endsWith`, `extract`, `==` and `!=` are);
3. **`resource.name` is not supported at all** for Artifact Registry
   repositories, Cloud Run jobs, Cloud Scheduler jobs, IAM service accounts and
   API keys.

The consequence recorded there is the reason this is not cosmetic: *"setIamPolicy
would reject the binding and stop every fugaro init."* A defect in a conditioned
binding is not a quiet loss of a fence — it is a failure of the owner-run apply
that creates the whole installation.

**F2 — the same role cannot be bound twice.** `deployer.tf:19-21`: *"a binding
grants its whole role, so a condition on one binding cannot fence permissions an
unconditioned binding of the same role already grants project-wide."* The two
roles exist because of this, and it is also why the redesign cannot be "put the
condition on one binding and leave the rest alone": every permission must sit in
exactly one role, and every role that holds a permission the fence is supposed
to limit must be bound only under a condition that limits it.

**F3 — what the fence must achieve** (`deployer.tf:9-17`): the account can
create/update/delete **only resources it owns** (names carrying the managed
prefix `fmgd-`, `tf.ManagedPrefix`, `internal/infra/tf/managed_cover.go:52`) and
can **never** reach the installation's runs bucket (where the marker and the
owner's delete list are), Fugaro's own accounts, jobs, scheduler jobs, secrets,
repositories, API key or state buckets, or the project's default compute account
— which holds Editor by default.

**F4 — the prefix is the fence's only key.** The two roles are the only thing
the account holds (`tf.RolePermissions`), and the condition is the only thing
that makes the second one narrow. There is no second mechanism: no VPC-SC, no
separate project, no deny policy.

**F5 — what is proved live today, and what is not.**

| claim | where | status |
| --- | --- | --- |
| a Cloud Storage `resource.name.startsWith("projects/_/buckets/<prefix>")` condition admits the prefix and refuses everything else | `docs/gcp-live-checklist.md` Check 32 | RUN 2026-10-14, PASSED |
| a Secret Manager name-prefix condition | checklist Check 52, U3 | **NOT RUN** |
| whether `resource.name` is honoured for an IAM service account, a Cloud Run job, a Cloud Scheduler job or an API key | `deployer.tf:70-76` names it as what "the plan's Check 68 has to measure" | **no check exists yet** (Checks 65-68 are unwritten; the highest written section is Check 58) |

## 2. Requirements the redesign has to meet

R1. **Every binding that holds a fence-limited permission is conditioned**, and
     its condition admits only `fmgd-`-named resources of the types that
     permission's role reaches.
R2. **Every condition is under IAM's 12-operator limit** and uses only supported
     functions, with a `resource.name` form the service honours.
R3. **No permission is added, removed or moved between the conditioned and the
     unconditioned role** except where §7 says so and §8 asks. The 25 + 31
     permissions are D75's, and the unconditioned role still holds nothing that
     updates, deletes, runs, pauses, sets a policy or acts as anything.
R4. **The shape is pinned by the tree**: the Terraform module and
     `internal/infra/tf` must agree byte for byte, and the cover gate must
     refuse any other expression rather than accept a plan that has been
     loosened.
R5. **A service that does not honour the name form is dropped, not unfenced.**
     `deployer.tf:73-76`: "If a service does not honour it, the permission that
     needs it cannot be fenced, so Task 1.1 drops the matching type; none of
     these permissions is ever moved into fugaroDeployer (the unconditioned
     role) or into a binding with no condition."
R6. **The failure mode is fail-closed.** If a condition is wrong in a way the
     tree cannot see, the plan must be refused or the grant must be denied —
     never silently widened.

## 3. Facts about IAM Conditions

Establishable from the repository (cited), or recorded by the documents; the
rest is Google behaviour and is labelled UNVERIFIED because this repository
cannot test it and this run makes no live call.

| # | fact | source | status |
| --- | --- | --- | --- |
| C1 | a condition expression allows at most 12 logical operators | recorded in `deployer.tf:32-33` (the 0.8.6 defect) and `docs/plans/2026-10-16-managed-projects.md` Task 1.5 | recorded, not measured here |
| C2 | the supported `resource.name` functions are `startsWith`, `endsWith`, `extract`, `==`, `!=`; `contains` is not one | `deployer.tf:34-35` | recorded |
| C3 | `resource.name` is unsupported for Artifact Registry repositories, Cloud Run jobs, Cloud Scheduler jobs, IAM service accounts and API keys | `deployer.tf:35-37` | recorded; **UNVERIFIED** — it is one of the things Check 68 measures |
| C4 | an unconditioned binding of a role defeats a conditioned binding of the same role; effective grant = union over bindings of (role's permissions ∩ resources the condition matches) | `deployer.tf:19-21`; enforced by `internal/infra/tf`'s `deployerGrant`/`unconditionedDeployerViolations` | **established in-tree** (the cover refuses the plan) |
| C5 | `resource.type` for the nine types D75 names, and the name shapes (`projects/_/buckets/<b>`, `projects/<n>/secrets/<s>`, …) | `deployer.tf:51-64`; `internal/infra/tf/cover.go`'s clause model | recorded, and the storage form is proved (Check 32) |
| C6 | `resource.name.extract("<template>")` returns the resource's own last segment when the name matches the template, and the template's placeholders are written `{project}`, `{location}`, `{job}` and so on | Google's IAM Conditions resource-attribute documentation, as cited by Task 1.5 | **UNVERIFIED** |
| C7 | a resource whose name does not match the template makes `extract` fail, and a failing condition is false (deny) | Google's conditions semantics, as read into Task 1.5 | **UNVERIFIED**, and load-bearing: §5's design is fail-closed only if a non-match denies |
| C8 | a project-level binding of a custom role reaches only that project's resources | IAM's resource hierarchy; also how every other Fugaro grant is written | recorded |
| C9 | `resource.matchTag` exists and can test a tag on a resource, but tag *values* are per-project and a tag key has to be created in the organisation/project first | Task 1.5's option list; no Fugaro code uses tags today | **UNVERIFIED** |
| C10 | `cloudscheduler.jobs.resume` does not exist; resume is `cloudscheduler.jobs.enable` | `deployer.tf:164` (the second 0.8.6 defect, already fixed) | recorded |

## 4. Options

Each option is stated as: shape, what it protects, what it needs from the owner
at creation, its failure modes, and its live check.

### Option 1 — one binding per service, each with its own role and its own condition

**Shape.** Split `fugaroDeployerScoped`'s 31 permissions by service into one
custom role per service, each bound to the account by its own
`google_project_iam_member` under its own condition. A condition covers only the
types of the service it grants, so it needs one or two clauses rather than nine,
and each service gets the `resource.name` form that service honours: the proven
`startsWith("projects/_/buckets/fmgd-")` for Cloud Storage, and, for the rest,
either a `startsWith` on a path whose front is a plan-time literal, or
`extract("<template>").startsWith("fmgd-")` where a middle segment (the project
number, the location) is not knowable at plan time.

**What it protects.** Everything F3 names, provided C3 and C7 hold: the roles
are disjoint, each is bound once, and each condition admits only `fmgd-` names.
Splitting also *reduces* the blast radius of a wrong condition: a condition that
is too wide for Cloud Scheduler can no longer widen storage.

**What it needs from the owner at creation.** Nothing new. Seven custom roles
and seven conditioned bindings are created by the same owner-run apply that
creates the account, all of them behind the same `managed_deployer_scoped`
flag. The owner's live check (§9) is the only new obligation, and it is the same
one D75 already needed.

**Failure modes.**
- A condition Google rejects (C1/C2/C3 wrong) stops the apply — F1's failure,
  which is why the gate stays off until §9 passes. Detected at creation, not at
  use.
- `extract` does not behave as C6/C7 describe: if a non-matching name makes the
  condition *true* instead of false, the fence inverts and the deployer reaches
  everything the role's permissions reach. This is the one failure that is not
  fail-closed, and it is why the design uses `extract` only where a `startsWith`
  on a literal front cannot be written, and why §9 tests a non-matching name
  explicitly, per service, both directions.
- `resource.name` is unsupported for a service (C3): that service's condition
  cannot be written at all. R5 applies — drop the type (§8, open point 5).
- Seven roles is seven objects to keep in step with the code. `internal/infra/tf`
  pins all seven (`RolePermissions`, `FugaroRoles`, the module pin test), so a
  drift is a test failure rather than a silent hole.

**Live check.** §9, per service, both directions, with the account impersonated
in a sandbox project only.

### Option 1b — the same split, with `startsWith` only (no `extract`)

**Shape.** As option 1, but every condition is a `startsWith` on a literal
front, which needs the project id (or number) and, for three of the services,
the location to be known at plan time.

**What it protects.** The same set, with no dependence on C6/C7 — this is the
appealing half: `startsWith` is the form Check 32 proved, and it cannot invert.

**What it needs from the owner at creation.** A decision per service, because
the literal is not always knowable:
- **Cloud Storage** — fine. `projects/_/buckets/fmgd-` carries no project.
- **IAM service accounts** — fine *if* the condition may name the project:
  `resource.name.startsWith("projects/${var.project}/serviceAccounts/fmgd-")`.
  `var.project` is known at plan time (`deployer.tf:198`).
- **Secret Manager** — the name carries the project **number**, which the module
  does not have at plan time; a `startsWith` would have to omit the front, and
  `"/secrets/fmgd-"` cannot be a `startsWith` (the name does not start with it).
  So Secret Manager needs either `extract` or the project number as an input.
- **Artifact Registry, Cloud Run jobs, Cloud Scheduler jobs, API keys** — the
  name carries `locations/<location>` before the resource's own segment, and the
  location is a plan-time value the *managed plan* chooses, not the module. A
  condition that pins the installation's own region would be wrong for a managed
  project that deploys elsewhere, and the installation itself already uses two
  scheduler regions (`scheduler_region` us-east4 against region us-east5 in the
  golden tfvars), which shows the location is not a constant even inside one
  installation.

**Failure modes.** Correct where it can be written; the cost is that for five of
the seven services it *cannot* be written without either `extract` or new
plan-time inputs (a project number, a location allow-list), and a location
allow-list is a fence with a hole the width of the allow-list.

**Live check.** As §9, minus the `extract` half — but only for the services that
keep a condition; the others fall to R5.

### Option 2 — fence by project, not by name

**Shape.** The deployer holds its roles **only on the managed project(s)** —
project-level bindings in the project a managed plan deploys into, created by
that plan's owner-run bootstrap, with **no condition at all**. On the
installation's own project it holds nothing.

**What it protects.** The installation's runs bucket, secrets, accounts, jobs,
API key, state buckets and default compute account — completely, and by IAM's
own resource hierarchy (C8) rather than by a name test. It needs no `extract`, no
`resource.name`, and no per-service reasoning; it cannot be defeated by a
resource-attribute limitation (C3) at all. It is the only option whose fence
does not depend on any UNVERIFIED Google behaviour except C8.

**What it gives up, and the question it puts to the spec.** Inside the managed
project the deployer is **unfenced**: it can update or delete *anything* in that
project, including resources the owner created there by hand, and including a
resource named without the prefix. D75's answer to "what may the agent's apply
touch" would become "whatever is in the project it was given" rather than
"whatever carries the prefix". Whether that is the intent is a spec question,
not an implementation one — `docs/design/managed-projects.md` §5 is written
around the prefix, and `tf.ManagedPrefix` is enforced on resource *names* by
`managed_cover.go`'s `managedName`, which suggests the prefix is meant to mean
something even where the deployer is the only actor.

**What it needs from the owner at creation.** A per-managed-project bootstrap
step (the grant in the managed project), and a decision on the question above.
Also a decision on who may create the managed project: today the account cannot
(`resourcemanager.projects.*` is deliberately absent, `deployer.tf:141-143`).

**Failure modes.** No name-based failure mode. The exposure is exactly the
project: an agent-authored plan that deletes the wrong resource in its own
managed project does so with the owner's blessing-by-design. It also weakens the
"the deployer cannot reach Fugaro's own things" argument from *cannot name them*
to *is not in their project* — which is stronger in one direction (no name-form
dependency) and weaker in another (a future managed project that shares a
project with an installation loses the distinction entirely; the
`firebase-same-project` golden shows the code already contemplates a project
playing two roles).

**Live check.** Much smaller: create the grant, impersonate, attempt one action
on a non-managed resource in the *installation* project and expect a refusal,
and one in the managed project and expect success. It does not test
`resource.name` at all.

### Option 3 — tags (`resource.matchTag`)

**Shape.** Keep one role per service (or one role), and condition it on
`resource.matchTag("fugaro-managed", "true")` (or a tag value per installation),
with the managed plan tagging every resource it creates.

**What it protects.** Whatever carries the tag — and, unlike a name prefix, the
tag is not a naming convention the resource's own creator can forget only by
forgetting a `name` attribute: `managed_cover.go` already enforces the prefix on
the resource's own name attribute, so the tag would be a second, independent
key.

**What it needs from the owner at creation.** The most of any option: a tag key
in the organisation or project, a tag *value* per installation, and
`resourcemanager` permissions for whoever creates them. It also needs the
provider to support tags on all seven resource types, which the pinned provider
(hashicorp/google 8.4.0) does for some but not all of these types — a fact this
repository cannot settle without a live provider (UNVERIFIED). Tag values are
also per-project, so a managed project's tag value is not the installation's.

**Failure modes.** The condition is only as good as the tagging: an untagged
resource is out of reach (fail-closed, good) but a *resource the plan forgets to
tag* becomes unmanageable after creation (fail-closed in the wrong direction —
it strands the plan's own resources). `resource.matchTag` on a type that does not
support tags fails closed too, which for Cloud Scheduler jobs and API keys would
mean those services are simply unavailable.

**Live check.** Larger than §9: tag key and value creation, one tag, one
condition, per service.

### Option 4 — a separate project for the agent's root

**Shape.** The managed plan applies into a project that exists only for it, and
the deployer's roles are bound there (this is option 2 with the project
question answered "yes, the whole project is the agent's").

**What it protects.** Everything outside that project, absolutely; nothing
inside it. It is the strongest version of option 2 and needs
`resourcemanager.projects.create` somewhere — which the deployer deliberately
does not have and which is a much larger grant than anything D75 hands the
account.

**Why it is not the recommendation.** It is an architecture decision about where
managed projects live, well outside "redesign the fence", and it does not remove
the need for the name prefix inside the project (`tf.ManagedPrefix` still
distinguishes managed resources from Fugaro's own within a shared project).

## 5. Recommendation: option 1

**Recommendation: option 1 — one binding per service, one role per service,
`startsWith` where the front of the name is a plan-time literal and
`extract("<template>").startsWith("fmgd-")` where it is not.**

Reasoning, in the order the requirements are numbered:

- **R1/R2** are met structurally, and by construction rather than by care: a
  condition that covers one service's two types is 1-3 operators, an order of
  magnitude under the limit, and no clause needs `contains` (C2) or a nine-way
  disjunction. The operator count is pinned by a test (§6).
- **R3** is met exactly: the 31 permissions are partitioned, not rewritten. No
  permission is added, removed, or moved into the unconditioned role. A test
  asserts the partition sums to D75's list.
- **R4** is met by the existing machinery: `internal/infra/tf` already refuses a
  scoped binding whose condition is not the one the code builds, byte for byte.
  The change is to make that "the one the code builds" a *set* — one per service
  — and to keep the same refusal strength per binding.
- **R6** holds for six of the seven services without qualification, and for the
  `extract` form it holds *if* C7 is right. That is the honest weak point of this
  option, and it is why §9 tests a non-matching name per service and both
  directions, and why §8 asks the owner to accept the `extract` dependency
  before the gate is flipped.
- It **keeps the prefix meaningful**, which options 2 and 4 give up, and it does
  not need the tag infrastructure option 3 needs.
- It **reduces blast radius**: today one wrong condition widens all nine types;
  after the split, a wrong condition widens one service.

**Why not 1b.** It is option 1 with a smaller dependency, and it is the right
fallback: for Cloud Storage and IAM service accounts the `startsWith` form is
available today, and §6's implementation uses it for both. It is not the whole
design because five of the seven services have a middle segment the module
cannot know at plan time, and the two ways to make it knowable — a project
number input, a location allow-list — are either an input the module does not
have or a fence with the allow-list's width as a hole.

**Why not 2.** It is a smaller change and a *stronger* fence against the
installation's own resources, and it should be reconsidered if §9 shows
`extract` or `resource.name` is not usable for most services. It is not the
recommendation because it gives up the prefix inside the managed project, which
is a spec decision (§8, open point 1) that this run must not take unilaterally,
and because it needs a new per-managed-project bootstrap that does not exist.

**Why not 3.** The most moving parts, the most owner setup, and the only option
whose viability depends on provider support this repository cannot check.

## 6. The shape this implementation writes

Seven roles. Each is a literal `permissions = [...]` list (the Terraform test
`TestCustomRolePermissionsAreAllowedByGoogle` requires one literal list per
custom-role block, so a `for_each` over a map is not available), and each is
bound by its own literal-role `google_project_iam_member` under its own
condition. All seven are behind `count = var.managed_deployer_scoped ? 1 : 0`.

| role | permissions | type(s) | condition clause |
| --- | --- | --- | --- |
| `fugaroDeployerStorage` (9) | `storage.buckets.{delete,get,setIamPolicy,update}`, `storage.objects.{create,delete,get,list,update}` | `storage.googleapis.com/Bucket`, `.../Object` | `resource.name.startsWith("projects/_/buckets/fmgd-")` — the form Check 32 proved |
| `fugaroDeployerSecret` (3) | `secretmanager.secrets.{delete,setIamPolicy,update}` | `secretmanager.googleapis.com/Secret` | `resource.name.extract("projects/{project}/secrets/{secret}").startsWith("fmgd-")` |
| `fugaroDeployerAccount` (4) | `iam.serviceAccounts.{actAs,delete,setIamPolicy,update}` | `iam.googleapis.com/ServiceAccount` | `resource.name.startsWith("projects/${var.project}/serviceAccounts/fmgd-")` — the project is a plan-time literal here (C5, `cover.go`'s `saName`) |
| `fugaroDeployerApiKey` (2) | `apikeys.keys.{delete,update}` | `apikeys.googleapis.com/Key` | `resource.name.extract("projects/{project}/locations/{location}/keys/{key}").startsWith("fmgd-")` |
| `fugaroDeployerRepository` (3) | `artifactregistry.repositories.{delete,setIamPolicy,update}` | `artifactregistry.googleapis.com/Repository` | `resource.name.extract("projects/{project}/locations/{location}/repositories/{repository}").startsWith("fmgd-")` |
| `fugaroDeployerRunJob` (5) | `run.jobs.{delete,run,runWithOverrides,setIamPolicy,update}` | `run.googleapis.com/Job` | `resource.name.extract("projects/{project}/locations/{location}/jobs/{job}").startsWith("fmgd-")` |
| `fugaroDeployerSchedulerJob` (5) | `cloudscheduler.jobs.{delete,enable,pause,run,update}` | `cloudscheduler.googleapis.com/Job` | `resource.name.extract("projects/{project}/locations/{location}/jobs/{job}").startsWith("fmgd-")` |

9+3+4+2+3+5+5 = **31** — D75's scoped list, partitioned, nothing added or
dropped. Operator counts: storage 3 (two clauses, joined by one `||`), and 1 for
each of the other six (one clause, so a single `&&`). The largest is 3 against a
limit of 12 (C1), where the 0.8.6 shape's single condition counted 17.

`secretmanager.googleapis.com/SecretVersion` is deliberately **not** fenced and
**not** named by any clause: no permission in `fugaroDeployerSecret` acts on a
version (`secretmanager.versions.*` is in no deployer role at all), and a
version's own name (`projects/<n>/secrets/<secret>/versions/<v>`) does not match
the secret template, so a clause for it would be dead weight in a condition that
has to stay under 12 operators. A version resource is therefore out of the
fence's reach by being out of the role's reach — fail-closed. If a version
permission were ever added to the role, `TestDeployerScopedRolesAreD75Partitioned`
(internal/infra/tf/cover_test.go) refuses it: it is not in D75's scoped list,
which that test carries as its own literal, and the partition test is what makes
a permission moved between services a visible diff.

The condition **title** stays `fugaro-managed-prefix` (`tf.DeployerConditionTitle`)
for every binding: it names the *fence*, and an operator reading the project's
IAM policy should see one fence, not seven names. The **role** is what differs.

`internal/infra/tf` models this as a `DeployerService` per row — role, title,
clauses — with `DeployerClause` gaining the `extract` form
(`resource.name.extract("T").startsWith("V")`) alongside `startsWith`. The cover
gate looks a binding's role up in the set and requires that binding's condition
to be exactly that service's expression; a role outside the set, a condition
that is not the one for that role, an unconditioned binding of any of the seven,
and a second binding of any of the seven are all refused.

## 7. What this changes about D75

D75 (`docs/design/managed-projects.md` §5) says the deployer holds **two**
custom roles and that `fugaroDeployerScoped` is *"bound exactly once under the
name-prefix condition"*. The redesign makes that **eight** roles and **eight**
bindings (one unconditioned, seven conditioned), for the reason in F1: the
single-condition shape cannot be applied at all.

The permissions, the prefix, the account, the impersonation grant, the
deliberate absences (no `resourcemanager.projects.*`, no `iam.roles.*`, no
`secretmanager.versions.*`) and the "drop the type, never unfence it" rule are
unchanged.

## 8. Open points the owner must decide

1. **Is the prefix still the right key inside a managed project?** Option 2's
   question. If a managed project is the agent's own, the prefix is doing less
   work there than D75 assumed, and the per-service split buys less than it
   looks like. If the prefix must keep meaning something, option 1 is the only
   one of the four that keeps it.
2. **Accept the `extract` dependency?** Five of the seven services need
   `resource.name.extract` (C6) and depend on C7's fail-closed behaviour. If the
   owner will not accept that before it is measured, the alternatives are 1b
   (with the project number and location inputs it needs, and the location hole
   that comes with a location allow-list) or dropping those five services.
3. **What if `resource.name` is unsupported for a service (C3)?** R5 says drop
   the type. Dropping IAM service accounts or Cloud Run jobs guts the feature
   (a managed plan creates accounts and jobs); dropping API keys, scheduler jobs,
   Artifact Registry repositories or secrets is survivable. The owner should say
   now which drops are acceptable, so §9's result has a written consequence.
4. **What if `extract` is unsupported but `resource.name` is?** Then option 1b
   with the extra inputs becomes the design, and the location question in point 2
   has to be answered.
5. **Does the API key's ID start with the prefix in practice?** `managedName`
   enforces the prefix on a resource's own name attribute, and
   `google_apikeys_key`'s `name` is that attribute — but an API key ID is a
   value the provider may or may not let the plan choose. If it cannot, the
   `fugaroDeployerApiKey` role's condition can never match and the two
   permissions are dead (fail-closed, but dead).
6. **Seven roles in every installation, `belong` included.** D75 accepted one
   service account per installation; the redesign makes that one account, eight
   roles and eight bindings. Confirm the count is acceptable, or say that the
   roles should be created only where `managed_deployer_scoped` is true (they
   already are) and that `belong` therefore keeps only the unconditioned role
   until someone flips the flag there.
7. **The condition title.** This design keeps one title for all seven bindings.
   If the owner would rather see which service a binding fences in the policy
   listing, the title becomes per-service and the pin test changes with it.

## 9. The live check (owner-run; `docs/gcp-live-checklist.md` Check 68)

Not run. Written into the checklist as a section in the house style, with the
hard stops the other checks use. In outline, per service, in a **sandbox project
only**, never `belong`:

1. Apply the installation with `managed_deployer_scoped = true` in the sandbox
   project and record whether the seven roles and seven conditioned bindings are
   accepted — this alone settles F1 (does it apply at all?).
2. For each service: impersonate `fugaro-deployer@<sandbox>.iam.gserviceaccount.com`
   and attempt one action on a **prefixed** resource (expect success) and the
   same action on a **non-prefixed** resource of the same type, including one
   whose name *contains* the prefix but does not start with it (expect a
   permission-denied that names the condition, not the role).
3. Record the exact error text for a refusal; a refusal that reads like a
   missing role rather than a failed condition is a different result from a
   refusal that reads like a failed condition.
4. Restore: delete the sandbox resources the check created, and the condition
   itself if the apply is rolled back.

Check 67 (the plan's Task 5.3) owns the transitive-reach half — what the
identity can reach through impersonation and through the roles it may grant —
and this check owns the fence half. Neither substitutes for the other.

**Until Check 68 has run and passed, `managed_deployer_scoped` stays false and
`managedDeployerShipped` stays false.**
