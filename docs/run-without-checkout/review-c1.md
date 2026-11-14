# C1 — the independent review of A0, A1 and A3

**Status:** the plan's C1 gate (D914), answered 2026-11-14. The verdict is at the end; the
findings are numbered and carry a severity and a `file:line`. This document is the whole
deliverable: no source file, no other document, no plan and no `docs/status.md` row was
touched by the review.

**What was reviewed.** Three landed commits on `main`, in the state `origin/main` holds now
and not only in the state their own commits left:

| | commit | what it is |
| --- | --- | --- |
| A0 | `3761ffac9` (#2200) | `internal/credsvc`: the person read mint asks for the file read it is for |
| A1 | `69688f52f` (#2230) | `internal/gitprov/github`: one file of one repository at one ref, read with the person's read mint |
| A3 | `d51ada6d5` (#2233) | `internal/cli`: a launch without the repository's checkout reads the ref's document through the credentials service |

`fd72893aa` (A2) is context and not under review. This is review level 3 — the credentials
service's rule for a change of its own (`.claude/rules/credentials-service.md`).

**How it was reviewed.** Every claim below was re-derived from the code at `origin/main`
rather than from a commit message, a comment, a test name or the plan's description of the
work: the code was read, the test that pins each property was read, and each security-relevant
property was also attacked with a scratch mutation that was reverted before the next step (the
working tree was confirmed clean after every one). No mutation is committed, and none of the
observations below is a patch. The suites named by the three tasks' plan sections were run one
at a time with `GOFLAGS=-p=1 GOMAXPROCS=2`, never as a whole package:

```
go test ./internal/credsvc        -run 'GitHubRead|Person|Permissions'
go test ./internal/gitprov/github -run 'FileAt|Permissions|AppToken|ServiceRead'
go test ./internal/cli            -run 'ProjectJobAgentAuth|ServiceRead|ReadRepoConfigAtRef|LaunchWithoutCheckout|LaunchWithACheckout|NoCheckout|PersonID'
```

All three are green at `origin/main`.

---

## 1. The plan's seven questions

### Q1 — the permission set the mint asks with, and the two pins that must not move. PASS

`personReadTokenPermissions` is exactly `{contents: read, pull_requests: read, metadata: read}`
(`internal/gitprov/github/apptoken.go:197-199`), exported by `PersonReadTokenPermissions`
(`apptoken.go:205`, which clones, so a caller cannot mutate the set), and it is the set the
route mints with: `internal/credsvc/githubread.go:321` passes
`github.PersonReadTokenPermissions()` to `MintInstallationToken` in place of the coordinator's
set. Nothing else is asked for, and there is no parameter to ask with: the mint body is built
from `s.repo` and the permission map alone (`apptoken.go:328`), so the token is scoped to the
one repository the caller named — `{"repositories": ["web"], "permissions": …}` is the shape
`testdata/coordinator_token.json` holds for the coordinator's mint.

The set's own pin is `TestPersonReadPermissionsCoverAFileRead`
(`internal/gitprov/github/apptoken_test.go:391`), which asserts the map as an equality and then
each level is `read`.

**The two pins A0 must not move:**

- `TestAppTokenCoordinatorPermissionsHoldNoWrite` (`apptoken_test.go:293`) still asserts the
  coordinator's set as the equality of its two reads (`:294`), still mutates the returned copy
  and re-reads (`:304-307`), and still mints against `testdata/coordinator_token.json` through
  a fixture that refuses a body it does not hold (`:310-315`). A0 does not touch that fixture:
  `git show --stat 3761ffac9` lists `internal/gitprov/github/apptoken_test.go` and not
  `internal/gitprov/github/testdata/coordinator_token.json`.
- `TestGitHubReadAsksForNoWrite` (`internal/credsvc/githubread_test.go:145`) reads the mint's
  **real request body** out of the fixture (`:151-155`), holds it to the accessor entry by
  entry (`:161-167`) and then to containing no `write` anywhere (`:167-171`). A0 changed which
  accessor it reads and nothing else about it.

**Mutations run (reverted).**

| mutation | observed |
| --- | --- |
| `apptoken.go:198` `contents: "read"` → `"write"` | **RED** — `TestGitHubReadAsksForNoWrite`: `the read mint asked for a write: contents="write"` |
| `apptoken.go:198` drop `pull_requests` | **RED** — `TestPersonReadPermissionsCoverAFileRead`: `PersonReadTokenPermissions() = map[contents:read metadata:read], want map[contents:read metadata:read pull_requests:read]` |
| `apptoken.go:169` `contents: "read"` → `"write"` (the *review* set, whose three entries are spelled identically) | **RED** — `TestReviewTokenPermissionsAreReadOnly`: `ReviewTokenPermissions() = map[contents:write …]` |

The third row is why the first mutation has to be aimed by line and not by text: the review
set's line is textually identical to the person set's, and a text-scoped edit of the file
hits the wrong one.

### Q2 — the person the service verifies. PASS

Identity: `VerifyPersonCaller` (`internal/credsvc/auth.go:1166`) folds the caller's address and
admits only a fold present in `personAccounts` (`:1172-1176`); the fold is
`callerClaimsFrom`'s, which verifies the signature and the Google issuer and folds the email
the same way. Audience: `checkCallerAudience(claims, a.cfg.Audience, GcloudAudience)`
(`:1178`) — the service's own URL and gcloud's default client id, and nothing else. Window and
`email_verified`: `checkCallerToken` (`:1385`, called at `:1181`). Who may mint is therefore a
deployment fact, not a route fact: `people` is `foldPersonAccounts(cfg.PersonAccounts)`
(`auth.go:804`), and an installation with an empty list admits nobody.

Two construction rules hold the class where the operator can see it: a person that is a
service-account address is refused (`auth.go:719-721`), and — the one that matters for this
route — **a person that is also the coordinator's account is refused**, in as many words ("one
caller would be two classes on the read mint", `auth.go:717`).

**Pins.** `TestRouteCallerClassMatrix` is the class boundary across every route and both
audiences (`internal/credsvc/callerclass_test.go:404-410` aims `githubReadPath` at it,
`:439` is the two audiences, `:457` is the assertion), and `TestGitHubReadAdmitsOnlyAPerson`
(`githubread_test.go:183`) is the route's own four cases.

**Mutations run (reverted).**

| mutation | observed |
| --- | --- |
| `auth.go:1172` admit the job, verifier and coordinator lists too | **RED** — `TestRouteCallerClassMatrix/{the service's own audience, gcloud's default audience}/{job, verifier, coordinator}/github-read` (`a coordinator caller was answered 200 on github-read, want 403`), and `TestGitHubReadAdmitsOnlyAPerson/a per-repository job's account` |
| `auth.go:1178` drop `GcloudAudience` | **RED** — `TestRouteCallerClassMatrix/gcloud's default audience/person/github-read`: `a person caller was answered 403 on github-read, want 200` |

The property is pinned, and by the matrix rather than by the route-local test — see finding 3.

### Q3 — the launcher's freedom from the App key and from any environment variable. PASS, with one scoped caveat (finding 5)

`FileAt` builds the provider from the caller's `Options`, nils any key it was handed and marks
the key as the service's (`internal/gitprov/github/fileread.go:165-166`), and installs the
caller's own token source (`:171`). The rule that a service-held key wins over a passed one is
the provider's own and is pinned one package over:
`TestServiceHeldKeyWinsOverAPassedKey` (`internal/gitprov/github/apptoken_service_test.go:287`,
against `github.go:141`). The launcher hands it no key at all: the call site is
`github.FileAt(ctx, github.Options{AppID: appID, BaseURL: githubAPIURL, HTTP: githubAPIClient}, ts, repo, ref, "fugaro.yaml")`
(`internal/cli/project_job.go:742`), whose only credential-shaped input is the **App ID**,
which is not a secret, out of the project's own local config
(`serviceReadTarget`, `project_job.go:688-704`; `errNoGitHubAppID` at `:595`).

Environment: `os.Getenv` appears exactly **once** in the whole of `project_job.go` and it is
the guard's own argument at `:727`; `personid.go` reads no variable at all and shells out only
to `gcloud auth print-identity-token` for the audience the service admits. This is not only a
grep: deleting that one call makes the file fail to compile (`"os" imported and not used`),
which is what "that is the only environment read on this path" means mechanically.

**Mutations run (reverted).**

| mutation | observed |
| --- | --- |
| `fileread.go:171` drop `p.UseServiceTokens(ts)` | **RED** — all eight `TestFileAt*` tests, including `TestFileAtUsesTheTokenSourceItIsGiven` (`fileread_test.go:241`) |
| `fileread.go:165` drop `o.PrivateKey = nil` | **GREEN** — no `TestFileAt*` test passes a key to `FileAt` (`fileread_test.go` names neither `PrivateKey` nor `ServiceHeldKey`); see finding 4 |

### Q4 — the agent-session refusal, before any read, naming the launch. PASS

`serviceReadAtTheForge` refuses in the read and before it: the installation's own records first
(`project_job.go:723-726`), then
`refuseKeyReadInAgent(os.Getenv, launchReadCommand, launchServiceReadWhat)` (`:727`), and only
then the identity mint (`:734`) and the forge read (`:742`). `launchReadCommand` is
`"fugaro run"` (`:514`) and `launchServiceReadWhat` names the document, the operator, the
service, and the checkout as the way out (`:515`). Placing the guard in the read rather than in
a caller is what makes both readers of the document — the job decision and the credential gate
— refuse it: they share `serviceReadRepoConfigAtRef` (`:643`).

**Pins.** `TestLaunchWithoutCheckoutInAnAgentSessionIsRefusedBeforeAnyRead`
(`internal/cli/run_test.go:2451`) asserts the refusal says `fugaro run`, `coding agent's
session` and `fugaro.yaml` (`:2464`), that the fixture counted `mints == 0 && reads == 0`
(`:2469`) and that Cloud Run was asked for no job at all (`:2472`).
`TestProjectJobAgentAuthRefusesAnAgentSessionBeforeTheForgeRead`
(`project_job_test.go:3261`) is the same for the other reader. The order of the two refusals —
no credentials service means no read to refuse, so the no-service line is the honest one and
its way out is one an agent can take — is pinned by
`TestLaunchWithoutCheckoutInAnAgentSessionWithoutAServiceNamesTheCheckout`
(`run_test.go:2483`, asserting the no-service sentence is present and the agent sentence is
not).

**Mutation run (reverted).** Blinding the guard — `os.Getenv` replaced by a function that
reads a renamed variable, so no marker is ever seen — is **RED**:
`TestProjectJobAgentAuthRefusesAnAgentSessionBeforeTheForgeRead`
(`an identity token was minted for https://127.0.0.1:… in a coding agent's session`) and
`TestLaunchWithoutCheckoutInAnAgentSessionIsRefusedBeforeAnyRead` (`run_test.go:2462`).

### Q5 — the ref the document is read at. PASS

The ref is `spec.Ref` and nothing else: `readRepoConfigAtRef(ctx, e, spec.Repo, spec.Ref)`
(`project_job.go:485`, and the second reader at `:1317`), through `FileAt` (`fileread.go:159`)
into `Contents(ctx, path, ref)` (`internal/gitprov/github/contents.go:51`). There is no
default-branch path: `Contents` sets the `ref` parameter only when a ref was named
(`contents.go:61-64`), and the read at the API's default branch is what an **empty** ref means,
so an empty ref can only be a caller's own empty one.

`ErrRefNotFound` is the absent-ref row and `ErrFileNotFound` the absent-file row, told apart by
the 404's own body (`fileread.go:130-144`), and a 404 from a repository the token cannot reach
is refused rather than read as "no document" — `confirmAbsent` re-asks the repository and, when
a ref was named, the ref (`fileread.go:112-123`).

**No injection.** The ref travels as a query parameter built with `url.Values`
(`contents.go:61-64`) and encoded by `q.Encode()` (`contents.go:111`), and every path segment
is escaped on its own (`contents.go:104-109`).

**Mutations run (reverted).**

| mutation | observed |
| --- | --- |
| `project_job.go:485` `spec.Ref` → `""` | **RED** — `TestLaunchWithoutCheckoutReadsTheRefThroughTheService` (`project_job_test.go:3001`: `2 mint(s) and 2 read(s), want exactly one of each` — the memo key at `project_job.go:660` is `repo+"\x00"+ref`), `TestLaunchWithoutCheckoutJudgesACloneWorkflowByTheCloneGates`, `TestLaunchWithoutCheckoutOnARefWithNoDocumentKeepsThePerRepositoryJob` (`:3083`: the absent file was confirmed against 1 of the two things it takes) and `TestLaunchWithoutCheckoutFoldsTheRepositoryKeyTheConfigRecords` |
| `contents.go:111` `q.Encode()` → the ref written raw | **RED** — `TestContentsReadsTheFileAtItsRef` (`contents_test.go:131`): `…agent%3Fv=1?ref=release/x, want …agent%3Fv=1?ref=release%2Fx` |

The fixture files under `testdata/fileread/` pin the ref **value** (`at_ref.json` names
`?ref=release/1.2`) but not its escaping: `httpfixture` parses the fixture's path and compares
decoded query values (`internal/gitprov/httpfixture/httpfixture.go:115-116`), so a raw and an
encoded ref look the same to it. The escaping is pinned by `contents_test.go` instead, which is
why the second mutation above is the one that matters.

### Q6 — the absence of a fallback. PASS

`repoConfigAtTheRef` (`project_job.go:478`) treats exactly one outcome as "nothing is
selected": `errors.Is(err, github.ErrFileNotFound)` → `nil, nil` (`:487-492`, the row spec.md
§7 says is the answer and not a fallback), and every other error is `serviceReadRefusal`
(`:493-494`). The credential gate refuses too rather than falling back
(`project_job.go:1317-1321`).

**Mutation run (reverted).** `serviceReadRefusal(spec.Repo, spec.Ref, err)` → `nil, nil`
(a failed read silently selecting nothing) is **RED** in eight tests:
`TestNoCheckoutGitHubRefusalNamesTheCheckoutAndTheRef` (`project_job_test.go:2551`),
`TestLaunchWithoutCheckoutNamesAForgeStatusAndNotItsBody` (`:3105`),
`TestLaunchWithoutCheckoutWhereTheRefDoesNotExistNamesTheRef` (`:3141`),
`TestLaunchWithoutCheckoutWithoutARecordedAppIDNamesTheRead` (`:3183`),
`TestLaunchWithoutCheckoutWithoutACredentialsServiceNamesTheCheckout` (`:3371`),
`TestLaunchWithoutCheckoutWhereTheMintCannotReadAFileNamesTheRedeploy` (`:3405`),
`TestLaunchWithoutCheckoutInAnAgentSessionIsRefusedBeforeAnyRead` (`run_test.go:2462`) and
`TestLaunchWithoutCheckoutInAnAgentSessionWithoutAServiceNamesTheCheckout` (`run_test.go:2492`).

### Q7 — the two message texts. PASS for the words; CONCERN for the second one's remedy (finding 1)

Both sentences are in the code verbatim as spec.md §7 has them — the no-service row
(`project_job.go:543`, against `spec.md:239`) and the redeploy row (`project_job.go:562`,
against `spec.md:241`) — and neither carries a credential value: they name the repository, the
ref, the service and the way out, and the general remote row prints `HTTP %d` and never
GitHub's own text (`project_job.go:577-581`, pinned by
`TestLaunchWithoutCheckoutNamesAForgeStatusAndNotItsBody`, `project_job_test.go:3093`).
`TestLaunchWithoutCheckoutWhereTheMintCannotReadAFileNamesTheRedeploy`
(`project_job_test.go:3393`) also asserts that neither the fixture's minted token, nor the
operator's identity token, nor GitHub's own message appears in the refusal.

**Mutations run (reverted).**

| mutation | observed |
| --- | --- |
| `project_job.go:543` reword the cause clause | **RED** — `TestLaunchWithoutCheckoutWithoutACredentialsServiceNamesTheCheckout` (`project_job_test.go:3375`: `the refusal is not spec.md §7's`, got/want printed in full) |
| `project_job.go:562` drop `with fugaro update` | **RED** — `TestLaunchWithoutCheckoutWhereTheMintCannotReadAFileNamesTheRedeploy` (`project_job_test.go:3409`, same wording) |

The concern is the *accuracy* of the second sentence for one of the two causes its own comment
names — finding 1, with the decision it needs.

---

## 2. The credentials-service rule's own checks

- **One audit line per refusal, once the caller is verified.** `refuseGitHubRead`
  (`githubread.go:388`) writes exactly one line through `auditGitHubRead` (`:370`) and then
  answers; every refusal after `VerifyPersonCaller` (`:244`) goes through it. The rule is
  **not pinned for this route's refusals** — see finding 2. (The success path and the
  stop-switch refusal are pinned; the rest are not.)
- **No credential value in an audit line, a log line or an error.** PASS.
  `TestGitHubReadAuditsOneLineAndNoCredential` (`githubread_test.go:337`) reads both buffers
  back for the minted token and for `pemCore` of the App key, and
  `TestAnAuditLineNeverCarriesACredentialValue` (`audit_answer_test.go:477`) holds the property
  at the auditor. Mutation (reverted): `s.log.Warn("credsvc: read mint debug", "token", token)`
  before the answer → **RED** in `TestGitHubReadAuditsOneLineAndNoCredential` (`:370`, `the
  service's log carries the token GitHub minted`) and `TestGitHubReadUnauditedIssuesNothing`
  (`:426`).
- **No secret-shaped literal in tests or fixtures.** PASS. The files these three commits added
  or changed were scanned for PEM blocks, `ghp_`/`ghs_` provider tokens, `sk-` model keys,
  `AKIA` access keys, `AIza` API keys, JWTs and long hex/base64 blobs. The only hits are a
  commit SHA in `docs/rigor-modes/reconciliation.md` and the base64 body of the file fixture
  `testdata/fileread/at_ref.json`, which is the encoded `fugaro.yaml` the read is meant to
  return. The token the fixtures name is `ghs_fixture_person_read` and the minted one
  `fake-…-not-a-real-credential`: both say what they are.
- **The minted token's read-only scope: it must reach a file read and nothing that writes.**
  PASS. It asks for `contents: read` (`apptoken.go:198`), the route asks with that set
  (`githubread.go:321`), the read the set has to reach is the one `FileAt` makes
  (`fileread.go:172`), and no level anywhere is a `write` — pinned at the set
  (`apptoken_test.go:391`), at the mint's real body (`githubread_test.go:145`) and at the
  coordinator's unchanged set (`apptoken_test.go:293`).
- **The owner-only paths must not be widened.** PASS. Across all three commits, `git show
  --name-only` finds **no** change to `deploy/terraform`, to `internal/config`, to `schemas/`,
  to `SECURITY.md`, to `internal/cli/creds*.go`, and none to `config.ServiceHeldSecretNames`
  or `config.ProxiedGitSecretNames`. The rule's `paths:` list gates `internal/creds/**`,
  `internal/credsvc/**`, `internal/gitproxy/**`, `internal/gateway/gitforward*.go`,
  `SECURITY.md` and `website/content/security.md`; the two files it gates that any of the three
  commits touches are both A0's — `internal/credsvc/githubread.go`, which is what A0 is for, and
  `internal/credsvc/githubread_test.go`, that route's own test — and nothing under
  `internal/creds/`, `internal/gitproxy/` or `internal/gateway/` moves at all. A0's changes
  outside those two are A0's subject and its tests — the permission set itself
  (`internal/gitprov/github/apptoken.go` with `apptoken_test.go`),
  `internal/cli/docs_run_without_checkout_test.go` and
  `ee/coordinator/cli/console_contract_test.go` — and the records of them: the prose over the
  service-held-key client (`internal/gitprov/github/serviceread.go`), the console's and the
  coordinator's own words (`console/src/*`, `ee/coordinator/cli/coordinator*.go`,
  `website/content/cli-reference.md`), and line re-points in other designs' documents —
  verified line by line, no code line in that last group.

---

## 3. Findings

**1. LOW — the redeploy message's remedy is right for one of the two causes the row covers.**
`project_job.go:562` says `… minted a token that cannot read a file; redeploy it with fugaro
update`, and its own comment (`project_job.go:556-561`) says the 403 it answers has two causes:
a service not yet redeployed after A0, **or an App not granted `contents`**. Redeploying fixes
the first; for the second the operator has to grant the App the permission and reinstall it, and
the message would send them round the same loop — `fugaro update` will redeploy, the App still
cannot read, the same sentence comes back. spec.md §7's row (`spec.md:241`) folds both causes
into this one sentence deliberately, and the code matches the spec, so this is not a code
defect and is not fixed here: it is a wording decision, and it is D916 below. The existing test
(`project_job_test.go:3393`) pins the sentence as it is, which is right — a fix changes the test
and the spec together.

**2. MEDIUM — the "exactly one audit line per refusal" rule is unpinned on this route's
post-admission refusals.** `refuseGitHubRead` (`githubread.go:388`) is what writes the line;
the route's refusals after admission are the two run-identity headers (`:252`, `:257`), the
unreadable body (`:266`), the request shape (`:274`), the unreadable switch and the switch-on
refusal (`:281`, `:285`), the bundle load and the App key (`:291`, `:305`), the unknown
repository (`:314`) and the failed mint (`:333`). Only the switch-on refusal counts a line
(`TestGitHubReadIsStoppedByTheGlobalSwitch`, `githubread_test.go:287`), beside the success path
(`:337`) and the unaudited path (`:406`). Mutation (reverted): `s.refuseGitHubRead(...)` →
`s.refuse(...)` at `:274` — a verified caller refused with **zero** lines — leaves the whole
`-run GitHubRead` suite **GREEN**, and so does the same swap at `:266`. `TestGitHubReadBodyIsTwo
FieldsAndNoMore` (`:235`) is the test that covers the refusal at `:274` and it asserts the
status, the error class and the zero GitHub calls, not the line. The property is a stated rule
of the rule file, so the follow-up is a test that counts one line per post-admission refusal
(a table over the refusal paths, the way this service's other routes do it) — no new behaviour,
no decision.

**3. LOW — the route-local class test's comment claims more than the test demonstrates.**
`TestGitHubReadAdmitsOnlyAPerson`'s comment (`githubread_test.go:178-182`) says "a mutant that
widened `VerifyPersonCaller` — to the coordinator's list, to the job accounts, to the verifiers
— is refused a token by this test". One of the three holds there: the fixture the route's tests
build (`githubReadSvcWith`, `githubread_test.go:67-86`, over `newFixtureWith`,
`internal/credsvc/auth_test.go:208-241`) sets no `CoordinatorAccount` and no `VerifierAccounts`
(`:221-231`), so the coordinator case (`:190`) passes for the wrong reason — the address is in
no list at all — and there is no verifier case in it to pass at all. The mutation shows it:
admitting only the coordinator
(`a.coordinatorAccount != "" && folded == a.coordinatorAccount`) leaves this test GREEN, while
admitting all three lists flips only the job case. Both are caught by `TestRouteCallerClassMatrix`
(`callerclass_test.go:457`, and its fixture does set `CoordinatorAccount`, `:136`), so the
property is pinned and this is a comment/coverage matter: either the comment names the matrix,
or the fixture grows the two accounts and the test's own cases start doing the work it claims.

**4. LOW — two defensive lines in `FileAt` are unpinned.** `fileread.go:165-166` nil the key
and mark it the service's. Mutation (reverted): dropping `o.PrivateKey = nil` leaves all eight
`TestFileAt*` tests GREEN, because no test hands `FileAt` a key at all. The property they
defend is real and is pinned one level down (`github.go:141`,
`TestServiceHeldKeyWinsOverAPassedKey`, `apptoken_service_test.go:287`), so this is coverage
and not behaviour: one `FileAt` case that passes a key and asserts it is not used would close
it.

**5. LOW — the comment at `project_job.go:739-741` overstates.** It says "nothing here reads a
host or a proxy out of the environment, and a test points both at its own server". The host is
true — `BaseURL: githubAPIURL` (`:742`) is this package's own seam, and so is the client. The
proxy is not: `githubAPIClient` is nil in production, so the read goes through
`http.DefaultTransport`, which resolves `HTTPS_PROXY`/`HTTP_PROXY`/`NO_PROXY` from the
environment, exactly as every other GitHub call this CLI makes does (`serviceMintClient` clones
the same default transport for the credentials service's own mint,
`internal/gitprov/github/apptoken.go:641-654`). No Fugaro variable is read on this path — the
one `os.Getenv` at `:727` is the agent guard's — but the process-wide proxy posture is
inherited, and the sentence as written says otherwise. Whether that posture should be dropped
for the operator's own read (a `Proxy: nil` transport) is a decision, not a defect, and it is
D917 below; correcting the comment is not.

---

## 4. New decisions for the D901 block

Written in the block's form, with the next free ids after D915.

**D916 — does the 403 redeploy row stay one sentence for both of its causes?**
The row (spec.md §7, `project_job.go:562`) answers "the minted token cannot read a file" with
`redeploy it with fugaro update`, and the causes it covers are a service not redeployed after
A0 and an App not granted `contents` (`project_job.go:556-561`). Redeploying only answers the
first. Recommended answer: keep one sentence but make the remedy cover both — name the redeploy
and the App's `contents` grant in it — because the operator reading a 403 cannot tell the two
apart from here, and a sentence that names only the remedy that cannot work for one of them
sends them round the loop. The change lands with `spec.md:241-242`, the code and
`TestLaunchWithoutCheckoutWhereTheMintCannotReadAFileNamesTheRedeploy` in one commit.

**D917 — does the operator's own read follow the environment's proxy?**
The read is made with the process-wide default transport, so `HTTPS_PROXY` and `HTTP_PROXY`
decide where it goes (`project_job.go:742`, and the credentials service's mint for it at
`internal/gitprov/github/apptoken.go:641-654`). Recommended answer: leave it. The read is an
ordinary GitHub API call with a short-lived token scoped to one repository, it is the same
posture every other call this CLI makes has, and a proxy is how a good part of the people who
run this reach GitHub at all; the fix is the comment at `project_job.go:739-741`, which must
stop claiming a freedom this process does not have.

---

## 5. Verdict

**SHIP IT WITH FOLLOW-UPS.**

The three commits do what the plan says they do, at `origin/main`, and every one of the plan's
seven questions is answered PASS on code that was read and on properties that were attacked and
held: the mint asks for a read-only set of exactly three permissions and the coordinator's set
and its fixture body did not move; the route admits a person and only a person, on two
audiences, and refuses the coordinator, the job accounts and the verifiers; the launcher holds
no App key and reads no environment variable of its own, refusing an agent's session before it
mints or reads anything; the document is read at `spec.Ref` and nowhere else, with the ref
escaped as a query parameter; nothing falls back to the per-repository job when a read fails;
and the two refusal messages are spec.md §7's, word for word, carrying no credential value. The
one rule of the credentials-service rule file that is not fully pinned — one audit line per
refusal — is unpinned in test coverage only, with the code itself writing the line.

The follow-ups are findings 1 to 5: a wording decision (finding 1, D916), a test that counts the
route's refusals (finding 2), a comment that claims more than its test demonstrates (finding 3),
a `FileAt` case that hands it a key (finding 4), and a comment that claims a freedom this
process does not have (finding 5, D917). None of them changes what the code does today, and none
of them is a reason to hold the three commits back.
