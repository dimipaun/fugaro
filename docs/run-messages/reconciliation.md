# Messaging a running run: reconciliation

Checked against `origin/main` at `0db80aa40` on 2026-11-18. **Where this document disagrees with [spec.md](spec.md), this document wins.**
Decisions **D2940–D2947** are taken; **D2948–D2959** are reserved for this feature. Live check **Check 740** is taken; **741–744** are reserved.

## Assumptions

- **A1.** "A run that is going" means a run whose execution has started and has not reached finalize. A queued run (no execution yet) is out of scope: its task can still be changed by cancelling and launching again.
- **A2.** "The current stage" for `--now` means a **coder** stage (implement, fix, a follow-up's resumed implement). A review stage is never interrupted (D2942 keeps reviewers blind to messages).
- **A3.** The message's text is the person's own words, of the same standing as the launcher's instructions on a follow-up (`docs/design/v1.md` §4.4: "The launcher's own text is always trusted"), and reaches the agent fenced and marked as data from that person, never spliced into the system prompt.
- **A4.** The first slice is the CLI and the runner. The console, the mobile app and the orchestrator daemon send through the same objects later (spec "Later surfaces"), and nothing in this plan builds them.

## What the repository already has

| Spec concept | What the repository has |
| --- | --- |
| A launcher-written object a running run polls for | The cancel marker: `runstore.Store.RequestCancel` writes `runs/<slug>/<run>/cancel`, `runner.WatchCancel` (`internal/runner/budget.go`) polls `Store.CancelRequested` every `CancelPoll` (30 s by default, `internal/cli/cancel.go`) |
| A prefix launchers write and the job only reads | `tokens/` (`token.ObjectPrefix`, `internal/budget/token/object.go`): launchers write it under `gcp.LauncherBucketCondition`; each job account holds `roles/storage.objectViewer` on `tokens/<slug>/` (`google_storage_bucket_iam_member.job_tokens`, `deploy/terraform/gcp/modules/workflow/sa.tf`), a project job on the whole of `tokens/` (`modules/project/sa.tf`) — "so a peer run cannot delete a queued run's token" |
| Trusted person text in an agent prompt | A follow-up's prompt (`internal/followup/prompt.go`): redaction, `cleanText`, a nonce-drawn fence (`followup.NewNonce`), clipping |
| Resuming a Claude Code session | `agent.Args` passes `--resume <id>` (`internal/agent/agent.go`); `agent.ErrNoSession` is the recovered failure |
| A record of who launched | `runstore.Launch.LaunchedBy` (`launch.json`), written from the launching CLI's `RequestedBy`: self-declared, display only |
| A phone-to-host message relay | `fugaro mailbox` (`internal/cli/mailbox.go`, `internal/mailbox`): a MAC-checked mailbox in the RTDB whose `message` kind is delivered to the orchestrator. Not a channel into a running job; a later surface can forward into this feature's objects |

## Findings

- **F1 (Rule → resolved by D2940).** The spec's "trusted senders: the launcher and `followup.trusted`" does not type-check. `followup.trusted` (`config.Followup.Trusted`, `internal/config/config.go`) lists **git-host account IDs**; a message is written to the bucket under a **GCP identity**, and the job has no way to learn which identity wrote an object (no per-object writer metadata; `LaunchedBy` is self-declared). Every launcher can already write every run's `task.json` and `cancel` marker (`docs/design/bucket-iam.md` §2's writers table, H2: "Launchers write all of `runs/`"), and `SECURITY.md` states the model the system rests on: "Everyone who can push to the repository, everyone who can launch a run, and (for follow-up runs) the people listed in `followup.trusted` can steer an agent". A launcher sending a message adds no one to that list.
- **F2 (Gap → resolved by D2941). The spec's own "authenticated" requirement fails if messages live under `runs/`.** `SECURITY.md` ("The metadata server is reachable, by the agent and by every container it starts") means a run's agent can act as its job account, which holds `roles/storage.objectUser` on `runs/<slug>/` — and a project job account on the whole of `runs/` (`docs/design/bucket-iam.md` §2.1). An inbox under `runs/` would let a hostile agent plant a "message from the person" into its own later stages or, on a shared project job, into **another repository's** running run. `tokens/` is read-only for every job account and writable by every launcher: the one prefix that already has exactly this shape.
- **F3 (Wrong → resolved by D2942).** Spec item 6 assumes a reviewer judges the diff against the brief. It does not: `ReviewPrompt` and `reviewBrief` (`internal/runner/prompts.go`) take no task text, so a reviewer never sees the brief and cannot flag a change as "not what was asked". Showing it messages would add an injection path into the one stage whose independence the design protects.
- **F4 (Gap → resolved by D2943).** After implement ends, the only coder stage left is a fix, and `FixPrompt` ends "Touch nothing else: make only the changes these findings ask for" — `CLAUDE.md`'s "a fix round for review findings adds no functionality". A message saying "also do X" delivered there contradicts the prompt it rides in.
- **F5 (Gap → resolved by D2944).** A message can arrive when no coder stage remains (the last review shipped; finalize next), or during finalize itself. The spec says only "never delivered (the run ended first)".
- **F6 (Gap → resolved by D2945).** `agent.Claude.Run` writes the prompt to stdin once (`Stdin: strings.NewReader(req.Prompt)`) and closes it: there is no channel into a running `claude -p`. Interrupting a stage today is the cancel path (`WatchCancel` → context cancel → `procgroup`'s SIGTERM then SIGKILL), which ends the stage as a failure. `--now` therefore needs a new, non-failing stop and a `--resume` of the same session. Stopping in the middle of a tool call loses that call (a test run of many minutes).
- **F7 (Open → resolved by D2946).** "Wrap up" in the spec is open between a flag and ordinary text.
- **F8 (Open → resolved by D2947).** The command's name was left open. `fugaro cancel RUN` (`internal/cli/cancel.go`) is the one top-level command that acts on a running run; the `runs` family (`ls`, `show`, `watch`, `digest`, `stats`, `summary`, `reports`) only reads.
- **F9 (Gap, resolution).** **Version skew.** A CLI with this feature can send to a run whose runner predates it, and the run record carries no runner version (`runstore` writes `version` as the record's format only). Resolution: the runner writes an additive `messages` block into `result.json` at bootstrap with `accepting: true`; `fugaro tell` refuses, printing why, when the record has no such block ("this run's runner does not read messages; it started before Fugaro <release>"). No prompt, no wait (the no-prompt policy).
- **F10 (Gap, resolution).** **Listing.** A job account's conditional grant gives no `storage.objects.list` (`docs/design/bucket-iam.md` §2.3: "a GET of a missing object answers 403"). So the runner never lists: messages are numbered, `tokens/<slug>/<run>/messages/0001.msg.json` onward, and the runner GETs the next number on each poll, treating 403 and 404 alike as "none yet". The CLI (a launcher holds `objectViewer` on the whole bucket) lists the prefix to find the next number and writes with create-if-absent, retrying on a collision, so two senders never overwrite each other.
- **F11 (Gap, resolution).** **Retention.** The bucket's only age rule on `tokens/` matches the suffix `/budget-token` and exists only with the budget enabled (`deploy/terraform/gcp/modules/installation/bucket.tf`); message objects would stay for ever. Resolution: one unconditional lifecycle rule deleting objects under `tokens/` whose names end `.msg.json` at 7 days of age (GCS matches a prefix and a suffix, never a middle segment), shipped in the installation root that `fugaro update` already applies. No operator step, no IAM change. The run's own record keeps its copy (F13).
- **F12 (Gap, resolution).** **Caps and hygiene.** A message is at most 4 KiB of text after cleaning, a run reads at most 20 messages (the CLI refuses the 21st; the runner ignores numbers past 20), and the text goes through the run's redactor and `safetext` cleaning, inside a fence drawn from a fresh nonce (the follow-up prompt's pattern), before the agent sees it.
- **F13 (Gap, resolution).** **The record.** `result.json` gains an additive `messages` block: `accepting`, and per message `seq`, `sent_at`, `sent_by` (the self-declared sender, display only), `flags` (`now`, `wrap_up`), `state` (`delivered` | `undelivered` | `refused`), `stage`, `delivered_at` and `reason`. Additive, so `result.schema.json`'s version is unchanged (`CLAUDE.md`, "A field never changes meaning"). The copy is the runner's own write under `runs/`, which the agent could overwrite: it is an audit trail, and `report.md` is posted from the runner's memory, not re-read from it.
- **F14 (Rule, resolution).** **The no-prompt policy.** `fugaro tell` never waits for delivery and never asks: it writes, prints the number and returns. With no text argument it fails at once; it does not read a terminal. It runs in a coding agent's session as `fugaro run` and `fugaro cancel` do: an agent that can launch a run can already steer one.
- **F15 (Gap, resolution).** **Halts.** A run whose last review is all `needs_owner` halts with `owner_decision` at once (v1.md §4.1). Messages do not change that path in this slice: a ruling sent by message before the halt reaches the next coder stage like any other; one pending at the halt is `undelivered` (D2944).
- **F16 (Rule, resolution).** **Upgrade.** Nothing here needs an operator step: the runner and CLI ship in the release, `fugaro update` rebuilds the job images behind its own confirmation and applies the installation root that carries F11's rule. An installation at the previous version converges by that one command; a run started before the update keeps running without the `messages` block, and `fugaro tell` refuses it (F9).

## Decisions

| # | Question | Recommendation | Answer |
| --- | --- | --- | --- |
| D2940 | Who may message a run (F1)? | Any launcher of the installation: the trust of `fugaro cancel` and `fugaro run --pr`; the sender recorded for display only | **Any launcher**, 2026-11-18 (as recommended). Why: it is the trust model `SECURITY.md` already states; per-launcher would need signed messages and new key infrastructure |
| D2941 | Where do messages live (F2)? | `tokens/<slug>/<run>/messages/NNNN.msg.json`: no IAM change, job accounts read-only | **`tokens/<slug>/<run>/messages/`**, 2026-11-18 (as recommended). Why: the agent can reach the job account's credential; only a job-read-only prefix makes a message unforgeable from inside a run |
| D2942 | Do review stages see messages (F3)? | No: reviewers stay task-blind | **No**, 2026-11-18 (as recommended). Spec item 6 is dropped. Why: reviewers never see the brief today, so nothing can be flagged as off-brief, and the reviewer gains no injection path |
| D2943 | May a message widen a fix round (F4)? | Yes: the fix prompt says the person's message may ask for more than the findings | **Yes**, 2026-11-18 (as recommended). Why: the "fix round adds no functionality" rule governs review findings, not the owner's own word |
| D2944 | A message after the last coder stage (F5)? | Recorded `undelivered`; the report and `diagnose` show it with the `fugaro run --pr N "…"` line to send it | **Report it undelivered**, 2026-11-18 (as recommended). Why: the run's loop and outcome rules stay as they are |
| D2945 | How does `--now` reach a running stage (F6)? | Wait (at most 10 minutes) for the agent to be between tool calls, stop it without failing the stage, resume the session with the message | **Interrupt between tools**, 2026-11-18 (as recommended). Why: uses only what the pinned Claude Code does today; streaming stdin input is unverified (U1) |
| D2946 | Wrap-up (F7)? | `--wrap-up`: delivered like `--now`, telling the agent to commit and stop; the runner then ends the coder stage and goes to review and finalize as usual | **`--wrap-up` flag**, 2026-11-18 (as recommended). Why: deterministic; does not rely on the agent obeying |
| D2947 | The command (F8)? | `fugaro tell RUN "text" [--now \| --wrap-up] [--json]`, top level beside `cancel` | **`fugaro tell`**, 2026-11-18 (as recommended) |

### The rules the answers make

1. **Delivery points.** A pending message is put into the **next coder stage's prompt**: the implement prompt if the run has not reached it, the next fix prompt, or the resume prompt of a `--now` interrupt. A review, a first-line review, an adversarial review or a triage stage never carries one.
2. **`--now` (D2945).** On seeing a `now` message during a coder stage, the runner waits for a **quiet point** in the agent's `stream-json` output — a `tool_result` has arrived and no new `tool_use` has started, or a text-only assistant turn — then stops the agent with a reason the stage treats as an interrupt (not a failure, not a cancel), and starts the same stage again with `--resume <session>` and the message as the prompt. After 10 minutes with no quiet point, it stops at once and says so in the record (`reason: "no quiet point in 10m"`). Time, spend and tokens keep counting against the same stage and run caps. A `now` message seen during a review waits for the next coder stage.
3. **`--wrap-up` (D2946).** Delivered as rule 2, with the prompt telling the agent to commit what it has, write `pr.md`, and stop. When that resumed turn ends, the runner ends the coder stage as succeeded and the run continues to its review rounds and finalize as usual. Further fix rounds run as configured.
4. **The fix prompt (D2943).** When a fix stage carries messages, `FixPrompt`'s closing line is replaced by one that says the findings above and the person's messages below are both the round's work, and the messages may ask for more than the findings.
5. **Undelivered (D2944).** At finalize, every message still pending is recorded `undelivered`; `report.md` lists it with the exact `fugaro run --pr N "…"` line, and `diagnose` shows the same.
6. **Refused.** A message the runner cannot parse, over the cap, or past number 20 is recorded `refused` with the reason, and never reaches the agent.

## Spec items, as amended

| Spec item | Status |
| --- | --- |
| 1 Inbound only | Kept |
| 2 A command | Kept, named `fugaro tell` (D2947) |
| 3 Trusted senders | Amended: any launcher (D2940) |
| 4 Authenticated | Amended: unforgeable from inside a run by living under `tokens/` (D2941); the sender's name is display only |
| 5 Delivery | Kept, with rule 1 and `--now` as rule 2 (D2945) |
| 6 The reviewer sees them | **Dropped** (D2942) |
| 7 Recorded | Kept: F13, rule 5 |
| 8 Bounded | Kept: F12 |
| 9 Wrap up | A flag (D2946) |

## Open tensions

None.

## Open items

None.

## UNVERIFIED

- **U1.** Whether Claude Code 2.1.283 accepts user messages on stdin mid-run under `-p --input-format stream-json`. Not used (D2945); it would make `--now` lossless. Settled by a probe against the pinned binary; anyone can run it.
- **U2.** That a session stopped by SIGINT/SIGTERM at a quiet point resumes cleanly with `--resume` on 2.1.283, keeping the turns before it. The quiet-point rule is designed so the session file ends on a complete turn. Settled by RM3's test against `fakeclaude` for the runner's side and by **Check 740** against the real binary.
- **U3.** That the quiet point can be read from the `stream-json` events `agent.ParseStream` already parses (assistant `tool_use`, user `tool_result`). Settled by RM3.
- **U4.** That a GCS lifecycle rule with `matches_prefix = ["tokens/"]` and `matches_suffix = [".msg.json"]` selects only the message objects and never a `budget-token`. Settled by RM6's `terraform test` and Check 740's last step.
