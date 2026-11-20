# A launcher does not mint a run's identity: reconciliation

This is the spec as amended against the code. **Where it disagrees with [spec.md](spec.md), this
document wins.** Checked against `origin/main` at `7987acb99` on 2026-11-20. Two open pull
requests were read on their branches: batch-run-identity (#3323, `docs/batch-run-identity` at
`910a545a7`) and credentials-belong-to-models (#3315, `docs/credentials-belong-to-models` at
`d78e355ff`). Owner: Dimi Paun. Every decision below was asked with its options and costs on
2026-11-20, and the owner's answer is in its cell.

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
  the claims in R4.

## What the spec names, and what the repository has

| The spec says | The repository has |
| --- | --- |
| "the grant that lets a person sign as the runs' signer" | `google_service_account_iam_member.minter`, `for_each = local.people`, role `fugaroTokenMinter`, on the runs' signer (`modules/firebase/iam.tf`) |
| "something that is not the launcher's machine, and that checks who is launching" | The credentials service's launcher class: `Authenticator.VerifyLauncher` (`internal/credsvc/auth.go`) and `Launchers.VerifyLauncher` (`internal/credsvc/launchers.go`), a Google ID token checked against the roster `fugaro update` renders as `FUGARO_LAUNCHER_ACCOUNTS`. The CLI already calls it at launch: `slipGrantOverService` (`internal/cli/run.go`) to `SlipPath` (`/v1/slip`) |
| "the launcher roster" | Two lists from one source: the IAM members `var.launchers` and `var.operators` (Firebase root), and the bare addresses `var.launcher_accounts` (installation root, `internal/infra/launchers.go`), which holds the `user:` entries only |
| "the coordinator [has a] signer grant of its own" | Nothing. See F2 |
| "pairing tokens, device tokens, the mobile account's" | `token.MintPairing` and `token.MintDevice`, both called from `internal/cli/pair.go` with the runs' signer; and a second, device-only signer for the public mobile service (`google_service_account.device_signer`, `mobile_token_minter`) |
| "the job's platform" as an issuer | Rejected already as alternative C4 of `docs/design/budget-token-channel.md` |

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
and the CLI already asks it for something at launch (`slipGrantOverService`). Consequence: D3121.

**F2 (Wrong). The coordinator holds no signer grant and launches nothing.** The brief says the
orchestrator and the coordinator have signer grants of their own. Only
`orchestrator_token_minter` exists; `modules/installation/coordinator.tf` grants its account a
bucket reader, a secret accessor, the credentials service's invoker and the database admin role,
and no signing. `ee/coordinator` calls neither `launchRun` nor `token.Mint`. Consequence: the
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
once inside it (U3). `docs/design/budget-token-channel.md` says the seal is "a capability, not a
boundary" and keeps a launcher out of scope because "a launcher can already mint". Once a
launcher cannot mint, that exemption is gone and the copy is the remaining way to ask 3's
"obtain". Consequence: D3122.

**F10 (Gap). Nothing server-side binds a run to the person who launched it.** The launcher chooses
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

**F18 (Rule). This changes batch-run-identity's design.** That plan (#3323) builds a KMS key, a
launcher-wrapped seal key, a registration and a release route so a Batch VM can open the sealed
token (its D3101, D3102, D3108, R1), because "any carrier the run account opens can be replayed by
a second launcher" (its F4). With a ticket the run claims there is no sealed token to open; the
same VM attestation can guard the claim. Its F24 and O6 are this spec's origin, and its O12 (the
launcher's identity token is a bearer token for a wider audience) applies here unchanged (O5).
Tasks of theirs this touches: **C1** (the key-release wire), **BR1**, **BR2**, **BR4**, **BR5**,
**BR17**. Untouched: **BRS**, **BR0** (Check 830), **BR6**. Decisions of theirs this touches:
D3101 and D3108 keep their intent and change their object (from the seal key to the claim);
D3102 is strengthened (the same service); D3109 (people only) meets D3123 here, which admits
roster service accounts, and the amendment of that plan has to settle the two (O2). Consequence:
D3129.

**F19 (Rule). credentials-belong-to-models adds another thing a launcher says about a run.** Its
R15 has the launcher resolve each craft's access and put it in the execution's environment, and
states "the runner's own answer is never a boundary: the service serves a call only what the
record names". This spec does not change that. The issuance record (R3) is a place a later plan
could move that answer to; nothing here does (O6).

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
`docs/design/roles.md`, `docs/design/budget-token-channel.md` and
`docs/mobile/design/always-on-orchestrator.md` (section 4.5, E4). Consequence: the plan's
documents task rewrites each; "may mint" becomes "may launch".

**F22 (Exists). This is the ruling `docs/design/roles.md` already recorded.** D628 (answered
2026-10-29): "A minter the ranked people cannot call." D686 asked where it lives and noted that
narrowing the role "takes the minter away from `fugaro pair` and the run's budget token". This
spec carries both out.

## What each option does to the trust boundary

Before: every launcher, every operator and `fugaro-orch` can sign **any claims for any uid** of
the Firebase project: any run, any device, the consumer. A stolen launcher laptop is that reach
for as long as its refresh token lives.

| | Who can mint what, after | A stolen launcher laptop | Single point of |
| --- | --- | --- | --- |
| **Credentials service issues a one-time ticket (chosen: D3121, D3122)** | Only the credentials account signs. It signs a run's identity for a ticket a roster caller asked for, and a pairing or device token for a verified person; no route takes a uid or a claim set | Launches runs on any repository of the installation, up to the caps, as that person and on the record. Cannot sign anything, cannot take over or re-issue another person's run, cannot mint a device token for anyone else | Every launch and every run start. It already is the single point of every model call once keys-through-service lands |
| Credentials service returns the token | Only the service signs, but the token crosses the launcher's machine and the sealed channel | As above, plus a silent copy of any run's token inside its hour (F9) | Every launch |
| A second service with its own account | Only that account signs; the account that holds the keys does not | As the chosen row | Every launch; one more service to keep up |
| The workspace service | Nothing, for an installation without a workspace | not applicable | Every launch of a member; no answer for the default installation |
| The job's platform identity | Any code in any execution of the job | Launches, and the agent in any run of a shared job claims any run's identity | Nothing new, and no boundary |

## Decisions

Numbers D3120–D3130 are used; the block is D3120–D3139 (see the README for the reading).

| # | Question | Answer (owner, 2026-11-20) |
| --- | --- | --- |
| **D3120** | The spec's ruling section is a placeholder. Is the coordinator's reading right: no launcher or operator holds the grant that signs a run's identity, and a run gets its identity at launch from something off the launcher's machine that checks who launches, which repository and which run? | **Yes, as written.** As recommended. Why: the reading is the spec's six asks. `spec.md` keeps the placeholder; this cell is the confirmation. |
| **D3121** | What issues the identity? Recommended: the credentials service, on new routes for the launcher class. Alternatives: a second service of the same image with its own account (about 3 more tasks, one more service; the rule in F7 untouched); the workspace service (no answer for an installation without one); the job's platform (C4, rejected). | **The credentials service.** As recommended. Why: its launcher class, its roster and the launch-time call exist (F1). **This amends the rule that the service account's only grant is its database role** (F7, F8): it gains the minter role on the runs' signer account. |
| **D3122** | What does the launcher get back? Recommended: a one-time ticket the run redeems, so the launcher never holds the identity. Alternative: the token itself on today's sealed channel (runner and images untouched; a second launcher can still copy it silently, F9). | **A one-time ticket.** As recommended. Why: ask 3 says "obtain", and a copy becomes a race whose loser reports it. Cost accepted: the runner and the image contract change, every run start depends on the issuer, and on Cloud Run a peer launcher who also runs code as the job account can still win the race, detected and not prevented (R5). |
| **D3123** | What happens to roster entries that are not a person? Recommended: a `serviceAccount:` entry becomes its own class at the issuer; a `group:` entry cannot be issued to, and `update` names each before it removes the grant. Alternatives: people only (matches batch-run-identity's D3109; a pipeline's service account stops); both, with groups resolved through a directory (a grant outside the managed project). | **Service accounts yes, groups no.** As recommended. Why: a service account's ID token names it exactly; a group needs a lookup the installation does not have. |
| **D3124** | How does the removal reach a teammate's older CLI? Recommended: one release; `update` removes the people's grant only after the issuer answers, and the published policy's version moves so an older CLI refuses with the upgrade sentence. Alternatives: two releases (F24 open one more release); one release with no lever (IAM's 403 and the wrong fix). | **One release, with the policy version bump.** As recommended. Why: pre-1.0, and the older CLI's own refusal names the fix. Cost accepted: every launcher upgrades before their next launch after the operator's update. |
| **D3125** | The resident orchestrator loses its minter row and asks the issuer under its own class. What may it be issued? Recommended: a run of any repository of the installation, as it launches today. Alternatives: only chains a person started (about 2 more tasks; a root launched before the upgrade has no record); it keeps its own grant (a second minter). | **Any repository's run.** As recommended. Why: parity, and already narrower, since the issuer signs run identities only. |
| **D3126** | `fugaro pair` signs with the same grant (F3). What happens to pairing? Recommended: the issuer mints the pairing and device tokens for a verified person, on routes that take no uid and no claim. Alternatives: wait for the mobile plan's own minter (blocks the removal); pairing stops and `update` says so. | **The issuer mints both.** As recommended. Why: it closes H1 and keeps `fugaro pair` working. |
| **D3127** | Operators can still reach signing through the deployer account, and by re-granting (F6). What does the spec claim about operators? Recommended: no standing grant, and SECURITY.md says the rest; doctor lists every principal beyond the issuer. Alternatives: also narrow the deployer grant to `getAccessToken` (about 2 more tasks on the update's own impersonation chain; one hop removed, not the reach); claim it only where the Firebase project is a separate one. | **No standing grant; the documents say the rest.** As recommended. Why: an operator administers the installation's IAM; the honest claim is the standing grant and the audit trail. Ask 1 therefore holds in full for launchers, and as "no standing grant" for operators. |
| **D3128** | When does it ship (F16, F17)? Recommended: tasks start after always-a-budget's I1 and keys-through-service's release A, and it ships in the CLI release after them. Alternatives: with always-a-budget (that plan's no-release window grows); ahead of both (built on code AB4 deletes). | **After both, in its own release.** As recommended. Why: one mint path to replace, and every job reaches the service. Cost accepted: F24 stays open until those two land. |
| **D3129** | What happens to batch-run-identity (F18)? Recommended: its C1 and the tasks behind it (BR1, BR2, BR4, BR5, BR17) wait for a documents pull request that re-plans its release as a VM-attested claim on this plan's contract; BRS, BR0 and BR6 may go. Alternatives: Batch goes ahead as planned (a sealed channel built for one backend); decide after Check 830. | **Hold C1, amend that plan.** As recommended. Why: no KMS key and no wrap to build and then delete. **This puts on hold the go the owner gave that plan's C1 on 2026-11-20**; BRS, BR0 and BR6 keep theirs. |
| **D3130** | Ask 2 says the issuer checks "for which repository". There is no per-repository launch list. What does it check? Recommended: that the repository is one the installation has a job for, and it binds the ticket to the account that job runs as. Alternative: a per-repository launch list (a spec of its own, about 4 more tasks, a setup step per repository). | **It is the installation's.** As recommended. Why: any roster launcher may launch any repository today; this spec changes who signs, not who may launch. |

No answer departs from its recommendation. No answer was "your call".

## Resolutions

One defensible answer each; stated, not asked.

- **R1. The issuer unreachable.** A launch that cannot reach the issuer fails with the reason and
  the fix, after a bounded retry the size of the slip grant's (`slipGrantTimeout`). Nothing
  degrades to local signing, because the only thing that could is the grant this spec removes. A
  run that cannot claim waits and retries for a bounded time and then halts, as a workspace
  member's run does (workspace-service D2983).
- **R2. The routes.** Four, on the credentials service: issue a ticket, claim an identity, mint a
  pairing token, mint a device token. Their paths, bodies, refusal codes and the name of the
  execution variable that carries the ticket are the contract task's (C1). The claim is answered
  ahead of the shared request parse, as the merge route is for `VerifyOrchestrator`, because the
  caller holds no run identity yet.
- **R3. The issuance record.** The issuer writes, once per slug and run, who asked (the verified
  address and class), when, the claims it will sign, the account the claim must come from, a
  digest of the ticket and whether it was claimed. It lives beside the run's budget record, and
  the database rules refuse it to every token holder; batch-run-identity's registration (its
  D3108) is the same record with a job name on it. This is what makes a re-issue checkable (F10).
- **R4. What the issuer checks before it issues.** The caller is on the roster, in one of three
  classes (a person, a roster service account, the orchestrator). The repository is one the
  installation has a job for (D3130). The account the launch names is that repository's job
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
  claim first. The victim run halts and the audit line names the claim; it is detected, not
  prevented (F11, Check 840). On Batch the VM attestation can prevent it (D3129).
- **R6. A retry by the same person.** Before the ticket is claimed, the same caller asking again
  for the same slug and run gets a new ticket and the old one dies. After it is claimed, a new
  attempt takes a new run id, as a follow-up already does.
- **R7. One release of bridge, on both sides.** The CLI of this release asks the issuer; where the
  service does not serve the route (an installation not yet updated, F15) it signs locally as
  before, which works exactly as long as the people's row is still there. The runner of this
  release claims when the execution carries a ticket, and opens the sealed object when it carries
  the old marker, so an execution created before the update finishes. Both old paths, the seal and
  the `tokens/` prefix are deleted in the release after (plan task LM18, with its own go).
- **R8. `update` moves the grant late, and only once the issuer answers.** The Firebase root gains
  the issuer's row at its usual stage and keeps the people's row behind a switch that is still
  on. A stage after the credentials service's own probes the ticket route; when it answers, the
  stage applies both roots with the switch off, which removes the people's row and the
  orchestrator's. When it does not answer, the rows stay and the update says why. Nothing is
  asked (no-prompt policy, spec §1).
- **R9. Runs in flight.** A run that already exchanged its token holds a Firebase session that no
  IAM change touches; a queued run holds a token already signed (`docs/design/update-in-flight.md`,
  the Firebase row). Removing a signing grant reaches neither. The policy version bump is
  shape-free and follows version 10's ordering. `TestUpdateLeavesARunInFlightRunning` gains the
  case.
- **R10. What `update` says before the grant moves.** One line each, before the stage runs: the
  `group:` roster entries that can no longer launch, by name, with the fix (name the people);
  that a CLI older than this release must be upgraded, and that it will be told so; that
  `fugaro pair` now asks the service. And the standing line about runs in flight.
- **R11. The launcher's name.** `rb` stays the self-asserted name it is: a field never changes
  meaning. The verified address is on the issuance record (R3) and in the audit line. No claim is
  added (out of scope).
- **R12. The mobile account's signer.** Unchanged. It is a service's grant on a signer of its
  own, not a person's, and the spec's ask 1 is about people. H1 closes through D3126, not through
  that signer.
- **R13. The coordinator.** Unchanged (F2).
- **R14. Who may pair.** The people who can today: the roster's launchers and operators, with
  `pair.go`'s own role checks unchanged. The issuer derives the pairing uid and the device's
  claims itself.
- **R15. Check 840 does not gate.** If it finds that an execution's identity token carries
  something of the execution, a contract change (R-C1-execution) binds the claim to it and the
  residual in R5 closes for Cloud Run.

## Answers to what the brief did not know

| The brief's question | Answer |
| --- | --- |
| What issues the identity instead | The credentials service (D3121) |
| How the issuer knows the caller may launch, and what it trusts | The roster as `fugaro update` renders it to the service, by verified Google ID token (R4, D3123). A roster change reaches it at the next update, as it reaches the invoker grant today |
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
- **O2. batch-run-identity's amendment is not written.** D3129 holds its C1; the documents pull
  request that re-plans it is task LM19 here and needs its own go. It must settle D3109 (people
  only) against D3123 (roster service accounts).
- **O3. #3323 is open and the owner is reading it.** This pull request does not edit it. If it
  merges as it stands, LM19 amends it on main.
- **O4. An installation with no published policy has no lever for an older CLI** (F14). There the
  older CLI shows IAM's own error. always-a-budget makes a database required for a launch; whether
  that also means a policy is always published by then is U5.
- **O5. A launcher's Google ID token is a bearer token.** The class also admits gcloud's audience
  (accepted under D1939 and D1944), replayable for about an hour by any service that received it.
  A replay obtains a ticket as that person, on the record. Stated, not fixed; it is
  batch-run-identity's O12.
- **O6. The launcher's other statements about a run** (branch, pull requests, context
  repositories, and #3315's access entry) stay the launcher's. The issuer signs what a roster
  caller asks for its own run (A6, F19).
- **O7. A roster change waits for an update** to reach the issuer. A launcher removed from the
  roster keeps the class until then, as they keep the invoker grant today.
- **O8. The other plans that may move the policy version.** The bump takes the next version at
  merge time; if another plan lands one first, this plan's bump is that plan's, or one more.
- **O9. The estimate has no cost.** No history is cached for these tasks on this machine, so the
  graph read prints a time range and no dollar figure.
- **O10. Whether D3128's hold covers LM19.** LM19 is a documents task that unblocks Batch. D3128
  as answered lets only C1 and LM0 start early, so LM19 is held by default; lifting that is the
  owner's.
- **O11. The graph draws no edge from LM1 or LM19 to C1.** `fugaro plan graph` admits an edge
  onto a contract only from the consumer side of an edge the contract broke. The plan's own text
  carries both waits.

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
  (O4).
- **U6.** Whether an operator impersonating the deployer can set the signer's IAM policy where the
  two projects are one; the deployer role's condition was not read. D3127 holds either way.
- **U7.** That a Batch VM's attestation can guard a claim as it would have guarded the key
  release. It rests on batch-run-identity's own Check 830.
- **U8.** Whether an execution created before an update runs the image it was created with. R7's
  runner bridge covers both answers.
