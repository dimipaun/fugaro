# Workspace projects and users: the threat model

**Status: this is WU0's deliverable — the rewritten threat model
[implementation-plan.md](implementation-plan.md) asks for, and the gate every code task of that plan
waits behind.** Nothing here is implemented: no task but WU0 is approved, and no command this file
names exists yet. It is written to the owner's answers of 2026-11-18 (D2800–D2825,
[reconciliation.md](reconciliation.md) §3–§4) and states only what the addendum's authority would
reach. **An independent security review of this file is mandatory and the coordinator runs it**
(`fugaro review --pr N`, WU0's acceptance), and the owner answers every finding before any other task
launches — so what follows is the document that review reads, and the statement the roster contract
(C1) and the member-operations contract (C2) are checked against.

**Why there is a new threat model at all.** [docs/workspaces/spec.md](../workspaces/spec.md) §8 is the
threat model of the workspace's *identity and credential* half: what a stolen workspace token reaches,
what a member's operator reaches of the shared pool, what the cross-project read exposes. None of it
changes here (§4). What the addendum changes is the direction the older design forbade: the workspace
gains **IAM authority over its member projects** — a folder the members sit in, or a service account
the members granted — and it manages the members' people and roles. That authority is what this file
states at its real size, and it is why the workspaces design's own rule that "a workspace never widens
a member" had to be answered by the owner before anything was built (F1, D2800).

**No placeholder, credential or figure appears here.** Every repository, person and workspace is a
placeholder (`owner/repo-a`, `owner/repo-b`, `person-1`, `workspace-alpha`), no file carries a
credential, a key, a token or anything shaped like one, and no currency figure is stated.

## 1. The consent, which is the whole of the supersession

**The authority is given, never taken.** The member's owner runs `fugaro workspace project link` on
their own machine, with the member's own config selected and its pin naming the workspace, and the
link is made from **that owner's own credentials**: the folder move, or the service account's
`roles/owner` grant (D2800, D2819, WU18, WU2 and WU20). A run of `link` on any other config, or
against a pin naming another workspace, refuses — the authority cannot be created from the
workspace's side (WU18's `TestLinkNeedsTheMembersOwnConfig`,
`TestLinkRefusesOtherWorkspacePin`).

**`fugaro update` changes nothing about authority** (D2819, R7). Linking is opt-in, feature by
feature: a member joined under the old rule keeps `fugaro user …` and keeps its authority exactly as
it is until its owner links it, and an unlinked member is not reached by any command of the addendum
(WU10's `TestUserCommandsUnchangedOnAnUnlinkedMember`). So "existing members are widened by an
upgrade" is not a thing that happens here; each member's owner decides once, and can take it back
(§6).

**One consent covers a set that changes.** The owner consents to the workspace's **admins** holding
authority over their member, and the admin set is the workspace's to change afterwards (WU7): a
person added as a workspace admin later reaches the member without the member owner being asked
again. That is the consent's real shape, and §3 is its real size. The counterpart is the member
owner's own exit: `unlink` drops the authority over their member at once (§6).

**In the folder model the consent also moves policies, and that is a second effect rather than a
detail of the first** (stated as §7's tenth residual risk). A GCP project takes the IAM and the
organization policies of the parent it hangs under, so `link` — which moves the member's project into
the workspace's folder (WU2, WU18) — makes the member **lose whatever its old parent carried and
inherit the workspace folder's instead**: a key-creation ban, a domain restriction, an
allowed-locations constraint, an org policy the old parent applied and the workspace's does not, or
the reverse. Nothing in the model narrows the workspace folder's own policies to make linking
harmless, and the SA model does not move the project at all (the account is granted on it where it
stands), so this is the folder's cost and not the addendum's: what `link` does about it is **report
the difference it can read on both sides before the move is made** (WU18's acceptance), and what it
cannot do is undo an inheritance it has changed.

## 2. Which of spec §9's rules the addendum supersedes, and on what consent

[docs/workspaces/spec.md](../workspaces/spec.md) §9 states six prohibitions. **Three of them are
superseded, for people and roles only, on the consent of §1** — and the other three stand unchanged.

**Superseded:**

- **never widens a member.** A workspace admin is an admin on every linked member (D2804: `roles/owner`
  on the folder, or held by the workspace's service account on the member), and a workspace grant
  lands in a member's own roster, where that member's `fugaro update` turns it into IAM bindings
  (D2801). Adding a person, or a role, to a member is exactly what the rule forbade, and it is what
  the addendum is for.
- **never writes a member's database.** Three nodes of a member's `config/admin` are the workspace's
  to write: the member's service roster (`config/admin/access`), the mirror
  (`config/admin/workspace-members`) and the audit trail (`config/admin/workspace-audit`, D2811). The
  rule's own reason survives in the narrow shape — what the addendum writes is people and roles, and
  the nodes that decide what a member may do (`config/policy`, `config/caps`, `config/kill`,
  `config/limits`, `config/mode`, `config/admin/devices`) are not among them — and the workspace's
  *services* still hold no write there (D1299).
- **never becomes a second permission model.** There is now a workspace role that means something in
  a member: "workspace admin" is admin on every member, and a workspace grant is a member grant. What
  is *not* a second model is what the addendum's commands touch: no command of it raises a member's
  cap, edits its policy, admits a repository or changes its mode, and §4 of the workspaces design (the
  credential pool's intersection) and D1295's option (A) are untouched.

**Stated rather than left implied: "for people and roles" is the scope of the commands, not a
narrowing of the consent.** What the member owner hands over at `link` is `roles/owner` on their
project, and an owner's reach is wider than people and roles — §3 states it, caps and policy nodes
included. The phrase is true of every command the plan adds, and it must not be read as a claim that
the authority itself stops at the roster.

**Not superseded:**

- **never hands out a credential.** The rule stays as the workspaces design wrote it, about the
  workspace's *services and CLI*: no shared credential's value is returned to a run, a CLI, a person,
  an error message or a log line, and the credential service proxies the call. The addendum adds no
  exception to that — and it does not narrow the rule's honest reading either: the rule never claimed
  that an **IAM holder** cannot read a credential, and after `link` a workspace admin is one. §3
  states what that means, because a reader who took "never hands out a credential" for "no one in the
  workspace can read a member's secrets" would be wrong by a whole Secret Manager. **The workspace's
  own project is the same reading once more**: every workspace admin holds `roles/owner` on it (R8,
  D2818), the workspace's one credentials bundle lives there ([spec.md](../workspaces/spec.md) §4),
  and a project owner reads any secret in its project — so **the shared pool's keys are readable by
  every workspace admin**, beside every member secret §3 states.
- **never owns a repository, a runs bucket, a job or an image.** Unchanged: the workspace still holds
  no runs and launches nothing, and `project rm` deletes no GCP or Firebase resource — it detaches,
  prints the running-cost warning and names the console page for the owner to shut the project down
  themselves (WU6, D2808).
- **carries no workspace-wide repository allow-list.** Unchanged: the scope grammar is per grant
  (D2815), a grant lands in one member's own roster, and a repository allow-list stays the policy's
  and per project.

## 3. What each credential or actor reaches

Seven things are in reach of a linked member, and each is stated here at its real size. The one
sentence they share: **in the folder model a workspace admin is a project owner on every linked
member, and in the service-account model they can become one; a member's project owner is the widest
authority the member has.**

### A stolen workspace-admin credential reaches every linked member, and that is the consent's real size

A workspace admin holds, on every linked member:

- in the **folder** model, `roles/owner` on the workspace's folder (D2804), inherited by every project
  in it — the member's GCP project and, where its Firebase project is separate, that project too
  (R10);
- in the **service-account** model, the workspace IAM account's token-creator binding (D2818, R8), by
  which they impersonate an account that is `roles/owner` on every member it linked.

`roles/owner` on a project is the widest authority that project has, and it is worth stating what it
holds rather than "admin": it **reads every secret of the member** — the Secret Manager bundle's
model keys, the forge credential, and **every personal Claude OAuth token** the member's people stored
under their own prefixes; it **writes the member's IAM**, so it can grant itself or anyone else
anything, including impersonation of the member's own accounts; it **reads and rewrites the member's
whole Realtime Database** (IAM on a Realtime Database is whole-database and bypasses the rules — the
workspaces section of `SECURITY.md` says so at its real size), which is the member's policy, caps,
kill switches, mode, limits, paired devices and run ledgers; it **reads the member's runs bucket and
its run logs**; and it **can delete the member's project**.

Against §9's "never hands out a credential" this is the sentence that matters: the rule is about the
workspace's services and CLI, and this is a **person the member's owner made an owner of their
project**. The exposure is the consent's, and the consent is the member owner's to withdraw (§6).

**What bounds it, and what does not.** The admin set is small, named in the workspace node (`Admins`)
and audited; `status` warns when a workspace has a single admin (R1) and flags an `Admins` entry that
holds no live binding, or a binding holder that is no entry (D2823, WU11); `rm --admin` removes the
folder or service-account binding at once (D2818, §6); a member's owner removes it for their member
with `unlink` (D2822); and Cloud Audit Logs record `SetIamPolicy` on a member, which `status` reads
against Fugaro's own trail (D2811, WU12). **Nothing here detects a stolen credential**: it is a Google
account, its protection is the owner's (second factor, session lifetime), and no part of this design
reads how it was used.

### A compromised workspace IAM account is the same reach held as an account, and a key made on it outlives the binding that made it

The service-account fallback's identity (D2803, WU3, WU20) exists only where a member's owner linked
it through the fallback, and it is **used by impersonation alone**: the root that creates it writes
**no key**, and WU3's terraform test pins that the account is not the credentials service's and that
Terraform makes no key for it. That is a fact about what Fugaro writes, and **not a claim that no key
can exist**. Nobody in this design holds `iam.serviceAccountKeys.create` on purpose, but on GCP
`roles/editor` on the workspace project resolves to exactly that permission — the repository's own
`fugaro doctor token-signers` check classifies an editor by it (`internal/infra/tokensigners.go`) — as
does `roles/iam.serviceAccountKeyAdmin`, and `roles/iam.serviceAccountAdmin` can grant either to
itself. So the account has **two credential paths that Fugaro does not write and `status` does not
read today**:

- **a user-managed key made out of band** — a credential of the account's own, acting as the account
  wherever it still holds authority, with no token-creator binding, no `Admins` entry and nothing for
  D2823's intersection to intersect;
- **`iam.serviceAccounts.actAs`**, which `roles/editor` and `roles/iam.serviceAccountUser` carry — the
  credentials section of `SECURITY.md` names the editor's pair, `actAs` and `run.services.update` — by
  which a holder deploys a Cloud Run service, a VM or a Cloud Build that **runs as** the account: no
  key, no impersonation grant, and no binding for either check to see.

Its token creators are the workspace's admins (D2818, R8), so the account's reach is a stolen
workspace-admin credential's, one hop longer: whoever holds one of those token-creator bindings, or
holds `resourcemanager.projects.setIamPolicy` on the workspace project — which is what that project's
**owners** hold and its **editors do not**, `roles/editor` being unable to write an IAM policy —
reaches every member linked through the account as its owner. **What D2823's intersection and `status`
make visible is the binding set and the `Admins` list, and neither path above appears in either.** A
key the removed admin made under their own credentials survives `rm --admin`, which removes the
token-creator binding and revokes nothing on the account, so it keeps acting as the account — and
keeps reaching every member still linked — until it is deleted on the account or the account is
disabled; `gcloud iam service-accounts keys list` and the account's own IAM policy are what show it.
A member whose owner runs `unlink` does bound it for that member: it takes the account's `roles/owner`
away (D2822), and the key can then only act as an account that holds nothing there. **The one control
that closes key creation outright is the organization policy
`constraints/iam.disableServiceAccountKeyCreation`**, which no Fugaro root can set on a project it does
not own.

### Four accounts can rewrite the workspace's database, `Admins` included

The roster now lives in the workspace's own database, at `workspaceRosters/<name>` (R5, D2817), and
the workspace's project is converged by the installation's roots exactly as a member's is, so **four
accounts hold a whole-database write on that database**, none of them path-scoped:

- the **workspace's credentials service**, whose custom role is `fugaroCredentialsDatabase` —
  `firebasedatabase.instances.get` and `.update`, the pair every installation's credentials service is
  given on its own project's database
  (`deploy/terraform/gcp/modules/installation/credentials.tf`);
- the **workspace's own service account**, holding `fugaroWorkspaceDatabase`, the same pair, for the
  workspace's own service (`deploy/terraform/gcp/roots/workspace/main.tf`, whose plan is pinned to that
  one account and that one grant);
- the **history account** the installation's Firebase root creates and grants on every converged
  project, `fugaro-history@<project>` with `roles/firebasedatabase.admin` — all eight
  `firebasedatabase.instances.*` — where a `history_account` left empty is filled in from the
  project's own name (`deploy/terraform/gcp/modules/firebase/iam.tf`);
- the **coordinator's writer account**, holding that same admin role on the project where the owner has
  opted the coordinator in, and nothing where they have not (`internal/infra/firebase.go` leaves it
  empty unless the installation spec carries the coordinator).

A grant on a Realtime Database has no path-level scope, so any of the four is a whole-database read
and a whole-database write: **a compromise of any of them can rewrite the member list, the domains
and the `Admins` list whole**, and the workspace's cap, kill switch and its own meter figures beside
them. **The two `roles/firebasedatabase.admin` holders are the wider pair**: that role adds the
instance lifecycle (`create`, `delete`, `disable`, `reenable`, `undelete`), so the history account and
the coordinator's writer can also delete or disable the workspace's database, where the two
custom-role accounts can rewrite it and not remove it.

**What it cannot do is become an admin by writing the list.** D2823 is the fence: only an entry of
`Admins` that also holds the live folder-owner (or service-account token-creator) binding is
propagated or trusted, `project add` and `reconcile` use that intersection, and `status` flags both
directions of the difference (WU11 `TestStatusFlagsAdminTampering`, WU12
`TestDriftTrustsOnlyBoundAdmins`). None of the four can mint that binding: none of their roles writes
the workspace project's IAM policy, the credentials service holds no IAM write on the workspace's
project and **no write on any member at all** (D1299), and the workspace root's plan carries its one
service account and its one database grant (WU3 keeps the SA fallback's new IAM account, D2803,
distinct from both). What a compromise *can* do is make the workspace manage the wrong people — drop a
member's entry, mark an address external, suspend or restore the workspace's own stop — until a
workspace admin reads `status`, and, for the two admin-role holders, take the workspace's own database
away outright. The intersection is what keeps a rewrite out of authority and `status` what makes it
visible; a database that is gone is the workspace's own state lost, not authority anyone gained.

### The workspace project's owners, editors and budget admins are workspace admins de facto

The workspace's own project is an installation like any other, so its project IAM reaches the
workspace's database over IAM, rules bypassed:

- its **owners and editors** can write the roster whole — `Admins` included. Only its **owners** can
  then grant themselves a token-creator binding on the workspace's own account, which takes
  `resourcemanager.projects.setIamPolicy`: `roles/owner` has it and `roles/editor` does not. D2823's
  intersection *trusts* them once they hold it, because the binding is real — they are workspace admins
  with the authority §3 states and the workspace IAM account carries. An **editor's** route to that
  account is the pair the service-account paragraph above states — `iam.serviceAccounts.actAs` and
  `iam.serviceAccountKeys.create` — which mints no binding and leaves no `Admins` entry, so the
  intersection that makes a rewritten `Admins` visible sees nothing of it.
- its **budget admins** hold `roles/firebasedatabase.admin` on that project
  (`deploy/terraform/gcp/modules/firebase/iam.tf`) — `roles/owner`-derived today, D689 — which writes
  the same database nodes and **cannot write IAM**. That is exactly the difference D2823 rests on:
  they can rewrite `Admins`, and they cannot take the binding that would make the entry count.

**Stated plainly: the workspace project's own IAM is the workspace's root of trust.** Nobody who owns
that project can be kept out of the workspace's roster, and no arrangement of `Admins` changes it.
What the roster and D2823 buy is that the difference is **visible** — an `Admins` entry without the
binding, or a binding holder who is not an entry, is a `status` row — and that an actor who can only
write the database cannot thereby act on a member, because every member operation goes through a live
binding (`Authority.Holds`) and writes the member with the caller's own credentials.

### A hostile project admin rewrites the mirror and the member's audit node

A member's own admin is bound on that member with `roles/firebasedatabase.admin` on its Firebase
project (`deploy/terraform/gcp/modules/firebase/iam.tf`), and the member's own GCP owners and editors
are implicit admins the same way (D689). That role is a whole-database write, so a hostile project
admin — a member admin, or any account holding the member's database write — can rewrite:

- **the mirror**, `config/admin/workspace-members`. D2813 makes it the **advisory** gate on a
  workspace grant: a rewritten mirror lands a grant for an address the workspace node does not list,
  or hides one it does. The fence is not the mirror: it is **drift, run by a workspace admin against
  the workspace node and never against the mirror** (D2813, WU12 `TestDriftIgnoresARewrittenMirror`).
- **the member's audit node**, `config/admin/workspace-audit`. Its entries are create-only by
  construction (WU1's `AuditWriter`, WU4's `TestMemberAuditCreateOnly`), and they are written **with the
  permission the roster write itself needs** (D2811) — so **the audit trail is as tamperable as the
  roster**: whoever holds the member's database write can delete or rewrite it. The cross-check is
  Cloud Audit Logs, where `status` names each `SetIamPolicy` whose resulting binding is drift and
  which neither audit trail holds (D2811, R13, WU12).

A project admin can also change the member's own roster and, through the member's own `fugaro update`,
its IAM. **What a project admin cannot do** is write the workspace's own node: a member's people hold
no grant on the workspace's project (F11, D2813), so they cannot add themselves to the workspace,
change the domains, or reach another member; and `project add|rm` are the workspace project's owner
alone (D2821).

### A hostile member owner reaches their own member and nothing else

The member's owner holds their own project by definition: they can rewrite its roster, its mirror, its
audit node, its policy and caps, remove any binding the workspace holds there, read their own secrets,
and refuse to link at all.

The one record of theirs the workspace's own commands read is **`config/admin/workspace-link`** — the
original parent, written by the member owner at `link` (R14) and read by `Unlink` to move the project
back (WU2). It is a member-controlled value, and a member-controlled value is untrusted: the member's
budget admins hold the member's database write, so they — like the member's owner, and like anyone who
has taken either account — can rewrite it. What keeps a rewritten record from aiming anything is
WU2's `Unlink`, which **refuses a recorded parent inside any workspace's folder** rather than moving a
project there (`TestFolderUnlinkRefusesATamperedParent`): the worst a rewrite achieves is a refusal,
a denial of the move back and never a project placed in another workspace's folder.

Their reach stops at their own member: they hold no grant on the workspace's project and read no node
of its database, so the roster, the workspace's cap and the group's audit are not theirs to read. What
they can read of the group is the mirror, which the paragraph headed **Every member's launchers and
viewers read the mirror** below states — and what they can do to the
workspace is make a member look wrong, which `status` reports as "not linked" or as drift.

### Every member's launchers and viewers read the mirror: the workspace's member addresses and external marks

Every launcher, operator and viewer of a member holds `roles/firebasedatabase.viewer` on that member
(`deploy/terraform/gcp/modules/firebase/iam.tf`), which reads every node of its database over IAM —
`config/admin`'s deny to clients included, because IAM bypasses the rules. So the mirror is readable
by that member's own people, and with it **the workspace's member addresses, each member's kind and
which of them are external**.

That is a disclosure the addendum adds, and it is worth stating against what the workspaces design
said: §2 of that design has the member list read by the workspace's own service and the workspace
owner's CLI **"and by nothing else"**. The mirror is a second copy, in every member's database,
readable by that member's people. It is bounded — addresses, kinds and external marks only: no roles,
no grants, no per-project data, and each member's grants stay in that member's own roster — but it is
not nothing, no rules change narrows it (IAM bypasses the rules), and a workspace whose membership is
sensitive should know it. The same list is readable from the workspace's own side: the workspace's
project is an installation of its own, so its launchers, operators and viewers hold the same viewer
role on the workspace's database and read the roster whole, and its budget admins can write it.

**If the external mark's disclosure is unwanted, the fix is a decision and not a rules change** —
mirroring addresses without the mark, or a narrower reader — and it is recorded in §7 as an accepted
risk for the owner rather than settled here.

## 4. Two things the addendum does not change

- **The workspaces design's own threat model stands.** What a stolen workspace token reaches, what the
  exchange refuses, what a member's operator reaches of the shared credential pool, the cap and its
  meter, the cross-project read and the stop are `docs/workspaces/spec.md` §8's, restated in
  `SECURITY.md`'s workspaces section, and unchanged by this addendum: it adds no endpoint to a
  member's API and none to the workspace's project.
- **Terraform stays the only writer of a member's IAM bindings.** A workspace grant writes the
  member's roster, and the member's **own** `fugaro update` turns it into bindings (D2801, F7): no
  apply is run against a member from another machine, so the version-skewed apply and the config that
  lacks the member's pin are not paths this design opens.

## 5. What a workspace's own commands cannot do

- `status` writes nothing on any path, `--check-credentials` included (D2809, WU11), and
  `--check-credentials` reads credentials only in the owner's own terminal and never prints a value
  (D2820, D2825, WU22).
- `project add|rm` are the workspace project's owner's alone, on the machine whose config holds the
  member table (D2821); `project rm` deletes no GCP or Firebase resource (WU6).
- Every write command of the family refuses in a coding agent's session, as `fugaro user add|grant`
  do today (A7), and none asks a question: an address outside the domains needs `--external` (D2812),
  and `rm` saves state by default and takes `--no-save-state` (D2808).
- The state snapshot is refused for a public repository, because it carries addresses (D2824, WU15).

## 6. Revocation, and the lag that is real

**A workspace admin's removal is two acts with two clocks** (D2818 as corrected). `fugaro workspace
user rm <email> --admin` removes the folder's `roles/owner` or the account's token-creator binding
**at once, in the same command**, so the authority **Fugaro granted them** is gone then. What waits is
the stored admin grant in every linked member's roster and the resource-level bindings that member's
own Terraform derives from its admin list (D1940): they follow at **that member's own `fugaro
update`** (D2801), and `workspace project status` shows each member's grants still waiting as a
pending row (WU11). So between the two commands a removed admin still holds a member's bucket, job,
secret and log grants, bounded by that member's own next update.

**What no command here takes back is what the admin made while they held the authority** (the owner's
ruling of 2026-11-19, §7's sixth residual risk). Before the command ran they were an owner of the
member, and an owner can leave three things behind that outlive `rm --admin`, `unlink` and the
member's own `fugaro update` alike:

- **a key on a member's service account** — the member's own Terraform accounts included. It is a
  credential of that account's, usable from anywhere, and no Fugaro command revokes it;
- **an IAM binding they added for themselves outside Terraform.** The member's roots are additive
  (`google_project_iam_member`): a member's `fugaro update` adds and removes exactly what its own
  lists name, so a binding none of those lists names is left standing by every update that follows;
- **the copied value of any secret they could read** — every secret of the member (§3), and the
  shared pool's keys in the workspace's own project (§2, R8). A value someone has copied is not a
  grant, and there is nothing to revoke.

So the honest sentence is that removing an admin ends **their Fugaro-granted reach and not their
access**: what follows the removal is **rotating that member's secrets** — its model keys, the
personal Claude OAuth tokens its people stored under their own prefixes, its forge credential and the
run-scoped ones — and **reading the member's IAM policy for bindings the member's own roots did not
make**. `status` shows half of that: drift reports a member's policy against its roster and names the
grants the roster did not put there (WU12). It shows nothing of a key or of a copied value, and this
file adds no row for them — the gap is stated here as the acceptance it is, not built into a check.

**A member owner's `unlink` drops only the authority** (D2822 — not the recommendation). The workspace
admins' stored grants and the mirror stay in the member's roster, and the member's own `fugaro update`
keeps applying them, until the **workspace project owner's** `project rm` removes them; `status` says
"unlinked, grants left" in between (WU11), and `project rm` may be run after an unlink for exactly
that reason (WU6). The consequence is stated rather than implied: **offboarding a person from a
member, or ending a member's part in a workspace, takes both sides** — the member owner's `unlink`
and the workspace owner's `rm` — and until both have run, one of the two is still true.

**Nothing here is instantaneous, and nothing claims to be.** Both lags are a member's own `update`
away, which is the same clock every workspace grant runs on.

## 7. What this design accepts, as residual risk

1. **A workspace admin reads every member's secrets, personal Claude OAuth tokens included** (§3): the
   consent at `link`, and the widest exposure this addendum creates. No technical fence; the member's
   owner's `unlink`, an admin's `rm --admin` and Google's own account protection are what bound it.
2. **The workspace project's own IAM is the workspace's root of trust** (§3): its owners and editors
   are workspace admins whatever `Admins` says, and its budget admins can rewrite the roster. D2823
   makes the difference visible; nothing makes it impossible. Stated, not closed.
3. **Four accounts can rewrite the workspace's database, `Admins` included** (§3): the workspace
   credentials service's account and the workspace's own service account, each holding the same
   get-and-update pair there; and the history account, unconditionally, and the coordinator's writer
   account, where the owner has opted the coordinator in, holding `roles/firebasedatabase.admin`
   instead — the same whole-database write plus the instance lifecycle, so those two can delete or
   disable the workspace's database as well. Neither a
   rewrite nor a deletion becomes authority: D2823's intersection is what keeps a rewritten `Admins`
   out of it, and `status` is what shows the rewrite, while a deleted database is the workspace losing
   its own state rather than anyone gaining a member. A compromise of any of the four is a compromise
   of the workspace's own state either way.
4. **The mirror discloses the workspace's member addresses and external marks to every member's
   launchers and viewers** (§3), and to the workspace project's own people. Bounded to addresses,
   kinds and marks; no rules change narrows it.
5. **The audit trail is as tamperable as the roster** (D2811): create-only by construction, rewritable
   by whoever holds the member's database write, and cross-checked against Cloud Audit Logs rather
   than protected.
6. **A removal does not take back what the removed admin made while they held the authority** (§6,
   D2818 as corrected, and **the owner's ruling of 2026-11-19** is the consent to accept it): the
   Fugaro-granted authority goes at once and the stored grants follow at the member's own `fugaro
   update` — and the member that has unlinked keeps applying those grants until the workspace owner's
   `project rm` (D2822) — but a **key minted on one of the member's service accounts**, an **IAM
   binding added outside Terraform** (a member's roots are additive, so its own updates leave it) and
   the **copied value of any secret the admin could read** (every member secret, and the shared
   pool's keys in the workspace's own project, §2 R8) all outlive `rm --admin`, `unlink` and the
   member's own `update`. What the member is left to do is therefore part of the removal, not an
   afterthought of it: **rotate the member's secrets** and read its IAM policy for bindings its own
   roots did not make. `status` covers the second half through drift (WU12) and **none of it is a
   `status` row of its own** — no check is added for a key or for a copied value, and the gap is
   accepted here rather than closed.
7. **A member owner's own record can make `unlink` refuse, never aim it** (R14): the recorded parent is
   untrusted input, so the failure it can cause is a denial of the move back, not a project placed in
   another workspace's folder.
8. **One consent covers an open-ended admin set** (§1): a person made a workspace admin later reaches
   every linked member without the member's owner being asked again. A member owner who wants that
   bounded has `unlink`, which is all-or-nothing for their member.
9. **The workspace IAM account's own credential paths are outside every check this design builds**
   (§3): a project editor, or any holder of `roles/iam.serviceAccountKeyAdmin` or
   `roles/iam.serviceAccountAdmin`, can mint a **user-managed key** on the account — a credential that
   acts as it until it is deleted on the account or the account is disabled, and that `rm --admin`
   never revokes; and `iam.serviceAccounts.actAs` lets a holder run a workload **as** the account with
   no binding and no `Admins` entry at all, so neither D2823's intersection nor `status` sees them.
   WU11's acceptance takes both up as `status` rows whose fix says a key survives the removal, and the
   organization policy `constraints/iam.disableServiceAccountKeyCreation` is the only control that
   closes key creation outright — the project owner's to set, not Fugaro's.
10. **In the folder model, linking resets the member's inherited policies** (§1): the project moves
   under the workspace's folder, so it loses the IAM and organization policies its old parent carried
   — a key-creation ban, a domain restriction, an allowed-locations constraint — and takes on the
   workspace folder's. `link` reports the difference it can read on both sides before the move (WU18's
   acceptance); what it cannot do is undo an inheritance it has already changed, and the SA model does
   not move the project at all.

**What is deliberately absent from this list** is anything the addendum does not change: the shared
credential pool's *intersection* — who is admitted to it, which is unchanged, though what a workspace
admin's owner role reads of the pool's keys is §2's and item 6's — the cap and its meter, the stop,
and the cross-project read are [docs/workspaces/spec.md](../workspaces/spec.md) §8's accepted risks,
stated in `SECURITY.md`'s workspaces section, and this file does not restate them.
