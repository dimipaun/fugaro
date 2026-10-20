# Reconciliation — rigor modes against the tree

**Status: plan only; nothing is implemented; decisions open.**

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

### 1.1 The ten things the documents settle, and where

| The spec's question | Settled in |
| --- | --- |
| The score model and the per-mode thresholds | §5, D1–D4, plan T2.1–T2.4 |
| Where mode config lives, and precedence | §5, D5, D6, D8, D20, D21, D25, plan T1.1–T1.6 |
| What each mode changes in the run loop | D7, D10, D11, plan T3.1–T3.5, T5.4 |
| The backlog, and where a tolerated finding goes | D9, plan T3.3, T3.4, T4.1–T4.2 |
| Hotfix | §5, D12–D15, plan T5.1–T5.5 |
| Cost accounting | D16, D17, plan T6.1–T6.3 |
| Security | §6, D18, plan T1.5, T3.5, T5.3 |
| Tests and the live checks the owner runs | §7, plan T8.1–T8.3 |
| Migration and the CLI/doc surface | D19, plan T1.6, T7.1–T7.3, T8.3 |
| The scope ruling of 2026-10-20: project, repo, area | §1.2, F35–F41, D20–D25, plan T1.1–T1.7 |

### 1.2 The scope ruling of 2026-10-20, and where each of its five parts lands

The owner ruled that the rigor mode "has a default per PROJECT, overridable per
REPO, and then overridable DYNAMICALLY per AREA". This is §1.1's tenth row: the
ruling changes where the mode's value comes from, and nothing else in this folder
— the score, the run loop, the backlog, Hotfix and cost are untouched by it. The
five parts, and the finding or decision that carries each:

| The ruling's part | Where it lands |
| --- | --- |
| Precedence `area > repo > project`, one resolver, one place that states it | D6, D20; the statement is `docs/project-layer.md` §2's own rule, one rank longer (F37), and the resolver is `internal/rigor`'s (plan T1.1) |
| Where each layer lives, and what an area is | D20, D21; the project layer's closed `defaults:` (F36), the repository's `rigor:` block, and `rigor.areas` as `{name, match, mode}` `doublestar` rules (F9) |
| What "dynamically" means | D22: chosen per run from the files it touches — declared at bootstrap where the caller knows them, measured from `runstore.Record.ChangedFiles` as they become known, monotone within the run (F38, F39) |
| The resolved mode, and which layer won, in the run, PR and watch output | D24: the record (`runview.Row`, `fugaro runs ls`, `fugaro diagnose`), the PR body's status section (`internal/runner/prflow.go`'s `finalSection`), and `fugaro watch`'s live row, which costs a registry child (F40) |
| A rule for a run that touches several areas | D23: the strictest matched area wins, and it is an **open decision** — the owner's own proposal, recommended here with its alternatives and their costs |
| (The re-cut the ruling asks for, which is a plan matter and not a design one) | §2 of the [plan](implementation-plan.md): Slice 1's T1.2, T1.3 and T1.4, in that order |

Two things the ruling does not change, and this amendment does not touch: the
spec stays verbatim (its own commit, `37ea0d9d`), and every decision below stays
**open** — D20–D25 are recommendations with alternatives, exactly like D1–D25.

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
consumer.** Class: **Wrong**.
`runner.VerdictSchema` declares `severity` as `{"type":"string"}`; `runner.Finding`
carries it as a plain `string`; nothing in the repository reads it — not
`runner.Decide`, not `runner.prflow`'s `reviewsPart`, not `internal/cli/diagnose.go`,
which prints it and stops. Consequence: the spec's premise "severity acts as an
override" has no attachment point today. The score model arrives as new fields,
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
`fugaro.yaml` sets `agent.review_rounds` (default 2, range 1..10);
`internal/task/task.go`'s `Spec.Overrides.ReviewRounds` wins over it; a recipe's
`max_rounds` participates through `planOf`. `docs/recipes.md` documents the
precedence. Consequence: "max fix rounds" per mode must say which of these it
sets. The recommendation is that a mode sets a *ceiling* that the existing
precedence then operates beneath, so no existing task file changes meaning (D7,
plan T3.2).

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
`config/policy/shared` (`fugaro project policy publish`, version 3);
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
`fugaro image refresh`. There are eleven such constants. Consequence: the task
file's `rigor:` key cannot ship before the image that validates it, and the plan
orders the two in one slice with the skew test as its verification (plan T5.1).

**F24 — The recipe loop cannot express "this finding is tolerated".** Class:
**Gap**.
`internal/recipe` parses steps of kind `first_line`, `review`, `check`, `plan`,
`render`; `recipe.Parse` *refuses* `goto`, `on_reject`, `extends`, `on_pass`,
`on_fail`, `model`, `models` as reserved keys; the catalog holds eight recipes
(`default`, `solo`, `standard`, `premium`, `review-only`, `pick`, `test-and-fix`,
`lint-fix`). Nothing in a recipe sees a finding. Consequence: the modes are not
five new catalog recipes, and shipping them as five YAML files would be a
feature that looks implemented and changes nothing. The mode selects a recipe for
its *check* gate and a policy for its *verdict*; the policy is Go (plan T2.1,
T3.1).

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
rot.

**F31 — Independent review for security-sensitive work is already written down.**
Class: **Exists**.
`docs/credentials-service/implementation-plan.md` Global Constraint 10
(SECURITY-SENSITIVE → opus, plus a mandatory independent review), and
`docs/dogfooding.md`'s money-and-security rule. Consequence: Critical's
"independent review" requirement is not a new invention, and the mode table cites
the constraint instead of restating it. It also means Critical is already
*partly* the default for that tier by policy, which is why migrating with Critical
as the default relaxes nothing (D18, D19).

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
score changes a model's behaviour at all; that `agent.review_rounds`'s default of
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
`internal/runner/project.go` says why that read exists: a review never trusts a
pull request's own `fugaro.yaml`. Consequence: `fugaro run --ref feature-x`
resolves its mode from `feature-x`'s own `rigor:` block unless the design says
otherwise, so a branch can set the rigor of the run that is about to review it.
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
published until its rules are redeployed with `fugaro init --firebase`
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
operator to run `fugaro init --firebase`, and remembers for the session
(`noRecipe`); and the creds family's, where `internal/budget/rules/marker.go`
records a marker and the run halts on a downgrade
(`HaltCredsRulesDenied`). Consequence: showing the mode in `fugaro watch` costs a
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

## 4. The census, and what it is scoped to

Every count below is stated with the command that produces it, because a count
without its command is a figure a later reader cannot re-derive. All commands are
run from the repository root against the working tree of this folder's own commit,
which differs from the base `1d325928` by this folder's four documents and its pin
test and nothing else — so one row is the folder's own: the 48 pins are 47 other
folders' plus `internal/cli/docs_rigor_modes_test.go`.

| Construct | Count | Command |
| --- | --- | --- |
| Catalog recipes | 8 | `ls internal/recipe/catalog/*.yaml \| wc -l` |
| Top-level `fugaro.yaml` keys | 9 | `sed -n '/^  "properties": {/,/^  }/p' schemas/fugaro.schema.json \| grep -c '^    "[a-z_]*": '` |
| Top-level `fugaro.yaml` keys whose name contains `review` | 0 | `sed -n '/^  "properties": {/,/^  }/p' schemas/fugaro.schema.json \| grep -c '^    "[a-z_]*review[a-z_]*": '` |
| Config scope rows | 68 | `sed -n '/Scopes = \[\]ScopeRow{/,/^}/p' internal/config/scope.go \| grep -c '{Key:'` |
| Layers a configuration key may be set in | 4 | `sed -n '/The layers a key may be set in/,/^)/p' internal/config/scope.go \| grep -c 'In[A-Z]'` |
| Blocks the project layer's `defaults:` may set | 2 | `sed -n '/type LayerDefaults struct {/,/^}/p' internal/config/layer.go \| grep -c 'yaml:'` |
| Fields of `task.Spec.Overrides` | 4 | `sed -n '/type Overrides struct {/,/^}/p' internal/task/task.go \| grep -cE 'yaml:\|json:'` |
| Fields of `task.Spec` or `recipe.Step` whose name is a path | 0 | `{ sed -n '/^type Spec struct {/,/^}/p' internal/task/task.go; sed -n '/^type Step struct {/,/^}/p' internal/recipe/recipe.go; } \| grep -cE '^[[:space:]]+[A-Z][A-Za-z]*(Path\|Glob\|Dir)'` |
| Sites that compare a finding's severity to a value | 0 | `git grep -n 'Severity ==' -- internal/runner ':!*_test.go' \| wc -l` |
| Files in `internal/runner` that mention `Severity` at all | 3 | `git grep -l 'Severity' -- internal/runner ':!*_test.go' \| wc -l` |
| Image-skew `Since` constants | 11 | `git grep -hc 'Since = "' internal/cli/recipes_skew.go` |
| Run outcomes | 4 | `git grep -ho 'Outcome[A-Za-z]* *= *"[a-z]*"' internal/runstore/runstore.go \| wc -l` |
| Docs folders that ship their own `internal/cli/docs_*_test.go` pin | 48 | `ls internal/cli/docs_*_test.go \| wc -l` |
| Questions the in-job-review folder put to the owner, all answered | 17 | `git grep -hc '^\*\*Q[0-9]*\. ' docs/in-job-review/reconciliation.md` |
| Published project-policy version | 3 | `sed -n 's/^const Version = //p' internal/projectpolicy/version.go` |

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

The four rows the 2026-10-20 amendment added are the layering ones: **4** layers
a key may be set in (F37), **2** blocks the project layer's closed `defaults:`
may set (F36), **4** fields of `task.Spec.Overrides` and **0** fields of
`task.Spec` or `recipe.Step` whose name is a path (F38). The last row counts
*declared fields*, which is why its command matches a field name at the start of
a line rather than any mention: the `Spec` struct's prose mentions a path, and a
count of prose is not a count of inputs.

The **48** is the convention this folder follows: there is no registry test over
`docs/`, so a folder that wants its documents pinned writes its own file, and
this folder's is `internal/cli/docs_rigor_modes_test.go` — the forty-eighth, and
the one row in this table that the folder's own commit moves.

## 5. Decisions for the owner

Each decision states the recommendation first with its reason, then the
alternative and what it costs. **None of these is taken.** Nothing below is
implemented, and the plan's tasks are not approved.

### D1 — The scoring scale: 1–5 per axis, anchored

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
the honest design is a per-mode word list. The plan's T2.2 writes the anchors;
if the owner prefers 1–3 the table in T2.3 is rewritten, not redesigned.

### D2 — What `blocker`/`high`/`medium`/`low`/`nit` are: labels, not inputs

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
three-axis scoring section of the spec becomes prose. If the owner wants the
words as inputs, the design loses fix cost entirely, and the recommendation is
to say so in the spec rather than in the code.

### D3 — Where the score lives: new optional properties on the finding item

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
`fugaro init --firebase`, and a mode default is not worth that (`marker.go`'s
warning is the reason the rule exists).

**Recommended, part three: a *floor* the project's repositories cannot edit
belongs in the published project policy, not in the project layer.** The two are
not the same thing and the tree already distinguishes them: `docs/project-layer.md`
§2's "the budget ceiling is the owner's, outside every layer, and only tightens"
is the published policy's sentence, and the project layer is a *file in the
project's Git repository*, which a repository admitted to the project can change
by merging a pull request. So the project-wide **default** is a layer value
(part one) and a project-wide **floor** is policy — with the version bump F36
describes as its price. Which of the two the owner wants for the security tier is
D18's question, not this decision's, and the first cut ships with the built-in
set alone (T1.5) so the answer can land later without a migration.

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

### D7 — What each mode changes in the run loop

**Recommended: Critical is byte-identical to today.** Every other mode keeps the
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

### D8 — Who may lower a floor: the trusted list, acting through a merged config change

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

### D9 — The backlog: the record, the PR, and `fugaro diagnose`; the ledger and a provider issue come later

**Recommended first cut: the tolerated findings are (a) persisted on the run
record — the finding objects, extending the work
`docs/in-job-review/reconciliation.md` Q8 already decided (F19, F20); (b) listed
in the PR body's existing status section as "left open by policy", with the mode
that left them (F28); and (c) recoverable by `fugaro diagnose`, whose finding row
already exists and whose JSON contract §6.1 is already the console's input
(F18).** Deferred to a later slice, with the precedent named: a `docs/`-style
ledger file (the pattern in `docs/plans/2026-10-13-project-secrets.md` and
`docs/plans/2026-10-17-managed-projects-review-follow-ups.md`, F32) and a
provider issue.

Reason: the three carriers in the first cut are one storage change, one sentence
in a section that already exists, and zero new surfaces; they are also the three
surfaces the owner already reads. The ledger and the issue each need something
that does not exist yet — a decision about which pull request a ledger entry
lands in and who merges it, and an issues permission the App does not hold — and
neither is needed for a single mode to be usable.

**Alternative: the `docs/`-style ledger first.** Cost: a run that writes a ledger
file either dirties the PR it is about or opens a second PR, and the second PR
has its own merge policy, its own format and its own pin test — all before the
first mode ships. It is the carrier that survives an ephemeral job best, which is
the argument for it; against that, the run record is already durable and already
read back by a command.

**Note on `version`:** `schemas/result.schema.json` is closed and pinned to the
record by `TestResultSchemaAcceptsRunnerRecords` (F20), so the new fields land
with their schema entries in one commit. The recommendation is to **keep
`version: 1`**: `internal/runstore/runstore.go` decodes with plain
`json.Unmarshal` and no reader refuses on the const (F21), so a bump would be a
compatibility claim nothing enforces, while the additive field is genuinely
compatible.

### D10 — The verify stage's gate is the recipe's `check`, not a mode switch

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

### D11 — Draft or ready, per mode

**Recommended: every mode opens a draft PR except Hotfix, which opens ready under
D12's gate.** Critical, Pro, Startup and POC all draft, and a person marks ready
after reading the run's status section — exactly today's practice
("check the PR's CI, mark it ready and merge", F30). Reason: a POC run's `ship`
comes from the same cheap reviewer as a Critical run's, and marking a POC PR
ready is precisely the substitution the owner refuses ("A run's own `ship` never
replaces it", F30). The mode changes what the run *tolerates*, which is visible
in the body; it does not change who decides to merge. Hotfix is the one exception
because time is its entire justification (D13).

**Alternative: POC and Startup open ready when their policy tolerates every
finding.** Cost: a scaffold PR and a payment-adjacent PR look identical to
whatever automation reads PR state, and the difference between them exists only
in a body a machine does not read. The convenience is real — a POC PR that
nobody has to click through — and it is the one place where this design's
recommendation is deliberately inconvenient.

### D12 — Who may invoke Hotfix: an operator, with a base-branch opt-in

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

**Recommended: the agent's stated confidence is recorded on the run and never
gates anything. The gate is a deterministic rule over the diff and the record: a
Hotfix may not touch `.github/workflows/`, a credential or IAM path, or any path
with a floor above the allowed mode; the diff's size and file count bound it; and
the compile plus the targeted test must pass. When the rule cannot decide, it
takes the conservative branch (more checks, not fewer).** Reason: an agent
grading its own confidence is the weakest signal in the system — it is the point
the spec itself flags as an open question, and it is the same class of signal the
owner already distrusts ("the in-loop reviewer is a cheap model too", F30).
Making the gate depend on the diff means the gate is checkable by a reader who
has the diff, which is what makes a post-hoc audit of a Hotfix possible at all.

**Alternative: let the agent's confidence number pick the branch (high → no
pre-push tests; low → compile and a targeted test).** Cost: a model's self-report
becomes the control on the one path with no review, and the failure is silent —
a confident wrong fix ships with the run record agreeing that it was confident.
It is also unmeasurable afterwards: nothing in the record can distinguish "the
agent was right" from "the agent was sure".

### D15 — The automatic follow-up: recorded now, scheduled later

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

**Alternative: a scheduled job that scans merged Hotfix PRs and opens the
follow-up task.** Cost: a scheduler, its credentials, its own failure mode
(a scheduler that stops is invisible), and a new surface for the trusted list to
be wrong about — in exchange for removing one paste. Worth doing later, and worth
deciding on its own merits rather than as a footnote to this mode.

### D16 — Cost: modes lower the ceiling, never raise it

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
Critical as the default relaxes nothing; and the built-in part matters because a
repository that forgets to declare its credential paths would otherwise run them
at whatever its default is — the refusal in `internal/runner/refused.go` is the
precedent for a protection that does not depend on the repository remembering.

**Alternative: sensitive paths are declared in configuration only, with no
built-in set.** Cost: one absent line in a config file is the difference between
a credential path running at Critical and running at POC, and the failure is
silent and one-way. The cost of the recommendation is that a repository cannot
*lower* `.github/workflows/`, which is not a cost — that refusal is not
configurable today and should not become so (F12).

### D19 — Migration: the default is Critical, and the first cut relaxes nothing

**Recommended: the default mode is Critical — today's behaviour, byte for byte —
until the owner sets another, and the entire first cut ships with every path on
Critical. A repository with no `rigor:` block behaves exactly as it does today.**
The CLI and documentation surface: `fugaro run --mode`, the `rigor:` config and
policy keys, `fugaro doctor` checking that the mode vocabulary and the floors are
self-consistent, and `fugaro diagnose` printing the mode of the run it read.
Reason: the spec's whole premise is that modes differ in what happens to
findings; a default of Pro would change what "ready" means for every repository
at the moment the code merges, with nobody having asked for it. Critical being
the default also means the first cut's risk is bounded by construction — with
nothing configured, no behaviour changes at all, which is the property that lets
each slice of the plan merge alone. It is also the only default consistent with
F31: the security-sensitive tier is already mandated to the strictest treatment.

**Alternative: default to Pro.** Cost: every existing repository's runs start
tolerating findings they were failing on, and the change arrives as a difference
in behaviour rather than as a difference in configuration. The argument for it is
that Critical-everywhere is the problem the spec exists to solve, and a default
that does not change anything means the feature helps nobody until they opt in —
which is true, and is the reason the plan's T0.1 and T7.1 put the owner's own
`fugaro.yaml` in front of them as the first thing to set.

### D20 — Where each layer's key lives, and how an area is named and matched

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
YAML map cannot carry the one comment that explains why a rule exists beside the
rule.

**Alternative: areas as a directory tree, one config file per area.** Cost: a
second configuration surface with its own schema, its own trust question and its
own load order, for a shape the repository does not use anywhere; and an area
file inside the tree is a file the branch under review can write (F35, F41).

### D21 — Whether a narrower layer may lower

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
being a mode that arrived too late to be used. It is one call site fewer, and it
is what the plan's T1.4 would build if the owner prefers it.

**Alternative: resolve from `plan.Footprint` — the planner's prediction — at
bootstrap, and skip the declared paths.** Cost: nothing a run executes reads that
file today (`internal/plan/footprint.go` says it is advisory and `fugaro plan
graph` is its only reader), so a run would gain a dependency on a plan artifact
that may not exist, may be stale, and covers the files a *plan* files changed
rather than the files a run will.

### D23 — A run that touches several areas: strictest wins (an open decision)

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

### D24 — What each surface shows, and which value it shows

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
decision where the owner weighs a deploy against seeing a live run's rigor.

**Alternative: watch reads the mode from the machine that launched the run.**
Cost: does not work — a run executes on a Cloud Run job and watch reads the
database for runs on any machine, which is why the registry exists at all.

### D25 — Which revision of the configuration the mode is read from

**Recommended: the base branch, for every run kind, including a first run.** That
means a new read beside `internal/runner/followup.go`'s `readBaseConfig` (which
already fetches `origin/<ref>` and reads `fugaro.yaml` with `repo.ShowFile`) for
the mode's configuration, rather than the working tree's copy that
`internal/runner/runner.go`'s bootstrap reads today (F35). Reason: the mode
decides how much review the PR gets, and a value that decides that must not come
from the artifact under review — which is precisely why `readBaseConfig` exists
for a review run and a follow-up, as `internal/runner/project.go` states. A
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

**Alternative: the checked-out ref, for a first run only.** Cost: free (the file
is already read), consistent with every other key a first run uses, and it means a
branch merges together with the rigor it was developed under — which is a genuine
argument. What it accepts: a branch sets its own mode until the floors stop it,
and the floor set is the only backstop. This is the alternative to weigh against
the recommendation, and neither is taken here.

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
| `fugaro watch` on a live run after the rules are redeployed | the mode and its layer reach a live row, and an installation with old rules drops the field and warns instead of failing the entry (F40) | needs a deployed database and `fugaro init --firebase` |

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
owner's allocation, block by block**, the highest number written anywhere in
`docs/` is **D192**, and **D193 onward is free**. This folder numbers its
decisions **D1–D25** anyway, which collides numerically with
`docs/setup-wizard/`'s D1–D12 and `docs/windows-cli/`'s D1–D16 —
the same choice the newer of those two made and recorded in its own index
("**Recommendation: keep D1–D16 local and distinct by folder, or move this work
to D193 onward; renumbering is mechanical and changes nothing but the numbers**").
The recommendation here is the same: keep D1–D25 **folder-local**, distinct by
folder, or move this work to **D193 onward** if the owner prefers one sequence.
Either way the register line should be updated in the same commit that settles
it, because a reader who greps for `D7` gets this folder's D7 and
`docs/windows-cli/`'s D7 and has no way to tell them apart except the path.

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
| U10 | adding a child to the registry entry is accepted by a deployed database after `fugaro init --firebase`, and refused readably before it (F40) | the rules deployed to a real project, then one run live in `fugaro watch` |
