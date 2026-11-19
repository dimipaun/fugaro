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
again. That is the consent's real shape, and §4.1 is its real size. The counterpart is the member
owner's own exit: `unlink` drops the authority over their member at once (§6).

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
project, and an owner's reach is wider than people and roles — §4.1 states it, caps and policy nodes
included. The phrase is true of every command the plan adds, and it must not be read as a claim that
the authority itself stops at the roster.

**Not superseded:**

- **never hands out a credential.** The rule stays as the workspaces design wrote it, about the
  workspace's *services and CLI*: no shared credential's value is returned to a run, a CLI, a person,
  an error message or a log line, and the credential service proxies the call. The addendum adds no
  exception to that — and it does not narrow the rule's honest reading either: the rule never claimed
  that an **IAM holder** cannot read a credential, and after `link` a workspace admin is one. §4.1
  states what that means, because a reader who took "never hands out a credential" for "no one in the
  workspace can read a member's secrets" would be wrong by a whole Secret Manager.
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

### A compromised workspace IAM account is the same reach held as an account, and it holds no key

The service-account fallback's identity (D2803, WU3, WU20) exists only where a member's owner linked
it through the fallback, it is **used by impersonation alone**, and it carries **no key** — WU3's
terraform test pins that the account is not the credentials service's and has none. Its token creators
are the workspace's admins (D2818, R8).

So the account's reach is a stolen workspace-admin credential's, one hop longer: whoever holds one of
those token-creator bindings — or holds write on the workspace project's IAM, which is what the
workspace project's own owners and editors hold — reaches every member linked through the account as
its owner. There is no key to steal and no credential of
the account's own to leak; the theft paths are an impersonation grant and the workspace project's IAM.
A member whose owner runs `unlink` takes the account's `roles/owner` away (D2822), and the account
holds nothing on that member afterwards.

### The workspace credentials service's account can rewrite the roster, `Admins` included

The roster now lives in the workspace's own database, at `workspaceRosters/<name>` (R5, D2817), and
two service accounts hold a get-and-update role on that database, neither of them path-scoped:

- the **workspace's credentials service**, whose custom role is `fugaroCredentialsDatabase` —
  `firebasedatabase.instances.get` and `.update`, the pair every installation's credentials service is
  given on its own project's database
  (`deploy/terraform/gcp/modules/installation/credentials.tf`);
- the **workspace's own service account**, holding `fugaroWorkspaceDatabase`, the same pair, for the
  workspace's own service (`deploy/terraform/gcp/roots/workspace/main.tf`, whose plan is pinned to that
  one account and that one grant).

A grant on a Realtime Database has no path-level scope, so either role is a whole-database read and a
whole-database write: **a compromise of either identity can rewrite the member list, the domains and
the `Admins` list whole**, and the workspace's cap, kill switch and its own meter figures beside them.

**What it cannot do is become an admin by writing the list.** D2823 is the fence: only an entry of
`Admins` that also holds the live folder-owner (or service-account token-creator) binding is
propagated or trusted, `project add` and `reconcile` use that intersection, and `status` flags both
directions of the difference (WU11 `TestStatusFlagsAdminTampering`, WU12
`TestDriftTrustsOnlyBoundAdmins`). Neither account can mint a binding: the credentials service holds
no IAM write on the workspace's project and **no write on any member at all** (D1299), and the
workspace root's plan carries its one service account and its one database grant (WU3 keeps the SA
fallback's new IAM account, D2803, distinct from both). What a compromised service *can* do is make the
workspace manage the wrong people — drop a member's entry, mark an address external, suspend or
restore the workspace's own stop — until a workspace admin reads `status`. It is a write the
intersection makes visible, not one it prevents.

### The workspace project's owners, editors and budget admins are workspace admins de facto

The workspace's own project is an installation like any other, so its project IAM reaches the
workspace's database over IAM, rules bypassed:

- its **owners and editors** can write the roster whole — `Admins` included — and can grant
  themselves the workspace account's token-creator binding on the workspace's own account. D2823's
  intersection then *trusts* them, because the binding is real: they are workspace admins with the
  authority §3 states and the workspace IAM account carries.
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
they can read of the group is the mirror, which the last paragraph of this section states — and what they can do to the
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
mirroring addresses without the mark, or a narrower reader — and it is recorded in §8 as an accepted
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
**at once, in the same command**, so the authority that reaches everything — every member's secrets,
IAM, database, bucket and logs — is gone then. What waits is the stored admin grant in every linked
member's roster and the resource-level bindings that member's own Terraform derives from its admin
list (D1940): they follow at **that member's own `fugaro update`** (D2801), and `workspace project
status` shows each member's grants still waiting as a pending row (WU11). So between the two commands
a removed admin still holds a member's bucket, job, secret and log grants — bounded by the next
update, and by nothing else.

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
3. **The workspace credentials service's account can rewrite the roster** (§3): `Admins` included, and
   the whole workspace database beside it — and the workspace's own service account, which holds the
   same get-and-update pair on that database, is the same reach. D2823's intersection is what keeps a
   rewrite out of authority, and `status` is what shows it; a compromise of either identity is a
   compromise of the workspace's own state either way.
4. **The mirror discloses the workspace's member addresses and external marks to every member's
   launchers and viewers** (§3), and to the workspace project's own people. Bounded to addresses,
   kinds and marks; no rules change narrows it.
5. **The audit trail is as tamperable as the roster** (D2811): create-only by construction, rewritable
   by whoever holds the member's database write, and cross-checked against Cloud Audit Logs rather
   than protected.
6. **The authority outlives a removal until the member's next `fugaro update`** (§6, D2818 as
   corrected), and a member that has unlinked keeps applying the workspace's stored grants until the
   workspace owner's `project rm` (D2822).
7. **A member owner's own record can make `unlink` refuse, never aim it** (R14): the recorded parent is
   untrusted input, so the failure it can cause is a denial of the move back, not a project placed in
   another workspace's folder.
8. **One consent covers an open-ended admin set** (§1): a person made a workspace admin later reaches
   every linked member without the member's owner being asked again. A member owner who wants that
   bounded has `unlink`, which is all-or-nothing for their member.

**What is deliberately absent from this list** is anything the addendum does not change: the shared
credential pool's intersection, the cap and its meter, the stop, and the cross-project read are
[docs/workspaces/spec.md](../workspaces/spec.md) §8's accepted risks, stated in `SECURITY.md`'s
workspaces section, and this file does not restate them.
