# Rigor Levels: the 2026-10-22 spec against the rigor-modes work

**Checked against** `main` at `cc50b5b23` and the branch `rigor-modes` (three commits ahead of `main` and 131 behind: Slice 0, Slice 1 and the backlog's T4.3/T4.4), **2026-10-22**. **Where this document and [levels-spec.md](levels-spec.md) disagree, this document wins.** It amends nothing in [reconciliation.md](reconciliation.md) (D1–D32, answered 2026-10-20); it lists exactly which of those answers the new spec touches and what the owner ruled.

**Numbers this work holds:** decisions **D261–D271** (the last on `main` is D252 and PR #922 takes D253–D260), findings **LF1–LF14**, **no new check number** (the live check stays Check 119 of Slice 8, extended). Nothing here is approved to build.

## 0. What the spec is, and what it is not

The owner's second spec states the five rigor **levels** (`poc`, `startup`, `pro`, `critical`, `hotfix`) as **personas** (a short character each: what it cares about, ignores and hands back), how a level is **resolved** (built-in `pro` < project default < project area/repo overrides < the repo's own YAML < the task), a **promote** task that raises existing code to a higher level, **review matching** (a reviewer judges by the task's own level) and the **debt artefact** each level leaves. It is a behavioural layer. The 2026-10-20 work is the **mechanism**: scores on findings, thresholds per mode, a resolver, floors, a Markdown backlog. The two are complementary, which is the finding that shapes everything below.

## 1. Spec concept → what the repository has

| The spec says | What exists | Verdict |
| --- | --- | --- |
| Five levels, default `pro` | the five modes, default **Pro** (D19; Critical stays the default of the security-sensitive tier, D18) | **Exists** (LF1) |
| Personas as the agent's character | nothing: the prompts (`internal/runner/prompts.go`: `SystemPrompt`, `ReviewPrompt`, `FixPrompt`) are mode-blind | **Gap** (LF7) |
| Findings handling per level | scores 1–5 on likelihood, severity, fix cost; thresholds per mode; applied inside `runner.Decide` (D1–D7) | **Exists** (LF6) |
| `fugaro run --rigor hotfix`, `level:`, `rigor: critical` | planned `--mode`, area rule key `mode:`, config key `rigor:` (D5, D12) | **Wrong words** (LF2) |
| Project default, project areas, project repos, repo YAML, task | D6: area > repo > project; D20 areas are path globs inside one repo; floors (D8, D18, D21) | **Rule** (LF3, LF4, LF5) |
| `deferred list` / `open-risks` / `morning note` / run note | the Markdown backlog: eight front-matter fields, three statuses (D9, D26–D32; T4.3/T4.4 on the branch) | **Exists + Gap** (LF8, LF9) |
| Hotfix: rollback ready, verify restored | Fugaro opens PRs and never deploys (D13); a deterministic diff rule gates, the agent's confidence is recorded (D14); an operator invokes it, gated on the base branch (D12) | **Rule** (LF10) |
| Pro asks for guidance on scope, cost, data model | headless runs; a draft PR is the existing "not ready" state | **Gap** (LF11) |
| Recipes can read the level | recipes arrange the loop (`internal/recipe`); D16: modes lower the cost ceiling and never raise it | **Gap + Rule** (LF12) |
| Promote task | follow-up runs on a PR exist (`fugaro run --pr N`); nothing that reads a backlog or audits | **Gap** (LF13) |
| Reviewer judges by the same level | D7(a): the findings policy is handed to the reviewer | **Exists, extended** (LF14) |

## 2. Findings

**LF1 — Exists.** The five levels and the default `pro` are decisions already answered (D19: the built-in default is Pro, and Critical is what a repository pins to; D18: the security-sensitive tier stays Critical). The new spec restates them. *Consequence:* nothing to decide.

**LF2 — Wrong (vocabulary).** The spec says *level*, `level:`, `--rigor`; the approved plan, the Slice 1 code on the branch (`rigor.areas[].mode`, the `mode` front-matter field of a backlog item, D27) and the planned flag (D12, `--mode`) say *mode*. The config key `rigor:` already matches. Nothing of this is released: only Slices 0–1 and the backlog's T4.3/T4.4 exist, and only on the branch. *Consequence:* a rename, cheap now and expensive after Slice 3. → **D262**.

**LF3 — Wrong (what an area is).** The spec's "area of interest" (`console`, `mobile`) was first read as a group of repos. The approved plan's area is a **path glob inside one repository** (D20; the code: `rigor.areas: [{name, match, mode}]`, and the project layer's `defaults.rigor.areas`, whose invalid fixture `rigor-area-outside-repo.yaml` refuses a glob that leaves the repo). The owner's answer to D264: an area is **a named semantic part of the project**, not necessarily one repo; its members are whole repos or repos-with-paths. *Consequence:* the two notions unify into one: an area is a name plus members, defined at the project layer (and a repo's own YAML defines areas for its own paths as today). → **D264**.

**LF4 — Rule (precedence).** D6 answered: area, then repository, then project, then the built-in default, one resolver, the mode never falls within a run. The spec: built-in < project default < project areas < project per-repo < repo's own YAML < per-task. With D264's areas the layers become, weakest to strongest: **built-in `pro` < project default < project area (matched member) < project per-repo < the repo's own YAML (its default, then its own path areas) < the task**, floors excepted (LF5). The only reversal against D6 is area vs repo at the **project** layer (D6 had area strongest; the spec has the project's per-repo entry beating the project's area); the repo's own path areas stay the narrowest layer inside a repo, as D6 says. *Consequence:* D6 is amended in one place. → **D263/D264**.

**LF5 — Rule (floors).** D8, D18 and D21: a floor (the security-sensitive tier and any declared floor) is outside every layer; **no run, task request, follow-up, label or job variable lowers one**, only a merged change to the base branch's configuration. The spec's "per-task override: strongest of all" read literally lets a task go below a floor. *Consequence:* the owner's D263: floors stay; "strongest" means strongest among **defaults**. A task may raise above a floor or lower a default, never go below a floor. → **D263**.

**LF6 — Exists (thresholds vs personas).** D1–D7: the reviewer scores each finding (1–5 on three axes), a mode is a set of thresholds, and the policy lives inside `runner.Decide`, so "may this pull request be ready" has one deterministic answer. The spec expresses the same five levels as characters, with no numbers. Two ways to read it: personas **replace** the thresholds (readiness then depends on model judgement) or **sit on top** of them. *Consequence:* the owner's D261: both. The persona is the prompt that shapes how the coder and reviewer behave; the thresholds still decide what blocks. → **D261**.

**LF7 — Gap (no persona layer).** The three prompt builders are level-blind. A persona needs an injection point: the system prompt of the implement stage (`SystemPrompt`), the reviewer's prompt (`ReviewPrompt`) and the fix prompt (`FixPrompt`). The text must live somewhere stable and tested. → **D270** (embedded, one Markdown file per level, not overridable yet).

**LF8 — Exists (the debt artefacts are the backlog).** D9 and D30: one Markdown file per tolerated finding, filed in the run's own pull request; Startup and Pro file one per tolerated finding, POC none or one line, Critical none of its own, Hotfix one for what it deferred. The spec's "deferred list" is that set of items, the Hotfix "morning note" is the item Hotfix always files, POC's "run note and prototype warning" is PR-body text. *Consequence:* no second format. → **D265**.

**LF9 — Gap (open risks).** The spec has Critical **never defer quietly**: anything it cannot fully cover is an explicit **open risk**. D30 had Critical file nothing of its own (it tolerates nothing). The backlog's front matter (D27) has eight fields (`id`, `title`, `mode`, `area`, `status`, `filed-by`, `filed-in`, `date`) and a closed three-word `status`. An open risk is a finding Critical could not close; it needs to be distinguishable from a tolerated finding. *Consequence:* a ninth required front-matter field `kind` (`deferred`, `open-risk`), and the `mode` field is renamed `level` (LF2). The generated index and its pins change with it. → **D265**.

**LF10 — Rule (Hotfix).** The persona says rollback ready before deploying and verify the fix restored service. D13: Fugaro opens pull requests and never deploys; `gitprov` has no merge call; D14: a deterministic diff rule gates and the agent's confidence number is recorded and never trusted; D12: an operator invokes Hotfix, gated on the base branch's configuration, never a PR comment. *Consequence:* the persona's text is adjusted to what Fugaro can do: rollback-ready means the PR body carries the exact revert (a `git revert` target or the flag to flip); "verify restored" is a line in the morning note for the human or CI. → **D267**.

**LF11 — Gap (Pro asks for guidance).** A cloud run cannot ask mid-run. The existing "not ready" state is a draft PR. *Consequence:* a run that reaches a scope, cost or data-model call it should not make stops with the PR as a draft whose body **opens with an OPEN QUESTIONS section** and ends with a distinct reason, so a follow-up (or the orchestrator) can answer it. One new end reason, no new waiting state. → **D268**.

**LF12 — Gap + Rule (recipes).** Recipes arrange the loop and name models; D16: modes lower the budget ceiling, never raise it; D10: the verify stage's gate is the recipe's `check`. The spec wants a recipe to read the level (cheap coder plus senior reviewer at `poc`/`startup`, stronger models at `critical`). *Consequence:* a recipe may branch on the resolved level; the per-run cap is unchanged, so a Critical recipe with stronger models reaches the cap sooner. The shipped recipes stay level-agnostic until a per-level one is asked for. → **D269**.

**LF13 — Gap (promote).** No task kind reads a backlog, audits code, or raises its level. The nearest thing is the follow-up run on a pull request. A promote task reads an area's code plus its backlog items (deferred, open-risk, morning notes) and works at the target level; from `poc` there is no list, so an **audit pass** comes first. It also closes the loop D15 left open ("the automatic follow-up: recorded now, scheduled later"): the Hotfix morning note is the promote task's input. → **D266** (a later slice).

**LF14 — Exists, extended (review matching).** D7(a) already hands the reviewer the findings policy. The spec adds per-level review standards: no nitpicking POC for missing error handling, adversarial scrutiny at Critical, checking that skipped items appear on the deferred list (startup, pro) or as open risks (critical), checking minimality, the revert plan and the morning note (hotfix). *Consequence:* the persona text carries the standards (the reviewer's persona is the level's own); the artefact-presence checks are deterministic checks in `Decide`, not model judgement. → **D261**, **D265**.

## 3. Decisions (all answered by the owner, 2026-10-22)

| # | Question | Recommendation | Owner's answer |
| --- | --- | --- | --- |
| **D261** | How do personas relate to the scored-findings mechanism? | Both: the persona is the coder's and reviewer's prompt; the thresholds stay and decide readiness. | **Both (recommended), 2026-10-22.** Readiness stays one deterministic rule inside `Decide`; the persona shapes behaviour. |
| **D262** | Rename mode → level, `--mode` → `--rigor`? | Yes: nothing is released, only the branch holds it. | **Rename (recommended), 2026-10-22.** `--rigor`, area key `level:`, backlog field `level`, prose "rigor level". |
| **D263** | Do floors survive the spec's "per-task is strongest"? | Yes: per-task is strongest among defaults; no one goes below a floor. | **Keep floors (recommended), 2026-10-22.** The one exception to the order is written once, in the resolver's statement. |
| **D264** | Areas of interest vs path areas? | (My question offered repo groups or replace; the owner reframed.) | **2026-10-22, the owner's words: "areas of interest are not necessarily tied to a repo but … semantic parts of the project".** Confirmed reading: an area is a named semantic part of the project; members are whole repos or repos-with-paths, defined at the project level, and a repo's own YAML may define areas for its own paths. A run touching several areas gets the strictest (D23). A repo in no area falls back to its repo setting. (Not my recommendation: the recommendation was two named concepts.) |
| **D265** | Where do the debt artefacts live? | The backlog, plus an `open-risk` kind. | **Reuse the backlog (recommended), 2026-10-22.** Ninth required front-matter field `kind` (`deferred` or `open-risk`); POC's run note and the "prototype, do not ship" line are PR-body text; Hotfix's morning note is the item Hotfix always files plus a PR-body section. |
| **D266** | Build promote tasks? | Yes, a later slice: `fugaro run --promote --rigor pro <area>`. | **Yes (recommended), 2026-10-22.** The last slice; a POC source gets an audit pass first. |
| **D267** | Hotfix rollback and verification, given D13? | Rollback = a revert plan in the PR body; verification = the morning note's checklist. | **As recommended, 2026-10-22.** |
| **D268** | How does Pro "ask for guidance" in a headless run? | Stop at a draft PR with an OPEN QUESTIONS section and a distinct end reason. | **As recommended, 2026-10-22.** |
| **D269** | Expose the level to recipes? | Yes; the budget ceiling is unchanged (D16). | **Yes (recommended), 2026-10-22.** |
| **D270** | Where do persona texts live? | Embedded, one Markdown file per level, not overridable yet. | **As recommended, 2026-10-22.** |
| **D271** | How is the addendum planned against the approved plan? | A levels overlay plan beside the existing one. | **Overlay (recommended), 2026-10-22.** [levels-implementation-plan.md](levels-implementation-plan.md); the 2026-10-20 plan and answers stay as approved. |

## 4. Open tensions and open items for the owner

1. **"Critical is byte-identical to today."** The 2026-10-20 documents say Critical mode changes nothing about a run. With D261 every run's prompts now carry the level's persona, **including Critical's**, so the claim holds for **the findings policy and readiness** and no longer for the prompt bytes. *Recommendation:* restate every such claim as "Critical's findings policy and readiness are unchanged" (the overlay plan lists the sentences). Confirm.
2. **Persona versus the guardrails of POC.** The POC persona says "no error handling, no validation, no tests", with two hard guardrails (no real secrets, nothing destructive to shared resources). The repository's own safety rules (no secret-shaped literals, no live cloud action) are stronger than a persona's wording; the persona cannot lower them, and they are listed in the plan's conventions as outside every level. Confirm that is the intent.
3. **A level at or below a floor changes nothing at the floor.** Hotfix lowers review depth by design, so a Hotfix run on a floor path (D18's security tier) stays refused by D8/D12's gating; the owner may want an explicit "hotfix may go below a floor with two approvals" rule. Not decided here.

## UNVERIFIED

- U1. Whether the persona texts fit the prompt size budgets of `SystemPrompt` and `ReviewPrompt` (the code was not run).
- U2. The exact files Slice 1 touched on the branch for the rename: the plan lists them by `git diff --stat origin/main...origin/rigor-modes`, not by name.
- U3. Whether the recipe schema has an existing conditional construct a `rigor` condition can extend.
- U4. Whether a new end reason (D268) needs a version bump of the run record (`runstore.Record`) and the console's schema; the plan makes that task's red step check it.
