# C3.9's review half: reconciliation against the code

Read against `origin/main` at `a38cfeec` and against the open pull requests of 2026-11-01 (their heads
fetched and their `docs/` grepped for the decision and check numbers they write). Line numbers are that
commit's. Each finding is **Exists**, **Missing** or **Conflicts**, with its consequence for the spec or
the plan. **F1–F14**, **D1062–D1074** and **X1–X4** are this folder's own numbering. Every decision was
answered by the owner on **2026-11-01** (§3), D1063 as amended by that day's ruling **O1**, and the same
review corrected four claims the first draft made (§1, "What the review of 2026-11-01 corrected").

## 1. The findings

**F1: Missing. The service has no account class, and no place to put one.**
`Authenticator.verifyJob` (`internal/credsvc/auth.go:423`) returns a single string — the configured
spelling of the calling account — and its only two outcomes are "matched a row of `jobAccounts`" and
"refused". `jobAccounts` maps an email to `JobAccount{Email, Slug, Repo}` (`internal/credsvc/server.go:76`):
**one repository per account**, which is exactly what a per-`(profile, account)` review job does not have.
`sharedAccounts` (`auth.go:469`) is a refusal set with no shape of its own. *Consequence:* the review class
is new, it must be built from the deployment's own derivation rather than typed (§2 of
[spec.md](spec.md)), and every branch that reads the account must read the class with it (D1062).

**F2: Conflicts. Global Constraint 6's text covers the review variant, and its derivation will name it.**
The gate refuses every account in `sharedAccounts`, and `SharedJobAccountsFor`
(`internal/credsvc/server.go:154`) computes that set from the policy's profile axis
(`p.ProfileNames()` × `config.ProjectJobAccounts`, `internal/config/claude_accounts.go:115`) — the same
axis CSR7's variant is deployed on. So the moment `internal/infra/projectjobs.go` deploys the review
variant, the service refuses its account **whole**, model routes included. *Consequence:* the constraint's
**scope** must be narrowed deliberately — not lifted — and the two sets made disjoint, in one change
(D1063).

**F3: Exists. The mint's repository is the account's, and never the caller's.**
`Server.mint` (`internal/credsvc/github.go:239`) calls `mintRepository(ja.Repo)` (`:195`) and passes the
result to `github.MintInstallationToken`; the request carries the **App ID alone** (`MintRequest`, `:133`;
`readMintRequest`, `:167`). *Consequence:* the request shape does not change for a review caller. What
changes is where the repository comes from — and it must come from another **verified** place, not from
the caller (D1064).

**F4: Missing. There is no slug → canonical-repository resolution in the service.**
The run identity carries a **slug** (`Identity.Slug`, `auth.go:158`, minted from `task.Slug`), and the
service's only map from a slug to `owner/repo` is `JobAccount.Repo` — "the canonical repository name the
account's runs belong to", built by `JobAccountsFor` (`server.go:110`) from the deployment's `[]RepoJob`
input. The published policy cannot supply it either: the repository's node is keyed by slug and
`projectpolicy.RepoPolicy` carries admissions only (`internal/projectpolicy/policy.go:1039`, whose one
member is `credentials.accounts`). *Consequence:* the service needs a repository table derived from the
input it already gets, or a new published field; the design takes the table (D1066) and records the
alternative.

**F5: Conflicts. The mint's permission set is hard-coded, and the set a review needs is a fifth one that
does not exist.** `mint` passes `github.RunTokenPermissions()` (`github.go:239`;
`internal/gitprov/github/apptoken.go:91`, whose exported copy is `:152`), and
`github.MintInstallationToken` takes one set for one repository. The package holds **four** sets already —
the run's (`:91`), an image build's (`:97`), a challenger arm's (`:111`) and the coordinator's (`:128`) — and
none of them is CSR1's: build and shadow read `contents` and `metadata` and no pull request, the coordinator
reads `pull_requests` and `metadata` and no contents, and CSR1's `{contents: read, pull_requests: read,
metadata: read}` is a combination none of them has. It is `docs/cloud-senior-review/`'s **CSR1**, an open work
stream's task, and it must be **a set of its own** for the reason `shadowTokenPermissions`'s own comment
gives: a later change to one must not silently widen the other. *Consequence:* the set is chosen per class
(§4.2 of [spec.md](spec.md)), and CSR1 is a **hard dependency** of the review mint — see
[implementation-plan.md](implementation-plan.md) §"Dependencies" (D1065).

**F6: Exists. The claims that make an identity a review identity are already verified.**
`verifyRun` (`auth.go:516`) reads the identity, and `branchClaims` (`:626`) holds `fb` and `fpr` to their
rules (`token.ClaimBranch`/`ClaimPRs`, `internal/budget/token/token.go:115-116`; `token.MaxPRs` = 4), and
`Claims.Branch`'s own comment says a review or artifact run pushes nothing. *Consequence:* no new claim,
no new header, no new token type — the class rule is a reading of claims the service already checks (D1065,
§3.3 of [spec.md](spec.md)).

**F7: Exists. Every closed set the two routes touch is closed on purpose, and each one grows.**
`ServeHTTP` (`server.go:440`) refuses every path but `MessagesPath`, `CountTokensPath` and `MintPath`
(`:33`) — "there is no other route… by there being nothing to reach rather than by a check inside a
handler"; `routeAct` (`:59`) is the one rule mapping a route to an audit purpose and credential name;
`ValidPurpose` (`internal/credsvc/audit.go:109`) is a three-member closed set (`:90`, `:95`, `:104`); the
introduction's "no admin routes" property is pinned by `TestServiceHasNoAdminRoutes`
(`internal/credsvc/forward_test.go:341`), which names the paths it drives one by one. *Consequence:* the
two new routes, two purposes and the refuse-everything-else sentence change together, and the guard names
five paths (D1067, D1068).

**F8: Conflicts. D892's "a Secret Manager secret, one per account" contradicts Q6's one-container rule.**
Q6 (the owner's ruling of 2026-10-19) pins the service's reach in Secret Manager to **one container and one
accessor grant** — Global Constraint 12 — and `TestCredentialsServiceGrantsAreTheOneBundle`
(`deploy/terraform/terraform_test.go:3984`) fails on a second `google_secret_manager_secret`, a second
`google_secret_manager_secret_iam_member`, or a `condition` on the one grant;
`modules/installation/credentials.tf` creates exactly those two resources. *Consequence:* the key is a
**bundle entry**, one per account; the "one key per account, one reader" property D892 asks for is kept and
only the container-per-account spelling is given up (D1069).

**F9: Missing. The bundle's key set is closed, and the new entry has to join all of it.**
`CredentialKey` (`internal/credsvc/bundle.go:441`) decides which document keys are legal, `ParseBundle`
(`:514`) refuses one outside the set, `Bundle.Credentials()` (`:332`) is what a cascade may see, and the
published shape is `schemas/creds-bundle.schema.json` held to the loader by `TestBundleSchemaMatchesTheLoader`.
*Consequence:* the review-signing entry is added to all four in one change, and it is
**not** a cascade credential (D1069).

**F10: Exists. The write path for a bundle entry is the owner's, guarded and never printed.**
`bundleStore.add` (`internal/cli/creds.go:346`) writes a new version, `setBundleEntry` (`:869`) merges one
key into the document, `credsLostUpdate` (`:668`) refuses a write that lost a race, and no version is ever
written by Terraform (`TestNoSecretVersionsInTerraform`). What the path has **no** mode for is
**generating** a value: `readBundleValue` (`:759`) reads what the operator pastes. *Consequence:* the key's
bytes need a generating mode on a command that already exists, and a kind in `CredentialKey`'s vocabulary
(D1071).

**F11: Exists. The liveness check makes signing an end-of-run act.**
`checkLive` (`auth.go:665`) refuses a run whose write-once outcome exists, and the runner writes that
outcome at its end (`internal/budget/registry.go`; the plan's C1.10 text). *Consequence:* the ordering in
§3.4 of [spec.md](spec.md) is a design constraint, not an implementation detail, and an unsigned artifact
is the remedy's problem (re-review), not the service's (D1067).

**F12: Missing. There is no caller class that is not a job of the installation.**
`verifyJob` matches **every** caller against the installation's job accounts, and the only other admitted
thing is the run identity; there is no configuration for a verifier, and the coordinator
(`docs/coordinator/`, a resident service with its own account `fugaro-coordinator`) is not a run and holds
no run identity. *Consequence:* the verify route needs its own configured class, empty by default (D1068).

**F13: Exists. O4's admissions reader is in the tree and is the only per-repository admission the service
can check.** `PolicyReader.Admissions` (`internal/credsvc/server.go:245`), implemented by `RTDBPolicy`
(`:266`, `:281`), reads the repository's published node; the model path already fails closed on a project
with no allow-list. *Consequence:* the pairing for a project-scoped caller uses what is published rather
than a new registry (D1064, D1071's "not done" list in [spec.md](spec.md) §10).

**F14: Exists, and it is CSR7's wiring, not this slice's.** The service's URL reaches a launch through
`credentialsServiceLaunchEnv` (`internal/cli/run.go:2958`), which is deliberately per-repository — a shared
job would not be given it, because a shared job's calls are refused. CSR7's variant carries the variable in
its own environment. *Consequence:* nothing in this slice changes the launch path; the design states the
assumption it rests on, and the review variant's environment stays CSR7's to write.

**What the review of 2026-11-01 corrected.** Four claims this folder made were wrong against the code, and
each is fixed where it was made rather than only here. Two `:line` citations had also drifted by then, and
they are the fifth bullet:

- **The audit line never names pull requests.** `AuditEvent` (`internal/credsvc/audit.go:159`) is the closed
  set — Run, Repository, Job, Caller, Credential, Tier, Purpose, Status — and carries **no** pull-request
  field. The places that said a line names the identity's pull requests (the D1067 answer, and §5.2 and §5.5
  of [spec.md](spec.md)) said more than the shape allows, and now say what the line carries: the account,
  the repository slug and the run. C3.9a does **not** grow `AuditEvent` a field for it — adding a field is
  new scope, and the four replay checks of CSR3 do not read one.
- **The refusal order is the code's.** `ServeHTTP` (`server.go:440`) hands the request to `RequestFromHTTP`
  (`wire.go:842`), then `Verify` (`:457`), then reads the route's act (`:475`), and only then checks the
  binding — `ja, bound := s.jobs[id.JobAccount]`, whose `ja.Slug != id.Slug` arm is `:477` — **before** the
  stop flag at `:492`. The first
  draft listed the stop flag before the pairing. §4.4 of [spec.md](spec.md) and C3.9b's refusal order now
  name pairing first, which is what the caller reads and what the audit line records.
- **The verify route cannot sit behind `RequestFromHTTP`.** That function parses the request's identity
  headers, and `Authenticator.Verify` (`auth.go:367`) requires **both** identities — the job's Google ID
  token and the run's Firebase ID token — while `ServeHTTP` refuses an unverified request before it branches
  on the route (`:450`–`:460`). A verifier (the coordinator, or the owner's own hand-made call) is not a run
  and holds no run identity, so the route needs **its own path ahead of `RequestFromHTTP`**, with its own
  refusal and its own audit line; that is C3.9f's subject, and `internal/credsvc/wire.go` and
  `internal/credsvc/server.go` are in its file list.
- **Four citations had drifted** from the tree: `ValidPurpose` is `audit.go:109` (not `:107`),
  `TestServiceHasNoAdminRoutes` is `forward_test.go:341`,
  `TestCredentialsServeRefusesWithoutItsEnvironment` is `credentials_serve_test.go:100`, and
  `credentialsServiceLaunchEnv` is `run.go:2958`. Each is fixed at its own site; the symbol, not the number,
  is what a reader searches for.
- **Two more had drifted by the time the base commit moved to `a38cfeec`**, which is what this folder is now
  read against: `RequestFromHTTP` is `wire.go:842` (not `:152`, which is `ErrorUnverifiedRunIdentity`, the
  constant it was confused with) and `AuditEvent` is `audit.go:159`. The code that mattered had not changed;
  the two lines here had.

## 2. What this design rests on that already exists

- the two-identity verifier, with the shared-job gate decided from the verified email and never from
  `X-Fugaro-Run-Job` (`verifyJob` `auth.go:423`, the shared set's refusal `:469`);
- the mint: one App ID, one repository, one permission set, an audit line written before the token is
  handed over (`github.go:239`, `:346`);
- the audit line: a closed field set, a closed purpose set, and no free-form field (`audit.go`);
- the one bundle container, the one accessor grant and the owner-run write path (Q6; `creds.go`);
- O4's admissions reader (`server.go:281`);
- the keyless fetch path and the `Token(ctx, minValid)` refresh contract
  (`internal/gitprov/github/apptoken.go:381` for `ServiceTokens`, `:438` for its `Token`).

## 3. Decisions D1062–D1074

**Every one is answered.** The owner answered **D1062–D1071 on 2026-11-01, each as recommended**, answered
**D1072 as 0.16.0** (the recommendation was 0.15.0), ruled **O1** that day — which **amends D1063** — and
answered **D1073** and **D1074**, the two decisions the same review added. Each row keeps the recommendation it was written with, the cost of its alternative, and the
answer; the spec is written to the answers.

The block is **D1062–D1074**, taken above every number on `main` (highest written: **D1061**) and above
every open pull request's (the inventory and how it was read: [README.md](README.md) §"Numbers this work
holds"). The block was reserved as D1062–D1075 so that a decision added in review did not collide;
**D1075 is the one number still unused**. **A merger re-reads the inventory and renumbers if it has
moved.**

| D | Question | Answer | Why, and what the alternative cost |
| --- | --- | --- | --- |
| **D1062** | How is the review-only job variant's account admitted to the service? | **As a third class, decided from the verified email**: `ReviewJobAccountsFor` derives one row per `(profile, account)` from the **published policy** over the same `config.ProjectJobAccounts` axis the deployer uses, `SharedJobAccountsFor` is narrowed in the same change so no account is in both sets, and one shared helper plus `TestReviewAccountsAreTheNamesProjectJobsDeploys` keep the service's set equal to the deployed set (F1, F2) | Admitting **every** shared project job account — deleting the gate — is a few lines and is the general C3.9 destination, but it hands the model routes and the mint's *run* set to a container that mounts the App key and serves every repository of the account at once, which is the exposure Global Constraint 6 exists to refuse (F2) |
| **D1063** | What happens to Global Constraint 6, whose text says "per-repository jobs only"? | **Its scope is narrowed to the jobs it was written for, and the amendment is carried by the implementing pull request.** Coding *shared* project jobs stay refused on every route with today's sentence; the **review** class is admitted on the **mint**, the **sign** and the **model path**, and on the model path only **for its own account** — the tier the pairing resolved, and never the cascade's other tiers (§1, §2.1, §2.3 of [spec.md](spec.md)) | Lifting the constraint for all project jobs now is where C3.9 ends up, and it wants the phase-3 rules the constraint names — bucket narrowing, the shared jobs' retention, the tier-A registry — none of which is designed; refusing the review variant instead stops the whole cloud-senior-review chain, which is what E7 is about. **Amended by the owner's ruling O1 (2026-11-01):** the reviewer's model calls route through the service, so the review container holds **no** OAuth token and a review job ignores the published `routing.claude` switch rather than letting it decide; if C3.9 slips, the fallback is the token mount CSR7 describes. The narrower "mint and sign only, model routes refused" reading is withdrawn |
| **D1064** | How are the two identities paired when the account names no repository? | **By the account's published admission for the run's repository** (O4), and by the **model path's own admission check** rather than a second spelling of it: the run identity's `fs` slug is resolved through the deployment's repository table and the account that serves the call must be in `admittedAccounts(ctx, slug, canonical)` (`internal/credsvc/cascade.go:87`) — the **intersection of three** published facts (the repository's child node names the account, the shared node defines it, and **both** the account's `allow_repos` and the project's `allow_repos` admit the repository), which is what closes the `project repos rm` gap — and an absent child is an empty admission while an unreadable node is **refused**, the fail-closed rule the model path already follows. Today's `ja.Slug == id.Slug` comparison is kept exactly as it is for the repository class (F1, F13) | Trusting the caller's request to name the repository is caller text, and the mint's whole design is that the repository is **not** the caller's to name (F3); trusting that the launcher's `allow_data_to` check already happened is an admission the service cannot verify. Reading the child node's `credentials.accounts` alone would be the narrower spelling and would keep admitting a repository the project has withdrawn with `fugaro project repos rm`, which removes the repository's place in the project's allow-list and does not touch its child |
| **D1065** | Which token set does a review mint serve, and how is it chosen? | **CSR1's read-only set** for a review-class caller whose verified identity has **no `fb`** and a **non-empty `fpr`**; a review-class caller of any other shape is **refused** — never the run set; the repository class keeps `RunTokenPermissions` unchanged (F5, F6) | Choosing the set from a request field lets the caller pick its own scope; serving the read-only set to **any** identity without `fb` hands the narrow set to callers the class rule never admitted, and it makes the class — the thing that keeps a coding run out — do no work |
| **D1066** | Which repository is the mint scoped to, and how does the service learn it? | **The run identity's `fs` slug**, resolved to `owner/repo` through a **repository table** the service is configured with — the same `[]RepoJob` input the job accounts are already built from, keyed by `task.Slug` and valued by `projectpolicy.Canonical`, so neither derivation is spelled a second time (F3, F4) | **(a)** Publishing the canonical name inside the repository's policy node adds a field to a published format and makes a repository whose node is unpublished, or published by an older CLI, **unmintable** — a failure that arrives as "the service cannot mint" for a name the deployment already publishes; **(b)** letting the request name the repository is refused by D1064 |
| **D1067** | What exactly is signed at the sign route, and where does the replay binding live? | **HMAC-SHA-256 under the account's key, lowercase hex** — D892's answered algorithm, not re-decided — over the **framed message of D1073**, in a `{"alg":"HMAC-SHA-256","signature":…}` answer; the pull request and head are bound inside the artifact's bytes and checked by the coordinator's four checks (CSR3); the service's own contribution is the audit line, which names the verified account, repository slug and run, and the ordering that makes signing an end-of-run act (F7, F11) | **What the frame buys is D1073's answer**: a signature that verifies for exactly one run of one repository, so a cross-run replay is refused by the verify route itself and not left entirely to the coordinator's four checks. X3 recorded that option against D892's plainer reading and the owner took it; the algorithm and the hex rendering D892 fixed are untouched. The line's field set does not grow: it names the account, the slug and the run, and **not** the identity's pull requests, because `AuditEvent` has no field for them |
| **D1068** | What is the verify route's caller class? | **A configured list of caller identities, verified by the same code path as a job token minus the shared-job gate, empty by default**: `{"account","digest","signature","run","repository"}` in — the last two being the frame's two verified fields, which the caller names because the service recomputes the HMAC over them and naming them cannot make a signature valid that was not made for them (D1073) — **`200 {"valid":true\|false}`** out for a signature that is or is not the one recomputed, and a **typed refusal** whenever the service cannot answer at all (`state_unavailable`, the two unverified classes, `request_refused`) — so "not this account's" is never confused with "I cannot tell". The coordinator's own account joins the list when that work stream lands; the owner's may join for a manual check (F12) | Trying every admitted account's key needs no account name in the call, and it makes the service answer **which** key matched — an account oracle — and lets a signature valid under another account of the same installation verify |
| **D1069** | Where does the per-account review-signing key live, and what does rotation look like there? | **In the one bundle document, one entry per account** (Q6, Global Constraint 12, `TestCredentialsServiceGrantsAreTheOneBundle`), kind-checked so no cascade ever sees it, named by the audit line, with the **previous entry carried beside the current one for a rotation window** and nothing older accepted; a rotation that cannot be read is a service that **refuses to sign**, never one that signs under a key the operator believes retired (F8, F9) | D892's literal "a Secret Manager secret, one per account" needs a second container and a second accessor grant, which Q6 forbids and a Terraform test refuses; keeping the key in the bundle costs only the container-per-account spelling, and the property that matters — one key per account, one reader — is kept |
| **D1070** | What of the general C3.9 does this slice **not** do? | **Everything in §10 of [spec.md](spec.md)**: coding runs on shared jobs stay refused; Task 3.4a's refusal stays as written; the project-centric §7 registry is not built and O4's published admissions stay the source; `--provision-credentials` still does not reach `modules/project`; the review class's model path is **tier A and nothing else**, so no tier registry, no fallback ladder and no bucket narrowing is designed here (D1063 as amended); the coordinator, the artifact format, the four replay checks and the mutation check are other work streams' (F2, F13) | Doing the whole of C3.9 now needs the phase-3 shared-job rules that are not designed (the bucket narrowing F15 of the credentials plan records, the retention paragraph), and it would move coding runs' model credentials to the service with no consumer waiting for them — a release-sized change for a design that needs two routes |
| **D1071** | Who produces the key's bytes, and how does an installation get them? | **The CLI, at the owner's terminal, through the write path `github-app-key` already travels** (`creds set`'s merge with its lost-update guard): 32 random bytes generated in the process and written into the bundle's next version, **never printed, logged or shown**; `fugaro doctor` names the missing entry, and the service's refusal names it too, so the step is reachable from the two places an operator already looks (F10) | The **provisioning apply** generating it puts key material in a stage whose contract is that no credential value ever reaches Terraform or its state, and an installation already opted in never re-runs that stage — so the key would exist on new installations and silently not on old ones, which is the failure the HARD RULE names |
| **D1072** | Which release carries it? | **0.16.0, and before the review work's CSR7 and CSR2 in the same window**: CSR7 is the variant whose account this slice admits and CSR2 is the command that launches it, so this work is what unblocks them; the service converges by `fugaro update` (the image pin) with the key's bytes as the service's own documented `creds set` step, both of which are steps the credentials service already has (§9 of [spec.md](spec.md)) | **0.15.0** was the recommendation, on the ground that the first slice (E1 + CSR1 + CSR7 + CSR2) is already 0.15.0's and this work is what unblocks it; the answer given on 2026-11-01 spends that release and takes 0.16.0. The **order** is unaffected by the slot and is not optional: landing this **after** CSR7/CSR2 ships a launch the service refuses — the exact HARD-RULE failure D893's entry says the design must not ship — so a window that moves one of the three moves all three |
| **D1073** | Does the signature bind the run and the repository, or only the digest's bytes? | **The run and the repository, by framing**: the HMAC is taken over `v1\n<repository slug>\n<review run>\n<digest>` — the digest as lowercase hex, the slug and the run as the identity's own verified `fs` and `fr` — and every field is length- and charset-bounded before it is framed, so two different triples cannot frame to the same bytes. The verify route therefore refuses a signature made for another run of another repository (or the same repository's other run) **by itself**, and the coordinator's four checks (CSR3) keep their own jobs (X3, now closed) | **D892's digest-only construction** is the alternative, and its cost is the gap §7 of [spec.md](spec.md) stated at its true size: the signature says "this account" and not "this run", so a refusal of a replay is entirely the coordinator's to make — and the coordinator can only make it if it holds the artifact it is checking. The frame costs two fields in the verify call (D1068) and makes the signed bytes a function of the run as well as the digest, which is the point. **Answered by the owner, 2026-11-01, as X3's option** |
| **D1074** | How many signatures may one review run obtain, and what has to hold before the sign route is built? | **One, enforced by a write-once marker — and the mutant-subprocess egress denial is a hard precondition of building the route.** The service writes a mark for `(slug, run)` as it signs, with the create-only discipline the probe lease already uses (`ProbeLeases`, `internal/credsvc/probe.go:100`; `PutIfMatch` with an absent ETag, `internal/rtdb/client.go:209` — create-only, because the service's own writes go through IAM and are not governed by the rules), and a second sign for the same run is **refused**: one line, the sign's purpose, the wire's existing `request_refused` status — the status set is closed and does not grow — plus a warning in the service's own log, so what an operator's alerting selects is a line they can name today rather than a field added for it. Before the route is built, **U7**'s owner-run observation must have returned: a subprocess inside the review container — which runs the package's own untrusted test code (§7) — cannot reach the network, **the instance metadata server included**, because that server is where a Google ID token for the job's own service account comes from. Both identity tokens, the job's Google ID token and the run's Firebase ID token, are registered in the service's redaction set for the call | **Neither half has a cheap alternative.** Without the mark, a container that can reach the service signs as often as it likes; the write-once outcome `checkLive` reads (F11) bounds *when* signing may happen and not how often. Without U7's observation the route rests on an unverified assumption — which is what U7 was raised for, in `docs/cloud-senior-review/`'s Check 125 step 6, where a child that cannot be fenced means **D891 (a) is amended and the owner is asked again** rather than the check falling back quietly to `denyWeb`. The mark's own cost is one node per signed run and a write the service makes outside the cascade. **Answered by the owner, 2026-11-01** |

## 4. The items this work found, and where each stands

**One is still open (X1), and three were settled on 2026-11-01** — X2 by ruling O1, X3 as **D1073**, X4
with **D1071**. Each item keeps the text it was raised with, and its heading says what happened to it.

**X1 (open) — What the coordinator's own class should be.** D1068 gives the verify route a configured list of
identities so this slice needs nothing from `docs/coordinator/`. Whether the coordinator should instead
have a *typed* class of its own (a named role, a distinct token audience, a request that must carry the
review run it launched) is that work stream's to decide, and this design deliberately does not pre-empt it.

**X2 (settled, 2026-11-01, by ruling O1) — Whether the review job's model call should ever come from the
service.** As raised: it did not — CSR7 mounted the account's Claude token on the variant and §10 refused the
review class the model routes — and a later slice that wanted the review job keyless for *model* calls too
would have to answer what a tier and an admission mean for a job with no repository of its own. **The ruling
amends D1063 so that they do come from the service**: the review container holds no OAuth token, and the
question this item posed is answered by the pairing §3 already designs — the review class's model path is
**tier A only** (the one account the pairing resolved), never the cascade's other tiers. If C3.9 slips, CSR7's
token mount is the fallback the ruling names rather than a second design.

**X3 (answered as D1073, 2026-11-01) — Whether the sign should bind the run and the repository into the
MAC.** As raised: D892 answered the algorithm (the HMAC of the digest), so the signature bound the bytes and
not the identity that signed them, and a framed message (`v1\n<repository>\n<review_run>\n<digest>`) would
make a signature valid for exactly one run of one repository and let the verify route refuse a cross-run
replay **by itself** rather than leaving it to the coordinator's four checks — stronger than what D892
answered, and a change to a construction that ruling fixed. **The owner took the frame (D1073)**: the
message is `v1\n<slug>\n<run>\n<digest>` over the identity's own verified `fs` and `fr`, the verify request
carries the same two fields (D1068), and the item is closed rather than recorded.

**X4 (answered with D1071, 2026-11-01: it should not) — Whether an ordinary `fugaro update` should generate
a missing review-signing key.** §9 of
[spec.md](spec.md) reads the HARD RULE as satisfied because nothing breaks without the key, and because the
key is the credentials service's own documented step. An owner who reads the HARD RULE more strictly would
want `fugaro update` on an opted-in installation to generate it, which needs a decision about a command
writing a secret value into a document outside the owner's `creds set` ceremony. The owner answers this one
with D1071.

## 5. What a merger must re-check

1. **The D-block** ([README.md](README.md) §"Numbers this work holds"): the open pull requests and the
   merged tree, renumbered if the first free block has moved.
2. **The live check's number** ([spec.md](spec.md) §8): the implementing task takes the number free at
   **its** merge.
3. **The `:line` citations** under the convention in [README.md](README.md).
4. **CSR1's set's name** (`internal/gitprov/github/apptoken.go`): if that work stream lands the read-only set
   under a different symbol or with different members, this design serves what landed and the mint's
   refusal tests name it.
