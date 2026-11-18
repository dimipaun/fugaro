# Workspace projects and users: reconciliation

The amended spec. [spec.md](spec.md) is the owner's addendum as received; **where this file disagrees
with it, this file wins**. Checked against `origin/main` at `76ce3d6b9` on 2026-11-18. Citations are
paths and symbols, never line numbers. Decisions take the block **D2800–D2829** (D2800–D2819 used,
D2820–D2829 reserved); checks take **Checks 720–729**.

The addendum extends the merged workspaces design ([../workspaces/](../workspaces/README.md), W0–W9,
listed as done in [../status.md](../status.md)) and the merged roles design
([../roles-scopes/](../roles-scopes/README.md), RS1–RS10). It was written without the repository in
view, so much of what it proposes exists already under other names (§2), and its central move — the
workspace holds IAM authority over its members — reverses a rule the merged design states as a
prohibition (F1, D2800).

## 1. Assumptions

- **A1.** "Workspace" is the merged design's workspace: a project of its own, named after the
  workspace, with its own Firebase project (D1290), grouping member projects one workspace each
  (D1291). The addendum adds to it and does not start a second kind of workspace.
- **A2.** "Fugaro project" is an installation: one GCP project with its runs bucket, jobs and roster
  (`docs/design/v1.md`, the vocabulary), not a repository.
- **A3.** "admin, operator, viewer" are the three roles of `internal/access` (`access.Roles`) with
  their scopes, not three new roles.
- **A4.** "Users" are people with a Google account and the service accounts of D2806; never the
  end users of a customer's product.
- **A5.** "The doctor" for a project is a read-only command in the sense of `fugaro doctor`'s own
  contract: it reads and never writes (D2809).
- **A6.** Cloud writes outside `fugaro update` are acceptable for **people and roles** only, the way
  `fugaro user grant` and `fugaro user offboard` already write the roster live, plus the three
  authority writes this design cannot route through Terraform: `workspace init`'s folder, a member
  owner's `workspace project link` (the folder move or the SA's owner grant), and the workspace
  admins' own bindings on the folder and the workspace SA (D2818). No binding **on a member
  project** is ever written outside that member's own Terraform (D2801). Everything else still
  reaches the cloud through `fugaro update` (D1303 stands outside the roster, D2817). *(Amended after
  review round 1.)*
- **A7.** Every write command here refuses in a coding agent's session, as `fugaro user add|grant`
  do today: a change to who may do what is the owner's act.

## 2. What the repository already has

| Spec concept | What `main` has |
| --- | --- |
| Workspace's own Firebase/GCP project | Exists: D1290; root `deploy/terraform/gcp/roots/workspace`, rules `internal/budget/rules/workspace.go` |
| One workspace per project | Exists: D1291, the scalar `workspace:` key (`localcfg.Config.Workspace`) |
| `project add` / `project rm` | Exists in two halves: `fugaro workspace member add|rm <workspace> <project>` (owner's half, `newWorkspaceMemberAddCmd`, `newWorkspaceMemberRmCmd`) and `fugaro workspace add|rm <name>` (the member's half with its pin, `newWorkspaceAddCmd`, `newWorkspaceRmCmd`) |
| `project ls` | Partly: `fugaro workspace ls` and `workspace show` list workspaces and members offline, with no health |
| `project status` doctor | Partly: `fugaro doctor` (one installation) and `fugaro project repos status` (`newProjectReposStatusCmd`, one repository's enrolment); nothing across a workspace |
| Roles admin/operator/viewer | Exists: `internal/access` (`Roles`, `ParseScope`, `SubsetOf`, `Effective`), the roster node `config/admin/access` |
| Role → IAM mapping (open question 1) | Exists: `docs/design/roles.md` §1–§2, applied by each installation's Terraform as additive `google_*_iam_member` resources from the roster's derived lists; admin is the project's `roles/owner`/`roles/editor` (D689) |
| `user add/rm/ls/grant/revoke` | Exists **per installation**: `fugaro user add|rm` (`newUserAddCmd`, `newUserRmCmd`), `grant|revoke` (`newUserPairCmd`), `ls` (`newUserLsCmd`) |
| Offboarding | Exists per installation: `fugaro user offboard` (`newUserOffboardCmd`, `internal/cli/offboard.go`): roster grants dropped, the person's own secrets (`fugaro_u_<sub>_<name>`, owner annotation) swept by the intake |
| Fail over to other named tokens | Exists: pools with `strategy: failover` (`docs/project-claude-accounts.md` §13), the run's account choice `internal/runner/accountchoice.go` |
| Credentials service responds; tokens broken | Exists: `doctor --check-bitbucket-token`, the credential state's broken marks (`internal/cli/doctor_brokering.go`), `claudeAccountsStoredCheck` |
| Scheduled image rebuilds | Exists: `google_cloud_scheduler_job.preload_rebuild` (`modules/compute/preload.tf`), `catalog_check` (`modules/installation/catalogcheck.tf`) |
| Spending caps | Exists: the budget nodes; `fugaro project show`; the workspace cap (`fugaro workspace cap`, D1299–D1301) |
| Firestore in a Firebase project | Exists: `internal/firestore`, the spend history (`spendDaily/…`), rules deny every client |
| Folders and organizations | Partly: `fugaro setup --parent organizations/N|folders/N` (`infra.ValidateParent`, `internal/infra/projectcreate.go`) creates a project under a parent; nothing moves an existing project, detects an organization or creates a folder |
| Allowed domains | Exists in another place: the mobile allowlist's domain entries (`internal/mobileauth`, D223); nothing for the roster |
| State repo | Does not exist. Nearest: periodic-reviews' E6 designated repository and folder (owner, 2026-11-17), its writer R9 (draft #3140) |

## 3. Findings

**F1 — Rule.** The addendum gives the workspace "actual IAM authority on each member project" and
makes a workspace admin "automatically an admin on every project". The merged design forbids exactly
this: `docs/workspaces/spec.md` §9 "a workspace never widens a member", D1295 option (A), and D1299's
amendment that the workspace's service holds **no write on a member**. Consequence: the addendum
supersedes the rule for people and roles, by the member's consent at join (D2800); the threat model
(`docs/workspaces/spec.md` §8, `SECURITY.md`'s workspaces section) is rewritten and independently
reviewed before any task (WU0).

**F2 — Wrong.** "Each Fugaro project has its own Firebase project, which is also a GCP project with
the same name." `docs/design/v1.md` (D3 revised 2026-10-04): the Firebase project **may be the
installation's own GCP project or a project of its own**; `doctor_workspace.go`'s
`workspaceSplitCheckID` exists because a workspace's Firebase can be another project's. Consequence:
every per-member operation names the GCP project and the Firebase project separately.

**F3 — Wrong (partly).** "The Firebase identity providers are not used." No public IdP is enabled
(`refusePublicSignUp`, `internal/cli/init_firebase.go`), but Firebase Auth **is** used: a run's custom
token (`docs/design/v1.md`, M9b Identity), the mobile app's pairing custom token (`docs/mobile-auth`)
and the workspace sign-on (`internal/cli/workspace_login.go`), whose verifying half is
`ee/coordinator/oauth` (`workspace_assertion.go`, `admit.go`).
Consequence: D2810 keeps the workspace sign-on, scoped to the hosted authorization server.

**F4 — Exists.** `project add|rm` are `workspace member add|rm` (owner's half) plus `workspace add|rm`
(member's half and pin). Consequence: the new verbs absorb `workspace member` (D2802); the member's
pin stays, because its purpose (no workspace value aims a member's exchange) is untouched.

**F5 — Rule.** `fugaro project` means the selected installation (`project repos`, `policy`, `show`)
and `fugaro user` its roster. The spec's `project add|ls|rm|status` and `user add|rm|ls|grant|revoke`
would give one verb two meanings. Consequence: D2802 puts them under `fugaro workspace`.

**F6 — Gap.** A Fugaro role is not an IAM binding. Each installation's Terraform derives
`terraform.operators`/`admins` from the roster and binds them as ~100 additive resource-level
`google_*_iam_member` resources (buckets, jobs, secrets, registries, the log view). "Translates
workspace role assignments into IAM bindings" can only mean "into each member's roster, which its
own converge turns into bindings". Consequence: D2801.

**F7 — Wrong premise (found in review round 1).** There is no "IAM converge": `-target` is a
forbidden apply flag (`internal/infra/tf/tf_test.go`), so converging a member means a full apply of
its installation with the modules of whoever runs it. From a workspace admin's machine that is a
version-skewed upgrade outside the member's own `update`, from a config `init_new_machine.go` itself
warns not to apply from, and that config lacks the member's workspace pin, so the apply would plan
away the workspace service's grant. Consequence: D2801 amended — the member's own `fugaro update`
applies its roster, and `status` names the grants still waiting for it.

**F8 — Rule.** Three spec behaviours ask a question: `rm` "offers to save" state, an external
address "needs explicit confirmation", drift "offers to reconcile". `CLAUDE.md` and
`docs/no-prompt-policy/` forbid every prompt. Consequence: D2808 (save by default,
`--no-save-state`), D2812 (`--external`), D2809 (a separate `reconcile` verb).

**F9 — Rule.** `fugaro doctor` "only ever reads". Consequence: `workspace project status` reads and
names the fix; it never writes (D2809).

**F10 — Gap.** "Only workspace members can hold roles" and "project admins assign roles only to
existing members" cannot be enforced by IAM: an owner grants anyone in the console, and
installation-level `fugaro user grant` takes any address. Consequence: D2813 (installation commands
refuse on a member; console grants are drift).

**F11 — Gap.** Project admins checking "is this person a workspace member" need to read the
workspace roster; the merged design gives a member's people **no grant on the workspace's project**
(`docs/workspaces/spec.md` §5). Realtime Database IAM is whole-database (`docs/workspaces/README.md`
states it for the service's grant), so a "read-only grant on the roster" would also read the record,
the cap and D1296's token hashes, and a write would reach the membership list itself. Consequence:
D2813 and D2817 as amended on 2026-11-18 — the member list is mirrored into each member's own
database, and per-project grants stay in each member's own roster.

**F12 — Exists.** Repository-scoped grants exist in the member roster (`access.ParseScope`). The
addendum speaks only of per-project roles. Consequence: D2815 carries the scope grammar.

**F13 — Exists.** Offboarding per installation exists (`newUserOffboardCmd`); the addendum's is a
fan-out of it over every member plus the workspace roster. Token failover exists (pools). Consequence:
`workspace user rm` calls the member's offboard for each member (WU9); "stop with a clear error"
is the run's existing refusal when its pool is exhausted (U4).

**F14 — Gap.** "The model keys and named OAuth tokens it references are valid": no check of a
model key's or a Claude token's validity exists — `doctor` has `--check-github-app` and
`--check-bitbucket-token` only. Consequence: `status` reports presence, age and the credentials
service's own broken marks; a validity probe is new behaviour and is open item O4. *(Corrected in
review round 1.)*

**F15 — Gap.** "The state repo is in sync" and "saved to the state repo" name a store that does not
exist. Consequence: D2807 reuses periodic-reviews E6; WU15 waits on R9 (#3140).

**F16 — Gap.** Detecting an organization, creating a folder and moving a project into it are new
cloud surfaces (`internal/gcpfake/crm.go` fakes projects only). Moving a project needs
`resourcemanager.projects.move`-class permission on the project and on both parents; a project with
no parent joins an organization by Google's own migration (U1). Consequence: WU2.

**F17 — Gap.** Drift needs to tell Fugaro's own accounts (job, credentials service, signer,
coordinator) from people and recorded machines. Consequence: D2806; drift reads the member's
Terraform outputs for its own accounts.

**F18 — Gap.** The audit trail as written is self-reported by the actor's CLI. Consequence: D2811
pairs it with Cloud Audit Logs.

**F19 — Rule.** HARD RULE: `fugaro update` is the whole upgrade. Renaming `workspace member` and
adding a roster node must converge an existing workspace with no other step. Consequence: the
roster node is created by its first write (R7), linking is opt-in (D2819), the `workspace member`
rename lands with the commands that replace it (I1), and `fugaro update` gains no stage. *(Amended
in review round 1: the first draft seeded the roster from `update`, which could not read
unlinked members.)*

**F20 — Gap.** The workspace's service account for SA delegation (D2803) is a **new** account with
`roles/owner` on members; the workspace credentials service's account (D1299) must stay without any
write on a member. Consequence: two accounts, never one; WU3's test pins it.

**F21 — Open.** Machine identities' exact model (spec's own words). Answered by D2806.

**F22 — Open.** Linking fugaro-prod app sign-ins (spec's open question 4). Answered by D2814.

## 4. Decisions

All answered by the owner on **2026-11-18**, every one as recommended; D2813 and D2817 were amended the same day (O3), and review round 1 amended D2801, D2802, D2811 and D2813 and added D2818 and D2819 (O5), each answer also as recommended.

| # | Question | Answer (2026-11-18) | Why |
| --- | --- | --- | --- |
| **D2800** | The merged rule "a workspace never widens a member" (F1) | **Superseded by consent** for people and roles: the member's owner gives the authority at join (folder move or SA grant); it is never taken. Threat model rewritten; mandatory independent security review before any task | The addendum's core ask; consent at join keeps the member the one who decides |
| **D2801** | Where a workspace grant lands (F6) | **The member's own roster node, then that member's IAM converge in the same command** — ***amended after review round 1 (owner, 2026-11-18, as recommended): the grant writes the member's roster live and the member's own `fugaro update` applies the IAM; `workspace project status` names each member's grants still waiting for that update; no apply from another machine (F7)*** | One role→binding mapping; Terraform stays the single writer of member bindings; the HARD RULE and in-flight safety hold |
| **D2802** | Command names (F4, F5) | **Under `fugaro workspace`**: `init`, `project add|ls|rm|status|reconcile` (absorbs `workspace member`), `user add|rm|ls|grant|revoke`, `domains add|rm|ls`. Installation-level `fugaro user` stays — ***amended after review round 1 as a consequence of D2819: adds `workspace project link|unlink`, the member owner's consent, run on the member owner's machine; `workspace add|rm` (the pin) stay*** | No verb with two meanings |
| **D2803** | Authority mechanism (F16) | **Folder first, SA fallback**, picked by `workspace init`; the SA is used by impersonation only (no keys), workspace admins its only token creators; the folder slice ships first | The spec's two mechanisms, without a key to leak |
| **D2804** | Workspace admin's role; open question 1 | **`roles/owner`** on the folder (or held by the SA on a member); per-project mapping unchanged from `docs/design/roles.md` | Matches D689: admin is owner/editor |
| **D2805** | Existing bindings at join; open question 2 | **Adopt, never strip**: roster grants imported (outside the domains → external, needs `--external`); nothing removed at join; unmapped bindings reported as drift | Nobody is locked out by joining |
| **D2806** | Machine identities; open question 3 | **Members of kind `sa`** with per-project roles via the same commands; Fugaro's own accounts recognised from Terraform outputs, never drift | Audit and drift cover CI accounts |
| **D2807** | The state repo (F15) | **Periodic-reviews E6's designated repository and folder**, written as a pull request through R9's writer; the workspace uses its own project's E6 setting | One designated-repo setting |
| **D2808** | `rm` and state (F8) | **Save by default** (a pull request to the state repo; with none set, a local file it names), `--no-save-state` skips; prints the running-cost warning and the console shut-down link | No question asked, nothing lost by default |
| **D2809** | Fixing drift (F8, F9) | **A separate write verb**: `workspace project reconcile <project>` (`--dry-run` prints the change); `status` only reads and names it | `doctor` stays read-only |
| **D2810** | The workspace sign-on (F3) | **Kept, scoped**: the Google address is the identity; the Firebase exchange stays as the hosted authorization server's sign-in. *(Round 1 removed a clause the first draft added — "its admission reads the workspace roster" — which was not the owner's answer and which `ee/coordinator/oauth/admit.go` does not do.)* | No retirement work |
| **D2811** | Audit trail (F18) | **Firestore + Cloud Audit Logs**: create-only Firestore events (rules deny every client); `status` names IAM changes in Cloud Audit Logs that Fugaro's trail lacks; the docs say the Firestore trail is not tamper-proof — ***amended after review round 1 (owner, 2026-11-18, as recommended): a project admin's grant, revoke and offboard are audited in the member's own Firestore, which they can write; workspace admins' changes in the workspace's; `status` aggregates both*** | Console changes get a who-did-it; a project admin holds no write on the workspace project |
| **D2812** | Allowed domains | **Fugaro's list only**: outside the list needs `--external`, marked external; no organization policy set | Externals stay possible |
| **D2813** | Enforcing membership (F10, F11) | **Workspace commands only** on a member: installation-level `fugaro user add|rm|grant|revoke|offboard` refuse and name the workspace command; console grants are drift. Project admins ~~get read-only on the workspace roster~~ — ***amended 2026-11-18 (owner, as recommended): the member list (address, kind, external) is mirrored into each member's own `config/admin/workspace-members` on every membership write and converge; no grant on the workspace project*** — ***amended again after review round 1 (owner, 2026-11-18, as recommended): the mirror gate is advisory, because a member's owners hold `roles/firebasedatabase.admin` on their own database and can rewrite it; the fence is drift, run by workspace admins against the workspace node (never against the mirror); the threat model says so*** | One writer of a member's roster; RTDB IAM cannot scope a read to one path (F11) |
| **D2814** | fugaro-prod app sign-ins; open question 4 | **Out of scope, no task**; the app keeps per-project pairing | Spec puts the app out of scope |
| **D2815** | Grant scope (F12) | **Carry the scope grammar**: `--scope project|all-repos|<patterns>`, default `project`, with roles-scopes' subset and last-admin refusals | Nothing a member expresses today is lost |
| **D2816** | *(reserved, unused: the questions were numbered in the order they were asked)* | — | — |
| **D2818** | How workspace admins are held on members (R4 withdrawn) | ***Added after review round 1, answered 2026-11-18 as recommended:*** **stored grants plus live bindings** — `workspace user add --admin` writes a project-scope admin grant marked `via: workspace` into every linked member's roster and adds the folder `roles/owner` or the workspace SA's token-creator binding directly; `rm --admin` removes all of them in the same command | Offboarding needs a stored admin grant (D1940); a removed admin must lose every member at once |
| **D2819** | Existing workspaces | ***Added after review round 1, answered 2026-11-18 as recommended:*** **opt-in link, `update` unchanged** — `fugaro update` changes nothing about authority; a member owner turns people management on with `fugaro workspace project link` on their own machine; `status` says "not linked" | Members joined under "never widens"; widening them needs fresh consent, and an opt-in feature is not an upgrade step |
| **D2817** | Where the roster lives | **A live node** `workspaces/<name>/roster` (moved to `workspaceRosters/<name>` by R5) in the workspace's database, ETag-conditional writes, rules deny clients; amends D1303 for the roster only — ***amended 2026-11-18 (owner, as recommended): the node holds members, their kind, the external mark, the domains and the workspace admins, written by workspace admins only; per-project grants stay in each member's own roster `config/admin/access`, written by `workspace user grant` and gated by the mirror; `workspace user ls` aggregates across members*** | Two admins never overwrite each other; a project admin's grant must not need a write that reaches the membership list (F11) |

Resolutions with one defensible answer (no decision):

- **R1.** A single workspace admin is a `warn` row in `status` with the hint `fugaro workspace user add
  <email> --admin` (spec's own rule).
- **R2.** `workspace project add` refuses a project whose config names another workspace (D1291) and,
  in the folder model, one whose parent is another workspace's folder.
- **R3.** `rm`'s console link is `https://console.cloud.google.com/iam-admin/settings?project=<id>`,
  printed, never opened (U3 checks it is the shut-down page).
- **R4.** *Withdrawn after review round 1:* "a workspace admin is never written into member
  rosters" would make `fugaro user offboard` refuse on every member, because it admits only a stored
  project-scope admin grant (D1940). Replaced by D2818.
- **R5.** The roster lives at `workspaceRosters/<name>`, a sibling of the record, not under
  `workspaces/<name>`: `fugaro update`'s publish stage rewrites `workspaces/<name>` whole under one
  ETag and refuses a node with keys it does not know (`internal/cli/update_workspace.go`,
  `node.UnknownKeys`), so a roster inside it would stop every older CLI's update. *(Found in review
  round 1; amends where D2817 put the node, not what it holds.)*
- **R6.** The roster's person type is `Person`, not `Member`: `internal/workspace` already defines
  `Member` (a member project). *(Found in review round 1.)*
- **R7.** A first write of the roster creates the node (a conditional create when no ETag exists),
  so no `update` stage seeds it and an existing workspace converges with `fugaro update` unchanged.

## 5. Open tensions and open items

- **Open tensions:** none. D2813 (installation commands refuse on a member) and D2815 (the scope
  grammar) were checked together: the workspace command carries every scope the installation
  command could, so nothing is stranded.
- **O1.** D2800 makes WU0's independent security review a gate on every code task; its findings may
  amend D2801, D2803 or D2817.
- **O2.** WU15 waits on periodic-reviews R9 (#3140) merging.
- **O4.** A validity probe for model keys and Claude tokens (F14) is new behaviour with no design;
  `status` reports presence, age and broken marks until one is designed.
- **O5.** Round 1 of the review amended D2801, D2802, D2811 and D2813 and added D2818 and D2819; the
  owner answered every one as recommended on 2026-11-18. D2802's amendment (`project link|unlink`)
  follows from D2819 and was not asked separately: confirm it.
- **O3.** D2813 and D2817 were amended on 2026-11-18, after their first answers, because RTDB IAM
  has no path-level scope (F11). The owner answered both amendments as recommended; the original
  answers are kept, struck, beside them. "The workspace is the single source of truth" now reads:
  the workspace node is the truth for **who is a member**, each member's roster for **what they may
  do there**, and nothing else holds either fact.

## 6. UNVERIFIED

- **U1.** The exact permissions to move an existing project into a folder, and whether a project with
  no organization can be moved by the CLI or only through Google's migration flow. Settled by Check
  720 on a test organization (owner).
- **U2.** *Settled by review round 1:* a rebuilt config does not carry the workspace pin, so no
  apply from another machine is planned (F7, D2801 amended).
- **U3.** That R3's URL is Google's shut-down page for a project. Settled by the owner opening it once.
- **U4.** That a run whose only credential was swept stops with a clear error rather than a generic
  401. Settled by WU9's `TestSweptOnlyCredentialRefusesClearly`.
- **U5.** Cloud Audit Logs' Admin Activity entries for `SetIamPolicy` are readable with
  `roles/logging.viewer` on the member, which a workspace admin's `roles/owner` includes. Settled by
  Check 721 (owner).
- **U6.** Whether a workspace admin holding `roles/owner` on the folder can move a member back to its
  original parent without the member owner's rights there. Until Check 720 settles it, `project rm`
  on the folder mechanism prints `fugaro workspace project unlink` for the member owner (WU6).
