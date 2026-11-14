# Implementation plan: Bitbucket token types in the credential proxy

Builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md) (decisions D2350–D2356, resolutions R1–R13, findings F1–F23; amended after review round 1, see the reconciliation's R2, R3, R5, R6, R12, R13).
**Nothing is implemented, and no task is approved: each needs the owner's explicit go.** The answers that shaped the plan:
tokens live in the bundle as a key family spelled `bitbucket-token--<name>` (D2350, following D506; spelling R13); type, coverage and expiry are the operator's local config, derived into the node, and
status lives in the creds state store (D2351); a token is checked in the CLI when added (D2352), read scopes only, write scopes on first use (D2353);
coverage never widens `allow_data_to` (D2354); a request is served by the narrowest covering token, swapped per request (D2355); a 429 marks the token
throttled and counts are shown, no proactive queue (D2356). The mounted Secret Manager copy is on its way out and is **not designed for** (F18, R11).

The decoupling pass is **on** (`fugaro settings planning.decoupling --json --repo dimipaun/fugaro` reads `true`, the default). Rigor mode per path was
not read: every task that touches credentials, the proxy or `SECURITY.md` is flagged opus with an independent review, and the contract, which is over
credentials, gets the strictest rigor and an independent review before any side is written against it.

## Prerequisites outside this plan

- **P1.** Nothing blocks the token store: a per-repository proxied job is served today (`proxy: true`, released 0.22.0). A Bitbucket **shared** job reaches the store only after B5.1 (#2545), B5.3 (`docs/secrets-brokering`) and `docs/shared-jobs-default` S2, which this plan does not build (F19). The plan changes none of their files.
- **P2.** Check 191 (owner-run) is the proxy's own live proof and is not a task here.
- **Launch gates (not graph edges, because the schema draws no edge from a provider to its contract)**: T1, T2, T5, T6 and T7 compile against C1's types or provide what its interfaces describe; they may launch beside C1 but their pull requests merge after C1 does. CR1 is never launched at level 0 although the graph reads it ready there. T3, T4, T8, T13 and T16 are written against C1 and wait for it. #2545 (`pairShared`) is a draft and not on main.
- **Checks 590 and 591 need a Bitbucket Premium sandbox workspace**, none of which is known (D468 of `docs/bitbucket-workspace-token`). They are owner-run and gate no task.

## Conventions

- **Always**: tests before the commit; a targeted `go test ./<pkg>/ -run '<names>' -count=1` (CLAUDE.md); `gofmt -l .` empty; a doc change runs `go test ./internal/cli/ -run Docs`; a task's pull request names its `T#` and `D#`; the audit line, log line and every error name a token and never its value.
- **Ask first**: a bundle schema or node-block change (a field or key family is added, never retyped: CLAUDE.md "a field never changes meaning"; `schemas/creds-bundle.schema.json` and `TestBundleSchemaMatchesTheLoader` move together); a policy version bump (none is planned: the node gains a top-level key, R5; if a task finds one is needed it raises it); a new dependency; any `.github/workflows` change (stage it under `docs/pending-workflows/`).
- **Never**: a secret-shaped literal in a test, doc or fixture (test tokens are plain words, not token-shaped strings); a live cloud action by an agent; a TODO comment; a token in a log, an error or an audit line; a token sent to a host other than Bitbucket's own constants (F22); touching the mounted `bitbucket-token` secret path (F18).
- Hermetic tests only: `internal/gcpfake`, `internal/gitprov/httpfixture`, `internal/testutil`, `t.TempDir()`.
- **Hard rule** (CLAUDE.md): T12 is a `fugaro update` step; it is idempotent, safe with runs in flight, has a row in `docs/design/update-in-flight.md` and a test that an installation at the previous version converges. The release notes' `### For operators` is "run `fugaro update`" and what it asks, which here is nothing.
- Reviewer tier **opus + independent review** for: C1, T1, T3, T4, T5, T6, T7, T8, T9, T11, T12, T13, T16, I1, T14, CK (credentials, the proxy, the node, `SECURITY.md`). Standard for T2, T10, T15.
- Path rules that load: `.claude/rules/credentials-service.md` (`internal/creds/**`, `internal/credsvc/**`, `SECURITY.md`).

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| A token is chosen only after `Decide` admitted the repository: the selector never widens what a run may reach | T8, I1 |
| The selection is the narrowest covering token (repository, then project, then workspace); a request no token covers is refused naming the repository, not a token | T4, T8 |
| A coverage entry that over-claims fails closed: Bitbucket answers 403, the call is refused and audited, and no other token is tried in its place | T9, I1 |
| The project UUID is looked up per repository with a token that covers it by a narrower rule, cached per instance, and a 403 or 404 evicts the entry | T5, T9 |
| A 401 marks one token broken, a 429 marks it throttled until `Retry-After`, a 403 or 404 marks nothing; other tokens keep serving | T6, T9 |
| The audit line names the token used (name only), and a refusal class names no token | T8 |
| `creds set` refuses a token that does not work with one precise message and writes nothing; a write scope it cannot prove is reported "not verified" | T7, T11 |
| Type and coverage that disagree are refused at `creds set` and at `fugaro validate` | T2, T11 |
| The node block carries name, type, coverage and identity and no value, no secret name and no secret version | T3 |
| `allow_data_to` stays the operator's authorization; coverage never replaces it, and `doctor` names an allowed repository no token covers | T3, T10 |
| An installation holding only the legacy `bitbucket-token` keeps working with no step, and a run in flight is unaffected | T12, I1 |
| The proxy's upstream hosts remain the adapter's constants; no record can name a host | T8 |
| The own-identity set reaches the adapter and a comment by any of Fugaro's tokens is the run's own | T13, I1 |
| The bundle's size bound still holds with many tokens, and a bundle holding `claude-oauth-token-bitbucket-token-ci` still loads | T1 |
| A token the deployed service is too old to read is refused at `creds set`, never written (an older loader refuses the whole bundle) | T11 |
| A run cannot mark a token broken with an ordinary 401: the proxy confirms with `GET /2.0/user` first | T9 |
| The identity set is written only by `creds set` into the state store, never by hand-edited config | T7, T11 |
| Selection, `Decide` and the forward use one read of the policy and the bundle | C1, T8 |
| An empty project UUID never matches a record | T4, T5 |

## Slice 1: the store (contract, bundle, config, node)

Inert until the proxy reads it (I1). A bundle with the new keys is refused by an older loader, so T1 merges before anyone writes such a key.

### C1: the token-store contract
- Acceptance: one package, `internal/bbtokens`, states, and `docs/bitbucket-token-types/contract.md` explains, what every side may rely on: `Record{Name, Type, Coverage []string, ProjectUUID, ProjectKey, Expires}` and `Status{Mark, VerifiedAt, Identity (account UUID), Count}`; `Type` (`repo`, `project`, `workspace`, or untyped for the legacy key) and the coverage forms R1 pairs with them; the **snapshot** `Snapshot{Policy, Bundle}` that `Decide`, selection and the forward all use (one read per request); `Selector.Select(ctx, snap, canonical string, write bool) (Choice, error)` and `Choice{Name}`, where `canonical` is the pairing's repository (also for `GET /user`, whose request carries none); `BundleTokens` (the names the bundle holds and each entry's value accessor); `ProjectResolver.ProjectOf(ctx, canonical string, tok Name) (uuid string, err error)` and `ProjectResolver.Evict(canonical string)`; `StatusStore` (read and write a `Status`, increment a count); the refusal classes (`token_not_covered`, `token_broken`, `token_throttled`, `write_scope_missing`, `no_bitbucket_token`); the node key's JSON shape (R5); the identity-set shape the adapter reads, compared by UUID. An empty project UUID never matches. A stub (`stub.go`) implements `Selector`, `ProjectResolver` and `StatusStore`, returns the shape and does nothing else.
- Files: `internal/bbtokens/bbtokens.go` (new), `internal/bbtokens/stub.go` (new), `docs/bitbucket-token-types/contract.md` (new).
- Interfaces: consumes none; produces everything above. These files belong to C1 and its change requests alone.
- Tests: `TestContractRecordForms` (type and coverage forms agree and disagree), `TestContractEmptyProjectUUIDNeverMatches`, `TestStubImplementsContract`.
- Command: `go test ./internal/bbtokens/ -run 'Contract|Stub' -count=1`
- Red step: the form test fails against a type that accepts `ws/*` as `repo`.
- Depends on: none.
- Commit: `bbtokens: the token-store contract (C1)`
- Reviewer tier: opus with a mandatory independent review (`fugaro review --pr N`) at the strictest rigor before anything is written against it; credentials and a wire format.

### T1: the bundle's token key family (D2350, R13)
- Acceptance: the loader accepts `bitbucket-token--<name>` entries (kind `KindGit`) beside the legacy `bitbucket-token`, with a name held to the account-name rules; `Bundle.BitbucketTokens()` answers them in a stable order with each entry's name; a duplicate name is refused; a bundle holding `claude-oauth-token-bitbucket-token-ci` (a legal account today) still loads; `BitbucketToken()` still answers the legacy entry; schema and loader are held together by `TestBundleSchemaMatchesTheLoader`; a value never appears in an error, log or rendering.
- Files: `internal/credsvc/bundle.go`, `internal/credsvc/bundle_test.go`, `schemas/creds-bundle.schema.json`, `internal/config/config.go` (the key-name function beside `BitbucketSecret`).
- Interfaces: produces the bundle accessor the contract names; consumes none (a provider of C1, no edge).
- Tests: `TestBundleBitbucketTokenFamily`, `TestBundleBitbucketNameCollisions`, `TestBundleStillLoadsAnAccountNamedLikeTheLegacyKey`, `TestBundleSchemaMatchesTheLoader`, `TestBundleValueNeverInErrorOrLog`.
- Command: `go test ./internal/credsvc/ -run 'BundleBitbucket|BundleStillLoads|BundleSchema|BundleValueNever' -count=1`
- Red step: a bundle with `bitbucket-token--ci` is refused today as an unknown key.
- Depends on: none (launch gate: merges after C1).
- Commit: `credsvc: the bundle holds a family of Bitbucket tokens (T1, D2350)`
- Reviewer tier: opus, independent review.

### T2: the local config's token records
- Acceptance: the local config has a `bitbucket_tokens` block of records (name, type, coverage, project UUID or key, `expires`), operator-only like `project_secrets`; identity and `verified_at` are **not** members (R12: they live in the state store); R1's agreement of type and coverage is checked at load and in `fugaro validate`, naming the record; `project` accepts a workspace slug plus a project UUID or key; `expires` is the one expiry source for a typed token (R6); unknown members are errors (strict decode).
- Files: `internal/localcfg/localcfg.go`, `internal/localcfg/bitbucket_tokens_test.go` (new), `internal/config/bitbucket_tokens.go` (new), `internal/config/bitbucket_tokens_test.go` (new).
- Interfaces: produces the records the contract describes; consumes none.
- Tests: `TestBitbucketTokenRecordForms`, `TestBitbucketTokenRecordRefusesTypeMismatch`, `TestLocalConfigRejectsUnknownTokenMember`.
- Command: `go test ./internal/localcfg/ ./internal/config/ -run 'BitbucketToken|UnknownTokenMember' -count=1`
- Red step: a record typed `repo` with coverage `ws/*` is accepted (the block does not exist, so the tests fail to find it).
- Depends on: none.
- Commit: `localcfg: Bitbucket token records with typed coverage (T2, R1)`
- Reviewer tier: standard.

### T3: the node carries each token's coverage
- Acceptance: `projectpolicy` derives a new top-level node key `bitbucket_tokens` (R5), holding each record's name, type and coverage patterns (provider-qualified like `allow_repos`) and, from the state store, its identity UUID; never a value; `bitbucket_token` (`allow_repos`, `proxy`) is untouched and `Version` does not move; a node without the key reads as today; a reader that does not know the key tolerates it (P8), pinned by a test against the previous reader's decode; the key is derived from records only, so a config with none publishes none.
- Files: `internal/projectpolicy/bitbucket.go`, `internal/projectpolicy/bitbucket_test.go`, `internal/projectpolicy/read.go`.
- Interfaces: consumes C1's node-key shape and record types; produces the key the proxy and the runner read.
- Tests: `TestDeriveBitbucketTokensKey`, `TestNodeWithoutTokensKeyReadsAsBefore`, `TestNodeTokensKeyCarriesNoValue`, `TestPreviousReaderToleratesTheTokensKey`.
- Command: `go test ./internal/projectpolicy/ -run 'DeriveBitbucketTokens|NodeWithoutTokens|NodeTokensKey|PreviousReaderToleratesTheTokens' -count=1`
- Red step: a derivation with two records publishes no `bitbucket_tokens` key.
- Depends on: C1 (the shape; broken edge to T2).
- Commit: `projectpolicy: the node states each Bitbucket token's coverage (T3, R5)`
- Reviewer tier: opus, independent review.

## Slice 2: selection, lookup, status and verification

Four tasks that run side by side against C1.

### T4: the selector
- Acceptance: given records, a canonical repository and a `ProjectResolver`, `Select` returns the narrowest covering token (exact repository, then project by UUID, then workspace slug) or `token_not_covered` naming the repository; a project token is considered only when the resolver says its UUID matches; equal candidates resolve by name order; a throttled or broken token is skipped (status read through the contract) and the narrowest **usable** token serves, which may be a wider one (R12); when none remains the class is `token_throttled` or `token_broken`; the legacy untyped `bitbucket-token` ranks after every typed record and covers what its `allow_data_to` list says; an empty project UUID never matches.
- Files: `internal/bbtokens/select.go` (new), `internal/bbtokens/select_test.go` (new).
- Interfaces: consumes C1's `ProjectResolver` and `StatusStore`; produces the real `Selector`.
- Tests: `TestSelectNarrowestFirst`, `TestSelectProjectMatchesByUUID`, `TestSelectNamesUncoveredRepository`, `TestSelectSkipsThrottledAndBroken`.
- Command: `go test ./internal/bbtokens/ -run 'Select' -count=1`
- Red step: with a repository token and a workspace token both covering, the workspace token is returned.
- Depends on: C1 (broken edge to T5).
- Commit: `bbtokens: choose the narrowest covering token (T4, D2355)`
- Reviewer tier: opus, independent review (the choice of a credential); a mutation check: a selector that returns the widest token must fail a test.

### T5: the project-UUID lookup and its cache
- Acceptance: `ProjectOf` asks `GET /2.0/repositories/{workspace}/{repo}` once per repository per TTL (O1, a named constant, proposal 10 minutes) with the candidate project token itself (R2): a 403 or 404 means not covered, a 200 matches when `project.uuid` equals the record's; never with a workspace or repository token; a 403 or 404 evicts the entry and the next call asks again (`Evict`); the lookup goes to Bitbucket's constant base URL only; the cache is per instance and bounded in size.
- Files: `internal/bbtokens/projectlookup.go` (new), `internal/bbtokens/projectlookup_test.go` (new).
- Interfaces: produces the `ProjectResolver`; consumes none.
- Tests: `TestProjectOfCachesWithinTTL`, `TestProjectOfEvictsOn403And404`, `TestProjectOfUsesTheCandidateProjectToken`, `TestProjectOfEmptyUUIDNeverMatches`, `TestProjectLookupHostIsConstant`.
- Command: `go test ./internal/bbtokens/ -run 'ProjectOf|ProjectLookup' -count=1`
- Red step: a second call within the TTL makes a second upstream request.
- Depends on: none (launch gate: merges after C1).
- Commit: `bbtokens: look up a repository's project once and cache it (T5, R2)`
- Reviewer tier: opus, independent review.

### T6: a Bitbucket token's shared status marks
- Acceptance: a Bitbucket token's `Status` (mark, `VerifiedAt`, identity UUID, count) is stored under **its own subtree** of the creds state store, so the readers of model marks (`accounts.go`, `doctor.go`, `review.go` through `Marks()`) never see it; a 401, after the proxy's `GET /user` confirmation, writes `broken`, a 429 writes `exhausted` until `Retry-After` (a backoff when it names none); the probe for a backed-off Bitbucket token is `GET /2.0/user` and nothing that writes; a request count per window is kept per token for display and is advisory (no queue, D2356); the existing model credentials' marks and probe are unchanged.
- Files: `internal/creds/state.go`, `internal/creds/marks.go`, `internal/creds/marks_test.go`, `internal/credsvc/probe.go`, `internal/credsvc/probe_test.go`.
- Interfaces: produces the `StatusStore`; consumes none.
- Tests: `TestBitbucketTokenStatusSubtree`, `TestModelMarkReadersNeverSeeABitbucketToken`, `TestBitbucketTokenProbeIsAReadOnlyCall`, `TestModelCredentialMarksUnchanged`, `TestBitbucketRequestCountWindow`.
- Command: `go test ./internal/creds/ ./internal/credsvc/ -run 'BitbucketToken|ModelCredentialMarks|BitbucketRequestCount' -count=1`
- Red step: marking `ci` broken also reads as the model account `ci` broken.
- Depends on: none (launch gate: merges after C1).
- Commit: `creds: status marks for a Bitbucket token (T6, D2356)`
- Reviewer tier: opus, independent review; a mutation check (the marks gate every credential's selection).

### T7: the add-time verifier
- Acceptance: `Verify(ctx, token, record)` returns a `Result` with `OK`, the read scopes proven, the write scopes `not verified`, the account UUID `GET /2.0/user` answers (the identity, compared as `selfUser` compares `user.UUID`), and one precise message per refusal (`the token does not cover workspace X`, `missing repository read`, `the token has expired`, `Bitbucket refused the token`); it makes read calls only, within the stated coverage (the repository, the project or the workspace); it never prints or returns the token; it records what a scope header says when Bitbucket returns one (U2).
- Files: `internal/bbtokens/verify.go` (new), `internal/bbtokens/verify_test.go` (new).
- Interfaces: consumes none; produces the verifier `creds set` calls (T11).
- Tests: `TestVerifyRepoWorkspaceAndProjectCoverage`, `TestVerifyNamesWhatIsMissing`, `TestVerifyMakesOnlyReadCalls`, `TestVerifyReportsWriteNotVerified`, `TestVerifyNeverReturnsTheToken`.
- Command: `go test ./internal/bbtokens/ -run 'Verify' -count=1`
- Red step: a token the fake answers 403 on the repository read is reported OK (there is no verifier yet, so the test fails to build against the missing symbol).
- Depends on: none.
- Commit: `bbtokens: verify a token on the way in (T7, D2352, D2353)`
- Reviewer tier: opus, independent review.

## Slice 3: the proxy

### T8: the proxy chooses a token per request and names it
- Acceptance: after `Decide` admits a call, `bitbucketProxy` takes one snapshot of the policy and the bundle (the one `Decide` and the forward use) and asks `Selector.Select` for the pairing's canonical repository (also for `GET /user`, whose request names none) and the `write` flag, forwards with the chosen entry, writes the token's **name** on the audit line (`credential`), and refuses with the contract's class when none is chosen; with only the legacy `bitbucket-token` and no node `tokens` member the behaviour is today's exactly; the upstream URL is still rebuilt from the classified parts (no byte of the caller's path is copied) and no record can name an upstream host.
- Files: `internal/credsvc/bitbucketproxy.go`, `internal/credsvc/bitbucketproxy_test.go`, `internal/credsvc/server.go` (the `Config` field).
- Interfaces: consumes C1's `Selector` and refusal classes (against the stub); produces the proxy's selection step.
- Tests: `TestProxyChoosesTokenAfterDecide`, `TestProxyAuditNamesTheToken`, `TestProxyRefusesUncoveredRepository`, `TestProxyLegacySingleTokenUnchanged`, `TestProxyNeverForwardsToARecordedHost`.
- Command: `go test ./internal/credsvc/ -run 'ProxyChoosesToken|ProxyAuditNames|ProxyRefusesUncovered|ProxyLegacy|ProxyNeverForwards' -count=1`
- Red step: with the stub selecting token `b`, the forward still uses the legacy entry.
- Depends on: C1 (broken edges to T4, T1, T3).
- Commit: `credsvc: the Bitbucket proxy serves each request with its narrowest covering token (T8, D2355)`
- Reviewer tier: opus with a mandatory independent review; a mutation check.

### T9: the proxy reacts to the upstream
- Acceptance: an upstream 401 is confirmed with `GET /user` and, if the token is really refused, marks it broken (an ordinary 401 that `GET /user` does not confirm marks nothing) and refuses the call; a 429 marks it throttled until `Retry-After` and refuses with `token_throttled` and the wait; a 403 or 404 evicts the project cache entry once and refuses that call with its class, **marking nothing** (a 403 on a write whose scope was not verified is `write_scope_missing`, D2353); the next request is served by another covering token or the same one; every reaction writes one audit line.
- Files: `internal/credsvc/bitbucketproxy.go`, `internal/credsvc/bitbucketproxy_react_test.go` (new).
- Interfaces: consumes C1's `StatusStore` (against the stub) and T8's selection step.
- Tests: `TestProxy401ConfirmedMarksBroken`, `TestProxyUnconfirmed401MarksNothing`, `TestProxyWriteScopeMissingMessage`, `TestProxy429MarksThrottledWithRetryAfter`, `TestProxy403EvictsAndMarksNothing`, `TestProxyReactionAuditsOnce`.
- Command: `go test ./internal/credsvc/ -run 'Proxy401|ProxyUnconfirmed401|Proxy429|Proxy403|ProxyWriteScope|ProxyReaction' -count=1`
- Red step: a 403 marks the token broken.
- Depends on: C1 (broken edge to T6); T8 (the same file, `bitbucketproxy.go`).
- Commit: `credsvc: the Bitbucket proxy marks a failing token and backs off (T9, D2356, R4)`
- Reviewer tier: opus, independent review.

### I1: wire the proxy to the real store and prove it
- Acceptance: the stub is deleted; the server builds the real selector (T4) over the bundle's tokens (T1), the node's `tokens` member (T3) built from the config records (T2), the resolver (T5) and the marks (T6); an end-to-end test with `gcpfake` and a fake Bitbucket proves, for a project holding a repository token, a project token and a workspace token: each repository is served by its narrowest covering token; a repository none covers is refused naming it; a 429 on the workspace token throttles it while the repository token keeps serving; an installation with only the legacy key behaves as before; the node's identity set reaches the adapter through the runner (T16, T13) and a comment by any listed identity is the run's own, proved in `internal/runner` as well as here.
- Files: `internal/bbtokens/stub.go` (deleted), `internal/credsvc/server.go`, `internal/credsvc/tokens_wire.go` (new), `internal/credsvc/bitbucketproxy_tokens_e2e_test.go` (new).
- Interfaces: consumes T1, T2, T3, T4, T5, T6, T8, T9, T13, T16; produces the working store.
- Tests: `TestProxyMixedTokensEndToEnd`, `TestProxyOnlyLegacyTokenEndToEnd`, `TestProxyThrottleDoesNotStopOtherTokens`, `TestAdapterOwnIdentitySetEndToEnd` (with its runner half in T16).
- Command: `go test ./internal/credsvc/ -run 'ProxyMixedTokens|ProxyOnlyLegacy|ProxyThrottleDoesNot|AdapterOwnIdentity' -count=1`
- Red step: with the stub deleted and nothing wired, the proxy refuses every call.
- Depends on: every side above (the integration re-joins each edge C1 broke).
- Commit: `credsvc: wire the token store into the Bitbucket proxy (I1)`
- Reviewer tier: opus, independent review.

## Slice 4: the operator

### T10: `creds ls` and `doctor` show each token
- Acceptance: `fugaro creds ls --json` lists every Bitbucket token by name with `type`, `coverage`, `expires`, `verified_at`, `state` and the **new** fields `throttled_until` and `requests_in_window` (R7; no existing field changes meaning); `doctor` names a token near its expiry (O3), a broken token, and a repository `allow_data_to` admits that no token covers; `--check-bitbucket-token` re-checks every stored token, not only the legacy one (F4); no value is shown; the generated CLI reference follows the changed help.
- Files: `internal/cli/creds.go`, `internal/cli/doctor_brokering.go`, `internal/cli/doctor_brokering_test.go`, `internal/cli/creds_test.go`, `website/content/cli-reference.md`.
- Interfaces: consumes T6's real status and T11's additions to `creds.go`.
- Tests: `TestCredsLsJSONBitbucketTokens`, `TestDoctorNamesUncoveredAllowedRepository`, `TestDoctorWarnsTokenNearExpiry`, `TestDoctorChecksEveryStoredBitbucketToken`, `TestCredsLsAddsFieldsOnly`, and the CLI-reference pin.
- Command: `go test ./internal/cli/ -run 'CredsLsJSONBitbucket|DoctorNamesUncovered|DoctorWarnsTokenNear|DoctorChecksEveryStored|CredsLsAddsFields|DocsWebsiteCLIRef' -count=1`
- Red step: an allowed repository with no covering token produces no doctor line.
- Depends on: T6 (the real status); T11 (the same file, `creds.go`).
- Commit: `cli: show each Bitbucket token and its status (T10, R7)`
- Reviewer tier: standard.

### T11: `creds set` adds a typed, checked token
- Acceptance: `fugaro creds set bitbucket-token--<name>` takes the token on the usual hidden prompt and the type, coverage (`ws/repo`, `ws/*`, or workspace plus project UUID or key; a key is resolved to the UUID by the check and kept for display) and optional expiry **as prompts or as flags** (a managed project gets no prompt); it first applies the serving-release guard `creds set` already applies to `provider-key` (`refuseAProviderEntryTheServiceCannotRead`, `bundleReaderRelease`) so a deployed service too old to read the family is refused before anything is written; runs T7's verifier; on failure prints the one precise message and **writes nothing**; on success writes the bundle entry (T1), the local config record (T2), and `verified_at` and the identity UUID into the state store (R12), says "write scopes not verified" when so, and says a republish (`fugaro update`) publishes the new coverage to runs.
- Files: `internal/cli/creds.go`, `internal/cli/creds_bitbucket.go` (new), `internal/cli/creds_bitbucket_test.go` (new), `website/content/cli-reference.md`.
- Interfaces: consumes T7's verifier, T2's records and T6's status store.
- Tests: `TestCredsSetBitbucketTokenRefusesAndWritesNothing`, `TestCredsSetBitbucketTokenWritesBundleRecordAndStatus`, `TestCredsSetBitbucketTokenReportsWriteNotVerified`, `TestCredsSetBitbucketTokenNeverPrintsTheToken`, `TestCredsSetBitbucketTokenRefusedByAnOldService`, `TestCredsSetBitbucketTokenFlagsAskNothing`, and the CLI-reference pin.
- Command: `go test ./internal/cli/ -run 'CredsSetBitbucketToken|DocsWebsiteCLIRef' -count=1`
- Red step: a token the fake Bitbucket refuses is written to the bundle.
- Depends on: T7 (the real verifier); T2 (the record it writes, in T2's localcfg member); T6 (the status store).
- Commit: `cli: creds set adds a Bitbucket token of any type after checking it (T11, D2352, R12)`
- Reviewer tier: opus, independent review.

### T12: `update` publishes the tokens key and converges an old installation (R3, R5)
- Acceptance: the callers of the policy derivation (`init_policy_migrate.go`, `project_cmd.go`, `project_profile.go`) pass the records and the state store's identities so `fugaro update` and `project policy publish` publish `bitbucket_tokens`; an installation at the previous version, holding only the legacy `bitbucket-token`, converges with no step and no prompt (no record is written: R3); a second run changes nothing; a run in flight is unaffected (the node is read once at bootstrap) and the in-flight document's table gains the row for this step.
- Files: `internal/cli/init_policy_migrate.go`, `internal/cli/project_cmd.go`, `internal/cli/project_profile.go`, `internal/cli/update_bitbucket_tokens_test.go` (new), `docs/design/update-in-flight.md`.
- Interfaces: consumes T3's derivation and T2's records.
- Tests: `TestUpdatePublishesTheTokensKey`, `TestUpdateWithOnlyTheLegacyTokenPublishesNoKey`, `TestInstallationAtPreviousVersionConvergesBitbucketTokens`, `TestUpdateTokensStepIsIdempotent`; the existing `TestUpdateLeavesARunInFlightRunning` (in `internal/cli/update_in_flight_test.go`) is run, not copied.
- Command: `go test ./internal/cli/ -run 'UpdatePublishesTheTokensKey|UpdateWithOnlyTheLegacy|InstallationAtPreviousVersionConvergesBitbucket|UpdateTokensStepIsIdempotent|UpdateLeavesARunInFlightRunning' -count=1`
- Red step: with records present, `update` publishes a node with no `bitbucket_tokens` key.
- Depends on: T2 (the records); T3 (the derivation it calls).
- Commit: `update: publish each Bitbucket token's coverage (T12, R5)`
- Reviewer tier: opus, independent review (an `update` step on an existing installation).

### T13: own-identity over the set of token identities (F20)
- Acceptance: `selfUser` treats a comment whose author is in the identity set (account UUIDs, as `selfUser` compares `user.UUID`) as the run's own; with no set it is today's behaviour; `MarkersFromAnyone` is read only when the set is empty and the account is unknown; the trust rules are unchanged and pinned by the four tests `docs/bitbucket-credentials` T3.3 names (`TestAgentCommentAsPersonNeverSteers`, `TestPRAuthorIDPathCovered`, `TestNoAccountScopeMarkersPinned` here, and `TestReviewModeSameRepoPRByConsumerOwner` in `internal/runner/reviewmode_test.go`), written against the set.
- Files: `internal/gitprov/bitbucket/comments.go`, `internal/gitprov/bitbucket/comments_test.go` (new), `internal/followup/select.go`, `internal/followup/select_test.go`, `internal/runner/reviewmode_test.go`.
- Interfaces: consumes C1's identity-set shape; T16 hands the set to it.
- Tests: the four named above plus `TestSelfUserAcceptsAnyTokenIdentity`.
- Command: `go test ./internal/gitprov/bitbucket/ ./internal/followup/ -run 'Self|Steer|PRAuthor|NoAccountScope' -count=1 && go test -timeout 40m ./internal/runner -run ReviewModeSameRepo -count=1`
- Red step: with two identities in the set, the second token's comment is not the run's own.
- Depends on: C1 (broken edge to T3).
- Commit: `followup: a comment by any of the project's token identities is the run's own (T13, F20)`
- Reviewer tier: opus, independent review (it decides whom a run trusts).

### T16: the runner hands the identity set to the adapter
- Acceptance: on a proxied run the runner reads `bitbucket_tokens` from the node it already reads once at bootstrap, builds the UUID set and passes it to the Bitbucket adapter's option; a node without the key leaves the adapter as today; a run's own set is never taken from the task, the environment or the workspace.
- Files: `internal/runner/project.go`, `internal/runner/projectpolicy.go`, `internal/gitprov/bitbucket/service.go`, `internal/gitprov/providers/providers.go`, `internal/runner/bitbucket_identities_test.go` (new).
- Interfaces: consumes T3's node key (against C1's shape) and T13's adapter option.
- Tests: `TestRunnerPassesTheIdentitySetToTheAdapter`, `TestRunnerWithoutTheTokensKeyKeepsToday`, `TestRunnerNeverReadsTheSetFromTheEnvironment`.
- Command: `go test -timeout 40m ./internal/runner -run 'RunnerPassesTheIdentitySet|RunnerWithoutTheTokensKey|RunnerNeverReadsTheSet' -count=1`
- Red step: with two identities in the node the adapter's set is empty.
- Depends on: C1 (the node key's shape); T13 (the adapter option, in the adapter files they share).
- Commit: `runner: the node's token identities reach the Bitbucket adapter (T16, F20)`
- Reviewer tier: opus, independent review.

## Slice 5: documents and checks

### T14: the documents say it
- Acceptance: `docs/git-providers.md`, `SECURITY.md` and the credentials runbook state: the proxy accepts any mix of the three types; the scopes are D470's and the token name is D472's, cited and not restated; coverage never widens `allow_data_to`; the proxy picks the narrowest covering token and several bot identities can appear in one run; a failing token is marked and shown; the reach of each type; and the Premium and 25-token facts only as far as they are verified. Pinned sentences change in their tests in the same commit.
- Files: `docs/git-providers.md`, `SECURITY.md`, `docs/credentials-service/runbook.md`.
- Interfaces: consumes the behaviour of I1 and T11.
- Tests: the existing Docs pins, moved with the wording.
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: a Docs pin on the old "one token" sentence fails until both places change.
- Depends on: I1, T11 (it documents what they build).
- Commit: `docs: the Bitbucket proxy accepts repository, project and workspace tokens (T14)`
- Reviewer tier: opus, independent review (`SECURITY.md` and the credentials rule load).

### T15: dated pointers in the earlier plans
- Acceptance: each of `docs/bitbucket-credentials/README.md`, `docs/bitbucket-workspace-token/README.md` and `docs/shared-jobs-default/README.md` gains one dated amendment pointing here: D461 extended to any mix and several at once; D506 extended to a key family; R7 and F15 of the shared-jobs plan read as "any mix within one workspace", and shared-jobs S6's "`update` asks once for the one workspace token" now points to `fugaro creds set bitbucket-token--<name>`. No historical text is rewritten.
- Files: the three READMEs.
- Interfaces: none.
- Tests: the existing Docs pins.
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: none beyond the Docs pins; the task is text.
- Depends on: none.
- Commit: `docs: the earlier Bitbucket plans point at the token-types plan (T15)`
- Reviewer tier: standard. `docs/status.md` is moved by whoever merges, not by this task.

### CK: Checks 590 and 591
- Acceptance: `scripts/live-checks/check590.sh` adds a token of each type to a sandbox installation and records what the verifier says for each (it settles U1, U2 and U5, and with Check 180 U6); `check591.sh` runs a two-repository job on a project holding a repository token and a project token and records which token served each repository, the audit lines and a forced 403; both are registered in `docs/gcp-live-checklist.md` as USER-RUN, NOT RUN, each with its `_test.sh`.
- Files: `scripts/live-checks/check590.sh` (new), `scripts/live-checks/check590_test.sh` (new), `scripts/live-checks/check591.sh` (new), `scripts/live-checks/check591_test.sh` (new), `docs/gcp-live-checklist.md`.
- Interfaces: consumes the real proxy (I1) and `creds set` (T11).
- Tests: the two `_test.sh` run hermetically; the checks themselves are the owner's.
- Command: `bash scripts/live-checks/check590_test.sh && bash scripts/live-checks/check591_test.sh`
- Red step: the register has no row 590.
- Depends on: I1, T11.
- Commit: `live-checks: Checks 590 and 591, the Bitbucket token types, owner-run (CK)`
- Reviewer tier: opus, independent review.
- Owner-run: the owner types `bash scripts/live-checks/check590.sh` and then `bash scripts/live-checks/check591.sh` against a Bitbucket Premium sandbox. No agent performs any step of either, and no Premium sandbox is known.

## Parallel execution

**Edges kept** (the dependent needs behaviour or the same file):
- T16 → T13: the adapter option in `internal/gitprov/bitbucket`, and the adapter files they share.
- T12 → T3: it calls T3's derivation (behaviour, not a shape).
- T11 → T6: it writes the status store T6 builds.
- T9 → T8: the same file, `internal/credsvc/bitbucketproxy.go`, and the selection step it reacts to.
- T10 → T11: the same file, `internal/cli/creds.go`. T10 → T6: the listing reads the real marks.
- T11 → T7: `creds set` runs the real verifier. T11 → T2 and T12 → T2: they use T2's localcfg member.
- T14 → I1, T14 → T11: it documents what they build.
- CK → I1, CK → T11: the checks run the real proxy and the real add path.
- I1 → T1, T2, T3, T4, T5, T6, T8, T9, T13, T16: the integration re-joins every edge C1 broke.

**Edges broken by contract** (8), all by **C1** and re-joined in **I1**:
- T8 needed T4 for the selector's signature only.
- T8 needed T1 for the bundle accessor's shape only.
- T8 needed T3 for the node member's shape only.
- T9 needed T6 for the status store's shape only.
- T4 needed T5 for the resolver's signature only.
- T3 needed T2 for the record types only.
- T13 needed T3 for the identity set's shape only.
- T16 needed T3 for the node key's shape only.

**Considered and not broken.** A second contract for the verifier (T11 needing T7): the read reported no gain, because T7 is small and off the critical path, so the pass was undone and T11 depends on T7 directly. T10 reads the real marks, so it waits on T6 and is not a contract consumer.

The graph is in [implementation-plan.graph.json](implementation-plan.graph.json). Its schema draws no edge from a provider to its contract, so T1, T2, T5, T6 and T7 are drawn without one and merge after C1 (the launch gate above).

**Levels** (as `fugaro plan graph` reads them; tasks of a level run at once):
- L0: C1, T1, T2, T5, T6, T7, T15 (CR1 is anticipated and never launched unless raised)
- L1: T3, T4, T8, T11, T13
- L2: T9, T10, T12, T16
- L3: I1
- L4: T14, CK

**Critical path** (the read): C1 → T13 → T16 → I1 → CK, five tasks (C1 → T8 → T9 → I1 → CK is the same length). It is sequential because the adapter option precedes the runner that feeds it, the proxy's selection step and its reaction share one file, the integration waits for every side, and the live checks run the integrated proxy.

**The gain** (the read): 8 edges broken, 11 kept, 3 nodes added (one contract, one integration, one anticipated change request); the weighted path goes from 11 to 10.5, which the CLI reports as an improvement. It is small because the dominant chain is the proxy file's own two tasks, which a contract cannot split.

**Shared-files table** (no level holds two writers of one file; the contract's own `stub.go` is deleted by its integration task):

| File | Tasks (by level) |
| --- | --- |
| `internal/credsvc/bitbucketproxy.go` | T8 (L1), T9 (L2) |
| `internal/credsvc/server.go` | T8 (L1), I1 (L3) |
| `internal/cli/creds.go` | T11 (L1), T10 (L2) |
| `internal/bbtokens/stub.go` | C1 (L0), I1 (L3) |
| `docs/gcp-live-checklist.md` | CK (L4) |
| `website/content/cli-reference.md` | T11 (L1), T10 (L2) |
| `internal/creds/state.go`, `marks.go` | T6 (L0) |
| `internal/runner/project.go`, `projectpolicy.go`, `internal/gitprov/providers/providers.go`, `internal/gitprov/bitbucket/service.go` | T16 (L2) |
| `internal/runner/reviewmode_test.go` | T13 (L1) |
| `internal/cli/init_policy_migrate.go`, `project_cmd.go`, `project_profile.go`, `docs/design/update-in-flight.md` | T12 (L2) |
| `internal/localcfg/localcfg.go` | T2 (L0) |
| `internal/credsvc/bundle.go`, `schemas/creds-bundle.schema.json`, `internal/config/config.go` | T1 (L0) |
| `internal/projectpolicy/bitbucket.go`, `read.go` | T3 (L1) |
| `internal/gitprov/bitbucket/comments.go`, `internal/followup/select.go` | T13 (L1) |
| `internal/cli/doctor_brokering.go` | T10 (L2) |
| `docs/git-providers.md`, `SECURITY.md`, `docs/credentials-service/runbook.md` | T14 (L4) |

CR1 shares files with C1 and is launched only when raised, never at level 0.

**Contract changes anticipated**: CR1 amends C1 if T8's review finds the snapshot or the selector's signature needs another input (for example the request's method). It reaches T9, T13 and T16; a launched run is followed up by a run on its pull request that names `internal/bbtokens/bbtokens.go` by path and never pastes its text.

## Checks the plan takes

**Checks 590 and 591** (block 590–599; 592–599 reserved and unused). Both are owner-run, NOT RUN, and need a Premium sandbox (none known). 590 settles U1, U2 and U5; 591 proves per-request swapping and a refused call live.

## After-deploy steps

None beyond "run `fugaro update`", which asks nothing here: the legacy token keeps working untouched (R3) and `update` publishes the `bitbucket_tokens` key once records exist (T12). The order for a new token is `fugaro update` first (so the deployed service can read the key family, which `creds set` checks, T11), then `fugaro creds set bitbucket-token--<name>`, then `fugaro update` again to publish its coverage. That is use of the feature and not an upgrade step, so the release notes carry it as one sentence under the feature and not under `### For operators`.

## Considered and declined

- **User-scoped Atlassian API tokens** as a fourth type (the spec's "later"): the type set is closed at three, and a fourth is a version bump of the record (R8).
- **A proactive request queue** (the spec's "queues requests as it nears the limit"): waits for the measured limits (U4); the first build reacts to a 429 and counts for display (D2356).
- **One Secret Manager secret per token** (the spec's letter): D2350 follows D506.
- **A write probe in a scratch repository**: D2353.
- **A service route for the add-time check**: D2352.
- **Synchronising or extending the mounted copy**: out of scope by the owner's ruling (F18).
- **A console view of the new `--json` fields**: the console reads the CLI's JSON and ships on its own release, and added fields are safe for it (CLAUDE.md); it is a follow-up there.
