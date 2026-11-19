# The workspace's credentials service: reconciliation

Checked against `origin/main` at `21b0a622b` on 2026-11-19. **Where this file disagrees with
[spec.md](spec.md), this file wins.** It cites paths and symbols, never line numbers. Decisions are
**D2980–D2992**, all answered by the owner on 2026-11-19 (D2988–D2991 were added by review round 1,
which also amended D2987; D2992 by round 3); the settled decisions it builds on are
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
  Anthropic key, declared `anthropic` credentials, the Bitbucket tokens and the GitHub App key, for
  the routes a run calls (R14). The Bitbucket read consumer is not one of them (D2988). Provider keys stay behind Check 50 (D150), and job-mounted keys are D2975's own
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
Consequence: WS8 writes the headers the gateway already reads. `WorkspaceStop` stays without a
caller: its own comment says ending a run needs a halt reason and a scope the run record's closed
vocabulary does not have, which is a wire change of its own (declined here, plan §10).

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
Consequence: WS4's writer refuses a shared entry naming a project that is not a member.

**F14 — Rule.** The workspaces design left two answered items to "the task that gives the node a
writer": O3, each job account carries the repository it is a job of, and O4, a member's
`database_region`. `fugaro workspace member add` writes neither. Consequence: R6, in WS3 and WS4.

**F15 — Gap.** The shared pool is model-only. `creds.Shared` is a `creds.Credential` (name and
tier) plus `Projects`. The Bitbucket routes choose from `Bundle.BitbucketToken` and
`BitbucketTokens` with coverage from the installation's published node and `cfg.Repos`; the GitHub
routes use `Bundle.AppKey` with the repository from the installation's `JobAccount`. Consequence:
D2978's "every kind" needs the pool and the node to carry forge credentials, and the forge routes
to take their tables from the per-call seam. D2984, tasks WS15 to WS18.

**F16 — Rule.** The keep-warm beat is the run's: `startSlipBeat` and `beatSlip`
(`internal/runner/budget_session.go`) at `slipBeatEvery`, started only when the run claimed a slip.
The service's Terraform has `min_instance_count = 0` and no maximum instance count or concurrency
written. Consequence: D2985, D2986, WS13, WS6.

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
workspace check (`doctorWorkspaceSplitCheck`). Consequence: D2987 as amended by D2991, WS14.

**F21 — Gap.** Nothing a run can read says which credentials a project holds itself. A member's policy
names the accounts it admits (`projectpolicy.MemberAdmitsShared` asks whether the name is a key of
the member's `accounts`), which is true of a workspace's credential and of the project's own alike.
Consequence: D2982 needs a published route; R4.

**F22 — Open, answered here.** V5 of the workspaces design: D2973 and D2978 answer it, and R4 and
R5 say how.

**F23 — Rule (review round 1).** A run cannot read the credential state: the database rules hold
`"creds": { ".read": false, ".write": false }` (`internal/budget/rules/rules.json.tmpl`). The first
draft of R4 put the route there. Consequence: the route lives in a policy-side leaf a run may read
and no run may write, which needs a rules change, a `RulesVersion` bump and a convergence test
(R4, WS22). This settles U1.

**F24 — Gap (review round 1).** The routes verify ten caller classes between them
(`VerifyOrchestrator`, `VerifyBuildCaller`, `VerifyReadCaller`, `VerifyPersonCaller`,
`VerifyLauncher`, `VerifyVerifier`, `VerifyRepoBuild` and the job classes), and `Workspace.Caller`
admits only a member's job accounts and the workspace owner's people. Consequence: workspace mode
serves a stated set of routes and refuses the rest (R14), and a member's coordinator, orchestrator
and image builds are not served by it (D2988).

**F25 — Gap (review round 1).** `tf.WorkspaceNotCovered` refuses every action but create, update,
no-op and read, so removing a member, which deletes its invoke grants, would refuse the plan and
leave an operator repair. It also takes the project and the account id only, so admitting member
principals changes its signature and its caller in `internal/cli/workspace_create.go`. Consequence:
WS6 changes the cover to admit the deletion of an invoke grant and nothing else, with its caller.

**F26 — Gap (review round 1).** `WorkspaceConfig.Members` is read once, in `NewWorkspace`. A member
added or an entry shared after the service started would not be seen by a running instance.
Consequence: R13.

**F27 — Gap (review round 1).** `WorkspaceMeter.Reserve` takes a worst-case amount, and
`cascade.go`, `forward.go` and `server.go` hold no price. Consequence: R15.

**F28 — Rule (review round 1).** The workspaces design's O3 answer has two halves: the column, and
holding the run's verified slug to it. The first draft carried only the column. Consequence: WS2
enforces the pairing in the workspace's scope and moves the test and the `SECURITY.md` sentence
that describe the gap.

**F29 — Rule (review round 1).** `SECURITY.md` pins, by test, "Unreachable halts, never falls back"
(`internal/cli/docs_security_test.go`) and that no member holds a grant on the workspace's project.
D2983 and D2981 change both. Consequence: WS12 and WS6 own those sentences.

**F30 — Rule (review round 1).** The run's gateway sends the run's Firebase token with a service
call (`Service.RunIDToken`), and that token can write the run's own nodes in the member's database.
Sent to the workspace's service it is in the hands of a service outside the member. This is the
identity design the workspaces review accepted (W6: `verifyRun` under each member's issuer), and it
weakens the sentence that the workspace's service cannot write a member. Consequence: O4, and WS11
owns the sentence.

**F31 — Gap (review round 1).** `budget.WorkspaceLedger` is built by nothing that runs, no member
records what the workspace paid for it, and a member grants its read only to the workspace's
service account. The alarm of D2987 cannot be computed from the owner's machine. Consequence:
D2991.

**F32 — Gap (review round 2).** A repository's own job is not given the service's address by
Terraform: the launcher sets it at launch (`credentialsServiceLaunchEnv`, `internal/cli/run.go`),
and only shared project jobs and review variants get it from `internal/infra/projectjobs.go`. An
address a launcher supplies is an address a launcher can move, and the run sends its own token
there (F30). Consequence: R3 as amended: the run reads the address from the member's own published
record, never from its environment, and the first draft's task WS10 is dropped.

**F33 — Gap (review round 2).** The beat posts the slip's id and claim secret (`beatSlip`,
`internal/runner/budget_session.go`) and starts only when a slip was claimed, and R14 refuses the
slip routes in workspace mode. Reusing it would send a member's slip secret to a service outside
the member, to be refused. Consequence: R14 adds a keep-warm route that carries no secret.

**F34 — Gap (review round 2).** Admission is per repository (R5), and a route per project and kind
would send every repository of a project to `project` as soon as one of them is covered there.
Consequence: R4 as amended: the route is per repository and kind.

**F35 — Gap (review round 2).** `ContextTokenPath` (a run's GitHub context tokens, signed with the
App key) and `VerifyPath` (review verification) were in neither list of R14. Consequence: R14 as
amended.

**F36 — Gap (review round 2).** A member job account with no invoke grant yet (F17's expected
case) is answered by the platform with a bare 403, which is neither a typed refusal nor an
unreachable service. Consequence: R16.

**F37 — Gap (review round 3).** A job account is not always one repository's. A shared project job
serves several: `credsvc.SharedJobAccount` carries an address, a profile and a list of
repositories (`SharedJobAccountsFor`, `internal/credsvc/server.go`), deployed by
`internal/infra/projectjobs.go`, and the shared-jobs-default work makes it the usual shape. A
member row whose accounts each carry exactly one repository would refuse every such run.
Consequence: D2992, R6 as amended.

**F38 — Rule (review round 3).** A member reads nothing the workspace publishes: the workspace's
database rules (`internal/budget/rules/workspace.json.tmpl`) deny every read, and the workspaces
documents pin that a member holds no grant there. So a member's `fugaro update` cannot know what
the workspace shares. Consequence: R4's report says only what the member can know.

**F39 — Rule (review round 3).** A rules change moves more than the template: the untagged
`TestRulesGolden` and `TestRulesVersionUnchanged` (`internal/budget/rules/rules_test.go`, with
`testdata/rules.golden.json`), and Check 82's row in `docs/gcp-live-checklist.md`, which a
documents test holds to the generated size and the version. Consequence: WS22's files, and WS20
after it.

**F40 — Rule (review round 4).** The run's gateway sends the slip's id and its claim secret on
every service call once a slip is set (`Server.serviceUpstream` and `Service.Post`,
`internal/gateway/service.go`; `HeaderSlip`, `HeaderSlipClaim` in `internal/credsvc/wire.go`), and
the server checks them on its common path (`verifyCallSlip`). A second upstream built the same way
would hand a member's slip secret to the workspace's service on every call. Consequence: R18.

**F41 — Rule (review round 4).** A new command, flag or update stage is counted in more places than
its own file: the CLI reference (`TestCliReferenceNamesEveryCommandAndFlag`,
`website/content/cli-reference.md`), the closed-stdin harness
(`internal/cli/noprompt_harness_test.go`), the list of writing commands
(`internal/cli/report_exempt.go`), the stage refusal table (`internal/cli/setup_refusals_test.go`)
and the settings-to-stage map (`internal/apply/steps.go`). Consequence: WS3, WS9 and WS19 name them.

**F42 — Wrong (review round 4), corrects F37.** `SharedJobAccount.Repos` is the profile's
`allow_repos` patterns, checked by `projectpolicy.AdmitRepo` and then by the member's live profile
gate (`pairShared`, `internal/credsvc/review.go`). The workspace's check compares concrete slugs
(`canonicalOfSlug`). Consequence: R6 says concrete repositories and refuses a pattern. The residual
is stated: the workspace owner's list for a shared job can be wider than the member's profile at
that moment; the member's own policy is still asked through R5's intersection.

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
| **D2987** | Where does the reconciliation alarm run (F20)? | On demand: `fugaro doctor` and `fugaro workspace show`, from the workspace owner's machine | as recommended; **amended after review**: deferred by D2991, because it cannot be computed there (F31) |
| **D2988** | A member's coordinator, orchestrator and image builds also use forge credentials. What serves them when the member's only forge credential is the workspace's (F24)? | The workspace's service serves a member's **runs only**, for now. A member that uses an orchestrator, a coordinator or image builds keeps its own forge credential, which then also wins for its runs | added by review round 1; as recommended |
| **D2989** | Does a run fall through to the workspace when the project's own model credential is exhausted mid-run? | No. The route is by kind and fixed at the run's start; a project that holds a usable model credential uses its own service for the whole run and waits when exhausted, as today | added by review round 1; as recommended |
| **D2990** | Is there a per-member share of the workspace cap? | No: one cap for now. Each member's own caps still bound that member | added by review round 1; as recommended |
| **D2991** | The alarm cannot be computed as D2987 placed it (F31). What now? | **Deferred.** The cap is enforced at the meter without it; the alarm stays unbuilt, `SECURITY.md` keeps saying so, and it gets its own spec | added by review round 1; as recommended |
| **D2992** | Does the workspace's service serve a member's runs on shared project jobs (F37)? | **Yes.** A member job account carries a list of repositories, and the run's repository must be one of them | added by review round 3; as recommended |

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
- **R3. The address.** *(Amended after review round 2, F32.)* The workspace's service address is a
  fifth field of the member's own pin, written by `fugaro workspace add`, and never read from
  anything the **workspace** publishes: a workspace must not be able to move a member's calls. The
  member's own `fugaro update` publishes it inside the route record of R4, and the run reads it
  there at bootstrap. It is never a job or launch variable, so no launcher can move it. A pin
  without it is valid and means "this member takes no workspace credential", so an existing pin
  converges with no edit.
- **R4. The route, published, by repository and kind.** *(Amended after review rounds 1 and 2,
  F23, F34.)* A model call names no credential, so the route is per kind of credential (model,
  Bitbucket, GitHub), and admission is per repository, so it is per repository too. The member's
  `fugaro update` works out, for each repository and kind, whether the project's own bundle holds
  a credential the member's policy admits for that repository, and publishes `project` or
  `workspace`, with the address of R3, in one policy-side record that a run may read and no token
  but the member's admins' may write. The run reads it once at bootstrap and keeps it (D2982,
  D2989). The project's copy wins when both exist (D2973). `fugaro update` reports, for
  each repository and kind it serves from the project's own copy, that a workspace credential of
  that kind is not used; it cannot name the workspace's entries, which a member cannot read (F38). A repository and kind with no route is served by the project,
  as today. `fugaro creds set` ends by naming `fugaro update`, and `fugaro doctor` flags a route
  the bundle no longer matches.
- **R5. The fallback is still fenced.** A call routed to the workspace passes the workspace
  entry's allow-list, the member's own policy and the repository's admission, exactly as
  `Workspace.Shared` intersects them today (D1298, D2978).
- **R6. The table's two missing columns.** *(Amended after review round 3, D2992.)* A member row
  carries `database_region`, and each job account carries the concrete repositories it is a job
  of, never a pattern (F42): one for a repository's own job, several for a shared project job (the workspaces design's O4 and
  O3). `fugaro workspace member add` takes both, and the scope holds the run's verified
  repository to that list.
- **R7. `all_members`.** A new key of a shared entry, exclusive with `projects`. The node's version
  becomes 2 with a migrator. The guard for an older service is an order, not a version probe: the
  workspace owner's `fugaro update` publishes the node only after its own deploy of the service
  succeeded in the same update, so the service that reads the node is never older than the CLI that
  wrote it. The key is written only once WS8 has landed and only for a workspace with a cap set
  (D2977).
- **R8. State lives at the workspace.** Cooldowns, round-robin position and the spend figure for a
  workspace credential are written to the workspace's own database. The service holds no write on
  any member (D1299 as amended).
- **R9. Limits.** The workspace's Cloud Run service states a maximum of 10 instances and a
  concurrency of 40 per instance. They are first values: Check 770 confirms or corrects them at 100
  concurrent runs (D2986).
- **R10. The beat's switch.** A setting turns the workspace beat off. On is the default (D2985).
- **R11. Update with runs in flight.** A run in flight keeps the environment and the route it
  started with. Two steps can still reach one, and `fugaro update` names each in a short line, as
  the HARD RULE asks: an entry unshared or a member removed, which the service refuses from its
  next read of the node (R13), and a new revision of the service, which drains the old one.
- **R12. Split Firebase.** For a workspace whose Firebase is another project's, `fugaro update`
  deploys no workspace service and says why (A4).

- **R13. The service re-reads its node.** The workspace's scope re-reads the published node when
  it is older than 60 seconds, conditionally, so a member added or an entry shared or unshared
  takes effect within a minute with no new revision (F26). A member's new repository still needs
  the workspace owner to declare it and run `fugaro update`: for the invoke grant when it has a
  job of its own (F17), and for the account's list when it runs on a shared project job.
- **R14. What workspace mode serves.** *(Amended after review round 2, F33, F35.)* The model
  routes; the forge routes a run calls (the git proxy, a run's own token mint and its context
  tokens); and a keep-warm route that takes the job's identity and carries no secret. Every other
  route (review signing and verification, slips, workflow secrets, build tokens, the read mints,
  the merge, label and issue routes) is refused in workspace mode with a typed refusal, and a
  member's run keeps calling its own project's service for them (A2, D2988).
- **R15. What is reserved.** For a paid credential the reservation is the call's worst case from
  the price table (`internal/pricing`), settled on the answer's usage. A stream cut before its
  usage arrives settles at the worst case. A model with no price is refused. A call on a
  subscription token reserves nothing and is counted in the notional figures, as an OAuth run is
  today (D1300, D2965). A reservation whose call never settles expires at
  `budget.WorkspaceReservationTTL`.
- **R16. A missing invoke grant is a refusal that names the fix.** *(Review round 2, F36.)* The
  gateway reads a platform 403 from the workspace's service as "this repository's job is not yet
  granted": the run halts at once with a reason that names the workspace owner's `fugaro update`.
  It is not waited for.
- **R18. The workspace's upstream carries no member secret.** *(Review round 4, F40.)* A call to
  the workspace's service carries the job's identity and the run's token and nothing else of the
  member's: no slip id, no claim secret, no auth-mode or account header. The workspace's scope
  refuses a call that presents a slip.
- **R19. A repository the workspace does not list is a refusal that names the fix.** *(Review round
  4.)* A run whose repository is not in its job account's list is refused by the scope with a typed
  reason, and the run halts naming the workspace owner's `fugaro update`, as R16 does for a missing
  grant.
- **R17. A node that cannot be re-read refuses.** When R13's re-read fails and the copy held is
  older than 60 seconds, the scope refuses the call; it never serves from a stale table.

## 6. Open tensions and open items

No two answers pull against each other.

- **O1.** The first values of R9's limits are a guess until Check 770 runs. The owner runs the
  check.
- **O2.** D2983 changes what a run does when its **project's** service is unreachable (a bounded
  wait where it halts at once today). It follows from the one-code-path constraint; it is named
  here so it is not a surprise in review.
- **O4.** F30: the run's Firebase token reaches the workspace's service. It is the accepted
  identity design, and it is named here for the independent security review of C1 and WS11 to
  confirm or to ask for a narrower token.
- **O5.** The graph cannot record that the tasks building a contract's other side wait on that
  contract (the plan's §5 says which). A launcher follows the plan's levels table, not the graph's
  ready column.
- **O6.** One member can use up the workspace's cap for the others (D2990). Stated in
  `SECURITY.md` by WS8.
- **O7.** Turning the feature on is configuration, not an upgrade: the workspace owner declares
  members and shares entries (`fugaro workspace member add`, `fugaro workspace share`) and each
  member pins the address (`fugaro workspace add`), each followed by `fugaro update`. An
  installation that uses none of it needs only `fugaro update`. **Confirmed by the owner on
  2026-11-19:** opting in to a feature is configuration, and it satisfies the one-command rule.
- **O3.** The workspaces design's own open checks U1 to U5 (a member job account admitted by a real
  deployment, and the rest) are still open. Checks 771 to 773 cover U1; U5's alarm half waits on the alarm's own spec (D2991); U3 and U4 stay
  that design's.

## 7. UNVERIFIED

- **U1.** *Settled by review round 1 (F23):* the route cannot live beside the credential state; it
  is a policy-side leaf. What stays unverified is whether a member's policy must name an account
  before the workspace may serve it (`projectpolicy.MemberAdmitsShared`); WS9's first step settles it.
- **U2.** How a Bitbucket token's coverage and a GitHub App's installation are expressed for a
  member's repository in the workspace's node. Settled by contract C3.
- **U3.** Whether a database read through `fugaroWorkspaceRead` (`instances.get` only) is enough
  for the service to read a member's policy and run state on a real deployment. The workspaces
  design's own U1; settled by Check 771, owner-run.
- **U4.** The real cold-start time of the workspace's service and whether the beat is needed at all
  (the owner's doubt in D2985). Settled by Check 772, owner-run.
- **U5.** The exact hook in `Server.answer` where usage is known for the settle. Settled by WS8's
  red step.
- **U6.** *Settled by review round 1:* `TestCredentialsServiceResourcesAreCounted` is in
  `deploy/terraform/terraform_test.go`. Whether the workspace root gets a like count is WS6's.
