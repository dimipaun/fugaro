# Workspace credentials: the threat model for a person's token at the workspace

**Status: this is WC0's deliverable — the threat model the personal half of
[implementation-plan.md](implementation-plan.md) waits behind, and the gate every other task of that
half stands on.** Nothing of the half is built: the workspace's service serves no grant route, no
roster person is admitted at the workspace's project's intake, no member's policy carries the switch,
no candidate of the offering reads a person's container, and no command stores a token at a
workspace. It is written to the owner's answers of 2026-11-21 (D3140–D3148,
[reconciliation.md](reconciliation.md) §4), to that file's findings and resolutions, and to
[spec.md](spec.md) §4, §5, §5a and §6 as that file amends them. Every statement about the code below
was read against the tree, and the file cites paths and symbols, never line numbers. **An independent
security review of this file is mandatory, and the owner answers every finding of it before any other
personal task is launched** (reconciliation O1), so what follows is the document that review reads,
and §11 is where the owner's answers go.

**Why there is a new threat model at all.**
[workspace-projects-users/threat-model.md](../workspace-projects-users/threat-model.md) is the model
this file sits beside, and it is the model of the workspace's authority over a member: a workspace
admin reading every member's secrets, personal Claude OAuth tokens included, is its §7 residual risk
1. It does **not** cover a token held *at the workspace* — one Secret Manager container in the
workspace's own project, read through an accessor of that project and served by a service of that
project — an intake that admits the workspace's roster people, or a launcher class of the workspace's
own (reconciliation F19). D2969 puts the person's prefix "after the threat model", and D2971 says the
settled rule it amends "goes through the threat model": this file is that text, and every other task
of the personal half waits on the owner's answers to its review.

**No placeholder, credential or figure appears here.** Every person, repository and workspace is a
placeholder (`person-1`, `owner/repo-a`, `workspace-alpha`), no file carries a credential, a key, a
token or anything shaped like one, and no currency figure is stated.

## 1. What this half is, and what it is not

**One token per person, stored once at the workspace.** A person stores their own Claude subscription
(OAuth) token in the workspace's own project, through the intake that project already has as an
ordinary installation, with the existing command and the workspace's own project named — no new flag
and nothing that resolves a workspace name to a project (R13, D3148). The container is the person's
own by its prefix, `fugaro_u_<id>_claude-oauth-token` (`secretname.User`, A1), and there is one
container per person and name (D3143, R5).

**It is served to the runs that person launched in the workspace's member projects.** When the
repository's model route is the workspace and the member's switch is on, `fugaro run` asks the
workspace's service for a grant (R8, D3141). The grant admits a verified person whose address is a
`human` of the workspace's roster, names the member's job account, the repository and the run, and
answers a slip's id and nothing secret (R3, F14). The job claims the slip, and the offering puts that
person's token first, ahead of the shared entries the call is admitted to (R7).

**What it is not.**

- A project that is in no workspace takes none of it: no route, no reader and no refusal of an
  installation's service changes (A3, D2966). The workspace's own project's runs are served no
  personal token either, though the token is stored in that project (A4; D3143's cost as corrected
  after review round 1).
- A person's forge token is not part of this half (A1), and a per-person token is never a project's
  or a workspace's to lend: it is owner-only, and a pool and a `fugaro_p_` name are outside every
  grant (D2962, D2976; R7 and R11 of dynamic delivery).
- A run launched by a service account asks for no grant and holds no personal token (D2976's
  consequence).
- Nothing moves a stored token: a project's own copies stay where they are, and a project that keeps
  a model credential of its own keeps using it, so the workspace's copy is shadowed until that
  credential is removed (R10, D3141, reconciliation O3).
- Nothing of the *shared* half is restated here. What a workspace's shared entries are, who is
  admitted to them, the cap and its meter are
  [workspace-service](../workspace-service/README.md)'s and `SECURITY.md`'s workspaces section, and
  this file adds only what a person's own token changes.

**What this half depends on, none of it built.** The workspace's service, its node and its meter
(workspace-service); the roster (`workspace.Roster` at `workspaceRosters/<name>`); and the intake that
already exists in the workspace's project (`internal/intake`,
`deploy/terraform/gcp/modules/installation/intake.tf`), whose invoker set is that installation's own
launchers today (F9, F10).

## 2. Who can read a token

**Every workspace admin, through an owner's role on the workspace's project.** R8 of
workspace-projects-users gives each workspace admin `roles/owner` on the workspace's own project, and
a project owner reads any secret in its project: each of them reads every personal container from
Secret Manager in one place (F20). The spec's §6 names it as the cost of storing tokens there, and D2825
is not amended — that decision's rule is about Fugaro's own commands, which never read a personal
token under a person's prefix (the `--check-credentials` probe reads project model keys and
project-owned accounts only, WU22), and it is a rule about a terminal rather than about IAM: an owner's
role is not narrowed by it. §10's first residual risk is that reach, stated with no technical fence.

**The workspace's service account, through one conditioned accessor.** The workspace's root gives the
account the workspace's bundle accessor it already needs, and one more in the workspace's project,
conditioned to the `fugaro_u_` prefix and to a name that ends in `_claude-oauth-token` — the one
personal name this half serves (R6). So the service reads a person's Claude token and **no other
personal secret**: not another name of theirs, and not a `fugaro_p_` name of the workspace's project.
It is a name condition on the accessor rather than a second implementation of anything (R6).

**Nobody else.** The container is in the workspace's project, so no member's admin reads it through
their own project's IAM, and a member holds no grant on the workspace's project beyond the invoke
grants of its job accounts (`tf.Cover.WorkspaceNotCovered`, `internal/infra/tf/workspace.go`: the one
accessor on the bundle, the conditioned accessor above, invoke grants to member job accounts and one
invoke grant per address for each `human` of the roster are the whole of what a plan of that root may
add, and the two deletions it admits are a member's and a person's invoke grant — F15, WC2). Another roster person
reads nothing of it: a grant names only the caller's own personal names (`checkGrantedNames`,
`internal/credsvc/slip.go`; D2962). A launcher reads nothing of it: the grant's answer is a slip id and
a runtime (`SlipGrantResponse`). A run reads nothing of it: no value is handed to a run, a CLI or a
person (§9). And the intake's account cannot read a value at all — its IAM is create, version-add and
delete under a name-prefix condition, with list unconditioned and **no accessor** (R3 of dynamic
delivery) — so whoever holds it can destroy a token and cannot read one.

## 3. Who can replace or delete a token

**The person.** The intake derives the container from the caller's verified account id and the name
and from nothing else (`(*Server).container`, `internal/intake/handlers.go`), so a person writes and
deletes under their own prefix and cannot touch another's. The value comes from stdin only (WC9).

**Whoever holds the intake's account.** The workspace's project is an ordinary installation, so its
intake is the account D3143 chose: the same account that can add a version to any container under the
`fugaro_u_` prefix, and delete it, in the workspace's project — every person's token for every member
at once (D3143, spec §6). The intake is reached by a person through that project's own address, read
out of that project's config on the person's machine (`intakeURLOf`, `internal/cli/secrets_intake.go`)
and never out of anything a member holds or publishes (R13, F23). Holding that account is the trust
the workspace's owner already carries for the whole project.

**Every workspace admin again.** An owner's role on the workspace's project carries the writes as well
as the read §2 states, and the container is that project's secret: the same people delete or replace
the token out of band. That is one reach and not two, and it is stated once more in §10's first
residual risk.

**What a deletion does.** A container that is missing, or holds no version, **refuses** the call and
names `fugaro secrets user set`; it never falls to a bundle entry (R7, D2974). So a deletion refuses a call
that names the token, visibly, and never moves it to a paid shared entry. **It is not loud for a run
that has not yet asked:** on the person's next launch the grant answers `held:false` and the run
starts on the shared entries exactly as before and prints nothing (contract-person.md, R8), so a
deletion or a replacement can silently move a person's runs onto paid shared entries or through
another account (§11, finding 5). A member's admin turning the member's switch off is the same thing by another
route, and it is theirs to set (§4).

## 4. Who can spend a token, and for how long

**The person's own runs, while their slip lives.** The mechanism is F14's, and it is worth stating
exactly: a grant answers a slip's **id** and nothing secret (`SlipGrantResponse`); the launcher hands
the id to the job, and the job's claim is answered the claim secret once, which the run keeps out of
its environment (`slipHold`, `internal/runner/budget_session.go`); the claim is held to the run's
verified identity and job account; and a slip serves until `maxSlipRuntime`, 24 hours **counted from
its claim** (`internal/credsvc/slip.go`; `creds.Slip.Expires` is `Claimed.At + MaxRuntime`). That
clock starts only at the claim, and an unclaimed record carries no creation time, so as the code
stands **a grant nobody has claimed never expires**; the owner narrowed that (§11, finding 1): WC4
gives a workspace slip a **claim deadline counted from the grant**, chosen and pinned by WC4, after
which the slip cannot be claimed. Every bound below is read with that deadline. A call under the slip, with the member's switch on, is served the
person's token as one more subscription candidate and reserves nothing against the workspace's cap
(R7, D2965, workspace-service R15).

**A member's admins, for a claim deadline counted from the grant (chosen and pinned by WC4) plus up to
24 hours from the claim, after that person launched a run in that member.** The claim
is held to the run's verified identity and job account, and both are the member's: the identity is
minted in the member's own Firebase project, which its admins can forge (D1943's class, dynamic
delivery F33), and the image the run executes is theirs. So a member's admins can claim a person's
slip first, or take the claim secret from a run they control, and spend the slip until it expires
(F14). Launching a run in a member therefore lets that member spend the launcher's subscription for
the slip's lifetime, and no longer. **That is the bound D2970 asked for** — no spending without an act
by the person — and not a stronger one. §10's second residual risk states it, and it is one of the two
the owner accepts in words (reconciliation O2).

**With automatic use, the switch is the member admins' own.** With D3142 a run draws on the person's
token without a flag: the switch that exposes a person's subscription is one key of the **member's**
published policy, off when absent, set by the member's admin and published by the member's
`fugaro update` (R9, D2972). The party that turns the exposure on is therefore the party that can then
spend the token (F14). `fugaro run` says in one line that it draws on the person's token, and
`--no-personal-token` skips it for one run (D3142, R8). The person's own runs in a member they never
launched in carry no slip, so that member's switch exposes nothing of theirs *through their own
runs*; it is no fence against the replay below, whose grant names any member's job account.

**The replay, and the bound the review corrected.** The grant of R3 is asked at the workspace service's
address, which a run and a launcher read from the member's own published route (workspace-service R3,
R4). A member admin who publishes a **false** address receives the identity token of whoever launches
there and can replay it while it lives: the services accept gcloud's own audience (`GcloudAudience`,
`internal/credsvc/auth.go`), and Cloud Run accepts that token at **any Cloud Run service the person can
invoke**, the intakes of their other projects included (F24, corrected after review round 3 and again
by the review of this file; D1939 and D1944 accepted the replayability of such a token). **The false
address is not needed for the reach.** On every switched-on launch the launcher sends the person's
gcloud-audience token to the member's own credentials service (`slipGrantOverService` and
`slipIdentity` in `internal/cli/run.go`, and `serviceReadIdentity` and `pairIdentity`), whose
deployment the member's admins control, so a member's admins receive it with no false address at all
(§11, finding 2). What the replay can
do, in the hour the token lives: **ask for grants as the person**, naming **any member's job account**
— R3 holds the account to a member row of the node and the repository to that account's list, and
nothing on the route ties the grant to the member the person launched in (F24) — and **replace or
delete the person's own secrets** at any intake that admits them, and a project's shared ones where
the person is an admin of that project. What it cannot do: **read a token**, which a grant never
answers (D2962), and **spend the slip it takes**: the claim is held to the run's verified identity and
job account, so a grant naming a second member's job account is spent by *that* member's admins. A
grant taken in that time can be claimed up to the claim deadline after the grant (chosen and pinned
by WC4) and then serves up to 24 hours from the claim, so the window ends at most the token's hour,
plus that deadline, plus 24 hours; the "about 25 hours" this file first stated was false, because
with no claim deadline an unclaimed grant never expired (§11, finding 1). Where the replayer
administers a second member too, or its admins collude, the subscription is spent in a member the
person never launched in; the owner narrowed that reach to the members the person is a launcher of,
checked against that member's own list (§11, finding 2). §10's third residual risk states it. What
narrows it as the design stands: the identity token is short-lived; the address a person stores a token at is read out
of the **workspace's** project config on that person's own machine and out of nothing a member holds
or publishes, so the intake leg is not one a member can aim (F23, R13, `intakeURLOf`); and
`fugaro doctor` calls neither the workspace's service nor its
intake, so it sends a person's identity nowhere (WC11, declined in the plan's §10).

**The reverse direction.** Every roster person sends their gcloud-audience token to the workspace's
service on each launch and to its intake (WC9). The workspace's admins, or a compromised workspace
service, can replay it for about an hour after each launch and reach the person's projects outside the
workspace: forge read mints, pairing, slips, and intake writes, `fugaro_p_` secrets included where the
person is an admin of that project. The owner accepted it (§10 risk 10, §11 finding 4).

## 5. What each actor can do

### A hostile member admin

The member's own admin holds that member's database write, controls its job images and its published
policy, and can publish the address its runs read. In this half they can: turn the member's switch on
or off, because it is their policy (R9); forge a run identity of their member and take a claim secret
from a run they control, so they spend any slip granted in that member until it expires (F14, §4);
replay the identity token of whoever launches there (sent to their own service on every launch, with
no false address needed) at every Cloud Run service that person can invoke (F24, §4, §11 finding 2); and replace or delete the person's own secrets at
any intake that admits them, and a project's shared ones where the person is an admin there (F24).
And, with the replayed identity, ask for a grant of the person's token **naming any member's job
account**: until the owner's narrowing is built (a grant bound to the members the person is a launcher
of, §11 finding 2), R3 holds that account only to a member row of the node, so where they administer a
second member — or its admins collude — the person's subscription is spent in a member the person never
launched in (F24, §4, §10 risk 3).

What they **cannot** do: read a token's value (§2); spend a slip held to a job account of a member
they do not administer — the claim is held to that run's own identity and job account, so the reach
above is not theirs alone; obtain a grant for someone else's token, because a grant names only the
caller's own personal names (`checkGrantedNames`, D2962); or make the workspace's service write
anything into the member's database (§9). Everything
this actor reaches needs their own member, a second member whose jobs they or someone colluding with
them control, and the person's identity while it lives — that is what §10's second and third residual
risks state, and it is not a new authority: the member's admin could already run anything they like on
the member's jobs.

### A hostile roster person

A person on the roster who holds a token is the token's owner, and this half is built so that owning
one is all they can exercise: they store, replace and delete their own token (§3); their own runs in
the member projects are served it (§1); they can refuse to be served by deleting it, at the cost of a
refusal that names the fix (§3); and they learn what §6 states from the grant route, and nothing more.
What they **cannot** do: reach another person's token — a grant names only their own personal names
and the pool and the `fugaro_p_` prefix are outside it (D2962, D2976); make another person's run draw
on their token; read the workspace's node, its roster or another member's state. A roster person who
holds no token is answered "no token held" before the node is read, so they learn nothing at all from
the route (R3, §6).

### A stolen workspace-admin credential

A stolen workspace-admin credential reaches the whole workspace's project (§2 of
workspace-projects-users/threat-model.md is the reach itself: an owner on the project, its secrets, its
IAM, its database, its members through the workspace's own authority). In this half it reads **every
person's token** — the container is a Secret Manager secret of that project — and can delete or
replace any of them (§2, §3). A value read out of Secret Manager is a bearer credential at Anthropic's
own service, so whoever holds it spends that person's subscription from anywhere, outside Fugaro, and
nothing in Fugaro revokes it: what ends it is the person rotating the token, and, for Fugaro's own
use, that the workspace's service would then serve a version the person wrote. **This reach already
covers the service and its own database**, so nothing of this half fences it: `roles/owner` on the
project is owner over the workspace's own database — where this design keeps the roster and the slips
(R3, §7) — and over the service's deployment, so a stolen credential does not need the person's
identity to have a token served: it writes what it likes there, or redeploys the service as its own
account. **Nothing here detects the theft or fences it.** It
is a Google account, whose protection is the owner's, and no check this design builds reads a Secret
Manager payload access: as the model this file sits beside says of a minted key and of a copied value,
the gap is stated here as the acceptance it is rather than built into a check. §10's first residual
risk is this reach, and it is not closed.

### A compromised run

A run of a person's own in a member executes that member's image, so "compromised" and "the member's
own act" meet: what the run can do is bounded by its own identity, which is the member's (F14). In
this half a compromised run can: make model calls under its slip with the member's switch on, and so
spend the person's subscription while the slip lives (up to 24 hours from its claim, though the run's
own execution is shorter); for a workspace slip the run's outcome record is no bound against a hostile
member, because it sits in the member's own database, which that member's admins write (§11, finding
8); and take the claim secret, since the agent
shares the runner's container and reaches what the runner reaches (F22 of dynamic delivery, D1941) —
`slipHold` keeps the secret out of the environment and out of logs, not out of the run's own process.

What it **cannot** do: read the token's value — the service proxies the call and hands no value to a
run (§9); use the slip at another member's service, because the slip is this service's own and is kept
in the workspace's own database (R3, R4); use the token outside Fugaro, because it never holds it;
ask for a grant of its own, because the grant route's caller is a **launcher**, verified by a person's
identity token that the launcher holds and the run does not (R3); or reach another person's token. And
a run of the person's in a member where nobody set the switch is served the shared entries exactly as
before, with no personal candidate at all (R7, WC5).

## 6. What a roster person who holds a token can learn from the grant route

**"No token held" is answered before the node is read** (R3), which is the whole of the design's care
here: a person who holds no token is answered that, gets no slip, and learns nothing — no member
table, no account, no repository.

**A person who does hold one learns one thing: whether a job account and a repository are a member's.**
A grant asks for the member's job account, a repository and the run, and the service holds the account
to a member row of the node and the repository to that account's list before it writes anything (R3).
So a grant and a refusal are a yes/no over the pairs the caller names, and a person who holds a token —
and who can already name a job account and a repository of any member they are in — can probe that
one bit. It is bounded and deliberately not closed: **every refusal of a grant is one class that names
no account, repository or person**, so a refusal discloses nothing beyond itself; every grant and every
refusal writes one audit line, so the probing is visible to the workspace's owner; and what is
disclosed is a fact about the **workspace's own table** (which job accounts are a member's), not about
a member's secrets, its people or its policy. D3145 is **reopened** by the review (§11, finding 2): the roster's `grants` list is a record
and not a grant, and the owner now binds a grant to the members the person is a launcher of. §10's eighth residual risk states the residue, as R3 asks WC0 to.

## 7. What offboarding ends, and what it does not

**It ends the person's grants and their runs in flight, within a minute.** The service re-reads the
roster as it re-reads its node (60 seconds), and the same check is made **on every call under a
workspace slip**, against the same copy: a call whose slip's owner is no longer on the roster is
refused, whether or not the sweep of their containers succeeded (R3, D3146). Such a run **halts** with
a reason that names the removal, and moves on to nothing — not to the shared entries (R3, WC8). A
roster that cannot be re-read refuses the call as unavailable, and the run waits, as it waits for a
workspace service it cannot reach, and moves to nothing meanwhile (R3). `fugaro workspace user rm`
also sweeps the person's containers at the workspace's intake, after the members' sweeps and whatever
they answered (WC10, F21), and its output says that a run in flight on that person's token halts
within a minute (R3). The sweep is not what stops the run; R3 is, and that is the design's whole point
here.

**It does not end the person's admission at the intake.** The roster people admitted at the
workspace's project's intake are admitted by that project's own Terraform, rendered from the roster by
the workspace owner's `fugaro update` (R5, D2971). A person removed from the roster therefore stays
admitted there **until the workspace owner's next `fugaro update`**, and inside that window they can
store a token that **nothing will serve**: a grant is issued only to a `human` of the roster, so a
person off it is refused one and no run of theirs is served a personal token (R3). What they can still
reach in that window is the intake's own bound, which is the integrity power R3 of dynamic delivery
states — they can write under their own prefix, which is a token nobody is served.

**It does not end the other copies.** No command moves a stored token (R10), so the person's
per-project containers stay until each member's own sweep runs, exactly as §6 of the spec says; and
nothing of a member's own state — its policy, its caps, its own people — is touched by removing a
person at the workspace.

**And a run halted under D3146 can be launched again.** The person's next launch in a member whose own
lists still admit them asks for a grant, is refused, and **starts on the member's shared entries** with
one line saying it is not on the person's own token and why (D3147, R8). So offboarding ends the use of
that person's **token**; it does not end their ability to run in a member that still admits them, and
what those runs spend is then the workspace's shared entries through their allow-list and the meter.
D3146 states the other half of the cost: it also stops the runs of someone removed from the workspace
who still holds a role in that member.

## 8. The three settled statements this half amends

Each of these is quoted, and what it becomes follows it. A task that makes one false changes the
sentence, the test that pins it and `website/content/security.md` in the same change, as F16 and
`.claude/rules/credentials-service.md` require.

**1. The workspaces spec §4, that the workspace's service admits no member's person**
([spec.md](../workspaces/spec.md) §4):

> Every route of the workspace's service therefore admits **no member's person** — none is a *person
> of one member* here, because the list that names a member's people is that member's own
> launcher/operator list, the member's own file, which the workspace's service holds no grant to read
> and no member's `update` publishes into the workspace (§2, §5). An admission the service cannot
> check is not an admission.

**It becomes:** the workspace's service admits a member's **person** on two routes of its own — the
grant route and the calls that present a slip it granted (R3, R4) — and it does so on a list it *can*
check, which is why this is an amendment and not a reversal of the sentence's reason: the workspace's
own roster — a node of the workspace's own database, written by several parties, which
`internal/workspace/rosterstore.go` names as the workspace's own service, its admins and the workspace
project's owners, and re-read by the service every 60 seconds (R3) — whose people the workspace
owner's `fugaro update` renders into the installation's own admission (R5, D2971, §7). The rest of §4
stands: the workspace's person class is still the workspace owner's people, and the roster's people
are admitted as a class of their own beside it. What does not change is the reach: a person admitted
there is served **their own** run's token and nothing of another's (D2962, D2976). WC4 and WC2 change
the sentence, its test
(`TestSecurityMdStatesTheWorkspaceThreatModel`) and `website/content/security.md` together.

**2. R18 of workspace-service, that a call to the workspace's service carries no slip**
([reconciliation.md](../workspace-service/reconciliation.md) §5):

> A call to the workspace's service carries the job's identity and the run's token and nothing else of
> the member's: no slip id, no claim secret, no auth-mode or account header. The workspace's scope
> refuses a call that presents a slip.

**It becomes:** the first sentence stands as the rule for every call the run makes to the workspace's
service, and the second is narrowed to what it always meant — the scope refuses a **member's** slip,
one the member's own service granted, and never the workspace's own (F13, R4). A call under a personal
grant does carry a slip id and a claim secret, and both are the **workspace's** own: the slip is
written to the workspace's own database and the workspace's service is the only grantor of it (R3,
R4), so nothing of a member's crosses. The gateway keeps the two apart by upstream — the project's
slip goes to the project's service and the workspace's to the workspace's (R4). WC4 and WC8 move the
tests that pin the old wording (`TestWorkspaceScopeRefusesACallThatPresentsASlip` becomes
`TestWorkspaceScopeRefusesAMembersSlip`, and `TestTheWorkspaceUpstreamCarriesNoSlip` becomes
`TestTheWorkspaceUpstreamCarriesNoMembersSlip`).

**3. §6 of the workspace-credentials spec, that offboarding does not stop a run in flight**
([spec.md](spec.md) §6):

> Offboarding does not get simpler at first. Per-project copies remain, so the sweep over every member
> stays. Offboarding does not end an open slip (up to 24 hours) or stop a run in flight.

**It becomes:** the first two sentences stand as written. The third does not: with D3146 a removed
person's grants and their runs in flight **stop within a minute**, because the slip's owner is checked
against the roster on every call (R3, §7). What remains of the third sentence is its other half: a
slip whose owner is still on the roster is not ended by anything here and serves up to 24 hours from
its claim, which is §4's bound for the members who still admit that person. D3146 states the cost of the reversal:
it stops the runs of someone removed from the workspace who still holds a role in that member.

## 9. The two rules of the workspaces design this half keeps

**It never hands out a credential.** The workspaces design's rule, written as a prohibition in
[docs/workspaces/spec.md](../workspaces/spec.md) §9 and stated in `SECURITY.md`'s "What a workspace
never does": no shared credential's value is returned to a run, a CLI, a person, an error message or a
log line, because the service proxies the call. The personal half keeps it, and it is the reason the
candidate of R7 is a container the **service** reads: the token's value never reaches the run, the
launcher, the CLI or the person; the grant's answer is an id and a runtime; the claim answers a claim
secret that is the service's own; and `fugaro doctor`'s probe reads project model keys and
project-owned accounts only and never a personal token (D2825). What the rule never claimed, and this
file states plainly in §2 and §10: it is about the workspace's services and CLI and never about an
**IAM holder**, and after R8 of workspace-projects-users a workspace admin is one.

**It never writes a member's database — any node of it.** D1299 as amended on 2026-11-04, and the
same list in `SECURITY.md`. The personal half keeps it and adds no write: the slip of R3 is written to
the **workspace's own** database, the roster re-read is the workspace's own node, the audit line each
grant and refusal writes is the workspace's own record, and the workspace's service's account holds no
write permission on any member (R3, R8 of workspace-service). The one write the workspaces section
already records beside this rule is not this half's and is unchanged by it: a run's call to the
workspace's service carries the run's own Firebase token (F30, R18 of workspace-service), and a
compromised workspace's service could write, through that token, what the run could write for itself.
The personal half adds no second path of that kind: it adds no call a run makes that carries anything
more than it carries today, and the nodes such a token could reach are the run's own.

## 10. What this half accepts, as residual risk

Numbered from 1 for this file, and read beside
[workspace-projects-users/threat-model.md](../workspace-projects-users/threat-model.md) §7 and
[workspaces/spec.md](../workspaces/spec.md) §8, which are not restated here. Each is named with who
accepts it.

1. **A workspace admin reads every person's token, and a read token is a bearer at Anthropic.** Every
   workspace admin holds `roles/owner` on the workspace's project (R8 of workspace-projects-users), so
   each of them reads, replaces and deletes every personal container in one place (§2, §3), and a value
   they read spends that person's subscription from anywhere, outside Fugaro, with nothing here to
   revoke it. **There is no technical fence.** D2825 is stated and not amended: Fugaro's own commands
   never read a personal token under a person's prefix, and that is a rule about this CLI and not about
   IAM. What bounds the reach is not technical either: the workspace's owner's choice of its admins,
   `fugaro workspace user rm --admin` (which removes the authority, not what the admin already read),
   Google's own account protection, and the person rotating their token. Stated, not closed (F20,
   spec §6). **Accepted by the workspace's owner**, who names the admins; the same acceptance the model
   this file sits beside records for the member's side, at risk 1 of its §7.

2. **A member's admins can spend a person's subscription in that member for up to 24 hours from the
   claim of a slip granted when that person launched a run there, the claim itself bounded by a deadline
   counted from the grant, chosen and pinned by WC4.** The claim is held to the member's own identity and job account, so
   the member's admins can take a slip's claim secret or claim it first and spend it until it expires
   (F14, §4). It is D2970's bound — no spending without an act by the person — and no stronger one.
   The owner's words (O2) were given on "up to 24 hours"; the deadline is the review's correction of a
   bound that was false (§11, finding 1). **Accepted by the owner in words** (reconciliation O2), and the cost of D3142's automatic use is
   part of it: the switch that exposes the subscription is set by the member's admins, the party that
   can then spend it.

3. **A member's admin can act as a person who launched there while that person's identity token
   lives, about an hour, at every Cloud Run service the person can invoke, with or without a false
   address, and a grant taken in that hour reaches a member the person never launched in: the grant
   names a job account R3 holds only to a member row of the node, so a second member whose jobs the
   replayer administers, or whose admins collude, spends the person's subscription — the window ends at
   most the token's hour, plus WC4's claim deadline counted from the grant, plus 24 hours from the
   claim (F24, D1939, D1944).** The owner has narrowed the reach: a grant is bound to the members the
   person is a launcher of (§11, finding 2). What the
   replay can do, and what it cannot, are §4's: ask for grants as the person, naming any member's job
   account; replace or delete the person's own secrets at any intake that admits them, and a project's
   shared ones where the person is an admin there; never read a token; never spend the slip itself,
   which that member's own admins claim (F24).

   **The window in the member the person launched in is accepted by the owner in words**
   (reconciliation O2). **The cross-member reach was not in that record** — F24 and O2 both state the
   narrower one ("grants as that person for jobs of that member"; "cannot run a job in another
   member") — so it was put to the owner (O1), who answered on 2026-11-22: narrow it, a grant bound to
   the members the person is a launcher of, checked against that member's own list (§11, finding 2).
   The mechanism is an open design question for WC4 and WC5's briefs, because the workspace's service
   holds no grant to read a member's own list (§8, workspaces spec §4).

4. **A person's runs draw on their own subscription by default.** With D3142 the person stored the
   token to use it, and every run they launch in a member whose admins turned the switch on draws on
   it without being asked: a person spends their own subscription unless they pass
   `--no-personal-token` for one run (D3142, R8). The one line `fugaro run` prints is the whole of the
   notice. **Accepted by the person who stores the token**, which is the answer D3142 was given on.

5. **Whoever holds the intake's account, and every workspace admin, can replace or delete any person's
   token in one place.** One container per person and name in the workspace's project means one
   account's reach is workspace-wide (D3143, spec §6). What bounds it is not a fence: a token someone
   else replaced is one the person did not write, and the design's answer is that the next run draws on
   it, while a deletion refuses a call that names the token and
   moves it to no paid entry (R7, D2974, §3). It is not loud for the person's next launch: the grant
   answers `held:false` and the run starts on the shared entries and prints nothing (§11, finding 5).
   **Accepted by the workspace's owner**, who holds that account and names its admins.

6. **A person removed from the roster stays admitted at the workspace's intake until the workspace
   owner's next `fugaro update`, and what they store there in that window is served to nobody.** The
   admission is the installation's Terraform, rendered from the roster by that update (R5, D2971), so
   it lags the roster by one update; a grant is issued only to a roster `human`, so nothing serves what
   they wrote (R3, §7). **Accepted by the workspace's owner** as a lag of the same class every
   workspace grant runs on (workspace-projects-users threat-model §6's clock), with the intake's own
   integrity bound as the limit of what the window exposes (R3 of dynamic delivery).

7. **A refused or failed grant starts the run on the member's shared entries.** D3147: a person whose
   grant fails — because the workspace's service is unreachable, because their container is missing, or
   because they were removed — launches anyway, and a run they expected on their own subscription
   spends the **workspace's** paid entries through their allow-list and the meter (D3147, D3144). The
   alternative the plan declined was refusing the launch, which would let a brief outage block every
   launch by a person who holds a token. **Accepted by the workspace's owner**, whose money the shared
   entries are.

8. **A roster person who holds a token learns whether a job account and a repository are a member's.**
   The grant route answers a grant or one class of refusal for a named pair, and "no token held" is
   answered before the node is read, so only a token-holder learns it (R3, §6). It is the workspace's
   own membership table and not a member's secrets, every refusal names nothing, and every grant and
   refusal is one audit line in the workspace's own record. R3 asks WC0 to state this residue rather
   than to close it. **Accepted by the workspace's owner**, whose table it is.

9. **One subscription, more runs.** A token served to several projects reaches its rate window sooner,
   and there is no workspace-wide pool to fail over within: pools are each member's own policy, and a
   personal token is never lent and never a pool's (spec §6, D2976, R11 of dynamic delivery).
   **Accepted by the person who stores the token**, who chose to store it once for their members.

10. **Every roster person sends their gcloud token to the workspace's service and its intake, and the
    workspace's admins can replay it.** On each launch and at the intake (WC9) the token goes to
    services the workspace's admins deploy; they, or a compromised workspace service, can replay it
    for about an hour after each launch and reach the person's projects outside the workspace: forge
    read mints, pairing, slips and intake writes, `fugaro_p_` secrets included where the person is an
    admin of that project. **Accepted by the owner** (2026-11-22, §11 finding 4), as a new residual risk.

## 11. Review findings and the owner's answers

An independent security review of this file (read-only, by an Opus security reviewer, 2026-11-22)
found the eight things below. Each is recorded with the owner's answer. **Answered by the owner
2026-11-22**, in the owner's own words through a question dialog; an agent only recorded them here, and
no agent answers one (reconciliation O1, now answered).

1. **High: the "about 25 hours" and "up to 24 hours" bound was false.** A workspace grant nobody has
   claimed never expires: `creds.Slip.Expires()` is `Claimed.At + MaxRuntime` and an unclaimed record
   has no creation time, so a replayer can take grants in the token's hour and claim each weeks later;
   the person cannot end them (rotating the token does not end a slip, and deleting then storing again
   re-arms them). **Owner: narrow it.** WC4 gives a workspace slip a claim deadline counted from the
   grant, chosen and pinned by WC4 (its length is WC4's to choose; this file states no number), and
   the document's bound is corrected to the token's hour plus that deadline plus 24 hours from the
   claim (§4, §5, §10 risks 2 and 3). Carried into WC4's acceptance and tests and the plan's §2.
2. **High: F24's cross-member reach needs no false address.** On every switched-on launch the launcher
   sends the person's gcloud-audience token to the member's own credentials service
   (`slipGrantOverService` and `slipIdentity` in `internal/cli/run.go`, and `serviceReadIdentity` and
   `pairIdentity`), whose deployment the member's admins control, and Cloud Run accepts it at any
   service the person can invoke (the `GcloudAudience` comment in `internal/credsvc/auth.go`), so
   "the replay and not the route is what has to be caught" and "every Fugaro service" were wrong, and
   D3145's premise "having launched the run" is checked by nothing. **Owner: narrow it.** A grant is
   bound to the members the person is a launcher of, checked against that member's own list. **This
   reopens D3145's `grants` mark**, the one record the workspace can read (reconciliation D3145, F18).
   The mechanism is an open design question for the briefs of WC4 and WC5: where the member's own
   list is read from, given that the workspace's service holds no grant to read it.
3. **Medium-high: the intake address is writable by any launcher.** It comes from
   `credentials_service.url` in the workspace project's published shared file, which any launcher can
   write (`internal/localcfg/shared.go`), and only the leading `fugaro-credentials-` label is checked
   (`validCredentialsServiceURL`), so any launcher of the workspace's project can collect every roster
   person's raw Claude token, which works outside Fugaro. **Owner: narrow it.** WC9 reads the address
   from a source only the workspace's admins write (WC9's acceptance). The same weakness for ordinary
   installations (`secrets user set`) is a follow-up to the dynamic-delivery work, listed below and not
   a task.
4. **Medium: the reverse direction.** Every roster person sends their gcloud token to the workspace's
   service and its intake (WC9), and the workspace's admins can replay it for about an hour after each
   launch (§4, "The reverse direction"). **Owner: accepted**, as new residual risk 10 (§10).
5. **Medium: a deletion is loud only for a run that already holds a slip.** On the next launch the
   grant answers `held:false` and the run starts on the shared entries and prints nothing
   (contract-person.md, R8). **Owner: accepted**, with the wording "a deletion is loud" corrected (§3,
   §10 risk 5); no new warning behaviour, because a new warning would be a product decision.
6. **Medium: the acceptances were not the owner's.** Only risk 2 and the in-member part of risk 3 carry
   the owner's words (O2), and "D3145 was answered with this residue in view" was unsupported (D3145's
   question does not mention the probe). **Owner: accepted risks 1 and 4 to 9**, one line each below;
   the unsupported sentence is struck (§6).
7. **Low: the check on every call needs the verified address**, and `creds.Slip` stores only the `sub`
   and the roster holds no id (F11). **Owner: yes.** WC4 stores the case-folded address at grant time
   (WC4's acceptance).
8. **Low: "the slip ends with the run's outcome" is no bound against a member's admins**, because for a
   workspace slip the outcome record sits in the member's own database, which its admins write.
   **Owner: yes**, the wording is corrected (§5).

**Acceptances, 2026-11-22, by the owner:** risk 1 (a workspace admin reads every token): accepted.
Risk 4 (runs draw on the person's own subscription by default): accepted. Risk 5 (whoever holds the
intake's account replaces or deletes any token): accepted. Risk 6 (a removed person stays admitted at
the intake until the next update): accepted. Risk 7 (a failed grant starts the run on the shared
entries): accepted. Risk 8 (the grant route's yes/no over a member's pairs): accepted. Risk 9 (one
subscription, more runs): accepted. Risk 10: accepted. Risks 2 and 3 were accepted in words earlier
(O2), on the bounds as corrected by findings 1 and 2.

**Follow-ups, not tasks.** The address `secrets user set` reaches for an ordinary installation has the
same weakness as finding 3, the service address read from the project's shared file that any launcher
can write; it goes to the dynamic-delivery work as one open item.
