# Roles on the existing lists

**Status: design. No code. Every decision in this document is open and belongs to the owner, except
the three the owner ruled on 2026-10-30, recorded in §9's *Settled* list.**

This is subscriptions **S5.1** and forge **E0**: the viewer, operator and admin roles of
`docs/mobile-auth/spec.md` §"Allowlist and roles", laid over the lists an installation already has,
with the enforcement point of every action named. It decides nothing the owner has not ruled. Each
product choice is a row of §9's table, with its options, a recommendation and the cost of each, and
its status is `open`; the build is written from the answers.

The rulings that bind it are in §9's *Settled* list. Where a sentence below reads as settled it is
because one of those rulings settles it, and the ruling is cited beside it; where nothing settles a
question, the sentence asks.

**Numbers.** This work holds **D680–D719** and **Checks 250–259** (verified free: no `D68x`, `D69x`,
`D70x`, `D71x` or `Check 25x` appears anywhere under `docs/`).

**What this document is not.** It is not the sign-in design (§6 describes where a minter could live
as options), not the licence design (T16 fixes the split and §7 only says which parts fall on which
side of it), and not the subscriptions plan: seats, licence keys and the Terms are out of scope by
§10.

## 1. Today

### 1.1 The lists

Five things already name people, and none of them is a role model.

| List | Where it lives | Who is on it |
| --- | --- | --- |
| `terraform.launchers` | `internal/localcfg/localcfg.go:510`, `fugaro setup --launcher` (`internal/cli/init.go:362`) | IAM members who start runs |
| `terraform.operators` | `internal/localcfg/localcfg.go:511`, `fugaro setup --operator` (`internal/cli/init.go:363`) | IAM members who onboard repositories and apply Terraform |
| The project's owners and editors (`admins`) | read from the project's IAM policy into `deploy/terraform/gcp/modules/firebase/variables.tf:92` | whoever Google Cloud already trusts with the whole project |
| `terraform.budget_admins` | `internal/localcfg/localcfg.go:515`, `fugaro setup --budget-admin` (`internal/cli/init.go:372`) | further people who may change caps and kill switches; refuses `domain:` members (`internal/localcfg/localcfg_test.go:981`) |
| The device allowlist `config/admin/devices/<uid>` | `internal/budget/rules/rules.go:458` (`PathDevices`), written over IAM by `fugaro pair` and `fugaro pair revoke` | paired phones; each entry carries a `role` child (`internal/mobileauth/mobileauth.go:144`, `NodeFieldRole`) |

`localcfg`'s member grammar accepts `user:`, `group:`, `domain:` and `serviceAccount:`
(`internal/localcfg/localcfg.go:830`). A `domain:` member therefore grants a whole domain whatever
the list grants — which is why `budget_admins` refuses it (F42,
`docs/forge-authorship-images/reconciliation.md:328`).

Three more facts about the lists matter here.

- **The role on a device node is recorded authority and not enforced authority.** The rules read
  neither the email nor the role child; `USER` turns on the node's *existence* alone
  (`internal/budget/rules/rules.go:435`). A `role` predicate is D225's later half, and D225 answered
  (2026-10-22) that the role is a field on the node, read in v1 by the CLI and the console and
  **never by the rules** (`docs/mobile-auth/reconciliation.md:540`).
- **A pairing inherits and only lowers.** `--role` defaults to the pairing user's own role and a word
  above it is refused (`docs/plans/2026-10-22-mobile-pairing-cli.md:477`, decision 13, amended
  2026-10-29). The pairing user's role is read from the CLI's lists, and `pairInheritedRole` maps
  **both** a launcher and an operator to the device word `operator`, because the lists hold no viewer
  and no admin (`internal/cli/pair.go:511`).
- **A pairing session is a third identity**, the one-shot `pu` claim (`pairingClaim`,
  `internal/budget/rules/rules.go:540`). It reaches the two pairing nodes and nothing else.
- **A run is not a person.** A run's job account holds no role on the Firebase project
  (`modules/firebase/iam.tf:2`) and is on none of the lists, so it holds no `signJwt` on the signer:
  an agent inside a run cannot mint a role claim or write a device node, whatever its prompt says.

### 1.2 What each list grants, per surface

Every grant below is a resource in the Terraform modules today; none of it is a role, and none of it
is checked against a role anywhere.

| Surface | Launchers | Operators | The project's owners/editors and `budget_admins` |
| --- | --- | --- | --- |
| Firebase signer: mint a custom token | token-minter custom role on the signer account (`modules/firebase/iam.tf:35`) | same (`local.people = launchers ∪ operators`, `:30`) | not by this grant |
| RTDB | `roles/firebasedatabase.viewer` (`:44`) | same | `roles/firebasedatabase.admin` (`:53`), which *writes* `/config`, bypassing the rules |
| Firestore (spend history) | `roles/datastore.viewer` (`:65`) | same | same |
| Launch a job (Cloud Run) | the `job_runner` custom role, `run.jobs.run` and `run.jobs.runWithOverrides` (`modules/installation/iam.tf:26`), on the project's job (`modules/project/iam.tf:7`) and each workflow job (`modules/workflow/job.tf:97`) | — | — |
| Launch a job (the opt-in Batch backend, `--provision-batch`) | `roles/batch.jobsEditor`, conditioned to Fugaro-shaped job names (`modules/compute/iam.tf:140`), and `roles/iam.serviceAccountUser` on each run account, never on the project (`:159`) | — | — |
| Cancel a run, read job/execution status | the `fugaroLauncher` custom role: `run.jobs.get/list`, `run.executions.get/list/cancel`, `run.operations.get` (`modules/installation/iam.tf:8`) | — | — |
| Run logs | `roles/logging.viewAccessor` on the isolated log view (`modules/installation/logging.tf:106`) | same | — |
| Runs bucket | read the whole bucket (`objectViewer`, `modules/installation/iam.tf:156`); write `runs/` and `tokens/` only (`objectUser`, conditioned, `:172`) | read and write **all** of it (`objectAdmin`, `:141`) | — |
| Terraform state bucket | — | `roles/storage.objectAdmin` (`:194`) | — |
| Image builds / base images | — | `fugaroBuildSubmitter` (`:77`), the base-image writer on the registry (`:204`) | — |
| Secret Manager | list secrets and versions, metadata only (`fugaroLauncher`, `modules/installation/iam.tf:16`) | — | — |
| The credentials service | nothing: it admits the installation's **job service accounts** only (`internal/credsvc/auth.go:312`, `:475`) | same | same |

Read together, that table says who can do what today:

- **A launcher launches, and that is the whole of the launch surface.** `fugaro run` starts the
  Cloud Run job **with the launcher's own credentials** (F28, `docs/forge-authorship-images/reconciliation.md:291`);
  there is no backend API that starts a job on anyone's behalf (F31, `:321`).
- **An operator applies Terraform and onboards repositories.** Operators hold the state bucket, the
  runs bucket whole, the build submitter, the Cloud Asset reader (`:128`) and the base-image writer.
  An operator who is not also a launcher cannot launch or cancel: the `job_runner` grants on the jobs,
  `launcher_jobs_editor` on the Batch backend and the `fugaroLauncher` role are granted to
  `var.launchers` alone.
- **An owner or editor can do anything on this table and more.** Owners and editors hold
  `roles/owner` or `roles/editor` on the project — Terraform, Secret Manager and the Firebase project
  included — and `roles/firebasedatabase.admin`, which bypasses the rules. §2 calls this the *admin*
  column.
- **A `budget_admin` who is on no other list holds two grants and nothing else:**
  `roles/firebasedatabase.admin` and `roles/datastore.viewer` (`modules/firebase/iam.tf:31`, `:53`,
  `:65`). They can write anything in the database over IAM, `/config` (caps and kill switches) and
  the device nodes included, and cannot launch, cancel, read the runs bucket or apply Terraform.
- **Every launcher and every operator can mint a token for any identity in the project.** Both are
  in `local.people`, and the token-minter role is granted to that set (F44,
  `docs/forge-authorship-images/reconciliation.md:310`). A claim, a uid or an email in a custom token
  therefore binds nothing against them — the reason D628 requires a minter the ranked people cannot
  call.
- **An RTDB `roles/firebasedatabase.viewer` holder bypasses the rules.** The rules bound *client
  tokens*; they are not what keeps `config/admin` private from a launcher, which reads it through
  IAM (`internal/budget/rules/rules.go:61`, `:496`). Any confidential thing placed in RTDB is
  readable by everyone on either list.
- **No viewer exists anywhere, on any surface** (F6, `docs/subscriptions/reconciliation.md:89`,
  `:101`). The `viewer` word exists in exactly two places: `internal/mobileauth`'s closed set, and
  the `--role` flag on a pairing, where it can only be reached by *lowering* a launcher's or
  operator's device (`internal/cli/pair.go:511`). It has never been enforced by anything, and it
  grants nothing: a device whose node says `viewer` reaches precisely what any other device node
  reaches.
- **Nobody is a viewer in practice, and seats can only be estimated** (F7, `:103`): Firebase Auth
  holds the run identities and the paired devices, not the people, and the people are spread over
  four IAM lists with no shared key.
- **The console has no sign-in and spawns the CLI** (F31). Its fixed operation table
  (`console/src/main/ipc.ts`, `run.launch` at `:671`) drives `fugaro run` in a child process, so every
  console user is a launcher by construction, with the credentials the CLI finds in their own
  environment. It has no cancel operation.

## 2. The role model

**viewer ⊂ operator ⊂ admin.** The rank order is not new: `internal/mobileauth` already encodes it
as a closed set (`RoleViewer`, `RoleOperator`, `RoleAdmin`, `mobileauth.go:81`), with `Rank`
(`:115`) and `AtLeast` (`:127`) over it, and `ParseRole` (`:98`) refusing every other spelling. This
document proposes nothing about that type; it fixes what each rank would *hold*, which is the part no
code states today.

Each cell names the enforcement point that would have to hold the action, and whether that point is a
**boundary** (the API refuses without it) or only a **convenience** (the person runs their own CLI, so
a check there is a courtesy that a determined holder bypasses). A cell today's enforcement cannot
express is marked: **`gap`** where the action is reachable and nothing names it, and **`must not`**
alone on the viewer row, because no grant in §1.2 can hold a read-only person — every list that
grants a read there grants a write beside it.

| Action | viewer | operator | admin |
| --- | --- | --- | --- |
| Read run status and logs | runs bucket `objectViewer` + job/execution `get` + log view — **boundary**, narrowed to reads | same — boundary | same, plus `firebasedatabase.admin` — boundary |
| Read spend | `roles/datastore.viewer` on the history — **boundary** | same — boundary | same — boundary |
| Launch | must not | `job_runner` on the jobs (Batch: `batch.jobsEditor`, conditioned, + `serviceAccountUser`) — **boundary** | today: `roles/owner`/`editor` — boundary |
| Cancel | must not | `run.executions.cancel` — **boundary** | today: as owner/editor — boundary |
| Steer or talk to an agent | must not | the launch itself is the whole of it today — **gap**: nothing distinguishes steering from launching | same |
| Change caps and kill switches | must not | must not — **gap**: today `firebasedatabase.admin` writes `/config` over IAM, and the rules give no client token a write there | `firebasedatabase.admin` — boundary |
| Onboard a repository | must not | the state bucket, build submitter, base-image writer — **boundary** | same |
| Apply Terraform | must not | the state bucket `objectAdmin` and the operator's own credentials — **boundary** | same, and today `roles/owner`/`editor` |
| Manage people and roles | must not | must not — **gap**: today granting a list is `fugaro setup`/`fugaro user` on an operator's own credentials, and anyone holding the deployer can do it | **gap**: `roles/owner`/`editor`, i.e. whoever can edit the project's IAM policy, and in practice the deployer |
| Publish the project policy | must not | **gap**: no grant names it; today it travels in the shared config the runs bucket carries, which operators write whole | same |
| Manage credentials | must not | must not — **boundary**: the credentials service admits job service accounts only (`internal/credsvc/auth.go:312`), and the credential's secret has `secretAccessor` for the service's own account alone (`modules/installation/credentials.tf:113`) | `roles/owner`/`editor` reach Secret Manager; the service's own account is the only reader of the credential |

The **viewer** row is where the matrix and today part company, and it is the row T16 asks about.
Every one of its `must not` cells is unenforceable today: the mint is granted to `local.people`
(`modules/firebase/iam.tf:35`), and `run.executions.cancel` comes bundled in `fugaroLauncher` with
the reads, so a person cannot be given a read without also being given the writes beside it — which
is F6's "there is no viewer anywhere" read from the other side. A read-only list needs its own role:
`run.jobs.get`, `run.jobs.list`, `run.executions.get`, `run.executions.list` and `run.operations.get`,
and neither of `run.executions.cancel` nor the mint.

The two `gap`s in the operator column are the same shape one rank up: nothing distinguishes steering a
run from launching one, and nothing distinguishes publishing the project policy from writing the
shared config it travels in.

## 3. The terminology clash

The words in use do not mean the same thing as the roles, and the two vocabularies overlap exactly
where it is most confusing.

| Word today | What it actually grants | The role it is closest to |
| --- | --- | --- |
| `launchers` | launch a run, cancel one, read status and logs | **operator** |
| `operators` | apply Terraform, onboard repositories, write the whole runs bucket, submit builds | **admin** |
| (nothing) | read the project's runs without any write | **viewer** |

So "the roles on the existing lists" is not a relabelling; it is a decision. The owner ruled the
vocabulary on 2026-10-30 (§9 *Settled*: map now, rename later); which list becomes which role is
D681. The options were: the mapping may be accepted as-is
(today's *operator* is the role *admin*, today's *launcher* is the role *operator*, and a new list is
the viewer); the lists may be renamed pre-1.0 while the surface is still free to move (the
pre-1.0 rule in `CLAUDE.md`); or both vocabularies may be kept and the mapping documented. The third
option is the one with a permanent cost: two words for one thing, in the CLI, the Terraform
variables, `internal/cli/roles.go`'s comments and every operator's config file.

Whatever the owner answers, one thing is already fixed by what exists: `fugaro pair --role` writes
the device words `viewer`, `operator` and `admin`, and `internal/cli/pair.go:511` maps a launcher and
an operator *both* to the device word `operator`. The device vocabulary and the IAM vocabulary meet
there and nowhere else, and the mapping between them is the answer's to state.

## 4. The viewer in the cloud (T16)

T16 rules the placement: **the cloud keeps enforcing the launcher, operator and admin lists in MIT
core**, and a **small MIT core change** adds the viewer's cloud enforcement (`docs/subscriptions/reconciliation.md:450`).
D446 rules the shape: viewer, operator and admin **enforced system-wide, the cloud included**, not
just in the console (`:398`).

What a read-only list would grant:

- `roles/firebasedatabase.viewer` — read the database, as launchers and operators already do;
- `roles/datastore.viewer` — read the spend history;
- a read-only job role: `run.jobs.get`, `run.jobs.list`, `run.executions.get`, `run.executions.list`,
  `run.operations.get` — the `fugaroLauncher` role **minus** `run.executions.cancel`;
- `roles/logging.viewAccessor` on the isolated log view, if the owner wants a viewer to read logs;
- read access to the runs bucket, at whatever scope D683 picks.

What it must **not** grant, and what each absence protects:

- **no `job_runner` on any job, and on the Batch backend no `batch.jobsEditor` and no
  `serviceAccountUser`** — a viewer cannot start a job and cannot act as a run account, which is the
  whole of the launch surface (F28);
- **no token-minter role** — otherwise a viewer can mint the identity of anyone in the project, and
  the role it is being denied exists only on paper (F44);
- **no Terraform state bucket** — a viewer cannot read or lock the installation's state;
- **no `secretmanager.secretAccessor`** — a viewer reads the secret list's metadata and no value;
- **no write anywhere**, including `firebasedatabase.admin`, which writes `/config` over IAM.

**Can a viewer be held by IAM alone, or does it need D628's minter?** That is the question T16 leaves
open, and it is D685. IAM alone is the smaller change: a viewer list, the grants above and one new
role, all in Terraform, all in core, and a viewer who never signs in to Firebase at all — so nothing
is minted, nothing is claimed, and none of F44's problem is touched. It has a cost the owner should
see: an IAM viewer is not the console's and the app's viewer, because neither has a sign-in, and it
cannot be revoked faster than IAM reevaluates. The minter buys the role as a claim and the console's
and app's enforcement; it also buys a new service and a new trust boundary (§6), and it does not
replace the IAM grants, because a claim checked by the rules still needs an IAM grant to read the
rules' own bypass paths.

The second question the row raises is D683: the runs bucket is one bucket holding `runs/`, `tokens/`,
`builds/`, `cache/`, `locks/` and `fugaro/`. `objectViewer` with no condition is one
line, and it also hands a viewer `fugaro/` — the project layer, the shared config and the recipes. A
condition can narrow it to prefixes; listing needs `storage.objects.list`, which a condition on an
object's name cannot grant (`modules/installation/iam.tf:154`). So the choice costs either a wider
read than the role sounds like, or a viewer who cannot list the bucket.

## 5. Identity and sign-in

**D628 (answered 2026-10-29): a minter the people being ranked cannot call**, role as a claim, public
sign-up stays refused (`docs/forge-authorship-images/reconciliation.md:414`). **D627 (answered):** no
hosted launch path now; `fugaro user` over the IAM lists is the interim (`:413`). **D604/U1** is the
authenticated launcher identity that would replace the claim slice A states, and it is E0's subject
with this document.

The constraint D628 creates is precise, and F44 is why: today's signer *is* callable by every
launcher and every operator, because both are in `local.people` and the token-minter custom role is
granted to that set (`modules/firebase/iam.tf:35`). A role claim minted by that signer is minted by
the people it ranks. So a minter must be an identity that the ranked people cannot impersonate or
act as.

Where such a minter could live — options, not a choice (**D686**):

- **A new Cloud Run service in the installation, with its own service account** as the requesting
  identity: it checks membership and mints. This is the honest shape and the largest change:
  a new deployed service, a new credential path, a new thing to keep alive, and a check inside it
  that is now the authorization boundary. D628's words read as pointing here, and D686's
  recommendation is the next shape instead, for the reason given there — the owner should read the
  two together.
- **The existing signer account, with the minter role narrowed**: today's `token_minter` grant is
  removed from `local.people` and given to one new account the people are not. The smallest diff,
  and it is not free: `local.people`'s minter grant is what the *pairing* path relies on today, so
  removing it changes `fugaro pair` (and the `signJwt` the launcher makes for a run's budget token),
  which the design would have to place on the new minter instead.
- **No minter now**, with IAM alone holding the viewer (§4). The interim reading of D627, and the
  one that ships no new service.

**It is a new service, whichever of the first two shapes is chosen, and that is worth stating
plainly**: either adds a deployed component whose whole job is to decide who someone is. That is a
new trust boundary, it is built after the lists are the authority it consults, and its failure mode
is the sign-in path for the console and the app.

`fugaro user` (forge **E1**) is the interim over the lists, already planned and specified there
(`docs/forge-authorship-images/implementation-plan.md:594`): it edits `terraform.launchers` and
`terraform.operators` safely, refuses when an operator's local list has drifted from the
installation's own outputs, and tells the operator that the change lands on their next `fugaro
update`. This document does not re-specify it. It only notes, in **D692**, that E1's `--role` names
`launcher|operator` and has no viewer, so a viewer list has no writer until either E1 widens or a new
slice does it.

## 6. Suspension (D629, answered)

**D629 (answered 2026-10-29): a suspended person keeps the seat until the 30-day move rule allows
moving it** (`docs/forge-authorship-images/reconciliation.md:415`). F32 is why: a suspended user who
freed their seat at once would let a customer rotate a seat by suspending and resuming (`:324`).
That answer is about the licence, and it is out of scope here (§10).

What roles need from it is only this: **what a suspended person keeps and loses on each surface**. It
is a real question and it is not settled by D629, so it is **D693**. The shape of the surfaces:

| Surface | Suspension would have to reach |
| --- | --- |
| Launch | the IAM grant (`job_runner` on the jobs; on Batch, `batch.jobsEditor` and `serviceAccountUser`) or a check the launcher runs |
| Cancel | `run.executions.cancel` |
| Read the project | the RTDB and bucket reads |
| Sign-in | the minter's check, or the auth user's own state |
| Pairing / device | the device node — the one surface where revocation *is* immediate today (`PathDevices`, `internal/budget/rules/rules.go:435`) |

The last row is the interesting one and the reason the question is worth asking rather than
assuming: the device node was chosen precisely because deleting it revokes at once, "the next read or
write of every family USER reaches fails its existence test"
(`internal/budget/rules/rules.go:428`), whereas an IAM change and a minted claim both lag. A
suspension built on IAM is not immediate; a suspension built on a node it can delete is.

## 7. Core and `ee/`

T16 fixes the split (`docs/subscriptions/reconciliation.md:450`), and D637 fixes the licence
placement (`docs/forge-authorship-images/reconciliation.md:423`): MIT holds the lists' enforcement
and `fugaro user`; `ee/` holds the role model, its UX in the console and the app, and "roles
enforcement beyond the lists".

| Part | Side | Why |
| --- | --- | --- |
| The IAM grants for launchers, operators, admins and the new viewer list | **core (MIT)** | "the cloud keeps enforcing the launcher, operator and admin lists in MIT core" (T16); enforcement that protects a project stays free |
| A read-only job role and the viewer's bucket/log grants | **core (MIT)** | T16's "small MIT core change for the viewer's cloud enforcement" |
| The `viewer`/`operator`/`admin` rank type | **core (MIT)** | `internal/mobileauth` already holds it and is already core |
| RTDB rules that read a role | **core (MIT)** | a rule is the deployment's own boundary, and the rules are core |
| The action matrix as an actual check — which role may do which action, and the answers when one may not | **`ee/`** | this is the role model D637 puts beyond the lists |
| The console's Members screen and its read-only gating | **`ee/`** | D637's "SSO and domain or workspace invites, roles enforcement beyond the lists" |
| The app's suspend/resume surface | **`ee/`** | same |
| The waiving, delegating or extending of a list's meaning (workspace invites, D638) | **`ee/`**, and **out of scope here** | D638 waits for the Workspaces spec |

The `ee/` parts reach the core through the seams `docs/design/ee-boundary-moves.md` already
describes: every seam is a package-level value whose default is the core implementation, and
`ee/register` (reached through `cmd/fugaro/ee_on.go`) is the one assembly point
(`docs/design/ee-boundary-moves.md:39`, `:435`). What that means for the role model is **D694**: a
seam whose core default is *today's* behaviour — no ranking at all, so nothing is refused — and
whose `ee/` implementation is the matrix. Nothing in core may import `ee/`, and the core default
must keep working untagged, which is the same rule the governance seam obeys.

## 8. Build slices

Written from the answers. Each is security-sensitive where it touches an authorization boundary, and
each of those is **opus + mandatory independent review** per the repository's practice for IAM and
rule changes.

**R1. The viewer list, cloud side (core, ships inert).**
Files: `internal/localcfg/localcfg.go` (a `viewers` list beside `launchers`), `internal/cli/init.go`
(`--viewer`), the Terraform variables and IAM in `deploy/terraform/gcp/modules/firebase/` and
`deploy/terraform/gcp/modules/installation/`. A new read-only job role, and the grants §4 lists.
Tests: `TestViewerListRefusesDomain`, `TestViewerGetsNoMinterRole`, `TestViewerGetsNoJobRunner`,
`TestViewerRoleHasNoCancelPermission`. Depends on: **D681, D682, D683, D684**. Ships inert:
an installation with an empty viewer list applies exactly today's plan. **For operators:** the list
is a Terraform variable, so the change lands on an owner-run `fugaro update`; the release notes carry
the step. **Security-sensitive.**

**R2. `fugaro user --role viewer` (core).**
Files: `internal/cli/user.go` (E1's file), `internal/cli/user_test.go`, the plugin skill and the
regenerated CLI reference. Depends on: E1, **D692**. Tests: extend E1's suite with
`TestUserAddViewer`. **Security-sensitive** (an IAM list). **For operators:** as E1, the edit lands
on the owner's next `fugaro update`.

**R3. The role model in `ee/` (ee, ships inert).**
Files: `ee/roles/` (the rank type as policy, the action matrix and its refusals),
`ee/register/roles.go`, and the core seam's default. Depends on: R1, **D694**. Tests: the matrix's
own table test plus `ee/register`'s identity check. Ships inert: untagged, the core default ranks
nobody and refuses nothing. **Security-sensitive.**

**R4. The minter (core or `ee/` — **D686**).**
Files: a new service, its Terraform, its caller check and its tests. Depends on: **D686**. Not
inert: it is a new deployed component, and it is the sign-in path's boundary. **Security-sensitive.**
**Check 250–252** (owner-run) — the minter refuses a member of the ranked set, and a role claim it
mints is what the rules read.

**R5. A rule that reads the role (core).**
Files: `internal/budget/rules/rules.go` and its goldens, `internal/projectpolicy/version.go` (the
generation bump). Depends on: **D688**, R4. Tests: the rules' own golden tests and a
`Test...RolePredicate` beside them. Ships inert only in the sense that an installation that has not
redeployed its rules sees the old document; the document itself moves, which is a generation and an
owner step. **For operators:** redeploy the rules. **Security-sensitive.** **Check 253–255.**

**R6. Suspension (core or `ee/` — **D693**).**
Files: `fugaro user suspend|resume` and whatever surface §6 picks. Depends on: **D693**, R1.
**Security-sensitive.**

**R7. The console's and the app's role surfaces (ee).**
Files: the console's Members screen (read-only) and the app's suspend surface. Depends on: R3, R6.
**Check 256–257.** UI only; the enforcement stays in R1–R6.

**R8. The interim (already planned: forge E1).**
`fugaro user ls|add|rm` over the lists
(`docs/forge-authorship-images/implementation-plan.md:594`). Not re-specified here; R2 is the one
addition this design asks of it.

**Checks 258–259** are left for the two the build finds it needs; §9 records that the numbering is
held.

**Release placement (owner, 2026-10-30).** R8 (E1) ships in 0.14.0 if it is in time. R1, R4 and R5
are cloud role enforcement and none of them is in 0.14.0; enforcement is 0.15.0.

**Files more than one slice touches.** Everything else is one slice's own, so slices that share no
row here can run in parallel once their dependencies are answered.

| File | Slices | Order |
| --- | --- | --- |
| `internal/cli/user.go`, `internal/cli/user_test.go` | R8, R2, R6 | R8 first: it creates them |
| the plugin skill and the regenerated CLI reference | R8, R2, R6 | one slice at a time, regenerated last |
| `deploy/terraform/gcp/modules/firebase/iam.tf` | R1, R4 | R1 first: R4 moves the minter grant R1 withholds |
| `internal/budget/rules/rules.go` and its goldens | R5, and R4 if the minted claim changes the rules | R4 before R5 |
| the release notes' *For operators* section | R1, R2, R4, R5 | each slice adds its own entry |

## 9. Decisions for the owner

### Settled

Restated, with its source, so that nothing below is read as open that is not.

- **D446** — a separate design first, and viewer/operator/admin enforced **system-wide, the cloud
  included**, not just in the console. It creates D433's free viewer.
  (`docs/subscriptions/reconciliation.md:398`)
- **T16** — the cloud keeps enforcing the launcher, operator and admin lists **in MIT core**; `ee/`
  adds the role model, its UX and the viewer built on those lists, with a small MIT core change for
  the viewer's cloud enforcement. (`:450`)
- **D627** — roles design first; `fugaro user` over the IAM lists meanwhile, each change an
  owner-run `fugaro update`. (`docs/forge-authorship-images/reconciliation.md:413`)
- **D628** — sign-in is a custom token minted by a service the people being ranked cannot call, role
  as a claim; public sign-up stays refused. (`:414`)
- **D629** — a suspended person keeps the seat until the 30-day move rule allows moving it. (`:415`)
- **D637** — licence placement: the lists' enforcement in MIT, the role model and its UX in `ee/`.
  (`:423`)
- **D225, v1 half (answered 2026-10-22)** — the role is a field on the device node, written day one,
  read in v1 by the CLI and the console and **never by the rules**.
  (`docs/mobile-auth/reconciliation.md:540`)
- **Pairing decision 13 (amended 2026-10-29)** — the device role is inherited from the pairing user's
  role and may only be lowered. (`docs/plans/2026-10-22-mobile-pairing-cli.md:477`)
- **2026-10-30, vocabulary (was D680)** — map viewer, operator and admin onto the existing IAM lists
  now; rename the lists later. Which list becomes which role stays D681.
- **2026-10-30, `fugaro user`** — E1 ships in 0.14.0 if it is in time.
- **2026-10-30, enforcement** — no cloud role enforcement in 0.14.0; it is 0.15.0 (R1, R4, R5).
- **D433** — free read-only viewers once the viewer role exists (D446). Out of scope here (§10), but
  it is what a viewer *is for*.

### Open

| # | Question | Options | Recommendation | Cost of each | Status |
| --- | --- | --- | --- | --- | --- |
| D681 | Which existing list becomes which role? | (a) `launchers`→operator, `operators`→admin, new list→viewer; (b) a fresh launcher/operator/viewer triple and today's `operators` becomes an onboarding list only | (a) | (a) matches what each list already grants (§3); (b) renames a list whose grants do not move, so the name and the grant disagree until the grants move too | open |
| D682 | Where does the viewer list live? | (a) a new IAM list, core MIT; (b) derive viewers from the device nodes' `role` child; (c) no viewer list, the console/app only | (a) | (a) one more list and its grants; (b) the device node is per-phone, `session`-scoped and written by `fugaro pair`, so a viewer without a phone cannot exist and IAM would have to read RTDB to answer; (c) fails D446's "the cloud included" | open |
| D683 | How much of the runs bucket does a viewer read? | (a) `objectViewer` on the whole bucket; (b) a conditioned read on `runs/` and `fugaro/`; (c) none, reads come through the CLI | (a) | (a) also hands a viewer `cache/`, `locks/` and the history; (b) no `storage.objects.list`, so a viewer cannot list the bucket; (c) a viewer cannot read logs or artifacts at all | open |
| D684 | Does a viewer read run logs? | (a) `logging.viewAccessor` like launchers and operators; (b) no log view | (a) | (a) a viewer sees the run's own output, which may quote the repository's code; (b) the role reads status but not what the run said, which is most of what a run is | open |
| D685 | Is a viewer held by IAM alone, or by D628's minter? | (a) IAM alone; (b) the minter only; (c) both | (a) first, (c) later | (a) no sign-in, no claim, no new service — but no console or app enforcement and IAM-latency revocation; (b) a new service before a viewer can read anything; (c) both costs, and the honest end state | open |
| D686 | Where does the minter live? | (a) a new Cloud Run service with its own account; (b) the existing signer with the minter role narrowed to one account the ranked people are not; (c) none now | (b) | (a) a whole new deployed component and a new trust boundary; (b) the smallest diff, and it takes the minter away from `fugaro pair` and the run's budget token, both of which use it today; (c) the console and the app stay unranked | open |
| D687 | How does a granted role travel? | (a) a claim minted by the minter; (b) read from the lists at each check; (c) a claim for the console/app and the lists for the cloud | (c) | (a) a demotion waits for the token to expire (D225's cost, `docs/mobile-auth/reconciliation.md:545`); (b) every check becomes an IAM read, and neither the console nor the app has an identity to make it with; (c) two sources of truth to keep in step | open |
| D688 | Does an RTDB rule read the device `role`? | (a) never (D225's v1 answer); (b) a rule reads it, as D225's later half allows | (b), when R5 is built | (a) a device's `role` stays recorded authority, so a `viewer` device reaches what any device reaches; (b) a rules generation bump, an owner redeploy, and a demotion that lands on the next request | open |
| D689 | Are the project's owners/editors, and `budget_admins`, automatically the role *admin*? | (a) yes, both; (b) owners/editors only; (c) neither — admin is a list of its own | (a) | (a) `roles/owner` is a wider grant than any role, and the mapping is a statement about Google's project rather than about the role model; (b) `budget_admins` can already write `/config` and would be an operator on paper; (c) the role *admin* would hold less than an editor holds anyway | open |
| D690 | Does the CLI rank its own commands? | (a) no (`internal/cli/roles.go:22` — nothing there authorises); (b) a convenience check for viewer-level commands | (a) | (a) a viewer running `fugaro run` gets GCP's refusal rather than Fugaro's sentence; (b) a check in the person's own binary, which they can replace — a courtesy, and it must never be reported as a boundary | open |
| D691 | How does the console know a role, with no sign-in (F6)? | (a) it does not, until the minter exists; (b) it reads the installation's own lists through the CLI; (c) a local configuration by the operator | (a), with (b) for a read-only Members view | (a) the console stays unranked, which is today; (b) a list read is not an identity — it says what the local config says, not who is at the keyboard; (c) a local file decides who may do what, in the app that can launch | open |
| D692 | Does `fugaro user` gain `--role viewer` (E1 today names launcher and operator only)? | (a) yes, in R2; (b) no, a separate command | (a) | (a) one flag on a command that already writes IAM lists, and it makes the viewer list writable at all; (b) a second command doing the same thing to a different list | open |
| D693 | What does a suspended person keep and lose, per surface? | (a) everything except launch and cancel; (b) nothing — a full revocation; (c) read-only, cancel kept | (c) | (a) a suspended person still reads every run and every log; (b) suspension and the 30-day rule then interact badly with a return to service, and it is more than D629's answer needs; (c) matches the lapse state T12 already rules for the console and the app (`docs/subscriptions/implementation-plan.md:57`) | open |
| D694 | Through which seam does `ee/` register the role model? | (a) a new `Set*` seam beside the governance ones, core default = nobody ranked; (b) the command constructor pattern `NewReportCmd` uses; (c) a build tag | (a) | (a) one more package-level value and the `ee/register` panic rule to obey; (b) the matrix is not a command, so it has no constructor to hang on; (c) `cmd/fugaro/ee_on.go` is the only route and a second one is what `internal/eeboundary` exists to refuse | open |
| D695 | Which of Checks 250–259 runs first, and what does each prove? | the assignment in §8, or another | §8's | each check is an owner-run step on a real installation, so a check that is not run is the failure mode — the same rule as the live checklist | open |

## 10. Out of scope

- **Seat counting and what a seat is.** D433 makes a seat a pricing rule and D439 the estimate
  (`docs/subscriptions/implementation-plan.md:51`); both belong to the subscriptions plan, and no
  figure may appear here (D430).
- **Licence keys, the key service and licensing enforcement.** D438, D440, D447/T21; the
  subscriptions plan and `docs/licensing/`.
- **The Subscription Terms**, including the words for suspension and for a read-only user: counsel's
  (O4, `docs/forge-authorship-images/reconciliation.md:507`).
- **Workspaces and workspace invites.** D638 waits for the Workspaces spec
  (`docs/forge-authorship-images/implementation-plan.md:585`).
- **Central orchestrators and the hosted launch path** (D435, F31): a launch path that starts jobs on
  someone else's behalf is the central orchestrator's scale, ruled out of the interim by D627.
- **Pricing**, of any kind.
