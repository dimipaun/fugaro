# The review identity, the read-only mint and the signed verdict

This is the design. Where it and [reconciliation.md](reconciliation.md) disagree, **the reconciliation
wins**. The owner decisions it is written to are **D1062–D1072**, each with the cost of its alternative,
in [reconciliation.md](reconciliation.md) §3.

---

## 1. The slice, and the boundary it keeps

**The problem, in the service's own terms.** `Authenticator.Verify` (`internal/credsvc/auth.go:367`) checks
two identities on every call: the calling job's Google ID token, whose `email` claim
`verifyJob` (`:423`) matches against the installation's job accounts, and the run's Firebase identity,
checked by `verifyRun` (`:516`). One rule is decided from the verified email and never from the caller's
header: **Global Constraint 6**'s shared-job gate (`:460`), which refuses every account in
`Authenticator.sharedAccounts` — `SharedJobAccountsFor`'s derivation over the published policy
(`internal/credsvc/server.go:154`). The review variant is one of those accounts the moment
`internal/infra/projectjobs.go` deploys it, so **today the service refuses the review job whole**, and the
mint `Server.mint` (`internal/credsvc/github.go:239`) mints from `ja.Repo`, **the one repository a
per-repository job's account names** — a name a per-`(profile, account)` job does not have.

**What the slice adds is three things and no more:**

1. a **third account class** — the review-only job variant, per `(profile, account)`, admitted as such
   (§2, §3);
2. a mint that serves **R1's read-only permission set** for a review caller, scoped to the repository the
   **run identity** names, within what the account is admitted for (§4);
3. the **sign and verify endpoints** for the verdict digest, with a per-account key the service alone
   holds (§5, §6).

**The boundary is the whole design.** Global Constraint 6 is not lifted; it is **narrowed to the jobs it
was written for**. Its reason is that a shared project job's account (i) mounts the provider keys and the
App key with live accessors, (ii) reaches the whole `runs/` prefix, and (iii) serves many repositories at
once — so admitting it to the service's **model** path would put a credential of a container that runs
untrusted code behind one shared account. The review variant is a **different job**: it mounts the
account's Claude token and nothing else, with no App key, no signing key and no accessor on either
(`docs/cloud-senior-review/implementation-plan.md` R7), and it is admitted on **two routes** — the mint
and the sign — and **refused on the model routes with the sentence the service already answers**. A
*coding* run in a shared project job is refused on every route, before and after this lands (D1062,
D1063).

---

## 2. The account classes

### 2.1 Three classes, decided from the verified email

`verifyJob` today returns one thing: the configured spelling of the calling account, or a refusal. It
gains a **class** — the same answer the rest of the call branches on:

| Class | Who | Where it comes from | What it is admitted to |
| --- | --- | --- | --- |
| **repository** | a workflow job of one repository (`gcp.ServiceAccountID(slug, workflow)`) | `JobAccountsFor` (`server.go:110`), which binds email → `{Slug, Repo}` | everything the service does today: the model routes, the mint (the run set), the stop flag |
| **shared** | a *coding* shared project job (`gcp.ProjectServiceAccountID(profile, provider, account)` over `config.ProjectJobAccounts`) | `SharedJobAccountsFor` (`:154`) | **nothing** — refused whole, exactly as today |
| **review** | the review-only variant, per `(profile, account)` | new, `ReviewJobAccountsFor` (§2.2) | the **mint** (the read-only set) and the **sign**. Refused on `/v1/messages` and `/v1/messages/count_tokens`, and refused the verify route |

An account is in exactly one class, and an account that a deployment's input puts in two is refused rather
than admitted — the rule the shared gate already applies (`auth.go:469`: "an account that is in both sets
is refused rather than admitted: the refusal is the safe answer").

The class is read from the **verified email** and never from `X-Fugaro-Run-Job`, for the reason the shared
gate already gives: one service account is dedicated to one Cloud Run job, so the account *is* the job,
while the header is the caller's own text. The `JobAccount`/`ReviewJobAccount` rows are built from what
the *deployment* publishes, so a caller cannot name its own class.

### 2.2 The review set is derived, never listed

`ReviewJobAccountsFor(p *projectpolicy.Policy, gcpProject string) []ReviewJobAccount` mirrors
`SharedJobAccountsFor` exactly: it walks `p.ProfileNames()`, takes `config.ProjectJobAccounts(pr.JobProfile)`
(`internal/config/claude_accounts.go:115`) — the **one** function the deployer, the launcher and the
runner already share for the job axis — and derives each account's email through the same
`serviceAccountEmail` (`server.go:178`) over the variant's own service-account ID. The variant's
service-account ID spelling is **R7's**, not this design's (`docs/cloud-senior-review/` R7: "same axis,
same `gcp.ProjectJobName` shape with a review suffix"); what this design fixes is the *requirement*:

- **one derivation**: the service and `internal/infra/projectjobs.go` compute the review accounts from one
  shared helper rather than two spellings, so the accounts the service admits are the accounts the
  deployer created;
- **one input**: `ReviewJobAccountsFor` reads the **published policy**, so an installation that has not
  published a profile for an account admits no review job for it — the same fail-closed shape
  `SharedJobAccountsFor` has;
- **one test**: `TestReviewAccountsAreTheNamesProjectJobsDeploys`, which builds one installation both ways
  and requires the two sets to be equal — the shape
  `TestSharedJobAccountsAreTheNamesProjectJobsDeploys` already establishes for the shared set;
- **`SharedJobAccountsFor` is narrowed in the same change** so the variant's accounts are not in both
  sets: the shared set keeps naming the *coding* jobs, and the review variant's accounts leave it. A
  test pins that the two sets are disjoint, because an account in both would be refused everywhere and the
  review job could never mint.

### 2.3 What a review account never gets

The class buys two routes and no privileges beyond them. In particular a review account:

- reads **no model credential** from the service: the model routes refuse it with the Global Constraint 6
  sentence, so the service's model path keeps its per-repository-only property and the review job's model
  call is the one R7 wires (its mounted `CLAUDE_CODE_OAUTH_TOKEN`);
- cannot be given more by a request: the class is not a field of any request (§5.2), and the repository a
  mint is scoped to is the run identity's (§4.1).

---

## 3. The two identities, and the pairing

### 3.1 The identities are the ones the service already verifies

A review call presents exactly what every other call presents (`internal/credsvc/wire.go`; `Request`,
`auth.go:142`): the job service account's Google ID token in `Authorization`, whose `email` claim is
folded ASCII (`foldEmail`, `:508`) and matched against the admitted accounts, and the run's own Firebase
ID token in the run-identity header, verified against Google's keys with `fp` checked against the project
and the run's liveness read from the write-once outcome (`checkLive`, `:665`). **No new claim, no new
header and no new token type is introduced by this design.**

### 3.2 The pairing, which is where the two classes differ

For a repository job the pairing is one comparison: `ja.Slug == id.Slug` (`server.go:476`) — the account
names a repository and the run must be one of that repository's. A per-`(profile, account)` review job
names **no** repository, so that comparison cannot be made, and it must not be replaced by "trust the run
token": a run identity is verified on its own, and nothing in the two verifications pairs them
(`docs/cloud-senior-review/reconciliation.md`, finding m2, which records the same gap for the shared
jobs).

**The pairing for a review caller is the account's admission**, read from what the launcher already
publishes (O4, `docs/credentials-service/implementation-plan.md`):

1. the repository is the run identity's `fs` slug — the only repository the call can be about, because it
   is the one the *launcher* minted into the token;
2. the slug is resolved to a canonical repository through the service's **repository table**
   (§4.1), and an unknown slug is refused;
3. the account the call is served as (the profile triple of the **review** class row) must appear in that
   repository's published admissions, `PolicyReader.Admissions(ctx, slug)` → `projectpolicy.RepoPolicy`'s
   `credentials.accounts`; **a repository with no published node, or one whose node cannot be read, is
   refused** — fail closed, the rule the model path already follows for a project with no allow-list;
4. the run identity must be a **review** identity (§3.3).

Both halves of the admission are the launcher's and neither is new: the account's `allow_data_to` must
list the repository or the **launch** is refused (`claudeAccountAllowed`, `internal/cli/run.go:1790`), and
the run identity can only be minted by whoever holds the launcher's minting authority. The service's check
is the second, independent reading of the same fact — the reading that is available to a service which
must not trust the caller.

### 3.3 What makes a run identity a review identity

Read from the claims the service already verifies (`verifyRun` → `branchClaims`, `auth.go:626`), never
from a request field:

- **`fb` absent or empty** — the identity pushes nothing (`internal/budget/token/token.go:115`). A review
  run and an artifact run both satisfy this; a **coding** run does not, which is what stops a coding run
  launched into the review job from being served the read-only set (it is **refused**, not downgraded);
- **`fpr` present and non-empty** — the identity names the pull requests it reviews
  (`token.go:116`, at most `token.MaxPRs` = 4). This is what distinguishes a review identity from an
  artifact identity, which pushes nothing and writes no pull request either.

A review call whose run identity carries an `fb`, or a `fpr` that is present but empty, is refused with a
typed refusal naming the claim that failed and never its value. The refusal vocabulary is the one the
two-identity check already answers with — `ErrUnverified` in Go, `unverified_run_identity`
(`ErrorUnverifiedRunIdentity`, `StatusUnverified`) on the wire (`internal/credsvc/wire.go:152`, `:176`) —
so no runner needs a new switch arm and no caller learns a new error type.

### 3.4 Signing is an end-of-run act

`checkLive` refuses a run whose write-once outcome exists, and the runner writes that outcome at its end.
So the order is fixed and the design states it rather than leaving it to be discovered: **the review job
writes `review.json`, computes its digest, signs, and only then ends** — the artifact and its signature
are written while the run is still live. A review run that has already ended cannot be signed; a job killed
before that point leaves an **unsigned** artifact, which the coordinator must refuse
(`docs/cloud-senior-review/` R3), and the honest remedy is re-reviewing the head, not re-signing — a
re-signing path would be a way to obtain a signature for bytes a later process chose.

---

## 4. The read-only mint

### 4.1 The repository comes from the run identity, and is resolved through the deployment's own table

`Server.mint` today takes the repository from the account: `mintRepository(ja.Repo)` (`github.go:239`),
turned into `owner/repo` for `github.MintInstallationToken`, with the request carrying the **App ID alone**
(`MintRequest`, `readMintRequest`, `:133`/`:167`) — the repository is deliberately not the caller's to
name. For a review caller `ja.Repo` does not exist, and the caller must not gain the power to name it, so:

- the repository is the run identity's **`fs` slug** (`Identity.Slug`, `auth.go:158`);
- the slug is resolved to a canonical repository through a **repository table** the service is
  configured with — the same `[]RepoJob` input `JobAccountsFor` already receives
  (`server.go:85`), which carries each repository's canonical name beside the workflow. The table's
  keys are `task.Slug(provider, repo)` and its values `projectpolicy.Canonical(provider, repo)` — the
  two derivations the runner's identity and the mint already use, so no second spelling of either is
  introduced. An unknown slug is refused: a slug the deployment does not know is not a repository this
  installation has;
- `mintRepository` (`github.go:195`) then splits the canonical name exactly as it does today, and the
  answer's `Repository` field carries it (`MintResponse`, `:157`).

*The alternative — publishing the canonical name inside the repository's policy node — is alternative
**(a)** of [D1066](reconciliation.md) §3: it adds a field to a published format and makes a repository
whose node is unpublished unmintable, for a name the deployment already publishes.*

### 4.2 The permission set is chosen from the verified identity

The mint hard-codes the run's set (`github.RunTokenPermissions()`, `github.go:239`;
`internal/gitprov/github/apptoken.go:91`). The review caller is served **R1's read-only set**
(`docs/cloud-senior-review/implementation-plan.md` R1: `{contents: read, pull_requests: read, metadata:
read}`) — a **fifth** set, not a reuse: the package's four (`:91` the run's, `:97` an image build's, `:111` a
challenger arm's, `:128` the coordinator's) each read something else, and R1's combination is the one a
review needs. The choice is made from the verified side and nothing else:

| caller | run identity | set served |
| --- | --- | --- |
| review class | no `fb`, non-empty `fpr` | **the read-only set** (R1's) |
| review class | any other shape | **refused** — never the run set |
| repository class | any | the run set, unchanged (`RunTokenPermissions`) |

R1's set is **another work stream's symbol**, and it must be **a set of its own** rather than one composed
out of the four — the reason `shadowTokenPermissions`'s own comment gives for its own duplicate: a later
change to one set must not silently widen the other. This design serves it and declares no second spelling of
the permissions. If R1 has not landed, the review mint cannot land either — the dependency is recorded in
[implementation-plan.md](implementation-plan.md) §"Dependencies" and is a precondition, not a task here.

### 4.3 Lifetime, refresh, and what is never mounted

Unchanged from the mint the service already answers: GitHub's own installation token, **one hour**,
minted on demand and refreshed by the `Token(ctx, minValid)` contract the in-job
`github.ServiceTokens` source already holds (`internal/gitprov/github/apptoken.go:381` is the type, `:438`
its `Token`); it lives in the
review container's memory for the run's length and is **never written down, never mounted and never handed
in at launch** — D893 (c)'s whole point. The review variant carries the service's URL in its environment
and nothing else about it (`docs/cloud-senior-review/` R7).

*The alternative — a shorter service-side cap — is not available: GitHub mints no shorter token, so a
shorter lifetime would mean more refreshes and more audit lines for a lifetime nothing can shorten
(D1066).*

### 4.4 What the review mint refuses, in order

The refusals are the existing ones plus §3's, and the order matters because it is the order the
service's "no credential is read until the caller is admitted" property is stated in
(`ServeHTTP`, `server.go:440`): unverified identities → class not admitted to this route → the stop flag
(`Kills.Stop`) → the pairing and the claim rule of §3 → the bundle → the mint itself. The **bundle is not
read** and the **App key is not touched** for a call refused before it, which is the property the audit
line's purpose and credential fields already record (`routeAct`, `server.go:59`).

---

## 5. The sign and verify endpoints

### 5.1 Two routes, and the property the third-route refusal already has

`ServeHTTP` refuses every path but `MessagesPath`, `CountTokensPath` and `MintPath`
(`server.go:33`), and its comment is explicit that this is how D143's "no admin endpoint" is kept — by
there being nothing to reach rather than by a check inside a handler. Two routes join that set:
**`SignPath = "/v1/review/sign"`** and **`VerifyPath = "/v1/review/verify"`**, both `POST`. The
`TestServiceHasNoAdminRoutes` guard is extended to name the five, so a sixth route is a test failure and
not a discovery.

### 5.2 The sign route

**Request** (strict decode, `DisallowUnknownFields`, bounded by `maxMintRequestBytes`' 4 KiB,
`github.go:92`): `{"digest": "<64 lowercase hex>"}` — SHA-256 over `review.json`'s exact bytes, computed
by the artifact writer (`docs/cloud-senior-review/` R3). The **digest is not the object**: the reviewer's
prose never leaves the run (D892).

**Admission**, in the order `ServeHTTP` already makes its decisions (§4.4): the review class (§2.1); the
stop flag; the pairing (§3.2); the review claim rule (§3.3); the run live, already checked by `verifyRun`
(§3.4).

**Answer**: `{"alg":"HMAC-SHA-256","signature":"<64 lowercase hex>"}`, the HMAC-SHA-256 of the digest's 32
bytes under **that account's review-signing key** (§6), rendered lowercase hex. The algorithm is named in
the answer so that neither end can quietly accept a weaker construction (D892), and the service refuses a
request that names an algorithm of its own: the request has no algorithm field at all.

**What the service does not do at sign time, said plainly**: it does not read the artifact, does not see
the reviewer's prose, does not check the pull request or the head, and does not know what the coordinator
launched. What the signature therefore binds is **the exact bytes, under the key of one account**; which
repository, pull request, head and review run those bytes belong to is inside the bytes and is checked by
the verifier against what it launched (D892's four checks, shipped by the review work's R3). The service's
contribution to that binding is its **audit line**, which names the account, the repository slug, the run
and the pull requests from the verified identity — so a signature can be traced to the run that asked for
it even though the signature itself says only "this account". **The residual is recorded at its true size
in §7.**

### 5.3 The verify route

**Request**: `{"account": "<the review's account name>", "digest": "...", "signature": "..."}` — the same
64-hex shapes, the account name held to the bundle's credential-name rule.

**Answer**: `200 {"valid": true}` when the signature equals the HMAC the service recomputes, and
`200 {"valid": false}` when it does not — **one refusal bit and nothing else**, no reason, no key id, no
account echo, and no distinguisher between "wrong key" and "wrong bytes". Every case where the service
cannot answer the question at all is a **typed refusal** instead (`state_unavailable` for an unreadable
key, `unverified_job_identity`/`unverified_run_identity` for the caller, `request_refused` for a body this
contract does not read), so a caller can always separate "the artifact is not this account's" from "your
call was bad" — which a bare `valid:false` would conflate.

Why an endpoint at all, given the coordinator could hold the key: **because a second holder is a second
place it can leak from** (D892). The verify call exists so the coordinator never holds a key that forges
every verdict of an account.

Why the account is named by the caller: the caller is the component that launched the review and knows the
account (D882), and naming it selects **which key to recompute under** — it cannot make an invalid
signature valid. *The alternative — trying every admitted account's key — is recorded in
[D1068](reconciliation.md) §3: the service would have to answer which key matched, and a signature valid
under another account of the same installation would then verify.*

### 5.4 The caller class for verify, which the service does not have today

The coordinator is not a run: it holds no run identity, so the verify route cannot be authenticated the
way the other routes are. It gets its own class, and it is **configuration, not code**:

- the request must carry a **Google ID token** whose `email` claim is in the service's configured
  **verifier list** (`AuthConfig.VerifierAccounts`), verified by the same code path `verifyJob` uses —
  audience, issuer, `email_verified`, expiry, not-issued-in-the-future, ASCII fold — with **one**
  difference, stated rather than implied: the shared-job gate does not apply to a verifier, because a
  verifier is not a job of this installation's job set at all;
- the list is **empty by default**, and an empty list means the route answers every call with the caller
  class's refusal — an installation with no coordinator does not carry a route anybody can reach;
- the coordinator's own account (`fugaro-coordinator`, `docs/coordinator/`) is added to the list by
  **that** work stream's deployment when it lands, which is why this plan adds no coordinator task;
- the owner's own identity may be added for a manual verification; the design says so because the live
  check uses it (§8), and records what it costs in §7.

### 5.5 What the audit line says

C1.13's audit line carries a closed field set and a closed purpose vocabulary (`internal/credsvc/audit.go`:
`PurposeForward` `:90`, `PurposeProbe` `:95`, `PurposeMint` `:104`, `ValidPurpose` `:107`). Two purposes
join it — **`review_sign`** and **`review_verify`** — and `routeAct` (`server.go:59`) gains the two
routes, so a refusal is recorded as the act the run asked for and not as the branch it was answered in.
The credential a sign line names is the **review-signing key's bundle entry name** (§6), the way a mint's
line names the App key; a verify line names the same key, because that is the credential the answer was
computed with, and its issuer is the service itself rather than the caller.

The line carries the account, the repository slug, the run and (for a sign) the identity's pull requests —
all of them from the **verified** side — and **never the digest**: the digest is not a secret, but a field
that is a caller's bytes is exactly the free-form text this file exists to keep out, and a signature line
that carried it would invite one to carry the signature.

---

## 6. The key

### 6.1 It lives in the one bundle document, one entry per account

**This is the design's one deliberate departure from D892's wording, and the reason is a constraint D892
predates.** D892 says the key is "a Secret Manager secret, one per account, read by the service's own
service account and by nothing else". **Q6** (the owner's ruling of 2026-10-19) pins the service's entire
reach in Secret Manager to **ONE container and ONE accessor grant** — Global Constraint 12 — and
`TestCredentialsServiceGrantsAreTheOneBundle` (`deploy/terraform/terraform_test.go:3984`) refuses a second
`google_secret_manager_secret`, a second `google_secret_manager_secret_iam_member`, and any `condition`
on the one grant.

So the key lives **in the bundle**, as one document entry per account:

- the entry's name is derived like every other credential's (`config.ClaudeAccountSecret(name)` family;
  `internal/config`), with a review-signing suffix, and it is **one entry per account** — the property
  D892 asks for is kept, and what is given up is only the container-per-account spelling;
- the entry joins the loader's **closed key set** (`CredentialKey`, `internal/credsvc/bundle.go:441`;
  `ParseBundle`, `:514`; `schemas/creds-bundle.schema.json`) and the `creds ls` rendering, in the same
  change — a bundle key outside the set is refused by the loader today, so a key the schema does not know
  is a key the service cannot read;
- the entry's value is `Kind`-checked like the App key's (`KindGitHubApp`, `bundle.go:441`): a review
  signing key is a signing key and not a model credential, so no cascade ever sees it
  (`Bundle.Credentials()` stays what it is);
- **the reach is unchanged**: the one grant, the one container, and the service's own account as its only
  reader. Nothing else in the installation can read it — the review variant, the shared project job and
  the coder's job all hold no accessor on the bundle
  (`internal/cli/init_credentials.go:21`), and the coordinator does not either, which is why it calls the
  verify route.

### 6.2 Who generates the bytes, and why nobody ever sees them

An HMAC key has no upstream to paste from, unlike the App key. The bytes are therefore **generated by the
CLI** at the owner's terminal, written straight into the bundle's next version, and **never printed,
logged or shown** — the same lost-update-safe write path `fugaro creds set` already uses for
`github-app-key` (`internal/cli/creds.go`: `bundleStore.add` `:346`, `setBundleEntry` `:869`; a value
never in Terraform, `TestNoSecretVersionsInTerraform`). One command per account, one entry per account,
and a second run of it **rotates** rather than duplicates.

*The alternative — a provisioned key generated by the apply — is [D1071](reconciliation.md) §3's: it puts
key material in a stage that has no writer for document values and that an installation already opted in
never re-runs, so the key would exist on new installations and not on old ones.*

### 6.3 Rotation, and the window

D892's rotation shape, expressed in the bundle's terms:

- the service **signs with the current entry**;
- it **verifies against the current entry and the previous one**, and against nothing older: a retiring
  key is carried as a second entry (`…-previous`) that the rotation writes beside the new one, and the
  window ends when a later `fugaro creds set` writes a document without it;
- an artifact signed under a retired key **stops verifying**, and the honest remedy is **re-reviewing the
  head** — there is no escrow and no re-signing (D892);
- the bundle's short cache is what makes a rotation take effect promptly, and the loader's rule is what
  makes it safe: **a document that fails to parse is never cached and never serves the previous one**
  (`internal/credsvc/bundle.go`, the loader's own comment), so a botched rotation is a service that
  refuses to sign rather than one that signs under a key the operator believes they retired.

### 6.4 Fail closed, always

A missing entry, an entry that is not a usable key, an unreadable bundle: the sign route **refuses**
(`state_unavailable` / the bundle's own typed refusal) and the verify route **refuses** — never
`{"valid":false}`, which would be a service that says "this verdict is not mine" when the truth is "I
cannot tell". The distinction is the one §5.3 draws, and it is the difference between a coordinator that
re-reviews and one that quietly drops a verdict it could not check.

---

## 7. The threat model

**Assets.** The App key (never on the review variant, never in its container); the account's Claude token
(mounted on the variant, unchanged by this slice); the per-account review-signing key (the service's
alone); the minted installation token (in-job memory, one hour, one repository).

**Adversaries, and what each gets.**

| Adversary | What it holds today | What this slice does |
| --- | --- | --- |
| the **review run itself** — a container that runs a test from the head under review by design (D885) | its account's Claude token, the read-only token it fetches | **closed as far as write access goes**: it can obtain only the read-only set, and only for the repository its own identity names. It **cannot** obtain a write token: the mint chooses the set from the verified claims (§4.2), and a coding run's identity is refused |
| **another run on the same project job set** — the D892 gap: it holds `objectUser` on `runs/`, can list, and can write under a prefix it can see | a shared project job's account | **the verdict is signed, and it holds no key**: a forged or replayed `review.json` has no signature to carry, and the key is not in any container |
| a caller that can **reach the service's URL** but holds no admitted identity | nothing | refused before a credential is read, as today |
| the **launcher** | minting authority for run identities, and a share of the installation's own IAM | **unchanged, and stated**: a launcher that can mint an identity for any repository, and deploy a job with an admitted account name, can obtain a read-only token for that repository and sign as that account. This is the same launcher trust the credentials-service plan already records (F29, SECURITY.md) and this slice neither widens nor closes it |
| the **owner's own identity**, if it is added to the verifier list (§5.4) | a Google identity admitted by configuration | it can ask "is this digest valid under this account" and learns one bit. It holds no key and can forge nothing. The list is configuration and empty by default; the live check is why it is stated |

**Residuals, at their true size.**

- **A leaked signing key forges every verdict of that account until rotation.** That is the price of the
  one-holder design and the reason the key stays out of the container that reads an untrusted diff
  (D892). No escrow: an artifact signed under a retired key stops verifying.
- **The signature binds bytes, not run or repository.** With the digest alone MAC'd (D892's answered
  algorithm), a signature says "an admitted review run of account A asked for a signature over these
  bytes" — the *which* run and *which* repository come from the artifact's own fields, checked by the
  coordinator against what it launched. The service's audit line is what makes the binding *findable* after
  the fact. A stronger construction, MAC'ing a framed message that includes the verified run and
  repository, is recorded as an open item (X3, [reconciliation.md](reconciliation.md) §4) rather than
  invented here, because D892 answers the algorithm and this design does not re-decide an answered
  question.
- **A dishonest reviewer inside its own run is not stopped by any signature.** An injected reviewer can
  write a dishonest verdict and have it signed; the signature stops *another job* forging one. The verdict
  stays advice, CI still runs, and the owner's own look is still the last step (§7 of
  `docs/cloud-senior-review/`, unchanged).
- **A review run that ends before signing leaves an unsigned artifact**, and the remedy is re-reviewing
  (§3.4). A job killed between the artifact write and the sign is exactly that case, and it is refused
  rather than repaired.
- **The read-only token is not read-only for the *repository's* private data**: `contents: read` reads the
  whole repository the identity names, not only the pull request's diff. It is the repository under
  review, which the run's own worktree already holds, so this adds nothing a review run did not already
  have; it is stated because "read-only" is a permission word and not a scope word.
- **Nothing here makes the review job's model call safe.** That call is R7's wiring (the mounted token);
  this slice is about the forge credential and the verdict's provenance.

---

## 8. The owner-run live check

**Named here, numbered at implementation** ([README.md](README.md) §"Numbers this work holds"): the
register's highest written section is Check 190 and open pull requests claim above it, so the task that
writes the check takes the number free at its merge. **It is owner-run: no agent performs any step of
it**, and it is **NOT RUN** until the tasks below ship and the review work's R7 and R2 are on `main`.

Installation `fugaro`, sandbox checkout only, never `belong`; a sandbox repository (`owner/repo-a`) and a
sandbox pull request.

1. **The two facts `fugaro doctor` reports** (R6's): the credentials service is enabled for this
   installation, and the review-only variant's account is admitted. A `warning` line instead is the
   finding, and the check stops there.
2. **The variant holds no key**: `gcloud run jobs describe <the review job>` shows exactly one secret —
   the account's Claude token, at `latest`, never as a plain value — and the variant's service account's
   IAM shows **no** `secretAccessor` on the App key, on the bundle, or on anything else.
3. **A review launches and runs**: `fugaro review --pr <N>` on the sandbox completes, and its log names
   **no** token value (the run's own evidence, read by eye).
4. **The token is read-only, and it is one repository's**: a throwaway execution of the variant mints a
   token for the reviewed repository and a **push** with it is refused by GitHub (403); a second
   hand-made call naming a *different* repository the account is admitted for is refused by the
   **service** — the repository is not the caller's to name.
5. **The verdict is signed**: `runs/<slug>/<review-run>/artifacts/review.json.sig` exists, is one line of
   lowercase hex, and the coordinator's verify path (or a hand-made verify call, step 7) answers valid
   for it.
6. **A tampered artifact does not verify**: one byte changed in a copy of `review.json` (never the stored
   object) and the signature no longer verifies; a copy of a valid signature pasted under a *different*
   review run is refused by the verifier's four checks; an artifact with **no** sidecar is refused.
7. **The verify route's caller class**: with the owner's own identity in the verifier list, a hand-made
   `POST /v1/review/verify` with the owner's ID token answers `{"valid":true}` for the real pair and
   `{"valid":false}` for the tampered one; the same call **without** a token is a typed refusal; the
   service's log shows the line naming the account, the repository and the run.
8. **The gate still holds where it was written**: the same call from a *coding* shared project job's
   account is refused, and so is the review account on `/v1/messages` — the refusal's own sentence is what
   the owner reads. Then the check's own cost: the review run's spend, the artifact kept, and nothing
   created, enabled or deleted outside the sandbox.

---

## 9. What `fugaro update` does, which is everything that is an upgrade

The HARD RULE is that `fugaro update` is the whole upgrade and that a release ships no operator step that
is *another command*. This slice's reading of it, stated because a reviewer must be able to check it:

- **No Terraform, no new container, no new grant.** The key is a bundle entry (§6.1), so the installation
  root's plan is unchanged and an installation that has never opted in is byte-for-byte untouched by this
  work: the review class is empty when no policy publishes a profile, the routes answer their refusals, and
  `TestReviewAccountsAreTheNamesProjectJobsDeploys` holds an installation with no review job to an empty
  set.
- **An installation already opted in converges with an ordinary `fugaro update`** for everything this
  slice adds to *code*: the classes, the routes, the refusals, the audit purposes. The service's image is
  re-pinned by `fugaro update` already.
- **The key's bytes are the service's own documented step**, exactly as `github-app-key` is (D893's own
  words: "both of which are the credentials service's own documented steps, not new ones this design
  adds"). It is not an upgrade step: nothing breaks without it, the service keeps answering everything it
  answered before, and a sign or verify call answers a **named** refusal the operator can act on. What the
  design does owe is that the *name* is reachable: `fugaro doctor` reports it beside the two facts R6
  already reports, and the service's refusal says which entry is missing.
- **The review job cannot be launched before the key exists**, because R6's doctor check and R2's launcher
  refuse the launch rather than shipping one the service refuses. That is the same posture E7's other half
  has, and it is why no user meets a half-configured review.
- **A version gate, not a silent ignore**: this slice adds no `fugaro.yaml`, task or wire *field*, so
  `internal/cli/recipes_skew.go`'s `…Since` gates are not needed for it — the two routes are new paths and
  an older job image never calls them. Where the review work adds fields, E6 is that work's rule.

---

## 10. What this slice does not do

- **Shared jobs for coding runs (the general C3.9).** The coding shared project jobs stay refused on every
  route, with today's sentence and today's test. **Task 3.4a's refusal** of an OAuth launch onto a shared
  job stays lifted-at-C3.9 and is **not touched**.
- **The tier-A registry and the reference record of `docs/design/project-centric.md` §7.** D131 and O4
  name them as what the shared-job work grows into. This slice reads **O4's published admissions** — which
  is the source the model path already uses — and builds no registry.
- **The model-credential move for shared jobs.** `--provision-credentials` still does not reach
  `modules/project`; the shared job keeps its provider keys and its `CLAUDE_CODE_OAUTH_TOKEN`
  (`internal/cli/init_credentials.go:21`), and the review variant mounts its own.
- **A model route for the review class.** The review job's model call is its mounted token. Admitting the
  review class to `/v1/messages` would mean serving a *tier* and an *admission* to a job with no
  repository of its own — the exact widening Global Constraint 6 exists to refuse.
- **The coordinator and its merge path.** The verify endpoint is built and configured; the component that
  calls it, and the `ship` → merge path that acts on a verified verdict, is the coordinator's own work
  stream (`docs/coordinator/`), and it stays blocked on this slice only in the sense that it now has a
  route to call.
- **The reviewer's own artifact format, its four replay checks, and the mutation check.** R3 and R4 of
  `docs/cloud-senior-review/`.
- **Any per-run cap or lease for a review.** The service's per-run cap is advisory (D143); nothing here
  adds a second budget.
- **A `fugaro review` command.** R2's, and gated on this slice rather than part of it.
