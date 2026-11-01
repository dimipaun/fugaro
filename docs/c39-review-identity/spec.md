# The review identity, the read-only mint and the signed verdict

This is the design. Where it and [reconciliation.md](reconciliation.md) disagree, **the reconciliation
wins**. The owner decisions it is written to are **D1062–D1074**, each with the cost of its alternative,
in [reconciliation.md](reconciliation.md) §3; the owner answered them all on **2026-11-01** — D1062–D1071
as recommended, D1072 as 0.16.0, D1063 as amended by the same day's ruling **O1**, and D1073/D1074 as the
review added them.

---

## 1. The slice, and the boundary it keeps

**The problem, in the service's own terms.** `Authenticator.Verify` (`internal/credsvc/auth.go:367`) checks
two identities on every call: the calling job's Google ID token, whose `email` claim
`verifyJob` (`:423`) matches against the installation's job accounts, and the run's Firebase identity,
checked by `verifyRun` (`:516`). One rule is decided from the verified email and never from the caller's
header: **Global Constraint 6**'s shared-job gate (`:469`), which refuses every account in
`Authenticator.sharedAccounts` — `SharedJobAccountsFor`'s derivation over the published policy
(`internal/credsvc/server.go:154`). The review variant is one of those accounts the moment
`internal/infra/projectjobs.go` deploys it, so **today the service refuses the review job whole**, and the
mint `Server.mint` (`internal/credsvc/github.go:239`) mints from `ja.Repo`, **the one repository a
per-repository job's account names** — a name a per-`(profile, account)` job does not have.

**What the slice adds is three things and no more:**

1. a **third account class** — the review-only job variant, per `(profile, account)`, admitted as such
   (§2, §3);
2. a mint that serves **CSR1's read-only permission set** for a review caller, scoped to the repository the
   **run identity** names, within what the account is admitted for (§4);
3. the **sign and verify endpoints** for the verdict digest, with a per-account key the service alone
   holds (§5, §6).

**The boundary is the whole design.** Global Constraint 6 is not lifted; it is **narrowed to the jobs it
was written for**. Its reason is that a shared project job's account (i) mounts the provider keys and the
App key with live accessors, (ii) reaches the whole `runs/` prefix, and (iii) serves many repositories at
once — so admitting it to the service's **model** path would put a credential of a container that runs
untrusted code behind one shared account. The review variant is a **different job**, and the ways it
differs are exactly what the class buys: it mounts **no** model credential and no App key, no signing key
and no accessor on either (`docs/cloud-senior-review/implementation-plan.md` CSR7), and it is admitted on
**three routes** — the mint, the sign, and the model path **for its own account** (§2.3, §4.2) — while it
is refused the verify route and refused as a *coding* job would be. A *coding* run in a shared project job
is refused on every route, before and after this lands (D1062, D1063).

**The model path is part of the class, and that is the owner's ruling O1 of 2026-11-01.** The reviewer's
model calls route **through the service** rather than around it, so the review container holds no
`CLAUDE_CODE_OAUTH_TOKEN` and §2.3's "reads no model credential" is not this design's shape. What keeps
Global Constraint 6's *reason* intact is the pairing: a review caller is served **tier A only** — the one
account §3.2 resolves, and only for a repository that account is admitted to — so a review job cannot reach
a credential of a container that is not its own, which is the exposure the constraint exists to refuse. A
review job also **ignores the published `routing.claude` switch** (§5.2): whatever that switch says, a
review job's Claude calls go to the service, because the service is the only path its container has. If
C3.9 slips, the fallback the ruling names is CSR7's token mount, and the class goes back to two routes.

---

## 2. The account classes

### 2.1 Three classes, decided from the verified email

`verifyJob` today returns one thing: the configured spelling of the calling account, or a refusal. It
gains a **class** — the same answer the rest of the call branches on:

| Class | Who | Where it comes from | What it is admitted to |
| --- | --- | --- | --- |
| **repository** | a workflow job of one repository (`gcp.ServiceAccountID(slug, workflow)`) | `JobAccountsFor` (`server.go:110`), which binds email → `{Slug, Repo}` | everything the service does today: the model routes, the mint (the run set), the stop flag |
| **shared** | a *coding* shared project job (`gcp.ProjectServiceAccountID(profile, provider, account)` over `config.ProjectJobAccounts`) | `SharedJobAccountsFor` (`:154`) | **nothing** — refused whole, exactly as today |
| **review** | the review-only variant, per `(profile, account)` | new, `ReviewJobAccountsFor` (§2.2) | the **mint** (the read-only set), the **sign**, and the **model path** (`/v1/messages` and its `count_tokens` sibling) **for its own account only** — tier A, resolved by the pairing, and never the cascade's other tiers. Refused the verify route |

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
service-account ID spelling is **CSR7's**, not this design's (`docs/cloud-senior-review/` CSR7: "same axis,
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

The class buys three routes and no privileges beyond them. In particular a review account:

- reads **no other account's model credential**: on the model path the pairing (§3.2) resolves the one
  account the call is served by, that account's credential is the only tier A candidate, and the cascade's
  other tiers are **never** offered to it — a request cannot name an account (§5.2), a caller that fails the
  pairing is refused before any credential is chosen, and a review caller is admitted on `/v1/messages` only
  as the account the pairing resolved;
- reads **no key of its own and no token of its own**: no App key, no signing key, no Secret Manager
  accessor, and — under the owner's ruling O1 — no model token either, because its Claude calls go to the
  service. The signing key stays in the service and nowhere else (§6), and the review container's only
  credential is the read-only installation token it fetches per run (§4);
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
token": a run identity is verified on its own, and nothing in the two verifications pairs them — which is
the same gap the shared jobs have, and the reason the class of caller is decided from the verified email in
the first place.

**The pairing for a review caller is the account's admission**, read from what the launcher already
publishes (O4, `docs/credentials-service/implementation-plan.md`):

1. the repository is the run identity's `fs` slug — the only repository the call can be about, because it
   is the one the *launcher* minted into the token;
2. the slug is resolved to a canonical repository through the service's **repository table**
   (§4.1), and an unknown slug is refused;
3. the account the call is served as (the profile triple of the **review** class row) must appear in that
   repository's published admissions — and the admission is **the model path's own**, not a second spelling
   of it: `admittedAccounts(ctx, slug, canonical)` (`internal/credsvc/cascade.go:87`), the O4 check the
   cascade already makes when it offers a tier-A credential, is the pairing's third step. It is an
   **intersection of three published facts** and not a read of one: the repository's own child node names the
   accounts an administrator admitted it to (`PolicyReader.Admissions(ctx, slug)` →
   `projectpolicy.RepoPolicy`'s `credentials.accounts`); the shared node defines the account
   (`shared.accounts.<name>`); and **both** the account's `allow_repos` and the project's own `allow_repos`
   admit the repository. Taking the whole intersection rather than the child node alone is what closes the
   `project repos rm` gap: that command removes the repository from the project's allow-list and does not
   touch its child, so a pairing that read `credentials.accounts` by itself would keep admitting a
   repository the project has withdrawn. **An absent child is an empty admission and never an error**, and
   an unreadable child or node is a refusal and not an empty set — fail closed, the rule the model path
   already follows for a project with no allow-list;
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
(`docs/cloud-senior-review/` CSR3), and the honest remedy is re-reviewing the head, not re-signing — a
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
`internal/gitprov/github/apptoken.go:152`). The review caller is served **CSR1's read-only set**
(`docs/cloud-senior-review/implementation-plan.md` CSR1: `{contents: read, pull_requests: read, metadata:
read}`) — a **fifth** set, not a reuse: the package's four (`:91` the run's, `:97` an image build's, `:111` a
challenger arm's, `:128` the coordinator's) each read something else, and CSR1's combination is the one a
review needs. The choice is made from the verified side and nothing else:

| caller | run identity | set served |
| --- | --- | --- |
| review class | no `fb`, non-empty `fpr` | **the read-only set** (CSR1's) |
| review class | any other shape | **refused** — never the run set |
| repository class | any | the run set, unchanged (`RunTokenPermissions`) |

**The class is the account's, and the claim shape never moves a call between classes.** The row a call
takes is decided by the pairing (§3.2) — which account serves it — so a **repository-class** caller is
served the run set whatever its claims say, **including an identity shaped the review way** (non-empty
`fpr`, no `fb`): it is served the run set and it is **not** the review class, and the shape alone could
not decide it either, because a coding run's own identity legitimately carries both claims (`fb` and `fpr`
are written when the run has them, `internal/budget/token/mint.go:57`, `:66`, `:69`). That is today's
behaviour unchanged — the account class is what decides, and nothing here reads the claims on a
repository-class call. §3.3's rule is an admission rule **inside** the review class: it refuses a
review-class call whose claims fail it and never **downgrades** one to the run set, which is the direction
that would lose the property the class exists for. C3.9b names
`TestRepositoryMintKeepsTheRunSetForAReviewShapedIdentity` for the anomalous shape, beside
`TestReviewMintRefusesARunIdentityWithABranch` and `TestReviewMintRefusesARunIdentityWithNoPullRequests`
for the refusals.

CSR1's set is **another work stream's symbol**, and it must be **a set of its own** rather than one composed
out of the four — the reason `shadowTokenPermissions`'s own comment gives for its own duplicate: a later
change to one set must not silently widen the other. This design serves it and declares no second spelling of
the permissions. If CSR1 has not landed, the review mint cannot land either — the dependency is recorded in
[implementation-plan.md](implementation-plan.md) §"Dependencies" and is a precondition, not a task here.

### 4.3 Lifetime, refresh, and what is never mounted

Unchanged from the mint the service already answers: GitHub's own installation token, **one hour**,
minted on demand and refreshed by the `Token(ctx, minValid)` contract the in-job
`github.ServiceTokens` source already holds (`internal/gitprov/github/apptoken.go:381` is the type, `:438`
its `Token`); it lives in the
review container's memory for the run's length and is **never written down, never mounted and never handed
in at launch** — D893 (c)'s whole point. The review variant carries the service's URL in its environment
and nothing else about it (`docs/cloud-senior-review/` CSR7).

*The alternative — a shorter service-side cap — is not available: GitHub mints no shorter token, so a
shorter lifetime would mean more refreshes and more audit lines for a lifetime nothing can shorten
(D1066).*

### 4.4 What the review mint refuses, in order

The refusals are the existing ones plus §3's, and the order matters because it is the order the
service's "no credential is read until the caller is admitted" property is stated in
(`ServeHTTP`, `server.go:440`): unverified identities (`RequestFromHTTP` `:450`, `Verify` `:457`) → the
class not admitted to this route → **the pairing and the claim rule of §3** (the binding check,
`ja, bound := s.jobs[id.JobAccount]` `:476`, whose shape the class decides) → **the stop flag**
(`Kills.Stop` `:492`) → the bundle → the mint itself. **The pairing comes before the stop flag because that
is what the code does**, and stating it the other way round was a mistake of this document's first draft: a
caller whose two identities are not paired is refused before the operator's switch is read, and either
refusal is recorded as the act the route asked for (`routeAct`, `server.go:59`). The **bundle is not read**
and the **App key is not touched** for a call refused before it, which is the property the audit line's
purpose and credential fields already record.

---

## 5. The sign and verify endpoints

### 5.1 Two routes, and the property the third-route refusal already has

`ServeHTTP` refuses every path but `MessagesPath`, `CountTokensPath` and `MintPath` (`server.go:442`, the
paths spelled at `:33`), and its comment is explicit that this is how D143's "no admin endpoint" is kept —
by there being nothing to reach rather than by a check inside a handler. Two routes join that set:
**`SignPath = "/v1/review/sign"`** and **`VerifyPath = "/v1/review/verify"`**, both `POST`. The
`TestServiceHasNoAdminRoutes` guard (`internal/credsvc/forward_test.go:341`) drives a list of method-and-path
pairs that must all be refused — three of them the served paths themselves, with a wrong method or a
trailing slash — and the two new routes join that list, so a sixth route is a test failure and not a
discovery.

### 5.2 The sign route

**Request** (strict decode, `DisallowUnknownFields`, bounded by `maxMintRequestBytes`' 4 KiB,
`github.go:92`): `{"digest": "<64 lowercase hex>"}` — SHA-256 over `review.json`'s exact bytes, computed
by the artifact writer (`docs/cloud-senior-review/` CSR3). The **digest is not the object**: the reviewer's
prose never leaves the run (D892).

**Admission**, in the order `ServeHTTP` already makes its decisions (§4.4): the review class (§2.1); the
pairing (§3.2) and the review claim rule (§3.3); the stop flag; the run live, already checked by `verifyRun`
(§3.4); the account's **key**, read before anything is written and **failing closed** — a missing entry, an
entry that is not a usable key, an unreadable bundle is `state_unavailable` or the bundle's own typed
refusal, and never a signature under something else (§6.4); and, last, the mark of §5.6, which is what makes
the answer the run's first and only signature. **The key comes before the mark on purpose**: a run whose key
does not exist yet is refused with nothing written, so the operator's `creds set` and a re-run sign the head,
where a mark taken and then found unanswerable would be an unsigned review and a re-review. C3.9e's
`TestSignRouteRefusesAReviewAccountWithNoKeyYet` asserts both halves of that order — the typed refusal and
**no mark written**.

**The message that is signed is framed, not the bare digest (D1073, the owner's answer of 2026-11-01)**:

```
v1\n<repository slug>\n<review run>\n<digest>
```

— three newline-separated fields after the version tag, where the slug and the run are the **verified**
identity's own `fs` and `fr` (§3.1) and the digest is the request's, lowercase hex. Every field is
length- and charset-bounded before the message is built (the slug and the run by `token.ValidSegment`, the
rule the identity itself was read with at `auth.go:219`, and the digest by its 64-hex shape), so two
different triples cannot frame to the same bytes — a framing that could
be padded into ambiguity would be worse than no framing at all. The **request does not name the slug or the
run**: a request field the caller could write is the one thing the frame exists to exclude, so the two
fields come from the identity the service verified and from nowhere else. The frame is why **a signature
made for one review run of one repository cannot be presented as another's**, and it is what makes the
verify route able to refuse a cross-run replay by itself rather than leaving that to the coordinator's four
checks (§5.3).

**Answer**: `{"alg":"HMAC-SHA-256","signature":"<64 lowercase hex>"}`, the HMAC-SHA-256 of the framed
message's bytes under **that account's review-signing key** (§6), rendered lowercase hex. The algorithm is
named in the answer so that neither end can quietly accept a weaker construction (D892), and the service
refuses a request that names an algorithm of its own: the request has no algorithm field at all.

**What the service does not do at sign time, said plainly**: it does not read the artifact, does not see
the reviewer's prose, does not check the pull request or the head, and does not know what the coordinator
launched. What the signature therefore binds is **the exact bytes, for one run of one repository, under the
key of one account**; which pull request and which head those bytes belong to is inside the bytes and is
checked by the verifier against what it launched (D892's four checks, shipped by the review work's CSR3). The
service's contribution to the rest is its **audit line**, which names the account, the repository slug and
the run from the verified identity — **not the identity's pull requests**, because C3.9a's `AuditEvent` has
no field for them and adding one is scope this design does not take — so a signature can be traced to the
run that asked for it. **What used to be a residual here is closed by D1073**: the signature now says
"this run of this repository" and not only "this account", and §7 states what is left at its true size.

**A review job ignores the published `routing.claude` switch** (the owner's ruling O1): that switch decides
whether a run's Claude calls are sent to the service, and a review job has no other path — its container
holds no token — so the switch has nothing to decide for it. What the review job's model call needs from the
service instead is §2.3's admission: the pairing, and the account's own tier A credential.

### 5.3 The verify route

**Request**: `{"account": "<the review's account name>", "digest": "...", "signature": "...", "run": "...",
"repository": "..."}` — the 64-hex shapes as at sign time, the account name held to the bundle's
credential-name rule, and the last two held to `token.ValidSegment`. **The run and the repository are in the
request because the frame is recomputed** (D1073): a verifier holds no run identity, so the two fields the
signature was made over have to be named — and naming them cannot make a signature valid that was not made
for them, because the HMAC is taken over the frame those fields build: a caller that names the wrong run gets
`valid:false` and not a match.

**Answer**: `200 {"valid": true}` when the signature equals the HMAC the service recomputes **over the
frame**, and `200 {"valid": false}` when it does not — **one refusal bit and nothing else**, no reason, no
key id, no account echo, and no distinguisher between "wrong key" and "wrong bytes". Every case where the
service cannot answer the question at all is a **typed refusal** instead (`state_unavailable` for an
unreadable key, `unverified_job_identity`/`unverified_run_identity` for the caller, `request_refused` for a
body this contract does not read), so a caller can always separate "the artifact is not this account's" from
"your call was bad" — which a bare `valid:false` would conflate. **An `account` the caller may not ask
about is one of those typed refusals and never `valid:false`**: a name that is not a legal bundle
credential name is `request_refused`, and a name this installation holds no review-signing entry for is
the unreadable-key case (`state_unavailable`). A caller that asks about a nonexistent account therefore
learns that **its call was bad**, not that the artifact is not that account's, and `valid:false` keeps its
one meaning — the service recomputed the HMAC and it differs. C3.9f's
`TestVerifyRouteRefusesAnAccountTheCallerMayNotAskAbout` is the test that holds it.

**The coordinator's rule is the one this distinction exists for, and it is stated here in one sentence:
nothing but `{"valid":true}` ships.** A `valid:false` and every typed refusal mean the same thing to the
merge path — *not ship* — and a refusal is never read as a reason to re-review on its own.

**The clock is the caller's, and the tolerance is not this route's to invent**: a verify request carries no
timestamp, so there is no window to widen here, and what applies is what the service already checks on the
two tokens the call presents. `verifyJob` (`auth.go:423`) checks expiry (`:482`), an `iat` not in the future
(`:489`) and a `nbf` not in the future (`:492`), the last two against the one shared `clockSkew` the package
declares (`auth.go:123`). `verifyRun` (`auth.go:516`) checks expiry (`:544`) and an `iat` not in the future
(`:547`) — **two** of the three, and no `nbf`, because a Firebase ID token carries none: that claim belongs
to the Google-issued job token, and `verifyJob` is the rule that reads it. Inventing a third check for the
run identity would be a rule this design did not need to add. **`checkLive` (`auth.go:665`)
has no tolerance of its own and reads no token's window**: it reads the run's write-once outcome, so the
liveness rule is a fact about the run and not a clock comparison. **This design adds no third tolerance**,
and the verifier's own token is held to the same three rules rather than to a second spelling of them.

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
way the other routes are. It gets its own class, which is **configuration plus one small piece of code** —
the list is what an operator writes, and the path that reads it is new:

- **the route is parsed ahead of `RequestFromHTTP`.** `RequestFromHTTP` (`wire.go:842`) reads the two
  identity headers, and `Authenticator.Verify` (`auth.go:367`, reached from `server.go:457`) refuses a
  request whose run identity is missing or unverified — while `ServeHTTP` refuses an unverified request
  **before** it branches on the route (`server.go:450`–`:460`). A verifier holds no run identity, so the
  verify path must be answered **before** the shared parse: the path is recognised first, the one Google ID
  token the verifier carries is verified on its own, and the refusal it gets is the verifier class's own
  refusal with **its own audit line**. Left behind the shared parse, the route would refuse every verifier
  before the class were ever consulted, and the whole route would be dead configuration. This is the one
  place this design changes `ServeHTTP`'s shape rather than adding a row to it;
- the request must carry a **Google ID token** whose `email` claim is in the service's configured
  **verifier list** (`AuthConfig.VerifierAccounts`), verified by the same **token check** `verifyJob` uses —
  audience, issuer, `email_verified`, expiry, not-issued-in-the-future, ASCII fold — with **two**
  differences, stated rather than implied: the address is matched against the verifier list rather than
  against the installation's job accounts, and the shared-job gate does not apply to a verifier, because a
  verifier is not a job of this installation's job set at all;
- the list is **empty by default** — the variable's `env` entry is rendered only when the field is set, and
  the service reads that name as an **optional** value outside the seven it requires, so an installation that
  never sets it starts a service whose list is empty rather than one that refuses to start — and an empty
  list means the route answers every call with the caller class's refusal: an installation with no coordinator
  does not carry a route anybody can reach;
- the coordinator's own account (`fugaro-coordinator`, `docs/coordinator/`) is added to the list by
  **that** work stream's deployment when it lands, which is why this plan adds no coordinator task;
- the owner's own identity may be added for a manual verification; the design says so because the live
  check uses it (§8), and records what it costs in §7.

### 5.5 What the audit line says

C1.13's audit line carries a closed field set and a closed purpose vocabulary (`internal/credsvc/audit.go`:
`PurposeForward` `:90`, `PurposeProbe` `:95`, `PurposeMint` `:104`, `ValidPurpose` `:109`). Two purposes
join it — **`review_sign`** and **`review_verify`** — and `routeAct` (`server.go:59`) gains the two
routes, so a refusal is recorded as the act the run asked for and not as the branch it was answered in.
The credential a sign line names is the **review-signing key's bundle entry name** (§6), the way a mint's
line names the App key; a verify line names the same key, because that is the credential the answer was
computed with.

**A sign line carries** the account, the repository slug and the run — all three from the **verified**
side — and **never the digest**: the digest is not a secret, but a field that is a caller's bytes is exactly
the free-form text this file exists to keep out, and a signature line that carried it would invite one to
carry the signature. It carries **no pull requests**: `AuditEvent` has no field for them, and this design
does not add one (D1062).

**A verify line carries the verifier as its caller and the key as its credential**: `Caller` is the
`email` the verifier's Google ID token verified — the same field a job's line uses for the account, and a
legal one for any foldable address (`validAccount` → `foldEmail`, `audit.go:498`) — and `Credential` is the
review-signing key's bundle entry name, which is the credential the answer was computed from even though the
caller supplied no credential at all.

**Three rules of `checked` (`audit.go:419`) move for these two purposes, and the design states all three
rather than discovering them in a handler.** `checked` is written for lines whose caller holds **both**
identities and whose credential always belongs to a tier, and neither holds for a sign or a verify line:

1. **A credential with no tier is a mint's alone today** (`audit.go:448`, whose both-or-neither rule refuses
   a line naming one without the other for every purpose except `PurposeMint`; `:442` refuses a tier on a
   mint). **A sign line and a verify line are the second and third cases of the same shape**: each names the
   review-signing key, and the key is a signing key and not a model credential of the cascade, so neither
   carries a tier. The exemption grows from the one purpose to the three credential-naming purposes that
   have no tier — and `:442`'s arm, widened the same way, keeps refusing a tier on all three, so the
   change adds no way to record a tier the service never assigned. C3.9e names the test that holds the sign
   line to it (`TestSignLineCarriesACredentialAndNoTier`) and C3.9f the one that holds the verify line
   (`TestVerifyLineCarriesACredentialAndNoTier`), each beside a case that asserts a job purpose still
   carries neither the credential-without-tier shape nor a tier of its own.
2. **`Job` and `Caller` are set together** (`audit.go:438`), because the identity is what carries both. A
   verifier is not a run in a job and reports no `Identity.Job` at all, so the verify purpose carries
   `Caller` without `Job`, and the rule is relaxed for **that purpose only** — a job-purpose line that
   dropped its `Job` is still refused. C3.9f names the test
   (`TestVerifyLineCarriesTheCallerWithoutAJob`).
3. **A `StatusOK` line must carry `Run` and `Job`** (`audit.go:462`–`:473`), which is right for a sign line
   — the caller is the review variant, so both are set from the verified identity — and wrong for a verify
   line, which has **neither**: the verifier holds no run identity and is not a job of this installation, so
   its line carries a purpose, a credential and a status and states no run. The `StatusOK` requirement is
   relaxed for the verify purpose alone, and the run identity's own rule (a `Run` with no `Repository` is
   refused, `:431`) is untouched. C3.9f names the test
   (`TestVerifyAnswerIsAuditedWithoutARunOrAJob`).

**The field set itself does not grow** — no field is added, and a reader of a verify line gets the same
eight names it already reads. What grows is the set of lines three of the rules admit, by exactly the three
cases above and no others.

### 5.6 One signature per review run (D1074)

**A review run gets exactly one signature**, and the service enforces that rather than trusting the caller to
ask once. When it signs, the service writes a **mark** for the run, and it takes the mark **before** it
answers: a mark that cannot be taken is a refusal, and no signature comes back. The mark is a **create-only**
write, and create-only is a *pair* and not one call: the node is read for its ETag first (`GetETag`,
`internal/rtdb/client.go:178`) and written back under that ETag with `PutIfMatch` (`:209`), which is
create-only **because the ETag that came back is the absent node's** — a node created in between carries a
different one and the write fails `ErrPrecondition`. The write cannot be made without the read: `PutIfMatch`
refuses an empty ETag in as many words ("an empty etag is no precondition; read the node with `GetETag`
first", `:211`). This is the discipline `ProbeLeases` already uses for the probe lease
(`internal/credsvc/probe.go:100` — the read at `:617`, the conditional write at `:627`) — on **one node per
`(slug, run)`**:
`creds/review-signs/<slug>/<run>`, a sibling of `creds/probes` (the probe lease's own node, `probe.go:112`)
and the other records under the subtree, with each segment escaped the one way every node under `creds/`
escapes one (`budget.Key`, `probe.go:182`).
Create-only is a property of **the write** and not of the database rules, and that is deliberate: the
service's own writes go through IAM and are not governed by the rules at all, which the rules' own header
states ("admin writes are IAM-only", `internal/budget/rules/rules.go`), so a write-once rule would be a
guarantee this path never consults.

**No client token can read the mark or write it, and that is the subtree's rule rather than this node's**:
`"creds": {".read": false, ".write": false}` (`internal/budget/rules/rules.json.tmpl:9`) denies **every** run
token the whole subtree, which is the same rule that keeps a run out of the marks, the leases and the
stickiness records (O1, C1.3). A mark under it therefore needs **no rules change to be unreachable**, and the
test is the one the subtree's own tests already run:
`TestRunTokenCannotReadOrWriteTheReviewSignMark` (`internal/budget/rules/creds_emulator_test.go`), which
asserts the denial against the emulator for this path and adds it to `credsPaths()` so the two sweeps that
walk that list (`TestCredsRunCannotReadAnything`, `TestCredsRunCannotWriteAnything`) cover the mark as well.
The result is that a review run can neither read its own mark nor clear it to sign again — the write is the
service's, over IAM, and the only reader of the node is the service.

**Both identity tokens are registered with the service's redaction, and that registration is the call's**
(D1074): the run's Firebase ID token and the job's Google ID token are the caller's own values, and the
service holds them for **the length of one call** as `call.scrubs` (`internal/credsvc/forward.go:104`), the
list `readCall(r, []string{req.RunIDToken, req.JobIDToken})` (`server.go:530`) builds — the same registration
the model path makes, and the list the upstream request's body and headers and the answer's own stream are
scrubbed with (`cascade.go:517`). **It is not the auditor's own scrub**: `WithAuditScrub` is armed **once**,
by the bundle loader, with its own scrub of the document it read (`probe.go:702`), and it is
installation-wide — a route that leaned on it would be leaning on a scrub that never knew this call's
tokens. **The verify route's own token is registered by its own read** (§5.4): that path is answered ahead
of `readCall` — a verifier holds no run identity, so the shared parse cannot run for it at all — and the
handler therefore registers the one Google ID token it carries with the same per-call scrub itself. C3.9f's
`TestVerifyRouteRegistersItsOwnIdentityTokenForRedaction` is the test that holds it;
`TestReviewRoutesNeverPrintEitherIdentityToken` — shared with C3.9e — drives the routes the shared parse
does reach, which is why the verify path needs one of its own. This is the §7 asset sentence's mechanism,
and the plan carries it as code on both routes: C3.9e for the sign half, C3.9f for the verify one.

A second sign for the same run is **refused** and audited like every other refusal — one line, the sign's
purpose, the wire's existing `request_refused` status, because the status set is closed and this design does
not grow it — and the service writes a warning to its own log beside it, so what an operator's alerting
selects is a line they can name today rather than a field added for one. **A refused second sign is a
security event**, and the design says which of its two readings it takes: a stage re-run is the honest case
and the run's own end-of-run ordering (§3.4) already makes it unlikely, while something *inside* the
container asking is the case this rule and **U7** (§7) exist for. The mark is what bounds the *how often*
that F11's write-once outcome leaves open — that outcome bounds *when* signing may happen, and this is the
second half.

**What the mark costs, said plainly.** If the mark is taken and the answer never reaches the run — a dropped
connection, a process that dies between the two — then the run has no signature and **cannot ask again**:
the review is unsigned, and the remedy is re-reviewing the head (§3.4), never a second signature. That is
the direction this failure is allowed to go, and it is the opposite of the one §6.4 picks for an unreadable
key — which is read **before** the mark (§5.2), so a run with no usable key takes no mark and loses no
signature it could still be given.

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

**The revoke mode is two consecutive generates, and the design says so rather than adding a mode (D1069).**
One `fugaro creds set --generate <name>` moves the current entry to `…-previous` and writes a new one; run
**twice**, the value that was current before the first run is pushed out of the accepted pair entirely, so
the service accepts neither signature nor verification under it. That is the whole of the revoke: the
accepted set is the current entry and the previous one and **nothing older** (§6.3's first bullet), so the
second generate is what retires a key whose bytes may have been exposed. The alternative — a `--revoke`
flag that writes a document with no current entry — is a third state on a two-state rotation, and it leaves
an account that can neither sign nor verify, which is not a state an operator should be able to reach except
by deleting the entry on purpose. The plan's C3.9d names the test that holds the two-run behaviour
(`TestCredsSetGenerateTwiceRevokesTheOriginalKey`).

### 6.4 Fail closed, always

A missing entry, an entry that is not a usable key, an unreadable bundle: the sign route **refuses**
(`state_unavailable` / the bundle's own typed refusal) and the verify route **refuses** — never
`{"valid":false}`, which would be a service that says "this verdict is not mine" when the truth is "I
cannot tell". The distinction is the one §5.3 draws, and it is the difference between a coordinator that
re-reviews and one that quietly drops a verdict it could not check.

---

## 7. The threat model

**Assets.** The App key (never on the review variant, never in its container); the account's **model
credential** (the bundle's tier A entry, reachable for a review caller through the service's model path and
for **its own account only** — ruling O1); the per-account review-signing key (the service's alone, §6); the
minted installation token (in-job memory, one hour, one repository); the run's **two identity tokens** (the
job's Google ID token and the run's Firebase ID token, which travel in the request's headers and are
registered with the service's redaction, §5.6 and D1074).

**Adversaries, and what each gets.**

| Adversary | What it holds today | What this slice does |
| --- | --- | --- |
| the **review run itself** — a container that runs a test from the head under review by design (D885) | its account's **model credential through the service** (it holds no token of its own, ruling O1), and the read-only installation token it fetches | **closed as far as write access goes**: it can obtain only the read-only set, and only for the repository its own identity names. It **cannot** obtain a write token: the mint chooses the set from the verified claims (§4.2), and a coding run's identity is refused. It **cannot** sign twice (§5.6), and it can reach no key: the review-signing key is not in its container, not in its environment and not in the bundle it may read |
| **a subprocess inside the review container** — the mutation check's mutant, which runs code from the head by design (D885, CSR4b) | whatever the container's own environment holds — and, if the container's egress is not denied, a Google ID token **from the instance metadata server** for the job's own service account | **this is U7, and the sign route is not built before it is answered** (below). Egress denial including the metadata server is a **hard precondition**; the mark of §5.6 bounds what such a subprocess can obtain even then — one signature for the run, never a second |
| **another run on the same project job set** — the D892 gap: it holds `objectUser` on `runs/`, can list, and can write under a prefix it can see | a shared project job's account | **the verdict is signed, and it holds no key**: a forged or replayed `review.json` has no signature to carry, and the key is not in any container |
| a caller that can **reach the service's URL** but holds no admitted identity | nothing | refused before a credential is read, as today |
| the **launcher** | minting authority for run identities, and a share of the installation's own IAM | **unchanged, and stated**: a launcher that can mint an identity for any repository, and deploy a job with an admitted account name, can obtain a read-only token for that repository and sign as that account. This is the same launcher trust the credentials-service plan already records (F29, SECURITY.md) and this slice neither widens nor closes it |
| the **owner's own identity**, if it is added to the verifier list (§5.4) | a Google identity admitted by configuration | it can ask "is this digest valid under this account" and learns one bit. It holds no key and can forge nothing. The list is configuration and empty by default; the live check is why it is stated |

**Residuals, at their true size.**

- **A leaked signing key forges every verdict of that account until rotation.** That is the price of the
  one-holder design and the reason the key stays out of the container that reads an untrusted diff
  (D892). No escrow: an artifact signed under a retired key stops verifying.
- **The signature binds the run and the repository, and that is D1073's answer.** The framed message (§5.2)
  makes a signature valid for exactly one run of one repository, so a transplanted or replayed signature
  fails **at the service** and not only at the coordinator. **What it does not bind is the head**: the digest
  is the artifact's, so which pull request and which head those bytes describe come from the artifact's own
  fields, checked against what the coordinator launched (CSR3's four checks). A head that moves between the
  artifact write and the merge is that work stream's check, not this signature's, and this design says so
  rather than claiming a binding it does not make.
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
- **The review job's model call is this slice's to admit, and the pairing is what makes it safe.** Under
  ruling O1 the review job holds no token and its Claude calls come to the service; the argument is §2.3's —
  the pairing resolves **one** account, tier A only, and a review caller that fails it is refused before a
  credential is chosen. The model path's own machinery (the circuit, the cap, the audit line) is unchanged.
  Nothing here makes an untrusted diff safe to review: the reviewer is still a program reading a hostile
  diff, which is D885's problem and not a credential's.

**The hard precondition: U7 (D1074).** Everything the sign route does rests on one assumption that this
design cannot make from the tree, and it is **U7** of `docs/cloud-senior-review/` — the owner's live
observation, taken before that work stream's mutation check (CSR4b) launches, that **egress can be denied for a
subprocess inside a Cloud Run container** (Check 125 step 6; no network namespace and no `unshare` in
generation 2 has been checked at the time of writing). The sign route makes that assumption load-bearing in
a way CSR4b does not: the review container runs the package's own test code by design (D885), so a subprocess
that can reach the network at all can reach **the instance metadata server** and mint itself a Google ID
token for the job's own service account — which is the identity the sign route admits. Therefore:

- **the sign route is not built before U7 has returned**, and the plan's C3.9e says so in its own
  preconditions rather than in a footnote;
- **the egress denial must cover the metadata server explicitly** — a denial that leaves the metadata
  endpoint reachable leaves the route open, so the observation is about that endpoint and not only about the
  internet at large;
- **if a child cannot be fenced, the route is not built**: U7's own rule applies unchanged — D891 (a) is
  amended and the owner is asked again, with no quiet fallback to a weaker denial;
- **the live check tries it**: §8's steps include an attempt to sign from inside a mutant subprocess, and an
  attempt to reach the metadata server from one, both of which must fail. A check that only reads
  configuration would not observe the property that matters;
- **and §5.6's mark is the second line of defence**, for the case where the assumption holds at build time
  and a later image or platform changes it: even a subprocess that reaches the service gets at most the run's
  one signature, and the second attempt is the security event an operator can alert on.

---

## 8. The owner-run live check

**Named here, numbered at implementation** ([README.md](README.md) §"Numbers this work holds"): the
register's highest written section is Check 190 and open pull requests claim above it, so the task that
writes the check takes the number free at its merge. **It is owner-run: no agent performs any step of
it**, and it is **NOT RUN** until the tasks below ship and the review work's CSR7 and CSR2 are on `main`.

Installation `fugaro`, sandbox checkout only, never `belong`; a sandbox repository (`owner/repo-a`) and a
sandbox pull request.

1. **The two facts `fugaro doctor` reports** (CSR6's): the credentials service is enabled for this
   installation, and the review-only variant's account is admitted. A `warning` line instead is the
   finding, and the check stops there.
2. **The variant holds no credential at all** (ruling O1): `gcloud run jobs describe <the review job>`
   shows **no** secret mounted — not the account's Claude token, which the review job no longer needs
   because its model calls come to the service, and never the App key — and the variant's service
   account's IAM shows **no** `secretAccessor` on the App key, on the bundle, or on anything else.
3. **A review launches and runs**: `fugaro review --pr <N>` on the sandbox completes, and its log names
   **no** token value (the run's own evidence, read by eye).
4. **The token is read-only, and it is one repository's**: a throwaway execution of the variant mints a
   token for the reviewed repository and a **push** with it is refused by GitHub (403); a second
   hand-made call naming a *different* repository the account is admitted for is refused by the
   **service** — the repository is not the caller's to name.
5. **The model call goes to the service, for the account's own tier only**: the run's model traffic is the
   service's (the gateway's own log names it, and the variant mounts no token, step 2), the service's
   **audit line for that call names the account's own tier A credential and no other**, and a hand-made
   call as the review account naming a model that only another tier serves is **not** served by another
   account's credential.
6. **The verdict is signed**: `runs/<slug>/<review-run>/artifacts/review.json.sig` exists, is one line of
   lowercase hex, and the coordinator's verify path (or a hand-made verify call, step 8) answers valid
   for it. The **sign line** names the account, the repository slug and the run, and carries **no** pull
   requests — the field set has nowhere to put them.
7. **A tampered artifact does not verify**: one byte changed in a copy of `review.json` (never the stored
   object) and the signature no longer verifies; a copy of a valid signature pasted under a *different*
   review run is refused **by the service itself** — the framed message (§5.2) is not the one that was
   signed, so the answer is `{"valid":false}` before any coordinator check runs — and an artifact with
   **no** sidecar is refused.
8. **The verify route's caller class, and what its line carries**: with the owner's own identity in the
   verifier list, a hand-made `POST /v1/review/verify` with the owner's ID token answers `{"valid":true}`
   for the real pair and `{"valid":false}` for the tampered one; the same call **without** a token is a
   typed refusal; and the service's log shows the line — the **verifier's email as its caller field** and
   the **review-signing key's entry name as its credential**, with no run and no job on it, because a
   verifier is not a run.
9. **A subprocess inside the container can reach nothing — U7, observed after the route exists
   (D1074)**: from inside a mutant subprocess of the mutation check, a `POST /v1/review/sign` **fails**
   (no egress) and a request to the **instance metadata server** fails too. Both are the property the sign
   route was built on, and the owner re-runs this observation against the shipped image rather than
   trusting the pre-build one.
10. **The second sign is refused, and it is the security event**: with the run's own identity (minted by
    the owner as the launcher would), a second `POST /v1/review/sign` for the run that already signed is
    refused with `request_refused`, **no** second signature exists, and the warning the service wrote is
    the line an operator's alerting would select.
11. **The gate still holds where it was written**: a call from a *coding* shared project job's account is
    refused on every route, and the **review account is refused the verify route**; the refusal's own
    sentence is what the owner reads. Then the check's own cost: the review run's spend, the artifact
    kept, and nothing created, enabled or deleted outside the sandbox.

**The register-shaped entry this becomes is [live-check.md](live-check.md)** — the row's shape, read off
the register's own Check 190, and the eleven steps above one line each with the numbering this section
gives them, so the task that takes the number derives neither the shape nor the numbering at merge. That
file is a rendering and this section is the check: **where the two disagree, this one governs.**

---

## 9. What `fugaro update` does, which is everything that is an upgrade

The HARD RULE is that `fugaro update` is the whole upgrade and that a release ships no operator step that
is *another command*. This slice's reading of it, stated because a reviewer must be able to check it:

- **No new resource, no new container, no new grant, and one variable that is null until someone asks for
  it.** The key is a bundle entry (§6.1), so no plan this work touches gains a resource and an installation
  that has never opted in is byte-for-byte untouched by this work: the review class is empty when no policy
  publishes a profile, the routes answer their refusals, and
  `TestReviewAccountsAreTheNamesProjectJobsDeploys` holds an installation with no review job to an empty
  set. The one Terraform change is the verify route's verifier list (C3.9g1): a variable beside the service's
  others, whose `env` entry is rendered **only when the field is set**, so the plan of an installation that
  never sets it is the plan it has today — the same seven names, held as an assertion by the tftest's unset
  run. **Inert is a claim about the container too, and it is why the name is read as an optional value**:
  `credentialsServiceEnvNames` is the list the service **requires** — `credentialsServiceConfig` refuses to
  bind a port when any of its names is empty — so the verifier list's name stays out of it and is read
  outside that loop, where an absent or empty value is an **empty verifier list**. An installation that never
  sets the field therefore starts the same service it starts today, with a route that refuses every caller,
  rather than one that refuses to start; Check 96's "the seven names the config builder reads" stays true.
  **Where that value comes from at `fugaro update` is D1075, recorded and not decided**
  ([reconciliation.md](reconciliation.md) §3): the recommendation is the **configuration `fugaro update`
  already holds and publishes as the policy** — the source `internal/infra/tfvars.go`'s `Credentials()`
  (`:289`) and `CredentialsJobAccounts` (`:343`) already read — so naming verifiers is the publication the
  owner already makes and not a new act, and the alternative readings — a second, unpublished local
  configuration, or a value derived from the coordinator's deployment — are stated there with their costs.
- **An installation already opted in converges with an ordinary `fugaro update`** for everything this
  slice adds to *code*: the classes, the routes, the refusals, the audit purposes. The service's image is
  re-pinned by `fugaro update` already.
- **The key's bytes are the service's own documented step**, exactly as `github-app-key` is (D893's own
  words: "both of which are the credentials service's own documented steps, not new ones this design
  adds"). It is not an upgrade step: nothing breaks without it, the service keeps answering everything it
  answered before, and a sign or verify call answers a **named** refusal the operator can act on. What the
  design does owe is that the *name* is reachable: `fugaro doctor` reports it beside the two facts CSR6
  already reports, and the service's refusal says which entry is missing.
- **The review job cannot be launched before the key exists**, because CSR6's doctor check and CSR2's launcher
  refuse the launch rather than shipping one the service refuses. That is the same posture E7's other half
  has, and it is why no user meets a half-configured review.
- **A version gate, not a silent ignore**: this slice adds no `fugaro.yaml`, task or wire *field*, so
  `internal/cli/recipes_skew.go`'s `…Since` gates are not needed for it — the two routes are new paths and
  an older job image never calls them. Where the review work adds fields, E6 is that work's rule.
- **The review variant's own environment is CSR7's, and this slice asks for one change in it**: under ruling
  O1 the variant carries the service's URL (§4.3) and **no** model token, so CSR7 mounts nothing where it
  would have mounted the account's Claude token. That is a change to CSR7's module and not to `fugaro update`:
  no installation root's plan moves because of it, and the class is admitted the moment the service's image
  is re-pinned.

---

## 10. What this slice does not do

- **Shared jobs for coding runs (the general C3.9).** The coding shared project jobs stay refused on every
  route, with today's sentence and today's test. **Task 3.4a's refusal** of an OAuth launch onto a shared
  job stays lifted-at-C3.9 and is **not touched**.
- **The tier-A registry and the reference record of `docs/design/project-centric.md` §7.** D131 and O4
  name them as what the shared-job work grows into. This slice reads **O4's published admissions** — which
  is the source the model path already uses — and builds no registry.
- **The model-credential move for shared jobs.** `--provision-credentials` still does not reach
  `modules/project`; the **coding** shared job keeps its provider keys and its `CLAUDE_CODE_OAUTH_TOKEN`
  (`internal/cli/init_credentials.go:21`). The **review** variant is the one job that mounts nothing, under
  ruling O1, and that is this slice's whole difference from it.
- **The cascade for a review caller.** The review class **is** admitted to the model path (ruling O1), and
  what it is not admitted to is the *cascade*: it is served tier A only — the account the pairing resolves,
  for a repository that account is admitted to — so no tier registry, no `fallback` ladder and no bucket
  narrowing is exercised for a review caller, and D131/O4's registry stays unbuilt. Serving a review caller
  another account's credential would be the widening Global Constraint 6 exists to refuse, and it is what
  §2.3's pairing rules out.
- **The coordinator and its merge path.** The verify endpoint is built and configured; the component that
  calls it, and the `ship` → merge path that acts on a verified verdict, is the coordinator's own work
  stream (`docs/coordinator/`), and it stays blocked on this slice only in the sense that it now has a
  route to call.
- **The reviewer's own artifact format, its four replay checks, and the mutation check.** CSR3 and CSR4b of
  `docs/cloud-senior-review/`.
- **Any per-run cap or lease for a review.** The service's per-run cap is advisory (D143); nothing here
  adds a second budget.
- **A `fugaro review` command.** CSR2's, and gated on this slice rather than part of it.
