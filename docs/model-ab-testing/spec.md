# Fugaro Model A/B Testing Spec

## Goal
Continuously optimise cost and quality as new models appear, using real head-to-head results instead of estimates alone. Today's "what it would have cost on model X" reports are useful but unreliable: a stronger model costs more per token but may need fewer iterations.

## Approach
- Sample a small share of jobs (configurable, roughly 1–10%) for A/B testing. Never all jobs.
- A sampled job runs on the current default model and on a challenger model.
- Record the actual cost (tokens, iterations, wall time) and quality of each run.
- Only one result is kept for merge. The other is evaluation data.

## Grouping
- Group results by rigor level (POC, Startup, Pro, Critical, Hotfix) and/or job difficulty.
- Simple jobs may do fine on cheaper models, so conclusions are drawn per group, never globally.
- Set a minimum sample size per group before reporting a conclusion.

## Automated quality judging
- No human review. The orchestrator agent judges quality using a dedicated skill.
- Judging is blind: the judge doesn't know which model produced which output, to avoid same-family bias.
- Scores are anchored in hard signals: tests passing, lint/type checks, review findings, iterations to green, and later rework or reverts.

## Cost/quality trade-off (fuzzy logic)
- A utility function weighs quality gain against extra cost.
- If quality is clearly better for a trivial extra cost, prefer quality.
- Each rigor level has its own exchange rate: Critical pays a lot for small quality gains, POC pays very little.

## Reporting, not auto-switching (v1)
- Results go into the existing daily, weekly and monthly cost reports.
- Reports recommend default changes. The user decides.
- In team projects, results are aggregated at the project level across developers, so one unified decision is made on project defaults. The per-project coordinator is a natural place for this aggregation.
- Pooling across developers reaches meaningful sample sizes faster.

## Later
- Dynamic, automatic flipping of defaults once there's confidence in the judging and the sample sizes.
- Automatic challenger selection when new models are released.

## Open questions
- Default sampling rate and cadence.
- How job difficulty is classified.
- Budget cap for A/B overhead per project.
