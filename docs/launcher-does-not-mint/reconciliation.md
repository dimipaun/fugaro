# A launcher does not mint a run's identity: reconciliation

This is the spec as amended against the code. **Where it disagrees with [spec.md](spec.md), this
document wins.** First checked against `origin/main` at `7987acb99` on 2026-11-20. Main then
moved: `c16781dc8` was merged into this branch during review round 1 and `588750891` before the
push, and the symbols cited here were checked again on the merged tree each time. Two plans this one touches merged in between and are
read on main: batch-run-identity (#3323, merged as `b03f9147a`, its branch tip `69f76967d`) and
credentials-belong-to-models (#3315, merged as `7abc5eb2f`). Owner: Dimi Paun. Every decision
below was asked with its options and costs on 2026-11-20, and the owner's answer is in its cell;
D3129 was asked three times and D3130 twice, and each says why; D3131 was added by review round 2.

**Amended 2026-11-22 by [model credentials routing](../model-credentials-routing/README.md)** (decisions D3189 and D3191, resolutions R8, R19): **D3136** is widened by one case — the issuer also serves the orchestrator a *resume* of a pause the credentials service recorded, which is no attempt on its own record. The service, not the job, is the only writer of that paused node, the resumed run keeps the launcher and the pool the pause recorded (R8, D3191), and how the issuer recognises such a node is that plan's PR4. Of [credentials-belong-to-models](../credentials-belong-to-models/README.md), its **D3066** is noted here as amended by **D3174** of [model credentials routing](../model-credentials-routing/README.md): a Vertex call a chain reaches through the credentials service is made by the service on its own identity, as it serves a key, and not by the run's gateway with the job's own account. The entries they touch are marked in place; nothing here is approved by that line.

Paths and symbols are cited without line numbers. "The runs' signer" is
`google_service_account.signer` in `deploy/terraform/gcp/modules/firebase/iam.tf`; "the minter
role" is `google_project_iam_custom_role.token_minter` (`fugaroTokenMinter`, one permission:
`iam.serviceAccounts.signJwt`).

## Assumptions

- **A1.** "A run's identity" is the Firebase custom token whose uid is `r~<slug>~<run>`
  (`token.UID`, `internal/budget/token`), and the session the runner exchanges it for. It is not
  the job's Google service account.
- **A2.** "Launchers and operators" are the principals in `local.people` of the Firebase module:
  the roster's launchers plus its operators. Admins and viewers hold no minter row today and are
  not in question.
- **A3.** "Keeps working" (ask 4) means the same command, with the same flags, still launches. It
  does not mean an older CLI keeps working (D3124).
- **A4.** "Obtain" in ask 3 includes copying a token another person's launch produced, not only
  signing a new one. F9 is why this matters, and D3122 rests on it.
- **A5.** The installation is the unit: one roster, one issuer, one signer. A workspace is
  optional (`docs/workspaces/README.md`) and is not assumed.
- **A6.** The claims a run's identity carries stay what the launcher asks for its own run (branch,
  pull requests, context repositories). The spec puts them out of scope; the issuer checks only
  the claims in R4. Review round 3 contradicted this for the context repositories, which the CLI
  gate judges today: F31.

## What the spec names, and what the repository has

| The spec says | The repository has |
| --- | --- |
| "the grant that lets a person sign as the runs' signer" | `google_service_account_iam_member.minter`, `for_each = local.people`, role `fugaroTokenMinter`, on the runs' signer (`modules/firebase/iam.tf`) |
| "something that is not the launcher's machine, and that checks who is launching" | The credentials service's launcher class: `Authenticator.VerifyLauncher` (`internal/credsvc/auth.go`) and `Launchers.VerifyLauncher` (`internal/credsvc/launchers.go`), a Google ID token checked against the roster `fugaro update` renders as `FUGARO_LAUNCHER_ACCOUNTS`. The CLI already calls it at launch: `slipGrantOverService` (`internal/cli/run.go`) to `SlipPath` (`/v1/slip`) |
| "the launcher roster" | Two lists from one source: the IAM members `var.launchers` and `var.operators` (Firebase root), and the bare addresses `var.launcher_accounts` (installation root, `internal/infra/launchers.go`), which holds the `user:` entries only |
| "the coordinator [has a] signer grant of its own" | Nothing. See F2 |
| "pairing tokens, device tokens, the mobile account's" | `token.MintPairing` and `token.MintDevice`, both called from `internal/cli/pair.go` with the runs' signer; and a second, device-only signer for the public mobile service (`google_service_account.device_signer`, `mobile_token_minter`) |
| "the job's platform" as an issuer | Rejected already as alternative C4 of `docs/budget-token-channel/spec.md` |

## The census

### Who signs (non-test callers)

| Caller | What it signs | With whose credential |
| --- | --- | --- |
| `(*cloudEnv).mintBudgetToken`, `internal/cli/launch_budget.go` → `token.Mint` | a run's custom token | the caller's application default credentials, through `token.IAMSigner` built in `(*cloudEnv).tokenSigner` |
| `internal/cli/pair.go` → `token.MintPairing` | the one-shot pairing token | the same signer, the same credentials |
| `internal/cli/pair.go` → `token.MintDevice` | a paired device's token | the same |

`token.MintAt` is called by `token.Mint` only. `token.IAMSigner` is the only non-test
implementation of `token.Signer`, and `(*cloudEnv).tokenSigner` is the only place it is built.
Nothing in `ee/`, `internal/orchestrator`, `internal/credsvc`, `internal/mobilesession` or `cmd/`
calls any of the four. `internal/mobilesession` declares a `Minter` interface for the device-only
signer; its only non-test implementation is `stubMinter`.

### Who holds a signing grant, by Terraform resource

| Signer | Principal | Resource | Role |
| --- | --- | --- | --- |
| runs' signer | launchers and operators (`local.people`) | `modules/firebase/iam.tf`, `google_service_account_iam_member.minter` | `fugaroTokenMinter` |
| runs' signer | the resident orchestrator's account, when `local.orchestrator_on` | `modules/installation/orchestrator.tf`, `google_service_account_iam_member.orchestrator_token_minter` | `fugaroTokenMinter` |
| device signer | the public mobile service's account, when `var.mobile_account != ""` | `modules/firebase/iam.tf`, `google_service_account_iam_member.mobile_token_minter` | `fugaroTokenMinter` |

`roles/iam.serviceAccountTokenCreator` appears twice and on neither signer: operators hold it on
the deployer account (`modules/installation/deployer.tf`) and on the verifier account
(`modules/installation/iam.tf`). F6 says why that still matters. Admins and budget admins hold
`roles/firebasedatabase.admin` in the Firebase project and no signing grant.

### Where a launch starts

Every launch goes through one function. `launchRun` (`internal/cli/run.go`) holds the only call to
`backend.Launch` and the only call to `mintBudgetToken`.

| Way in | Path to `launchRun` | Signs as |
| --- | --- | --- |
| `fugaro run`, a follow-up, `fugaro review`, a shared project job, a retry | `finishLaunch` | the person |
| the A/B challenger arm | `launchChallenger` | the person |
| `fugaro troubleshoot` | `troubleshootLaunchRun` | the person |
| the orchestrator daemon on a person's machine | `orchestratorLauncher.Launch` (`internal/cli/orchestrator_launcher.go`) | the person |
| the resident orchestrator service | `residentLauncher.Launch` (same file) | its own account, `fugaro-orch` |
| the console | spawns the CLI's `run` (`console/src/main/ipc.ts`) | the person |
| the mobile app | writes a mailbox the resident orchestrator acts on | `fugaro-orch` |
| the coordinator | launches nothing (F2) | none |

### Who verifies

| Verifier | What it trusts |
| --- | --- |
| Firebase Auth, at the exchange (`token.Exchange`) | a custom token signed by **any** service account of the Firebase project (`internal/infra/tokensigners.go`, live Check 26) |
| the runner, `startBudgetBackend` (`internal/runner/budget_session.go`) | the sealed object it opens with the key in `runner.BudgetTokenEnv`; that the token names this slug and run; `strayTokenNoBackend` refuses a marker with no backend |
| the database rules (`internal/budget/rules/rules.go`, generated) | `auth.uid` and the token's claims, whoever signed them |
| the credentials service, `Authenticator.Verify` | the job's Google ID token (account in its job tables) and the run's Firebase ID token (project, uid shape, `fp`, `fx` ahead), and that no outcome is written |

## Findings

**F1 (Exists). The issuer's front half is already built.** The credentials service verifies a
launcher by a Google ID token against the roster (`VerifyLauncher`), every `user:` launcher and
operator already holds `run.invoker` on it (`credentials_invoker`, `modules/installation/iam.tf`),
and the CLI already asks it for something at launch (`slipGrantOverService`). That something, the
slip (`internal/credsvc/slip.go`), is the same mechanism in small: a launcher-class grant for a
slug, a run and an account, a claim made once by the run, and a stored hash of a secret; and
`slipRouteAbsent` (`internal/cli/run.go`) is already a bridge for a service that does not serve
a route. The contract says what of it the identity routes reuse. Consequence: D3121.

**F2 (Wrong). The coordinator holds no signer grant and launches nothing.** The brief says the
orchestrator and the coordinator have signer grants of their own. Only
`orchestrator_token_minter` exists. The coordinator's account holds a bucket reader and a secret
accessor (`modules/installation/coordinator.tf`), the credentials service's invoker
(`modules/installation/iam.tf`), and its writer account the database admin role
(`modules/firebase/iam.tf`); neither holds signing. `ee/coordinator` calls neither `launchRun` nor `token.Mint`. Consequence: the
coordinator needs no change, and ask 4's "the coordinator" is met by doing nothing.

**F3 (Gap). The same signer and the same grant sign pairing and device tokens.** `fugaro pair`
builds its signer with `(*cloudEnv).tokenSigner`, so it signs as the runs' signer with the
person's credentials; its own refusal text names the `fugaroTokenMinter` role. Removing the
people's row stops `fugaro pair`. SECURITY.md's H1 is the other side of the same fact: "any
launcher can mint a device token and sign in as an operator's device at the approve step".
Consequence: D3126.

**F4 (Gap). The roster admits members the launcher class cannot name.** `var.launchers` accepts
`user:`, `group:` and `serviceAccount:` members, and IAM resolves all three for the minter row.
`var.launcher_accounts` is bare addresses, made from the `user:` entries; `NewLaunchers` refuses a
service-account address as a configuration error. A `group:` or `serviceAccount:` launcher can
sign today and has no class at the service. Consequence: D3123.

**F5 (Gap). The resident orchestrator is a second minter.** `fugaro-orch` holds the minter role
on the runs' signer. `orchestrator.tf` says of the row: "signJwt signs ANY claim set for ANY uid,
so it is also 'may be the phone and may be the consumer'". It launches through `launchRun` like
everyone else, and the credentials service already has a class for it (`VerifyOrchestrator`).
Consequence: D3125.

**F6 (Rule). Signing as the runs' signer is not the boundary; signing as any service account of
the Firebase project is.** `internal/infra/tokensigners.go` records it: "Firebase accepts a custom
sign-in token signed by ANY service account of the project (live Check 26, step 7: a token signed
by fugaro-scheduler was accepted)". Operators hold `roles/iam.serviceAccountTokenCreator` on the
deployer account, which includes `signJwt` as the deployer. Where the Firebase project is the
installation's own project, an operator can therefore still sign a run's token after the minter
row is gone. And whoever applies the Firebase root can re-grant the row. `TokenSigners` already
lists every such principal for doctor. Consequence: D3127, and the plan's doctor task.

**F7 (Rule). The issuer's grant amends a standing rule.** `.claude/rules/credentials-service.md`:
"The service account's only project-level grant is the custom database role
`fugaroCredentialsDatabase`". A minter row on the signer account is bound on the account, not on
a project, which is the shape the mobile plan took (O1 there). The brief still calls it an
amendment, and it is one in substance: the account that holds every model key becomes the account
that signs every run's identity. The owner took it knowingly (D3121); the plan amends the rule's
sentence in the same pull request as the grant.

**F8 (Exists). The issuer already holds more than a run's identity gives.** The credentials
account's database role carries `firebasedatabase.instances.update`, which writes the database
past the rules. A compromise of the service already writes any ledger and serves every key. What
the minter row adds is the ability to sign in as any uid of the Firebase project, a device's
included. That is the honest size of the amendment in F7.

**F9 (Gap). Removing the grant does not stop a second launcher copying a run's token.** The
sealed token is an object in the runs bucket every launcher reads (`runs_reader`), its key is the
value of `runner.BudgetTokenEnv` in the execution's override, and the launcher role
(`google_project_iam_custom_role.launcher`, `modules/installation/iam.tf`) carries
`run.executions.get`. `token.Lifetime` is one hour, and a custom token can be exchanged more than
once inside it (U3). `docs/budget-token-channel/spec.md` says the seal is "a capability, not a
boundary" and keeps a launcher out of scope because "a launcher can already mint". Once a
launcher cannot mint, that exemption is gone and the copy is the remaining way to ask 3's
"obtain". Consequence: D3122.

**F10 (Gap). Nothing server-side binds a run's identity to the person who launched it.** (A slip,
where one is granted, records its launcher; the identity does not read it.) The launcher chooses
the run id (`task.NewRunID`, or `--run-id`); the `rb` claim is `localcfg.Config.Me`, and the
code's own comment says "attribution only, nothing in the rules trusts it"; `mintBudgetToken`
deletes the token object before it writes one, so a re-mint replaces it. Every guard against
reusing a run id (`takeClaim`, `existingLaunch`) is launcher-side. Consequence: R3, the issuance
record.

**F11 (Gap). The platform cannot tell two executions of one job apart, as far as the code knows.**
`CLOUD_RUN_EXECUTION` is read from the environment (`internal/backend/codec.go`) and is
self-reported. The runner's `metadataIDToken` (`internal/runner/gateway.go`) names the job's
account. SECURITY.md states the agent shares the container and can mint the job's Google ID
token. So a claim by the run proves the job, not the execution, and not the runner as distinct
from the agent (U1). Consequence: the residual in R5, and Check 840.

**F12 (Gap). A shared project job's account spans repositories.** `modules/project/sa.tf` has one
job account for every repository a profile admits. A ticket bound to "that repository's job
account" is, for a shared job, bound to the shared account. Consequence: R4 binds the ticket to
the account the launch will run as, and says for a shared job that this narrows to the profile,
not to the repository.

**F13 (Gap). `fugaro update` applies the signer's root before it deploys the service.** The
stages run installation, then Firebase, then, several stages later, the credentials service
(`initStages`, `internal/cli/init_stages.go`). The signer and the people's row are in the Firebase
root. A plan that removed the people's row where it stands would leave minutes in which nobody
can issue. Consequence: R8, a late stage that moves the grant only after the issuer answers.

**F14 (Gap). An older CLI has no way to be told why it failed.** There is no installation-side
"minimum CLI" marker. What an older CLI already reads is the published policy's version: one above
its own makes it refuse with `projectpolicy.SkewSentence`, which names the release to upgrade to.
Without it, `token.Mint` returns IAM's 403. An installation with no published policy has no such
lever (O4). Consequence: D3124.

**F15 (Gap). A CLI upgraded before the installation meets no issuer.** After `brew upgrade` and
before an operator's `fugaro update`, the new CLI faces a service with no identity route. A
teammate who is not an operator cannot run the update. Consequence: R7, a one-release bridge.

**F16 (Rule). A run needs to reach the issuer at its start.** With D3122 the runner claims its
identity from the credentials service. Each job account holds `run.invoker` on the service, and
keys-through-service makes every model call go through it. Consequence: D3128, and U4.

**F17 (Rule). Two plans rewrite the functions this one changes.** always-a-budget's AB4 edits
`budgetBackendOn`, `budgetMintOn` and their callers so that every launch mints, and AB1a makes
`startBudgetBackend` open a session for every run. This plan replaces what the first does and
changes how the second gets its token. Consequence: D3128.

**F18 (Rule). This changes batch-run-identity's design.** That plan (merged, #3323) builds a KMS key, a
launcher-wrapped seal key, a registration and a release route so a Batch VM can open the sealed
token (its D3101, D3102, D3108, R1), because "any carrier the run account opens can be replayed by
a second launcher" (its F4). With a ticket the run claims there is no sealed token to open; the
same VM attestation can guard the claim. Its F24 and O6 are this spec's origin, and its O12 (the
launcher's identity token is a bearer token for a wider audience) applies here unchanged (O5).
Their **C1** is not only a wire. It holds the wrapped blob's envelope, the release route, the
registration, the published opt-in fact, and the `batch:` block's key fact with its settings
entry and schema. Read on main, the tasks built on the key or on that C1 are **C1**, **BR1**,
**BR2**, **BR3**, **BR4**, **BR5**, **BR6** (it depends on C1 for the opt-in fact), **BR7**,
**BR12**, **BR13**, **BR14**, **BR15** (through BR7 and BR12), **BR16**, **BR17**, **BR18**,
**BR19**, **BR20**, **BR21**, **BR22**, their two change requests (`R-C1-link-permission`,
`R-C1-project-job-account`) and their I1. Free of both: **BRS**, **BR0** (Check 830) and
**BR8** to **BR11**. Two earlier versions of this finding listed fewer; the list is now read
from that plan's own Depends lines, and LM19 is what settles it task by task. Decisions of theirs this touches:
D3101 and D3108 keep their intent and change their object (from the seal key to the claim);
D3102 is strengthened (the same service); D3109 (people only) meets D3123 here, which admits
roster service accounts, and the amendment of that plan has to settle the two (O2). Consequence:
D3129.

**F19 (Rule). credentials-belong-to-models adds another thing a launcher says about a run.** Its
R15 has the launcher resolve each craft's access and put it in the execution's environment, and
states "the runner's own answer is never a boundary: the service serves a call only what the
record names". This spec does not change that. The issuance record (R3) is a place a later plan
could move that answer to; nothing here does (O6).

*(Amended 2026-11-22: that plan's **D3066** — Vertex reached through the run's gateway with the
job's own identity, the service holding nothing and never called — is amended by **D3174** of
[model credentials routing](../model-credentials-routing/README.md). For a chain the credentials
service serves, the service calls Vertex on its own identity, as it serves a key, so a job's own
Vertex grant is not what carries the call, and what a launcher resolves into an execution's
environment is not the whole of what a Vertex chain runs on.)*

**F20 (Exists). The precedents for every Terraform move exist.** A minter row for one named
service account, written with literals so the plan is readable: `mobile_token_minter`. A row in
one root naming another root's account: `orchestrator_token_minter`. Plan-cover rows that tie a
minter grant to one account: `orchestratorMinterGrant` and `mobileMinterGrant`
(`internal/infra/tf/cover.go`). A grant taken off launchers by an update: `HardeningAllowDelete`
(`internal/infra/hardening.go`). A shape-free policy version bump with its in-flight ordering:
version 10 (`internal/projectpolicy/version.go`, `docs/design/update-in-flight.md`).

**F21 (Rule). The security documents state today's reach as an accepted limit.** SECURITY.md:
"**Any launcher can mint a budget identity for any repository.**" It is mirrored byte for byte in
`website/content/security.md` (`internal/cli/docs_website_security_test.go`), and repeated in
`docs/roles/spec.md`, `docs/budget-token-channel/spec.md` and
`docs/mobile/design/always-on-orchestrator.md` (section 4.5, E4). Consequence: the plan's
documents task rewrites each; "may mint" becomes "may launch".

**F22 (Exists). This is a ruling already on record.** `docs/roles/spec.md` gives D628 as the
reason a minter is needed that the people being ranked cannot call, and its D686 asks where that
minter lives, noting that narrowing the role takes it away from `fugaro pair` and the run's
budget token. This spec carries both out.

**F23 (Wrong, found by review round 1; the first version of this document said the opposite). A
per-repository launch check exists.** `internal/access` resolves roles at a scope, and
`ActionLaunch` needs the operator role at the repository's scope (`resolve.go`). `fugaro run` and
`fugaro troubleshoot` ask it through `requireAccess` (`internal/cli/access_run_gate.go`,
`run_allowlist.go`). That file says the check "is a CLI check": the cloud holds no
per-repository grant, and roles-scopes lists RS12, per-repository cloud enforcement, as later and
unplanned. The issuer is the first server-side place that can enforce it. Consequence: D3130 was
asked again.

**F24 (Gap, round 1). The policy stage runs before the credentials stage.** So the version bump
of D3124 lands before the late stage of R8 probes the issuer. Three consequences. What `update`
says about older CLIs has to be printed at the update's door, before the policy stage, not by
the late stage. If the probe then fails, older CLIs are already told to upgrade while the rows
stay; an upgraded CLI then launches through the bridge where the service answers "not served",
and is refused where the service is down (as every model call is). And a runner of the release
behind refuses a policy ahead of it at bootstrap (`internal/runner/projectpolicy.go`, the
`SkewSentence` arm with `ReaderRunner`): an execution created before the update and not yet
bootstrapped halts on its old image. That is what every policy version bump does today, version
10 included; `docs/design/update-in-flight.md` promises for a run that has bootstrapped.
Consequence: R8, R9, R10 and O12.

**F25 (Gap, rounds 1 and 2). An update that deletes a grant needs the delete listed.** `tf.Guard`
(`internal/infra/tf/guard.go`) refuses a delete no list names. The precedent is
`HardeningAllowDelete`, wired in `internal/cli/init.go`. Without the same for the people's and
the orchestrator's rows, the update of an ordinary project would refuse and point at a flag,
which the hard rule forbids. A managed project is different: `GuardManaged` warns about, and
does not refuse, a delete of a type classified as holding no data, and
`google_service_account_iam_member` is one (`nonDataBearingTypes`, `internal/infra/tf/cover.go`);
its update passes addresses through `managed.Spec.Upgrade` in `internal/cli/update_managed.go`.
Consequence: plan task LM20 covers both paths, and LM14's stage runs on both.

**F26 (Gap, round 1). A run's own node is readable by the run.** In
`internal/budget/rules/rules.json.tmpl` a run's node grants its token a read, and a grant
cascades to every child, so a record "beside the run's budget record" could not be closed. The
`creds` subtree is already closed to every signed-in identity (read and write false).
Consequence: R3 puts the record there, and LM5 pins it with a test and generates no new rules.

**F27 (Gap, round 1). An installation not yet updated has no stub to answer "not served".** Its
service was built before the contract. A path it does not know is answered 404 `not_found` by
`servedRoute` in `ServeHTTP` (`internal/credsvc/server.go`), before any caller is resolved, and
an installation may have no service configured at all. The service already has a refusal named
`refuseNotServed` for another purpose (a workspace scope); the contract's code is a different
one. Consequence: R7 names what the
bridge keys on, and C1 pins the answer of a release-behind service with a test.

**F28 (Gap, round 1). The service's environment names are held in two exact lists.**
`internal/infra/tf/cover.go` says a new name goes on its allow-list first
(`CredentialsServiceEnvAwaitingTheModule`) and into the module second, and the installation
root's own test holds the environment as an exact map. `TestCredentialsServiceEnvIsTheDeploymentsOwn`
(`internal/cli/credentials_serve_test.go`) also fails when the module sets a name the command
does not read, or renders one still on the awaiting list. Consequence: LM22 (the names and their
reader) before LM12 (the module, which also takes them off the awaiting list).

**F29 (Gap, round 2). The CLI gate's answer has inputs the service does not hold.** `accessInput`
(`internal/cli/access_run_gate.go`) gives the resolver the machine's implicit admins
(`userImplicitAdmins`, from the local `terraform.budget_admins`), admits without judging a
service-account credential (`ErrServiceAccount`), and does not run when the roles records cannot
be read. An issuer that ran the resolver without the first would refuse an owner a launch that
works today. Consequence: R16.

**F30 (Gap, round 2). Any launcher can run code as a job account.** It launches a task on that
job. With `run.executions.get` it also reads a peer's ticket. So the claim race of R5 is open to
every launcher of the same job, or of a shared job, not to a special few. Consequence: the
trust-boundary table and R5 say so.

**F31 (Gap, round 3). The CLI gate also judges the context repositories.** `launchPolicyGates`
(`internal/cli/run_allowlist.go`) calls `requireAccess` a second time, with `ActionViewRepo`, for
each of the run's context repositories, and then `projectpolicy.AdmitRepo`; its comment says why:
the launcher signs the list into the run's identity, and a reader of that identity holds a
signature and not a role. `mintBudgetToken` puts the list in the claims. An issuer that checked
the launch scope alone would sign a list the gate refuses. Consequence: R16's fourth arm; A6
holds for the branch and the pull requests, not for the context repositories.

**F32 (Gap, round 3). The Firebase root is planned and guarded in its own file.**
`internal/cli/init_firebase.go` builds the root's inputs (`infra.Firebase`, `FirebaseInputs`) and
runs its guard with the update's allow-list. `HardeningAllowDelete`, F25's precedent, is wired
for the installation root only. The people's row is in the Firebase root. Consequence: LM13 and
LM14 name that file.

**F33 (Gap, round 3). A ticket route that answers proves nothing about signing.** Issuing a
ticket signs nothing; the signature happens at the claim. A wrong signer account or a missing
issuer row would pass a probe of the ticket route, the rows would go, and every run would halt
at its claim. Consequence: R8's probe signs.

**F34 (Gap, traced for D3132 after the pull request opened; corrected by review round 4).
Nothing in the cloud judges what the resident orchestrator asks for.** `residentLauncher`
(`internal/cli/orchestrator_launcher.go`) runs no gate block and no access check, and says so on
purpose. It starts only re-issues (`ActionReissue`, `internal/orchestrator/resident_launch.go`):
attempts already committed on the orchestrator's node. It refuses, on its own side
(`residentRunIsTheItems`), a stored task that differs from the approved item or that carries a
field no item can express, context repositories among them. Two facts the first version of this
finding had wrong. The node is closed to every signed-in identity by the rules (`orchestrator`,
write false), so it is written over IAM by the database admins alone, the same people who write
the roles records; a launcher with a modified CLI cannot write it. And an attempt names no
committer: `Intent` (`internal/orchestrator/state.go`) holds an id, a run id, a spec digest,
two times and an outcome. So there is no person to judge, and a committer field would be the
admin's own word. What is open is the orchestrator's account itself: compromised, or
impersonated by an operator, it could ask for a run of its own choosing on any repository with
any context repositories, because every check above is on its own side. Consequence: D3136 as
amended.

## What each option does to the trust boundary

Before: every launcher, every operator and `fugaro-orch` can sign **any claims for any uid** of
the Firebase project: any run, any device, the consumer. A stolen launcher laptop is that reach
for as long as its refresh token lives.

| | Who can mint what, after | A stolen launcher laptop | Single point of |
| --- | --- | --- | --- |
| **Credentials service issues a one-time ticket (chosen: D3121, D3122)** | Only the credentials account signs. It signs a run's identity for a ticket a roster caller asked for, and a pairing or device token for a verified person; no route takes a uid or a claim set | Launches runs on the repositories that person holds the launch scope on, up to the caps, as that person and on the record. Cannot sign anything, cannot re-issue another person's run, cannot mint a device token for anyone else. Can still race the claim of a run on a job it can launch, which is detected and not prevented (R5, F30) | Every launch and every run start. It already is the single point of every model call once keys-through-service lands |
| Credentials service returns the token | Only the service signs, but the token crosses the launcher's machine and the sealed channel | As above, plus a silent copy of any run's token inside its hour (F9) | Every launch |
| A second service with its own account | Only that account signs; the account that holds the keys does not | As the chosen row | Every launch; one more service to keep up |
| The workspace service | Nothing, for an installation without a workspace | not applicable | Every launch of a member; no answer for the default installation |
| The job's platform identity | Any code in any execution of the job | Launches, and the agent in any run of a shared job claims any run's identity | Nothing new, and no boundary |

## Decisions

Numbers D3120–D3137 are used; the block is D3120–D3139 (see the README for the reading).

| # | Question | Answer (owner, 2026-11-20) |
| --- | --- | --- |
| **D3120** | The spec's ruling section is a placeholder. Is the coordinator's reading right: no launcher or operator holds the grant that signs a run's identity, and a run gets its identity at launch from something off the launcher's machine that checks who launches, which repository and which run? | **Yes, as written.** As recommended. Why: the reading is the spec's six asks. `spec.md` keeps the placeholder; this cell is the confirmation. |
| **D3121** | What issues the identity? Recommended: the credentials service, on new routes for the launcher class. Alternatives: a second service of the same image with its own account (about 3 more tasks, one more service; the rule in F7 untouched); the workspace service (no answer for an installation without one); the job's platform (C4, rejected). | **The credentials service.** As recommended. Why: its launcher class, its roster and the launch-time call exist (F1). Costs accepted as shown: it becomes the one minter and a single point of every launch, and whoever compromises it can sign any uid. **This amends the rule that the service account's only grant is its database role** (F7, F8): it gains the minter role on the runs' signer account. |
| **D3122** | What does the launcher get back? Recommended: a one-time ticket the run redeems, so the launcher never holds the identity. Alternative: the token itself on today's sealed channel (runner and images untouched; a second launcher can still copy it silently, F9). | **A one-time ticket.** As recommended. Why: ask 3 says "obtain", and a copy becomes a race whose loser reports it. Cost accepted: the runner and the image contract change, every run start depends on the issuer, and on Cloud Run a peer launcher who also runs code as the job account can still win the race, detected and not prevented (R5). The reach of that race, every launcher of the same job, was found later (F30) and was not in the option's text. |
| **D3123** | What happens to roster entries that are not a person? Recommended: a `serviceAccount:` entry becomes its own class at the issuer; a `group:` entry cannot be issued to, and `update` names each before it removes the grant. Alternatives: people only (matches batch-run-identity's D3109; a pipeline's service account stops); both, with groups resolved through a directory (a grant outside the managed project). | **Service accounts yes, groups no.** As recommended. Why: a service account's ID token names it exactly; a group needs a lookup the installation does not have. |
| **D3124** | How does the removal reach a teammate's older CLI? Recommended: one release; `update` removes the people's grant only after the issuer answers, and the published policy's version moves so an older CLI refuses with the upgrade sentence. Alternatives: two releases (batch-run-identity's F24 open one more release); one release with no lever (IAM's 403 and the wrong fix). | **One release, with the policy version bump.** As recommended. Why: pre-1.0, and the older CLI's own refusal names the fix. Costs accepted as shown: every launcher upgrades before their next launch after the operator's update; a policy migration inside `update`; an installation with no published policy shows the older CLI IAM's own error (O4). A cost not shown then is D3131. |
| **D3125** | The resident orchestrator loses its minter row and asks the issuer under its own class. What may it be issued? Recommended: a run of any repository of the installation, as it launches today. Alternatives: only chains a person started (about 2 more tasks; a root launched before the upgrade has no record); it keeps its own grant (a second minter). | First answer: "Any repository's run", as recommended. **Amended by D3136 (owner, 2026-11-20): any attempt the orchestrator's own record carries.** The orchestrator keeps its own class and loses its grant as answered; what changed is that the issuer serves it only a recorded, unstarted attempt, and no context repositories. |
| **D3126** | `fugaro pair` signs with the same grant (F3). What happens to pairing? Recommended: the issuer mints the pairing and device tokens for a verified person, on routes that take no uid and no claim. Alternatives: wait for the mobile plan's own minter (blocks the removal); pairing stops and `update` says so. | **The issuer mints both.** As recommended. Why: it keeps `fugaro pair` working and takes device tokens away from launchers. The option's text said it "closes" SECURITY.md's H1; review round 1 showed that overstates it, and O13 says what remains. |
| **D3127** | Operators can still reach signing through the deployer account, and by re-granting (F6). What does the spec claim about operators? Recommended: no standing grant, and SECURITY.md says the rest; doctor lists every principal beyond the issuer. Alternatives: also narrow the deployer grant to `getAccessToken` (about 2 more tasks on the update's own impersonation chain; one hop removed, not the reach); claim it only where the Firebase project is a separate one. | **No standing grant; the documents say the rest.** As recommended. Why: an operator administers the installation's IAM; the honest claim is the standing grant and the audit trail. Ask 1 therefore holds in full for launchers, and as "no standing grant" for operators. |
| **D3128** | When does it ship (F16, F17)? Recommended: tasks start after always-a-budget's I1 and keys-through-service's release A, and it ships in the CLI release after them. Alternatives: with always-a-budget (that plan's no-release window grows); ahead of both (built on code AB4 deletes). | **After both, in its own release.** As recommended. Why: one mint path to replace, and every job reaches the service. Cost accepted: batch-run-identity's F24 stays open until those two land. |
| **D3129** | What happens to batch-run-identity (F18)? **Asked three times**, because the first two questions each rested on a wrong reading of that plan. First: its C1 and the tasks behind it wait, "BRS, BR0 and BR6 may go". Second, after round 1 showed BR6 depends on that C1: its C1 goes and only the key-release tasks wait. Third, after round 2 showed that C1 also publishes the key fact and that the update side is built around the key: recommended, everything that touches the key or that C1 waits for LM19, which may start as soon as this plan's C1 merges. Alternatives the third time: C1 goes as answered (a config field published and then removed, the most rework); Batch goes ahead in full (two identity paths). | First answer: "Hold C1, amend that plan". Second: "C1 goes; key-release tasks wait". **Amended after review twice; the owner's third answer (2026-11-20) is the one that stands: hold all that touches the key.** As recommended. Their C1 and every task depending on it or on the key wait for LM19 (the list is in F18). Only BRS, BR0 and BR8 to BR11 go. **The go the owner gave their C1 and BR6 on 2026-11-20 is on hold**; BRS and BR0 keep theirs. LM19 may start as soon as this plan's C1 merges, ahead of D3128's gate (this settles O10). |
| **D3130** | Ask 2 says the issuer checks "for which repository". **Asked twice.** First, on the wrong premise that no per-repository launch list exists: recommended, the issuer checks only that the repository is the installation's. Second, after review round 1 found the roles-and-scopes resolver (F23): recommended, the issuer runs the resolver the CLI gate runs, on the verified caller. Alternative the second time: installation only, as first answered (a modified CLI can still launch any repository). | First answer: "It is the installation's", as recommended. **Amended after review, and answered again by the owner (2026-11-20): enforce the scopes at the issuer.** As recommended. No launch scope on that repository, no ticket. The first answer's two checks stay under it: the repository is one the installation has a job for, and the ticket is bound to the account that job runs as (for a shared or review job, that job's account: F12). Cost accepted: about 2 more tasks, and the service reads the access records on every ticket. |
| **D3131** | Added by review round 2. The policy version bump of D3124 makes a runner of the release behind refuse the policy at bootstrap, so an execution created before the update and not yet started halts (F24). Accept that for this release? Recommended: accept, and `update` says the count at its door. Alternatives: no bump (IAM's 403 and the wrong fix for older CLIs); bump only when nothing is queued (the grant may never move on a busy installation). | **Accept, and say it at the door.** As recommended. Why: every policy bump already does this, and the window is the queue. Cost accepted: executions queued and not started when the update runs are lost to it. |
| **D3132** | Asked after the pull request opened (open item O17). A launch from a phone reaches the issuer as the resident orchestrator, whose asks are not scope-checked. Recommended: trace what the orchestrator checks, then decide. Alternatives: scope its asks by the person at once; accept as is. | **Trace it now, then decide.** As recommended. The trace is F34; the decision is D3136. |
| **D3133** | The cap is three review rounds and round 3's fixes were unreviewed (O18). A fourth round? Recommended: yes. Alternative: land as it stands, with C1's own review to catch the rest. | **Yes, one more round.** As recommended. It goes past the skill's cap on the owner's word. |
| **D3134** | D3126 was chosen from an option that said it "closes" H1; it narrows it (O13). Does D3126 stand? Recommended: it stands, and the documents say narrowed. Alternatives: stands, and a backlog entry for the device-held key; reconsider D3126. | **It stands; H1 is narrowed.** As recommended. No backlog entry was asked for. |
| **D3135** | An installation with no published policy has no lever for an older CLI (O4), and whether one can exist after always-a-budget is unverified (U5). Recommended: LM14's first step settles the fact, and the question returns only if such an installation can exist. Alternatives: accept the gap; `update` publishes a minimal policy. | **Settled by the fact, then by the owner on 2026-11-26: accept the gap.** LM14's first step settled the fact — an installation that can launch after always-a-budget CAN have no published policy, so U5 is yes — and the question returned to the owner, who accepted the gap. An installation with no published policy keeps no lever for an older CLI: that CLI meets IAM's own 403, and `fugaro project policy publish` fixes it by hand. `update` does not publish a minimal policy. |
| **D3136** | The trace of D3132 (F34). **Asked twice.** First, on a threat that review round 4 showed was wrong (a person with a modified CLI committing an attempt): recommended, judge the launch scope of the person who committed. Second, with the facts of F34 as corrected (only database admins write the node; an attempt names no committer): recommended, the issuer serves the orchestrator only a run id that is a recorded, unstarted attempt of that repository, and refuses an ask naming context repositories. Alternatives the second time: add a committer field and judge it as well (about 2 more tasks, a schema bump, the field being the admin's word); unscoped, as D3125 first stood. | First answer: "Judge the person who committed". **Amended after review, and answered again by the owner (2026-11-20): bind the ask to a recorded attempt.** As recommended. No committer field and no scope judgement for this class. Cost accepted: 1 task (LM24), no schema change. A compromised orchestrator account can launch only what an admin's patch already committed. **Amends D3125.** **Widened 2026-11-22 by [model credentials routing](../model-credentials-routing/README.md)** (D3189, R19): the issuer also serves the orchestrator a resume of a pause the credentials service itself recorded — no attempt on its own record, but a node the service alone writes, its resume time included — and the resumed run keeps the launcher and the pool the pause recorded (D3191). Nothing else moves: an ask it cannot tie to a record still meets the same refusal. |
| **D3137** | Raised by review round 4 as a decision this document had made unasked (R16, arm 2). After D3130 a roster service account would be the one class with a ticket for any repository and no scope judgement, because the CLI gate admits a service-account credential without judging it today. Recommended: parity, admitted unscoped, and SECURITY.md says so. Alternative: judge it like a person (a pipeline stops until its account is granted a scope; about 1 more task). | **Parity: admitted unscoped.** As recommended. Why: a pipeline that launches keeps working (ask 4). Cost accepted: a leaked roster service-account key launches on every repository. |

No answer was "your call". Two answers were amended
after review and put to the owner again, who answered the same day: D3129 (twice) and D3130.
D3131 was added by review round 2. D3132 to D3137 were asked after the pull request opened, from
its open items and from review round 4; D3136 was itself amended after that round and answered
again, and it amends D3125. D3135's recommendation was to settle the fact first and to return the
question if such an installation can exist; the fact was settled the way that sends it back (U5
is yes), and the owner then took the alternative the row already named — **accept the gap**
(2026-11-26).

## Resolutions

One defensible answer each; stated, not asked.

- **R1. The issuer unreachable.** A launch that cannot reach the issuer fails with the reason and
  the fix, after a bounded retry the size of the slip grant's (`slipGrantTimeout`). Nothing
  degrades to local signing, because the only thing that could is the grant this spec removes. A
  run that cannot claim waits and retries for a bounded time and then halts, as a workspace
  member's run does (workspace-service D2983). This answers one of the brief's unknowns without a
  question; it rests on D3121, whose stated cost is that the service is a single point of every
  launch.
- **R2. The routes.** Five, on the credentials service: issue a ticket, claim an identity, mint a
  pairing token, mint a device token, and the probe `update` calls (R8). The probe admits the
  person class (the roster's operators) only, writes one audit line per refusal, signs a fixed
  payload and returns no signature bytes, so it is neither a signing oracle nor an open trigger
  of the signing quota. Their paths, bodies, refusal codes and the name of the
  execution variable that carries the ticket are the contract task's (C1). The claim is answered
  ahead of the shared request parse, as the merge route is for `VerifyOrchestrator`, because the
  caller holds no run identity yet. A claim carries a nonce the runner draws and keeps in memory:
  the same nonce again gets the same answer, so a retry after a lost response is not a second
  claim; another nonce after a success is. The record keeps the nonce's digest, never the nonce;
  the nonce is written to no log and no audit line; and the same-nonce answer holds only for the
  ticket's own life. Stated: a ticket, its nonce and the job's identity token together are a
  token for that long. Of the three only the nonce is confined to the run's own process: a
  launcher can read the ticket and can obtain the job's token (F30).
- **R3. The issuance record.** The issuer writes, once per slug and run, who asked (the verified
  address and class), when, the claims it will sign, the account the claim must come from, a
  digest of the ticket and whether it was claimed. The ticket itself is never stored. The record
  lives under the database's `creds` subtree, which the rules already close to every signed-in
  identity (F26); batch-run-identity's registration (its D3108) is the same kind of record with a
  job name on it. This is what makes a re-issue checkable (F10). **Stated:** the rules do not
  bind the principals that write past them through IAM. Database admins, the history account,
  the coordinator's writer and the public mobile service's account (the same database role as
  the credentials account) can read and write the record, and could write one of their own for
  the issuer to sign. So the claim checks the record again before it signs: the account is in
  the service's job tables, the uid comes from the record's path, and the claims pass R4's
  checks. The same principals write the roles records the scope check reads
  (`internal/access/store.go`). A launcher is not among them: it holds the database's viewer role.
- **R4. What the issuer checks before it issues.** The caller is on the roster, in one of three
  classes (a person, a roster service account, the orchestrator). The repository is one the
  installation has a job for, and the caller holds the launch action on it by the resolver the
  CLI gate uses (D3130 as amended, F23): the issuer gives the answer `requireAccess` gives today
  for every caller that gate judges, by R16. For the orchestrator's class there is no person to judge
  (F34): the issuer reads the orchestrator's own record and serves only a run id that is a
  recorded, unstarted attempt of that repository, and refuses an ask that names context
  repositories (D3136 as amended). The account the launch names is that repository's job
  account, the shared project job's or the review job's (F12). The run id is well formed. The
  project claim is the installation's own, and the expiry is no further out than the service's
  configured maximum. No record exists for this slug and run under another caller, no ticket of
  it has been claimed, and no outcome is written.
- **R5. What "one person cannot obtain, replay or re-issue" comes to.** Re-issue: refused to
  anyone but the caller on the record, and to the caller too once the ticket is claimed. Replay: a
  ticket is claimed once; the second claim is refused with one audit line, and the run that lost
  halts saying so. Obtain: no token exists until the run claims, and the claim must come from the
  recorded job account. **Residual, stated:** on Cloud Run, a launcher who reads another run's
  ticket from its execution and runs code as the same job account before that run starts can
  claim first. Any launcher of that job can do both (F30). The victim run halts and the audit line
  names the claim; it is detected, not prevented (F11, Check 840). On Batch the VM attestation can prevent it (D3129). **A second
  residual, stated:** a roster caller who learns a run id before its launcher has asked can ask
  first and take the record; the real launch is then refused. That is a denial of one launch, on
  the record under the denier's verified name, and never another person's identity. The launcher
  asks for its ticket before it writes anything that names the run id where others read.
- **R6. A retry by the same person.** Before the ticket is claimed, the same caller asking again
  for the same slug and run gets a new ticket and the old one dies. After it is claimed, a new
  attempt takes a new run id, as a follow-up already does.
- **R7. One release of bridge, on both sides.** The CLI of this release asks the issuer; where the
  service does not serve the route (an installation not yet updated, F15) it signs locally as
  before, which works exactly as long as the people's row is still there. The bridge keys on
  three things only: the contract's "not served" answer, which the service also gives while it
  has no signer configured; the answer a release-behind service gives a path it does not know,
  which C1 pins to what `slipRouteAbsent` already reads as an absent route (F27); and no service
  configured. It is no
  downgrade: local signing needs the row, and once LM14 has removed the row the bridge has
  nothing to sign with; there the launch refuses with the fix (run `fugaro update`), not with
  IAM's error. The runner of this
  release claims when the execution carries a ticket, and opens the sealed object when it carries
  the old marker. An execution created before the update that has bootstrapped finishes; one
  that has not is F24's case (R9). Both old paths, the seal and
  the `tokens/` prefix are deleted in the release after (plan task LM18, with its own go).
- **R8. `update` moves the grant late, and only once the issuer answers and can sign.** The Firebase root gains
  the issuer's row at its usual stage and keeps the people's row behind a switch that is still
  on. A stage after the credentials service's own probes the service; the probe makes the
  service sign a fixed payload as the runs' signer and discard it, so an answer means the row
  and the account are right (F33). When it answers, the
  stage applies both roots with the switch off, which removes the people's row and the
  orchestrator's; the two deletes are on the update's own allow-list, for an ordinary and for a
  managed project (F25). The orchestrator's row goes only once the resident service runs this
  release's image, which the installation stage has moved earlier in the same update; an image
  of the release behind would sign locally and fail. When the issuer does not answer, the rows
  stay and the update says why. The
  policy version has moved by then (F24): where the service answers "not served" an upgraded CLI
  still launches through the bridge. Nothing is asked (no-prompt policy, spec §1).
- **R9. Runs in flight.** A run that already exchanged its token holds a Firebase session that no
  IAM change touches; a queued run holds a token already signed (`docs/design/update-in-flight.md`,
  the Firebase row). Removing a signing grant reaches neither. The policy version bump is
  shape-free and follows version 10's ordering, and it has version 10's reach: an execution
  created before the update that has not bootstrapped yet halts with the skew sentence (F24,
  accepted in D3131). The service of the release behind refuses the migrated policy until the
  credentials stage has replaced it, which follows the policy stage at once
  (`docs/design/update-in-flight.md`, the credentials-service row); that window exists for every
  bump today. `TestUpdateLeavesARunInFlightRunning` gains the case of a run that has bootstrapped.
- **R10. What `update` says, and when.** At the update's door, with the note about runs in
  flight and before the policy stage (F24), one line each: the
  `group:` roster entries that can no longer launch, by name, with the fix (name the people);
  that a CLI older than this release must be upgraded, and that it will be told so; that
  `fugaro pair` now asks the service; and, when executions are queued and not started, that those
  will halt and need a relaunch. And the standing line about runs in flight.
- **R11. The launcher's name.** `rb` stays the self-asserted name it is: a field never changes
  meaning. The rules do read it in one place (`RBOK` in `rules.json.tmpl` ties a budget entry to
  the token's `rb`), so a launcher can still book a run's spend under a name of its choosing;
  LM16 says so. The verified address is on the issuance record (R3) and in the audit line. No claim is
  added (out of scope).
- **R12. The mobile account's signer.** Unchanged. It is a service's grant on a signer of its
  own, not a person's, and the spec's ask 1 is about people. H1 narrows through D3126 (O13), not
  through that signer.
- **R13. The coordinator.** Unchanged (F2).
- **R14. Who may pair.** The people who can today: the roster's launchers and operators, with
  `pair.go`'s own role checks unchanged. The issuer derives the pairing uid and the device's
  claims itself.
- **R15. Check 840 does not gate.** If it finds that an execution's identity token carries
  something of the execution, a contract change (R-C1-execution) binds the claim to it and the
  residual in R5 closes for Cloud Run.
- **R16. The issuer's scope check gives the CLI gate's answer, arm by arm** (F29, F31). The
  resolver's input is the gate's: the roles records and the policy's allow-list for the
  repository (`accessAllowRepos`). (1) The implicit admins the gate reads from the local
  configuration are rendered to the service by `update`, as one of the three environment entries
  LM22 names, so an owner is not refused. (2) A roster service account is admitted without a
  scope judgement, as the gate admits it today (D3137: asked, parity).
  (3) An installation with no roles to judge by is allowed, as the gate allows it. (4) Each
  context repository the launch names is judged for the view action and for the policy's
  allow-list, as the gate judges it, before the list is signed. (5) Roles records that do not
  decode refuse, as at the gate. (6) **The one deliberate difference:** when the records cannot
  be reached the gate does not judge and the launch goes on; the issuer refuses, because it
  cannot write its own record there either.

## Answers to what the brief did not know

| The brief's question | Answer |
| --- | --- |
| What issues the identity instead | The credentials service (D3121) |
| How the issuer knows the caller may launch, and what it trusts | The roster as `fugaro update` renders it to the service, by verified Google ID token, and the roles-and-scopes records for the repository (R4, D3123, D3130). A roster change reaches it at the next update, as it reaches the invoker grant today |
| Whether the same signer signs other things, and what happens to each | Pairing and device tokens move to the issuer (D3126). The mobile service's signer is untouched (R12) |
| What an older CLI does, and for how many releases the grant stays | It refuses with the upgrade sentence; the grant goes in the same release (D3124). The code paths stay one release (R7) |
| What happens when the issuer is unreachable | The launch fails with the fix; a run retries, then halts (R1) |
| Which launches are a service account's, and whether they keep the grant | The resident orchestrator and any `serviceAccount:` roster entry. Neither keeps the grant; both ask the issuer (D3125, D3123) |
| Whether Cloud Batch's design gets simpler | Yes: no KMS key, no wrapped seal key; the VM attests the claim (F18, D3129). Stated as the direction; that plan's own amendment settles it |
| Whether this ships before, with or after the plans it touches | After always-a-budget and keys-through-service's release A, in its own release (D3128) |

## Open tensions

None. No two answers pull against each other.

## Open items

- **O1. The spec's ruling section holds a placeholder.** The owner confirmed the reading (D3120)
  and has not written his own words into it. `spec.md` stays as received.
- **O2. batch-run-identity's amendment is not written.** D3129 as amended holds every task of
  it that touches the key or its C1; the documents pull request that re-plans them is task LM19 here and needs its own go.
  It must settle D3109 (people only) against D3123 (roster service accounts).
- **O3. #3323 merged while this was in review.** This pull request does not edit that folder;
  LM19 amends it on main. Its README still reads as before D3129.
- **O4. An installation with no published policy has no lever for an older CLI** (F14; D3135,
  settled by the owner on 2026-11-26: **the gap is accepted**). LM14's first step settled the
  fact — such an installation can exist after always-a-budget, so U5 is yes — and the owner
  accepted the gap rather than have `update` publish a minimal policy. There the older CLI shows
  IAM's own error, and `fugaro project policy publish` is the by-hand fix. always-a-budget makes a
  database required for a launch; a published policy is not required by the same fact.
- **O5. A launcher's Google ID token is a bearer token.** The class also admits gcloud's audience
  (accepted under D1939 and D1944), replayable for about an hour by any service that received it.
  A replay obtains a ticket as that person, on the record, and the same holds for the pairing
  routes. Stated, not fixed; it is batch-run-identity's O12.
- **O6. The launcher's other statements about a run** (branch, pull requests, and #3315's access
  entry) stay the launcher's; the context repositories are judged (F31, R16). The issuer signs what a roster
  caller asks for its own run (A6, F19).
- **O7. A roster change waits for an update** to reach the issuer, and so does a change to the
  implicit admins, which are read from the updating operator's local configuration. A launcher removed from the
  roster keeps the class until then, as they keep the invoker grant today.
- **O8. The other plans that may move the policy version.** The bump takes the next version at
  merge time; if another plan lands one first, this plan's bump is that plan's, or one more.
- **O9. The estimate has no cost.** No history is cached for these tasks on this machine, so the
  graph read prints a time range and no dollar figure.
- **O10. Settled by D3129's third answer:** LM19 may start as soon as this plan's C1 merges,
  ahead of D3128's gate.
- **O11. The graph draws no edge from LM1 or LM19 to C1.** `fugaro plan graph` admits an edge
  onto a contract only from the consumer side of an edge the contract broke. The plan's own text
  carries both waits.
- **O12. Settled by D3131:** a policy version bump halts executions that are queued and not
  bootstrapped (F24); the owner accepted it, and the update says how many at its door.
- **O13. H1 closes for launchers, not for everyone (D3134: D3126 stands on that reading).** After D3126 a launcher can no longer mint a
  device token. An operator through the deployer (D3127) and the credentials account still can,
  and SECURITY.md names a device-held signing key as the change that closes H1. LM16 says this
  and does not claim H1 closed.
- **O14. The orchestrator design's consumer claim.** `docs/mobile/design/always-on-orchestrator.md`
  (section 4.5) has the resident orchestrator satisfy a consumer check with a claim it would mint
  through its own `signJwt`. No code mints it today; after D3125 no route can. LM16 records it
  there; whether that design needs a route is that plan's.
- **O15. D3129 and D3130 as amended** are recorded from the owner's later answers (D3129's third,
  D3130's second). Listed here so the reader sees the cells are not the first answers.
- **O16. Two approvals in another plan are on hold by D3129:** batch-run-identity's C1 and BR6.
  That plan's README and status row still say four tasks are approved; LM19 corrects the folder,
  and this pull request adds the hold to its status row only.
- **O17. Settled by D3132 and D3136 as amended:** the issuer serves the orchestrator only a
  recorded, unstarted attempt and no context repositories (F34). What remains stated: a
  compromised orchestrator account, or an operator impersonating it, can start any attempt an
  admin's patch committed, and the task it starts is the object in the runs bucket.
- **O18. Four review rounds were run** (the cap is three; the owner asked for the fourth, D3133).
  Round 4 reviewed round 3's fixes and found five more things, among them that D3136's first
  question was wrong. **Round 4's own fixes are not reviewed**: D3136 as amended, D3137, the
  probe's route and class in R2, LM24 as reshaped, LM15's place. A reader should treat those as
  unreviewed.
- **O19. A roster service account is the one class the scope check does not judge** (D3137).

## UNVERIFIED

- **U1.** What a Cloud Run job execution's metadata identity token carries, and whether anything
  in it is the execution's. Settled by Check 840 (owner-run).
- **U2.** Whether `run.executions.get` returns an execution's environment overrides to a launcher.
  `budget-token-channel.md` carries it as its own U2. Check 840 reads it.
- **U3.** Whether Firebase accepts a second exchange of one custom token inside its lifetime. F9
  assumes it does. Check 840 reads it; if it does not, the copy in F9 is a race today as well.
- **U4.** That keys-through-service's release A leaves every job (per-repository, shared, review)
  with the service's address and an invoker row. Read off that plan, not off merged code; LM7's
  first step is to check it.
- **U5.** Whether every installation that can launch after always-a-budget has a published policy
  (O4). **Settled by LM14's first step: no.** An installation can launch with no published policy,
  and the gap that leaves an older CLI is accepted (D3135, the owner's answer of 2026-11-26).
- **U6.** Whether an operator impersonating the deployer can set the signer's IAM policy where the
  two projects are one; the deployer role's condition was not read. D3127 holds either way.
- **U7.** That a Batch VM's attestation can guard a claim as it would have guarded the key
  release. It rests on batch-run-identity's own Check 830.
- **U8.** Whether an execution created before an update runs the image it was created with. R7's
  runner bridge covers both answers.
- **U9.** That the credentials account exists when the Firebase stage first plans the issuer's
  row on a fresh setup. The installation root is applied first and creates it; LM13 passes the
  account only once it is there, and tests the first apply.
- **U10.** How the service reads the access records the resolver needs (F23): its database role
  reads the database, but which node and which decode `internal/access` needs was not read. LM23's
  first step.
- **U11.** Settled by review round 4, and no longer needed: an attempt names no committer
  (`Intent`, `internal/orchestrator/state.go`), and D3136 as amended asks for none.
