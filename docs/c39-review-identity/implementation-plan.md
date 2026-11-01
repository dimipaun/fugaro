# C3.9's review half: implementation plan

**Nothing here is approved to build, and no decision is answered.** This plan is written to the
*recommended* answer of each decision in [reconciliation.md](reconciliation.md) §3 (D1062–D1072); a decision
the owner answers the other way changes the tasks that cite it and the spec paragraph they implement. Each
task touches at most 10 files, names its tests, starts with a red step, and ends at the gates below.
[docs/credentials-service/implementation-plan.md](../credentials-service/implementation-plan.md) keeps
**C3.9** for the general case; this plan is the review half of it and carries the scope amendment in
§"The amendment the implementing pull request carries".

## Dependencies

Four things this plan does **not** own. **R1 is the one that blocks a task**; the other three are facts the
tasks are written to.

- **R1, the read-only permission set** (`docs/cloud-senior-review/`, `internal/gitprov/github/apptoken.go`):
  C3.9b **serves** that set and does not declare a second spelling of it. This is the one blocking
  dependency, and it is another work stream's.
- **R7, the review-only job variant**: its account's ID spelling is R7's, and C3.9a computes the service's
  review set **through the same helper the deployer uses for it**
  (`docs/cloud-senior-review/implementation-plan.md` R7; [spec.md](spec.md) §2.2). Two requirements this
  plan depends on and does not write: the variant has its **own** service account — not the coding job's,
  which would put one email in two classes and leave the class rule with nothing to decide — and the
  variant's environment carries `FUGARO_CREDENTIALS_SERVICE_URL` (finding **F14**; `internal/cli/run.go`'s
  `credentialsServiceLaunchEnv` deliberately does not reach a shared job).
- **E7, the service enabled for the installation**: `fugaro update --provision-credentials`, the App key in
  the bundle, and the review variant deployed. C3.9a–C3.9g change nothing about that deployment; they make
  the service **admit** what E7 deploys.
- **The coordinator** (`docs/coordinator/`): the verify route is built and its caller class configured
  empty. The component that calls it, and the merge path that acts on a verified verdict, is that work
  stream's, and no task below adds one (X1).
- **Naming a file that other work owns.** `internal/gitprov/github/apptoken.go` (R1),
  `internal/infra/projectjobs.go` and the module that deploys the variant (R7), and everything under
  `ee/`, `console/`, `mobile/`, `website/`, `plugin/` are **read, not changed** by this plan.

### Order

| Level | Tasks | Needs |
| --- | --- | --- |
| 0 | C3.9a, C3.9c | nothing (each reads a derivation another work stream owns) |
| 1 | C3.9b | C3.9a, and **R1 merged** (the set it serves) |
| 2 | C3.9d, C3.9e | C3.9c (the entry's name, kind and read path); C3.9e also C3.9a |
| 3 | C3.9f | C3.9a, C3.9c |
| 4 | C3.9g | C3.9e, C3.9f; the live check it names is not run until **R7 and R2** are on `main` |

## Where each decision lands

| Decision | Task | Decision | Task |
| --- | --- | --- | --- |
| D1062 the third account class | C3.9a | D1068 the verify route's caller class | C3.9f |
| D1063 Global Constraint 6's narrowed scope | C3.9a (code), and §"The amendment the implementing pull request carries" | D1069 where the key lives | C3.9c |
| D1064 the pairing for a project-scoped caller | C3.9a | D1070 what the slice does not do | §"The amendment the implementing pull request carries" (its second edit), and nothing in the code |
| D1065 which token set | C3.9b | D1071 who generates the bytes | C3.9d |
| D1066 the repository, and the table | C3.9a (the table), C3.9b (the mint's use) | D1072 the release slot | §"Release slot" |
| D1067 what is signed | C3.9e | | |

## Where each finding lands

| Finding | Task | Finding | Task |
| --- | --- | --- | --- |
| F1 no account class | C3.9a | F8 D892 vs Q6's one container | C3.9c |
| F2 the gate's text covers the variant | C3.9a, C3.9g | F9 the bundle's closed key set | C3.9c |
| F3 the mint's repository is the account's | C3.9b | F10 the write path, and the missing generating mode | C3.9d |
| F4 no slug → canonical resolution | C3.9a | F11 signing is an end-of-run act | C3.9e |
| F5 the set is hard-coded, R1 is missing | C3.9b | F12 no verifier class | C3.9f |
| F6 the claims are already verified | C3.9b | F13 O4's admissions reader exists | C3.9a |
| F7 every closed set the routes touch | C3.9e, C3.9f | F14 the launch wiring is R7's | no task — a dependency |

## Gates (every task)

`go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); the task's named tests with
`GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1`; `GOFLAGS=-p=2 go test ./internal/cli/ -run
Docs -count=1 -timeout 40m` when a doc changed; `go test ./internal/eeboundary/... -count=1`; and
`go test -tags terraform ./internal/infra/...` in C3.9f, which changes a variable. Never the whole
`internal/cli` or `internal/runner` package. No task adds a dependency, so `go.mod` is unchanged by all
seven.

## C3.9a. The review class, the repository table and the pairing (D1062, D1063, D1064, D1066, F1, F2, F4, F13). **SEC**

The service gains a third account class and the one thing the other two do not need: a way to pair a caller
with a run when the caller's account names no repository. The gate is **narrowed**, not lifted: `verifyJob`
keeps refusing the shared set with today's sentence, and the review set is refused on every route except the
two C3.9b, C3.9e and C3.9f add.

- **Files (7):** `internal/credsvc/review.go` (new: `ReviewJobAccount{Email, Account}` — the account
  **name** is what the published admissions speak and the email is what the service is called as;
  `ReviewJobAccountsFor(p *projectpolicy.Policy, gcpProject string) []ReviewJobAccount`;
  `RepositoryTableFor(fugaroProject, gcpProject, provider string, jobs []RepoJob) (map[string]string, error)`
  over the same input `JobAccountsFor` takes; `ClassRepository`/`ClassShared`/`ClassReview` and `ReviewOf(id
  Identity) error` — the claim rule of [spec.md](spec.md) §3.3), `internal/credsvc/auth.go` (the class on
  `Identity`, `verifyJob` answering it, `AuthConfig.ReviewJobAccounts` beside `SharedJobAccounts`, the
  disjointness refusal beside the "in both sets" one), `internal/credsvc/server.go` (`Config.ReviewAccounts`
  and `Config.Repos`, the pairing in the binding check at `:476` — the class decides which of the two
  pairings is made), `internal/cli/credentials_serve.go` (the review set built from the policy the service
  already reads, and the repository table from `FUGARO_JOB_ACCOUNTS`' own input), `internal/credsvc/review_test.go`
  (new), `internal/credsvc/sharedjobs_test.go`, `internal/credsvc/auth_test.go`.
- **One derivation, one input, one test.** `ReviewJobAccountsFor` reads the **published policy** — so an
  installation that publishes no profile admits no review job — and derives each email through the same
  `serviceAccountEmail` (`server.go:178`) over the variant's ID spelling, which R7 owns;
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
- **Red step:** `TestReviewAccountIsAdmittedAndIsRefusedTheModelRoutes` fails at its first assertion while
  the class does not exist: the review caller is refused with "the caller is not one of the installation's
  job service accounts", which is the state this task's subject exists to change.
- **Tests:** `TestReviewAccountIsAdmittedAndIsRefusedTheModelRoutes`,
  `TestReviewAccountsAreTheNamesProjectJobsDeploys`, `TestSharedAndReviewSetsAreDisjoint`,
  `TestSharedJobCallerIsStillRefusedOnEveryRoute` (F2 — today's behaviour, unchanged),
  `TestReviewCallerOnAnUnpublishedRepositoryIsRefused` (F13, fail closed),
  `TestRepositoryTableResolvesASlugToItsCanonicalName`, `TestRepositoryTableRefusesAnUnknownSlug` (F4),
  `TestReviewIdentityRequiresNoBranchAndAtLeastOnePR`,
  `TestReviewIdentityWithABranchIsRefused` (a coding run in the review job is refused, never downgraded),
  `TestReviewIdentityWithAnEmptyPRListIsRefused` (an artifact run is not a review),
  `TestClassIsDecidedFromTheVerifiedEmailNotTheHeader`.

## C3.9b. The mint: R1's set, the run's repository, and the order of the refusals (D1065, D1066, F3, F5, F6). **SEC**

The mint keeps its shape — one App ID, no repository from the caller, one permission set for one repository
(F3) — and gains two decisions made on the verified side: **which repository** (the run identity's `fs`
resolved through C3.9a's table) and **which set** (R1's read-only set for a review caller, the run set for a
repository caller, and a refusal for any other shape).

- **Files (4):** `internal/credsvc/github.go` (the set chosen from the class and the claim rule, the
  repository from the table, the refusal order documented where the branch is), `internal/credsvc/review.go`
  (the set chooser, beside the class it reads), `internal/credsvc/github_test.go`,
  `internal/credsvc/review_test.go`. **Read, not changed:** `github.MintInstallationToken` and,
  once R1 lands, the exported read-only set in `internal/gitprov/github/apptoken.go`.
- **The refusal order is part of the contract**, because it decides what an audit line says and which
  refusal a caller reads first: unverified identities → class not admitted to the mint → the stop flag →
  pairing → the claim rule → the bundle. The stop flag is where it is because `ServeHTTP` already reads it
  before it branches on the route (`server.go:440`), and the order is not rearranged to suit the new class.
  A review caller that fails the pairing or the claim rule is refused **before** the bundle is read, so a
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
- **Files (4):** `internal/cli/creds.go` (the flag, the generation, the two-entry write, the help text),
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
  `TestCredsSetGenerateKeepsEveryOtherEntryByteForByte`,
  `TestCredsSetGenerateIsRefusedInACodingAgentsSession` (the existing refusal, honoured by the new mode),
  `TestCredsSetGenerateRefusesANameOutsideTheReviewSigningFamily` (the mode is not a general generator).

## C3.9e. The sign route (D1067, F7, F11). **SEC**

`POST /v1/review/sign` takes a digest and answers the HMAC of its 32 bytes under the account's key,
lowercase hex, with the algorithm named in the answer. What it deliberately does not do is in
[spec.md](spec.md) §5.2, and the design's honesty about the binding (`D1067`, X3) is repeated in the
handler's own comment.

- **Files (6):** `internal/credsvc/verdict.go` (new: `SignPath`'s handler, the strict 4 KiB body decode,
  the answer, the key read through C3.9c's accessor, the HMAC and its hex rendering),
  `internal/credsvc/server.go` (`SignPath` beside the three paths at `:33`, `routeAct`'s row), 
  `internal/credsvc/audit.go` (`PurposeReviewSign`), `internal/credsvc/verdict_test.go` (new),
  `internal/credsvc/forward_test.go` (`TestServiceHasNoAdminRoutes` `:319` naming **five** paths, so a sixth
  is a test failure and not a discovery), `internal/credsvc/audit_test.go`.
- **The line carries the verified side and never the digest** (§5.5): the account, the repository slug, the
  run and the pull requests come from the identity the service verified; the digest, the signature and the
  key's bytes reach no field, because a field that is a caller's bytes is what the closed field set exists
  to keep out.
- **Signing is an end-of-run act** (F11): `checkLive` already refuses a run whose outcome is written, and
  the handler does not weaken it. A killed run leaves an unsigned artifact and no re-signing path exists.
- **Red step:** `TestSignRouteAnswersAnHMACOfTheDigestUnderTheAccountsKey` fails while the route is a 404:
  the path is not in `ServeHTTP`'s set, which is how D143's "no admin endpoint" is kept.
- **Tests:** `TestSignRouteAnswersAnHMACOfTheDigestUnderTheAccountsKey`,
  `TestSignAnswerNamesTheAlgorithmAndIsLowercaseHex` (D892's answer shape),
  `TestSignRouteRefusesADigestThatIsNotThirtyTwoBytesOfLowercaseHex`,
  `TestSignRouteRefusesAnUnknownFieldAndAnOversizeBody`,
  `TestSignRouteRefusesARunThatHasEnded` (F11), `TestSignRouteRefusesAReviewAccountWithNoKeyYet` (fail
  closed, `state_unavailable`, never a signature under something else),
  `TestSignRouteRefusesACodingCallerAndAnUnadmittedClass`,
  `TestSignRouteAuditsTheActAndNeverTheDigest`,
  `TestServiceHasNoAdminRoutes` (extended), `TestValidPurposeNamesTheReviewSignPurpose`.

## C3.9f. The verify route and its caller class (D1068, F7, F12). **SEC**

`POST /v1/review/verify` takes `{account, digest, signature}` and answers `200 {"valid":true|false}`, or a
**typed refusal** when the service cannot answer at all — the distinction the design insists on, because a
bare `false` tells a coordinator that a verdict is not ours when the truth is that we could not tell.

- **The caller class is configuration and it is empty by default** (D1068, F12): the request carries a
  Google ID token whose verified `email` is in `AuthConfig.VerifierAccounts`, checked by the same code path
  as a job token minus the shared-job gate (a verifier is not a job of this installation's job set at all).
  The list is empty until someone is put in it, so an installation with no coordinator has no reachable
  route; the coordinator's own account joins it when that work stream deploys.
- **Files (6):** `internal/credsvc/verdict.go` (the handler, the request and answer shapes, the recompute),
  `internal/credsvc/auth.go` (`AuthConfig.VerifierAccounts`, the verifier branch in `verifyJob` — one branch,
  not a second verifier), `internal/credsvc/server.go` (`VerifyPath`, `routeAct`'s row),
  `internal/credsvc/audit.go` (`PurposeReviewVerify`), `internal/credsvc/verdict_test.go`,
  `internal/credsvc/auth_test.go`. The list itself is **configured** — C3.9g wires the variable — and this
  task's tests build the config directly, so nothing here needs Terraform.
- **Red step:** `TestVerifyRouteAnswersValidForTheRealPairAndFalseForTheTamperedOne` fails while the route
  is a 404; `TestVerifyRouteRefusesAnEmptyVerifierList` fails while an unconfigured installation still
  answers a caller at all.
- **Tests:** `TestVerifyRouteAnswersValidForTheRealPairAndFalseForTheTamperedOne`,
  `TestVerifyRouteAnswersATypedRefusalWhenTheKeyIsUnreadable` (never `valid:false`),
  `TestVerifyRouteRefusesAnAccountTheCallerMayNotAskAbout` (the account name is held to the bundle's
  credential-name rule), `TestVerifyRouteDoesNotEchoTheAccountOrAKeyID`,
  `TestVerifyRouteRefusesACallerOutsideTheVerifierList`,
  `TestVerifierIsNotGatedByTheSharedJobRule` (a verifier is not a job),
  `TestReviewAccountCannotUseTheVerifyRoute`, `TestVerifyRouteRefusesAnUnknownFieldAndAnOversizeBody`,
  `TestVerifyRouteAuditsTheActAndNeverTheDigestOrTheSignature`.

## C3.9g. The verifier list's deployment, the amendment, the docs, and the named live check (D1063, D1070, F2)

Two halves: the one place this plan changes a deployment, and the documents.

- **The one deployment change in the whole plan, and it is inert.** The verifier list is set by a new
  variable beside the service's others, which means two allow-lists grow by exactly one and both refuse a
  name they do not know: `internal/cli/credentials_serve.go`'s `credentialsServiceEnvNames` (`:177`) and
  `internal/infra/tf/cover.go`'s `credentialsServiceEnv` (`:792`), which
  `TestGoldenCredentialsServiceCarriesItsSevenNames` (`internal/infra/tf/credentials_golden_test.go:95`)
  holds equal to the golden plan's seven names in both directions. That test becomes *eight*, and the golden
  plan is regenerated with `scripts/gen-golden-plans.sh` rather than hand-edited. It defaults to **null**, so
  an installation that never sets it is byte-for-byte unchanged and `fugaro update` converges it with no
  operator step — which is what §9 of [spec.md](spec.md) claims, and what
  `TestCredentialsServeRefusesWithoutItsEnvironment` (`internal/cli/credentials_serve_test.go:95`) plus the
  golden test must keep true.
- **Files (9):** `internal/cli/credentials_serve.go` (the variable), `internal/cli/credentials_serve_test.go`,
  `internal/infra/tf/cover.go` (the allow-list), `internal/infra/tf/credentials_golden_test.go`,
  `internal/infra/tf/testdata/golden/installation-credentials.plan.json` (regenerated),
  `deploy/terraform/gcp/modules/installation/credentials.tf`, `internal/infra/tfvars.go`,
  `docs/credentials-service/implementation-plan.md` (the two edits of §"The amendment the implementing pull
  request carries"), `docs/c39-review-identity/live-check.md` (new: the check written under the **name** in
  §8 of [spec.md](spec.md), with its "NOT RUN" line and the number left to the merge).
- **`fugaro update` owes nothing new**, and the release notes say so rather than leaving it to be inferred:
  no Terraform for the key, no new container, no new grant, no operator step beyond the service's own
  documented `creds set` for the key's bytes (§9 of [spec.md](spec.md)). The variable defaults to null, so an
  installation that never sets it is untouched; whoever wants the verify route reachable sets it and runs the
  one command, which is the HARD RULE's shape.
- **The live-check register row is not this task's if the ruling stands.** `docs/gcp-live-checklist.md` is on
  the 0.15.0 do-not-touch list, so the check is written in this folder and the register row is a merge-day act
  or a follow-up run's; many `internal/cli/docs_*_test.go` pins read that file, so moving the row is not a
  one-line edit.
- **Red step:** the plan amendment has no red test of its own — it is prose in a file whose facts
  `TestPlanTestListsNamesOnlyTestsThatExist` (`internal/cli/docs_credentials_plan_tests_test.go`) parses, and
  which must stay green; the live check is red by being **NOT RUN** until R7 and R2 exist, which is the state
  the check itself records.
- **Tests:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs|Backlog' -count=1 -timeout 40m`;
  `GOFLAGS=-p=2 go test ./internal/infra/tf/ -run 'Credentials' -count=1`; and
  `GOFLAGS=-p=2 go test ./internal/cli/ -run 'PlanTestListsNamesOnlyTestsThatExist|DocsCredentials' -count=1`.

---

## The amendment the implementing pull request carries

C3.9a narrows Global Constraint 6's scope in **code**; the plan that owns the sentence is amended in the
same pull request, by C3.9g, and the amendment is **two edits and no more**:

1. **Global Constraint 6** (`docs/credentials-service/implementation-plan.md:111`) keeps its sentence for
   the jobs it was written for — "per-repository jobs only until the phase-3 shared-job rules are on `main`"
   — and gains the scope: the **review** class is admitted on the mint and the sign and refused on the model
   routes, while a **coding** run in a shared project job is refused exactly as it is today.
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
| `internal/credsvc/server.go` | C3.9a, C3.9e, C3.9f | a: `Config.ReviewAccounts`, `Config.Repos` and the pairing at `:476`; e: `SignPath` and `routeAct`; f: `VerifyPath` and `routeAct` |
| `internal/credsvc/auth.go` | C3.9a, C3.9f | a: the class on `Identity`, `AuthConfig.ReviewJobAccounts`, `verifyJob`'s answer; f: `AuthConfig.VerifierAccounts` and the verifier branch |
| `internal/credsvc/review.go` | C3.9a, C3.9b | a: creates it — the class, the table, the claim rule; b: the set chooser beside the class |
| `internal/credsvc/verdict.go` | C3.9e, C3.9f | e: the sign handler; f: the verify handler and the caller class's own shape checks |
| `internal/credsvc/audit.go` | C3.9e, C3.9f | one purpose each, and `ValidPurpose` in the first of the two |
| `internal/credsvc/bundle.go` | C3.9c, read by C3.9e/C3.9f | c: the kind, the key set, the accessor; the routes only call the accessor |
| `internal/cli/credentials_serve.go` | C3.9a, C3.9f | a: the review set and the repository table; f: the verifier list variable |
| `internal/credsvc/*_test.go` | many | each task adds its own file where it can; the three existing files it extends (`auth_test.go`, `sharedjobs_test.go`, `forward_test.go`, `bundle_test.go`) are named in the task |

## Live checks

One, and it is **named and not numbered** — the shape [docs/run-limit-carry-over/README.md](../run-limit-carry-over/README.md)
chose on 2026-11-01: the register's highest **written** section is **Check 190**, open pull requests claim
above it, and the register's own rule is that a claim above the highest written section is settled at its
merge. So the task that writes this check (C3.9g) takes the number free at **its** merge. The check is
[spec.md](spec.md) §8: eight steps, owner-run, **no agent performs any step of it**, and **NOT RUN** until
C3.9a–C3.9g ship and the review work's **R7** and **R2** are on `main` — because step 3 launches a review
with `fugaro review --pr N`, which is R2, on the variant R7 deploys.

Step 7 is the one that needs a configuration act rather than a fact, and the check says so: the owner's own
identity is put in the verifier list (C3.9f's variable) and the installation is converged by the one
documented command, which is what makes the hand-made verify call possible. Cost: one sandbox review run's
spend, the artifact kept, and nothing created, enabled or deleted outside the sandbox.

## Release slot

- **This design** merges whenever the owner answers it; it carries no code and nothing depends on it merging
  early except the plan's own review.
- **Implementation is recommended for 0.15.0, and before R7 and R2 in the same window** (D1072). R7 is the
  variant whose account C3.9a admits, R2 is the command that launches it, and this work is what makes the
  launch possible: a release that shipped R7 with **no** service admission would ship exactly the failure
  E7 and D893 (c) exist to prevent. The order inside the window is C3.9a–C3.9f, then R7 and R2.
- **`fugaro update` carries it whole.** An opted-in installation converges with the one documented command:
  the image pin moves, the classes and the routes arrive in the code, and the key is the credentials
  service's own documented `creds set` step, named by `doctor` and by the service's refusal when it is
  missing. Nothing in this plan asks an operator to run anything else, and no task adds an operator step to
  a release's `### For operators` — the one variable C3.9f adds is null by default.
