# User roles and scopes: reconciliation

**Status: design answered in part by the owner on 2026-11-07; plan only; nothing is implemented; no task
approved.** This file amends [spec.md](spec.md) against the code. **Where it disagrees with the spec, it
wins.** Checked against `origin/main` at `0fdc0909b` on 2026-11-07. **Independent review rounds have NOT
been run on this package** (see §6).

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
and so cannot be given a bare word (its exact refusal is UNVERIFIED, §6). *Consequence:* the reservation is a **test** that the two keywords can
never parse as a repository (RS1), not a refusal in `repos add`.

**F7 — Wrong: "all repos, including repos added later" needs a definition.** The repo set is
`allow_repos`; a repo not enrolled there cannot run at all. *Consequence:* `all-repos` means every
repository the policy's `allow_repos` admits **at the moment of the check**; a newly enrolled repo is
covered with no change to any grant, and a wildcard pattern is re-evaluated the same way (spec's own rule).

**F8 — Wrong: "an image tied to a repo is managed by an admin of that repo".** There is no such image
(§1 table). *Consequence:* today every catalog entry is a **project-level resource**, managed only
by a project-scope admin; D1584 asks whether a catalog entry may later carry a repo binding.

**F9 — Exists: "repo admins change settings only within the project guardrails".** The project layer and
`internal/policy` merge already only tighten. *Consequence:* a repo-scoped admin's writes go through the
same merge; no new guardrail machinery. What is new is only **who may call** the write (RS6b).

**F10 — Gap: where the grants live.** `fugaro user` edits a machine-local list and applies at the next
`update`, and refuses when two machines drift (`user.go` "add and rm read the installation's own outputs
first"). A list of scoped pairs that "takes effect right away" and that every machine and the console
read cannot be a per-machine file. *Consequence:* D1585 (stored in the published project policy, an
`access` node; the IAM lists become **derived** from the project-scope pairs by the composite-commands
step registry).

**F11 — Rule: licensing keeps cancel and the kill switch in read-only use.** `docs/subscriptions`
reconciliation F15/T12: after a lapse a User "cannot launch, cancel or change anything" in the Terms
text, while T12 ruled that read-only keeps cancel and the kill switch. The spec says a viewer "can't
launch jobs or change anything" and that a lapse drops users to viewer. *Consequence:* D1587.

**F12 — Rule, amends the spec: a lapse is not a role rewrite (D1582).** The spec's last acceptance
criterion ("all users are limited to viewer") would also stop the **MIT core CLI**, which the licensing
design keeps free. The owner's 2026-11-07 answer: only Subscription-edition surfaces (console, mobile
and subscription features) drop to viewer; assignments are untouched and return on resume. A lapse is
therefore a **licence-gate input** to the resolver, supplied through `ee/`'s seam (roles.md D694), and
**does not differ from the spec's intent for those surfaces**. *Consequence:* the spec's acceptance
sentence is replaced by "when a subscription lapses, the Subscription surfaces limit every user to
viewer; no assignment is changed or lost; the core CLI is unaffected".

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
`.git` variant is normalised by `Canonical` before it is stored. (5) The caller is the **authenticated
Google identity**; a run's job account holds no role on the Firebase project and is on no list
(roles.md §1.1), so an agent *inside a run* cannot grant anything.

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

## 3. Decisions

| # | Question | Recommendation and why | Alternative and its cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D1580** | Where is per-repo scope enforced? | **CLI and console checks first; cloud IAM stays project-wide.** Smallest correct slice; the cloud has no per-repo grant (F4). Documented as a policy check, not a cloud barrier | Cloud IAM from the start: a real barrier, a large security-sensitive Terraform and cover-table change. Project-scope only: the contractor example does not work | **2026-11-07: as recommended.** Why: ship the check now; per-repo cloud enforcement is RS12, a later slice |
| **D1581** | How do `launchers`/`operators` become the spec's words? | **Rename to the spec's words (launcher→operator, operator→admin at project scope, new viewer) and migrate inside `fugaro update`** with a converge test (CLAUDE.md "update is the whole upgrade"); flags, skills and docs renamed, no aliases (pre-1.0) | Keep the old words inside, show the new: two vocabularies for one thing | **2026-11-07: as recommended.** Why: roles.md §3 named the permanent cost of two words |
| **D1582** | A lapse: everyone to viewer, or only the Subscription surfaces? | **Only Subscription-edition surfaces drop to viewer;** the MIT core CLI keeps working; assignments kept and returned on resume. A licence-gate input, not a role rewrite (F12) | All surfaces to viewer, as the spec says: stops the free core | **2026-11-07: as recommended; differs from the spec's acceptance sentence** (amended in F12). Why: the MIT core promise |
| **D1583** | Scope grammar | **`project`, `all-repos`, or a comma-separated list of repo patterns; `*` stays inside one path segment; no `**`; patterns re-evaluated at check time; bare names refused with a hint** | Accept bare names when unique: ambiguous across owners and changes meaning when a second owner registers a same-named repo | **2026-11-07: as recommended in its text "owner/name or owner/pattern only"; AMENDED AFTER REVIEW below** |
| **D1584** | May a catalog entry carry a repo binding so a repo admin manages it? | **No for now: every catalog entry is project-level, project-scope admin only** (F8). A `repos:` binding is a later change to `config.Catalog`, its schema and the published node | Add the binding now: a catalog shape change and the cover/policy rows in the same release | *(empty)* |
| **D1585** | Where are the role/scope pairs stored? | **A new `access` node in the published project policy** (the one place every machine and the console read), with the IAM lists derived from the project-scope pairs by the step registry. Local `terraform.launchers/operators` become derived, then removed | Keep the per-machine config lists and add a scope column: drift between machines (the reason `user rm` refuses today) and the console cannot read them | *(empty)* |
| **D1586** | Does a viewer read spend, and per scope? | **Yes at project scope; a repo-scoped viewer reads only the runs and logs of those repos, and project-level spend is not shown to them.** Spend history is one Firestore collection per project (roles.md `datastore.viewer`), so a per-repo spend view is a CLI-side filter, not a barrier | Spend for everyone who has any role: simplest, exposes other repos' cost to a contractor | *(empty)* |
| **D1587** | May an operator cancel a run another person started, and does a lapsed viewer keep cancel and the kill switch? | **Operator: yes, within scope** (today a launcher cancels any run, `fugaroLauncher`). **A lapse keeps cancel and the kill switch, as T12 ruled**; an ordinary viewer cannot cancel. The spec's "can't change anything" is amended for the lapse case | No cancel for a lapsed viewer: contradicts T12 and the Terms draft's read-only use | *(empty)* |
| **D1588** | A paired device and scopes | **A device inherits the highest PROJECT-scope role of the pairing user and may only lower it; a user with only repo-scoped roles cannot pair** (a device node has no scope field). Scoped devices need a node-shape change later | Add a `scope` child to the device node now: a rules and schema change and a version bump | *(empty)* |
| **D1589** | Do the Terraform variable names (`launchers`, `operators`) change with the config words? | **No: config keys, flags and docs are renamed; the Terraform variable and resource names stay.** The plan is then empty for an installation that only renames, and no state move is needed | Rename the variables: state moves and a noisy plan for no user-visible gain | *(empty)* |
| **D1590** | May a coding-agent session grant or revoke a role? | **No: a grant or revoke in an agent session refuses (not a prompt) and prints the exact command for the person** — the same shape as the sibling's cap-raise rule (no-prompt D1542). Every other command stays open to the agent *within the person's own role* | Yes: an agent in an admin's session can promote itself or anyone, defeating the role | *(empty)* |

### D1583 — amended after review

The owner chose "owner/name or owner/pattern only; `*` stays inside one segment; bare names refused".
The code fact that contradicts the premise (F5): the one spelling of a repository is
**`<provider>/<owner>/<name>`**, and `projectpolicy.MatchRepo` **refuses a bare `owner/*`** because it
names no provider. Keeping the owner's text literally would add a second spelling beside the allow-list's.
**What changes:** a scope value's repo pattern is a **canonical pattern** (`github/acme/api-*`,
`bitbucket/acme/*`, `*/acme/*`). The command accepts the shorthand `owner/name` or `owner/api-*` and
**takes the provider from this machine's project config, exactly as `fugaro project repos add` does**,
then stores the canonical form; the listing prints the canonical form. **What does not change:** `*`
stays inside one segment, no `**`, patterns re-evaluated at check time, bare names (`frontend`) refused
with a hint, `project`/`all-repos` keywords. **The owner is asked to confirm this amended text** (open
item 1).

## 4. Open tensions and open items for the owner

1. **Confirm D1583 as amended** (canonical `provider/owner/name`, shorthand resolved with the machine's
   provider).
2. **Tension — D1582 against the spec's lapse sentence and against the Terms text.** The spec limits all
   users to viewer; the owner's answer limits only Subscription surfaces; and the Terms draft says a
   User "cannot launch, cancel or change anything" while T12 keeps cancel and the kill switch (F11).
   D1587 asks the owner to rule on the lapse rights; counsel's text (`docs/licensing`) is not this
   plan's to change.
3. **Tension — a role is a policy check, not a barrier (D1580).** A person with a project-wide IAM grant
   can launch a repo they are not scoped to with `gcloud`. The docs must say so wherever a scoped role
   is described; a contractor scoped to one repo must not be given the project-wide IAM list. Until
   RS12 the safe way to hold a contractor is **no** IAM grant for the lists and a launch through a
   coordinator that does not exist yet. The owner should rule whether **repo-scoped grants ship before
   RS12** (a courtesy with a stated limit) or wait for it.
4. **Open decisions D1584–D1590** above (empty answer cells).
5. **Sibling dependencies.** Role changes use `docs/composite-commands`' step registry (D1561) and its
   partial-failure message (D1563); refusals use `docs/no-prompt-policy`. Neither is merged: RS5 and RS6
   wait for them.

## 5. Spec text this reconciliation overrides

| Spec text | Replaced by |
| --- | --- |
| "an image tied to a repo is managed by an admin of that repo" | F8, D1584: catalog entries are project-level |
| "refuses to create or register a repo with either name" | F6: a test, no new refusal |
| scope values "a comma-separated list of repo names, for example `frontend,api`" | D1583 amended: canonical patterns; bare names refused |
| "When a subscription lapses, all users are limited to viewer" | F12, D1582, D1587 |
| "no follow-up steps" | F20: composite step registry |

## 6. UNVERIFIED

- **`meMember`'s identity source.** `internal/cli/roles.go` names the function; I did not read where it
  gets the caller's address. The design assumes the authenticated Google identity (F17.5).
- **Which settings keys are "repo overrides".** `settingsManagedKeys` holds two scalar keys; per-repo
  budget caps exist (`budget set --repo`), per-repo `fugaro.yaml` lives in the customer's repository and
  is reviewed there. RS6b assigns each writable key a required role; the list is to be confirmed
  against `internal/settings`.
- **Whether `fugaro project repos add` already refuses `project`/`all-repos`.** F6 reasons from the
  canonical spelling; the refusal's exact message was not read.
- **Console surfaces.** The exact-argv read table (`consoleReadPaths`, `console/src/main/spawn.ts`) was
  not opened on this pass; RS7 states the equality test the rule requires.
- **Cloud mechanics of RS12** (per-job `run.invoker`, bucket prefix conditions and `objects.list`) are
  roles.md's reading (§4) and were not re-verified.
- **No independent review round has run.** Every citation was grepped on `origin/main`; no fresh reviewer
  has attacked the trust boundaries (a scoped admin widening their own scope, the derived IAM lists, the
  last-admin lockout).
