# C3.9's review half: implementation plan

**Nothing here is approved to build, and every decision is answered.** The owner answered
[reconciliation.md](reconciliation.md) §3's **D1062–D1074** on 2026-11-01 — D1062–D1071 as recommended,
D1072 as 0.16.0, D1063 as amended by that day's ruling O1, and D1073/D1074 as the review added them — so
each task below is written to an answer and not to a recommendation. Each
task touches at most 10 files, names its tests, starts with a red step, and ends at the gates below.
[docs/credentials-service/implementation-plan.md](../credentials-service/implementation-plan.md) keeps
**C3.9** for the general case; this plan is the review half of it and carries the scope amendment in
§"The amendment the implementing pull request carries".

## Dependencies

Five things this plan does **not** own. **CSR1 and U7 are the two that block a task** — CSR1 blocks C3.9b,
U7 blocks C3.9e (D1074) — and the other three are facts the tasks are written to. **The `CSR` ids are
`docs/cloud-senior-review/`'s own task ids**, spelled as that work stream spells them (CSR1–CSR7, with its
former R4 split into **CSR4a**, the settings posture, and **CSR4b**, the mutation check); this folder cites
them and renames none of them.

- **CSR1, the read-only permission set** (`docs/cloud-senior-review/`, `internal/gitprov/github/apptoken.go`):
  C3.9b **serves** that set and does not declare a second spelling of it. This is the one blocking
  dependency, and it is another work stream's.
- **U7, the egress observation** (`docs/cloud-senior-review/`, Check 125 step 6): the owner's live probe that
  a subprocess inside a Cloud Run container cannot reach the network — **the instance metadata server
  included** — and it is the **hard precondition of C3.9e** (D1074, [spec.md](spec.md) §7). The reader should
  read the dependency in the direction the ruling wrote it: the sign route is what makes U7 load-bearing,
  because a review container runs the package's own test code by design (D885) and the metadata server is
  where a Google ID token for the job's own account comes from. If a child cannot be fenced, **C3.9e does not
  start**: D891 (a) is amended and the owner is asked again, with no quiet fallback to a weaker denial.
- **CSR7, the review-only job variant**: its account's ID spelling is CSR7's, and C3.9a computes the service's
  review set **through the same helper the deployer uses for it**
  (`docs/cloud-senior-review/implementation-plan.md` CSR7; [spec.md](spec.md) §2.2). Two requirements this
  plan depends on and does not write: the variant has its **own** service account — not the coding job's,
  which would put one email in two classes and leave the class rule with nothing to decide — and the
  variant's environment carries `FUGARO_CREDENTIALS_SERVICE_URL` (finding **F14**; `internal/cli/run.go`'s
  `credentialsServiceLaunchEnv` at `:2958` deliberately does not reach a shared job). **Ruling O1 adds a
  third**: the variant mounts **no** model token, where CSR7 would have mounted the account's Claude token,
  because a review job's model calls come to the service ([spec.md](spec.md) §1). That is CSR7's module to
  change and no task here touches it.
- **E7, the service enabled for the installation**: `fugaro update --provision-credentials`, the App key in
  the bundle, and the review variant deployed. C3.9a–C3.9g2 change nothing about that deployment; they make
  the service **admit** what E7 deploys.
- **The coordinator** (`docs/coordinator/`): the verify route is built and its caller class configured
  empty. The component that calls it, and the merge path that acts on a verified verdict, is that work
  stream's, and no task below adds one (X1).
- **Naming a file that other work owns.** `internal/gitprov/github/apptoken.go` (CSR1),
  `internal/infra/projectjobs.go` and the module that deploys the variant (CSR7), and everything under
  `ee/`, `console/`, `mobile/`, `website/`, `plugin/` are **read, not changed** by this plan.

### Order

| Level | Tasks | Needs |
| --- | --- | --- |
| 0 | C3.9a, C3.9c | nothing (each reads a derivation another work stream owns) |
| 1 | C3.9b | C3.9a, and **CSR1 merged** (the set it serves) |
| 2 | C3.9d, C3.9e | C3.9c (the entry's name, kind and read path); C3.9e also C3.9a **and U7 returned** (D1074) |
| 3 | C3.9f | C3.9a, C3.9c, C3.9e (the frame the verify route recomputes) |
| 4 | C3.9g1, C3.9g2 | C3.9e, C3.9f; the two are one pull request because a test holds the module's names and the CLI's equal (see C3.9g1); the live check C3.9g2 names is not run until **CSR7 and CSR2** are on `main` |

## Where each decision lands

| Decision | Task | Decision | Task |
| --- | --- | --- | --- |
| D1062 the third account class | C3.9a | D1068 the verify route's caller class | C3.9f |
| D1063 Global Constraint 6's narrowed scope | C3.9a (code), and §"The amendment the implementing pull request carries" | D1069 where the key lives | C3.9c |
| D1064 the pairing for a project-scoped caller | C3.9a | D1070 what the slice does not do | §"The amendment the implementing pull request carries" (its second edit), and nothing in the code |
| D1065 which token set | C3.9b | D1071 who generates the bytes | C3.9d |
| D1066 the repository, and the table | C3.9a (the table), C3.9b (the mint's use) | D1072 the release slot | §"Release slot" |
| D1067 what is signed | C3.9e | D1073 the framed message | C3.9e (built), C3.9f (recomputed) |
| D1074 one signature per run, and U7 | C3.9e | D1075 the verifier list's value (**answered**: the published policy) | C3.9g1 (the field), C3.9g2 (the wiring) |

## Where each finding lands

| Finding | Task | Finding | Task |
| --- | --- | --- | --- |
| F1 no account class | C3.9a | F8 D892 vs Q6's one container | C3.9c |
| F2 the gate's text covers the variant | C3.9a, C3.9g2 | F9 the bundle's closed key set | C3.9c |
| F3 the mint's repository is the account's | C3.9b | F10 the write path, and the missing generating mode | C3.9d |
| F4 no slug → canonical resolution | C3.9a | F11 signing is an end-of-run act | C3.9e |
| F5 the set is hard-coded, CSR1 is missing | C3.9b | F12 no verifier class | C3.9f |
| F6 the claims are already verified | C3.9b | F13 O4's admissions reader exists | C3.9a |
| F7 every closed set the routes touch | C3.9e, C3.9f | F14 the launch wiring is CSR7's | no task — a dependency |

## Gates (every task)

`go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); the task's named tests with
`GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1`; `GOFLAGS=-p=2 go test ./internal/cli/ -run
Docs -count=1 -timeout 40m` when a doc changed; `go test ./internal/eeboundary/... -count=1`; and
`go test -tags terraform ./internal/infra/...` in C3.9g1, which is the task that wires the variable. Never the
whole `internal/cli` or `internal/runner` package. No task adds a dependency, so `go.mod` is unchanged by all
eight.

## C3.9a. The review class, the repository table and the pairing (D1062, D1063, D1064, D1066, F1, F2, F4, F13). **SEC**

The service gains a third account class and the one thing the other two do not need: a way to pair a caller
with a run when the caller's account names no repository. The gate is **narrowed**, not lifted: `verifyJob`
keeps refusing the shared set with today's sentence, and the review set is admitted on the **three routes its
class owns** — the **mint** (C3.9b), the **sign** (C3.9e) and the **model path** (`/v1/messages` and its
`count_tokens` sibling) **for its own account only**, which is what the owner's ruling **O1** put into
**D1063** — and refused everywhere else, the **verify route** (C3.9f) included. **The model path is not a
later task's**: the mint and the model path already exist, and what admits a review caller to them is the
class and the pairing this task writes, decided in the same `verifyJob` that refuses a shared caller on every
route (`sharedAccounts`, `auth.go:469`, which is the gate a shared caller meets on the model path too).

- **Files (9):** `internal/credsvc/review.go` (new: `ReviewJobAccount{Email, Account}` — the account
  **name** is what the published admissions speak and the email is what the service is called as;
  `ReviewJobAccountsFor(p *projectpolicy.Policy, gcpProject string) []ReviewJobAccount`;
  `RepositoryTableFor(fugaroProject, gcpProject, provider string, jobs []RepoJob) (map[string]string, error)`
  over the same input `JobAccountsFor` takes; `ClassRepository`/`ClassShared`/`ClassReview` and `ReviewOf(id
  Identity) error` — the claim rule of [spec.md](spec.md) §3.3), `internal/credsvc/auth.go` (the class on
  `Identity`, `verifyJob` answering it, `AuthConfig.ReviewJobAccounts` beside `SharedJobAccounts`, the
  disjointness refusal beside the "in both sets" one), `internal/credsvc/server.go` (`Config.ReviewAccounts`
  and `Config.Repos`, the pairing in the binding check at `:476` — the class decides which of the two
  pairings is made — **and the `candidates` call at `:581`**, which passes `ja.Repo` and is the reason a
  review caller needs the pairing to have resolved a repository before it: a review caller has **no `ja`**,
  so the canonical repository the cascade is asked about comes from the table, and the call site reads it
  from the pairing rather than from the job account),
  `internal/credsvc/cascade.go` (the offering rule a review caller must not widen: `candidates` (`:33`) is
  what decides which tiers are offered, and `admittedAccounts` (`:87`) is the admission check the pairing
  **reuses** rather than spelling a second time — see the next bullet), `internal/cli/credentials_serve.go`
  (the review set built from the policy the service already reads, and the repository table from
  `FUGARO_JOB_ACCOUNTS`' own input), `internal/credsvc/review_test.go` (new), `internal/credsvc/cascade_test.go`,
  `internal/credsvc/sharedjobs_test.go`, `internal/credsvc/auth_test.go`.
- **The pairing reuses `admittedAccounts`, and it does not read the child node alone.** The third step of
  [spec.md](spec.md) §3.2 is `admittedAccounts(ctx, slug, canonical)` (`cascade.go:87`), the intersection the
  cascade already makes before it offers a tier-A credential: the child node names the account, the shared
  node **defines** it, and **both** the account's `allow_repos` and the project's `allow_repos` admit the
  repository. Reading `PolicyReader.Admissions` by itself is the narrower spelling and keeps admitting a
  repository the owner withdrew with `fugaro project repos rm` — the command removes the repository's place
  in the project's allow-list and does not touch its child — so the review pairing takes the whole
  intersection and no second implementation of O4 exists. **The class must not widen the offering**:
  `candidates` (`cascade.go:33`) is the one place that decides which credentials a call may be served, and
  for a review caller it returns the pairing's **one** account and **tier A** — never another tier-A
  account's entry, and never a tier B/C one, so a review call has nothing for the ladder to fall to even
  where the policy's `fallback` would allow one ([spec.md](spec.md) §10's cascade bullet).
  `TestReviewCallerIsOfferedNoTierBOrCAndNoOtherAccount` is the test that holds it, beside the existing
  cascade tests over the same function.
- **One derivation, one input, one test.** `ReviewJobAccountsFor` reads the **published policy** — so an
  installation that publishes no profile admits no review job — and derives each email through the same
  `serviceAccountEmail` (`server.go:178`) over the variant's ID spelling, which CSR7 owns;
  `TestReviewAccountsAreTheNamesProjectJobsDeploys` builds one installation both ways and requires equality,
  the shape `TestSharedJobAccountsAreTheNamesProjectJobsDeploys` (`sharedjobs_test.go:136`) already has. The
  shared set is narrowed in the same change; `TestSharedAndReviewSetsAreDisjoint` pins it, because an
  account in both is refused everywhere and the review job could never mint.
- **The class is not on the wire and not in the audit line.** `Identity.JobClass` is internal to the
  package and the line's field set (`internal/credsvc/audit.go`) does not grow: the act the line records is
  the route the caller asked for, which already says what the caller was doing. A refusal names the account
  it refused no more than it does today.
- **The repository table is not a second spelling.** Its keys are `task.Slug(provider, repo)` and its values
  `projectpolicy.Canonical(provider, repo)` — the two derivations the run identity and the mint already use —
  and an unknown slug is refused rather than guessed (D1066).
- **Red step:** `TestReviewAccountIsAdmittedOnTheModelPathForItsOwnAccount` fails at its first assertion
  while the class does not exist: the review caller is refused with "the caller is not one of the
  installation's job service accounts", which is the state this task's subject exists to change. The test
  names the **model path** because that is the route a review caller reaches first, and the one **D1063** as
  amended by **O1** admits it to; the mint and the sign are not in its name because C3.9b and C3.9e are the
  tasks that open those two routes to the class, and it claims no refusal on the model path at all — the
  refusal the test does pin is the pairing's, for an account that is not the one the run identity claims.
- **Tests:** `TestReviewAccountIsAdmittedOnTheModelPathForItsOwnAccount`,
  `TestReviewAccountIsRefusedTheModelPathForAnotherAccount` (the pairing is the class's, and a review account
  is admitted for its own account only),
  `TestReviewCallerIsOfferedNoTierBOrCAndNoOtherAccount` (the offering rule: tier A for the pairing's one
  account, never tier B/C and never another account),
  `TestReviewAccountsAreTheNamesProjectJobsDeploys`, `TestSharedAndReviewSetsAreDisjoint`,
  `TestSharedJobCallerIsStillRefusedOnEveryRoute` (F2 — today's behaviour, unchanged),
  `TestReviewCallerOnAnUnpublishedRepositoryIsRefused` (F13, fail closed),
  `TestRepositoryTableResolvesASlugToItsCanonicalName`, `TestRepositoryTableRefusesAnUnknownSlug` (F4),
  `TestReviewIdentityRequiresNoBranchAndAtLeastOnePR`,
  `TestReviewIdentityWithABranchIsRefused` (a coding run in the review job is refused, never downgraded),
  `TestReviewIdentityWithAnEmptyPRListIsRefused` (an artifact run is not a review),
  `TestClassIsDecidedFromTheVerifiedEmailNotTheHeader`.

## C3.9b. The mint: CSR1's set, the run's repository, and the order of the refusals (D1065, D1066, F3, F5, F6). **SEC**

The mint keeps its shape — one App ID, no repository from the caller, one permission set for one repository
(F3) — and gains two decisions made on the verified side: **which repository** (the run identity's `fs`
resolved through C3.9a's table) and **which set** (CSR1's read-only set for a review caller, the run set for a
repository caller, and a refusal for any other shape).

- **Files (4):** `internal/credsvc/github.go` (the set chosen from the class and the claim rule, the
  repository from the table, the refusal order documented where the branch is), `internal/credsvc/review.go`
  (the set chooser, beside the class it reads), `internal/credsvc/github_test.go`,
  `internal/credsvc/review_test.go`. **Read, not changed:** `github.MintInstallationToken` and,
  once CSR1 lands, the exported read-only set in `internal/gitprov/github/apptoken.go`.
- **The refusal order is part of the contract, and it is the code's rather than this plan's to choose**:
  unverified identities (`RequestFromHTTP`, `wire.go:842`; `Verify`, `server.go:457`) → class not admitted to
  the mint → **the pairing and the claim rule together** (`ja, bound := s.jobs[id.JobAccount]`,
  `server.go:476`, whose shape the class decides; the claim rule of [spec.md](spec.md) §3.3 is the same
  admission read for the mint's two questions, so the two are one step and not two) → **the stop flag**
  (`Kills.Stop`, `server.go:492`) → the bundle → the mint itself. **The pairing comes before the stop flag
  because that is where `ServeHTTP` makes it**, and this plan's first draft had the two the other way round;
  the order is not rearranged to suit the new class, and [spec.md](spec.md) §4.4 and §5.2 say the same. A
  review caller that fails the pairing or the claim rule is refused **before** the bundle is read, so a
  mis-shaped identity never touches key material.
- **No new request field and no new answer field.** `MintRequest` stays the App ID alone, and the answer's
  existing `Repository` field carries the resolved canonical name — so a review run learns which repository
  its token is for from the answer, not from a request it wrote (D893 (c)).
- **Red step:** `TestReviewMintServesTheReadOnlySetForTheRunsRepository` fails while the mint serves
  `RunTokenPermissions()`: the token's permissions are the run's, which is exactly the exposure D883 removes.
- **Tests:** `TestReviewMintServesTheReadOnlySetForTheRunsRepository`,
  `TestReviewMintRefusesARunIdentityWithABranch`, `TestReviewMintRefusesARunIdentityWithNoPullRequests`,
  `TestReviewMintRefusesARepositoryTheAccountIsNotAdmittedFor` (D1064's third step),
  `TestReviewMintRefusesBeforeReadingTheBundle` (the order), `TestRepositoryMintKeepsTheRunSet` (F5),
  `TestRepositoryMintKeepsTheRunSetForAReviewShapedIdentity` (§4.2's third row read with the class
  deciding: a repository-class account's call carrying the review identity's claim shape is served the run
  set and is not the review class),
  `TestRepositoryMintKeepsNamingTheAccountsRepository` (F3), `TestMintRefusalsAreAuditedAndCarryNoToken`.

## C3.9c. The key's home: one entry per account, a closed key set, and a fail-closed read (D1069, F8, F9). **SEC**

The per-account review-signing key is a **bundle entry**, because Q6 pins the service's Secret Manager reach
to one container and one grant and `TestCredentialsServiceGrantsAreTheOneBundle` refuses a second (F8).
Nothing in this task reads a key at request time yet; it makes the entry expressible, checkable and
discoverable.

- **Files (6):** `internal/config/secretnames.go` (the name — the account family's spelling with a
  review-signing suffix, and the `…-previous` name the rotation writes), `internal/config/secretnames_test.go`,
  `internal/credsvc/bundle.go` (`CredentialKey` `:441` answering the new kind `KindReviewSigning` for the
  family, `ParseBundle`'s closed set, `Bundle.ReviewSigningKey(account, previous bool)` and its fail-closed
  refusals), `internal/credsvc/bundle_test.go`, `schemas/creds-bundle.schema.json`,
  `internal/cli/creds.go` (`creds ls` naming the entries and their kind, never a value).
- **No tier and no cascade.** The entry's kind is not `KindModel`, so `Bundle.Credentials()`
  (`bundle.go:332`) never offers it and the model path is untouched by it — the property
  `TestBundleSchemaMatchesTheLoader` (`bundle_test.go:637`) and the cascade's own tests hold together.
- **The reach is unchanged**: no Terraform, no second container, no second grant, no `condition`. A task
  that finds itself editing `deploy/terraform/` for this has taken a wrong turn, and
  `TestCredentialsServiceGrantsAreTheOneBundle` (`deploy/terraform/terraform_test.go:3984`) is the test that
  says so.
- **Red step:** `TestReviewSigningEntryIsALegalBundleKeyAndIsNoModelCredential` fails while
  `CredentialKey` refuses the name: the loader refuses the whole document, which is what makes the entry
  first a *shape* problem and only later a key.
- **Tests:** `TestReviewSigningEntryIsALegalBundleKeyAndIsNoModelCredential`,
  `TestReviewSigningKeyIsRefusedAValueThatIsNotAUsableKey`,
  `TestReviewSigningKeyIsNotOfferedToTheCascade`, `TestReviewSigningKeyReadIsFailClosed` (missing, empty,
  unparseable, and a document that does not parse → a typed refusal each),
  `TestCredsLsNamesTheReviewSigningEntryAndNoValue` (the rendering, held to `creds ls`'s own rule that no
  value is ever printed).

## C3.9d. Generating the bytes, and the rotation window (D1071, F10). **SEC**

An HMAC key has no upstream to paste from, so the CLI generates it at the owner's terminal and writes it
straight into the bundle. The write travels the path `github-app-key` already travels
(`bundleStore.add` `internal/cli/creds.go:346`, `setBundleEntry` `:869`, `credsLostUpdate` `:668`), so the
lost-update guard, the read-back comparison and the disable-the-previous-version step are the ones that
already exist rather than new ones.

- **The mode is a flag on `creds set`, and it carries no value:** `fugaro creds set --generate NAME`. The
  value is generated in the process and never printed, so the command's own rule — a value never comes from
  an argument, a flag or the environment, and is never printed — is kept; what changes is only where the
  bytes come from, and `creds set`'s help text says so in the same words. *(The alternative — a new
  `fugaro creds rotate` subcommand — is a second surface for the same write; the owner may prefer it and
  only this task changes.)*
- **Rotation is one write of one document** (§6.3 of [spec.md](spec.md)): the new value under the current
  name and the replaced value under the `…-previous` name, in the same version, so there is no instant in
  which the service holds neither. The service signs with the current entry and verifies against the current
  and the previous one, and against nothing older.
- **The revoke mode is two consecutive generates, and the task writes no mode for it** (D1069, §6.3 of
  [spec.md](spec.md)): the second run pushes the once-current value out of the accepted pair, which is
  exactly the revoke. `TestCredsSetGenerateTwiceRevokesTheOriginalKey` is the test that holds it — it asserts
  that a value signed with the original key no longer verifies after two generates — and the help text says
  so in one sentence, so an operator does not have to infer it.
- **Files (5):** `internal/cli/creds.go` (the flag, the generation, the two-entry write, the help text),
  `internal/cli/creds_test.go`, `internal/cli/creds_pty_test.go` (the terminal path, which is the one an
  owner actually uses), and the doc that quotes `creds set`'s help — `docs/credentials-service/`'s bundle
  document — with the pin test that reads it (`internal/cli/docs_credentials_bundle_test.go`) updated in the
  same change, as the docs rule requires.
- **Red step:** `TestCredsSetGenerateWritesAFreshValueAndNeverPrintsIt` fails while `--generate` is an
  unknown flag; `TestCredsSetGenerateTwiceRotatesAndKeepsThePreviousEntry` fails while the second run
  overwrites instead of carrying the previous value.
- **Tests:** `TestCredsSetGenerateWritesAFreshValueAndNeverPrintsIt`,
  `TestCredsSetGenerateWritesALegalBundleKey` (the value is one the service's loader accepts),
  `TestCredsSetGenerateTwiceRotatesAndKeepsThePreviousEntry`,
  `TestCredsSetGenerateTwiceRevokesTheOriginalKey` (the two-run revoke, §6.3),
  `TestCredsSetGenerateKeepsEveryOtherEntryByteForByte`,
  `TestCredsSetGenerateIsRefusedInACodingAgentsSession` (the existing refusal, honoured by the new mode),
  `TestCredsSetGenerateRefusesANameOutsideTheReviewSigningFamily` (the mode is not a general generator).

## C3.9e. The sign route, the frame and the one-signature mark (D1067, D1073, D1074, F7, F11). **SEC**

**Precondition: U7 has returned, and it must cover the metadata server** (D1074, §7 of [spec.md](spec.md)).
This task does not start before the owner's live observation says a subprocess inside the review container
cannot reach the network — the instance metadata server included — because the review container runs the
package's own test code by design (D885) and the metadata server is where a Google ID token for the job's own
service account comes from. If a child cannot be fenced, the task stops and the owner is asked again; there
is no weaker fallback and no `denyWeb`-shaped substitute.

`POST /v1/review/sign` takes a digest and answers the HMAC-SHA-256 of the **framed message** under the
account's key, lowercase hex, with the algorithm named in the answer. The frame is `v1\n<slug>\n<run>\n<digest>`
over the identity's own verified `fs` and `fr` (D1073) — the request never names either field — and the task
writes the frame in one place so the verify route (C3.9f) recomputes the same bytes rather than a second
spelling of them. What the route deliberately does not do is in [spec.md](spec.md) §5.2.

- **Files (9):** `internal/credsvc/verdict.go` (new: the frame builder, `SignPath`'s handler, the strict
  4 KiB body decode, the answer, the key read through C3.9c's accessor, the HMAC and its hex rendering, the
  mark), `internal/credsvc/server.go` (`SignPath` beside the three paths at `:33`, `routeAct`'s row, the
  create-only writer on `Config`), `internal/credsvc/audit.go` (`PurposeReviewSign`, and the
  credential-without-tier exemption `:442`/`:448` grown to this purpose),
  `internal/credsvc/verdict_test.go` (new), `internal/credsvc/forward_test.go`
  (`TestServiceHasNoAdminRoutes`, `:341`, whose list the new route joins), `internal/credsvc/audit_test.go`,
  `internal/budget/rules/creds_emulator_test.go` (the mark's node denied to a run token, and its path added
  to `credsPaths()`), `internal/cli/credentials_serve.go` (the writer wired from the database client the
  service already holds), `internal/cli/credentials_serve_test.go`.
- **One signature per run, and the mark is taken before the answer** (D1074, §5.6 of [spec.md](spec.md)):
  a **create-only** write, which is a pair of calls and not one — the node is read for its ETag
  (`GetETag`, `internal/rtdb/client.go:178`) and written back under it with `PutIfMatch` (`:209`), and the
  write is create-only because that ETag is the absent node's, so a node created in between fails
  `ErrPrecondition`; `PutIfMatch` refuses an empty ETag, which is why the read comes first (`:211`), and the
  pair is the discipline `ProbeLeases` uses for the probe lease (`internal/credsvc/probe.go:100`, read at
  `:617`, write at `:627`) — on one node per `(slug, run)`,
  **`creds/review-signs/<slug>/<run>`**: a sibling of the probe lease's own `creds/probes` (`probe.go:112`),
  with each segment escaped the one way every node under `creds/` escapes one (`budget.Key`, `probe.go:182`).
  The property is the write's and not the rules': the service's own writes go through IAM and bypass the
  rules (the rules' own header says so). **No client token can read the node or write it, and the rules need
  no change for that**: `"creds": {".read": false, ".write": false}`
  (`internal/budget/rules/rules.json.tmpl:9`) denies every run token the whole subtree, and
  `TestRunTokenCannotReadOrWriteTheReviewSignMark` (`internal/budget/rules/creds_emulator_test.go`) asserts
  the denial against the emulator for this path and adds it to `credsPaths()`, so the two sweeps that walk
  that list cover the mark too. The order is mark-then-answer, so a mark that cannot be taken is a refusal
  and never a signature; a second sign is refused with the wire's existing `request_refused` status (the
  status set is closed) and logged, and a run marked but unanswered is an unsigned review and a re-review —
  the direction §5.6 chooses on purpose.
- **The key is read before the mark, and an absent key is a refusal that leaves the mark unwritten** (F6,
  §5.2 and §6.4 of [spec.md](spec.md)): the handler reads the account's bundle entry first, **failing
  closed** — a missing entry, an entry that is not a usable key, an unreadable bundle is `state_unavailable`
  or the bundle's own typed refusal, and **no signature under anything else** — and only a key in hand
  reaches the mark. The order is what makes the two failures different on purpose: a run with no key yet is
  refused **before** anything is written, so the operator's `creds set` and a re-run sign the head, while a
  mark that cannot be taken is a refusal for a run that has already signed. `TestSignRouteRefusesAReviewAccountWithNoKeyYet`
  asserts both halves — the typed refusal and **no mark written** — which is what holds the order in place
  rather than leaving it to the handler's own reading order.
- **Both identity tokens are registered with the redaction for the call, and that is `call.scrubs` rather
  than the auditor's scrub** (D1074): the two header values this route verifies — the run's Firebase ID token
  and the job's Google ID token — are the caller's own tokens, and the service registers them **for the
  length of one call** as `call.scrubs` (`internal/credsvc/forward.go:104`), the list `readCall(r,
  []string{req.RunIDToken, req.JobIDToken})` (`server.go:530`) builds and the same registration the model
  path makes: the upstream request's body and headers are scrubbed with it and so is the answer's own stream
  (`cascade.go:517`). **It is not `WithAuditScrub`**: that one is armed **once**, by the bundle loader, with
  the loader's own scrub of the document it read (`internal/credsvc/probe.go:702`) and is installation-wide —
  a route that leaned on it would be leaning on a scrub that never knew this call's tokens.
  `TestReviewRoutesNeverPrintEitherIdentityToken` drives the route with fixture values and asserts neither
  token reaches the answer, the audit line, a log line or the text of a refusal. C3.9f registers the
  verifier's one token itself, because its path is answered ahead of `RequestFromHTTP` and so never reaches
  `readCall` — and it names its own test for that, `TestVerifyRouteRegistersItsOwnIdentityTokenForRedaction`,
  because this shared one drives the routes the shared parse reaches and not the verify path.
- **The line carries the verified side and never the digest** (§5.5): the account, the repository slug and
  the run come from the identity the service verified, and the digest, the signature and the key's bytes
  reach no field, because a field that is a caller's bytes is what the closed field set exists to keep out.
  The line does **not** name pull requests: `AuditEvent` has no field for them (D1062).
- **Signing is an end-of-run act** (F11): `checkLive` already refuses a run whose outcome is written, and
  the handler does not weaken it. A killed run leaves an unsigned artifact and no re-signing path exists.
- **Red step:** `TestSignRouteAnswersAnHMACOfTheFramedMessageUnderTheAccountsKey` fails while the route is a
  404: the path is not in `ServeHTTP`'s set, which is how D143's "no admin endpoint" is kept.
- **Tests:** `TestSignRouteAnswersAnHMACOfTheFramedMessageUnderTheAccountsKey`,
  `TestSignAnswerNamesTheAlgorithmAndIsLowercaseHex` (D892's answer shape),
  `TestFrameBindsTheVerifiedSlugAndRunAndNotARequestField` (D1073: a request carrying `run` or `repository`
  is refused as an unknown field, and two runs' frames differ),
  `TestSignRouteRefusesADigestThatIsNotThirtyTwoBytesOfLowercaseHex`,
  `TestSignRouteRefusesAnUnknownFieldAndAnOversizeBody`,
  `TestSignRouteRefusesARunThatHasEnded` (F11), `TestSignRouteRefusesAReviewAccountWithNoKeyYet` (fail
  closed, `state_unavailable`, never a signature under something else),
  `TestSignRouteRefusesACodingCallerAndAnUnadmittedClass`,
  `TestSignRouteSignsOnceAndRefusesTheSecondTime` (D1074, `request_refused`, and no second signature),
  `TestSecondSignIsLoggedWhereAnOperatorCanAlertOnIt` (D1074's security event),
  `TestSignRouteRefusesWhenTheMarkCannotBeTaken` (the service does not sign what it cannot mark),
  `TestReviewRoutesNeverPrintEitherIdentityToken` (D1074's redaction),
  `TestSignRouteAuditsTheActAndNeverTheDigest`,
  `TestSignLineCarriesACredentialAndNoTier` (the sign purpose's `checked` exemption, `audit.go:442`/`:448`:
  the key has no tier, and a tier on the sign line is still refused),
  `TestServiceHasNoAdminRoutes` (extended), `TestValidPurposeNamesTheReviewSignPurpose`.

## C3.9f. The verify route and its caller class (D1068, F7, F12). **SEC**

`POST /v1/review/verify` takes `{account, digest, signature, run, repository}` and answers
`200 {"valid":true|false}`, or a **typed refusal** when the service cannot answer at all — the distinction
the design insists on, because a bare `false` tells a coordinator that a verdict is not ours when the truth
is that we could not tell. The `run` and `repository` fields are the frame's (D1073): the caller names them
because the service recomputes the HMAC over them, and naming them cannot make a signature valid that was
not made for them.

- **The route is answered ahead of `RequestFromHTTP`, and that is the one place this task changes
  `ServeHTTP`'s shape rather than adding a row to it.** `RequestFromHTTP` (`wire.go:842`) parses the two
  identity headers and `Verify` (`auth.go:367`) refuses a request with no run identity, **before**
  `ServeHTTP` ever branches on the route (`server.go:450`–`:460`) — and a verifier holds no run identity.
  So the verify path is recognised **first**, the one Google ID token it carries is verified on its own, and
  its refusal is the verifier class's own refusal with **its own audit line**.
  `TestVerifyRouteIsAnsweredAheadOfTheSharedParse` is the test that holds the ordering, and a route left
  behind the shared parse is a route no verifier can reach.
- **That own read is also where the redaction is registered, and this task names its own test for it**
  (§5.6, D1074): the route's `RequestFromHTTP` never runs, so the per-call scrub `readCall`
  (`server.go:530`) builds is never built for it either — the handler therefore registers the one Google ID
  token it carries, and only that one, with the same call-scoped scrub the model path and the sign route
  use. `TestVerifyRouteRegistersItsOwnIdentityTokenForRedaction` asserts neither the verifier's token nor
  its signature reaches the answer, the audit line, a log line or the text of a refusal;
  `TestReviewRoutesNeverPrintEitherIdentityToken` (shared with C3.9e) drives the shared parse's routes and
  is not a substitute, which is why this one exists.
- **The caller class is configuration and it is empty by default** (D1068, F12): the request carries a
  Google ID token whose verified `email` is in `AuthConfig.VerifierAccounts`, checked by the same **token
  check** as a job token — audience, issuer, `email_verified`, expiry, not-issued-in-the-future, ASCII fold
  — minus the shared-job gate (a verifier is not a job of this installation's job set at all). The list is
  empty until someone is put in it, so an installation with no coordinator has no reachable route; the
  coordinator's own account joins it when that work stream deploys.
- **The comparison is constant-time**: the recomputed HMAC is compared with the presented one through
  `hmac.Equal` (`crypto/hmac`), never `==` on the hex strings — the signature is an attacker-supplied value
  and its rejection must not leak a prefix through timing. `TestVerifyComparesTheSignatureInConstantTime` is
  the test that names the rule (it asserts the comparison helper is reached with equal-length inputs, and
  that a same-length wrong signature is a plain `valid:false`).
- **The verify line carries the verifier as its caller and the key as its credential** (§5.5), and it is the
  purpose **three** of `checked`'s rules move for. `Caller` is the verified `email`, `Credential` is the
  review-signing key's bundle entry name, and the key is a signing key and not a model credential of the
  cascade, so the line carries **no tier**: the both-or-neither rule (`audit.go:448`) refuses a line naming a
  credential without one for every purpose but `PurposeMint`, and C3.9e grows that exemption to the sign
  purpose — this task grows it **once more, to the verify purpose and no further**, so that `:442`'s arm
  keeps refusing a tier on all three and the change adds no way to record a tier the service never assigned.
  `TestVerifyLineCarriesACredentialAndNoTier` is the test that holds it, beside a case that asserts the rule
  still refuses the bare-credential shape for a job purpose. The line has **no** `Job` either — the field
  set's "job and caller together" rule (`audit.go:438`) carries `Caller` without `Job` for this purpose
  only, and no other purpose may — and **no `Run` and no `Repository`**, with the `StatusOK` requirement that
  a line names both (`audit.go:462`–`:473`) relaxed the same way and no further: a verifier is not a run,
  holds no run identity and is not a job of this installation, so the line's act is the purpose, the
  credential and the status. `TestVerifyLineCarriesTheCallerWithoutAJob` holds the second and
  `TestVerifyAnswerIsAuditedWithoutARunOrAJob` the third, each beside a case that asserts the rule still
  refuses the relaxed shape for a job purpose.
- **Files (8):** `internal/credsvc/verdict.go` (the handler, the request and answer shapes, the frame
  recompute through C3.9e's builder, the constant-time compare), `internal/credsvc/server.go` (`VerifyPath`,
  `routeAct`'s row, the path answered ahead of `RequestFromHTTP`), `internal/credsvc/wire.go` (the request's
  two new fields and their bounded shapes), `internal/credsvc/auth.go` (`AuthConfig.VerifierAccounts`, the
  verifier branch — one branch, not a second verifier), `internal/credsvc/audit.go`
  (`PurposeReviewVerify`, the credential-without-tier exemption grown to this purpose as the third case of
  the shape the mint and the sign purposes already are, the caller-without-job rule and the
  `StatusOK`-without-run rule, all three for this purpose only), `internal/credsvc/verdict_test.go`,
  `internal/credsvc/auth_test.go`, `internal/credsvc/audit_test.go`. The list itself is **configured** —
  C3.9g2 wires the variable — and this task's tests build the config directly, so nothing here needs
  Terraform.
- **Red step:** `TestVerifyRouteAnswersValidForTheRealPairAndFalseForTheTamperedOne` fails while the route
  is a 404; `TestVerifyRouteRefusesAnEmptyVerifierList` fails while an unconfigured installation still
  answers a caller at all.
- **Tests:** `TestVerifyRouteAnswersValidForTheRealPairAndFalseForTheTamperedOne`,
  `TestVerifyRouteAnswersATypedRefusalWhenTheKeyIsUnreadable` (never `valid:false`),
  `TestVerifyRouteRefusesAnAccountTheCallerMayNotAskAbout` (the account name is held to the bundle's
  credential-name rule), `TestVerifyRouteDoesNotEchoTheAccountOrAKeyID`,
  `TestVerifyRouteRefusesASignatureMadeForAnotherRunOrRepository` (D1073: the frame, refused by the service
  with `valid:false` and no coordinator check),
  `TestVerifyRouteIsAnsweredAheadOfTheSharedParse`,
  `TestVerifyComparesTheSignatureInConstantTime` (F7's constant-time rule),
  `TestVerifyLineCarriesACredentialAndNoTier` (§5.5's first rule: the key's name with no tier, and a job
  purpose that still carries neither),
  `TestVerifyLineCarriesTheCallerWithoutAJob` (§5.5's field-set rule),
  `TestVerifyAnswerIsAuditedWithoutARunOrAJob` (the second relaxation: a `StatusOK` line with no `Run` and
  no `Job`, for the verify purpose only),
  `TestVerifyRouteRefusesACallerOutsideTheVerifierList`,
  `TestVerifierIsNotGatedByTheSharedJobRule` (a verifier is not a job),
  `TestReviewAccountCannotUseTheVerifyRoute`, `TestVerifyRouteRefusesAnUnknownFieldAndAnOversizeBody`,
  `TestReviewRoutesNeverPrintEitherIdentityToken` (shared with C3.9e, and it drives the shared parse's
  routes rather than this one),
  `TestVerifyRouteRegistersItsOwnIdentityTokenForRedaction` (the verify path's own token, registered by its
  own read because `readCall` never runs for it — §5.6),
  `TestVerifyRouteAuditsTheActAndNeverTheDigestOrTheSignature`.

## C3.9g1. The verifier list's variable, its types and its tests (D1063, D1070, F2)

The one deployment change in the whole plan, and it is inert: a new `verifier_accounts` beside the service's
other variables, **null by default**, so an installation that never sets it is untouched — and still starts
the service it starts today, because the name is read as an **optional** value (the allow-list bullet below).
Its second half is
C3.9g2, and the two are **one pull request and not two merges**: `TestCredentialsServiceEnvIsTheDeploymentsOwn`
(`internal/cli/credentials_serve_test.go:386`) holds the module's own `FUGARO_*` names
(`deploy/terraform/gcp/modules/installation/credentials.tf`) against the CLI's lists in **both** directions —
the seven **required** names and the one **optional** name C3.9g2 adds (the next section says why the two
lists are two) — so a merge that carried one side only would leave the tree red between them. The split is
for review size, not for sequencing.

- **The variable, and the entry is conditional on it.** `variable "credentials_service"`
  (`deploy/terraform/gcp/modules/installation/variables.tf:336`) and the root's own pass-through
  (`deploy/terraform/gcp/roots/installation/variables.tf:184`) gain the list, and
  `credentials.tf` renders it as one more `env` entry beside the seven it sets
  (`deploy/terraform/gcp/modules/installation/credentials.tf:398`–`:448`) **only when the field is set** —
  which is what the two halves of §9's claim rest on: an installation that never sets it renders the same
  seven names and its plan is byte-for-byte unchanged, while the fixture that does set it plans **eight**.
- **The type and the writer.** `CredentialsServiceSpec` gains the field
  (`internal/infra/tfvars.go:180`) and `Credentials()` fills it (`:289`) from the configuration it already
  reads the service's other fields from — the source `fugaro update` holds and publishes as the policy, the
  same one `CredentialsJobAccounts` (`:343`) reads the job table from — so the value is not an operator's
  new act. **D1075 is answered** (2026-11-01) as exactly this derivation — the published policy, so **no
  operator step** — and it records the two rejected readings with their costs.
- **One allow-list grows by exactly one, and it refuses a name it does not know**:
  `internal/infra/tf/cover.go`'s `credentialsServiceEnv` (`:792`), which is what one `fugaro update`
  confirmation covers and which must name the entry the module can now render.
  **`internal/cli/credentials_serve.go`'s `credentialsServiceEnvNames` (`:177`) does not grow, and this is
  the one place the design could have shipped a service that never starts**: that list is the names the
  service **requires** — `credentialsServiceConfig` (`:281`–`:285`) refuses to bind a port if any of them is
  empty — and the verifier list is **null** on every installation that has not opted in, so a name added to
  it would turn "an installation that never sets the field" into "a service that refuses to start". The
  eighth name is therefore read as an **optional** value, outside that loop (C3.9g2), where an absent or
  empty value is an empty verifier list. Check 96's own sentence — "the seven names the config builder
  reads" — stays true, and so does the guard that reads it
  (`internal/cli/docs_check96_test.go:436`–`:437`).
- **The tests that hold it, and why they move together.** `TestGoldenCredentialsServiceCarriesItsSevenNames`
  (`internal/infra/tf/credentials_golden_test.go:95`) compares the golden plan's environment to
  `credentialsServiceEnv` in **both** directions, so the fixture that sets the field
  (`internal/infra/testdata/installation-credentials.tfvars.json`) makes the plan carry eight names and the
  test's `want` map — and the test's own name, which counts them, from
  `TestGoldenCredentialsServiceCarriesItsSevenNames` to `TestGoldenCredentialsServiceCarriesItsNames` —
  grows with it. The golden plan
  (`internal/infra/tf/testdata/golden/installation-credentials.plan.json`) is regenerated with
  `scripts/gen-golden-plans.sh` rather than hand-edited. The tftest's exact env map
  (`deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`, the `assert` that holds the whole
  environment as a map and whose message and comment today refuse an eighth entry) gains the name — and the
  sentence that counts the names — in the run that **sets** the field, while its unset run keeps its own
  `length(…) == 7` assertion — the byte-for-byte half held as a test rather than asserted in prose.
- **Files (9):** `deploy/terraform/gcp/modules/installation/variables.tf` (the variable),
  `deploy/terraform/gcp/roots/installation/variables.tf` (its pass-through),
  `deploy/terraform/gcp/modules/installation/credentials.tf` (the conditional `env` entry),
  `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl` (the exact map, and the unset
  run's seven), `internal/infra/tfvars.go` (the field and the writer), `internal/infra/tf/cover.go` (the
  allow-list), `internal/infra/tf/credentials_golden_test.go` (the map, and the test's name),
  `internal/infra/testdata/installation-credentials.tfvars.json` (the fixture that sets the field) and
  `internal/infra/tf/testdata/golden/installation-credentials.plan.json` (regenerated).
- **Red step:** `TestGoldenCredentialsServiceCarriesItsNames` fails while the cover's allow-list still holds
  seven names and the plan carries eight, and the tftest's exact-map assertion fails while the module renders
  seven where the run's variables set the field.
- **Tests:** `GOFLAGS=-p=2 go test ./internal/infra/tf/ -run 'Credentials' -count=1`; `go test -tags terraform
  ./internal/infra/...`; and `terraform fmt -check -recursive deploy/terraform` with the installation root's
  own `validate` and `test`, which is where the tftest runs.

## C3.9g2. The `credentials_serve` wiring, the amendment, the docs, and the named live check (D1063, D1070, F2)

C3.9g1's half on the CLI side, and the documents. It lands with C3.9g1, for the reason stated there.

- **The wiring, and the eighth name is optional.** `internal/cli/credentials_serve.go` reads the verifier
  list as an **optional** value **outside** `credentialsServiceEnvNames` (`:177`) — the loop that requires
  every name it holds (`credentialsServiceConfig`, `:281`–`:285`) — and an absent or empty value is an
  **empty verifier list**, which is the state §5.4 already designs for: the route answers every caller with
  the class's typed refusal and the service starts. `credentialsServiceEnvNames` therefore stays the seven
  required names, and the comments that count them (`:54`, `:168`, `:208`, `:275`) stay true for that list —
  they describe the names this command **requires**, which are still the seven `credentials.tf` always sets;
  the comment that says an empty variable is "a deployment that is wrong rather than a deployment with a
  default" (`:277`–`:279`) gains the one sentence it now needs, that the verifier list is the name the
  service reads as optional and the rest are not. Three tests hold it: `TestCredentialsServeStartsWithoutTheVerifierList`
  is the new one — the service binds a port with the name absent, and the route answers the class's refusal
  rather than the process failing — `TestCredentialsServeRefusesWithoutItsEnvironment`
  (`internal/cli/credentials_serve_test.go:100`) keeps the refusal for each of the **seven**, and does
  **not** require the new name, and `TestCredentialsServiceEnvIsTheDeploymentsOwn` (`:386`) compares the
  module's `FUGARO_*` names against the **required names plus the optional one**, in both directions.
- **`fugaro update` owes nothing new**, and the release notes say so rather than leaving it to be inferred:
  no Terraform for the key, no new container, no new grant, and nothing beyond the service's own documented
  `creds set` for the key's bytes (§9 of [spec.md](spec.md)). The variable defaults to null, so an
  installation that never sets it is untouched, and the service reads that name as an **optional** value, so
  the same installation starts with an **empty verifier list** — a route that refuses every caller — rather
  than a process that refuses to start; whoever wants the verify route reachable gets the value from
  the published policy and runs the one command, which is the HARD RULE's shape.
- **The live-check register row is not this design's, and the check's file already exists as a stub.**
  `docs/gcp-live-checklist.md` is on the do-not-touch list of the release window in flight (0.15.0's while
  this was written, and 0.16.0's now that D1072 answers the slot), so **this design PR writes no register
  row, edits no file outside `docs/c39-review-identity/` and takes no number**; the row is a merge-day act
  or a follow-up run's, and many `internal/cli/docs_*_test.go` pins read that file, so moving the row is not
  a one-line edit. What the design does fix is the row's **shape**, so that whoever writes it copies one
  rather than deriving it: [live-check.md](live-check.md) holds the register's own convention read off
  Check 190 (the heading and its scope parentheses, the `NOT RUN` paragraph that carries no steps of its
  own, and the four sections after it) and §8's eleven steps one line each under §8's own numbering — so
  this task adds nothing to that file but the number it takes and the row it puts in the register.
- **Files (4):** `internal/cli/credentials_serve.go` (the optional read, the config field and the one comment
  it needs), `internal/cli/credentials_serve_test.go` (the new start test, and the two the change touches),
  `docs/credentials-service/implementation-plan.md` (the two edits
  of §"The amendment the implementing pull request carries"), and `docs/c39-review-identity/live-check.md`
  (in this folder already, as the design's **stub** of the register row — its shape and §8's eleven steps
  one line each — which that task fills in with the number it takes at its merge; §8 of [spec.md](spec.md)
  is the check itself).
- **Red step:** `TestCredentialsServeStartsWithoutTheVerifierList` fails while the name is one the config
  requires — the process refuses to start with it absent — and `TestCredentialsServiceEnvIsTheDeploymentsOwn`
  fails while the module's names and the CLI's two lists disagree. The plan amendment has no red test of its
  own — it is prose in a file whose facts `TestPlanTestListsNamesOnlyTestsThatExist`
  (`internal/cli/docs_credentials_plan_tests_test.go`) parses, and which must stay green; the live check is
  red by being **NOT RUN** until CSR7 and CSR2 exist, which is the state the check itself records.
- **Tests:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs|Backlog' -count=1 -timeout 40m`;
  `GOFLAGS=-p=2 go test ./internal/cli/ -run 'CredentialsServe|CredentialsServiceEnv' -count=1`; and
  `GOFLAGS=-p=2 go test ./internal/cli/ -run 'PlanTestListsNamesOnlyTestsThatExist|DocsCredentials' -count=1`.

---

## The amendment the implementing pull request carries

C3.9a narrows Global Constraint 6's scope in **code**; the plan that owns the sentence is amended in the
same pull request, by C3.9g2, and the amendment is **two edits and no more**:

1. **Global Constraint 6** (`docs/credentials-service/implementation-plan.md:111`) keeps its sentence for
   the jobs it was written for — "per-repository jobs only until the phase-3 shared-job rules are on `main`"
   — and gains the scope: the **review** class is admitted on the **mint**, the **sign** and the **model
   path for its own account only** (ruling O1's amendment to D1063: the pairing resolves one account, tier A
   only, and the review variant mounts no model token), while a **coding** run in a shared project job is
   refused exactly as it is today.
2. **C3.9** (`:674`) gains one sentence naming this folder as the review half that shipped and the general
   half that did not (D1070, [spec.md](spec.md) §10), so a reader who follows C3.9's own line learns what is
   done and what is still owed.

Nothing here edits the credentials-service plan **now**: an amendment written in advance of the code is
either an amendment nobody applies or a second copy of a plan. The amended sentences name **tests that
exist** when they land, because `TestPlanTestListsNamesOnlyTestsThatExist`
(`internal/cli/docs_credentials_plan_tests_test.go`) parses that file and refuses a name it cannot find.

## Shared files

Files more than one task edits, and what each does to them. A task that finds itself in one of these for a
different reason has taken a wrong turn.

| File | Tasks | What each writes |
| --- | --- | --- |
| `internal/credsvc/server.go` | C3.9a, C3.9e, C3.9f | a: `Config.ReviewAccounts`, `Config.Repos`, the pairing at `:476` and the `candidates` call at `:581` (which stops reading `ja.Repo`, because a review caller has no `ja`); e: `SignPath`, `routeAct`'s row, the create-only writer on `Config`; f: `VerifyPath`, `routeAct`'s row, and the path answered ahead of `RequestFromHTTP` |
| `internal/credsvc/cascade.go` | C3.9a | the offering rule for a review caller: `candidates` (`:33`) offers the pairing's one account, tier A, and no other account and no tier B/C; `admittedAccounts` (`:87`) is reused as the pairing's admission check rather than re-implemented |
| `internal/credsvc/auth.go` | C3.9a, C3.9f | a: the class on `Identity`, `AuthConfig.ReviewJobAccounts`, `verifyJob`'s answer; f: `AuthConfig.VerifierAccounts` and the verifier branch |
| `internal/credsvc/wire.go` | C3.9f | the verify request's `run` and `repository` fields and their bounded shapes |
| `internal/credsvc/review.go` | C3.9a, C3.9b | a: creates it — the class, the table, the claim rule; b: the set chooser beside the class |
| `internal/credsvc/verdict.go` | C3.9e, C3.9f | e: creates it — the frame builder, the sign handler and the mark; f: the verify handler, the constant-time compare and the caller class's own shape checks |
| `internal/credsvc/audit.go` | C3.9e, C3.9f | one purpose each, `ValidPurpose` in the first of the two, and the credential-without-tier exemption of `checked` (`:442`, `:448`) grown to the sign purpose and, by f, once more to the verify purpose; f carries the verify purpose's other two relaxations as well — `Caller` without `Job` (`:438`) and a `StatusOK` line with no `Run` and no `Job` (`:462`–`:473`) |
| `internal/credsvc/bundle.go` | C3.9c, read by C3.9e/C3.9f | c: the kind, the key set, the accessor; the routes only call the accessor |
| `internal/cli/credentials_serve.go` | C3.9a, C3.9e, C3.9g2 | a: the review set and the repository table; e: the create-only writer's wiring; g2: the verifier list's **optional** read and the config field, outside `credentialsServiceEnvNames`, which stays the seven required names |
| `internal/cli/creds.go` | C3.9c, C3.9d | c: `creds ls` names the review-signing entry and its kind, and never a value; d: the `--generate` flag, the bytes generated in the process, the two-entry write and `creds set`'s help text. Different subcommands and different regions of the file, so the two edits do not collide — the row is here because a reader checking who owns a change in this file should not have to open both tasks |
| `internal/credsvc/*_test.go` | many | each task adds its own file where it can; the four existing files it extends (`auth_test.go`, `sharedjobs_test.go`, `forward_test.go`, `bundle_test.go`) are named in the task |

## Live checks

One, and it is **named and not numbered** — the shape [docs/run-limit-carry-over/README.md](../run-limit-carry-over/README.md)
chose on 2026-11-01: the register's highest **written** section is **Check 190**, open pull requests claim
above it, and the register's own rule is that a claim above the highest written section is settled at its
merge. So the task that writes this check (C3.9g2) takes the number free at **its** merge. The check is
[spec.md](spec.md) §8: **eleven steps**, owner-run, **no agent performs any step of it**, and **NOT RUN**
until C3.9a–C3.9g2 ship and the review work's **CSR7** and **CSR2** are on `main` — because step 3 launches a
review with `fugaro review --pr N`, which is CSR2, on the variant CSR7 deploys.

Step 8 is the one that needs a configuration act rather than a fact, and the check says so: the owner's own
identity is put in the verifier list (C3.9g1's variable, which C3.9g2's wiring reads) and the installation is
converged by the one documented command, which is what makes the hand-made verify call possible. **Step 9 is U7's property,
observed after the route exists** (D1074): a sign attempt and a metadata-server request from inside a mutant
subprocess must both fail, and the check says the owner re-runs the pre-build observation against the
shipped image rather than trusting it. Cost: one sandbox review run's spend, the artifact kept, and nothing
created, enabled or deleted outside the sandbox.

## Release slot

- **This design** merges whenever the owner answers it; it carries no code and nothing depends on it merging
  early except the plan's own review.
- **Implementation is 0.16.0, and before CSR7 and CSR2 in the same window** (D1072; the recommendation was
  0.15.0 and the owner moved it). CSR7 is the
  variant whose account C3.9a admits, CSR2 is the command that launches it, and this work is what makes the
  launch possible: a release that shipped CSR7 with **no** service admission would ship exactly the failure
  E7 and D893 (c) exist to prevent. The order inside the window is C3.9a–C3.9g2, then CSR7 and CSR2.
- **`fugaro update` carries it whole.** An opted-in installation converges with the one documented command:
  the image pin moves, the classes and the routes arrive in the code, and the key is the credentials
  service's own documented `creds set` step, named by `doctor` and by the service's refusal when it is
  missing. Nothing in this plan asks an operator to run anything else, and no task adds an operator step to
  a release's `### For operators` — the one variable C3.9g1 adds is null by default.
