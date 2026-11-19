# Messaging a running run: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md).

**Status: plan only. Nothing is implemented and no task is approved.** Each task needs the owner's explicit go before it is launched. The answers that shaped it, all given 2026-11-18 and all as recommended: any launcher may send (D2940); messages live under `tokens/<slug>/<run>/messages/` (D2941); reviewers never see them (D2942); a message may widen a fix round (D2943); one that arrives after the last coder stage is reported undelivered (D2944); `--now` stops the agent at a quiet point and resumes its session (D2945); `--wrap-up` is a flag the runner enforces (D2946); the command is `fugaro tell` (D2947).

## Conventions every task follows

- `CLAUDE.md`'s commands, hermetic tests (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`), no secret-shaped literal, comments that say why, targeted test runs (`-run`), `gofmt -l .` printing nothing.
- `.claude/rules/terraform.md` for RM6.
- **The no-prompt policy** (`docs/no-prompt-policy/`): `fugaro tell` never waits for delivery and never reads a terminal.
- **A field never changes meaning**: `result.json`'s `messages` block and `runs show --json`'s field are additive; no schema version moves.
- **Plugin skills follow the CLI**: a command a skill names is checked by `plugin/` tests, so RM1 adds the skill line in the same PR.
- Boundaries. *Always*: a red test before the change; the record and the report say what happened to every message. *Ask first*: any IAM change (none is planned: D2941 needs none), any change to the review prompts. *Never*: a message in a review stage's prompt; a run that waits for a message; a live cloud action by an agent (Check 740 is the owner's).

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| A message never reaches a review, first-line review, adversarial or triage stage (D2942) | RM2's `TestMessagesNeverReachAReview` |
| A run that receives no message is byte-for-byte unchanged: the same prompts, the same stage sequence | RM2's `TestNoMessagesNoChange` (golden prompts) |
| The job account cannot write a message (D2941) | RM0's path test (prefix is `token.ObjectPrefix`); Check 740 step 3 live |
| A `--now` interrupt is never recorded as a stage failure, a cancel or a halt | RM4's `TestNowInterruptIsNotAFailure` |
| A cancel or a halt landing during a `--now` wait wins over the message | RM4's `TestCancelWinsOverNow` |
| Text is redacted, cleaned, clipped and fenced with a fresh nonce | RM2's `TestMessageBlockRedactsAndFences` |

## Slices

- **Slice A — messages at stage boundaries** (RM0, RM1, RM2, RM5, RM6): useful alone. A message reaches the next coder stage, and the record, report and `diagnose` show it.
- **Slice B — `--now` and `--wrap-up`** (RM3, RM4): delivery inside a running coder stage.
- **Slice C — documents and the live check** (RM7, RM8).

## Tasks

### RM0: the message format, its store and the record block
- Acceptance: `runmsg.Message` round-trips through `schemas/run-message.schema.json` with strict decoding; `runmsg.Put` writes `tokens/<slug>/<run>/messages/NNNN.msg.json` create-if-absent, taking the next free number by listing and retrying on a collision; `runmsg.Next(ctx, seq)` returns the message numbered `seq` or `(nil, nil)` on a 403 or 404; the 4 KiB and 20-message caps are refused at `Put` and at `Next`; `runstore`'s result carries the additive `messages` block of F13 and `result.schema.json` describes it.
- Files: `internal/runmsg/message.go` (new), `internal/runmsg/store.go` (new), `internal/runmsg/runmsg_test.go` (new), `schemas/run-message.schema.json` (new), `schemas/result.schema.json`, `internal/runstore/runstore.go`.
- Interfaces: produces `runmsg.Message`, `runmsg.Delivery`, `runmsg.Record`, `runmsg.Put`, `runmsg.Next`, `runmsg.Path`; the prefix is `token.ObjectPrefix`, never a second literal.
- Tests: `TestMessageSchemaRoundTrip`, `TestPutTakesNextNumber`, `TestPutCollisionRetries`, `TestNextMissingIsNone`, `TestCapsRefused`, `TestPathUnderTokens`, plus `schemas`' own test.
- Command: `go test ./internal/runmsg/ ./schemas/ ./internal/runstore/ -run 'Message|Put|Next|Caps|Path|Schema' -count=1`
- Red step: `TestPathUnderTokens` fails before `runmsg.Path` exists, and fails if the path is put under `runs/`.
- Depends on: none.
- Commit: `runmsg: the run-message format, its store under tokens/ and the record block (D2941)`
- Reviewer tier: opus with an independent review: the prefix is the authentication (D2941), and `result.json` is a wire format.

### RM1: `fugaro tell`
- Acceptance: `fugaro tell RUN "text" [--now | --wrap-up] [--json]` writes one message through `runmsg.Put` and prints its number; it refuses, at once and with the fix, a run whose record has no `messages.accepting` (F9), a run past finalize, an empty or over-cap text, and a 21st message; `--now` and `--wrap-up` are mutually exclusive; it never waits for delivery; the `working` skill names it in one line.
- Files: `internal/cli/tell.go` (new), `internal/cli/tell_test.go` (new), `internal/cli/root.go`, `plugin/skills/working/SKILL.md`.
- Interfaces: consumes RM0's `runmsg.Put` and `runmsg.Record`; resolves `RUN` the way `cancel` does (`runstore` resolve, full ID or `<slug>/<run-id>`).
- Tests: `TestTellWrites`, `TestTellRefusesOldRunner`, `TestTellRefusesFinished`, `TestTellNoTextFailsAtOnce`, `TestTellJSON`, and `plugin/`'s command test.
- Command: `go test ./internal/cli/ -run 'TestTell' -count=1 && (cd plugin && go test ./... -count=1)`
- Red step: `TestTellWrites` fails before the command is registered.
- Depends on: RM0 (the store it calls).
- Commit: `cli: fugaro tell sends a message to a running run (D2947)`
- Reviewer tier: opus with an independent review: it touches a plugin skill.

### RM2: the runner reads messages and delivers them at stage boundaries
- Acceptance: at bootstrap the runner writes `messages.accepting: true`; on the cancel watch's cadence it reads `runmsg.Next` for the next number; a pending message goes into the next coder stage's prompt (implement, fix, a follow-up's resume) in a fenced, redacted block, and never into a review-class stage; a fix stage that carries messages ends with rule 4's sentence instead of "Touch nothing else" (D2943); every message's fate is recorded, and one still pending at finalize is `undelivered` (D2944); an unparseable or over-cap one is `refused`.
- Files: `internal/runner/messages.go` (new), `internal/runner/messages_test.go` (new), `internal/runner/prompts.go`, `internal/runner/prompts_test.go`, `internal/runner/runner.go`.
- Interfaces: consumes RM0's `runmsg.Next` and `runmsg.Record`; produces the pending queue `(*run).takeMessages(stage)` RM4 uses, and `MessageBlock(msgs, nonce)`.
- Tests: `TestMessageReachesNextFix`, `TestMessageReachesImplement`, `TestMessagesNeverReachAReview`, `TestNoMessagesNoChange`, `TestFixPromptWidensWithMessage`, `TestPendingAtFinalizeIsUndelivered`, `TestMessageBlockRedactsAndFences`, and an end-to-end test that writes through `runmsg.Put` into a `gcpfake` bucket and reads the fix prompt `fakeclaude` received.
- Command: `go test ./internal/runner/ -run 'TestMessage|TestNoMessagesNoChange|TestFixPromptWidens|TestPendingAtFinalize' -count=1`
- Red step: `TestMessageReachesNextFix` fails: the fix prompt today has no message block.
- Depends on: RM0 (the store and the record block).
- Commit: `runner: messages reach the next coder stage, never a review (D2942, D2943, D2944)`
- Reviewer tier: opus with an independent review: it changes agent prompts.

### RM3: the agent's quiet point and an interrupt that is not a failure
- Acceptance: `agent.ParseStream` reports a quiet point (a `tool_result` with no `tool_use` begun after it, or a text-only assistant turn) through a callback; `agent.Claude.Run` accepts an interrupt request that stops the process at the next quiet point, or at once after a bound, and returns a result marked `Interrupted` with the session ID, which is not an error; `fakeclaude` can simulate a long tool call and a resume.
- Files: `internal/agent/stream.go`, `internal/agent/agent.go`, `internal/agent/interrupt_test.go` (new), `internal/agent/fakeclaude/main.go`.
- Interfaces: produces `agent.Request.Interrupt` (a channel or func the runner fires) and `Result.Interrupted`; consumes nothing of this plan.
- Tests: `TestQuietPointAfterToolResult`, `TestNoQuietPointMidTool`, `TestInterruptWaitsForQuietPoint`, `TestInterruptBoundStopsAtOnce`, `TestInterruptedIsNotAnError`.
- Command: `go test ./internal/agent/... -run 'TestQuietPoint|TestNoQuietPoint|TestInterrupt' -count=1`
- Red step: `TestInterruptedIsNotAnError` fails: a stopped process is an error today.
- Depends on: none.
- Commit: `agent: a quiet point in the stream and an interrupt that keeps the session (D2945)`
- Reviewer tier: standard.

### RM4: `--now` and `--wrap-up` in the runner
- Acceptance: a `now` message during a coder stage fires RM3's interrupt; the stage restarts with `--resume` and the message as its prompt, counted against the same stage's time and spend; one during a review waits for the next coder stage; a `wrap_up` message is delivered the same way with the wrap-up instruction, and when that turn ends the coder stage ends as succeeded and the run goes on to its reviews and finalize (D2946); a cancel or halt during the wait wins; the record says `delivered` with `flags` and, after the 10-minute bound, the reason.
- Files: `internal/runner/interrupt.go` (new), `internal/runner/interrupt_test.go` (new), `internal/runner/runner.go`, `internal/runner/prompts.go`.
- Interfaces: consumes RM2's `takeMessages` and RM3's `Request.Interrupt` and `Result.Interrupted`.
- Tests: `TestNowResumesSameSession`, `TestNowDuringReviewWaits`, `TestWrapUpEndsCoderStage`, `TestNowInterruptIsNotAFailure`, `TestCancelWinsOverNow`, `TestNowBoundRecorded`.
- Command: `go test ./internal/runner/ -run 'TestNow|TestWrapUp|TestCancelWinsOverNow' -count=1`
- Red step: `TestNowResumesSameSession` fails: a `now` message today waits for the boundary.
- Depends on: RM2 (it delivers through RM2's queue, and both write `runner.go` and `prompts.go`); RM3 (the interrupt's behaviour, not just its signature: the restart is only correct once the stop keeps the session).
- Commit: `runner: --now resumes the stage with the message, --wrap-up ends it (D2945, D2946)`
- Reviewer tier: opus with an independent review: agent prompts and the stage loop.

### RM5: the report, `diagnose` and `runs show`
- Acceptance: `report.md` has a **Messages** section listing each message's number, sender, flags, fate and stage, and for an undelivered one the exact `fugaro run --pr N "…"` line (D2944); `fugaro diagnose` text and `--json` show the same; `fugaro runs show --json` carries the block and `runs-show.schema.json` says so. Text is quoted through `safetext`.
- Files: `internal/runner/report.go`, `internal/runner/report_test.go`, `internal/cli/diagnose.go`, `internal/cli/runs_show.go`, `schemas/runs-show.schema.json`.
- Interfaces: consumes RM0's `runmsg.Record` only.
- Tests: `TestReportMessagesSection`, `TestReportUndeliveredLine`, `TestDiagnoseMessages`, `TestRunsShowMessages`.
- Command: `go test ./internal/runner/ -run 'TestReportMessages|TestReportUndelivered' -count=1 && go test ./internal/cli/ -run 'TestDiagnoseMessages|TestRunsShowMessages' -count=1`
- Red step: `TestReportMessagesSection` fails: the report has no such section.
- Depends on: RM0 (the record's type: it reads a recorded block, never RM2's behaviour, so it runs beside RM2 on fixture records).
- Commit: `report, diagnose, runs show: what happened to every message`
- Reviewer tier: standard.

### RM6: the lifecycle rule for message objects
- Acceptance: the installation module carries one unconditional rule deleting objects under `tokens/` ending `.msg.json` at 7 days of age; `terraform test` shows it present with the budget enabled and disabled, and the `budget-token` rule unchanged.
- Files: `deploy/terraform/gcp/modules/installation/bucket.tf`, `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`.
- Interfaces: the suffix `.msg.json` is RM0's object name, written in both places; RM0's `TestPathUnderTokens` pins the suffix.
- Tests: the rule's `run` block in `installation.tftest.hcl`.
- Command: `terraform fmt -check -recursive deploy/terraform && (cd deploy/terraform/gcp/roots/installation && terraform init -backend=false -lockfile=readonly && terraform validate && terraform test)`
- Red step: the new `run` block fails before the rule exists.
- Depends on: none (the suffix is fixed by the reconciliation, F11).
- Commit: `terraform: message objects under tokens/ go after 7 days (F11)`
- Reviewer tier: opus with an independent review: Terraform.

### RM7: the documents
- Acceptance: `SECURITY.md` says a launcher can message a running run, that messages live where no job account can write, and why (D2940, D2941); `docs/design/v1.md` gains the delivery rules (reconciliation rules 1–6) and the `fugaro tell` row in the commands table; `docs/design/bucket-iam.md`'s writers table gains the message row; the docs tests pass.
- Files: `SECURITY.md`, `docs/design/v1.md`, `docs/design/bucket-iam.md`.
- Tests: `go test ./internal/cli/ -run Docs -count=1`.
- Command: as above.
- Red step: none (documents); the docs tests are run before and after.
- Depends on: RM1 (the command and flags it names exist), RM4 (the `--now` and `--wrap-up` behaviour it describes).
- Commit: `docs: messaging a running run`
- Reviewer tier: opus with an independent review: `SECURITY.md`.

### RM8: Check 740, the owner's live check
- Acceptance: `scripts/live-checks/check-740-run-messages.sh` and its `_test.sh`, and the Check 740 row in `docs/gcp-live-checklist.md`; the script, run by the owner, (1) launches a small run, sends a plain message and confirms it is `delivered` in the next coder stage; (2) sends a `--now` message during implement and confirms the same session resumed (U2); (3) with the job account's credential (impersonated by the owner, never by an agent) tries to write a message object and confirms 403 (D2941); (4) reads the bucket's lifecycle configuration and confirms the `.msg.json` rule (U4).
- Files: `scripts/live-checks/check-740-run-messages.sh` (new), `scripts/live-checks/check-740-run-messages_test.sh` (new), `docs/gcp-live-checklist.md`.
- Tests: the `_test.sh` (dry run, no cloud).
- Command: `bash scripts/live-checks/check-740-run-messages_test.sh`
- Red step: the `_test.sh` fails before the script exists.
- Depends on: RM1, RM4, RM6 (the script drives the real command against a released runner and the applied rule).
- Commit: `live-checks: Check 740, messaging a running run`
- Reviewer tier: standard.
- Owner-run: after the release and `fugaro update`, the owner runs `bash scripts/live-checks/check-740-run-messages.sh --installation fugaro`. No agent performs any step of it.

## Parallel execution

**Levels.**

| Level | Tasks |
| --- | --- |
| 0 | RM0, RM3, RM6 |
| 1 | RM1, RM2, RM5 |
| 2 | RM4 |
| 3 | RM7, RM8 |

**Edges kept**, each with its reason:

- RM1 → RM0, RM2 → RM0, RM5 → RM0: they call the store or read the record type RM0 defines (kept; see below for why they were not broken).
- RM4 → RM2: delivers through RM2's queue, and both write `runner.go` and `prompts.go`.
- RM4 → RM3: needs the interrupt's behaviour (the stop that keeps the session), not only its signature.
- RM7 → RM1, RM7 → RM4: the documents describe the command and the behaviour as built.
- RM8 → RM1, RM4, RM6: the live check drives all three.

**Edges broken.** None. The decoupling pass is on (`planning.decoupling: true`, rigor `startup`, which decouples aggressively) and was run on every edge. RM1, RM2 and RM5 need only RM0's interface, so a contract `C-RM` could break those three edges. But RM0 is itself only types, a schema and two small store functions (size 2): a contract would be nearly the whole of it, and adding the contract and its integration task lengthens the weighted critical path (RM0 → RM2 → RM4 → RM7: 2 + 3 + 3 + 1 = 9) to 1 + 3 + 3 + 2 + 1 = 10 rather than shortening it. RM5 already runs beside RM2, because it reads the record type and not RM2's behaviour. RM3 was split from RM4 so that the agent-side work runs at level 0.

**Critical path:** RM0 → RM2 → RM4 → RM7 (weight 9). `fugaro plan graph` reports the same path and no break.

**Shared files.** No level has two writers of one file:

| File | Writers (level) |
| --- | --- |
| `internal/runner/runner.go` | RM2 (1), RM4 (2) |
| `internal/runner/prompts.go` | RM2 (1), RM4 (2) |
| `schemas/result.schema.json`, `internal/runstore/runstore.go` | RM0 (0) |
| `internal/cli/root.go`, `plugin/skills/working/SKILL.md` | RM1 (1) |
| `internal/runner/report.go`, `internal/cli/diagnose.go`, `internal/cli/runs_show.go` | RM5 (1) |
| `internal/agent/*` | RM3 (0) |
| `deploy/terraform/gcp/modules/installation/bucket.tf` | RM6 (0) |
| `SECURITY.md`, `docs/design/v1.md`, `docs/design/bucket-iam.md` | RM7 (3) |
| `docs/gcp-live-checklist.md` | RM8 (3) |

**Contract changes anticipated.** None, since no contract is planned. A change to RM0's format after RM1, RM2 or RM5 is launched reaches them by a follow-up run naming `internal/runmsg/message.go` by path.

## Checks the plan takes

- **Check 740** (RM8): the four steps above. The owner runs it after the release; it gates nothing in this plan and is not a release gate unless the owner makes it one.

## After-deploy steps

None. RM6's rule is applied by `fugaro update`, which also rebuilds the job images that carry RM2–RM4 (F16). A run started before the update has no `messages.accepting`, and `fugaro tell` refuses it with the reason (F9). A release note's `### For operators` stays "run `fugaro update`".

## Later, not in this plan

The console's and the mobile app's send buttons, and the orchestrator daemon forwarding a person's ruling, all write through `runmsg.Put` once it exists (spec "Later surfaces").

## Considered and declined

- A contract task for RM0's types (above: no gain).
- An inbox under `runs/` (D2941: forgeable from inside a run).
- Streaming stdin for `--now` (D2945; U1).
- Showing messages to reviewers (D2942).
- An extra coder and review round for a late message (D2944).
