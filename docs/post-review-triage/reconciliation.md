# Post-review triage: reconciliation

Where this file disagrees with [spec.md](spec.md), this file wins. Checked against `origin/main` at
`2232815b0` on 2026-11-08, plus the open pull requests #1999 (BF1m) and #1990 (BF2) read as they stand.
Decision numbers D1800-D1829 and checks 470-479 are this folder's block (the highest in use were D1779
and Check 462).

## What the spec gets right, wrong and silent about

The spec reads greenfield. It is not: the repository already decides "which findings block" in code, has
the five modes it names (plus Hotfix), per-mode review caps, and a record of what a mode tolerated. What it
does not have is the *second judge*, a fixer that sees only blockers, a way to turn a deferral into a task,
and any notion of a merge. The reconciliation is therefore about layering the spec's judge over what is
there and about two things the spec assumes and the platform forbids: that Fugaro merges, and that a job can
queue work.

| Spec concept | What the repo already has |
| --- | --- |
| Block / Defer verdict | `rigor.Blocks` and `rigor.Split` (`internal/rigor/table.go`): a 1-5 score on three axes plus a class, thresholds per mode. "Defer" is `FindingsPolicy.Tolerated`. Deterministic, no reason string. |
| Persona per rigor level | Written in `docs/rigor-modes/levels-spec.md` (Startup is "the 10x cowboy"), **not built**: the three prompt builders are level-blind (`SystemPrompt`, `ReviewPrompt`, `FixPrompt` in `internal/runner/prompts.go`). |
| The five levels | `rigor.Modes`: POC, Startup, Pro, Critical, Hotfix (off-axis). Default Pro. |
| Only blockers go to the fixer | **Not built.** `FixPrompt` takes every `fixableFindings` (only `needs_owner` removed). |
| Nothing blocks, so merge | `Decide` (`internal/runner/outcome.go`) already returns ready when the whole last round is tolerated; but the loop runs fix rounds first, and **no merge exists** (`gitprov` has no merge call). |
| Deferred finding becomes a follow-up task | `internal/backlog` writes Markdown items (`rigor.Files`); run-side filing was removed at finalize. `task.Task` has no parent, label or dependency field. |
| Follow-ups run in parallel, waiters unblocked | Dependencies exist only in the orchestrator state (`WorkItem.DependsOn`), which a job cannot write. |
| Review cap hit defers | `rigor.roundDefaults` caps rounds; a cap hit makes the PR a draft (`ReviewNotConverged`). BF1m (#1999) is changing exactly this for Startup and POC. |
| Triage reruns each fix round | `roundRigor` re-resolves the mode after each fix; `seniorLoop` reads a new verdict each round. |
| Every verdict logged with a reason | `rec.Tolerated` (no reason, no blocked ones). No per-finding id exists. |
| A new stage | `config.RunStages` is a closed list; `MixedAccountStages` the only mixed-account allow-list; `recipe` step kinds are closed at version 1. |

## Findings

- **F1. Exists.** Per-mode blocking is built: `rigor.Blocks(m, s)` and `rigor.Split(m, findings)` in
  `internal/rigor/table.go`; `runner.Decide` reads `FindingsPolicy{Blocking, Tolerated}` through
  `findingsPolicyOf`. The thresholds are POC `{}`, Startup `{Risk: 8}`, Pro `{Risk: 4, Rare: 1, Expensive: 4}`,
  Critical `{Risk: 1}`, Hotfix `{Override: overrideSecurity}`. The spec proposes a second judge for a decision
  that already has a deterministic one.
- **F2. Wrong.** "See rigor levels" for the persona points at work that exists only as design:
  `docs/rigor-modes/levels-reconciliation.md` LF7 ("no persona layer": the prompt builders are level-blind), LD1
  (personas shape prompts, thresholds decide blocking), LD10 (embedded, one Markdown file per level). No code
  contains a persona. The spec's "guidance in the persona prompt, not hard rules" for blocking contradicts LD1
  unless the judge sits *inside* the table's floor (D1800).
- **F3. Rule.** The floors in `rigor.Blocks`: an unscored finding blocks in every mode; class `security` blocks in
  every mode including Hotfix (`overrideSecurity`); `data_loss` and impact 5 block in all modes but Hotfix; a
  `needs_owner` finding stops the loop (`ownerStop`, `pendingOwner` in `Decide`) and is never fixable.
  The spec's Hotfix line ("defer everything else") is wrong for security, and its Critical line ("defer very
  little") is wrong against D7 (Critical tolerates nothing). A judge may only *add* deferrals among findings the
  table blocks and the floors do not name (D1800, D1815).
- **F4. Gap.** The loop does not behave as acceptance 1 and 2 need. `seniorLoop` (`internal/runner/runner.go`) calls
  `r.fix(...)` after any round whose verdict is not `ship` unless the round cap is reached; `fix` builds
  `FixPrompt(v)`, which lists every `fixableFindings(v.Findings)`. Tolerance acts only in `Decide`, after the
  loop. So a round of only tolerated findings still triggers fix rounds, and a mixed round hands the fixer the
  tolerated findings too. The loop needs a break when nothing blocks, and the fixer a blocking-only brief.
- **F5. Gap.** Deferral filing was built and removed: the `finalize` comment in `internal/runner/runner.go` says
  the run "used to file the findings it left open as backlog items here, which committed after that suite run and
  was not verified". Today tolerated findings go to `rec.Tolerated` (`runstore.ToleratedFinding`), the PR status
  section (`toleratedPart` in `internal/runner/prflow.go`) and `result.json`. `internal/backlog` is the repo's
  "tech debt" mechanism, and it is a Markdown file, not a task. Nothing in the tree uses the words "tech debt".
- **F6. Gap.** A job cannot create a task. `fugaro run` (the CLI) writes the task to the runs bucket; a coding
  agent's session never writes cloud state (CLAUDE.md; `internal/cli/accounts_add.go` refuses under an agent
  marker). `task.Task` (`internal/task/task.go`, `schemas/task.schema.json`, `additionalProperties: false`,
  `version` const 1) carries `PlanTask`, `Batch`, `PreviousRun`, `PR`, `Branch`, `Rigor`, `Paths` and no parent,
  follow-up-of, label or dependency field. So deferrals can only be *recorded* by the run and turned into tasks by
  the CLI or the orchestrator (D1803, D1809).
- **F7. Exists.** `internal/followup` and `fugaro run --pr N` continue an existing pull request from review
  comments by trusted authors. It is comment-sourced and carries no level of its own; it is not the spec's
  follow-up (a new task from a finding), and cannot be reused for it.
- **F8. Wrong.** "The patch merges right away" contradicts the code: `internal/gitprov` has no merge call (it only
  reads `PRMerged` and `PRClosed`), and `docs/rigor-modes/reconciliation.md` D13 says Fugaro opens pull requests and
  never merges or deploys. "Ready" is `ApplyReady(ctx, number, reviewers, labels)`: the PR leaves draft. A person or
  the coordinator merges. "Tasks waiting on the parent are unblocked" can only mean the orchestrator seeing a merge
  later (D1802, D1809).
- **F9. Gap.** `config.RunStages` (`internal/config/stage_accounts.go`) is `plan, implement, review_first, review,
  adversarial, fix` and validates `stages.<stage>.claude_account` in local config, the published policy node,
  `fugaro validate` and launch. A `triage` stage must be added to it, and `MixedAccountStages` (today `review`,
  `adversarial`) decides whether an API-key account may serve it; the runner records `rec.StageAccounts[stage]`.
  `internal/recipe` step kinds are closed at `Version = 1`, so triage is a runner-internal step like the digest,
  not a recipe step (D1801). `.fugaro/review.md` already tells reviewers to report blocking findings only, which
  pre-triages at the reviewer.
- **F10. Rule.** A field never changes meaning (CLAUDE.md). `result.json` (`schemas/result.schema.json`, `version`
  const 1, closed) already has `tolerated`, written by `toleratedRecord` and meaning "the mode's table let it
  through". The triage verdicts need new names, not a reuse of `tolerated` (D1810).
- **F11. Gap.** A finding has no identity across rounds: `runner.Finding` carries severity, likelihood, impact,
  fix_cost, class, needs_owner, file and summary, and `VerdictSchema` requires only `summary`. Round summaries keep
  counts only (`runstore.ReviewSummary`). A verdict must reference a finding by its index in that round's list plus
  a hash of file and summary (D1810). The independent review job has a different artifact
  (`schemas/review.schema.json`, `reviewartifact.go`); review-only runs never change draft state, so triage does
  not apply to them.
- **F12. Exists.** Caps are built: `rigor.roundDefaults` (POC 1/1, Startup 2/2, Pro 3/3, Critical 3/5, Hotfix 1/1),
  re-read each round through `reviewRounds(st)`. "Existing caps stay as they are" is accurate. A cap hit today makes
  the PR a draft with `failure.ReviewNotConverged`; nothing files the leftovers. Token and money caps are separate
  halts (`internal/runner/halt.go`) and end the run whatever triage says.
- **F13. Rule.** Review levels are built (`firstLine`, `seniorLoop`, `adversarialLoop`; `Decide` reads a held-back
  level; `docs/review-levels-ab-jobmax`). Triage has to say which level's findings it judges (D1807).
  `docs/recipes.md` still says readiness is "unchanged and not configurable", already stale against `Decide`; the
  Docs tests pin sentences, so a doc change keeps the pinned fact (CLAUDE.md "Docs are tested").
- **F14. Rule.** The mode is opt-in and read from the *base* branch (`baseRigor`/`readBaseRigor` in
  `internal/runner/rigor.go`, D25): `findingsPolicyOf` returns the zero policy unless `cfg.Configured()`, because a
  branch that could set its own mode could set POC in the pull request it wants merged. An unconfigured repository
  resolves Pro by default (D19) and tolerates nothing; it must not defer (D1812). The mode can rise mid-run (D22) and
  never falls, so the persona is chosen from the mode reached at that round (`reachedRigor`), not the starting one.
- **F15. Rule.** Floors are outside every layer (`levels-reconciliation` LF5; security classification D1500-D1519):
  triage runs at the post-floor mode. Reviewer text is untrusted data to the triage agent exactly as `.fugaro/review.md`
  makes it for reviewers; it may not flip a `needs_owner` finding (D1816).
- **F16. Rule.** CLAUDE.md: "A fix round adds no functionality." `FixPrompt` today says only "Address each one,
  commit, and run `fugaro verify test` again" and has no "touch nothing else" sentence; the new text needs one, and
  a prompt is on `.fugaro/review.md`'s mutation-check list.
- **F17. Rule.** Config and wire formats decode strictly with schemas in `schemas/`; a project layer may only
  tighten (`internal/policy`). A new `triage` key is tighter when it turns triage *off* (more findings block), so the
  project layer may disable and a repository may not re-enable above it (D1814). Plugin tests check every command
  and flag a skill names, so a new command means a skill change in the same PR.
- **F18. Open, settled by D1800.** Two judges: the table (deterministic) and the agent (the spec). Settled: the
  agent runs inside the floor and only among table-blocked findings; the table stays the authority and the
  fallback. A triage call that fails or returns no parseable verdict blocks every finding (D1806, matching D4 and
  `Decide`'s `last.Verdict == "none"` rule).
- **F19. Exists (in flight).** BF1m (#1999, a draft at the time of writing) is the owner's 2026-11-08 ruling that
  Startup and POC finalize ready at the round cap or a time threshold and keep the draft for Pro and above. It
  edits `internal/rigor/table.go`, `internal/runner/outcome.go`, `runner.go`, `prflow.go`, `report.go`, `budget.go`,
  `internal/config/*`, `schemas/result.schema.json` and `schemas/fugaro.schema.json`. This plan is built **on top
  of** it (D1813), so every task that shares those files waits for it.
- **F20. Exists (different layer).** BF2 (#1990) adds a stale-run triage to the stateless orchestrator and states
  that "the rule is code, never a model" (D1244, matching D1221 and D1234). That governs the orchestrator's own
  decisions; this plan's triage is an in-job *stage* (a model call by nature, like review). No conflict, but the
  two must not be confused: BF2 triages whole stale runs, this plan triages a run's findings.

## Decisions

Answered through `AskUserQuestion` on 2026-11-08 unless marked "resolved" (one defensible answer, not asked).

| ID | Question | Recommendation and cost | Owner's answer |
| --- | --- | --- | --- |
| **D1800** | Who decides Block vs Defer? | An agent *within the floor*: it decides among findings the table blocks, never for floor classes, fails safe to Block. Costs one short model call per round. The alternatives: the table only (cheap, cannot tell doc drift by its words) or an agent that may also defer floor findings (contradicts D1-D7). | **2026-11-08: the recommendation.** The table stays the authority and the fallback. |
| **D1801** | Where does triage live? | Resolved: a runner-internal stage named `triage`, added to `config.RunStages` and `MixedAccountStages`; no recipe step kind (recipes are closed at version 1). | Resolved 2026-11-08 (not asked). |
| **D1802** | What does "merges right away" mean? | Resolved: the PR is marked **ready** (`ApplyReady`); a person or the coordinator merges (D13: Fugaro never merges). The spec's acceptance 1 reads "is ready after one review". | Resolved 2026-11-08 (not asked). |
| **D1803** | How does a deferral become a follow-up task? | The run records deferred findings in a new result field; a CLI command turns them into tasks carrying new optional `Task` fields (`follow_up_of`: run id plus finding keys; `debt: true`). No change to what a job may write. Alternative: Markdown backlog files (not tasks, not runnable). | **2026-11-08: the recommendation.** |
| **D1804** | Which rigor do follow-ups get? | Resolved: the parent's recorded *applied* mode (post-floor, `RigorRecord.Applied`), set as `Task.Rigor`, resolved again at launch through the floors (a follow-up never lowers one). | Resolved 2026-11-08 (not asked). |
| **D1805** | A review cap is hit with findings left. | Follow BF1m: Startup and POC finalize ready at the cap with the leftovers deferred and recorded; Pro and above stay draft at the cap but still record non-floor leftovers as deferrals; floor findings always hold the draft. | **2026-11-08: the recommendation.** |
| **D1806** | What if triage fails, times out or returns nothing parseable? | Resolved: every finding blocks (D4), recorded as `triage_failed`; the run continues as today. | Resolved 2026-11-08 (not asked). |
| **D1807** | Which review levels does triage apply to? | The recommendation was levels 1 and 2 only (level 3 passes every finding through). | **2026-11-08: all levels, NOT the recommendation.** Consequence: a run that asked for level 3 can finish ready with non-floor adversarial findings deferred. See Open tension 1. |
| **D1808** | Which model and account serve the stage? | The senior reviewer's model and account: independent of the coder, short input. A mixed-account stage like `review`. | **2026-11-08: the recommendation.** `stages.triage.claude_account` overrides it. |
| **D1809** | Who launches follow-ups after the parent merges? | A new CLI command now, callable by the orchestrator later. Alternatives: orchestrator only (nothing works until that chain lands), or launch at ready (they would run on a base lacking the parent). | **2026-11-08: the recommendation.** |
| **D1810** | How are verdicts logged? | Resolved: new optional `triage` block in `result.json` (new names, `tolerated` untouched), one entry per finding: round, index, key (hash of file and summary), verdict, one-line reason, group key, source (`agent`, `table`, `fail_safe`, `cap`). | Resolved 2026-11-08 (not asked). |
| **D1811** | Which modes run the agent? | Startup, Pro and Hotfix. POC (the table already tolerates all but the floor) and Critical (D7: tolerates nothing) keep the table only and are byte-identical to today. | **2026-11-08: the recommendation.** |
| **D1812** | What about a repository that never opted in? | Resolved: no triage and no deferral where `cfg.Configured()` is false (D25: the base branch decides). | Resolved 2026-11-08 (not asked). |
| **D1813** | How does this relate to BF1m (#1999)? | BF1m ships first as the deterministic baseline; this plan builds over it. Tasks sharing its files wait for its merge. | **2026-11-08: the recommendation.** |
| **D1814** | What is the config surface? | Resolved: the `triage` stage's `claude_account`, and one boolean `agent.triage` (default on in the three modes). The project layer may turn it off; a repository may not turn it back on (tightening only). Persona text is embedded per level (LD10), not configurable. | Resolved 2026-11-08 (not asked). |
| **D1815** | The spec's Hotfix and Critical lines. | Resolved: Hotfix defers everything except what the floor keeps (security still blocks); Critical is not triaged (D7). Both spec lines are amended here. | Resolved 2026-11-08 (not asked). |
| **D1816** | How does triage treat reviewer text? | Resolved: as untrusted data (the same stance as `.fugaro/review.md`); it can never turn a `needs_owner` finding into a deferral, and a verdict that names no known finding is dropped. | Resolved 2026-11-08 (not asked). |

## Open tensions and open items for the owner

1. **Level 3 and deferral (D1807, not the recommendation).** Level 3 is the adversarial outer review the owner asks for per
   job, mostly for security trees. With "all levels", a level-3 run can finish ready with its non-floor adversarial
   findings deferred. The floors still hold (security, data loss, impact 5, `needs_owner`). Choices: keep as
   answered, or let an explicit level-3 request pass every finding through. Please rule.
2. **A fixer that sees only blockers needs the loop to stop early (F4).** That changes today's loop in `Decide` and
   `seniorLoop`, which BF1m also edits; the plan sequences it after BF1m. Please confirm the order stands if BF1m
   is delayed.
3. **Follow-up wiring to the orchestrator is out of scope.** D1809 gives a CLI command; "tasks waiting on the parent
   are unblocked" needs the orchestrator to call it and set `DependsOn` (planned elsewhere). Say if you want a task
   for that in this plan once its chain lands.
4. **Merging.** Acceptance 1 reads "ready" here (D1802). If you want Fugaro to merge, that is a different, larger
   decision (a `gitprov` merge call against D13) and not part of this plan.

## UNVERIFIED

- Whether the persona work of `docs/rigor-modes/levels-*` (T1.5 floors, the `--mode` flag) is fully merged: the code
  shows `Decide`, `Split`, `Files` and the round defaults on `main`, but the levels document's own status line says its
  branch was 131 commits behind `main` and "nothing here is approved to build".
- The final content of BF1m (#1999) and BF2 (#1990): both were open drafts when read; any change to them re-checks F19.
- Per-stage model and budget defaults for a new stage: `internal/config/config.go` tables were read by name only.
- Whether the run record has a version-bump procedure beyond `result.schema.json`'s `version` const 1.
- Whether `internal/orchestrator/launch.go` launches from `WorkItem.DependsOn` today (it exists on `main`; not read).
- Whether any rule under `.claude/rules/` says anything about a stage or prompt change beyond CLAUDE.md.
