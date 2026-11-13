# Reconciliation: Bitbucket token types in the credential proxy

Checked against `origin/main` at `abd0a60af` on 2026-11-13. Owner of the spec: the project owner (the spec names none). **Where
this document disagrees with [spec.md](spec.md), this document wins.** Nothing here is implemented and no task is approved.

The spec is one capability (the proxy accepts any mix of repository, project and workspace tokens), so there is no capability map.

The spec was written without the repository in front of it. Three of its parts already exist in another shape: the **coverage
list** (`project_secrets.bitbucket-token.allow_data_to`, published as the node's `allow_repos`), the **expiry record**
(`secret_expires`) and **mark, back off and probe** for a failing credential (`internal/creds`, for model credentials). What is
genuinely new is narrower than the spec reads: **the service holds and forwards exactly one Bitbucket token today**, so
holding several, choosing one per request, matching a project token by UUID, and checking a token on the way in are the work.

## Assumptions

- **A1.** "The credential service" is `internal/credsvc` and "the proxy" is its route `internal/credsvc/bitbucketproxy.go`; no new component.
- **A2.** "Job" is a run on a project job whose repositories the verified identity pairs to (`pairShared`, `docs/shared-jobs-default` S2), or a per-repository proxied job: both reach this route the same way.
- **A3.** "The dashboard" is the console and `fugaro doctor` / `--json` reads, not a new surface.
- **A4.** The Bitbucket **read consumer** (`bitbucket-read-consumer`, an OAuth consumer the read mint exchanges) is a different credential for a different route (`coordinator show`) and is untouched.
- **A5.** One Bitbucket workspace per project holds (`docs/shared-jobs-default` D2314); this work does not lift it.

## What the repository already has

| Spec concept | What the repository has |
| --- | --- |
| Token `coverage` pattern | `project_secrets.bitbucket-token.allow_data_to` (`internal/config/project_secrets.go`: `owner/name`, `*` inside a segment, never a wildcard owner), derived by `projectpolicy.DeriveBitbucketToken` into the node's `bitbucket_token.allow_repos`. It is the operator's *authorization*, not a per-token fact |
| Stored token | One key `bitbucket-token` in the bundle (`internal/credsvc/bundle.go`, `Bundle.BitbucketToken`, kind `KindGit`), schema `schemas/creds-bundle.schema.json`, written by `fugaro creds set bitbucket-token` |
| Forward with the stored token | `bitbucketProxy` loads the bundle and forwards with the one entry; `x-token-auth` Basic for git, Bearer for REST (`forwardBitbucket`) |
| Narrow to the job's repositories and to read or write | `gitproxy.Decide` and `CheckPRWrite`, with the pairing's canonical repository and the job class (`proxyIdentity`) |
| Record and warn on expiry | `secret_expires` in the local config (D503c) and `fugaro doctor`'s expiry measure (`internal/cli/doctor.go`) |
| Check a token still works | `bitbucket-token-valid` in `internal/cli/doctor_brokering.go`, behind `--check-bitbucket-token` |
| Mark a failing token, back off, probe | `internal/creds` (`Mark`: available, exhausted, backed_off, broken) and `internal/credsvc/probe.go`, for model credentials; `forward.go` reads a 429 and `Retry-After` |
| Add a token | `fugaro creds set` (`internal/cli/creds.go`), which reads and writes the bundle with the operator's identity |
| Cache per repository | Nothing for Bitbucket projects |
| Throttling view | None |

## Reconciled against the two earlier Bitbucket plans

The spec restates, and in places overturns, rulings made in [docs/bitbucket-credentials](../bitbucket-credentials/README.md) (D360 to
D367, D1960 to D1962) and [docs/bitbucket-workspace-token](../bitbucket-workspace-token/README.md) (D460 to D477, D1980 to D1984),
both read in full on `origin/main` together with their `reconciliation-secrets-brokering.md` verdicts and
[docs/secrets-brokering](../secrets-brokering/reconciliation.md) (D493, D505, D506).

| Spec statement | What the earlier plans ruled or built | Effect here |
| --- | --- | --- |
| Support repository, project and workspace tokens, any mix | D461 (2026-10-27): either a workspace or a project token, the operator picks the narrowest. Repository tokens are today's default, per repository, mounted on the job | **Extends D461** to a repository token inside the proxy's store and to several tokens at once. Records an amendment, not a reversal |
| Required scopes: repository and pull-request read and write | D470: minimum scopes are `repository:write` and `pullrequest:write` (each implies its read), no admin, webhook, pipeline or project-admin scope; that the Bitbucket UI names them so for project and workspace tokens is UNVERIFIED (U7 of that plan); Check 180 records them | **Exists.** The published list is D470's; this work reuses it and does not write a second one |
| Name the token (for example "Fugaro") so agent activity is visible | D472: the PR author is the token's name; the docs already say to name a shared token | **Exists** (R10) |
| Optional expiry, warn near it | D462 added "an optional expiry"; `secret_expires` and doctor (D503c) | **Exists** (F4) |
| Store coverage as a pattern, including `workspace/*` | D464 (dc73): `*` inside one segment of `allow_data_to`, never a bare `*` or a wildcard owner | **Exists as authorization.** D2354 keeps it as authorization |
| The secret in Secret Manager, the record a reference | D506 (2026-10-27): the upstream Bitbucket token lives **in the bundle**, written by `fugaro creds set bitbucket-token`, for Q6's one secret and one grant; the rejected alternative was "a project secret with a second grant" | **The spec's letter is already ruled against once.** D2350 follows D506 and extends it to a key family |
| Workspace and project tokens; Premium; 25 tokens per repository, project and workspace | `docs/bitbucket-credentials/reconciliation.md` §3 (read 2026-10-26): both are Premium, the limit of 25 cannot be raised, and an access token cannot query or manipulate permissions (swagger); whether Free and Standard plans may create repository tokens is UNVERIFIED | **Confirms the spec's premise** that no endpoint says what a token reaches (F7), and leaves the lower-plan claim UNVERIFIED (U8) |
| The proxy narrows to the job's repositories and to read or write | B1.4/B1.5 (`Decide`, `CheckPRWrite`), `proxy: true` and the node's `bitbucket_token` block (BW-T2.1), live in the released 0.22.0 | **Exists** |
| Use several tokens, swapping per request | Nothing. The workspace-token plan's S2 re-plan (D493) kept the node's block as data and moved enforcement to the service, with one token | New (F1) |
| Bitbucket shared jobs unblocked | T2.2, T2.4, T2.6's phase-3 half and T2.7 wait on B5.1 (D1980, D1982); `SharedJobProvider` still admits `github` alone | **Not delivered by this spec.** The token store is independent of B5.1: selection happens per request, after the admission B5.1 builds (F11, F19) |
| Own-identity of a token on comments | T3.3's trust-path half is **kept and owed** (D1960 reading, 2026-11-10): `comments.go`'s `selfUser` and `followup/select.go`'s `MarkersFromAnyone`, and four named tests absent from the tree | **Coupled** to per-request swapping (F13, F20) |

## Findings

- **F1 (Wrong).** The spec treats the proxy as able to choose among tokens. `Bundle.BitbucketToken()` answers one entry and the loader's key set is closed (`bitbucket-token` is the only Bitbucket git key; `claude-oauth-token-bitbucket-token` is refused so that one name never means two credentials). Holding several tokens is a schema, loader, `creds set`, `creds ls` and doctor change, not a routing change. See D2350.
- **F2 (Rule).** The spec keeps "the secret itself in Secret Manager, the record a reference". The credentials service reads one document with one accessor grant (Global Constraint 12, `docs/credentials-service`; restated in `docs/api-key-review/implementation-plan.md`), and that design is what lets the loader fail closed. One secret per token would add a grant per token. See D2350.
- **F3 (Exists, and Gap).** Coverage already exists as `allow_data_to` / `allow_repos`, but as the operator's authorization over the *one* token. The spec's coverage is a different fact (what a given token can reach). Merging them would let a workspace token's `ws/*` admit every present and future repository to every run, the reach D462 accepted only through an explicit allow-list. See D2354.
- **F4 (Exists).** Expiry is recorded and warned about (`secret_expires`, keyed by a secret's logical name), and `bitbucket-token-valid` is the periodic re-check. Both are keyed to the single name `bitbucket-token` and need to be keyed per token.
- **F5 (Exists, partly).** A 429 with `Retry-After`, an exhausted, backed-off or broken credential, and a probe are built for model credentials under `creds/marks/<name>`. Reusing them for a Bitbucket token means generalising what a mark's name can be and what a probe's minimal call is; today `credsvc.probe` asks a model question. See D2356.
- **F6 (Gap).** The proxy is a multi-instance Cloud Run service. "Keeps track of how many requests each upstream token has made" has no shared counter today; an in-instance count under-reports by the instance count. See D2356.
- **F7 (Wrong, partly).** The spec says Bitbucket has no endpoint that says what a token can access. The adapter already calls `GET /2.0/user` (`internal/gitprov/bitbucket/comments.go`), and a recorded fixture (`internal/gitprov/bitbucket/testdata/comments.json`) shows `kind: repository_access_token` in its answer. Whether the same call names a project or workspace token, or returns any scope header, is unverified (U1, U2). The conclusion stands for coverage (nothing lists what a token reaches), so the operator still states it.
- **F8 (Gap).** Bitbucket has no dry-run for a push or a pull-request write. "Checks that the scopes it needs work, using read-only or dry-run calls where possible" can prove read scopes and coverage; the write scopes cannot be proven without creating something. See D2353.
- **F9 (Gap, trust boundary).** A multi-token store raises the cost of a compromise of the bundle from one token to all of them (the service already holds one), and **choosing the token is now a security decision**: the wrong choice sends a wider token's authority to a request that a narrower one could serve. The design holds the line three ways: selection happens only after `Decide` (the operator's allow-list and the job's pairing), the choice is narrowest first (repository, project, workspace), and a coverage entry that over-claims can only fail closed (Bitbucket answers 403, the audit line records it). A job never receives a token.
- **F10 (Gap).** `Decide` takes the canonical repository from the *pairing* of the calling job account, and the proxy forwards for that repository. For a shared job the pairing is a profile's `allow_repos` (`docs/shared-jobs-default` D2302), so a request names one repository at a time and per-request selection is possible without changing who may ask. Submodule and LFS requests (`docs/shared-jobs-default` N-tasks) are classified per request as well.
- **F11 (Rule).** `docs/shared-jobs-default` R7 says the proxy forwards "the one workspace token in the credentials bundle", and its F15 and D2314 say one workspace token per project. This spec amends both: a project may hold any mix of tokens, all within one workspace (A5). Tasks S2 and S6 of that plan read the token store through the interface this plan's contract defines.
- **F12 (Gap).** The audit line names "the bundle's Bitbucket token" as the credential (`gitproxyAct`). With several tokens it must name which one (by its name, never a value), or an operator cannot tell which token a refused call used.
- **F13 (Gap).** Own-comment and own-identity detection calls `GET /user` through the adapter. A proxied run's adapter gets several bot identities in one run once tokens swap per request. Whether the proxy's classification admits `GET /2.0/user` at all is unverified (U3); the answer decides whether "own" is one identity or the set of Fugaro's tokens' identities.
- **F14 (Rule).** CLAUDE.md's hard rule: `fugaro update` is the whole upgrade, safe with runs in flight. An installation holding the single `bitbucket-token` and its `allow_data_to` list must converge with no operator step: see R3. A run that started under the release behind keeps working because the legacy key stays readable.
- **F15 (Rule).** "A field never changes meaning": the node's `bitbucket_token` block gains fields (R5) and keeps `allow_repos` and `proxy` as they are; the bundle schema gains a key family and keeps `bitbucket-token`. No existing name changes meaning.
- **F16 (Gap, completeness).** The spec gives no number for the project cache TTL, the re-check cadence or the expiry warning lead time. They are open items O1 to O3, with proposals.
- **F17 (Exists).** Naming the token ("Fugaro") for identity is documentation only (D472); `docs/git-providers.md` and the Bitbucket setup text are where it goes.
- **F18 (Gap, two stores).** A Bitbucket token lives in **two** places today, for two paths. The proxy forwards the **bundle's** token (D506). A job that is not proxied, and every job until the flip (B1.13 part two, draft #2166, held on Check 191; `ProxiedGitSecretNames` is empty), still **mounts** the Secret Manager secret `bitbucket-token`, repository copy first and then the project copy (`infra.ResolveSecretID`, D466). The spec's types apply to the proxy's store. The mounted path keeps one project copy and its own `allow_data_to`, and is unchanged (D473). Until the flip an operator holds the same token in both places; nothing synchronises them. The plan states that, and `doctor` names a token present in one store and absent from the other for a repository that needs both.
- **F19 (Rule).** B5.1 (#2545, the shared-job admission at the service) is a draft, and `docs/shared-jobs-default` lists it as a prerequisite. This work does not depend on it for correctness (a per-repository proxied job is already served), but a Bitbucket shared job reaches the multi-token store only after B5.1 and B5.3. The plan's integration task waits on them, and says so.
- **F20 (Rule, owed work).** T3.3's trust-path half of `docs/bitbucket-credentials` is still owed and is what per-request swapping stresses: `selfUser` decides whether a comment is the run's own by asking `GET /user` with the run's one token, and without it `MarkersFromAnyone` honours markers from any author. With several tokens in one run the "own" identities are a set. This work owns the identity-set half (the four tests T3.3 names are written against the set) and does not re-open the trust rules themselves.
- **F21 (Rule).** D465's trust statement stands and widens by one fact: the node's block is derived from the publisher's own local config, so whoever publishes also states each token's coverage. Coverage in the node is not a secret (a project UUID and a workspace slug), but the node is readable by every run token of the job, so it tells a hostile agent which repositories a token reaches. The block already tells it `allow_repos`; coverage adds the token's name and type and nothing the agent could use to take a token.
- **F22 (Rule).** The API-root pin (T2.8, D474) is untouched and still correct: the proxy's upstream hosts are constants (`bitbucket.DefaultOAuthRoot`, `bitbucket.DefaultBaseURL`). A multi-token proxy keeps that; a token is never sent to a root the owner has not listed.
- **F23 (Gap).** The workspace-token plan's U1 (do project and workspace tokens authenticate as `x-token-auth` for git and Bearer for REST) and U3 (does a project token cover a repository moved in later) are still unverified, and Check 180 (its live check, USER-RUN, NOT RUN) is the settling run. This work's own live checks join Check 180's sandbox need, and no Premium sandbox is known (D468).

## Decisions

| # | Question | Recommendation | Answer |
| --- | --- | --- | --- |
| D2350 | Where do stored tokens live (F1, F2) | Bundle keys, one per token (a `bitbucket-token-<name>` family beside the legacy `bitbucket-token`); the record holds the key name | **Bundle keys (as recommended), 2026-11-13.** Departs from the spec's letter ("a reference to Secret Manager") and follows D506, which ruled the same against a project secret with a second grant: one document, one grant. |
| D2351 | Where do the record's non-secret parts and its status live | Local config, derived into the node; status in the service's creds state store | **Local config, derived into the node (as recommended), 2026-11-13.** Type, coverage and expiry are the operator's, like `allow_data_to` and `secret_expires`; verifiedAt and failing marks live in the creds state store beside the model marks. |
| D2352 | Where is a token checked when added | In the CLI, at `creds set`, refusing before it writes; the service re-verifies on any refused upstream call | **In the CLI (as recommended), 2026-11-13.** |
| D2353 | How is the write scope checked at add time (F8) | Read probes plus first use: report write as "not verified", fail on first use with a precise message | **Read probes plus first use (as recommended), 2026-11-13.** Nothing is created in the user's workspace. |
| D2354 | Coverage against `allow_data_to` (F3) | Both; a call needs both | **Both (as recommended), 2026-11-13.** `allow_data_to` stays the operator's authorization (node `allow_repos`); coverage only says which token can serve a repository. `doctor` flags an allowed repository no token covers. |
| D2355 | When no single token covers a job's repositories | Swap per request, narrowest first; fail naming the uncovered repositories | **Swap per request (as recommended), 2026-11-13.** One run can carry several bot identities (F13). |
| D2356 | How far do rate limits go in the first build (F5, F6) | React to 429 with a shared mark and `Retry-After`; count for display; no proactive queue | **React to 429, count for display (as recommended), 2026-11-13.** A proactive queue waits for measured limits (U4). |

Resolutions (one defensible answer, so not asked):

- **R1.** `type` is stored and `coverage` is validated against it: `ws/repo` is `repo`, `ws/*` is `workspace`, a workspace slug plus a project UUID is `project`. A record whose type and coverage disagree is refused at `creds set` and at `validate`. The type is redundant for matching and is kept for display and for the add-time probe (which call to make).
- **R2.** The project UUID of a repository is looked up with the repository API (`GET /2.0/repositories/{workspace}/{repo}`, `project.uuid`) the first time a project token is considered for it and cached **in the instance's memory** with a TTL. A 403 or 404 on a call evicts the entry and the next request looks it up again. A stale entry costs one extra lookup or one refused call, never a wrong grant, because `Decide` has already admitted the repository. The spec's cache stands as written; sharing it across instances is not needed. The lookup itself is made with a token that covers the repository (narrowest first), never with a wider one for the sake of the lookup.
- **R3.** Migration, inside `fugaro update`: an installation with the legacy `bitbucket-token` key keeps it as the first record. Its coverage is its `allow_data_to` list as the operator wrote it, and its type is not asserted (it was written before types existed). Nothing is re-entered. A new record is added with `creds set bitbucket-token-<name>`. A test pins that an installation at the previous version converges (F14).
- **R4.** A token's failure is marked per token in the shared state: a 401 marks it broken (an operator clears it, as for a model credential); a 429 marks it throttled until `Retry-After`; a 403 or 404 evicts the project cache entry once, then refuses that request with the class and does **not** mark the token (a token that lacks one repository is not broken). Requests for other repositories continue on other tokens or the same one.
- **R5.** The node's `bitbucket_token` block gains the derived coverage per token (no secret, no name of a secret), so the runner and the doctor can state which repositories have no covering token; `allow_repos` and `proxy` keep their meaning, and the block's reader tolerates the new member only at a version that knows it.
- **R6.** Expiry: `secret_expires` keyed per token name; the existing doctor warning and the console read it.
- **R7.** Throttling is shown by a new field (`throttled_until`, `requests_in_window`) on the `--json` output that already lists the bundle's names, never by reusing an existing field's name.
- **R8.** The optional user-scoped Atlassian API token is considered and declined here (the spec marks it "later"). The record's type set is closed at three; a fourth is a version bump of the record.
- **R10.** The published scope list is D470's (`repository:write` and `pullrequest:write`, each implying its read) and the naming advice is D472's; this work cites both and writes neither again. The spec's pick-list at `creds set` offers D470's list and the three types.
- **R11.** No synchronisation between the bundle and the mounted Secret Manager copy (F18): the mounted path is unchanged until the flip retires it, and `doctor` reports a repository that needs a token in a store that lacks one.
- **R9.** The proxy writes the token's **name** on the audit line (F12), never its value, as the model credentials' lines name theirs.

## Open tensions and open items

No open tension. Three amendments of earlier rulings are recorded rather than asked, because each is the owner's own earlier answer extended and not contradicted: D461 (any mix of the three types, several at once), D506 (a key family in the bundle instead of one key) and F11's reading of "one token" in `docs/shared-jobs-default` R7 and D2314 (the one-workspace limit stays). Follow-through owed to the older plans: a dated pointer in each of `docs/bitbucket-credentials` and `docs/bitbucket-workspace-token` READMEs, and T3.3's identity half (F20).

- **O1.** The project cache TTL. Proposal: 10 minutes.
- **O2.** The re-check cadence for stored tokens. Proposal: `doctor --check-bitbucket-token` on demand, plus the service's own re-verification on a refused call; no scheduled job in the first build.
- **O3.** The expiry warning lead time. Proposal: the lead the doctor already uses for `secret_expires`.

The owner may answer these at any time; the plan builds with the proposals and states them as constants.

## UNVERIFIED

- **U1.** Whether `GET /2.0/user` answers for a project or workspace access token, and whether `kind` names the token's type there. Settled by one call with each token type; the owner or anyone holding a Premium sandbox workspace can run it. It would let `creds set` infer the type.
- **U2.** Whether Bitbucket returns a scope header (such as `X-OAuth-Scopes`) for an access token. Settled by the same call. If it does, `creds set` can prove the write scopes without a write (D2353 then improves, with no change of decision).
- **U3.** Whether `gitproxy.Classify` admits `GET /2.0/user` on the proxy route. Settled by reading `internal/gitproxy/rules.go` in the contract task; it decides F13.
- **U4.** Bitbucket Cloud's published request limits for access tokens and whether they are per token or per workspace. Settled by Atlassian's rate-limit page and one measured run; it decides when a proactive queue is worth building.
- **U5.** That a project access token is accepted as `x-token-auth` for git over HTTPS and as Bearer for REST (the same item as `docs/bitbucket-workspace-token` U1, Check 180, not run).
- **U6.** Whether a project token covers a repository moved into its project later (`docs/bitbucket-workspace-token` U3). The per-request lookup of R2 is correct either way.
- **U8.** Whether Free and Standard plans may create repository access tokens (the spec's premise for "lower plans may only have repo tokens"; `docs/bitbucket-credentials/reconciliation.md` §3 leaves it UNVERIFIED). Settled by Atlassian's plan page or one attempt on a Free workspace.
- **U7.** Whether a Premium sandbox workspace exists to run the live checks. None is known (`docs/bitbucket-workspace-token`); the live checks are owner-run.
