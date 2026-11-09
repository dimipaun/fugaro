# The api-key review — the security review of the merged AK4 and AK5

**Written 2026-11-09.** This is [implementation-plan.md](implementation-plan.md) §6 item 6's mandatory
review, owed **before** the SEC tasks of that plan land rather than after: the two that have landed —
**AK4** (`1d2591d5`, PR #1860) and **AK5** (`0944d4d4`, PR #1905, with `9fa5fd90`, PR #2033) — landed
under it, and it **stands for AK6a, AK6b and AK7**, which have not. It reviews **code that is on
`main`** and not a sketch: what the two tasks actually changed, what that change is worth as an
attack surface, and what it leaves to the tasks that follow.

**It is not a second design and it changes no design.** Where it disagrees with
[implementation-plan.md](implementation-plan.md) or [reconciliation.md](reconciliation.md), those two
win about the plan; where it disagrees about **the merged code**, it wins. **No code was edited by
this review**: what it found and could not fix in a docs pass is written here as a finding and became
a task (**AK9**, **AK10**), written and **not approved** — nothing was approved after 2026-11-07.

**What AK4 and AK5 are, in one paragraph.** AK4 lets the credentials service's **review class** be
served by the project's own Anthropic API key: the policy's pairing may name the sentinel
`config.AnthropicKeyAccount` (`internal/config/claude_accounts.go`, the publishable spelling of
`config.AnthropicSecret + "--project"`), `internal/credsvc`'s `offeredToReview` offers that pairing
the bundle's **tier-B** entry at `config.AnthropicSecret` and nothing beside it, and
`internal/credsvc/forward.go`'s `send` forwards the call to Anthropic under `x-api-key` as it always
did for tier B. AK5 makes the runner admit such a review — `internal/runner/routing.go`'s
`checkReviewClassModelPath` takes the sentinel beside `"oauth"` and the cascade and still refuses an
`api-key` repository's own credential — and makes the sentinel **deployable**: a profile naming it
gets the job whose account segment is the sentinel, mounting nothing, and its review-only variant
beside it, so an installation that publishes the sentinel and no Claude account has a review job at
all.

## 1. What was read

The whole request path of the service, and every reader of the facts the two tasks moved:

- `internal/credsvc/server.go` — the routes (`servedRoute`), the caller's verification, the service's
  own door to the class (`ServeHTTP`'s `id.JobClass` switch, whose `ClassReview` arm is `pairReview`),
  the destination a forward goes to;
- `internal/credsvc/auth.go` — `AuthConfig` and the job sets, the review set's own rules;
- `internal/credsvc/review.go` — `ReviewJobAccount`, `ReviewJobAccountsFor`, `ReviewOf`,
  `mintPermissions`, `reviewAccount` and `pairReview`;
- `internal/credsvc/bundle.go` — `CredentialKey`'s closed set and `ParseBundle`;
- `internal/credsvc/cascade.go` — the wire tiers, `reviewCandidates` and `offeredToReview`;
- `internal/credsvc/forward.go` — `send`, `destination` and `carriesAny`;
- `internal/credsvc/probe.go` and `audit.go` — the auditor the service arms, and the one line a
  refusal writes;
- `internal/credsvc/github.go` and `bitbucket.go` — the mint's permission sets;
- `internal/creds/select.go` — which candidate a call is offered, and the tier-C refusal;
- `internal/config/claude_accounts.go` and `project_secrets.go` — the sentinel's rules and the
  project's own key's reach;
- `internal/gateway/export.go` and `gateway.go` — the helpers the service reuses, and the base it
  builds;
- `internal/runner/routing.go` — the class's model path and its allow-list arm;
- `internal/cli/run.go`, `internal/cli/project_job.go` and `internal/cli/creds.go` — the launch, the
  account axis's placement and the bundle's only writer;
- `internal/projectpolicy/accounts.go` and `policy.go` — the `accounts` block and the account-choice
  members;
- `internal/task/task.go` with `schemas/task.schema.json` — the axis value where it may reach a task;
- `deploy/terraform/gcp/modules/installation/variables.tf` and `modules/project/variables.tf` — the
  two validations the axis is held to;
- the tests that pin the properties below, `internal/credsvc/review_test.go` and `cascade_test.go` and
  `internal/runner/routing_internal_test.go` first among them.

**Not read:** no live installation, no real Secret Manager, Firebase project or provider, and no
service that has ever run outside a test (§6).

## 2. The properties the review verified

Each is a property of the merged code, with what it rests on. **The review found none of them
false.**

1. **The review job holds no credential of its own.** The variant's job mounts nothing
   (`config.ProjectJobTokenAccount` answers `("", false)` for the sentinel's segment), and
   `internal/credsvc/review.go`'s own words are that a review job mounts no repository and holds no
   installation token — which is also why `mintPermissions` gives a **review** caller
   `github.ReviewTokenPermissions()`, the read-only set, while a repository caller keeps the run's
   write set (`github.RunTokenPermissions()`).
2. **The class's door is the job the caller was admitted to, and not a credential.** On the service's
   side it is `ServeHTTP`'s `id.JobClass` switch (`internal/credsvc/server.go:720`), whose
   `ClassReview` arm resolves the caller through `pairReview` and `ReviewOf` and hands the mint that
   pairing's own answer — so a review job cannot be traded for a coding one and no caller states its
   way in. The runner's side of the same fact is `(*run).reviewClassRun`
   (`internal/runner/gateway.go:187`, called from `internal/runner/routing.go:68`), the conjunction of
   the run's mode and the job's own class. **Neither door is in the other's package**: the service
   decides from the verified identity, the runner from the job it was launched as.
3. **The pairing names exactly one credential and there is no fallback.** `reviewCandidates` returns
   the pairing's **one** credential: a tier-A entry whose name is the account, or — for the sentinel —
   the tier-B entry at `config.AnthropicSecret`, matched **by name and tier**, so a bundle that grew a
   second tier-B credential would never have it offered to a review caller. Every other candidate is
   reported `ReasonNotAdmitted`; the cascade's ladder has nothing to fall to even where the policy's
   `fallback` would allow one.
4. **A repository's own file cannot choose the class's credential.** `checkReviewClassModelPath` still
   refuses a repository whose own `agent.auth` is `api-key`, and the axis value is refused in a
   repository's `fugaro.yaml` (`config.Validate`) and as a stage account (AK4's choice-key refusal).
   `TestReviewRunIsNeverServedByAnAPIKeyOfItsOwn` holds the fail-closed direction, rewritten and not
   deleted (**D1420**).
5. **The destination is not the caller's, and it is a constant.** `destination` is
   `gateway.AnthropicBase(gateway.Upstream{})` joined to `cl.path`, which comes from `servedRoute`'s
   closed path set; the call's query is dropped **whole** when it carries either token the caller
   presented. A caller cannot name a host and cannot smuggle a token into a query.
6. **A redirect is never followed.** `upstreamClient` sets `CheckRedirect` to `http.ErrUseLastResponse`
   and drops the cookie jar, whoever built the client — the one answer that would take a credential
   somewhere nobody chose.
7. **The answer is redacted, and the audit line is not.** The response is chained through
   `gateway.NewRedactWriter` for the credential **and** every caller token, and every header carrying
   one of those tokens is deleted rather than edited (`gateway.RedactHeaders`). The issuance line is
   written before the answer and names the credential's **name and tier**, never its value; the
   auditor is armed with the service's own bundle loader (`NewProbeAuditor`), which is the one place
   the values it redacts live.
8. **The class is still held to the project's model allow-list.** `routing.go`'s `budget.allowed_models`
   arm is untouched by which credential pays (**D1425**), and the service applies the gateway's own
   request check (`gateway.RequestRefusal`) before it reads a credential.
9. **The axis is validated at both ends.** `modules/project`'s `job.account` validation admits the
   sentinel as a literal, `modules/installation`'s `review_job_accounts` validation requires all three
   fields of a row, and the service refuses a row it could not serve a call by.
10. **The tests that pin the two tasks' intent exist and assert the right thing.**
    `TestReviewPairingMayNameTheProjectsAnthropicKey` (`internal/credsvc/review_test.go`) pins the
    destination, the auth shape, the tier, the switch and the admissions;
    `TestReviewAccountsAreTheNamesProjectJobsDeploys` holds the deployer and the service to one set of
    names; `TestReviewJobIsDeployedForAProjectWithNoClaudeAccount` pins the variant an installation
    with no Claude account gets.

## 3. The three attack questions

**What can a hostile agent do with the bundle?** **It cannot reach it.** The job holds no model
credential at all — that is D143's posture and D1426's answer — and the service's whole Secret Manager
reach is **one** `secretAccessor` grant on the **one** container (Global Constraint 12). The document
is written by `fugaro creds set` alone, which refuses a coding agent's session, a run's own process and
the GODEBUG leak **before** it reads anything or opens the cloud, and no workflow may declare the
container. A bundle that fails to load is a refusal of every caller rather than of one credential
(which is **F26**'s subject, and a safety property rather than a hole). No header, body or audit line
an answer carries holds a value. **Verdict: no path found, and the one that exists is the owner's own
credential chain, which is `SECURITY.md`'s already-recorded residual.**

**What can a hostile agent do with the forwarded key?** **It gets one call forwarded under it, to one
endpoint, and nothing else.** The key is not in the job's environment, not in the agent's environment,
and not in the answer: the agent asks the service, the service attaches the credential and forwards.
The caller cannot steer the host (property 5), cannot steer the credential (property 3), cannot use a
redirect to move it (property 6), and cannot read it back (property 7). What the key **can** be made to
do is what the class exists to let it do: spend the project's own Anthropic key on a review of a
repository the published admissions admit — which a cascade coding run already does with the same key,
so AK4 widened **who may spend it** and not **what it reaches**. **Verdict: the residual is the
project's own key being spendable by one more class of run, which is the design's whole point and is
bounded by the admission and the allow-list.**

**What can a hostile agent do with the base URL?** **Nothing today — there is no base URL in the
merged code.** `send` builds `upstreamKindAnthropic` with `gateway.AnthropicBase`'s constant, so a
provider's endpoint is a property of **AK6a**, **AK6b** and **AK7** rather than of what has landed,
and this question's answer for them is a list of conditions instead of a verdict (§5). **Verdict: no
attack surface added by AK4 or AK5; the surface AK6b adds is the one §5 constrains.**

## 4. Findings

**F25 — four of the service's own sentences say a review call is served by a Claude account, and AK4
made that inexact.** `internal/credsvc/auth.go` refuses a review job account naming nothing with
"names no Claude account, so no call of it could be served" and a duplicate with "bound to two Claude
accounts"; `internal/credsvc/server.go` writes the same two in the serve path, one of them as "no
email or no Claude account"; and `modules/installation/variables.tf` describes `review_job_accounts`
as "carrying the Claude account the variant's calls are served by". Since AK4 the value may be the
sentinel, which names the project's own key and — `review.go`'s own words — **is not an account**.
**One-line fix:** the refusal names what the value is, the **account axis** value the review job is
served by, in all four sentences and the module's prose. **Became AK9** (SEC), written and not
approved. **Severity: none on its own** — no behaviour changes with the words — but the sentence is
what an operator reads when the service refuses a call, and it would send them looking for an account
that does not exist.

**F26 — the bundle's strictness turns a newer write into a service-wide halt.** `ParseBundle` refuses
the **whole document** on a key it does not know (`bundleErr(ErrBundleUnknownKey, …)`), and the
service answers that as a refusal of every caller: a bundle written with **AK6b**'s provider entry by
a CLI newer than the installation's deployed service halts every run that reaches the service, coding
runs included, until `fugaro update` moves the image. **One-line fix:** `fugaro creds set` refuses a
write whose newest key the installation's own recorded service image
(`terraform.tfvars.json`'s `credentials_service.image`) predates, naming `fugaro update` — the
`*Since` guard `reviewAdmissionSince` already establishes — with the release's `### For operators`
sentence. **Became AK10** (SEC), written and not approved, and it is a **guard AK6b's own shape needs**
rather than a defect in AK4 or AK5: today no credential kind reaches the bundle that the deployed
service cannot read. **Severity: high when AK6b lands, none before it.**

**F27 — the README was reading the gates of 2026-11-07.** Its status line still said "items 6 and 7
… E7, not met, so those four tasks may not start and the cloud half stays unlaunchable", and its file
table still claimed "each with its files (at most six)". Item 7's gate is lifted (the flip of
2026-11-08), item 6 is now partly settled by this review, and the reconciliation of 2026-11-09 made
the file count false in both directions (AK6a is 24 files). **One-line fix:** the status, the file
table and the numbers are read off the tree, as they now are. **Fixed in this docs pass, not a task.**

**What was considered and is not a finding.** The mint's read-only set, the audit line's one-per-refusal
rule, the `ReasonNotAdmitted` spelling, the class's door, the sentinel's spelling (`--project`, which
`config.ClaudeAccountNameRE` refuses, so no account name can ever be the sentinel) and the sentinel's
refusal in a repository's own file were each examined and each holds.

## 5. What this review places on AK6a, AK6b and AK7

These are the conditions the three tasks **keep** rather than re-decide; they are the base-URL
question's answer, and [implementation-plan.md](implementation-plan.md) §3 carries them in the tasks
themselves.

1. **The provider's base URL and auth shape live in the bundle and nowhere else** (**D1418**,
   2026-11-09): not in the runner's environment, not in a task, not in the published policy. AK7's
   files are `internal/runner` and nothing else for this reason.
2. **An unknown provider fails closed.** A provider entry with no base URL, or with an auth shape
   outside `bearer` and `x-api-key`, is refused by name; nothing is defaulted, because a defaulted
   base URL is a credential sent to an endpoint nobody named.
3. **The destination rule stays where it is.** The provider's base URL joined to the path
   `servedRoute`'s closed set yields, the query dropped where it carries a caller token, and the
   credential is attached by the gateway's own helper rather than by a second implementation.
4. **The redaction and the redirect refusal are not moved.** The answer stays chained through
   `gateway.NewRedactWriter` for the credential and every caller token, and the client stays the one
   that never follows a redirect.
5. **The class's offer is still exactly one credential with no fallback.** AK6b's offer rule matches
   the new axis value by **name and tier**, as the sentinel's does.
6. **No secret-shaped literal** in a test, a fixture or a doc for either fact: a base URL and an auth
   shape are configuration and are written as such, which is the rule
   `.claude/rules/credentials-service.md` states for the package and this folder keeps for its docs.
7. **The served path waits on Check 50.** `internal/creds/select.go`'s `checkCandidates` refuses every
   tier-C candidate in a sentence naming **Check 50** (**D150**, **C3.6**), and the provider key is
   that tier (**F16**): AK6b may land its document, its offer and its forwarding, each unit-tested,
   and AK7 its runner path, but **no release note may present a provider-served review as working**
   and **Check 330's step C may not be run** until Check 50 has run or the owner takes a new decision
   ([implementation-plan.md](implementation-plan.md) §6 item 10, [reconciliation.md](reconciliation.md)
   **U7**).

## 6. What this review could not verify

- **No live installation was measured.** Every property in §2 is read from the code and its tests; the
  owner's live checks are unrun (implementation-plan §6 item 7), and the cloud paths have never been
  exercised on a real service, a real Secret Manager or a real Firebase project.
- **No provider was called.** The base-URL question is answered for **AK6a/AK6b/AK7** as conditions,
  because the code that carries a provider's endpoint does not exist yet; a real provider's own
  behaviour (a redirect, a downgrade, an unusual `WWW-Authenticate` or error body) is unmeasured.
- **The service's own configuration was read as a deployment, not as a running process**: nothing here
  confirms what a deployed revision's environment holds on any particular installation.
- **The review is a single pass by the same author as this folder**, which is not the independent
  reviewer the SEC tier asks for: `.claude/rules/credentials-service.md` requires an independent review
  before merging, and that step is the owner's or a second agent's, not this document's.
