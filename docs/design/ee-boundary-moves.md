# The core-to-`ee/` moves: the coordinator, `fugaro report` and governance

Status: **design, no code. Nothing described here is built.** It is task E0.3 of
`docs/ee-reorg/implementation-plan.md` (slice E0, level L0), and it supersedes subscriptions
S1.1's file lists where the two differ — the differences are marked *corrected by E0.3* below and
listed in one place in §11. The owner approved E0 on 2026-10-29; every decision it rests on
(D520–D536) is answered in `docs/ee-reorg/reconciliation.md` §4.

Every path and symbol below was verified against the tree at `ac06578a` by name — a file, a
function, a constant or a struct field, not a paraphrase — because this document is what E1.1–E1.7
and E2.1–E2.3 build against, and a citation that has moved is a design nobody can implement.

**What this document does not do.**

- It changes no wire format (R7): no JSON or YAML field is renamed, no schema under `schemas/`
  moves or tightens, and `version` stays 1 everywhere. A format is not a feature.
- It writes no notice of any kind. D523 is answered NOT the recommendation: there is no licence
  notice until the verifier, the key-minting service and the launch are ready, so no stderr line
  on `report` or `coordinator`, no run-log licence line, and the 0.13.0 notes carry no licence
  warning. Where this document would otherwise have specified one, it says **deferred with the key
  work (D523)**.
- It carries no price, no currency figure and no commercial term, and names no person or
  organisation.
- It decides no rights question: what `ee/LICENSE` says about the moved code, and what the
  Subscription Terms say about the coordinator, are counsel's and the owner's (§E3.6, §E3.5 of the
  plan; `docs/licensing/`).

## 1. The shape, in one paragraph

One binary ships (T21). The core stays MIT and keeps **the wire and the mechanism** it already
has — the `governance:` types and their strict decoding, the schemas, `internal/tier`, the task's
`tier` and `expected_scope` fields, the record fields and halt reasons, the halt-with-checkpoint
plumbing, the spend caps and the launch-time skew gates — while **the policy and the features**
move to `ee/`: the coordinator's library and command, `fugaro report`, the governance defaults, the
tighten-only merge, the stage timeout and the tripwires. The core reaches `ee/` through a small set
of seams whose core defaults are today's behaviour, and `cmd/fugaro/ee_on.go` → `ee/register` is the
one assembly point (R2). An untagged build therefore keeps working, refuses nothing, and simply has
no governance of its own (§6).

## 2. The coordinator (D434, T10 — task E2.1)

### What moves to `ee/coordinator/`

`git mv` of the library `internal/coordinator/` — `intent.go`, `project.go`, `state.go`, `sweep.go`,
`view.go` and their tests (`intent_test.go`, `project_test.go`, `state_test.go`, `sweep_test.go`,
`sweep_bounds_test.go`, `view_test.go`) and `testdata/`. R5 says the folder stays in this public
repository; the move is a `git mv` plus the notice plus the package path, in one commit, so
`git log --follow` keeps the history, and no behaviour change rides in that commit.

The CLI half moves to `ee/coordinator/cli/`: `internal/cli/coordinator.go` (the `report`, `sweep`
and `status` commands — `newCoordinatorReportCmd`, `newCoordinatorSweepCmd`, `newCoordinatorCmd`,
`newCoordinatorStatusCmd`), `internal/cli/coordinator_view.go` (`--from-store`:
`runCoordinatorStatusFromStore`, `coordinatorStoredView`), and their tests
(`coordinator_test.go`, `coordinator_view_test.go`).

The whole of `coordinator.go` goes, not part of it: every unexported symbol in it is used by
nothing outside it or by `run.go` — `openCoordinatorStore`, `coordinatorAppCredential`,
`coordinatorTokens`, `coordinatorRevoke`, `coordinatorMint`, `installCoordinatorSource`,
`openCoordinatorProvider`, `coordinatorRepos`, `coordinatorViewOf`, `writeCoordinatorSweep`,
`coordinatorSweepRepos`, `coordinatorReportRepo`, `writeCoordinatorReport`, `coordinatorCellList`,
`coordinatorRunsSource`. `openCoordinatorStore` is also the function `run.go`'s launch declarer
calls, which is why the declarer's implementation moves with it (§5.1).

### What stays in the core, and why

- **The database paths, in a new small MIT package `internal/coordpath/`** (corrected by E0.3:
  S1.1 kept them in `internal/coordinator`, which cannot stay once the library moves). Three files
  outside the coordinator name them today and must keep doing so, because the project rules and the
  rules' tests are core:
  - `internal/cli/project_cmd.go:407` writes `coordinator.PathRoot` into the generated rules;
  - `internal/budget/rules/coordinator_test.go` (`PathRoot`) and `rules_test.go:1705` (`PathRoot`);
  - `internal/budget/rules/emulator_coordinator_test.go` (`PathRoot`, `PathProject`,
    `PathProjectDocument`, `PathRepoDocument`, `PathRepos`, `DocumentID`).
  `internal/coordpath/` holds exactly what those readers name — `PathRoot`, `PathProject`,
  `PathRepo`, `PathRepos`, `PathProjectDocument`, `PathRepoDocument`, `DocumentID` — and nothing
  else; `ee/coordinator` imports it, so the tree of the database has one definition and the rules
  cannot drift from the code that reads them.
- **The launch's declaration**, through the declarer hook (§5.1). An untagged build declares
  nothing, which is D535's accepted consequence: a source build's runs still appear through the
  sweep, which reads the runs bucket.
- **The console's read table**: `internal/cli/project.go`'s `consoleReadPaths` names
  `"fugaro report"` (line 219) and `"fugaro coordinator status"` (line 237). Both entries stay, and
  after the move they name commands an untagged build answers with a stub (§6). The table is what
  `internal/cli/docs_desktop_console_test.go` checks the console's design §5.1 argv against, in both
  directions (an allowed argv must be listed; a listed path must be an allowed argv or a read the
  design names), so removing an entry would fail that check and would also be untrue — the console
  really does spawn both, and a tagged binary really does run both.

F6's third gap stays core with it: the sweep job's Terraform
(`deploy/terraform/gcp/modules/installation/coordinator.tf`, which starts
`/usr/local/bin/fugaro coordinator sweep`), `internal/infra`'s `CoordinatorSpec` and
`CoordinatorImageExists`, `internal/infra/tf/cover.go`'s `toCoordinator`, and the `--coordinator`
opt-in. All of it deploys a job; the *job's binary* is what is licensed, not the code that schedules
it — and the Terraform is buried in the released CLI, where a free installation's `fugaro update`
must still be able to write it.

### Test data

Four files move with their tests, each gaining the `.license` sibling the `ee/` notice rule
requires of a type with no comment syntax: `internal/cli/testdata/coordinator_status.golden`,
`coordinator_mint_refused.json`, `coordinator_no_requests.json`, `coordinator_read_token.json`.

`coordinator_token.json` is different: it stays at `internal/cli/testdata/coordinator_token.json`
and the moved tests take **a copy** under `ee/coordinator/cli/testdata/`, with its own `.license`
sibling. The copy is not a convenience: the moved tests read their fixtures by a path relative to
their own package, as `internal/cli/coordinator_test.go:604` reads
`filepath.Join("testdata", "coordinator_token.json")` today, so the file has to exist under the new
package as well. The reason S1.1 gives for keeping
the original — that `internal/gitprov/github/apptoken_test.go` reads it too — is true of
`internal/gitprov/github/testdata/coordinator_token.json`, which is a separate file in the
provider's own package; `internal/cli/testdata/coordinator_token.json` has one reader today,
`coordinator_test.go:604`. Keeping it there deletes nothing from the core, which is the smaller
change of the two, and the plan lists the copy.

### Pins and dependents to re-point

- `internal/cli/docs_desktop_console_test.go`: the nine view types
  (`coordinator.StoredProjectView`, `RepoView`, `View`, `Record`, `Run`, `PullRequest`, `Collision`,
  `Unread`, `Stale`, at lines 403–411 and 582–586) and `newCoordinatorStatusCmd` in
  `consoleContractCommands` (line 844) leave the core test and are pinned by a tagged test under
  `ee/coordinator/cli`. `consoleContractCommands` keeps `report` only until E2.2, which re-points
  that entry the same way.
- `internal/cli/doctor_policy_test.go`, `internal/cli/runid_test.go` (its
  `{cmd: "coordinator status", window: lsDefaultSince}` case, line 493), `internal/cli/root.go`
  (the entry in `root.AddCommand`).
- `internal/runview/launched_by_test.go`: its allow-list entry
  `"internal/coordinator/view.go": "the view's declared_by field…"` is re-pointed to the moved path,
  **never loosened** — the launcher's name is untrusted text and that list is the record of who may
  name it.
- `docs/coordinator/implementation-plan.md` and `docs/coordinator/README.md`: their
  `internal/coordinator` citations are re-pointed in the commit that moves the code, because
  `internal/cli/docs_coordinator_test.go`'s `TestDocsCoordinatorPlanCitationsExist` resolves every
  path the plan cites, its directories included, and fails naming a citation that no longer exists.
- `website/content/cli-reference.md` is regenerated by E1.7's generator, after the move (§9).
- `ee/register/coordinator.go` (new) registers the command and restores today's declarer as the
  launch hook's implementation; `ee/register/coordinator_test.go` is the identity check, that the
  hook holds `ee/`'s function and not merely a non-nil one (G10).

## 3. `fugaro report` (D444, T11 — task E2.2)

### What moves to `ee/report/` and `ee/report/cli/`

`internal/report/` in full (`report.go`, `render.go`, `report_test.go`), and the command part of
`internal/cli/report.go` + `report_test.go`: `newReportCmd`, `reportOptions`, `reportRange`,
`runReport`, `reportEmit`, `reportByMode`, `modeRecords`, `reportDegraded`, `reportLive`,
`lazyFacts`, `daysWithCompute`, `microsOfUSD`, `applyCost`, `reportMaxRuns`, `reportModeSourceNote`,
`reportComputeEstimateNote`.

**`reportHistory` moves with the command** (corrected by E0.3: S1.1 listed it among what stays in
core). It returns `[]report.Rec` (`internal/cli/report.go:210`), so a core copy would have to name
a type that has moved; its only caller is `report.go:174`, in the same file; and the one other
mention in core is a comment (`internal/cli/project_enroll.go:332`). Nothing core can keep it.

### What stays in the core, in a new `internal/cli/reportcore.go`

Exactly the five symbols core MIT code reads, each with the callers that pin it:

| Symbol | Read by |
| --- | --- |
| `costLines` (`report.go:567`) | `internal/cli/diagnose.go:652`, `cloudrun_golden_test.go` |
| `recordOfRun` (`report.go:599`) | `cloudrun_golden_test.go:119` |
| `newReportFirestore` (`report.go:349`) | `internal/cli/project_enroll.go:342` |
| `firestoreErr` (`report.go:371`) | `internal/cli/project_enroll.go:353`, `:361` |
| `computeSources` (`report.go:529`) | `cloudrun_golden_test.go:122`, `report_source_test.go:30` |

`internal/cli/report_test.go` splits the same way. It stays in core for the cases that test a
symbol the core keeps — `TestReportNoAuthNeverReachesRealFirestore` (line 984) exercises
`newReportFirestore` — and for `TestReportDimParsing` (line 972), which tests the period vocabulary
by name and re-points to the digest's copy (§3, `ParseDim`). The command's own cases — its flags,
its output shapes, the degraded paths — move with it.

The moved command still needs two of the five, through the helper API (§10):
`newReportFirestore` and `firestoreErr` are what `reportHistory` (`report.go:214`, `:226`, `:234`)
and `fugaro report`'s own degraded path open Firestore with, published there as
`Cloud.Firestore` and `Cloud.FirestoreErr`. The other three are the core's alone.

### The digest's MIT copy, and the `USD6` gap (F6)

`internal/digest` must keep building with `ee/` deleted (G1, G5: `TestDigestNeedsNoEE`), so what it
reads out of `internal/report` moves into a new `internal/digest/period.go` as MIT code:
`Dim` and its constants (`ByDay`, `ByWeek`, `ByMonth`, `ByYear`, `ByRepo`, `ByModel`, `ByPerson`,
`ByMode`), `ParseDim`, `PeriodKey`, `USD` — **and `USD6`** (corrected by E0.3: S1.5's list omits
it, and the digest cannot compile without it). `report.USD6` is read at
`internal/digest/patterns.go:296`, `:487`, `:492`, `internal/digest/render.go:107`, `:108`, `:120`
and `internal/digest/digest_test.go:535`.

Re-pointed to the copy in the same commit: `internal/digest/digest.go` (`Dim`, `PeriodKey`, the
`NewPeriod` clock comment), `layout.go` (`Dim`, `ReportName`), `patterns.go` (`byPeriod`,
`periodDim`, the medians), `render.go` (`USD`, `USD6`), `internal/runner/digest.go`,
`internal/cli/runs_digest.go` (`digestDim`, `digestPeriod`, the `dim` field) and the tests
`internal/digest/digest_test.go`, `layout_test.go`, `internal/cli/runs_digest_test.go`,
`internal/runner/digest_test.go`.

Nothing else of the digest moves: the five governance halt reasons it counts, the `calls`,
`agents`, `repeats` and `review_scope` fields it reads and its two report-only flags (a stage at
five repeats or more, review-scope drift) are core under D521/D528, so the digest stays MIT and
`diagnose` keeps rendering halts and checkpoints (D529).

### The rest

- `plugin/skills/working/SKILL.md:53` (`fugaro report --by …`) marks the command as one of the
  business-licensed features and the digest as free (§E3.1 of the plan writes the wording).
- `internal/cli/runid_test.go`'s `{cmd: "report", window: reportDefaultSince}` case (line 487),
  `internal/cli/root.go`, `website/content/cli-reference.md` (regenerated), `ee/register/report.go`
  and `ee/register/report_test.go` (the identity check).
- **deferred with the key work (D523)**: `TestNoticeNeverOnStdout` (R4's rule that a notice would go
  to stderr so `--json` stays parseable for the console). With no notice there is nothing to keep off
  stdout; the test is written with the notice, not now.

## 4. Governance: the split (D521 — task E2.3)

Governance is not a package to move; it is fields and branches in eleven shared packages (F7), so
D521's line is *the wire and the mechanism stay, the policy goes*. Below, symbol by symbol.

### Stays in `internal/` (the wire and the mechanism)

| Package | Symbols |
| --- | --- |
| `internal/config` | `Config.Governance`, the types `Governance`, `GovernanceTiers`, `TierLimits`, `StageBudget`, `StageTokens`, `StageTimeout` (`config.go`); `ProjectLayer.Governance`, `SetsGovernance` (`layer.go`); `setsGovernance` (`resolve.go`); `validateGovernance` (`validate.go`); the `governance.*` rows of `scope.go`. Strict decoding (`KnownFields(true)`) stays, which is what makes F9's compatibility surface safe. |
| `internal/task` | `Spec.Tier`, `Spec.ExpectedScope`, and the strict `task.Parse` |
| `internal/tier` | the whole package: the four-word vocabulary the task validator uses |
| `internal/runstore` | `StageTiming.Calls`, `.Agents`, `.Repeats`, `Record.ReviewScope`, `Record.Checkpoint`, and the halt reasons `HaltStageBudget`, `HaltReviewRatio`, `HaltFanout`, `HaltReviewScope`, `HaltExpectedExceeded` |
| `internal/policy` | `Governance` and the rest of the vocabulary, `Effective.Governance`, `Effective.DefaultGovernanceKeys`, `Governance.WithDefaults`, `TierKey`, and the merge's non-governance half (`Merge`, `tightenCap`, `tightenInt`, `tightenDuration`, `toSet`, `dedupe`) |
| `internal/runner` | `governanceHalt` (`halt.go:238`), `checkpointPR` (`halt.go:278`), `checkpointReportPart` (`prflow.go:503`), `checkpointPrompt` (`prompts.go:220`), `readReviewScope` (`diffbase.go:280`), `stageTiming` (`cost.go:37`) |
| `internal/agent` | `SubAgents`, `Repeats` |
| `internal/gateway` | `StageReport`, `.Agents` |
| `internal/digest` | the halt accounting and the two report-only flags |
| `internal/cli` | `governanceSince` (`recipes_skew.go:510`), `refuseGovernanceSkew`, `checkGovernanceImage`, `checkCloneGovernanceImage`, `governanceImageWarnings`, `governanceLaunchSources`, the launch gates in `run.go` and `validate.go`'s call, `--tier` (T8.1b), and `diagnose.go`'s `checkpointBlock` |
| `schemas/` | `fugaro.schema.json` and `project-layer.schema.json` (`governance`), `result.schema.json` (`calls`, `agents`, `repeats`, the five reasons, `checkpoint`, `review_scope`), `task.schema.json` (`tier`, `expected_scope`) — unchanged (R7) |

Two reasons this is the line, and both are load-bearing: strict decoding means the core must know
the block, or an untagged runner refuses the file — and in the project layer that stops every
repository of the project (F9); and the MIT digest and `diagnose` read the records, so the record
fields and the halt enum cannot move (F8, D528, D529). The launch-time skew gates stay core for the
first reason from the other side: they protect a free user from an old runner.

`internal/runner/checkpoint.go` and `checkpoint_schedule.go` are the older checkpoint *pushes*
(design `checkpoint-pushes.md`), not governance, and stay core. `runstore.Review.Tier` and the
credentials service's tiers are other tiers.

### Moves to `ee/governance/`

| From | To `ee/governance/` |
| --- | --- |
| `internal/policy/policy.go`: `DefaultGovernance` (line 199), `DefaultTierExpectations` (line 122) | `policy.go` |
| `internal/policy/merge.go`: `(*Effective).tightenGovernance` (line 138), `(*Effective).tightenTier` (line 166) | `policy.go` |
| `internal/runner/runner.go`: `(*run).stageTimeout` (line 2477) and the stage loop's halt decision at line 2812 | `runner.go` |
| tests: `internal/policy/governance_test.go`, `tier_test.go`, `internal/runner/stage_timeout_test.go` and the internal one #1216 adds | `policy_test.go`, `runner_test.go` |

`stageTimeoutKey` (`runner.go:2497`) and `noteStageTimeoutClamped` (`runner.go:2539`) go with
`stageTimeout`: they are how the key is named and how a clamped bound is recorded, and none of the
three is read from outside the runner.

`ee/governance` also takes what has not been written yet: the checkpoint model call (T7.2–T7.3), the
tier expected-cost checkpoint (T8.2) and the tier calibration (T8.3), all born there (D527), plus
`ee/governance/cli` (D530) for the first governance command — E2.3 adds none.

### New in the core for the split

- `internal/policy/hook.go` (E1.2), `internal/runner/governance_hook.go` (E1.2),
  `internal/digest/section.go` (E1.2) — the seams of §5.2–§5.4.
- `internal/runner/runnertest/` (E1.2, exported): the minimal stage harness `ee/governance`'s tests
  need, because the runner's own `_test` harness cannot be imported from `ee/`.
- Re-pointed so the core default path still runs: the governance cases of
  `internal/runner/policy_internal_test.go`, `halt_test.go` and
  `internal/cli/project_policy_test.go` keep only the core-default path (R8).
- `ee/register/governance.go`, `ee/register/governance_test.go` (the identity check).

`TestStageTimeout*` moves with the code and runs through `internal/runner/runnertest`;
`TestCapsUnchangedByTheTag` (G7) lives in `ee/governance` and runs the spend caps with the hook set
and unset, because the core cannot import `ee/` to do it the other way round.

## 5. The seams (E1.1, E1.2)

### 5.1 The command registry and the launch declarer — `internal/cli`

E1.1 publishes, in `internal/cli/extend.go`:

```go
// RegisterCommand adds a paid command to the root. One binary ships (T21), so the
// moved command joins the tree the released CLI builds.
func RegisterCommand(name string, newCmd func() *cobra.Command) error

// LaunchDeclarer declares one launch; the core default is the declarer below.
type LaunchDeclarer func(ctx context.Context, c *Cloud, slug string, spec *task.Spec) error
func SetLaunchDeclarer(d LaunchDeclarer) error
```

- `RegisterCommand` **refuses a name a core command holds** — that is the whole
  `TestRegisteredCommandCannotShadowACoreCommand` — and refuses a late or duplicate registration.
  While `report` and `coordinator` are still core commands (until E2.1/E2.2), `root.go` keeps adding
  them and the stub is not installed for a name a core command holds.
- The **launch-declarer hook's core default is today's declarer** (`reportLaunchIntent`,
  `internal/cli/run.go:2881`, which calls `(&coordinator.Reporter{Store: store}).Report`), so a hook
  that landed before the move cannot stop every launch's declaration (F16a,
  `TestLaunchStillDeclaresBeforeTheMove`). E2.1 flips the default to **declare nothing** and
  `ee/register/coordinator.go` registers today's declarer (D535).
- **Paid-command stubs**: a core stub for each name whose code is absent (`report`, `coordinator`),
  which exits non-zero with D522's message — that the command is part of Fugaro Enterprise and this
  build does not include it — rather than cobra's "unknown command", which would mislead a source
  builder and the console. Seen through a seam in E1.1 (`TestStubSaysWhyThroughASeam`, a test-only
  stub name) and for real in E2.1/E2.2 (`TestUntaggedPaidCommandSaysWhy`, G3).
- `TestLateOrDuplicateRegistrationIsRefused`, `TestNoRegistrationLeavesTheRootUnchanged`,
  `TestRegisteredCommandCannotReplaceCoreSubcommandsOrPersistentFlags`,
  `TestLaunchWithoutADeclarerDeclaresNothing`.

The declarer hook's `*Cloud` is the opaque handle of §10: the declarer reads the resolved config,
the launch's `--no-budget-check` switch and the coordinator store through it, so an unexported
`*cloudEnv` never crosses the boundary.

### 5.2 The governance policy hook — `internal/policy/hook.go`

```go
// GovernanceFuncs is the policy half of governance: what Fugaro ships and how a
// layer tightens it. Core default: today's implementation.
type GovernanceFuncs struct {
	Defaults func() Governance                             // today: DefaultGovernance
	Tighten  func(e *Effective, g Governance, src string)  // today: (*Effective).tightenGovernance
}
func SetGovernance(f GovernanceFuncs) error
```

`Defaults` is the core's one reach for the shipped values: `Governance.WithDefaults` (line 264) and
`DefaultGovernanceKeys` (line 311) call it, and `Merge` enters through them, so moving
`DefaultGovernance` and `DefaultTierExpectations` to `ee/governance` needs no other core edit.
`Tighten` is the whole of `tightenGovernance`, `tightenTier` included — `tightenTier` is called by
nothing else.

**Core default after E2.3:** `Defaults` returns the zero `Governance` and `Tighten` is a no-op — an
untagged build reads the block, validates it, warns once and enforces none of it, which is D522.

### 5.3 The runner stage hook — `internal/runner/governance_hook.go`

```go
// StageView is what the runner hands a stage hook: the stage that is starting,
// the governance the run resolved, and the run's own bound on it.
type StageView struct {
	Stage      string
	Governance policy.Governance // the run's effective governance
	RunStage   time.Duration     // the run's own workflows.*.timeouts.stage
}

// StageDecision is what a tripwire decided: whether to stop the stage, and the
// halt it leaves. The halt itself (governanceHalt, the checkpoint report) stays core.
type StageDecision struct {
	Halt   bool
	Reason runstore.HaltReason
	Detail string
}

type StageFuncs struct {
	// Timeout is the stage's own wall-clock ceiling; 0 means none.
	Timeout func(v StageView) time.Duration
	// TimedOut is the decision when that ceiling fires.
	TimedOut func(v StageView) StageDecision
}
func SetStage(f StageFuncs) error
```

**Core default today:** `Timeout` is `(*run).stageTimeout` and `TimedOut` returns
`{Halt: true, Reason: runstore.HaltStageBudget, Detail: "time"}` — the pair the stage loop already
performs at `runner.go:2809–2814`. **Core default after E2.3:** no timeout and no halt; the run
keeps the plumbed halt reasons and the checkpoint report, but nothing raises them from governance.
The stage budget, tokens, ratio, fan-out, review-scope stop and expected-cost tripwires arrive here
as `ee/`'s `Timeout`/`TimedOut` grow (T7.2–T7.3, T8.2), and `r.policy.Governance` they read is
already resolved by the policy hook above.

The ordering the halt path requires is unchanged and stays with the core caller: the decision is
taken on the run goroutine, after the stage's spend and history are recorded; a cancel that landed
first wins, and the checkpoint report is the core plumbing that reads `runstore.Checkpoint`
(D521, D529).

### 5.4 The gateway stage-limit hook and the digest section hook

- **The gateway stage limit (R2's fourth): not in E1.2.** It lands with T7.2, whose first step is
  wiring it. It sits on `gateway.Options` — today `Options` carries `Cap`, `Lease` and
  `OnServiceAnswer`, and a stage limit is a field of the same kind, taking the gateway's own `Stage`
  and `StageReport` (not the runner's `StageView` above, which is a different seam in a different
  package). **Core default: nil**, which is today's behaviour exactly: the gateway enforces the run's
  spend cap and the lease sub-cap (D520 keeps those core) and no per-stage budget.
- **The digest section hook** — `internal/digest/section.go`: `SetSections(extra func(Digest)
  []Section) error`, **core default: none**, so `Markdown` prints the digest it prints today
  (`TestDigestHasNoExtraSectionByDefault`). T8.3's per-tier calibration joins through it and an
  untagged digest omits it (D528).

### 5.5 The rule the seams share

- **Every seam is a package-level value whose default is the core implementation, and `ee/register`
  sets it once.** Each setter refuses a second call from anyone else, so nothing can quietly halve
  the enforcement a tagged binary registered. The edition is set the same way (§7).
- **A seam that switches behaviour *off* is test-only**: the `SetCore()`-style seams live in an
  `export_test.go` beside the hook, never in production code, so no core caller can turn governance
  off in a tagged binary. `export_test.go` is compiled only into that package's own tests.
  Concretely, E1.2's `SetCore()` installs the pair the *untagged build will have* (warn-and-ignore),
  which is how `TestUntaggedRunnerIgnoresGovernanceWithAWarning` and
  `TestDiagnoseReadsACheckpointUntagged` pass before E2.3 exists. E1.2's red step is exactly this:
  today a stage timeout halts regardless, so the test fails until the seam is there.
- **G10, as a review question rather than a test:** can a tagged binary have a hook unset, or set to
  a nil function? `ee/register`'s tests check identity, not non-nil-ness: until E2.3 the policy and
  runner hooks *default* to today's non-nil core implementation, so a test that only asked "is it
  set" would pass while enforcement was silently dropped.

## 6. What an untagged build does (D522)

| Surface | Untagged (core) build | Tagged build | Pinned by |
| --- | --- | --- | --- |
| `governance:` in `fugaro.yaml` or the project layer | decoded, validated, **one warning in the run log**, ignored — it tightens nothing | decoded and effective | E1.2 `TestUntaggedRunnerIgnoresGovernanceWithAWarning` |
| `tier`, `expected_scope` on a task | decoded, the same one warning, ignored | read | E1.2, E2.3 |
| launch / `fugaro validate` / `--tier` warnings | judged by **the job image's edition**, never the launcher's | same | E1.3 `TestGovernanceOnACoreImageWarns`, `TestTaggedImageFromAnUntaggedLauncherDoesNotWarn` |
| `fugaro report`, `fugaro coordinator …` | core stub, non-zero exit with the Enterprise message (nothing can run, so warn-and-continue does not apply) | runs | E1.1 stub, E2.1/E2.2, G3 |
| the launch's declaration | nothing declared (D535) | declared | E2.1 |
| `diagnose`, the digest, the console's run detail | unchanged: they read the record | unchanged | G6, D529 |
| spend caps, the 80% warning, the kill switch, rigor's per-mode budgets | unchanged (D520) | unchanged | E2.3 `TestCapsUnchangedByTheTag`, G7 |
| a licence notice anywhere | **none — deferred with the key work (D523)** | none, for now | G4 |

The warning the untagged runner writes is a **capability statement** — *this build ignores the
block* — and not a licensing warning. It exists because the alternative is silence about a switch a
user set, and because the same sentence is what the launcher-side edition warning says about the
image. Launch-side warnings follow the job image because governance is enforced by the job's
`fugaro`, not the launcher's (R3, F11).

The one thing an untagged build must not do is **refuse**: `governance:`, `tier` and
`expected_scope` are decoded and ignored, never rejected (G2). A core runner given a `governance:`
block that refuses a run would be the strict-decoding failure F9 exists to prevent.

## 7. The edition marker, and its carrier (R3)

**The carrier is a field in the build record.** `internal/imagecheck.Record` — the object the
image build writes to the runs bucket at `RecordKey(slug, workflow)`,
`builds/<slug>/<workflow>/image.json` — gains:

```go
// Edition is the edition of the binary that rendered this record: "ee" when the
// build's render step ran a fugaro built with the ee tag, "" when it cannot say
// (every record written before this field, and every dev build).
Edition string `json:"edition,omitempty"`
```

Three things make this the right carrier rather than an OCI label.

- **The gate already reads exactly this object, for exactly this question.** `checkImageSinceDev`
  (`internal/cli/recipes_skew.go:651`) reads `imagecheck.RecordKey(slug, spec.Workflow)` out of the
  runs bucket and judges the *job image* by `rec.BaseRef` — the base image the build ran FROM, whose
  binary the job runs. The edition belongs beside the field that answers the same question, and the
  gate gains no read, no client and no credential.
- **The job image is usually a derived image, and labels do not survive `FROM`.** An OCI label is
  written into the image *config* by whoever builds that image: a derived image built from the base
  carries the base's labels only if its Dockerfile redeclares them, and a `dockerfile:` workflow's
  image is the repository's own to build. So a label read off the job image would be absent, or the
  repository's. Reading one would also cost a config-blob GET against the registry with the
  launcher's own credentials (`internal/imagecheck/registry.go` does `HEAD` manifests only today),
  at launch time, from a laptop that may hold no credential for the image's host.
- **Precedent, in the same struct.** `SealedBudgetToken` (`internal/imagecheck/record.go:65`) is a
  record field written by the base image's own binary at render time and read by the launch's seal
  gate; `Edition` is the same shape for the same reason. The render step already writes the running
  binary's identity into the record: `internal/cli/image.go:591` sets `FugaroVersion: Version` and
  `TemplateSalt: gcp.TemplateSalt(Version)`, and `Edition` is written on the next line, from the
  binary's own edition.

The OCI labels are still set — `images/base/Dockerfile:309` already writes
`org.opencontainers.image.*`, and E1.4 adds the licence label naming MIT plus the enterprise
licence — they are simply not what the gate judges.

**An image with no record, no `edition` field, or an empty one is *unknown*, and an unknown image
gets no edition warning.** That is today's behaviour, and it is what every image before E1.4, every
dev image and every `--image-source` mirror looks like. Only a record that says `core` against a
configuration that sets `governance:` produces the warning.

### Why `-ldflags -X` cannot flip it

- `-X` sets **one string variable** and nothing else. The edition is not a string variable:
  `internal/cli/edition.go` (E1.3) computes it from *whether `ee/register`'s `init` ran the setter*,
  which is a fact about which files the build compiled — `cmd/fugaro/ee_on.go` under `//go:build ee`
  and nothing else. There is no variable for `-X` to write, so
  `-X github.com/…/internal/cli.Edition=ee` is not merely ineffective, it is a link error; and a
  tagged build has no `core` string to write into either.
- The contrast is the point. `Version` *is* a package-level string
  (`internal/cli/root.go:15`, `var Version = "dev"`), and `images/base/Dockerfile:34` sets it with
  `-X` on purpose. The edition deliberately does not live there: if it did, a build flag could raise
  a core image to `ee` and silence the warning, or — worse — lower a tagged image to `core` and make
  the binary with the code in it deny having it.
- E1.3's `TestEditionHasNoLdflagsRoute` is the source scan that keeps it that way: the edition is
  not a package-level string a `-X` can set, and no build file (a Dockerfile, `.goreleaser.yaml`,
  Cloud Build) passes `-X` for it.
- **The marker is advisory** (R3): nothing stops a hand-built image from claiming `ee`, and nothing
  should — R5 states plainly that key-gating is a licence instrument, not a security boundary. The
  value is written by the image's own binary so that an honest build never disagrees with it, and a
  false answer costs a warning or its absence, never a refusal.

## 8. Which images carry the tag (D524 — task E1.4)

| Image | Tag | Why |
| --- | --- | --- |
| `images/base/Dockerfile` | `-tags ee`, `COPY ee ./ee` | governance runs in the job's own `fugaro` (F11): stage timeouts, the halts, the planned stage budgets are all in the process the job starts |
| `images/history/Dockerfile` | `-tags ee`, `COPY ee ./ee` | the coordinator's sweep runs *from this image* (`deploy/terraform/gcp/modules/installation/coordinator.tf` starts `/usr/local/bin/fugaro coordinator sweep`). Untagged, the sweep would hit the core stub of §6 and the coordinator would stop sweeping, which is the window open item O6 warns a dogfood installation about |
| `images/credentials/Dockerfile` | untagged | it serves the credentials service and runs no coordination, no report and no governance: with no `ee/` code to carry, its licence label stays MIT and true. A tagged image here would publish a compiled form of `ee/` to introduce a capability the service does not have |

Three rules ride along:

- **The tag lands only once the relabel has** (F16c, E3.2): `scripts/notices_test.go`'s
  `TestEETagNeedsTheLabels` refuses a release that sets the tag while a licence label says MIT, and
  E3.2 extends that gate to the image builds, which it does not see today. So base and history
  labels name MIT plus the enterprise licence, and credentials' label names MIT.
- **The build context admits `ee/`**: the root `.dockerignore` lists `go.mod`, `go.sum`, `cmd/`,
  `internal/`, `images/` and `deploy/terraform/` today, so `COPY ee` would fail (F16b); E1.4 adds it
  and tests the context.
- **No key and no secret enters a job image** (R6, G9): no `COPY` and no `ARG` of a key or a secret
  in any of the three Dockerfiles. Under T27 (b) a job needs no key at all, and when enforcement
  ships the entitlement travels in the task as a claim, not as a key.

D525 keeps the tagged images public: anyone may pull them, and the licence governs use.

## 9. Which command tree the reference and the plugin read (D536 — task E1.7)

**The tagged tree**, because one binary ships (T21) and the reference describes that binary.

- The generator is `ee/clidoc` (`go run ./ee/clidoc`): it imports `ee/register` and a new
  *importable* package `internal/clidoc/cliref/` (`Generate` and `Prepare`, split out of
  `internal/clidoc`, which is `package main` and cannot be imported), and it writes
  `website/content/cli-reference.md`.
- `ee/clidoc`'s own test holds the reference current against the tagged tree; the core keeps a
  **subset** check (`internal/clidoc/cliref_test.go`'s `TestCoreCommandsAreInTheReference`,
  `internal/cli/docs_website_cliref_test.go`) so the core's half of the file cannot drift — and
  `internal/clidoc/cliref.go` stops writing the file itself, so the two generators never overwrite
  each other.
- The plugin's flag checks split the same way: `plugin/routing_skill_test.go` and
  `plugin/setup_skill_test.go` build `cli.NewRootCmd()` in core, and the working skill's
  `fugaro report --by …` (`plugin/skills/working/SKILL.md:53`) names flags of a command that the core
  tree knows only as a stub. So the core tests accept a paid command **name** through E1.1's stub
  list, and the paid commands' flags are checked by `ee/plugincheck/skills_test.go` against the
  tagged tree. Without this, the move would turn a passing plugin check red for a command the
  released binary still has (F18).
- The file's banner names the generator that wrote it (`<!-- generated by go run …; do not edit -->`,
  pinned by `website/test/meta.mjs`), so the banner and that pin move together with the generator.
- `internal/cli/docs_mobile_mailbox_plan_test.go` pins
  `func TestCliReferenceIsCurrent(` in `internal/clidoc/cliref_test.go`; that pin is re-pointed to
  `ee/clidoc/cliref_test.go` in E1.7's commit.

## 10. The helper API an `ee/` command needs from `internal/cli`

S1.1's list, made concrete: `internal/cli/api.go` (E1.1) publishes exactly these, and nothing more.
An `ee/` command that needs something outside this list either imports a leaf package that already
exists (`internal/safetext` for terminal-safe text) or adds a helper here, in the open.

| Core helper today | Published in `api.go` | Signature |
| --- | --- | --- |
| `cloudOptions` | `CloudFlags` (opaque) | — |
| `addCloudFlags` (`cloud.go:51`) | `AddCloudFlags` | `func AddCloudFlags(cmd *cobra.Command, f *CloudFlags)` |
| `cloudEnv` | `Cloud` (opaque) | — |
| `openCloud` (`cloud.go:212`) | `OpenCloud` | `func OpenCloud(ctx context.Context, f *CloudFlags) (*Cloud, error)` |
| `openCloudFor` (`cloud.go:225`) | `OpenFromConfig` | `func OpenFromConfig(ctx context.Context, lc *localcfg.Config) (*Cloud, error)` |
| `cloudEnv.lc` (the resolved project config) | `Cloud.Config` | `func (c *Cloud) Config() *localcfg.Config` |
| `cloudEnv.noBudgetCheck` | `Cloud.NoBudgetCheck` | `func (c *Cloud) NoBudgetCheck() bool` |
| `repoSlugOf` (`budget.go:176`), `(*cloudEnv).repoSlug` (`cloud.go:527`) | `Cloud.RepoSlug` | `func (c *Cloud) RepoSlug(ctx context.Context, repo string) (string, error)` |
| `budgetDBFor` (`cloud.go:364`) | `Cloud.BudgetDB` | `func (c *Cloud) BudgetDB(ctx context.Context) (*rtdb.Client, error)` |
| `dbErr` (`budget.go:132`) | `Cloud.DBErr` | `func (c *Cloud) DBErr(err error, write bool) error` |
| `newReportFirestore` (`report.go:349`) | `Cloud.Firestore` | `func (c *Cloud) Firestore(ctx context.Context) (*firestore.Client, error)` |
| `firestoreErr` (`report.go:371`) | `Cloud.FirestoreErr` | `func (c *Cloud) FirestoreErr(err error) error` |
| `userErr` (`cloud.go:196`) | `UserErr` | `func UserErr(format string, args ...any) error` |
| `remote` (`cloud.go:201`) | `RemoteErr` | `func RemoteErr(err error) error` |
| `oneLine` (`safetext.go:18`) | — (import `internal/safetext` directly) | `safetext.OneLine(string) string` |
| `clipRunes` (`budget.go:118`) | `ClipRunes` | `func ClipRunes(s string, n int) string` |

Notes that make this list the right size:

- `Cloud` and `CloudFlags` are **opaque handles**: an `ee/` package cannot see `cloudEnv` or
  `cloudOptions`, so it cannot reach the project selection, the header, the endpoints or the
  backend opener behind them.
- `UserErr` and `RemoteErr` return errors that `ExitCode` (`internal/cli/root.go:43`, already
  exported) classifies as exit 1 and exit 2, so a moved command keeps the CLI's exit codes without
  exporting the classification.
- The moved commands' own error wording stays theirs; only the prefix kinds move.
- `OneLine` stays out of `api.go` on purpose: `safetext.OneLine` is already an importable leaf, and
  a wrapper would be a second name for one function.

## 11. Where this document overrides the plans

| Item | S1.1 / previous text | Here | Why |
| --- | --- | --- | --- |
| The coordinator's path constants | stay in `internal/coordinator/` | new `internal/coordpath/` | the library moves; `internal/cli/project_cmd.go` and three rules tests still name `PathRoot` and friends |
| `reportHistory` | stays in core (`reportcore.go`) | **moves with the command** | it returns `[]report.Rec`; its only caller is `report.go:174` |
| The digest's MIT copy | `Dim`, `ParseDim`, `PeriodKey`, the period keys, `USD` | **plus `USD6`** | `patterns.go` and `render.go` read it six times; the digest does not compile without it |
| The gating model | "a paid command is present and refuses without a key" | **warn-and-continue for fields; a stub for absent commands** (D522) | T27 (b), answered 2026-10-29; the two disagree (F4) |
| The notice | one line on stderr per paid command, one run-log line | **none** | D523, answered NOT the recommendation; deferred with the key work |
| The edition marker's carrier | an OCI label **or** a field in the build record | **the build record's `Edition`** | §7 |
| Hook signatures | named only | fixed in §5 | this document is what E1.1/E1.2 build against |

## 12. What each task lands, and what holds it

Ordering is the plan's (E0.3 → the in-flight gate → E1.1/E1.2/E1.3 → … → E2.x). Two invariants hold
across every commit:

- **G1: the core builds and its tests pass with `ee/` deleted**, and `go test ./internal/eeboundary/
  -count=1` passes with it in place. No task edits a boundary test to admit a route: that is the
  owner's call.
- **The Docs suite holds this document's own citations**: `GOFLAGS=-p=2 go test ./internal/cli/
  -run Docs -count=1`. It runs before and after every docs commit of the slice, and the citations in
  §2–§4 above are the ones to check first when a move commit fails it.

**Release note for the 0.13.0 operators' section (O7, answered 2026-10-29):** the notes carry ONE
line — refresh images to get governance — and it is **not** a required operator step. An
installation that never refreshes its images keeps refusing `governance:`, which is today's
behaviour anyway; the line exists because D524 ships governance in a tagged base image and a reader
should know that is where it arrives. The release notes themselves are E3.3's, not this document's.
