# The workspace's credentials service: reconciliation

Checked against `origin/main` at `21b0a622b` on 2026-11-19. **Where this file disagrees with
[spec.md](spec.md), this file wins.** It cites paths and symbols, never line numbers. Decisions are
**D2980–D2987**, all answered by the owner on 2026-11-19; the settled decisions it builds on are
D1290–D1305 ([the workspaces design](../workspaces/reconciliation.md)) and D2960–D2979
([workspace credentials](../workspace-credentials/spec.md)).

## 1. What the spec asks for, and what the repository already has

| The spec's concept | What the repository has |
| --- | --- |
| The workspace's service | Library code only: `credsvc.Workspace`, `NewWorkspace`, `WorkspaceShared`, `WorkspaceMeter` in `internal/credsvc`. No caller outside tests, no deployment |
| The member table and the shared allow-lists | `projectpolicy.WorkspaceCredentials` with an encoder (`WorkspaceCredentialsNode`) and a decoder (`DecodeWorkspaceCredentials`). Neither has a caller outside tests |
| The cap meter | `credsvc.WorkspaceMeter` over `budget.ReserveWorkspaceSpend` and `SettleWorkspaceSpend`. Not called by the paid-call path |
| The workspace's bundle | The same Secret Manager container the workspace project's own installation already has (`credsvc.WorkspaceBundleSecretID` is `BundleSecretID` under the same name). `fugaro creds set` writes it today |
| "One code path" | `credsvc.Server` and its `Config`, built for exactly one installation |
| The cascade, project then workspace | Nothing. A run knows one service address and halts when that service refuses |

## 2. Assumptions

- **A1.** "The workspace's service" serves the member projects' runs. The workspace project's own
  runs keep using that project's ordinary service, which reads the same bundle and is not subject to
  the shared allow-lists: the bundle is that project's own.
- **A2.** "A project with no credential of its own never wakes its service" is about model and forge
  calls. Review signing, slips, workflow secrets and build credentials stay on the project's service
  and may wake it.
- **A3.** "Every credential kind" means the kinds the service serves today: Claude OAuth tokens, the
  Anthropic key, declared `anthropic` credentials, the Bitbucket tokens and read consumer, and the
  GitHub App key. Provider keys stay behind Check 50 (D150), and job-mounted keys are D2975's own
  spec.
- **A4.** A workspace whose Firebase is another project's (`workspaceSplitFirebase`) is out of
  scope: `fugaro update` skips the workspace root for it today and keeps doing so, with a refusal
  that says why.
- **A5.** Per-person tokens are out of scope (D2969), so the workspace's service keeps admitting job
  accounts and the workspace owner's own people, and no member's person.
- **A6.** No workspace serves a credential today, so there is no serving behaviour to migrate; what
  must converge is configuration (the pin, the member table, the node).

## 3. Findings

**F1 — Exists.** The per-member identity checks are written: `Workspace.Caller`, `Verify`, `Policy`,
`Shared` and `database` (`internal/credsvc/workspace.go`), with the member's database derived from
the verified issuer (`workspaceDatabase`). Consequence: the work is wiring and a seam, not a new
identity design.

**F2 — Gap.** `credsvc.Config` is single-installation: one `Auth Verifier`, one bundle loader, and
one database behind `Policy`, `State`, `Kills`, `Slips`, `Signs`, `Probes`, `WorkflowSecrets` and
`BuildPairs`. `Workspace.Verify` returns a `MemberCall`, not the `Identity` the routes take.
Consequence: "one code path" needs a per-call seam in `Server` that both an installation and a
workspace implement. R1, contract C1.

**F3 — Gap.** `internal/cli/credentials_serve.go` (`serveCredentials`, `credentialsServiceConfig`)
has no workspace mode and requires one installation's environment (`credentialsServiceEnvNames`).
Consequence: task WS5.

**F4 — Gap.** `deploy/terraform/gcp/roots/workspace/main.tf` holds `google_service_account.workspace`,
`google_project_iam_custom_role.workspace_database` and its binding, and nothing else: no Cloud Run
service, no bundle accessor, no invoke grant. `tf.WorkspaceNotCovered` refuses any plan of that root
that is more than those three. Consequence: WS6 adds the resources and changes the cover in one task.

**F5 — Wrong (a comment).** `internal/credsvc/bundle_workspace.go` says the workspace's root binds
the one secret grant. It does not (F4). Consequence: WS6 makes the comment true.

**F6 — Exists, and it shapes D2980.** The workspace's project is an ordinary installation, so
`ensureCredentialsService` and `deployCredentialsService` already deploy an installation service
there, reading the same bundle container. What tells the two apart in the design is identity and
admission: `google_service_account.credentials` with one `Authenticator`, against
`google_service_account.workspace` with a verifier per member. Consequence: D2980.

**F7 — Gap.** A member's run cannot reach a second service. `gateway.Upstream` has one `BaseURL`,
the run reads one address (`credentialsServiceURLEnv` in `internal/runner/gateway.go`), and
`localcfg.WorkspacePin` carries a Firebase project, an issuer, an assertion key and a service
account, and no address. `projectpolicy.Workspace` carries none either. Consequence: D2981, R3.

**F8 — Gap.** Nothing decides between two services. A typed refusal halts the run
(`gateway.Server.serviceAnswer`, `haltService`), and an unreachable service halts it at once
(`serviceUnreachable`). Consequence: D2982, D2983.

**F9 — Exists.** The run's gateway already reads the workspace's headers (`noteWorkspaceHeadroom`,
`WorkspaceRefusal`), and `DeliveredHeadroom` and `WorkspaceStop` exist with no caller outside tests.
The service-side writers `SetWorkspaceHeadroom` and `SetWorkspaceRefusal` have none either.
Consequence: WS8 and WS11 wire the two ends.

**F10 — Gap.** The paid-call path takes no reservation: `Server.forward` loops the candidates,
`Server.send` makes the call and `Server.answer` builds the answer (`cascade.go`, `forward.go`).
`probe.go` also calls `send`, and a probe is a paid call. Consequence: WS8 reserves before every
`send`, probes included, and settles in `answer`; a failed settle refuses (D1301 as amended).

**F11 — Gap.** The node has no writer and no reader that runs. `fugaro update`'s `workspaceStage`
publishes the workspace record (`projectpolicy.PathWorkspace`) from `workspace_members` and
`workspace_cap`, and never the node. Consequence: WS3, WS4.

**F12 — Gap.** `WorkspaceCredentials.Validate` refuses an unknown key inside a row and any version
other than `WorkspaceCredentialsVersion`, and the node has no migrator. An `all_members` key written
for a service of today's code would make it refuse the whole node. Consequence: D2977 needs the
version bump, a migrator and a guard in the writer; R7.

**F13 — Gap.** `Projects` in a shared entry is not checked against the node's `Members`.
Consequence: WS3's writer refuses a shared entry naming a project that is not a member.

**F14 — Rule.** The workspaces design left two answered items to "the task that gives the node a
writer": O3, each job account carries the repository it is a job of, and O4, a member's
`database_region`. `fugaro workspace member add` writes neither. Consequence: R6, in WS3 and WS4.

**F15 — Gap.** The shared pool is model-only. `creds.Shared` is a `creds.Credential` (name and
tier) plus `Projects`. The Bitbucket routes choose from `Bundle.BitbucketToken` and
`BitbucketTokens` with coverage from the installation's published node and `cfg.Repos`; the GitHub
routes use `Bundle.AppKey` with the repository from the installation's `JobAccount`. Consequence:
D2978's "every kind" needs the pool and the node to carry forge credentials, and the forge routes
to take their tables from the per-call seam. D2984, tasks WS14 to WS16.

**F16 — Rule.** The keep-warm beat is the run's: `startSlipBeat` and `beatSlip`
(`internal/runner/budget_session.go`) at `slipBeatEvery`, started only when the run claimed a slip.
The service's Terraform has `min_instance_count = 0` and no maximum instance count or concurrency
written. Consequence: D2985, D2986, WS12, WS6.

**F17 — Gap.** Each member job account needs `roles/run.invoker` on the workspace's service. For an
installation that grant is `google_cloud_run_v2_service_iam_member.credentials` in the job's own
module; for a workspace it is a grant in the workspace's project to accounts of other projects.
Consequence: the workspace root binds it from the member table, and a member that adds a repository
needs the workspace owner's `fugaro update` before that repository's runs reach the service. Stated
in WS6 and in the operator notes.

**F18 — Rule.** `SECURITY.md` states that a workspace's cap binds nothing today and that the meter,
the headroom and the alarm arrive together with the wiring, and
`TestSecurityMdStatesTheWorkspaceThreatModel` pins those sentences. Consequence: the task that
wires the meter changes the sentences, the test and `website/content/security.md` together (WS8,
and `.claude/rules/credentials-service.md` applies).

**F19 — Rule.** `TestDocsWorkspacesCitationsResolve` resolves every line citation in
`docs/workspaces/*.md` into `auth.go`, `server.go`, `workspace.go`, `workspace_credentials.go` and
`localcfg.go`. Consequence: every task that edits one of those files runs
`go test ./internal/cli/ -run DocsWorkspaces` and moves the citations in the same change.

**F20 — Gap.** `budget.ReconcileWorkspace` has no caller outside tests, and `fugaro doctor` has one
workspace check (`doctorWorkspaceSplitCheck`). Consequence: D2987, WS13.

**F21 — Gap.** Nothing published says which credentials a project holds itself. A member's policy
names the accounts it admits (`projectpolicy.MemberAdmitsShared` asks whether the name is a key of
the member's `accounts`), which is true of a workspace's credential and of the project's own alike.
Consequence: D2982 needs a published route per credential; R4.

**F22 — Open, answered here.** V5 of the workspaces design: D2973 and D2978 answer it, and R4 and
R5 say how.

## 4. Decisions

All answered by the owner on 2026-11-19.

| # | Question | Answer | Mark |
| --- | --- | --- | --- |
| **D2980** | How is the workspace's service deployed, given the workspace's project already runs an installation service on the same bundle (F6)? | A second Cloud Run service in the workspace's project, the same image and code, in workspace mode by its environment, running as the existing `fugaro-workspace` account | as recommended |
| **D2981** | How does a member's run get a credential the workspace holds (F7)? | The run's gateway calls the workspace's service directly, as a second upstream. Each member job account holds an invoke grant in the workspace's project | as recommended |
| **D2982** | How does a run choose between the two services (F8, F21)? | Once, at the run's start, from what the project publishes; the choice holds for the run | as recommended |
| **D2983** | What does a run do when the workspace's service cannot be reached? | It waits and retries for a bounded time, then halts with a reason that names the workspace's service. The project's service gets the same behaviour, because it is the same code | as recommended |
| **D2984** | In what order are the credential kinds built (F15)? | **All kinds at once**: model, Bitbucket and GitHub credentials are served by the workspace's service from the first release | **not the recommendation** (which was model first) |
| **D2985** | What keeps the service warm (F16)? | The run's beat, as Check 511, **with a way to turn it off**: "I'm not sure we actually need it" | the recommendation, **with the owner's addition** |
| **D2986** | At how many concurrent runs is the load check measured? | 100 | as recommended |
| **D2987** | Where does the reconciliation alarm run (F20)? | On demand: `fugaro doctor` and `fugaro workspace show`, from the workspace owner's machine | as recommended |

**What D2984 costs, stated because it is not the recommendation.** Git traffic depends on the
workspace's service from the first release, so Check 770 measures git pushes and clones beside
model calls, and the release that carries the service carries the forge slice with it.

## 5. Resolutions

Each has one defensible answer given the decisions and the owner's constraints.

- **R1. The seam.** `credsvc.Server` takes a per-call scope: the verified caller, and the policy,
  kill, state, outcome, table and offering readers chosen for that caller. An installation
  implements it with its one database and one table. A workspace implements it from
  `Workspace.Verify` and `Workspace.database`. No route is written twice. Contract C1.
- **R2. The mode.** One environment name selects workspace mode in `fugaro credentials serve`.
  Without it the process is exactly today's.
- **R3. The address.** The workspace's service address is a fifth field of the member's own pin,
  written by `fugaro workspace add`, and never read from anything the workspace publishes: a
  workspace must not be able to move a member's calls by publishing a different address. The
  member's `fugaro update` puts it in its jobs' environment. A pin without it is valid and means
  "this member takes no workspace credential", so an existing pin converges with no edit.
- **R4. The route, published.** The member's `fugaro update` works out, for each credential name
  the member's policy admits, whether the project's own bundle holds it, and publishes the route
  (`project` or `workspace`) beside the member's credential state. The run reads it at bootstrap
  (D2982). The project's copy wins when both exist (D2973). A name with no route is served by the
  project, as today.
- **R5. The fallback is still fenced.** A call routed to the workspace passes the workspace
  entry's allow-list, the member's own policy and the repository's admission, exactly as
  `Workspace.Shared` intersects them today (D1298, D2978).
- **R6. The table's two missing columns.** A member row carries `database_region`, and each job
  account carries the repository it is a job of (the workspaces design's O4 and O3).
  `fugaro workspace member add` takes both.
- **R7. `all_members`.** A new key of a shared entry, exclusive with `projects`. The node's version
  becomes 2 with a migrator. The writer refuses to publish version 2 to a workspace whose deployed
  service is older than the release that reads it, and the key is written only once WS8 has landed
  (D2977).
- **R8. State lives at the workspace.** Cooldowns, round-robin position and the spend figure for a
  workspace credential are written to the workspace's own database. The service holds no write on
  any member (D1299 as amended).
- **R9. Limits.** The workspace's Cloud Run service states its maximum instance count and its
  concurrency. The first values are the plan's; Check 770 confirms or corrects them at 100
  concurrent runs (D2986).
- **R10. The beat's switch.** A setting turns the workspace beat off. On is the default (D2985).
- **R11. Update with runs in flight.** A run in flight keeps the environment it started with, so
  it never sees a route or an address change. Deploying a new revision of the workspace's service
  drains the old one. `fugaro update` says so in the one line it prints when runs are running.
- **R12. Split Firebase.** For a workspace whose Firebase is another project's, `fugaro update`
  deploys no workspace service and says why (A4).

## 6. Open tensions and open items

No two answers pull against each other.

- **O1.** The first values of R9's limits are a guess until Check 770 runs. The owner runs the
  check.
- **O2.** D2983 changes what a run does when its **project's** service is unreachable (a bounded
  wait where it halts at once today). It follows from the one-code-path constraint; it is named
  here so it is not a surprise in review.
- **O3.** The workspaces design's own open checks U1 to U5 (a member job account admitted by a real
  deployment, and the rest) are still open. Checks 771 to 773 below cover U1 and U5; U3 and U4 stay
  that design's.

## 7. UNVERIFIED

- **U1.** Where exactly the route of R4 is published, and whether the member's policy must name an
  account before the workspace may serve it. Settled by WS9's first step, reading
  `internal/creds/repoauth.go` and `projectpolicy.MemberAdmitsShared`'s callers.
- **U2.** How a Bitbucket token's coverage and a GitHub App's installation are expressed for a
  member's repository in the workspace's node. Settled by contract C3.
- **U3.** Whether a database read through `fugaroWorkspaceRead` (`instances.get` only) is enough
  for the service to read a member's policy and run state on a real deployment. The workspaces
  design's own U1; settled by Check 771, owner-run.
- **U4.** The real cold-start time of the workspace's service and whether the beat is needed at all
  (the owner's doubt in D2985). Settled by Check 772, owner-run.
- **U5.** The exact hook in `Server.answer` where usage is known for the settle. Settled by WS8's
  red step.
- **U6.** Which test holds the resource count of `credentials.tf`
  (`TestCredentialsServiceResourcesAreCounted` is referenced; its file was not found), and whether
  the workspace root gets a like count. Settled in WS6.
