# Workspaces: the reconciliation

Each claim of [spec.md](spec.md) §0 — the owner's spec as this run recorded it — against the code at
`origin/main` `3d6ed04d`, read on 2026-11-01, and against the 24 pull requests open at that moment.
Every citation is a path and a line at that commit. Each finding is classified **Exists**,
**Missing** or **Conflicts**, and states its consequence. §4 is the decisions **D980–D995**, each
with the recommended option first and the cost of the alternative; §5 is the number block and how it
was read; §6 is the owner checks no hermetic test can stand in for. **Where this file disagrees with
[spec.md](spec.md), this file wins.**

## 1. What the code has, in one paragraph

Fugaro has **projects** and no grouping of them. A machine holds many — `internal/localcfg` loads
each from `~/.config/fugaro/projects/<name>.yaml` (`localcfg.go:40`, `:984`, `:1020`) and
`fugaro config list` lists them (`internal/cli/config_list.go:219`) for the console's picker. Each
project is one GCP project with its own Firebase, its own Realtime Database, its own caps
(`internal/budget/model.go:31-33`, `:51`), its own policy node
(`internal/projectpolicy/paths.go:19-22`, `:29`) and its own identity: the credentials service verifies a
Firebase ID token whose issuer is that project's own
(`internal/credsvc/auth.go:541`). The only cross-project machinery in the tree is the *owner's*
machine: `fugaro config list` reads several local configs, and nothing else does. Identity,
credentials and caps are all single-project facts, and the workspace asks for all three to span
projects at once.

## 2. The findings

### 2a. The grouping (spec §0, first bullet)

**F1 — A machine can hold many projects, and there is a read that lists them. — Exists.**
`internal/localcfg/localcfg.go:40` (`type Config`), `:950` (`ProjectsDir`), `:959` (`ProjectPath`),
`:984` (`Projects`), `:1020` (`LoadProject`). `internal/cli/config_list.go:219` is `fugaro config
list`, "List the Fugaro installations this machine has", whose document type `configListDoc`
(`config_list.go:42`) is the console's installation picker read. Consequence: the *members* of a
workspace are already enumerable locally, so the workspace adds a grouping over an existing set and
not a new set.

**F2 — Nothing groups projects. — Missing.** `localcfg.Config` (`localcfg.go:40`) has no `workspace`
key, and no other key means "this project is in a group": the struct's fields are the project's own
(`Name` `:44`, `Region` `:47`, `BackendName` `:80`, `SchedulerRegion` `:101`, `CommitName`/`CommitEmail`
`:239`, `FirebaseProject` `:440`, and the rest). A scan of the tree returns no grouping either:
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
`internal/credsvc/auth.go:541` compares the token's `iss` against
`"https://securetoken.google.com/" + a.cfg.FirebaseProject`; `:842` names the public x509 endpoint
the signing keys come from; `:453` records why the issuer is not caller-chosen text. The mobile
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
Conflicts.** `internal/budget/rules/rules.go:53-57` states the policy's grant: `config/policy/shared`
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
bundle nor be admitted by the service as it stands — D987 is a second deployment, not a flag on the
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
(`PathCapsRepo`), `:207` (`GlobalCaps`), `:213` (`DefaultCaps`), `:220` (`RepoCaps`), `:288`
(`PerRun`, "the lifetime cap of a run: min(the repository's own per-run cap or the default, the
global per-run cap)"). `internal/policy/merge.go:44` (`Merge`) and `:63` (`tightenCap`) apply the
rule that "a later layer may only tighten: the smaller positive cap". Consequence: the *only-tighten*
rule the workspace cap needs already exists and is tested; what is missing is any cap whose inputs
live in two databases.

**F10 — `fugaro budget` is the operator's cap surface, and `--global` already means the project. —
Exists.** `internal/cli/budget.go:66` (`budget`), `:243` (`show`), `:881`
(`set (--global | --defaults | --repo [owner/name]) …`). Consequence: the workspace's cap must be
spelled with a word that is not `global`, or an operator will read "the workspace's cap" where the
command means "this project's cap" — D989 and D993 name the word.

**F11 — The gateway is where a cap is enforced, and it runs inside the job. — Exists.**
`internal/gateway` is the loopback proxy that holds the model credential and enforces the budget;
`internal/budget/session.go` raises the halt reasons (`ReasonBudgetUnavailable`, the lease's
refusals) and the runner maps a session halt to a run record by the reason string alone.
Consequence: a workspace cap enforced only at a member's gateway is enforced by the member's own
process, so it cannot be a fence against the member — D989 puts the enforcement at the service and
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
hang on `update` (D982) and take no new command.

**F14 — Managed projects are marked on the project itself. — Exists, and a workspace is not one.**
`internal/cli/managed_flag.go:128` names the label `fugaro_managed=true` on the GCP project, and
`internal/managed/` holds the plan/apply/inventory for a project the tooling creates. Consequence:
the workspace must not borrow the word "managed": a managed project owns a runs bucket, jobs and an
image, and a workspace owns none of them (spec §9).

**F15 — The console's project view is `fugaro config list --json`. — Exists.**
`internal/cli/config_list.go:42` (`configListDoc`) and `:216` (`newConfigListCmd`), read by the
console's installation picker (`console/src/main/installlist.ts:71`). Consequence: a console
workspace screen is a read of a new CLI document, which D994 leaves to a later design.

**F16 — The subscriptions design already measures the paid unit. — Exists.**
`docs/subscriptions/spec.md:55` — "Paid tiers are priced per seat. A seat is a person who launches
jobs or signs in to the console or mobile app as operator or admin; viewers are free" — and `:57` —
"Seats are measurable without phoning home: Firebase Auth and the per-project allowlists already know
who signs in". Consequence: a workspace is a shape the existing measurement takes, not a new unit
(D995).

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
and not here", and that it "prints the invite for a person to paste". Consequence: D985 hangs a
member's admission on exactly this list, and the exchange reads it and never writes it.

### 2e. What the workspace adds that nothing today has

**F20 — There is no cross-project read of anything. — Missing.** Every client is built on one
project's database (`internal/rtdb/client.go` takes one base URL) and one installation's credentials
(`internal/creds/`). Consequence: the workspace service is the first process in this repository that
reads more than one project, and [spec.md](spec.md) §8 bounds what it may read to each member's run
ledger and its headroom node and nothing else.

**F21 — A workspace name should reuse the project-name grammar, not invent one. — Exists.**
`internal/localcfg/localcfg.go:42`'s doc names the grammar: "the Fugaro project
(config.ProjectNameRE), which the file" carries in `name:`. Consequence: a second grammar for
workspace names would be a second thing two writers could disagree about; the plan's W1 says the
record reuses this one.

**F22 — The live checklist has no workspace check. — Missing.** `docs/gcp-live-checklist.md` carries
Checks 90–99 for the credentials service and 170–179 for subscriptions; the register's rule is that
check numbers are allocated, never reused and never renumbered. Consequence: the workspace's
owner-run checks take the next free numbers at the task that writes them, and **this design claims
none** — the plan names what the owner must run and does not number it.

## 3. What the reconciliation refuses to accept

- **That a workspace is a new kind of project.** It is a grouping over projects that exist; it owns
  no runs bucket, no job, no image and no repositories (F14, spec §9).
- **That a workspace may be built by widening a member's rules.** The rules grant by project token
  (F6), and the exchange exists so that no member's rules change.
- **That the cap can be enforced by the member being capped.** The run writes its own ledger (F12),
  so an enforcement that lives only in the run's own gateway is advice; D989 puts it at the service.
- **That the workspace's own services are core MIT.** They are the coordinator's and the credentials
  service's, and those move under `ee/` by rulings A5 and A6 (F17).

## 4. The decisions

Sixteen decisions, **D980–D995**. Each names the question, the options with **the recommended one
first**, and the cost of the alternative. Every one is **open**: the owner has answered none of them,
and an answer is not an approval.

**D980 — Where the workspace's identity lives.** A workspace needs one identity provider its members
accept. Which Firebase is it?
- **(A, recommended) The workspace's own Firebase project**, created for the workspace and holding
  nothing else.
- **(B) One member's Firebase, designated the workspace's.**
Cost of (B): the designated member becomes a privileged member — its owner holds the workspace's
whole identity, deleting or compromising it takes the workspace's sign-in with it, and its
launchers/operators lists, which today gate only itself, would silently become the workspace's
admission lists: a permission widening nobody asked for. (A) costs a new Firebase project and one
more thing `fugaro update` must converge.

**D981 — May a project belong to more than one workspace?**
- **(A, recommended) At most one.** The membership is a scalar key in the project's own config.
- **(B) Any number.**
Cost of (B): "which workspace's headroom applies" has no unique answer, a shared credential's "which
workspace may this project use" has none either, and every refusal would have to name which of N
workspaces refused. (A) costs a second key the day a project genuinely needs two groups.

**D982 — Where the workspace record lives, and who writes it.**
- **(A, recommended) Declared in each member's local project config and published by
  `fugaro update`** to the workspace's own database, on the policy stage's pattern.
- **(B) A new `fugaro workspace publish` command writing the node.**
Cost of (B): a second command that must run after a change, which the HARD RULE in `CLAUDE.md`
forbids and which the models design already refused once (F13). (A) costs the published record being
as fresh as the last `fugaro update`, which is what every other published node already is.

**D983 — What "single sign-on" is.**
- **(A, recommended) One sign-in at the workspace, one exchange per member**: a person holds a
  short-lived workspace token and exchanges it for each member's own token. No session is shared.
- **(B) Every member accepts the workspace token directly as a bearer.**
Cost of (B): one stolen token is every member at once, and each member's rules would need a grant for
a token whose issuer is not its own Firebase (F6) — a wider change to the rules than the exchange is.

**D984 — Where the exchange happens.**
- **(A, recommended) At the member**, in the member's own authorization server, which verifies the
  workspace token and mints the member's own. The member stays the authority over what the identity
  may do there.
- **(B) At the workspace**, which mints a per-member token and hands it back.
Cost of (B): the workspace service holds the ability to mint tokens for every member, so it becomes a
credential authority over them rather than an identity provider; a compromise of it is a compromise
of every member's API, and revocation needs the workspace to reach into each member.

**D985 — What a workspace identity may do in a member.**
- **(A, recommended) Nothing beyond what the member's own lists grant.** The exchange admits the
  person only when the member's launchers/operators list names their address.
- **(B) A workspace role that maps to a member role.**
Cost of (B): a second permission model beside the member's, and the only-tighten rule would not hold
— a workspace admin could widen a member's list without that member's owner acting. (A) costs one
`fugaro user add` per member, which is the same act as today.

**D986 — What is stored, and where.**
- **(A, recommended) Hashes only**, in the workspace's own subtree that no client of its database may
  read, plus each member's own unchanged record.
- **(B) The workspace stores the member tokens it minted, so a person can list their sessions.**
Cost of (B): the workspace holds a value it can present to a member — the one thing "store only
hashes" exists to prevent — and revocation would have to reach every member's store.

**D987 — What "shared credentials" means.**
- **(A, recommended) One workspace bundle and one workspace credentials service**: the credentials
  service deployed once in the workspace's project, holding the workspace's credentials in its one
  bundle secret, proxying every member's calls, admitting each member's jobs by their verified
  service-account email.
- **(B) The bundle is copied into each member's project, each with its own service.**
Cost of (B): N copies to rotate, N places a key can leak, no single revocation, and no one hop that
sees the total, which is what the cap needs (D989). (A) costs the workspace holding model credentials
for every member — a trust the members must accept knowingly, and the reason spec §8 is a threat
model.

**D988 — Which credentials may be shared, and with whom.**
- **(A, recommended) Every shared credential carries a per-project allow-list**, and the effective
  set is the intersection of that list and the member's own policy; the member's policy is the
  tighter side.
- **(B) A workspace credential is available to every member.**
Cost of (B): one member's compromise reaches a credential every member's code is billed for, and a
member whose policy excludes a provider would still be served it — a policy bypass, which the
only-tighten rule forbids.

**D989 — Where the workspace cap is enforced.**
- **(A, recommended) At the workspace's credentials service**, on the calls it pays for, with an
  advisory headroom published to each member's gateway so a run stops early.
- **(B) Only at each member's gateway, from the published headroom.**
Cost of (B): the cap becomes cooperative — a member that under-reports spends past it silently
(F12), and nothing sees the total. (A) costs the service being on the critical path of every
shared-credential call, so its outage stops work; the advisory headroom and the stop flag bound that.

**D990 — What the workspace cap bounds.**
- **(A, recommended) The spend of the workspace's own credentials**, and, for a member that routes
  every call through the workspace, that member's whole spend. The design refuses to call it a bound
  on a member's total spend.
- **(B) A bound on every member's total spend**, enforced by requiring every member to route
  everything through the workspace.
Cost of (B): it is a larger change than the workspace itself — it makes the workspace's service
mandatory for all traffic, including a member's own keys — and it would have to be stated as a
requirement on every member rather than as a property of the cap. (A) costs the honest sentence "the
cap covers what the workspace pays for", which the console must show and spec §8 must carry.

**D991 — The workspace ledger.**
- **(A, recommended) A rollup recomputed by the workspace service from each member's own run
  ledger**, on the history job's schedule, with the enforcement reading the last good rollup.
- **(B) Each run writes the workspace ledger directly.**
Cost of (B): a cross-database write from a job that holds no grant on the workspace's database — a
new grant and a new failure mode in the run path — and a run that cannot write would halt work
rather than merely go unrolled. (A) costs the headroom being as fresh as the rollup.

**D992 — The stop.**
- **(A, recommended) `fugaro budget kill` gains a workspace scope**, one command that stops every
  member's runs, on the credentials service's own precedent (`fugaro budget kill --all` is already
  its stop flag).
- **(B) A separate `fugaro workspace kill`.**
Cost of (B): two stop flags an operator has to remember under pressure, and the workspace one would
need its own runbook line beside the other. (A) costs the kill command's surface growing a scope,
which its `--all` already models.

**D993 — The command surface.**
- **(A, recommended) `fugaro workspace ls | show | add | rm | cap`**, local writes and reads only;
  every cloud change reaches the workspace through `fugaro update`.
- **(B) A `fugaro workspace` verb set with its own publish and apply.**
Cost of (B): the HARD RULE again (D982). (A) costs nothing beyond the commands themselves.

**D994 — The console and the mobile app.**
- **(A, recommended) No console or mobile task in this plan.** The console reads `fugaro config list
  --json` today; a workspace screen is a later design reading a versioned `fugaro workspace ls
  --json`.
- **(B) A console workspace screen in this plan.**
Cost of (B): a console contract (`console/src/main/installlist.ts`) fixed before the document it
carries exists — the mistake the MCP design's §10 refuses for the roadmap scope. (A) costs the
workspace being CLI-only at first.

**D995 — What a workspace is commercially.**
- **(A, recommended) Not a new licensed unit and not a new price.** The subscriptions design already
  measures the paid unit — a seat is a person who launches jobs or signs in as operator or admin,
  measured by Firebase Auth and the per-project allowlists — and a workspace is the shape that
  measurement takes when projects are grouped. The workspace's own services are `ee/` under rulings
  A5 and A6.
- **(B) The workspace is the licensed unit** (one key per workspace).
Cost of (B): it re-bases the key on a grouping the owner may create or dissolve without a billing
event, and it needs a seat-to-workspace migration for every installation that groups later. (A)
costs nothing today and leaves the choice open.

## 5. The number block

Read at launch by hand, not from a neighbour's line, on 2026-11-01:

| | |
| --- | --- |
| Highest number in `docs/` on `origin/main` (`3d6ed04d`) | **D900**, in `docs/rigor-command/README.md`:117, which is answered. (`D901` occurs on `main` only as the number that file says a later task takes) |
| Highest number in the open pull requests' own diffs | **D979**, reserved by `docs/periodic-reviews` (**#1490**), which writes **D960–D971** |
| Also taken, the same morning | **D920–D934**, `docs/mcp-server` (**#1487**), reserving **D935–D939**; **D940–D953**, `docs/plugin-user-scope` (**#1488**), reserving **D954–D959**; **D901–D919**, `docs/run-without-checkout` (**#1481**); and **D901–D920**, `docs/run-limit-carry-over` (**#1485**) — the last two **overlap**, and neither is this work's to fix (**noticed, not done**) |
| **This work's block** | **D980–D995** — the sixteen decisions of §4 |
| Reserved, unused | **D996–D999** |
| A later task takes | **D1000**, and re-checks the facts above rather than this conclusion |

`grep -rhoE 'D[0-9]{3,4}' docs` is not quite enough on its own, for the two reasons the MCP design
records and this reading confirms: it returns `D1000`, which is not a decision at all but the test
name `TestSelftestManagedSettingsDirUID1000` in
`docs/plans/2026-09-30-m9a-gateway-and-project-identity.md`:229, `:949` and `:1181`; and a concurrent
work's block is not on `main` at all — #1481's, #1485's, #1487's, #1488's and #1490's READMEs, in
their own diffs, are where those numbers are written. Read with a boundary on both sides
(`[^A-Za-z0-9]D[0-9]{3,4}[^0-9]`), the highest number on `main` is **D900** and the highest in the
open pull requests is **D979**.

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
| V2 | Whether the MCP design (**#1487**) is merged by the time this work starts | It is open at this base; [implementation-plan.md](implementation-plan.md) §2 builds on it as a dependency either way, and says what changes if it lands first |
| V3 | The current revision of the MCP authorization specification, and whether the exchange's flow is a conforming token exchange or a small departure | The public specification is not reachable from this container; the MCP design's §0 open item carries the same question for its own flow |
| V4 | Whether the `ee/` boundary rulings (A5, A6) have moved again by the time the workspace's services are built | `docs/ee-reorg/` is itself a plan; the workspace's service placement is read from it, not decided here |
| V5 | Whether a member may keep a credential the workspace also holds, and how the two are chosen between at a run | The design says a member's own credentials keep working (§4) and does not say which of two is picked; the plan's W6 owns the rule and this document does not guess it |
