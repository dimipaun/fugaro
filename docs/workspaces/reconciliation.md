# Workspaces: the reconciliation

**Status: design answered by the owner 2026-11-04; plan only; nothing implemented; no task
approved.**

Each claim of [spec.md](spec.md) §0 — the owner's spec as this run recorded it — against the code at
`origin/main` `b41709ea`, merged into this branch and read on 2026-11-04; the first reading was taken
on 2026-11-01 at `3d6ed04d`, and **§2f lists what main has overtaken since**, which is the section to
read before trusting any finding above it. Every citation is a path and a line at `b41709ea`. Each
finding is classified **Exists**, **Missing** or **Conflicts**, and states its consequence. §4 is the
decisions **D1290–D1305**, each with the recommended option first, the cost of the alternative and
**the owner's answer of 2026-11-04**; §5 is the number block and how it was read; §6 is the owner
checks no hermetic test can stand in for. **Where this file disagrees with [spec.md](spec.md), this
file wins.**

## 1. What the code has, in one paragraph

Fugaro has **projects** and no grouping of them. A machine holds many — `internal/localcfg` loads
each from `~/.config/fugaro/projects/<name>.yaml` (`localcfg.go:41`, `:1145`, `:1181`) and
`fugaro config list` lists them (`internal/cli/config_list.go:220`) for the console's picker. Each
project is one GCP project with its own Firebase, its own Realtime Database, its own caps
(`internal/budget/model.go:31-33`, `:51`), its own policy node
(`internal/projectpolicy/paths.go:18-23`, `:29`) and its own identity: the credentials service verifies a
Firebase ID token whose issuer is that project's own
(`internal/credsvc/auth.go:1072`), and since #1669 its front door has a **person class** as well as
its job classes (`internal/credsvc/auth.go:375` `PersonAccounts`, `:890` `VerifyReadCaller`, `:968`
`VerifyPersonCaller`) — still one installation's people, named in that installation's own config. The
only cross-project machinery in the tree is the *owner's* machine: `fugaro config list` reads several
local configs, and nothing else does. Identity, credentials and caps are all single-project facts,
and the workspace asks for all three to span projects at once.

## 2. The findings

### 2a. The grouping (spec §0, first bullet)

**F1 — A machine can hold many projects, and there is a read that lists them. — Exists.**
`internal/localcfg/localcfg.go:41` (`type Config`), `:1086` (`ProjectsDir`), `:1095` (`ProjectPath`),
`:1145` (`Projects`), `:1181` (`LoadProject`). `internal/cli/config_list.go:220` is `fugaro config
list`, "List the Fugaro installations this machine has", whose document type `configListDoc`
(`config_list.go:53`) is the console's installation picker read. Consequence: the *members* of a
workspace are already enumerable locally, so the workspace adds a grouping over an existing set and
not a new set.

**F2 — Nothing groups projects. — Missing.** `localcfg.Config` (`localcfg.go:41`) has no `workspace`
key, and no other key means "this project is in a group": the struct's fields are the project's own
(`Name` `:46`, `Region` `:48`, `BackendName` `:104`, `SchedulerRegion` `:125`,
`CommitName`/`CommitEmail` `:338-339`, `FirebaseProject` `:567`, and the rest). A scan of the tree returns no grouping either:
`grep -rn 'workspace' internal/ --include='*.go'` returns Bitbucket's workspace
(`internal/projectpolicy/bitbucket.go:30`, `internal/config/providers.go`) and pnpm's (an image
test), and nothing else. Consequence: §2 of the spec is entirely new surface, and its red step is
the record's own validation.

**F3 — "workspace" is a word this repository already uses for something else. — Conflicts (naming).**
`docs/bitbucket-workspace-token/spec.md` is the owner's spec for *Bitbucket* workspace-level tokens
("We need to support workspace level Bitbucket tokens"), with `internal/projectpolicy/bitbucket.go`
carrying the derived block. A Fugaro workspace and a Bitbucket workspace are unrelated, and a reader
who conflates them would expect a Fugaro workspace to hold repositories. Consequence: the design says
"Fugaro workspace" wherever the two could be confused, and the workspace's node lives in the
workspace's own database under `workspaces/`, never in a member's, so a path never reads as a
Bitbucket fact.

### 2b. Single sign-on (spec §0, second bullet)

**F4 — Identity today is per project, verified from one Firebase. — Exists.**
`internal/credsvc/auth.go:1072` compares the token's `iss` against
`"https://securetoken.google.com/" + a.cfg.FirebaseProject`; `:1373` (`GoogleKeysURL`) names the
public x509 endpoint the signing keys come from; `:767` records why the issuer is not caller-chosen
text. The mobile
app's own sign-in normalises and allow-lists an address (`internal/mobileauth/email.go`,
`mobileauth.go`). Consequence: the *verification* the workspace's exchange needs already exists and
is tested; what does not exist is a second project's willingness to accept a token from anywhere
else.

**F5 — There is no exchange endpoint on `main`, and no service that could host one. — Missing.**
The authorization server the exchange would live in is the MCP design's §3
(`docs/mcp-server/spec.md` §3), which is in **open pull request #1487** and not on `main`. The
coordinator is `ee/coordinator/`, and its P6–P8 are not started (`docs/coordinator/README.md`).
Consequence: spec §3 depends on work that does not exist yet, and
[implementation-plan.md](implementation-plan.md) §2 makes that dependency the reason for the
release slot.

**F6 — The database rules grant by project token, so another project's token is refused. —
Conflicts.** `internal/budget/rules/rules.go:55` states the policy's grant: `config/policy/shared`
"is readable by any token of the project", and `config/policy/repos/$slug` only by the token whose
`fs` claim is that slug. The caps are the same shape: `internal/budget/model.go:31-33` roots every
cap path in one project's database and `:51` (`PathCapsRepo`) is a child of it. A workspace token is
not a token of any member's project, so every member's rules refuse it — which is precisely why the
design is an **exchange** and not a pass-through. Consequence: the workspace cannot be built by
widening a member's rules; the exchange's whole purpose is that no member's rules change.

**F7 — The credentials service is one installation's, and its front door admits only that
installation's job service accounts. — Exists, and it conflicts with sharing.**
`internal/credsvc/auth.go`'s package doc states the three properties of the front door, the second of
which is that the caller's ID-token `email` claim is checked "against the installation's job service
accounts", with a shared project job refused whole until the phase-3 rules land. The bundle is ONE
Secret Manager secret with ONE `secretAccessor` grant (the owner's Q6 ruling,
`docs/credentials-service/README.md`). Consequence: a second project's jobs can neither read the
bundle nor be admitted by the service as it stands — D1297 is a second deployment, not a flag on the
existing one.

**F8 — The cascade's credential state is per installation, and it is the shape to copy. — Exists.**
`internal/creds/state.go` holds `creds/marks/<name>`, `creds/current/<slug>/<run>`,
`creds/leases/<slug>/<run>` and `creds/roundrobin` under one project's database, written only by the
service's admin account, with the rules denying every run token the subtree (C0.6, C1.3) and the
boundary held by `internal/creds/boundary_test.go`. Consequence: the workspace's shared state is the
same shape one level up and carries the same rule — a run never reads it — so the design adds no new
kind of state, only a second instance of one.

### 2c. Shared credentials (spec §0, third bullet)

**F9 — Caps are per project, and the merge only tightens. — Exists.**
`internal/budget/model.go:31` (`PathCapsGlobal`), `:32` (`PathCapsDefaults`), `:51`
(`PathCapsRepo`), `:207-208` (`GlobalCaps`), `:213-215` (`DefaultCaps`), `:220-221` (`RepoCaps`),
`:294` (`PerRun`, "the lifetime cap of a run: min(the repository's own per-run cap or the default,
the global per-run cap)"). `internal/policy/merge.go:44` (`Merge`) and `:63` (`tightenCap`) apply the
rule that "a later layer may only tighten: the smaller positive cap". Consequence: the *only-tighten*
rule the workspace cap needs already exists and is tested; what is missing is any cap whose inputs
live in two databases.

**F10 — `fugaro budget` is the operator's cap surface, and `--global` already means the project. —
Exists.** `internal/cli/budget.go:65` (`newBudgetCmd`), `:280` (`show`), `:918`
(`set (--global | --defaults | --repo [owner/name]) …`), `:1383` (`newBudgetKillCmd(kill bool)`,
one constructor for `kill (--all | --repo …)` and `resume`, which is the command D1302 grows a
workspace scope on). Consequence: the workspace's cap must be
spelled with a word that is not `global`, or an operator will read "the workspace's cap" where the
command means "this project's cap" — D1299 and D1303 name the word.

**F11 — The gateway is where a cap is enforced, and it runs inside the job. — Exists.**
`internal/gateway` is the loopback proxy that holds the model credential and enforces the budget;
`internal/budget/session.go` raises the halt reasons (`ReasonBudgetUnavailable`, the lease's
refusals) and the runner maps a session halt to a run record by the reason string alone.
Consequence: a workspace cap enforced only at a member's gateway is enforced by the member's own
process, so it cannot be a fence against the member — D1299 puts the enforcement at the service and
keeps the gateway's copy advisory.

**F12 — A run writes its own ledger, and the rules only forbid it going down. — Exists.**
`internal/budget/rules/rules.json.tmpl:60` validates `runs/$slug/$run/reserved` with
`NEW >= OLD && … NEW - OLD == D(spend/TODAY/runs/$slug/$run/reserved)`, `:64` validates `spent` with
`NEW >= OLD && NEW + N(runs/$slug/$run/spent) <= N(runs/$slug/$run/reserved)`, and `:68` does the
same for `released`. Consequence: a run cannot *lower* its recorded spend, and nothing forces it to
raise it — so the workspace cap's accounting is only as good as each member's image, and
[spec.md](spec.md) §8 states it as the cap's residual risk rather than hiding it.

### 2d. The workspace-wide cap (spec §0, fourth bullet)

**F13 — `fugaro update` is the one command that converges an installation, and it already has a
publish stage coming. — Exists.** The update path (`internal/cli/update*.go`) is the HARD RULE's one
command, and the policy stage the update-converge work adds is the models design's D787
(`docs/models-commands/spec.md` §7, F8). Consequence: the workspace node and the workspace's rules
hang on `update` (D1292) and take no new command.

**F14 — Managed projects are marked on the project itself, and the gate over them is now real. —
Exists, and a workspace is not one.** `internal/cli/managed_flag.go:134` names the label
`fugaro_managed=true` on the GCP project, and `internal/managed/` holds the plan/apply/inventory for
a project the tooling creates. Consequence: the workspace must not borrow the word "managed": a
managed project owns a runs bucket, jobs and an image, and a workspace owns none of them (spec §9).
**See §2f F23** for what landed over this since the first reading: the gate is no longer a design.

**F15 — The console's project view is `fugaro config list --json`. — Exists.**
`internal/cli/config_list.go:53` (`configListDoc`) and `:220` (`newConfigListCmd`), read by the
console's installation picker (`console/src/main/installlist.ts:71`, `DiscoveredInstallation`). Consequence: a console
workspace screen is a read of a new CLI document, which D1304 leaves to a later design.

**F16 — The subscriptions design already measures the paid unit. — Exists.**
`docs/subscriptions/spec.md:55` — "Paid tiers are priced per seat. A seat is a person who launches
jobs or signs in to the console or mobile app as operator or admin; viewers are free" — and `:57` —
"Seats are measurable without phoning home: Firebase Auth and the per-project allowlists already know
who signs in". Consequence: a workspace is a shape the existing measurement takes, not a new unit
(D1305).

**F17 — The `ee/` boundary the workspace's own services fall under. — Exists.**
`docs/ee-reorg/spec.md:7` records the standing rulings: A5 (the coordinator, command and library,
moves to the business-licensed folder), A6 (central orchestrators and the hosted MCP server start in
the business folder), A15 (spend and cost reports move), A18 (key-gated single binary). Consequence:
the workspace's credentials-service deployment and its exchange endpoint are A6/A5 shaped and land
under `ee/`, and this design adds no licence notice anywhere (it is core documentation).

**F18 — The per-repository allow-list is per project, and a workspace must not widen it. —
Missing, and refused.** `internal/projectpolicy/match.go` carries `AdmitRepo` and `NotAdmitted`, and
the patterns are a project's own policy. Consequence: spec §9 refuses a workspace-wide repository
allow-list outright; the only allow-list a workspace adds is a shared credential's, which can only
*narrow* what a member's policy already admits.

**F19 — `fugaro user` is the member list, it is local, and `update` applies it. — Exists.**
`internal/cli/user.go:73` (`newUserCmd`), whose own doc says it "changes the LOCAL config's
`terraform.launchers` / `terraform.operators` and nothing else", that the change "takes effect then
and not here", and that it "prints the invite for a person to paste". Consequence: D1295 hangs a
member's admission on exactly this list, and the exchange reads it and never writes it.

### 2e. What the workspace adds that nothing today has

**F20 — There is no cross-project read of anything. — Missing.** Every client is built on one
project's database (`internal/rtdb/client.go` takes one base URL) and one installation's credentials
(`internal/creds/`). Consequence: the workspace service is the first process in this repository that
reads more than one project, and [spec.md](spec.md) §8 bounds what it may read to each member's run
ledger and its headroom node and nothing else.

**F21 — A workspace name should reuse the project-name grammar, not invent one. — Exists.**
`internal/localcfg/localcfg.go:43`'s doc names the grammar: "the Fugaro project
(config.ProjectNameRE), which the file" carries in `name:`. Consequence: a second grammar for
workspace names would be a second thing two writers could disagree about; the plan's W1 says the
record reuses this one.

**F22 — The live checklist has no workspace check. — Missing.** `docs/gcp-live-checklist.md` carries
Checks 90–99 for the credentials service (its own rows: 90, 91, 92, 94, 95, 96, 99), and the
register's highest written number on this base is **190**; the register's rule is that check numbers
are allocated, never reused and never renumbered. Consequence: the workspace's
owner-run checks take the next free numbers at the task that writes them, and **this design claims
none** — the plan names what the owner must run and does not number it.

## 2f. What main has overtaken since the first reading

**This section is new on 2026-11-04, and it is the one to read first.** The findings above were
written against `3d6ed04d` on 2026-11-01; main has since moved to `b41709ea`, and six things landed
that touch this design. Each is classified the same way, and each states what it changes here.

**F23 — Managed mode landed, and the gate is real code. — Exists (new since the first reading).**
The gate is a value in `internal/managed` (`plan.go:217` `Gate`, `budget.go:124` `ApplyGate`), and
the CLI has **one** helper for the four steps around a write (`internal/cli/managed_gate.go:181`
`readManagedGate`, whose own doc states why it is written once rather than four times). It is called
by `fugaro config publish` (`internal/cli/config_publish_managed.go:97`), `fugaro project policy
publish` (`internal/cli/project_policy_managed.go:513`), `fugaro image refresh`
(`internal/cli/image_refresh_managed.go:115`) and `fugaro accounts add`
(`internal/cli/accounts_managed.go:86`); `fugaro update` runs the same four steps through its own
path, and `fugaro settings`' writes are local config edits. Consequence for this design: **W2's
`fugaro update` stage lands inside a gate that exists** rather than beside a design, and the
workspace's own commands are local writes of exactly the `settings`/`models` shape — so the
workspace adds **no** new command to the gated set. That is a fact in this design's favour, and
[implementation-plan.md](implementation-plan.md) W2 says so.

**F24 — The credentials service's person class landed. — Exists (new), and it re-opens D1297 and
D1298.** The front door admits **people** as a class of their own
(`internal/credsvc/auth.go:375` `PersonAccounts`, `:890` `VerifyReadCaller`, `:968`
`VerifyPersonCaller`), with the owner's ruling of **2026-11-04** recorded where the check lives: a
user account cannot mint a token for the service's own URL, so that class alone also admits the
audience `gcloud auth print-identity-token` produces (`:241` `GcloudAudience`), and the tradeoff —
that audience is not scoped to the service — is stated there rather than hidden. A person is a class
that may not overlap the job classes (`:369-375`). Consequence: **D1297's "one workspace service" is
a service with two admission lists, not one.** Its job class is the workspace's member job service
accounts; its person class has to be decided, and **D1295 answers it** — the member's own
launchers/operators lists are the whole of the admission, so the workspace's person list is a
*derived* set (the union of the members' lists, read from the members' own configs) and not a second
list an operator maintains. **D1298 is unchanged and is now checkable against real code**: a shared
credential's per-project allow-list is the same shape as the service's own class lists, and the
intersection with the member's policy is still the only-tighten rule. **Re-checked, and both
stand** — with one addition: the workspace service's person class is a new surface
[spec.md](spec.md) §8 must name, and it does (see §8's "who can reach what").

**F25 — Three commands are on main that were not. — Exists (new).** `fugaro settings`
(`internal/cli/settings.go:61` `newSettingsCmd`, with the writes G0–G2 added), `fugaro accounts`
(`internal/cli/accounts.go:25` `newAccountsCmd`, with `accounts add` under the gate) and the rigor
surface (`docs/rigor-command/`). All are registered in `internal/cli/root.go:191`. Consequence:
spec §6's `fugaro workspace` family is still a free name, and the design's two rules that touch
these — a workspace write is a **local config edit** and every write ends with `next: fugaro
update` — are now the *same* rule `settings` and `accounts add` follow, so W1 has a pattern to copy
rather than a rule to invent.

**F26 — The project image catalog is on main, and the repo-image removal is a design beside this
one. — Exists (new), and it changes nothing here.** The catalog is built and deployed
(`internal/backend/gcp/catalog.go:65` `CatalogDefinition`, `:74` `CatalogBuildSpec`;
`docs/project-image-catalog/`), and the design that removes the per-repository image is **#1676**,
which holds **D1190–D1219** — the block immediately above this design's. Consequence: a workspace
owns no image and no repository ([spec.md](spec.md) §9), so neither the catalog nor its removal
touches a single task here. It is recorded because the block arithmetic in §5 depends on it.

**F27 — `internal/runner/stray.go` is on main (#1476). — Exists (new), and it changes nothing
here.** The run's finalize now refuses to commit a file that is not text (`strayArtifact`,
`internal/runner/stray.go:24`, and `binaryScanBytes` `:17` for the test). Consequence: it is a rule
about a **run's** worktree, and a workspace launches no run and owns no checkout, so nothing in this
design touches it. It is recorded so a later reader does not read its absence from this folder as an
oversight.

**F28 — `fugaro upgrade` still exists. — Exists, and the HARD RULE is unaffected.**
`internal/cli/root.go:191` registers `newUpgradeCmd` beside `newUpdateCmd`. Consequence: `upgrade`
is the local self-upgrade (the binary and the skill pin) and is **not** a second converge path, so
D1292 and D1303 stand exactly as written: a workspace's cloud state reaches the cloud through
`fugaro update` and through nothing else. A reader who found `upgrade` and wondered whether it could
publish a workspace node has the answer here: it cannot, and the design does not add a way for it to.

## 3. What the reconciliation refuses to accept

- **That a workspace is a new kind of project.** It is a grouping over projects that exist; it owns
  no runs bucket, no job, no image and no repositories (F14, spec §9).
- **That a workspace may be built by widening a member's rules.** The rules grant by project token
  (F6), and the exchange exists so that no member's rules change.
- **That the cap can be enforced by the member being capped.** The run writes its own ledger (F12),
  so an enforcement that lives only in the run's own gateway is advice; D1299 puts it at the service.
- **That the workspace's own services are core MIT.** They are the coordinator's and the credentials
  service's, and those move under `ee/` by rulings A5 and A6 (F17).

## 4. The decisions

Sixteen decisions, **D1290–D1305**, renumbered on 2026-11-04 (they were written as D980–D995; the
mapping is in [README.md](README.md), and §5 says why the block moved). Each names the question, the
options with **the recommended one first**, the cost of the alternative, and **the owner's answer of
2026-11-04**.

**Every one is answered, and every one as recommended.** So no decision here carries a *not the
recommendation* marker — there is no deviation to record. **An answered decision is still not an
approval:** no task is approved, the owner approves each task by name, and
[spec.md](spec.md) §8's independent SECURITY review — which **the coordinator runs** — gates the
merge.

**D1290 — Where the workspace's identity lives.** A workspace needs one identity provider its members
accept. Which Firebase is it?
- **(A, recommended) The workspace's own Firebase project**, created for the workspace and holding
  nothing else.
- **(B) One member's Firebase, designated the workspace's.**
Cost of (B): the designated member becomes a privileged member — its owner holds the workspace's
whole identity, deleting or compromising it takes the workspace's sign-in with it, and its
launchers/operators lists, which today gate only itself, would silently become the workspace's
admission lists: a permission widening nobody asked for. (A) costs a new Firebase project and one
more thing `fugaro update` must converge.

**Answered 2026-11-04: (A), as recommended** — the workspace's own Firebase project. Why: the
alternative makes one member's launcher and operator lists the workspace's admission lists, which is
a widening nobody asked for.

**D1291 — May a project belong to more than one workspace?**
- **(A, recommended) At most one.** The membership is a scalar key in the project's own config.
- **(B) Any number.**
Cost of (B): "which workspace's headroom applies" has no unique answer, a shared credential's "which
workspace may this project use" has none either, and every refusal would have to name which of N
workspaces refused. (A) costs a second key the day a project genuinely needs two groups.

**Answered 2026-11-04: (A), as recommended** — at most one workspace per project, one scalar key.
Why: with N workspaces, "whose headroom applies" and "which workspace may use this credential" have
no unique answer.

**D1292 — Where the workspace record lives, and who writes it.**
- **(A, recommended) Declared in each member's local project config and published by
  `fugaro update`** to the workspace's own database, on the policy stage's pattern.
- **(B) A new `fugaro workspace publish` command writing the node.**
Cost of (B): a second command that must run after a change, which the HARD RULE in `CLAUDE.md`
forbids and which the models design already refused once (F13). (A) costs the published record being
as fresh as the last `fugaro update`, which is what every other published node already is.

**Answered 2026-11-04: (A), as recommended** — declared locally, published by `fugaro update`. Why:
the HARD RULE (`CLAUDE.md`) forbids a second command that must be run after a change, and F23 shows
the publish stage now lands inside a gate that exists.

**D1293 — What "single sign-on" is.**
- **(A, recommended) One sign-in at the workspace, one exchange per member**: a person holds a
  short-lived workspace token and exchanges it for each member's own token. No session is shared.
- **(B) Every member accepts the workspace token directly as a bearer.**
Cost of (B): one stolen token is every member at once, and each member's rules would need a grant for
a token whose issuer is not its own Firebase (F6) — a wider change to the rules than the exchange is.

**Answered 2026-11-04: (A), as recommended** — one sign-in at the workspace, one exchange per
member. Why: a stolen token that is every member at once is the one outcome the design exists to
prevent.

**D1294 — Where the exchange happens.**
- **(A, recommended) At the member**, in the member's own authorization server, which verifies the
  workspace token and mints the member's own. The member stays the authority over what the identity
  may do there.
- **(B) At the workspace**, which mints a per-member token and hands it back.
Cost of (B): the workspace service holds the ability to mint tokens for every member, so it becomes a
credential authority over them rather than an identity provider; a compromise of it is a compromise
of every member's API, and revocation needs the workspace to reach into each member.

**Answered 2026-11-04: (A), as recommended** — the exchange happens **at the member**. Why: the
member stays the authority over what the identity may do there, and the workspace never becomes a
credential authority over its members.

**D1295 — What a workspace identity may do in a member.**
- **(A, recommended) Nothing beyond what the member's own lists grant.** The exchange admits the
  person only when the member's launchers/operators list names their address.
- **(B) A workspace role that maps to a member role.**
Cost of (B): a second permission model beside the member's, and the only-tighten rule would not hold
— a workspace admin could widen a member's list without that member's owner acting. (A) costs one
`fugaro user add` per member, which is the same act as today.

**Answered 2026-11-04: (A), as recommended — nothing beyond the member's own launcher and operator
lists.** Why: a workspace role that maps to a member role is a second permission model, and the
only-tighten rule would not hold across it. F24 makes this concrete on current main: the workspace
service's *person* class is a derived read of the members' own lists, not a list of its own.

**D1296 — What is stored, and where.**
- **(A, recommended) Hashes only**, in the workspace's own subtree that no client of its database may
  read, plus each member's own unchanged record.
- **(B) The workspace stores the member tokens it minted, so a person can list their sessions.**
Cost of (B): the workspace holds a value it can present to a member — the one thing "store only
hashes" exists to prevent — and revocation would have to reach every member's store.

**Answered 2026-11-04: (A), as recommended** — hashes only, in a subtree no client reads. Why: a
stored member token is a value the workspace could present, which is exactly what hashes-only
prevents.

**D1297 — What "shared credentials" means.**
- **(A, recommended) One workspace bundle and one workspace credentials service**: the credentials
  service deployed once in the workspace's project, holding the workspace's credentials in its one
  bundle secret, proxying every member's calls, admitting each member's jobs by their verified
  service-account email.
- **(B) The bundle is copied into each member's project, each with its own service.**
Cost of (B): N copies to rotate, N places a key can leak, no single revocation, and no one hop that
sees the total, which is what the cap needs (D1299). (A) costs the workspace holding model credentials
for every member — a trust the members must accept knowingly, and the reason spec §8 is a threat
model.

**Answered 2026-11-04: (A), as recommended** — one workspace bundle and one workspace service. Why:
N copies are N rotations, N leak sites, no single revocation, and no one hop that sees the total,
which the cap needs (D1299). F24 adds the one thing the first reading did not have: that service
admits **people** as well as jobs, so its person class is a decision this design now states rather
than inherits.

**D1298 — Which credentials may be shared, and with whom.**
- **(A, recommended) Every shared credential carries a per-project allow-list**, and the effective
  set is the intersection of that list and the member's own policy; the member's policy is the
  tighter side.
- **(B) A workspace credential is available to every member.**
Cost of (B): one member's compromise reaches a credential every member's code is billed for, and a
member whose policy excludes a provider would still be served it — a policy bypass, which the
only-tighten rule forbids.

**Answered 2026-11-04: (A), as recommended** — a per-project allow-list on every shared credential,
intersected with the member's own policy. Why: the member's policy is the tighter side, and serving
past it would be the policy bypass the only-tighten rule forbids. Re-checked against main's person
class (F24) and unchanged.

**D1299 — Where the workspace cap is enforced.**
- **(A, recommended) At the workspace's credentials service**, on the calls it pays for, with an
  advisory headroom published to each member's gateway so a run stops early.
- **(B) Only at each member's gateway, from the published headroom.**
Cost of (B): the cap becomes cooperative — a member that under-reports spends past it silently
(F12), and nothing sees the total. (A) costs the service being on the critical path of every
shared-credential call, so its outage stops work; the advisory headroom and the stop flag bound that.

**Answered 2026-11-04: (A), as recommended** — enforced at the workspace service, advisory at each
member's gateway. Why: a member cannot be the fence over its own spend (F12), and the service is the
only hop that sees the total.

**D1300 — What the workspace cap bounds.**
- **(A, recommended) The spend of the workspace's own credentials**, and, for a member that routes
  every call through the workspace, that member's whole spend. The design refuses to call it a bound
  on a member's total spend.
- **(B) A bound on every member's total spend**, enforced by requiring every member to route
  everything through the workspace.
Cost of (B): it is a larger change than the workspace itself — it makes the workspace's service
mandatory for all traffic, including a member's own keys — and it would have to be stated as a
requirement on every member rather than as a property of the cap. (A) costs the honest sentence "the
cap covers what the workspace pays for", which the console must show and spec §8 must carry.

**Answered 2026-11-04: (A), as recommended** — the cap bounds the spend of the workspace's own
credentials. Why: a member may hold credentials of its own whose calls never pass the workspace, so
calling it a bound on a member's total spend would be false.

**D1301 — The workspace ledger.**
- **(A, recommended) A rollup recomputed by the workspace service from each member's own run
  ledger**, on the history job's schedule, with the enforcement reading the last good rollup.
- **(B) Each run writes the workspace ledger directly.**
Cost of (B): a cross-database write from a job that holds no grant on the workspace's database — a
new grant and a new failure mode in the run path — and a run that cannot write would halt work
rather than merely go unrolled. (A) costs the headroom being as fresh as the rollup.

**Answered 2026-11-04: (A), as recommended** — a rollup recomputed from each member's own ledger,
with an unreadable member recorded `stale` rather than zero. Why: a member nobody can read must
never look like a free one.

**D1302 — The stop.**
- **(A, recommended) `fugaro budget kill` gains a workspace scope**, one command that stops every
  member's runs, on the credentials service's own precedent (`fugaro budget kill --all` is already
  its stop flag).
- **(B) A separate `fugaro workspace kill`.**
Cost of (B): two stop flags an operator has to remember under pressure, and the workspace one would
need its own runbook line beside the other. (A) costs the kill command's surface growing a scope,
which its `--all` already models.

**Answered 2026-11-04: (A), as recommended** — `fugaro budget kill` gains a workspace scope. Why:
two stop flags are one too many for an operator to remember under pressure, and this command's
`--all` already models a scope. F10 records the constructor it grows from
(`internal/cli/budget.go:1383`).

**D1303 — The command surface.**
- **(A, recommended) `fugaro workspace ls | show | add | rm | cap`**, local writes and reads only;
  every cloud change reaches the workspace through `fugaro update`.
- **(B) A `fugaro workspace` verb set with its own publish and apply.**
Cost of (B): the HARD RULE again (D1292). (A) costs nothing beyond the commands themselves.

**Answered 2026-11-04: (A), as recommended** — `fugaro workspace ls | show | add | rm | member |
cap`, local writes and reads only. Why: the HARD RULE again, and F25 shows the local-write shape
`settings` and `accounts add` already follow, so W1 copies a pattern rather than inventing one.

**D1304 — The console and the mobile app.**
- **(A, recommended) No console or mobile task in this plan.** The console reads `fugaro config list
  --json` today; a workspace screen is a later design reading a versioned `fugaro workspace ls
  --json`.
- **(B) A console workspace screen in this plan.**
Cost of (B): a console contract (`console/src/main/installlist.ts`) fixed before the document it
carries exists — the mistake the MCP design's §10 refuses for the roadmap scope. (A) costs the
workspace being CLI-only at first.

**Answered 2026-11-04: (A), as recommended** — no console or mobile task in this plan. Why: a wire
contract fixed before the document it carries exists is the mistake the MCP design's §10 already
refuses. The console still reads only `fugaro config list --json` (F15), unchanged.

**D1305 — What a workspace is commercially.**
- **(A, recommended) Not a new licensed unit and not a new price.** The subscriptions design already
  measures the paid unit — a seat is a person who launches jobs or signs in as operator or admin,
  measured by Firebase Auth and the per-project allowlists — and a workspace is the shape that
  measurement takes when projects are grouped. The workspace's own services are `ee/` under rulings
  A5 and A6.
- **(B) The workspace is the licensed unit** (one key per workspace).
Cost of (B): it re-bases the key on a grouping the owner may create or dissolve without a billing
event, and it needs a seat-to-workspace migration for every installation that groups later. (A)
costs nothing today and leaves the choice open.

**Answered 2026-11-04: (A), as recommended — not a licence unit and not a price.** Why: the
subscriptions design already measures the paid unit (a seat, F16), and a workspace is the shape that
measurement takes when projects are grouped; re-basing the key on a grouping the owner may create or
dissolve without a billing event would need a seat-to-workspace migration for every installation
that groups later. The workspace's own services stay `ee/` by rulings A5 and A6 (F17).

## 5. The number block

**The block moved on 2026-11-04, and this is why.** The design was written on 2026-11-01 as
**D980–D995**. The next day **#1491** (`docs/binary-allow-rule`) claimed **D981–D1010**, which
overlaps it, and main itself had moved from **D900** to **D1189** — so the numbers were re-read and
the whole design moved, in order, into the block this pull request was given. The mapping is
**D980→D1290, D981→D1291, …, D995→D1305**, one to one and in order, and it is tabulated in
[README.md](README.md). Nothing about a decision changed but its number.

Read by hand at `origin/main` `b41709ea` on 2026-11-04:

| | |
| --- | --- |
| Highest number written in `docs/` on `main` | **D1189**, the reserved tail of `docs/config-management`'s block: its own test writes "D1160–D1183 are written here, and D1184–D1189 are reserved and unused" (`internal/cli/docs_config_management_test.go`:118) |
| Taken by work not on `main` | **D1190–D1219** (**#1676**, the repo-image removal design), **D1220–D1259** (the stateless-orchestrator work), **D1260–D1289** (**#1678**) |
| Still holding its block, unmerged | **D920–D934**, `docs/mcp-server` (**#1487**), which this design's SSO half depends on and which is **still not on `main`** |
| **This work's block** | **D1290–D1309** — the sixteen decisions of §4 take **D1290–D1305** |
| Reserved, unused | **D1306–D1309** |
| The next task that needs one | takes **D1310**, and re-checks the facts above rather than this conclusion |

`grep -rhoE 'D[0-9]{3,4}' docs` is not enough on its own, for the two reasons the MCP design records
and this reading confirms: it returns `D1000`, which is not a decision at all but the test name
`TestSelftestManagedSettingsDirUID1000` in
`docs/plans/2026-09-30-m9a-gateway-and-project-identity.md`:229, `:949` and `:1181`; and a concurrent
work's block is not on `main` at all — #1487's, #1676's and #1678's own READMEs, in their own diffs,
are where those numbers are written. Read with a boundary on both sides
(`[^A-Za-z0-9]D[0-9]{3,4}[^0-9]`), the highest number written on `main` is **D1189**.

**Checks.** None is claimed. The workspace's owner-run checks — creating the workspace's Firebase
project, deploying its rules, deploying its service, and the exchange end to end — take the next free
numbers at the task that writes them (F22), and [implementation-plan.md](implementation-plan.md) §7
names them without numbering them.

## 6. Owner checks no hermetic test can stand in for

| | Check | Why no test here can |
| --- | --- | --- |
| U1 | Whether a member's job service account is **admitted by the workspace's credentials service** in a real deployment: the ID-token `email` claim is what admits it, and the claim is minted by the platform, not by a fixture | The hermetic test proves the *comparison* against a fixture set; that the platform mints the claim the comparison expects is a fact about a deployed service |
| U2 | Whether the workspace's Firebase project can be created and converged by `fugaro update` without an operator step | It needs a real GCP project, a real billing account and a real apply |
| U3 | Whether the exchange's refusals are **timing-indistinguishable** for "a member you are not in" and "a member that does not exist" (spec §8) | A timing claim is a property of a running server under a real network, not of a table test |
| U4 | Whether `fugaro budget kill --workspace` actually stops a run in flight in another project | It needs two real installations and a running job |
| U5 | Whether the workspace's rollup stays `stale` rather than reading zero when a member's database is unreachable | It needs a real database made unreachable, which no hermetic fixture reproduces honestly |

## 7. UNVERIFIED

| | What | Why |
| --- | --- | --- |
| V1 | The exact wording of the owner's spec of 2026-10-24 | **The verbatim text was not recovered.** §0 of [spec.md](spec.md) is the recorded summary, and a later reader who has the original should treat it as authoritative and fix §0 |
| V2 | Whether the MCP design (**#1487**) is merged by the time this work starts | **Still open, and still not on `main`, at the 2026-11-04 reading** (`docs/mcp-server/` is absent from the tree). [implementation-plan.md](implementation-plan.md) §2 and §5 build on it as a dependency either way, and say what changes if it lands first |
| V3 | The current revision of the MCP authorization specification, and whether the exchange's flow is a conforming token exchange or a small departure | The public specification is not reachable from this container; the MCP design's §0 open item carries the same question for its own flow |
| V4 | Whether the `ee/` boundary rulings (A5, A6) have moved again by the time the workspace's services are built | `docs/ee-reorg/` is itself a plan; the workspace's service placement is read from it, not decided here |
| V5 | Whether a member may keep a credential the workspace also holds, and how the two are chosen between at a run | The design says a member's own credentials keep working (§4) and does not say which of two is picked; the plan's W6 owns the rule and this document does not guess it |
| V6 | Whether the credentials service's **person class** (F24) is stable enough to build a workspace's person admission on | It landed on 2026-11-03/04 (#1669 and #1646) and the owner's ruling that dates it is the **same day** as this design's answers; a class that is still moving is a dependency W6 must re-read rather than assume |
