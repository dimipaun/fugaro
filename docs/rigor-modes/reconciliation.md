# Reconciliation — rigor modes against the tree

**Status: approved to build on the branch `rigor-modes`; the owner approval gate is passed.**

The owner's [spec](spec.md) was written without seeing this repository. This
document is what the tree says back: every claim the spec makes, checked against
the code that would have to carry it, with the consequence for the design. The
[implementation plan](implementation-plan.md) is written from the findings here.

## 1. Checked against, and how

- Base: the commit this document was read at is `1d325928`
  (`1d32592836a089cce2d72ff53e29116a00e1c433`, the branch point of this work),
  and the running tree is the one the reader has.
- Method: read the packages named below, then run the command in §4's census
  for every count. A claim that a symbol exists is a claim the reader can check
  with `git grep`; a claim about behaviour is a claim about a named function, and
  the function is named so the reader can read it rather than trust this file.
- **No line numbers anywhere in this folder.** A line number is wrong the first
  time anyone edits the file above it; a path and a symbol survive.
- This folder's pin test is `internal/cli/docs_rigor_modes_test.go`. It holds the
  status line, the numbering of the findings, decisions and items below, the
  census's commands, and the plan's own claims about itself.

Nothing was changed in this repository to write these documents: no code, no
config, no schema, no workflow. The several files this reconciliation says must
change are the *plan's* files, not this commit's.

### 1.1 The eleven things the documents settle, and where

| The spec's question | Settled in |
| --- | --- |
| The score model and the per-mode thresholds | §5, D1–D4, plan T2.1–T2.4 |
| Where mode config lives, and precedence | §5, D5, D6, D8, D20, D21, D25, plan T1.1–T1.6 |
| What each mode changes in the run loop | D7, D10, D11, plan T3.1–T3.5, T5.4 |
| The backlog, and where a tolerated finding goes | D9, D26–D32, plan T3.3, T3.4, T4.1–T4.7 |
| Hotfix | §5, D12–D15, plan T5.1–T5.5 |
| Cost accounting | D16, D17, plan T6.1–T6.3 |
| Security | §6, D18, plan T1.5, T3.5, T5.3 |
| Tests and the live checks the owner runs | §7, plan T8.1–T8.3 |
| Migration and the CLI/doc surface | D19, plan T1.6, T7.1–T7.3, T8.3 |
| The scope ruling of 2026-10-20: project, repo, area | §1.2, F35–F41, D20–D25, plan T1.1–T1.7 |
| The backlog ruling of 2026-10-20: one Markdown file per item, and one backlog | §1.3, F42–F48, D26–D32, plan T4.3–T4.7 |

### 1.2 The scope ruling of 2026-10-20, and where each of its five parts lands

The owner ruled that the rigor mode "has a default per PROJECT, overridable per
REPO, and then overridable DYNAMICALLY per AREA". This is §1.1's tenth row: the
ruling changes where the mode's value comes from, and nothing else in this folder
— the score, the run loop, the backlog, Hotfix and cost are untouched by it. The
five parts, and where each lands — with the plan re-cut the launcher asked for
last, since that one is a plan matter rather than a design one:

| The ruling's part | Where it lands |
| --- | --- |
| Precedence `area > repo > project`, one resolver, one place that states it | D6, D20; the statement is `docs/project-layer.md` §2's own rule, one rank longer (F37), and the resolver is `internal/rigor`'s (plan T1.1) |
| Where each layer lives, and what an area is | D20, D21; the project layer's closed `defaults:` (F36), the repository's `rigor:` block, and `rigor.areas` as `{name, match, mode}` `doublestar` rules (F9) |
| What "dynamically" means | D22: chosen per run from the files it touches — declared at bootstrap where the caller knows them, measured from `runstore.Record.ChangedFiles` as they become known, monotone within the run (F38, F39) |
| The resolved mode, and which layer won, in the run, PR and watch output | D24: the record (`runview.Row`, `fugaro runs ls`, `fugaro diagnose`), the PR body's status section (`internal/runner/prflow.go`'s `finalSection`), and `fugaro watch`'s live row, which costs a registry child (F40) |
| A rule for a run that touches several areas | D23: the strictest matched area wins and every matched area is recorded, in the order strictest first |
| (The re-cut the ruling asks for, which is a plan matter and not a design one) | §2 of the [plan](implementation-plan.md): Slice 1's T1.2, T1.3 and T1.4, in that order |

Two things the ruling does not change: the spec stays verbatim (its own commit,
`37ea0d9d`), and the answers are written where the recommendations were rather
than in the spec. Every one of the six decisions the ruling raised is **answered**
(D20–D25), with the answer in place of the recommendation and the recommendation
kept beneath it as the displaced alternative.

### 1.3 The backlog ruling of 2026-10-20, and where each of its parts lands

The owner ruled that where the rigor modes have to save or file tickets for the
backlog, the backlog is maintained as **Markdown files in the repository under
`docs/`**: one file per item, plus a generated index that a small tool regenerates
and a test pins as current, so that parallel pull requests filing tickets never
conflict on one file. This is §1.1's eleventh row. The ruling is settled and the seven decisions below
(D26–D32) are **answered** by it, each with the recommendation it displaced kept
beneath the answer as the alternative; **D29**'s designated repository is the one
value in the block still the owner's to name. The parts, and where each lands:

| The ruling's part | Where it lands |
| --- | --- |
| One file per item under `docs/backlog/`, named `<id>-<slug>.md` | D26; the folder, the format and the first items are plan T4.3 |
| The front matter — id, title, mode, area, status, filed-by, filed-in, date | D27, which names where each value comes from in the record today (F47); plan T4.3 |
| Area and mode tags follow the project / repo / area scoping | D27 for the value; §1.2, D20–D22 for where the value comes from; F47 |
| A generated index (`docs/backlog/README.md`) a small tool regenerates, and a test pins as current | D28; the tool and its **two** pins are plan T4.4 (F45) |
| Project-level items live in one designated repo, named by the project config | D29 — the owner's own question, recommendation first; F42, F43 |
| How each mode files | D30: POC none or a line, Startup and Pro one item per tolerated finding, Critical none of its own, Hotfix always one for what it deferred; plan T4.6 |
| How an item is closed | D31: the pull request that fixes it flips `status` and regenerates the index in the same commit; plan T4.7 |
| The relation to the deferred-leftover ledger practice, so there is one backlog and not two | D32 and F46; the practice sentence, and the sweep, are plan T4.7 |
| The plan part for the format, the index tool and the filing command, each mergeable alone | the plan's Slice 4, re-cut into T4.3 (the format and the first items), T4.4 (the index and its pins), T4.5 (the filing command and the run that files), T4.6 (how each mode files) and T4.7 (closing, and the one-backlog practice) |

Two things the ruling does not change: the spec stays verbatim (its own commit,
`37ea0d9d`), and the answers are written here rather than in the spec. D26–D32 are
**answered** by the ruling's own words — one value excepted, **D29**'s designated
repository, which is the owner's to name and is recorded as the open value rather
than as a recommendation this folder takes.

## 2. Corrections to the premises this task was given

Five things stated in the brief, or plainly implied by it, are not what the tree
says. They are listed first because every one of them moves work: a design that
assumed any of them would be designing against a repository that does not exist.

**C1 — There is no `review:` configuration block, and one is pinned absent.**
`fugaro.yaml` has nine top-level keys — `version`, `project`, `gcp_project`,
`profile`, `git`, `agent`, `budget`, `followup`, `workflows` (the list is
`schemas/fugaro.schema.json`'s own `properties`, and it is closed:
`additionalProperties: false`). The reviewer is configured under `agent`
(`agent.models.reviewer`, `agent.review_rounds`, `agent.first_line_review`). A
`review:` block is not merely absent: `internal/config/docs_scope_test.go`'s
`TestScopeTableMatchesDocs` and the corpus case `testdata/config/invalid/review-allow-forks.yaml`
(a file nothing in Go names — `internal/config/corpus_test.go` globs the
directory) are fixtures that would go red if one appeared. So the reconciliation of "the
`review` configuration" is that the reviewer's knobs are under `agent`, and that
the word `review` at the top level of `fugaro.yaml` is taken by a refusal
fixture, not by a feature.

**C2 — `blocker`, `high`, `medium`, `low`, `nit` are not this repository's
vocabulary.** `runner.VerdictSchema` types `severity` as `{"type":"string"}`
with no `enum`; `runner.Finding.Severity` is a `string`; and the only
severity literal anywhere in Go is `"blocker"`, in `runner.ParseVerdict`'s
unparseable-verdict fallback. `high` and `low` appear only in test fixtures.
`blocker|major|minor` appears in the in-job-review spec's prose, which is also
not code. So the brief's four-word ladder is a *proposed* vocabulary, and the
design has to say where it comes from — it cannot be "kept", because it is not
there (D2).

**C3 — The owner's operating practice is not in `docs/coordinator/`.** The
brief points at that folder for the merge-then-follow-up rule, the deferred
ledger and the independent-review requirement. The three sentences are real, and
they live elsewhere:

- "A run that ends as a failed draft only because the excluded or unrunnable
  parts failed can still be fine: check the PR's CI, mark it ready and merge"
  and "CI is the release gate, not the merge gate" and "The in-run verify is a
  smoke check, not the gate" are `docs/dogfooding.md`.
- "anything that handles money or security" goes through a local session, and
  "the in-loop reviewer is a cheap model too, so its `ship` verdict is weak … A
  run's own `ship` never replaces it", are `docs/dogfooding.md` as well.
- SECURITY-SENSITIVE → opus plus a mandatory independent review is
  `docs/credentials-service/implementation-plan.md`, Global Constraint 10.
- The deferred-items pattern is `docs/plans/2026-10-13-project-secrets.md` and
  `docs/plans/2026-10-17-managed-projects-review-follow-ups.md`.

`docs/coordinator/` documents the coordinator surface, not the merge practice.
This matters for the design because the modes are a *formalisation of practice
the owner already wrote down*, and the practice's home is what the plan's
documents have to cite when a reviewer asks why a mode is allowed to do
something.

**C4 — "today a run ends failed when its own review rounds leave findings" is
true, and the precise shape of it is load-bearing.** `runner.Decide` returns
`false` with the reason `review round N still has M findings` when the last
review's verdict is not `ship`; the caller turns that into `runstore.StatusFailed`
with `runstore.OutcomeDraft`. The PR still exists, as a draft, with the run's
body and its status section. Two consequences the spec does not have: the
findings *count* survives in `runstore.ReviewSummary`, the findings themselves do
not (F20); and "failed" here does not mean "nothing was produced", which is what
makes the backlog carrier cheap (D9).

**C5 — There is no `CLAUDE.md` and no `.claude/rules/`.** `.claude/` holds one
skill (`skills/new-release/SKILL.md`). This repository's conventions live in
`docs/` — written, and pinned by tests. So the plan cannot say "follow the
repository rules"; it has to state the rules it follows, as
`docs/setup-wizard/implementation-plan.md` does in its "Conventions every task
follows" list, which the plan below copies.

## 3. Findings

Each finding is one claim about the tree, its class, and what it does to the
design. The classes are `Wrong` (a premise that does not hold), `Exists` (the
machinery is already there and the design should use it rather than build a
second), `Gap` (the machinery is absent and has to be built), `Rule` (an
existing invariant the design must not break) and `Open` (a question the tree
does not answer and the owner must).

**F1 — A finding's severity is a free-form string with no enum and no
rule.** Class: **Wrong**.
`runner.VerdictSchema` declares `severity` as `{"type":"string"}`; `runner.Finding`
carries it as a plain `string`; and **no rule branches on it** — §4's census
counts **zero** sites that compare it to a value, `runner.Decide` never sees it,
and `runner.prflow`'s `reviewsPart` does not print it. The places that do read it
render it as a label and stop: `internal/runner/prompts.go` (into the fix prompt),
`internal/runner/reviewmode.go` and `internal/cli/diagnose.go`. Consequence: the
spec's premise "severity acts as an override" has no attachment point today. The score model arrives as new fields,
and `severity` keeps its current role — a human-readable label on a row — rather
than being promoted into a rule (D2, D3).

**F2 — Every finding blocks equally, and the rule that does it is one
comparison.** Class: **Wrong**.
`runner.Decide` branches on `last.Verdict != "ship"` and on `last.Findings` only
for the reason string. `runstore.ReviewSummary` is `{Round, Tier, Verdict,
Findings int, Bounce}` — a count, not a list. Consequence: a threshold table
cannot be implemented by teaching a reader to skip findings, because there is no
place in the readiness rule where a finding is examined individually. The mode's
policy has to be applied where the verdict is turned into rounds and readiness
(D7).

**F3 — The one Go-authored severity is a fail-closed fallback.** Class: **Rule**.
`runner.ParseVerdict` returns `Verdict{Verdict: "changes", Findings: []Finding{{Severity: "blocker", Summary: "review produced no parseable verdict"}}}`
when there is no structured verdict and no fenced JSON block. `runner.TryVerdict`
and `decodeVerdict` also reject any verdict that is not exactly `ship` or
`changes`. Consequence: an unscored or unparseable finding must keep failing
closed in every mode. A mode cannot be a way to get a run past a reviewer that
broke: this is the existing guarantee, and the design preserves it explicitly
(D4).

**F4 — Readiness is one rule, stated as not configurable.** Class: **Rule**.
`runner.Decide` requires both a clean-tree `verify.KindTest` record on the final
commit (`latestVerifiedTest`, `ReasonNoVerifiedTest`) and a last review whose
verdict is `ship`. `docs/recipes.md` states the same from the other side:
"Readiness is unchanged and not configurable … Anything else is a draft."
Consequence: no mode can be implemented by making `Decide` disagree with the
verdict. Every mode below Critical still has to *reach* a `ship` — by fixing what
the mode says must be fixed and recording what it tolerates — or the run ends as
today's failed draft. Where the tolerated findings go instead is the whole of D7
and D9.

**F5 — The failed-draft path is the backlog's natural trigger.** Class: **Exists**.
The run that ends with findings already ends with `StatusFailed`, `OutcomeDraft`
and a reason naming the round and the count (`runner.Decide` and `runner.Plural`).
Consequence: the backlog needs no new terminal state; it needs the *content*
that is currently thrown away (F20) plus a place to put it (D9).

**F6 — Review rounds are bounded, and three inputs set the bound with a stated
precedence.** Class: **Exists**.
`internal/recipe/recipe.go` holds `MaxReviewRounds = 10` and
`MaxFirstLineRounds = 3` (`internal/config/config.go` holds the second of the
two as well, for the config's own bound);
`fugaro.yaml` sets `agent.review_rounds` (range 0..10, 0 or unset being the
resolved mode's row); `internal/task/task.go`'s `Spec.Overrides.ReviewRounds`
wins over it; a recipe's `max_rounds` participates through `planOf`.
`docs/recipes.md` documents the precedence. Consequence: "max fix rounds" per mode
must say which of these it sets. *O3 is answered* (2026-10-31): a mode sets a
**default** and not a ceiling — the four inputs above still win, and an unset knob
takes the mode's row — so the recommendation in the sentence before this one is
superseded (D7, plan T3.2).

**F7 — Mode config cannot reuse a `review:` block, and adding a top-level key is
five edits.** Class: **Wrong** (see C1) — and the five edits are the Rule part.
`internal/config/config.go` (the struct), `internal/config/defaults.go`,
`internal/config/scope.go`'s `Scopes` table (pinned by `TestScopeTableMatchesDocs`
in `internal/config/docs_scope_test.go`, which holds `docs/project-layer.md` in
step; `docs/project-layer.md` itself is read by
`internal/cli/docs_project_layer_test.go`), `schemas/fugaro.schema.json`
(hand-written, `additionalProperties: false`, no generator) and a corpus case
under `testdata/config/valid/`, which `internal/config/corpus_test.go` picks up
by globbing the directory — so the corpus file and nothing else is enough to add
a case.
Consequence: the config slice is one commit with all five, with its own red step;
a schema-only or struct-only change is not mergeable and would go red in the
corpus tests rather than in review (plan T1.3).

**F8 — Configuration is layered, and the layering is a named function.** Class:
**Exists**.
`internal/config/resolve.go`'s `Resolve` merges project-layer defaults → repo top
level → workflow profile → repo workflow. The project layer is
`config.LayerKey = "fugaro/project-layer.yaml"`; the published policy is
`config/policy/shared` (`fugaro project policy publish`, version 4);
`internal/projectpolicy` reads and writes it. Consequence: the spec's "a project
has a default mode" and "paths can override it" are two different inputs — the
repository's `fugaro.yaml` and the project's published policy — and the design
must say which carries the default and which carries the floors, or the same
value will end up in two places with two precedence stories (D5, D6).

**F9 — Paths already have a glob facility, and it is `doublestar/v4`.** Class:
**Exists**.
`internal/plan/footprint.go`'s `Footprint.Matches`, `internal/imagecheck/triggers.go`'s
`matchGlob`, `internal/config/validate.go`'s `doublestar.ValidatePattern` and
`internal/verify/reports.go`'s `matchAny` all use `github.com/bmatcuk/doublestar/v4`.
Consequence: a path rule reuses this and validates its patterns the way config
already validates them. A second glob dialect in the same repository is how the
"which glob matched?" bug gets in (D6).

**F10 — `internal/projectpolicy/paths.go`'s `PathRulesMarker` and `PathRepo` are
RTDB node paths, not filesystem globs.** Class: **Wrong**.
The word "path" appears in both, and the file is the first hit for a search for
path rules in the policy area. Consequence: name this in the design so the
implementer does not attach mode floors to database node paths. It is a
one-sentence finding whose absence costs a reviewer a round.

**F11 — The changed paths of a run are already recorded.** Class: **Exists**.
`internal/runstore/changedfiles.go` collects them and `runstore.Record.ChangedFiles`
keeps them. Consequence: "the strictest floor wins when a diff touches several
paths" is computable — but only *after* the work is done, while the mode has to
be chosen *before*. So the design must state both halves: the mode is resolved at
bootstrap, and it is re-checked against `ChangedFiles` before the PR is marked
ready, with a path that turns out to be floored raising the mode and the run
saying so (D6).

**F12 — The protected path today is enforced by refusing a push.** Class: **Rule**.
`internal/gitops/rejected.go`'s `WorkflowDir = ".github/workflows/"` and
`internal/runner/refused.go`'s `workflowGuard` / `workflowRefusal` refuse the
push, and the run ends `infra_error`. The App never holds the Workflows
permission. Consequence: a mode may never be a route around a refusal. The mode
check runs beside the guard and the guard stays unconditional — no mode, no
config value and no comment reaches it (D18, §6).

**F13 — Trust is one list, resolved live, and it is base-branch-scoped.** Class:
**Exists**.
`internal/followup/trust.go`'s `Trust`, `NewTrust` and `Allows`: membership in
`followup.trusted` (in the base branch's `fugaro.yaml`, repo-only scope) *and* a
live `Collaborator` check on GitHub's `author_association`
(OWNER/MEMBER/COLLABORATOR; always true on Bitbucket), and never the PR's own
author. `followup.Select` chooses which comment becomes work, and the
nonce-delimited block it reads is built by `internal/followup/prompt.go`;
`followup.allow_public` is the owner's explicit 2026-10-04 decision. Consequence:
"who may lower a floor" and "who may invoke Hotfix" reuse this list rather than
growing a second one, and because it is read from the base branch it is
unreachable from a fork (D8, D12).

**F14 — A follow-up is not a fresh task.** Class: **Rule**.
`fugaro run --pr N` selects a trusted comment and continues an existing PR;
`followup.Select` is the only way a comment becomes work. Consequence: a
follow-up's mode is the PR's recorded mode, and the follow-up path must be
unable to lower it. `--mode` on a follow-up may only raise, and the design says
so where the flag is defined rather than in prose (D6, plan T5.2).

**F15 — The budget is enforced in two places and the lower one wins.** Class:
**Exists**.
`budget.per_run_usd` is frozen into the job at deploy as `FUGARO_MAX_RUN_USD`
(`internal/infra/spec.go`'s `budgetEnv`); the effective cap is the lower of that
and the database's (`internal/runner/policy.go`, `internal/budget/lease.go`);
`budget.Evaluate` orders kills → per-run → repo daily → global daily, and
`HaltRunCap` is the kill. Consequence: a mode's cost knob can only move *below*
the deployed cap. A mode that "raises the budget" is not implementable, which is
the right answer — and the plan says it in those words rather than leaving it to
be discovered (D16).

**F16 — `agent.max_budget_usd` is not a cost estimate.** Class: **Rule**.
`fugaro.yaml` says it in its own comment: Claude Code prices a non-Anthropic model
at Sonnet-like rates, about 25× what the gateway charges for DeepSeek, so the
value is set high on purpose and the gateway's per-run cap is the real stop.
Consequence: a mode's *expected* cost cannot be expressed through
`agent.max_budget_usd`, and any document that pretends otherwise is wrong by
25×. The plan's cost section is written against the run record's `Budget` and the
gateway's prices, not against this number (D16).

**F17 — `fugaro budget preview` is a ceiling, not a forecast, and says so.**
Class: **Rule**.
`internal/cli/budget_preview.go` computes `N × min(per-run cap, stages_max(recipe) ×
agent.max_budget_usd)` using `recipe.StageBound`, labels the compute part NOT
CAPPED, and prints a `typical:` line only from at least five real-cost runs in
thirty days (oauth excluded). Consequence: the modes cannot be sold as "the
preview tells you what a mode costs." What the preview can honestly take is the
mode's recipe, because that changes `StageBound`; what it cannot take is a
per-mode forecast (D17).

**F18 — `fugaro diagnose` already reads findings back out of a stored
transcript.** Class: **Exists**.
`internal/cli/diagnose.go` recovers `Diagnosis.Findings []runner.Finding` by
re-parsing the stored senior review transcript (`transcriptResult`, `seniorRound`)
and prints `[severity] file: summary`, or JSON through `writeJSON`
(`internal/cli/budget.go`); the desktop console renders that row
(`console/docs/json-contract.md` §6.1). `runs ls --json` and `watch --json` carry
no findings. Consequence: the backlog's first carrier can be `fugaro diagnose`
essentially unchanged, *if* the findings are persisted (F19) — and a richer
finding row is a console-contract change, which is why the plan keeps the row's
shape and spends the new fields in one place (D9, plan T3.4).

**F19 — The run record keeps a count, not the findings.** Class: **Gap**.
`runstore.ReviewSummary` carries `Findings int`; `runstore.Record` carries
`Reviews []ReviewSummary` and `ChangedFiles`, and no finding objects. The
transcript is the only place they survive, which is why `diagnose` re-parses it.
Consequence: a backlog of things the mode decided not to fix needs the findings
persisted. `docs/in-job-review/reconciliation.md` Q8 already decided the
principle for the verdict ("persist the findings themselves (not just the count)
in `result.json`"); this finding is that the *rigor* mode needs the same thing
for a different reason, and the two should land as one change rather than two.

**F20 — The result schema is closed, and a test holds it in step with the
record.** Class: **Rule**.
`schemas/result.schema.json` is `additionalProperties: false` with
`version: {const: 1}`; `schemas/schemas_test.go`'s
`TestResultSchemaAcceptsRunnerRecords` marshals a fully-populated
`runstore.Record` and validates it. Consequence: any new record field lands with
its schema entry in the same commit. The record is a pinned tuple, and the pin is
a test a task can run — which is what makes this a plan step rather than a hope
(plan T3.4).

**F21 — The record's `version` is a const no reader refuses on.** Class: **Open**.
`internal/runstore/runstore.go` decodes with plain `json.Unmarshal`, so unknown
fields are ignored by an older reader; nothing in the package compares `version`
against a supported value. Consequence: whether an additive field bumps the const
is genuinely open. The recommendation is to keep `1` and say why in the commit
that adds the field (D9's note), because a const that nothing refuses on is
documentation, and a bump that no reader honours is a compatibility claim nobody
enforces.

**F22 — The task file's `mode` key is occupied.** Class: **Wrong**.
`internal/task/task.go`'s `Spec.Mode` is the *recipe* mode — validated to
`"" | implement | review | artifact` — and `task.Parse` uses
`DisallowUnknownFields`, so a task file that spells a rigor mode `mode: hotfix`
fails to parse today, loudly. Consequence: the task asks for a rigor mode under
a *different* key; `rigor` is the recommendation. Reusing `mode` would silently
change what every existing task file means, and the parser's strictness is what
turns that from a data bug into a parse error (D19, plan T5.1).

**F23 — A new task field needs an image-skew gate.** Class: **Rule**.
`internal/cli/recipes_skew.go`'s `…Since` constants and `checkImageSince` gate a
task field on the image that understands it; the remedy printed to the user is
`fugaro image refresh`. There were eleven such constants when this was written; the census (§4) counts them now, `rigorSince` among them, which gates `fugaro.yaml`'s `rigor:` block and the project layer's `defaults.rigor` on the base image in `fugaro update --repo` — not the task field; since 0.11.1 `fugaro run` gates the launch on it as well (`checkRigorImage`, `checkCloneRigorImage`), but only from a checkout of the repository, so outside a checkout only `fugaro update --repo` checks. **The task field is built since 0.11.1** (plan T5.1): `task.Spec.Rigor` and `task.Spec.Paths` carry `fugaro run --mode` and `--path` into the run, and `rigorRequestSince` gates a launch that would set either on the job image whose runner reads them (`checkImageSince` for a built image, `checkCloneImageSince` for a `checkout: clone` workflow), because `task.Parse` decodes with `DisallowUnknownFields` and an older runner refuses the whole spec at bootstrap. Consequence: the task
file's `rigor:` key cannot ship before the image that validates it, and the plan
orders the two in one slice with the skew test as its verification (plan T5.1).

**F24 — The recipe loop cannot express "this finding is tolerated".** Class:
**Gap**.
`internal/recipe` parses steps of kind `first_line`, `review`, `check`,
`digest`, `plan`, `render`; `recipe.Parse` *refuses* `goto`, `on_reject`,
`extends`, `on_pass`, `on_fail`, `model`, `models` as reserved keys; the catalog
holds nine recipes
(`default`, `solo`, `standard`, `premium`, `review-only`, `pick`, `test-and-fix`,
`lint-fix`, `run-digest`). Nothing in a recipe sees a finding. Consequence: the
modes are not five new catalog recipes, and shipping them as five YAML files
would be a feature that looks implemented and changes nothing. The mode selects
a recipe for its *check* gate and a policy for its *verdict*; the policy is Go
(plan T2.1, T3.1).

**F25 — The one required `review` step is a format rule.** Class: **Rule**.
`internal/recipe`'s parse and mode rules, and `docs/recipes.md` (`review-only`
exists precisely because a recipe's review step is what makes it a review), mean
a mode cannot be implemented as "the recipe has no review step". Consequence: a
mode changes the reviewer's *depth* and its *policy*, not its presence. The one
exception is the owner's to take (D7's alternative), and the plan does not assume
it.

**F26 — The `check` gate is what a mode can actually widen or narrow.** Class:
**Exists**.
`internal/runner/gate.go`: a clean gate ends the run as succeeded with outcome
`none` before any PR exists, and a fixed gate goes on to review as usual.
Consequence: "POC — happy path only" lands concretely on the recipe's `check`
command set, selected by the mode. It is also the reason a mode cannot switch the
*verify stage* off: `runner.Decide` requires a clean-tree test record on the
final commit, and a run without one ends failed (F4, D10).

**F27 — The in-run verify is a smoke check, and the excluded packages are
listed.** Class: **Rule**.
`fugaro.yaml`'s `workflows.go.commands.test` runs `go test` over every package
except `internal/runner`, `internal/e2e`, `internal/image` and `fugaro/images`,
inside a 25-minute verify timeout, with the comment that CI on `main`
(`go test -race ./...`, the docker-tests, rules and terraform jobs) is the real
gate. Consequence: a mode's "which tests" knob is bounded by what the container
can run at all. The plan says the mode does not change that list, and for Hotfix
it says the post-merge full suite *is* that existing CI — no new workflow is
needed, which is what makes Hotfix's most expensive-sounding requirement the
cheapest one to satisfy (D13). It is also the constraint that keeps a plan from
proposing a `.github/workflows/` change this run is forbidden to make (§8).

**F28 — The PR body already carries a per-round review status.** Class: **Exists**.
`internal/runner/prflow.go`'s `finalSection` and `reviewsPart` write a status
section, including `"<tier> review round N: <M findings>"`, on top of the agent's
own `pr.md`. Consequence: "left open by policy" is a new line in an existing
section, not a new document, and the mode is recorded in the same section with
the same words the run record uses — which is what §6's audit requirement needs
(D11, D18).

**F29 — Draft/ready is set once, and labels and reviewers follow it.** Class:
**Rule**.
`internal/runner/runner.go`'s `finalize` builds `gitprov.PRSpec{Draft: !ready}`
and calls `settlePR`; `internal/runner/prflow.go`'s `applyReady` applies labels
and reviewers *only* for a non-draft PR. There is no merge call in `gitprov`. Consequence: Hotfix's "ship immediately"
cannot mean a merged PR — the provider interface has no merge path and the tool
never deploys. It means a **ready** PR plus the operator's own merge, and
`applyReady`'s behaviour is what makes labels and reviewers arrive (D11, D13).

**F30 — The owner's operating practice is the manual version of the modes.**
Class: **Exists**.
`docs/dogfooding.md`: "Merge in dependency order"; "A run that ends as a failed
draft only because the excluded or unrunnable parts failed can still be fine:
check the PR's CI, mark it ready and merge"; "CI is the release gate, not the
merge gate"; "The in-run verify is a smoke check, not the gate"; "a task that
touches `.github/` is done by a person or a local session, never by a run"; "The
in-loop reviewer is a cheap model too, so its `ship` verdict is weak … A run's own
`ship` never replaces it"; "anything that handles money or security" goes through
a local session. Consequence: this feature formalises sentences the owner already
wrote, and the design *quotes* them, so a mode that contradicts one is caught in
review rather than in production. This document is where the quotes are written
down with a finding number each (T0.1 is the task that produced it), and T7.2 is
the task that puts the mode that automates each practice beside its sentence in
`docs/dogfooding.md` itself, with a pin of its own so the mapping cannot quietly
rot. **One of these sentences is read correctly only with a correction** (D11):
"check the PR's CI, mark it ready and merge" describes a person salvaging a
failed draft *by hand* after the fact, not the run path — a run opens its pull
request as a draft and **the runner** marks it ready if and only if `runner.Decide`
says so, and the mode changes only the policy `Decide` is given. No person marks
a run's pull request ready, in any mode, Hotfix included.

**F31 — Independent review for security-sensitive work is already written down.**
Class: **Exists**.
`docs/credentials-service/implementation-plan.md` Global Constraint 10
(SECURITY-SENSITIVE → opus, plus a mandatory independent review), and
`docs/dogfooding.md`'s money-and-security rule. Consequence: Critical's
"independent review" requirement is not a new invention, and the mode table cites
the constraint instead of restating it. It also means Critical is already
*partly* the default for that tier by policy — and D18's floors are what keeps
that tier where the policy already puts it once D19's answer makes Pro the default
a repository gets: a credential path, an IAM path or `.github/workflows/` runs at
Critical whatever a repository's default says.

**F32 — Unfixed work already has a recording pattern.** Class: **Exists**.
`docs/plans/2026-10-13-project-secrets.md` and
`docs/plans/2026-10-17-managed-projects-review-follow-ups.md` are the
"deferred review items" pattern. Consequence: the backlog's second carrier — a
`docs/`-style ledger — has a precedent to copy, which is what makes deferring it
to a later slice a defensible ordering rather than a hole in the first cut (D9).

**F33 — The neighbouring questions were already answered once.** Class: **Rule**.
`docs/in-job-review/reconciliation.md` decides Q1–Q17 (2026-10-19): Q5 keeps
`Decide`'s rule, Q7 puts one `verdict` object on `runstore.Record` with the
schema's `version` bumped, Q8 persists the findings themselves, Q9 puts no Opus
reviewer in the first slice, Q13 has the in-loop reviewer precede the owner's
review for ordinary work and never for security-sensitive work, and Q17 makes
the diff-path check deterministic. Consequence: the rigor modes must not
contradict an answered question. Where the spec's intent pushes against one — an
Opus reviewer for Critical is Q9's deferral, and a machine-decided readiness for
the security tier is Q13's — the reconciliation says so and puts it to the owner
as a decision or an item rather than assuming the merge (D2, O2, O3).

**F34 — Eight findings were rejected during this reconciliation.** Class:
**Rule** — the rule being that this list is what survived, and what did not is
recorded so a reader does not re-derive it. Dropped for being unverifiable from
the tree: that the reviewer prompt can be made to score reliably (U1); that a
score changes a model's behaviour at all; that `agent.review_rounds`'s then-default of
2 is a policy choice rather than a latency one; that the console can render a
scored row without a contract change (U7); that any workflow reacts to a merge
(U5); that a Hotfix PR's ready state survives `settlePR` on a provider other than
GitHub (U4); that a narrower `check` gate fits the verify timeout (U8); and that
the gateway's prices make POC cheaper in fact (U2). Six of those eight are the
UNVERIFIED table's, which is the point: an unverifiable claim is an item, not a
finding.

**F35 — A first run reads its configuration from the ref it checks out, not
from the base branch.** Class: **Wrong** — of D6's own premise.
`internal/runner/runner.go`'s bootstrap reads `fugaro.yaml` from the working
tree, after `CheckoutNewBranch(ctx, spec.Ref, branch)` has put that ref's tree
there; the base branch is read by `internal/runner/followup.go`'s
`readBaseConfig` alone, which fetches `origin/<ref>` and calls `repo.ShowFile`
for `fugaro.yaml`, `agent.instructions` and `agent.review`, and which a
follow-up run and a review run call and a first run does not.
`internal/runner/followup.go`'s own doc comment says why that read exists — the
base branch is read rather than the checked-out one because a review run "never
trusts a reviewed pull request's own tree at all" — and `internal/runner/runner.go`'s
`bootstrap` says the same of `fugaro.yaml` when a review run reads the working
tree. Consequence: `fugaro run --ref feature-x` resolves its mode from
`feature-x`'s own `rigor:` block unless the design says otherwise, so a branch can set the rigor of the run that is about to review it.
The ruling makes this load-bearing rather than incidental — the mode now comes
from configuration in three layers — and D25 is the decision it raises, with the
floors (D18) as the backstop either way.

**F36 — The project layer cannot carry a mode today.** Class: **Gap**.
`internal/config/layer.go`'s `LayerDefaults` is a closed struct with exactly two
members, `Git` and `Agent`, documented as "the project-wide values of the
repository keys the project layer may set (scope project)"; `Resolve` merges
`defaults:` into the repository's tree with `KnownFields(true)`, so an unknown
member is a load error rather than an ignored key. Consequence: "a default per
PROJECT" is a new member of `LayerDefaults`, a new `InProject` row in
`internal/config/scope.go`'s `Scopes`, a new line in `docs/project-layer.md`'s
printed table (pinned by `TestScopeTableMatchesDocs`), and a case in
`testdata/config/layered/` — five edits, the same "adding a top-level key is
five edits" shape F7 records for the repository's own file. The published
project *policy* is the wrong carrier for it: a new key inside the policy node is
a `projectpolicy.Version` bump, and a bump drops every policy an installation has
published until its rules are redeployed with `fugaro init --firebase-project`
(`internal/budget/rules/marker.go`) — a mode default is not worth that.

**F37 — The precedence regime exists, is stated once, and has no area rank.**
Class: **Exists**, with a gap inside it.
`docs/project-layer.md` §2's one rule is the statement: "the narrowest layer that
sets it wins: per-task flag, then repository, then the workflow's profile, then
project defaults, then Fugaro default".
`internal/config/resolve.go`'s `Resolve` is the implementation,
`internal/config/scope.go`'s `Scopes`/`ScopeRow`/`ScopeOf` is the enumeration of
which key may be set in which layer, and `TestScopeTableMatchesDocs` holds the
table and the doc in step. Two gaps: the four `Scope` bits
(`InProject|InProfile|InRepo|InOverride`) have no rank an area could be, and
`ScopeRow.Key` is a `fugaro.yaml` path in which `"*"` stands for a workflow name,
so an area is not expressible as a scope row at all — an area is a *value shape*
under one key, like `workflows`, not a layer. And §2's *sentence* is prose: only
the table is pinned, so a sentence the design leans on now would be an untested
claim. Consequence: the mode's precedence is the existing rule, one rank longer
and one layer of `only tightens` on top (the shape §2 already gives the budget
ceiling), stated in §2 and pinned by a test rather than restated here (D6, D20).

**F38 — Nothing tells a run which paths it will touch, before it touches them.**
Class: **Gap**. `task.Spec` has no path field and `task.Spec.Overrides` has
exactly four, none of them a path; `recipe.Step` has five members, none of them a
path; and `internal/plan/footprint.go`'s `Footprint` is what a planner
*predicts*, read only by `fugaro plan graph` (`internal/cli/plan.go`) and by no
runner code at all — the file says as much, calling it advisory. Consequence: at
bootstrap the files touched are the one input an area rule needs and the tree
does not have. Either the caller declares them (a new `fugaro run --path`, and a
`paths:` field in the task file later, which is F23's image-skew gate again) or
the area can only bind once the work exists. D22 takes both halves and names the
moment each one applies.

**F39 — The measured paths exist and are read at exactly one point, late.**
Class: **Exists**. `internal/runner/changedfiles.go`'s `recordChangedFiles` calls
`r.repo.ChangedFiles(ctx, r.workBase())` and writes
`runstore.Record.ChangedFiles` (`runstore.NewChangedFiles`, capped at
`MaxChangedFiles` paths of `MaxChangedPathBytes` each); it is called only from
`internal/runner/runner.go`'s `finalize`, twice. Consequence: the pre-ready
re-resolution has its input exactly where the ready-or-draft decision is made,
which is why D6's second resolution is cheap; a *mid-run* re-resolution has no
call site today, and the machinery for one already exists next door —
`internal/runner/checkpoint.go`'s `startCheckpoints` runs during a stage and
`checkpointTip` already reasons about the run's own base. D22 puts the two
moments in that order and says what each can still buy.

**F40 — A live run's mode has nowhere to travel: `fugaro watch` reads the
database, not the record.** Class: **Gap**. A live row is built by
`internal/watch/build.go`'s `runRow` from a `budget.AgentEntry` — the RTDB node
`/agents/<slug>/<run>` — and every child of that node is validated by the
generated rules in `internal/budget/rules/rules.json.tmpl`, whose entry ends with
`"$other": { ".validate": false }`, so an unknown child refuses the whole entry
rather than being ignored. `internal/budget/model.go`'s `AgentEntry` has no mode
field. The tree records two ways of adding one: `Recipe`'s, where
`internal/budget/registry.go`'s `Session.Start` drops the field on
`ErrPermissionDenied`, warns that the database's rules predate it and tells the
operator to run `fugaro init --firebase-project`, and remembers for the session
(`noRecipe`); and the creds family's, where `internal/budget/rules/marker.go`
records a marker and the run halts on a downgrade — planned when this finding
was first written, and **built since** on `integration-0.9.6` (plan task C1.17 of
`docs/credentials-service/implementation-plan.md`): `HaltCredsRulesDenied` is now
an entry of `internal/runstore`'s `HaltReason` vocabulary (`creds_rules_denied`),
beside its mentions in that file's comment, in `internal/budget/model.go` and in
`internal/projectpolicy/version.go`, so the second precedent is one this work can
read back as well as follow. Consequence: showing the mode in `fugaro watch` costs a
rules regeneration (and its golden file), a deploy the owner runs, and one of
those two paths. The other surfaces are free by comparison: `fugaro runs ls`,
`fugaro diagnose` and the console read the record through `internal/runview`'s
`Row`/`Join`, and the finished half of watch reads `result.json`
(`watch.JSONFinished`), so a run's mode is visible in watch *after* it ends
without any of this.

**F41 — Seven claims the 2026-10-20 amendment considered and rejected.** Class:
**Rule** — the rule being that what did not survive is recorded so a reader does
not re-derive it. Rejected: that the project-wide mode could live in the
published policy without a version bump (F36's reason); that `plan.Footprint` is
a usable pre-work input for an area (F38: nothing a run executes reads it); that
a live mode in `fugaro watch` is free because the record is already there (F40:
the live rows are not built from the record); that an area rule should be able to
*lower* a built-in or declared floor (D18's rule, and §6); that a follow-up run
should re-resolve its areas from the follow-up's own diff (F14: it keeps its PR's
recorded mode); that the widest matching area should win (D23's alternative, kept
as an alternative); and that "dynamically" could mean a static per-directory
config file such as `.fugaro-rigor.yaml` in the tree (a second configuration
surface with its own schema and its own trust question, and the branch that writes
it is the branch under review — the same objection F35 raises, with no base-branch
read to answer it).

**F42 — A project-wide item has no repository to live in: no project-level key names one.** Class: **Gap**. The one place a project-wide value lives is the project layer's closed `defaults:` (F36), and that closure is the reason there is nothing to point at: `internal/config/layer.go`'s `LayerDefaults` admits `git` and `agent` and nothing else, `schemas/project-layer.schema.json` pins the same set, and the local project config's `Repos` reads repositories from a file on **one machine**, not from anything in the repository. The structures that do name repositories are plural or per-account: `projectpolicy.Policy.AllowRepos`, its `CloneURLs` keyed by canonical name, and `Account.AllowRepos`. Consequence: D29's designated repository needs one new member of a closed struct, its schema entry, its scope row and one sentence in `docs/project-layer.md` — the same price F36 names for the mode's own key, and one more reason to spend it once.

**F43 — A run cannot write into a repository other than its own.** Class: **Gap**. The installation token is narrowed to exactly one repository: `internal/gitprov/github/apptoken.go`'s `tokenSource.Token` mints with `{"repositories": []string{s.repo}, "permissions": perms}`, and `MintInstallationToken` refuses without an owner and a repository. The provider contract names no repository either: `internal/gitprov/gitprov.go`'s `PRSpec` carries `Number`, `Branch`, `Base`, `Title`, `Body`, `Draft`, `Labels` and `Reviewers`, so a pull request is always opened in the client's own repository. A run works in one checkout (`internal/runner/runner.go`'s `openCheckout`, `r.d.WorkDir`), pushes one branch and opens one pull request. The narrowing is a mint parameter rather than a permission — `internal/gitprov/github/apprepos.go` mints an installation token with a permissions map and **no** `repositories` list, which is how the App lists an installation's repositories — so it is liftable, at the price of a second token, a second checkout, a second push and a second pull request. Consequence: D29 cannot recommend that a run anywhere files into the designated repository; the designated repository holds the items filed **in** it, and a cross-repo filing is a task of its own rather than a line in the filing command.

**F44 — Filing during a run writes into the branch under review.** Class: **Exists**. A run's writes are its branch's: `internal/runner/runner.go`'s `openCheckout` opens or clones the repository, `CheckoutNewBranch` then puts the task's ref's tree in that work directory, the commits the agent's stages and `finalize` make, and `internal/runner/checkpoint.go` pushes them as the run goes — a checkpoint never commits and never touches the working tree or the index, by its own doc comment — so the push at the end carries all of it into the pull request. One path is refused outright, whatever the mode: `internal/runner/refused.go`'s `workflowGuard` (F12). Consequence: the item a run files arrives as a commit in the pull request it is about, which is the honest reading of the ruling rather than a problem. The pull request is where a reader meets the deferred leftover, `filed-in:` is a number the record already holds (`runstore.Record.PR`), and the write passes the same refusal every other write does — which is free, because the backlog lives under `docs/` and `.github/workflows/` is the only refused path.

**F45 — The generated-document pattern exists, and its one trap is already written down.** Class: **Exists**. `internal/clidoc` renders `website/content/cli-reference.md` from the command tree and is invoked by `go run ./internal/clidoc`: exactly the "a small tool regenerates it" shape the ruling asks for, with the header line that tells a reader not to edit it. `internal/pricing/catalog/gencatalog` (through `scripts/gen-price-catalog.sh`) shows the other half of the discipline — a required date, sorted rows and a determinism re-run — so the generator reads no wall clock. Two guards of different kinds hold the CLI reference: `internal/clidoc/cliref_test.go`'s `TestCliReferenceIsCurrent` regenerates and compares, and `internal/cli/docs_website_cliref_test.go` derives the same claim from the command tree instead of from the generator. The trap is named in `internal/cli/docs_website_security_test.go`'s own comment: a test written against the generator agrees with whatever the generator does, which is the one thing a drift test must not do. Consequence: the backlog's index needs both kinds of guard (D28), and its generator must take every date from the item rather than from the clock.

**F46 — Deferrals are written in three places today, and none of them is a backlog.** Class: **Gap**. The `docs/plans` sections — §4's census counts **three** of them at this commit, by their headings: `docs/plans/2026-10-13-project-secrets.md`'s `## Known follow-ups before implementation …` and its `## Deferred review items (2026-10-14 fix round)`, and `docs/plans/2026-10-18-project-named-oauth-tokens.md`'s `## Known follow-ups, deliberately not in this plan` (a fourth list states the practice under a heading the pattern does not match — `docs/plans/2026-10-14-project-centric.md`'s `## Accepted as deferred`, whose lower-case `d` is why it is outside the figure, and the reason this finding names the sections rather than only citing the count); the pull request's own body, whose status section names what a run tolerated and no addressable item (F28); and the run record, where `runstore.ReviewSummary.Findings` is a **count** — the finding text survives only in the stored transcript, which `internal/cli/diagnose.go` reads back (F18). None of the three is a list a later reader can close one line of, and two of them die with the artifact that carries them: the pull request body is history the moment the pull request merges, and a review round's table is a snapshot of that round. Consequence: "one backlog and not two" is a change of practice as much as a format (D32), and the item file is the first carrier that outlives both the ephemeral job and the merged pull request.

**F47 — Every front-matter field the ruling names has a source in the record except the area.** Class: **Exists**. `runstore.Record.RunID`, `Record.PR` (`PRRef{Number, URL}`), `Record.FinishedAt`, `Record.Repo` and the launch's `LaunchedBy` (read back as `runview.Row.LaunchedBy`) give `filed-in`, `date`, `filed-by` and the repository with no new field at all; the resolved mode and the layer that won are D24's recorded value, from T1.2; and `id`, `title` and `status` are the filer's own words. The area is the one field a mixed run has no single answer for: D22's resolution names the areas that matched and D23's answer records them all, strictest first, so `area:` carries the list rather than one name. Consequence: the front matter is cheap everywhere including `area:`, which is a list rather than a choice.

**F48 — What the ruling's own shape rejects, and why.** Class: **Rule** — the rule being that what did not survive is recorded, so the next reader does not re-propose it. Rejected: that the backlog should be the provider's issues (O5's permission question is unanswered, and the ruling puts it in the repository); that one file per month, per mode or per work would do (two pull requests filing at once is the reason the ruling gives for one file per **item**, and every one of those shapes puts them in one file); that a hand-maintained index would do (a list a hand edits is a list the pin then has to accept, and the ruling asks for a generator); that an item should be filed after the merge by something scheduled (D15 deferred the scheduler, and an item exists the moment the run ends — filing it later would lose the ones whose pull request is never merged); that an item should carry the finding's text (the transcript holds it and `fugaro diagnose` reads it back, F18; the item is what to do next, not what was found); and that one ledger file per work — D9's carrier before this ruling — is enough (F46: it is the file two runs filing in the same week conflict on).

## 4. The census, and what it is scoped to

Every count below is stated with the command that produces it, because a count
without its command is a figure a later reader cannot re-derive. All commands are
run from the repository root against the working tree — and the guard,
`internal/cli/docs_rigor_modes_test.go`, re-runs every one of them there, so a
figure that has drifted from its command is a failing test rather than a stale
number. At this folder's own commit, `e5211a8a` (#773) — whose own diff is this
folder's four documents and its pin test and nothing else — one row is then the
folder's own: the pins were **48**, the 47 the base `1d325928` (§1) already held
plus this folder's `internal/cli/docs_rigor_modes_test.go`, and they are files
rather than folders, 13 of them pinning one of `docs/`'s folders and the rest a
standalone document, which is why the row above counts the pins and not the
folders they sit beside. **The figure is 71 at this branch's tree, because other
folders have written a pin since** — `main` holds 70 — among them `internal/cli/docs_gcp_setup_test.go` (#764, the WSL
route's Windows forms), `internal/cli/docs_setup_wizard_checks_test.go` (#771, the
register's Check 27), `internal/cli/docs_live_init_census_test.go` (#772, the
setup wizard's rename census), `internal/cli/docs_windows_console_test.go`
(#774, the Console on Windows, whose folder did not exist at `1d325928`),
`internal/cli/docs_mobile_release_workflow_test.go` (#777, the mobile release
workflow while it waits under `docs/pending-workflows/`) and
`internal/cli/docs_mobile_eas_runbook_test.go` (`docs/mobile/eas-signing-runbook.md`,
the EAS signing runbook of Task P4.4, whose page this repository's P4.4 refresh
rewrote against the tree of 2026-10-22), `internal/cli/docs_credentials_cascade_checks_test.go`
(#951, which `main` gained after this sentence was written),
`internal/cli/docs_batch_live_check_test.go` (#1022, the owner-run Check 124 for the
Cloud Batch backend) and `internal/cli/docs_contributing_cla_test.go` (#1031, the
contributor licence agreement), `internal/cli/docs_ee_repo_test.go` and
`internal/cli/docs_ee_reserved_test.go` (#1032, the reserved `ee/` folder), the
last four of which `main` gained before this branch merged it and are why `main`
holds 70, and on this branch
`internal/cli/docs_backlog_test.go`, which plan T4.3 wrote as the folder's own
pin on `docs/backlog/` rather than on this folder. (Amended 2026-10-29:
`internal/cli/docs_mobile_auth_test.go`, the mobile auth folder's pin that its
plan's P0 wrote, moved the table's row from 84 to 85.) They
are named because the figure is a running total of other folders' guards: a folder
that writes one moves this row in its own commit, and a reader who finds the row
stale has found a merge that did not.

| Construct | Count | Command |
| --- | --- | --- |
| Catalog recipes | 9 | `ls internal/recipe/catalog/*.yaml \| wc -l` |
| Top-level `fugaro.yaml` keys | 12 | `sed -n '/^  "properties": {/,/^  }/p' schemas/fugaro.schema.json \| grep -c '^    "[a-z_]*": '` |
| Top-level `fugaro.yaml` keys whose name contains `review` | 0 | `sed -n '/^  "properties": {/,/^  }/p' schemas/fugaro.schema.json \| grep -c '^    "[a-z_]*review[a-z_]*": '` |
| Config scope rows | 80 | `sed -n '/Scopes = \[\]ScopeRow{/,/^}/p' internal/config/scope.go \| grep -c '{Key:'` |
| Layers a configuration key may be set in | 4 | `sed -n '/The layers a key may be set in/,/^)/p' internal/config/scope.go \| grep -c 'In[A-Z]'` |
| Blocks the project layer's `defaults:` may set | 3 | `sed -n '/type LayerDefaults struct {/,/^}/p' internal/config/layer.go \| grep -c 'yaml:'` |
| Fields of `task.Spec.Overrides` | 4 | `sed -n '/type Overrides struct {/,/^}/p' internal/task/task.go \| grep -cE 'yaml:\|json:'` |
| Fields of `task.Spec` or `recipe.Step` whose name is a path | 0 | `{ sed -n '/^type Spec struct {/,/^}/p' internal/task/task.go; sed -n '/^type Step struct {/,/^}/p' internal/recipe/recipe.go; } \| grep -cE '^[[:space:]]+[A-Z][A-Za-z]*(Path\|Glob\|Dir)'` |
| Sites that compare a finding's severity to a value | 0 | `git grep -n 'Severity ==' -- internal/runner ':!*_test.go' \| wc -l` |
| Files in `internal/runner` that mention `Severity` at all | 3 | `git grep -l 'Severity' -- internal/runner ':!*_test.go' \| wc -l` |
| Image-skew `Since` constants | 23 | `git grep -hc 'Since = "' internal/cli/recipes_skew.go` |
| Run outcomes | 4 | `git grep -ho 'Outcome[A-Za-z]* *= *"[a-z]*"' internal/runstore/runstore.go \| wc -l` |
| Docs folders that ship their own `internal/cli/docs_*_test.go` pin | 91 | `ls internal/cli/docs_*_test.go \| wc -l` |
| Questions the in-job-review folder put to the owner, all answered | 17 | `git grep -hc '^\*\*Q[0-9]*\. ' docs/in-job-review/reconciliation.md` |
| Published project-policy version | 4 | `sed -n 's/^const Version = //p' internal/projectpolicy/version.go` |
| Blocks the project layer's `defaults:` may set whose value names a repository | 0 | `sed -n '/^type LayerDefaults struct {/,/^}/p' internal/config/layer.go \| grep -ci repo` |
| Fields of `gitprov.PRSpec` whose name is a repository | 0 | `sed -n '/^type PRSpec struct {/,/^}/p' internal/gitprov/gitprov.go \| grep -ci repo` |
| Sites that narrow an installation token to one repository | 1 | `git grep -hc '"repositories": \[\]string{s.repo}' internal/gitprov/github/apptoken.go` |
| `docs/plans` sections whose heading the deferral pattern matches | 3 | `git grep -hE '^## .*(Deferred\|[Ff]ollow-ups)' -- docs/plans \| wc -l` |
| Item files under `docs/backlog/` | 2 | `ls docs/backlog/[0-9]*.md 2>/dev/null \| wc -l` |

Every one of those commands prints the number in the Count column and nothing
else on stdout, so a reader — and this folder's guard — can re-run the row rather
than trust the figure. `git grep -h` is there for the same reason: without it the
command prints `file:count`, which is not the figure the row states.

Three of those rows are worth reading rather than counting.

The **zero** is F1 and F2 in one figure: `internal/runner/prompts.go` and
`internal/runner/reviewmode.go` each read `f.Severity` only to print it as a
label, and no comparison against any value exists in the package. A mode
threshold table has nothing to attach to today.

The **three** files are `verdict.go` (the schema string, the field, the
fallback), `prompts.go` (the label) and `reviewmode.go` (the label again) — that
is the entire surface a scoring change touches inside the runner, which is why D3
is as small as it is.

The four layering rows the 2026-10-20 amendment added are these: **4** layers a
key may be set in (F37), **3** blocks the project layer's closed `defaults:` may
set (F36 — two at this folder's tree, the third Slice 1's `defaults.rigor`),
**4** fields of `task.Spec.Overrides` and **0** fields of `task.Spec`
or `recipe.Step` whose name is a path (F38). The last row counts *declared
fields*, which is why its command matches a field name at the start of a line
rather than any mention: the `Spec` struct's prose mentions a path, and a count of
prose is not a count of inputs.

The five rows the backlog ruling's amendment adds are that ruling's two structural
facts and their witnesses: **0** blocks of the project layer's `defaults:` name a
repository and **0** fields of `gitprov.PRSpec` name one, while an installation
token is narrowed to a single repository in exactly **one** place (F42, F43) —
which is why D29 recommends a designated repository rather than a cross-repo
write; **3** `docs/plans` sections match the deferral headings (F46 names them,
and names the fourth list whose lower-case heading keeps it out of the figure);
and **0** item files existed under `docs/backlog/`, which was that
amendment's own claim about itself: it changed documents and nothing else. Plan
T4.3 moved that row to the two items it filed and the pin-count row above with
it, in its own commit, and the row's command counts the item files alone — a
name that starts with the id's first digit — so the folder's `FORMAT.md` and
its generated index are not items and are not counted as ones. A slice that adds
a top-level key, a scope row or a member of the project layer's `defaults:` moves
the row that counts it in the same commit for the same reason: the figure and the
command beside it are read together, and the command reads the tree the slice
leaves behind — Slice 1 moved all three (10 keys, 70 scope rows, 3 `defaults:`
blocks), which is the five-edit shape F7 and F36 price, measured.

The **67** is the convention this folder follows: there is no registry test over
`docs/`, so a folder that wants its documents pinned writes its own file, and
this folder's is `internal/cli/docs_rigor_modes_test.go` — the forty-eighth when
this folder's own commit wrote it, and one of the pins the row has counted
since. It is the row in this table that this folder's own commit moved;
`internal/cli/docs_backlog_test.go`, which plan T4.3 wrote, is the folder's own
pin on `docs/backlog/` rather than on this folder.

## 5. Decisions for the owner

**Every decision here is answered**, because the owner answered §5's decisions (D1–D32)
on 2026-10-20 — the first twenty-five each asked independently, and D26–D32 in the
backlog ruling he gave the same day, which §1.3 indexes. Each decision states the answer first,
then the recommendation as this folder weighed it with its reason, then the
alternative and what it cost — the alternatives stay because they are the record
of what was considered and priced rather than missed. **Where the answer differs
from the recommendation, the recommendation text is rewritten to the answer** and
the option this folder had recommended is kept as the alternative it displaced;
four decisions differ (D9, D11, D14, D19) and each says so in its own words.
**One value is still the owner's**: **D29**'s designated repository, which the
ruling puts in the project configuration and leaves the owner to name. Nothing
below is implemented, and the plan's slices **are approved to build on the branch
`rigor-modes`** — its OWNER APPROVAL GATE is passed — so a task that depends on a
decision takes it as settled and says so in its **Depends on** line.

### D1 — The scoring scale: 1–5 per axis, anchored

**Answered 2026-10-20: integers 1–5 on each of likelihood, impact and fix
cost, with each value anchored in the reviewer prompt.**
Why: three values cannot separate "rare but catastrophic" from "common but
cosmetic", which is the pair the threshold table exists to tell apart.

**Recommended: integers 1–5 on each of likelihood, impact and fix cost, with
each value given an anchored definition and one example in the reviewer prompt.**
Reason: three points cannot express the spec's own example ("rare but
catastrophic") against its other example ("common but cosmetic") — with three
values, one step swamps the product, so Pro's "weigh fix cost against
likelihood × severity" has nothing to weigh. Five is also the smallest scale on
which "rare" and "very rare" are distinguishable, and the spec itself proposes
1–5.

**Alternative: 1–3 per axis.** Cost: fewer calibration failures, but the
threshold table collapses to a handful of distinct cells and the security
override ends up doing all the work — at which point the scale is decoration and
the honest design is a per-mode word list. The plan's T2.2 writes the anchors at
five, which is the scale the answer above chose; at three, the table in T2.3 would
be rewritten rather than redesigned, and the security override would carry the
weight.

### D2 — What `blocker`/`high`/`medium`/`low`/`nit` are: labels, not inputs

**Answered 2026-10-20: the five words are the reviewer's derived label for a
finding, not the threshold table's input — the table reads the three axes and the
finding's class.**
Why: a word derived from the score cannot disagree with it, and the vocabulary
stays one this repository can define and a reader can check.

**Recommended: the five words are the reviewer's *label* for a finding, derived
from the axes by a stated rule, and the label is what a human reads in the PR
body and in `fugaro diagnose`. The threshold table's inputs are the three axes
and the finding's class, never the label.** Reason: the spec wants both a
readable word and a weighted decision. Deriving the word keeps one source of
truth (so a finding cannot be labelled `low` and scored 5×5), and it makes the
vocabulary answerable to the tree: today's free-form `severity` string simply
becomes the label field with a documented set of values (F1, C2).

**Alternative: keep the four words as the only input, as the brief's phrasing
implies (`blocker`/`high`/`medium`/`low`/`nit`).** Cost: the spec's own
"weigh fix cost vs likelihood × severity" is unimplementable, the security
override has nowhere to attach except a word that a model chose freely, and the
three-axis scoring section of the spec becomes prose. The answer above kept the
words as labels, so the design keeps fix cost; had it gone the other way, the
design would lose fix cost entirely and the honest place to record that would be
the spec rather than the code.

### D3 — Where the score lives: new optional properties on the finding item

**Answered 2026-10-20: new optional finding fields — `likelihood`, `impact`,
`fix_cost` and `class` — added to the finding item, with `severity` kept exactly
as it is.**
Why: the item's property set is open, so the change is additive and every stored
transcript and every verdict still parses.

**Recommended: add `likelihood`, `impact`, `fix_cost` (integers 1–5) and
`class` (a closed enum that includes `security` and `data_loss`) to the finding
item of `runner.VerdictSchema`, add the matching fields to `runner.Finding`, and
keep `severity` exactly as it is.** Reason: the item's property set is open — it
declares `properties` with `required: ["summary"]` and no
`additionalProperties: false` — so the change is additive: every stored
transcript and every existing verdict still parses, and `runner.ParseVerdict`'s
fallback keeps its meaning. It is also the smallest change that gives the
threshold table real inputs (F1).

**Alternative: replace `severity` with a nested `score: {likelihood, impact, fix_cost}` object.**
Cost: a breaking change to a schema that stored review transcripts are read
against, a change to `internal/cli/diagnose.go`'s row and to the console's
`json-contract.md` §6.1, and the loss of a field that some stored transcript
already carries. It buys tidiness and nothing else.

### D4 — An unscored finding blocks in every mode

**Answered 2026-10-20: an unscored finding blocks in every mode, POC
included.**
Why: a reviewer that stops emitting scores must not turn Critical into POC
silently, and `ParseVerdict` already fails closed.

**Recommended: fail closed.** A finding with no score, or a verdict that does not
parse, blocks in POC as much as in Critical, and the run says which finding was
unscored. Reason: `runner.ParseVerdict` already fails closed (F3), and the
owner's own note that "the in-loop reviewer is a cheap model too, so its `ship`
verdict is weak" (F30) is exactly the argument against letting a scoring failure
become leniency. Without this rule a mode is a dial that turns itself down
whenever the reviewer drifts.

**Alternative: treat an unscored finding as `medium`/severity-3.** Cost: a
reviewer that stops emitting scores silently converts Critical into Pro, and the
drift is invisible in the run record — the mode would report itself as Critical
while behaving otherwise. The failure mode is a bad patch merged with a green
audit trail, which is worse than a loud red one.

### D5 — Mode config lives in `fugaro.yaml` and in the project layer; the published policy is not a carrier

**Answered 2026-10-20: the mode is configured in the project layer's
`defaults.rigor.default` and in a top-level `rigor:` block in the repository's
`fugaro.yaml`; the published project policy is not a carrier, and a floor comes
later through the policy.**
Why: the mode is a per-project and per-repository value like every other, and a
policy key costs a version bump and a redeploy of every installation's rules.

**Amended 2026-10-20.** The ruling adds a project-wide default, and the first
recommendation below was written before it existed.

**Recommended: the mode is configured where every other key is — the project
layer's `defaults.rigor.default` for the project-wide default, and a top-level
`rigor:` block in the repository's `fugaro.yaml` for that repository's default
and its areas.** The project layer is where a per-project value belongs
(`config.LayerKey`, `internal/config/resolve.go`'s `Resolve`), and it is a
*closed* struct of two members today, so this is a new member of
`LayerDefaults` with its own scope row, doc-table line, corpus case and layer
corpus case (F36) — the five-edit shape F7 records, done at the layer rather
than in the file. The published project policy is deliberately **not** a carrier
for it: a policy key is a `projectpolicy.Version` bump, a bump drops every policy
an installation has already published until its rules are redeployed with
`fugaro init --firebase-project`, and a mode default is not worth that (`marker.go`'s
warning is the reason the rule exists).

**Recommended, part three: a *floor* the project's repositories cannot edit
belongs in the published project policy, not in the project layer.** The two are
not the same thing and the tree already distinguishes them: `docs/project-layer.md`
§2's "the budget ceiling is the owner's, outside every layer, and only tightens"
is the published policy's sentence, and the project layer is a *file in the
project's Git repository*, which a repository admitted to the project can change
by merging a pull request. So the project-wide **default** is a layer value
(part one) and a project-wide **floor** is policy — with the version bump F36
describes as its price. Where the security tier's floor lives is D18's question,
not this decision's, and D18's answer puts it outside every layer: the built-in
set plus whatever a repository declares, with the first cut shipping the built-in
set alone (T1.5) so a policy-level floor can land later without a migration.

**Recommended, part two (unchanged): the repository's own `rigor:` block carries
the mode for this repository and the area rules.** Reason: the spec asks whether
mode config lives in the project's Git repository alongside recipes, and the
answer the tree gives is "yes, and there are already two layers of it" (F8): the
repository's `fugaro.yaml` is where a mode is set for this repository, the
project layer is where it is set for every repository in the project, and
`Resolve` already defines what happens when both speak. Adding a separate
`rigor.yaml` would need its own schema, its own corpus tests, its own layer entry
and its own pin, for no capability the existing file lacks — and an area file *in
the tree* has the further problem F35 raises: the branch under review could be
the one that writes it.

**Alternative: a `rigor.yaml` beside recipes.** Cost: a second configuration file
in the same directory with a second schema and a second validation path, and the
question "which one wins for a path rule?" gets a new answer invented for a file
that did not need to exist. The one thing it buys — a file whose owner is the
mode system alone — is a real argument, and this is the decision where the owner
weighs it.

### D6 — Precedence: area > repo > project, one resolver, one statement

**Answered 2026-10-20: area, then repository, then project, then Fugaro's
own built-in default; one resolver; and the mode never falls within a run.**
Why: it is `docs/project-layer.md` §2's existing narrowest-layer-wins rule one
rank longer, so the order is stated once, in the place that already states it.

**Amended 2026-10-20.** The ruling names three layers and asks for one place that
states their order. What follows replaces this decision's earlier `max(...)`
formulation; the floors keep their role and lose their place as the only rule.

**Recommended: the mode is a configuration key like any other, so it follows
`docs/project-layer.md` §2's existing rule — the narrowest layer that sets it
wins — one rank longer: area, then repository, then project default, then
Fugaro's own default (Critical, D19). On top of that sits the only-tightens
layer the same section already gives the budget ceiling: the built-in and
declared floors are outside every layer and nothing lowers them (D8, §6). The
task's own request may only *raise* (F14's recorded behaviour for a follow-up,
kept for every run because it is the fail-closed direction).** Patterns are
`doublestar` globs validated by `internal/config/validate.go`'s
`doublestar.ValidatePattern`, matched by the one matcher the repository uses
everywhere (F9).

**Recommended, part two: it is stated once, and the statement is §2's.** The
sentence is amended there (plan T1.4) rather than written here, because §2 is
already the one place this repository states configuration precedence and a
second statement is the failure mode the ruling is guarding against. The
implementation states it once too — `internal/rigor`'s `Resolve` is the only
resolver, and no other package may order modes (§1.2 is the index of who cites
it). It is pinned on both sides: the resolver's table test holds the order, and
the doc's sentence is covered by a guard beside `TestScopeTableMatchesDocs`,
which is what F37 says is missing today.

**Recommended, part three: the resolution happens more than once, and is
monotone within a run.** D22 has the moments; the property is that a mode a run
has reached never falls for that run.

**Alternative: resolve the mode from the finished diff only.** Cost: the entire
run was done at the wrong rigor; by the time the floor is noticed the fix rounds
are spent and the choice is between discarding the run and shipping the wrong
rigor. Cheaper to code, and it makes a locked payments path a suggestion.

**Alternative: resolve once, at bootstrap, and never again.** Cost: a floor on a
path nobody declared is discovered after the work and does nothing — the mode is
recorded, the PR is ready, and the floor is a comment in a record. It is
attractive because bootstrap is the easy moment (the configuration is in hand and
no diff exists), and it is the reason D22 keeps bootstrap as one of its moments
rather than the only one.

**Amended 2026-10-22, by the levels addendum, amended after review and confirmed by the owner 2026-10-22:** a new run's own request may lower a default (never below a floor), while a follow-up stays raise-only; see [levels-reconciliation.md](levels-reconciliation.md) §3a (LD3).

### D7 — What each mode changes in the run loop

**Answered 2026-10-20: the policy lives inside `runner.Decide`; readiness
stays Decide's one rule; and the findings a mode tolerates become backlog
items.**
Why: one answer to "may this pull request be ready" is the property the owner
relies on, and a tolerated finding has to land where a reader can close it.

**Recommended: Critical *mode* is byte-identical to today — and only Critical,
because D19's answer makes Pro the mode a repository gets for configuring
nothing.** Every other mode keeps the
reviewer, keeps the one required `review` step, keeps the verify stage, and
changes exactly three things: (a) the findings policy handed to the reviewer and
applied to the verdict — the threshold table as data; (b) the review round
*ceiling*, under the existing precedence of `Overrides.ReviewRounds` >
`agent.review_rounds` > recipe `max_rounds` (`planOf`, F6), so no existing task
file changes meaning; (c) what happens to the findings the policy tolerates:
they become the run's backlog instead of the run's failure. The mechanism is
`runner.Decide` extended with the mode's policy — a tolerated set of findings
still yields a ready PR, with a reason that names the policy and the count —
rather than a caller that ignores `Decide`'s answer.

**Reason:** `Decide` is the one place readiness is decided, and `docs/recipes.md`
states that readiness is "unchanged and not configurable". Putting the policy
*inside* the rule keeps one answer to "may this PR be ready", which is the
property the owner relies on when a draft is merged by hand. It also means the
mode's effect is visible in the record: the reason string, the backlog and the
mode all come from the same decision.

**Alternative: implement POC as "the recipe has no review step".** Cost: it does
not work, and it is worse than not working — `runner.Decide` returns "no review
verdict" for a run with no review, so a review-less POC run ends as a *failed
draft*, stricter than Critical for a run that was supposed to be the cheap one.
Making it work requires changing the readiness rule itself, which contradicts
`docs/recipes.md` and F4. The one thing this alternative has going for it — a POC
run that spends nothing on review — is available inside the recommendation by
making the mode's reviewer shallow rather than absent, which is what D1 gives the
knobs for.

**Amended 2026-10-26, by the owner's ruling of that date: Hotfix has the fewest
gates of any mode, so out of the severity override its row of the threshold table
keeps the security gate alone.** A finding whose class is `security` blocks at
Hotfix whatever its axes say; a finding whose class is `data_loss` or whose impact
is 5 is tolerated there and filed as advisory by the mode's own filing row (D30),
which is where a Hotfix run's record of them belongs. Every other mode's row is
untouched — POC still blocks both classes the spec names, and an impact of 5 —
and an unscored finding still blocks in every mode: that is D4's rule, and the
ruling does not lift it. What makes a hotfix's fix *wrong* is still
not a finding at all, which is D14's answer: the deterministic diff rule gates the
run, and the agent's own confidence number is recorded and never trusted. The row
is `internal/rigor/table.go` (plan T2.3), and it carries the mode's reading of the
override as data in the row rather than testing the mode's name inside the rule.

### D8 — Who may lower a floor: the trusted list, acting through a merged config change

**Answered 2026-10-20: only a merged change to the base branch's
configuration lowers a floor — no run, no task request, no follow-up comment, no
label and no job environment variable.**
Why: the lowering then has a reviewed diff as its audit trail, and the trusted
list stays the answer to *who*.

**Recommended: the trusted list (`followup.trusted`, `internal/followup/trust.go`,
F13) is the answer to *who*, and the only mechanism that lowers a floor is a
change to the base branch's configuration — a merged pull request reviewed as a
config change. No run, no task, no follow-up comment, no label and no job
environment variable can lower a floor.** Reason: the trusted list is already the
repository's one notion of "an account whose words are obeyed", and it is
resolved live and read from the base branch, so a fork cannot reach it. Wiring
that list to a *config edit* rather than to a runtime comment keeps the lowering
inside the surface where the owner is looking: a pull request that changes
`rigor:` is reviewed by a person, and its own diff is the audit trail.

**Amended 2026-10-20: the ruling adds ranks below the trusted list's reach, and
they may lower a *default* but never a floor.** A repository may run its own
paths below the project's default — a default is not a floor, and the reason to
have one is so that a repository can be cheaper without asking anyone. What no
rank may do is reach under a floor: not a repository's `rigor:`, not an area
rule, not a task request, not a follow-up comment. A project-wide floor the
project's own repositories cannot edit is not a layer value at all; D5's part
three has it, and D18 decides whether the first cut needs one.

**Alternative: a trusted comment may lower a floor for the PR it is written on.**
Cost: it makes an untrusted-input path the way the security tier's protection
moves, on a repository whose `followup.allow_public: true` is a deliberate,
recorded risk (F13); and the lowering would not be visible in any reviewed diff,
so the audit trail is a comment on a PR rather than a change to the
configuration. It is genuinely more convenient for a hotfix that needs to touch
one floored file, and that convenience is the cost: the case it optimises is the
case where the control matters.

### D9 — The backlog: one Markdown file per item, filed in the run's own pull request

**Answered 2026-10-20, and it differs from this folder's recommendation: the
Markdown backlog under `docs/backlog/` is the **first** carrier, not the last. An
item file is written into the run's own branch and pull request and reviewed with
it, and the tolerated findings are **still** recorded on the run record, which is
cheap.**
Why: the item file is the one carrier that outlives both the ephemeral job and the
merged pull request, so it is the first thing to build rather than the thing to
defer.

**Recommended, and taken: the tolerated findings are filed as items of the
Markdown backlog — one file per item under `docs/backlog/` (D26–D32) — and that
is the first cut of this surface.** The three carriers this decision's earlier
recommendation put first stay, because each is cheap and each answers a different
question: (a) the finding objects on the run record, extending the work
`docs/in-job-review/reconciliation.md` Q8 already decided (F19, F20) — what the
run found; (b) the pull request body's existing status section, listing what the
mode left open and the mode that left it (F28, D24) — what a reader meets at the
pull request; and (c) `fugaro diagnose`, whose finding row already exists and
whose JSON contract §6.1 is already the console's input (F18) — what a later
reader can read back. The item file is what says **what to do about it**, and it
is the only one of the four that outlives both the ephemeral job and the merged
pull request (F46).

Reason: the ruling picks the carrier that survives, so the item file is the first
thing built rather than the thing deferred, and the plan's slice order follows
from that rather than from convenience: the folder, the format and the first
items land before the score and the run loop (T4.3), because D7 makes a mode's
tolerated findings backlog items and a mode must not be able to tolerate a
finding before there is a file to put it in. The record-side carriers stay
because they are one storage change and zero new surfaces, and because a run's
own record is what the mode's effect is audited from.

**How the item file is written without dirtying the pull request it is about —
the question the ruling asked this folder to settle. The item is part of the
**same** run branch and the **same** pull request, and it is reviewed with it.**
A run files into its own repository and its own branch, which is the one
repository its installation token reaches and the one branch it can push (F43,
F44); the item file is one more commit in the pull request it is already about,
so the deferred leftover arrives in the diff where the reader already is and the
reviewer can refuse it like any other change. An item that is to be filed later —
D15's follow-up task file, D30's per-mode rule — is a file in that same pull
request rather than a second branch or a second pull request, and a run writes
nothing anywhere else. That is also what §1.3's Slice 4 order and the plan's
T4.5 assume.

**Alternative (this folder's earlier recommendation, displaced by the answer):
the run-record side first — the finding objects on the record, the status
section, `fugaro diagnose` — with the Markdown ledger deferred to a later
slice.** Cost: a tolerated finding then has no carrier a later reader can close
one line of, because the two the first cut would have shipped are both ephemeral
— the pull request body is history the moment the pull request merges, and a
review round's table is a snapshot of that round (F46) — and the deferred work
waits a second slice to become addressable at all. It is cheaper by one task and
strictly weaker, which is why it was recommended and why it was declined.

**Alternative: a provider issue per tolerated finding.** Cost: the App holds no
issues permission, so this is a task before it is a carrier, and O5's question
about the permission has to be settled before it is planned rather than
during.

**Note on `version`:** `schemas/result.schema.json` is closed and pinned to the
record by `TestResultSchemaAcceptsRunnerRecords` (F20), so the new fields land
with their schema entries in one commit. The recommendation is to **keep
`version: 1`**: `internal/runstore/runstore.go` decodes with plain
`json.Unmarshal` and no reader refuses on the const (F21), so a bump would be a
compatibility claim nothing enforces, while the additive field is genuinely
compatible.

**Why one file per item rather than one ledger per work**, which the earlier
recommendation's precedent used (F32, F46): two runs filing in the same week would
conflict on one file, and the ruling's whole point is that parallel pull requests
filing items never conflict (D26, D28).

### D10 — The verify stage's gate is the recipe's `check`, not a mode switch

**Answered 2026-10-20: the verify stage is never skipped; the mode picks the
recipe, and so the `check` gate and the round ceiling.**
Why: `Decide` requires the verify record, so skipping it produces a failed draft
rather than a cheaper run.

**Recommended: the mode selects the recipe (and so the `check` gate and the
round ceiling); the verify stage itself — a `verify.KindTest` record with a clean
tree on the final commit — never changes, in any mode.** Reason: `runner.Decide`
requires that record (`ReasonNoVerifiedTest`), so a mode that skips the verify
stage produces a failed draft in the mode that was supposed to be the cheap one
(F4, F26). And there is nothing to gain: the in-run verify is a smoke check
either way, and CI on `main` is the merge gate (F27, F30).

**Alternative: POC runs no verify stage.** Cost: every POC run ends as a failed
draft with "no verified test run on the final commit", which is not a mode, it is
a bug report. Making it work means changing `Decide`, which is F4's rule.

### D11 — Draft or ready: unchanged, and Decide is the one who says which

**Answered 2026-10-20, and it corrects this folder's reading of today's
practice: unchanged — a pull request is ready if and only if `runner.Decide` says
so, whose policy the mode sets, and Hotfix follows the same rule.**
Why: the runner already opens the draft and marks it ready on Decide's answer
(`gitprov`'s `markPullRequestReadyForReview`), so no mode needs a second rule and
no person stands in that path.

**Recommended, and taken: nothing here changes. A pull request is ready if and
only if `runner.Decide` says so, and the mode changes only what `Decide` is
given — its policy.** Every mode opens a draft and every mode's ready state is
Decide's answer, Hotfix included: Hotfix's difference is that its policy tolerates
more and D12's gate decides whether the run may open at all, not that some other
act marks it ready. This is today's practice, read correctly: the runner opens the
draft (`internal/runner/prflow.go`'s `openDraftPR`, `Draft: true`) and marks it
ready itself, through `provider.UpdatePR` with `Draft: !ready`
(`internal/runner/runner.go`'s finalize) — which is `gitprov`'s
`markPullRequestReadyForReview` on GitHub — so **no person is in that path and no
mode adds one.** Reason: one rule for readiness is the property the owner relies
on (F4), and a mode that decided readiness a second way would be a second answer
to "may this merge". A POC run's `ship` comes from the same cheap reviewer as a
Critical run's, which is the argument for the mode being *visible* in the pull
request's body and its record (D24) rather than for a mode that holds a pull
request hostage.

**What the owner's own practice sentence means, corrected.** `docs/dogfooding.md`
says "a run that ends as a failed draft only because the excluded or unrunnable
parts failed can still be fine: check the PR's CI, mark it ready and merge"
(F30). That is the **fallback** for a run that ended `not_ready` for a reason the
operator can accept — a failed draft that a person may rescue by hand — not the
normal path, in which the runner has already marked the pull request ready. Read
the other way it would describe a repository where every run waits for a human,
which is not what the tree does and not what the modes change.

**Alternative: a mode whose tolerated set is empty opens ready and the rest
draft, with a person marking those ready.** Cost: readiness becomes a function of
the policy in a second place, so the answer to "may this merge" would be split
between `Decide` and whoever clicks; and the human step would land on exactly the
runs whose findings a person most needs to read *before* an approval-looking state
appears. It is the shape this folder originally wrote down, and it is what the
owner's correction of 2026-10-20 displaced.

### D12 — Who may invoke Hotfix: an operator, with a base-branch opt-in

**Answered 2026-10-20: an operator runs `fugaro run --mode hotfix`, gated on
the base branch's configuration — never a pull request comment, never a
follow-up.**
Why: the mode with the least review must not be reachable from the surface the
owner already treats as hostile.

**Recommended: `fugaro run --mode hotfix` by a person, gated on the base branch's
configuration (`rigor.hotfix` on, plus the trusted list) — never a PR comment,
never a follow-up.** Reason: a follow-up comment is the one input that arrives
while nobody who owns the risk is watching (F13, F14), and Hotfix is the mode
whose blast radius is production. An opt-in in the base branch's config means the
capability exists only in repositories whose owner turned it on, and the run
record names the account that asked.

**Alternative: a trusted follow-up comment may request `--mode hotfix` on an
existing PR.** Cost: the trusted list gates it, so it is not open to the public —
but the mechanism is a comment on a pull request, which is the surface the owner
has already decided to treat as hostile (`allow_public: true` with a one-account
list, F13), and the mode it grants is the one with the least review. If the owner
wants the convenience, the design's fallback is that the comment may raise the
mode *to* Critical and never *into* Hotfix, which is an asymmetry worth stating
in the flag's help text.

### D13 — What "deploy" means: Fugaro opens PRs and never deploys

**Answered 2026-10-20: Fugaro opens pull requests and never deploys; "ship
immediately" means a ready pull request the operator merges now, with the full
suite running as CI on `main` after the merge.**
Why: `gitprov` has no merge call and a run may not touch `.github/workflows/` at
all, so the post-deploy suite can only be the CI that already runs.

**Recommended: say it plainly in the design and in the mode's documentation.
"Ship immediately" means "open the PR ready, with a label, for the operator to
merge now". The full test suite after deploy is CI on `main`, which already runs
on merge (`go test -race ./...`, the docker-tests, the rules job and the
terraform job) — so no new workflow is required, and the spec's most expensive
sounding requirement is satisfied by an existing one.** Reason: `gitprov` has no
merge call and the runner never deploys (F29); the job's App holds no Workflows
permission and a run may not touch `.github/workflows/` at all — `workflowGuard`
refuses the push and the run ends `infra_error` (F12). A design that promised to
"run the full suite after deploy" by triggering a workflow would be promising
something the runner is structurally forbidden from doing, which the owner's own
rule states: "a task that touches `.github/` is done by a person or a local
session, never by a run" (F30).

**Alternative: the runner triggers the full-suite workflow after merge.** Cost: it
cannot, for the reason above; and making it possible means granting the App the
Workflows permission and lifting the refusal, which is a security decision
several sizes larger than this feature. The honest version of this alternative is
"a person merges and watches CI", which is the recommendation.

### D14 — Fix confidence: a deterministic diff rule gates; the agent's number is recorded, never trusted

**Answered 2026-10-20, and it differs from this folder's recommendation: the
gate is a deterministic rule whose inputs are all **project-configured** — the
project's own recipe `check`, the protected paths the project declares in
`rigor:`, and diff-size and file-count bounds with tunable defaults — with
universal built-ins only for the host's CI and workflow configuration and for
credential-file patterns. The agent's confidence is recorded and never
trusted.**
Why: Fugaro has to work for any project, so a gate that hard-codes this
repository's own paths is not a rule a project can run.

**Recommended, and taken: the agent's stated confidence is recorded on the run
and never gates anything; the gate is a deterministic rule over the diff and the
record, and every input it reads is one the project configured.** The inputs, in
the order the rule reads them:

1. **The project's own recipe `check`**, which is the gate the project already
   trusts; a mode does not replace it, it selects which recipe runs (D10).
2. **The protected paths the project declares** in its `rigor:` block (D18,
   D20) — the project's own words about what is sensitive, matched with the same
   `doublestar` matcher every other path rule uses (F9). A Hotfix may not touch a
   path whose floor is above the mode it is running at, wherever that floor was
   declared.
3. **Diff-size and file-count bounds**, with tunable defaults, so a project can
   say how large a "minimal diff" is for it; when the rule cannot decide, it
   takes the conservative branch (more checks, not fewer).
4. **A small universal built-in set, and nothing more**: the host's own CI and
   workflow configuration (`.github/workflows/`, already refused outright,
   F12) and credential-file patterns — the two things that are unsafe to guess
   wrong in any project, and the only two this design hard-codes.

The compile and the targeted test still have to pass; that is the verify stage
(D10), not a fourth input. Reason: Fugaro runs for **any** project, so a gate
built from this repository's own paths would be a rule that only this repository
can run — and the configuration is where a project's own security posture already
lives. An agent grading its own confidence is the weakest signal in the system —
it is the point the spec itself flags as an open question, and the same class of
signal the owner already distrusts ("the in-loop reviewer is a cheap model too",
F30). Making the gate depend on the diff means the gate is checkable by a reader
who has the diff, which is what makes a post-hoc audit of a Hotfix possible.

**Alternative (this folder's earlier recommendation, displaced by the answer): a
Fugaro-specific rule with this repository's paths in it** — a credential or IAM
path list written into the runner, plus `.github/workflows/`. Cost: every other
project runs a Hotfix gate that knows nothing about its sensitive paths, and its
own config cannot teach it, because the list is code; and the day this
repository's paths change, the gate is wrong in a second place. It is smaller to
write and it does not generalise, which is the trade the answer refused.

**Alternative: let the agent's confidence number pick the branch (high → no
pre-push tests; low → compile and a targeted test).** Cost: a model's self-report
becomes the control on the one path with no review, and the failure is silent —
a confident wrong fix ships with the run record agreeing that it was confident.
It is also unmeasurable afterwards: nothing in the record can distinguish "the
agent was right" from "the agent was sure".

### D15 — The automatic follow-up: recorded now, scheduled later

**Answered 2026-10-20: the Hotfix run records a ready-to-paste follow-up
task file; no scheduler is built.**
Why: a trigger that reacts to a merge is new infrastructure with its own
credentials, and the task file exists the moment the run ends.

**Recommended: the Hotfix run records and prints a ready-to-paste follow-up task
file ("revisit in the area's normal mode", naming the PR, the paths and the
mode), and does **not** build a scheduler. Deferred: any event source that reacts
to a merge.** Reason: the follow-up path today is `fugaro run --pr N` on a
trusted comment and `fugaro run` on a task file (F14); there is no mechanism that
reacts to a merge, and a Cloud Run job is not a scheduler. The requirement the
spec states — "automatic follow-up task to revisit the fix properly" — is
satisfied in substance by a task file that exists the moment the run ends and is
trivial to run; what is missing is only the trigger, and building a trigger is
new infrastructure with its own authentication, which should not ride in on the
first slice of a mode.

**Recommended, part two — what happens when the asynchronous run finds a
regression: escalate, do not auto-fix.** CI on `main` fails after a Hotfix; the
follow-up runs at the area's normal mode, that mode cannot be lowered for it, and
the run record names the original Hotfix so a regression trace points at it. An
automatic revert is not proposed: `gitprov` has no merge path, so a revert is a
pull request anyway, and a machine-initiated revert of a machine-initiated fix
needs the same review the fix skipped.

**Amended 2026-10-20, by the backlog ruling.** The follow-up is now also an
**item** — the ruling's own words are "a follow-up item for what the hotfix
deferred" — filed under `docs/backlog/` by the run that opened the pull request
(D30, plan T4.5–T4.6), so that a follow-up outlives the pull request that
recorded it and sits where every other deferred leftover sits. The task file this
decision recommends stands exactly as written: the **item** is what to do next and
where a reader finds it, the **task file** is a thing to run, and T5.5 keeps
producing it.

**Alternative: a scheduled job that scans merged Hotfix PRs and opens the
follow-up task.** Cost: a scheduler, its credentials, its own failure mode
(a scheduler that stops is invisible), and a new surface for the trusted list to
be wrong about — in exchange for removing one paste. Worth doing later, and worth
deciding on its own merits rather than as a footnote to this mode.

### D16 — Cost: modes lower the ceiling, never raise it

**Answered 2026-10-20: a mode lowers the ceiling and never raises it — a
per-run default below the deployed cap, ordered POC < Startup < Pro < Critical,
with Hotfix tightest and still enough to finish the verify stage.**
Why: the effective cap is the lower of two values that are already frozen, so a
mode can only move downward.

**Recommended: each mode ships a per-run budget default expressed as a fraction
of the deployed cap, ordered POC < Startup < Pro < Critical, applied only when no
explicit `budget.per_run_usd` is set and never able to exceed the deployed
`FUGARO_MAX_RUN_USD`. Hotfix gets the tightest bound of all, with a floor high
enough to complete the verify stage — a run that dies on the cap produces
nothing, which is the most expensive outcome there is.** Reason: the effective
cap is the lower of the frozen job value and the database's (`budgetEnv`,
`internal/runner/policy.go`, `internal/budget/lease.go`, F15), so a mode can only
move downward; expressing the defaults as fractions makes that structural rather
than a convention. The "a run that dies produces nothing" floor is the one
non-obvious part, and it is why POC's bound cannot simply be "the smallest
number".

**Alternative: a mode sets `agent.max_budget_usd`.** Cost: that number is Claude
Code's own estimate, roughly 25× the gateway's DeepSeek price, and it is set high
on purpose (F16). Tuning it tunes a number that does not govern spend, and a mode
that lowered it would strangle a run's stages without lowering the bill.

### D17 — `fugaro budget preview` takes `--mode` where the arithmetic is real

**Answered 2026-10-20: `fugaro budget preview --mode` changes only the bound
the tool can compute; the output stays a ceiling and never becomes a forecast.**
Why: the mode is a real input to `StageBound` and to nothing the tool can
honestly predict.

**Recommended: the preview accepts `--mode`, and the mode changes only the terms
the tool can honestly compute — the recipe's `recipe.StageBound`, since a mode
selects a recipe. The output keeps saying it is a ceiling and not a forecast, and
the `typical:` line stays empirical (≥5 real-cost runs, 30 days).** Reason:
`internal/cli/budget_preview.go` computes a bound and says so in as many words
(F17); a mode is a real input to that bound. What a mode is *not* is a
prediction of what the run will spend, and the CLI already refuses to pretend
otherwise. The plan's T6.2 is the flag itself, and T6.3 makes
the empirical line per mode now that T1.6 puts the mode on every run's row so the empirical line can eventually be per mode — that is
the honest path to a per-mode number.

**Alternative: make the preview forecast per-mode cost.** Cost: it would be a
forecast built from a ceiling times a stage bound, and the tool would be lying in
a way its own documentation already disclaims. It is the kind of number that gets
quoted in a planning document and then treated as a fact.

### D18 — Security: floors win, Critical is the default for the sensitive tier, and the mode is recorded

**Answered 2026-10-20: floors win over every other input, the built-in
protected set stands beside whatever a repository declares, and the mode used is
recorded in the pull request body and in the run record.**
Why: the refusal in `refused.go` is this repository's precedent for a protection
that does not depend on the repository remembering to configure it.

**Recommended.** A mode must never bypass a protected path or an independent-review
requirement; floors win over every other input. **Critical is the default mode for
the security-sensitive tier**, which includes a built-in set — `.github/workflows/`
(already refused by `runner.workflowGuard`, F12) and credential and IAM paths — plus
whatever the repository declares. Paths are declared as `doublestar` globs in the
`rigor:` block (and, project-wide, in the published policy), each with a mode, and
the pattern is validated the way config validates patterns today (F9). **A task or
a follow-up comment cannot lower a mode below a floor, ever** (D6, D8). **The mode
used is recorded in the PR body and in the run record**, in the same words, so the
two can be diffed. Reason: the repository's security posture for this tier is
already written (`docs/credentials-service/implementation-plan.md` Global
Constraint 10, and `docs/dogfooding.md`'s money-and-security rule, F31), so
the tier the floors cover is already meant to run at Critical, and D18's answer is
what turns that policy into a rule the resolver cannot go under; and the built-in
part matters because a
repository that forgets to declare its credential paths would otherwise run them
at whatever its default is — the refusal in `internal/runner/refused.go` is the
precedent for a protection that does not depend on the repository remembering.

**Alternative: sensitive paths are declared in configuration only, with no
built-in set.** Cost: one absent line in a config file is the difference between
a credential path running at Critical and running at POC, and the failure is
silent and one-way. The cost of the recommendation is that a repository cannot
*lower* `.github/workflows/`, which is not a cost — that refusal is not
configurable today and should not become so (F12).

### D19 — Migration: the default is Pro, and Critical is what a repository pins to

**Answered 2026-10-20, and it differs from this folder's recommendation: the
default mode is **Pro**, not Critical.** The first cut therefore **does** change
behaviour for a repository with no `rigor:` block — its runs start tolerating what
Pro tolerates — while the security-sensitive tier (D18's floors) stays Critical
and any repository may pin itself back with `rigor.default: critical`.
Why: Critical everywhere is the problem the spec exists to solve, and a default
that changes nothing helps nobody until someone opts in.

**Recommended, and taken: the default mode is **Pro**.** Pro's threshold table is
therefore the behaviour of **every repository that sets no `rigor:` block**, from
the commit that gives the mode a reader — and that is a change of behaviour, not a
change of configuration: a finding the default used to fail on may now be
tolerated and filed. Two things bound it. **First, the security-sensitive tier
stays Critical**: D18's floors are outside every layer, so a credential path, an
IAM path or `.github/workflows/` runs at Critical whatever the default says.
**Second, a repository may pin itself back**: `rigor.default: critical` in that
repository's `fugaro.yaml` restores today's behaviour exactly, and it is one line
that a repository can merge on its own.

**The migration note, stated where a reader will meet it** (the plan's T8.2, and
the release note it writes): *the rigor modes change what "ready" means for a
repository that configures nothing. From this release the default is Pro, so
findings that used to fail a run may now be tolerated and filed as backlog items.
Set `rigor.default: critical` to keep the old behaviour, or leave it and read what
Pro tolerates.* Repository owners get the note, the one-line pin, and the mode's
own line in the pull request body (D24) telling them which mode a run ran under.

**Reason:** Critical everywhere is the problem the spec exists to solve (its
"Problem" section is two sentences long and both are about this), and a default
that changes nothing helps nobody until someone opts in — while the floors,
which no default can reach under, keep the security-sensitive tier exactly where
F31 already puts it. The CLI and documentation surface is unchanged from the
earlier recommendation below: `fugaro run --mode`, the `rigor:` config and policy
keys, `fugaro doctor` checking that the mode vocabulary and the floors are
self-consistent, and `fugaro diagnose` printing the mode of the run it read.

**What follows from the answer, and is the plan's problem rather than this
decision's:** every claim in this folder and in the plan that the first cut
"relaxes nothing", that an unconfigured repository "behaves exactly as it does
today", or that a run is "byte-identical to today" is **true of Critical mode
only** — of a run whose resolved mode is Critical, which after this answer is a
run that configured it or sits under a floor. The plan scopes them (T3.1's table,
T8.2's note) and §7's review focus gives Pro's threshold table the attention it
now owes: **Pro's table is the behaviour of every unconfigured repository**, so
it is the one table whose review cannot be deferred.

**Alternative (this folder's earlier recommendation, displaced by the answer):
default to Critical — today's behaviour, byte for byte — until the owner sets
another.** Cost: nothing changes until a repository opts in, so the feature helps
nobody who does not read a release note, and the "Critical everywhere" problem
the spec opens with survives the release that was meant to solve it. Its
advantage was that the first cut's risk was bounded by construction, with each
slice merging alone against unchanged behaviour; the answer trades that bound for
the point of the feature, and pays for it with the floors, the one-line pin and
the migration note above.

### D20 — Where each layer's key lives, and how an area is named and matched

**Answered 2026-10-20: three keys — `defaults.rigor.default` in the project
layer; a `rigor:` block in `fugaro.yaml` carrying `default` and `areas`; and an
area written as `{name, match: <doublestar glob>, mode}`.**
Why: each key lands where the tree already reads that kind of value, and a rule
with a name is a rule the run and the pull request can print.

**Recommended: three keys, in the three places the tree already reads, with an
area as a named `doublestar` rule.**
(a) The **project default** is `defaults.rigor.default` in the project layer
(`config.LayerKey`), a new member of `LayerDefaults` because that struct is
closed (F36), scope `InProject`, merged by `Resolve` into every repository of the
project like `defaults.git` and `defaults.agent`.
(b) The **repository's** own value is a top-level `rigor:` block in
`fugaro.yaml`: `rigor.default` for this repository's mode and `rigor.areas` for
its areas, scope `InRepo` — five edits (F7), the schema entry included.
(c) An **area** is one entry of `rigor.areas`, a list of
`{name: <short label>, match: <doublestar glob>, mode: <name>}`. The `name` is
not decoration: it is what the run, the PR and `fugaro watch` print when they say
which rule decided (D24), and a rule that cannot be named cannot be explained to
the person reading the PR. `match` is a path glob, matched with the same
`doublestar` matcher as everywhere else (F9) and validated at load by
`internal/config/validate.go`'s `doublestar.ValidatePattern`, so a bad pattern is
a configuration error rather than a rule that silently never fires; a relative
path is relative to the repository root, which is what `runstore.Record.ChangedFiles`
holds. The areas may be set in either layer — `InProject | InRepo` for the same
key — because the two lists merge without an ordering rule: D23's strictest-wins
rule makes a project area and a repository area that both match resolve to one
answer whatever order they were read in.
(d) The **task's** request is `fugaro run --mode` (T1.6) and, later, the task
file's own key (T5.1, with F23's image-skew gate); scope `InOverride`, raise-only
(D6).

**Alternative: `rigor.areas` as a map keyed by glob** (`"internal/payments/**":
Critical`). Cost: no name to print, so D24's readout becomes the glob text as
written — which is fine until two globs are near-identical in a PR body — and a
map key has no room for the one thing a rule needs beside its pattern, which is
what to call it in a sentence.

**Alternative: areas as a directory tree, one config file per area.** Cost: a
second configuration surface with its own schema, its own trust question and its
own load order, for a shape the repository does not use anywhere; and an area
file inside the tree is a file the branch under review can write (F35, F41).

**Amended 2026-10-22, by the levels addendum, amended after review and confirmed by the owner 2026-10-22:** a repository's own configuration may lower a project area, and a project area's members are repositories or repositories with paths; see [levels-reconciliation.md](levels-reconciliation.md) §3a (LD4).

### D21 — Whether a narrower layer may lower

**Answered 2026-10-20: a narrower layer may lower a default, and no layer may
lower a floor.**
Why: a default is a value a narrower layer is meant to replace, and a floor is a
constraint no layer reaches; making the two one thing leaves a project no way to
say "usually Pro here, POC for this repository".

**Recommended: yes for calls about the *default*, no for calls about a *floor*.
A repository's `rigor.default` and its areas may set a mode lower than the
project's default — that is what a default is for, and a project that wants a
floor says so with a floor (D5's part three, D18), not with a default.** Reason:
the alternative makes the project default a floor in disguise, and then the
project has no way to say "usually Critical here, but this repository may run its
own POC paths" — which is the ruling's whole shape. The line is not "narrower
wins" or "stricter wins"; it is that a default is a value a narrower layer may
replace and a floor is a constraint no layer may replace.

**Alternative: monotone across layers too — the mode may only rise from the
project default, so the project default is effectively a floor.** Cost: a
project that wants one strict default for most repositories and a cheap mode for
one of them cannot say it; the workaround is a second project. It buys a stronger
guarantee, and it is the right answer for an owner who wants a project-wide
minimum but not a policy entry: it is one comparison in `Resolve`, and the plan's
T1.1 test table is where it would land.

### D22 — What "dynamically, per area" means, and when the resolution runs

**Answered 2026-10-20: three moments — bootstrap, from `fugaro run --path`
(repeatable); after every review round that changed the tree; and before the pull
request is marked ready — and the mode never falls within a run.**
Why: those are the three places a run legitimately knows its paths, and monotone
is what makes them compose into one answer.

**Recommended: the area is chosen from the files the run touches, at three
moments, and the mode never falls within a run.** (a) **At bootstrap**, from the
paths the caller declares — `fugaro run --path` (repeatable), because the tree has
no path input at all today (F38); a run that declares none starts at its
repository's or project's value. (b) **After each review round that changed the
tree**, from the run's own diff against `workBase`
(`internal/runner/changedfiles.go`'s `r.repo.ChangedFiles(ctx, r.workBase())`,
which `recordChangedFiles` already calls), so the round ceiling the higher mode
wants can still be spent on the rounds that remain. (c) **Before the PR is marked
ready**, from `runstore.Record.ChangedFiles`, which is F39's existing measurement
and sits exactly at the decision point. The resolved value is monotone: once a
run has reached a mode, nothing lowers it for that run, and the record names the
moment and the area that raised it.

**Reason:** "dynamically" cannot mean "before the run", because the input does not
exist then (F38), and it cannot mean "after the run", because a raised mode that
arrives after the last round has spent nothing and can only refuse readiness. The
three moments are the three places the run legitimately knows its paths, and the
monotone rule is what makes them composable — the same only-tightens property the
floors have.

**Alternative: two moments only — bootstrap and finalize.** Cost: a diff that
first matches a Critical area in round 2 of 3 buys nothing, and the run's extra
rounds are gone; the PR turns into a draft nobody asked for, with the reason
being a mode that arrived too late to be used. It is one call site fewer, and it is
what the plan's T1.4 would have built had the answer taken it; the answer took the
three moments, so T1.4 builds three call sites and the monotone rule between
them.

**Alternative: resolve from `plan.Footprint` — the planner's prediction — at
bootstrap, and skip the declared paths.** Cost: nothing a run executes reads that
file today (`internal/plan/footprint.go` says it is advisory and `fugaro plan
graph` is its only reader), so a run would gain a dependency on a plan artifact
that may not exist, may be stale, and covers the files a *plan* files changed
rather than the files a run will.

### D23 — A run that touches several areas: strictest wins

**Answered 2026-10-20: the strictest mode among the areas the run's paths
match wins, and every matched area is recorded.**
Why: it fails closed and needs no order between a project area and a repository
area that both match.

**Recommended: the strictest mode among the areas the run's paths match, and the
record lists every area that matched.** This is the owner's own proposal, taken
as written. Reason: it is the same only-tightens rule the floors already have, so
there is one idea in the system rather than two; it needs no order between a
project area rule and a repository area rule that both match, which is what makes
D20's "the areas may be set in either layer" true; and it fails closed, so a run
cannot escape a rule by touching one more file under another area.

**Alternative: the area with the most changed files wins.** Cost: it is not
monotone — a run whose strict-area change is one file and whose cheap-area change
is ten resolves cheap — and it can be diluted deliberately by touching more files.
It is defensible as "where the work is", and dishonest as a control.

**Alternative: the first matching rule in configuration order wins.** Cost: the
answer depends on the order of a list in a file, so moving one entry changes which
rule governs a path, and the resolution can no longer be explained from the path
alone.

**Alternative: the narrowest glob wins** ("more specific beats less specific").
Cost: undefined for overlapping globs of equal or incomparable specificity
(`a/**` and `**/b.go`), and the tie-break that would define it is a rule nobody
will remember. Patterns have no natural order; modes do, and that is why
strictest-by-mode is the recommendation.

**Amended 2026-10-22, by the levels addendum, amended after review and confirmed by the owner 2026-10-22:** the strictest mode wins within one layer, and across layers the narrower layer wins; see [levels-reconciliation.md](levels-reconciliation.md) §3a (LD4).

### D24 — What each surface shows, and which value it shows

**Answered 2026-10-20: three surfaces — the run record, one line in the pull
request body, and a live `fugaro watch` row — and the watch half is a new registry
child, a rules regeneration with its golden file, and a deploy the owner runs.
Recorded as proposed rather than numbered: the plan's part and the owner-run
check, both below.**
Why: the mode is what the review was for and the owner reads all three surfaces;
the third is priced because it costs a deploy rather than a line of code.

**Recommended: one resolved value with three parts — the mode, the layer that
won, and the areas that matched — shown in three places.** The value is computed
once per resolution (D22) and carried, not recomputed per surface, from the same
source as the record's `Mode` (F18's one-value rule, §6's item 5).

- **The run.** `runstore.Record` gains the rigor block (mode, `from`, areas with
  the moment each was applied), so `fugaro runs ls --json`, `fugaro diagnose` and
  the console read it through `internal/runview`'s `Row`/`Join`, and the console's
  contract (`console/docs/json-contract.md` §6.1) says what the new fields are.
  This is the cheapest surface: the record is one struct plus its JSON schema.
- **The PR.** The body's existing status section (`internal/runner/prflow.go`'s
  `finalSection`) gains one line — the mode, and where it came from, in the shape
  D9 already plans for the backlog: `rigor: Pro (repository, area
  internal/payments/**), raised from Startup at round 2`. Reason: the PR is the
  artifact the owner reviews, and the mode is the thing that decided what the
  review was for.
- **`fugaro watch`.** This one costs, and the cost should be stated rather than
  discovered: a live row is built from the RTDB registry entry
  (`internal/watch/build.go`'s `runRow` over `budget.AgentEntry`), the entry's
  rules refuse an unknown child (`"$other": { ".validate": false }`), so a live
  mode needs a new registry child, a rules regeneration with its golden file, and
  a deploy the owner runs. The recorded precedent is `Recipe`'s drop-on-old-rules
  path in `internal/budget/registry.go`'s `Session.Start` (warn once, remember for
  the session) — F40 has both precedents and their cost.

**Alternative: no registry child — `fugaro watch` shows the mode only once the
run has finished** (its row then comes from `result.json`'s record, which is
free). Cost: the mode of a run in flight is invisible, which is exactly the run
whose mode someone would want to argue with; the price of the alternative is
zero and the price of the recommendation is a rules deploy, so this is the
decision where the owner weighed a deploy against seeing a live run's rigor — and
took the deploy: the answer above keeps the live `fugaro watch`, with the registry
child, the rules regeneration and its golden file as a plan part and the deploy
itself as a check the owner runs (the plan's §9, where the check is proposed and
not numbered).

**Alternative: watch reads the mode from the machine that launched the run.**
Cost: does not work — a run executes on a Cloud Run job and watch reads the
database for runs on any machine, which is why the registry exists at all.

### D25 — Which revision of the configuration the mode is read from

**Answered 2026-10-20: the base branch, for every run kind, including a
first run.**
Why: a value that decides how much review a pull request gets must not be read
from the pull request under review.

**Recommended: the base branch, for every run kind, including a first run.** That
means a new read beside `internal/runner/followup.go`'s `readBaseConfig` (which
already fetches `origin/<ref>` and reads `fugaro.yaml` with `repo.ShowFile`) for
the mode's configuration, rather than the working tree's copy that
`internal/runner/runner.go`'s bootstrap reads today (F35). Reason: the mode
decides how much review the PR gets, and a value that decides that must not come
from the artifact under review — which is precisely why `readBaseConfig` exists
for a review run and a follow-up, as its own doc comment in
`internal/runner/followup.go` states. A
branch that could set its own mode could set it to POC in the same pull request it
wants merged, and the built-in floors (D18) would be the only thing left holding
the line.

**Cost, stated: one extra read at bootstrap, and one divergence to keep
explained.** A first run would read its *other* keys from the checked-out ref and
the mode from the base branch, so `fugaro run --ref feature-x` can run on
`feature-x`'s recipes and under `main`'s rigor. That is a "two answers" hazard of
exactly the kind F35 is, and the honest mitigation is that the run's record says
which revision each came from (D24) rather than that the divergence does not
exist.

**As T1.2 read it: "the base branch" is the repository's default branch.** A
first run's checkout is cut from the ref the run will push, so `git.base_branch`
is a key the branch under review writes for itself: reading `origin/<that name>`
would let a branch be its own base, which is the one shape the answer above
forbids. `internal/runner/rigor.go` therefore reads the mode through the same
default-branch read the project check and the budget policy make
(`internal/runner/policy.go`'s `defaultBranchFile`, `repo.DefaultBranch`) — the
revision no file in the checkout can name — and D24's `revision` is what the
record says it read. A repository whose `git.base_branch` is not its default
branch resolves its mode from the default branch's file nonetheless. A local run
is the exception, and has its own reason: it has no project, so checkProject and
resolvePolicy already skip that read there, and it has no branch under review to
be protected from — the checkout is the developer's own — so it reads the ref
its own configuration names, as it did before the default-branch read arrived.

**Alternative: the checked-out ref, for a first run only.** Cost: free (the file
is already read), consistent with every other key a first run uses, and it means a
branch merges together with the rigor it was developed under — which is a genuine
argument. What it accepts: a branch sets its own mode until the floors stop it,
and the floor set is the only backstop. This is the alternative the answer weighed
against the base-branch read and refused: the mode is read from the base branch
for every run kind.

### D26 — An item's id is its file name: a date, a sequence, and a slug

**Answered 2026-10-20, in the backlog ruling's own words: one file per item
under `docs/backlog/`, named `<id>-<slug>.md`, so that parallel pull requests
filing tickets never conflict on one file.**
Why: a date-plus-sequence name reaches that with no allocator, and the one
collision left is a rename on the branch that would merge second.

**Recommended: the file is `docs/backlog/<id>-<slug>.md`, where `<id>` is
`YYYYMMDD-NN` — the day the item was filed and a two-digit sequence that day,
allocated by scanning the folder — and `<slug>` is lower-kebab words from the
title.** The front matter's `id:` repeats the id, so the name and the field are
checked against each other and an id resolves to a path without a lookup, which is
the same "a path and a symbol survive, a line number does not" rule this folder
states for its own citations (F32's neighbours). Reason: the ruling's whole point
is that **parallel pull requests filing tickets never conflict on one file**, and a
date-plus-slug name reaches that with no allocator: two runs on two branches write
two differently named files, and the merge is a union of disjoint files rather
than a conflict. The one collision left — two branches filing the day's Nth item
— is a **rename of one file**, mechanical and visible, and this repository's
registers already carry the rule for exactly that case: *whichever of two open
claims merges second renumbers itself*. The pin (T4.3) refuses a duplicate id and
a front matter whose `id:` disagrees with its file name, so the collision surfaces
on the branch that would merge second rather than on `main`.

**Alternative: a single global counter (`B1`, `B2`, …).** Cost: a counter nobody
owns. With no central allocator two parallel filers mint the same number, and
because the sequence is not in the folder's own state the collision is silent
until CI — which is precisely the failure the ruling's one-file-per-item shape
exists to avoid. **Alternative: the filing run's id as the item's id.** Cost: a
hand-filed item has no run, so the folder would need two id shapes and two
parsers; and a run id is opaque in a pull request body, where a reader meets the
citation first.

### D27 — The front matter: eight required fields, three statuses, and a body with headings

**Answered 2026-10-20, in the ruling's own words: the eight front-matter
fields it names — id, title, mode, area, status, filed-by, filed-in and date.**
Why: seven of the eight already have a source on the run record, so the format
invents no second place to disagree about them.

**Recommended: the eight fields the ruling names, each required, with `status` in
a closed three-word vocabulary and the body carrying named headings.** The fields
and where each value comes from today:

| Field | Where the value comes from |
| --- | --- |
| `id` | the file name (D26) |
| `title` | the filer's words, one line |
| `mode` | the **resolved** mode the run ran under (D24's value on the record); `none` for an item filed by hand |
| `area` | the matched area's `name` (`rigor.areas`, D20); `project` or `repo` for an item about the whole layer; and, for a mixed run, all the matched names, **strictest first** (D23's answer records every area a run matched, not only the one that won) |
| `status` | `open`, `closed` or `dropped` — three words, and no fourth |
| `filed-by` | `runstore.Launch.LaunchedBy` (read back as `runview.Row.LaunchedBy`) for a run; the filer's login for a hand-filed item |
| `filed-in` | `run:<run id>` or `pr:<number>`, both already on `runstore.Record` |
| `date` | the record's `FinishedAt` in UTC as `YYYY-MM-DD` — never the clock of whoever regenerates the index |

Reason: F47 shows seven of the eight have a source in the record and the eighth
is D23's rule arriving where a reader meets it, so the format should not invent a
second place to disagree about any of them. The mode and the area are recorded as the
**resolved** values, not as the configuration's words, so an item is readable
without the config that produced it — which is the same reason D24 records the
mode on the run. The body's headings are `## What was left open`, `## Why`,
`## What done looks like` and `## Links`, with the first two required, so an item
cannot be a title with no text; and `date` reads the record's own finish time,
because a generator that reads a clock produces a different index every day and
the pin would fail on a document nobody edited (F45's lesson from `gencatalog`).

**Alternative: JSON front matter, or one JSON file per item.** Cost: the pull
request diff — the one place a reviewer meets an item — becomes a machine file
nobody reads, and the id/title/status would have to be rendered back into Markdown
for the index anyway. **Alternative: two files per item, the item and a `closed`
marker.** Cost: two things to keep in step, two chances to drift, and the pin then
has to hold a relation between files rather than a field.

**Amended 2026-10-22, by the levels addendum, amended after review and confirmed by the owner 2026-10-22:** a ninth required field `kind`, and `mode` renamed `level`; see [levels-reconciliation.md](levels-reconciliation.md) §3a (LD5).

### D28 — The index is generated, pinned twice, and never hand-merged

**Answered 2026-10-20, in the ruling's own words: the index is regenerated by
a small tool and pinned by a test, so two pull requests filing at once never
conflict on it.**
Why: a hand-maintained index is a list the pin would have to accept, and the
ruling asks for a generator.

**Recommended: `docs/backlog/README.md`, generated by a small tool, with two
guards of different kinds, and a stated rule for two branches that regenerated it
at once.** The index is one row per item — id (linked to its file), title, mode,
area, status, filed-by and date, sorted by id, so by date — plus a count line and
the `closed` and `dropped` rows grouped under the open ones. `internal/backlog`'s
`Render` produces it; `fugaro backlog index` writes it; `fugaro backlog list`
prints it and `--json` gives the console the same rows (a new §6 entry in
`console/docs/json-contract.md`, the contract the console already reads). Its
first line is a generated-by header, as `internal/clidoc`'s documents have, and
its text says the file is regenerated rather than edited.

Two guards, because one is not enough (F45): **`internal/cli/docs_backlog_test.go`'s
regenerate-and-compare pin**, which renders the folder in memory and compares it
byte for byte with the committed index — the `internal/clidoc` model — **and a
tree-derived completeness guard**, which counts the item files in the tree and
asserts the index has a row for every id found and no row for an id that is not,
so a generator that silently skips an item is caught by the guard that does not
ask the generator. The second guard is the one `docs_website_security_test.go`
teaches the reason for: a test written against the generator agrees with whatever
the generator does. **The rule for two branches:** the index is never merged by
hand — whoever lands second re-runs `fugaro backlog index`, which is one command
that touches no item file.

Reason: the ruling asks for exactly this pair (an index a tool regenerates, a test
that pins it), the two kinds of guard are what this repository's two existing
generated documents between them demonstrate, and the "re-run the generator" rule
is cheap because the index is derived state: it holds nothing a reader cannot get
back in one command, which is what makes it safe to overwrite rather than merge.
The tool reads no network and no clock (D27), so regeneration is deterministic and
a conflict has one resolution rather than two.

**Alternative: a `merge=union` attribute for the index.** Cost: a union merge of
two generated documents concatenates both bodies and both headers, so the result
is *valid Markdown and wrong*, and only the pin would catch it — where a conflict
is loud, immediate and costs one command. It would also add the repository's first
`.gitattributes` for one file. **Alternative: print the index and never commit
it.** Cost: the folder loses its browsable front door and a reader on the provider
sees a list of file names instead of a list of items — which is the reader the
ruling's index exists for.

### D29 — Project-level items live in the project layer's own repository (the owner's question)

**Answered 2026-10-20 in shape, and this is the one question whose value is
still the owner's:** the ruling puts a project's items in one designated
repository named by the project configuration; **which** repository that is has
not been answered, and this folder recommends `defaults.backlog.repo` valued at
this repository. The plan names it at the task that needs it (T4.5's **Depends
on** line).
Why: a run's installation token reaches exactly one repository and `PRSpec` names
none (F43), so the designation has to be a value a run can read rather than one it
chooses.

**Recommended: one new member of the project layer's closed `defaults:` —
`defaults.backlog.repo`, a canonical `<provider>/<owner>/<name>` — and, for this
installation, its value is this repository (`dimipaun/fugaro`), the one whose
`fugaro.yaml` carries `project: fugaro` and `gcp_project: fugaro-dev`.** The rule
the value follows is §1.2's, unchanged: the project layer states the default, a
repository's own block may override it, and an item's `area:` says which area it
belongs to. Reason: a project-wide item has to live **somewhere in a repository**
that a reader of the project can find, and the project layer is the one place a
project-wide value already lives (F36); naming it there means the value is edited
in the file the owner already edits and is checkable against the enrollment the
policy lists (`projectpolicy.Policy.AllowRepos`, `CloneURLs`). This repository is
the recommendation for the value rather than a principle: it is the project's own
home, and an item about the project filed in the project's repository is the
shortest path from a reader to the thing.

**The honest limit, and it decides the first cut (F43).** A run's token reaches one
repository and `PRSpec` names none, so **a run in another repository cannot file
into the designated one**. In the first cut, therefore, the designated repository
holds the items filed **in it** — by the owner by hand, or by a run in that
repository — and a run elsewhere files into its own repository with `area:`
naming the area, which is what the ruling's "project-level items live in one
designated repo" means in practice at this tree: a **scope** rule, not a
cross-repo write. The cross-repo filing is a real task of its own (a second token
mint, a second checkout, a second push and a second pull request), and it should
be decided on its merits rather than hidden inside a filing command.

**Alternative: a dedicated `fugaro-backlog` repository.** Cost: a new repository
with its own CI, its own image, its own enrollment in the policy and its own
review surface, holding files nobody reads in a pull request — and the cross-repo
write is still missing, so it buys nothing the designated-repo key does not.
**Alternative: the first entry of the published policy's `allow_repos`.** Cost: the
value becomes order-dependent state in a published document, and changing it means
re-publishing the policy rather than editing a file. **Alternative: the local
project config's `repos:` map.** Cost: it lives on one machine and in no
repository, which contradicts the ruling's first line — the backlog is Markdown
files **in the repository**.

### D30 — How each of the five modes files

**Answered 2026-10-20, in the ruling's own words: POC files none or one line,
Startup and Pro file one item per tolerated finding, Critical files none of its
own, and Hotfix files one for what it deferred.**
Why: the mode is what decides which findings are tolerated, so it is also what
decides which of them become items.

**Recommended: one mechanism, and the mode decides only whether the tolerated set
is empty — except Hotfix, which always files one.**

| Mode | What it files |
| --- | --- |
| **POC** | nothing of its own. Its pull request body's one line and the run record are the whole account, and the owner may file an item by hand — the ruling's "none or one-line" |
| **Startup** | one item per tolerated finding, filed by the run, in the same pull request |
| **Pro** | one item per tolerated finding, filed by the run, in the same pull request. The two modes differ in their thresholds (D7), not in how they file |
| **Critical** | nothing of its own — it tolerates nothing (D7). If the owner's own instruction defers something, the caller files the item and it records `mode: critical` |
| **Hotfix** | **always one item** for what the hotfix deferred (the ruling; D15), plus one per tolerated finding if a floor or a path forced the run into a lower mode |

Reason: the modes differ in what they tolerate, and a filing rule that also varied
per mode would make a per-mode difference where there is none, which is the
"one rule, one place" argument this folder makes everywhere else (D6, D20).
Critical filing nothing is the same statement as its empty tolerated set, and
Hotfix's unconditional item is the ruling's own sentence and the substance of the
spec's "automatic follow-up task to revisit the fix properly". The mechanism is
T4.5's filing path; the table is T4.6's decision, held beside the policy T3.1
owns so it is a table rather than a condition inside the runner.

**Alternative: a per-mode switch over which carrier is used (an item for some
modes, the pull request body for others).** Cost: two records of one finding, and
the moment "one backlog and not two" (D32) breaks — by the mechanism that was
supposed to file.

**Amended 2026-10-22, by the levels addendum, amended after review and confirmed by the owner 2026-10-22:** Critical files its open risks as items, and Startup and Pro also file the deferrals the agent lists; see [levels-reconciliation.md](levels-reconciliation.md) §3a (LD5).

### D31 — Closing: the pull request that fixes it flips the status in the same commit

**Answered 2026-10-20, in the ruling's own words: the pull request that fixes
an item flips its status in the same commit.**
Why: the status then moves in a reviewed diff rather than by a second step someone
has to remember, and the item file is never deleted.

**Recommended: `status: open` → `closed` (or `dropped`, with its reason in the
body) **in the same commit as the fix**, by the pull request that fixes it, which
names the item id in its body and regenerates the index in that same commit.** A
closed item's file is **never deleted**: its id stays resolvable forever, because
older pull request bodies cite it, and this folder's own rule is that a path and a
symbol survive where a number does not. The pin holds what a tree can hold: a
`closed` item must carry a `## Closed by` line naming the pull request that closed
it, and a `dropped` item must carry its reason; the index row moves in the same
commit because the regenerate-and-compare pin (D28) refuses anything else. What a
test cannot hold is the honest rule beside it — **a run may not close an item it
did not fix** — and that is a review rule, stated here so a reviewer can ask.

Reason: the ruling says the pull request that fixes an item flips its status in the
same commit, and the mechanism that makes that cheap is the pin: an item flipped
without regenerating the index is red, so there is no way to close an item and
leave the list stale. Deleting the file instead of flipping it would be the
alternative with a real cost, below.

**Alternative: delete the item file when it closes.** Cost: every citation in an
older pull request stops resolving, and a reader who greps for the id finds
nothing — the opposite of what a backlog is for. **Alternative: move closed items
into a `closed/` subdirectory.** Cost: the move renames the path that carries the
id, so the citations break unless the id lives only in the front matter — which
D26 rejects, and which would make a link to an item depend on a lookup.

### D32 — One backlog: the practice, and what happens to the existing deferral notes

**Answered 2026-10-20, in the ruling's own words: one backlog and not two — a
deferral is filed as an item from now on, and the practice sentence lands in
`docs/dogfooding.md`.**
Why: two live lists of the same deferred work is the failure the ruling's own
words name, and freezing the `docs/plans` notes without saying so would leave
one.

**Recommended: the item is the one live backlog. Going forward, a deferral is
filed as an item and no new `docs/plans/*-review-follow-ups.md` file of that shape
is written; the practice sentence lands in `docs/dogfooding.md`; and the
deferral sections F46 names — the three the census counts and the fourth its
heading keeps out of the count — are converted in one mechanical pull request
that states the number of converted rows in its own body, each converted
section keeping its text with a one-line pointer to the items it became.**
Reason: the ruling's words are "so there is one backlog and not two", and the
sections are not merely a precedent — they are a second live list until
they are converted or explicitly frozen. Converting them is mechanical (a table
row becomes a file, and the file's front matter comes from the section's date and
the work it belongs to), and each section keeps its own words, because the prose
around a deferred item is what a reader of that plan needs. The merge-then-follow-up
practice is unchanged by any of this: what changes is where the follow-up is
**written down**, and the sentence in `docs/dogfooding.md` is what says so.

**Alternative: leave the history alone and start the backlog empty.** Cost: two
live backlogs until the last hand-written section ages out, and a reader who greps
for "deferred" finds both without being told which one is authoritative — which
is the state the ruling exists to end. **Alternative: convert them and also rewrite
the plan files' surrounding prose.** Cost: the history of a review round stops
reading as a review round, and the diff stops being mechanical; the one-line
pointer buys the same thing for a fraction of the review.

## 6. The security section, stated once

The brief asks for this explicitly, and it is the part of the design that must
not be reconstructed by a reader from the decisions:

1. **A mode never bypasses a protected path.** `runner.workflowGuard` /
   `runner.workflowRefusal` (F12) fire regardless of mode; the mode check runs
   beside them, not instead of them. No mode value, config value, or comment
   reaches them.
2. **A mode never bypasses an independent-review requirement.** SECURITY-SENSITIVE
   → opus plus mandatory independent review is `docs/credentials-service/implementation-plan.md`
   Global Constraint 10 (F31); the modes cite it and cannot waive it.
3. **Floors win.** The mode resolves as `area > repo > project` (D6, D20), with
   the task's request able to raise and never lower, and the built-in and declared
   floors outside every layer, only tightening — no repository value, no area
   rule, no task flag and no comment reaches under one (D8, D21). A follow-up
   keeps its PR's recorded mode and `--mode` may only raise it (D6, F14); a
   comment never participates in the resolution (D8, D12).
4. **Critical is the default for the security-sensitive tier**, with a built-in
   set (`.github/workflows/`, credential and IAM paths) plus the repository's
   declared globs (D18).
5. **The mode used is recorded in the PR body and in the run record** (D18), from
   one value, so a mismatch is a bug rather than a documentation drift.
6. **Fail closed.** An unscored finding, an unparseable verdict, or a policy that
   cannot decide blocks in every mode (D4, F3).
7. **The mode's configuration comes from the base branch, not from the branch
   under review** (D25, F35), for a first run as much as for a review run and a
   follow-up: the value decides how much review the changes get, and no run reads
   that value out of the artifact it is about to review. The floors are the
   backstop if the owner rules otherwise, and the run's record says which revision
   it read either way (D24).
8. **A raised mode is monotone within the run** (D22): once a run has reached a
   mode, no later resolution lowers it, so a path that turns out to be sensitive
   cannot be made cheap by touching more of it. This is the same property the
   floors have, and it is what makes the three resolution moments compose.
9. **The backlog is written by the run, into its own repository and its own
   branch, and nowhere else.** An item a run files is a Markdown file under
   `docs/backlog/` on the run's branch (F44), because that is the one repository
   its installation token reaches (`apptoken.go`'s
   `{"repositories": []string{s.repo}}`, F43) and the one place it can push; it
   passes `workflowGuard` like every other write; it carries no finding text the
   stored transcript does not already hold (F18); and a run never writes into the
   designated project repository (D29) unless it is a run **in** that repository.

## 7. What the owner would run, and what would go red

The plan's slices are verified by `fugaro verify build` and `fugaro verify test`.
The live checks below are the ones a person runs, because a container cannot:

| Check | What it proves | Why a container cannot |
| --- | --- | --- |
| A real POC run on a scratch repository, then `fugaro diagnose` | the mode reached the reviewer, and the tolerated findings are readable | needs a live model call and a real PR |
| The same task at Critical | the two modes differ in exactly the three places D7 names | comparison of two real runs |
| A Hotfix run touching a floored path | it is refused, and the refusal is the mode's, not the guard's | needs a real repository configured with a floor |
| `fugaro budget preview --mode` against the gateway's own figures for a finished run | the ceiling the mode claims is a ceiling the run respected | the gateway's price is not in the tree |
| The post-merge CI run on a merged Hotfix PR | the "full suite after deploy" claim is CI, and it fires | CI runs on the provider |
| A project with one repository carrying a `rigor.default` and one without | the project default reaches the repositories that say nothing, and the repository's own value wins where it speaks | needs a published project layer and two repositories |
| A run started with `--ref` at a branch whose own `rigor:` disagrees with the base branch's | which revision the mode was read from (D25) — the answer the owner's ruling on that decision makes | needs two branches and a real run on the branch |
| A run whose diff matches two areas, one Critical and one POC | the mixed-area rule is the strictest of the matched, and the record names both (D23) | needs a real diff that lands in both |
| A run whose first round stays in one area and whose second round reaches another | the raised mode bought the rounds that remained, and never fell back (D22) | needs a live agent and a multi-round run |
| `fugaro watch` on a live run after the rules are redeployed | the mode and its layer reach a live row, and an installation with old rules drops the field and warns instead of failing the entry (F40) | needs a deployed database and `fugaro init --firebase-project` |
| The pull request that fixes a filed item | the item's `status` flips and the index regenerates in that same commit, and the pin goes red if either is forgotten (D31) | a real pull request, reviewed as a diff |
| Two branches filing items on the same day, merged in either order | the id collision is a rename rather than a silent clash, and the index conflict is resolved by re-running the generator (D26, D28) | two real branches, two merges, and the provider's own conflict |

## 8. Items for the owner — not decisions this folder takes

**O1 — Calibration of "common" versus "rare" (the spec's own second open
question) is not solved by a scale.** Anchored definitions and examples get a
reviewer to a repeatable number; only measurement gets it to a *calibrated* one.
The plan's T2.4 samples scored findings from real runs and reports the
distribution, so the owner can move the thresholds on evidence instead of
intuition. This is deliberately not a decision: it is a measurement that produces
one.

**O2 — Whether Critical should require the independent local review the
credentials plan mandates.** Today a cheap in-loop model's `ship` can make a
Critical PR ready (F4, F30). The modes do not change that, and a reviewer of this
design will ask why the strictest mode is still gated by the weakest verdict in
the system. The honest answer is that this is
`docs/in-job-review/reconciliation.md` Q9's deferral (F33), and it belongs to the
owner rather than to this folder.

**O3 — Whether a mode may reduce the review round ceiling below today's default
of 2.** One round changes what "reviewed" means, and the difference in cost is
real. The plan's ceiling is multiplicative on the existing precedence, so the
answer is a number in a table — but it is the kind of number the owner should set
once, in the open, rather than a default an implementer picks.
*Owner ruling 2026-10-27:* the floor stays 2 (`minRoundCeiling`), and a
repository with no `rigor:` block keeps today's behaviour, with no ceiling.
*Owner ruling 2026-10-31, superseding the above:* a mode **may** go below two, and
the question is answered as **defaults** rather than as a ceiling. Each mode
supplies its own default counts — **POC 1/1, Startup 2/2, Pro 3/3, Critical 3/5,
Hotfix 1/1**, each pair being the first-line (inner) rounds and the senior rounds
the mode runs by default — and the `minRoundCeiling` clamp to two goes with the
answer. It is a default and not a cap: the task override, the recipe's
`max_rounds`, the repository's `agent.review_rounds` and `agent.first_line_rounds`,
and the project layer each set another number, up or down, within the recipe's
bounds (inner 1–3, senior 1–10), which are the only ceiling there is. A repository
with no `rigor:` block resolves Pro and runs Pro's row, 3/3, where it ran 1/2
before; `rigor.default: critical` is the pin a repository keeps the older
behaviour with. The names that meant a ceiling (`minRoundCeiling`,
`AdversarialCeiling`) go with the clamp, because a field never changes meaning,
and `internal/rigor/rounds.go` states the table under names that mean a default.

**O4 — What happens to a ready Hotfix PR nobody merges.** It sits ready and
unmerged, which is a state no other mode produces and nothing expires. Whether
the console should surface it, and whether the follow-up task D15 records should
fire from "still unmerged after N hours" rather than from a merge, are open.

**O5 — A provider issue as a backlog carrier needs a permission the App may not
hold.** The Workflows permission's absence is why `refused.go` exists (F12);
whether the App has issues-write is the same shape of question, and it should be
answered before D9's deferred carrier is planned rather than during it.

**O6 — The names.** `task.Spec.Mode` already means the recipe mode (F22), so the
rigor flag and config key need names that do not collide in a reader's head:
`--mode`/`rigor:` is the recommendation, `--rigor`/`rigor:` is the alternative,
and `--recipe-mode`/`mode:`-in-a-new-block is the one to avoid. A flag is a
compatibility surface — `internal/clidoc` generates `website/content/cli-reference.md`,
pinned by `internal/clidoc/cliref_test.go`'s `TestCliReferenceIsCurrent` — so the
name should be settled before the flag ships, not after.

**O7 — This folder's D-block is local, and the register's own rule is the reason
to say so out loud.** `docs/coordinator/README.md` §"The numbers this work holds"
is the one table in this repository that allocates D-numbers: **D1–D192 is the
owner's allocation, block by block**, and **D193 onward is free** — but that row
anchors its own figure (*"the highest number written anywhere in `docs/` at
`c6009b5f` is D192"*), and the anchor is the point: a figure like this moves, so
it is read anchored and name by name, the way
[docs/mobile/voice-session/](../mobile/voice-session/) reads it. Read at **this
work's base commit `1d325928`**, the register's own command
(`git grep -hoE '\bD[0-9]{2,3}\b' 1d325928 -- docs/ | sort -t D -k2 -n | tail -1`)
returns **D220**, because voice-session — merged at the fork point, the same work
whose **D219** the plan's check table cites — writes **D201–D220**. So **D193
onward is not the free range at this tree**, and the move named below is not
merely cosmetic: it would land this work's thirty-two decisions inside
voice-session's block, which is the collision both readings exist to avoid — a
number an earlier plan has already written is never taken. **D193–D200** are
written nowhere in this tree, but voice-session **left them alone** deliberately
and started at **D201** rather than collide with **D200**, which the owner's brief
records as an already-taken ruling this tree cannot cite (voice-session's F20 and
O5) — so they are not this work's to take on this tree's evidence either.

This folder numbers its decisions **D1–D32** anyway, which collides numerically
with `docs/setup-wizard/`'s D1–D12 and `docs/windows-cli/`'s D1–D16 —
the same choice the newer of those two made and recorded in its own index
("**Recommendation: keep D1–D16 local and distinct by folder, or move this work
to D193 onward; renumbering is mechanical and changes nothing but the numbers**").
The recommendation here is the same in its first half and corrected in its
second: keep D1–D32 **folder-local**, distinct by folder, as the two folders
above did; and if the owner prefers one sequence, the next free block read from
this branch's base is **D221 onward**, not the unanchored **D193 onward** that
voice-session's own index had to correct the same way. Either way the register
line should be updated in the same commit that settles it, because a reader who
greps for `D7` gets this folder's D7 and `docs/windows-cli/`'s D7 and has no way
to tell them apart except the path.

## 9. UNVERIFIED

Claims that this reconciliation could not check from the tree, with what would be
needed to check each. None of them is load-bearing for the plan's ordering, and
the plan says so where it touches one.

| # | Claim | What would verify it |
| --- | --- | --- |
| U1 | the reviewer prompt can be made to emit per-finding scores reliably on the configured model | a live review over a batch of real diffs, scored and compared |
| U2 | POC is in fact cheaper than Critical, in gateway prices | at least five finished runs per mode, read from the run records' `Budget` |
| U3 | GitHub's `author_association` covers every account the owner would trust for Hotfix | a live `Collaborator` call against the repository's `followup.trusted` |
| U4 | a ready PR gets labels and reviewers applied on the provider actually in use | a live `applyReady` against a provider, on a non-draft PR |
| U5 | an existing workflow reacts to a merge on `main` and would serve as the post-Hotfix full suite | reading the workflow files and observing one merge's CI |
| U6 | the Go-side `runner.VerdictSchema` string and the prompt's instructions to the model agree on the item's shape | a live review whose structured output is validated against the schema |
| U7 | the console tolerates a richer finding row without a contract change | the console's renderer run against a `fugaro diagnose --json` payload with scores |
| U8 | a narrower `check` gate for POC fits inside `fugaro.yaml`'s verify timeout on a real repository | a timed run of each mode's gate |
| U9 | an area rule matches the paths a real run actually touches at a moment when the run can still act on it (D22's moments are the tree's, not a measurement) | a live multi-round run whose diff crosses an area boundary, read from the record's own resolution note |
| U10 | adding a child to the registry entry is accepted by a deployed database after `fugaro init --firebase-project`, and refused readably before it (F40) | the rules deployed to a real project, then one run live in `fugaro watch` |
| U11 | an item filed by a run into its own branch is read by the reviewer of that pull request as a record rather than as a change under review (F44) | one real run that files an item, and a reading of that pull request's diff by a person |
| U12 | the generated index stays mergeable when two branches filed on the same day, in either merge order, without a hand edit (D28) | two real branches filing items, merged in one order and then the other |
