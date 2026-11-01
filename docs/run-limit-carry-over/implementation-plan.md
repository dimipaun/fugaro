# Per-launch limits and a chain budget: implementation plan

**The decisions are open; nothing here is approved.** This plan is written to the *recommended* answer of
each decision (D901–D920, [reconciliation.md](reconciliation.md) §3). A decision answered the other way
changes the tasks that cite it. Each task touches at most 12 files, names its tests, starts with a red step
and ends at the gates below.

## Order, and what this plan does not own

- **External dependency E1 (image skew):** the launcher gates a launch that writes any of the four
  `overrides` members behind `checkImageSince` with this work's own `*Since` constant
  (`internal/cli/recipes_skew.go:700`). The release that carries L3 **must** also ship the new constant and
  the gate, or a launch against an older job image fails in the container (`internal/task/task.go:863`,
  `DisallowUnknownFields`). There is nothing for `fugaro update` to converge here: no job environment
  changes (F3), so the gate is the whole story.
- **External dependency E2 (the cloud senior reviewer):** `docs/cloud-senior-review/` is open PR #1472 and
  not on `main`. L0–L8 do not depend on it. The one thing L4 gives it for free is `--continues`, which is
  what a review job that must not outspend a chain would pass (D918).
- **Files other work owns:** registering the `runs show` subcommand is inside `internal/cli/runs.go`, which
  this work owns for that purpose; the generated `website/content/cli-reference.md`, the plugin skills and
  every `console/**` file are owned elsewhere. L8 names them and the task that touches them waits for the
  owner's go.

| Level | Tasks | Needs |
| --- | --- | --- |
| 0 | L0, L1, L2 | nothing |
| 1 | L3 | L1, L2 |
| 2 | L4, L5 | L3 (L5 also L0) |
| 3 | L6, L7 | L0, L5 |
| 4 | L8 | L1–L7; E1 merged in the same release |

## Where each decision lands

Every decision D901–D920 has one owning task, so a reviewer can follow a ruling to the work that carries
it. A decision answered the other way changes that task and the spec paragraph that names it.

| Decision | Task | Decision | Task |
| --- | --- | --- | --- |
| D901 carrier in `task.json` | L2 | D911 the record's `tokens` | L1 |
| D902 one flag per dimension | L3 | D912 `--pr` inheritance | L4 |
| D903 loosening is refused | L3 | D913 `--retry` / `--attempts` | L4 (retry), L3 (batch) |
| D904 merged twice | L3, L5 | D914 the orchestrator and the coordinator | none — nothing in `ee/` changes |
| D905 what a chain is | L0, L4 | D915 `runs show` and the display | L6 |
| D906 the ledger is the records | L0 | D916 the JSON additions | L1, L2, L6, L7 |
| D907 `remaining` from settled | L0, L5 | D917 under the rigor ceiling | L3 |
| D908 `--used-*` may only raise | L4 | D918 the cloud senior reviewer | none — nothing here changes for it |
| D909 unreadable is unknown | L0, L4 | D919 the gateway and the day caps | none — nothing in the gateway changes |
| D910 the halted-run writeback | L5 | D920 clock skew | L0 |

## Gates (every task)

`go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); the task's named tests with
`GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1`; `GOFLAGS=-p=2 go test ./internal/cli/ -run
Docs -count=1 -timeout 40m` when a doc changed; `go test ./internal/eeboundary/... -count=1`. Never the whole
`internal/cli` or `internal/runner` package.

## L0. The chain read: resolve a link and sum what the chain used (D905, D906, D907, D920)

The engine's one read of "what the chain already spent". No new store: the records are the ledger (F7).

- **Files (5):** `internal/runstore/chain.go` (new: `Chain` with `Root`, `Runs []Record`; `ResolveChain`,
  `Used(dimension)`, `Remaining(limit, dimension)`), `internal/runstore/chain_test.go` (new),
  `internal/runstore/runstore.go` (the record's `Tokens` member lands here with L1; this task reads it),
  `internal/runstore/testdata/chain/` (new: placeholder run ids only), `internal/runview/runview.go` (the
  still-running time read, `now − started_at`, off `Row`).
- **Interface:** `Used` returns dollars from `cost.model_usd` (settled, never `budget.granted_micros`),
  tokens from the record's `tokens`, time as `max(finished−started, Σ stages[].duration_s)`, and rounds as
  `len(reviews)`.
- **Red step:** `TestChainUsedSumsTheRecords` fails while `ResolveChain` does not exist; a chain whose
  second record is unreadable makes `TestChainUnreadableRecordIsUnknownNotZero` fail with a zero total
  (D909).
- **Tests:** `TestChainUsedSumsTheRecords`, `TestChainUsedReadsSettledDollarsNotTheLease`,
  `TestChainTimeTakesTheLargerOfRecordAndStages` (D920), `TestChainUnreadableRecordIsUnknownNotZero`,
  `TestChainOfAPRFollowsPreviousRun` (F10), `TestChainOfARetryIsTheStoredRunID`,
  `TestChainRefusesAContinuesItCannotConfirm`.

## L1. The run record's token total (D911, D916)

The one wire addition this work needs (F8): `result.json` gains `tokens`, within version 1.

- **Files (5):** `internal/runstore/runstore.go` (`Tokens *TokenTotal` on `Record`, with the input/output/
  cache split), `schemas/result.schema.json` (the new object, additive), `schemas/schemas_test.go` (the
  schema still loads and a record with `tokens` validates), `internal/runner/cost.go` (the write, beside the
  gateway cache totals it already sets at `:115`), `internal/runner/cost_internal_test.go` (new).
- **Red step:** `TestRecordWritesTheTokenTotal` fails while the field is absent; `TestResultSchemaAcceptsTokens`
  fails while the schema does not declare it.
- **Tests:** `TestRecordWritesTheTokenTotal`, `TestRecordOmitsTokensWhenTheRunMadeNoCall`,
  `TestResultSchemaAcceptsTokens`, `TestResultSchemaStillRefusesAnUnknownMember`.

## L2. The four `overrides` members (D901, D916)

`task.json`'s `overrides` gains `max_usd`, `max_tokens`, `max_time`, `max_review_rounds`
(`internal/task/task.go:753`), additive within version 1.

- **Files (4):** `internal/task/task.go` (the four members on `Overrides`), `internal/task/task_test.go`
  (decode, and a task with them round-trips), `schemas/task.schema.json` (the four members, additive),
  `schemas/schemas_test.go`.
- **Red step:** `TestTaskDecodesTheLaunchLimits` fails while the members do not exist.
- **Tests:** `TestTaskDecodesTheLaunchLimits`, `TestTaskRejectsANegativeLimit`,
  `TestTaskSchemaAcceptsTheLaunchLimits`, `TestTaskSchemaStillRefusesAnUnknownMember`.

## L3. The four flags, and the tighten-only refusal (D902, D903, D904, D913). **SEC**

The launcher resolves the effective policy before the launch and refuses a value that would loosen, naming
both numbers and the layer that set the lower one (F4). It gates the launch behind a new `*Since` constant
(E1).

- **Files (7):** `internal/cli/run.go` (the four flags on `newRunCmd`; the resolve-then-merge before the
  write, beside the gates at `:727-801`), `internal/cli/run_limits_test.go` (new),
  `internal/cli/recipes_skew.go` (the new `*Since` constant, beside `governanceSince` `:531`),
  `internal/cli/recipes_skew_test.go` (the gate refuses an older image and names `fugaro image refresh`),
  `internal/cli/launch_budget.go` (the resolve helper the pre-check and the merge share),
  `internal/cli/launch_budget_test.go`, `internal/policy/merge.go` (only if the four dimensions need a
  `Layer` member the existing one does not carry; search before adding).
- **Red step:** `TestRunRefusesALooseningLimit` fails while `--max-usd` does not exist;
  `TestRunGatesTheLimitsOnTheImage` fails while the gate is absent.
- **Tests:** `TestRunRefusesALooseningLimit` (both numbers and the layer named),
  `TestRunRefusesAMaxReviewRoundsAboveTheModeCeiling` (D917), `TestRunAcceptsATighteningLimit`,
  `TestRunGatesTheLimitsOnTheImage`, `TestRunWritesNoLimitWhenNoneIsGiven` (the task is byte-identical),
  `TestRunTotalTimeoutAndMaxTimeComposeAsMin` (D903, F18).

## L4. `--continues RUN-ID`, `--used-*`, and what a chain is (D905, D908, D909, D912, D913)

The link for a chain with no PR — a halted run that opened nothing — and the caller's one honest knob,
`--used-*`, which may only raise the charge (D908).

- **Files (4):** `internal/cli/run.go` (the `--continues` and `--used-*` flags, and the follow-up path where
  a `--pr` launch inherits the chain's remaining, D912; refused with `--pr`, `--retry` and `--attempts`, as
  `--attempts` already refuses its three at `:284`), `internal/cli/run_continues_test.go` (new),
  `internal/cli/runs.go` (the message when `--pr` finds no run and `--continues` is the way, F9),
  `internal/cli/runs_test.go` (D913's retry half: a retry is the stored task's chain, computed at its own
  launch, and it takes no new limits). D913's batch half — a `--max-*` on a batch applies to every attempt
  and the batch is N chains, not one — is L3's, where the flags are parsed.
- **Red step:** `TestRunContinuesAHaltedChain` fails while the flag does not exist;
  `TestRunFollowUpInheritsRemaining` fails while a follow-up starts from the full cap.
- **Tests:** `TestRunContinuesAHaltedChain`, `TestRunRefusesAContinuesItCannotConfirm` (T3),
  `TestRunRefusesAContinuesWithNoUsableRecordWithoutUsed` (D909),
  `TestRunContinuesWithAUsedFigureThatRaises` (D908),
  `TestRunRefusesAUsedFigureBelowWhatTheEngineRead` (D908),
  `TestRunFollowUpInheritsRemaining`, `TestRunFollowUpMergesItsLimitUnderTheRemaining`,
  `TestRunRetryIsTheSameChain` (D913), `TestRunBatchIsNChainsNotOne` (D913, L3's flag parsing).

## L5. The runner applies `remaining` at bootstrap (D907, D919)

The runner reads the chain's records, computes `remaining`, and merges it as the last tighten-only layer, so
the gateway's cap *is* `remaining` and nothing in the gateway changes.

- **Files (5):** `internal/runner/policy.go` (`resolvePolicy`, `:65`), `internal/runner/policy_test.go`,
  `internal/runner/spend.go` (the cap the resolved policy yields, `:93`), `internal/runner/spend_test.go`,
  `internal/runner/halt.go` (the token cap comparison at `:149-153` now reads the tightened figure).
- **Red step:** `TestPolicyMergesTheChainRemaining` fails while the merge does not consult the chain;
  `TestPolicyChainRemainingCannotLoosen` fails while a hand-written task's looser value survives.
- **Tests:** `TestPolicyMergesTheChainRemaining`, `TestPolicyChainRemainingCannotLoosen`,
  `TestSpendCapIsTheRemaining`, `TestTokenCapIsTheRemaining`, `TestChainZeroRemainingRefusesAtLaunch`
  (D910).

## L6. `fugaro runs show` and the `diagnose` block (D915, D916)

- **Files (7):** `internal/cli/runs_show.go` (new), `internal/cli/runs_show_test.go` (new),
  `internal/cli/runs.go` (the subcommand registered beside `ls`, `stats`, `digest` at `:107`),
  `internal/cli/diagnose.go` (the same block for a halted run), `internal/cli/diagnose_test.go`,
  `schemas/runs-show.schema.json` (new), `schemas/schemas_test.go`.
- **Red step:** `TestRunsShowPrintsTheChainLimits` fails while the subcommand does not exist.
- **Tests:** `TestRunsShowPrintsTheChainLimits` (chain, each dimension's limit/used/remaining and source),
  `TestRunsShowJSONMatchesTheSchema`, `TestRunsShowNamesTheHalt`, `TestDiagnoseShowsTheChainForAHaltedRun`,
  `TestRunsShowRefusesARunThatIsNotInThisRepository`.

## L7. The launch reply's `chain` and `limits[]` (D916)

`launchResult` (`internal/cli/run.go:73`) gains `chain` (the root run id) and `limits[]`, the same shape
`runs show --json` prints. The console's decoder ignores keys it does not know, so this is additive.

- **Files (3):** `internal/cli/run.go` (the `launchResult` members), `internal/cli/run_json_test.go` (new:
  the reply's shape and the unknown-key tolerance), `internal/cli/run_test.go` (the text reply names the
  chain only when one exists).
- **Red step:** `TestLaunchJSONCarriesTheChain` fails while the members do not exist.
- **Tests:** `TestLaunchJSONCarriesTheChain`, `TestLaunchJSONLimitsMatchRunsShow`,
  `TestLaunchJSONOmitsTheChainForAFirstRun`.

## L8. Documentation, skill and release notes

- **Files (5):** `docs/design/v1.md` §4.4 and §4.5 (the follow-up's inheritance and the halt's writeback,
  owned elsewhere — see "Order"), `plugin/skills/working/SKILL.md` (a launch with a limit; owned elsewhere),
  `docs/releases/vX.Y.Z.md` (`### For operators`: "run `fugaro update`" and what it will ask; no new
  operator step is introduced by this work, E1 is a gate and not a command), the generated
  `website/content/cli-reference.md` (regenerated, not hand-edited), `website/data/release-summaries.yaml`.
- **Red step:** the plugin's command test fails while the skill names `--max-usd` before L3 is merged; it
  turns green on L3's branch.
- **Tests:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs`, the plugin tests, `npm run check` in
  `website/`.

## Shared files

A file that more than one task writes, and what each writes there.

| File | Tasks | What each writes |
| --- | --- | --- |
| `internal/runstore/runstore.go` | L0, L1 | L1 adds the record's `Tokens` member; L0 reads it |
| `schemas/schemas_test.go` | L1, L2, L6 | one row per new schema or new member |
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
  forbids. Nothing here needs a `### For operators` step beyond "run `fugaro update`".
