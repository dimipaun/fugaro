# J1.1 — job max in the `parallelism` skill — was already on `main`

**Nothing was implemented for this task: its deliverable was already on `main` when this run
started.** This note is the evidence, and — with the plan's own `Done` line and the one pin it
points at — the pull request's whole change. No skill text was touched.

## Where it landed

`da056ce7`, *plugin: job max in the parallelism skill (#1413)* — merged 2026-10-30, an ancestor
of this branch's base `bc920501`, and the commit line `#### J1.1` (plan line 358) names under
**Commit** with the pull request's own number appended and nothing else changed. Its diff is the
task: the three files the block's **Files** line names, and no others —
`plugin/skills/parallelism/SKILL.md`, `plugin/skills/working/SKILL.md` and
`plugin/parallelism_skill_test.go`. The pull request's second commit is its review's fixes: the
`/jobmax` exception names both of its conditions, and the `working` pointer is pinned.

Two later commits rewrote other parts of the same skill and left the job max text as #1413 wrote
it: `3cfff357` (CD1, the contract-first section and `reference/decoupling.md`) and `c96b339f`
(schema 3 graphs and `planning.decoupling`).

## The task, point by point

| J1.1's clause | state on `main` |
|---|---|
| job max is "the most completed, reviewed runs in parallel, not tokens spent; only real dependencies order runs" | `plugin/skills/parallelism/SKILL.md:33`, under the `## Job max` heading, verbatim |
| contracts first | `plugin/skills/parallelism/SKILL.md:35`, and the split bullet it points at (line 40) |
| stubs and fakes so neither side waits | `plugin/skills/parallelism/SKILL.md:35` — the name and the clause, split by the bold markers, one phrase to a reader |
| the graph kept wide and shallow | `plugin/skills/parallelism/SKILL.md:35` |
| the skill's "one yes per batch" rule stays the default | `plugin/skills/parallelism/SKILL.md:122`: the yes for the whole batch, with `/jobmax` the only exception and only where the owner has turned on the project's opt-in, and the rule standing where it is off. J1.2 owns the next edit to this line; this task does not touch it |
| the one-line pointer in `working` | `plugin/skills/working/SKILL.md:16`, in the decide-first list |
| `TestParallelismSkillDefinesJobMax` | `plugin/parallelism_skill_test.go:252` (the definition and the techniques); `TestWorkingSkillPointsAtJobMax` (line 41) holds the pointer and `TestParallelismSkillJobMaxExceptionNeedsTheOptIn` (line 57) the exception's two conditions |
| the lint, "which checks every command and flag a skill names against the live cobra tree" | `plugin/skills_lint_test.go`, run by the same command |

No command or flag the skill's job max text names is missing from the CLI: the lint's own walk
over the cobra tree is green (below), and the text names `/jobmax` only as the exception J1.2
builds and J1.3 gates. `jobmax.auto_launch` is named nowhere as existing.

## Evidence, run on this branch's base `bc920501`

- **The plan's command is green.** `GOFLAGS=-p=2 go test ./plugin -count=1` →
  `ok github.com/dimipaun/fugaro/plugin 12.925s`.
- **The plugin check is green.** `go test ./ee/plugincheck/ -count=1` →
  `ok github.com/dimipaun/fugaro/plugincheck 0.057s`.
- **The definition's pin is real.** With
  `**the most completed, reviewed runs in parallel, not tokens spent; only real dependencies
  order runs**` replaced in the skill by a shorter phrase, the test fails with
  `skills/parallelism/SKILL.md does not define job max as "..."`.
- **The three technique names are pinned, and the stub technique's clause was not.** See below.

## The one clause no test held

`TestParallelismSkillDefinesJobMax` matched the techniques as bare names, and the skill bolds the
name of the stub technique, so the phrase a reader sees — *stubs and fakes so neither side
waits* — is two strings in the file: `**stubs and fakes**` and `so neither side waits`. Only the
first was matched.

Proved against its mutation, on this branch's base: with ` so neither side waits` deleted from
`plugin/skills/parallelism/SKILL.md:35`, the test as it stood was **green**
(`ok github.com/dimipaun/fugaro/plugin 0.033s`), and the same mutation against the pin this
commit adds fails with
`skills/parallelism/SKILL.md does not name the planning technique "so neither side waits"`. With
the clause back, both are green.

The clause is the technique and not a flourish: a stub written to be waited on leaves the
dependent waiting on the side the contract was meant to free, which is exactly the graph D729
defines job max against. The change is the test's list and the comment saying why the clause is
in it; the skill's text is untouched.

## What this task leaves alone

- **J1.2 and J1.3.** The `/jobmax` skill and the owner's opt-in are not started here, and
  `jobmax.auto_launch` is described nowhere as existing. J1.2's own edit to the
  one-yes-per-batch line is its own.
- **The dated snapshots.** `docs/review-levels-ab-jobmax/reconciliation.md` §2.3 F21 — read
  against `origin/main` on 2026-10-30 — ends "Three things the spec asks for are **not** in the
  skill: the term 'job max', 'stubs and fakes so neither side waits', and 'make the graph as
  wide and shallow as possible'". `da056ce7` landed all three, so the finding is a snapshot of
  the day rather than a statement about the tree. Correcting another artifact's finding is the
  owner's call, so this note records it and does not rewrite it. The folder's README status line
  and `docs/status.md`'s row are the same kind of dated snapshot, and a task's own pull request
  does not move the row.
- **The plan's own block**, beyond the `Done` line this landing never got: the **Content**,
  **Tests**, **Command** and **Red step** lines read as they were written, because that is what
  the task asked for and #1413 did it.
