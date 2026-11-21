# Reviews check the pull request against its task

November 2026. Owner: the owner (agreed in conversation on 2026-11-18). Scope: every review stage Fugaro runs — the in-run first-line, senior and adversarial (level 3) reviews, and the independent review job `fugaro review --pr N` launches.

## Problem

No review stage sees the task a pull request was written for. The reviewers are told to review `git diff <base>...HEAD` and are given the base branch's instructions; `fugaro review` launches its task with no text at all, and the in-run review prompt does not carry the run's task either. The task does reach the pull request body (`## Task`), but the reviewer never reads it and the review job has no shell to fetch it.

So a verdict judges correctness, security and conventions, and never whether the change does what was asked. A well-built pull request that misses part of its brief, or does more than it was asked, can be passed with `ship`. The owner assumed until now that reviews already checked this.

## Goals

- Every review stage checks the pull request against its task by default.
- A review can still be run on the code alone, on request.
- A pull request no run produced (a person's, or one whose records are gone) can be reviewed against a spec supplied by hand.

## Requirements

1. **Default: review against the task.** Every review stage is given the task the pull request was written for and asked whether the change does what it asks.
2. **Where the task comes from.**
   - For a pull request a Fugaro run produced: the originating run's stored task (its `task.json` in the runs bucket), which the launcher wrote and is therefore trusted. When that task names a plan task, the reviewer is also pointed at that task's section of its implementation plan.
   - Never the pull request body: anyone who can edit the pull request can rewrite it.
   - For a follow-up run: the original task **and** the follow-up's own text, each labelled, so the pull request is judged against what it was for and not only against the fix instructions.
3. **`fugaro review --code-only`** restores today's behaviour: the reviewer gets no task and judges the code on its own.
4. **`fugaro review --spec FILE`** supplies the spec by hand, overriding the originating run's task, and is the way to review an orphan pull request against a spec. Its text is redacted like any other text a launcher adds.
5. `--code-only` and `--spec` together are refused at launch.
6. **What the reviewer reports.** Two new kinds of issue:
   - **unmet** — the task asks for something the diff does not do;
   - **beyond** — the diff does something the task did not ask for.
   They are scored like every other issue: an unmet requirement leads to `fix first`; a `beyond` item is low severity unless it is risky in itself.
7. **The task is intent, not a checklist.** The reviewer is told that briefs are often looser than the code, so it reports a gap only where the brief clearly asks for something.
8. **In-run reviews follow the same default**, and a repository or project can turn it off with a configuration key (for example `agent.review_against_task: false`); between layers, the stricter setting (on) wins.
9. When no task can be found (an orphan pull request with no `--spec`), the review runs code-only and says so in its output rather than failing.

## Out of scope

- Re-reviewing pull requests already merged.
- Reading the pull request body as a spec source.
