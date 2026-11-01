# Per-launch limits and a chain budget: implementation plan

**The owner has answered every decision; nothing here is approved to build.** This plan is written to the
owner's answers (D1041–D1061, [reconciliation.md](reconciliation.md) §3), all of them as recommended. A
decision answered the other way changes the tasks that cite it. Each task touches at most 12 files, names
its tests, starts with a red step and ends at the gates below. Nothing blocks the first step: the one question
that did (U4, who reads the chain) is settled in L1.

## Order, and what this plan does not own

- **External dependency E1 (image skew):** the launcher gates a launch that writes any of the four
  `overrides` members behind `checkImageSince` with this work's own `*Since` constant
  (`internal/cli/recipes_skew.go:700`). The release that carries L3 **must** also ship the new constant and
  the gate, or a launch against an older job image fails in the container (`internal/task/task.go:863`,
  `DisallowUnknownFields`). There is nothing for `fugaro update` to converge here: no job environment
  changes (F3), so the gate is the whole story.
- **External dependency E2 (the cloud senior reviewer):** `docs/cloud-senior-review/` is open PR #1472 and
  not on `main`. L0–L9 do not depend on it. What this work gives it is the **link** (D1058): a review job on
  PR N joins the PR's chain by its `--pr`, and the chain's `--max-review-rounds` counts senior-review
  iterations, which is what bounds that design's review-fix loop (L4; the flag's meaning is L3's) — **#1472
  lists that cap among the things it does not do (its finding F27)**, so this is where it lands, and the one
  thing this work asks of it is that the review job define the record its verdict is written to, because a
  chain counts the `reviews[]` entries the runner writes (spec §8).
- **Files other work owns:** registering the `runs show` subcommand is inside `internal/cli/runs.go`, which
  this work owns for that purpose; the generated `website/content/cli-reference.md`, the plugin skills and
  every `console/**` file are owned elsewhere. L8 names them and the task that touches them waits for the
  owner's go.

| Level | Tasks | Needs |
| --- | --- | --- |
| 0 | L0, L1, L2 | nothing (L0's tests use L1's reader interface, with a fake) |
| 1 | L3 | L1, L2 |
| 2 | L4, L5 | L3 (L5 also L0, L1) |
| 3 | L6, L7 | L0, L5 |
| 4 | L8, L9 | L1–L7; E1 merged in the same release |

## Where each decision lands

Every decision D1041–D1061 has one owning task, so a reviewer can follow a ruling to the work that carries
it. A decision answered the other way changes that task and the spec paragraph that names it.

| Decision | Task | Decision | Task |
| --- | --- | --- | --- |
| D1041 carrier in `task.json` | L2, and the gate in L3 | D1052 `--pr` inheritance | L4 |
| D1042 one flag per dimension | L3 | D1053 `--retry` / `--attempts` | L4 (retry), L3 (batch) |
| D1043 loosening is refused | L3 | D1054 the orchestrator and the coordinator | none — nothing in `ee/` changes |
| D1044 merged twice | L3, L5 | D1055 `runs show` and the display | L6 |
| D1045 what a chain is | L0, L4 | D1056 the JSON additions | L2, L6, L7 |
| D1046 the ledger's figures, the records' chain | L0, L1, L5 | D1057 under the rigor ceiling | L3 |
| D1047 `remaining` from used | L0, L5 | D1058 review jobs join the PR's chain | L4 (the link), L3 (the flag's meaning) |
| D1048 `--used-*` may only raise | L4 | D1059 the gateway and the day caps | none — nothing in the gateway changes |
| D1049 unreadable is charged the cap | L0, L1, L4 | D1060 clock skew | L0 |
| D1050 the halted-run writeback | L5 | D1061 raising an exhausted chain | L9 |
| D1051 the token dimension | L0, L1 | | |

## Gates (every task)

`go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); the task's named tests with
`GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1`; `GOFLAGS=-p=2 go test ./internal/cli/ -run
Docs -count=1 -timeout 40m` when a doc changed; `go test ./internal/eeboundary/... -count=1`. Never the whole
`internal/cli` or `internal/runner` package.

## L0. The chain read: resolve a link and sum what the chain used (D1045, D1046, D1047, D1049, D1051, D1060)

The engine's one read of "what the chain already spent". No new store and no new node: the budget database's
per-run ledger is the source of the money and token figures (F20, spec §4), the **run records** carry the
chain's membership, its limit and the two record-backed dimensions, and the records are the only source when
the budget is off, where the chain is **UNENFORCED**.

- **Files (4):** `internal/runstore/chain.go` (new: `Chain` with `Root` and `Runs []string`; `ResolveChain`,
  `Used(dimension)`, `Remaining(limit, dimension)`; the ledger reads arrive through the one-method
  `LedgerReader` interface L1 fills), `internal/runstore/chain_test.go` (new, with a fake reader),
  `internal/runview/runview.go` (the still-running time read, `now − started_at`, off `Row`, `:74`),
  `internal/runstore/testdata/chain/` (new: placeholder run ids only; no secret-shaped literal).
  **Read, not changed:** `internal/budget/model.go` — the dollars figure is `Outstanding()` (`:351`), which
  already exists, and the path builder is `budget.PathRun` (`:55`).
- **The ids come from the link, not from a listing.** The job's account is bound to `runs/<slug>/` and "the
  runner never lists" (`docs/design/v1.md` §6.1, F21), so **the launcher resolves the chain's run ids** —
  from the PR link or from the root run it was given — and the **runner reads each record by name** (L5).
- **Interface:** `Used` takes dollars and tokens from the ledger (`Outstanding()` = `Reserved − Released` for
  dollars, the `tokens` leaf for tokens), time as `max(finished−started, Σ stages[].duration_s)`, and rounds
  as `len(reviews)` — the last two from the records, because a clock and a round count are not money and
  because they are what a runner can read about a run that is not itself (spec §4).
- **A ledger that cannot be read charges its full per-run cap** (D1049) — never zero — because the cap is the
  largest amount that run could have been holding; a caller's `--used-*` may only raise that figure (L4).
- **Red step:** `TestChainUsedSumsTheLedgers` fails while `ResolveChain` does not exist;
  `TestChainUnreadableLedgerChargesTheFullPerRunCap` fails with a zero total (D1049).
- **Tests:** `TestChainUsedSumsTheLedgers`, `TestChainUsedIsTheHeldAmount` (D1046),
  `TestChainUsedCannotBeLoweredByARelease` (T3), `TestChainUsedIgnoresAForgeableRecord` (T2),
  `TestChainTokenDimensionIsTheLedgersTokens` (D1051), `TestChainTimeTakesTheLargerOfRecordAndStages`
  (D1060), `TestChainUnreadableLedgerChargesTheFullPerRunCap`, `TestChainOfAPRFollowsPreviousRun` (F10),
  `TestChainOfARetryIsTheStoredRunID`, `TestChainIdsComeFromTheLinkNotAListing` (F21).

## L1. Reading a chain's ledger nodes, and with whose credential (D1046, D1049, D1051). **SEC**

**Who performs the read is settled, and it is the launcher** (U4, [reconciliation.md](reconciliation.md) §5):
`runs/$slug/$run` is readable on `RUN` alone (`internal/budget/rules/rules.json.tmpl:56-57`) and `RUN`
requires `auth.token.fs == $slug && auth.token.fr == $run`
(`internal/budget/rules/rules.go:580`), so a **run's token reads its own ledger and no sibling's** — `USER`
reaches only `spend/<day>/runs/$slug/$run`. The **launcher** is the identity that reads a chain, because it
already holds `roles/firebasedatabase.viewer`
(`deploy/terraform/gcp/modules/firebase/iam.tf:44-50`), which reads every path; the earlier draft called this
an admin read, and the launcher holds the **viewer** role, not the admin one. The runner holds its **own**
run's token, so it re-reads its own ledger and the chain's records, and merges only **downward**: a figure it
cannot read is the launch layer's, never a larger one.

- **Files (2):** `internal/budget/ledger.go` (new: `ReadLedger(ctx, slug, run) (RunLedger, bool, error)` —
  one named get, `found` and `unreadable` kept apart, because a chain prints which of the two it hit even
  though D1049 charges both the same) and `internal/budget/ledger_test.go` (new). `internal/rtdb/` is **read,
  not changed**: `Client.Get` already reads a named path (`internal/rtdb/client.go:171`) and `budget.PathRun`
  already builds it (`internal/budget/model.go:55`).
- **Red step:** `TestReadLedgerReadsANamedRun` fails while the function does not exist;
  `TestReadLedgerKeepsUnreadableApartFromAbsent` fails while both collapse into one error.
- **Tests:** `TestReadLedgerReadsANamedRun`, `TestReadLedgerKeepsUnreadableApartFromAbsent`,
  `TestReadLedgerRefusesAnEmptyRunID` (the path builder's own guard),
  `TestOutstandingIsTheHeldAmountWhetherOrNotTheRunSettled` (D1046),
  `TestUnreadableLedgerChargesTheFullPerRunCap` (D1049).

## L2. The four `overrides` members (D1041, D1042, D1056)

`task.json`'s `overrides` gains `max_usd`, `max_tokens`, `max_time`, `max_review_rounds`
(`internal/task/task.go:753`), additive within version 1. A task that carries none carries no chain limit at
all (D1041).

- **Files (4):** `internal/task/task.go` (the four members on `Overrides`), `internal/task/task_test.go`
  (decode, and a task with them round-trips), `schemas/task.schema.json` (the four members, additive),
  `schemas/schemas_test.go`.
- **Red step:** `TestTaskDecodesTheLaunchLimits` fails while the members do not exist.
- **Tests:** `TestTaskDecodesTheLaunchLimits`, `TestTaskRejectsANegativeLimit`,
  `TestTaskSchemaAcceptsTheLaunchLimits`, `TestTaskSchemaStillRefusesAnUnknownMember`.

## L3. The four flags, the tighten-only refusal, and the layer they merge as (D1041, D1042, D1043, D1044, D1057, D1058). **SEC**

The launcher resolves the effective policy before the launch and refuses a value that would loosen, naming
both numbers and the layer that set the lower one (F4). It gates the launch behind a new `*Since` constant
(E1), and it writes the batch half of D1053: a `--max-*` on a batch applies to **every** attempt, and the
batch is N chains, not one.

- **Files (9):** `internal/cli/run.go` (the four flags on `newRunCmd`; the resolve-then-merge before the
  write, beside the gates at `:727-801`), `internal/cli/run_limits_test.go` (new),
  `internal/cli/recipes_skew.go` (the new `*Since` constant, beside `governanceSince` `:531`),
  `internal/cli/recipes_skew_test.go` (the gate refuses an older image and names `fugaro image refresh`),
  `internal/cli/launch_budget.go` (the resolve helper the pre-check and the merge share), `internal/cli/
  launch_budget_test.go`, `internal/policy/policy.go` (the two members `Layer` lacks — a wall-time one and a
  review-rounds one; the dollars and tokens reuse `PerRunUSD` and `MaxRunTokens`), `internal/policy/merge.go`
  (**unconditional**: the chain layer's own source name, so `layerName` `:27-35` stops calling it `branch`)
  plus `internal/policy/merge_test.go`.
- **The merged layer is not a `branch`.** A launch/chain layer merged at index ≥ 2 would be printed as
  `branch` by `layerName`, a place the number did not come from (F4, D1055). The new source name (`chain`)
  is what `runs show` prints.
- **Rigor (D1057):** the refusal is against the **chain's own** limit, never the mode's; per run the rounds
  are the level's default unless the recipe sets them, capped by **`RoundCeiling`'s output** and by the
  chain's remaining, and a refusal names that output — today POC 2, Startup 2, Pro 3, Critical 10, Hotfix 2.
  `RoundCeiling` (`internal/rigor/rounds.go:51-56`) is `min(max(n, minRoundCeiling=2), widestRoundCeiling=10)`
  and **two of the table's five rows sit below the floor** (POC 1, Hotfix 1) while **two sit above it** (Pro 3
  and Critical 10), so the code quotes the function's answer and never the row (F15). The owner's per-level
  **defaults** given 2026-11-01 (poc 1/1, startup 2/2, pro 3/3, critical 3/5, hotfix 1/1) do not change
  that: O3's recorded ruling keeps the floor at **2** (`docs/rigor-modes/reconciliation.md` §8), so a level
  whose default is 1 still runs at 2 today, and whether the *floor* moves is the rigor work's edit of
  `minRoundCeiling` — nothing here. **So the two tests below call `RoundCeiling` and assert against its
  return, never against a literal transcribed from either table**, which is what keeps them correct whichever
  way that question is settled.
- **Red step:** `TestRunRefusesALooseningLimit` fails while `--max-usd` does not exist;
  `TestRunGatesTheLimitsOnTheImage` fails while the gate is absent.
- **Tests:** `TestRunRefusesALooseningLimit` (both numbers and the layer named),
  `TestRunRefusesALooseningMaxTimeAgainstTotalTimeout` (F18), `TestRunAcceptsATighteningLimit`,
  `TestRunGatesTheLimitsOnTheImage`, `TestRunWritesNoLimitWhenNoneIsGiven` (the task is byte-identical),
  `TestRunTotalTimeoutAndMaxTimeComposeAsMin` (D1043), `TestRunBatchLimitsEveryAttempt` (D1053),
  `TestMergeNamesTheChainLayer` (D1055), `TestRunLimitNeverAboveTheModeCeiling` (D1057),
  `TestRunClampedRoundsRefusalNamesTheFigure` (D1057, F15).

## L4. `--continues RUN-ID`, `--used-*`, and what a chain is (D1045, D1048, D1049, D1052, D1053, D1058)

The link for a chain with no PR — a halted run that opened nothing — the caller's one honest knob,
`--used-*`, which may only raise the charge (D1048), and the **PR link that makes a review job part of the
chain it reviews** (D1058).

- **Files (4):** `internal/cli/run.go` (the `--continues` and `--used-*` flags, and the follow-up path where
  a `--pr` launch inherits the chain's remaining, D1052; **`--continues` is refused with `--pr`, `--retry`
  and `--attempts`**, as `--attempts` already refuses its three at `:300-308`), `internal/cli/run_continues_test.go`
  (new), `internal/cli/runs.go` (the message when `--pr` finds no run and `--continues` is the way, F9),
  `internal/cli/runs_test.go` (D1053's retry half: a retry is the stored task's chain, computed at its own
  launch, and it takes no new limits). D1053's batch half is L3's, where the flags are parsed.
- **The links are three and the engine derives the chain id from them** (the root run id): the PR, the stored
  task, and the run `--continues` names. A fifth `--chain ID` string is not added (F10, F11).
- **Red step:** `TestRunContinuesAHaltedChain` fails while the flag does not exist;
  `TestRunFollowUpInheritsRemaining` fails while a follow-up starts from the full cap.
- **Tests:** `TestRunContinuesAHaltedChain`, `TestRunRefusesAContinuesWithPR`, `TestRunRefusesAContinuesWithRetry`,
  `TestRunRefusesAContinuesWithAttempts`, `TestRunRefusesAContinuesItCannotConfirm` (T4),
  `TestRunContinuesWithAnUnreadableLedgerChargedTheCap` (D1049),
  `TestRunContinuesWithAUsedFigureThatRaises` (D1048),
  `TestRunRefusesAUsedFigureBelowWhatTheEngineRead` (D1048), `TestRunFollowUpInheritsRemaining`,
  `TestRunFollowUpMergesItsLimitUnderTheRemaining` (D1052), `TestRunRetryIsTheSameChain` (D1053),
  `TestReviewJobJoinsThePRChain` (D1058).

## L5. The runner applies `remaining` at bootstrap (D1044, D1047, D1050)

The runner reads the chain's **records by name** (the ids the launcher resolved, F21), computes `remaining`
with L0, and merges it as the last tighten-only layer, so the gateway's cap *is* `remaining` and nothing in
the gateway changes (D1059). The ledger figures it can read are its own run's (L1).

- **Files (5):** `internal/runner/policy.go` (`resolvePolicy`, `:65`), `internal/runner/policy_test.go`,
  `internal/runner/spend.go` (the cap the resolved policy yields, `:93`), `internal/runner/spend_test.go`,
  `internal/runner/halt.go` (the token cap comparison at `:149-153` now reads the tightened figure).
- **Red step:** `TestPolicyMergesTheChainRemaining` fails while the merge does not consult the chain;
  `TestPolicyChainRemainingCannotLoosen` fails while a hand-written task's looser value survives.
- **Tests:** `TestPolicyMergesTheChainRemaining`, `TestPolicyChainRemainingCannotLoosen`,
  `TestChainRecordsAreReadByNameNotListed` (F21), `TestSpendCapIsTheRemaining`,
  `TestTokenCapIsTheRemaining`, `TestChainZeroRemainingRefusesAtLaunch` (D1050 case 1),
  `TestHaltedOpenLedgerChargesTheHeldAmount` (D1050 case 3).

## L6. `fugaro runs show` and the `diagnose` block (D1055, D1056)

- **Files (7):** `internal/cli/runs_show.go` (new), `internal/cli/runs_show_test.go` (new),
  `internal/cli/runs.go` (the subcommand registered beside `ls`, `stats`, `digest` at `:107`),
  `internal/cli/diagnose.go` (the same block for a halted run), `internal/cli/diagnose_test.go`,
  `schemas/runs-show.schema.json` (new), `schemas/schemas_test.go`.
- **Red step:** `TestRunsShowPrintsTheChainLimits` fails while the subcommand does not exist.
- **Tests:** `TestRunsShowPrintsTheChainLimits` (chain, each dimension's limit/used/remaining and source),
  `TestRunsShowNamesTheChainLayerAsChainSource` (F4, D1055), `TestRunsShowMarksABudgetOffChainUnenforced`
  (D1046), `TestRunsShowJSONMatchesTheSchema`, `TestRunsShowNamesTheHalt`,
  `TestDiagnoseShowsTheChainForAHaltedRun`, `TestRunsShowRefusesARunThatIsNotInThisRepository`.

## L7. The launch reply's `chain` and `limits[]` (D1056)

`launchResult` (`internal/cli/run.go:73`) gains `chain` (the root run id) and `limits[]`, the same shape
`runs show --json` prints. The console's decoder ignores keys it does not know, so this is additive.

- **Files (3):** `internal/cli/run.go` (the `launchResult` members), `internal/cli/run_json_test.go` (new:
  the reply's shape and the unknown-key tolerance), `internal/cli/run_test.go` (the text reply names the
  chain only when one exists).
- **Red step:** `TestLaunchJSONCarriesTheChain` fails while the members do not exist.
- **Tests:** `TestLaunchJSONCarriesTheChain`, `TestLaunchJSONLimitsMatchRunsShow`,
  `TestLaunchJSONOmitsTheChainForAFirstRun`, `TestLaunchJSONMarksAnUnenforcedChain` (D1046).

## L8. Documentation, skill and release notes

- **Files (5):** `docs/design/v1.md` §4.4 and §4.5 (the follow-up's inheritance and the halt's writeback,
  owned elsewhere — see "Order"), `plugin/skills/working/SKILL.md` (a launch with a limit; owned elsewhere),
  `docs/releases/vX.Y.Z.md` (`### For operators`: "run `fugaro update`" and what it will ask; no new
  operator step is introduced by this work, E1 is a gate and L9's command is used only by choice), the
  generated `website/content/cli-reference.md` (regenerated, not hand-edited),
  `website/data/release-summaries.yaml`.
- **Red step:** the plugin's command test fails while the skill names `--max-usd` before L3 is merged; it
  turns green on L3's branch.
- **Tests:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs`, the plugin tests, `npm run check` in
  `website/`.

## L9. Raising a chain that has spent its limit: a record rewrite with a reason (D1061). **SEC**

A chain limit is a launcher's number held in the **root launch's `task.json`** (D1046), so raising an
exhausted chain is a **rewrite of that stored task, with the reason for the raise recorded** — no new command
and no new node (F22, spec §4). The design does not pretend a launcher cannot do it: a launcher holds
`objectUser` on the `runs/` and `tokens/` prefixes (F21), so what this task builds is the **supported path**
an honest launcher uses, and
the reason it is a convention rather than a platform-enforced bound is written where a reader finds it
(spec §9).

- **Files (4):** `internal/cli/budget_chain.go` (new: the subcommand, beside the budget family, rewriting the
  root run's stored task through the same runs-bucket path a launcher already has, and recording the reason),
  `internal/cli/budget_chain_test.go` (new: the rewrite, the reason, and the refusals),
  `internal/runstore/runstore.go` (the record gains the raise's entry — who, when, from, to and why — beside
  the policy record it already writes at `:859-865`), `internal/runstore/runstore_test.go`.
- **Red step:** `TestChainRaiseRewritesTheRootTask` fails while the subcommand does not exist;
  `TestChainRaiseRefusesAnEmptyReason` fails while a reasonless raise is accepted.
- **Tests:** `TestChainRaiseRewritesTheRootTask`, `TestChainRaiseRecordsTheReasonAndTheFigures`,
  `TestChainRaiseRefusesAnEmptyReason`, `TestChainRaiseLeavesTheTaskUntouchedOnFailure`,
  `TestRunRefusesARaiseShapedFlag` (no launcher-reachable raise flag on `fugaro run`).

## Shared files

A file that more than one task writes, and what each writes there.

| File | Tasks | What each writes |
| --- | --- | --- |
| `internal/budget/ledger.go` | L1, and read by L0 | L1 adds the named read; L0 consumes it through its one-method `LedgerReader` interface, so the fake in L0's tests is the whole seam |
| `schemas/schemas_test.go` | L2, L6 | one row per new schema or new member |
| `internal/cli/run.go` | L3, L4, L7 | L3 the four flags, L4 `--continues` and the follow-up inheritance, L7 the reply's members |
| `internal/cli/runs.go` | L4, L6 | L4 the `--pr`-found-nothing hint, L6 the `runs show` subcommand registration |
| `internal/task/task.go` | L2, and read by L3 | L2 adds the four `Overrides` members; L3 writes them |

## Live checks

One, and it is **named and not numbered**: *a chain that halts at its dollar cap, is continued with
`--continues`, and reports a smaller remaining than it started with*. The register's highest written section
is **Check 190**, and **Check 125** is claimed by the open `cloud-senior-review` design (#1472), so the task
that writes this check takes the number free at **its** merge, under the register's rule that whichever of
two open claims merges second renumbers itself (`docs/gcp-live-checklist.md`). It is owner-run, needs a real
project and a real spend, and is not part of any task above.

## Release slot

- **This design** merges in the **0.15.0** window, unmerged until the 0.14.2 tag.
- **Implementation** is recommended for **0.16.0**: L3 changes what `fugaro run` writes into a task, and E1's
  gate means the release must also carry the new `*Since` constant and the `fugaro image refresh` path. A
  release that carried L3 without E1 would ship a launch that fails in the container, which the HARD RULE
  forbids. L9 adds a command, not a step: `fugaro update` still converges an installation on its own, and
  nothing in this work asks an operator to run anything.
