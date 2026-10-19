# Fugaro: In-Job Review Spec

Status: Draft · Oct 18, 2026 · Owner: Dimi Paun

## 1. Problem

Reviews are the bottleneck. Cloud coding jobs scale out, but every patch comes back to the local orchestrator, which runs at most 5 parallel review slots at 20–30 minutes each (roughly 10–15 reviews/hour). The laptop can't take more load.

## 2. Goal

Move review into the cloud job so that, most of the time, a job returns a PR that is already reviewed and ready to merge. The orchestrator only gives final sign-off and merges, which is cheap.

## 3. Job flow

Review is part of the recipe and runs inside the same Cloud Run job as the coding work.

1. **Rebase** onto the current target branch (for jobs picking up an existing PR; see §8).
2. **Inner loop** (cheap model, e.g. DeepSeek): code → compile → test → first-pass review → fix, until green.
3. **Outer loop** (review plan, e.g. senior reviewer on Opus): run the reviews in the plan; blocker/major findings go back to the inner loop in the same job.
4. **Cap** outer-loop rounds at 2–3 (configurable per review). If blocker/major findings remain, escalate.
5. **Minor cleanup**: one batched pass for minor findings, or list them in the PR.
6. **Return** the PR plus a structured verdict to the orchestrator.

The orchestrator gives final sign-off and merges. It steps in only on escalation, on flags, or when the job stopped early.

## 4. Review plans

The orchestrator already chooses reviews per task (Opus for hard tasks, Sonnet or DeepSeek for easy ones, an extra adversarial security review when there are security implications). It now states that choice up front.

When submitting a job, the orchestrator attaches a **review plan**: a list of reviews, each with:

- `type`: e.g. `general`, `adversarial-security`
- `model`: e.g. `opus`, `sonnet`, `deepseek`
- `effort`: effort level for the reviewer
- `max_rounds` (optional): round cap for this review

**Presets** cover common cases and are referenced by name. A custom plan is spelled out only when needed. The recipe runs whatever plan it receives.

Illustrative example (names and fields not final):

```yaml
review_plan:
  preset: security-sensitive
---
review_plan:
  reviews:
    - type: general
      model: opus
      effort: high
      max_rounds: 3
    - type: adversarial-security
      model: opus
      effort: high
      max_rounds: 2
```

Suggested starting presets: `trivial`, `standard`, `security-sensitive`.

## 5. Running reviews

- Reviews in a plan run **in parallel**, each with its own **fresh context** (diff + spec only), so reviewers don't inherit and rubber-stamp the coding model's reasoning.
- Findings from all reviews are **merged into one list** before going back to the coding loop: one fix pass per round, not one per reviewer.
- **Conflicting findings** (one reviewer wants a change, another says leave it): the senior model breaks the tie, or the job flags the conflict for the orchestrator.
- **Reviewers are read-only.** They comment; they never edit code. Every fix traces back to the finding that asked for it.

## 6. Findings and verdict

Each finding has:

- `severity`: `blocker` | `major` | `minor`
- `file` (and location where applicable)
- `description`: short

Routing:

- `blocker` and `major` → back to the coding loop.
- `minor` → batched into a single cleanup pass at the end, or listed in the PR for later. Minors never trigger a senior round on their own.

The job returns a structured verdict:

- `approve`: PR is ready to merge.
- `escalate`: round cap reached with blockers/majors left, unresolved reviewer conflict, or stopped early on time budget.
- A summary of what was found and fixed, plus any **flags** (see §7).

## 7. Review-only jobs

- Same recipe with the coding loop switched off. Input: an existing branch or PR, plus a review plan (preset or custom).
- One review format, two entry points: inside a coding job, or as a standalone job.
- **No self-escalation.** If the diff goes beyond what the plan covers (e.g. touches auth or payments with no security review in the plan), the job **flags** it in the verdict. The orchestrator decides whether to spin off a review-only job.

## 8. Rebase and stale base

- **Rebase first:** any job that picks up an existing PR (review-only, or sent back by the orchestrator for more work) starts by rebasing onto the current target branch, before any other work.
- If the rebase conflicts, resolving the conflicts is the job's first task, and a cheap review of the conflict resolution runs inside the same job.
- For a reviewed PR waiting to merge: if it rebases cleanly and tests pass, no re-review. If there were conflicts, run a cheap review-only job on just the conflict resolution, not the whole PR.
- Result: a returning PR is never stale on arrival.

## 9. Timeouts and time budget

- Current job limit is ~2–2.5 h. Typical run: ~30 min coding, ~1 h with reviews, +~1 h for 1–2 senior rounds. **Raise the limit to ~3 h.** (Cloud Run jobs allow up to 24 h.)
- **Time-budget check:** before starting another outer-loop round, the recipe checks the remaining time. If there isn't enough for a full round, it stops cleanly and hands its current state back to the orchestrator (verdict `escalate`) instead of being killed mid-fix.

## 10. Calibration

- Every review logs its findings and their outcomes: fixed, dismissed as a false positive, or caught later by a stronger reviewer.
- Use this data to tune presets (e.g. task types where DeepSeek misses things Opus catches) instead of going on gut feel.
