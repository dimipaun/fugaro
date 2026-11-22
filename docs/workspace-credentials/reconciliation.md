# Workspace credentials: reconciliation

Checked against `origin/main` at `2e8c7cb0b` on 2026-11-21, and re-read at `72859a77d` and at
`bbcebb1cb` the same day, after review rounds 1 and 3. **Where this file disagrees with
[spec.md](spec.md), this file wins.** It cites paths and symbols, never line numbers. The spec's own
decisions are D2960–D2979, all answered on 2026-11-19; this file adds **D3140–D3148**, answered by
the owner on 2026-11-21, every one as recommended (D3146 and D3147 were added by review round 2,
D3148 by round 3).

**How to read a decision number here.** D2960–D2969 are allocated twice on `main` (F1). In this
folder they always mean the workspace-credentials decisions of [spec.md](spec.md) §4 and §5.

**Amended 2026-11-22 by [model credentials routing](../model-credentials-routing/README.md)**, whose
task A2 writes this note, and nothing here changes. That plan gives a person's own token a model call,
served through that person's personal pool in the project (its PL4, D3179), which is the half F4 below
records as served by nothing. **D3141 stands**: a person's token at the workspace is served only where a
repository's model route is the workspace, and a project that carries a routing table serves the call
from its own service, so a project's own routing shadows it exactly as D3141's cost says. **D2976 is
amended by one case in a project** (its D3191): a run that resumes a person's paused run draws on that
person's pool, where a run launched by a service account is otherwise served no personal token. No
task, resolution or finding of this plan is withdrawn or amended by that one.

## 1. What the spec asks for, and what the repository has now

The spec was written on 2026-11-19, two days before this read. Most of what it proposes was planned
and largely built under other plans in between.

| The spec's concept | What the repository has on 2026-11-21 | Who owns it |
| --- | --- | --- |
| A shared account or key stored once at the workspace and served to the members its allow-list names (§3 item 1, D2969 first half) | Built. `fugaro workspace share` and `unshare` (`newWorkspaceShareCmd`, `internal/cli/workspace_share.go`), the published node at version 2 (`projectpolicy.WorkspaceCredentials`), the offering (`credsvc.WorkspaceShared`, `Workspace.Shared`) | [workspace-service](../workspace-service/README.md) |
| The workspace's service, deployed (D2968) | Built. `credentialsWorkspaceService` (`internal/cli/credentials_serve.go`) calls `credsvc.NewWorkspace`; the workspace root holds `google_cloud_run_v2_service.workspace` | workspace-service |
| The member's copy wins (D2973, V5) | Built, by repository and kind: `routeStage` and `kindHolder` (`internal/cli/update_route.go`), reported by `RouteKindLine` | workspace-service |
| One cascade for every kind; the holder proxies (D2978, D2979) | Built for model, Bitbucket and GitHub credentials; the Bitbucket token store in workspace mode (`workspaceForge.Bitbucket`) is still nil | workspace-service |
| `all_members` (D2977) | The type and the writer's fence exist (`workspaceCredentialsAllMembersFence`); no command writes it yet (WS19) | workspace-service |
| The cap meter | Built and handed to the scope; **not on the paid-call path**: `WorkspaceMeter.Reserve` has no caller outside tests (WS8, open as #3410) | workspace-service |
| An OpenRouter key through the service (§3 item 3, D2964, D2975) | A provider key is a bundle entry and a provider route is served through a project's service. **The workspace cannot share one**: `workspaceSharedKind` admits a model, Bitbucket or GitHub-App name and nothing else | [keys-through-service](../keys-through-service/README.md) for a project; **this plan** for the workspace (F7) |
| A person's own Claude token at the workspace (§3 item 2) | **Nothing**, and less than the spec assumed (F4): no model call can be served a person's own token anywhere, a single project included | **this plan** |
| A launcher the workspace's service can trust (D2970) | Nothing. Workspace mode refuses the slip routes and any call that presents a slip (`servesWorkspaceRoute`, `workspaceScope.AcceptsSlip`) | this plan |
| A member's person storing a token at the workspace (D2971) | Nothing at the workspace. A project's intake exists (`internal/intake`, `modules/installation/intake.tf`) and admits that installation's launchers only | this plan |
| The member's switch (D2972) | Nothing | this plan |

## 2. Assumptions

- **A1.** "A person's token" means a Claude subscription (OAuth) token under the person's own
  prefix, `fugaro_u_<id>_claude-oauth-token` (`secretname.User`). A person's forge token is not
  part of this plan: D2978 allows one at either level, and nothing in the spec asks for it to be
  served per person.
- **A2.** "The roster" is the workspace's roster of [workspace projects and users](../workspace-projects-users/README.md):
  `workspace.Roster` at `workspaceRosters/<name>` in the workspace's own database. Only its people
  of kind `human` are meant; a machine holds no personal token (D2976's consequence).
- **A3.** A project in no workspace is unchanged by this plan (D2966): no route, no reader and no
  refusal of an installation's service changes, and R7 of dynamic delivery (a missing personal name
  falls to the bundle) stays as it is there.
- **A4.** The workspace project's own runs keep using that project's ordinary service, as
  workspace-service's A1 says.
- **A5.** "The workspace's service" is the second Cloud Run service of D2980, in workspace mode, and
  this plan adds no third.

## 3. Findings

**F1 — Rule.** Decisions D2960–D2969 are allocated twice on `main`.
`docs/runs-ls-speed/README.md` takes D2960–D2967 and reserves D2968–D2969, and code cites them with
that meaning (`internal/runstore/prindex.go`, `internal/cli/docs_runs_ls_speed_test.go`). The spec's
sentence "no file on `main` and no open pull request uses D2960 or higher" was true when written
and false by the time it merged. D2973, D2975 and D2977 to D2979 are cited in code with this
spec's meaning. Consequence: R1.

**F2 — Wrong (stale).** Four statements of the spec's §2 and two of its §7 are no longer true: the
bundle is served to a member's run, the workspace root deploys a service, the node has a writer,
V5 is settled in code, and U4 ("there is no writer") and half of U2 are closed. The spec's header
sentence "this work waits on the workspace's service being deployed under its own plan" describes
a plan that has merged 19 of its 25 runs. Consequence: the table of §1 replaces the spec's §2;
`spec.md` is not edited.

**F3 — Exists.** Everything the spec proposes for shared entries is workspace-service's, built or
in flight there: D2968, D2973, D2977, D2978 and D2979, and the first half of D2969. Consequence:
this plan holds no task for them and depends on that plan's unmerged tasks by name (plan §5).

**F4 — Gap, and the headline.** No model call can use a person's own token today. The pieces exist
for a project: the intake writes `fugaro_u_<id>_<name>` containers, the grant route holds a
per-user name to its owner (`checkGrantedNames`, `internal/credsvc/slip.go`), and the model path
reads a per-secret container before the bundle (`Server.resolveEntry`, `DynamicSecrets.read`). But
nothing ever puts such a name on a candidate list. The service's candidates come from bundle
entries only, and the launcher's slip list is the crafts' resolved access names
(`slipCredentialNames`, `internal/cli/run.go`), which are declared credential names and cannot be
a `fugaro_u_` id. The header of `internal/credsvc/dynamicsecrets.go` says the same. The spec's §2
reads "`fugaro secrets user set` stores a secret under the person's own prefix" as if a run then
used it. Consequence: the personal half is not "move the token to the workspace". It has to build
the producer of the candidate, and D3140 asks whether to plan that now. *(Amended 2026-11-22: the
project half of the producer is planned — [model credentials routing](../model-credentials-routing/reconciliation.md)
D3179 and PL4 serve a person's own tokens from that person's personal pool in the project, so "no
model call can use a person's own token" holds at the workspace only, which is this plan's half.)*

**F5 — Rule.** The route between a project's service and the workspace's is chosen once per
repository and kind and holds for the run (D2982, D2989; `creds.Route`,
`internal/creds/route.go`). A model call names no credential, so a run's model calls go to one
service. A project that holds a model credential of its own therefore never calls the workspace
for a model, a person's token there included. Consequence: D3141.

**F6 — Gap.** The spec never says when a run draws on its launcher's token. With F4 there is no
existing behaviour to inherit. Consequence: D3142.

**F7 — Gap.** D2978 names an OpenRouter key among the credentials a workspace may hold.
workspace-service left provider keys out (its A3) and keys-through-service left the workspace's
service out (its spec's "Out of scope"), so neither plan lets a workspace share one, and
`workspaceSharedKind` refuses it. Consequence: task WC1.

**F8 — Rule.** The gate the spec records as "D150's gate stays" was narrowed the same day by
D3000 of keys-through-service: only a Claude model through a provider key waits for Check 50.
That plan's KS18 amends the spec's sentence. Consequence: this file uses the narrowed gate, and
WC1 keeps the refusal the selection already makes.

**F9 — Exists, and it shapes D3143.** The workspace's project is an ordinary installation
(workspace-service's F6), so `fugaro update` already deploys an intake there
(`intakeServiceName`, `internal/cli/secrets_intake.go`) with its own account and the one
name-prefix condition. What it lacks is admission: its invoker set is
`launcher_invoker_members`, the `user:` entries of that installation's own launchers
(`modules/installation/intake.tf`). Consequence: D3143.

**F10 — Rule.** Adding roster people to that installation's launcher variable would also admit
them to the workspace project's own credentials service and grant route, because
`launcher_invoker_members` is also the invoker set of that service (`iam.tf`). Consequence: the
intake's extra people are a variable of their own, read by the intake alone (R5, task WC3).

**F11 — Gap.** The roster is keyed by a person's address and the person prefix by the numeric
account id; the roster holds no id. Consequence: admission is by the verified token's address
against the roster, and the container's owner is the token's own `sub`, as the intake already does
(`(*Server).container`, `internal/intake/handlers.go`). Nothing has to map one to the other.

**F12 — Rule.** D2970 asks for a launcher the workspace's service can trust. The launcher class is
people only: `foldLauncherAccounts` (`internal/credsvc/launchers.go`) refuses a service account.
`Identity.Launcher` exists (the `rb` claim, `internal/credsvc/auth.go`) and no code reads it; it is
minted in the member's own Firebase project, so it is not evidence to the workspace. The identity
ticket of [launcher-does-not-mint](../launcher-does-not-mint/README.md) records a caller, in the
member's own database, which a member's admins can rewrite. Consequence: neither is used. The
workspace's service gets its own grant and claim, kept in its own database (R3).

**F13 — Rule.** Workspace-service's R18 says a call to the workspace's service carries nothing of
the member's, and that the scope refuses a call that presents a slip. D2970 needs a slip on those
calls. The two do not conflict once they are told apart: R18 is about a **member's** slip, whose
claim secret would be handed to a service outside the member (its F40). A slip the workspace's own
service granted is the workspace's secret, not a member's. Consequence: R4, and tasks WC4 and WC8
move the tests that pin the old wording.

**F14 — Gap (mechanism corrected after review round 2).** A grant answers a slip's id and nothing
secret (`SlipGrantResponse`, `internal/credsvc/slip.go`); the launcher hands the id to the job, and
the job's claim is answered the claim secret once, which the run keeps out of its environment
(`slipHold`, `internal/runner/budget_session.go`). The claim is held to the run's verified identity
and job account, and both are the member's: the identity is minted in the member's own Firebase
project, which its admins can forge (D1943's class), and the image the run executes is theirs. So
a member's admins can claim a person's slip first, or take the secret from a run they control, and
spend the slip until it expires (`maxSlipRuntime`, 24 hours). Consequence: stated as a residual risk for the threat model (WC0), not
closed: launching a run in a member lets that member spend the launcher's subscription for the
slip's lifetime, and no longer. It is the bound D2970 asked for (no spending without an act by the
person), not a stronger one. With D3142 the exposure starts without a flag: the switch that
turns it on is set by the member's admins, the party that can then spend the token.

**F15 — Rule.** `tf.Cover.WorkspaceNotCovered` (`internal/infra/tf/workspace.go`) admits exactly
the workspace root's resources: the service, one accessor on the bundle, and invoke grants to
member job accounts. An invoke grant to a person and a second accessor are refused today.
Consequence: WC2 changes the cover in the task that adds the resources, and admits the deletion of
a person's invoke grant so that removing someone needs no repair (as workspace-service's F25 did
for a member).

**F16 — Rule.** Three pinned statements become false: that the workspace's service admits no
member's person (`SECURITY.md`, `TestSecurityMdStatesTheWorkspaceThreatModel`; the workspaces
spec §4, F24 and F31), that no call to it carries a slip (workspace-service R18), and that only
member job accounts hold a grant on it. Consequence: each task that makes one false changes the
sentence, its test and `website/content/security.md` together (`.claude/rules/credentials-service.md`).

**F17 — Wrong (a citation).** D2971 cites "workspaces spec §4, F11" for the rule that the
workspace's service admits no member's person. F11 of the workspaces reconciliation is about the
gateway and the cap; the rule is §4 with F24 and F31, which the spec's own §2 cites correctly.
Consequence: none for the design.

**F18 — Exists.** D2961's premise moved. The roster now carries a `grants` list
(`workspace.Roster`, `internal/workspace/roster.go`), which the spec's review said it did not. The
roster's contract calls it a record of what the workspace wrote into a member and "never the grant
itself". Consequence: D3145.

**F19 — Rule.** The threat model of workspace-projects-users (`threat-model.md`) covers a
workspace admin reading a **member's** secrets, personal tokens included (its §7, residual risk
1). It does not cover personal tokens held at the workspace, an intake that admits roster people,
or a launcher class at the workspace. D2969 puts the person's prefix "after the threat model" and
D2971 says the amended rule "goes through the threat model". Consequence: WC0 writes that text
first, and every other personal task waits on the owner's answers to it.

**F20 — Rule.** D2825 says a workspace admin's terminal must not read personal tokens, and R8
gives every workspace admin owner on the workspace's project. The spec's §6 already names this as
a cost. Consequence: not reopened. WC0 states it as a residual risk with no technical fence, in the
words the existing threat model uses for the member's side.

**F21 — Gap.** Offboarding sweeps a person's containers through each linked member's intake
(`memberSweeper`, `internal/cli/workspace_offboard.go`) and never the workspace's. A person removed
from the roster would keep a token at the workspace. Consequence: WC10.

**F22 — Gap.** The meter's rule that a subscription call reserves nothing (workspace-service R15)
is written by token type and is not on `main` yet. D2965 "must follow the credential actually
served" (the spec's own note). Consequence: WC5 pins that a run which moved from the person's
token to a paid shared entry (D3144) reserves from that call on, and it waits on WS8.

**F23 — Gap (amended after review round 1).** How a member's person reaches the workspace's
intake. `secretsIntakeServiceURL` reads the address out of the selected project's own local config
(`intakeURLOf`, the swap of the service's name in `credentials_service.url`). The first draft put
the intake's address in the member's pin. That is the wrong direction: a pin is the member admin's
value, and `fugaro secrets user set` sends a person's raw token to the address, so a hostile
member admin could collect the tokens of everyone who trusted the workspace. Consequence: R13. The
address is never read from anything a member holds or publishes.

**F24 — Rule (review round 1).** A person's identity token for a service call is gcloud's, and
dynamic delivery accepted that it can be replayed while it lives (its D1939 and D1944). The grant
of R3 is asked at the workspace service's address, which a run and a launcher read from the
member's published route (workspace-service R3, R4). A member admin who publishes a false address
receives the token of whoever launches there and can replay it to the real services for its
lifetime: ask for grants as that person for jobs of that member, and replace or delete that
person's token at the intake. *(Reach corrected after review round 3.)* The services accept
gcloud's own audience (`GcloudAudience`, `internal/credsvc/launchers.go`), so the replay is not
limited to the workspace: it works at every Fugaro service that admits that person *(corrected again
by WC0's review, 2026-11-22: Cloud Run accepts the token at any Cloud Run service the person can
invoke, and the member's own service receives it on every switched-on launch with no false address;
the owner narrowed the grant, D3145 below)*, the intakes of
their other projects included, where the admin can replace or delete the person's own secrets and,
where the person is a project admin, that project's shared ones. It cannot read a token and cannot
run a job in another member.
Consequence: not closed here; WC0 states it beside F14 for the owner to accept or to ask for a
narrower token.

**F25 — Gap (review round 1).** The first draft gave three answers for a run in flight whose
launcher is offboarded: the roster was re-read only at the grant, R11 said "refused within a
minute", and WC10 said the run is not stopped while deleting the container it draws on.
Consequence: R3 as amended, one answer.

## 4. Decisions

The spec's D2960–D2979 keep their answers; §5 says how each lands. These nine were asked and
answered on 2026-11-21.

| # | Question | Answer | Mark |
| --- | --- | --- | --- |
| **D3140** | Most of the spec was built elsewhere (F3), and the personal half is larger than it assumed (F4). What does this plan cover? | **Both**: sharing a provider key, and the personal half, with every personal task behind a threat-model task whose findings the owner answers first. Not chosen: the remainder only, or the personal half only | 2026-11-21, as recommended. Why: the ask of 2026-11-18 is not met until a person's token works, and the gate keeps the security review in front |
| **D3141** | A project that holds its own model credential never calls the workspace for a model (F5). Which rule for a person's token at the workspace? | **It follows the route.** A personal token is served only in repositories whose model route is the workspace. Not chosen: per-run routing, which reverses D2989 for those runs | 2026-11-21, as recommended. Why: no new routing. Cost: a project that keeps a model credential of its own shadows every person's workspace token until that credential is removed |
| **D3142** | With the member's switch on and the person holding a token, when is it used (F6)? | **Automatically**: every run that person launches there draws on it; `fugaro run` says so in one line, and `--no-personal-token` skips it for one run. Not chosen: only on `--personal-token` | 2026-11-21, as recommended. Why: the person stored it to use it. Cost: a person spends their own subscription unless they pass the flag |
| **D3143** | Where do roster people's tokens go (F9)? | **The workspace project's existing intake**, admitting the roster's people as well; one container per person and name. Not chosen: a second intake with its own prefix | 2026-11-21, as recommended. Why: one code path. Cost, **corrected after review**: the question said the token would also serve the workspace project's own runs; it does not (A4, F4: no installation is served a personal token). What stands: one container per person in the workspace's project, and whoever holds the intake's account can replace any of them |
| **D3144** | D2974 covers a missing token. What when the person's token is there but exhausted mid-run? | **The run moves on to the shared entries** it is admitted to, through their allow-list and the meter. Not chosen: wait and never switch | 2026-11-21, as recommended. Why: it is what the cascade does. Cost: a heavy user's runs spend the workspace's paid entries once their own window is used up |
| **D3145** | Should the service also require the roster's `grants` list to name the person for that member (F18)? | **No.** Being on the roster and having launched the run is enough. Not chosen: require the mark | 2026-11-21, as recommended. Why: the list is a record and not a grant, and a person a member granted directly is not in it. **Reopened by WC0's review, answered by the owner 2026-11-22:** "having launched the run" is checked by nothing, so a grant is bound to the members the person is a launcher of, checked against that member's own list (threat-model §11, finding 2); the `grants` mark, the one record the workspace can read, is reopened for WC4 and WC5's briefs, which list the open design question |
| **D3146** | A person is removed from the roster while a run of theirs draws on their token. The spec's §6 says offboarding does not stop a run in flight (F25) | **The run halts within a minute**: the slip's owner is checked against the roster on every call (R3). Not chosen: let it finish on a grant that lasts up to 24 hours | 2026-11-21, added by review round 2, as recommended. Why: a removed person's token stops being spent at once. Cost: it reverses that sentence of the spec's §6, and it stops runs of someone removed from the workspace who still holds a role in that member |
| **D3147** | The person holds a token and the grant fails at launch | **The run starts on the shared entries**, with one line saying it is not on the person's own token and why. Not chosen: refuse the launch | 2026-11-21, added by review round 2, as recommended. Why: it is what a failed grant does inside one project today. Cost: a run the person expected on their own subscription spends the workspace's paid entries |
| **D3148** | `--workspace <name>` cannot tell the workspace's own project from a member's (R13). How does a person address the workspace's intake? | **By naming the workspace's project** with the existing `--project`; no `--workspace` flag. Not chosen: a new field in the workspace's shared config that marks it as the owner | 2026-11-21, added by review round 3, as recommended. Why: nothing resolves a name, so nothing can be spoofed. Cost: people must know the workspace's project name |

## 5. Resolutions

Each has one defensible answer given the decisions and the owner's constraints.

- **R1. The numbers stay.** `spec.md` is kept as received and D2968 is cited across
  workspace-service's documents and D2973, D2975 and D2977 to D2979 in code, with its meaning, so nothing is renumbered. Every document of this folder says which
  D2960–D2969 it means, and the README carries the note (F1).
- **R2. Where the spec's decisions land.**

  | Decision | Lands in |
  | --- | --- |
  | D2960 (stored once at the workspace) | WC3, WC9, as amended by D3143 and R13 |
  | D2961 (which members) | "wherever the person launched a run" (the spec's own review), confirmed by D3145; WC4 |
  | D2962 (owner-only), D2976 (never lent) | WC4: a grant names only the caller's own personal names, never a pool and never a `fugaro_p_` name. *(Amended 2026-11-22: D2976 by one case in a project — a run that resumes a person's paused run draws on that person's pool, [model credentials routing](../model-credentials-routing/reconciliation.md) D3191.)* |
  | D2963, D2977 (`all_members`) | workspace-service WS19 |
  | D2964, D2975 (provider keys) | keys-through-service for a project; WC1 for the workspace |
  | D2965 (not counted against the cap) | workspace-service WS8 (R15), and WC5 for the move of D3144 |
  | D2966 (a project in no workspace) | A3; no task |
  | D2967 (a slice of workspace-projects-users) | superseded by D2968 and D3140: its own plan, this one |
  | D2968 (the service first) | workspace-service |
  | D2969 (accounts and keys first, the person's prefix after the threat model) | first half workspace-service; second half WC0, then the rest |
  | D2970 (its own grant route and launcher class) | WC4, WC7, WC8 |
  | D2971 (roster people may store a token) | WC2, WC3, WC9 |
  | D2972 (one switch in the member's policy) | WC6 |
  | D2973, D2978, D2979 | workspace-service; D3141 states D2973 for a personal token |
  | D2974 (a missing token refuses) | WC5 |

- **R3. The launcher class of the workspace's service.** Workspace mode serves the grant route and
  the claim route (`SlipPath`, `SlipClaimPath`). The grant admits a verified person whose address
  is a `human` of the roster; the roster is re-read as the node is (60 seconds, and a roster that
  cannot be re-read refuses). *(Amended after review round 1, F25; the halt is the owner's answer
  D3146.)* The same check is made on
  every call under a workspace slip, against the same copy: a call whose slip's owner is no longer
  on the roster is refused, so a removed person's grants and runs in flight stop within a minute,
  whether or not the sweep of their containers succeeded. The claim is checked the same way. A
  roster that cannot be re-read refuses the call as unavailable, and the run waits for it as it
  waits for a workspace service it cannot reach (D2983), never moving to another credential
  meanwhile. Such a run halts with a reason that
  names the removal; it does not move on to the shared entries. A grant names the
  member's job account, the repository and the run, and the service holds the account to a member
  row of the node and the repository to that account's list before it writes anything. Every refusal of a grant is one class that names no account, repository or
  person, and each grant and each refusal writes one audit line. "No token held" is answered before the
  node is read, so only a person who holds a token learns anything from a slip or a refusal:
  whether a job account and repository are a member's. WC0 states that residue. The slip is
  kept in the workspace's own database (R8 of workspace-service). The claim is the member job
  account's, as today. An addition to the roster still waits for the workspace owner's
  `fugaro update`, which binds the person's invoke grant (D2971).
- **R4. Which slip a call may carry.** A call to the workspace's service carries a slip only when
  the workspace's service granted it, and never a member's (F13). The gateway keeps the two apart
  by upstream: the project's slip goes to the project's service and the workspace's to the
  workspace's.
- **R5. The intake's people.** The intake's invoker set and its in-process list take one more
  input, the roster's people, through a variable of their own that nothing else reads (F10). It is
  empty for every installation that is not a workspace's project. The `fugaro_p_` half of the
  intake is unchanged and stays the workspace project's admins'.
- **R6. What the workspace's service reads.** Its account gains one accessor in the workspace's
  project, conditioned to the `fugaro_u_` prefix and to the one name this plan serves (a name that
  ends in `_claude-oauth-token`), so it reads no other personal secret a person keeps there
  *(narrowed after review round 3)*. It holds none on `fugaro_p_`: a project-shared
  secret of the workspace's project is not served to a member; what is shared is a shared entry
  (D2976).
- **R7. The candidate.** For a call that carries a workspace slip naming the person's token, with
  the member's switch on, the workspace's offering puts that token first, as one more subscription
  candidate, ahead of the shared entries the call is admitted to. A container that is missing or
  has no version refuses the call and names `fugaro secrets user set`; it never falls
  to a bundle entry (D2974). A read that failed refuses, as today. A token that is present and
  exhausted is skipped by the cascade's existing rule and the next candidate is tried (D3144).
- **R8. A person with no token.** *(Amended after review round 1.)* `fugaro run` asks the
  workspace for a grant only when the repository's model route is the workspace (D3141) and the
  member's switch is on. The service answers whether the caller holds a token: with none there is
  no slip, and the run is served the shared entries exactly as before. The launcher never calls
  the intake. A grant that is refused, or a service that cannot be reached, starts the run on the
  shared entries with one line that says so and why (D3147); those entries are served through
  their allow-list and the meter, which is what D2974 guards. A run launched by a service account asks for none (D2976's consequence). *(Amended 2026-11-22: except a run that resumes a person's paused run, which draws on that person's pool — D2976's one case in a project, [model credentials routing](../model-credentials-routing/reconciliation.md) D3191.)*
- **R9. The member's switch.** One key at the top level of the member's published policy, off when
  absent, set by the member's admin and published by the member's `fugaro update`. It is read by
  the workspace's service, which is the only enforcer, so an older reader that lists the key as
  unknown and decodes past it loses nothing, and the policy's version does not move.
- **R10. Nothing moves.** No command copies a token from a project to the workspace: values written
  by hand cannot be read back (R6 of dynamic delivery). A person stores the token once at the
  workspace. A project's own model credential keeps shadowing it (D3141) until the project's admin
  removes that credential, and the route line of `fugaro update` already says which copy is used.
- **R11. Update with runs in flight.** A run in flight keeps the slip and the route it started
  with. Two new steps can reach one, and `fugaro update` names each in a short line: the
  member's switch turned off, which the service refuses from its next read of the member's policy;
  and a person removed from the roster, whose runs halt within a minute (R3). A new revision of
  the workspace's service already has its line (`workspaceServiceRevisionStale`,
  `internal/cli/update_in_flight.go`).
- **R12. An unlinked member.** A member that has not run `fugaro workspace project link` (D2819)
  takes none of the personal half: its people are not managed through the roster, and the spec's §6
  says so. Its shared entries are unaffected.
- **R13. The intake's address is the workspace's own.** *(Review round 1, F23; amended by D3148
  after round 3.)* A person stores a token with the existing command and the workspace's own
  project named: `fugaro secrets user set --project <the workspace's project>`. The address is
  read out of that project's config on the person's machine, which the person has from the
  workspace's owner as for any project. There is no `--workspace` flag and nothing resolves a
  workspace's name to a project: the second draft tested for `workspace_members`, a key
  `(*Config).Shared` strips from the config other people receive (`internal/localcfg/shared.go`),
  so it would have refused everyone but the owner. No pin, route or policy of a member carries
  the intake's address.

## 6. Open tensions and open items

No two answers pull against each other.

- **O1. Answered by the owner 2026-11-22.** The owner answers every finding of WC0's independent
  review before any other personal task is launched, as WU0 was handled. The eight findings and the
  owner's answers are in [threat-model.md](threat-model.md) §11; the personal tasks, WC2 onward, are
  launchable by their plan edges.
- **O2.** F14's residual risk is the owner's to accept in WC0's review, in words: a member's admins
  can spend a person's subscription in that member for up to 24 hours after that person launched a
  run there (its bound is a claim deadline counted from the grant, chosen and pinned by WC4, plus 24
  hours from the claim, per the owner's answer to finding 1). F24's replay is the second, and the
  owner narrowed its cross-member reach to the members the person is a launcher of (finding 2): a member admin can act
  as a person who launched there while that person's identity token lives, about an hour, at
  every Fugaro service that admits the person; a grant taken in that hour lasts up to 24 more.
- **O3.** D3141's cost falls on a workspace whose projects still hold model credentials of their
  own. The plan tells an admin which copy is used and does not remove anything.
- **O4.** Five tasks of another plan are waited on and are not merged: workspace-service WS8
  (#3410, a draft), WS18, WS19, WS21 and its I1. The plan's §5 says which task waits on which. keys-through-service
  KS4 and workspace-projects-users WU7 were prerequisites at the first read and merged the same
  day (#3454, #3417).
- **O5.** keys-through-service's KS18 edits `docs/workspace-credentials/spec.md` (one sentence, F8).
  This pull request does not touch that file, so the two do not conflict.
- **O6.** The cost stated with D3143 was corrected after the owner answered it (review round 1):
  the question said a token would also serve the workspace project's own runs, and it does not.
  The corrected cost is the lesser one; it is listed here for the owner to see.

## 7. UNVERIFIED

The spec's own §7 uses U1 to U4; these continue from U5.

- **U5.** Whether a real workspace service can read a `fugaro_u_` container through a
  prefix-conditioned accessor in the workspace's project the way a project's service does. Settled
  by Check 850, owner-run.
- **U6.** Whether a person who holds only an invoke grant on the two services in the workspace's
  project (no role on the project itself) is admitted by the platform with a gcloud identity token.
  Settled by Check 850.
- **U7.** How a named provider entry (a declared `openrouter` credential, keys-through-service KS1)
  is keyed in the workspace's bundle, and whether `credsvc.CredentialKey` reads it as
  `KindProvider`. WC1's red step settles it.
- **U8.** Whether the member's settings layer or its local config is the right home for the switch
  of R9. Contract C1 settles the name and the home before either side is written; WC6 is the only
  level-1 writer of `internal/localcfg/localcfg.go`, so either answer fits the levels.
- **U9.** Whether the crafts a member's repository declares are honoured for a call routed to the
  workspace, where no account header is sent (workspace-service R18). It decides nothing here: R7
  places the personal token first whatever the craft, and WC5's red step records what it finds.
- **U10.** Whether a roster person who is no launcher of the workspace's project can load that
  project's config with the grants WC2 and WC3 give (R13 and D3148 say where it must come from, not that
  it loads). WC9's red step shows it over the fakes and Check 850 on a real workspace.
