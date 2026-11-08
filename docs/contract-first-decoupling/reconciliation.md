# Contract-first task decoupling: reconciliation

Checked against `origin/main` at `a7eb6ca28` on 2026-11-08. Numbers: **D1810-D1839**, **checks 480-489**
(see the README). **Where this file disagrees with `spec.md`, this file wins.**

The spec was written without seeing the repository. It reads as if Fugaro had a planner program that builds
a task graph and could be given a new pass. It does not: the planner is the user's own coding agent,
following a Fugaro-owned plugin skill, and the only machine-read part of planning is the graph artifact
the CLI validates. That changes where every part of the spec lands.

## 1. What the spec names, and what the repo already has

| Spec concept | What the repo already has |
| --- | --- |
| "The planner builds the task graph" | No planner code. The agent follows `plugin/skills/parallelism/SKILL.md` and writes `<plan>.graph.json` beside its plan; `internal/plan` parses and validates it, `fugaro plan graph` prints it |
| Dependency edge A to B with a reason | `plan.Edge{Task, Needs, Reason}`: the reason is already required |
| Contracts first, stubs and fakes | Already two of the three job-max techniques in the `parallelism` skill (docs/review-levels-ab-jobmax D729). The spec adds the procedure around them, not the idea |
| Critical path before and after | `plan.Graph.CriticalPath`, `Levels`, per-node `critical_path_length` and `downstream` (`internal/plan/analyze.go`); counted in tasks, no sizes |
| Rigor levels POC / Startup / Pro / Critical / Hotfix | `internal/rigor` (`rigor.Mode`, `rigor.Resolve`): project < repo < area, and a per-run request may only raise |
| "Review contract tasks at least at the parent's rigor" | `fugaro run --mode <mode>` is raise-only (`internal/cli/run.go`), which is exactly "at least" |
| Bundled planner skill, on by default | The plugin's five skills; `TestSkillSetIsFive`. `parallelism` is the planning skill |
| "Turn off or tune per project, area, repo or task" | The settings registry (`internal/settings/registry.go`) and the two config layers; no skill-level switch exists |
| Notify dependents and have them rebase | Nothing automatic. `fugaro run --pr N` follow-up runs exist; the orchestrator tick (docs/stateless-orchestrator O5/O7) is unmerged |
| Post-review triage personas | Not in the tree (UNVERIFIED, F14) |

## 2. Findings

**F1 (Exists): there is no planner to add a pass to.** `git grep` finds no planner in `internal/` or
`cmd/`; `internal/plan` only reads the artifact a planner writes. The decoupling pass is therefore *skill
text* the user's agent follows, plus CLI support for what the agent writes (the graph) and what a person
or a launch reads back. Consequence: the acceptance "the planner inserts contract tasks" is satisfied by
the skill and the schema, and is verified by tests on the graph and on the skill's own text, never by a
live agent.

**F2 (Exists): contracts first and stubs are already advice.** The `parallelism` skill's "Job max" and
"How to split" sections say "settle shared decisions ... before writing a task" and "a task that consumes
another's work is written against a stub and lands without the edge". What is missing is the
edge-by-edge test, the contract task's contents, the integration task, and what happens when a contract
is wrong. `/jobmax` is described in `docs/review-levels-ab-jobmax` (J1.2) and the skill's own text, but no
`jobmax` skill directory is present under `plugin/skills` (UNVERIFIED whether it is built elsewhere).

**F3 (Rule): the plugin carries exactly five skills.** `plugin/plugin_test.go` `TestSkillSetIsFive` pins
the set (setup, working, routing, parallelism, upgrade), and `plugin/skills_lint_test.go` bounds a skill
to 400 lines and 24 KB and a reference file to 300 lines and 16 KB. `parallelism` is 89 lines. A sixth
skill changes the pinned set and every doc that says "five". **D1810** extends `parallelism` with one
section and a new `reference/decoupling.md`.

**F4 (Gap): the graph cannot say what the spec needs.** Schema 2 (`internal/plan/document.go`) has plain
nodes and edges. It has no node kind, no record of a broken edge, no stub list, no size, and no change
log. `Parse` decodes with `DisallowUnknownFields` and refuses a future `schema_version` with "upgrade
fugaro". So any new field is a schema bump, and a teammate on an older CLI is *refused*, not shown a
degraded graph. `console/docs/json-contract.md` section 6.9 pins "schema_version `1` or `2`" and the key
tables, so the contract page and its tests change in the same PR as the CLI. The skill may teach schema 3
only in a release whose CLI reads it (`plugin` tests check every command and flag a skill names against
the CLI). **D1811** takes schema 3.

**F5 (Wrong, and the headline): breaking one edge can make the plan slower.** For a chain A to B the
critical path is 2 tasks. After the pass it is C, then A and B in parallel, then the integration task:
3 tasks. The spec's measure ("critical path length before and after") counted in tasks reports a *loss*
for the simplest case, and the graph has no sizes to say a contract task is small. Breaking an edge pays
only when A and B are large against C and the integration task. **D1816** adds an optional relative
`size` and reports both the task-count path and the weighted path, and the skill breaks an edge only when
the weighted path shrinks.

**F6 (Rule): per-task "off" lowers a layer, and rigor only raises.** `rigor.Resolve` composes project, then
repo, then area (areas replace the value below them in either direction, D21), and a run's own
`--mode` may only raise (`Known(req.Mode) && req.Mode.Rank() > mode.Rank()`). The spec wants "off or tune
per project, area, repo or task following the existing precedence". A per-task off would be a lowering
request, which the resolver refuses by design. **D1813** keeps the rigor mode as the tuning (the pass's
behaviour is a function of the task's resolved mode, read from its footprint paths) and adds one
on/off setting at project and repo.

**F7 (Rule, version skew): a new config key is a skew event.** `fugaro.yaml` and the project layer decode
strictly in the container's runner; a runner older than the key refuses it. The repo already carries this
as a pattern (`gitAuthorSince`, `noneModelSince` in `internal/cli/recipes_skew.go`, and the
`fugaro update --repo` gate, `gateGitAuthor` in `internal/cli/init.go`). CLAUDE.md's hard rule applies:
a release must not ship an operator step, so `fugaro update` must learn the gate in the same release and
a test must show an installation at the previous version converges. Whether an older runner also refuses
an unknown `defaults.*` key in the project layer is UNVERIFIED (U2); the plan assumes it does.

**F8 (Gap): no automatic way to tell a running task its contract changed.** The runner has no rebase
path from outside, and `fugaro run --pr N` continues a PR's branch; its instructions are trusted text and
must not relay PR comments (`plugin/skills/working/reference/followup.md`). A fix round may merge the base
branch in (`internal/runner/diffbase.go` handles exactly that case). CLAUDE.md forbids merging `main` into
a branch with a cloud follow-up run if `main` changed `.github/workflows/`. **D1812** therefore makes the
amendment a plan node and drives dependents by follow-up runs whose instructions *name the contract file
and ask the run to merge the base and conform*, never paste the contract's text.

**F9 (Gap): "no stubs remain" needs a definition a command can check.** `plan.Footprint` is documented as
a *prediction*, and a run's real changes are the run's own record, never the graph. A marker comment is
discouraged by this repository's comment rule (CLAUDE.md "No TODO comments, no section-header
comments"). **D1815**: stubs live in whole files the contract names; the integration task deletes them;
the read reports which of them still exist. Stub paths are literal repo-relative paths, not globs, and are
untrusted plan text: reject absolute paths, `..` elements and symlinks that leave the checkout.

**F10 (Gap, trust boundary): a merged contract is a single point both sides trust, and nothing guards it.**
Nothing mechanical stops a run from editing a merged contract file to suit itself: the graph footprint
is advisory, and the check of a run's real changed files against its plan footprint does not exist. A
hostile or confused agent in task A could change the contract B was built against, and the review would
see only A's diff. The mitigations this plan takes are procedural: contract tasks are launched with
`--mode` at least the highest rigor of the tasks that consume them, security-relevant contracts (auth,
credentials, IAM, wire formats) go to `opus + mandatory independent review`, a contract's files appear
only in the contract node's and the change-request node's footprints, and the skill tells reviewers to
treat any other task's diff to a contract file as a contract change. A mechanical guard is **O1**.

**F11 (Gap): cost.** Every broken edge adds two tasks, so the spend and the number of simultaneous runs
rise (`max_parallel` default 20 in the project config; the job cap is the owner's, set elsewhere). The
pass adds no new way to spend: contract and integration tasks are ordinary tasks under the same caps, the
same "tell the user the number of runs and the worst case, get their yes" rule, and the same `/jobmax`
opt-in. The planner output reports the added task count next to the gain.

**F12 (Gap): one integration task per broken edge is wasteful when edges share a contract.** The spec
asks for "an integration task for every broken dependency". Two edges that share one contract C (A to B
and A to D, both broken through C) need one integration task that depends on A, B and D, not two.
**D1817** resolves it: one integration node per contract; every broken edge has an integration node that
depends on both of its endpoints, which satisfies the spec's acceptance.

**F13 (Gap): Hotfix, Critical and the prose-only rules.** "Hotfix: usually skip" and "Critical: decouple
only with fully specified contracts and strong integration tests" are judgement rules. Only the first is
mechanical (the resolved mode is read from `rigor.Resolve`). The second is skill text, plus a
requirement that the integration node of a Critical-mode plan names its end-to-end test in the task text.
No command can check that a contract is "fully specified". **D1818** records it as prose by design.

**F14 (Open, UNVERIFIED): the post-review triage personas.** The spec says they "should follow the same
rule". `git grep -i persona` over `docs`, `plugin` and `internal` finds no triage personas; they are part
of the owner's own setup. They cannot be reconciled or shipped from here. The general principle (what we
learn while dogfooding becomes a built-in skill or recipe where it applies to everyone; anything specific
to our own projects stays in project settings) is recorded in the README and in the skill's maintenance
note, with no task. See **O2**.

**F15 (Exists): a "short contract doc in the repo" and "commit contracts so every agent reads them"
needs no mechanism.** A run checks out the repository at its base ref, so a contract that has merged is
read by every task launched after it. The mechanism needed is only the ordering the graph edge already
gives (contract before consumers). Tasks launched *before* the merge are the change-request case (F8).

**F16 (Rule): the plugin tests tie the skill to the CLI.** The skill text may name a command or flag only
if the CLI has it, so the graph and `plan graph` changes must land before the skill section that teaches
them. The skill header carries `fugaro-version`, and plugin skills are "Fugaro-owned, do not edit": the
tuning is therefore a setting the skill tells the agent to *read* with `fugaro settings --json`, never an
edit of the skill.

**F17 (Rule): release notes.** CLAUDE.md requires `### For operators` in the release notes for any step
after a deploy. The only step here is the one `fugaro update` performs (the plugin pin, the new setting's
skew gate). The entry is "run `fugaro update`" plus what it asks.

## 3. Decisions

Answered by the owner on 2026-11-08, in this conversation. `(rec)` is the recommendation.

| ID | Question | Recommendation and reason | Alternative and its cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D1810** | Where does the built-in pass live? | **Extend `parallelism`** (one section, one reference file): keeps the five-skill set and its tests; the skill is 89 of 400 lines | New sixth skill: cleaner split, changes `TestSkillSetIsFive`, the plugin README and "five skills" docs | **Extend `parallelism`** (rec), 2026-11-08: no change to the pinned set |
| **D1811** | Does the graph artifact learn contract and integration tasks? | **Schema 3**: node `kind` (`task`, `contract`, `integration`, `contract_change`), a document-level `decoupling` record of each broken edge, node `stubs` and `size`. The CLI computes the before/after paths itself | No schema change: contracts are ordinary nodes by convention; broken-versus-kept and gain stay prose no command checks | **Schema 3** (rec), 2026-11-08: the CLI, not the agent, does the arithmetic |
| **D1812** | How do tasks already launched against an old contract get the amended one? | **A plan node plus follow-up runs**: the change is a node of kind `contract_change`; unlaunched dependents get an edge on it; launched ones get a `fugaro run --pr N` follow-up that names the contract file and asks the run to merge the base and conform | Cancel and relaunch (throws away work); wait for the orchestrator tick (blocks on unmerged O5/O7) | **Plan node plus follow-up runs** (rec), 2026-11-08: no daemon needed, no work thrown away |
| **D1813** | What is the tuning surface? | **The rigor mode plus one setting `planning.decoupling` on/off at project and repo.** No per-task off, because a per-task value can only raise | Full per-area and per-task switch (needs a lowering rule rigor refuses); rigor only (no off switch) | **Rigor mode plus one on/off setting** (rec), 2026-11-08: keeps raise-only precedence |
| **D1814** | Where are change requests recorded? | **In the plan's graph document**, a `contract_changes` list: git-tracked, reviewed with the plan, read by `plan graph` | `fugaro backlog` (not reviewed with the plan); the contract doc's own log (not machine-readable) | **In the graph document** (rec), 2026-11-08: lives with the plan it concerns |
| **D1815** | How are stubs identified so "none remain" is checkable? | **Stubs live in whole files the contract names**; integration deletes them; `plan graph` reports which still exist. No marker comments | A greppable marker comment (works in mixed files; discouraged by the comment rule) | **Stubs in files the contract names** (rec), 2026-11-08: fits the comment rule, checkable by path |
| **D1816** | What is "gain"? | **Weighted path, and break an edge only if it shrinks.** Nodes get optional relative `size` (default 1); the CLI reports the path in tasks and weighted, before and after, plus the added task count | Task count only (misleading, F5); report both but never gate | **Weighted path, break only if shorter** (rec), 2026-11-08: the task-count path shows a loss for one edge |

Resolutions with one defensible answer (not questions):

| ID | Resolution |
| --- | --- |
| **D1817** | One integration node per contract, covering every edge the contract broke (F12). Each broken edge still has an integration node that depends on both of its endpoints. |
| **D1818** | "Critical: fully specified contracts and strong integration tests" is skill prose and a task-text requirement (the integration node names its end-to-end test), not a machine check (F13). "Hotfix: usually skip" is the resolved mode read from `rigor.Resolve` of the plan's footprint paths; the skill skips the pass in Hotfix. |
| **D1819** | The contract task is launched with `fugaro run --mode <highest consumer mode>`; the flag is raise-only, which is the spec's "at least the parent's rigor". Security-relevant contracts are `opus + mandatory independent review` (F10). |
| **D1820** | The decoupling pass adds no spend path (F11): contract and integration tasks are ordinary tasks under the same caps and the same go-ahead rule; the planner output states the added task count. |
| **D1821** | The skill never parses the graph: the CLI does (the same rule as docs/review-levels-ab-jobmax G9: plan text is untrusted, `fugaro plan graph` validates it). Stub paths are validated as literal repo-relative paths (F9). |
| **D1822** | First slice is skill-only on schema 2 (contract and integration tasks as ordinary nodes, the reason on each edge); schema 3 and the setting follow. Slice 1 ships something useful alone and teaches nothing the CLI cannot read. |
| **D1823** | The personas (F14) and a mechanical contract-immutability guard (F10) are out of scope; they are **O2** and **O1**. |

## 4. Open tensions and open items for the owner

No tension between two answers: D1811's schema 3 and D1813's new setting both bump what an older reader
refuses, and F7 and F4 say how `fugaro update` carries them; that is a cost, not a conflict.

- **O1 (owner): a guard that a task does not edit a merged contract.** Nothing stops it today (F10). The
  smallest guard is a finalize-time check of the run's changed files against the contract files of the
  plan, refused unless the run's own plan task is a `contract_change`. It needs the run to know its plan
  task's kind and the contracts of its plan, so it is a new runner behaviour and a new trust input. Not
  planned; say if you want it designed.
- **O2 (owner): the triage personas.** They are not in the tree (F14). To follow the principle they must
  first be written down somewhere the repo can see.
- **O3 (owner, release):** schema 3 and the new setting make a teammate on an older CLI unable to read a
  v3 graph until they run `fugaro update`. The plan treats that as the normal upgrade (F4, F7). Say if the
  graph should instead stay readable by an older CLI, which would mean carrying the new facts outside the
  artifact.

## 5. UNVERIFIED

- **U1.** Whether a `jobmax` skill exists outside `plugin/skills` (F2). The repo's `.claude/skills` holds
  only Go and release skills.
- **U2.** Whether an older runner refuses an unknown `defaults.*` key in the project layer (F7).
- **U3.** Whether the console, which reads `plan graph --json`, tolerates a document carrying
  `schema_version: 3` and the new node keys. The contract page says additive keys are ignored and an
  unknown version is refused by the CLI; no console decoder was read.
- **U4.** Whether a follow-up run's agent reliably merges the base and conforms to a named contract file.
  The plan's check 480 measures it live; it is owner-run.
