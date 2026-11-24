# Messaging a running run: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md).

**Status: plan only. Nothing is implemented and no task is approved.** Each task needs the owner's explicit go before it is launched. The answers that shaped it, all given 2026-11-18 and all as recommended: any launcher may send (D2940); messages live under `tokens/<slug>/<run>/messages/` (D2941); reviewers never see them (D2942); a message may widen a fix round (D2943); one that arrives after the last coder stage is reported undelivered (D2944); `--now` yields the agent at a quiet point and resumes its session (D2945); `--wrap-up` is a flag the runner enforces (D2946); the command is `fugaro tell` (D2947); and the values of O1 (15-minute wrap-up, 10-minute quiet-point wait, 4 KiB and 20 messages, 7 days).

## Conventions every task follows

- `CLAUDE.md`'s commands, hermetic tests (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`), no secret-shaped literal, comments that say why, targeted test runs (`-run`), `gofmt -l .` printing nothing.
- `.claude/rules/terraform.md` for RM6: `TF_DATA_DIR` outside the tree, golden plans regenerated with `scripts/gen-golden-plans.sh` and never edited by hand, `go test -tags terraform ./internal/infra/...`, `tflint`, `sh deploy/terraform/scan.sh`.
- **The no-prompt policy** (`docs/no-prompt-policy/`): `fugaro tell` never waits for delivery and never reads a terminal.
- **A field never changes meaning**: `result.json`'s `messages` block and `runs show --json`'s field are additive; no schema version moves.
- **Plugin skills follow the CLI**: RM9 adds the skill line once the command exists.
- **The CLI reference is generated**: a new command or flag regenerates `website/content/cli-reference.md` with `go run ./ee/clidoc` (`TestCliReferenceNamesEveryCommandAndFlag`), and a new `--json` command gets a fixture or a stated reason (`TestJSONFixtureCoverage`).
- Boundaries. *Always*: a red test before the change; the record and the report say what happened to every message. *Ask first*: any IAM change (none is planned: D2941 needs none), any change to a review prompt (none is planned). *Never*: a message in a stage outside rule 1's allow-list; a run that waits for a message; a copy-paste command built from `result.json`; a live cloud action by an agent (Check 740 is the owner's).

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| A message reaches only a first run's implement, a follow-up's implement and a fix (rule 1, D2942) | RM2's `TestMessageCallSites` and `TestRebaseStageGetsNone` |
| A run that receives no message is unchanged: the same prompts, the same stage sequence, the same report | RM2's `TestNoMessagesNoChange`; RM5's `TestReportUnchangedWithoutMessages` |
| The job account cannot write a message (D2941) | RM0's `TestPathUnderTokens` (prefix is `token.ObjectPrefix`); Check 740 step 3, live |
| A gap in the numbering never stalls delivery (F10) | RM0's `TestPutRefusesGap`; RM2's `TestGapDoesNotStall` |
| A message written after the last poll is not lost silently (F18) | RM2's `TestFinalReadAtFinalize`; RM10's `TestDiagnoseListsUnreadMessages` |
| A yield is never recorded as a failure, a cancel or a halt, and its spend counts | RM4's `TestNowYieldIsNotAFailure`, `TestYieldSpendCounted` |
| A cancel or a halt during a `--now` wait wins | RM4's `TestCancelWinsOverNow` |
| Text is redacted, cleaned, clipped and fenced with a fresh nonce | RM2's `TestMessageBlockRedactsAndFences` |
| A copy-paste line never runs pasted text, names its repository, puts the text after `--`, and takes nothing from `runs/` | RM5's `TestReportUndeliveredLine`, `TestReportFenceLongerThanText`; RM10's `TestDiagnoseLineFromObjectsAndHost` |
| A resume never resets the stage's deadline, and a wrap-up is bounded | RM4's `TestResumeKeepsDeadline`, `TestWrapUpBounded` |

## Slices

- **Slice A — messages at stage boundaries** (RM0, RM1, RM2, RM5, RM6, RM10): a message reaches the next coder stage, and the record and `diagnose` show it. The report's Messages section is built here and wired into the endings by RM4.
- **Slice B — `--now`, `--wrap-up` and the report's wiring** (RM3, RM4, RM9): delivery inside a running coder stage, and the section on the pull request. RM1 to RM4 ship in one release; until RM4 lands, `tell` refuses the flags because the runner does not list them (F25).
- **Slice C — documents and the live check** (RM7, RM8).

## Tasks

### RM0: the message format, its store and the record block
- Acceptance: `runmsg.Message` round-trips through `schemas/run-message.schema.json` with strict decoding; `runmsg.Put` writes `tokens/<slug>/<run>/messages/NNNN.msg.json` create-if-absent at the highest listed number plus one, retries on a collision, and refuses when the listing shows a gap; `runmsg.Next(ctx, seq)` returns the message numbered `seq` or `(nil, nil)` on a 403 or 404; the 4 KiB and 20-message caps are refused at `Put` and never read past at `Next`; a message carries a `version`, and the record block carries `accepting`, `format` and `flags` (F24, F25); the slug and run segments are checked with `token.ValidSegment`; `runstore`'s result carries the additive `messages` block of F13, with no text, and `result.schema.json` describes it.
- Files: `internal/runmsg/message.go` (new), `internal/runmsg/store.go` (new), `internal/runmsg/runmsg_test.go` (new), `schemas/run-message.schema.json` (new), `schemas/result.schema.json`, `schemas/schemas_test.go` (its fully populated record gains the block, `TestResultSchemaAcceptsRunnerRecords`), `internal/runstore/runstore.go`. Seven files, kept as one task: the record block, its schema and its schema test are one shape, and splitting them leaves a red schema test between two tasks.
- Interfaces: produces `runmsg.Message`, `runmsg.Delivery`, `runmsg.Record`, `runmsg.Put`, `runmsg.Next`, `runmsg.Path`, `runmsg.Suffix` (`.msg.json`); the prefix is `token.ObjectPrefix`, never a second literal.
- Tests: `TestMessageSchemaRoundTrip`, `TestPutTakesNextNumber`, `TestPutCollisionRetries`, `TestPutRefusesGap`, `TestNextMissingIsNone`, `TestCapsRefused`, `TestPathUnderTokens`, plus `TestResultSchemaAcceptsRunnerRecords`.
- Command: `go test ./internal/runmsg/ ./schemas/ ./internal/runstore/ -run 'Message|Put|Next|Caps|Path|Schema' -count=1`
- Red step: `TestPathUnderTokens` fails before `runmsg.Path` exists, and fails if the path is put under `runs/`.
- Depends on: none.
- Commit: `runmsg: the run-message format, its store under tokens/ and the record block (D2941)`
- Reviewer tier: opus with an independent review: the prefix is the authentication (D2941), and `result.json` is a wire format.

### RM1: `fugaro tell`
- Acceptance: `fugaro tell RUN "text" [--now | --wrap-up] [--json]` checks `ActionLaunch` with `requireRunAccess` as `cancel` checks `ActionCancel` (F17); writes one message through `runmsg.Put` and prints its number; refuses at once, with the fix, a run whose record has no `messages.accepting` (F9), a run past finalize, a repository whose readable bucket policy shows no `tokens/` read grant (F21, with the `fugaro update --repo` fix; when the policy cannot be read it sends and says the grant was not checked), an empty or over-cap text and a 21st message; `--now` and `--wrap-up` are mutually exclusive and refused when the run's `messages.flags` does not list them (F25); a run with `accepting: false` (F24) is refused; it never waits for delivery and never reads stdin; the CLI reference, the JSON fixture, the reporter census and the closed-stdin harness cover it.
- Files: `internal/cli/tell.go` (new), `internal/cli/tell_test.go` (new), `internal/cli/root.go`, `internal/cli/report_json_fixtures_test.go`, `internal/cli/report_exempt.go` (or the reporter list: `TestEveryCommandIsOnTheReporterOrExempt`), `internal/cli/noprompt_harness_test.go` (a `closedStdinFixtures` entry: `TestEveryCommandRunsWithClosedStdin`), `website/content/cli-reference.md` (generated with `go run ./ee/clidoc`). Seven files: each of the last four is a census a new command must join, and leaving one out turns `main` red.
- Interfaces: consumes RM0's `runmsg.Put` and `runmsg.Record`; resolves `RUN` the way `cancel` does (full ID or `<slug>/<run-id>`).
- Tests: `TestTellWrites`, `TestTellChecksLaunchAccess`, `TestTellRefusesOldRunner`, `TestTellRefusesFinished`, `TestTellRefusesNoTokenGrant`, `TestTellNoTextFailsAtOnce`, `TestTellPolicyUnreadableSends`, `TestTellFlagsExclusive`, `TestTellFlagsRefusedUnlisted`, `TestTellOverCapText`, `TestTellTwentyFirst`, `TestTellNotAccepting`, `TestTellJSON`, `TestJSONFixtureCoverage`, `TestCliReferenceNamesEveryCommandAndFlag`, `TestEveryCommandIsOnTheReporterOrExempt`, `TestEveryCommandRunsWithClosedStdin`.
- Command: `go test ./internal/cli/ -run 'TestTell|TestJSONFixtureCoverage|TestCliReferenceNamesEveryCommandAndFlag|TestEveryCommandIsOnTheReporterOrExempt|TestEveryCommandRunsWithClosedStdin' -count=1 && go test ./ee/clidoc/ -run TestCliReferenceIsCurrent -count=1`
- Red step: `TestTellWrites` fails before the command is registered.
- Depends on: RM0 (the store it calls).
- Commit: `cli: fugaro tell sends a message to a running run (D2947)`
- Reviewer tier: opus with an independent review: an access gate and a write into the runs bucket.

### RM9: the `working` skill names `fugaro tell`
- Acceptance: `plugin/skills/working/SKILL.md` says in one line when to use `fugaro tell` (a run going the wrong way, before cancelling), that it never waits, and that its `--now` means "at the next quiet point", unlike `cancel --now`; `tell` joins the line "Never launch, cancel or follow up without the user's go-ahead"; `plugin/`'s command test passes.
- Files: `plugin/skills/working/SKILL.md`.
- Tests: `plugin/`'s command and flag test.
- Command: `(cd plugin && go test ./... -count=1)`
- Red step: none (a document); the test is run before and after.
- Depends on: RM1 (the plugin test checks the command exists); RM4 (the skill describes `--now` and `--wrap-up`, which only RM4 makes true).
- Commit: `plugin: the working skill names fugaro tell`
- Reviewer tier: opus with an independent review: a plugin skill.

### RM2: the runner reads messages and delivers them at stage boundaries
- Acceptance: at bootstrap the runner writes `messages.accepting: true` with `format` and `flags: []` only on a run whose recipe has a coder stage, and `accepting: false` otherwise (F24); on the cancel watch's cadence it reads `runmsg.Next` for the next number, looking ahead three numbers on a miss and recording a skipped one `missing` (F10); a pending message goes into the next prompt built at rule 1's three call sites (a first run's implement, a follow-up's implement, a fix), in a fenced, redacted block built with `agent.Redact` and `safetext.MultiLine`, and no other stage makes the call, deny by default; a fix stage that carries messages ends with rule 4's sentence instead of "Touch nothing else" (D2943); at finalize it reads once more and records every message still pending `undelivered` (D2944, F18); an unparseable or over-cap one is `refused`; it never reads delivery state back from the record (F13).
- Files: `internal/runner/messages.go` (new), `internal/runner/messages_test.go` (new), `internal/runner/prompts.go`, `internal/runner/prompts_test.go`, `internal/runner/runner.go`.
- Interfaces: consumes RM0's `runmsg.Next` and `runmsg.Record`; produces the pending queue `(*run).takeMessages(stage)` RM4 uses, and `MessageBlock(msgs, nonce)`.
- Tests: `TestMessageReachesNextFix`, `TestMessageReachesImplement`, `TestMessageCallSites` (the three sites take messages), `TestRebaseStageGetsNone` (the rebase conflict stage, also named `"implement"`, carries none), `TestNotAcceptingWithoutCoderStage`, `TestNewerFormatRefused`, `TestNoMessagesNoChange`, `TestFixPromptWidensWithMessage`, `TestPendingAtFinalizeIsUndelivered`, `TestFinalReadAtFinalize`, `TestGapDoesNotStall`, `TestMessageBlockRedactsAndFences`, and `TestMessageEndToEnd`, which writes through `runmsg.Put` into a `gcpfake` bucket and reads the fix prompt `fakeclaude` received.
- Command: `go test ./internal/runner/ -run 'TestMessage|TestRebaseStageGetsNone|TestNotAcceptingWithoutCoderStage|TestNewerFormatRefused|TestNoMessagesNoChange|TestFixPromptWidens|TestPendingAtFinalize|TestFinalReadAtFinalize|TestGapDoesNotStall' -count=1`
- Red step: `TestMessageReachesNextFix` fails: the fix prompt today has no message block.
- Depends on: RM0 (the store and the record block).
- Commit: `runner: messages reach the next coder stage and nothing else (D2942, D2943, D2944)`
- Reviewer tier: opus with an independent review: it changes agent prompts.

### RM3: the agent's quiet point and a yield that is not a failure
- Acceptance: a new decoder of the `stream-json` events reports a quiet point per rule 2 as amended (every main-thread `tool_use` id answered by its `tool_result`, sub-agent events with a `parent_tool_use_id` ignored), from a stream in which one assistant message may arrive as one event per content block; `agent.Claude.Run` takes a yield request that stops the process at the next quiet point, or at once after a bound, and returns a result marked `Yielded` with the session ID and the last usage seen, which is not an error; `fakeclaude` can simulate a long tool call, parallel tool calls, a sub-agent and a resume. `Run`'s nil-Env guard and its process-group stop are unchanged.
- Files: `internal/agent/quiet.go` (new), `internal/agent/quiet_test.go` (new), `internal/agent/agent.go`, `internal/agent/fakeclaude/main.go`.
- Interfaces: produces `agent.Request.Yield` and `Result.Yielded`; consumes nothing of this plan.
- Tests: `TestQuietPointAfterToolResult`, `TestNoQuietPointMidTool`, `TestParallelToolsCountedByID`, `TestSubAgentEventsIgnored`, `TestQuietAfterEveryToolAnswered`, `TestPerBlockEventsHandled`, `TestYieldWaitsForQuietPoint`, `TestYieldBoundStopsAtOnce`, `TestYieldedIsNotAnError`.
- Command: `go test ./internal/agent/... -run 'TestQuietPoint|TestNoQuietPoint|TestParallelTools|TestSubAgentEvents|TestQuietAfterEvery|TestPerBlockEvents|TestYield' -count=1`
- Red step: `TestYieldedIsNotAnError` first fails to compile (`Request.Yield` and `Result.Yielded` do not exist), then, with the fields stubbed, fails because a stopped process is an error today.
- Depends on: none.
- Commit: `agent: a quiet point in the stream and a yield that keeps the session (D2945)`
- Reviewer tier: opus with an independent review: `agent.Claude.Run` is the agent's environment (the nil-Env guard, the process-group stop).

### RM4: `--now` and `--wrap-up` in the runner
- Acceptance: a `now` message during a coder stage fires RM3's yield; the stage restarts with `--resume` and the message as its prompt; the yielded segment's spend and tokens are counted (the gateway's stage figure, else the last usage seen), and the resumed segment's `--max-budget-usd` is the stage's remainder; one during a review waits for the next coder stage; a `wrap_up` message is delivered the same way with the wrap-up instruction, and when that turn ends the coder stage ends as succeeded and the run goes on to its reviews and finalize (D2946); a resume keeps the stage's original deadline; a wrap-up segment is bounded at 15 minutes (rule 3); the yield and resume go through `r.stage`'s existing in-stage resume, charged as its transient-error attempts are and never across accounts (A7b); after a forced stop whose resume fails, the stage starts again in a fresh session with the message and an instruction to read `git log` and the tree first, and the record says so (a first run's implement and a fix have no such fallback today); a `--now` message that resumes a fix carries rule 4's widening line; at every ending (finalize, halt, cancel, infra error) the runner reads once more and records what is pending; the resumed prompt says background tasks were stopped (F23); a cancel or halt during the wait wins; the record says `delivered` with `flags` and, after the 10-minute bound, the reason. It also wires RM5's Messages section into every report site except the artifact path (`runner.go`'s finalize, `storeUnposted` and `storeEnded` in `followup.go`): the call hands the section the texts and the pull-request number from the runner's memory; the run then lists `now` and `wrap_up` in `messages.flags` (F25).
- Files: `internal/runner/yield.go` (new), `internal/runner/yield_test.go` (new), `internal/runner/runner.go`, `internal/runner/prompts.go`, `internal/runner/followup.go`.
- Interfaces: consumes RM2's `takeMessages`, RM3's `Request.Yield` and `Result.Yielded`, and RM5's section builder.
- Tests: `TestNowResumesSameSession`, `TestNowDuringReviewWaits`, `TestWrapUpEndsCoderStage`, `TestNowYieldIsNotAFailure`, `TestYieldSpendCounted`, `TestResumedBudgetIsRemainder`, `TestResumeKeepsDeadline`, `TestWrapUpBounded`, `TestForcedStopStartsFresh`, `TestYieldNeverCrossesAccounts`, `TestNowInFixWidens`, `TestEndingsReadOnceMore`, `TestHaltReportCarriesMessages`, `TestFlagsListedAfterRM4`, `TestCancelWinsOverNow`, `TestNowBoundRecorded`, `TestFinalizeReportCarriesMessages`.
- Command: `go test ./internal/runner/ -run 'TestNow|TestWrapUp|TestYield|TestResumedBudget|TestResumeKeepsDeadline|TestForcedStop|TestCancelWinsOverNow|TestFinalizeReportCarries|TestEndingsReadOnceMore|TestHaltReportCarries|TestFlagsListed' -count=1`
- Red step: `TestNowResumesSameSession` fails: a `now` message today waits for the boundary.
- Depends on: RM2 (it delivers through RM2's queue, and both write `runner.go` and `prompts.go`); RM3 (the yield's behaviour, not only its signature: the restart is correct only once the stop keeps the session); RM5 (it wires RM5's section into the report sites in `runner.go` and `followup.go`, which RM5 does not touch, so the two never write one file at one level).
- Commit: `runner: --now resumes the stage with the message, --wrap-up ends it (D2945, D2946)`
- Reviewer tier: opus with an independent review: agent prompts, the stage loop and spend accounting.

### RM5: the report's Messages section
- Acceptance: `report.md` has a **Messages** section listing each message's number, sender, flags, fate and stage, quoting each message's text from the runner's memory through `safetext`, and for an undelivered one the line `fugaro run --pr N --repo <owner/name> -- '…'` built with `shellword.Quote` from what the caller hands in (the runner's memory), or the text alone when the run opened no pull request (F19); each text sits in a code fence one backtick longer than its longest backtick run; a run with no messages has no section and an unchanged report. The section is a builder the caller invokes; wiring it into finalize is RM4's.
- Files: `internal/runner/report.go`, `internal/runner/report_test.go`.
- Interfaces: consumes RM0's `runmsg.Record` and `internal/shellword`; produces the section builder RM4 calls; the texts and the number are handed in by the caller, so the task runs on fixtures beside RM2.
- Tests: `TestReportMessagesSection`, `TestReportUndeliveredLine`, `TestReportNoPRNoLine`, `TestReportFenceLongerThanText`, `TestReportTextStartingWithDash`, `TestReportUnchangedWithoutMessages`.
- Command: `go test ./internal/runner/ -run 'TestReportMessages|TestReportUndelivered|TestReportNoPR|TestReportFence|TestReportTextStarting|TestReportUnchanged' -count=1`
- Red step: `TestReportMessagesSection` fails: the report has no such section.
- Depends on: RM0 (the record's type).
- Commit: `report: what happened to every message, and the line to send an undelivered one`
- Reviewer tier: opus with an independent review: launcher text and a copy-paste line in a comment posted on the pull request.

### RM10: `diagnose` and `runs show`
- Acceptance: `fugaro diagnose` text and `--json` show every message's fate labelled "as the run recorded them" and cross-checked against the message objects (F20), list `tokens/<slug>/<run>/messages/` and show any message the record never mentions as "never read" (F18), and build an undelivered message's line from the message object's text and the open pull request `gitprov`'s `PullRequestByBranch` reports for `fugaro/<run-id>` (it returns closed and merged ones too, so `diagnose` keeps only an open one), with `--repo` and `--` (F19), never from anything under `runs/`, printing the line without a number when the host cannot be asked and "text expired" once the object is gone; `fugaro runs show` carries the block in its text output and in `--json`, and `runs-show.schema.json` says so.
- Files: `internal/cli/diagnose.go`, `internal/cli/diagnose_test.go`, `internal/cli/runs_show.go`, `internal/cli/runs_show_test.go`, `schemas/runs-show.schema.json`.
- Interfaces: consumes RM0's `runmsg.Record` and `runmsg.Path`, and `internal/shellword`.
- Tests: `TestDiagnoseMessages`, `TestDiagnoseListsUnreadMessages`, `TestDiagnoseLineFromObjectsAndHost`, `TestDiagnoseNoHostNoNumber`, `TestDiagnoseTextExpired`, `TestRunsShowMessages`, `TestRunsShowJSONMatchesTheSchema`.
- Command: `go test ./internal/cli/ -run 'TestDiagnoseMessages|TestDiagnoseListsUnread|TestDiagnoseLineFromObjects|TestDiagnoseNoHost|TestDiagnoseTextExpired|TestRunsShowMessages|TestRunsShowJSONMatchesTheSchema' -count=1`
- Red step: `TestDiagnoseMessages` fails: `diagnose` shows no messages.
- Depends on: RM0.
- Commit: `diagnose, runs show: every message and its fate`
- Reviewer tier: opus with an independent review: it builds a copy-paste command line and reasons about what a run can forge.

### RM6: the lifecycle rule for message objects
- Acceptance: the installation module carries one unconditional rule deleting objects under `tokens/` ending `.msg.json` at an age taken from `var.bucket_lifecycle` (default 7 days), and `installation.tftest.hcl`'s exact rule-count asserts move by one; the release notes in progress (`docs/releases/v0.27.0.md` today, or the next one) say under `### For operators` that `fugaro update` will show a lifecycle-rule change on the runs bucket; `terraform test` shows it present with the budget enabled and disabled and the `budget-token` rule unchanged; a Go test pins `bucket.tf`'s suffix to `runmsg.Suffix`; the golden plans are regenerated; `project/sa.tf`'s comment and `token.PutObject`'s comment (`internal/budget/token/object.go`) no longer say everything under `tokens/` is ciphertext (F2).
- Files: `deploy/terraform/gcp/modules/installation/bucket.tf`, `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`, `deploy/terraform/gcp/modules/project/sa.tf` (comment only), `internal/budget/token/object.go` (comment only), `internal/infra/msglifecycle_test.go` (new), `deploy/terraform/gcp/modules/installation/variables.tf`, the release notes file, and `internal/infra/tf/testdata/golden/installation*.plan.json` (regenerated, never hand-edited). More than five files: four are one-line comments, counts or notes that the rule change forces, and the golden plans are generated.
- Interfaces: consumes RM0's `runmsg.Suffix`.
- Tests: the rule's `run` block in `installation.tftest.hcl`; `TestMessageLifecycleSuffixMatchesRunmsg`; the golden-plan tests, which the `terraform`-tagged run covers.
- Command: `export TF_DATA_DIR=$(mktemp -d) && terraform fmt -check -recursive deploy/terraform && (cd deploy/terraform/gcp/roots/installation && terraform init -backend=false -lockfile=readonly && terraform validate && terraform test) && go test ./internal/infra/ -run TestMessageLifecycleSuffix -count=1 && go test -tags terraform ./internal/infra/... -count=1 && tflint --chdir=deploy/terraform/gcp --init && tflint --chdir=deploy/terraform/gcp --recursive && sh deploy/terraform/scan.sh`
- Red step: the new `run` block fails before the rule exists; `TestMessageLifecycleSuffixMatchesRunmsg` fails before it.
- Depends on: RM0 (the suffix constant the pinning test imports).
- Commit: `terraform: message objects under tokens/ go after 7 days (F11)`
- Reviewer tier: opus with an independent review: Terraform.

### RM7: the documents
- Acceptance: `SECURITY.md` says a launcher can message a running run (the role check is the CLI's and advisory, F22), that messages live where no job account can write, that on a shared project job every repository's agent can read them, that the resident orchestrator's account writes them and viewers read them, that a hostile agent can suppress delivery but not forge a message (F20), and that text stays readable about 14 days through versioning (F11); `website/content/security.md` is regenerated from it with `go run ./internal/clidoc`, never edited; `docs/design/v1.md` gains the delivery rules (reconciliation rules 1–6) and the `fugaro tell` row in the commands table, saying its `--now` waits for a quiet point unlike `cancel --now`; `docs/bucket-iam/spec.md`'s writers table and its `tokens/` sentence, and `docs/budget-token-channel/spec.md` §7.2 and §7.4, say plaintext messages live under `tokens/` too (F2); the docs tests and the website checks pass.
- Files: `SECURITY.md`, `website/content/security.md` (generated), `docs/design/v1.md`, `docs/bucket-iam/spec.md`, `docs/budget-token-channel/spec.md`.
- Tests: `go run ./internal/clidoc` (regenerates `website/content/security.md`), then `go test ./internal/cli/ -run 'Docs|TestWebsiteSecurityPageIsByteIdenticalToSecurityMd' -count=1`; `(cd website && npm run check)`.
- Command: as above.
- Red step: none (documents); the tests are run before and after.
- Depends on: RM1 (the command and its access rule), RM4 (the `--now` and `--wrap-up` behaviour), RM5 and RM10 (rule 5 as the report and `diagnose` show it), RM6 (the lifecycle row).
- Commit: `docs: messaging a running run`
- Reviewer tier: opus with an independent review: `SECURITY.md`.

### RM8: Check 740, the owner's live check
- Acceptance: `scripts/live-checks/check-740-run-messages.sh` and its `_test.sh`, and the Check 740 row in `docs/gcp-live-checklist.md`. The script, run by the owner against a scratch installation the owner names (`--installation <name>`; it refuses `belong`, the owner's own development installation, as `check340.sh` does), (1) launches a small run, sends a plain message and confirms it is `delivered` in the next coder stage; (2) sends a `--now` message during implement and confirms the same session resumed (settles U2 and U3); (3) asks the IAM Policy Troubleshooter whether the **real** job account of a workflow and, where one exists, of a project job holds `storage.objects.create` on a `tokens/<slug>/<run>/messages/` object, and confirms it is denied (D2941) — a throwaway account given only `objectViewer` would prove nothing, since the role cannot write by definition; (4) reads the bucket's lifecycle configuration and confirms the `.msg.json` rule (U4).
- Files: `scripts/live-checks/check-740-run-messages.sh` (new), `scripts/live-checks/check-740-run-messages_test.sh` (new), `scripts/live_checks_test.go` (the `liveCheckScripts` entry, so CI runs the `_test.sh`), `internal/cli/docs_check740_test.go` (new: the checklist row's pin), `docs/gcp-live-checklist.md`.
- Tests: the `_test.sh` (dry run, no cloud) through `scripts/live_checks_test.go`; the docs pin.
- Command: `go test ./scripts/ -run TestLiveCheckScripts -count=1 && go test ./internal/cli/ -run TestDocsCheck740 -count=1`
- Red step: the `_test.sh` fails before the script exists.
- Depends on: RM1, RM4, RM6 (the script drives the real command against a released runner and the applied rule).
- Commit: `live-checks: Check 740, messaging a running run`
- Reviewer tier: opus with an independent review: it reads IAM on the real job accounts.
- Owner-run: after the release and `fugaro update`, the owner runs `bash scripts/live-checks/check-740-run-messages.sh --installation <scratch installation>`. No agent performs any step of it.

## Parallel execution

**Levels.**

| Level | Tasks |
| --- | --- |
| 0 | RM0, RM3 |
| 1 | RM1, RM2, RM5, RM6, RM10 |
| 2 | RM4 |
| 3 | RM7, RM8, RM9 |

**Edges kept**, each with its reason:

- RM1 → RM0, RM2 → RM0, RM5 → RM0, RM6 → RM0, RM10 → RM0: they call the store, read the record type, or pin the suffix RM0 defines (kept; see below for why they were not broken).
- RM9 → RM1: the plugin test checks that the command a skill names exists.
- RM9 → RM4: the skill describes `--now` and `--wrap-up`, which only RM4 makes true.
- RM4 → RM2: delivers through RM2's queue, and both write `runner.go` and `prompts.go`.
- RM4 → RM3: needs the yield's behaviour, not only its signature.
- RM4 → RM5: it wires RM5's section into finalize, in `runner.go`, which RM5 leaves alone.
- RM7 → RM1, RM4, RM5, RM6, RM10: the documents describe what those tasks built.
- RM8 → RM1, RM4, RM6: the live check drives all three.

**Edges broken.** None. The decoupling pass is on (`planning.decoupling: true`, rigor `startup`, which decouples aggressively) and was run on every edge. RM1, RM2, RM5, RM6 and RM10 need only RM0's interface, so a contract `C-RM` could break those edges. But RM0 is itself only types, a schema and two small store functions (size 2): a contract would be nearly the whole of it, and the contract plus its integration task lengthens the weighted critical path (RM0 → RM2 → RM4 → RM7: 2 + 3 + 3 + 1 = 9) to 1 + 3 + 3 + 2 + 1 = 10 rather than shortening it. RM5 and RM10 already run beside RM2, because they read the record type and not RM2's behaviour. RM3 was split from RM4 so the agent-side work runs at level 0.

**Critical path:** RM0 → RM2 → RM4 → RM7 (weight 9). `fugaro plan graph` reports the same path and no break.

**Shared files.** No level has two writers of one file:

| File | Writers (level) |
| --- | --- |
| `internal/runner/runner.go`, `internal/runner/prompts.go` | RM2 (1), RM4 (2) |
| `internal/runner/followup.go` | RM4 (2) |
| `internal/runner/report.go` | RM5 (1) |
| `schemas/result.schema.json`, `internal/runstore/runstore.go` | RM0 (0) |
| `internal/cli/root.go`, `internal/cli/report_json_fixtures_test.go`, `website/content/cli-reference.md` | RM1 (1) |
| `internal/cli/diagnose.go`, `internal/cli/runs_show.go`, `schemas/runs-show.schema.json` | RM10 (1) |
| `plugin/skills/working/SKILL.md` | RM9 (3) |
| `internal/agent/*` | RM3 (0) |
| `deploy/terraform/gcp/modules/installation/bucket.tf`, `deploy/terraform/gcp/modules/project/sa.tf`, `internal/infra/tf/testdata/golden/*` | RM6 (1) |
| `SECURITY.md`, `website/content/security.md`, `docs/design/v1.md`, `docs/bucket-iam/spec.md`, `docs/budget-token-channel/spec.md` | RM7 (3) |
| `docs/gcp-live-checklist.md`, `scripts/live_checks_test.go` | RM8 (3) |
| `deploy/terraform/gcp/modules/installation/variables.tf`, the release notes file | RM6 (1) |

**Contract changes anticipated.** None, since no contract is planned. A change to RM0's format after a dependent is launched reaches it by a follow-up run naming `internal/runmsg/message.go` by path.

## Checks the plan takes

- **Check 740** (RM8): the four steps above. The owner runs it after the release; it gates nothing in this plan and is not a release gate unless the owner makes it one.

## After-deploy steps

None. RM6's rule is applied by `fugaro update`, which also rebuilds the job images that carry RM2–RM4 (F16). A run started before the update has no `messages.accepting`, and `fugaro tell` refuses it with the reason (F9); a repository root never re-applied lacks the token read, which `doctor` reports and `tell` refuses (F21). A release note's `### For operators` stays "run `fugaro update`", plus the one line RM6 adds: the update will show a lifecycle-rule change on the runs bucket.

## The proposed launch

Ten agent tasks (RM0–RM7, RM9, RM10) as ten runs in four waves, plus RM8 whose script is an agent task and whose run is the owner's. Each run's spend is bounded by the repository's own caps (the gateway enforces them; no brief sets one), so the worst case is ten times the per-run cap, with the usual follow-up rounds on top. None is launched until the owner says so.

## Later, not in this plan

The console's and the mobile app's send buttons, and the orchestrator daemon forwarding a person's ruling, all write through `runmsg.Put` once it exists (spec "Later surfaces").

## Considered and declined

- A contract task for RM0's types (above: no gain).
- An inbox under `runs/` (D2941: forgeable from inside a run).
- Streaming stdin for `--now` (D2945; U1).
- Showing messages to reviewers (D2942).
- An extra coder and review round for a late message (D2944).
- A new access action for `tell` (F17: it is a launch's steering, so `ActionLaunch`).
- Exporting `followup`'s `cleanText` (RM2 uses `agent.Redact` and `safetext.MultiLine` directly).
