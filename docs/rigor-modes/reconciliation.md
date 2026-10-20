# Reconciliation — rigor modes against the tree

**Status: plan only; nothing is implemented; decisions open.**

The owner's [spec](spec.md) was written without seeing this repository. This
document is what the tree says back: every claim the spec makes, checked against
the code that would have to carry it, with the consequence for the design. The
[implementation plan](implementation-plan.md) is written from the findings here.

## 1. Checked against, and how

- Base: `main` at `54cb02a8`'s successor — the commit this document was read at
  is `1d325928` (the branch point of this work), and the running tree is the one
  the reader has.
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

### 1.1 The eight things the documents settle, and where

| The spec's question | Settled in |
| --- | --- |
| The score model and the per-mode thresholds | §5, D1–D4, plan T2.1–T2.4 |
| Where mode config lives, and precedence | §5, D5, D6, D8, plan T1.1–T1.4 |
| What each mode changes in the run loop, and the backlog | D7, D9, D10, D11, plan T3.1–T3.5 |
| Hotfix | §5, D12–D15, plan T4.1–T4.5 |
| Cost accounting | D16, D17, plan T5.1–T5.3 |
| Security | §6, D18, plan T1.2, T3.1, T6.4 |
| Tests and the live checks the owner runs | §7, plan T6.1–T6.5 |
| Migration and the CLI/doc surface | D19, plan T0.1, T6.2, T7.1–T7.3 |

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
`review:` block is not merely absent: `internal/config/scope_test.go`'s
`TestScopeTableMatchesDocs` and `internal/config/testdata/config/invalid/review-allow-forks.yaml`
are fixtures that would go red if one appeared. So the reconciliation of "the
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
`internal/runner` holds `MaxReviewRounds = 10` and `MaxFirstLineRounds = 3`;
`fugaro.yaml` sets `agent.review_rounds` (default 2, range 1..10);
`internal/task/task.go`'s `Spec.Overrides.ReviewRounds` wins over it; a recipe's
`max_rounds` participates through `planOf`. `docs/recipes.md` documents the
precedence. Consequence: "max fix rounds" per mode must say which of these it
sets. The recommendation is that a mode sets a *ceiling* that the existing
precedence then operates beneath, so no existing task file changes meaning (D7,
plan T1.4).

**F7 — Mode config cannot reuse a `review:` block, and adding a top-level key is
five edits.** Class: **Wrong** (see C1) — and the five edits are the Rule part.
`internal/config/config.go` (the struct), `internal/config/defaults.go`,
`internal/config/scope.go`'s `Scopes` table (pinned by `TestScopeTableMatchesDocs`
in `docs_project_layer_test.go`, which holds `docs/project-layer.md` in step),
`schemas/fugaro.schema.json` (hand-written, `additionalProperties: false`, no
generator) and a corpus case under `internal/config/testdata/config/valid/`.
Consequence: the config slice is one commit with all five, with its own red step;
a schema-only or struct-only change is not mergeable and would go red in the
corpus tests rather than in review (plan T1.1).

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
author. `followup.Select` reads the nonce-delimited comment block;
`followup.allow_public` is the owner's explicit 2026-10-04 decision. Consequence:
"who may lower a floor" and "who may invoke Hotfix" reuse this list rather than
growing a second one, and because it is read from the base branch it is
unreachable from a fork (D8, D12).

**F14 — A follow-up is not a fresh task.** Class: **Rule**.
`fugaro run --pr N` selects a trusted comment and continues an existing PR;
`followup.Select` is the only way a comment becomes work. Consequence: a
follow-up's mode is the PR's recorded mode, and the follow-up path must be
unable to lower it. `--mode` on a follow-up may only raise, and the design says
so where the flag is defined rather than in prose (D6, plan T4.3).

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
turns that from a data bug into a parse error (D19, plan T4.2).

**F23 — A new task field needs an image-skew gate.** Class: **Rule**.
`internal/cli/recipes_skew.go`'s `…Since` constants and `checkImageSince` gate a
task field on the image that understands it; the remedy printed to the user is
`fugaro image refresh`. There are eleven such constants. Consequence: the task
file's `rigor:` key cannot ship before the image that validates it, and the plan
orders the two in one slice with the skew test as its verification (plan T4.2).

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
`internal/runner/prflow.go`'s `finalize` builds `gitprov.PRSpec{Draft: !ready}`;
`settlePR` and `applyReady` apply labels and reviewers *only* for a non-draft PR.
There is no merge call in `gitprov`. Consequence: Hotfix's "ship immediately"
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
review rather than in production. The plan's T0.1 is a document that does exactly
this mapping.

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
the gateway's prices make POC cheaper in fact (U2). Seven of those eight are the
UNVERIFIED table's, which is the point: an unverifiable claim is an item, not a
finding.

## 4. The census, and what it is scoped to

Every count below is stated with the command that produces it, because a count
without its command is a figure a later reader cannot re-derive. All commands are
run from the repository root against the tree at `1d325928`.

| Construct | Count | Command |
| --- | --- | --- |
| Catalog recipes | 8 | `ls internal/recipe/catalog/*.yaml \| wc -l` |
| Top-level `fugaro.yaml` keys | 9 | `python3 -c "import json;d=json.load(open('schemas/fugaro.schema.json'));print(len(d['properties']))"` |
| Top-level `fugaro.yaml` keys whose name contains `review` | 0 | `python3 -c "import json;d=json.load(open('schemas/fugaro.schema.json'));print([k for k in d['properties'] if 'review' in k])"` |
| Config scope rows | 68 | `sed -n '/Scopes = \[\]ScopeRow{/,/^}/p' internal/config/scope.go \| grep -c '^\t{'` |
| Sites that compare a finding's severity to a value | 0 | `git grep -n 'Severity ==' -- internal/runner ':!*_test.go'` |
| Files in `internal/runner` that mention `Severity` at all | 3 | `git grep -l 'Severity' -- internal/runner ':!*_test.go'` |
| Image-skew `Since` constants | 11 | `git grep -c 'Since = "' internal/cli/recipes_skew.go` |
| Run outcomes | 3 | `git grep -ho 'Outcome[A-Za-z]* *Outcome *= *"[a-z]*"' internal/runstore/runstore.go \| wc -l` |
| Docs folders that ship their own `internal/cli/docs_*_test.go` pin | 47 | `ls internal/cli/docs_*_test.go \| wc -l` |
| Questions the in-job-review folder put to the owner, all answered | 17 | `git grep -c '^\*\*Q[0-9]*\. ' docs/in-job-review/reconciliation.md` |
| Published project-policy version | 3 | `sed -n 's/^const Version = //p' internal/projectpolicy/version.go` |

Three of those rows are worth reading rather than counting.

The **zero** is F1 and F2 in one figure: `internal/runner/prompts.go` and
`internal/runner/reviewmode.go` each read `f.Severity` only to print it as a
label, and no comparison against any value exists in the package. A mode
threshold table has nothing to attach to today.

The **three** files are `verdict.go` (the schema string, the field, the
fallback), `prompts.go` (the label) and `reviewmode.go` (the label again) — that
is the entire surface a scoring change touches inside the runner, which is why D3
is as small as it is.

The **47** is the convention this folder follows: there is no registry test over
`docs/`, so a folder that wants its documents pinned writes its own file, and
this folder's is `internal/cli/docs_rigor_modes_test.go`.

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

### D5 — Mode config lives in `fugaro.yaml`, under a new top-level `rigor:` block

**Recommended: a top-level `rigor:` block in `fugaro.yaml` for the default mode
and the path rules, and the published project policy for the project-wide
floors — the same pair the repository already uses for configuration and policy
(`internal/config/resolve.go`'s `Resolve`, `config.LayerKey`,
`config/policy/shared`).** Reason: the spec asks whether mode config lives in the
project's Git repository alongside recipes, and the answer the tree gives is
"yes, and there are already two layers of it" (F8): the repository's
`fugaro.yaml` is where a mode is set for this repository, the project policy is
where it is set for every repository in the project, and `Resolve` already
defines what happens when both speak. Adding a separate `rigor.yaml` would need
its own schema, its own corpus tests, its own layer entry and its own pin, for no
capability the existing file lacks.

**Alternative: a `rigor.yaml` beside recipes.** Cost: a second configuration file
in the same directory with a second schema and a second validation path, and the
question "which one wins for a path rule?" gets a new answer invented for a file
that did not need to exist. The one thing it buys — a file whose owner is the
mode system alone — is a real argument, and this is the decision where the owner
weighs it.

### D6 — Precedence: task request ∩ path floors, project default beneath

**Recommended: resolve the mode as `max(project default, path floors for the
declared paths, task request)`, where a task may only *raise* the mode, and the
strictest floor wins when the diff touches several paths.** The path rule's floor
can be raised by a task; it can only be lowered by changing the configuration on
the base branch (that is, by a merged pull request), never at run time. Patterns
are `doublestar` globs validated by `internal/config/validate.go`'s
`doublestar.ValidatePattern`, and the same matcher used everywhere else (F9).

**Recommended, part two: the resolution happens twice.** At bootstrap, from the
task's declared paths and the base branch's configuration; and again before the
PR is marked ready, against `runstore.Record.ChangedFiles`. A path that turns out
to be floored raises the mode, the run says so in the PR body, and the extra fix
rounds the higher mode wants become the reason the run is a draft rather than a
silent merge of tolerated findings (F11). Reason: the mode has to be known before
the work, and the diff is only known after it; doing both is the only way to have
a floor mean anything and still fail closed.

**Alternative: resolve the mode from the finished diff only.** Cost: the entire
run was done at the wrong rigor; by the time the floor is noticed the fix rounds
are spent and the choice is between discarding the run and shipping the wrong
rigor. Cheaper to code, and it makes a locked payments path a suggestion.

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
otherwise. The plan's T5.3 adds the mode to the run record's own reporting so the
empirical line can eventually be per mode — that is the honest path to a per-mode
number.

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
3. **Floors win.** The mode is `max(project default, path floors, task request)`
   (D6). A task may raise a floor and never lower it; a follow-up keeps its PR's
   recorded mode and `--mode` may only raise it (D6, F14); a comment never
   participates in the resolution (D8, D12).
4. **Critical is the default for the security-sensitive tier**, with a built-in
   set (`.github/workflows/`, credential and IAM paths) plus the repository's
   declared globs (D18).
5. **The mode used is recorded in the PR body and in the run record** (D18), from
   one value, so a mismatch is a bug rather than a documentation drift.
6. **Fail closed.** An unscored finding, an unparseable verdict, or a policy that
   cannot decide blocks in every mode (D4, F3).

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
