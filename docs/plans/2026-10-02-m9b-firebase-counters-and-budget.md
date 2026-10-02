# M9b — Firebase Counters, Kill Switches and `fugaro budget` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Spend is capped per day as well as per run, across every concurrent run of a Fugaro project, and an admin can stop one repository or the whole project in seconds. Each Fugaro project gets its **own Firebase project** (D3). `fugaro run` mints a per-run Firebase custom token (D1, no budget service); the runner's gateway takes **leases** from shared RTDB counters with one atomic multi-path write per lease, and database **rules** make that write safe against a compromised token. Kill switches reach jobs over an RTDB event stream. A run that cannot reach the backend halts after a 3-minute grace (D14). `fugaro budget show|set|kill|resume|prices` is the admin surface, `fugaro init --firebase` builds the Firebase side, and a small scheduled **sweeper** (the history job's `--sweep` mode, D12) cleans up crashed runs. Caps count model dollars only (D7); the day is UTC (D5).

**Spec:** [docs/design/m9-budget-and-dashboard.md](../design/m9-budget-and-dashboard.md) §4, §5.3, §5.6–§5.10, §6 (all), §8, §12, §14, §15 tasks 9–16, §16 (D1–D18), §17 (A2, A4, A5, A7, A12–A15); [m9-spec-v3-reconciliation.md](../design/m9-spec-v3-reconciliation.md) (rulings 1, 2, 5, 7). Built before this: M9a [2026-09-30-m9a-gateway-and-project-identity.md](2026-09-30-m9a-gateway-and-project-identity.md) (gateway, `halted`, `Spend`, project identity), M9a.1 [2026-10-01-m9a1-policy-in-fugaro-yaml.md](2026-10-01-m9a1-policy-in-fugaro-yaml.md) (`internal/policy`, the default-branch layer).

## What the design's split puts where

| In M9b (this plan) | Not here |
|---|---|
| design tasks 9–16: `internal/rtdb`, minting, rules and emulator suite, leases, registry, kill stream, `fugaro budget`, launch pre-check, the Firebase Terraform root and `init --firebase`, the history job's **image and `--sweep` mode** with its Scheduler job, live checks, docs | **M9c** `fugaro watch` (reads M9b's data; the watch-key writes reuse T8's admin client). **M9d** the rollover mode, Firestore `spendDaily`, day-node pruning, `fugaro report`. **M9e** verify gate, draft PR at first push. **M10** non-Claude models. **Later** external gateway, org roll-up |

The sweeper is in M9b because design §15 task 15 puts it here and the registry is useless without it. `budget history --rollover` exists as a hidden mode that exits 1 "arrives with M9d"; its Scheduler job and Firestore are **not** created now (M9b creates no Firestore database either; M9d adds it). Day nodes older than 8 days stay until M9d (a few KB a day).

## What M9a and M9a.1 left ready, and what is missing

| Area | Ready (real code) | Missing |
|---|---|---|
| Gateway | `internal/gateway`: in-process ledger `Granted/Used/Reserved`, static `Options.Cap`, 403 `run_cap` halt, 429 "held by calls in flight", `Halted()` channel, `StageReport`, observe mode | a `Lease` source that grows `Granted`; `HaltExternal` (kill, grace) that also cancels in-flight streams; a usage reporter |
| Runner | `haltNow`/`cancelHaltedStage`/`markCancelled`, `HaltError`, `r.spend`, `resolvePolicy` (default-branch read, `policy.Merge`), `startGateway`, `watchHalt`, bootstrap halts with outcome `none` and exit 0 | the budget session (token exchange, caps read, registry, heartbeats, kill stream, grace), halts for the schema's already-listed reasons `kill_switch`, `repo_daily_cap`, `global_daily_cap`, `budget_unavailable`, `budget_token_expired`; `oauth`'s notional report |
| Policy | `internal/policy` `Layer`/`Merge` (mode, per-run, tokens, models, output limits); `budget.per_day_usd` decoded and refused "not supported yet (M9b)" (`config.go:54`, `validate.go:392`) | `PerDayUSD` as a merged key: `min(RTDB daily cap, committed)` |
| Local config / env | `localcfg.Budget{Mode, PerRunUSD, MaxRunTokens, AllowedModels}`, `model_prices`; plain job env `FUGARO_BUDGET_MODE`, `FUGARO_MAX_RUN_USD`, `FUGARO_MAX_RUN_TOKENS`, `FUGARO_ALLOWED_MODELS`, `FUGARO_MODEL_PRICES` from `infra/spec.go` | `firebase_project`, `rtdb_url`, `firebase_api_key`, `token_signer`, grace/heartbeat; `FUGARO_RTDB_URL`, `FUGARO_FIREBASE_API_KEY`, `FUGARO_BUDGET_GRACE` |
| Infra | installation + repo Terraform roots, `fugaro init` (`--name`, `--repo`, discovery by marks, guard with `prevent_destroy`), Scheduler (image checks), runs-bucket marker `fugaro/project.json`, `gcpfake` (GCS, Run, Scheduler, IAM policies, Service Usage, CRM) | a third root `roots/firebase` (google-beta), `init --firebase`, the signer and minter role, the history job and its sweep Scheduler job; `gcpfake` has **no** RTDB, Identity Toolkit/Secure Token, or IAM Credentials `signJwt` |
| CI | `go test -race`, vet under 5 tag sets, `terraform` job (fmt, validate/test per root, tflint, Trivy) | a `rules` job running the RTDB emulator (JRE + pinned `firebase-tools`) under a `firebase` build tag |

## Decisions already made (binding, user)

D1 per-run custom tokens + rules, no budget service. D3 one Firebase project (FP) per Fugaro project; the user creates it and links billing, `fugaro init --firebase <fp-id>` adopts it. D4 RTDB `us-central1`. D5 UTC day. D6 admins = GCP project owners and editors + optional `budget_admins`. D7 caps count model dollars; compute reported, never capped. D8 unpinned models rejected (M9a). D9 a halt before the branch exists is `halted`, no PR, exit 0. D12 registry = heartbeats + sweeper. D14 backend unreachable → halt after a 3-minute grace for **every auth mode and every mode except `off`**. A1/ruling 7: `oauth` is uncapped for dollars but subject to kill switches, the token cap and D14. Everything is Go. No backward compatibility (memory: only the user's two installs exist; reshape directly, migrate by re-running init). **The user runs anything that costs money or changes real resources: every such step is ⚠ CONFIRM, each its own question.**

## Global Constraints

- **Money is integer µ$** end to end (`pricing.Micros`); rules compare exact integers. `float64` only at the edges (flags, `result.json`).
- **A refusal is never fail-open.** A missing cap node reads `null`; `N <= null` is false, so rules deny (design §6.4). A repository with no cap and no defaults has no budget and halts (`no_cap`), in `enforce`.
- **Kill switches and D14 apply in `observe`.** Observe relaxes caps only (design §5.7). `off` never touches the backend.
- **The job holds no IAM identity on the FP.** Its only credentials are the run's Firebase ID token and refresh token, in memory, registered with the redactor, never in an env var, log, record or bucket object after bootstrap deletes the token object.
- **Every job-written string is untrusted** (registry `title`, `stage`): clipped to 200 chars by the rules, and sanitised again in `fugaro budget show` (reuse `internal/cli/safetext.go`).
- **The rules file is generated, never hand-edited:** `internal/budget/rules` template + golden. One generator feeds `init`, the emulator tests and the pure `budget.Evaluate` predicate.
- **TDD, hermetic by default.** `go test ./...` needs no network, GCP, Firebase, Java or model. Emulator tests carry `//go:build firebase`.
- Exit codes as M9a: 0 ok (a bootstrap halt exits 0), 1 a user error or refusal, 2 a remote failure (`fugaro run` with an unreadable RTDB exits 2: it fails closed).
- No company- or repo-specific values outside this plan: use `aurora`, `proj-1234`, `aurora-fp`, `example.invalid`.
- CI after every task: gofmt, vet (plain, `docker`, `live`, `live docker`, `terraform`), `go test -race ./...`, and the `terraform` job; from T3 also the `rules` job.

## Rulings

Each says what it costs if wrong.

**R1. Cap layers.** *Per run:* `min(effective M9a.1 per-run, RTDB perRun)`, checked in-process and by rules. *Per day:* `min(RTDB repo daily cap (or `defaults.repoDailyUsd`), committed `budget.per_day_usd`)`, as M9a.1 promised. The RTDB cap is the owner's ceiling, set only with `fugaro budget set` (**the project config gets no `per_day_usd`**: one home for owner-set daily caps). `fugaro.yaml` on the default branch and the run's branch may only tighten (`policy.Merge`, new key `per_day_usd`; the branch layer's looser value goes to `policy.ignored` as before). Rules cannot see git, so the committed day cap is enforced **client-side in the lease top-up** (`budget.Evaluate` takes the effective cap) and is a guardrail like everything under D2; the RTDB cap is rules-enforced. A committed cap applies to the **repository's** day counter only (a repository's file cannot speak for the project). A refusal by the committed cap halts as `repo_daily_cap` with `detail` naming `fugaro.yaml`. *Cost if wrong:* a committed day cap silently loosening. Pinned by `TestPerDayMergeNeverLoosens`, `TestCommittedDayCapRefusesClientSide`.

**R2. Mode lives in the project's RTDB (`config/mode`: `observe|enforce`), plus the job's `FUGARO_BUDGET_MODE` as the on/off gate.** Rules cannot differ per repository without a node, and one FP is one project, so calibrating caps is a project-wide act (design §13: observe a week, then enforce). `init --firebase --budget-mode` seeds it, `budget set --mode` changes it (admin). The rules' cap checks read it (`root.child('config/mode').val() == 'enforce'`; any other value, absent included, is observe-leniency **for cap checks only**, never for kill switches, `maxReserve`, the delta equations or `RUN`; absent `config/mode` makes `fugaro budget show` and the launch pre-check say so). The job's mode (M9a.1 merge, strictest wins) is `off` → no backend at all; `observe` or `enforce` → the backend, with the run following RTDB's mode for shared caps. A repository whose env says `observe` under an `enforce` project is refused by rules like any run: it halts. *Cost if wrong:* a per-repository observe would need a node per slug; the design's text ("the observe rules variant") is read as this node, flagged as open question 1.

**R3. Leases (design §5.3), with the gateway as level 1.** `gateway.Options.Lease` (nil = M9a's static `Cap`, unchanged) supplies `Grant(ctx, need)`, `Report(ctx, usage)` and `Release(ctx, unused)`. `reserve` becomes two-phase: under the mutex it reserves if `free >= w`; else it drops the mutex and calls `Grant` through a single-flight top-up (parallel calls wait on one grant, never one each), then retries. `Granted` rises only by grants, so `used + reserved <= granted` still holds. Lease size is `clamp(5% of the smallest headroom, $0.25, $2.00)` and at least `w`; when `L` fits no cap but `w` does, ask for `w`; before halting on a refusal, retry once after 20 s (releases may land). A real refusal halts with the reason the **local predicate** gives; a stale one retries ≤ 8 times with jitter, then counts as unreachable (grace).

**R4. Rules are the compare-and-set; the client re-evaluates to classify a 401.** `budget.Evaluate(state, L, caps, switches, now)` is a pure Go function that mirrors the rules; the emulator suite proves **parity** (property: random states and writes, rules allow ⇔ `Evaluate` allows). A permission error re-reads five nodes (run, repo day counter, global day counter, caps, kills) and evaluates: pass = stale (retry), fail = refusal with a reason. *Cost if wrong:* drift between the two; pinned by `TestRulesParity` (emulator) and the golden.

**R5. The token (design §6.4).** The launcher mints after the launch claim and before `jobs.run`, for `run` and `run --retry`; claims `{fs, fr, fx, fp, rb}` (`rb` = `requested_by` from the launcher's own credential; the registry and outcome writes must carry it: a rules check). `fx = launch + job timeout + 1h queueing + 5m`. Delivery: create-if-absent object `runs/<slug>/<run>/budget-token` in the runs bucket; the runner reads, **deletes**, then calls `signInWithCustomToken`; redactor registration before the first log line. Older than 1 h at exchange: halt `budget_token_expired`, outcome `none`. Refresh via Secure Token keeps uid and claims. *Verified only live (A12, A15).*

**R6. D14 grace.** One `budget.Grace` (default 3 m, `FUGARO_BUDGET_GRACE` for tests, floor 5 s) starts at the first failure of any backend call (token exchange, lease, heartbeat, kill stream, registry) and resets on any success; expiry halts `budget_unavailable`. The gateway spends the lease it already holds during the grace. Bootstrap unreachable: halted, outcome `none`, exit 0. `oauth` has no lease, but heartbeats and the kill stream are its backend, so it halts too. Image checks, `verify`, finalize and writeback never touch the backend.

**R7. `oauth` (design §5.8).** No gateway. After each stage, `notional += total_cost_usd` on the run, repo and global day nodes (increase-only, no cap check) and `byModel` from `modelUsage`; the registry shows `notional`. Kill switch, token cap, D14 as above.

**R8. Registry and sweeper (D12).** Entry at bootstrap after the exchange; heartbeat every 15 s (stage, round, spend, `updatedAt`, plus the usage report in the same multi-path write); at run end write `outcomes/<day>/<slug>/<run>` once, then delete the entry. The sweeper (`fugaro budget history --sweep`, Cloud Run job `fugaro-history`, Scheduler `*/15 * * * *`) lists the project's Cloud Run executions, removes entries of ended or missing executions, marks their `/runs` ledger `crashed` (outstanding stays counted: errs high), writes an `infra_error` outcome, deletes auth users older than 2 days. It holds `firebasedatabase.admin` on the FP (the most privileged new identity; design §11) and never writes `/config`.

**R9. Admin access.** `internal/rtdb` accepts an `oauth2.TokenSource`. People use ADC with the `firebase.database` and `userinfo.email` scopes (A2); a 403 from a write is reported as "you are not a budget admin for project aurora (GCP owners/editors and `budget_admins`)". Cap and switch writes are single-node ETag `PUT`s showing old and new values; **raising** a cap or `resume` needs a typed confirmation (or `--yes`); `kill --all` types the project name.

**R10. Job environment is plain env from `infra/spec.go`** (M9a R11), not Terraform variables: `FUGARO_RTDB_URL`, `FUGARO_FIREBASE_API_KEY` for every workflow job whose budget is not off. This corrects the design §6.6 "through the tfvars". The history job's env is the one Terraform-defined environment (T9).

**R11. Firebase Terraform is its own root and state** (`roots/firebase`, state `fugaro/firebase`, operator-only), as design §6.6; `init --firebase` runs three confirmed applies (installation → firebase → installation). It never creates the project or links billing. RTDB rules and the `/fugaro/mark` are deployed by `init` over REST (Terraform does not manage them). Firestore is not created in M9b (see the scope table).

## Review Focus

Most likely mistakes first; each is pinned by a named test in the owning task.

1. **A rule that lets a token write another run's or repo's state, or move a counter by an amount that is not its own delta.** T3 `TestDenialTable` (all 12 rows of design §6.4 as named emulator tests), `TestAllowedPaths`, `TestObserveLeniencyOnlyCaps` (observe still denies kills, `maxReserve`, wrong deltas, foreign slugs), `TestDeleteOwnLedgerDenied`, `TestExpiredFxDenied`.
2. **Rules and `Evaluate` disagreeing**, so the client calls a refusal stale and spins, or the reverse. T3 `TestRulesParity`; T1 `TestEvaluateTable`.
3. **`TODAY` and the day boundary** (A14): T3 `TestTodayExpression` (generated expression against 23:59:59.999 and 00:00:00.000 using the emulator's clock if it has one, else the expression evaluated by a Go mirror), `TestOldDayAllowsOnlySpentAndReleased`; T6 `TestLeaseAcrossMidnight` (a lease belongs to the day it was granted on; releases go to that day).
4. **Counted ≠ spent, double counting on release or heartbeat.** T5/T6 `TestReleaseEqualsUnused`, `TestHeartbeatMovesSpentNotCounted`, `TestCrashedRunStaysCounted`; T3 `TestReleaseBoundedByOutstanding`.
5. **A halt that corrupts the run**: a kill during finalize or writeback is ignored (`TestKillDuringFinalizeIgnored`); a kill before the branch exists is `halted`/`none`/exit 0 (`TestKillAtBootstrapNoPR`); a kill after a push opens a draft PR with the halted report; halt vs cancel precedence unchanged (`TestKillLosesToEarlierCancel`); no run is ever ready after a halt.
6. **Failing open on backend loss**, or failing closed too early. T6 `TestGraceHaltsAfterThreeMinutes` for api-key, vertex and `oauth` and for `observe`, `TestGraceResetsOnSuccess`, `TestOffNeverTouchesBackend`, `TestHeldLeaseSpendableDuringGrace`, `TestStaleRetriesThenGrace`, `TestImageCheckUnaffectedByBackendLoss`.
7. **Secrets of the run's Firebase identity leaking** (custom token, ID token, refresh token, API key use): T2 `TestTokenObjectDeletedAfterRead`, `TestFirebaseTokensRedacted`; T6 `TestBudgetSecretScan` (full hermetic run: no token in any bucket object, log, transcript, record, env of the agent); T7 `TestMintedTokenNotInJobsRunRequest`.
8. **Committed policy loosened**, or a branch lifting the day cap. T4 `TestPerDayMergeNeverLoosens`, `TestPerDayKeyNoLongerRefused`; R1's client-side tests.
9. **Wrong project's backend**: `fp` claim and `/fugaro/project` must equal; T2 `TestMintClaims`, T3 `TestForeignProjectClaimDenied`; T10 `TestInitFirebaseRefusesUnmarkedData`, `TestFirebaseNeverCreatesProject`.
10. **Launch pre-check blocking or letting through wrongly**: killed, no cap, headroom < $0.25 → exit 1; RTDB unreadable → exit 2; `--no-budget-check` skips only the client check; a repository whose budget is off is never checked. T7.
11. **Budget admin rights over-granted or missing**: T9 `TestAdminGrantsFromOwnersAndEditors`, `TestLauncherHasViewerAndMinterOnly`, `TestJobAccountsHaveNoFPGrant`, `TestSignerHasNoRoles`; T8 `TestSetWithoutAdminExplains`.
12. **Untrusted text reaching a terminal**: T8 `TestShowSanitizesRegistryText`.
13. **Contention**: T3 `TestConcurrentLeasesNeverExceedCaps` (50 goroutines, 3 caps, emulator), T1 `TestRTDBFakeMultiPathAtomic`.

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/budget/` (new: `model.go`, `evaluate.go`, `lease.go`, `session.go`, `registry.go`, `kill.go`, `grace.go`), `internal/budget/rules/` (new: `rules.go`, `rules.json.tmpl`, `testdata/rules.golden.json`, `emulator_test.go` tagged `firebase`) | paths and types (µ$, day numbers, key escaping), `Evaluate`, the lease client, the run's session, the generated rules | T1, T3, T6 |
| `internal/rtdb/` (new) | REST client: GET (+ETag), PUT `if-match`, multi-path PATCH, SSE stream, `auth=<idtoken>` or OAuth source | T1 |
| `internal/budget/token/` (new) | mint (`signJwt`), the object, exchange, refresh, expiry | T2 |
| `internal/gcpfake/{rtdb,identitytoolkit,iamcredentials}.go` (new) | RTDB fake (no rules), sign-in and refresh, `signJwt` | T1, T2 |
| `internal/policy/policy.go`, `merge.go`, `internal/config/{config,validate,example.yaml}`, `schemas/fugaro.schema.json` | `per_day_usd` | T4 |
| `internal/gateway/{gateway,ledger}.go`, `lease.go` (new) | `Options.Lease`, two-phase reserve, `HaltExternal`, `Release` at Close | T5 |
| `internal/runner/{budget_session,gateway,halt,policy,spend,runner,report}.go`, `internal/runstore/runstore.go`, `schemas/result.schema.json` | the session in `bootstrap`/`stage`/`finalize`, kill and grace halts, `oauth` notional, optional `budget` record | T6 |
| `internal/cli/{run,followup}.go`, `internal/backend/gcp/*` | mint at launch, pre-check, `--no-budget-check` | T7 |
| `internal/cli/budget.go` (new), `internal/cli/cloud.go` | `fugaro budget`, admin client | T8 |
| `deploy/terraform/gcp/roots/firebase/`, `modules/firebase/` (new), `modules/installation/{history,iam}.tf`, tests | FP root, signer, minter role, grants, history job + sweep Scheduler | T9 |
| `internal/infra/{spec,tfvars,discover,installation}.go`, `internal/cli/init.go`, `internal/localcfg/localcfg.go` | `--firebase`, `--budget-mode`, `--budget-admin`, discovery, three applies, rules deploy, mark, config keys, job env | T10 |
| `cmd/` or `internal/cli/history.go`, `images/history/Dockerfile` (new), `.github/workflows/{ci,images}.yml` | history image, `--sweep`, CI `rules` job | T11, T3 |
| `docs/design/{v1,m9-budget-and-dashboard}.md`, `docs/gcp-setup.md`, `docs/gcp-live-checklist.md`, `internal/e2e/live_budget_test.go` (new) | docs, check 21 | T12 |

## Task dependency graph and lanes

| Task | Size | Lane | Depends on |
|---|---|---|---|
| T1 data model, `internal/rtdb`, RTDB fake, `Evaluate` | M | A | — |
| T2 tokens: mint, object, exchange, refresh + fakes | M | A | T1 |
| T3 rules generator, emulator suite, CI `rules` job | L | B | T1 |
| T4 `per_day_usd` in policy and `fugaro.yaml` | S | C | — |
| T5 gateway `Lease`, `HaltExternal`, release | M | C | T1 (types) |
| T6 the runner's budget session | L | A | T1, T2, T4, T5 |
| T7 launcher: mint, token object, pre-check | M | A | T2, T6 (record fields) |
| T8 `fugaro budget` | M | D | T1 |
| T9 Terraform: firebase root, signer, grants, history job | L | E | — |
| T10 `init --firebase` and the config/env | L | E | T3 (rules), T9 |
| T11 history image and `--sweep` | M | D | T1, T9 |
| T12 docs and the live test | M | — | T1–T11 |
| T13 migration and live verification (controller) | S | — | all |

Five lanes may run in parallel worktrees until T6: A (T1, T2), B (T3), C (T4, T5), D (T8), E (T9). They touch disjoint files except the hot files: `internal/runner/runner.go` (T6 only, via `budget_session.go`), `internal/cli/init.go` and `localcfg.go` (T10 only), `internal/policy/*` (T4 only), `internal/gateway/*` (T5 only), `internal/cli/cloud.go` (T8 adds the admin client constructor only), `internal/gcpfake/*` (T1 then T2).

---

### Task 1 (M, lane A): Data model, `internal/rtdb`, the RTDB fake, `Evaluate`

**Files:** `internal/budget/{model,evaluate}.go` and tests; `internal/rtdb/{client,stream}.go` and tests; `internal/gcpfake/rtdb.go` and test.

- `model.go`: node paths for design §6.2 (`/config`, `/runs`, `/spend/<day>`, `/agents`, `/outcomes`, `/fugaro`), key escaping (`.` → `%2E`), `Day(now)` = UTC epoch day, `Micros` JSON, and typed documents (`Caps`, `Kill`, `RunLedger`, `Counters`, `AgentEntry`).
- `rtdb.Client`: `Get`, `GetETag`, `PutIfMatch`, `Patch(root, map[path]any)` (one multi-path update), `Stream(path) <-chan Event` (`put`, `patch`, `keep-alive`, `cancel`, `auth_revoked`; reconnect with backoff), auth by `Auth{IDToken}` or `Auth{Source oauth2.TokenSource}`; `Date` header gives server time. Errors are typed: `ErrPermission` (401/403), `ErrPrecondition` (412), `ErrUnavailable`.
- `gcpfake` RTDB implements the REST subset, ETags, atomic multi-path patch and SSE. **It does not implement rules** (the emulator's job); a test hook (`DenyNext`) injects 401s.
- `Evaluate(State, Write, Caps, Kill, Now) Decision{Allow, Reason, Scope}` is the pure mirror of the rules for a lease and a release (R4): kill (either), `maxReserve`, run lifetime cap, repo daily, global daily, plus the committed day cap argument (R1).
- [ ] **Failing tests first:** `TestKeyEscaping`, `TestDayNumbers` (UTC, boundaries, leap), `TestRTDBGetPutETag`, `TestRTDBPatchMultiPathAtomic` (one failing path rejects all), `TestRTDBStreamEvents` (put, patch, keep-alive, cancel, `auth_revoked`, reconnect), `TestRTDBPermissionIsTyped`, `TestRTDBServerTimeFromDate`, `TestEvaluateTable` (a row per reason, boundary equals-cap allowed, null cap denied), `TestEvaluateCommittedDayCap`, `TestRTDBFakeMultiPathAtomic` under `-race`.
- [ ] `go test -race ./internal/budget/... ./internal/rtdb/... ./internal/gcpfake/...`. Commit: `rtdb: REST client, data model and the lease predicate`

### Task 2 (M, lane A): Tokens

**Files:** `internal/budget/token/{mint,object,exchange}.go` and tests; `internal/gcpfake/{iamcredentials,identitytoolkit}.go` and tests.

- `Mint(ctx, signer, Claims{Slug, Run, FX, FP, RB}) (string, error)` builds the custom-token JWT (R5) and signs it with `iamcredentials.signJwt` **as the signer** (the launcher's ADC needs only the minter role). `PutObject`/`TakeObject` use the runs bucket (create-if-absent; read then delete, an error on delete is fatal for the exchange). `Exchange(ctx, apiKey, token)` → `IDToken, RefreshToken, Expiry`; `Refresh` before expiry; `Session.Token()` hands `rtdb` the live ID token. All values go to the redactor at creation.
- [ ] **Failing tests first:** `TestMintClaims` (uid `r~<slug>~<run>`, `fx` formula, `exp = iat+1h`, `aud`), `TestMintSignsAsSigner` (the fake sees the signer's name and that no access token was requested), `TestRetryMintsFresh`, `TestTokenObjectCreateIfAbsent`, `TestTokenObjectDeletedAfterRead` (a second read finds nothing; a failed delete fails the exchange), `TestExchangeOlderThanOneHourExpired`, `TestRefreshKeepsClaims`, `TestFirebaseTokensRedacted`, `TestExchangeUnreachableIsTyped`.
- [ ] Commit: `budget: mint per-run Firebase custom tokens and exchange them in the runner`

### Task 3 (L, lane B): Rules generator, emulator suite, CI job

**Files:** `internal/budget/rules/*`, `.github/workflows/ci.yml` (new `rules` job).

- Generator expands the macros `O(p)`, `N(p)`, `RUN`, `TODAY` of design §6.4 into the rules JSON for `/config`, `/runs`, `/spend/$day/{global,repos,runs}`, `/agents`, `/outcomes`, `/fugaro/project`; adds the `config/mode` leniency of R2 (only on cap comparisons) and the `fp`/`rb` checks. Output is deterministic; golden file `testdata/rules.golden.json`.
- Emulator harness (`//go:build firebase`): starts `firebase emulators:start --only database` from a pinned `firebase-tools` (npm lockfile in `internal/budget/rules/emulator/`), mints unsigned emulator tokens with `fs`, `fr`, `fx`, `fp`, `rb` over REST (A13), loads the generated rules, drives `internal/rtdb`. The CI `rules` job installs Java 21 and Node, caches `~/.cache/firebase/emulators`, and runs `go test -tags firebase ./internal/budget/rules/...`; it is a required check.
- [ ] **Failing tests first (emulator):** `TestDenialTable` (rows 1–12 by name), `TestAllowedPaths` (reserve, report, release, notional, heartbeat, registry, outcome-once), `TestObserveLeniencyOnlyCaps`, `TestForeignProjectClaimDenied`, `TestRbMismatchDenied`, `TestTodayExpression`, `TestOldDayAllowsOnlySpentAndReleased`, `TestReleaseBoundedByOutstanding`, `TestConcurrentLeasesNeverExceedCaps`, `TestRulesParity` (property vs `Evaluate`, 500 random cases), `TestNullCapDenies`. Plain unit: `TestRulesGolden`, `TestGeneratedRulesParse`, `TestMacrosExpandOnce`.
- [ ] If the emulator does not enforce rules as production (A13) or lacks something, the test names it as a `FACT` and the live smoke (check 21) takes over **for that row only**, recorded in the plan's results.
- [ ] Commit: `budget: generated RTDB rules, emulator suite and CI job`

### Task 4 (S, lane C): `per_day_usd`

**Files:** `internal/policy/{policy,merge}.go`, `internal/config/{config,validate,example.yaml}`, `schemas/fugaro.schema.json`, `testdata/config/**`, `internal/runner/policy.go` (key plumbing only).

- `Layer.PerDayUSD` (0 = unset), key `per_day_usd`, `min` of positive values; `PolicyOf` returns it; the "not supported yet (M9b)" problem and `invalid/budget-per-day.yaml` go (the corpus gets a valid `per_day` and an invalid negative); `Effective` exposes it for T6. `validate` warns: "per_day_usd is enforced by the runner, not the database; an RTDB cap lower than this wins".
- [ ] **Failing tests first:** `TestPerDayKeyNoLongerRefused`, `TestPerDayMergeNeverLoosens` (property, as M9a.1's), `TestPerDayBranchLooserIgnoredRecorded`, `TestSchemaPerDay`, `TestPolicyRecordCarriesPerDay`.
- [ ] Commit: `policy: per-day cap as a tightening key`

### Task 5 (M, lane C): Gateway leases

**Files:** `internal/gateway/{gateway,ledger,lease}.go` and tests.

- `type Lease interface { Grant(ctx, need pricing.Micros) (pricing.Micros, error); Report(ctx, StageReport/ModelUsage) error; Release(ctx, unused pricing.Micros) error }`; `Options.Lease`; a `Refusal{Reason, Scope, Detail}` error type from `Grant` halts with that reason; any other error is `Unavailable` (the session owns the grace, R6). Single-flight top-up, ordering, no mutex held across `Grant`. `Server.HaltExternal(Halt)` makes later calls 403 with `x-should-retry: false` and cancels in-flight upstream requests (charging as the existing client-cancel rule). `Close` calls `Release(unused)` once.
- [ ] **Failing tests first:** `TestLeaseGrowsGrantedOnly`, `TestLedgerNeverExceedsGrantedWithLease` (property, `-race`, 50 goroutines, grants of random size), `TestTopUpSingleFlight` (10 parallel calls, one `Grant`), `TestRefusalHalts` (reason and scope carried), `TestUnavailableIsNotAHalt` (the call gets a retryable 503, no halt), `TestHaltExternalCancelsInFlight`, `TestReleaseOnCloseOnce`, `TestNilLeaseIsM9a` (M9a's tests unchanged), `TestObserveLeaseNeverRefusesForCaps`.
- [ ] Commit: `gateway: take leases from a source and halt on an external cause`

### Task 6 (L, lane A): The runner's budget session

**Files:** `internal/budget/{lease,session,registry,kill,grace}.go`, `internal/runner/{budget_session,gateway,halt,policy,spend,runner,report}.go`, `internal/runstore/runstore.go`, `schemas/result.schema.json` (+ tests), `internal/agent/fakeclaude` (kill/notional cases).

- Bootstrap (after the M9a.1 policy and before the lock): read and delete the token object, exchange, read caps and `config/mode`, check the kill switches and the cap (`no_cap` halt, `kill_switch` halt: both outcome `none`, exit 0), create the registry entry, start the heartbeat (15 s: registry + usage report in one multi-path patch) and the kill stream on `config/kill` (re-read each heartbeat as a backstop). `budget.Lease` implements T5's interface with `Evaluate` classification, 8 stale retries, committed day cap and effective per-run from `policy.Effective`. `Grace` implements R6. Mid-run halts call `haltNow` then `cancelHaltedStage` and `gateway.HaltExternal`. `oauth` skips the gateway and reports `notional` per stage (R7). At run end: `Release`, write the outcome once, delete the entry. A kill or grace halt after finalize started is ignored.
- `result.json` (additive): `halt.scope` now `repo|global` for kills and daily caps; optional `budget: {day, granted_micros, released_micros, mode, backend}`; the report's halted text names the switch, who and why, or the cap and source, and how to continue.
- [ ] **Failing tests first (hermetic: `gcpfake` RTDB and token fakes, fake upstream):** `TestBootstrapExchangesAndDeletesToken`, `TestKillAtBootstrapNoPR`, `TestNoCapHaltsEnforce`, `TestKillMidImplementOpensDraft`, `TestKillDuringFinalizeIgnored`, `TestKillLosesToEarlierCancel`, `TestLeaseTopUpAndRelease`, `TestReleaseEqualsUnused`, `TestHeartbeatMovesSpentNotCounted`, `TestRepoDailyCapHalts`, `TestGlobalDailyCapHalts`, `TestRunCapFromRTDBMin`, `TestCommittedDayCapHalts`, `TestLeaseAcrossMidnight`, `TestObserveNeverHaltsForCaps`, `TestObserveStillHonoursKill`, `TestGraceHaltsAfterThreeMinutes` (table: api-key, vertex observe, oauth), `TestGraceResetsOnSuccess`, `TestHeldLeaseSpendableDuringGrace`, `TestStaleRetriesThenGrace`, `TestOffNeverTouchesBackend`, `TestOAuthReportsNotional`, `TestOAuthKillHalts`, `TestTokenExpiredHalts`, `TestCrashedRunStaysCounted`, `TestBudgetSecretScan`, `TestRegistryEntryWrittenAndDeleted`, `TestOutcomeWrittenOnce`, `TestHaltedRecordSchema`, `TestImageCheckUnaffectedByBackendLoss`. e2e: `TestCloudBudgetRunWithFakes`.
- [ ] Commit: `runner: leases, kill switches, registry and the 3-minute grace`

### Task 7 (M, lane A): Launcher

**Files:** `internal/cli/{run,followup}.go` and tests, `internal/budget/precheck.go`.

- After the claim and before `jobs.run`: mint (T2), write the token object, then launch. Pre-check (budget not off for the workflow): killed, no cap or daily headroom < $0.25 → exit 1 with the reason; RTDB unreadable → exit 2; `--no-budget-check` skips only this. `--retry` mints fresh. The token never goes in the `jobs.run` request (the request body reaches audit logs).
- [ ] **Failing tests first:** `TestRunMintsAndWritesObjectBeforeLaunch`, `TestRetryMintsFresh`, `TestMintedTokenNotInJobsRunRequest`, `TestPrecheckKilledRefuses`, `TestPrecheckNoCapRefuses`, `TestPrecheckLowHeadroomRefuses`, `TestPrecheckUnreadableExits2`, `TestNoBudgetCheckSkipsOnlyPrecheck` (the mint still happens), `TestBudgetOffSkipsEverything`, `TestRunRequestedByInClaim`.
- [ ] Commit: `run: mint the run's budget token and pre-check the budget`

### Task 8 (M, lane D): `fugaro budget`

**Files:** `internal/cli/budget.go` and tests, `internal/cli/cloud.go` (admin client constructor).

- `show [--repo R|--all] [--json]` (caps, today counted/spent/notional, headroom, switches, mode, running entries; project header; `notional` labelled), `set (--global|--defaults|--repo R) [--daily] [--per-run] [--max-reserve] [--mode observe|enforce] [--clear]` (finite, ≥ 0, ≤ $100,000, per-run ≤ daily), `kill`/`resume (--all|--repo R) [--reason]`, `prices` (reads `internal/pricing`, warns on tables older than 90 days; no cloud), hidden `history --sweep|--rollover`. Output goes through `safetext`.
- [ ] **Failing tests first:** `TestShowSanitizesRegistryText`, `TestShowJSONProject`, `TestSetShowsOldAndNew`, `TestSetRaiseNeedsConfirmation`, `TestSetLowerNoConfirmation`, `TestSetValidatesRanges`, `TestSetETagConflictRetries`, `TestSetWithoutAdminExplains` (403 → the D6 message), `TestKillAllTypesProjectName`, `TestKillRecordsBy`, `TestResumeConfirms`, `TestPricesOfflineAndStaleWarning`, `TestBudgetRefusesWithoutFirebaseConfig`.
- [ ] Commit: `budget: show, set, kill, resume and prices`

### Task 9 (L, lane E): Terraform

**Files:** `deploy/terraform/gcp/roots/firebase/*`, `modules/firebase/*`, `modules/installation/{history,iam,apis}.tf`, `outputs.tf`, tests under each root, `deploy/terraform/scan.sh` roots list, `.tflint.hcl`.

- `roots/firebase`: google and google-beta (pinned), APIs (`firebase`, `firebasedatabase`, `identitytoolkit`, `securetoken`, `iamcredentials`, `apikeys`), `google_firebase_project`, `google_firebase_database_instance` (`us-central1`, A5), a restricted web API key (Identity Toolkit and Secure Token only), the signer `fugaro-token-signer` with **no roles**, custom role `fugaroTokenMinter` (`iam.serviceAccounts.signJwt`) bound **on the signer** for launchers and operators, `firebasedatabase.viewer` for launchers/operators, `firebasedatabase.admin` for the discovered owners/editors and `budget_admins`, `firebasedatabase.admin` + `firebaseauth.admin` for `fugaro-history`. Outputs: `rtdb_url`, `firebase_api_key`, `token_signer`. No job-account grant on the FP.
- Installation root, gated by `enable_budget`: `fugaro-history` account (`fugaroLauncher`'s `run.executions.list`), the history Cloud Run job, `fugaro-scheduler`, one Scheduler job `*/15 * * * *` for `--sweep`.
- [ ] **Failing tests first** (`terraform test` with mock providers + Go `-tags terraform` golden tfvars): `TestSignerHasNoRoles`, `TestLauncherHasViewerAndMinterOnly`, `TestJobAccountsHaveNoFPGrant`, `TestAdminGrantsFromOwnersAndEditors`, `TestHistoryAccountGrants`, `TestOnlySweepSchedulerJob`, `TestApiKeyRestricted`, `TestRtdbRegionIsUsCentral1`, `tflint`, Trivy scan clean.
- [ ] Commit: `terraform: Firebase root, token signer, grants and the sweep job`

### Task 10 (L, lane E): `init --firebase` and the config

**Files:** `internal/cli/init.go`, `internal/infra/{discover,installation,tfvars,spec}.go`, `internal/localcfg/localcfg.go`, `internal/gcpfake/*` (FP project, billing, RTDB mark), tests.

- Flags `--firebase <fp-id>`, `--budget-mode observe|enforce`, `--budget-admin` (repeatable). Discovery refuses: FP missing, no billing, unmarked RTDB data. Reads the GCP project's owners and editors from its IAM policy. Three applies, each with its own plan and confirmation (installation → firebase → installation); after the second, `PUT /.settings/rules.json` (T3's output) and the `/fugaro/mark`, `/fugaro/project`, `config/mode`. `localcfg.Budget` gains `FirebaseProject`, `RTDBURL`, `FirebaseAPIKey`, `TokenSigner`, `Grace`, `Heartbeat` (strict decoding); `init --repo` adds `FUGARO_RTDB_URL`, `FUGARO_FIREBASE_API_KEY` and, when set, `FUGARO_BUDGET_GRACE` to workflow jobs whose budget is not off (R10). `guard` adds the RTDB instance to `prevent_destroy`. `--print-vars` ungated notes as before.
- [ ] **Failing tests first:** `TestInitFirebaseThreeAppliesConfirmedSeparately`, `TestInitFirebaseNeverCreatesProject`, `TestInitFirebaseRefusesUnmarkedData`, `TestInitFirebaseNoBilling`, `TestInitFirebaseOwnersToTfvars`, `TestRulesDeployedAfterSecondApply`, `TestMarkAndProjectWritten`, `TestLocalConfigBudgetFirebaseKeys`, `TestInitRepoPassesRTDBEnv`, `TestJobEnvOmitsBackendWhenBudgetOff`, `TestBudgetModeSeedsRTDB`, `TestInitFirebaseIdempotent` (re-run changes nothing), `TestGuardListsRemovedAdminGrant`.
- [ ] Commit: `init: --firebase adopts the project's Firebase project and wires the jobs`

### Task 11 (M, lane D): The history job's image and `--sweep`

**Files:** `images/history/Dockerfile`, `internal/budget/sweep.go`, `internal/cli/history.go`, `.github/workflows/images.yml`, tests.

- Distroless image with the `fugaro` binary, built by `init` until M7 publishes images (D11). `--sweep`: list Cloud Run executions (as `ls`), delete registry entries of ended or missing executions, mark `/runs` `crashed`, write the `infra_error` outcome once, delete auth users older than 2 days. Never writes `/config` (a test asserts the set of paths written).
- [ ] **Failing tests first:** `TestSweepRemovesEndedExecutions`, `TestSweepKeepsLiveRuns`, `TestSweepMarksCrashedKeepsCounted`, `TestSweepWritesOutcomeOnce`, `TestSweepNeverWritesConfig`, `TestSweepDeletesOldAuthUsers`, `TestSweepIdempotent`, `TestRolloverIsNotYetImplemented` (exit 1, names M9d).
- [ ] Commit: `history: the sweeper job`

### Task 12 (M): Docs and the live budget test

**Files:** `docs/design/v1.md` (§6.1 trust, §4.6 record), `docs/design/m9-budget-and-dashboard.md` (R2, R10 corrections to §5.7 and §6.6), `docs/gcp-setup.md` (the FP step), `docs/gcp-live-checklist.md` (check 21), `internal/e2e/live_budget_test.go` (`//go:build live && docker`), `README.md`.

- `TestLiveBudget` (check 21, modelled on check 20): local `docker run` of the branch's base image, real FP, tokens minted by the harness with the user's ADC, `auth: api-key`, a different pinned model per role, tiny caps; asserts lease/release counters, a repo daily cap halt (`$0.25`), a kill mid-run, token expiry, `FACT:` lines for A2, A4, A12, A14, A15. Gated by `FUGARO_LIVE_SPEND_OK=1`, `FUGARO_LIVE_FIREBASE_PROJECT`, `FUGARO_LIVE_ANTHROPIC_API_KEY`; skips when any is missing.
- [ ] Commit: `docs: the budget backend, who can change caps and check 21`

### Task 13 (S, controller): Migration and live verification

Controller-run, one **⚠ CONFIRM** per step, each its own question; record every `FACT` in the PR. Preconditions: M9a and M9a.1 merged; the user's two installs on the M9a shapes; no active run (`fugaro ls --since 1d`); every `gcloud` command passes `--project`. **Freeze launches** between the base image rebuild and each repository's `init --repo`.

0. Snapshot (read-only): the project config, `base_image`, each job's `gcloud run jobs describe --format json`, the Scheduler list.
1. **⚠ CONFIRM — the user creates the Firebase project and links billing** (Blaze; needs `resourcemanager.projects.create` and Billing Account User). Suggested id `<project>-fp`, same org or folder as the GCP project. We never create it or enable billing. Cost: under ~$10/month at the design's scale (RTDB + history job + Scheduler); the user adds a billing budget alert.
2. **⚠ CONFIRM — `gcloud auth application-default login --scopes=…,https://www.googleapis.com/auth/firebase.database,https://www.googleapis.com/auth/userinfo.email`** at the user's terminal (A2).
3. **⚠ CONFIRM — `fugaro init --firebase <fp-id> --budget-mode observe --print-vars`** (read-only), then the real run: three applies, each plan shown and confirmed separately; rules and the mark deployed. Read-only checks: `fugaro budget show` prints the project header, caps unset, mode observe.
4. **⚠ CONFIRM — set generous caps** (`fugaro budget set --global --daily 150 --per-run 20`, `--defaults --repo-daily 60`) after the plan shows old and new.
5. **⚠ CONFIRM — build the new base image and each repository's derived image, then `fugaro init --repo` for each** (billable builds, confirmed separately); resume the Scheduler check jobs after each.
6. **Free rules smoke (read-only to the cloud):** with a minted real token, a write to another run's ledger is denied, a legitimate lease is allowed and released (A4, A13).
7. **⚠ CONFIRM — one sandbox `oauth` run in `observe`** (task text from the user): registry entry appears and disappears, `notional` is recorded, the outcome node is written, the sweeper finds nothing. Then **⚠ CONFIRM — a second sandbox run killed mid-run** with `fugaro budget kill --repo <sandbox>`: `halted`, draft PR with the halted report, `resume` afterwards.
8. **⚠ CONFIRM — check 21 on the user's own Anthropic API key, at the user's terminal** (`FUGARO_LIVE_ANTHROPIC_API_KEY`, never given to the controller): about $0.50 in total across a lease/release run, a `$0.25` repo-daily-cap halt and a `$0.002` per-run halt; plus the free token-expiry and the D14 grace halt (point `FUGARO_RTDB_URL` at a dead port for a local run).
9. **⚠ CONFIRM — the user switches to `enforce`** after their own week of observe (`fugaro budget set --mode enforce`, real caps). The web repository only ever runs with the user's go-ahead and task text.

*Rollback:* `fugaro init --firebase … --budget-mode off` and `init --repo` (jobs lose the backend env); data is kept (`prevent_destroy`); `budget kill --all` is the emergency stop. Restore jobs from step 0's snapshot if needed.

## Unverified assumptions (Firebase and RTDB behaviour can't be checked offline)

Design §17 numbering; each says where it is checked and what changes if false. **Blocks** = false changes the design.

| # | Assumption | Blocks | Checked | If false |
|---|---|---|---|---|
| A2 | RTDB REST accepts the user's ADC token with the Firebase scopes | `budget`, M9c | step 2/3, check 21 | document `gcloud auth print-access-token --scopes` |
| A4 | A root multi-path `PATCH` is atomic and rules see the merged `newData` through `parent()` | **the whole lease design** | emulator (T3), then step 6 live | rolling day nodes with single-location ETag `PUT` and per-repo partitions; the global cap then needs a service |
| A5 | `google_firebase_project` and `google_firebase_database_instance` are google-beta only; the instance can be created in `us-central1` | T9 | T9 mock, step 3 | create the instance by REST in `init` |
| A7 | Anthropic Console offers a spend limit per workspace | docs backstop | docs check | alerts only |
| A12 | Custom tokens last ≤ 1 h; `fs/fr/fx/fp/rb` appear in `auth.token`; an account with no roles can sign; ID tokens refresh until the user is deleted | R5 | step 6, 8 | only `fx` and delivery change |
| A13 | The emulator enforces rules like production and takes unsigned tokens with custom claims | rules tests | T3 | run the rules tests against a test FP; row-by-row `FACT`s |
| A14 | `'' + (integer arithmetic)` yields the plain decimal day in rules | **TODAY** | T3, step 6 | rolling current-day node whose rollover rules enforce archiving |
| A15 | Custom-token sign-in needs no provider enabled and is free at this volume | R5 | step 3 | enable the provider; check Identity Platform pricing |
| A-F1 | A denied multi-path write returns one 401 for the whole update, and a concurrent winner makes the loser's delta check fail | R3/R4 | emulator, check 21 | classification by re-read stays; adjust retry count |
| A-F2 | The runner (Cloud Run job, default egress) reaches `*.firebaseio.com`, Identity Toolkit and Secure Token; SSE survives ~1 h and `auth_revoked` arrives on token refresh | R6 | step 7 | polling-only kill (15 s) until fixed |
| A-F3 | `roles/storage.objectUser` on the job account allows deleting the token object | R5 | T9 test, step 7 | delete via a launcher-written short TTL or a lifecycle rule |
| A-F4 | A restricted web API key (Identity Toolkit and Secure Token only) works for `signInWithCustomToken` and refresh | R5 | step 3 | unrestricted key; note it is not secret |
| A-F5 | `signJwt` on a signer in another project works with `iamcredentials` enabled in the FP and only the minter role | R5 | step 3 | grant `serviceAccountTokenCreator` on the signer and say so |
| A-F6 | The RTDB `Date` header is a usable server clock for the client's day | R3 | T1, step 6 | read `/.info/serverTimeOffset` |

## Risks

- **Rules are security code verified only by the emulator and one live smoke.** Mitigation: the generator, golden, parity property, the denial table as named tests, and the `rules` job as a required check.
- **Contention on the global counter.** Every lease writes it; design estimates 0.1–1 writes/s. Eight stale retries then grace; check 21 records the observed retry rate.
- **The history identity holds `firebasedatabase.admin`** (it could lift caps). Mitigated by its own image, no model credential, `TestSweepNeverWritesConfig`; accepted by the design (§11).
- **Billing coupling:** a Blaze FP is a second billing surface; a missing link makes `init --firebase` refuse, never fall back.
- **Mode skew** (R2): env `observe` under an `enforce` project halts on cap refusals. Documented; `fugaro budget show` prints both.
- **D14 stops everything, `oauth` included, when RTDB or Identity Toolkit is down.** Accepted by the user; the grace is the only softening.
- **Queued runs older than 1 h halt** (`budget_token_expired`) and can't be retried: launch again.

## Open questions for the user

1. **Mode scope (R2).** Plan: observe/enforce is one project-wide node in RTDB (`config/mode`), set by `init --budget-mode` or `budget set --mode`. Alternative: per repository (a node per slug in the rules). *Default:* project-wide.
2. **Committed `per_day_usd` is client-enforced and repository-scoped (R1).** A compromised agent can ignore it (D2); the RTDB cap is rules-enforced. *Default:* as planned.
3. **Rollover and Firestore are M9d, so M9b creates no Firestore database and no rollover Scheduler job.** The sweeper alone ships now. *Default:* as planned (design §15 puts the sweep here).
4. **Firebase project id and parent** (org or folder) for step 1; and whether to skip the Console billing-budget alert (we only recommend it).
5. **Anthropic API key for check 21** at your own terminal, about $0.50. Without it, daily-cap and lease behaviour is verified for `oauth` (notional) only, and `api-key` budgets stay documented as unverified.
6. **Observe for a week before `enforce`** is your call after step 9; the plan only ships the switch.

## Execution

Subagent-driven is recommended, a fresh reviewer per task. T3 (the rules), T6 (halts, the grace, secrets) and T2 (tokens) are where a shipped mistake lets a run spend past a shared cap, move another run's counters, or leak a Firebase credential; give them the closest review, with the full-review round-2 the batch workflow calls for. T13 is controller-only: it changes real resources and may spend money, the user confirms every ⚠ step, the user alone creates the Firebase project and the billing link, and the web repository gets a run only on the user's explicit go-ahead.
