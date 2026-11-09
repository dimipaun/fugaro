# User roles and scopes: reconciliation

**Status: design answered by the owner on 2026-11-07; plan only; nothing is implemented; no task
approved.** This file amends [spec.md](spec.md) against the code. **Where it disagrees with the spec, it
wins.** Checked against `origin/main` at `91ae90aa3` (main's tip on 2026-11-07, merged into the branch; rounds 1 and 2 re-verified every finding below on main). **Rounds 1 and 2 (independent security reviews) ran on 2026-11-07 and are applied here; see §7 and §8. No third round has run.**

**Numbers.** This work holds **D1580–D1599** and **Checks 400–409** and reserves all of them. The
siblings are `docs/no-prompt-policy` (D1540–D1559, PR #1906) and `docs/composite-commands`
(D1560–D1579); this spec leans on both (its "Fits with other specs").

## 1. What the repo already has

| Spec concept | What the repo already has |
| --- | --- |
| viewer / operator / admin, ranked | `internal/mobileauth`: `Role`, `RoleViewer`, `RoleOperator`, `RoleAdmin`, `Rank`, `AtLeast`, `ParseRole` (closed set). Used only for device nodes; "recorded authority, not enforced" (`docs/design/roles.md` §1.1) |
| The roles over the installation's people | **`docs/design/roles.md`** (D680–D695, owner answered 2026-10-30, all as recommended): `launchers` = operator, `operators` = admin, a new viewer list; project-wide IAM lists with no per-repo grant |
| A command that grants and removes | `fugaro user` (`internal/cli/user.go`: `ls`, `add`, `rm`): edits `terraform.launchers` / `terraform.operators` in **this machine's own project config**; nothing applies until the next `fugaro update` |
| Who the caller is | `internal/cli/roles.go`: `operatorMember`, `launcherMember`, `meMember` read the local config's lists. Deliberately "never shared with anything that authorises" |
| Repo set, and "every repo incl. later" | the published policy's `allow_repos`, written by `fugaro project repos add/rm` (`internal/cli/project_enroll.go`); `["*"]` is the list's own all-repos entry |
| Repo patterns with `*` | **`projectpolicy.MatchRepo` and `projectpolicy.Canonical`** (`internal/projectpolicy/match.go`): canonical spelling `<provider>/<owner>/<name>`; `*` inside one segment never crosses `/`; `*` alone admits everything; a bare `acme/*` is illegal because it names no provider |
| Project guardrails that repos may only tighten | the project layer (`internal/config/layer.go`, `layerTopKeys`) and `internal/policy` merge: only tightening wins |
| An image tied to a repo | **does not exist**: the image catalog is a project policy node (`projectpolicy.ImageCatalog = config.Catalog`) and the per-repository derived image was removed (`docs/repo-image-removal/`) |
| Lapse | `docs/subscriptions` (D436: no perpetual fallback; read-only console and app after the grace period) and `docs/licensing`; read-only "keeps cancel and the kill switch" (T12) |

## 2. Findings

**F1 — Exists: the role vocabulary and ranking.** `mobileauth.Role` is the closed set the spec names, with
`Rank` and `AtLeast`. *Consequence:* the resolver is built on that type and adds no second role set; the
type moves (or is re-exported) into the new `internal/access` package so the CLI, the console's reads and
the pairing path call one function (RS1).

**F2 — Rule: the roles are already designed, project-wide.** `docs/design/roles.md` fixes what each rank
holds per surface, names the enforcement point of every action and marks which are **boundaries** (an
API refuses) and which are **conveniences** (the person runs their own CLI). The spec adds the one thing
that design lacks: scope. *Consequence:* this package extends that design; it does not replace it, and
the viewer list, the read-only job role and the D683/D684 bucket and log questions stay roles.md's.

**F3 — Wrong: the words collide.** Today's `--launcher` list is the spec's *operator*; today's
`--operator` list (onboard repos, apply Terraform, write the whole runs bucket) is the spec's *admin*.
`internal/cli/pair.go` `pairInheritedRole` maps **both** to the device word `operator`. *Consequence:*
D1581 (renamed, migrated in `update`).

**F4 — Gap: the cloud has no per-repo grant.** Every list in roles.md §1.2 is project-wide, and a launcher
launches any repo's job with their own credentials. A repo-scoped role cannot be a cloud barrier without
new Terraform. *Consequence:* D1580 — the scoped pairs are a **policy check** in the CLI and the console
first; per-repo cloud enforcement (per-job `run.invoker`, prefix-conditioned bucket grants) is a later
slice (RS12), and the docs say plainly that a holder of a project-wide IAM grant can bypass a scoped
check with `gcloud`.

**F5 — Exists, and amends the owner's scope grammar: repos are `<provider>/<owner>/<name>`.** The spec's
`frontend,api`, `dimipaun/*` and `acme/api-*` do not match the repo's one spelling.
`projectpolicy.MatchRepo` already implements "`*` stays inside one segment, never crosses `/`" and
**refuses a bare `owner/*`**. *Consequence:* **D1583 amended after review** (§3).

**F6 — Wrong: "refuses to create or register a repo named `project` or `all-repos`".** A canonical repo
contains slashes, so it cannot equal either word. `fugaro project repos add` takes `provider/owner/name`, `owner/name` or `*`
and so cannot be given a bare word (`canonicalRepoArg` refuses a one-segment name: it needs `provider/owner/name` or a listed `owner/name`). *Consequence:* the reservation is a **test** that the two keywords can
never parse as a repository (RS1), not a refusal in `repos add`.

**F7 — Wrong: "all repos, including repos added later" needs a definition.** The repo set is
`allow_repos`; a repo not enrolled there cannot run at all. *Consequence:* `all-repos` means every
repository the policy's `allow_repos` admits **at the moment of the check**; a newly enrolled repo is
covered with no change to any grant, and a wildcard pattern is re-evaluated the same way (spec's own rule).

**F8 — Wrong, corrected in round 1: "an image tied to a repo".** Two things are true. The **catalog**
(`projectpolicy.ImageCatalog = config.Catalog`) is a project-level node with no repo binding. But the
**per-repository derived image still exists on main**: `fugaro image build [name] --workflow`
(`internal/cli/image.go`) and `internal/cli/image_refresh_steps.go` still build and refresh a workflow's
derived image, and only R0/R1 of `docs/repo-image-removal/` have merged. *Consequence:* catalog entries are
project-level, project-scope admin only (D1584); **rebuilding a workflow's derived image is a repo-scoped
action** (admin on that repo) until the removal chain deletes it, and RS6c covers it or depends on that
chain.

**F9 — Exists: "repo admins change settings only within the project guardrails".** The project layer and
`internal/policy` merge already only tighten. *Consequence:* a repo-scoped admin's writes go through the
same merge; no new guardrail machinery. What is new is only **who may call** the write (RS6b).

**F10 — Gap: where the grants live.** `fugaro user` edits a machine-local list and applies at the next
`update`, and refuses when two machines drift (`user.go` "add and rm read the installation's own outputs
first"). A list of scoped pairs that "takes effect right away" and that every machine and the console
read cannot be a per-machine file. *Consequence:* D1585 (stored in the published project policy, an
`access` node; the IAM lists become **derived** from the project-scope pairs by the composite-commands
step registry).

**F11 — Rule, corrected in round 1: the lapse is already ruled.** Subscription Terms §9.1 (counsel-approved
2026-10-29, `docs/licensing`) keeps cancel and the kill switch in read-only use, and D436/T12
(`docs/subscriptions/reconciliation.md` F15) ruled that after a lapse the console and the app read and
cancel while the MIT CLI keeps working. *Consequence:* no new tension; the spec's "can't launch jobs or
change anything" for a **lapsed** viewer is already amended by T12.

**F12 — Rule: a lapse is not a role rewrite (D1582 restates D436).** The spec's last acceptance criterion
("all users are limited to viewer") would also stop the **MIT core CLI**. The owner's 2026-11-07 answer
(only Subscription-edition surfaces drop to viewer, assignments untouched and returned on resume) is
D436 in this design's words. A lapse is a **licence-gate input** to the resolver, supplied through `ee/`'s
seam (roles.md D694). The spec's acceptance sentence is replaced by "when a subscription lapses, the
Subscription surfaces limit every user to viewer (cancel and the kill switch kept, T12); no assignment is
changed or lost; the core CLI is unaffected".

**F13 — Rule: a workspace is not the project scope.** `docs/workspaces` groups **projects**; D1295
(answered 2026-11-04) refused any workspace role that maps to a member project's role. *Consequence:*
`project` in this spec is one Fugaro project, never a workspace; no grant crosses a project boundary.

**F14 — Rule: no prompts.** A role refusal is a refusal, never a question, and names the role and scope
it needs (no-prompt spec rule 1; `docs/no-prompt-policy`, #1906). The role check is a **CLI policy
check**, not the managed gate (`internal/managed`, `internal/infra/tf/cover.go`), which the sibling
spec's D1545 reduces to audit-only: the two are not the same mechanism and this design does not rest on
the gate's refusal.

**F15 — Gap: an agent session.** A coding agent runs the person's commands with the person's
credentials (`agentMarker`). The spec says nothing about whether an agent may grant a role. The sibling
no-prompt D1542 makes cap raising owner-session-only for the same reason. *Consequence:* D1590.

**F16 — Gap: pairing.** `pairInheritedRole` reads the two lists and gives a device `operator`;
roles.md decision 13 says a device inherits the pairing user's role and may only lower it. A scoped
user has no single role. *Consequence:* D1588.

**F17 — Resolved (not asked): grant rules.** (1) A grantee's scope must be a **subset of the grantor's
scope at that role or higher**: a repo admin holding `github/acme/api-*` cannot grant `github/acme/*`,
and no one but a project admin grants `project` or `all-repos` (the spec's rule, made exact by a
subset test over canonical patterns, RS1). (2) Removing the **last project admin** is refused, and
self-removal stays refused (the existing `fugaro user rm` ruling). (3) The GCP project's **owners and
editors are implicit project admins** and so are `budget_admins` (roles.md D689 answered as
recommended). (4) A pattern grant is checked against the **canonical spelling** only; a case or
`.git` variant is normalised by `Canonical` before it is stored. (5) A run's job account holds no role on the Firebase project and is on no list (roles.md §1.1), so an
agent *inside a run* cannot grant anything. **(The caller's identity is F21: it is NOT the Google
identity today.)**

**F18 — Resolved: where the code lives.** Enforcement of the three roles stays in MIT core (T16, D637);
the role model's UX and the lapse input live in `ee/` through a `Set*` seam beside the governance ones
(roles.md D694 recommended). `internal/access` is core and contains no licence code.

**F19 — Resolved: the project-level action list.** A project-scope role is required for: the project
layer and its guardrails, `allow_repos` (`project repos add/rm`), the image catalog, accounts, budget
`--global`/`--defaults`, the grants of `project` or `all-repos` scope, `setup`/`update`/`config publish`
of the project layer. An all-repos admin is a repo admin of every enrolled repo and holds none of
these. Viewer and operator differ in effect between `project` and `all-repos` only for **project-level
reads** (spend, members): project scope reads them, all-repos reads those of its repos only where the
data is per repo (see D1586).

**F20 — Wrong: "no follow-up steps".** A role change reaches the cloud through the IAM lists on the
**next converge**. *Consequence:* it is a composite command (`docs/composite-commands`, D1561's named
steps): the command writes the node, then the registry runs the IAM-list step, and the user is told
"saved, not live yet" only on a failed step (that sibling's D1563). This plan has **no step of its own**
and declares only what each change affects.

**F21 — Wrong, round 1: the caller is not the authenticated identity today.** `meMember` (`internal/cli/roles.go`)
and `pairMeLookup` (`internal/cli/pair.go`) read `localcfg.Config.Me`, which is the config's `user:` key,
**else `git config user.email`** (`internal/localcfg/localcfg.go`, `Me`). Both are text a person or an
agent can set. A role check built on it is a courtesy. *Consequence:* RS1 defines `access.Caller` as read
from the **credential** the command will use (the token's email claim from the Google application-default
or gcloud credential), never from config or git; RS5 and RS8 take it from there; pinned by
`TestCallerIsTheCredentialNotGitConfig`. The existing `meMember` keeps its table-default use and is never
fed to `access`.

**F22 — Missing, BLOCKING, round 1: what each role and scope holds in the cloud, and who can write the
grants.** Grants must live in the Realtime Database under `config/…`; `internal/budget/rules/rules.json.tmpl`
gives `config/policy/*` and `config/admin/*` **`.write: false`**, so the only writers are IAM holders of `roles/firebasedatabase.admin`: the project's owners and editors and `budget_admins` (roles.md §1.2), **and the history job's account and the coordinator's writer account** (`deploy/terraform/gcp/modules/firebase/iam.tf`, `history_database` and the coordinator member). The migrated **admins (today's `operators`) do not hold it.** Table (today, and after RS3/RS4):

| Role and scope | Cloud grants it carries | Can it write the `access` node or any `config/policy/*` setting? |
| --- | --- | --- |
| viewer, project | the RS4 viewer list: RTDB viewer, spend viewer, read-only job role, log view, runs-bucket read | no |
| operator, project | the launcher grants: `job_runner` on the jobs, `fugaroLauncher`, logs, runs bucket `runs/` and `tokens/` write | no |
| admin, project | the `operators` grants: state bucket, build submitter, base-image writer, runs bucket whole | **no** — not by these grants |
| owners, editors, `budget_admins` | `roles/owner`/`editor`, `firebasedatabase.admin` | **yes** (implicit project admins, F17.3) |
| **any role at all-repos scope** | **none of its own** | no |
| **any role at specific-repos scope** | **none of its own** | no |

**Round 2 consequence: an explicit `admin@project` cannot grant anything.** After migration only the implicit admins (owners, editors, `budget_admins`) can write `config/admin/access`: RS3/RS4 do not derive `firebasedatabase.admin` for the new admin role. RS5 therefore refuses for an explicit admin with a sentence naming `budget_admins` ("add this person to `budget_admins`, or ask a project owner"). **D1592** asks whether `admin@project` should derive it. Implicit admins **do count** toward the last-admin rule (F17.2): the rule refuses only when no project admin of either kind would remain.

Three consequences. (a) **Repo-scoped and all-repos pairs carry no cloud grant before RS12**: they are a
CLI/console policy check over grants the person already holds project-wide, or none. (b) **Anyone who is
given the write the `access` node (or a repo's settings under `config/policy/repos/$slug`) needs is
effectively a project admin**: `firebasedatabase.admin` writes everything under `config/`, including
the `access` node itself, so a repo admin holding it could promote themselves to project admin. (c) The
spec's acceptance criterion 2, the **team-lead example** and **Check 402** (a repo admin changes their
repo's settings and images) therefore **cannot hold as stated** until a write path that checks the node
exists. This was hidden by D1585's earlier text deriving cloud grants from project-scope pairs only; it is
now D1591 (below) and RS5 depends on it.

**F23 — Missing, round 1: check-then-write races.** `fugaro user grant`/`revoke` judge the caller's
authority and the last-admin rule against a read, then write. Two concurrent writers could each pass the
rule and together remove the last admin or both widen. `rtdb.Client.GetETag` and `PutIfMatch`
(`internal/rtdb/client.go`) exist. *Consequence:* RS5 writes **conditionally on the ETag of the read it
judged** and retries from the read on a mismatch, with a concurrency test (two writers, one wins, the
other re-judges). The same test covers **self-demotion**: `grant --role operator` for oneself is a
demotion and must obey the last-admin rule like `revoke`.

**F24 — Missing, round 1: who may revoke or demote.** The spec says who may grant. *Consequence:* a
revoke or demote of a pair is allowed only to a caller whose own role covers that pair's scope at admin;
**a repo admin can never revoke or demote a project-scope or all-repos pair**, nor a pair whose scope is
not a subset of their own (`TestRepoAdminCannotRevokeProjectGrant`).

**F25 — Gap, round 1: the member forms of the old lists.** `localcfg` validates list members as `user:`,
`group:`, `serviceAccount:` (and `domain:` for `launchers`/`operators`, `internal/localcfg/localcfg.go`
validation); `fugaro user` writes only `user:`. A group or domain cannot be matched against a caller by
the CLI. *Consequence (resolved):* `access` grants are **`user:` only**. A `group:`/`domain:`/
`serviceAccount:` member already on a list at migration stays as a **legacy IAM-only member** at project
scope: it keeps the cloud grant (RS3 keeps deriving it) and is listed by `ls` as legacy, but the CLI
resolver does not rank it (it cannot know a caller's groups), so a command check treats such a caller by
their own `user:` grant or as nobody. The migration test covers each form.

**F26 — Rule, round 1: this design reverses answered decisions of `docs/design/roles.md`.** D1580
reverses **D690(a)** ("the CLI does not rank its own commands") and **D691(a)** (the console does not
know a role) and narrows **D446** ("enforced system-wide, the cloud included"); roles.md on main still
shows D681–D695 with status `open` although the owner answered them on 2026-10-30. *Consequence:*
RS10 records D681–D695 as answered and marks D690 and D691 **superseded by the owner's 2026-11-07 D1580
answer**; the narrowing of D446 is stated beside it.

**F29 — Missing, round 2: the IAM derive is not ETag-protected; the contractor example.**
(a) Only the node write is conditional (F23). The IAM lists are derived from a read and applied by `update` under Terraform's state lock; a concurrent grant or a revoke racing an `update` can apply lists from a **stale read and re-grant a revoked member IAM**. *Consequence:* RS3 and RS5 derive **from a read taken after the state lock is held**, re-reading the node; `TestRevokeIsNotUndoneByAStaleDerive`.
(b) **The contractor example** (operator on one repo only): IAM is derived from **project-scope pairs only**, so such a person gets **no cloud grant before RS12 and cannot launch**. The example waits for RS12 (D1593). Check 402's "refused on another repo" proves nothing for a person who holds no IAM: the check subject must hold the project-wide launcher grant (operator@project) plus a scoped pair, and the script header states that IAM.

**F28 — Wrong, round 1: the first D1585 put the grants where every run reads them.** `Policy.Access` in
`projectpolicy.Policy` would sit in `config/policy/shared`, whose rule is `.read: TOKEN` (`rules.json.tmpl`):
each run's agent could read every member's address and role. *Consequence:* D1585 amended (above); RS2
stores the node under `config/admin/` and pins it with a rules test.

**F27 — Resolved, round 1: smaller design points.**
- **Implicit admins** (owners, editors, `budget_admins`) are an **input** to the resolver
  (`access.Input.Implicit`, read from the installation's outputs), not a stored pair.
- **"The member exists":** Fugaro has no directory. The check is that the member is a well-formed `user:`
  address; GCP refuses an IAM member it cannot resolve when the derived list is applied, and that failure
  is the composite command's partial-failure case. UNVERIFIED which GCP error an unknown address gives.
- **`ls` "the repos a pattern matches now":** when `allow_repos` holds patterns the set of repos cannot be
  enumerated. `ls` prints matches among the **repositories this machine's config lists and runs it has
  seen**, labelled "known", and never claims completeness.
- **A scope never admits a repo `allow_repos` does not** (a repo outside it cannot run at all).
- **A renamed or recreated repo inherits a pattern grant** (matching is by name at check time); the docs
  state it as a limit, and a rename is a new name.
- **Size:** the node has no `MaxPolicyBytes` bound if it is not in the policy (F22/D1585 amended), so RS2
  defines its own cap (a count and a byte bound) and refuses a write over it.
- **An older CLI:** once the node is written, an older CLI's `fugaro update` could ignore or drop it.
  RS3 adds a floor so an older CLI refuses naming `brew upgrade` and `fugaro update`; UNVERIFIED how an
  older CLI treats an unknown `config/admin` member today (RS3's first step reads it).
- **The `fugaro user ls --json` document** must not reuse the key `operators` or `launchers` with the new
  meaning (CLAUDE.md "a field never changes meaning"): it gets new names (`grants`, `effective`) and the
  old keys are removed with a version bump.
- **The subset algorithm** is written down in RS1 (below) with hostile cases.

## 3. Decisions

| # | Question | Recommendation and why | Alternative and its cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D1580** | Where is per-repo scope enforced? | **CLI and console checks first; cloud IAM stays project-wide.** Smallest correct slice; the cloud has no per-repo grant (F4). Documented as a policy check, not a cloud barrier | Cloud IAM from the start: a real barrier, a large security-sensitive Terraform and cover-table change. Project-scope only: the contractor example does not work | **2026-11-07: as recommended.** Why: ship the check now; per-repo cloud enforcement is RS12, a later slice |
| **D1581** | How do `launchers`/`operators` become the spec's words? | **Rename to the spec's words (launcher→operator, operator→admin at project scope, new viewer) and migrate inside `fugaro update`** with a converge test (CLAUDE.md "update is the whole upgrade"); flags, skills and docs renamed, no aliases (pre-1.0) | Keep the old words inside, show the new: two vocabularies for one thing | **2026-11-07: as recommended.** Why: roles.md §3 named the permanent cost of two words |
| **D1582** | A lapse: everyone to viewer, or only the Subscription surfaces? | **Only Subscription-edition surfaces drop to viewer;** the MIT core CLI keeps working; assignments kept and returned on resume. A licence-gate input, not a role rewrite (F12) | All surfaces to viewer, as the spec says: stops the free core | **2026-11-07: as recommended; differs from the spec's acceptance sentence** (amended in F12). Why: the MIT core promise |
| **D1583** | Scope grammar | **`project`, `all-repos`, or a comma-separated list of repo patterns; `*` stays inside one path segment; no `**`; patterns re-evaluated at check time; bare names refused with a hint** | Accept bare names when unique: ambiguous across owners and changes meaning when a second owner registers a same-named repo | **2026-11-07: as recommended in its text; AMENDED AFTER REVIEW below, and the amendment CONFIRMED by the owner 2026-11-07** |
| **D1584** | May a catalog entry carry a repo binding so a repo admin manages it? | **No for now: every catalog entry is project-level, project-scope admin only** (F8). A `repos:` binding is a later change to `config.Catalog`, its schema and the published node | Add the binding now: a catalog shape change and the cover/policy rows in the same release | **recommended; owner delegated reading to the PR, 2026-11-07** |
| **D1585** | Where are the role/scope pairs stored? **(amended after review, round 1)** | **A new `access` node in the Realtime Database under `config/admin/` (`.read: false`, `.write: false` in `rules.json.tmpl`), NOT in `config/policy/shared`**, which carries `.read: TOKEN` so every run's agent would read every member's address and role (F28). Read and written over IAM by CLI callers holding the database grant; derived IAM lists from the project-scope pairs by the step registry. **The fence binds client tokens only:** `.read: false` does not stop an IAM `roles/firebasedatabase.viewer` holder, which every launcher and operator holds today and RS4 gives the viewer list (`rules.json.tmpl`, roles.md §1.2), so **every listed person, and an agent in their session, can read every grant**; a repo-scoped-only user holds no such role and cannot read the node at all (F22). The node has its own size and count cap (F27) and an RS2 **rules test that no run token reads it**. Local `terraform.launchers/operators` become derived, then removed | The published policy node (first recommendation): shared, but run-readable | **2026-11-07: amended after review; amendment CONFIRMED by the owner 2026-11-07** (node under `config/admin/`) |
| **D1586** | Does a viewer read spend, and per scope? | **Yes at project scope; a repo-scoped viewer reads only the runs and logs of those repos, and project-level spend is not shown to them.** Spend history is one Firestore collection per project (roles.md `datastore.viewer`), so a per-repo spend view is a CLI-side filter, not a barrier | Spend for everyone who has any role: simplest, exposes other repos' cost to a contractor | **recommended; owner delegated reading to the PR, 2026-11-07** |
| **D1587** | May an operator cancel a run another person started? **(lapse half dropped in round 1: already ruled by D436/T12, F11)** | **Yes, within scope** (today a launcher cancels any run, `fugaroLauncher`). An ordinary viewer cannot cancel; a lapsed viewer keeps cancel and the kill switch as T12 ruled | No cancel for any operator but the starter's own: needs a run-owner check the cloud cannot make (`fugaroLauncher` bundles cancel on every run) | **recommended; owner delegated reading to the PR, 2026-11-07** |
| **D1588** | A paired device and scopes | **A device inherits the highest PROJECT-scope role of the pairing user and may only lower it; a user with only repo-scoped roles cannot pair** (a device node has no scope field). Scoped devices need a node-shape change later | Add a `scope` child to the device node now: a rules and schema change and a version bump | **recommended; owner delegated reading to the PR, 2026-11-07** |
| **D1589** | Do the Terraform variable names (`launchers`, `operators`) change with the config words? | **No: config keys, flags and docs are renamed; the Terraform variable and resource names stay.** The plan is then empty for an installation that only renames, and no state move is needed — **with one carve-out, the compute root**: its `var.launchers` is handed the derived launcher list whole, the admins among it, because an admin operates too and the installation and repository roots already take that union (`internal/cli/init_compute.go`; `launchersWithOperators`, `internal/infra/tfvars.go`), so a Batch-provisioned installation that only renames plans new `roles/batch.jobsEditor` grants for its admins on its next `--provision-batch` (that stage is opt-in, prints its plan, and asks for the project's name typed at a real terminal) | Rename the variables: state moves and a noisy plan for no user-visible gain | **recommended; owner delegated reading to the PR, 2026-11-07** |
| **D1590** | May a coding-agent session grant or revoke a role? | **No: a grant or revoke in an agent session refuses (not a prompt) and prints the exact command for the person** — the same shape as the sibling's cap-raise rule (no-prompt D1542). Every other command stays open to the agent *within the person's own role*. **Stated honestly (round 1): agent sessions are detected by environment variables (`internal/agentenv`) that an agent can unset; this is a courtesy against accidents, not a barrier**, exactly as `agentenv.go` says of itself, and RS10's doc test pins that sentence | Yes: an agent in an admin's session can promote itself or anyone, defeating the role | **recommended; owner delegated reading to the PR, 2026-11-07** |
| **D1591** | How does a repo-scoped admin's write reach the cloud (F22)? Before RS12 a repo-scoped admin has no cloud grant, and anyone given the database write is effectively a project admin | **(c) Until RS12 a repo-scoped admin changes only what a person can change without a cloud write: their repo's `fugaro.yaml` through the repository's own review, and CLI-local actions; scoped operator and viewer roles are CLI/console checks over grants the person already holds. The `access` node and every `config/` write need a project-scope admin (in practice an implicit admin: an explicit `admin@project` holds no database write, D1592).** Honest and ships now | (a) A write path that checks the node before writing (a small service the repo admin may call, a new trust boundary: RS12). (b) Give repo admins `firebasedatabase.admin`: they become project admins in practice and can promote themselves | **2026-11-07: as recommended (option c).** Why: ships project-scope writes now and leaves repo-scoped admin writes to RS12 |
| **D1592** | Should `admin@project` derive `roles/firebasedatabase.admin` so an explicit admin can grant? **(round 2)** | **No.** It widens an admin to lift caps and write all of `/config` (roles.md §1.2); grants stay with the implicit admins (owners, editors, `budget_admins`) | Yes: admins can grant, and are also budget admins | **recommended; owner delegated reading to the PR, 2026-11-07** |
| **D1593** | Derive launcher IAM from a scoped operator pair so the contractor example works before RS12? **(round 2)** | **No: wait for RS12.** A project-wide IAM grant would let the contractor launch every repo, defeating the scope | Derive it: the contractor works now, with project-wide launch power and a CLI check as the only limit | **recommended; owner delegated reading to the PR, 2026-11-07** |

### D1583 — amended after review (rewritten in round 1)

The owner chose "owner/name or owner/pattern only; `*` stays inside one segment; bare names refused".
Code facts (F5): the one spelling of a repository is **`<provider>/<owner>/<name>`**;
`projectpolicy.MatchRepo` refuses a bare `owner/*` because it names no provider; and
`canonicalRepoArg` (`internal/cli/project_enroll.go`) resolves a two-segment `owner/name` **only when
that exact repository is listed in this machine's config** (`configRepo`), refusing anything else with
"name it canonically". A Fugaro project has **no single provider**, so an `owner/pattern` shorthand has
no provider to take. **What the scope grammar is:** the **stored and printed spelling is canonical**
(`github/acme/api-*`, `bitbucket/acme/*`); the shorthand `owner/name` is accepted **only for an exact
repository this machine's config lists** (as `repos add` does); a **pattern is always written canonical**;
a **`*` in the provider segment is refused** (so the `*/acme/*` form `MatchRepo` allows is not a scope value), and **a lone `*` is refused as a scope value too: write `all-repos`**, which is reserved to project admins, so an all-repos admin cannot mint it by writing `*`. Bare names
(`frontend`) are refused with a hint; `*` stays inside one segment; no `**`; patterns re-evaluated at
check time. The owner **confirmed this amended text on 2026-11-07**.

## 4. Open tensions and open items for the owner

1. **Settled 2026-11-07:** D1583 as amended and D1585 as amended are confirmed by the owner.
2. **Tension — the spec's scoped examples against the cloud (F22, D1591).** Acceptance criterion 2, the
   team-lead example and Check 402 need a repo admin to write repo settings and images; the database only
   admits `firebasedatabase.admin` holders, who are project admins in practice. The owner answered D1591 as recommended (option c) on 2026-11-07: project-scope admin writes ship now and repo-scoped admin
   writes wait for RS12. **Acceptance criterion 2 is therefore met for the CLI's local and PR-based actions
   only, until RS12.**
3. **The role is a policy check, not a barrier (D1580)** for launch and read: a person with a
   project-wide IAM grant can act on any repo with `gcloud`. The docs say so wherever a scoped role is
   described (RS10 doc test). D1591 (c) settles it: repo-scoped operator and viewer are stated courtesies until RS12.
4. **No decision is open:** D1584, D1586–D1590, D1592 and D1593 are recorded as the recommendation ("owner delegated reading to the PR"), D1591 and the two amendments as answered.
5. **Sibling dependencies.** Role changes use `docs/composite-commands`' step registry (D1561) and its
   partial-failure message (D1563); refusals use `docs/no-prompt-policy`. Neither is merged: RS5 and RS6
   wait for them.

## 5. Spec text this reconciliation overrides

| Spec text | Replaced by |
| --- | --- |
| "an image tied to a repo is managed by an admin of that repo" | F8, D1584: catalog entries are project-level; a workflow's derived image rebuild is repo-scoped until the removal chain lands |
| "refuses to create or register a repo with either name" | F6: a test, no new refusal |
| scope values "a comma-separated list of repo names, for example `frontend,api`" | D1583 amended: canonical patterns; bare names refused |
| "When a subscription lapses, all users are limited to viewer" | F12, D1582 (restates D436/T12): Subscription surfaces only, cancel and the kill switch kept |
| "Contractor: operator on one specific repo only" | F29(b), D1593: no cloud grant before RS12, so the example waits for RS12 |
| acceptance criterion 2 (a repo admin changes repo A's settings and images) | F22, D1591: only as far as a person needs no cloud write, until RS12 |
| "Only a project admin can grant ... A repo admin can grant roles up to admin" | F17.1 subset rule, F24 revoke rule, F23 conditional write |
| "no follow-up steps" | F20: composite step registry |

## 6. UNVERIFIED

- **How a CLI command obtains the credential's email claim** (F21). `access.Caller` is designed but the
  exact reader (ADC token claim versus `gcloud auth list`) is RS1's first step; `localcfg.Me` is known to
  be forgeable (read and cited).
- **Which settings keys are "repo overrides".** `settingsManagedKeys` holds two scalar keys; per-repo
  budget caps exist (`budget set --repo`), per-repo `fugaro.yaml` lives in the customer's repository and
  is reviewed there. RS6b assigns each writable key a required role; the list is to be confirmed
  against `internal/settings`.

- **Console surfaces.** `consoleReadPaths` lives in `internal/cli/project.go` (round 1 corrected the file);
  its console-side half and the Go equality test were not re-read in full: RS7 states the test the rule
  requires.
- **Cloud mechanics of RS12** (per-job `run.invoker`, bucket prefix conditions and `objects.list`) are
  roles.md's reading (§4) and were not re-verified.
- **Round 2 has not run.** Round 1's findings were each re-verified on `origin/main` (§7); no fresh
  reviewer has attacked the amended text (the `config/admin/` node, the conditional write, the subset
  algorithm).

## 7. Round 1 (independent security review, 2026-11-07): accepted and dismissed

Each finding was re-verified on `origin/main` at `1ad2d3143` before acting.

| # | Finding | Verified | Action |
| --- | --- | --- | --- |
| 1 | No table of cloud grants per role and scope; the access node's writer | `rules.json.tmpl` gives `config/policy/*` `.write: false`; roles.md §1.2: only `firebasedatabase.admin` writes | **Accepted:** F22 table, D1591, RS5 depends on it, criterion 2 and Check 402 re-scoped |
| 2 | Caller is not the authenticated identity | `localcfg.Config.Me`: config `user:` else `git config user.email`; `meMember`, `pairMeLookup` read it | **Accepted:** F21, RS1/RS5/RS8, `TestCallerIsTheCredentialNotGitConfig` |
| 3 | `Policy.Access` readable by every run | `config/policy/shared` `.read: TOKEN` | **Accepted:** F28, D1585 amended, RS2 rules test |
| 4 | Check-then-write races; self-demotion | `rtdb.Client.GetETag` / `PutIfMatch` exist | **Accepted:** F23, RS5 |
| 5 | Who may revoke | spec silent | **Accepted:** F24, `TestRepoAdminCannotRevokeProjectGrant` |
| 6 | D1583 amendment wrong (`repos add` shorthand) | `canonicalRepoArg` resolves a 2-segment arg only through `configRepo`; refuses otherwise | **Accepted:** D1583 rewritten; `*/acme/*` example dropped |
| 7 | F8 wrong: derived image still exists | `image build --workflow` in `internal/cli/image.go`; `image_refresh_steps.go` exists | **Accepted with a correction:** the reviewer also named an `image dockerfile` command; there is none (`--dockerfile` is a flag of the catalog add, `image_catalog.go`), so F8 names the two that exist |
| 8 | Stale lapse tension | Terms §9.1 and D436/T12 read in `docs/licensing`, `docs/subscriptions/reconciliation.md` F15 | **Accepted:** F11/F12 rewritten, D1587's lapse half and the tension dropped |
| 9 | roles.md consistency | roles.md §9 shows D681–D695 `open` | **Accepted:** F26, RS10 records answers and supersessions |
| 10 | D1590 overstated | `internal/agentenv/agentenv.go` says it is a mitigation, not a barrier | **Accepted:** D1590 states it, RS10 pins it |
| lesser | RS4→RS5 edge; group/domain/serviceAccount members; implicit admins input; member-exists source; `ls` enumeration; `consoleReadPaths` file; `ls --json` keys; main's commit; D1586's filter in `budget.go`; subset algorithm; rename/recreate; scope ⊆ `allow_repos`; node size; older CLI; package docs test | each checked (F25, F27, `internal/cli/project.go`) | **Accepted** in F25, F27, the plan's edges, RS1, RS2, RS3, RS6c, RS7 and RS10 |

Dismissed: none in full. One partial dismissal: the non-existent `image dockerfile` command (row 7).

## 8. Round 2 (independent review, 2026-11-07): accepted and dismissed

| # | Finding | Verified on main | Action |
| --- | --- | --- | --- |
| 1 | An explicit `admin@project` cannot grant | `modules/firebase/iam.tf`: `firebasedatabase.admin` only on the listed accounts | **Accepted:** F22, D1591, RS5 (refusal names `budget_admins`), D1592 (recommended NO) |
| 2 | F22 writers incomplete | `iam.tf` `history_database`, the coordinator member | **Accepted:** both added |
| 3 | `.read: false` binds tokens only | viewer role bypasses rules (`rules.json.tmpl`, roles.md §1.2) | **Accepted:** D1585 and RS2 |
| 4 | Contractor example, Check 402 subject | IAM derives from project-scope pairs only | **Accepted:** F29(b), §5 row, D1593, Check 402 states the IAM |
| 5 | Derive race | only the node write was conditional | **Accepted:** F29(a), RS3/RS5, `TestRevokeIsNotUndoneByAStaleDerive` |
| 6 | Lone `*` | plan pinned refusal while D1583 allowed it | **Accepted:** `*` refused as a scope value, D1583 reworded, RS1 |
| follow-ups | RS7 test names; `status*.go`; F6 UNVERIFIED; RS3 drift and retire order; implicit admins in the last-admin rule; grant prints canonical scope; RS10 depends on RS7; cited commit; earlier docs test | `TestConsoleAllowListedReadsWriteNothing` (`checkouts_test.go`) and `TestDocsDesktopConsoleDesignAllowListIsReadOnlyInTheCode` exist; no `internal/cli/status*.go`; `canonicalRepoArg` refuses `len(parts) < 3` | **Accepted**; the docs test starts in RS1 |

Dismissed: none.
