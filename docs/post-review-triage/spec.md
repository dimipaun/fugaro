# Fugaro: post-review triage step

## Problem

In Startup mode, review loops spend 10 to 12 hours on trivial findings, like docs not exactly matching the code. Patches sit unmerged, and work that depends on them stays blocked. Review caps exist, but nothing judges which findings actually matter.

## Solution

Add a **triage step** between review and fix. The reviewer still reports everything it finds. Triage decides what blocks the merge.

## Flow

1. Review produces its findings, unchanged.
2. Triage agent reads the findings, the task goal and the task's rigor level.
3. Each finding gets one of two verdicts:
   - **Block**: fix in this patch before merging.
   - **Defer**: file as a follow-up task linked to the parent task.
4. If nothing blocks, the patch merges right away.
5. If something blocks, the fixer gets **only the blocking findings** and must not touch anything else.
6. After the merge, follow-up tasks are queued and run in parallel. Tasks waiting on the parent are unblocked.

## Triage persona and question

The triage agent uses the rigor level's persona (see rigor levels) and answers one question for each finding:

> "Would a [persona] hold this merge for this?"

For Startup, that's the 10x cowboy CTO: get the main idea out, unblock other work, write down the debt.

The agent gives a verdict plus a one-line reason for each finding.

## Per-level behaviour (guidance in the persona prompt, not hard rules)

- **POC**: block almost nothing except a broken build or the main path not working.
- **Startup**: block only correctness of the main path, a broken build or tests, security issues and data loss. Things like doc drift, naming, style, small refactors and edge cases usually get deferred.
- **Pro**: block real bugs and meaningful edge cases. Defer cosmetic findings.
- **Critical**: defer very little. Triage mostly passes everything through.
- **Hotfix**: block only what the fix needs to be safe. Defer everything else.

## Follow-up tasks

- One task per deferred finding, or group related small findings into one task.
- Each follow-up gets the parent task's rigor level, the finding text and the triage reason.
- Mark them as tech debt so they're easy to list.

## Interaction with existing pieces

- Existing rigor-driven review caps stay as they are. When a cap is hit, anything still open is deferred instead of dropped.
- Triage runs again after every fix round, on the new review's findings.

## Out of scope (later)

- Re-reviewing only what changed instead of doing full reviews.

## Acceptance

- In Startup, a patch whose only findings are trivial merges after one review, with follow-ups filed.
- The fixer never changes code for deferred findings.
- Every verdict is logged with its reason, so the triage judgment can be checked and tuned.
