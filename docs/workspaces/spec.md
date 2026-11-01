# Workspaces: the spec

**Status: design only.** Nothing here is approved and no line of it is implemented. §0 is the
owner's spec as this run recorded it; the design that follows (§1 onward) is written to the
**recommended** answer of each decision in [reconciliation.md](reconciliation.md) §4 (D980–D995), and
**where the two disagree the reconciliation wins** — §0 is the record of what was asked, not of what
this design proposes. Implementation is **not** approved: the owner decides, task by task, after
[implementation-plan.md](implementation-plan.md) has been reviewed.

Every repository, person and workspace this folder's own text names is a placeholder: `owner/repo-a`,
`owner/repo-b`, `person-1`, `workspace-a`. No file here contains a credential, a key, a token, a
price or anything shaped like one. [README.md](README.md) says what in §0 is the owner's own words
and what was not recovered.

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
for this work on 2026-10-31.

- **Workspaces:** an optional grouping of Fugaro projects.
- **Single sign-on** via a workspace Firebase token exchange.
- **Shared credentials** across the workspace's projects.
- **A workspace-wide cap** (spend).

The original's worked example named a real organisation. It is `workspace-a` everywhere in this
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

- The `workspace` commands **edit local files and read** (D993). None of them writes the workspace
  node, a member's database, a secret or anything else in the cloud.
- Every change they make reaches the cloud through the next `fugaro update`, which already
  re-converges the installation and, with the policy stage the update-converge work adds, publishes
  the policy (D982). Each write command ends by printing that one command and nothing else.
- A change `fugaro update` cannot carry is a gap in `update`, not a step for the user (F13).

## 2. The workspace record

A workspace is declared in **two** places, and the second is derived from the first:

1. **In each member's own project config** (`~/.config/fugaro/projects/<name>.yaml`,
   `internal/localcfg.Config`): one key, `workspace: <name>`. It is read by every command that reads
   a project config, and it is what makes a project a member. A project with no key is not a member
   of anything, which is the state of every installation today (D981).
2. **In the workspace's own database**, at `workspaces/<name>`, published by `fugaro update`: the
   member list (each member's project name and its canonical `owner/repo` set), the workspace's
   identity provider, and the workspace's cap. This is the copy a *second* member reads when it
   needs to know who else is in the group, and it is the copy the workspace's own services read
   (D982).

The workspace name reuses the project-name grammar (`config.ProjectNameRE`, F21) rather than a second
one. A workspace's record is validated strictly and strictly decoded, like every other config and
wire format here: an unknown field is an error, and the shape has a JSON Schema in `schemas/`.

**Membership is at most one workspace per project** (D981). The key is a scalar, not a list: the cap
and the credential pool both need a unique owner for a project, and "which of N workspaces' headroom
applies" has no answer a reader can act on.

## 3. Single sign-on: the workspace identity and the token exchange

**The workspace has its own Firebase project**, created for the workspace and holding nothing else
(D980). It is the workspace's identity provider, and it is not any member's: a member's own Firebase
continues to issue that member's own tokens, unchanged.

**What a person does.** They sign in to the workspace's Firebase once — the same Firebase sign-in
methods the member projects already support (Google, email), verified against the keys Google
publishes, with the issuer and the bare-project-id audience rule `internal/credsvc/auth.go` already
implements (F4). What they hold afterwards is a **short-lived workspace token**, and it is a bearer
for exactly one thing: the exchange endpoint of a member they are already admitted to.

**The exchange** (D983, D984). To reach a member, the person presents the workspace token to that
member's own authorization server — the one the MCP design puts inside the coordinator
(`docs/mcp-server/spec.md` §3) — which:

1. verifies the workspace token against the workspace's published keys, its issuer and its audience;
2. maps the identity to the member's own permission lists (`internal/cli/user.go`: the launchers and
   operators `fugaro user` writes), and **refuses when the member's lists do not name the person**;
3. mints **the member's own token**, short-lived, with the member's own scopes and limits, exactly as
   it mints one for a sign-in that started at the member (D985).

**Nothing is shared but the sign-in.** No session is shared, no member accepts another member's
token as a bearer, and the workspace token is never a bearer for a member's API. A member that is
down does not stop a person reaching another member; a member that is removed from the workspace
stops being reachable through it, and its own sign-in keeps working.

**What is stored** (D986). Only **hashes**: at the workspace, the record of who signed in and what
was issued, in a subtree no client of the workspace's database may read (the MCP design's D922
applied to the workspace); at each member, the member's own token record, unchanged. Revocation is
one delete at the workspace, and the next exchange by that person is refused; a member's own
revocation is unchanged and independent.

**What a workspace identity may do in a member is what the member already grants** (D985). The
workspace cannot add a launcher, cannot make an operator, cannot admit an address a member's lists
do not name, and cannot widen a member's policy, caps or allow-lists. That is rule 1 of this folder
stated as a mechanism: the exchange reads the member's lists and never writes them.

## 4. Shared credentials

**One workspace bundle, one workspace service** (D987). The credentials service
(`docs/credentials-service/`) is deployed **once, in the workspace's project**, and holds the
workspace's model credentials in its one bundle secret — the same shape the owner ruled for an
installation (one Secret Manager secret, one `secretAccessor` grant). Every member's runs reach it
the way a run reaches the service today: the job's own service account presents a Google ID token,
the service verifies the signature and the `email` claim, and the run is proved by its budget
identity. The one thing that changes is that the admitted set is the **workspace's** member job
service accounts, not one installation's.

**Which credentials may be shared, and with whom** (D988). Every shared credential carries a
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

**What it bounds** (D990). The workspace cap bounds **the spend of the workspace's own credentials**.
It is *not* a bound on a member's total spend, and the design refuses to call it one: a member may
hold credentials of its own whose calls never pass the workspace. For a member that routes *every*
model call through the workspace, the cap does bound that member's whole spend — but that is a
property of how the member is configured, not a property of the cap, and the console and the docs
must say so.

**Where it is enforced** (D989). At the **workspace's credentials service**, on the calls it pays
for. That is the one hop every shared-credential call passes, so a member cannot bypass it by
choosing a different gateway, and it is the only place that sees the total. Each member's own
gateway additionally receives an **advisory headroom** — the workspace's remaining room, published
to the member's database — so a run stops early rather than being cut off mid-stage; the advisory
number is a courtesy to the run, never the enforcement.

**The ledger** (D991). The authoritative per-run ledger is unchanged: `runs/<slug>/<run>` in the
member's own database, written by the member's own gateway under the rules that already constrain it
(`NEW >= OLD`, `spent <= reserved`). The workspace's ledger is a **rollup**, recomputed by the
workspace service from each member's ledger on the history job's schedule, and the enforcement reads
the last good rollup. A member whose ledger cannot be read is a member the rollup records as
`stale` rather than as zero: an unreadable member must never look like a free one.

**The scope of the cap is stated, not implied.** `fugaro budget set --global` today means *the
project* (F10). The workspace's cap is a **fourth scope**, spelled `fugaro workspace cap set
--workspace <name>`, and the two are never spelled with the same word.

**The stop** (D992). `fugaro budget kill` gains a **workspace scope**: one command stops every
member's runs. It is the same switch the credentials service's own stop flag already is
(`fugaro budget kill --all`, the operator's answer of 2026-10-19), so an operator under pressure has
one command to remember and not two.

## 6. The command surface

`fugaro workspace` (D993), five verbs:

| Verb | What it does |
| --- | --- |
| `workspace ls [--json]` | Offline. Every workspace this machine's project configs name, with its members, and which of them this machine has a config for. Versioned JSON with a schema, like every other CLI document |
| `workspace show <name> [--json]` | Offline. One workspace: its members, its identity provider, its cap as last published, and the drift between the local configs and the published record |
| `workspace add <name>` / `workspace rm <name>` | Edits the **selected local project config** and nothing else: `workspace: <name>` on, or off. Prints the diff, backs up the file, writes atomically, and ends by printing `next: fugaro update` |
| `workspace member add <name> <project>` / `member rm …` | The same, for the member list of a workspace this machine holds a config for |
| `workspace cap set --workspace <name> --monthly <amount>` | Records the workspace's cap in the local config; `fugaro update` publishes it |

None of them writes the cloud. `workspace ls`, `workspace show`, the offline reads and the
`--json` documents run anywhere, including under a coding agent's environment; the writes are local
config edits and behave exactly as `fugaro models`' writes do — under a coding agent's marker they
print the diff and the command for the user to run, and write nothing.

## 7. The console and the mobile app

Neither reads a workspace today, because neither reads a workspace's *inputs* today either: the
console reads `fugaro config list --json` (`internal/cli/config_list.go`), which lists the
installations this machine has, and the mobile app reads the runs and the budget of one project.

**No console or mobile task is in this plan** (D994). A workspace is a group over the document the
console already reads, so a console screen is a small read of a *versioned* `fugaro workspace ls
--json` — and the design that adds it should be written after the CLI document exists, not before,
because a wire contract fixed before the shape it carries is the mistake the MCP design's §10
refuses for the roadmap scope. The mobile app reads nothing of this.

## 8. Threat model

This section is the one the owner's brief requires an independent review of, and the review is
mandatory before any decision in [reconciliation.md](reconciliation.md) §4 is answered. It is
written as the questions a reader should be able to answer from it.

**Who can reach what.** A workspace adds exactly one new reachable surface: the **exchange
endpoint**, which lives on a member (D984) and is reached with a workspace token. It adds no
endpoint to the workspace's own project, and it opens no member's API to anyone who is not already
in that member's own lists. The workspace's database holds the member list and the token record
hashes, and it is read by no client (D986).

**What a stolen workspace token reaches.** One thing: `POST <member>/oauth/token`, and only for a
member whose lists name the person, and only while it is unexpired. It is never a bearer for a
member's API, which is the whole reason the exchange exists. Its blast radius is therefore the set
of members the person was already admitted to — a theft that grants nothing the thief did not
already have, with one exception stated next.

**What a stolen workspace token reaches that the person did not.** Nothing about a member, but
something about the workspace: the **member list**, because the exchange endpoint must tell the
holder which members it may reach. So the design's rule is that the workspace token carries **no
member enumeration**: the client names the member it wants, and a member the person is not in is
refused with a refusal that is identical in shape and timing to one for a member that does not
exist. An endpoint that answered "which members are there?" would let any stolen token map the
group.

**What a stolen member token reaches.** Unchanged from today: one member, its own scopes, its own
caps, its own policy, until it expires. The workspace does not extend it, cannot mint a sibling from
it, and cannot revoke it — revocation at a member stays the member's.

**Rotation and revocation.** The workspace's refresh token rotates on every use, and a replayed
refresh token revokes the whole family (the MCP design's D930 applied here). Revocation at the
workspace ends the person's ability to *exchange*, and nothing else: a member token already minted
keeps working until it expires, which is why the member tokens stay short-lived. The honest
statement is that a workspace revocation is not instantaneous across members, and the design buys
that back with token lifetime rather than with a callback into every member.

**Shared credentials: what a compromised member reaches.** A member's job service account can call
the workspace's credentials service, and therefore spend the workspace's money and use the
credentials its allow-list admits. It cannot read a credential's value (rule 2: the service proxies
and never discloses), and it cannot reach a credential another member's allow-list admits and its
own does not (D988). The residual risk this design accepts, stated plainly: **a member's operator
who controls that member's job image can make the member's jobs call the workspace's service with
any request they like**, within the intersection above. That is the same trust the member's operator
already has over the member's own credentials today; grouping it means one member's compromise
reaches the *pool*, not just that member. It is why the allow-list is mandatory and why a shared
credential's allow-list should name the fewest members that need it.

**The workspace cap's integrity.** The cap is enforced at the service (D989), so a member cannot
bypass the *refusal*. Its *accounting*, however, rests on each member's own ledger, and a run writes
its own ledger under rules that forbid lowering it and do nothing to force it to rise (F12). A
member whose image under-reports therefore consumes more of the workspace's room than the rollup
believes. The design's answer is honest rather than complete: the rollup reads each member's ledger
*and* its runs bucket, a disagreement is recorded as `stale` for that member rather than averaged
away, and the hard stop is the workspace kill (D992) — one command, which does not depend on any
member's honesty.

**Cross-project read.** The workspace's service reads N members' databases. That is new in this
repository: every client today is built on one project's database (F20). What bounds it is that the
service reads **only** the two nodes it needs — each member's run ledger and its published headroom —
and never a member's policy, secrets or run records. A compromised workspace service therefore
learns each member's *spend*, which is the minimum the cap needs, and not each member's code, its
repositories or its credentials. The service's own credential is a read grant per member and nothing
else, and it is the one grant this design adds.

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
- **never writes a member's database** except the advisory headroom node (D989), which is a number a
  member may ignore.
- **never becomes a second permission model.** There is no workspace role that means something in a
  member; the member's own lists are the only thing that admits anyone.
- **never carries a workspace-wide repository allow-list.** The per-repository allow-list is the
  policy's and it is per project (F18); a workspace that widened it would be a policy bypass.

## 10. What this design leaves open

- **The workspace's own creation.** Whether `fugaro setup` can create a workspace's Firebase project,
  or whether it is an owner-run step beside `fugaro init`, is a task-level decision
  ([implementation-plan.md](implementation-plan.md) W3), not a design one: either way the workspace
  exists before a member joins it, and `fugaro update` converges it.
- **The exchange's exact HTTP shape** — the endpoints, the well-known documents and the error
  bodies — is the MCP design's §3 for a member, and this design reuses it rather than writing a
  second one. If the owner answers that design's D921 (the OAuth library) differently, this design
  moves with it.
- **Whether a member may be a member of a workspace whose identity provider it cannot reach** — a
  member whose network cannot reach the workspace's Firebase — is not settled here; the honest
  answer is that such a member is not a member in any useful sense, and the plan's W5 should refuse
  the state rather than half-support it.
- **The workspace's own upgrade path.** `fugaro update` converges a workspace's record, its service
  and its rules the way it converges an installation; the plan's W9 carries the release note's
  `### For operators` line, and the convergence test is that plan's, not this document's.
