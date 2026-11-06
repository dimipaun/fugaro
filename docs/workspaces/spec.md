# Workspaces: the spec

**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task
approved.** §0 is the owner's spec as this run recorded it; the design that follows (§1 onward) is
written to the answer of each decision in [reconciliation.md](reconciliation.md) §4 (**D1290–D1305**,
the block this design was renumbered into on 2026-11-04), and **where the two disagree the
reconciliation wins** — §0 is the record of what was asked, not of what this design proposes.
**The owner answered all sixteen decisions on 2026-11-04, every one as recommended**, so every
sentence below is the design the owner answered and not a recommendation awaiting one. That is still
**not an approval**: no task is approved, the owner approves each task by name, and §8's independent
SECURITY review gates the merge.

**Every decision this file cites is answered, on 2026-11-04, and each as recommended.** A
parenthetical like "(D1293)" below is therefore a **settled** decision and not an open one;
[README.md](README.md) tabulates the sixteen with a one-line why for each, and
[reconciliation.md](reconciliation.md) §4 carries the owner's answer beside the options and the cost
of the alternative. The design's three load-bearing answers are stated where they bite: §3 (D1290,
D1293, D1294, D1295), §4 (D1297, D1298) and §5 (D1299, D1300).

Every repository, person and workspace this folder's own text names is a placeholder: `owner/repo-a`,
`owner/repo-b`, `person-1`, `workspace-alpha` (and not `workspace-a`, which is already a Bitbucket
workspace name in this repository's own examples — [README.md](README.md) says why). No file here
contains a credential, a key, a token, a price or anything shaped like one.
[README.md](README.md) says what in §0 is the owner's own words and what was not recovered.

Two rules bind everything below and are not up for a decision:

1. **A workspace never widens a member.** Every member keeps its own policy, its own caps, its own
   allow-lists and its own launcher/operator lists, and nothing a workspace holds can make a call
   possible in a member that the member's own configuration forbids. A workspace *groups*; it never
   grants. §9 states it as a prohibition.
2. **No agent handles a credential.** The workspace's services never hand a model credential or a
   forge credential to a run, to a CLI, or to a person — not as a tool result, not in an error, not
   in a log line. A run's credential reaches the run the way it reaches every run today: the
   platform mounts it into the job, or the credentials service proxies the call and never discloses
   the key. §9 states this too.

## 0. The owner's spec

**The verbatim text was not recovered.** What follows is the recorded summary of the owner's spec of
2026-10-24, exactly as this run received it; it is what this folder reconciles and designs against,
and [README.md](README.md) records what was and was not recovered. The owner approved a design PR
for this work on 2026-10-31 and **answered the sixteen decisions on 2026-11-04**, every one as
recommended ([README.md](README.md) tabulates them).

- **Workspaces:** an optional grouping of Fugaro projects.
- **Single sign-on** via a workspace Firebase token exchange.
- **Shared credentials** across the workspace's projects.
- **A workspace-wide cap** (spend).

The original's worked example named a real organisation. It is `workspace-alpha` everywhere in this
folder, and the projects in it are `owner/repo-a` and `owner/repo-b`. This design states no currency
figure of any kind: a cap is a number the owner writes into a config, and §5 says why the design
carries the *shape* of the number and not a number.

## 1. What a workspace is, and the rule this serves

A **workspace** is an optional, named grouping of Fugaro projects that share three things and
nothing else:

- **one identity** — the people who may reach the group, signed in once at the workspace's own
  Firebase and exchanged per member (§3);
- **one pool of model credentials** — the workspace's credentials, held by the workspace's
  credentials service and served to every member's runs through a per-credential allow-list (§4);
- **one spend cap** — the ceiling on what the workspace's own credentials pay for, enforced where
  those calls are paid for (§5).

It is **optional**: a project with no workspace is exactly what a project is today, and nothing in
this design changes its behaviour, its config, its database or its rules. A workspace is a
**grouping over projects that already exist**, not a new kind of project: it owns no repositories,
no runs bucket, no job, no image and no policy.

`fugaro update` is the whole upgrade (`CLAUDE.md`, HARD RULE). So:

- The `workspace` commands **edit local files and read** (D1303). None of them writes the workspace
  node, a member's database, a secret or anything else in the cloud.
- Every change they make reaches the cloud through the next `fugaro update`, which already
  re-converges the installation and, with the policy stage the update-converge work adds, publishes
  the policy (D1292). Each write command ends by printing that one command and nothing else.
- A change `fugaro update` cannot carry is a gap in `update`, not a step for the user (F13).

## 2. The workspace record

A workspace is declared in **two** places, and the second is derived from the first:

1. **In each member's own project config** (`~/.config/fugaro/projects/<name>.yaml`,
   `internal/localcfg.Config`): the membership key `workspace: <name>` (D1291), and beside it the
   member's **pin** — the workspace's Firebase project id, the issuer the member's exchange requires,
   and the workspace service account the member's own `fugaro update` binds §5's two grants to. The
   membership is a scalar; the pin is the member's own record of *which* workspace it trusts and
   *which* identity acts for it there. It is the member's and never the workspace's (W1 writes it,
   W4 and W7 read it): a record that could supply the issuer or the service account would be a record
   that could aim a member's exchange and a member's grants at a party of its choosing, which is the
   direction rule 1 does not survive. The membership key is read by every command that reads a
   project config, and it is what makes a project a member. A project with no key is not a member of
   anything, which is the state of every installation today.
2. **In the workspace's own database**, at `workspaces/<name>`, published by the workspace owner's
   `fugaro update`: the member list (each member's project name and its canonical `owner/repo` set)
   and the workspace's cap (D1292). **It holds no identity provider and names no service account** —
   both are the member's pin, so which identity a member trusts is never something the workspace
   states about a member. **It is read by the workspace's own service and by the workspace owner's
   CLI, and by nothing else.** No member reads it: a member's machine holds no grant on the
   workspace's project at all (§5), and a member's exchange verifies the pin **in its own config**
   and reads **its own** launcher/operator lists (§3). The record is the workspace's own statement
   about the group; it is not an input any member trusts. **It also does not say how to reach a
   member** — the job accounts, the Firebase project and `fp`, the database URL and the runs bucket
   are the service's own per-member table, from the workspace owner's config (§5), and the record
   only names the members.

The workspace name reuses the project-name grammar (`config.ProjectNameRE`, F21) rather than a second
one. A workspace's record is validated strictly and strictly decoded, like every other config and
wire format here: an unknown field is an error, and the shape has a JSON Schema in `schemas/`.

**Membership is at most one workspace per project** (D1291). The key is a scalar, not a list: the cap
and the credential pool both need a unique owner for a project, and "which of N workspaces' headroom
applies" has no answer a reader can act on.

## 3. Single sign-on: the workspace identity and the token exchange

**The workspace has its own Firebase project**, created for the workspace and holding nothing else
(D1290, **answered 2026-11-04**: a designated member's Firebase would silently make that member's
launcher and operator lists the workspace's admission lists). It is the workspace's identity
provider, and it is not any member's: a member's own Firebase continues to issue that member's own
tokens, unchanged.

**What a person does.** They sign in to the workspace's Firebase once — the same Firebase sign-in
methods the member projects already support (Google, email), verified against the keys Google
publishes, with the issuer and the bare-project-id audience rule `internal/credsvc/auth.go` already
implements (F4). What they hold afterwards is a **short-lived workspace token**, and it is a bearer
for exactly one thing: the exchange endpoint of a member they are already admitted to.

**The workspace token is a Firebase ID token, and three facts about those are load-bearing here.**

1. **Its audience is the bare project id**, not a URL (`docs/mcp-server/spec.md` "Firebase
   specifics"), so **one workspace token verifies at every member's exchange**. D1293's answer is
   *one sign-in at the workspace and one exchange per member* — not because the token stops at one
   member (it does not), but because **no member accepts another member's token as a bearer**: what
   each member mints is its own short-lived token, with its own scopes and limits, and the workspace
   token is never one. The containment is the **member's own lists**, not the token's audience: the
   exchange refuses unless that member's launcher/operator lists name the person, so a stolen token
   reaches only the members the person was already admitted to.
2. **A caller must present a verified address.** `email_verified` is required to be true, and a
   claim that is absent or false is a refusal — exactly the rule every class of caller to the
   credentials service already carries (`internal/credsvc/auth.go`:1018, `checkCallerToken`). An
   unverified address is an address nobody proved they own, and admitting one would let anybody who
   can register it with any identity provider the workspace's Firebase trusts become a listed
   person.
3. **A Firebase refresh token does not rotate and Firebase does not detect a replay.** The MCP
   design's D930 — rotating refresh tokens, a replayed one revoking the whole family — is a property
   of **the MCP server's own** tokens, which that design mints because a Firebase token's audience
   fails its resource checks. It does **not** apply here: the workspace's sign-in is Firebase's, and
   Firebase's refresh tokens are neither rotating nor replay-detecting. The workspace's theft
   detection is therefore the token lifetime and the member's own lists, not rotation, and §8 says so
   rather than borrowing a property this design does not have.

**The exchange** (D1293 and D1294, **both answered 2026-11-04**: one sign-in at the workspace and one
exchange per member, with the exchange happening **at the member** — fact 1 above says what that
answer is and is not). The member stays the authority over what the identity may do there. To reach
a member, the person presents the workspace token to that member's own authorization server — the
one the MCP design puts inside the coordinator (`docs/mcp-server/spec.md` §3; that design is **on
`main`** now, merged as #1487 on 2026-11-04, though none of its code is written) — which:

1. verifies the workspace token's signature against **Google's published Firebase signing keys** —
   one global set, `GoogleKeysURL` (`internal/credsvc/auth.go`:1371-1373), the same document for every
   Firebase project — and then against **the audience rule of fact 1** and against **the issuer the
   member's own config pins**, whose pair has to agree (W1: the issuer is that project's securetoken
   issuer, `:1072`), and requires `email_verified` (fact 2). Both of those values are the *member's*
   pin and never values read from the workspace's record: §2's record carries neither, so a workspace
   cannot move a member's trust by publishing a different one;
2. maps the identity to the member's own permission lists (`internal/cli/user.go`: the launchers and
   operators `fugaro user` writes), and **refuses when the member's lists do not name the person**;
3. mints **the member's own token**, short-lived, with the member's own scopes and limits, exactly as
   it mints one for a sign-in that started at the member (D1295).

**Nothing is shared but the sign-in.** No session is shared, no member accepts another member's
token as a bearer, and the workspace token is never a bearer for a member's API. A member that is
down does not stop a person reaching another member; a member that is removed from the workspace
stops being reachable through it, and its own sign-in keeps working.

**Removal takes both sides, like membership itself, and the member's side is the one that bites.**
The exchange verifies against **the pin in the member's own config** and reads **the member's own**
lists (§3 step 1, §2), so the owner removing the member from `workspaces/<name>` changes nothing at
that member's exchange: a person holding an unexpired workspace token still reaches it, because the
exchange never reads the record and the record was never an input. What ends it is the **member's own
`workspace rm`** (W1), which drops the membership key and the pin, and then there is no pinned project
or issuer left to verify against and the exchange refuses. The honest statement, which the design
states rather than implies: **a workspace removal is complete only when both sides have run** — the
owner's record no longer lists the member and the member's own config no longer names the workspace —
and until then the two sides disagree, which is exactly what `workspace show`'s drift report names
(§6, W2). A workspace cannot drop a member unilaterally, and the design does not pretend otherwise.

**What is stored** (D1296). The workspace mints no token of its own, so it stores no token of its
own: the sign-in record is **Firebase's** (the workspace's Auth user list, which is what a Firebase
identity provider is), and the tokens are the **members'**, each in the member's own token record
exactly as today. D1296's answer binds whatever a task does store — **hashes only**, never a token
value, in a subtree no client of the workspace's database may read (the MCP design's D922 applied to
the workspace) — and this design stores none of the three, so the rule is a floor and not a promise
about a node.

**Revocation waits for the token to expire, and that is stated rather than implied.** "One delete at
the workspace and the next exchange is refused" would hold only if **every member's exchange read the
workspace's revocation state** on each call. It does not: the exchange checks the signature against
Google's published keys and the audience and issuer the member's own config pins, and nothing else,
because a member holds **no grant at all** on
the workspace's project and reads no node of it (§5 states the grants this design makes, and the
reverse one is not among them). So the honest statement is that a workspace
revocation — disabling the person in the workspace's Firebase — takes effect **when the workspace
token they already hold expires** (fact 1), and that the member's own revocation is unchanged,
independent, and bounded the same way. §8 carries it as a residual risk, and the mitigation is the
token's lifetime and nothing else.

**What a workspace identity may do in a member is what the member already grants** (D1295,
**answered 2026-11-04**: option (A) — a workspace grants **nothing beyond the member's own launcher
and operator lists**, because the only-tighten rule would not survive a workspace role that maps to
a member role). The workspace cannot add a launcher, cannot make an operator, cannot admit an
address a member's lists do not name, and cannot widen a member's policy, caps or allow-lists. That
is rule 1 of this folder stated as a mechanism: the exchange reads the member's lists and never
writes them.

## 4. Shared credentials

**One workspace bundle, one workspace service** (D1297, **answered 2026-11-04** and **re-read
against main on that date**: N bundles would be N rotations, N leak sites and no single revocation —
and no one hop that sees the total, which the cap needs). The credentials service
(`docs/credentials-service/`) is deployed **once, in the workspace's project**, and holds the
workspace's model credentials in its one bundle secret — the same shape the owner ruled for an
installation (one Secret Manager secret, one `secretAccessor` grant). Every member's runs reach it
the way a run reaches the service today: the job's own service account presents a Google ID token,
the service verifies the signature and the `email` claim, and the run is proved by its budget
identity.

**It is not true that "the only thing that changes is the admitted set", and the design does not say
it.** The admitted set does change — it becomes the **workspace's** member job service accounts —
but the checks around it are pinned to one installation today, and they have to move with it:

- **The caller's member comes from the verified service account and from nowhere else.** The service
  already resolves a caller by looking the token's folded `email` up in its own list of the
  installation's job accounts (`internal/credsvc/auth.go`:727, in `Verify`), and it returns **the
  configured spelling** of the account it found (`:738`), never the claim's. That is the whole of
  what names the member. **A member name in a request body, a header or a query parameter is never
  identity** — a job's own launcher can write any header, so a member read off one would be a member
  a caller chooses.
- **The token's own pins have to admit the member.** `verifyRun` (`:1047`) refuses a token whose
  `aud` is not this service's Firebase project (`:1069`), whose `iss` is not that project's
  securetoken issuer (`:1072`), or whose `fp` is not this service's Fugaro project (`:1105`). A
  workspace-deployed service therefore has to accept **each member's** Firebase project as the issuer
  and **each member's** Fugaro project as `fp`, and hold the member it found to both: the account's
  member, the issuer's member and the `fp`'s member must be the **same** member, or the call is
  refused. That is a change to the check, not only to the list it looks the account up in, and W6
  owns it.

**The person class is the one the service already has, and who fills it, and who is refused, is the
one thing this deployment fixes.** Main's credentials service admits a **person** as a class of its
own beside the job classes —
`PersonAccounts` (`internal/credsvc/auth.go`:375) with `VerifyPersonCaller` (`:968`) for the GitHub
read mint and `VerifyReadCaller` (`:890`) for the Bitbucket read mint, both taking a token a plain
`gcloud auth print-identity-token` may have minted (`GcloudAudience`, `:258`, the owner's ruling of
2026-11-04). **Whose people they are, and what a person may read, does change here.** The
workspace's person list is neither the installation's nor the **members'**: it is the **workspace
owner's** people, which the workspace's own `fugaro update` writes from the workspace's own config
the way an installation's list is written today. Every route of the workspace's service therefore
admits **no member's person** — none is a *person of one member* here, because the list that names a
member's people is that member's own launcher/operator list, the member's own file, which the
workspace's service holds no grant to read and no member's `update` publishes into the workspace
(§2, §5). An admission the service cannot check is not an admission. The consequence is stated
rather than left to be discovered: **the workspace's own figures are the workspace owner's to read**
— by that owner's installation, whose CLI holds a credential for the workspace's project (§2, W2),
and by the owner's people through this class, and by nobody else. One member's people are therefore
never handed another member's spend, and a member has no view of its own share of the pool through
the workspace's service at all: a member-readable share is a surface this design does not add, and
[reconciliation.md](reconciliation.md) F31 records that it is not one. A person is never a way to a
credential's value (rule 2), the class rule that a person may not also be a job
(`NewAuthenticator`) holds unchanged, and W6 states the refusals route by route as tests rather than
leaving the deployment one person list.

**Which credentials may be shared, and with whom** (D1298, **answered 2026-11-04** and **re-read
against main on that date**: a workspace credential available to every member would be a policy
bypass, so the intersection is the only shape that keeps rule 1). Every shared credential carries a
**per-project allow-list** — the pattern `allow_data_to` already uses for a model route — and the
effective set is the **intersection** of:

- the workspace credential's allow-list;
- the member's own policy, which may refuse a provider or a credential the workspace holds.

The member's policy is always the tighter side, because that is what "only tighten" means
(`internal/policy/merge.go`). A credential the workspace holds and the member's policy excludes is
**not served**, and the refusal names the member's own policy as the reason, so an operator is never
sent to the workspace for a refusal the member made.

**What a run sees.** Nothing new. The run's gateway is the loopback proxy it is today; it calls the
workspace's credentials service, which holds the key and proxies the call. No run, no CLI and no
person is ever handed a shared credential's value (rule 2).

**A member may keep its own credentials.** A project that holds model credentials of its own —
today's state for every installation — keeps them, and its runs keep using them. Sharing is
additive: the workspace's pool is a set a member *may* draw on, subject to the intersection above.

## 5. The workspace cap

**What it bounds** (D1300, **answered 2026-11-04**: calling it a bound on a member's total spend
would be false for a member that holds credentials of its own). The workspace cap bounds **the spend
of the workspace's own credentials**.
It is *not* a bound on a member's total spend, and the design refuses to call it one: a member may
hold credentials of its own whose calls never pass the workspace. For a member that routes *every*
model call through the workspace, the cap does bound that member's whole spend — but that is a
property of how the member is configured, not a property of the cap, and the console and the docs
must say so.

**Where it is enforced** (D1299, **answered 2026-11-04**: a member cannot be the fence over its own
spend). At the **workspace's credentials service**, on the calls it pays for. That is the one hop every shared-credential call passes, so a member cannot bypass it by
choosing a different gateway, and it is the only place that sees the total. Each member's own
gateway additionally receives an **advisory headroom** — the workspace's remaining room, published
to the member's database — so a run stops early rather than being cut off mid-stage; the advisory
number is a courtesy to the run, never the enforcement.

**The basis is the rollup, and there is only one** (D1301, unchanged). The workspace service does
**not** meter the calls it proxies and keeps no counter of its own: the authoritative per-run ledger
is unchanged — `runs/<slug>/<run>` in the member's own database, written by the member's own gateway
under the rules that already constrain it (`NEW >= OLD`, `spent <= reserved`,
`internal/budget/rules/rules.json.tmpl`:60) — and the workspace's ledger is a **rollup** of those,
recomputed by the workspace service on the history job's schedule. The enforcement reads the **last
good rollup**. A design that also counted at the proxy would have two numbers for one spend and no
rule for which wins, which is why there is one basis and this is it.

**A `stale` member fails closed.** A member whose ledger cannot be read is recorded as `stale` rather
than as zero — an unreadable member must never look like a free one — and **that member's
shared-credential calls are refused** while it is `stale`. The refusal is the workspace's, it names
the member as unreadable, and it lifts when the rollup next reads the member. Refusing is the only
answer consistent with the rest of this folder: serving a call whose cost cannot be counted is
spending against a cap the service cannot see.

**The service keeps one table of members, and every value in it comes from the workspace owner's
config.** To resolve a caller and to read a member's ledger the service needs, per member, its **job
service accounts**, its **Firebase project id and `fp`**, its **database URL** and its **runs
bucket** — and all five are the workspace owner's own declarations, made once per member on the
owner's machine (`workspace member add`, W1) and converged by the owner's `fugaro update` (W2's stage
writes the record; W3's root of the workspace's own project and W6's service carry the table). **They
are not in
`workspaces/<name>`**: §2's record names the members and carries the cap, and it carries no identity
provider and no service account, so the member's Firebase project is not something the record states
about a member any more than the issuer is. The two directions stay separate — the **member's own**
config supplies the pin (what the member trusts), and the **workspace owner's** config supplies the
table (how the workspace reaches each member) — and neither side's value is ever taken from the
other's.

**A member the table does not bind reads `stale`, and is refused.** The record can list a member the
table has no entry for, or an entry with a value missing — a member added to the record whose owner
has not yet declared how to reach it, or one whose project moved. That member is **not** treated as a
member with no spend: the rollup records it `stale` like any other member it cannot read, and its
shared-credential calls are refused while it is (the `stale` rule above). An unbound member is an
unreadable one, and an unreadable member must never look free.

**What the service holds on a member, and what it does not.** The rollup and the enforcement need
exactly three things, and the design grants exactly those:

| On | Grant | What it reaches |
| --- | --- | --- |
| Each member's Firebase project | A **custom role holding exactly `firebasedatabase.instances.get` and `firebasedatabase.instances.update`** — the shape `fugaroCredentialsDatabase` already uses (`deploy/terraform/gcp/modules/installation/credentials.tf`:242-246) | `get` reads the member's run ledger; `update` writes the advisory headroom node |
| Each member's runs bucket | `roles/storage.objectViewer` | The rollup's second read, which is what catches a member whose ledger and its bucket disagree |
| The workspace's own project | The bundle secret's `secretAccessor`, and nothing else | §4 |

**Who applies those two bindings, and to which identity.** The member's **own** `fugaro update`
applies both — the custom role on the member's Firebase project and `objectViewer` on the member's
runs bucket — and it binds them to the workspace service account **named in the member's own config**,
beside the issuer it pins (§2). It is the member that decides which account is handed access to its
own database and its own bucket, and it takes the account from no other place: §2's record names no
service account (it names no issuer either), so a record that could name one would be a record that
could aim a member's database access at a party of its choosing — the direction rule 1 does not
survive. Nobody else can create the binding: the member owns the project and the bucket, and only its
`fugaro update` reaches them. W7 owns the file and the test.

**Those two permissions reach the whole database, and this design will not pretend otherwise.**
`credentials.tf`:201-210 is the authoritative statement: Realtime Database IAM has **no path-level
scope**, so a binding is per instance and reaches the whole database — `instances.get` is "read any
node of the instance", the only read permission there is, and `instances.update` is "write any node
of the instance", the only write permission. A condition cannot fence it either: RTDB "cannot express
a path prefix in IAM" (`credentials.tf`:251). So the grant is **whole-database read and whole-database
write on every member**, and "the ledger and the headroom" is what the service's **code** does with
it.

**That has a consequence for rule 1, and it is the sharpest thing in this section.** A write to any
node includes a write to `config/caps/global` — `credentials.tf`:209-213 says so in as many words:
`instances.update` "is what `roles/firebasedatabase.admin` carries for the budget admins who 'write
/config over IAM'". So **the workspace service's grant can lift a member's cap**, and rule 1's "a
workspace never widens a member" is therefore a property of the **service's code**, exactly as
`deploy/terraform/gcp/modules/firebase/iam.tf`:75 says of the history job's own write to `/config`:
"its code never writes /config, **which IAM can't enforce**". The design states it here rather than
leaving it to be discovered, and it is residual risk 5 in §8.

**What `admin` would add is worse, and the narrow role is still the right one.**
`roles/firebasedatabase.admin` carries `instances.list` and the instance **lifecycle** —
`create`, `delete`, `disable`, `reenable` and `undelete` (`credentials.tf`:215-219, and
`deploy/terraform/testdata/custom-role-permissions.txt` is the whole set a custom role may hold). A
component that holds every model credential must not be able to **delete or disable a member's
database**. The narrow role buys that, and it does not buy the cap-lifting back — nothing does, short
of the alternative below.

**The service holds no grant on a member's policy, a member's secrets, a member's job or a member's
project IAM**, and **a member holds no grant at all on the workspace's project and reads no node of
its database** — which is why §3's revocation is bounded by the token's lifetime rather than by a
read of the workspace's state, and why §2's record is read by the workspace's own service and the
owner's CLI and by nobody else.

**The headroom stays in the member's database, and moving it is an owner decision.**
Writing the headroom is the only reason the service needs `instances.update` on a member at all. If
the headroom moved into the **workspace's** database, the service's grant on every member could drop
to `instances.get` alone and it would hold **no write** on any member — which would make rule 1 a
property of the grant rather than of the code, and is the one change this round found that would
buy that. The cost is the other direction: a member's gateway would need read access to the
workspace's database, that read is whole-database too, and every member would therefore read the
workspace's member list and its rollup. **The design keeps the headroom where it is** — one's own
headroom in one's own project is the smaller grant, and it is what makes §9's "never writes a
member's database" a rule about one node rather than about the whole database —
[reconciliation.md](reconciliation.md) §8 records the trade as an owner decision, because it decides
which data each side sees and not a detail a task may settle.

**The scope of the cap is stated, not implied.** `fugaro budget set --global` today means *the
project* (F10). The workspace's cap is a **fourth scope**, spelled `fugaro workspace cap set
--workspace <name>`, and the two are never spelled with the same word.

**The stop** (D1302). `fugaro budget kill` gains a **workspace scope**: one command stops every
member's runs. It is the same switch the credentials service's own stop flag already is
(`fugaro budget kill --all`, the operator's answer of 2026-10-19), so an operator under pressure has
one command to remember and not two. **Its scope is exactly this:** it stops the runs of every member
of the workspace, and it makes the workspace's credentials service refuse every shared-credential
call. It does **not** touch a member's own credentials, a member's own budget or caps, a member's own
sign-in, or a member's runs that never touch the workspace — a stop that reached any of those would
be a workspace reaching into a member, which §9 forbids.

**An owner decision this round did not take.** Metering the workspace's spend at the proxy — the
service counting each call it pays for, instead of reading the members' ledgers — is the other
possible basis, and it is **not** this design's. It would change D1301's answer, so it is reported
here as an owner decision to revisit rather than taken: [reconciliation.md](reconciliation.md) §8
carries it, and D1301 stands as answered until the owner says otherwise.

## 6. The command surface

`fugaro workspace` (D1303), five verbs:

| Verb | What it does |
| --- | --- |
| `workspace ls [--json]` | Offline. Every workspace this machine's project configs name, with the members it can see **from its own files** — the owner's machine, which holds the record it publishes, sees the whole list, and a member's sees the memberships its own configs name — and which of them this machine has a config for. Versioned JSON with a schema, like every other CLI document |
| `workspace show <name> [--json]` | Offline. One workspace from **this machine's own files**: the configs on it and the pin each carries, its cap as last published here, and the drift between what this machine holds and what it last knew the record to say. It reads no cloud node (§2: a member holds no grant on the workspace's project), so on a member it shows the member's own half and says so rather than printing an empty member list; the workspace owner's machine, which holds the record it publishes, shows the whole of it |
| `workspace add <name>` / `workspace rm <name>` | Edits the **selected local project config** and nothing else: the membership key `workspace: <name>` **and the pin beside it** — the three values of §2, which are the one place a member decides what it trusts — on, or off. Prints the diff, backs up the file, writes atomically, and ends by printing `next: fugaro update` |
| `workspace member add <name> <project>` / `member rm …` | The same, for the member list of a workspace this machine holds a config for — the owner's side of a membership. `member add` is also where the owner records the five per-member values §5's service table is built from (the member's job accounts, its Firebase project and `fp`, its database URL and its runs bucket), because the owner is the side that declares them |
| `workspace cap set --workspace <name> --monthly <amount>` | Records the workspace's cap in the local config; `fugaro update` publishes it |

**`workspace show` reads files and never a cloud node**, and what "drift" means follows from that.
On the **workspace owner's** machine, which holds the record it publishes, drift is W2's two-sided
membership check in full: a project the record lists that does not name the workspace, and a project
that names the workspace and is not listed — both printed, neither half silently winning. On a
**member's** machine the record is not readable at all, so the drift it can report is exactly that:
the member's config names the workspace and carries its pin, and `workspace show` says the group's own
record cannot be read from here rather than printing an empty one or an invented one. A member's half
is the only half a member holds, and the command says so instead of guessing.

None of them writes the cloud. `workspace ls`, `workspace show`, the offline reads and the
`--json` documents run anywhere, including under a coding agent's environment; the writes are local
config edits and behave exactly as `fugaro models`' and `fugaro settings`' writes do — under a
coding agent's marker they print the diff and the command for the user to run, and write nothing
(`internal/cli/managed_gate.go`:181 `readManagedGate`, the one helper the cloud writes on main
already share: `config publish`, `project policy publish`, `image refresh` and `accounts add`).
`fugaro workspace` adds no new caller of the gate, because it has nothing to gate — its writes never
leave the machine. **Two writes** do go to the cloud, and both are stages of `fugaro update`, which
already reads the gate directly for its own stages (`internal/cli/update_managed.go`:217 calls
`readManagedGate`), so neither needs a path of its own: **W2's publish of `workspaces/<name>`**, on
the owner's side, which runs under the gate `update` already opens; and **W7's two grants on a
member**, which the gate **refuses** in a managed project — an IAM binding is a loosening, and the
cover never admits one (`internal/infra/tf/cover.go`:860-867) — so that stage falls back to its own
typed confirmation and is run by the member's owner at their own terminal (W7). The distinction is
the whole reason the two are stated apart: a plan can be read for a document, and it cannot be read
for an access grant.

## 7. The console and the mobile app

Neither reads a workspace today, because neither reads a workspace's *inputs* today either: the
console reads `fugaro config list --json` (`internal/cli/config_list.go`), which lists the
installations this machine has, and the mobile app reads the runs and the budget of one project.

**No console or mobile task is in this plan** (D1304). A workspace is a group over the document the
console already reads, so a console screen is a small read of a *versioned* `fugaro workspace ls
--json` — and the design that adds it should be written after the CLI document exists, not before,
because a wire contract fixed before the shape it carries is the mistake the MCP design's §10
refuses for the roadmap scope. The mobile app reads nothing of this.

## 8. Threat model

**This section needs an independent SECURITY review before this design merges, and the coordinator
runs that review** — not this folder's author, and not the runs that implement anything. The owner's
brief first put the review before the *decisions* in [reconciliation.md](reconciliation.md) §4; the
owner answered all sixteen on 2026-11-04, so the gate the review now holds is the **merge**, and
[README.md](README.md) and [implementation-plan.md](implementation-plan.md) §1 say the same. It is
written as the questions a reader should be able to answer from it.

**Who can reach what.** A workspace adds **three** reachable surfaces, and an earlier draft of this
section named only the first — which is the kind of omission a threat model cannot afford:

1. **The exchange endpoint**, on each member (D1294), reached with a workspace token. It opens no
   member's API to anyone who is not already in that member's own lists.
2. **The workspace's credentials service**, in the workspace's project, reached by every member's job
   service account with a Google ID token (§4), and — for the read mint, not for a credential — by a
   person of the **workspace owner's** people, which is the whole of the class it admits (§4: no
   route admits a member's person, so a member's people reach no figure of the workspace's). This is
   the surface that spends the workspace's money, and it is reachable by anything holding one of the
   member job service accounts.
3. **The workspace's own Firebase**, which is a public sign-in surface: anybody who can reach it may
   attempt to sign in, and Firebase decides whether they get a token. What that token is worth is the
   member's lists (§3), so the sign-in surface itself grants nothing — but it is a surface, and a
   threat model that did not name it would be describing a smaller system than the one being built.

It adds **no endpoint to a member's own API** and no endpoint to the workspace's project other than
the credentials service's. The workspace's database holds the member list and whatever the rollup
writes; its readers are the workspace's own service and the workspace owner's CLI, **no member reads
it** (§2), and D1296's rule keeps whatever a task stores in its token subtree shut to every client of
the workspace's own database.

**What a stolen workspace token reaches.** One thing: `POST <member>/oauth/token`, and only for a
member whose lists name the person, and only while it is unexpired. It is never a bearer for a
member's API, which is the whole reason the exchange exists. Its blast radius is therefore the set
of members the person was already admitted to — a theft that grants nothing the thief did not
already have, with one exception stated next.

**What a stolen workspace token reaches that the person did not.** Nothing about a member, and
nothing about the group: the **member list** is the one thing an exchange endpoint could be built to
disclose, by answering "which members may I reach?" — and the design forbids it. The workspace token
carries **no member enumeration**: the client names the member it wants, and a member the person is
not in is refused with a refusal that is identical in shape and timing to one for a member that does
not exist. An endpoint that answered "which members are there?" would let any stolen token map the
group, and it would also be the one member-side reader of the workspace's own record, which §2 says
no member has.

**What a stolen member token reaches.** Unchanged from today: one member, its own scopes, its own
caps, its own policy, until it expires. The workspace does not extend it, cannot mint a sibling from
it, and cannot revoke it — revocation at a member stays the member's.

**Rotation and revocation.** **There is no rotation here, and the design does not claim one.** The
MCP design's D930 — a refresh token that rotates on every use, and a replayed one that revokes the
whole family — is a property of the tokens **that design mints**, and it is not a property of
Firebase's: a Firebase refresh token neither rotates nor detects a replay, and this design's
workspace token is Firebase's (§3, fact 3). A theft of the workspace token is therefore **not
detectable by rotation**; what bounds it is that the token is a bearer for the exchange and for
nothing else, that the exchange refuses anyone a member's lists do not name, and that the token is
short-lived.

**Revocation at the workspace ends the person's ability to *exchange*, and nothing else**, and it is
bounded by the token's lifetime rather than instantaneous: the exchange checks the signature against
Google's published keys, the audience and the issuer the member's own config pins, and nothing
else, because no member holds a grant on the workspace's project (§5). A member token already minted
keeps working until it expires, which is why member
tokens stay short-lived. The honest statement is that **neither revocation is instantaneous across
members**, and the design buys both back with token lifetime rather than with a callback into every
member or a read of the workspace's revocation state on every exchange.

**Shared credentials: what a compromised member reaches.** A member's job service account can call
the workspace's credentials service, and therefore spend the workspace's money and use the
credentials its allow-list admits. It cannot read a credential's value (rule 2: the service proxies
and never discloses), and it cannot reach a credential another member's allow-list admits and its
own does not (D1298). The residual risk this design accepts, stated plainly: **a member's operator
who controls that member's job image can make the member's jobs call the workspace's service with
any request they like**, within the intersection above. That is the same trust the member's operator
already has over the member's own credentials today; grouping it means one member's compromise
reaches the *pool*, not just that member. It is why the allow-list is mandatory and why a shared
credential's allow-list should name the fewest members that need it.

**The workspace cap's integrity.** The cap is enforced at the service (D1299), so a member cannot
bypass the *refusal*. Its *accounting*, however, rests on each member's own ledger, and a run writes
its own ledger under rules that forbid lowering it and do nothing to force it to rise (F12). A
member whose image under-reports therefore consumes more of the workspace's room than the rollup
believes. The design's answer is honest rather than complete: the rollup reads each member's ledger
*and* its runs bucket, a disagreement is recorded as `stale` for that member rather than averaged
away, **a `stale` member's shared-credential calls are refused while it is `stale`** (§5 — the
service fails closed, because a call whose cost cannot be counted is spending against a cap the
service cannot see), and the hard stop is the workspace kill (D1302) — one command, which does not
depend on any member's honesty.

**Cross-project read.** The workspace's service reads and writes N members' databases, and its grant
is **wider than the nodes it uses**. That is new in this repository: every client today is built on
one project's database (F20). The honest statement of what the service holds is §5's table, and the
honest statement of what that means is this:

- **IAM on a Realtime Database is whole-database, and it bypasses the rules.**
  `deploy/terraform/gcp/modules/firebase/iam.tf`:52 says of the
  admin grant that it "writes /config over IAM, which bypasses the rules", and
  `credentials.tf`:251 says why the shape cannot be narrowed: RTDB "cannot express a path prefix in
  IAM". So the custom role's `firebasedatabase.instances.get` reads **every node** of a member's
  database, and its `instances.update` writes **every node**. "Only the ledger and the headroom" is a
  property of the service's **code**, not of its grant, and no IAM policy enforces it.
- **A compromised workspace service therefore reads a member's whole database** — its policy, its
  budget and caps, its run records, its outcomes — and reads the member's runs bucket, which is where
  the run's own artifacts and logs live. It learns each member's *spend* **and a great deal more**,
  and it can write **any node** of a member's database — the member's rules do not stand in its way,
  because IAM is what the account is subject to and the rules are what a *client* is.
- **It can lift a member's cap**, and the design says so rather than claiming IAM stops it: a write
  to any node is a write to `config/caps/global` (`credentials.tf`:209-213), so rule 1's "a workspace
  never widens a member" is a property of the service's **code** and not of its grant. Residual risk
  5 below.
- **What it still cannot do** is what the service's code and the members' own projects keep out of
  reach: it cannot read a credential's **value** (rule 2 — the bundle is in Secret Manager, not the
  database, and the service proxies calls), it cannot read a member's **secrets**, it cannot reach a
  member's **project IAM** or its job, and it cannot **delete or disable a member's database** — that
  needs the instance lifecycle, which the narrow role does not carry and `admin` does (§5).
- **A member's runs bucket read is the same shape**: `roles/storage.objectViewer` on the bucket is
  every object in it, not the ledger file the rollup wants.

This is the design's largest accepted exposure, it is stated rather than argued away, and it is
carried in the residual risks below. The mitigation that is real is the one in §5: the narrow custom
role instead of `admin`, which keeps the instance lifecycle — deleting or disabling a member's
database — out of the service's reach, and a reviewable, small service. A member's own database rules
are **not** a mitigation against this account: IAM bypasses them.

**Injection into the workspace service.** The service parses untrusted text on every call: a member
name, a run identifier, a bearer token. The rules are the ones the MCP design already states — a
token's content is never echoed, a member name is validated against the workspace's own record
rather than concatenated into a path, and a run identifier is resolved through the member's own run
list rather than used as a key.

**An unattended agent as a caller.** An agent's workspace token is admitted exactly as a person's, on
the same lists, and the exchange cannot widen it. The exchange's own limits are a member's, not the
workspace's: an agent that should only ever run in `owner/repo-a` is bounded by that member's own
limits and by nothing the workspace adds. The design therefore has no workspace-level token limit to
get wrong, which is deliberate — a second limit beside the MCP design's would be a second place a
ceiling is decided.

**What this design accepts, as residual risk.**

1. A member's operator can spend the workspace's pool within the intersection of the credential's
   allow-list and the member's policy, and can under-report its own spend to the rollup. Mitigation:
   the allow-list, the `stale` marker, the kill. Not elimination.
2. A workspace revocation does not reach a member token already minted. Mitigation: short token
   lifetime.
3. The workspace's identity provider is a single point of *sign-in*: if it is down, no one signs in
   through the workspace. Mitigation: each member's own sign-in is untouched, so a member remains
   reachable the way it is today.
4. The workspace service is on the critical path of every shared-credential call. Mitigation: the
   advisory headroom lets a run stop early, and the runbook's stop flag is one command.
5. **A compromised workspace service can write any node of any member's database**, including
   `config/caps/global`: its grant is whole-database write, IAM cannot scope it and IAM bypasses the
   rules (§5, §8). Rule 1 — a workspace never widens a member — is therefore enforced by the
   service's **code** and by nothing else. Mitigation: the narrow custom role instead of `admin`, so
   it cannot delete or disable a member's database; the service's code, which writes one node; and
   the workspace kill. **Not elimination, and not a claim that IAM or a member's rules stop it.**
6. **A Firebase Auth administrator of the workspace's project can mint a verified token for any
   address a member's lists name**, and therefore reach that member as that person. That is not a
   widening this design introduces — it is what any identity provider is — but it is the trust the
   workspace's Firebase now carries for every member at once, and a member's own Firebase does not
   carry it for another member. Mitigation: the workspace's project is the workspace's own, with its
   own IAM, and the member's lists still decide who is admitted; a member that will not extend that
   trust to a workspace's administrator should not join the workspace, and it says which identity
   provider it trusts in **the pin §2 puts in its own config** — the project id and the issuer there
   are the member's, and the workspace's record carries neither. (The exchange's `email_verified`
   requirement, §3 fact 2, is not a mitigation against this administrator: setting that flag is
   exactly what an Auth administrator can do. It is there because an unverified address is one nobody
   proved they own.)

## 9. What a workspace never does

This is rule 1 and rule 2 written as prohibitions. A workspace:

- **never widens a member.** It cannot add a launcher or an operator, admit an address a member's
  lists do not name, raise a member's cap, run a mode a member's policy forbids, or serve a
  credential a member's policy excludes. It cannot make a repository reachable in a member that the
  member's own policy excludes.
- **never hands out a credential.** No shared credential's value is returned to a run, a CLI, a
  person, an error message or a log line. The service proxies the call; the key stays with the
  service.
- **never owns a repository, a runs bucket, a job or an image.** A workspace is a grouping over
  projects that already exist; it holds no runs and launches nothing.
- **never writes a member's database** except the advisory headroom node (D1299), which is a number a
  member may ignore.
- **never becomes a second permission model.** There is no workspace role that means something in a
  member; the member's own lists are the only thing that admits anyone.
- **never carries a workspace-wide repository allow-list.** The per-repository allow-list is the
  policy's and it is per project (F18); a workspace that widened it would be a policy bypass.

## 10. What this design leaves open

- **The workspace's own creation.** Whether `fugaro setup` can create a workspace's Firebase project,
  or whether it is an owner-run step beside the installation's own first-run command, is a task-level
  decision
  ([implementation-plan.md](implementation-plan.md) W3), not a design one: either way the workspace
  exists before a member joins it, and `fugaro update` converges it.
- **The exchange's exact HTTP shape** — the endpoints, the well-known documents and the error
  bodies — is the MCP design's §3 for a member, and this design reuses it rather than writing a
  second one. If the owner answers that design's D921 (the OAuth library) differently, this design
  moves with it.
- **#1487's authorization server.** The exchange endpoint of §3 *is* the MCP design's authorization
  server. That design **landed on `main` on 2026-11-04** (merged as #1487, `docs/mcp-server/`), so
  the interface this design was written to is now a settled document rather than an open pull
  request — but **none of its code is written**: its plan's tasks M1–M10 are all unbuilt, and its
  M5 is gated on D921's library licence being confirmed. This design's SSO half therefore still
  cannot be built until that work is, and [implementation-plan.md](implementation-plan.md) §2 and §5
  carry that as the dependency that forces the release slot. Nothing here duplicates it: this design
  adds a workspace token and a workspace identity provider, and reuses that server as the member's
  exchange.
- **Where the advisory headroom lives.** §5 states the trade: in the member's database (this
  design's reading) the service needs `instances.update` on a member, so rule 1 rests on the
  service's code; in the workspace's database the service needs only `instances.get`, but every
  member's gateway gains a whole-database read of the workspace. It is an owner decision
  ([reconciliation.md](reconciliation.md) §8), not a task's, because it decides which data each side
  sees.
- **Whether the workspace's cap should be metered at the proxy instead of rolled up.** §5 states that
  the basis is D1301's rollup and that a `stale` member is refused. Metering each call the service
  pays for is the other possible basis, and it would change D1301's answer, so it is reported as an
  owner decision ([reconciliation.md](reconciliation.md) §8) rather than taken.
- **Whether a member may be a member of a workspace whose identity provider it cannot reach** — a
  member whose network cannot reach the workspace's Firebase — is not settled here; the honest
  answer is that such a member is not a member in any useful sense, and the plan's W5 should refuse
  the state rather than half-support it.
- **The workspace's own upgrade path.** `fugaro update` converges a workspace's record, its service
  and its rules the way it converges an installation; the plan's W9 carries the release note's
  `### For operators` line, and the convergence test is that plan's, not this document's.
