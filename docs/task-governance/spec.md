# Fugaro Task Governance Spec

(Owner's spec, 2026-10-22 night. Source of the verbatim text: the owner's message; the full text is reproduced below.)

## Problem
Two recent incidents hit the $10 per-task limit where typical tasks cost $0.10-$0.20: (1) Runaway implementation: a few-line website change ran all day; the agent followed very strict instructions too literally and kept trying to satisfy them, with no point where it had to stop and justify the cost. (2) Runaway review: implementation took about 12 model calls, review 800+; the review diff was taken against main and included the whole integration branch, so Claude fanned out to around 10 sub-agents to review code the task never touched. The diff bug is already fixed; governance should have caught it after a few dollars, not at the $10 cap. Goal: catch waste early, make cost overruns explicit, and give the orchestrator, or a human, a decision point before money is burned.

## 1. Review-scope sanity check
- Before review starts, record the review input size (files, lines added, lines removed) and compare with what the implementation phase actually edited.
- If the review set includes files the agent never touched, or is much larger than the implementation diff, pause and send a checkpoint report (section 4) instead of reviewing.
- Log the mismatch, so scope bugs show up in telemetry rather than on the invoice.

## 2. Phase budgets
Implementation: set by the orchestrator from the task estimate (3). Review: scales with diff size and implementation cost, e.g. max(floor, k x implementation calls), k around 2-3 to start. Fix-up after review: a fraction of implementation budget. Budgets are tracked in model calls, tokens, dollars and wall-clock time; whichever limit is hit first triggers. Ratio guardrail: if review calls are more than N x implementation calls (start N = 5), pause and escalate. The global per-task hard cap ($10) stays as a last-resort backstop.

## 3. Orchestrator expectations
When dispatching, the orchestrator attaches an expectation envelope: difficulty tier (trivial, small, medium, large), expected cost and duration, expected scope (roughly which files or areas), rationale (one line). Starting tier defaults (to be tuned): Trivial (copy/text tweak, few lines) $0.25 / 10 min; Small (single-file feature or bug fix) $1 / 30 min; Medium (multi-file change with tests) $3 / 1.5 h; Large (cross-cutting refactor) $8 / 4 h.

## 4. Checkpoints and escalation
At 100% of expected budget (cost or time) the job must pause and send a checkpoint report to the orchestrator: what has been done, why it is over the estimate, what it plans next and the estimated remaining cost, any instructions it finds hard to satisfy or contradictory. The orchestrator chooses: Continue (new budget extension, e.g. +50%), Redirect (corrective instructions), Stop (keep partial work on a branch), Escalate to human (unsure, or after a second overrun). After a second overrun, escalation to a human is required. Optional soft check-in at 50% for medium and large tasks.

## 5. Tripwires (deterministic, regardless of budget)
Sub-agent fan-out cap (max concurrent and total sub-agents per phase, e.g. 3 for review on trivial and small tiers); loop detection (same test failing more than X times in a row, same file region edited back and forth, repeated near-identical prompts); scope drift (edits outside expected scope trigger a checkpoint); idle spin (many model calls with no file changes or test progress). Any tripwire triggers a checkpoint report rather than a silent kill.

## 5a. Instruction, spend and retry safeguards
Instruction pre-flight: before implementation the agent reviews its instructions against the task's tier, flags constraints that look contradictory, impossible or out of proportion (e.g. exhaustive verification rules for a text change); flags go to the orchestrator before work begins, which relaxes or confirms. Aggregate spend caps: a daily cap and a per-project cap on top of the $10 per-task cap; at 80% warn, at 100% pause new dispatches and escalate to a human. Retry policy: a failed or killed task is never re-run automatically with the same plan; a retry needs a new plan or a changed instruction plus a note on why the first attempt failed; at most one automatic retry per task, then a human decides.

## 6. Telemetry and learning
Log per task: tier, estimate, actual cost per phase, call counts, sub-agent counts, checkpoints, decisions. Dashboard: estimate vs actual, review-to-implementation ratio, top cost offenders. Re-tune tier defaults and ratio thresholds periodically. Self-calibrating estimates: per tier (later per repo or recipe) a rolling window (last 20 tasks or 14 days), median actual-to-expected ratio for cost, calls and duration; new estimates = tier default x current rolling ratio within bounds; tasks killed by a guardrail are logged but left out of the window. Scheduled outlier sweep 1-2 times a day: flag outliers per tier (cost, calls, duration, review-to-implementation ratio, sub-agent count beyond e.g. 3 MAD from the median), pull logs and checkpoint reports, write a short diagnosis (bug, bad instruction, mis-tiered task, legitimately hard task), post a digest for human review with suggested threshold/tier-default changes; nothing changes automatically without approval.

## 7. Rollout order
1 Review-scope sanity check; 2 daily and per-project spend caps; 3 phase budgets plus ratio guardrail; 4 fan-out cap and loop tripwires; 5 retry policy; 6 expectation envelope from the orchestrator; 7 instruction pre-flight; 8 checkpoint and escalation protocol; 9 telemetry dashboard, self-calibrating estimates and the daily outlier sweep.

## Open questions
Should the expectation envelope come from the orchestrator model alone, or be calibrated against historical tasks for the same repo? How should human escalation be delivered: CLI, the Electron console, or a push notification? Should budgets be configurable per project or recipe?
