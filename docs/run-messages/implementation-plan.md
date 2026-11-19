# Messaging a running run: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md).

**Status: plan only. Nothing is implemented and no task is approved.** Each task needs the owner's explicit go before it is launched. The answers that shaped it, all given 2026-11-18 and all as recommended: any launcher may send (D2940); messages live under `tokens/<slug>/<run>/messages/` (D2941); reviewers never see them (D2942); a message may widen a fix round (D2943); one that arrives after the last coder stage is reported undelivered (D2944); `--now` yields the agent at a quiet point and resumes its session (D2945); `--wrap-up` is a flag the runner enforces (D2946); the command is `fugaro tell` (D2947).

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
| A message reaches only implement, fix and a follow-up's implement or resume (rule 1, D2942) | RM2's `TestMessagesOnlyInAllowedStages`, walking every stage name |
| A run that receives no message is unchanged: the same prompts, the same stage sequence, the same report | RM2's `TestNoMessagesNoChange`; RM5's `TestReportUnchangedWithoutMessages` |
| The job account cannot write a message (D2941) | RM0's `TestPathUnderTokens` (prefix is `token.ObjectPrefix`); Check 740 step 3, live |
| A gap in the numbering never stalls delivery (F10) | RM0's `TestPutRefusesGap`; RM2's `TestGapDoesNotStall` |
| A message written after the last poll is not lost silently (F18) | RM2's `TestFinalReadAtFinalize`; RM10's `TestDiagnoseListsUnreadMessages` |
| A yield is never recorded as a failure, a cancel or a halt, and its spend counts | RM4's `TestNowYieldIsNotAFailure`, `TestYieldSpendCounted` |
| A cancel or a halt during a `--now` wait wins | RM4's `TestCancelWinsOverNow` |
| Text is redacted, cleaned, clipped and fenced with a fresh nonce | RM2's `TestMessageBlockRedactsAndFences` |
| A copy-paste line never runs pasted text, and never comes from `result.json` | RM0's `TestShellQuote`; RM5's `TestReportUndeliveredLine`; RM10's `TestDiagnoseLineFromObjectsNotRecord` |

## Slices

- **Slice A — messages at stage boundaries** (RM0, RM1, RM2, RM5, RM6, RM9, RM10): useful alone. A message reaches the next coder stage, and the record, report and `diagnose` show it.
- **Slice B — `--now` and `--wrap-up`** (RM3, RM4): delivery inside a running coder stage.
- **Slice C — documents and the live check** (RM7, RM8).

## Tasks

### RM0: the message format, its store and the record block
- Acceptance: `runmsg.Message` round-trips through `schemas/run-message.schema.json` with strict decoding; `runmsg.Put` writes `tokens/<slug>/<run>/messages/NNNN.msg.json` create-if-absent at the highest listed number plus one, retries on a collision, and refuses when the listing shows a gap; `runmsg.Next(ctx, seq)` returns the message numbered `seq` or `(nil, nil)` on a 403 or 404; the 4 KiB and 20-message caps are refused at `Put` and never read past at `Next`; `runmsg.ShellQuote` single-quotes text for a shell; `runstore`'s result carries the additive `messages` block of F13, with no text, and `result.schema.json` describes it.
- Files: `internal/runmsg/message.go` (new), `internal/runmsg/store.go` (new), `internal/runmsg/runmsg_test.go` (new), `schemas/run-message.schema.json` (new), `schemas/result.schema.json`, `internal/runstore/runstore.go`. Six files, kept as one task: the record block and the format are one shape.
- Interfaces: produces `runmsg.Message`, `runmsg.Delivery`, `runmsg.Record`, `runmsg.Put`, `runmsg.Next`, `runmsg.Path`, `runmsg.Suffix` (`.msg.json`), `runmsg.ShellQuote`; the prefix is `token.ObjectPrefix`, never a second literal.
- Tests: `TestMessageSchemaRoundTrip`, `TestPutTakesNextNumber`, `TestPutCollisionRetries`, `TestPutRefusesGap`, `TestNextMissingIsNone`, `TestCapsRefused`, `TestPathUnderTokens`, `TestShellQuote`, plus `schemas`' own test.
- Command: `go test ./internal/runmsg/ ./schemas/ ./internal/runstore/ -run 'Message|Put|Next|Caps|Path|ShellQuote|Schema' -count=1`
- Red step: `TestPathUnderTokens` fails before `runmsg.Path` exists, and fails if the path is put under `runs/`.
- Depends on: none.
- Commit: `runmsg: the run-message format, its store under tokens/ and the record block (D2941)`
- Reviewer tier: opus with an independent review: the prefix is the authentication (D2941), and `result.json` is a wire format.

### RM1: `fugaro tell`
- Acceptance: `fugaro tell RUN "text" [--now | --wrap-up] [--json]` checks `ActionLaunch` with `requireRunAccess` as `cancel` checks `ActionCancel` (F17); writes one message through `runmsg.Put` and prints its number; refuses at once, with the fix, a run whose record has no `messages.accepting` (F9), a run past finalize, a repository with no `tokens/` read grant (F21, with the `fugaro update --repo` fix), an empty or over-cap text and a 21st message; `--now` and `--wrap-up` are mutually exclusive; it never waits for delivery; the CLI reference and the JSON fixture cover it.
- Files: `internal/cli/tell.go` (new), `internal/cli/tell_test.go` (new), `internal/cli/root.go`, `internal/cli/report_json_fixtures_test.go`, `website/content/cli-reference.md` (generated).
- Interfaces: consumes RM0's `runmsg.Put` and `runmsg.Record`; resolves `RUN` the way `cancel` does (full ID or `<slug>/<run-id>`).
- Tests: `TestTellWrites`, `TestTellChecksLaunchAccess`, `TestTellRefusesOldRunner`, `TestTellRefusesFinished`, `TestTellRefusesNoTokenGrant`, `TestTellNoTextFailsAtOnce`, `TestTellJSON`, `TestJSONFixtureCoverage`, `TestCliReferenceNamesEveryCommandAndFlag`.
- Command: `go test ./internal/cli/ -run 'TestTell|TestJSONFixtureCoverage|TestCliReferenceNamesEveryCommandAndFlag' -count=1`
- Red step: `TestTellWrites` fails before the command is registered.
- Depends on: RM0 (the store it calls).
- Commit: `cli: fugaro tell sends a message to a running run (D2947)`
- Reviewer tier: opus with an independent review: an access gate and a write into the runs bucket.

### RM9: the `working` skill names `fugaro tell`
- Acceptance: `plugin/skills/working/SKILL.md` says in one line when to use `fugaro tell` (a run going the wrong way, before cancelling) and that it never waits; `plugin/`'s command test passes.
- Files: `plugin/skills/working/SKILL.md`.
- Tests: `plugin/`'s command and flag test.
- Command: `(cd plugin && go test ./... -count=1)`
- Red step: none (a document); the test is run before and after.
- Depends on: RM1 (the plugin test checks the command exists).
- Commit: `plugin: the working skill names fugaro tell`
- Reviewer tier: opus with an independent review: a plugin skill.

### RM2: the runner reads messages and delivers them at stage boundaries
- Acceptance: at bootstrap the runner writes `messages.accepting: true`; on the cancel watch's cadence it reads `runmsg.Next` for the next number, looking ahead three numbers on a miss and recording a skipped one `missing` (F10); a pending message goes into the next prompt on rule 1's allow-list, in a fenced, redacted block built with `agent.Redact` and `safetext.MultiLine`, and never into any other stage; a fix stage that carries messages ends with rule 4's sentence instead of "Touch nothing else" (D2943); at finalize it reads once more and records every message still pending `undelivered` (D2944, F18); an unparseable or over-cap one is `refused`; it never reads delivery state back from the record (F13).
- Files: `internal/runner/messages.go` (new), `internal/runner/messages_test.go` (new), `internal/runner/prompts.go`, `internal/runner/prompts_test.go`, `internal/runner/runner.go`.
- Interfaces: consumes RM0's `runmsg.Next` and `runmsg.Record`; produces the pending queue `(*run).takeMessages(stage)` RM4 uses, and `MessageBlock(msgs, nonce)`.
- Tests: `TestMessageReachesNextFix`, `TestMessageReachesImplement`, `TestMessagesOnlyInAllowedStages`, `TestNoMessagesNoChange`, `TestFixPromptWidensWithMessage`, `TestPendingAtFinalizeIsUndelivered`, `TestFinalReadAtFinalize`, `TestGapDoesNotStall`, `TestMessageBlockRedactsAndFences`, and `TestMessageEndToEnd`, which writes through `runmsg.Put` into a `gcpfake` bucket and reads the fix prompt `fakeclaude` received.
- Command: `go test ./internal/runner/ -run 'TestMessage|TestNoMessagesNoChange|TestFixPromptWidens|TestPendingAtFinalize|TestFinalReadAtFinalize|TestGapDoesNotStall' -count=1`
- Red step: `TestMessageReachesNextFix` fails: the fix prompt today has no message block.
- Depends on: RM0 (the store and the record block).
- Commit: `runner: messages reach the next coder stage and nothing else (D2942, D2943, D2944)`
- Reviewer tier: opus with an independent review: it changes agent prompts.

### RM3: the agent's quiet point and a yield that is not a failure
- Acceptance: a new decoder of the `stream-json` events reports a quiet point per rule 2 (every main-thread `tool_use` id answered by its `tool_result`, sub-agent events with a `parent_tool_use_id` ignored, an assistant message ending in `tool_use` never quiet); `agent.Claude.Run` takes a yield request that stops the process at the next quiet point, or at once after a bound, and returns a result marked `Yielded` with the session ID and the last usage seen, which is not an error; `fakeclaude` can simulate a long tool call, parallel tool calls, a sub-agent and a resume. `Run`'s nil-Env guard and its process-group stop are unchanged.
- Files: `internal/agent/quiet.go` (new), `internal/agent/quiet_test.go` (new), `internal/agent/agent.go`, `internal/agent/fakeclaude/main.go`.
- Interfaces: produces `agent.Request.Yield` and `Result.Yielded`; consumes nothing of this plan.
- Tests: `TestQuietPointAfterToolResult`, `TestNoQuietPointMidTool`, `TestParallelToolsCountedByID`, `TestSubAgentEventsIgnored`, `TestTextThenToolUseNotQuiet`, `TestYieldWaitsForQuietPoint`, `TestYieldBoundStopsAtOnce`, `TestYieldedIsNotAnError`.
- Command: `go test ./internal/agent/... -run 'TestQuietPoint|TestNoQuietPoint|TestParallelTools|TestSubAgentEvents|TestTextThenToolUse|TestYield' -count=1`
- Red step: `TestYieldedIsNotAnError` fails: a stopped process is an error today.
- Depends on: none.
- Commit: `agent: a quiet point in the stream and a yield that keeps the session (D2945)`
- Reviewer tier: opus with an independent review: `agent.Claude.Run` is the agent's environment (the nil-Env guard, the process-group stop).

### RM4: `--now` and `--wrap-up` in the runner
- Acceptance: a `now` message during a coder stage fires RM3's yield; the stage restarts with `--resume` and the message as its prompt; the yielded segment's spend and tokens are counted (the gateway's stage figure, else the last usage seen), and the resumed segment's `--max-budget-usd` is the stage's remainder; one during a review waits for the next coder stage; a `wrap_up` message is delivered the same way with the wrap-up instruction, and when that turn ends the coder stage ends as succeeded and the run goes on to its reviews and finalize (D2946); a cancel or halt during the wait wins; the record says `delivered` with `flags` and, after the 10-minute bound, the reason.
- Files: `internal/runner/yield.go` (new), `internal/runner/yield_test.go` (new), `internal/runner/runner.go`, `internal/runner/prompts.go`.
- Interfaces: consumes RM2's `takeMessages` and RM3's `Request.Yield` and `Result.Yielded`.
- Tests: `TestNowResumesSameSession`, `TestNowDuringReviewWaits`, `TestWrapUpEndsCoderStage`, `TestNowYieldIsNotAFailure`, `TestYieldSpendCounted`, `TestResumedBudgetIsRemainder`, `TestCancelWinsOverNow`, `TestNowBoundRecorded`.
- Command: `go test ./internal/runner/ -run 'TestNow|TestWrapUp|TestYield|TestResumedBudget|TestCancelWinsOverNow' -count=1`
- Red step: `TestNowResumesSameSession` fails: a `now` message today waits for the boundary.
- Depends on: RM2 (it delivers through RM2's queue, and both write `runner.go` and `prompts.go`); RM3 (the yield's behaviour, not only its signature: the restart is correct only once the stop keeps the session).
- Commit: `runner: --now resumes the stage with the message, --wrap-up ends it (D2945, D2946)`
- Reviewer tier: opus with an independent review: agent prompts, the stage loop and spend accounting.

### RM5: the report's Messages section
- Acceptance: `report.md` has a **Messages** section listing each message's number, sender, flags, fate and stage, quoting each message's text from the runner's memory through `safetext`, and for an undelivered one the line `fugaro run --pr N '…'` built with `runmsg.ShellQuote`, or the text alone when the run opened no pull request (F19); a run with no messages has no section and an unchanged report.
- Files: `internal/runner/report.go`, `internal/runner/report_test.go`.
- Interfaces: consumes RM0's `runmsg.Record` and `runmsg.ShellQuote`; the texts are handed in by the caller, so the task runs on fixtures beside RM2.
- Tests: `TestReportMessagesSection`, `TestReportUndeliveredLine`, `TestReportNoPRNoLine`, `TestReportUnchangedWithoutMessages`.
- Command: `go test ./internal/runner/ -run 'TestReportMessages|TestReportUndelivered|TestReportNoPR|TestReportUnchanged' -count=1`
- Red step: `TestReportMessagesSection` fails: the report has no such section.
- Depends on: RM0 (the record's type and the quoting).
- Commit: `report: what happened to every message, and the line to send an undelivered one`
- Reviewer tier: opus with an independent review: launcher text and a copy-paste line in a comment posted on the pull request.

### RM10: `diagnose` and `runs show`
- Acceptance: `fugaro diagnose` text and `--json` show every message's fate from the record, list `tokens/<slug>/<run>/messages/` and show any message the record never mentions as "never read" (F18), and build an undelivered message's line only from the message object's text (never from `result.json`, F19), saying "text expired" once it is gone; `fugaro runs show --json` carries the record's block and `runs-show.schema.json` says so.
- Files: `internal/cli/diagnose.go`, `internal/cli/diagnose_test.go`, `internal/cli/runs_show.go`, `internal/cli/runs_show_test.go`, `schemas/runs-show.schema.json`.
- Interfaces: consumes RM0's `runmsg.Record`, `runmsg.Path` and `runmsg.ShellQuote`.
- Tests: `TestDiagnoseMessages`, `TestDiagnoseListsUnreadMessages`, `TestDiagnoseLineFromObjectsNotRecord`, `TestDiagnoseTextExpired`, `TestRunsShowMessages`.
- Command: `go test ./internal/cli/ -run 'TestDiagnoseMessages|TestDiagnoseListsUnread|TestDiagnoseLineFromObjects|TestDiagnoseTextExpired|TestRunsShowMessages' -count=1`
- Red step: `TestDiagnoseMessages` fails: `diagnose` shows no messages.
- Depends on: RM0.
- Commit: `diagnose, runs show: every message and its fate`
- Reviewer tier: standard.

### RM6: the lifecycle rule for message objects
- Acceptance: the installation module carries one unconditional rule deleting objects under `tokens/` ending `.msg.json` at 7 days of age; `terraform test` shows it present with the budget enabled and disabled and the `budget-token` rule unchanged; a Go test pins `bucket.tf`'s suffix to `runmsg.Suffix`; the golden plans are regenerated; `project/sa.tf`'s comment no longer says everything under `tokens/` is ciphertext (F2).
- Files: `deploy/terraform/gcp/modules/installation/bucket.tf`, `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`, `deploy/terraform/gcp/modules/project/sa.tf` (comment only), `internal/infra/msglifecycle_test.go` (new), and `internal/infra/tf/testdata/golden/installation*.plan.json` (regenerated, never hand-edited).
- Interfaces: consumes RM0's `runmsg.Suffix`.
- Tests: the rule's `run` block in `installation.tftest.hcl`; `TestMessageLifecycleSuffixMatchesRunmsg`; the golden-plan tests, which the `terraform`-tagged run covers.
- Command: `export TF_DATA_DIR=$(mktemp -d) && terraform fmt -check -recursive deploy/terraform && (cd deploy/terraform/gcp/roots/installation && terraform init -backend=false -lockfile=readonly && terraform validate && terraform test) && go test ./internal/infra/ -run TestMessageLifecycleSuffix -count=1 && go test -tags terraform ./internal/infra/... -count=1 && tflint --recursive && sh deploy/terraform/scan.sh`
- Red step: the new `run` block fails before the rule exists; `TestMessageLifecycleSuffixMatchesRunmsg` fails before it.
- Depends on: RM0 (the suffix constant the pinning test imports).
- Commit: `terraform: message objects under tokens/ go after 7 days (F11)`
- Reviewer tier: opus with an independent review: Terraform.

### RM7: the documents
- Acceptance: `SECURITY.md` and `website/content/security.md` (byte for byte) say a launcher with the repository's launch role can message a running run, that messages live where no job account can write, that on a shared project job every repository's agent can read them, and what a hostile agent can still do (F20); `docs/design/v1.md` gains the delivery rules (reconciliation rules 1–6) and the `fugaro tell` row in the commands table; `docs/design/bucket-iam.md`'s writers table and its `tokens/` sentence, and `docs/design/budget-token-channel.md` §7.2, say messages live under `tokens/` too (F2); the docs tests and the website checks pass.
- Files: `SECURITY.md`, `website/content/security.md`, `docs/design/v1.md`, `docs/design/bucket-iam.md`, `docs/design/budget-token-channel.md`.
- Tests: `go test ./internal/cli/ -run Docs -count=1`; `(cd website && npm run check)`.
- Command: as above.
- Red step: none (documents); the tests are run before and after.
- Depends on: RM1 (the command and its access rule), RM4 (the `--now` and `--wrap-up` behaviour), RM5 and RM10 (rule 5 as the report and `diagnose` show it), RM6 (the lifecycle row).
- Commit: `docs: messaging a running run`
- Reviewer tier: opus with an independent review: `SECURITY.md`.

### RM8: Check 740, the owner's live check
- Acceptance: `scripts/live-checks/check-740-run-messages.sh` and its `_test.sh`, and the Check 740 row in `docs/gcp-live-checklist.md`. The script, run by the owner against an installation the owner names (`--installation <name>`; the script refuses none by name, and the owner picks a scratch one), (1) launches a small run, sends a plain message and confirms it is `delivered` in the next coder stage; (2) sends a `--now` message during implement and confirms the same session resumed (settles U2 and U3); (3) creates a throwaway service account with the job account's bucket bindings copied, as `check340.sh` does, tries to write a message object as it and confirms 403, then deletes the account (D2941); (4) reads the bucket's lifecycle configuration and confirms the `.msg.json` rule (U4).
- Files: `scripts/live-checks/check-740-run-messages.sh` (new), `scripts/live-checks/check-740-run-messages_test.sh` (new), `docs/gcp-live-checklist.md`.
- Tests: the `_test.sh` (dry run, no cloud).
- Command: `bash scripts/live-checks/check-740-run-messages_test.sh`
- Red step: the `_test.sh` fails before the script exists.
- Depends on: RM1, RM4, RM6 (the script drives the real command against a released runner and the applied rule).
- Commit: `live-checks: Check 740, messaging a running run`
- Reviewer tier: opus with an independent review: it creates and deletes a service account and copies IAM bindings.
- Owner-run: after the release and `fugaro update`, the owner runs `bash scripts/live-checks/check-740-run-messages.sh --installation <scratch installation>`. No agent performs any step of it.

## Parallel execution

**Levels.**

| Level | Tasks |
| --- | --- |
| 0 | RM0, RM3 |
| 1 | RM1, RM2, RM5, RM6, RM10 |
| 2 | RM4, RM9 |
| 3 | RM7, RM8 |

**Edges kept**, each with its reason:

- RM1 → RM0, RM2 → RM0, RM5 → RM0, RM6 → RM0, RM10 → RM0: they call the store, read the record type, or pin the suffix RM0 defines (kept; see below for why they were not broken).
- RM9 → RM1: the plugin test checks that the command a skill names exists.
- RM4 → RM2: delivers through RM2's queue, and both write `runner.go` and `prompts.go`.
- RM4 → RM3: needs the yield's behaviour, not only its signature.
- RM7 → RM1, RM4, RM5, RM6, RM10: the documents describe what those tasks built.
- RM8 → RM1, RM4, RM6: the live check drives all three.

**Edges broken.** None. The decoupling pass is on (`planning.decoupling: true`, rigor `startup`, which decouples aggressively) and was run on every edge. RM1, RM2, RM5, RM6 and RM10 need only RM0's interface, so a contract `C-RM` could break those edges. But RM0 is itself only types, a schema and two small store functions (size 2): a contract would be nearly the whole of it, and the contract plus its integration task lengthens the weighted critical path (RM0 → RM2 → RM4 → RM7: 2 + 3 + 3 + 1 = 9) to 1 + 3 + 3 + 2 + 1 = 10 rather than shortening it. RM5 and RM10 already run beside RM2, because they read the record type and not RM2's behaviour. RM3 was split from RM4 so the agent-side work runs at level 0.

**Critical path:** RM0 → RM2 → RM4 → RM7 (weight 9). `fugaro plan graph` reports the same path and no break.

**Shared files.** No level has two writers of one file:

| File | Writers (level) |
| --- | --- |
| `internal/runner/runner.go`, `internal/runner/prompts.go` | RM2 (1), RM4 (2) |
| `internal/runner/report.go` | RM5 (1) |
| `schemas/result.schema.json`, `internal/runstore/runstore.go` | RM0 (0) |
| `internal/cli/root.go`, `internal/cli/report_json_fixtures_test.go`, `website/content/cli-reference.md` | RM1 (1) |
| `internal/cli/diagnose.go`, `internal/cli/runs_show.go`, `schemas/runs-show.schema.json` | RM10 (1) |
| `plugin/skills/working/SKILL.md` | RM9 (2) |
| `internal/agent/*` | RM3 (0) |
| `deploy/terraform/gcp/modules/installation/bucket.tf`, `deploy/terraform/gcp/modules/project/sa.tf`, `internal/infra/tf/testdata/golden/*` | RM6 (1) |
| `SECURITY.md`, `website/content/security.md`, `docs/design/v1.md`, `docs/design/bucket-iam.md`, `docs/design/budget-token-channel.md` | RM7 (3) |
| `docs/gcp-live-checklist.md` | RM8 (3) |

**Contract changes anticipated.** None, since no contract is planned. A change to RM0's format after a dependent is launched reaches it by a follow-up run naming `internal/runmsg/message.go` by path.

## Checks the plan takes

- **Check 740** (RM8): the four steps above. The owner runs it after the release; it gates nothing in this plan and is not a release gate unless the owner makes it one.

## After-deploy steps

None. RM6's rule is applied by `fugaro update`, which also rebuilds the job images that carry RM2–RM4 (F16). A run started before the update has no `messages.accepting`, and `fugaro tell` refuses it with the reason (F9); a repository root never re-applied lacks the token read, which `doctor` reports and `tell` refuses (F21). A release note's `### For operators` stays "run `fugaro update`".

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
