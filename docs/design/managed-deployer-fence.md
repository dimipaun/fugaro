# The managed deployer's fence, redesigned for 0.10.0

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
labelled UNVERIFIED and is what §9's check measures. **C6 and C7 are among
them.** The redesign rests on a reading of `resource.name.extract`: that the
template is literal text around **one** `{identifier}` placeholder, that the
placeholder takes the characters after the **first** occurrence of the literal
text, and that a name the template does not match extracts the empty string, so
`"".startsWith("fmgd-")` is false and a non-match denies (C6/C7 in §3).
**That reading is recorded here, not cited: no source in this tree quotes the
reference's text.** `deployer.tf`'s header states the rule without naming a
document; Task 1.5 (`docs/plans/2026-10-16-managed-projects.md:454`) cites the
reference for the services' *name shapes* and never for this grammar; and
`extractSegment` (`internal/infra/tf/cover_test.go`), which models the reading,
is this repository's own reimplementation rather than evidence for it. So C6 and
C7 carry the UNVERIFIED label like C3 and C9, and the templates are the reading's
form rather than a form anyone in this repository can point at a source for. It
matters because **no later run can falsify the reading**: every conditioned
type's real names carry their own clause's anchor, so §9 cannot construct a
non-match (see C7 in §3 and §9 below), and the two readings of the grammar differ
only in which half of the fence grants nothing, both fail-closed. What §9 does
measure is whether the services honour `resource.name` at all (C3), whether IAM
accepts the expressions, and whether a prefixed resource is actually reachable.
If a later change quotes the reference's text into §3, the label comes off. The claim
"it applies cleanly" belongs to the owner's live check and to nothing here.

**What this fence is expected to be worth if C3 is true.** The five services
whose resource-attribute reference does not list `resource.name` — IAM service
accounts, Cloud Run jobs, Cloud Scheduler jobs, Artifact Registry repositories
and API keys — would, if `resource.name` is unsupported for them, have
conditions that can never be satisfied: every request against those types is
denied, so those five roles grant nothing. That is fail-closed and it is the
safe direction, but it is not a fence: **after the flag flips, the deployer is
expected to be fenced for Cloud Storage (proved by Check 32) and for Secret
Manager, and to hold five roles that do nothing** — and the Secret Manager half
is **measured, not expected**: that service's name-prefix condition is Check 52's
U3, **RUN 2026-10-16 and answered yes — the condition is honoured** (F5's table
below). So the forms this repository has measured live are Cloud Storage's and
Secret Manager's name prefix, and every other service's is expected at best. The
feature's own use of those services would then have to be reconsidered (§8 point
3), and no document here may claim more than that until Check 68 has run.

## 1. The problem, as the tree records it

**F1 — the fence is off because it cannot be applied.** `deploy/terraform/gcp/
modules/installation/deployer.tf`'s "GATED OFF since 0.8.6" comment records the
owner's 2026-10-18 ruling ("Gate it off by default; redesign for 0.9.0"), and
its "ONE BINDING PER SERVICE (0.10.0 redesign)" comment records the three
defects:

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

**F2 — the same role cannot be bound twice.** `deployer.tf`'s "Two halves and
not one role bound twice" comment: *"a binding
grants its whole role, so a condition on one binding cannot fence permissions an
unconditioned binding of the same role already grants project-wide."* The two
roles exist because of this, and it is also why the redesign cannot be "put the
condition on one binding and leave the rest alone": every permission must sit in
exactly one role, and every role that holds a permission the fence is supposed
to limit must be bound only under a condition that limits it.

**F3 — what the fence must achieve** (`deployer.tf`'s header comment, "The
deployer account of D75"): the account can
create/update/delete **only resources it owns** (names carrying the managed
prefix `fmgd-`, `tf.ManagedPrefix`, `internal/infra/tf/managed_cover.go:52`) and
can **never** reach, **on the project**, the installation's runs bucket (where
the marker and the owner's delete list are), Fugaro's own accounts, jobs,
scheduler jobs, secrets, repositories, API key or state buckets, or the
project's default compute account — which holds Editor by default. **The one
reach it has that is not on the project is named in F4a, and F3 does not cover
it**: the account can write objects in **D77's managed state bucket**, by a
bucket-level binding of its own.

**F4 — the prefix is the fence's only key.** The eight roles are the only thing
the account holds **on the project** (`tf.RolePermissions`: one unconditioned,
seven conditioned — §7 lists the eight), and a condition is the only thing that
narrows a project-level role that holds a fence-limited permission. There is no
second mechanism: no VPC-SC, no separate project, no deny policy.

**F4a — the ninth grant, which is not one of the eight and is not
conditioned.** A design of record that stopped at "eight roles, and a condition
is the only thing that narrows one" would be claiming a fence wider than the one
in the tree, because the account holds a ninth grant that has no condition on
it: **`roles/storage.objectAdmin` (`managed.DeployerStateRole`) on D77's managed
state bucket, bound at the BUCKET level** — `internal/managed/statebucket.go`'s
`EnsureManagedStateBucket` grants the deployer member on
`gs://fugaro-managed-tfstate-<project>` when that bucket is created, beside the
operators' read-only grant (`managed.OperatorStateRole`) that is theirs and not
the account's. **Why it is there**: the managed root's state lives in that
bucket and the deployer is the identity that applies that root, so without
object write there no managed apply can run at all — the feature's own use, like
the IAM service accounts and Cloud Run jobs of §8 point 3, and not a convenience.
**Why it is not conditioned**: a condition would have to admit this bucket's own
name, and the state bucket deliberately does not carry the `fmgd-` prefix the
seven conditions admit — `managedStatePrefix` is `fugaro-managed-tfstate-`, and
its own comment says why the two must not overlap (the seven project-level roles
are fenced to `fmgd-` names, and the state bucket is reached by this binding
alone, so a condition admitting the state bucket's name would put the state
inside the fence around the seven). **What confines it instead is the scope of
the binding**: it is bucket-level, on the one bucket the installation created for
this purpose, where a project-level `roles/storage.objectAdmin` would reach every
bucket in the project. **Where it comes from is not the installation module**:
`internal/cli/init.go`'s D77 setup step writes it, on the operator's own
credentials, for every project the creating run creates — it is not gated on
`--managed` and not gated on `managed_deployer_scoped`, so it is there in a
project whose fence is still off, `belong` included, and a `fugaro init` that
creates no project writes nothing. **So R1 reads "every binding on the project
that holds a fence-limited permission is conditioned"**, and this is the one
binding that holds one (`storage.objects.*`) without a condition; it is why §7
and §8 point 6 count **nine** bindings and not eight, and it is the reason this
document names it rather than letting the count imply it.

**F5 — what is proved live today, and what is not.**

| claim | where | status |
| --- | --- | --- |
| a Cloud Storage `resource.name.startsWith("projects/_/buckets/<prefix>")` condition admits the prefix and refuses everything else | `docs/gcp-live-checklist.md` Check 32 | RUN 2026-10-14, PASSED |
| a Secret Manager name-prefix condition | checklist Check 52, U3 | RUN 2026-10-16: **honoured** — the inside read (a secret under the condition's prefix) was allowed and the outside read denied. Answered on a rerun: the first U3 run was invalid, a bug in the check's own script rather than the API's answer |
| whether `resource.name` is honoured for an IAM service account, a Cloud Run job, a Cloud Scheduler job, an Artifact Registry repository or an API key | checklist Check 68, steps 4 and 5, per service and in both directions | **written, owner-run, NOT RUN** |

## 2. Requirements the redesign has to meet

R1. **Every binding on the project that holds a fence-limited permission is
     conditioned**, and its condition admits only `fmgd-`-named resources of the
     types that permission's role reaches. The one binding that holds such a
     permission without a condition is F4a's, on D77's managed state bucket: it
     is bucket-level, and its scope is what confines it.
R2. **Every condition is under IAM's 12-operator limit** and uses only supported
     functions, with a `resource.name` form the service honours.
R3. **No permission is added, removed or moved between the conditioned and the
     unconditioned role** except where §7 says so and §8 asks. The 26 + 31
     permissions are D75's, and the unconditioned role still holds nothing that
     updates, deletes, runs, pauses, sets a policy or acts as anything.
R4. **The shape is pinned by the tree**: the Terraform module and
     `internal/infra/tf` must agree byte for byte, and the cover gate must
     refuse any other expression rather than accept a plan that has been
     loosened.
R5. **A service that does not honour the name form is dropped, not unfenced.**
     `deployer.tf`'s "The conditions, one per service" comment, the "If a
     service does not honour its form" paragraph: "If a service does not honour
     its form, the
     permission that needs it cannot be fenced, so the matching type is dropped;
     none of these permissions is ever moved into fugaroDeployer (the
     unconditioned role) or into a binding with no condition."
R6. **The failure mode is fail-closed.** If a condition is wrong in a way the
     tree cannot see, the plan must be refused or the grant must be denied —
     never silently widened.

## 3. Facts about IAM Conditions

Establishable from the repository (cited), or recorded by the documents; the
rest is Google behaviour and is labelled UNVERIFIED because this repository
cannot test it and this run makes no live call.

| # | fact | source | status |
| --- | --- | --- | --- |
| C1 | a condition expression allows at most 12 logical operators | recorded in `deployer.tf`'s "ONE BINDING PER SERVICE (0.10.0 redesign)" comment (the 0.8.6 defect: 17 operators where IAM allows 12) and `docs/plans/2026-10-16-managed-projects.md` Task 1.5 | recorded, not measured here |
| C2 | the supported `resource.name` functions are `startsWith`, `endsWith`, `extract`, `==`, `!=`; `contains` is not one | the same "ONE BINDING PER SERVICE" comment (the functions it lists) | recorded |
| C3 | `resource.name` is unsupported for Artifact Registry repositories, Cloud Run jobs, Cloud Scheduler jobs, IAM service accounts and API keys | the same "ONE BINDING PER SERVICE" comment (the five types it names) | recorded; **UNVERIFIED** — it is one of the things Check 68 measures (the other is the Secret Manager name form, so six of the seven services are measured) |
| C4 | an unconditioned binding of a role defeats a conditioned binding of the same role; effective grant = union over bindings of (role's permissions ∩ resources the condition matches) | `deployer.tf`'s "Two halves and not one role bound twice" comment; enforced by `internal/infra/tf`'s `deployerGrant`/`unconditionedDeployerViolations` | **established in-tree** (the cover refuses the plan) |
| C5 | `resource.type` for the nine types D75 names, and the name shapes (`projects/_/buckets/<b>`, `projects/<n>/secrets/<s>`, …) | `deployer.tf`'s "The conditions, one per service" comment (the two forms and the name shapes); `internal/infra/tf/cover.go`'s clause model | recorded, and the storage form is proved (Check 32) |
| C6 | `resource.name.extract("<template>")` takes a template of literal text around **one** `{identifier}` placeholder and returns the text that placeholder matched — **read here as** the reference defining that by **occurrence**: with a prefix-only template, the characters after the **first** occurrence of the literal prefix (to the end of the name), and with a suffix as well, the characters between that first occurrence of the prefix and the **first subsequent** occurrence of the suffix. A template with more than one placeholder is not the form these templates use, and not the form this design reads as documented | **uncited** — no source in this tree quotes the reference's text. `deployer.tf`'s header states the rule without naming a document, and Task 1.5 (`docs/plans/2026-10-16-managed-projects.md:454`) cites the reference for the services' *name shapes*, never for this grammar | **UNVERIFIED — recorded, not cited.** The fence's templates are written in that form and only that form, pinned by `TestDeployerExtractTemplatesAreTheRecordedForm`, and the first cut of this fence wrote `extract("projects/{project}/secrets/{secret}")`, two placeholders in the shape of a resource name, which is not that form — and the form the other four services carried, `extract("projects/{project}/locations/{location}/keys/{key}")`, is the three-placeholder one: either `setIamPolicy` refuses the expression (F1's failure, the 0.8.6 outage again) or the call extracts nothing and the role grants nothing. What is UNVERIFIED is the grammar and the **first**-occurrence rule above: they are this repository's reading, and the review that reclassified them as settled could point at no source — so the templates stand as the reading's form, and remain the reason to quote the reference's text before the fence is trusted |
| C7 | a name that does not carry the template's literal text matches nothing, so `extract` returns the empty string, and `"".startsWith("fmgd-")` is false — a non-match denies, so the fence is **fail-closed** in this direction | the same reading, taken with C2 (`startsWith` is the function applied to the result); **the tree is cited for the direction, no source is cited for Google's behaviour** — `extractSegment` (`internal/infra/tf/cover_test.go`) models the reading and `TestExtractSegmentModelsTheRecordedReading` pins the model, and the fail-closed rows of `TestDeployerConditionFencesOnlyTheManagedPrefix` assert that a name which does not match is denied | **UNVERIFIED for Google, established in-tree for this repository**, and unmeasurable live. What the tree establishes is what *this* code does with the reading — the model and its pins are the citation for the fail-closed direction — while whether `extract` on Google's side returns the empty string for a non-match is uncited, like C6. **No step of §9 can falsify it**: every conditioned type's real names carry their own clause's anchor, so no probe can construct the non-match, and the near miss tests the prefix, not the template. The two readings of the grammar differ only in which half of the fence grants nothing — a whole-name template extracts the project number or the location and never matches the prefix, and an anchored mid-name template cannot match at all — and both are fail-closed. So the residual is not a measured fact but a risk about the reference itself, **including the one direction that is not fail-closed**: a non-matching name making the condition TRUE would invert the fence rather than close it, and this check cannot reach that either. Until the reference's text is quoted in §3, C7 is in the same class as C3 and C9 |
| C8 | a project-level binding of a custom role reaches only that project's resources | IAM's resource hierarchy; also how every other Fugaro grant is written | recorded |
| C9 | `resource.matchTag` exists and can test a tag on a resource, but tag *values* are per-project and a tag key has to be created in the organisation/project first | Task 1.5's option list; no Fugaro code uses tags today | **UNVERIFIED** |
| C10 | `cloudscheduler.jobs.resume` does not exist; resume is `cloudscheduler.jobs.enable` | `deployer.tf`'s `fugaro_deployer_scheduler_job` role, the comment on its `cloudscheduler.jobs.enable` permission (the second 0.8.6 defect, already fixed) | recorded |

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
number, the location) is not knowable at plan time. The `extract` templates are
the form C6 records (UNVERIFIED there) — literal text and one placeholder —
anchored at the
collection segment the resource's own name follows (`"/secrets/{secret}"`,
`"/jobs/{job}"`), so the text extracted runs from there to the end of the name,
which for a resource of the clause's own type is its own name.

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
- `extract` does not behave as C6/C7 read it: under that reading a
  non-match extracts nothing and denies, and the tree models exactly that
  (`extractSegment`, `TestDeployerConditionFencesOnlyTheManagedPrefix`) — which
  is a model, not a measurement. If the
  reference were wrong in the other direction — a non-matching name making the
  condition *true* — the fence would invert and the deployer would reach
  everything the role's permissions reach. That is the one failure that is not
  fail-closed, it is why the design uses `extract` only where a `startsWith` on
  a literal front cannot be written, and **§9 cannot reach it**: a name of a
  conditioned type that misses its own template cannot be created. The near miss
  §9 does use (a name that contains `fmgd-` without starting with it) tests the
  prefix, not the template. **C6/C7 carry the label §3 gives them, for exactly
  this reason**:
  the fail-closed reading is the repository's, nothing in the tree quotes the
  reference for it, and no live step can reach the question either way — so this
  stays an open question about Google's behaviour rather than a risk the design
  has retired.
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
  `var.project` is known at plan time (`deployer.tf`'s "Binding (1)" comment,
  and the clause itself, the `deployer_condition_account` local).
- **Secret Manager** — the name carries the project **number**, which the module
  does not have at plan time; a `startsWith` would have to omit the front, and
  `"/secrets/fmgd-"` cannot be a `startsWith` (the name does not start with it).
  So Secret Manager needs either `extract` or the project number as an input.
- **Artifact Registry, Cloud Run jobs, Cloud Scheduler jobs** — the name carries
  `locations/<location>` before the resource's own segment, and the location is a
  plan-time value the *managed plan* chooses, not the module. A condition that
  pins the installation's own region would be wrong for a managed project that
  deploys elsewhere, and the installation itself already uses two scheduler
  regions (`scheduler_region` us-east4 against region us-east5 in the golden
  tfvars), which shows the location is not a constant even inside one
  installation.
- **API keys** — the location is not the unknown here: an API key's name carries
  `locations/global/keys/<key>` (`internal/gcpfake/apikeys.go`'s `AddKey`), so
  its location is the constant `global` and a location allow-list buys nothing
  for it. What a whole-name form would have to spell is the segment before
  `/keys/`, which is what the `extract` anchor steps over.

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
(`resourcemanager.projects.*` is deliberately absent, `deployer.tf`'s "What none
of them holds, and why" comment).

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
- **R6** holds for all seven services, **on the reading C6/C7 record (§3's two
  rows carry the label; neither is cited)**: the storage and service-account clauses are
  `startsWith` on a plan-time literal, and the five `extract` clauses deny when
  their template does not match (C7). The remaining exposure is not the
  fail-closed direction but the one that cannot be measured: a service that does
  not honour `resource.name` at all (C3) leaves
  its clause unsatisfiable, which is fail-closed — the role grants nothing —
  and §9 measures it per service. **§8 point 2 is therefore no longer "accept
  the `extract` dependency unmeasured"**; it is what to do with a service whose
  clause cannot be evaluated at all (§8 point 3).
- It **keeps the prefix meaningful**, which options 2 and 4 give up, and it does
  not need the tag infrastructure option 3 needs.
- It **reduces blast radius**: today one wrong condition widens all nine types;
  after the split, a wrong condition widens one service.

**Why not 1b.** It is option 1 with a smaller dependency, and it stays the
recorded fallback: for Cloud Storage and IAM service accounts the `startsWith`
form is available today, and §6's implementation uses it for both. It is not the
design because five of the seven services take the anchor form rather than a
whole-name one, and for each of them a whole-name form would have to spell
something the module does not have at plan time — Secret Manager's project
**number**, the **location** Artifact Registry, Cloud Run and Cloud Scheduler
carry, and, for API keys, the segment before `/keys/` (their location is the
constant `global`, so it is not a location the plan chooses —
`internal/gcpfake/apikeys.go`'s `AddKey`). The two ways to make the first four
knowable are a project-number input the module does not have or a location
allow-list, which is a fence with the allow-list's width as a hole. The `extract`
anchor form needs neither: `"/secrets/{secret}"` steps over the unknown project
number and `"/jobs/{job}"` over the location, and both are in the
one-placeholder form C6 records (UNVERIFIED there: the grammar is this
repository's reading of the reference, and nothing in this tree quotes it).

**Why not 2.** It is a smaller change and a *stronger* fence against the
installation's own resources, and it should be reconsidered if §9 shows
`extract` or `resource.name` is not usable for most services. It is not the
recommendation because it gives up the prefix inside the managed project, which
is a spec decision (§8, open point 1) that this run must not take unilaterally,
and because it needs a new per-managed-project bootstrap that does not exist.

**The fallback, written out and ready: option 2, with its trigger.** The risk
this recommendation runs is C3, and it is the one risk no reading can settle
here: **the IAM Conditions reference does not list `resource.name` support for
IAM service accounts, Cloud Run jobs, Cloud Scheduler jobs, Artifact Registry
repositories or API keys** — five of the seven services. If `resource.name` is
unsupported for a type, that type's clause can never be satisfied, the role
grants nothing, and the fence is fail-closed but absent: the deployer would hold
five roles that do nothing. §9's Check 68 measures it per service in both
directions (a condition IAM refuses in step 2/3, or a denied positive action in
step 4), and **the trigger is precise, service by service, and §8 point 3's
ruling of 2026-10-20 draws the line between its two outcomes.** A condition IAM
**refuses to store** (step 2 or step 3) is a STOP and this fallback whatever
service it belongs to: a shape `setIamPolicy` will not take is the 0.8.6 outage
again. Of a **step-4 denial** — a positive action the deployer was refused — the
two services that are the feature's own use, **IAM service accounts and Cloud Run
jobs**, are that same STOP and this same fallback and not a drop; the other
three of the five — **API keys, Cloud Scheduler jobs and Artifact Registry
repositories** — are the **acceptable drops** the ruling names (Secret Manager
secrets with them, for its own reason: that condition's open question is the
`extract` form and not `resource.name`): the service's type and its permissions
go with its role (R5), the FACT records which service was dropped, and the check
carries on with the rest rather than stopping. Option 2 above is that redesign
and it is ready: project-level bindings
in the managed project, no condition at all, no `extract`, no `resource.name`,
no per-service reasoning, and the only option that depends on no UNVERIFIED
Google behaviour beyond C8. What it needs before it can be taken is the two
decisions §8's open point 1 and option 2's own "what it needs from the owner"
name — whether "whatever is in the project it was given" is the intent, and who
creates the managed project — and those are the owner's, taken with Check 68's
results in hand. **A drop is the smaller case and R5 is what covers it**: the
type goes from the design and its permissions with it. **A STOP is this fallback
whether it is one service or most of them** — a condition IAM will not store, or
a step-4 denial for one of the two services that are the feature's own use.

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
| `fugaroDeployerSecret` (3) | `secretmanager.secrets.{delete,setIamPolicy,update}` | `secretmanager.googleapis.com/Secret` | `resource.name.extract("/secrets/{secret}").startsWith("fmgd-")` |
| `fugaroDeployerAccount` (4) | `iam.serviceAccounts.{actAs,delete,setIamPolicy,update}` | `iam.googleapis.com/ServiceAccount` | `resource.name.startsWith("projects/${var.project}/serviceAccounts/fmgd-")` — the project is a plan-time literal here (C5, `cover.go`'s `saName`) |
| `fugaroDeployerApiKey` (2) | `apikeys.keys.{delete,update}` | `apikeys.googleapis.com/Key` | `resource.name.extract("/keys/{key}").startsWith("fmgd-")` |
| `fugaroDeployerRepository` (3) | `artifactregistry.repositories.{delete,setIamPolicy,update}` | `artifactregistry.googleapis.com/Repository` | `resource.name.extract("/repositories/{repository}").startsWith("fmgd-")` |
| `fugaroDeployerRunJob` (5) | `run.jobs.{delete,run,runWithOverrides,setIamPolicy,update}` | `run.googleapis.com/Job` | `resource.name.extract("/jobs/{job}").startsWith("fmgd-")` |
| `fugaroDeployerSchedulerJob` (5) | `cloudscheduler.jobs.{delete,enable,pause,run,update}` | `cloudscheduler.googleapis.com/Job` | `resource.name.extract("/jobs/{job}").startsWith("fmgd-")` |

The five `extract` templates are the form C6 records (UNVERIFIED there): literal
text around one placeholder, anchored at the collection segment the resource's
own name follows, with nothing after the placeholder — so the extracted text is
the resource's own name. They deliberately do **not** spell the whole resource path
(`"projects/{project}/secrets/{secret}"`): that is two placeholders, not the
form C6 records — and the locations form the other four services would have
needed, `"projects/{project}/locations/{location}/keys/{key}"`, carries three —
and the project in a secret's name is the project **number**,
which this module does not know at plan time. A template that grew a second
placeholder is a test failure
(`TestDeployerExtractTemplatesAreTheRecordedForm`), not an apply that never
completes.

9+3+4+2+3+5+5 = **31** — D75's scoped list, partitioned, nothing added or
dropped. Operator counts: storage 3 (two clauses, joined by one `||`), and 1 for
each of the other six (one clause, so a single `&&`). The largest is 3 against a
limit of 12 (C1), where the 0.8.6 shape's single condition counted 17.

`secretmanager.googleapis.com/SecretVersion` is deliberately **not** fenced and
**not** named by any clause: no permission in `fugaroDeployerSecret` acts on a
version (`secretmanager.versions.*` is in no deployer role at all). Note what
excludes it, because it is not the name test: a version's name
(`projects/<n>/secrets/<secret>/versions/<v>`) *does* carry the secret clause's
anchor, and `extract("/secrets/{secret}")` returns `fmgd-x/versions/1` for it,
which starts with the prefix. What keeps a version out is the **type** term
beside the name test — a version reports
`secretmanager.googleapis.com/SecretVersion`, which no clause names — together
with the role pin, which holds `secretmanager.versions.*` out of every deployer
role. The same is true of an execution under a managed Cloud Run job, a package
under a managed repository and so on: they are other types. A version resource
is therefore out of the fence's reach by being out of the role's reach —
fail-closed. Both halves are pinned:
`TestDeployerConditionFencesOnlyTheManagedPrefix` offers those longer names
under their own types and refuses them, and asserts that no clause names those
types. If a version
permission were ever added to the role, `TestDeployerScopedRolesAreD75Partitioned`
(internal/infra/tf/cover_test.go) refuses it: it is not in D75's scoped list,
which that test carries as its own literal, and the partition test is what makes
a permission moved between services a visible diff.

The condition **title** stays `fugaro-managed-prefix` (`tf.DeployerConditionTitle`)
for every binding: it names the *fence*, and an operator reading the project's
IAM policy should see one fence, not seven names. The **role** is what differs.

`internal/infra/tf` models this as a `DeployerService` per row — role, clauses —
with `DeployerClause` gaining the `extract` form
(`resource.name.extract("T").startsWith("V")`) alongside `startsWith`. The cover
gate (`deployerGrant`) looks a binding's role up in the set and checks that
binding's member, project and condition on their own: a role outside the set, a
binding of not the deployer account, a condition that is not the one for that
role, and an unconditioned binding of any of the seven are all refused. What the
cover does **not** enforce is one binding per role — it reads each binding
independently, so a second `google_project_iam_member` of the same role under the
byte-identical condition is accepted, which is harmless because IAM members are a
set. The single-binding property is held by the module tree instead:
`TestDeployerBindingsAreTheModules` (`internal/infra/tf/cover_test.go`) counts
each role's binding in the modules and requires exactly one, the seven
conditioned ones under their own condition local and the other under none, so a
second binding in the module is a test failure rather than something a plan
review catches.

## 7. What this changes about D75

D75 (`docs/design/managed-projects.md` §5) says the deployer holds **two**
custom roles and that `fugaroDeployerScoped` is *"bound only under the
name-prefix condition"*. The redesign makes that **eight** roles and **nine**
bindings (one unconditioned project binding, seven conditioned, and F4a's
bucket-level one on D77's managed state bucket), for the reason in F1: the
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
2. **Accept the `extract` form as the design?** Five of the seven services need
   `resource.name.extract`, written in the one-placeholder form C6 records (which
   §3 marks **UNVERIFIED**: the grammar and the first-occurrence rule are this
   repository's reading, and no source in this tree quotes the reference), and
   their fail-closed behaviour follows from C7 — likewise unmeasured, and
   unreachable by any probe, because every conditioned type's real names carry
   their own clause's anchor. The
   decision left to the owner is therefore not "accept it unmeasured" but
   whether the anchored form is the design at all, against option 1b (which
   needs a project-number input and a location allow-list, and a location
   allow-list is a fence with the allow-list's width as a hole) and against
   dropping those five services (point 3). **What the tree can say is that the
   form is the one C6 records, modelled and pinned in this tree; what the tree
   cannot say is that the reference's text matches that reading, nor that IAM
   accepts it for those five types — the first is what C6's UNVERIFIED label
   records, and the second is point 3 and Check 68.**
3. **What if `resource.name` is unsupported for a service (C3)?** R5 says drop
   the type. Dropping IAM service accounts or Cloud Run jobs guts the feature
   (a managed plan creates accounts and jobs); dropping API keys, scheduler jobs,
   Artifact Registry repositories or secrets is survivable. The owner should say
   now which drops are acceptable, so §9's result has a written consequence.
   **This is the question that decides how much fence there is**: if C3 holds
   for all five services, the deployer ends up fenced for Cloud Storage and
   Secret Manager and holding five roles that deny everything (fail-closed, and
   unusable) — which is a smaller feature than any document here may claim
   until Check 68 has run.
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
   roles and nine bindings — the eight project-level ones F3/F4 describe and
   F4a's on the managed state bucket. Confirm the count is acceptable, or say that the
   roles should be created only where `managed_deployer_scoped` is true (they
   already are) and that `belong` therefore keeps only the unconditioned role
   until someone flips the flag there.
7. **The condition title.** This design keeps one title for all seven bindings.
   If the owner would rather see which service a binding fences in the policy
   listing, the title becomes per-service and the pin test changes with it.

**Ruled by the owner on 2026-10-20.** Recorded as rulings, and each one's
consequence is a dependency this branch does not take — the branch's job is the
measurement, and a ruling that changed the tree before the measurement would be
the shape decided by the document rather than by the answer.

- **Point 2 — the `extract` form is the design**, in the one-placeholder form C6
  records, and **Check 68 is the arbiter**: if IAM refuses the expression, or a
  service's positive action is denied, what runs is §5's fallback and not a
  re-opened form.
- **Point 3 — the acceptable drops are API keys, Cloud Scheduler jobs, Artifact
  Registry repositories and Secret Manager secrets.** IAM service accounts and
  Cloud Run jobs are not acceptable drops: they are the feature's own use, so a
  C3 failure there is a fallback question (§5's option 2) rather than an R5 drop.
- **Point 6 — seven roles in EVERY installation**, `belong` included: the roles
  are not conditional on the flag, and `belong` is not exempt. **This is the one
  ruling whose consequence has to wait for Check 68, and the reason is the 0.8.6
  outage**: the installation root is what every installation's `fugaro init`
  applies, so a shape IAM refuses is not one installation's bad apply — it is
  `setIamPolicy` refusing the binding and stopping **every** init, `belong`
  included, which is exactly what the 0.8.6 expression did (F1). Today the seven
  roles and their bindings sit behind `count = var.managed_deployer_scoped ? 1 :
  0` (`deploy/terraform/gcp/modules/installation/deployer.tf`), false in every
  installation, so nothing plans them and `belong` keeps only the unconditioned
  role — and that gate is what makes the ruling safe to record before the
  measurement: **the change that un-gates them is the change that follows Check
  68's pass**, made with the
  measurement in hand and in the release that carries it, not here. A repeat of
  the 0.8.6 init outage across every installation is the risk this dependency
  exists to keep out of the branch that has not measured the shape.
- **Point 7 — one shared condition title** for all seven bindings
  (`fugaro-managed-prefix`), as implemented; a per-service title is not taken.

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

**What it measures, and what it does not.** The check settles F1 (the shape
applies), C1 (the operator counts) and C3 (whether `resource.name` is honoured,
per type — six of the seven services; storage's form is already proved by
Check 32). Its **positive** direction is what carries the weight: a prefixed
resource the impersonated account can act on is a clause that is evaluated and
true, and it is the only thing that distinguishes a working fence from a clause
that can never be satisfied. A service that fails the positive direction leaves
its role granting nothing — fail-closed, and open point 3 above.

The check does **not** probe the non-match direction of `extract` (C7), and the
checklist's step 6 says why in full: every conditioned type's real names carry
their own clause's anchor — a bucket and an object under `projects/_/buckets/`,
a service account under `projects/<project>/serviceAccounts/`, and for the five
`extract` services the anchor is the collection segment the resource's own name
follows — so a name of a conditioned type that misses the anchor cannot be
created; and the names that carry a longer path (a Secret Manager version, a
Cloud Run execution, an Artifact Registry package) report types no clause names,
so their requests are denied on the type term and for want of the permission
before any name is read. **C7 cannot be given a live probe, and this check does
not pretend otherwise**: it follows from C6, which §3 records as **UNVERIFIED**
and uncited, and is modelled by `extractSegment` and pinned by the fail-closed
rows of `TestDeployerConditionFencesOnlyTheManagedPrefix` — both of which are
this repository's reading rather than evidence for it. The near
miss in the check (a name that contains `fmgd-` without starting with it) tests
the prefix test, and is not a substitute for any of this.

Check 67 (the plan's Task 5.3) owns the transitive-reach half — what the
identity can reach through impersonation and through the roles it may grant —
and this check owns the fence half. Neither substitutes for the other.

**Until Check 68 has run and passed, `managed_deployer_scoped` stays false and
`managedDeployerShipped` stays false.**
