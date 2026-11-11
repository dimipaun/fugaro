# Fugaro: Outer-Loop Escalation for Non-Converging PRs

## Problem

Many Fugaro PRs stall for 20 to 40+ hours. Runs hit their cap, the orchestrator restarts them, and the senior reviewer finds one more issue every cycle. Lots of these PRs sit on the critical path and block other work. When a local agent is asked to just assess the PR, it often fixes conflicts and merges within minutes, which shows that many of these PRs were good enough long before they stopped.

Root cause: the reviewer's job is to find problems, not to decide whether the PR is acceptable. Rigor levels (even Startup mode) and the planned end-of-run triage step help, but nothing owns the **outer loop**, meaning the orchestrator repeatedly resubmitting the same PR.

## Goals

- Make the outer loop time-aware and attempt-aware, with a growing sense of urgency.
- Give the orchestrator a set of escalation tools instead of a single "send it back" option.
- Keep the work already invested. Don't throw away a long-running patch just to re-split the task.
- Take criticality into account: critical-path PRs escalate sooner.

## Non-goals

- Replacing the inner recipe loops or the triage step. This sits on top of both.

## Escalation ladder

The orchestrator tracks, per PR: total wall-clock time, number of outer-loop attempts, total cost, and whether the PR blocks other tasks. It picks the next tool based on these.

### Tier 0: Rebase and re-test

Before any escalation, run a cheap step: rebase onto the latest base, resolve conflicts, and re-run the tests. Many stalled PRs only need this, which is why a local agent can often close them in minutes. If it passes and the last review had no blocking findings, merge.

### Tier 1: Resume (early failure)

Applies when the PR is young (Startup default: the first failure, up to ~6 hours total; the default recipe times out at 3 hours, so this allows one more normal run). Send it back to the normal recipe loop, which is the current behaviour.

### Tier 2: Escalation recipe (not converging)

Applies past a time or attempt threshold (Startup default: after ~6 hours or 2 normal runs, exactly one attempt). Run a dedicated single-pass recipe:

- **Coder:** the project-level "strong" model (default Sonnet 5.5). For critical-path PRs, Critical rigor mode, or Hotfix mode, use the "strongest" model (default Opus 5.5). The tiers are defined once in project configuration (`models.strong`, `models.strongest`), and each role points at a tier (`escalation.tier2_coder`, `escalation.merge_judge`). The normal default coder is DeepSeek Flash.
- **Senior reviewer**, plus an optional **security reviewer**.
- **No inner loop.** One coding pass, one review pass.
- If the review is clean, merge. If it finds issues, go to Tier 3 instead of looping.

### Tier 3: Hold-your-nose merge (hard ceiling)

Applies past a hard ceiling (Startup default: right after the single Tier 2 attempt; hard ceiling ~9 hours), or straight after a Tier 2 run that still has findings. This mirrors what a human lead would do with a colleague's long-running patch. The only question asked is whether anything **fundamental** makes it unmergeable:

- security vulnerability
- data loss or corruption risk
- broken build or failing core tests
- wrong architecture or design that would be expensive to unwind later

If nothing fundamental is wrong: resolve conflicts, merge as is, and file **every remaining finding as a follow-up task** against the new base.

### Tier 4: Split and restart (last resort)

Only when Tier 3 finds a genuine blocker. Salvage what's usable (keep the branch, record what was learned), then break the task into smaller tasks, scoped to the blocker the merge judge named. This should be rare. It is not the default reaction to non-convergence.

## Default thresholds per rigor mode

Critical-path PRs halve all times.

| Mode | Normal runs before Tier 2 | Tier 2 model | Hard ceiling | At ceiling |
| --- | --- | --- | --- | --- |
| POC | 1 (~3 h) | strong | ~5 h | Tier 3, auto-merge |
| Startup | 2 (~6 h) | strong | ~9 h | Tier 3, auto-merge |
| Pro | 3 (~9 h) | strong | ~12 h | Tier 3, auto-merge + notify |
| Critical | 3 (~9 h) | strongest | ~12 h | Tier 3 verdict, human approves |
| Hotfix | 0 (straight to Tier 2) | strongest | ~3 h | Tier 3, auto-merge + notify |

## Urgency modifiers

- **Critical path:** if a PR blocks other tasks, scale all thresholds by 0.5x and use the strongest model for Tier 2.
- **Rigor level:** thresholds scale with the mode, as in the table above.
- **Hotfix:** go straight to Tier 2, using the strongest model.
- **Pro:** uses the strong model. Bump it up only if the daily reports show Pro PRs often failing after Tier 2.
- **Cost:** an optional cost ceiling triggers escalation just like time does.

## Churn detection

Compare findings across review rounds. If each round raises **new, minor** findings instead of the same unresolved issue, the reviewer is churning. Escalate straight to Tier 3, whatever the clock says. The same unresolved issue repeating is different: escalate to Tier 2.

## Merge judge (Tier 3 decision)

The Tier 3 call is made by a dedicated merge judge agent, separate from the reviewers, running on the strong model. It gets only the diff, the open findings, and the fundamentals checklist (security, data loss, broken build or core tests, wrong architecture). It never sees the reviewer's fix-request framing. It answers one question: **merge with follow-ups** or **blocked**, plus a short reason. A blocked verdict names the specific fundamental issue, and that issue becomes the scope of Tier 4.

## Follow-up tasks

Findings left over after a Tier 3 merge are filed as normal tasks, each tagged with the PR it came from. The merge judge marks each one as either "important, not blocking" or "minor". Important follow-ups are prioritised above new work; minor ones go into the normal queue.

## Human sign-off on Tier 3 merges

- **POC and Startup:** merge automatically.
- **Pro:** merge automatically and notify the project owner.
- **Critical:** wait for a human to approve before merging.
- **Hotfix:** merge automatically and notify the project owner.

## Interaction with triage

The end-of-run triage step keeps deciding, inside each run, whether findings are acceptable at the current rigor level. The escalation ladder is the outer-loop backstop for cases where triage still sends work back over and over.

## Daily review of escalation metrics

Same pattern as the other Fugaro reports: each orchestrator produces its own daily report, and these roll up into a project-level report for the project owner. Each covers the last 24 hours and a rolling 7 days:

- PRs per tier and median time-to-merge per tier
- Tier 3 merges and any regressions traced back to them
- Follow-ups created by Tier 3 merges, and how many have been closed
- Churn detections, and the longest-running open PRs
- Hours and cost spent on PRs that went past Tier 1

The report suggests threshold changes. For example, if Tier 3 merges rarely cause regressions, lower the thresholds; if they often do, raise them. A human approves any change.

## Configuration

Thresholds are set per rigor level and can be overridden at the project level, following the existing config precedence. Example keys:

- Model tiers, defined once: `models.strong` (default Sonnet 5.5), `models.strongest` (default Opus 5.5)
- Roles point at a tier by name, so swapping a model only changes the tier definition:
  - `escalation.tier2_coder: strong` (critical-path PRs, Critical and Hotfix use `strongest`)
  - `escalation.merge_judge: strong`
- `escalation.tier2_after_hours`, `escalation.tier2_after_attempts`
- `escalation.tier3_after_hours`
- `escalation.critical_path_factor` (default 0.5)
- `escalation.cost_ceiling`

## Observability

- Record the tier transitions and their reasons on every PR.
- Dashboard: time-to-merge distribution, the share of PRs reaching each tier, and the follow-ups created by Tier 3 merges.
- Tune thresholds based on how often Tier 3 merges cause regressions.
